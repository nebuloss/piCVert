package layout

import (
	"strings"
	"testing"
)

// A polygon must fill its box, not degenerate into a rectangle.
//
// The viewBox is square and a node's box usually is not. Stretched to fill a
// 54x62 box, a hexagon whose points were computed on a circle lands its six
// vertices on the corners of its bounding box and draws as a RECTANGLE — which
// is what happened, and looked like the polygon maths being wrong rather than
// the scaling.
func TestPolygonFillsItsBoxWithoutDegenerating(t *testing.T) {
	pts := polygonPoints(6, 30)
	coords := strings.Fields(pts)
	if len(coords) != 6 {
		t.Fatalf("expected 6 points, got %d: %s", len(coords), pts)
	}
	// It touches every edge of the viewBox…
	if !strings.Contains(pts, "0,") || !strings.Contains(pts, "100,") {
		t.Errorf("the shape does not reach the edges of its box: %s", pts)
	}
	// …and its points are not all at the corners, which is what a rectangle is.
	corners := 0
	for _, c := range coords {
		x, y, _ := strings.Cut(c, ",")
		if (x == "0" || x == "100") && (y == "0" || y == "100") {
			corners++
		}
	}
	if corners > 2 {
		t.Errorf("%d of 6 points sit on a corner; this will draw as a rectangle: %s",
			corners, pts)
	}
}
