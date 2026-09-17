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
	// Empty by default, so the configuration decides. A flag that defaulted to
	// an address would silently beat the file it is meant to be an override of.
	addr := fs.String("addr", "", "public address (default: the configured one)")
	adminAddr := fs.String("admin", "", "admin address, or \"off\"")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	root, err := home()
	if err != nil {
		return err
	}
	s, err := server.New(root, cfg, version)
	if err != nil {
		return err
	}
	if *adminAddr == "off" {
		*adminAddr = ""
		s.Config.Admin.Listen = ""
	}

	fmt.Fprintf(os.Stderr, "  data:      %s\n", s.Profiles.DataDir())
	fmt.Fprintf(os.Stderr, "  published: %s\n", cfg.DescribeAccess())
	if cfg.Domain != "" {
		fmt.Fprintf(os.Stderr, "  domain:    %s\n", cfg.Domain)
	} else {
		// Said out loud, because the failure is quiet: every link handed out
		// comes back as a bare path, which is no use to whoever has to paste
		// one into a message.
		fmt.Fprintf(os.Stderr, "  domain:    NOT SET — links will be paths, not addresses\n")
	}
	if cfg.Admin.Password != "" {
		fmt.Fprintf(os.Stderr, "  admin:     password required\n")
	}
	if cfg.Turnstile.SiteKey != "" {
		fmt.Fprintf(os.Stderr, "  challenge: Turnstile\n")
	}
	return s.Serve(*addr, *adminAddr)
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
