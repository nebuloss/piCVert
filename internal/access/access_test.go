package access

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func at(l *Log, moment time.Time) { l.now = func() time.Time { return moment } }

// The whole point is the grouping: six fetches from one address are one
// visitor, not six.
func TestFetchesAreGroupedByAddress(t *testing.T) {
	l := New()
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	at(l, base)
	l.Record("jean", "10.0.0.5", Viewer, "Mozilla/5.0 Chrome/140 Safari/537", false)
	at(l, base.Add(time.Minute))
	l.Record("jean", "10.0.0.5", Page, "Mozilla/5.0 Chrome/140 Safari/537", false)
	at(l, base.Add(2*time.Minute))
	l.Record("jean", "10.0.0.5", PDF, "Mozilla/5.0 Chrome/140 Safari/537", false)
	at(l, base.Add(3*time.Minute))
	l.Record("jean", "203.0.113.9", Viewer, "Mozilla/5.0 Firefox/141", false)

	at(l, base.Add(4*time.Minute))
	got := l.For("jean")
	if len(got.Visitors) != 2 {
		t.Fatalf("expected 2 addresses, got %d: %+v", len(got.Visitors), got.Visitors)
	}
	if got.Total != 4 {
		t.Errorf("total = %d, want 4", got.Total)
	}
	// Most recent first.
	if got.Visitors[0].IP != "203.0.113.9" {
		t.Errorf("the most recent address is %q, want 203.0.113.9", got.Visitors[0].IP)
	}
	first := got.Visitors[1]
	if first.Hits != 3 || first.Viewer != 1 || first.Pages != 1 || first.PDFs != 1 {
		t.Errorf("the repeat visitor summarised as %+v", first)
	}
	if first.Agent != "Chrome" {
		t.Errorf("agent = %q, want Chrome", first.Agent)
	}
	if !first.First.Equal(base) || !first.Last.Equal(base.Add(2*time.Minute)) {
		t.Errorf("span is %v..%v", first.First, first.Last)
	}
}

// An edit-link visit is the author, and must be marked as such.
//
// Without it an afternoon of the owner's own typing reads as an audience,
// which is the opposite of what this panel is for.
func TestTheAuthorIsNotAnAudience(t *testing.T) {
	l := New()
	l.Record("jean", "10.0.0.5", Editor, "", true)
	l.Record("jean", "203.0.113.9", Viewer, "", false)

	for _, v := range l.For("jean").Visitors {
		want := v.IP == "10.0.0.5"
		if v.Author != want {
			t.Errorf("%s: author = %v, want %v", v.IP, v.Author, want)
		}
	}
}

// The ring is bounded, and says so rather than under-reporting.
func TestTheRingIsBoundedAndSaysSo(t *testing.T) {
	l := New()
	l.PerCV = 10
	for i := 0; i < 25; i++ {
		l.Record("jean", fmt.Sprintf("10.0.0.%d", i), Page, "", false)
	}
	got := l.For("jean")
	if got.Kept != 10 {
		t.Errorf("kept %d entries, the ring holds %d", got.Kept, l.PerCV)
	}
	if got.Total != 25 {
		t.Errorf("total = %d, want 25 — a bounded log must still count what it dropped", got.Total)
	}
	if len(got.Visitors) != 10 {
		t.Errorf("%d addresses survive a ring of 10", len(got.Visitors))
	}
}

// One busy CV must not push every other CV out of the table.
func TestOneCVCannotEvictTheRest(t *testing.T) {
	l := New()
	l.MaxCVs = 3
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	for i, slug := range []string{"a", "b", "c"} {
		at(l, base.Add(time.Duration(i)*time.Minute))
		l.Record(slug, "10.0.0.1", Page, "", false)
	}
	// "a" is the least recently active, so it is the one that goes.
	at(l, base.Add(10*time.Minute))
	l.Record("d", "10.0.0.1", Page, "", false)

	if got := l.For("a").Total; got != 0 {
		t.Errorf("the oldest CV survived eviction with %d entries", got)
	}
	for _, slug := range []string{"b", "c", "d"} {
		if got := l.For(slug).Total; got == 0 {
			t.Errorf("%s was evicted, but it is more recent than a", slug)
		}
	}
}

// Entries older than the retention are not reported.
func TestOldEntriesFallOutOfTheWindow(t *testing.T) {
	l := New()
	l.Retain = time.Hour
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	at(l, base)
	l.Record("jean", "10.0.0.5", Page, "", false)
	at(l, base.Add(90*time.Minute))
	l.Record("jean", "203.0.113.9", Page, "", false)

	got := l.For("jean")
	if len(got.Visitors) != 1 || got.Visitors[0].IP != "203.0.113.9" {
		t.Errorf("the window kept %+v", got.Visitors)
	}
	if got.Total != 2 {
		t.Errorf("total = %d — what fell out of the window is still counted", got.Total)
	}
}

