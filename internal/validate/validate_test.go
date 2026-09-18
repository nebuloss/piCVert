// Tests transposed from test/engine.test.js (“== Validation ==”), plus the
// cases the TypeScript suite never had to state because its type system did.
//
// The example profile is the fixture, exactly as in the Node suite: it is the
// one document that must always be valid, and it is shipped with the engine,
// so there is nothing to keep in sync by hand.
package validate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"picvert/internal/document"
)

// demo reads the example CV afresh for each test: they mutate it, and a shared
// fixture would make them depend on their own order.
func demo(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "jean-dupont", "cv.json"))
	if err != nil {
		t.Fatalf("example profile unreadable: %v", err)
	}
	var d map[string]any
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("example profile is not valid JSON: %v", err)
	}
	return d
}

func content(t *testing.T, d map[string]any) map[string]any {
	t.Helper()
	c, ok := document.Obj(d, "content")
	if !ok {
		t.Fatal("example profile has no content")
	}
	return c
}

func identity(t *testing.T, d map[string]any) map[string]any {
	t.Helper()
	i, ok := document.Obj(content(t, d), "identity")
	if !ok {
		t.Fatal("example profile has no identity")
	}
	return i
}

func sections(t *testing.T, d map[string]any) []any {
	t.Helper()
	s, ok := document.Arr(content(t, d), "sections")
	if !ok {
		t.Fatal("example profile has no sections")
	}
	return s
}

// sectionOfType finds the first section of a type, so a test does not break
// when the example CV is reordered.
func sectionOfType(t *testing.T, d map[string]any, want string) map[string]any {
	t.Helper()
	for _, raw := range sections(t, d) {
		s, ok := raw.(map[string]any)
		if ok && s["type"] == want {
			return s
		}
	}
	t.Fatalf("the example profile has no %q section", want)
	return nil
}

// refuses asserts that validation fails, and that it fails for the stated
// reason. Asserting only "it failed" would pass for a document rejected by
// something else entirely.
func refuses(t *testing.T, d any, wantPath, wantMsg string) {
	t.Helper()
	_, err := Validate(d)
	if err == nil {
		t.Fatalf("expected a refusal mentioning %q, got none", wantMsg)
	}
	ve, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected a *validate.Error, got %T", err)
	}
	if wantPath != "" && ve.Path != wantPath {
		t.Errorf("path = %q, want %q (message: %s)", ve.Path, wantPath, ve.Error())
	}
	if !strings.Contains(ve.Msg, wantMsg) {
		t.Errorf("message = %q, want it to contain %q", ve.Msg, wantMsg)
	}
}

func TestExampleProfileIsValid(t *testing.T) {
	if _, err := Validate(demo(t)); err != nil {
		t.Fatalf("the shipped example must always validate: %v", err)
	}
}

func TestMissingIdentityRefused(t *testing.T) {
	refuses(t, map[string]any{
		"content": map[string]any{"sections": []any{}},
	}, "content.identity", "required section is missing")
}

func TestBlankNameRefused(t *testing.T) {
	d := demo(t)
	identity(t, d)["name"] = "  "
	refuses(t, d, "content.identity.name", "cannot be empty")
}

func TestUnknownColumnRefused(t *testing.T) {
	d := demo(t)
	sections(t, d)[0].(map[string]any)["column"] = "middle"
	refuses(t, d, "content.sections[0].column", "invalid value “middle”")
}

func TestUnknownSectionTypeRefused(t *testing.T) {
	d := demo(t)
	sections(t, d)[0].(map[string]any)["type"] = "carousel"
	refuses(t, d, "content.sections[0].type", "invalid value “carousel”")
}

func TestDuplicateSectionIDRefused(t *testing.T) {
	d := demo(t)
	all := sections(t, d)
	all[1].(map[string]any)["id"] = all[0].(map[string]any)["id"]
	refuses(t, d, "content.sections[1].id", "already used")
}

func TestOnlyBoldAllowedInRichText(t *testing.T) {
	d := demo(t)
	sectionOfType(t, d, "text")["text"] = "Bonjour <i>monde</i>"
	refuses(t, d, "", "only <b> is allowed")
}

func TestBoldIsAllowedInRichText(t *testing.T) {
	d := demo(t)
	sectionOfType(t, d, "text")["text"] = "Bonjour <b>monde</b> et <B>bonsoir</B>"
	if _, err := Validate(d); err != nil {
		t.Fatalf("<b> must pass, in either case: %v", err)
	}
}

