package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// A backup carries the CVs, not this machine's administration password.
//
// Two measured failures, both of which this prevents:
//
// The nightly backup leaves the machine — the installer says "Copy them
// somewhere that is not this machine" — so a credential riding along is a
// credential in every copy of every backup.
//
// And restoring clobbered a password nobody knew. A fresh install generates
// one and prints it ONCE, saying it is written down nowhere else; restoring
// the old machine's CVs then replaced it silently, so the password on the
// printout was refused and the one that worked belonged to a machine the
// person migrating may no longer have.
func TestABackupDoesNotCarryTheAdminPassword(t *testing.T) {
	data := t.TempDir()
	if err := os.MkdirAll(filepath.Join(data, "jean"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "jean", "cv.json"),
		[]byte(`{"meta":{},"content":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, ".admin-password"),
		[]byte("pbkdf2-sha256$600000$AAAA$BBBB"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := backupCmd([]string{"--data", data, "--out", out}); err != nil {
		t.Fatal(err)
	}

	names := namesIn(t, out)
	for _, n := range names {
		if strings.Contains(n, "admin-password") {
			t.Fatalf("the backup carries the credential: %v", names)
		}
	}
	// And it does still carry the CV, or it is not a backup.
	if !slices.Contains(names, "jean/cv.json") {
		t.Fatalf("the CV did not make it into the backup: %v", names)
	}
}

// Restoring an archive taken BEFORE this change must not overwrite the
// password of the machine it is being restored onto.
//
// Excluding it only on the way out would leave every archive already in
// existence able to do the damage.
func TestRestoringAnOldArchiveKeepsThisMachinesPassword(t *testing.T) {
	// An archive of the old shape, built by hand: credential included.
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "jean"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "jean", "cv.json"),
		[]byte(`{"meta":{},"content":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".admin-password"),
		[]byte("the-old-machines-hash"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "old.tar.gz")
	writeTarGz(t, archive, source, []string{".admin-password", "jean/cv.json"})

	// This machine, with its own password.
	data := t.TempDir()
	const mine = "the-password-this-machine-was-given"
	if err := os.WriteFile(filepath.Join(data, ".admin-password"),
		[]byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := restoreCmd([]string{"--from", archive, "--data", data}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(data, ".admin-password"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != mine {
		t.Fatal("restoring overwrote this machine's password with the archive's")
	}
	// The CV still arrived.
	if _, err := os.Stat(filepath.Join(data, "jean", "cv.json")); err != nil {
		t.Fatalf("the CV was not restored: %v", err)
	}
}

// namesIn lists what a backup contains.
func namesIn(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	r := tar.NewReader(gz)
	for {
		h, err := r.Next()
		if err != nil {
			break
		}
		out = append(out, h.Name)
	}
	return out
}

// writeTarGz builds an archive of named files, for fixtures of the old shape.
func writeTarGz(t *testing.T, path, root string, names []string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	w := tar.NewWriter(gz)
	defer w.Close()
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := w.WriteHeader(&tar.Header{
			Name: name, Mode: 0o600, Size: int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
}
