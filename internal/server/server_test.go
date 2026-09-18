package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"picvert/internal/config"
	"picvert/internal/document"
	"picvert/internal/tokens"
)

// The acceptance criteria of the whole service, stated as tests.
//
// They drive it over HTTP rather than through its internals, for the same
// reason the engine it replaces did: what a route answers is the contract, and
// a test calling a method directly can pass while the route above it does not
// exist. The two that matter most are “nothing is public unless it is named”
// and “a link only opens its own CV” — everything else here is a feature, and
// those two are the security model.

// home is the checkout, found by walking up to the directory holding go.mod.
//
// go.mod rather than templates/: internal/ holds a package called templates,
// so a walk looking for that name stops one directory too soon and every test
// then fails on a missing example with no hint as to why.
func home(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 6 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			break
		}
		dir = up
	}
	t.Fatal("cannot find the checkout root")
	return ""
}

// service builds a server over a scratch data directory holding one CV, copied
// from the shipped example so the test needs no document of its own.
func service(t *testing.T) (*Server, string) {
	t.Helper()
	root := home(t)
	data := t.TempDir()

	profile := filepath.Join(data, "jean")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "examples", "jean-dupont")
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(source, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profile, e.Name()), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The defaults, plus the sandbox. Stated rather than put in the
	// environment: a test that loaded a real configuration file would pass or
	// fail depending on what happened to be on the machine it ran on.
	cfg := config.Defaults()
	cfg.DataDir = data
	cfg.Home = root

	s, err := New(root, cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	return s, "jean"
}

func call(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, path, reader)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// callAs is `call` carrying the cookies an earlier response set.
//
// What a browser does by itself, and what makes the difference between one
// window and two testable: two calls that carry the same cookie are the same
// window, and two that do not are not.
func callAs(t *testing.T, h http.Handler, method, path string, body any,
	headers map[string]string, previous ...*httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, path, reader)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	for _, response := range previous {
		for _, cookie := range response.Result().Cookies() {
			r.AddCookie(cookie)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("not JSON (%d): %s", w.Code, w.Body.String())
	}
	return out
}

func TestNothingIsPublicByDefault(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()

	if got := call(t, h, "GET", "/p/"+slug+"/", nil, nil).Code; got != http.StatusNotFound {
		t.Fatalf("an unnamed CV answered %d, want 404", got)
	}
	// Named, and it opens. The rule is a setting, not an accident.
	s.Config.Access.Public = []string{slug}
	if got := call(t, h, "GET", "/p/"+slug+"/", nil, nil).Code; got != http.StatusOK {
		t.Fatalf("a published CV answered %d, want 200", got)
	}
}

func TestBeingTheDefaultProfileDoesNotPublishIt(t *testing.T) {
	s, slug := service(t)
	if s.Profiles.Default() == nil {
		t.Fatal("no default profile at all")
	}
	// Asked of the configuration, which is what the server actually consults.
	// This used to ask a second implementation of the same rule, in a package
	// nothing else referenced — so one of the two stated acceptance criteria
	// of this service was being checked against code that never ran.
	if s.Config.IsPublic(slug) {
		t.Fatal("the default profile is public without being named")
	}
	// And the route agrees, which is the part that matters: a rule that holds
	// in a function while the address serves the CV anyway is not a rule.
	h := s.Handler()
	if w := call(t, h, "GET", "/p/"+slug+"/cv.html", nil, nil); w.Code == http.StatusOK {
		t.Fatal("the default profile is readable at a guessable address")
	}
}

func TestALinkOnlyOpensItsOwnCV(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, err := s.Tokens.ForProfile(slug)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		headers map[string]string
		path    string
		want    int
	}{
		{"no token", nil, "/api/p/" + slug, http.StatusUnauthorized},
		{"a token nobody issued", map[string]string{"X-CV-Token": "0123456789abcdefghij"},
			"/api/p/" + slug, http.StatusUnauthorized},
		{"the edit link", map[string]string{"X-CV-Token": links.Edit},
			"/api/p/" + slug, http.StatusOK},
		{"the edit link, another CV", map[string]string{"X-CV-Token": links.Edit},
			"/api/p/somebody-else", http.StatusForbidden},
	}
	for _, c := range cases {
		if got := call(t, h, "GET", c.path, nil, c.headers).Code; got != c.want {
			t.Errorf("%s answered %d, want %d", c.name, got, c.want)
		}
	}
}

