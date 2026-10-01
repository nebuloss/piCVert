// What the journal costs, because it was measured to cost more than the save
// it is attached to.
//
// Every save read the whole journal and parsed it again, then wrote it back
// indented. At the default cap of five hundred entries that was about 1.2 ms —
// against a save advertised at 496 us — and all of it inside the single mutex
// every write in the service shares, so it was a ceiling on the whole service
// rather than a cost to one editor.
//
// Keeping what was parsed, and writing the journal compactly, brings it to
// about 0.2 ms. This benchmark is here so that a change putting the millisecond
// back is visible rather than discovered on a CV with a long history:
//
//	go test ./internal/store/ -run XXX -bench Record
package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"picvert/internal/profiles"
)

func BenchmarkRecordIntoAFullJournal(b *testing.B) {
	dir := b.TempDir()
	p := &profiles.Profile{Slug: "x", Dir: dir}
	h := &History{
		EpisodeOf:    func() time.Duration { return 5 * time.Minute },
		MaxEntriesOf: func() int { return 500 },
	}
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	log := make([]Entry, 0, 500)
	for i := 0; i < 500; i++ {
		log = append(log, Entry{
			At: old, From: old, Lang: "fr", Path: "content.identity.f", Kind: "set",
			Before: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			After:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Trail:  []Crumb{{Label: "Profile", I18n: "fields.identity"}, {Label: "Summary"}},
			Count:  1,
		})
	}
	if err := h.save(p, log); err != nil {
		b.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(dir, historyFile))
	b.Logf("journal on disk: %d bytes, %d entries", info.Size(), len(log))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := h.Record(p, doc("version A"), doc("version B"), "fr"); err != nil {
			b.Fatal(err)
		}
	}
}
