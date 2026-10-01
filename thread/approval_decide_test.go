package thread_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// parkTurn drives one Send that parks, and returns the parked turn
// beside its pending call — for the tests that follow Turn.Next.
func parkTurn(t *testing.T, s *thread.Session, ctx context.Context) (*thread.Turn, weft.ToolCallPart) {
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
		t.Fatal("no calls parked")
	}
	return turn, res.Pending[0]
}

// decisionsFor collects the decision entries recorded for a call.
func decisionsFor(s *thread.Session, callID string) []thread.ApprovalDecisionEntry {
	var out []thread.ApprovalDecisionEntry
	for _, e := range s.Entries() {
		if d, ok := e.(thread.ApprovalDecisionEntry); ok && d.CallID == callID {
			out = append(out, d)
		}
	}
	return out
}

// grantCount counts the grant entries a session holds.
func grantCount(s *thread.Session) int {
	n := 0
	for _, e := range s.Entries() {
		if _, ok := e.(thread.GrantEntry); ok {
			n++
		}
	}
	return n
}

// resultFor returns the last tool result the context holds for a call.
func resultFor(s *thread.Session, callID string) string {
	last := ""
	for _, r := range toolResults(s.Context()) {
		if r.CallID == callID {
			last = r.Content
		}
	}
	return last
}

// A decision for a request past its expiry never runs the call:
// Decide refuses it with ErrExpired, the request is denied on the spot
// with the expiry reason — audit step and decision — and, the denial
// completing the boundary, AutoResume resumes it; the resume is
// reachable through the parked turn's Next.
func TestDecideOnExpiredRequestIsRefused(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("expired noted"))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.RequestExpiry(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	parked, call := parkTurn(t, s, ctx)
	pend := s.Pending()[0]
	time.Sleep(time.Until(pend.Expiry) + 5*time.Millisecond)

	rt, err := s.Decide(ctx, thread.Approve(call.ID))
	if !errors.Is(err, thread.ErrExpired) {
		t.Fatalf("Decide on an expired request: got %v, want ErrExpired", err)
	}
	if rt != nil {
		t.Fatal("a refused Decide returned a Turn")
	}
	next := parked.Next()
	if next == nil {
		t.Fatal("the expiry denial completed the boundary and no resume was armed")
	}
	if _, err := next.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("an expired request's call ran on a late approval: %v", got)
	}
	want := "DENIED: expired: no decision before " + pend.Expiry.UTC().Format(time.RFC3339)
	if got := resultFor(s, call.ID); got != want {
		t.Fatalf("the model saw %q, want %q", got, want)
	}
	counts := countApprovalEntries(s)
	if counts["audit:expiry:denied"] != 1 || counts["decision:"+call.ID+":deny:expiry"] != 1 {
		t.Fatalf("expiry not recorded as its audit step and decision: %v", counts)
	}
	if counts["decision:"+call.ID+":approve:user"] != 0 {
		t.Fatalf("the late approval was recorded: %v", counts)
	}
}

