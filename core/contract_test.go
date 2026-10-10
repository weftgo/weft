package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/codes"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
)

// turnModel is a bare-bones core.Model playing raw event slices, for
// testing how the loop treats streams that bend or break the Model
// contract. wefttest cannot express those — by design.
type turnModel struct {
	turns [][]core.ModelEvent
	pos   int
}

func (m *turnModel) Stream(context.Context, core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
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

func (panicModel) Stream(context.Context, core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(func(core.ModelEvent, error) bool) { panic("adapter bug") }
}

// The Model stream contract is enforced by the loop, not merely
// documented: every violation fails the run wrapping ErrModelContract
// instead of corrupting the transcript or crashing the process.
func TestModelStreamContractViolations(t *testing.T) {
	finish := core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}}
	cases := []struct {
		name  string
		model core.Model
		want  string // fragment of the contract-violation message
	}{
		{
			name:  "stream ends without ModelFinish",
			model: &turnModel{turns: [][]core.ModelEvent{{core.ModelTextDelta{Text: "hi"}}}},
			want:  "without ModelFinish",
		},
		{
			name:  "two ModelFinish events",
			model: &turnModel{turns: [][]core.ModelEvent{{finish, finish}}},
			want:  "after ModelFinish",
		},
		{
			name: "text delta after ModelFinish",
			model: &turnModel{turns: [][]core.ModelEvent{
				{finish, core.ModelTextDelta{Text: "late"}},
			}},
			want: "after ModelFinish",
		},
		{
			name: "tool call with an empty ID",
			model: &turnModel{turns: [][]core.ModelEvent{
				{core.ModelToolCall{ID: "", Name: "t"}, finish},
			}},
			want: "empty ID",
		},
		{
			name: "tool call with an empty name",
			model: &turnModel{turns: [][]core.ModelEvent{
				{core.ModelToolCall{ID: "c1", Name: ""}, finish},
			}},
			want: "empty name",
		},
		{
			name: "duplicate tool call ID",
			model: &turnModel{turns: [][]core.ModelEvent{
				{core.ModelToolCall{ID: "c1", Name: "t"}, core.ModelToolCall{ID: "c1", Name: "t"}, finish},
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
			agt := core.New(tc.model)
			res, err := agt.Generate(context.Background(), core.Prompt("x"))
			if !errors.Is(err, core.ErrModelContract) {
				t.Fatalf("error = %v, want one wrapping ErrModelContract", err)
			}
			if res != nil {
				t.Error("result should be nil on a contract failure")
			}
			var runErr *core.RunError
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
	agt := core.New(wefttest.Script(wefttest.MaxTokens("The answer is 4")))
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatalf("a truncated reply must not fail the run: %v", err)
	}
	if res.StopReason != core.StopMaxTokens {
		t.Errorf("RunResult.StopReason = %q, want %q", res.StopReason, core.StopMaxTokens)
	}
	if last := res.Steps[len(res.Steps)-1]; last.StopReason != core.StopMaxTokens {
		t.Errorf("last StepRecord.StopReason = %q, want %q", last.StopReason, core.StopMaxTokens)
	}
	if res.Text() != "The answer is 4" {
		t.Errorf("partial text = %q, want the truncated reply preserved", res.Text())
	}
	// An ordinary run records the ordinary reason.
	res, err = core.New(wefttest.Script(wefttest.Say("done"))).Generate(context.Background(), core.Prompt("x"))
	if err != nil || res.StopReason != core.StopEndTurn {
		t.Errorf("normal run: res.StopReason = %q, err = %v; want %q", res.StopReason, err, core.StopEndTurn)
	}
}

// max_tokens alongside tool calls is recoverable: none of the step's
// calls execute — each gets the pinned truncation failure — and the
// next model call proceeds normally.
func TestMaxTokensWithToolCallsFailsThemWithoutExecuting(t *testing.T) {
	executed := 0
	touch := core.Tool("touch", "", func(_ context.Context, _ struct{}) (string, error) { executed++; return "ok", nil })
	model := &turnModel{turns: [][]core.ModelEvent{
		{
			core.ModelToolCall{ID: "c1", Name: "touch", Args: json.RawMessage(`{}`)},
			core.ModelToolCall{ID: "c2", Name: "touch", Args: json.RawMessage(`{"cut`)},
			core.ModelFinish{Reason: core.StopMaxTokens, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			core.ModelTextDelta{Text: "recovered"},
			core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}}
	var events []core.Event
	agt := core.New(model, touch, core.Tap(func(_ context.Context, ev core.Event) { events = append(events, ev) }))
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
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
	if m := res.Messages[len(res.Messages)-2]; m.Role != core.RoleTool || len(m.Content) != 2 {
		t.Errorf("message before the recovery = %+v, want a tool message with both results", m)
	}
	for _, ev := range events {
		switch ev.(type) {
		case core.ToolStart, core.ToolFinish:
			t.Errorf("unexpected %T on a truncated step: nothing executed", ev)
		}
	}
}

// Oversized tool results are capped with a visible marker — successes,
// failures alike — and the cap can be turned off.
func TestMaxResultBytes(t *testing.T) {
	const cap = 64 << 10
	marker := fmt.Sprintf("\n…[truncated %d bytes]", 200_000-cap)
	big := core.Tool("big", "", func(_ context.Context, _ struct{}) (string, error) {
		return strings.Repeat("x", 200_000), nil
	})
	boom := core.Tool("boom", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", errors.New(strings.Repeat("e", 200_000))
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "big"}, wefttest.Call{Name: "boom"}),
		wefttest.Say("ok"),
	), big, boom)

	res, err := agt.Generate(context.Background(), core.Prompt("x"))
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
	agt2 := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "big"}),
		wefttest.Say("ok"),
	), core.MaxResultBytes(0), big)
	res2, err := agt2.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(res2.Steps[0].Results[0].Content); got != 200_000 {
		t.Errorf("uncapped result length = %d, want 200000", got)
	}

	// The cut lands on a rune boundary: capping 200 bytes of two-byte
	// runes at 101 leaves exactly 100 bytes of payload.
	runes := core.Tool("runes", "", func(_ context.Context, _ struct{}) (string, error) {
		return strings.Repeat("é", 100), nil
	})
	res3, err := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "runes"}),
		wefttest.Say("ok"),
	), core.MaxResultBytes(101), runes).Generate(context.Background(), core.Prompt("x"))
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
	touch := core.Tool("touch", "", func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	run := func(opts ...core.Option) bool {
		model := wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
			wefttest.Say("ok"),
		)
		agt := core.New(model, append([]core.Option{touch}, opts...)...)
		if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
			t.Fatal(err)
		}
		return model.Requests()[0].SequentialTools
	}
	if run() {
		t.Error("default policy: SequentialTools = true, want false (provider default)")
	}
	if run(core.Parallelism(2)) {
		t.Error("Parallelism(2): SequentialTools = true, want false")
	}
	if !run(core.Parallelism(1)) {
		t.Error("Parallelism(1): SequentialTools = false, want true")
	}
	if !run(core.Sequential()) {
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
		core.Tool("t", "", func(_ context.Context, in string) (string, error) { return in, nil })
	})
	didPanic("map input", func() {
		core.Tool("t", "", func(_ context.Context, in map[string]any) (string, error) { return "", nil })
	})
	didPanic("slice input", func() {
		core.Tool("t", "", func(_ context.Context, in []string) (string, error) { return "", nil })
	})
	// A struct, and a pointer to one, are both fine.
	core.Tool("t", "", func(_ context.Context, in struct{ A int }) (string, error) { return "", nil })
	core.Tool("t", "", func(_ context.Context, in *struct{ A int }) (string, error) { return "", nil })
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
	core.New((*wefttest.Model)(nil))
}

