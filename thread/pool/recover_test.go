package pool_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
)

// crashed stands in for a process that died without closing
// anything: the writer's hold each of its sessions kept is released
// raw, where the storage keeps one, so a new process can write them.
func crashed(ctx context.Context, st thread.Storage, ids ...string) {
	if r, ok := st.(thread.Releaser); ok {
		for _, id := range ids {
			_ = r.Release(ctx, id)
		}
	}
}

// orphan builds what a dead process leaves behind for one delegation:
// a child session of parent made as the pool makes it — lineage, the
// wrap name — driven as far as drive takes it, and an acceptance and
// a running receipt in the parent that nothing in this process
// answers for.
func orphan(t *testing.T, st thread.Storage, parent *thread.Session, agent *core.Agent, wrap, call string, drive func(*thread.Session)) pool.Receipt {
	t.Helper()
	ctx := context.Background()
	opts := []thread.SessionOption{thread.WithLineage(parent.ID(), call), thread.InheritApprovals(parent)}
	if wrap != "" {
		opts = append(opts, thread.WithMeta(map[string]string{"pool_agent": wrap}))
	}
	child, err := thread.Create(ctx, st, agent, opts...)
	if err != nil {
		t.Fatal(err)
	}
	if drive != nil {
		drive(child)
	}
	crashed(ctx, st, child.ID())
	accept, err := parent.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
		Status: thread.PoolAccepted, Child: child.ID(), Call: call, Prompt: "the task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parent.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
		Receipt: accept.ID, Status: thread.PoolRunning, Child: child.ID(),
	}); err != nil {
		t.Fatal(err)
	}
	return pool.Receipt{ID: accept.ID, State: pool.Running, Child: child.ID(), Call: call}
}

