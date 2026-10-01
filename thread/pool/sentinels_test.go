package pool_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
	"github.com/weftgo/weft/wefttest"
)

// Every exported sentinel of the pool is reachable with errors.Is from
// the public call that documents it. Each row provokes the error
// through the pool's API on thread.Memory and scripted models; the one
// assertion is errors.Is(err, sentinel).
//
// ErrDepth and ErrCycle have two faces: Submit returns them as Go
// errors — the rows here — and a wrapped tool reports the same refusal
// to the model as data (SUBAGENT_DEPTH, SUBAGENT_CYCLE in the tool
// result), which no errors.Is can see; TestDepthLimit and TestCycle
// (handoff_test.go) pin that text.
func TestSentinelsAreMatchable(t *testing.T) {
	rows := []struct {
		name     string
		sentinel error
		provoke  func(t *testing.T) error
	}{
		{"ErrClosed/Submit after Close", pool.ErrClosed, func(t *testing.T) error {
			p, s := sentinelClosedPool(t)
			_, err := p.Submit(sentinelCtx(t), s, weft.New(wefttest.Script()), "go")
			return err
		}},
		{"ErrClosed/Decide after Close", pool.ErrClosed, func(t *testing.T) error {
			p, s := sentinelClosedPool(t)
			return p.Decide(sentinelCtx(t), s)
		}},
		{"ErrClosed/Recover after Close", pool.ErrClosed, func(t *testing.T) error {
			p, s := sentinelClosedPool(t)
			return p.Recover(sentinelCtx(t), s)
		}},
		// ErrClosed from a wrapped tool's call has no row: the wrap is
		// tool middleware, so the refusal is the call's tool result —
		// data the model reads ("thread/pool: pool is closed"), not an
		// error any caller holds. TestCloseCoversEverything
		// (bridge_test.go) pins that text.

		{"ErrUnknownReceipt/Cancel of an id the ledger does not hold", pool.ErrUnknownReceipt, func(t *testing.T) error {
			p, s := sentinelPool(t)
			return p.Cancel(sentinelCtx(t), s, "e_unknown")
		}},
		{"ErrUnknownReceipt/Wait on an id the ledger does not hold", pool.ErrUnknownReceipt, func(t *testing.T) error {
			p, s := sentinelPool(t)
			_, err := p.Wait(sentinelCtx(t), s, "e_unknown")
			return err
		}},
		{"ErrUnknownReceipt/Forward to an id the ledger does not hold", pool.ErrUnknownReceipt, func(t *testing.T) error {
			p, s := sentinelPool(t)
			_, err := p.Forward(sentinelCtx(t), s, "e_unknown", weft.User("hello"))
			return err
		}},
		{"ErrUnknownReceipt/Forward through another session's receipt", pool.ErrUnknownReceipt, func(t *testing.T) error {
			ctx := sentinelCtx(t)
			p, s := sentinelPool(t)
			r := sentinelSettled(t, p, s)
			other, err := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			_, err = p.Forward(ctx, other, r.ID, weft.User("hello"))
			return err
		}},

		{"ErrNotRunning/Cancel of a settled receipt", pool.ErrNotRunning, func(t *testing.T) error {
			p, s := sentinelPool(t)
			r := sentinelSettled(t, p, s)
			err := p.Cancel(sentinelCtx(t), s, r.ID)
			var se *pool.StateError
			if !errors.As(err, &se) || se.State != pool.Done {
				t.Errorf("Cancel of a settled receipt = %v, want a *StateError in state done", err)
			}
			return err
		}},
		{"ErrNotRunning/Forward to a settled receipt", pool.ErrNotRunning, func(t *testing.T) error {
			p, s := sentinelPool(t)
			r := sentinelSettled(t, p, s)
			_, err := p.Forward(sentinelCtx(t), s, r.ID, weft.User("late"))
			return err
		}},

		{"ErrNoAgent/Recover by a pool that holds no agent for a parked child", pool.ErrNoAgent, func(t *testing.T) error {
			reopened := sentinelParkedDelegation(t)
			return pool.New(1).Recover(sentinelCtx(t), reopened)
		}},
		{"ErrNoAgent/Decide by a pool that holds no agent for a parked child", pool.ErrNoAgent, func(t *testing.T) error {
			reopened := sentinelParkedDelegation(t)
			var ds []thread.Decision
			for _, r := range reopened.Pending() {
				ds = append(ds, thread.Approve(r.CallID))
			}
			if len(ds) != 1 {
				t.Fatalf("Pending after the restart = %+v, want the child's one request", reopened.Pending())
			}
			return pool.New(1).Decide(sentinelCtx(t), reopened, ds...)
		}},

		{"ErrDepth/Submit past MaxDepth from inside a run", pool.ErrDepth, func(t *testing.T) error {
			_, depthErr := sentinelSubmitInside(t)
			return depthErr
		}},
		{"ErrCycle/Submit of the agent already running above", pool.ErrCycle, func(t *testing.T) error {
			cycleErr, _ := sentinelSubmitInside(t)
			return cycleErr
		}},

		{"ErrDuplicateWrap/Wrap of another agent under a held name", pool.ErrDuplicateWrap, func(t *testing.T) error {
			p := pool.New(1)
			if _, err := p.Wrap("research", "", weft.New(wefttest.Script())); err != nil {
				t.Fatalf("the first Wrap: %v", err)
			}
			_, err := p.Wrap("research", "", weft.New(wefttest.Script()))
			return err
		}},
	}

	// Every exported sentinel of the pool is named by a row.
	covered := map[error]bool{}
	for _, row := range rows {
		covered[row.sentinel] = true
	}
	for name, sentinel := range map[string]error{
		"ErrClosed": pool.ErrClosed, "ErrUnknownReceipt": pool.ErrUnknownReceipt,
		"ErrNotRunning": pool.ErrNotRunning, "ErrNoAgent": pool.ErrNoAgent,
		"ErrDepth": pool.ErrDepth, "ErrCycle": pool.ErrCycle, "ErrDuplicateWrap": pool.ErrDuplicateWrap,
	} {
		if !covered[sentinel] {
			t.Errorf("pool.%s has no row", name)
		}
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			err := row.provoke(t)
			if err == nil {
				t.Fatalf("no error; want one matching %q", row.sentinel)
			}
			if !errors.Is(err, row.sentinel) {
				t.Fatalf("errors.Is(err, %q) = false\n  err:  %v\n  type: %T", row.sentinel, err, err)
			}
		})
	}
}

