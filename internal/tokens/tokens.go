// Package tokens holds the private links — two per CV, and stable.
//
//	edit: /e/<token>   view AND change
//	read: /e/<token>   view only; no edit button, and writes refused by the
//	                   server rather than merely hidden
//
// No account, no password to pass on, and deliberately not a collection of
// links to administer: two fixed addresses that can be shown again, and renewed
// if they leak.
//
// Tokens are stored IN CLEAR, unlike a password. That is on purpose: a link one
// cannot be shown again is no longer a stable link — it would have to be
// reissued every time it is mislaid. The file is mode 0600 and owned by the
// service account; that is where the protection comes from.
//
// A token is only good for ITS profile. Adding a second CV does not
// retroactively expose it through a link already handed out.
package tokens

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"picvert/internal/profiles"
)

// Mode is what a link grants.
type Mode string

const (
	Edit Mode = "edit"
	Read Mode = "read"
)

// Modes is both, in the order they are reported.
var Modes = []Mode{Edit, Read}

// tokenBytes is 192 bits, which is out of reach of guessing.
const tokenBytes = 24

// Links are one profile's two addresses.
type Links struct {
	Edit          string `json:"edit"`
	Read          string `json:"read"`
	CreatedAt     string `json:"createdAt,omitempty"`
	EditRotatedAt string `json:"editRotatedAt,omitempty"`
	ReadRotatedAt string `json:"readRotatedAt,omitempty"`
}

// Grant is what a verified token turned out to be.
type Grant struct {
	Slug string
	Mode Mode
}

// Store is the link file.
type Store struct {
	Profiles *profiles.Repository
	mu       sync.Mutex
}

func New(repo *profiles.Repository) *Store { return &Store{Profiles: repo} }

// File is resolved on every call: the data root can move.
func (s *Store) File() string {
	return filepath.Join(s.Profiles.DataDir(), ".share-tokens.json")
}

type database map[string]Links

func (s *Store) load() database {
	raw, err := os.ReadFile(s.File())
	if err != nil {
		return database{}
	}
	var db database
	if json.Unmarshal(raw, &db) != nil || db == nil {
		return database{}
	}
	return db
}

func (s *Store) save(db database) error {
	file := s.File()
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		return err
	}
	_ = os.Chmod(file, 0o600)
	alignOwnership(file)
	return nil
}

func newToken() string {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		// A token that is not random is not a token. Nothing here can recover
		// from a broken entropy source, and carrying on would issue a guessable
		// link to somebody's CV.
		panic("tokens: no randomness available: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// ForProfile is a profile's two links, created on first request.
func (s *Store) ForProfile(slug string) (Links, error) {
	if _, err := s.Profiles.Get(slug); err != nil {
		return Links{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	db := s.load()
	entry := db[slug]
	if entry.Edit == "" || entry.Read == "" {
		if entry.Edit == "" {
			entry.Edit = newToken()
		}
		if entry.Read == "" {
			entry.Read = newToken()
		}
		if entry.CreatedAt == "" {
			entry.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		db[slug] = entry
		if err := s.save(db); err != nil {
			return Links{}, err
		}
	}
	return entry, nil
}

// Rotate renews one link. The old one stops working at that instant.
func (s *Store) Rotate(slug string, mode Mode) (Links, error) {
	if mode != Edit && mode != Read {
		return Links{}, fmt.Errorf("unknown mode: %q (expected edit or read)", mode)
	}
	if _, err := s.ForProfile(slug); err != nil {
		return Links{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	db := s.load()
	entry := db[slug]
	stamp := time.Now().UTC().Format(time.RFC3339)
	if mode == Edit {
		entry.Edit, entry.EditRotatedAt = newToken(), stamp
	} else {
		entry.Read, entry.ReadRotatedAt = newToken(), stamp
	}
	db[slug] = entry
	if err := s.save(db); err != nil {
		return Links{}, err
	}
	return entry, nil
}

// Forget drops a profile's links for good.
//
// Called when a CV is really destroyed, not when it is merely set aside. Left
// in place, the entry makes Verify hand back a slug whose directory is gone,
// and every route trusting it then fails on a read instead of answering
// cleanly that the link is invalid.
func (s *Store) Forget(slug string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	db := s.load()
	if _, ok := db[slug]; !ok {
		return false
	}
	delete(db, slug)
	_ = s.save(db)
	return true
}

// Entry is one profile's links, with its slug, for the inventory.
type Entry struct {
	Slug string `json:"slug"`
	Links
}

// List is every profile that has links.
func (s *Store) List() []Entry {
	db := s.load()
	slugs := make([]string, 0, len(db))
	for slug := range db {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	out := make([]Entry, 0, len(slugs))
	for _, slug := range slugs {
		out = append(out, Entry{Slug: slug, Links: db[slug]})
	}
	return out
}

// Verify identifies a token.
//
// Compared in constant time, and EVERY entry is compared even after a match:
// returning early would leak, in the time taken, roughly where in the file a
// token sits.
func (s *Store) Verify(token string) (Grant, bool) {
	if len(token) < 16 {
		return Grant{}, false
	}
	want := []byte(token)
	var found Grant
	ok := false
	for slug, links := range s.load() {
		for mode, value := range map[Mode]string{Edit: links.Edit, Read: links.Read} {
			got := []byte(value)
			// Different lengths are a non-match; subtle.ConstantTimeCompare
			// returns 0 for them anyway, but says nothing about the timing.
			if len(got) == len(want) && subtle.ConstantTimeCompare(got, want) == 1 {
				found, ok = Grant{Slug: slug, Mode: mode}, true
			}
		}
	}
	return found, ok
}
