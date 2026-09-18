// Package server is the service around the engine: viewers, private links, the
// editor, and an admin interface on its own port.
//
//	PUBLIC (read-only)
//	  GET  /                      the creation page, or a CV when one is named
//	  GET  /p/<slug>/             the viewer, if that CV is published
//	  GET  /p/<slug>/cv.html      the page itself
//	  GET  /p/<slug>/cv.pdf       the PDF
//	  GET  /healthz
//
//	BY PRIVATE LINK (two per CV, stable)
//	  GET  /e/<token>/            the CV; an “Edit” button when the link allows
//	  GET  /e/<token>/edit/       the editor
//
//	  /api/p/<slug>/…             reading and writing, reserved to the holder of
//	                              an edit link (X-CV-Token header)
//
// There is NO account and NO password: access rests entirely on the links. On a
// public service, a password-protected surface would be the only thing here
// actually worth attacking.
//
// NOTHING IS BUILT AHEAD OF TIME. cv.json is the source of truth and the page
// is drawn from it on request, so there is no artefact that can fall out of
// step with the document — the class of fault where a CV is edited and the
// world keeps reading the previous one simply does not exist here.
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"picvert/internal/access"
	"picvert/internal/config"
	"picvert/internal/document"
	"picvert/internal/engine"
	"picvert/internal/favicon"
	"picvert/internal/lease"
	"picvert/internal/metrics"
	"picvert/internal/profiles"
	"picvert/internal/security"
	"picvert/internal/store"
	"picvert/internal/templates"
	"picvert/internal/tokens"
	"picvert/internal/turnstile"
)

// Server is everything the service is made of, in one place.
//
// Assembled by the caller rather than reached through package globals: what a
// request can touch is the list of fields below, and a test mounts a sandbox by
// building one of these rather than by setting the environment and hoping about
// import order.
type Server struct {
	Engine   *engine.Engine
	Profiles *profiles.Repository
	Store    *store.Store
	History  *store.History
	Tokens   *tokens.Store
	Access   *access.Policy
	Guard    *security.Guard
	Registry *templates.Registry
	// Leases grant one editor at a time. See lease.go.
	Leases *lease.Registry

	// Config is every setting, from a file the environment may override.
	Config config.Config
	// Metrics is what the admin page reports. Counted rather than guessed.
	Metrics *metrics.Metrics
	// Session signs administration logins; the key is made at startup and
	// never written down, so a restart signs everybody out.
	Session *Session
	// Challenge is Cloudflare's, and does nothing unless configured.
	Challenge *turnstile.Verifier

	// pages caches rendered CVs. See cache.go for what bounds it and why.
	pages *pageCache
}

// New assembles the service from its configuration.
func New(home string, cfg config.Config, version string) (*Server, error) {
	if cfg.Home != "" {
		home = cfg.Home
	}
	e := engine.New(home)
	repo := profiles.New(home)
	if cfg.DataDir != "" {
		// Stated rather than read from the environment again: the config has
		// already layered the file and the environment, and a second reading
		// here would be a second answer.
		dir := cfg.DataDir
		repo.DataDirOf = func() string { return dir }
	}
	history := store.NewHistory(e.Registry)

	leases := lease.New()
	if cfg.Editing.LeaseTTL > 0 {
		leases.TTL = cfg.Editing.LeaseTTL
	}
	if cfg.Editing.Inactivity > 0 {
		leases.Inactivity = cfg.Editing.Inactivity
	}

	return &Server{
		Config:    cfg,
		Metrics:   metrics.New(version),
		Session:   &Session{Secret: config.NewSecret(), TTL: cfg.Admin.Session},
		Challenge: turnstile.New(cfg.Turnstile.Secret),
		Engine:    e,
		Profiles:  repo,
		Store:     store.New(repo, e.Registry, history),
		History:   history,
		Tokens:    tokens.New(repo),
		Access:    access.New(),
		Guard:     security.New(),
		Registry:  e.Registry,
		Leases:    leases,
		pages:     newPageCache(),
	}, nil
}

// --- rendering --------------------------------------------------------------

