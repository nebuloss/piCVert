// Package fields holds the field vocabulary, and what every section type is
// made of.
//
// THIS PACKAGE IS THE DESCRIPTION OF A CV. Three things used to state it
// separately and drift apart: the validator knew the constraints, the editor
// knew the forms, the schema file knew the shape. Written down once as a tree
// of fields, all three read from it — the validator walks it, the editor builds
// its forms from it, and a template manifest composes sections out of it.
//
// WHY THE VOCABULARY IS CLOSED. A field has one of a fixed set of kinds. That
// is what lets the engine promise anything at all: each kind has exactly one
// validation rule, one form control and one renderer. A template that could
// invent a field kind would be a template the engine can neither check nor
// edit — it would render a blank area and report nothing.
//
// ADDING A FIELD is editing the tree below. Validation, the generated form and
// the served manifest follow; only the layouts have to be taught to draw it.
//
// Ported from engine/lib/fields.ts. The two must agree field for field: the
// browser editor still compiles against the TypeScript one, and a difference
// shows up as a form control that writes something the server refuses.
package fields

// Kind is what a field is. A closed set, deliberately.
type Kind string

const (
	KindText   Kind = "text"   // one line, escaped on render
	KindRich   Kind = "rich"   // several lines, <b> allowed and nothing else
	KindIcon   Kind = "icon"   // a name from the template's icon set
	KindNumber Kind = "number" // bounded numeric value
	KindBool   Kind = "bool"   // a flag
	KindEnum   Kind = "enum"   // one of a fixed set of values
	KindPhoto  Kind = "photo"  // an image file living in the profile folder
	KindList   Kind = "list"   // an ordered, reorderable series of one field
	KindGroup  Kind = "group"  // a record of named fields
)

// MaxRich is the longest a rich field may be. Beyond that no layout holds
// anyway.
const MaxRich = 4000

// Default lengths applied by the shorthand constructors, matching the
// TypeScript defaults exactly. A plain text field that declares no maximum
// still has one: an unbounded field is a field the layout cannot promise
// anything about.
const (
	defaultTextMax  = 300
	defaultRichRows = 4
	// DefaultPhotoMax is the fallback the validator applies to a photo field
	// with no explicit maximum. It lives here rather than in the validator so
	// the two cannot disagree.
	DefaultPhotoMax = 200
	// DefaultTextMax is the fallback the validator applies to a text field
	// whose maximum was never declared.
	DefaultTextMax = defaultTextMax
)

// EnumValue is one allowed value of an enum field, with how to name it.
type EnumValue struct {
	Value string `json:"value"`
	Label string `json:"label"`
	I18n  string `json:"i18n,omitempty"`
}

// Field is one node of the tree.
//
// TypeScript expresses this as a union discriminated on `kind`, with each
// variant carrying only the members that apply to it. Go has no such union, so
// this is one struct whose members are read according to Kind. The validator,
// the form generator and the schema writer each look only at what their kind
// allows — and a member set on the wrong kind is ignored rather than
// misinterpreted.
//
// The optional numbers are pointers on purpose: `Min: 0` on the language gauge
// is a real bound, and a plain int could not tell it from "not declared".
type Field struct {
	Kind  Kind   `json:"kind"`
	Key   string `json:"key"`
	Label string `json:"label"`

	I18n            string `json:"i18n,omitempty"`
	Required        bool   `json:"required,omitempty"`
	Help            string `json:"help,omitempty"`
	HelpI18n        string `json:"helpI18n,omitempty"`
	Placeholder     string `json:"placeholder,omitempty"`
	PlaceholderI18n string `json:"placeholderI18n,omitempty"`

	// Widget names a specialised control for this field.
	//
	// Presentation only: a widget reads and writes exactly what the generic
	// rendering would. It exists because some shapes are unusable as a stack
	// of rows — thirty skill chips being the case that forced it.
	Widget string `json:"widget,omitempty"`

	// Max applies to text, rich, icon, photo and number.
	Max *int `json:"max,omitempty"`
	// Rows is a rendering hint for the form: how many rows the control gets.
	Rows *int `json:"rows,omitempty"`
	// Min applies to number (a bound) and to list (a required count).
	Min *int `json:"min,omitempty"`

	// Values and Default belong to enum. Default is a pointer because the
	// empty string is a legitimate default — the grey chip is "".
	Values  []EnumValue `json:"values,omitempty"`
	Default *string     `json:"default,omitempty"`

	// Of is the element type of a list.
	Of *Field `json:"of,omitempty"`
	// Fields are the members of a group.
	Fields []Field `json:"fields,omitempty"`

	// AddLabel names the "add an entry" control of a list.
	AddLabel string `json:"addLabel,omitempty"`
	AddI18n  string `json:"addI18n,omitempty"`
}

