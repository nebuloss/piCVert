// Command bundle compiles the interface into what the server ships.
//
// # WHY THIS EXISTS, GIVEN THE INTERFACE IS PLAIN JAVASCRIPT
//
// Served as sources, the editor costs eleven requests before it can draw
// anything — each one a round trip, which on a phone on a train is most of a
// second apiece. Bundled, it is one. That is the visible half.
//
// The invisible half matters more: a syntax error in a module is a blank
// editor, and nothing catches it until somebody opens the page. Parsing every
// file at build time turns that into a failed build.
//
// # WHY IT IS A GO PROGRAM AND NOT A NODE ONE
//
// esbuild is written in Go and ships a Go API, and it compiles TypeScript.
// Using it through that keeps the project's one real property — clone it, `go
// build`, done — where a Node toolchain would put a runtime and a lockfile in
// the way of compiling the interface.
//
// It does NOT typecheck: esbuild strips the types and asks no questions. That
// is `tsc`, which needs Node and runs in CI and on a developer's machine, never
// on a build host. So the types are enforced without the build depending on
// them being checkable.
//
// It is a SEPARATE MODULE for the same reason. The server depends on exactly
// one external package; a bundler in its go.mod would be a dependency of every
// person who builds the server and a dependency of the server in no other
// sense.
//
// # WHY THE OUTPUT IS COMMITTED
//
// Same argument as the font subsets, and the same rule: a checkout must build
// with the Go toolchain alone. The bundle is generated, committed, and CI
// regenerates it and fails if it differs — so it cannot drift from the source
// it was built from, which is the one thing that makes a committed artefact
// dangerous.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// entry is one page's script or stylesheet, and the name the HTML asks for it
// by.
//
// Three pages, three bundles, rather than one bundle for the whole interface:
// somebody reading a CV should not download the editor, and the admin page is
// served from a different port to a different person entirely.
var entries = []string{
	"editor/app.ts",
	"editor/editor.css",
	"viewer/viewer.ts",
	"viewer/viewer.css",
	"admin/admin.ts",
	"admin/admin.css",
	"home/home.ts",
	"home/home.css",
}

// names maps an entry point to the name the templates use.
var names = map[string]string{
	"editor/app.ts":     "editor.js",
	"editor/editor.css": "editor.css",
	"viewer/viewer.ts":  "viewer.js",
	"viewer/viewer.css": "viewer.css",
	"admin/admin.ts":    "admin.js",
	"admin/admin.css":   "admin.css",
	"home/home.ts":      "home.js",
	"home/home.css":     "home.css",
}

func main() {
	root, err := findRoot()
	if err != nil {
		fail(err)
	}
	src := filepath.Join(root, "internal", "server", "ui")
	out := filepath.Join(root, "internal", "server", "web", "assets")

	// Wiped first, on purpose. esbuild never removes anything, so a bundle
	// whose source was deleted would sit in the directory for ever — embedded,
	// shipped, and referenced by nothing.
	if err := os.RemoveAll(out); err != nil {
		fail(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		fail(err)
	}

	points := make([]string, 0, len(entries))
	for _, e := range entries {
		points = append(points, filepath.Join(src, e))
	}

	result := api.Build(api.BuildOptions{
		EntryPoints: points,
		Outdir:      out,
		Bundle:      true,
		Write:       true,
		// ES modules out as well as in. The interface uses `#private` fields
		// and top-level await; down-levelling to an older target would rewrite
		// those into something larger for browsers that cannot run the service
		// anyway — it needs `dialog`, which arrived later than any of this.
		Format: api.FormatESModule,
		Target: api.ES2022,

		MinifyWhitespace:  true,
		MinifyIdentifiers: true,
		MinifySyntax:      true,

		// Sourcemaps are LINKED, not inlined: a stack trace in the console
		// should name `controls.js` and a line, and the map is only fetched
		// when somebody opens the tools. Inlined, every visitor downloads it.
		Sourcemap: api.SourceMapLinked,
		// The sources travel inside the map, so a fault can be read on a host
		// that does not carry the interface's source — which is every host,
		// since only the bundle is embedded.
		SourcesContent: api.SourcesContentInclude,

		// Content-hashed names are what make the assets cacheable for ever.
		// Without them the interface has to be served with a short cache life
		// so an upgrade is picked up, and every page load re-fetches it.
		EntryNames: "[name]-[hash]",
		AssetNames: "[name]-[hash]",

		LogLevel: api.LogLevelWarning,
	})

	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			where := ""
			if e.Location != nil {
				where = fmt.Sprintf("%s:%d:%d: ", e.Location.File, e.Location.Line, e.Location.Column)
			}
			fmt.Fprintf(os.Stderr, "%s%s\n", where, e.Text)
		}
		os.Exit(1)
	}

	manifest, err := manifestOf(result)
	if err != nil {
		fail(err)
	}
	// Written sorted and indented, because it is committed: a file that
	// reorders itself between runs is a file with a diff on every build and no
	// change in it.
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), append(raw, '\n'), 0o644); err != nil {
		fail(err)
	}

	keys := make([]string, 0, len(manifest))
	for k := range manifest {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		info, err := os.Stat(filepath.Join(out, manifest[k]))
		if err != nil {
			continue
		}
		fmt.Printf("  %-12s %-26s %6d bytes\n", k, manifest[k], info.Size())
	}
}

// manifestOf maps each asked-for name to the hashed file that answers it.
//
// Built from the output list rather than by guessing at the hash: esbuild
// decides the name, and a second implementation of its rule here would be a
// second chance to disagree with it.
func manifestOf(result api.BuildResult) (map[string]string, error) {
	manifest := map[string]string{}
	for _, file := range result.OutputFiles {
		base := filepath.Base(file.Path)
		if strings.HasSuffix(base, ".map") {
			continue
		}
		// Reverse the hashing: "app-A1B2C3D4.js" came from an entry whose stem
		// is "app" and whose output extension is ".js".
		ext := filepath.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		if i := strings.LastIndex(stem, "-"); i > 0 {
			stem = stem[:i]
		}
		for entry, name := range names {
			entryBase := filepath.Base(entry)
			entryExt := filepath.Ext(entryBase)
			if entryStem := strings.TrimSuffix(entryBase, entryExt); entryStem != stem {
				continue
			}
			// TypeScript comes out as JavaScript, so the extensions are
			// compared after that translation rather than literally. Comparing
			// them as they are was the first attempt, and it matched nothing at
			// all: every entry is a `.ts` and every output a `.js`.
			if outputExt(entryExt) != ext {
				continue
			}
			manifest[name] = base
		}
	}
	for _, name := range names {
		if manifest[name] == "" {
			return nil, fmt.Errorf("nothing was built for %q", name)
		}
	}
	return manifest, nil
}

// outputExt is what a source extension becomes once it is compiled.
func outputExt(ext string) string {
	if ext == ".ts" {
		return ".js"
	}
	return ext
}

// findRoot walks up to the directory holding go.mod for the server module.
func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for range 6 {
		if raw, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			if strings.HasPrefix(string(raw), "module picvert\n") {
				return dir, nil
			}
		}
		up := filepath.Dir(dir)
		if up == dir {
			break
		}
		dir = up
	}
	return "", fmt.Errorf("cannot find the piCVert checkout")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "bundle:", err)
	os.Exit(1)
}
