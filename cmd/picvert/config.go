package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"
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
	print_ := fs.Bool("print", false, "print the hash instead of saving it")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// BEFORE asking for anything.
	//
	// If something else is going to win, this command cannot do what it is
	// being asked to do, and the moment to say so is now — not after somebody
	// has typed a new password twice, been told it was "Saved", restarted the
	// service and found the old password still in force. That is exactly what
	// used to happen: it warned in the middle of its own success message, went
	// on to say "it takes effect on restart" which was untrue, and exited 0.
	if !*print_ {
		if err := checkNothingShadowsTheSave(); err != nil {
			return err
		}
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
		// Said before the prompt rather than after the failure. Without a
		// terminal, reading a password without echo fails deep in a system
		// call and surfaces as "inappropriate ioctl for device" — which names
		// neither the cause nor the flag that avoids it, and appears AFTER
		// the word "Password:" has already been printed, so it reads as a
		// rejected password rather than a question never asked. Over `ssh
		// host picvert passwd`, or from a script, that is the whole of the
		// output.
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("this asks for a password and there is no terminal " +
				"to ask on — pipe it instead:\n\n" +
				`  printf '%s\n' 'the password' | picvert passwd --stdin`)
		}
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

	// The floor comes from the configuration, so the rule this command enforces
	// is the one the appliance was set up with rather than one compiled in. A
	// missing or unreadable file is not fatal here: `picvert passwd` is often
	// the very first command run on a machine, before there is a file at all,
	// and refusing to hash a password because the service is not yet
	// configured would be refusing at precisely the wrong moment.
	floor := config.Defaults().Admin.MinPasswordLength
	if cfg, err := loadConfig(); err == nil {
		floor = cfg.Admin.MinPasswordLength
	}

	if floor > 0 && len(password) < floor {
		// Not a complexity policy, a floor. This guards the one surface that
		// deletes CVs and hands out every private link on the service.
		return fmt.Errorf("that is too short — %d characters at the very least.\n\n"+
			"To change or remove the floor:\n\n"+
			"admin:\n"+
			"  min-password-length: %d   # 0 turns the check off", floor, floor)
	}
	hash, err := config.Hash(password)
	if err != nil {
		return err
	}

	if floor == 0 {
		// Said every time, because a floor that is off is a thing to know
		// about a machine rather than a thing to have decided once.
		fmt.Fprintln(os.Stderr,
			"\nNote: admin.min-password-length is 0, so nothing was checked.")
	}

	if *print_ {
		// To stdout alone, so it can be piped, while the prompts went to
		// stderr. For templating the value into a configuration managed
		// somewhere else.
		fmt.Println(hash)
		return nil
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	file, err := config.WritePasswordFile(cfg.ResolvedDataDir(), hash)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Saved in %s\n", file)

	fmt.Fprintln(os.Stderr, "\nIt takes effect on restart:\n\n"+
		"  systemctl restart picvert     # or: rc-service picvert restart\n\n"+
		"Everyone signed in now is signed out by that restart.")
	return nil
}

// checkNothingShadowsTheSave refuses when saving a password cannot take effect.
//
// The order this service resolves the password in is, highest first: the
// environment, then admin.password in the configuration file, then what was
// stored by this command. That order is right — a deployment that templates
// its configuration out of Ansible or Nix must not have it silently replaced
// by a hash somebody generated on the box.
//
// But it means this command can be a no-op, and a no-op that reports success
// is worse than an error: the password is set, the service is restarted, and
// the OLD password is still the one that works, with nothing anywhere saying
// why. Somebody then concludes the new password "does not work" and has no
// thread to pull.
//
// So it is refused, before the password is even asked for, and the message
// names the file and the line rather than describing them.
func checkNothingShadowsTheSave() error {
	if env := strings.TrimSpace(os.Getenv("PICVERT_ADMIN_PASSWORD")); env != "" {
		return errors.New("PICVERT_ADMIN_PASSWORD is set in this environment, and it " +
			"wins over\nanything saved here — so saving a password now would have no " +
			"effect.\n\n" +
			"Change it where it is set, or unset it and run this again.\n\n" +
			"To generate a hash to put there:  picvert passwd --print")
	}

	path := configPath()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		// Unreadable is not the same as shadowing. `picvert passwd` is often
		// run before the service is configured at all, and refusing then would
		// be refusing at the moment the command is most needed.
		return nil
	}
	var probe struct {
		Admin struct {
			Password string `yaml:"password"`
		} `yaml:"admin"`
	}
	if yaml.Unmarshal(raw, &probe) != nil {
		return nil
	}
	if probe.Admin.Password == "" {
		return nil
	}

	line := "admin.password"
	for i, text := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(text)
		if strings.HasPrefix(trimmed, "password:") && !strings.HasPrefix(trimmed, "#") {
			line = fmt.Sprintf("line %d", i+1)
			break
		}
	}
	return fmt.Errorf("admin.password is set in %s (%s), and it wins over\n"+
		"anything saved here — so saving a password now would have no effect.\n\n"+
		"Either comment that line out and run this again, so the password lives\n"+
		"with the CVs and there is nothing to copy:\n\n"+
		"  sudo sed -i 's/^  password:/  # password:/' %s\n"+
		"  sudo picvert passwd\n\n"+
		"or keep managing it in that file, and put a fresh hash on that line:\n\n"+
		"  picvert passwd --print",
		path, line, path)
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
	// Shown always rather than only when it is off: this command exists to
	// answer "what is this machine actually configured with", and a setting
	// that only appears when it is unusual is one nobody knows to look for.
	fmt.Printf("  passwd floor  %s\n", yesNo(cfg.Admin.MinPasswordLength > 0,
		fmt.Sprintf("%d characters", cfg.Admin.MinPasswordLength),
		"off — `picvert passwd` checks nothing"))
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
