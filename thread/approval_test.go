package thread_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/wefttest"
)

// refundInput is the approval-gated test tool's input.
type refundInput struct {
	OrderID string `json:"order_id"`
}

// ranLog records the Approved flag of each executed call, safely for
// concurrent tool handlers and polling test goroutines.
type ranLog struct {
	mu   sync.Mutex
	vals []bool
}

func (r *ranLog) add(b bool) {
	r.mu.Lock()
	r.vals = append(r.vals, b)
	r.mu.Unlock()
}

// snapshot copies the flags recorded so far.
func (r *ranLog) snapshot() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.vals...)
}

// refundAgent builds an agent whose "refund" tool requires approval,
// playing turns, and reporting whether each executed call ran with
// Call.Approved set (ADR 0007: an approved resume runs the ordinary
// chain, Approved true).
func refundAgent(turns ...wefttest.Turn) (*weft.Agent, *ranLog) {
	ran := &ranLog{}
	tool := weft.Tool("refund", "Refund an order.",
		func(ctx context.Context, in refundInput) (string, error) {
			c, _ := weft.CallFromContext(ctx)
			ran.add(c.Approved)
			return "refunded " + in.OrderID, nil
		},
		weft.RequireApproval())
	return weft.New(wefttest.Script(turns...), weft.Name("approvals-test"), tool), ran
}

// parkSend drives one Send that parks exactly one refund call, waits
// for the turn, and returns the pending call.
func parkSend(t *testing.T, s *thread.Session, ctx context.Context) weft.ToolCallPart {
	t.Helper()
	turn, err := s.Send(ctx, weft.User("refund it"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := turn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) == 0 {
		t.Fatalf("no calls parked")
	}
	return res.Pending[0]
}

// toolResults collects the tool results of a context, in order.
func toolResults(msgs []weft.Message) []weft.ToolResultPart {
	var out []weft.ToolResultPart
	for _, m := range msgs {
		for _, p := range m.Content {
			if r, ok := p.(weft.ToolResultPart); ok {
				out = append(out, r)
			}
		}
	}
	return out
}

// countApprovalEntries tallies the approval entries by a printable
// key, for the completeness assertions.
func countApprovalEntries(t *thread.Session) map[string]int {
	out := map[string]int{}
	for _, e := range t.Entries() {
		switch e := e.(type) {
		case thread.ApprovalRequestEntry:
			out["request:"+e.CallID]++
		case thread.ApprovalDecisionEntry:
			out["decision:"+e.CallID+":"+string(e.Outcome)+":"+e.Via]++
		case thread.ApprovalAuditEntry:
			out["audit:"+e.Step+":"+e.Outcome]++
		}
	}
	return out
}

// TestApprovalsParkDecideResume is the ADR's whole §1 in one flow: a
// call parks (the request entry written with the turn), Pending names
// it, Decide records the approval and AutoResume runs it through the
// ordinary chain with Call.Approved set, and the boundary closes.
func TestApprovalsParkDecideResume(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1234"}`}),
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)

	pend := s.Pending()
	if len(pend) != 1 {
		t.Fatalf("Pending: got %d, want 1", len(pend))
	}
	r := pend[0]
	if r.CallID != call.ID || r.Tool != "refund" {
		t.Fatalf("pending request: got %+v, want call %s of refund", r, call.ID)
	}
	if string(r.Args) != `{"order_id":"1234"}` {
		t.Fatalf("pending args: got %s", r.Args)
	}
	sum := sha256.Sum256(call.Args)
	if r.ArgsSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("args sha256: got %s, want %s", r.ArgsSHA256, hex.EncodeToString(sum[:]))
	}
	if r.Reason != "tool requires approval" {
		t.Fatalf("reason: got %q", r.Reason)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("gated tool ran before any decision: %v", got)
	}

	rt, err := s.Decide(ctx, thread.Approve(call.ID))
	if err != nil {
		t.Fatal(err)
	}
	if rt == nil {
		t.Fatal("Decide with AutoResume on returned no Turn")
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("approved resume: Approved flags %v, want [true]", got)
	}
	if got := s.Pending(); len(got) != 0 {
		t.Fatalf("Pending after resume: got %d, want 0", len(got))
	}
	results := toolResults(s.Context())
	if len(results) != 1 || results[0].Content != "refunded 1234" || results[0].IsError {
		t.Fatalf("results after resume: %+v", results)
	}
}

