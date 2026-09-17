package server

import (
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"picvert/internal/profiles"
	"picvert/internal/security"
)

// web holds every asset the interface is made of.
//
// EMBEDDED, so the service is one file. A CV engine that needs a directory of
// scripts next to the binary is a CV engine that will one day be deployed
// without it and serve blank pages, and the failure looks like a bug in the
// editor rather than a missing folder.
//
//go:embed web
var web embed.FS

var pages = template.Must(template.ParseFS(web,
	"web/viewer.html", "web/home.html", "web/editor.html", "web/admin.html"))

// assetHandler serves the interface's own scripts and styles.
func assetHandler(guard *security.Guard) http.Handler {
	sub, err := fs.Sub(web, "web")
	if err != nil {
		panic("server: assets missing from the binary: " + err.Error())
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guard.CSP(w, security.Admin)
		// Long-lived, because these change only when the binary does — and the
		// pages that load them are never cached, so a new build is picked up on
		// the next reload of the page rather than on the next expiry.
		w.Header().Set("Cache-Control", "public, max-age=300")
		files.ServeHTTP(w, r)
	})
}

// viewerData is what the frame around a CV needs to know.
type viewerData struct {
	// Base is the prefix every link on the page is built from: “/p/<slug>” for
	// a published CV, “/e/<token>” for a private one. Stated once so the page
	// itself has no idea which of the two it is being served as.
	Base    string
	Profile *profiles.Profile
	Lang    string
	CanEdit bool
}

func (s *Server) sendViewer(w http.ResponseWriter, r *http.Request, data viewerData) {
	languages, _ := json.Marshal(data.Profile.Languages())
	body, err := render("viewer.html", map[string]any{
		"Base":      data.Base,
		"Name":      nameOf(data.Profile),
		"Lang":      data.Lang,
		"CanEdit":   data.CanEdit,
		"Languages": template.JS(languages),
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	s.sendHTML(w, r, security.Viewer, body)
}

func homePage(public []*profiles.Profile) string {
	body, err := render("home.html", map[string]any{"Profiles": public})
	if err != nil {
		return "<!doctype html><title>piCVert</title><p>" + template.HTMLEscapeString(err.Error())
	}
	return body
}

func editorPage(base, slug, token string) string {
	body, err := render("editor.html", map[string]any{
		"Base": base, "Slug": slug, "Token": token,
	})
	if err != nil {
		return "<!doctype html><title>piCVert</title><p>" + template.HTMLEscapeString(err.Error())
	}
	return body
}

func render(name string, data any) (string, error) {
	var b strings.Builder
	if err := pages.ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return b.String(), nil
}
