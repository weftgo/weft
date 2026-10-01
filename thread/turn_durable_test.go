package thread_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// runIDsOf lists every run id the turn entries record, in order.
func runIDsOf(s *thread.Session) []string {
	var out []string
	for _, te := range turnEntries(s) {
		out = append(out, te.RunID)
	}
	return out
}

// closeNow closes the session without waiting for its work: the first
// Close gives up at once — the running turn is canceled, queued turns
// end with ErrClosed — and the second seals and releases. What a
// process that stops mid-work leaves behind, done in order.
func closeNow(t *testing.T, s *thread.Session) {
	t.Helper()
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Close(gone); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Close (giving up): %v", err)
	}
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// The run-id counter recovers from the highest id the tree records,
// not from a count of turn entries: a steer spends an id without ever
// writing a turn entry, so a count falls behind and a reopened session
// would mint an id that is already in the run store.
func TestRunIDRecoversPastIDsThatLeftNoTurnEntry(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		echo := weft.Tool("echo", "", func(context.Context, struct{}) (string, error) { return "ok", nil })
		model := wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
			wefttest.Say("steered"),
			wefttest.Say("second"),
			wefttest.Say("after the reopen"),
		)
		var ref *thread.Session
		var once sync.Once
		agent := weft.New(model, echo, weft.Tap(func(_ context.Context, ev weft.Event) {
			if _, ok := ev.(weft.ToolStart); ok {
				once.Do(func() {
					if _, err := ref.Send(ctx, weft.User("a steer"), thread.As(thread.Steer)); err != nil {
						t.Errorf("steer: %v", err)
					}
				})
			}
		}))
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatal(err)
		}
		ref = s
		t1, _ := s.Send(ctx, weft.User("one")) // -t1; the steer spends -t2
		if _, err := t1.Wait(); err != nil {
			t.Fatal(err)
		}
		t2, _ := s.Send(ctx, weft.User("two"))
		if _, err := t2.Wait(); err != nil {
			t.Fatal(err)
		}
		if t2.RunID() != s.ID()+"-t3" {
			t.Fatalf("the second turn ran as %q, want -t3 (the steer spent -t2)", t2.RunID())
		}
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
		s2 := reopenWith(t, ctx, st, s, agent)
		t3, err := s2.Send(ctx, weft.User("three"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := t3.Wait(); err != nil {
			t.Fatal(err)
		}
		if t3.RunID() != s.ID()+"-t4" {
			t.Errorf("run id after the reopen = %q, want -t4: -t3 is already a turn's", t3.RunID())
		}
		seen := map[string]bool{}
		for _, id := range runIDsOf(s2) {
			if seen[id] {
				t.Errorf("run id %q names two turn entries", id)
			}
			seen[id] = true
		}
	})
}

