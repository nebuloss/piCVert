package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"picvert/internal/config"
	"picvert/internal/security"
)

// The administration password.
//
// # IT IS OFF BY DEFAULT, AND THAT IS NOT LAZINESS
//
// What protects the admin port is that it is not reachable from outside. A
// password does not replace that and must not be read as permission to proxy
// the port to the internet — the surface behind it deletes CVs and hands out
// every private link on the service.
//
// What it does is cover the case the topology does not: somebody who wants the
// port reachable over a VPN, from another machine on the network, through a
// tunnel they leave open. Telling them "do not" is advice that gets ignored;
// giving them a password is a thing they can actually use.
//
// # THE SESSION IS SIGNED, NOT STORED
//
// No table of sessions, because the alternative to a table is arithmetic: the
// cookie carries an expiry and a signature over it, and a signature this
// process cannot reproduce is a cookie this process did not issue. Nothing to
// clean up, nothing to grow, and no way to be logged in on a service that has
// been restarted — the signing key is made at startup and never written down.
//
// That last part is deliberate. A forgotten session on a laptop somebody no
// longer has does not outlive the next deployment.

const adminCookie = "cv_admin"

// Session is what proves a login.
type Session struct {
	Secret []byte
	TTL    time.Duration
}

// issue writes a signed cookie saying when it stops being valid.
func (s *Session) issue(w http.ResponseWriter, r *http.Request) {
	until := time.Now().Add(s.TTL).Unix()
	value := fmt.Sprintf("%d.%s", until, s.sign(until))
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		MaxAge:   int(s.TTL.Seconds()),
	})
}

func (s *Session) clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: adminCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
}

func (s *Session) sign(until int64) string {
	mac := hmac.New(sha256.New, s.Secret)
	fmt.Fprintf(mac, "%d", until)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// valid reports whether a request carries a session this process issued.
func (s *Session) valid(r *http.Request) bool {
	c, err := r.Cookie(adminCookie)
	if err != nil {
		return false
	}
	stamp, signature, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	until, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || time.Now().Unix() > until {
		return false
	}
	// The expiry is INSIDE what is signed, so moving it forward invalidates the
	// signature. A cookie whose expiry could be edited is a cookie that never
	// expires.
	return hmac.Equal([]byte(s.sign(until)), []byte(signature))
}

// guardAdmin puts the login in front of everything on the admin port.
//
// One wrapper around the whole mux rather than a check per route: an admin
// interface with one unguarded route is an unguarded admin interface, and the
// route that gets forgotten is always the one added last.
func (s *Server) guardAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No password configured means no login, exactly as before. The port is
		// protected by not being reachable, which is the arrangement this
		// service is designed around.
		if s.Config.Admin.Password == "" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/login" || strings.HasPrefix(r.URL.Path, "/assets/") {
			next.ServeHTTP(w, r)
			return
		}
		if s.Session.valid(r) {
			next.ServeHTTP(w, r)
			return
		}
		// An API call gets a status it can act on; a person gets the form.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			fail(w, http.StatusUnauthorized, fmt.Errorf("not signed in"))
			return
		}
		s.Guard.Base(w, r)
		s.Guard.CSP(w, security.Admin)
		security.NoCache(w)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(loginPage("")))
	})
}

// adminLogin is the form, and the attempt.
func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	s.Guard.Base(w, r)
	s.Guard.CSP(w, security.Admin)
	security.NoCache(w)

	if r.Method == http.MethodGet {
		_, _ = w.Write([]byte(loginPage("")))
		return
	}

	// The SAME throttle the private links use. A password is a thing to guess,
	// and this is the only surface on the service where guessing gets you
	// everything at once.
	if s.Guard.Refuse(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if !config.Verify(s.Config.Admin.Password, r.PostFormValue("password")) {
		s.Guard.RecordFailure(security.ClientIP(r))
		s.Metrics.Refused()
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(loginPage("That is not the password.")))
		return
	}
	s.Guard.RecordSuccess(security.ClientIP(r))
	s.Session.issue(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) adminLogout(w http.ResponseWriter, r *http.Request) {
	s.Session.clear(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// loginPage is deliberately its own small page rather than the admin interface
// with a form on it: nothing behind the password should be fetched, drawn or
// even named before the password is right.
func loginPage(problem string) string {
	body, err := render("login.html", map[string]any{"Problem": problem})
	if err != nil {
		return "<!doctype html><title>piCVert</title><form method=post action=/login>" +
			"<input type=password name=password><button>Sign in</button></form>"
	}
	return body
}
