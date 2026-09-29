package thread

import (
	"context"
	"errors"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// TestRunResumeWithoutBoundaryMakesNoGhostRun: a resume armed for a
// boundary that vanished before the runner picked it up — the caller
// branched away from the parked tail between arming and the run — must
// finish its turn with ErrNotPending instead of running the model over
// nothing. The ghost run would be a model call with no prompt and no
// decisions: a spurious assistant turn the caller never asked for.
func TestRunResumeWithoutBoundaryMakesNoGhostRun(t *testing.T) {
	ctx := context.Background()
	tool := weft.Tool("refund", "Refund an order.",
		func(context.Context, struct{}) (string, error) { return "refunded", nil },
		weft.RequireApproval())
	agent := weft.New(
		wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("resumed")),
		tool)
	s, err := Create(ctx, Memory(), agent, AutoResume(false))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("refund it"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	pend := s.Pending()
	if len(pend) != 1 {
		t.Fatalf("Pending: %d", len(pend))
	}
	if _, err := s.Decide(ctx, Approve(pend[0].CallID)); err != nil {
		t.Fatal(err)
	}
	// Arm exactly as armResumeLocked does, then navigate the leaf off
	// the parked tail before the runner would have picked the resume
	// up: the boundary is gone by the time runResume runs.
	s.mu.Lock()
	if !s.boundaryLocked() {
		t.Fatal("the parked boundary is not open")
	}
	rt := s.newTurnLocked()
	rt.resume = true
	s.await.resumed = rt
	s.mu.Unlock()
	if err := s.Branch(ctx, turn.ID()); err != nil { // the prompt entry: below the parked tail
		t.Fatal(err)
	}
	s.runResume(ctx, rt)
	rt.mu.Lock()
	rerr := rt.waitErr
	rt.mu.Unlock()
	if rerr == nil || !errors.Is(rerr, ErrNotPending) {
		t.Fatalf("the resume over a vanished boundary: %v, want ErrNotPending", rerr)
	}
	// No ghost model call: no turn entry exists beyond the parked
	// turn's — the script's second turn was never played.
	runs := 0
	for _, e := range s.order {
		if _, ok := e.(TurnEntry); ok {
			runs++
		}
	}
	if runs != 1 {
		t.Fatalf("turn entries after the ghost guard: %d, want 1 (the parked turn only)", runs)
	}
}
