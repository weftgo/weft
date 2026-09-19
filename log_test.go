package weft_test

// The log-line tests, L1–L8 of docs/phase2-observability-plan.md §4.2.
// The observer's lines share the spans' reporting points, so what the
// span tests pin structurally, these pin textually: the exact messages,
// keys, and values at Debug level (ADR 0016).

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// capHandler records every Debug record a run emits, at the level it is
// constructed with — the Logger option's destination in these tests.
type capHandler struct {
	mu      sync.Mutex
	level   slog.Level
	records []slog.Record
}

func (h *capHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *capHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *capHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capHandler) WithGroup(string) slog.Handler      { return h }

// line is one captured record flattened for assertions: msg, attrs by
// key, and the keys in emission order.
type line struct {
	msg   string
	attrs map[string]any
	keys  []string
}

func (h *capHandler) lines() []line {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]line, 0, len(h.records))
	for _, r := range h.records {
		l := line{msg: r.Message, attrs: map[string]any{}}
		r.Attrs(func(a slog.Attr) bool {
			l.attrs[a.Key] = a.Value.Any()
			l.keys = append(l.keys, a.Key)
			return true
		})
		out = append(out, l)
	}
	return out
}

func keySet(l line) map[string]bool {
	set := map[string]bool{}
	for _, k := range l.keys {
		set[k] = true
	}
	return set
}

func mustGet(t *testing.T, l line, key string) any {
	t.Helper()
	v, ok := l.attrs[key]
	if !ok {
		t.Fatalf("line %q lacks key %q (has %v)", l.msg, key, l.keys)
	}
	return v
}

// logEcho and logScript are log_test's standard fixtures, identical in
// shape to the span tests'.
var logEcho = weft.Tool("echo", "Echo the message.", func(_ context.Context, in struct {
	Msg string `json:"msg"`
}) (string, error) {
	return "echo: " + in.Msg, nil
})

func logScript() *wefttest.Model {
	return wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hi"}`}),
		wefttest.Say("done"),
	)
}

// L3: five lines for a two-step, one-tool run — the exact messages,
// key sets, and the values that are not durations.
func TestLoggerLines(t *testing.T) {
	h := &capHandler{level: slog.LevelDebug}
	agt := weft.New(logScript(), weft.Logger(slog.New(h)), weft.Name("demo"), logEcho)
	if _, err := agt.Generate(context.Background(), weft.RunID("r1"), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	lines := h.lines()
	if len(lines) != 5 {
		t.Fatalf("lines = %d, want 5: %v", len(lines), lines)
	}
	wantMsgs := []string{"run start", "model call", "tool call", "model call", "run finish"}
	for i, msg := range wantMsgs {
		if lines[i].msg != msg {
			t.Errorf("line %d msg = %q, want %q", i, lines[i].msg, msg)
		}
	}
	start := lines[0]
	if got := mustGet(t, start, "run"); got != "r1" {
		t.Errorf("run start run = %v", got)
	}
	if got := mustGet(t, start, "agent"); got != "demo" {
		t.Errorf("run start agent = %v", got)
	}
	if got := mustGet(t, start, "provider"); got != "wefttest" {
		t.Errorf("run start provider = %v", got)
	}
	if got := mustGet(t, start, "model"); got != "script" {
		t.Errorf("run start model = %v", got)
	}
	first := lines[1]
	if got := mustGet(t, first, "step"); got != int64(0) && got != 0 {
		t.Errorf("model call step = %v (%T)", got, got)
	}
	if got := mustGet(t, first, "reason"); got != "tool_calls" {
		t.Errorf("model call reason = %v", got)
	}
	if got := mustGet(t, first, "tool_calls"); got != int64(1) && got != 1 {
		t.Errorf("model call tool_calls = %v", got)
	}
	tool := lines[2]
	for k, want := range map[string]any{"run": "r1", "call": "call_1", "tool": "echo"} {
		if got := mustGet(t, tool, k); got != want {
			t.Errorf("tool call %s = %v, want %v", k, got, want)
		}
	}
	if got := mustGet(t, tool, "result_bytes"); got != int64(8) && got != 8 {
		t.Errorf("tool call result_bytes = %v", got)
	}
	finish := lines[4]
	for k, want := range map[string]any{"run": "r1", "stop": "stop"} {
		if got := mustGet(t, finish, k); got != want {
			t.Errorf("run finish %s = %v, want %v", k, got, want)
		}
	}
	if got := mustGet(t, finish, "input_tokens"); got != int64(20) && got != 20 {
		t.Errorf("run finish input_tokens = %v", got)
	}
	wantKeys := map[string]bool{
		"run": true, "agent": true, "provider": true, "model": true,
	}
	gotKeys := keySet(start)
	if len(gotKeys) != len(wantKeys) {
		t.Errorf("run start keys = %v, want exactly %v", gotKeys, wantKeys)
	}
	for _, l := range lines {
		if _, ok := l.attrs["dur"]; !ok && l.msg != "run start" {
			t.Errorf("line %q lacks dur", l.msg)
		}
		if d, ok := l.attrs["dur"]; ok {
			if _, isDur := d.(time.Duration); !isDur {
				t.Errorf("line %q dur = %T, want time.Duration", l.msg, d)
			}
		}
	}
}

// L5: deltas are never logged — the five messages are the whole set.
func TestLoggerNoDeltas(t *testing.T) {
	h := &capHandler{level: slog.LevelDebug}
	agt := weft.New(wefttest.Script(wefttest.Say("STREAMED-TEXT")), weft.Logger(slog.New(h)))
	if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"run start": true, "model call": true, "run finish": true}
	for _, l := range h.lines() {
		if !allowed[l.msg] {
			t.Errorf("unexpected line %q", l.msg)
		}
		for _, v := range l.attrs {
			if s, ok := v.(string); ok && s == "STREAMED-TEXT" {
				t.Errorf("line %q carries streamed text", l.msg)
			}
		}
	}
	if n := len(h.lines()); n != 3 {
		t.Errorf("lines = %d, want 3", n)
	}
}

