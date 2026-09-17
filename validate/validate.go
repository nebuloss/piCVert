// Package validate is the authority on whether a document may be written.
//
// What a document is MADE OF lives next door in `fields`, and this package
// walks that tree: one description, one set of rules. Before, the constraints
// were written out by hand while the editor built its forms from its own idea
// of the same shape — the two could disagree, and the disagreement only
// surfaced as a rejected save with a message nobody could act on.
//
// The document may arrive over the network, so anything not explicitly
// expected is refused rather than let through and patched up afterwards.
// Unknown extra properties are ignored, not rejected: they are how a document
// written by a newer version of the engine survives being read by an older
// one.
//
// # WHAT REPLACES THE TYPE PREDICATE
//
// In TypeScript `validate` is declared `asserts doc is CvDocument`: past the
// call, the compiler knows. Go has nothing equivalent, and a plain
// `func Validate(d Doc) error` would leave every caller free to use an
// unvalidated document — the compiler would not care.
//
// So validation PRODUCES a value instead of blessing one. Only `Validate` can
// build a `Checked`, and everything that may only touch a valid document takes
// a `Checked` rather than a `Doc`. The guarantee survives the change of
// language, carried by the type system rather than by discipline.
//
// Ported from engine/lib/validate.ts. Error messages are reproduced verbatim,
// including their punctuation: the editor shows them to the person typing, and
// the test suite asserts on them.
package validate

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"picvert/internal/document"
	"picvert/internal/fields"
)

// Error is a refusal, and where in the document it happened.
type Error struct {
	Path string
	Msg  string
}

func (e *Error) Error() string { return e.Path + ": " + e.Msg }

func fail(path, format string, args ...any) error {
	return &Error{Path: path, Msg: fmt.Sprintf(format, args...)}
}

// Checked is a document that has been through Validate.
//
// The field is unexported so this package is the only one that can produce
// one. That is the whole point: a function taking a Checked cannot be handed
// something that merely looks like a document.
type Checked struct {
	doc document.Doc
}

// Doc returns the underlying document. Reading a validated document is always
// safe; writing through this map is not, and callers that mutate must
// re-validate.
func (c Checked) Doc() document.Doc { return c.doc }

// Columns is the whole region vocabulary.
//
// A section is validated against ALL of them, not against the regions its
// template happens to draw: moving a CV to a narrower template must not make
// the file itself invalid. The template registry is what refuses a section its
// layout cannot render, and it says so in its own terms.
var Columns = []string{"left", "right", "full"}

// join builds `a.b` at depth and `b` at the root, so paths read like the JSON
// they point at.
func join(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// jsType names a value the way JavaScript's `typeof` does, because the error
// messages being reproduced quote it. Arrays and null are both "object" there.
func jsType(v any, present bool) string {
	if !present {
		return "undefined"
	}
	switch v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, int:
		return "number"
	}
	return "object"
}

