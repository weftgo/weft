package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"
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
// the tools record (ADR 0028 §5) names its source "mcp", with the
// replay class it actually has (unannotated: never).
func TestToolsRecordSourceIsMCP(t *testing.T) {
	sess, stop := served(t, func(srv *sdk.Server) {
		addRemoteTool(srv, "remote", true, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
		})
	})
	defer stop()
	tools, err := Tools(context.Background(), sess)
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
	if len(body.Tools) != 1 || body.Tools[0].Source != "mcp" || body.Tools[0].Replay != "never" {
		t.Errorf("tools record = %s, want the remote tool with source mcp, replay never", rec.bodies[0])
	}
}
