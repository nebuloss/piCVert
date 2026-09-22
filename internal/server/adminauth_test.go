package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"picvert/internal/config"
)

// What a browser does with the sign-in page, as opposed to what a Go client
// does with it.
//
// Every test here exists because the admin interface was unusable in a real
// browser while every existing test passed. That is the shape of the whole
// problem: an HTTP client ignores the headers a browser obeys, so a route can
// answer 200 with correct bytes and still be a page nobody can use. These
// assert the HEADERS, which is the part only a browser reads.

// guarded is a service whose admin port asks for a password.
func guarded(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s, _ := service(t)
	hash, err := config.Hash("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Admin.Password = hash
	return s, s.AdminHandler()
}

// The sign-in form must be allowed to submit itself.
//
// It was not. The page was served under the admin policy, which says
// `form-action 'none'` because no other page on this port submits anything —
// and the browser then blocked the POST before it left. Nothing appeared in the
// log, because nothing was sent: the password was neither right nor wrong, the
// button simply did nothing, and there was no failed attempt anywhere to
// explain why. An administration interface that cannot be signed in to, with no
// error on either side saying so.
func TestTheSignInFormIsAllowedToSubmit(t *testing.T) {
	_, admin := guarded(t)

	for _, path := range []string{"/", "/login"} {
		w := call(t, admin, "GET", path, nil, nil)
		policy := w.Header().Get("Content-Security-Policy")
		if policy == "" {
			t.Fatalf("%s: no content policy at all", path)
		}
		if strings.Contains(policy, "form-action 'none'") {
			t.Fatalf("%s: the form cannot be submitted — a browser blocks the "+
				"POST and nobody can sign in:\n%s", path, policy)
		}
		if !strings.Contains(policy, "form-action 'self'") {
			t.Fatalf("%s: form-action must name this origin:\n%s", path, policy)
		}
	}
}

// And the form must actually work end to end, headers included.
func TestSigningInWorks(t *testing.T) {
	_, admin := guarded(t)

	form := url.Values{"password": {"hunter2"}}
	r := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("signing in answered %d, not a redirect: %s", w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) == 0 {
		t.Fatal("signing in set no session cookie")
	}

	// And the session it issued opens the page.
	page := callAs(t, admin, "GET", "/", nil, nil, w)
	if page.Code != http.StatusOK {
		t.Fatalf("the session did not open the interface: %d", page.Code)
	}
}

// The wrong password is still refused, and says so.
func TestTheWrongPasswordIsRefused(t *testing.T) {
	_, admin := guarded(t)

	form := url.Values{"password": {"not it"}}
	r := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	admin.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("the wrong password answered %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Value != "" {
			t.Fatal("the wrong password issued a session")
		}
	}
}

// The icon is readable without signing in.
//
// It was not, and the consequence outlived the request: a browser refused an
// icon caches that refusal for the origin, so the tab stayed blank for the rest
// of the session — including after signing in, when the icon was being served
// perfectly well. The fix is not making the icon nicer, it is answering the
// first request for it.
//
// Nothing is given away by this. The icon is the same bytes for everyone, and
// it is already on a page shown to anyone who connects.
func TestTheIconIsServedBeforeSigningIn(t *testing.T) {
	_, admin := guarded(t)

	for _, path := range []string{"/favicon.svg", "/favicon.ico"} {
		w := call(t, admin, "GET", path, nil, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s answered %d to somebody not signed in — a browser "+
				"stops asking after that, and the tab stays blank even once "+
				"they are", path, w.Code)
		}
		if got := w.Header().Get("Content-Type"); got != "image/svg+xml" {
			t.Fatalf("%s is typed %q, so a browser will not draw it", path, got)
		}
	}
}

// Everything else stays behind the password. The point of letting three paths
// through is that they are three, named, and none of them says anything about
// the CVs.
func TestTheRestOfTheAdminInterfaceStaysShut(t *testing.T) {
	_, admin := guarded(t)

	shut := []string{"/api/profiles", "/api/metrics", "/api/trash", "/view/jean/cv.html"}
	for _, path := range shut {
		if w := call(t, admin, "GET", path, nil, nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s answered %d without a session, not 401", path, w.Code)
		}
	}
}

