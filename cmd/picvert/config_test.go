package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"picvert/internal/config"
)

// `picvert passwd` with nothing to type on must say so.
//
// Reading a password without echo needs a terminal. Without one it fails inside
// a system call and the message is "inappropriate ioctl for device" — printed
// after the word "Password:", so it reads as a rejected password rather than a
// question that was never asked. Over `ssh host picvert passwd`, from a cron
// job, or from an installer, that is the entire output.
//
// The test pipes an empty file in, which is what any of those look like.
func TestAskingForAPasswordWithNoTerminalSaysSo(t *testing.T) {
	empty, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()

	was := os.Stdin
	os.Stdin = empty
	defer func() { os.Stdin = was }()

	err = passwdCmd(nil)
	if err == nil {
		t.Fatal("passwd succeeded with no terminal and no input")
	}
	if strings.Contains(err.Error(), "ioctl") {
		t.Fatalf("the message is the system call's, which names neither the "+
			"cause nor the way out: %v", err)
	}
	// It has to name the flag, because that is the thing to do next.
	if !strings.Contains(err.Error(), "--stdin") {
		t.Fatalf("the message does not say how to supply the password: %v", err)
	}
}

// And the piped form still works, since that is what the message recommends.
func TestAPipedPasswordIsHashed(t *testing.T) {
	if err := hashPiped(t, "a long enough password"); err != nil {
		t.Fatalf("a piped password was refused: %v", err)
	}
}

// A password too short to guard the one surface that deletes CVs is refused.
func TestAShortPasswordIsRefused(t *testing.T) {
	if err := hashPiped(t, "short"); err == nil {
		t.Fatal("a five-character password was accepted")
	}
}

// And the floor can be lowered, or turned off, for a deployment whose operator
// has decided the risk for themselves.
//
// The point of the setting is that it is honoured where it is written down. A
// refusal that can only be sidestepped by generating the hash some other way
// is not a control — it is an obstacle to the person who is being honest about
// what their machine is.
func TestTheFloorComesFromTheConfiguration(t *testing.T) {
	for _, c := range []struct {
		name     string
		floor    string
		password string
		accepted bool
	}{
		{"the default refuses a short one", "", "short", false},
		{"zero turns the check off", "0", "x", true},
		{"a lower floor is honoured", "4", "abcd", true},
		{"and still refuses below it", "4", "abc", false},
		{"a higher floor is honoured", "20", "only-fifteen-x", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "picvert.yaml")
			body := "admin:\n  listen: \"127.0.0.1:3001\"\n"
			if c.floor != "" {
				body += "  min-password-length: " + c.floor + "\n"
			}
			if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PICVERT_CONFIG", file)

			err := hashPiped(t, c.password)
			if c.accepted && err != nil {
				t.Fatalf("%q was refused with a floor of %q: %v", c.password, c.floor, err)
			}
			if !c.accepted && err == nil {
				t.Fatalf("%q was accepted with a floor of %q", c.password, c.floor)
			}
		})
	}
}

// The environment overrides the file, as it does for every other setting: a
// container is configured by environment, and this one has to be reachable the
// same way.
func TestTheFloorCanBeTurnedOffByEnvironment(t *testing.T) {
	t.Setenv("PICVERT_ADMIN_MIN_PASSWORD", "0")
	if err := hashPiped(t, "x"); err != nil {
		t.Fatalf("the environment did not turn the floor off: %v", err)
	}
}

// Being unable to read a configuration file must not stop a password being
// hashed. `picvert passwd` is often the FIRST command run on a machine, before
// there is a file at all — refusing then would be refusing at exactly the
// moment the command is most needed.
func TestAPasswordIsHashedWithNoConfigurationAtAll(t *testing.T) {
	t.Setenv("PICVERT_CONFIG", filepath.Join(t.TempDir(), "absent.yaml"))
	if err := hashPiped(t, "a long enough password"); err != nil {
		t.Fatalf("no configuration file stopped a password being hashed: %v", err)
	}
}

// hashPiped runs `passwd --stdin` with a password on standard input.
func hashPiped(t *testing.T, password string) error {
	t.Helper()
	// --print, because these exercise the reading and the floor rather than
	// the storing. Without it every one of them would write a password file
	// into whatever this machine's data directory turns out to be.
	return hashPipedArgs(t, password, "--stdin", "--print")
}

