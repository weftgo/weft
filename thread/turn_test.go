package thread_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// blockingTool returns a tool that blocks until release is closed (or
// ctx dies), for the busy-policy and cancellation rows.
func blockingTool() (*weft.ToolDef, *release) {
	r := &release{ch: make(chan struct{})}
	return weft.Tool("wait", "blocks until released", func(ctx context.Context, in struct{}) (string, error) {
		select {
		case <-r.ch:
			return "released", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}), r
}

type release struct {
	ch   chan struct{}
	once sync.Once
}

func (r *release) open() { r.once.Do(func() { close(r.ch) }) }

func TestSendMultiTurn(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script(wefttest.Say("first reply"), wefttest.Say("second reply")))
		s, _ := thread.Create(ctx, st, agent)

		t1, err := s.Send(ctx, weft.User("question one"))
		if err != nil {
			t.Fatalf("Send 1: %v", err)
		}
		res1, err := t1.Wait()
		if err != nil {
			t.Fatalf("Wait 1: %v", err)
		}
		if res1.Text() != "first reply" {
			t.Errorf("reply 1 = %q", res1.Text())
		}
		t2, err := s.Send(ctx, weft.User("question two"))
		if err != nil {
			t.Fatalf("Send 2: %v", err)
		}
		if _, err := t2.Wait(); err != nil {
			t.Fatalf("Wait 2: %v", err)
		}

		// The receipt is the prompt entry; the run id names the run.
		open := reopen(t, ctx, st, s)
		ids := map[string]bool{}
		for _, e := range open.Entries() {
			switch e := e.(type) {
			case thread.MessageEntry:
				ids[e.ID] = true
			}
		}
		if !ids[t1.ID()] || !ids[t2.ID()] {
			t.Errorf("receipts not in the tree: %q %q", t1.ID(), t2.ID())
		}
		if t1.RunID() != s.ID()+"-t1" || t2.RunID() != s.ID()+"-t2" {
			t.Errorf("run ids = %q, %q; want -t1, -t2", t1.RunID(), t2.RunID())
		}
		got := contextTexts(open)
		want := []string{"question one", "first reply", "question two", "second reply"}
		if !equalStrings(got, want) {
			t.Errorf("Context = %v, want %v", got, want)
		}
		// Two turn entries with the ledger fields, and the usage adds
		// up: two scripted steps of 10 input / 5 output each.
		turns := 0
		for _, e := range open.Entries() {
			if te, ok := e.(thread.TurnEntry); ok {
				turns++
				if te.RunID != s.ID()+"-t"+string(rune('0'+turns)) {
					t.Errorf("turn entry run id = %q", te.RunID)
				}
				if te.StopReason != weft.StopEndTurn {
					t.Errorf("turn entry stop = %q", te.StopReason)
				}
				if te.Usage.InputTokens != 10 {
					t.Errorf("turn entry usage = %+v", te.Usage)
				}
			}
		}
		if turns != 2 {
			t.Errorf("turn entries = %d, want 2", turns)
		}
		if u := open.Usage(); u.Turns.InputTokens != 20 || u.Turns.OutputTokens != 10 {
			t.Errorf("Usage.Turns = %+v", u.Turns)
		}
	})
}

