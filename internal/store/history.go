package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"picvert/internal/diff"
	"picvert/internal/document"
	"picvert/internal/fields"
	"picvert/internal/profiles"
	"picvert/internal/templates"
)

// History is the journal of a CV: which field changed, from what to what, and
// when.
//
// # ONE ENTRY PER EPISODE OF EDITING, NOT PER SAVE
//
// Typing saves itself every second and a half; a paragraph rewritten over two
// minutes is one act, and recording it as eighty lines of “Summary → Summar →
// Summa…” would bury the one change someone is actually looking for. So a write
// that touches a field already being worked on EXTENDS its entry instead of
// adding one: the before stays the value the episode started from, the after
// follows the keystrokes. An episode ends when nothing has touched that field
// for a while.
//
// A field typed into and then put back as it was leaves NOTHING behind — the
// entry is dropped when it closes on its own starting value. A history of
// changes that never happened is noise.
//
// # WHAT IS STORED IS TEXT, NOT DATA
//
// This is something to read, not something to replay: values are captured as
// they will be shown, clipped, and structured ones are reduced to a one-line
// digest. A journal that could restore would have to keep whole sections
// verbatim, and a CV's own weight would then be spent on its past rather than
// on its photo.
//
//	data/<slug>/history.json
//
// It lives INSIDE the profile, so it follows the CV into the trash, comes back
// with it, and is destroyed with it. Nothing to clean up separately, and no way
// for a deleted CV to leave its wording behind on the disk.
type History struct {
	Registry *templates.Registry
	// EpisodeOf is how long a field stays “being edited”, and MaxEntriesOf how
	// many entries a CV keeps. Read on every access, never frozen: a test sets
	// its environment before the first request, and a value captured at load
	// would make the journal depend on import order.
	EpisodeOf    func() time.Duration
	MaxEntriesOf func() int

	// parsed holds each journal as last read, so a save does not re-read and
	// re-parse one. See the cache comment on load.
	mu     sync.Mutex
	parsed map[string]snapshot
}

// snapshot is a journal as it was last seen on disk.
//
// Validated by modification time AND size, the same way the page cache
// validates a rendering against its document: either changing means somebody
// else wrote the file — a restore from a backup, or a hand edit — and what is
// held is stale.
type snapshot struct {
	at   time.Time
	size int64
	log  []Entry
}

// NewHistory builds the journal of a running service.
func NewHistory(registry *templates.Registry) *History {
	return &History{
		Registry:     registry,
		EpisodeOf:    func() time.Duration { return time.Duration(envInt("PICVERT_EPISODE_MIN", 5)) * time.Minute },
		MaxEntriesOf: func() int { return envInt("PICVERT_HISTORY_ENTRIES", 500) },
	}
}

func envInt(name string, fallback int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && v > 0 {
		return v
	}
	return fallback
}

// Crumb is one step of the trail naming a field.
type Crumb struct {
	Label string `json:"label"`
	I18n  string `json:"i18n,omitempty"`
	// N is which one, when the step addresses an element of a list. One-based,
	// because it is read by a person.
	N int `json:"n,omitempty"`
}

// Entry is one editing episode.
type Entry struct {
	At    string    `json:"at"`
	From  string    `json:"from"`
	Lang  string    `json:"lang,omitempty"`
	Path  string    `json:"path"`
	Kind  diff.Kind `json:"kind"`
	Trail []Crumb   `json:"trail"`
	// Before and After are what will be SHOWN, not what could be restored.
	Before any `json:"before"`
	After  any `json:"after"`
	Count  int `json:"count"`
}

// maxText is the longest value kept. A whole page of prose is not what one
// reads back.
const maxText = 500

// historyFile is the journal's name inside the profile.
const historyFile = "history.json"

// ignored is rewritten on every single save, and by nobody.
var ignored = map[string]bool{"meta.updatedAt": true}

func (h *History) fileFor(p *profiles.Profile) string { return filepath.Join(p.Dir, historyFile) }

