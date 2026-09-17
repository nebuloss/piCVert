package server

import (
	"net/http"
	"testing"
	"time"

	"picvert/internal/lease"
)

// The lease, over HTTP.
//
// What these establish is the promise the interface makes: one person edits,
// the other is told rather than discovering it when their save is thrown away.

func leaseFor(t *testing.T, h http.Handler, slug string, auth map[string]string, extra map[string]string) map[string]any {
	t.Helper()
	return decode(t, call(t, h, "POST", "/api/p/"+slug+"/lease", nil, merge(auth, extra)))
}

func TestTheSecondEditorIsToldRatherThanRefusedLater(t *testing.T) {
	s, slug := service(t)
	h := s.Handler()
	links, _ := s.Tokens.ForProfile(slug)
	auth := map[string]string{"X-CV-Token": links.Edit}

	first := leaseFor(t, h, slug, auth, nil)
	if first["held"] != true {
		t.Fatal("the first editor was refused the lease")
	}
	holder, _ := first["editor"].(string)
	if holder == "" {
		t.Fatal("the server did not mint an identity for the editor")
	}

	// A second window, with the SAME link — which is the whole situation.
	second := leaseFor(t, h, slug, auth, nil)
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

	first := leaseFor(t, h, slug, auth, nil)
	holder, _ := first["editor"].(string)

	second := leaseFor(t, h, slug, auth, nil)
	other, _ := second["editor"].(string)

	doc, err := s.Store.Read(slug, "")
	if err != nil {
		t.Fatal(err)
	}

	// The holder may write.
	if w := call(t, h, "PUT", "/api/p/"+slug, doc,
		merge(auth, map[string]string{"X-CV-Editor": holder})); w.Code != http.StatusOK {
		t.Fatalf("the lease holder could not save: %d %s", w.Code, w.Body.String())
	}
	// The other may not — and is told why, rather than silently succeeding.
	w := call(t, h, "PUT", "/api/p/"+slug, doc,
		merge(auth, map[string]string{"X-CV-Editor": other}))
	if w.Code != http.StatusLocked {
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

	leaseFor(t, h, slug, auth, nil) // somebody holds it

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

	first := leaseFor(t, h, slug, auth, nil)
	holder, _ := first["editor"].(string)

	call(t, h, "DELETE", "/api/p/"+slug+"/lease", nil,
		merge(auth, map[string]string{"X-CV-Editor": holder}))

	next := leaseFor(t, h, slug, auth, nil)
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

	leaseFor(t, h, slug, auth, nil)

	// Rather than waiting three quarters of a minute, move the clock.
	at := time.Now()
	s.Leases.Now = func() time.Time { return at.Add(lease.DefaultTTL * 2) }

	next := leaseFor(t, h, slug, auth, nil)
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

	first := decode(t, call(t, h, "POST", "/api/p/"+slug+"/lease", nil, auth))
	second := decode(t, call(t, h, "POST", "/api/p/"+slug+"/lease?lang=en", nil, auth))
	if first["held"] != true || second["held"] != true {
		t.Fatal("editing the default language blocked editing a translation")
	}
}