func TestSendFailureKeepsPartial(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		boom := errors.New("boom")
		agent := weft.New(
			wefttest.Script(
				wefttest.ToolCalls(wefttest.Call{Name: "lookup"}),
				wefttest.Fail(boom),
			),
			weft.Tool("lookup", "finds an order", func(ctx context.Context, in struct{}) (string, error) {
				return "order 1234 shipped", nil
			}),
		)
		s, _ := thread.Create(ctx, st, agent)
		t1, err := s.Send(ctx, weft.User("where is order 1234?"))
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		res, err := t1.Wait()
		if err == nil {
			t.Fatal("failed script: no error from Wait")
		}
		var runErr *weft.RunError
		if !errors.As(err, &runErr) {
			t.Fatalf("Wait err = %T (%v), want *weft.RunError", err, err)
		}
		if res != nil {
			t.Error("Wait returned a result with the error")
		}

		// The partial transcript — the assistant's call and the tool's
		// answer — is kept, and the turn entry names the failure.
		open := reopen(t, ctx, st, s)
		msgs := open.Context()
		if len(msgs) != 3 {
			t.Fatalf("Context = %d messages, want 3 (prompt, call, result)", len(msgs))
		}
		if msgs[1].Role != weft.RoleAssistant || msgs[2].Role != weft.RoleTool {
			t.Errorf("partial roles = %q, %q", msgs[1].Role, msgs[2].Role)
		}
		found := false
		for _, e := range open.Entries() {
			if te, ok := e.(thread.TurnEntry); ok {
				found = true
				if te.Err == "" || !contains(te.Err, "boom") {
					t.Errorf("turn entry err = %q, want it to name boom", te.Err)
				}
				if te.Canceled {
					t.Error("turn entry canceled on a model failure")
				}
				if te.Steps != 1 {
					t.Errorf("turn entry steps = %d, want 1", te.Steps)
				}
			}
		}
		if !found {
			t.Error("no turn entry for the failed turn")
		}
	})
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestSendCancel(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		tool, _ := blockingTool()
		agent := weft.New(
			wefttest.Script(
				wefttest.ToolCalls(wefttest.Call{Name: "wait"}),
				wefttest.Say("never reached"),
			),
			tool,
		)
		s, _ := thread.Create(ctx, st, agent)

		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		t1, err := s.Send(runCtx, weft.User("go wait"))
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		// Cancel mid-run: at the tool's start, while it blocks.
		for ev, err := range t1.Events() {
			if err != nil {
				break
			}
			if _, ok := ev.(weft.ToolStart); ok {
				cancel()
			}
		}
		if _, err := t1.Wait(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Wait err = %v, want context.Canceled", err)
		}

		open := reopen(t, ctx, st, s)
		var te *thread.TurnEntry
		for _, e := range open.Entries() {
			if x, ok := e.(thread.TurnEntry); ok {
				te = &x
			}
		}
		if te == nil {
			t.Fatal("no turn entry for the canceled turn")
		}
		if !te.Canceled {
			t.Error("turn entry not marked canceled")
		}
		// The prompt survived, and the partial transcript (the call,
		// repaired) is kept.
		if got := contextTexts(open); len(got) < 1 || got[0] != "go wait" {
			t.Errorf("Context = %v, want the prompt kept", got)
		}
	})
}

func TestSendQueueOrdering(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script(
			wefttest.Say("r"), wefttest.Say("r"), wefttest.Say("r"),
			wefttest.Say("r"), wefttest.Say("r"),
		))
		s, _ := thread.Create(ctx, st, agent)

		const n = 5
		var wg sync.WaitGroup
		turns := make([]*thread.Turn, n)
		var mu sync.Mutex
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				turn, err := s.Send(ctx, weft.User("follow-up"))
				if err != nil {
					t.Errorf("Send %d: %v", i, err)
					return
				}
				mu.Lock()
				turns[i] = turn
				mu.Unlock()
				if _, err := turn.Wait(); err != nil {
					t.Errorf("Wait %d: %v", i, err)
				}
			}(i)
		}
		wg.Wait()

		// Whatever order the sends were accepted in, the final context
		// is strictly paired — a prompt, its reply, a prompt, its
		// reply — and the run ids are the five distinct turn numbers.
		open := reopen(t, ctx, st, s)
		msgs := open.Context()
		if len(msgs) != 2*n {
			t.Fatalf("Context = %d messages, want %d", len(msgs), 2*n)
		}
		for i, m := range msgs {
			wantRole := weft.RoleUser
			if i%2 == 1 {
				wantRole = weft.RoleAssistant
			}
			if m.Role != wantRole {
				t.Errorf("message %d role = %q, want %q (prompts and replies must not interleave)", i, m.Role, wantRole)
			}
		}
		seen := map[string]bool{}
		for _, e := range open.Entries() {
			if te, ok := e.(thread.TurnEntry); ok {
				if seen[te.RunID] {
					t.Errorf("duplicate run id %q", te.RunID)
				}
				seen[te.RunID] = true
			}
		}
		if len(seen) != n {
			t.Errorf("distinct run ids = %d, want %d", len(seen), n)
		}
		mu.Lock()
		defer mu.Unlock()
		for i, turn := range turns {
			if turn != nil && (turn.RunID() == "" || turn.ID() == "") {
				t.Errorf("turn %d missing ids", i)
			}
		}
	})
}

