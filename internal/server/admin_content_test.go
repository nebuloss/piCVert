package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// adminPort is the administration interface of a service holding one CV.
func adminPort(t *testing.T) http.Handler {
	t.Helper()
	s, _ := service(t)
	return s.AdminHandler()
}

// The administration port must not serve a CV, by any route.
//
// # WHY THIS IS A TEST AND NOT A COMMENT
//
// It used to serve them: /view/{slug}/cv.html and /view/{slug}/cv.pdf rendered
// any CV on demand, with no token, on the port whose whole protection is that
// nobody proxies it. That gave the service three surfaces able to hand out a
// CV where the design has two, and the extra one was the surface nobody
// reviews precisely because it is meant to be unreachable.
//
// It was not added carelessly — it existed so an administrator could look at a
// CV without holding a link — and that is exactly why it needs a test rather
// than a note: the reason it was added is still true, still reasonable, and
// would justify adding it back.
func TestTheAdminPortServesNoCVContent(t *testing.T) {
	handler := adminPort(t)

	// The routes that used to exist, and the obvious spellings of them.
	for _, path := range []string{
		"/view/jean/cv.html",
		"/view/jean/cv.pdf",
		"/view/jean/",
		"/p/jean/cv.html",
		"/p/jean/cv.pdf",
		"/jean/cv.html",
	} {
		w := call(t, handler, "GET", path, nil, nil)
		if w.Code == http.StatusOK {
			t.Errorf("%s answered 200 from the administration port — "+
				"only the public port serves CVs", path)
		}
		if kind := w.Header().Get("Content-Type"); strings.HasPrefix(kind, "application/pdf") {
			t.Errorf("%s answered with a PDF from the administration port", path)
		}
	}
}

// And the page must send people to the public service rather than to itself.
//
// The inventory is where the links come from, so this checks the shape the
// interface is given: a read link that is followed on the public port. A
// browser cannot be asked here, but the thing it is handed can.
func TestTheInventoryOffersPublicLinks(t *testing.T) {
	handler := adminPort(t)

	w := call(t, handler, "GET", "/api/profiles", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the inventory answered %d", w.Code)
	}
	var answer struct {
		Profiles []struct {
			Slug  string            `json:"slug"`
			Links map[string]string `json:"links"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatalf("unreadable inventory: %v", err)
	}
	if len(answer.Profiles) == 0 {
		t.Fatal("no CVs in the inventory — this test is checking nothing")
	}
	for _, p := range answer.Profiles {
		read := p.Links["read"]
		if !strings.Contains(read, "/e/") {
			t.Errorf("%s: the read link is %q, which is not a share link", p.Slug, read)
		}
		// The page appends cv.html and cv.pdf to it, so it has to end where
		// that produces a path the public port answers.
		if !strings.HasSuffix(read, "/") {
			t.Errorf("%s: the read link %q does not end in a slash, so the "+
				"page's “page” and “pdf” buttons build a broken address", p.Slug, read)
		}
	}
}
