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
	// Polygon draws a filled regular polygon inscribed in the node's box: a
	// hexagon, a triangle, a diamond.
	//
	// GENERAL RATHER THAN A HEXAGON, because a kind that could only be six
	// sides would be a kind the next theme has to work around — and the cost of
	// the general case is one integer and a loop. It is ornament: it holds no
	// text and takes part in layout only by occupying its box, so a renderer
	// that cannot draw one loses decoration, never an arrangement.
	Polygon
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

// OrRegular is the weight to write down, for a style that never named one.
//
// Zero is not a weight. Written into CSS it is invalid, so the browser drops
// the declaration and picks its own — which was happening on most of the text
// on the page, silently, because a style with no weight of its own and no
// ancestor to inherit one from kept the zero value.
func (w Weight) OrRegular() Weight {
	if w == 0 {
		return Regular
	}
	return w
}

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

// Shadow is a soft drop shadow: an offset, a blur, and a colour.
type Shadow struct {
	X, Y, Blur float64
	Colour     string
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
	// WidthPercent is a width given as a share of the room offered, 0 to 100.
	//
	// The language gauge needs it: its fill is "82% of the bar", and no number
	// of pixels can say that, because the bar's own width depends on the column
	// it lands in. It takes precedence over Width when set.
	WidthPercent float64
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
	// Sides is how many a Polygon has, and Rotate turns it, in degrees.
	//
	// A hexagon standing on a point is `6` and `30` — the orientation everyone
	// means by a hexagon, and not the one the maths gives by default.
	Sides  int
	Rotate float64
	// Opacity fades a node, 0 to 1. Zero means opaque, so a style that says
	// nothing is drawn normally.
	Opacity float64

	// Shadow is ornament: a soft drop shadow under a box.
	//
	// THE ONE PROPERTY A RENDERER MAY IGNORE, and it is here because the design
	// it reproduces already worked that way — the original drew the portrait's
	// glow on screen and left it out of the PDF, which is the correct call. A
	// shadow is ink around a shape, not part of it: it changes no size and no
	// position, so a renderer that cannot draw one loses nothing but the
	// ornament. Every other property in this vocabulary must mean the same
	// thing to both, or the two renderings could differ in layout.
	Shadow *Shadow

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

// LineHeightOr is the line height a style names, or a plain default.
//
// Used only where no font is at hand. The real answer comes from the font
// itself — see LineMetrics.Height — because `normal` is a property of a
// typeface and not a number one picks.
func (s Style) LineHeightOr() float64 {
	if s.LineHeight > 0 {
		return s.LineHeight
	}
	return 1.2
}

// WeightOf is the weight to draw a run in, given whether it is bold.
//
// Bold inside a paragraph is a heavier face of the SAME family, never a
// different block — and a style already at bold or above stays where it is
// rather than trying to go heavier than it shipped.
func (s Style) WeightOf(bold bool) Weight {
	if !bold {
		return s.Weight
	}
	if s.Weight >= Bold {
		return s.Weight
	}
	return Bold
}

// AlignOf is how a child sits across a row: its own choice if it made one, its
// parent's otherwise.
func (s Style) AlignOf(child Style) Align {
	if child.SelfAlign != nil {
		return *child.SelfAlign
	}
	return s.Align
}

// inheritFrom takes the typography an ancestor settled, for the properties CSS
// itself inherits and no others.
func (s *Style) inheritFrom(from Style) {
	if s.Family == "" {
		s.Family = from.Family
	}
	if s.Size == 0 {
		s.Size = from.Size
	}
	if s.Weight == 0 {
		s.Weight = from.Weight
	}
	if s.Colour == "" {
		s.Colour = from.Colour
	}
	if s.LineHeight == 0 {
		s.LineHeight = from.LineHeight
	}
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
