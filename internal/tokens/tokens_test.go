package tokens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"picvert/internal/profiles"
)

// store builds a token store over a scratch data directory holding one CV.
func store(t *testing.T) (*Store, string) {
	t.Helper()
	data := t.TempDir()
	if err := os.MkdirAll(filepath.Join(data, "jean"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "jean", "cv.json"),
		[]byte(`{"meta":{},"content":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := profiles.New(data)
	repo.DataDirOf = func() string { return data }
	return New(repo), data
}

// A profile's links live inside the profile.
//
// That is what makes a folder a CV: copied to another machine it arrives on
// the same links, and set aside it keeps them, because the links are part of
// what moves. They used to live in one file for the whole service, which is
// why restoring a deleted CV returned it on new links and locked out everybody
// holding an old one.
func TestLinksLiveInsideTheProfile(t *testing.T) {
	s, data := store(t)

	links, err := s.ForProfile("jean")
	if err != nil {
		t.Fatal(err)
	}
	if links.Edit == "" || links.Read == "" {
		t.Fatal("no links were minted")
	}

	inside := filepath.Join(data, "jean", "links.json")
	if _, err := os.Stat(inside); err != nil {
		t.Fatalf("the links are not in the profile folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(data, ".share-tokens.json")); err == nil {
		t.Fatal("a service-wide link file was written — the folder is not self-contained")
	}
}

// Moving the folder moves the CV, links and all.
//
// This is the property the old arrangement lacked, and the one that makes
// setting a CV aside and restoring it work without anybody copying tokens
// about by hand.
func TestAProfileFolderCarriesItsLinks(t *testing.T) {
	s, data := store(t)
	before, err := s.ForProfile("jean")
	if err != nil {
		t.Fatal(err)
	}

	// Somewhere else entirely: another machine, another data directory.
	elsewhere := t.TempDir()
	if err := os.Rename(filepath.Join(data, "jean"), filepath.Join(elsewhere, "jean")); err != nil {
		t.Fatal(err)
	}
	other := profiles.New(elsewhere)
	other.DataDirOf = func() string { return elsewhere }

	after, err := New(other).ForProfile("jean")
	if err != nil {
		t.Fatal(err)
	}
	if after.Edit != before.Edit || after.Read != before.Read {
		t.Fatal("the CV arrived on different links — the folder did not carry them")
	}
}

// A service upgraded in place keeps handing out the links it already gave.
//
// Every machine installed before this has one shared file. Minting fresh links
// on first read would silently break every link already handed out — which is
// the one thing this service must never do, since a link is the only way into
// a CV and there is no account to recover it with.
func TestLinksFromTheOldSharedFileAreHonoured(t *testing.T) {
	s, data := store(t)

	old := map[string]Links{"jean": {
		Edit: "an-edit-token-long-enough-to-pass", Read: "a-read-token-long-enough-to-pass",
		CreatedAt: "2026-01-01T00:00:00Z",
	}}
	raw, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(data, ".share-tokens.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	links, err := s.ForProfile("jean")
	if err != nil {
		t.Fatal(err)
	}
	if links.Edit != old["jean"].Edit || links.Read != old["jean"].Read {
		t.Fatal("an upgrade minted new links and broke every one already handed out")
	}
	// And the token still resolves, which is what somebody following an old
	// link actually depends on.
	grant, ok := s.Verify(old["jean"].Edit)
	if !ok || grant.Slug != "jean" || grant.Mode != Edit {
		t.Fatal("a link from before the upgrade no longer opens its CV")
	}
}

// Renewing replaces one link and leaves the other alone.
func TestRenewingOneLinkLeavesTheOther(t *testing.T) {
	s, _ := store(t)
	before, err := s.ForProfile("jean")
	if err != nil {
		t.Fatal(err)
	}

	after, err := s.Rotate("jean", Read)
	if err != nil {
		t.Fatal(err)
	}
	if after.Read == before.Read {
		t.Fatal("the read link was not renewed")
	}
	if after.Edit != before.Edit {
		t.Fatal("renewing the read link changed the edit one")
	}
	if _, ok := s.Verify(before.Read); ok {
		t.Fatal("the old read link still opens the CV")
	}
	if _, ok := s.Verify(after.Edit); !ok {
		t.Fatal("the untouched edit link stopped working")
	}
}

// Reading old links MOVES them into the profile.
//
// A fallback that is only ever read is not a migration — it is a second place
// to look, forever, which is exactly the arrangement this change removes. The
// first read after an upgrade must leave the profile self-contained.
func TestOldLinksAreMovedIntoTheProfile(t *testing.T) {
	s, data := store(t)

	old := map[string]Links{"jean": {
		Edit: "an-edit-token-long-enough-to-pass", Read: "a-read-token-long-enough-to-pass",
	}}
	raw, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(data, ".share-tokens.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ForProfile("jean"); err != nil {
		t.Fatal(err)
	}

	inside := filepath.Join(data, "jean", "links.json")
	if _, err := os.Stat(inside); err != nil {
		t.Fatalf("the links were read but not moved in: %v", err)
	}

	// Proof it no longer depends on the old file: take it away entirely.
	if err := os.Remove(filepath.Join(data, ".share-tokens.json")); err != nil {
		t.Fatal(err)
	}
	links, err := s.ForProfile("jean")
	if err != nil {
		t.Fatal(err)
	}
	if links.Edit != old["jean"].Edit {
		t.Fatal("the links did not survive the old file being removed")
	}
}
