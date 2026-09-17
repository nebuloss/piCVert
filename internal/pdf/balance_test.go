package pdf

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"picvert/internal/layout"
)

// checkout is the repository root, found by the file that marks it.
func checkout(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 6 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			break
		}
		dir = up
	}
	t.Fatal("cannot find the checkout root")
	return ""
}

// Every `q` must have its `Q`.
//
// A content stream is a stack machine: `q` saves the graphics state and `Q`
// restores it. One `q` too many leaves whatever it set — a clip, a transform —
// in force for everything drawn afterwards. It cost a page that rendered with
// most of its content clipped away, and nothing in the file is invalid, so no
// reader complains.
func TestGraphicsStateIsBalanced(t *testing.T) {
	// A page with the shapes that push state: a gradient, a clip, a polygon.
	page := layout.Box(layout.Style{Display: layout.Block, Width: 794, Height: 1123,
		Background: layout.Fill{Colour: "#F5F8FE"}},
		layout.Box(layout.Style{Display: layout.Block, Width: 700, Height: 180,
			Radius: 26, Clip: true,
			Background: layout.Fill{Gradient: &layout.Gradient{Angle: 135, Stops: []layout.Stop{
				{At: 0, Colour: "#D3E3FD"}, {At: 1, Colour: "#CFE7DC"}}}}}),
		layout.Box(layout.Style{Display: layout.Polygon, Width: 40, Height: 46,
			Sides: 6, Opacity: 0.2, Background: layout.Fill{Colour: "#0B57D0"}}),
		layout.Box(layout.Style{Display: layout.Block, Width: 300, Height: 100,
			Radius: 20, Background: layout.Fill{Colour: "#FFFFFF"},
			Border: struct {
				Width  float64
				Colour string
			}{Width: 1, Colour: "#D5DBE6"}}),
	)
	frame := layout.NewEngine(layout.NewFonts()).Layout(page, 794, 1123)

	p := NewPainter(nil, 1123)
	p.images = map[string]string{}
	frame.Paint(p)
	ops := p.ops.String()

	q := len(regexp.MustCompile(`(?m)^q$`).FindAllString(ops, -1))
	rest := len(regexp.MustCompile(`(?m)^Q$`).FindAllString(ops, -1))
	if q != rest {
		t.Errorf("%d q against %d Q: the graphics state is left pushed, and "+
			"whatever it holds — a clip, a transform — applies to everything after it", q, rest)
	}

	// And the stack never goes negative, which would be a Q with nothing to
	// restore.
	depth := 0
	for _, line := range strings.Split(ops, "\n") {
		switch line {
		case "q":
			depth++
		case "Q":
			depth--
			if depth < 0 {
				t.Fatal("a Q with no matching q: the stream restores a state it never saved")
			}
		}
	}
}

// A dot must be round in the PDF, as it is on the page.
//
// An ellipse is a box whose corner radius is half its shorter side — which is
// what the HTML painter means by `border-radius:50%`. Without that, the section
// dots and list bullets drew as squares in the PDF and as circles on screen:
// the same design, two renderings, disagreeing. Exactly the class of fault this
// engine exists to remove, and small enough to miss at page scale.
func TestEllipsesAreRound(t *testing.T) {
	dot := layout.Box(layout.Style{Display: layout.Ellipse, Width: 8, Height: 8,
		Background: layout.Fill{Colour: "#0B57D0"}})
	page := layout.Box(layout.Style{Display: layout.Block, Width: 100, Height: 100}, dot)
	frame := layout.NewEngine(layout.NewFonts()).Layout(page, 100, 100)

	p := NewPainter(nil, 100)
	p.images = map[string]string{}
	frame.Paint(p)
	ops := p.ops.String()

	// A rounded path is drawn with curves; a square one is a single `re`.
	if strings.Contains(ops, " re\n") && !strings.Contains(ops, " c\n") {
		t.Errorf("the dot was drawn as a rectangle, not a circle:\n%s", ops)
	}
	if !strings.Contains(ops, " c\n") {
		t.Errorf("no curve operators: the dot has no rounded corners:\n%s", ops)
	}
}

// Character spacing must not leak out of the text object that set it.
//
// `Tc` is TEXT STATE, not part of a text object: `ET` ends the object and
// resets the text matrix, and leaves `Tc` exactly as it was. Written only when
// non-zero — which looks like a sensible economy — a section title's
// letter-spacing carried on into every paragraph drawn after it. At 0.6 pt a
// character, a thirty-character run came out twenty points wider than the
// engine measured it and was drawn straight over the run beside it.
//
// The fault is invisible to every check that READS the PDF: the text extracts
// perfectly, because extraction does not care where the glyphs landed. Only
// looking at the page shows it, which is why this test looks at the operators
// rather than at the words.
func TestCharacterSpacingDoesNotLeakBetweenRuns(t *testing.T) {
	spaced := layout.Para(layout.Style{
		Display: layout.Text, Family: "Roboto", Size: 10, Letter: 0.8,
	}, "A SECTION TITLE")
	plain := layout.Para(layout.Style{
		Display: layout.Text, Family: "Roboto", Size: 10,
	}, "A paragraph that follows it and sets no spacing of its own")
	page := layout.Box(layout.Style{Display: layout.Block, Width: 400, Height: 200},
		spaced, plain)
	// The real face, because a painter with no face draws nothing at all and
	// the check would pass by having nothing to look at.
	file := filepath.Join(checkout(t), "fonts", "Roboto-Regular.ttf")
	fonts := layout.NewFonts()
	if err := fonts.Load("Roboto", 400, false, file); err != nil {
		t.Fatal(err)
	}
	face, err := LoadFace("", "Roboto", 400, false, file)
	if err != nil {
		t.Fatal(err)
	}
	frame := layout.NewEngine(fonts).Layout(page, 400, 200)

	p := NewPainter([]*Face{face}, 200)
	p.images = map[string]string{}
	frame.Paint(p)

	// Every text object states the spacing it wants, so none of them inherits
	// one. Checked per object rather than by counting, because "some of them
	// set it" is exactly the state that produced the fault.
	objects := regexp.MustCompile(`(?s)BT\n(.*?)ET`).FindAllStringSubmatch(p.ops.String(), -1)
	if len(objects) < 2 {
		t.Fatalf("expected a text object per run, got %d", len(objects))
	}
	for i, object := range objects {
		body := object[1]
		// An object that draws nothing has nothing to get wrong.
		if !strings.Contains(body, " Tj") {
			continue
		}
		if !strings.Contains(body, " Tc") {
			t.Errorf("text object %d draws without stating its character spacing, "+
				"so it inherits whatever the last one left:\n%s", i, body)
		}
	}
}
