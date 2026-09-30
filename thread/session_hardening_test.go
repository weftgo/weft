package thread

import (
	"context"
	"iter"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// gatedModel delegates to a script, blocking call i until gates[i] is
// closed (calls without a gate run at once).
type gatedModel struct {
	inner *wefttest.Model
	gates map[int]chan struct{}
	n     atomic.Int32
}

func (g *gatedModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	i := int(g.n.Add(1)) - 1
	if ch, ok := g.gates[i]; ok {
		select {
		case <-ch:
		case <-ctx.Done():
		}
	}
	return g.inner.Stream(ctx, req)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(what)
}

// TestForkAtOffPathLeafEntryRefused: a fork is positioned where its
// own file reopens — a trailing leaf entry redirects to its target — so
// a fork at a navigation to another branch, whose target the copied
// path does not hold, is refused before anything is written. Before,
// the fork stood on the leaf entry itself and its file failed to Open
// (post-0.7 review).
func TestForkAtOffPathLeafEntryRefused(t *testing.T) {
	ctx := context.Background()
	st := Memory()
	s, err := Create(ctx, st, weft.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CustomMessage(ctx, "n", weft.User("a")); err != nil {
		t.Fatal(err)
	}
	a := s.Leaf()
	if err := s.CustomMessage(ctx, "n", weft.User("b")); err != nil {
		t.Fatal(err)
	}
	b := s.Leaf()
	if err := s.Branch(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.CustomMessage(ctx, "n", weft.User("c")); err != nil {
		t.Fatal(err)
	}
	if err := s.Branch(ctx, b); err != nil { // leaf entry on the c line, target on the b line
		t.Fatal(err)
	}
	es := s.Entries()
	leafEntry := idOf(es[len(es)-1])
	if _, err := s.Fork(ctx, leafEntry); err == nil {
		t.Fatal("a fork at a leaf entry navigating off its path was accepted")
	}
	page, err := List(ctx, st, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Fatalf("the refused fork left %d sessions in storage, want 1", page.Total)
	}
	// A leaf entry whose target is on the path forks onto the target,
	// live and reopened alike.
	if err := s.Branch(ctx, a); err != nil {
		t.Fatal(err)
	}
	es = s.Entries()
	f, err := s.Fork(ctx, idOf(es[len(es)-1]))
	if err != nil {
		t.Fatal(err)
	}
	re, err := Open(ctx, st, f.ID(), weft.New(wefttest.Script()))
	if err != nil {
		t.Fatalf("the fork cannot be reopened: %v", err)
	}
	if f.Leaf() != a || re.Leaf() != a {
		t.Fatalf("fork leaf live %q, reopened %q, want %q", f.Leaf(), re.Leaf(), a)
	}
}

// TestCrashMidToolIsNoBoundary: a crash while a tool runs (per-step
// durability wrote the assistant's call, the tool message never came)
// leaves no approval boundary — nothing recorded a park, and ADR 0011
// §7's input repair answers the dangling call. Before, the reopened
// session read it as pending and every Send queued behind it forever
// (post-0.7 review).
func TestCrashMidToolIsNoBoundary(t *testing.T) {
	ctx := context.Background()
	release := make(chan struct{})
	entered := make(chan struct{})
	slow := weft.Tool("slow", "", func(ctx context.Context, _ struct{}) (string, error) {
		close(entered)
		<-release
		return "ok", nil
	})
	st := Memory()
	s, err := Create(ctx, st, weft.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "slow"}), wefttest.Say("done")), slow))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, weft.User("go")); err != nil {
		t.Fatal(err)
	}
	<-entered
	defer close(release)
	// The crash: snapshot the storage exactly as it stands mid-tool.
	h, entries, _, err := st.Load(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	st2 := Memory()
	if err := st2.Create(ctx, h); err != nil {
		t.Fatal(err)
	}
	if err := st2.Append(ctx, h.ID, entries...); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, st2, h.ID, weft.New(wefttest.Script(wefttest.Say("after the crash"))))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("pending after reopen: %+v", s2.Pending())
	turn, err := s2.Send(ctx, weft.User("hello again"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = turn.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("the Send after a mid-tool crash never ran: queued behind a phantom approval boundary (pending %d)", len(s2.Pending()))
	}
}

type lockOnceEstimator struct {
	once sync.Once
	t    atomic.Pointer[Turn]
}

func (e *lockOnceEstimator) Estimate(msgs []weft.Message) int {
	if t := e.t.Load(); t != nil {
		e.once.Do(func() { t.mu.Lock() }) // stall the runner just before t.finish
	}
	return 1
}

// TestUsurpedRunnerLeavesNewTurnsSteers: a Send may take the slot in
// the window between a turn's retire and its runner's epilogue; the
// old runner must not settle the new run's steers. Before, it deferred
// a steer the new run had already delivered — the model saw it twice
// (post-0.7 review).
func TestUsurpedRunnerLeavesNewTurnsSteers(t *testing.T) {
	ctx := context.Background()
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	g := &gatedModel{
		inner: wefttest.Script(
			wefttest.Say("one"),
			wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
			wefttest.Say("two"),
			wefttest.Say("three"),
			wefttest.Say("four"),
		),
		gates: map[int]chan struct{}{0: make(chan struct{}), 1: make(chan struct{}), 2: make(chan struct{})},
	}
	est := &lockOnceEstimator{}
	s, err := Create(ctx, Memory(), weft.New(g, echo), WithEstimator(est))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("first"))
	if err != nil {
		t.Fatal(err)
	}
	est.t.Store(t1)
	close(g.gates[0])
	// t1's runner is now parked inside t1.finish, after the retire.
	waitFor(t, "t1 never retired", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.running && s.inFlight == nil && len(s.order) >= 3
	})
	t2, err := s.Send(ctx, weft.User("second"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.Send(ctx, weft.User("steer me"), As(Steer))
	if err != nil {
		t.Fatal(err)
	}
	close(g.gates[1]) // t2 step 1: the tool call, then the drain takes the steer
	waitFor(t, "the steer was never handed", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.handed) == 1
	})
	t1.mu.Unlock() // the old runner resumes: finish, then its epilogue
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	close(g.gates[2])
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	if n := st.Next(); n != nil {
		_, _ = n.Wait()
	}
	s.mu.Lock()
	idle := !s.running
	s.mu.Unlock()
	waitFor(t, "runner never idled", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.running })
	_ = idle
	count := 0
	for _, m := range s.Context() {
		if m.Role == weft.RoleUser && m.Text() == "steer me" {
			count++
		}
	}
	status := receiptStatus(s)
	t.Logf("steer receipt status = %v, steer next = %v", status, st.Next() != nil)
	if count != 1 {
		t.Fatalf("the steer reached the model %d times, want 1", count)
	}
}

