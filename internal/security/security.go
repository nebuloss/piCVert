// Package security is the hardening a public exposure needs.
//
// The engine was designed for a private network. Opened to the internet, four
// things are missing, and they are all here:
//
//  1. NOTHING SLOWS DOWN LINK SCANNING. That is THE risk when all access rests
//     on secret URLs: a machine can try tokens in a loop. A token is 192 bits,
//     so finding one is out of reach — but letting a stranger hammer the
//     service without limit is not. A per-address failure counter ends that,
//     and makes scanning visible in the log instead of silent.
//
//  2. THE TOKEN CAN LEAK THROUGH THE REFERER HEADER. A /e/<token> address
//     visited and then followed by an outbound link hands the whole address to
//     the third party. Referrer-Policy: no-referrer stops that outright.
//
//  3. NO CONTENT POLICY. A CV is a self-contained document: a strict policy
//     costs nothing and turns any injection into inert text, on top of the
//     escaping already done when it is drawn.
//
//  4. THE CV CAN BE FRAMED BY A THIRD PARTY. frame-ancestors prevents a site
//     from showing it in an iframe to make people click elsewhere.
package security

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind is a sort of page, each with its own content policy.
type Kind string

const (
	// CV is the document itself: inline styles, data-URI images, embedded
	// fonts, and no script whatsoever.
	CV Kind = "cv"
	// Viewer is the frame around a CV.
	Viewer Kind = "viewer"
	// Admin is the editor and the admin interface.
	Admin Kind = "admin"
	// Home is the creation page.
	Home Kind = "home"
)

// maxEntries bounds the memory: we do not track the whole internet.
const maxEntries = 10000

type attempts struct {
	count int
	until time.Time
}

// Guard is the bumper, and the source of every header.
//
// All of its settings are read on EVERY access, never frozen at construction:
// they are placed by the environment, and a value captured when this package
// happened to load would be the value of a different moment.
type Guard struct {
	mu       sync.Mutex
	failures map[string]*attempts

	WindowOf         func() time.Duration
	MaxFailuresOf    func() int
	HSTSOf           func() string
	FrameAncestorsOf func() string
}

// New reads the process environment.
func New() *Guard {
	return &Guard{
		failures:      map[string]*attempts{},
		WindowOf:      func() time.Duration { return time.Duration(envInt("PICVERT_RATE_WINDOW_MIN", 15)) * time.Minute },
		MaxFailuresOf: func() int { return envInt("PICVERT_RATE_MAX_FAILURES", 10) },
		// HSTS, deliberately WITHOUT includeSubDomains by default. Served from
		// an apex, includeSubDomains forces HTTPS on ALL subdomains for a year
		// and browsers cache it: another service on plain HTTP under a
		// subdomain would become unreachable, with no easy way back. This
		// application does not know what lives next to it — it is not its call.
		//
		// PICVERT_HSTS=off disables it (the proxy may already handle it);
		// PICVERT_HSTS=subdomains re-enables it knowingly.
		HSTSOf: func() string {
			mode := strings.ToLower(os.Getenv("PICVERT_HSTS"))
			if mode == "off" {
				return ""
			}
			value := fmt.Sprintf("max-age=%d", envInt("PICVERT_HSTS_MAX_AGE", 31536000))
			if mode == "subdomains" {
				value += "; includeSubDomains"
			}
			return value
		},
		// Who may display these pages in an iframe. 'self' compares the EXACT
		// origin: a CV served from cv.example.com cannot be framed by
		// www.example.com. The variable allows it explicitly, to embed a CV in
		// a personal site for instance.
		FrameAncestorsOf: func() string {
			if v := strings.TrimSpace(os.Getenv("PICVERT_FRAME_ANCESTORS")); v != "" {
				return v
			}
			return "'self'"
		},
	}
}

func envInt(name string, fallback int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && v > 0 {
		return v
	}
	return fallback
}

// --- attempt throttling -----------------------------------------------------

func (g *Guard) prune(now time.Time) {
	for ip, e := range g.failures {
		if !e.until.After(now) {
			delete(g.failures, ip)
		}
	}
	if len(g.failures) > maxEntries {
		// Last resort: start over rather than grow without bound.
		g.failures = map[string]*attempts{}
	}
}

// RecordFailure is called on an invalid link or a refused authentication.
func (g *Guard) RecordFailure(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	g.prune(now)
	window := g.WindowOf()
	e := g.failures[ip]
	if e == nil || !e.until.After(now) {
		g.failures[ip] = &attempts{count: 1, until: now.Add(window)}
		return
	}
	e.count++
	// A single line when the threshold is crossed: scanning must show up in the
	// log without drowning it in one line per attempt.
	if e.count == g.MaxFailuresOf() {
		log.Printf("[security] %s blocked: %d invalid links in under %d min",
			ip, e.count, int(window.Minutes()))
	}
}