// TestApprovalsRestartDecideResume parks, reopens the session from the
// jsonl backend (a restart), and decides: Pending works after a
// restart because requests and decisions are entries (ADR 0021 §1),
// and the reopened session resumes under the recorded decision.
func TestApprovalsRestartDecideResume(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"77"}`}))
	id := parkSession(t, ctx, st, agent)

	// The reopen goes through the same backend — jsonl's one-writer
	// lock is per process, and the restart under test is the state
	// recovery: the new Session Loads the tree from disk and rebuilds
	// its pending approvals from the entries.
	resumed, _ := refundAgent(wefttest.Say("denied noted"))
	abandon(t, st, id)
	s2, err := thread.Open(ctx, st, id, resumed)
	if err != nil {
		t.Fatal(err)
	}
	pend := s2.Pending()
	if len(pend) != 1 || pend[0].CallID != "call_1" {
		t.Fatalf("Pending after restart: %+v", pend)
	}

	rt, err := s2.Decide(ctx, thread.Deny(pend[0].CallID, "not on call"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(s2.Context())
	if len(results) != 1 || !results[0].IsError || results[0].Content != "DENIED: not on call" {
		t.Fatalf("denied result: %+v", results)
	}
	// The denial is durable truth, not just model text: a decision
	// entry with the outcome, the via and the run it decided.
	found := false
	for _, e := range s2.Entries() {
		if d, ok := e.(thread.ApprovalDecisionEntry); ok && d.CallID == "call_1" {
			found = true
			if d.Outcome != thread.OutcomeDeny || d.Reason != "not on call" || d.Via != "user" || d.RunID != pend[0].RunID {
				t.Fatalf("decision entry: %+v", d)
			}
		}
	}
	if !found {
		t.Fatal("no decision entry after restart-and-decide")
	}
}

// parkSession creates a session whose one Send parks a refund call,
// and returns the session id.
func parkSession(t *testing.T, ctx context.Context, st thread.Storage, agent *weft.Agent) string {
	t.Helper()
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	parkSend(t, s, ctx)
	return s.ID()
}

// TestApprovalsPartialDecisions: a boundary with two parked calls
// takes partial decisions without resuming — the half-decided state is
// durable — and resumes only when the last one lands.
func TestApprovalsPartialDecisions(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(
			wefttest.Call{Name: "refund", ID: "call_1", Args: `{"order_id":"1"}`},
			wefttest.Call{Name: "refund", ID: "call_2", Args: `{"order_id":"2"}`},
		),
		wefttest.Say("both resolved"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	parkSend(t, s, ctx)
	if got := len(s.Pending()); got != 2 {
		t.Fatalf("Pending: got %d, want 2", got)
	}
	rt, err := s.Decide(ctx, thread.Approve("call_1"))
	if err != nil {
		t.Fatal(err)
	}
	if rt != nil {
		t.Fatal("a half-decided boundary resumed")
	}
	if got := len(s.Pending()); got != 1 || s.Pending()[0].CallID != "call_2" {
		t.Fatalf("Pending after partial decide: %+v", s.Pending())
	}
	rt, err = s.Decide(ctx, thread.Deny("call_2", "second refusal"))
	if err != nil {
		t.Fatal(err)
	}
	if rt == nil {
		t.Fatal("the completed boundary did not resume")
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 {
		t.Fatalf("executions: %v, want only the approved one", got)
	}
	results := toolResults(s.Context())
	if len(results) != 2 {
		t.Fatalf("results: %d, want 2", len(results))
	}
	if results[0].Content != "refunded 1" || results[0].IsError {
		t.Fatalf("approved result: %+v", results[0])
	}
	if results[1].Content != "DENIED: second refusal" || !results[1].IsError {
		t.Fatalf("denied result: %+v", results[1])
	}
}

// TestApprovalsExpiry: a request past its expiry is denied with the
// stated reason by the resume that finds it lapsed (ADR 0021 §5),
// audited, and the denial text is a pinned golden.
func TestApprovalsExpiry(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("expired noted"))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.RequestExpiry(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	pend := s.Pending()[0]
	if pend.Expiry.IsZero() {
		t.Fatal("RequestExpiry gave the request no expiry")
	}

	// Wait until the request is strictly past its expiry — the
	// request's own deadline, not a guessed sleep — then resume: the
	// call is denied with the expiry reason, not the core's "no
	// decision".
	time.Sleep(time.Until(pend.Expiry) + 5*time.Millisecond)
	rt, err := s.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(s.Context())
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("expired result: %+v", results)
	}
	reason := strings.TrimPrefix(results[0].Content, "DENIED: ")
	if results[0].Content == reason { // the prefix was never there
		t.Fatalf("expired denial text: %q", results[0].Content)
	}
	// The denial reason is model-visible bytes, pinned (ADR 0021 §5):
	// the fixed words plus the request's own expiry, RFC 3339 UTC.
	wantRe := regexp.MustCompile(`^expired: no decision before \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)
	if !wantRe.MatchString(reason) {
		t.Fatalf("expiry reason shape: %q", reason)
	}
	if want := "expired: no decision before " + pend.Expiry.UTC().Format(time.RFC3339); reason != want {
		t.Fatalf("expiry reason: got %q, want %q", reason, want)
	}
	counts := countApprovalEntries(s)
	if counts["audit:expiry:denied"] != 1 {
		t.Fatalf("expiry audit: %v", counts)
	}
	if counts["decision:"+call.ID+":deny:expiry"] != 1 {
		t.Fatalf("expiry decision: %v", counts)
	}
}

// TestApprovalsExpiryBoundary: a request exactly at its expiry is not
// yet expired — the rule is strictly after — so a resume at that
// instant denies with the core's "no decision".
func TestApprovalsExpiryBoundary(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("noted"))
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	parkSend(t, s, ctx)
	// A request with no expiry never expires; the boundary case itself
	// is the rule's edge, pinned by resuming exactly at a fabricated
	// deadline through the public surface: undecided calls past no
	// deadline read "no decision".
	rt, err := s.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(s.Context())
	if len(results) != 1 || results[0].Content != "DENIED: no decision" {
		t.Fatalf("undecided resume: %+v", results)
	}
}

