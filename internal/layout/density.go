package layout

// Density is how tightly a page is set.
//
// # WHY THE ENGINE SETS THE SPACING RATHER THAN THE AUTHOR
//
// A CV is one page, and a design drawn at one fixed rhythm meets that page in
// only two ways: it runs over, and the author is told to cut a sentence, or it
// stops short, and the page ends in a band of white that says the document ran
// out rather than that it finished. Both are the design's problem handed to
// whoever wrote the text.
//
// A designer laying this out by hand would do neither. They would close the
// gaps a little, or open them a little, until the columns reached the foot of
// the page, and would never mention it. That adjustment is mechanical, it is
// invisible to a reader, and it is what this does.
//
// # WHAT IS ALLOWED TO GIVE, AND IN WHAT ORDER
//
// Spacing before type, always. The gaps between cards, the margins between
// entries and the padding inside them carry several percent of give in either
// direction that nobody can see — a reader has no reference for what the gap
// "should" have been. Font size has no such slack: a CV set at 92% looks like a
// CV that was shrunk, and everyone recognises it. So type is only ever touched
// when spacing alone cannot bring the page home.
//
// Leading is not spacing for this purpose. It comes from the type size and
// moves only with it, because the line pitch inside a paragraph is the one
// vertical measure a reader really does feel: gaps between blocks can close a
// long way before anything becomes harder to read, and lines cannot.
//
// Fixed sizes are never touched at all. The portrait, the section dots, the
// gauge bars and the corner radii are the design's proportions, and scaling
// them is what makes a page look squashed rather than merely close-set.
//
// # WHY EACH COLUMN IS SET SEPARATELY
//
// Two columns hold different amounts of text, so one rhythm cannot fill both:
// tightening until the fuller column fits leaves the emptier one ending half
// way up the page, which is the ragged bottom edge this exists to remove. The
// columns are independent — nothing in one moves anything in the other — so
// each gets the spacing that brings it to the foot of the page, and the page
// comes out square at the bottom.
//
// The page's own chrome is left alone. The header is not what overflows, and a
// header that breathed differently from one CV to the next would be the one
// part of this that a reader WOULD notice.
type Density struct {
	// Regions scales the spacing inside each region, in the template's own
	// column order.
	Regions []float64
	// Text scales font size, and with it every line height derived from it.
	Text float64
}

// The range a column's spacing may be set within.
//
// The floor exists because a fit that always succeeds is a fit that means
// nothing: past this point the honest answer is that the CV is too long, and
// the engine says so and names what to shorten — which is something the author
// can act on, where a page of 7-point type is not.
//
// The ceiling exists for the same reason from the other side. A nearly empty CV
// cannot be inflated into a full one, and a page whose four cards stand an inch
// apart looks worse than one that simply ends early.
const (
	tightestSpacing = 0.62
	loosestSpacing  = 1.40
)

// textLadder is tried in order, and only when spacing alone is not enough.
var textLadder = []float64{1.00, 0.97, 0.94, 0.92, 0.90}

// refinements is how many halvings the spacing search gets.
//
// Nine brings the range down to under a thousandth, far below what any eye
// resolves; the cost is nine layouts, and a layout of a CV is under a
// millisecond.
const refinements = 9

// Fitted is a page laid out at the spacing that suits it.
type Fitted struct {
	Frame   *Frame
	Density Density
	// Fits is false when even the tightest setting overflows — and then the
	// frame is that tightest attempt, so the caller can still show the page and
	// name what runs over.
	Fits bool
}

// Spread is the tightest and loosest spacing any column was set at, for
// reporting. A page nobody had to touch returns 1, 1.
func (f Fitted) Spread() (lo, hi float64) {
	if len(f.Density.Regions) == 0 {
		return 1, 1
	}
	lo, hi = f.Density.Regions[0], f.Density.Regions[0]
	for _, s := range f.Density.Regions[1:] {
		lo, hi = min(lo, s), max(hi, s)
	}
	return lo, hi
}

