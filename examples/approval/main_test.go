package main

import (
	"context"
	"testing"

	"github.com/weftgo/weft"
)

func TestApprovalRoundTrip(t *testing.T) {
	agt := newAgent()
	res, err := agt.Generate(context.Background(), weft.Prompt("Please refund order 1234 in full."))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 || res.Pending[0].Name != "refund_order" {
		t.Fatalf("Pending = %+v", res.Pending)
	}
	res, err = agt.Generate(context.Background(), weft.Messages(res.Messages...), weft.Deny(res.Pending[0].ID, "test"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() == "" || len(res.Pending) != 0 {
		t.Errorf("resumed: text=%q pending=%v", res.Text(), res.Pending)
	}
}

// The Approve leg: the parked call executes through the tool chain and
// its result lands in the transcript the model then sees.
func TestApprovalApproveExecutesRefund(t *testing.T) {
	agt := newAgent()
	res, err := agt.Generate(context.Background(), weft.Prompt("Please refund order 1234 in full."))
	if err != nil {
		t.Fatal(err)
	}
	res, err = agt.Generate(context.Background(), weft.Messages(res.Messages...), weft.Approve(res.Pending[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	var refunded string
	for _, m := range res.Messages {
		if m.Role != weft.RoleTool {
			continue
		}
		for _, p := range m.Content {
			if r, ok := p.(weft.ToolResultPart); ok && r.Name == "refund_order" && !r.IsError {
				refunded = r.Content
			}
		}
	}
	if refunded != "refunded $129.99 on order 1234" {
		t.Errorf("refund_order result = %q, want the executed refund", refunded)
	}
}

// The Resolve leg: the parked call never executes; the pasted result
// is what the model sees (TODO §2a.5).
func TestApprovalResolvePastesRowCount(t *testing.T) {
	agt := runSQLAgent()
	res, err := agt.Generate(context.Background(), weft.Prompt("How many orders are there?"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 || res.Pending[0].Name != "run_sql" {
		t.Fatalf("Pending = %+v, want the parked run_sql call", res.Pending)
	}
	res, err = agt.Generate(context.Background(),
		weft.Messages(res.Messages...), weft.Resolve(res.Pending[0].ID, "row_count: 42"))
	if err != nil {
		t.Fatal(err)
	}
	var pasted string
	for _, m := range res.Messages {
		if m.Role != weft.RoleTool {
			continue
		}
		for _, p := range m.Content {
			if r, ok := p.(weft.ToolResultPart); ok && r.Name == "run_sql" {
				pasted = r.Content
			}
		}
	}
	if pasted != "row_count: 42" {
		t.Errorf("run_sql result = %q, want the pasted row count", pasted)
	}
}
