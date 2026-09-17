package layout

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
)

// HTMLPainter writes a laid-out tree as HTML.
//
// # WHY EVERY LINE IS PLACED
//
// The obvious emitter would write paragraphs and let the browser wrap them.
// That is what the previous engine did, and it is exactly what broke: the
// browser reached a different number of lines from the PDF renderer, the two
// answers differed by about a line per block, and a CV that measured as fitting
// came out cut off.
//
// So the browser is given nothing to decide. Each line the engine broke becomes
// its own run, placed at the baseline it was measured for. Whatever the browser
// would have done with the paragraph is irrelevant, because it never sees one.
//
// The usual objection to pre-broken text — that it cannot reflow — does not
// apply: the page is a fixed box with `overflow:hidden`. It never reflows. This
// is the rare layout where baking the lines in is not a compromise.
//
// Text stays selectable and reads in order, because the runs are emitted in
// reading order and each holds real text.
type HTMLPainter struct {
	b strings.Builder
	// class is put on the next box painted, and then cleared. Only the page
	// gets one: it is the handle the surrounding document needs in order to
	// scale the sheet, and nothing inside needs naming.
	class string
	// origin is the content corner of the enclosing box. CSS positions an
	// absolute child against its positioned ancestor, so the absolute figures
	// the engine computed have to be made relative on the way out — and this is
	// the only place that knows which ancestor that is.
	origin point
}

type point struct{ x, y float64 }

// RenderHTML paints a page and returns its markup.
// RenderHTML paints a page and returns its markup.
//
// The page is painted like any other box, and that is the point. It used to be
// written by hand here — a div with a width, a height and a clip — and only its
// CHILDREN were painted. So the page's own style was silently dropped: the
// theme asks for a pale surface behind everything, and the page came out white.
// Every card is white too, so a whole column of them vanished into the
// background and only their contents showed, which reads as cards that have
// lost their shape rather than as a missing backdrop.
//
// A root that is exempt from the painting rules is a root whose style nobody
// checks. Painting it normally means the theme is obeyed at every level, and
// the class name is all this function still has to add.
func RenderHTML(r *Render) string {
	p := &HTMLPainter{class: "page"}
	// The page is positioned by the wrapper that scales it, not by an ancestor
	// frame, so it is written from the origin.
	p.origin = point{r.Frame.X, r.Frame.Y}
	r.Frame.Paint(p)
	return p.b.String()
}

// num prints a length the way CSS wants it: short, and without a trailing `.0`
// that would make two identical numbers look different in a diff.
func num(v float64) string {
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
}

// frame writes the geometry and paint every node shape shares.
func (p *HTMLPainter) frame(f *Frame) string { return p.frameOf(f, f.Style) }

// frameOf is frame, with the style to use stated — for a painter that needs to
// draw the box differently from what the node asked for.
func (p *HTMLPainter) frameOf(f *Frame, s Style) string {
	var style strings.Builder
	if p.class != "" {
		// The page itself: `relative`, so it is the box every absolute
		// descendant is measured against, and so the wrapper can place it.
		fmt.Fprintf(&style, "position:relative;width:%spx;height:%spx",
			num(f.Width), num(f.Height))
	} else {
		fmt.Fprintf(&style, "position:absolute;left:%spx;top:%spx;width:%spx;height:%spx",
			num(f.X-p.origin.x), num(f.Y-p.origin.y), num(f.Width), num(f.Height))
	}

	if s.Display == Ellipse {
		style.WriteString(";border-radius:50%")
	} else if s.Radius > 0 {
		fmt.Fprintf(&style, ";border-radius:%spx", num(s.Radius))
	}
	if s.Clip {
		style.WriteString(";overflow:hidden")
	}
	if s.Border.Width > 0 {
		fmt.Fprintf(&style, ";box-sizing:border-box;border:%spx solid %s",
			num(s.Border.Width), s.Border.Colour)
	}
	if bg := cssBackground(s.Background); bg != "" {
		fmt.Fprintf(&style, ";background:%s", bg)
	}
	if sh := s.Shadow; sh != nil {
		fmt.Fprintf(&style, ";box-shadow:%spx %spx %spx %s",
			num(sh.X), num(sh.Y), num(sh.Blur), sh.Colour)
	}
	return style.String()
}

