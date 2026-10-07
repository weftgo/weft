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
)

// Done is the select-shaped wait, and WaitContext the bounded one:
// giving up a wait leaves the turn running, and both report its end.
func TestTurnDoneAndWaitContext(t *testing.T) {
	ctx := context.Background()
	agent, _, started, rel := heldAgent("done")
	s, _ := thread.Create(ctx, thread.Memory(), agent)
	turn, err := s.Send(ctx, core.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	select {
	case <-turn.Done():
		t.Fatal("Done is closed while the turn runs")
	default:
	}
	if got := turn.Outcome(); got != thread.TurnRunning {
		t.Errorf("Outcome of a running turn = %v", got)
	}
	short, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if res, err := turn.WaitContext(short); res != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitContext over a running turn = %v, %v; want the context's error", res, err)
	}
	// The wait was given up, not the turn.
	rel.open()
	select {
	case <-turn.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done never closed")
	}
	res, err := turn.WaitContext(short) // an ended turn answers even a dead context's wait
	if err != nil || res.Text() != "done" {
		t.Fatalf("WaitContext after the end = %v, %v", res, err)
	}
	if res2, err := turn.Wait(); err != nil || res2 != res {
		t.Errorf("Wait after WaitContext = %v, %v", res2, err)
	}
	if got := turn.Outcome(); got != thread.TurnAnswered {
		t.Errorf("Outcome = %v, want answered", got)
	}
}

// heldSummarizer is a Summarizer that blocks until released.
type heldSummarizer struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *heldSummarizer) Summarize(ctx context.Context, _ thread.SummaryInput) (thread.Summary, error) {
	h.once.Do(func() { close(h.started) })
	select {
	case <-h.release:
		return thread.Summary{Text: "the held summary"}, nil
	case <-ctx.Done():
		return thread.Summary{}, ctx.Err()
	}
}

