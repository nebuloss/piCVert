package layout

import "math"

// Space is the room a node is offered by its parent.
type Space struct{ Width, Height float64 }

// Layouter measures one kind of node.
//
// # WHY AN INTERFACE AND NOT A SWITCH
//
// Measuring used to be one function with a `switch` on the display kind, and
// the switch had to be repeated — once to measure, once per emitter to draw.
// Adding a kind meant finding every switch, and the compiler had nothing to say
// about the one you missed: a node whose kind no case matched simply came out
// with no size, which looks like a layout bug a long way from its cause.
//
// As an interface, a kind is a type. Adding one is writing that type and
// registering it; the registry below is the only place that maps the theme's
// spelling of a kind to the code for it, so a kind that exists in the
// vocabulary and nowhere else fails at load with its own name in the message.
//
// This is Strategy, and it earns its place for a specific reason: `Row` and
// `RowWrap` are the SAME algorithm with one flag, and sharing a type says so
// far better than two switch cases that must be kept in step by hand.
type Layouter interface {
	// Measure sizes a node and positions its children relative to it.
	// Absolute positions come later, in one pass over the finished tree.
	Measure(e *Engine, n *Node, avail Space) *Frame
}

// layouters maps a display kind to the code that lays it out.
//
// A map rather than a switch so that registration is data: a kind with no
// entry is a startup error naming itself, instead of a node that quietly
// measures as nothing.
var layouters = map[Display]Layouter{
	Block:   blockLayout{},
	Row:     rowLayout{},
	RowWrap: rowLayout{wrap: true},
	Text:    textLayout{},
	Image:   leafLayout{},
	Ellipse: leafLayout{},
}

// box is the geometry every layouter needs: what the node's own padding and
// border leave for its content.
type box struct {
	// outer is the node's full width, including its padding and border.
	outer float64
	// inner is what is left for children.
	inner float64
	// inset is the padding plus border, on all four sides.
	inset Edges
}

func boxOf(s Style, avail Space) box {
	inset := s.Padding
	if w := s.Border.Width; w > 0 {
		inset.Top += w
		inset.Right += w
		inset.Bottom += w
		inset.Left += w
	}
	outer := s.Width
	if outer == 0 {
		outer = avail.Width - s.Margin.Left - s.Margin.Right
	}
	return box{
		outer: outer,
		inner: math.Max(0, outer-inset.Left-inset.Right),
		inset: inset,
	}
}

// --- leaves -------------------------------------------------------------------

// leafLayout sizes a node that has no content to measure: an image, a dot. Its
// size is whatever the theme asked for.
type leafLayout struct{}

func (leafLayout) Measure(e *Engine, n *Node, avail Space) *Frame {
	b := boxOf(n.Style, avail)
	return &Frame{Node: n, Style: n.Style, Width: b.outer, Height: n.Style.Height}
}

// textLayout breaks a paragraph into lines and stacks them.
type textLayout struct{}

func (textLayout) Measure(e *Engine, n *Node, avail Space) *Frame {
	s := n.Style
	b := boxOf(s, avail)
	f := &Frame{Node: n, Style: s, Width: b.outer}

	f.Lines = breakText(n.Spans, s, b.inner, e.Fonts)
	if len(f.Lines) == 0 {
		return f
	}

	// The box shrinks to the text it ended up holding, unless the theme fixed a
	// width or asked it to grow.
	//
	// It was left at the full width offered, which draws correctly — the box is
	// transparent — and is wrong in two ways that only show later. A paragraph
	// of one short line claimed the whole column, so it overlapped whatever sat
	// beside it: harmless to look at, but it breaks selecting the text, and it
	// makes an overlap check unable to tell a real collision from this. And a
	// box wider than its content reports a width nothing in it occupies, which
	// is the sort of figure a later pass takes at its word.
	if s.Width == 0 && s.Grow == 0 {
		widest := 0.0
		for _, line := range f.Lines {
			widest = math.Max(widest, line.Width)
		}
		f.Width = math.Min(b.outer, widest+b.inset.Left+b.inset.Right)
	}

	m := e.Fonts.Metrics(s.Family, s.Size, s.Weight, s.Italic)
	// A theme that names a line height gets it; one that says nothing gets what
	// the FONT asks for, which is what a browser means by `normal`.
	lh := m.Height
	if s.LineHeight > 0 {
		lh = s.Size * s.LineHeight
	}
	f.LineHeight = lh
	// The baseline sits where the extra leading is split evenly above and below
	// the text, which is what CSS does with a line box — and therefore what the
	// HTML painter has to be able to reproduce without knowing any of this.
	half := (lh - (m.Ascent + m.Descent)) / 2
	for i := range f.Lines {
		f.Baselines = append(f.Baselines, float64(i)*lh+half+m.Ascent)
	}
	f.Height = float64(len(f.Lines))*lh + b.inset.Top + b.inset.Bottom
	return f
}

