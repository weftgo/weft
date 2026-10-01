package thread_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// testClock is a settable session clock.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// mirrorUnder appends one mirrored child request parked under
// wrapper, the way the pool does.
func mirrorUnder(t *testing.T, s *thread.Session, child, callID, run, wrapper string, expiry time.Time) thread.ApprovalRequestEntry {
	t.Helper()
	out, err := s.AppendApprovalRequests(context.Background(), thread.ApprovalRequestEntry{
		CallID: child + "/" + callID, Tool: "wire", ArgsSHA256: "h", RunID: run,
		Child: child, Wrapper: wrapper, Expiry: expiry,
	})
	if err != nil {
		t.Fatal(err)
	}
	return out[0]
}

// A delegating call takes no decision (ADR 0022 §7): Pending hides it,
// and every door that would decide it — Decide, DecideSigned, the
// challenge Request mints — refuses with ErrDelegated. Before the fix
// Decide validated against the raw pending set, accepted the approval
// and re-ran the delegation: a second child, the side effects twice.
func TestDecideRefusesDelegatingCall(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-wrapper", Args: `{"order_id":"5"}`}),
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring))
	if err != nil {
		t.Fatal(err)
	}
	t1, call := parkTurn(t, s, ctx)
	// A challenge minted while the call was still an ordinary request:
	// the signature below is valid in every respect but one.
	early, err := s.Request(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	mirrorUnder(t, s, "s_child", "c-inner", "s_child-t1", call.ID, time.Time{})

	if pend := s.Pending(); len(pend) != 1 || pend[0].Child != "s_child" {
		t.Fatalf("Pending = %+v, want the child's request alone", pend)
	}
	for _, d := range []thread.Decision{
		thread.Approve(call.ID), thread.Deny(call.ID, "no"), thread.Resolve(call.ID, "forged"),
	} {
		if _, err := s.Decide(ctx, d); !errors.Is(err, thread.ErrDelegated) {
			t.Fatalf("Decide(%s) on the delegating call: %v, want ErrDelegated", d.Kind, err)
		}
	}
	// A batch that names the wrapper beside a decidable call records
	// neither.
	if _, err := s.Decide(ctx, thread.Approve("s_child/c-inner"), thread.Approve(call.ID)); !errors.Is(err, thread.ErrDelegated) {
		t.Fatalf("a batch naming the delegating call: %v, want ErrDelegated", err)
	}
	if _, err := s.Request(call.ID); !errors.Is(err, thread.ErrDelegated) {
		t.Fatalf("Request on the delegating call: %v, want ErrDelegated", err)
	}
	if _, err := s.DecideSigned(ctx, thread.SignDecision(secret, early, thread.Approve(call.ID))); !errors.Is(err, thread.ErrDelegated) {
		t.Fatalf("DecideSigned on the delegating call: %v, want ErrDelegated", err)
	}
	if got := len(decisionsFor(s, call.ID)) + len(decisionsFor(s, "s_child/c-inner")); got != 0 {
		t.Fatalf("refused decisions recorded: %d", got)
	}
	if t1.Next() != nil || len(ran.snapshot()) != 0 {
		t.Fatal("a refused decision resumed the delegating call")
	}
	// The one door: the child's answer.
	rt, err := s.ResolveDelegation(ctx, call.ID, "s_child", "the child's answer", false)
	if err != nil {
		t.Fatal(err)
	}
	if rt != nil {
		t.Fatal("the boundary resumed with the child's request still undecided")
	}
}

