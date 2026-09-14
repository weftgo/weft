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
