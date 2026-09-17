package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"picvert/internal/config"
)

// The binary is self-contained, or it is not a release.
//
// # WHAT THIS EXISTS FOR
//
// The service claimed in its README, its documentation, its installer and
// several comments to be one file with the templates, the fonts and the
// interface inside it. Only the interface was. Installing a published release
// and running it anywhere but a checkout produced:
//
//	default template “material-you” not found
//
// A service that starts, serves a front page, and cannot render a single CV.
//
// Nothing caught it because EVERY test set PICVERT_HOME to the checkout, where
// the directory it needed happened to be. This one deliberately does not: it
// points the engine at an empty directory, which is what an installed binary
// sees.
func TestItWorksWithNothingBesideTheBinary(t *testing.T) {
	// Empty, and staying empty. No templates, no fonts, nothing to fall back on
	// but what was compiled in.
	empty := t.TempDir()
	if entries, err := os.ReadDir(empty); err != nil || len(entries) != 0 {
		t.Fatalf("the test's own directory is not empty: %v %v", entries, err)
	}

	data := t.TempDir()
	cfg := config.Defaults()
	cfg.Home = empty
	cfg.DataDir = data
	cfg.Admin.Listen = ""

	s, err := New(empty, cfg, "test")
	if err != nil {
		t.Fatal(err)
	}

	// A template, from inside the binary.
	all, err := s.Registry.All()
	if err != nil {
		t.Fatalf("no templates with nothing beside the binary: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("the binary carries no templates")
	}
	t.Logf("%d templates, from %s", len(all), s.Registry.Source())

	// A CV, made and rendered.
	if _, err := s.Store.Create("jean", "Jean Dupont", "", "en"); err != nil {
		t.Fatalf("could not create a CV: %v", err)
	}
	p, err := s.Profiles.Get("jean")
	if err != nil {
		t.Fatal(err)
	}

	entry, err := s.render(p, "")
	if err != nil {
		t.Fatalf("could not render a page: %v", err)
	}
	if len(entry.html) < 10_000 {
		t.Errorf("the page is %d bytes — too small to be a CV", len(entry.html))
	}
	// The fonts travel INSIDE the page, so their absence is visible here rather
	// than as a CV in the wrong typeface on somebody else's machine.
	if !containsFont(entry.html) {
		t.Error("the page carries no embedded font: it would be drawn in " +
			"whatever the reader has installed, which is not what it was measured in")
	}

	pdf, err := s.pdf(p, "")
	if err != nil {
		t.Fatalf("could not render a PDF: %v", err)
	}
	if len(pdf) < 5_000 {
		t.Errorf("the PDF is %d bytes — too small to hold a page", len(pdf))
	}
	t.Logf("page %d KB, PDF %d KB, with nothing on disk but the CV",
		len(entry.html)/1024, len(pdf)/1024)

	// And over HTTP, which is what anybody would actually do.
	h := s.Handler()
	links, err := s.Tokens.ForProfile("jean")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/cv.html", "/cv.pdf", "/edit/"} {
		w := call(t, h, "GET", "/e/"+links.Edit+path, nil, nil)
		if w.Code != http.StatusOK {
			t.Errorf("%s answered %d with nothing beside the binary", path, w.Code)
		}
	}
}

// A template directory beside the binary still wins, because adding a layout
// has to stay a matter of adding a folder.
func TestADirectoryBesideTheBinaryWins(t *testing.T) {
	home := t.TempDir()
	root := repoDir(t)

	// One template, copied out of the checkout.
	from := filepath.Join(root, "templates", "material-you")
	to := filepath.Join(home, "templates", "material-you")
	if err := copyTree(from, to); err != nil {
		t.Skipf("cannot copy a template: %v", err)
	}

	cfg := config.Defaults()
	cfg.Home = home
	cfg.DataDir = t.TempDir()
	cfg.Admin.Listen = ""

	s, err := New(home, cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	if s.Registry.Source() == "built in" {
		t.Fatal("a template directory beside the binary was ignored")
	}
	all, err := s.Registry.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("found %d templates, expected only the one on disk", len(all))
	}
}

func containsFont(html string) bool {
	return len(html) > 0 &&
		(indexOf(html, "@font-face") >= 0 && indexOf(html, "data:font/woff2") >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func repoDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 6 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			break
		}
		dir = up
	}
	t.Fatal("cannot find the checkout")
	return ""
}

func copyTree(from, to string) error {
	return filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
}
