package mcp

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/wefttest"
)

func attrOf(s sdktrace.ReadOnlySpan, k string) string {
	for _, a := range s.Attributes() {
		if a.Key == attribute.Key(k) {
			return a.Value.String()
		}
	}
	return ""
}

// serveHelper stands up Serve(helper) in memory and imports its tools
// with opts; the session closes at cleanup.
func serveHelper(t *testing.T, tp *sdktrace.TracerProvider, opts ...Option) []*core.ToolDef {
	t.Helper()
	helper := core.New(wefttest.Script(wefttest.Say("helped"), wefttest.Say("helped again")), core.Name("helper"), core.TracerProvider(tp))
	srv := sdk.NewServer(&sdk.Implementation{Name: "helpers", Version: "0"}, nil)
	Serve(srv, helper, "Helps with anything.")
	st, ct := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), st) }()
	sess, err := sdk.NewClient(&sdk.Implementation{Name: "lead", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	tools, err := Tools(context.Background(), sess, append([]Option{Server("helpers")}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return tools
}

// leadCalls runs a lead agent that calls the imported helper tool once.
func leadCalls(t *testing.T, tp *sdktrace.TracerProvider, tools []*core.ToolDef) {
	t.Helper()
	opts := []core.Option{core.Name("lead"), core.TracerProvider(tp)}
	for _, tl := range tools {
		opts = append(opts, tl)
	}
	lead := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "helper", Args: `{"prompt":"help me"}`, ID: "c_h"}),
		wefttest.Say("done"),
	), opts...)
	if _, err := lead.Generate(context.Background(), core.RunID("lead1"), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
}

// TestServeLinksTheCaller (U33.1, U33.1-fix; UX-S2-08's remainder):
// under CallerMeta an imported agent tool sends its caller in
// params._meta — the execute_tool span's traceparent and the weft run
// and call — and Serve's agent tool reads it back: the served run's
// invoke_agent span starts its own trace, linked (an OpenTelemetry
// link) to the caller's tool span, and records weft.caller.run.id /
// weft.caller.call.id with weft.caller.remote, never weft.parent.*.
func TestServeLinksTheCaller(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	leadCalls(t, tp, serveHelper(t, tp, CallerMeta()))

	var leadTool, served sdktrace.ReadOnlySpan
	for _, s := range exp.GetSpans().Snapshots() {
		switch {
		case s.Name() == "execute_tool helper" && attrOf(s, "weft.run.id") == "lead1":
			leadTool = s
		case s.Name() == "invoke_agent helper":
			served = s
		}
	}
	if leadTool == nil || served == nil {
		t.Fatalf("spans: lead's tool %v, served run %v", leadTool, served)
	}
	if served.Parent().IsValid() || served.SpanContext().TraceID() == leadTool.SpanContext().TraceID() {
		t.Errorf("served run parent %v trace %s: want a new root, not the caller's (%s) child", served.Parent(), served.SpanContext().TraceID(), leadTool.SpanContext().TraceID())
	}
	links := served.Links()
	if len(links) != 1 || links[0].SpanContext.TraceID() != leadTool.SpanContext().TraceID() ||
		links[0].SpanContext.SpanID() != leadTool.SpanContext().SpanID() {
		t.Errorf("served run links %+v, want one link to the caller's tool span %s/%s", links, leadTool.SpanContext().TraceID(), leadTool.SpanContext().SpanID())
	}
	for k, want := range map[string]string{"weft.caller.run.id": "lead1", "weft.caller.call.id": "c_h", "weft.caller.remote": "true",
		"weft.parent.run.id": "", "weft.parent.call.id": ""} {
		if got := attrOf(served, k); got != want {
			t.Errorf("served run %s = %q, want %q", k, got, want)
		}
	}
}

// TestCallerMetaOptIn: without CallerMeta an imported tool sends no
// _meta — the weft ids and trace state stay home — so the served run is
// unlinked.
func TestCallerMetaOptIn(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	leadCalls(t, tp, serveHelper(t, tp))
	seen := false
	for _, s := range exp.GetSpans().Snapshots() {
		if s.Name() != "invoke_agent helper" {
			continue
		}
		seen = true
		if len(s.Links()) != 0 || attrOf(s, "weft.caller.run.id") != "" || attrOf(s, "weft.caller.remote") != "" {
			t.Errorf("served run without CallerMeta: links %v, caller %q", s.Links(), attrOf(s, "weft.caller.run.id"))
		}
	}
	if !seen {
		t.Fatal("no served run")
	}
}

