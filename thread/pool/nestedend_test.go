package pool_test

import (
	"context"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
)

// siblingChain builds lead → mid → {slow, repark}, every delegation a
// sync wrap of p, and parks it: slow's refund blocks once approved
// until release (or its context) ends it, and repark asks a second
// approval after its first. The lead parks on both grandchildren's
// first requests.
func siblingChain(t *testing.T, p *pool.Pool, st thread.Storage, started chan<- struct{}, release <-chan struct{}) (*thread.Session, *thread.Turn, *flags) {
	t.Helper()
	ctx := context.Background()
	slowTool := core.Tool("refund", "", func(ctx context.Context, in struct{ OrderID string }) (string, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
			return "refunded " + in.OrderID, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}, core.RequireApproval())
	slow := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"S"}`}),
		wefttest.Say("slow: done"),
	), slowTool)
	repark, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"R1"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"R2"}`}),
		wefttest.Say("repark: done"),
	)
	mid := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "slow", ID: "ms", Args: wefttest.Args(struct{ Prompt string }{"s"})},
			wefttest.Call{Name: "repark", ID: "mr", Args: wefttest.Args(struct{ Prompt string }{"r"})},
		),
		wefttest.Say("mid: done"),
	), p.MustWrap("slow", "", slow), p.MustWrap("repark", "", repark))
	lead := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "mid", ID: "lm", Args: wefttest.Args(struct{ Prompt string }{"m"})}),
		wefttest.Say("lead: done"),
	), p.MustWrap("mid", "", mid))
	s, _ := thread.Create(ctx, st, lead)
	t1, err := s.Send(ctx, core.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := t1.Wait(); err != nil || len(res.Pending) != 1 || len(s.Pending()) != 2 {
		t.Fatalf("the lead's first turn = %+v, %v; Pending %+v", res, err, s.Pending())
	}
	return s, t1, ran
}

// A grandchild that parks again while a sibling grandchild still runs
// does not park the middle child: the middle's boundary is not final
// while one of its delegations works, and parking it then left that
// sibling running under a parked child — a Cancel or Close of the
// middle closed its session under the sibling (its receipt read
// running for good, its settlement "not recorded: session is closed"),
// and the re-parking grandchild was resumed with the cancel's denial.
// Now the middle waits on, and parks only once every delegation left
// is parked; a Cancel or Close meanwhile ends both grandchildren first.
func TestNestedReparkWaitsForTheRunningSibling(t *testing.T) {
	for _, end := range []string{"Cancel", "Close", "SiblingFinishes"} {
		t.Run(end, func(t *testing.T) {
			ctx := context.Background()
			st := thread.Memory()
			p := pool.New(3)
			defer func() { _ = p.Close(ctx) }()
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			s, t1, ran := siblingChain(t, p, st, started, release)
			var ds []thread.Decision
			for _, r := range s.Pending() {
				ds = append(ds, thread.Approve(r.CallID))
			}
			if err := p.Decide(ctx, s, ds...); err != nil {
				t.Fatal(err)
			}
			<-started
			rc := pool.Receipts(s)[0]
			// The re-parking grandchild's second request lands in the
			// middle's session while slow still runs.
			deadline := time.Now().Add(5 * time.Second)
			for len(mustOpen(ctx, t, st, rc.Child).Pending()) == 0 && time.Now().Before(deadline) {
				time.Sleep(2 * time.Millisecond)
			}
			if n := len(mustOpen(ctx, t, st, rc.Child).Pending()); n != 1 {
				t.Fatalf("the middle's Pending holds %d requests, want repark's second", n)
			}
			time.Sleep(50 * time.Millisecond) // a broken middle parks in here
			if got := pool.Receipts(s)[0]; got.State != pool.Running || len(s.Pending()) != 0 {
				t.Fatalf("the middle parked beside its running sibling: receipt %+v, lead Pending %+v", got, s.Pending())
			}
			switch end {
			case "Cancel":
				if err := p.Cancel(ctx, s, rc.ID); err != nil {
					t.Fatal(err)
				}
				waitFor(t, p, s, rc.ID, pool.Canceled)
			case "Close":
				within(t, 10*time.Second, "Close", func() {
					if err := p.Close(ctx); err != nil {
						t.Errorf("Close: %v", err)
					}
				})
			case "SiblingFinishes":
				close(release)
				waitFor(t, p, s, rc.ID, pool.Parked)
				if pend := s.Pending(); len(pend) != 1 || pend[0].Tool != "refund" {
					t.Fatalf("lead Pending = %+v, want repark's second request", pend)
				}
				if err := p.Decide(ctx, s, thread.Approve(s.Pending()[0].CallID)); err != nil {
					t.Fatal(err)
				}
				if got := waitFor(t, p, s, rc.ID, pool.Done); got.Stop != "mid: done" {
					t.Errorf("mid receipt = %+v", got)
				}
				if res, err := continuation(t, t1).Wait(); err != nil || res.Text() != "lead: done" {
					t.Errorf("lead continuation = %v, %v", res, err)
				}
				if got := ran.snapshot(); len(got) != 2 {
					t.Errorf("repark's refunds ran %v, want R1 and R2", got)
				}
				return
			}
			for _, g := range pool.Receipts(mustOpen(ctx, t, st, rc.Child)) {
				switch {
				case g.State == pool.Canceled:
				case end == "Close" && g.State == pool.Parked: // Close leaves a parked one parked
				default:
					t.Errorf("grandchild receipt %s reads %s (%s) after the middle ended", g.ID, g.State, g.Stop)
				}
			}
			if got := ran.snapshot(); len(got) != 1 {
				t.Errorf("repark's refunds ran %v, want R1 alone: R2 was never approved", got)
			}
			if end == "Cancel" {
				res, err := continuation(t, t1).Wait()
				if err != nil || res.Text() != "lead: done" {
					t.Fatalf("lead continuation = %v, %v", res, err)
				}
				want := `SUBAGENT_CANCELED: agent "mid" was canceled before it finished`
				if got := toolResultsOf(s.Context()); len(got) != 1 || got[0] != want {
					t.Errorf("lead tool results = %v, want %q", got, want)
				}
			}
		})
	}
}