// A turn whose end never landed still spent its run id: the prompt
// entry carries it, written before the run started, so the reopened
// session's next turn takes the next one.
func TestRunIDRecoversAfterALostTurnEnd(t *testing.T) {
	ctx := context.Background()
	st := &failTurnEndStorage{Storage: thread.Memory(), fail: true}
	agent := weft.New(wefttest.Script(wefttest.Say("lost"), wefttest.Say("kept")))
	s, err := thread.Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	t1, _ := s.Send(ctx, weft.User("one"))
	if _, err := t1.Wait(); !errors.Is(err, thread.ErrNotPersisted) {
		t.Fatalf("Wait = %v, want ErrNotPersisted", err)
	}
	if n := len(turnEntries(s)); n != 0 {
		t.Fatalf("%d turn entries after a refused end", n)
	}
	var prompt thread.MessageEntry
	for _, e := range s.Entries() {
		if me, ok := e.(thread.MessageEntry); ok && me.ID == t1.ID() {
			prompt = me
		}
	}
	if prompt.RunID != t1.RunID() {
		t.Errorf("the prompt entry records run %q, want %q", prompt.RunID, t1.RunID())
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	s2 := reopenWith(t, ctx, st.Storage, s, agent)
	t2, err := s2.Send(ctx, weft.User("two"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t2.Wait(); err != nil {
		t.Fatal(err)
	}
	if t2.RunID() != s.ID()+"-t2" {
		t.Errorf("run id after the reopen = %q, want -t2: -t1 ran, though its end never landed", t2.RunID())
	}
}

// The overflow re-run's id is on the record before the re-run starts —
// on the failed attempt's turn entry — and a reopen continues past it.
func TestRunIDRecoversAfterAnOverflowReRun(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script(
			wefttest.Say("the first answer"),
			wefttest.Fail(weft.ErrContextOverflow),
			wefttest.Say("the summary of what came before"),
			wefttest.Say("recovered after compaction"),
			wefttest.Say("after the reopen"),
		))
		s, err := thread.Create(ctx, st, agent, thread.KeepRecent(1))
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"a first question", "a prompt that overflows"} {
			turn, _ := s.Send(ctx, weft.User(text))
			if _, err := turn.Wait(); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
		s2 := reopenWith(t, ctx, st, s, agent, thread.KeepRecent(1))
		turn, err := s2.Send(ctx, weft.User("and now"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Fatal(err)
		}
		want := []string{s.ID() + "-t1", s.ID() + "-t2", s.ID() + "-t3", s.ID() + "-t4"}
		if got := runIDsOf(s2); !reflect.DeepEqual(got, want) {
			t.Errorf("run ids = %v, want %v", got, want)
		}
	})
}

// A prompt that was written but could not be flushed fails the Send
// and leaves the leaf where it was: the retry writes its own prompt
// beside the failed one, and the context — live and reopened — holds
// the message once.
func TestPromptFlushFailureLeavesNoOrphanPrompt(t *testing.T) {
	for _, withHistory := range []bool{false, true} {
		name := "first_entry"
		if withHistory {
			name = "after_a_turn"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st := &flushFailStorage{Storage: thread.Memory()}
			agent := weft.New(wefttest.Script(wefttest.Say("earlier"), wefttest.Say("answered")))
			s, err := thread.Create(ctx, st, agent)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"hello", "answered"}
			if withHistory {
				t0, _ := s.Send(ctx, weft.User("before"))
				if _, err := t0.Wait(); err != nil {
					t.Fatal(err)
				}
				want = []string{"before", "earlier", "hello", "answered"}
			} else {
				agent = weft.New(wefttest.Script(wefttest.Say("answered")))
				if s, err = thread.Create(ctx, st, agent); err != nil {
					t.Fatal(err)
				}
			}
			leaf := s.Leaf()
			st.failNext.Store(1)
			if turn, err := s.Send(ctx, weft.User("hello")); err == nil || turn != nil {
				t.Fatalf("Send over a failing flush = %v, %v; want an error", turn, err)
			}
			if got := s.Leaf(); got != leaf {
				t.Errorf("the leaf after the failed Send = %q, want it back at %q", got, leaf)
			}
			turn, err := s.Send(ctx, weft.User("hello"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := turn.Wait(); err != nil {
				t.Fatal(err)
			}
			if got := contextTexts(s); !equalStrings(got, want) {
				t.Errorf("Context = %v, want %v", got, want)
			}
			if err := s.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if got := contextTexts(reopenWith(t, ctx, st.Storage, s, agent)); !equalStrings(got, want) {
				t.Errorf("the reopened Context = %v, want %v", got, want)
			}
		})
	}
}

// A failed Send with no retry reads the same after a reopen: the leaf
// move is an entry, so the unflushed prompt is off the path there too.
func TestPromptFlushFailureIsRecordedForAReopen(t *testing.T) {
	ctx := context.Background()
	st := &flushFailStorage{Storage: thread.Memory()}
	agent := weft.New(wefttest.Script(wefttest.Say("first")))
	s, _ := thread.Create(ctx, st, agent)
	t0, _ := s.Send(ctx, weft.User("before"))
	if _, err := t0.Wait(); err != nil {
		t.Fatal(err)
	}
	st.failNext.Store(1)
	if _, err := s.Send(ctx, weft.User("lost")); err == nil {
		t.Fatal("Send over a failing flush succeeded")
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	s2 := reopenWith(t, ctx, st.Storage, s, agent)
	if got, want := contextTexts(s2), []string{"before", "first"}; !equalStrings(got, want) {
		t.Errorf("the reopened Context = %v, want %v", got, want)
	}
}

// heldAgent returns an agent whose first turn blocks in a tool until
// released, then answers; every later turn answers at once.
func heldAgent(replies ...string) (*weft.Agent, *wefttest.Model, chan struct{}, *release) {
	steps := []wefttest.Turn{wefttest.ToolCalls(wefttest.Call{Name: "wait"})}
	for _, r := range replies {
		steps = append(steps, wefttest.Say(r))
	}
	model := wefttest.Script(steps...)
	tool, rel := blockingTool()
	started := make(chan struct{})
	var once sync.Once
	agent := weft.New(model, tool, weft.Tap(func(_ context.Context, ev weft.Event) {
		if _, ok := ev.(weft.ToolStart); ok {
			once.Do(func() { close(started) })
		}
	}))
	return agent, model, started, rel
}

// A queued Send is durable from the moment it is accepted (ADR 0011
// §4): an accepted receipt carries the message and the id its prompt
// entry will take. A writer that stops before the send's turn starts
// loses nothing — the reopened session lists it in its queue, runs
// nothing on its own, and Continue runs it under the same receipt id
// and run id.
func TestQueuedSendSurvivesARestart(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent, _, started, rel := heldAgent("never reached")
		defer rel.open()
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Send(ctx, weft.User("long work")); err != nil {
			t.Fatal(err)
		}
		<-started
		queued, err := s.Send(ctx, weft.User("and after that, this"))
		if err != nil {
			t.Fatal(err)
		}
		// The receipt is in the tree already; no entry has the turn's id
		// yet — the receipt names it.
		var accepted thread.ReceiptEntry
		for _, r := range receipts(s) {
			if r.Status == thread.ReceiptAccepted {
				accepted = r
			}
		}
		if accepted.Turn != queued.ID() || accepted.RunID != queued.RunID() || accepted.Msg == nil ||
			accepted.Msg.Text() != "and after that, this" {
			t.Fatalf("accepted receipt = %+v, want the queued turn's id %q, run %q and message", accepted, queued.ID(), queued.RunID())
		}
		if q := s.Queue(); len(q) != 1 || q[0].Receipt != queued.ID() || q[0].Policy != thread.Queue || q[0].Msg.Text() != "and after that, this" {
			t.Fatalf("Queue = %+v, want the queued send", q)
		}
		// The writer stops before the queued turn starts.
		closeNow(t, s)
		if _, err := queued.Wait(); !errors.Is(err, thread.ErrClosed) {
			t.Fatalf("the queued turn of a closed session: %v, want ErrClosed", err)
		}

		model2 := wefttest.Script(wefttest.Say("picked up after the restart"))
		s2 := reopenWith(t, ctx, st, s, weft.New(model2))
		q := s2.Queue()
		if len(q) != 1 || q[0].Receipt != queued.ID() || q[0].Policy != thread.Queue || q[0].Msg.Text() != "and after that, this" {
			t.Fatalf("the reopened Queue = %+v, want the send restored under its id", q)
		}
		if n := len(model2.Requests()); n != 0 {
			t.Fatalf("Open ran %d model calls", n)
		}
		turn, err := s2.Continue(ctx)
		if err != nil || turn == nil {
			t.Fatalf("Continue = %v, %v", turn, err)
		}
		res, err := turn.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if res.Text() != "picked up after the restart" {
			t.Errorf("reply = %q", res.Text())
		}
		if turn.ID() != queued.ID() || turn.RunID() != queued.RunID() {
			t.Errorf("the restored turn is %q / %q, want the accepted %q / %q", turn.ID(), turn.RunID(), queued.ID(), queued.RunID())
		}
		// Its prompt entry took the id the receipt promised, and the
		// queue is empty — here and after another reopen.
		found := false
		for _, e := range s2.Entries() {
			if me, ok := e.(thread.MessageEntry); ok && me.ID == queued.ID() {
				found = me.Message.Text() == "and after that, this"
			}
		}
		if !found {
			t.Error("the restored send's prompt entry does not carry the receipt's id")
		}
		if err := s2.Close(ctx); err != nil {
			t.Fatal(err)
		}
		s3 := reopen(t, ctx, st, s)
		if q := s3.Queue(); len(q) != 0 {
			t.Errorf("Queue after the send ran = %+v, want empty", q)
		}
		if n := countUser(s3, "and after that, this"); n != 1 {
			t.Errorf("the queued message sits %d times in the context, want 1", n)
		}
	})
}