// A deleted CV leaves nothing behind.
func TestForgettingACVDropsItsReaders(t *testing.T) {
	l := New()
	l.Record("jean", "10.0.0.5", Page, "", false)
	l.Forget("jean")
	if got := l.For("jean"); len(got.Visitors) != 0 || got.Total != 0 {
		t.Errorf("addresses survived the CV: %+v", got)
	}
}

// A user agent is shown to tell a person from a robot, so that is what it must
// distinguish — and every browser claiming to be every other browser is the
// trap this walks into if the order is wrong.
func TestTheAgentNamesWhatItActuallyIs(t *testing.T) {
	cases := map[string]string{
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36":                     "Chrome",
		"Mozilla/5.0 (Windows NT 10.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36 Edg/140.0":             "Edge",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15": "Safari",
		"Mozilla/5.0 (X11; Linux x86_64; rv:141.0) Gecko/20100101 Firefox/141.0":                                                "Firefox",
		"WhatsApp/2.23": "WhatsApp/2.23",
		"Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)": "Slackbot-LinkExpanding 1.0 (+https://api\u2026",
		"": "",
	}
	for agent, want := range cases {
		if got := shorten(agent); got != want {
			t.Errorf("shorten(%.30q\u2026) = %q, want %q", agent, got, want)
		}
	}
}

// Recording happens on request-serving goroutines, so it must be safe to call
// from all of them at once. The race detector is the actual assertion.
func TestConcurrentRecordingIsSafe(t *testing.T) {
	l := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				l.Record(fmt.Sprintf("cv%d", n%3), fmt.Sprintf("10.0.%d.%d", n, j), Page, "", false)
				l.For(fmt.Sprintf("cv%d", n%3))
				l.Counts()
			}
		}(i)
	}
	wg.Wait()
	var total int64
	for _, n := range l.Counts() {
		total += n
	}
	if total != 400 {
		t.Errorf("counted %d of 400 fetches", total)
	}
}

// The whole-service view groups one address across every CV it touched.
//
// That is the question no per-CV summary can answer, and the reason this view
// exists: one address against one CV is a reader, and one address against nine
// is either the owner or somebody walking the tokens.
func TestOverallGroupsOneAddressAcrossCVs(t *testing.T) {
	l := New()
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)

	at(l, base)
	l.Record("jean", "203.0.113.9", Page, "Mozilla/5.0 Firefox/141", false)
	at(l, base.Add(time.Minute))
	l.Record("marie", "203.0.113.9", PDF, "Mozilla/5.0 Firefox/141", false)
	at(l, base.Add(2*time.Minute))
	l.Record("paul", "203.0.113.9", Page, "Mozilla/5.0 Firefox/141", false)
	at(l, base.Add(3*time.Minute))
	l.Record("jean", "198.51.100.4", Page, "", false)

	at(l, base.Add(4*time.Minute))
	got := l.Overall()
	if len(got.Visitors) != 2 {
		t.Fatalf("expected 2 addresses across 3 CVs, got %d: %+v", len(got.Visitors), got.Visitors)
	}
	if got.Total != 4 {
		t.Errorf("total = %d, want 4", got.Total)
	}

	var wide Visitor
	for _, v := range got.Visitors {
		if v.IP == "203.0.113.9" {
			wide = v
		}
	}
	if wide.Hits != 3 {
		t.Errorf("the roaming address has %d hits, want 3", wide.Hits)
	}
	// Most recently touched first, which is what makes the first name useful.
	want := []string{"paul", "marie", "jean"}
	if len(wide.CVs) != 3 {
		t.Fatalf("CVs = %v, want %v", wide.CVs, want)
	}
	for i, slug := range want {
		if wide.CVs[i] != slug {
			t.Errorf("CVs = %v, want %v (most recent first)", wide.CVs, want)
			break
		}
	}
}

// A single CV's summary names only that CV, whatever else the address did.
func TestForNamesOnlyTheCVItWasAsked(t *testing.T) {
	l := New()
	l.Record("jean", "203.0.113.9", Page, "", false)
	l.Record("marie", "203.0.113.9", Page, "", false)

	got := l.For("jean")
	if len(got.Visitors) != 1 {
		t.Fatalf("expected one address, got %+v", got.Visitors)
	}
	if v := got.Visitors[0]; len(v.CVs) != 1 || v.CVs[0] != "jean" {
		t.Errorf("asked about jean, answered with %v", v.CVs)
	}
	if got.Total != 1 {
		t.Errorf("total = %d — the other CV's fetch is not jean's", got.Total)
	}
}
