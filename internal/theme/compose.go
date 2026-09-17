package theme

import (
	"fmt"
	"regexp"
	"strings"

	"picvert/internal/document"
	"picvert/internal/layout"
)

// Compose turns a document into a tree of layout nodes, following what the
// theme declares.
//
// This is the whole of what used to be `layout.ts` and `pdf/index.ts` — two
// renderers, 332 lines, one design stated twice. It is generic: it knows about
// binding and repetition, and nothing about what a CV contains. Which section
// types exist, and what each is made of, is the theme's business and the field
// tree's.
type Composer struct {
	Theme *Theme
	// Icons is the template's glyph set, for `icon` bindings.
	Icons map[string]Glyph
	// Regions is the list of areas the page declares, in reading order.
	Regions []string
}

// Glyph is one icon: a viewBox and a path, which is all an SVG icon is.
type Glyph struct {
	ViewBox string
	Path    string
}

// scope is what bindings resolve against: the current value, plus the section
// and the document above it.
//
// Three levels rather than one, because a composition legitimately reaches
// outward: inside a repeat over bullets, `{role}` should still find the block's
// role. Resolution walks outward and stops at the first hit, which is what
// anyone writing the theme will expect.
type scope struct {
	value   map[string]any
	section map[string]any
	doc     document.Doc
	// index is the position in the current repeat, 1-based, for `{#}`.
	index int
}

func (s scope) lookup(key string) (any, bool) {
	if key == "#" {
		return s.index, true
	}
	for _, level := range []map[string]any{s.value, s.section} {
		if level == nil {
			continue
		}
		if v, ok := level[key]; ok {
			return v, true
		}
	}
	if s.doc != nil {
		if identity, ok := document.Identity(s.doc); ok {
			if v, ok := identity[key]; ok {
				return v, true
			}
		}
		if v, ok := document.Meta(s.doc)[key]; ok {
			return v, true
		}
	}
	return nil, false
}

func (s scope) str(key string) string {
	v, _ := s.lookup(key)
	out, _ := v.(string)
	return out
}

// present reports whether a field has anything worth drawing. Absent, null, the
// empty string and the empty list all count as nothing — the same rule the diff
// uses, so `when` agrees with what the journal considers a change.
func (s scope) present(key string) bool {
	v, ok := s.lookup(key)
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t) != ""
	case []any:
		return len(t) > 0
	case bool:
		return t
	}
	return true
}

var binding = regexp.MustCompile(`\{([#a-zA-Z0-9_.]+)\}`)

// interpolate replaces `{field}` with its value, leaving literal text alone.
func (s scope) interpolate(text string) string {
	return binding.ReplaceAllStringFunc(text, func(whole string) string {
		key := whole[1 : len(whole)-1]
		v, ok := s.lookup(key)
		if !ok || v == nil {
			return ""
		}
		switch t := v.(type) {
		case string:
			return t
		case float64:
			return trimFloat(t)
		case int:
			return fmt.Sprint(t)
		case bool:
			if t {
				return "true"
			}
			return ""
		}
		return ""
	})
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%g", f)
	return s
}

// boldRun splits rich text into spans on `<b>`.
//
// Bold must not become its own node: a bold word in the middle of a sentence
// has to wrap with the sentence, and a separate box would break the line there.
// Spans are how a run stays part of the paragraph the engine measures.
var boldTag = regexp.MustCompile(`(?i)<b>(.*?)</b>`)

func spansOf(text string) []layout.Span {
	var out []layout.Span
	last := 0
	for _, m := range boldTag.FindAllStringSubmatchIndex(text, -1) {
		if m[0] > last {
			out = append(out, layout.Span{Text: text[last:m[0]]})
		}
		out = append(out, layout.Span{Text: text[m[2]:m[3]], Bold: true})
		last = m[1]
	}
	if last < len(text) {
		out = append(out, layout.Span{Text: text[last:]})
	}
	if len(out) == 0 {
		return []layout.Span{{Text: ""}}
	}
	return out
}

// Page composes the whole document.
func (c *Composer) Page(doc document.Doc) (*layout.Node, error) {
	sc := scope{doc: doc, section: nil, value: nil}
	nodes, err := c.element(c.Theme.Page, sc, doc)
	if err != nil {
		return nil, err
	}
	if len(nodes) != 1 {
		return nil, fmt.Errorf("the page composition must produce exactly one node, got %d", len(nodes))
	}
	return nodes[0], nil
}

