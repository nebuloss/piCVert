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

	// The PDF, from the same layout the page above is drawn from — which is
	// what makes looking at both worth anything.
	mux.HandleFunc("GET /cv.pdf", func(w http.ResponseWriter, r *http.Request) {
		lang := r.URL.Query().Get("lang")
		p, err := build(*profileDir, lang)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data, err := renderPDF(p, *profileDir, lang)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		// Inline by default: this exists to be looked at, and a download is a
		// worse way to look at something than a tab is.
		disposition := "inline"
		if r.URL.Query().Get("download") == "1" {
			disposition = "attachment"
		}
		w.Header().Set("Content-Disposition", disposition+`; filename="cv.pdf"`)
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		_, _ = w.Write(data)
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

	// Which template this instance draws with, said out loud. Two previews of
	// the same CV in different templates are indistinguishable from their URLs,
	// and half an hour went into a rendering fault that was a tab pointed at
	// the wrong port.
	if p, err := build(*profileDir, ""); err == nil {
		log.Printf("piCVert preview on http://%s  —  template: %s", *addr, p.Template.Title)
	} else {
		log.Printf("piCVert preview on http://%s", *addr)
	}
	log.Printf("  /         the page          (rebuilt on every reload)")
	log.Printf("  /cv.pdf   the PDF, from the same layout")
	log.Printf("  /fit      does it hold on one page")
	return http.ListenAndServe(*addr, mux)
}
