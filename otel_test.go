package weft_test

// The span tests, S1–S10 of docs/phase2-observability-plan.md §3.2. The
// tracer is implemented in this file on the API's embedded types — the
// OTel API is designed to be implemented — so the root module's tests
// assert the full span tree without the SDK ever entering go.mod (Go has
// no test-only dependencies) and without registering any global (ADR
// 0016).

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// recProvider records every span started through it: name, kind,
// parent, attributes, status, recorded exceptions, and end state.
type recProvider struct {
	trace.TracerProvider // embedded: only Tracer is overridden

	mu    sync.Mutex
	spans []*recSpan
	next  uint64
}

func newRecProvider() *recProvider { return &recProvider{} }

func (p *recProvider) Tracer(string, ...trace.TracerOption) trace.Tracer {
	return &recTracer{p: p}
}

// find returns the one span with the exact name, failing the test when
// it is missing or ambiguous — every assertion here is against a tree
// whose span names are unique per test, so start order never matters.
func (p *recProvider) find(t *testing.T, name string) *recSpan {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	var found *recSpan
	for _, s := range p.spans {
		if s.name == name {
			if found != nil {
				t.Fatalf("span %q recorded more than once", name)
			}
			found = s
		}
	}
	if found == nil {
		var names []string
		for _, s := range p.spans {
			names = append(names, s.name)
		}
		t.Fatalf("span %q not recorded; have %v", name, names)
	}
	return found
}

type recTracer struct {
	trace.Tracer // embedded marker
	p            *recProvider
}

func (t *recTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	cfg := trace.NewSpanStartConfig(opts...)
	p := t.p
	p.mu.Lock()
	p.next++
	n := p.next
	parent := trace.SpanContextFromContext(ctx)
	traceID := parent.TraceID()
	if !parent.IsValid() {
		var b [16]byte
		b[14], b[15] = byte(n>>8), byte(n)
		traceID = trace.TraceID(b)
	}
	var sid [8]byte
	sid[7] = byte(n)
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     trace.SpanID(sid),
		TraceFlags: trace.FlagsSampled,
	})
	s := &recSpan{name: name, kind: cfg.SpanKind(), sc: sc, parent: parent, recording: true}
	p.spans = append(p.spans, s)
	p.mu.Unlock()
	return trace.ContextWithSpan(ctx, s), s
}

type recSpan struct {
	trace.Span // embedded: the methods the loop and tests use are overridden

	mu        sync.Mutex
	name      string
	kind      trace.SpanKind
	sc        trace.SpanContext
	parent    trace.SpanContext
	attrs     []attribute.KeyValue
	status    codes.Code
	statusMsg string
	events    []string
	ended     bool
	recording bool
}

func (s *recSpan) End(...trace.SpanEndOption) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended, s.recording = true, false
}

func (s *recSpan) SpanContext() trace.SpanContext { return s.sc }

func (s *recSpan) IsRecording() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recording
}

func (s *recSpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attrs = append(s.attrs, attrs...)
}

func (s *recSpan) SetStatus(c codes.Code, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status, s.statusMsg = c, msg
}

func (s *recSpan) RecordError(err error, _ ...trace.EventOption) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "exception: "+err.Error())
}

func (s *recSpan) Name() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.name
}

// attrsMap flattens the span's attributes for exact-set assertions.
func (s *recSpan) attrsMap() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for _, kv := range s.attrs {
		out[string(kv.Key)] = kv.Value.String()
	}
	return out
}

func (s *recSpan) state() (ended bool, status codes.Code, statusMsg string, events []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended, s.status, s.statusMsg, append([]string(nil), s.events...)
}

// spanEcho is the standard tool of the span tests: one string in, one
// string out, nothing async.
var spanEcho = weft.Tool("echo", "Echo the message.", func(_ context.Context, in struct {
	Msg string `json:"msg"`
}) (string, error) {
	return "echo: " + in.Msg, nil
})

// spanScript is the standard two-step script: one echo call, then a
// final reply. wefttest fixes usage at 10 input / 5 output tokens per
// step, so the token attributes are assertable.
func spanScript() *wefttest.Model {
	return wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hi"}`}),
		wefttest.Say("done"),
	)
}

