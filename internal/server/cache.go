package server

import (
	"container/list"
	"sync"
	"time"

	"picvert/internal/engine"
)

// The rendered-page cache.
//
// # WHY THERE IS ONE
//
// A layout costs a couple of hundred milliseconds and nothing about a document
// changes between two requests that did not go through the store. The viewer
// reloads, the editor asks for the PDF, a recruiter opens the link twice: all
// the same answer.
//
// # WHY IT IS BOUNDED IN BYTES
//
// It was bounded at 256 ENTRIES, which says nothing about memory. A cached CV
// is about 400 kB of page and 300 kB of PDF, so a full cache was some 180 MB
// against a service the unit caps at 256 MB — the bound was the wrong unit and
// the ceiling it implied was most of the process.
//
// Bytes, then, and evicted least-recently-used rather than emptied wholesale.
// Emptying meant one profile too many cost every other profile its rendering,
// so a busy service periodically re-laid-out everything at once.

// cacheMaxBytes is what the cache may hold.
//
// 48 MB against the unit's 256 MB: enough for sixty-odd rendered CVs, and small
// enough that the rest of the process has room to render the next one. A cache
// that leaves no space to work in is a cache that trades an outage for a hit
// rate.
const cacheMaxBytes = 48 << 20

// cacheMaxEntries is a second bound, for the case bytes cannot catch: a great
// many tiny CVs. Each entry costs a layout tree that is not counted below,
// because walking it to weigh it would cost more than the tree.
const cacheMaxEntries = 128

// cached is one rendered profile.
type cached struct {
	stamp time.Time
	html  string
	pdf   []byte
	page  *engine.Page

	key string
	// at is this entry's place in the recency list, so touching it is O(1).
	at *list.Element
}

// bytes is roughly what this entry holds. The layout tree is not counted:
// measuring it means walking it, which costs more than it saves, and it is
// small beside the two renderings.
func (c *cached) bytes() int {
	return len(c.html) + len(c.pdf)
}

// pageCache is a least-recently-used cache with a size in bytes.
//
// Its own type rather than three fields on the Server, because the invariant —
// total equals the sum of the entries, and the list holds exactly the keys of
// the map — is the sort that survives being stated in one place and stops
// being true when it is maintained in four handlers.
type pageCache struct {
	mu      sync.Mutex
	entries map[string]*cached
	// recent is most-recently-used at the front.
	recent *list.List
	total  int
}

func newPageCache() *pageCache {
	return &pageCache{entries: map[string]*cached{}, recent: list.New()}
}

// get returns the entry for a key if it is still current.
//
// Currency is the document's modification time, not a duration: a cached page
// is wrong exactly when the document has changed and at no other moment, so an
// expiry would be either too eager or too slow and never right.
func (c *pageCache) get(key string, stamp time.Time) (*cached, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if !entry.stamp.Equal(stamp) {
		c.removeLocked(entry)
		return nil, false
	}
	c.recent.MoveToFront(entry.at)
	return entry, true
}

// put stores an entry, evicting until the cache fits.
func (c *pageCache) put(key string, entry *cached) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.entries[key]; ok {
		c.removeLocked(old)
	}
	entry.key = key
	entry.at = c.recent.PushFront(entry)
	c.entries[key] = entry
	c.total += entry.bytes()
	c.evictLocked()
}

// grew tells the cache an entry got bigger — the PDF being drawn after the
// page, which is the only way that happens.
func (c *pageCache) grew(entry *cached, by int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[entry.key]; !ok {
		// Evicted while its PDF was being drawn. The PDF is still returned to
		// whoever asked; it simply is not remembered.
		return
	}
	c.total += by
	c.evictLocked()
}

func (c *pageCache) evictLocked() {
	for (c.total > cacheMaxBytes || len(c.entries) > cacheMaxEntries) && c.recent.Len() > 1 {
		// Never the entry just added: evicting it would mean a document larger
		// than the whole cache is re-rendered on every single request, which is
		// the worst behaviour available rather than the safest.
		oldest := c.recent.Back()
		if oldest == nil {
			return
		}
		c.removeLocked(oldest.Value.(*cached))
	}
}

func (c *pageCache) removeLocked(entry *cached) {
	delete(c.entries, entry.key)
	c.recent.Remove(entry.at)
	c.total -= entry.bytes()
	if c.total < 0 {
		c.total = 0
	}
}

// forget drops every entry whose key begins with a prefix — one profile's
// languages, when that profile is written to or deleted.
func (c *pageCache) forget(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			c.removeLocked(entry)
		}
	}
}

// size and weight are for the tests and the health report.
func (c *pageCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *pageCache) weight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

// cacheSize is what the tests and the health report ask for.
func (s *Server) cacheSize() int { return s.pages.size() }

// cacheBytes is how much the cache is holding.
func (s *Server) cacheBytes() int { return s.pages.weight() }
