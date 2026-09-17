package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"picvert/internal/lease"
)

// The lease, over HTTP.
//
// What these establish is the promise the interface makes: one person edits,
// the other is told rather than discovering it when their save is thrown away.

// window is one browser window: the cookies it has been given, carried through
// every request it makes. Two of these are two editors.
type window struct {
	t    *testing.T
	h    http.Handler
	auth map[string]string
	jar  []*httptest.ResponseRecorder
}

func newWindow(t *testing.T, h http.Handler, auth map[string]string) *window {
	return &window{t: t, h: h, auth: auth}
}

func (win *window) do(method, path string, body any) *httptest.ResponseRecorder {
	win.t.Helper()
	w := callAs(win.t, win.h, method, path, body, win.auth, win.jar...)
	// Anything the server set is remembered, which is the whole of what makes
	// this a window rather than a request.
	if len(w.Result().Cookies()) > 0 {
		win.jar = append(win.jar, w)
	}
	return w
}

func (win *window) lease(path string) map[string]any {
	win.t.Helper()
	return decode(win.t, win.do("POST", path, nil))
}

func TestTheSecondEditorIsToldRatherThanRefusedLater(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	first := newWindow(t, h, auth).lease("/api/p/" + slug + "/lease")
	if first["held"] != true {
		t.Fatal("the first editor was refused the lease")
	}

	// A second window, with the SAME link — which is the whole situation.
	second := newWindow(t, h, auth).lease("/api/p/" + slug + "/lease")
	if second["held"] != false {
		t.Fatal("two windows were granted the lease at once")
	}
	if second["heldBy"] == "" {
		t.Error("the second editor was not told who has it")
	}
	if second["freeInMs"] == nil {
		t.Error("the second editor was not told how long to expect to wait")
	}
}

// And the lease is enforced where it counts: at the write.
func TestAWindowWithoutTheLeaseCannotSave(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	holder := newWindow(t, h, auth)
	holder.lease("/api/p/" + slug + "/lease")

	other := newWindow(t, h, auth)
	other.lease("/api/p/" + slug + "/lease")

	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}

	// The holder may write.
	if w := holder.do("PUT", "/api/p/"+slug, doc); w.Code != http.StatusOK {
		t.Fatalf("the lease holder could not save: %d %s", w.Code, w.Body.String())
	}
	// The other may not — and is told why, rather than silently succeeding.
	if w := other.do("PUT", "/api/p/"+slug, doc); w.Code != http.StatusLocked {
		t.Fatalf("a window without the lease saved anyway: %d", w.Code)
	}
}

// A caller that takes no lease is still served: the command line and anyone
// with curl have no way to take one, and are covered by the revision check.
func TestACallerWithNoLeaseIsStillServed(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	newWindow(t, h, auth).lease("/api/p/" + slug + "/lease") // somebody holds it

	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}
	if w := call(t, h, "PUT", "/api/p/"+slug, doc, auth); w.Code != http.StatusOK {
		t.Fatalf("a caller presenting no editor identity was refused: %d", w.Code)
	}
}

// Closing a tab frees it at once, rather than at the timeout.
func TestReleasingLetsTheNextPersonIn(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	leaving := newWindow(t, h, auth)
	leaving.lease("/api/p/" + slug + "/lease")
	leaving.do("DELETE", "/api/p/"+slug+"/lease", nil)

	next := newWindow(t, h, auth).lease("/api/p/" + slug + "/lease")
	if next["held"] != true {
		t.Fatal("the lease was not free after being released")
	}
}

// A window that goes quiet loses it, so a closed tab or a crash cannot hold a
// CV for ever.
func TestASilentWindowLosesTheLease(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	newWindow(t, h, auth).lease("/api/p/" + slug + "/lease")

	// Rather than waiting three quarters of a minute, move the clock.
	at := time.Now()
	s.Leases.Now = func() time.Time { return at.Add(lease.DefaultTTL * 2) }

	next := newWindow(t, h, auth).lease("/api/p/" + slug + "/lease")
	if next["held"] != true {
		t.Fatal("a window that stopped talking kept the lease past its expiry")
	}
}

// Two languages are two documents, and must not lock one another.
func TestEditingOneLanguageDoesNotLockTheOther(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	if _, err := s.Store.AddLanguage(slug, "en", ""); err != nil {
		t.Fatal(err)
	}

	first := newWindow(t, h, auth).lease("/api/p/" + slug + "/lease")
	second := newWindow(t, h, auth).lease("/api/p/" + slug + "/lease?lang=en")
	if first["held"] != true || second["held"] != true {
		t.Fatal("editing the default language blocked editing a translation")
	}
}

