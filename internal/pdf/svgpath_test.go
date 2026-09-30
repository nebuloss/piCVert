package pdf

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Every icon every shipped template carries must translate WHOLE.
//
// # WHY THIS LOOKS AT THE DRAWING AND NOT AT THE PARSE
//
// The translator has no way to report a failure and should not have one: an
// icon it cannot finish still returns the operators it managed, the painter
// fills them, and a half-drawn outline closes into a plausible-looking blob.
// A phone became a lozenge and a location pin lost the hole in its head, and
// nothing anywhere said so — the PDF was valid, the page was the right size,
// and the only evidence was looking at it.
//
// So this asks the two questions a truncation cannot survive: does the ink
// still close every subpath the source closes, and does it still reach the
// edges of the box the icon was drawn in.
func TestEveryShippedIconTranslatesWhole(t *testing.T) {
	sets, err := filepath.Glob(filepath.Join("..", "..", "templates", "*", "icons.json"))
	if err != nil || len(sets) == 0 {
		t.Fatalf("no icon sets found: %v", err)
	}

	checked := 0
	for _, set := range sets {
		raw, err := os.ReadFile(set)
		if err != nil {
			t.Fatalf("read %s: %v", set, err)
		}
		var icons map[string]struct {
			ViewBox string `json:"viewBox"`
			Path    string `json:"path"`
		}
		if err := json.Unmarshal(raw, &icons); err != nil {
			t.Fatalf("parse %s: %v", set, err)
		}
		template := filepath.Base(filepath.Dir(set))

		for name, icon := range icons {
			checked++
			ops := svgPathToPDF(icon.Path)
			side := parseViewBox(icon.ViewBox)
			if side == 0 {
				t.Errorf("%s/%s: unreadable viewBox %q", template, name, icon.ViewBox)
				continue
			}

			// Every subpath the source closes must be closed in the output.
			// A command abandoned halfway never reaches its own `z`.
			wantClosed := strings.Count(icon.Path, "z") + strings.Count(icon.Path, "Z")
			if got := strings.Count(ops, "h\n"); got != wantClosed {
				t.Errorf("%s/%s: %d subpaths closed, the source closes %d — "+
					"the path is being abandoned partway", template, name, got, wantClosed)
			}

			// And the ink must still fill the box it was designed in. A
			// truncated icon draws the first few strokes and stops, which no
			// count of operators catches but a bounding box does.
			//
			// Half the box, not most of it: a location pin is legitimately 14
			// wide in a 24 box, so a threshold tight enough to be interesting
			// on its own would be one that fires on the pin. The close count
			// above is the precise detector; this is the coarse one, and it is
			// here for the truncation that happens before anything closes.
			minX, minY, maxX, maxY := pathBounds(t, ops)
			if maxX-minX < side*0.5 || maxY-minY < side*0.5 {
				t.Errorf("%s/%s: ink spans %.1fx%.1f of a %.0f box — the icon is cut short",
					template, name, maxX-minX, maxY-minY, side)
			}
			if minX < -0.5 || minY < -0.5 || maxX > side+0.5 || maxY > side+0.5 {
				t.Errorf("%s/%s: ink runs outside the viewBox, to (%.2f,%.2f)-(%.2f,%.2f)",
					template, name, minX, minY, maxX, maxY)
			}
		}
	}
	if checked < 5 {
		// A probe that measures nothing reports no failures.
		t.Fatalf("only %d icons checked — the test is not finding the templates", checked)
	}
	t.Logf("%d icons translate whole", checked)
}

// An arc is the command a material icon set is most likely to need and the one
// this translator did not have. Checked against the circle it describes, since
// that is a thing with a known answer.
func TestArcFollowsTheCircleItDescribes(t *testing.T) {
	// The whole of a circle of radius 5 about (12,12), as two half arcs —
	// which is how a minifier writes a dot, and how the location pin does.
	ops := svgPathToPDF("M7 12a5 5 0 1010 0a5 5 0 10-10 0z")
	if strings.Count(ops, "h\n") != 1 {
		t.Fatalf("the circle did not close:\n%s", ops)
	}
	for _, p := range flatten(t, ops, 7, 12) {
		r := math.Hypot(p[0]-12, p[1]-12)
		if math.Abs(r-5) > 0.01 {
			t.Errorf("a point of the circle sits %.4f from the centre, not 5", r)
		}
	}
}

