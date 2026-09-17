package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
	"picvert/internal/config"
)

// configPath is where the settings are looked for.
//
// Three places, in the order somebody would expect: what was asked for, then
// the system one, then a file beside the checkout. A missing file is not an
// error — the defaults are a working service, and requiring one would make the
// simplest deployment the one that needs the most explaining.
func configPath() string {
	if v := strings.TrimSpace(os.Getenv("PICVERT_CONFIG")); v != "" {
		return v
	}
	for _, candidate := range []string{"/etc/picvert.yaml", "picvert.yaml"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if root, err := home(); err == nil {
		beside := filepath.Join(root, "picvert.yaml")
		if _, err := os.Stat(beside); err == nil {
			return beside
		}
	}
	return ""
}

func loadConfig() (config.Config, error) {
	return config.Load(configPath())
}

// passwdCmd turns a password into the hash that goes in the file.
//
// Read from the terminal WITHOUT echoing, and never taken as an argument: an
// argument is in the shell history, in the process list, and in the logs of
// anything watching either.
func passwdCmd(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
	stdin := fs.Bool("stdin", false, "read the password from standard input")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var password string
	if *stdin {
		// For a script setting this up. Still not an argument.
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return err
		}
		password = strings.TrimRight(line, "\r\n")
	} else {
		fmt.Fprint(os.Stderr, "Password: ")
		typed, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		fmt.Fprint(os.Stderr, "Again: ")
		again, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		if string(typed) != string(again) {
			return fmt.Errorf("those did not match")
		}
		password = string(typed)
	}

	if len(password) < 10 {
		// Not a policy, a floor. This guards the one surface that deletes CVs
		// and hands out every private link on the service.
		return fmt.Errorf("that is too short — ten characters at the very least")
	}
	hash, err := config.Hash(password)
	if err != nil {
		return err
	}
	// To stdout alone, so it can be piped, while the prompts went to stderr.
	fmt.Println(hash)
	fmt.Fprintln(os.Stderr, "\nPut that in your configuration:\n\nadmin:\n  password: \""+hash+"\"")
	return nil
}

// configCmd prints what the service would be told, and where from.
//
// The question "what is this thing actually configured with" has no answer
// otherwise: a file layered with an environment, some of it defaulted, is three
// places to look and no way to be sure.
func configCmd(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	path := configPath()
	if path == "" {
		fmt.Println("no configuration file — these are the defaults")
	} else {
		fmt.Printf("from %s, with the environment layered over it\n", path)
	}
	fmt.Println()
	fmt.Printf("  domain        %s\n", orNone(cfg.Domain,
		"NOT SET — links come out as paths"))
	fmt.Printf("  listen        %s\n", cfg.Listen)
	fmt.Printf("  admin         %s\n", orNone(cfg.Admin.Listen, "off"))
	fmt.Printf("  admin login   %s\n", yesNo(cfg.Admin.Password != "",
		"password required", "none — protected only by not being reachable"))
	fmt.Printf("  challenge     %s\n", yesNo(cfg.Turnstile.SiteKey != "",
		"Turnstile", "off"))
	fmt.Printf("  published     %s\n", cfg.DescribeAccess())
	fmt.Printf("  data          %s\n", orNone(cfg.DataDir, "<home>/data"))
	fmt.Printf("  per CV        %d MB, %d MB must stay free\n",
		cfg.Limits.MaxProfileMB, cfg.Limits.MinFreeMB)
	fmt.Printf("  lease         %s, idle after %s\n",
		cfg.Editing.LeaseTTL, cfg.Editing.Inactivity)
	return nil
}

func orNone(value, instead string) string {
	if value == "" {
		return instead
	}
	return value
}

func yesNo(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}
