package pool

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The queue's order is its own: tickets are granted in the order they
// queued, a free slot never jumps a waiter, and a waiter that gives
// up keeps nothing — neither its place nor a slot granted in the same
// instant.
func TestFIFOQueue(t *testing.T) {
	ctx := context.Background()
	f := newFIFO(1)
	held := f.enqueue()
	if err := held.wait(ctx); err != nil {
		t.Fatalf("a free slot: %v", err)
	}
	a, b, c := f.enqueue(), f.enqueue(), f.enqueue()
	granted := func(tk *ticket) bool {
		select {
		case <-tk.ready:
			return true
		default:
			return false
		}
	}
	if granted(a) || granted(b) || granted(c) {
		t.Fatal("a waiter was granted a held slot")
	}
	// b gives up while it waits: it leaves the queue.
	dead, cancel := context.WithCancel(ctx)
	cancel()
	if err := b.wait(dead); !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled wait: %v", err)
	}
	f.release()
	if !granted(a) || granted(c) {
		t.Fatalf("after one release: a=%v c=%v, want a alone", granted(a), granted(c))
	}
	// a was granted and its context ended in the same instant: the
	// slot goes on to c, not to waste.
	if err := a.wait(dead); !errors.Is(err, context.Canceled) {
		t.Fatalf("a granted-and-canceled wait: %v", err)
	}
	if !granted(c) {
		t.Fatal("the slot a canceled waiter was granted did not pass to the next")
	}
	if err := c.wait(ctx); err != nil {
		t.Fatal(err)
	}
	// A free slot with nobody waiting is taken at once; with the slot
	// free again, the count is back where it started.
	f.release()
	if f.free != 1 || f.waiters.Len() != 0 {
		t.Fatalf("after everything: free=%d waiters=%d", f.free, f.waiters.Len())
	}
	// A queued newcomer never overtakes: with a waiter present, a
	// release goes to the waiter even though enqueue could see a slot.
	x := f.enqueue() // takes the free slot
	y := f.enqueue() // waits
	f.release()      // x's slot → y
	z := f.enqueue()
	if !granted(x) || !granted(y) || granted(z) {
		t.Fatalf("x=%v y=%v z=%v", granted(x), granted(y), granted(z))
	}
}

// A run's slot under fan-out: given up while any of the run's sync
// delegations waits, taken back only when the last has returned — on
// the run's context, not the returning call's.
func TestSlotHandOff(t *testing.T) {
	ctx := context.Background()
	f := newFIFO(1)
	if err := f.enqueue().wait(ctx); err != nil {
		t.Fatal(err)
	}
	s := &slot{f: f, ctx: ctx, held: true}
	s.yield() // first delegation waits: the slot is free
	s.yield() // a sibling waits too
	if f.free != 1 {
		t.Fatalf("free = %d while the run waits, want its slot back in the pool", f.free)
	}
	if err := s.regain(); err != nil { // one returned, one still waits
		t.Fatal(err)
	}
	if f.free != 1 || s.held {
		t.Fatalf("the run took its slot back with a sibling still waiting (free=%d held=%v): with one slot that is the deadlock", f.free, s.held)
	}
	if err := s.regain(); err != nil { // the last one returned
		t.Fatal(err)
	}
	if f.free != 0 || !s.held {
		t.Fatalf("the run works without a slot (free=%d held=%v)", f.free, s.held)
	}
	s.finish()
	s.finish() // idempotent: one slot back, not two
	if f.free != 1 {
		t.Fatalf("free = %d after the run", f.free)
	}
	// A delegation returning after its run ended takes nothing.
	s.yield()
	if err := s.regain(); err != nil || f.free != 1 {
		t.Fatalf("a late return: err=%v free=%d", err, f.free)
	}
	// A nil slot — a run that is no pool child — is inert.
	var none *slot
	none.yield()
	if err := none.regain(); err != nil {
		t.Fatal(err)
	}
	// Taking the slot back waits on the run's context: a canceled
	// run stops queueing, and holds nothing.
	runCtx, cancel := context.WithCancel(ctx)
	busy := newFIFO(1)
	if err := busy.enqueue().wait(ctx); err != nil {
		t.Fatal(err)
	}
	r := &slot{f: busy, ctx: runCtx, held: true}
	r.yield()
	other := busy.enqueue() // someone else takes the slot the run gave up
	if err := other.wait(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- r.regain() }()
	select {
	case err := <-done:
		t.Fatalf("regain returned %v with the slot held elsewhere", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("regain on a canceled run: %v", err)
	}
	if r.held || busy.waiters.Len() != 0 {
		t.Fatalf("a canceled regain left held=%v waiters=%d", r.held, busy.waiters.Len())
	}
}
