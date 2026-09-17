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

// holderCookie is how a browser remembers which editor it is.
const holderCookie = "cv_editor"

// windowHeader is which TAB of that browser is asking.
//
// A cookie identifies a browser, and a browser can have the same CV open in two
// tabs — which would then share an identity and both believe they hold the
// lease. The two halves together are what make an editing WINDOW:
//
//	cookie   durable, unreadable by script, distinct per browser
//	header   per tab, from sessionStorage, and survives a reload of that tab
//
// Forging the header buys nothing. Doing so requires the cookie as well, and
// anybody who has that is already this browser — so the worst available attack
// is impersonating one's own other tab.
const windowHeader = "X-CV-Window"

// holderOf reads the identity a browser was given, if it has one.
//
// # WHY A COOKIE
//
// It was minted per page load and kept in a JavaScript variable, and that had
// one obvious consequence nobody enjoys: pressing F5 produced a NEW identity,
// the server quite correctly refused it because the old one still held the
// lease, and the person was told somebody else was editing their CV. That
// somebody was them, for three quarters of a minute.
//
// A cookie is the browser's own memory of itself, which is exactly what this
// needs to be. A reload carries it, so a reload gets the same lease back.
//
// Three other things follow, and each of them removes something:
//
//   - It can be HttpOnly, so no script can read it and none can forge one. The
//     header it replaces was set by page script, which could therefore have
//     claimed to be any window it liked.
//   - The beacon a closing tab sends carries it automatically, so the identity
//     no longer has to travel in a query string — where it went into every
//     proxy log on the way.
//   - Nothing in the interface tracks it at all. The browser does it.
//
// It is not a credential. It says WHICH WINDOW you are, not that you may edit;
// a write still needs the link token as well. Two people who somehow shared one
// would be two people sharing a browser, and they have larger problems.
func holderOf(r *http.Request) lease.Holder {
	c, err := r.Cookie(holderCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	return joinHolder(c.Value, tabOf(r))
}

// editorOf is holderOf, giving a browser an identity if it has none yet.
//
// Separate from holderOf, and the separation is load-bearing twice over.
//
// It mints, so only ASKING FOR A LEASE creates an identity. A write does not:
// the command line and anyone with curl present no identity and are let
// through, and minting one for them would turn them into a window that holds
// nothing and is refused.
//
// And it composes the SAME WAY holderOf does — which is the bug this shape
// exists to prevent. The first request has no cookie, so the first version
// minted one and took the lease under the browser half ALONE, while every
// request after it presented browser-and-tab. The first window therefore held
// a lease under an identity it could never present again, and was locked out
// of its own CV by itself. It took a log line to see, because every individual
// answer was correct.
func (s *Server) editorOf(w http.ResponseWriter, r *http.Request) lease.Holder {
	browser := ""
	if c, err := r.Cookie(holderCookie); err == nil {
		browser = c.Value
	}
	if browser == "" {
		browser = string(lease.NewHolder())
		rememberHolder(w, r, lease.Holder(browser))
	}
	return joinHolder(browser, tabOf(r))
}

// tabOf is which tab of the browser is asking, if it says.
//
// A beacon cannot set a header, so it puts the same value in the query string.
// The cookie is what makes either of them trustworthy: without it the tab name
// identifies nothing.
func tabOf(r *http.Request) string {
	if tab := r.Header.Get(windowHeader); tab != "" {
		return tab
	}
	return r.URL.Query().Get("window")
}

// joinHolder makes one identity out of the browser and the tab.
//
// ONE function, used by both readers, because two spellings of this is exactly
// how a lease comes to be held under a name nobody can say again.
func joinHolder(browser, tab string) lease.Holder {
	if tab == "" {
		return lease.Holder(browser)
	}
	return lease.Holder(browser + ":" + tab)
}

// rememberHolder gives a browser an identity to send back.
func rememberHolder(w http.ResponseWriter, r *http.Request, holder lease.Holder) {
	http.SetCookie(w, &http.Cookie{
		Name:  holderCookie,
		Value: string(holder),
		// Site-wide, because the identity is the BROWSER rather than one CV:
		// somebody with two CVs open is one editor of each, not two of one.
		Path: "/",
		// Unreadable by script. It is set here and sent by the browser, and no
		// code in the page ever touches it — so an injected script cannot read
		// it, and cannot claim to be somebody else's window.
		HttpOnly: true,
		// Never sent from another site, so nothing can make a browser act as
		// this editor from somewhere else.
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		// A session cookie: it lasts as long as the browser is open, which is
		// as long as an editing session can possibly last. A dated one would
		// outlive every lease it could ever name.
	})
}

// leaseRoutes are the two the editor calls.
func (s *Server) leaseRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/p/{slug}/lease", s.owned(func(w http.ResponseWriter, r *http.Request, p *profiles.Profile) {
		key := leaseKey(p.Slug, lang(r))
		// Minting here rather than in holderOf: asking for a lease is the one
		// request that may create an identity.
		holder := s.editorOf(w, r)

		// A beacon can only POST, so a release arrives here rather than as a
		// DELETE. Handled before anything else, because it is the one request
		// that must work while a page is being torn down — and it carries the
		// identity by itself, being a same-origin request with a cookie.
		if r.URL.Query().Get("release") == "1" {
			s.Leases.Release(key, holder)
			sendJSON(w, map[string]any{"ok": true})
			return
		}

		var grant lease.Grant
		if r.URL.Query().Get("renew") == "1" {
			// A heartbeat, which may extend a lease but never create one. That
			// is what makes the inactivity timeout mean anything: a tab left
			// open says hello for ever and works never, and only Acquire
			// resets the idle clock.
			grant = s.Leases.Renew(key, holder)
		} else {
			grant = s.Leases.Acquire(key, holder, nameOf(p))
		}

		answer := map[string]any{
			"ok":   true,
			"held": grant.OK,
			// How long an editor may stay silent, so it can choose its own
			// heartbeat rather than having one hard-coded on both sides.
			//
			// The identity is NOT reported: it is a cookie the browser keeps
			// and no script can read, and echoing it into a response body would
			// undo exactly that.
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
