package layout

import (
	"sync"
	"testing"
	"time"
)

// Can several goroutines measure text at the same time?
//
// # WHY THIS DOES NOT TIME ANYTHING
//
// It did, twice, and failed on CI both times. The first version timed twelve
// HTTP requests against one; the second timed the same work spread over the
// processors against one goroutine. Both passed on a twelve-core build host and
// failed on a runner — the second at 1.02× where the host gave 1.6×.
//
// The reason is that Go reporting two processors does not mean two execution
// units. A shared CI runner gives two contended vCPUs, and pinning two threads
// to one core on the build host reproduces the runner's number exactly. So NO
// threshold can tell "the lock is global" from "there is one CPU": on a machine
// with one usable core, the correct answer and the broken answer are the same
// number.
//
// The property worth protecting is not speed. It is that the lock ADMITS
// READERS TOGETHER — and that is a fact about the code, testable exactly.
//
// # HOW THIS TESTS IT INSTEAD
//
// Hold the font table's read lock, then measure text from another goroutine.
//
//	sync.RWMutex   the measurement takes a read lock too, both proceed
//	sync.Mutex     the measurement waits for a lock nobody is going to release
//
// Deterministic, instant, and the same answer on one processor or ninety-six.
// The timeout exists only so a failure is a message rather than a hung suite.

func TestMeasuringAdmitsSeveralReadersAtOnce(t *testing.T) {
	fonts := loadTestFonts(t)
	// Warm, so the measurement below is a cache HIT and takes only read locks.
	// A miss legitimately takes a write lock to store what it computed, and
	// that is not the path being tested.
	fonts.Width("Ingénieur", "Roboto", 10.6, Regular, false, 0)

	// A reader, held open for the duration.
	fonts.mu.RLock()
	defer fonts.mu.RUnlock()

	done := make(chan float64, 1)
	go func() {
		// Takes the table's lock as every measurement does. With an RWMutex
		// this proceeds alongside the reader above; with a Mutex it waits for
		// a lock that is not going to be released.
		done <- fonts.Width("Ingénieur", "Roboto", 10.6, Regular, false, 0)
	}()

	select {
	case width := <-done:
		if width <= 0 {
			t.Fatalf("measured a word as %v", width)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("measuring text blocked while another reader held the lock.\n" +
			"The font table is behind an exclusive lock again, which makes every " +
			"layout in the process queue behind every other — see the comment at " +
			"the top of this file.")
	}
}

// The same, for the per-face caches holding the glyph widths.
//
// Two locks, because they protect different things and either could be made
// exclusive on its own: the table says which faces exist, and each face holds
// the advances measured from it. The second is the one every character of every
// line goes through.
func TestGlyphWidthsAdmitSeveralReadersAtOnce(t *testing.T) {
	fonts := loadTestFonts(t)
	fonts.Width("Ingénieur", "Roboto", 10.6, Regular, false, 0)

	face := fonts.faceFor("Roboto", Regular, false)
	if face == nil {
		t.Fatal("no face loaded")
	}

	face.mu.RLock()
	defer face.mu.RUnlock()

	done := make(chan float64, 1)
	go func() {
		buf, release := fonts.borrow()
		defer release()
		done <- face.advance(buf, 'e')
	}()

	select {
	case advance := <-done:
		if advance <= 0 {
			t.Fatalf("measured a glyph as %v", advance)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reading a cached glyph width blocked while another reader held " +
			"the cache. Measuring is serialised per face, which is the hot path " +
			"of every layout in the service.")
	}
}

// Each measurement gets its own scratch space.
//
// sfnt's contract is that a Font may be used concurrently PROVIDED each caller
// has its own Buffer. A shared one does not crash — it produces plausible,
// wrong widths, which is a CV that lays out differently depending on what else
// the service happened to be doing. The race detector catches the sharing; this
// catches the consequence, and does so without timing anything.
func TestConcurrentMeasurementAgrees(t *testing.T) {
	fonts := loadTestFonts(t)

	lines := []string{
		"Ingénieur R&D et architecte système",
		"Refonte de ZeeOS, l'OS Linux embarqué des clients légers",
		"Université du Québec à Chicoutimi (UQAC)",
	}
	want := make([]float64, len(lines))
	for i, text := range lines {
		want[i] = fonts.Width(text, "Roboto", 10.6, Regular, false, 0)
	}

	var wg sync.WaitGroup
	wrong := make(chan string, 8)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for round := 0; round < 100; round++ {
				for j, text := range lines {
					// Another weight in between, so two faces are in use at
					// once exactly as a real layout uses them — that is what
					// would expose a buffer shared across faces.
					if (round+n)%2 == 0 {
						fonts.Width(text, "Roboto", 10.6, Bold, false, 0)
					}
					if got := fonts.Width(text, "Roboto", 10.6, Regular, false, 0); got != want[j] {
						select {
						case wrong <- text:
						default:
						}
						return
					}
				}
			}
		}(i)
	}
	wg.Wait()
	close(wrong)

	if text, bad := <-wrong; bad {
		t.Fatalf("measured %q differently under concurrency — a CV would lay out "+
			"differently depending on what else the service was doing", text)
	}
}
