package weft_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// turnModel is a bare-bones weft.Model playing raw event slices, for
// testing how the loop treats streams that bend or break the Model
// contract. wefttest cannot express those — by design.
type turnModel struct {
	turns [][]weft.ModelEvent
	pos   int
}

func (m *turnModel) Stream(context.Context, weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		if m.pos >= len(m.turns) {
			yield(nil, errors.New("script exhausted"))
			return
		}
		for _, ev := range m.turns[m.pos] {
			if !yield(ev, nil) {
				return
			}
		}
		m.pos++
	}
}

// panicModel's stream panics while being consumed.
type panicModel struct{}

func (panicModel) Stream(context.Context, weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(func(weft.ModelEvent, error) bool) { panic("adapter bug") }
}

// The Model stream contract is enforced by the loop, not merely
// documented: every violation fails the run wrapping ErrModelContract
// instead of corrupting the transcript or crashing the process.
func TestModelStreamContractViolations(t *testing.T) {
	finish := weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}}
	cases := []struct {
		name  string
		model weft.Model
		want  string // fragment of the contract-violation message
	}{
		{
			name:  "stream ends without ModelFinish",
			model: &turnModel{turns: [][]weft.ModelEvent{{weft.ModelTextDelta{Text: "hi"}}}},
			want:  "without ModelFinish",
		},
		{
			name:  "two ModelFinish events",
			model: &turnModel{turns: [][]weft.ModelEvent{{finish, finish}}},
			want:  "after ModelFinish",
		},
		{
			name: "text delta after ModelFinish",
			model: &turnModel{turns: [][]weft.ModelEvent{
				{finish, weft.ModelTextDelta{Text: "late"}},
			}},
			want: "after ModelFinish",
		},
		{
			name: "tool call with an empty ID",
			model: &turnModel{turns: [][]weft.ModelEvent{
				{weft.ModelToolCall{ID: "", Name: "t"}, finish},
			}},
			want: "empty ID",
		},
		{
			name: "tool call with an empty name",
			model: &turnModel{turns: [][]weft.ModelEvent{
				{weft.ModelToolCall{ID: "c1", Name: ""}, finish},
			}},
			want: "empty name",
		},
		{
			name:  "panicking stream",
			model: panicModel{},
			want:  "panicked",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agt := weft.New(tc.model)
			res, err := agt.Generate(context.Background(), weft.Prompt("x"))
			if !errors.Is(err, weft.ErrModelContract) {
				t.Fatalf("error = %v, want one wrapping ErrModelContract", err)
			}
			if res != nil {
				t.Error("result should be nil on a contract failure")
			}
			var runErr *weft.RunError
			if !errors.As(err, &runErr) || runErr.Step != 0 {
				t.Errorf("error = %v, want *RunError at step 0", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not explain the violation (%q)", err, tc.want)
			}
		})
	}
}

// A max_tokens finish is recorded, not fatal: the run succeeds and
// RunResult.StopReason (plus the last StepRecord) carries the truncation
// so callers can branch on it without indexing.
func TestMaxTokensStopReasonIsRecorded(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.MaxTokens("The answer is 4")))
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatalf("a truncated reply must not fail the run: %v", err)
	}
	if res.StopReason != weft.StopMaxTokens {
		t.Errorf("RunResult.StopReason = %q, want %q", res.StopReason, weft.StopMaxTokens)
	}
	if last := res.Steps[len(res.Steps)-1]; last.StopReason != weft.StopMaxTokens {
		t.Errorf("last StepRecord.StopReason = %q, want %q", last.StopReason, weft.StopMaxTokens)
	}
	if res.Text() != "The answer is 4" {
		t.Errorf("partial text = %q, want the truncated reply preserved", res.Text())
	}
	// An ordinary run records the ordinary reason.
	res, err = weft.New(wefttest.Script(wefttest.Say("done"))).Generate(context.Background(), weft.Prompt("x"))
	if err != nil || res.StopReason != weft.StopEndTurn {
		t.Errorf("normal run: res.StopReason = %q, err = %v; want %q", res.StopReason, err, weft.StopEndTurn)
	}
}

// max_tokens alongside tool calls is recoverable: the step's tools run
// (truncated arguments become error results) and the next model call
// proceeds normally.
func TestMaxTokensWithToolCallsContinues(t *testing.T) {
	touch := weft.Tool("touch", "", func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	model := &turnModel{turns: [][]weft.ModelEvent{
		{
			weft.ModelToolCall{ID: "c1", Name: "touch", Args: json.RawMessage(`{}`)},
			weft.ModelFinish{Reason: weft.StopMaxTokens, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			weft.ModelTextDelta{Text: "recovered"},
			weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}}
	res, err := weft.New(model, touch).Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatalf("a max_tokens step with tool calls must not fail the run: %v", err)
	}
	if res.NumSteps() != 2 || res.Text() != "recovered" {
		t.Errorf("result = %d steps, text %q; want 2 steps ending in recovery", res.NumSteps(), res.Text())
	}
}

// Oversized tool results are capped with a visible marker — successes,
// failures alike — and the cap can be turned off.
func TestMaxResultBytes(t *testing.T) {
	const cap = 64 << 10
	marker := fmt.Sprintf("\n…[truncated %d bytes]", cap)
	big := weft.Tool("big", "", func(_ context.Context, _ struct{}) (string, error) {
		return strings.Repeat("x", 200_000), nil
	})
	boom := weft.Tool("boom", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", errors.New(strings.Repeat("e", 200_000))
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "big"}, wefttest.Call{Name: "boom"}),
		wefttest.Say("ok"),
	), big, boom)

	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	results := res.Steps[0].Results
	for i, r := range results {
		if !strings.HasSuffix(r.Content, marker) {
			t.Errorf("result %d does not end with the truncation marker: ...%q", i, r.Content[len(r.Content)-80:])
		}
		if len(r.Content) > cap+64 { // cut + marker, with room to spare
			t.Errorf("result %d length = %d, want ≈ cap", i, len(r.Content))
		}
		if !utf8.ValidString(r.Content) {
			t.Errorf("result %d is not valid UTF-8 after the cut", i)
		}
	}
	if !strings.HasPrefix(results[0].Content, strings.Repeat("x", 1000)) {
		t.Error("capped result lost its head")
	}
	if !results[1].IsError {
		t.Error("capped error result lost IsError")
	}

	// MaxResultBytes(0) disables the cap.
	agt2 := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "big"}),
		wefttest.Say("ok"),
	), weft.MaxResultBytes(0), big)
	res2, err := agt2.Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(res2.Steps[0].Results[0].Content); got != 200_000 {
		t.Errorf("uncapped result length = %d, want 200000", got)
	}

	// The cut lands on a rune boundary: capping 200 bytes of two-byte
	// runes at 101 leaves exactly 100 bytes of payload.
	runes := weft.Tool("runes", "", func(_ context.Context, _ struct{}) (string, error) {
		return strings.Repeat("é", 100), nil
	})
	res3, err := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "runes"}),
		wefttest.Say("ok"),
	), weft.MaxResultBytes(101), runes).Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSuffix(res3.Steps[0].Results[0].Content, "\n…[truncated 101 bytes]")
	if len(body) != 100 || !utf8.ValidString(body) {
		t.Errorf("rune-boundary cut left %d bytes (valid UTF-8: %v), want 100", len(body), utf8.ValidString(body))
	}
}

// The execution policy travels on the request: adapters mirror
// SequentialTools in the provider setting so Sequential() also stops the
// model from emitting parallel batches.
func TestSequentialToolsHintMirrorsPolicy(t *testing.T) {
	touch := weft.Tool("touch", "", func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	run := func(opts ...weft.Option) bool {
		model := wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
			wefttest.Say("ok"),
		)
		agt := weft.New(model, append([]weft.Option{touch}, opts...)...)
		if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
			t.Fatal(err)
		}
		return model.Requests()[0].SequentialTools
	}
	if run() {
		t.Error("default policy: SequentialTools = true, want false (provider default)")
	}
	if run(weft.Parallelism(2)) {
		t.Error("Parallelism(2): SequentialTools = true, want false")
	}
	if !run(weft.Parallelism(1)) {
		t.Error("Parallelism(1): SequentialTools = false, want true")
	}
	if !run(weft.Sequential()) {
		t.Error("Sequential(): SequentialTools = false, want true")
	}
}

