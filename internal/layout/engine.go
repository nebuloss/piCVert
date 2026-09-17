package layout

import "math"

// Frame is a node once it has been placed: a box, and what is inside it.
//
// This tree is the SINGLE SOURCE both renderings are emitted from. Positions
// are absolute, in CSS pixels, relative to the page — so an emitter has nothing
// left to compute and therefore nothing left to get differently.
type Frame struct {
	Node  *Node
	Style Style

	X, Y          float64
	Width, Height float64

	// Lines are the already-broken lines of a Text frame, with the baseline
	// offset of each from the top of the frame.
	Lines     []Line
	Baselines []float64

	Children []*Frame
}

// ID is what the theme named this block, if anything.
func (f *Frame) ID() string {
	if f.Node == nil {
		return ""
	}
	return f.Node.ID
}

// Engine lays documents out.
type Engine struct{ Fonts *Fonts }

func NewEngine(f *Fonts) *Engine { return &Engine{Fonts: f} }

const defaultLineHeight = 1.2

func (s Style) lineHeight() float64 {
	if s.LineHeight > 0 {
		return s.LineHeight
	}
	return defaultLineHeight
}

// Layout places a tree inside a page of the given size.
func (e *Engine) Layout(root *Node, pageWidth, pageHeight float64) *Frame {
	inherit(root, Style{})
	frame := e.measure(root, pageWidth, pageHeight)
	e.place(frame, 0, 0)
	return frame
}

// inherit pushes typography down the tree before anything is measured.
//
// CSS inherits font family, size, weight and colour; this engine must do the
// same, and must do it HERE rather than in the emitters. A node measured in one
// font and drawn in another breaks lines in places the reader never sees — the
// exact fault this package exists to remove. Doing it once, up front, means the
// measurement and both drawings read the same values.
//
// Only the properties CSS itself inherits: a box's padding or background is its
// own business, and inheriting those would make every child repeat its parent's
// card.
func inherit(n *Node, from Style) {
	if n.Style.Family == "" {
		n.Style.Family = from.Family
	}
	if n.Style.Size == 0 {
		n.Style.Size = from.Size
	}
	if n.Style.Weight == 0 {
		n.Style.Weight = from.Weight
	}
	if n.Style.Colour == "" {
		n.Style.Colour = from.Colour
	}
	if n.Style.LineHeight == 0 {
		n.Style.LineHeight = from.LineHeight
	}
	for _, child := range n.Children {
		inherit(child, n.Style)
	}
}

// measure computes a node's size, given the space it has been offered.
//
// Two passes — measure then place — rather than one, because a child's position
// depends on its siblings' sizes, and a parent's size depends on its children's.
// Doing both at once means guessing one of them.
func (e *Engine) measure(n *Node, availW, availH float64) *Frame {
	s := n.Style
	f := &Frame{Node: n, Style: s}

	// The content box: what is left after this node's own padding and border.
	inset := s.Padding
	if s.Border.Width > 0 {
		inset.Top += s.Border.Width
		inset.Right += s.Border.Width
		inset.Bottom += s.Border.Width
		inset.Left += s.Border.Width
	}

	outerW := s.Width
	if outerW == 0 {
		outerW = availW - s.Margin.Left - s.Margin.Right
	}
	innerW := math.Max(0, outerW-inset.Left-inset.Right)

	switch s.Display {
	case Text:
		f.Lines = breakText(n.Spans, s, innerW, e.Fonts)
		m := e.Fonts.Metrics(s.Family, s.Size, s.Weight, s.Italic)
		lh := s.Size * s.lineHeight()
		// The baseline sits where the extra leading is split evenly above and
		// below the text, which is what CSS does with a line box and therefore
		// what the HTML emitter has to be able to reproduce.
		half := (lh - (m.Ascent + m.Descent)) / 2
		for i := range f.Lines {
			f.Baselines = append(f.Baselines, float64(i)*lh+half+m.Ascent)
		}
		f.Width = outerW
		f.Height = float64(len(f.Lines))*lh + inset.Top + inset.Bottom
		if len(f.Lines) == 0 {
			f.Height = 0
		}

	case Image, Ellipse:
		f.Width, f.Height = outerW, s.Height

	case Row, RowWrap:
		e.measureRow(f, n, innerW, availH, inset, outerW)

	default: // Block
		e.measureBlock(f, n, innerW, availH, inset, outerW)
	}

	if s.Height > 0 {
		f.Height = s.Height
	}
	return f
}

