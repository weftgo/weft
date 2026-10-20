package pool

import (
	"container/list"
	"context"
	"sync"
)

// fifo is the pool's semaphore: a fixed number of slots, granted
// strictly in the order the waiters queued. The order is the queue's
// own — a list under a mutex, the shape of golang.org/x/sync/semaphore
// — not the runtime's channel wait order, which the language does not
// specify: a waiter that queued first is granted first, always.
type fifo struct {
	mu      sync.Mutex
	free    int
	waiters list.List // of *ticket, front = longest waiting
}

func newFIFO(max int) *fifo { return &fifo{free: max} }

// A ticket is one place in the queue. enqueue takes the place — at
// once, on the caller's goroutine, so the queue's order is the order
// of the calls that queued — and wait blocks for the grant.
type ticket struct {
	f     *fifo
	ready chan struct{} // closed when the slot is granted
	// el is the ticket's place in the queue; nil once it is granted
	// or withdrawn. Guarded by f.mu.
	el *list.Element
}

// enqueue takes a place in the queue. A free slot with nobody waiting
// is granted on the spot; otherwise the ticket queues behind every
// earlier one — a free slot never jumps the queue.
func (f *fifo) enqueue() *ticket {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &ticket{f: f, ready: make(chan struct{})}
	if f.free > 0 && f.waiters.Len() == 0 {
		f.free--
		close(t.ready)
		return t
	}
	t.el = f.waiters.PushBack(t)
	return t
}

// wait blocks until the ticket's slot is granted or ctx ends. On nil
// the caller holds one slot and owes one release. On an error it
// holds nothing: the ticket left the queue, and a slot granted in the
// same instant was handed to the next waiter — a canceled waiter
// never keeps capacity.
func (t *ticket) wait(ctx context.Context) error {
	select {
	case <-t.ready:
		if ctx.Err() == nil {
			return nil
		}
	case <-ctx.Done():
	}
	t.withdraw()
	return ctx.Err()
}

// withdraw gives the ticket up: out of the queue when it is still
// waiting, or — when the grant already happened — the slot released
// to whoever is next.
func (t *ticket) withdraw() {
	t.f.mu.Lock()
	if t.el != nil {
		t.f.waiters.Remove(t.el)
		t.el = nil
		t.f.mu.Unlock()
		return
	}
	t.f.mu.Unlock()
	t.f.release()
}

// release returns one slot: to the longest-waiting ticket when there
// is one, else to the free count.
func (f *fifo) release() {
	f.mu.Lock()
	defer f.mu.Unlock()
	front := f.waiters.Front()
	if front == nil {
		f.free++
		return
	}
	t := front.Value.(*ticket)
	f.waiters.Remove(front)
	t.el = nil
	close(t.ready)
}

// A slot is one child run's hold on the pool, and the hand-off that
// keeps delegation from deadlocking it (ADR 0022, amendment
// 2026-10-01): a run holds its slot while it works — its model calls,
// its tools — and gives it up while it only waits, which is what a
// run does while a sync delegation of its own is running a child.
// The child takes a slot of its own from the queue; the parent takes
// one back, from the same queue, before it works again.
//
// A step may delegate several times at once (Parallelism): the run
// holds no slot while at least one of its sync delegations is
// waiting, and re-acquires when the last of them has returned — the
// next thing it does is its next model call. Taking the slot back any
// earlier would hold capacity the still-waiting siblings' children
// need: with one slot, that is the deadlock.
type slot struct {
	f *fifo
	// ctx is the owning run's context. A re-acquisition waits on it,
	// not on the delegating call's: the slot is the run's, and a call
	// that timed out must not leave its run working without one.
	ctx context.Context

	mu      sync.Mutex
	held    bool
	done    bool // the run ended; nothing is re-acquired any more
	waiting int  // sync delegations of this run blocked on a child
}

// yield marks one delegation of the run as waiting on its child, and
// releases the run's slot if it holds one. A nil slot — a run that is
// no pool child, a top-level session's — holds nothing and yields
// nothing.
func (s *slot) yield() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.waiting++
	if !s.held {
		s.mu.Unlock()
		return
	}
	s.held = false
	s.mu.Unlock()
	s.f.release()
}

// regain marks one delegation as returned, and — when it was the last
// one waiting — queues for a slot and blocks until the run holds one
// again. It returns the run context's error when the run was canceled
// while it queued; the run then holds nothing.
func (s *slot) regain() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.waiting--
	if s.waiting > 0 || s.held || s.done {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	if err := s.f.enqueue().wait(s.ctx); err != nil {
		return err
	}
	s.mu.Lock()
	if s.waiting > 0 || s.held || s.done {
		// The world moved while this call queued: a sibling
		// delegation started waiting (the run must not hold a slot
		// over it), another call re-acquired first, or the run ended.
		s.mu.Unlock()
		s.f.release()
		return nil
	}
	s.held = true
	s.mu.Unlock()
	return nil
}

// lend hands s's slot straight to the run that owns to, when that run
// is waiting slotless on its delegations: a nested resolution's
// hand-off (ADR 0022 §7). The grandchild's work is over, and the
// resume its answer is about to start is the waiting run's work, so
// the slot passes between them without going back to the queue —
// nothing can take it in between, and the resumed run never works
// without one. It reports whether the slot moved; when it did not
// (s holds none, to holds one already, or to's run is not waiting or
// is over) s keeps its slot and its finish releases it. Lock order: s,
// then to.
func (s *slot) lend(to *slot) bool {
	if s == nil || to == nil || s == to {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.held {
		return false
	}
	to.mu.Lock()
	defer to.mu.Unlock()
	if to.held || to.done || to.waiting == 0 {
		return false
	}
	to.held, s.held = true, false
	return true
}

// unlend gives back a slot lend moved to s when the resume it was
// lent for did not start: while s's run still waits (the slot unused),
// it goes back to the queue. A run that has stopped waiting is using
// it, and keeps it.
func (s *slot) unlend() {
	s.mu.Lock()
	give := s.held && s.waiting > 0
	if give {
		s.held = false
	}
	s.mu.Unlock()
	if give {
		s.f.release()
	}
}

// finish ends the run's hold: the slot goes back, and a delegation
// still returning — a handler its tool's timeout abandoned — takes
// nothing on the run's behalf any more.
func (s *slot) finish() {
	s.mu.Lock()
	held := s.held
	s.held, s.done = false, true
	s.mu.Unlock()
	if held {
		s.f.release()
	}
}
