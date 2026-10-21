package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// A Forward landing between a run's running receipt and its running
// mark (testHookRecorded stands in for the scheduler) is refused as
// the state the run is leaving — accepted before the first turn,
// parked before a resume. On v0.18.2 the phase still read queued
// there, Forward fell through to the ledger, which already read
// running, and refused with "receipt … is running, not running" (the
// arena's pool18.restart under full-suite load).
func TestForwardBetweenTheRunningReceiptAndTheMarkReadsTheStateItLeaves(t *testing.T) {
	ctx := context.Background()
	p := New(1)
	s, err := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script(wefttest.Say("lead"))))
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
	type got struct {
		turn *thread.Turn
		err  error
	}
	var seen []got
	p.testHookRecorded = func(d *delegate) {
		turn, err := p.Forward(ctx, s, d.receipt, core.User("hurry"))
		seen = append(seen, got{turn, err})
	}
	rc, err := p.Submit(ctx, s, child, "the task")
	if err != nil {
		t.Fatal(err)
	}
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
	if r, err := p.Wait(wctx, s, rc.ID); err != nil || r.State != Done {
		t.Fatalf("Wait: %+v %v; want done", r, err)
	}
	if len(seen) != 2 {
		t.Fatalf("%d runs recorded running; want the first run and the resume", len(seen))
	}
	for i, want := range []State{Accepted, Parked} {
		var se *StateError
		if seen[i].turn != nil || !errors.As(seen[i].err, &se) || se.State != want || se.Orphan {
			t.Errorf("Forward after run %d's running receipt: turn %v, err %v; want a *StateError reading %s", i+1, seen[i].turn != nil, seen[i].err, want)
		}
	}
	if err := p.Close(wctx); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// A Forward landing while the delegation settles — the phase settled,
// the settlement receipt not yet written (testHookSettling) — names
// the state the receipt is settling to. On v0.18.2 it read the ledger
// a step behind: "receipt … is running, not running".
func TestForwardWhileSettlingReadsTheSettlement(t *testing.T) {
	ctx := context.Background()
	p := New(1)
	s, err := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script(wefttest.Say("lead"))))
	if err != nil {
		t.Fatal(err)
	}
	var fwdTurn *thread.Turn
	var fwdErr error
	p.testHookSettling = func(d *delegate) {
		fwdTurn, fwdErr = p.Forward(ctx, s, d.receipt, core.User("hurry"))
	}
	rc, err := p.Submit(ctx, s, core.New(wefttest.Script(wefttest.Say("done"))), "the task")
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if r, err := p.Wait(wctx, s, rc.ID); err != nil || r.State != Done {
		t.Fatalf("Wait: %+v %v; want done", r, err)
	}
	var se *StateError
	if fwdTurn != nil || !errors.As(fwdErr, &se) || se.State != Done || se.Orphan {
		t.Errorf("Forward while settling: turn %v, err %v; want a *StateError reading done", fwdTurn != nil, fwdErr)
	}
	if err := p.Close(wctx); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// A Forward to a parked child is refused as parked even when a
