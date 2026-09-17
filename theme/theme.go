// Package theme loads a template: its palette, its named styles, and how it
// composes a document into a page.
//
// # WHY A THEME IS DATA, NOT CODE
//
// A template used to ship two renderers — `layout.ts` for the browser and
// `pdf/index.ts` for the PDF — describing one design twice, in two languages,
// with nothing holding them together but care. They drifted, and the drift is
// what silently cut CVs off the bottom of the page.
//
// Collapsing them into one Go renderer would fix the drift and lose something
// worth more: a template is DATA ON DISK, and the engine discovers it by
// finding a directory. Adding a layout means adding a folder, and the
// conformance kit covers it from the moment it exists. Compiling templates in
// would make a new one a code change, a rebuild and a redeploy.
//
// So the composition is data as well. A theme states, per section type, the
// tree of boxes it wants and where the document's values go in it. The engine
// walks that tree exactly as the validator walks the field tree — one
// description, read by everything.
//
// # WHAT THAT BUYS
//
//   - A new template is a directory. No Go, no rebuild.
//   - A theme cannot invent a style property or a node kind: both vocabularies
//     are closed, so every theme is one the engine can lay out and both
//     emitters can draw.
//   - The conformance kit can synthesise a document from the field tree and put
//     any template through the same battery, because every template answers the
//     same way.
//
// # THE FILE
//
//	template.json   identity, regions, accepted sections, fonts   (unchanged)
//	theme.json      tokens, named styles, composition             (this package)
//	icons.json      the glyph set                                  (unchanged)
//	cover.svg       the picker illustration                        (unchanged)
package theme

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"picvert/internal/layout"
)

// Theme is a template's design, loaded.
type Theme struct {
	// Tokens are the palette, by name. A style may write `$primary` wherever a
	// colour is expected, so the palette is stated once and a recolour is a
	// four-line change.
	Tokens map[string]string `json:"tokens"`

	// Styles are the named styles, by name. A composition refers to them by
	// name, so a metric used in six places is written once.
	Styles map[string]RawStyle `json:"styles"`

	// Page is the frame: the page box, the header, and where the regions go.
	Page *Element `json:"page"`

	// Sections is one composition per section type the template accepts. A
	// template that declares a slot in template.json but composes nothing for
	// it would render a blank card; the conformance kit refuses that.
	Sections map[string]*Element `json:"sections"`

	dir string
}

// RawStyle is a style as written in theme.json.
//
// Separate from layout.Style because the file says `"padding": "11 15"` and
// `"background": "$card"`, which are shorthands worth having in a file and not
// worth carrying into the engine. Resolve turns one into the other, once, at
// load.
type RawStyle struct {
	// Extends names another style to start from, so a variant states only its
	// difference. One level of inheritance, deliberately: a chain is a thing to
	// trace, and a theme is meant to be read.
	Extends string `json:"extends,omitempty"`

	Display string `json:"display,omitempty"` // block | row | row-wrap | text | image | ellipse

	Width    any     `json:"width,omitempty"`
	Height   any     `json:"height,omitempty"`
	Grow     float64 `json:"grow,omitempty"`
	Gap      float64 `json:"gap,omitempty"`
	CrossGap float64 `json:"crossGap,omitempty"`
	Padding  string  `json:"padding,omitempty"` // "v h" | "t r b l" | "all"
	Margin   string  `json:"margin,omitempty"`

	Align   string `json:"align,omitempty"`   // start | center | end | baseline | stretch
	Justify string `json:"justify,omitempty"` // start | between
	Self    string `json:"self,omitempty"`

	Background any     `json:"background,omitempty"` // "$token" | "#rrggbb" | gradient object
	Radius     float64 `json:"radius,omitempty"`
	Border     string  `json:"border,omitempty"` // "1.5 $outline"
	Clip       bool    `json:"clip,omitempty"`

	Family     string  `json:"family,omitempty"`
	Size       float64 `json:"size,omitempty"`
	Weight     int     `json:"weight,omitempty"`
	Italic     bool    `json:"italic,omitempty"`
	Colour     string  `json:"colour,omitempty"`
	LineHeight float64 `json:"lineHeight,omitempty"`
	Letter     float64 `json:"letter,omitempty"`
	Uppercase  bool    `json:"uppercase,omitempty"`
}