// spanAgent builds the standard agent under a recording provider.
func spanAgent(tp trace.TracerProvider, opts ...weft.Option) *weft.Agent {
	return weft.New(spanScript(), append([]weft.Option{
		weft.Name("demo"), weft.TracerProvider(tp), spanEcho,
	}, opts...)...)
}

// S1: one invoke_agent span per run, ended on success with the run's
// totals; the attribute set is exactly ADR 0016's tables — no more.
func TestSpansRunTree(t *testing.T) {
	tp := newRecProvider()
	agt := spanAgent(tp)
	if _, err := agt.Generate(context.Background(), weft.RunID("run-1"), weft.Prompt("hello")); err != nil {
		t.Fatal(err)
	}
	run := tp.find(t, "invoke_agent demo")
	if run.kind != trace.SpanKindInternal {
		t.Errorf("run span kind = %v, want Internal", run.kind)
	}
	if run.parent.IsValid() {
		t.Errorf("run span has a parent %v, want a root", run.parent)
	}
	ended, status, _, events := run.state()
	if !ended || status != codes.Ok || len(events) != 0 {
		t.Errorf("run span ended=%v status=%v events=%v, want ended/Ok/none", ended, status, events)
	}
	want := map[string]string{
		"gen_ai.operation.name":      "invoke_agent",
		"gen_ai.agent.name":          "demo",
		"gen_ai.provider.name":       "wefttest",
		"gen_ai.request.model":       "script",
		"weft.run.id":                "run-1",
		"gen_ai.usage.input_tokens":  "20",
		"gen_ai.usage.output_tokens": "10",
		"weft.run.steps":             "2",
		"weft.run.stop_reason":       "stop",
	}
	got := run.attrsMap()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("run span attr %s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("run span attrs = %v, want exactly %v", got, want)
	}
}

// S2: one chat span per model call, under the run span, carrying the
// call's usage and finish reason; tool execution sits outside it.
func TestSpansChatBoundsTheModelCall(t *testing.T) {
	tp := newRecProvider()
	if _, err := spanAgent(tp).Generate(context.Background(), weft.RunID("r"), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	run := tp.find(t, "invoke_agent demo")
	chats := 0
	tp.mu.Lock()
	for _, s := range tp.spans {
		if s.name != "chat script" {
			continue
		}
		chats++
		if s.kind != trace.SpanKindClient {
			t.Errorf("chat span kind = %v, want Client", s.kind)
		}
		if s.parent.SpanID() != run.sc.SpanID() {
			t.Errorf("chat span parent = %v, want the run span", s.parent.SpanID())
		}
		ended, status, _, _ := s.state()
		if !ended || status != codes.Ok {
			t.Errorf("chat span ended=%v status=%v, want ended/Ok", ended, status)
		}
	}
	tp.mu.Unlock()
	if chats != 2 {
		t.Fatalf("chat spans = %d, want 2", chats)
	}
	want := map[string]string{
		"gen_ai.operation.name":          "chat",
		"gen_ai.provider.name":           "wefttest",
		"gen_ai.request.model":           "script",
		"weft.run.id":                    "r",
		"weft.step.index":                "0",
		"gen_ai.usage.input_tokens":      "10",
		"gen_ai.usage.output_tokens":     "5",
		"gen_ai.response.finish_reasons": `["tool_calls"]`,
		"weft.model.tool_calls":          "1",
	}
	var first *recSpan
	tp.mu.Lock()
	for _, s := range tp.spans {
		if s.name == "chat script" && s.attrsMap()["weft.step.index"] == "0" {
			first = s
		}
	}
	tp.mu.Unlock()
	if first == nil {
		t.Fatal("no chat span for step 0")
	}
	got := first.attrsMap()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("chat span attr %s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("chat span attrs = %v, want exactly %v", got, want)
	}
	tool := tp.find(t, "execute_tool echo")
	if tool.parent.SpanID() != run.sc.SpanID() {
		t.Errorf("tool span parent = %v, want the run span (tools run outside the chat span)", tool.parent.SpanID())
	}
}

// S3: the tool span is on the handler's context — the span a handler
// finds there is the execute_tool span itself — and carries the call's
// identity attributes.
func TestSpansToolIsHandlerParent(t *testing.T) {
	var name string
	var recording bool
	probe := weft.Tool("probe", "Record the span it runs under.", func(ctx context.Context, _ struct{}) (string, error) {
		// trace.Span has no Name in the API's interface; the recording
		// span carries one, and asserting for it proves the ctx holds
		// the observer's span, not a no-op.
		if n, ok := trace.SpanFromContext(ctx).(interface{ Name() string }); ok {
			name = n.Name()
		}
		recording = trace.SpanFromContext(ctx).IsRecording()
		return "ok", nil
	})
	tp := newRecProvider()
	agt := weft.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "probe"}), wefttest.Say("done")),
		weft.TracerProvider(tp), probe)
	if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if name != "execute_tool probe" || !recording {
		t.Fatalf("handler span = %q recording=%v, want %q recording", name, recording, "execute_tool probe")
	}
	tool := tp.find(t, "execute_tool probe")
	want := map[string]string{
		"gen_ai.operation.name":  "execute_tool",
		"gen_ai.tool.name":       "probe",
		"gen_ai.tool.call.id":    "call_1",
		"weft.step.index":        "0",
		"weft.tool.seq":          "1", // the ToolStart's Seq
		"weft.tool.result_bytes": "2", // len("ok")
	}
	got := tool.attrsMap()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("tool span attr %s = %q, want %q", k, got[k], v)
		}
	}
	if got["weft.run.id"] == "" {
		t.Error("tool span lacks weft.run.id")
	}
	if len(got) != len(want)+1 {
		t.Errorf("tool span attrs = %v, want exactly %s + run id", got, want)
	}
	ended, status, _, _ := tool.state()
	if !ended || status != codes.Ok {
		t.Errorf("tool span ended=%v status=%v, want ended/Ok", ended, status)
	}
}

