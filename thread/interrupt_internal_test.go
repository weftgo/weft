package thread

import (
	"context"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// TestRollbackBeforeReceiptLands: a Rollback that lands while the
// in-flight turn's receipt entry is not on the tree — the runner marks
// a turn in-flight before its prompt appends (an interrupt at birth),
// and a resume turn's entry lands only at its end — must target the
// leaf the turn hangs from. A silent no-op would leave the follow-up
// on the interrupted turn's own line, the opposite of the policy.
func TestRollbackBeforeReceiptLands(t *testing.T) {
	ctx := context.Background()
	s, err := Create(ctx, Memory(), weft.New(wefttest.Script(wefttest.Say("x"))))
	if err != nil {
		t.Fatal(err)
	}
	// One landed entry: the leaf a birth-window turn would hang from.
	if err := s.Custom(ctx, "seed", nil); err != nil {
		t.Fatal(err)
	}
	leaf := s.leaf
	// The birth window: in-flight set, receipt not yet appended.
	s.running = true
	inflight := s.newTurnLocked()
	s.inFlight = inflight
	s.mu.Lock()
	if _, err := s.interruptSendLocked(ctx, weft.User("rollback this"), nil, true); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()
	rollback, preTurn := inflight.rollback, inflight.preTurn
	if !rollback {
		t.Fatal("the in-flight turn is not marked for rollback")
	}
	if preTurn != leaf {
		t.Fatalf("rollback target = %q, want the leaf %q the turn hangs from", preTurn, leaf)
	}
}