// sentinelCtx is a row's context: bounded, so a row that would
// deadlock fails instead of hanging the binary.
func sentinelCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// sentinelPool is a pool and a parent session to delegate from; the
// row's cleanup closes the pool.
func sentinelPool(t *testing.T) (*pool.Pool, *thread.Session) {
	t.Helper()
	s, err := thread.Create(sentinelCtx(t), thread.Memory(), weft.New(wefttest.Script()))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	p := pool.New(1)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.Close(ctx); err != nil {
			t.Errorf("the pool's Close: %v", err)
		}
	})
	return p, s
}

// sentinelClosedPool is a pool whose Close has run, and a parent
// session.
func sentinelClosedPool(t *testing.T) (*pool.Pool, *thread.Session) {
	t.Helper()
	p, s := sentinelPool(t)
	if err := p.Close(sentinelCtx(t)); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return p, s
}

// sentinelSettled submits one child that answers at once and waits for
// its receipt to settle done.
func sentinelSettled(t *testing.T, p *pool.Pool, s *thread.Session) pool.Receipt {
	t.Helper()
	ctx := sentinelCtx(t)
	r, err := p.Submit(ctx, s, weft.New(wefttest.Script(wefttest.Say("done"))), "go")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	final, err := p.Wait(ctx, s, r.ID)
	if err != nil || final.State != pool.Done {
		t.Fatalf("the child: %+v, %v; want done", final, err)
	}
	return final
}

// sentinelParkedDelegation plays a process that parks a sync
// delegation — the child's gated call waits for a decision — and
// stops; it returns the parent session as a second process opens it,
// on an agent whose pool is not the one the caller will ask.
func sentinelParkedDelegation(t *testing.T) *thread.Session {
	t.Helper()
	ctx := sentinelCtx(t)
	st := thread.Memory()
	gated := weft.Tool("refund", "", func(_ context.Context, in struct {
		OrderID string `json:"order_id"`
	}) (string, error) {
		return "refunded " + in.OrderID, nil
	}, weft.RequireApproval())

	p := pool.New(1)
	child := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "call-refund", Args: `{"order_id":"42"}`}),
	), gated)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refunds", ID: "call-delegate", Args: `{"prompt":"refund order 42"}`}),
	), p.MustWrap("refunds", "delegates the refund flow", child))
	s, err := thread.Create(ctx, st, parent)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	turn, err := s.Send(ctx, weft.User("refund order 42"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := turn.WaitContext(ctx); err != nil {
		t.Fatalf("the delegating turn: %v", err)
	}
	if rs := pool.Receipts(s); len(rs) != 1 || rs[0].State != pool.Parked {
		t.Fatalf("receipts before the restart = %+v, want one parked", rs)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatalf("the pool's Close: %v", err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatalf("the session's Close: %v", err)
	}
	reopened, err := thread.Open(ctx, st, s.ID(), weft.New(wefttest.Script()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return reopened
}

// sentinelSubmitInside submits twice from inside a run of a
// MaxDepth(1) pool's child: the running agent again (a cycle), and
// another agent one level deeper (past the limit). It returns the two
// errors Submit gave.
func sentinelSubmitInside(t *testing.T) (cycleErr, depthErr error) {
	t.Helper()
	ctx := sentinelCtx(t)
	p := pool.New(2, pool.MaxDepth(1))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.Close(ctx); err != nil {
			t.Errorf("the pool's Close: %v", err)
		}
	})
	var agent *weft.Agent
	other := weft.New(wefttest.Script(wefttest.Say("other")))
	probe := weft.Tool("probe", "", func(ctx context.Context, _ struct{}) (string, error) {
		s := thread.SessionFromContext(ctx)
		_, cycleErr = p.Submit(ctx, s, agent, "again")
		_, depthErr = p.Submit(ctx, s, other, "deeper")
		return "probed", nil
	})
	agent = weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "probe", ID: "c-probe"}),
		wefttest.Say("done"),
	), probe)
	s, err := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	r, err := p.Submit(ctx, s, agent, "go")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if final, err := p.Wait(ctx, s, r.ID); err != nil || final.State != pool.Done {
		t.Fatalf("the probing child: %+v, %v; want done", final, err)
	}
	return cycleErr, depthErr
}
