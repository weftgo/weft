package pool_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/thread/pool"
	"github.com/weftgo/weft/wefttest"
)

// gatedChild builds the child agent of the nested-approval tests: one
// refund tool behind RequireApproval, whose handler records the
// Approved flag it ran under.
func gatedChild(turns ...wefttest.Turn) (*weft.Agent, *flags) {
	ran := &flags{}
	tool := weft.Tool("refund", "", func(ctx context.Context, in struct{ OrderID string }) (string, error) {
		call, _ := weft.CallFromContext(ctx)
		ran.add(call.Approved)
		return "refunded " + in.OrderID, nil
	}, weft.RequireApproval())
	return weft.New(wefttest.Script(turns...), tool), ran
}

type flags struct {
	mu sync.Mutex
	v  []bool
}

func (f *flags) add(b bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.v = append(f.v, b)
}

func (f *flags) snapshot() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.v...)
}

// nestedParent builds the parent agent whose model delegates once to
// the wrapped child, then concludes.
func nestedParent(p *pool.Pool, child *weft.Agent, opts ...pool.WrapOption) *weft.Agent {
	return weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research",
			Args: wefttest.Args(struct{ Prompt string }{"refund order 1234"})}),
		wefttest.Say("all done"),
	), p.Wrap("research", "delegates the refund flow", child, opts...))
}

