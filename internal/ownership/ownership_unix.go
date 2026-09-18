//go:build unix

package ownership

import (
	"os"
	"path/filepath"
	"syscall"
)

// Align puts a file under the same owner as the directory holding it.
//
// Both files this is used for are mode 0600 and are written by whoever runs
// the command — root, over SSH — while the service runs under its own account.
// Without this the service cannot read what was just written for it, and the
// failure never mentions ownership: the private links all answer 403, or the
// administration password is silently the old one.
//
// The directory is the reference because it is the thing the installer created
// and chowned to the service account. Nothing here invents an owner.
//
// A no-op unless running as root: a non-root process cannot give a file away,
// and attempting it would only produce an error to ignore.
func Align(file string) {
	if os.Geteuid() != 0 {
		return
	}
	ref, err := os.Stat(filepath.Dir(file))
	if err != nil {
		return
	}
	cur, err := os.Stat(file)
	if err != nil {
		return
	}
	refStat, ok := ref.Sys().(*syscall.Stat_t)
	curStat, ok2 := cur.Sys().(*syscall.Stat_t)
	if !ok || !ok2 {
		return
	}
	if curStat.Uid != refStat.Uid || curStat.Gid != refStat.Gid {
		_ = os.Chown(file, int(refStat.Uid), int(refStat.Gid))
	}
}
