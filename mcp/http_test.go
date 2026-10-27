package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft/core"
)

// U23.2-fix2: over streamable HTTP the go-sdk wraps its own transport
// failures — a 5xx / 429 answer, a dropped connection — as JSON-RPC
// errors (ErrRejected, -32005). They stay mcp_transport ("MCP
// unreachable"); only a server's own JSON-RPC error answer (an unknown
// tool, a failing raw handler) is mcp_rpc_error. A closed session
// stays transport.
func TestToolsStreamableHTTPErrorKinds(t *testing.T) {
	srv := sdk.NewServer(&sdk.Implementation{Name: "http", Version: "0"}, nil)
	addRemoteTool(srv, "ping", true, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "pong"}}}, nil
	})
	addRemoteTool(srv, "explode", true, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return nil, errors.New("remote exploded")
	})
	mcpHandler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	// mode 0 serves; 1 answers 503; 2 drops the connection.
	var mode atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 1:
			http.Error(w, "overloaded", http.StatusServiceUnavailable)
		case 2:
			if hj, ok := w.(http.Hijacker); ok {
				if c, _, err := hj.Hijack(); err == nil {
					_ = c.Close()
					return
				}
			}
			panic(http.ErrAbortHandler)
		default:
			mcpHandler.ServeHTTP(w, r)
		}
	}))
	defer ts.Close()
	connect := func() *sdk.ClientSession {
		t.Helper()
		sess, err := sdk.NewClient(&sdk.Implementation{Name: "c", Version: "0"}, nil).Connect(context.Background(),
			&sdk.StreamableClientTransport{Endpoint: ts.URL, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return sess
	}
	sess := connect()
	defer func() { _ = sess.Close() }()
	tools, err := Tools(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*core.ToolDef{}
	for _, td := range tools {
		byName[td.Name] = td
	}
	call := func(f func(context.Context, json.RawMessage) (string, error)) string {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := f(ctx, json.RawMessage(`{"q":"a"}`))
		return errorTypeOf(err)
	}
	if got := call(callHandler(sess, "never_listed")); got != "mcp_rpc_error" {
		t.Errorf("unknown tool over HTTP = %q, want mcp_rpc_error", got)
	}
	if got := call(byName["explode"].Invoke); got != "mcp_rpc_error" {
		t.Errorf("handler error over HTTP = %q, want mcp_rpc_error", got)
	}
	mode.Store(1)
	if got := call(byName["ping"].Invoke); got != "mcp_transport" {
		t.Errorf("HTTP 503 = %q, want mcp_transport", got)
	}
	mode.Store(2)
	if got := call(byName["ping"].Invoke); got != "mcp_transport" {
		t.Errorf("dropped connection = %q, want mcp_transport", got)
	}
	mode.Store(0)
	if got := call(byName["ping"].Invoke); got != "" {
		t.Errorf("served again = %q, want no error", got)
	}
	_ = sess.Close()
	if got := call(byName["ping"].Invoke); got != "mcp_transport" {
		t.Errorf("closed session = %q, want mcp_transport", got)
	}
}
