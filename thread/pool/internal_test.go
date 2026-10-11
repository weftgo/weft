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
	// A registration landing after the settlement, before the lookup
	// answers: the settled answer drops it.
	if err := p.Register(r.Child, core.New(stubModel{})); err != nil {
		t.Fatal(err)
	}
	d, err := p.delegateFor(ctx, parent, stale, "")
	if !errors.Is(err, errSettled) || d != nil {
		t.Fatalf("delegateFor on a delegation settled since the read = %v, %v; want errSettled", d, err)
	}
	p.mu.Lock()
	n := len(p.sessionAgents)
	p.mu.Unlock()
	if n != 0 {
		t.Errorf("sessionAgents after delegateFor answered settled = %d, want 0", n)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// A registration made after its session's delegation settled — Submit
// returns, the child runs to its end and settles before the caller's
// Register — has no settlement left to drop it. The pool drops it the
// next time it reads the settled receipt; before, it stayed for the
// life of the pool (TestRegistersPruned on CI: "sessionAgents=1").
func TestRegisterAfterSettlementPruned(t *testing.T) {
	ctx := context.Background()
	p := New(1)
	child := core.New(stubModel{})
	parent, _ := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script()))
	r, err := p.Submit(ctx, parent, child, "go")
	if err != nil {
		t.Fatal(err)
	}
	if rc, err := p.Wait(ctx, parent, r.ID); err != nil || rc.State != Done {
		t.Fatalf("receipt = %+v, %v", rc, err)
	}
	p.mu.Lock()
	_, live := p.byChild[r.Child]
	p.mu.Unlock()
	if live {
		t.Fatal("the delegate outlived Wait's settled read")
	}
	// The interleaving CI hit, forced: the Register lands after the
	// settlement retired the delegate.
	if err := p.Register(r.Child, child); err != nil {
		t.Fatal(err)
	}
	if rc, err := p.Wait(ctx, parent, r.ID); err != nil || rc.State != Done {
		t.Fatalf("receipt = %+v, %v", rc, err)
	}
	p.mu.Lock()
	n := len(p.sessionAgents)
	p.mu.Unlock()
	if n != 0 {
		t.Errorf("sessionAgents after Wait read the settled receipt = %d, want 0 (a late Register is never dropped)", n)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// Two pumps race over one restarted child — Decide's own and the
// watch's the decision it records fires. Both miss the delegate and
// read the ledger unsettled; one rebuilds the child, arms and runs it
// to its end, and its settlement drops the Register'd agent. The
// other then looked the agent up and reported ErrNoAgent for a child
// that had just finished (TestRegisterResumesParkedSubmitChild on CI:
// "no agent for child session … (a Submit child — Register the
// session)", Decide failing at once). A settlement since the ledger
// read now sends the lookup back to the ledger, which reports it
// settled.
func TestRacingPumpSettlementIsNoAgentFree(t *testing.T) {
	racingPumpSettlement(t, false)
}

// The same race where the losing pump's agent lookup succeeds — a
// wrapped child, whose wrap-name agent no settlement drops, or a
// registration made again in the window, as here. It used to go on to
// install a parked delegate for the finished delegation, alive in
// delegates and byChild for the life of the pool; a settlement of the
// child seen at the insert now answers settled instead.
func TestRacingPumpSettlementNotRebuilt(t *testing.T) {
	racingPumpSettlement(t, true)
}

func racingPumpSettlement(t *testing.T, lateRegister bool) {
	ctx := context.Background()
	st := thread.Memory()
	refund := core.Tool("refund", "", func(_ context.Context, _ struct{ OrderID string }) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	p := New(1)
	s, _ := thread.Create(ctx, st, core.New(wefttest.Script()))
	r, err := p.Submit(ctx, s, core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"2"}`}),
	), refund), "refund order 2")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(s.Pending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(s.Pending()) != 1 {
		t.Fatalf("the mirror never landed: %+v", s.Pending())
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	// The restart: a new pool holding a registration, the parent
	// reopened and the approval recorded on it.
	open, err := thread.Open(ctx, st, s.ID(), core.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	p2 := New(1)
	resumed := core.New(wefttest.Script(wefttest.Say("resumed")), refund)
	if err := p2.Register(r.Child, resumed); err != nil {
		t.Fatal(err)
	}
	if _, err := open.Decide(ctx, thread.Approve(open.Pending()[0].CallID)); err != nil {
		t.Fatal(err)
	}
	// The second pump runs whole inside the first one's window: after
	// its ledger read, before its agent lookup.
	var raced []error
	entered := false
	p2.testHookResolving = func(Receipt) {
		if entered {
			return // the racing pump's own lookup passes through
		}
		entered = true
		raced = append(raced, p2.pump(ctx, open))
		if rc, err := p2.Wait(ctx, open, r.ID); err != nil || rc.State != Done {
			t.Errorf("the racing pump's run = %+v, %v", rc, err)
		}
		if lateRegister {
			// The first pump's agent lookup now succeeds.
			if err := p2.Register(r.Child, resumed); err != nil {
				t.Error(err)
			}
		}
	}
	if err := p2.pump(ctx, open); err != nil {
		t.Fatalf("the pump beside a racing one's settlement: %v", err)
	}
	if len(raced) != 1 || raced[0] != nil {
		t.Fatalf("the racing pump = %v", raced)
	}
	rc, _ := lookup(open, r.ID)
	if rc.State != Done || rc.Stop != "resumed" {
		t.Errorf("receipt = %+v, want done \"resumed\"", rc)
	}
	p2.mu.Lock()
	if len(p2.delegates) != 0 || len(p2.byChild) != 0 || len(p2.sessionAgents) != 0 {
		t.Errorf("registers after the race: delegates=%d byChild=%d sessionAgents=%d, want all empty (a settled delegation rebuilt)",
			len(p2.delegates), len(p2.byChild), len(p2.sessionAgents))
	}
	p2.mu.Unlock()
	if err := p2.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// Wait's promise for a sync delegation that settles after a park: when
// it returns, the parent's parked call is resolved and its resume armed
// — Turn.Next of the parked turn is the continuation. The delegate used
// to retire with the settlement, before the resolution: a Wait landing
// in between found no delegate, read the settled ledger and returned,
// and the parked turn's Next was nil (TestNestedSyncFullFlow and
// TestChildInheritsSigning in the nightly soak; the latter called Wait
// on the nil Turn and panicked). The delegate now retires after the
// resolution, and Wait waits for a concluding delegate.
func TestWaitReturnsWithTheResumeArmed(t *testing.T) {
	ctx := context.Background()
	p := New(1)
	refund := core.Tool("refund", "", func(_ context.Context, _ struct{ OrderID string }) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.Say("approved"),
	), refund)
	parent, _ := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", ID: "c-wrapper", Args: `{"prompt":"refund order 1"}`}),
		wefttest.Say("all done"),
	), p.MustWrap("research", "", child)))
	t1, err := parent.Send(ctx, core.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := t1.Wait(); err != nil || len(res.Pending) != 1 {
		t.Fatalf("the parent's turn = %+v, %v; want parked on the wrapper", res, err)
	}
	rs := Receipts(parent)
	if len(rs) != 1 {
		t.Fatalf("receipts = %+v", rs)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	p.testHookConcluding = func(*delegate) {
		close(entered)
		<-release
	}
	if err := p.Decide(ctx, parent, thread.Approve(parent.Pending()[0].CallID)); err != nil {
		t.Fatal(err)
	}
	<-entered // settled in the ledger, the wrapper not yet resolved
	type waited struct {
		rc   Receipt
		err  error
		next *thread.Turn
	}
	done := make(chan waited, 1)
	go func() {
		rc, err := p.Wait(ctx, parent, rs[0].ID)
		done <- waited{rc, err, t1.Next()}
	}()
	var w waited
	select {
	case w = <-done:
		// Returned inside the window: the promise is broken when the
		// continuation is not there.
		if w.next == nil {
			close(release)
			t.Fatalf("Wait returned (%+v, %v) before the wrapper resolved: the parked turn's Next is nil", w.rc, w.err)
		}
		close(release)
	case <-time.After(100 * time.Millisecond):
		close(release)
		w = <-done
	}
	if w.err != nil || w.rc.State != Done || w.next == nil {
		t.Fatalf("Wait = %+v, %v, Next = %v; want done with the continuation armed", w.rc, w.err, w.next)
	}
	if res, err := w.next.Wait(); err != nil || res.Text() != "all done" {
		t.Fatalf("the parent's continuation: %v, %v", res, err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// parkedSyncDelegation runs a parent turn that delegates through a
// wrap to a child that parks at an approval, and returns the parked
// parent turn and the receipt.
func parkedSyncDelegation(t *testing.T, ctx context.Context, p *Pool, st thread.Storage) (*thread.Session, *thread.Turn, Receipt) {
	t.Helper()
	refund := core.Tool("refund", "", func(_ context.Context, _ struct{ OrderID string }) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.Say("approved"),
	), refund)
	parent, err := thread.Create(ctx, st, core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", ID: "c-wrapper", Args: `{"prompt":"refund order 1"}`}),
		wefttest.Say("all done"),
	), p.MustWrap("research", "", child)))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := parent.Send(ctx, core.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := t1.Wait(); err != nil || len(res.Pending) != 1 {
		t.Fatalf("the parent's turn = %+v, %v; want parked on the wrapper", res, err)
	}
	rs := Receipts(parent)
	if len(rs) != 1 || rs[0].State != Parked {
		t.Fatalf("receipts = %+v", rs)
	}
	return parent, t1, rs[0]
}

// A waited Wait: what it returned and the parked turn's Next then.
type waited struct {
	rc   Receipt
	err  error
	next *thread.Turn
}

// waitInWindow runs Wait on its own goroutine while a hook holds the
// pool inside a window, and reports what it returned if it returned
// inside the window (early: Next is then the promise's test), then
// releases the window and returns Wait's answer.
func waitInWindow(t *testing.T, p *Pool, parent *thread.Session, t1 *thread.Turn, id string, release chan struct{}) (w waited, early bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan waited, 1)
	go func() {
		rc, err := p.Wait(ctx, parent, id)
		done <- waited{rc, err, t1.Next()}
	}()
	select {
	case w = <-done:
		early = true
		close(release)
	case <-time.After(100 * time.Millisecond):
		close(release)
		w = <-done
	}
	return w, early
}

// Wait's promise on the cancel path: Cancel concludes a parked child on
// its own goroutine with no run in flight, so Wait has no run's rest to
// watch — it watches the conclusion, closed when the delegate retires
// after the parent's call is resolved (SUBAGENT_CANCELED) and its
// resume armed. Without the conclusion arm Wait returned at once with
// the receipt canceled and Next nil; without its close it never
// returned.
func TestWaitDuringCancelParkedReturnsWithTheResumeArmed(t *testing.T) {
	ctx := context.Background()
	p := New(1)
	parent, t1, rc := parkedSyncDelegation(t, ctx, p, thread.Memory())
	entered := make(chan struct{})
	release := make(chan struct{})
	p.testHookConcluding = func(*delegate) {
		close(entered)
		<-release
	}
	canceled := make(chan error, 1)
	go func() { canceled <- p.Cancel(ctx, parent, rc.ID) }()
	<-entered // settled canceled in the ledger, the wrapper not yet resolved
	w, early := waitInWindow(t, p, parent, t1, rc.ID, release)
	if early && w.next == nil {
		t.Fatalf("Wait returned (%+v, %v) before the wrapper resolved: the parked turn's Next is nil", w.rc, w.err)
	}
	if err := <-canceled; err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if w.err != nil || w.rc.State != Canceled || w.next == nil {
		t.Fatalf("Wait = %+v, %v, armed = %v; want canceled with the continuation armed", w.rc, w.err, w.next != nil)
	}
	if res, err := w.next.Wait(); err != nil || res.Text() != "all done" {
		t.Fatalf("the parent's continuation: %v, %v", res, err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// Cancel moves a parked delegate to settling before it closes the
// child's boundary; until the settlement the delegate has no run and
// was not settled, so a Wait there returned at once — the ledger still
// parked, Next nil — and a second Cancel returned nil before the
// cancellation was settled. Both now wait for the conclusion: Wait
// returns canceled with the resume armed, the second Cancel reports the
// receipt settled. A cancel whose boundary cannot be closed releases
// them with the child parked again.
func TestCancelParkedSettlingWindowHeld(t *testing.T) {
	ctx := context.Background()
	t.Run("closed", func(t *testing.T) {
		p := New(1)
		parent, t1, rc := parkedSyncDelegation(t, ctx, p, thread.Memory())
		entered := make(chan struct{})
		release := make(chan struct{})
		p.testHookCanceling = func(*delegate) error {
			close(entered)
			<-release
			return nil
		}
		canceled := make(chan error, 1)
		go func() { canceled <- p.Cancel(ctx, parent, rc.ID) }()
		<-entered // settling, not settled
		again := make(chan error, 1)
		go func() { again <- p.Cancel(ctx, parent, rc.ID) }()
		w, early := waitInWindow(t, p, parent, t1, rc.ID, release)
		if early && (w.next == nil || w.rc.State != Canceled) {
			t.Fatalf("Wait returned (%+v, %v) inside the settling window: armed = %v", w.rc, w.err, w.next != nil)
		}
		if err := <-canceled; err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		if err := <-again; !errors.Is(err, ErrNotRunning) {
			t.Errorf("a second Cancel during the first = %v, want ErrNotRunning once it settled", err)
		}
		if w.err != nil || w.rc.State != Canceled || w.next == nil {
			t.Fatalf("Wait = %+v, %v, armed = %v; want canceled with the continuation armed", w.rc, w.err, w.next != nil)
		}
		if res, err := w.next.Wait(); err != nil || res.Text() != "all done" {
			t.Fatalf("the parent's continuation: %v, %v", res, err)
		}
		if err := p.Close(ctx); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("not closed", func(t *testing.T) {
		p := New(1)
		parent, t1, rc := parkedSyncDelegation(t, ctx, p, thread.Memory())
		entered := make(chan struct{})
		release := make(chan struct{})
		errClose := errors.New("boundary not closed")
		p.testHookCanceling = func(*delegate) error {
			close(entered)
			<-release
			return errClose
		}
		canceled := make(chan error, 1)
		go func() { canceled <- p.Cancel(ctx, parent, rc.ID) }()
		<-entered
		w, _ := waitInWindow(t, p, parent, t1, rc.ID, release)
		if err := <-canceled; !errors.Is(err, errClose) {
			t.Fatalf("Cancel = %v, want the close's failure", err)
		}
		if w.err != nil || w.rc.State != Parked {
			t.Fatalf("Wait after a failed cancel = %+v, %v; want parked", w.rc, w.err)
		}
		if err := p.Close(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

// panicOnSettle is a storage whose first append of a settled pool
// receipt panics — a settlement that dies inside settle.
type panicOnSettle struct {
	thread.Storage
	fired atomic.Bool
}

func (s *panicOnSettle) Append(ctx context.Context, session string, entries ...thread.Entry) error {
	for _, e := range entries {
		if pr, ok := e.(thread.PoolReceiptEntry); ok && State(pr.Status).Settled() && s.fired.CompareAndSwap(false, true) {
			panic("storage died appending the settlement")
		}
	}
	return s.Storage.Append(ctx, session, entries...)
}

// A panic inside settle — after it marked the delegate settled, before
// the conclusion's retire was deferred — left the delegate unretired
// and its conclusion never closed: the run's recover re-entered
// conclude, settle answered "already settled", and every Wait after
// blocked for good. settle now retires on its way out of a panic; Wait
// returns the receipt as the ledger reads it (the settlement was not
// recorded: Recover settles it from the child).
func TestPanicInSettleRetires(t *testing.T) {
	ctx := context.Background()
	st := &panicOnSettle{Storage: thread.Memory()}
	p := New(1)
	parent, _ := thread.Create(ctx, st, core.New(wefttest.Script()))
	r, err := p.Submit(ctx, parent, core.New(stubModel{}), "go")
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rc, err := p.Wait(wctx, parent, r.ID)
	if err != nil {
		t.Fatalf("Wait after a panic in settle: %v (the conclusion never closed)", err)
	}
	if !st.fired.Load() {
		t.Fatal("the settlement's append never panicked")
	}
	if rc.Settled() {
		t.Errorf("receipt = %+v; the panicked settlement was not recorded", rc)
	}
	p.mu.Lock()
	n := len(p.delegates) + len(p.byChild)
	p.mu.Unlock()
	if n != 0 {
		t.Errorf("the delegate outlived its panicked settlement: %d entries", n)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// Close closes the session of a child parked at an approval, letting
// its writer lease go for the process that resumes it. A child still
// parking when Close ran — its requests mirrored and its parked receipt
// written, its phase not yet parked — was taken for a running one:
// Close canceled its context (nothing to cancel: the turn had ended),
// the park completed, and nobody closed the session. Its lease outlived
// the pool, and the next process's resume failed with "session is
// locked by another writer" (TestRacingPumpSettlementNotRebuilt in the
// nightly soak, whose restart closes the old pool as soon as the
// mirror lands). park now closes the child itself when it finds the
// pool closed as it reaches parked.
func TestCloseWhileParkingReleasesTheChild(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	refund := core.Tool("refund", "", func(_ context.Context, _ struct{ OrderID string }) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	p := New(1)
	closed := make(chan error, 1)
	p.testHookParking = func(*delegate) {
		// Close inside the window, and hold the park until it has
		// marked the pool closed and taken its census of parked
		// children (the drain then waits for this run).
		go func() { closed <- p.Close(ctx) }()
		for {
			p.mu.Lock()
			c := p.closed
			p.mu.Unlock()
			if c {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	s, _ := thread.Create(ctx, st, core.New(wefttest.Script()))
	r, err := p.Submit(ctx, s, core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"2"}`}),
	), refund), "refund order 2")
	if err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatalf("Close: %v", err)
	}
	if rc, _ := lookup(s, r.ID); rc.State != Parked {
		t.Fatalf("receipt after the close = %+v, want parked", rc)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	// The next process: the child resumes under a new pool.
	open, err := thread.Open(ctx, st, s.ID(), core.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	p2 := New(1)
	if err := p2.Register(r.Child, core.New(wefttest.Script(wefttest.Say("resumed")), refund)); err != nil {
		t.Fatal(err)
	}
	if err := p2.Decide(ctx, open, thread.Approve(open.Pending()[0].CallID)); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rc, err := p2.Wait(wctx, open, r.ID)
	if err != nil || rc.State != Done || rc.Stop != "resumed" {
		t.Fatalf("the resume after the restart = %+v, %v; want done \"resumed\" (the old pool's child kept its lease)", rc, err)
	}
	if err := p2.Close(ctx); err != nil {
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
	// armed closes once the hook's pump has armed the resume: the
	// test's Wait starts only then, so it watches the resume's run, not
	// the first park's — it returns at the re-park while the hook still
	// holds the first park's arm, and the test goes on to decide the new
	// call and run the child to its end inside that hold (the ordering
	// CI hit when the test goroutine was slow to reach Wait).
	armed := make(chan struct{})
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
		close(armed)
		// Re-parked is parks reaching 2, which only grows; the phase
		// is no witness — Wait returns at the re-park, the test decides
		// the new call and the child runs on and settles, all while
		// this hook still holds the first park's arm.
		deadline := time.Now().Add(5 * time.Second)
		for {
			p.mu.Lock()
			reparked := d.parks >= 2
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
	<-armed
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
