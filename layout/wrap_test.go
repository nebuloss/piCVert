// The test that decides whether this engine is worth having.
//
// The whole point of computing the layout once is that the browser and the PDF
// stop disagreeing about where paragraphs break. That claim is only worth
// anything if THIS engine's breaks match what a browser actually does with the
// same text, in the same font, at the same width — otherwise the disagreement
// has merely moved.
//
// So the reference is produced by a real browser, and checked in. See
// testdata/README.md for how to regenerate it. When it is absent the comparison
// is SKIPPED rather than passed: a guard that quietly succeeds without its
// evidence is worse than no guard.
package layout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func loadTestFonts(t *testing.T) *Fonts {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "fonts")
	f := NewFonts()
	for _, face := range []struct {
		weight Weight
		italic bool
		file   string
	}{
		{Regular, false, "Roboto-Regular.ttf"},
		{Medium, false, "Roboto-Medium.ttf"},
		{Bold, false, "Roboto-Bold.ttf"},
		{Regular, true, "Roboto-Italic.ttf"},
	} {
		if err := f.Load("Roboto", face.weight, face.italic, filepath.Join(dir, face.file)); err != nil {
			t.Skipf("fonts unavailable: %v", err)
		}
	}
	return f
}

// wrapCase is one paragraph a browser has already measured for us.
type wrapCase struct {
	Text   string   `json:"text"`
	Family string   `json:"family"`
	Size   float64  `json:"size"`
	Weight int      `json:"weight"`
	Width  float64  `json:"width"`
	Lines  []string `json:"lines"`
}

// TestLineBreaksAreNeverOptimistic is the load-bearing test of this package.
//
// It does NOT demand that our breaks match a browser's exactly, and the reason
// is worth stating, because the obvious stricter test would be wrong.
//
// Chrome truncates the font size to 1/64 px before scaling: asked for 10.6 px
// it draws at 678/64 = 10.59375. Verified against the font's own metrics —
// 1086/2048 × 10.59375 = 5.6180 px, which is exactly what the browser reports
// for “e”, against our exact 5.6209. So a browser draws text about 0.06 %
// NARROWER than this engine measures it, and the gap is an implementation
// detail of one renderer: Firefox and Safari quantise differently, and the PDF
// not at all.
//
// Chasing it would mean reproducing Chrome's rounding — matching one browser at
// the cost of the other two and of the PDF. The property actually worth holding
// is one-sided: we may measure text as wider than it will be drawn, never
// narrower. Then a line that this engine says fits always fits, and the fit
// check errs towards telling someone to shorten a CV that would in fact have
// held — an annoyance, where the opposite is a CV silently cut off.
//
// So: our line count is allowed to exceed the browser's, and never to fall
// below it.
func TestLineBreaksAreNeverOptimistic(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "wrapping.json"))
	if err != nil {
		t.Skip("no browser reference: see testdata/README.md to regenerate it")
	}
	var cases []wrapCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("reference unreadable: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("reference is empty")
	}

	f := loadTestFonts(t)
	agreed := 0
	for i, c := range cases {
		style := Style{
			Family: c.Family, Size: c.Size, Weight: Weight(c.Weight),
			LineHeight: 1.4,
		}
		got := breakText([]Span{{Text: c.Text}}, style, c.Width, f)

		// Fewer lines than the browser would mean we fitted text the browser
		// could not — the optimistic direction, and the one that clips a CV.
		if len(got) < len(c.Lines) {
			t.Errorf("case %d (%.0fpx wide, %gpx text): we made %d lines, the browser needed %d — "+
				"we fitted text it could not\n  ours:    %q\n  browser: %q",
				i, c.Width, c.Size, len(got), len(c.Lines), first(got), firstOf(c.Lines))
			continue
		}
		// More than one line of slack means the measurement has drifted, not
		// merely rounded.
		if len(got) > len(c.Lines)+1 {
			t.Errorf("case %d: %d lines against the browser's %d — too far apart to be rounding",
				i, len(got), len(c.Lines))
			continue
		}
		if len(got) == len(c.Lines) {
			agreed++
		}
	}
	t.Logf("%d/%d paragraphs broken exactly as the browser breaks them; "+
		"the rest are one line more, which is the safe direction", agreed, len(cases))
}

