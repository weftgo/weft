package pool_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
	"github.com/weftgo/weft/wefttest"
)

// firingChild is a child agent with one side-effect tool, "fire", that
// counts its runs and carries no approval rule of its own: only a park
// rule the child inherits can park it.
func firingChild(fired *atomic.Int32, turns ...wefttest.Turn) *weft.Agent {
	fire := weft.Tool("fire", "a side effect", func(context.Context, struct{}) (string, error) {
		fired.Add(1)
		return "fired", nil
	})
	return weft.New(wefttest.Script(turns...), fire)
}

// An async child runs on the pool's context — it outlives the
// delegating turn — yet the delegating run's ParkAllExcept still binds
// it: its side-effect call parks (before the fix it ran unparked).
func TestAsyncChildInheritsParkRule(t *testing.T) {
	ctx := context.Background()
	p := pool.New(2)
	defer func() { _ = p.Close(ctx) }()
	var fired atomic.Int32
	child := firingChild(&fired,
		wefttest.ToolCalls(wefttest.Call{Name: "fire", ID: "c-fire"}),
		wefttest.Say("fired"),
	)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", ID: "c-wrapper", Args: `{"prompt":"fire"}`}),
		wefttest.Say("submitted"),
	), p.MustWrap("research", "", child, pool.Async()))
	s, err := thread.Create(ctx, thread.Memory(), parent)
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("go"), thread.RunOptions(weft.ParkAllExcept("research")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	rs := pool.Receipts(s)
	if len(rs) != 1 {
		t.Fatalf("receipts = %+v, want one", rs)
	}
	waitFor(t, p, s, rs[0].ID, pool.Parked)
	if n := fired.Load(); n != 0 {
		t.Errorf("the async child's fire ran %d times, want it parked by the delegating run's ParkAllExcept", n)
	}
}

// A sync child parked under the delegating run's ParkAllExcept and
// resumed through Pool.Decide runs on the pool's context: its resumed
// steps still carry the rule — the approved call runs once, the next
// side-effect call parks again (before the fix it ran unparked).
func TestResumedSyncChildKeepsParkRule(t *testing.T) {
	ctx := context.Background()
	p := pool.New(2)
	defer func() { _ = p.Close(ctx) }()
	var fired atomic.Int32
	child := firingChild(&fired,
		wefttest.ToolCalls(wefttest.Call{Name: "fire", ID: "c-1"}),
		wefttest.ToolCalls(wefttest.Call{Name: "fire", ID: "c-2"}),
		wefttest.Say("fired twice"),
	)
	s, err := thread.Create(ctx, thread.Memory(), delegating(p, child))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("go"), thread.RunOptions(weft.ParkAllExcept("research")))
	if err != nil {
		t.Fatal(err)
	}
	res, err := t1.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 {
		t.Fatalf("parent parked %d calls, want the delegating call", len(res.Pending))
	}
	rc := pool.Receipts(s)[0]
	pend := s.Pending()
	if len(pend) != 1 || pend[0].CallID != rc.Child+"/c-1" {
		t.Fatalf("Pending = %+v, want the child's c-1", pend)
	}
	if err := p.Decide(ctx, s, thread.Approve(pend[0].CallID)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, s, rc.ID, pool.Parked)
	if n := fired.Load(); n != 1 {
		t.Errorf("fire ran %d times, want once (the approved c-1) and c-2 parked by the inherited rule", n)
	}
	if pend := s.Pending(); len(pend) != 1 || pend[0].CallID != rc.Child+"/c-2" {
		t.Errorf("Pending after the resume = %+v, want the child's c-2", pend)
	}
}