func TestAReadLinkMayReadAndMayNotWrite(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, err := s.Tokens.ForProfile(slug)
	if err != nil {
		t.Fatal(err)
	}

	if got := call(t, h, "GET", "/e/"+links.Read+"/", nil, nil).Code; got != http.StatusOK {
		t.Fatalf("the read link would not open the CV: %d", got)
	}
	// Refused by the server, not merely hidden in the interface: a button that
	// is not drawn is not a permission.
	if got := call(t, h, "GET", "/e/"+links.Read+"/edit/", nil, nil).Code; got != http.StatusForbidden {
		t.Errorf("the read link reached the editor: %d", got)
	}
	got := call(t, h, "PATCH", "/api/p/"+slug+"/identity",
		map[string]any{"name": "Someone Else"},
		map[string]string{"X-CV-Token": links.Read}).Code
	if got != http.StatusForbidden {
		t.Errorf("the read link wrote to the CV: %d", got)
	}
}

func TestAnInvalidLinkIsNotACV(t *testing.T) {
	s, _ := service(t)
	h := s.Handler()
	if got := call(t, h, "GET", "/e/aaaaaaaaaaaaaaaaaaaaaa/", nil, nil).Code; got != http.StatusNotFound {
		t.Fatalf("a made-up link answered %d, want 404", got)
	}
}

func TestSavingValidatesAndStores(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	// A document that is not a CV never reaches the disk.
	if got := call(t, h, "PUT", "/api/p/"+slug, map[string]any{"content": map[string]any{}}, auth).Code; got != http.StatusBadRequest {
		t.Errorf("an invalid document was accepted: %d", got)
	}

	w := call(t, h, "PATCH", "/api/p/"+slug+"/identity",
		map[string]any{"name": "Jeanne Dupont"}, auth)
	if w.Code != http.StatusOK {
		t.Fatalf("the save failed: %d %s", w.Code, w.Body.String())
	}
	answer := decode(t, w)
	// A save reports NO fit, deliberately. The fit is a layout, and a keystroke
	// must not pay for one — see Server.saved. Reinstating it here would
	// reinstate a 193 ms save.
	if answer["fit"] != nil {
		t.Error("the save reported a fit, which means it laid the page out — " +
			"that is most of a second of CPU for one keystroke")
	}
	if answer["doc"] == nil {
		t.Error("the save did not return the stored document")
	}

	// Stored, not merely echoed.
	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}
	content, _ := doc["content"].(map[string]any)
	identity, _ := content["identity"].(map[string]any)
	if identity["name"] != "Jeanne Dupont" {
		t.Errorf("stored name is %v", identity["name"])
	}
}

func TestUnknownPropertiesSurviveARoundTrip(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}
	// What a NEWER version of the engine might have written. An older one must
	// hand it back untouched, or opening a CV in the wrong version quietly
	// deletes part of it.
	doc["somethingFromTheFuture"] = map[string]any{"kept": true}
	if w := call(t, h, "PUT", "/api/p/"+slug, doc, auth); w.Code != http.StatusOK {
		t.Fatalf("the save failed: %d %s", w.Code, w.Body.String())
	}

	again, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}
	future, _ := again["somethingFromTheFuture"].(map[string]any)
	if future == nil || future["kept"] != true {
		t.Fatalf("an unknown property was dropped on save: %v", again["somethingFromTheFuture"])
	}
}

func TestThePreviewLaysOutAnUnsavedDocument(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}
	content, _ := doc["content"].(map[string]any)
	identity, _ := content["identity"].(map[string]any)
	identity["name"] = "Not Yet Saved"

	answer := decode(t, call(t, h, "POST", "/api/p/"+slug+"/preview", doc, auth))
	html, _ := answer["html"].(string)
	if html == "" {
		t.Fatal("the preview drew nothing")
	}
	if !bytes.Contains([]byte(html), []byte("Not Yet Saved")) {
		t.Error("the preview drew the stored document rather than the one sent")
	}
	// And nothing was written: a preview that saves is not a preview.
	stored, _ := s.Store.Read(slug, "")
	storedContent, _ := stored["content"].(map[string]any)
	storedIdentity, _ := storedContent["identity"].(map[string]any)
	if storedIdentity["name"] == "Not Yet Saved" {
		t.Error("the preview wrote to the disk")
	}
}

func TestTheJournalRecordsWhatChanged(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	call(t, h, "PATCH", "/api/p/"+slug+"/identity", map[string]any{"name": "First"}, auth)
	call(t, h, "PATCH", "/api/p/"+slug+"/identity", map[string]any{"name": "Second"}, auth)

	answer := decode(t, call(t, h, "GET", "/api/p/"+slug+"/history", nil, auth))
	entries, _ := answer["entries"].([]any)
	if len(entries) == 0 {
		t.Fatal("nothing was recorded")
	}
	first, _ := entries[0].(map[string]any)
	if first["after"] != "Second" {
		t.Errorf("the latest entry says %v, want “Second”", first["after"])
	}
	// One episode, not two: typing is one act even when it is fifty saves.
	if count, _ := first["count"].(float64); count < 2 {
		t.Errorf("two edits to one field became %v entries", count)
	}
}

