// Package store is the single door every write goes through.
//
// cv.json is THE source of truth. Nothing else is: the page and the PDF are
// rendered from it on demand, so there is no artefact that can fall out of step
// with the document, and no build to roll back when one fails.
//
// Every write is validated BEFORE anything touches the disk, then written
// atomically — temporary file, rename — with a .bak alongside and an optional
// git commit. What a write puts at risk can be read off this one type instead
// of discovered by following imports.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"picvert/internal/document"
	"picvert/internal/engine"
	"picvert/internal/profiles"
	"picvert/internal/templates"
	"picvert/internal/validate"
)

var langRE = regexp.MustCompile(`^[a-z]{2}$`)

// Store reads and writes profiles.
type Store struct {
	Profiles *profiles.Repository
	Registry *templates.Registry
	History  *History

	// VersionedOf is re-read on every write: the setting is placed by the
	// environment, often after this package was loaded.
	VersionedOf func() bool
}

// New builds the store of a running service.
func New(repo *profiles.Repository, registry *templates.Registry, history *History) *Store {
	return &Store{
		Profiles:    repo,
		Registry:    registry,
		History:     history,
		VersionedOf: func() bool { return os.Getenv("PICVERT_GIT") == "1" },
	}
}

// Read is one profile's document, lifted to the current shape.
func (s *Store) Read(slug, lang string) (document.Doc, error) {
	p, err := s.Profiles.Get(slug)
	if err != nil {
		return nil, err
	}
	doc, err := engine.ReadDoc(p.DocPath(lang))
	if err != nil {
		return nil, fmt.Errorf("%s unreadable for %q: %w", profiles.DocName(lang), slug, err)
	}
	return doc, nil
}