// S3: a child run's invoke_agent span nests under the delegating tool
// span, exactly as Nested events nest inside ToolStart..ToolFinish.
func TestSpansSubagentNests(t *testing.T) {
	tp := newRecProvider()
	child := weft.New(wefttest.Script(wefttest.Say("child reply")), weft.TracerProvider(tp), weft.Name("research"))
	orch := weft.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "research"}), wefttest.Say("done")),
		weft.TracerProvider(tp), weft.Name("orchestrator"),
		weft.Subagent("research", "Delegate research.", child))
	if _, err := orch.Generate(context.Background(), weft.RunID("p"), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	delegation := tp.find(t, "execute_tool research")
	childRun := tp.find(t, "invoke_agent research")
	if childRun.parent.SpanID() != delegation.sc.SpanID() {
		t.Errorf("child run span parent = %v, want the delegating execute_tool span", childRun.parent.SpanID())
	}
	if id := childRun.attrsMap()["weft.run.id"]; id != "p/0/call_1" {
		t.Errorf("child run id attr = %q, want %q", id, "p/0/call_1")
	}
}

// S3: a parked (approval) call's span ends pending, with no error
// status — the call did not fail, it was not made.
func TestSpansPendingCall(t *testing.T) {
	tp := newRecProvider()
	gated := weft.Tool("gated", "Needs a human.", func(context.Context, struct{}) (string, error) {
		return "ran", nil
	}, weft.RequireApproval())
	agt := weft.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "gated"}), wefttest.Say("done")),
		weft.TracerProvider(tp), gated)
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil || len(res.Pending) != 1 {
		t.Fatalf("res=%v err=%v, want a pending call", res, err)
	}
	span := tp.find(t, "execute_tool gated")
	attrs := span.attrsMap()
	if attrs["weft.tool.pending"] != "true" {
		t.Errorf("pending attr = %q, want true", attrs["weft.tool.pending"])
	}
	if _, ok := attrs["error.type"]; ok {
		t.Error("pending span carries error.type")
	}
	ended, status, _, _ := span.state()
	if !ended || status != codes.Unset {
		t.Errorf("pending span ended=%v status=%v, want ended/Unset", ended, status)
	}
	if got := tp.find(t, "invoke_agent").attrsMap()["weft.run.pending"]; got != "1" {
		t.Errorf("run span weft.run.pending = %q, want 1", got)
	}
}

