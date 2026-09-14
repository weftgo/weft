package main

import (
	"context"
	"testing"

	"github.com/weftgo/weft"
)

// The example's agent runs end to end offline: the scripted model looks
// the order up, the tool answers, and the run closes with the summary.
func TestGettingStartedRun(t *testing.T) {
	res, err := newSupportAgent().Generate(context.Background(), weft.Prompt("Where is order 1234?"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "Order 1234 shipped and is on its way." {
		t.Errorf("Text() = %q", res.Text())
	}
	if res.NumSteps() != 2 {
		t.Errorf("NumSteps() = %d, want 2 (lookup, then answer)", res.NumSteps())
	}
}
