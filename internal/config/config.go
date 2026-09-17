// Package config is every setting, in one place, from a file a person can read.
//
// # WHY A FILE, GIVEN THE ENVIRONMENT ALREADY WORKED
//
// Twenty environment variables is a configuration nobody can see. There is no
// way to look at a running service and know what it was told, no way to write
// a comment saying why a number is what it is, and no way to review a change
// to it — the setting and the reason for it live in different places, and one
// of them is a systemd file somebody edits at midnight.
//
// A file can be read, commented, kept in git and diffed.
//
// # THE FILE IS NEVER WRITTEN
//
// Nothing here rewrites it. That is what lets your comments survive, and what
// makes it safe to keep under version control: a service that rewrote its own
// configuration would produce a diff on every restart and eventually lose the
// comment explaining the setting somebody is about to change.
//
// Things that CHANGE — the links, the leases, the CVs — live in the data
// directory, and nothing there is hand-edited.
//
// # THE ENVIRONMENT STILL WINS
//
// A container is configured by environment, and telling somebody to bake a file
// into an image to set one address is worse than the variable it replaced. So
// the file is the readable default and the environment overrides it, which is
// also the order that makes a secret injectable without ever being written to
// disk.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the whole of what this service can be told.
type Config struct {
	// Domain is the address the world reaches this service at, with its scheme:
	// https://cv.example.com
	//
	// It is what every link the service hands out is built from — the private
	// links in the admin page, the read-only link in the editor, the address
	// printed by `picvert new`. Without it those come out as bare paths, which
	// are no use to somebody who has to paste one into a message.
	//
	// It cannot be worked out from a request. Behind a reverse proxy the Host
	// header is the proxy's idea of the service and the scheme is plain HTTP,
	// whatever the world outside sees.
	Domain string `yaml:"domain"`

	// Listen is the public address. Behind a proxy this stays on localhost.
	Listen string `yaml:"listen"`

	Admin     Admin     `yaml:"admin"`
	Turnstile Turnstile `yaml:"turnstile"`
	Access    Access    `yaml:"access"`
	Limits    Limits    `yaml:"limits"`
	Editing   Editing   `yaml:"editing"`
	Security  Security  `yaml:"security"`

	// DataDir is where the CVs live: the one directory worth backing up.
	DataDir string `yaml:"data-dir"`
	// TemplateDir and FontDir are only needed when running from a checkout.
	// An installed binary carries both.
	Home string `yaml:"home"`
	// Template is the layout a CV gets when it names none.
	Template string `yaml:"template"`
	// Profile is the CV served at "/" instead of the front page. Naming one
	// publishes it.
	Profile string `yaml:"home-profile"`
}

// Admin is the administration interface, on its own port.
type Admin struct {
	Listen string `yaml:"listen"`

	// Password is a HASH, never a password. See password.go — `picvert passwd`
	// prints one to paste here.
	//
	// # THIS CHANGES A DESIGN DECISION, DELIBERATELY
	//
	// The admin port has no access control by design: what protects it is that
	// it is not reachable from outside, and this file has said in several
	// places that a password would make it the one surface here worth
	// attacking.
	//
	// That reasoning holds for a service where the port is not exposed, and it
	// is still the default: empty means no password and no login page, exactly
	// as before. What it does not cover is somebody who WANTS the port
	// reachable — over a VPN, through a proxy, from another machine — and for
	// them "do not do that" is advice they will ignore rather than follow.
	//
	// So: a password is defence in depth when the port is private, and the
	// thing that makes exposing it merely unwise rather than reckless. It is
	// not permission to proxy it to the internet.
	Password string `yaml:"password"`

	// Session is how long a login lasts.
	Session time.Duration `yaml:"session"`
}

// Turnstile is Cloudflare's challenge, for surfaces open to strangers.
//
// Off unless both keys are set, and off is the default: a challenge on a
// service nobody can create a CV on protects nothing and costs every visitor a
// request to Cloudflare.
type Turnstile struct {
	// SiteKey is public and goes into the page.
	SiteKey string `yaml:"site-key"`
	// Secret is not, and is verified server-side. A challenge checked only in
	// the browser is a challenge that is not checked.
	Secret string `yaml:"secret"`
}

