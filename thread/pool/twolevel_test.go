package pool_test

import (
	"context"
	"fmt"
	"iter"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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
	return overPayments(p, payments, nil, ""), ran
}

// overPayments builds the lead and finance levels of the chain over a
// payments agent of the test's own; g, when set, counts the finance
// child's model calls in flight, and sfx tells the wrap names of
// several chains on one pool apart.
func overPayments(p *pool.Pool, payments *core.Agent, g *gauge, sfx string) *core.Agent {
	var fm core.Model = wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "pay" + sfx, ID: "fp",
			Args: wefttest.Args(struct{ Prompt string }{"refund B-7"})}),
		wefttest.Say("finance: settled"),
	)
	if g != nil {
		fm = counted{fm, g}
	}
	finance := core.New(fm, p.MustWrap("pay"+sfx, "the payments desk", payments))
	return core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "finance" + sfx, ID: "cf",
			Args: wefttest.Args(struct{ Prompt string }{"settle B-7"})}),
		wefttest.Say("lead: all done"),
	), p.MustWrap("finance"+sfx, "the finance desk", finance))
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

// reparking is a payments grandchild over m that asks one approval per
// order, in a row — every resume parks again until the last — and then
// answers.
func reparking(orders ...string) []wefttest.Turn {
	var turns []wefttest.Turn
	for _, o := range orders {
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: fmt.Sprintf(`{"order_id":%q}`, o)}))
	}
	return append(turns, wefttest.Say("payments: refunded "+strings.Join(orders, ", ")))
}

// parkLead runs the lead's first turn to its park on the delegating
// call and returns the turn.
func parkLead(t *testing.T, s *thread.Session) *thread.Turn {
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
	return t1
}

// decideEach takes a parked chain through one decision on the lead per
// park of the deepest child's: each decision must bring the chain back
// to rest parked on the next request — offered on the lead, the
// receipt parked — until the last settles the lead's delegation.
func decideEach(t *testing.T, p *pool.Pool, s *thread.Session, orders []string, decide func(i int, r thread.Request) thread.Decision) pool.Receipt {
	t.Helper()
	rid := pool.Receipts(s)[0].ID
	var prev thread.Request
	var rc pool.Receipt
	for i, o := range orders {
		pend := s.Pending()
		if len(pend) != 1 || pend[0].Tool != "refund" || !strings.Contains(string(pend[0].Args), o) {
			t.Fatalf("park %d: lead Pending = %+v, want the refund of %s alone", i, pend, o)
		}
		if i > 0 {
			// Call ids repeat across a child's runs: the mirror keeps the
			// namespaced id, a new entry names the new occurrence by its
			// run, and it supersedes the earlier one — one request
			// offered, undecided: the earlier decision does not carry
			// over (decisions are scoped to the occurrence's run).
			if pend[0].CallID != prev.CallID || pend[0].RunID == prev.RunID || pend[0].ID == prev.ID {
				t.Errorf("park %d: request %+v after %+v, want the same call id under a new run and entry", i, pend[0], prev)
			}
		}
		prev = pend[0]
		if err := p.Decide(context.Background(), s, decide(i, pend[0])); err != nil {
			t.Fatalf("Decide %d: %v", i, err)
		}
		want := pool.Parked
		if i == len(orders)-1 {
			want = pool.Done
		}
		rc = waitFor(t, p, s, rid, want)
	}
	return rc
}

// finishLead follows the lead's continuation and checks its delegating
// call read the finance answer.
func finishLead(t *testing.T, s *thread.Session, t1 *thread.Turn) {
	t.Helper()
	res, err := continuation(t, t1).Wait()
	if err != nil {
		t.Fatalf("lead continuation: %v", err)
	}
	if res.Text() != "lead: all done" {
		t.Errorf("lead reply = %q", res.Text())
	}
	if got := toolResultsOf(s.Context()); len(got) != 1 || got[0] != "finance: settled" {
		t.Errorf("lead tool results = %v, want the finance answer", got)
	}
}

// toolResultsIn opens a session of st and returns its tool results.
func toolResultsIn(t *testing.T, st thread.Storage, id string) []string {
	t.Helper()
	return toolResultsOf(mustOpen(context.Background(), t, st, id).Context())
}