// RecordSuccess is called on success: a legitimate session must not stay
// penalised.
func (g *Guard) RecordSuccess(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.failures, ip)
}

// Blocked reports whether an address has failed too often, and for how much
// longer.
func (g *Guard) Blocked(ip string) (time.Duration, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.failures[ip]
	if e == nil || !e.until.After(time.Now()) || e.count < g.MaxFailuresOf() {
		return 0, false
	}
	return time.Until(e.until), true
}

// Reset forgets every counter: for tests, and for unblocking by hand.
func (g *Guard) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failures = map[string]*attempts{}
}

// ClientIP is the address, as the throttle knows it.
//
// An address we cannot determine is bucketed under one name rather than let
// through: someone we cannot identify should not get a free pass.
func ClientIP(r *http.Request) string {
	// Only consulted when the deployment says a proxy is in front. Trusting the
	// header unconditionally would let anyone claim a fresh address per request
	// and make the throttle purely decorative.
	if os.Getenv("PICVERT_TRUST_PROXY") == "1" {
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			if first, _, ok := strings.Cut(v, ","); ok {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(v)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}

// Throttle refuses addresses that have failed too often, early.
func (g *Guard) Throttle(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if retry, blocked := g.Blocked(ClientIP(r)); blocked {
			seconds := int(retry.Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, `{"ok":false,"error":"Too many attempts. Try again in %d minute(s)."}`,
				(seconds+59)/60)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- headers ----------------------------------------------------------------

// Policy is the content policy of one kind of page.
//
// Recomposed on every read, because the frame-ancestors setting goes into it:
// frozen at load, it would keep the value the environment had at the first
// import.
func (g *Guard) Policy(kind Kind) string {
	frame := g.FrameAncestorsOf()
	switch kind {
	case CV:
		// A self-contained document: inline styles, data-URI images, embedded
		// fonts, zero script.
		//
		// `font-src data:` is NOT optional decoration. The page carries its
		// typefaces as data URIs so that it renders the same everywhere;
		// without this directive `default-src 'none'` blocks them, the browser
		// falls back to whatever is installed, and a CV that fits its page by
		// less than a line is silently cut off. The file was correct and the
		// DELIVERY undid it, which is the hardest kind of fault to see: opening
		// the same file from disk works, because the policy travels in a header.
		return "default-src 'none'; img-src data:; style-src 'unsafe-inline'; " +
			"font-src data:; base-uri 'none'; form-action 'none'; frame-ancestors " + frame
	case Viewer:
		// The chrome around a CV: our own script and stylesheet, the page
		// itself in a same-origin frame, and nothing from anywhere else.
		//
		// NO `'unsafe-inline'` FOR SCRIPT. It was there while the viewer
		// carried its behaviour in a <script> block, and it is exactly the
		// directive that makes a content policy decorative: an injected script
		// in a CV field would have run. The behaviour is a module now, so the
		// policy can say what it means.
		return "default-src 'none'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; " +
			"script-src 'self'; connect-src 'self'; frame-src 'self'; " +
			"font-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors " + frame
	case Admin:
		// The editor: assets served by us, never a third-party origin. Its
		// preview pane draws the page in an iframe that inherits THIS policy,
		// so it needs the fonts too — otherwise what one types is measured in a
		// font the finished CV will not use, and the fit shown while typing is
		// not the fit one gets.
		return "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; " +
			"script-src 'self'; connect-src 'self'; frame-src 'self'; " +
			"font-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors " + frame
	default:
		return "default-src 'none'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; " +
			"script-src 'self'; connect-src 'self'; " +
			"base-uri 'none'; form-action 'none'; frame-ancestors " + frame
	}
}

// Base sets the headers every response carries.
func (g *Guard) Base(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The token lives in the URL: no Referer may leave with it.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")

	// These only mean anything in a secure context. Sent over plain HTTP they
	// protect nothing and make the browser console complain that the origin was
	// untrustworthy, so they go out only when the request actually arrived over
	// HTTPS — directly, or through a proxy that says so.
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		if hsts := g.HSTSOf(); hsts != "" {
			w.Header().Set("Strict-Transport-Security", hsts)
		}
	}
}

// CSP sets the content policy of a kind of page.
func (g *Guard) CSP(w http.ResponseWriter, kind Kind) {
	w.Header().Set("Content-Security-Policy", g.Policy(kind))
}

// NoCache marks a response as not cacheable.
//
// HTML pages carry the interface code and change on every deployment. Without
// this a browser keeps serving the old version after a fix — and one then
// believes one is debugging the new code while looking at the old. They weigh a
// few kilobytes: there is nothing to gain by caching them. The CV and the PDF
// keep their own caching.
func NoCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
}
