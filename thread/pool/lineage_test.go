package pool_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
)

// Deleting a parent deletes no child; Children and Descendants are
// how an application cascades — deepest first — and a fork, whose
// ledger is a copy, lists none of its origin's children.
func TestChildrenAndDescendants(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(2)
	tool, _, _ := tree(p, nil, 3, 2) // parent → 2 × level1 → 2 × level2 → 2 × level3
	s, err := thread.Create(ctx, st, weft.New(fanOut(nil, "level1", 2), tool))
	if err != nil {
		t.Fatal(err)
	}
	turn, _ := s.Send(ctx, weft.User("go"))
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}

	children, err := pool.Children(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	rs := pool.Receipts(s)
	if len(children) != 2 || children[0] != rs[0].Child || children[1] != rs[1].Child {
		t.Fatalf("Children = %v, want the two receipts' children in acceptance order (%+v)", children, rs)
	}
	all, err := pool.Descendants(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2+4+8 {
		t.Fatalf("Descendants = %d sessions, want 14", len(all))
	}
	if n := len(mustList(ctx, t, st)); n != 15 {
		t.Fatalf("%d sessions stored, want the parent and its 14 descendants", n)
	}
	// Deepest first: every session comes after all of its own
	// children, so the lineage each header names is still there when
	// the session is reached.
	at := map[string]int{}
	for i, id := range all {
		at[id] = i
	}
	for _, id := range all {
		h, _, _, err := st.Load(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if parentAt, ok := at[h.Lineage.Session]; ok && parentAt < at[id] {
			t.Errorf("session %s is listed after its parent %s", id, h.Lineage.Session)
		}
	}

	// A fork copies the receipts; the children stay the origin's.
	fork, err := s.Fork(ctx, s.Leaf())
	if err != nil {
		t.Fatal(err)
	}
	if len(pool.Receipts(fork)) != 2 {
		t.Fatalf("the fork's receipts = %+v, want the copied ledger", pool.Receipts(fork))
	}
	if got, err := pool.Children(ctx, fork); err != nil || len(got) != 0 {
		t.Errorf("Children of the fork = %v, %v; a cascade over it would delete the origin's children", got, err)
	}
	if got, err := pool.Descendants(ctx, fork); err != nil || len(got) != 0 {
		t.Errorf("Descendants of the fork = %v, %v", got, err)
	}

	// Delete(parent) alone orphans every one of them…
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := fork.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := thread.Delete(ctx, st, fork.ID()); err != nil {
		t.Fatal(err)
	}
	// …so the application cascades, in the order given, parent last.
	for _, id := range all {
		if err := thread.Delete(ctx, st, id); err != nil {
			t.Fatalf("Delete %s: %v", id, err)
		}
	}
	if err := thread.Delete(ctx, st, s.ID()); err != nil {
		t.Fatal(err)
	}
	if left := mustList(ctx, t, st); len(left) != 0 {
		t.Errorf("%d sessions left after the cascade", len(left))
	}
	// A deleted child is simply no longer listed.
	if got, err := pool.Children(ctx, s); err != nil || len(got) != 0 {
		t.Errorf("Children after the cascade = %v, %v", got, err)
	}
	if _, err := pool.Children(ctx, nil); err == nil {
		t.Error("Children(nil): no error")
	}
}

// What Delete(parent) alone leaves: the children, each still naming
// the parent that is gone.
func TestDeleteParentOrphansChildren(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(1)
	s, _ := thread.Create(ctx, st, weft.New(say(nil, "parent")))
	var kids []string
	for i := 0; i < 2; i++ {
		r, err := p.Submit(ctx, s, weft.New(say(nil, fmt.Sprintf("child %d", i))), "go")
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, p, s, r.ID, pool.Done)
		kids = append(kids, r.Child)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := thread.Delete(ctx, st, s.ID()); err != nil {
		t.Fatal(err)
	}
	for _, id := range kids {
		h, _, _, err := st.Load(ctx, id)
		if err != nil {
			t.Fatalf("child %s after Delete(parent): %v", id, err)
		}
		if h.Lineage == nil || h.Lineage.Session != s.ID() {
			t.Errorf("child %s lineage = %+v", id, h.Lineage)
		}
	}
	if _, _, _, err := st.Load(ctx, s.ID()); !errors.Is(err, thread.ErrNotFound) {
		t.Errorf("the parent after Delete: %v", err)
	}
}
