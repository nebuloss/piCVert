package main

import (
	"flag"
	"fmt"
	"os"

	"picvert/internal/server"
)

// serveCmd runs the service: every profile, the private links, the editor, and
// the admin interface on its own port.
//
// TWO PORTS, and the second one has no access control at all. Its protection is
// topological — it is not proxied outwards — which is a deployment decision
// rather than a code one. Merging the two would put an unauthenticated
// administration surface on the public internet, so the separation is here
// rather than left to whoever writes the reverse proxy configuration.
func serveCmd(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", envOr("PICVERT_ADDR", "127.0.0.1:3000"), "public address")
	adminAddr := fs.String("admin", envOr("PICVERT_ADMIN_ADDR", "127.0.0.1:3001"),
		"admin address, or empty to run without one")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := home()
	if err != nil {
		return err
	}
	s, err := server.New(root)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "  data:      %s\n", s.Profiles.DataDir())
	fmt.Fprintf(os.Stderr, "  published: %s\n", s.Access.Describe())
	return s.Serve(*addr, *adminAddr)
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
