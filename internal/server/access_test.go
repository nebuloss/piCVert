package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Fetching a CV through its link is recorded against the address that did it.
//
// # WHY THIS IS WORTH A TEST
//
// A CV here is published by handing over a link, and that link is the whole of
// its access control. The one question it raises — has anybody opened this,
// and is it one person or a mailing list — had no answer at all: the service
// counted requests in total and by nothing else, so "the recruiter never
// received it" and "this link is on a mailing list" both looked like a number
// going up.
//
// The recording is three lines inside a route, which is exactly the kind of
// thing that survives a refactor as a comment and not as behaviour.
func TestFetchingACVIsRecordedAgainstItsAddress(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	read := "/e/" + links.Read + "/"

	from := func(ip, agent, path string) {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = ip + ":51000"
		r.Header.Set("User-Agent", agent)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s from %s answered %d", path, ip, w.Code)
		}
	}

	const chrome = "Mozilla/5.0 (X11; Linux) AppleWebKit/537 (KHTML, like Gecko) Chrome/140 Safari/537"
	from("203.0.113.9", chrome, read)
	from("203.0.113.9", chrome, read+"cv.html")
	from("203.0.113.9", chrome, read+"cv.pdf")
	from("198.51.100.4", "Mozilla/5.0 Firefox/141", read+"cv.pdf")

	got := s.Access.For(slug)
	if len(got.Visitors) != 2 {
		t.Fatalf("expected 2 addresses, got %d: %+v", len(got.Visitors), got.Visitors)
	}
	if got.Total != 4 {
		t.Errorf("recorded %d fetches, want 4", got.Total)
	}

	byIP := map[string]int{}
	for _, v := range got.Visitors {
		byIP[v.IP] = v.Hits
		if v.Author {
			t.Errorf("%s came through the READ link and is marked as the author", v.IP)
		}
	}
	if byIP["203.0.113.9"] != 3 || byIP["198.51.100.4"] != 1 {
		t.Errorf("hits per address: %v", byIP)
	}
}

// The photo is part of a page already counted.
//
// Counting it too would report one visit as two, and the page's whole value is
// that its numbers can be read as "people who looked".
func TestThePartsOfAPageAreNotCountedTwice(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	read := "/e/" + links.Read + "/"

	call(t, h, "GET", read+"cv.html", nil, nil)
	call(t, h, "GET", read+"photo", nil, nil)
	call(t, h, "GET", read+"info", nil, nil)

	if got := s.Access.For(slug).Total; got != 1 {
		t.Errorf("one page view was recorded as %d fetches", got)
	}
}

// A visit through the EDIT link is the author, not an audience.
func TestTheOwnersOwnVisitsAreMarked(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)

	call(t, h, "GET", "/e/"+links.Edit+"/", nil, nil)

	got := s.Access.For(slug)
	if len(got.Visitors) != 1 {
		t.Fatalf("expected one address, got %+v", got.Visitors)
	}
	if !got.Visitors[0].Author {
		t.Error("a visit through the edit link is not marked as the author's")
	}
}

// A bad link records nothing.
//
// Somebody guessing at tokens is a thing the throttle deals with; writing them
// into the log of a CV they did not reach would put a stranger's address in a
// list headed "who has fetched this CV", which is simply false.
func TestAGuessIsNotAVisit(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()

	call(t, h, "GET", "/e/thisisnotarealtokenatall/cv.html", nil, nil)

	if got := s.Access.For(slug).Total; got != 0 {
		t.Errorf("a failed guess was recorded as %d fetches of %s", got, slug)
	}
}

