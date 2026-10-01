package pool_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
	"github.com/weftgo/weft/wefttest"
)

// The pool's SUBAGENT_FAILED is the core's, byte for byte: the same
// failing child reads the same to a parent model whether it ran as a
// bare weft.Subagent or as a pool child session. The core's string is
// not exported, so this test is what holds the two copies together.
func TestFailureTextMatchesCore(t *testing.T) {
	ctx := context.Background()
	failing := func() *weft.Agent {
		return weft.New(wefttest.Script(wefttest.SayThenFail("partial", errors.New("connection reset"))))
	}
	script := func() *wefttest.Model {
		return wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "research", ID: "c-research", Args: `{"prompt":"go"}`}),
			wefttest.Say("noted"),
		)
	}
	bare, err := weft.New(script(), weft.Subagent("research", "", failing())).Generate(ctx, weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	p := pool.New(1)
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(script(), p.MustWrap("research", "", failing())))
	turn, err := s.Send(ctx, weft.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	pooled, err := turn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	const want = `SUBAGENT_FAILED: agent "research" failed at step 0: model stream: connection reset`
	if got := lastToolResult(bare.Messages); got != want {
		t.Errorf("the core's text moved:\n got %q\nwant %q", got, want)
	}
	if got := lastToolResult(pooled.Messages); got != want {
		t.Errorf("the pool's text:\n got %q\nwant %q", got, want)
	}
}

// Every text the pool puts in front of a model is pinned (AGENTS.md
// rule 5; ADR 0022's amendment lists them). The ones with a test of
// their own — the receipt line (TestWrapAsyncGolden), SUBAGENT_CYCLE
// (TestCycle), SUBAGENT_DEPTH (TestDepthLimit), SUBAGENT_CANCELED
// (TestCancelParked, TestCancelRunningSyncChild), the unmirrored
// park (TestMirrorFailureFailsDelegation), the closed pool
// (TestCloseCoversEverything) — are not repeated here; the rest are.
func TestModelVisibleTexts(t *testing.T) {
	ctx := context.Background()

	t.Run("arguments that do not decode", func(t *testing.T) {
		p := pool.New(1)
		parent := weft.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "research", ID: "c-research", Args: `{"prompt":42}`}),
			wefttest.Say("noted"),
		), p.MustWrap("research", "", weft.New(wefttest.Script())))
		s, _ := thread.Create(ctx, thread.Memory(), parent)
		turn, _ := s.Send(ctx, weft.User("go"))
		res, err := turn.Wait()
		if err != nil {
			t.Fatal(err)
		}
		// The pool's part is pinned; what follows it is encoding/json's
		// own description of the mismatch.
		const want = `thread/pool: decode "research" arguments: json: cannot unmarshal number `
		if got := lastToolResult(res.Messages); !strings.HasPrefix(got, want) {
			t.Errorf("got %q\nwant the prefix %q", got, want)
		}
		if rs := pool.Receipts(s); len(rs) != 0 {
			t.Errorf("a call that did not decode left receipts: %+v", rs)
		}
	})

	t.Run("a child that fails after its park", func(t *testing.T) {
		p := pool.New(1)
		child, _ := gatedChild(
			wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"1"}`}),
			wefttest.Fail(errors.New("connection reset")),
		)
		s, _ := thread.Create(ctx, thread.Memory(), delegating(p, child))
		t1, rc := park(t, s)
		if err := p.Decide(ctx, s, thread.Approve(rc.Child+"/c-a")); err != nil {
			t.Fatal(err)
		}
		waitFor(t, p, s, rc.ID, pool.Failed)
		if _, err := t1.Next().Wait(); err != nil {
			t.Fatal(err)
		}
		// The same shape a first run's failure has, delivered as the
		// parked call's resolve_error.
		const want = `SUBAGENT_FAILED: agent "research" failed at step 0: model stream: connection reset`
		if got := toolResultsOf(s.Context()); len(got) != 1 || got[0] != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	// Outcomes read back from the ledger by Recover: the step a live
	// failure names is not recorded, so the text has none.
	for _, tc := range []struct {
		name  string
		state string
		want  string
	}{
		{"recovered failure", thread.PoolFailed, `SUBAGENT_FAILED: agent "research" failed: connection reset`},
		{"recovered budget death", thread.PoolCapped, `SUBAGENT_FAILED: agent "research" failed: connection reset`},
		{"recovered cancellation", thread.PoolCanceled, `SUBAGENT_CANCELED: agent "research" was canceled before it finished`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := thread.Memory()
			agent, _ := gatedChild(
				wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-wrapper", Args: `{"order_id":"1"}`}),
				wefttest.Say("all done"),
			)
			parent, _ := thread.Create(ctx, st, agent)
			t1, err := parent.Send(ctx, weft.User("go"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := t1.Wait(); err != nil {
				t.Fatal(err)
			}
			child, err := thread.Create(ctx, st, weft.New(wefttest.Script()),
				thread.WithLineage(parent.ID(), "c-wrapper"), thread.WithMeta(map[string]string{"pool_agent": "research"}))
			if err != nil {
				t.Fatal(err)
			}
			accept, _ := parent.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
				Status: thread.PoolAccepted, Child: child.ID(), Call: "c-wrapper",
			})
			if _, err := parent.AppendApprovalRequests(ctx, thread.ApprovalRequestEntry{
				CallID: child.ID() + "/c-a", Tool: "wire", ArgsSHA256: "h", RunID: child.ID() + "-t1",
				Child: child.ID(), Wrapper: "c-wrapper",
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := parent.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
				Receipt: accept.ID, Status: tc.state, Child: child.ID(), Stop: "connection reset",
			}); err != nil {
				t.Fatal(err)
			}
			if err := pool.New(1).Recover(ctx, parent); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			next := t1.Next()
			if next == nil {
				t.Fatal("the parked call was not resolved")
			}
			if _, err := next.Wait(); err != nil {
				t.Fatal(err)
			}
			if got := toolResultsOf(parent.Context()); len(got) != 1 || got[0] != tc.want {
				t.Errorf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}
