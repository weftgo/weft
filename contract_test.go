package weft_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
			name: "duplicate tool call ID",
			model: &turnModel{turns: [][]weft.ModelEvent{
				{weft.ModelToolCall{ID: "c1", Name: "t"}, weft.ModelToolCall{ID: "c1", Name: "t"}, finish},
			}},
			want: `duplicate tool call ID "c1"`,
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

// max_tokens alongside tool calls is recoverable: none of the step's
// calls execute — each gets the pinned truncation failure — and the
// next model call proceeds normally.
func TestMaxTokensWithToolCallsFailsThemWithoutExecuting(t *testing.T) {
	executed := 0
	touch := weft.Tool("touch", "", func(_ context.Context, _ struct{}) (string, error) { executed++; return "ok", nil })
	model := &turnModel{turns: [][]weft.ModelEvent{
		{
			weft.ModelToolCall{ID: "c1", Name: "touch", Args: json.RawMessage(`{}`)},
			weft.ModelToolCall{ID: "c2", Name: "touch", Args: json.RawMessage(`{"cut`)},
			weft.ModelFinish{Reason: weft.StopMaxTokens, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			weft.ModelTextDelta{Text: "recovered"},
			weft.ModelFinish{Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}}
	var events []weft.Event
	agt := weft.New(model, touch, weft.Tap(func(_ context.Context, ev weft.Event) { events = append(events, ev) }))
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatalf("a max_tokens step with tool calls must not fail the run: %v", err)
	}
	if executed != 0 {
		t.Errorf("handler ran %d times on a truncated step; a cut message's calls must not be acted on", executed)
	}
	if res.NumSteps() != 2 || res.Text() != "recovered" {
		t.Errorf("result = %d steps, text %q; want 2 steps ending in recovery", res.NumSteps(), res.Text())
	}
	// Every call — intact or cut — gets the same pinned failure text.
	want := "tool call touch was not executed: the response hit the output token limit"
	results := res.Steps[0].Results
	if len(results) != 2 {
		t.Fatalf("results = %d, want one per call", len(results))
	}
	for i, r := range results {
		if !r.IsError || r.Content != want {
			t.Errorf("result %d = %+v, want IsError with %q", i, r, want)
		}
	}
	// The transcript carries the tool message, so the model retries
	// with a full budget on a valid transcript.
	if m := res.Messages[len(res.Messages)-2]; m.Role != weft.RoleTool || len(m.Content) != 2 {
		t.Errorf("message before the recovery = %+v, want a tool message with both results", m)
	}
	for _, ev := range events {
		switch ev.(type) {
		case weft.ToolStart, weft.ToolFinish:
			t.Errorf("unexpected %T on a truncated step: nothing executed", ev)
		}
	}
}

// Oversized tool results are capped with a visible marker — successes,
// failures alike — and the cap can be turned off.
func TestMaxResultBytes(t *testing.T) {
	const cap = 64 << 10
	marker := fmt.Sprintf("\n…[truncated %d bytes]", 200_000-cap)
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
	body := strings.TrimSuffix(res3.Steps[0].Results[0].Content, "\n…[truncated 100 bytes]")
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

// A cancellation that lands after the loop's last ctx check and before
// the terminal RunFinish is emitted must not produce a success without
// its RunFinish (rule 4), nor a RunFinish followed by an error
// (Run.Events' one-terminal-element promise). A tap cancelling on the
// final StepFinish lands exactly in that window: taps run synchronously
// before the sink, so the cancel is visible when RunFinish is about to
// be emitted. Both exits — the ordinary one and the approval boundary —
// are covered, over Generate and Stream.
func TestCancelBeforeRunFinishIsNeverASuccess(t *testing.T) {
	approved := weft.Tool("gated", "Parks the run.",
		func(context.Context, struct{}) (string, error) { return "ran", nil },
		weft.RequireApproval())
	cases := []struct {
		name  string
		model func() weft.Model
	}{
		{"ordinary exit", func() weft.Model { return wefttest.Script(wefttest.Say("done")) }},
		{"approval boundary exit", func() weft.Model {
			return wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "gated"}), wefttest.Say("unreachable"))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []string{"generate", "stream"} {
				ctx, cancel := context.WithCancel(context.Background())
				var (
					mu        sync.Mutex
					sawFinish bool
					seen      []weft.Event
				)
				agt := weft.New(tc.model(), approved, weft.Tap(func(_ context.Context, ev weft.Event) {
					mu.Lock()
					defer mu.Unlock()
					seen = append(seen, ev)
					switch ev.(type) {
					case weft.StepFinish:
						cancel() // the window: after the loop's checks, before RunFinish
					case weft.RunFinish:
						sawFinish = true
					}
				}))
				var err error
				if mode == "generate" {
					_, err = agt.Generate(ctx, weft.Prompt("x"))
				} else {
					var streamed []weft.Event
					run := agt.Stream(ctx, weft.Prompt("x"))
					for ev, serr := range run.Events() {
						if serr != nil {
							err = serr
							break
						}
						streamed = append(streamed, ev)
						if _, ok := ev.(weft.RunFinish); ok {
							sawFinish = true
						}
					}
					if err == nil {
						_, err = run.Wait()
					}
				}
				mu.Lock()
				finish := sawFinish
				mu.Unlock()
				switch {
				case err == nil && !finish:
					t.Errorf("%s: run succeeded without delivering RunFinish", mode)
				case err != nil && finish:
					t.Errorf("%s: RunFinish was delivered and then the run failed: %v", mode, err)
				case err != nil && !errors.Is(err, context.Canceled):
					t.Errorf("%s: err = %v, want context.Canceled", mode, err)
				}
				// The transcript stays resumable on the error.
				var runErr *weft.RunError
				if err != nil && (!errors.As(err, &runErr) || runErr.Result == nil) {
					t.Errorf("%s: error = %v, want *RunError with a result", mode, err)
				}
				cancel()
			}
		})
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
        "max_result_bytes": 65536,
        "max_model_retries": 3
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
		{weft.StepStart{RunID: "r1", Index: 1}, `{"type":"step_start","run_id":"r1","index":1}`},
		{weft.StepStart{Index: 2}, `{"type":"step_start","run_id":"","index":2}`},
		{weft.TextDelta{RunID: "r1", Text: "hi"}, `{"type":"text_delta","run_id":"r1","text":"hi"}`},
		{weft.ReasoningDelta{RunID: "r1", Text: "hm"}, `{"type":"reasoning_delta","run_id":"r1","text":"hm"}`},
		{weft.ToolArgsDelta{RunID: "r1", Name: "write_file", Args: `{"content":"x`},
			`{"type":"tool_args_delta","run_id":"r1","name":"write_file","args":"{\"content\":\"x"}`},
		{weft.ToolStart{RunID: "r1", Seq: 5, CallID: "c1", Name: "echo", Args: json.RawMessage(`{"m":"x"}`)},
			`{"type":"tool_start","run_id":"r1","seq":5,"call_id":"c1","name":"echo","args":{"m":"x"}}`},
		{weft.ToolStart{RunID: "r1", Seq: 7, CallID: "c2", Name: "t"},
			`{"type":"tool_start","run_id":"r1","seq":7,"call_id":"c2","name":"t","args":null}`},
		{weft.ToolFinish{RunID: "r1", Seq: 6, CallID: "c1", Name: "echo", Content: "ok", IsError: false},
			`{"type":"tool_finish","run_id":"r1","seq":6,"call_id":"c1","name":"echo","content":"ok","is_error":false}`},
		{weft.StepFinish{RunID: "r1", Index: 1, Reason: weft.StopToolCalls, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
			`{"type":"step_finish","run_id":"r1","index":1,"reason":"tool_calls","usage":{"input_tokens":10,"output_tokens":5}}`},
		{weft.StepFinish{RunID: "r1", Index: 2, Reason: weft.StopEndTurn, Usage: weft.Usage{InputTokens: 1, OutputTokens: 1}, Raw: "refusal"},
			`{"type":"step_finish","run_id":"r1","index":2,"reason":"stop","usage":{"input_tokens":1,"output_tokens":1},"raw":"refusal"}`},
		{weft.RunFinish{RunID: "r1", Usage: weft.Usage{InputTokens: 20, OutputTokens: 10}, Steps: 2},
			`{"type":"run_finish","run_id":"r1","usage":{"input_tokens":20,"output_tokens":10},"steps":2}`},
		{weft.Nested{RunID: "r1", Seq: 4, CallID: "c1", Event: weft.ToolStart{RunID: "r1/0/c1", Seq: 1, CallID: "call_1", Name: "deep_search", Args: json.RawMessage(`{}`)}},
			`{"type":"nested","run_id":"r1","seq":4,"call_id":"c1","event":{"type":"tool_start","run_id":"r1/0/c1","seq":1,"call_id":"call_1","name":"deep_search","args":{}}}`},
		{weft.Nested{RunID: "r1", Seq: 5, CallID: "c1", Event: weft.Nested{RunID: "r1/0/c1", Seq: 2, CallID: "call_1", Event: weft.TextDelta{RunID: "r1/0/c1/0/call_1", Text: "deep"}}},
			`{"type":"nested","run_id":"r1","seq":5,"call_id":"c1","event":{"type":"nested","run_id":"r1/0/c1","seq":2,"call_id":"call_1","event":{"type":"text_delta","run_id":"r1/0/c1/0/call_1","text":"deep"}}}`},
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
// wefttest models ignore it. The default-allowed assertion is skipped
// when deny is ambient (WEFT_MODEL_REQUESTS=deny go test — the offline
// gate): the switch exists to keep that mode green, not to break it.
func TestModelRequestsAllowed(t *testing.T) {
	ambientDeny := os.Getenv("WEFT_MODEL_REQUESTS") == "deny"
	if !ambientDeny && !weft.ModelRequestsAllowed() {
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

// A registered tool is frozen: New keeps a deep copy, so mutating the
// caller's value afterwards — name, description, a schema leaf — never
// reaches dispatch, advertisement, or a running run (Fix 1).
func TestToolDefFrozenAtRegistration(t *testing.T) {
	def := weft.Tool("echo", "Echo the query.",
		func(_ context.Context, in struct {
			Q string `json:"q" jsonschema:"the query"`
		}) (string, error) {
			return "echo: " + in.Q, nil
		})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"q":"hi"}`}),
		wefttest.Say("done"),
	), def)

	def.Name = "renamed"
	def.Description = "hacked"
	def.InputSchema.Properties["q"].Type = "number"

	res, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Steps[0].Results[0]; r.IsError || r.Content != "echo: hi" {
		t.Errorf("dispatch result = %+v; post-New mutation must not reach it", r)
	}
	tools := agt.Tools()
	if len(tools) != 1 || tools[0].Name != "echo" || tools[0].Description != "Echo the query." {
		t.Fatalf("advertised tool = %+v, want the registration-time echo", tools[0])
	}
	if got := tools[0].InputSchema.Properties["q"].Type; got != "string" {
		t.Errorf("schema leaf = %q, want string (frozen at New)", got)
	}
}

// Agent.Tools returns deep copies: mutating them (fields and schema
// trees alike) leaves the agent untouched (Fix 1).
func TestToolsReturnsCopies(t *testing.T) {
	def := weft.Tool("echo", "d", func(_ context.Context, in struct {
		Q string `json:"q"`
	}) (string, error) {
		return in.Q, nil
	})
	agt := weft.New(wefttest.Script(wefttest.Say("ok")), def)

	copies := agt.Tools()
	copies[0].Name = "renamed"
	copies[0].Description = "hacked"
	copies[0].InputSchema.Properties["q"].Type = "number"

	again := agt.Tools()
	if again[0].Name != "echo" || again[0].Description != "d" ||
		again[0].InputSchema.Properties["q"].Type != "string" {
		t.Errorf("mutating Tools() reached the agent: %+v", again[0])
	}
}

// Concurrent runs with a caller mutating its own def pointer stay
// race-clean: the agent's frozen copy shares no memory with it. Under
// -race this is a tripwire for removing the registration-time clone.
func TestCallerMutationDuringRunIsRaceClean(t *testing.T) {
	def := weft.Tool("echo", "d", func(_ context.Context, in struct {
		Q string `json:"q"`
	}) (string, error) {
		return in.Q, nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"q":"x"}`}),
		wefttest.Say("done"),
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"q":"x"}`}),
		wefttest.Say("done"),
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"q":"x"}`}),
		wefttest.Say("done"),
	), def)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			def.Description = fmt.Sprintf("spin %d", i)
			def.InputSchema.Properties["q"].Description = fmt.Sprintf("spin %d", i)
		}
	}()
	for i := 0; i < 3; i++ {
		if _, err := agt.Generate(context.Background(), weft.Prompt("go")); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	<-done
}

