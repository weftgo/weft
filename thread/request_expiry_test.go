package thread_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
)

// lapseClock is a settable approval clock.
type lapseClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *lapseClock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *lapseClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// expiryDenials counts a session's expiry sweeps: the audit step and
// the deny decision Via "expiry" every lapse records.
func expiryDenials(s *thread.Session, callID string) (audits, decisions int) {
	for _, e := range s.Entries() {
		switch e := e.(type) {
		case thread.ApprovalAuditEntry:
			if e.CallID == callID && e.Step == thread.StepExpiry {
				audits++
			}
		case thread.ApprovalDecisionEntry:
			if e.CallID == callID && e.Via == "expiry" {
				decisions++
			}
		}
	}
	return audits, decisions
}

// Request on a lapsed request sweeps it (ADR 0021 §5): it still fails
// with ErrExpired, and the lapse is recorded as every other path records
// it — the expiry audit and the deny — so Pending stops offering a
// request no challenge can be minted for, and under RequireSigned (no
// unsigned Decide to sweep it) the boundary resumes with the denial.
func TestRequestSweepsALapsedRequest(t *testing.T) {
	ctx := context.Background()
	ring, _ := signerRing(t)
	clock := &lapseClock{now: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	agent, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.Say("lapsed, not refunded"),
	)
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.WithKeyring(ring), thread.RequireSigned(),
		thread.Clock(clock.read), thread.RequestExpiry(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	call := parkSend(t, s, ctx)
	// The runner's post-turn step sweeps expiry too: let it end before
	// the clock moves, so the sweep under test is Request's.
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	clock.advance(2 * time.Hour)
	if _, err := s.Request(call.ID); !errors.Is(err, thread.ErrExpired) {
		t.Fatalf("Request on a lapsed request: %v, want ErrExpired", err)
	}
	if pend := s.Pending(); len(pend) != 0 {
		t.Fatalf("Pending still offers the lapsed request: %+v", pend)
	}
	if a, d := expiryDenials(s, call.ID); a != 1 || d != 1 {
		t.Fatalf("the lapse recorded %d expiry audits and %d expiry denials, want one of each", a, d)
	}
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if got := lastAssistantText(s); got != "lapsed, not refunded" {
		t.Errorf("the boundary resumed to %q, want the run's end over the denial", got)
	}
	if len(ran.snapshot()) != 0 {
		t.Error("the lapsed call ran")
	}
	// A second Request reads the request gone, not lapsed again.
	if _, err := s.Request(call.ID); !errors.Is(err, thread.ErrNotPending) {
		t.Errorf("Request after the sweep: %v, want ErrNotPending", err)
	}
}

// The same on a pool-mirrored request: Request on the parent sweeps the
// lapsed mirror, the denial reaches the child through its pool, the
// child runs to its end over it and the parent resumes — no Decide
// needed, none possible unsigned.
func TestRequestSweepsALapsedMirroredRequest(t *testing.T) {
	ctx := context.Background()
	ring, _ := signerRing(t)
	clock := &lapseClock{now: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	p := pool.New(1)
	child, ran := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"5"}`}),
		wefttest.Say("no refund: the approval lapsed"),
	)
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"go"}`}),
		wefttest.Say("all done"),
	), p.MustWrap("research", "", child))
	s, err := thread.Create(ctx, thread.Memory(), parent, thread.WithKeyring(ring), thread.RequireSigned(),
		thread.Clock(clock.read), thread.RequestExpiry(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	defer func() { _ = p.Close(ctx) }() // before the parent's Close
	t1, err := s.Send(ctx, core.User("refund it"))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := t1.Wait(); err != nil || len(res.Pending) != 1 {
		t.Fatalf("the delegating call did not park: %v %+v", err, res)
	}
	var req thread.Request
	deadline := time.Now().Add(5 * time.Second)
	for req.Child == "" && time.Now().Before(deadline) {
		if pend := s.Pending(); len(pend) == 1 {
			req = pend[0]
		}
		time.Sleep(time.Millisecond)
	}
	if req.Child == "" {
		t.Fatal("no mirrored child request surfaced")
	}
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	clock.advance(2 * time.Hour)
	if _, err := s.Request(req.CallID); !errors.Is(err, thread.ErrExpired) {
		t.Fatalf("Request on a lapsed mirrored request: %v, want ErrExpired", err)
	}
	if pend := s.Pending(); len(pend) != 0 {
		t.Fatalf("Pending still offers the lapsed mirror: %+v", pend)
	}
	if a, d := expiryDenials(s, req.CallID); a != 1 || d != 1 {
		t.Fatalf("the lapse recorded %d expiry audits and %d expiry denials, want one of each", a, d)
	}
	rs := pool.Receipts(s)
	if len(rs) != 1 {
		t.Fatalf("receipts %+v", rs)
	}
	// The pool's watch carries the parent's denial down on its own:
	// the child resumes over it and settles.
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var rc pool.Receipt
	for {
		rc, err = p.Wait(wctx, s, rs[0].ID)
		if err != nil || rc.Settled() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil || rc.State != pool.Done || rc.Stop != "no refund: the approval lapsed" {
		t.Fatalf("the child: %+v %v, want done over the denial", rc, err)
	}
	if err := s.WaitIdle(wctx); err != nil {
		t.Fatal(err)
	}
	if got := lastAssistantText(s); got != "all done" {
		t.Errorf("the parent ended %q, want its continuation", got)
	}
	if len(ran.snapshot()) != 0 {
		t.Error("the lapsed call ran")
	}
}

// lastAssistantText is the text of a session's last assistant message.
func lastAssistantText(s *thread.Session) string {
	msgs := s.Context()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == core.RoleAssistant {
			var b strings.Builder
			for _, part := range msgs[i].Content {
				if tp, ok := part.(core.TextPart); ok {
					b.WriteString(tp.Text)
				}
			}
			return b.String()
		}
	}
	return ""
}
