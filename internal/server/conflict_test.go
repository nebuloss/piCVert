package server

import (
	"net/http"
	"testing"
)

// What happens when two people hold the same edit link?
//
// They can, and it is the ordinary case rather than an exotic one: a link is
// the only credential there is, so sharing it IS how a CV comes to have two
// editors. A tab left open on another machine counts as the second.
//
// The editor sends the WHOLE document on every save. So without something to
// stop it, the second person's save carries the document as it was before the
// first person's change, and that change is destroyed — with nothing said, and
// nothing to notice until somebody reloads and finds their paragraph gone.

func TestALateSaveDoesNotDestroyAnEarlierOne(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	// Both people open the CV. Same document, same revision.
	first := call(t, h, "GET", "/api/p/"+slug, nil, auth)
	if first.Code != http.StatusOK {
		t.Fatalf("load: %d", first.Code)
	}
	revision := first.Header().Get("ETag")
	if revision == "" {
		t.Fatal("the load did not say which revision it was, so no client can " +
			"detect that it has become stale")
	}

	docOf := func(w interface{ Body() string }) map[string]any { return nil }
	_ = docOf

	loaded := decode(t, first)
	doc, _ := loaded["doc"].(map[string]any)

	// The first person changes the name and saves.
	mine := clone(doc)
	setName(mine, "First Person")
	saved := call(t, h, "PUT", "/api/p/"+slug, mine,
		merge(auth, map[string]string{"If-Match": revision}))
	if saved.Code != http.StatusOK {
		t.Fatalf("the first save failed: %d %s", saved.Code, saved.Body.String())
	}

	// The second person, who has been typing all along, saves the document as
	// it was when THEY loaded it. It must be refused.
	theirs := clone(doc)
	setName(theirs, "Second Person")
	late := call(t, h, "PUT", "/api/p/"+slug, theirs,
		merge(auth, map[string]string{"If-Match": revision}))

	if late.Code != http.StatusConflict {
		t.Fatalf("a save based on a stale document answered %d, want 409 — "+
			"the first person's change was silently destroyed", late.Code)
	}

	// And the first person's change is still there.
	after, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}
	if name := nameIn(after); name != "First Person" {
		t.Errorf("the stored name is %q, want “First Person”", name)
	}
}

// A save carrying the current revision goes through. Otherwise the check would
// be refusing everything, which passes the test above for the wrong reason.
func TestASaveOnTheCurrentRevisionIsAccepted(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	first := call(t, h, "GET", "/api/p/"+slug, nil, auth)
	loaded := decode(t, first)
	doc, _ := loaded["doc"].(map[string]any)

	mine := clone(doc)
	setName(mine, "One")
	w := call(t, h, "PUT", "/api/p/"+slug, mine,
		merge(auth, map[string]string{"If-Match": first.Header().Get("ETag")}))
	if w.Code != http.StatusOK {
		t.Fatalf("a save on the current revision was refused: %d", w.Code)
	}

	// And the answer carries the NEW revision, so the same client can save
	// again without reloading.
	next := w.Header().Get("ETag")
	if next == "" || next == first.Header().Get("ETag") {
		t.Fatalf("the save did not report a new revision (%q → %q), so the "+
			"client's next save would conflict with its own", first.Header().Get("ETag"), next)
	}

	mine2 := clone(doc)
	setName(mine2, "Two")
	again := call(t, h, "PUT", "/api/p/"+slug, mine2,
		merge(auth, map[string]string{"If-Match": next}))
	if again.Code != http.StatusOK {
		t.Fatalf("the same client's second save was refused: %d", again.Code)
	}
}

// A caller that says nothing about revisions is still served: the command line
// and a person with curl have no revision to send, and refusing them would make
// the API unusable by hand for a guarantee they did not ask for.
func TestASaveWithoutARevisionIsStillAccepted(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}
	if w := call(t, h, "PUT", "/api/p/"+slug, doc, auth); w.Code != http.StatusOK {
		t.Fatalf("a save with no If-Match was refused: %d", w.Code)
	}
}

// --- helpers ----------------------------------------------------------------

func clone(doc map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range doc {
		out[k] = v
	}
	return out
}

func setName(doc map[string]any, name string) {
	content, _ := doc["content"].(map[string]any)
	identity, _ := content["identity"].(map[string]any)
	identity["name"] = name
}

func nameIn(doc map[string]any) string {
	content, _ := doc["content"].(map[string]any)
	identity, _ := content["identity"].(map[string]any)
	name, _ := identity["name"].(string)
	return name
}

func merge(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
