package thread_test

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
)

// stepAgent builds an agent whose run answers with one tool step (signed
// reasoning included) and one closing step — the two-step shape the
// per-step tests assert over.
func stepAgent(t *testing.T) (*core.Agent, *wefttest.Model) {
	t.Helper()
	m := wefttest.Script(
		wefttest.Raw(
			core.ModelReasoningDelta{Text: "planning", Signature: "sig-step"},
			core.ModelToolCall{ID: "call_1", Name: "note", Args: []byte(`{"text":"hi"}`)},
			core.ModelFinish{Reason: core.StopToolCalls, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
		),
		wefttest.Say("done"),
	)
	note := core.Tool("note", "Record a note.", func(_ context.Context, in struct {
		Text string `json:"text"`
	}) (string, error) {
		return "noted: " + in.Text, nil
	})
	return core.New(m, note), m
}

// The per-step contract (ADR 0011 §7): the message entries a turn
// leaves in the tree are exactly the run's messages beyond its input —
// the same bytes RunResult.Messages holds, signatures included —
// written as they join, and written once: no message appears twice,
// whatever the backend.
func TestPerStepEntriesEqualRunResult(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		agent, _ := stepAgent(t)
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatal(err)
		}
		turn, err := s.Send(ctx, core.User("go"))
		if err != nil {
			t.Fatal(err)
		}
		res, err := turn.Wait()
		if err != nil {
			t.Fatal(err)
		}
		// The turn's input is the prompt alone: everything the result
		// holds beyond it must be in the tree, in order, exactly.
		want := res.Messages[1:]
		var got []core.Message
		var sawTurn int
		for _, e := range s.Entries() {
			switch e := e.(type) {
			case thread.MessageEntry:
				if e.ID != turn.ID() { // the prompt entry holds the input
					got = append(got, e.Message)
				}
			case thread.TurnEntry:
				sawTurn++
			}
		}
		if sawTurn != 1 {
			t.Errorf("%d turn entries, want 1", sawTurn)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("the tree's turn messages differ from the run's:\n got %+v\nwant %+v", got, want)
		}
		var sig string
		for _, e := range s.Entries() {
			if me, ok := e.(thread.MessageEntry); ok {
				for _, p := range me.Message.Content {
					if r, ok := p.(core.ReasoningPart); ok && r.Signature != "" {
						sig = r.Signature
					}
				}
			}
		}
		if sig != "sig-step" {
			t.Errorf("the signed reasoning survived as %q, want sig-step — signatures are the point", sig)
		}
	})
}

// Mid-run, the emitted steps are already durable: a reader — another
// Storage handle, the shape of another process — sees the first step's
// messages while the run is still blocked in its second, and nothing is
// torn. That is the whole per-step promise: a crash mid-turn loses
// nothing emitted.
func TestPerStepDurableWhileRunning(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The model's second call blocks until the test releases it.
	release := make(chan struct{})
	m := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "note", Args: `{"text":"hi"}`}),
	)
	blocking := &blockingModel{script: m, block: release}
	note := core.Tool("note", "Record a note.", func(_ context.Context, in struct {
		Text string `json:"text"`
	}) (string, error) {
		return "noted: " + in.Text, nil
	})
	s, err := thread.Create(ctx, st, core.New(blocking, note))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, core.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	waitForStep(t, turn, 0) // the tool step finished; the model call blocks

	// A fresh handle over the same directory — a reader, never a writer.
	fresh, err := jsonl.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, entries, report, err := fresh.Load(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if report != nil {
		t.Errorf("a mid-run load reported repairs: %+v", report)
	}
	// The prompt, the assistant with its call, and the tool's answer.
	if len(entries) != 3 {
		t.Fatalf("%d entries mid-run, want 3 (prompt, assistant, tool)", len(entries))
	}
	tool := entries[2].(thread.MessageEntry)
	if r, ok := tool.Message.Content[0].(core.ToolResultPart); !ok || r.Content != "noted: hi" {
		t.Errorf("the tool message mid-run: %+v", tool.Message.Content)
	}
	for _, e := range entries {
		if _, ok := e.(thread.TurnEntry); ok {
			t.Error("a turn entry landed while the turn still runs")
		}
	}
	close(release)
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	// And the end adds the closing step once, not the first step again.
	_, reloaded, _, err := fresh.Load(ctx, s.ID())
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, e := range reloaded {
		if me, ok := e.(thread.MessageEntry); ok {
			texts = append(texts, me.Message.Text())
		}
	}
	if len(texts) != 4 || texts[3] != "done" { // prompt, assistant, tool, closing assistant
		t.Errorf("texts after the turn: %v — the tail appends, never duplicates", texts)
	}
}

