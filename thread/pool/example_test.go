package pool_test

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/thread/pool"
	"github.com/weftgo/weft/wefttest"
)

// A sync delegation through a session: the wrapped tool's child runs
// as a child session under a pool slot, the call waits, the result is
// the child's answer, and the parent's ledger carries the child's cost
// in its Delegated bucket.
func ExamplePool_Wrap() {
	ctx := context.Background()
	st := thread.Memory()
	child := weft.New(wefttest.Script(wefttest.Say("the answer is 42")))
	p := pool.New(2)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research",
			Args: wefttest.Args(struct{ Prompt string }{"find the answer"})}),
		wefttest.Say("done"),
	), p.MustWrap("research", "delegates research to a child session", child))
	s, _ := thread.Create(ctx, st, parent)
	turn, err := s.Send(ctx, weft.User("what is the answer?"))
	if err != nil {
		panic(err)
	}
	res, err := turn.Wait()
	if err != nil {
		panic(err)
	}
	fmt.Println("reply:", res.Text())
	for _, r := range pool.Receipts(s) {
		fmt.Println("receipt:", r.State, "-", r.Stop)
	}
	fmt.Println("delegated output tokens:", s.Usage().Delegated.OutputTokens)
	// Output:
	// reply: done
	// receipt: done - the answer is 42
	// delegated output tokens: 5
}

// An async delegation: Submit returns at once with the acceptance,
// the child runs on the pool's context, and Wait follows it to its
// settlement.
func ExamplePool_Submit() {
	ctx := context.Background()
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	p := pool.New(1)
	child := weft.New(wefttest.Script(wefttest.Say("background answer")))
	r, err := p.Submit(ctx, s, child, "work in the background")
	if err != nil {
		panic(err)
	}
	fmt.Println("accepted:", r.State)
	final, err := p.Wait(ctx, s, r.ID)
	if err != nil {
		panic(err)
	}
	fmt.Println("settled:", final.State, "-", final.Stop)
	if err := p.Close(ctx); err != nil {
		panic(err)
	}
	// Output:
	// accepted: accepted
	// settled: done - background answer
}

// Watching an async child live: the receipt names the child session,
// and a storage with the Watcher capability — jsonl here; Memory has
// none — tails its entries as the child writes them.
func ExamplePool_Submit_watch() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "weft-pool-example")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	st, err := jsonl.Open(dir)
	if err != nil {
		panic(err)
	}
	s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
	p := pool.New(1)
	r, err := p.Submit(ctx, s, weft.New(wefttest.Script(wefttest.Say("background answer"))), "work in the background")
	if err != nil {
		panic(err)
	}
	watcher, ok := thread.Storage(st).(thread.Watcher)
	if !ok {
		panic("this storage cannot tail a session")
	}
	entries, err := watcher.Watch(ctx, r.Child, "")
	if err != nil {
		panic(err)
	}
	for e, err := range entries {
		if err != nil {
			panic(err)
		}
		switch e := e.(type) {
		case thread.MessageEntry:
			fmt.Printf("%s: %s\n", e.Message.Role, e.Message.Text())
		case thread.TurnEntry:
			fmt.Println("turn ended:", e.StopReason)
		}
		if _, ended := e.(thread.TurnEntry); ended {
			break // the child's one turn is over; a tail would wait for more
		}
	}
	if err := p.Close(ctx); err != nil {
		panic(err)
	}
	// Output:
	// user: work in the background
	// assistant: background answer
	// turn ended: stop
}