// hashPipedArgs is the same with the flags spelled out.
func hashPipedArgs(t *testing.T, password string, args ...string) error {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(password + "\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	was := os.Stdin
	os.Stdin = file
	defer func() { os.Stdin = was }()
	return passwdCmd(args)
}

// End to end: set a password, and find the service using it.
//
// The four-step version of this — hash, copy, paste, restart — is what the
// stored file replaced, and a test that only checks the hashing would have
// passed throughout the time the copy-paste was the thing going wrong.
func TestSettingAPasswordIsOneStep(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PICVERT_DATA", dir)
	t.Setenv("PICVERT_CONFIG", "")
	t.Setenv("PICVERT_ADMIN_PASSWORD", "")

	if err := hashPipedArgs(t, "a long enough password", "--stdin"); err != nil {
		t.Fatalf("setting the password failed: %v", err)
	}

	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Admin.Password == "" {
		t.Fatal("the password was not picked up by the service's own loader")
	}
	if !config.Verify(cfg.Admin.Password, "a long enough password") {
		t.Fatal("the stored password is not the one that was set")
	}
}

// --print stores nothing.
//
// It exists for templating the value into a configuration managed elsewhere,
// and a flag that printed AND stored would leave a credential on a machine
// that was only ever asked to compute one.
func TestPrintingDoesNotStore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PICVERT_DATA", dir)
	t.Setenv("PICVERT_CONFIG", "")

	if err := hashPipedArgs(t, "a long enough password", "--stdin", "--print"); err != nil {
		t.Fatal(err)
	}
	if got := config.ReadPasswordFile(dir); got != "" {
		t.Fatal("--print wrote a password file")
	}
}

// Saving a password that something else would override must FAIL.
//
// This is written from a real report: the password was changed, the service
// was restarted, and the login form still refused it. The command had said
// "Saved in ...", then "it takes effect on restart", and exited 0 — while
// admin.password in the configuration file went on winning. Everything
// reported success and the password did not change.
//
// A warning was not enough. It was printed between two lines that both read as
// success, and nothing that reads an exit status could see it at all.
func TestSavingAPasswordThatWouldBeIgnoredIsRefused(t *testing.T) {
	dir := t.TempDir()
	existing, err := config.Hash("the one already configured")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "picvert.yaml")
	body := "data-dir: \"" + dir + "\"\nadmin:\n  password: \"" + existing + "\"\n"
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PICVERT_CONFIG", file)
	t.Setenv("PICVERT_DATA", dir)
	t.Setenv("PICVERT_ADMIN_PASSWORD", "")

	err = hashPipedArgs(t, "a long enough password", "--stdin")
	if err == nil {
		t.Fatal("saving succeeded while the configuration file went on winning — " +
			"the password appears changed and is not")
	}
	// The message has to name the file, because the next question is always
	// "which file, and which line".
	if !strings.Contains(err.Error(), file) {
		t.Fatalf("the message does not name the file to edit: %v", err)
	}

	// And nothing was written, so the state is not half-changed.
	if got := config.ReadPasswordFile(dir); got != "" {
		t.Fatal("it refused and stored the password anyway")
	}
}

// The same for the environment, which wins over both.
func TestSavingIsRefusedWhenTheEnvironmentWins(t *testing.T) {
	dir := t.TempDir()
	injected, _ := config.Hash("the injected one")
	t.Setenv("PICVERT_CONFIG", "")
	t.Setenv("PICVERT_DATA", dir)
	t.Setenv("PICVERT_ADMIN_PASSWORD", injected)

	err := hashPipedArgs(t, "a long enough password", "--stdin")
	if err == nil {
		t.Fatal("saving succeeded while PICVERT_ADMIN_PASSWORD went on winning")
	}
	if !strings.Contains(err.Error(), "PICVERT_ADMIN_PASSWORD") {
		t.Fatalf("the message does not name what is winning: %v", err)
	}
}

// --print still works in that situation: it is how you generate the hash to
// put INTO the file that is winning, so refusing it would leave no way out.
func TestPrintingStillWorksWhenSomethingElseWins(t *testing.T) {
	dir := t.TempDir()
	injected, _ := config.Hash("the injected one")
	t.Setenv("PICVERT_DATA", dir)
	t.Setenv("PICVERT_ADMIN_PASSWORD", injected)

	if err := hashPipedArgs(t, "a long enough password", "--stdin", "--print"); err != nil {
		t.Fatalf("--print was refused, leaving no way to manage the password: %v", err)
	}
}

// And with nothing shadowing it, saving works as before.
func TestSavingWorksWhenNothingElseIsSet(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "picvert.yaml")
	if err := os.WriteFile(file, []byte("data-dir: \""+dir+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PICVERT_CONFIG", file)
	t.Setenv("PICVERT_DATA", dir)
	t.Setenv("PICVERT_ADMIN_PASSWORD", "")

	if err := hashPipedArgs(t, "a long enough password", "--stdin"); err != nil {
		t.Fatalf("saving was refused with nothing in the way: %v", err)
	}
	if !config.Verify(config.ReadPasswordFile(dir), "a long enough password") {
		t.Fatal("the password was not stored")
	}
}
