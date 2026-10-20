package pool

import (
	"context"
	"iter"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// flagModel counts the model calls its wrapped model starts while the
// test says no run may be working (holds): each one is a breach of
// the pool's bound.
type flagModel struct {
	m      core.Model
	holds  *atomic.Bool
	breach *atomic.Int32
}

func (f flagModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		if f.holds.Load() {
			f.breach.Add(1)
		}
		for ev, err := range f.m.Stream(ctx, req) {
			if !yield(ev, err) {
				return
			}
		}
	}
}

// queued reports how many tickets wait in the pool's queue.
func queued(p *Pool) int {
	p.sem.mu.Lock()
	defer p.sem.mu.Unlock()
	return p.sem.waiters.Len()
}

// waitQueued polls until n tickets wait in the pool's queue.
func waitQueued(t *testing.T, p *Pool, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if queued(p) == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the queue never held %d tickets (it holds %d)", n, queued(p))
}

// chainOver builds lead → finance → payments, each a sync wrap of p:
// the internal tests' two-level chain over a payments agent and a
// finance model of the test's own.
func chainOver(p *Pool, payments *core.Agent, financeModel func(core.Model) core.Model) *core.Agent {
	var fm core.Model = wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "pay", ID: "fp", Args: wefttest.Args(struct{ Prompt string }{"refund B-7"})}),
		wefttest.Say("finance: settled"),
	)
	if financeModel != nil {
		fm = financeModel(fm)
	}
	finance := core.New(fm, p.MustWrap("pay", "", payments))
	return core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "finance", ID: "cf", Args: wefttest.Args(struct{ Prompt string }{"settle B-7"})}),
		wefttest.Say("lead: done"),
	), p.MustWrap("finance", "", finance))
}

