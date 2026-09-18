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

// A password in the configuration file is REFUSED, loudly.
//
// There used to be three ways to set this, resolved in an order — the
// environment, then this file, then the stored file. That produced the worst
// bug this service has had: `picvert passwd` reported success, the operator
// restarted, and the old password still worked, because a line the installer
// had left in the configuration was quietly winning. Nothing anywhere pointed
// at it.
//
// So the key is not merely ignored now, it does not exist. The YAML decoder
// runs with KnownFields, which turns a leftover `password:` into a refusal to
// start, naming the line — rather than a setting that silently does nothing,
// which is the failure that was so expensive the first time.
func TestAPasswordInTheConfigurationFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	hash, _ := Hash("the one in the file")
	file := filepath.Join(dir, "picvert.yaml")
	body := "admin:\n  password: \"" + hash + "\"\n"
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PICVERT_DATA", dir)

	_, err := Load(file)
	if err == nil {
		t.Fatal("a password in the configuration file was accepted — it would " +
			"either win silently or do nothing silently, and both have bitten")
	}
	// The message is for somebody holding a YAML file, not a Go developer.
	// The decoder's own words are "field password not found in type
	// config.Admin", and every machine installed before this change has that
	// line — so this message IS the upgrade path.
	for _, wanted := range []string{"password:", "picvert passwd", file} {
		if !strings.Contains(err.Error(), wanted) {
			t.Fatalf("the message is missing %q: %v", wanted, err)
		}
	}
	if strings.Contains(err.Error(), "config.Admin") {
		t.Fatalf("the message names a Go type at somebody editing YAML: %v", err)
	}
}

// And the environment cannot set it either.
//
// Every other setting here is overridable by environment, because a container
// is configured that way. This one is not, and that asymmetry is the point:
// the password has one home, and a variable that overrode it would be a second
// source nobody could see from the machine.
func TestTheEnvironmentCannotSetThePassword(t *testing.T) {
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
	if cfg.Admin.Password != stored {
		t.Fatal("the environment set the password — there is supposed to be " +
			"exactly one way to do that")
	}
}

// Removing the stored file removes the password.
//
// With one source this is simply true, and it is what makes the state
// recoverable: whatever went wrong, deleting one file returns the service to
// having no administration password, which the port refuses to come up with
// unless it is bound locally. There is no second place to look.
func TestDeletingTheFileRemovesThePassword(t *testing.T) {
	dir := t.TempDir()
	stored, _ := Hash("a long enough password")
	file, err := WritePasswordFile(dir, stored)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PICVERT_DATA", dir)

	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admin.Password != "" {
		t.Fatal("the password survived the file being deleted")
	}
}

// No password file is not an error: a service with no administration password
// is still the default arrangement.
func TestNoStoredPasswordIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PICVERT_DATA", dir)
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