// TestNestedSyncFullFlow is ADR 0022 §7's sync half, end to end: the
// child parks, the parent's call parks through the middleware rule,
// the child's requests surface on the parent's Pending with their
// lineage (the wrapper hidden), a decision through the pool resumes
// the child, and the parent's parked call resolves with the child's
// answer — the parent model reads it as the tool result and finishes.
func TestNestedSyncFullFlow(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(2)
	child, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1234"}`}),
		wefttest.Say("child done: refunded 1234"),
	)
	s, err := thread.Create(ctx, st, nestedParent(p, child))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t1, err := s.Send(ctx, weft.User("refund it"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	res1, err := t1.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(res1.Pending) != 1 {
		t.Fatalf("parent parked %d calls, want 1 (the wrapper)", len(res1.Pending))
	}
	wrapper := res1.Pending[0]

	// The offered view: the child's request, with lineage; never the
	// wrapper.
	pend := s.Pending()
	if len(pend) != 1 {
		t.Fatalf("Pending = %+v, want the child's one request", pend)
	}
	req := pend[0]
	if req.Tool != "refund" || req.CallID == wrapper.ID {
		t.Fatalf("pending request = %+v (wrapper is %s)", req, wrapper.ID)
	}
	if req.Child == "" || req.Child == s.ID() {
		t.Errorf("request lineage = %q", req.Child)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Fatalf("gated tool ran before any decision: %v", got)
	}

	// Decide through the pool: records in the parent, replays into
	// the child, resumes it, and completes the wrapper with the
	// child's answer.
	ct, err := p.Decide(ctx, s, thread.Approve(req.CallID))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if ct == nil {
		t.Fatalf("Decide resumed no child turn")
	}
	if _, err := ct.Wait(); err != nil {
		t.Fatalf("child resume: %v", err)
	}
	// The parent's own continuation, through its parked boundary.
	next := t1.Next()
	if next == nil {
		t.Fatalf("no continuation turn after the wrapper resolved")
	}
	res2, err := next.Wait()
	if err != nil {
		t.Fatalf("parent continuation: %v", err)
	}
	if res2.Text() != "all done" {
		t.Errorf("parent reply = %q", res2.Text())
	}
	results := toolResultsOf(s.Context())
	if len(results) != 1 || results[0] != "child done: refunded 1234" {
		t.Errorf("parent context tool results = %v", results)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Errorf("approved flags = %v, want [true]", got)
	}
	if pend := s.Pending(); len(pend) != 0 {
		t.Errorf("Pending after the flow = %+v", pend)
	}
	rs := pool.Receipts(s)
	if len(rs) != 1 || rs[0].State != thread.PoolDone {
		t.Fatalf("receipts = %+v", rs)
	}
	if rs[0].Stop != "child done: refunded 1234" {
		t.Errorf("settled stop = %q", rs[0].Stop)
	}
}

// The async half: no wrapper exists — the receipt line was the tool
// result — yet the child's parked calls still surface on the parent
// and decide through the pool, the receipt settling when the child
// ends (ADR 0022 §7).
func TestNestedAsyncParkAndDecide(t *testing.T) {
	ctx := context.Background()
	p := pool.New(2)
	child, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"7"}`}),
		wefttest.Say("child finished the refund"),
	)
	s, _ := thread.Create(ctx, thread.Memory(), nestedParent(p, child, pool.Async()))
	t1, err := s.Send(ctx, weft.User("refund it"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var pend []thread.Request
	for time.Now().Before(deadline) {
		pend = s.Pending()
		if len(pend) == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(pend) != 1 || pend[0].Tool != "refund" || pend[0].Child == "" {
		t.Fatalf("Pending = %+v", pend)
	}
	if _, err := p.Decide(ctx, s, thread.Approve(pend[0].CallID)); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	final := waitState(t, s, thread.PoolDone)
	if final.Stop != "child finished the refund" {
		t.Errorf("settled receipt = %+v", final)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Errorf("approved flags = %v", got)
	}
	if pend := s.Pending(); len(pend) != 0 {
		t.Errorf("Pending after = %+v", pend)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// The restart review focus: the whole process dies after the park,
// and a brand-new pool and session objects decide and resume — the
// wrap name in the child's header is the agent the re-Wrap restores.
func TestNestedAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatalf("jsonl.Open: %v", err)
	}
	p := pool.New(2)
	child, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"9"}`}),
		wefttest.Say("resumed after the restart"),
	)
	s, err := thread.Create(ctx, st, nestedParent(p, child))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t1, err := s.Send(ctx, weft.User("refund it"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := t1.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if pend := s.Pending(); len(pend) != 1 {
		t.Fatalf("parked = %+v", pend)
	}

	// The restart: new pool, new session objects, same wrap name. The
	// resumed child's agent starts at the continuation — the session's
	// transcript is the state; the agent's script only stands in for
	// the model the resumed run is about to call.
	p2 := pool.New(2)
	child2, ran2 := gatedChild(wefttest.Say("resumed after the restart"))
	// The restarted parent's agent resumes positioned too: its first
	// model call is the continuation the boundary's resume is about to
	// make, not the delegation it already made.
	resumeParent := weft.New(wefttest.Script(wefttest.Say("all done")),
		p2.Wrap("research", "delegates the refund flow", child2))
	open, err := thread.Open(ctx, st, s.ID(), resumeParent)
	if err != nil {
		t.Fatalf("Open parent: %v", err)
	}
	pend := open.Pending()
	if len(pend) != 1 || pend[0].Tool != "refund" {
		t.Fatalf("Pending after restart = %+v", pend)
	}
	ct, err := p2.Decide(ctx, open, thread.Approve(pend[0].CallID))
	if err != nil {
		t.Fatalf("Decide after restart: %v", err)
	}
	if ct == nil {
		t.Fatalf("no resumed child turn")
	}
	if _, err := ct.Wait(); err != nil {
		t.Fatalf("child resume: %v", err)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Errorf("the pre-restart agent ran: %v", got)
	}
	if got := ran2.snapshot(); len(got) != 1 || !got[0] {
		t.Errorf("post-restart approved flags = %v, want [true]", got)
	}
	rs := pool.Receipts(open)
	if len(rs) != 1 || rs[0].State != thread.PoolDone || rs[0].Stop != "resumed after the restart" {
		t.Fatalf("receipts after restart = %+v", rs)
	}
	// The parent's boundary closes through its own resume — the
	// wrapper resolving with the child's answer — which arms as the
	// decisions land; a restarted process has no Turn handle to wait,
	// so the test polls the context for the real result (before it
	// lands, the repaired view reads the dangling call's marker).
	deadline := time.Now().Add(5 * time.Second)
	var results []string
	for time.Now().Before(deadline) {
		results = toolResultsOf(open.Context())
		if len(results) == 1 && results[0] == "resumed after the restart" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(results) != 1 || results[0] != "resumed after the restart" {
		t.Errorf("parent tool results = %v", results)
	}
}

// A signed decision for a child's call (the 5.3 review focus): the
// challenge the parent mints covers the child's call, the signature
// verifies there, and the pool's pump resumes the child the plain
// Decide never touched.
func TestNestedSignedDecision(t *testing.T) {
	ctx := context.Background()
	secret := []byte("sixteen-byte test secret!")
	ring, err := thread.NewKeyring(thread.Key{ID: "k1", Secret: secret, Active: true})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	p := pool.New(1)
	child, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"5"}`}),
		wefttest.Say("signed refund done"),
	)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"go"}`}),
		wefttest.Say("all done"),
	), p.Wrap("research", "", child))
	s, err := thread.Create(ctx, thread.Memory(), parent, thread.WithKeyring(ring))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t1, _ := s.Send(ctx, weft.User("refund it"))
	if _, err := t1.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var req thread.Request
	for time.Now().Before(deadline) {
		pend := s.Pending()
		if len(pend) == 1 {
			req = pend[0]
			break
		}
		time.Sleep(time.Millisecond)
	}
	if req.CallID == "" {
		t.Fatalf("no nested request surfaced")
	}
	challenge, err := s.Request(req.CallID)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	sd := thread.SignDecision(secret, challenge, thread.Approve(req.CallID))
	if _, err := s.DecideSigned(ctx, sd); err != nil {
		t.Fatalf("DecideSigned: %v", err)
	}
	// The signed decision recorded; the pump drives the child.
	if _, err := p.Decide(ctx, s); err != nil {
		t.Fatalf("pump: %v", err)
	}
	final := waitState(t, s, thread.PoolDone)
	if final.Stop != "signed refund done" {
		t.Errorf("settled receipt = %+v", final)
	}
	if got := ran.snapshot(); len(got) != 1 || !got[0] {
		t.Errorf("approved flags = %v", got)
	}
}