// Reloading your own editor must not lock you out.
//
// The identity was minted per page load and kept in a JavaScript variable, so
// pressing F5 produced a NEW one — and the server, quite correctly, refused it
// because the old identity still held the lease. The person was told somebody
// else was editing, and that somebody else was them, for the length of the
// timeout.
//
// It is the likeliest way to meet this feature and the worst way to meet it.
func TestReloadingYourOwnEditorKeepsTheLease(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	win := newWindow(t, h, auth)
	// A tab says which tab it is from its very first request, because the
	// nonce is made before anything is asked for.
	win.auth = merge(auth, map[string]string{"X-CV-Window": "tab-one"})

	if win.lease("/api/p/" + slug + "/lease")["held"] != true {
		t.Fatal("the first request for the lease was refused")
	}
	// What a reload is: the same browser, the same tab, asking again — and
	// now carrying the cookie the first request was given.
	if win.lease("/api/p/" + slug + "/lease")["held"] != true {
		t.Fatal("reloading the editor locked the person out of their own CV")
	}
}

// Two tabs of ONE browser are two editors.
//
// A cookie says which browser, and a browser can have the same CV open twice —
// so the cookie alone would have both tabs believing they held the lease. This
// is what the per-tab half of the identity is for, and the first version got it
// wrong in a way that only showed up with two real tabs: the lease was taken
// under the browser half alone, because the very first request had no cookie
// yet to combine with.
func TestTwoTabsOfOneBrowserAreTwoEditors(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	first := newWindow(t, h, auth)
	first.auth = merge(auth, map[string]string{"X-CV-Window": "tab-one"})
	if first.lease("/api/p/" + slug + "/lease")["held"] != true {
		t.Fatal("the first tab was refused")
	}

	// The second tab: same browser, so it carries the same cookie.
	second := newWindow(t, h, auth)
	second.auth = merge(auth, map[string]string{"X-CV-Window": "tab-two"})
	second.jar = first.jar
	if second.lease("/api/p/" + slug + "/lease")["held"] != false {
		t.Fatal("two tabs of one browser both hold the lease")
	}

	// And the first tab still has it, and can still save.
	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}
	if w := first.do("PUT", "/api/p/"+slug, doc); w.Code != http.StatusOK {
		t.Fatalf("the holder could not save: %d", w.Code)
	}
	if w := second.do("PUT", "/api/p/"+slug, doc); w.Code != http.StatusLocked {
		t.Fatalf("the second tab saved anyway: %d", w.Code)
	}
}

// Somebody guessing links must not lock out somebody holding a real one.
//
// The throttle used to run BEFORE the link was checked, so a valid link was
// refused because another machine behind the same address had been guessing —
// and being right never cleared it, because the handler that records a success
// was never reached. One person scanning locked out everybody on that address
// for a quarter of an hour, including whoever's CV it was.
//
// Behind a company's shared address, that is one careless person costing
// everybody else access to their own CVs.
func TestGuessingDoesNotLockOutSomebodyWithARealLink(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)

	// Somebody on this address tries a great many bad links.
	for i := 0; i < 30; i++ {
		call(t, h, "GET", "/e/aaaaaaaaaaaaaaaaaaaaaaaa/", nil, nil)
	}
	// They are refused, which is the point of the throttle.
	if w := call(t, h, "GET", "/e/bbbbbbbbbbbbbbbbbbbbbbbb/", nil, nil); w.Code != http.StatusTooManyRequests {
		t.Errorf("guessing was not throttled: %d", w.Code)
	}

	// And somebody holding a real link is served anyway.
	if w := call(t, h, "GET", "/e/"+links.Read+"/", nil, nil); w.Code != http.StatusOK {
		t.Fatalf("a valid link was refused because of somebody else's "+
			"guessing: %d", w.Code)
	}
	// Having proved they are not scanning, the address is clear again.
	if w := call(t, h, "GET", "/e/"+links.Edit+"/", nil, nil); w.Code != http.StatusOK {
		t.Errorf("the address was not cleared by a valid link: %d", w.Code)
	}
}

// One CV cannot be made to fill a disk.
//
// Asking for a language costs one request and yields a whole second document
// with its own journal. Four hundred were accepted in a few seconds, and only
// because the asking stopped — the per-request limits were all real and all
// beside the point, because none of them looked at the total.
func TestOneCVCannotGrowWithoutEnd(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	// A small ceiling, so the test is about the rule rather than about
	// writing eight megabytes.
	t.Setenv("PICVERT_MAX_PROFILE_MB", "1")

	refused := false
	for a := 'a'; a <= 'z' && !refused; a++ {
		for b := 'a'; b <= 'z'; b++ {
			w := call(t, h, "POST", "/api/p/"+slug+"/languages",
				map[string]any{"lang": string(a) + string(b)}, auth)
			if w.Code == http.StatusInsufficientStorage {
				refused = true
				break
			}
		}
	}
	if !refused {
		t.Fatal("676 languages were accepted without the profile ever being full")
	}

	// And the CV still works: refusing to grow is not refusing to serve.
	if w := call(t, h, "GET", "/e/"+links.Read+"/cv.html", nil, nil); w.Code != http.StatusOK {
		t.Errorf("a full profile stopped rendering: %d", w.Code)
	}
}
