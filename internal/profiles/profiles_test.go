package profiles

import (
	"os"
	"path/filepath"
	"testing"
)

// Count must agree with List about what a profile is, and must not open any.
//
// Two functions answering "which directories are profiles" is two chances to
// disagree — and a /healthz that counts differently from the page listing them
// is a monitoring signal nobody can reconcile with what they see.
func TestCountAgreesWithListWithoutReadingDocuments(t *testing.T) {
	dir := t.TempDir()
	repo := New(dir)
	repo.DataDirOf = func() string { return dir }

	write := func(slug, body string) {
		if err := os.MkdirAll(filepath.Join(dir, slug), 0o700); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if err := os.WriteFile(filepath.Join(dir, slug, "cv.json"),
				[]byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	write("jean", `{"meta":{},"content":{}}`)
	write("marie", `{"meta":{},"content":{}}`)
	// The cases that must not be counted, each for its own reason.
	write("no-document", "")            // a directory with nothing in it
	write("Not_A_Slug", `{}`)           // fails the slug pattern
	write("broken", `{not json at all`) // unreadable, but it IS a profile
	if err := os.WriteFile(filepath.Join(dir, ".admin-password"),
		[]byte("x"), 0o600); err != nil { // the credential, not a CV
		t.Fatal(err)
	}

	list := len(repo.List())
	count := repo.Count()
	if count != list {
		t.Fatalf("Count() says %d and List() says %d — /healthz and the "+
			"inventory page would disagree", count, list)
	}
	// Three: jean, marie, and broken — which is a profile with a document that
	// happens not to parse, and List keeps it under its slug rather than
	// dropping it.
	if count != 3 {
		t.Fatalf("expected 3 profiles, got %d", count)
	}
}