// A burst of wrong passwords must not run all at once.
//
// Verifying one costs 600,000 rounds of PBKDF2 — measured at 99 ms of solid
// CPU, which is the point of it. It also makes signing in the most expensive
// thing a stranger can ask for without proving anything, and the per-address
// throttle does not cover it: the failure is recorded AFTER the derivation, so
// requests arriving together all pass the check before any has failed.
//
// Measured unbounded on one core: fifty at once is 3.5 seconds of nothing else
// being served, and the appliance this runs on has one core that also serves
// the CVs.
//
// The gate is tested rather than the login, and concurrency rather than
// duration: a wall-clock assertion would pass or fail depending on how busy
// the machine running the test happens to be.
func TestPasswordDerivationsAreBounded(t *testing.T) {
	const burst = 40

	var (
		mu      sync.Mutex
		running int
		peak    int
	)
	var wg sync.WaitGroup
	for range burst {
		wg.Add(1)
		go func() {
			defer wg.Done()
			withKDFSlot(context.Background(), func() {
				mu.Lock()
				running++
				if running > peak {
					peak = running
				}
				mu.Unlock()

				// Long enough that every goroutine is certainly queued behind
				// this one, so the peak reflects the gate and not scheduling
				// luck.
				time.Sleep(2 * time.Millisecond)

				mu.Lock()
				running--
				mu.Unlock()
			})
		}()
	}
	wg.Wait()

	if peak > cap(kdfGate) {
		t.Fatalf("%d derivations ran at once, past the bound of %d", peak, cap(kdfGate))
	}
	if peak == 0 {
		t.Fatal("nothing ran at all")
	}
}

// And a caller who goes away releases their place in the queue.
//
// Otherwise a flood of abandoned requests is still a flood of work: the
// machine spends 99 ms each producing answers nobody is waiting for.
func TestAnAbandonedLoginDoesNotHoldTheQueue(t *testing.T) {
	_, admin := guarded(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // gone before it is even served

	form := url.Values{"password": {"hunter2"}}
	r := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(ctx)

	// Repeated, because the failure this guards against was a COIN FLIP: a
	// select with both a free slot and a cancelled context ready picks one at
	// random. A single attempt passed about half the time, which is how it got
	// through review once already.
	for range 20 {
		w := httptest.NewRecorder()
		admin.ServeHTTP(w, r)

		// The RIGHT password, from a caller who has gone: it must not be
		// treated as a sign-in. Nobody is there to receive the cookie.
		if w.Code == http.StatusSeeOther {
			t.Fatal("an abandoned request was signed in")
		}
	}
}

// The administration page must be told which port the CVs are on.
//
// A private link is stored as a bare path whenever no domain is configured,
// which is the default. Followed from this page a bare path resolves against
// the ADMINISTRATION port, where /e/<token>/ does not exist — measured as 404
// there against 200 on the public one. So the page is given the public port
// and builds absolute addresses with it.
//
// Taken from the configuration rather than assumed to be 3000: guessing is
// right almost always, and silently wrong for anybody who moved it.
func TestTheAdminPageIsToldThePublicPort(t *testing.T) {
	s, _ := service(t)
	s.Config.Listen = "0.0.0.0:8080"
	admin := s.AdminHandler()

	w := call(t, admin, "GET", "/", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the page answered %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `data-public-port="8080"`) {
		t.Fatal("the page was not told the public port — its links would " +
			"resolve against the administration port, which does not serve them")
	}
}

// And `--addr` must reach the configuration, not merely the listener.
//
// It used to be passed straight to Serve while the configuration kept whatever
// the file said, so anything asking where CVs are served got an answer that
// was quietly untrue.
func TestPublicPortFallsBackWhenTheAddressIsOdd(t *testing.T) {
	for _, c := range []struct{ listen, want string }{
		{"0.0.0.0:3000", "3000"},
		{"127.0.0.1:9400", "9400"},
		{":8080", "8080"},
		{"nonsense", "3000"},
		{"", "3000"},
	} {
		if got := publicPort(c.listen); got != c.want {
			t.Errorf("publicPort(%q) = %q, want %q", c.listen, got, c.want)
		}
	}
}

// The editor offers a way to reach both of its panes on a phone.
//
// It is a form beside a preview, and below about 900 px they stacked. Measured
// on a 375x667 screen that gave the form 97 pixels above 573 pixels of
// preview: the field being typed into was a sliver, under a rendering of an A4
// page far too small to read. Neither pane was usable, so the editor was
// desktop-only without ever saying so.
//
// The panes are now shown one at a time, chosen by a switch — which has to be
// in the page for the stylesheet and the script to have anything to work with.
func TestTheEditorPageCarriesItsPaneSwitch(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, err := s.Tokens.ForProfile(slug)
	if err != nil {
		t.Fatal(err)
	}

	w := call(t, h, "GET", "/e/"+links.Edit+"/edit/", nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the editor answered %d", w.Code)
	}
	body := w.Body.String()
	for _, wanted := range []string{`id="panes"`, `id="pane-edit"`, `id="pane-preview"`} {
		if !strings.Contains(body, wanted) {
			t.Fatalf("the editor has no %s — the panes cannot be switched on a phone", wanted)
		}
	}
	// Each button must name the pane it controls, or the switch is two
	// unlabelled buttons to anybody not looking at the screen.
	if !strings.Contains(body, `aria-controls="form"`) ||
		!strings.Contains(body, `aria-controls="preview"`) {
		t.Fatal("the switch does not say which pane each button shows")
	}
}