// blockingModel plays its script's turns and then blocks: every model
// call beyond the script waits on block. ctx still cancels it — a
// blocked model must honor the contract.
type blockingModel struct {
	script *wefttest.Model
	block  chan struct{}
	calls  atomic.Int32
}

func (m *blockingModel) Info() core.ModelInfo { return m.script.Info() }

func (m *blockingModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	if m.calls.Add(1) <= 1 {
		return m.script.Stream(ctx, req) // the scripted tool step
	}
	return func(yield func(core.ModelEvent, error) bool) {
		select {
		case <-m.block:
		case <-ctx.Done():
			yield(nil, ctx.Err())
			return
		}
		for _, ev := range []core.ModelEvent{
			core.ModelTextDelta{Text: "done"},
			core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
		} {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// waitForStep blocks until the turn's stream reports the tool step's
// StepFinish — the signal the first step is fully emitted.
func waitForStep(t *testing.T, turn *thread.Turn, index int) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev, err := range turn.Events() {
			if err != nil {
				return
			}
			if f, ok := ev.(core.StepFinish); ok && f.Index == index {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the first step never finished")
	}
}

// An interrupted turn's raw tail is rewritten repaired on the active
// path — the golden completions the turn-end batch always wrote — with
// the raw tail kept on its own branch: nothing lost, the context the
// model next sees exactly what it saw before per-step durability.
func TestPerStepFailedTailRewritten(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		m := wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "hang", Args: `{}`}),
			wefttest.Say("after the interrupt"),
		)
		hang := core.Tool("hang", "Block until canceled.", func(ctx context.Context, _ struct{}) (string, error) {
			<-ctx.Done()
			return "", ctx.Err() // the bare cancellation noise
		})
		started := make(chan struct{})
		var once sync.Once
		agent := core.New(m, hang, core.Tap(func(_ context.Context, ev core.Event) {
			if _, ok := ev.(core.ToolStart); ok {
				once.Do(func() { close(started) })
			}
		}))
		s, err := thread.Create(ctx, st, agent, thread.BusyPolicy(thread.Interrupt))
		if err != nil {
			t.Fatal(err)
		}
		t1, err := s.Send(ctx, core.User("start the work"))
		if err != nil {
			t.Fatal(err)
		}
		<-started
		t2, err := s.Send(ctx, core.User("stop, do this instead"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := t1.Wait(); !errors.Is(err, context.Canceled) {
			t.Fatalf("the interrupted turn: %v", err)
		}
		if _, err := t2.Wait(); err != nil {
			t.Fatal(err)
		}
		// The active path holds the golden completion, not the noise.
		golden := "tool call hang was interrupted: the run was canceled for a newer message"
		sawGolden := false
		for _, msg := range s.Context() {
			for _, p := range msg.Content {
				if r, ok := p.(core.ToolResultPart); ok && r.Content == golden {
					sawGolden = true
				}
			}
		}
		if !sawGolden {
			t.Errorf("the active path lacks the golden completion; context:\n%s", renderContext(s))
		}
		// The raw tail — the tool's bare cancellation noise — survives
		// on its own branch: evidence, never deleted.
		sawRaw := false
		for _, e := range s.Entries() {
			me, ok := e.(thread.MessageEntry)
			if !ok || me.Message.Role != core.RoleTool {
				continue
			}
			for _, p := range me.Message.Content {
				if r, ok := p.(core.ToolResultPart); ok && r.Content == "context canceled" {
					sawRaw = true
				}
			}
		}
		if !sawRaw {
			t.Error("the raw interrupted tail vanished from the tree — the rewrite deleted evidence")
		}
	})
}