// The message wire format is a compatibility contract: every part carries
// a type discriminator and a transcript round-trips losslessly.
func TestMessageJSONRoundTrip(t *testing.T) {
	in := []core.Message{
		core.User("hi"),
		{Role: core.RoleUser, Content: []core.Part{
			core.FilePart{MediaType: "image/png", Data: []byte("png")},
			core.FilePart{MediaType: "image/png", URL: "https://example.com/a.png"},
		}},
		{Role: core.RoleAssistant, Content: []core.Part{
			core.ReasoningPart{Text: "thinking"},
			core.TextPart{Text: "calling"},
			core.ToolCallPart{ID: "c1", Name: "echo", Args: json.RawMessage(`{"msg":"x"}`)},
		}},
		{Role: core.RoleTool, Content: []core.Part{
			core.ToolResultPart{CallID: "c1", Name: "echo", Content: `"X"`},
			core.ToolResultPart{CallID: "c2", Name: "boom", Content: "down", IsError: true},
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

	var out []core.Message
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
	if _, ok := out[2].Content[0].(core.ReasoningPart); !ok {
		t.Errorf("part 0 decoded as %T, want ReasoningPart", out[2].Content[0])
	}
	if _, ok := out[2].Content[1].(core.TextPart); !ok {
		t.Errorf("part 1 decoded as %T, want TextPart", out[2].Content[1])
	}
	// Files decode with their bytes intact.
	f, ok := out[1].Content[0].(core.FilePart)
	if !ok || f.MediaType != "image/png" || string(f.Data) != "png" {
		t.Errorf("file part = %+v, want the inline image/png back", out[1].Content[0])
	}
}

// UserParts preserves part order and does not alias the caller's slice.
func TestUserParts(t *testing.T) {
	parts := []core.Part{
		core.TextPart{Text: "What is this?"},
		core.FilePart{MediaType: "image/png", Data: []byte("png")},
	}
	m := core.UserParts(parts...)
	if m.Role != core.RoleUser || len(m.Content) != 2 {
		t.Fatalf("UserParts = %+v, want a user message with both parts", m)
	}
	if txt, ok := m.Content[0].(core.TextPart); !ok || txt.Text != "What is this?" {
		t.Errorf("part 0 = %+v, want the text first (order preserved)", m.Content[0])
	}
	if _, ok := m.Content[1].(core.FilePart); !ok {
		t.Errorf("part 1 = %T, want FilePart", m.Content[1])
	}
	// Mutating the returned content must not touch the caller's slice.
	m.Content[0] = core.TextPart{Text: "mutated"}
	if parts[0].(core.TextPart).Text != "What is this?" {
		t.Error("UserParts aliased the caller's slice")
	}
}

func TestMessageJSONRejectsUnknownPart(t *testing.T) {
	var m core.Message
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
	tag := core.Tool("tag", "Record the call.",
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
		agt := core.New(wefttest.Script(wefttest.ToolCalls(calls...), wefttest.Say("ok")), core.Sequential(), tag)
		if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
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
	sleepy := core.Tool("sleepy", "Vary in duration.",
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
	agt := core.New(wefttest.Script(wefttest.ToolCalls(calls...), wefttest.Say("ok")), core.Parallelism(2), sleepy)

	var starts []string
	for ev, err := range agt.Stream(context.Background(), core.Prompt("x")).Events() {
		if err != nil {
			t.Fatal(err)
		}
		if s, ok := ev.(core.ToolStart); ok {
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
	block := core.Tool("block", "Cancels the run, then waits for it.",
		func(ctx context.Context, _ struct{}) (string, error) {
			cancel()
			<-ctx.Done()
			return "", ctx.Err()
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "block"}, wefttest.Call{Name: "block"}, wefttest.Call{Name: "block"}),
		wefttest.Say("unreachable"),
	)
	agt := core.New(model, core.Sequential(), block)

	run := agt.Stream(ctx, core.Prompt("x"))
	started := map[string]bool{}
	for ev, err := range run.Events() {
		if err != nil {
			break
		}
		switch e := ev.(type) {
		case core.ToolStart:
			started[e.CallID] = true
		case core.ToolFinish:
			if !started[e.CallID] {
				t.Errorf("ToolFinish for %s without a ToolStart", e.CallID)
			}
		}
	}
	_, err := run.Wait()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	var runErr *core.RunError
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
	approved := core.Tool("gated", "Parks the run.",
		func(context.Context, struct{}) (string, error) { return "ran", nil },
		core.RequireApproval())
	cases := []struct {
		name  string
		model func() core.Model
	}{
		{"ordinary exit", func() core.Model { return wefttest.Script(wefttest.Say("done")) }},
		{"approval boundary exit", func() core.Model {
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
					seen      []core.Event
				)
				agt := core.New(tc.model(), approved, core.Tap(func(_ context.Context, ev core.Event) {
					mu.Lock()
					defer mu.Unlock()
					seen = append(seen, ev)
					switch ev.(type) {
					case core.StepFinish:
						cancel() // the window: after the loop's checks, before RunFinish
					case core.RunFinish:
						sawFinish = true
					}
				}))
				var err error
				if mode == "generate" {
					_, err = agt.Generate(ctx, core.Prompt("x"))
				} else {
					var streamed []core.Event
					run := agt.Stream(ctx, core.Prompt("x"))
					for ev, serr := range run.Events() {
						if serr != nil {
							err = serr
							break
						}
						streamed = append(streamed, ev)
						if _, ok := ev.(core.RunFinish); ok {
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
				var runErr *core.RunError
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
	touch := core.Tool("touch", "Cancels the run.",
		func(_ context.Context, _ struct{}) (string, error) {
			cancel()
			return "ok", nil
		})
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "touch"}))
	agt := core.New(model, core.MaxSteps(1), touch)

	_, err := agt.Generate(ctx, core.Prompt("x"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if errors.Is(err, core.ErrMaxSteps) {
		t.Error("cancellation must not be reported as ErrMaxSteps")
	}
}

func TestSchemaUntaggedFieldUsesGoName(t *testing.T) {
	tool := core.Tool("t", "", func(_ context.Context, _ struct {
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
	tool := core.Tool("tree", "", func(_ context.Context, _ treeNode) (string, error) { return "", nil })
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
	tool := core.Tool("seg", "", func(_ context.Context, _ struct {
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
	slow := core.Tool("slow", "Waits for cancellation.",
		func(ctx context.Context, _ struct{}) (string, error) {
			<-ctx.Done()
			sawCancel.Done()
			return "", ctx.Err()
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "slow"}),
		wefttest.Say("unreachable"),
	)
	agt := core.New(model, slow)

	before := runtime.NumGoroutine()
	run := agt.Stream(context.Background(), core.Prompt("x"))
	for ev := range run.Events() {
		if _, ok := ev.(core.ToolStart); ok {
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
	agt := core.New(wefttest.Script(wefttest.Say("hi")))
	run := agt.Stream(context.Background(), core.Prompt("x"))
	for _, err := range run.Events() {
		if err != nil {
			t.Fatal(err)
		}
	}
	var got error
	for _, err := range run.Events() {
		got = err
	}
	if !errors.Is(got, core.ErrRunConsumed) {
		t.Errorf("second Events() error = %v, want ErrRunConsumed", got)
	}
}

// Wait alone must not deadlock: it runs the agent if nobody consumed Events.
func TestWaitWithoutEventsRunsTheAgent(t *testing.T) {
	agt := core.New(wefttest.Script(wefttest.Say("hi")))
	done := make(chan struct{})
	var res *core.RunResult
	var err error
	go func() {
		defer close(done)
		res, err = agt.Stream(context.Background(), core.Prompt("x")).Wait()
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
	count := core.Tool("count", "", func(_ context.Context, in struct {
		Q string `json:"q"`
	}) (int, error) {
		return len(in.Q), nil
	})
	agt := core.New(wefttest.Script(), count)
	ctx := context.Background()

	if _, err := agt.CallTool(ctx, core.ToolCallPart{Name: "nope"}); !errors.Is(err, core.ErrNoSuchTool) {
		t.Errorf("unknown tool error = %v, want ErrNoSuchTool", err)
	}
	if _, err := agt.CallTool(ctx, core.ToolCallPart{Name: "count", Args: json.RawMessage(`{"q":1}`)}); !errors.Is(err, core.ErrInvalidToolInput) {
		t.Errorf("bad args error = %v, want ErrInvalidToolInput", err)
	}
	out, err := agt.CallTool(ctx, core.ToolCallPart{Name: "count", Args: json.RawMessage(`{"q":"abc"}`)})
	if err != nil || out != "3" {
		t.Errorf("CallTool = %s, %v", out, err)
	}
}

func TestToolFinishCarriesContent(t *testing.T) {
	echo := core.Tool("echo", "", func(_ context.Context, in struct {
		M string `json:"m"`
	}) (string, error) {
		return in.M, nil
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"m":"hey"}`}, wefttest.Call{Name: "nope"}),
		wefttest.Say("ok"),
	), echo)
	finishes := map[string]core.ToolFinish{}
	for ev, err := range agt.Stream(context.Background(), core.Prompt("x")).Events() {
		if err != nil {
			t.Fatal(err)
		}
		if f, ok := ev.(core.ToolFinish); ok {
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
	agt := core.New(wefttest.Script(wefttest.Say("")))
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
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
	submit := core.Tool("submit_answer", "Final answer.",
		func(_ context.Context, in struct {
			Answer string `json:"answer"`
		}) (string, error) {
			return "recorded", nil
		})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "submit_answer", Args: `{"answer":"42"}`}),
		wefttest.Say("unreachable: the run must stop before this call"),
	)
	agt := core.New(model, core.StopWhen(core.HasToolCall("submit_answer")), submit)

	res, err := agt.Generate(context.Background(), core.Prompt("x"))
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
	touch := core.Tool("touch", "", func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	script := func() *wefttest.Model {
		return wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
			wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
			wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
		)
	}
	res, err := core.New(script(), core.StopWhen(core.StepCountIs(2)), touch).Generate(context.Background(), core.Prompt("x"))
	if err != nil || res.NumSteps() != 2 {
		t.Errorf("StepCountIs(2): res=%v err=%v, want a successful 2-step run", res, err)
	}
	_, err = core.New(script(), core.MaxSteps(2), touch).Generate(context.Background(), core.Prompt("x"))
	if !errors.Is(err, core.ErrMaxSteps) {
		t.Errorf("MaxSteps(2): err=%v, want ErrMaxSteps", err)
	}
}

func TestCallFromContextInsideTools(t *testing.T) {
	var got core.Call
	var ok bool
	peek := core.Tool("peek", "", func(ctx context.Context, _ struct{}) (string, error) {
		got, ok = core.CallFromContext(ctx)
		return "", nil
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "call_xyz", Name: "peek"}),
		wefttest.Say("done"),
	), peek)
	res, err := agt.Generate(context.Background(), core.Prompt("x"), core.RunID("run-1"))
	if err != nil {
		t.Fatal(err)
	}
	want := core.Call{RunID: "run-1", Step: 0, CallID: "call_xyz", Name: "peek"}
	if !ok || got != want {
		t.Errorf("CallFromContext = %+v (ok=%v), want %+v", got, ok, want)
	}
	if res.ID != "run-1" {
		t.Errorf("RunResult.ID = %q, want the supplied id", res.ID)
	}
	// Outside the loop there is no call.
	if _, ok := core.CallFromContext(context.Background()); ok {
		t.Error("CallFromContext must report ok=false outside a run")
	}
}

func TestRunIDsAreGeneratedAndStreamed(t *testing.T) {
	agt := core.New(wefttest.Script(wefttest.Say("hi")))
	run := agt.Stream(context.Background(), core.Prompt("x"))
	if len(run.ID()) != 32 {
		t.Errorf("Run.ID() = %q, want a 32-hex-char generated id", run.ID())
	}
	var first core.Event
	for ev, err := range run.Events() {
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = ev
		}
	}
	start, ok := first.(core.RunStart)
	if !ok || start.ID != run.ID() {
		t.Errorf("first event = %+v, want RunStart{ID: %q}", first, run.ID())
	}
	res, _ := run.Wait()
	if res.ID != run.ID() {
		t.Errorf("RunResult.ID = %q, want %q", res.ID, run.ID())
	}
	other := agt.Stream(context.Background(), core.Prompt("x"))
	if other.ID() == run.ID() {
		t.Error("two runs got the same generated id")
	}
}

// Provider reasoning round-trips: deltas accumulate into one
// ReasoningPart placed before the TextPart, the last non-empty signature
// rides along, and the wire format is pinned.
func TestReasoningRoundTrip(t *testing.T) {
	model := &turnModel{turns: [][]core.ModelEvent{{
		core.ModelReasoningDelta{Text: "a"},
		core.ModelReasoningDelta{Text: "b", Signature: "sig-1"},
		core.ModelTextDelta{Text: "hi"},
		core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	res, err := core.New(model).Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	msg := res.Messages[1]
	if len(msg.Content) != 2 {
		t.Fatalf("assistant content = %+v, want [ReasoningPart, TextPart]", msg.Content)
	}
	if r, ok := msg.Content[0].(core.ReasoningPart); !ok || r.Text != "ab" || r.Signature != "sig-1" {
		t.Errorf("reasoning part = %+v, want {Text: ab, Signature: sig-1}", msg.Content[0])
	}
	if txt, ok := msg.Content[1].(core.TextPart); !ok || txt.Text != "hi" {
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
	bare, err := json.Marshal(core.ReasoningPart{Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bare), "signature") {
		t.Errorf("empty signature must be omitted on the wire: %s", bare)
	}
	var back core.Message
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Content[0] != msg.Content[0] {
		t.Errorf("round trip = %+v, want %+v", back.Content[0], msg.Content[0])
	}
}

// Reasoning streams before text, in the order the model produced it.
func TestReasoningEventEmitted(t *testing.T) {
	model := &turnModel{turns: [][]core.ModelEvent{{
		core.ModelReasoningDelta{Text: "pondering"},
		core.ModelTextDelta{Text: "answer"},
		core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	var order []string
	for ev, err := range core.New(model).Stream(context.Background(), core.Prompt("x")).Events() {
		if err != nil {
			t.Fatal(err)
		}
		switch e := ev.(type) {
		case core.ReasoningDelta:
			order = append(order, "reasoning:"+e.Text)
		case core.TextDelta:
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
	model := &turnModel{turns: [][]core.ModelEvent{{
		core.ModelReasoningDelta{Text: "first "},
		core.ModelReasoningDelta{Text: "thought", Signature: "sig-1"},
		core.ModelReasoningDelta{Text: "second"},
		core.ModelReasoningDelta{Signature: "sig-2"},
		core.ModelTextDelta{Text: "hi"},
		core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	res, err := core.New(model).Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	msg := res.Messages[1]
	if len(msg.Content) != 3 {
		t.Fatalf("assistant content = %+v, want [Reasoning, Reasoning, Text]", msg.Content)
	}
	r1, ok1 := msg.Content[0].(core.ReasoningPart)
	r2, ok2 := msg.Content[1].(core.ReasoningPart)
	if !ok1 || !ok2 || r1.Text != "first thought" || r1.Signature != "sig-1" ||
		r2.Text != "second" || r2.Signature != "sig-2" {
		t.Errorf("reasoning parts = %+v %+v, want per-block text+signature", r1, r2)
	}
	if txt, ok := msg.Content[2].(core.TextPart); !ok || txt.Text != "hi" {
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
	var back core.Message
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
	model := &turnModel{turns: [][]core.ModelEvent{{
		core.ModelReasoningDelta{Text: "a"},
		core.ModelReasoningDelta{Text: "b"},
		core.ModelTextDelta{Text: "x"},
		core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	res, err := core.New(model).Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages[1].Content) != 2 {
		t.Fatalf("content = %+v, want one reasoning part and the text", res.Messages[1].Content)
	}
	if r, ok := res.Messages[1].Content[0].(core.ReasoningPart); !ok || r.Text != "ab" || r.Signature != "" {
		t.Errorf("reasoning = %+v, want the accumulated unsigned block", res.Messages[1].Content[0])
	}
}

// A tool call's own signature (Gemini attaches thought signatures to
// function calls and requires them back on the same part) survives the
// loop and the wire, additively: signature-less calls encode exactly as
// before.
func TestToolCallSignatureRoundTrip(t *testing.T) {
	model := &turnModel{turns: [][]core.ModelEvent{
		{
			core.ModelToolCall{ID: "c1", Name: "t", Args: json.RawMessage(`{}`), Signature: "sig-1"},
			core.ModelToolCall{ID: "c2", Name: "t", Args: json.RawMessage(`{}`)},
			core.ModelFinish{Reason: core.StopToolCalls, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			core.ModelTextDelta{Text: "done"},
			core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}}
	res, err := core.New(model, core.Tool("t", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})).Generate(context.Background(), core.Prompt("x"))
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
	var back core.Message
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if got := back.Content[0].(core.ToolCallPart).Signature; got != "sig-1" {
		t.Errorf("round-trip signature = %q, want sig-1", got)
	}
}

// A turn with reasoning but no text and no calls is recorded: it has
// content, and dropping it would lose a signed block the provider may
// expect back.
func TestReasoningOnlyTurnIsRecorded(t *testing.T) {
	model := &turnModel{turns: [][]core.ModelEvent{{
		core.ModelReasoningDelta{Text: "just thinking"},
		core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	res, err := core.New(model).Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("transcript = %+v, want the user and the reasoning turn", res.Messages)
	}
	if _, ok := res.Messages[1].Content[0].(core.ReasoningPart); !ok {
		t.Errorf("assistant part = %T, want ReasoningPart", res.Messages[1].Content[0])
	}
}

// A turn without reasoning events gets no reasoning part.
func TestEmptyReasoningNotAppended(t *testing.T) {
	model := &turnModel{turns: [][]core.ModelEvent{{
		core.ModelTextDelta{Text: "plain"},
		core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	res, err := core.New(model).Generate(context.Background(), core.Prompt("x"))
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
	model := &turnModel{turns: [][]core.ModelEvent{{
		core.ModelReasoningDelta{Signature: "sig-2"},
		core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	res, err := core.New(model).Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	r, ok := res.Messages[1].Content[0].(core.ReasoningPart)
	if !ok || r.Text != "" || r.Signature != "sig-2" {
		t.Errorf("signed empty reasoning = %+v, want {Text: , Signature: sig-2}", res.Messages[1].Content[0])
	}
}

// Repair: missing results are synthesised in call order, appended to
// the tool message that follows, with pinned model-visible text.
func TestRepairSynthesisesMissingResults(t *testing.T) {
	in := []core.Message{
		core.User("go"),
		{Role: core.RoleAssistant, Content: []core.Part{
			core.ToolCallPart{ID: "a", Name: "t1", Args: json.RawMessage(`{}`)},
			core.ToolCallPart{ID: "b", Name: "t2", Args: json.RawMessage(`{}`)},
		}},
		{Role: core.RoleTool, Content: []core.Part{
			core.ToolResultPart{CallID: "b", Name: "t2", Content: "ok"},
		}},
	}
	out := core.Repair(in)
	if len(out) != 3 || out[2].Role != core.RoleTool {
		t.Fatalf("repaired = %+v, want user/assistant/tool", out)
	}
	parts := out[2].Content
	if len(parts) != 2 {
		t.Fatalf("tool message = %+v, want the real result plus the synthesised one", parts)
	}
	if r := parts[0].(core.ToolResultPart); r.CallID != "b" || r.Content != "ok" {
		t.Errorf("part 0 = %+v, want the real b result kept", parts[0])
	}
	r := parts[1].(core.ToolResultPart)
	if r.CallID != "a" || r.Name != "t1" || !r.IsError ||
		r.Content != "no result recorded: the call was interrupted" {
		t.Errorf("synthesised result = %+v, want the pinned interrupted text", parts[1])
	}
}

// The Crush case: an assistant turn whose tools never ran (the run was
// interrupted) is repaired before the first model call, so a resumed
// session never locks.
func TestRepairDanglingLastAssistantTurn(t *testing.T) {
	in := []core.Message{
		core.User("go"),
		{Role: core.RoleAssistant, Content: []core.Part{
			core.ToolCallPart{ID: "c1", Name: "lookup", Args: json.RawMessage(`{}`)},
		}},
	}
	out := core.Repair(in)
	if len(out) != 3 || out[2].Role != core.RoleTool {
		t.Fatalf("repaired = %+v, want a tool message appended", out)
	}
	if r := out[2].Content[0].(core.ToolResultPart); !r.IsError || r.CallID != "c1" {
		t.Errorf("synthesised result = %+v", out[2].Content[0])
	}

	// The loop repairs its input: the model's first request already
	// carries the synthesised result.
	model := wefttest.Script(wefttest.Say("resumed"))
	_, err := core.New(model).Generate(context.Background(), core.Messages(in...))
	if err != nil {
		t.Fatal(err)
	}
	req := model.Requests()[0]
	if len(req.Messages) != 3 || req.Messages[2].Role != core.RoleTool {
		t.Fatalf("first request transcript = %+v, want the repaired input", req.Messages)
	}
}

// Orphans are dropped: results naming no preceding call, tool messages
// with nothing left, tool messages with no assistant before them, and a
// second consecutive tool message (the canonical shape batches one
// step's results on one message).
func TestRepairDropsOrphanResults(t *testing.T) {
	in := []core.Message{
		core.User("go"),
		{Role: core.RoleTool, Content: []core.Part{ // no assistant before it
			core.ToolResultPart{CallID: "x", Name: "t", Content: "orphan"},
		}},
		{Role: core.RoleAssistant, Content: []core.Part{
			core.ToolCallPart{ID: "a", Name: "t1", Args: json.RawMessage(`{}`)},
		}},
		{Role: core.RoleTool, Content: []core.Part{
			core.ToolResultPart{CallID: "ghost", Name: "t", Content: "orphan"}, // dropped part
		}},
		{Role: core.RoleTool, Content: []core.Part{ // second consecutive: dropped whole
			core.ToolResultPart{CallID: "a", Name: "t1", Content: "late"},
		}},
	}
	out := core.Repair(in)
	if len(out) != 3 {
		t.Fatalf("repaired = %d messages (%+v), want user/assistant/tool", len(out), out)
	}
	tool := out[2].Content
	if len(tool) != 1 {
		t.Fatalf("tool message = %+v, want only the synthesised a result", tool)
	}
	if r := tool[0].(core.ToolResultPart); r.CallID != "a" || !r.IsError {
		t.Errorf("result = %+v, want the synthesised a (both real ones were orphans)", tool[0])
	}

	// A tool message whose every part is orphaned is dropped whole.
	in = []core.Message{
		{Role: core.RoleAssistant, Content: []core.Part{
			core.ToolCallPart{ID: "a", Name: "t1", Args: json.RawMessage(`{}`)},
		}},
		{Role: core.RoleTool, Content: []core.Part{
			core.ToolResultPart{CallID: "ghost", Name: "t", Content: "orphan"},
		}},
	}
	out = core.Repair(in)
	if len(out) != 2 || len(out[1].Content) != 1 || !out[1].Content[0].(core.ToolResultPart).IsError {
		t.Errorf("repaired = %+v, want the orphan message replaced by synthesis", out)
	}
}

func TestRepairKeepsFirstDuplicate(t *testing.T) {
	in := []core.Message{
		{Role: core.RoleAssistant, Content: []core.Part{
			core.ToolCallPart{ID: "a", Name: "t1", Args: json.RawMessage(`{}`)},
		}},
		{Role: core.RoleTool, Content: []core.Part{
			core.ToolResultPart{CallID: "a", Name: "t1", Content: "first"},
			core.ToolResultPart{CallID: "a", Name: "t1", Content: "second"},
		}},
	}
	out := core.Repair(in)
	if len(out[1].Content) != 1 || out[1].Content[0].(core.ToolResultPart).Content != "first" {
		t.Errorf("repaired = %+v, want only the first result kept", out[1].Content)
	}
}

func TestRepairLeavesConsecutiveRolesAlone(t *testing.T) {
	in := []core.Message{
		core.User("one"),
		core.User("two"),
		core.Assistant("a"),
		core.Assistant("b"),
	}
	out := core.Repair(in)
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
	in := []core.Message{
		core.User("go"),
		{Role: core.RoleAssistant, Content: []core.Part{
			core.ToolCallPart{ID: "a", Name: "t1", Args: json.RawMessage(`{}`)},
		}},
		{Role: core.RoleTool, Content: []core.Part{
			core.ToolResultPart{CallID: "a", Name: "t1", Content: "ok"},
		}},
	}
	before, _ := json.Marshal(in)
	out := core.Repair(in)
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Errorf("Repair mutated its input:\n was %s\n now %s", before, after)
	}
	once, _ := json.Marshal(out)
	twice, _ := json.Marshal(core.Repair(out))
	if string(once) != string(twice) {
		t.Errorf("Repair is not idempotent:\n once  %s\n twice %s", once, twice)
	}
	// nil/empty corner: no nil-vs-empty surprises.
	if core.Repair(nil) != nil {
		t.Error("Repair(nil) must be nil")
	}
	if out := core.Repair([]core.Message{}); out == nil || len(out) != 0 {
		t.Errorf("Repair(empty) = %v, want empty non-nil", out)
	}
}

// RunStart names the model when it can (wefttest), and carries the zero
// ModelInfo when it cannot (an inline model without Info).
func TestRunStartCarriesModelInfo(t *testing.T) {
	first := func(agt *core.Agent) core.RunStart {
		t.Helper()
		var start core.RunStart
		for ev, err := range agt.Stream(context.Background(), core.Prompt("x")).Events() {
			if err != nil {
				t.Fatal(err)
			}
			if s, ok := ev.(core.RunStart); ok {
				start = s
			}
		}
		return start
	}
	if m := first(core.New(wefttest.Script(wefttest.Say("hi")))).Model; m != (core.ModelInfo{Provider: "wefttest", Name: "script"}) {
		t.Errorf("RunStart.Model = %+v, want the wefttest identity", m)
	}
	bare := &turnModel{turns: [][]core.ModelEvent{{
		core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}
	if m := first(core.New(bare)).Model; m != (core.ModelInfo{}) {
		t.Errorf("RunStart.Model = %+v, want the zero value for a model without Info", m)
	}
}

// StopFunc adapts an ordinary function where the built-ins do not fit;
// the interface keeps the manifest nameable (TODO §2.9).
func TestStopFuncAdaptsFunctions(t *testing.T) {
	touch := core.Tool("touch", "", func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "touch"}),
		wefttest.Say("unreachable"),
	)
	agt := core.New(model, core.StopWhen(core.StopFunc(func(steps []core.StepRecord) bool {
		return len(steps) >= 1
	})), touch)
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil || res.NumSteps() != 1 {
		t.Errorf("StopFunc: res=%v err=%v, want a successful 1-step run", res, err)
	}
}

// The manifest is deterministic: same agents, same bytes; tool order
// follows registration order and nothing else moves.
func TestManifestIsStable(t *testing.T) {
	x := core.Tool("x", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	y := core.Tool("y", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	newAgent := func(opts ...core.Option) *core.Agent {
		return core.New(wefttest.Script(wefttest.Say("ok")),
			append([]core.Option{core.Name("same")}, opts...)...)
	}
	one := newAgent(x, y)
	b1, err := core.Manifest(one)
	if err != nil {
		t.Fatal(err)
	}
	b1again, _ := core.Manifest(one)
	if string(b1) != string(b1again) {
		t.Error("Manifest is not deterministic across calls")
	}
	b2, err := core.Manifest(newAgent(y, x))
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
	agt := core.New(wefttest.Script(wefttest.Say("ok")),
		core.Name("support-bot"),
		core.Instructions("You are a support agent."),
		core.Tool("refund_order", "Refund a customer's order",
			func(_ context.Context, _ struct {
				OrderID string `json:"order_id"`
			}) (string, error) {
				return "refunded", nil
			}),
	)
	b, err := core.Manifest(agt)
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
	unnamed := core.New(wefttest.Script(wefttest.Say("ok")))
	if _, err := core.Manifest(unnamed); err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("unnamed agent error = %v, want one about the missing name", err)
	}
	named := func() *core.Agent {
		return core.New(wefttest.Script(wefttest.Say("ok")), core.Name("dup"))
	}
	if _, err := core.Manifest(named(), named()); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicate name error = %v, want one about duplicates", err)
	}
}

// output_schema is reflected from Out: absent for a verbatim string,
// scalar for scalars, object for structs.
func TestManifestOutputSchema(t *testing.T) {
	agt := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("schemas"),
		core.Tool("text_out", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil }),
		core.Tool("int_out", "", func(_ context.Context, _ struct{}) (int, error) { return 0, nil }),
		core.Tool("obj_out", "", func(_ context.Context, _ struct{}) (struct {
			Ok bool `json:"ok"`
		}, error) {
			return struct {
				Ok bool `json:"ok"`
			}{}, nil
		}),
	)
	b, err := core.Manifest(agt)
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
	sourced := core.Tool("sourced", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	b, err := core.Manifest(core.New(wefttest.Script(wefttest.Say("ok")), core.Name("a"), sourced))
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
	if got := fmt.Sprint(core.HasToolCall("a", "b")); got != "has_tool_call:a,b" {
		t.Errorf("HasToolCall name = %q, want has_tool_call:a,b", got)
	}
	if got := fmt.Sprint(core.StepCountIs(3)); got != "step_count_is:3" {
		t.Errorf("StepCountIs name = %q, want step_count_is:3", got)
	}
	agt := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("stops"),
		core.StopWhen(core.StepCountIs(2)),
		core.StopWhen(core.StopFunc(func([]core.StepRecord) bool { return false })),
	)
	b, err := core.Manifest(agt)
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
		ev   core.Event
		want string
	}{
		{core.RunStart{ID: "r1", Model: core.ModelInfo{Provider: "openai", Name: "gpt-5-mini"}, Agent: "support-bot"},
			`{"type":"run_start","id":"r1","model":{"provider":"openai","name":"gpt-5-mini"},"agent":"support-bot"}`},
		{core.RunStart{ID: "r2"},
			`{"type":"run_start","id":"r2","model":{"provider":"","name":""}}`},
		{core.StepStart{RunID: "r1", Index: 1}, `{"type":"step_start","run_id":"r1","index":1}`},
		{core.StepStart{Index: 2}, `{"type":"step_start","run_id":"","index":2}`},
		{core.TextDelta{RunID: "r1", Text: "hi"}, `{"type":"text_delta","run_id":"r1","text":"hi"}`},
		{core.ReasoningDelta{RunID: "r1", Text: "hm"}, `{"type":"reasoning_delta","run_id":"r1","text":"hm"}`},
		{core.ToolArgsDelta{RunID: "r1", Name: "write_file", Args: `{"content":"x`},
			`{"type":"tool_args_delta","run_id":"r1","name":"write_file","args":"{\"content\":\"x"}`},
		{core.ToolStart{RunID: "r1", Seq: 5, CallID: "c1", Name: "echo", Args: json.RawMessage(`{"m":"x"}`)},
			`{"type":"tool_start","run_id":"r1","seq":5,"call_id":"c1","name":"echo","args":{"m":"x"}}`},
		{core.ToolStart{RunID: "r1", Seq: 7, CallID: "c2", Name: "t"},
			`{"type":"tool_start","run_id":"r1","seq":7,"call_id":"c2","name":"t","args":null}`},
		{core.ToolFinish{RunID: "r1", Seq: 6, CallID: "c1", Name: "echo", Content: "ok", IsError: false},
			`{"type":"tool_finish","run_id":"r1","seq":6,"call_id":"c1","name":"echo","content":"ok","is_error":false}`},
		{core.StepFinish{RunID: "r1", Index: 1, Reason: core.StopToolCalls, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
			`{"type":"step_finish","run_id":"r1","index":1,"reason":"tool_calls","usage":{"input_tokens":10,"output_tokens":5}}`},
		{core.StepFinish{RunID: "r1", Index: 2, Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 1, OutputTokens: 1}, Raw: "refusal"},
			`{"type":"step_finish","run_id":"r1","index":2,"reason":"stop","usage":{"input_tokens":1,"output_tokens":1},"raw":"refusal"}`},
		// A4: the model call's timing, additive and omitted when 0.
		{core.StepFinish{RunID: "r1", Index: 3, Reason: core.StopEndTurn, LatencyMS: 812, TTFTMS: 140},
			`{"type":"step_finish","run_id":"r1","index":3,"reason":"stop","usage":{"input_tokens":0,"output_tokens":0},"latency_ms":812,"ttft_ms":140}`},
		{core.StepFinish{RunID: "r1", Index: 4, Reason: core.StopToolCalls, LatencyMS: 3},
			`{"type":"step_finish","run_id":"r1","index":4,"reason":"tool_calls","usage":{"input_tokens":0,"output_tokens":0},"latency_ms":3}`},
		{core.Steered{RunID: "r1", Seq: 3, Step: 0, Messages: []core.Message{core.User("use metric")}},
			`{"type":"steered","run_id":"r1","seq":3,"step":0,"messages":[{"role":"user","content":[{"type":"text","text":"use metric"}]}]}`},
		{core.Steered{RunID: "r1", Seq: 7, Step: 4},
			`{"type":"steered","run_id":"r1","seq":7,"step":4,"messages":null}`},
		{core.RunFinish{RunID: "r1", Usage: core.Usage{InputTokens: 20, OutputTokens: 10}, Steps: 2},
			`{"type":"run_finish","run_id":"r1","usage":{"input_tokens":20,"output_tokens":10},"steps":2}`},
		{core.Nested{RunID: "r1", Seq: 4, CallID: "c1", Event: core.ToolStart{RunID: "r1/0/c1", Seq: 1, CallID: "call_1", Name: "deep_search", Args: json.RawMessage(`{}`)}},
			`{"type":"nested","run_id":"r1","seq":4,"call_id":"c1","event":{"type":"tool_start","run_id":"r1/0/c1","seq":1,"call_id":"call_1","name":"deep_search","args":{}}}`},
		{core.Nested{RunID: "r1", Seq: 6, CallID: "c1", Event: core.Steered{RunID: "r1/0/c1", Seq: 2, Step: 0, Messages: []core.Message{core.User("left door")}}},
			`{"type":"nested","run_id":"r1","seq":6,"call_id":"c1","event":{"type":"steered","run_id":"r1/0/c1","seq":2,"step":0,"messages":[{"role":"user","content":[{"type":"text","text":"left door"}]}]}}`},
		{core.Nested{RunID: "r1", Seq: 5, CallID: "c1", Event: core.Nested{RunID: "r1/0/c1", Seq: 2, CallID: "call_1", Event: core.TextDelta{RunID: "r1/0/c1/0/call_1", Text: "deep"}}},
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
		back, err := core.UnmarshalEvent(b)
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
func normArgs(ev core.Event) core.Event {
	if s, ok := ev.(core.ToolStart); ok && string(s.Args) == "null" {
		s.Args = nil
		return s
	}
	return ev
}

func TestUnmarshalEventRejectsUnknownType(t *testing.T) {
	if _, err := core.UnmarshalEvent([]byte(`{"type":"hologram"}`)); err == nil || !strings.Contains(err.Error(), "hologram") {
		t.Errorf("unknown type error = %v, want one naming the type", err)
	}
	if _, err := core.UnmarshalEvent([]byte(`{"text":"no type"}`)); err == nil {
		t.Error("an event without a type discriminator must not decode")
	}
}

func TestStringOutputIsSentVerbatim(t *testing.T) {
	text := core.Tool("text", "", func(_ context.Context, _ struct{}) (string, error) { return "sunny in Paris", nil })
	obj := core.Tool("obj", "", func(_ context.Context, _ struct{}) (map[string]int, error) { return map[string]int{"n": 1}, nil })
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "text"}, wefttest.Call{Name: "obj"}),
		wefttest.Say("ok"),
	), text, obj)
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
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
	agt := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("a"), core.Name(""))
	if _, err := core.Manifest(agt); err != nil {
		t.Fatalf("Name(\"\") cleared the agent name: %v", err)
	}
}

// ErrStreamIdle is a run error like any provider error: the loop wraps
// it in RunError and callers branch on it provider-agnostically.
func TestErrStreamIdleIsARunError(t *testing.T) {
	agt := core.New(wefttest.Script(
		wefttest.Fail(fmt.Errorf("%w after 60s", core.ErrStreamIdle)),
	))
	_, err := agt.Generate(context.Background(), core.Prompt("x"))
	if !errors.Is(err, core.ErrStreamIdle) {
		t.Fatalf("err = %v, want ErrStreamIdle", err)
	}
	var re *core.RunError
	if !errors.As(err, &re) {
		t.Fatalf("err = %T, want *RunError", err)
	}
}

// ModelFinish.Raw is recorded, not interpreted: the step and the
// StepFinish event carry the provider's unmapped stop reason.
func TestModelFinishRawRecorded(t *testing.T) {
	rawTurn := []core.ModelEvent{
		core.ModelTextDelta{Text: "no"},
		core.ModelFinish{Reason: core.StopEndTurn, Raw: "refusal", Usage: core.Usage{InputTokens: 3, OutputTokens: 2}},
	}
	agt := core.New(&turnModel{turns: [][]core.ModelEvent{rawTurn}})
	var sawRaw string
	for ev, err := range agt.Stream(context.Background(), core.Prompt("x")).Events() {
		if err != nil {
			t.Fatal(err)
		}
		if f, ok := ev.(core.StepFinish); ok {
			sawRaw = f.Raw
		}
	}
	if sawRaw != "refusal" {
		t.Errorf("StepFinish.Raw = %q, want %q", sawRaw, "refusal")
	}
	agt = core.New(&turnModel{turns: [][]core.ModelEvent{rawTurn}})
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Steps[0].RawStopReason; got != "refusal" {
		t.Errorf("StepRecord.RawStopReason = %q, want %q", got, "refusal")
	}
	if got := res.Steps[0].StopReason; got != core.StopEndTurn {
		t.Errorf("StopReason = %q, want the mapped %q", got, core.StopEndTurn)
	}
}

// The kill switch is checked on every call (no cached state to fight);
// wefttest models ignore it. The default-allowed assertion is skipped
// when deny is ambient (WEFT_MODEL_REQUESTS=deny go test — the offline
// gate): the switch exists to keep that mode green, not to break it.
func TestModelRequestsAllowed(t *testing.T) {
	ambientDeny := os.Getenv("WEFT_MODEL_REQUESTS") == "deny"
	if !ambientDeny && !core.ModelRequestsAllowed() {
		t.Fatal("allowed by default")
	}

	t.Setenv("WEFT_MODEL_REQUESTS", "deny")
	if core.ModelRequestsAllowed() {
		t.Fatal("deny must disallow")
	}

	t.Setenv("WEFT_MODEL_REQUESTS", "allow")
	if !core.ModelRequestsAllowed() {
		t.Fatal("an explicit allow must allow")
	}

	// wefttest models ignore the switch: ordinary offline tests keep
	// running under deny.
	t.Setenv("WEFT_MODEL_REQUESTS", "deny")
	agt := core.New(wefttest.Script(wefttest.Say("offline")))
	if res, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil || res.Text() != "offline" {
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
	capital := core.RawTool("capital_of", "Return the capital of a country.",
		&core.Schema{
			Type: "object",
			Properties: map[string]*core.Schema{
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
	agt := core.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "capital_of", Args: `{"country":"France"}`}),
			wefttest.Say("done"),
		),
		core.Name("rawtool"),
		capital,
	)
	res, err := agt.Generate(context.Background(), core.Prompt("capital?"))
	if err != nil {
		t.Fatal(err)
	}
	var saw string
	for _, m := range res.Messages {
		for _, p := range m.Content {
			if tr, ok := p.(core.ToolResultPart); ok && tr.Name == "capital_of" {
				saw = tr.Content
			}
		}
	}
	if saw != "Paris" {
		t.Errorf("raw tool result = %q, want Paris", saw)
	}

	// The manifest renders the explicit schema and no source line.
	b, err := core.Manifest(agt)
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
	anyIn := core.RawTool("any_input", "takes anything", nil,
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
		core.RawTool("", "x", nil, func(context.Context, json.RawMessage) (string, error) { return "", nil })
	}()
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Error("nil handler must panic")
			}
		}()
		core.RawTool("x", "x", nil, nil)
	}()
}

// TestToolSource (bobina §9 W-2, ADR 0003 amendment): a tool registered
// between steps — here, by a tool handler — is callable by name in the
// very next step, with no agent rebuild; without a source, the static
// list stands (and unknown names stay ErrNoSuchTool).
func TestToolSource(t *testing.T) {
	mu := sync.Mutex{}
	dynamic := []*core.ToolDef{}

	mounter := core.Tool("mount_late", "Register a late tool in the test.",
		func(ctx context.Context, in struct {
			Name string `json:"name"`
		}) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			dynamic = append(dynamic, core.RawTool(in.Name, "mounted mid-run", nil,
				func(ctx context.Context, args json.RawMessage) (string, error) {
					return "from:" + in.Name, nil
				}))
			return "mounted " + in.Name, nil
		})

	agt := core.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "mount_late", Args: `{"name":"late_tool"}`}),
			wefttest.ToolCalls(wefttest.Call{Name: "late_tool", Args: `{}`}),
			wefttest.Say("done"),
		),
		core.Name("toolsource"),
		mounter,
		core.ToolSource(func() []*core.ToolDef {
			mu.Lock()
			defer mu.Unlock()
			return append([]*core.ToolDef{mounter}, dynamic...)
		}),
	)
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	var late string
	for _, m := range res.Messages {
		for _, p := range m.Content {
			if tr, ok := p.(core.ToolResultPart); ok && tr.Name == "late_tool" {
				late = tr.Content
			}
		}
	}
	if late != "from:late_tool" {
		t.Errorf("mid-run mounted tool result = %q; the source must refresh per step", late)
	}

	// CallTool consults the source too (the manual-dispatch seam).
	if out, err := agt.CallTool(context.Background(), core.ToolCallPart{Name: "late_tool", Args: []byte(`{}`)}); err != nil || out != "from:late_tool" {
		t.Errorf("CallTool via source: %q, %v", out, err)
	}
	// Unknown tools remain ErrNoSuchTool, not a silent nil.
	if _, err := agt.CallTool(context.Background(), core.ToolCallPart{Name: "never_mounted"}); !errors.Is(err, core.ErrNoSuchTool) {
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
	def := core.Tool("echo", "Echo the query.",
		func(_ context.Context, in struct {
			Q string `json:"q" jsonschema:"the query"`
		}) (string, error) {
			return "echo: " + in.Q, nil
		})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"q":"hi"}`}),
		wefttest.Say("done"),
	), def)

	def.Name = "renamed"
	def.Description = "hacked"
	def.InputSchema.Properties["q"].Type = "number"

	res, err := agt.Generate(context.Background(), core.Prompt("go"))
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
	def := core.Tool("echo", "d", func(_ context.Context, in struct {
		Q string `json:"q"`
	}) (string, error) {
		return in.Q, nil
	})
	agt := core.New(wefttest.Script(wefttest.Say("ok")), def)

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
	def := core.Tool("echo", "d", func(_ context.Context, in struct {
		Q string `json:"q"`
	}) (string, error) {
		return in.Q, nil
	})
	agt := core.New(wefttest.Script(
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
		if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
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
	echo := core.Tool("echo", "", func(_ context.Context, in struct {
		Q string `json:"q"`
	}) (string, error) {
		return "ok:" + in.Q, nil
	})
	agt := core.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"q":"hi"}`}),
			wefttest.Say("done"),
		),
		core.ToolSource(func() []*core.ToolDef {
			if fetches.Add(1) > 1 {
				return nil // the registry drops echo after step 0
			}
			return []*core.ToolDef{echo}
		}),
	)
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
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
	slow := core.Tool("slow", "", func(_ context.Context, _ struct{}) (string, error) {
		time.Sleep(10 * time.Millisecond) // outlast the sibling's dispatch
		return "s", nil
	})
	lonely := func(seq bool) *core.ToolDef {
		opts := []core.ToolOption{}
		if seq {
			opts = append(opts, core.Sequential())
		}
		return core.Tool("lonely", "", func(_ context.Context, _ struct{}) (string, error) {
			return "l", nil
		}, opts...)
	}
	agt := core.New(
		wefttest.Script(
			wefttest.ToolCalls(
				wefttest.Call{Name: "slow"},
				wefttest.Call{Name: "lonely"},
			),
			wefttest.Say("done"),
		),
		core.ToolSource(func() []*core.ToolDef {
			if fetches.Add(1) == 1 {
				return []*core.ToolDef{slow, lonely(true)}
			}
			return []*core.ToolDef{slow, lonely(false)}
		}),
	)
	var order []string
	for ev, err := range agt.Stream(context.Background(), core.Prompt("go")).Events() {
		if err != nil {
			t.Fatal(err)
		}
		switch e := ev.(type) {
		case core.ToolStart:
			order = append(order, ">"+e.Name)
		case core.ToolFinish:
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
	dup := func(tag string) *core.ToolDef {
		return core.Tool("dup", tag, func(_ context.Context, _ struct{}) (string, error) {
			return tag, nil
		})
	}
	agt := core.New(
		wefttest.Script(wefttest.Say("never reached")),
		core.ToolSource(func() []*core.ToolDef {
			return []*core.ToolDef{dup("a"), dup("b")}
		}),
	)
	_, err := agt.Generate(context.Background(), core.Prompt("go"))
	if !errors.Is(err, core.ErrDuplicateTool) {
		t.Errorf("run error = %v, want ErrDuplicateTool", err)
	}
	if _, err := agt.CallTool(context.Background(), core.ToolCallPart{Name: "dup"}); !errors.Is(err, core.ErrDuplicateTool) {
		t.Errorf("CallTool error = %v, want ErrDuplicateTool", err)
	}
}

// A ToolSource snapshot with a nil entry fails the run with ErrNilTool
// — a malformed snapshot is a run error, not a silently shortened tool
// list whose advertisement would dereference nil inside every adapter.
func TestToolSourceNilEntryFailsRun(t *testing.T) {
	tool := core.Tool("real", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ran", nil
	})
	agt := core.New(
		wefttest.Script(wefttest.Say("never reached")),
		core.ToolSource(func() []*core.ToolDef {
			return []*core.ToolDef{tool, nil}
		}),
	)
	_, err := agt.Generate(context.Background(), core.Prompt("go"))
	if !errors.Is(err, core.ErrNilTool) {
		t.Errorf("run error = %v, want ErrNilTool", err)
	}
	if _, err := agt.CallTool(context.Background(), core.ToolCallPart{Name: "real"}); !errors.Is(err, core.ErrNilTool) {
		t.Errorf("CallTool error = %v, want ErrNilTool", err)
	}
}

// Tool arguments must be exactly one JSON value — the loop-level pin
// of ADR 0002's 2026-09-18 amendment (the decode-level table lives in
// tooloption_test.go): a scripted call with trailing garbage after the
// arguments becomes an INVALID_INPUT result the model sees, not a
// decoded prefix. Siblings keep running.
func TestTrailingArgumentDataIsInvalidInput(t *testing.T) {
	tool := core.Tool("t", "", func(_ context.Context, _ struct {
		N int `json:"n"`
	}) (string, error) {
		return "ran", nil
	})
	agt := core.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "t", Args: `{"n":1} {"n":2}`}),
			wefttest.Say("ok"),
		),
		tool,
	)
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
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
	park := core.Tool("park", "", func(_ context.Context, _ struct{}) (string, error) {
		return "parked-ran", nil
	}, core.RequireApproval())
	plain := core.Tool("plain", "", func(_ context.Context, _ struct{}) (string, error) {
		return "plain-ran", nil
	})
	agt := core.New(
		wefttest.Script(wefttest.ToolCalls(
			wefttest.Call{ID: "p1", Name: "park"},
			wefttest.Call{ID: "c1", Name: "plain"},
		), wefttest.Say("done")),
		park, plain,
	)
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	res2, err := agt.Generate(context.Background(),
		core.Messages(res.Messages...), core.Approve("p1"), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	// The completed tool message: p1's fresh result, then c1's earlier
	// one — the assistant's order.
	var contents []string
	for _, msg := range res2.Messages {
		if msg.Role != core.RoleTool {
			continue
		}
		for _, p := range msg.Content {
			if r, ok := p.(core.ToolResultPart); ok {
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
type hostileModel struct{ inner core.Model }

func (h hostileModel) Info() core.ModelInfo { return core.InfoOf(h.inner) }

func (h hostileModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	req.Messages = append(req.Messages, core.User("injected"))
	req.Tools = append(req.Tools, nil)
	req.Tools[0] = nil
	return h.inner.Stream(ctx, req)
}

func TestModelRequestCopiesDefendTheRun(t *testing.T) {
	echo := core.Tool("echo", "", func(_ context.Context, in struct {
		Q string `json:"q"`
	}) (string, error) {
		return in.Q, nil
	})
	agt := core.New(hostileModel{wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"q":"hi"}`}),
		wefttest.Say("done"),
	)}, echo)
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Messages {
		if m.Role == core.RoleUser && m.Text() == "injected" {
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
	echo := core.Tool("echo", "", func(_ context.Context, in struct {
		Q string `json:"q"`
	}) (string, error) {
		return in.Q, nil
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"q":"hi"}`}),
		wefttest.Say("done"),
	), echo, core.PrepareStep(func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) {
		req.Tools[0].Description = "sabotaged"
		req.Messages[0].Content[0] = core.TextPart{Text: "injected"}
		for i := range req.Messages {
			if c, ok := req.Messages[i].Content[0].(core.ToolCallPart); ok {
				c.Args = json.RawMessage(`{"q":"hacked"}`)
				req.Messages[i].Content[0] = c
			}
		}
		return req, nil
	}))
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
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
			if c, ok := p.(core.ToolCallPart); ok {
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

func (stepModel) Info() core.ModelInfo { return core.ModelInfo{Provider: "wefttest", Name: "step"} }

func (stepModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	answered := false
	for _, m := range req.Messages {
		if m.Role == core.RoleAssistant {
			answered = true
		}
	}
	return func(yield func(core.ModelEvent, error) bool) {
		if answered {
			yield(core.ModelTextDelta{Text: "done"}, nil)
			yield(core.ModelFinish{Reason: core.StopEndTurn}, nil)
			return
		}
		yield(core.ModelToolCall{ID: "c1", Name: "ping", Args: json.RawMessage(`{}`)}, nil)
		yield(core.ModelFinish{Reason: core.StopToolCalls}, nil)
	}
}

func eventRunID(ev core.Event) string {
	switch e := ev.(type) {
	case core.RunStart:
		return e.ID
	case core.StepStart:
		return e.RunID
	case core.TextDelta:
		return e.RunID
	case core.ReasoningDelta:
		return e.RunID
	case core.ToolArgsDelta:
		return e.RunID
	case core.ToolStart:
		return e.RunID
	case core.ToolFinish:
		return e.RunID
	case core.StepFinish:
		return e.RunID
	case core.RunFinish:
		return e.RunID
	}
	return "?"
}

// Every event carries its run's id, so a tap watching concurrent runs on
// one agent can attribute each event — per-run Seq counters are unique
// only within their run (Fix 6).
func TestEventsCarryRunIDUnderConcurrency(t *testing.T) {
	ping := core.Tool("ping", "", func(_ context.Context, _ struct{}) (string, error) {
		return "pong", nil
	})
	var mu sync.Mutex
	buckets := map[string][]core.Event{}
	agt := core.New(stepModel{}, ping,
		core.Tap(func(_ context.Context, ev core.Event) {
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
			if _, err := agt.Generate(context.Background(), core.Prompt("go"), core.RunID(id)); err != nil {
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
		if _, ok := evs[0].(core.RunStart); !ok {
			t.Errorf("%s: first event is %T, want RunStart", id, evs[0])
		}
		if _, ok := evs[len(evs)-1].(core.RunFinish); !ok {
			t.Errorf("%s: last event is %T, want RunFinish", id, evs[len(evs)-1])
		}
		var seqs []int64
		for _, ev := range evs {
			if got := eventRunID(ev); got != id {
				t.Errorf("%s: event %T attributed to %q", id, ev, got)
			}
			switch e := ev.(type) {
			case core.ToolStart:
				seqs = append(seqs, e.Seq)
			case core.ToolFinish:
				seqs = append(seqs, e.Seq)
			}
		}
		if !slices.Equal(seqs, []int64{1, 2}) {
			t.Errorf("%s: tool event seqs = %v, want [1 2] within the run", id, seqs)
		}
	}

	// Legacy recordings without run_id still decode (RunID is additive).
	ev, err := core.UnmarshalEvent([]byte(`{"type":"step_start","index":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if ss := ev.(core.StepStart); ss.Index != 3 || ss.RunID != "" {
		t.Errorf("legacy step_start = %+v, want index 3 and empty RunID", ss)
	}
}

// Events are snapshots: writing into a received ToolStart's Args must
// not corrupt the transcript the run is building (Fix 4, option b).
func TestEventArgsAreSnapshots(t *testing.T) {
	echo := core.Tool("echo", "", func(_ context.Context, in struct {
		Q string `json:"q"`
	}) (string, error) {
		return in.Q, nil
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"q":"hi"}`}),
		wefttest.Say("done"),
	), echo)
	run := agt.Stream(context.Background(), core.Prompt("go"))
	for ev, err := range run.Events() {
		if err != nil {
			t.Fatal(err)
		}
		if ts, ok := ev.(core.ToolStart); ok {
			copy(ts.Args, []byte(`ZZ`)) // in-place write into the event's copy
		}
	}
	res, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range res.Messages {
		for _, p := range m.Content {
			if c, ok := p.(core.ToolCallPart); ok && string(c.Args) != `{"q":"hi"}` {
				t.Errorf("transcript args = %s; event mutation leaked into the transcript", c.Args)
			}
		}
	}
}

// RunFinish.Pending is a snapshot too: writing into the event's pending
// args must not reach the run result or the transcript (Fix 4, option b).
func TestRunFinishPendingArgsAreSnapshots(t *testing.T) {
	gate := core.Tool("gate", "", func(_ context.Context, _ struct{}) (string, error) {
		return "never", nil
	}, core.RequireApproval())
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "gate", Args: `{"secret":"1"}`}),
	), gate)
	run := agt.Stream(context.Background(), core.Prompt("go"))
	for ev, err := range run.Events() {
		if err != nil {
			t.Fatal(err)
		}
		if rf, ok := ev.(core.RunFinish); ok && len(rf.Pending) > 0 {
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
			if c, ok := p.(core.ToolCallPart); ok && string(c.Args) != `{"secret":"1"}` {
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
	agt := core.New(wefttest.Script(wefttest.Say("never consumed")),
		core.Tool("noop", "", func(_ context.Context, _ struct{}) (string, error) {
			return "", nil
		}))
	run := agt.Stream(parent, core.Prompt("go"))
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
	done := agt.Stream(context.Background(), core.Prompt("go"))
	if _, err := done.Wait(); err != nil {
		t.Fatal(err)
	}
	done.Close()
}

// A panicking tap is contained and counted: the run completes, and
// TapPanics reports exactly the number of matching invocations (Fix 12).
func TestTapPanicsContainedAndCounted(t *testing.T) {
	agt := core.New(
		wefttest.Script(wefttest.Say("hello")),
		core.Tap(func(_ context.Context, ev core.Event) {
			if _, ok := ev.(core.TextDelta); ok {
				panic("observer bug")
			}
		}),
	)
	if got := agt.TapPanics(); got != 0 {
		t.Fatalf("TapPanics before any run = %d, want 0", got)
	}
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
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
	parts := make([]core.Part, 3, 8) // deliberate spare capacity
	parts[0] = core.ToolResultPart{CallID: "a", Name: "t", Content: "ok"}
	parts[1] = core.ToolResultPart{CallID: "b", Name: "t", Content: "ok"}
	parts[2] = core.ToolResultPart{CallID: "c", Name: "t", Content: "ok"}
	spare := parts[:8] // a second window onto the whole array
	in := []core.Message{
		{Role: core.RoleAssistant, Content: []core.Part{
			core.ToolCallPart{ID: "a", Name: "t", Args: json.RawMessage(`{}`)},
			core.ToolCallPart{ID: "b", Name: "t", Args: json.RawMessage(`{}`)},
			core.ToolCallPart{ID: "c", Name: "t", Args: json.RawMessage(`{}`)},
			core.ToolCallPart{ID: "d", Name: "t", Args: json.RawMessage(`{}`)}, // missing result
		}},
		{Role: core.RoleTool, Content: parts},
	}
	out := core.Repair(in)
	if len(out) < 2 || out[1].Role != core.RoleTool {
		t.Fatalf("repair shape = %+v, want the kept tool message", out)
	}
	var served []string
	for _, p := range out[1].Content {
		if r, ok := p.(core.ToolResultPart); ok {
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
func streamEvents(t *testing.T, agt *core.Agent, opts ...core.RunOption) ([]core.Event, *core.RunResult) {
	t.Helper()
	run := agt.Stream(context.Background(), opts...)
	var evs []core.Event
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
	child := core.New(wefttest.Script(wefttest.Say("found: 3 orders")))
	def := core.Subagent("research", "Research a topic.", child, core.Timeout(30*time.Second))
	if def.Name != "research" {
		t.Fatalf("tool name = %q", def.Name)
	}
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"find orders"}`}),
		wefttest.Say("done"),
	), def)
	if _, ok := agt.Tools()[0].InputSchema.Properties["prompt"]; !ok {
		t.Error("the subagent tool did not register with New")
	}
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Steps[0].Results[0]; r.IsError || r.Content != "found: 3 orders" {
		t.Errorf("result = %+v, want the child's final text", r)
	}
	// The per-tool option reached the manifest, as for any tool.
	b, err := core.Manifest(core.New(wefttest.Script(wefttest.Say("ok")), core.Name("a"), def))
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
	child := core.New(wefttest.Script(wefttest.Say("ok")))
	def := core.Subagent("research", "Research a topic.", child)
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
	child := core.New(wefttest.Script(wefttest.Say("found it")))
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"p"}`}),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child))
	evs, _ := streamEvents(t, parent, core.Prompt("q"), core.RunID("r1"))

	var last int64
	var nested []core.Event
	finishIdx := -1
	for i, ev := range evs {
		switch e := ev.(type) {
		case core.ToolStart:
			if e.Seq <= last {
				t.Fatalf("ToolStart Seq %d not after %d", e.Seq, last)
			}
			last = e.Seq
		case core.ToolFinish:
			if e.Seq <= last {
				t.Fatalf("ToolFinish Seq %d not after %d", e.Seq, last)
			}
			last = e.Seq
			finishIdx = i
			if e.Content != "found it" {
				t.Errorf("finish content = %q", e.Content)
			}
		case core.Nested:
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
		if n, ok := ev.(core.Nested); ok && n.CallID == "call_1" {
			t.Errorf("Nested delivered after the call's ToolFinish: %+v", n)
		}
	}
	kinds := make([]string, len(nested))
	for i, ev := range nested {
		kinds[i] = fmt.Sprintf("%T", ev)
	}
	want := []string{"core.RunStart", "core.StepStart", "core.TextDelta", "core.StepFinish", "core.RunFinish"}
	if !slices.Equal(kinds, want) {
		t.Errorf("child event kinds = %v, want %v", kinds, want)
	}
}

// Nested round-trips the wire byte-exactly, recursing into the inner
// event; an unknown inner type is an error, never a drop.
func TestNestedEventRoundTrip(t *testing.T) {
	outer := core.Nested{RunID: "r1", Seq: 9, CallID: "c1",
		Event: core.Nested{RunID: "r1/0/c1", Seq: 3, CallID: "call_1",
			Event: core.ToolStart{RunID: "r1/0/c1/0/call_1", Seq: 1, CallID: "p1", Name: "deep_search", Args: json.RawMessage(`{}`)}}}
	b, err := json.Marshal(outer)
	if err != nil {
		t.Fatal(err)
	}
	back, err := core.UnmarshalEvent(b)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := json.Marshal(back)
	if err != nil || string(b2) != string(b) {
		t.Errorf("round trip unstable:\n first %s\n second %s (%v)", b, b2, err)
	}
	if _, err := core.UnmarshalEvent([]byte(`{"type":"nested","run_id":"r1","seq":1,"call_id":"c","event":{"type":"nope"}}`)); err == nil {
		t.Error("an unknown inner event type decoded without error")
	}
}

// Child usage is added to the run total and recorded per call;
// StepRecord.Usage and StepFinish.Usage stay the model call's own.
func TestSubagentUsageRollsUp(t *testing.T) {
	child := core.New(wefttest.Script(wefttest.Say("ok")))
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child))
	evs, res := streamEvents(t, parent, core.Prompt("q"))
	// 2 parent steps × (10/5) + 1 child step × (10/5).
	wantTotal := core.Usage{InputTokens: 30, OutputTokens: 15}
	if res.Usage != wantTotal {
		t.Errorf("RunResult.Usage = %+v, want %+v", res.Usage, wantTotal)
	}
	step := res.Steps[0]
	if step.Usage != (core.Usage{InputTokens: 10, OutputTokens: 5}) {
		t.Errorf("StepRecord.Usage = %+v, want the model call's own", step.Usage)
	}
	if got := step.SubagentUsage["call_1"]; got != (core.Usage{InputTokens: 10, OutputTokens: 5}) {
		t.Errorf("SubagentUsage[call_1] = %+v", got)
	}
	if res.Steps[1].SubagentUsage != nil {
		t.Errorf("step 1 SubagentUsage = %v, want nil (no subagent ran)", res.Steps[1].SubagentUsage)
	}
	var sf core.StepFinish
	for _, ev := range evs {
		if e, ok := ev.(core.StepFinish); ok && e.Index == 0 {
			sf = e
		}
	}
	if sf.Usage != (core.Usage{InputTokens: 10, OutputTokens: 5}) {
		t.Errorf("StepFinish.Usage = %+v, want the model call's own", sf.Usage)
	}
}

// A failed child is data: SUBAGENT_FAILED reaches the model, the parent
// run continues, and the child's *RunError is reachable through
// ToolError.Err for middleware.
func TestSubagentFailureIsData(t *testing.T) {
	boomed := errors.New("provider down")
	child := core.New(wefttest.Script(wefttest.Fail(boomed)))
	var childErr *core.RunError
	spy := func(next core.ToolCaller) core.ToolCaller {
		return func(ctx context.Context, call core.ToolCallPart) (string, error) {
			out, err := next(ctx, call)
			var te *core.ToolError
			if errors.As(err, &te) {
				errors.As(te.Err, &childErr)
			}
			return out, err
		}
	}
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("recovered"),
	), core.Subagent("research", "Research.", child), core.WrapTools(spy))
	res, err := parent.Generate(context.Background(), core.Prompt("q"))
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
	probe := core.Tool("probe", "", func(ctx context.Context, _ struct{}) (string, error) {
		close(started)
		<-ctx.Done()
		close(childCancelled)
		return "never", nil
	})
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "probe"}),
		wefttest.Say("ok"),
	), probe)
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err := parent.Generate(ctx, core.Prompt("q"))
	var re *core.RunError
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
	probe := core.Tool("probe", "", func(ctx context.Context, _ struct{}) (string, error) {
		<-ctx.Done() // honours the deadline
		return "late", nil
	})
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "probe"}),
		wefttest.Say("ok"),
	), probe)
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child, core.Timeout(50*time.Millisecond)))
	evs, _ := streamEvents(t, parent, core.Prompt("q"))
	finishIdx, sawNested := -1, false
	for i, ev := range evs {
		switch e := ev.(type) {
		case core.Nested:
			if e.CallID == "call_1" {
				sawNested = true
			}
		case core.ToolFinish:
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
		if n, ok := ev.(core.Nested); ok && n.CallID == "call_1" {
			t.Errorf("Nested delivered after the timed-out finish: %+v", n)
		}
	}
}