// L1 + O13: without the option, lines go to slog.Default at Debug —
// silent under the default Info handler, honoured when a program sets a
// Debug default after building its agents (the common order). This is
// the one test that touches the process default; it restores it.
func TestLoggerDefaultIsSilent(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.Say("ok"), wefttest.Say("ok")), logEcho)
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })

	info := &capHandler{level: slog.LevelInfo}
	slog.SetDefault(slog.New(info))
	if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if n := len(info.lines()); n != 0 {
		t.Errorf("Info-level default logged %d lines, want 0", n)
	}

	debug := &capHandler{level: slog.LevelDebug}
	slog.SetDefault(slog.New(debug))
	if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if n := len(debug.lines()); n != 3 {
		t.Errorf("Debug default after New logged %d lines, want 3 (resolved at log time)", n)
	}
}

// L2: Logger routes to its logger; DiscardHandler turns the lines off
// outright; nil is the default and is ignored.
func TestLoggerOption(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.Say("ok")),
		weft.Logger(slog.New(slog.DiscardHandler)), weft.Logger(nil), logEcho)
	if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
}

// L4: a cancelled run logs "run error" with the ctx error as err — the
// line a tap could never write, because nothing is delivered after
// cancellation.
func TestLoggerCancellation(t *testing.T) {
	block := weft.Tool("block", "Blocks until canceled.", func(ctx context.Context, _ struct{}) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	h := &capHandler{level: slog.LevelDebug}
	agt := weft.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "block"}), wefttest.Say("done")),
		weft.Logger(slog.New(h)), block)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := agt.Stream(ctx, weft.Prompt("x"))
	for ev, err := range run.Events() {
		if err != nil {
			break
		}
		if _, isStart := ev.(weft.ToolStart); isStart {
			cancel()
		}
	}
	if _, err := run.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context.Canceled", err)
	}
	lines := h.lines()
	if len(lines) == 0 || lines[len(lines)-1].msg != "run error" {
		t.Fatalf("last line = %v, want run error", lines)
	}
	last := lines[len(lines)-1]
	if got := mustGet(t, last, "err"); got != "context canceled" {
		t.Errorf("run error err = %v, want the ctx error", got)
	}
	if _, ok := last.attrs["dur"]; !ok {
		t.Error("run error line lacks dur")
	}
}

