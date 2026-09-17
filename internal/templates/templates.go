// Package templates finds the layouts this engine can draw with.
//
// A template is a DIRECTORY, and that is the point: adding a layout means
// adding a folder, not changing this program. The registry discovers what is on
// disk and validates it, so a broken template is a startup error naming itself
// rather than a page that renders blank.
//
//	templates/<name>/
//	  template.json   identity, regions, accepted sections, fonts
//	  theme.json      palette, styles, composition
//	  icons.json      glyph set
//	  cover.svg       picker illustration
//
// Nothing here is compiled in. A new template needs no Go and no rebuild.
package templates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"picvert/internal/document"
	"picvert/internal/fields"
	"picvert/internal/layout"
	"picvert/internal/theme"
)

// sharedPrefix names a font that travels with the engine rather than with one
// template. Several templates want Roboto; shipping four copies of it would be
// four copies to keep in step.
const sharedPrefix = "@engine/"

var (
	uuidRE  = regexp.MustCompile(`^(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	nameRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	coverRE = regexp.MustCompile(`(?i)\.(svg|png)$`)
)

// Regions are the areas a template may lay out. A closed set, like the section
// types: a document moves between templates by a mechanical remapping, which is
// only possible if both sides name the same places.
var Regions = []string{"left", "right", "full"}

// FontSource is one file of one family.
type FontSource struct {
	File   string `json:"file"`
	Weight int    `json:"weight,omitempty"`
	Style  string `json:"style,omitempty"`
}

// Font is a family the template asks for.
type Font struct {
	Family  string       `json:"family"`
	Sources []FontSource `json:"sources"`
}

// Slot is a kind of section the layout can draw, and where a new one goes.
type Slot struct {
	Type   string `json:"type"`
	Column string `json:"column"`
	// Max is how many of this type the layout can render. Absent means no
	// limit — a pointer, because zero would be a real answer.
	Max *int `json:"max,omitempty"`
}

// Manifest is template.json.
type Manifest struct {
	UUID        string   `json:"uuid"`
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Version     int      `json:"version"`
	Description string   `json:"description,omitempty"`
	Cover       string   `json:"cover,omitempty"`
	Columns     []string `json:"columns"`
	Sections    []Slot   `json:"sections"`
	Fonts       []Font   `json:"fonts,omitempty"`
}

// Template is a manifest, its design, and where it lives.
type Template struct {
	Manifest
	Dir   string
	Theme *theme.Theme
	Icons map[string]theme.Glyph
	// IconOrder keeps the order icons.json declared, because an error message
	// lists them and a list that reshuffles between runs cannot be compared.
	IconOrder []string
}

// Error is a template that will not load, and why.
//
// A distinct type because it means "this template is broken" — a deployment
// problem — as opposed to "this document does not suit this template", which is
// something the person editing can fix.
type Error struct {
	Name string
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("Template “%s”: %s", e.Name, e.Msg) }

func bad(name, format string, args ...any) error {
	return &Error{Name: name, Msg: fmt.Sprintf(format, args...)}
}

// Registry is the set of templates this process serves.
type Registry struct {
	Dir string
	// SharedFonts is where `@engine/` fonts live.
	SharedFonts string
	// Default names the template a document falls back to.
	Default string

	mu    sync.Mutex
	cache []*Template
}

func New(dir, sharedFonts, defaultName string) *Registry {
	return &Registry{Dir: dir, SharedFonts: sharedFonts, Default: defaultName}
}

// Reload drops the cache, for a template added without a restart.
func (r *Registry) Reload() {
	r.mu.Lock()
	r.cache = nil
	r.mu.Unlock()
}

// All is every template, by title.
func (r *Registry) All() ([]*Template, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache != nil {
		return r.cache, nil
	}

	entries, err := os.ReadDir(r.Dir)
	if err != nil {
		return nil, fmt.Errorf("no templates folder at %s", r.Dir)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(r.Dir, e.Name(), "template.json")); err == nil {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no template in %s — each one needs a template.json", r.Dir)
	}
	sort.Strings(names)

	loaded := make([]*Template, 0, len(names))
	// The UUID is what a document stores, so two templates sharing one would
	// make a document resolve to whichever loaded first — that is, at random.
	byUUID := map[string]string{}
	for _, name := range names {
		t, err := r.load(filepath.Join(r.Dir, name), name)
		if err != nil {
			return nil, err
		}
		if other, clash := byUUID[t.UUID]; clash {
			return nil, fmt.Errorf("templates “%s” and “%s” share the UUID %s", other, t.Name, t.UUID)
		}
		byUUID[t.UUID] = t.Name
		loaded = append(loaded, t)
	}
	sort.SliceStable(loaded, func(i, j int) bool { return loaded[i].Title < loaded[j].Title })
	r.cache = loaded
	return loaded, nil
}

func (r *Registry) load(dir, name string) (*Template, error) {
	m, err := r.readManifest(dir, name)
	if err != nil {
		return nil, err
	}
	th, err := theme.Load(dir)
	if err != nil {
		return nil, bad(name, "%v", err)
	}
	// Every accepted section type must be composed. A slot the theme draws
	// nothing for renders a blank card, which is the failure a template system
	// exists to prevent — so it is refused at load rather than discovered on
	// someone's CV.
	for _, slot := range m.Sections {
		if th.Sections[slot.Type] == nil {
			return nil, bad(name, "accepts “%s” sections but composes none", slot.Type)
		}
	}
	icons, order := readIcons(dir)
	return &Template{Manifest: *m, Dir: dir, Theme: th, Icons: icons, IconOrder: order}, nil
}

func (r *Registry) readManifest(dir, name string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "template.json"))
	if err != nil {
		return nil, bad(name, "unreadable template.json (%v)", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, bad(name, "unreadable template.json (%v)", err)
	}

	if !uuidRE.MatchString(m.UUID) {
		return nil, bad(name, "“uuid” must be a UUID, got %q", m.UUID)
	}
	m.UUID = strings.ToLower(m.UUID)
	if !nameRE.MatchString(m.Name) {
		return nil, bad(name, "“name” must be lowercase letters, digits and dashes, got %q", m.Name)
	}
	// The folder is how a person names a template on the command line, the
	// manifest is how it names itself. Letting them drift gives one thing two
	// names and no way to tell which a message meant.
	if m.Name != name {
		return nil, bad(name, "“name” is “%s” but the folder is “%s” — they must match", m.Name, name)
	}
	if strings.TrimSpace(m.Title) == "" {
		return nil, bad(name, "“title” is required")
	}
	if m.Version < 1 {
		return nil, bad(name, "“version” must be a positive integer")
	}
	if len(m.Columns) == 0 {
		return nil, bad(name, "“columns” must list at least one region")
	}
	for _, c := range m.Columns {
		if !known(c, Regions) {
			return nil, bad(name, "unknown region “%s” (expected: %s)", c, strings.Join(Regions, ", "))
		}
	}
	if len(m.Sections) == 0 {
		return nil, bad(name, "“sections” must declare at least one section type")
	}
	seen := map[string]bool{}
	for i, s := range m.Sections {
		if !known(s.Type, fields.SectionTypeOrder) {
			return nil, bad(name, "sections[%d].type “%s” is not a known section type (expected: %s)",
				i, s.Type, strings.Join(fields.SectionTypeOrder, ", "))
		}
		if !known(s.Column, m.Columns) {
			return nil, bad(name, "sections[%d].column “%s” is not one of this template's regions (%s)",
				i, s.Column, strings.Join(m.Columns, ", "))
		}
		if s.Max != nil && *s.Max < 1 {
			return nil, bad(name, "sections[%d].max must be a positive integer", i)
		}
		if seen[s.Type] {
			return nil, bad(name, "section type “%s” is declared twice", s.Type)
		}
		seen[s.Type] = true
	}

	if m.Cover != "" {
		if strings.ContainsAny(m.Cover, `\/`) || !coverRE.MatchString(m.Cover) {
			return nil, bad(name, "“cover” must be a plain .svg or .png file name inside the template folder")
		}
		if _, err := os.Stat(filepath.Join(dir, m.Cover)); err != nil {
			return nil, bad(name, "cover “%s” not found", m.Cover)
		}
	}

	for i, f := range m.Fonts {
		if strings.TrimSpace(f.Family) == "" {
			return nil, bad(name, "fonts[%d].family is required", i)
		}
		if len(f.Sources) == 0 {
			return nil, bad(name, "fonts[%d].sources must list at least one file", i)
		}
		for j, src := range f.Sources {
			if src.File == "" {
				return nil, bad(name, "fonts[%d].sources[%d].file is required", i, j)
			}
			abs, ok := r.FontPath(dir, src.File)
			if !ok {
				return nil, bad(name, "fonts[%d].sources[%d].file “%s” escapes the template folder", i, j, src.File)
			}
			// Checked now rather than at render time: a missing font shows up
			// as a CV in the wrong typeface, months later, on someone else's
			// machine.
			if _, err := os.Stat(abs); err != nil {
				return nil, bad(name, "font file “%s” not found", src.File)
			}
			if src.Style != "" && src.Style != "normal" && src.Style != "italic" {
				return nil, bad(name, "fonts[%d].sources[%d].style must be “normal” or “italic”", i, j)
			}
		}
	}
	return &m, nil
}

// FontPath resolves a declared font file, refusing anything outside.
func (r *Registry) FontPath(templateDir, file string) (string, bool) {
	if strings.HasPrefix(file, sharedPrefix) {
		name := strings.TrimPrefix(file, sharedPrefix)
		// A single segment: `@engine/../../etc/passwd` is a path, not a font.
		if name == "" || strings.ContainsAny(name, `\/`) {
			return "", false
		}
		return filepath.Join(r.SharedFonts, name), true
	}
	base, err := filepath.Abs(templateDir)
	if err != nil {
		return "", false
	}
	abs, err := filepath.Abs(filepath.Join(templateDir, file))
	if err != nil {
		return "", false
	}
	if !strings.HasPrefix(abs, base+string(filepath.Separator)) {
		return "", false
	}
	return abs, true
}

func known(v string, in []string) bool {
	for _, x := range in {
		if x == v {
			return true
		}
	}
	return false
}

// readIcons loads the glyph set, and never fails.
//
// A template with no icons is not an error: whether it needs any is decided by
// the section types it accepts, and confronting the two is the conformance
// kit's job. Failing here would take the whole registry down over a file that
// may legitimately be absent.
func readIcons(dir string) (map[string]theme.Glyph, []string) {
	out := map[string]theme.Glyph{}
	raw, err := os.ReadFile(filepath.Join(dir, "icons.json"))
	if err != nil {
		return out, nil
	}
	var parsed map[string]theme.Glyph
	if json.Unmarshal(raw, &parsed) != nil {
		return out, nil
	}
	for name, g := range parsed {
		if g.ViewBox != "" && g.Path != "" {
			out[name] = g
		}
	}
	order := make([]string, 0, len(out))
	for name := range out {
		order = append(order, name)
	}
	sort.Strings(order)
	return out, order
}

// Find looks a template up by UUID, then by name.
//
// UUID first: it is what documents store, and a template named after another's
// UUID must not be able to intercept it.
func (r *Registry) Find(ref string) *Template {
	key := strings.ToLower(strings.TrimSpace(ref))
	if key == "" {
		return nil
	}
	all, err := r.All()
	if err != nil {
		return nil
	}
	for _, t := range all {
		if t.UUID == key {
			return t
		}
	}
	for _, t := range all {
		if t.Name == key {
			return t
		}
	}
	return nil
}

// Resolve is Find that insists.
//
// An unknown reference is an error rather than a silent fallback: rendering
// someone's CV in a layout they did not choose, because theirs was renamed, is
// worse than telling them it is missing.
func (r *Registry) Resolve(ref string) (*Template, error) {
	if ref != "" {
		if hit := r.Find(ref); hit != nil {
			return hit, nil
		}
		all, _ := r.All()
		names := make([]string, 0, len(all))
		for _, t := range all {
			names = append(names, t.Name)
		}
		return nil, fmt.Errorf("unknown template: “%s” (available: %s)", ref, strings.Join(names, ", "))
	}
	if hit := r.Find(r.Default); hit != nil {
		return hit, nil
	}
	return nil, fmt.Errorf("default template “%s” not found", r.Default)
}

// ForDocument resolves the template a document names.
func (r *Registry) ForDocument(doc document.Doc) (*Template, error) {
	meta := document.Meta(doc)
	ref := document.Str(meta, "template")
	if ref == "" {
		// The pre-template field. A CV is a file people keep for years, and a
		// format change must never be the reason one stops opening.
		ref = document.Str(meta, "theme")
	}
	return r.Resolve(ref)
}

// Check refuses a document this template cannot draw, in terms the person
// editing it will recognise.
func (r *Registry) Check(doc document.Doc, t *Template) error {
	counts := map[string]int{}
	accepted := make([]string, 0, len(t.Sections))
	for _, s := range t.Sections {
		accepted = append(accepted, s.Type)
	}

	for _, raw := range document.Sections(doc) {
		s, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := document.Str(s, "id")
		kind := document.Str(s, "type")

		var slot *Slot
		for i := range t.Sections {
			if t.Sections[i].Type == kind {
				slot = &t.Sections[i]
				break
			}
		}
		if slot == nil {
			return fmt.Errorf("section “%s”: template “%s” does not render “%s” sections (it accepts: %s)",
				id, t.Title, kind, strings.Join(accepted, ", "))
		}
		if column := document.Str(s, "column"); !known(column, t.Columns) {
			return fmt.Errorf("section “%s”: template “%s” has no “%s” region (it has: %s)",
				id, t.Title, column, strings.Join(t.Columns, ", "))
		}
		counts[kind]++
		if slot.Max != nil && counts[kind] > *slot.Max {
			return fmt.Errorf("template “%s” accepts at most %d “%s” section(s)", t.Title, *slot.Max, kind)
		}
	}
	return nil
}

// FontDecls describes a template's fonts for the layout engine.
func (t *Template) FontDecls() []layout.FontDecl {
	out := make([]layout.FontDecl, 0, len(t.Fonts))
	for _, f := range t.Fonts {
		d := layout.FontDecl{Family: f.Family}
		for _, s := range f.Sources {
			d.Sources = append(d.Sources, layout.FontSourceDecl{
				File: s.File, Weight: s.Weight, Style: s.Style})
		}
		out = append(out, d)
	}
	return out
}
