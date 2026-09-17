//go:build unix

package quota

import "syscall"

// freeBytes is how much room is left on the filesystem holding a path.
//
// Bavail rather than Bfree: the former is what an unprivileged process may
// actually use, the latter includes the reserve only root can touch. Reporting
// the reserve as available is how a service decides it has room and then fails
// to write.
func freeBytes(path string) (int64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, err
	}
	return int64(fs.Bavail) * int64(fs.Bsize), nil
}