// The overflow re-run under per-step durability (ADR 0011 §7 with ADR
// 0020 §5): the failed attempt's emitted messages — a whole first step
// — survive on their own branch of the tree, and nothing of them rides
// the active path the re-run continues on: the compaction summarizes a
// path that does not contain them, and the model's next request reads
// the re-run's transcript alone.
func TestPerStepOverflowAttemptsKeepTheirOwnLines(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		model := wefttest.Script(
			wefttest.Say("the first answer"),                                                // turn 1: history for the cut
			wefttest.ToolCalls(wefttest.Call{Name: "note", Args: `{"text":"attempt one"}`}), // attempt 1, step 0 — emitted
			wefttest.Fail(core.ErrContextOverflow),                                          // attempt 1, step 1 — overflows
			wefttest.Say("the summary of what came before"),                                 // the compaction's summarizer
			wefttest.Say("recovered after compaction"),                                      // attempt 2
		)
		note := core.Tool("note", "Record a note.", func(_ context.Context, in struct {
			Text string `json:"text"`
		}) (string, error) {
			return "noted: " + in.Text, nil
		})
		s, err := thread.Create(ctx, st, core.New(model, note), thread.KeepRecent(1))
		if err != nil {
			t.Fatal(err)
		}
		t0, err := s.Send(ctx, core.User("a first question"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := t0.Wait(); err != nil {
			t.Fatal(err)
		}
		t1, err := s.Send(ctx, core.User("a prompt that overflows"))
		if err != nil {
			t.Fatal(err)
		}
		res, err := t1.Wait()
		if err != nil {
			t.Fatalf("the overflow re-run failed: %v", err)
		}
		if res.Text() != "recovered after compaction" {
			t.Fatalf("reply = %q, want the re-run's answer", res.Text())
		}
		// Three turn entries: the first turn's, the failed attempt's own
		// ledger — its run id, the overflow, the step it completed and
		// paid for, the re-run it led to — and the re-run's.
		tes := turnEntries(s)
		if len(tes) != 3 {
			t.Fatalf("turn entries = %d, want 3 (the first turn, the failed attempt, the re-run)", len(tes))
		}
		attempt, rerun := tes[1], tes[2]
		if attempt.RunID != s.ID()+"-t2" || attempt.ReRun != s.ID()+"-t3" || rerun.RunID != s.ID()+"-t3" {
			t.Errorf("run ids: attempt %q (re-run %q), re-run %q; want -t2 (-t3), -t3", attempt.RunID, attempt.ReRun, rerun.RunID)
		}
		if attempt.Err == "" || attempt.Steps != 1 || attempt.Usage.InputTokens != 10 {
			t.Errorf("the attempt's ledger = %+v, want the overflow, one step and its usage", attempt)
		}
		if rerun.Err != "" || rerun.ReRun != "" {
			t.Errorf("the re-run's entry = %+v", rerun)
		}
		// The attempt's tokens were spent: the ledger counts them. Four
		// model steps of 10 input tokens ran as turns (the first turn,
		// the attempt's step, the re-run) — the summarizer's is its own
		// bucket.
		if u := s.Usage(); u.Turns.InputTokens != 30 {
			t.Errorf("Usage.Turns = %+v, want the attempt's step counted (30 input tokens)", u.Turns)
		}
		// The failed attempt's emitted step exists exactly once — on its
		// own branch — and never on the path the walk reads.
		sawAttempt := 0
		for _, e := range s.Entries() {
			me, ok := e.(thread.MessageEntry)
			if !ok {
				continue
			}
			for _, p := range me.Message.Content {
				if r, ok := p.(core.ToolResultPart); ok && r.Content == "noted: attempt one" {
					sawAttempt++
				}
			}
		}
		if sawAttempt != 1 {
			t.Errorf("the failed attempt's tool result appears %d times, want exactly once on its branch", sawAttempt)
		}
		for _, m := range s.Context() {
			for _, p := range m.Content {
				if r, ok := p.(core.ToolResultPart); ok && r.Content == "noted: attempt one" {
					t.Error("the failed attempt's work rides the active path — the re-run must not see it")
				}
			}
		}
	})
}

