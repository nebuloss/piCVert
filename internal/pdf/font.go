package pdf

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// fixedI is the size sfnt is asked to report metrics at: font units, so the
// numbers come back in the same unit the file stores them in.
func fixedI(v int) fixed.Int26_6 { return fixed.I(v) }

// Face is one embedded font, and what the file needs to know about it.
//
// # WHY THE FONTS TRAVEL INSIDE THE FILE
//
// The layout was computed from these exact files. A PDF that named its fonts
// instead of carrying them would be drawn in whatever the reader's machine had,
// every line would break somewhere other than where it was measured, and the
// one-page guarantee would be a guarantee about a document nobody receives.
type Face struct {
	// Name is how the content stream refers to it: /F1, /F2.
	Name string
	// Family, Weight and Italic are what a style asks for.
	Family string
	Weight int
	Italic bool

	// file names where the bytes came from, for messages only.
	file string
	// raw is what this face was parsed from AND what it embeds. One field, so
	// the two cannot be different files — see ParseFace.
	raw  []byte
	font *sfnt.Font
	upem float64
	// glyphs maps a rune to its glyph index, filled as text is drawn. Only the
	// glyphs actually used are recorded, which is what keeps the widths array
	// and the ToUnicode map small.
	glyphs map[rune]uint16
	// widths holds each used glyph's advance, in thousandths of an em — the
	// unit PDF measures text in.
	widths map[uint16]int
	buf    sfnt.Buffer
}

// LoadFace reads a TrueType file.
//
// It opens the file that will actually be EMBEDDED, not the one beside it. A
// subsetter renumbers glyphs — "G" is index 44 in a full Roboto and 40 in the
// subset — so encoding text against one file and embedding the other draws the
// right number of glyphs in the wrong shapes: a page of plausible-looking
// gibberish, with the metrics still correct because the widths came from the
// same wrong indices.
func LoadFace(name, family string, weight int, italic bool, file string) (*Face, error) {
	embed := embedFileFor(file)
	raw, err := os.ReadFile(embed)
	if err != nil {
		return nil, fmt.Errorf("font %s: %w", embed, err)
	}
	// Through ParseFace, so the path-taking and byte-taking routes cannot hold
	// the invariant differently.
	return ParseFace(name, family, weight, italic, embed, raw)
}

// ParseFace builds a face from bytes somebody else read.
//
// The form the engine uses, because a template's fonts may be in a directory or
// inside the binary and only the registry knows which. The caller has already
// chosen the subset where there is one — see SubsetName.
// ParseFace builds a face from bytes somebody else read.
//
// # THE BYTES ARE KEPT, NOT THE PATH
//
// A face ENCODES text with the font it parsed and EMBEDS the file it names, and
// those two must be the same file: a subsetter renumbers glyphs, so encoding
// against one and embedding another produces a page of plausible gibberish —
// with the metrics still correct, because the widths came from the same wrong
// indices. That is recorded in docs/STATUS.md as a lesson this codebase paid
// for once, and it was paid for a second time here: reading a path at write
// time meant the two could differ, and did.
//
// Holding the bytes makes them the same by construction, and is also the only
// way a font compiled into the binary can be embedded at all — there is no path
// to re-read.
func ParseFace(name, family string, weight int, italic bool, from string, raw []byte) (*Face, error) {
	font, err := sfnt.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("font %s is not readable: %w", from, err)
	}
	return &Face{
		Name: name, Family: family, Weight: weight, Italic: italic,
		file: from, raw: raw, font: font, upem: float64(font.UnitsPerEm()),
		glyphs: map[rune]uint16{}, widths: map[uint16]int{},
	}, nil
}

// SubsetName is the trimmed font beside a full one, by convention.
//
// The PDF embeds the smallest file that draws the text, and the subsets are
// generated beside the originals. Exported because the caller now reads the
// bytes and therefore has to decide which file to read — this states the rule
// once rather than leaving it to be re-derived.
func SubsetName(file string) string {
	return strings.TrimSuffix(file, path.Ext(file)) + ".subset.ttf"
}

// embedFileFor is the file to carry inside the PDF: the subset where one was
// generated, the original otherwise.
//
// A .woff2 cannot go in — the format takes bare TrueType — so the subset is
// written twice by scripts/gen-webfonts.py, once in each wrapping, from a
// single subsetting pass. That is what lets the page and the PDF carry
// literally the same outlines.
func embedFileFor(file string) string {
	subset := strings.TrimSuffix(file, filepath.Ext(file)) + ".subset.ttf"
	if _, err := os.Stat(subset); err == nil {
		return subset
	}
	return file
}

// Encode turns text into the glyph indices a PDF draws, recording what it used.
//
// A PDF does not draw characters; it draws glyphs by index into a font. That is
// why a PDF needs a ToUnicode map to be searchable at all — the file itself has
// no idea what letters it is showing.
func (f *Face) Encode(text string) string {
	var b strings.Builder
	for _, r := range text {
		idx, ok := f.glyphs[r]
		if !ok {
			g, err := f.font.GlyphIndex(&f.buf, r)
			if err != nil || g == 0 {
				// No glyph: a space keeps the line the length it was measured
				// to be, which matters more than showing the character.
				g, _ = f.font.GlyphIndex(&f.buf, ' ')
			}
			idx = uint16(g)
			f.glyphs[r] = idx
			if adv, err := f.font.GlyphAdvance(&f.buf, sfnt.GlyphIndex(idx),
				fixedI(int(f.upem)), 0); err == nil {
				f.widths[idx] = int(float64(adv) / 64.0 / f.upem * 1000)
			}
		}
		fmt.Fprintf(&b, "%04X", idx)
	}
	return b.String()
}