// Tool inputs must be structs (or pointers to one): providers and MCP
// require an object at the top level of the schema.
func TestNonStructToolInputPanics(t *testing.T) {
	didPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: Tool did not panic", name)
			}
		}()
		fn()
	}
	didPanic("string input", func() {
		weft.Tool("t", "", func(_ context.Context, in string) (string, error) { return in, nil })
	})
	didPanic("map input", func() {
		weft.Tool("t", "", func(_ context.Context, in map[string]any) (string, error) { return "", nil })
	})
	didPanic("slice input", func() {
		weft.Tool("t", "", func(_ context.Context, in []string) (string, error) { return "", nil })
	})
	// A struct, and a pointer to one, are both fine.
	weft.Tool("t", "", func(_ context.Context, in struct{ A int }) (string, error) { return "", nil })
	weft.Tool("t", "", func(_ context.Context, in *struct{ A int }) (string, error) { return "", nil })
}

// Typed nils must be caught at construction: a nil *Model passes a plain
// interface-nil check and would crash later inside a run goroutine,
// where no caller can recover it.
func TestTypedNilModelPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New((*wefttest.Model)(nil)) did not panic")
		}
	}()
	weft.New((*wefttest.Model)(nil))
}

// The message wire format is a compatibility contract: every part carries
// a type discriminator and a transcript round-trips losslessly.
func TestMessageJSONRoundTrip(t *testing.T) {
	in := []weft.Message{
		weft.User("hi"),
		{Role: weft.RoleUser, Content: []weft.Part{
			weft.FilePart{MediaType: "image/png", Data: []byte("png")},
			weft.FilePart{MediaType: "image/png", URL: "https://example.com/a.png"},
		}},
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ReasoningPart{Text: "thinking"},
			weft.TextPart{Text: "calling"},
			weft.ToolCallPart{ID: "c1", Name: "echo", Args: json.RawMessage(`{"msg":"x"}`)},
		}},
		{Role: weft.RoleTool, Content: []weft.Part{
			weft.ToolResultPart{CallID: "c1", Name: "echo", Content: `"X"`},
			weft.ToolResultPart{CallID: "c2", Name: "boom", Content: "down", IsError: true},
		}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"role":"user","content":[{"type":"text","text":"hi"}]},` +
		`{"role":"user","content":[{"type":"file","media_type":"image/png","data":"cG5n"},{"type":"file","media_type":"image/png","url":"https://example.com/a.png"}]},` +
		`{"role":"assistant","content":[{"type":"reasoning","text":"thinking"},{"type":"text","text":"calling"},{"type":"tool_call","id":"c1","name":"echo","args":{"msg":"x"}}]},` +
		`{"role":"tool","content":[{"type":"tool_result","call_id":"c1","name":"echo","content":"\"X\"","is_error":false},{"type":"tool_result","call_id":"c2","name":"boom","content":"down","is_error":true}]}]`
	if string(b) != want {
		t.Errorf("wire format:\n got  %s\n want %s", b, want)
	}

	var out []weft.Message
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("round trip produced %d messages, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i].Role != in[i].Role || len(out[i].Content) != len(in[i].Content) {
			t.Fatalf("message %d differs: %+v vs %+v", i, out[i], in[i])
		}
		for j := range in[i].Content {
			gb, _ := json.Marshal(out[i].Content[j])
			wb, _ := json.Marshal(in[i].Content[j])
			if string(gb) != string(wb) {
				t.Errorf("message %d part %d: got %s want %s", i, j, gb, wb)
			}
		}
	}

	// Text and reasoning are distinguishable after decoding.
	if _, ok := out[2].Content[0].(weft.ReasoningPart); !ok {
		t.Errorf("part 0 decoded as %T, want ReasoningPart", out[2].Content[0])
	}
	if _, ok := out[2].Content[1].(weft.TextPart); !ok {
		t.Errorf("part 1 decoded as %T, want TextPart", out[2].Content[1])
	}
	// Files decode with their bytes intact.
	f, ok := out[1].Content[0].(weft.FilePart)
	if !ok || f.MediaType != "image/png" || string(f.Data) != "png" {
		t.Errorf("file part = %+v, want the inline image/png back", out[1].Content[0])
	}
}

// UserParts preserves part order and does not alias the caller's slice.
func TestUserParts(t *testing.T) {
	parts := []weft.Part{
		weft.TextPart{Text: "What is this?"},
		weft.FilePart{MediaType: "image/png", Data: []byte("png")},
	}
	m := weft.UserParts(parts...)
	if m.Role != weft.RoleUser || len(m.Content) != 2 {
		t.Fatalf("UserParts = %+v, want a user message with both parts", m)
	}
	if txt, ok := m.Content[0].(weft.TextPart); !ok || txt.Text != "What is this?" {
		t.Errorf("part 0 = %+v, want the text first (order preserved)", m.Content[0])
	}
	if _, ok := m.Content[1].(weft.FilePart); !ok {
		t.Errorf("part 1 = %T, want FilePart", m.Content[1])
	}
	// Mutating the returned content must not touch the caller's slice.
	m.Content[0] = weft.TextPart{Text: "mutated"}
	if parts[0].(weft.TextPart).Text != "What is this?" {
		t.Error("UserParts aliased the caller's slice")
	}
}

func TestMessageJSONRejectsUnknownPart(t *testing.T) {
	var m weft.Message
	err := json.Unmarshal([]byte(`{"role":"user","content":[{"type":"hologram","x":1}]}`), &m)
	if err == nil || !strings.Contains(err.Error(), "hologram") {
		t.Errorf("unknown part type error = %v, want one naming the type", err)
	}
	err = json.Unmarshal([]byte(`{"role":"user","content":[{"text":"no type"}]}`), &m)
	if err == nil {
		t.Error("a part without a type discriminator must not decode")
	}
}

// Sequential means one at a time AND in call order.
func TestSequentialRunsInCallOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	tag := weft.Tool("tag", "Record the call.",
		func(_ context.Context, in struct {
			N string `json:"n"`
		}) (string, error) {
			mu.Lock()
			order = append(order, in.N)
			mu.Unlock()
			return in.N, nil
		})

	const n = 8
	for round := 0; round < 50; round++ {
		order = nil
		var calls []wefttest.Call
		for j := 0; j < n; j++ {
			calls = append(calls, wefttest.Call{Name: "tag", Args: `{"n":"` + string(rune('a'+j)) + `"}`})
		}
		agt := weft.New(wefttest.Script(wefttest.ToolCalls(calls...), wefttest.Say("ok")), weft.Sequential(), tag)
		if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < n; j++ {
			if order[j] != string(rune('a'+j)) {
				t.Fatalf("round %d: tools ran out of call order: %v", round, order)
			}
		}
	}
}

// Under any parallelism, ToolStart events are emitted in call order.
func TestToolStartsInCallOrder(t *testing.T) {
	sleepy := weft.Tool("sleepy", "Vary in duration.",
		func(_ context.Context, in struct {
			N int `json:"n"`
		}) (int, error) {
			time.Sleep(time.Duration(5-in.N) * 3 * time.Millisecond)
			return in.N, nil
		})
	var calls []wefttest.Call
	for j := 0; j < 5; j++ {
		calls = append(calls, wefttest.Call{ID: string(rune('a' + j)), Name: "sleepy", Args: `{"n":` + string(rune('0'+j)) + `}`})
	}
	agt := weft.New(wefttest.Script(wefttest.ToolCalls(calls...), wefttest.Say("ok")), weft.Parallelism(2), sleepy)

	var starts []string
	for ev, err := range agt.Stream(context.Background(), weft.Prompt("x")).Events() {
		if err != nil {
			t.Fatal(err)
		}
		if s, ok := ev.(weft.ToolStart); ok {
			starts = append(starts, s.CallID)
		}
	}
	if got := strings.Join(starts, ""); got != "abcde" {
		t.Errorf("ToolStart order = %q, want call order abcde", got)
	}
}

// A tool that never started because the run was canceled produces an
// error result and no events — never a ToolFinish without a ToolStart.
func TestCanceledBeforeStartEmitsNoOrphanEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	block := weft.Tool("block", "Cancels the run, then waits for it.",
		func(ctx context.Context, _ struct{}) (string, error) {
			cancel()
			<-ctx.Done()
			return "", ctx.Err()
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "block"}, wefttest.Call{Name: "block"}, wefttest.Call{Name: "block"}),
		wefttest.Say("unreachable"),
	)
	agt := weft.New(model, weft.Sequential(), block)

	run := agt.Stream(ctx, weft.Prompt("x"))
	started := map[string]bool{}
	for ev, err := range run.Events() {
		if err != nil {
			break
		}
		switch e := ev.(type) {
		case weft.ToolStart:
			started[e.CallID] = true
		case weft.ToolFinish:
			if !started[e.CallID] {
				t.Errorf("ToolFinish for %s without a ToolStart", e.CallID)
			}
		}
	}
	_, err := run.Wait()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	var runErr *weft.RunError
	if !errors.As(err, &runErr) || runErr.Result == nil {
		t.Fatalf("error = %v, want *RunError with a partial result", err)
	}
	results := runErr.Result.Steps[0].Results
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	for i := 1; i < 3; i++ {
		if !results[i].IsError || !strings.Contains(results[i].Content, "before the tool started") {
			t.Errorf("result %d = %+v, want a canceled-before-start error", i, results[i])
		}
	}
}

// Cancellation during the final allowed step is reported as cancellation,
// not as step exhaustion.
func TestCancellationBeatsMaxSteps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	touch := weft.Tool("touch", "Cancels the run.",
		func(_ context.Context, _ struct{}) (string, error) {
			cancel()
			return "ok", nil
		})
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "touch"}))
	agt := weft.New(model, weft.MaxSteps(1), touch)

	_, err := agt.Generate(ctx, weft.Prompt("x"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if errors.Is(err, weft.ErrMaxSteps) {
		t.Error("cancellation must not be reported as ErrMaxSteps")
	}
}

func TestSchemaUntaggedFieldUsesGoName(t *testing.T) {
	tool := weft.Tool("t", "", func(_ context.Context, _ struct {
		City string
		Zip  string `json:"zip"`
	}) (string, error) {
		return "", nil
	})
	b, _ := json.Marshal(tool.InputSchema)
	want := `{"type":"object","properties":{"City":{"type":"string"},"zip":{"type":"string"}},"required":["City","zip"]}`
	if string(b) != want {
		t.Errorf("schema:\n got  %s\n want %s", b, want)
	}
}

type treeNode struct {
	Label    string      `json:"label"`
	Children []*treeNode `json:"children,omitempty"`
}

func TestSchemaRecursiveTypeTerminates(t *testing.T) {
	tool := weft.Tool("tree", "", func(_ context.Context, _ treeNode) (string, error) { return "", nil })
	b, _ := json.Marshal(tool.InputSchema)
	want := `{"type":"object","properties":{"children":{"type":"array","items":{}},"label":{"type":"string"}},"required":["label"]}`
	if string(b) != want {
		t.Errorf("schema:\n got  %s\n want %s", b, want)
	}
}

func TestSchemaSameTypeTwiceIsNotACycle(t *testing.T) {
	type point struct {
		X int `json:"x"`
	}
	tool := weft.Tool("seg", "", func(_ context.Context, _ struct {
		A point `json:"a"`
		B point `json:"b"`
	}) (string, error) {
		return "", nil
	})
	b, _ := json.Marshal(tool.InputSchema)
	if strings.Count(string(b), `"x":{"type":"integer"}`) != 2 {
		t.Errorf("sibling fields of the same type must each get a full schema: %s", b)
	}
}

// Breaking out of the range cancels the run and leaks no goroutine.
func TestStreamEarlyBreakCancelsRun(t *testing.T) {
	var sawCancel sync.WaitGroup
	sawCancel.Add(1)
	slow := weft.Tool("slow", "Waits for cancellation.",
		func(ctx context.Context, _ struct{}) (string, error) {
			<-ctx.Done()
			sawCancel.Done()
			return "", ctx.Err()
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "slow"}),
		wefttest.Say("unreachable"),
	)
	agt := weft.New(model, slow)

	before := runtime.NumGoroutine()
	run := agt.Stream(context.Background(), weft.Prompt("x"))
	for ev := range run.Events() {
		if _, ok := ev.(weft.ToolStart); ok {
			break
		}
	}
	sawCancel.Wait()
	_, err := run.Wait()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait after early break = %v, want context.Canceled", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Errorf("goroutines after run: %d, before: %d — leak", n, before)
	}
}

func TestRunEventsIsSingleUse(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.Say("hi")))
	run := agt.Stream(context.Background(), weft.Prompt("x"))
	for _, err := range run.Events() {
		if err != nil {
			t.Fatal(err)
		}
	}
	var got error
	for _, err := range run.Events() {
		got = err
	}
	if !errors.Is(got, weft.ErrRunConsumed) {
		t.Errorf("second Events() error = %v, want ErrRunConsumed", got)
	}
}

// Wait alone must not deadlock: it runs the agent if nobody consumed Events.
func TestWaitWithoutEventsRunsTheAgent(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.Say("hi")))
	done := make(chan struct{})
	var res *weft.RunResult
	var err error
	go func() {
		defer close(done)
		res, err = agt.Stream(context.Background(), weft.Prompt("x")).Wait()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait() without Events() deadlocked")
	}
	if err != nil || res == nil || res.Text() != "hi" {
		t.Errorf("Wait() = %+v, %v", res, err)
	}
}

func TestCallToolSentinels(t *testing.T) {
	count := weft.Tool("count", "", func(_ context.Context, in struct {
		Q string `json:"q"`
	}) (int, error) {
		return len(in.Q), nil
	})
	agt := weft.New(wefttest.Script(), count)
	ctx := context.Background()

	if _, err := agt.CallTool(ctx, weft.ToolCallPart{Name: "nope"}); !errors.Is(err, weft.ErrNoSuchTool) {
		t.Errorf("unknown tool error = %v, want ErrNoSuchTool", err)
	}
	if _, err := agt.CallTool(ctx, weft.ToolCallPart{Name: "count", Args: json.RawMessage(`{"q":1}`)}); !errors.Is(err, weft.ErrInvalidToolInput) {
		t.Errorf("bad args error = %v, want ErrInvalidToolInput", err)
	}
	out, err := agt.CallTool(ctx, weft.ToolCallPart{Name: "count", Args: json.RawMessage(`{"q":"abc"}`)})
	if err != nil || out != "3" {
		t.Errorf("CallTool = %s, %v", out, err)
	}
}

func TestToolFinishCarriesContent(t *testing.T) {
	echo := weft.Tool("echo", "", func(_ context.Context, in struct {
		M string `json:"m"`
	}) (string, error) {
		return in.M, nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"m":"hey"}`}, wefttest.Call{Name: "nope"}),
		wefttest.Say("ok"),
	), echo)
	finishes := map[string]weft.ToolFinish{}
	for ev, err := range agt.Stream(context.Background(), weft.Prompt("x")).Events() {
		if err != nil {
			t.Fatal(err)
		}
		if f, ok := ev.(weft.ToolFinish); ok {
			finishes[f.Name] = f
		}
	}
	if f := finishes["echo"]; f.Content != "hey" || f.IsError {
		t.Errorf("echo ToolFinish = %+v", f)
	}
	if f := finishes["nope"]; !f.IsError || !strings.Contains(f.Content, "nope") {
		t.Errorf("nope ToolFinish = %+v", f)
	}
}