// element expands one declared element into zero or more layout nodes.
//
// Zero when `when` is false or a repeat is empty; more than one when it
// repeats. Returning a slice rather than a node is what lets those two be
// ordinary cases instead of special ones.
func (c *Composer) element(e *Element, sc scope, doc document.Doc) ([]*layout.Node, error) {
	if e == nil {
		return nil, nil
	}
	if e.When != "" && !sc.present(e.When) {
		return nil, nil
	}

	// A repeat draws its children once per entry, with the entry in scope.
	if e.Repeat != "" {
		raw, _ := sc.lookup(e.Repeat)
		entries, _ := raw.([]any)
		var out []*layout.Node
		for i, entry := range entries {
			inner := sc
			inner.index = i + 1
			if m, ok := entry.(map[string]any); ok {
				inner.value = m
			} else {
				// A list of plain strings — bullets, lines. `{.}` names the
				// entry itself, so a scalar list needs no special syntax.
				inner.value = map[string]any{".": entry}
			}
			// The repeat element itself is not drawn; its children are, once
			// per entry. Drawing it too would wrap every entry in an extra box
			// nobody asked for.
			for _, child := range e.Children {
				nodes, err := c.element(child, inner, doc)
				if err != nil {
					return nil, err
				}
				out = append(out, nodes...)
			}
		}
		return out, nil
	}

	style, err := c.styleOf(e)
	if err != nil {
		return nil, err
	}

	node := &layout.Node{Style: style}
	if e.ID != "" {
		node.ID = sc.interpolate(e.ID)
	}

	switch {
	case e.Text != "":
		node.Style.Display = layout.Text
		text := sc.interpolate(e.Text)
		if node.Style.Uppercase {
			text = strings.ToUpper(text)
		}
		node.Spans = spansOf(text)

	case e.Icon != "":
		name := sc.interpolate(e.Icon)
		glyph, ok := c.Icons[name]
		if !ok {
			// Refused rather than drawn empty. An unknown icon used to render a
			// blank box, so switching template could erase every contact line
			// of a CV and look as though it had worked.
			return nil, fmt.Errorf("unknown icon %q", name)
		}
		node.Style.Display = layout.Image
		node.Src = "icon:" + glyph.ViewBox + "|" + glyph.Path

	case e.Image != "":
		node.Style.Display = layout.Image
		node.Src = sc.interpolate(e.Image)
		node.Alt = sc.str("photoAlt")
		if node.Alt == "" {
			node.Alt = sc.str("name")
		}

	case e.Slot != "":
		children, err := c.slot(e.Slot, sc, doc)
		if err != nil {
			return nil, err
		}
		node.Children = children

	default:
		for _, child := range e.Children {
			nodes, err := c.element(child, sc, doc)
			if err != nil {
				return nil, err
			}
			node.Children = append(node.Children, nodes...)
		}
	}

	return []*layout.Node{node}, nil
}

func (c *Composer) styleOf(e *Element) (layout.Style, error) {
	if e.Inline != nil {
		base := *e.Inline
		if e.Style != "" && base.Extends == "" {
			base.Extends = e.Style
		}
		return c.Theme.Resolve(base)
	}
	return c.Theme.StyleNamed(e.Style)
}

// slot fills in what the engine owns rather than the theme.
//
// Two of them, and they are the seam between “how this template draws a CV” and
// “what this CV contains”: a region holds whichever sections the document put
// there, and a section holds whatever its own composition says.
func (c *Composer) slot(name string, sc scope, doc document.Doc) ([]*layout.Node, error) {
	if name == "body" {
		return nil, fmt.Errorf("slot \"body\" is only valid inside a section composition")
	}
	// Any other slot name is a region: draw the sections assigned to it.
	var out []*layout.Node
	for _, raw := range document.Sections(doc) {
		section, ok := raw.(map[string]any)
		if !ok || document.Str(section, "column") != name {
			continue
		}
		node, err := c.section(section, doc)
		if err != nil {
			return nil, err
		}
		if node != nil {
			out = append(out, node)
		}
	}
	return out, nil
}

// section composes one section with the theme's rule for its type.
func (c *Composer) section(section map[string]any, doc document.Doc) (*layout.Node, error) {
	kind := document.Str(section, "type")
	decl, ok := c.Theme.Sections[kind]
	if !ok {
		// The manifest said this template accepts the type and the theme draws
		// nothing for it: a blank card, which is the failure a template system
		// exists to prevent.
		return nil, fmt.Errorf("template accepts %q sections but composes none", kind)
	}
	sc := scope{value: section, section: section, doc: doc}
	nodes, err := c.element(decl, sc, doc)
	if err != nil {
		return nil, fmt.Errorf("section %q: %w", document.Str(section, "id"), err)
	}
	if len(nodes) == 0 {
		return nil, nil
	}
	if len(nodes) > 1 {
		return nil, fmt.Errorf("section %q composed %d nodes, expected one card",
			document.Str(section, "id"), len(nodes))
	}
	// Named with the section's identifier, so overflow can point at it. The
	// theme may override with its own `id`, but the default is the one the
	// editor already uses to address a section.
	if nodes[0].ID == "" {
		nodes[0].ID = document.Str(section, "id")
	}
	return nodes[0], nil
}
