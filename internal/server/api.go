package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"picvert/internal/document"
	"picvert/internal/engine"
	"picvert/internal/fields"
	"picvert/internal/profiles"
	"picvert/internal/security"
	"picvert/internal/templates"
	"picvert/internal/tokens"
)

// grantKey carries what a verified link allows, on the request.
//
// On the CONTEXT rather than passed along by hand: every handler past the check
// needs it, and threading it through signatures is how one handler ends up
// reading a profile the link never covered.
type grantKey struct{}

func grantOf(r *http.Request) tokens.Grant {
	g, _ := r.Context().Value(grantKey{}).(tokens.Grant)
	return g
}

// maxBody is the largest request accepted. A CV is a few kilobytes; a portrait
// is the only thing here with any size, and it has its own limit.
const maxBody = 8 << 20

// apiRoute checks the link, then hands over.
//
// The check happens ONCE, here, for everything under /api. A per-route check is
// a check that will eventually be missing from a route.
func (s *Server) apiRoute(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-CV-Token")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	grant, ok := s.Tokens.Verify(token)
	if !ok {
		s.Guard.RecordFailure(security.ClientIP(r))
		fail(w, http.StatusUnauthorized, fmt.Errorf("invalid or expired link"))
		return
	}
	s.Guard.RecordSuccess(security.ClientIP(r))

	// A read link may read. Anything that changes a CV needs the other one, and
	// it is refused HERE rather than merely hidden in the interface: a button
	// that is not drawn is not a permission.
	if r.Method != http.MethodGet && grant.Mode != tokens.Edit {
		fail(w, http.StatusForbidden, fmt.Errorf("this link is read-only"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	s.api().ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), grantKey{}, grant)))
}

// api is the route table of the editor's surface.
func (s *Server) api() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/whoami", func(w http.ResponseWriter, r *http.Request) {
		grant := grantOf(r)
		sendJSON(w, map[string]any{
			"ok": true, "slug": grant.Slug, "mode": string(grant.Mode),
			"canEdit": grant.Mode == tokens.Edit,
		})
	})

	mux.HandleFunc("GET /api/templates", func(w http.ResponseWriter, r *http.Request) {
		all, err := s.Registry.All()
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		out := make([]map[string]any, 0, len(all))
		for _, t := range all {
			out = append(out, describe(t))
		}
		sendJSON(w, map[string]any{"ok": true, "templates": out})
	})

	mux.HandleFunc("GET /api/p/{slug}", s.owned(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		doc, err := s.Store.Read(p.Slug, lang(r))
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		tpl, err := s.Registry.ForDocument(doc)
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		sendJSON(w, map[string]any{
			"ok": true, "slug": p.Slug, "doc": doc,
			"template":  describe(tpl),
			"languages": p.Languages(),
			"fit":       s.fitOf(p, lang(r)),
		})
	}))

	mux.HandleFunc("PUT /api/p/{slug}", s.write(func(r *http.Request, p *profiles.Profile, body any) (document.Doc, error) {
		return s.Store.Write(p.Slug, body, lang(r))
	}))

	mux.HandleFunc("PATCH /api/p/{slug}/identity", s.patch(func(r *http.Request, p *profiles.Profile, patch map[string]any) (document.Doc, error) {
		return s.Store.UpdateIdentity(p.Slug, patch, lang(r))
	}))

	mux.HandleFunc("PATCH /api/p/{slug}/meta", s.patch(func(r *http.Request, p *profiles.Profile, patch map[string]any) (document.Doc, error) {
		return s.Store.UpdateMeta(p.Slug, patch, lang(r))
	}))

	mux.HandleFunc("PUT /api/p/{slug}/template", s.patch(func(r *http.Request, p *profiles.Profile, patch map[string]any) (document.Doc, error) {
		return s.Store.SetTemplate(p.Slug, document.Str(patch, "template"), lang(r))
	}))

	mux.HandleFunc("PUT /api/p/{slug}/sections/order", s.patch(func(r *http.Request, p *profiles.Profile, patch map[string]any) (document.Doc, error) {
		order, err := intList(patch, "order")
		if err != nil {
			return nil, err
		}
		return s.Store.ReorderSections(p.Slug, order, lang(r))
	}))

	mux.HandleFunc("PUT /api/p/{slug}/sections/{id}/order", s.patch(func(r *http.Request, p *profiles.Profile, patch map[string]any) (document.Doc, error) {
		order, err := intList(patch, "order")
		if err != nil {
			return nil, err
		}
		return s.Store.ReorderEntries(p.Slug, r.PathValue("id"), order, lang(r))
	}))

	mux.HandleFunc("PUT /api/p/{slug}/sections/{id}", s.patch(func(r *http.Request, p *profiles.Profile, patch map[string]any) (document.Doc, error) {
		return s.Store.UpdateSection(p.Slug, r.PathValue("id"), patch, lang(r))
	}))

	mux.HandleFunc("GET /api/p/{slug}/fit", s.owned(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		sendJSON(w, map[string]any{"ok": true, "fit": s.fitOf(p, lang(r))})
	}))

	mux.HandleFunc("GET /api/p/{slug}/history", s.owned(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		_, filter := r.URL.Query()["lang"]
		sendJSON(w, map[string]any{
			"ok": true, "entries": s.History.List(p, 200, lang(r), filter)})
	}))

	// The live preview: a document that has NOT been saved, laid out as it
	// would be. This is the hot path — it runs on every pause in typing — and it
	// is the reason the layout is one pass rather than two.
	mux.HandleFunc("POST /api/p/{slug}/preview", s.owned(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		var body any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		doc, ok := document.AsObject(document.Upgrade(body))
		if !ok {
			fail(w, http.StatusBadRequest, fmt.Errorf("a CV must be a JSON object"))
			return
		}
		page, err := s.Engine.Prepare(doc, p.Dir)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		html, err := s.Engine.HTML(page)
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		sendJSON(w, map[string]any{"ok": true, "html": html, "fit": fitOf(page)})
	}))

	mux.HandleFunc("GET /api/p/{slug}/languages", s.owned(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		sendJSON(w, map[string]any{"ok": true, "languages": p.Languages()})
	}))

	mux.HandleFunc("POST /api/p/{slug}/languages", s.patch(func(r *http.Request, p *profiles.Profile, patch map[string]any) (document.Doc, error) {
		return s.Store.AddLanguage(p.Slug, document.Str(patch, "lang"), document.Str(patch, "from"))
	}))

	mux.HandleFunc("DELETE /api/p/{slug}/languages/{lang}", s.owned(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		if err := s.Store.RemoveLanguage(p.Slug, r.PathValue("lang")); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		s.forget(p)
		sendJSON(w, map[string]any{"ok": true, "languages": p.Languages()})
	}))

	mux.HandleFunc("GET /api/p/{slug}/photo", s.owned(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		s.sendPhoto(w, r, p)
	}))

	mux.HandleFunc("POST /api/p/{slug}/photo", s.owned(s.uploadPhoto))

	mux.HandleFunc("GET /api/p/{slug}/links", s.owned(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		links, err := s.Tokens.ForProfile(p.Slug)
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		// Only the read link is ever handed back through an edit link. The edit
		// token is what the caller already holds, and echoing it would put it
		// into every log and cache between here and the browser a second time
		// for nothing.
		sendJSON(w, map[string]any{"ok": true, "read": "/e/" + links.Read + "/"})
	}))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusNotFound, fmt.Errorf("unknown route"))
	})
	return mux
}

