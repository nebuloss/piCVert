package pdf

import (
	"fmt"
	"math"
	"strings"

	"picvert/internal/layout"
)

// Painter draws a laid-out tree into a PDF content stream.
//
// # THE SECOND CONSUMER OF ONE LAYOUT
//
// This is the point of the whole engine. The HTML painter and this one receive
// the SAME frame, with the line breaks already decided, so they cannot disagree
// about where anything goes. The old engine laid the document out twice — once
// in CSS and once in react-pdf — and the two came out about a line apart per
// block, which is how a CV that measured as fitting arrived clipped.
//
// Here there is nothing left for either painter to decide. This one turns
// positions into drawing operators; it never computes one.
//
// # TWO COORDINATE SYSTEMS
//
// PDF's origin is the BOTTOM-left of the page and y grows upward; the layout is
// in CSS pixels from the top-left. And a PDF point is 1/72 inch where a CSS
// pixel is 1/96, so everything is scaled by 0.75. Both conversions happen in
// one place — see pt() and y() — because a second place would eventually
// disagree with the first.
type Painter struct {
	ops    strings.Builder
	faces  []*Face
	height float64 // page height in CSS pixels, for flipping y

	// used records which faces were actually drawn with, so the page's resource
	// dictionary names only those.
	used map[*Face]bool
	// images maps a data URI to the name the resource dictionary gives it.
	images map[string]string
}

// NewPainter starts a page.
func NewPainter(faces []*Face, pageHeight float64) *Painter {
	return &Painter{faces: faces, height: pageHeight, used: map[*Face]bool{}}
}

// pt converts CSS pixels (96 dpi) to PDF points (72 dpi).
func pt(px float64) float64 { return px * 0.75 }

// y flips a top-down coordinate into PDF's bottom-up space.
func (p *Painter) y(top float64) float64 { return pt(p.height - top) }

// num formats a number for a content stream: short, and never in exponent
// notation, which the format does not accept.
func num(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", v), "0"), ".")
}

// --- drawing primitives -------------------------------------------------------

// rect appends a rounded rectangle path.
//
// Corners are Bézier curves, because PDF has no arc operator — the magic
// constant is the usual 4/3·tan(π/8), which puts a cubic curve close enough to
// a quarter circle that no eye can tell.
func (p *Painter) rect(x, y, w, h, r float64) {
	if r <= 0 {
		fmt.Fprintf(&p.ops, "%s %s %s %s re\n", num(x), num(y-h), num(w), num(h))
		return
	}
	r = math.Min(r, math.Min(w, h)/2)
	const k = 0.5523
	c := r * k
	top, bottom := y, y-h
	left, right := x, x+w

	fmt.Fprintf(&p.ops, "%s %s m\n", num(left+r), num(top))
	fmt.Fprintf(&p.ops, "%s %s l\n", num(right-r), num(top))
	fmt.Fprintf(&p.ops, "%s %s %s %s %s %s c\n",
		num(right-r+c), num(top), num(right), num(top-r+c), num(right), num(top-r))
	fmt.Fprintf(&p.ops, "%s %s l\n", num(right), num(bottom+r))
	fmt.Fprintf(&p.ops, "%s %s %s %s %s %s c\n",
		num(right), num(bottom+r-c), num(right-r+c), num(bottom), num(right-r), num(bottom))
	fmt.Fprintf(&p.ops, "%s %s l\n", num(left+r), num(bottom))
	fmt.Fprintf(&p.ops, "%s %s %s %s %s %s c\n",
		num(left+r-c), num(bottom), num(left), num(bottom+r-c), num(left), num(bottom+r))
	fmt.Fprintf(&p.ops, "%s %s l\n", num(left), num(top-r))
	fmt.Fprintf(&p.ops, "%s %s %s %s %s %s c\n",
		num(left), num(top-r+c), num(left+r-c), num(top), num(left+r), num(top))
	p.ops.WriteString("h\n")
}

