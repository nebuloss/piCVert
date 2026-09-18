// Package metrics is what the admin page can say about a running service.
//
// # WHAT IS WORTH COUNTING
//
// Only the things somebody would act on. A page of numbers nobody reads is
// worse than no page, because it looks like observability while telling you
// nothing — so each of these answers a question an administrator actually asks:
//
//	is it up, and since when            uptime, version
//	is it busy                          requests, and how long a render takes
//	is the cache doing anything         hits against misses
//	is anybody using it right now       live editing leases
//	is somebody attacking it            addresses currently throttled
//	will it run out of room             what the CVs weigh
//	when was the last backup            the one that matters at 3am
//
// # WHY NOT PROMETHEUS
//
// A scrape endpoint needs something to scrape it, and this is a service that
// holds a handful of CVs on one machine. The numbers are on the page the
// administrator already has open. If it ever runs somewhere with a monitoring
// stack, the same counters render as text in whatever format that stack wants
// — which is why they live here rather than inside the handlers.
package metrics

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Metrics is the running count.
//
// Every field is touched from many goroutines at once, so all of them are
// atomic or behind the mutex. A counter that is merely "probably about right"
// is fine for a dashboard and not fine for the race detector, which runs over
// this in CI.
type Metrics struct {
	Started time.Time
	Version string

	requests atomic.Int64
	renders  atomic.Int64
	pdfs     atomic.Int64
	saves    atomic.Int64
	errors   atomic.Int64
	cacheHit atomic.Int64
	cacheMis atomic.Int64
	conflict atomic.Int64
	refused  atomic.Int64

	mu sync.Mutex
	// renderTimes is a ring of recent layout durations.
	//
	// A ring rather than a total, because an average over the life of the
	// process hides everything: a service that was fast for a week and has been
	// slow since lunch has a fine average and a real problem. A few hundred
	// recent samples say what it is doing NOW.
	renderTimes []time.Duration
	renderAt    int

	lastBackup time.Time
}

const samples = 256

func New(version string) *Metrics {
	return &Metrics{
		Started:     time.Now(),
		Version:     version,
		renderTimes: make([]time.Duration, 0, samples),
	}
}

func (m *Metrics) Request()   { m.requests.Add(1) }
func (m *Metrics) PDF()       { m.pdfs.Add(1) }
func (m *Metrics) Save()      { m.saves.Add(1) }
func (m *Metrics) Error()     { m.errors.Add(1) }
func (m *Metrics) Conflict()  { m.conflict.Add(1) }
func (m *Metrics) Refused()   { m.refused.Add(1) }
func (m *Metrics) CacheHit()  { m.cacheHit.Add(1) }
func (m *Metrics) CacheMiss() { m.cacheMis.Add(1) }

// Render records a layout and how long it took.
func (m *Metrics) Render(took time.Duration) {
	m.renders.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.renderTimes) < samples {
		m.renderTimes = append(m.renderTimes, took)
		return
	}
	m.renderTimes[m.renderAt] = took
	m.renderAt = (m.renderAt + 1) % samples
}

// Backup records that one was taken.
//
// # WHY THIS IS SET FROM OUTSIDE AND NOT COUNTED IN HERE
//
// Backups are taken by a SEPARATE PROCESS — a systemd timer, or busybox cron
// running /etc/periodic/daily. The serving process never sees one happen, so a
// counter it increments itself can only ever say "none", which is exactly what
// the administration page reported: a standing warning that no backup had been
// taken, on a machine backing itself up every night.
//
// The honest answer is on disk, in the backups directory, and the server reads
// it there. This remains for a caller that genuinely knows.
func (m *Metrics) Backup(at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if at.After(m.lastBackup) {
		m.lastBackup = at
	}
}

// Snapshot is the numbers at one moment, for the admin page.
type Snapshot struct {
	Version   string `json:"version"`
	UptimeSec int64  `json:"uptimeSec"`

	Requests int64 `json:"requests"`
	Renders  int64 `json:"renders"`
	PDFs     int64 `json:"pdfs"`
	Saves    int64 `json:"saves"`
	Errors   int64 `json:"errors"`

	// Conflicts and Refused are the two that mean somebody had a bad time:
	// a save rejected because another window got there first, and a link
	// refused because an address was guessing.
	Conflicts int64 `json:"conflicts"`
	Refused   int64 `json:"refused"`

	CacheHits   int64   `json:"cacheHits"`
	CacheMisses int64   `json:"cacheMisses"`
	CacheRatio  float64 `json:"cacheRatio"`

	RenderMedianMs float64 `json:"renderMedianMs"`
	RenderSlowMs   float64 `json:"renderSlowMs"`

	LastBackup string `json:"lastBackup,omitempty"`
}

func (m *Metrics) Snapshot() Snapshot {
	hits, misses := m.cacheHit.Load(), m.cacheMis.Load()
	ratio := 0.0
	if hits+misses > 0 {
		ratio = float64(hits) / float64(hits+misses)
	}

	m.mu.Lock()
	times := append([]time.Duration(nil), m.renderTimes...)
	backup := m.lastBackup
	m.mu.Unlock()

	median, slow := percentiles(times)
	s := Snapshot{
		Version:   m.Version,
		UptimeSec: int64(time.Since(m.Started).Seconds()),
		Requests:  m.requests.Load(),
		Renders:   m.renders.Load(),
		PDFs:      m.pdfs.Load(),
		Saves:     m.saves.Load(),
		Errors:    m.errors.Load(),
		Conflicts: m.conflict.Load(),
		Refused:   m.refused.Load(),

		CacheHits:   hits,
		CacheMisses: misses,
		CacheRatio:  ratio,

		RenderMedianMs: median,
		RenderSlowMs:   slow,
	}
	if !backup.IsZero() {
		s.LastBackup = backup.UTC().Format(time.RFC3339)
	}
	return s
}

// percentiles is the middle and the slow end, in milliseconds.
//
// The 95th rather than the maximum: a maximum is one unlucky request and tells
// you nothing repeatable, where the 95th is what a person actually meets when
// the service is having a bad time.
func percentiles(times []time.Duration) (median, slow float64) {
	if len(times) == 0 {
		return 0, 0
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	at := func(q float64) float64 {
		i := int(float64(len(times)-1) * q)
		return float64(times[i].Microseconds()) / 1000
	}
	return at(0.5), at(0.95)
}
