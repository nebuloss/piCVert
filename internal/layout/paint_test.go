package layout

import (
	"strings"
	"testing"
)

// Padding must survive the trip from the engine to the markup.
//
// It did not, once, and the way it failed is worth keeping a test for: the
// engine placed children correctly inside their parent's padding, and the
// painter then wrote their offsets relative to the CONTENT corner — subtracting
// the very padding CSS was never going to add back, because an absolutely
// positioned child is placed against the padding box. The two cancelled exactly,
// on every nested box, so it looked as though the padding had been forgotten
// rather than counted twice.
func TestPaddingSurvivesPainting(t *testing.T) {
	e := NewEngine(NewFonts())
	child := Box(Style{Display: Block, Width: 50, Height: 10})
	card := Box(Style{Display: Block, Width: 200, Padding: XY(11, 15)}, child)
	page := Box(Style{Display: Block, Width: 300, Height: 400, Padding: All(30)}, card)

	root := e.Layout(page, 300, 400)

	// The engine's own view: page padding, then the card's.
	placed := root.Children[0].Children[0]
	if placed.X != 45 || placed.Y != 41 {
		t.Fatalf("engine placed the child at (%.0f,%.0f), want (45,41)", placed.X, placed.Y)
	}

	// And the markup, where each offset is relative to the box CSS measures
	// from.
	html := RenderHTML(&Render{Frame: root, Width: 300, Height: 400, Usable: 370})
	if !strings.Contains(html, "left:30px;top:30px") {
		t.Errorf("the card lost the page's padding:\n%s", html)
	}
	if !strings.Contains(html, "left:15px;top:11px") {
		t.Errorf("the child lost the card's padding:\n%s", html)
	}
}

// Each line of text gets a box of its own height, at its own top.
//
// The baseline used to be a transform on a zero-height box at the top of the
// paragraph. It DREW correctly — a transform moves the ink — but every line's
// rectangle then covered the whole paragraph, so selecting one line selected
// its neighbours and no overlap check could tell a real collision from this.
func TestEachLineHasItsOwnBox(t *testing.T) {
	f := loadTestFonts(t)
	e := NewEngine(f)
	style := Style{Display: Text, Family: "Roboto", Size: 10, Weight: Regular, LineHeight: 1.4}
	// Narrow enough to force several lines.
	para := Para(style, "alpha beta gamma delta epsilon zeta eta theta")
	page := Box(Style{Display: Block, Width: 120, Height: 400}, para)

	root := e.Layout(page, 120, 400)
	text := root.Children[0]
	if len(text.Lines) < 2 {
		t.Fatalf("expected the text to wrap, got %d line(s)", len(text.Lines))
	}

	html := RenderHTML(&Render{Frame: root, Width: 120, Height: 400, Usable: 400})
	if strings.Contains(html, "translateY") {
		t.Error("lines are still placed by transform; their boxes will not match their ink")
	}
	// The second line sits one line-height down, not at the top with the first.
	if !strings.Contains(html, "top:14px") {
		t.Errorf("the second line is not at its own top:\n%s", html)
	}
}

// A text box is as wide as the text it holds, not as wide as the room it was
// offered — otherwise a short line claims a whole column and overlaps whatever
// sits beside it.
func TestTextBoxShrinksToItsContent(t *testing.T) {
	f := loadTestFonts(t)
	e := NewEngine(f)
	style := Style{Display: Text, Family: "Roboto", Size: 10, Weight: Regular}
	short := Para(style, "hi")
	page := Box(Style{Display: Block, Width: 400, Height: 100}, short)

	root := e.Layout(page, 400, 100)
	got := root.Children[0]
	if got.Width > 40 {
		t.Errorf("a two-letter paragraph measured %.0fpx wide inside a 400px column", got.Width)
	}
}

// The page is painted like any other box, style included.
//
// It used to be written by hand — a div with a width, a height and a clip —
// with only its CHILDREN painted, so the page's own style was silently
// dropped. The theme asks for a pale surface behind everything and the page
// came out white; every card is white too, so a column of them disappeared into
// the background and only their contents showed. That reads as cards having
// lost their shape, which is a long way from the cause.
func TestPageIsPaintedWithItsOwnStyle(t *testing.T) {
	e := NewEngine(NewFonts())
	card := Box(Style{Display: Block, Width: 100, Height: 40,
		Radius: 20, Background: Fill{Colour: "#FFFFFF"}})
	page := Box(Style{Display: Block, Padding: All(30),
		Background: Fill{Colour: "#F5F8FE"}}, card)

	root := e.Layout(page, 794, 1123)
	html := RenderHTML(&Render{Frame: root, Width: 794, Height: 1123, Usable: 1093})

	if !strings.Contains(html, "background:#F5F8FE") {
		t.Errorf("the page lost its own background:\n%s", html)
	}
	if !strings.Contains(html, "border-radius:20px") {
		t.Errorf("the card lost its rounded corners:\n%s", html)
	}
	// The page is the positioning context for everything inside it.
	if !strings.Contains(html, `class="page" style="position:relative`) {
		t.Errorf("the page must be the positioned ancestor:\n%s", html)
	}
}

// A sheet of A4 is 794x1123 whatever a theme would prefer.
//
// The page is a block, and a block sizes itself to its content — so left to
// itself it simply grew to fit whatever was put on it. Nothing ever overflowed,
// and the fit check had nothing to report: exactly the failure a one-page CV
// engine exists to prevent.
func TestPageKeepsItsSizeWhateverItHolds(t *testing.T) {
	e := NewEngine(NewFonts())
	// Named, because overflow is reported by name — a block the theme did not
	// name cannot be pointed at, which is why the conformance kit insists every
	// card has an id.
	tall := Box(Style{Display: Block, Width: 100, Height: 4000}).Named("too-tall")
	// A theme that says nothing about the page size, as themes do.
	page := Box(Style{Display: Block, Padding: All(30)}, tall)

	root := e.Layout(page, 794, 1123)
	if root.Height != 1123 || root.Width != 794 {
		t.Errorf("the page grew to %.0fx%.0f; it must stay 794x1123", root.Width, root.Height)
	}
	if !root.Style.Clip {
		t.Error("the page must clip: what runs past its edge is what overflow reports")
	}
	if root.Fits(1093) {
		t.Error("a 4000px block must be reported as overflowing")
	}
}