// A restored queued send runs ahead of the next Send: acceptance order
// holds across the restart.
func TestRestoredQueuedSendRunsBeforeTheNextSend(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	agent, _, started, rel := heldAgent()
	defer rel.open()
	s, _ := thread.Create(ctx, st, agent)
	if _, err := s.Send(ctx, weft.User("long work")); err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := s.Send(ctx, weft.User("queued before the restart")); err != nil {
		t.Fatal(err)
	}
	closeNow(t, s)

	model2 := wefttest.Script(wefttest.Say("first"), wefttest.Say("second"))
	s2 := reopenWith(t, ctx, st, s, weft.New(model2))
	turn, err := s2.Send(ctx, weft.User("sent after the restart"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := turn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "second" {
		t.Errorf("the new send's reply = %q, want it to run second", res.Text())
	}
	var users []string
	for _, m := range s2.Context() {
		if m.Role == weft.RoleUser && m.Text() != "" {
			users = append(users, m.Text())
		}
	}
	if len(users) < 2 || users[len(users)-2] != "queued before the restart" || users[len(users)-1] != "sent after the restart" {
		t.Errorf("user messages = %v, want the restored send before the new one", users)
	}
}

// ClearQueue drops queued sends as it drops queued steers: one dropped
// receipt each, the Turn ending with ErrDropped, and nothing restored
// by a reopen.
func TestClearQueueDropsQueuedSends(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent, model, started, rel := heldAgent("done")
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatal(err)
		}
		t1, _ := s.Send(ctx, weft.User("long work"))
		<-started
		queued, err := s.Send(ctx, weft.User("on second thought"))
		if err != nil {
			t.Fatal(err)
		}
		steer, err := s.Send(ctx, weft.User("a steer too"), thread.As(thread.Steer))
		if err != nil {
			t.Fatal(err)
		}
		q := s.Queue()
		if len(q) != 2 || q[0].Policy != thread.Steer || q[1].Policy != thread.Queue {
			t.Fatalf("Queue = %+v, want the steer then the send", q)
		}
		n, err := s.ClearQueue(ctx)
		if err != nil || n != 2 {
			t.Fatalf("ClearQueue = %d, %v; want 2", n, err)
		}
		for name, turn := range map[string]*thread.Turn{"send": queued, "steer": steer} {
			if res, err := turn.Wait(); res != nil || !errors.Is(err, thread.ErrDropped) {
				t.Errorf("the dropped %s: %v, %v; want ErrDropped", name, res, err)
			}
			if got := turn.Outcome(); got != thread.TurnDropped {
				t.Errorf("the dropped %s's Outcome = %v", name, got)
			}
		}
		rel.open()
		if _, err := t1.Wait(); err != nil {
			t.Fatal(err)
		}
		if err := s.WaitIdle(ctx); err != nil {
			t.Fatal(err)
		}
		if got := len(model.Requests()); got != 2 {
			t.Errorf("%d model calls, want 2: the dropped messages never ran", got)
		}
		dropped := 0
		for _, r := range receipts(s) {
			if r.Status == thread.ReceiptDropped {
				dropped++
			}
		}
		if dropped != 2 {
			t.Errorf("%d dropped receipts, want 2", dropped)
		}
		if err := s.Close(ctx); err != nil {
			t.Fatal(err)
		}
		s2 := reopen(t, ctx, st, s)
		if q := s2.Queue(); len(q) != 0 {
			t.Errorf("the reopened Queue = %+v, want nothing restored", q)
		}
		for _, text := range []string{"on second thought", "a steer too"} {
			if strings.Contains(strings.Join(contextTexts(s2), "\n"), text) {
				t.Errorf("dropped message %q reached the context", text)
			}
		}
	})
}

