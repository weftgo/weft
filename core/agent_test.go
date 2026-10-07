package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

type echoIn struct {
	Msg string `json:"msg"`
}

type echoOut struct {
	Echoed string `json:"echoed"`
}

func TestNilModelPanics(t *testing.T) {
	for name, m := range map[string]core.Model{
		"untyped nil":  nil,
		"typed nil":    (*wefttest.Model)(nil),
		"typed nil in": func() core.Model { var m *wefttest.Model; return m }(),
	} {
		panicked := func() (p bool) {
			defer func() { p = recover() != nil }()
			core.New(m)
			return false
		}()
		if !panicked {
			t.Errorf("New(%s) did not panic", name)
		}
	}
}

func TestGenerateRunsToolsEndToEnd(t *testing.T) {
	echo := core.Tool("echo", "Echo a message back, uppercased.",
		func(_ context.Context, in echoIn) (echoOut, error) {
			return echoOut{Echoed: strings.ToUpper(in.Msg)}, nil
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: wefttest.Args(echoIn{Msg: "hello"})}),
		wefttest.Say("All done."),
	)
	agt := core.New(model, core.Instructions("You are a test agent."), echo)

	res, err := agt.Generate(context.Background(), core.Prompt("Echo hello."))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := res.Text(); got != "All done." {
		t.Errorf("Text() = %q, want %q", got, "All done.")
	}
	if res.NumSteps() != 2 {
		t.Errorf("NumSteps() = %d, want 2", res.NumSteps())
	}
	if res.Usage.Total() != 30 { // two scripted steps, 15 tokens each
		t.Errorf("Usage.Total() = %d, want 30", res.Usage.Total())
	}

	wantRoles := []core.Role{core.RoleUser, core.RoleAssistant, core.RoleTool, core.RoleAssistant}
	if len(res.Messages) != len(wantRoles) {
		t.Fatalf("transcript has %d messages, want %d", len(res.Messages), len(wantRoles))
	}
	for i, role := range wantRoles {
		if res.Messages[i].Role != role {
			t.Errorf("message %d role = %q, want %q", i, res.Messages[i].Role, role)
		}
	}
	result, ok := res.Messages[2].Content[0].(core.ToolResultPart)
	if !ok {
		t.Fatalf("tool message part is %T, want ToolResultPart", res.Messages[2].Content[0])
	}
	if result.Content != `{"echoed":"HELLO"}` {
		t.Errorf("tool result content = %q, want %q", result.Content, `{"echoed":"HELLO"}`)
	}

	// The model saw the system prompt, the tool catalog, and the tool result.
	reqs := model.Requests()
	if len(reqs) != 2 {
		t.Fatalf("model was called %d times, want 2", len(reqs))
	}
	if reqs[0].System != "You are a test agent." {
		t.Errorf("system prompt = %q", reqs[0].System)
	}
	if len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Name != "echo" {
		t.Errorf("first request tools = %+v", reqs[0].Tools)
	}
	if len(reqs[1].Messages) != 3 { // user, assistant (tool call), tool results
		t.Errorf("second request transcript length = %d, want 3", len(reqs[1].Messages))
	}
}

func TestToolErrorIsDataTheModelSees(t *testing.T) {
	var siblingRan atomic.Bool
	boom := core.Tool("boom", "Always fails.",
		func(_ context.Context, _ struct{}) (string, error) {
			return "", errors.New("billing is down")
		})
	fine := core.Tool("fine", "Always works.",
		func(_ context.Context, _ struct{}) (string, error) {
			siblingRan.Store(true)
			return "ok", nil
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "boom"}, wefttest.Call{Name: "fine"}),
		wefttest.Say("I recovered."),
	)
	agt := core.New(model, boom, fine)

	res, err := agt.Generate(context.Background(), core.Prompt("Try the tools."))
	if err != nil {
		t.Fatalf("a failing tool must not fail the run: %v", err)
	}
	if !siblingRan.Load() {
		t.Error("sibling tool did not run alongside the failing one")
	}

	results := res.Steps[0].Results
	if !results[0].IsError || !strings.Contains(results[0].Content, "billing is down") {
		t.Errorf("boom result = %+v, want an error result carrying the cause", results[0])
	}
	if results[1].IsError || results[1].Content != "ok" {
		t.Errorf("fine result = %+v", results[1])
	}

	// The failure was fed back to the model as data.
	toolMsg := model.Requests()[1].Messages[2]
	part, ok := toolMsg.Content[0].(core.ToolResultPart)
	if !ok || !part.IsError {
		t.Errorf("model-visible tool result = %+v, want IsError", toolMsg.Content[0])
	}
}