// load never fails. There may be no journal yet, or one damaged by a
// half-written disk; either way the CV is what matters and it is elsewhere, so
// a fresh log is started rather than a save failed over its history.
//
// # WHY THE PARSED JOURNAL IS KEPT
//
// Every save read the whole file and parsed it again. On a CV at the default
// cap that is a hundred and sixty kilobytes of JSON, and parsing it measured
// at about a millisecond — twice what the save it was attached to was
// advertised to cost, and all of it inside the one mutex every write in the
// service shares. The journal of the work was more expensive than the work.
//
// So what was last read is kept, keyed by the file and checked against its
// modification time and size. Same bargain as the page cache and the font
// cache: cheap to hold, and the thing that invalidates it is the thing that
// changed.
//
// The caller gets a COPY, because absorb rewrites the slice in place — without
// one, extending an episode would reach into what is being held and leave the
// cache describing a journal that was never written.
func (h *History) load(p *profiles.Profile) []Entry {
	file := h.fileFor(p)
	info, err := os.Stat(file)
	if err != nil {
		return nil
	}

	h.mu.Lock()
	hit, ok := h.parsed[file]
	h.mu.Unlock()
	if ok && hit.at.Equal(info.ModTime()) && hit.size == info.Size() {
		return append([]Entry(nil), hit.log...)
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var log []Entry
	if json.Unmarshal(raw, &log) != nil {
		h.setAside(p, raw)
		return nil
	}
	h.remember(file, info.ModTime(), info.Size(), log)
	return append([]Entry(nil), log...)
}

func (h *History) remember(file string, at time.Time, size int64, log []Entry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.parsed == nil {
		h.parsed = map[string]snapshot{}
	}
	h.parsed[file] = snapshot{at: at, size: size, log: append([]Entry(nil), log...)}
}

// setAside keeps a journal that could not be read.
//
// Starting a fresh log is right — a save must not fail over its own history —
// but the old file is about to be overwritten by the next one, and discarding
// somebody's record without trace is not a decision to make on their behalf.
// Renamed rather than copied, so a file that is damaged because the disk is
// full does not need more of it.
//
// One ".bad" per profile, deliberately: it is the ORIGINAL that is worth
// keeping, and a sequence of them would be a directory filling up with
// successive failures to read the same broken file.
func (h *History) setAside(p *profiles.Profile, raw []byte) {
	kept := h.fileFor(p) + ".bad"
	if _, err := os.Stat(kept); err == nil {
		return
	}
	if os.Rename(h.fileFor(p), kept) == nil {
		return
	}
	// A rename across a mount, or a directory that will not have it. The
	// contents are already in hand, so write them out instead.
	_ = os.WriteFile(kept, raw, 0o600)
}

// save replaces the journal, and records what was written.
//
// COMPACT, not indented. Nothing reads this by eye — the editor draws it as a
// panel and `picvert` never prints it — and the indentation was a fifth of the
// file and four fifths of the time spent writing it. A journal is not a
// configuration file.
func (h *History) save(p *profiles.Profile, log []Entry) error {
	raw, err := json.Marshal(log)
	if err != nil {
		return err
	}
	file := h.fileFor(p)
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		return err
	}
	// Remembered from the file rather than from the clock: what the cache is
	// checked against is what the filesystem recorded, and the two need not
	// agree to the nanosecond.
	if info, err := os.Stat(file); err == nil {
		h.remember(file, info.ModTime(), info.Size(), log)
	}
	return nil
}

// Record journals what separates two versions of a document.
//
// before is nil when the document did not exist: that is one entry saying the
// CV was created, not a hundred saying each of its fields appeared.
func (h *History) Record(p *profiles.Profile, before, after document.Doc, lang string) error {
	var changes []diff.Change
	var previous any
	if before != nil {
		previous = map[string]any(before)
	}
	for _, c := range diff.Diff(previous, map[string]any(after)) {
		if !ignored[c.Path] {
			changes = append(changes, c)
		}
	}
	if len(changes) == 0 {
		return nil
	}

	// The template names the fields. An unknown one must not cost the journal:
	// the engine's own vocabulary describes them nearly as well.
	var tpl *templates.Template
	if h.Registry != nil {
		tpl, _ = h.Registry.ForDocument(after)
	}

	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	now := time.Now()
	log := h.load(p)

	for _, c := range changes {
		trail := trailFor(after, tpl, c.Path)
		entry := Entry{
			At: stamp, From: stamp, Lang: lang, Path: c.Path, Kind: c.Kind,
			Trail:  trail,
			Before: capture(c.Before),
			After:  capture(c.After),
			Count:  1,
		}
		// Which element was dragged, and from where to where. Storing the two
		// orders would be exact and unreadable: eight section names before, the
		// same eight after, and the reader left to spot which two swapped.
		if c.Kind == diff.Move {
			if a, ok := c.Before.([]any); ok {
				if b, ok := c.After.([]any); ok {
					if name, from, to, ok := moved(a, b); ok {
						entry.Trail = append(entry.Trail, Crumb{Label: name})
						entry.Before, entry.After = from, to
					}
				}
			}
		}
		if at := episodeOf(log, entry, now, h.EpisodeOf()); at >= 0 {
			log = absorb(log, at, entry, after)
		} else {
			log = append(log, entry)
		}
	}

	return h.save(p, trim(log, h.MaxEntriesOf()))
}

