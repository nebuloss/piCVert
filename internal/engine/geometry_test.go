package engine

import (
	"bytes"
	"compress/zlib"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestPDFDrawsNoTextOverText reads the GEOMETRY of the PDF, not its text.
//
// # WHY A TEXT CHECK CANNOT SEE THIS
//
// A PDF whose runs are drawn on top of one another extracts perfectly: every
// character is there, in order, with its spaces. Only the ink is wrong. So a
// real overlap of up to 4.2 pt — an upright run measured in the italic face,
// drawn 3 % wider than the width the next run was placed at — shipped in a
// release past every check that read a PDF rather than looked at one.
//
// This walks the content stream the engine itself wrote, takes each run's
// start from its text matrix and its width from the advances in the embedded
// font's own /W array, and asks whether any run begins inside the one before
// it on the same baseline. It needs no external tool: the same question
// `scripts/pdf-overlap.py` asks of mutool's output, asked of the bytes.
func TestPDFDrawsNoTextOverText(t *testing.T) {
	root := repoRoot(t)
	profile := filepath.Join(root, "examples", "jean-dupont")

	e := New(root)
	doc, err := ReadDoc(filepath.Join(profile, "cv.json"))
	if err != nil {
		t.Fatalf("read document: %v", err)
	}
	page, err := e.Prepare(doc, profile)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	raw, err := e.PDF(page, "", nil)
	if err != nil {
		t.Fatalf("pdf: %v", err)
	}

	runs := textRuns(t, raw)
	if len(runs) < 50 {
		// A probe that measures nothing reports zero: the population is part
		// of the result.
		t.Fatalf("only %d text runs found — the probe is not reading the page", len(runs))
	}

	byLine := map[string][]drawnRun{}
	for _, r := range runs {
		byLine[strconv.FormatFloat(r.y, 'f', 2, 64)] = append(byLine[strconv.FormatFloat(r.y, 'f', 2, 64)], r)
	}

	// Adjacent runs legitimately touch; only a real overlap is a fault.
	const tolerance = 0.25
	overlaps := 0
	for _, line := range byLine {
		sort.Slice(line, func(i, j int) bool { return line[i].x < line[j].x })
		for i := 1; i < len(line); i++ {
			prev := line[i-1]
			if line[i].x < prev.x+prev.width-tolerance {
				overlaps++
				t.Errorf("%.2f pt of %q is drawn over by %q (baseline %.2f)",
					prev.x+prev.width-line[i].x, prev.text, line[i].text, prev.y)
			}
		}
	}
	if overlaps == 0 {
		t.Logf("%d runs on %d baselines, none overlapping", len(runs), len(byLine))
	}
}

// drawnRun is one Tj: where it starts, how wide it is drawn, what it says.
type drawnRun struct {
	x, y, width float64
	text        string
}

// embedded is one font the PDF carries, as a reader sees it: what each glyph
// advances, and what each glyph says.
type embedded struct {
	widths map[int]float64
	text   map[int]rune
}

var (
	objectRE     = regexp.MustCompile(`(?s)(\d+) 0 obj(.*?)endobj`)
	fontRefRE    = regexp.MustCompile(`/(F\d+) (\d+) 0 R`)
	descendantRE = regexp.MustCompile(`/DescendantFonts \[(\d+) 0 R\]`)
	toUnicodeRE  = regexp.MustCompile(`/ToUnicode (\d+) 0 R`)
	bfcharRE     = regexp.MustCompile(`<([0-9A-Fa-f]{4})> <([0-9A-Fa-f]{4})>`)
	widthsRE     = regexp.MustCompile(`(?s)/W \[(.*?)\] /CIDToGIDMap`)
	widthRunRE   = regexp.MustCompile(`(\d+) \[([^\]]*)\]`)
	tfRE         = regexp.MustCompile(`/(F\d+) ([-\d.]+) Tf`)
	tcRE         = regexp.MustCompile(`^([-\d.]+) Tc$`)
	tmRE         = regexp.MustCompile(`^1 0 0 1 ([-\d.]+) ([-\d.]+) Tm$`)
	tjRE         = regexp.MustCompile(`^<([0-9A-Fa-f]*)> Tj$`)
)

// textRuns replays the content stream the painter wrote.
//
// The widths come from the file's own /W array, which is what a reader draws
// with — so this measures what the PDF SAYS, never what the engine believed.
func textRuns(t *testing.T, raw []byte) []drawnRun {
	t.Helper()
	objects := map[string]string{}
	for _, m := range objectRE.FindAllStringSubmatch(string(raw), -1) {
		objects[m[1]] = m[2]
	}

	fonts := map[string]embedded{}
	for _, body := range objects {
		for _, ref := range fontRefRE.FindAllStringSubmatch(body, -1) {
			font := objects[ref[2]]
			if !strings.Contains(font, "/Type0") {
				continue
			}
			d := descendantRE.FindStringSubmatch(font)
			if d == nil {
				continue
			}
			face := embedded{widths: parseWidths(objects[d[1]]), text: map[int]rune{}}
			if u := toUnicodeRE.FindStringSubmatch(font); u != nil {
				for _, pair := range bfcharRE.FindAllStringSubmatch(inflate(objects[u[1]]), -1) {
					glyph, _ := strconv.ParseInt(pair[1], 16, 32)
					r, _ := strconv.ParseInt(pair[2], 16, 32)
					face.text[int(glyph)] = rune(r)
				}
			}
			fonts[ref[1]] = face
		}
	}
	if len(fonts) == 0 {
		t.Fatal("no embedded fonts found in the PDF")
	}

	var runs []drawnRun
	var font string
	var size, letter, x, y float64
	for _, line := range strings.Split(contentStream(t, raw), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case tfRE.MatchString(line):
			m := tfRE.FindStringSubmatch(line)
			font, size = m[1], toFloat(m[2])
		case tcRE.MatchString(line):
			letter = toFloat(tcRE.FindStringSubmatch(line)[1])
		case tmRE.MatchString(line):
			m := tmRE.FindStringSubmatch(line)
			x, y = toFloat(m[1]), toFloat(m[2])
		case tjRE.MatchString(line):
			hex := tjRE.FindStringSubmatch(line)[1]
			face, ok := fonts[font]
			if !ok {
				t.Fatalf("text drawn in %s, which the PDF does not embed", font)
			}
			var width float64
			var said strings.Builder
			for i := 0; i+4 <= len(hex); i += 4 {
				g, err := strconv.ParseInt(hex[i:i+4], 16, 32)
				if err != nil {
					t.Fatalf("unreadable glyph %q: %v", hex[i:i+4], err)
				}
				adv, known := face.widths[int(g)]
				if !known {
					// /DW: the default width a reader falls back to.
					adv = 1000
				}
				width += adv/1000*size + letter
				said.WriteRune(face.text[int(g)])
			}
			runs = append(runs, drawnRun{x: x, y: y, width: width, text: said.String()})
		}
	}
	return runs
}

