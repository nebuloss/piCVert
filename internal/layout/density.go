package layout

// Density is how tightly a page is set.
//
// # WHY THE ENGINE TIGHTENS RATHER THAN THE AUTHOR
//
// A CV is one page, and the usual answer to twenty pixels of overflow is to ask
// whoever wrote it to cut a sentence. That is the wrong party: the sentence is
// the thing with value, and the twenty pixels are an accident of how much slack
// the design happened to leave. A designer setting this by hand would close the
// gaps a little and never think about it again.
//
// So the engine does that first, and only reports overflow when tightening can
// no longer honestly absorb it.
//
// # WHAT IS ALLOWED TO GIVE, AND IN WHAT ORDER
//
// Spacing before type, always. The gaps between cards, the margins between
// entries and the leading inside a paragraph carry several percent of give that
// nobody can see — a reader has no reference for what the gap "should" have
// been. Font size has no such slack: a CV set at 92% looks like a CV that was
// shrunk, and everyone recognises it.
//
// Fixed sizes are never touched. The portrait, the section dots, the gauge
// bars and the corner radii are the design's proportions, and scaling them is
// what makes a tightened page look squashed rather than merely close-set.
type Density struct {
	// Spacing scales gaps, margins and the vertical part of padding.
	Spacing float64
	// Text scales font size, and with it every line height derived from it.
	Text float64
}

// Natural is the design exactly as drawn.
var Natural = Density{Spacing: 1, Text: 1}

// Tightest is as close-set as this engine is willing to go.
//
// The floor exists because a fit that always succeeds is a fit that means
// nothing. Past this point the honest answer is that the CV is too long, and
// the engine says so and names what to shorten — which is a thing the author
// can act on, where a page of 7-point type is not.
var Tightest = Density{Spacing: 0.62, Text: 0.90}

// ladder is the series tried, loosest first.
//
// Spacing is spent entirely before type is touched at all, and the last two
// rungs are deliberately small: by then the page is visibly tight, and the
// difference between fitting and not is worth a percent of type, but not five.
var ladder = []Density{
	{1.00, 1.00},
	{0.94, 1.00},
	{0.88, 1.00},
	{0.80, 1.00},
	{0.72, 1.00},
	{0.66, 1.00},
	{0.62, 1.00},
	{0.62, 0.97},
	{0.62, 0.94},
	{0.62, 0.92},
	{0.62, 0.90},
}

// Fitted is the result of laying a page out as loosely as it will go.
type Fitted struct {
	Frame *Frame
	// Density actually used.
	Density Density
	// Tightened is true when the design had to be closed up to fit.
	Tightened bool
	// Fits is false when even the tightest setting overflows — and then the
	// frame is the tightest attempt, so the caller can still show the page and
	// name what runs over.
	Fits bool
}

// LayoutFitted lays the page out at the loosest density that holds.
//
// Tried rung by rung rather than solved for, because the relationship between
// spacing and height is not continuous: closing a gap by a pixel can remove a
// line from a paragraph three cards down, or nothing at all. A ladder of
// settings that were each chosen to look right is worth more than a binary
// search through settings that were not.
func (e *Engine) LayoutFitted(build func() *Node, pageWidth, pageHeight, usable float64) Fitted {
	var last *Frame
	for i, d := range ladder {
		root := build()
		applyDensity(root, d)
		frame := e.Layout(root, pageWidth, pageHeight)
		// Two conditions, not one. Closing the spacing far enough will always
		// make a page "fit", by running its lines into one another — and a page
		// that fits by collapsing is worse than one that honestly reports being
		// too long, because it looks finished.
		if frame.Fits(usable) && !frame.Collides() {
			return Fitted{Frame: frame, Density: d, Tightened: i > 0, Fits: true}
		}
		if frame.Fits(usable) {
			// It held, but only by touching. Keep it as the fallback — showing
			// a crowded page beats showing nothing — and go on looking for a
			// setting that does not.
			last = frame
			continue
		}
		last = frame
	}
	return Fitted{Frame: last, Density: ladder[len(ladder)-1], Tightened: true, Fits: false}
}

// applyDensity closes up a tree before it is measured.
//
// Applied to the NODES, before any measuring, so that every consequence follows
// on its own: a tighter leading may remove a line, which may shorten a card,
// which may let the next one rise. Scaling the finished frame instead would
// move boxes without re-breaking the text inside them, and the page would be
// wrong in a way that is hard to see and impossible to correct.
func applyDensity(n *Node, d Density) {
	if d == Natural {
		return
	}
	s := &n.Style

	s.Gap *= d.Spacing
	s.CrossGap *= d.Spacing
	// Vertical only. Horizontal padding is what keeps a chip's text off its own
	// rounded edge, and a chip whose sides close in reads as a mistake where a
	// tighter column does not.
	s.Padding.Top *= d.Spacing
	s.Padding.Bottom *= d.Spacing
	s.Margin.Top *= d.Spacing
	s.Margin.Bottom *= d.Spacing

	if d.Text != 1 && s.Size > 0 {
		s.Size *= d.Text
	}

	for _, child := range n.Children {
		applyDensity(child, d)
	}
}
