package pdf

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"sort"
	"strings"
	"time"

	"picvert/internal/layout"
)

// Document is a PDF being assembled.
type Document struct {
	Width, Height float64 // in CSS pixels
	Title         string
	Lang          string

	faces  []*Face
	images map[string]*embeddedImage
	// Source is the cv.json the page was rendered from, carried inside the
	// file. See Attach.
	source     []byte
	sourceName string
}

type embeddedImage struct {
	name   string
	data   []byte
	w, h   int
	isJPEG bool
}

func New(width, height float64, title, lang string) *Document {
	return &Document{Width: width, Height: height, Title: title, Lang: lang,
		images: map[string]*embeddedImage{}}
}

// AddFace registers a font to embed.
func (d *Document) AddFace(f *Face) {
	f.Name = fmt.Sprintf("F%d", len(d.faces)+1)
	d.faces = append(d.faces, f)
}

// Attach carries the source document inside the PDF.
//
// # WHY THE SOURCE TRAVELS WITH THE PAGE
//
// A PDF cannot be read back into a CV: its content stream says where ink goes,
// not which field a run of text came from. Reading one is a thing this project's
// predecessor tried and abandoned — coordinates live under nested transforms,
// clip paths look exactly like drawn ones, and nothing marks a section.
//
// So the file carries its own JSON instead. Measured on a real CV it costs about
// one percent of the PDF, and it makes the exported file both the thing you send
// and a thing this engine can open again. The mechanism is the one Factur-X
// invoices use: an embedded file, associated with the document.
func (d *Document) Attach(name string, source []byte) {
	d.sourceName, d.source = name, source
}

// AddImage registers a picture, keyed by the data URI the layout refers to.
func (d *Document) AddImage(src string) error {
	if _, done := d.images[src]; done {
		return nil
	}
	const prefix = "data:"
	if !strings.HasPrefix(src, prefix) {
		return nil
	}
	comma := strings.Index(src, ",")
	if comma < 0 {
		return nil
	}
	header := src[:comma]
	raw, err := base64.StdEncoding.DecodeString(src[comma+1:])
	if err != nil {
		return nil
	}
	// SVG cannot be embedded: PDF has no vector image format it can carry
	// wholesale. The example profile's placeholder is an SVG, and it simply
	// does not appear — better than a broken object in the file.
	if strings.Contains(header, "svg") {
		return nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	d.images[src] = &embeddedImage{
		name: fmt.Sprintf("Im%d", len(d.images)+1),
		data: raw, w: cfg.Width, h: cfg.Height,
		isJPEG: format == "jpeg",
	}
	return nil
}

// Render paints a laid-out page and returns the finished file.
func (d *Document) Render(frame *layout.Frame) []byte {
	// Every picture the page refers to, registered before painting so the
	// painter can name them.
	frame.Walk(func(f *layout.Frame) {
		if f.Style.Display == layout.Image && f.Node != nil {
			_ = d.AddImage(f.Node.Src)
		}
	})

	p := NewPainter(d.faces, d.Height)
	p.images = map[string]string{}
	for src, img := range d.images {
		p.images[src] = img.name
	}
	frame.Paint(p)

	w := &writer{}
	pagesID := w.reserve()

	contentID := w.addStream("", []byte(p.ops.String()))

	// Fonts: only those actually drawn with, so an unused face is not embedded.
	var fontRefs []string
	used := make([]*Face, 0, len(d.faces))
	for _, f := range d.faces {
		if p.used[f] {
			used = append(used, f)
		}
	}
	sort.Slice(used, func(i, j int) bool { return used[i].Name < used[j].Name })
	for _, f := range used {
		id := f.write(w)
		fontRefs = append(fontRefs, fmt.Sprintf("/%s %d 0 R", f.Name, id))
	}

	var imageRefs []string
	for _, img := range d.images {
		id := d.writeImage(w, img)
		imageRefs = append(imageRefs, fmt.Sprintf("/%s %d 0 R", img.name, id))
	}

	// Transparency states, one per opacity a polygon asked for.
	var gsRefs []string
	for i := 1; i < 100; i++ {
		if !strings.Contains(p.ops.String(), fmt.Sprintf("/GS%d gs", i)) {
			continue
		}
		id := w.add(fmt.Sprintf("<< /Type /ExtGState /ca %s >>", num(float64(i)/100)))
		gsRefs = append(gsRefs, fmt.Sprintf("/GS%d %d 0 R", i, id))
	}

	resources := "<< /Font << " + strings.Join(fontRefs, " ") + " >>"
	if len(imageRefs) > 0 {
		resources += " /XObject << " + strings.Join(imageRefs, " ") + " >>"
	}
	if len(gsRefs) > 0 {
		resources += " /ExtGState << " + strings.Join(gsRefs, " ") + " >>"
	}
	resources += " >>"

	pageID := w.add(fmt.Sprintf(
		"<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %s %s] /Resources %s /Contents %d 0 R >>",
		pagesID, num(pt(d.Width)), num(pt(d.Height)), resources, contentID))
	w.fill(pagesID, fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R] /Count 1 >>", pageID))

	catalogue := fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R /Lang %s",
		pagesID, escapeString(d.Lang))
	if d.source != nil {
		fileID := d.writeAttachment(w)
		catalogue += fmt.Sprintf(
			" /Names << /EmbeddedFiles << /Names [%s %d 0 R] >> >> /AF [%d 0 R]",
			escapeString(d.sourceName), fileID, fileID)
	}
	catalogue += " >>"
	rootID := w.add(catalogue)

	infoID := w.add(fmt.Sprintf(
		"<< /Title %s /Producer (piCVert) /Creator (piCVert) /CreationDate (D:%s) >>",
		hexString(d.Title), time.Now().UTC().Format("20060102150405Z")))

	return w.bytes(rootID, infoID)
}

