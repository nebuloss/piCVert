// PDF writing: the object model, the cross-reference table, and the trailer.
//
// Written by hand rather than with a library, for the same reason the `.cvz`
// writer is: a CV needs one page, a handful of shapes, some text and two
// embedded files. A dependency that can typeset a book would be a dependency to
// track, and its abstractions would sit between this engine's layout and the
// bytes it wants to emit.
//
// The format is simpler than its reputation. A PDF is a list of numbered
// objects, a table saying where each one starts, and a trailer pointing at the
// table. Everything else is content streams, which are a small stack language.
package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
)

// object is one numbered thing in the file: a dictionary, a stream, a name.
type object struct {
	id      int
	body    string
	stream  []byte
	hasData bool
}

// writer accumulates objects and serialises them.
type writer struct {
	objects []*object
}

// add registers a dictionary object and returns its number.
func (w *writer) add(body string) int {
	o := &object{id: len(w.objects) + 1, body: body}
	w.objects = append(w.objects, o)
	return o.id
}

// addStream registers an object carrying data.
//
// Deflated, because a content stream is repetitive text and an embedded font is
// large; the saving is most of the file. `/Filter /FlateDecode` is understood
// by every reader written since 1996.
func (w *writer) addStream(dict string, data []byte) int {
	var buf bytes.Buffer
	z := zlib.NewWriter(&buf)
	_, _ = z.Write(data)
	_ = z.Close()
	packed := buf.Bytes()

	full := fmt.Sprintf("<< %s /Length %d /Filter /FlateDecode >>", dict, len(packed))
	o := &object{id: len(w.objects) + 1, body: full, stream: packed, hasData: true}
	w.objects = append(w.objects, o)
	return o.id
}

// reserve takes a number now and fills the object in later — needed wherever
// two objects must name each other, as a page and its parent do.
func (w *writer) reserve() int {
	o := &object{id: len(w.objects) + 1}
	w.objects = append(w.objects, o)
	return o.id
}

func (w *writer) fill(id int, body string) {
	w.objects[id-1].body = body
}

// bytes serialises the file.
//
// The cross-reference table is a list of byte offsets, one per object, and it
// has to be exact: a reader seeks straight to them. That is the one part of
// this format with no slack in it, which is why the offsets are recorded while
// writing rather than computed afterwards.
func (w *writer) bytes(rootID, infoID int) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.7\n")
	// A comment of high bytes, which is how a file is detected as binary by
	// tools that would otherwise mangle the line endings in transit.
	out.Write([]byte{'%', 0xE2, 0xE3, 0xCF, 0xD3, '\n'})

	offsets := make([]int, len(w.objects)+1)
	for _, o := range w.objects {
		offsets[o.id] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n", o.id)
		out.WriteString(o.body)
		if o.hasData {
			out.WriteString("\nstream\n")
			out.Write(o.stream)
			out.WriteString("\nendstream")
		}
		out.WriteString("\nendobj\n")
	}

	start := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n", len(w.objects)+1)
	// Object zero is always the head of the free list, and always looks like
	// this. It is not a real object.
	out.WriteString("0000000000 65535 f \n")
	for _, o := range w.objects {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[o.id])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root %d 0 R /Info %d 0 R >>\n",
		len(w.objects)+1, rootID, infoID)
	fmt.Fprintf(&out, "startxref\n%d\n%%%%EOF\n", start)
	return out.Bytes()
}

// escapeString quotes a value for a PDF string literal.
func escapeString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return "(" + r.Replace(s) + ")"
}

// hexString writes a value as UTF-16BE, which is how a PDF carries anything
// beyond ASCII in a text field — a name with an accent in it, for instance.
func hexString(s string) string {
	var b strings.Builder
	b.WriteString("<FEFF")
	for _, r := range s {
		if r > 0xFFFF {
			r = 0xFFFD
		}
		fmt.Fprintf(&b, "%04X", r)
	}
	b.WriteString(">")
	return b.String()
}