// The between-turn compaction runs after the turn is decided: a caller
// waiting on the turn never waits on the summarizer's model call. It
// still runs before the session's next turn — a Send in that window is
// accepted under any policy, queues behind the compaction, and reads
// the compacted context.
func TestWaitReturnsBeforeTheBetweenTurnCompaction(t *testing.T) {
	ctx := context.Background()
	model := wefttest.Script(
		wefttest.Say("first").WithUsage(core.Usage{InputTokens: 95_000, OutputTokens: 5}),
		wefttest.Say("second").WithUsage(core.Usage{InputTokens: 1_000, OutputTokens: 5}),
	)
	sum := &heldSummarizer{started: make(chan struct{}), release: make(chan struct{})}
	s := compactable(t, thread.Memory(), core.New(model),
		thread.ContextWindow(100_000), thread.WithSummarizer(sum), thread.BusyPolicy(thread.Reject))
	t1, err := s.Send(ctx, core.User("one"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := t1.WaitContext(deadline(t, 5*time.Second))
	if err != nil {
		t.Fatalf("the turn's Wait hung on the between-turn compaction: %v", err)
	}
	if res.Text() != "first" {
		t.Fatalf("reply = %q", res.Text())
	}
	select {
	case <-sum.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the post-turn trigger never reached the summarizer")
	}
	if n := hasCompaction(s); n != 0 {
		t.Fatalf("%d compaction entries while the summarizer is still held", n)
	}
	// No turn is running: a Reject session accepts the Send, and it
	// waits for the compaction.
	t2, err := s.Send(ctx, core.User("two"))
	if err != nil {
		t.Fatalf("Send between turns: %v, want it accepted", err)
	}
	if err := s.CheckBusyInvariant(); err != nil {
		t.Error(err)
	}
	select {
	case <-t2.Done():
		t.Fatal("the next turn ran beside the between-turn compaction")
	case <-time.After(50 * time.Millisecond):
	}
	close(sum.release)
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := s.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	// The compaction landed before the second turn's prompt, and the
	// second request read the compacted context.
	compactionAt, promptAt := -1, -1
	for i, e := range s.Entries() {
		switch e := e.(type) {
		case thread.CompactionEntry:
			if compactionAt < 0 {
				compactionAt = i
			}
		case thread.MessageEntry:
			if e.ID == t2.ID() {
				promptAt = i
			}
		}
	}
	if compactionAt < 0 || promptAt < 0 || compactionAt > promptAt {
		t.Errorf("compaction at %d, the second prompt at %d; want the compaction first", compactionAt, promptAt)
	}
	reqs := model.Requests()
	if len(reqs) != 2 || !strings.Contains(reqs[1].Messages[0].Text(), "the held summary") {
		t.Errorf("the second turn did not read the compacted context")
	}
}

// Every way a Turn ends has a name: Outcome tells apart the ends Wait
// reports alike.
func TestTurnOutcomes(t *testing.T) {
	ctx := context.Background()
	t.Run("answered", func(t *testing.T) {
		s, _ := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script(wefttest.Say("ok"))))
		turn, _ := s.Send(ctx, core.User("q"))
		if _, err := turn.Wait(); err != nil || turn.Outcome() != thread.TurnAnswered || turn.Next() != nil {
			t.Errorf("err %v, outcome %v, next %v", err, turn.Outcome(), turn.Next())
		}
	})
	t.Run("parked", func(t *testing.T) {
		agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}))
		s, _ := thread.Create(ctx, thread.Memory(), agent)
		turn, _ := parkTurn(t, s, ctx)
		if got := turn.Outcome(); got != thread.TurnParked {
			t.Errorf("outcome = %v, want parked", got)
		}
	})
	t.Run("failed", func(t *testing.T) {
		s, _ := thread.Create(ctx, thread.Memory(), core.New(wefttest.Script(wefttest.Fail(errors.New("model down")))))
		turn, _ := s.Send(ctx, core.User("q"))
		_, err := turn.Wait()
		var runErr *core.RunError
		if !errors.As(err, &runErr) || turn.Outcome() != thread.TurnFailed {
			t.Errorf("err %v, outcome %v; want a *core.RunError and failed", err, turn.Outcome())
		}
	})
	t.Run("canceled", func(t *testing.T) {
		agent, _, started, rel := heldAgent()
		defer rel.open()
		s, _ := thread.Create(ctx, thread.Memory(), agent)
		cctx, cancel := context.WithCancel(ctx)
		turn, _ := s.Send(cctx, core.User("q"))
		<-started
		cancel()
		if _, err := turn.Wait(); !errors.Is(err, context.Canceled) || turn.Outcome() != thread.TurnCanceled {
			t.Errorf("err %v, outcome %v; want canceled", err, turn.Outcome())
		}
	})
	t.Run("interrupted", func(t *testing.T) {
		agent, _, started, rel := heldAgent("after")
		defer rel.open()
		s, _ := thread.Create(ctx, thread.Memory(), agent)
		turn, _ := s.Send(ctx, core.User("q"))
		<-started
		next, err := s.Send(ctx, core.User("stop"), thread.As(thread.Interrupt))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err == nil || turn.Outcome() != thread.TurnCanceled {
			t.Errorf("the interrupted turn: err %v, outcome %v; want canceled", err, turn.Outcome())
		}
		if _, err := next.Wait(); err != nil || next.Outcome() != thread.TurnAnswered {
			t.Errorf("the interrupting turn: err %v, outcome %v", err, next.Outcome())
		}
	})
	t.Run("delivered", func(t *testing.T) {
		s, steer, t1 := steerInto(t, wefttest.Say("done"))
		if _, err := t1.Wait(); err != nil {
			t.Fatal(err)
		}
		st := *steer
		if res, err := st.Wait(); res != nil || err != nil || st.Outcome() != thread.TurnDelivered || st.Next() != nil {
			t.Errorf("the delivered steer: %v, %v, outcome %v, next %v", res, err, st.Outcome(), st.Next())
		}
		for _, r := range receipts(s) {
			if r.Status == thread.ReceiptDelivered && r.Unanswered {
				t.Error("a steer the run answered is marked unanswered")
			}
		}
	})
	t.Run("delivered, unanswered", func(t *testing.T) {
		// The run drains the steer after its tool step and then fails
		// before the model answers it (ADR 0019 §5).
		s, steer, t1 := steerInto(t, wefttest.Fail(errors.New("model down")))
		if _, err := t1.Wait(); err == nil {
			t.Fatal("the scripted failure did not fail")
		}
		st := *steer
		if _, err := st.Wait(); err != nil || st.Outcome() != thread.TurnDelivered {
			t.Fatalf("the steer: err %v, outcome %v; want delivered", err, st.Outcome())
		}
		marked := false
		for _, r := range receipts(s) {
			if r.Status == thread.ReceiptDelivered {
				marked = r.Unanswered
			}
		}
		if !marked {
			t.Error("the delivered receipt does not say the steer went unanswered")
		}
		// The message is in the recorded transcript: the next turn's
		// model sees it.
		if n := countUser(s, "switch to metric units"); n != 1 {
			t.Errorf("the unanswered steer sits %d times in the context, want 1", n)
		}
	})
	t.Run("deferred", func(t *testing.T) {
		agent, _ := refundAgent(wefttest.ToolCalls(wefttest.Call{Name: "refund"}))
		s, _ := thread.Create(ctx, thread.Memory(), agent)
		parkTurn(t, s, ctx)
		steer, err := s.Send(ctx, core.User("also this"), thread.As(thread.Steer))
		if err != nil {
			t.Fatal(err)
		}
		if res, err := steer.Wait(); res != nil || err != nil || steer.Outcome() != thread.TurnDeferred || steer.Next() == nil {
			t.Errorf("the deferred steer: %v, %v, outcome %v, next %v", res, err, steer.Outcome(), steer.Next())
		}
		if got := steer.Next().Outcome(); got != thread.TurnRunning {
			t.Errorf("the follow-up behind the boundary: outcome %v, want running", got)
		}
	})
	t.Run("names", func(t *testing.T) {
		want := map[thread.TurnOutcome]string{
			thread.TurnRunning: "running", thread.TurnAnswered: "answered", thread.TurnParked: "parked",
			thread.TurnDelivered: "delivered", thread.TurnDeferred: "deferred", thread.TurnDropped: "dropped",
			thread.TurnFailed: "failed", thread.TurnCanceled: "canceled",
		}
		for o, name := range want {
			if o.String() != name {
				t.Errorf("%d.String() = %q, want %q", int(o), o.String(), name)
			}
		}
	})
}