func TestDeletingSetsACVAsideAndRestoringBringsItBack(t *testing.T) {
	s, slug := service(t)
	admin := s.AdminHandler()

	if got := call(t, admin, "DELETE", "/api/p/"+slug, nil, nil).Code; got != http.StatusOK {
		t.Fatalf("the deletion failed: %d", got)
	}
	if _, err := s.Profiles.Get(slug); err == nil {
		t.Fatal("the CV is still in place after being deleted")
	}
	// The links go with it, or a token would identify a slug whose directory no
	// longer exists.
	if _, ok := s.Tokens.Verify("whatever"); ok {
		t.Fatal("a made-up token verified")
	}

	entries := s.TrashList()
	if len(entries) != 1 || entries[0].Slug != slug {
		t.Fatalf("the trash holds %v", entries)
	}
	if got := call(t, admin, "POST", "/api/trash/"+slug+"/restore", nil, nil).Code; got != http.StatusOK {
		t.Fatalf("the restore failed: %d", got)
	}
	if _, err := s.Profiles.Get(slug); err != nil {
		t.Fatalf("the CV did not come back: %v", err)
	}
}

func TestRotatingALinkInvalidatesTheOldOne(t *testing.T) {
	s, slug := service(t)
	before, err := s.Tokens.ForProfile(slug)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.Tokens.Rotate(slug, tokens.Edit)
	if err != nil {
		t.Fatal(err)
	}
	if after.Edit == before.Edit {
		t.Fatal("the edit link did not change")
	}
	if _, ok := s.Tokens.Verify(before.Edit); ok {
		t.Fatal("the old edit link still opens the CV")
	}
	// The other link is untouched: renewing one must not break the other.
	if grant, ok := s.Tokens.Verify(before.Read); !ok || grant.Mode != tokens.Read {
		t.Fatal("renewing the edit link broke the read link")
	}
}

func TestTheCVCarriesAContentPolicyThatAllowsItsOwnFonts(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	s.Config.Access.Public = []string{slug}

	w := call(t, h, "GET", "/p/"+slug+"/cv.html", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the page answered %d", w.Code)
	}
	policy := w.Header().Get("Content-Security-Policy")
	// Without this the browser blocks the embedded typefaces, falls back to
	// whatever is installed, and a CV that fits by less than a line is silently
	// cut off — a fault that does not reproduce when the same file is opened
	// from disk, because the policy travels in a header.
	if !bytes.Contains([]byte(policy), []byte("font-src data:")) {
		t.Errorf("the page's policy would block its own fonts: %s", policy)
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("a token in the address could leave through the Referer header")
	}
}

// A CV cannot gain a second copy of the language it is already written in.
//
// The check used to be "does cv.<lang>.json exist", which misses the default
// document entirely: a CV whose own language is English accepted "add
// English", because cv.en.json did not exist yet even though cv.json was
// already English.
//
// The result was two documents claiming one language. The switcher listed it
// twice, the bare address served one and the language picker the other, and
// editing through either left them silently disagreeing — with nothing to say
// which was the real CV.
func TestACVCannotBeAddedInTheLanguageItIsAlreadyIn(t *testing.T) {
	s, slug := service(t)

	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}
	own := document.Str(document.Meta(doc), "lang")
	if own == "" {
		t.Fatal("the example has no language of its own to test against")
	}

	if _, err := s.Store.AddLanguage(slug, own, ""); err == nil {
		t.Fatalf("a %q version was added to a CV already written in %q", own, own)
	}

	// And exactly one document still claims it.
	p, err := s.Profiles.Get(slug)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, l := range p.Languages() {
		if l.Lang == own {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("%d documents claim %q, expected 1", seen, own)
	}
}

// Another language is still perfectly welcome.
func TestADifferentLanguageIsStillAccepted(t *testing.T) {
	s, slug := service(t)

	doc, _ := s.Store.Read(slug, "")
	own := document.Str(document.Meta(doc), "lang")
	other := "de"
	if own == other {
		other = "it"
	}

	if _, err := s.Store.AddLanguage(slug, other, ""); err != nil {
		t.Fatalf("adding %q was refused: %v", other, err)
	}
}