// L6: the lines carry the span-carrying context, so a handler that
// reads trace.SpanFromContext correlates them with the spans with no
// weft code.
func TestLoggerContextCarriesSpan(t *testing.T) {
	tp := newRecProvider()
	spanHandler := &spanCapHandler{level: slog.LevelDebug}
	agt := weft.New(logScript(), weft.TracerProvider(tp), weft.Logger(slog.New(spanHandler)),
		weft.Name("demo"), logEcho)
	if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	wantSpan := map[string]string{
		"run start":  "invoke_agent demo",
		"model call": "chat script",
		"tool call":  "execute_tool echo",
		"run finish": "invoke_agent demo",
	}
	spanHandler.mu.Lock()
	lines := append([]spanLine(nil), spanHandler.lines...)
	spanHandler.mu.Unlock()
	for _, l := range lines {
		want, ok := wantSpan[l.msg]
		if !ok {
			t.Errorf("unexpected line %q", l.msg)
			continue
		}
		if l.spanName != want {
			t.Errorf("line %q carries span %q, want %q", l.msg, l.spanName, want)
		}
	}
	if n := len(lines); n != 5 {
		t.Errorf("lines = %d, want 5", n)
	}
}

// spanCapHandler records each line's message together with the span
// name found on the record's context.
type spanCapHandler struct {
	mu    sync.Mutex
	level slog.Level
	lines []spanLine
}

type spanLine struct {
	msg      string
	spanName string
}

func (h *spanCapHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *spanCapHandler) Handle(ctx context.Context, r slog.Record) error {
	// Handle's ctx is the context the line was logged with — the
	// span-carrying one, which is the whole of L6.
	name := ""
	if n, ok := trace.SpanFromContext(ctx).(interface{ Name() string }); ok {
		name = n.Name()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lines = append(h.lines, spanLine{msg: r.Message, spanName: name})
	return nil
}

func (h *spanCapHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *spanCapHandler) WithGroup(string) slog.Handler      { return h }

// L7: a child run's lines carry the child's run id — the hierarchy is
// in the id, not in extra keys.
func TestLoggerSubagentIDs(t *testing.T) {
	h := &capHandler{level: slog.LevelDebug}
	child := weft.New(wefttest.Script(wefttest.Say("child reply")), weft.Logger(slog.New(h)), weft.Name("research"))
	orch := weft.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "research"}), wefttest.Say("done")),
		weft.Logger(slog.New(h)), weft.Name("orchestrator"),
		weft.Subagent("research", "Delegate research.", child))
	if _, err := orch.Generate(context.Background(), weft.RunID("p"), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	var sawChild bool
	for _, l := range h.lines() {
		if run, ok := l.attrs["run"]; ok && run == "p/0/call_1" {
			sawChild = true
		}
	}
	if !sawChild {
		t.Error("no line carries the child run id p/0/call_1")
	}
}

// ExampleLogger shows the whole log surface: one line per phase, at
// Debug, ids and counts only.
func ExampleLogger() {
	scrub := func(_ []string, a slog.Attr) slog.Attr {
		switch a.Key {
		case slog.TimeKey, "dur": // timestamps and durations are not reproducible
			return slog.Attr{}
		}
		return a
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:       slog.LevelDebug,
		ReplaceAttr: scrub,
	}))
	echo := weft.Tool("echo", "Echo the message.", func(_ context.Context, in struct {
		Msg string `json:"msg"`
	}) (string, error) {
		return "echo: " + in.Msg, nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hi"}`}),
		wefttest.Say("done"),
	), weft.Logger(logger), weft.Name("demo"), echo)
	if _, err := agt.Generate(context.Background(), weft.RunID("demo"), weft.Prompt("hi")); err != nil {
		return
	}
	// Output:
	// level=DEBUG msg="run start" run=demo agent=demo provider=wefttest model=script
	// level=DEBUG msg="model call" run=demo step=0 provider=wefttest model=script reason=tool_calls input_tokens=10 output_tokens=5 tool_calls=1
	// level=DEBUG msg="tool call" run=demo step=0 call=call_1 tool=echo result_bytes=8
	// level=DEBUG msg="model call" run=demo step=1 provider=wefttest model=script reason=stop input_tokens=10 output_tokens=5 tool_calls=0
	// level=DEBUG msg="run finish" run=demo steps=2 input_tokens=20 output_tokens=10 stop=stop
}
