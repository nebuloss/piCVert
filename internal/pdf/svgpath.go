package pdf

import (
	"fmt"
	"strconv"
	"strings"
)

// svgPathToPDF translates an SVG path to PDF drawing operators.
//
// PDF has no SVG, so an icon has to be re-expressed. Only the commands a
// material icon uses are handled — move, line, horizontal, vertical, cubic,
// close — which is what a 24x24 glyph is made of. Arcs are not, and would draw
// nothing rather than draw something wrong; the conformance kit is what would
// catch an icon set that needed them.
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
			case 'Z':
				out.WriteString("h\n")
				cx, cy = sx, sy
				goto next
			default:
				// An unhandled command: stop rather than emit nonsense, which
				// would corrupt every path after it.
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

func (t *pathTokens) number() (float64, bool) {
	t.skip()
	start := t.i
	if t.i < len(t.src) && (t.src[t.i] == '-' || t.src[t.i] == '+') {
		t.i++
	}
	for t.i < len(t.src) {
		c := t.src[t.i]
		if (c >= '0' && c <= '9') || c == '.' {
			t.i++
			continue
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
