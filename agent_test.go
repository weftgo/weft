package weft_test

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

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

type echoIn struct {
	Msg string `json:"msg"`
}

type echoOut struct {
	Echoed string `json:"echoed"`
}

func TestNilModelPanics(t *testing.T) {
	for name, m := range map[string]weft.Model{
		"untyped nil":  nil,
		"typed nil":    (*wefttest.Model)(nil),
		"typed nil in": func() weft.Model { var m *wefttest.Model; return m }(),
	} {
		panicked := func() (p bool) {
			defer func() { p = recover() != nil }()
			weft.New(m)
			return false
		}()
		if !panicked {
			t.Errorf("New(%s) did not panic", name)
		}
	}
}

func TestGenerateRunsToolsEndToEnd(t *testing.T) {
	echo := weft.Tool("echo", "Echo a message back, uppercased.",
		func(_ context.Context, in echoIn) (echoOut, error) {
			return echoOut{Echoed: strings.ToUpper(in.Msg)}, nil
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hello"}`}),
		wefttest.Say("All done."),
	)
	agt := weft.New(model, weft.Instructions("You are a test agent."), echo)

	res, err := agt.Generate(context.Background(), weft.Prompt("Echo hello."))
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

	wantRoles := []weft.Role{weft.RoleUser, weft.RoleAssistant, weft.RoleTool, weft.RoleAssistant}
	if len(res.Messages) != len(wantRoles) {
		t.Fatalf("transcript has %d messages, want %d", len(res.Messages), len(wantRoles))
	}
	for i, role := range wantRoles {
		if res.Messages[i].Role != role {
			t.Errorf("message %d role = %q, want %q", i, res.Messages[i].Role, role)
		}
	}
	result, ok := res.Messages[2].Content[0].(weft.ToolResultPart)
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
	boom := weft.Tool("boom", "Always fails.",
		func(_ context.Context, _ struct{}) (string, error) {
			return "", errors.New("billing is down")
		})
	fine := weft.Tool("fine", "Always works.",
		func(_ context.Context, _ struct{}) (string, error) {
			siblingRan.Store(true)
			return "ok", nil
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "boom"}, wefttest.Call{Name: "fine"}),
		wefttest.Say("I recovered."),
	)
	agt := weft.New(model, boom, fine)

	res, err := agt.Generate(context.Background(), weft.Prompt("Try the tools."))
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
	part, ok := toolMsg.Content[0].(weft.ToolResultPart)
	if !ok || !part.IsError {
		t.Errorf("model-visible tool result = %+v, want IsError", toolMsg.Content[0])
	}
}