func TestEmptyAssistantTurnIsNotRecorded(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.Say("")))
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 1 {
		t.Errorf("transcript = %+v, want only the user message", res.Messages)
	}
	if res.NumSteps() != 1 {
		t.Errorf("NumSteps = %d, want 1 (the step still happened)", res.NumSteps())
	}
}

// StopWhen ends a run successfully after the step that meets a condition;
// MaxSteps remains the failure budget behind it.
func TestStopWhenHasToolCall(t *testing.T) {
	submit := weft.Tool("submit_answer", "Final answer.",
		func(_ context.Context, in struct {
			Answer string `json:"answer"`
		}) (string, error) {
			return "recorded", nil
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "submit_answer", Args: `{"answer":"42"}`}),
		wefttest.Say("unreachable: the run must stop before this call"),
	)
	agt := weft.New(model, weft.StopWhen(weft.HasToolCall("submit_answer")), submit)

	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if res.NumSteps() != 1 || len(model.Requests()) != 1 {
		t.Errorf("run made %d model calls, want 1", len(model.Requests()))
	}
	if r := res.Steps[0].Results[0]; r.Content != "recorded" {
		t.Errorf("the stopping step's tools still run; result = %+v", r)
	}
}

func TestStopWhenStepCountIsSucceedsWhereMaxStepsFails(t *testing.T) {
	touch := weft.Tool("touch", "", func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	script := func() *wefttest.Model {
		return wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
			wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
			wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
		)
	}
	res, err := weft.New(script(), weft.StopWhen(weft.StepCountIs(2)), touch).Generate(context.Background(), weft.Prompt("x"))
	if err != nil || res.NumSteps() != 2 {
		t.Errorf("StepCountIs(2): res=%v err=%v, want a successful 2-step run", res, err)
	}
	_, err = weft.New(script(), weft.MaxSteps(2), touch).Generate(context.Background(), weft.Prompt("x"))
	if !errors.Is(err, weft.ErrMaxSteps) {
		t.Errorf("MaxSteps(2): err=%v, want ErrMaxSteps", err)
	}
}

