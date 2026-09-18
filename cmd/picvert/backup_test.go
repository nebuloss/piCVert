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

// Backup finds the data directory the way the service does.
//
// It used to work it out on its own, from the environment and a path beside
// the binary, which meant that on a machine configured by /etc/picvert.yaml —
// every installed one — it looked in the wrong place and said "no data
// directory". Run as root on the appliance it reported /root/data while
// `picvert config` on the same binary reported /var/lib/picvert/data.
//
// That is the command you reach for when moving a machine, so it failing is
// the difference between a migration and a support ticket.
func TestBackupFindsTheConfiguredDataDirectory(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "elsewhere")
	if err := os.MkdirAll(filepath.Join(data, "jean"), 0o700); err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(dir, "picvert.yaml")
	if err := os.WriteFile(file, []byte("data-dir: \""+data+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PICVERT_CONFIG", file)
	t.Setenv("PICVERT_DATA", "")

	if got := configuredDataDir(); got != data {
		t.Fatalf("configuredDataDir() = %q, want the configured %q", got, data)
	}
}

// The environment still wins, as it does for every other setting.
func TestTheEnvironmentStillChoosesTheDataDirectory(t *testing.T) {
	dir := t.TempDir()
	fromEnv := filepath.Join(dir, "from-env")
	if err := os.MkdirAll(fromEnv, 0o700); err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(dir, "picvert.yaml")
	if err := os.WriteFile(file,
		[]byte("data-dir: \""+filepath.Join(dir, "from-file")+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PICVERT_CONFIG", file)
	t.Setenv("PICVERT_DATA", fromEnv)

	if got := configuredDataDir(); got != fromEnv {
		t.Fatalf("configuredDataDir() = %q, want %q from the environment", got, fromEnv)
	}
}

// A configuration that will not parse must not stop a backup.
//
// A backup is the thing you want MOST when something is wrong with the
// configuration, and refusing to take one then would be refusing at the worst
// possible moment.
func TestBackupStillWorksWithABrokenConfiguration(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "picvert.yaml")
	if err := os.WriteFile(file, []byte("this: [is not: valid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PICVERT_CONFIG", file)
	t.Setenv("PICVERT_DATA", dir)

	if got := configuredDataDir(); got != dir {
		t.Fatalf("a broken configuration changed the answer: %q", got)
	}
}