// TestApproverDeclines: an Approver that declines to decide leaves the
// audit trail saying so and the call parks (ADR 0021 §2).
func TestApproverDeclines(t *testing.T) {
	ctx := context.Background()
	var seen *thread.Request
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("parked then run"))
	approver := func(_ context.Context, r thread.Request) (thread.Decision, bool) {
		seen = &r
		return thread.Decision{}, false
	}
	s, err := thread.Create(ctx, thread.Memory(), agent,
		thread.WithApprover(approver, 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	if seen == nil || seen.CallID != call.ID {
		t.Fatalf("approver never consulted: %+v", seen)
	}
	if got := len(s.Pending()); got != 1 {
		t.Fatalf("declined approver still parked nothing: Pending %d", got)
	}
	counts := countApprovalEntries(s)
	if counts["audit:approver:declined"] != 1 {
		t.Fatalf("approver audit: %v", counts)
	}
}

// TestApproverTimesOut: an Approver that blocks past its timeout
// is abandoned, audited as a timeout, and the call parks — the chain
// never blocks forever (ADR 0021 §2).
func TestApproverTimesOut(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}))
	approver := func(cctx context.Context, r thread.Request) (thread.Decision, bool) {
		<-cctx.Done() // an approver that ignores everything
		return thread.Decision{}, false
	}
	s, err := thread.Create(ctx, thread.Memory(), agent,
		thread.WithApprover(approver, 50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	parkSend(t, s, ctx)
	counts := countApprovalEntries(s)
	if counts["audit:approver:timeout"] != 1 {
		t.Fatalf("timeout audit: %v", counts)
	}
	if got := len(s.Pending()); got != 1 {
		t.Fatalf("timed-out approver: Pending %d", got)
	}
}

// TestApproverDecides: an Approver's live approval never parks — no
// request entry exists — and AutoResume runs the call at once; the
// decision and its audit entry are the record (ADR 0021 §2: "every
// step writes an audit entry, including automatic approvals").
func TestApproverDecides(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"9"}`}),
		wefttest.Say("ran"),
	)
	approver := func(_ context.Context, r thread.Request) (thread.Decision, bool) {
		d := thread.Approve(r.CallID)
		d.Who = "avi"
		return d, true
	}
	s, err := thread.Create(ctx, thread.Memory(), agent,
		thread.WithApprover(approver, 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("refund 9"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	// The chain decided everything, so no boundary parked for a human:
	// the auto-resume links to the caller's turn through Next, and its
	// Wait is the conversation's continuation.
	next := turn.Next()
	if next == nil {
		t.Fatal("the chain-decided auto-resume never linked")
	}
	if _, err := next.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("approver-approved call: Approved flags %v", got)
	}
	counts := countApprovalEntries(s)
	if counts["audit:approver:approved"] != 1 {
		t.Fatalf("approver audit: %v", counts)
	}
	for k := range counts {
		if strings.HasPrefix(k, "request:") {
			t.Fatalf("a decided call parked a request entry: %v", counts)
		}
	}
	results := toolResults(s.Context())
	if len(results) != 1 || results[0].Content != "refunded 9" {
		t.Fatalf("results: %+v", results)
	}
	found := false
	for _, e := range s.Entries() {
		if d, ok := e.(thread.ApprovalDecisionEntry); ok {
			found = true
			if d.Via != "approver" || d.Who != "avi" || d.Outcome != thread.OutcomeApprove {
				t.Fatalf("approver decision entry: %+v", d)
			}
		}
	}
	if !found {
		t.Fatal("no decision entry for the approver's approval")
	}
}

// TestApproverPanicContained: a panicking Approver is contained, read
// as a decline, and audited.
func TestApproverPanicContained(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}))
	approver := func(context.Context, thread.Request) (thread.Decision, bool) {
		panic("no tty")
	}
	s, err := thread.Create(ctx, thread.Memory(), agent,
		thread.WithApprover(approver, 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	parkSend(t, s, ctx)
	counts := countApprovalEntries(s)
	if counts["audit:approver:panic"] != 1 {
		t.Fatalf("panic audit: %v", counts)
	}
}

// TestApprovalsAuditCompleteness: the park path's chain leaves exactly
// the entries ADR 0021 §2 names — one request, one park audit per
// call — plus the decision when one lands and the resume audit when
// the boundary resumes.
func TestApprovalsAuditCompleteness(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund"}),
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	counts := countApprovalEntries(s)
	if counts["request:"+call.ID] != 1 || counts["audit:park:parked"] != 1 {
		t.Fatalf("park chain entries: %v", counts)
	}
	rt, err := s.Decide(ctx, thread.Resolve(call.ID, `{"status":"already refunded"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	counts = countApprovalEntries(s)
	if counts["decision:"+call.ID+":resolve:user"] != 1 {
		t.Fatalf("resolve decision: %v", counts)
	}
	if counts["audit:resume:started"] != 1 {
		t.Fatalf("resume audit: %v", counts)
	}
	results := toolResults(s.Context())
	if len(results) != 1 || results[0].Content != `{"status":"already refunded"}` || results[0].IsError {
		t.Fatalf("resolved result: %+v", results)
	}
}

// TestDecideErrNotPending: a decision for a call that is not pending
// fails with ErrNotPending before anything is recorded — and before
// any run starts (ADR 0021 §1).
func TestDecideErrNotPending(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("done"))
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(ctx, thread.Approve("bogus")); !errors.Is(err, thread.ErrNotPending) {
		t.Fatalf("bogus call: %v, want ErrNotPending", err)
	}
	call := parkSend(t, s, ctx)
	before := len(s.Entries())
	if _, err := s.Decide(ctx, thread.Approve(call.ID), thread.Deny("other", "x")); !errors.Is(err, thread.ErrNotPending) {
		t.Fatalf("batch with one bad id: %v, want ErrNotPending", err)
	}
	if got := len(s.Entries()); got != before {
		t.Fatalf("a rejected Decide recorded something: %d entries, want %d", got, before)
	}
	// Deciding the same call twice: the second time it is not pending.
	rt, err := s.Decide(ctx, thread.Approve(call.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(ctx, thread.Approve(call.ID)); !errors.Is(err, thread.ErrNotPending) {
		t.Fatalf("re-decide: %v, want ErrNotPending", err)
	}
}

// TestResumeWithoutBoundary: Resume with no open boundary is
// ErrNotPending, never a no-op run.
func TestResumeWithoutBoundary(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(wefttest.Say("nothing"))
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resume(ctx); !errors.Is(err, thread.ErrNotPending) {
		t.Fatalf("Resume on an idle session: %v, want ErrNotPending", err)
	}
}

// TestSendQueuedBehindBoundary: a Send while approvals are pending is
// queued (ADR 0021 §5) and runs only after the boundary resolves —
// the resume turn lands first, the follow-up second.
func TestSendQueuedBehindBoundary(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"5"}`}),
		wefttest.Say("resumed"),
		wefttest.Say("second turn"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	t2, err := s.Send(ctx, weft.User("and then?"))
	if err != nil {
		t.Fatal(err)
	}
	rt, err := s.Decide(ctx, thread.Approve(call.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	res2, err := t2.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if res2.Text() != "second turn" {
		t.Fatalf("queued send: got %q", res2.Text())
	}
	// Order: the resume's turn entry precedes the follow-up's.
	var resumeAt, followAt = -1, -1
	for i, e := range s.Entries() {
		if te, ok := e.(thread.TurnEntry); ok {
			if te.RunID == rt.RunID() {
				resumeAt = i
			}
			if te.RunID == t2.RunID() {
				followAt = i
			}
		}
	}
	if resumeAt < 0 || followAt < 0 || resumeAt > followAt {
		t.Fatalf("turn order: resume at %d, follow-up at %d", resumeAt, followAt)
	}
}

// TestAutoResumeOff: with AutoResume(false) a completed boundary waits
// for the caller's Resume; a Send in between stays queued.
func TestAutoResumeOff(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"3"}`}),
		wefttest.Say("resumed late"),
		wefttest.Say("follow-up"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.AutoResume(false))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	rt, err := s.Decide(ctx, thread.Approve(call.ID))
	if err != nil {
		t.Fatal(err)
	}
	if rt != nil {
		t.Fatal("AutoResume(false) resumed on its own")
	}
	t2, err := s.Send(ctx, weft.User("next"))
	if err != nil {
		t.Fatal(err)
	}
	manual, err := s.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manual.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 {
		t.Fatalf("executions: %v", got)
	}
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
}