func TestCallFromContextInsideTools(t *testing.T) {
	var got weft.Call
	var ok bool
	peek := weft.Tool("peek", "", func(ctx context.Context, _ struct{}) (string, error) {
		got, ok = weft.CallFromContext(ctx)
		return "", nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "call_xyz", Name: "peek"}),
		wefttest.Say("done"),
	), peek)
	res, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.RunID("run-1"))
	if err != nil {
		t.Fatal(err)
	}
	want := weft.Call{RunID: "run-1", Step: 0, CallID: "call_xyz", Name: "peek"}
	if !ok || got != want {
		t.Errorf("CallFromContext = %+v (ok=%v), want %+v", got, ok, want)
	}
	if res.ID != "run-1" {
		t.Errorf("RunResult.ID = %q, want the supplied id", res.ID)
	}
	// Outside the loop there is no call.
	if _, ok := weft.CallFromContext(context.Background()); ok {
		t.Error("CallFromContext must report ok=false outside a run")
	}
}

func TestRunIDsAreGeneratedAndStreamed(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.Say("hi")))
	run := agt.Stream(context.Background(), weft.Prompt("x"))
	if len(run.ID()) != 32 {
		t.Errorf("Run.ID() = %q, want a 32-hex-char generated id", run.ID())
	}
	var first weft.Event
	for ev, err := range run.Events() {
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = ev
		}
	}
	start, ok := first.(weft.RunStart)
	if !ok || start.ID != run.ID() {
		t.Errorf("first event = %+v, want RunStart{ID: %q}", first, run.ID())
	}
	res, _ := run.Wait()
	if res.ID != run.ID() {
		t.Errorf("RunResult.ID = %q, want %q", res.ID, run.ID())
	}
	other := agt.Stream(context.Background(), weft.Prompt("x"))
	if other.ID() == run.ID() {
		t.Error("two runs got the same generated id")
	}
}