// A deferred steer's follow-up is a queued send like any other: it is
// durable from the deferral, and a reopen restores it.
func TestDeferredSteerFollowUpSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	agent := weft.New(
		wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "dangerous", ID: "call_d"})),
		weft.Tool("dangerous", "", func(context.Context, struct{}) (string, error) { return "ran", nil }, weft.RequireApproval()),
	)
	s, _ := thread.Create(ctx, st, agent)
	t1, _ := s.Send(ctx, weft.User("do it"))
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	// A steer meeting the open boundary defers at once.
	steer, err := s.Send(ctx, weft.User("also: be careful"), thread.As(thread.Steer))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := steer.Wait(); err != nil || steer.Outcome() != thread.TurnDeferred || steer.Next() == nil {
		t.Fatalf("the steer: err %v, outcome %v, next %v; want deferred with a follow-up", err, steer.Outcome(), steer.Next())
	}
	followUp := steer.Next()
	closeNow(t, s)

	model2 := wefttest.Script(wefttest.Say("denied, moving on"), wefttest.Say("careful it is"))
	agent2 := weft.New(model2,
		weft.Tool("dangerous", "", func(context.Context, struct{}) (string, error) { return "ran", nil }, weft.RequireApproval()))
	s2 := reopenWith(t, ctx, st, s, agent2)
	q := s2.Queue()
	if len(q) != 1 || q[0].Receipt != followUp.ID() || q[0].Msg.Text() != "also: be careful" {
		t.Fatalf("the reopened Queue = %+v, want the steer's follow-up %q", q, followUp.ID())
	}
	rt, err := s2.Decide(ctx, thread.Deny("call_d", "no"))
	if err != nil || rt == nil {
		t.Fatalf("Decide = %v, %v", rt, err)
	}
	if _, err := rt.Wait(); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the restored follow-up never ran", func() bool { return steerInContext(s2, "also: be careful") })
	if err := s2.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countUser(s2, "also: be careful"); n != 1 {
		t.Errorf("the steered message sits %d times in the context, want 1", n)
	}
}