func TestSendReject(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		tool, rel := blockingTool()
		agent := weft.New(
			wefttest.Script(
				wefttest.ToolCalls(wefttest.Call{Name: "wait"}),
				wefttest.Say("done waiting"),
				wefttest.Say("after the wait"),
			),
			tool,
		)
		s, _ := thread.Create(ctx, st, agent, thread.BusyPolicy(thread.Reject))

		t1, err := s.Send(ctx, weft.User("first"))
		if err != nil {
			t.Fatalf("Send 1: %v", err)
		}
		if _, err := s.Send(ctx, weft.User("second")); !errors.Is(err, thread.ErrBusy) {
			t.Fatalf("Send while busy: err = %v, want ErrBusy", err)
		}
		rel.open()
		if _, err := t1.Wait(); err != nil {
			t.Fatalf("Wait 1: %v", err)
		}
		// The runner slot is free again.
		t2, err := s.Send(ctx, weft.User("third"))
		if err != nil {
			t.Fatalf("Send after idle: %v", err)
		}
		if _, err := t2.Wait(); err != nil {
			t.Fatalf("Wait 2: %v", err)
		}
		open := reopen(t, ctx, st, s)
		if got := contextTexts(open); !equalStrings(got, []string{"first", "done waiting", "third", "after the wait"}) {
			t.Errorf("Context = %v; the rejected send wrote nothing", got)
		}
	})
}

func TestSendEventsInOrder(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script(wefttest.Say("hello")))
		s, _ := thread.Create(ctx, st, agent)
		t1, _ := s.Send(ctx, weft.User("hi"))

		var events []weft.Event
		for ev, err := range t1.Events() {
			if err != nil {
				t.Fatalf("Events err: %v", err)
			}
			events = append(events, ev)
		}
		if len(events) < 3 {
			t.Fatalf("events = %d, want at least RunStart, StepStart, …", len(events))
		}
		if _, ok := events[0].(weft.RunStart); !ok {
			t.Errorf("first event = %T, want RunStart", events[0])
		}
		if _, ok := events[len(events)-1].(weft.RunFinish); !ok {
			t.Errorf("last event = %T, want RunFinish", events[len(events)-1])
		}
		// The step events of one run arrive in their loop order:
		// StepStart, the deltas, StepFinish — and nothing after
		// RunFinish.
		order := map[string]int{"RunStart": 0, "StepStart": 1, "TextDelta": 2, "StepFinish": 3, "RunFinish": 4}
		last := -1
		for _, ev := range events {
			name := fmt.Sprintf("%T", ev)
			name = strings.TrimPrefix(name, "weft.")
			rank, ok := order[name]
			if !ok {
				continue
			}
			if rank < last {
				t.Errorf("%s after a later event: events out of order", name)
			}
			last = rank
		}
		if _, err := t1.Wait(); err != nil {
			t.Fatalf("Wait after Events: %v", err)
		}
		// The stream may be ranged again: the session drives the run.
		n := 0
		for range t1.Events() {
			n++
		}
		if n == 0 {
			t.Error("second Events range saw nothing")
		}
	})
}

func TestSendPromptDurableWhenRunNeverStarts(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script()) // empty: the first model call fails
		s, _ := thread.Create(ctx, st, agent)
		t1, err := s.Send(ctx, weft.User("answer me"))
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if _, err := t1.Wait(); err == nil {
			t.Fatal("empty script: no error")
		}
		open := reopen(t, ctx, st, s)
		if got := contextTexts(open); !equalStrings(got, []string{"answer me"}) {
			t.Errorf("Context = %v, want the durable prompt", got)
		}
		for _, e := range open.Entries() {
			if te, ok := e.(thread.TurnEntry); ok && te.Err == "" {
				t.Error("turn entry has no error text")
			}
		}
	})
}

func TestSendRunOptionsRejected(t *testing.T) {
	ctx := context.Background()
	s, _ := thread.Create(ctx, thread.Memory(), weft.New(wefttest.Script()))
	for name, opt := range map[string]weft.RunOption{
		"Messages": weft.Messages(weft.User("x")),
		"Prompt":   weft.Prompt("x"),
		"RunID":    weft.RunID("mine"),
	} {
		if _, err := s.Send(ctx, weft.User("q"), thread.RunOptions(opt)); err == nil {
			t.Errorf("RunOptions(%s): no error", name)
		}
	}
	// Without the rejected options the run carries the caller's extras.
	if _, err := s.Send(ctx, weft.User("q"), thread.RunOptions(weft.Deny("call_1", "not mine"))); err != nil {
		t.Errorf("RunOptions(Deny): %v", err)
	}
}