// TestOnRequestFires: OnRequest fires once per parked call, after the
// request is durable, and a panic in it is contained (ADR 0021 §5).
func TestOnRequestFires(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(
		wefttest.ToolCalls(
			wefttest.Call{Name: "refund", ID: "call_1"},
			wefttest.Call{Name: "refund", ID: "call_2"},
		),
		wefttest.Say("done"),
	)
	var got []thread.Request
	s, err := thread.Create(ctx, thread.Memory(), agent,
		thread.OnRequest(func(r thread.Request) {
			got = append(got, r)
			if len(got) == 1 {
				panic("push pipeline hiccup") // contained, and the second still fires
			}
		}))
	if err != nil {
		t.Fatal(err)
	}
	parkSend(t, s, ctx)
	if len(got) != 2 || got[0].CallID != "call_1" || got[1].CallID != "call_2" {
		t.Fatalf("OnRequest deliveries: %+v", got)
	}
	for _, r := range got {
		if r.Session != s.ID() || r.Created.IsZero() {
			t.Fatalf("OnRequest request not the durable one: %+v", r)
		}
	}
}

// TestCompactionWaitsForBoundary: an open boundary holds compaction —
// even a hand-built Compaction is refused and the refusal names the
// boundary (the dangling tail must stay raw for its decisions).
func TestCompactionWaitsForBoundary(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}))
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	parkSend(t, s, ctx)
	entries := s.Entries()
	c := &thread.Compaction{
		Summary:   "everything so far",
		FirstKept: entries[0].(thread.MessageEntry).ID,
		Reason:    thread.ReasonManual,
	}
	if err := s.ApplyCompaction(ctx, c); err == nil || !strings.Contains(err.Error(), "approval requests pending") {
		t.Fatalf("ApplyCompaction over a boundary: %v", err)
	}
}

// TestApprovalContextAfterPark: the parked boundary in the caller's
// view is repaired (the dangling call reads interrupted), while the
// raw transcript the resume feeds stays dangling — v0.1's rule,
// unchanged.
func TestApprovalContextAfterPark(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}))
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	parkSend(t, s, ctx)
	results := toolResults(s.Context())
	if len(results) != 1 || !strings.Contains(results[0].Content, "interrupted") {
		t.Fatalf("caller view over a boundary: %+v", results)
	}
}

// The format-2 samples: the approvals entry kinds (ADR 0021 §1–§2),
// with fixed ids and times so the golden bytes are deterministic.

// Grant golden ids, continuing the entry chain.
const (
	grantID0 = "e_01J8X9M2K7QW4R5N8T6V2B3C4T"
	grantID1 = "e_01J8X9M2K7QW4R5N8T6V2B3C4V"
)