// render lays a profile out, reusing the last answer while the document has not
// moved.
func (s *Server) render(p *profiles.Profile, lang string) (*cached, error) {
	file := p.DocPath(lang)
	info, err := os.Stat(file)
	if err != nil {
		return nil, fmt.Errorf("unknown CV: %q", p.Slug)
	}
	key := file
	if hit, ok := s.pages.get(key, info.ModTime()); ok {
		s.Metrics.CacheHit()
		return hit, nil
	}
	s.Metrics.CacheMiss()
	started := time.Now()

	doc, err := engine.ReadDoc(file)
	if err != nil {
		return nil, err
	}
	page, err := s.Engine.Prepare(doc, p.Dir)
	if err != nil {
		return nil, err
	}
	html, err := s.Engine.HTML(page)
	if err != nil {
		return nil, err
	}
	s.Metrics.Render(time.Since(started))
	entry := &cached{stamp: info.ModTime(), html: html, page: page}
	s.pages.put(key, entry)
	return entry, nil
}

// pdf draws the PDF of a profile, and keeps it alongside the page.
func (s *Server) pdf(p *profiles.Profile, lang string) ([]byte, error) {
	entry, err := s.render(p, lang)
	if err != nil {
		return nil, err
	}
	if entry.pdf != nil {
		return entry.pdf, nil
	}

	name := profiles.DocName(lang)
	source, _ := os.ReadFile(filepath.Join(p.Dir, name))
	data, err := s.Engine.PDF(entry.page, name, source)
	if err != nil {
		return nil, err
	}
	entry.pdf = data
	s.Metrics.PDF()
	// The entry was weighed without its PDF, which is about 300 kB of it.
	s.pages.grew(entry, len(data))
	return data, nil
}

// --- answering --------------------------------------------------------------

// fail is the one place an error becomes a response.
//
// The message is the engine's own, and it is shown rather than swallowed: the
// person holding an edit link is the person who can fix the CV that broke, and
// “something went wrong” tells them nothing they can act on. Nothing here ever
// reveals whether a profile EXISTS, which is a different question — that is
// settled before a handler is reached.
func fail(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	security.NoCache(w)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
}

func sendJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	security.NoCache(w)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) sendHTML(w http.ResponseWriter, r *http.Request, kind security.Kind, body string) {
	s.Guard.Base(w, r)
	s.Guard.CSP(w, kind)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	security.NoCache(w)
	_, _ = w.Write([]byte(body))
}

// sendPage answers with a rendered CV.
func (s *Server) sendPage(w http.ResponseWriter, r *http.Request, p *profiles.Profile, lang string) {
	entry, err := s.render(p, lang)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	s.sendHTML(w, r, security.CV, entry.html)
}

// sendPDF answers with the PDF of a CV.
func (s *Server) sendPDF(w http.ResponseWriter, r *http.Request, p *profiles.Profile, lang string) {
	data, err := s.pdf(p, lang)
	if err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	s.Guard.Base(w, r)
	w.Header().Set("Content-Type", "application/pdf")
	// Inline unless a download is asked for: a CV is something to look at, and
	// a file in the downloads folder is a worse way to look at something than a
	// tab is.
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("%s; filename=%q", disposition, filename(p, lang)))
	security.NoCache(w)
	_, _ = w.Write(data)
}

func filename(p *profiles.Profile, lang string) string {
	if lang != "" {
		return fmt.Sprintf("cv-%s-%s.pdf", p.Slug, lang)
	}
	return fmt.Sprintf("cv-%s.pdf", p.Slug)
}

// sendPhoto answers with a profile's portrait.
func (s *Server) sendPhoto(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
	path := p.PhotoPath()
	if path == "" {
		fail(w, http.StatusNotFound, fmt.Errorf("this CV has no portrait"))
		return
	}
	s.Guard.Base(w, r)
	security.NoCache(w)
	http.ServeFile(w, r, path)
}

// --- routing ----------------------------------------------------------------