// Provider reasoning round-trips: deltas accumulate into one
// ReasoningPart placed before the TextPart, the last non-empty signature
// rides along, and the wire format is pinned.
func TestReasoningRoundTrip(t *testing.T) {
	model := &turnModel{turns: [][]weft.ModelEvent{{
		weft.ModelReasoningDelta{Text: "a"},
		weft.ModelReasoningDelta{Text: "b", Signature: "sig-1"},
		weft.ModelTextDelta{Text: "hi"},
		weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	res, err := weft.New(model).Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	msg := res.Messages[1]
	if len(msg.Content) != 2 {
		t.Fatalf("assistant content = %+v, want [ReasoningPart, TextPart]", msg.Content)
	}
	if r, ok := msg.Content[0].(weft.ReasoningPart); !ok || r.Text != "ab" || r.Signature != "sig-1" {
		t.Errorf("reasoning part = %+v, want {Text: ab, Signature: sig-1}", msg.Content[0])
	}
	if txt, ok := msg.Content[1].(weft.TextPart); !ok || txt.Text != "hi" {
		t.Errorf("text part = %+v, want hi", msg.Content[1])
	}

	// The wire bytes are a compatibility contract: signature present
	// when set, absent when empty.
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"role":"assistant","content":[{"type":"reasoning","text":"ab","signature":"sig-1"},{"type":"text","text":"hi"}]}`
	if string(b) != want {
		t.Errorf("wire format:\n got  %s\n want %s", b, want)
	}
	bare, err := json.Marshal(weft.ReasoningPart{Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bare), "signature") {
		t.Errorf("empty signature must be omitted on the wire: %s", bare)
	}
	var back weft.Message
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Content[0] != msg.Content[0] {
		t.Errorf("round trip = %+v, want %+v", back.Content[0], msg.Content[0])
	}
}

// Reasoning streams before text, in the order the model produced it.
func TestReasoningEventEmitted(t *testing.T) {
	model := &turnModel{turns: [][]weft.ModelEvent{{
		weft.ModelReasoningDelta{Text: "pondering"},
		weft.ModelTextDelta{Text: "answer"},
		weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	var order []string
	for ev, err := range weft.New(model).Stream(context.Background(), weft.Prompt("x")).Events() {
		if err != nil {
			t.Fatal(err)
		}
		switch e := ev.(type) {
		case weft.ReasoningDelta:
			order = append(order, "reasoning:"+e.Text)
		case weft.TextDelta:
			order = append(order, "text:"+e.Text)
		}
	}
	want := []string{"reasoning:pondering", "text:answer"}
	if len(order) != 2 || order[0] != want[0] || order[1] != want[1] {
		t.Errorf("event order = %v, want %v", order, want)
	}
}

// Several signed reasoning blocks in one step keep their boundaries: a
// delta carrying a signature closes the block, the next delta opens a
// new one, and each part carries its own signature. Providers require
// blocks (Anthropic) or parts (Gemini) back exactly as issued — merging
// them would corrupt the round trip.
func TestReasoningMultipleBlocksKeepBoundaries(t *testing.T) {
	model := &turnModel{turns: [][]weft.ModelEvent{{
		weft.ModelReasoningDelta{Text: "first "},
		weft.ModelReasoningDelta{Text: "thought", Signature: "sig-1"},
		weft.ModelReasoningDelta{Text: "second"},
		weft.ModelReasoningDelta{Signature: "sig-2"},
		weft.ModelTextDelta{Text: "hi"},
		weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	res, err := weft.New(model).Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	msg := res.Messages[1]
	if len(msg.Content) != 3 {
		t.Fatalf("assistant content = %+v, want [Reasoning, Reasoning, Text]", msg.Content)
	}
	r1, ok1 := msg.Content[0].(weft.ReasoningPart)
	r2, ok2 := msg.Content[1].(weft.ReasoningPart)
	if !ok1 || !ok2 || r1.Text != "first thought" || r1.Signature != "sig-1" ||
		r2.Text != "second" || r2.Signature != "sig-2" {
		t.Errorf("reasoning parts = %+v %+v, want per-block text+signature", r1, r2)
	}
	if txt, ok := msg.Content[2].(weft.TextPart); !ok || txt.Text != "hi" {
		t.Errorf("text part = %+v, want hi", msg.Content[2])
	}
	// The wire bytes carry both blocks, each with its own signature.
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"role":"assistant","content":[` +
		`{"type":"reasoning","text":"first thought","signature":"sig-1"},` +
		`{"type":"reasoning","text":"second","signature":"sig-2"},` +
		`{"type":"text","text":"hi"}]}`
	if string(b) != want {
		t.Errorf("wire format:\n got  %s\n want %s", b, want)
	}
	var back weft.Message
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Content[0] != msg.Content[0] || back.Content[1] != msg.Content[1] {
		t.Errorf("round trip = %+v, want %+v", back.Content[:2], msg.Content[:2])
	}
}

// Unsigned reasoning still accumulates into a single block: no provider
// accepts unsigned blocks back, so their internal boundaries are not
// load-bearing and the transcript stays compact.
func TestUnsignedReasoningStaysOneBlock(t *testing.T) {
	model := &turnModel{turns: [][]weft.ModelEvent{{
		weft.ModelReasoningDelta{Text: "a"},
		weft.ModelReasoningDelta{Text: "b"},
		weft.ModelTextDelta{Text: "x"},
		weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	res, err := weft.New(model).Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages[1].Content) != 2 {
		t.Fatalf("content = %+v, want one reasoning part and the text", res.Messages[1].Content)
	}
	if r, ok := res.Messages[1].Content[0].(weft.ReasoningPart); !ok || r.Text != "ab" || r.Signature != "" {
		t.Errorf("reasoning = %+v, want the accumulated unsigned block", res.Messages[1].Content[0])
	}
}

// A tool call's own signature (Gemini attaches thought signatures to
// function calls and requires them back on the same part) survives the
// loop and the wire, additively: signature-less calls encode exactly as
// before.
func TestToolCallSignatureRoundTrip(t *testing.T) {
	model := &turnModel{turns: [][]weft.ModelEvent{
		{
			weft.ModelToolCall{ID: "c1", Name: "t", Args: json.RawMessage(`{}`), Signature: "sig-1"},
			weft.ModelToolCall{ID: "c2", Name: "t", Args: json.RawMessage(`{}`)},
			weft.ModelFinish{Reason: weft.StopToolCalls, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			weft.ModelTextDelta{Text: "done"},
			weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}}
	res, err := weft.New(model, weft.Tool("t", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})).Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	calls := res.Steps[0].ToolCalls
	if len(calls) != 2 || calls[0].Signature != "sig-1" || calls[1].Signature != "" {
		t.Fatalf("recorded calls = %+v, want the signature on its own call only", calls)
	}
	b, err := json.Marshal(res.Messages[1])
	if err != nil {
		t.Fatal(err)
	}
	want := `{"role":"assistant","content":[` +
		`{"type":"tool_call","id":"c1","name":"t","args":{},"signature":"sig-1"},` +
		`{"type":"tool_call","id":"c2","name":"t","args":{}}]}`
	if string(b) != want {
		t.Errorf("wire format:\n got  %s\n want %s", b, want)
	}
	var back weft.Message
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if got := back.Content[0].(weft.ToolCallPart).Signature; got != "sig-1" {
		t.Errorf("round-trip signature = %q, want sig-1", got)
	}
}

// A turn with reasoning but no text and no calls is recorded: it has
// content, and dropping it would lose a signed block the provider may
// expect back.
func TestReasoningOnlyTurnIsRecorded(t *testing.T) {
	model := &turnModel{turns: [][]weft.ModelEvent{{
		weft.ModelReasoningDelta{Text: "just thinking"},
		weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	res, err := weft.New(model).Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("transcript = %+v, want the user and the reasoning turn", res.Messages)
	}
	if _, ok := res.Messages[1].Content[0].(weft.ReasoningPart); !ok {
		t.Errorf("assistant part = %T, want ReasoningPart", res.Messages[1].Content[0])
	}
}

// A turn without reasoning events gets no reasoning part.
func TestEmptyReasoningNotAppended(t *testing.T) {
	model := &turnModel{turns: [][]weft.ModelEvent{{
		weft.ModelTextDelta{Text: "plain"},
		weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	res, err := weft.New(model).Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages[1].Content) != 1 {
		t.Errorf("content = %+v, want only the text part", res.Messages[1].Content)
	}
}

// A signed block with empty text is kept: adaptive-thinking providers
// return exactly that shape and the signature must be replayable.
func TestSignedEmptyReasoningIsKept(t *testing.T) {
	model := &turnModel{turns: [][]weft.ModelEvent{{
		weft.ModelReasoningDelta{Signature: "sig-2"},
		weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	res, err := weft.New(model).Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	r, ok := res.Messages[1].Content[0].(weft.ReasoningPart)
	if !ok || r.Text != "" || r.Signature != "sig-2" {
		t.Errorf("signed empty reasoning = %+v, want {Text: , Signature: sig-2}", res.Messages[1].Content[0])
	}
}

// Repair: missing results are synthesised in call order, appended to
// the tool message that follows, with pinned model-visible text.
func TestRepairSynthesisesMissingResults(t *testing.T) {
	in := []weft.Message{
		weft.User("go"),
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ToolCallPart{ID: "a", Name: "t1", Args: json.RawMessage(`{}`)},
			weft.ToolCallPart{ID: "b", Name: "t2", Args: json.RawMessage(`{}`)},
		}},
		{Role: weft.RoleTool, Content: []weft.Part{
			weft.ToolResultPart{CallID: "b", Name: "t2", Content: "ok"},
		}},
	}
	out := weft.Repair(in)
	if len(out) != 3 || out[2].Role != weft.RoleTool {
		t.Fatalf("repaired = %+v, want user/assistant/tool", out)
	}
	parts := out[2].Content
	if len(parts) != 2 {
		t.Fatalf("tool message = %+v, want the real result plus the synthesised one", parts)
	}
	if r := parts[0].(weft.ToolResultPart); r.CallID != "b" || r.Content != "ok" {
		t.Errorf("part 0 = %+v, want the real b result kept", parts[0])
	}
	r := parts[1].(weft.ToolResultPart)
	if r.CallID != "a" || r.Name != "t1" || !r.IsError ||
		r.Content != "no result recorded: the call was interrupted" {
		t.Errorf("synthesised result = %+v, want the pinned interrupted text", parts[1])
	}
}

// The Crush case: an assistant turn whose tools never ran (the run was
// interrupted) is repaired before the first model call, so a resumed
// session never locks.
func TestRepairDanglingLastAssistantTurn(t *testing.T) {
	in := []weft.Message{
		weft.User("go"),
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ToolCallPart{ID: "c1", Name: "lookup", Args: json.RawMessage(`{}`)},
		}},
	}
	out := weft.Repair(in)
	if len(out) != 3 || out[2].Role != weft.RoleTool {
		t.Fatalf("repaired = %+v, want a tool message appended", out)
	}
	if r := out[2].Content[0].(weft.ToolResultPart); !r.IsError || r.CallID != "c1" {
		t.Errorf("synthesised result = %+v", out[2].Content[0])
	}

	// The loop repairs its input: the model's first request already
	// carries the synthesised result.
	model := wefttest.Script(wefttest.Say("resumed"))
	_, err := weft.New(model).Generate(context.Background(), weft.Messages(in...))
	if err != nil {
		t.Fatal(err)
	}
	req := model.Requests()[0]
	if len(req.Messages) != 3 || req.Messages[2].Role != weft.RoleTool {
		t.Fatalf("first request transcript = %+v, want the repaired input", req.Messages)
	}
}

// Orphans are dropped: results naming no preceding call, tool messages
// with nothing left, tool messages with no assistant before them, and a
// second consecutive tool message (the canonical shape batches one
// step's results on one message).
func TestRepairDropsOrphanResults(t *testing.T) {
	in := []weft.Message{
		weft.User("go"),
		{Role: weft.RoleTool, Content: []weft.Part{ // no assistant before it
			weft.ToolResultPart{CallID: "x", Name: "t", Content: "orphan"},
		}},
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ToolCallPart{ID: "a", Name: "t1", Args: json.RawMessage(`{}`)},
		}},
		{Role: weft.RoleTool, Content: []weft.Part{
			weft.ToolResultPart{CallID: "ghost", Name: "t", Content: "orphan"}, // dropped part
		}},
		{Role: weft.RoleTool, Content: []weft.Part{ // second consecutive: dropped whole
			weft.ToolResultPart{CallID: "a", Name: "t1", Content: "late"},
		}},
	}
	out := weft.Repair(in)
	if len(out) != 3 {
		t.Fatalf("repaired = %d messages (%+v), want user/assistant/tool", len(out), out)
	}
	tool := out[2].Content
	if len(tool) != 1 {
		t.Fatalf("tool message = %+v, want only the synthesised a result", tool)
	}
	if r := tool[0].(weft.ToolResultPart); r.CallID != "a" || !r.IsError {
		t.Errorf("result = %+v, want the synthesised a (both real ones were orphans)", tool[0])
	}

	// A tool message whose every part is orphaned is dropped whole.
	in = []weft.Message{
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ToolCallPart{ID: "a", Name: "t1", Args: json.RawMessage(`{}`)},
		}},
		{Role: weft.RoleTool, Content: []weft.Part{
			weft.ToolResultPart{CallID: "ghost", Name: "t", Content: "orphan"},
		}},
	}
	out = weft.Repair(in)
	if len(out) != 2 || len(out[1].Content) != 1 || !out[1].Content[0].(weft.ToolResultPart).IsError {
		t.Errorf("repaired = %+v, want the orphan message replaced by synthesis", out)
	}
}

func TestRepairKeepsFirstDuplicate(t *testing.T) {
	in := []weft.Message{
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ToolCallPart{ID: "a", Name: "t1", Args: json.RawMessage(`{}`)},
		}},
		{Role: weft.RoleTool, Content: []weft.Part{
			weft.ToolResultPart{CallID: "a", Name: "t1", Content: "first"},
			weft.ToolResultPart{CallID: "a", Name: "t1", Content: "second"},
		}},
	}
	out := weft.Repair(in)
	if len(out[1].Content) != 1 || out[1].Content[0].(weft.ToolResultPart).Content != "first" {
		t.Errorf("repaired = %+v, want only the first result kept", out[1].Content)
	}
}

func TestRepairLeavesConsecutiveRolesAlone(t *testing.T) {
	in := []weft.Message{
		weft.User("one"),
		weft.User("two"),
		weft.Assistant("a"),
		weft.Assistant("b"),
	}
	out := weft.Repair(in)
	if len(out) != 4 {
		t.Fatalf("repaired = %d messages, want all four kept", len(out))
	}
	for i, want := range []string{"one", "two", "a", "b"} {
		if got := out[i].Text(); got != want {
			t.Errorf("message %d = %q, want %q (merging is the adapter's job)", i, got, want)
		}
	}
}

func TestRepairIsPureAndIdempotent(t *testing.T) {
	in := []weft.Message{
		weft.User("go"),
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ToolCallPart{ID: "a", Name: "t1", Args: json.RawMessage(`{}`)},
		}},
		{Role: weft.RoleTool, Content: []weft.Part{
			weft.ToolResultPart{CallID: "a", Name: "t1", Content: "ok"},
		}},
	}
	before, _ := json.Marshal(in)
	out := weft.Repair(in)
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Errorf("Repair mutated its input:\n was %s\n now %s", before, after)
	}
	once, _ := json.Marshal(out)
	twice, _ := json.Marshal(weft.Repair(out))
	if string(once) != string(twice) {
		t.Errorf("Repair is not idempotent:\n once  %s\n twice %s", once, twice)
	}
	// nil/empty corner: no nil-vs-empty surprises.
	if weft.Repair(nil) != nil {
		t.Error("Repair(nil) must be nil")
	}
	if out := weft.Repair([]weft.Message{}); out == nil || len(out) != 0 {
		t.Errorf("Repair(empty) = %v, want empty non-nil", out)
	}
}

