//go:build unix

package tokens

import (
	"os"
	"path/filepath"
	"syscall"
)

// alignOwnership puts the link file under the same owner as the data directory.
//
// The file is mode 0600. Written by root — created over SSH, say — while the
// service runs under another account, that account can no longer read it and
// EVERY link answers 403. Nothing about the failure points at file ownership,
// which is what makes it worth handling rather than documenting.
func alignOwnership(file string) {
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
