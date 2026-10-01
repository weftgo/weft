package pool_test

import (
	"context"
	"fmt"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
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

// An async delegation: Submit returns at once with the acceptance, the
// child runs on the pool's context, and Close drains — every child
// settled before it returns.
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
	// The child settles on the pool's goroutine; a live application
	// reads the receipt back — polled here, bounded for the example's
	// sake — and closes the pool only to drain what is still flying.
	var final pool.Receipt
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		for _, rc := range pool.Receipts(s) {
			if rc.ID == r.ID {
				final = rc
			}
		}
		if final.State == thread.PoolDone {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := p.Close(ctx); err != nil {
		panic(err)
	}
	fmt.Println("settled:", final.State, "-", final.Stop)
	// Output:
	// accepted: accepted
	// settled: done - background answer
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
