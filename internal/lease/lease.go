// Package lease grants one editor at a time the right to change a CV.
//
// # WHY A LEASE AND NOT A MERGE
//
// Two people with the same link could both type, and the loser found out only
// when their save was refused — having written a paragraph that was then thrown
// away. Refusing the save protects the work already stored; it does nothing for
// the work being done.
//
// A lease moves the answer to the start. The second person is told the CV is
// being edited before they type anything, and waits.
//
// # WHY IT IS A LEASE AND NOT A LOCK
//
// A lock is held until it is released, and the holder of this one is a browser
// tab — which can be closed, crash, lose its network or be left open on a
// laptop that goes in a bag. Every one of those would hold a lock for ever, and
// the only cure would be an administrator.
//
// So it EXPIRES, and is kept alive by the holder saying it is still there.
// Silence for long enough and it is gone; the cost of a wrong answer is bounded
// by the timeout rather than by somebody noticing.
//
// # TWO TIMEOUTS, FOR TWO DIFFERENT FAILURES
//
//	TTL          the holder stopped talking: closed, crashed, disconnected.
//	             Short, because the next person is waiting and nothing is
//	             happening.
//
//	Inactivity   the holder is still there and has not touched the CV in a long
//	             time: a tab left open on a second monitor. It heartbeats
//	             perfectly and would hold the lease for ever.
//
// One timeout cannot do both: short enough to free a closed tab quickly is far
// too short for somebody thinking about a sentence.
//
// # WHY IN MEMORY
//
// A lease is about who is editing RIGHT NOW. Written to disk it would survive a
// restart, and a restart is exactly the moment every holder has gone — so the
// first thing a fresh process would do is honour leases belonging to nobody.
// Losing them on restart is the correct behaviour, not a limitation.
package lease

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// Default timings.
//
// TTL is three heartbeats: two can be lost to a bad connection before somebody
// is treated as gone. Inactivity is long enough to read a CV through and think
// about it, and short enough that a forgotten tab frees up within a coffee
// break.
const (
	DefaultTTL        = 45 * time.Second
	DefaultHeartbeat  = 15 * time.Second
	DefaultInactivity = 15 * time.Minute
)

// Holder identifies one editing session.
//
// NOT the link token. The whole situation this exists for is two people holding
// the SAME link, so a token identifies the CV and says nothing about who is at
// the keyboard. Minted per session, by the server, and kept by that one tab.
type Holder string

// Lease is one grant.
type Lease struct {
	Holder Holder
	// Name is what the waiting person is told, when there is anything to tell:
	// the name on the CV is the closest this service has to an identity, since
	// there are no accounts.
	Name string
	// Expires when the heartbeat stops.
	Expires time.Time
	// Idle is when the CV was last actually CHANGED, as opposed to when the
	// holder last said hello. The distinction is the whole of the second
	// timeout.
	Idle time.Time
}

// Registry is every lease currently granted.
type Registry struct {
	mu     sync.Mutex
	leases map[string]*Lease

	// Now is the clock, so the timeouts can be tested without waiting for them.
	Now func() time.Time

	TTL        time.Duration
	Inactivity time.Duration
}

func New() *Registry {
	return &Registry{
		leases:     map[string]*Lease{},
		Now:        time.Now,
		TTL:        DefaultTTL,
		Inactivity: DefaultInactivity,
	}
}

// NewHolder mints an identity for one editing session.
func NewHolder() Holder {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		// A holder that is not unique is a holder that can steal somebody
		// else's lease. Nothing here can recover from a broken entropy source,
		// and carrying on would hand out a guessable identity.
		panic("lease: no randomness available: " + err.Error())
	}
	return Holder(base64.RawURLEncoding.EncodeToString(buf))
}

// Grant is the answer to asking for a lease.
type Grant struct {
	// OK is whether the asker may edit.
	OK bool
	// Holder is the asker's identity, echoed so a fresh session learns its own.
	Holder Holder
	// Until is when this lease lapses if the holder goes quiet.
	Until time.Time
	// HeldBy names who has it, when the answer is no. Empty when it is yes.
	HeldBy string
	// Free is when the current holder's lease lapses — what a waiting person is
	// counting down to. Only meaningful when OK is false.
	Free time.Time
}