// RunStart names the model when it can (wefttest), and carries the zero
// ModelInfo when it cannot (an inline model without Info).
func TestRunStartCarriesModelInfo(t *testing.T) {
	first := func(agt *weft.Agent) weft.RunStart {
		t.Helper()
		var start weft.RunStart
		for ev, err := range agt.Stream(context.Background(), weft.Prompt("x")).Events() {
			if err != nil {
				t.Fatal(err)
			}
			if s, ok := ev.(weft.RunStart); ok {
				start = s
			}
		}
		return start
	}
	if m := first(weft.New(wefttest.Script(wefttest.Say("hi")))).Model; m != (weft.ModelInfo{Provider: "wefttest", Name: "script"}) {
		t.Errorf("RunStart.Model = %+v, want the wefttest identity", m)
	}
	bare := &turnModel{turns: [][]weft.ModelEvent{{
		weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	if m := first(weft.New(bare)).Model; m != (weft.ModelInfo{}) {
		t.Errorf("RunStart.Model = %+v, want the zero value for a model without Info", m)
	}
}

// StopFunc adapts an ordinary function where the built-ins do not fit;
// the interface keeps the manifest nameable (TODO §2.9).
func TestStopFuncAdaptsFunctions(t *testing.T) {
	touch := weft.Tool("touch", "", func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
		wefttest.Say("unreachable"),
	)
	agt := weft.New(model, weft.StopWhen(weft.StopFunc(func(steps []weft.StepRecord) bool {
		return len(steps) >= 1
	})), touch)
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil || res.NumSteps() != 1 {
		t.Errorf("StopFunc: res=%v err=%v, want a successful 1-step run", res, err)
	}
}

// The manifest is deterministic: same agents, same bytes; tool order
// follows registration order and nothing else moves.
func TestManifestIsStable(t *testing.T) {
	x := weft.Tool("x", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	y := weft.Tool("y", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	newAgent := func(opts ...weft.Option) *weft.Agent {
		return weft.New(wefttest.Script(wefttest.Say("ok")),
			append([]weft.Option{weft.Name("same")}, opts...)...)
	}
	one := newAgent(x, y)
	b1, err := weft.Manifest(one)
	if err != nil {
		t.Fatal(err)
	}
	b1again, _ := weft.Manifest(one)
	if string(b1) != string(b1again) {
		t.Error("Manifest is not deterministic across calls")
	}
	b2, err := weft.Manifest(newAgent(y, x))
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) == string(b2) {
		t.Error("tool registration order is not reflected")
	}
	if sortTools(b1) != sortTools(b2) {
		t.Errorf("manifests differ beyond tool order:\n%s\n%s", sortTools(b1), sortTools(b2))
	}
}

// sortTools re-marshals a manifest with every agent's tools sorted by
// name, for order-insensitive comparison.
func sortTools(b []byte) string {
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return "unmarshal error: " + err.Error()
	}
	for _, a := range doc["agents"].([]any) {
		tools := a.(map[string]any)["tools"].([]any)
		sort.Slice(tools, func(i, j int) bool {
			return tools[i].(map[string]any)["name"].(string) < tools[j].(map[string]any)["name"].(string)
		})
	}
	out, _ := json.Marshal(doc)
	return string(out)
}

// The manifest format is a compatibility contract; these bytes are it
// (the source line is normalised — it moves with the file).
func TestManifestPinsFormat(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.Say("ok")),
		weft.Name("support-bot"),
		weft.Instructions("You are a support agent."),
		weft.Tool("refund_order", "Refund a customer's order",
			func(_ context.Context, _ struct {
				OrderID string `json:"order_id"`
			}) (string, error) {
				return "refunded", nil
			}),
	)
	b, err := weft.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	got := regexp.MustCompile(`contract_test.go:\d+`).ReplaceAllString(string(b), "contract_test.go:L")
	want := `{
  "weft": 1,
  "agents": [
    {
      "name": "support-bot",
      "model": {
        "provider": "wefttest",
        "name": "script"
      },
      "instructions": "You are a support agent.",
      "policy": {
        "parallelism": 4,
        "max_steps": 10,
        "max_result_bytes": 65536
      },
      "tools": [
        {
          "name": "refund_order",
          "description": "Refund a customer's order",
          "input_schema": {
            "type": "object",
            "properties": {
              "order_id": {
                "type": "string"
              }
            },
            "required": [
              "order_id"
            ]
          },
          "source": "contract_test.go:L"
        }
      ]
    }
  ]
}
`
	if got != want {
		t.Errorf("manifest format:\n got  %s\n want %s", got, want)
	}
}

func TestManifestRejectsUnnamedAndDuplicate(t *testing.T) {
	unnamed := weft.New(wefttest.Script(wefttest.Say("ok")))
	if _, err := weft.Manifest(unnamed); err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("unnamed agent error = %v, want one about the missing name", err)
	}
	named := func() *weft.Agent {
		return weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("dup"))
	}
	if _, err := weft.Manifest(named(), named()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicate name error = %v, want one about duplicates", err)
	}
}