func TestPanickingToolIsContained(t *testing.T) {
	explode := core.Tool("explode", "Panics.",
		func(_ context.Context, _ struct{}) (string, error) {
			panic("oh no")
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "explode"}),
		wefttest.Say("recovered"),
	)
	agt := core.New(model, explode)

	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatalf("a panicking tool must not kill the run: %v", err)
	}
	r := res.Steps[0].Results[0]
	if !r.IsError || !strings.Contains(r.Content, "oh no") {
		t.Errorf("explode result = %+v, want a contained panic", r)
	}
}

func TestParallelToolExecution(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	gate := core.Tool("gate", "Blocks until released.",
		func(ctx context.Context, _ struct{}) (string, error) {
			entered <- struct{}{}
			select {
			case <-release:
				return "released", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "gate"}, wefttest.Call{Name: "gate"}),
		wefttest.Say("done"),
	)
	agt := core.New(model, gate)

	type outcome struct {
		res *core.RunResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := agt.Generate(context.Background(), core.Prompt("Run the gates."))
		done <- outcome{res, err}
	}()

	// Both tools must enter before either is released — impossible if the
	// executor were sequential.
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("tools did not run concurrently: a gate never opened")
		}
	}
	close(release)
	if out := <-done; out.err != nil {
		t.Fatalf("Generate: %v", out.err)
	}
}

func TestSequentialPolicy(t *testing.T) {
	var mu sync.Mutex
	active, maxActive := 0, 0
	touch := core.Tool("touch", "Touch a shared resource.",
		func(_ context.Context, _ struct{}) (string, error) {
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			return "ok", nil
		})
	model := wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "touch"},
			wefttest.Call{Name: "touch"},
			wefttest.Call{Name: "touch"},
		),
		wefttest.Say("done"),
	)
	agt := core.New(model, core.Sequential(), touch)

	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if maxActive != 1 {
		t.Fatalf("tools overlapped under Sequential(): max concurrent = %d, want 1", maxActive)
	}
}

func TestMaxStepsExceeded(t *testing.T) {
	touch := core.Tool("touch", "No-op.",
		func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
		wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
		wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
	)
	agt := core.New(model, core.MaxSteps(2), touch)

	res, err := agt.Generate(context.Background(), core.Prompt("loop forever"))
	if !errors.Is(err, core.ErrMaxSteps) {
		t.Fatalf("error = %v, want ErrMaxSteps", err)
	}
	if res != nil {
		t.Error("Generate should return a nil result on failure; use RunError.Result")
	}
	var runErr *core.RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("error type = %T, want *core.RunError", err)
	}
	if runErr.Step != 2 {
		t.Errorf("RunError.Step = %d, want 2", runErr.Step)
	}
	if runErr.Result == nil || runErr.Result.NumSteps() != 2 {
		t.Errorf("RunError.Result = %+v, want the 2-step partial transcript", runErr.Result)
	}
}

func TestStreamEventOrdering(t *testing.T) {
	add := core.Tool("add", "Add two numbers.",
		func(_ context.Context, in struct {
			A int `json:"a"`
			B int `json:"b"`
		}) (int, error) {
			return in.A + in.B, nil
		})
	model := wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "add", Args: `{"a":1,"b":2}`},
			wefttest.Call{Name: "add", Args: `{"a":3,"b":4}`},
		),
		wefttest.Say("7"),
	)
	agt := core.New(model, add)

	var events []core.Event
	for ev, err := range agt.Stream(context.Background(), core.Prompt("Add.")).Events() {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		events = append(events, ev)
	}

	if _, ok := events[0].(core.RunStart); !ok {
		t.Errorf("first event is %T, want RunStart", events[0])
	}
	if _, ok := events[1].(core.StepStart); !ok {
		t.Errorf("second event is %T, want StepStart", events[1])
	}
	last, ok := events[len(events)-1].(core.RunFinish)
	if !ok {
		t.Fatalf("last event is %T, want RunFinish", events[len(events)-1])
	}
	if last.Steps != 2 || last.Usage.Total() != 30 {
		t.Errorf("RunFinish = %+v, want 2 steps / 30 tokens", last)
	}

	// Tool events pair by CallID; Seq totally orders the observed stream.
	starts := make(map[string]core.ToolStart)
	var seqs []int64
	for _, ev := range events {
		switch e := ev.(type) {
		case core.ToolStart:
			if _, dup := starts[e.CallID]; dup {
				t.Fatalf("duplicate ToolStart for %s", e.CallID)
			}
			starts[e.CallID] = e
			seqs = append(seqs, e.Seq)
		case core.ToolFinish:
			st, ok := starts[e.CallID]
			if !ok {
				t.Fatalf("ToolFinish for unstarted call %s", e.CallID)
			}
			if e.Seq <= st.Seq {
				t.Fatalf("finish seq %d not after start seq %d", e.Seq, st.Seq)
			}
			delete(starts, e.CallID)
			seqs = append(seqs, e.Seq)
		}
	}
	if len(starts) != 0 {
		t.Errorf("unclosed tool calls: %v", starts)
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("observed event order contradicts Seq: %v", seqs)
		}
	}
}

