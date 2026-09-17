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

//go:generate go -C ../../tools/bundle run .

// web holds the templates and the compiled interface.
//
// EMBEDDED, so the service is one file. A CV engine that needs a directory of
// scripts next to the binary is a CV engine that will one day be deployed
// without it and serve blank pages — and the failure looks like a bug in the
// editor rather than a missing folder.
//
// `web/assets` is GENERATED from `internal/server/ui` by `tools/bundle`, and
// committed. Same rule as the font subsets: a checkout must build with the Go
// toolchain alone. What keeps a committed artefact from drifting out of step
// with its source is that CI rebuilds it and fails if the result differs.
//
//go:embed web
var web embed.FS

// assets maps the name a template asks for to the content-hashed file that
// answers it.
//
// The indirection is what buys the caching. A page asks for "editor.js"; what
// it gets is "app-7F3A21C8.js", whose name changes whenever its contents do —
// so the file can be cached for a year and an upgrade is still picked up
// immediately, because the page naming it is never cached at all.
var assets = mustManifest()

func mustManifest() map[string]string {
	raw, err := web.ReadFile("web/assets/manifest.json")
	if err != nil {
		// Unreachable in a built binary: go:embed fails at compile time when
		// the directory is missing. Reached only by somebody who deleted the
		// bundle by hand, and saying so beats serving pages whose every script
		// 404s.
		panic("server: the interface has not been bundled — run `go generate ./...`: " + err.Error())
	}
	var manifest map[string]string
	if err := json.Unmarshal(raw, &manifest); err != nil {
		panic("server: the asset manifest is unreadable: " + err.Error())
	}
	return manifest
}

var pages = template.Must(template.New("pages").
	Funcs(template.FuncMap{
		// asset resolves a name to the hashed file serving it, so no template
		// ever spells a filename that changes on every build.
		"asset": func(name string) string {
			hashed, ok := assets[name]
			if !ok {
				// Loudly, in the page. A missing asset is a blank editor, and a
				// 404 in the console is easier to act on than silence.
				return "/assets/MISSING-" + name
			}
			return "/assets/" + hashed
		},
	}).
	ParseFS(web, "web/viewer.html", "web/home.html", "web/editor.html", "web/admin.html"))

// assetHandler serves the compiled interface.
func assetHandler(guard *security.Guard) http.Handler {
	sub, err := fs.Sub(web, "web/assets")
	if err != nil {
		panic("server: assets missing from the binary: " + err.Error())
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guard.CSP(w, security.Admin)
		// The manifest is read at startup, never over HTTP. Serving it would
		// publish the mapping for no purpose.
		if strings.HasSuffix(r.URL.Path, "manifest.json") {
			http.NotFound(w, r)
			return
		}
		// A year, and immutable. Safe only because the name carries a hash of
		// the contents: a new build is a new name, so nothing has to expire for
		// an upgrade to be seen. Without the hash this had to be five minutes,
		// and every page load re-fetched the whole interface.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
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
