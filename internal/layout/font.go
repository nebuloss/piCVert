package layout

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// ppem asks sfnt for metrics at a deliberately huge nominal size.
//
// The library answers in 26.6 fixed point — 1/64 of a pixel — so the size it is
// asked for decides how coarsely every advance is rounded. Asked at one pixel
// per font unit, the rounding is a whole font unit per glyph, and measured
// against a browser that came out 0.06 % wide: about a quarter of a pixel over
// a full line, which is enough to move a break when a word lands near the edge.
// That is the one error this package cannot afford, since a break that differs
// is exactly the fault it exists to remove.
//
// Asking at 64 units per font unit makes the rounding 64 times finer, and the
// remaining error too small to reach a break. The scale is divided back out in
// scale() below, so nothing downstream knows about it.
func (fc *face) ppem() fixed.Int26_6 { return fixed.I(int(fc.font.UnitsPerEm())) }

// scale converts what ppem() asked for back into em.
func (fc *face) scale(v fixed.Int26_6) float64 { return float64(v) / 64.0 / fc.upem }

// Fonts measures text, and is the reason this engine can promise anything.
//
// # WHY MEASUREMENT IS THE WHOLE PROBLEM
//
// Laying boxes out is arithmetic. Deciding where a paragraph breaks is not: it
// depends on the exact advance width of every glyph, on the kerning between
// each pair, and on where the engine decides a line has run out. Two engines
// that agree on every box and disagree on one of those will disagree on the
// number of lines — which is precisely how a CV that measured as fitting came
// out clipped.
//
// So the widths come from the font file itself, the same file both emitters
// embed, and every consumer of this package asks THIS for a width. Nothing
// estimates.
type Fonts struct {
	mu     sync.Mutex
	faces  map[string]*face
	buffer sfnt.Buffer
}

// face is one loaded font file, with the caches that make measuring cheap.
type face struct {
	font *sfnt.Font
	upem float64
	// advances caches the width of a rune in font units. A CV re-measures the
	// same few hundred characters on every keystroke of the preview.
	advances map[rune]float64
	// missing records runes this face has no glyph for, so a lookup that failed
	// once is not retried on every frame.
	missing map[rune]bool
}

func NewFonts() *Fonts { return &Fonts{faces: map[string]*face{}} }

// key identifies a face the way a style asks for one.
func key(family string, weight Weight, italic bool) string {
	style := "normal"
	if italic {
		style = "italic"
	}
	return fmt.Sprintf("%s|%d|%s", family, weight, style)
}