// A child that ends pending is a loud tool error: the parent's
// transcript has nowhere to carry the child's approval decision.
func TestSubagentPendingIsLoud(t *testing.T) {
	pay := core.Tool("pay", "", func(_ context.Context, _ struct{}) (string, error) {
		return "paid", nil
	}, core.RequireApproval())
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "pay"}),
		wefttest.Say("never reached"),
	), pay)
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("recovered"),
	), core.Subagent("research", "Research.", child))
	res, err := parent.Generate(context.Background(), core.Prompt("q"))
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
	var a *core.Agent
	b := core.New(bModel, core.ToolSource(func() []*core.ToolDef {
		return []*core.ToolDef{core.Subagent("use_a", "Back to A.", a)}
	}))
	a = core.New(aModel, core.Subagent("use_b", "To B.", b))
	evs, res := streamEvents(t, a, core.Prompt("q"), core.RunID("r1"))
	if r := res.Steps[0].Results[0]; r.IsError || r.Content != "b done" {
		t.Errorf("outer delegation = %+v", r)
	}
	var cycle string
	for _, ev := range wefttest.Flatten(evs) {
		if f, ok := ev.(core.ToolFinish); ok && f.Name == "use_a" {
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
	var childCall core.Call
	ping := core.Tool("ping", "", func(ctx context.Context, _ struct{}) (string, error) {
		childCall, _ = core.CallFromContext(ctx)
		return "pong", nil
	})
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "ping"}),
		wefttest.Say("ok"),
	), ping)
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", ID: "call_9"}),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child))
	evs, _ := streamEvents(t, parent, core.Prompt("q"), core.RunID("r1"))
	var ids []string
	for _, ev := range wefttest.Flatten(evs) {
		if rs, ok := ev.(core.RunStart); ok {
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
	var childCall core.Call
	ping := core.Tool("ping", "", func(ctx context.Context, _ struct{}) (string, error) {
		childCall, _ = core.CallFromContext(ctx)
		return "pong", nil
	})
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "ping"}),
		wefttest.Say("ok"),
	), ping)
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", ID: "call_9"}),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child, core.RequireApproval()))
	res, err := parent.Generate(context.Background(), core.Prompt("q"), core.RunID("r1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 || res.Pending[0].ID != "call_9" {
		t.Fatalf("pending = %+v, want the parked research call", res.Pending)
	}
	evs, _ := streamEvents(t, parent,
		core.Messages(res.Messages...), core.Approve("call_9"), core.RunID("r1"))
	var ids []string
	for _, ev := range wefttest.Flatten(evs) {
		if rs, ok := ev.(core.RunStart); ok {
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
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "submit_output", Args: `{"approved":true,"reason":"ok"}`}),
	), core.Output[Verdict]())
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child))
	res, err := parent.Generate(context.Background(), core.Prompt("q"))
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
	child := core.New(&turnModel{turns: [][]core.ModelEvent{{
		core.ModelTextDelta{Text: "submitting empty"},
		core.ModelToolCall{ID: "call_1", Name: "submit_output"},
		core.ModelFinish{Reason: core.StopToolCalls, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
	}}}, core.Output[Verdict]())
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child))
	res, err := parent.Generate(context.Background(), core.Prompt("q"))
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
	var childSeen, parentSeen []core.Event
	child := core.New(wefttest.Script(wefttest.Say("hi")),
		core.Tap(func(_ context.Context, ev core.Event) { childSeen = append(childSeen, ev) }))
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child),
		core.Tap(func(_ context.Context, ev core.Event) { parentSeen = append(parentSeen, ev) }))
	if _, err := parent.Generate(context.Background(), core.Prompt("q"), core.RunID("p1")); err != nil {
		t.Fatal(err)
	}
	sawText := false
	for _, ev := range childSeen {
		if _, ok := ev.(core.Nested); ok {
			t.Error("the child's tap saw a Nested wrapper")
		}
		if _, ok := ev.(core.TextDelta); ok {
			sawText = true
		}
	}
	if !sawText {
		t.Error("the child's tap saw no raw TextDelta")
	}
	sawNested := false
	for _, ev := range parentSeen {
		if td, ok := ev.(core.TextDelta); ok && td.RunID != "p1" {
			t.Errorf("the parent's tap saw a bare child event: %+v", td)
		}
		if _, ok := ev.(core.Nested); ok {
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
	var childEvents []core.Event
	child := core.New(wefttest.Script(wefttest.Say("standalone"), wefttest.Say("standalone")),
		core.Tap(func(_ context.Context, ev core.Event) { childEvents = append(childEvents, ev) }))
	def := core.Subagent("research", "Research.", child)
	out, err := def.Invoke(context.Background(), json.RawMessage(`{"prompt":"hi"}`))
	if err != nil || out != "standalone" {
		t.Fatalf("Invoke = %q, %v", out, err)
	}
	agt := core.New(wefttest.Script(wefttest.Say("x")), def)
	out2, err := agt.CallTool(context.Background(), core.ToolCallPart{ID: "c1", Name: "research", Args: json.RawMessage(`{"prompt":"hi"}`)})
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
	child := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("researcher"))
	parent := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("orchestrator"),
		core.Subagent("research", "Research.", child))
	b, err := core.Manifest(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"subagent": "researcher"`) {
		t.Errorf("manifest missing the delegation edge:\n%s", b)
	}
	anon := core.New(wefttest.Script(wefttest.Say("ok")))
	parent2 := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("p2"),
		core.Subagent("research", "Research.", anon))
	b2, err := core.Manifest(parent2)
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
	core.Subagent("research", "Research.", nil)
}

// A grandchild event is a Nested inside a Nested, in the child's
// emission order.
func TestSubagentGrandchildDoubleWrap(t *testing.T) {
	c := core.New(wefttest.Script(wefttest.Say("deep")))
	b := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "grand"}),
		wefttest.Say("mid"),
	), core.Subagent("grand", "To C.", c))
	a := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "use_b"}),
		wefttest.Say("top"),
	), core.Subagent("use_b", "To B.", b))
	evs, _ := streamEvents(t, a, core.Prompt("q"), core.RunID("r1"))
	var deep bool
	for _, ev := range evs {
		n, ok := ev.(core.Nested)
		if !ok {
			continue
		}
		inner, ok := n.Event.(core.Nested)
		if !ok {
			continue
		}
		if n.RunID != "r1" || n.CallID != "call_1" {
			t.Errorf("outer envelope = %+v", n)
		}
		if inner.RunID != "r1/0/call_1" || inner.CallID != "call_1" {
			t.Errorf("inner envelope = %+v", inner)
		}
		if td, ok := inner.Event.(core.TextDelta); ok && td.Text == "deep" {
			deep = true
		}
	}
	if !deep {
		t.Error("no double-wrapped grandchild TextDelta in the parent stream")
	}
}

// Generate consumes no events, but the child's usage still rolls up.
func TestSubagentUnderGenerateStillRollsUsage(t *testing.T) {
	child := core.New(wefttest.Script(wefttest.Say("ok")))
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child))
	res, err := parent.Generate(context.Background(), core.Prompt("q"))
	if err != nil {
		t.Fatal(err)
	}
	if want := (core.Usage{InputTokens: 30, OutputTokens: 15}); res.Usage != want {
		t.Errorf("usage = %+v, want %+v", res.Usage, want)
	}
}

// Cancelling the parent while a child is mid-run, under Stream: the
// error is the cancellation, it is the last element, and no RunFinish
// (parent or nested) is delivered — rule 4 holds one level down.
func TestSubagentCancelMidChildUnderStream(t *testing.T) {
	started := make(chan struct{})
	probe := core.Tool("probe", "", func(ctx context.Context, _ struct{}) (string, error) {
		close(started)
		<-ctx.Done()
		return "never", nil
	})
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "probe"}),
		wefttest.Say("ok"),
	), probe)
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child))
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	var gotErr error
	for ev, err := range parent.Stream(ctx, core.Prompt("q")).Events() {
		if err != nil {
			gotErr = err
			continue
		}
		if gotErr != nil {
			t.Fatalf("event delivered after the error: %+v", ev)
		}
		switch e := ev.(type) {
		case core.RunFinish:
			t.Error("RunFinish delivered on a cancelled run")
		case core.Nested:
			if _, ok := e.Event.(core.RunFinish); ok {
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
	flaky := core.Tool("parse_date", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", core.ModelRetry("date must be ISO-8601")
	})
	turns := []wefttest.Turn{}
	for range 3 {
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "parse_date"}))
	}
	child := core.New(wefttest.Script(append(turns, wefttest.Say("child done"))...), flaky)
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child), core.MaxModelRetries(1))
	res, err := parent.Generate(context.Background(), core.Prompt("q"))
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
	child := core.New(wefttest.Script(
		wefttest.Say("found"), wefttest.Say("found"), wefttest.Say("found"), wefttest.Say("found"),
	))
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}, wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
		wefttest.ToolCalls(wefttest.Call{Name: "research"}, wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child))
	var wg sync.WaitGroup
	for _, id := range []string{"A", "B"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var last int64
			for ev, err := range parent.Stream(context.Background(), core.Prompt("q"), core.RunID(id)).Events() {
				if err != nil {
					t.Errorf("%s: %v", id, err)
					return
				}
				var seq int64
				switch e := ev.(type) {
				case core.Nested:
					if e.RunID != id {
						t.Errorf("Nested.RunID = %q, want %q", e.RunID, id)
					}
					seq = e.Seq
				case core.ToolStart:
					seq = e.Seq
				case core.ToolFinish:
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
	echo := core.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("never reached"),
	), echo, core.UsageLimit(core.Usage{OutputTokens: 4})) // Say spends 5
	_, err := agt.Generate(context.Background(), core.Prompt("q"))
	var re *core.RunError
	if !errors.As(err, &re) || !errors.Is(err, core.ErrUsageLimit) {
		t.Fatalf("err = %v, want ErrUsageLimit", err)
	}
	if re.Step != 0 {
		t.Errorf("failed at step %d, want 0", re.Step)
	}
	msgs := re.Result.Messages
	if last := msgs[len(msgs)-1]; last.Role != core.RoleTool {
		t.Errorf("transcript ends on %v, want the tool message", last.Role)
	}
	if re.Result.Steps[0].StopReason != core.StopToolCalls {
		t.Errorf("last step stop reason = %v", re.Result.Steps[0].StopReason)
	}
}

// A step that ends the run may overshoot the limit and still succeed:
// the budget stops further spend, it does not discard finished work.
// WithUsage lifts the turn off the fixed 10/5 so the overshoot is
// explicit (TODO §9.3).
func TestUsageLimitFinalStepMayOvershoot(t *testing.T) {
	agt := core.New(wefttest.Script(
		wefttest.Say("done").WithUsage(core.Usage{OutputTokens: 40})),
		core.UsageLimit(core.Usage{OutputTokens: 4}))
	res, err := agt.Generate(context.Background(), core.Prompt("q"))
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
	plain := core.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	solo := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("done"),
	), plain, core.UsageLimit(core.Usage{OutputTokens: 8}))
	if _, err := solo.Generate(context.Background(), core.Prompt("q")); err != nil {
		t.Fatalf("solo run under the limit failed: %v", err)
	}
	child := core.New(wefttest.Script(wefttest.Say("found")))
	withSub := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research"}),
		wefttest.Say("done"),
	), core.Subagent("research", "Research.", child), core.UsageLimit(core.Usage{OutputTokens: 8}))
	_, err := withSub.Generate(context.Background(), core.Prompt("q"))
	if !errors.Is(err, core.ErrUsageLimit) {
		t.Fatalf("err = %v, want ErrUsageLimit once the child's usage rolls in", err)
	}
}

// The manifest records the limit, omitting zero fields.
func TestManifestUsageLimit(t *testing.T) {
	agt := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("a"),
		core.UsageLimit(core.Usage{OutputTokens: 50_000}))
	b, err := core.Manifest(agt)
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
	flaky := core.Tool("parse_date", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", core.ModelRetry("date must be ISO-8601")
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "parse_date", Args: `{"d":"tomorrow"}`}),
		wefttest.Say("ok"),
	), flaky)
	res, err := agt.Generate(context.Background(), core.Prompt("q"))
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
	newAgent := func(retryFirst int) (*core.Agent, *wefttest.Model) {
		var calls atomic.Int32
		tool := core.Tool("parse_date", "", func(_ context.Context, _ struct{}) (string, error) {
			if int(calls.Add(1)) <= retryFirst {
				return "", core.ModelRetry("date must be ISO-8601")
			}
			return "2026-09-19", nil
		})
		turns := []wefttest.Turn{}
		for range retryFirst + 1 {
			turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "parse_date"}))
		}
		m := wefttest.Script(append(turns, wefttest.Say("done"))...)
		return core.New(m, tool), m
	}

	stuck, _ := newAgent(4) // never succeeds
	_, err := stuck.Generate(context.Background(), core.Prompt("q"))
	var re *core.RunError
	if !errors.As(err, &re) || !errors.Is(err, core.ErrModelRetriesExceeded) {
		t.Fatalf("err = %v, want ErrModelRetriesExceeded", err)
	}
	if re.Step != 3 {
		t.Errorf("failed at step %d, want 3 (the fourth consecutive RETRY)", re.Step)
	}
	msgs := re.Result.Messages
	if last := msgs[len(msgs)-1]; last.Role != core.RoleTool {
		t.Errorf("transcript ends on %v, want the tool message", last.Role)
	}

	healing, m := newAgent(3) // three retries, then it succeeds
	res, err := healing.Generate(context.Background(), core.Prompt("q"))
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
	tool := core.Tool("parse_date", "", func(_ context.Context, _ struct{}) (string, error) {
		return "fine", nil
	})
	var n atomic.Int32
	nag := func(next core.ToolCaller) core.ToolCaller {
		return func(ctx context.Context, call core.ToolCallPart) (string, error) {
			if int(n.Add(1)) <= 4 {
				return "", core.ModelRetry("middleware says no")
			}
			return next(ctx, call)
		}
	}
	turns := []wefttest.Turn{}
	for range 4 {
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "parse_date"}))
	}
	agt := core.New(wefttest.Script(turns...), tool, core.WrapTools(nag))
	_, err := agt.Generate(context.Background(), core.Prompt("q"))
	if !errors.Is(err, core.ErrModelRetriesExceeded) {
		t.Fatalf("err = %v, want ErrModelRetriesExceeded from middleware retries", err)
	}
}

// MaxModelRetries ignores values below 1: the default 3 stands.
func TestMaxModelRetriesIgnoresZero(t *testing.T) {
	tool := core.Tool("parse_date", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", core.ModelRetry("no")
	})
	turns := []wefttest.Turn{}
	for range 4 {
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "parse_date"}))
	}
	agt := core.New(wefttest.Script(turns...), tool, core.MaxModelRetries(0))
	_, err := agt.Generate(context.Background(), core.Prompt("q"))
	if !errors.Is(err, core.ErrModelRetriesExceeded) {
		t.Fatalf("err = %v, want the default budget of 3 to stand", err)
	}
}

// --- Loop detection (TODO §5.4) ---

func loopTool() *core.ToolDef {
	return core.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
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
	agt := core.New(wefttest.Script(turns...), loopTool(), core.DetectLoops(5))
	_, err := agt.Generate(context.Background(), core.Prompt("q"))
	var re *core.RunError
	if !errors.As(err, &re) || !errors.Is(err, core.ErrLoopDetected) {
		t.Fatalf("err = %v, want ErrLoopDetected", err)
	}
	if re.Step != 4 {
		t.Errorf("failed at step %d, want 4 (the fifth identical step)", re.Step)
	}
	msgs := re.Result.Messages
	if last := msgs[len(msgs)-1]; last.Role != core.RoleTool {
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
	agt := core.New(wefttest.Script(turns...), loopTool(), core.DetectLoops(3))
	res, err := agt.Generate(context.Background(), core.Prompt("q"))
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
	echoes := []*core.ToolDef{loopTool(), loopTool()}
	echoes[0].Name = "a"
	echoes[1].Name = "b"
	agt := core.New(wefttest.Script(turns...), core.DetectLoops(3), echoes[0], echoes[1])
	if _, err := agt.Generate(context.Background(), core.Prompt("q")); !errors.Is(err, core.ErrLoopDetected) {
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
	agt := core.New(wefttest.Script(turns...), loopTool())
	if _, err := agt.Generate(context.Background(), core.Prompt("q")); err != nil {
		t.Fatalf("err = %v, want success without the option", err)
	}
}

// The manifest records the setting only when on.
func TestManifestDetectLoops(t *testing.T) {
	agt := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("a"), core.DetectLoops(5))
	b, err := core.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"detect_loops": 5`) {
		t.Errorf("manifest missing detect_loops:\n%s", b)
	}
	off := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("b"))
	b2, _ := core.Manifest(off)
	if strings.Contains(string(b2), `"detect_loops"`) {
		t.Errorf("detect_loops should be omitted when off:\n%s", b2)
	}
}

