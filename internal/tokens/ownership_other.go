//go:build !unix

package tokens

// alignOwnership has nothing to do where there are no POSIX owners.
func alignOwnership(string) {}
