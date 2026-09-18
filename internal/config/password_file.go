package config

// The administration password, kept beside the CVs rather than in the
// configuration file.
//
// # WHY NOT IN picvert.yaml
//
// Because that file is documentation as much as it is settings. It is mostly
// comments explaining the reasoning behind each choice, it is meant to be read
// and edited by a person, and this program never writes it — so your comments
// survive, your ordering survives, and nothing you wrote there is ever
// reformatted by a machine.
//
// Which is a good rule, and it made changing the password a four-step chore:
// run a command, copy a ninety-character hash out of the terminal, open the
// file, paste it in. A hash mistyped by one character is refused in a way that
// looks exactly like the wrong password, so the most error-prone step in
// setting this service up was a copy-paste that a program could do perfectly.
//
// The way out is not to start writing the configuration file. It is to notice
// that a generated credential is not configuration at all: nobody hand-writes
// a PBKDF2 hash, nobody reviews one in a diff, and nobody wants it in the file
// they paste into a bug report. It belongs with the other generated secret
// this service already keeps — .share-tokens.json, in the data directory —
// and for the same reasons.
//
// So picvert.yaml stays read-only and fully commented, `picvert passwd` writes
// a file it owns completely, and neither one has to compromise for the other.
//
// admin.password in the configuration still works, and still wins if set: some
// deployments template that file out of Ansible or a Nix module, and taking
// that away would break them for no gain.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"picvert/internal/ownership"
)

// PasswordFile is where `picvert passwd` writes the hash.
//
// A dotfile, because the data directory is enumerated to list the CVs and
// anything that is not a directory with a slug-shaped name is skipped — the
// same reason .share-tokens.json sits there without becoming a profile called
// "share-tokens".
func PasswordFile(dataDir string) string {
	return filepath.Join(dataDir, ".admin-password")
}

// ReadPasswordFile returns the stored hash, or "" if there is none.
//
// A missing file is not an error: it is the normal state of a service whose
// administration port has no password, which is still the default.
func ReadPasswordFile(dataDir string) string {
	raw, err := os.ReadFile(PasswordFile(dataDir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// WritePasswordFile stores the hash, readable only by the service.
func WritePasswordFile(dataDir, hash string) (string, error) {
	if !IsHash(hash) {
		// A plaintext password written here would be a plaintext password
		// accepted at the login form, silently, because nothing downstream
		// re-checks the shape of what it was given.
		return "", fmt.Errorf("refusing to store something that is not a hash")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", err
	}
	file := PasswordFile(dataDir)

	// Written to a temporary name and renamed, so a half-written file can
	// never be the thing the service reads. Unlike the configuration file,
	// this one is ours: replacing the inode costs nothing, because the owner
	// it should have is realigned immediately below rather than inherited.
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(hash+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, file); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	_ = os.Chmod(file, 0o600)

	// THE STEP THAT MAKES THIS WORK AT ALL. `picvert passwd` is run with sudo;
	// the service runs as its own account. A 0600 file left owned by root is a
	// password the service cannot read, and the symptom is that the new
	// password simply does not work — with nothing anywhere mentioning a file.
	ownership.Align(file)
	return file, nil
}
