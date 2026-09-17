package server

import (
	"net/http"
	"testing"
	"time"
)

// What does one keystroke cost the server?
//
// Every change is saved as it is made, so this is the number that decides
// whether the service can be used by more than one person at once. It has two
// halves, and only one of them is necessary:
//
//   VALIDATE AND WRITE   a few kilobytes of JSON. Microseconds.
//   LAY THE PAGE OUT     the whole engine. Hundreds of milliseconds.
//
// A save that reports the fit does both, because the fit IS a layout. That is
// most of a second of one CPU for a keystroke, and it is why the two are now
// separate: the document is saved at once and cheaply, and the page is drawn
// when somebody actually wants to look at it.

func TestWhatASaveCosts(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}

	// Warm: the first layout of a template parses its fonts, which is not what
	// a keystroke pays.
	call(t, h, "POST", "/api/p/"+slug+"/preview", doc, auth)

	const runs = 20
	start := time.Now()
	for i := 0; i < runs; i++ {
		w := call(t, h, "PUT", "/api/p/"+slug, doc, auth)
		if w.Code != http.StatusOK {
			t.Fatalf("save: %d %s", w.Code, w.Body.String())
		}
	}
	save := time.Since(start) / runs

	start = time.Now()
	for i := 0; i < runs; i++ {
		w := call(t, h, "POST", "/api/p/"+slug+"/preview", doc, auth)
		if w.Code != http.StatusOK {
			t.Fatalf("preview: %d", w.Code)
		}
	}
	render := time.Since(start) / runs

	t.Logf("a save costs %v; laying the page out costs %v (%.0f× the save)",
		save.Round(time.Microsecond), render.Round(time.Millisecond),
		float64(render)/float64(save))

	// The point of separating them. A save that also renders is a save that
	// costs what a render costs, and a person typing generates one per
	// keystroke.
	if save > render/4 {
		t.Errorf("a save costs %v against a render's %v — it is still laying "+
			"the page out, which is the thing a keystroke must not do",
			save, render)
	}
}