// A grandchild that parks again while the middle child waits on it
// (awaitNested) reaches the lead: the middle child parks in turn and
// mirrors the new request on up. Before the fix the second request
// stopped at the middle — the middle's resume kept waiting on a
// delegation that was parked, never parking itself — the lead's
// Pending read empty, its receipt running, and Wait never returned.
func TestNestedTwoLevelReparkApprove(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(3)
	defer func() { _ = p.Close(ctx) }()
	payments, ran := gatedChild(reparking("B-7", "B-8")...)
	s, _ := thread.Create(ctx, st, overPayments(p, payments, nil, ""))
	t1 := parkLead(t, s)
	rc := decideEach(t, p, s, []string{"B-7", "B-8"}, func(_ int, r thread.Request) thread.Decision {
		return thread.Approve(r.CallID)
	})
	if rc.Stop != "finance: settled" {
		t.Fatalf("finance receipt = %+v", rc)
	}
	finishLead(t, s, t1)
	if got := ran.snapshot(); len(got) != 2 || !got[0] || !got[1] {
		t.Errorf("grandchild refund runs = %v, want two approved runs", got)
	}
	if got := toolResultsIn(t, st, rc.Child); len(got) != 1 || got[0] != "payments: refunded B-7, B-8" {
		t.Errorf("finance tool results = %v", got)
	}
}

// The second request denied: the denial reaches the grandchild's model
// as data, and each level above reads its child's answer.
func TestNestedTwoLevelReparkDeny(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(3)
	defer func() { _ = p.Close(ctx) }()
	payments, ran := gatedChild(reparking("B-7", "B-8")...)
	s, _ := thread.Create(ctx, st, overPayments(p, payments, nil, ""))
	t1 := parkLead(t, s)
	rc := decideEach(t, p, s, []string{"B-7", "B-8"}, func(i int, r thread.Request) thread.Decision {
		if i == 1 {
			return thread.Deny(r.CallID, "over the limit")
		}
		return thread.Approve(r.CallID)
	})
	finishLead(t, s, t1)
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Errorf("grandchild refund runs = %v, want the first alone", got)
	}
	frs := pool.Receipts(mustOpen(ctx, t, st, rc.Child))
	if len(frs) != 1 || frs[0].State != pool.Done {
		t.Fatalf("payments receipt = %+v", frs)
	}
	if got := toolResultsIn(t, st, frs[0].Child); len(got) != 2 || !strings.HasPrefix(got[0], "refunded") || got[1] != "DENIED: over the limit" {
		t.Errorf("payments saw %v, want the refund and the denial", got)
	}
}

// Three approvals in a row, each one a park of the grandchild's.
func TestNestedTwoLevelReparkThreeTimes(t *testing.T) {
	ctx := context.Background()
	p := pool.New(3)
	defer func() { _ = p.Close(ctx) }()
	orders := []string{"B-7", "B-8", "B-9"}
	payments, ran := gatedChild(reparking(orders...)...)
	s, _ := thread.Create(ctx, thread.Memory(), overPayments(p, payments, nil, ""))
	t1 := parkLead(t, s)
	decideEach(t, p, s, orders, func(_ int, r thread.Request) thread.Decision { return thread.Approve(r.CallID) })
	finishLead(t, s, t1)
	if got := ran.snapshot(); len(got) != 3 {
		t.Errorf("grandchild refund runs = %v, want three", got)
	}
}