// --- containers ---------------------------------------------------------------

// blockLayout stacks children vertically.
type blockLayout struct{}

func (blockLayout) Measure(e *Engine, n *Node, avail Space) *Frame {
	s := n.Style
	b := boxOf(s, avail)
	f := &Frame{Node: n, Style: s}

	y := 0.0
	widest := 0.0
	var growing []*Frame

	for i, child := range n.Children {
		cf := e.Measure(child, Space{Width: b.inner, Height: avail.Height})
		f.Children = append(f.Children, cf)
		if i > 0 {
			y += s.Gap
		}
		y += child.Style.Margin.Top
		cf.Y = y
		y += cf.Height + child.Style.Margin.Bottom
		widest = math.Max(widest, cf.Width+child.Style.Margin.Left+child.Style.Margin.Right)
		if child.Style.Grow > 0 {
			growing = append(growing, cf)
		}
	}

	f.Width = b.outer
	if s.Width == 0 && b.inner == 0 {
		f.Width = widest + b.inset.Left + b.inset.Right
	}
	f.Height = y + b.inset.Top + b.inset.Bottom

	// A growing child takes the leftover height. This is what makes the two
	// columns run the full body height — and what made reading a finished PDF
	// back useless, since both columns then measure as exactly full whatever
	// they hold.
	if len(growing) > 0 && s.Height > 0 {
		spare := s.Height - b.inset.Top - b.inset.Bottom - y
		if spare > 0 {
			share := spare / float64(len(growing))
			shift := 0.0
			for _, cf := range f.Children {
				cf.Y += shift
				if cf.Style.Grow > 0 {
					cf.Height += share
					shift += share
					e.reflow(cf)
				}
			}
		}
	}
	return f
}

// rowLayout places children side by side, wrapping when asked.
//
// One type for both `row` and `row-wrap`, because they are one algorithm: the
// flag decides whether a child that does not fit starts a new line or simply
// overflows. Two switch cases would have had to be kept in step by hand.
type rowLayout struct{ wrap bool }

// seat is a child with the frame it was measured into.
type seat struct {
	frame *Frame
	node  *Node
}

func (seat) outerWidth(s seat) float64 {
	return s.frame.Width + s.node.Style.Margin.Left + s.node.Style.Margin.Right
}

func (r rowLayout) Measure(e *Engine, n *Node, avail Space) *Frame {
	s := n.Style
	b := boxOf(s, avail)
	f := &Frame{Node: n, Style: s}

	lines := r.fill(e, n, b, avail)

	crossGap := s.CrossGap
	if crossGap == 0 {
		crossGap = s.Gap
	}

	y, widest := 0.0, 0.0
	for i, line := range lines {
		if i > 0 {
			y += crossGap
		}
		used := r.share(e, line, s, b.inner)
		height := r.tallest(line)
		r.place(f, line, s, y, height, b.inner-used)
		widest = math.Max(widest, used)
		y += height
	}

	f.Width = b.outer
	if s.Width == 0 && b.inner == 0 {
		f.Width = widest + b.inset.Left + b.inset.Right
	}
	f.Height = y + b.inset.Top + b.inset.Bottom
	return f
}

