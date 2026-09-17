// Package profiles is where CVs live on disk, and how they are found.
//
// A profile is a directory holding a cv.json. The engine knows no profile in
// particular — which is what lets this repository be published with no personal
// data in it at all — and it knows no filesystem layout either: the data and
// build roots are read from the environment on EVERY access rather than frozen
// at construction, so a test can mount a sandbox by setting them before the
// first request and not care what was imported when.
package profiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"picvert/internal/document"
)

// slugRE is the whole of what a profile may be called.
//
// It is a WHITELIST, and it is the only thing standing between a URL and the
// rest of the filesystem: a slug becomes a path component, so anything that can
// contain a dot or a slash can leave the data directory. Rejecting by pattern
// rather than sanitising by substitution means there is no clever encoding to
// find — the name either is one of these or it does not exist.
var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// langRE is the suffix of a translated document, cv.<lang>.json.
var langRE = regexp.MustCompile(`^cv\.([a-z]{2})\.json$`)

// DefaultDoc is the document of a profile's own language.
const DefaultDoc = "cv.json"

// CheckSlug rejects anything that is not a profile name.
func CheckSlug(slug string) error {
	if !slugRE.MatchString(slug) {
		return fmt.Errorf("invalid profile identifier: %q (lowercase letters, digits, - and _)", slug)
	}
	return nil
}

// DocName is the file a language's document is kept in.
//
// A CV in French and a CV in English are not translations of one another: the
// phrasing differs and usually the content does too. They are TWO DOCUMENTS
// sharing one photo, not one document with translated fields — which is why
// this is a filename and not a field.
func DocName(lang string) string {
	if lang == "" || lang == "default" {
		return DefaultDoc
	}
	return "cv." + lang + ".json"
}

// Language is one of the documents a profile holds.
type Language struct {
	Lang    string `json:"lang"`
	Variant string `json:"variant,omitempty"`
	Default bool   `json:"isDefault"`
}

// Profile is one CV's directory: where its files are, and in what languages it
// exists.
type Profile struct {
	Slug     string
	Dir      string
	BuildDir string
	// Name is the name read from the document, and is filled in by List only.
	Name string
}

func (p *Profile) JSONPath() string { return filepath.Join(p.Dir, DefaultDoc) }

// DocPath is a language's document; an empty language means the default one.
func (p *Profile) DocPath(lang string) string { return filepath.Join(p.Dir, DocName(lang)) }

func (p *Profile) HTMLFor(lang string) string {
	return filepath.Join(p.BuildDir, artefact(lang, "html"))
}
func (p *Profile) PDFFor(lang string) string { return filepath.Join(p.BuildDir, artefact(lang, "pdf")) }

func artefact(lang, ext string) string {
	if lang == "" || lang == "default" {
		return "cv." + ext
	}
	return "cv." + lang + "." + ext
}

// PhotoPath is the portrait, whatever it was uploaded as. Empty when there is
// none.
func (p *Profile) PhotoPath() string {
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".svg"} {
		path := filepath.Join(p.Dir, "photo"+ext)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// Languages is every document this CV exists as.
//
// The default one comes first, labelled with its own meta.lang so an interface
// can name it. meta sits outside the content envelope, so this read is the same
// whatever shape the document is in.
func (p *Profile) Languages() []Language {
	out := []Language{}
	if raw, err := os.ReadFile(p.JSONPath()); err == nil {
		var doc map[string]any
		lang := "en"
		if json.Unmarshal(raw, &doc) == nil {
			if meta, ok := document.Obj(doc, "meta"); ok {
				if v := document.Str(meta, "lang"); v != "" {
					lang = v
				}
			}
		}
		out = append(out, Language{Lang: lang, Default: true})
	}
	names, err := os.ReadDir(p.Dir)
	if err != nil {
		return out
	}
	var found []string
	for _, e := range names {
		if m := langRE.FindStringSubmatch(e.Name()); m != nil {
			found = append(found, m[1])
		}
	}
	sort.Strings(found)
	for _, lang := range found {
		out = append(out, Language{Lang: lang, Variant: lang})
	}
	return out
}

// Repository is where profiles live.
type Repository struct {
	// DataDirOf and BuildDirOf are read on every access. Freezing them at
	// construction would make the answer depend on when this package happened
	// to be loaded, which is a trap that only shows itself once.
	DataDirOf  func() string
	BuildDirOf func() string
}

// New builds a repository rooted under home, with the environment able to move
// either directory elsewhere — onto a volume, or into a separate repository —
// without touching any code.
func New(home string) *Repository {
	return &Repository{
		DataDirOf:  func() string { return envDir("PICVERT_DATA", filepath.Join(home, "data")) },
		BuildDirOf: func() string { return envDir("PICVERT_BUILD", filepath.Join(home, "build")) },
	}
}

func envDir(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		abs, err := filepath.Abs(v)
		if err == nil {
			return abs
		}
	}
	return fallback
}

func (r *Repository) DataDir() string  { return r.DataDirOf() }
func (r *Repository) BuildDir() string { return r.BuildDirOf() }

// For is a slug's profile, whether or not it exists on disk.
func (r *Repository) For(slug string) (*Profile, error) {
	if err := CheckSlug(slug); err != nil {
		return nil, err
	}
	return &Profile{
		Slug:     slug,
		Dir:      filepath.Join(r.DataDir(), slug),
		BuildDir: filepath.Join(r.BuildDir(), slug),
	}, nil
}

// Get is a slug's profile, and fails when there is no CV there.
func (r *Repository) Get(slug string) (*Profile, error) {
	p, err := r.For(slug)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(p.JSONPath()); err != nil {
		return nil, fmt.Errorf("unknown CV: %q", slug)
	}
	return p, nil
}

// List is every profile that exists, sorted, each carrying the name read from
// its document.
//
// It never fails: an unreadable profile keeps its slug as its name. This runs
// on every request to the inventory, and one corrupt document must not be able
// to empty the list.
func (r *Repository) List() []*Profile {
	entries, err := os.ReadDir(r.DataDir())
	if err != nil {
		return nil
	}
	var out []*Profile
	for _, e := range entries {
		if !e.IsDir() || !slugRE.MatchString(e.Name()) {
			continue
		}
		p, err := r.For(e.Name())
		if err != nil {
			continue
		}
		if _, err := os.Stat(p.JSONPath()); err != nil {
			continue
		}
		p.Name = p.Slug
		if raw, err := os.ReadFile(p.JSONPath()); err == nil {
			var doc map[string]any
			if json.Unmarshal(raw, &doc) == nil {
				if id, ok := document.Identity(doc); ok {
					if name := document.Str(id, "name"); name != "" {
						p.Name = name
					}
				}
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// Default is the profile served at the root of the site: the one named by
// PICVERT_PROFILE, else the only one present, else the first alphabetically.
func (r *Repository) Default() *Profile {
	all := r.List()
	if len(all) == 0 {
		return nil
	}
	if wanted := strings.TrimSpace(os.Getenv("PICVERT_PROFILE")); wanted != "" {
		for _, p := range all {
			if p.Slug == wanted {
				return p
			}
		}
	}
	return all[0]
}
