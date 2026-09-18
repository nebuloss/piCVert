package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"picvert/internal/profiles"
	"picvert/internal/security"
)

// Making a CV from the front page.
//
// # THIS ROUTE ONLY EXISTS WHEN A CHALLENGE IS CONFIGURED
//
// A route that lets anybody create something is a disk somebody fills with a
// shell loop, and a per-address limit only slows down an attacker who has one
// address. Without Turnstile there is no form and no route — the front page
// says so, and CVs come from the admin port or the command line, which is the
// arrangement this service had before and is still right for a private one.
//
// So the challenge is not decoration bolted onto an existing route: it is the
// thing that makes the route safe enough to exist.
//
// # THREE GATES, AND EACH STOPS A DIFFERENT ATTACK
//
//	the challenge      a script with no browser
//	the address limit  one browser, in a loop
//	the quota          a CV made honestly and then grown without end
//
// None of them is sufficient. A challenge can be solved once and replayed; an
// address limit is nothing to a botnet; a quota does not care how the CV got
// there.

// newProfileLimit is how many CVs one address may make before it waits.
//
// Deliberately small. Somebody making a CV makes one, occasionally two — a
// second for a different language of their name, a third after a mistake. Ten
// is far past anything honest and far below what would be useful to abuse.
const newProfileLimit = 5

// publicNew creates a CV from the front page.
func (s *Server) publicNew(w http.ResponseWriter, r *http.Request) {
	if !s.Challenge.Enabled() {
		// Not 404: a person who found this route deserves to know why it is
		// closed rather than to think the service is broken.
		fail(w, http.StatusNotFound, fmt.Errorf(
			"this service does not take new CVs from the web"))
		return
	}

	ip := security.ClientIP(r)
	if s.Guard.Refuse(w, r) {
		s.Metrics.Refused()
		return
	}

	var body struct {
		Name     string `json:"name"`
		Slug     string `json:"slug"`
		Lang     string `json:"lang"`
		Template string `json:"template"`
		Token    string `json:"turnstile"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}

	// The challenge FIRST, before anything is read from the disk or written to
	// it. A verification that happened after the work would make the work the
	// attack.
	if err := s.Challenge.Verify(r.Context(), body.Token, ip); err != nil {
		// Counted as a failure: a script hammering this without solving
		// anything should meet the address limit as well.
		s.Guard.RecordFailure(ip)
		fail(w, http.StatusForbidden, err)
		return
	}

	slug := strings.TrimSpace(strings.ToLower(body.Slug))
	if slug == "" {
		fail(w, http.StatusBadRequest, fmt.Errorf("a CV needs an identifier"))
		return
	}
	if err := profiles.CheckSlug(slug); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}

	if _, err := s.Store.Create(slug, strings.TrimSpace(body.Name),
		body.Template, strings.TrimSpace(body.Lang)); err != nil {
		// A name already taken is the common case and is not a failure of the
		// challenge, so it does not count against the address.
		fail(w, s.statusFor(err), err)
		return
	}
	s.Guard.RecordSuccess(ip)

	links, err := s.Tokens.ForProfile(slug)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}

	// The edit link is the ONLY way into what was just made. There is no
	// account, no email and nothing to recover it with — so it comes back in
	// the response and the page shows it before anything else.
	security.NoCache(w)
	sendJSON(w, map[string]any{
		"ok":   true,
		"slug": slug,
		"edit": s.Config.LinkTo("/e/" + links.Edit + "/edit/"),
		"read": s.Config.LinkTo("/e/" + links.Read + "/"),
	})
}
