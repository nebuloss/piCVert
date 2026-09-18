package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	return passwdCmd([]string{"--stdin"})
}