func TestUnknownToolBecomesErrorResult(t *testing.T) {
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "nope"}),
		wefttest.Say("I adjusted."),
	)
	agt := core.New(model) // no tools registered

	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatalf("an unknown tool must not fail the run: %v", err)
	}
	r := res.Steps[0].Results[0]
	if !r.IsError || !strings.Contains(r.Content, "nope") {
		t.Errorf("result = %+v, want an error result naming the tool", r)
	}
	if res.Text() != "I adjusted." {
		t.Errorf("Text() = %q", res.Text())
	}
}

func TestContextCancellationAbortsRun(t *testing.T) {
	slow := core.Tool("slow", "Ignores cancellation.",
		func(_ context.Context, _ struct{}) (string, error) {
			time.Sleep(750 * time.Millisecond) // deliberately ignores ctx
			return "finally", nil
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "slow"}),
		wefttest.Say("done"),
	)
	agt := core.New(model, slow)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := agt.Generate(ctx, core.Prompt("x"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	var runErr *core.RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("error type = %T, want *core.RunError", err)
	}
	if runErr.Result == nil || runErr.Result.NumSteps() != 1 {
		t.Errorf("RunError.Result = %+v, want the 1-step partial transcript", runErr.Result)
	}
}

// Taps see exactly what Events() sees — same events, same order — even
// with tools running in parallel.
func TestTapSeesEventsInStreamOrder(t *testing.T) {
	tag := core.Tool("tag", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	var mu sync.Mutex
	var tapped []core.Event
	agt := core.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "tag"}, wefttest.Call{Name: "tag"}, wefttest.Call{Name: "tag"}),
			wefttest.Say("done"),
		),
		core.Tap(func(_ context.Context, ev core.Event) {
			mu.Lock()
			tapped = append(tapped, ev)
			mu.Unlock()
		}),
		tag,
	)
	var streamed []core.Event
	for ev, err := range agt.Stream(context.Background(), core.Prompt("x")).Events() {
		if err != nil {
			t.Fatal(err)
		}
		streamed = append(streamed, ev)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(tapped) != len(streamed) {
		t.Fatalf("tap saw %d events, stream saw %d", len(tapped), len(streamed))
	}
	for i := range tapped {
		tb, _ := json.Marshal(tapped[i])
		sb, _ := json.Marshal(streamed[i])
		if string(tb) != string(sb) {
			t.Fatalf("event %d: tap saw %s, stream saw %s", i, tb, sb)
		}
	}
}

// Generate emits into a no-op for the caller, but taps still see the
// whole run.
func TestGenerateFeedsTaps(t *testing.T) {
	var first, last core.Event
	agt := core.New(wefttest.Script(wefttest.Say("hi")),
		core.Tap(func(_ context.Context, ev core.Event) {
			if first == nil {
				first = ev
			}
			last = ev
		}),
	)
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if _, ok := first.(core.RunStart); !ok {
		t.Errorf("first tapped event = %T, want RunStart", first)
	}
	if _, ok := last.(core.RunFinish); !ok {
		t.Errorf("last tapped event = %T, want RunFinish", last)
	}
}