// Access is who may read what without a link.
type Access struct {
	// Public names the CVs readable at a guessable address. Empty means none,
	// which is the default and the right one: a CV carries a name, an email
	// address and an employer.
	//
	// "*" publishes everything, including CVs somebody else made.
	Public []string `yaml:"public"`
	// TrustProxy honours X-Forwarded-For. Only say yes behind a proxy you
	// control: trusting it unconditionally lets anyone claim a fresh address
	// per request and makes the failure throttle decorative.
	TrustProxy bool `yaml:"trust-proxy"`
}

// Limits are the ceilings.
type Limits struct {
	// MaxProfileMB is what one CV may occupy, in total.
	MaxProfileMB int64 `yaml:"max-profile-mb"`
	// MinFreeMB is how much room must remain on the disk after a write.
	MinFreeMB int64 `yaml:"min-free-mb"`
	// MaxPhotoMB is the largest portrait accepted.
	MaxPhotoMB int64 `yaml:"max-photo-mb"`
	// HistoryEntries is how many journal entries a CV keeps.
	HistoryEntries int `yaml:"history-entries"`
	// TrashHours is how long a deleted CV can still be recovered.
	TrashHours int `yaml:"trash-hours"`
}

// Editing is the one-editor-at-a-time lease.
type Editing struct {
	// LeaseTTL is how long a window may go silent before it loses the lease.
	LeaseTTL time.Duration `yaml:"lease-ttl"`
	// Inactivity is how long a window may hold it without changing anything.
	Inactivity time.Duration `yaml:"inactivity"`
	// EpisodeMinutes is how long a field stays "being edited" in the journal.
	EpisodeMinutes int `yaml:"episode-minutes"`
}

// Security is the headers and the throttle.
type Security struct {
	// RateMaxFailures is how many bad links an address may try.
	RateMaxFailures int `yaml:"rate-max-failures"`
	// RateWindowMinutes is how long those are counted over.
	RateWindowMinutes int `yaml:"rate-window-minutes"`
	// HSTS is "", "off" or "subdomains". Deliberately without subdomains by
	// default: from an apex it would force HTTPS on every neighbour for a year.
	HSTS string `yaml:"hsts"`
	// FrameAncestors is who may show these pages in an iframe.
	FrameAncestors string `yaml:"frame-ancestors"`
}

// Defaults is the configuration of a service told nothing at all.
//
// Every one of these is a working value, so an empty file is a running service
// — and the example file can then show the defaults rather than a set of
// choices somebody has to make before starting.
func Defaults() Config {
	return Config{
		Listen: "127.0.0.1:3000",
		Admin: Admin{
			Listen:  "127.0.0.1:3001",
			Session: 12 * time.Hour,
		},
		Template: "material-you",
		Limits: Limits{
			MaxProfileMB:   8,
			MinFreeMB:      64,
			MaxPhotoMB:     4,
			HistoryEntries: 500,
			TrashHours:     24,
		},
		Editing: Editing{
			LeaseTTL:       45 * time.Second,
			Inactivity:     15 * time.Minute,
			EpisodeMinutes: 5,
		},
		Security: Security{
			RateMaxFailures:   10,
			RateWindowMinutes: 15,
			FrameAncestors:    "'self'",
		},
	}
}

// Load reads a file, then lets the environment override it.
//
// A missing file is not an error: the defaults are a working service, and
// requiring a file to start would make the simplest deployment the one that
// needs the most explaining.
func Load(path string) (Config, error) {
	c := Defaults()

	if path != "" {
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			// KnownFields, so a typo is an error rather than a setting that
			// silently does nothing. A misspelt key in a configuration file is
			// the most expensive kind of quiet failure: everything starts, and
			// the thing you configured is not configured.
			decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
			decoder.KnownFields(true)
			if err := decoder.Decode(&c); err != nil {
				return c, fmt.Errorf("%s: %w", path, err)
			}
		case os.IsNotExist(err):
			// Only a problem if somebody ASKED for this file by name.
			if os.Getenv("PICVERT_CONFIG") != "" {
				return c, fmt.Errorf("%s: no such file", path)
			}
		default:
			return c, err
		}
	}

	c.overrideFromEnv()
	return c, c.check()
}

