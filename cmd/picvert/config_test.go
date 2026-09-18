package main

import (
	"os"
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
	file, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("a long enough password\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	was := os.Stdin
	os.Stdin = file
	defer func() { os.Stdin = was }()

	if err := passwdCmd([]string{"--stdin"}); err != nil {
		t.Fatalf("a piped password was refused: %v", err)
	}
}

// A password too short to guard the one surface that deletes CVs is refused.
func TestAShortPasswordIsRefused(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("short\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	was := os.Stdin
	os.Stdin = file
	defer func() { os.Stdin = was }()

	if err := passwdCmd([]string{"--stdin"}); err == nil {
		t.Fatal("a five-character password was accepted")
	}
}