func TestTapPanicIsContained(t *testing.T) {
	var seen int
	agt := core.New(wefttest.Script(wefttest.Say("hi")),
		core.Tap(func(_ context.Context, ev core.Event) { panic("broken observer") }),
		core.Tap(func(_ context.Context, ev core.Event) { seen++ }),
	)
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatalf("a panicking tap must not break the run: %v", err)
	}
	if res.Text() != "hi" || seen == 0 {
		t.Errorf("text = %q, second tap saw %d events; want the run and the healthy tap intact", res.Text(), seen)
	}
}

func TestTapsRunInRegistrationOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	tap := func(name string) core.Option {
		return core.Tap(func(_ context.Context, ev core.Event) {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
		})
	}
	agt := core.New(wefttest.Script(wefttest.Say("hi")), tap("a"), tap("b"))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) < 2 {
		t.Fatalf("taps saw %d observations, want several", len(order))
	}
	for i := 1; i < len(order); i++ {
		if order[i] == order[i-1] {
			t.Fatalf("taps interleaved: %v", order)
		}
	}
}

// Canceled before start: no events at all — not to the sink, not to taps.
func TestTapSeesNothingAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var seen int
	agt := core.New(wefttest.Script(wefttest.Say("hi")),
		core.Tap(func(_ context.Context, ev core.Event) { seen++ }),
	)
	_, _ = agt.Generate(ctx, core.Prompt("x"))
	if seen != 0 {
		t.Errorf("tap saw %d events after cancellation, want none", seen)
	}
}

func TestModelFailureFailsTheRun(t *testing.T) {
	sentinel := errors.New("provider exploded")
	model := wefttest.Script(wefttest.Fail(sentinel))
	agt := core.New(model)

	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the provider error", err)
	}
	if res != nil {
		t.Error("result should be nil on model failure")
	}
}

// A failure after partial output (SayThenFail) fails the run, and the
// half-spoken turn is not appended to the partial transcript: a caller
// resuming from RunError.Result must not feed a truncated assistant
// message back in.
func TestMidStreamFailureDiscardsPartialText(t *testing.T) {
	boom := errors.New("stream cut mid-turn")
	agt := core.New(wefttest.Script(wefttest.SayThenFail("half an answ", boom)))

	_, err := agt.Generate(context.Background(), core.Prompt("x"))
	var runErr *core.RunError
	if !errors.As(err, &runErr) || !errors.Is(err, boom) {
		t.Fatalf("error = %v, want a RunError wrapping the stream error", err)
	}
	for _, msg := range runErr.Result.Messages {
		if msg.Role == core.RoleAssistant && strings.Contains(msg.Text(), "half an answ") {
			t.Errorf("partial assistant text reached the transcript: %v", msg)
		}
	}
}

func TestThinkingOption(t *testing.T) {
	newScript := func() *wefttest.Model {
		return wefttest.Script(wefttest.Say("ok"), wefttest.Say("ok"), wefttest.Say("ok"))
	}

	// No options: the zero ThinkingConfig — the provider default.
	m := newScript()
	if _, err := core.New(m).Generate(context.Background(), core.Prompt("q")); err != nil {
		t.Fatal(err)
	}
	if got := m.Requests()[0].Thinking; got != (core.ThinkingConfig{}) {
		t.Errorf("no options: Thinking = %+v, want the zero value", got)
	}

	// Agent-level default applies to every run.
	m = newScript()
	agt := core.New(m, core.Thinking(core.ThinkingConfig{Level: core.ThinkOff}))
	for range 2 {
		if _, err := agt.Generate(context.Background(), core.Prompt("q")); err != nil {
			t.Fatal(err)
		}
	}
	for i, req := range m.Requests() {
		if req.Thinking.Level != core.ThinkOff {
			t.Errorf("run %d: Thinking.Level = %v, want ThinkOff", i, req.Thinking.Level)
		}
	}

	// Run-level option overrides the agent default for that run alone.
	off := core.Thinking(core.ThinkingConfig{Level: core.ThinkOff})
	high := core.Thinking(core.ThinkingConfig{Level: core.ThinkHigh, Budget: 8192})
	m = newScript()
	agt = core.New(m, off)
	if _, err := agt.Generate(context.Background(), high, core.Prompt("deep")); err != nil {
		t.Fatal(err)
	}
	if _, err := agt.Generate(context.Background(), core.Prompt("quick")); err != nil {
		t.Fatal(err)
	}
	if got := m.Requests()[0].Thinking; got.Level != core.ThinkHigh || got.Budget != 8192 {
		t.Errorf("override run: Thinking = %+v, want high/8192", got)
	}
	if got := m.Requests()[1].Thinking; got.Level != core.ThinkOff {
		t.Errorf("next run: Thinking = %+v, want the agent default (off)", got)
	}
}