// steerInto runs a turn whose first step calls a tool, steers a message
// into it while the tool runs, and returns the session, the steer's
// Turn (set once the steer is sent) and the running turn. after is the
// model's turn once the steer is delivered.
func steerInto(t *testing.T, after wefttest.Turn) (*thread.Session, **thread.Turn, *thread.Turn) {
	t.Helper()
	ctx := context.Background()
	echo := core.Tool("echo", "", func(context.Context, struct{}) (string, error) { return "ok", nil })
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "echo"}), after)
	var ref *thread.Session
	var once sync.Once
	steer := new(*thread.Turn)
	agent := core.New(model, echo, core.Tap(func(_ context.Context, ev core.Event) {
		if _, ok := ev.(core.ToolStart); ok {
			once.Do(func() {
				st, err := ref.Send(ctx, core.User("switch to metric units"))
				if err != nil {
					t.Errorf("steer Send: %v", err)
				}
				*steer = st
			})
		}
	}))
	s, err := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Steer))
	if err != nil {
		t.Fatal(err)
	}
	ref = s
	// The first Send finds an idle session: under the Steer policy it
	// runs as a plain turn.
	t1, err := s.Send(ctx, core.User("convert this"))
	if err != nil {
		t.Fatal(err)
	}
	return s, steer, t1
}

// A turn records the busy policy its Send was called under — the
// session's, or the Send's own — and a resume records none.
func TestTurnEntryRecordsThePolicy(t *testing.T) {
	ctx := context.Background()
	for p, name := range map[thread.Policy]string{
		thread.Queue: "queue", thread.Reject: "reject", thread.Steer: "steer",
		thread.Interrupt: "interrupt", thread.Rollback: "rollback",
	} {
		if p.String() != name {
			t.Errorf("Policy(%d).String() = %q, want %q", int(p), p.String(), name)
		}
	}
	agent, _ := refundAgent(
		wefttest.Say("plain"),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "call_r"}),
		wefttest.Say("resumed"),
		wefttest.Say("after the interrupt"),
	)
	s, _ := thread.Create(ctx, thread.Memory(), agent, thread.BusyPolicy(thread.Reject))
	t1, _ := s.Send(ctx, core.User("one")) // the session's policy
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	t2, _ := s.Send(ctx, core.User("refund it"), thread.As(thread.Queue)) // the Send's own
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	t3, err := s.Send(ctx, core.User("never mind"), thread.As(thread.Interrupt)) // denies the boundary
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t3.Wait(); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, te := range turnEntries(s) {
		got = append(got, te.Policy)
	}
	// the plain turn, the parked turn, the resume the interrupt's
	// denial armed, the interrupting send's own turn
	if want := []string{"reject", "queue", "", "interrupt"}; !equalStrings(got, want) {
		t.Errorf("recorded policies = %q, want %q", got, want)
	}
}