func cssBackground(fill Fill) string {
	if fill.Gradient != nil {
		stops := make([]string, 0, len(fill.Gradient.Stops))
		for _, st := range fill.Gradient.Stops {
			stops = append(stops, fmt.Sprintf("%s %s%%", st.Colour, num(st.At*100)))
		}
		return fmt.Sprintf("linear-gradient(%sdeg,%s)",
			num(fill.Gradient.Angle), strings.Join(stops, ","))
	}
	return fill.Colour
}

// Box opens a container, paints what is inside it, and closes it.
//
// WHY THE ORIGIN IS THE BOX'S CORNER AND NOT ITS CONTENT CORNER. An absolutely
// positioned child is placed against its ancestor's PADDING BOX — CSS does not
// move it in by the padding, the way normal flow does. So writing a child's
// offset relative to the content corner subtracted the padding that CSS was
// never going to add back, and every card drew its contents hard against its
// own edge: cancelled exactly, on every nested box, which is why it looked like
// the padding had simply been forgotten rather than counted twice.
//
// The engine already placed the child correctly, inside the padding. The
// painter's only job is to express that same position relative to the box CSS
// will measure it from.
func (p *HTMLPainter) Box(f *Frame, in func()) {
	if p.class != "" {
		fmt.Fprintf(&p.b, `<div class="%s" style="%s">`, p.class, p.frame(f))
		p.class = ""
	} else {
		fmt.Fprintf(&p.b, `<div style="%s">`, p.frame(f))
	}
	// Saved and restored rather than recomputed on the way out, so a change
	// here cannot leave the origin pointing at the wrong ancestor.
	saved := p.origin
	p.origin = point{f.X, f.Y}
	in()
	p.origin = saved
	p.b.WriteString(`</div>`)
}

// Image paints a picture, or an inline glyph.
//
// Icons travel as `icon:<viewBox>|<path>` rather than as files, because the
// page is a single self-contained document: a request for an icon would be a
// request the promise of opening offline does not allow.
func (p *HTMLPainter) Image(f *Frame) {
	style := p.frame(f)
	src := f.Node.Src

	if spec, ok := strings.CutPrefix(src, "icon:"); ok {
		viewBox, path, _ := strings.Cut(spec, "|")
		fmt.Fprintf(&p.b,
			`<div style="%s"><svg viewBox="%s" style="width:100%%;height:100%%;display:block">`+
				`<path d="%s" fill="%s"/></svg></div>`,
			style, html.EscapeString(viewBox), html.EscapeString(path),
			orElse(f.Style.Colour, "currentColor"))
		return
	}
	if src == "" {
		fmt.Fprintf(&p.b, `<div style="%s"></div>`, style)
		return
	}
	fmt.Fprintf(&p.b, `<img src="%s" alt="%s" style="%s;object-fit:cover;display:block">`,
		html.EscapeString(src), html.EscapeString(f.Node.Alt), style)
}

