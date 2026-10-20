package pool_test

import (
	"context"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/thread/pool"
)

// twoLevel builds lead → finance → payments, every delegation a sync
// Wrap of one pool: the payments grandchild's refund parks, and its
// request surfaces on the lead through the finance child's mirror.
func twoLevel(t *testing.T, p *pool.Pool, paymentsReply string) (lead *core.Agent, ran *flags) {
	t.Helper()
	payments, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"B-7"}`}),
		wefttest.Say(paymentsReply),
	)
	finance := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "pay", ID: "fp",
			Args: wefttest.Args(struct{ Prompt string }{"refund B-7"})}),
		wefttest.Say("finance: settled"),
	), p.MustWrap("pay", "the payments desk", payments))
	lead = core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "finance", ID: "cf",
			Args: wefttest.Args(struct{ Prompt string }{"settle B-7"})}),
		wefttest.Say("lead: all done"),
	), p.MustWrap("finance", "the finance desk", finance))
	return lead, ran
}

// parkTwoLevel runs the lead's first turn to its park and returns the
// turn and the grandchild's request as the lead offers it.
func parkTwoLevel(t *testing.T, s *thread.Session) (*thread.Turn, thread.Request) {
	t.Helper()
	t1, err := s.Send(context.Background(), core.User("settle the B-7 dispute"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	res, err := t1.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(res.Pending) != 1 {
		t.Fatalf("lead parked %d calls, want the delegating call alone", len(res.Pending))
	}
	pend := s.Pending()
	if len(pend) != 1 || pend[0].Tool != "refund" || strings.Count(pend[0].CallID, "/") != 2 {
		t.Fatalf("lead Pending = %+v, want the grandchild's refund", pend)
	}
	return t1, pend[0]
}

// A two-level sync park decided once on the top parent resumes the
// whole chain (ADR 0022 §7): the grandchild runs the approved call,
// the middle child reads its answer and completes, and the lead's
// delegating call resolves with the middle child's answer. Before
// 0.18.1 the middle delegation settled failed with "nothing to
// resume: the child's resume did not survive a restart" — nothing
// had restarted.
func TestNestedTwoLevelApprove(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(3)
	defer func() { _ = p.Close(ctx) }()
	lead, ran := twoLevel(t, p, "payments: refunded B-7")
	s, err := thread.Create(ctx, st, lead)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t1, req := parkTwoLevel(t, s)
	if err := p.Decide(ctx, s, thread.Approve(req.CallID)); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	rc, err := p.Wait(ctx, s, pool.Receipts(s)[0].ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if rc.State != pool.Done || rc.Stop != "finance: settled" {
		t.Fatalf("finance receipt = %+v, want done with the finance answer", rc)
	}
	next := t1.Next()
	if next == nil {
		t.Fatalf("the lead's continuation is not armed")
	}
	res, err := next.Wait()
	if err != nil {
		t.Fatalf("lead continuation: %v", err)
	}
	if res.Text() != "lead: all done" {
		t.Errorf("lead reply = %q", res.Text())
	}
	if got := toolResultsOf(s.Context()); len(got) != 1 || got[0] != "finance: settled" {
		t.Errorf("lead tool results = %v, want the finance answer", got)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Errorf("grandchild refund runs = %v, want one approved run", got)
	}
	fin, err := thread.Open(ctx, st, rc.Child, core.New(wefttest.Script()))
	if err != nil {
		t.Fatalf("Open finance: %v", err)
	}
	if got := toolResultsOf(fin.Context()); len(got) != 1 || got[0] != "payments: refunded B-7" {
		t.Errorf("finance tool results = %v, want the payments answer", got)
	}
	frs := pool.Receipts(fin)
	if len(frs) != 1 || frs[0].State != pool.Done || frs[0].Stop != "payments: refunded B-7" {
		t.Errorf("payments receipt = %+v", frs)
	}
}

// The deny half: the grandchild's model reads the denial
// ("DENIED: <reason>") and answers; that answer is the middle child's
// tool result, and the middle child's answer the lead's — a denial is
// data each level reads, never a failed delegation.
func TestNestedTwoLevelDeny(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(3)
	defer func() { _ = p.Close(ctx) }()
	lead, ran := twoLevel(t, p, "payments: refund refused")
	s, _ := thread.Create(ctx, st, lead)
	t1, req := parkTwoLevel(t, s)
	if err := p.Decide(ctx, s, thread.Deny(req.CallID, "out of policy")); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	rc, err := p.Wait(ctx, s, pool.Receipts(s)[0].ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if rc.State != pool.Done || rc.Stop != "finance: settled" {
		t.Fatalf("finance receipt = %+v, want done", rc)
	}
	next := t1.Next()
	if next == nil {
		t.Fatalf("the lead's continuation is not armed")
	}
	if _, err := next.Wait(); err != nil {
		t.Fatalf("lead continuation: %v", err)
	}
	if got := toolResultsOf(s.Context()); len(got) != 1 || got[0] != "finance: settled" {
		t.Errorf("lead tool results = %v", got)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Errorf("the denied refund ran: %v", got)
	}
	fin, _ := thread.Open(ctx, st, rc.Child, core.New(wefttest.Script()))
	if got := toolResultsOf(fin.Context()); len(got) != 1 || got[0] != "payments: refund refused" {
		t.Errorf("finance tool results = %v", got)
	}
	frs := pool.Receipts(fin)
	if len(frs) != 1 {
		t.Fatalf("finance receipts = %+v", frs)
	}
	pay, _ := thread.Open(ctx, st, frs[0].Child, core.New(wefttest.Script()))
	if got := toolResultsOf(pay.Context()); len(got) != 1 || got[0] != "DENIED: out of policy" {
		t.Errorf("payments saw %v, want the denial", got)
	}
}

// twoLevelRestarted parks lead → finance → payments on jsonl, lets the
// process go (the pool and the lead closed, every writer's hold
// released), and opens the lead again under a new pool whose agents
// start at the continuation. withPay false leaves the payments wrap
// out of the new process: the grandchild then has no agent to resume.
func twoLevelRestarted(t *testing.T, withPay bool) (*pool.Pool, *thread.Session, *flags) {
	t.Helper()
	ctx := context.Background()
	st, err := jsonl.Open(t.TempDir())
	if err != nil {
		t.Fatalf("jsonl.Open: %v", err)
	}
	p := pool.New(3)
	lead, ran := twoLevel(t, p, "never: the process dies first")
	s, err := thread.Create(ctx, st, lead)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	parkTwoLevel(t, s)
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close the old pool: %v", err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close the old lead: %v", err)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("the refund ran before any decision: %v", got)
	}

	p2 := pool.New(3)
	payments, ran2 := gatedChild(wefttest.Say("payments: refunded after the restart"))
	var financeOpts []core.Option
	if withPay {
		financeOpts = append(financeOpts, p2.MustWrap("pay", "the payments desk", payments))
	}
	finance := core.New(wefttest.Script(wefttest.Say("finance: settled after the restart")), financeOpts...)
	lead2 := core.New(wefttest.Script(wefttest.Say("lead: all done")),
		p2.MustWrap("finance", "the finance desk", finance))
	restart(t, st)
	open, err := thread.Open(ctx, st, s.ID(), lead2)
	if err != nil {
		t.Fatalf("Open the lead: %v", err)
	}
	return p2, open, ran2
}

// After a restart the chain resumes the same way: the decision on the
// lead reaches the middle child, whose resume pumps its own parked
// delegation — the grandchild's bridge rebuilt from the ledger — and
// the grandchild's answer resumes the middle child.
func TestNestedTwoLevelAcrossRestart(t *testing.T) {
	ctx := context.Background()
	p, s, ran := twoLevelRestarted(t, true)
	defer func() { _ = p.Close(ctx) }()
	pend := s.Pending()
	if len(pend) != 1 || pend[0].Tool != "refund" {
		t.Fatalf("Pending after the restart = %+v", pend)
	}
	if err := p.Decide(ctx, s, thread.Approve(pend[0].CallID)); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	rc, err := p.Wait(ctx, s, pool.Receipts(s)[0].ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if rc.State != pool.Done || rc.Stop != "finance: settled after the restart" {
		t.Fatalf("finance receipt = %+v", rc)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Errorf("post-restart refund runs = %v, want one approved run", got)
	}
}

// A restart that leaves the grandchild without an agent is the one
// case the restart words are for: the middle child's boundary is open
// only on a delegation nothing in this process can resume, so the
// middle delegation settles failed with them.
func TestNestedTwoLevelRestartWithoutGrandchildAgent(t *testing.T) {
	ctx := context.Background()
	p, s, ran := twoLevelRestarted(t, false)
	defer func() { _ = p.Close(ctx) }()
	pend := s.Pending()
	if len(pend) != 1 {
		t.Fatalf("Pending after the restart = %+v", pend)
	}
	if err := p.Decide(ctx, s, thread.Approve(pend[0].CallID)); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	rc, err := p.Wait(ctx, s, pool.Receipts(s)[0].ID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if rc.State != pool.Failed || rc.Stop != "nothing to resume: the child's resume did not survive a restart" {
		t.Fatalf("finance receipt = %+v, want the restart failure", rc)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Errorf("the refund ran with no agent for it: %v", got)
	}
}
