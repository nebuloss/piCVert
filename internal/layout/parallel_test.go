package layout

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// Can several goroutines measure text at once?
//
// # WHY THIS LIVES HERE AND NOT IN THE SERVER
//
// It was tested through the HTTP layer, by timing twelve concurrent previews
// against one. That measurement is dominated by the machine: on twelve cores it
// came out at 2.8× one, on two cores at 7.5×, and on a shared CI runner at
// 11.9× — which failed a threshold chosen on a twelve-core box. The number was
// telling the truth about the runner and nothing about the lock.
//
// The thing actually worth protecting is one line of this package: the glyph
// cache behind an RWMutex, so that measuring text — which every layout does
// thousands of times — does not queue behind a single lock. Measured HERE it is
// nearly pure CPU with no HTTP, no allocation of pages and no disk, so the
// signal is large and the noise is small.
//
// # WHAT IT WOULD CATCH
//
// A return to `sync.Mutex`, or a scratch buffer shared between goroutines
// rather than pooled. Both are one-word changes that look harmless and make the
// whole service serial.

// The fonts come from loadTestFonts in wrap_test.go: one loader for the
// package, so a face added there is measured here too.

// measuring is a realistic amount of text: a CV's worth of lines, measured over
// and over as the layout engine does.
var measuring = []string{
	"Ingénieur R&D et architecte système",
	"Refonte de ZeeOS, l'OS Linux embarqué des clients légers",
	"Conception d'un nouveau système de paquets pour l'OS",
	"Secure Boot UEFI : intégration de Shim et signature",
	"Université du Québec à Chicoutimi (UQAC)",
}

func measureAll(fonts *Fonts, times int) {
	for i := 0; i < times; i++ {
		for _, text := range measuring {
			fonts.Width(text, "Roboto", 10.6, 400, false, 0)
			fonts.Width(text, "Roboto", 10.6, 700, false, 0)
		}
	}
}

func TestMeasuringTextRunsInParallel(t *testing.T) {
	cores := runtime.GOMAXPROCS(0)
	if cores < 2 {
		// Nothing to measure: with one processor, parallel and sequential are
		// the same thing however the lock is written.
		t.Skip("one processor available")
	}

	fonts := loadTestFonts(t)
	// Warm, because the cache miss path takes a write lock legitimately and
	// what matters is the hit path — which is every lookup after the first
	// page of the first CV.
	measureAll(fonts, 2)

	const rounds = 400

	// Sequential, on one goroutine.
	start := time.Now()
	measureAll(fonts, rounds*cores)
	sequential := time.Since(start)

	// The same total work, spread over every processor.
	start = time.Now()
	var wg sync.WaitGroup
	for i := 0; i < cores; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			measureAll(fonts, rounds)
		}()
	}
	wg.Wait()
	parallel := time.Since(start)

	speedup := float64(sequential) / float64(parallel)
	t.Logf("%d processors: the same work took %v on one and %v spread out (%.1f× faster)",
		cores, sequential.Round(time.Millisecond), parallel.Round(time.Millisecond), speedup)

	// A GENEROUS floor, deliberately. Perfect scaling would be `cores`, and
	// anything above 1.3 means the goroutines are genuinely running at the same
	// time — which is the question. A global lock gives 1.0 or worse, and a
	// loaded machine cannot push a parallel run BELOW the sequential one it is
	// compared against, because both are measured under the same load.
	const floor = 1.3
	if speedup < floor {
		t.Errorf("measuring text is serialised: the same work took %.1f× longer "+
			"spread over %d processors than it did on one (%.2f× against a floor "+
			"of %.1f). The glyph cache lock is the likely cause",
			1/speedup, cores, speedup, floor)
	}
}

// And the same work must produce the same answer, whoever asks.
//
// The parallel path shares a cache and pools its scratch buffers, and a buffer
// handed to two goroutines at once would produce plausible, wrong widths rather
// than a crash — a CV that lays out differently depending on what else the
// service happened to be doing. Run under -race in CI, which is where a shared
// buffer is actually caught; this checks the answers agree.
func TestConcurrentMeasurementAgrees(t *testing.T) {
	fonts := loadTestFonts(t)

	want := make([]float64, len(measuring))
	for i, text := range measuring {
		want[i] = fonts.Width(text, "Roboto", 10.6, 400, false, 0)
	}

	var wg sync.WaitGroup
	wrong := make(chan string, 64)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 50; round++ {
				for j, text := range measuring {
					if got := fonts.Width(text, "Roboto", 10.6, 400, false, 0); got != want[j] {
						select {
						case wrong <- text:
						default:
						}
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(wrong)

	if text, bad := <-wrong; bad {
		t.Fatalf("measured %q differently under concurrency — a CV would lay out "+
			"differently depending on what else the service was doing", text)
	}
}