// runTurn sends one prompt and waits the turn out.
func runTurn(t *testing.T, s *thread.Session) {
	t.Helper()
	turn, err := s.Send(context.Background(), core.User("the task"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = turn.Wait() // a failing child is one of the cases
}

// The crash window between a child's park and its mirror (the P2): a
// child parked durably when the process died before the mirror was
// written used to be orphaned for ever — the pump is driven by the
// parent's mirrors, and there were none. Recover writes them, the
// receipt reads parked, and the delegation completes as if nothing
// had happened.
func TestRecoverParkedUnmirrored(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	parent, _ := thread.Create(ctx, st, core.New(wefttest.Script()))
	dead, _ := gatedChild(wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"7"}`}))
	rc := orphan(t, st, parent, dead, "research", "", func(c *thread.Session) { runTurn(t, c) })
	if pend := parent.Pending(); len(pend) != 0 {
		t.Fatalf("the crash left mirrors: %+v", pend)
	}

	p := pool.New(1)
	alive, ran := gatedChild(wefttest.Say("refunded after the crash"))
	p.MustWrap("research", "", alive)
	// Before recovery the pump has nothing to go by.
	if err := p.Decide(ctx, parent); err != nil {
		t.Fatalf("pump: %v", err)
	}
	if got := pool.Receipts(parent)[0]; got.State != pool.Running {
		t.Fatalf("receipt before Recover = %+v", got)
	}
	if err := p.Recover(ctx, parent); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	pend := parent.Pending()
	if len(pend) != 1 || pend[0].CallID != rc.Child+"/c-a" || pend[0].Tool != "refund" || pend[0].Child != rc.Child {
		t.Fatalf("Pending after Recover = %+v, want the child's request mirrored", pend)
	}
	if got := receiptStates(parent, rc.ID); got != "accepted running parked" {
		t.Fatalf("the recovered receipt = %q", got)
	}
	// Recover is idempotent: nothing is mirrored or recorded twice.
	if err := p.Recover(ctx, parent); err != nil {
		t.Fatalf("a second Recover: %v", err)
	}
	if len(parent.Pending()) != 1 || receiptStates(parent, rc.ID) != "accepted running parked" {
		t.Fatalf("a second Recover changed the ledger: %q, %+v", receiptStates(parent, rc.ID), parent.Pending())
	}
	if err := p.Decide(ctx, parent, thread.Approve(pend[0].CallID)); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	final := waitFor(t, p, parent, rc.ID, pool.Done)
	if final.Stop != "refunded after the crash" {
		t.Errorf("settled = %+v", final)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Errorf("approved flags = %v", got)
	}
}

// Every unsettled receipt of a dead process reaches a final state
// with its cause: settled from the child's own file when the child
// finished, failed when it never did or is gone.
func TestRecoverSettles(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	parent, _ := thread.Create(ctx, st, core.New(wefttest.Script()))
	run := func(c *thread.Session) { runTurn(t, c) }

	done := orphan(t, st, parent, core.New(wefttest.Script(wefttest.Say("finished before the crash"))), "", "", run)
	failed := orphan(t, st, parent, core.New(wefttest.Script(wefttest.Fail(errors.New("connection reset")))), "", "", run)
	capped := orphan(t, st, parent, core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "loop", ID: "c-loop"}),
		wefttest.ToolCalls(wefttest.Call{Name: "loop", ID: "c-loop-2"}),
	), core.MaxSteps(1), core.Tool("loop", "", func(context.Context, struct{}) (string, error) { return "again", nil })), "", "", run)
	never := orphan(t, st, parent, core.New(wefttest.Script()), "", "", nil)
	gone := orphan(t, st, parent, core.New(wefttest.Script()), "", "", nil)
	if err := thread.Delete(ctx, st, gone.Child); err != nil {
		t.Fatal(err)
	}

	p := pool.New(1)
	if err := p.Recover(ctx, parent); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	byID := map[string]pool.Receipt{}
	for _, r := range pool.Receipts(parent) {
		byID[r.ID] = r
	}
	if r := byID[done.ID]; r.State != pool.Done || r.Stop != "finished before the crash" {
		t.Errorf("the finished child's receipt = %+v", r)
	}
	if r := byID[failed.ID]; r.State != pool.Failed || !strings.Contains(r.Stop, "connection reset") {
		t.Errorf("the failed child's receipt = %+v", r)
	}
	if r := byID[capped.ID]; r.State != pool.Capped {
		t.Errorf("the capped child's receipt = %+v", r)
	}
	if r := byID[never.ID]; r.State != pool.Failed || !strings.Contains(r.Stop, "did not survive a restart") {
		t.Errorf("the never-run child's receipt = %+v", r)
	}
	if r := byID[gone.ID]; r.State != pool.Failed || !strings.Contains(r.Stop, "is gone") {
		t.Errorf("the deleted child's receipt = %+v", r)
	}
	// The finished child's cost reaches the parent's ledger with its
	// settlement — once: a second Recover settles nothing again.
	before := parent.Usage().Delegated
	if before.OutputTokens == 0 {
		t.Errorf("Delegated = %+v, want the recovered children's cost", before)
	}
	if err := p.Recover(ctx, parent); err != nil {
		t.Fatalf("a second Recover: %v", err)
	}
	if after := parent.Usage().Delegated; after != before {
		t.Errorf("a second Recover billed again: %+v, was %+v", after, before)
	}
	for _, r := range pool.Receipts(parent) {
		if n := len(strings.Fields(receiptStates(parent, r.ID))); n != 3 {
			t.Errorf("receipt %s has %d ledger entries (%s), want accepted, running and one settlement", r.ID, n, receiptStates(parent, r.ID))
		}
	}
}

// A settlement whose resolution a crash lost: the receipt is done,
// the parent's delegating call still parked — hidden from Pending,
// decidable by nobody. Recover resolves it from the ledger.
func TestRecoverResolvesParkedWrapper(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	// The parent's own gated tool stands in for the delegating call:
	// what matters is a call parked in the parent, named by a mirror.
	agent, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-wrapper", Args: `{"order_id":"1"}`}),
		wefttest.Say("all done"),
	)
	parent, _ := thread.Create(ctx, st, agent)
	t1, err := parent.Send(ctx, core.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	accept, err := parent.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
		Status: thread.PoolAccepted, Child: "s_child", Call: "c-wrapper",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parent.AppendApprovalRequests(ctx, thread.ApprovalRequestEntry{
		CallID: "s_child/c-a", Tool: "wire", ArgsSHA256: "h", RunID: "s_child-t1",
		Child: "s_child", Wrapper: "c-wrapper",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := parent.Decide(ctx, thread.Approve("s_child/c-a")); err != nil {
		t.Fatal(err)
	}
	if _, err := parent.AppendPoolReceipt(ctx, thread.PoolReceiptEntry{
		Receipt: accept.ID, Status: thread.PoolDone, Child: "s_child", Stop: "the child's answer",
	}); err != nil {
		t.Fatal(err)
	}
	if t1.Next() != nil || len(parent.Pending()) != 0 {
		t.Fatal("the scenario is not the wedge: the parent resumed, or still offers something")
	}
	p := pool.New(1)
	if err := p.Recover(ctx, parent); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	next := t1.Next()
	if next == nil {
		t.Fatal("Recover did not resolve the parked delegating call")
	}
	if res, err := next.Wait(); err != nil || res.Text() != "all done" {
		t.Fatalf("the parent's continuation: %v, %v", res, err)
	}
	if got := toolResultsOf(parent.Context()); len(got) != 1 || got[0] != "the child's answer" {
		t.Errorf("the delegating call's result = %q", got)
	}
	if len(ran.snapshot()) != 0 {
		t.Error("the delegating call's own handler ran")
	}
	// And once: a second Recover finds nothing to resolve.
	if err := p.Recover(ctx, parent); err != nil {
		t.Fatalf("a second Recover: %v", err)
	}
}

// Recover reports the children it cannot reopen and recovers the
// rest; it leaves alone what this pool is running; and Cancel on a
// receipt left by a dead process recovers it first.
func TestRecoverPartialAndLive(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	parent, _ := thread.Create(ctx, st, core.New(wefttest.Script()))
	gated := func() *core.Agent {
		a, _ := gatedChild(wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"7"}`}))
		return a
	}
	run := func(c *thread.Session) { runTurn(t, c) }
	unknown := orphan(t, st, parent, gated(), "", "", run)         // a Submit child nobody registered
	renamed := orphan(t, st, parent, gated(), "old-name", "", run) // a wrap this process does not have
	finished := orphan(t, st, parent, core.New(wefttest.Script(wefttest.Say("done"))), "", "", run)

	p := pool.New(2)
	started, release := make(chan struct{}), make(chan struct{})
	live, err := p.Submit(ctx, parent, core.New(blocking{release: release, text: "live",
		onStart: func() { close(started) }}), "run")
	if err != nil {
		t.Fatal(err)
	}
	<-started

	err = p.Recover(ctx, parent)
	if !errors.Is(err, pool.ErrNoAgent) {
		t.Fatalf("Recover = %v, want ErrNoAgent for the two it cannot reopen", err)
	}
	for _, rc := range []pool.Receipt{unknown, renamed} {
		if !strings.Contains(err.Error(), rc.Child) {
			t.Errorf("the error does not name %s: %v", rc.Child, err)
		}
	}
	if !strings.Contains(err.Error(), `"old-name"`) {
		t.Errorf("the error does not name the wrap to restore: %v", err)
	}
	state := func(id string) pool.State {
		for _, r := range pool.Receipts(parent) {
			if r.ID == id {
				return r.State
			}
		}
		return ""
	}
	if state(finished.ID) != pool.Done {
		t.Errorf("the recoverable receipt = %s, want done", state(finished.ID))
	}
	if state(live.ID) != pool.Running {
		t.Errorf("Recover touched a live delegation: %s", state(live.ID))
	}
	if state(unknown.ID) != pool.Running || state(renamed.ID) != pool.Running {
		t.Errorf("unrecoverable receipts = %s, %s; want them left as they were", state(unknown.ID), state(renamed.ID))
	}
	// Forward says what such a receipt is.
	_, ferr := p.Forward(ctx, parent, unknown.ID, core.User("x"))
	var se *pool.StateError
	if !errors.As(ferr, &se) || !se.Orphan {
		t.Errorf("Forward to an orphaned receipt = %v, want a StateError marked Orphan", ferr)
	}
	// Cancel recovers first: without the agent it cannot, and says so…
	if err := p.Cancel(ctx, parent, unknown.ID); !errors.Is(err, pool.ErrNoAgent) {
		t.Errorf("Cancel of an unrecoverable orphan = %v, want ErrNoAgent", err)
	}
	// …and with it, the parked orphan is recovered and canceled.
	if err := p.Register(unknown.Child, gated()); err != nil {
		t.Fatal(err)
	}
	if err := p.Cancel(ctx, parent, unknown.ID); err != nil {
		t.Fatalf("Cancel of a recoverable orphan: %v", err)
	}
	if state(unknown.ID) != pool.Canceled {
		t.Errorf("the canceled orphan = %s", state(unknown.ID))
	}
	close(release)
	waitFor(t, p, parent, live.ID, pool.Done)
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Recover(ctx, parent); !errors.Is(err, pool.ErrClosed) {
		t.Errorf("Recover on a closed pool = %v", err)
	}
}