// Three levels, the deepest child parking twice: each park climbs the
// two waiting levels above it, on a pool of one slot and of three.
func TestNestedThreeLevelRepark(t *testing.T) {
	for _, n := range []int{1, 3} {
		t.Run(fmt.Sprintf("max=%d", n), func(t *testing.T) {
			ctx := context.Background()
			p := pool.New(n)
			defer func() { _ = p.Close(ctx) }()
			leaf, ran := gatedChild(reparking("B-7", "B-8")...)
			payments := core.New(wefttest.Script(
				wefttest.ToolCalls(wefttest.Call{Name: "ledger", ID: "pl",
					Args: wefttest.Args(struct{ Prompt string }{"post B-7"})}),
				wefttest.Say("payments: posted"),
			), p.MustWrap("ledger", "the ledger desk", leaf))
			s, _ := thread.Create(ctx, thread.Memory(), overPayments(p, payments, nil, ""))
			t1 := parkLead(t, s)
			if pend := s.Pending(); len(pend) != 1 || strings.Count(pend[0].CallID, "/") != 3 {
				t.Fatalf("lead Pending = %+v, want the leaf's refund", pend)
			}
			decideEach(t, p, s, []string{"B-7", "B-8"}, func(_ int, r thread.Request) thread.Decision {
				return thread.Approve(r.CallID)
			})
			finishLead(t, s, t1)
			if got := ran.snapshot(); len(got) != 2 {
				t.Errorf("leaf refund runs = %v, want two", got)
			}
		})
	}
}

// blockingPayments is a grandchild whose approved refund blocks until
// its context ends, saying when it started.
func blockingPayments(started chan<- struct{}) *core.Agent {
	tool := core.Tool("refund", "", func(ctx context.Context, in struct{ OrderID string }) (string, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return "", ctx.Err()
	}, core.RequireApproval())
	return core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"B-7"}`}),
		wefttest.Say("payments: never"),
	), tool)
}

// Cancel or Close of the middle child while its resume waits on the
// grandchild (awaitNested) ends the grandchild first: it settles
// canceled in the middle's ledger, then the middle settles canceled
// and the lead's delegating call reads SUBAGENT_CANCELED, with no
// goroutine left behind. Before the fix the middle concluded at once
// — its session closed under the grandchild, whose settlement could
// not be recorded — and the middle's ledger read the grandchild
// running for good.
func TestNestedCancelWhileAwaitingTheGrandchild(t *testing.T) {
	for _, closeIt := range []bool{false, true} {
		name := map[bool]string{false: "Cancel", true: "Close"}[closeIt]
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			before := runtime.NumGoroutine()
			st := thread.Memory()
			p := pool.New(2)
			started := make(chan struct{}, 1)
			s, _ := thread.Create(ctx, st, overPayments(p, blockingPayments(started), nil, ""))
			t1 := parkLead(t, s)
			if err := p.Decide(ctx, s, thread.Approve(s.Pending()[0].CallID)); err != nil {
				t.Fatalf("Decide: %v", err)
			}
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("the approved refund never ran")
			}
			rc := pool.Receipts(s)[0]
			if closeIt {
				within(t, 10*time.Second, "Close", func() {
					if err := p.Close(ctx); err != nil {
						t.Errorf("Close: %v", err)
					}
				})
			} else {
				if err := p.Cancel(ctx, s, rc.ID); err != nil {
					t.Fatalf("Cancel: %v", err)
				}
				waitFor(t, p, s, rc.ID, pool.Canceled)
			}
			if got := pool.Receipts(s)[0]; got.State != pool.Canceled {
				t.Errorf("finance receipt = %+v, want canceled", got)
			}
			grs := pool.Receipts(mustOpen(ctx, t, st, rc.Child))
			if len(grs) != 1 || grs[0].State != pool.Canceled {
				t.Errorf("payments receipt in the finance ledger = %+v, want canceled", grs)
			}
			if _, err := continuation(t, t1).Wait(); err != nil {
				t.Fatalf("lead continuation: %v", err)
			}
			want := `SUBAGENT_CANCELED: agent "finance" was canceled before it finished`
			if got := toolResultsOf(s.Context()); len(got) != 1 || got[0] != want {
				t.Errorf("lead tool results = %v, want %q", got, want)
			}
			_ = p.Close(ctx)
			_ = s.Close(ctx)
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) && runtime.NumGoroutine() > before+2 {
				time.Sleep(10 * time.Millisecond)
			}
			if n := runtime.NumGoroutine(); n > before+2 {
				buf := make([]byte, 1<<20)
				t.Fatalf("%d goroutines, %d before:\n%s", n, before, buf[:runtime.Stack(buf, true)])
			}
		})
	}
}

// counted is a model whose calls a gauge counts while in flight, each
// held a little to widen any overlap a broken bound would allow.
type counted struct {
	m core.Model
	g *gauge
}

func (c counted) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		c.g.enter()
		defer c.g.leave()
		time.Sleep(2 * time.Millisecond)
		for ev, err := range c.m.Stream(ctx, req) {
			if !yield(ev, err) {
				return
			}
		}
	}
}

// The bound holds through a nested resolution: the middle child's
// resume, started by the grandchild's answer, runs under the slot the
// grandchild's finished work frees — never beside the next delegation
// to take it. Before the fix the resume's model call ran slotless
// while the grandchild's slot went back to the queue, so on a pool of
// one the next chain's child ran beside it.
func TestNestedResumeHoldsTheBound(t *testing.T) {
	ctx := context.Background()
	p := pool.New(1)
	defer func() { _ = p.Close(ctx) }()
	g := &gauge{}
	const chains = 3
	sessions := make([]*thread.Session, chains)
	parked := make([]*thread.Turn, chains)
	for i := range chains {
		payments, _ := gatedOver(counted{wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"B-7"}`}),
			wefttest.Say("payments: refunded"),
		), g})
		sessions[i], _ = thread.Create(ctx, thread.Memory(), overPayments(p, payments, g, strconv.Itoa(i)))
		parked[i] = parkLead(t, sessions[i])
	}
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.Decide(ctx, s, thread.Approve(s.Pending()[0].CallID)); err != nil {
				t.Errorf("Decide: %v", err)
			}
		}()
	}
	wg.Wait()
	for i, s := range sessions {
		if rc := waitFor(t, p, s, pool.Receipts(s)[0].ID, pool.Done); rc.Stop != "finance: settled" {
			t.Errorf("chain %d: finance receipt = %+v", i, rc)
		}
		finishLead(t, s, parked[i])
	}
	if peak := g.Peak(); peak != 1 {
		t.Errorf("model calls in flight peaked at %d on a pool of one", peak)
	}
}

