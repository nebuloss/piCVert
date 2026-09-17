package config

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// The admin password, as a hash.
//
// # WHY NOT JUST COMPARE THE PASSWORD
//
// A configuration file is read by whoever can read the disk, ends up in a
// backup, and gets pasted into a support message. A password in it is a
// password that has escaped before anybody has logged in. A hash is useless to
// whoever finds it.
//
// # WHY PBKDF2 AND NOT SOMETHING NEWER
//
// It is in the standard library as of Go 1.24, which is the whole argument: the
// alternative is a dependency for one function on a service that has one
// dependency. Argon2 is better against an attacker with a GPU farm, and that
// attacker is not interested in a single admin password on a CV service — they
// would have to get the file first, and if they have the file they have the
// CVs.
//
// What is NOT negotiable is the shape: a per-password salt, a deliberately slow
// derivation, and a constant-time comparison. Those are what make a stolen hash
// expensive, and all three are here.

const (
	// hashPrefix names the scheme, so a future change can be told apart from
	// this one rather than silently misread as it.
	hashPrefix = "pbkdf2-sha256"
	// iterations is the cost. Six hundred thousand is what OWASP asks for with
	// SHA-256, and it is about a tenth of a second — which nobody notices once
	// per login and an attacker pays for every guess.
	iterations = 600_000
	saltBytes  = 16
	keyBytes   = 32
)

// Hash turns a password into something safe to write down.
func Hash(password string) (string, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, keyBytes)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		hashPrefix,
		strconv.Itoa(iterations),
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	}, "$"), nil
}

// IsHash reports whether a string is one of ours.
//
// Used to refuse a configuration with a plaintext password in it. Somebody who
// pastes their password where a hash belongs has made a mistake that looks
// exactly like success — the service starts, the login works, and the password
// is in the file.
func IsHash(s string) bool {
	parts := strings.Split(s, "$")
	return len(parts) == 4 && parts[0] == hashPrefix
}

// Verify checks a password against a hash, in constant time.
func Verify(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != hashPrefix {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	// The iteration count comes FROM the hash, so raising it later leaves
	// every existing password working — they simply keep their old cost until
	// they are set again.
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	// Constant time: a comparison that stops at the first wrong byte tells an
	// attacker how much of their guess was right.
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NewSecret makes a random value to sign sessions with.
//
// Kept in memory and never written down, which means every restart invalidates
// every login. For an administration interface that is the right trade: a
// forgotten session on a machine somebody no longer has does not outlive the
// next deployment, and logging in again costs one password.
func NewSecret() []byte {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic("config: no randomness available: " + err.Error())
	}
	return secret
}

var _ = fmt.Sprintf