// A step consults its ToolSource exactly once: advertising and dispatch
// resolve against the same snapshot, so a source that drops a tool
// mid-run cannot produce the advertised-then-NO_SUCH_TOOL failure, and
// a source that allocates is not hammered per call (Fix 5).
func TestToolSourceSnapshotConsistency(t *testing.T) {
	var fetches atomic.Int64
	echo := weft.Tool("echo", "", func(_ context.Context, in struct {
		Q string `json:"q"`
	}) (string, error) {
		return "ok:" + in.Q, nil
	})
	agt := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"q":"hi"}`}),
			wefttest.Say("done"),
		),
		weft.ToolSource(func() []*weft.ToolDef {
			if fetches.Add(1) > 1 {
				return nil // the registry drops echo after step 0
			}
			return []*weft.ToolDef{echo}
		}),
	)
	res, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Steps[0].Results[0]; r.IsError || r.Content != "ok:hi" {
		t.Errorf("result = %+v; dispatch must resolve against the step's snapshot, not a refetch", r)
	}
	if got := fetches.Load(); got != 2 {
		t.Errorf("source consulted %d times, want exactly once per step (2 steps)", got)
	}
}

// The sequential barrier decision matches the executed def: a source
// whose sequential flag flips between fetches cannot make the barrier
// check and the execution disagree (Fix 5).
func TestToolSourceSequentialBarrierMatchesSnapshot(t *testing.T) {
	var fetches atomic.Int64
	slow := weft.Tool("slow", "", func(_ context.Context, _ struct{}) (string, error) {
		time.Sleep(10 * time.Millisecond) // outlast the sibling's dispatch
		return "s", nil
	})
	lonely := func(seq bool) *weft.ToolDef {
		opts := []weft.ToolOption{}
		if seq {
			opts = append(opts, weft.Sequential())
		}
		return weft.Tool("lonely", "", func(_ context.Context, _ struct{}) (string, error) {
			return "l", nil
		}, opts...)
	}
	agt := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(
				wefttest.Call{Name: "slow"},
				wefttest.Call{Name: "lonely"},
			),
			wefttest.Say("done"),
		),
		weft.ToolSource(func() []*weft.ToolDef {
			if fetches.Add(1) == 1 {
				return []*weft.ToolDef{slow, lonely(true)}
			}
			return []*weft.ToolDef{slow, lonely(false)}
		}),
	)
	var order []string
	for ev, err := range agt.Stream(context.Background(), weft.Prompt("go")).Events() {
		if err != nil {
			t.Fatal(err)
		}
		switch e := ev.(type) {
		case weft.ToolStart:
			order = append(order, ">"+e.Name)
		case weft.ToolFinish:
			order = append(order, "<"+e.Name)
		}
	}
	want := []string{">slow", "<slow", ">lonely", "<lonely"}
	if !slices.Equal(order, want) {
		t.Errorf("event order = %v, want %v (barrier must match the snapshot's sequential flag)", order, want)
	}
}

// A ToolSource snapshot with a duplicate name fails the run loudly —
// the runtime analogue of New's duplicate-name panic — instead of
// silently resolving first-wins (Fix 9).
func TestToolSourceDuplicateFailsRun(t *testing.T) {
	dup := func(tag string) *weft.ToolDef {
		return weft.Tool("dup", tag, func(_ context.Context, _ struct{}) (string, error) {
			return tag, nil
		})
	}
	agt := weft.New(
		wefttest.Script(wefttest.Say("never reached")),
		weft.ToolSource(func() []*weft.ToolDef {
			return []*weft.ToolDef{dup("a"), dup("b")}
		}),
	)
	_, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if !errors.Is(err, weft.ErrDuplicateTool) {
		t.Errorf("run error = %v, want ErrDuplicateTool", err)
	}
	if _, err := agt.CallTool(context.Background(), weft.ToolCallPart{Name: "dup"}); !errors.Is(err, weft.ErrDuplicateTool) {
		t.Errorf("CallTool error = %v, want ErrDuplicateTool", err)
	}
}

// A ToolSource snapshot with a nil entry fails the run with ErrNilTool
// — a malformed snapshot is a run error, not a silently shortened tool
// list whose advertisement would dereference nil inside every adapter.
func TestToolSourceNilEntryFailsRun(t *testing.T) {
	tool := weft.Tool("real", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ran", nil
	})
	agt := weft.New(
		wefttest.Script(wefttest.Say("never reached")),
		weft.ToolSource(func() []*weft.ToolDef {
			return []*weft.ToolDef{tool, nil}
		}),
	)
	_, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if !errors.Is(err, weft.ErrNilTool) {
		t.Errorf("run error = %v, want ErrNilTool", err)
	}
	if _, err := agt.CallTool(context.Background(), weft.ToolCallPart{Name: "real"}); !errors.Is(err, weft.ErrNilTool) {
		t.Errorf("CallTool error = %v, want ErrNilTool", err)
	}
}

// Tool arguments must be exactly one JSON value — the loop-level pin
// of ADR 0002's 2026-09-18 amendment (the decode-level table lives in
// tooloption_test.go): a scripted call with trailing garbage after the
// arguments becomes an INVALID_INPUT result the model sees, not a
// decoded prefix. Siblings keep running.
func TestTrailingArgumentDataIsInvalidInput(t *testing.T) {
	tool := weft.Tool("t", "", func(_ context.Context, _ struct {
		N int `json:"n"`
	}) (string, error) {
		return "ran", nil
	})
	agt := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "t", Args: `{"n":1} {"n":2}`}),
			wefttest.Say("ok"),
		),
		tool,
	)
	res, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	r := res.Steps[0].Results[0]
	if !r.IsError || !strings.Contains(r.Content, "INVALID_INPUT") || !strings.Contains(r.Content, "trailing data") {
		t.Errorf("result = %+v, want an INVALID_INPUT trailing-data error the model sees", r)
	}
}

// Approval-resume results complete the earlier step's tool message in
// the assistant's call order (ADR 0007 §3), not appended after the
// earlier run's results — Gemini matches functionResponses by name and
// position, so a reordered tool message can attach a result to the
// wrong call.
func TestApprovalResumeCompletesInCallOrder(t *testing.T) {
	park := weft.Tool("park", "", func(_ context.Context, _ struct{}) (string, error) {
		return "parked-ran", nil
	}, weft.RequireApproval())
	plain := weft.Tool("plain", "", func(_ context.Context, _ struct{}) (string, error) {
		return "plain-ran", nil
	})
	agt := weft.New(
		wefttest.Script(wefttest.ToolCalls(
			wefttest.Call{ID: "p1", Name: "park"},
			wefttest.Call{ID: "c1", Name: "plain"},
		), wefttest.Say("done")),
		park, plain,
	)
	res, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	res2, err := agt.Generate(context.Background(),
		weft.Messages(res.Messages...), weft.Approve("p1"), weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	// The completed tool message: p1's fresh result, then c1's earlier
	// one — the assistant's order.
	var contents []string
	for _, msg := range res2.Messages {
		if msg.Role != weft.RoleTool {
			continue
		}
		for _, p := range msg.Content {
			if r, ok := p.(weft.ToolResultPart); ok {
				contents = append(contents, r.CallID)
			}
		}
	}
	if strings.Join(contents, ",") != "p1,c1" {
		t.Errorf("completed tool message order = %v, want [p1 c1] (the assistant's call order)", contents)
	}
}

// hostileModel tries to corrupt the run through the request: appending
// to Messages and Tools, reassigning tool elements. The loop hands each
// request fresh slice copies, so none of it reaches the run, the
// transcript, or the agent (Fix 2).
type hostileModel struct{ inner weft.Model }

func (h hostileModel) Info() weft.ModelInfo { return weft.InfoOf(h.inner) }

func (h hostileModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	req.Messages = append(req.Messages, weft.User("injected"))
	req.Tools = append(req.Tools, nil)
	req.Tools[0] = nil
	return h.inner.Stream(ctx, req)
}

func TestModelRequestCopiesDefendTheRun(t *testing.T) {
	echo := weft.Tool("echo", "", func(_ context.Context, in struct {
		Q string `json:"q"`
	}) (string, error) {
		return in.Q, nil
	})
	agt := weft.New(hostileModel{wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"q":"hi"}`}),
		wefttest.Say("done"),
	)}, echo)
	res, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Messages {
		if m.Role == weft.RoleUser && m.Text() == "injected" {
			t.Error("hostile model injected a message into the run transcript")
		}
	}
	if r := res.Steps[0].Results[0]; r.IsError || r.Content != "hi" {
		t.Errorf("result = %+v; tool-list sabotage must not reach dispatch", r)
	}
	if tools := agt.Tools(); len(tools) != 1 || tools[0].Name != "echo" {
		t.Errorf("agent tool list = %+v; sabotage must not reach the agent", tools)
	}
}