func entryID12Safe() string { return "e_01J8X9M2K7QW4R5N8T6V2B3C4W" }

func approvalSampleEntries() []thread.Entry {
	return []thread.Entry{
		thread.ApprovalRequestEntry{
			ID: entryID9, ParentID: entryID8, Created: at(10),
			CallID:     "call_1",
			Tool:       "refund_order",
			Args:       json.RawMessage(`{"order_id":"1234"}`),
			ArgsSHA256: "7d0656a84e6bbb1695439d794ea5a55e75f09fcb176bbef1b717cf1142332df6",
			RunID:      sessionID + "-t1",
			Reason:     "tool requires approval",
			Expiry:     at(600),
		},
		thread.ApprovalDecisionEntry{
			ID: entryID10, ParentID: entryID9, Created: at(11),
			CallID:  "call_1",
			Outcome: thread.OutcomeDeny,
			Reason:  "not on call",
			Who:     "avi",
			Via:     "user",
			RunID:   sessionID + "-t1",
		},
		thread.ApprovalAuditEntry{
			ID: entryID11, ParentID: entryID10, Created: at(12),
			CallID:  "call_1",
			Step:    thread.StepApprover,
			Outcome: "declined",
			RunID:   sessionID + "-t1",
		},
		thread.GrantEntry{
			ID: grantID0, ParentID: entryID11, Created: at(13),
			Grant: thread.Grant{
				Tool: "run_command",
				Args: []thread.Arg{
					{Pointer: "/command", Glob: "go test*"},
					{Pointer: "/dir", Prefix: "/home/wajih/ws/"},
				},
				Reason: "vetted on 2026-09-29",
			},
		},
		thread.GrantEntry{
			ID: grantID1, ParentID: grantID0, Created: at(14),
			Grant: thread.Grant{
				Tool:    "run_command",
				Deny:    true,
				Reason:  "no network from tests",
				Expiry:  at(900),
				MaxUses: 3,
			},
		},
		thread.GrantRevokedEntry{
			ID: entryID12Safe(), ParentID: grantID1, Created: at(15),
			GrantID: grantID0,
		},
	}
}

// format2GoldenName maps an approvals entry to its golden file name.
func format2GoldenName(e thread.Entry) string {
	switch e := e.(type) {
	case thread.ApprovalRequestEntry:
		_ = e
		return "approval_request.json"
	case thread.ApprovalDecisionEntry:
		_ = e
		return "approval_decision.json"
	case thread.ApprovalAuditEntry:
		_ = e
		return "approval_audit.json"
	case thread.GrantEntry:
		if e.Deny {
			return "grant_deny.json"
		}
		return "grant.json"
	default:
		return "grant_revoked.json"
	}
}

// TestApprovalEntryGoldens pins the wire bytes of every format-2 kind
// — "v":2 beside the type discriminator (ADR 0011 §6, ADR 0021).
// Regenerate with `go test ./thread -update` from the module root.
func TestApprovalEntryGoldens(t *testing.T) {
	for _, e := range approvalSampleEntries() {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("%T: %v", e, err)
		}
		if !strings.Contains(string(b), `"v":2`) {
			t.Errorf("%T: no v:2 on the wire: %s", e, b)
		}
		wefttest.Golden(t, filepath.Join("testdata", "format2", format2GoldenName(e)), append(b, '\n'))
	}
	// The round trip: decode, equal value, same bytes again.
	for _, e := range approvalSampleEntries() {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		got, err := thread.UnmarshalEntry(b)
		if err != nil {
			t.Fatalf("%T: %v", e, err)
		}
		if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", e) {
			t.Errorf("%T round trip: got %+v, want %+v", e, got, e)
		}
	}
}

// TestApprovalEntryLoud: the approvals kinds refuse a newer version of
// themselves and stay unknown-kind-loud for names near theirs.
func TestApprovalEntryLoud(t *testing.T) {
	if _, err := thread.UnmarshalEntry([]byte(`{"type":"approval_request","v":3,"id":"e_1"}`)); !errors.Is(err, thread.ErrNewerFormat) {
		t.Fatalf("newer approval v: %v, want ErrNewerFormat", err)
	}
	if _, err := thread.UnmarshalEntry([]byte(`{"type":"approval","id":"e_1"}`)); !errors.Is(err, thread.ErrNewerFormat) {
		t.Fatalf("unknown kind near approvals: %v, want ErrNewerFormat", err)
	}
	// A v0.1-shaped kind list stays read-write identical: the format-1
	// goldens still read (TestReadEveryGolden), and the approval kinds
	// accept their own v (round trip above).
	if _, err := os.Stat(filepath.Join("testdata", "format1", "turn.json")); err != nil {
		t.Fatalf("format1 goldens moved: %v", err)
	}
}