// turnMessages counts, per text, how often a message body appears in
// the message entries above the turn's prompt — the shape the

// turnMessages counts, per text, how often a message body appears in
// the message entries above the turn's prompt — the shape the
// no-duplicates assertions read.
func turnMessages(t *testing.T, s *thread.Session, promptID string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, e := range s.Entries() {
		me, ok := e.(thread.MessageEntry)
		if !ok || me.ID == promptID {
			continue
		}
		counts[string(me.Message.Role)+"\x00"+me.Message.Text()]++
	}
	return counts
}

// assertOnceEach fails when any message the turn emitted is held more
// than once, printing the path for the failure.
func assertOnceEach(t *testing.T, s *thread.Session, promptID string) {
	t.Helper()
	for text, n := range turnMessages(t, s, promptID) {
		if n != 1 {
			t.Errorf("message %q held %d times, want exactly once:\n%s", text, n, renderContext(s))
		}
	}
}

// Bookkeeping the turn admits between its steps must not uncount the
// step messages around it. A delivered steer's queued receipt entry
// lands on the active path between the step batches (accepted input is
// durable input); the turn's end computes what the tree already holds
// by walking that path, so a receipt between the messages may not make
// the batch rewrite messages the observer already wrote — the reply
// would ride the path twice and every later turn's context would
// carry the duplicate (the regression: the walk stopped at the first
// non-message entry it met).
func TestPerStepSteerReceiptBetweenSteps(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		echo := core.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
			return "ok", nil
		})
		model := wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
			wefttest.Say("done"),
		)
		var steerRef *thread.Session
		var steerOnce sync.Once
		agent := core.New(model, echo, core.Tap(func(_ context.Context, ev core.Event) {
			if _, ok := ev.(core.ToolStart); ok {
				steerOnce.Do(func() {
					if _, err := steerRef.Send(ctx, core.User("switch to metric units")); err != nil {
						t.Errorf("steer Send: %v", err)
					}
				})
			}
		}))
		s, err := thread.Create(ctx, st, agent, thread.BusyPolicy(thread.Steer))
		if err != nil {
			t.Fatal(err)
		}
		steerRef = s
		t1, err := s.Send(ctx, core.User("convert this"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := t1.Wait(); err != nil {
			t.Fatal(err)
		}
		assertOnceEach(t, s, t1.ID())
	})
}

// The same shape with a label: a caller may label an entry while a
// turn runs, and the label entry lands between the step messages — the
// turn's end must still see every step message the observer wrote.
func TestPerStepLabelBetweenSteps(t *testing.T) {
	eachBackend(t, func(t *testing.T, st thread.Storage) {
		ctx := context.Background()
		note := core.Tool("note", "Record a note.", func(_ context.Context, in struct {
			Text string `json:"text"`
		}) (string, error) {
			return "noted: " + in.Text, nil
		})
		model := wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "note", Args: `{"text":"hi"}`}),
			wefttest.Say("done"),
		)
		var s *thread.Session
		var once sync.Once
		agent := core.New(model, note, core.Tap(func(_ context.Context, ev core.Event) {
			if _, ok := ev.(core.ToolStart); ok {
				once.Do(func() {
					if err := s.Label(ctx, s.Leaf(), "mid-turn"); err != nil {
						t.Errorf("mid-run Label: %v", err)
					}
				})
			}
		}))
		s, err := thread.Create(ctx, st, agent)
		if err != nil {
			t.Fatal(err)
		}
		t1, err := s.Send(ctx, core.User("go"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := t1.Wait(); err != nil {
			t.Fatal(err)
		}
		assertOnceEach(t, s, t1.ID())
	})
}

// failNthAppend fails the nth Append it sees (1-based), once, and
// behaves otherwise — the storage hiccup of the exactly-once rows.
type failNthAppend struct {
	thread.Storage
	mu sync.Mutex
	n  int
	at int
}