// Element is one node of a composition.
//
// It is a node tree with three additions, and only three. Each exists because
// a CV cannot be drawn without it, and nothing more was added: a composition
// language that grows keywords becomes a programming language, and then a theme
// is code again with worse tools.
type Element struct {
	// Style is a named style, or an inline one.
	Style  string    `json:"style,omitempty"`
	Inline *RawStyle `json:"inline,omitempty"`

	// ID names this node so overflow can report it. `$id` interpolates the
	// section's own identifier.
	ID string `json:"id,omitempty"`

	// --- the three additions ------------------------------------------------

	// Repeat iterates a list field, drawing Children once per entry. Inside,
	// bindings resolve against the entry.
	Repeat string `json:"repeat,omitempty"`

	// When draws this node only if the named field has a value. That is how
	// “a note, if there is one” is said without a conditional.
	When string `json:"when,omitempty"`

	// Text is the content of a text node: literal, or `{field}` to bind. Rich
	// fields keep their `<b>` and become spans.
	Text string `json:"text,omitempty"`

	// Icon binds a glyph from the template's icon set.
	Icon string `json:"icon,omitempty"`
	// Image binds a picture — the portrait.
	Image string `json:"image,omitempty"`
	// Slot marks where the engine injects something it owns: a region's
	// sections, or a section's own composition.
	Slot string `json:"slot,omitempty"`

	Children []*Element `json:"children,omitempty"`
}

// Load reads a theme from a template directory.
func Load(dir string) (*Theme, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "theme.json"))
	if err != nil {
		return nil, fmt.Errorf("no theme.json in %s", filepath.Base(dir))
	}
	var t Theme
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("theme.json in %s is not readable: %w", filepath.Base(dir), err)
	}
	t.dir = dir
	if t.Page == nil {
		return nil, fmt.Errorf("theme.json in %s declares no page", filepath.Base(dir))
	}
	// Resolved once, at load, so a typo in a colour name is a startup failure
	// rather than a field that renders in the wrong colour months later.
	for name, s := range t.Styles {
		if _, err := t.Resolve(s); err != nil {
			return nil, fmt.Errorf("style %q in %s: %w", name, filepath.Base(dir), err)
		}
	}
	return &t, nil
}

// colour resolves `$token` against the palette, and passes anything else
// through.
func (t *Theme) colour(v string) (string, error) {
	if !strings.HasPrefix(v, "$") {
		return v, nil
	}
	hit, ok := t.Tokens[strings.TrimPrefix(v, "$")]
	if !ok {
		return "", fmt.Errorf("unknown token %q", v)
	}
	return hit, nil
}

// edges parses "10", "10 15" or "1 2 3 4" the way CSS does, because that is
// what someone writing a theme will expect and getting it wrong is silent.
func edges(spec string) (layout.Edges, error) {
	if spec == "" {
		return layout.Edges{}, nil
	}
	parts := strings.Fields(spec)
	nums := make([]float64, 0, 4)
	for _, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return layout.Edges{}, fmt.Errorf("bad length %q", p)
		}
		nums = append(nums, v)
	}
	switch len(nums) {
	case 1:
		return layout.All(nums[0]), nil
	case 2:
		return layout.XY(nums[0], nums[1]), nil
	case 4:
		return layout.Edges{Top: nums[0], Right: nums[1], Bottom: nums[2], Left: nums[3]}, nil
	}
	return layout.Edges{}, fmt.Errorf("expected 1, 2 or 4 lengths, got %d", len(nums))
}

var displays = map[string]layout.Display{
	"":         layout.Block,
	"block":    layout.Block,
	"row":      layout.Row,
	"row-wrap": layout.RowWrap,
	"text":     layout.Text,
	"image":    layout.Image,
	"ellipse":  layout.Ellipse,
}

var aligns = map[string]layout.Align{
	"": layout.AlignStart, "start": layout.AlignStart, "center": layout.AlignCenter,
	"end": layout.AlignEnd, "baseline": layout.AlignBaseline, "stretch": layout.AlignStretch,
}