// setFill writes a colour, given as `#rrggbb`.
func (p *Painter) setFill(colour string) bool {
	r, g, b, ok := parseHex(colour)
	if !ok {
		return false
	}
	fmt.Fprintf(&p.ops, "%s %s %s rg\n", num(r), num(g), num(b))
	return true
}

func (p *Painter) setStroke(colour string) bool {
	r, g, b, ok := parseHex(colour)
	if !ok {
		return false
	}
	fmt.Fprintf(&p.ops, "%s %s %s RG\n", num(r), num(g), num(b))
	return true
}

func parseHex(s string) (r, g, b float64, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	var v [3]int
	for i := 0; i < 3; i++ {
		if _, err := fmt.Sscanf(s[i*2:i*2+2], "%02x", &v[i]); err != nil {
			return 0, 0, 0, false
		}
	}
	return float64(v[0]) / 255, float64(v[1]) / 255, float64(v[2]) / 255, true
}

// --- the Painter interface ----------------------------------------------------

// surface paints a frame's own background and border.
//
// Shared by every node shape, because a background belongs to a BOX and any
// node is one. It used to live inside Box alone, so a text node with a
// background — a chip, a date badge, the title pill — drew its text and nothing
// behind it. On screen those all have their fill, because the HTML painter
// writes the same geometry for every shape; the PDF quietly dropped it.
func (p *Painter) surface(f *layout.Frame) {
	s := f.Style
	x, y := pt(f.X), p.y(f.Y)
	w, h := pt(f.Width), pt(f.Height)
	if w <= 0 || h <= 0 {
		return
	}

	// An ellipse is a box whose radius is half its shorter side — what the HTML
	// painter means by `border-radius:50%`.
	radius := pt(s.Radius)
	if s.Display == layout.Ellipse {
		radius = math.Min(w, h) / 2
	}

	if g := s.Background.Gradient; g != nil {
		p.gradient(f, g)
	} else if s.Background.Colour != "" {
		p.ops.WriteString("q\n")
		if p.setFill(s.Background.Colour) {
			p.rect(x, y, w, h, radius)
			p.ops.WriteString("f\n")
		}
		p.ops.WriteString("Q\n")
	}

	if s.Border.Width > 0 && s.Border.Colour != "" {
		p.ops.WriteString("q\n")
		if p.setStroke(s.Border.Colour) {
			fmt.Fprintf(&p.ops, "%s w\n", num(pt(s.Border.Width)))
			inset := pt(s.Border.Width) / 2
			p.rect(x+inset, y-inset, w-2*inset, h-2*inset, math.Max(0, radius-inset))
			p.ops.WriteString("S\n")
		}
		p.ops.WriteString("Q\n")
	}
}

// Box paints a container: its background, its border, then what is inside it.
func (p *Painter) Box(f *layout.Frame, in func()) {
	p.surface(f)
	// Clipping is deliberately NOT applied to anything but the page. A PDF that
	// silently cut a card off would hide exactly the overflow the engine exists
	// to report — better that it shows, since the fit check has already named
	// it.
	in()
}