// output_schema is reflected from Out: absent for a verbatim string,
// scalar for scalars, object for structs.
func TestManifestOutputSchema(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("schemas"),
		weft.Tool("text_out", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil }),
		weft.Tool("int_out", "", func(_ context.Context, _ struct{}) (int, error) { return 0, nil }),
		weft.Tool("obj_out", "", func(_ context.Context, _ struct{}) (struct {
			Ok bool `json:"ok"`
		}, error) {
			return struct {
				Ok bool `json:"ok"`
			}{}, nil
		}),
	)
	b, err := weft.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Agents []struct {
			Tools []map[string]any `json:"tools"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	tools := map[string]map[string]any{}
	for _, t := range doc.Agents[0].Tools {
		tools[t["name"].(string)] = t
	}
	if _, has := tools["text_out"]["output_schema"]; has {
		t.Error("string Out must not carry an output schema (sent verbatim)")
	}
	if s := tools["int_out"]["output_schema"].(map[string]any); s["type"] != "integer" {
		t.Errorf("int Out output_schema = %v, want integer", tools["int_out"]["output_schema"])
	}
	if s := tools["obj_out"]["output_schema"].(map[string]any); s["type"] != "object" {
		t.Errorf("struct Out output_schema = %v, want object", tools["obj_out"]["output_schema"])
	}
}

// The tool's source is the defining call site, module-relative.
func TestManifestSource(t *testing.T) {
	sourced := weft.Tool("sourced", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	b, err := weft.Manifest(weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("a"), sourced))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"source": "contract_test.go:`) {
		t.Errorf("source is not a module-relative file:line of this test file:\n%s", b)
	}
}

// Stop conditions are nameable: the built-ins stringify, a StopFunc is
// "custom", and the manifest prints exactly that.
func TestStopConditionNames(t *testing.T) {
	if got := fmt.Sprint(weft.HasToolCall("a", "b")); got != "has_tool_call:a,b" {
		t.Errorf("HasToolCall name = %q, want has_tool_call:a,b", got)
	}
	if got := fmt.Sprint(weft.StepCountIs(3)); got != "step_count_is:3" {
		t.Errorf("StepCountIs name = %q, want step_count_is:3", got)
	}
	agt := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("stops"),
		weft.StopWhen(weft.StepCountIs(2)),
		weft.StopWhen(weft.StopFunc(func([]weft.StepRecord) bool { return false })),
	)
	b, err := weft.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"stop_when": [`, `"step_count_is:2"`, `"custom"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("manifest lacks %s:\n%s", want, b)
		}
	}
}

// The event wire format is a compatibility contract (store records
// event streams, the Inspector replays them): every event carries a
// type discriminator, round-trips through UnmarshalEvent, and these
// bytes are pinned.
func TestEventJSONRoundTrip(t *testing.T) {
	cases := []struct {
		ev   weft.Event
		want string
	}{
		{weft.RunStart{ID: "r1", Model: weft.ModelInfo{Provider: "openai", Name: "gpt-5-mini"}, Agent: "support-bot"},
			`{"type":"run_start","id":"r1","model":{"provider":"openai","name":"gpt-5-mini"},"agent":"support-bot"}`},
		{weft.RunStart{ID: "r2"},
			`{"type":"run_start","id":"r2","model":{"provider":"","name":""}}`},
		{weft.StepStart{Index: 1}, `{"type":"step_start","index":1}`},
		{weft.TextDelta{Text: "hi"}, `{"type":"text_delta","text":"hi"}`},
		{weft.ReasoningDelta{Text: "hm"}, `{"type":"reasoning_delta","text":"hm"}`},
		{weft.ToolStart{Seq: 5, CallID: "c1", Name: "echo", Args: json.RawMessage(`{"m":"x"}`)},
			`{"type":"tool_start","seq":5,"call_id":"c1","name":"echo","args":{"m":"x"}}`},
		{weft.ToolStart{Seq: 7, CallID: "c2", Name: "t"},
			`{"type":"tool_start","seq":7,"call_id":"c2","name":"t","args":null}`},
		{weft.ToolFinish{Seq: 6, CallID: "c1", Name: "echo", Content: "ok", IsError: false},
			`{"type":"tool_finish","seq":6,"call_id":"c1","name":"echo","content":"ok","is_error":false}`},
		{weft.StepFinish{Index: 1, Reason: weft.StopToolCalls, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
			`{"type":"step_finish","index":1,"reason":"tool_calls","usage":{"input_tokens":10,"output_tokens":5}}`},
		{weft.StepFinish{Index: 2, Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 1, OutputTokens: 1}, Raw: "refusal"},
			`{"type":"step_finish","index":2,"reason":"stop","usage":{"input_tokens":1,"output_tokens":1},"raw":"refusal"}`},
		{weft.RunFinish{Usage: weft.Usage{InputTokens: 20, OutputTokens: 10}, Steps: 2},
			`{"type":"run_finish","usage":{"input_tokens":20,"output_tokens":10},"steps":2}`},
	}
	for i, tc := range cases {
		b, err := json.Marshal(tc.ev)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if string(b) != tc.want {
			t.Errorf("case %d bytes:\n got  %s\n want %s", i, b, tc.want)
		}
		back, err := weft.UnmarshalEvent(b)
		if err != nil {
			t.Fatalf("case %d: UnmarshalEvent: %v", i, err)
		}
		if got := normArgs(back); !reflect.DeepEqual(got, normArgs(tc.ev)) {
			t.Errorf("case %d round trip = %+v, want %+v", i, got, tc.ev)
		}
	}
}

// normArgs treats a nil Args and the literal JSON null as equal: nil
// marshals as null, and decoding null yields RawMessage("null").
func normArgs(ev weft.Event) weft.Event {
	if s, ok := ev.(weft.ToolStart); ok && string(s.Args) == "null" {
		s.Args = nil
		return s
	}
	return ev
}

func TestUnmarshalEventRejectsUnknownType(t *testing.T) {
	if _, err := weft.UnmarshalEvent([]byte(`{"type":"hologram"}`)); err == nil || !strings.Contains(err.Error(), "hologram") {
		t.Errorf("unknown type error = %v, want one naming the type", err)
	}
	if _, err := weft.UnmarshalEvent([]byte(`{"text":"no type"}`)); err == nil {
		t.Error("an event without a type discriminator must not decode")
	}
}

func TestStringOutputIsSentVerbatim(t *testing.T) {
	text := weft.Tool("text", "", func(_ context.Context, _ struct{}) (string, error) { return "sunny in Paris", nil })
	obj := weft.Tool("obj", "", func(_ context.Context, _ struct{}) (map[string]int, error) { return map[string]int{"n": 1}, nil })
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "text"}, wefttest.Call{Name: "obj"}),
		wefttest.Say("ok"),
	), text, obj)
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Steps[0].Results[0].Content; got != "sunny in Paris" {
		t.Errorf("string output = %q, want it verbatim (no JSON quotes)", got)
	}
	if got := res.Steps[0].Results[1].Content; got != `{"n":1}` {
		t.Errorf("struct output = %q, want JSON", got)
	}
}

// Name: an empty value is ignored, as its doc comment promises, so
// Name("") cannot silently clear a name set earlier.
func TestNameIgnoresEmpty(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("a"), weft.Name(""))
	if _, err := weft.Manifest(agt); err != nil {
		t.Fatalf("Name(\"\") cleared the agent name: %v", err)
	}
}

// ErrStreamIdle is a run error like any provider error: the loop wraps
// it in RunError and callers branch on it provider-agnostically.
func TestErrStreamIdleIsARunError(t *testing.T) {
	agt := weft.New(wefttest.Script(
		wefttest.Fail(fmt.Errorf("%w after 60s", weft.ErrStreamIdle)),
	))
	_, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if !errors.Is(err, weft.ErrStreamIdle) {
		t.Fatalf("err = %v, want ErrStreamIdle", err)
	}
	var re *weft.RunError
	if !errors.As(err, &re) {
		t.Fatalf("err = %T, want *RunError", err)
	}
}