// Handler is the public surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, map[string]any{"ok": true, "profiles": s.Profiles.Count()})
	})

	// Crawlers are kept off the private surface, and off the artefacts.
	//
	// THIS IS A PRIVACY MEASURE, not housekeeping. A /e/<token> address that
	// reaches a crawler is a CV in a search index, and the link was the only
	// thing keeping it private — the person who shared it with one recruiter
	// would have shared it with everyone. Nothing links to those addresses, but
	// a browser extension, a referrer or a pasted URL is enough, and the cost of
	// saying so is four lines.
	//
	// Published CVs are deliberately left crawlable: publishing one is an
	// explicit request to be found.
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /e/\nDisallow: /api/\n"))
	})

	icon := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(favicon.SVG(favicon.Public))
	}
	mux.HandleFunc("GET /favicon.ico", icon)
	mux.HandleFunc("GET /favicon.svg", icon)

	// The published surface. EVERY route under it asks the access policy first,
	// and the policy is stated in one place — a second copy of the reasoning
	// would eventually disagree with the first, and a CV would then be readable
	// while the admin reported it as private.
	mux.HandleFunc("GET /p/{slug}/{$}", s.publicRoute(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		s.sendViewer(w, r, viewerData{Base: "/p/" + p.Slug, Profile: p, Lang: lang(r)})
	}))
	mux.HandleFunc("GET /p/{slug}/cv.html", s.publicRoute(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		s.sendPage(w, r, p, lang(r))
	}))
	mux.HandleFunc("GET /p/{slug}/cv.pdf", s.publicRoute(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		s.sendPDF(w, r, p, lang(r))
	}))
	mux.HandleFunc("GET /p/{slug}/photo", s.publicRoute(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		s.sendPhoto(w, r, p)
	}))
	mux.HandleFunc("GET /p/{slug}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/p/"+r.PathValue("slug")+"/", http.StatusMovedPermanently)
	})

	// The private surface. The throttle lives INSIDE these handlers rather than
	// in front of them: a link is checked first, and only a bad one counts
	// against the address. See Guard.Refuse.
	mux.HandleFunc("GET /e/{token}/", s.shareRoute)
	mux.HandleFunc("GET /e/{token}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/e/"+r.PathValue("token")+"/", http.StatusFound)
	})

	mux.HandleFunc("/api/", s.apiRoute)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", assetHandler(s.Guard)))

	// Making a CV, when a challenge is configured. See newcv.go for why that
	// condition is the feature rather than a setting.
	mux.HandleFunc("POST /api/new", s.publicNew)

	mux.HandleFunc("GET /{$}", s.home)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Metrics.Request()
		s.Guard.Base(w, r)
		mux.ServeHTTP(w, r)
	})
}

func lang(r *http.Request) string { return r.URL.Query().Get("lang") }

// publicRoute wraps a handler with the publication rule.
//
// An unpublished CV answers exactly as a missing one does. Distinguishing them
// would turn the address bar into an oracle for which CVs exist, and the names
// of the people who wrote them are half of what a slug is.
func (s *Server) publicRoute(h func(http.ResponseWriter, *http.Request, *profiles.Profile)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if !s.Config.IsPublic(slug) {
			fail(w, http.StatusNotFound, fmt.Errorf("unknown CV"))
			return
		}
		p, err := s.Profiles.Get(slug)
		if err != nil {
			fail(w, http.StatusNotFound, fmt.Errorf("unknown CV"))
			return
		}
		h(w, r, p)
	}
}

// home is the root: a named CV, or the creation page.
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if slug := s.Config.Profile; slug != "" {
		if p, err := s.Profiles.Get(slug); err == nil {
			s.sendViewer(w, r, viewerData{Base: "/p/" + p.Slug, Profile: p, Lang: lang(r)})
			return
		}
	}
	s.sendHTML(w, r, security.Home, homePage(s.publicList(), s.Config.Turnstile.SiteKey))
}

// publicList is the CVs anyone may read, for the front page.
func (s *Server) publicList() []*profiles.Profile {
	var out []*profiles.Profile
	for _, p := range s.Profiles.List() {
		if s.Config.IsPublic(p.Slug) {
			out = append(out, p)
		}
	}
	return out
}

