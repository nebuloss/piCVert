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