// TestServeUnsampledCallerStillRecorded: a caller whose traceparent says
// "not sampled" (flags 00) does not decide for the server — the served
// run is a root its own sampler keeps, exported, its link carrying the
// caller's trace and span ids.
func TestServeUnsampledCallerStillRecorded(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp)) // ParentBased(AlwaysSample)
	defer func() { _ = tp.Shutdown(context.Background()) }()
	helper := core.New(wefttest.Script(wefttest.Say("helped")), core.Name("helper"), core.TracerProvider(tp))
	srv := sdk.NewServer(&sdk.Implementation{Name: "helpers", Version: "0"}, nil)
	Serve(srv, helper, "Helps.")
	st, ct := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), st) }()
	sess, err := sdk.NewClient(&sdk.Implementation{Name: "c", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{
		Meta: sdk.Meta{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00",
			MetaCaller: map[string]any{"run_id": "lead9", "call_id": "c9"}},
		Name: "helper", Arguments: map[string]any{"prompt": "x"},
	})
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res)
	}
	var served sdktrace.ReadOnlySpan
	for _, s := range exp.GetSpans().Snapshots() {
		if s.Name() == "invoke_agent helper" {
			served = s
		}
	}
	if served == nil {
		t.Fatalf("an unsampled caller's flag decided for the server: %d spans exported, no served run", len(exp.GetSpans()))
	}
	if l := served.Links(); len(l) != 1 || l[0].SpanContext.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" ||
		l[0].SpanContext.SpanID().String() != "00f067aa0ba902b7" || l[0].SpanContext.IsSampled() {
		t.Errorf("served run links %+v, want the caller's unsampled span", l)
	}
	if attrOf(served, "weft.caller.run.id") != "lead9" || attrOf(served, "weft.caller.remote") != "true" {
		t.Errorf("served run caller %q remote %q", attrOf(served, "weft.caller.run.id"), attrOf(served, "weft.caller.remote"))
	}
}

// TestServedMetaIgnoredWhenMalformed: a _meta that does not read — a
// bad traceparent, a caller that is not an object, an over-long id —
// links nothing and refuses nothing: the call runs as an unlinked run.
func TestServedMetaIgnoredWhenMalformed(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	helper := core.New(wefttest.Script(wefttest.Say("a"), wefttest.Say("b"), wefttest.Say("c")), core.Name("helper"), core.TracerProvider(tp))
	srv := sdk.NewServer(&sdk.Implementation{Name: "helpers", Version: "0"}, nil)
	Serve(srv, helper, "Helps.")
	st, ct := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), st) }()
	sess, err := sdk.NewClient(&sdk.Implementation{Name: "c", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	long := string(make([]byte, metaIDMax+1))
	for _, meta := range []sdk.Meta{
		{"traceparent": "00-zz-11-01", MetaCaller: "lead1"},
		{"traceparent": "00-00000000000000000000000000000000-0000000000000000-01"},
		{MetaCaller: map[string]any{"run_id": long, "call_id": "c"}},
	} {
		res, err := sess.CallTool(context.Background(), &sdk.CallToolParams{Meta: meta, Name: "helper", Arguments: map[string]any{"prompt": "x"}})
		if err != nil || res.IsError {
			t.Fatalf("call with _meta %v: %v %+v", meta, err, res)
		}
	}
	for _, s := range exp.GetSpans().Snapshots() {
		if s.Name() != "invoke_agent helper" {
			continue
		}
		if s.Parent().IsValid() || len(s.Links()) != 0 || attrOf(s, "weft.caller.run.id") != "" {
			t.Errorf("a malformed _meta linked the run: parent %v, links %v, caller %q", s.Parent(), s.Links(), attrOf(s, "weft.caller.run.id"))
		}
	}
}

func TestParseTraceparent(t *testing.T) {
	for _, c := range []struct {
		in string
		ok bool
	}{
		{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", true},
		{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00", true},
		{"01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-extra", true}, // a later version: its first four fields
		{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-extra", false},
		{"ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", false},
		{"00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01", false},
		{"00-00000000000000000000000000000000-00f067aa0ba902b7-01", false},
		{"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01", false},
		{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-0g", false},
		{"", false},
	} {
		sc, ok := parseTraceparent(c.in, "")
		if ok != c.ok {
			t.Errorf("parseTraceparent(%q) ok = %v, want %v", c.in, ok, c.ok)
		}
		if ok && (!sc.IsRemote() || sc.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736") {
			t.Errorf("parseTraceparent(%q) = %+v", c.in, sc)
		}
	}
	sc, _ := parseTraceparent("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "vendor=x")
	if sc.TraceState().Get("vendor") != "x" || sc.TraceFlags() != trace.FlagsSampled {
		t.Errorf("tracestate / flags lost: %+v", sc)
	}
}