// shareRoute is everything reached through a private link.
//
// One handler rather than a route per path, because the token has to be
// verified BEFORE anything else happens — including before deciding which page
// was asked for. A route table with the check bolted onto each entry is a route
// table with one entry that will eventually be missing it.
func (s *Server) shareRoute(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	grant, ok := s.Tokens.Verify(token)
	if !ok {
		// Counted first, then refused. A bad link is a bad link whether or not
		// this address had already run out of patience.
		s.Guard.RecordFailure(security.ClientIP(r))
		if s.Guard.Refuse(w, r) {
			return
		}
		fail(w, http.StatusNotFound, fmt.Errorf("invalid or expired link"))
		return
	}
	// A good link clears the address. Somebody who has just proved they hold a
	// token is not somebody scanning for one, whatever their neighbours have
	// been doing.
	s.Guard.RecordSuccess(security.ClientIP(r))

	// Belt as well as braces. robots.txt is a request a crawler may ignore and
	// some do; this header is the one they honour, and it is the difference
	// between a CV shared with one recruiter and a CV in a search index.
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")

	p, err := s.Profiles.Get(grant.Slug)
	if err != nil {
		fail(w, http.StatusNotFound, fmt.Errorf("invalid or expired link"))
		return
	}

	base := "/e/" + token
	rest := strings.TrimPrefix(r.URL.Path, base)
	rest = strings.TrimPrefix(rest, "/")
	switch {
	case rest == "":
		s.sendViewer(w, r, viewerData{
			Base: base, Profile: p, Lang: lang(r),
			CanEdit: grant.Mode == tokens.Edit,
		})
	case rest == "cv.html":
		s.sendPage(w, r, p, lang(r))
	case rest == "cv.pdf":
		s.sendPDF(w, r, p, lang(r))
	case rest == "photo":
		s.sendPhoto(w, r, p)
	case rest == "info":
		links, _ := s.Tokens.ForProfile(p.Slug)
		sendJSON(w, map[string]any{
			"ok": true, "slug": p.Slug, "mode": string(grant.Mode),
			"name": nameOf(p), "languages": p.Languages(),
			"canEdit": grant.Mode == tokens.Edit,
			"read":    "/e/" + links.Read + "/",
		})
	case rest == "edit" || rest == "edit/":
		if grant.Mode != tokens.Edit {
			fail(w, http.StatusForbidden, fmt.Errorf("this link is read-only"))
			return
		}
		if rest == "edit" {
			http.Redirect(w, r, base+"/edit/", http.StatusFound)
			return
		}
		s.sendHTML(w, r, security.Admin, editorPage(base, p.Slug, token))
	default:
		fail(w, http.StatusNotFound, fmt.Errorf("unknown route"))
	}
}

func nameOf(p *profiles.Profile) string {
	doc, err := engine.ReadDoc(p.JSONPath())
	if err != nil {
		return p.Slug
	}
	if id, ok := document.Identity(doc); ok {
		if name := document.Str(id, "name"); name != "" {
			return name
		}
	}
	return p.Slug
}

// Serve runs both ports until one of them fails.
//
// The admin port is SEPARATE and has no access control at all. Its only
// protection is not being proxied, which is a deployment decision rather than a
// code one — so it must never be merged into the public handler “just for
// convenience”, and it must never grow a password, because a password-protected
// surface would be the only thing here worth attacking.
func (s *Server) Serve(addr, adminAddr string) error {
	if addr == "" {
		addr = s.Config.Listen
	}
	if adminAddr == "" {
		adminAddr = s.Config.Admin.Listen
	}
	errs := make(chan error, 2)
	go func() {
		log.Printf("piCVert on http://%s", addr)
		errs <- http.ListenAndServe(addr, s.Handler())
	}()
	if adminAddr != "" {
		go func() {
			log.Printf("piCVert admin on http://%s  —  no access control, do not proxy", adminAddr)
			errs <- http.ListenAndServe(adminAddr, s.AdminHandler())
		}()
	}
	return <-errs
}
