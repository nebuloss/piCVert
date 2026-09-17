package layout

import "strings"

// Line is one line of text, already broken, with its pieces and its width.
//
// THE POINT OF THIS TYPE. Once a paragraph is a list of these, no renderer has
// a wrapping decision left to make: the HTML emitter places them, the PDF
// emitter draws them, and neither can reach a different answer. The
// disagreement that clipped CVs lived exactly in this decision, and this is
// where it is now taken once.
type Line struct {
	Pieces []Piece
	Width  float64
}

// Piece is a run of one style within a line.
type Piece struct {
	Text   string
	Bold   bool
	Colour string
	Width  float64
}

// Text returns the line's text, for emitters that need it whole.
func (l Line) Text() string {
	var b strings.Builder
	for _, p := range l.Pieces {
		b.WriteString(p.Text)
	}
	return b.String()
}

// token is a word or a run of spaces, kept apart so a break can fall between
// them and the spaces can be dropped at the end of a line.
type token struct {
	text  string
	space bool
	bold  bool
	col   string
	width float64
}

// tokenise splits spans into words and gaps, measuring each.
//
// Measured per token rather than per line, so a word's width is computed once
// however many candidate lines it is tried on. A paragraph of forty words is
// measured forty times, not forty times per attempt.
func tokenise(spans []Span, s Style, f *Fonts) []token {
	var out []token
	for _, span := range spans {
		text := span.Text
		if s.Uppercase {
			text = strings.ToUpper(text)
		}
		// Measured in the face it will be DRAWN in, or the line breaks where it
		// is not drawn.
		weight := s.WeightOf(span.Bold)
		colour := span.Colour
		for _, chunk := range splitKeepingSpaces(text) {
			isSpace := strings.TrimSpace(chunk) == ""
			out = append(out, token{
				text:  chunk,
				space: isSpace,
				bold:  span.Bold,
				col:   colour,
				width: f.Width(chunk, s.Family, s.Size, weight, s.Italic, s.Letter),
			})
		}
	}
	return out
}

// splitKeepingSpaces cuts a string into alternating runs of spaces and
// non-spaces, keeping both. The spaces are needed to measure a line's interior
// and must be droppable at its end.
func splitKeepingSpaces(text string) []string {
	var out []string
	var cur strings.Builder
	inSpace := false
	for i, r := range text {
		isSpace := r == ' ' || r == '\t' || r == '\n' || r == '\u00a0'
		if i > 0 && isSpace != inSpace {
			out = append(out, cur.String())
			cur.Reset()
		}
		cur.WriteRune(r)
		inSpace = isSpace
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// breakText wraps a paragraph to a width.
//
// Greedy — first fit — because that is what browsers do for ordinary text, and
// the HTML emitter's output has to agree with what a browser WOULD have done if
// anyone re-flowed it. Knuth-Plass would set better paragraphs and would put
// this engine at odds with the medium it emits into.
//
// A word wider than the whole line is not broken: it overflows its box, which
// is visible, rather than being cut mid-word, which looks like data loss.
func breakText(spans []Span, s Style, width float64, f *Fonts) []Line {
	tokens := tokenise(spans, s, f)
	if len(tokens) == 0 {
		return nil
	}

	var lines []Line
	var cur Line
	// pendingSpace holds the gap between two words: it is only added if another
	// word follows on the same line, so a line never ends with trailing spaces
	// it paid for.
	var pending []token
	var pendingWidth float64

	flush := func() {
		if len(cur.Pieces) > 0 {
			lines = append(lines, cur)
		}
		cur = Line{}
		pending, pendingWidth = nil, 0
	}

	add := func(t token) {
		// Merged into the previous piece when the style matches, so a line is a
		// handful of runs rather than one per word.
		if n := len(cur.Pieces); n > 0 &&
			cur.Pieces[n-1].Bold == t.bold && cur.Pieces[n-1].Colour == t.col {
			cur.Pieces[n-1].Text += t.text
			cur.Pieces[n-1].Width += t.width
		} else {
			cur.Pieces = append(cur.Pieces, Piece{Text: t.text, Bold: t.bold, Colour: t.col, Width: t.width})
		}
		cur.Width += t.width
	}

	for _, t := range tokens {
		if t.space {
			if len(cur.Pieces) > 0 {
				pending = append(pending, t)
				pendingWidth += t.width
			}
			continue
		}
		// Does the word fit, with the gap that would precede it?
		if len(cur.Pieces) > 0 && width > 0 && cur.Width+pendingWidth+t.width > width+0.01 {
			flush()
		} else {
			for _, sp := range pending {
				add(sp)
			}
			pending, pendingWidth = nil, 0
		}
		add(t)
	}
	flush()
	return lines
}