// deltaModel streams a tool call's argument in fragments before the
// whole call — the shape OpenAI-compatible and Anthropic providers
// deliver while the model "writes" large arguments. Step 0 requests
// the call; step 1 answers.
type deltaModel struct{ steps int }

func (m *deltaModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		m.steps++
		if m.steps == 1 {
			if !yield(core.ModelToolCallDelta{Index: 0, Name: "echo", Args: `{"msg":"he`}, nil) {
				return
			}
			if !yield(core.ModelToolCallDelta{Index: 0, Name: "echo", Args: `llo"}`}, nil) {
				return
			}
			if !yield(core.ModelToolCall{ID: "c1", Name: "echo", Args: json.RawMessage(`{"msg":"hello"}`)}, nil) {
				return
			}
			yield(core.ModelFinish{Reason: core.StopToolCalls}, nil)
			return
		}
		yield(core.ModelTextDelta{Text: "done"}, nil)
		yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
	}
}

// TestToolArgsDeltaStreamsProgress: ModelToolCallDelta progress from
// the model seam surfaces as ToolArgsDelta run events, before the call
// exists — the model is still writing its arguments. The assembled
// call still arrives and executes normally.
func TestToolArgsDeltaStreamsProgress(t *testing.T) {
	echo := core.Tool("echo", "echo", func(_ context.Context, in echoIn) (string, error) {
		return in.Msg, nil
	})
	agt := core.New(&deltaModel{}, echo)

	var evs []core.Event
	run := agt.Stream(context.Background(), core.Prompt("q"))
	for ev, err := range run.Events() {
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
	}
	res, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	var deltas []core.ToolArgsDelta
	sawToolStart := false
	for _, ev := range evs {
		switch e := ev.(type) {
		case core.ToolArgsDelta:
			deltas = append(deltas, e)
		case core.ToolStart:
			sawToolStart = true
		}
	}
	if len(deltas) != 2 || deltas[0].Name != "echo" || deltas[0].Args != `{"msg":"he` || deltas[1].Args != `llo"}` {
		t.Errorf("deltas = %+v, want both argument fragments named echo", deltas)
	}
	if sawToolStart && len(deltas) == 2 {
		// order: every delta must precede ToolStart of the call it belongs to
		for i, j := 0, 0; i < len(evs); i++ {
			switch evs[i].(type) {
			case core.ToolStart:
				if j < len(deltas) {
					t.Errorf("ToolStart arrived before delta %d", j)
				}
			case core.ToolArgsDelta:
				j++
			}
		}
	}
	if got := res.Text(); got != "done" {
		t.Errorf("text = %q, want the follow-up answer", got)
	}
}

// The orchestrator-worker pattern: one step fans out to two parallel
// delegations and one ordinary tool. Results stay in call order, Seq
// stays monotonic across the whole parent stream, and each delegation's
// usage is attributed per call.
func TestSubagentParallelDelegations(t *testing.T) {
	child := core.New(wefttest.Script(
		wefttest.Say("found"),
		wefttest.Say("found"),
	))
	refund := core.Tool("refund", "", func(_ context.Context, _ struct{}) (string, error) {
		return "refunded", nil
	})
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "research", Args: `{"prompt":"a"}`},
			wefttest.Call{Name: "research", Args: `{"prompt":"b"}`},
			wefttest.Call{Name: "refund"},
		),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child), refund)
	run := parent.Stream(context.Background(), core.Prompt("q"), core.RunID("r1"))
	var evs []core.Event
	for ev, err := range run.Events() {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		evs = append(evs, ev)
	}
	res, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range res.Steps[0].Results {
		got = append(got, r.Content)
	}
	if want := []string{"found", "found", "refunded"}; !slices.Equal(got, want) {
		t.Errorf("results = %v, want %v (call order, not completion order)", got, want)
	}
	var last int64
	for _, ev := range evs {
		var seq int64
		switch e := ev.(type) {
		case core.ToolStart:
			seq = e.Seq
		case core.ToolFinish:
			seq = e.Seq
		case core.Nested:
			seq = e.Seq
		default:
			continue
		}
		if seq <= last {
			t.Fatalf("Seq %d not after %d", seq, last)
		}
		last = seq
	}
	sub := res.Steps[0].SubagentUsage
	if len(sub) != 2 {
		t.Fatalf("SubagentUsage = %v, want two delegations", sub)
	}
	for id, u := range sub {
		if u != (core.Usage{InputTokens: 10, OutputTokens: 5}) {
			t.Errorf("SubagentUsage[%s] = %+v", id, u)
		}
	}
	if want := (core.Usage{InputTokens: 40, OutputTokens: 20}); res.Usage != want {
		t.Errorf("usage = %+v, want %+v", res.Usage, want)
	}
}

