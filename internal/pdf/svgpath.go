package pdf

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// svgPathToPDF translates an SVG path to PDF drawing operators.
//
// PDF has no SVG, so an icon has to be re-expressed: move, line, horizontal,
// vertical, cubic, quadratic, arc, close. PDF has no quadratic and no arc
// operator either, so both are converted to cubics — exactly, for the
// quadratic, and to within a fifth of a thousandth of a pixel for the arc,
// which is split into segments of ninety degrees or less.
//
// # WHY EVERY COMMAND IS HANDLED RATHER THAN THE COMMON ONES
//
// This used to stop at the first command it did not know, on the reasoning
// that drawing nothing beats drawing something wrong. It does — but it was
// never nothing: the commands ALREADY EMITTED stayed, so an icon whose circle
// was an arc came out as the half of it drawn before the arc, filled shut by
// the path's own closing. A location pin lost its hole, and the GitHub mark,
// which is one long arc, became a blob. Nothing reported an error, because
// nothing had gone wrong as far as this function was concerned.
func svgPathToPDF(d string) string {
	var out strings.Builder
	t := &pathTokens{src: d}
	var cx, cy float64 // current point
	var sx, sy float64 // start of the current subpath, for Z
	var prevCx, prevCy float64
	var last byte

	for {
		cmd, ok := t.command()
		if !ok {
			break
		}
		relative := cmd >= 'a' && cmd <= 'z'
		abs := upper(cmd)

		for {
			switch abs {
			case 'M':
				x, y, ok := t.pair()
				if !ok {
					goto next
				}
				if relative {
					x, y = cx+x, cy+y
				}
				fmt.Fprintf(&out, "%s %s m\n", fnum(x), fnum(y))
				cx, cy, sx, sy = x, y, x, y
				// A second pair after M is an implicit L, per the spec.
				abs = 'L'
			case 'L':
				x, y, ok := t.pair()
				if !ok {
					goto next
				}
				if relative {
					x, y = cx+x, cy+y
				}
				fmt.Fprintf(&out, "%s %s l\n", fnum(x), fnum(y))
				cx, cy = x, y
			case 'H':
				x, ok := t.number()
				if !ok {
					goto next
				}
				if relative {
					x += cx
				}
				fmt.Fprintf(&out, "%s %s l\n", fnum(x), fnum(cy))
				cx = x
			case 'V':
				y, ok := t.number()
				if !ok {
					goto next
				}
				if relative {
					y += cy
				}
				fmt.Fprintf(&out, "%s %s l\n", fnum(cx), fnum(y))
				cy = y
			case 'C':
				x1, y1, ok1 := t.pair()
				x2, y2, ok2 := t.pair()
				x, y, ok3 := t.pair()
				if !ok1 || !ok2 || !ok3 {
					goto next
				}
				if relative {
					x1, y1 = cx+x1, cy+y1
					x2, y2 = cx+x2, cy+y2
					x, y = cx+x, cy+y
				}
				fmt.Fprintf(&out, "%s %s %s %s %s %s c\n",
					fnum(x1), fnum(y1), fnum(x2), fnum(y2), fnum(x), fnum(y))
				cx, cy, prevCx, prevCy = x, y, x2, y2
			case 'S':
				// A smooth cubic: the first control point mirrors the last one.
				x2, y2, ok1 := t.pair()
				x, y, ok2 := t.pair()
				if !ok1 || !ok2 {
					goto next
				}
				if relative {
					x2, y2 = cx+x2, cy+y2
					x, y = cx+x, cy+y
				}
				x1, y1 := cx, cy
				if upper(last) == 'C' || upper(last) == 'S' {
					x1, y1 = 2*cx-prevCx, 2*cy-prevCy
				}
				fmt.Fprintf(&out, "%s %s %s %s %s %s c\n",
					fnum(x1), fnum(y1), fnum(x2), fnum(y2), fnum(x), fnum(y))
				cx, cy, prevCx, prevCy = x, y, x2, y2
			case 'Q', 'T':
				// A quadratic, written as the cubic it exactly equals: the two
				// cubic controls sit a third and two thirds of the way from
				// each end towards the single quadratic one.
				qx, qy := 2*cx-prevCx, 2*cy-prevCy
				if upper(last) != 'Q' && upper(last) != 'T' {
					qx, qy = cx, cy
				}
				var ok1 bool
				if abs == 'Q' {
					qx, qy, ok1 = t.pair()
					if !ok1 {
						goto next
					}
					if relative {
						qx, qy = cx+qx, cy+qy
					}
				}
				x, y, ok2 := t.pair()
				if !ok2 {
					goto next
				}
				if relative {
					x, y = cx+x, cy+y
				}
				fmt.Fprintf(&out, "%s %s %s %s %s %s c\n",
					fnum(cx+2.0/3.0*(qx-cx)), fnum(cy+2.0/3.0*(qy-cy)),
					fnum(x+2.0/3.0*(qx-x)), fnum(y+2.0/3.0*(qy-y)),
					fnum(x), fnum(y))
				cx, cy, prevCx, prevCy = x, y, qx, qy
			case 'A':
				rx, ry, ok1 := t.pair()
				rotation, ok2 := t.number()
				// THE TWO FLAGS ARE SINGLE CHARACTERS, and the grammar lets
				// them run together with what follows: "a2.5 2.5 0 110-5" is
				// large-arc 1, sweep 1, then 0 and -5. Read as ordinary
				// numbers they come out as one hundred and ten.
				large, ok3 := t.flag()
				sweep, ok4 := t.flag()
				x, y, ok5 := t.pair()
				if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
					goto next
				}
				if relative {
					x, y = cx+x, cy+y
				}
				out.WriteString(arcToCubics(cx, cy, rx, ry, rotation, large, sweep, x, y))
				cx, cy, prevCx, prevCy = x, y, x, y
			case 'Z':
				out.WriteString("h\n")
				cx, cy = sx, sy
				goto next
			default:
				// A command outside SVG's own grammar: stop rather than emit
				// nonsense, which would corrupt every path after it.
				return out.String()
			}
			last = cmd
			if !t.moreNumbers() {
				break
			}
		}
	next:
		last = cmd
	}
	return out.String()
}

