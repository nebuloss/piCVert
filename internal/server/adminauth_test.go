package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
