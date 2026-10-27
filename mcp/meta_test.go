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

// TestServeLinksTheCaller (U33.1, UX-S2-08's remainder): an agent
// tool imported through Tools sends its caller in params._meta — the
// execute_tool span's traceparent and the weft run and call — and
// Serve's agent tool reads it back: the served run's invoke_agent span
// is in the caller's trace, under the caller's tool span (a remote
// parent), and records weft.caller.run.id / weft.caller.call.id, never
// weft.parent.* (a link, not parenthood, O11).
func TestServeLinksTheCaller(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	helper := core.New(wefttest.Script(wefttest.Say("helped")), core.Name("helper"), core.TracerProvider(tp))
	srv := sdk.NewServer(&sdk.Implementation{Name: "helpers", Version: "0"}, nil)
	Serve(srv, helper, "Helps with anything.")
	st, ct := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), st) }()
	sess, err := sdk.NewClient(&sdk.Implementation{Name: "lead", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	tools, err := Tools(context.Background(), sess, Server("helpers"))
	if err != nil {
		t.Fatal(err)
	}
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
	if got, want := served.SpanContext().TraceID(), leadTool.SpanContext().TraceID(); got != want {
		t.Errorf("served run's trace %s, want the caller's %s", got, want)
	}
	if got, want := served.Parent().SpanID(), leadTool.SpanContext().SpanID(); got != want || !served.Parent().IsRemote() {
		t.Errorf("served run's parent span %s (remote %v), want the caller's tool span %s", got, served.Parent().IsRemote(), want)
	}
	for k, want := range map[string]string{"weft.caller.run.id": "lead1", "weft.caller.call.id": "c_h",
		"weft.parent.run.id": "", "weft.parent.call.id": ""} {
		if got := attrOf(served, k); got != want {
			t.Errorf("served run %s = %q, want %q", k, got, want)
		}
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
		if s.Parent().IsValid() || attrOf(s, "weft.caller.run.id") != "" {
			t.Errorf("a malformed _meta linked the run: parent %v, caller %q", s.Parent(), attrOf(s, "weft.caller.run.id"))
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
