package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"net"

	"picvert/internal/document"
	"picvert/internal/engine"
	"picvert/internal/favicon"
	"picvert/internal/profiles"
	"picvert/internal/security"
	"picvert/internal/tokens"
)

// AdminHandler is the administration interface — on a SEPARATE port,
// deliberately.
//
// Its protection does not come from a password but from the topology: this port
// is not proxied to the internet. The public port therefore exposes NO
// authenticated surface at all, and there is nothing to guess anywhere.
//
//	public port  ->  the CVs, and the private links /e/<token>
//	admin port   ->  list, view, delete CVs and their links
//
// NEVER to be proxied outwards: this interface has no access control, by
// construction. Do not add a password to it either — that would make it the one
// thing here actually worth attacking.
func (s *Server) AdminHandler() http.Handler {
	mux := http.NewServeMux()

	// Amber, where the public surface is green. Both are open at once, on the
	// same host and two ports, and only one of them deletes CVs.
	icon := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(favicon.SVG(favicon.Admin))
	}
	mux.HandleFunc("GET /favicon.ico", icon)
	mux.HandleFunc("GET /favicon.svg", icon)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", assetHandler(s.Guard)))

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		body, err := render("admin.html", map[string]any{
			"PublicURL":  s.Config.Domain,
			"PublicPort": publicPort(s.Config.Listen),
		})
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		s.sendHTML(w, r, security.Admin, body)
	})

	mux.HandleFunc("GET /api/profiles", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, s.inventory(r))
	})

	// Making a CV is an ADMIN act, not a public one. A public creation route is
	// a route strangers fill a disk through, and it needs a quota, a rate limit
	// and a challenge before it is safe — none of which this service has. Until
	// it does, new CVs come from the port that is not proxied outwards.
	mux.HandleFunc("POST /api/profiles", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Slug     string `json:"slug"`
			Name     string `json:"name"`
			Template string `json:"template"`
			Lang     string `json:"lang"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if _, err := s.Store.Create(body.Slug, body.Name, body.Template, body.Lang); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		p, err := s.Profiles.Get(body.Slug)
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		// The links come back with the creation. They are the only way into the
		// CV that was just made, and a second call to fetch them is a second
		// chance to end up with a CV nobody can open.
		sendJSON(w, map[string]any{"ok": true, "profile": s.describeProfile(p)})
	})

	mux.HandleFunc("GET /api/templates", func(w http.ResponseWriter, r *http.Request) {
		all, err := s.Registry.All()
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		out := make([]map[string]any, 0, len(all))
		for _, t := range all {
			out = append(out, map[string]any{
				"uuid": t.UUID, "name": t.Name, "title": t.Title,
				"description": t.Description,
			})
		}
		sendJSON(w, map[string]any{"ok": true, "templates": out})
	})

	mux.HandleFunc("GET /view/{slug}/cv.html", s.adminProfile(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		s.sendPage(w, r, p, lang(r))
	}))
	mux.HandleFunc("GET /view/{slug}/cv.pdf", s.adminProfile(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		s.sendPDF(w, r, p, lang(r))
	}))

	mux.HandleFunc("POST /api/p/{slug}/links/rotate", s.adminProfile(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		mode := tokens.Mode(r.URL.Query().Get("mode"))
		links, err := s.Tokens.Rotate(p.Slug, mode)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		sendJSON(w, map[string]any{"ok": true, "links": s.describeLinks(links)})
	}))

	mux.HandleFunc("DELETE /api/p/{slug}", s.adminProfile(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		if err := s.Trash(p); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		sendJSON(w, map[string]any{"ok": true, "slug": p.Slug})
	}))

	mux.HandleFunc("GET /api/trash", func(w http.ResponseWriter, r *http.Request) {
		sendJSON(w, map[string]any{"ok": true, "entries": s.TrashList()})
	})

	// What the service has been doing. Counted rather than guessed, and shown
	// on the page the administrator already has open — see internal/metrics for
	// why each number is there.
	mux.HandleFunc("GET /api/metrics", func(w http.ResponseWriter, r *http.Request) {
		snapshot := s.Metrics.Snapshot()
		profiles := s.Profiles.List()
		var bytes int64
		for _, p := range profiles {
			bytes += dirSize(p.Dir)
		}
		sendJSON(w, map[string]any{
			"ok": true, "metrics": snapshot,
			"profiles":   len(profiles),
			"bytes":      bytes,
			"cacheBytes": s.cacheBytes(),
			"cacheCount": s.cacheSize(),
			"editing":    s.Leases.Count(),
			"domain":     s.Config.Domain,
			"policy":     s.Config.DescribeAccess(),
			"guarded":    s.Config.Admin.Password != "",
			"challenge":  s.Config.Turnstile.SiteKey != "",
			"limits": map[string]any{
				"profileMB": s.Config.Limits.MaxProfileMB,
				"freeMB":    s.Config.Limits.MinFreeMB,
			},
		})
	})

	mux.HandleFunc("POST /api/trash/{slug}/restore", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Restore(r.PathValue("slug")); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		sendJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("DELETE /api/trash/{slug}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Purge(r.PathValue("slug")); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		sendJSON(w, map[string]any{"ok": true})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusNotFound, fmt.Errorf("unknown route"))
	})
	mux.HandleFunc("/login", s.adminLogin)
	mux.HandleFunc("GET /logout", s.adminLogout)

	// Everything above is behind the password, when there is one. Wrapped as a
	// whole rather than per route: an admin interface with one unguarded route
	// is an unguarded admin interface.
	return s.guardAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Guard.Base(w, r)
		mux.ServeHTTP(w, r)
	}))
}

func (s *Server) adminProfile(h func(http.ResponseWriter, *http.Request, *profiles.Profile)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.Profiles.Get(r.PathValue("slug"))
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		h(w, r, p)
	}
}

// publicPort is the port the CVs are served on, for a page that is not on it.
//
// The administration interface has to build addresses on the OTHER port: a
// private link is stored as a bare path whenever no domain is configured, and
// a bare path followed from this page resolves against this port, where
// /e/<token>/ does not exist.
//
// Taken from the configuration rather than assumed to be 3000. The default is
// 3000 and guessing it would be right almost always — and silently wrong for
// anybody who moved it, which is the kind of "almost" that costs an afternoon.
func publicPort(listen string) string {
	if _, port, err := net.SplitHostPort(listen); err == nil && port != "" {
		return port
	}
	return "3000"
}

// --- the inventory ----------------------------------------------------------

const pageSize = 25
const maxPageSize = 200

// inventory is what is in the data directory, searchable and paginated.
//
// THE ORDER OF OPERATIONS IS THE POINT. Listing is cheap — one name read per CV
// — while describing one is not: it weighs the folder and touches the link
// store. So we FILTER, then SLICE, then describe only what the page shows.
// Describing everything before dropping most of it would make the interface
// slower the more CVs there are, which is exactly when it needs to stay usable.
func (s *Server) inventory(r *http.Request) map[string]any {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	limit := clampQuery(r, "limit", pageSize, 1, maxPageSize)

	all := s.Profiles.List()
	matched := all
	if q != "" {
		matched = nil
		for _, p := range all {
			if strings.Contains(p.Slug, q) || strings.Contains(strings.ToLower(p.Name), q) {
				matched = append(matched, p)
			}
		}
	}
	pageCount := max(1, (len(matched)+limit-1)/limit)
	page := clampQuery(r, "page", 1, 1, pageCount)
	start := (page - 1) * limit
	end := min(start+limit, len(matched))
	if start > end {
		start = end
	}

	described := make([]map[string]any, 0, end-start)
	for _, p := range matched[start:end] {
		described = append(described, s.describeProfile(p))
	}

	defaultSlug := ""
	if d := s.Profiles.Default(); d != nil {
		defaultSlug = d.Slug
	}
	return map[string]any{
		"ok":        true,
		"publicUrl": s.Config.Domain,
		"policy":    s.Config.DescribeAccess(),
		"default":   defaultSlug,
		"total":     len(matched),
		"page":      page,
		"pages":     pageCount,
		"profiles":  described,
	}
}

func clampQuery(r *http.Request, key string, fallback, low, high int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		v = fallback
	}
	return min(high, max(low, v))
}

func (s *Server) describeProfile(p *profiles.Profile) map[string]any {
	links, _ := s.Tokens.ForProfile(p.Slug)
	// Read here rather than trusted from the profile: List() fills in the name
	// and Get() does not, so a profile that arrived by either route describes
	// itself the same way. It came back blank from the creation route once,
	// which is the moment the name matters most.
	name := p.Name
	if name == "" {
		name = nameOf(p)
	}
	out := map[string]any{
		"slug":      p.Slug,
		"name":      name,
		"public":    s.Config.IsPublic(p.Slug),
		"languages": p.Languages(),
		"bytes":     dirSize(p.Dir),
		"history":   s.History.Size(p),
		"links":     s.describeLinks(links),
	}
	if info, err := os.Stat(p.JSONPath()); err == nil {
		out["updatedAt"] = info.ModTime().UTC().Format(time.RFC3339)
	}
	s.describeDocument(p, out)
	return out
}

// describeDocument says what the CV IS, and whether it can be read at all.
//
// The inventory used to report a size and a date for every CV and nothing
// about its contents, which made a corrupt document look exactly like a
// healthy one — the row is the same width whether the file parses or not. The
// one moment somebody scans this page is when something is wrong, and it was
// the one question the page could not answer.
//
// Read once, here, rather than by each caller: this is already paid for by
// nameOf on the same document, and the page shows twenty-five of them.
func (s *Server) describeDocument(p *profiles.Profile, out map[string]any) {
	doc, err := engine.ReadDoc(p.JSONPath())
	if err != nil {
		// Named rather than swallowed. "unreadable" with no reason is a report
		// that sends somebody to the shell to find out what this already knows.
		out["ok"] = false
		out["problem"] = err.Error()
		return
	}
	out["ok"] = true
	out["sections"] = len(document.Sections(doc))
	if meta := document.Meta(doc); meta != nil {
		// Resolved to its title, not left as the UUID the document carries. A
		// document references its template by a stable identifier precisely so
		// that renaming one does not break it — which makes the identifier the
		// wrong thing to show a person, who has never seen it before and
		// cannot tell two apart.
		ref := document.Str(meta, "template")
		if t := s.Registry.Find(ref); t != nil && t.Title != "" {
			out["template"] = t.Title
		} else if t != nil {
			out["template"] = t.Name
		} else if ref != "" {
			// Unresolved is worth showing rather than hiding: it means the
			// template this CV was written for is not installed, and the page
			// it renders will not be the page its author last saw.
			out["template"] = ref
			out["templateMissing"] = true
		}
	}
	if id, ok := document.Identity(doc); ok {
		out["role"] = document.Str(id, "role")
		out["photo"] = document.Str(id, "photo") != ""
	}
}

// describeLinks turns two tokens into two addresses.
//
// The tokens are shown IN FULL. That is the whole point of this interface: a
// stable link one cannot be shown again is a link that has to be reissued every
// time it is mislaid, and reissuing is what breaks every copy already handed
// out.
func (s *Server) describeLinks(l tokens.Links) map[string]any {
	// Through the config's own builder, so every link this service hands out is
	// spelt the same way — and so a domain set once produces real addresses
	// everywhere rather than bare paths in half of them.
	return map[string]any{
		"edit":      s.Config.LinkTo("/e/" + l.Edit + "/edit/"),
		"read":      s.Config.LinkTo("/e/" + l.Read + "/"),
		"createdAt": l.CreatedAt,
	}
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// --- deleting, with a way back ----------------------------------------------
//
// A CV is deleted from the editing interface, by whoever holds its link — which,
// for a self-service CV, means its author and nobody else. That deletion is one
// click, and what it destroys exists nowhere else: no account to recover it
// from, no copy on a server, nothing.
//
// So it is not destroyed at once. The directory is MOVED ASIDE for a grace
// period and only erased afterwards. During that window the admin interface can
// put it back exactly as it was.
//
//	data/<slug>/              the CV
//	data/.trash/<slug>/       the same directory, set aside
//	data/.trash/<slug>.json   when, and on whose account
//
// The note sits BESIDE the directory rather than inside it, so a restoration
// returns the profile as it was, with no stray file the engine would then have
// to learn to ignore.
//
// .trash is invisible to profile discovery for free: a slug must begin with a
// letter or a digit, so a name beginning with a dot can never be taken for a CV.

func (s *Server) trashDir() string { return filepath.Join(s.Profiles.DataDir(), ".trash") }

func (s *Server) graceHours() int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("PICVERT_TRASH_HOURS"))); err == nil && v >= 0 {
		return v
	}
	return 24
}

// Trash sets a CV aside.
func (s *Server) Trash(p *profiles.Profile) error {
	if err := os.MkdirAll(s.trashDir(), 0o700); err != nil {
		return err
	}
	target := filepath.Join(s.trashDir(), p.Slug)
	_ = os.RemoveAll(target)
	if err := os.Rename(p.Dir, target); err != nil {
		return err
	}
	note := map[string]any{
		"slug": p.Slug, "name": p.Name,
		"deletedAt": time.Now().UTC().Format(time.RFC3339),
	}
	writeJSON(target+".json", note)
	// The links go with it. Left in place, a token would still identify a slug
	// whose directory is gone, and every route trusting it would then fail on a
	// read instead of answering cleanly that the link is invalid.
	s.Tokens.Forget(p.Slug)
	s.forget(p)
	return nil
}

// TrashEntry is one CV set aside, and how long it can still be caught.
type TrashEntry struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	DeletedAt string `json:"deletedAt"`
	ExpiresAt string `json:"expiresAt"`
	Bytes     int64  `json:"bytes"`
}

// TrashList is what can still be restored, and sweeps up what cannot.
//
// Swept HERE rather than on a timer: a service that is never looked at is a
// service with nobody to notice a timer that stopped, and this is the one place
// that has a reason to know the grace period has passed.
func (s *Server) TrashList() []TrashEntry {
	entries, err := os.ReadDir(s.trashDir())
	if err != nil {
		return nil
	}
	grace := time.Duration(s.graceHours()) * time.Hour
	var out []TrashEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(s.trashDir(), e.Name())
		note := map[string]any{}
		readJSON(dir+".json", &note)
		deleted, _ := time.Parse(time.RFC3339, str(note, "deletedAt"))
		if deleted.IsZero() {
			if info, err := os.Stat(dir); err == nil {
				deleted = info.ModTime()
			}
		}
		expires := deleted.Add(grace)
		if time.Now().After(expires) {
			_ = os.RemoveAll(dir)
			_ = os.Remove(dir + ".json")
			continue
		}
		name := str(note, "name")
		if name == "" {
			name = e.Name()
		}
		out = append(out, TrashEntry{
			Slug: e.Name(), Name: name,
			DeletedAt: deleted.UTC().Format(time.RFC3339),
			ExpiresAt: expires.UTC().Format(time.RFC3339),
			Bytes:     dirSize(dir),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt > out[j].DeletedAt })
	return out
}

// Restore puts a CV back exactly where it was.
func (s *Server) Restore(slug string) error {
	if err := profiles.CheckSlug(slug); err != nil {
		return err
	}
	source := filepath.Join(s.trashDir(), slug)
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("nothing set aside under %q", slug)
	}
	p, err := s.Profiles.For(slug)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p.Dir); err == nil {
		return fmt.Errorf("a CV already exists under %q", slug)
	}
	if err := os.MkdirAll(filepath.Dir(p.Dir), 0o700); err != nil {
		return err
	}
	if err := os.Rename(source, p.Dir); err != nil {
		return err
	}
	_ = os.Remove(source + ".json")
	return nil
}

// Purge destroys a CV for good, before its grace period is up.
func (s *Server) Purge(slug string) error {
	if err := profiles.CheckSlug(slug); err != nil {
		return err
	}
	dir := filepath.Join(s.trashDir(), slug)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("nothing set aside under %q", slug)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	_ = os.Remove(dir + ".json")
	return nil
}