func TestSendQueuedPromptSurvivesCancel(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		tool, rel := blockingTool()
		agent := weft.New(
			wefttest.Script(
				wefttest.ToolCalls(wefttest.Call{Name: "wait"}),
				wefttest.Say("done"),
				wefttest.Say("second done"),
			),
			tool,
		)
		s, _ := thread.Create(ctx, st, agent)

		t1, _ := s.Send(ctx, weft.User("first"))
		// Accepted while live, then its context dies while it waits
		// in the queue: the prompt is still kept.
		queuedCtx, cancel := context.WithCancel(ctx)
		t2, err := s.Send(queuedCtx, weft.User("second"))
		if err != nil {
			t.Fatalf("queued Send: %v", err)
		}
		cancel()
		rel.open()
		if _, err := t1.Wait(); err != nil {
			t.Fatalf("Wait 1: %v", err)
		}
		if _, err := t2.Wait(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Wait 2: err = %v, want context.Canceled", err)
		}

		// The queued send's prompt was durable (kept under the
		// WithoutCancel window), and its turn is recorded as canceled.
		open := reopen(t, ctx, st, s)
		var canceled *thread.TurnEntry
		for _, e := range open.Entries() {
			if te, ok := e.(thread.TurnEntry); ok && te.Canceled {
				canceled = &te
			}
		}
		if canceled == nil {
			t.Fatal("no canceled turn entry")
		}
		if got := contextTexts(open); !equalStrings(got, []string{"first", "done", "second"}) {
			t.Errorf("Context = %v, want both prompts kept in order", got)
		}
	})
}

func TestSendConcurrentSessionsAndReads(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script(wefttest.Say("ok"), wefttest.Say("ok"), wefttest.Say("ok"), wefttest.Say("ok")))
		s, _ := thread.Create(ctx, st, agent)
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				turn, err := s.Send(ctx, weft.User("q"))
				if err != nil {
					t.Errorf("Send: %v", err)
					return
				}
				if _, err := turn.Wait(); err != nil {
					t.Errorf("Wait: %v", err)
				}
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = s.Context()
				_ = s.Entries()
				_ = s.Leaf()
				_ = s.Usage()
				time.Sleep(time.Millisecond)
			}
		}()
		wg.Wait()
		if len(open2(t, ctx, st, s).Entries()) != 4*3 { // prompt + reply + turn, four times
			t.Error("wrong entry count after concurrent sends")
		}
	})
}

func open2(t *testing.T, ctx context.Context, st thread.Storage, s *thread.Session) *thread.Session {
	t.Helper()
	return reopen(t, ctx, st, s)
}

func TestSendPendingApprovalResume(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		ran := make(chan string, 1)
		agent := weft.New(
			wefttest.Script(
				wefttest.ToolCalls(wefttest.Call{Name: "dangerous", ID: "call_9"}),
				wefttest.Say("the call ran"),
				wefttest.Say("queued reply"),
			),
			weft.Tool("dangerous", "needs a human", func(ctx context.Context, in struct{}) (string, error) {
				ran <- "ran"
				return "approved result", nil
			}, weft.RequireApproval()),
		)
		s, _ := thread.Create(ctx, st, agent)

		t1, err := s.Send(ctx, weft.User("do the dangerous thing"))
		if err != nil {
			t.Fatalf("Send 1: %v", err)
		}
		res1, err := t1.Wait()
		if err != nil {
			t.Fatalf("Wait 1: %v", err) // a pending turn is a success
		}
		if len(res1.Pending) != 1 || res1.Pending[0].ID != "call_9" {
			t.Fatalf("Pending = %+v, want call_9", res1.Pending)
		}

		// The turn entry records the pending call, the request entry
		// makes it durable, and the tree keeps the call unresolved —
		// v0.2's resume is Decide, which resumes on its own (ADR 0021
		// §1): the manual Send-with-a-decision path is superseded, and
		// a Send now queues behind the open boundary instead.
		open := reopen(t, ctx, st, s)
		var pending []weft.ToolCallPart
		for _, e := range open.Entries() {
			if te, ok := e.(thread.TurnEntry); ok && len(te.Pending) > 0 {
				pending = te.Pending
			}
		}
		if len(pending) != 1 || pending[0].ID != "call_9" {
			t.Fatalf("turn entry Pending = %+v, want call_9", pending)
		}
		if pend := open.Pending(); len(pend) != 1 || pend[0].CallID != "call_9" {
			t.Fatalf("reopened Pending = %+v, want call_9", pend)
		}

		t2, err := s.Send(ctx, weft.User("approve it"))
		if err != nil {
			t.Fatalf("Send 2: %v", err)
		}
		rt, err := s.Decide(ctx, thread.Approve("call_9"))
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
		if rt == nil {
			t.Fatal("Decide did not resume the boundary")
		}
		res2, err := rt.Wait()
		if err != nil {
			t.Fatalf("resume Wait: %v", err)
		}
		_ = res2
		res3, err := t2.Wait()
		if err != nil {
			t.Fatalf("Wait 2: %v", err)
		}
		// The queued follow-up ran after the resume, on its own step.
		if res3.Text() != "queued reply" {
			t.Errorf("reply = %q, want the queued follow-up's reply", res3.Text())
		}
		select {
		case r := <-ran:
			if r != "ran" {
				t.Errorf("tool reported %q", r)
			}
		default:
			t.Error("the approved call never ran")
		}
	})
}

