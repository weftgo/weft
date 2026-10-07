package weft_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// A handler running under Approve that dispatches another tool through
// Agent.CallTool must not lend that call its approval: the inner call
// is a fresh, unapproved call, so a RequireApproval tool refuses with
// ErrApprovalRequired — and the inner handler sees its own call, not
// the outer one.
func TestCallToolDoesNotInheritApproval(t *testing.T) {
	var refunded atomic.Int64
	var innerCall weft.Call
	var innerOK bool
	var innerErr error
	var agt *weft.Agent
	refund := weft.Tool("refund", "Refunds.", func(ctx context.Context, _ struct{}) (string, error) {
		refunded.Add(1)
		innerCall, innerOK = weft.CallFromContext(ctx)
		return "refunded", nil
	}, weft.RequireApproval())
	peek := weft.Tool("peek", "Peeks.", func(ctx context.Context, _ struct{}) (string, error) {
		innerCall, innerOK = weft.CallFromContext(ctx)
		return "peeked", nil
	})
	review := weft.Tool("review", "Reviews, then refunds.", func(ctx context.Context, _ struct{}) (string, error) {
		_, innerErr = agt.CallTool(ctx, weft.ToolCallPart{ID: "inner", Name: "refund"})
		return "reviewed", nil
	}, weft.RequireApproval())
	agt = weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "review", ID: "c1"}),
		wefttest.Say("done"),
	), refund, peek, review)
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil || len(res.Pending) != 1 {
		t.Fatalf("pending = %v, err = %v; want review parked", res.Pending, err)
	}
	if _, err := agt.Generate(context.Background(), weft.Messages(res.Messages...), weft.Approve("c1")); err != nil {
		t.Fatal(err)
	}
	if refunded.Load() != 0 || !errors.Is(innerErr, weft.ErrApprovalRequired) {
		t.Errorf("CallTool from an approved handler: refund ran %d times, err = %v; want ErrApprovalRequired",
			refunded.Load(), innerErr)
	}

	// The nested handler sees its own call.
	outer := weft.Tool("outer", "Dispatches peek.", func(ctx context.Context, _ struct{}) (string, error) {
		return agt.CallTool(ctx, weft.ToolCallPart{ID: "inner2", Name: "peek"})
	})
	agt2 := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "outer", ID: "c2"}),
		wefttest.Say("done"),
	), outer)
	if _, err := agt2.Generate(context.Background(), weft.Prompt("x"), weft.RunID("r")); err != nil {
		t.Fatal(err)
	}
	if !innerOK || innerCall.CallID != "inner2" || innerCall.Name != "peek" || innerCall.Approved || innerCall.RunID != "r" {
		t.Errorf("nested handler saw %+v (ok=%v), want its own call inner2/peek in run r, unapproved", innerCall, innerOK)
	}
}
