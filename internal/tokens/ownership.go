package tokens

import "picvert/internal/ownership"

// alignOwnership is stated once, in internal/ownership, because the
// administration password file needs exactly the same treatment for exactly
// the same reason.
func alignOwnership(file string) { ownership.Align(file) }
