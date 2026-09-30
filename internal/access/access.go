// Package access records who fetched a CV, so its owner can be told.
//
// # WHY THIS EXISTS
//
// A CV here is published by handing over a link. That link is the whole of its
// access control, and the one question it raises is the one nothing could
// answer: has anybody actually opened it, and is it being passed around. The
// service counted requests in total and by nothing else, so "the recruiter says
// they never received it" and "this link is on a mailing list" looked
// identical from the administration page — both were a number going up.
//
// # WHY IN MEMORY, AND WHY THAT IS NOT A SHORTCUT
//
// An address is personal data. Written to disk it becomes something to
// retain, rotate, back up and eventually explain, and this is a service whose
// design goes to some trouble to hold as little as possible about the people
// reading a CV — no analytics, no cookies on the public surface, robots turned
// away at two levels.
//
// Held in memory it answers the question while the question is live ("who has
// opened this, this week") and forgets by itself: a restart clears it, and so
// does the bound below. That is a deliberate retention policy rather than a
// missing feature, and it is stated on the page so nobody reads an empty list
// as "nobody came".
//
// # WHAT IS BOUNDED, AND WHY BOTH
//
// Per CV, so one popular CV cannot push every other CV's history out. In
// total, so a service with ten thousand CVs cannot be made to hold ten
// thousand rings — the eviction is by last activity, which is also the order
// in which this stops being interesting.
package access

import (
	"sort"
	"sync"
	"time"
)

// What kind of fetch an entry records.
//
// The three are kept apart because they answer different questions: a viewer
// open is somebody looking, a PDF is somebody KEEPING it, and an edit-link
// visit is the author themselves and not a reader at all. Summed together they
// would make an author's own afternoon of typing look like an audience.
const (
	Viewer = "viewer"
	Page   = "page"
	PDF    = "pdf"
	Editor = "editor"
)

// Log holds recent fetches, per CV.
type Log struct {
	// PerCV is how many entries one CV keeps. A ring, so the newest always
	// win: an old visit matters much less than the fact that one happened an
	// hour ago.
	PerCV int
	// MaxCVs is how many CVs are tracked at once, evicted by last activity.
	MaxCVs int
	// Retain is how long an entry is kept. Zero means only the ring bounds it.
	Retain time.Duration

	mu   sync.Mutex
	cvs  map[string]*ring
	now  func() time.Time
	seen int64
}

// Entry is one fetch.
type Entry struct {
	At   time.Time
	IP   string
	What string
	// Agent is the browser's own description of itself, shortened. Kept
	// because it is what distinguishes a person opening a CV from a link
	// preview fetched by a chat application — which is otherwise an
	// indistinguishable visit from an address nobody recognises, moments
	// after the link was sent.
	Agent string
	// Edit is whether this came through the edit link, which means the author.
	Edit bool
}

type ring struct {
	entries []Entry
	at      int
	last    time.Time
	// total counts everything ever recorded, including what the ring has since
	// dropped — so the page can say "34 visits, last 20 shown" rather than
	// quietly under-reporting.
	total int64
}

// New builds a log with the bounds this service uses.
func New() *Log {
	return &Log{
		PerCV:  200,
		MaxCVs: 500,
		Retain: 30 * 24 * time.Hour,
		cvs:    map[string]*ring{},
		now:    time.Now,
	}
}

// Record notes one fetch. It never blocks on anything but its own mutex.
func (l *Log) Record(slug, ip, what, agent string, edit bool) {
	if l == nil || slug == "" {
		return
	}
	at := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	r := l.cvs[slug]
	if r == nil {
		l.evict()
		r = &ring{entries: make([]Entry, 0, 16)}
		l.cvs[slug] = r
	}
	entry := Entry{At: at, IP: ip, What: what, Agent: shorten(agent), Edit: edit}
	if len(r.entries) < l.PerCV {
		r.entries = append(r.entries, entry)
	} else {
		r.entries[r.at] = entry
		r.at = (r.at + 1) % l.PerCV
	}
	r.last = at
	r.total++
	l.seen++
}