// The log is on the administration port, and nowhere else.
//
// An address is personal data. The surface that hands it out must be the one
// that is never proxied outwards — and in particular the holder of a CV's own
// edit link must not be able to read the addresses of everyone who opened it.
func TestTheAccessLogIsAdminOnly(t *testing.T) {
	s, slug := service(t)
	public := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)

	for _, path := range []string{"/api/access", "/api/access?slug=" + slug} {
		for _, headers := range []map[string]string{
			nil,
			{"X-CV-Token": links.Edit},
			{"X-CV-Token": links.Read},
		} {
			w := call(t, public, "GET", path, nil, headers)
			if w.Code == http.StatusOK {
				t.Errorf("the public port served %s with headers %v", path, headers)
			}
		}
	}

	w := call(t, s.AdminHandler(), "GET", "/api/access?slug="+slug, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the admin port answered %d for its own access log", w.Code)
	}
	var answer struct {
		Access struct {
			Visitors []struct {
				IP string `json:"ip"`
			} `json:"visitors"`
		} `json:"access"`
		RetainHours int `json:"retainHours"`
		PerCV       int `json:"perCV"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatalf("unreadable answer: %v", err)
	}
	// The bounds travel with the answer: an empty list must be readable as
	// "nothing is remembered" rather than "nobody came".
	if answer.RetainHours == 0 || answer.PerCV == 0 {
		t.Error("the answer does not say how much it keeps, so an empty list cannot be read")
	}
}

// Deleting a CV forgets who read it.
func TestDeletingACVForgetsItsReaders(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)

	call(t, h, "GET", "/e/"+links.Read+"/cv.html", nil, nil)
	if s.Access.For(slug).Total == 0 {
		t.Fatal("nothing was recorded, so this test proves nothing")
	}

	w := call(t, s.AdminHandler(), "DELETE", "/api/p/"+slug, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("deleting answered %d", w.Code)
	}
	if got := s.Access.For(slug); got.Total != 0 || len(got.Visitors) != 0 {
		t.Errorf("addresses outlived the CV they belonged to: %+v", got)
	}
}

// The whole-service view groups across every CV, and says which ones an
// address touched.
//
// That is the answer no per-CV panel can give: an address against one CV is a
// reader, and an address against every CV is either the owner or somebody
// walking the tokens.
func TestTheRequestsViewSpansEveryCV(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	call(t, h, "GET", "/e/"+links.Read+"/cv.html", nil, nil)

	w := call(t, s.AdminHandler(), "GET", "/api/access", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the whole-service view answered %d", w.Code)
	}
	var answer struct {
		Access struct {
			Visitors []struct {
				IP  string   `json:"ip"`
				CVs []string `json:"cvs"`
			} `json:"visitors"`
		} `json:"access"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatalf("unreadable answer: %v", err)
	}
	if len(answer.Access.Visitors) != 1 {
		t.Fatalf("expected one address, got %+v", answer.Access.Visitors)
	}
	if got := answer.Access.Visitors[0].CVs; len(got) != 1 || got[0] != slug {
		t.Errorf("the address is recorded against %v, want [%s]", got, slug)
	}
}

// Narrowing to a CV nobody owns is a 404, not an empty list.
//
// An empty list here reads as "nobody has opened it", which is a different
// and much more alarming answer than "there is no such CV".
func TestNarrowingToAnUnknownCVIsNotAnEmptyList(t *testing.T) {
	s, _ := service(t)
	if w := call(t, s.AdminHandler(), "GET", "/api/access?slug=nosuchcv", nil, nil); w.Code == http.StatusOK {
		t.Errorf("an unknown CV answered %d with a list rather than 404", w.Code)
	}
}

// The inventory carries a count per CV, which is what makes anybody open the
// details at all.
func TestTheInventoryCountsVisits(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	call(t, h, "GET", "/e/"+links.Read+"/cv.html", nil, nil)

	w := call(t, s.AdminHandler(), "GET", "/api/profiles", nil, nil)
	if !strings.Contains(w.Body.String(), `"visits"`) {
		t.Fatalf("the inventory carries no visit count:\n%s", w.Body.String())
	}
	var answer struct {
		Profiles []struct {
			Slug   string `json:"slug"`
			Visits int64  `json:"visits"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatalf("unreadable inventory: %v", err)
	}
	for _, p := range answer.Profiles {
		if p.Slug == slug && p.Visits != 1 {
			t.Errorf("%s reports %d visits, want 1", p.Slug, p.Visits)
		}
	}
}
