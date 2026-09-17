package layout

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Document wraps a laid-out page into a standalone HTML file.
//
// Self-contained in the strict sense: styles inlined, portrait as a data URI,
// FONTS EMBEDDED, no network request at all. That is what lets a CV be opened
// offline, archived as a single file, and — the part that matters — rendered
// identically on a machine that has never heard of the typeface it uses.
//
// Embedding the fonts is not decoration. The layout above was computed from
// these exact files; drawing it in whatever the reader happens to have
// installed would move every line break away from where it was measured, and a
// page that fits by less than one line would silently lose its last section.
func Document(r *Render, fonts []FontDecl, templateDir, sharedDir, title string) (string, error) {
	faces, err := embedFonts(fonts, templateDir, sharedDir)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html>\n<head>\n<meta charset=\"UTF-8\">\n")
	// The page is a document meant for print: its colours must match the PDF.
	// Automatic dark modes recompute them and break that correspondence.
	// `darkreader-lock` is that extension's own opt-out; `color-scheme`
	// disables the browsers' forced dark mode, which Chrome on Android applies
	// unprompted.
	b.WriteString("<meta name=\"darkreader-lock\">\n")
	b.WriteString("<meta name=\"color-scheme\" content=\"light\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", escapeTitle(title))
	b.WriteString("<style>\n")
	b.WriteString(faces)
	// Nothing inherited from a browser default: every position, size and colour
	// on the page was computed, and a user-agent margin would move all of them.
	b.WriteString("*{margin:0;padding:0;box-sizing:content-box}\n")
	b.WriteString("html,body{color-scheme:only light;background:#fff}\n")
	b.WriteString("</style>\n</head>\n<body>\n")
	b.WriteString(RenderHTML(r))
	b.WriteString("\n</body>\n</html>\n")

	page := b.String()
	if err := assertOffline(page); err != nil {
		return "", err
	}
	return page, nil
}

// embedFonts emits an @font-face per declared face, carrying the file itself.
//
// The woff2 next to the TTF, by convention: the TTF is what this engine
// measures with and what a PDF embeds, the woff2 is the same outlines
// compressed for the web. A template shipping no woff2 simply gets no
// @font-face, and falls back to whatever the reader has — which is the
// behaviour this exists to avoid, so the conformance kit refuses it.
func embedFonts(fonts []FontDecl, templateDir, sharedDir string) (string, error) {
	var b strings.Builder
	for _, decl := range fonts {
		for _, src := range decl.Sources {
			path := src.File
			if rel, ok := trimShared(path); ok {
				path = filepath.Join(sharedDir, rel)
			} else {
				path = filepath.Join(templateDir, path)
			}
			web := strings.TrimSuffix(path, filepath.Ext(path)) + ".woff2"
			raw, err := os.ReadFile(web)
			if err != nil {
				continue
			}
			weight := 400
			if src.Weight != 0 {
				weight = src.Weight
			}
			style := "normal"
			if src.Style == "italic" {
				style = "italic"
			}
			fmt.Fprintf(&b,
				"@font-face{font-family:'%s';font-style:%s;font-weight:%d;font-display:block;"+
					"src:url(data:font/woff2;base64,%s) format('woff2')}\n",
				decl.Family, style, weight, base64.StdEncoding.EncodeToString(raw))
		}
	}
	return b.String(), nil
}

var titleEscaper = strings.NewReplacer("<", "&lt;", ">", "&gt;", "&", "&amp;")

// escapeTitle escapes only what could close the element or start an entity. A
// document title is plain text, and the apostrophe is common in it.
func escapeTitle(s string) string { return titleEscaper.Replace(s) }

// The strict-offline guard. The emitters escape everything already; this is the
// belt over the braces, and it is what lets the page be promised to open with
// no network at all.
func assertOffline(page string) error {
	lower := strings.ToLower(page)
	for _, forbidden := range []struct{ needle, label string }{
		{"<script", "<script> tag"},
		{"<link", "<link> tag"},
		{"<iframe", "<iframe> tag"},
		{"@import", "CSS @import"},
	} {
		if strings.Contains(lower, forbidden.needle) {
			return fmt.Errorf("external asset in the generated page (%s)", forbidden.label)
		}
	}
	// A URL that is not a data: URI is a request, and a request is a promise
	// broken.
	for _, scheme := range []string{"http://", "https://", "//fonts."} {
		if strings.Contains(lower, scheme) {
			return fmt.Errorf("external asset in the generated page (%s)", scheme)
		}
	}
	return nil
}
