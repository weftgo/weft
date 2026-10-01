package thread

import (
	"context"
	"errors"
	"testing"
	"time"

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

// TestEffectiveDecisionFold pins the fold over one occurrence's
// decisions: who counts as an approver, what conflicts, and which
// denials are the session's own.
func TestEffectiveDecisionFold(t *testing.T) {
	approve := func(who, via, key string) ApprovalDecisionEntry {
		return ApprovalDecisionEntry{CallID: "c", Outcome: OutcomeApprove, Who: who, Via: via, KeyID: key}
	}
	deny := func(reason, via string) ApprovalDecisionEntry {
		return ApprovalDecisionEntry{CallID: "c", Outcome: OutcomeDeny, Reason: reason, Via: via}
	}
	resolve := ApprovalDecisionEntry{CallID: "c", Outcome: OutcomeResolve, Content: "42", Via: viaUser}
	for _, tc := range []struct {
		name    string
		ds      []ApprovalDecisionEntry
		quorum  int
		decided bool
		outcome Outcome
		reason  string
	}{
		{"nothing decided", nil, 1, false, "", ""},
		{"one approval, no quorum", []ApprovalDecisionEntry{approve("a", viaUser, "")}, 1, true, OutcomeApprove, ""},
		{"one approval of two", []ApprovalDecisionEntry{approve("a", viaUser, "")}, 2, false, "", ""},
		{"the same Who twice is one approver", []ApprovalDecisionEntry{approve("a", viaUser, ""), approve("a", viaUser, "")}, 2, false, "", ""},
		{"two declared names", []ApprovalDecisionEntry{approve("a", viaUser, ""), approve("b", viaUser, "")}, 2, true, OutcomeApprove, ""},
		{"one key under two names is one approver", []ApprovalDecisionEntry{approve("alice", viaSigned, "k1"), approve("bob", viaSigned, "k1")}, 2, false, "", ""},
		{"two keys under one name are two approvers", []ApprovalDecisionEntry{approve("ops", viaSigned, "k1"), approve("ops", viaSigned, "k2")}, 2, true, OutcomeApprove, ""},
		{"a Who cannot pose as a key", []ApprovalDecisionEntry{approve("x", viaSigned, "k1"), approve("key:k1", viaUser, "")}, 2, true, OutcomeApprove, ""},
		{"a grant and a caller are two", []ApprovalDecisionEntry{approve("", viaGrant, ""), approve("", viaUser, "")}, 2, true, OutcomeApprove, ""},
		{"a Who cannot pose as the grant", []ApprovalDecisionEntry{approve("", viaGrant, ""), approve("grant", viaUser, "")}, 2, true, OutcomeApprove, ""},
		{"a deny alone resolves", []ApprovalDecisionEntry{deny("no", viaUser)}, 3, true, OutcomeDeny, "no"},
		{"approve then deny conflicts", []ApprovalDecisionEntry{approve("a", viaUser, ""), deny("no", viaUser)}, 2, true, OutcomeDeny, conflictReason},
		{"approve then expiry keeps the expiry's reason", []ApprovalDecisionEntry{approve("a", viaUser, ""), deny("expired: x", viaExpiry)}, 2, true, OutcomeDeny, "expired: x"},
		{"approve then interrupt keeps the interrupt's reason", []ApprovalDecisionEntry{approve("a", viaUser, ""), deny(reasonInterrupted, viaInterrupt)}, 2, true, OutcomeDeny, reasonInterrupted},
		{"a resolve alone resolves", []ApprovalDecisionEntry{resolve}, 2, true, OutcomeResolve, ""},
		{"resolve then approve conflicts", []ApprovalDecisionEntry{resolve, approve("a", viaUser, "")}, 2, true, OutcomeDeny, conflictReason},
		{"approve then resolve conflicts", []ApprovalDecisionEntry{approve("a", viaUser, ""), resolve}, 2, true, OutcomeDeny, conflictReason},
		{"two resolves conflict", []ApprovalDecisionEntry{resolve, resolve}, 1, true, OutcomeDeny, conflictReason},
	} {
		got, ok := effectiveDecision(tc.ds, tc.quorum)
		if ok != tc.decided || (ok && (got.Outcome != tc.outcome || got.Reason != tc.reason)) {
			t.Errorf("%s: got %+v decided=%v; want outcome %q reason %q decided=%v",
				tc.name, got, ok, tc.outcome, tc.reason, tc.decided)
		}
	}
}

// Decisions are scoped to the occurrence they were recorded for,
// exactly: a decision is never lent to another run's occurrence of
// the same call id, an empty run id included.
func TestScopedDecisionsExact(t *testing.T) {
	ds := []ApprovalDecisionEntry{
		{ID: "d1", CallID: "c", RunID: "s-t1"},
		{ID: "d2", CallID: "c", RunID: "s-t2"},
		{ID: "d3", CallID: "c"},
	}
	for run, want := range map[string]string{"s-t1": "d1", "s-t2": "d2", "": "d3"} {
		got := scopedDecisions(ds, run)
		if len(got) != 1 || got[0].ID != want {
			t.Errorf("run %q: got %+v, want only %s", run, got, want)
		}
	}
	if got := scopedDecisions(ds, "s-t3"); len(got) != 0 {
		t.Errorf("an occurrence nobody decided borrowed %+v", got)
	}
}

// approvalNow is the one clock the approvals code reads: UTC, and
// current.
func TestApprovalNowIsUTC(t *testing.T) {
	s := &Session{}
	now := s.approvalNow()
	if now.Location() != time.UTC {
		t.Fatalf("approvalNow location: %v", now.Location())
	}
	if d := time.Since(now); d < 0 || d > time.Minute {
		t.Fatalf("approvalNow is %v away from the wall clock", d)
	}
}
