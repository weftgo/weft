package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// toolsRecords captures the body of every weft.tools record.
type toolsRecords struct {
	embedded.LoggerProvider
	mu     sync.Mutex
	bodies []string
}

type toolsLogger struct {
	embedded.Logger
	p *toolsRecords
}

func (p *toolsRecords) Logger(string, ...log.LoggerOption) log.Logger { return toolsLogger{p: p} }
func (toolsLogger) Enabled(context.Context, log.EnabledParameters) bool {
	return true
}
func (l toolsLogger) Emit(_ context.Context, r log.Record) {
	if r.EventName() != "weft.tools" {
		return
	}
	l.p.mu.Lock()
	defer l.p.mu.Unlock()
	l.p.bodies = append(l.p.bodies, r.Body().AsString())
}

// A tool imported by Tools is an MCP tool in the observability record:
// the tools record (ADR 0028 §5) names its source "mcp:<server>" under
// Server, "mcp" without it (O8), with the replay class it actually has
// (unannotated: never).
func TestToolsRecordSourceIsMCP(t *testing.T) {
	for name, c := range map[string]struct {
		opts []Option
		want string
	}{
		"default":         {want: "mcp"},
		"named server":    {opts: []Option{Server("billing")}, want: "mcp:billing"},
		"caller's Origin": {opts: []Option{Server("billing"), Policy(core.Origin("github"))}, want: "github"},
	} {
		t.Run(name, func(t *testing.T) { toolsRecordSource(t, c.opts, c.want) })
	}
}

func toolsRecordSource(t *testing.T, importOpts []Option, want string) {
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "remote", true, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
		})
	})
	defer stop()
	tools, err := Tools(context.Background(), sess, importOpts...)
	if err != nil {
		t.Fatal(err)
	}
	rec := &toolsRecords{}
	opts := []core.Option{core.LoggerProvider(rec), core.Content(true)}
	for _, td := range tools {
		opts = append(opts, td)
	}
	if _, err := core.New(wefttest.Script(wefttest.Say("done")), opts...).Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.bodies) != 1 {
		t.Fatalf("tools records = %d, want 1", len(rec.bodies))
	}
	var body struct {
		Tools []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
			Replay string `json:"replay"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(rec.bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tools) != 1 || body.Tools[0].Source != want || body.Tools[0].Replay != "never" {
		t.Errorf("tools record = %s, want the remote tool with source %s, replay never", rec.bodies[0], want)
	}
}

// O8: under Server, Transport and Prefix every imported tool's tools
// record entry carries source_detail — the server, the transport, the
// prefix and its name on the server — and the record lists the tools
// the listing skipped once, with why and the source they would have
// come from; each call's execute_tool span names the origin
// (weft.tool.origin), a remote isError reads mcp_tool_error and a
// severed session mcp_transport.
func TestToolsRecordNamesTheServer(t *testing.T) {
	ok := func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
	}
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "get_invoice", true, ok)
		addRemoteTool(srv, "legacy_ledger", true, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "ledger offline"}}}, nil
		})
		odd := &sdk.Tool{Name: "odd", Description: "odd", InputSchema: json.RawMessage(`{"type":"object"}`)}
		srv.AddTool(odd, ok)
		odd.InputSchema = json.RawMessage(`{"type":["object","null"]}`)
	})
	defer stop()
	tools, err := Tools(context.Background(), sess, Server("billing"), Transport("memory"), Prefix("svc_"))
	var ie *ImportError
	if !errors.As(err, &ie) || len(tools) != 2 {
		t.Fatalf("tools = %d, err = %v; want two imported and odd skipped", len(tools), err)
	}
	// A second server, closed before the call: a transport failure.
	gone, stopGone := served(t, func(srv *sdk.Server) { addRemoteTool(srv, "ping", true, ok) })
	defer stopGone()
	pings, err := Tools(context.Background(), gone, Server("ledger"))
	if err != nil {
		t.Fatal(err)
	}
	_ = gone.Close()

	rec := &toolsRecords{}
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	opts := []core.Option{core.LoggerProvider(rec), core.TracerProvider(tp), core.Content(true)}
	for _, td := range append(tools, pings...) {
		opts = append(opts, td)
	}
	model := wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "svc_get_invoice", Args: `{"q":"a"}`},
			wefttest.Call{Name: "svc_legacy_ledger", Args: `{"q":"a"}`},
			wefttest.Call{Name: "ping", Args: `{"q":"a"}`}),
		wefttest.Say("done"))
	if _, err := core.New(model, opts...).Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	body := rec.bodies[0]
	rec.mu.Unlock()
	var got struct {
		Tools []struct {
			Name         string            `json:"name"`
			Source       string            `json:"source"`
			SourceDetail map[string]string `json:"source_detail"`
		} `json:"tools"`
		Skipped []map[string]string `json:"skipped"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	byName := map[string]map[string]string{}
	for _, e := range got.Tools {
		byName[e.Name] = e.SourceDetail
		byName[e.Name]["source"] = e.Source
	}
	if d := byName["svc_get_invoice"]; d["source"] != "mcp:billing" || d["server"] != "billing" ||
		d["transport"] != "memory" || d["prefix"] != "svc_" || d["remote_name"] != "get_invoice" {
		t.Errorf("svc_get_invoice = %v, want mcp:billing with its parts", d)
	}
	if d := byName["ping"]; d["source"] != "mcp:ledger" || d["server"] != "ledger" || d["transport"] != "" || d["prefix"] != "" {
		t.Errorf("ping = %v, want mcp:ledger, no transport or prefix", d)
	}
	if len(got.Skipped) != 1 || got.Skipped[0]["name"] != "odd" || got.Skipped[0]["source"] != "mcp:billing" ||
		!strings.Contains(got.Skipped[0]["reason"], "ParseSchema") {
		t.Errorf("skipped = %v, want odd from mcp:billing with ParseSchema's reason", got.Skipped)
	}
	spans := map[string]map[string]string{}
	for _, s := range sr.Ended() {
		a := map[string]string{}
		for _, kv := range s.Attributes() {
			a[string(kv.Key)] = kv.Value.String()
		}
		if a["gen_ai.operation.name"] == "execute_tool" {
			spans[a["gen_ai.tool.name"]] = a
		}
	}
	for name, want := range map[string][2]string{
		"svc_get_invoice":   {"mcp:billing", ""},
		"svc_legacy_ledger": {"mcp:billing", "mcp_tool_error"},
		"ping":              {"mcp:ledger", "mcp_transport"},
	} {
		if a := spans[name]; a["weft.tool.origin"] != want[0] || a["error.type"] != want[1] {
			t.Errorf("%s span: origin %q error.type %q, want %q %q", name, a["weft.tool.origin"], a["error.type"], want[0], want[1])
		}
	}
}