// measureBlock stacks children vertically.
func (e *Engine) measureBlock(f *Frame, n *Node, innerW, availH float64, inset Edges, outerW float64) {
	s := n.Style
	y := 0.0
	maxChild := 0.0
	var grows []*Frame

	for i, child := range n.Children {
		cf := e.measure(child, innerW, availH)
		f.Children = append(f.Children, cf)
		if i > 0 {
			y += s.Gap
		}
		y += child.Style.Margin.Top
		cf.Y = y
		y += cf.Height + child.Style.Margin.Bottom
		maxChild = math.Max(maxChild, cf.Width+child.Style.Margin.Left+child.Style.Margin.Right)
		if child.Style.Grow > 0 {
			grows = append(grows, cf)
		}
	}

	f.Width = outerW
	if s.Width == 0 && innerW == 0 {
		f.Width = maxChild + inset.Left + inset.Right
	}
	f.Height = y + inset.Top + inset.Bottom

	// A growing child takes the leftover height. This is what makes the two
	// columns run the full body height, and what made reading the finished PDF
	// back useless: both columns measure as exactly full whatever they hold.
	if len(grows) > 0 && s.Height > 0 {
		spare := s.Height - inset.Top - inset.Bottom - y
		if spare > 0 {
			share := spare / float64(len(grows))
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
}

// measureRow places children side by side, wrapping when asked.
func (e *Engine) measureRow(f *Frame, n *Node, innerW, availH float64, inset Edges, outerW float64) {
	s := n.Style
	type placed struct {
		frame *Frame
		child *Node
	}
	var rows [][]placed
	var cur []placed
	curW := 0.0

	for _, child := range n.Children {
		// Measured against what is LEFT, not the whole row: a block in a row
		// takes the width of its content, as flexbox does, and only a growing
		// child claims the remainder. Measuring every child at full width made
		// each one as wide as the row and pushed its siblings off the page.
		room := innerW - curW
		if len(cur) > 0 {
			room -= s.Gap
		}
		if room < 0 {
			room = 0
		}
		cf := e.measure(child, room, availH)
		if child.Style.Width == 0 && child.Style.Grow == 0 {
			cf.Width = natural(cf)
		}
		w := cf.Width + child.Style.Margin.Left + child.Style.Margin.Right
		if s.Display == RowWrap && len(cur) > 0 && curW+s.Gap+w > innerW+0.01 {
			rows = append(rows, cur)
			cur, curW = nil, 0
		}
		if len(cur) > 0 {
			curW += s.Gap
		}
		curW += w
		cur = append(cur, placed{cf, child})
	}
	if len(cur) > 0 {
		rows = append(rows, cur)
	}

	crossGap := s.CrossGap
	if crossGap == 0 {
		crossGap = s.Gap
	}

	y := 0.0
	widest := 0.0
	for ri, row := range rows {
		if ri > 0 {
			y += crossGap
		}
		// Share the leftover width among growing children.
		used, grow := 0.0, 0.0
		for i, p := range row {
			if i > 0 {
				used += s.Gap
			}
			used += p.frame.Width + p.child.Style.Margin.Left + p.child.Style.Margin.Right
			grow += p.child.Style.Grow
		}
		spare := innerW - used
		if grow > 0 && spare > 0 {
			for _, p := range row {
				if g := p.child.Style.Grow; g > 0 {
					p.frame.Width += spare * (g / grow)
					e.reflow(p.frame)
				}
			}
			used = innerW
		}

		// Tallest child sets the row's height.
		rowH := 0.0
		for _, p := range row {
			rowH = math.Max(rowH, p.frame.Height+p.child.Style.Margin.Top+p.child.Style.Margin.Bottom)
		}

		x := 0.0
		gap := s.Gap
		if s.Justify == JustifyBetween && len(row) > 1 && spare > 0 && grow == 0 {
			gap = s.Gap + spare/float64(len(row)-1)
		}
		for i, p := range row {
			if i > 0 {
				x += gap
			}
			x += p.child.Style.Margin.Left
			p.frame.X = x
			align := s.Align
			if p.child.Style.SelfAlign != nil {
				align = *p.child.Style.SelfAlign
			}
			switch align {
			case AlignCenter:
				p.frame.Y = y + (rowH-p.frame.Height)/2
			case AlignEnd:
				p.frame.Y = y + rowH - p.frame.Height
			case AlignStretch:
				p.frame.Y = y
				if p.child.Style.Height == 0 {
					p.frame.Height = rowH
					e.reflow(p.frame)
				}
			default:
				p.frame.Y = y + p.child.Style.Margin.Top
			}
			x += p.frame.Width + p.child.Style.Margin.Right
			f.Children = append(f.Children, p.frame)
		}
		widest = math.Max(widest, x)
		y += rowH
	}

	f.Width = outerW
	if s.Width == 0 && innerW == 0 {
		f.Width = widest + inset.Left + inset.Right
	}
	f.Height = y + inset.Top + inset.Bottom
}

// natural is how wide a frame's content actually is, as opposed to the room it
// was offered. A text frame is as wide as its widest line; a box is as wide as
// its widest child.
func natural(f *Frame) float64 {
	inset := f.Style.Padding.Left + f.Style.Padding.Right + 2*f.Style.Border.Width
	switch f.Style.Display {
	case Text:
		widest := 0.0
		for _, line := range f.Lines {
			widest = math.Max(widest, line.Width)
		}
		return math.Min(f.Width, widest+inset)
	case Image, Ellipse:
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

// reflow re-lays a frame whose width or height changed after it was measured.
func (e *Engine) reflow(f *Frame) {
	if f.Node == nil {
		return
	}
	w, h := f.Width, f.Height
	fresh := e.measure(f.Node, w, h)
	fresh.X, fresh.Y = f.X, f.Y
	if f.Style.Height > 0 || h > fresh.Height {
		fresh.Height = h
	}
	*f = *fresh
}

// place turns positions relative to a parent into absolute ones.
func (e *Engine) place(f *Frame, originX, originY float64) {
	f.X += originX
	f.Y += originY
	inset := f.Style.Padding
	if f.Style.Border.Width > 0 {
		inset.Top += f.Style.Border.Width
		inset.Left += f.Style.Border.Width
	}
	for _, child := range f.Children {
		e.place(child, f.X+inset.Left, f.Y+inset.Top)
	}
}

// Walk visits every frame, parents before children.
func (f *Frame) Walk(visit func(*Frame)) {
	visit(f)
	for _, child := range f.Children {
		child.Walk(visit)
	}
}

// Overflow reports which named blocks do not hold inside a height.
//
// By NAME, because “it overflows by 14 px” is not something anyone can act on,
// and “shorten Technical Projects” is. A block the theme did not name cannot be
// reported, which is why the conformance kit insists every card has an id.
func (f *Frame) Overflow(usableBottom float64) []string {
	var out []string
	seen := map[string]bool{}
	f.Walk(func(fr *Frame) {
		id := fr.ID()
		if id == "" || seen[id] {
			return
		}
		if fr.Y+fr.Height > usableBottom+0.01 {
			seen[id] = true
			out = append(out, id)
		}
	})
	return out
}

// Margins is the room left under each region, by name.
type Margins map[string]float64

// Fits reports whether everything holds within the page.
func (f *Frame) Fits(usableBottom float64) bool {
	return len(f.Overflow(usableBottom)) == 0
}
