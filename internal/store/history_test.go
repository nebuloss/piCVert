// The journal's rules, each of which has been got wrong at least once.
//
// They are tested here rather than through the service because every one of
// them is a decision about what a PERSON should read back, and the HTTP layer
// can only say that some journal came out. The rules are: an episode of typing
// is one entry, a change put back is no entry, a position is not an identity,
// and a journal is never worth failing a save over.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"picvert/internal/diff"
	"picvert/internal/document"
	"picvert/internal/profiles"
)

// journal is a History writing into a temporary profile, with the episode
// window and the cap stated rather than read from the environment: a test that
// depended on those would pass or fail on whatever the machine had set.
func journal(t *testing.T, window time.Duration, cap int) (*History, *profiles.Profile) {
	t.Helper()
	dir := t.TempDir()
	return &History{
		EpisodeOf:    func() time.Duration { return window },
		MaxEntriesOf: func() int { return cap },
	}, &profiles.Profile{Slug: "subject", Dir: dir}
}

// doc is a CV with one summary, which is the field most of these edit.
func doc(summary string) document.Doc {
	return document.Doc{
		"meta": map[string]any{"lang": "fr", "template": "t"},
		"content": map[string]any{
			"identity": map[string]any{"name": "Someone", "summary": summary},
		},
	}
}

// withSections is a CV carrying a list, for the reordering rules.
func listOf(items ...string) document.Doc {
	list := make([]any, 0, len(items))
	for _, it := range items {
		list = append(list, map[string]any{"id": it, "title": it})
	}
	return document.Doc{
		"meta":    map[string]any{"lang": "fr", "template": "t"},
		"content": map[string]any{"sections": list},
	}
}

// number reads a journalled value that is a number.
//
// Tolerant of the Go type on purpose. An entry's before and after are DISPLAY
// values, bound for a browser over JSON, where there is one number type — so
// whether this one came straight from the journal in memory or back from the
// file it was written to is not a difference anybody is entitled to see. A test
// that insisted on float64 would be testing which of those two paths ran.
func number(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	}
	return 0, false
}

