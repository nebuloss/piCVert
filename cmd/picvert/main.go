// Command picvert turns a cv.json into a page.
//
// One layout engine, two emitters. The page you read and the PDF you send are
// drawn from the same computed frame, so they cannot come out differently —
// which is the whole reason this program exists.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"picvert/internal/engine"
	"picvert/internal/profiles"
)

// version is stamped at build time with the tag being released.
//
// "dev" everywhere else, which is the honest answer for a binary built from a
// working tree: a version number on something that was never tagged is a
// version number somebody will quote in a bug report.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "render":
		err = renderCmd(os.Args[2:])
	case "fit":
		err = fitCmd(os.Args[2:])
	case "preview":
		err = previewCmd(os.Args[2:])
	case "pdf":
		err = pdfCmd(os.Args[2:])
	case "serve":
		err = serveCmd(os.Args[2:])
	case "new":
		err = newCmd(os.Args[2:])
	case "backup":
		err = backupCmd(os.Args[2:])
	case "restore":
		err = restoreCmd(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("picvert", version)
		return
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `picvert — a CV engine that lays out once

  picvert render --profile <dir> [--lang xx] [--out page.html]
        Lays the CV out and writes the page.

  picvert fit --profile <dir> [--lang xx]
        Reports whether it holds on one page, and what is left over.

  picvert pdf --profile <dir> [--lang xx] [--out cv.pdf]
        Writes the PDF, from the same layout the page is drawn from. The
        source cv.json travels inside the file.

  picvert preview --profile <dir> [--addr host:port]
        Serves one CV, laid out again on every reload. /fit reports the fit.

  picvert serve [--addr host:port] [--admin host:port]
        Serves every profile: viewer, editor, private links, and an admin
        interface on its own port.

  picvert new --slug <name> [--name "Full Name"] [--lang xx] [--template t]
        Creates a CV and prints its two private links. Those links are the
        only way into it.

  picvert backup [--out file.tar.gz] [--keep N]
        Writes every CV to one file, and optionally removes older ones.

  picvert restore --from file.tar.gz
        Puts one back. Refuses a data directory that already holds CVs.

  picvert version

Environment:
  PICVERT_HOME     where templates/ and fonts/ live (default: alongside the binary)
  PICVERT_DATA     where profiles live             (default: <home>/data)
  PICVERT_PUBLIC   which profiles are readable without a link (default: none)
  PICVERT_ADDR     public address       (default: 127.0.0.1:3000)
  PICVERT_ADMIN_ADDR  admin address, unproxied (default: 127.0.0.1:3001)

See deploy/picvert.env.example for the rest.
`)
}

// home is where the engine's own data lives: the templates and the fonts.
//
// Found next to the binary rather than compiled in, because a template is a
// directory someone may add. The variable exists so a checkout can be run
// without installing it anywhere.
func home() (string, error) {
	if v := os.Getenv("PICVERT_HOME"); v != "" {
		return v, nil
	}
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 4; i++ {
			if _, err := os.Stat(filepath.Join(dir, "templates")); err == nil {
				return dir, nil
			}
			up := filepath.Dir(dir)
			if up == dir {
				break
			}
			dir = up
		}
	}
	return os.Getwd()
}

// prepare lays out one profile directory, which is what every command below
// starts by doing.
func prepare(profileDir, lang string) (*engine.Engine, *engine.Page, error) {
	root, err := home()
	if err != nil {
		return nil, nil, err
	}
	e := engine.New(root)
	doc, err := engine.ReadDoc(filepath.Join(profileDir, profiles.DocName(lang)))
	if err != nil {
		return nil, nil, err
	}
	page, err := e.Prepare(doc, profileDir)
	if err != nil {
		return nil, nil, err
	}
	return e, page, nil
}

func renderCmd(args []string) error {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	profileDir := fs.String("profile", "", "profile directory holding cv.json")
	lang := fs.String("lang", "", "language variant")
	out := fs.String("out", "", "file to write (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *profileDir == "" {
		return fmt.Errorf("--profile is required")
	}
	e, page, err := prepare(*profileDir, *lang)
	if err != nil {
		return err
	}
	html, err := e.HTML(page)
	if err != nil {
		return err
	}
	if *out == "" {
		fmt.Print(html)
		return nil
	}
	return os.WriteFile(*out, []byte(html), 0o644)
}

func fitCmd(args []string) error {
	fs := flag.NewFlagSet("fit", flag.ExitOnError)
	profileDir := fs.String("profile", "", "profile directory holding cv.json")
	lang := fs.String("lang", "", "language variant")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *profileDir == "" {
		return fmt.Errorf("--profile is required")
	}
	_, page, err := prepare(*profileDir, *lang)
	if err != nil {
		return err
	}
	fmt.Println(page.Summary())
	margins := page.Margins()
	for _, name := range page.Template.Columns {
		fmt.Printf("  %-6s %+8.1f px left\n", name, margins[name])
	}
	if over := page.Overflow(); len(over) > 0 {
		fmt.Printf("  shorten: %s\n", strings.Join(over, ", "))
	}
	return nil
}