// S4: a tool error result is a status and an error.type — the ToolError
// code when there is one, "timeout" for a timed-out call, "tool_error"
// otherwise — and never the result text. Siblings are unaffected.
func TestSpansToolErrorIsStatusNotContent(t *testing.T) {
	failing := weft.Tool("failing", "Fails with a code.", func(context.Context, struct{}) (string, error) {
		return "", &weft.ToolError{Code: "ORDER_NOT_FOUND", Message: "order 42 does not exist"}
	})
	slow := weft.Tool("slow", "Times out.", func(ctx context.Context, _ struct{}) (string, error) {
		<-ctx.Done()
		return "never", nil
	}, weft.Timeout(10*time.Millisecond))
	fine := weft.Tool("fine", "Succeeds.", func(context.Context, struct{}) (string, error) {
		return "fine", nil
	})
	tp := newRecProvider()
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "failing"}, wefttest.Call{Name: "slow"}, wefttest.Call{Name: "fine"}),
		wefttest.Say("done"),
	), weft.TracerProvider(tp), failing, slow, fine)
	if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	coded := tp.find(t, "execute_tool failing")
	if got := coded.attrsMap()["error.type"]; got != "ORDER_NOT_FOUND" {
		t.Errorf("error.type = %q, want ORDER_NOT_FOUND", got)
	}
	_, status, msg, events := coded.state()
	if status != codes.Error || msg != "" || len(events) != 0 {
		t.Errorf("coded span status=%v msg=%q events=%v, want Error with no text and no exception", status, msg, events)
	}
	timed := tp.find(t, "execute_tool slow")
	if got := timed.attrsMap()["error.type"]; got != "timeout" {
		t.Errorf("timeout error.type = %q, want timeout", got)
	}
	// The result text is model-visible content; it must not reach the span.
	for _, s := range []*recSpan{coded, timed} {
		for k, v := range s.attrsMap() {
			if v == "order 42 does not exist" || v == "never" {
				t.Errorf("span attr %s leaks result text %q", k, v)
			}
		}
	}
	if _, status, _, _ = tp.find(t, "execute_tool fine").state(); status != codes.Ok {
		t.Errorf("sibling span status = %v, want Ok (siblings unaffected)", status)
	}
}

// S5: no message, tool-argument, or result content reaches any span —
// ids, names, counts, reasons, and error types only.
func TestSpansNoContent(t *testing.T) {
	tp := newRecProvider()
	agt := weft.New(spanScript(), weft.TracerProvider(tp), weft.Name("demo"),
		weft.Instructions("SECRET-SYSTEM"), spanEcho)
	if _, err := agt.Generate(context.Background(), weft.Prompt("SECRET-PROMPT")); err != nil {
		t.Fatal(err)
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	for _, s := range tp.spans {
		for k, v := range s.attrsMap() {
			for _, secret := range []string{"SECRET-SYSTEM", "SECRET-PROMPT", "echo: hi", `{"msg":"hi"}`} {
				if v == secret {
					t.Errorf("span %q attr %s leaks content %q", s.name, k, v)
				}
			}
		}
	}
}

// S6: provider names map onto the semconv well-known values; anything
// else passes through verbatim.
func TestSpansProviderNames(t *testing.T) {
	for _, tc := range []struct{ provider, want string }{
		{"openai", "openai"},
		{"anthropic", "anthropic"},
		{"google", "gcp.gemini"},
		{"wefttest", "wefttest"},
		{"acme", "acme"},
	} {
		m := infoModel{wefttest.Script(wefttest.Say("ok")), weft.ModelInfo{Provider: tc.provider, Name: "m1"}}
		tp := newRecProvider()
		agt := weft.New(m, weft.TracerProvider(tp))
		if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
			t.Fatal(err)
		}
		if got := tp.find(t, "chat m1").attrsMap()["gen_ai.provider.name"]; got != tc.want {
			t.Errorf("provider %q mapped to %q, want %q", tc.provider, got, tc.want)
		}
	}
}

// infoModel overrides the scripted model's identity for the
// provider-name mapping test.
type infoModel struct {
	weft.Model
	info weft.ModelInfo
}

func (m infoModel) Info() weft.ModelInfo { return m.info }

