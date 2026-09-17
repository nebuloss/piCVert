package layout

import (
	"encoding/base64"
	"fmt"
	"math"
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
	// Without this a phone assumes a desktop-width page, renders the CV at
	// about a third of its size and lets nobody zoom back in usefully. The CV
	// is the one document people are most likely to open on a phone, from a
	// link someone sent them.
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">\n")
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
	b.WriteString(responsive(r.Width, r.Height))
	b.WriteString("</style>\n</head>\n<body>\n")
	// The page is wrapped rather than altered: what is inside keeps the exact
	// dimensions everything was measured at, and only the wrapper scales.
	b.WriteString(`<div class="sheet">`)
	b.WriteString(RenderHTML(r))
	b.WriteString(`</div>`)
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

// responsive makes a fixed page fit a screen narrower than it is.
//
// # WHY A TRANSFORM AND NOT A RESPONSIVE LAYOUT
//
// The page is 794x1123 and cannot become anything else: every position on it
// was computed for that size, and the whole promise of this engine is that the
// arrangement is settled once. Reflowing it for a phone would be a second
// layout, decided by the browser — which is the thing this exists to remove.
//
// So the page is not changed, it is scaled whole, like a sheet of paper held
// further away. The proportions stay those of the PDF, which is what anyone
// comparing the two would expect.
//
// `transform` rather than `zoom`: zoom re-runs layout at the new size, letting
// the browser re-break text and putting the divergence straight back. A
// transform moves pixels and decides nothing.
//
// # WHY THE RANGES DO NOT OVERLAP
//
// Written as a series of `max-width` queries, EVERY one of them matches on a
// narrow screen — `max-width:768` is true at 390px just as `max-width:390` is —
// and the last one in the file wins. A phone got the tablet's scale and the
// page ran off the side, which looks exactly like the scaling never worked.
//
// Each step therefore carries both bounds, and scales by the bottom of its
// range: a range that scaled by its top would render wider than the narrowest
// screen it matches. The steps are 16px, so at worst a page sits 2% narrower
// than it could — a margin nobody sees, against an overflow everybody does.
func responsive(width, height float64) string {
	var b strings.Builder
	// The wrapper is given the page's own width. Left to grow to the body it
	// would be as wide as the screen, and the page inside it would spill out of
	// a box that claims to contain it — which measures as fitting while looking
	// like it does not.
	fmt.Fprintf(&b, ".sheet{width:%.0fpx;transform-origin:top left}\n", width)

	const step = 16
	const floor = 280 // narrower than any phone still sold

	scaled := func(w float64) string {
		s := w / width
		// The body must reserve the SCALED height: a transform moves pixels
		// without changing how much room the element claims, so the page would
		// otherwise leave a screen's worth of blank space below it.
		return fmt.Sprintf(".sheet{transform:scale(%.4f)}body{height:%.0fpx}", s, height*s)
	}

	for w := float64(floor); w < width; w += step {
		upper := math.Min(w+step-1, width-1)
		fmt.Fprintf(&b, "@media (min-width:%.0fpx) and (max-width:%.0fpx){%s}\n",
			w, upper, scaled(w))
	}
	// Anything narrower than the floor gets the floor's scale rather than
	// nothing at all.
	fmt.Fprintf(&b, "@media (max-width:%dpx){%s}\n", floor-1, scaled(floor-1))

	// Printing must not scale: a sheet of paper is already the right size.
	b.WriteString("@media print{.sheet{transform:none}body{height:auto}}\n")
	return b.String()
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