// arcToCubics writes an elliptical arc as cubic Bézier segments.
//
// The endpoint parameterisation SVG uses says where the arc ENDS; drawing it
// needs the centre it turns about, so this is the conversion in the spec's own
// appendix F.6.5, followed by the standard four-magic-number approximation of
// a circular sweep by a cubic. Split at ninety degrees, the error is under
// 0.0003 of the radius — a thousandth of a pixel on a 24-pixel icon, which is
// well inside the ink.
func arcToCubics(x1, y1, rx, ry, rotation float64, large, sweep bool, x2, y2 float64) string {
	// A degenerate radius is a straight line, per the spec, not an error.
	if rx == 0 || ry == 0 || (x1 == x2 && y1 == y2) {
		return fmt.Sprintf("%s %s l\n", fnum(x2), fnum(y2))
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	phi := rotation * math.Pi / 180
	cosPhi, sinPhi := math.Cos(phi), math.Sin(phi)

	dx, dy := (x1-x2)/2, (y1-y2)/2
	x1p := cosPhi*dx + sinPhi*dy
	y1p := -sinPhi*dx + cosPhi*dy

	// Radii too small to reach the far end are scaled up until they just do,
	// which is what the spec asks for rather than refusing to draw.
	if lambda := x1p*x1p/(rx*rx) + y1p*y1p/(ry*ry); lambda > 1 {
		s := math.Sqrt(lambda)
		rx, ry = rx*s, ry*s
	}

	num := rx*rx*ry*ry - rx*rx*y1p*y1p - ry*ry*x1p*x1p
	den := rx*rx*y1p*y1p + ry*ry*x1p*x1p
	factor := 0.0
	if den != 0 && num > 0 {
		factor = math.Sqrt(num / den)
	}
	if large == sweep {
		factor = -factor
	}
	cxp := factor * rx * y1p / ry
	cyp := -factor * ry * x1p / rx
	cx := cosPhi*cxp - sinPhi*cyp + (x1+x2)/2
	cy := sinPhi*cxp + cosPhi*cyp + (y1+y2)/2

	theta := math.Atan2((y1p-cyp)/ry, (x1p-cxp)/rx)
	delta := math.Atan2((-y1p-cyp)/ry, (-x1p-cxp)/rx) - theta
	switch {
	case !sweep && delta > 0:
		delta -= 2 * math.Pi
	case sweep && delta < 0:
		delta += 2 * math.Pi
	}

	var out strings.Builder
	segments := int(math.Ceil(math.Abs(delta) / (math.Pi / 2)))
	step := delta / float64(segments)
	// The control-point distance that makes a cubic follow a circular sweep.
	k := 4.0 / 3.0 * math.Tan(step/4)
	for i := 0; i < segments; i++ {
		a0 := theta + float64(i)*step
		a1 := a0 + step
		px0, py0 := ellipse(cx, cy, rx, ry, cosPhi, sinPhi, a0)
		px1, py1 := ellipse(cx, cy, rx, ry, cosPhi, sinPhi, a1)
		dx0, dy0 := ellipseSlope(rx, ry, cosPhi, sinPhi, a0)
		dx1, dy1 := ellipseSlope(rx, ry, cosPhi, sinPhi, a1)
		fmt.Fprintf(&out, "%s %s %s %s %s %s c\n",
			fnum(px0+k*dx0), fnum(py0+k*dy0),
			fnum(px1-k*dx1), fnum(py1-k*dy1),
			fnum(px1), fnum(py1))
	}
	return out.String()
}

// ellipse is the point on the ellipse at an angle, in the icon's own frame.
func ellipse(cx, cy, rx, ry, cosPhi, sinPhi, angle float64) (float64, float64) {
	c, s := math.Cos(angle), math.Sin(angle)
	return cx + rx*c*cosPhi - ry*s*sinPhi, cy + rx*c*sinPhi + ry*s*cosPhi
}

// ellipseSlope is the tangent there, which is where a control point goes.
func ellipseSlope(rx, ry, cosPhi, sinPhi, angle float64) (float64, float64) {
	c, s := math.Cos(angle), math.Sin(angle)
	return -rx*s*cosPhi - ry*c*sinPhi, -rx*s*sinPhi + ry*c*cosPhi
}

func upper(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 32
	}
	return c
}

