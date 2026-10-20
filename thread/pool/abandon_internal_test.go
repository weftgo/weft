package pool

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// A grandchild run endNested's bound abandoned (settled canceled by the
// pool) that comes to another approval boundary when its handler
// finally returns records nothing more: no second settlement in the
// middle's ledger, no park into the closed session, no error logged.
func TestNestedAbandonedRunParkingLaterWritesNothing(t *testing.T) {
	ctx := context.Background()
	p := New(2)
	shortenNestedEnd(p, 200*time.Millisecond)
	logs := &records{}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	stuck := core.Tool("refund", "", func(context.Context, struct{ OrderID string }) (string, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release // ignores its context
		return "refunded late", nil
	}, core.RequireApproval())
	payments := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"B-7"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"B-8"}`}),
		wefttest.Say("payments: never"),
	), stuck, core.Logger(slog.New(logs)))
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, chainOver(p, payments, nil))
	t1 := parkChain(t, s)
	if err := p.Decide(ctx, s, thread.Approve(s.Pending()[0].CallID)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the approved refund never ran")
	}
	fin := Receipts(s)[0]
	if err := p.Cancel(ctx, s, fin.ID); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if rc, err := p.Wait(wctx, s, fin.ID); err != nil || rc.State != Canceled {
		t.Fatalf("finance receipt = %+v, %v; want canceled", rc, err)
	}
	mid, err := thread.Open(ctx, st, fin.Child, core.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	g := Receipts(mid)
	if len(g) != 1 || g[0].State != Canceled || !strings.Contains(g[0].Stop, "did not end within 200ms") {
		t.Fatalf("payments receipt in the finance ledger = %+v, want canceled by the pool past the bound", g)
	}
	if res, err := t1.Next().Wait(); err != nil || res.Text() != "lead: done" {
		t.Fatalf("lead continuation = %v, %v", res, err)
	}
	close(release) // the abandoned run ends now
	cctx, ccancel := context.WithTimeout(ctx, 10*time.Second)
	defer ccancel()
	if err := p.Close(cctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	mid, _ = thread.Open(ctx, st, fin.Child, core.New(wefttest.Script()))
	settlements := 0
	for _, e := range mid.Entries() {
		if pr, ok := e.(thread.PoolReceiptEntry); ok && pr.Receipt == g[0].ID && State(pr.Status).Settled() {
			settlements++
		}
	}
	if settlements != 1 {
		t.Errorf("%d settlements recorded for the payments receipt, want the pool's one", settlements)
	}
	for _, l := range logs.snapshot() {
		t.Logf("log: %s", l)
		if strings.Contains(l, "not recorded") || strings.Contains(l, "closed") || strings.Contains(l, "ERROR") {
			t.Errorf("the abandoned run wrote into the closed session: %s", l)
		}
	}
}