// trim keeps the newest entries, PER LANGUAGE.
//
// The journal holds every language of a CV in one file, and a reader asks for
// one language at a time. Trimming the file as a whole meant a busy afternoon
// on the French CV silently deleted the English one's entire history — a CV
// losing a record it had no part in creating, which is the kind of data loss
// nobody thinks to look for because nothing went wrong.
//
// The surviving entries stay in their original order, because that order is
// what List reads back to front and what absorb maintains.
func trim(log []Entry, max int) []Entry {
	if max <= 0 || len(log) <= max {
		return log
	}
	// Counted from the end, so what is counted first is what is kept.
	seen := map[string]int{}
	keep := make([]bool, len(log))
	for i := len(log) - 1; i >= 0; i-- {
		if seen[log[i].Lang] < max {
			seen[log[i].Lang]++
			keep[i] = true
		}
	}
	out := make([]Entry, 0, len(log))
	for i, e := range log {
		if keep[i] {
			out = append(out, e)
		}
	}
	return out
}

// List is the journal, most recent first.
//
// Read back to front rather than sorted by date: the file is kept in the order
// things happened — an extended episode moves to the end — and two saves a
// millisecond apart carry the same timestamp, which a sort has no way to
// separate.
func (h *History) List(p *profiles.Profile, limit int, lang string, filterLang bool) []Entry {
	log := h.load(p)
	out := make([]Entry, 0, len(log))
	for i := len(log) - 1; i >= 0; i-- {
		if filterLang && log[i].Lang != lang {
			continue
		}
		out = append(out, log[i])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// Size is how many entries a CV carries, all languages together.
func (h *History) Size(p *profiles.Profile) int { return len(h.load(p)) }

// --- episodes ---------------------------------------------------------------

// episodeOf is the index of the entry this change belongs to, or -1.
//
// Looked up over the whole window rather than only at the last entry: a save
// can carry several fields, and keying on “the previous one” would then open a
// new episode for each of them at every keystroke — the very flood this exists
// to prevent.
//
// A change INSIDE something just added belongs to that addition. A section
// created and then filled in is one event; listing its every field afterwards
// would say nothing the “added” line does not. The document itself is excluded:
// a CV is created and then entirely typed, and swallowing that would hide the
// first minutes of every new CV behind the word “created”.
func episodeOf(log []Entry, entry Entry, now time.Time, window time.Duration) int {
	for i := len(log) - 1; i >= 0; i-- {
		e := log[i]
		at, err := time.Parse(time.RFC3339Nano, e.At)
		if err != nil {
			// A timestamp nothing here wrote. Skipped rather than trusted, and
			// the scan carries on: one unreadable entry is not a reason to stop
			// looking for the episode this change belongs to.
			continue
		}
		// THE LOG IS IN ORDER, so the first entry too old to extend means every
		// entry before it is older still. Walking the rest would parse five
		// hundred timestamps to examine the three inside the window — which is
		// what it used to do, on every keystroke.
		if now.Sub(at) > window {
			break
		}
		if e.Lang != entry.Lang {
			continue
		}
		if e.Kind == diff.Add && inside(e.Path, entry.Path) {
			return i
		}
		if e.Path != entry.Path {
			continue
		}
		// A PATH IS A POSITION, and an insertion shifts every position after
		// it. Extending the entry of the bullet that used to sit there would
		// merge two unrelated edits into one line claiming a bullet turned into
		// another. An addition is a new thing, always its own event — as is a
		// reorder, which is a gesture rather than a stretch of typing.
		if entry.Kind == diff.Add || entry.Kind == diff.Move {
			continue
		}
		// Same reason on the other side: what stands at the path of something
		// removed is no longer the thing that was removed.
		if e.Kind == diff.Remove || e.Kind == diff.Move {
			continue
		}
		return i
	}
	return -1
}

func inside(parent, child string) bool {
	return parent != "" &&
		(strings.HasPrefix(child, parent+".") || strings.HasPrefix(child, parent+"["))
}

// absorb extends an episode with a change.
//
// Created then removed leaves nothing at all: it never existed as far as anyone
// reading this is concerned.
func absorb(log []Entry, at int, entry Entry, after document.Doc) []Entry {
	hit := log[at]
	if hit.Kind == diff.Add && entry.Kind == diff.Remove && hit.Path == entry.Path {
		return append(log[:at], log[at+1:]...)
	}
	hit.At = entry.At
	hit.Count++

	if hit.Path == entry.Path {
		hit.After = entry.After
		hit.Trail = entry.Trail
		if hit.Kind != diff.Add {
			hit.Kind = entry.Kind
		}
	} else {
		// Inside something added: the event stays the addition, but what was
		// added is re-read, or the line would describe the section as it was
		// created rather than as it now stands.
		hit.After = capture(valueAt(map[string]any(after), hit.Path))
	}

	// Ordered by their last change, which is also the order they are read in.
	log = append(log[:at], log[at+1:]...)
	// Typed into, then put back: nothing happened.
	if hit.Kind != diff.Add && diff.Same(hit.Before, hit.After) {
		return log
	}
	return append(log, hit)
}

// moved names the element that travelled furthest, and its two positions.
func moved(before, after []any) (name string, from, to int, ok bool) {
	used := map[int]bool{}
	best := -1
	for j, element := range after {
		at := -1
		for k, x := range before {
			if !used[k] && diff.Same(x, element) {
				at = k
				break
			}
		}
		if at < 0 {
			continue
		}
		used[at] = true
		travel := at - j
		if travel < 0 {
			travel = -travel
		}
		if travel == 0 {
			continue
		}
		if travel > best {
			best, name, from, to, ok = travel, shortOf(element), at+1, j+1, true
		}
	}
	return name, from, to, ok
}

// --- naming the field -------------------------------------------------------

var indexRE = regexp.MustCompile(`\[(\d+)\]`)

// step is one element of a path: a name, or an index.
type step struct {
	name  string
	index int
	isNum bool
}

// steps splits content.sections[2].items[0].role into its parts.
func steps(p string) []step {
	var out []step
	for _, part := range strings.Split(p, ".") {
		if part == "" {
			continue
		}
		if name := indexRE.ReplaceAllString(part, ""); name != "" {
			out = append(out, step{name: name})
		}
		for _, m := range indexRE.FindAllStringSubmatch(part, -1) {
			n, _ := strconv.Atoi(m[1])
			out = append(out, step{index: n, isNum: true})
		}
	}
	return out
}

// valueAt is the value a path points at, in a document.
func valueAt(root any, p string) any {
	cursor := root
	for _, s := range steps(p) {
		if cursor == nil {
			return nil
		}
		if s.isNum {
			arr, ok := cursor.([]any)
			if !ok || s.index >= len(arr) {
				return nil
			}
			cursor = arr[s.index]
			continue
		}
		obj, ok := cursor.(map[string]any)
		if !ok {
			return nil
		}
		cursor = obj[s.name]
	}
	return cursor
}

// trailFor turns a path into something a person recognises: “Experience ›
// Blocks #2 › Title”.
//
// It reads the FIELD TREE, the same one the forms are generated from, so a
// field added to the vocabulary is named here without a line changing — and a
// template that composes its sections differently is described in its own
// terms.
func trailFor(doc document.Doc, tpl *templates.Template, p string) []Crumb {
	seg := steps(p)
	if len(seg) == 0 {
		return []Crumb{{Label: "CV", I18n: "history.document"}}
	}

	var out []Crumb
	var here []fields.Field
	var rest []step

	switch {
	case seg[0].name == "meta":
		out = append(out, Crumb{Label: "Settings", I18n: "editor.settings"})
		here = fields.MetaFields
		rest = seg[1:]
	case len(seg) > 1 && seg[0].name == "content" && seg[1].name == "identity":
		out = append(out, Crumb{Label: "Identity", I18n: "editor.identity"})
		here = fields.IdentityFields
		rest = seg[2:]
	case len(seg) > 1 && seg[0].name == "content" && seg[1].name == "sections":
		if len(seg) < 3 || !seg[2].isNum {
			return []Crumb{{Label: "Sections", I18n: "history.sections"}}
		}
		sections := document.Sections(doc)
		if seg[2].index >= len(sections) {
			// A section that has just been removed is no longer there to name
			// itself.
			return []Crumb{{Label: "Sections", I18n: "history.sections", N: seg[2].index + 1}}
		}
		section, _ := document.AsObject(sections[seg[2].index])
		kind := document.Str(section, "type")
		label := document.Str(section, "title")
		if label == "" {
			if known, ok := fields.SectionLabels[kind]; ok {
				label = known.Label
			}
		}
		if label == "" {
			label = document.Str(section, "id")
		}
		out = append(out, Crumb{Label: label})
		here = sectionFields(tpl, kind)
		rest = seg[3:]
	default:
		return []Crumb{{Label: p}}
	}

	var cursor *fields.Field
	for _, s := range rest {
		if s.isNum {
			// An index is not a step of its own: it says WHICH of the previous
			// one.
			if len(out) > 0 {
				out[len(out)-1].N = s.index + 1
			}
			if cursor != nil && cursor.Kind == fields.KindList && cursor.Of != nil {
				next := *cursor.Of
				cursor = &next
				here = groupFields(cursor)
			} else {
				cursor, here = nil, nil
			}
			continue
		}
		hit := findField(here, s.name)
		if hit == nil {
			out = append(out, Crumb{Label: s.name})
			cursor, here = nil, nil
			continue
		}
		out = append(out, Crumb{Label: hit.Label, I18n: hit.I18n})
		cursor = hit
		here = groupFields(hit)
	}
	return out
}

func sectionFields(tpl *templates.Template, kind string) []fields.Field {
	var out []fields.Field
	out = append(out, fields.SectionCommon...)
	if tpl != nil {
		out = append(out, fields.ColumnField(tpl.Columns))
	}
	out = append(out, fields.SectionFields[kind]...)
	return out
}

func groupFields(f *fields.Field) []fields.Field {
	if f == nil {
		return nil
	}
	if f.Kind == fields.KindGroup {
		return f.Fields
	}
	return nil
}

func findField(in []fields.Field, key string) *fields.Field {
	for i := range in {
		if in[i].Key == key {
			return &in[i]
		}
	}
	return nil
}

// --- capturing the value ----------------------------------------------------

// plumbing is the structural keys, which carry no meaning to a reader.
var plumbing = map[string]bool{"id": true, "type": true, "column": true, "$schema": true}

// digest is the words inside a value, in order, so a section reads as its own
// summary.
func digest(v any, limit int) string {
	var bits []string
	var walk func(any)
	walk = func(x any) {
		if len(bits) >= limit {
			return
		}
		switch t := x.(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				bits = append(bits, s)
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				if !plumbing[k] {
					keys = append(keys, k)
				}
			}
			// Sorted so two runs digest the same value into the same sentence.
			sortStrings(keys)
			for _, k := range keys {
				walk(t[k])
			}
		}
	}
	walk(v)
	return strings.Join(bits, " · ")
}

func sortStrings(v []string) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// shortOf is the first words of a value: enough to tell one list element from
// another.
func shortOf(v any) string {
	if s := digest(v, 1); s != "" {
		return s
	}
	return "…"
}

func clip(s string) string {
	if len([]rune(s)) <= maxText {
		return s
	}
	return string([]rune(s)[:maxText]) + "…"
}

func capture(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" {
			return nil
		}
		return clip(t)
	case bool:
		return t
	case float64, int:
		return t
	}
	s := clip(digest(v, 12))
	if s == "" {
		return nil
	}
	return s
}