// --- PrepareStep (TODO §5.5) ---

// recordModel captures the ModelRequest a middleware chain forwards.
type recordModel struct {
	next core.Model
	seen *core.ModelRequest
}

func (m recordModel) Info() core.ModelInfo { return core.InfoOf(m.next) }

func (m recordModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	*m.seen = req
	return m.next.Stream(ctx, req)
}

// The prepared tool list is both the advertisement and the dispatch
// snapshot: a tool absent from step N's list cannot be called in step N.
func TestPrepareStepSubsetsToolsPerStep(t *testing.T) {
	a := core.Tool("a", "", func(_ context.Context, _ struct{}) (string, error) { return "a", nil })
	b := core.Tool("b", "", func(_ context.Context, _ struct{}) (string, error) { return "b", nil })
	phase := func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
		keep := "a"
		if step > 0 {
			keep = "b"
		}
		req.Tools = slices.DeleteFunc(req.Tools, func(t *core.ToolDef) bool { return t.Name != keep })
		return req, nil
	}
	m := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "b"}), // b not advertised in step 0
		wefttest.ToolCalls(wefttest.Call{Name: "b"}), // now it is
		wefttest.Say("done"),
	)
	agt := core.New(m, a, b, core.PrepareStep(phase))
	res, err := agt.Generate(context.Background(), core.Prompt("q"))
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
	lastOnly := func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) {
		if len(req.Messages) > 1 {
			req.Messages = req.Messages[len(req.Messages)-1:]
		}
		return req, nil
	}
	m := wefttest.Script(wefttest.Say("done"))
	agt := core.New(m, core.PrepareStep(lastOnly))
	res, err := agt.Generate(context.Background(), core.Prompt("first"), core.Prompt("second"))
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
	agt := core.New(wefttest.Script(wefttest.Say("never")),
		core.PrepareStep(func(context.Context, int, core.ModelRequest) (core.ModelRequest, error) {
			return core.ModelRequest{}, refuse
		}))
	_, err := agt.Generate(context.Background(), core.Prompt("q"))
	var re *core.RunError
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
	noisy := core.Tool("noisy", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil },
		core.PromptSnippet("Use noisy carefully."))
	quiet := core.Tool("quiet", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	drop := func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) {
		req.Tools = slices.DeleteFunc(req.Tools, func(t *core.ToolDef) bool { return t.Name == "noisy" })
		return req, nil
	}
	baseM := wefttest.Script(wefttest.Say("done"))
	base := core.New(baseM, core.Instructions("Base."), noisy, quiet)
	trimmedM := wefttest.Script(wefttest.Say("done"))
	trimmed := core.New(trimmedM, core.Instructions("Base."), noisy, quiet, core.PrepareStep(drop))
	if _, err := base.Generate(context.Background(), core.Prompt("q")); err != nil {
		t.Fatal(err)
	}
	if _, err := trimmed.Generate(context.Background(), core.Prompt("q")); err != nil {
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
	tag := func(name string, rewrite func(*core.ModelRequest)) func(context.Context, int, core.ModelRequest) (core.ModelRequest, error) {
		return func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) {
			order = append(order, name)
			rewrite(&req)
			return req, nil
		}
	}
	m := wefttest.Script(wefttest.Say("done"))
	agt := core.New(m,
		core.PrepareStep(tag("first", func(r *core.ModelRequest) { r.System += "+1" })),
		core.PrepareStep(tag("second", func(r *core.ModelRequest) { r.System += "+2" })),
	)
	if _, err := agt.Generate(context.Background(), core.Prompt("q")); err != nil {
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
	var seen core.ModelRequest
	recorder := func(next core.Model) core.Model {
		return recordModel{next: next, seen: &seen}
	}
	m := wefttest.Script(wefttest.Say("done"))
	agt := core.New(m,
		core.WrapModel(recorder),
		core.PrepareStep(func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) {
			req.System = "prepared"
			return req, nil
		}),
	)
	if _, err := agt.Generate(context.Background(), core.Prompt("q")); err != nil {
		t.Fatal(err)
	}
	if seen.System != "prepared" {
		t.Errorf("seam saw %q, want the prepared system", seen.System)
	}
}