// owned refuses a slug the link does not cover.
//
// A token is good for ITS profile and no other. Without this, holding a link to
// any CV would be holding a link to every CV — the check is one line and its
// absence would be the whole security model.
func (s *Server) owned(h func(http.ResponseWriter, *http.Request, *profiles.Profile)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if grantOf(r).Slug != slug {
			fail(w, http.StatusForbidden, fmt.Errorf("this link does not cover %q", slug))
			return
		}
		p, err := s.Profiles.Get(slug)
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		h(w, r, p)
	}
}

// write is a handler taking a whole document.
func (s *Server) write(apply func(*http.Request, *profiles.Profile, any) (document.Doc, error)) http.HandlerFunc {
	return s.owned(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		var body any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		doc, err := apply(r, p, body)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		s.saved(w, p, r, doc)
	})
}

// patch is a handler taking a partial change.
func (s *Server) patch(apply func(*http.Request, *profiles.Profile, map[string]any) (document.Doc, error)) http.HandlerFunc {
	return s.owned(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		patch := map[string]any{}
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil && err != io.EOF {
			fail(w, http.StatusBadRequest, err)
			return
		}
		doc, err := apply(r, p, patch)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		s.saved(w, p, r, doc)
	})
}

// saved is the answer to every write: the stored document, and the fit.
//
// The FIT COMES BACK WITH THE SAVE. The editor needs to know whether what was
// just typed still holds on the page, and a second round trip to ask would show
// the answer to the previous keystroke.
func (s *Server) saved(w http.ResponseWriter, p *profiles.Profile, r *http.Request, doc document.Doc) {
	s.forget(p)
	sendJSON(w, map[string]any{"ok": true, "doc": doc, "fit": s.fitOf(p, lang(r))})
}

// forget drops the cached rendering of a profile.
func (s *Server) forget(p *profiles.Profile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.pages {
		if strings.HasPrefix(key, p.Dir+string(filepath.Separator)) {
			delete(s.pages, key)
		}
	}
}

