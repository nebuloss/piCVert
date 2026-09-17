package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"picvert/internal/favicon"
)

// previewCmd serves a profile, rebuilt on every request.
//
// Rebuilt rather than cached, deliberately: this exists to look at a CV while
// changing the theme it is drawn with, and a preview that has to be restarted
// to show an edit is a preview nobody trusts. A full layout takes a few
// milliseconds — far less than the reload it answers.
func previewCmd(args []string) error {
	fs := flag.NewFlagSet("preview", flag.ExitOnError)
	profileDir := fs.String("profile", "", "profile directory holding cv.json")
	addr := fs.String("addr", "127.0.0.1:8080", "address to listen on")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *profileDir == "" {
		return fmt.Errorf("--profile is required")
	}
	if _, err := os.Stat(filepath.Join(*profileDir, "cv.json")); err != nil {
		return fmt.Errorf("no cv.json in %s", *profileDir)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		lang := r.URL.Query().Get("lang")
		p, err := build(*profileDir, lang)
		if err != nil {
			// Shown rather than logged: whoever is looking at this page is the
			// person who can fix the theme that broke it.
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		root, _ := home()
		page, err := Document(p, root, documentTitle(*profileDir, lang))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// A preview that answers from cache is a preview showing the last
		// theme, not this one.
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		_, _ = w.Write([]byte(page))
	})

	mux.HandleFunc("GET /fit", func(w http.ResponseWriter, r *http.Request) {
		p, err := build(*profileDir, r.URL.Query().Get("lang"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if p.Render.Fits() {
			fmt.Fprintln(w, "fits on one page")
		} else {
			fmt.Fprintln(w, "DOES NOT FIT")
		}
		margins := p.Render.Margins(p.Template.Columns)
		for _, name := range p.Template.Columns {
			fmt.Fprintf(w, "  %-6s %+8.1f px left\n", name, margins[name])
		}
		if over := p.Render.Overflow(); len(over) > 0 {
			fmt.Fprintf(w, "  shorten: %s\n", strings.Join(over, ", "))
		}
	})

	icon := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(favicon.SVG(favicon.Public))
	}
	mux.HandleFunc("GET /favicon.ico", icon)
	mux.HandleFunc("GET /favicon.svg", icon)

	log.Printf("piCVert preview on http://%s  (rebuilt on every reload)", *addr)
	return http.ListenAndServe(*addr, mux)
}