func receiptStatus(s *Session) map[string]string {
	out := map[string]string{}
	for _, e := range s.Entries() {
		if r, ok := e.(ReceiptEntry); ok {
			if r.Receipt == "" {
				out[r.ID] = r.Status
			} else {
				out[r.Receipt] = r.Status
			}
		}
	}
	return out
}

// TestForkRecoversLikeOpen: a live fork and the same fork reopened
// agree on the per-model compaction overrides and the trigger's last
// measurement — Fork shares Open's recovery (post-0.7 review).
func TestForkRecoversLikeOpen(t *testing.T) {
	ctx := context.Background()
	m := wefttest.Script(wefttest.Say("hi").WithUsage(weft.Usage{InputTokens: 500, OutputTokens: 1}))
	info := weft.InfoOf(m)
	st := Memory()
	s, err := Create(ctx, st, weft.New(m))
	if err != nil {
		t.Fatal(err)
	}
	tu, err := s.Send(ctx, weft.User("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tu.Wait(); err != nil {
		t.Fatal(err)
	}
	opt := ModelWindows(map[weft.ModelInfo]int64{info: 1000})
	f, err := s.Fork(ctx, s.Leaf(), opt)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, st, f.ID(), weft.New(m), opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("live fork: window=%d lastInput=%d measured=%q; reopened: window=%d lastInput=%d measured=%q",
		f.cfg.compaction.window, f.lastInput, f.lastMeasureLeaf, r.cfg.compaction.window, r.lastInput, r.lastMeasureLeaf)
	if f.cfg.compaction.window != r.cfg.compaction.window || f.lastMeasureLeaf != r.lastMeasureLeaf {
		t.Fatal("the live fork's compaction state differs from the same fork reopened")
	}
}

// TestInterruptKeepsRunOptions: an Interrupt (or Rollback) Send's
// RunOptions reach the follow-up it queues, as the Queue policy's do
// (post-0.7 review).
func TestInterruptKeepsRunOptions(t *testing.T) {
	ctx := context.Background()
	g := &gatedModel{
		inner: wefttest.Script(wefttest.Say("one"), wefttest.Say("two")),
		gates: map[int]chan struct{}{0: make(chan struct{})},
	}
	s, err := Create(ctx, Memory(), weft.New(g))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("first"))
	if err != nil {
		t.Fatal(err)
	}
	var seen atomic.Int32
	obs := weft.OnMessages(func(context.Context, int, []weft.Message) { seen.Add(1) })
	t2, err := s.Send(ctx, weft.User("second"), As(Interrupt), RunOptions(obs))
	if err != nil {
		t.Fatal(err)
	}
	close(g.gates[0])
	_, _ = t1.Wait()
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	if seen.Load() == 0 {
		t.Fatal("the Interrupt send's RunOptions never reached its run")
	}
}

// TestRunIDNotReusedAfterReopen: the run-id sequence moves for turns
// that write no turn entry (a steer's receipt turn, an overflow
// re-run), so a reopen recovers it from the highest recorded run id,
// not the turn count. Before, a deferred steer's follow-up ran as
// <s>-t3 behind <s>-t1, and after a reopen the next Send minted <s>-t3
// again (post-0.7 review).
func TestRunIDNotReusedAfterReopen(t *testing.T) {
	ctx := context.Background()
	submit := weft.Tool("submit", "", func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	g := &gatedModel{
		inner: wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "submit"}), wefttest.Say("follow-up")),
		gates: map[int]chan struct{}{0: make(chan struct{})},
	}
	st := Memory()
	s, err := Create(ctx, st, weft.New(g, submit, weft.StopWhen(weft.HasToolCall("submit"))))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := s.Send(ctx, weft.User("first"))
	if err != nil {
		t.Fatal(err)
	}
	steer, err := s.Send(ctx, weft.User("late"), As(Steer))
	if err != nil {
		t.Fatal(err)
	}
	close(g.gates[0])
	_, _ = t1.Wait()
	_, _ = steer.Wait()
	next := steer.Next()
	if next == nil {
		t.Fatal("the steer did not defer")
	}
	if _, err := next.Wait(); err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, e := range s.Entries() {
		if te, ok := e.(TurnEntry); ok {
			used[te.RunID] = true
		}
	}
	r, err := Open(ctx, st, s.ID(), weft.New(wefttest.Script(wefttest.Say("x"))))
	if err != nil {
		t.Fatal(err)
	}
	t3, err := r.Send(ctx, weft.User("after reopen"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = t3.Wait()
	t.Logf("run ids already recorded: %v; after reopen: %s", used, t3.RunID())
	if used[t3.RunID()] {
		t.Fatalf("run id %s reused after reopen", t3.RunID())
	}
}