// Name is the read side of the Name option: Serve (weft/mcp) names the
// tool it exposes after the agent through it.
func TestAgentNameAccessor(t *testing.T) {
	m := wefttest.Script()
	if got := core.New(m).Name(); got != "" {
		t.Errorf("unnamed agent reports %q", got)
	}
	if got := core.New(m, core.Name("support")).Name(); got != "support" {
		t.Errorf("named agent reports %q", got)
	}
}

// stampedModel is a middleware wrapper that names itself and forwards the
// rest: enough Model for the accessor tests, plus the Unwrap the
// convention asks of wrappers.
type stampedModel struct {
	name string
	next core.Model
}

func (m stampedModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return m.next.Stream(ctx, req)
}
func (m stampedModel) Info() core.ModelInfo { return core.InfoOf(m.next) }
func (m stampedModel) Unwrap() core.Model   { return m.next }

// Model is the read side of the model chain (step 1.1, ADR 0020 §2):
// the model as the loop calls it — WrapModel middleware included, in
// the installed order — and Unwrap walks it one level at a time.
func TestAgentModelAccessor(t *testing.T) {
	base := wefttest.Script(wefttest.Say("hi"))
	// No middleware: the model itself.
	if got := core.New(base).Model(); got != core.Model(base) {
		t.Errorf("Model without middleware = %#v, want the New model", got)
	}
	// WrapModel(a, b) calls a(b(model)): the walk sees a, b, the model.
	outer := func(next core.Model) core.Model { return stampedModel{name: "outer", next: next} }
	inner := func(next core.Model) core.Model { return stampedModel{name: "inner", next: next} }
	var walked []string
	for m := core.New(base, core.WrapModel(outer, inner)).Model(); m != nil; m = core.Unwrap(m) {
		if tag, ok := m.(stampedModel); ok {
			walked = append(walked, tag.name)
			continue
		}
		walked = append(walked, "base")
	}
	if want := []string{"outer", "inner", "base"}; !slices.Equal(walked, want) {
		t.Errorf("the chain walked %v, want %v", walked, want)
	}
	// Unwrap on a non-wrapper is nil, so the walk terminates.
	if got := core.Unwrap(base); got != nil {
		t.Errorf("Unwrap on a non-wrapper = %#v, want nil", got)
	}
}