// A PrepareStep function receives the request as a copy it may mutate
// freely: in-place writes — rewriting a transcript part, a tool call's
// raw argument bytes, a definition's fields — must reach neither the
// run's transcript nor the agent's frozen registry, in this run or any
// later one (ADR 0006 amendment).
func TestPrepareStepCopiesDefendTheRun(t *testing.T) {
	echo := weft.Tool("echo", "", func(_ context.Context, in struct {
		Q string `json:"q"`
	}) (string, error) {
		return in.Q, nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"q":"hi"}`}),
		wefttest.Say("done"),
	), echo, weft.PrepareStep(func(_ context.Context, _ int, req weft.ModelRequest) (weft.ModelRequest, error) {
		req.Tools[0].Description = "sabotaged"
		req.Messages[0].Content[0] = weft.TextPart{Text: "injected"}
		for i := range req.Messages {
			if c, ok := req.Messages[i].Content[0].(weft.ToolCallPart); ok {
				c.Args = json.RawMessage(`{"q":"hacked"}`)
				req.Messages[i].Content[0] = c
			}
		}
		return req, nil
	}))
	res, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Steps[0].Results[0]; r.IsError || r.Content != "hi" {
		t.Errorf("result = %+v; dispatch must see the step's own snapshot", r)
	}
	if got := res.Messages[0].Text(); got != "go" {
		t.Errorf("first message = %q; transcript parts must be copies", got)
	}
	var args string
	for _, m := range res.Messages {
		for _, p := range m.Content {
			if c, ok := p.(weft.ToolCallPart); ok {
				args = string(c.Args)
			}
		}
	}
	if args != `{"q":"hi"}` {
		t.Errorf("recorded call args = %q; argument bytes must be copies", args)
	}
	if tools := agt.Tools(); tools[0].Description != "" {
		t.Errorf("tool description = %q; sabotage must not reach the frozen registry", tools[0].Description)
	}
}

// stepModel is stateless and thread-safe: the first call of any run asks
// for a tool, every later one answers. Run-scoped behaviour from request
// shape alone, so one Model can serve concurrent runs on one agent.
type stepModel struct{}

func (stepModel) Info() weft.ModelInfo { return weft.ModelInfo{Provider: "wefttest", Name: "step"} }

func (stepModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	answered := false
	for _, m := range req.Messages {
		if m.Role == weft.RoleAssistant {
			answered = true
		}
	}
	return func(yield func(weft.ModelEvent, error) bool) {
		if answered {
			yield(weft.ModelTextDelta{Text: "done"}, nil)
			yield(weft.ModelFinish{Reason: weft.StopEndTurn}, nil)
			return
		}
		yield(weft.ModelToolCall{ID: "c1", Name: "ping", Args: json.RawMessage(`{}`)}, nil)
		yield(weft.ModelFinish{Reason: weft.StopToolCalls}, nil)
	}
}

func eventRunID(ev weft.Event) string {
	switch e := ev.(type) {
	case weft.RunStart:
		return e.ID
	case weft.StepStart:
		return e.RunID
	case weft.TextDelta:
		return e.RunID
	case weft.ReasoningDelta:
		return e.RunID
	case weft.ToolArgsDelta:
		return e.RunID
	case weft.ToolStart:
		return e.RunID
	case weft.ToolFinish:
		return e.RunID
	case weft.StepFinish:
		return e.RunID
	case weft.RunFinish:
		return e.RunID
	}
	return "?"
}

// Every event carries its run's id, so a tap watching concurrent runs on
// one agent can attribute each event — per-run Seq counters are unique
// only within their run (Fix 6).
func TestEventsCarryRunIDUnderConcurrency(t *testing.T) {
	ping := weft.Tool("ping", "", func(_ context.Context, _ struct{}) (string, error) {
		return "pong", nil
	})
	var mu sync.Mutex
	buckets := map[string][]weft.Event{}
	agt := weft.New(stepModel{}, ping,
		weft.Tap(func(_ context.Context, ev weft.Event) {
			mu.Lock()
			defer mu.Unlock()
			buckets[eventRunID(ev)] = append(buckets[eventRunID(ev)], ev)
		}),
	)
	var wg sync.WaitGroup
	for _, id := range []string{"r1", "r2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := agt.Generate(context.Background(), weft.Prompt("go"), weft.RunID(id)); err != nil {
				t.Error(err)
			}
		}(id)
	}
	wg.Wait()

	for _, id := range []string{"r1", "r2"} {
		evs := buckets[id]
		if len(evs) == 0 {
			t.Fatalf("no events attributed to %s", id)
		}
		if _, ok := evs[0].(weft.RunStart); !ok {
			t.Errorf("%s: first event is %T, want RunStart", id, evs[0])
		}
		if _, ok := evs[len(evs)-1].(weft.RunFinish); !ok {
			t.Errorf("%s: last event is %T, want RunFinish", id, evs[len(evs)-1])
		}
		var seqs []int64
		for _, ev := range evs {
			if got := eventRunID(ev); got != id {
				t.Errorf("%s: event %T attributed to %q", id, ev, got)
			}
			switch e := ev.(type) {
			case weft.ToolStart:
				seqs = append(seqs, e.Seq)
			case weft.ToolFinish:
				seqs = append(seqs, e.Seq)
			}
		}
		if !slices.Equal(seqs, []int64{1, 2}) {
			t.Errorf("%s: tool event seqs = %v, want [1 2] within the run", id, seqs)
		}
	}

	// Legacy recordings without run_id still decode (RunID is additive).
	ev, err := weft.UnmarshalEvent([]byte(`{"type":"step_start","index":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if ss := ev.(weft.StepStart); ss.Index != 3 || ss.RunID != "" {
		t.Errorf("legacy step_start = %+v, want index 3 and empty RunID", ss)
	}
}

// Events are snapshots: writing into a received ToolStart's Args must
// not corrupt the transcript the run is building (Fix 4, option b).
func TestEventArgsAreSnapshots(t *testing.T) {
	echo := weft.Tool("echo", "", func(_ context.Context, in struct {
		Q string `json:"q"`
	}) (string, error) {
		return in.Q, nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"q":"hi"}`}),
		wefttest.Say("done"),
	), echo)
	run := agt.Stream(context.Background(), weft.Prompt("go"))
	for ev, err := range run.Events() {
		if err != nil {
			t.Fatal(err)
		}
		if ts, ok := ev.(weft.ToolStart); ok {
			copy(ts.Args, []byte(`ZZ`)) // in-place write into the event's copy
		}
	}
	res, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Messages {
		for _, p := range m.Content {
			if c, ok := p.(weft.ToolCallPart); ok && string(c.Args) != `{"q":"hi"}` {
				t.Errorf("transcript args = %s; event mutation leaked into the transcript", c.Args)
			}
		}
	}
}

// RunFinish.Pending is a snapshot too: writing into the event's pending
// args must not reach the run result or the transcript (Fix 4, option b).
func TestRunFinishPendingArgsAreSnapshots(t *testing.T) {
	gate := weft.Tool("gate", "", func(_ context.Context, _ struct{}) (string, error) {
		return "never", nil
	}, weft.RequireApproval())
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "gate", Args: `{"secret":"1"}`}),
	), gate)
	run := agt.Stream(context.Background(), weft.Prompt("go"))
	for ev, err := range run.Events() {
		if err != nil {
			t.Fatal(err)
		}
		if rf, ok := ev.(weft.RunFinish); ok && len(rf.Pending) > 0 {
			copy(rf.Pending[0].Args, []byte(`XX`))
		}
	}
	res, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Pending[0].Args) != `{"secret":"1"}` {
		t.Errorf("res.Pending args = %s; event mutation leaked into the run result", res.Pending[0].Args)
	}
	for _, m := range res.Messages {
		for _, p := range m.Content {
			if c, ok := p.(weft.ToolCallPart); ok && string(c.Args) != `{"secret":"1"}` {
				t.Errorf("transcript args = %s; event mutation leaked into the transcript", c.Args)
			}
		}
	}
}

// Closing an abandoned run releases it: the cancel handle Stream
// created is freed without ever consuming events, and a later
// consumption reports the cancellation. Close after completion is a
// no-op (Fix 11).
func TestRunCloseAbandoned(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	agt := weft.New(wefttest.Script(wefttest.Say("never consumed")),
		weft.Tool("noop", "", func(_ context.Context, _ struct{}) (string, error) {
			return "", nil
		}))
	run := agt.Stream(parent, weft.Prompt("go"))
	run.Close() // abandoned without consuming
	run.Close() // idempotent

	// The canceled run still consumes its single Events sequence
	// cleanly, and must actually deliver the cancellation — a silent,
	// error-free stream would mean Close released nothing.
	sawErr := false
	for _, err := range run.Events() {
		if err == nil {
			continue
		}
		sawErr = true
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error after Close = %v, want context.Canceled", err)
		}
		break
	}
	if !sawErr {
		t.Error("Events after Close delivered no error; Close must cancel the run")
	}

	// Close after a completed run is a no-op.
	done := agt.Stream(context.Background(), weft.Prompt("go"))
	if _, err := done.Wait(); err != nil {
		t.Fatal(err)
	}
	done.Close()
}

// A panicking tap is contained and counted: the run completes, and
// TapPanics reports exactly the number of matching invocations (Fix 12).
func TestTapPanicsContainedAndCounted(t *testing.T) {
	agt := weft.New(
		wefttest.Script(wefttest.Say("hello")),
		weft.Tap(func(_ context.Context, ev weft.Event) {
			if _, ok := ev.(weft.TextDelta); ok {
				panic("observer bug")
			}
		}),
	)
	if got := agt.TapPanics(); got != 0 {
		t.Fatalf("TapPanics before any run = %d, want 0", got)
	}
	res, err := agt.Generate(context.Background(), weft.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "hello" {
		t.Errorf("run text = %q; a broken tap must not break the run", res.Text())
	}
	if got := agt.TapPanics(); got != 1 {
		t.Errorf("TapPanics = %d, want 1 (one TextDelta panicked)", got)
	}
}

// Repair never writes into the caller's memory — including the spare
// capacity of append-built parts slices. The second window over the
// same backing array stays untouched even when Repair synthesizes a
// result onto a kept tool message (Fix 3 hardening: purity must not
// depend on whose backing array the kept Content uses).
func TestRepairNeverWritesIntoCallerMemory(t *testing.T) {
	parts := make([]weft.Part, 3, 8) // deliberate spare capacity
	parts[0] = weft.ToolResultPart{CallID: "a", Name: "t", Content: "ok"}
	parts[1] = weft.ToolResultPart{CallID: "b", Name: "t", Content: "ok"}
	parts[2] = weft.ToolResultPart{CallID: "c", Name: "t", Content: "ok"}
	spare := parts[:8] // a second window onto the whole array
	in := []weft.Message{
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ToolCallPart{ID: "a", Name: "t", Args: json.RawMessage(`{}`)},
			weft.ToolCallPart{ID: "b", Name: "t", Args: json.RawMessage(`{}`)},
			weft.ToolCallPart{ID: "c", Name: "t", Args: json.RawMessage(`{}`)},
			weft.ToolCallPart{ID: "d", Name: "t", Args: json.RawMessage(`{}`)}, // missing result
		}},
		{Role: weft.RoleTool, Content: parts},
	}
	out := weft.Repair(in)
	if len(out) < 2 || out[1].Role != weft.RoleTool {
		t.Fatalf("repair shape = %+v, want the kept tool message", out)
	}
	var served []string
	for _, p := range out[1].Content {
		if r, ok := p.(weft.ToolResultPart); ok {
			served = append(served, r.CallID)
		}
	}
	if !slices.Equal(served, []string{"a", "b", "c", "d"}) {
		t.Errorf("repaired results = %v, want a b c d", served)
	}
	for i, p := range spare[3:] {
		if p != nil {
			t.Errorf("repair wrote into the caller's spare capacity at %d: %v", i+3, p)
		}
	}
	if got := parts[0]; got == nil {
		t.Error("input parts disturbed")
	}
}

// --- Subagents as tools (TODO §5.1, ADR 0014) ---

// streamEvents runs agt to completion, returning every event in
// emission order.
func streamEvents(t *testing.T, agt *weft.Agent, opts ...weft.RunOption) ([]weft.Event, *weft.RunResult) {
	t.Helper()
	run := agt.Stream(context.Background(), opts...)
	var evs []weft.Event
	for ev, err := range run.Events() {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		evs = append(evs, ev)
	}
	res, err := run.Wait()
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	return evs, res
}

// A subagent is an ordinary tool: it registers with New, dispatches
// through the chain, and every ToolOption applies to it.
func TestSubagentIsAnOrdinaryTool(t *testing.T) {
	child := weft.New(wefttest.Script(wefttest.Say("found: 3 orders")))
	def := weft.Subagent("research", "Research a topic.", child, weft.Timeout(30*time.Second))
	if def.Name != "research" {
		t.Fatalf("tool name = %q", def.Name)
	}
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"find orders"}`}),
		wefttest.Say("done"),
	), def)
	if _, ok := agt.Tools()[0].InputSchema.Properties["prompt"]; !ok {
		t.Error("the subagent tool did not register with New")
	}
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Steps[0].Results[0]; r.IsError || r.Content != "found: 3 orders" {
		t.Errorf("result = %+v, want the child's final text", r)
	}
	// The per-tool option reached the manifest, as for any tool.
	b, err := weft.Manifest(weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("a"), def))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"timeout": "30s"`) {
		t.Error("per-subagent Timeout not recorded in the manifest")
	}
}

// The input schema is exactly one required prompt string; the output is
// the child's final text (a string Out, so no output schema).
func TestSubagentSchema(t *testing.T) {
	child := weft.New(wefttest.Script(wefttest.Say("ok")))
	def := weft.Subagent("research", "Research a topic.", child)
	b, err := json.Marshal(def.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"object","properties":{"prompt":{"type":"string","description":"The task, stated in full: the agent sees only this prompt, not the conversation."}},"required":["prompt"]}`
	if string(b) != want {
		t.Errorf("input schema:\n got  %s\n want %s", b, want)
	}
	if def.OutputSchema != nil {
		t.Errorf("output schema = %v, want none (string Out is verbatim)", def.OutputSchema)
	}
}