// Cancel of a middle child parked on its grandchild's request ends the
// grandchild too, before Cancel returns: the grandchild is canceled at
// its boundary and settles canceled in the middle's ledger, its
// request denied, its call never run; the middle settles canceled and
// the lead reads SUBAGENT_CANCELED. Before the fix the denial of the
// middle's boundary re-armed the grandchild, whose resume then failed
// into the middle's closed session ("running receipt not recorded"):
// the middle's ledger read the grandchild parked for good.
func TestNestedCancelParkedMiddleEndsTheGrandchild(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(2)
	defer func() { _ = p.Close(ctx) }()
	lead, ran := twoLevel(t, p, "payments: refunded")
	s, _ := thread.Create(ctx, st, lead)
	t1, _ := parkTwoLevel(t, s)
	rc := pool.Receipts(s)[0]
	if err := p.Cancel(ctx, s, rc.ID); err != nil {
		t.Fatal(err)
	}
	if got := pool.Receipts(s)[0]; got.State != pool.Canceled {
		t.Errorf("finance receipt = %+v, want canceled", got)
	}
	mid := mustOpen(ctx, t, st, rc.Child)
	gs := pool.Receipts(mid)
	if len(gs) != 1 || gs[0].State != pool.Canceled {
		t.Fatalf("payments receipt in the finance ledger = %+v, want canceled when Cancel returns", gs)
	}
	time.Sleep(50 * time.Millisecond) // a broken pump re-arms the grandchild in here
	if gs := pool.Receipts(mustOpen(ctx, t, st, rc.Child)); gs[0].State != pool.Canceled || len(gs) != 1 {
		t.Errorf("payments receipt moved after the cancel: %+v", gs)
	}
	if pend := mustOpen(ctx, t, st, gs[0].Child).Pending(); len(pend) != 0 {
		t.Errorf("payments still asks %+v", pend)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Errorf("payments' refund ran %v after the cancel", got)
	}
	res, err := continuation(t, t1).Wait()
	if err != nil || res.Text() != "lead: all done" {
		t.Fatalf("lead continuation = %v, %v", res, err)
	}
	want := `SUBAGENT_CANCELED: agent "finance" was canceled before it finished`
	if got := toolResultsOf(s.Context()); len(got) != 1 || got[0] != want {
		t.Errorf("lead tool results = %v, want %q", got, want)
	}
}

// Ending a middle child ends its sync delegations only: an async child
// it submitted (an Async wrap) follows D4 — it is the pool's work, not
// the middle's run's — and runs to its end. Before the fix the cancel
// of the middle's resume canceled the async child with the sync
// grandchild.
func TestNestedEndLeavesAsyncChildren(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(3)
	defer func() { _ = p.Close(ctx) }()
	bgStarted := make(chan struct{})
	bgRelease := make(chan struct{})
	bg := core.New(blocking{release: bgRelease, text: "bg: done", onStart: func() { close(bgStarted) }})
	started := make(chan struct{}, 1)
	finance := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "bg", ID: "fb", Args: wefttest.Args(struct{ Prompt string }{"in the background"})},
			wefttest.Call{Name: "pay", ID: "fp", Args: wefttest.Args(struct{ Prompt string }{"refund B-7"})},
		),
		wefttest.Say("finance: settled"),
	), p.MustWrap("bg", "", bg, pool.Async()), p.MustWrap("pay", "", blockingPayments(started)))
	lead := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "finance", ID: "cf", Args: wefttest.Args(struct{ Prompt string }{"settle"})}),
		wefttest.Say("lead: done"),
	), p.MustWrap("finance", "", finance))
	s, _ := thread.Create(ctx, st, lead)
	parkLead(t, s)
	<-bgStarted
	if err := p.Decide(ctx, s, thread.Approve(s.Pending()[0].CallID)); err != nil {
		t.Fatal(err)
	}
	<-started
	rc := pool.Receipts(s)[0]
	if err := p.Cancel(ctx, s, rc.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, p, s, rc.ID, pool.Canceled)
	var bgChild string
	for _, g := range pool.Receipts(mustOpen(ctx, t, st, rc.Child)) {
		if g.Call == "fb" {
			bgChild = g.Child
		} else if g.State != pool.Canceled {
			t.Errorf("pay receipt = %+v, want canceled", g)
		}
	}
	close(bgRelease)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if text := lastAssistantText(mustOpen(ctx, t, st, bgChild).Context()); text == "bg: done" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the async child did not run to its end: its context %+v", mustOpen(ctx, t, st, bgChild).Context())
}

// lastAssistantText is the text of the last assistant message in msgs.
func lastAssistantText(msgs []core.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != core.RoleAssistant {
			continue
		}
		for _, part := range msgs[i].Content {
			if tp, ok := part.(core.TextPart); ok {
				return tp.Text
			}
		}
	}
	return ""
}