func TestToolChoiceOption(t *testing.T) {
	classify := core.Tool("classify", "", func(_ context.Context, _ struct{}) (string, error) {
		return "billing", nil
	})
	named := core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "classify"}
	anyMode := core.ToolChoiceConfig{Mode: core.ToolChoiceAny}
	newScript := func() *wefttest.Model {
		return wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "classify"}),
			wefttest.Say("ok"), wefttest.Say("ok"), wefttest.Say("ok"), wefttest.Say("ok"),
		)
	}

	// No options: the zero config — nothing forced.
	m := newScript()
	if _, err := core.New(m, classify).Generate(context.Background(), core.Prompt("q")); err != nil {
		t.Fatal(err)
	}
	if got := m.Requests()[0].ToolChoice; got != (core.ToolChoiceConfig{}) {
		t.Errorf("no options: ToolChoice = %+v, want the zero value", got)
	}

	// Agent-level default applies to every step of every run.
	m = newScript()
	agt := core.New(m, classify, core.ToolChoice(anyMode))
	for range 2 {
		if _, err := agt.Generate(context.Background(), core.Prompt("q")); err != nil {
			t.Fatal(err)
		}
	}
	for i, req := range m.Requests() {
		if req.ToolChoice.Mode != core.ToolChoiceAny {
			t.Errorf("request %d: ToolChoice.Mode = %q, want any", i, req.ToolChoice.Mode)
		}
	}

	// Run-level option overrides the agent default for that run alone.
	m = newScript()
	agt = core.New(m, classify, core.ToolChoice(anyMode))
	if _, err := agt.Generate(context.Background(), core.ToolChoice(named), core.Prompt("route")); err != nil {
		t.Fatal(err)
	}
	if _, err := agt.Generate(context.Background(), core.Prompt("route")); err != nil {
		t.Fatal(err)
	}
	if got := m.Requests()[0].ToolChoice; got != named {
		t.Errorf("override run: ToolChoice = %+v, want %+v", got, named)
	}
	if got := m.Requests()[2].ToolChoice; got.Mode != core.ToolChoiceAny {
		t.Errorf("next run: ToolChoice = %+v, want the agent default (any)", got)
	}

	// PrepareStep rewrites the choice per step: forced on step 0, auto
	// afterwards — the router shape.
	m = newScript()
	agt = core.New(m, classify, core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
		if step == 0 {
			req.ToolChoice = named
		} else {
			req.ToolChoice = core.ToolChoiceConfig{}
		}
		return req, nil
	}))
	if _, err := agt.Generate(context.Background(), core.Prompt("route")); err != nil {
		t.Fatal(err)
	}
	if got := m.Requests()[0].ToolChoice; got != named {
		t.Errorf("step 0: ToolChoice = %+v, want forced classify", got)
	}
	if got := m.Requests()[1].ToolChoice; got != (core.ToolChoiceConfig{}) {
		t.Errorf("step 1: ToolChoice = %+v, want the provider default", got)
	}
}

func TestToolChoiceValidation(t *testing.T) {
	classify := core.Tool("classify", "", func(_ context.Context, _ struct{}) (string, error) {
		return "billing", nil
	})
	forced := core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "classify"}
	cases := []struct {
		name string
		opts []core.Option
		want string
	}{
		{
			name: "no tools",
			opts: []core.Option{core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceAny})},
			want: "tool_choice mode \"any\" with an empty tool list",
		},
		{
			name: "no tools none",
			opts: []core.Option{core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceNone})},
			want: "tool_choice mode \"none\" with an empty tool list",
		},
		{
			name: "name not advertised",
			opts: []core.Option{classify, core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "route"})},
			want: "tool_choice names \"route\" but the step advertises no such tool",
		},
		{
			name: "empty name",
			opts: []core.Option{classify, core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceNamed})},
			want: "mode \"tool\" requires a Name",
		},
		{
			name: "name under any",
			opts: []core.Option{classify, core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceAny, Name: "classify"})},
			want: "Name \"classify\" set under mode \"any\"",
		},
		{
			name: "name under auto",
			opts: []core.Option{classify, core.ToolChoice(core.ToolChoiceConfig{Name: "classify"})},
			want: "Name \"classify\" set under mode \"\"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := wefttest.Script(wefttest.Say("never reached"))
			_, err := core.New(m, tc.opts...).Generate(context.Background(), core.Prompt("q"))
			if err == nil {
				t.Fatalf("run succeeded; want failure containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}

	// A PrepareStep function that drops the tool a forced name needs
	// fails in the same place — the validation runs on the request
	// after the chain.
	m := wefttest.Script(wefttest.Say("never reached"))
	drop := func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) {
		req.Tools = nil
		return req, nil
	}
	_, err := core.New(m, classify, core.ToolChoice(forced), core.PrepareStep(drop)).
		Generate(context.Background(), core.Prompt("q"))
	if err == nil || !strings.Contains(err.Error(), `tool_choice names "classify" but the step advertises no such tool`) {
		t.Errorf("err = %v, want the no-such-tool failure after PrepareStep", err)
	}
}

func TestParamsValidation(t *testing.T) {
	neg := -5
	zero := 0
	// The three ways a negative MaxTokens can reach a step: an agent
	// default, a run option, a PrepareStep edit — all fail alike, at
	// the step that carries them, named in the text (the
	// validateToolChoice stance).
	agentErr := func(opts ...core.Option) error {
		m := wefttest.Script(wefttest.Say("never reached"))
		_, err := core.New(m, opts...).Generate(context.Background(), core.Prompt("q"))
		return err
	}
	if err := agentErr(core.Params(core.RequestParams{MaxTokens: &neg})); err == nil ||
		!strings.Contains(err.Error(), "params: MaxTokens -5 is negative") {
		t.Errorf("agent default: err = %v, want the negative-MaxTokens failure", err)
	}
	m := wefttest.Script(wefttest.Say("never reached"))
	_, err := core.New(m, core.Params(core.RequestParams{MaxTokens: &zero})).
		Generate(context.Background(), core.Prompt("q"), core.Params(core.RequestParams{MaxTokens: &neg}))
	if err == nil || !strings.Contains(err.Error(), "params: MaxTokens -5 is negative") {
		t.Errorf("run override: err = %v, want the negative-MaxTokens failure", err)
	}
	m = wefttest.Script(wefttest.Say("never reached"))
	_, err = core.New(m, core.PrepareStep(func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) {
		req.Params = core.RequestParams{MaxTokens: &neg}
		return req, nil
	})).Generate(context.Background(), core.Prompt("q"))
	if err == nil || !strings.Contains(err.Error(), "params: MaxTokens -5 is negative") {
		t.Errorf("PrepareStep edit: err = %v, want the negative-MaxTokens failure", err)
	}
	// Zero stays a value (the anthropic exception's territory), and an
	// absent MaxTokens stays absent — neither fails.
	m = wefttest.Script(wefttest.Say("ok"))
	if _, err := core.New(m, core.Params(core.RequestParams{MaxTokens: &zero})).
		Generate(context.Background(), core.Prompt("q")); err != nil {
		t.Errorf("zero MaxTokens: err = %v, want success", err)
	}
}