// The expiry sweep runs on Send's arming path too: a Send that finds
// an idle session parked on nothing but lapsed requests denies them
// and resumes — the follow-up runs instead of waiting behind a
// boundary no one will decide.
func TestSendOverExpiredBoundaryResumes(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund"}),
		wefttest.Say("expired noted"),
		wefttest.Say("follow-up answered"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.RequestExpiry(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_, call := parkTurn(t, s, ctx)
	time.Sleep(time.Until(s.Pending()[0].Expiry) + 5*time.Millisecond)

	follow, err := s.Send(ctx, weft.User("anything else?"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := follow.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "follow-up answered" {
		t.Fatalf("follow-up reply: %q", res.Text())
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("the expired call ran: %v", got)
	}
	if got := resultFor(s, call.ID); !strings.HasPrefix(got, "DENIED: expired: no decision before ") {
		t.Fatalf("the lapsed call's result: %q", got)
	}
}

// One call takes one verdict per Decide: a batch naming a call twice
// is refused whole, as is one with nothing in it or a decision with no
// outcome — nothing recorded, the call still pending.
func TestDecideRejectsInvalidBatches(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}), wefttest.Say("done"))
	s, err := thread.Create(ctx, thread.Memory(), agent)
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	before := len(s.Entries())
	for name, ds := range map[string][]thread.Decision{
		"approve-always beside deny": {thread.ApproveAlways(call.ID), thread.Deny(call.ID, "no")},
		"the same approval twice":    {thread.Approve(call.ID), thread.Approve(call.ID)},
		"no decisions":               nil,
		"no outcome":                 {{CallID: call.ID}},
	} {
		if rt, err := s.Decide(ctx, ds...); !errors.Is(err, thread.ErrInvalidDecision) || rt != nil {
			t.Errorf("%s: got turn %v, err %v; want ErrInvalidDecision", name, rt, err)
		}
	}
	if got := len(s.Entries()); got != before {
		t.Fatalf("refused batches recorded %d entries", got-before)
	}
	if got := len(s.Pending()); got != 1 {
		t.Fatalf("Pending after refused batches: %d, want 1", got)
	}
}

// "Approve and always allow" grants only when the call's effective
// verdict is approve. Under a quorum the first approval is not a
// verdict: no grant is minted beside it; a denial that follows leaves
// none behind, and the approval that completes the quorum mints
// exactly one.
func TestApproveAlwaysGrantsOnlyAnEffectiveApproval(t *testing.T) {
	ctx := context.Background()
	always := func(call, who string) thread.Decision {
		d := thread.ApproveAlways(call)
		d.Who = who
		return d
	}
	t.Run("a denied verdict leaves no grant", func(t *testing.T) {
		agent, ran := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}), wefttest.Say("refused"))
		s, err := thread.Create(ctx, thread.Memory(), agent, thread.Quorum(2))
		if err != nil {
			t.Fatal(err)
		}
		call := parkSend(t, s, ctx)
		if _, err := s.Decide(ctx, always(call.ID, "alice")); err != nil {
			t.Fatal(err)
		}
		if got := grantCount(s); got != 0 {
			t.Fatalf("a grant was minted on one approval of two: %d", got)
		}
		no := thread.Deny(call.ID, "not this one")
		no.Who = "bob"
		rt, err := s.Decide(ctx, no)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rt.Wait(); err != nil {
			t.Fatal(err)
		}
		if got := grantCount(s); got != 0 {
			t.Fatalf("a denied call left %d grant(s) behind", got)
		}
		if got := ran.snapshot(); len(got) != 0 {
			t.Fatalf("the denied call ran: %v", got)
		}
	})
	t.Run("the completing approval mints the grant once", func(t *testing.T) {
		agent, ran := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}), wefttest.Say("done"))
		s, err := thread.Create(ctx, thread.Memory(), agent, thread.Quorum(2))
		if err != nil {
			t.Fatal(err)
		}
		call := parkSend(t, s, ctx)
		if _, err := s.Decide(ctx, always(call.ID, "alice")); err != nil {
			t.Fatal(err)
		}
		yes := thread.Approve(call.ID)
		yes.Who = "bob"
		rt, err := s.Decide(ctx, yes)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rt.Wait(); err != nil {
			t.Fatal(err)
		}
		if got := grantCount(s); got != 1 {
			t.Fatalf("grants after the quorum completed: %d, want 1", got)
		}
		if got := ran.snapshot(); len(got) != 1 || !got[0] {
			t.Fatalf("the approved call: %v", got)
		}
	})
}

// Decide is the unsigned door and says so: whatever Via the caller
// wrote — a session-owned channel included — the entry records
// "user".
func TestDecideRecordsViaUser(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("done"))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.AutoResume(false))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	d := thread.Deny(call.ID, "no")
	d.Via, d.Who = "expiry", "root"
	if _, err := s.Decide(ctx, d); err != nil {
		t.Fatal(err)
	}
	got := decisionsFor(s, call.ID)
	if len(got) != 1 || got[0].Via != "user" || got[0].Who != "root" {
		t.Fatalf("recorded decision: %+v, want Via \"user\" and Who as declared", got)
	}
	if got[0].RequestID == "" {
		t.Fatalf("the decision does not name its request entry: %+v", got[0])
	}
}

// A decision is spent by the resume that applied it. A Branch back to
// the decided boundary shows the call pending again: a resume there
// does not run the approved call a second time on the old approval —
// it takes a new decision.
func TestBranchBackToDecisionNeedsNewDecision(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1234"}`}),
		wefttest.Say("refunded"),
		wefttest.Say("not refunded again"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.AutoResume(false))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	if _, err := s.Decide(ctx, thread.Approve(call.ID)); err != nil {
		t.Fatal(err)
	}
	decision := decisionsFor(s, call.ID)[0]
	rt, err := s.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("the approved call: %v", got)
	}
	// The resume's audit entry lists the decision it applied.
	applied := false
	for _, e := range s.Audit() {
		if a, ok := e.(thread.ApprovalAuditEntry); ok && a.Step == thread.StepResume {
			applied = len(a.Decisions) == 1 && a.Decisions[0] == decision.ID
		}
	}
	if !applied {
		t.Fatal("the resume's audit entry does not list the decision it applied")
	}

	// Back to the decided boundary: the decision is spent.
	if err := s.Branch(ctx, decision.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.Pending(); len(got) != 1 || got[0].CallID != call.ID {
		t.Fatalf("Pending after branching back to the decision: %+v, want the call pending again", got)
	}
	no := thread.Deny(call.ID, "once was enough")
	if _, err := s.Decide(ctx, no); err != nil {
		t.Fatal(err)
	}
	rt2, err := s.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt2.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 1 {
		t.Fatalf("the call ran %d times on one approval", len(got))
	}
	if got := resultFor(s, call.ID); got != "DENIED: once was enough" {
		t.Fatalf("the branch's result: %q, want the new decision's", got)
	}
}