// decision resumes the child the moment Forward has read its phase:
// the refusal is the state read under the pool's lock. On v0.18.2 the
// state came from a ledger read after the lock was let go
// (testHookLedgerRead stands in for the scheduler), and the resume's
// running receipt made it "receipt … is running, not running"
// (TestNestedRandomChains with a retried Forward under make stress).
func TestForwardToAParkedChildResumingMeanwhileReadsParked(t *testing.T) {
	ctx := context.Background()
	p := New(1)
	s, err := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script(wefttest.Say("lead"))))
	if err != nil {
		t.Fatal(err)
	}
	refund := core.Tool("refund", "", func(context.Context, struct{ OrderID string }) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	gate := make(chan struct{})
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c1", Args: `{"order_id":"1"}`}),
		wefttest.Say("done"),
	), refund)
	rc, err := p.Submit(ctx, s, child, "the task")
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if r, err := p.Wait(wctx, s, rc.ID); err != nil || r.State != Parked {
		t.Fatalf("Wait: %+v %v; want parked", r, err)
	}
	// The resume holds at its start (after its running receipt) until
	// the Forward has answered.
	p.testHookStarting = func(*delegate) { <-gate }
	p.testHookLedgerRead = func(string) {
		if err := p.Decide(ctx, s, thread.Approve(s.Pending()[0].CallID)); err != nil {
			t.Error(err)
			return
		}
		for dl := time.Now().Add(5 * time.Second); time.Now().Before(dl); {
			if rs := Receipts(s); len(rs) == 1 && rs[0].State == Running {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	turn, err := p.Forward(ctx, s, rc.ID, core.User("hurry"))
	p.testHookLedgerRead = nil
	var se *StateError
	if turn != nil || !errors.As(err, &se) || se.State != Parked || se.Orphan {
		t.Errorf("Forward to a parked child: turn %v, err %v; want a *StateError reading parked", turn != nil, err)
	}
	if len(s.Pending()) == 1 { // the fix answered from the lock: decide now
		if err := p.Decide(ctx, s, thread.Approve(s.Pending()[0].CallID)); err != nil {
			t.Fatal(err)
		}
	}
	close(gate)
	if r, err := p.Wait(wctx, s, rc.ID); err != nil || r.State != Done {
		t.Fatalf("Wait: %+v %v; want done", r, err)
	}
	if err := p.Close(wctx); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// steerSeeker is the arena's racer: it calls watch until a request
// carries the steer, then answers.
type steerSeeker struct{}

func (steerSeeker) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		for _, m := range req.Messages {
			if m.Role == core.RoleUser && strings.Contains(m.Text(), "also watch ap-south") {
				if yield(core.ModelTextDelta{Text: "watching both"}, nil) {
					yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
				}
				return
			}
		}
		if yield(core.ModelToolCall{ID: fmt.Sprintf("w%d", len(req.Messages)), Name: "watch", Args: json.RawMessage(`{}`)}, nil) {
			yield(core.ModelFinish{Reason: core.StopToolCalls}, nil)
		}
	}
}

// Forward retried from the moment an async delegation's receipt exists
// (the arena's pool18.restart shape) is refused only as accepted until
// the child's turn is in flight, then steers it; the child settles
// done on the steered answer. POOL_FORWARD_RETRY_ITERS for a longer run.
func TestForwardRetriedFromTheReceiptSteers(t *testing.T) {
	iters := 50
	if v, err := strconv.Atoi(os.Getenv("POOL_FORWARD_RETRY_ITERS")); err == nil {
		iters = v
	}
	for i := 0; i < iters; i++ {
		forwardRetryOnce(t)
		if t.Failed() {
			t.Fatalf("iteration %d", i)
		}
	}
}

func forwardRetryOnce(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	p := New(2)
	s, err := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script(wefttest.Say("lead"))))
	if err != nil {
		t.Fatal(err)
	}
	watch := core.Tool("watch", "", func(context.Context, struct{}) (string, error) {
		return "0.1% errors", nil
	}, core.Replay(core.ReplaySafe))
	child := core.New(steerSeeker{}, watch, core.MaxSteps(1000))
	rc, err := p.Submit(ctx, s, child, "watch eu-north")
	if err != nil {
		t.Fatal(err)
	}
	landed := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, err := p.Forward(ctx, s, rc.ID, core.User("also watch ap-south"))
		if err == nil {
			landed = true
			break
		}
		var se *StateError
		if errors.As(err, &se) && se.State == Accepted {
			continue
		}
		t.Errorf("Forward refused: %v; want only accepted before the turn is in flight", err)
		break
	}
	if !landed && !t.Failed() {
		t.Error("Forward never steered the child")
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r, err := p.Wait(wctx, s, rc.ID)
	if landed && (err != nil || r.State != Done || r.Stop != "watching both") {
		t.Errorf("Wait: %+v %v; want done on the steered answer", r, err)
	}
	if err := p.Close(wctx); err != nil {
		t.Errorf("Close: %v", err)
	}
	_ = s.Close(wctx)
}

// A Forward landing while the child parks — the parked receipt
// written, the phase not yet parked (testHookParking) — is either
// refused as parked or steers the run that is parking (its steer
// deferred to a follow-up turn, ADR 0019 §7); never refused as
// running. Either way the decision resumes the child and it settles.
func TestForwardWhileParkingNeverReadsRunning(t *testing.T) {
	ctx := context.Background()
	p := New(1)
	s, err := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script(wefttest.Say("lead"))))
	if err != nil {
		t.Fatal(err)
	}
	refund := core.Tool("refund", "", func(context.Context, struct{ OrderID string }) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c1", Args: `{"order_id":"1"}`}),
		wefttest.Say("done"),
		wefttest.Say("steered"),
	), refund)
	var fwdTurn *thread.Turn
	var fwdErr error
	parks := 0
	forwarded := make(chan struct{})
	p.testHookParking = func(d *delegate) {
		parks++
		if parks == 1 {
			fwdTurn, fwdErr = p.Forward(ctx, s, d.receipt, core.User("hurry"))
			close(forwarded)
		}
	}
	rc, err := p.Submit(ctx, s, child, "the task")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(s.Pending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(s.Pending()) != 1 {
		t.Fatalf("parent pending %+v, receipts %+v; want the child's refund mirrored", s.Pending(), Receipts(s))
	}
	<-forwarded
	var se *StateError
	if fwdErr != nil && (!errors.As(fwdErr, &se) || se.State != Parked) || fwdErr == nil && fwdTurn == nil {
		t.Errorf("Forward while parking: turn %v, err %v; want a steer or a refusal reading parked", fwdTurn != nil, fwdErr)
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
	if r.Stop != "done" {
		t.Errorf("the receipt's answer %q; want the steered run's own (done)", r.Stop)
	}
	if err := p.Close(wctx); err != nil {
		t.Errorf("Close: %v", err)
	}
}