func TestParamsOption(t *testing.T) {
	p := func(f float64) *float64 { return &f }
	newScript := func() *wefttest.Model {
		return wefttest.Script(wefttest.Say("ok"), wefttest.Say("ok"), wefttest.Say("ok"))
	}

	// No options: the zero RequestParams — construction defaults stand.
	m := newScript()
	if _, err := core.New(m).Generate(context.Background(), core.Prompt("q")); err != nil {
		t.Fatal(err)
	}
	if got := m.Requests()[0].Params; got.Temperature != nil || got.TopP != nil || got.MaxTokens != nil || got.Stop != nil || got.Seed != nil {
		t.Errorf("no options: Params = %+v, want the zero value", got)
	}

	// Agent-level default applies to every run.
	m = newScript()
	agt := core.New(m, core.Params(core.RequestParams{Temperature: p(0.2)}))
	for range 2 {
		if _, err := agt.Generate(context.Background(), core.Prompt("q")); err != nil {
			t.Fatal(err)
		}
	}
	for i, req := range m.Requests() {
		if req.Params.Temperature == nil || *req.Params.Temperature != 0.2 {
			t.Errorf("request %d: Params.Temperature = %v, want 0.2", i, req.Params.Temperature)
		}
	}

	// A run-level Params replaces the agent's struct whole — no field
	// merge: the override carries only what it sets.
	m = newScript()
	agt = core.New(m, core.Params(core.RequestParams{Temperature: p(0.2), TopP: p(0.9)}))
	if _, err := agt.Generate(context.Background(),
		core.Params(core.RequestParams{Temperature: p(0.9)}), core.Prompt("creative")); err != nil {
		t.Fatal(err)
	}
	got := m.Requests()[0].Params
	if got.Temperature == nil || *got.Temperature != 0.9 {
		t.Errorf("override run: Temperature = %v, want 0.9", got.Temperature)
	}
	if got.TopP != nil {
		t.Errorf("override run: TopP = %v, want nil (the struct replaces whole)", got.TopP)
	}

	// PrepareStep edits the request's Params per step: cold for the
	// classifying step, the default afterwards.
	m = wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "classify"}),
		wefttest.Say("ok"),
	)
	classify := core.Tool("classify", "", func(_ context.Context, _ struct{}) (string, error) { return "x", nil })
	agt = core.New(m, classify, core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
		if step == 0 {
			req.Params.Temperature = p(0)
		}
		return req, nil
	}))
	if _, err := agt.Generate(context.Background(), core.Prompt("two steps")); err != nil {
		t.Fatal(err)
	}
	if got := m.Requests()[0].Params.Temperature; got == nil || *got != 0 {
		t.Errorf("step 0: Temperature = %v, want 0", got)
	}
	if got := m.Requests()[1].Params.Temperature; got != nil {
		t.Errorf("step 1: Temperature = %v, want nil", got)
	}
}
