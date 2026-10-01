package thread_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
	"github.com/weftgo/weft/wefttest"
)

// A parent session under RequireSigned completes a pool delegation
// end to end: the child's parked call is decided by signature on the
// parent, the pool resumes the child, and the child's answer resolves
// the parent's delegating call through the session's own delegation
// path — recorded with Via "child" — instead of wedging on the
// unsigned Decide the parent refuses.
func TestPoolDelegationCompletesUnderRequireSigned(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	p := pool.New(1)
	child, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"5"}`}),
		wefttest.Say("signed refund done"),
	)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"go"}`}),
		wefttest.Say("all done"),
	), p.Wrap("research", "", child))
	s, err := thread.Create(ctx, thread.Memory(), parent, thread.WithKeyring(ring), thread.RequireSigned())
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("refund it"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := t1.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 {
		t.Fatalf("the delegating call did not park: %+v", res.Pending)
	}
	wrapper := res.Pending[0].ID

	var req thread.Request
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pend := s.Pending(); len(pend) == 1 {
			req = pend[0]
			break
		}
		time.Sleep(time.Millisecond)
	}
	if req.Child == "" {
		t.Fatalf("no mirrored child request surfaced: %+v", req)
	}
	// The pool's unsigned route is the caller's Decide: refused.
	if _, err := p.Decide(ctx, s, thread.Approve(req.CallID)); !errors.Is(err, thread.ErrSignatureRequired) {
		t.Fatalf("an unsigned decision through the pool: %v, want ErrSignatureRequired", err)
	}
	challenge, err := s.Request(req.CallID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideSigned(ctx, thread.SignDecision(secret, challenge, thread.Approve(req.CallID))); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Decide(ctx, s); err != nil { // the pump: resume the child, settle the delegation
		t.Fatal(err)
	}
	// The wrapper resolved with the child's answer and the parent ran
	// to its end.
	next := t1.Next()
	if next == nil {
		t.Fatal("the parent's boundary did not resume: the delegation's resolution was not recorded")
	}
	final, err := next.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if final.Text() != "all done" {
		t.Fatalf("the parent's reply: %q", final.Text())
	}
	if got := resultFor(s, wrapper); got != "signed refund done" {
		t.Fatalf("the delegating call's result: %q", got)
	}
	got := decisionsFor(s, wrapper)
	if len(got) != 1 || got[0].Via != "child" || got[0].Who != "thread/pool" {
		t.Fatalf("the delegation's resolution: %+v", got)
	}
	if flags := ran.snapshot(); len(flags) != 1 || !flags[0] {
		t.Fatalf("the child's approved call: %v", flags)
	}
}

// A parent's quorum survives the pool's replay: the two approvals the
// parent recorded for a child's call arrive in the child as one batch
// naming the call twice — the one session shape whose Decide takes
// that — and the delegation completes.
func TestPoolReplaysAQuorumIntoTheChild(t *testing.T) {
	ctx := context.Background()
	p := pool.New(1)
	child, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"5"}`}),
		wefttest.Say("refund done"),
	)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"go"}`}),
		wefttest.Say("all done"),
	), p.Wrap("research", "", child))
	s, err := thread.Create(ctx, thread.Memory(), parent, thread.Quorum(2))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("refund it"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	var req thread.Request
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if pend := s.Pending(); len(pend) == 1 {
			req = pend[0]
			break
		}
		time.Sleep(time.Millisecond)
	}
	if req.Child == "" {
		t.Fatalf("no mirrored child request surfaced: %+v", req)
	}
	for _, who := range []string{"alice", "bob"} {
		d := thread.Approve(req.CallID)
		d.Who = who
		if _, err := p.Decide(ctx, s, d); err != nil {
			t.Fatalf("%s's approval through the pool: %v", who, err)
		}
	}
	next := t1.Next()
	if next == nil {
		t.Fatal("the parent's boundary did not resume after the quorum")
	}
	final, err := next.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if final.Text() != "all done" {
		t.Fatalf("the parent's reply: %q", final.Text())
	}
	if flags := ran.snapshot(); len(flags) != 1 || !flags[0] {
		t.Fatalf("the child's call after the parent's quorum: %v", flags)
	}
}

// A pool child's Decide records the replay in order, and the grant an
// "approve and always allow" asks for is minted once — with the
// decision that makes the verdict — however many approvals follow.
func TestChildReplayBatchMintsOneGrant(t *testing.T) {
	ctx := context.Background()
	agent, ran := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"5"}`}), wefttest.Say("done"))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithLineage("s_parent", "call_w"))
	if err != nil {
		t.Fatal(err)
	}
	call := parkSend(t, s, ctx)
	first := thread.ApproveAlways(call.ID)
	first.Who = "alice"
	second := thread.ApproveAlways(call.ID)
	second.Who = "bob"
	rt, err := s.Decide(ctx, first, second)
	if err != nil {
		t.Fatalf("a replay batch into a pool child: %v", err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := len(decisionsFor(s, call.ID)); got != 2 {
		t.Fatalf("replayed decisions recorded: %d, want both", got)
	}
	if got := grantCount(s); got != 1 {
		t.Fatalf("grants minted by the replay: %d, want 1", got)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Fatalf("the replayed approval: %v", got)
	}
}
