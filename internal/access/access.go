// Package access answers one question: which CVs are readable WITHOUT a link,
// at an address someone could simply type?
//
// NOTHING, unless it is named. That is the whole point of a service whose
// access rests on links: an address one can guess is an address one can find,
// and a CV carries a name, an email address and an employer.
//
//	PICVERT_PUBLIC unset    nothing public — links only          (default)
//	PICVERT_PUBLIC=a,b      those two, by name
//	PICVERT_PUBLIC=*        everything, including CVs made by strangers
//	PICVERT_HOME=a          “a” is served at “/”, so necessarily public
//
// Being the DEFAULT profile does not imply being public. It used to, and that
// meant there was no way to run this service with no public surface at all.
//
// THE RULE IS STATED HERE ONCE. The public server enforces it and the admin
// interface reports it; a second copy of the reasoning would eventually
// disagree with the first, and the admin would then show a CV as private while
// the world could read it.
package access

import (
	"os"
	"strings"
)

// Policy reads its settings on every call rather than at construction, so what
// the server enforces is what the environment says at the moment of the
// request — and so the setting can be exercised in a test without starting a
// second process.
type Policy struct {
	PublicOf func() string
	HomeOf   func() string
}

// New reads the process environment.
func New() *Policy {
	return &Policy{
		PublicOf: func() string { return strings.TrimSpace(os.Getenv("PICVERT_PUBLIC")) },
		HomeOf:   func() string { return strings.TrimSpace(os.Getenv("PICVERT_HOME_PROFILE")) },
	}
}

// named is the slugs listed one by one, “*” aside.
func (p *Policy) named() []string {
	list := p.PublicOf()
	if list == "" || list == "*" {
		return nil
	}
	var out []string
	for _, s := range strings.Split(list, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// IsPublic reports whether a slug may be read without a link.
func (p *Policy) IsPublic(slug string) bool {
	if slug == "" {
		return false
	}
	if p.PublicOf() == "*" {
		return true
	}
	// Naming a profile as the home page IS the request to publish it: it is
	// served at an address nobody has to be given.
	if home := p.HomeOf(); home != "" && slug == home {
		return true
	}
	for _, s := range p.named() {
		if s == slug {
			return true
		}
	}
	return false
}

// Describe is how the current setting reads, for the admin inventory.
func (p *Policy) Describe() string {
	if p.PublicOf() == "*" {
		return "all"
	}
	var named []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			named = append(named, s)
		}
	}
	add(p.HomeOf())
	for _, s := range p.named() {
		add(s)
	}
	if len(named) == 0 {
		return "none — by private link only"
	}
	return strings.Join(named, ", ")
}
