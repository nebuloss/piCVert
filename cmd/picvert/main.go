// Command picvert turns a cv.json into a page.
//
// One layout engine, two emitters. The page you read and the PDF you send are
// drawn from the same computed frame, so they cannot come out differently —
// which is the whole reason this program exists.
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"picvert/internal/document"
	"picvert/internal/layout"
	"picvert/internal/templates"
	"picvert/internal/theme"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "render":
		err = renderCmd(os.Args[2:])
	case "fit":
		err = fitCmd(os.Args[2:])
	case "preview":
		err = previewCmd(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `picvert — a CV engine that lays out once

  picvert render --profile <dir> [--lang xx] [--out page.html]
        Lays the CV out and writes the page.

  picvert fit --profile <dir> [--lang xx]
        Reports whether it holds on one page, and what is left over.

  picvert preview --profile <dir> [--addr host:port]
        Serves the CV, laid out again on every reload. /fit reports the fit.

Environment:
  PICVERT_HOME    where templates/ and fonts/ live (default: alongside the binary)
`)
}

// home is where the engine's own data lives: the templates and the fonts.
//
// Found next to the binary rather than compiled in, because a template is a
// directory someone may add. The variable exists so a checkout can be run
// without installing it anywhere.
func home() (string, error) {
	if v := os.Getenv("PICVERT_HOME"); v != "" {
		return v, nil
	}
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 4; i++ {
			if _, err := os.Stat(filepath.Join(dir, "templates")); err == nil {
				return dir, nil
			}
			up := filepath.Dir(dir)
			if up == dir {
				break
			}
			dir = up
		}
	}
	return os.Getwd()
}

// prepared is a document laid out, with everything the emitters need.
type prepared struct {
	Template *templates.Template
	Render   *layout.Render
	Fonts    *layout.Fonts
}

// build is the pipeline, start to finish. Both commands go through it, so what
// `fit` measures is exactly what `render` draws.
func build(profileDir, lang string) (*prepared, error) {
	root, err := home()
	if err != nil {
		return nil, err
	}
	registry := templates.New(
		filepath.Join(root, "templates"),
		filepath.Join(root, "fonts"),
		envOr("PICVERT_TEMPLATE", "material-you"),
	)

	doc, err := readDoc(profileDir, lang)
	if err != nil {
		return nil, err
	}

	tpl, err := registry.ForDocument(doc)
	if err != nil {
		return nil, err
	}
	if err := registry.Check(doc, tpl); err != nil {
		return nil, err
	}

	fonts := layout.NewFonts()
	if err := layout.LoadTemplateFonts(fonts, tpl.Dir,
		filepath.Join(root, "fonts"), tpl.FontDecls()); err != nil {
		return nil, err
	}

	ready, err := inline(doc, profileDir)
	if err != nil {
		return nil, err
	}

	composer := &theme.Composer{Theme: tpl.Theme, Icons: tpl.Icons, Regions: tpl.Columns}
	node, err := composer.Page(ready)
	if err != nil {
		return nil, err
	}

	frame := layout.NewEngine(fonts).Layout(node, layout.PageWidth, layout.PageHeight)
	return &prepared{
		Template: tpl,
		Fonts:    fonts,
		Render: &layout.Render{
			Frame: frame, Width: layout.PageWidth, Height: layout.PageHeight,
			Usable: layout.PageHeight - tpl.Theme.PagePadding(),
		},
	}, nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func docName(lang string) string {
	if lang == "" || lang == "default" {
		return "cv.json"
	}
	return "cv." + lang + ".json"
}

func readDoc(profileDir, lang string) (document.Doc, error) {
	raw, err := os.ReadFile(filepath.Join(profileDir, docName(lang)))
	if err != nil {
		return nil, err
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("%s: %w", docName(lang), err)
	}
	doc, ok := document.AsObject(document.Upgrade(parsed))
	if !ok {
		return nil, fmt.Errorf("%s is not a JSON object", docName(lang))
	}
	return doc, nil
}

func renderCmd(args []string) error {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	profileDir := fs.String("profile", "", "profile directory holding cv.json")
	lang := fs.String("lang", "", "language variant")
	out := fs.String("out", "", "file to write (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *profileDir == "" {
		return fmt.Errorf("--profile is required")
	}
	p, err := build(*profileDir, *lang)
	if err != nil {
		return err
	}
	root, _ := home()
	page, err := Document(p, root, documentTitle(*profileDir, *lang))
	if err != nil {
		return err
	}
	if *out == "" {
		fmt.Print(page)
		return nil
	}
	return os.WriteFile(*out, []byte(page), 0o644)
}

// Document wraps a laid-out page into a standalone file. Here rather than
// inlined at each call site so the two commands cannot assemble it differently.
func Document(p *prepared, root, title string) (string, error) {
	return layout.Document(p.Render, p.Template.FontDecls(), p.Template.Dir,
		filepath.Join(root, "fonts"), title)
}

func documentTitle(profileDir, lang string) string {
	doc, err := readDoc(profileDir, lang)
	if err != nil {
		return "CV"
	}
	if identity, ok := document.Identity(doc); ok {
		if name := document.Str(identity, "name"); name != "" {
			return name
		}
	}
	return "CV"
}

func fitCmd(args []string) error {
	fs := flag.NewFlagSet("fit", flag.ExitOnError)
	profileDir := fs.String("profile", "", "profile directory holding cv.json")
	lang := fs.String("lang", "", "language variant")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *profileDir == "" {
		return fmt.Errorf("--profile is required")
	}
	p, err := build(*profileDir, *lang)
	if err != nil {
		return err
	}
	if p.Render.Fits() {
		fmt.Println("fits on one page")
	} else {
		fmt.Println("DOES NOT FIT")
	}
	for _, name := range p.Template.Columns {
		fmt.Printf("  %-6s %+8.1f px left\n", name, p.Render.Margins(p.Template.Columns)[name])
	}
	if over := p.Render.Overflow(); len(over) > 0 {
		fmt.Printf("  shorten: %s\n", strings.Join(over, ", "))
	}
	return nil
}

var photoMIME = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".webp": "image/webp", ".svg": "image/svg+xml",
}

// inline works out the values a composition binds but should not have to
// compute: the portrait as a data URI, and the subtitle as one line.
func inline(doc document.Doc, profileDir string) (document.Doc, error) {
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
		uri, err := photoURI(profileDir, photo)
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
				b.WriteString(" \u00b7 ")
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
func photoURI(profileDir, file string) (string, error) {
	if strings.HasPrefix(strings.ToLower(file), "data:") {
		return file, nil
	}
	base, err := filepath.Abs(profileDir)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(filepath.Join(profileDir, file))
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
