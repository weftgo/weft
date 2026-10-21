package pool

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// A pool's pump that reaches another pool's child is told apart by
// foreign — but foreign asks the other pool, and a closed pool answers
// for nothing. When that child settles and its pool closes inside the
// pump's lookup (after the ledger read, before the agent lookup), the
// child is over: the lookup answers errSettled from the ledger read
// again, not ErrNoAgent. Before the fix the pump returned ErrNoAgent
// for a delegation that had ended, and the watch logged ERROR "a
// decision recorded on the parent did not reach its children"
// (TestTwoPoolsOneParent under make stress, GOMAXPROCS=1: one
// iteration in tens of thousands).
func TestForeignChildSettledAndItsPoolClosedDuringTheLookup(t *testing.T) {
	ctx := context.Background()
	pa, pb := New(1), New(1)
	defer func() { _ = pb.Close(ctx) }()
	parent, err := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	r, err := pa.Submit(ctx, parent, core.New(stubModel{gate: gate}), "go")
	if err != nil {
		t.Fatal(err)
	}
	pb.attach(parent)                 // both pools watch the parent, as two delegating pools do
	stale, ok := lookup(parent, r.ID) // pb's pump's ledger read: the child running
	if !ok || stale.Settled() {
		t.Fatalf("receipt before the run = %+v, %v", stale, ok)
	}
	pb.testHookResolving = func(Receipt) {
		// Inside pb's lookup: pa's child ends and pa closes.
		close(gate)
		if rc, err := pa.Wait(ctx, parent, r.ID); err != nil || rc.State != Done {
			t.Errorf("pa's receipt = %+v, %v", rc, err)
		}
		if err := pa.Close(ctx); err != nil {
			t.Errorf("pa Close: %v", err)
		}
	}
	d, err := pb.delegateFor(ctx, parent, stale, "")
	if !errors.Is(err, errSettled) || d != nil {
		t.Fatalf("pb's lookup of pa's child, settled and its pool closed since the ledger read = %v, %v; want errSettled", d, err)
	}
	pb.testHookResolving = nil
	if err := pb.pump(ctx, parent); err != nil {
		t.Errorf("pb's pump after pa closed = %v, want nil", err)
	}
}

// heldModel blocks until its run is canceled, then until release: a
// child run that a Close canceled and that has not settled yet.
type heldModel struct{ started, release chan struct{} }

func (m heldModel) Stream(ctx context.Context, _ core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		close(m.started)
		<-ctx.Done()
		<-m.release
		yield(nil, ctx.Err())
	}
}

// The other window: the other pool is closing, its child canceled and
// not settled yet. That child is still the closing pool's — its
// settlement comes from that pool's drain — so another pool's lookup
// answers errForeign, not ErrNoAgent. (A child a closed pool left
// parked stays no pool's: canResume answers for it no more.)
func TestForeignChildOfAClosingPool(t *testing.T) {
	ctx := context.Background()
	pa, pb := New(1), New(1)
	defer func() { _ = pb.Close(ctx) }()
	parent, err := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	m := heldModel{started: make(chan struct{}), release: make(chan struct{})}
	r, err := pa.Submit(ctx, parent, core.New(m), "go")
	if err != nil {
		t.Fatal(err)
	}
	pb.attach(parent)
	<-m.started
	closed := make(chan error, 1)
	go func() { closed <- pa.Close(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		pa.mu.Lock()
		c := pa.closed
		pa.mu.Unlock()
		if c {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pa never closed")
		}
		time.Sleep(time.Millisecond)
	}
	rc, _ := lookup(parent, r.ID)
	if rc.Settled() {
		t.Fatalf("pa's receipt settled while its run is held: %+v", rc)
	}
	d, err := pb.delegateFor(ctx, parent, rc, "")
	if !errors.Is(err, errForeign) || d != nil {
		t.Errorf("pb's lookup of the closing pool's child = %v, %v; want errForeign", d, err)
	}
	close(m.release)
	if err := <-closed; err != nil {
		t.Fatalf("pa Close: %v", err)
	}
	if rc, _ := lookup(parent, r.ID); rc.State != Canceled {
		t.Errorf("pa's receipt after its Close = %+v, want canceled", rc)
	}
}