// evict drops the least recently active CV when the table is full. The caller
// holds the lock.
func (l *Log) evict() {
	if len(l.cvs) < l.MaxCVs {
		return
	}
	oldest, at := "", time.Time{}
	for slug, r := range l.cvs {
		if oldest == "" || r.last.Before(at) {
			oldest, at = slug, r.last
		}
	}
	delete(l.cvs, oldest)
}

// Forget drops everything known about a CV.
//
// Called when one is deleted. A CV that no longer exists leaving its readers'
// addresses behind in memory is the one way this could outlive its own subject.
func (l *Log) Forget(slug string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.cvs, slug)
}

// Visitor is one address, and everything it did.
//
// GROUPED BY ADDRESS rather than listed by time, because that is the question
// being asked. A raw stream of forty entries is forty lines to add up by eye;
// six addresses with counts is an answer — and the one that matters, "is this
// one person or six", is the grouping itself.
type Visitor struct {
	IP     string    `json:"ip"`
	Hits   int       `json:"hits"`
	First  time.Time `json:"first"`
	Last   time.Time `json:"last"`
	Agent  string    `json:"agent,omitempty"`
	Viewer int       `json:"viewer"`
	Pages  int       `json:"pages"`
	PDFs   int       `json:"pdfs"`
	// Author is whether this address ever used the edit link. It is almost
	// always the owner's own address, and saying so stops the owner reading
	// their own week of editing as an audience.
	Author bool `json:"author"`
	// CVs is which CVs this address fetched, most recently touched first.
	//
	// The question a whole-service view asks that a per-CV one cannot: an
	// address that has opened ONE CV is a reader, and an address that has
	// opened nine is either the owner or somebody walking the tokens.
	CVs []string `json:"cvs,omitempty"`
}

// Summary is what the administration page shows for one CV.
type Summary struct {
	Visitors []Visitor `json:"visitors"`
	// Total is everything recorded, including entries the ring has dropped.
	Total int64 `json:"total"`
	// Kept is how many entries the summary was actually built from.
	Kept int `json:"kept"`
	// Since is the oldest entry still held, which is what "kept" means in time.
	Since time.Time `json:"since,omitempty"`
}

// For summarises one CV's recent fetches, newest address first.
func (l *Log) For(slug string) Summary {
	if l == nil {
		return Summary{Visitors: []Visitor{}}
	}
	l.mu.Lock()
	r := l.cvs[slug]
	if r == nil {
		l.mu.Unlock()
		return Summary{Visitors: []Visitor{}}
	}
	held := map[string][]Entry{slug: append([]Entry(nil), r.entries...)}
	total := r.total
	l.mu.Unlock()

	return l.summarise(held, total)
}

// Overall summarises every CV at once, for the whole-service view.
//
// The same grouping by address, across the lot. That is a different question
// from the per-CV one and the reason this exists: "who is reading my CVs"
// cannot be answered by opening thirty panels one at a time, and the answer
// that matters — one address appearing against nine different CVs — is
// invisible from inside any single one of them.
func (l *Log) Overall() Summary {
	if l == nil {
		return Summary{Visitors: []Visitor{}}
	}
	l.mu.Lock()
	held := make(map[string][]Entry, len(l.cvs))
	var total int64
	for slug, r := range l.cvs {
		held[slug] = append([]Entry(nil), r.entries...)
		total += r.total
	}
	l.mu.Unlock()

	return l.summarise(held, total)
}

