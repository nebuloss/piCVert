package main

import (
	"flag"
	"fmt"

	"picvert/internal/server"
)

// newCmd creates a CV from the command line.
//
// The admin port can do this too, and usually should. This exists because that
// port has no access control and therefore must not be reachable from
// anywhere; on a host where nobody wants to forward it over SSH, a shell is the
// remaining way in — and a service whose only creation path is an interface you
// are told not to expose is a service nobody can start using.
func newCmd(args []string) error {
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	slug := fs.String("slug", "", "identifier, as it appears in addresses")
	name := fs.String("name", "", "the name on the CV")
	lang := fs.String("lang", "en", "two-letter language code")
	tpl := fs.String("template", "", "template name or UUID (default: the configured one)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *slug == "" {
		return fmt.Errorf("--slug is required")
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
	if _, err := s.Store.Create(*slug, *name, *tpl, *lang); err != nil {
		return err
	}
	p, err := s.Profiles.Get(*slug)
	if err != nil {
		return err
	}
	links, err := s.Tokens.ForProfile(p.Slug)
	if err != nil {
		return err
	}

	fmt.Printf("created %s in %s\n\n", p.Slug, p.Dir)
	// The edit link is the ONLY way into the CV just made. Printed first and
	// printed whole: somebody who loses this line has made something nobody can
	// open, and there is no account to recover it through.
	fmt.Printf("  edit  %s\n", cfg.LinkTo("/e/"+links.Edit+"/edit/"))
	fmt.Printf("  read  %s\n", cfg.LinkTo("/e/"+links.Read+"/"))
	if cfg.Domain == "" {
		fmt.Printf("\nThose are paths, not addresses: set `domain` in the " +
			"configuration\nand they come out as links somebody can open.\n")
	}
	return nil
}