func TestSendRunIDUniqueAcrossReopen(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(wefttest.Script(wefttest.Say("a"), wefttest.Say("b")))
		s, _ := thread.Create(ctx, st, agent)
		t1, _ := s.Send(ctx, weft.User("one"))
		if _, err := t1.Wait(); err != nil {
			t.Fatal(err)
		}
		// Reopen: the counter recovers from the turn entries, so the
		// next run id continues the sequence.
		s2, err := thread.Open(ctx, st, s.ID(), agent)
		if err != nil {
			t.Fatal(err)
		}
		t2, err := s2.Send(ctx, weft.User("two"))
		if err != nil {
			t.Fatal(err)
		}
		if t2.RunID() != s.ID()+"-t2" {
			t.Errorf("run id after reopen = %q, want -t2", t2.RunID())
		}
		if _, err := t2.Wait(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSendAfterFailedTurn(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		boom := errors.New("boom")
		agent := weft.New(
			wefttest.Script(
				wefttest.ToolCalls(wefttest.Call{Name: "lookup"}),
				wefttest.Fail(boom),
				wefttest.Say("recovered"),
			),
			weft.Tool("lookup", "", func(ctx context.Context, in struct{}) (string, error) {
				return "found", nil
			}),
		)
		s, _ := thread.Create(ctx, st, agent)
		t1, _ := s.Send(ctx, weft.User("first"))
		if _, err := t1.Wait(); err == nil {
			t.Fatal("want the scripted failure")
		}
		// Nothing from the failed turn corrupts the next turn: the
		// context is a valid transcript and the run continues.
		t2, err := s.Send(ctx, weft.User("try again"))
		if err != nil {
			t.Fatalf("Send after failure: %v", err)
		}
		res, err := t2.Wait()
		if err != nil {
			t.Fatalf("Wait after failure: %v", err)
		}
		if res.Text() != "recovered" {
			t.Errorf("reply = %q", res.Text())
		}
		if msgs := s.Context(); msgs[len(msgs)-1].Text() != "recovered" {
			t.Errorf("final context message = %+v", msgs[len(msgs)-1])
		}
	})
}

func TestSendToolPanicRecordsTurn(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent := weft.New(
			wefttest.Script(
				wefttest.ToolCalls(wefttest.Call{Name: "bang"}),
				wefttest.Say("carried on"),
			),
			weft.Tool("bang", "", func(ctx context.Context, in struct{}) (string, error) {
				panic("tool blew up")
			}),
		)
		s, _ := thread.Create(ctx, st, agent)
		t1, _ := s.Send(ctx, weft.User("use the tool"))
		res, err := t1.Wait()
		if err != nil {
			t.Fatalf("a contained tool panic must not fail the run: %v", err)
		}
		if res.Text() != "carried on" {
			t.Errorf("reply = %q", res.Text())
		}
		found := false
		for _, e := range reopen(t, ctx, st, s).Entries() {
			if te, ok := e.(thread.TurnEntry); ok && te.Err == "" {
				found = true
			}
		}
		if !found {
			t.Error("no clean turn entry for the panic-containing turn")
		}
	})
}
