package server

import (
	"net/http"
	"testing"
)

// What two people editing one CV actually experience.
//
// Written to answer the question rather than to assert a design: each of these
// documents a BEHAVIOUR, including the ones that are limitations. A test that
// records what the software does is worth more here than an opinion about what
// it should do, because the limitations are the part somebody has to decide
// about.

// The second person does NOT see the first person's changes arrive.
//
// There is no channel from the server to an open editor: no polling, no events,
// no socket. An editor shows the document it loaded, plus whatever its own user
// has typed, until something makes it reload.
func TestAnOpenEditorDoesNotSeeAnotherPersonsChanges(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	// Both load. This is the whole of what each one knows.
	second := decode(t, call(t, h, "GET", "/api/p/"+slug, nil, auth))
	theirDoc, _ := second["doc"].(map[string]any)

	// The first person saves a change.
	first := call(t, h, "GET", "/api/p/"+slug, nil, auth)
	mine, _ := decode(t, first)["doc"].(map[string]any)
	setName(mine, "Changed By The First")
	if w := call(t, h, "PUT", "/api/p/"+slug, mine,
		merge(auth, map[string]string{"If-Match": first.Header().Get("ETag")})); w.Code != http.StatusOK {
		t.Fatalf("the first save failed: %d", w.Code)
	}

	// The second person's copy is untouched. Nothing pushed it to them, and
	// nothing will.
	if name := nameIn(theirDoc); name == "Changed By The First" {
		t.Fatal("the second editor's document changed on its own, which this " +
			"service has no mechanism to do")
	}

}

// There is no lock, per field or otherwise.
//
// Two people can both have the editor open and both type; nothing marks a
// field, a section or the document as taken. The first to save wins and the
// second is refused — which is a check at the END rather than a claim at the
// start.
//
// The difference matters: a lock would stop the second person wasting their
// time, and this does not. What it stops is their work being destroyed without
// anybody noticing, which is the worse of the two failures.
func TestThereIsNoLock(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	// Opening a CV takes nothing and announces nothing: a second reader is
	// served exactly as the first was.
	a := call(t, h, "GET", "/api/p/"+slug, nil, auth)
	b := call(t, h, "GET", "/api/p/"+slug, nil, auth)
	if a.Code != http.StatusOK || b.Code != http.StatusOK {
		t.Fatalf("two readers: %d, %d", a.Code, b.Code)
	}
	if a.Header().Get("ETag") != b.Header().Get("ETag") {
		t.Error("two people opening the same CV got different revisions")
	}

	// And nothing anywhere records that a CV is being edited: there is no
	// registry of editors to consult, which is a design decision rather than an
	// omission — see the comment above.
}

// Changes to DIFFERENT fields still conflict.
//
// The unit of comparison is the document, not the field: the editor sends the
// whole CV, and the revision covers all of it. Two people working on unrelated
// sections therefore collide exactly as two people working on the same line do.
//
// Recorded because it is the sharpest edge of the current design, and the thing
// a per-field merge would fix.
func TestEditsToDifferentFieldsStillConflict(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	loaded := call(t, h, "GET", "/api/p/"+slug, nil, auth)
	revision := loaded.Header().Get("ETag")
	doc, _ := decode(t, loaded)["doc"].(map[string]any)

	// One person changes the name.
	mine := clone(doc)
	setName(mine, "Someone")
	if w := call(t, h, "PUT", "/api/p/"+slug, mine,
		merge(auth, map[string]string{"If-Match": revision})); w.Code != http.StatusOK {
		t.Fatalf("first save: %d", w.Code)
	}

	// The other changes the job title — a different field entirely.
	theirs := clone(doc)
	content, _ := theirs["content"].(map[string]any)
	identity, _ := content["identity"].(map[string]any)
	identity["role"] = "A Different Job Title"

	w := call(t, h, "PUT", "/api/p/"+slug, theirs,
		merge(auth, map[string]string{"If-Match": revision}))
	if w.Code != http.StatusConflict {
		t.Fatalf("an edit to an untouched field answered %d; if this is no "+
			"longer 409 then the merge this test documents has been built",
			w.Code)
	}
}

// Restoring a CV brings back the links it was handed out on.
//
// The administration page says "restored, with its original links" and it was
// not true: the links live in a file of their own rather than in the folder
// that gets moved to the trash, so an undo returned the CV on NEW links and
// locked out everybody who had been given the old ones — silently, and with a
// message claiming the opposite.
//
// That is the cost of a CV's identity living outside the CV's folder, and it
// is why the links now travel with it.
func TestRestoringBringsBackTheOriginalLinks(t *testing.T) {
	s, slug := service(t)

	before, err := s.Tokens.ForProfile(slug)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Profiles.Get(slug)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Trash(p); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(slug); err != nil {
		t.Fatal(err)
	}

	after, err := s.Tokens.ForProfile(slug)
	if err != nil {
		t.Fatal(err)
	}
	if after.Edit != before.Edit || after.Read != before.Read {
		t.Fatal("the CV came back on different links — everyone holding one " +
			"was locked out by an undo")
	}

	// And the link genuinely opens it, rather than merely matching a string.
	h := s.Handler()
	w := call(t, h, "GET", "/api/p/"+slug, nil,
		map[string]string{"X-CV-Token": before.Edit})
	if w.Code != http.StatusOK {
		t.Fatalf("the restored link answered %d", w.Code)
	}
}

// While it is set aside, its links open nothing.
//
// They have to leave the live store: a token resolving to a slug whose folder
// is gone fails on a read instead of answering cleanly that the link is
// invalid.
func TestALinkToASetAsideCVOpensNothing(t *testing.T) {
	s, slug := service(t)

	links, err := s.Tokens.ForProfile(slug)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.Profiles.Get(slug)
	if err := s.Trash(p); err != nil {
		t.Fatal(err)
	}

	h := s.Handler()
	w := call(t, h, "GET", "/api/p/"+slug, nil,
		map[string]string{"X-CV-Token": links.Edit})
	if w.Code == http.StatusOK {
		t.Fatal("a link to a deleted CV still opened it")
	}
}