// fill measures the children and cuts them into lines.
//
// WHY EACH CHILD IS MEASURED AGAINST THE WHOLE ROW, not against what is left of
// it. Measuring against the remaining room seems right — it is how one would
// fill a shelf — and it is wrong for a reason that only shows on a dense CV.
//
// A child offered almost no room wraps to almost nothing, so its frame comes
// back a few pixels wide, or zero. The row then seats the NEXT child a few
// pixels along, while the first one's text draws at the width it actually
// needs: two runs of text on top of each other. It appeared as skill chips
// overlapping their neighbours, and only ever near the end of a line, which is
// what made it look intermittent.
//
// A child's intrinsic width does not depend on where it happens to land. So it
// is measured once against the full row, and the wrap decision is made
// afterwards, from a width that is true. That is also what flexbox does: it
// measures max-content first, then breaks.
func (r rowLayout) fill(e *Engine, n *Node, b box, avail Space) [][]seat {
	s := n.Style
	var lines [][]seat
	var cur []seat
	curW := 0.0

	for _, child := range n.Children {
		cf := e.Measure(child, Space{Width: b.inner, Height: avail.Height})
		// A child that neither fixes its width nor grows takes the width of its
		// content, as flexbox does; only a growing child claims the remainder.
		if child.Style.Width == 0 && child.Style.Grow == 0 {
			// NOT re-measured. A text frame already shrank to the lines it
			// broke, and its width is the OUTER one — content plus padding.
			// Handing that back as the space AVAILABLE gave the second pass
			// sixteen pixels less room than the first, so a chip that fitted on
			// one line broke onto two, by exactly its own horizontal padding.
			// It showed on the short chips, where sixteen pixels is a quarter
			// of the chip.
			//
			// Only containers need asking, and only because their width is a
			// fact about their children rather than about themselves.
			cf.Width = natural(cf)
		} else if child.Style.Grow > 0 {
			// A growing child contributes NOTHING to the width already taken:
			// what it gets is whatever the others leave, worked out in share()
			// below. Counting the full row it was measured into would make the
			// line look over-full, leave no spare to hand out, and let the
			// child keep the whole row — which is how the right-hand column
			// came out 734 px wide inside a 734 px body and broke its text far
			// past the page edge.
			cf.Width = 0
		}
		w := cf.Width + child.Style.Margin.Left + child.Style.Margin.Right

		if r.wrap && len(cur) > 0 && curW+s.Gap+w > b.inner+0.01 {
			lines = append(lines, cur)
			cur, curW = nil, 0
		}
		if len(cur) > 0 {
			curW += s.Gap
		}
		curW += w
		cur = append(cur, seat{cf, child})
	}
	if len(cur) > 0 {
		lines = append(lines, cur)
	}
	return lines
}

// share hands the leftover width to the growing children, and reports the width
// the line ends up using.
func (rowLayout) share(e *Engine, line []seat, s Style, inner float64) float64 {
	used, grow := 0.0, 0.0
	for i, st := range line {
		if i > 0 {
			used += s.Gap
		}
		used += seat{}.outerWidth(st)
		grow += st.node.Style.Grow
	}
	spare := inner - used
	if grow <= 0 || spare <= 0 {
		return used
	}
	for _, st := range line {
		if g := st.node.Style.Grow; g > 0 {
			st.frame.Width += spare * (g / grow)
			// Re-laid at the width it actually got. Its contents were measured
			// against the whole row, so without this its paragraphs keep the
			// line breaks of a column far wider than the one it ended up in.
			e.reflow(st.frame)
		}
	}
	return inner
}

func (rowLayout) tallest(line []seat) float64 {
	h := 0.0
	for _, st := range line {
		h = math.Max(h, st.frame.Height+st.node.Style.Margin.Top+st.node.Style.Margin.Bottom)
	}
	return h
}

// place positions one line's children along it.
func (rowLayout) place(f *Frame, line []seat, s Style, top, height, spare float64) {
	gap := s.Gap
	if s.Justify == JustifyBetween && len(line) > 1 && spare > 0 {
		gap += spare / float64(len(line)-1)
	}
	x := 0.0
	for i, st := range line {
		if i > 0 {
			x += gap
		}
		x += st.node.Style.Margin.Left
		st.frame.X = x
		st.frame.Y = top + s.AlignOf(st.node.Style).offset(
			height, st.frame.Height, st.node.Style.Margin.Top)
		x += st.frame.Width + st.node.Style.Margin.Right
		f.Children = append(f.Children, st.frame)
	}
}

// offset is where a child sits across the line, given how tall the line is.
func (a Align) offset(line, child, marginTop float64) float64 {
	switch a {
	case AlignCenter:
		return (line - child) / 2
	case AlignEnd:
		return line - child
	case AlignStretch:
		return 0
	default:
		return marginTop
	}
}

// natural is how wide a frame's content actually is, as opposed to the room it
// was offered.
//
// Text and leaves already know: a text frame shrank to its lines when it was
// measured, and an image is whatever size the theme gave it. Only a container
// has to be asked, because its width is a fact about its children.
func natural(f *Frame) float64 {
	inset := f.Style.Padding.Left + f.Style.Padding.Right + 2*f.Style.Border.Width
	switch f.Style.Display {
	case Text, Image, Ellipse:
		return f.Width
	}
	widest := 0.0
	for _, child := range f.Children {
		widest = math.Max(widest, child.X+child.Width+child.Style.Margin.Right)
	}
	if widest == 0 {
		return f.Width
	}
	return math.Min(f.Width, widest+inset)
}