// write emits the font objects and returns the reference to the font resource.
//
// A composite (Type0) font, because a simple one can only address 256 glyphs
// and a CV in French with typographic punctuation passes that easily. The
// two-byte encoding is also what lets Encode above write glyph indices
// directly.
//
// The file embedded is the WEB font next to the TTF where one exists — the same
// subset the HTML page carries, of the same outlines. Embedding the full faces
// made a CV four times the size of the one the previous engine produced: 185 KB
// per face against about 12 KB, for a document that uses a hundred glyphs of
// the several thousand a complete Roboto holds. The page and the PDF then also
// carry literally the same bytes, which is one fewer thing that can differ.
func (f *Face) write(w *writer) int {
	// The bytes this face was PARSED from, never a re-read. See ParseFace.
	raw := f.raw
	if raw == nil {
		var err error
		raw, err = os.ReadFile(f.file)
		if err != nil {
			return 0
		}
	}
	fileID := w.addStream(fmt.Sprintf("/Length1 %d", len(raw)), raw)

	used := make([]uint16, 0, len(f.widths))
	for g := range f.widths {
		used = append(used, g)
	}
	sort.Slice(used, func(i, j int) bool { return used[i] < used[j] })

	// The widths array, in the runs the format expects: `<first> [<w> <w> …]`.
	var widths strings.Builder
	widths.WriteString("[")
	for i := 0; i < len(used); {
		j := i
		for j+1 < len(used) && used[j+1] == used[j]+1 {
			j++
		}
		fmt.Fprintf(&widths, "%d [", used[i])
		for k := i; k <= j; k++ {
			if k > i {
				widths.WriteByte(' ')
			}
			fmt.Fprintf(&widths, "%d", f.widths[used[k]])
		}
		widths.WriteString("] ")
		i = j + 1
	}
	widths.WriteString("]")

	m, _ := f.font.Metrics(&f.buf, fixedI(int(f.upem)), 0)
	scale := func(v int) int { return int(float64(v) / 64.0 / f.upem * 1000) }

	flags := 4 // symbolic: the font supplies its own encoding
	if f.Italic {
		flags |= 64
	}
	descriptor := w.add(fmt.Sprintf(
		"<< /Type /FontDescriptor /FontName /%s /Flags %d "+
			"/FontBBox [-1000 %d 2000 %d] /ItalicAngle %d /Ascent %d /Descent %d "+
			"/CapHeight %d /StemV 80 /FontFile2 %d 0 R >>",
		f.postScriptName(), flags,
		-scale(int(m.Descent)), scale(int(m.Ascent)),
		italicAngle(f.Italic), scale(int(m.Ascent)), -scale(int(m.Descent)),
		scale(int(m.CapHeight)), fileID))

	toUnicode := w.addStream("", []byte(f.toUnicodeCMap()))

	descendant := w.add(fmt.Sprintf(
		"<< /Type /Font /Subtype /CIDFontType2 /BaseFont /%s "+
			"/CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> "+
			"/FontDescriptor %d 0 R /DW 1000 /W %s /CIDToGIDMap /Identity >>",
		f.postScriptName(), descriptor, widths.String()))

	return w.add(fmt.Sprintf(
		"<< /Type /Font /Subtype /Type0 /BaseFont /%s /Encoding /Identity-H "+
			"/DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>",
		f.postScriptName(), descendant, toUnicode))
}

func italicAngle(italic bool) int {
	if italic {
		return -12
	}
	return 0
}

func (f *Face) postScriptName() string {
	name := strings.ReplaceAll(f.Family, " ", "")
	switch {
	case f.Italic:
		return name + "-Italic"
	case f.Weight >= 700:
		return name + "-Bold"
	case f.Weight >= 500:
		return name + "-Medium"
	}
	return name + "-Regular"
}

// toUnicodeCMap is what makes the text searchable and copyable.
//
// Without it a PDF is a picture of words: the glyph indices mean nothing
// outside the font, so selecting a line yields gibberish and Ctrl-F finds
// nothing. It is thirty lines of boilerplate and the difference between a
// document and an image of one.
func (f *Face) toUnicodeCMap() string {
	type pair struct {
		glyph uint16
		r     rune
	}
	pairs := make([]pair, 0, len(f.glyphs))
	for r, g := range f.glyphs {
		pairs = append(pairs, pair{g, r})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].glyph < pairs[j].glyph })

	var b strings.Builder
	b.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n")
	b.WriteString("/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n")
	b.WriteString("/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n")
	b.WriteString("1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")

	// In blocks of 100: the format's own limit.
	for i := 0; i < len(pairs); i += 100 {
		end := i + 100
		if end > len(pairs) {
			end = len(pairs)
		}
		fmt.Fprintf(&b, "%d beginbfchar\n", end-i)
		for _, p := range pairs[i:end] {
			if p.r > 0xFFFF {
				fmt.Fprintf(&b, "<%04X> <FFFD>\n", p.glyph)
				continue
			}
			fmt.Fprintf(&b, "<%04X> <%04X>\n", p.glyph, p.r)
		}
		b.WriteString("endbfchar\n")
	}
	b.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	return b.String()
}