// Resolve turns a written style into one the engine can use.
func (t *Theme) Resolve(r RawStyle) (layout.Style, error) {
	var s layout.Style

	if r.Extends != "" {
		base, ok := t.Styles[r.Extends]
		if !ok {
			return s, fmt.Errorf("extends unknown style %q", r.Extends)
		}
		if base.Extends != "" {
			// One level only. A chain is a thing to trace, and the point of a
			// theme file is that it can be read straight through.
			return s, fmt.Errorf("style %q itself extends: only one level is allowed", r.Extends)
		}
		resolved, err := t.Resolve(base)
		if err != nil {
			return s, err
		}
		s = resolved
	}

	d, ok := displays[r.Display]
	if !ok {
		return s, fmt.Errorf("unknown display %q", r.Display)
	}
	if r.Display != "" || s.Display == 0 {
		s.Display = d
	}

	if v, ok := length(r.Width); ok {
		s.Width = v
	}
	if v, ok := length(r.Height); ok {
		s.Height = v
	}
	if r.Grow != 0 {
		s.Grow = r.Grow
	}
	if r.Gap != 0 {
		s.Gap = r.Gap
	}
	if r.CrossGap != 0 {
		s.CrossGap = r.CrossGap
	}
	if r.Padding != "" {
		e, err := edges(r.Padding)
		if err != nil {
			return s, fmt.Errorf("padding: %w", err)
		}
		s.Padding = e
	}
	if r.Margin != "" {
		e, err := edges(r.Margin)
		if err != nil {
			return s, fmt.Errorf("margin: %w", err)
		}
		s.Margin = e
	}
	if r.Align != "" {
		a, ok := aligns[r.Align]
		if !ok {
			return s, fmt.Errorf("unknown align %q", r.Align)
		}
		s.Align = a
	}
	if r.Justify == "between" {
		s.Justify = layout.JustifyBetween
	}
	if r.Self != "" {
		a, ok := aligns[r.Self]
		if !ok {
			return s, fmt.Errorf("unknown self %q", r.Self)
		}
		s.SelfAlign = &a
	}

	if r.Background != nil {
		fill, err := t.fill(r.Background)
		if err != nil {
			return s, err
		}
		s.Background = fill
	}
	if r.Radius != 0 {
		s.Radius = r.Radius
	}
	if r.Border != "" {
		parts := strings.Fields(r.Border)
		if len(parts) != 2 {
			return s, fmt.Errorf("border: expected \"<width> <colour>\", got %q", r.Border)
		}
		w, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return s, fmt.Errorf("border width %q", parts[0])
		}
		c, err := t.colour(parts[1])
		if err != nil {
			return s, fmt.Errorf("border: %w", err)
		}
		s.Border.Width, s.Border.Colour = w, c
	}
	if r.Clip {
		s.Clip = true
	}

	if r.Family != "" {
		s.Family = r.Family
	}
	if r.Size != 0 {
		s.Size = r.Size
	}
	if r.Weight != 0 {
		s.Weight = layout.Weight(r.Weight)
	}
	if r.Italic {
		s.Italic = true
	}
	if r.Colour != "" {
		c, err := t.colour(r.Colour)
		if err != nil {
			return s, fmt.Errorf("colour: %w", err)
		}
		s.Colour = c
	}
	if r.LineHeight != 0 {
		s.LineHeight = r.LineHeight
	}
	if r.Letter != 0 {
		s.Letter = r.Letter
	}
	if r.Uppercase {
		s.Uppercase = true
	}
	return s, nil
}

func length(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	}
	return 0, false
}

// fill reads a background: a colour, or a gradient object.
func (t *Theme) fill(v any) (layout.Fill, error) {
	switch g := v.(type) {
	case string:
		c, err := t.colour(g)
		return layout.Fill{Colour: c}, err
	case map[string]any:
		grad := &layout.Gradient{Angle: 135}
		if a, ok := g["angle"].(float64); ok {
			grad.Angle = a
		}
		stops, _ := g["stops"].([]any)
		for _, raw := range stops {
			st, _ := raw.(map[string]any)
			at, _ := st["at"].(float64)
			col, _ := st["colour"].(string)
			c, err := t.colour(col)
			if err != nil {
				return layout.Fill{}, fmt.Errorf("gradient: %w", err)
			}
			grad.Stops = append(grad.Stops, layout.Stop{At: at, Colour: c})
		}
		if len(grad.Stops) < 2 {
			return layout.Fill{}, fmt.Errorf("gradient needs at least two stops")
		}
		return layout.Fill{Gradient: grad}, nil
	}
	return layout.Fill{}, fmt.Errorf("background must be a colour or a gradient")
}

// StyleNamed resolves a style by name, for a composition.
func (t *Theme) StyleNamed(name string) (layout.Style, error) {
	if name == "" {
		return layout.Style{}, nil
	}
	r, ok := t.Styles[name]
	if !ok {
		return layout.Style{}, fmt.Errorf("unknown style %q", name)
	}
	return t.Resolve(r)
}

// PagePadding is the page's own bottom padding, which is where the usable area
// ends.
//
// Read from the theme rather than assumed: overflow is measured against it, and
// a card that reaches the paper edge has already lost the margin the design
// gave it.
func (t *Theme) PagePadding() float64 {
	if t.Page == nil {
		return 0
	}
	name := t.Page.Style
	if t.Page.Inline != nil && t.Page.Inline.Padding != "" {
		if e, err := edges(t.Page.Inline.Padding); err == nil {
			return e.Bottom
		}
	}
	if s, ok := t.Styles[name]; ok {
		if e, err := edges(s.Padding); err == nil {
			return e.Bottom
		}
	}
	return 0
}
