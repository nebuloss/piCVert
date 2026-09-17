package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"picvert/internal/profiles"
)

// pdfCmd writes the PDF of a profile.
//
// From the SAME layout the page is drawn from — that is the whole point of the
// engine. The old one laid the document out twice and the two answers differed
// by about a line per block, which is how a CV that measured as fitting arrived
// with its last section cut off.
func pdfCmd(args []string) error {
	fs := flag.NewFlagSet("pdf", flag.ExitOnError)
	profileDir := fs.String("profile", "", "profile directory holding cv.json")
	lang := fs.String("lang", "", "language variant")
	out := fs.String("out", "cv.pdf", "file to write")
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
	name := profiles.DocName(*lang)
	source, _ := os.ReadFile(filepath.Join(*profileDir, name))
	data, err := e.PDF(page, name, source)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}

	fmt.Printf("%s  %d KB  —  %s", *out, len(data)/1024, page.Summary())
	if !page.Fitted.Fits {
		fmt.Printf("  (shorten %s)", strings.Join(page.Overflow(), ", "))
	}
	fmt.Println()
	return nil
}
