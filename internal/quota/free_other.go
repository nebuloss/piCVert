//go:build !unix

package quota

import "errors"

// freeBytes has no portable answer off Unix. The caller treats an error as
// "cannot tell", and the per-profile ceiling still applies — which is the
// bound that protects one CV from another. The disk floor is a protection
// against everything else on the machine, and a host without statfs is a host
// where that has to be somebody else's job.
func freeBytes(string) (int64, error) {
	return 0, errors.New("free space is not reportable on this platform")
}