// Fit is what the editor shows about the page: whether it holds, how much room
// is left in each column, and what to shorten when it does not.
type Fit struct {
	OK      bool               `json:"ok"`
	Summary string             `json:"summary"`
	Margins map[string]float64 `json:"margins"`
	Over    []string           `json:"over,omitempty"`
	// Spacing is how far the engine had to set the page from its natural
	// rhythm, tightest and loosest column. Reported rather than hidden: a CV
	// that only fits at the floor is a CV that is too long, and its author is
	// owed that fact even though the page in front of them looks fine.
	Spacing [2]float64 `json:"spacing"`
	Text    float64    `json:"text"`
}

func fitOf(page *engine.Page) Fit {
	lo, hi := page.Fitted.Spread()
	return Fit{
		OK: page.Fitted.Fits, Summary: page.Summary(),
		Margins: page.Margins(), Over: page.Overflow(),
		Spacing: [2]float64{lo, hi}, Text: page.Fitted.Density.Text,
	}
}

func (s *Server) fitOf(p *profiles.Profile, language string) any {
	entry, err := s.render(p, language)
	if err != nil {
		return map[string]any{"ok": false, "summary": err.Error()}
	}
	return fitOf(entry.page)
}

// --- the portrait -----------------------------------------------------------

// maxPhoto is what a portrait may weigh. A CV carries its photo inline, in
// every page and every PDF it produces, so a large one is paid for on every
// single read.
const maxPhoto = 4 << 20

var photoExt = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp",
}

func (s *Server) uploadPhoto(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
	kind := r.Header.Get("Content-Type")
	ext, ok := photoExt[kind]
	if !ok {
		fail(w, http.StatusUnsupportedMediaType,
			fmt.Errorf("unsupported image format: %q (PNG, JPEG or WebP)", kind))
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxPhoto+1))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if len(raw) > maxPhoto {
		fail(w, http.StatusRequestEntityTooLarge,
			fmt.Errorf("portrait too large (%d MB maximum)", maxPhoto>>20))
		return
	}

	// One portrait per profile, whatever it was uploaded as: the old one goes,
	// or a profile accumulates the images of everyone who ever edited it.
	for _, old := range []string{".png", ".jpg", ".jpeg", ".webp", ".svg"} {
		_ = os.Remove(filepath.Join(p.Dir, "photo"+old))
	}
	if err := os.WriteFile(filepath.Join(p.Dir, "photo"+ext), raw, 0o600); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	// Recorded in the document, so the renderer finds it the same way it finds
	// one that was there all along.
	doc, err := s.Store.UpdateIdentity(p.Slug, map[string]any{"photo": "photo" + ext}, lang(r))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	s.saved(w, p, r, doc)
}

// --- describing a template --------------------------------------------------

// describe is a template plus the tree of fields each of its sections is made
// of.
//
// THIS IS WHAT MAKES THE EDITOR GENERIC. It receives a description of the
// document — categories, sub-categories, leaves and their kinds — and builds
// its forms from that, so it has no per-section code and nothing to update when
// a field is added. The tree is composed here rather than copied into each
// manifest: field shapes belong to the engine's vocabulary, and duplicating
// them per template is how the copies would start to disagree.
func describe(t *templates.Template) map[string]any {
	column := fields.ColumnField(t.Columns)
	sections := make([]map[string]any, 0, len(t.Sections))
	for _, slot := range t.Sections {
		entry := map[string]any{
			"type":   slot.Type,
			"column": slot.Column,
			"fields": append(append(append([]fields.Field{}, fields.SectionCommon...), column),
				fields.SectionFields[slot.Type]...),
		}
		if slot.Max != nil {
			entry["max"] = *slot.Max
		}
		if label, ok := fields.SectionLabels[slot.Type]; ok {
			entry["label"], entry["i18n"] = label.Label, label.I18n
		}
		sections = append(sections, entry)
	}
	out := map[string]any{
		"uuid": t.UUID, "name": t.Name, "title": t.Title,
		"description": t.Description, "columns": t.Columns,
		"identity": fields.IdentityFields, "meta": fields.MetaFields,
		"icons": t.IconOrder, "sections": sections,
	}
	if t.Cover != "" {
		out["cover"] = "/assets/templates/" + t.UUID + "/cover"
	}
	return out
}

func intList(patch map[string]any, key string) ([]int, error) {
	raw, ok := patch[key].([]any)
	if !ok {
		return nil, fmt.Errorf("%q must be a list of positions", key)
	}
	out := make([]int, len(raw))
	for i, v := range raw {
		n, ok := v.(float64)
		if !ok || n != float64(int(n)) {
			return nil, fmt.Errorf("%q must hold whole numbers", key)
		}
		out[i] = int(n)
	}
	return out, nil
}