// S1: cancellation ends every open span with the ctx error, even though
// no event is delivered after cancellation — the report a tap could
// never make.
func TestSpansEndOnCancellation(t *testing.T) {
	block := weft.Tool("block", "Blocks until canceled.", func(ctx context.Context, _ struct{}) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	tp := newRecProvider()
	agt := weft.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "block"}), wefttest.Say("done")),
		weft.TracerProvider(tp), block)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // the loop cancels on ToolStart; this covers the paths that never reach it
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
	span := tp.find(t, "invoke_agent")
	ended, status, _, events := span.state()
	if !ended {
		t.Fatal("run span never ended after cancellation")
	}
	if status != codes.Error || len(events) != 1 {
		t.Errorf("run span status=%v events=%v, want Error with the recorded failure", status, events)
	}
	if got := span.attrsMap()["error.type"]; got != "context.Canceled" {
		t.Errorf("error.type = %q, want context.Canceled", got)
	}
}

// S9: a resumed call carries weft.tool.approved; a denied call produces
// no span (it never executes).
func TestSpansResumedCalls(t *testing.T) {
	tp := newRecProvider()
	gated := weft.Tool("gated", "Needs a human.", func(context.Context, struct{}) (string, error) {
		return "ran", nil
	}, weft.RequireApproval())
	agt := weft.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "gated"}), wefttest.Say("done")),
		weft.TracerProvider(tp), gated)
	res, err := agt.Generate(context.Background(), weft.Prompt("x"))
	if err != nil || len(res.Pending) != 1 {
		t.Fatalf("res=%v err=%v, want one pending call", res, err)
	}
	id := res.Pending[0].ID
	if _, err := agt.Generate(context.Background(), weft.Messages(res.Messages...), weft.Approve(id)); err != nil {
		t.Fatal(err)
	}
	// Two spans carry the name: the parked call's (pending) from the
	// first run, the executed one's (approved) from the resume.
	var parked, approved bool
	tp.mu.Lock()
	for _, s := range tp.spans {
		if s.name != "execute_tool gated" {
			continue
		}
		attrs := s.attrsMap()
		if attrs["weft.tool.pending"] == "true" {
			parked = true
		}
		if attrs["weft.tool.approved"] == "true" {
			approved = true
		}
	}
	tp.mu.Unlock()
	if !parked || !approved {
		t.Errorf("gated spans: parked=%v approved=%v, want both", parked, approved)
	}
}

// S8: the option replaces the global provider for one agent; nothing
// here — or anywhere in the module's tests — calls
// otel.SetTracerProvider.
func TestTracerProviderOption(t *testing.T) {
	tp := newRecProvider()
	agt := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Options(weft.TracerProvider(tp), weft.Name("opt")))
	if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	tp.find(t, "invoke_agent opt")
	// Without the option the agent runs on the global provider — a
	// no-op until an SDK registers — and must neither panic nor record.
	plain := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("plain"))
	if _, err := plain.Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
}

// ExampleTracerProvider prints the span tree of a two-step run, as an
// exporter would render it.
func ExampleTracerProvider() {
	tp := newRecProvider()
	agt := weft.New(spanScript(), weft.TracerProvider(tp), weft.Name("demo"), spanEcho)
	if _, err := agt.Generate(context.Background(), weft.Prompt("hi")); err != nil {
		return
	}
	tp.mu.Lock()
	bySpan := map[[8]byte]*recSpan{}
	for _, s := range tp.spans {
		bySpan[s.sc.SpanID()] = s
	}
	var print func(s *recSpan, depth int)
	print = func(s *recSpan, depth int) {
		_, status, _, _ := s.state()
		fmt.Printf("%s%s [%v]\n", strings.Repeat("  ", depth), s.name, status)
		for _, child := range tp.spans {
			if child.parent.SpanID() == s.sc.SpanID() {
				print(child, depth+1)
			}
		}
	}
	for _, s := range tp.spans {
		if !s.parent.IsValid() {
			print(s, 0)
		}
	}
	tp.mu.Unlock()
	// Output:
	// invoke_agent demo [Ok]
	//   chat script [Ok]
	//   execute_tool echo [Ok]
	//   chat script [Ok]
}

