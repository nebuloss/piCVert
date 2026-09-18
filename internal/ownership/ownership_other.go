//go:build !unix

package ownership

// Align has nothing to do where there are no POSIX owners.
func Align(string) {}