// A turn that ran out of time is a canceled turn, like one whose
// caller walked away: the entry says so.
func TestTurnEntryCanceledOnDeadline(t *testing.T) {
	ctx := context.Background()
	agent, _, started, rel := heldAgent()
	defer rel.open()
	s, _ := thread.Create(ctx, thread.Memory(), agent)
	dctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	turn, err := s.Send(dctx, core.User("too slow"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := turn.Wait(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait = %v, want the deadline", err)
	}
	if got := turn.Outcome(); got != thread.TurnCanceled {
		t.Errorf("Outcome = %v, want canceled", got)
	}
	tes := turnEntries(s)
	if len(tes) != 1 || !tes[0].Canceled || !strings.Contains(tes[0].Err, "deadline") {
		t.Errorf("turn entries = %+v, want one canceled by its deadline", tes)
	}
}

// The interrupted turn's partial is completed on a copy: the error the
// core handed its own observers keeps the transcript the run made,
// while the turn's — and the tree's — carries the interruption text.
func TestInterruptCompletesACopyOfThePartial(t *testing.T) {
	ctx := context.Background()
	golden := "tool call wait was interrupted: the run was canceled for a newer message"
	hasGolden := func(msgs []core.Message) bool {
		for _, m := range msgs {
			for _, p := range m.Content {
				if r, ok := p.(core.ToolResultPart); ok && r.Content == golden {
					return true
				}
			}
		}
		return false
	}
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "wait"}), wefttest.Say("after"))
	tool, rel := blockingTool()
	defer rel.open()
	started := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var observed []*core.RunResult
	agent := core.New(model, tool,
		core.Tap(func(_ context.Context, ev core.Event) {
			if _, ok := ev.(core.ToolStart); ok {
				once.Do(func() { close(started) })
			}
		}),
		core.OnRunEnd(func(_ context.Context, _ *core.RunResult, err error) {
			var runErr *core.RunError
			if errors.As(err, &runErr) {
				mu.Lock()
				observed = append(observed, runErr.Result)
				mu.Unlock()
			}
		}),
	)
	s, _ := thread.Create(ctx, thread.Memory(), agent)
	turn, _ := s.Send(ctx, core.User("go"))
	<-started
	next, err := s.Send(ctx, core.User("stop"), thread.As(thread.Interrupt))
	if err != nil {
		t.Fatal(err)
	}
	_, werr := turn.Wait()
	var runErr *core.RunError
	if !errors.As(werr, &runErr) || !hasGolden(runErr.Result.Messages) {
		t.Fatalf("the turn's error = %v; want a *core.RunError whose partial carries the interruption text", werr)
	}
	if _, err := next.Wait(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 1 {
		t.Fatalf("OnRunEnd saw %d failed runs, want 1", len(observed))
	}
	if hasGolden(observed[0].Messages) {
		t.Error("the run's own result was rewritten in place: its observer reads the interruption text the session added")
	}
	if observed[0] == runErr.Result {
		t.Error("the turn's error shares the run's Result")
	}
}

// The approval trail holds approval entries and nothing else; how a
// resume ended is its own audit step, written with the resume's turn
// entry and naming its run.
func TestAuditRecordsHowAResumeEnded(t *testing.T) {
	ctx := context.Background()
	agent, _ := refundAgent(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "call_a"}),
		wefttest.Say("refunded"),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "call_b"}),
		wefttest.Fail(errors.New("model down")),
	)
	s, _ := thread.Create(ctx, thread.Memory(), agent)
	steps := func() (out []thread.ApprovalAuditEntry) {
		for _, e := range s.Audit() {
			switch e := e.(type) {
			case thread.ApprovalAuditEntry:
				if e.Step == thread.StepResume {
					out = append(out, e)
				}
			case thread.TurnEntry:
				t.Fatalf("Audit returned a turn entry: %+v", e)
			}
		}
		return out
	}
	parkTurn(t, s, ctx)
	rt, err := s.Decide(ctx, thread.Approve("call_a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	got := steps()
	if len(got) != 2 || got[0].Outcome != "started" || got[1].Outcome != "completed" ||
		got[0].RunID != rt.RunID() || got[1].RunID != rt.RunID() || got[1].Detail != "" {
		t.Fatalf("resume steps = %+v, want started and completed under %q", got, rt.RunID())
	}
	// The completed step lands with the resume's turn entry: one append.
	path, _ := s.Path(got[1].ID)
	if te, ok := path[len(path)-2].(thread.TurnEntry); !ok || te.RunID != rt.RunID() {
		t.Errorf("the completed step does not follow the resume's turn entry")
	}

	// A resume whose run fails says failed, with the error.
	parkTurn(t, s, ctx)
	rt2, err := s.Decide(ctx, thread.Approve("call_b"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt2.Wait(); err == nil {
		t.Fatal("the scripted failure did not fail")
	}
	got = steps()
	if last := got[len(got)-1]; len(got) != 4 || last.Outcome != "failed" || last.RunID != rt2.RunID() ||
		!strings.Contains(last.Detail, "model down") {
		t.Fatalf("resume steps = %+v, want the second resume failed with its error", got)
	}
}