// The child's events arrive wrapped in Nested, numbered from the
// parent's counter, bracketed by the delegating call's ToolStart and
// ToolFinish, and never after the finish (the late-event rule).
func TestSubagentNestedEventsAreOrdered(t *testing.T) {
	child := weft.New(wefttest.Script(wefttest.Say("found it")))
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"p"}`}),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child))
	evs, _ := streamEvents(t, parent, weft.Prompt("q"), weft.RunID("r1"))

	var last int64
	var nested []weft.Event
	finishIdx := -1
	for i, ev := range evs {
		switch e := ev.(type) {
		case weft.ToolStart:
			if e.Seq <= last {
				t.Fatalf("ToolStart Seq %d not after %d", e.Seq, last)
			}
			last = e.Seq
		case weft.ToolFinish:
			if e.Seq <= last {
				t.Fatalf("ToolFinish Seq %d not after %d", e.Seq, last)
			}
			last = e.Seq
			finishIdx = i
			if e.Content != "found it" {
				t.Errorf("finish content = %q", e.Content)
			}
		case weft.Nested:
			if e.Seq <= last {
				t.Fatalf("Nested Seq %d not after %d", e.Seq, last)
			}
			last = e.Seq
			if e.RunID != "r1" || e.CallID != "call_1" {
				t.Errorf("nested envelope = %+v", e)
			}
			nested = append(nested, e.Event)
		}
	}
	if finishIdx < 0 {
		t.Fatal("the delegating call never finished")
	}
	for _, ev := range evs[finishIdx+1:] {
		if n, ok := ev.(weft.Nested); ok && n.CallID == "call_1" {
			t.Errorf("Nested delivered after the call's ToolFinish: %+v", n)
		}
	}
	kinds := make([]string, len(nested))
	for i, ev := range nested {
		kinds[i] = fmt.Sprintf("%T", ev)
	}
	want := []string{"weft.RunStart", "weft.StepStart", "weft.TextDelta", "weft.StepFinish", "weft.RunFinish"}
	if !slices.Equal(kinds, want) {
		t.Errorf("child event kinds = %v, want %v", kinds, want)
	}
}

// Nested round-trips the wire byte-exactly, recursing into the inner
// event; an unknown inner type is an error, never a drop.
func TestNestedEventRoundTrip(t *testing.T) {
	outer := weft.Nested{RunID: "r1", Seq: 9, CallID: "c1",
		Event: weft.Nested{RunID: "r1/0/c1", Seq: 3, CallID: "call_1",
			Event: weft.ToolStart{RunID: "r1/0/c1/0/call_1", Seq: 1, CallID: "p1", Name: "deep_search", Args: json.RawMessage(`{}`)}}}
	b, err := json.Marshal(outer)
	if err != nil {
		t.Fatal(err)
	}
	back, err := weft.UnmarshalEvent(b)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := json.Marshal(back)
	if err != nil || string(b2) != string(b) {
		t.Errorf("round trip unstable:\n first %s\n second %s (%v)", b, b2, err)
	}
	if _, err := weft.UnmarshalEvent([]byte(`{"type":"nested","run_id":"r1","seq":1,"call_id":"c","event":{"type":"nope"}}`)); err == nil {
		t.Error("an unknown inner event type decoded without error")
	}
}

// Child usage is added to the run total and recorded per call;
// StepRecord.Usage and StepFinish.Usage stay the model call's own.
func TestSubagentUsageRollsUp(t *testing.T) {
	child := weft.New(wefttest.Script(wefttest.Say("ok")))
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child))
	evs, res := streamEvents(t, parent, weft.Prompt("q"))
	// 2 parent steps × (10/5) + 1 child step × (10/5).
	wantTotal := weft.Usage{InputTokens: 30, OutputTokens: 15}
	if res.Usage != wantTotal {
		t.Errorf("RunResult.Usage = %+v, want %+v", res.Usage, wantTotal)
	}
	step := res.Steps[0]
	if step.Usage != (weft.Usage{InputTokens: 10, OutputTokens: 5}) {
		t.Errorf("StepRecord.Usage = %+v, want the model call's own", step.Usage)
	}
	if got := step.SubagentUsage["call_1"]; got != (weft.Usage{InputTokens: 10, OutputTokens: 5}) {
		t.Errorf("SubagentUsage[call_1] = %+v", got)
	}
	if res.Steps[1].SubagentUsage != nil {
		t.Errorf("step 1 SubagentUsage = %v, want nil (no subagent ran)", res.Steps[1].SubagentUsage)
	}
	var sf weft.StepFinish
	for _, ev := range evs {
		if e, ok := ev.(weft.StepFinish); ok && e.Index == 0 {
			sf = e
		}
	}
	if sf.Usage != (weft.Usage{InputTokens: 10, OutputTokens: 5}) {
		t.Errorf("StepFinish.Usage = %+v, want the model call's own", sf.Usage)
	}
}

// A failed child is data: SUBAGENT_FAILED reaches the model, the parent
// run continues, and the child's *RunError is reachable through
// ToolError.Err for middleware.
func TestSubagentFailureIsData(t *testing.T) {
	boomed := errors.New("provider down")
	child := weft.New(wefttest.Script(wefttest.Fail(boomed)))
	var childErr *weft.RunError
	spy := func(next weft.ToolCaller) weft.ToolCaller {
		return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
			out, err := next(ctx, call)
			var te *weft.ToolError
			if errors.As(err, &te) {
				errors.As(te.Err, &childErr)
			}
			return out, err
		}
	}
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("recovered"),
	), weft.Subagent("research", "Research.", child), weft.WrapTools(spy))
	res, err := parent.Generate(context.Background(), weft.Prompt("q"))
	if err != nil {
		t.Fatalf("a failed child must not fail the parent: %v", err)
	}
	r := res.Steps[0].Results[0]
	if !r.IsError {
		t.Fatal("the child failure was not an error result")
	}
	want := `SUBAGENT_FAILED: agent "research" failed at step 0: model stream: provider down`
	if r.Content != want {
		t.Errorf("content = %q, want %q", r.Content, want)
	}
	if childErr == nil || !errors.Is(childErr.Err, boomed) || childErr.Step != 0 {
		t.Errorf("ToolError.Err did not reach the child *RunError: %+v", childErr)
	}
}

// Parent cancellation cancels the child at its next ctx check.
func TestSubagentFollowsParentCancellation(t *testing.T) {
	started := make(chan struct{})
	childCancelled := make(chan struct{})
	probe := weft.Tool("probe", "", func(ctx context.Context, _ struct{}) (string, error) {
		close(started)
		<-ctx.Done()
		close(childCancelled)
		return "never", nil
	})
	child := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "probe"}),
		wefttest.Say("ok"),
	), probe)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err := parent.Generate(ctx, weft.Prompt("q"))
	var re *weft.RunError
	if !errors.As(err, &re) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want a RunError wrapping context.Canceled", err)
	}
	select {
	case <-childCancelled:
	default:
		t.Error("the child was not cancelled with the parent")
	}
}

// A Timeout on the subagent tool bounds the child run; the ordinary
// timeout result is recorded, and no Nested event for the call is
// delivered after its ToolFinish.
func TestSubagentTimeoutClosesNesting(t *testing.T) {
	probe := weft.Tool("probe", "", func(ctx context.Context, _ struct{}) (string, error) {
		<-ctx.Done() // honours the deadline
		return "late", nil
	})
	child := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "probe"}),
		wefttest.Say("ok"),
	), probe)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child, weft.Timeout(50*time.Millisecond)))
	evs, _ := streamEvents(t, parent, weft.Prompt("q"))
	finishIdx, sawNested := -1, false
	for i, ev := range evs {
		switch e := ev.(type) {
		case weft.Nested:
			if e.CallID == "call_1" {
				sawNested = true
			}
		case weft.ToolFinish:
			finishIdx = i
			if want := `tool "research" timed out after 50ms`; e.Content != want {
				t.Errorf("content = %q, want %q", e.Content, want)
			}
		}
	}
	if finishIdx < 0 || !sawNested {
		t.Fatalf("finishIdx = %d, sawNested = %v", finishIdx, sawNested)
	}
	for _, ev := range evs[finishIdx+1:] {
		if n, ok := ev.(weft.Nested); ok && n.CallID == "call_1" {
			t.Errorf("Nested delivered after the timed-out finish: %+v", n)
		}
	}
}

// A child that ends pending is a loud tool error: the parent's
// transcript has nowhere to carry the child's approval decision.
func TestSubagentPendingIsLoud(t *testing.T) {
	pay := weft.Tool("pay", "", func(_ context.Context, _ struct{}) (string, error) {
		return "paid", nil
	}, weft.RequireApproval())
	child := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "pay"}),
		wefttest.Say("never reached"),
	), pay)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("recovered"),
	), weft.Subagent("research", "Research.", child))
	res, err := parent.Generate(context.Background(), weft.Prompt("q"))
	if err != nil {
		t.Fatal(err)
	}
	r := res.Steps[0].Results[0]
	want := `SUBAGENT_PENDING: agent "research" ended awaiting approval of 1 call(s)`
	if !r.IsError || r.Content != want {
		t.Errorf("result = %+v, want %q", r, want)
	}
}

// A delegation to an agent already running in the call chain is refused
// before any model call — the inner agents' scripts would be exhausted
// otherwise.
func TestSubagentCycleIsRefused(t *testing.T) {
	aModel := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "use_b"}),
		wefttest.Say("done"),
	)
	bModel := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "use_a"}),
		wefttest.Say("b done"),
	)
	var a *weft.Agent
	b := weft.New(bModel, weft.ToolSource(func() []*weft.ToolDef {
		return []*weft.ToolDef{weft.Subagent("use_a", "Back to A.", a)}
	}))
	a = weft.New(aModel, weft.Subagent("use_b", "To B.", b))
	evs, res := streamEvents(t, a, weft.Prompt("q"), weft.RunID("r1"))
	if r := res.Steps[0].Results[0]; r.IsError || r.Content != "b done" {
		t.Errorf("outer delegation = %+v", r)
	}
	var cycle string
	for _, ev := range wefttest.Flatten(evs) {
		if f, ok := ev.(weft.ToolFinish); ok && f.Name == "use_a" {
			cycle = f.Content
		}
	}
	want := `SUBAGENT_CYCLE: agent "use_a" is already running in this call chain`
	if cycle != want {
		t.Errorf("cycle result = %q, want %q", cycle, want)
	}
	// No extra model call: both scripts were consumed by exactly their
	// two turns.
	if got := len(aModel.Requests()); got != 2 {
		t.Errorf("A made %d model calls, want 2 (the inner A never ran)", got)
	}
	if got := len(bModel.Requests()); got != 2 {
		t.Errorf("B made %d model calls, want 2", got)
	}
}

// The child's run id is derived from the parent's; CallFromContext
// inside the child reports it.
func TestSubagentLineageIDs(t *testing.T) {
	var childCall weft.Call
	ping := weft.Tool("ping", "", func(ctx context.Context, _ struct{}) (string, error) {
		childCall, _ = weft.CallFromContext(ctx)
		return "pong", nil
	})
	child := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "ping"}),
		wefttest.Say("ok"),
	), ping)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", ID: "call_9"}),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child))
	evs, _ := streamEvents(t, parent, weft.Prompt("q"), weft.RunID("r1"))
	var ids []string
	for _, ev := range wefttest.Flatten(evs) {
		if rs, ok := ev.(weft.RunStart); ok {
			ids = append(ids, rs.ID)
		}
	}
	if !slices.Equal(ids, []string{"r1", "r1/0/call_9"}) {
		t.Errorf("RunStart ids = %v", ids)
	}
	if childCall.RunID != "r1/0/call_9" || childCall.Step != 0 || childCall.Name != "ping" {
		t.Errorf("CallFromContext inside the child = %+v", childCall)
	}
}

// A delegation resumed under Approve reports the resume lineage id,
// <parent>/resume/<callID> — distinct from any step-0 call, which
// childRunID's literal segment guarantees (ADR 0014).
func TestSubagentResumeLineageID(t *testing.T) {
	var childCall weft.Call
	ping := weft.Tool("ping", "", func(ctx context.Context, _ struct{}) (string, error) {
		childCall, _ = weft.CallFromContext(ctx)
		return "pong", nil
	})
	child := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "ping"}),
		wefttest.Say("ok"),
	), ping)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", ID: "call_9"}),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child, weft.RequireApproval()))
	res, err := parent.Generate(context.Background(), weft.Prompt("q"), weft.RunID("r1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 || res.Pending[0].ID != "call_9" {
		t.Fatalf("pending = %+v, want the parked research call", res.Pending)
	}
	evs, _ := streamEvents(t, parent,
		weft.Messages(res.Messages...), weft.Approve("call_9"), weft.RunID("r1"))
	var ids []string
	for _, ev := range wefttest.Flatten(evs) {
		if rs, ok := ev.(weft.RunStart); ok {
			ids = append(ids, rs.ID)
		}
	}
	if !slices.Equal(ids, []string{"r1", "r1/resume/call_9"}) {
		t.Errorf("RunStart ids = %v, want the resumed child under r1/resume/call_9", ids)
	}
	if childCall.RunID != "r1/resume/call_9" || childCall.Name != "ping" {
		t.Errorf("CallFromContext inside the resumed child = %+v", childCall)
	}
}

// A child built with Output returns the submitted JSON bytes verbatim.
func TestSubagentTypedOutput(t *testing.T) {
	type Verdict struct {
		Approved bool   `json:"approved"`
		Reason   string `json:"reason"`
	}
	child := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "submit_output", Args: `{"approved":true,"reason":"ok"}`}),
	), weft.Output[Verdict]())
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child))
	res, err := parent.Generate(context.Background(), weft.Prompt("q"))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Steps[0].Results[0].Content; got != `{"approved":true,"reason":"ok"}` {
		t.Errorf("typed delegation = %q, want the submitted bytes verbatim", got)
	}
}

// A child that submits with no argument bytes at all — decodeInput
// reads empty as {} — has still submitted: the delegation returns the
// submitted bytes (empty), not the child's final text, exactly what
// OutputOf would decode (ADR 0014). wefttest defaults empty call args
// to {}, so the empty-byte turn is scripted raw.
func TestSubagentTypedOutputEmptySubmission(t *testing.T) {
	type Verdict struct {
		Approved bool `json:"approved"`
	}
	child := weft.New(&turnModel{turns: [][]weft.ModelEvent{{
		weft.ModelTextDelta{Text: "submitting empty"},
		weft.ModelToolCall{ID: "call_1", Name: "submit_output"},
		weft.ModelFinish{Reason: weft.StopToolCalls, Usage: weft.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}, weft.Output[Verdict]())
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child))
	res, err := parent.Generate(context.Background(), weft.Prompt("q"))
	if err != nil {
		t.Fatal(err)
	}
	r := res.Steps[0].Results[0]
	if r.IsError {
		t.Fatalf("delegation failed: %s", r.Content)
	}
	if r.Content != "" {
		t.Errorf("typed delegation = %q, want the empty submitted bytes, not the child's final text", r.Content)
	}
}

// The child's taps see its raw events; the parent's taps see the Nested
// wrappers — never the child's events bare.
func TestSubagentTapsSeeTheirOwnLevel(t *testing.T) {
	var childSeen, parentSeen []weft.Event
	child := weft.New(wefttest.Script(wefttest.Say("hi")),
		weft.Tap(func(_ context.Context, ev weft.Event) { childSeen = append(childSeen, ev) }))
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child),
		weft.Tap(func(_ context.Context, ev weft.Event) { parentSeen = append(parentSeen, ev) }))
	if _, err := parent.Generate(context.Background(), weft.Prompt("q"), weft.RunID("p1")); err != nil {
		t.Fatal(err)
	}
	sawText := false
	for _, ev := range childSeen {
		if _, ok := ev.(weft.Nested); ok {
			t.Error("the child's tap saw a Nested wrapper")
		}
		if _, ok := ev.(weft.TextDelta); ok {
			sawText = true
		}
	}
	if !sawText {
		t.Error("the child's tap saw no raw TextDelta")
	}
	sawNested := false
	for _, ev := range parentSeen {
		if td, ok := ev.(weft.TextDelta); ok && td.RunID != "p1" {
			t.Errorf("the parent's tap saw a bare child event: %+v", td)
		}
		if _, ok := ev.(weft.Nested); ok {
			sawNested = true
		}
	}
	if !sawNested {
		t.Error("the parent's tap saw no Nested wrapper")
	}
}

// Outside the loop the child still runs — no emitter, no usage sink, an
// empty ancestry — and returns the same text.
func TestSubagentOutsideTheLoop(t *testing.T) {
	var childEvents []weft.Event
	child := weft.New(wefttest.Script(wefttest.Say("standalone"), wefttest.Say("standalone")),
		weft.Tap(func(_ context.Context, ev weft.Event) { childEvents = append(childEvents, ev) }))
	def := weft.Subagent("research", "Research.", child)
	out, err := def.Invoke(context.Background(), json.RawMessage(`{"prompt":"hi"}`))
	if err != nil || out != "standalone" {
		t.Fatalf("Invoke = %q, %v", out, err)
	}
	agt := weft.New(wefttest.Script(wefttest.Say("x")), def)
	out2, err := agt.CallTool(context.Background(), weft.ToolCallPart{ID: "c1", Name: "research", Args: json.RawMessage(`{"prompt":"hi"}`)})
	if err != nil || out2 != "standalone" {
		t.Fatalf("CallTool = %q, %v", out2, err)
	}
	if len(childEvents) == 0 {
		t.Error("the child did not run")
	}
}

// The manifest names the child on the tool; an unnamed child omits the
// field, and the manifest does not recurse into it.
func TestManifestSubagent(t *testing.T) {
	child := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("researcher"))
	parent := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("orchestrator"),
		weft.Subagent("research", "Research.", child))
	b, err := weft.Manifest(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"subagent": "researcher"`) {
		t.Errorf("manifest missing the delegation edge:\n%s", b)
	}
	anon := weft.New(wefttest.Script(wefttest.Say("ok")))
	parent2 := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("p2"),
		weft.Subagent("research", "Research.", anon))
	b2, err := weft.Manifest(parent2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b2), `"subagent"`) {
		t.Errorf("unnamed child should omit the field:\n%s", b2)
	}
}

func TestSubagentNilChildPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Subagent with a nil child did not panic")
		}
	}()
	weft.Subagent("research", "Research.", nil)
}

// A grandchild event is a Nested inside a Nested, in the child's
// emission order.
func TestSubagentGrandchildDoubleWrap(t *testing.T) {
	c := weft.New(wefttest.Script(wefttest.Say("deep")))
	b := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "grand"}),
		wefttest.Say("mid"),
	), weft.Subagent("grand", "To C.", c))
	a := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "use_b"}),
		wefttest.Say("top"),
	), weft.Subagent("use_b", "To B.", b))
	evs, _ := streamEvents(t, a, weft.Prompt("q"), weft.RunID("r1"))
	var deep bool
	for _, ev := range evs {
		n, ok := ev.(weft.Nested)
		if !ok {
			continue
		}
		inner, ok := n.Event.(weft.Nested)
		if !ok {
			continue
		}
		if n.RunID != "r1" || n.CallID != "call_1" {
			t.Errorf("outer envelope = %+v", n)
		}
		if inner.RunID != "r1/0/call_1" || inner.CallID != "call_1" {
			t.Errorf("inner envelope = %+v", inner)
		}
		if td, ok := inner.Event.(weft.TextDelta); ok && td.Text == "deep" {
			deep = true
		}
	}
	if !deep {
		t.Error("no double-wrapped grandchild TextDelta in the parent stream")
	}
}