// length counts UTF-16 code units, which is what JavaScript's `String.length`
// counts.
//
// Not bytes and not runes: an emoji is two units there, one rune here, and
// four bytes. A limit enforced in a different unit on each side would let the
// editor accept what the server refuses, on exactly the characters people are
// least able to count themselves.
func length(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// isAbsent matches the TypeScript `v === null || v === undefined`: a missing
// member and an explicit null, and nothing else. The empty string, zero, false
// and the empty list are all PRESENT values with their own rules.
func isAbsent(v any, present bool) bool { return !present || v == nil }

var (
	anyTag   = regexp.MustCompile(`<[^>]+>`)
	boldOnly = regexp.MustCompile(`(?i)^</?b>$`)
	// A photo is a plain file name: no separator anywhere, and no leading
	// parent reference. Anything else is a path, and a path is how a field
	// meant to name a file inside the profile reaches outside it.
	pathish = regexp.MustCompile(`[\\/]|^\.\.`)
)

func checkText(f fields.Field, v any, present bool, p string) error {
	s, ok := v.(string)
	if !ok {
		return fail(p, "expected text, got %s", jsType(v, present))
	}
	if f.Required && strings.TrimSpace(s) == "" {
		return fail(p, "cannot be empty")
	}
	max := fields.DefaultTextMax
	if f.Max != nil {
		max = *f.Max
	}
	if n := length(s); n > max {
		return fail(p, "too long (%d characters, %d maximum)", n, max)
	}
	if f.Kind == fields.KindRich {
		// Everything else would be escaped by the renderer and come out as
		// visible angle brackets. Refusing it here is the only place the
		// person typing can be told why.
		var bad []string
		for _, tag := range anyTag.FindAllString(s, -1) {
			if !boldOnly.MatchString(tag) {
				bad = append(bad, tag)
			}
		}
		if len(bad) > 0 {
			if len(bad) > 3 {
				bad = bad[:3]
			}
			return fail(p, "only <b> is allowed; remove: %s", strings.Join(bad, " "))
		}
	}
	return nil
}

func checkPhoto(f fields.Field, v any, present bool, p string) error {
	max := fields.DefaultPhotoMax
	if f.Max != nil {
		max = *f.Max
	}
	// Deliberately NOT carrying `required` across: a photo field left empty is
	// a CV without a portrait, which is a choice rather than an error.
	if err := checkText(fields.Field{Kind: fields.KindText, Key: f.Key, Label: f.Label, Max: &max}, v, present, p); err != nil {
		return err
	}
	if pathish.MatchString(v.(string)) {
		return fail(p, "must be a plain file name inside the profile folder")
	}
	return nil
}

func checkNumber(f fields.Field, v any, p string) error {
	lo, hi := 0, 0
	if f.Min != nil {
		lo = *f.Min
	}
	if f.Max != nil {
		hi = *f.Max
	}
	n, ok := v.(float64)
	// A bool is not a number even though it converts to one, and NaN fails
	// every comparison — including against itself, which is how it is caught.
	if !ok || n != n || n < float64(lo) || n > float64(hi) {
		return fail(p, "expected a number between %d and %d", lo, hi)
	}
	return nil
}

func checkBool(v any, present bool, p string) error {
	if _, ok := v.(bool); !ok {
		return fail(p, "expected true or false, got %s", jsType(v, present))
	}
	return nil
}

func checkEnum(f fields.Field, v any, present bool, p string) error {
	value := v
	if isAbsent(v, present) {
		if f.Default != nil {
			value = *f.Default
		} else {
			value = ""
		}
	}
	for _, allowed := range f.Values {
		if s, ok := value.(string); ok && s == allowed.Value {
			return nil
		}
	}
	expected := make([]string, 0, len(f.Values))
	for _, allowed := range f.Values {
		if allowed.Value == "" {
			expected = append(expected, `""`)
		} else {
			expected = append(expected, allowed.Value)
		}
	}
	// The message quotes what the caller actually sent, not the defaulted
	// value: being told `invalid value ""` when one sent null helps nobody.
	return fail(p, "invalid value “%s” (expected: %s)", jsString(v, present), strings.Join(expected, ", "))
}

// jsString renders a value the way JavaScript's String() would, for the one
// error message that quotes it back.
func jsString(v any, present bool) string {
	if !present {
		return "undefined"
	}
	if v == nil {
		return "null"
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []any:
		parts := make([]string, len(t))
		for i, x := range t {
			parts[i] = jsString(x, true)
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func checkList(f fields.Field, v any, p string) error {
	items, ok := v.([]any)
	if !ok {
		return fail(p, "expected a list")
	}
	if f.Min != nil && len(items) < *f.Min {
		return fail(p, "at least %d item(s) expected", *f.Min)
	}
	if f.Of == nil {
		return nil
	}
	for i, item := range items {
		if err := checkValue(*f.Of, item, true, fmt.Sprintf("%s[%d]", p, i)); err != nil {
			return err
		}
	}
	return nil
}

func checkGroup(fs []fields.Field, v any, p string) error {
	record, ok := document.AsObject(v)
	if !ok {
		return fail(p, "expected a JSON object")
	}
	// Declaration order, so the first complaint is about the first field the
	// person filling the form would have reached.
	for _, sub := range fs {
		value, present := record[sub.Key]
		if err := checkValue(sub, value, present, join(p, sub.Key)); err != nil {
			return err
		}
	}
	// Unknown members are left alone on purpose. See the package comment.
	return nil
}

// checkValue applies the one rule that belongs to this field's kind.
func checkValue(f fields.Field, v any, present bool, p string) error {
	if isAbsent(v, present) {
		switch f.Kind {
		case fields.KindEnum:
			// An enum may have a default, so absence is not yet a verdict.
			return checkEnum(f, v, present, p)
		case fields.KindList:
			// An absent list is an empty list — unless a minimum was asked for,
			// in which case the absence is exactly what fails.
			if f.Min != nil && *f.Min > 0 {
				return fail(p, "at least %d item(s) expected", *f.Min)
			}
			return nil
		}
		if f.Required {
			return fail(p, "required field is missing")
		}
		return nil
	}

	switch f.Kind {
	case fields.KindText, fields.KindRich, fields.KindIcon:
		return checkText(f, v, present, p)
	case fields.KindPhoto:
		return checkPhoto(f, v, present, p)
	case fields.KindNumber:
		return checkNumber(f, v, p)
	case fields.KindBool:
		return checkBool(v, present, p)
	case fields.KindEnum:
		return checkEnum(f, v, present, p)
	case fields.KindList:
		return checkList(f, v, p)
	case fields.KindGroup:
		return checkGroup(f.Fields, v, p)
	}
	return nil
}

// SectionFields is everything a section of this type is made of: what every
// section has, its region, and its own payload.
func SectionFields(sectionType string) []fields.Field {
	out := make([]fields.Field, 0, len(fields.SectionCommon)+1+len(fields.SectionFields[sectionType]))
	out = append(out, fields.SectionCommon...)
	out = append(out, fields.ColumnField(Columns))
	out = append(out, fields.SectionFields[sectionType]...)
	return out
}

func isSectionType(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	for _, t := range fields.SectionTypeOrder {
		if t == s {
			return true
		}
	}
	return false
}

var sectionIDMax = 60

var sectionIDField = fields.Field{
	Kind: fields.KindText, Key: "id", Label: "Identifier",
	Max: &sectionIDMax, Required: true,
}

// Validate checks a whole document and, on success, returns the token that
// proves it.
//
// It stops at the FIRST problem rather than collecting them all. A document is
// refused as a whole, and a list of twelve complaints — most of them knock-on
// effects of the first — is harder to act on than one.
func Validate(input any) (Checked, error) {
	// Through document.AsObject, so that a Doc — a named map type — is
	// accepted as well as a bare one. See the note there.
	d, ok := document.AsObject(input)
	if !ok {
		return Checked{}, fail("document", "expected a JSON object")
	}

	// A falsy `meta` is read as an empty one: the engine's own half is entirely
	// optional, and a CV that never named its language is a valid CV. A meta
	// that is present but is not an object falls through to checkGroup, which
	// says so properly.
	metaValue, metaPresent := d["meta"]
	meta := any(map[string]any{})
	if document.Truthy(metaValue, metaPresent) {
		meta = metaValue
	}
	if err := checkGroup(fields.MetaFields, meta, "meta"); err != nil {
		return Checked{}, err
	}

	content, ok := document.Obj(d, "content")
	if !ok {
		return Checked{}, fail("content", "required section is missing")
	}

	identity, identityPresent := content["identity"]
	if !document.Truthy(identity, identityPresent) {
		return Checked{}, fail("content.identity", "required section is missing")
	}
	if err := checkGroup(fields.IdentityFields, identity, "content.identity"); err != nil {
		return Checked{}, err
	}

	sections, ok := document.Arr(content, "sections")
	if !ok {
		return Checked{}, fail("content.sections", "expected a list")
	}

	seen := make(map[string]bool, len(sections))
	for i, raw := range sections {
		p := fmt.Sprintf("content.sections[%d]", i)
		section, ok := raw.(map[string]any)
		if !ok || section == nil {
			return Checked{}, fail(p, "expected a JSON object")
		}

		id, idPresent := section["id"]
		if err := checkValue(sectionIDField, id, idPresent, p+".id"); err != nil {
			return Checked{}, err
		}
		// An identifier is how the editor addresses a section and how overflow
		// names one. Two sections sharing it means every such reference picks
		// one of them at random.
		sid := id.(string)
		if seen[sid] {
			return Checked{}, fail(p+".id", "identifier “%s” is already used", sid)
		}
		seen[sid] = true

		sectionType, typePresent := section["type"]
		if !isSectionType(sectionType) {
			return Checked{}, fail(p+".type", "invalid value “%s” (expected: %s)",
				jsString(sectionType, typePresent), strings.Join(fields.SectionTypeOrder, ", "))
		}

		if err := checkGroup(SectionFields(sectionType.(string)), section, p); err != nil {
			return Checked{}, err
		}
	}

	return Checked{doc: d}, nil
}
