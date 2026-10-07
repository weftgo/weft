package core_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// A handler running under Approve that dispatches another tool through
// Agent.CallTool must not lend that call its approval: the inner call
// is a fresh, unapproved call, so a RequireApproval tool refuses with
// ErrApprovalRequired — and the inner handler sees its own call, not
// the outer one.
func TestCallToolDoesNotInheritApproval(t *testing.T) {
	var refunded atomic.Int64
	var innerCall core.Call
	var innerOK bool
	var innerErr error
	var agt *core.Agent
	refund := core.Tool("refund", "Refunds.", func(ctx context.Context, _ struct{}) (string, error) {
		refunded.Add(1)
		innerCall, innerOK = core.CallFromContext(ctx)
		return "refunded", nil
	}, core.RequireApproval())
	peek := core.Tool("peek", "Peeks.", func(ctx context.Context, _ struct{}) (string, error) {
		innerCall, innerOK = core.CallFromContext(ctx)
		return "peeked", nil
	})
	review := core.Tool("review", "Reviews, then refunds.", func(ctx context.Context, _ struct{}) (string, error) {
		_, innerErr = agt.CallTool(ctx, core.ToolCallPart{ID: "inner", Name: "refund"})
		return "reviewed", nil
	}, core.RequireApproval())
	agt = core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "review", ID: "c1"}),
		wefttest.Say("done"),
	), refund, peek, review)
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil || len(res.Pending) != 1 {
		t.Fatalf("pending = %v, err = %v; want review parked", res.Pending, err)
	}
	if _, err := agt.Generate(context.Background(), core.Messages(res.Messages...), core.Approve("c1")); err != nil {
		t.Fatal(err)
	}
	if refunded.Load() != 0 || !errors.Is(innerErr, core.ErrApprovalRequired) {
		t.Errorf("CallTool from an approved handler: refund ran %d times, err = %v; want ErrApprovalRequired",
			refunded.Load(), innerErr)
	}

	// The nested handler sees its own call.
	outer := core.Tool("outer", "Dispatches peek.", func(ctx context.Context, _ struct{}) (string, error) {
		return agt.CallTool(ctx, core.ToolCallPart{ID: "inner2", Name: "peek"})
	})
	agt2 := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "outer", ID: "c2"}),
		wefttest.Say("done"),
	), outer)
	if _, err := agt2.Generate(context.Background(), core.Prompt("x"), core.RunID("r")); err != nil {
		t.Fatal(err)
	}
	if !innerOK || innerCall.CallID != "inner2" || innerCall.Name != "peek" || innerCall.Approved || innerCall.RunID != "r" {
		t.Errorf("nested handler saw %+v (ok=%v), want its own call inner2/peek in run r, unapproved", innerCall, innerOK)
	}
}
