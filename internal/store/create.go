package store

import (
	"fmt"
	"os"
	"time"

	"picvert/internal/document"
	"picvert/internal/fields"
	"picvert/internal/profiles"
	"picvert/internal/templates"
)

// Create makes a new CV, and returns its links.
//
// A NEW CV IS NOT AN EMPTY ONE. A blank page with a name field is a page nobody
// knows what to do with, and the first thing anyone does with a CV engine is
// look for the shape of a CV. So it is seeded with the sections the chosen
// template lays out, each empty but present and titled — the person filling it
// in is editing a CV from the first second rather than assembling one.
//
// The seed is derived from the TEMPLATE, not written out here: a template that
// accepts a section type this engine has never heard of still gets one, and a
// template that refuses a type does not get an empty card it cannot draw.
func (s *Store) Create(slug, name, templateRef, lang string) (document.Doc, error) {
	if err := profiles.CheckSlug(slug); err != nil {
		return nil, err
	}
	p, err := s.Profiles.For(slug)
	if err != nil {
		return nil, err
	}
	// Checked before anything is written, and by the DIRECTORY rather than by
	// the document: a profile whose cv.json failed to write is still a profile
	// whose name is taken, and handing the same slug to someone else would put
	// two people's CVs in one folder.
	if _, err := os.Stat(p.Dir); err == nil {
		return nil, fmt.Errorf("a CV already exists under %q", slug)
	}
	t, err := s.Registry.Resolve(templateRef)
	if err != nil {
		return nil, err
	}
	if lang == "" {
		lang = "en"
	}
	if name == "" {
		name = "Your name"
	}

	doc := document.Doc{
		"meta": map[string]any{
			"lang":     lang,
			"template": t.UUID,
		},
		"content": map[string]any{
			"identity": map[string]any{"name": name},
			"sections": seedSections(t),
		},
	}

	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return nil, err
	}
	stored, err := s.Write(slug, doc, "")
	if err != nil {
		// The directory goes with the failed document. Left behind, it is a
		// slug that is taken and a CV that does not exist — and the next
		// attempt at the same name fails on the check above with no way to
		// clear it except by hand.
		_ = os.RemoveAll(p.Dir)
		return nil, err
	}
	return stored, nil
}

// seedSections is one empty section per kind the template lays out.
//
// Empty but PRESENT: a card with a heading and nothing under it says "put
// something here", where no card at all says nothing. They are created in the
// template's own declared order, which is the order it was designed to be read
// in.
func seedSections(t *templates.Template) []any {
	out := make([]any, 0, len(t.Sections))
	seen := map[string]bool{}
	for _, slot := range t.Sections {
		// One per KIND, not one per slot: a template accepting three chip
		// sections wants three when somebody asks for them, not three empty
		// ones on the first day.
		if seen[slot.Type] {
			continue
		}
		seen[slot.Type] = true

		section := map[string]any{}
		// THE BODY COMES FROM THE FIELD TREE, not from a list of kinds written
		// out here. A `chips` section needs a group, which needs a chip, which
		// needs text — and the first attempt at this hard-coded an empty list
		// for each kind and was rejected by the validator that knows better.
		// Anything written here would be a second statement of what a section
		// is, and the two would part company the first time a field gained a
		// minimum.
		for _, field := range fields.SectionFields[slot.Type] {
			if value, ok := blankField(field); ok {
				section[field.Key] = value
			}
		}
		section["id"] = slot.Type + "-1"
		section["type"] = slot.Type
		section["column"] = slot.Column
		section["title"] = sectionTitle(slot.Type)
		out = append(out, section)
	}
	return out
}

// blankField is an empty value of a field, or nothing when the field can simply
// be left out.
//
// Absent beats empty wherever the validator allows it: a document carrying a
// key for every field it has ever heard of is a document whose diff, on the
// first real edit, is a page of nothing-to-nothing.
func blankField(f fields.Field) (any, bool) {
	switch f.Kind {
	case fields.KindGroup:
		out := map[string]any{}
		for _, child := range f.Fields {
			if value, ok := blankField(child); ok {
				out[child.Key] = value
			}
		}
		return out, true

	case fields.KindList:
		// A list with a minimum gets exactly that many entries, each built the
		// same way. That is what makes a seeded `chips` section valid: the
		// group it must contain is itself seeded, down to the chip inside it.
		count := 0
		if f.Min != nil {
			count = *f.Min
		}
		items := make([]any, 0, count)
		if f.Of != nil {
			for range count {
				if value, ok := blankField(*f.Of); ok {
					items = append(items, value)
				}
			}
		}
		return items, true

	case fields.KindText, fields.KindRich:
		if !f.Required {
			return nil, false
		}
		// A required field cannot be blank — the validator is right about that,
		// and a seeded CV still has to be a valid one. So it gets the field's
		// OWN placeholder, which is the text the editor already shows in the
		// empty control, and an ellipsis when it has none.
		//
		// Never invented prose. Placeholder sentences that read like a real CV
		// are the ones that survive into a published document, because they
		// look finished; an ellipsis is unmistakably something to replace.
		if f.Placeholder != "" {
			return f.Placeholder, true
		}
		return "…", true

	case fields.KindNumber:
		if !f.Required {
			return nil, false
		}
		if f.Min != nil {
			return *f.Min, true
		}
		return 0, true

	case fields.KindBool:
		return nil, false

	case fields.KindEnum:
		if f.Default != nil {
			return *f.Default, true
		}
		return nil, false

	default:
		return nil, false
	}
}

// seedTitles is the heading a new section of each kind is born with.
//
// In English, deliberately: it is the language of the field vocabulary, and a
// heading in the wrong language is something an author changes on sight, where
// an empty one is something they have to work out they are allowed to change.
var seedTitles = map[string]string{
	"contact":   "Contact",
	"chips":     "Skills",
	"languages": "Languages",
	"text":      "Profile",
	"items":     "Experience",
	"list":      "Projects",
}

func sectionTitle(kind string) string {
	if title, ok := seedTitles[kind]; ok {
		return title
	}
	return kind
}

// Touch records that a profile was looked at, without changing the document.
//
// Sweeping up abandoned drafts leans on this rather than on file timestamps:
// comparing dates with a margin made a CV edited just after being created look
// abandoned.
func (s *Store) Touch(p *profiles.Profile) {
	now := time.Now()
	_ = os.Chtimes(p.JSONPath(), now, now)
}
