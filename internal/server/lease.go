package server

import (
	"fmt"
	"net/http"
	"time"

	"picvert/internal/lease"
	"picvert/internal/profiles"
)

// The editing lease, over HTTP.
//
// One editor at a time per document, so that two people holding the same link
// do not both type and one of them lose the afternoon. The refusal-on-save that
// this sits in front of is still there and still matters — see
// store.WriteIfUnchanged — because a lease can lapse while somebody is mid
// sentence, and because the command line does not take one.
//
//	POST   /api/p/{slug}/lease        ask to edit, or say you are still here
//	DELETE /api/p/{slug}/lease        give it up, closing a tab
//
// The holder identity is minted by the server and kept by one tab. It cannot be
// the link token: the whole situation this exists for is two people holding the
// SAME link.

// leaseKey identifies one document. Per language, because the French CV and the
// English one are two documents and editing one must not lock the other.
func leaseKey(slug, language string) string {
	if language == "" {
		language = "default"
	}
	return slug + "|" + language
}

// holderHeader is how an editor says which session it is.
const holderHeader = "X-CV-Editor"

// holderOf reads the identity from a header, or from the query string.
//
// The query string is for ONE caller: the beacon a tab sends as it closes.
// sendBeacon cannot set headers, and a lease released the moment a window goes
// is the difference between the next person waiting a moment and waiting the
// whole timeout. It is not a weakening — the identity is not a secret, it only
// says which window you are, and a write still needs the link token as well.
func holderOf(r *http.Request) lease.Holder {
	if h := r.Header.Get(holderHeader); h != "" {
		return lease.Holder(h)
	}
	return lease.Holder(r.URL.Query().Get("editor"))
}

// leaseRoutes are the two the editor calls.
func (s *Server) leaseRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/p/{slug}/lease", s.owned(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		key := leaseKey(p.Slug, lang(r))
		holder := holderOf(r)

		// A beacon can only POST, so a release arrives here rather than as a
		// DELETE. Handled before anything else, because it is the one request
		// that must work while a page is being torn down.
		if r.URL.Query().Get("release") == "1" {
			s.Leases.Release(key, holder)
			sendJSON(w, map[string]any{"ok": true})
			return
		}

		var grant lease.Grant
		switch {
		case holder == "":
			// A fresh editor with no identity yet. Minted here so that a client
			// cannot choose its own — one that did could name itself whatever
			// the current holder is called and take the lease.
			grant = s.Leases.Acquire(key, lease.NewHolder(), nameOf(p))
		case r.URL.Query().Get("renew") == "1":
			// A heartbeat, which may extend a lease but never create one. That
			// is what makes the inactivity timeout mean anything: a tab left
			// open says hello for ever and works never, and only Acquire
			// resets the idle clock.
			grant = s.Leases.Renew(key, holder)
		default:
			grant = s.Leases.Acquire(key, holder, nameOf(p))
		}

		answer := map[string]any{
			"ok":     true,
			"held":   grant.OK,
			"editor": string(grant.Holder),
			// How long an editor may stay silent, so it can choose its own
			// heartbeat rather than having one hard-coded on both sides.
			"heartbeatMs": lease.DefaultHeartbeat.Milliseconds(),
		}
		if grant.OK {
			answer["untilMs"] = time.Until(grant.Until).Milliseconds()
		} else {
			answer["heldBy"] = grant.HeldBy
			// What the waiting page counts down to. It is when the current
			// holder goes stale, not a promise — they may carry on, and then
			// the wait simply continues.
			answer["freeInMs"] = max(0, time.Until(grant.Free).Milliseconds())
		}
		sendJSON(w, answer)
	}))

	release := s.owned(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		s.Leases.Release(leaseKey(p.Slug, lang(r)), holderOf(r))
		sendJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("DELETE /api/p/{slug}/lease", release)
}

// requireLease refuses a write from anybody who is not the current editor.
//
// WITHOUT THIS THE LEASE IS DECORATION: a courtesy the interface observes and
// anything else ignores, and the second editor's changes land anyway. The
// check belongs at the write, not at the page that draws the form.
//
// A caller that presents no holder at all is let through. The command line and
// anyone with curl have no lease and no way to take one, and refusing them
// would make the API unusable by hand to enforce a rule they are not part of —
// they are still covered by the revision check underneath.
func (s *Server) requireLease(h func(http.ResponseWriter, *http.Request, *profiles.Profile)) func(http.ResponseWriter, *http.Request, *profiles.Profile) {
	return func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		holder := holderOf(r)
		if holder == "" {
			h(w, r, p)
			return
		}
		key := leaseKey(p.Slug, lang(r))
		if !s.Leases.Holds(key, holder) {
			held, _ := s.Leases.Current(key)
			fail(w, http.StatusLocked, fmt.Errorf(
				"somebody else is editing this CV%s", by(held.Name)))
			return
		}
		// A write is the definition of working, so it holds off the inactivity
		// timeout. Nothing else does — least of all the heartbeat.
		s.Leases.Touch(key, holder)
		h(w, r, p)
	}
}

func by(name string) string {
	if name == "" {
		return ""
	}
	return " (" + name + ")"
}