// A wrapper is hidden by occurrence, not by id: call ids repeat across
// turns, and an ordinary call that reuses a resolved wrapper's id is
// offered and decided like any other.
func TestReusedWrapperIDIsOffered(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "same-id", Args: `{"order_id":"1"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "same-id", Args: `{"order_id":"2"}`}),
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	_, call := parkTurn(t, s, ctx)
	m := mirrorUnder(t, s, "s_child", "c-inner", "s_child-t1", call.ID, time.Time{})
	if _, err := s.Decide(ctx, thread.Approve(m.CallID)); err != nil {
		t.Fatal(err)
	}
	rt, err := s.ResolveDelegation(ctx, call.ID, "s_child", "child answer", false)
	if err != nil || rt == nil {
		t.Fatalf("resolving the delegation: turn %v, err %v", rt, err)
	}
	res, err := rt.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 || res.Pending[0].ID != "same-id" {
		t.Fatalf("the resumed run parked %+v, want the reused id", res.Pending)
	}
	pend := s.Pending()
	if len(pend) != 1 || pend[0].CallID != "same-id" || pend[0].Child != "" {
		t.Fatalf("Pending = %+v, want the ordinary call that reuses the wrapper's id", pend)
	}
	nt, err := s.Decide(ctx, thread.Approve("same-id"))
	if err != nil {
		t.Fatalf("deciding the reused id: %v", err)
	}
	if _, err := nt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("the approved call: %v", got)
	}
}

// A mirror superseded by a later one for the same call id (a child
// parking twice on one id) is not pending; and when a delegation ends
// with requests nobody decided, DenyMirrored takes them off the
// parent's Pending with a recorded reason.
func TestMirrorsSupersededAndDenied(t *testing.T) {
	ctx := context.Background()
	s, err := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	first := mirrorUnder(t, s, "s_child", "call_1", "s_child-t1", "", time.Time{})
	if _, err := s.Decide(ctx, thread.Approve(first.CallID)); err != nil {
		t.Fatal(err)
	}
	if pend := s.Pending(); len(pend) != 0 {
		t.Fatalf("Pending after the first decision = %+v", pend)
	}
	second := mirrorUnder(t, s, "s_child", "call_1", "s_child-t2", "", time.Time{})
	other := mirrorUnder(t, s, "s_other", "call_1", "s_other-t1", "", time.Time{})
	pend := s.Pending()
	if len(pend) != 2 || pend[0].ID != second.ID || pend[0].RunID != "s_child-t2" || pend[1].ID != other.ID {
		t.Fatalf("Pending after the re-park = %+v, want the second mirror and the other child's", pend)
	}
	ms, err := s.MirroredRequests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Request.ID != second.ID || ms[0].Decided || len(ms[0].Decisions) != 0 {
		t.Fatalf("MirroredRequests = %+v, want the second mirror, undecided, and the other child's", ms)
	}
	// The delegation ends with the request undecided — a canceled
	// child: the request is no longer anyone's to decide.
	if err := s.DenyMirrored(ctx, "s_child", "the delegation was canceled"); err != nil {
		t.Fatal(err)
	}
	pend = s.Pending()
	if len(pend) != 1 || pend[0].ID != other.ID {
		t.Fatalf("Pending after DenyMirrored = %+v, want the other child's request alone", pend)
	}
	got := decisionsFor(s, second.CallID)
	last := got[len(got)-1]
	if last.Outcome != thread.OutcomeDeny || last.Via != "child" || last.Who != "thread/pool" ||
		last.Reason != "the delegation was canceled" || last.RunID != "s_child-t2" {
		t.Fatalf("the denial's record = %+v", last)
	}
	if _, err := s.Decide(ctx, thread.Approve(second.CallID)); !errors.Is(err, thread.ErrNotPending) {
		t.Fatalf("deciding an ended delegation's request: %v, want ErrNotPending", err)
	}
	// Nothing pending for the child: nothing recorded.
	before := len(s.Entries())
	if err := s.DenyMirrored(ctx, "s_child", "again"); err != nil {
		t.Fatal(err)
	}
	if len(s.Entries()) != before {
		t.Fatal("a second DenyMirrored recorded something")
	}
}

// A mirror lapses like any request — the parent's sweep denies it,
// Via "expiry" — and the delegating call it parks under does not: its
// fate is the child's answer, whenever that comes.
func TestMirrorExpiresWrapperDoesNot(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	agent, _ := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-wrapper", Args: `{"order_id":"5"}`}),
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.Clock(clock.Now), thread.RequestExpiry(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	t1, call := parkTurn(t, s, ctx)
	m := mirrorUnder(t, s, "s_child", "c-inner", "s_child-t1", call.ID, clock.Now().Add(time.Hour))
	clock.Advance(2 * time.Hour) // both the wrapper's own request and the mirror are past their expiry

	ms, err := s.MirroredRequests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || !ms[0].Decided || len(ms[0].Decisions) != 1 {
		t.Fatalf("MirroredRequests after the expiry = %+v, want the mirror denied", ms)
	}
	if d := ms[0].Decisions[0]; d.Outcome != thread.OutcomeDeny || d.Via != "expiry" || d.CallID != m.CallID {
		t.Fatalf("the mirror's decision = %+v", d)
	}
	if ms[0].Wrapper != call.ID {
		t.Fatalf("the mirror's wrapper = %q", ms[0].Wrapper)
	}
	if got := decisionsFor(s, call.ID); len(got) != 0 {
		t.Fatalf("the delegating call was decided by the sweep: %+v", got)
	}
	if t1.Next() != nil {
		t.Fatal("the sweep resumed the parent with the delegation still open")
	}
	rt, err := s.ResolveDelegation(ctx, call.ID, "s_child", "the child, denied, answered", false)
	if err != nil || rt == nil {
		t.Fatalf("resolving after the expiry: turn %v, err %v", rt, err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := resultFor(s, call.ID); got != "the child, denied, answered" {
		t.Fatalf("the delegating call's result: %q", got)
	}
}

// ReplayDecisions is the child's door for its parent's decisions: a
// pool child only, the decider's identity kept (a signed approver
// counts by key under the child's quorum), Via "parent" — never
// "signed" — and decisions for calls no longer pending dropped, so a
// repeated replay records nothing twice.
func TestReplayDecisions(t *testing.T) {
	ctx := context.Background()
	ring, _ := signerRing(t)
	approve := func(call, who, key string) thread.ApprovalDecisionEntry {
		return thread.ApprovalDecisionEntry{CallID: call, Outcome: thread.OutcomeApprove, Who: who, KeyID: key, Via: "signed"}
	}

	plain, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a"}), wefttest.Say("done"))
	notChild, err := thread.Create(ctx, thread.Memory(), plain)
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, notChild, ctx)
	if _, err := notChild.ReplayDecisions(ctx, approve(call.ID, "alice", "")); err == nil {
		t.Fatal("ReplayDecisions on a session with no lineage: no error — it is not a way around Decide")
	}
	if got := decisionsFor(notChild, call.ID); len(got) != 0 {
		t.Fatalf("the refused replay recorded %+v", got)
	}

	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"5"}`}),
		wefttest.Say("done"),
	)
	child, err := thread.Create(ctx, thread.Memory(), agent, thread.WithLineage("s_parent", "c-wrapper"),
		thread.Quorum(2), thread.WithKeyring(ring), thread.RequireSigned())
	if err != nil {
		t.Fatal(err)
	}
	call = parkSend(t, child, ctx)
	// The unsigned door stays shut on the child.
	if _, err := child.Decide(ctx, thread.Approve(call.ID)); !errors.Is(err, thread.ErrSignatureRequired) {
		t.Fatalf("Decide on a RequireSigned child: %v", err)
	}
	// One key twice is one approver: the quorum of two stays open.
	rt, err := child.ReplayDecisions(ctx, approve(call.ID, "alice", "k1"), approve(call.ID, "bob", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	if rt != nil {
		t.Fatal("one key counted as two approvers")
	}
	// A second key completes it; a decision for a call that is not
	// pending rides along and is dropped.
	rt, err = child.ReplayDecisions(ctx, approve(call.ID, "alice", "k2"), approve("c-gone", "alice", "k2"))
	if err != nil {
		t.Fatal(err)
	}
	if rt == nil {
		t.Fatal("two keys did not complete the quorum")
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	got := decisionsFor(child, call.ID)
	if len(got) != 3 {
		t.Fatalf("replayed decisions = %+v, want 3", got)
	}
	for _, d := range got {
		if d.Via != "parent" || d.Nonce != "" || d.RequestID == "" {
			t.Errorf("replayed decision = %+v, want Via parent, no nonce, the child's request", d)
		}
	}
	if got[0].KeyID != "k1" || got[2].KeyID != "k2" || got[0].Who != "alice" || got[1].Who != "bob" {
		t.Errorf("identities = %+v", got)
	}
	if len(decisionsFor(child, "c-gone")) != 0 {
		t.Error("a decision for a call that was not pending was recorded")
	}
	// The same replay again: the call is resolved, nothing records,
	// nothing errors — the pool's pump may repeat itself.
	rt, err = child.ReplayDecisions(ctx, approve(call.ID, "alice", "k1"), approve(call.ID, "alice", "k2"))
	if err != nil || rt != nil {
		t.Fatalf("a repeated replay: turn %v, err %v", rt, err)
	}
	if got := decisionsFor(child, call.ID); len(got) != 3 {
		t.Fatalf("a repeated replay recorded again: %d decisions", len(got))
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("the approved call: %v", got)
	}
}

// CancelDelegated denies a pool child's pending calls and resumes
// nothing: the canceled delegation does no more work, and its parked
// calls can never run on a later approval.
func TestCancelDelegated(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"5"}`}),
		wefttest.Say("done"),
	)
	child, err := thread.Create(ctx, thread.Memory(), agent, thread.WithLineage("s_parent", ""))
	if err != nil {
		t.Fatal(err)
	}
	t1, call := parkTurn(t, child, ctx)
	if err := child.CancelDelegated(ctx, "the delegation was canceled"); err != nil {
		t.Fatal(err)
	}
	if t1.Next() != nil {
		t.Fatal("CancelDelegated resumed the child")
	}
	got := decisionsFor(child, call.ID)
	if len(got) != 1 || got[0].Outcome != thread.OutcomeDeny || got[0].Via != "parent" || got[0].Who != "thread/pool" {
		t.Fatalf("the cancellation's record: %+v", got)
	}
	if pend := child.Pending(); len(pend) != 0 {
		t.Fatalf("Pending after the cancellation = %+v", pend)
	}
	if _, err := child.Decide(ctx, thread.Approve(call.ID)); !errors.Is(err, thread.ErrNotPending) {
		t.Fatalf("approving a canceled call: %v, want ErrNotPending", err)
	}
	if err := child.CancelDelegated(ctx, "again"); err != nil {
		t.Fatalf("a second CancelDelegated: %v", err)
	}
	if len(ran.snapshot()) != 0 {
		t.Fatal("the denied call ran")
	}
	plain, err := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	if err := plain.CancelDelegated(ctx, "x"); err == nil {
		t.Fatal("CancelDelegated on a session with no lineage: no error")
	}
}

// InheritApprovals gives a child the parent's nesting policy: the
// request lifetime read against the parent's clock, the quorum, the
// keyring and the signing rule — on Create and on Open alike.
func TestInheritApprovals(t *testing.T) {
	ctx := context.Background()
	ring, _ := signerRing(t)
	clock := newTestClock()
	st := thread.Memory()
	parent, err := thread.Create(ctx, st, weft.New(wefttest.Script()),
		thread.Clock(clock.Now), thread.RequestExpiry(time.Hour), thread.Quorum(2),
		thread.WithKeyring(ring), thread.RequireSigned())
	if err != nil {
		t.Fatal(err)
	}
	agent, _ := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"5"}`}),
		wefttest.Say("done"),
	)
	child, err := thread.Create(ctx, st, agent, thread.WithLineage(parent.ID(), ""), thread.InheritApprovals(parent))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, child, ctx)
	pend := child.Pending()
	if len(pend) != 1 || !pend[0].Expiry.Equal(clock.Now().Add(time.Hour)) {
		t.Fatalf("the child's request = %+v, want the parent's lifetime on the parent's clock", pend)
	}
	if _, err := child.Decide(ctx, thread.Approve(call.ID)); !errors.Is(err, thread.ErrSignatureRequired) {
		t.Fatalf("the unsigned door on the child: %v, want ErrSignatureRequired", err)
	}
	if _, err := child.Request(call.ID); err != nil {
		t.Fatalf("the child mints no challenge under the inherited ring: %v", err)
	}
	// One approval does not complete the inherited quorum of two.
	rt, err := child.ReplayDecisions(ctx, thread.ApprovalDecisionEntry{CallID: call.ID, Outcome: thread.OutcomeApprove, Who: "alice"})
	if err != nil || rt != nil {
		t.Fatalf("one approval under the inherited quorum: turn %v, err %v", rt, err)
	}
	// The signing rule is the header's; the rest is configuration, and
	// a reopen that inherits again has all of it.
	reopened, err := thread.Open(ctx, st, child.ID(), agent, thread.InheritApprovals(parent))
	if err != nil {
		t.Fatalf("reopening the child under the parent's policy: %v", err)
	}
	if _, err := reopened.Decide(ctx, thread.Approve(call.ID)); !errors.Is(err, thread.ErrSignatureRequired) {
		t.Fatalf("the unsigned door on the reopened child: %v", err)
	}
	if thread.InheritApprovals(nil) != nil {
		t.Error("InheritApprovals(nil) is not the nil option")
	}
}

// A mirror names its child: without one the entry would be an
// ordinary request with no parked call behind it.
func TestAppendApprovalRequestsNeedsChild(t *testing.T) {
	ctx := context.Background()
	s, err := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendApprovalRequests(ctx, thread.ApprovalRequestEntry{CallID: "call_1", Tool: "spend"}); err == nil {
		t.Fatal("a mirror naming no child was accepted")
	}
	if n := len(s.Entries()); n != 0 {
		t.Fatalf("the refused mirror wrote %d entries", n)
	}
}
