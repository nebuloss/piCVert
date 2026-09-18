package server

import (
	"net/http"
	"os"
	"path/filepath"
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

// The numbers on the administration page must move when the thing they count
// happens.
//
// They did not. `saves`, `errors` and `conflicts` were counted nowhere, so the
// page reported "0 saves" and "0 conflicts, 0 errors" for the life of the
// service — and `lastBackup` was a field only the serving process could set,
// while backups are taken by a separate timer, so the page carried a standing
// warning that no backup had ever been taken on a machine backing itself up
// every night.
//
// A dashboard of permanent zeros is worse than no dashboard: it reads as "all
// is well" and it is the thing somebody checks when deciding whether anything
// is wrong.
func TestTheNumbersOnTheAdminPageMove(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	before := s.Metrics.Snapshot()

	// A save.
	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}
	if w := call(t, h, "PUT", "/api/p/"+slug, doc, auth); w.Code != http.StatusOK {
		t.Fatalf("the save failed: %d %s", w.Code, w.Body.String())
	}

	// A save that cannot succeed: a name that is not text.
	bad := map[string]any{"content": map[string]any{
		"identity": map[string]any{"name": 123}, "sections": []any{}}}
	if w := call(t, h, "PUT", "/api/p/"+slug, bad, auth); w.Code == http.StatusOK {
		t.Fatal("an invalid document was accepted")
	}

	// A page, which is a render.
	if w := call(t, h, "GET", "/e/"+links.Read+"/cv.html", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("the page failed: %d", w.Code)
	}

	after := s.Metrics.Snapshot()
	for _, c := range []struct {
		what        string
		before, now int64
	}{
		{"saves", before.Saves, after.Saves},
		{"errors", before.Errors, after.Errors},
		{"renders", before.Renders, after.Renders},
	} {
		if c.now <= c.before {
			t.Errorf("%s did not move: %d -> %d", c.what, c.before, c.now)
		}
	}
}

// And the last backup is read from where backups are actually written.
//
// The serving process never sees a backup happen — a timer or cron takes them
// — so a counter it keeps itself can only ever say "none". It said exactly
// that, permanently.
func TestTheLastBackupIsReadFromDisk(t *testing.T) {
	data := t.TempDir()
	backups := filepath.Join(filepath.Dir(data), "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, ok := lastBackupAt(data); ok {
		t.Fatal("a backup was reported before one existed")
	}
	if err := os.WriteFile(filepath.Join(backups, "picvert-2026-09-18.tar.gz"),
		[]byte("not really an archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	at, ok := lastBackupAt(data)
	if !ok {
		t.Fatal("a backup on disk was not noticed")
	}
	if time.Since(at) > time.Minute {
		t.Fatalf("the time reported is not the file's: %v", at)
	}

	// Something that is not a backup must not count as one.
	if err := os.Remove(filepath.Join(backups, "picvert-2026-09-18.tar.gz")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backups, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := lastBackupAt(data); ok {
		t.Fatal("a stray file was counted as a backup")
	}
}
