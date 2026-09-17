// Package html renders markup that escapes by default.
//
// The rule is inverted from the usual one: interpolated values are escaped
// unless someone says otherwise, rather than raw unless someone remembers to
// escape. A CV carries a name, a job title and free text written by whoever is
// editing it, and the one place that must never be got wrong is also the place
// one writes hundreds of times.
//
// Ported from engine/lib/html.ts.
package html

import (
	"fmt"
	"regexp"
	"strings"
)

// Exactly four characters, and the apostrophe is NOT among them.
//
// Every attribute in every theme is double-quoted, so escaping the double
// quote is enough to make an attribute safe to close. Escaping the apostrophe
// too would fill a French CV with `&#39;` — visible in the source, identical
// on screen, and bought for nothing.
var escaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
)

// Escape renders a value as text that cannot become markup.
func Escape(v any) string { return escaper.Replace(stringOf(v)) }

// Raw is markup that has already been made safe, and must not be escaped
// again.
//
// A distinct type rather than a convention: escaping twice turns `&amp;` into
// `&amp;amp;` and is only noticed by a reader, while not escaping at all is a
// vulnerability. Making "already safe" a type means the compiler can tell the
// two apart.
type Raw struct{ Value string }

func NewRaw(v any) Raw       { return Raw{Value: stringOf(v)} }
func (r Raw) String() string { return r.Value }

// stringOf renders a value the way the TypeScript's String() did, so that the
// two implementations produce identical documents.
func stringOf(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case Raw:
		return t.Value
	case *Raw:
		if t == nil {
			return "null"
		}
		return t.Value
	case bool:
		if t {
			return "true"
		}
		return "false"
	case fmt.Stringer:
		return t.String()
	}
	return fmt.Sprint(v)
}

// stringify is what an interpolated value becomes.
func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	// Booleans render as nothing at all, in either state. They appear in
	// templates as `cond && fragment`, where the false branch must vanish
	// rather than print the word "false".
	case bool:
		return ""
	case Raw:
		return t.Value
	case *Raw:
		if t == nil {
			return ""
		}
		return t.Value
	case []any:
		var b strings.Builder
		for _, x := range t {
			b.WriteString(stringify(x))
		}
		return b.String()
	}
	return Escape(v)
}

// Tmpl assembles literal markup and escaped values.
//
// Go has no tagged templates, so the two halves are passed separately: the
// literal chunks, which go out verbatim, and the values, which do not. There
// must be exactly one more chunk than value — the same invariant a tagged
// template guarantees for free.
func Tmpl(chunks []string, values ...any) Raw {
	var b strings.Builder
	for i, chunk := range chunks {
		b.WriteString(chunk)
		if i < len(values) {
			b.WriteString(stringify(values[i]))
		}
	}
	return Raw{Value: b.String()}
}

var boldTag = regexp.MustCompile(`(?i)&lt;(/?)b&gt;`)

// Rich renders a field where bold is allowed and nothing else is.
//
// It escapes everything, then puts back exactly `<b>` and `</b>`. Working that
// way round rather than by stripping forbidden tags means the list of what is
// allowed is the whole rule: there is no unknown tag to have forgotten, and
// `<b onclick=…>` does not come back, because it is not one of the two strings
// being restored.
func Rich(v any) Raw {
	if v == nil {
		return Raw{}
	}
	return Raw{Value: boldTag.ReplaceAllString(Escape(v), "<${1}b>")}
}

// Join renders parts with a separator.
//
// The separator is markup from the template and goes out verbatim; the parts
// are values and do not.
func Join(parts []any, separator string) Raw {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, stringify(p))
	}
	return Raw{Value: strings.Join(out, separator)}
}
