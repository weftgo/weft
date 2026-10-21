package pool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// A decision recorded while a child is parking (its parked receipt
// written, its phase not yet parked) meets a pump that leaves the arm
// to the park's own re-read; a Close in that window used to make the
// park leave the child parked on a boundary already decided — Decide
// had returned, the resume as good as queued, yet nothing armed it and
// Close returned at once, the parent's delegating call parked until a
// Recover (TestCloseResolvesACanceledResumeOnceTheParentParks under
// make stress, GOMAXPROCS=1). Close cancels that resume as it cancels
// a queued one: the delegation settles canceled, and a first run's
// delegating call reads SUBAGENT_CANCELED.
func TestCloseWhileParkingOnADecidedBoundary(t *testing.T) {
	for _, syncWrap := range []bool{false, true} {
		name := "submit"
		if syncWrap {
			name = "wrap"
		}
		t.Run(name, func(t *testing.T) { closeWhileParkingDecided(t, syncWrap) })
	}
}

func closeWhileParkingDecided(t *testing.T, syncWrap bool) {
	ctx := context.Background()
	p := New(1)
	refund := core.Tool("refund", "", func(context.Context, struct{ OrderID string }) (string, error) {
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
	parent, err := thread.Create(ctx, thread.Memory(), parentAgent)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	hooked := false
	p.testHookParking = func(d *delegate) {
		if hooked {
			return
		}
		hooked = true
		pending := parent.Pending()
		if len(pending) != 1 {
			t.Errorf("mirrors while parking = %+v", pending)
			return
		}
		// Decide inside the window: recorded, its pump finds the child
		// still running and leaves it to this park.
		if err := p.Decide(ctx, parent, thread.Approve(pending[0].CallID)); err != nil {
			t.Errorf("Decide inside the window: %v", err)
		}
		go func() { closed <- p.Close(ctx) }()
		deadline := time.Now().Add(5 * time.Second)
		for {
			p.mu.Lock()
			c := p.closed
			p.mu.Unlock()
			if c || time.Now().After(deadline) {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	var res *core.RunResult
	if syncWrap {
		turn, err := parent.Send(ctx, core.User("refund order 1"))
		if err != nil {
			t.Fatal(err)
		}
		if res, err = turn.Wait(); err != nil {
			t.Fatalf("the parent's turn: %v", err)
		}
	} else if _, err := p.Submit(ctx, parent, child, "refund order 1"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close never returned")
	}
	rs := Receipts(parent)
	if len(rs) != 1 || rs[0].State != Canceled {
		t.Fatalf("receipts after Close = %+v; want the decided child canceled, not left parked", rs)
	}
	if syncWrap {
		if len(res.Pending) != 0 || res.Text() != "all done" {
			t.Fatalf("the parent's turn ended %q with %d pending; want the delegating call answered", res.Text(), len(res.Pending))
		}
		var got string
		for _, m := range parent.Context() {
			for _, part := range m.Content {
				if tp, ok := part.(core.ToolResultPart); ok {
					got = tp.Content
				}
			}
		}
		if !strings.HasPrefix(got, "SUBAGENT_CANCELED") {
			t.Errorf("the delegating call's result = %q, want SUBAGENT_CANCELED", got)
		}
	}
}