// The semconv constants the observer must keep using — pinned so a
// semconv version bump that renames a key or value fails loudly.
func TestSemconvConstantsPinned(t *testing.T) {
	for _, c := range []struct {
		kv  attribute.KeyValue
		key string
		val string
	}{
		{semconv.GenAIOperationNameInvokeAgent, "gen_ai.operation.name", "invoke_agent"},
		{semconv.GenAIOperationNameChat, "gen_ai.operation.name", "chat"},
		{semconv.GenAIOperationNameExecuteTool, "gen_ai.operation.name", "execute_tool"},
		{semconv.GenAIProviderNameGCPGemini, "gen_ai.provider.name", "gcp.gemini"},
	} {
		if string(c.kv.Key) != c.key || c.kv.Value.AsString() != c.val {
			t.Errorf("semconv constant drifted: %s=%s, want %s=%s", c.kv.Key, c.kv.Value.AsString(), c.key, c.val)
		}
	}
}

// S1/S2, the error paths: a failed model call ends the chat span and
// the run span with Error status, the recorded exception, and
// error.type "model_error"; the vendor's text is on the exception, not
// the type.
func TestSpansChatErrorPath(t *testing.T) {
	tp := newRecProvider()
	agt := weft.New(failingModel{wefttest.Script(wefttest.Say("x"))}, weft.TracerProvider(tp), weft.Name("a"))
	if _, err := agt.Generate(context.Background(), weft.Prompt("p")); err == nil {
		t.Fatal("run succeeded, want the model error")
	}
	for _, name := range []string{"chat", "invoke_agent a"} {
		s := tp.find(t, name)
		ended, status, msg, events := s.state()
		if !ended || status != codes.Error || len(events) != 1 || !strings.Contains(msg, "HTTP 500") {
			t.Errorf("%s ended=%v status=%v msg=%q events=%v, want ended/Error/one exception", name, ended, status, msg, events)
		}
		attrs := s.attrsMap()
		if attrs["error.type"] != "model_error" {
			t.Errorf("%s error.type = %q, want model_error", name, attrs["error.type"])
		}
		if _, ok := attrs["gen_ai.response.finish_reasons"]; ok && name == "chat" {
			t.Errorf("chat span carries a finish reason for a call that never finished")
		}
	}
}

// failingModel's stream fails before any event. The wrappers here do
// not forward Info, so their chat spans carry the bare name.
type failingModel struct{ weft.Model }

func (failingModel) Stream(context.Context, weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		yield(nil, errors.New("HTTP 500 from the vendor"))
	}
}

// The error.type contract for weft's own failures: one snake_case token
// per sentinel (ADR 0016's table), never the sentinel's text, and the
// context errors by name. Pinned end to end for the two the loop can
// produce on demand, and by table for the rest.
func TestSpansErrorTypes(t *testing.T) {
	echo := weft.Tool("echo", "Echo.", func(context.Context, struct{}) (string, error) { return "ok", nil })
	tp := newRecProvider()
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
	), weft.TracerProvider(tp), weft.MaxSteps(1), echo)
	if _, err := agt.Generate(context.Background(), weft.Prompt("p")); !errors.Is(err, weft.ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps", err)
	}
	if got := tp.find(t, "invoke_agent").attrsMap()["error.type"]; got != "max_steps" {
		t.Errorf("ErrMaxSteps error.type = %q, want max_steps", got)
	}

	tp = newRecProvider()
	agt = weft.New(wefttest.Script(wefttest.Raw(weft.ModelTextDelta{Text: "hi"})), weft.TracerProvider(tp))
	if _, err := agt.Generate(context.Background(), weft.Prompt("p")); !errors.Is(err, weft.ErrModelContract) {
		t.Fatalf("err = %v, want ErrModelContract", err)
	}
	// "chat script": the span carries the model's name (wefttest's is
	// "script"), the way an adapter's would carry gpt-5 or claude.
	for _, name := range []string{"chat script", "invoke_agent"} {
		if got := tp.find(t, name).attrsMap()["error.type"]; got != "model_contract" {
			t.Errorf("%s ErrModelContract error.type = %q, want model_contract", name, got)
		}
	}
	for _, s := range []*recSpan{tp.find(t, "chat script"), tp.find(t, "invoke_agent")} {
		if v := s.attrsMap()["error.type"]; strings.ContainsAny(v, " :") {
			t.Errorf("error.type %q is not a token", v)
		}
	}
}

