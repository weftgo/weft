package main

// The real-SDK half of P1: through go.opentelemetry.io/otel/sdk and the
// in-memory exporter, the same tree the root module's recording-tracer
// tests assert — so the API-only implementation is proven against the
// SDK a user actually registers (ADR 0016).

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

func TestSpanTreeThroughRealSDK(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	if _, err := run(context.Background(), tp); err != nil {
		t.Fatal(err)
	}
	spans := exp.GetSpans()
	byName := map[string]*tracetest.SpanStub{}
	for i := range spans {
		s := spans[i]
		if _, dup := byName[s.Name]; dup && s.Name != "chat script" {
			t.Fatalf("span %q recorded more than once", s.Name)
		}
		byName[s.Name] = &spans[i]
	}
	root, ok := byName["invoke_agent demo"]
	if !ok {
		t.Fatalf("no run span; got %v", spanNames(spans))
	}
	if root.Parent.IsValid() {
		t.Errorf("run span has parent %v, want a root", root.Parent)
	}
	// The child run's invoke_agent nests under the delegating tool span,
	// and the tool spans nest under the run.
	delegation := byName["execute_tool research"]
	child := byName["invoke_agent research"]
	if !child.Parent.Equal(delegation.SpanContext) {
		t.Errorf("child run parent = %v, want the delegating execute_tool span", child.Parent)
	}
	for _, name := range []string{"execute_tool research", "execute_tool echo"} {
		if s := byName[name]; !s.Parent.Equal(root.SpanContext) {
			t.Errorf("%s parent = %v, want the run span", name, s.Parent)
		}
	}
	// Four model calls: three by the parent, one by the child.
	chats := 0
	for _, s := range spans {
		if s.Name == "chat script" {
			chats++
		}
	}
	if chats != 4 {
		t.Errorf("chat spans = %d, want 4", chats)
	}
	// The run span carries the whole bill, the child included: 3 calls
	// × wefttest's fixed 10 in / 5 out.
	for _, kv := range root.Attributes {
		if string(kv.Key) == "gen_ai.usage.input_tokens" && kv.Value.AsInt64() != 40 {
			t.Errorf("run input tokens = %d, want 40 (subagents included)", kv.Value.AsInt64())
		}
	}
}

func spanNames(spans []tracetest.SpanStub) []string {
	out := make([]string, len(spans))
	for i, s := range spans {
		out[i] = s.Name
	}
	return out
}

// TestGlobalProviderPath proves zero-config instrumentation the other
// way: with no TracerProvider option at all, registering the SDK
// globally makes weft's spans appear (P1) — and the agent is built
// *before* the SDK registers, the common program order, so the test
// covers the global delegate's hand-off, not just a direct lookup.
// This module's tests are the one place allowed to touch the global;
// note the OTel global is set-once by design (the delegate keeps
// forwarding to the first SDK), so no later test in this module may
// rely on the global being pristine.
func TestGlobalProviderPath(t *testing.T) {
	echo := weft.Tool("echo", "Echo.", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	agt := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("plain"), echo)

	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer func() {
		otel.SetTracerProvider(old)
		_ = tp.Shutdown(context.Background())
	}()
	if _, err := agt.Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if n := len(exp.GetSpans()); n == 0 {
		t.Fatal("no spans recorded through the global provider")
	}
}