// Deny propagates the same way: the child's run continues with the
// denial the model sees, and whatever it then says is the answer the
// wrapper resolves with.
func TestNestedDeny(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(1)
	child, _ := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"3"}`}),
		wefttest.Say("refund refused by the approver"),
	)
	s, _ := thread.Create(ctx, st, nestedParent(p, child))
	t1, _ := s.Send(ctx, weft.User("refund it"))
	if _, err := t1.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	pend := s.Pending()
	if len(pend) != 1 {
		t.Fatalf("Pending = %+v", pend)
	}
	ct, err := p.Decide(ctx, s, thread.Deny(pend[0].CallID, "out of policy"))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if ct == nil {
		t.Fatalf("no resumed turn")
	}
	if _, err := ct.Wait(); err != nil {
		t.Fatalf("child resume: %v", err)
	}
	next := t1.Next()
	if next == nil {
		t.Fatalf("no continuation")
	}
	if _, err := next.Wait(); err != nil {
		t.Fatalf("continuation: %v", err)
	}
	results := toolResultsOf(s.Context())
	if len(results) != 1 || results[0] != "refund refused by the approver" {
		t.Errorf("parent tool results = %v", results)
	}
	// The denial the child's model saw is in the child session's own
	// context.
	rs := pool.Receipts(s)
	childOpen, err := thread.Open(ctx, st, rs[0].Child, child)
	if err != nil {
		t.Fatalf("Open child: %v", err)
	}
	saw := false
	for _, m := range childOpen.Context() {
		for _, part := range m.Content {
			if tp, ok := part.(weft.ToolResultPart); ok && tp.IsError && tp.Content == "DENIED: out of policy" {
				saw = true
			}
		}
	}
	if !saw {
		t.Errorf("the child model never saw the denial text")
	}
}

// Explicit steering (ADR 0022 §8): Forward delivers into the running
// child's turn; an unknown or settled receipt refuses.
func TestForward(t *testing.T) {
	ctx := context.Background()
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	p := pool.New(1)
	inTool := make(chan struct{})
	proceed := make(chan struct{})
	child := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "work", Args: `{}`}),
		wefttest.Say("steer noted"),
	), weft.Tool("work", "", func(_ context.Context, _ struct{}) (string, error) {
		close(inTool)
		<-proceed
		return "worked", nil
	}))
	r, err := p.Submit(ctx, s, child, "go")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	<-inTool // the child is mid-run inside its tool

	st, err := p.Forward(ctx, r.ID, weft.User("switch to euros"))
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	close(proceed)
	if _, err := st.Wait(); err != nil {
		t.Fatalf("steer turn: %v", err)
	}
	final := waitState(t, s, thread.PoolDone)
	// The forwarded message reached the child's transcript.
	childOpen, err := thread.Open(ctx, s.Storage(), final.Child, child)
	if err != nil {
		t.Fatalf("Open child: %v", err)
	}
	saw := false
	for _, m := range childOpen.Context() {
		if m.Role == weft.RoleUser {
			for _, part := range m.Content {
				if tp, ok := part.(weft.TextPart); ok && tp.Text == "switch to euros" {
					saw = true
				}
			}
		}
	}
	if !saw {
		t.Errorf("the forwarded message never reached the child's transcript")
	}
	// A settled receipt refuses.
	if _, err := p.Forward(ctx, r.ID, weft.User("late")); !errors.Is(err, pool.ErrNotRunning) {
		t.Errorf("Forward after settle err = %v", err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// Forward serves the whole running phase, from the turn's start: the
// running mark lands before the child's Send flies, so a steer sent
// the moment the model began is never refused as queued — the window
// between Send returning and the mark was a false ErrNotRunning.
func TestForwardSeesStart(t *testing.T) {
	ctx := context.Background()
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	p := pool.New(1)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	child := weft.New(blocking{release: release, text: "ran",
		onStart: func() { once.Do(func() { close(started) }) }})
	r, err := p.Submit(ctx, s, child, "go")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	<-started // the model is running: Forward must serve it, not refuse
	if _, err := p.Forward(ctx, r.ID, weft.User("steer")); err != nil {
		t.Fatalf("Forward at the run's start: %v", err)
	}
	close(release)
	waitState(t, s, thread.PoolDone)
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// Concurrent Decides over one parent are safe: pumps racing an
// approving Decide see the in-flight resume and skip it — one resume
// per child, whichever call got there first (the pumping flag is pool
// state, read under the pool lock).
func TestConcurrentDecide(t *testing.T) {
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
		p := pool.New(1)
		child, _ := gatedChild(
			wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"1"}`}),
			wefttest.Say("once only"),
		)
		if _, err := p.Submit(ctx, s, child, "refund order 1"); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for len(s.Pending()) == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if len(s.Pending()) != 1 {
			t.Fatalf("mirrored requests = %+v", s.Pending())
		}
		call := s.Pending()[0].CallID
		start := make(chan struct{})
		var wg sync.WaitGroup
		for g := 0; g < 3; g++ {
			wg.Add(1)
			go func(approve bool) {
				defer wg.Done()
				<-start
				if approve {
					_, _ = p.Decide(ctx, s, thread.Approve(call))
					return
				}
				_, _ = p.Decide(ctx, s) // a pure pump
			}(g == 0)
		}
		close(start)
		wg.Wait()
		final := waitState(t, s, thread.PoolDone)
		if final.Stop != "once only" {
			t.Fatalf("iteration %d settled = %+v", i, final)
		}
		if err := p.Close(ctx); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
}