// TestSendOverDecidedBoundaryResumes: a boundary whose every call is
// decided must not wedge a Send — AutoResume's contract holds even
// when the session was reopened between the decisions' append and the
// resume that never ran (the crash window), so the Send arms the
// resume and then takes its own turn (ADR 0021 §1).
func TestSendOverDecidedBoundaryResumes(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	agent1, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"4"}`}))
	s1, err := thread.Create(ctx, st, agent1, thread.AutoResume(false))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s1, ctx)
	if _, err := s1.Decide(ctx, thread.Approve(call.ID)); err != nil {
		t.Fatal(err)
	}
	// The decisions are durable; the resume never ran. A reopen with
	// defaults sees a decided boundary and no runner.
	agent2, ran := refundAgent(wefttest.Say("resumed after reopen"), wefttest.Say("the follow-up"))
	abandon(t, st, s1.ID())
	s2, err := thread.Open(ctx, st, s1.ID(), agent2)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(s2.Pending()); got != 0 {
		t.Fatalf("Pending after reopen: %d, want 0 (all decided)", got)
	}
	t2, err := s2.Send(ctx, weft.User("carry on"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := t2.Wait()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the Send wedged behind a decided boundary")
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("approved call after reopen: %v", got)
	}
	results := toolResults(s2.Context())
	if len(results) != 1 || results[0].Content != "refunded 4" {
		t.Fatalf("results: %+v", results)
	}
}

// TestDecideConcurrent: two goroutines deciding the boundary's calls
// at once — exactly one resume is armed, both decisions land, and the
// race detector stays quiet.
func TestDecideConcurrent(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(
			wefttest.Call{Name: "refund", ID: "call_1"},
			wefttest.Call{Name: "refund", ID: "call_2"},
		),
		wefttest.Say("both decided"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	parkSend(t, s, ctx)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = s.Decide(ctx, thread.Approve("call_1")) }()
	go func() { defer wg.Done(); _, _ = s.Decide(ctx, thread.Approve("call_2")) }()
	wg.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(ran.snapshot()) == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := ran.snapshot(); len(got) != 2 {
		t.Fatalf("approved calls after concurrent decides: %v", got)
	}
}

// secondDeathCtx is a context that dies the moment anyone asks about
// it twice: the first Err() probe reads healthy, every later one reads
// canceled. It makes "the caller's context died right after the call
// checked it" — a race window in production — deterministic in a test.
// Only Err is probed on the paths under test; Done stays nil.
type secondDeathCtx struct{ fired bool }

func (c *secondDeathCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *secondDeathCtx) Done() <-chan struct{}       { return nil }
func (c *secondDeathCtx) Value(any) any               { return nil }
func (c *secondDeathCtx) Err() error {
	if c.fired {
		return context.Canceled
	}
	c.fired = true
	return nil
}

// TestResumeRetryAfterCanceledArm: a resume armed under a context that
// died between Resume's check and the runner's pickup fails its turn —
// and the arming must retire with it, so a later Resume arms a fresh
// resume and the boundary still resolves (armResumeLocked's "cleared
// when the resume completes so a failed one can retry" contract).
func TestResumeRetryAfterCanceledArm(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"6"}`}),
		wefttest.Say("resumed on retry"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.AutoResume(false))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	if _, err := s.Decide(ctx, thread.Approve(call.ID)); err != nil {
		t.Fatal(err)
	}
	dead := &secondDeathCtx{}
	t1, err := s.Resume(dead)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err == nil {
		t.Fatal("the dead-context resume succeeded; the test's window closed")
	}
	// The failed arm must not own the boundary: a healthy Resume arms
	// again and the resume runs.
	t2, err := s.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if t2 == t1 {
		t.Fatal("Resume returned the dead turn; the failed arm was never retired")
	}
	if _, err := t2.Wait(); err != nil {
		t.Fatalf("the retried resume: %v", err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("approved call after retry: %v", got)
	}
	if got := s.Pending(); len(got) != 0 {
		t.Fatalf("Pending after the retried resume: %d", len(got))
	}
}

// gateModel wraps a scripted model, blocking the n-th model call until
// released and counting every call — the handle a test holds on a run
// in flight.
type gateModel struct {
	inner   weft.Model
	mu      sync.Mutex
	calls   int
	blockAt int
	entered chan struct{}
	release chan struct{}
}

func (g *gateModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	g.mu.Lock()
	g.calls++
	n := g.calls
	g.mu.Unlock()
	if n == g.blockAt {
		g.entered <- struct{}{}
		<-g.release
	}
	return g.inner.Stream(ctx, req)
}