func fnum(v float64) string {
	return strconv.FormatFloat(v, 'f', 3, 64)
}

// pathTokens walks an SVG path's commands and numbers.
type pathTokens struct {
	src string
	i   int
}

func (t *pathTokens) skip() {
	for t.i < len(t.src) {
		c := t.src[t.i]
		if c == ' ' || c == ',' || c == '\t' || c == '\n' || c == '\r' {
			t.i++
			continue
		}
		return
	}
}

func (t *pathTokens) command() (byte, bool) {
	t.skip()
	if t.i >= len(t.src) {
		return 0, false
	}
	c := t.src[t.i]
	if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
		t.i++
		return c, true
	}
	return 0, false
}

func (t *pathTokens) moreNumbers() bool {
	t.skip()
	if t.i >= len(t.src) {
		return false
	}
	c := t.src[t.i]
	return c == '-' || c == '.' || (c >= '0' && c <= '9')
}

// flag reads one of the arc command's two boolean flags.
//
// A flag is a SINGLE character, and the grammar allows it to be written with
// no separator at all: "a2.5 2.5 0 110-5" carries large-arc 1, sweep 1, then
// the pair 0,-5. Read as an ordinary number that is one hundred and ten, and
// the rest of the command is then read off by one.
func (t *pathTokens) flag() (bool, bool) {
	t.skip()
	if t.i >= len(t.src) {
		return false, false
	}
	switch t.src[t.i] {
	case '0':
		t.i++
		return false, true
	case '1':
		t.i++
		return true, true
	}
	return false, false
}

// number reads one number, and stops where the next one starts.
//
// # A SECOND DECIMAL POINT BEGINS A SECOND NUMBER
//
// SVG lets a separator be omitted whenever the next number starts with a dot,
// so ".27.67" is two numbers and "1.99.9" is 1.99 and 0.9 — a compaction every
// minifier applies and every material icon therefore contains. Consuming both
// halves made one unparseable token, the command holding it was abandoned
// halfway, and the icon was drawn as however much of it came before that
// point: the phone and the envelope each lost their remaining strokes and
// filled shut into a lozenge.
func (t *pathTokens) number() (float64, bool) {
	t.skip()
	start := t.i
	if t.i < len(t.src) && (t.src[t.i] == '-' || t.src[t.i] == '+') {
		t.i++
	}
	seenDot := false
	for t.i < len(t.src) {
		c := t.src[t.i]
		if c >= '0' && c <= '9' {
			t.i++
			continue
		}
		if c == '.' && !seenDot {
			seenDot = true
			t.i++
			continue
		}
		// An exponent, which a coordinate is entitled to carry, and whose own
		// sign is part of it rather than the start of the next number.
		if (c == 'e' || c == 'E') && t.i > start {
			next := t.i + 1
			if next < len(t.src) && (t.src[next] == '-' || t.src[next] == '+') {
				next++
			}
			if next < len(t.src) && t.src[next] >= '0' && t.src[next] <= '9' {
				t.i = next
				continue
			}
		}
		break
	}
	if start == t.i {
		return 0, false
	}
	v, err := strconv.ParseFloat(t.src[start:t.i], 64)
	return v, err == nil
}

func (t *pathTokens) pair() (float64, float64, bool) {
	x, ok := t.number()
	if !ok {
		return 0, 0, false
	}
	y, ok := t.number()
	return x, y, ok
}