// summarise groups entries by address, dropping whatever has aged out.
func (l *Log) summarise(held map[string][]Entry, total int64) Summary {
	cutoff := time.Time{}
	if l.Retain > 0 {
		cutoff = l.now().Add(-l.Retain)
	}

	byIP := map[string]*Visitor{}
	// Which CVs each address touched, and when it last touched each — so the
	// list can be ordered by recency rather than alphabetically, which is what
	// makes the first name in it the useful one.
	seen := map[string]map[string]time.Time{}
	kept := 0
	var since time.Time
	for slug, entries := range held {
		for _, e := range entries {
			if !cutoff.IsZero() && e.At.Before(cutoff) {
				continue
			}
			kept++
			if since.IsZero() || e.At.Before(since) {
				since = e.At
			}
			v := byIP[e.IP]
			if v == nil {
				v = &Visitor{IP: e.IP, First: e.At}
				byIP[e.IP] = v
				seen[e.IP] = map[string]time.Time{}
			}
			if at := seen[e.IP][slug]; e.At.After(at) {
				seen[e.IP][slug] = e.At
			}
			v.Hits++
			if e.At.After(v.Last) {
				v.Last = e.At
				// The most recent description wins: a browser that has been
				// updated is still the same reader.
				if e.Agent != "" {
					v.Agent = e.Agent
				}
			}
			if e.At.Before(v.First) {
				v.First = e.At
			}
			switch e.What {
			case Viewer:
				v.Viewer++
			case Page:
				v.Pages++
			case PDF:
				v.PDFs++
			}
			if e.Edit {
				v.Author = true
			}
		}
	}

	out := make([]Visitor, 0, len(byIP))
	for ip, v := range byIP {
		v.CVs = recentFirst(seen[ip])
		out = append(out, *v)
	}
	// Most recent first, and by address on a tie so the order is stable. A
	// list whose order depends on map iteration is a list that reshuffles
	// itself on every refresh of a page that refreshes itself.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].IP < out[j].IP
	})
	return Summary{Visitors: out, Total: total, Kept: kept, Since: since}
}

// recentFirst orders the CVs an address touched by when it last touched them.
func recentFirst(at map[string]time.Time) []string {
	out := make([]string, 0, len(at))
	for slug := range at {
		out = append(out, slug)
	}
	sort.Slice(out, func(i, j int) bool {
		if !at[out[i]].Equal(at[out[j]]) {
			return at[out[i]].After(at[out[j]])
		}
		return out[i] < out[j]
	})
	return out
}

// Counts is how many visits each CV has had, for the inventory.
//
// One pass under one lock rather than a call per CV: the inventory draws every
// CV it lists, and a lock taken per row is a lock taken thirty times to answer
// one question.
func (l *Log) Counts() map[string]int64 {
	out := map[string]int64{}
	if l == nil {
		return out
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for slug, r := range l.cvs {
		out[slug] = r.total
	}
	return out
}

// shorten turns a user agent into the few words worth showing.
//
// A modern one is about 120 characters of compatibility archaeology — every
// browser claims to be Mozilla, Safari and Gecko — and none of it fits a table
// or means anything to a reader. What is worth knowing is whether this was a
// person's browser or a robot fetching a preview, so the name is taken and the
// rest dropped.
func shorten(agent string) string {
	if agent == "" {
		return ""
	}
	// The distinctive name, in the order that identifies rather than the order
	// they appear: every Chrome claims Safari, and every Edge claims Chrome.
	for _, name := range []string{
		"Edg", "OPR", "Firefox", "Chrome", "Safari",
	} {
		if containsWord(agent, name) {
			switch name {
			case "Edg":
				return "Edge"
			case "OPR":
				return "Opera"
			}
			return name
		}
	}
	// Not a browser: a crawler, a link preview, a command-line client. The
	// whole point of showing this is to tell one of those from a person, so
	// its own name is kept rather than reduced to "other".
	if len(agent) > 40 {
		return agent[:40] + "\u2026"
	}
	return agent
}

func containsWord(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] != needle {
			continue
		}
		// Followed by a version separator or nothing, so "Chrome" does not
		// match inside a longer token.
		if i+len(needle) == len(haystack) {
			return true
		}
		switch haystack[i+len(needle)] {
		case '/', ' ', ')', ';', ',':
			return true
		}
	}
	return false
}