// A prepared list with a duplicate name or nil entry fails the run —
// the same rule a tool-source snapshot obeys.
func TestPrepareStepDuplicateToolFailsRun(t *testing.T) {
	echo := core.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	dup := func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) {
		req.Tools = append(req.Tools, req.Tools...)
		return req, nil
	}
	agt := core.New(wefttest.Script(wefttest.Say("never")), echo, core.PrepareStep(dup))
	_, err := agt.Generate(context.Background(), core.Prompt("q"))
	if !errors.Is(err, core.ErrDuplicateTool) {
		t.Fatalf("err = %v, want ErrDuplicateTool", err)
	}
	nilEntry := func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) {
		req.Tools = []*core.ToolDef{nil}
		return req, nil
	}
	agt2 := core.New(wefttest.Script(wefttest.Say("never")), echo, core.PrepareStep(nilEntry))
	if _, err := agt2.Generate(context.Background(), core.Prompt("q")); !errors.Is(err, core.ErrNilTool) {
		t.Fatalf("err = %v, want ErrNilTool", err)
	}
}

// PrepareStep over a ToolSource: the source is consulted once per step
// and the prepared subset of its snapshot is the dispatch snapshot.
func TestPrepareStepOverToolSource(t *testing.T) {
	var fetches int
	a := core.Tool("a", "", func(_ context.Context, _ struct{}) (string, error) { return "a", nil })
	b := core.Tool("b", "", func(_ context.Context, _ struct{}) (string, error) { return "b", nil })
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "a"}, wefttest.Call{Name: "b"}),
		wefttest.Say("done"),
	),
		core.ToolSource(func() []*core.ToolDef { fetches++; return []*core.ToolDef{a, b} }),
		core.PrepareStep(func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) {
			req.Tools = slices.DeleteFunc(req.Tools, func(t *core.ToolDef) bool { return t.Name == "b" })
			return req, nil
		}))
	res, err := agt.Generate(context.Background(), core.Prompt("q"))
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
	echo := core.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	plugin := core.Options(
		core.Instructions("first"),
		nil, // ignored
		core.Instructions("second"),
		echo,
		core.MaxSteps(7),
	)
	plugin = core.Options(core.Name("composed"), plugin)
	agt := core.New(wefttest.Script(wefttest.Say("ok")), plugin)
	b, err := core.Manifest(agt)
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
	inner := core.Options(core.Instructions("inner"))
	agt := core.New(wefttest.Script(wefttest.Say("ok")),
		core.Name("nested"),
		core.Options(inner, core.Instructions("outer")))
	b, err := core.Manifest(agt)
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
	echo := core.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	_ = core.New(wefttest.Script(wefttest.Say("ok")), core.Options(echo, echo))
}