// Generate consumes no events, but the child's usage still rolls up.
func TestSubagentUnderGenerateStillRollsUsage(t *testing.T) {
	child := weft.New(wefttest.Script(wefttest.Say("ok")))
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child))
	res, err := parent.Generate(context.Background(), weft.Prompt("q"))
	if err != nil {
		t.Fatal(err)
	}
	if want := (weft.Usage{InputTokens: 30, OutputTokens: 15}); res.Usage != want {
		t.Errorf("usage = %+v, want %+v", res.Usage, want)
	}
}

// Cancelling the parent while a child is mid-run, under Stream: the
// error is the cancellation, it is the last element, and no RunFinish
// (parent or nested) is delivered — rule 4 holds one level down.
func TestSubagentCancelMidChildUnderStream(t *testing.T) {
	started := make(chan struct{})
	probe := weft.Tool("probe", "", func(ctx context.Context, _ struct{}) (string, error) {
		close(started)
		<-ctx.Done()
		return "never", nil
	})
	child := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "probe"}),
		wefttest.Say("ok"),
	), probe)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child))
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	var gotErr error
	for ev, err := range parent.Stream(ctx, weft.Prompt("q")).Events() {
		if err != nil {
			gotErr = err
			continue
		}
		if gotErr != nil {
			t.Fatalf("event delivered after the error: %+v", ev)
		}
		switch e := ev.(type) {
		case weft.RunFinish:
			t.Error("RunFinish delivered on a cancelled run")
		case weft.Nested:
			if _, ok := e.Event.(weft.RunFinish); ok {
				t.Error("nested RunFinish delivered on a cancelled run")
			}
		}
	}
	if !errors.Is(gotErr, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled as the last element", gotErr)
	}
}

// A child's RETRY results feed the child's counter, never the parent's:
// a parent with MaxModelRetries(1) tolerates a child whose tool retried
// three times under the child's own default.
func TestSubagentChildRetriesStayInTheChild(t *testing.T) {
	flaky := weft.Tool("parse_date", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", weft.ModelRetry("date must be ISO-8601")
	})
	turns := []wefttest.Turn{}
	for range 3 {
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "parse_date"}))
	}
	child := weft.New(wefttest.Script(append(turns, wefttest.Say("child done"))...), flaky)
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child), weft.MaxModelRetries(1))
	res, err := parent.Generate(context.Background(), weft.Prompt("q"))
	if err != nil {
		t.Fatalf("the child's retries must not fail the parent: %v", err)
	}
	if r := res.Steps[0].Results[0]; r.IsError || r.Content != "child done" {
		t.Errorf("delegation = %+v", r)
	}
}

// Concurrent runs on one orchestrator: every Nested envelope carries its
// own run's id, and Seq stays monotonic within each run — the parent's
// counter and lock are per run, not per agent.
func TestSubagentConcurrentParentRuns(t *testing.T) {
	child := weft.New(wefttest.Script(
		wefttest.Say("found"), wefttest.Say("found"), wefttest.Say("found"), wefttest.Say("found"),
	))
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}, wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
		wefttest.ToolCalls(wefttest.Call{Name: "research"}, wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child))
	var wg sync.WaitGroup
	for _, id := range []string{"A", "B"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var last int64
			for ev, err := range parent.Stream(context.Background(), weft.Prompt("q"), weft.RunID(id)).Events() {
				if err != nil {
					t.Errorf("%s: %v", id, err)
					return
				}
				var seq int64
				switch e := ev.(type) {
				case weft.Nested:
					if e.RunID != id {
						t.Errorf("Nested.RunID = %q, want %q", e.RunID, id)
					}
					seq = e.Seq
				case weft.ToolStart:
					seq = e.Seq
				case weft.ToolFinish:
					seq = e.Seq
				default:
					continue
				}
				if seq <= last {
					t.Errorf("%s: Seq %d not after %d", id, seq, last)
				}
				last = seq
			}
		}()
	}
	wg.Wait()
}

// --- Usage limits (TODO §5.3) ---

// The limit is checked at the continuation point: a step whose tools
// ran over the budget fails the run before the next model call, with
// the partial transcript ending on the tool message.
func TestUsageLimitFailsBeforeTheNextCall(t *testing.T) {
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("never reached"),
	), echo, weft.UsageLimit(weft.Usage{OutputTokens: 4})) // Say spends 5
	_, err := agt.Generate(context.Background(), weft.Prompt("q"))
	var re *weft.RunError
	if !errors.As(err, &re) || !errors.Is(err, weft.ErrUsageLimit) {
		t.Fatalf("err = %v, want ErrUsageLimit", err)
	}
	if re.Step != 0 {
		t.Errorf("failed at step %d, want 0", re.Step)
	}
	msgs := re.Result.Messages
	if last := msgs[len(msgs)-1]; last.Role != weft.RoleTool {
		t.Errorf("transcript ends on %v, want the tool message", last.Role)
	}
	if re.Result.Steps[0].StopReason != weft.StopToolCalls {
		t.Errorf("last step stop reason = %v", re.Result.Steps[0].StopReason)
	}
}

// A step that ends the run may overshoot the limit and still succeed:
// the budget stops further spend, it does not discard finished work.
// WithUsage lifts the turn off the fixed 10/5 so the overshoot is
// explicit (TODO §9.3).
func TestUsageLimitFinalStepMayOvershoot(t *testing.T) {
	agt := weft.New(wefttest.Script(
		wefttest.Say("done").WithUsage(weft.Usage{OutputTokens: 40})),
		weft.UsageLimit(weft.Usage{OutputTokens: 4}))
	res, err := agt.Generate(context.Background(), weft.Prompt("q"))
	if err != nil {
		t.Fatalf("a run-ending step must not be failed for overshooting: %v", err)
	}
	if res.Usage.OutputTokens != 40 {
		t.Errorf("usage = %+v, want the overshooting 40 output tokens recorded", res.Usage)
	}
}

// Child runs count: the same limit passes a plain tool step and fails
// once a subagent's usage rolls in.
func TestUsageLimitIncludesSubagents(t *testing.T) {
	plain := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	solo := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("done"),
	), plain, weft.UsageLimit(weft.Usage{OutputTokens: 8}))
	if _, err := solo.Generate(context.Background(), weft.Prompt("q")); err != nil {
		t.Fatalf("solo run under the limit failed: %v", err)
	}
	child := weft.New(wefttest.Script(wefttest.Say("found")))
	withSub := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), weft.Subagent("research", "Research.", child), weft.UsageLimit(weft.Usage{OutputTokens: 8}))
	_, err := withSub.Generate(context.Background(), weft.Prompt("q"))
	if !errors.Is(err, weft.ErrUsageLimit) {
		t.Fatalf("err = %v, want ErrUsageLimit once the child's usage rolls in", err)
	}
}

