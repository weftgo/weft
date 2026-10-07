package thread_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// An Interrupt over a parked boundary is the session's own path: it
// denies the parked calls under RequireSigned too — recorded with Via
// "interrupt", through no unsigned door — and the follow-up runs.
func TestInterruptDeniesUnderRequireSigned(t *testing.T) {
	ctx := context.Background()
	ring, _ := signerRing(t)
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund"}),
		wefttest.Say("resumed tail"),
		wefttest.Say("after the denial"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent,
		thread.WithKeyring(ring), thread.RequireSigned(), thread.BusyPolicy(thread.Interrupt))
	if err != nil {
		t.Fatal(err)
	}
	_, call := parkTurn(t, s, ctx)
	follow, err := s.Send(ctx, weft.User("forget the refund, do this"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := follow.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "after the denial" {
		t.Fatalf("follow-up reply: %q", res.Text())
	}
	got := decisionsFor(s, call.ID)
	if len(got) != 1 || got[0].Via != "interrupt" || got[0].Outcome != thread.OutcomeDeny {
		t.Fatalf("the interrupt's denial: %+v", got)
	}
	if got := resultFor(s, call.ID); got != "DENIED: interrupted by a newer message" {
		t.Fatalf("the model saw %q", got)
	}
	if len(ran.snapshot()) != 0 {
		t.Fatal("the interrupted call ran")
	}
}

// failAppendStorage fails every Append while down is set.
type failAppendStorage struct {
	thread.Storage
	down atomic.Bool
}

var errStorageDown = errors.New("storage is down")

func (f *failAppendStorage) Append(ctx context.Context, session string, entries ...thread.Entry) error {
	if f.down.Load() {
		return errStorageDown
	}
	return f.Storage.Append(ctx, session, entries...)
}

// An interrupt whose denial cannot be recorded fails loudly: the Send
// returns the storage's error and the message is not queued — the
// boundary still holds the session, and a follow-up accepted behind
// it would never run.
func TestInterruptDenialFailureFailsTheSend(t *testing.T) {
	ctx := context.Background()
	st := &failAppendStorage{Storage: thread.Memory()}
	agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("resumed tail"))
	s, err := thread.Create(ctx, st, agent, thread.BusyPolicy(thread.Interrupt))
	if err != nil {
		t.Fatal(err)
	}
	_, call := parkTurn(t, s, ctx)

	st.down.Store(true)
	turn, err := s.Send(ctx, weft.User("forget the refund"))
	if !errors.Is(err, errStorageDown) || turn != nil {
		t.Fatalf("the interrupting Send: turn %v, err %v; want the storage's error", turn, err)
	}
	st.down.Store(false)
	if got := len(s.Pending()); got != 1 {
		t.Fatalf("Pending after the failed interrupt: %d, want the call still parked", got)
	}
	// The boundary resolves by hand, and nothing runs behind it: the
	// refused message was never queued (the script holds no turn for
	// it, and the tree no prompt).
	rt, err := s.Decide(ctx, thread.Deny(call.ID, "no"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	turns := 0
	for _, e := range s.Entries() {
		switch e := e.(type) {
		case thread.TurnEntry:
			turns++
		case thread.MessageEntry:
			for _, p := range e.Message.Content {
				if tp, ok := p.(weft.TextPart); ok && strings.Contains(tp.Text, "forget the refund") {
					t.Fatal("the refused message reached the tree")
				}
			}
		}
	}
	if turns != 2 {
		t.Fatalf("turn entries: %d, want the parked turn and its resume only", turns)
	}
}

// ResolveDelegation is the pool's path for a delegating call: it
// records the child's outcome with Via "child" under RequireSigned
// too, resumes the boundary, and refuses any call that delegates to
// no child — it is not a second unsigned Decide.
func TestResolveDelegation(t *testing.T) {
	ctx := context.Background()
	ring, secret := signerRing(t)
	agent, ran := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}), wefttest.Say("the child answered"))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring), thread.RequireSigned())
	if err != nil {
		t.Fatal(err)
	}
	_, call := parkTurn(t, s, ctx)

	// A parked call that delegates to no child is not the pool's to
	// resolve.
	if _, err := s.ResolveDelegation(ctx, call.ID, "s_child", "forged", false); !errors.Is(err, thread.ErrNotPending) {
		t.Fatalf("ResolveDelegation on an ordinary call: %v, want ErrNotPending", err)
	}
	if _, err := s.ResolveDelegation(ctx, "call_nope", "s_child", "forged", false); !errors.Is(err, thread.ErrNotPending) {
		t.Fatalf("ResolveDelegation on an unknown call: %v, want ErrNotPending", err)
	}

	// The pool mirrors the child's parked call under the wrapper...
	const mirror = "s_child/call_1"
	if _, err := s.AppendApprovalRequests(ctx, thread.ApprovalRequestEntry{
		CallID: mirror, Tool: "wire", ArgsSHA256: "h", RunID: "s_child-t1",
		Child: "s_child", Wrapper: call.ID,
	}); err != nil {
		t.Fatal(err)
	}
	// ...the mirrored request is decided, signed...
	r, err := s.Request(mirror)
	if err != nil {
		t.Fatal(err)
	}
	if rt, err := s.DecideSigned(ctx, thread.SignDecision(secret, r, thread.Approve(mirror))); err != nil || rt != nil {
		t.Fatalf("deciding the mirror: turn %v, err %v", rt, err)
	}
	// ...another child's answer does not resolve it: the wrapper is
	// this child's, and a call id reused by a later delegation must
	// never take an earlier child's answer...
	if _, err := s.ResolveDelegation(ctx, call.ID, "s_other", "forged", false); !errors.Is(err, thread.ErrNotPending) {
		t.Fatalf("ResolveDelegation naming another child: %v, want ErrNotPending", err)
	}
	// ...and the child's answer resolves the wrapper.
	rt, err := s.ResolveDelegation(ctx, call.ID, "s_child", "wired 40", false)
	if err != nil {
		t.Fatal(err)
	}
	if rt == nil {
		t.Fatal("the resolved delegation did not resume the boundary")
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	got := decisionsFor(s, call.ID)
	if len(got) != 1 || got[0].Via != "child" || got[0].Who != "thread/pool" || got[0].Outcome != thread.OutcomeResolve {
		t.Fatalf("the wrapper's resolution: %+v", got)
	}
	if got := resultFor(s, call.ID); got != "wired 40" {
		t.Fatalf("the wrapper's result: %q", got)
	}
	if len(ran.snapshot()) != 0 {
		t.Fatal("the delegating call's handler ran")
	}
}
