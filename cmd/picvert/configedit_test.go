package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The real configuration file, shortened, with every trap it actually contains.
//
// Three of the four lines mentioning "password" must survive untouched: a line
// of prose, a commented-out example showing the shape of a hash, and
// `min-password-length`, which contains the word and is a different setting
// whose value is a number.
const sample = `# piCVert
listen: "0.0.0.0:3000"

admin:
  # It manages every CV, so being reachable makes the password below
  # mandatory rather than optional.
  listen: "0.0.0.0:3001"

  # A PASSWORD HASH, never a password.
  password: "old-hash"

  # password: "pbkdf2-sha256$600000$...$..."

  session: "12h"
  min-password-length: 10

turnstile:
  secret: "keep-me"
`

func TestSavingThePasswordTouchesOneLine(t *testing.T) {
	got := string(setScalar([]byte(sample), "admin", "password", "NEW"))

	if !strings.Contains(got, `  password: "NEW"`) {
		t.Fatalf("the password was not set:\n%s", got)
	}
	if strings.Contains(got, "old-hash") {
		t.Fatal("the previous password is still in the file")
	}

	// Everything else, byte for byte. This is the whole promise of editing a
	// line rather than re-encoding the document.
	for _, survivor := range []string{
		`# A PASSWORD HASH, never a password.`,
		`  # password: "pbkdf2-sha256$600000$...$..."`,
		`  min-password-length: 10`,
		`  # mandatory rather than optional.`,
		`  session: "12h"`,
		`  secret: "keep-me"`,
		`listen: "0.0.0.0:3000"`,
	} {
		if !strings.Contains(got, survivor) {
			t.Fatalf("this was lost or changed: %q\n\n%s", survivor, got)
		}
	}

	// And exactly one line changed.
	if before, after := strings.Count(sample, "\n"), strings.Count(got, "\n"); before != after {
		t.Fatalf("the file went from %d lines to %d", before, after)
	}
	changed := 0
	old := strings.Split(sample, "\n")
	for i, line := range strings.Split(got, "\n") {
		if line != old[i] {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("%d lines changed, not 1", changed)
	}
}

// `min-password-length` starts with neither "password" nor a comment, and a
// naive search for the word finds it. Setting it must not be possible to
// confuse with setting the password, in either direction.
func TestTheLengthFloorIsNotThePassword(t *testing.T) {
	got := string(setScalar([]byte(sample), "admin", "password", "NEW"))
	if !strings.Contains(got, "  min-password-length: 10") {
		t.Fatal("setting the password overwrote the length floor")
	}

	got = string(setScalar([]byte(sample), "admin", "min-password-length", "0"))
	if !strings.Contains(got, `  min-password-length: "0"`) {
		t.Fatalf("the floor was not set:\n%s", got)
	}
	if !strings.Contains(got, `  password: "old-hash"`) {
		t.Fatal("setting the floor overwrote the password")
	}
}

// The same, with the two keys the other way round.
//
// This case is the one that matters, and the first version of the test above
// did not cover it: YAML keys have no required order, and with `password`
// written first a naive "does this line contain the word" match happens to
// find the right line and the bug stays hidden. Put the floor first and the
// same code replaces `min-password-length: 10` with a password hash — a
// setting silently destroyed, and a length floor that now fails to parse as a
// number.
func TestTheLengthFloorSurvivesWhicheverOrderTheyAreIn(t *testing.T) {
	reversed := `admin:
  min-password-length: 10
  password: "old-hash"
`
	got := string(setScalar([]byte(reversed), "admin", "password", "NEW"))

	if !strings.Contains(got, "  min-password-length: 10") {
		t.Fatalf("the floor was overwritten because it was listed first:\n%s", got)
	}
	if !strings.Contains(got, `  password: "NEW"`) {
		t.Fatalf("the password was not set:\n%s", got)
	}
}

// A key in another block with the same name must not be reached.
func TestOnlyTheNamedBlockIsEdited(t *testing.T) {
	got := string(setScalar([]byte(sample), "turnstile", "secret", "NEW"))
	if !strings.Contains(got, `  secret: "NEW"`) {
		t.Fatalf("the secret was not set:\n%s", got)
	}
	if !strings.Contains(got, `  password: "old-hash"`) {
		t.Fatal("editing turnstile changed admin")
	}
}

// A setting the file does not mention yet is added to its block, not appended
// to the end of the file where it would land in whatever section came last.
func TestAnAbsentKeyIsAddedInsideItsBlock(t *testing.T) {
	source := "admin:\n  listen: \"0.0.0.0:3001\"\n\nturnstile:\n  secret: \"keep-me\"\n"
	got := string(setScalar([]byte(source), "admin", "password", "NEW"))

	admin := strings.Index(got, "admin:")
	added := strings.Index(got, `password: "NEW"`)
	turnstile := strings.Index(got, "turnstile:")
	if added < admin || added > turnstile {
		t.Fatalf("it did not land inside the admin block:\n%s", got)
	}
	if !strings.Contains(got, `  secret: "keep-me"`) {
		t.Fatalf("turnstile was disturbed:\n%s", got)
	}
}

// And a file with no such block at all gets one.
func TestAnAbsentBlockIsCreated(t *testing.T) {
	got := string(setScalar([]byte("listen: \"0.0.0.0:3000\"\n"), "admin", "password", "NEW"))
	if !strings.Contains(got, "admin:\n  password: \"NEW\"") {
		t.Fatalf("no block was created:\n%s", got)
	}
	if !strings.Contains(got, `listen: "0.0.0.0:3000"`) {
		t.Fatal("what was already there was lost")
	}
}

// A commented-out setting stays commented out.
//
// It is an EXAMPLE of the shape of a value, not a value. Uncommenting it would
// turn a piece of documentation into configuration, which for this particular
// key means turning the literal text "pbkdf2-sha256$600000$...$..." into the
// administration password.
func TestACommentedSettingIsNotRevived(t *testing.T) {
	source := "admin:\n  # password: \"do-not-use-me\"\n  session: \"12h\"\n"
	got := string(setScalar([]byte(source), "admin", "password", "NEW"))
	if !strings.Contains(got, `  # password: "do-not-use-me"`) {
		t.Fatalf("the commented example was consumed:\n%s", got)
	}
	if strings.Count(got, "password:") != 2 {
		t.Fatalf("expected the comment plus one real setting:\n%s", got)
	}
}

// The file keeps its inode, and therefore its owner, group and mode.
//
// /etc/picvert.yaml is root:picvert 0640 — owned by root so it cannot be
// edited by the service, readable by the group so the service can read it.
// Writing a new file and renaming it over the target makes it root:root, and
// the service then cannot read its own configuration and does not start.
func TestSavingKeepsTheFilesIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "picvert.yaml")
	if err := os.WriteFile(path, []byte(sample), 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := writeInPlace(path, []byte("listen: \"changed\"\n")); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode() != after.Mode() {
		t.Fatalf("the mode changed from %v to %v", before.Mode(), after.Mode())
	}
	if !os.SameFile(before, after) {
		t.Fatal("the file was replaced rather than written — the owner goes with it")
	}

	// And the previous contents are recoverable.
	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != sample {
		t.Fatal("the backup is not what was there before")
	}
}

