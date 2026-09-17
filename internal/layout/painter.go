package layout

// Painter draws a laid-out tree. One implementation per output format.
//
// # WHY THIS INTERFACE EXISTS
//
// The HTML emitter used to be a method on Frame. That put every output format
// inside the type that represents the result, so adding the PDF emitter would
// have meant a second method, then a third for anyone wanting SVG — and Frame,
// whose one job is to say where things are, would slowly have become the place
// all rendering lives.
//
// Worse, it is the wrong dependency. A frame is a fact about geometry; HTML is
// an opinion about how to write that fact down. The fact should not know about
// the opinion.
//
// So the traversal lives here, once, and each format implements this. Adding
// one is writing a type, not editing this package — which matters because the
// traversal is where the subtle work is (origins, nesting, painting order), and
// every emitter that re-derived it would be a chance to derive it differently.
//
// This is the Visitor pattern, in the form Go makes natural: an interface with
// one method per node shape, rather than a double dispatch through an Accept
// method that would buy nothing here.
type Painter interface {
	// Box paints a container. `in` paints its children, and the implementation
	// decides WHERE to call it: before, after, or nested inside whatever markup
	// it opens. A tree-structured format calls it between its open and close
	// tags; a flat one calls it last, after painting itself.
	Box(f *Frame, in func())
	// Text paints a frame whose lines have already been broken. The
	// implementation places them; it never decides where they break.
	Text(f *Frame)
	// Image paints a picture or an inline glyph.
	Image(f *Frame)
	// Shape paints a polygon: ornament with no content.
	Shape(f *Frame)
}

// Paint walks the tree, handing each frame to the painter.
//
// Parents before children, in document order, which is both the painting order
// (a card is drawn before what sits on it) and the reading order (a screen
// reader meets the text in the order it was written). Those two coinciding is
// not luck — it is why the composition is built in reading order in the first
// place.
func (f *Frame) Paint(p Painter) {
	switch f.Style.Display {
	case Text:
		p.Text(f)
	case Image:
		p.Image(f)
	case Polygon:
		p.Shape(f)
	default:
		p.Box(f, func() {
			for _, child := range f.Children {
				child.Paint(p)
			}
		})
	}
}

// ContentOrigin is where this frame's children are measured from: its own
// corner, moved in by its padding and border.
//
// Every painter needs it and none should compute it: an emitter that derived
// the inset differently would place an entire subtree a few pixels out, which
// is invisible until it is not.
func (f *Frame) ContentOrigin() (x, y float64) {
	inset := f.Style.Border.Width
	return f.X + f.Style.Padding.Left + inset, f.Y + f.Style.Padding.Top + inset
}