func intp(v int) *int       { return &v }
func strp(v string) *string { return &v }

// --- shorthand constructors -------------------------------------------------
//
// They exist so the tree below reads as the description it is, rather than as
// a wall of struct literals. Each mirrors the TypeScript helper of the same
// name, defaults included.

type opts = func(*Field)

func with(f Field, os ...opts) Field {
	for _, o := range os {
		o(&f)
	}
	return f
}

func text(key, label string, os ...opts) Field {
	return with(Field{Kind: KindText, Key: key, Label: label, Max: intp(defaultTextMax)}, os...)
}

func rich(key, label string, os ...opts) Field {
	return with(Field{Kind: KindRich, Key: key, Label: label, Max: intp(MaxRich), Rows: intp(defaultRichRows)}, os...)
}

func group(key, label string, fs []Field, os ...opts) Field {
	return with(Field{Kind: KindGroup, Key: key, Label: label, Fields: fs}, os...)
}

func list(key, label string, of Field, os ...opts) Field {
	return with(Field{Kind: KindList, Key: key, Label: label, Of: &of}, os...)
}

// `limit` and `atLeast` rather than `max` and `min`: those two are builtins
// since Go 1.21, and shadowing them across a whole package to save four
// characters in a data table is how someone later writes `max(a, b)` and gets
// something else entirely.
func i18n(v string) opts            { return func(f *Field) { f.I18n = v } }
func required() opts                { return func(f *Field) { f.Required = true } }
func limit(v int) opts              { return func(f *Field) { f.Max = intp(v) } }
func rows(v int) opts               { return func(f *Field) { f.Rows = intp(v) } }
func atLeast(v int) opts            { return func(f *Field) { f.Min = intp(v) } }
func placeholder(v string) opts     { return func(f *Field) { f.Placeholder = v } }
func placeholderI18n(v string) opts { return func(f *Field) { f.PlaceholderI18n = v } }
func helpI18n(v string) opts        { return func(f *Field) { f.HelpI18n = v } }
func widget(v string) opts          { return func(f *Field) { f.Widget = v } }
func add(label, key string) opts {
	return func(f *Field) { f.AddLabel = label; f.AddI18n = key }
}

// --- the vocabulary of chip styles ------------------------------------------

// ChipVariantValues are the chip styles, named for the interface.
//
// The empty value is the ordinary grey chip, and it is a real member: dropping
// it would make "no variant" indistinguishable from "invalid variant".
var ChipVariantValues = []EnumValue{
	{Value: "", Label: "Gray", I18n: "editor.chip_gray"},
	{Value: "pri", Label: "Blue", I18n: "editor.chip_blue"},
	{Value: "ter", Label: "Green", I18n: "editor.chip_green"},
	{Value: "ghost", Label: "Outline", I18n: "editor.chip_outline"},
}

// --- what each section type is made of --------------------------------------

// SectionTypeOrder is the declaration order of the section types.
//
// Go maps have no order, and TypeScript's `Object.keys(SECTION_FIELDS)` does —
// the validator prints that order verbatim in its "invalid value" message.
// Stating it here keeps the two error messages identical.
var SectionTypeOrder = []string{"contact", "chips", "languages", "text", "list", "items"}

