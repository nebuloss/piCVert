// Package turnstile verifies Cloudflare's challenge.
//
// # WHAT IT IS FOR
//
// A surface strangers can reach and act on — creating a CV, most obviously.
// Without something like this, such a route is a disk somebody fills with a
// shell loop, and the per-address rate limit only slows down an attacker with
// one address.
//
// # THE CHECK IS SERVER-SIDE, AND THAT IS THE WHOLE POINT
//
// The widget in the page produces a token. A page that merely draws the widget
// and posts the form has added a picture of a security control: the token can
// be anything, or absent, and nothing notices. It is the verification below —
// our secret, Cloudflare's answer — that makes the challenge mean something.
//
// # OFF UNLESS CONFIGURED
//
// No keys, no challenge, and no request to Cloudflare. A service with nothing a
// stranger can do gains nothing from one, and would be sending every visitor to
// a third party for it.
package turnstile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// endpoint is Cloudflare's verifier.
const endpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// Verifier checks tokens, or does not.
type Verifier struct {
	Secret string
	// Client is the one used to reach Cloudflare. Its own field so a test can
	// answer without a network, and so the timeout below is stated rather than
	// inherited from a default that has none.
	Client *http.Client
}

func New(secret string) *Verifier {
	return &Verifier{
		Secret: secret,
		// A short timeout on purpose. Cloudflare being slow must not become
		// this service being slow — and a challenge that cannot be checked is
		// handled below as a refusal, not as a pass.
		Client: &http.Client{Timeout: 8 * time.Second},
	}
}

// Enabled reports whether there is anything to check.
func (v *Verifier) Enabled() bool { return v != nil && v.Secret != "" }

type answer struct {
	Success bool     `json:"success"`
	Errors  []string `json:"error-codes"`
}

// Verify asks Cloudflare whether a token is good.
//
// The remote address is passed on when known: Cloudflare uses it to spot a
// token solved on one machine and replayed from a thousand others, which is
// exactly the abuse a challenge is meant to make expensive.
func (v *Verifier) Verify(ctx context.Context, token, remoteIP string) error {
	if !v.Enabled() {
		return nil
	}
	if token == "" {
		return fmt.Errorf("the challenge was not completed")
	}

	form := url.Values{"secret": {v.Secret}, "response": {token}}
	if remoteIP != "" && remoteIP != "unknown" {
		form.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := v.Client.Do(req)
	if err != nil {
		// REFUSED, not waved through. A verifier that fails open is a verifier
		// an attacker turns off by making it unreachable — and the honest
		// answer to "I cannot tell" on a route that creates things is no.
		return fmt.Errorf("the challenge could not be checked, please try again")
	}
	defer res.Body.Close()

	var got answer
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		return fmt.Errorf("the challenge could not be checked, please try again")
	}
	if !got.Success {
		// Cloudflare's own codes are not shown: they name our configuration as
		// often as the visitor's behaviour, and "invalid-input-secret" is not
		// something to put in front of somebody trying to make a CV.
		return fmt.Errorf("the challenge was not accepted, please try again")
	}
	return nil
}
