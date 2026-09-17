package layout

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
)

// HTML emits a page from an already-computed layout.
//
// # WHY EVERY LINE IS PLACED
//
// The obvious emitter would write paragraphs and let the browser wrap them.
// That is what the engine did before, and it is exactly the thing that broke:
// the browser reached a different number of lines from the PDF renderer, the
// two answers differed by about a line per block, and a CV that measured as
// fitting came out cut off.
//
// So the browser is given nothing to decide. Each line this engine broke
// becomes its own absolutely-positioned run at the baseline it was measured
// for. Whatever the browser would have done with the paragraph is irrelevant,
// because it never sees one.
//
// The usual objection to pre-broken text — that it cannot reflow — does not
// apply: the page is a fixed 794×1123 box with `overflow:hidden`. It never
// reflows. This layout is the rare case where baking the lines in is not a
// compromise.
//
// Text remains selectable, and reads in order, because the runs are emitted in
// reading order and each holds real text.
func (f *Frame) HTML(pageWidth, pageHeight float64) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		`<div class="page" style="position:relative;width:%spx;height:%spx;overflow:hidden">`,
		num(pageWidth), num(pageHeight)))
	f.emit(&b, true, 0, 0)
	b.WriteString(`</div>`)
	return b.String()
}

// num prints a length the way CSS wants it: short, and without a trailing
// `.0` that would make two identical numbers look different in a diff.
func num(v float64) string {
	rounded := math.Round(v*100) / 100
	return strconv.FormatFloat(rounded, 'f', -1, 64)
}

// emit writes one frame and its children.
//
// Positions are computed absolutely by the engine but written RELATIVE to the
// parent, because that is what an absolutely-positioned box inside another one
// means in CSS. Writing the absolute figures made every nested box add its
// parent's offset a second time, and the page came out empty below the header.
func (f *Frame) emit(b *strings.Builder, isRoot bool, originX, originY float64) {
	s := f.Style

	if !isRoot {
		var style strings.Builder
		fmt.Fprintf(&style, "position:absolute;left:%spx;top:%spx;width:%spx;height:%spx",
			num(f.X-originX), num(f.Y-originY), num(f.Width), num(f.Height))

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
		if bg := background(s.Background); bg != "" {
			fmt.Fprintf(&style, ";background:%s", bg)
		}

		switch s.Display {
		case Image:
			f.emitImage(b, style.String())
			return
		case Text:
			f.emitText(b, style.String())
			return
		}
		fmt.Fprintf(b, `<div style="%s">`, style.String())
	}

	// A child's offsets are measured from this frame's CONTENT box, which is
	// where its own padding and border have already moved the origin to.
	childX := f.X + s.Padding.Left + s.Border.Width
	childY := f.Y + s.Padding.Top + s.Border.Width
	if isRoot {
		childX, childY = 0, 0
	}
	for _, child := range f.Children {
		child.emit(b, false, childX, childY)
	}
	if !isRoot {
		b.WriteString(`</div>`)
	}
}

func background(fill Fill) string {
	if fill.Gradient != nil {
		stops := make([]string, 0, len(fill.Gradient.Stops))
		for _, st := range fill.Gradient.Stops {
			stops = append(stops, fmt.Sprintf("%s %s%%", st.Colour, num(st.At*100)))
		}
		return fmt.Sprintf("linear-gradient(%sdeg,%s)", num(fill.Gradient.Angle), strings.Join(stops, ","))
	}
	return fill.Colour
}

// emitImage draws a picture, or an inline icon.
//
// Icons travel as `icon:<viewBox>|<path>` rather than as files, because a CV is
// a single self-contained document: a request for an icon would be a request
// the promise of opening offline does not allow.
func (f *Frame) emitImage(b *strings.Builder, style string) {
	src := f.Node.Src
	if strings.HasPrefix(src, "icon:") {
		spec := strings.TrimPrefix(src, "icon:")
		viewBox, path, _ := strings.Cut(spec, "|")
		fmt.Fprintf(b,
			`<div style="%s"><svg viewBox="%s" style="width:100%%;height:100%%;display:block"><path d="%s" fill="%s"/></svg></div>`,
			style, html.EscapeString(viewBox), html.EscapeString(path), colourOr(f.Style.Colour, "currentColor"))
		return
	}
	if src == "" {
		fmt.Fprintf(b, `<div style="%s"></div>`, style)
		return
	}
	fmt.Fprintf(b, `<img src="%s" alt="%s" style="%s;object-fit:cover;display:block">`,
		html.EscapeString(src), html.EscapeString(f.Node.Alt), style)
}

func colourOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// emitText places each measured line at its own baseline.
//
// `white-space:pre` because the spacing inside a line was already measured:
// letting the browser collapse runs of spaces would move text away from where
// it was measured to be. The line cannot wrap — it is one line by construction
// — so `pre` costs nothing and removes a decision.
func (f *Frame) emitText(b *strings.Builder, style string) {
	s := f.Style
	fmt.Fprintf(b, `<div style="%s">`, style)

	base := fmt.Sprintf("font-size:%spx", num(s.Size))
	if s.Family != "" {
		base = fmt.Sprintf("font-family:'%s';%s", s.Family, base)
	}
	if s.Letter != 0 {
		base += fmt.Sprintf(";letter-spacing:%spx", num(s.Letter))
	}
	if s.Italic {
		base += ";font-style:italic"
	}

	for i, line := range f.Lines {
		baseline := f.Baselines[i]
		fmt.Fprintf(b,
			`<div style="position:absolute;left:0;top:0;transform:translateY(%spx);white-space:pre;%s">`,
			num(baseline), base)
		for _, piece := range line.Pieces {
			weight := s.Weight
			if piece.Bold {
				weight = Bold
				if s.Weight >= Bold {
					weight = s.Weight
				}
			}
			colour := colourOr(piece.Colour, s.Colour)
			fmt.Fprintf(b, `<span style="font-weight:%d;color:%s">%s</span>`,
				int(weight), colour, html.EscapeString(piece.Text))
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div>`)
}

// The baseline is expressed as a translate on a zero-height box, so the text
// sits exactly where it was measured regardless of the font's own line box —
// which differs between browsers and would otherwise reintroduce the drift this
// engine removes.
