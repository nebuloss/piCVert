package server

import (
	"fmt"
	"net/http"
	"runtime"
	"testing"
)

// What does this cost to run?
//
// The unit caps the service at 256 MB, which is a number somebody has to be
// able to justify. These measure rather than assume — and the cache below is
// the reason: it holds a rendered page and a rendered PDF per profile, and a
// bound expressed in ENTRIES says nothing about bytes.

// TestWhatOneCachedProfileCosts measures a single cached rendering.
func TestWhatOneCachedProfileCosts(t *testing.T) {
	s, slug := service(t)
	p, err := s.Profiles.Get(slug)
	if err != nil {
		t.Fatal(err)
	}

	entry, err := s.render(p, "")
	if err != nil {
		t.Fatal(err)
	}
	pdf, err := s.pdf(p, "")
	if err != nil {
		t.Fatal(err)
	}

	html := len(entry.html)
	t.Logf("one cached profile: %d KB of page + %d KB of PDF = %d KB, "+
		"before the layout tree",
		html/1024, len(pdf)/1024, (html+len(pdf))/1024)

	// The bound has to be in bytes to mean anything. At this size, a few
	// hundred entries is a few hundred megabytes — which is more than the unit
	// allows the whole process.
	if (html+len(pdf))*cacheMaxEntries > 200<<20 {
		t.Errorf("a full cache would be %d MB, against a 256 MB ceiling: "+
			"the bound must be in bytes, not entries",
			(html+len(pdf))*cacheMaxEntries>>20)
	}
}

// TestMemoryUnderLoad renders many distinct profiles and reports the heap.
//
// Distinct profiles on purpose: one profile rendered repeatedly answers from
// the cache and measures nothing. This is the shape of a host serving a few
// dozen CVs that are all being looked at.
func TestMemoryUnderLoad(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	s.Config.Access.Public = []string{"*"}

	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}
	// Without the portrait: it lives in the profile it was uploaded to, and a
	// document naming a file that is not there is REFUSED rather than drawn
	// without it — which is correct, and is not what this is measuring.
	if content, ok := doc["content"].(map[string]any); ok {
		if identity, ok := content["identity"].(map[string]any); ok {
			delete(identity, "photo")
		}
	}

	const profiles = 40
	for i := 0; i < profiles; i++ {
		name := fmt.Sprintf("person-%02d", i)
		if _, err := s.Store.Create(name, fmt.Sprintf("Person %d", i), "", "en"); err != nil {
			t.Fatal(err)
		}
		// A real document rather than the seeded skeleton: an empty CV lays out
		// in a fraction of the time and holds a fraction of the memory, so
		// measuring one would flatter the result.
		if _, err := s.Store.Write(name, doc, ""); err != nil {
			t.Fatal(err)
		}
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	for i := 0; i < profiles; i++ {
		name := fmt.Sprintf("person-%02d", i)
		if w := call(t, h, "GET", "/p/"+name+"/cv.html", nil, nil); w.Code != http.StatusOK {
			t.Fatalf("%s: %d", name, w.Code)
		}
		if w := call(t, h, "GET", "/p/"+name+"/cv.pdf", nil, nil); w.Code != http.StatusOK {
			t.Fatalf("%s pdf: %d", name, w.Code)
		}
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	grew := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("%d profiles rendered to page and PDF: heap %d MB → %d MB (+%d MB), "+
		"cache holds %d",
		profiles, before.HeapAlloc>>20, after.HeapAlloc>>20, grew>>20, s.cacheSize())

	// The whole process has 256 MB. Rendering forty CVs must not spend most of
	// it on remembering them.
	if grew > 64<<20 {
		t.Errorf("%d profiles cost %d MB of heap — the cache is not bounded "+
			"usefully", profiles, grew>>20)
	}
}