// ToolOptions packages per-tool policy under one name.
func TestToolOptions(t *testing.T) {
	policy := core.ToolOptions(core.Timeout(3*time.Second), core.StrictInput())
	echo := core.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil }, policy)
	b, err := core.Manifest(core.New(wefttest.Script(wefttest.Say("ok")), core.Name("a"), echo))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"timeout": "3s"`) || !strings.Contains(string(b), `"strict_input": true`) {
		t.Errorf("ToolOptions policy not applied:\n%s", b)
	}
}

// The three reporting splits (TODO §2a.4): Add is element-wise over
// all five fields, Total still reads the two totals, and the wire is
// omitempty — an old event decodes unchanged and a split-carrying one
// round-trips.
func TestUsageSplits(t *testing.T) {
	a := core.Usage{InputTokens: 100, OutputTokens: 50, CachedInputTokens: 40, CacheWriteTokens: 10, ReasoningTokens: 20}
	b := core.Usage{InputTokens: 30, OutputTokens: 20, CachedInputTokens: 5, CacheWriteTokens: 1, ReasoningTokens: 2}
	got := a.Add(b)
	want := core.Usage{InputTokens: 130, OutputTokens: 70, CachedInputTokens: 45, CacheWriteTokens: 11, ReasoningTokens: 22}
	if got != want {
		t.Errorf("Add = %+v, want %+v", got, want)
	}
	if a.Total() != 150 {
		t.Errorf("Total = %d, want 150 (the two totals only)", a.Total())
	}
	// omitempty: zero splits are absent from the wire.
	b2, err := json.Marshal(core.Usage{InputTokens: b.InputTokens, OutputTokens: b.OutputTokens})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b2), "cached_input_tokens") || strings.Contains(string(b2), "reasoning_tokens") {
		t.Errorf("zero splits marshalled: %s", b2)
	}
	// A Usage with all five fields round-trips through the event wire.
	ev := core.StepFinish{RunID: "r1", Index: 1, Reason: core.StopEndTurn, Usage: a}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	back, err := core.UnmarshalEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.(core.StepFinish).Usage; got != a {
		t.Errorf("round trip = %+v, want %+v", got, a)
	}
}

// UsageLimit reads the totals, never the splits: a cached-heavy run
// budgets identically to an uncached one with the same totals (the
// splits are reporting, not budget bases).
func TestUsageLimitIgnoresSplits(t *testing.T) {
	echo := core.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "ok", nil })
	spend := func(u core.Usage) wefttest.Turn {
		return wefttest.ToolCalls(wefttest.Call{Name: "echo"}).WithUsage(u)
	}
	cached := spend(core.Usage{InputTokens: 100, OutputTokens: 5, CachedInputTokens: 100, CacheWriteTokens: 100})
	uncached := spend(core.Usage{InputTokens: 100, OutputTokens: 5})
	for name, turn := range map[string]wefttest.Turn{"cached": cached, "uncached": uncached} {
		// Two steps, so the budget's continuation point is reached:
		// the limit sits exactly at step 0's totals, and both runs get
		// to spend step 1 — cached or not, a split-based limit would
		// have failed the cached one at step 0.
		agt := core.New(wefttest.Script(turn, turn, wefttest.Say("done")), echo, core.UsageLimit(core.Usage{InputTokens: 300, OutputTokens: 30}))
		if _, err := agt.Generate(context.Background(), core.Prompt("q")); err != nil {
			t.Errorf("%s: err = %v, want success (the limit reads the totals)", name, err)
		}
		// One token tighter than step 0 alone: both fail alike.
		agt = core.New(wefttest.Script(turn, turn, wefttest.Say("done")), echo, core.UsageLimit(core.Usage{InputTokens: 99, OutputTokens: 30}))
		if _, err := agt.Generate(context.Background(), core.Prompt("q")); err == nil {
			t.Errorf("%s: succeeded under a tighter input limit", name)
		}
	}
}

// Resolve pastes an externally-computed result into the transcript:
// the handler never runs, the model's next request carries the content
// verbatim, and the transcript shape is an ordinary tool result (ADR
// 0007's 2026-09-22 amendment).
func TestResolvePastesExternalResult(t *testing.T) {
	ran := false
	park := core.Tool("run_sql", "", func(_ context.Context, _ struct{}) (string, error) {
		ran = true
		return "should not run", nil
	}, core.RequireApproval())
	m := wefttest.Script(wefttest.ToolCalls(wefttest.Call{ID: "q1", Name: "run_sql"}), wefttest.Say("done"))
	agt := core.New(m, park)
	res, err := agt.Generate(context.Background(), core.Prompt("run it"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 || res.Pending[0].ID != "q1" {
		t.Fatalf("pending = %+v, want q1", res.Pending)
	}

	m2 := wefttest.Script(wefttest.Say("counted"))
	agt2 := core.New(m2, park)
	res2, err := agt2.Generate(context.Background(),
		core.Messages(res.Messages...), core.Resolve("q1", "row_count: 42"), core.Prompt("resume"))
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Error("the handler ran under Resolve; it never should")
	}
	// The pasted content reaches the next model call verbatim.
	req := m2.Requests()[0]
	var saw string
	for _, msg := range req.Messages {
		if msg.Role != core.RoleTool {
			continue
		}
		for _, p := range msg.Content {
			if r, ok := p.(core.ToolResultPart); ok && r.CallID == "q1" {
				saw = r.Content
				if r.IsError {
					t.Error("Resolve produced an error result")
				}
			}
		}
	}
	if saw != "row_count: 42" {
		t.Errorf("resolved content = %q, want it verbatim on the next request", saw)
	}
	// And the resolved transcript re-feeds cleanly through Repair.
	if _, err := core.New(wefttest.Script(wefttest.Say("ok")), park).
		Generate(context.Background(), core.Messages(res2.Messages...), core.Prompt("again")); err != nil {
		t.Errorf("resolved transcript does not re-feed: %v", err)
	}
}

// Resolve, Approve, and Deny compose in one resuming call, in call
// order; the last option for an id wins.
func TestResolveComposesWithApproveDeny(t *testing.T) {
	park := core.Tool("park", "", func(_ context.Context, _ struct{}) (string, error) {
		return "approved-ran", nil
	}, core.RequireApproval())
	agt := core.New(wefttest.Script(wefttest.ToolCalls(
		wefttest.Call{ID: "a1", Name: "park"},
		wefttest.Call{ID: "r1", Name: "park"},
		wefttest.Call{ID: "d1", Name: "park"},
		wefttest.Call{ID: "n1", Name: "park"},
	), wefttest.Say("done")), park)
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 4 {
		t.Fatalf("pending = %d, want 4", len(res.Pending))
	}
	agt2 := core.New(wefttest.Script(wefttest.Say("done")), park)
	res2, err := agt2.Generate(context.Background(),
		core.Messages(res.Messages...),
		core.Approve("a1"),
		core.Resolve("r1", "pasted"),
		core.Deny("d1", "too risky"),
		core.Prompt("resume"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]core.ToolResultPart{}
	for _, msg := range res2.Messages {
		if msg.Role != core.RoleTool {
			continue
		}
		for _, p := range msg.Content {
			if r, ok := p.(core.ToolResultPart); ok {
				got[r.CallID] = r
			}
		}
	}
	if r := got["a1"]; r.Content != "approved-ran" || r.IsError {
		t.Errorf("a1 = %+v, want the executed result", r)
	}
	if r := got["r1"]; r.Content != "pasted" || r.IsError {
		t.Errorf("r1 = %+v, want the pasted result", r)
	}
	if r := got["d1"]; !r.IsError || !strings.Contains(r.Content, "DENIED: too risky") {
		t.Errorf("d1 = %+v, want DENIED: too risky", r)
	}
	if r := got["n1"]; !r.IsError || !strings.Contains(r.Content, "DENIED: no decision") {
		t.Errorf("n1 = %+v, want DENIED: no decision", r)
	}
}

// ResolveError marks the pasted content as an error result; resolved
// content over MaxResultBytes is capped with the visible marker.
func TestResolveErrorAndCap(t *testing.T) {
	park := core.Tool("park", "", func(_ context.Context, _ struct{}) (string, error) {
		return "never", nil
	}, core.RequireApproval())
	agt := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{ID: "p1", Name: "park"}), wefttest.Say("done")), park)
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}

	agt2 := core.New(wefttest.Script(wefttest.Say("done")), park, core.MaxResultBytes(10))
	res2, err := agt2.Generate(context.Background(),
		core.Messages(res.Messages...), core.Resolve("p1", strings.Repeat("x", 40)), core.Prompt("r"))
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range res2.Messages {
		if msg.Role != core.RoleTool {
			continue
		}
		for _, p := range msg.Content {
			if r, ok := p.(core.ToolResultPart); ok && r.CallID == "p1" {
				if len(r.Content) >= 40 {
					t.Errorf("resolved content was not capped: %q", r.Content)
				}
				if !strings.Contains(r.Content, "…[truncated") {
					t.Errorf("capped content lacks the visible marker: %q", r.Content)
				}
			}
		}
	}

	// ResolveError renders IsError.
	agt3 := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{ID: "p1", Name: "park"}), wefttest.Say("done")), park)
	res3, err := agt3.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	agt4 := core.New(wefttest.Script(wefttest.Say("done")), park)
	res4, err := agt4.Generate(context.Background(),
		core.Messages(res3.Messages...), core.ResolveError("p1", "prod is down"), core.Prompt("r"))
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range res4.Messages {
		if msg.Role != core.RoleTool {
			continue
		}
		for _, p := range msg.Content {
			if r, ok := p.(core.ToolResultPart); ok && r.CallID == "p1" {
				if !r.IsError || r.Content != "prod is down" {
					t.Errorf("ResolveError result = %+v, want error with the content verbatim", r)
				}
			}
		}
	}
}

// Resolve on an id that is not pending fails the run at step 0 with
// the id in the message — the asymmetry with Approve/Deny, which
// ignore unknown ids, is deliberate (ADR 0007's 2026-09-22 amendment).
func TestResolveNonPendingFails(t *testing.T) {
	park := core.Tool("park", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", nil
	}, core.RequireApproval())
	agt := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{ID: "p1", Name: "park"}), wefttest.Say("done")), park)
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = core.New(wefttest.Script(wefttest.Say("done")), park).Generate(context.Background(),
		core.Messages(res.Messages...), core.Resolve("bogus", "payload"), core.Approve("also-bogus"), core.Prompt("r"))
	if err == nil {
		t.Fatal("run succeeded; want the loud non-pending resolve failure")
	}
	if !strings.Contains(err.Error(), `"bogus"`) {
		t.Errorf("err = %v, want it to name the id", err)
	}
	var re *core.RunError
	if !errors.As(err, &re) || re.Step != 0 {
		t.Errorf("err = %v, want a RunError at step 0", err)
	}
	// Approve on the same unknown id stayed ignored — no complaint
	// about "also-bogus".
	if strings.Contains(err.Error(), "also-bogus") {
		t.Errorf("err = %v, want Approve's unknown id ignored", err)
	}
}

// Review 2026-09-24 §2.1: the same loud failure when nothing at all is
// pending. The validation used to live inside the resume block, so a
// Resolve against a transcript with zero dangling calls was silently
// dropped — err = nil, payload gone — while the same Resolve next to
// another pending call failed as documented.
func TestResolveWithNothingPendingFails(t *testing.T) {
	agt := core.New(wefttest.Script(wefttest.Say("done")))
	_, err := agt.Generate(context.Background(), core.Prompt("hi"), core.Resolve("nonexistent", "payload"))
	if err == nil {
		t.Fatal("run succeeded; the Resolve was silently dropped")
	}
	if !strings.Contains(err.Error(), `"nonexistent"`) {
		t.Errorf("err = %v, want it to name the id", err)
	}
	var re *core.RunError
	if !errors.As(err, &re) || re.Step != 0 {
		t.Errorf("err = %v, want a RunError at step 0", err)
	}
}

// A resolved call follows the denied path, not the executed path: a
// result in the tool message, no ToolStart, no ToolFinish, no span —
// nothing executed.
func TestResolveEmitsNoToolEvents(t *testing.T) {
	park := core.Tool("park", "", func(_ context.Context, _ struct{}) (string, error) {
		return "never", nil
	}, core.RequireApproval())
	agt := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{ID: "p1", Name: "park"}), wefttest.Say("done")), park)
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	var sawStart, sawFinish bool
	for ev, err := range core.New(wefttest.Script(wefttest.Say("done")), park).
		Stream(context.Background(), core.Messages(res.Messages...), core.Resolve("p1", "pasted"), core.Prompt("r")).Events() {
		if err != nil {
			t.Fatal(err)
		}
		switch e := ev.(type) {
		case core.ToolStart:
			if e.CallID == "p1" {
				sawStart = true
			}
		case core.ToolFinish:
			if e.CallID == "p1" {
				sawFinish = true
			}
		}
	}
	if sawStart || sawFinish {
		t.Errorf("resolved call emitted ToolStart=%v ToolFinish=%v; nothing executed", sawStart, sawFinish)
	}
}

// A nil *ToolDef handed to New is loud, like a duplicate name: the
// runtime snapshot path already fails the run with ErrNilTool for the
// same mistake, and New used to skip it silently (review 2026-09-24 §3).
func TestNewNilToolPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("a nil *ToolDef option did not panic")
		}
		if s, _ := r.(string); !strings.Contains(s, "nil *ToolDef") {
			t.Errorf("panic = %v, want it to name the nil tool", r)
		}
	}()
	var missing *core.ToolDef
	_ = core.New(wefttest.Script(wefttest.Say("ok")), missing)
}

// --- OnRunEnd (TODO §11, plan §3.7) ---------------------------------
//
// The outcome observer: exactly once per run, after RunFinish or the
// RunError, before Run returns. A tap cannot be this — a failed run
// emits nothing after its last delivered event (ADR 0004) — and neither
// seam wraps the run. These tests pin the four contract points the
// store depends on.

type runEndLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *runEndLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, s)
}

func (l *runEndLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

// Fires once on success, with the complete result and a nil error, and
// has fired by the time Generate returns.
func TestOnRunEndFiresOnceOnSuccess(t *testing.T) {
	var calls int
	agt := core.New(wefttest.Script(wefttest.Say("hello")), core.OnRunEnd(
		func(_ context.Context, res *core.RunResult, err error) {
			calls++
			if err != nil {
				t.Errorf("err = %v, want nil on success", err)
			}
			if res == nil || res.Text() != "hello" {
				t.Errorf("res = %v, want the completed result", res)
			}
		}))
	if _, err := agt.Generate(context.Background(), core.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("OnRunEnd called %d times, want exactly 1", calls)
	}
}

// Fires once on failure; res is the RunError's partial and err is the
// *RunError itself, so errors.Is works in the observer.
func TestOnRunEndFiresOnRunErrorWithPartial(t *testing.T) {
	sentinel := errors.New("provider down")
	var gotRes *core.RunResult
	agt := core.New(wefttest.Script(wefttest.Fail(sentinel)),
		core.OnRunEnd(func(_ context.Context, res *core.RunResult, err error) {
			gotRes = res
			var re *core.RunError
			if !errors.As(err, &re) {
				t.Errorf("err = %v, want *RunError", err)
			}
			if !errors.Is(err, sentinel) {
				t.Errorf("err = %v, want the cause reachable", err)
			}
		}))
	_, err := agt.Generate(context.Background(), core.Prompt("hi"))
	if err == nil {
		t.Fatal("run should have failed")
	}
	if gotRes == nil {
		t.Fatal("OnRunEnd did not fire")
	}
	var re *core.RunError
	errors.As(err, &re)
	if gotRes != re.Result {
		t.Errorf("res = %p, want the RunError's partial %p", gotRes, re.Result)
	}
}

// Fires once on cancellation, with the ctx error reachable through the
// RunError.
func TestOnRunEndFiresOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	slow := core.Tool("slow", "", func(ctx context.Context, _ struct{}) (string, error) {
		cancel()
		<-ctx.Done()
		return "", ctx.Err()
	})
	var gotErr error
	agt := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "slow"}), wefttest.Say("late")), slow,
		core.OnRunEnd(func(_ context.Context, _ *core.RunResult, err error) { gotErr = err }))
	_, err := agt.Generate(ctx, core.Prompt("hi"))
	if err == nil {
		t.Fatal("run should have failed with the cancellation")
	}
	if gotErr == nil {
		t.Fatal("OnRunEnd did not fire")
	}
	if !errors.Is(gotErr, context.Canceled) {
		t.Errorf("observer err = %v, want context.Canceled reachable", gotErr)
	}
}

// A run that panics did not end — OnRunEnd never fires, and the panic
// still reaches the caller (the span guard re-panics; the observer
// must not launder a crash into an outcome).
func TestOnRunEndDoesNotFireOnPanic(t *testing.T) {
	calls := 0
	agt := core.New(wefttest.Script(wefttest.Say("ok")),
		core.PrepareStep(func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) {
			panic("user code bug")
		}),
		core.OnRunEnd(func(context.Context, *core.RunResult, error) { calls++ }))
	defer func() {
		if recover() == nil {
			t.Fatal("the PrepareStep panic should have reached the caller")
		}
		if calls != 0 {
			t.Errorf("OnRunEnd fired %d times on a panicked run; a crash is not an outcome", calls)
		}
	}()
	_, _ = agt.Generate(context.Background(), core.Prompt("hi"))
}

// A child run's OnRunEnd fires inside the parent's tool call — before
// the parent's — with the child's own id on res, the same visibility
// rule as the child's events (ADR 0014).
func TestOnRunEndChildFiresInsideParentToolCall(t *testing.T) {
	log := &runEndLog{}
	child := core.New(wefttest.Script(wefttest.Say("child done")),
		core.Name("child"),
		core.OnRunEnd(func(_ context.Context, res *core.RunResult, _ error) {
			log.add("child:" + res.ID)
		}))
	parent := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "delegate"}), wefttest.Say("parent done")),
		core.Subagent("delegate", "Runs the child.", child),
		core.OnRunEnd(func(_ context.Context, res *core.RunResult, _ error) {
			log.add("parent:" + res.ID)
		}))
	res, err := parent.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	calls := log.snapshot()
	if len(calls) != 2 {
		t.Fatalf("OnRunEnd calls = %v, want child then parent", calls)
	}
	if !strings.HasPrefix(calls[0], "child:") || calls[0] != "child:"+res.ID+"/0/call_1" {
		t.Errorf("first call = %q, want the child with its derived id", calls[0])
	}
	if calls[1] != "parent:"+res.ID {
		t.Errorf("second call = %q, want the parent after its tool call", calls[1])
	}
}

// Several OnRunEnd options run in registration order (the Tap rule),
// and a panic in one is contained and counted, not propagated — a
// broken observer cannot break a run.
func TestOnRunEndOrderAndContainment(t *testing.T) {
	log := &runEndLog{}
	agt := core.New(wefttest.Script(wefttest.Say("ok")),
		core.OnRunEnd(func(context.Context, *core.RunResult, error) { log.add("first") }),
		core.OnRunEnd(func(context.Context, *core.RunResult, error) { panic("observer bug") }),
		core.OnRunEnd(func(context.Context, *core.RunResult, error) { log.add("third") }))
	if _, err := agt.Generate(context.Background(), core.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	calls := log.snapshot()
	if len(calls) != 2 || calls[0] != "first" || calls[1] != "third" {
		t.Errorf("calls = %v, want [first third] around the contained panic", calls)
	}
	if n := agt.TapPanics(); n != 1 {
		t.Errorf("TapPanics = %d, want 1 contained observer panic", n)
	}
}

// --- AgentFromContext / Agent.Logger (TODO §11, plan §3.7's found gap) ---

// A tap sees the agent it is installed on through the run's context —
// the head of the ancestry chain — and a child run's tap sees the
// child. Outside a run there is none.
func TestAgentFromContext(t *testing.T) {
	var parent, child, outside *core.Agent
	kid := core.New(wefttest.Script(wefttest.Say("done")), core.Name("kid"),
		core.Tap(func(ctx context.Context, _ core.Event) {
			child = core.AgentFromContext(ctx)
		}))
	agt := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "delegate"}), wefttest.Say("done")),
		core.Name("parent"),
		core.Tap(func(ctx context.Context, _ core.Event) {
			if parent == nil {
				parent = core.AgentFromContext(ctx)
			}
		}),
		core.Subagent("delegate", "Runs the child.", kid))
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	if parent != agt {
		t.Errorf("parent tap saw %p, want the agent itself %p", parent, agt)
	}
	if child != kid {
		t.Errorf("child tap saw %p, want the child agent %p", child, kid)
	}
	if outside = core.AgentFromContext(context.Background()); outside != nil {
		t.Errorf("AgentFromContext outside a run = %p, want nil", outside)
	}
}

// A child run's tap reads its parent linkage from the same context:
// the parent's tool call rides it, so a recorder learns ParentID and
// ParentCallID without parsing run ids.
func TestAgentFromContextChildSeesParentCall(t *testing.T) {
	type link struct{ run, call string }
	var got link
	kid := core.New(wefttest.Script(wefttest.Say("done")), core.Name("kid"),
		core.Tap(func(ctx context.Context, ev core.Event) {
			if _, ok := ev.(core.RunStart); ok {
				if c, ok := core.CallFromContext(ctx); ok {
					got = link{run: c.RunID, call: c.CallID}
				}
			}
		}))
	agt := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "delegate"}), wefttest.Say("done")),
		core.Subagent("delegate", "Runs the child.", kid))
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if got.run != res.ID || got.call != "call_1" {
		t.Errorf("child saw parent linkage {run:%s call:%s}, want {run:%s call:call_1}", got.run, got.call, res.ID)
	}
}

// Logger returns the configured logger, defaulting to slog.Default.
func TestAgentLogger(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, nil))
	agt := core.New(wefttest.Script(wefttest.Say("ok")), core.Logger(l))
	if agt.Logger() != l {
		t.Error("Logger() != the Logger option's value")
	}
	if got := core.New(wefttest.Script(wefttest.Say("ok"))).Logger(); got != slog.Default() {
		t.Error("Logger() without the option != slog.Default()")
	}
}

// reportingMW reports one attempt (and one wire pair) per model call
// through the reporting hook, and nothing else: the A8 promise is that
// a report never changes the run.
func reportingMW(next core.Model) core.Model {
	return reportingModel{next}
}

type reportingModel struct{ next core.Model }

func (m reportingModel) Info() core.ModelInfo { return core.InfoOf(m.next) }
func (m reportingModel) Unwrap() core.Model   { return m.next }

func (m reportingModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		start := time.Now()
		r := core.ReportFromContext(ctx)
		for ev, err := range m.next.Stream(ctx, req) {
			if !yield(ev, err) {
				break
			}
		}
		r.Raw(core.RawPair{Request: []byte(`{}`), Response: []byte(`{}`), MediaType: "application/json"})
		r.Attempt(core.AttemptInfo{Model: "m", Provider: "p", Start: start, End: time.Now(),
			Err: errors.New("reported, not returned"), RetryAfter: time.Second})
	}
}

// The reporting hook (ReportFromContext) is reporting, not a seam. The
// same two-step run is made five ways — no hook; a reporting middleware
// with every report written (tracer recording, logger at Debug) and
// with none written (no tracer, logger at Info, no records); loud also
// records every log record kind, the request records included (ADR
// 0028); real mw.Retry over a
// script that fails before each step, loud and quiet — and the
// transcript, the event stream, the stop reason and the usage are
// identical across all five.
func TestReportingHookChangesNothingModelVisible(t *testing.T) {
	type outcome struct {
		messages, events, usage []byte
		stop                    core.StopReason
	}
	run := func(failFirst, loud bool, wrap ...core.ModelMiddleware) outcome {
		t.Helper()
		var turns []wefttest.Turn
		if failFirst {
			turns = append(turns, wefttest.Fail(core.ErrStreamIdle))
		}
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{ID: "c1", Name: "echo", Args: `{"msg":"hi"}`}))
		if failFirst {
			turns = append(turns, wefttest.Fail(core.ErrStreamIdle))
		}
		turns = append(turns, wefttest.Say("done"))
		echo := core.Tool("echo", "Echo.", func(_ context.Context, in struct {
			Msg string `json:"msg"`
		}) (string, error) {
			return "echo: " + in.Msg, nil
		})
		opts := []core.Option{echo, core.WrapModel(wrap...)}
		if loud {
			opts = append(opts,
				core.Logger(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))),
				core.TracerProvider(newRecProvider()),
				// Every record kind on, the request records (ADR 0028)
				// included: one per reported attempt under retry.
				core.LoggerProvider(newRecLogProvider()), core.Content(true))
		} else {
			opts = append(opts, core.Logger(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))))
		}
		r := core.New(wefttest.Script(turns...), opts...).Stream(context.Background(), core.RunID("r"), core.Prompt("hello"))
		var events []core.Event
		for ev, err := range r.Events() {
			if err != nil {
				t.Fatal(err)
			}
			events = append(events, ev)
		}
		res, err := r.Wait()
		if err != nil {
			t.Fatal(err)
		}
		var o outcome
		var merr error
		if o.messages, merr = json.Marshal(res.Messages); merr != nil {
			t.Fatal(merr)
		}
		// The step timing (A4) is wall-clock, never equal across runs:
		// stripped here, asserted by TestRetryOverFallbackReportsEveryAttempt.
		if o.events, merr = json.Marshal(stripStepTiming(events)); merr != nil {
			t.Fatal(merr)
		}
		if o.usage, merr = json.Marshal(res.Usage); merr != nil {
			t.Fatal(merr)
		}
		o.stop = res.StopReason
		return o
	}
	retry := mw.Retry(mw.BaseDelay(0))
	base := run(false, false)
	for name, got := range map[string]outcome{
		"hook, loud":  run(false, true, reportingMW),
		"hook, quiet": run(false, false, reportingMW),
		"retry, loud": run(true, true, retry),
		"retry quiet": run(true, false, retry),
	} {
		if !bytes.Equal(got.messages, base.messages) {
			t.Errorf("%s: transcript differs:\nbase %s\ngot  %s", name, base.messages, got.messages)
		}
		if !bytes.Equal(got.events, base.events) {
			t.Errorf("%s: events differ:\nbase %s\ngot  %s", name, base.events, got.events)
		}
		if !bytes.Equal(got.usage, base.usage) || got.stop != base.stop {
			t.Errorf("%s: usage/stop = %s/%q, want %s/%q", name, got.usage, got.stop, base.usage, base.stop)
		}
	}
}

// stripStepTiming zeroes StepFinish's wall-clock fields (LatencyMS,
// TTFTMS), nested child events included, so event streams from two runs
// compare byte for byte; the timing is asserted on its own.
func stripStepTiming(evs []core.Event) []core.Event {
	out := make([]core.Event, len(evs))
	for i, ev := range evs {
		switch e := ev.(type) {
		case core.StepFinish:
			e.LatencyMS, e.TTFTMS = 0, 0
			ev = e
		case core.Nested:
			e.Event = stripStepTiming([]core.Event{e.Event})[0]
			ev = e
		}
		out[i] = ev
	}
	return out
}

// namedScript is a scripted model under its own name, so a fallback's
// models are told apart on the record. silent marks it as reporting its
// own attempts (the ReportsAttempts convention) while reporting none:
// the mw layers above it stay silent, which turns the reporting off
// without changing the chain.
type namedScript struct {
	*wefttest.Model
	name   string
	silent bool
}

func (m namedScript) Info() core.ModelInfo  { return core.ModelInfo{Provider: "wefttest", Name: m.name} }
func (m namedScript) ReportsAttempts() bool { return m.silent }

// Plan A4's Done line, the core-provable half: mw.Retry(3) over
// mw.Fallback(a, b), a failing twice and b answering on its second
// try, shows every attempt — its model, its error — under one chat
// span; the model that answered is named on the chat span and the
// step_finish record (gen_ai.response.model); the step carries its
// timing (latency_ms, ttft_ms on the event, the record and the span);
// and the model-visible output — the requests each model saw, the
// transcript, the events minus their timing, usage, stop — is byte
// identical with the reporting on and off, with and without a tracer.
func TestRetryOverFallbackReportsEveryAttempt(t *testing.T) {
	type outcome struct {
		requestsA, requestsB, messages, events, usage []byte
		stop                                          core.StopReason
		raw                                           []core.Event
		tp                                            *recProvider
		lp                                            *recLogProvider
	}
	run := func(report, traced bool) outcome {
		t.Helper()
		a := namedScript{Model: wefttest.Script(wefttest.Fail(core.ErrStreamIdle), wefttest.Fail(core.ErrStreamIdle)), name: "glm-a", silent: !report}
		b := namedScript{Model: wefttest.Script(wefttest.Fail(core.ErrStreamIdle), wefttest.Say("done")), name: "glm-b", silent: !report}
		opts := []core.Option{core.WrapModel(mw.Retry(mw.MaxRetries(3), mw.BaseDelay(0)), mw.Fallback(b))}
		var o outcome
		if traced {
			o.tp, o.lp = newRecProvider(), newRecLogProvider()
			opts = append(opts, core.TracerProvider(o.tp), core.LoggerProvider(o.lp), core.Content(true),
				core.Logger(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))))
		}
		r := core.New(a, opts...).Stream(context.Background(), core.RunID("r"), core.Prompt("hello"))
		for ev, err := range r.Events() {
			if err != nil {
				t.Fatal(err)
			}
			o.raw = append(o.raw, ev)
		}
		res, err := r.Wait()
		if err != nil {
			t.Fatal(err)
		}
		for dst, v := range map[*[]byte]any{
			&o.requestsA: a.Requests(), &o.requestsB: b.Requests(),
			&o.messages: res.Messages, &o.events: stripStepTiming(o.raw), &o.usage: res.Usage,
		} {
			if *dst, err = json.Marshal(v); err != nil {
				t.Fatal(err)
			}
		}
		o.stop = res.StopReason
		return o
	}
	base := run(true, true)

	// Every attempt under the one chat span, in order, with its model
	// and its outcome; the fourth (glm-b's second try) answered.
	chat := base.tp.find(t, "chat glm-a")
	atts := attemptSpans(base.tp)
	wantModels := []string{"glm-a", "glm-b", "glm-a", "glm-b"}
	if len(atts) != len(wantModels) {
		t.Fatalf("attempt spans = %d, want %d", len(atts), len(wantModels))
	}
	for i, att := range atts {
		got := att.attrsMap()
		if att.parent.SpanID() != chat.sc.SpanID() {
			t.Errorf("attempt %d is not a child of the chat span", i+1)
		}
		if got["weft.attempt.index"] != strconv.Itoa(i+1) || got["gen_ai.request.model"] != wantModels[i] {
			t.Errorf("attempt %d attrs = %v, want index %d model %s", i+1, got, i+1, wantModels[i])
		}
		_, status, _, _ := att.state()
		if last := i == len(atts)-1; last {
			if status != codes.Ok || got["gen_ai.response.model"] != "glm-b" || got["error.type"] != "" {
				t.Errorf("answering attempt status=%v attrs=%v, want Ok, gen_ai.response.model=glm-b", status, got)
			}
		} else if status != codes.Error || got["error.type"] != "stream_idle" || got["gen_ai.response.model"] != "" {
			t.Errorf("failed attempt %d status=%v attrs=%v, want Error/stream_idle, no response model", i+1, status, got)
		}
	}
	// The chat span: asked glm-a, answered by glm-b, streamed, timed.
	cs := chat.attrsMap()
	if cs["gen_ai.request.model"] != "glm-a" || cs["gen_ai.response.model"] != "glm-b" || cs["weft.stream"] != "true" {
		t.Errorf("chat span attrs = %v, want request glm-a, response glm-b, weft.stream true", cs)
	}
	if ms, err := strconv.ParseInt(cs["weft.ttft_ms"], 10, 64); err != nil || ms < 1 {
		t.Errorf("chat span weft.ttft_ms = %q, want >= 1", cs["weft.ttft_ms"])
	}
	// The event: latency covers the whole chain, TTFT the first delta.
	var sf core.StepFinish
	for _, ev := range base.raw {
		if e, ok := ev.(core.StepFinish); ok {
			sf = e
		}
	}
	if sf.TTFTMS < 1 || sf.LatencyMS < sf.TTFTMS {
		t.Errorf("StepFinish timing = latency %d ttft %d, want ttft >= 1 and latency >= ttft", sf.LatencyMS, sf.TTFTMS)
	}
	// The step_finish record names the model that answered and carries
	// the event's timing as attributes.
	var found bool
	for _, rec := range base.lp.ofKind(t, "event") {
		if rec.attr("weft.event.type") != "step_finish" {
			continue
		}
		found = true
		lat, _ := rec.intAttr("weft.latency_ms")
		ttft, _ := rec.intAttr("weft.ttft_ms")
		if rec.attr("gen_ai.response.model") != "glm-b" || lat != sf.LatencyMS || ttft != sf.TTFTMS {
			t.Errorf("step_finish record attrs = %v, want gen_ai.response.model glm-b, latency %d, ttft %d", rec.attrs, sf.LatencyMS, sf.TTFTMS)
		}
	}
	if !found {
		t.Error("no step_finish record")
	}

	// Silent chain, tracer on: nothing reported, so no attempt spans,
	// and the answering model falls back to the one asked (the
	// documented precedence: reported success, else the request).
	silent := run(false, true)
	if n := len(attemptSpans(silent.tp)); n != 0 {
		t.Errorf("silent chain: %d attempt spans, want 0", n)
	}
	if got := silent.tp.find(t, "chat glm-a").attrsMap()["gen_ai.response.model"]; got != "glm-a" {
		t.Errorf("silent chain: chat gen_ai.response.model = %q, want the asked glm-a", got)
	}

	for name, got := range map[string]outcome{
		"reporting, no tracer": run(true, false),
		"silent, tracer":       silent,
		"silent, no tracer":    run(false, false),
	} {
		for what, pair := range map[string][2][]byte{
			"glm-a requests": {base.requestsA, got.requestsA},
			"glm-b requests": {base.requestsB, got.requestsB},
			"transcript":     {base.messages, got.messages},
			"events":         {base.events, got.events},
			"usage":          {base.usage, got.usage},
		} {
			if !bytes.Equal(pair[0], pair[1]) {
				t.Errorf("%s: %s differ:\nbase %s\ngot  %s", name, what, pair[0], pair[1])
			}
		}
		if got.stop != base.stop {
			t.Errorf("%s: stop = %q, want %q", name, got.stop, base.stop)
		}
	}
}

// A step whose model yields no TextDelta or ToolArgsDelta has no TTFT —
// absent on the event, the record and the chat span, never 0 — while
// its latency is still measured. Reasoning is not a first token.
func TestStepTimingWithoutADelta(t *testing.T) {
	tp, lp := newRecProvider(), newRecLogProvider()
	model := wefttest.Script(
		wefttest.Think("hmm", wefttest.ToolCalls(wefttest.Call{ID: "c1", Name: "echo", Args: `{"msg":"x"}`})),
		wefttest.Raw(core.ModelFinish{Reason: core.StopEndTurn}),
	)
	var steps []core.StepFinish
	agt := core.New(model, spanEcho, core.TracerProvider(tp), core.LoggerProvider(lp),
		core.Tap(func(_ context.Context, ev core.Event) {
			if e, ok := ev.(core.StepFinish); ok {
				steps = append(steps, e)
			}
		}))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("StepFinish events = %d, want 2", len(steps))
	}
	for _, e := range steps {
		if e.TTFTMS != 0 || e.LatencyMS < 1 {
			t.Errorf("step %d timing = latency %d ttft %d, want latency >= 1, no ttft", e.Index, e.LatencyMS, e.TTFTMS)
		}
		b, _ := json.Marshal(e)
		if bytes.Contains(b, []byte("ttft_ms")) {
			t.Errorf("step %d wire carries ttft_ms: %s", e.Index, b)
		}
	}
	tp.mu.Lock()
	for _, s := range tp.spans {
		if strings.HasPrefix(s.name, "chat ") {
			if _, ok := s.attrsMap()["weft.ttft_ms"]; ok {
				t.Errorf("chat span %v carries weft.ttft_ms without a delta", s.attrsMap())
			}
		}
	}
	tp.mu.Unlock()
	for _, rec := range lp.ofKind(t, "event") {
		if rec.attr("weft.event.type") != "step_finish" {
			continue
		}
		if _, ok := rec.intAttr("weft.ttft_ms"); ok {
			t.Errorf("step_finish record carries weft.ttft_ms without a delta: %v", rec.attrs)
		}
		if lat, ok := rec.intAttr("weft.latency_ms"); !ok || lat < 1 {
			t.Errorf("step_finish record weft.latency_ms = %d (%v), want >= 1", lat, ok)
		}
		if rec.attr("gen_ai.response.model") != "script" {
			t.Errorf("step_finish record gen_ai.response.model = %q, want the asked script", rec.attr("gen_ai.response.model"))
		}
	}
}

// A model call that fails names no answering model — nothing answered —
// while a delta that arrived before the failure still gives the chat
// span its weft.ttft_ms.
func TestFailedModelCallNamesNoModel(t *testing.T) {
	tp := newRecProvider()
	model := wefttest.Script(wefttest.SayThenFail("partial", core.ErrStreamIdle))
	if _, err := core.New(model, core.TracerProvider(tp)).Generate(context.Background(), core.Prompt("x")); err == nil {
		t.Fatal("run succeeded, want the stream's error")
	}
	got := tp.find(t, "chat script").attrsMap()
	if _, ok := got["gen_ai.response.model"]; ok {
		t.Errorf("failed chat span names an answering model: %v", got)
	}
	if ms, err := strconv.ParseInt(got["weft.ttft_ms"], 10, 64); err != nil || ms < 1 || got["weft.stream"] != "true" {
		t.Errorf("failed chat span attrs = %v, want weft.ttft_ms >= 1 and weft.stream true", got)
	}
}

// Outside a run's model call the reporter is a no-op, never nil: a bare
// context, a nil one, and a tool handler's context (the run's, not the
// chain's) all discard reports without panicking.
func TestReportFromContextOutsideAModelCall(t *testing.T) {
	core.ReportFromContext(context.Background()).Attempt(core.AttemptInfo{Model: "m"})
	core.ReportFromContext(nil).Raw(core.RawPair{}) //nolint:staticcheck // a nil ctx is the documented no-op
	var zero core.Reporter
	zero.Attempt(core.AttemptInfo{})
	zero.Raw(core.RawPair{})
	if core.ReportFromContext(context.Background()) != zero {
		t.Error("ReportFromContext outside a run is not the zero Reporter")
	}
	var inTool core.Reporter
	tool := core.Tool("probe", "Probe.", func(ctx context.Context, _ struct{}) (string, error) {
		inTool = core.ReportFromContext(ctx)
		return "ok", nil
	})
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "probe", Args: `{}`}), wefttest.Say("done"))
	if _, err := core.New(model, tool).Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if inTool != zero {
		t.Error("a tool handler's context carries the model call's reporter")
	}
}

// TestAgentRunDefaultsAccessors pins the run-default accessors: Params,
// Thinking and ToolChoice report the New options' values (zero values
// when unset), and Params is a copy — changing its pointers or Stop
// does not reach the agent.
func TestAgentRunDefaultsAccessors(t *testing.T) {
	temp, topP, maxTok, seed := 0.2, 0.9, 512, int64(7)
	agt := core.New(wefttest.Script(wefttest.Say("ok")),
		core.Params(core.RequestParams{Temperature: &temp, TopP: &topP, MaxTokens: &maxTok, Seed: &seed, Stop: []string{"END"}}),
		core.Thinking(core.ThinkingConfig{Level: core.ThinkHigh}),
		core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceNone}),
	)
	p := agt.Params()
	if *p.Temperature != 0.2 || *p.TopP != 0.9 || *p.MaxTokens != 512 || *p.Seed != 7 || !slices.Equal(p.Stop, []string{"END"}) {
		t.Fatalf("Params = %+v, want the New option's values", p)
	}
	*p.Temperature, p.Stop[0] = 1.5, "X"
	if q := agt.Params(); *q.Temperature != 0.2 || q.Stop[0] != "END" {
		t.Errorf("a changed copy reached the agent: %+v", q)
	}
	if agt.Thinking().Level != core.ThinkHigh {
		t.Errorf("Thinking = %+v", agt.Thinking())
	}
	if agt.ToolChoice().Mode != core.ToolChoiceNone {
		t.Errorf("ToolChoice = %+v", agt.ToolChoice())
	}
	bare := core.New(wefttest.Script(wefttest.Say("ok")))
	if p := bare.Params(); p.Temperature != nil || p.TopP != nil || p.MaxTokens != nil || p.Seed != nil || p.Stop != nil {
		t.Errorf("unset Params = %+v, want the zero value", p)
	}
	if bare.Thinking() != (core.ThinkingConfig{}) || bare.ToolChoice() != (core.ToolChoiceConfig{}) {
		t.Errorf("unset Thinking/ToolChoice = %+v / %+v, want zero values", bare.Thinking(), bare.ToolChoice())
	}
}

// An auto tool-choice override over a forcing default is recorded as
// "auto" on weft.override.tool_choice — the zero mode spelled out, never
// an empty value a reader cannot tell from "not overridden".
func TestOverrideToolChoiceAutoIsSpelledOut(t *testing.T) {
	tp := newRecProvider()
	agt := core.New(wefttest.Script(wefttest.Say("ok"), wefttest.Say("ok")),
		core.Name("demo"), core.TracerProvider(tp), lookupTool(),
		core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceAny}))
	for id, tc := range map[string]core.ToolChoiceConfig{
		"r_auto":  {Mode: core.ToolChoiceAuto},
		"r_named": {Mode: core.ToolChoiceNamed, Name: "lookup"},
	} {
		if _, err := agt.Generate(context.Background(), core.RunID(id), core.Prompt("x"), core.ToolChoice(tc)); err != nil {
			t.Fatal(err)
		}
	}
	if got := spanAttrsByID(t, tp, "r_auto")["weft.override.tool_choice"]; got != "auto" {
		t.Errorf("auto override recorded %q, want \"auto\"", got)
	}
	if got := spanAttrsByID(t, tp, "r_named")["weft.override.tool_choice"]; got != "tool:lookup" {
		t.Errorf("named override recorded %q, want \"tool:lookup\"", got)
	}
}
