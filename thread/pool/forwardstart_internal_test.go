package pool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// A Forward landing between the running mark and the child's first
// Send (testHookStarting stands in for the scheduler) is refused as
// accepted — a steer there would be the child session's first turn,
// the task's prompt queued behind it, its park mirrored by no one —
// and the delegation runs as if it never came: the task's prompt is
// the first turn, the receipt settles, Wait and Close return.
func TestForwardInTheStartWindowIsRefused(t *testing.T) {
	ctx := context.Background()
	p := New(1)
	st := thread.Memory()
	s, err := thread.Create(ctx, st, core.New(wefttest.Script(wefttest.Say("lead"))))
	if err != nil {
		t.Fatal(err)
	}
	refund := core.Tool("refund", "", func(context.Context, struct{ OrderID string }) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c1", Args: `{"order_id":"1"}`}),
		wefttest.Say("done"),
	), refund)
	var fwdTurn, resTurn *thread.Turn
	var fwdErr, resErr error
	calls := 0
	forwarded := make(chan struct{})
	p.testHookStarting = func(d *delegate) {
		calls++
		switch calls {
		case 1:
			fwdTurn, fwdErr = p.Forward(ctx, s, d.receipt, core.User("hurry"))
			close(forwarded)
		case 2: // the resume's start
			resTurn, resErr = p.Forward(ctx, s, d.receipt, core.User("hurry"))
		}
	}
	rc, err := p.Submit(ctx, s, child, "the task")
	if err != nil {
		t.Fatal(err)
	}
	<-forwarded
	var se *StateError
	if fwdTurn != nil || !errors.As(fwdErr, &se) || se.State != Accepted || !errors.Is(fwdErr, ErrNotRunning) {
		t.Fatalf("Forward in the start window: turn %v, err %v; want a *StateError reading accepted", fwdTurn != nil, fwdErr)
	}
	// The child parks on its approval, mirrored to the parent.
	deadline := time.Now().Add(5 * time.Second)
	for len(s.Pending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(s.Pending()) != 1 {
		t.Fatalf("parent pending %+v, receipts %+v; want the child's refund mirrored", s.Pending(), Receipts(s))
	}
	if err := p.Decide(ctx, s, thread.Approve(s.Pending()[0].CallID)); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := p.Wait(wctx, s, rc.ID)
	if err != nil || r.State != Done {
		t.Fatalf("Wait: %+v %v; want done", r, err)
	}
	if calls != 2 || resTurn != nil || !errors.As(resErr, &se) || se.State != Parked {
		t.Errorf("Forward in the resume's start window (%d starts): turn %v, err %v; want a *StateError reading parked", calls, resTurn != nil, resErr)
	}
	c, err := thread.Open(ctx, st, rc.Child, core.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	var first string
	for _, m := range c.Context() {
		if m.Role == core.RoleUser {
			first = m.Text()
			break
		}
	}
	if first != "the task" {
		t.Errorf("the child's first turn is %q; want the task's prompt", first)
	}
	cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
	defer ccancel()
	if err := p.Close(cctx); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// A Forward landing after the child's first Send returned — its turn
// in the child's session and working — but before the pool marked it
// sent (testHookStarted stands in for the scheduler that has not yet
// run the start's caller again) steers that turn: it waits for the
// mark, then the steer is delivered. Before the fix it was refused as
// accepted though the child's model was running
// (TestForwardRequiresRunning and
// TestForwardedSteerDoesNotOutliveACanceledRun under make stress).
func TestForwardAfterTheStartReturnedSteers(t *testing.T) {
	ctx := context.Background()
	p := New(1)
	s, err := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script(wefttest.Say("lead"))))
	if err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	type result struct {
		turn *thread.Turn
		err  error
	}
	got := make(chan result, 1)
	early := false
	p.testHookStarted = func(d *delegate) {
		fwd := make(chan result, 1)
		go func() {
			turn, err := p.Forward(ctx, s, d.receipt, core.User("hurry"))
			fwd <- result{turn, err}
		}()
		// The Forward waits for the sent mark, which comes after this
		// hook returns: an answer while the hook holds is the refusal.
		select {
		case r := <-fwd:
			early = true
			got <- r
		case <-time.After(100 * time.Millisecond):
			go func() { got <- <-fwd }()
		}
	}
	rc, err := p.Submit(ctx, s, core.New(stubModel{gate: gate}), "the task")
	if err != nil {
		t.Fatal(err)
	}
	var r result
	select {
	case r = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("Forward never returned")
	}
	if early || r.err != nil || r.turn == nil {
		t.Fatalf("Forward after the start returned: early %v, turn %v, err %v; want it to wait and steer", early, r.turn != nil, r.err)
	}
	close(gate)
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if rc, err := p.Wait(wctx, s, rc.ID); err != nil || rc.State != Done {
		t.Fatalf("Wait: %+v %v; want done", rc, err)
	}
	if err := p.Close(wctx); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// After a restart, the delegate a new pool rebuilds for a parked child
// (delegateFor: parked, no park of its own counted) is resuming when a
// decision arms it: a Forward in that resume's start window is refused
// as parked, the state it is leaving. On v0.18.0 it read accepted, the
// refusal picked by the delegate's park count.
func TestForwardInARestartedResumesStartWindowReadsParked(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	s, err := thread.Create(ctx, st, core.New(wefttest.Script(wefttest.Say("lead"))))
	if err != nil {
		t.Fatal(err)
	}
	refund := func() *core.ToolDef {
		return core.Tool("refund", "", func(context.Context, struct{ OrderID string }) (string, error) {
			return "refunded", nil
		}, core.RequireApproval())
	}
	p1 := New(1)
	rc, err := p1.Submit(ctx, s, core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c1", Args: `{"order_id":"1"}`}),
	), refund()), "the task")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(s.Pending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(s.Pending()) != 1 {
		t.Fatalf("parent pending %+v; want the child's refund mirrored", s.Pending())
	}
	// The restart: the old process lets go, a new pool knows the child.
	if err := p1.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	open, err := thread.Open(ctx, st, s.ID(), core.New(wefttest.Script(wefttest.Say("lead"))))
	if err != nil {
		t.Fatal(err)
	}
	p2 := New(1)
	var fwdTurn *thread.Turn
	var fwdErr error
	starts := 0
	p2.testHookStarting = func(d *delegate) {
		starts++
		if starts == 1 {
			fwdTurn, fwdErr = p2.Forward(ctx, open, d.receipt, core.User("hurry"))
		}
	}
	if err := p2.Register(rc.Child, core.New(wefttest.Script(wefttest.Say("done")), refund())); err != nil {
		t.Fatal(err)
	}
	if len(open.Pending()) != 1 {
		t.Fatalf("pending after the restart %+v", open.Pending())
	}
	if err := p2.Decide(ctx, open, thread.Approve(open.Pending()[0].CallID)); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if r, err := p2.Wait(wctx, open, rc.ID); err != nil || r.State != Done {
		t.Fatalf("Wait: %+v %v; want done", r, err)
	}
	var se *StateError
	if starts < 1 || fwdTurn != nil || !errors.As(fwdErr, &se) || se.State != Parked {
		t.Errorf("Forward in the restarted resume's start window (%d starts): turn %v, err %v; want a *StateError reading parked", starts, fwdTurn != nil, fwdErr)
	}
	if err := p2.Close(wctx); err != nil {
		t.Errorf("Close: %v", err)
	}
}
