package lease

import (
	"testing"
	"time"
)

// A fake clock, so the two timeouts can be tested without waiting for them.
// Waiting 45 seconds in a test is a test nobody runs.
type clock struct{ at time.Time }

func (c *clock) now() time.Time       { return c.at }
func (c *clock) tick(d time.Duration) { c.at = c.at.Add(d) }

func registry() (*Registry, *clock) {
	c := &clock{at: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	r := New()
	r.Now = c.now
	return r, c
}

func TestOnePersonAtATime(t *testing.T) {
	r, _ := registry()
	first, second := NewHolder(), NewHolder()

	if got := r.Acquire("cv", first, "Jean"); !got.OK {
		t.Fatal("the first asker was refused")
	}
	got := r.Acquire("cv", second, "Someone else")
	if got.OK {
		t.Fatal("two people were granted the lease at once")
	}
	if got.HeldBy != "Jean" {
		t.Errorf("the waiting person was told %q holds it", got.HeldBy)
	}
	if !r.Holds("cv", first) || r.Holds("cv", second) {
		t.Error("Holds disagrees with Acquire")
	}
}

// Two languages of one CV are two documents, and must not lock each other.
func TestDifferentDocumentsDoNotBlockEachOther(t *testing.T) {
	r, _ := registry()
	a, b := NewHolder(), NewHolder()
	if !r.Acquire("cv|fr", a, "").OK || !r.Acquire("cv|en", b, "").OK {
		t.Fatal("editing the French CV blocked editing the English one")
	}
}

// A holder asking again extends rather than being refused by itself.
func TestTheHolderCanRenew(t *testing.T) {
	r, c := registry()
	me := NewHolder()
	first := r.Acquire("cv", me, "Jean")

	c.tick(10 * time.Second)
	again := r.Renew("cv", me)
	if !again.OK {
		t.Fatal("the holder was refused its own lease")
	}
	if !again.Until.After(first.Until) {
		t.Error("renewing did not extend the lease")
	}
}

// A heartbeat cannot bring back a lease that has lapsed. Somebody who has been
// away has to ask again, which is what makes the inactivity timeout mean
// anything.
func TestAHeartbeatCannotResurrectALapsedLease(t *testing.T) {
	r, c := registry()
	me := NewHolder()
	r.Acquire("cv", me, "Jean")

	c.tick(DefaultTTL * 2)
	if r.Renew("cv", me).OK {
		t.Fatal("a heartbeat recreated a lease that had expired")
	}
	if !r.Acquire("cv", me, "Jean").OK {
		t.Fatal("asking again for a free lease was refused")
	}
}

// A holder that stops talking loses the lease — the closed tab, the crash, the
// train going into a tunnel.
func TestASilentHolderLosesIt(t *testing.T) {
	r, c := registry()
	gone, waiting := NewHolder(), NewHolder()
	r.Acquire("cv", gone, "Jean")

	c.tick(DefaultTTL / 2)
	if r.Acquire("cv", waiting, "").OK {
		t.Fatal("the lease was given away while its holder was still talking")
	}

	c.tick(DefaultTTL)
	if !r.Acquire("cv", waiting, "Someone else").OK {
		t.Fatal("a silent holder kept the lease past its expiry")
	}
	if r.Holds("cv", gone) {
		t.Error("the old holder still holds a lease it lost")
	}
}

// A tab left open heartbeats perfectly and changes nothing. It must still give
// the lease up — this is the case the second timeout exists for, and the one a
// TTL alone cannot catch.
func TestAForgottenTabGivesUpEventually(t *testing.T) {
	r, c := registry()
	forgotten, waiting := NewHolder(), NewHolder()
	r.Acquire("cv", forgotten, "Jean")

	// Heartbeating faithfully, and touching nothing. An hour's worth, which is
	// four times the inactivity timeout — so a failure here is the timeout not
	// working rather than the test being impatient.
	beats := int(time.Hour / DefaultHeartbeat)
	for i := 0; i < beats; i++ {
		c.tick(DefaultHeartbeat)
		if !r.Renew("cv", forgotten).OK {
			break
		}
	}

	if r.Holds("cv", forgotten) {
		t.Fatal("a tab left open for an hour still holds the lease")
	}
	if !r.Acquire("cv", waiting, "Someone else").OK {
		t.Fatal("the lease was never freed")
	}
}

// Working on the CV holds the inactivity timeout off, however long it takes.
func TestSomebodyStillWorkingKeepsIt(t *testing.T) {
	r, c := registry()
	working, waiting := NewHolder(), NewHolder()
	r.Acquire("cv", working, "Jean")

	// Half an hour of real editing: heartbeats, and changes.
	for i := 0; i < 120; i++ {
		c.tick(DefaultHeartbeat)
		if !r.Renew("cv", working).OK {
			t.Fatalf("lost the lease after %v of actual work",
				time.Duration(i)*DefaultHeartbeat)
		}
		r.Touch("cv", working)
	}
	if r.Acquire("cv", waiting, "").OK {
		t.Fatal("somebody editing all along lost their lease to the timeout")
	}
}

// Releasing frees it at once, rather than at the TTL.
func TestReleasingIsImmediate(t *testing.T) {
	r, _ := registry()
	leaving, next := NewHolder(), NewHolder()
	r.Acquire("cv", leaving, "Jean")
	r.Release("cv", leaving)

	if !r.Acquire("cv", next, "Someone else").OK {
		t.Fatal("a released lease was not free")
	}
}

// And nobody else can release it, which would otherwise be a way to take a
// lease away from whoever holds it.
func TestOnlyTheHolderCanRelease(t *testing.T) {
	r, _ := registry()
	holder, other := NewHolder(), NewHolder()
	r.Acquire("cv", holder, "Jean")
	r.Release("cv", other)

	if !r.Holds("cv", holder) {
		t.Fatal("somebody else released a lease they did not hold")
	}
}

// Touching a lease you do not hold does nothing, for the same reason.
func TestOnlyTheHolderCanTouch(t *testing.T) {
	r, c := registry()
	holder, other := NewHolder(), NewHolder()
	r.Acquire("cv", holder, "Jean")

	c.tick(DefaultTTL * 2)
	r.Touch("cv", other)
	if r.Holds("cv", other) {
		t.Fatal("touching granted a lease")
	}
}
