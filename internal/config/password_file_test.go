package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The password `picvert passwd` stored is the password the service uses.
//
// This is the whole point of keeping it in the data directory: setting it is
// one command, with nothing to copy and nowhere to paste it wrong.
func TestAStoredPasswordIsTheOneTheServiceUses(t *testing.T) {
	dir := t.TempDir()
	hash, err := Hash("a long enough password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WritePasswordFile(dir, hash); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PICVERT_DATA", dir)
	t.Setenv("PICVERT_ADMIN_PASSWORD", "")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admin.Password != hash {
		t.Fatalf("the stored password was not picked up: %q", cfg.Admin.Password)
	}
	if !Verify(cfg.Admin.Password, "a long enough password") {
		t.Fatal("the stored hash does not verify the password it was made from")
	}
}

// It is readable only by the account the service runs as.
//
// The file is a credential sitting in a directory that is otherwise CVs. 0600
// is what stops it being world-readable on a machine where the data directory
// is not.
func TestAStoredPasswordIsNotReadableByEveryone(t *testing.T) {
	dir := t.TempDir()
	hash, _ := Hash("a long enough password")
	file, err := WritePasswordFile(dir, hash)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("the password file is mode %04o, not 0600", mode)
	}
}

// It must not become a CV.
//
// The data directory is enumerated to list the profiles. A credential file
// appearing there as a profile called "admin-password" would be listed on the
// administration page, and published if the policy said "*".
func TestTheStoredPasswordIsNotAProfile(t *testing.T) {
	if base := filepath.Base(PasswordFile("/data")); !strings.HasPrefix(base, ".") {
		t.Fatalf("%q would be enumerated as a CV", base)
	}
}

// Anything that is not a hash is refused.
//
// Nothing downstream re-checks the shape of what it is given, so a plaintext
// password written here would be a plaintext password accepted at the login
// form, silently.
func TestOnlyAHashIsStored(t *testing.T) {
	if _, err := WritePasswordFile(t.TempDir(), "hunter2"); err == nil {
		t.Fatal("a plaintext password was stored as though it were a hash")
	}
}

// What is written down by hand beats what was generated.
//
// Some deployments template picvert.yaml out of Ansible or a Nix module. A
// hash generated on the box months ago must not quietly win over the one that
// is under version control.
func TestTheConfiguredPasswordWinsOverTheStoredOne(t *testing.T) {
	dir := t.TempDir()
	stored, _ := Hash("the stored one")
	if _, err := WritePasswordFile(dir, stored); err != nil {
		t.Fatal(err)
	}
	written, _ := Hash("the written one")

	file := filepath.Join(dir, "picvert.yaml")
	body := "admin:\n  password: \"" + written + "\"\n"
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PICVERT_DATA", dir)
	t.Setenv("PICVERT_ADMIN_PASSWORD", "")
	cfg, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admin.Password != written {
		t.Fatal("the stored password overrode the one in the configuration file")
	}
}

// And the environment beats both, as it does for every other setting.
func TestTheEnvironmentWinsOverTheStoredPassword(t *testing.T) {
	dir := t.TempDir()
	stored, _ := Hash("the stored one")
	if _, err := WritePasswordFile(dir, stored); err != nil {
		t.Fatal(err)
	}
	injected, _ := Hash("the injected one")

	t.Setenv("PICVERT_DATA", dir)
	t.Setenv("PICVERT_ADMIN_PASSWORD", injected)
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admin.Password != injected {
		t.Fatal("the stored password overrode the environment")
	}
}

// No password file is not an error: a service with no administration password
// is still the default arrangement.
func TestNoStoredPasswordIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PICVERT_DATA", dir)
	t.Setenv("PICVERT_ADMIN_PASSWORD", "")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("a missing password file was treated as a failure: %v", err)
	}
	if cfg.Admin.Password != "" {
		t.Fatal("a password appeared from nowhere")
	}
}

// Replacing the password twice must leave one file and no leftovers.
func TestStoringTwiceLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	first, _ := Hash("the first password")
	second, _ := Hash("the second password")
	if _, err := WritePasswordFile(dir, first); err != nil {
		t.Fatal(err)
	}
	if _, err := WritePasswordFile(dir, second); err != nil {
		t.Fatal(err)
	}
	if got := ReadPasswordFile(dir); got != second {
		t.Fatal("the second password did not replace the first")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("%s was left behind", e.Name())
		}
	}
}
