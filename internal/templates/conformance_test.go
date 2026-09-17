// The conformance kit.
//
// It does not test ONE template: it walks every directory under templates/ and
// puts each through the same battery. A new template is covered from the moment
// it exists — which is the whole point, because a kit that had to be extended
// per template is a kit nobody extends, and the second template ships untested.
//
// WHAT IT CHECKS, AND WHY EACH ONE. Every contract here has been broken at least
// once in this project's history, and all of them break SILENTLY:
//
//   - a template that accepts a section type and composes nothing for it renders
//     a blank card — the exact failure a template system exists to prevent;
//   - a block the theme does not NAME cannot be reported as overflowing, so a CV
//     is cut off with nothing to say which part to shorten;
//   - a template whose page does not read as [header…, body] makes the margin
//     measurement return nothing, and the fit check goes quiet;
//   - a theme that asks for a font it does not ship draws in whatever the
//     reader has, which is wider, and cuts the last section off the page.
//
// The document under test is SYNTHESISED from the field tree rather than written
// by hand: it exercises exactly what the template declares, and follows the
// vocabulary when that changes.
package templates

import (
	"os"
	"path/filepath"
	"testing"

	"picvert/internal/fields"
	"picvert/internal/layout"
	"picvert/internal/theme"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func registry(t *testing.T) *Registry {
	t.Helper()
	root := repoRoot(t)
	return New(filepath.Join(root, "templates"), filepath.Join(root, "fonts"), "material-you")
}

// sample is a valid value for a field, whatever its kind.
//
// Derived from the tree rather than written out, so a field added to the
// vocabulary is exercised without this file moving, and a field wrongly
// declared shows up here rather than on someone's CV.
func sample(f fields.Field, icons []string) any {
	switch f.Kind {
	case fields.KindText, fields.KindRich:
		return f.Label + " de référence"
	case fields.KindIcon:
		// An icon MUST come from the template's own set: it is the one kind of
		// field whose valid values depend on who renders it.
		if len(icons) > 0 {
			return icons[0]
		}
		return "mail"
	case fields.KindNumber:
		v := 50
		if f.Min != nil && *f.Min > v {
			v = *f.Min
		}
		if f.Max != nil && *f.Max < v {
			v = *f.Max
		}
		return float64(v)
	case fields.KindBool:
		return false
	case fields.KindEnum:
		if f.Default != nil {
			return *f.Default
		}
		if len(f.Values) > 0 {
			return f.Values[0].Value
		}
		return ""
	case fields.KindPhoto:
		// None: the reference document must render without one, or the kit
		// could not run on a template that ships no image.
		return ""
	case fields.KindList:
		if f.Of == nil {
			return []any{}
		}
		n := 2
		if f.Min != nil && *f.Min > n {
			n = *f.Min
		}
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, sample(*f.Of, icons))
		}
		return out
	case fields.KindGroup:
		out := map[string]any{}
		for _, sub := range f.Fields {
			out[sub.Key] = sample(sub, icons)
		}
		return out
	}
	return nil
}

// referenceDoc builds a document exercising everything a template declares.
func referenceDoc(t *Template) map[string]any {
	icons := t.IconOrder

	identity := map[string]any{}
	for _, f := range fields.IdentityFields {
		identity[f.Key] = sample(f, icons)
	}

	sections := make([]any, 0, len(t.Sections))
	for i, slot := range t.Sections {
		s := map[string]any{
			"id":     slot.Type + "-ref",
			"type":   slot.Type,
			"column": slot.Column,
			"title":  fields.SectionLabels[slot.Type].Label,
		}
		for _, f := range fields.SectionFields[slot.Type] {
			s[f.Key] = sample(f, icons)
		}
		_ = i
		sections = append(sections, s)
	}

	return map[string]any{
		"meta":    map[string]any{"lang": "fr", "template": t.UUID},
		"content": map[string]any{"identity": identity, "sections": sections},
	}
}