// overrideFromEnv lets the environment win.
//
// Last, and deliberately: a container is configured by environment, and a
// secret injected that way is a secret never written to a disk. The file is the
// readable default; the environment is the deployment.
func (c *Config) overrideFromEnv() {
	str := func(name string, into *string) {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			*into = v
		}
	}
	num := func(name string, into *int64) {
		if v, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64); err == nil && v > 0 {
			*into = v
		}
	}

	str("PICVERT_DOMAIN", &c.Domain)
	str("PICVERT_PUBLIC_URL", &c.Domain) // the name this used to have
	str("PICVERT_ADDR", &c.Listen)
	str("PICVERT_ADMIN_ADDR", &c.Admin.Listen)
	str("PICVERT_ADMIN_PASSWORD", &c.Admin.Password)
	str("PICVERT_TURNSTILE_SITE_KEY", &c.Turnstile.SiteKey)
	str("PICVERT_TURNSTILE_SECRET", &c.Turnstile.Secret)
	str("PICVERT_DATA", &c.DataDir)
	str("PICVERT_HOME", &c.Home)
	str("PICVERT_TEMPLATE", &c.Template)
	str("PICVERT_HOME_PROFILE", &c.Profile)
	str("PICVERT_HSTS", &c.Security.HSTS)
	str("PICVERT_FRAME_ANCESTORS", &c.Security.FrameAncestors)

	num("PICVERT_MAX_PROFILE_MB", &c.Limits.MaxProfileMB)
	num("PICVERT_MIN_FREE_MB", &c.Limits.MinFreeMB)

	if v := strings.TrimSpace(os.Getenv("PICVERT_PUBLIC")); v != "" {
		if v == "*" {
			c.Access.Public = []string{"*"}
		} else {
			c.Access.Public = nil
			for _, slug := range strings.Split(v, ",") {
				if slug = strings.TrimSpace(slug); slug != "" {
					c.Access.Public = append(c.Access.Public, slug)
				}
			}
		}
	}
	if os.Getenv("PICVERT_TRUST_PROXY") == "1" {
		c.Access.TrustProxy = true
	}
}

// check refuses a configuration that cannot mean what it says.
//
// Only the things that are WRONG rather than merely unset. A domain with no
// scheme produces links nobody can click, and a password that is not a hash is
// a password somebody has pasted in plaintext believing it was protected —
// both are worth refusing to start over, because both fail silently later.
func (c *Config) check() error {
	if c.Domain != "" {
		if !strings.HasPrefix(c.Domain, "http://") && !strings.HasPrefix(c.Domain, "https://") {
			return fmt.Errorf(
				"domain %q needs a scheme: write https://%s", c.Domain, c.Domain)
		}
		c.Domain = strings.TrimRight(c.Domain, "/")
	}
	if c.Admin.Password != "" && !IsHash(c.Admin.Password) {
		return fmt.Errorf(
			"admin.password is not a hash — it looks like a plaintext password.\n" +
				"Run `picvert passwd` and paste what it prints")
	}
	if c.Turnstile.SiteKey != "" && c.Turnstile.Secret == "" {
		return fmt.Errorf("turnstile.site-key is set without turnstile.secret — " +
			"a challenge checked only in the browser is not checked")
	}
	return nil
}

// LinkTo builds an address somebody can paste into a message.
//
// ONE function, so that every link this service hands out is spelt the same
// way: the admin inventory, the editor's share panel, `picvert new`. Three
// places building a URL is three places to forget the domain.
func (c Config) LinkTo(path string) string {
	if c.Domain == "" {
		return path
	}
	return c.Domain + path
}

// IsPublic reports whether a CV may be read without a link.
func (c Config) IsPublic(slug string) bool {
	if slug == "" {
		return false
	}
	if c.Profile != "" && slug == c.Profile {
		return true
	}
	for _, named := range c.Access.Public {
		if named == "*" || named == slug {
			return true
		}
	}
	return false
}

// DescribeAccess is how the current setting reads, for the admin inventory.
func (c Config) DescribeAccess() string {
	for _, named := range c.Access.Public {
		if named == "*" {
			return "all"
		}
	}
	named := append([]string{}, c.Access.Public...)
	if c.Profile != "" {
		named = append([]string{c.Profile}, named...)
	}
	if len(named) == 0 {
		return "none — by private link only"
	}
	return strings.Join(named, ", ")
}