// gradient paints a linear gradient as an axial shading.
//
// Approximated by bands rather than emitted as a shading dictionary: a CV has
// one gradient, in its header, and forty bands across 700 points is invisible
// banding. A shading dictionary would be forty lines of object plumbing for a
// result nobody could tell apart.
func (p *Painter) gradient(f *layout.Frame, g *layout.Gradient) {
	x, y := pt(f.X), p.y(f.Y)
	w, h := pt(f.Width), pt(f.Height)
	if w <= 0 || h <= 0 || len(g.Stops) < 2 {
		return
	}

	p.ops.WriteString("q\n")
	// Clipped to the rounded box, in page coordinates and before any transform:
	// a clip set inside a rotated frame would itself be rotated.
	p.rect(x, y, w, h, pt(f.Style.Radius))
	p.ops.WriteString("W n\n")

	// Banded along the gradient's own axis.
	//
	// A stripe has to cover the box even when it is slanted, so each band is
	// drawn as a rectangle rotated into the gradient's direction — which the
	// content stream does with a transform, rather than by computing four
	// corners. Doing that arithmetic by hand put bands at negative coordinates
	// and painted the header off the page.
	const bands = 64
	rad := (g.Angle - 90) * math.Pi / 180
	cos, sin := math.Cos(rad), math.Sin(rad)

	// How far the box reaches along the axis, and across it. The diagonal
	// covers either, whatever the angle.
	diag := math.Hypot(w, h)
	cxc, cyc := x+w/2, y-h/2
	band := diag/bands + 0.5

	// The band rectangles are drawn in a rotated frame centred on the box, so
	// each one is axis-aligned there and the transform does the slanting. No
	// second `q` for it: the transform and the clip are undone together by the
	// single `Q` that closes this function. An unmatched `q` leaves the clip in
	// force for everything drawn afterwards — which hid most of the page.
	fmt.Fprintf(&p.ops, "%s %s %s %s %s %s cm\n",
		num(cos), num(sin), num(-sin), num(cos), num(cxc), num(cyc))
	for i := 0; i < bands; i++ {
		t := (float64(i) + 0.5) / bands
		if !p.setFill(gradientAt(g, t)) {
			continue
		}
		off := (t-0.5)*diag - band/2
		fmt.Fprintf(&p.ops, "%s %s %s %s re\nf\n",
			num(off), num(-diag/2), num(band), num(diag))
	}
	p.ops.WriteString("Q\n")
}

// gradientAt interpolates the stops at a position.
func gradientAt(g *layout.Gradient, t float64) string {
	for i := 1; i < len(g.Stops); i++ {
		a, b := g.Stops[i-1], g.Stops[i]
		if t > b.At && i < len(g.Stops)-1 {
			continue
		}
		span := b.At - a.At
		if span <= 0 {
			return b.Colour
		}
		f := math.Max(0, math.Min(1, (t-a.At)/span))
		return mixHex(a.Colour, b.Colour, f)
	}
	return g.Stops[len(g.Stops)-1].Colour
}

func mixHex(a, b string, f float64) string {
	ar, ag, ab, ok1 := parseHex(a)
	br, bg, bb, ok2 := parseHex(b)
	if !ok1 || !ok2 {
		return a
	}
	return fmt.Sprintf("#%02X%02X%02X",
		int((ar+(br-ar)*f)*255), int((ag+(bg-ag)*f)*255), int((ab+(bb-ab)*f)*255))
}

// Shape paints a polygon.
func (p *Painter) Shape(f *layout.Frame) {
	s := f.Style
	sides := s.Sides
	if sides < 3 {
		sides = 6
	}
	x, y := pt(f.X), p.y(f.Y)
	w, h := pt(f.Width), pt(f.Height)

	p.ops.WriteString("q\n")
	if s.Opacity > 0 && s.Opacity < 1 {
		fmt.Fprintf(&p.ops, "/GS%d gs\n", int(s.Opacity*100))
	}
	// Filled or stroked, and rounded at the corners — the same shape the HTML
	// painter draws, expressed in the operators this format has.
	ok := false
	if s.Stroke > 0 {
		ok = p.setStroke(s.Background.Colour)
		if ok {
			fmt.Fprintf(&p.ops, "%s w\n", num(pt(s.Stroke)))
		}
	} else {
		ok = p.setFill(s.Background.Colour)
	}
	if ok {
		p.polygonPath(x, y, w, h, sides, s.Rotate, s.Corner)
		if s.Stroke > 0 {
			p.ops.WriteString("S\n")
		} else {
			p.ops.WriteString("f\n")
		}
	}
	p.ops.WriteString("Q\n")
}