func TestLanguageGaugeOutOfBoundsRefused(t *testing.T) {
	d := demo(t)
	langs, _ := document.Arr(sectionOfType(t, d, "languages"), "languages")
	langs[0].(map[string]any)["value"] = float64(140)
	refuses(t, d, "", "between 0 and 100")
}

func TestPhotoOutsideProfileRefused(t *testing.T) {
	d := demo(t)
	identity(t, d)["photo"] = "../../../etc/passwd"
	refuses(t, d, "content.identity.photo", "plain file name")
}

// The error must say WHERE, not just what. This is the assertion the Node
// suite makes on the message prefix.
func TestErrorCarriesAnActionablePath(t *testing.T) {
	d := demo(t)
	sections(t, d)[0].(map[string]any)["column"] = "middle"
	_, err := Validate(d)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.HasPrefix(err.Error(), "content.sections[0].column: ") {
		t.Errorf("message = %q, want it to start with the path", err.Error())
	}
}

// --- what the map-based port has to prove, and TypeScript never did ---------

// The reason this engine decodes into a map rather than into structs. If this
// test fails, every CV written by a newer version silently loses members the
// moment an older one saves it.
func TestUnknownPropertiesSurviveValidation(t *testing.T) {
	d := demo(t)
	identity(t, d)["futureField"] = "kept"
	sections(t, d)[0].(map[string]any)["futureFlag"] = true

	checked, err := Validate(d)
	if err != nil {
		t.Fatalf("unknown properties must be ignored, not refused: %v", err)
	}
	out := checked.Doc()
	if got := identity(t, out)["futureField"]; got != "kept" {
		t.Errorf("identity.futureField = %v, want \"kept\"", got)
	}
	if got := sections(t, out)[0].(map[string]any)["futureFlag"]; got != true {
		t.Errorf("section.futureFlag = %v, want true", got)
	}
}