// SectionFields is the tree, one entry per section type.
var SectionFields = map[string][]Field{
	"contact": {
		list("rows", "Rows",
			group("", "Row", []Field{
				with(Field{Kind: KindIcon, Key: "icon", Label: "Icon", Max: intp(40)}, required()),
				text("text", "Text", required()),
			}),
			i18n("editor.rows"), add("Add a row", "editor.add_line")),
	},

	"chips": {
		text("lead", "Highlighted line", limit(200), i18n("editor.lead_line")),
		list("groups", "Groups",
			group("", "Group", []Field{
				text("label", "Group label", limit(120)),
				list("chips", "Chips",
					group("", "Chip", []Field{
						text("text", "Text", limit(80), required(), i18n("editor.text")),
						{Kind: KindEnum, Key: "variant", Label: "Style", I18n: "editor.style",
							Values: ChipVariantValues, Default: strp("")},
					}),
					i18n("editor.chips"), add("Add", "editor.add_group"), widget("chips")),
			}),
			i18n("editor.groups"), atLeast(1), add("Add a group", "editor.add_group")),
	},

	"languages": {
		list("languages", "Languages",
			group("", "Language", []Field{
				text("name", "Language", limit(80), required(), i18n("editor.language_field")),
				text("level", "Level", limit(80), i18n("editor.level"), placeholder("e.g. B2 · 815 TOEIC")),
				with(Field{Kind: KindNumber, Key: "value", Label: "Gauge %", I18n: "editor.gauge",
					Min: intp(0), Max: intp(100)}, required()),
			}),
			i18n("editor.languages"), add("Add a language", "editor.add_line")),
	},

	"text": {
		rich("text", "Text", rows(7), required(), i18n("editor.text"), helpI18n("editor.rich_hint")),
	},

	"list": {
		list("items", "Lines",
			rich("", "Line", rows(2), required(), helpI18n("editor.rich_hint")),
			i18n("editor.lines"), add("Add a line", "editor.add_line")),
	},

	"items": {
		list("items", "Blocks",
			group("", "Block", []Field{
				text("role", "Title", limit(200), required(), placeholderI18n("editor.role")),
				text("period", "Period", limit(80), i18n("editor.period"), placeholder("e.g. 2025 – present")),
				text("org", "Organisation", limit(200), i18n("editor.organisation")),
				list("bullets", "Bullets",
					rich("", "Bullet", rows(2), required()),
					i18n("editor.bullets"), add("Add a bullet", "editor.add_bullet"), helpI18n("editor.rich_hint")),
				rich("note", "Note", rows(2), i18n("editor.note")),
			}),
			i18n("editor.blocks"), add("Add a block", "editor.add_block")),
	},
}

// SectionCommon are the members every section has, whatever its type.
//
// `column` is deliberately absent: which regions exist is the template's
// business, so the registry injects it with that template's own values.
var SectionCommon = []Field{
	text("title", "Section title", limit(120), i18n("editor.section_title")),
	{Kind: KindBool, Key: "tint", Label: "Tinted background"},
}

var columnLabels = map[string]string{
	"left":  "Left column",
	"right": "Main column",
	"full":  "Full width",
}

// ColumnField builds the region selector for a template, from the regions that
// template actually lays out.
//
// A CV cannot be put in a region its template does not draw, and the only place
// that knows the list is the manifest — hence a function rather than a
// constant.
func ColumnField(columns []string) Field {
	values := make([]EnumValue, 0, len(columns))
	for _, c := range columns {
		values = append(values, EnumValue{Value: c, Label: columnLabels[c], I18n: "editor.column_" + c})
	}
	return Field{Kind: KindEnum, Key: "column", Label: "Column", I18n: "editor.column",
		Required: true, Values: values}
}

// IdentityFields is who the CV is about.
var IdentityFields = []Field{
	text("name", "Name", limit(120), required(), i18n("editor.name")),
	text("role", "Job title", limit(240), i18n("editor.role")),
	list("subtitle", "Subtitle",
		group("", "Part", []Field{
			text("text", "Text", limit(120), required(), i18n("editor.text")),
			{Kind: KindBool, Key: "strong", Label: "Highlighted", I18n: "editor.highlight"},
		}),
		i18n("editor.subtitle"), add("Add a part", "editor.add_line")),
	{Kind: KindPhoto, Key: "photo", Label: "Photo", I18n: "editor.photo", Max: intp(200)},
	text("photoAlt", "Photo description", limit(200)),
}

// MetaFields is the engine's own half of the envelope.
var MetaFields = []Field{
	text("lang", "Language", limit(12)),
	text("template", "Template", limit(60)),
	text("theme", "Theme (legacy)", limit(60)),
	text("documentTitle", "Document title", limit(300)),
}

// SectionLabel names a section type for the interface.
type SectionLabel struct {
	Label string `json:"label"`
	I18n  string `json:"i18n"`
}

// SectionLabels names each section type in the interface.
var SectionLabels = map[string]SectionLabel{
	"contact":   {Label: "Contact details", I18n: "section.contact"},
	"chips":     {Label: "Tag list", I18n: "section.chips"},
	"languages": {Label: "Language gauges", I18n: "section.languages"},
	"text":      {Label: "Free text", I18n: "section.text"},
	"list":      {Label: "Bullet list", I18n: "section.list"},
	"items":     {Label: "Dated entries", I18n: "section.items"},
}