// parkChain runs the lead's first turn to its park on the grandchild's
// request and returns the turn.
func parkChain(t *testing.T, s *thread.Session) *thread.Turn {
	t.Helper()
	t1, err := s.Send(context.Background(), core.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	if res, err := t1.Wait(); err != nil || len(res.Pending) != 1 || len(s.Pending()) != 1 {
		t.Fatalf("the lead's first turn = %+v, %v; Pending %+v", res, err, s.Pending())
	}
	return t1
}

// liveChild returns the pool's live session for a child session id.
func liveChild(t *testing.T, p *Pool, id string) *thread.Session {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	d, ok := p.byChild[id]
	if !ok {
		t.Fatalf("no live delegate for child session %s", id)
	}
	return d.child
}

// A grandchild canceled while it queues holds no slot to lend, yet its
// end resolves the middle child's delegating call and starts the
// middle's resume: the resolution queues for a slot on the middle's
// behalf first, so the resume never works beside the bound. Before the
// fix the resume's first model call ran at once, slotless, while the
// test held the pool's only slot.
func TestNestedResolutionQueuesASlotWhenTheGrandchildHasNone(t *testing.T) {
	ctx := context.Background()
	p := New(1)
	defer func() { _ = p.Close(ctx) }()
	var holds atomic.Bool
	var breach atomic.Int32
	payments, _ := gatedRefund(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"B-7"}`}),
		wefttest.Say("payments: refunded"),
	))
	lead := chainOver(p, payments, func(m core.Model) core.Model { return flagModel{m, &holds, &breach} })
	s, _ := thread.Create(ctx, thread.Memory(), lead)
	t1 := parkChain(t, s)

	first := p.sem.enqueue() // the test holds the only slot
	<-first.ready
	if err := p.Decide(ctx, s, thread.Approve(s.Pending()[0].CallID)); err != nil {
		t.Fatal(err)
	}
	waitQueued(t, p, 1) // the middle's resume
	second := p.sem.enqueue()
	p.sem.release() // the middle's resume takes it; second is next
	select {
	case <-second.ready: // the middle yielded it, waiting on the grandchild
	case <-time.After(5 * time.Second):
		t.Fatal("the middle's resume never yielded its slot")
	}
	waitQueued(t, p, 1) // the grandchild's resume
	holds.Store(true)
	mid := liveChild(t, p, Receipts(s)[0].Child)
	gid := Receipts(mid)[0].ID
	if err := p.Cancel(ctx, mid, gid); err != nil {
		t.Fatal(err)
	}
	// The grandchild settles canceled, and its resolution waits in the
	// queue for the slot the test holds — or, broken, the middle's
	// resume runs its model call now.
	deadline := time.Now().Add(5 * time.Second)
	for !receiptSettled(mid, gid) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	for end := time.Now().Add(100 * time.Millisecond); breach.Load() == 0 && time.Now().Before(end); {
		time.Sleep(time.Millisecond)
	}
	if n := breach.Load(); n > 0 {
		t.Fatalf("the middle's resume ran %d model calls with no slot while the test held the only one", n)
	}
	holds.Store(false)
	p.sem.release()
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rc, err := p.Wait(wctx, s, Receipts(s)[0].ID)
	if err != nil || rc.State != Done || rc.Stop != "finance: settled" {
		t.Fatalf("finance receipt = %+v, %v; want done", rc, err)
	}
	if g := Receipts(mid); len(g) != 1 || g[0].State != Canceled {
		t.Errorf("payments receipt = %+v, want canceled", g)
	}
	if next := t1.Next(); next == nil {
		t.Error("the lead's continuation is not armed")
	} else if res, err := next.Wait(); err != nil || res.Text() != "lead: done" {
		t.Errorf("lead continuation = %v, %v", res, err)
	}
	if n := breach.Load(); n > 0 {
		t.Errorf("model calls with no slot: %d", n)
	}
}

// gatedRefund is an agent over m with one refund tool behind
// RequireApproval, and the count of its runs.
func gatedRefund(m core.Model) (*core.Agent, *atomic.Int32) {
	var ran atomic.Int32
	return core.New(m, core.Tool("refund", "", func(context.Context, struct{ OrderID string }) (string, error) {
		ran.Add(1)
		return "refunded", nil
	}, core.RequireApproval())), &ran
}

// A Cancel that ends a resumed sync child before the parent's turn has
// parked the delegating call — the parent still running another call
// of the same step — still resolves that call: the conclusion waits
// for the call to land in the parent, on the pool's context, and the
// parent resumes reading SUBAGENT_CANCELED. Before the fix the
// resolution was attempted at once ("delegating call no longer
// pending"), the parent then parked on the call, and nothing resolved
// it: its Pending empty, its Next nil, for good.
func TestCancelBeforeTheWrapperParksStillResolvesIt(t *testing.T) {
	ctx := context.Background()
	p := New(2)
	defer func() { _ = p.Close(ctx) }()
	child, ran := gatedRefund(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
		wefttest.Say("a: refunded"),
	))
	gate := make(chan struct{})
	slow := core.New(stubModel{gate: gate})
	lead := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "a", ID: "ca", Args: wefttest.Args(struct{ Prompt string }{"refund 1"})},
			wefttest.Call{Name: "b", ID: "cb", Args: wefttest.Args(struct{ Prompt string }{"take your time"})},
		),
		wefttest.Say("lead: done"),
	), p.MustWrap("a", "", child), p.MustWrap("b", "", slow))
	s, _ := thread.Create(ctx, thread.Memory(), lead)
	t1, err := s.Send(ctx, core.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(s.Pending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(s.Pending()) != 1 {
		t.Fatalf("lead Pending = %+v, want a's refund", s.Pending())
	}
	if err := p.Decide(ctx, s, thread.Approve(s.Pending()[0].CallID)); err != nil {
		t.Fatal(err)
	}
	var a Receipt
	for _, r := range Receipts(s) {
		if r.Call == "ca" {
			a = r
		}
	}
	// a's resume is armed and holds for the wrapper to land.
	for time.Now().Before(deadline) {
		p.mu.Lock()
		d := p.delegates[a.ID]
		armed := d != nil && d.phase == phaseQueued
		p.mu.Unlock()
		if armed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := p.Cancel(ctx, s, a.ID); err != nil {
		t.Fatal(err)
	}
	for !receiptSettled(s, a.ID) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // the broken resolution had come and gone by now
	close(gate)
	if res, err := t1.Wait(); err != nil || len(res.Pending) != 1 {
		t.Fatalf("the lead's first turn = %+v, %v; want parked on a's call", res, err)
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if rc, err := p.Wait(wctx, s, a.ID); err != nil || rc.State != Canceled {
		t.Fatalf("a's receipt = %+v, %v; want canceled", rc, err)
	}
	next := t1.Next()
	if next == nil {
		t.Fatalf("the lead's continuation is not armed: Pending %+v", s.Pending())
	}
	res, err := next.Wait()
	if err != nil || res.Text() != "lead: done" {
		t.Fatalf("lead continuation = %v, %v", res, err)
	}
	var results []string
	for _, m := range s.Context() {
		for _, part := range m.Content {
			if tr, ok := part.(core.ToolResultPart); ok {
				results = append(results, tr.Content)
			}
		}
	}
	want := `SUBAGENT_CANCELED: agent "a" was canceled before it finished`
	if len(results) != 2 || results[0] != want || results[1] != "ok" {
		t.Errorf("lead tool results = %q, want [%q ok]", results, want)
	}
	if n := ran.Load(); n != 0 {
		t.Errorf("a's refund ran %d times after its delegation was canceled", n)
	}
}

// records collects the messages a logger is handed.
type records struct {
	mu  sync.Mutex
	got []string
}

func (r *records) Enabled(context.Context, slog.Level) bool { return true }
func (r *records) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, rec.Level.String()+" "+rec.Message)
	return nil
}
func (r *records) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *records) WithGroup(string) slog.Handler      { return r }
func (r *records) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

// endNested's bound has a defined outcome: a grandchild whose run does
// not end within it (a handler ignoring its context, no Timeout) is
// settled canceled by the pool, in the middle child's ledger while
// that session is still open, its stop naming the bound — and its run,
// abandoned, records nothing when it finally ends. Before the fix the
// grandchild was only logged and left: the middle's session closed,
// the grandchild's settlement failed into it, and the middle's ledger
// read it running until a Recover.
func TestNestedEndBoundSettlesTheStuckGrandchild(t *testing.T) {
	ctx := context.Background()
	p := New(2)
	shortenNestedEnd(p, 200*time.Millisecond)
	logs := &records{}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	stuck := core.Tool("refund", "", func(context.Context, struct{ OrderID string }) (string, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release // ignores its context
		return "refunded late", nil
	}, core.RequireApproval())
	payments := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"B-7"}`}),
		wefttest.Say("payments: never"),
	), stuck, core.Logger(slog.New(logs)))
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, chainOver(p, payments, nil))
	t1 := parkChain(t, s)
	if err := p.Decide(ctx, s, thread.Approve(s.Pending()[0].CallID)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the approved refund never ran")
	}
	fin := Receipts(s)[0]
	if err := p.Cancel(ctx, s, fin.ID); err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if rc, err := p.Wait(wctx, s, fin.ID); err != nil || rc.State != Canceled {
		t.Fatalf("finance receipt = %+v, %v; want canceled", rc, err)
	}
	mid, err := thread.Open(ctx, st, fin.Child, core.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	g := Receipts(mid)
	if len(g) != 1 || g[0].State != Canceled || !strings.Contains(g[0].Stop, "did not end within 200ms") {
		t.Fatalf("payments receipt in the finance ledger = %+v, want canceled by the pool past the bound", g)
	}
	if res, err := t1.Next().Wait(); err != nil || res.Text() != "lead: done" {
		t.Fatalf("lead continuation = %v, %v", res, err)
	}
	close(release) // the abandoned run ends now
	cctx, ccancel := context.WithTimeout(ctx, 10*time.Second)
	defer ccancel()
	if err := p.Close(cctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	mid, _ = thread.Open(ctx, st, fin.Child, core.New(wefttest.Script()))
	settlements := 0
	for _, e := range mid.Entries() {
		if pr, ok := e.(thread.PoolReceiptEntry); ok && pr.Receipt == g[0].ID && State(pr.Status).Settled() {
			settlements++
		}
	}
	if settlements != 1 {
		t.Errorf("%d settlements recorded for the payments receipt, want the pool's one", settlements)
	}
	for _, l := range logs.snapshot() {
		if strings.Contains(l, "not recorded") {
			t.Errorf("the abandoned run wrote into the closed session: %s", l)
		}
	}
}
