// Package engine is the pipeline, from a document on disk to a page.
//
// It exists so that there is ONE of it. The command line, the preview server
// and the service all render CVs, and each of them having assembled the
// pipeline itself is how `fit` came to measure something slightly different
// from what `render` drew. Everything here is reachable from every caller, and
// no caller composes the steps in its own order.
package engine

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"picvert/internal/document"
	"picvert/internal/layout"
	"picvert/internal/pdf"
	"picvert/internal/templates"
	"picvert/internal/theme"
)

// Engine holds what is expensive to build and safe to share.
type Engine struct {
	// Home is where templates/ and fonts/ live.
	Home     string
	Registry *templates.Registry

	// faces caches the parsed fonts of each template.
	//
	// A CV renders in a few milliseconds and parsing four TrueType files takes
	// longer than that, so a server without this cache spends most of its time
	// re-reading the same four files. Keyed by template directory, because that
	// is what decides which files are loaded.
	mu    sync.Mutex
	faces map[string]*layout.Fonts
}

// New builds an engine rooted at home.
func New(home string) *Engine {
	return &Engine{
		Home: home,
		Registry: templates.New(
			filepath.Join(home, "templates"),
			filepath.Join(home, "fonts"),
			envOr("PICVERT_TEMPLATE", "material-you"),
		),
		faces: map[string]*layout.Fonts{},
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// Page is a document laid out, with everything the emitters need.
type Page struct {
	Doc      document.Doc
	Template *templates.Template
	Fonts    *layout.Fonts
	Fitted   layout.Fitted
	Render   *layout.Render
}

// Title is what the page and the PDF are called: the name on the CV.
func (p *Page) Title() string {
	if identity, ok := document.Identity(p.Doc); ok {
		if name := document.Str(identity, "name"); name != "" {
			return name
		}
	}
	return "CV"
}

// Lang is what the document declares itself to be written in, which is what a
// screen reader uses to choose a voice.
func (p *Page) Lang() string {
	if lang := document.Str(document.Meta(p.Doc), "lang"); lang != "" {
		return lang
	}
	return "en"
}

// Margins is the room left under each of the template's columns.
func (p *Page) Margins() layout.Margins { return p.Render.Margins(p.Template.Columns) }

// Overflow names the blocks that do not hold.
func (p *Page) Overflow() []string { return p.Render.Overflow() }

// Summary says whether the page holds, and at what setting.
//
// The setting is REPORTED, never hidden. Someone whose CV only fits at 62%
// spacing has a CV that is too long, and is owed that fact even though the page
// in front of them looks fine — it is the difference between a document that
// fits and one that has been made to.
func (p *Page) Summary() string {
	if !p.Fitted.Fits {
		return "DOES NOT FIT, even set as tightly as this engine will go"
	}
	lo, hi := p.Fitted.Spread()
	spacing := fmt.Sprintf("%.0f%%", lo*100)
	if hi-lo > 0.005 {
		spacing = fmt.Sprintf("%.0f–%.0f%%", lo*100, hi*100)
	}
	if p.Fitted.Density.Text < 1 {
		return fmt.Sprintf("fits — spacing %s, type %.0f%%", spacing, p.Fitted.Density.Text*100)
	}
	return fmt.Sprintf("fits — spacing %s", spacing)
}

// Prepare lays a document out.
//
// assetDir is where a relative photo path is resolved from — the profile's own
// directory, and nowhere above it.
func (e *Engine) Prepare(doc document.Doc, assetDir string) (*Page, error) {
	tpl, err := e.Registry.ForDocument(doc)
	if err != nil {
		return nil, err
	}
	if err := e.Registry.Check(doc, tpl); err != nil {
		return nil, err
	}
	fonts, err := e.fontsFor(tpl)
	if err != nil {
		return nil, err
	}

	ready, err := inline(doc, assetDir)
	if err != nil {
		return nil, err
	}

	composer := &theme.Composer{Theme: tpl.Theme, Icons: tpl.Icons, Regions: tpl.Columns}
	// Composed afresh for each attempt: the fit search sets the spacing on the
	// NODES, so a tree handed to it twice would be scaled twice.
	var composeErr error
	compose := func() *layout.Node {
		node, err := composer.Page(ready)
		if err != nil {
			// An empty page fits, so the search stops immediately and the error
			// is reported instead of being retried ten more times.
			composeErr = err
			return &layout.Node{}
		}
		return node
	}

	usable := layout.PageHeight - tpl.Theme.PagePadding()
	fitted := layout.NewEngine(fonts).LayoutFitted(
		compose, layout.PageWidth, layout.PageHeight, usable, len(tpl.Columns))
	if composeErr != nil {
		return nil, composeErr
	}

	return &Page{
		Doc:      doc,
		Template: tpl,
		Fonts:    fonts,
		Fitted:   fitted,
		Render: &layout.Render{
			Frame: fitted.Frame, Width: layout.PageWidth, Height: layout.PageHeight,
			Usable: usable,
		},
	}, nil
}

// webFonts reads the compressed faces a page embeds.
//
// The .woff2 beside each .ttf: the same outlines, subset and compressed, so the
// file the engine MEASURED with and the file the page DRAWS with are the same
// design. The conformance kit refuses a template that ships one without the
// other, which is what makes the error below unreachable in practice and worth
// keeping anyway.
func (e *Engine) webFonts(tpl *templates.Template) ([]layout.WebFont, error) {
	var out []layout.WebFont
	for _, decl := range tpl.FontDecls() {
		for _, src := range decl.Sources {
			name := strings.TrimSuffix(src.File, path.Ext(src.File)) + ".woff2"
			raw, err := e.Registry.ReadFont(tpl, name)
			if err != nil {
				return nil, fmt.Errorf("web font %s: %w", name, err)
			}
			out = append(out, layout.WebFont{
				Family: decl.Family, Weight: src.Weight,
				Italic: src.Style == "italic", WOFF2: raw,
			})
		}
	}
	return out, nil
}

// loadFonts registers the faces a template declares.
//
// THROUGH THE REGISTRY, which is the only thing that knows whether a template
// lives in a directory or inside the binary. Reading a path here worked for as
// long as every deployment was a checkout — and produced "default template not
// found" the first time somebody installed a release.
//
// The file measured, the file drawn and the file sent are one file, which is
// why this is not left to whatever fonts a machine has installed.
func (e *Engine) loadFonts(fonts *layout.Fonts, tpl *templates.Template) error {
	for _, decl := range tpl.FontDecls() {
		for _, src := range decl.Sources {
			raw, err := e.Registry.ReadFont(tpl, src.File)
			if err != nil {
				return fmt.Errorf("font %s: %w", src.File, err)
			}
			weight := layout.Weight(400)
			if src.Weight != 0 {
				weight = layout.Weight(src.Weight)
			}
			if err := fonts.Parse(decl.Family, weight, src.Style == "italic",
				src.File, raw); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) fontsFor(tpl *templates.Template) (*layout.Fonts, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// Keyed by UUID rather than by directory: a built-in template has no
	// directory, so every one of them would share the empty string.
	if hit := e.faces[tpl.UUID]; hit != nil {
		return hit, nil
	}
	fonts := layout.NewFonts()
	if err := e.loadFonts(fonts, tpl); err != nil {
		return nil, err
	}
	e.faces[tpl.UUID] = fonts
	return fonts, nil
}

// HTML wraps a laid-out page into a standalone file.
func (e *Engine) HTML(p *Page) (string, error) {
	faces, err := e.webFonts(p.Template)
	if err != nil {
		return "", err
	}
	return layout.Document(p.Render, faces, p.Title())
}

// PDF draws the page a second time, from the same frame.
//
// source, when given, travels inside the file the way Factur-X carries its XML:
// the CV that produced the PDF can be recovered from the PDF.
func (e *Engine) PDF(p *Page, sourceName string, source []byte) ([]byte, error) {
	doc := pdf.New(p.Render.Width, p.Render.Height, p.Title(), p.Lang())

	// The same faces the page embeds, from the same files the engine measured
	// with. Measuring in one font and drawing in another is the fault this whole
	// design exists to remove.
	for _, decl := range p.Template.FontDecls() {
		for _, src := range decl.Sources {
			// The subset where there is one, the whole face otherwise. The PDF
			// embeds the smallest file that draws the text, and reading the
			// full face when a subset exists triples what the file weighs.
			file := src.File
			raw, err := e.Registry.ReadFont(p.Template, pdf.SubsetName(file))
			if err != nil {
				raw, err = e.Registry.ReadFont(p.Template, file)
				if err != nil {
					return nil, fmt.Errorf("font %s: %w", file, err)
				}
			}
			weight := src.Weight
			if weight == 0 {
				weight = 400
			}
			face, err := pdf.ParseFace("", decl.Family, weight,
				src.Style == "italic", file, raw)
			if err != nil {
				return nil, err
			}
			doc.AddFace(face)
		}
	}
	if len(source) > 0 {
		doc.Attach(sourceName, source)
	}
	return doc.Render(p.Render.Frame), nil
}

var photoMIME = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".webp": "image/webp", ".svg": "image/svg+xml",
}

// inline works out the values a composition binds but should not have to
// compute: the portrait as a data URI, and the subtitle as one line.
func inline(doc document.Doc, assetDir string) (document.Doc, error) {
	out := make(document.Doc, len(doc))
	for k, v := range doc {
		out[k] = v
	}
	content, ok := document.Obj(doc, "content")
	if !ok {
		return out, nil
	}
	nextContent := make(map[string]any, len(content))
	for k, v := range content {
		nextContent[k] = v
	}
	identity, _ := document.Obj(content, "identity")
	next := make(map[string]any, len(identity)+2)
	for k, v := range identity {
		next[k] = v
	}

	if photo := document.Str(identity, "photo"); photo != "" {
		uri, err := photoURI(assetDir, photo)
		if err != nil {
			return nil, err
		}
		next["photo"] = uri
	}

	// The subtitle is a list of parts, some emphasised. Flattened here into
	// rich text so the composition binds ONE field and the whole line wraps as
	// one line — parts as separate nodes could not wrap at all.
	if parts, ok := document.Arr(identity, "subtitle"); ok {
		var b strings.Builder
		for i, raw := range parts {
			part, _ := raw.(map[string]any)
			if i > 0 {
				// Space, NO-BREAK SPACE, middle dot, NO-BREAK SPACE, space.
				// The non-breaking pair is what keeps the separator attached to
				// the words on either side, so a subtitle never wraps onto a
				// line beginning with a lone dot. The outer spaces are what make
				// it read as a separator rather than as punctuation.
				b.WriteString(" \u00a0\u00b7\u00a0 ")
			}
			text := document.Str(part, "text")
			if strong, _ := part["strong"].(bool); strong {
				b.WriteString("<b>" + text + "</b>")
			} else {
				b.WriteString(text)
			}
		}
		next["subtitleLine"] = b.String()
	}

	nextContent["identity"] = next
	out["content"] = nextContent
	return out, nil
}

// photoURI inlines the portrait, refusing anything outside the profile: the
// value comes from a document, and a document can be edited by whoever holds a
// link to it.
func photoURI(assetDir, file string) (string, error) {
	if strings.HasPrefix(strings.ToLower(file), "data:") {
		return file, nil
	}
	base, err := filepath.Abs(assetDir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(filepath.Join(assetDir, file))
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(abs, base+string(filepath.Separator)) {
		return "", fmt.Errorf("asset outside the profile refused: %q", file)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("asset not found: %s", file)
	}
	mime, ok := photoMIME[strings.ToLower(filepath.Ext(abs))]
	if !ok {
		return "", fmt.Errorf("unsupported image format: %q", filepath.Ext(abs))
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

// ReadDoc reads one document off disk, lifted to the current shape.
func ReadDoc(path string) (document.Doc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseDoc(raw, filepath.Base(path))
}

// ParseDoc lifts a document that arrived as bytes.
//
// Lifted on the way IN, so nothing downstream has to know that documents used
// to carry identity and sections at the top level.
func ParseDoc(raw []byte, name string) (document.Doc, error) {
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	doc, ok := document.AsObject(document.Upgrade(parsed))
	if !ok {
		return nil, fmt.Errorf("%s is not a JSON object", name)
	}
	return doc, nil
}