func TestPanickingToolIsContained(t *testing.T) {
	explode := weft.Tool("explode", "Panics.",
		func(_ context.Context, _ struct{}) (string, error) {
			panic("oh no")
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "explode"}),
		wefttest.Say("recovered"),
	)
	agt := weft.New(model, explode)

	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
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
	gate := weft.Tool("gate", "Blocks until released.",
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
	agt := weft.New(model, gate)

	type outcome struct {
		res *weft.RunResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := agt.Generate(context.Background(), weft.Prompt("Run the gates."))
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
	touch := weft.Tool("touch", "Touch a shared resource.",
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
	agt := weft.New(model, weft.Sequential(), touch)

	if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if maxActive != 1 {
		t.Fatalf("tools overlapped under Sequential(): max concurrent = %d, want 1", maxActive)
	}
}

func TestMaxStepsExceeded(t *testing.T) {
	touch := weft.Tool("touch", "No-op.",
		func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
		wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
		wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
	)
	agt := weft.New(model, weft.MaxSteps(2), touch)

	res, err := agt.Generate(context.Background(), weft.Prompt("loop forever"))
	if !errors.Is(err, weft.ErrMaxSteps) {
		t.Fatalf("error = %v, want ErrMaxSteps", err)
	}
	if res != nil {
		t.Error("Generate should return a nil result on failure; use RunError.Result")
	}
	var runErr *weft.RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("error type = %T, want *weft.RunError", err)
	}
	if runErr.Step != 2 {
		t.Errorf("RunError.Step = %d, want 2", runErr.Step)
	}
	if runErr.Result == nil || runErr.Result.NumSteps() != 2 {
		t.Errorf("RunError.Result = %+v, want the 2-step partial transcript", runErr.Result)
	}
}

func TestStreamEventOrdering(t *testing.T) {
	add := weft.Tool("add", "Add two numbers.",
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
	agt := weft.New(model, add)

	var events []weft.Event
	for ev, err := range agt.Stream(context.Background(), weft.Prompt("Add.")).Events() {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		events = append(events, ev)
	}

	if _, ok := events[0].(weft.RunStart); !ok {
		t.Errorf("first event is %T, want RunStart", events[0])
	}
	if _, ok := events[1].(weft.StepStart); !ok {
		t.Errorf("second event is %T, want StepStart", events[1])
	}
	last, ok := events[len(events)-1].(weft.RunFinish)
	if !ok {
		t.Fatalf("last event is %T, want RunFinish", events[len(events)-1])
	}
	if last.Steps != 2 || last.Usage.Total() != 30 {
		t.Errorf("RunFinish = %+v, want 2 steps / 30 tokens", last)
	}

	// Tool events pair by CallID; Seq totally orders the observed stream.
	starts := make(map[string]weft.ToolStart)
	var seqs []int64
	for _, ev := range events {
		switch e := ev.(type) {
		case weft.ToolStart:
			if _, dup := starts[e.CallID]; dup {
				t.Fatalf("duplicate ToolStart for %s", e.CallID)
			}
			starts[e.CallID] = e
			seqs = append(seqs, e.Seq)
		case weft.ToolFinish:
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
	agt := weft.New(model) // no tools registered

	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
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
	slow := weft.Tool("slow", "Ignores cancellation.",
		func(_ context.Context, _ struct{}) (string, error) {
			time.Sleep(750 * time.Millisecond) // deliberately ignores ctx
			return "finally", nil
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "slow"}),
		wefttest.Say("done"),
	)
	agt := weft.New(model, slow)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := agt.Generate(ctx, weft.Prompt("x"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	var runErr *weft.RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("error type = %T, want *weft.RunError", err)
	}
	if runErr.Result == nil || runErr.Result.NumSteps() != 1 {
		t.Errorf("RunError.Result = %+v, want the 1-step partial transcript", runErr.Result)
	}
}

// Taps see exactly what Events() sees — same events, same order — even
// with tools running in parallel.
func TestTapSeesEventsInStreamOrder(t *testing.T) {
	tag := weft.Tool("tag", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	var mu sync.Mutex
	var tapped []weft.Event
	agt := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "tag"}, wefttest.Call{Name: "tag"}, wefttest.Call{Name: "tag"}),
			wefttest.Say("done"),
		),
		weft.Tap(func(_ context.Context, ev weft.Event) {
			mu.Lock()
			tapped = append(tapped, ev)
			mu.Unlock()
		}),
		tag,
	)
	var streamed []weft.Event
	for ev, err := range agt.Stream(context.Background(), weft.Prompt("x")).Events() {
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
	var first, last weft.Event
	agt := weft.New(wefttest.Script(wefttest.Say("hi")),
		weft.Tap(func(_ context.Context, ev weft.Event) {
			if first == nil {
				first = ev
			}
			last = ev
		}),
	)
	if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if _, ok := first.(weft.RunStart); !ok {
		t.Errorf("first tapped event = %T, want RunStart", first)
	}
	if _, ok := last.(weft.RunFinish); !ok {
		t.Errorf("last tapped event = %T, want RunFinish", last)
	}
}

func TestTapPanicIsContained(t *testing.T) {
	var seen int
	agt := weft.New(wefttest.Script(wefttest.Say("hi")),
		weft.Tap(func(_ context.Context, ev weft.Event) { panic("broken observer") }),
		weft.Tap(func(_ context.Context, ev weft.Event) { seen++ }),
	)
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
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
	tap := func(name string) weft.Option {
		return weft.Tap(func(_ context.Context, ev weft.Event) {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
		})
	}
	agt := weft.New(wefttest.Script(wefttest.Say("hi")), tap("a"), tap("b"))
	if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
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
	agt := weft.New(wefttest.Script(wefttest.Say("hi")),
		weft.Tap(func(_ context.Context, ev weft.Event) { seen++ }),
	)
	_, _ = agt.Generate(ctx, weft.Prompt("x"))
	if seen != 0 {
		t.Errorf("tap saw %d events after cancellation, want none", seen)
	}
}

func TestModelFailureFailsTheRun(t *testing.T) {
	sentinel := errors.New("provider exploded")
	model := wefttest.Script(wefttest.Fail(sentinel))
	agt := weft.New(model)

	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want the provider error", err)
	}
	if res != nil {
		t.Error("result should be nil on model failure")
	}
}

func TestThinkingOption(t *testing.T) {
	newScript := func() *wefttest.Model {
		return wefttest.Script(wefttest.Say("ok"), wefttest.Say("ok"), wefttest.Say("ok"))
	}

	// No options: the zero ThinkingConfig — the provider default.
	m := newScript()
	if _, err := weft.New(m).Generate(context.Background(), weft.Prompt("q")); err != nil {
		t.Fatal(err)
	}
	if got := m.Requests()[0].Thinking; got != (weft.ThinkingConfig{}) {
		t.Errorf("no options: Thinking = %+v, want the zero value", got)
	}

	// Agent-level default applies to every run.
	m = newScript()
	agt := weft.New(m, weft.Thinking(weft.ThinkingConfig{Level: weft.ThinkOff}))
	for range 2 {
		if _, err := agt.Generate(context.Background(), weft.Prompt("q")); err != nil {
			t.Fatal(err)
		}
	}
	for i, req := range m.Requests() {
		if req.Thinking.Level != weft.ThinkOff {
			t.Errorf("run %d: Thinking.Level = %v, want ThinkOff", i, req.Thinking.Level)
		}
	}

	// Run-level option overrides the agent default for that run alone.
	off := weft.Thinking(weft.ThinkingConfig{Level: weft.ThinkOff})
	high := weft.Thinking(weft.ThinkingConfig{Level: weft.ThinkHigh, Budget: 8192})
	m = newScript()
	agt = weft.New(m, off)
	if _, err := agt.Generate(context.Background(), high, weft.Prompt("deep")); err != nil {
		t.Fatal(err)
	}
	if _, err := agt.Generate(context.Background(), weft.Prompt("quick")); err != nil {
		t.Fatal(err)
	}
	if got := m.Requests()[0].Thinking; got.Level != weft.ThinkHigh || got.Budget != 8192 {
		t.Errorf("override run: Thinking = %+v, want high/8192", got)
	}
	if got := m.Requests()[1].Thinking; got.Level != weft.ThinkOff {
		t.Errorf("next run: Thinking = %+v, want the agent default (off)", got)
	}
}

// deltaModel streams a tool call's argument in fragments before the
// whole call — the shape OpenAI-compatible and Anthropic providers
// deliver while the model "writes" large arguments. Step 0 requests
// the call; step 1 answers.
type deltaModel struct{ steps int }

func (m *deltaModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		m.steps++
		if m.steps == 1 {
			if !yield(weft.ModelToolCallDelta{Index: 0, Name: "echo", Args: `{"msg":"he`}, nil) {
				return
			}
			if !yield(weft.ModelToolCallDelta{Index: 0, Name: "echo", Args: `llo"}`}, nil) {
				return
			}
			if !yield(weft.ModelToolCall{ID: "c1", Name: "echo", Args: json.RawMessage(`{"msg":"hello"}`)}, nil) {
				return
			}
			yield(weft.ModelFinish{Reason: weft.StopToolCalls}, nil)
			return
		}
		yield(weft.ModelTextDelta{Text: "done"}, nil)
		yield(weft.ModelFinish{Reason: weft.StopEndTurn}, nil)
	}
}