// S4: a tool call the run's cancellation reached reports the context
// error by name, like the run span, not a generic tool_error.
func TestSpansToolCancellationType(t *testing.T) {
	block := weft.Tool("block", "Blocks until canceled.", func(ctx context.Context, _ struct{}) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	tp := newRecProvider()
	agt := weft.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "block"}), wefttest.Say("done")),
		weft.TracerProvider(tp), block)
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
	if got := tp.find(t, "execute_tool block").attrsMap()["error.type"]; got != "context.Canceled" {
		t.Errorf("tool span error.type = %q, want context.Canceled", got)
	}
}

// S2: the chat span's completion is the loop's finished flag, not an
// inferred one — a ModelFinish with an empty reason (the loop accepts
// it) still lands its usage on the span.
func TestSpansEmptyReasonKeepsUsage(t *testing.T) {
	tp := newRecProvider()
	agt := weft.New(wefttest.Script(wefttest.Raw(
		weft.ModelTextDelta{Text: "hi"},
		weft.ModelFinish{Usage: weft.Usage{InputTokens: 7, OutputTokens: 3}},
	)), weft.TracerProvider(tp))
	if _, err := agt.Generate(context.Background(), weft.Prompt("p")); err != nil {
		t.Fatal(err)
	}
	got := tp.find(t, "chat script").attrsMap()
	if got["gen_ai.usage.input_tokens"] != "7" || got["gen_ai.usage.output_tokens"] != "3" {
		t.Errorf("chat span usage = %v, want 7 in / 3 out", got)
	}
}

// S1: a panic nothing contains — a PrepareStep function is arbitrary
// user code — ends the run span on its way out; the crash itself still
// reaches the caller, and the span reports run_panicked.
func TestSpansRunSpanEndsOnPanic(t *testing.T) {
	tp := newRecProvider()
	agt := weft.New(wefttest.Script(wefttest.Say("x")),
		weft.TracerProvider(tp), weft.Name("boom"),
		weft.PrepareStep(func(_ context.Context, _ int, req weft.ModelRequest) (weft.ModelRequest, error) {
			panic("prepare exploded")
		}))
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic did not reach the caller")
			}
		}()
		_, _ = agt.Generate(context.Background(), weft.Prompt("p"))
	}()
	span := tp.find(t, "invoke_agent boom")
	ended, status, _, events := span.state()
	if !ended {
		t.Fatal("run span never ended after the panic")
	}
	if status != codes.Error || len(events) != 1 {
		t.Errorf("run span status=%v events=%v, want Error with the recorded panic", status, events)
	}
	if got := span.attrsMap()["error.type"]; got != "run_panicked" {
		t.Errorf("error.type = %q, want run_panicked", got)
	}
}

// The usage splits ride the spans in their semconv/v1.41.0 names, each
// only when non-zero (ADR 0016's 2026-09-22 amendment). Absence is
// pinned by every exact-set assertion above (the scripted turns carry
// no splits); this is the presence half.
func TestSpansUsageSplits(t *testing.T) {
	tp := newRecProvider()
	script := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hi"}`}).WithUsage(weft.Usage{
			InputTokens: 100, OutputTokens: 5, CachedInputTokens: 60, CacheWriteTokens: 10, ReasoningTokens: 3,
		}),
		wefttest.Say("done"),
	)
	agt := weft.New(script, weft.Name("demo"), weft.TracerProvider(tp), spanEcho)
	if _, err := agt.Generate(context.Background(), weft.RunID("run-splits"), weft.Prompt("q")); err != nil {
		t.Fatal(err)
	}
	var chat, run *recSpan
	for _, s := range tp.spans {
		switch {
		case s.name == "chat script" && s.attrsMap()["weft.step.index"] == "0":
			chat = s
		case s.name == "invoke_agent demo":
			run = s
		}
	}
	if chat == nil || run == nil {
		t.Fatalf("chat/run span missing: %v", tp.spans)
	}
	for name, span := range map[string]*recSpan{"chat": chat, "run": run} {
		got := span.attrsMap()
		want := map[string]string{
			"gen_ai.usage.cache_read.input_tokens":     "60",
			"gen_ai.usage.cache_creation.input_tokens": "10",
			"gen_ai.usage.reasoning.output_tokens":     "3",
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s span attr %s = %q, want %q", name, k, got[k], v)
			}
		}
	}
}
