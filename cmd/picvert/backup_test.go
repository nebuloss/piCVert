package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Rotation must not depend on what the backup was called.
//
// `--out t.tar.gz --keep 14` panicked: the prefix was taken by appending a dash
// and searching for it, which finds the appended one when the name has none of
// its own and then slices one past the end. It was only ever run with the
// default name, which always has a dash in its date.
func TestRotatingHandlesAnyName(t *testing.T) {
	for _, name := range []string{
		"picvert-2026-01-01-1200.tar.gz", // the default shape
		"t.tar.gz",                       // no dash at all — the panic
		"backup.tar.gz",
		"-leading.tar.gz",
		".tar.gz",
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		// Must not panic, whatever it decides to keep.
		if err := rotate(path, 3); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: rotation removed the backup it had just taken", name)
		}
	}
}

// And it still keeps the newest few when there are more than that.
func TestRotatingKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	var newest string
	for _, day := range []string{"01", "02", "03", "04", "05"} {
		path := filepath.Join(dir, "picvert-2026-01-"+day+".tar.gz")
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		newest = path
	}
	if err := rotate(newest, 3); err != nil {
		t.Fatal(err)
	}
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 3 {
		t.Fatalf("kept %d backups, expected 3", len(left))
	}
	// The newest, and never the one just written.
	if _, err := os.Stat(newest); err != nil {
		t.Error("rotation removed the backup it had just taken")
	}
}