// Write validates and stores a document, and returns what was stored.
//
// input is `any` on purpose: this is the door every write goes through,
// including bodies straight off the network. It is lifted to the current shape
// FIRST and validated second — never the other way round, or a document written
// by an older version would be rejected for being old rather than wrong.
func (s *Store) Write(slug string, input any, lang string) (document.Doc, error) {
	p, err := s.Profiles.Get(slug)
	if err != nil {
		return nil, err
	}
	doc, ok := document.AsObject(document.Upgrade(input))
	if !ok {
		return nil, fmt.Errorf("a CV must be a JSON object")
	}
	if _, err := validate.Validate(doc); err != nil {
		return nil, err
	}
	if err := s.pinTemplate(doc); err != nil {
		return nil, err
	}

	file := p.DocPath(lang)
	// Read BEFORE overwriting: the journal is the difference between the two,
	// and this is the only moment both exist. Nothing, when the document is
	// being created — which the journal reports as a creation rather than as
	// every field of a CV appearing at once.
	previous, _ := engine.ReadDoc(file)

	// An explicit mark of editing. Sweeping up abandoned drafts leans on this
	// rather than on file timestamps: comparing dates with a margin made a CV
	// edited just after being created look abandoned.
	meta := map[string]any{}
	for k, v := range document.Meta(doc) {
		meta[k] = v
	}
	meta["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	doc["meta"] = meta

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if err := writeAtomic(file, raw); err != nil {
		return nil, err
	}

	// After the write, and never a blocker: a journal that could fail a save
	// would be a feature costing people their work.
	if s.History != nil {
		if err := s.History.Record(p, previous, doc, lang); err != nil {
			fmt.Fprintf(os.Stderr, "[history] %s: %v\n", slug, err)
		}
	}
	if s.VersionedOf != nil && s.VersionedOf() {
		commit(p.Dir, file, slug)
	}
	return doc, nil
}

// pinTemplate rewrites the document's template reference as a UUID, in place.
//
// Documents written before templates had identities name a directory, and a
// hand-written one may name a template by its readable title. Both resolve;
// what gets STORED is always the UUID, because that is the only reference a
// rename cannot break. The migration happens on the next save rather than in a
// batch, so there is no moment at which half the CVs are converted.
func (s *Store) pinTemplate(doc document.Doc) error {
	t, err := s.Registry.ForDocument(doc)
	if err != nil {
		return err
	}
	if err := s.Registry.Check(doc, t); err != nil {
		return err
	}
	meta := map[string]any{}
	for k, v := range document.Meta(doc) {
		meta[k] = v
	}
	meta["template"] = t.UUID
	delete(meta, "theme")
	doc["meta"] = meta
	return nil
}

// writeAtomic replaces a file without ever leaving it half-written.
//
// A reader either sees the old document or the new one. Writing in place would
// leave a window in which a CV is truncated JSON, and the window is exactly as
// long as a power cut needs to be.
func writeAtomic(file string, raw []byte) error {
	if _, err := os.Stat(file); err == nil {
		if old, err := os.ReadFile(file); err == nil {
			_ = os.WriteFile(file+".bak", old, 0o600)
		}
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// commit records the change, when the profile lives in a git checkout.
//
// Self-service CVs are gitignored: `git add` simply does nothing for them, and
// that is intended — they are runtime data, and tracking them would dirty the
// deployment checkout. A failure here is never reported: the commit is a
// comfort, never a condition of saving.
func commit(dir, file, slug string) {
	run := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		return cmd.Run()
	}
	if run("rev-parse", "--is-inside-work-tree") != nil {
		return
	}
	_ = run("add", "--", file)
	if run("diff", "--cached", "--quiet", "--", file) == nil {
		return
	}
	_ = run("commit", "-m", fmt.Sprintf("cv(%s): edited through the web interface", slug))
}

// --- targeted edits ---------------------------------------------------------
//
// The editor changes a CV in small touches. Each helper below re-reads,
// changes, re-validates and rewrites the whole document: at the scale of a CV —
// a few kilobytes — that costs nothing, and it guarantees that an invalid
// document never reaches the disk by a side door.

func (s *Store) UpdateIdentity(slug string, patch map[string]any, lang string) (document.Doc, error) {
	doc, err := s.Read(slug, lang)
	if err != nil {
		return nil, err
	}
	content, _ := document.Obj(doc, "content")
	identity, _ := document.Obj(content, "identity")
	next := merge(identity, patch)
	nextContent := merge(content, map[string]any{"identity": next})
	doc["content"] = nextContent
	return s.Write(slug, doc, lang)
}

func (s *Store) UpdateMeta(slug string, patch map[string]any, lang string) (document.Doc, error) {
	doc, err := s.Read(slug, lang)
	if err != nil {
		return nil, err
	}
	doc["meta"] = merge(document.Meta(doc), patch)
	return s.Write(slug, doc, lang)
}

// SetTemplate switches the CV to another layout.
//
// Not a meta patch: sections have to be moved into the regions the target
// template lays out, and the move is REFUSED outright when the target cannot
// draw one of them. Refusing is the point — a section quietly dropped on a
// layout change is work lost with nothing to show for it.
func (s *Store) SetTemplate(slug, ref, lang string) (document.Doc, error) {
	doc, err := s.Read(slug, lang)
	if err != nil {
		return nil, err
	}
	target, err := s.Registry.Resolve(ref)
	if err != nil {
		return nil, err
	}
	adapted, err := s.Registry.Adapt(doc, target)
	if err != nil {
		return nil, err
	}
	return s.Write(slug, adapted, lang)
}

// UpdateSection changes one section.
//
// id and type are forced back to their current values. They decide the shape of
// everything else in the section, and switching either through this path would
// produce a document that passes validation and renders empty.
func (s *Store) UpdateSection(slug, id string, patch map[string]any, lang string) (document.Doc, error) {
	doc, err := s.Read(slug, lang)
	if err != nil {
		return nil, err
	}
	sections := document.Sections(doc)
	i, err := sectionIndex(sections, id)
	if err != nil {
		return nil, err
	}
	current, _ := document.AsObject(sections[i])
	next := merge(current, patch)
	next["id"] = document.Str(current, "id")
	next["type"] = document.Str(current, "type")
	sections[i] = next
	return s.writeSections(slug, doc, sections, lang)
}

// ReorderSections puts the sections in the given order.
func (s *Store) ReorderSections(slug string, order []int, lang string) (document.Doc, error) {
	doc, err := s.Read(slug, lang)
	if err != nil {
		return nil, err
	}
	sections := document.Sections(doc)
	if !isPermutation(order, len(sections)) {
		return nil, fmt.Errorf("invalid order: a permutation of 0..%d was expected", len(sections)-1)
	}
	next := make([]any, len(sections))
	for i, from := range order {
		next[i] = sections[from]
	}
	return s.writeSections(slug, doc, next, lang)
}

// ReorderEntries puts one section's entries in the given order.
func (s *Store) ReorderEntries(slug, id string, order []int, lang string) (document.Doc, error) {
	doc, err := s.Read(slug, lang)
	if err != nil {
		return nil, err
	}
	sections := document.Sections(doc)
	i, err := sectionIndex(sections, id)
	if err != nil {
		return nil, err
	}
	section, _ := document.AsObject(sections[i])
	items, ok := document.Arr(section, "items")
	if !ok {
		return nil, fmt.Errorf("section %q has no list of entries", id)
	}
	if !isPermutation(order, len(items)) {
		return nil, fmt.Errorf("invalid order: a permutation of 0..%d was expected", len(items)-1)
	}
	next := make([]any, len(items))
	for at, from := range order {
		next[at] = items[from]
	}
	sections[i] = merge(section, map[string]any{"items": next})
	return s.writeSections(slug, doc, sections, lang)
}

func (s *Store) writeSections(slug string, doc document.Doc, sections []any, lang string) (document.Doc, error) {
	content, _ := document.Obj(doc, "content")
	doc["content"] = merge(content, map[string]any{"sections": sections})
	return s.Write(slug, doc, lang)
}

// --- languages --------------------------------------------------------------

// AddLanguage creates a variant, seeded from an existing document.
func (s *Store) AddLanguage(slug, lang, from string) (document.Doc, error) {
	p, err := s.Profiles.Get(slug)
	if err != nil {
		return nil, err
	}
	if !langRE.MatchString(lang) {
		return nil, fmt.Errorf("invalid language code: %q (two lowercase letters expected)", lang)
	}
	if _, err := os.Stat(p.DocPath(lang)); err == nil {
		return nil, fmt.Errorf("this CV already has a %q version", lang)
	}
	doc, err := s.Read(slug, from)
	if err != nil {
		return nil, err
	}
	meta := merge(document.Meta(doc), map[string]any{"lang": lang})
	delete(meta, "updatedAt")
	doc["meta"] = meta
	return s.Write(slug, doc, lang)
}

// RemoveLanguage deletes a variant. The default document cannot go this way:
// it is the CV, not a translation of it.
func (s *Store) RemoveLanguage(slug, lang string) error {
	p, err := s.Profiles.Get(slug)
	if err != nil {
		return err
	}
	if lang == "" {
		return fmt.Errorf("the default language cannot be removed")
	}
	target := p.DocPath(lang)
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("no %q version for this CV", lang)
	}
	_ = os.Remove(target)
	_ = os.Remove(target + ".bak")
	return nil
}

// --- helpers ----------------------------------------------------------------

// merge is a shallow copy of base with patch laid over it.
//
// A COPY, never an edit in place. The document a handler holds may be the one a
// caller is still reading, and unknown properties have to survive the round
// trip — so nothing here deletes a key it did not put there.
func merge(base, patch map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(patch))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		out[k] = v
	}
	return out
}

func sectionIndex(sections []any, id string) (int, error) {
	var known []string
	for i, raw := range sections {
		s, ok := document.AsObject(raw)
		if !ok {
			continue
		}
		if document.Str(s, "id") == id {
			return i, nil
		}
		known = append(known, document.Str(s, "id"))
	}
	return 0, fmt.Errorf("unknown section: %q (available: %s)", id, strings.Join(known, ", "))
}

func isPermutation(order []int, n int) bool {
	if len(order) != n {
		return false
	}
	seen := make([]bool, n)
	for _, i := range order {
		if i < 0 || i >= n || seen[i] {
			return false
		}
		seen[i] = true
	}
	return true
}