// Length is counted in UTF-16 code units, like JavaScript's String.length.
// Counting bytes or runes instead would let the editor accept what the server
// refuses, on exactly the characters people cannot count themselves.
func TestLengthIsCountedInUTF16CodeUnits(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"abc", 3},
		{"éàü", 3}, // two bytes each, one unit each
		{"日本語", 3}, // three bytes each, one unit each
		{"👍", 2},   // outside the BMP: one rune, two units
		{"a👍b", 4},
	}
	for _, c := range cases {
		if got := length(c.in); got != c.want {
			t.Errorf("length(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// An absent list is an empty list — except where a minimum was asked for, and
// then the absence is exactly what fails.
func TestChipsGroupsMinimumIsEnforcedOnAbsence(t *testing.T) {
	d := demo(t)
	delete(sectionOfType(t, d, "chips"), "groups")
	refuses(t, d, "", "at least 1 item(s) expected")
}

func TestAbsentOptionalListIsAccepted(t *testing.T) {
	d := demo(t)
	delete(identity(t, d), "subtitle")
	if _, err := Validate(d); err != nil {
		t.Fatalf("an absent optional list must be read as empty: %v", err)
	}
}

// The empty chip variant is a real value, not a missing one. If omitempty-style
// thinking ever creeps in, the grey chip becomes unrepresentable.
func TestEmptyChipVariantIsValid(t *testing.T) {
	d := demo(t)
	groups, _ := document.Arr(sectionOfType(t, d, "chips"), "groups")
	first := groups[0].(map[string]any)
	chips, _ := document.Arr(first, "chips")
	chips[0].(map[string]any)["variant"] = ""
	if _, err := Validate(d); err != nil {
		t.Fatalf("the empty variant is the ordinary grey chip: %v", err)
	}
}

func TestInvalidChipVariantNamesTheEmptyOneInTheExpectedList(t *testing.T) {
	d := demo(t)
	groups, _ := document.Arr(sectionOfType(t, d, "chips"), "groups")
	chips, _ := document.Arr(groups[0].(map[string]any), "chips")
	chips[0].(map[string]any)["variant"] = "neon"
	_, err := Validate(d)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), `expected: "", pri, ter, ghost`) {
		t.Errorf("message = %q, want the empty variant shown as \"\"", err.Error())
	}
}

func TestNonObjectDocumentRefused(t *testing.T) {
	for _, in := range []any{nil, "a string", float64(3), []any{}} {
		if _, err := Validate(in); err == nil {
			t.Errorf("Validate(%#v) must fail", in)
		}
	}
}

// --- the envelope -----------------------------------------------------------

func TestUpgradeLiftsLegacyDocuments(t *testing.T) {
	legacy := map[string]any{
		"meta":     map[string]any{"lang": "fr"},
		"identity": map[string]any{"name": "Jean"},
		"sections": []any{},
	}
	out, ok := document.Upgrade(legacy).(map[string]any)
	if !ok {
		t.Fatal("Upgrade must return an object for an object")
	}
	if _, present := out["identity"]; present {
		t.Error("identity must move into content")
	}
	c, ok := document.Obj(out, "content")
	if !ok {
		t.Fatal("content must exist after the lift")
	}
	if id, _ := document.Obj(c, "identity"); document.Str(id, "name") != "Jean" {
		t.Error("the identity must survive the lift intact")
	}
	if document.Str(document.Meta(out), "lang") != "fr" {
		t.Error("meta must stay at the top level")
	}
	// The caller may still be holding the original to compare against.
	if _, present := legacy["identity"]; !present {
		t.Error("Upgrade must not modify its argument")
	}
}

func TestUpgradeLeavesModernDocumentsAlone(t *testing.T) {
	d := demo(t)
	if document.NeedsUpgrade(d) {
		t.Error("a document already carrying the envelope needs no upgrade")
	}
	if _, err := Validate(document.Upgrade(d)); err != nil {
		t.Fatalf("upgrading a modern document must be a no-op: %v", err)
	}
}

func TestUpgradeFillsMissingSectionsWithAnEmptyList(t *testing.T) {
	out, _ := document.Upgrade(map[string]any{
		"identity": map[string]any{"name": "Jean"},
	}).(map[string]any)
	c, _ := document.Obj(out, "content")
	s, ok := document.Arr(c, "sections")
	if !ok || len(s) != 0 {
		t.Errorf("sections = %#v, want an empty list", c["sections"])
	}
}

func TestUpgradePassesNonObjectsThrough(t *testing.T) {
	for _, in := range []any{nil, "x", float64(1), []any{1.0}} {
		if got := document.Upgrade(in); got == nil && in != nil {
			t.Errorf("Upgrade(%#v) must pass the value through", in)
		}
	}
}

// An embedded portrait is refused, and the message says what to do instead.
//
// This is the shape a CV exported from another tool arrives in, and it is a
// trap: the page renders perfectly, because drawing one inlines the portrait
// as a data URI anyway and passes an existing one straight through. Nothing
// fails until the first save, long after the cause would have been obvious.
//
// The generic length error it used to give — "too long (269166 characters, 200
// maximum)" — is true and useless. Nobody reading it would guess that the fix
// is to write the image to a file beside cv.json.
func TestAnEmbeddedPortraitIsRefusedByName(t *testing.T) {
	doc := map[string]any{
		"meta": map[string]any{"lang": "en"},
		"content": map[string]any{
			"identity": map[string]any{
				"name": "Jean Dupont",
				// Long enough to trip the length check too, so this proves the
				// order of the two: the specific message must win.
				"photo": "data:image/png;base64," + strings.Repeat("A", 4000),
			},
			"sections": []any{},
		},
	}
	_, err := Validate(doc)
	if err == nil {
		t.Fatal("an embedded portrait was accepted — it cannot be saved later")
	}
	if strings.Contains(err.Error(), "too long") {
		t.Fatalf("the generic length error won: %v", err)
	}
	for _, wanted := range []string{"beside cv.json", "photo.png"} {
		if !strings.Contains(err.Error(), wanted) {
			t.Fatalf("the message does not say what to do (%q): %v", wanted, err)
		}
	}
}

// A plain file name is still what a portrait looks like.
func TestAPortraitFileNameIsAccepted(t *testing.T) {
	doc := map[string]any{
		"meta": map[string]any{"lang": "en"},
		"content": map[string]any{
			"identity": map[string]any{"name": "Jean Dupont", "photo": "photo.png"},
			"sections": []any{},
		},
	}
	if _, err := Validate(doc); err != nil {
		t.Fatalf("a normal portrait was refused: %v", err)
	}
}
