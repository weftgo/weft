package pool

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// settle is idempotent per receipt: a second settlement — the panic
// handler's after the run's own, a Cancel racing the run's end — is
// not recorded, and the parent's Delegated bucket counts the child
// once. It used to append a second settlement and bill twice.
func TestSettleOnce(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := New(1)
	parent, _ := thread.Create(ctx, st, core.New(wefttest.Script()))
	gate := make(chan struct{})
	r, err := p.Submit(ctx, parent, core.New(stubModel{gate: gate}), "go")
	if err != nil {
		t.Fatal(err)
	}
	// Hold the delegate before it retires, to settle it again.
	p.mu.Lock()
	d := p.delegates[r.ID]
	p.mu.Unlock()
	if d == nil {
		t.Fatal("no delegate for a child that has not run")
	}
	close(gate)
	rc, err := p.Wait(ctx, parent, r.ID)
	if err != nil || rc.State != Done {
		t.Fatalf("receipt = %+v, %v", rc, err)
	}
	before := parent.Usage().Delegated
	if before.OutputTokens != 5 {
		t.Fatalf("Delegated = %+v", before)
	}
	if p.settle(ctx, d, Failed, "panic: late") {
		t.Error("a second settle reported itself the settlement")
	}
	p.conclude(ctx, d, Canceled, "again", "", true)
	var settlements int
	for _, e := range parent.Entries() {
		if pr, ok := e.(thread.PoolReceiptEntry); ok && pr.Receipt == r.ID && State(pr.Status).Settled() {
			settlements++
		}
	}
	if settlements != 1 {
		t.Errorf("%d settlements recorded for one receipt", settlements)
	}
	if after := parent.Usage().Delegated; after != before {
		t.Errorf("Delegated after a second settle = %+v, was %+v", after, before)
	}
	if got, _ := lookup(parent, r.ID); got.State != Done || got.Stop != "ok" {
		t.Errorf("the receipt after a second settle = %+v", got)
	}
}

// The pool's registers hold unsettled delegations only: nothing grows
// per delegation — sessionAgents used to keep one agent per child for
// the life of the pool — and a Register is dropped with its session's
// settlement.
func TestRegistersPruned(t *testing.T) {
	ctx := context.Background()
	p := New(2)
	child := core.New(stubModel{})
	parent, _ := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script()))
	var ids []string
	for i := 0; i < 20; i++ {
		r, err := p.Submit(ctx, parent, child, "go")
		if err != nil {
			t.Fatal(err)
		}
		// A registration for a live child, as a restart would make.
		if err := p.Register(r.Child, child); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	for _, id := range ids {
		if rc, err := p.Wait(ctx, parent, id); err != nil || rc.State != Done {
			t.Fatalf("receipt = %+v, %v", rc, err)
		}
	}
	// The run's last act is the settlement; a moment later it is at
	// rest and Close has nothing left to wait for.
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.delegates) != 0 || len(p.byChild) != 0 || len(p.sessionAgents) != 0 || len(p.calls) != 0 {
		t.Errorf("after 20 settled delegations: delegates=%d byChild=%d sessionAgents=%d calls=%d, want all empty",
			len(p.delegates), len(p.byChild), len(p.sessionAgents), len(p.calls))
	}
}

// A pump reads the ledger, then looks for each child's delegate; a
// delegation that settles between the two has retired its delegate and
// its Register'd agent. The lookup reads the ledger again after the
// miss and reports the delegation settled — it used to report
// ErrNoAgent for the child that had just finished (TestDecideJoinsErrors
// on CI, where Decide's own pump raced the watch's) or reopen it.
func TestDelegateForReadsTheLedgerAfterTheMiss(t *testing.T) {
	ctx := context.Background()
	p := New(1)
	parent, _ := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script()))
	gate := make(chan struct{})
	r, err := p.Submit(ctx, parent, core.New(stubModel{gate: gate}), "go")
	if err != nil {
		t.Fatal(err)
	}
	stale, ok := lookup(parent, r.ID) // the pump's read, a step behind
	if !ok || stale.Settled() {
		t.Fatalf("receipt before the run = %+v, %v", stale, ok)
	}
	close(gate)
	if rc, err := p.Wait(ctx, parent, r.ID); err != nil || rc.State != Done {
		t.Fatalf("receipt = %+v, %v", rc, err)
	}
	d, err := p.delegateFor(ctx, parent, stale, "")
	if !errors.Is(err, errSettled) || d != nil {
		t.Fatalf("delegateFor on a delegation settled since the read = %v, %v; want errSettled", d, err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// A decision can land while the child is parking: its requests are
// mirrored, the parked receipt written, but the phase still reads the
// run in flight. Every pump there — the deciding Decide's own, the
// watch's — sees no parked child and leaves it, and nothing pumps
// again. park reads the verdicts once more after the phase is parked
// and arms the resume itself; before, the receipt stayed parked for
// good (TestConcurrentDecide on CI, "no receipt reached done").
func TestDecisionWhileParkingIsArmed(t *testing.T) {
	ctx := context.Background()
	p := New(1)
	parent, _ := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script()))
	refund := core.Tool("refund", "", func(_ context.Context, _ struct{ OrderID string }) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.Say("approved"),
	), refund)
	var decided []error
	p.testHookParking = func(d *delegate) {
		if len(decided) > 0 {
			return // the resume parks no more; one decision only
		}
		pending := parent.Pending()
		if len(pending) != 1 {
			t.Errorf("mirrors while parking = %+v", pending)
			return
		}
		// The pool's watch on the parent, its pump goroutines counted
		// so the window holds until every one has run.
		var watched sync.WaitGroup
		parent.WatchMirrors(p, func(log *slog.Logger) {
			watched.Add(1)
			go func() { defer watched.Done(); p.pumpAsync(parent, log) }()
		})
		// Decide's two halves, every pump inside the window: the
		// parent records the approval (the watch's pump fires), then
		// Decide's own pump.
		_, err := parent.Decide(ctx, thread.Approve(pending[0].CallID))
		watched.Wait()
		if err == nil {
			err = p.pump(ctx, parent)
		}
		decided = append(decided, err)
		p.mu.Lock()
		phase := d.phase
		p.mu.Unlock()
		if phase != phaseRunning {
			t.Errorf("phase inside the window = %d, want running", phase)
		}
	}
	r, err := p.Submit(ctx, parent, child, "refund order 1")
	if err != nil {
		t.Fatal(err)
	}
	// Wait returns at rest, and parked is rest: poll to settlement.
	deadline := time.Now().Add(5 * time.Second)
	rc, err := p.Wait(ctx, parent, r.ID)
	for err == nil && !rc.Settled() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		rc, err = p.Wait(ctx, parent, r.ID)
	}
	if err != nil || rc.State != Done || rc.Stop != "approved" {
		t.Fatalf("receipt = %+v, %v; want done \"approved\" (the decision made while parking was lost)", rc, err)
	}
	if len(decided) != 1 || decided[0] != nil {
		t.Fatalf("Decide inside the window = %v", decided)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// stubModel answers every request with one line — a model any number
// of sessions can share — once its gate, when it has one, is open.
type stubModel struct{ gate chan struct{} }

func (m stubModel) Stream(ctx context.Context, _ core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		if m.gate != nil {
			select {
			case <-m.gate:
			case <-ctx.Done():
			}
		}
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		if !yield(core.ModelTextDelta{Text: "ok"}, nil) {
			return
		}
		yield(core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}}, nil)
	}
}