// LayoutFitted lays the page out at the spacing that brings each column to the
// foot of the page.
//
// Searched rather than solved for, because the relationship between spacing and
// height is not continuous: closing a gap by a pixel can remove a line from a
// paragraph three cards down, or nothing at all. It is monotone ENOUGH — looser
// is taller, near enough, near enough of the time — for halving the interval to
// land somewhere sensible, and every candidate is checked properly before it is
// kept, so a search that is misled still cannot return a page that overflows.
func (e *Engine) LayoutFitted(build func() *Node, pageWidth, pageHeight, usable float64, regions int) Fitted {
	at := func(scales []float64, text float64) (*Frame, []float64) {
		root := build()
		applyDensity(root, Density{Regions: scales, Text: text})
		frame := e.Layout(root, pageWidth, pageHeight)
		return frame, regionBottoms(frame, regions)
	}

	// A template whose page is not [chrome…, body[region…]] gets one honest
	// layout rather than a search driven by numbers that mean nothing.
	if probe, bottoms := at(nil, 1); bottoms == nil {
		return Fitted{Frame: probe, Density: Density{Text: 1}, Fits: probe.Fits(usable)}
	}

	var last *Frame
	var lastDensity Density
	for _, text := range textLadder {
		lo, hi := repeat(regions, tightestSpacing), repeat(regions, loosestSpacing)

		// The floor is the test of whether this type size can work at all: if
		// the page overflows with every gap closed as far as it will go, no
		// setting above the floor will help.
		floor, bottoms := at(lo, text)
		last, lastDensity = floor, Density{Regions: clone(lo), Text: text}
		if !within(bottoms, usable) || floor.Collides() {
			continue
		}

		best, bestScales := floor, clone(lo)
		for range refinements {
			mid := midpoint(lo, hi)
			frame, bottoms := at(mid, text)
			// Two conditions, not one. Spacing closed far enough will always
			// make a page "fit", by running its lines into one another, and a
			// page that fits by collapsing is worse than one that honestly
			// reports being too long, because it looks finished.
			if within(bottoms, usable) && !frame.Collides() {
				best, bestScales = frame, clone(mid)
			}
			// Each column moves its OWN bound, so one layout advances every
			// column's search at once — which is why this costs nine layouts
			// and not nine per column.
			for i := range mid {
				if bottoms[i] <= usable {
					lo[i] = mid[i]
				} else {
					hi[i] = mid[i]
				}
			}
		}
		return Fitted{Frame: best, Density: Density{Regions: bestScales, Text: text}, Fits: true}
	}
	return Fitted{Frame: last, Density: lastDensity, Fits: false}
}

// applyDensity sets a tree's spacing before it is measured.
//
// Applied to the NODES, before any measuring, so that every consequence follows
// on its own: a tighter gap may let a card rise, which may leave room for the
// next one. Scaling the finished frame instead would move boxes without
// re-breaking the text inside them, and the page would be wrong in a way that
// is hard to see and impossible to correct.
func applyDensity(root *Node, d Density) {
	if d.Text > 0 && d.Text != 1 {
		scaleText(root, d.Text)
	}
	body := regionParent(root)
	if body == nil || len(body.Children) != len(d.Regions) {
		return
	}
	for i, scale := range d.Regions {
		if scale != 1 {
			scaleSpacing(body.Children[i], scale)
		}
	}
}

func scaleText(n *Node, scale float64) {
	if n.Style.Size > 0 {
		n.Style.Size *= scale
	}
	for _, child := range n.Children {
		scaleText(child, scale)
	}
}

func scaleSpacing(n *Node, scale float64) {
	s := &n.Style
	s.Gap *= scale
	s.CrossGap *= scale
	// Vertical only. Horizontal padding is what keeps a chip's text off its own
	// rounded edge, and a chip whose sides close in reads as a mistake where a
	// tighter column does not.
	s.Padding.Top *= scale
	s.Padding.Bottom *= scale
	s.Margin.Top *= scale
	s.Margin.Bottom *= scale
	for _, child := range n.Children {
		scaleSpacing(child, scale)
	}
}

// regionParent is the node holding the regions: the last child of the page.
//
// Stated as a contract rather than searched for, so a theme that lays its page
// out differently gets nothing rather than a plausible wrong answer. It is the
// same contract Render.Margins reads the columns by.
func regionParent(root *Node) *Node {
	if root == nil || len(root.Children) == 0 {
		return nil
	}
	return root.Children[len(root.Children)-1]
}

// regionBottoms is how far down the page each column reaches. A nil result
// means the page does not have the shape the search needs.
func regionBottoms(f *Frame, regions int) []float64 {
	if regions <= 0 || len(f.Children) == 0 {
		return nil
	}
	body := f.Children[len(f.Children)-1]
	if len(body.Children) != regions {
		return nil
	}
	out := make([]float64, regions)
	for i := range out {
		out[i] = body.Children[i].ContentBottom()
	}
	return out
}

func within(bottoms []float64, usable float64) bool {
	for _, b := range bottoms {
		if b > usable {
			return false
		}
	}
	return true
}

func repeat(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func clone(v []float64) []float64 { return append([]float64(nil), v...) }

func midpoint(lo, hi []float64) []float64 {
	out := make([]float64, len(lo))
	for i := range out {
		out[i] = (lo[i] + hi[i]) / 2
	}
	return out
}
