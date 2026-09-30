package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The administration page and its script must agree about every identifier.
//
// # WHY THIS FAILURE IS WORSE THAN IT LOOKS
//
// The script resolves every element it needs in its constructor, through
// `need()`, which THROWS when one is missing. That happens at module load,
// before any handler is attached — so a single renamed or deleted id does not
// break one control, it leaves the whole administration page inert: no
// inventory, no tabs, no metrics, no error visible anywhere except a browser
// console nobody has open.
//
// Nothing else catches it. The template still renders, the bundle still
// builds, `tsc` is happy because the ids are strings, and every Go test here
// asserts HTTP status and JSON rather than what is in the markup.
func TestTheAdminPageHasEveryElementItsScriptNeeds(t *testing.T) {
	page := adminHTML(t)
	script := readUI(t, filepath.Join("ui", "admin", "admin.ts"))

	// Every `need(root, '#thing')` in the script, whatever its type argument.
	wanted := regexp.MustCompile(`need(?:<[^>]*>)?\(root, '#([\w-]+)'\)`)
	found := wanted.FindAllStringSubmatch(script, -1)
	if len(found) < 10 {
		t.Fatalf("only %d element lookups found in admin.ts — this test is "+
			"no longer reading the script it is meant to check", len(found))
	}

	for _, m := range found {
		id := m[1]
		if !strings.Contains(page, `id="`+id+`"`) {
			t.Errorf("admin.ts asks for #%s, which the page does not contain — "+
				"the whole interface fails to start when one of these is missing", id)
		}
	}
}

// The tabs must be coherent: each names a panel that exists, and exactly one
// view is showing.
//
// A tab whose aria-controls points at nothing is a tab that looks right and
// does nothing; two panels visible at once is the stacked layout these tabs
// exist to replace, arrived at by accident.
func TestTheAdminTabsNameRealPanels(t *testing.T) {
	page := adminHTML(t)

	tabs := regexp.MustCompile(`<button class="view-tab" id="(tab-[\w-]+)" role="tab"\s+aria-selected="(true|false)"\s+aria-controls="([\w-]+)"`)
	found := tabs.FindAllStringSubmatch(page, -1)
	if len(found) < 4 {
		t.Fatalf("found %d tabs; the page is meant to be divided into them", len(found))
	}

	selected := 0
	for _, m := range found {
		id, on, controls := m[1], m[2] == "true", m[3]

		panel := panelOf(t, page, controls)
		if panel == "" {
			t.Errorf("%s controls #%s, which is not on the page", id, controls)
			continue
		}
		if !strings.Contains(panel, `role="tabpanel"`) {
			t.Errorf("%s controls #%s, which is not a tabpanel", id, controls)
		}
		if !strings.Contains(panel, `aria-labelledby="`+id+`"`) {
			t.Errorf("#%s does not point back at %s, so a screen reader cannot "+
				"say which tab it belongs to", controls, id)
		}

		// The selected tab's panel is shown; every other is hidden. A page
		// that starts with two panels open is the stacked layout again.
		//
		// The OPENING TAG only. A view legitimately contains hidden things of
		// its own — the creation form is one — and searching the whole body
		// reports the panel itself as hidden because of something inside it.
		hidden := strings.Contains(openingTag(page, controls), " hidden")
		if on {
			selected++
			if hidden {
				t.Errorf("%s is the selected tab and its panel is hidden", id)
			}
		} else if !hidden {
			t.Errorf("%s is not selected but #%s is showing", id, controls)
		}
	}
	if selected != 1 {
		t.Errorf("%d tabs are selected at once; exactly one view shows at a time", selected)
	}
}

// Warnings are NOT inside a tab.
//
// Each is a thing to act on today — no backup taken, no password, the CVs near
// their ceiling. Filed under a tab they are invisible until somebody goes
// looking, and nobody goes looking for a warning they have not been shown.
// That is the one thing the tabs must not be allowed to hide, so it is pinned
// here rather than left to whoever next rearranges the page.
func TestWarningsAreNotFiledUnderATab(t *testing.T) {
	page := adminHTML(t)

	alerts := strings.Index(page, `id="alerts"`)
	if alerts < 0 {
		t.Fatal("the page has nowhere to put a warning")
	}
	tabs := strings.Index(page, `class="views"`)
	if tabs < 0 {
		t.Fatal("the page has no tabs")
	}
	if alerts > tabs {
		t.Error("the warnings are below the tab bar, so they can be hidden by " +
			"whichever view is open")
	}
	for _, panel := range []string{"view-cvs", "view-requests", "view-deleted", "view-service"} {
		if body := panelOf(t, page, panel); strings.Contains(body, `id="alerts"`) {
			t.Errorf("the warnings are inside #%s", panel)
		}
	}
}

// adminHTML is the administration page as a browser receives it.
func adminHTML(t *testing.T) string {
	t.Helper()
	body, err := render("admin.html", map[string]any{
		"PublicURL": "", "PublicPort": "3000",
	})
	if err != nil {
		t.Fatalf("the administration page does not render: %v", err)
	}
	return body
}

// panelOf returns the markup of one element, from its id to the end of the
// section. Crude on purpose: a parser here would be a dependency added to
// check four attributes.
func panelOf(t *testing.T, page, id string) string {
	t.Helper()
	start := strings.Index(page, `id="`+id+`"`)
	if start < 0 {
		return ""
	}
	// Back to the start of the tag, forward to the end of the section.
	open := strings.LastIndex(page[:start], "<")
	end := strings.Index(page[start:], "</section>")
	if open < 0 || end < 0 {
		return page[start:]
	}
	return page[open : start+end]
}

// openingTag is just the element's own tag, without anything nested in it.
func openingTag(page, id string) string {
	start := strings.Index(page, `id="`+id+`"`)
	if start < 0 {
		return ""
	}
	open := strings.LastIndex(page[:start], "<")
	shut := strings.Index(page[start:], ">")
	if open < 0 || shut < 0 {
		return ""
	}
	return page[open : start+shut]
}

// readUI reads a source file of the interface, which lives beside this package.
func readUI(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("cannot read %s: %v", rel, err)
	}
	return string(raw)
}

// The page says what things are, not what the software did to them.
//
// "Set aside" was the deleted list: it is what the code does — the folder is
// moved aside — and not what the person did, which was delete a CV. Somebody
// looking for a CV they deleted looks for the word they used. The reassurance
// the euphemism was reaching for belongs in the lede, where it can be said
// properly, and is.
func TestTheViewsAreNamedForWhatThePersonDid(t *testing.T) {
	page := adminHTML(t)

	if strings.Contains(strings.ToLower(page), "set aside") {
		t.Error("the page still says “set aside” somewhere; the list is of " +
			"CVs the reader DELETED, and that is the word they will look for")
	}
	for _, word := range []string{"Deleted", "Requests", "Service", "CVs"} {
		if !strings.Contains(page, ">\n      "+word+" ") && !strings.Contains(page, ">"+word+" ") {
			t.Errorf("no tab named %q", word)
		}
	}
	// And the reassurance is not lost with the euphemism.
	if !strings.Contains(page, "can be restored") && !strings.Contains(page, "undone") {
		t.Error("the deleted view does not say that a deletion can be undone")
	}
}
