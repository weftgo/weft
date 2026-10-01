package pool

import (
	"context"
	"iter"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// settle is idempotent per receipt: a second settlement — the panic
// handler's after the run's own, a Cancel racing the run's end — is
// not recorded, and the parent's Delegated bucket counts the child
// once. It used to append a second settlement and bill twice.
func TestSettleOnce(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := New(1)
	parent, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
	gate := make(chan struct{})
	r, err := p.Submit(ctx, parent, weft.New(stubModel{gate: gate}), "go")
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
	child := weft.New(stubModel{})
	parent, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
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

// stubModel answers every request with one line — a model any number
// of sessions can share — once its gate, when it has one, is open.
type stubModel struct{ gate chan struct{} }

func (m stubModel) Stream(ctx context.Context, _ weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
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
		if !yield(weft.ModelTextDelta{Text: "ok"}, nil) {
			return
		}
		yield(weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}}, nil)
	}
}