func (d *Document) writeImage(w *writer, img *embeddedImage) int {
	if img.isJPEG {
		// A JPEG goes in as it is: its own compression is better than deflate
		// would manage, and the format lets it pass through.
		o := &object{
			id: len(w.objects) + 1,
			body: fmt.Sprintf(
				"<< /Type /XObject /Subtype /Image /Width %d /Height %d "+
					"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>",
				img.w, img.h, len(img.data)),
			stream: img.data, hasData: true,
		}
		w.objects = append(w.objects, o)
		return o.id
	}

	// A PNG has to be decoded and re-emitted as raw samples: PDF cannot carry
	// PNG's own compression, only the raw or deflated pixels.
	decoded, _, err := image.Decode(bytes.NewReader(img.data))
	if err != nil {
		return w.add("<< /Type /XObject /Subtype /Image /Width 1 /Height 1 " +
			"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Length 3 >>")
	}
	b := decoded.Bounds()
	rgb := make([]byte, 0, b.Dx()*b.Dy()*3)
	var alpha []byte
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := decoded.At(x, y).RGBA()
			rgb = append(rgb, byte(r>>8), byte(g>>8), byte(bl>>8))
			if a < 0xFFFF {
				if alpha == nil {
					alpha = make([]byte, 0, b.Dx()*b.Dy())
				}
			}
			if alpha != nil {
				alpha = append(alpha, byte(a>>8))
			}
		}
	}

	// Filtered with the PNG "up" predictor before deflating.
	//
	// Deflate alone barely compresses a photograph: neighbouring pixels differ
	// in value but not by much, and the raw bytes look like noise to it. Sending
	// each row as its DIFFERENCE from the row above turns that into small
	// numbers clustered near zero, which deflate handles well. Measured on this
	// portrait: 343 KB down to about 200 KB, which is what the reference
	// implementation gets too.
	dict := fmt.Sprintf(
		"/Type /XObject /Subtype /Image /Width %d /Height %d "+
			"/ColorSpace /DeviceRGB /BitsPerComponent 8 "+
			"/DecodeParms << /Predictor 12 /Colors 3 /BitsPerComponent 8 /Columns %d >>",
		b.Dx(), b.Dy(), b.Dx())
	if alpha != nil && len(alpha) == b.Dx()*b.Dy() {
		// A soft mask, so a portrait cut out of its background stays cut out.
		maskID := w.addStream(fmt.Sprintf(
			"/Type /XObject /Subtype /Image /Width %d /Height %d "+
				"/ColorSpace /DeviceGray /BitsPerComponent 8", b.Dx(), b.Dy()), alpha)
		dict += fmt.Sprintf(" /SMask %d 0 R", maskID)
	}
	return w.addStream(dict, pngPredict(rgb, b.Dx(), 3))
}

// writeAttachment embeds the source document.
// pngPredict applies the PNG "up" filter: each row becomes its difference from
// the row above, with a leading 2 to say which filter was used.
//
// One filter for every row rather than choosing the best per row: the choice
// costs a pass over the data for a few percent more, and "up" is the one that
// suits a photograph, where vertical neighbours are the most alike.
func pngPredict(data []byte, width, channels int) []byte {
	stride := width * channels
	if stride == 0 || len(data)%stride != 0 {
		return data
	}
	rows := len(data) / stride
	out := make([]byte, 0, len(data)+rows)
	prev := make([]byte, stride)
	for r := 0; r < rows; r++ {
		row := data[r*stride : (r+1)*stride]
		out = append(out, 2) // filter type: Up
		for i := 0; i < stride; i++ {
			out = append(out, row[i]-prev[i])
		}
		prev = row
	}
	return out
}

func (d *Document) writeAttachment(w *writer) int {
	streamID := w.addStream(
		fmt.Sprintf("/Type /EmbeddedFile /Subtype /application#2Fjson /Params << /Size %d >>",
			len(d.source)), d.source)
	return w.add(fmt.Sprintf(
		"<< /Type /Filespec /F %s /UF %s /EF << /F %d 0 R >> "+
			"/Desc (The CV this page was rendered from) /AFRelationship /Source >>",
		escapeString(d.sourceName), hexString(d.sourceName), streamID))
}