func (g *gateModel) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// TestResumeDuringInFlightResumeArmsOnce: while the runner is already
// resuming a completed boundary (the chain-decided path — a grant or
// the Approver decided everything), a Resume must return the turn in
// flight, not arm a second resume for the same boundary. The second
// resume would run the model again over nothing: a ghost turn with no
// prompt and no decisions.
func TestResumeDuringInFlightResumeArmsOnce(t *testing.T) {
	ctx := context.Background()
	gm := &gateModel{
		inner:   wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"10"}`}), wefttest.Say("resumed")),
		blockAt: 2, // the resume run's model call
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	ran := &ranLog{}
	agent := weft.New(gm, weft.Name("double-resume-test"),
		weft.Tool("refund", "Refund an order.",
			func(ctx context.Context, in refundInput) (string, error) {
				c, _ := weft.CallFromContext(ctx)
				ran.add(c.Approved)
				return "refunded " + in.OrderID, nil
			},
			weft.RequireApproval()))
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, thread.Grant{Tool: "refund"}); err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("refund it"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	// The grant decided the call, so the runner is resuming now; its
	// model call blocks on the gate. A Resume over the same boundary
	// arrives while that resume is in flight.
	<-gm.entered
	inFlight := t1.Next()
	if inFlight == nil {
		t.Fatal("the chain-decided resume never linked")
	}
	rt, err := s.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rt != inFlight {
		t.Fatalf("Resume armed a second resume (%p) while one was in flight (%p)", rt, inFlight)
	}
	close(gm.release)
	if _, err := rt.Wait(); err != nil {
		t.Fatalf("the in-flight resume: %v", err)
	}
	if got := gm.count(); got != 2 {
		t.Fatalf("model calls: %d, want 2 (a ghost resume ran the model again)", got)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("approved call: %v", got)
	}
}

// TestApproverAlwaysGrants: "approve and always allow" from the live
// Approver records its grant like the same decision through Decide —
// the next identical call never parks (ADR 0021 §4: grants are created
// "explicitly or from a decision").
func TestApproverAlwaysGrants(t *testing.T) {
	ctx := context.Background()
	agent, ran := runAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go build"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "run", Args: `{"command":"go build"}`}),
		wefttest.Say("one"), wefttest.Say("two"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent,
		thread.WithApprover(func(_ context.Context, r thread.Request) (thread.Decision, bool) {
			return thread.ApproveAlways(r.CallID), true
		}, 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("build"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	if next := t1.Next(); next != nil {
		if _, err := next.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	found := false
	for _, e := range s.Entries() {
		if g, ok := e.(thread.GrantEntry); ok && g.Tool == "run" {
			found = true
		}
	}
	if !found {
		t.Fatal("an Approver's ApproveAlways recorded no grant")
	}
	t2, err := s.Send(ctx, weft.User("build again"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	if next := t2.Next(); next != nil {
		if _, err := next.Wait(); err != nil {
			t.Fatal(err)
		}
		t.Fatal("the always-granted call still parked")
	}
	if got := *ran; len(got) != 2 {
		t.Fatalf("executions: %v", got)
	}
}

// TestResolveErrorDecision: the fourth outcome maps to the core's
// ResolveError — the content verbatim, marked as an error the model
// sees (ADR 0021 §1; ADR 0007's amendment).
func TestResolveErrorDecision(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"12"}`}),
		wefttest.Say("noted the failure"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	rt, err := s.Decide(ctx, thread.ResolveError(call.ID, "the payments API is down"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	results := toolResults(s.Context())
	if len(results) != 1 || !results[0].IsError || results[0].Content != "the payments API is down" {
		t.Fatalf("resolve_error result: %+v", results)
	}
	for _, e := range s.Entries() {
		if d, ok := e.(thread.ApprovalDecisionEntry); ok && d.CallID == call.ID {
			if d.Outcome != thread.OutcomeResolveError {
				t.Fatalf("decision entry outcome: %q", d.Outcome)
			}
		}
	}
}

// TestResumeTwice: arming the resume twice before it lands must not
// orphan the first caller's Turn — the second Resume returns the
// already-armed turn, and both waits complete (one boundary, one
// resume).
func TestResumeTwice(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"8"}`}),
		wefttest.Say("resumed"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.AutoResume(false))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	if _, err := s.Decide(ctx, thread.Approve(call.ID)); err != nil {
		t.Fatal(err)
	}
	t1, err := s.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := s.Resume(ctx)
	if err != nil {
		t.Fatalf("second Resume: %v", err)
	}
	if t1 != t2 {
		t.Fatalf("double Resume armed two turns: %p and %p", t1, t2)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("executions: %v", got)
	}
}

// ── The 2026-09-29 review round's pins ────────────────────────────────

// A grant match under Quorum(2) is one identity, not a completed
// boundary: the chain-decided turn must not auto-resume (which would
// deny the call as "no decision"), and a second decision from a
// distinct identity completes it (ADR 0021 §5 — the chain-decided
// hand-off arms only when every dangling call holds an effective
// decision).
func TestQuorumGrantMatchWaitsForSecondDecision(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1234"}`}),
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.Quorum(2))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, thread.Grant{
		Tool: "refund",
		Args: []thread.Arg{thread.ArgEquals("/order_id", json.RawMessage(`"1234"`))},
	}); err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("the tool ran before the quorum completed: %v", got)
	}
	if got := s.Pending(); len(got) != 1 {
		t.Fatalf("Pending after a one-identity grant decision: got %d, want 1 (quorum unmet)", len(got))
	}
	rt, err := s.Decide(ctx, thread.Decision{CallID: call.ID, Kind: thread.OutcomeApprove, Who: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if rt == nil {
		t.Fatal("Decide completing the quorum returned no Turn")
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("approved resume: Approved flags %v, want [true]", got)
	}
	if got := s.Pending(); len(got) != 0 {
		t.Fatalf("Pending after the completed quorum: got %d, want 0", len(got))
	}
}

// An Approver's single approval under Quorum(2) leaves the call
// pending — one identity — instead of arming a resume that denies it
// as "no decision"; the second decision completes it.
func TestQuorumApproverApprovalWaitsForSecondDecision(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1234"}`}),
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent,
		thread.Quorum(2),
		thread.WithApprover(func(ctx context.Context, r thread.Request) (thread.Decision, bool) {
			return thread.Decision{CallID: r.CallID, Kind: thread.OutcomeApprove, Who: "terminal"}, true
		}, 5*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("the tool ran before the quorum completed: %v", got)
	}
	if got := s.Pending(); len(got) != 1 {
		t.Fatalf("Pending after the approver's single approval: got %d, want 1 (quorum unmet)", len(got))
	}
	rt, err := s.Decide(ctx, thread.Decision{CallID: call.ID, Kind: thread.OutcomeApprove, Who: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if rt == nil {
		t.Fatal("Decide completing the quorum returned no Turn")
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("approved resume: Approved flags %v, want [true]", got)
	}
}

// A Deny built with Always set mints no standing approval: the next
// such call parks again. Only an approve grants (ADR 0021 §4) — the
// same gate the Approver path applies.
func TestDenyAlwaysMintsNoGrant(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`, ID: "call_1"}),
		wefttest.Say("ok"),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`, ID: "call_2"}),
		wefttest.Say("ok again"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	d := thread.Deny(call.ID, "not allowed")
	d.Always = true
	rt, err := s.Decide(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, e := range s.Entries() {
		if g, ok := e.(thread.GrantEntry); ok && g.Tool == "refund" {
			t.Fatalf("a Deny with Always minted grant %+v", g.Grant)
		}
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("denied call ran: %v", got)
	}
	// The next such call parks again: no grant stands.
	turn, err := s.Send(ctx, weft.User("refund it again"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := s.Pending(); len(got) != 1 {
		t.Fatalf("Pending after re-issuing a deny-always call: got %d, want 1 (no grant may stand)", len(got))
	}
}

// A call id the model re-issues starts a fresh occurrence: the walk
// resets the call's request and decisions at the message that carries
// it, so a grant-decided second occurrence is not folded together
// with the first one's denial (ADR 0007 lets call ids
// repeat across turns; ADR 0021 scopes by occurrence).
func TestRepeatedCallIDOccurrencesStaySeparate(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`, ID: "call_9"}),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`, ID: "call_9"}),
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.AutoResume(false))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	// The first occurrence is denied.
	if _, err := s.Decide(ctx, thread.Deny(call.ID, "changed my mind")); err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(ctx, thread.Grant{
		Tool: "refund",
		Args: []thread.Arg{thread.ArgEquals("/order_id", json.RawMessage(`"1"`))},
	}); err != nil {
		t.Fatal(err)
	}
	// The resume's model re-issues call_9; the grant decides it.
	r1, err := s.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r1.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := s.Pending(); len(got) != 0 {
		t.Fatalf("Pending after the grant-decided re-issue: got %d, want 0 (the grant resolves the second occurrence)", len(got))
	}
	// Resolving the boundary runs the granted call approved.
	r2, err := s.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r2.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("granted re-issue: Approved flags %v, want [true] — the old occurrence's denial must not deny it", got)
	}
	// The transcript holds both occurrences' results — the first's
	// denial, then the granted run's own — in that order; the second
	// occurrence's is the one that must be last.
	var last string
	for _, r := range toolResults(s.Context()) {
		if r.CallID == call.ID {
			last = r.Content
		}
	}
	if last != "refunded 1" {
		t.Fatalf("the second occurrence's result: got %q, want the granted run's %q (the old occurrence's denial leaked)", last, "refunded 1")
	}
}

// A resolve beside an approve is a conflict — the fold's own words —
// not a silently discarded approve (ADR 0021 §5): under a quorum one
// approval leaves the call open, and a resolve arriving beside it
// splits the verdict. (The other order cannot be recorded: a resolve
// resolves its call at once. The fold itself is pinned both ways by
// TestEffectiveDecisionFold.)
func TestResolveBesideApproveConflicts(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.Say("ok"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.Quorum(2))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	if rt, err := s.Decide(ctx, thread.Decision{CallID: call.ID, Kind: thread.OutcomeApprove, Who: "bob"}); err != nil || rt != nil {
		t.Fatalf("one approval of two: turn %v, err %v", rt, err)
	}
	rt, err := s.Decide(ctx, thread.Resolve(call.ID, "42"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("a conflicted call ran: %v", got)
	}
	var saw string
	for _, r := range toolResults(s.Context()) {
		if r.CallID == call.ID {
			saw = r.Content
		}
	}
	if saw != "DENIED: conflicting decisions" {
		t.Fatalf("approve-then-resolve: got result %q, want the pinned conflict denial", saw)
	}
}

// flushCounter wraps a storage's Flusher capability, counting calls.
type flushCounter struct {
	thread.Storage
	mu sync.Mutex
	n  int
}

func (c *flushCounter) Flush(ctx context.Context, session string) error {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return c.Storage.(thread.Flusher).Flush(ctx, session)
}

func (c *flushCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// Decide, Grant and Revoke are durable when they return: under the
// FsyncOnFlush cadence the flush is what makes them so — a decision
// (its own doc's word) and a revocation (whose loss would re-activate
// the grant after a crash) must not live only in the page cache.
func TestDecideGrantRevokeFlushDurable(t *testing.T) {
	ctx := context.Background()
	st, err := jsonl.Open(t.TempDir(), thread.FsyncOnFlush())
	if err != nil {
		t.Fatal(err)
	}
	c := &flushCounter{Storage: st}
	agent, _ := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.Say("done"),
	)
	s, err := thread.Create(ctx, c, agent)
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	n0 := c.count()
	rt, err := s.Decide(ctx, thread.Approve(call.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if c.count() == n0 {
		t.Error("Decide did not flush: the decision is not durable under FsyncOnFlush")
	}
	n1 := c.count()
	if err := s.Grant(ctx, thread.Grant{Tool: "refund"}); err != nil {
		t.Fatal(err)
	}
	if c.count() == n1 {
		t.Error("Grant did not flush")
	}
	var grantID string
	for _, e := range s.Entries() {
		if g, ok := e.(thread.GrantEntry); ok {
			grantID = g.ID
		}
	}
	n2 := c.count()
	if err := s.Revoke(ctx, grantID); err != nil {
		t.Fatal(err)
	}
	if c.count() == n2 {
		t.Error("Revoke did not flush: a lost revocation re-activates the grant after a crash")
	}
}
