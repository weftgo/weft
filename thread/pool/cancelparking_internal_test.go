package pool

import (
	"context"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// A Cancel made while the child is parking — its requests mirrored on
// the parent and its parked receipt written, its phase not yet parked
// — is a Cancel of a parked child to its caller: everything it can
// read says parked. It keeps that case's promise, the settlement
// recorded before it returns. Before the fix the pool took the child
// for a running one: Cancel asked the (already ended) run to stop and
// returned at once, the receipt still parked, and the park settled it
// canceled later (TestCancelParkedChildBesideARunningSibling on CI,
// "receipt when Cancel returned = {… State:parked …}, want canceled").
// Both paths: a Submit, and a sync wrap beside a sibling call still
// running in the parent's step.
func TestCancelWhileParkingSettlesBeforeReturning(t *testing.T) {
	for _, syncWrap := range []bool{false, true} {
		name := "submit"
		if syncWrap {
			name = "wrap"
		}
		t.Run(name, func(t *testing.T) { cancelWhileParking(t, syncWrap) })
	}
}

func cancelWhileParking(t *testing.T, syncWrap bool) {
	ctx := context.Background()
	refund := core.Tool("refund", "", func(_ context.Context, _ struct{ OrderID string }) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"A"}`}),
		wefttest.Say("child done"),
	), refund)
	p := New(2)
	inWindow := make(chan *delegate, 1)
	canceling := make(chan struct{})
	p.testHookParking = func(d *delegate) {
		// Hold the park in its window until the Cancel is made, and a
		// while after: long enough for a Cancel that does not wait for
		// the park to have returned.
		inWindow <- d
		<-canceling
		time.Sleep(200 * time.Millisecond)
	}
	release := make(chan struct{})
	var s *thread.Session
	var t1 *thread.Turn
	var receiptID string
	if syncWrap {
		slow := core.Tool("slow", "", func(ctx context.Context, _ struct{}) (string, error) {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return "slow done", nil
		})
		lead := core.New(wefttest.Script(
			wefttest.ToolCalls(
				wefttest.Call{Name: "a", ID: "la", Args: wefttest.Args(struct{ Prompt string }{"x"})},
				wefttest.Call{Name: "slow", ID: "ls", Args: `{}`},
			),
			wefttest.Say("lead done"),
		), p.MustWrap("a", "", child), slow, core.Timeout(0))
		s, _ = thread.Create(ctx, thread.Memory(), lead)
		t1, _ = s.Send(ctx, core.User("go"))
	} else {
		s, _ = thread.Create(ctx, thread.Memory(), core.New(wefttest.Script()))
		r, err := p.Submit(ctx, s, child, "refund A")
		if err != nil {
			t.Fatal(err)
		}
		receiptID = r.ID
	}
	var d *delegate
	select {
	case d = <-inWindow:
	case <-time.After(5 * time.Second):
		t.Fatal("the child never reached its park")
	}
	if syncWrap {
		receiptID = d.receipt
	}
	if rc, _ := lookup(s, receiptID); rc.State != Parked {
		t.Fatalf("receipt in the parking window = %+v, want parked", rc)
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	close(canceling)
	if err := p.Cancel(cctx, s, receiptID); err != nil {
		t.Errorf("Cancel: %v", err)
	}
	if rc, _ := lookup(s, receiptID); rc.State != Canceled {
		t.Errorf("receipt when Cancel returned = %+v, want canceled", rc)
	}
	if n := len(s.Pending()); n != 0 && !syncWrap {
		t.Errorf("parent Pending after the Cancel = %d, want 0", n)
	}
	close(release)
	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()
	if rc, err := p.Wait(wctx, s, receiptID); err != nil || rc.State != Canceled {
		t.Errorf("Wait = %+v, %v; want canceled", rc, err)
	}
	if t1 != nil {
		res, err := t1.Wait()
		for err == nil && res != nil && len(res.Pending) > 0 && t1.Next() != nil {
			t1 = t1.Next()
			res, err = t1.Wait()
		}
		if err != nil || res.Text() != "lead done" {
			t.Errorf("lead = %v, %v", res, err)
		}
	}
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