// polygonPath traces a polygon, rounding its corners the way the HTML painter
// does — with a quadratic through each vertex, raised to a cubic because PDF
// has no quadratic operator.
func (p *Painter) polygonPath(x, y, w, h float64, sides int, rotate, corner float64) {
	pts := polygonPoints(sides, rotate)
	n := len(pts) / 2
	at := func(i int) (float64, float64) {
		return x + pts[(i%n)*2]*w/100, y - pts[(i%n)*2+1]*h/100
	}
	if corner <= 0 {
		for i := 0; i < n; i++ {
			cx, cy := at(i)
			op := "l"
			if i == 0 {
				op = "m"
			}
			fmt.Fprintf(&p.ops, "%s %s %s\n", num(cx), num(cy), op)
		}
		p.ops.WriteString("h\n")
		return
	}
	// The radius in page units, from the viewBox fraction the theme gave.
	r := corner / 100 * math.Min(w, h)
	for i := 0; i < n; i++ {
		cx, cy := at(i)
		px, py := at(i - 1 + n)
		nx, ny := at(i + 1)
		ax, ay := towardsPt(cx, cy, px, py, r)
		bx, by := towardsPt(cx, cy, nx, ny, r)
		if i == 0 {
			fmt.Fprintf(&p.ops, "%s %s m\n", num(ax), num(ay))
		} else {
			fmt.Fprintf(&p.ops, "%s %s l\n", num(ax), num(ay))
		}
		// A quadratic (a,c,b) is the cubic with controls two-thirds of the way
		// from each end towards the quadratic's own control point.
		c1x, c1y := ax+2.0/3*(cx-ax), ay+2.0/3*(cy-ay)
		c2x, c2y := bx+2.0/3*(cx-bx), by+2.0/3*(cy-by)
		fmt.Fprintf(&p.ops, "%s %s %s %s %s %s c\n",
			num(c1x), num(c1y), num(c2x), num(c2y), num(bx), num(by))
	}
	p.ops.WriteString("h\n")
}

func towardsPt(x, y, tx, ty, r float64) (float64, float64) {
	dx, dy := tx-x, ty-y
	d := math.Hypot(dx, dy)
	if d == 0 {
		return x, y
	}
	f := math.Min(r/d, 0.5)
	return x + dx*f, y + dy*f
}

// polygonPoints is the same normalised polygon the HTML painter draws, in
// hundredths of the box.
func polygonPoints(sides int, rotate float64) []float64 {
	xs := make([]float64, sides)
	ys := make([]float64, sides)
	minX, maxX := math.Inf(1), math.Inf(-1)
	minY, maxY := math.Inf(1), math.Inf(-1)
	for i := 0; i < sides; i++ {
		a := (float64(i)/float64(sides))*2*math.Pi - math.Pi/2 + rotate*math.Pi/180
		xs[i], ys[i] = math.Cos(a), math.Sin(a)
		minX, maxX = math.Min(minX, xs[i]), math.Max(maxX, xs[i])
		minY, maxY = math.Min(minY, ys[i]), math.Max(maxY, ys[i])
	}
	out := make([]float64, 0, sides*2)
	for i := 0; i < sides; i++ {
		out = append(out,
			(xs[i]-minX)/(maxX-minX)*100,
			(ys[i]-minY)/(maxY-minY)*100)
	}
	return out
}

// Image paints a picture or an inline glyph.
func (p *Painter) Image(f *layout.Frame) {
	p.surface(f)
	src := f.Node.Src
	if spec, ok := strings.CutPrefix(src, "icon:"); ok {
		p.icon(f, spec)
		return
	}
	if name, ok := p.images[src]; ok {
		x, y := pt(f.X), p.y(f.Y)
		w, h := pt(f.Width), pt(f.Height)
		p.ops.WriteString("q\n")
		// A rounded photo is clipped to its own box: the avatar is a disc.
		if f.Style.Radius > 0 {
			p.rect(x, y, w, h, pt(f.Style.Radius))
			p.ops.WriteString("W n\n")
		}
		fmt.Fprintf(&p.ops, "%s 0 0 %s %s %s cm\n/%s Do\nQ\n",
			num(w), num(h), num(x), num(y-h), name)
	}
}

