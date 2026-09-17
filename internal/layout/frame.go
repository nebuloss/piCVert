package layout

// Frame is a node once it has been placed: a box, and what is inside it.
//
// This tree is the SINGLE SOURCE both renderings are drawn from. Positions are
// absolute, in CSS pixels, relative to the page — so a painter has nothing left
// to compute, and therefore nothing left to compute differently.
type Frame struct {
	Node  *Node
	Style Style

	X, Y          float64
	Width, Height float64

	// Lines are the already-broken lines of a text frame, with the baseline of
	// each measured from the frame's top.
	Lines     []Line
	Baselines []float64
	// LineHeight is the distance between two baselines, settled here so the
	// painters cannot each arrive at a different one.
	LineHeight float64

	Children []*Frame
}

// ID is what the theme named this block, if anything.
func (f *Frame) ID() string {
	if f.Node == nil {
		return ""
	}
	return f.Node.ID
}

// Bottom is where this frame ends.
func (f *Frame) Bottom() float64 { return f.Y + f.Height }

// Walk visits every frame, parents before children.
func (f *Frame) Walk(visit func(*Frame)) {
	visit(f)
	for _, child := range f.Children {
		child.Walk(visit)
	}
}

// ContentBottom is where a frame's CONTENT ends, which is not where the frame
// does.
//
// A region stretches to the full body height whatever it holds, so its own box
// says nothing about how full it is. Its content ends where its last child
// ends. This is the distinction that made reading a finished PDF back useless:
// every column measured as exactly full.
func (f *Frame) ContentBottom() float64 {
	bottom := f.Y
	for _, child := range f.Children {
		if b := child.Bottom(); b > bottom {
			bottom = b
		}
	}
	return bottom
}

// Overflow reports which named blocks do not hold inside a height.
//
// By NAME, because "it overflows by 14 px" is not something anyone can act on,
// and "shorten Technical Projects" is. A block the theme did not name cannot be
// reported — which is why the conformance kit insists every card has an id.
func (f *Frame) Overflow(usableBottom float64) []string {
	var out []string
	seen := map[string]bool{}
	f.Walk(func(fr *Frame) {
		id := fr.ID()
		if id == "" || seen[id] {
			return
		}
		if fr.Bottom() > usableBottom+0.01 {
			seen[id] = true
			out = append(out, id)
		}
	})
	return out
}

// Fits reports whether everything holds within the page.
func (f *Frame) Fits(usableBottom float64) bool {
	return len(f.Overflow(usableBottom)) == 0
}

// Margins is the room left under each region, by name — what the editor shows
// as "you have this much left".
type Margins map[string]float64