// The bare SUBAGENT_PENDING path is untouched: a wrapped tool called
// outside any session — no parent to mirror onto — keeps the ordinary
// subagent behaviour, where a child that parks fails the call loudly.
func TestBareSubagentPendingUnchanged(t *testing.T) {
	ctx := context.Background()
	p := pool.New(1)
	tool := weft.Tool("spend", "", func(_ context.Context, _ struct{}) (string, error) {
		return "spent", nil
	}, weft.RequireApproval())
	child := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "spend", Args: `{}`}),
	), tool)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "ask", Args: `{"prompt":"go"}`}),
		wefttest.Say("noted the failure"),
	), p.Wrap("ask", "", child))
	res, err := parent.Generate(ctx, weft.Prompt("go"))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Text() != "noted the failure" {
		t.Errorf("reply = %q", res.Text())
	}
	saw := ""
	for _, m := range res.Messages {
		for _, part := range m.Content {
			if tp, ok := part.(weft.ToolResultPart); ok {
				saw = tp.Content
			}
		}
	}
	if want := `SUBAGENT_PENDING: agent "ask" ended awaiting approval of 1 call(s)`; saw != want {
		t.Errorf("tool result:\n got %q\nwant %q", saw, want)
	}
}

// Register is the restart hook for children no wrap names: a parked
// Submit child resumes under a registered agent after the process
// died between submit and decide.
func TestRegisterResumesParkedSubmitChild(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	p := pool.New(1)
	child, ran := gatedChild(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"2"}`}),
		wefttest.Say("registered resume done"),
	)
	s, _ := thread.Create(ctx, st, weft.New(wefttest.Script()))
	r, err := p.Submit(ctx, s, child, "refund order 2")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitState(t, s, thread.PoolRunning)
	// A "restart": a new pool holding nothing but a registration.
	p2 := pool.New(1)
	child2, ran2 := gatedChild(wefttest.Say("registered resume done"))
	p2.Register(r.Child, child2)
	open, err := thread.Open(ctx, st, s.ID(), weft.New(wefttest.Script()))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	pend := open.Pending()
	if len(pend) != 1 {
		t.Fatalf("Pending = %+v", pend)
	}
	if _, err := p2.Decide(ctx, open, thread.Approve(pend[0].CallID)); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	final := waitState(t, open, thread.PoolDone)
	if final.ID != r.ID || final.Stop != "registered resume done" {
		t.Errorf("settled = %+v", final)
	}
	if got := ran.snapshot(); len(got) != 0 {
		t.Errorf("pre-restart agent ran: %v", got)
	}
	if got := ran2.snapshot(); len(got) != 1 || !got[0] {
		t.Errorf("registered agent flags = %v", got)
	}
}

// toolResultsOf renders a context's tool results in order.
func toolResultsOf(msgs []weft.Message) []string {
	var out []string
	for _, m := range msgs {
		for _, part := range m.Content {
			if tp, ok := part.(weft.ToolResultPart); ok {
				out = append(out, tp.Content)
			}
		}
	}
	return out
}

// Forward refuses a child that has not started: the session is idle,
// and a steer into an idle session would run as its first turn — the
// task's own prompt queueing behind its steer (ADR 0022 §8's "running
// child").
func TestForwardRequiresRunning(t *testing.T) {
	ctx := context.Background()
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	p := pool.New(1)
	warmStarted := make(chan struct{})
	warmRelease := make(chan struct{})
	warm, err := p.Submit(ctx, s, weft.New(blocking{release: warmRelease, text: "warm",
		onStart: func() { close(warmStarted) }}), "warm")
	if err != nil {
		t.Fatalf("Submit warm: %v", err)
	}
	<-warmStarted // the only slot is held before the task queues
	waitStarted := make(chan struct{})
	release := make(chan struct{})
	// Queued behind the warm-up: accepted, not running.
	r, err := p.Submit(ctx, s, weft.New(blocking{release: release, text: "task",
		onStart: func() { close(waitStarted) }}), "the task")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if _, err := p.Forward(ctx, r.ID, weft.User("early")); !errors.Is(err, pool.ErrNotRunning) {
		t.Fatalf("Forward to a queued child err = %v, want ErrNotRunning", err)
	}
	close(warmRelease)
	waitState(t, s, thread.PoolDone)
	<-waitStarted
	if _, err := p.Forward(ctx, r.ID, weft.User("on time")); err != nil {
		t.Fatalf("Forward to the now-running child: %v", err)
	}
	close(release)
	waitState(t, s, thread.PoolDone)
	if _, err := p.Forward(ctx, warm.ID, weft.User("late")); !errors.Is(err, pool.ErrNotRunning) {
		t.Errorf("Forward after settle err = %v, want ErrNotRunning", err)
	}
	if err := p.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
