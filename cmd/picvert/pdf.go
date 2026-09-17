package main

import (
	"encoding/json"
	"strings"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"picvert/internal/layout"
	"picvert/internal/pdf"
)

// pdfCmd writes the PDF of a profile.
//
// From the SAME layout the page is drawn from — that is the whole point of the
// engine. The old one laid the document out twice and the two answers differed
// by about a line per block, which is how a CV that measured as fitting arrived
// with its last section cut off.
func pdfCmd(args []string) error {
	fs := flag.NewFlagSet("pdf", flag.ExitOnError)
	profileDir := fs.String("profile", "", "profile directory holding cv.json")
	lang := fs.String("lang", "", "language variant")
	out := fs.String("out", "cv.pdf", "file to write")
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
	data, err := renderPDF(p, *profileDir, *lang)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}

	fmt.Printf("%s  %d KB  —  %s", *out, len(data)/1024, fitSummary(p))
	if !p.Fitted.Fits {
		fmt.Printf("  (shorten %s)", strings.Join(p.Render.Overflow(), ", "))
	}
	fmt.Println()
	return nil
}

// renderPDF turns a laid-out profile into the finished file.
func renderPDF(p *prepared, profileDir, lang string) ([]byte, error) {
	root, err := home()
	if err != nil {
		return nil, err
	}

	doc := pdf.New(p.Render.Width, p.Render.Height,
		documentTitle(profileDir, lang), documentLang(profileDir, lang))

	// The same four faces the page embeds, from the same files the engine
	// measured with. Measuring in one font and drawing in another is the fault
	// this whole design exists to remove.
	for _, decl := range p.Template.FontDecls() {
		for _, src := range decl.Sources {
			path := src.File
			if rel, ok := trimEnginePrefix(path); ok {
				path = filepath.Join(root, "fonts", rel)
			} else {
				path = filepath.Join(p.Template.Dir, path)
			}
			weight := src.Weight
			if weight == 0 {
				weight = 400
			}
			face, err := pdf.LoadFace("", decl.Family, weight, src.Style == "italic", path)
			if err != nil {
				return nil, err
			}
			doc.AddFace(face)
		}
	}

	// The source document, carried inside the file: see pdf.Document.Attach.
	if raw, err := os.ReadFile(filepath.Join(profileDir, docName(lang))); err == nil {
		doc.Attach(docName(lang), raw)
	}

	return doc.Render(p.Render.Frame), nil
}

func trimEnginePrefix(file string) (string, bool) {
	const prefix = "@engine/"
	if len(file) > len(prefix) && file[:len(prefix)] == prefix {
		return file[len(prefix):], true
	}
	return "", false
}

// documentLang is what the file declares its language to be, which is what a
// screen reader uses to choose a voice.
func documentLang(profileDir, lang string) string {
	if lang != "" {
		return lang
	}
	doc, err := readDoc(profileDir, "")
	if err != nil {
		return "en"
	}
	var meta struct{ Lang string }
	if raw, err := json.Marshal(doc["meta"]); err == nil {
		_ = json.Unmarshal(raw, &meta)
	}
	if meta.Lang == "" {
		return "en"
	}
	return meta.Lang
}

var _ = layout.PageWidth