// A decision a Fork copied was made for the origin session: in the
// fork the call is pending again, and a resume there runs nothing on
// the inherited approval.
func TestForkDoesNotInheritDecisions(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1234"}`}),
		wefttest.Say("fork resumed"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.AutoResume(false))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	if _, err := s.Decide(ctx, thread.Approve(call.ID)); err != nil {
		t.Fatal(err)
	}
	f, err := s.Fork(ctx, s.Leaf(), thread.AutoResume(false))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Pending(); len(got) != 1 || got[0].CallID != call.ID {
		t.Fatalf("the fork's Pending: %+v, want the call pending again", got)
	}
	rt, err := f.Resume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("the fork ran the call on the origin's approval: %v", got)
	}
	if got := resultFor(f, call.ID); got != "DENIED: no decision" {
		t.Fatalf("the fork's result: %q", got)
	}
	// The origin still holds its decision.
	if got := len(s.Pending()); got != 0 {
		t.Fatalf("the origin's Pending: %d", got)
	}
}

// Under Quorum a chain step's single approval leaves the call open —
// and the call still parks like any other: a request entry with its
// run, time and expiry, written before the approval it holds, the
// park audited, OnRequest fired.
func TestQuorumChainApprovalStillParksARequest(t *testing.T) {
	ctx := context.Background()
	for name, chain := range map[string]thread.SessionOption{
		"grant": nil,
		"approver": thread.WithApprover(func(_ context.Context, r thread.Request) (thread.Decision, bool) {
			d := thread.Approve(r.CallID)
			d.Who = "terminal"
			return d, true
		}, 5*time.Second),
	} {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			var notified []thread.Request
			agent, ran := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}), wefttest.Say("done"))
			s, err := thread.Create(ctx, thread.Memory(), agent,
				thread.Quorum(2), thread.RequestExpiry(time.Hour), chain,
				thread.OnRequest(func(r thread.Request) {
					mu.Lock()
					notified = append(notified, r)
					mu.Unlock()
				}))
			if err != nil {
				t.Fatal(err)
			}
			if chain == nil {
				if err := s.Grant(ctx, thread.Grant{Tool: "refund"}); err != nil {
					t.Fatal(err)
				}
			}
			parked, call := parkTurn(t, s, ctx)
			pend := s.Pending()
			if len(pend) != 1 {
				t.Fatalf("Pending: %d, want the call awaiting its quorum", len(pend))
			}
			r := pend[0]
			if r.ID == "" || r.RunID != parked.RunID() || r.Created.IsZero() || r.Expiry.IsZero() {
				t.Fatalf("the parked request is not a full request: %+v", r)
			}
			mu.Lock()
			n := len(notified)
			mu.Unlock()
			if n != 1 || notified[0].CallID != call.ID || notified[0].ID != r.ID {
				t.Fatalf("OnRequest deliveries: %+v", notified)
			}
			// The request entry comes before the approval it holds.
			reqAt, decAt := -1, -1
			for i, e := range s.Entries() {
				switch e := e.(type) {
				case thread.ApprovalRequestEntry:
					reqAt = i
				case thread.ApprovalDecisionEntry:
					if e.CallID == call.ID {
						decAt = i
					}
				}
			}
			if reqAt < 0 || decAt < reqAt {
				t.Fatalf("request at %d, the chain's approval at %d", reqAt, decAt)
			}
			if counts := countApprovalEntries(s); counts["audit:park:parked"] != 1 {
				t.Fatalf("the park is not audited: %v", counts)
			}
			// The second approval completes it.
			yes := thread.Approve(call.ID)
			yes.Who = "alice"
			rt, err := s.Decide(ctx, yes)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := rt.Wait(); err != nil {
				t.Fatal(err)
			}
			if got := ran.snapshot(); len(got) != 1 || !got[0] {
				t.Fatalf("the call after its quorum: %v", got)
			}
		})
	}
}

// An Approver with no time to answer in is a configuration error, not
// a silently unconsulted step.
func TestWithApproverNeedsATimeout(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(wefttest.Say("hi"))
	approver := func(context.Context, thread.Request) (thread.Decision, bool) { return thread.Decision{}, false }
	st := thread.Memory()
	if _, err := thread.Create(ctx, st, agent, thread.WithApprover(approver, 0)); err == nil {
		t.Fatal("Create accepted an Approver with no timeout")
	}
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := thread.Open(ctx, st, s.ID(), agent, thread.WithApprover(approver, -time.Second)); err == nil {
		t.Fatal("Open accepted an Approver with a negative timeout")
	}
	if _, err := thread.Open(ctx, st, s.ID(), agent, thread.WithApprover(nil, 0)); err != nil {
		t.Fatalf("a nil Approver is ignored, timeout and all: %v", err)
	}
}