// TestToolArgsDeltaStreamsProgress: ModelToolCallDelta progress from
// the model seam surfaces as ToolArgsDelta run events, before the call
// exists — the model is still writing its arguments. The assembled
// call still arrives and executes normally.
func TestToolArgsDeltaStreamsProgress(t *testing.T) {
	echo := weft.Tool("echo", "echo", func(_ context.Context, in echoIn) (string, error) {
		return in.Msg, nil
	})
	agt := weft.New(&deltaModel{}, echo)

	var evs []weft.Event
	run := agt.Stream(context.Background(), weft.Prompt("q"))
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
	var deltas []weft.ToolArgsDelta
	sawToolStart := false
	for _, ev := range evs {
		switch e := ev.(type) {
		case weft.ToolArgsDelta:
			deltas = append(deltas, e)
		case weft.ToolStart:
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
			case weft.ToolStart:
				if j < len(deltas) {
					t.Errorf("ToolStart arrived before delta %d", j)
				}
			case weft.ToolArgsDelta:
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
	child := weft.New(wefttest.Script(
		wefttest.Say("found"),
		wefttest.Say("found"),
	))
	refund := weft.Tool("refund", "", func(_ context.Context, _ struct{}) (string, error) {
		return "refunded", nil
	})
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "research", Args: `{"prompt":"a"}`},
			wefttest.Call{Name: "research", Args: `{"prompt":"b"}`},
			wefttest.Call{Name: "refund"},
		),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child), refund)
	run := parent.Stream(context.Background(), weft.Prompt("q"), weft.RunID("r1"))
	var evs []weft.Event
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
		case weft.ToolStart:
			seq = e.Seq
		case weft.ToolFinish:
			seq = e.Seq
		case weft.Nested:
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
		if u != (weft.Usage{InputTokens: 10, OutputTokens: 5}) {
			t.Errorf("SubagentUsage[%s] = %+v", id, u)
		}
	}
	if want := (weft.Usage{InputTokens: 40, OutputTokens: 20}); res.Usage != want {
		t.Errorf("usage = %+v, want %+v", res.Usage, want)
	}
}

// Name is the read side of the Name option: Serve (weft/mcp) names the
// tool it exposes after the agent through it.
func TestAgentNameAccessor(t *testing.T) {
	m := wefttest.Script()
	if got := weft.New(m).Name(); got != "" {
		t.Errorf("unnamed agent reports %q", got)
	}
	if got := weft.New(m, weft.Name("support")).Name(); got != "support" {
		t.Errorf("named agent reports %q", got)
	}
}
