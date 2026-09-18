package pdf

import "testing"

// A polygon must fill its box, not degenerate into a rectangle.
//
// The viewBox is square and a node's box usually is not. Stretched to fill a
// 54x62 box, a hexagon whose points were computed on a circle lands its six
// vertices on the corners of its bounding box and draws as a RECTANGLE — which
// is what happened, and looked like the polygon maths being wrong rather than
// the scaling.
//
// This test used to guard a second copy of this function in internal/layout,
// which nothing called: the HTML painter had stopped using it and the PDF
// painter has its own. So the maths was tested in the copy that never ran and
// untested in the one that draws every polygon on every page.
func TestPolygonFillsItsBoxWithoutDegenerating(t *testing.T) {
	pts := polygonPoints(6, 30)
	if len(pts) != 12 {
		t.Fatalf("expected 6 points (12 numbers), got %d: %v", len(pts)/2, pts)
	}

	// It touches every edge of the box…
	var minX, minY, maxX, maxY = 100.0, 100.0, 0.0, 0.0
	corners := 0
	for i := 0; i < len(pts); i += 2 {
		x, y := pts[i], pts[i+1]
		minX, maxX = min(minX, x), max(maxX, x)
		minY, maxY = min(minY, y), max(maxY, y)
		onX := x < 0.01 || x > 99.99
		onY := y < 0.01 || y > 99.99
		if onX && onY {
			corners++
		}
	}
	if minX > 0.01 || minY > 0.01 || maxX < 99.99 || maxY < 99.99 {
		t.Errorf("the shape does not reach the edges of its box: %v", pts)
	}
	// …and its points are not all at the corners, which is what a rectangle is.
	if corners > 2 {
		t.Errorf("%d of 6 points sit on a corner; this will draw as a rectangle: %v",
			corners, pts)
	}
}