// A queued send whose context ended before its turn started did not
// run: the prompt is kept, the turn is recorded canceled, and Wait
// says both things — ErrNotRun and the context's error.
func TestQueuedSendCanceledBeforeItStarts(t *testing.T) {
	ctx := context.Background()
	agent, _, started, rel := heldAgent("done")
	s, _ := thread.Create(ctx, thread.Memory(), agent)
	t1, _ := s.Send(ctx, weft.User("long work"))
	<-started
	qctx, cancel := context.WithCancel(ctx)
	queued, err := s.Send(qctx, weft.User("never mind"))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	rel.open()
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	res, err := queued.WaitContext(deadline(t, 5*time.Second))
	if res != nil || !errors.Is(err, thread.ErrNotRun) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait = %v, %v; want ErrNotRun wrapping context.Canceled", res, err)
	}
	if got := queued.Outcome(); got != thread.TurnCanceled {
		t.Errorf("Outcome = %v, want canceled", got)
	}
	var runErr *weft.RunError
	if errors.As(err, &runErr) {
		t.Error("a turn that never ran reports a *weft.RunError")
	}
	tes := turnEntries(s)
	if last := tes[len(tes)-1]; !last.Canceled || last.RunID != queued.RunID() {
		t.Errorf("the turn entry = %+v, want it canceled under %q", last, queued.RunID())
	}
}

// deadline returns a context that ends after d — the tests' bound on a
// wait that must not hang.
func deadline(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

// A fork does not inherit its origin's queue: a queued send's accepted
// receipt may sit on the path the fork copies, but the send is the
// origin's to run — a reopened fork restores nothing.
func TestForkDoesNotInheritQueuedSends(t *testing.T) {
	ctx := context.Background()
	st := thread.Memory()
	agent, _, started, rel := heldAgent("done", "the queued send's reply")
	s, _ := thread.Create(ctx, st, agent)
	t1, _ := s.Send(ctx, weft.User("long work"))
	<-started
	queued, err := s.Send(ctx, weft.User("queued on the origin"))
	if err != nil {
		t.Fatal(err)
	}
	// Fork works mid-turn: the copied path ends at the accepted receipt.
	f, err := s.Fork(ctx, s.Leaf())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(ctx); err != nil {
		t.Fatal(err)
	}
	forked, err := thread.Open(ctx, st, f.ID(), weft.New(wefttest.Script()))
	if err != nil {
		t.Fatal(err)
	}
	if q := forked.Queue(); len(q) != 0 {
		t.Errorf("the reopened fork's Queue = %+v, want empty: the send is the origin's", q)
	}
	// The origin still runs it.
	rel.open()
	if _, err := t1.Wait(); err != nil {
		t.Fatal(err)
	}
	if res, err := queued.Wait(); err != nil || res.Text() != "the queued send's reply" {
		t.Fatalf("the origin's queued send: %v, %v", res, err)
	}
}