func TestEveryTemplateConforms(t *testing.T) {
	all, err := registry(t).All()
	if err != nil {
		t.Fatalf("no templates load: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no templates at all: the engine can render nothing")
	}
	t.Logf("%d template(s)", len(all))

	for _, tpl := range all {
		tpl := tpl
		t.Run(tpl.Name, func(t *testing.T) {
			doc := referenceDoc(tpl)

			// 1. What it declares must produce a document it accepts.
			if err := registry(t).Check(doc, tpl); err != nil {
				t.Fatalf("its own reference document is refused: %v", err)
			}

			// 2. Every accepted section type must be composed. A slot with no
			//    composition renders a blank card.
			for _, slot := range tpl.Sections {
				if tpl.Theme.Sections[slot.Type] == nil {
					t.Errorf("accepts %q sections but composes none", slot.Type)
				}
			}

			// 3. It must lay out, with its own fonts.
			fonts := layout.NewFonts()
			root := repoRoot(t)
			if err := layout.LoadTemplateFonts(fonts, tpl.Dir,
				filepath.Join(root, "fonts"), tpl.FontDecls()); err != nil {
				t.Fatalf("its fonts do not load: %v", err)
			}
			for _, decl := range tpl.FontDecls() {
				if !fonts.Has(decl.Family) {
					t.Errorf("declares the family %q but none of its files loaded", decl.Family)
				}
			}

			composer := &theme.Composer{Theme: tpl.Theme, Icons: tpl.Icons, Regions: tpl.Columns}
			node, err := composer.Page(doc)
			if err != nil {
				t.Fatalf("it does not compose: %v", err)
			}
			frame := layout.NewEngine(fonts).Layout(node, layout.PageWidth, layout.PageHeight)

			// 4. Its page must read as [header…, body], regions in the order
			//    `columns` declares them — or the margin measurement has
			//    nothing to say and the fit check goes quiet.
			if len(frame.Children) < 2 {
				t.Fatalf("its page has %d parts; the engine reads it as [header…, body]",
					len(frame.Children))
			}
			body := frame.Children[len(frame.Children)-1]
			if len(body.Children) != len(tpl.Columns) {
				t.Errorf("its body holds %d regions, but it declares %d columns",
					len(body.Children), len(tpl.Columns))
			}

			// 5. A reference document must FIT. A template whose own minimum
			//    content already overflows is one no real CV would fit.
			usable := layout.PageHeight - tpl.Theme.PagePadding()
			if over := frame.Overflow(usable); len(over) > 0 {
				t.Errorf("its own reference document already overflows: %v", over)
			}

			// 6. It must NAME the blocks it lays out, or overflow can point at
			//    nothing and the editor can mark nothing.
			named := 0
			frame.Walk(func(f *layout.Frame) {
				if f.ID() != "" {
					named++
				}
			})
			if named < len(tpl.Sections) {
				t.Errorf("names %d blocks for %d sections: overflow cannot report what it cannot name",
					named, len(tpl.Sections))
			}

			// 7. It must ship the web fonts the page embeds. Without them the
			//    page draws in whatever the reader has installed — wider, on
			//    a layout that fits by less than a line.
			for _, decl := range tpl.FontDecls() {
				for _, src := range decl.Sources {
					path, ok := registry(t).FontPath(tpl.Dir, src.File)
					if !ok {
						continue
					}
					web := path[:len(path)-len(filepath.Ext(path))] + ".woff2"
					if _, err := os.Stat(web); err != nil {
						t.Errorf("%s has no .woff2 beside it: the page cannot embed it",
							filepath.Base(path))
					}
				}
			}

			// 8. It must have a cover if it declares one.
			if tpl.Cover != "" {
				if _, err := os.Stat(filepath.Join(tpl.Dir, tpl.Cover)); err != nil {
					t.Errorf("declares a cover it does not ship: %s", tpl.Cover)
				}
			}
		})
	}
}
