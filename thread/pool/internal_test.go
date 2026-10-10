package pool

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"sync"
	"sync/atomic"
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
// good (TestConcurrentDecide on CI, "no receipt reached done"). Both
// paths: a Submit, and a sync wrap — where the park also marks the
// delegating call to resolve and the armed resume first waits for
// the parent's turn to park that call.
func TestDecisionWhileParkingIsArmed(t *testing.T) {
	for _, syncWrap := range []bool{false, true} {
		name := "submit"
		if syncWrap {
			name = "wrap"
		}
		t.Run(name, func(t *testing.T) { decisionWhileParking(t, syncWrap) })
	}
}

func decisionWhileParking(t *testing.T, syncWrap bool) {
	ctx := context.Background()
	p := New(1)
	refund := core.Tool("refund", "", func(_ context.Context, _ struct{ OrderID string }) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.Say("approved"),
	), refund)
	parentAgent := core.New(wefttest.Script())
	if syncWrap {
		parentAgent = core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "research", ID: "c-wrapper", Args: `{"prompt":"refund order 1"}`}),
			wefttest.Say("all done"),
		), p.MustWrap("research", "", child))
	}
	parent, _ := thread.Create(ctx, thread.Memory(), parentAgent)
	var decided []error
	p.testHookParking = func(d *delegate) {
		if len(decided) > 0 {
			return // the resume parks no more; one decision only
		}
		if (d.wrapper != "") != syncWrap {
			t.Errorf("delegate wrapper = %q, sync wrap %v", d.wrapper, syncWrap)
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
	var (
		turn *thread.Turn
		id   string
	)
	if syncWrap {
		var err error
		turn, err = parent.Send(ctx, core.User("refund order 1"))
		if err != nil {
			t.Fatal(err)
		}
		res, err := turn.Wait()
		if err != nil || len(res.Pending) != 1 || res.Pending[0].ID != "c-wrapper" {
			t.Fatalf("the parent's delegating turn = %+v, %v; want the delegating call parked", res, err)
		}
		rs := Receipts(parent)
		if len(rs) != 1 {
			t.Fatalf("receipts = %+v", rs)
		}
		id = rs[0].ID
	} else {
		r, err := p.Submit(ctx, parent, child, "refund order 1")
		if err != nil {
			t.Fatal(err)
		}
		id = r.ID
	}
	// Wait returns at rest, and parked is rest: poll to settlement.
	deadline := time.Now().Add(5 * time.Second)
	rc, err := p.Wait(ctx, parent, id)
	for err == nil && !rc.Settled() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		rc, err = p.Wait(ctx, parent, id)
	}
	if err != nil || rc.State != Done || rc.Stop != "approved" {
		t.Fatalf("receipt = %+v, %v; want done \"approved\" (the decision made while parking was lost)", rc, err)
	}
	if len(decided) != 1 || decided[0] != nil {
		t.Fatalf("Decide inside the window = %v", decided)
	}
	if syncWrap {
		// The resolution landed on the parked delegating call: the
		// parent's turn resumes with the child's answer.
		next := turn.Next()
		if next == nil {
			t.Fatal("the delegating call did not resolve")
		}
		if res, err := next.Wait(); err != nil || res.Text() != "all done" {
			t.Fatalf("the parent's continuation = %+v, %v", res, err)
		}
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// A park's own arm can come after a newer park: a pump arms the resume
// between the park's phase write and its arm, the resume takes a
// second slot, runs and parks again — under the same call id, which a
// new run mints afresh. The first park's replay, read at its own
// boundary, approves call_1 of the first run; armed now it used to
// pass the phase check (the child is parked again) and its approval,
// replayed by bare call id, ran the second run's call_1 without
// anyone deciding it. arm takes the park's number and refuses a stale
// one.
func TestStaleParkReplayRefused(t *testing.T) {
	ctx := context.Background()
	p := New(2)
	var refunds atomic.Int32
	refund := core.Tool("refund", "", func(_ context.Context, _ struct{ OrderID string }) (string, error) {
		refunds.Add(1)
		return "refunded", nil
	}, core.RequireApproval())
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"2"}`}),
		wefttest.Say("both refunded"),
	), refund)
	parent, _ := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script()))
	var parking, arming atomic.Int32
	p.testHookParking = func(d *delegate) {
		if parking.Add(1) != 1 {
			return
		}
		// No watch pumps here: the window's pump is the one the arming
		// hook runs. Record the approval of the first run's call_1, so
		// the park's own re-read finds its boundary complete.
		parent.WatchMirrors(p, func(*slog.Logger) {})
		pending := parent.Pending()
		if len(pending) != 1 {
			t.Errorf("mirrors while parking = %+v", pending)
			return
		}
		if _, err := parent.Decide(ctx, thread.Approve(pending[0].CallID)); err != nil {
			t.Errorf("Decide: %v", err)
		}
	}
	p.testHookArming = func(d *delegate) {
		if arming.Add(1) != 1 {
			t.Error("a second park found its boundary complete")
			return
		}
		// The racing pump: it reads parked and arms the resume, which
		// runs in the second slot and parks again on the new call_1.
		if err := p.pump(ctx, parent); err != nil {
			t.Errorf("pump: %v", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			p.mu.Lock()
			reparked := d.phase == phaseParked && d.parks == 2
			p.mu.Unlock()
			if reparked {
				return
			}
			if time.Now().After(deadline) {
				t.Error("the resume did not park again")
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	r, err := p.Submit(ctx, parent, child, "refund orders 1 and 2")
	if err != nil {
		t.Fatal(err)
	}
	rc, err := p.Wait(ctx, parent, r.ID)
	if err != nil || rc.State != Parked {
		t.Fatalf("receipt after the re-park = %+v, %v; want parked", rc, err)
	}
	if n := refunds.Load(); n != 1 {
		t.Fatalf("refund ran %d times before the second call was decided, want 1 (a stale replay approved it)", n)
	}
	pending := parent.Pending()
	if len(pending) != 1 {
		t.Fatalf("Pending after the re-park = %+v, want the second run's call alone", pending)
	}
	if err := p.Decide(ctx, parent, thread.Approve(pending[0].CallID)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	rc, err = p.Wait(ctx, parent, r.ID)
	for err == nil && !rc.Settled() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		rc, err = p.Wait(ctx, parent, r.ID)
	}
	if err != nil || rc.State != Done || rc.Stop != "both refunded" {
		t.Fatalf("receipt = %+v, %v; want done \"both refunded\"", rc, err)
	}
	if n := refunds.Load(); n != 2 {
		t.Errorf("refund ran %d times, want 2", n)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// A Cancel that fails leaves the child parked — but a decision
// recorded while the Cancel held it settling met a pump that skipped
// it (the child was not parked), and no later pump comes unless
// someone calls. The failed cancel's restore re-reads the verdicts
// and arms the resume itself, as park does; it used to restore the
// phase alone, and the ready decision waited for the next Decide.
func TestFailedCancelArmsReadyDecision(t *testing.T) {
	ctx := context.Background()
	p := New(1)
	var refunds atomic.Int32
	refund := core.Tool("refund", "", func(_ context.Context, _ struct{ OrderID string }) (string, error) {
		refunds.Add(1)
		return "refunded", nil
	}, core.RequireApproval())
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.Say("refunded"),
	), refund)
	parent, _ := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script()))
	r, err := p.Submit(ctx, parent, child, "refund order 1")
	if err != nil {
		t.Fatal(err)
	}
	rc, err := p.Wait(ctx, parent, r.ID)
	if err != nil || rc.State != Parked {
		t.Fatalf("receipt = %+v, %v; want parked", rc, err)
	}
	errClose := errors.New("boundary not closed")
	p.testHookCanceling = func(d *delegate) error {
		p.mu.Lock()
		settling := d.phase == phaseSettling
		p.mu.Unlock()
		if !settling {
			t.Error("cancel hook ran with the delegate not settling")
		}
		// No watch pumps from here on: the decision lands in the
		// settling window and nothing but the restore can arm it.
		parent.WatchMirrors(p, func(*slog.Logger) {})
		pending := parent.Pending()
		if len(pending) != 1 {
			t.Errorf("mirrors while settling = %+v", pending)
			return errClose
		}
		if _, err := parent.Decide(ctx, thread.Approve(pending[0].CallID)); err != nil {
			t.Errorf("Decide: %v", err)
		}
		return errClose
	}
	if err := p.Cancel(ctx, parent, r.ID); !errors.Is(err, errClose) {
		t.Fatalf("Cancel = %v; want the injected failure", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	rc, err = p.Wait(ctx, parent, r.ID)
	for err == nil && !rc.Settled() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		rc, err = p.Wait(ctx, parent, r.ID)
	}
	if err != nil || rc.State != Done || rc.Stop != "refunded" {
		t.Fatalf("receipt = %+v, %v; want done \"refunded\" without another Decide", rc, err)
	}
	if n := refunds.Load(); n != 1 {
		t.Errorf("refund ran %d times, want 1", n)
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
