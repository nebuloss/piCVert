//go:build unix

package config

import (
	"os"
	"syscall"
	"testing"
)

// The stored password must end up readable by the account the service runs as.
//
// This is the failure that is worth a test more than any other here, because
// it is the one that has actually happened: `sudo picvert passwd` writes a
// 0600 file as root, the service runs as its own account, and the new password
// simply does not work — with nothing in any log mentioning a file, let alone
// its owner.
//
// It needs root to reproduce, since giving a file away is a privileged
// operation. Skipped rather than faked: a version of this that stubbed out the
// system call would assert that the code calls a function, which is not the
// thing that was wrong.
func TestAStoredPasswordEndsUpOwnedByTheService(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root: only root can give a file to another account")
	}
	dir := t.TempDir()
	// The data directory as the installer leaves it: owned by the service.
	const serviceUID, serviceGID = 65534, 65534 // nobody
	if err := os.Chown(dir, serviceUID, serviceGID); err != nil {
		t.Fatal(err)
	}

	hash, _ := Hash("a long enough password")
	file, err := WritePasswordFile(dir, hash)
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no POSIX ownership here")
	}
	if stat.Uid != serviceUID || stat.Gid != serviceGID {
		t.Fatalf("the password file is owned by %d:%d, but the data directory "+
			"is %d:%d — the service cannot read its own password",
			stat.Uid, stat.Gid, serviceUID, serviceGID)
	}
}