// Load registers a font file for a family, weight and style.
func (f *Fonts) Load(family string, weight Weight, italic bool, file string) error {
	raw, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("font %s: %w", file, err)
	}
	font, err := sfnt.Parse(raw)
	if err != nil {
		return fmt.Errorf("font %s is not readable: %w", file, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.faces[key(family, weight, italic)] = &face{
		font:     font,
		upem:     float64(font.UnitsPerEm()),
		advances: map[rune]float64{},
		missing:  map[rune]bool{},
	}
	return nil
}

// face finds the closest registered face.
//
// Falling back rather than failing: a theme asking for a weight it did not ship
// should draw in the nearest one it did, exactly as a browser would, instead of
// refusing to render a CV over a missing italic.
//
// THE ORDER OF THE SEARCH IS FIXED, and that is not a detail. Ranging over the
// map directly picks an arbitrary winner among equally close weights — a
// request for 600 is exactly as far from Medium as from Bold — so the same CV
// measured twice came out with different metrics, and the page it produced
// changed between one run and the next. A renderer whose output depends on map
// iteration order cannot be compared against anything, including itself.
func (f *Fonts) face(family string, weight Weight, italic bool) *face {
	if hit := f.faces[key(family, weight, italic)]; hit != nil {
		return hit
	}
	if italic {
		if hit := f.faces[key(family, weight, false)]; hit != nil {
			return hit
		}
	}
	// Nearest weight in the same family. Ties go to the heavier face above 500
	// and the lighter below, which is the rule a browser follows.
	best, bestDist, bestWeight := (*face)(nil), 1<<30, 0
	for _, k := range f.keys() {
		parts := strings.SplitN(k, "|", 3)
		if parts[0] != family {
			continue
		}
		var w int
		fmt.Sscanf(parts[1], "%d", &w)
		d := abs(w - int(weight))
		better := d < bestDist
		if d == bestDist && best != nil {
			if weight > 500 {
				better = w > bestWeight
			} else {
				better = w < bestWeight
			}
		}
		if better {
			best, bestDist, bestWeight = f.faces[k], d, w
		}
	}
	if best != nil {
		return best
	}
	// No such family at all: any face, but always the SAME any face.
	for _, k := range f.keys() {
		return f.faces[k]
	}
	return nil
}

// keys is every registered face, in a fixed order.
func (f *Fonts) keys() []string {
	out := make([]string, 0, len(f.faces))
	for k := range f.faces {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// advance is the width of one rune, in font units.
func (fc *face) advance(buf *sfnt.Buffer, r rune) float64 {
	if w, ok := fc.advances[r]; ok {
		return w
	}
	if fc.missing[r] {
		return fc.fallbackAdvance(buf)
	}
	idx, err := fc.font.GlyphIndex(buf, r)
	if err != nil || idx == 0 {
		fc.missing[r] = true
		return fc.fallbackAdvance(buf)
	}
	adv, err := fc.font.GlyphAdvance(buf, idx, fc.ppem(), 0)
	if err != nil {
		fc.missing[r] = true
		return fc.fallbackAdvance(buf)
	}
	w := fc.scale(adv)
	fc.advances[r] = w
	return w
}

// fallbackAdvance is what an unmapped rune costs. The space width, because a
// character the font cannot draw still occupies the place of one.
func (fc *face) fallbackAdvance(buf *sfnt.Buffer) float64 {
	if w, ok := fc.advances[' ']; ok {
		return w
	}
	idx, err := fc.font.GlyphIndex(buf, ' ')
	if err != nil {
		return 0.25
	}
	adv, err := fc.font.GlyphAdvance(buf, idx, fc.ppem(), 0)
	if err != nil {
		return 0.25
	}
	fc.advances[' '] = fc.scale(adv)
	return fc.advances[' ']
}

// Width is how wide a string is, in pixels, at a given size.
//
// Kerning is included: dropping it would make every “AV” and “To” pair
// measurably wider here than it draws, and a measurement that disagrees with
// the drawing is the bug this package exists to prevent.
func (f *Fonts) Width(text string, family string, size float64, weight Weight, italic bool, letter float64) float64 {
	if text == "" {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fc := f.face(family, weight, italic)
	if fc == nil {
		return 0
	}

	var em float64
	var prev sfnt.GlyphIndex
	first := true
	count := 0
	for _, r := range text {
		em += fc.advance(&f.buffer, r)
		count++
		idx, err := fc.font.GlyphIndex(&f.buffer, r)
		if err == nil && idx != 0 {
			if !first {
				if k, err := fc.font.Kern(&f.buffer, prev, idx, fc.ppem(), 0); err == nil {
					em += fc.scale(k)
				}
			}
			prev, first = idx, false
		} else {
			first = true
		}
	}
	// Letter-spacing applies between and after each character, as CSS does.
	return em*size + float64(count)*letter
}

// LineMetrics is the vertical shape of a line of text.
type LineMetrics struct {
	// Ascent and Descent from the font, in pixels at this size. Used to place a
	// baseline; the emitters need it to draw text where it was measured.
	Ascent, Descent float64
	// Height is what the font itself recommends between two baselines — what a
	// browser means by `line-height: normal`.
	//
	// It is a property OF THE FONT, not a constant: Roboto asks for 1.172 em,
	// another face asks for something else. Assuming a round 1.2 made every
	// line a little taller than the browser draws it, and the error compounded
	// down a column — the header chip came out 3.4px low, and a block further
	// down proportionally more.
	Height float64
}

// Metrics reads a face's vertical metrics at a size.
func (f *Fonts) Metrics(family string, size float64, weight Weight, italic bool) LineMetrics {
	f.mu.Lock()
	defer f.mu.Unlock()
	fc := f.face(family, weight, italic)
	if fc == nil {
		return LineMetrics{Ascent: size * 0.8, Descent: size * 0.2, Height: size * 1.2}
	}
	m, err := fc.font.Metrics(&f.buffer, fc.ppem(), 0)
	if err != nil {
		return LineMetrics{Ascent: size * 0.8, Descent: size * 0.2, Height: size * 1.2}
	}
	return LineMetrics{
		Ascent:  fc.scale(m.Ascent) * size,
		Descent: fc.scale(m.Descent) * size,
		Height:  fc.scale(m.Height) * size,
	}
}

// Has reports whether any face of a family is loaded.
func (f *Fonts) Has(family string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k := range f.faces {
		if strings.HasPrefix(k, family+"|") {
			return true
		}
	}
	return false
}
