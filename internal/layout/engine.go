package layout

// Engine lays a composed tree out on a page.
//
// It coordinates and owns almost no algorithm: measuring one kind of node
// belongs to that kind's Layouter, and drawing belongs to a Painter. What is
// left here is the part that is true of every layout regardless of what is in
// it — inherit, measure, then place — and that order is the one thing every
// node must agree on.
type Engine struct{ Fonts *Fonts }

func NewEngine(f *Fonts) *Engine { return &Engine{Fonts: f} }

// Layout places a tree inside a page.
//
// Three passes, and each exists because the one before it cannot know what it
// needs:
//
//  1. INHERIT pushes typography down, because a node's own size depends on the
//     font it will be measured in, and that may come from an ancestor.
//  2. MEASURE sizes every node bottom-up, because a parent's size depends on
//     its children's.
//  3. PLACE turns parent-relative positions into absolute ones top-down,
//     because a child's position depends on where its parent ended up.
//
// Folding any two together means guessing one of them.
func (e *Engine) Layout(root *Node, pageWidth, pageHeight float64) *Frame {
	inherit(root, Style{})
	frame := e.Measure(root, Space{Width: pageWidth, Height: pageHeight})
	place(frame, 0, 0)
	return frame
}

// Measure sizes one node, through the strategy for its kind.
func (e *Engine) Measure(n *Node, avail Space) *Frame {
	layouter, ok := layouters[n.Style.Display]
	if !ok {
		// Unreachable while the vocabulary is closed and every kind is
		// registered — which is exactly why it is worth saying out loud rather
		// than returning a zero-sized frame that would surface as a hole in a
		// page, a long way from here.
		panic("layout: no layouter registered for display kind")
	}
	f := layouter.Measure(e, n, avail)
	// An explicit height always wins: a theme that states one is stating a
	// constraint, not a suggestion.
	if n.Style.Height > 0 {
		f.Height = n.Style.Height
	}
	return f
}

// inherit pushes typography down the tree before anything is measured.
//
// CSS inherits font family, size, weight and colour, and this engine must do
// the same — HERE, before measuring, rather than in the painters. A node
// measured in one font and drawn in another breaks lines in places the reader
// never sees, which is the exact fault this package exists to remove.
//
// Only the properties CSS itself inherits: a box's padding or background is its
// own business, and inheriting those would make every child repeat its parent's
// card.
func inherit(n *Node, from Style) {
	n.Style.inheritFrom(from)
	for _, child := range n.Children {
		inherit(child, n.Style)
	}
}

// place turns parent-relative positions into absolute ones.
func place(f *Frame, originX, originY float64) {
	f.X += originX
	f.Y += originY
	x, y := f.ContentOrigin()
	for _, child := range f.Children {
		place(child, x, y)
	}
}

// reflow re-lays a frame whose width or height changed after it was measured —
// what happens to a growing child once its share of the leftover space is
// known.
func (e *Engine) reflow(f *Frame) {
	if f.Node == nil {
		return
	}
	w, h := f.Width, f.Height
	fresh := e.Measure(f.Node, Space{Width: w, Height: h})
	fresh.X, fresh.Y = f.X, f.Y
	if f.Style.Height > 0 || h > fresh.Height {
		fresh.Height = h
	}
	*f = *fresh
}
