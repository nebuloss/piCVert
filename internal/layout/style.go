// Package layout computes where everything on a CV goes, once.
//
// # WHY THIS EXISTS
//
// The engine used to lay the same document out TWICE: the browser did it from
// CSS, and react-pdf did it from its own tree. The two agreed on the design and
// disagreed on the result — measured on a real CV, the same four cards came out
// 833 px tall in the browser and 804 pt (×⁴⁄₃) in the PDF, a drift of 29 px
// spread over four blocks, with the sign changing from one to the next. Not a
// scale factor, not a padding mistake: the two engines broke the same
// paragraphs into a different number of LINES.
//
// That gap is where a CV gets lost. The page is 1123 px with `overflow:hidden`,
// and a full CV fits it by less than one line; so the editor could report
// “fits” from one engine while the other silently cut the last section off the
// bottom. The measurement said yes and the page said no.
//
// So the layout is computed HERE, once, and both renderings are emitted from
// the result. The HTML emitter places lines that this package has already
// broken, and the PDF emitter draws the same ones. They cannot disagree,
// because there is nothing left for them to decide.
//
// # WHAT A STYLE MAY SAY
//
// The vocabulary below is CLOSED, for the reason the field vocabulary is closed
// (see internal/fields): each property has exactly one layout meaning, one CSS
// declaration and one PDF operator. A theme that could invent a property would
// be a theme this engine cannot lay out and one of the two emitters cannot
// draw — which is precisely the class of divergence this package exists to
// remove.
//
// Everything is in CSS pixels at 96 dpi, because that is what the themes are
// written in. The PDF emitter converts to points at the very end (×0.75), in
// one place.
package layout

// Display is how a node arranges what is inside it.
type Display uint8

const (
	// Block stacks children vertically. The default, and what a card is.
	Block Display = iota
	// Row places children horizontally.
	Row
	// RowWrap places children horizontally and wraps — the skill chips.
	RowWrap
	// Text is a leaf holding words to be broken into lines.
	Text
	// Image is a leaf holding a picture.
	Image
	// Ellipse draws a filled ellipse: the timeline dots and the bullets.
	//
	// A NODE, not a pseudo-element. The CSS wrote these with `::before` and the
	// PDF drew them as views, which is two descriptions of one dot; making it a
	// node means the layout engine knows it is there, and the two emitters draw
	// the same one.
	Ellipse
)

// Align is cross-axis placement.
type Align uint8

const (
	AlignStart Align = iota
	AlignCenter
	AlignEnd
	AlignBaseline
	AlignStretch
)

// Justify is main-axis distribution.
type Justify uint8

const (
	JustifyStart Justify = iota
	JustifyBetween
)

// Edges is a box's four sides. Named rather than an array so a caller cannot
// silently get the order wrong.
type Edges struct{ Top, Right, Bottom, Left float64 }

// XY is uniform padding, the common case.
func XY(v, h float64) Edges { return Edges{Top: v, Right: h, Bottom: v, Left: h} }

// All is the same on every side.
func All(v float64) Edges { return Edges{v, v, v, v} }

// Weight is a font weight, as CSS spells it.
type Weight int

const (
	Regular Weight = 400
	Medium  Weight = 500
	Bold    Weight = 700
)

// Fill is what paints a surface: a flat colour, or a gradient.
type Fill struct {
	// Colour is `#rrggbb`. Empty means nothing is painted.
	Colour string
	// Gradient, when set, replaces Colour. Both emitters can draw it: CSS as
	// `linear-gradient`, PDF as an axial shading.
	Gradient *Gradient
}

// Gradient is a linear gradient across a box.
type Gradient struct {
	// Angle in degrees, CSS convention: 0 points up, 135 down-right.
	Angle float64
	Stops []Stop
}

// Stop is one colour of a gradient, at a fraction of the way along it.
type Stop struct {
	At     float64 // 0..1
	Colour string
}

// Style is everything a theme may say about a node.
//
// One flat struct rather than a hierarchy: every field has a defined meaning
// for every Display, and a value that does not apply is ignored rather than
// misread. That is the same choice fields.Field makes, for the same reason.
type Style struct {
	Display Display

	// --- box ---------------------------------------------------------------

	// Width and Height in pixels. Zero means "as needed".
	Width, Height float64
	// Grow shares out leftover space along the main axis, like `flex-grow`.
	Grow float64
	// Gap between children, along the main axis. Also the line gap when
	// wrapping.
	Gap float64
	// CrossGap is the gap between wrapped lines. Zero falls back to Gap.
	CrossGap float64
	Padding  Edges
	Margin   Edges

	Align   Align
	Justify Justify
	// SelfAlign overrides the parent's Align for this node alone.
	SelfAlign *Align

	// --- paint -------------------------------------------------------------

	Background Fill
	// Radius rounds the corners. A single value: no theme here needs four, and
	// four would be four chances to disagree.
	Radius float64
	// Border is a uniform outline. Width zero means none.
	Border struct {
		Width  float64
		Colour string
	}
	// Clip hides what overflows this node — what makes `.page` cut rather than
	// grow, which is the constraint the whole engine exists to enforce.
	Clip bool

	// --- text --------------------------------------------------------------

	Family     string
	Size       float64
	Weight     Weight
	Italic     bool
	Colour     string
	LineHeight float64 // multiple of Size; zero means 1.2
	Letter     float64 // letter-spacing, in pixels
	Uppercase  bool
}

// Span is a run of text in one style. A paragraph is a list of them, which is
// how `<b>` inside a sentence stays part of the same wrapped text instead of
// becoming its own block.
type Span struct {
	Text string
	// Bold and Colour override the node's own style for this run only.
	Bold   bool
	Colour string
}

// Node is one thing on the page, before it has been placed.
type Node struct {
	Style Style
	// ID is what the theme chose to name. Overflow is reported by these names,
	// so a block without one cannot be pointed at — the conformance kit checks
	// that every card has one.
	ID string
	// Spans hold the content of a Text node.
	Spans []Span
	// Src is the data URI of an Image node.
	Src string
	// Alt is its description.
	Alt      string
	Children []*Node
}

// Box adds a node with the given style and children.
func Box(s Style, children ...*Node) *Node {
	return &Node{Style: s, Children: children}
}

// Para builds a text node from plain text.
func Para(s Style, text string) *Node {
	s.Display = Text
	return &Node{Style: s, Spans: []Span{{Text: text}}}
}

// Rich builds a text node from spans.
func Rich(s Style, spans ...Span) *Node {
	s.Display = Text
	return &Node{Style: s, Spans: spans}
}

// Named tags a node so overflow can report it.
func (n *Node) Named(id string) *Node {
	n.ID = id
	return n
}