func record(t *testing.T, h *History, p *profiles.Profile, before, after document.Doc) {
	t.Helper()
	if err := h.Record(p, before, after, "fr"); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

func entries(t *testing.T, h *History, p *profiles.Profile) []Entry {
	t.Helper()
	return h.List(p, 0, "", false)
}

// Typing is ONE entry, however many times it is saved.
//
// The editor saves on every change, so a paragraph rewritten over two minutes
// arrives here as dozens of writes. Recorded one per save it would be dozens of
// lines of "Summar → Summa → Summ", and the change somebody is looking for
// would be buried in the noise of them finding the words for it.
func TestAnEpisodeOfTypingIsOneEntry(t *testing.T) {
	h, p := journal(t, 5*time.Minute, 500)

	record(t, h, p, doc("A"), doc("An"))
	record(t, h, p, doc("An"), doc("An a"))
	record(t, h, p, doc("An a"), doc("An answer"))

	log := entries(t, h, p)
	if len(log) != 1 {
		t.Fatalf("three saves of one field made %d entries, want 1", len(log))
	}
	// The before is where the episode STARTED, not the previous keystroke —
	// that is the whole point of collapsing them.
	if log[0].Before != "A" {
		t.Errorf("before is %q, want the value the episode started from", log[0].Before)
	}
	if log[0].After != "An answer" {
		t.Errorf("after is %q, want the value it ended at", log[0].After)
	}
	if log[0].Count != 3 {
		t.Errorf("count is %d, want 3 saves", log[0].Count)
	}
}

// And typing after a long pause is a NEW entry.
//
// The episode window is what separates "still working on this" from "came back
// to it later", and the second is worth its own line.
func TestTypingAfterThePauseIsANewEntry(t *testing.T) {
	// A window of nothing, so the second save is always outside it.
	h, p := journal(t, 0, 500)

	record(t, h, p, doc("A"), doc("B"))
	record(t, h, p, doc("B"), doc("C"))

	if log := entries(t, h, p); len(log) != 2 {
		t.Fatalf("two edits a window apart made %d entries, want 2", len(log))
	}
}

// A field typed into and put back as it was leaves NOTHING.
//
// Not an entry saying it changed and changed back: as far as anybody reading
// this is concerned, nothing happened. A journal of changes that did not happen
// is a journal nobody trusts.
func TestAChangePutBackLeavesNothing(t *testing.T) {
	h, p := journal(t, 5*time.Minute, 500)

	record(t, h, p, doc("Original"), doc("Originl"))
	if len(entries(t, h, p)) != 1 {
		t.Fatal("the first edit was not recorded, so this test proves nothing")
	}
	record(t, h, p, doc("Originl"), doc("Original"))

	if log := entries(t, h, p); len(log) != 0 {
		t.Fatalf("a field put back left %d entries: %+v", len(log), log)
	}
}

// Something added and then removed never existed.
func TestSomethingAddedThenRemovedLeavesNothing(t *testing.T) {
	h, p := journal(t, 5*time.Minute, 500)

	record(t, h, p, listOf("a"), listOf("a", "b"))
	if len(entries(t, h, p)) == 0 {
		t.Fatal("the addition was not recorded, so this test proves nothing")
	}
	record(t, h, p, listOf("a", "b"), listOf("a"))

	if log := entries(t, h, p); len(log) != 0 {
		t.Fatalf("added then removed left %d entries: %+v", len(log), log)
	}
}

// A PATH IS A POSITION, NOT AN IDENTITY.
//
// Inserting shifts everything after it, so the thing now at a path is not the
// thing that was there. Extending the earlier entry would produce one line
// claiming an item turned into its neighbour — two unrelated edits merged into
// a sentence describing neither.
func TestAnInsertionIsNeverAStretchOfTyping(t *testing.T) {
	h, p := journal(t, 5*time.Minute, 500)

	record(t, h, p, listOf("a"), listOf("a", "b"))
	record(t, h, p, listOf("a", "b"), listOf("a", "b", "c"))

	log := entries(t, h, p)
	if len(log) != 2 {
		t.Fatalf("two insertions made %d entries, want 2 — a position is not an identity", len(log))
	}
	for _, e := range log {
		if e.Kind != diff.Add {
			t.Errorf("an insertion was recorded as %q, want %q", e.Kind, diff.Add)
		}
	}
}

// Filling in something just added belongs to the addition.
//
// A section created and then typed into is one event. Listing its every field
// afterwards would say nothing the "added" line does not, and would do it at
// length.
func TestFillingInSomethingJustAddedIsPartOfTheAddition(t *testing.T) {
	h, p := journal(t, 5*time.Minute, 500)

	before := listOf("a")
	added := listOf("a", "b")
	record(t, h, p, before, added)

	// Now type into the thing just added.
	filled := listOf("a", "b")
	sections, _ := document.AsObject(filled["content"])
	list, _ := sections["sections"].([]any)
	item, _ := document.AsObject(list[1])
	item["title"] = "A fuller title"
	record(t, h, p, added, filled)

	log := entries(t, h, p)
	if len(log) != 1 {
		t.Fatalf("adding then filling in made %d entries, want 1: %+v", len(log), log)
	}
	if log[0].Kind != diff.Add {
		t.Errorf("the event is %q, want it to stay %q", log[0].Kind, diff.Add)
	}
}

// A reorder is a gesture, not a stretch of typing, and says what moved where.
func TestAReorderNamesWhatMovedAndIsItsOwnEvent(t *testing.T) {
	h, p := journal(t, 5*time.Minute, 500)

	record(t, h, p, listOf("a", "b", "c"), listOf("c", "a", "b"))

	log := entries(t, h, p)
	if len(log) != 1 {
		t.Fatalf("a reorder made %d entries, want 1: %+v", len(log), log)
	}
	if log[0].Kind != diff.Move {
		t.Fatalf("a reorder was recorded as %q, want %q", log[0].Kind, diff.Move)
	}
	// Two positions, not two lists: storing eight names before and the same
	// eight after would be exact and unreadable.
	from, fromOK := number(log[0].Before)
	to, toOK := number(log[0].After)
	if !fromOK || !toOK {
		t.Fatalf("a move recorded %v → %v, want two positions", log[0].Before, log[0].After)
	}
	if from == to {
		t.Errorf("the move says %v → %v, which is not a move", from, to)
	}
	// And it names the element, or the reader cannot tell which one travelled.
	if len(log[0].Trail) == 0 {
		t.Error("the move names nothing that moved")
	}
}

// Two reorders in a row stay two events.
func TestReordersDoNotCollapseIntoEachOther(t *testing.T) {
	h, p := journal(t, 5*time.Minute, 500)

	record(t, h, p, listOf("a", "b", "c"), listOf("b", "a", "c"))
	record(t, h, p, listOf("b", "a", "c"), listOf("b", "c", "a"))

	if log := entries(t, h, p); len(log) != 2 {
		t.Fatalf("two reorders made %d entries, want 2", len(log))
	}
}

// Creating a CV is ONE entry, not one per field it was born with.
func TestCreatingACVIsOneEntry(t *testing.T) {
	h, p := journal(t, 5*time.Minute, 500)

	record(t, h, p, nil, doc("A new CV, with several fields already set"))

	log := entries(t, h, p)
	if len(log) != 1 {
		t.Fatalf("creating a CV made %d entries, want 1", len(log))
	}
	if log[0].Path != "" {
		t.Errorf("the creation names the field %q, want the document itself", log[0].Path)
	}
}

// But typing into a new CV is NOT swallowed by its creation.
//
// A CV is created and then entirely typed. Absorbing all of that into the word
// "created" would hide the first minutes of every CV ever written here.
func TestTypingIntoANewCVIsNotSwallowedByItsCreation(t *testing.T) {
	h, p := journal(t, 5*time.Minute, 500)

	record(t, h, p, nil, doc("First"))
	record(t, h, p, doc("First"), doc("Second"))

	if log := entries(t, h, p); len(log) != 2 {
		t.Fatalf("creating then typing made %d entries, want 2: %+v", len(log), log)
	}
}

// The timestamp nobody edits is not a change.
//
// meta.updatedAt is rewritten by every single save and by no person, so a
// journal that recorded it would have an entry for every save saying nothing.
func TestTheSaveTimestampIsNotAChange(t *testing.T) {
	h, p := journal(t, 5*time.Minute, 500)

	before := doc("Same")
	after := doc("Same")
	meta, _ := document.AsObject(after["meta"])
	meta["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	record(t, h, p, before, after)

	if log := entries(t, h, p); len(log) != 0 {
		t.Fatalf("the save timestamp was journalled: %+v", log)
	}
}

// Most recent first, which is the order anybody reads a journal in.
func TestTheJournalReadsNewestFirst(t *testing.T) {
	h, p := journal(t, 0, 500)

	record(t, h, p, doc("one"), doc("two"))
	record(t, h, p, doc("two"), doc("three"))

	log := entries(t, h, p)
	if len(log) != 2 {
		t.Fatalf("got %d entries, want 2", len(log))
	}
	if log[0].After != "three" {
		t.Errorf("the newest entry is %q, want the last change made", log[0].After)
	}
}

// A journal is never worth failing a save over.
//
// The CV is already safely on disk by the time this runs. A damaged or
// unreadable journal must not turn a successful save into a failed one — the
// person would lose work over a record of work.
func TestAnUnreadableJournalDoesNotFailASave(t *testing.T) {
	h, p := journal(t, 5*time.Minute, 500)

	file := filepath.Join(p.Dir, historyFile)
	if err := os.WriteFile(file, []byte("{this is not the journal"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := h.Record(p, doc("A"), doc("B"), "fr"); err != nil {
		t.Fatalf("a damaged journal failed the save: %v", err)
	}
	// And the new change is journalled, rather than the journal staying broken.
	if log := entries(t, h, p); len(log) != 1 {
		t.Errorf("after recovering, the journal holds %d entries, want 1", len(log))
	}
}

// A damaged journal is SET ASIDE, not quietly destroyed.
//
// It used to be discarded: one bad byte and every entry was gone, overwritten
// by the next save, with nothing said. The CV is what matters and it is
// elsewhere — but a record somebody may want is not something to delete on
// their behalf without trace.
func TestADamagedJournalIsKeptAside(t *testing.T) {
	h, p := journal(t, 5*time.Minute, 500)

	file := filepath.Join(p.Dir, historyFile)
	const damaged = "{this is not the journal"
	if err := os.WriteFile(file, []byte(damaged), 0o600); err != nil {
		t.Fatal(err)
	}
	record(t, h, p, doc("A"), doc("B"))

	kept, err := os.ReadFile(file + ".bad")
	if err != nil {
		t.Fatalf("the damaged journal was destroyed rather than kept: %v", err)
	}
	if string(kept) != damaged {
		t.Errorf("what was kept aside is not what was found: %q", kept)
	}
}

// The cap is per language, so editing one does not evict the other.
//
// The journal holds every language of a CV, and the reader asks for one at a
// time. Trimming the whole file to the cap meant a busy afternoon on the French
// CV silently deleted the English CV's entire history — a CV losing a record it
// had no part in creating.
func TestTheCapIsPerLanguage(t *testing.T) {
	h, p := journal(t, 0, 4)

	// Four entries in English, which exactly fills the cap.
	for i := 0; i < 4; i++ {
		if err := h.Record(p, doc("en"+string(rune('a'+i))), doc("en"+string(rune('b'+i))), "en"); err != nil {
			t.Fatal(err)
		}
	}
	// Then a busy afternoon in French.
	for i := 0; i < 10; i++ {
		if err := h.Record(p, doc("fr"+string(rune('a'+i))), doc("fr"+string(rune('b'+i))), "fr"); err != nil {
			t.Fatal(err)
		}
	}

	english := h.List(p, 0, "en", true)
	if len(english) != 4 {
		t.Errorf("the English journal holds %d entries, want 4 — French editing evicted them", len(english))
	}
	french := h.List(p, 0, "fr", true)
	if len(french) != 4 {
		t.Errorf("the French journal holds %d entries, want the cap of 4", len(french))
	}
}

// The cap is honoured, or a journal is a disk filling up one keystroke at a
// time.
func TestTheJournalIsBounded(t *testing.T) {
	h, p := journal(t, 0, 3)

	for i := 0; i < 20; i++ {
		record(t, h, p, doc("v"+string(rune('a'+i))), doc("v"+string(rune('b'+i))))
	}

	log := entries(t, h, p)
	if len(log) != 3 {
		t.Fatalf("the journal holds %d entries, want the cap of 3", len(log))
	}
	// The ones kept are the NEWEST: a journal that evicted the recent past
	// would keep the half nobody is looking for.
	if log[0].After != "vu" {
		t.Errorf("the newest kept entry is %q, want the last change made", log[0].After)
	}
}

// Long prose is clipped rather than stored whole.
func TestLongValuesAreClipped(t *testing.T) {
	h, p := journal(t, 5*time.Minute, 500)

	long := ""
	for len(long) < maxText*3 {
		long += "a very long paragraph of prose, repeated at length. "
	}
	record(t, h, p, doc("short"), doc(long))

	log := entries(t, h, p)
	if len(log) != 1 {
		t.Fatalf("got %d entries, want 1", len(log))
	}
	kept, _ := log[0].After.(string)
	if len(kept) > maxText+8 {
		t.Errorf("a value was kept at %d characters, want it clipped near %d", len(kept), maxText)
	}
}

// What is written is valid JSON on disk, since something else reads it back.
func TestTheJournalOnDiskIsValid(t *testing.T) {
	h, p := journal(t, 5*time.Minute, 500)
	record(t, h, p, doc("A"), doc("B"))

	raw, err := os.ReadFile(filepath.Join(p.Dir, historyFile))
	if err != nil {
		t.Fatal(err)
	}
	var log []Entry
	if err := json.Unmarshal(raw, &log); err != nil {
		t.Fatalf("the journal on disk is not valid JSON: %v", err)
	}
	if len(log) != 1 {
		t.Errorf("the journal on disk holds %d entries, want 1", len(log))
	}
}

// Size counts every language together, which is what an administrator is shown.
func TestSizeCountsEveryLanguage(t *testing.T) {
	h, p := journal(t, 0, 500)

	if err := h.Record(p, doc("a"), doc("b"), "fr"); err != nil {
		t.Fatal(err)
	}
	if err := h.Record(p, doc("c"), doc("d"), "en"); err != nil {
		t.Fatal(err)
	}

	if got := h.Size(p); got != 2 {
		t.Errorf("Size is %d, want 2 — both languages", got)
	}
}

// The log stays in the order things happened.
//
// Load-bearing, and not merely tidy: the episode search walks backwards and
// STOPS at the first entry too old to extend, which is only correct if
// everything before it is older still. Extending an episode moves its entry to
// the end, so the order is by last change — and a test is the only thing
// standing between that and a future rearrangement that quietly breaks the
// search into finding nothing.
func TestTheLogStaysInOrder(t *testing.T) {
	h, p := journal(t, 0, 500)

	for i := 0; i < 6; i++ {
		record(t, h, p, doc("v"+string(rune('a'+i))), doc("v"+string(rune('b'+i))))
		// Distinct timestamps, which is what the ordering is in terms of.
		time.Sleep(time.Millisecond)
	}

	log := h.load(p)
	for i := 1; i < len(log); i++ {
		earlier, err := time.Parse(time.RFC3339Nano, log[i-1].At)
		if err != nil {
			t.Fatal(err)
		}
		later, err := time.Parse(time.RFC3339Nano, log[i].At)
		if err != nil {
			t.Fatal(err)
		}
		if later.Before(earlier) {
			t.Fatalf("entry %d is older than the one before it (%s then %s)",
				i, log[i-1].At, log[i].At)
		}
	}
}

// A journal replaced underneath the service is noticed.
//
// What is held in memory is keyed on the file's modification time and size, so
// a restore from a backup — which replaces the file without this process
// writing it — must be read afresh rather than answered from what was cached
// before it. Otherwise restoring a CV would bring its journal back everywhere
// except in the running service.
func TestAJournalReplacedOnDiskIsReread(t *testing.T) {
	h, p := journal(t, 0, 500)

	record(t, h, p, doc("a"), doc("b"))
	if len(entries(t, h, p)) != 1 {
		t.Fatal("nothing was journalled, so this test proves nothing")
	}

	// What a restore does: the file is replaced by another process.
	replacement := []Entry{
		{At: "2020-01-01T00:00:00Z", From: "2020-01-01T00:00:00Z", Lang: "fr",
			Path: "content.identity.name", Kind: "set",
			Before: "Older", After: "Restored", Count: 1},
		{At: "2020-01-02T00:00:00Z", From: "2020-01-02T00:00:00Z", Lang: "fr",
			Path: "content.identity.summary", Kind: "set",
			Before: "Also", After: "Restored too", Count: 1},
	}
	raw, err := json.Marshal(replacement)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(p.Dir, historyFile)
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	log := entries(t, h, p)
	if len(log) != 2 {
		t.Fatalf("the restored journal reads as %d entries, want 2 — the stale one was served", len(log))
	}
	if log[0].After != "Restored too" {
		t.Errorf("newest restored entry is %v, want the restored content", log[0].After)
	}
}

// Recording and reading at once is safe.
//
// The journal is now held in memory between calls, which makes it shared state:
// a save writes it while the editor's journal panel reads it, and those arrive
// on different connections at the same moment. Run under -race, this is what
// says so.
func TestTheJournalIsSafeUnderConcurrentUse(t *testing.T) {
	h, p := journal(t, time.Minute, 50)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			_ = h.List(p, 0, "fr", false)
			_ = h.Size(p)
		}
	}()
	for i := 0; i < 200; i++ {
		if err := h.Record(p, doc("v"+string(rune('a'+i%26))), doc("v"+string(rune('b'+i%26))), "fr"); err != nil {
			t.Fatal(err)
		}
	}
	<-done
}
