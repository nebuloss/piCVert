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
	// Only serve opens a port, so only serve answers for what opening one
	// means. The other commands do not, and refusing to run `picvert new` over
	// the administration port's password is refusing for a reason that has
	// nothing to do with it.
	// Both flags fold into the configuration, not just the admin one.
	//
	// `--addr` used to be passed straight to Serve while cfg.Listen kept
	// whatever the file said, so anything asking the configuration where CVs
	// are served got an answer that was quietly untrue. The administration
	// page asks exactly that, to build links to the other port, and it
	// therefore pointed at the port from the file rather than the one being
	// listened on.
	if *addr != "" {
		cfg.Listen = *addr
	}
	if *adminAddr != "" && *adminAddr != "off" {
		cfg.Admin.Listen = *adminAddr
	}
	if *adminAddr == "off" {
		cfg.Admin.Listen = ""
	}
	if err := cfg.CheckServing(); err != nil {
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