func (f *failNthAppend) Append(ctx context.Context, session string, entries ...thread.Entry) error {
	f.mu.Lock()
	f.n++
	hit := f.n == f.at
	f.mu.Unlock()
	if hit {
		return errors.New("disk hiccup")
	}
	return f.Storage.Append(ctx, session, entries...)
}

// arm makes the nth Append from now fail.
func (f *failNthAppend) arm(nth int) {
	f.mu.Lock()
	f.n, f.at = 0, nth
	f.mu.Unlock()
}

// turnEntries lists the session's turn entries, in append order.
func turnEntries(s *thread.Session) []thread.TurnEntry {
	var out []thread.TurnEntry
	for _, e := range s.Entries() {
		if te, ok := e.(thread.TurnEntry); ok {
			out = append(out, te)
		}
	}
	return out
}

// Exactly once (ADR 0011 §7): whichever single append of a tool turn
// the storage refuses — the first step's assistant message, its tool
// message, the closing step, the turn's end — the context afterwards
// is the run's transcript exactly: no message lost, none written
// twice, none out of order. A failed step append is on the turn entry
// (LateSteps); a failed end is on the Turn (ErrNotPersisted).
func TestPerStepOneFailedAppendLosesAndDuplicatesNothing(t *testing.T) {
	// A tool turn's appends, in order: 1 the prompt (Send's own), 2 the
	// tool step's assistant message, 3 its tool message, 4 the closing
	// assistant message, 5 the turn's end.
	for nth := 2; nth <= 5; nth++ {
		t.Run(fmt.Sprintf("append_%d", nth), func(t *testing.T) {
			ctx := context.Background()
			st := &failNthAppend{Storage: thread.Memory()}
			agent, _ := stepAgent(t)
			s, err := thread.Create(ctx, st, agent)
			if err != nil {
				t.Fatal(err)
			}
			st.arm(nth)
			turn, err := s.Send(ctx, core.User("go"))
			if err != nil {
				t.Fatal(err)
			}
			res, err := turn.Wait()
			if nth == 5 {
				if !errors.Is(err, thread.ErrNotPersisted) || res == nil {
					t.Fatalf("Wait = %v, %v; want the result and ErrNotPersisted", res, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got := s.Context(); !reflect.DeepEqual(got, res.Messages) {
				t.Errorf("Context differs from the run's transcript:\n got %+v\nwant %+v", got, res.Messages)
			}
			// The raw path, not only the repaired view: every message of
			// the transcript sits on it once, in order.
			path, err := s.Path(s.Leaf())
			if err != nil {
				t.Fatal(err)
			}
			var raw []core.Message
			for _, e := range path {
				if me, ok := e.(thread.MessageEntry); ok {
					raw = append(raw, me.Message)
				}
			}
			if !reflect.DeepEqual(raw, res.Messages) {
				t.Errorf("the active path differs from the run's transcript:\n got %+v\nwant %+v", raw, res.Messages)
			}
			tes := turnEntries(s)
			if nth == 5 {
				if len(tes) != 0 {
					t.Errorf("%d turn entries after a refused end, want 0", len(tes))
				}
				return
			}
			if len(tes) != 1 {
				t.Fatalf("%d turn entries, want 1", len(tes))
			}
			if tes[0].LateSteps != 1 {
				t.Errorf("TurnEntry.LateSteps = %d, want 1: the gap is on the record", tes[0].LateSteps)
			}
			// The session keeps working, and a reopen reads the same.
			if got := reopenWith(t, ctx, st.Storage, s, agent).Context(); !reflect.DeepEqual(got, res.Messages) {
				t.Errorf("the stored context differs from the run's transcript:\n got %+v", got)
			}
		})
	}
}

// A turn whose every step landed records no gap.
func TestPerStepCleanTurnRecordsNoLateSteps(t *testing.T) {
	ctx := context.Background()
	agent, _ := stepAgent(t)
	s, _ := thread.Create(ctx, thread.Memory(), agent)
	turn, _ := s.Send(ctx, core.User("go"))
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if tes := turnEntries(s); len(tes) != 1 || tes[0].LateSteps != 0 {
		t.Errorf("turn entries = %+v, want one with no late steps", tes)
	}
}