func first(lines []Line) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0].Text()
}

func firstOf(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

func trimTrailing(s string) string {
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\n' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// --- properties that hold without the reference -----------------------------

// Kerning must be in the measurement, because it is in the drawing. A width
// computed without it is wider than what appears, and the break lands early.
func TestWidthIncludesKerning(t *testing.T) {
	f := loadTestFonts(t)
	// A pair the font kerns, against one it does not.
	kerned := f.Width("AV", "Roboto", 20, Regular, false, 0)
	apart := f.Width("A", "Roboto", 20, Regular, false, 0) +
		f.Width("V", "Roboto", 20, Regular, false, 0)
	if kerned >= apart {
		t.Errorf("AV measured %.3f, the two letters apart measure %.3f — kerning is missing",
			kerned, apart)
	}
}

func TestWidthIsProportionalToSize(t *testing.T) {
	f := loadTestFonts(t)
	at10 := f.Width("Ingénieur", "Roboto", 10, Regular, false, 0)
	at20 := f.Width("Ingénieur", "Roboto", 20, Regular, false, 0)
	if diff := at20 - 2*at10; diff > 0.01 || diff < -0.01 {
		t.Errorf("doubling the size gave %.4f, expected %.4f", at20, 2*at10)
	}
}

// Bold is measured in the bold face. Measuring it in the regular one is how a
// line that holds in the measurement overflows on the page.
func TestBoldIsMeasuredInTheBoldFace(t *testing.T) {
	f := loadTestFonts(t)
	regular := f.Width("Développeur", "Roboto", 12, Regular, false, 0)
	bold := f.Width("Développeur", "Roboto", 12, Bold, false, 0)
	if bold <= regular {
		t.Errorf("bold measured %.2f, regular %.2f — the bold face is not being used", bold, regular)
	}
}

// A word wider than its column overflows rather than being cut: an overflow is
// visible and fixable, a silent truncation looks like lost data.
func TestOverlongWordIsNotCut(t *testing.T) {
	f := loadTestFonts(t)
	style := Style{Family: "Roboto", Size: 10, Weight: Regular}
	lines := breakText([]Span{{Text: "Reimplementierungsbeauftragter"}}, style, 20, f)
	if len(lines) != 1 {
		t.Fatalf("expected the word to stay whole on one line, got %d lines", len(lines))
	}
	if lines[0].Text() != "Reimplementierungsbeauftragter" {
		t.Errorf("the word was altered: %q", lines[0].Text())
	}
}

// A line never keeps the space that would have followed its last word: the
// space is what the break consumed.
func TestTrailingSpacesAreDropped(t *testing.T) {
	f := loadTestFonts(t)
	style := Style{Family: "Roboto", Size: 10, Weight: Regular}
	width := f.Width("alpha beta", "Roboto", 10, Regular, false, 0)
	lines := breakText([]Span{{Text: "alpha beta gamma"}}, style, width, f)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if got := lines[0].Text(); got != "alpha beta" {
		t.Errorf("first line = %q, want %q", got, "alpha beta")
	}
}

// Bold inside a sentence must stay part of the paragraph, or the line would
// break at every style change.
func TestBoldRunsStayInTheSameParagraph(t *testing.T) {
	f := loadTestFonts(t)
	style := Style{Family: "Roboto", Size: 10, Weight: Regular}
	lines := breakText([]Span{
		{Text: "conception et "},
		{Text: "développement", Bold: true},
		{Text: " de services"},
	}, style, 1000, f)
	if len(lines) != 1 {
		t.Fatalf("expected one line, got %d", len(lines))
	}
	if got := lines[0].Text(); got != "conception et développement de services" {
		t.Errorf("text = %q", got)
	}
	if len(lines[0].Pieces) < 3 {
		t.Errorf("expected the bold run to remain its own piece, got %d pieces", len(lines[0].Pieces))
	}
}
