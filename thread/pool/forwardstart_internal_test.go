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