// The manifest records the limit, omitting zero fields.
func TestManifestUsageLimit(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("a"),
		weft.UsageLimit(weft.Usage{OutputTokens: 50_000}))
	b, err := weft.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"usage_limit"`) || !strings.Contains(string(b), `"output_tokens": 50000`) || strings.Contains(string(b), `"input_tokens"`) {
		t.Errorf("manifest missing usage_limit (output only):\n%s", b)
	}
}

// --- ModelRetry (TODO §5.2) ---

// A RETRY result renders as "RETRY: <hint>" — pinned bytes.
func TestModelRetryRendersHint(t *testing.T) {
	flaky := weft.Tool("parse_date", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", weft.ModelRetry("date must be ISO-8601")
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "parse_date", Args: `{"d":"tomorrow"}`}),
		wefttest.Say("ok"),
	), flaky)
	res, err := agt.Generate(context.Background(), weft.Prompt("q"))
	if err != nil {
		t.Fatal(err)
	}
	r := res.Steps[0].Results[0]
	if !r.IsError || r.Content != "RETRY: date must be ISO-8601" {
		t.Errorf("result = %+v, want RETRY: date must be ISO-8601", r)
	}
}

// Four consecutive RETRY results from one tool fail the run at the
// continuation point; three, a success, and three more do not — the
// count resets on success.
func TestModelRetryCountsPerTool(t *testing.T) {
	newAgent := func(retryFirst int) (*weft.Agent, *wefttest.Model) {
		var calls atomic.Int32
		tool := weft.Tool("parse_date", "", func(_ context.Context, _ struct{}) (string, error) {
			if int(calls.Add(1)) <= retryFirst {
				return "", weft.ModelRetry("date must be ISO-8601")
			}
			return "2026-09-19", nil
		})
		turns := []wefttest.Turn{}
		for range retryFirst + 1 {
			turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "parse_date"}))
		}
		m := wefttest.Script(append(turns, wefttest.Say("done"))...)
		return weft.New(m, tool), m
	}

	stuck, _ := newAgent(4) // never succeeds
	_, err := stuck.Generate(context.Background(), weft.Prompt("q"))
	var re *weft.RunError
	if !errors.As(err, &re) || !errors.Is(err, weft.ErrModelRetriesExceeded) {
		t.Fatalf("err = %v, want ErrModelRetriesExceeded", err)
	}
	if re.Step != 3 {
		t.Errorf("failed at step %d, want 3 (the fourth consecutive RETRY)", re.Step)
	}
	msgs := re.Result.Messages
	if last := msgs[len(msgs)-1]; last.Role != weft.RoleTool {
		t.Errorf("transcript ends on %v, want the tool message", last.Role)
	}

	healing, m := newAgent(3) // three retries, then it succeeds
	res, err := healing.Generate(context.Background(), weft.Prompt("q"))
	if err != nil {
		t.Fatalf("a tool that heals must not fail the run: %v", err)
	}
	if got := len(m.Requests()); got != 5 {
		t.Errorf("model calls = %d, want 5", got)
	}
	if res.Steps[3].Results[0].Content != "2026-09-19" {
		t.Errorf("healed result = %+v", res.Steps[3].Results[0])
	}
}

// A RETRY produced by middleware counts exactly like a handler's — the
// loop sees the code through the chain, not who returned it.
func TestModelRetryFromMiddlewareCounts(t *testing.T) {
	tool := weft.Tool("parse_date", "", func(_ context.Context, _ struct{}) (string, error) {
		return "fine", nil
	})
	var n atomic.Int32
	nag := func(next weft.ToolCaller) weft.ToolCaller {
		return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
			if int(n.Add(1)) <= 4 {
				return "", weft.ModelRetry("middleware says no")
			}
			return next(ctx, call)
		}
	}
	turns := []wefttest.Turn{}
	for range 4 {
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "parse_date"}))
	}
	agt := weft.New(wefttest.Script(turns...), tool, weft.WrapTools(nag))
	_, err := agt.Generate(context.Background(), weft.Prompt("q"))
	if !errors.Is(err, weft.ErrModelRetriesExceeded) {
		t.Fatalf("err = %v, want ErrModelRetriesExceeded from middleware retries", err)
	}
}

// MaxModelRetries ignores values below 1: the default 3 stands.
func TestMaxModelRetriesIgnoresZero(t *testing.T) {
	tool := weft.Tool("parse_date", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", weft.ModelRetry("no")
	})
	turns := []wefttest.Turn{}
	for range 4 {
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "parse_date"}))
	}
	agt := weft.New(wefttest.Script(turns...), tool, weft.MaxModelRetries(0))
	_, err := agt.Generate(context.Background(), weft.Prompt("q"))
	if !errors.Is(err, weft.ErrModelRetriesExceeded) {
		t.Fatalf("err = %v, want the default budget of 3 to stand", err)
	}
}

// --- Loop detection (TODO §5.4) ---

func loopTool() *weft.ToolDef {
	return weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
}

// repeats identical consecutive steps trip the detector; the failure
// lands at the continuation point of the repeats-th step.
func TestDetectLoopsTripsOnIdenticalSteps(t *testing.T) {
	turns := []wefttest.Turn{}
	for range 5 {
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"i":1}`}))
	}
	turns = append(turns, wefttest.Say("never"))
	agt := weft.New(wefttest.Script(turns...), loopTool(), weft.DetectLoops(5))
	_, err := agt.Generate(context.Background(), weft.Prompt("q"))
	var re *weft.RunError
	if !errors.As(err, &re) || !errors.Is(err, weft.ErrLoopDetected) {
		t.Fatalf("err = %v, want ErrLoopDetected", err)
	}
	if re.Step != 4 {
		t.Errorf("failed at step %d, want 4 (the fifth identical step)", re.Step)
	}
	msgs := re.Result.Messages
	if last := msgs[len(msgs)-1]; last.Role != weft.RoleTool {
		t.Errorf("transcript ends on %v, want the tool message", last.Role)
	}
}

// Varying arguments are not a loop: each corrected retry has a
// different signature.
func TestDetectLoopsIgnoresVaryingArgs(t *testing.T) {
	turns := []wefttest.Turn{}
	for i := 1; i <= 5; i++ {
		turns = append(turns, wefttest.ToolCalls(
			wefttest.Call{Name: "echo", Args: fmt.Sprintf(`{"i":%d}`, i)}))
	}
	turns = append(turns, wefttest.Say("done"))
	agt := weft.New(wefttest.Script(turns...), loopTool(), weft.DetectLoops(3))
	res, err := agt.Generate(context.Background(), weft.Prompt("q"))
	if err != nil {
		t.Fatalf("varying args must not trip: %v", err)
	}
	if res.Text() != "done" {
		t.Errorf("text = %q", res.Text())
	}
}

// The signature is a set: a permuted batch counts as the same request.
func TestDetectLoopsIsOrderInsensitive(t *testing.T) {
	turns := []wefttest.Turn{
		wefttest.ToolCalls(wefttest.Call{Name: "a"}, wefttest.Call{Name: "b", Args: `{"x":1}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "b", Args: `{"x":1}`}, wefttest.Call{Name: "a"}),
		wefttest.ToolCalls(wefttest.Call{Name: "a"}, wefttest.Call{Name: "b", Args: `{"x":1}`}),
		wefttest.Say("never"),
	}
	echoes := []*weft.ToolDef{loopTool(), loopTool()}
	echoes[0].Name = "a"
	echoes[1].Name = "b"
	agt := weft.New(wefttest.Script(turns...), weft.DetectLoops(3), echoes[0], echoes[1])
	if _, err := agt.Generate(context.Background(), weft.Prompt("q")); !errors.Is(err, weft.ErrLoopDetected) {
		t.Fatalf("err = %v, want ErrLoopDetected on permuted repeats", err)
	}
}

// Off by default: identical steps run to MaxSteps, not ErrLoopDetected.
func TestDetectLoopsOffByDefault(t *testing.T) {
	turns := []wefttest.Turn{}
	for range 5 {
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"i":1}`}))
	}
	turns = append(turns, wefttest.Say("done"))
	agt := weft.New(wefttest.Script(turns...), loopTool())
	if _, err := agt.Generate(context.Background(), weft.Prompt("q")); err != nil {
		t.Fatalf("err = %v, want success without the option", err)
	}
}