// A nested approval, end to end (ADR 0022 §7): the wrapped child parks
// at its gated tool, the parent's delegating call parks with it, the
// child's request surfaces on the parent's Pending with its lineage,
// and one decision through the pool resumes the child and completes
// the parent's call with the child's answer — the parent model reads
// it as the tool result and finishes its turn.
func ExamplePool_Decide() {
	ctx := context.Background()
	gated := weft.Tool("refund", "", func(_ context.Context, in struct{ OrderID string }) (string, error) {
		return "refunded " + in.OrderID, nil
	}, weft.RequireApproval())
	child := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"42"}`}),
		wefttest.Say("refund issued"),
	), gated)
	p := pool.New(2)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research",
			Args: wefttest.Args(struct{ Prompt string }{"refund order 42"})}),
		wefttest.Say("handled"),
	), p.MustWrap("research", "delegates the refund flow", child))
	s, _ := thread.Create(ctx, thread.Memory(), parent)
	t1, _ := s.Send(ctx, weft.User("refund order 42"))
	if _, err := t1.Wait(); err != nil {
		panic(err)
	}
	pend := s.Pending()
	fmt.Println("pending:", pend[0].Tool)
	if err := p.Decide(ctx, s, thread.Approve(pend[0].CallID)); err != nil {
		panic(err)
	}
	// Decide queued the child's resume and returned; Wait follows the
	// delegation to rest — here its settlement, which also resolved
	// the parent's parked call.
	if _, err := p.Wait(ctx, s, pool.Receipts(s)[0].ID); err != nil {
		panic(err)
	}
	res, err := t1.Next().Wait() // the parent's parked run, resumed with the answer
	if err != nil {
		panic(err)
	}
	fmt.Println("parent:", res.Text())
	for _, r := range pool.Receipts(s) {
		fmt.Println("receipt:", r.State, "-", r.Stop)
	}
	// Output:
	// pending: refund
	// parent: handled
	// receipt: done - refund issued
}

// waiting builds a child agent that blocks inside a tool until
// release closes — a child caught mid-run, for the examples that need
// one. started closes when the tool is entered.
func waiting(started, release chan struct{}, then string) *weft.Agent {
	wait := weft.Tool("wait", "", func(ctx context.Context, _ struct{}) (string, error) {
		close(started)
		select {
		case <-release:
			return "released", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	return weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "wait", ID: "call-wait"}),
		wefttest.Say(then),
	), wait)
}

// Cancel ends one delegation: the running child's run is canceled,
// its receipt settles canceled, and a second Cancel says the receipt
// is no longer running — and what it is instead.
func ExamplePool_Cancel() {
	ctx := context.Background()
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	p := pool.New(1)
	started, release := make(chan struct{}), make(chan struct{})
	r, err := p.Submit(ctx, s, waiting(started, release, "never said"), "work")
	if err != nil {
		panic(err)
	}
	<-started // the child is mid-run
	if err := p.Cancel(ctx, s, r.ID); err != nil {
		panic(err)
	}
	final, _ := p.Wait(ctx, s, r.ID)
	fmt.Println("receipt:", final.State, "settled:", final.Settled())

	err = p.Cancel(ctx, s, r.ID)
	var se *pool.StateError
	fmt.Println("again:", errors.Is(err, pool.ErrNotRunning), errors.As(err, &se), se.State)
	fmt.Println("wrong id:", errors.Is(p.Cancel(ctx, s, "e_unknown"), pool.ErrUnknownReceipt))
	// Output:
	// receipt: canceled settled: true
	// again: true true canceled
	// wrong id: true
}

// Close drains the pool: the running child and the one still queued
// behind it are canceled and settled before Close returns, and the
// pool takes no more work.
func ExamplePool_Close() {
	ctx := context.Background()
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	p := pool.New(1) // one slot: the second child queues
	started, release := make(chan struct{}), make(chan struct{})
	if _, err := p.Submit(ctx, s, waiting(started, release, "never said"), "first"); err != nil {
		panic(err)
	}
	<-started
	if _, err := p.Submit(ctx, s, weft.New(wefttest.Script(wefttest.Say("never ran"))), "second"); err != nil {
		panic(err)
	}
	if err := p.Close(ctx); err != nil {
		panic(err)
	}
	for _, r := range pool.Receipts(s) {
		fmt.Println("receipt:", r.State)
	}
	_, err := p.Submit(ctx, s, weft.New(wefttest.Script()), "third")
	fmt.Println("after Close:", err)
	// Output:
	// receipt: canceled
	// receipt: canceled
	// after Close: thread/pool: pool is closed
}

// Forward steers a running child, explicitly: the message joins the
// child's run at its next drain point. A child that is not running
// refuses, naming its state.
func ExamplePool_Forward() {
	ctx := context.Background()
	st := thread.Memory()
	s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
	p := pool.New(1)
	started, release := make(chan struct{}), make(chan struct{})
	child := waiting(started, release, "converted to euros")
	r, err := p.Submit(ctx, s, child, "convert the totals")
	if err != nil {
		panic(err)
	}
	<-started // mid-run: inside its tool
	if _, err := p.Forward(ctx, s, r.ID, weft.User("use euros, not dollars")); err != nil {
		panic(err)
	}
	close(release)
	final, _ := p.Wait(ctx, s, r.ID)
	fmt.Println("receipt:", final.State, "-", final.Stop)

	// The steer is in the child's transcript, after its prompt.
	childSession, err := thread.Open(ctx, st, r.Child, child)
	if err != nil {
		panic(err)
	}
	for _, m := range childSession.Context() {
		if m.Role == weft.RoleUser {
			fmt.Println("child read:", m.Text())
		}
	}
	_, err = p.Forward(ctx, s, r.ID, weft.User("too late"))
	fmt.Println("after the end:", err != nil, errors.Is(err, pool.ErrNotRunning))
	// Output:
	// receipt: done - converted to euros
	// child read: convert the totals
	// child read: use euros, not dollars
	// after the end: true true
}

// Receipts reads the ledger: every delegation of a session, in
// acceptance order, at the state its entries record last. Settled
// tells the finished from the ones still in flight or parked.
func ExampleReceipts() {
	ctx := context.Background()
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	p := pool.New(2)
	gated := weft.Tool("refund", "", func(context.Context, struct{}) (string, error) {
		return "refunded", nil
	}, weft.RequireApproval())
	quick, _ := p.Submit(ctx, s, weft.New(wefttest.Script(wefttest.Say("42"))), "compute")
	asks, _ := p.Submit(ctx, s, weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "call-refund"}),
	), gated), "refund the order")
	for _, r := range []*pool.Receipt{quick, asks} {
		if _, err := p.Wait(ctx, s, r.ID); err != nil { // at rest: settled, or parked
			panic(err)
		}
	}
	for _, r := range pool.Receipts(s) {
		fmt.Printf("%s settled=%v stop=%q\n", r.State, r.Settled(), r.Stop)
	}
	fmt.Println("delegated output tokens:", s.Usage().Delegated.OutputTokens)
	// Output:
	// done settled=true stop="42"
	// parked settled=false stop=""
	// delegated output tokens: 5
}

// After a restart: a Submit child parked at an approval when the
// process stopped. The new process registers the child's agent —
// nothing on disk carries it — recovers the parent's ledger, and the
// decision resumes the child where it parked.
func ExamplePool_Register() {
	ctx := context.Background()
	st := thread.Memory()
	gated := weft.Tool("refund", "", func(_ context.Context, in struct{ OrderID string }) (string, error) {
		return "refunded " + in.OrderID, nil
	}, weft.RequireApproval())

	// The first process: the child parks, and the process stops.
	s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
	p := pool.New(1)
	r, err := p.Submit(ctx, s, weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "call-refund", Args: `{"order_id":"42"}`}),
	), gated), "refund order 42")
	if err != nil {
		panic(err)
	}
	parked, _ := p.Wait(ctx, s, r.ID)
	fmt.Println("before the restart:", parked.State)
	if err := p.Close(ctx); err != nil { // closes the parked child's session
		panic(err)
	}
	if err := s.Close(ctx); err != nil {
		panic(err)
	}

	// The second process: same storage, new pool, new session values.
	reopened, err := thread.Open(ctx, st, s.ID(), weft.New(wefttest.Script()))
	if err != nil {
		panic(err)
	}
	p2 := pool.New(1)
	resumed := weft.New(wefttest.Script(wefttest.Say("refund issued")), gated)
	if err := p2.Register(r.Child, resumed); err != nil {
		panic(err)
	}
	if err := p2.Recover(ctx, reopened); err != nil {
		panic(err)
	}
	pend := reopened.Pending()
	fmt.Println("pending after the restart:", pend[0].Tool)
	if err := p2.Decide(ctx, reopened, thread.Approve(pend[0].CallID)); err != nil {
		panic(err)
	}
	final, _ := p2.Wait(ctx, reopened, r.ID)
	fmt.Println("after the decision:", final.State, "-", final.Stop)
	// Output:
	// before the restart: parked
	// pending after the restart: refund
	// after the decision: done - refund issued
}

// Recover after a restart, for wrapped delegations: wrapping the same
// agent under the same name is all the new process needs — the name
// is in the child's header — and Recover reattaches what was parked.
// Without the agent, Recover says which child it could not reopen.
func ExamplePool_Recover() {
	ctx := context.Background()
	st := thread.Memory()
	gated := weft.Tool("refund", "", func(_ context.Context, in struct{ OrderID string }) (string, error) {
		return "refunded " + in.OrderID, nil
	}, weft.RequireApproval())

	// The first process parks a sync delegation and stops.
	p := pool.New(1)
	child := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "call-refund", Args: `{"order_id":"42"}`}),
	), gated)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refunds", ID: "call-delegate", Args: `{"prompt":"refund order 42"}`}),
	), p.MustWrap("refunds", "delegates the refund flow", child))
	s, _ := thread.Create(ctx, st, parent)
	t1, _ := s.Send(ctx, weft.User("refund order 42"))
	if _, err := t1.Wait(); err != nil {
		panic(err)
	}
	receipt := pool.Receipts(s)[0]
	if err := p.Close(ctx); err != nil {
		panic(err)
	}
	if err := s.Close(ctx); err != nil {
		panic(err)
	}

	// The second process: the same agents wrapped under the same names.
	p2 := pool.New(1)
	child2 := weft.New(wefttest.Script(wefttest.Say("refund issued")), gated)
	parent2 := weft.New(wefttest.Script(wefttest.Say("handled")),
		p2.MustWrap("refunds", "delegates the refund flow", child2))
	reopened, err := thread.Open(ctx, st, s.ID(), parent2)
	if err != nil {
		panic(err)
	}
	// A pool that never wrapped "refunds" cannot reopen the child, and
	// says so.
	fmt.Println("no agent yet:", errors.Is(pool.New(1).Recover(ctx, reopened), pool.ErrNoAgent))

	if err := p2.Recover(ctx, reopened); err != nil {
		panic(err)
	}
	pend := reopened.Pending()
	if err := p2.Decide(ctx, reopened, thread.Approve(pend[0].CallID)); err != nil {
		panic(err)
	}
	final, _ := p2.Wait(ctx, reopened, receipt.ID)
	fmt.Println("receipt:", final.State, "-", final.Stop)
	// The delegating call resolved with the child's answer, which
	// armed the parent's own resume; Close waits for it.
	if err := reopened.Close(ctx); err != nil {
		panic(err)
	}
	last := reopened.Context()
	fmt.Println("parent:", last[len(last)-1].Text())
	// Output:
	// no agent yet: true
	// receipt: done - refund issued
	// parent: handled
}

// MaxDepth bounds how deep delegation goes, separately from how many
// children run at once. Past it a wrapped tool refuses with
// SUBAGENT_DEPTH — data the delegating model reads.
func ExampleMaxDepth() {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(1, pool.MaxDepth(1)) // children, but no children of children
	leaf := weft.New(wefttest.Script(wefttest.Say("never asked")))
	mid := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "deeper", ID: "call-deeper", Args: `{"prompt":"go on"}`}),
		wefttest.Say("could not delegate further"),
	), p.MustWrap("deeper", "", leaf))
	top := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "delegate", ID: "call-delegate", Args: `{"prompt":"go"}`}),
		wefttest.Say("done"),
	), p.MustWrap("delegate", "", mid))
	s, _ := thread.Create(ctx, st, top)
	turn, _ := s.Send(ctx, weft.User("go"))
	if _, err := turn.Wait(); err != nil {
		panic(err)
	}
	// What the middle agent's model read when it tried to go deeper.
	midSession, err := thread.Open(ctx, st, pool.Receipts(s)[0].Child, mid)
	if err != nil {
		panic(err)
	}
	for _, m := range midSession.Context() {
		for _, part := range m.Content {
			if result, ok := part.(weft.ToolResultPart); ok {
				fmt.Println(result.Content)
			}
		}
	}
	// Output:
	// SUBAGENT_DEPTH: delegation depth 2 exceeds the pool's limit 1
}

// Deleting a session deletes none of its children. Descendants lists
// the whole subtree, deepest first — the order to delete in, the
// parent last.
func ExampleDescendants() {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(1)
	leaf := weft.New(wefttest.Script(wefttest.Say("leaf answer")))
	mid := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "leaf", ID: "call-leaf", Args: `{"prompt":"go"}`}),
		wefttest.Say("mid answer"),
	), p.MustWrap("leaf", "", leaf))
	top := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "mid", ID: "call-mid", Args: `{"prompt":"go"}`}),
		wefttest.Say("done"),
	), p.MustWrap("mid", "", mid))
	s, _ := thread.Create(ctx, st, top)
	turn, _ := s.Send(ctx, weft.User("go"))
	if _, err := turn.Wait(); err != nil {
		panic(err)
	}
	children, _ := pool.Children(ctx, s)
	all, err := pool.Descendants(ctx, s)
	if err != nil {
		panic(err)
	}
	fmt.Println("children:", len(children), "descendants:", len(all))
	fmt.Println("deepest first:", all[len(all)-1] == children[0])

	if err := s.Close(ctx); err != nil {
		panic(err)
	}
	for _, id := range append(all, s.ID()) {
		if err := thread.Delete(ctx, st, id); err != nil {
			panic(err)
		}
	}
	page, _ := thread.List(ctx, st, thread.Query{})
	fmt.Println("sessions left:", page.Total)
	// Output:
	// children: 1 descendants: 2
	// deepest first: true
	// sessions left: 0
}
