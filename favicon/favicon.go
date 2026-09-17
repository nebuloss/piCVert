// Package favicon draws the tab icon: the green woodpecker, tinted per surface.
//
// Without one, every page logs a 404 for /favicon.ico — noise that hides the
// errors worth reading. Inline SVG rather than a file: nothing to fetch,
// nothing to store, no binary in the repository, and it scales from a 16 px tab
// to a README header without a second drawing.
//
// The shapes are the ones in assets/picvert.svg, on the same 32x32 grid. They
// are repeated here rather than read from that file because the icon must be
// servable by a binary running anywhere, and an icon that depends on finding a
// file is an icon that 404s on someone else's machine.
package favicon

import (
	"fmt"
	"strings"
)

// Palette is the colouring of one surface's icon.
//
// The administration interface gets a DIFFERENT one on purpose. Both surfaces
// are usually open side by side, on the same host and two ports; identical
// icons make it easy to act on the wrong tab — and one of those tabs deletes
// CVs.
type Palette struct {
	Trunk, Body, Belly, Rump, Crown, Mask, Beak, Tail string
}

// Public is the bird as it is: green body, red cap. Everything a visitor sees.
var Public = Palette{
	Trunk: "#B9C6BA", Body: "#5E9E42", Belly: "#CFE0A8", Rump: "#C9D84A",
	Crown: "#D3352B", Mask: "#23291F", Beak: "#3F4A38", Tail: "#4C6B3C",
}

// Admin is the same bird in amber. The colour says "this one deletes things".
var Admin = Palette{
	Trunk: "#D8CBB4", Body: "#B26A00", Belly: "#FFE0A6", Rump: "#F0B429",
	Crown: "#8A2F1E", Mask: "#2A2114", Beak: "#4A3A22", Tail: "#7A4A08",
}

// SVG draws the icon in a palette.
func SVG(p Palette) []byte {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" role="img" aria-label="piCVert">`)
	b.WriteString(`<title>piCVert</title>`)

	// The trunk: also the left edge of a sheet of paper.
	fmt.Fprintf(&b, `<rect x="3" y="2" width="3" height="28" rx="1.5" fill="%s"/>`, p.Trunk)
	// Tail, braced against the trunk — a woodpecker's third foot.
	fmt.Fprintf(&b, `<path d="M9 21 L6 29 L11.5 26.5 Z" fill="%s"/>`, p.Tail)

	fmt.Fprintf(&b, `<path d="M9 12C9 8.5 11.5 6 15 6C18.8 6 21.5 8.8 21.5 12.6`+
		`L21.5 18C21.5 22.4 18.4 25.5 14.6 25.5C11.4 25.5 9 23 9 19.6 Z" fill="%s"/>`, p.Body)
	fmt.Fprintf(&b, `<path d="M12 13.5C14.6 13.5 16.4 15.4 16.4 18.2`+
		`C16.4 21.4 14.8 23.6 12.4 24.2C10.4 23.4 9 21.7 9 19.6 Z" fill="%s"/>`, p.Belly)
	// The yellow-green rump: the field mark you see when it flies away.
	fmt.Fprintf(&b, `<path d="M9 18.5C10.6 19.8 11 22 10.4 24.6C9.4 23.4 9 21.8 9 19.8 Z" fill="%s"/>`, p.Rump)
	fmt.Fprintf(&b, `<path d="M10.2 10.2C10.6 6.6 13 4.4 16 4.4`+
		`C18.6 4.4 20.6 6 21.3 8.6C18.6 7.2 13.4 7.6 10.2 10.2 Z" fill="%s"/>`, p.Crown)
	fmt.Fprintf(&b, `<path d="M18.6 9.6C20.4 9.6 21.8 10.3 22.6 11.4L20.4 13`+
		`C19.4 11.6 18.6 10.6 18.6 9.6 Z" fill="%s"/>`, p.Mask)
	// Long, straight, chisel-tipped, and turned to face the page.
	fmt.Fprintf(&b, `<path d="M21.4 10.6 L30 12.1 L21.4 13.8 Z" fill="%s"/>`, p.Beak)

	b.WriteString(`<circle cx="19.1" cy="10.1" r="1.25" fill="#FFFFFF"/>`)
	fmt.Fprintf(&b, `<circle cx="19.1" cy="10.1" r="0.6" fill="%s"/>`, p.Mask)

	// Feet gripping the trunk. Short and overlapping the bark: drawn long they
	// read as two floating bars rather than as a grip.
	fmt.Fprintf(&b, `<path d="M10 14.6 L5.6 15.4 M10 19 L5.6 19.8" stroke="%s"`+
		` stroke-width="1.6" stroke-linecap="round" fill="none"/>`, p.Beak)
	fmt.Fprintf(&b, `<circle cx="6.2" cy="15.4" r="1.1" fill="%s"/>`, p.Beak)
	fmt.Fprintf(&b, `<circle cx="6.2" cy="19.8" r="1.1" fill="%s"/>`, p.Beak)

	b.WriteString(`</svg>`)
	return []byte(b.String())
}
