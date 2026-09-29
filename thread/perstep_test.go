package thread_test

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/wefttest"
)

// stepAgent builds an agent whose run answers with one tool step (signed
// reasoning included) and one closing step — the two-step shape the
// per-step tests assert over.
func stepAgent(t *testing.T) (*weft.Agent, *wefttest.Model) {
	t.Helper()
	m := wefttest.Script(
		wefttest.Raw(
			weft.ModelReasoningDelta{Text: "planning", Signature: "sig-step"},
			weft.ModelToolCall{ID: "call_1", Name: "note", Args: []byte(`{"text":"hi"}`)},
			weft.ModelFinish{Reason: weft.StopToolCalls, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		),
		wefttest.Say("done"),
	)
	note := weft.Tool("note", "Record a note.", func(_ context.Context, in struct {
		Text string `json:"text"`
	}) (string, error) {
		return "noted: " + in.Text, nil
	})
	return weft.New(m, note), m
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
		turn, err := s.Send(ctx, weft.User("go"))
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
		var got []weft.Message
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
					if r, ok := p.(weft.ReasoningPart); ok && r.Signature != "" {
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
// torn. That is the whole v0.4 promise: a crash mid-turn loses nothing
// emitted.
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
	note := weft.Tool("note", "Record a note.", func(_ context.Context, in struct {
		Text string `json:"text"`
	}) (string, error) {
		return "noted: " + in.Text, nil
	})
	s, err := thread.Create(ctx, st, weft.New(blocking, note))
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, weft.User("go"))
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
	if r, ok := tool.Message.Content[0].(weft.ToolResultPart); !ok || r.Content != "noted: hi" {
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

func (m *blockingModel) Info() weft.ModelInfo { return m.script.Info() }

func (m *blockingModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	if m.calls.Add(1) <= 1 {
		return m.script.Stream(ctx, req) // the scripted tool step
	}
	return func(yield func(weft.ModelEvent, error) bool) {
		select {
		case <-m.block:
		case <-ctx.Done():
			yield(nil, ctx.Err())
			return
		}
		for _, ev := range []weft.ModelEvent{
			weft.ModelTextDelta{Text: "done"},
			weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
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
			if f, ok := ev.(weft.StepFinish); ok && f.Index == index {
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
		hang := weft.Tool("hang", "Block until canceled.", func(ctx context.Context, _ struct{}) (string, error) {
			<-ctx.Done()
			return "", ctx.Err() // the bare cancellation noise
		})
		started := make(chan struct{})
		var once sync.Once
		agent := weft.New(m, hang, weft.Tap(func(_ context.Context, ev weft.Event) {
			if _, ok := ev.(weft.ToolStart); ok {
				once.Do(func() { close(started) })
			}
		}))
		s, err := thread.Create(ctx, st, agent, thread.BusyPolicy(thread.Interrupt))
		if err != nil {
			t.Fatal(err)
		}
		t1, err := s.Send(ctx, weft.User("start the work"))
		if err != nil {
			t.Fatal(err)
		}
		<-started
		t2, err := s.Send(ctx, weft.User("stop, do this instead"))
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
				if r, ok := p.(weft.ToolResultPart); ok && r.Content == golden {
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
			if !ok || me.Message.Role != weft.RoleTool {
				continue
			}
			for _, p := range me.Message.Content {
				if r, ok := p.(weft.ToolResultPart); ok && r.Content == "context canceled" {
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
			wefttest.Fail(weft.ErrContextOverflow),                                          // attempt 1, step 1 — overflows
			wefttest.Say("the summary of what came before"),                                 // the compaction's summarizer
			wefttest.Say("recovered after compaction"),                                      // attempt 2
		)
		note := weft.Tool("note", "Record a note.", func(_ context.Context, in struct {
			Text string `json:"text"`
		}) (string, error) {
			return "noted: " + in.Text, nil
		})
		s, err := thread.Create(ctx, st, weft.New(model, note), thread.KeepRecent(1))
		if err != nil {
			t.Fatal(err)
		}
		t0, err := s.Send(ctx, weft.User("a first question"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := t0.Wait(); err != nil {
			t.Fatal(err)
		}
		t1, err := s.Send(ctx, weft.User("a prompt that overflows"))
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
		turns := 0
		for _, e := range s.Entries() {
			if _, ok := e.(thread.TurnEntry); ok {
				turns++
			}
		}
		if turns != 2 {
			t.Errorf("turn entries = %d, want 2 (the failed attempt records none on its own)", turns)
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
				if r, ok := p.(weft.ToolResultPart); ok && r.Content == "noted: attempt one" {
					sawAttempt++
				}
			}
		}
		if sawAttempt != 1 {
			t.Errorf("the failed attempt's tool result appears %d times, want exactly once on its branch", sawAttempt)
		}
		for _, m := range s.Context() {
			for _, p := range m.Content {
				if r, ok := p.(weft.ToolResultPart); ok && r.Content == "noted: attempt one" {
					t.Error("the failed attempt's work rides the active path — the re-run must not see it")
				}
			}
		}
	})
}