// Acquire asks to start editing.
//
// A DELIBERATE request, made when an editor opens — which is why it may take a
// lease that has lapsed, including one this same holder lost. Renew is the
// heartbeat, and it may not.
//
// That separation is the whole of the inactivity timeout. Made one method, a
// forgotten tab heartbeating every fifteen seconds would let its lease lapse
// and immediately take a fresh one, with the idle clock reset — so the timeout
// existed, fired, and changed nothing. It took a test to notice, because from
// outside the lease simply never expired.
func (r *Registry) Acquire(key string, holder Holder, name string) Grant {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.Now()
	r.sweepLocked(now)

	if current, held := r.leases[key]; held {
		if current.Holder != holder {
			return Grant{
				OK: false, Holder: holder,
				HeldBy: current.Name, Free: current.Expires,
			}
		}
		// Already ours: extend, and treat asking as working. Somebody who has
		// just opened the editor again is somebody who is there.
		current.Expires = now.Add(r.TTL)
		current.Idle = now
		if name != "" {
			current.Name = name
		}
		return Grant{OK: true, Holder: holder, Until: current.Expires}
	}

	current := &Lease{Holder: holder, Name: name, Idle: now, Expires: now.Add(r.TTL)}
	r.leases[key] = current
	return Grant{OK: true, Holder: holder, Until: current.Expires}
}

// Renew is the heartbeat: "I am still here".
//
// It extends a lease and CANNOT create one. A holder whose lease lapsed —
// through silence or through doing nothing for long enough — is told so, and
// has to ask for it again like anybody else. That is what stops a tab left open
// on a second monitor holding a CV for ever while heartbeating perfectly.
func (r *Registry) Renew(key string, holder Holder) Grant {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.Now()
	r.sweepLocked(now)

	current, held := r.leases[key]
	if !held {
		return Grant{OK: false, Holder: holder}
	}
	if current.Holder != holder {
		return Grant{
			OK: false, Holder: holder,
			HeldBy: current.Name, Free: current.Expires,
		}
	}
	// Expires moves; Idle does NOT. Saying hello is not working.
	current.Expires = now.Add(r.TTL)
	return Grant{OK: true, Holder: holder, Until: current.Expires}
}

// Touch records that the CV was actually changed, which is what holds off the
// inactivity timeout.
//
// Separate from Acquire deliberately: a heartbeat says "I am still here" and a
// touch says "I am still working". A tab left open does the first for ever and
// the second never, which is precisely the case the two timeouts tell apart.
func (r *Registry) Touch(key string, holder Holder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, held := r.leases[key]; held && current.Holder == holder {
		now := r.Now()
		current.Idle = now
		current.Expires = now.Add(r.TTL)
	}
}

// Holds reports whether this holder currently has the lease.
//
// The question every write asks. Without it the lease would be advisory — a
// courtesy the interface observes and anything else ignores — and the second
// editor's changes would still land.
func (r *Registry) Holds(key string, holder Holder) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked(r.Now())
	current, held := r.leases[key]
	return held && current.Holder == holder
}

// Current is the lease on a key, if there is a live one.
func (r *Registry) Current(key string) (Lease, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked(r.Now())
	current, held := r.leases[key]
	if !held {
		return Lease{}, false
	}
	return *current, true
}

// Release gives the lease up, for a tab that is closing.
//
// The fast path, and only that: everything is correct without it, just slower
// for whoever is waiting. So it is never required and its failure is never
// reported — a browser refusing to send one last request as a tab closes is
// ordinary, and the TTL is the answer to it.
func (r *Registry) Release(key string, holder Holder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, held := r.leases[key]; held && current.Holder == holder {
		delete(r.leases, key)
	}
}

// sweepLocked drops leases that have lapsed.
//
// On every access rather than on a timer: a service nobody is using has no
// leases worth expiring, and a timer is a goroutine that can stop without
// anybody noticing. The cost is one pass over a map with as many entries as
// there are people editing, which is a handful.
func (r *Registry) sweepLocked(now time.Time) {
	for key, current := range r.leases {
		if now.After(current.Expires) || now.Sub(current.Idle) > r.Inactivity {
			delete(r.leases, key)
		}
	}
}

// Count is how many leases are live, for the health report and the tests.
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked(r.Now())
	return len(r.leases)
}