func orElse(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// Shape paints a regular polygon inscribed in the node's box.
//
// An inline SVG rather than a CSS `clip-path`: the polygon has to be drawn the
// same way by the PDF emitter, and a path of points is something both can
// consume. A clip-path would be a CSS-only description, and the two renderings
// would then have one more thing to disagree about.
func (p *HTMLPainter) Shape(f *Frame) {
	s := f.Style
	// The colour belongs to the POLYGON, not to the box holding it. Left on the
	// box it painted a solid rectangle behind the shape, which then filled its
	// own outline invisibly — six correct points drawing as a square, and
	// looking for all the world like the geometry was wrong.
	box := s
	box.Background = Fill{}
	style := p.frameOf(f, box)
	if s.Opacity > 0 {
		style += fmt.Sprintf(";opacity:%s", num(s.Opacity))
	}
	// A rounded polygon is a path, not a <polygon>: the corners are arcs. The
	// viewBox keeps its own square coordinates so the shape stretches with the
	// box, and the aspect ratio is the caller's to get right.
	paint := fmt.Sprintf(`fill="%s"`, orElse(s.Background.Colour, "currentColor"))
	if s.Stroke > 0 {
		paint = fmt.Sprintf(`fill="none" stroke="%s" stroke-width="%s"`,
			orElse(s.Background.Colour, "currentColor"), num(s.Stroke*100/math.Max(f.Width, 1)))
	}
	fmt.Fprintf(&p.b,
		`<div style="%s"><svg viewBox="0 0 100 100" preserveAspectRatio="none" `+
			`style="width:100%%;height:100%%;display:block">`+
			`<path d="%s" %s/></svg></div>`,
		style, polygonPath(s.Sides, s.Rotate, s.Corner), paint)
}

// polygonPoints is a regular polygon inscribed in a 100x100 box.
//
// In the SVG's own coordinates rather than in pixels, so neither emitter has to
// recompute the shape when a theme resizes the node.
//
// THE VIEWBOX IS SQUARE AND THE BOX USUALLY IS NOT, which is the whole
// difficulty. Stretched to fill a 54x62 box with `preserveAspectRatio="none"`,
// a hexagon's six points land on the corners of its bounding box and it draws
// as a rectangle — which is exactly what happened, and looked like the polygon
// code being wrong rather than the scaling.
//
// So the points are normalised to touch the edges of the viewBox in both axes:
// the caller gives the box the aspect ratio the shape should have (a hexagon
// standing on a point is about 1:1.155), and the shape then fills it honestly.
// polygonPath is the polygon as an SVG path, with rounded corners.
//
// Each vertex becomes a short arc between the two edges meeting there, so the
// shape keeps its silhouette and loses its points — which is what the design
// asks for, and what a hexagon needs in order to read as ornament rather than
// as a warning sign.
func polygonPath(sides int, rotate, corner float64) string {
	pts := polygonVertices(sides, rotate)
	if corner <= 0 {
		var b strings.Builder
		for i := 0; i < len(pts); i += 2 {
			op := "L"
			if i == 0 {
				op = "M"
			}
			fmt.Fprintf(&b, "%s%s %s ", op, num(pts[i]), num(pts[i+1]))
		}
		b.WriteString("Z")
		return b.String()
	}

	// The radius in viewBox units, capped so neighbouring corners cannot meet.
	n := len(pts) / 2
	r := math.Min(corner, 24)
	var b strings.Builder
	for i := 0; i < n; i++ {
		cx, cy := pts[i*2], pts[i*2+1]
		px, py := pts[((i-1+n)%n)*2], pts[((i-1+n)%n)*2+1]
		nx, ny := pts[((i+1)%n)*2], pts[((i+1)%n)*2+1]

		// A point on each edge, r away from the vertex.
		ax, ay := towards(cx, cy, px, py, r)
		bx, by := towards(cx, cy, nx, ny, r)
		if i == 0 {
			fmt.Fprintf(&b, "M%s %s ", num(ax), num(ay))
		} else {
			fmt.Fprintf(&b, "L%s %s ", num(ax), num(ay))
		}
		// A quadratic through the vertex rounds it without needing the arc's
		// centre, and at this size is indistinguishable from one.
		fmt.Fprintf(&b, "Q%s %s %s %s ", num(cx), num(cy), num(bx), num(by))
	}
	b.WriteString("Z")
	return b.String()
}

// towards is the point r of the way from (x,y) to (tx,ty).
func towards(x, y, tx, ty, r float64) (float64, float64) {
	dx, dy := tx-x, ty-y
	d := math.Hypot(dx, dy)
	if d == 0 {
		return x, y
	}
	f := math.Min(r/d, 0.5)
	return x + dx*f, y + dy*f
}

// polygonVertices is the polygon's corners, normalised to fill the viewBox.
func polygonVertices(sides int, rotate float64) []float64 {
	if sides < 3 {
		sides = 6
	}
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

func polygonPoints(sides int, rotate float64) string {
	if sides < 3 {
		sides = 6
	}
	xs := make([]float64, sides)
	ys := make([]float64, sides)
	minX, maxX := math.Inf(1), math.Inf(-1)
	minY, maxY := math.Inf(1), math.Inf(-1)
	for i := 0; i < sides; i++ {
		// Start at the top: the orientation anyone drawing one by hand would
		// choose, rather than the one the maths gives.
		angle := (float64(i)/float64(sides))*2*math.Pi - math.Pi/2 + rotate*math.Pi/180
		xs[i] = math.Cos(angle)
		ys[i] = math.Sin(angle)
		minX, maxX = math.Min(minX, xs[i]), math.Max(maxX, xs[i])
		minY, maxY = math.Min(minY, ys[i]), math.Max(maxY, ys[i])
	}

	var b strings.Builder
	for i := 0; i < sides; i++ {
		x := (xs[i] - minX) / (maxX - minX) * 100
		y := (ys[i] - minY) / (maxY - minY) * 100
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s,%s", num(x), num(y))
	}
	return b.String()
}

// Text places each measured line at its own baseline.
//
// `white-space:pre` because the spacing inside a line was already measured:
// letting the browser collapse runs of spaces would move text away from where
// it was measured to be. The line cannot wrap — it is one line by construction
// — so `pre` costs nothing and removes a decision.
//
// The baseline is a transform on a zero-height box rather than a `top`, so the
// text sits exactly where it was measured regardless of the font's own line
// box, which differs between browsers and would otherwise put back the drift
// this engine exists to remove.
func (p *HTMLPainter) Text(f *Frame) {
	s := f.Style
	fmt.Fprintf(&p.b, `<div style="%s">`, p.frame(f))

	base := fmt.Sprintf("font-size:%spx", num(s.Size))
	if s.Family != "" {
		// An empty family is invalid CSS and silently voids the whole
		// declaration, taking the size with it.
		base = fmt.Sprintf("font-family:'%s';%s", s.Family, base)
	}
	if s.Letter != 0 {
		base += fmt.Sprintf(";letter-spacing:%spx", num(s.Letter))
	}
	if s.Italic {
		base += ";font-style:italic"
	}

	// Each line sits in a box of exactly one line's height, placed at that
	// line's top, and the text inside it is pushed down to the baseline.
	//
	// The baseline used to be a transform on a zero-height box at the top of
	// the paragraph. It DRAWS in the right place — a transform moves the ink —
	// but the box keeps its declared geometry, so every line's rectangle
	// covered the whole paragraph and then some. Nothing looked wrong; what
	// broke was everything that asks the page where its text is. Selecting a
	// line selected its neighbours, and an overlap check could not tell a real
	// collision from this one.
	//
	// The ink lands where it did. The difference is that the geometry now says
	// so.
	// Lines start at the frame's CONTENT corner, not its own. A chip is a box
	// with padding and a radius, and its text was written from the box corner —
	// so it sat hard against the top edge with all the slack below it, which is
	// what "the text is not centred in the rounded shape" looks like. The
	// engine had already reserved that padding when it sized the box; only the
	// painter was not honouring it.
	left := s.Padding.Left + s.Border.Width
	top := s.Padding.Top + s.Border.Width

	// The height the ENGINE settled, not one recomputed here: two places
	// deriving it is two places to derive it differently.
	lh := f.LineHeight
	for i, line := range f.Lines {
		fmt.Fprintf(&p.b,
			`<div style="position:absolute;left:%spx;top:%spx;width:%spx;height:%spx;`+
				`line-height:%spx;white-space:pre;%s">`,
			num(left), num(top+float64(i)*lh), num(line.Width), num(lh), num(lh), base)
		for _, piece := range line.Pieces {
			fmt.Fprintf(&p.b, `<span style="font-weight:%d;color:%s">%s</span>`,
				int(s.WeightOf(piece.Bold).OrRegular()),
				orElse(s.ColourOf(piece.Bold, piece.Colour), "#000000"),
				html.EscapeString(piece.Text))
		}
		p.b.WriteString(`</div>`)
	}
	p.b.WriteString(`</div>`)
}

// `line-height` set to the measured line height is what puts the baseline back
// where the engine computed it: a CSS line box centres its text in that height,
// which is the same rule the engine used to place the baseline. So the two
// agree without the painter having to know the font's metrics.