// End to end: pipe a password in, and find it in the file afterwards.
func TestWritingAPasswordEndToEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "picvert.yaml")
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PICVERT_CONFIG", path)

	if err := hashPipedArgs(t, "a long enough password", "--stdin", "--write"); err != nil {
		t.Fatalf("writing the password failed: %v", err)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "old-hash") {
		t.Fatal("the previous password is still there")
	}
	if !strings.Contains(string(saved), "pbkdf2-sha256$") {
		t.Fatalf("no hash was written:\n%s", saved)
	}
	// It must still be a configuration file afterwards.
	if _, err := hashPipedConfig(path); err != nil {
		t.Fatalf("the file no longer loads: %v", err)
	}
}

// Writing when there is no file to write to must say so rather than create one
// somewhere arbitrary.
func TestWritingWithNoConfigurationFileSaysSo(t *testing.T) {
	t.Setenv("PICVERT_CONFIG", "")
	t.Setenv("PICVERT_HOME", t.TempDir())
	dir := t.TempDir()
	was, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(was)

	err = hashPipedArgs(t, "a long enough password", "--stdin", "--write")
	if err == nil {
		t.Skip("this machine has /etc/picvert.yaml, so there was one to write to")
	}
	if !strings.Contains(err.Error(), "PICVERT_CONFIG") {
		t.Fatalf("the message does not say how to point it at a file: %v", err)
	}
}
