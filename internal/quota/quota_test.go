package quota

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name string, size int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAProfileIsBoundedInBytes(t *testing.T) {
	dir := t.TempDir()
	limits := Limits{MaxProfileBytes: 1 << 20, MinFreeBytes: 0}

	write(t, dir, "cv.json", 4096)
	if err := Check(dir, 4096, limits); err != nil {
		t.Fatalf("an ordinary write was refused: %v", err)
	}

	// The thing this exists for: many files, each small.
	for i := 0; i < 300; i++ {
		write(t, dir, filepath.Base(filepath.Join("x", string(rune('a'+i%26))))+
			string(rune('a'+i/26))+".json", 4096)
	}
	if err := Check(dir, 4096, limits); err == nil {
		t.Fatal("a profile past its ceiling was allowed to grow")
	}
}

// Editing a CV that is already at its ceiling must still work: a save usually
// replaces what is there rather than adding to it.
func TestEditingAFullProfileIsStillAllowed(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "cv.json", 900<<10)
	limits := Limits{MaxProfileBytes: 1 << 20, MinFreeBytes: 0}

	if err := Check(dir, 0, limits); err != nil {
		t.Fatalf("a save that adds nothing was refused: %v", err)
	}
	if err := Check(dir, 200<<10, limits); err == nil {
		t.Fatal("a save that would pass the ceiling was allowed")
	}
}

// A refusal for want of room is its own kind, so the service can answer
// "full" rather than "your document is wrong".
func TestARefusalSaysItIsAboutSpace(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "cv.json", 2<<20)
	err := Check(dir, 1, Limits{MaxProfileBytes: 1 << 20})
	if err == nil {
		t.Fatal("no refusal")
	}
	if _, ok := err.(*Error); !ok {
		t.Fatalf("a space refusal came back as %T", err)
	}
}