// The two arc flags are single characters and may be written with no separator
// at all. Read as ordinary numbers, "110-5" becomes one hundred and ten.
func TestArcFlagsAreReadAsSingleCharacters(t *testing.T) {
	compact := svgPathToPDF("M9.5 9a2.5 2.5 0 110-5 2.5 2.5 0 010 5z")
	spaced := svgPathToPDF("M9.5 9a2.5 2.5 0 1 1 0-5 2.5 2.5 0 0 1 0 5z")
	if compact != spaced {
		t.Errorf("the compact form translates differently from the spaced one:\n%s\nvs\n%s",
			compact, spaced)
	}
}

// A separator may be dropped whenever the next number starts with a dot, which
// is what every minified icon does and what used to make one unreadable token.
func TestNumbersRunTogetherAtTheDecimalPoint(t *testing.T) {
	together := svgPathToPDF("M0 0l.27.67.5.5z")
	apart := svgPathToPDF("M0 0l0.27 0.67 0.5 0.5z")
	if together != apart {
		t.Errorf("run-together numbers translate differently:\n%s\nvs\n%s", together, apart)
	}
	if !strings.Contains(together, "0.270 0.670 l") {
		t.Errorf("expected a line to (0.27,0.67), got:\n%s", together)
	}
}

// A quadratic has no PDF operator and must become the cubic it equals exactly.
func TestQuadraticBecomesTheCubicItEquals(t *testing.T) {
	ops := svgPathToPDF("M0 0Q10 0 10 10")
	// Controls a third and two thirds of the way towards the quadratic's own.
	if !strings.Contains(ops, "6.667 0.000 10.000 3.333 10.000 10.000 c") {
		t.Errorf("quadratic converted wrongly:\n%s", ops)
	}
}

// pathBounds is the box the emitted operators draw inside.
func pathBounds(t *testing.T, ops string) (float64, float64, float64, float64) {
	t.Helper()
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, line := range strings.Split(ops, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// Only the endpoints, never a control point: a control point may sit
		// outside the curve it bends and would report ink that is not there.
		x, errX := strconv.ParseFloat(fields[len(fields)-3], 64)
		y, errY := strconv.ParseFloat(fields[len(fields)-2], 64)
		if errX != nil || errY != nil {
			continue
		}
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	if math.IsInf(minX, 1) {
		t.Fatalf("no points at all in:\n%s", ops)
	}
	return minX, minY, maxX, maxY
}

// flatten walks the curves, returning points along them rather than only their
// ends — which is what it takes to ask whether a curve follows a circle.
func flatten(t *testing.T, ops string, startX, startY float64) [][2]float64 {
	t.Helper()
	var out [][2]float64
	x, y := startX, startY
	for _, line := range strings.Split(ops, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[len(fields)-1] {
		case "m", "l":
			x, y = toNum(t, fields[0]), toNum(t, fields[1])
			out = append(out, [2]float64{x, y})
		case "c":
			x1, y1 := toNum(t, fields[0]), toNum(t, fields[1])
			x2, y2 := toNum(t, fields[2]), toNum(t, fields[3])
			x3, y3 := toNum(t, fields[4]), toNum(t, fields[5])
			for i := 1; i <= 16; i++ {
				s := float64(i) / 16
				r := 1 - s
				out = append(out, [2]float64{
					r*r*r*x + 3*r*r*s*x1 + 3*r*s*s*x2 + s*s*s*x3,
					r*r*r*y + 3*r*r*s*y1 + 3*r*s*s*y2 + s*s*s*y3,
				})
			}
			x, y = x3, y3
		}
	}
	if len(out) == 0 {
		t.Fatalf("nothing drawn by:\n%s", ops)
	}
	return out
}

func toNum(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("unreadable operand %q: %v", s, err)
	}
	return v
}