// icon draws a glyph from the template's icon set.
//
// The SVG path is translated to PDF operators rather than embedded: PDF has no
// SVG. Only the subset the icon sets use — move, line, cubic, close — which is
// what a 24x24 material icon is made of.
func (p *Painter) icon(f *layout.Frame, spec string) {
	viewBox, path, _ := strings.Cut(spec, "|")
	vb := parseViewBox(viewBox)
	if vb == 0 {
		return
	}
	// The glyph is drawn inside the tile's padding, like the HTML one.
	inset := f.Style.Padding
	x := pt(f.X + inset.Left)
	y := p.y(f.Y + inset.Top)
	size := pt(f.Width - inset.Left - inset.Right)
	scale := size / vb

	p.ops.WriteString("q\n")
	if p.setFill(orElse(f.Style.Colour, "#000000")) {
		fmt.Fprintf(&p.ops, "%s 0 0 %s %s %s cm\n", num(scale), num(-scale), num(x), num(y))
		p.ops.WriteString(svgPathToPDF(path))
		p.ops.WriteString("f\n")
	}
	p.ops.WriteString("Q\n")
}

func orElse(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func parseViewBox(vb string) float64 {
	parts := strings.Fields(vb)
	if len(parts) != 4 {
		return 0
	}
	var w float64
	if _, err := fmt.Sscanf(parts[2], "%f", &w); err != nil {
		return 0
	}
	return w
}

// Text draws the lines the engine already broke.
func (p *Painter) Text(f *layout.Frame) {
	p.surface(f)
	s := f.Style
	left := pt(f.X + s.Padding.Left + s.Border.Width)
	top := f.Y + s.Padding.Top + s.Border.Width

	for i, line := range f.Lines {
		baseline := p.y(top + f.Baselines[i])
		x := left
		for _, piece := range line.Pieces {
			face := p.faceFor(s.Family, int(s.WeightOf(piece.Bold)), s.Italic)
			if face == nil {
				continue
			}
			p.used[face] = true

			p.ops.WriteString("BT\n")
			if p.setFill(orElse(s.ColourOf(piece.Bold, piece.Colour), "#000000")) {
				fmt.Fprintf(&p.ops, "/%s %s Tf\n", face.Name, num(pt(s.Size)))
				// ALWAYS, even at zero. Character spacing is TEXT STATE, not
				// part of the text object: ET ends the object and resets the
				// matrix, and leaves Tc exactly where it was. Written only when
				// non-zero, a section title's letter-spacing carried on into
				// every paragraph drawn after it — 0.6 pt per character, which
				// on a thirty-character run is twenty points of width the
				// engine never measured, drawn straight over the run beside it.
				//
				// Nothing about this shows up in the extracted text, so it
				// survived every check that read the PDF rather than looked at
				// it.
				fmt.Fprintf(&p.ops, "%s Tc\n", num(pt(s.Letter)))
				fmt.Fprintf(&p.ops, "1 0 0 1 %s %s Tm\n", num(x), num(baseline))
				fmt.Fprintf(&p.ops, "<%s> Tj\n", face.Encode(piece.Text))
			}
			p.ops.WriteString("ET\n")
			x += pt(piece.Width)
		}
	}
}

// faceFor finds the closest embedded face, exactly as the measurement did — or
// the text would be drawn in a different face from the one it was measured in.
func (p *Painter) faceFor(family string, weight int, italic bool) *Face {
	var best *Face
	bestDist := 1 << 30
	for _, f := range p.faces {
		if f.Family != family {
			continue
		}
		d := abs(f.Weight - weight)
		if f.Italic != italic {
			d += 1000
		}
		if d < bestDist {
			best, bestDist = f, d
		}
	}
	if best == nil && len(p.faces) > 0 {
		return p.faces[0]
	}
	return best
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