// The manifest records the setting only when on.
func TestManifestDetectLoops(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("a"), weft.DetectLoops(5))
	b, err := weft.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"detect_loops": 5`) {
		t.Errorf("manifest missing detect_loops:\n%s", b)
	}
	off := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("b"))
	b2, _ := weft.Manifest(off)
	if strings.Contains(string(b2), `"detect_loops"`) {
		t.Errorf("detect_loops should be omitted when off:\n%s", b2)
	}
}

// --- PrepareStep (TODO §5.5) ---

// recordModel captures the ModelRequest a middleware chain forwards.
type recordModel struct {
	next weft.Model
	seen *weft.ModelRequest
}

func (m recordModel) Info() weft.ModelInfo { return weft.InfoOf(m.next) }

func (m recordModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	*m.seen = req
	return m.next.Stream(ctx, req)
}

// The prepared tool list is both the advertisement and the dispatch
// snapshot: a tool absent from step N's list cannot be called in step N.
func TestPrepareStepSubsetsToolsPerStep(t *testing.T) {
	a := weft.Tool("a", "", func(_ context.Context, _ struct{}) (string, error) { return "a", nil })
	b := weft.Tool("b", "", func(_ context.Context, _ struct{}) (string, error) { return "b", nil })
	phase := func(_ context.Context, step int, req weft.ModelRequest) (weft.ModelRequest, error) {
		keep := "a"
		if step > 0 {
			keep = "b"
		}
		req.Tools = slices.DeleteFunc(req.Tools, func(t *weft.ToolDef) bool { return t.Name != keep })
		return req, nil
	}
	m := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "b"}), // b not advertised in step 0
		wefttest.ToolCalls(wefttest.Call{Name: "b"}), // now it is
		wefttest.Say("done"),
	)
	agt := weft.New(m, a, b, weft.PrepareStep(phase))
	res, err := agt.Generate(context.Background(), weft.Prompt("q"))
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Steps[0].Results[0]; !r.IsError || !strings.HasPrefix(r.Content, "NO_SUCH_TOOL:") {
		t.Errorf("step 0 call to b = %+v, want NO_SUCH_TOOL", r)
	}
	if r := res.Steps[1].Results[0]; r.IsError || r.Content != "b" {
		t.Errorf("step 1 call to b = %+v, want success", r)
	}
	// The recorded requests prove the advertisement followed the phase.
	// The Request matchers are the short way to assert on one (§9.3).
	if got := (wefttest.Request{ModelRequest: m.Requests()[0]}).ToolNames(); !slices.Equal(got, []string{"a"}) {
		t.Errorf("step 0 advertised %v", got)
	}
	step1 := wefttest.Request{ModelRequest: m.Requests()[1]}
	if !step1.HasTool("b") {
		t.Errorf("step 1 advertised %v, want b", step1.ToolNames())
	}
}

// Trimming the request trims what the model sees; the transcript keeps
// the whole history.
func TestPrepareStepTrimsRequestNotTranscript(t *testing.T) {
	lastOnly := func(_ context.Context, _ int, req weft.ModelRequest) (weft.ModelRequest, error) {
		if len(req.Messages) > 1 {
			req.Messages = req.Messages[len(req.Messages)-1:]
		}
		return req, nil
	}
	m := wefttest.Script(wefttest.Say("done"))
	agt := weft.New(m, weft.PrepareStep(lastOnly))
	res, err := agt.Generate(context.Background(), weft.Prompt("first"), weft.Prompt("second"))
	if err != nil {
		t.Fatal(err)
	}
	// LastRequest is the one-request shorthand for the same assertion.
	if got := len(m.LastRequest().Messages); got != 1 {
		t.Errorf("model saw %d messages, want 1", got)
	}
	if got := len(res.Messages); got != 3 { // two input messages + the reply
		t.Errorf("transcript kept %d messages, want 3 (the whole history)", got)
	}
}

// An error from the function fails the run, with the caller's sentinel
// reachable through errors.Is.
func TestPrepareStepErrorFailsRun(t *testing.T) {
	refuse := errors.New("no tools for you")
	agt := weft.New(wefttest.Script(wefttest.Say("never")),
		weft.PrepareStep(func(context.Context, int, weft.ModelRequest) (weft.ModelRequest, error) {
			return weft.ModelRequest{}, refuse
		}))
	_, err := agt.Generate(context.Background(), weft.Prompt("q"))
	var re *weft.RunError
	if !errors.As(err, &re) || !errors.Is(err, refuse) {
		t.Fatalf("err = %v, want a RunError wrapping the caller's sentinel", err)
	}
	if re.Step != 0 {
		t.Errorf("failed at step %d, want 0", re.Step)
	}
}

// Snippets compose after preparation, from the tools it returned:
// removing a tool removes its snippet.
func TestPrepareStepComposesSnippetsAfter(t *testing.T) {
	noisy := weft.Tool("noisy", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil },
		weft.PromptSnippet("Use noisy carefully."))
	quiet := weft.Tool("quiet", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	drop := func(_ context.Context, _ int, req weft.ModelRequest) (weft.ModelRequest, error) {
		req.Tools = slices.DeleteFunc(req.Tools, func(t *weft.ToolDef) bool { return t.Name == "noisy" })
		return req, nil
	}
	baseM := wefttest.Script(wefttest.Say("done"))
	base := weft.New(baseM, weft.Instructions("Base."), noisy, quiet)
	trimmedM := wefttest.Script(wefttest.Say("done"))
	trimmed := weft.New(trimmedM, weft.Instructions("Base."), noisy, quiet, weft.PrepareStep(drop))
	if _, err := base.Generate(context.Background(), weft.Prompt("q")); err != nil {
		t.Fatal(err)
	}
	if _, err := trimmed.Generate(context.Background(), weft.Prompt("q")); err != nil {
		t.Fatal(err)
	}
	if sys := baseM.Requests()[0].System; !strings.Contains(sys, "Use noisy carefully.") {
		t.Errorf("unprepared system = %q, want the snippet", sys)
	}
	if sys := trimmedM.Requests()[0].System; sys != "Base." {
		t.Errorf("prepared system = %q, want the snippet gone (composed only from the returned tools)", sys)
	}
}

// Several PrepareStep options chain in order, each seeing the previous
// one's result.
func TestPrepareStepChainsInOrder(t *testing.T) {
	var order []string
	tag := func(name string, rewrite func(*weft.ModelRequest)) func(context.Context, int, weft.ModelRequest) (weft.ModelRequest, error) {
		return func(_ context.Context, _ int, req weft.ModelRequest) (weft.ModelRequest, error) {
			order = append(order, name)
			rewrite(&req)
			return req, nil
		}
	}
	m := wefttest.Script(wefttest.Say("done"))
	agt := weft.New(m,
		weft.PrepareStep(tag("first", func(r *weft.ModelRequest) { r.System += "+1" })),
		weft.PrepareStep(tag("second", func(r *weft.ModelRequest) { r.System += "+2" })),
	)
	if _, err := agt.Generate(context.Background(), weft.Prompt("q")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []string{"first", "second"}) {
		t.Errorf("order = %v", order)
	}
	if sys := m.Requests()[0].System; sys != "+1+2" {
		t.Errorf("system = %q, want the chain applied in order", sys)
	}
}

// The seam sees the prepared request: PrepareStep runs before
// WrapModel.
func TestPrepareStepRunsBeforeModelSeam(t *testing.T) {
	var seen weft.ModelRequest
	recorder := func(next weft.Model) weft.Model {
		return recordModel{next: next, seen: &seen}
	}
	m := wefttest.Script(wefttest.Say("done"))
	agt := weft.New(m,
		weft.WrapModel(recorder),
		weft.PrepareStep(func(_ context.Context, _ int, req weft.ModelRequest) (weft.ModelRequest, error) {
			req.System = "prepared"
			return req, nil
		}),
	)
	if _, err := agt.Generate(context.Background(), weft.Prompt("q")); err != nil {
		t.Fatal(err)
	}
	if seen.System != "prepared" {
		t.Errorf("seam saw %q, want the prepared system", seen.System)
	}
}

// A prepared list with a duplicate name or nil entry fails the run —
// the same rule a tool-source snapshot obeys.
func TestPrepareStepDuplicateToolFailsRun(t *testing.T) {
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	dup := func(_ context.Context, _ int, req weft.ModelRequest) (weft.ModelRequest, error) {
		req.Tools = append(req.Tools, req.Tools...)
		return req, nil
	}
	agt := weft.New(wefttest.Script(wefttest.Say("never")), echo, weft.PrepareStep(dup))
	_, err := agt.Generate(context.Background(), weft.Prompt("q"))
	if !errors.Is(err, weft.ErrDuplicateTool) {
		t.Fatalf("err = %v, want ErrDuplicateTool", err)
	}
	nilEntry := func(_ context.Context, _ int, req weft.ModelRequest) (weft.ModelRequest, error) {
		req.Tools = []*weft.ToolDef{nil}
		return req, nil
	}
	agt2 := weft.New(wefttest.Script(wefttest.Say("never")), echo, weft.PrepareStep(nilEntry))
	if _, err := agt2.Generate(context.Background(), weft.Prompt("q")); !errors.Is(err, weft.ErrNilTool) {
		t.Fatalf("err = %v, want ErrNilTool", err)
	}
}

// PrepareStep over a ToolSource: the source is consulted once per step
// and the prepared subset of its snapshot is the dispatch snapshot.
func TestPrepareStepOverToolSource(t *testing.T) {
	var fetches int
	a := weft.Tool("a", "", func(_ context.Context, _ struct{}) (string, error) { return "a", nil })
	b := weft.Tool("b", "", func(_ context.Context, _ struct{}) (string, error) { return "b", nil })
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "a"}, wefttest.Call{Name: "b"}),
		wefttest.Say("done"),
	),
		weft.ToolSource(func() []*weft.ToolDef { fetches++; return []*weft.ToolDef{a, b} }),
		weft.PrepareStep(func(_ context.Context, _ int, req weft.ModelRequest) (weft.ModelRequest, error) {
			req.Tools = slices.DeleteFunc(req.Tools, func(t *weft.ToolDef) bool { return t.Name == "b" })
			return req, nil
		}))
	res, err := agt.Generate(context.Background(), weft.Prompt("q"))
	if err != nil {
		t.Fatal(err)
	}
	if fetches != 2 {
		t.Errorf("source fetched %d times, want 2 (once per step)", fetches)
	}
	if r := res.Steps[0].Results[0]; r.IsError || r.Content != "a" {
		t.Errorf("kept tool = %+v", r)
	}
	if r := res.Steps[0].Results[1]; !r.IsError || !strings.HasPrefix(r.Content, "NO_SUCH_TOOL:") {
		t.Errorf("dropped tool = %+v, want NO_SUCH_TOOL", r)
	}
}

// --- Option composition (TODO §5.10) ---

// Options applies in order: later instructions win, tools register in
// sequence, and nil entries are ignored.
func TestOptionsPreservesOrder(t *testing.T) {
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	plugin := weft.Options(
		weft.Instructions("first"),
		nil, // ignored
		weft.Instructions("second"),
		echo,
		weft.MaxSteps(7),
	)
	plugin = weft.Options(weft.Name("composed"), plugin)
	agt := weft.New(wefttest.Script(wefttest.Say("ok")), plugin)
	b, err := weft.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"instructions": "second"`) || !strings.Contains(string(b), `"max_steps": 7`) {
		t.Errorf("composed option not applied:\n%s", b)
	}
	if !strings.Contains(string(b), `"name": "echo"`) {
		t.Errorf("tool inside Options not registered:\n%s", b)
	}
}

// Options nests: inner groups apply before the options that follow
// them, by construction.
func TestOptionsNests(t *testing.T) {
	inner := weft.Options(weft.Instructions("inner"))
	agt := weft.New(wefttest.Script(wefttest.Say("ok")),
		weft.Name("nested"),
		weft.Options(inner, weft.Instructions("outer")))
	b, err := weft.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"instructions": "outer"`) {
		t.Errorf("nested compose order wrong:\n%s", b)
	}
}

// The "plugin registered twice" mistake is loud, not deduplicated.
func TestOptionsDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate tool names inside Options did not panic")
		}
	}()
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	_ = weft.New(wefttest.Script(wefttest.Say("ok")), weft.Options(echo, echo))
}

// ToolOptions packages per-tool policy under one name.
func TestToolOptions(t *testing.T) {
	policy := weft.ToolOptions(weft.Timeout(3*time.Second), weft.StrictInput())
	echo := weft.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil }, policy)
	b, err := weft.Manifest(weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("a"), echo))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"timeout": "3s"`) || !strings.Contains(string(b), `"strict_input": true`) {
		t.Errorf("ToolOptions policy not applied:\n%s", b)
	}
}
