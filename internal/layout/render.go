package layout

import (
	"fmt"
	"os"
	"path/filepath"
)

// Render is the whole pipeline, from a composed tree to a page.
//
// Both emitters are handed the SAME Frame. That is the entire point of this
// package: there is no second interpretation of the design, so there is nothing
// for the two renderings to disagree about.
type Render struct {
	Frame  *Frame
	Width  float64
	Height float64
	// Usable is the bottom of the content area — the page height less its own
	// bottom padding. Overflow is measured against this, not against the paper
	// edge, because a card touching the edge has already lost its margin.
	Usable float64
}

// Fits reports whether everything holds.
func (r *Render) Fits() bool { return r.Frame.Fits(r.Usable) }

// Overflow names the blocks that do not.
func (r *Render) Overflow() []string { return r.Frame.Overflow(r.Usable) }

// Margins is the room left under each region, by name — what the editor shows
// as "you have this much left".
func (r *Render) Margins(regions []string) Margins {
	out := Margins{}
	// The regions are the children of the body, which is the last child of the
	// page. Stated as a contract rather than searched for, so a theme that
	// lays its page out differently gets nothing rather than a plausible wrong
	// number.
	if len(r.Frame.Children) == 0 {
		return out
	}
	body := r.Frame.Children[len(r.Frame.Children)-1]
	if len(body.Children) != len(regions) {
		return out
	}
	for i, name := range regions {
		out[name] = round1(r.Usable - body.Children[i].ContentBottom())
	}
	return out
}

func round1(v float64) float64 {
	return float64(int(v*10+sign(v)*0.5)) / 10
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

// A4 at 96 dpi, which is what the themes are written against and what the
// browser draws. The PDF emitter converts to points once, at the very end.
const (
	PageWidth  = 794
	PageHeight = 1123
)

// LoadTemplateFonts registers the faces a template declares.
//
// From the manifest, which is also what the PDF embeds and what the HTML
// emitter inlines — so the file measured, the file drawn and the file sent are
// one file. Measuring in a font other than the one drawn is how a CV that fits
// comes out clipped, and it is the reason this is not left to the machine's
// installed fonts.
func LoadTemplateFonts(f *Fonts, dir string, sharedDir string, fonts []FontDecl) error {
	for _, decl := range fonts {
		for _, src := range decl.Sources {
			path := src.File
			if rel, ok := trimShared(path); ok {
				path = filepath.Join(sharedDir, rel)
			} else {
				path = filepath.Join(dir, path)
			}
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("font %s not found", src.File)
			}
			weight := Weight(400)
			if src.Weight != 0 {
				weight = Weight(src.Weight)
			}
			if err := f.Load(decl.Family, weight, src.Style == "italic", path); err != nil {
				return err
			}
		}
	}
	return nil
}

// FontDecl mirrors a manifest's font declaration, without importing the
// template registry — which imports this package.
type FontDecl struct {
	Family  string
	Sources []FontSourceDecl
}

// FontSourceDecl is one file of one family.
type FontSourceDecl struct {
	File   string
	Weight int
	Style  string
}

func trimShared(file string) (string, bool) {
	const prefix = "@engine/"
	if len(file) > len(prefix) && file[:len(prefix)] == prefix {
		return file[len(prefix):], true
	}
	return "", false
}