// A child whose resume ran to its end in a process that died before
// the settlement: the receipt reads unsettled, the mirror decided,
// the child's boundary long closed. The pump's resume finds nothing
// to resume — and settles the receipt from the child's own file
// instead of leaving it, or failing a delegation that succeeded.
func TestPumpSettlesAChildThatAlreadyResumed(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	parent, _ := thread.Create(ctx, st, core.New(wefttest.Script()))
	dead, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c-a", Args: `{"order_id":"7"}`}),
		wefttest.Say("refunded before the crash"),
	)
	var mirror thread.ApprovalRequestEntry
	rc := orphan(t, st, parent, dead, "research", "", func(c *thread.Session) {
		runTurn(t, c)
		req := c.Pending()[0]
		mirror = thread.ApprovalRequestEntry{
			CallID: c.ID() + "/" + req.CallID, Tool: req.Tool, Args: req.Args, ArgsSHA256: req.ArgsSHA256,
			RunID: req.RunID, Child: c.ID(),
		}
		// The dead process's resume: decided, run, finished.
		rt, err := c.ReplayDecisions(ctx, thread.ApprovalDecisionEntry{CallID: req.CallID, Outcome: thread.OutcomeApprove, Who: "alice"})
		if err != nil || rt == nil {
			t.Fatalf("the dead process's resume: %v, %v", rt, err)
		}
		if _, err := rt.Wait(); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := parent.AppendApprovalRequests(ctx, mirror); err != nil {
		t.Fatal(err)
	}
	if _, err := parent.Decide(ctx, thread.Approve(mirror.CallID)); err != nil {
		t.Fatal(err)
	}

	p := pool.New(1)
	alive, ranAgain := gatedChild(wefttest.Say("must not run"))
	p.MustWrap("research", "", alive)
	if err := p.Decide(ctx, parent); err != nil {
		t.Fatalf("pump: %v", err)
	}
	final := waitFor(t, p, parent, rc.ID, pool.Done)
	if final.Stop != "refunded before the crash" {
		t.Errorf("settled = %+v, want the answer the child's file holds", final)
	}
	if got := ran.snapshot(); len(got) != 1 {
		t.Errorf("the dead process's child ran its call %d times", len(got))
	}
	if got := ranAgain.snapshot(); len(got) != 0 {
		t.Errorf("the new process ran the child again: %v", got)
	}
	if u := parent.Usage().Delegated; u.OutputTokens != 10 {
		t.Errorf("Delegated = %+v, want both of the child's steps", u)
	}
}
