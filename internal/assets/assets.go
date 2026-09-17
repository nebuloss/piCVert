// Package assets carries the templates and the fonts inside the binary.
//
// # WHY THIS EXISTS
//
// The service claimed, in its README, its documentation, its installer and
// several comments, to be one file with everything inside it. It was not: only
// the browser interface was embedded, and the templates and fonts were read
// from a directory beside the binary.
//
// Nothing caught it because every test ran from a checkout, where that
// directory is present. Installing the published release and running it
// elsewhere produced "default template material-you not found" — a service that
// starts, serves a front page, and cannot render a single CV.
//
// # A TEMPLATE IS STILL A DIRECTORY SOMEBODY CAN ADD
//
// That was the reason for reading from disk, and it is a good one. So this does
// not replace the directory: it is the FALLBACK. A template directory beside
// the binary wins, and what is embedded answers when there is none — which is
// the case on every machine that installed from a release.
//
// The engine reads both through the same io/fs interface, so neither knows
// which it got.
package assets

import (
	"embed"
	"io/fs"
)

// templates and fonts are the ones this binary was built with.
//
// The .ttf files are what the layout engine measures with and what the PDF
// embeds; the .woff2 are what a page carries. Both are needed, and they must
// come from the same source file — measuring in one font and drawing in another
// is the fault the whole engine exists to prevent.
//
//go:embed templates
var templates embed.FS

//go:embed fonts
var fonts embed.FS

// Templates is the built-in template directory.
func Templates() fs.FS {
	sub, err := fs.Sub(templates, "templates")
	if err != nil {
		// Unreachable: go:embed fails at compile time when the directory is
		// missing, so this can only happen to a binary somebody has taken
		// apart.
		panic("assets: templates missing from the binary: " + err.Error())
	}
	return sub
}

// Fonts is the built-in font directory.
func Fonts() fs.FS {
	sub, err := fs.Sub(fonts, "fonts")
	if err != nil {
		panic("assets: fonts missing from the binary: " + err.Error())
	}
	return sub
}
