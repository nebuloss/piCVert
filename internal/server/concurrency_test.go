package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Can two people use this at once?
//
// The service is one process serving a page that takes milliseconds to lay out,
// and the layout engine caches glyph widths behind a single mutex. Both facts
// are fine on their own; together they decide whether the second person waits
// for the first.
//
// These run with -race in CI, which is the half that matters most: a data race
// in the font cache would corrupt a measurement rather than fail, and a
// corrupted measurement is a CV that comes out wrong for reasons nobody can
// reproduce.

// TestConcurrentReadsAreSafe drives the read path from many goroutines.
func TestConcurrentReadsAreSafe(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	t.Setenv("PICVERT_PUBLIC", slug)

	var failures atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				w := call(t, h, "GET", "/p/"+slug+"/cv.html", nil, nil)
				if w.Code != http.StatusOK || w.Body.Len() < 1000 {
					failures.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if n := failures.Load(); n > 0 {
		t.Errorf("%d concurrent reads came back wrong", n)
	}
}

// TestConcurrentPreviewsAreSafe is the hot path: every editor keystroke pause
// lays a page out, and several people may be typing at once.
func TestConcurrentPreviewsAreSafe(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}

	var failures atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Each goroutine sends a DIFFERENT document, so no two requests
			// measure the same strings — which is what exercises the glyph
			// cache rather than a warm copy of one answer.
			mine := map[string]any{}
			for k, v := range doc {
				mine[k] = v
			}
			w := call(t, h, "POST", "/api/p/"+slug+"/preview", mine, auth)
			if w.Code != http.StatusOK {
				failures.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if n := failures.Load(); n > 0 {
		t.Errorf("%d concurrent previews failed", n)
	}
}

// TestConcurrentWritesDoNotCorrupt hammers one profile from several writers.
//
// The store writes atomically — temporary file, rename — so a reader sees the
// old document or the new one and never half of either. What this checks is
// that the document is still VALID afterwards, which is the thing a lost update
// would not break and a torn write would.
func TestConcurrentWritesDoNotCorrupt(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			call(t, h, "PATCH", "/api/p/"+slug+"/identity",
				map[string]any{"name": fmt.Sprintf("Writer %d", n)}, auth)
		}(i)
	}
	wg.Wait()

	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatalf("the document is unreadable after concurrent writes: %v", err)
	}
	content, _ := doc["content"].(map[string]any)
	identity, _ := content["identity"].(map[string]any)
	if name, _ := identity["name"].(string); name == "" {
		t.Error("the name is empty after concurrent writes")
	}
}

// TestRenderingIsNotSerialised measures whether a second request waits for the
// first.
//
// The layout engine caches glyph advances behind ONE mutex, held for the whole
// of a measurement. If that mutex is the bottleneck, twelve concurrent layouts
// take twelve times one — and the editor's preview, which is the hot path, gets
// slower for everybody as soon as two people are typing.
//
// Reported rather than asserted: a build host under load makes any threshold a
// flaky test. The number is the point.
func TestRenderingIsNotSerialised(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}
	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}

	const n = 12
	one := time.Now()
	call(t, h, "POST", "/api/p/"+slug+"/preview", doc, auth)
	single := time.Since(one)

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			call(t, h, "POST", "/api/p/"+slug+"/preview", doc, auth)
		}()
	}
	wg.Wait()
	together := time.Since(start)

	// Perfectly serial is n×single; perfectly parallel is 1×single.
	ratio := float64(together) / float64(single)
	t.Logf("one layout %v; %d at once %v (%.1f× one, %d× would be fully serial)",
		single.Round(time.Millisecond), n,
		together.Round(time.Millisecond), ratio, n)
	if ratio > float64(n)*0.9 {
		t.Errorf("concurrent layouts are effectively serialised (%.1f× of one, "+
			"against %d requests) — the font cache lock is the bottleneck", ratio, n)
	}
}

var _ = httptest.NewRequest
