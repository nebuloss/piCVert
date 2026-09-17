// Package document holds the document envelope, and how older ones are read.
//
// A cv.json separates what the ENGINE owns from what the TEMPLATE describes:
//
//	{
//	  "$schema": …,                       engine
//	  "meta":    { … },                   engine: language, template, timestamps
//	  "content": { identity, sections }   template-declared
//	}
//
// WHY THE ENVELOPE. Templates declare the fields a CV is made of. Left at the
// top level, one of those declarations would eventually be named `meta` or
// `$schema` and quietly shadow an engine key — and the collision would only
// show up as a setting that stops taking effect. One nesting level makes the
// two namespaces disjoint by construction, so a template can declare anything
// it likes without the engine having to reserve names.
//
// Documents written before the envelope carried `identity` and `sections` at
// the top level. They are lifted on read rather than rejected: a CV is a file
// people keep for years, and a format change must never be the reason one
// stops opening.
//
// # WHY A MAP AND NOT A STRUCT
//
// A document travels through this engine as a `map[string]any`, not as a typed
// struct, and that is a decision rather than an omission.
//
// The validator's own rule is that unknown properties are IGNORED, never
// rejected: that is how a document written by a newer engine survives being
// read by an older one. Decoding into a Go struct and re-encoding would delete
// every such property in silence — and the store rewrites the whole file on
// every save, so the loss would be immediate and total. Typed structs exist
// next door for rendering, where the shape is known and nothing is written
// back.
package document

// Doc is a CV document as it lives on disk and travels through the engine:
// whatever JSON actually contained, unknown members included.
type Doc map[string]any

// AsObject reads a value as a JSON object.
//
// It accepts both `map[string]any` and `Doc`, because a type assertion in Go
// matches the DYNAMIC type exactly: a `Doc` — a named type whose underlying
// type is that map — does NOT satisfy `.(map[string]any)`. A document that has
// already been through this package and comes back for another pass would
// otherwise be rejected as "not a JSON object", which is both wrong and
// bewildering. Everything that accepts a document goes through here.
func AsObject(v any) (map[string]any, bool) {
	switch t := v.(type) {
	case map[string]any:
		return t, t != nil
	case Doc:
		return t, t != nil
	}
	return nil, false
}

// Obj reads a member as an object. The second result says whether it was one;
// a missing member and a member holding a string are both "no".
func Obj(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	return AsObject(v)
}

// Arr reads a member as a list.
func Arr(m map[string]any, key string) ([]any, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	a, ok := v.([]any)
	return a, ok
}

// Str reads a member as a string, returning "" when it is absent or is
// something else. Callers that must tell "absent" from "empty" read the map
// directly.
func Str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// Truthy reproduces JavaScript's notion of a truthy value, because the
// behaviour being ported is written in terms of it: `!d.content` is true for
// an absent member, for null, for false, for zero and for the empty string.
// Getting this wrong would make a `"content": null` document take the modern
// path and fail with a confusing error instead of being lifted.
func Truthy(v any, present bool) bool {
	if !present || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	case int:
		return t != 0
	}
	return true
}

// IsLegacy reports whether a document predates the envelope: it has no
// `content`, but carries `identity` or `sections` at the top level.
//
// A member present and holding null still counts as carrying it — that is a
// document someone half-emptied by hand, and it should be lifted like any
// other rather than refused.
func IsLegacy(d map[string]any) bool {
	content, hasContent := d["content"]
	if Truthy(content, hasContent) {
		return false
	}
	_, hasIdentity := d["identity"]
	_, hasSections := d["sections"]
	return hasIdentity || hasSections
}

// NeedsUpgrade reports whether Upgrade would change anything.
func NeedsUpgrade(input any) bool {
	m, ok := AsObject(input)
	return ok && IsLegacy(m)
}

// Upgrade lifts a pre-envelope document into the current shape.
//
// It never modifies its argument: the caller may well be holding the document
// it just parsed and about to compare the two. Anything that is not a JSON
// object comes back untouched — that is not this function's error to report,
// and the validator will say it far better.
func Upgrade(input any) any {
	m, ok := AsObject(input)
	if !ok {
		return input
	}

	// Shallow copy, like the TypeScript spread. Nested values are shared: the
	// lift only moves top-level members, and copying the whole tree to move two
	// keys would be paid on every read.
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	if !IsLegacy(m) {
		return out
	}

	identity, hasIdentity := out["identity"]
	sections, hasSections := out["sections"]
	delete(out, "identity")
	delete(out, "sections")

	content := map[string]any{}
	if hasIdentity {
		content["identity"] = identity
	}
	// A falsy `sections` becomes an empty list rather than travelling on as
	// null: everything downstream iterates it, and "no sections" is a document
	// that should open, not one that should crash.
	if Truthy(sections, hasSections) {
		content["sections"] = sections
	} else {
		content["sections"] = []any{}
	}
	out["content"] = content
	return out
}

// Identity is the identity block of a document, if it has one.
func Identity(d Doc) (map[string]any, bool) {
	content, ok := Obj(d, "content")
	if !ok {
		return nil, false
	}
	return Obj(content, "identity")
}

// Sections is the section list of a document, empty when it has none.
func Sections(d Doc) []any {
	content, ok := Obj(d, "content")
	if !ok {
		return nil
	}
	s, _ := Arr(content, "sections")
	return s
}

// Meta is the engine's half of the envelope, empty when absent.
func Meta(d Doc) map[string]any {
	m, _ := Obj(d, "meta")
	return m
}