// inflate unpacks an object's stream, or returns nothing if it has none.
func inflate(object string) string {
	body := []byte(object)
	start := bytes.Index(body, []byte("stream\n"))
	end := bytes.LastIndex(body, []byte("endstream"))
	if start < 0 || end < start {
		return ""
	}
	r, err := zlib.NewReader(bytes.NewReader(body[start+len("stream\n") : end]))
	if err != nil {
		return ""
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return ""
	}
	return string(out)
}

// contentStream inflates the one stream that draws the page.
func contentStream(t *testing.T, raw []byte) string {
	t.Helper()
	for _, m := range objectRE.FindAllSubmatchIndex(raw, -1) {
		body := raw[m[4]:m[5]]
		start := bytes.Index(body, []byte("stream\n"))
		end := bytes.LastIndex(body, []byte("endstream"))
		if start < 0 || end < start {
			continue
		}
		r, err := zlib.NewReader(bytes.NewReader(body[start+len("stream\n") : end]))
		if err != nil {
			continue
		}
		out, err := io.ReadAll(r)
		r.Close()
		if err == nil && bytes.Contains(out, []byte(" Tj\n")) {
			return string(out)
		}
	}
	t.Fatal("no content stream with text in the PDF")
	return ""
}

// parseWidths reads `/W [ 3 [508 528] 40 [1261] ]`.
func parseWidths(descendant string) map[int]float64 {
	out := map[int]float64{}
	m := widthsRE.FindStringSubmatch(descendant)
	if m == nil {
		return out
	}
	for _, run := range widthRunRE.FindAllStringSubmatch(m[1], -1) {
		first, err := strconv.Atoi(run[1])
		if err != nil {
			continue
		}
		for i, field := range strings.Fields(run[2]) {
			out[first+i] = toFloat(field)
		}
	}
	return out
}

func toFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// repoRoot is where templates/ and fonts/ live, from wherever `go test` runs.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		// go.mod rather than templates/: internal/templates is a package, and
		// a search for a directory of that name stops one level too low.
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			t.Fatal("no repository root above the test")
		}
		dir = up
	}
}
