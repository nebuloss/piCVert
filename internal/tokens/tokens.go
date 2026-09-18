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

// Store is the links, kept inside the profile they belong to.
//
// # WHY NOT ONE FILE FOR THE WHOLE SERVICE
//
// It was one file, and that made a profile folder something less than a CV.
// Its two links are the ONLY credentials a CV has, and they lived somewhere
// else — so copying a profile to another machine produced a CV on different
// links, and setting one aside lost them entirely. The administration page
// promised "restored, with its original links" and returned the CV on new
// ones, locking out everybody who had been given one.
//
// That was patched by carrying the links through the trash note by hand. This
// removes the need: the links are in the folder, so they move when it moves,
// and Trash and Restore go back to being a rename.
//
// Every write also used to take a global lock, read the whole database, change
// one entry and write the whole database back. Two people editing two
// different CVs contended on one file for no reason. Now a profile's links are
// written by the profile's own path.
type Store struct {
	Profiles *profiles.Repository
	mu       sync.Mutex
}

func New(repo *profiles.Repository) *Store { return &Store{Profiles: repo} }

// file is where one profile keeps its links.
func (s *Store) file(slug string) string {
	return filepath.Join(s.Profiles.DataDir(), slug, "links.json")
}

// legacy is the single file this used to be, still read so that a service
// upgraded in place keeps handing out the links it has already given away.
func (s *Store) legacy() string {
	return filepath.Join(s.Profiles.DataDir(), ".share-tokens.json")
}

func (s *Store) read(slug string) Links {
	if raw, err := os.ReadFile(s.file(slug)); err == nil {
		var l Links
		if json.Unmarshal(raw, &l) == nil && l.Edit != "" {
			return l
		}
	}
	// Not there: this may be a service that has just been upgraded, whose
	// links are still in the old shared file. Read them rather than mint new
	// ones — new ones would silently break every link already handed out.
	if raw, err := os.ReadFile(s.legacy()); err == nil {
		var db map[string]Links
		if json.Unmarshal(raw, &db) == nil {
			return db[slug]
		}
	}
	return Links{}
}

func (s *Store) write(slug string, l Links) error {
	dir := filepath.Join(s.Profiles.DataDir(), slug)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	file := s.file(slug)
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Chmod(file, 0o600)
	// Same reason as the document beside it: written by root over SSH while
	// the service runs as its own account, a 0600 file it cannot read makes
	// every link answer 403 with nothing pointing at ownership.
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

	entry := s.read(slug)
	if entry.Edit != "" && entry.Read != "" {
		// Read, but possibly from the old service-wide file. Written back into
		// the profile so the upgrade actually completes: a fallback that is
		// only ever read is not a migration, it is a second place to look
		// forever — which is the thing this change exists to remove.
		if _, err := os.Stat(s.file(slug)); err != nil {
			_ = s.write(slug, entry)
		}
		return entry, nil
	}
	if entry.Edit == "" {
		entry.Edit = newToken()
	}
	if entry.Read == "" {
		entry.Read = newToken()
	}
	if entry.CreatedAt == "" {
		entry.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := s.write(slug, entry); err != nil {
		return Links{}, err
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

	entry := s.read(slug)
	stamp := time.Now().UTC().Format(time.RFC3339)
	if mode == Edit {
		entry.Edit, entry.EditRotatedAt = newToken(), stamp
	} else {
		entry.Read, entry.ReadRotatedAt = newToken(), stamp
	}
	if err := s.write(slug, entry); err != nil {
		return Links{}, err
	}
	return entry, nil
}

// Forget drops a profile's links for good.
//
// Only for a CV that is really destroyed. A CV merely SET ASIDE keeps its
// links without anybody arranging it: they are in the folder, and the folder
// is what moves. That is the whole point of keeping them there — the previous
// arrangement had to copy them into the trash note and put them back by hand,
// and got it wrong, so restoring locked people out.
func (s *Store) Forget(slug string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.Remove(s.file(slug)) == nil
}

// List is every profile that has links, for the inventory.
func (s *Store) List() []Entry {
	var out []Entry
	for _, p := range s.Profiles.List() {
		if l := s.read(p.Slug); l.Edit != "" {
			out = append(out, Entry{Slug: p.Slug, Links: l})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// Entry is one profile's links, with its slug, for the inventory.
type Entry struct {
	Slug string `json:"slug"`
	Links
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
	// Every profile is read. That was already true — the single file held them
	// all and was walked in full — so nothing got slower by splitting it up,
	// and the constant-time property below is unchanged.
	for _, entry := range s.List() {
		slug, links := entry.Slug, entry.Links
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