// A restart while the grandchild's approved call runs re-runs that
// call (Recover's godoc, docs/thread-operations.md §10): the approval
// was recorded and replayed down the chain before the process died,
// the call's result was not, and the grandchild's boundary is still
// the one to resume — decided, so Recover's pump resumes it, and the
// approved call executes again. Nothing in the pool or the session
// can tell a call that never started from one cut off half-way.
func TestNestedRestartMidApprovedCallRunsItAgain(t *testing.T) {
	ctx := context.Background()
	st, err := jsonl.Open(t.TempDir())
	if err != nil {
		t.Fatalf("jsonl.Open: %v", err)
	}
	// The old process: its approved refund starts and never returns
	// while the test runs — the process is gone mid-call.
	old := pool.New(3)
	started := make(chan struct{}, 1)
	s, err := thread.Create(ctx, st, overPayments(old, blockingPayments(started), nil, ""))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	parkLead(t, s)
	if err := old.Decide(ctx, s, thread.Approve(s.Pending()[0].CallID)); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the approved refund never ran")
	}
	defer func() { _ = old.Close(ctx) }() // after the assertions: the dead process's goroutines

	p2 := pool.New(3)
	defer func() { _ = p2.Close(ctx) }()
	payments, ran := gatedChild(wefttest.Say("payments: refunded after the restart"))
	finance := core.New(wefttest.Script(wefttest.Say("finance: settled after the restart")),
		p2.MustWrap("pay", "the payments desk", payments))
	lead := core.New(wefttest.Script(wefttest.Say("lead: all done")),
		p2.MustWrap("finance", "the finance desk", finance))
	restart(t, st)
	open, err := thread.Open(ctx, st, s.ID(), lead)
	if err != nil {
		t.Fatalf("Open the lead: %v", err)
	}
	if pend := open.Pending(); len(pend) != 0 {
		t.Fatalf("Pending after the restart = %+v, want none: the request was decided", pend)
	}
	if err := p2.Recover(ctx, open); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	rc := waitFor(t, p2, open, pool.Receipts(open)[0].ID, pool.Done)
	if rc.Stop != "finance: settled after the restart" {
		t.Errorf("finance receipt = %+v", rc)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Errorf("post-restart refund runs = %v, want the approved call run again", got)
	}
}