// ModelFinish.Raw is recorded, not interpreted: the step and the
// StepFinish event carry the provider's unmapped stop reason.
func TestModelFinishRawRecorded(t *testing.T) {
	rawTurn := []weft.ModelEvent{
		weft.ModelTextDelta{Text: "no"},
		weft.ModelFinish{Reason: weft.StopEndTurn, Raw: "refusal", Usage: weft.Usage{InputTokens: 3, OutputTokens: 2}},
	}
	agt := weft.New(&turnModel{turns: [][]weft.ModelEvent{rawTurn}})
	var sawRaw string
	for ev, err := range agt.Stream(context.Background(), weft.Prompt("x")).Events() {
		if err != nil {
			t.Fatal(err)
		}
		if f, ok := ev.(weft.StepFinish); ok {
			sawRaw = f.Raw
		}
	}
	if sawRaw != "refusal" {
		t.Errorf("StepFinish.Raw = %q, want %q", sawRaw, "refusal")
	}
	agt = weft.New(&turnModel{turns: [][]weft.ModelEvent{rawTurn}})
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Steps[0].RawStopReason; got != "refusal" {
		t.Errorf("StepRecord.RawStopReason = %q, want %q", got, "refusal")
	}
	if got := res.Steps[0].StopReason; got != weft.StopEndTurn {
		t.Errorf("StopReason = %q, want the mapped %q", got, weft.StopEndTurn)
	}
}

// The kill switch is checked on every call (no cached state to fight);
// wefttest models ignore it.
func TestModelRequestsAllowed(t *testing.T) {
	if !weft.ModelRequestsAllowed() {
		t.Fatal("allowed by default")
	}

	t.Setenv("WEFT_MODEL_REQUESTS", "deny")
	if weft.ModelRequestsAllowed() {
		t.Fatal("deny must disallow")
	}

	t.Setenv("WEFT_MODEL_REQUESTS", "allow")
	if !weft.ModelRequestsAllowed() {
		t.Fatal("an explicit allow must allow")
	}

	// wefttest models ignore the switch: ordinary offline tests keep
	// running under deny.
	t.Setenv("WEFT_MODEL_REQUESTS", "deny")
	agt := weft.New(wefttest.Script(wefttest.Say("offline")))
	if res, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil || res.Text() != "offline" {
		t.Fatalf("wefttest run under deny: res=%v err=%v, want success", res, err)
	}
}

// TestRawTool (TODO §5.9, ADR 0003 amendment): a tool from an explicit
// schema, not reflection — invoke gets the model's raw arguments
// verbatim, a nil schema becomes the empty object schema, and the
// manifest carries no source.
func TestRawTool(t *testing.T) {
	var got json.RawMessage
	called := false
	capital := weft.RawTool("capital_of", "Return the capital of a country.",
		&weft.Schema{
			Type: "object",
			Properties: map[string]*weft.Schema{
				"country": {Type: "string", Description: "the country to look up"},
			},
			Required: []string{"country"},
		},
		func(ctx context.Context, args json.RawMessage) (string, error) {
			called = true
			got = append([]byte(nil), args...)
			return "Paris", nil
		})

	if _, err := capital.Invoke(context.Background(), json.RawMessage(`{"country":"France"}`)); err != nil || !called {
		t.Fatalf("invoke: called=%v err=%v", called, err)
	}
	// Verbatim passthrough — including bytes a reflection-based Tool
	// would reject or rewrite (the unknown field stays, no
	// ErrInvalidToolInput is implied).
	if string(got) != `{"country":"France","extra":true}` && string(got) != `{"country":"France"}` {
		t.Errorf("raw args not verbatim: %s", got)
	}
	if _, err := capital.Invoke(context.Background(), json.RawMessage(`{"country":"France","extra":true}`)); err != nil {
		t.Errorf("RawTool must not validate like Tool: %v", err)
	}

	// Raw args flow through the loop untouched and the result streams back.
	agt := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "capital_of", Args: `{"country":"France"}`}),
			wefttest.Say("done"),
		),
		weft.Name("rawtool"),
		capital,
	)
	res, err := agt.Generate(context.Background(), weft.Prompt("capital?"))
	if err != nil {
		t.Fatal(err)
	}
	var saw string
	for _, m := range res.Messages {
		for _, p := range m.Content {
			if tr, ok := p.(weft.ToolResultPart); ok && tr.Name == "capital_of" {
				saw = tr.Content
			}
		}
	}
	if saw != "Paris" {
		t.Errorf("raw tool result = %q, want Paris", saw)
	}

	// The manifest renders the explicit schema and no source line.
	b, err := weft.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(`"source"`)) {
		t.Errorf("RawTool manifest must have no source:\n%s", b)
	}
	if !bytes.Contains(b, []byte("the country to look up")) {
		t.Errorf("explicit schema not in manifest:\n%s", b)
	}

	// nil schema ⇒ empty object schema.
	anyIn := weft.RawTool("any_input", "takes anything", nil,
		func(ctx context.Context, args json.RawMessage) (string, error) { return "ok", nil })
	if anyIn.InputSchema == nil || anyIn.InputSchema.Type != "object" || len(anyIn.InputSchema.Properties) != 0 {
		t.Errorf("nil schema should become the empty object schema, got %+v", anyIn.InputSchema)
	}

	// Panics match Tool.
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Error("empty name must panic")
			}
		}()
		weft.RawTool("", "x", nil, func(context.Context, json.RawMessage) (string, error) { return "", nil })
	}()
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Error("nil handler must panic")
			}
		}()
		weft.RawTool("x", "x", nil, nil)
	}()
}

// TestToolSource (bobina §9 W-2, ADR 0003 amendment): a tool registered
// between steps — here, by a tool handler — is callable by name in the
// very next step, with no agent rebuild; without a source, the static
// list stands (and unknown names stay ErrNoSuchTool).
func TestToolSource(t *testing.T) {
	mu := sync.Mutex{}
	dynamic := []*weft.ToolDef{}

	mounter := weft.Tool("mount_late", "Register a late tool in the test.",
		func(ctx context.Context, in struct {
			Name string `json:"name"`
		}) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			dynamic = append(dynamic, weft.RawTool(in.Name, "mounted mid-run", nil,
				func(ctx context.Context, args json.RawMessage) (string, error) {
					return "from:" + in.Name, nil
				}))
			return "mounted " + in.Name, nil
		})

	agt := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "mount_late", Args: `{"name":"late_tool"}`}),
			wefttest.ToolCalls(wefttest.Call{Name: "late_tool", Args: `{}`}),
			wefttest.Say("done"),
		),
		weft.Name("toolsource"),
		mounter,
		weft.ToolSource(func() []*weft.ToolDef {
			mu.Lock()
			defer mu.Unlock()
			return append([]*weft.ToolDef{mounter}, dynamic...)
		}),
	)
	res, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	var late string
	for _, m := range res.Messages {
		for _, p := range m.Content {
			if tr, ok := p.(weft.ToolResultPart); ok && tr.Name == "late_tool" {
				late = tr.Content
			}
		}
	}
	if late != "from:late_tool" {
		t.Errorf("mid-run mounted tool result = %q; the source must refresh per step", late)
	}

	// CallTool consults the source too (the manual-dispatch seam).
	if out, err := agt.CallTool(context.Background(), weft.ToolCallPart{Name: "late_tool", Args: []byte(`{}`)}); err != nil || out != "from:late_tool" {
		t.Errorf("CallTool via source: %q, %v", out, err)
	}
	// Unknown tools remain ErrNoSuchTool, not a silent nil.
	if _, err := agt.CallTool(context.Background(), weft.ToolCallPart{Name: "never_mounted"}); !errors.Is(err, weft.ErrNoSuchTool) {
		t.Errorf("unknown tool: %v, want ErrNoSuchTool", err)
	}

	// Manifest and Tools report the static construction-time set only.
	for _, td := range agt.Tools() {
		if td.Name == "late_tool" {
			t.Error("Agent.Tools must stay static")
		}
	}
}
