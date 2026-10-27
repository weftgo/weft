//go:build ignore

// g10bdemo.go — the phase 10b gate's in-gate Go program (gate10b.sh
// copies it under $RUN/demo, builds it against the tree through a
// scratch go.work, and never builds it in the repo: the ignore tag).
// Offline: every model is a wefttest script, the MCP servers are the
// go-sdk's in-memory pipes, and the only network is the loopback
// Studio named on the command line.
//
//	g10bdemo flow  <studio> <token> <batch>  three runs under scope.WithFlow
//	                                         "f_checkout", two under "f_search",
//	                                         each tagged g10b.batch=<batch>
//	g10bdemo mcp   <studio> <token>          a run calling three MCP tools of
//	                                         mcp.Server("billing"): an isError
//	                                         answer, a JSON-RPC refusal, a closed
//	                                         session (transport)
//	g10bdemo onion <studio> <token>          a governance-shaped agent (a scrub
//	                                         closure + mw.Allow over its tools,
//	                                         mw.Retry + mw.RepairJSON over its
//	                                         model) registered through
//	                                         runtime.Install, so Studio stores its
//	                                         manifest, then one run of it
//	g10bdemo live  <studio> <token> <secs>   a run whose tool sleeps <secs>: a
//	                                         row for /live
//
// Each prints one JSON line per run: {"mode","run","flow"?,"agent"}.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft"
	"github.com/weftgo/weft/mcp"
	"github.com/weftgo/weft/mw"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/runtime"
	"github.com/weftgo/weft/scope"
	"github.com/weftgo/weft/wefttest"
)

func say(mode, run, agent, flow string) {
	b, _ := json.Marshal(map[string]string{"mode": mode, "run": run, "agent": agent, "flow": flow})
	fmt.Println(string(b))
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "g10bdemo: "+format+"\n", a...)
	os.Exit(1)
}

type idIn struct {
	ID string `json:"id"`
}

func main() {
	if len(os.Args) < 4 {
		die("usage: g10bdemo flow|mcp|onion|live <studio> <token> [batch|secs]")
	}
	mode, url, tok := os.Args[1], os.Args[2], os.Args[3]
	stop := otel.Install(otel.Studio(url, tok), otel.NoEnv())
	ctx := context.Background()
	switch mode {
	case "flow":
		batch := "g10b-flow"
		if len(os.Args) > 4 {
			batch = os.Args[4]
		}
		for i, flow := range []string{"f_checkout", "f_checkout", "f_search", "f_checkout", "f_search"} {
			a := weft.New(wefttest.Script(wefttest.Say(fmt.Sprintf("flow %s, run %d", flow, i))), weft.Name("flow-desk"))
			res, err := a.Generate(scope.WithFlow(ctx, flow), weft.Prompt(fmt.Sprintf("%s request %d", flow, i)),
				weft.Metadata(map[string]string{"g10b.batch": batch}))
			if err != nil {
				die("flow run %d: %v", i, err)
			}
			say(mode, res.ID, "flow-desk", flow)
		}
	case "mcp":
		mcpRun(ctx)
	case "onion":
		onionRun(ctx, url, tok)
	case "live":
		secs := 20
		if len(os.Args) > 4 {
			if n, err := strconv.Atoi(os.Args[4]); err == nil {
				secs = n
			}
		}
		slow := weft.Tool("wait_for_stock", "Wait for the stock count.",
			func(ctx context.Context, in idIn) (string, error) {
				time.Sleep(time.Duration(secs) * time.Second)
				return "42 units", nil
			})
		a := weft.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "wait_for_stock", Args: `{"id":"w1"}`}),
			wefttest.Say("42 in stock.")), weft.Name("live-desk"), slow)
		res, err := a.Generate(ctx, weft.Prompt("how many widgets?"))
		if err != nil {
			die("live run: %v", err)
		}
		say(mode, res.ID, "live-desk", "")
	default:
		die("unknown mode %q", mode)
	}
	time.Sleep(2 * time.Second) // the exporter's last batch
	stop()
}

// serve runs an in-memory MCP server and returns a client session on it.
func serve(ctx context.Context, name string, add func(*sdk.Server)) *sdk.ClientSession {
	srv := sdk.NewServer(&sdk.Implementation{Name: name, Version: "0"}, nil)
	add(srv)
	st, ct := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, st) }()
	sess, err := sdk.NewClient(&sdk.Implementation{Name: "g10bdemo", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		die("mcp connect %s: %v", name, err)
	}
	return sess
}

var schema = json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)

func mcpRun(ctx context.Context) {
	sess := serve(ctx, "billing-srv", func(s *sdk.Server) {
		// isError: the server answered, its tool failed (mcp_tool_error).
		s.AddTool(&sdk.Tool{Name: "get_invoice", Description: "Fetch an invoice.", InputSchema: schema,
			Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "invoice INV-9 is archived"}}}, nil
			})
		// A raw handler's error: a JSON-RPC error answer (mcp_rpc_error).
		s.AddTool(&sdk.Tool{Name: "charge_card", Description: "Charge a card.", InputSchema: schema},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return nil, errors.New("card processor refused the charge")
			})
	})
	tools, err := mcp.Tools(ctx, sess, mcp.Server("billing"), mcp.Transport("memory"))
	if err != nil {
		die("mcp import: %v", err)
	}
	// A second session on the same named server, closed before the call:
	// no answer (mcp_transport).
	gone := serve(ctx, "billing-srv-2", func(s *sdk.Server) {
		s.AddTool(&sdk.Tool{Name: "archive_statement", Description: "Archive a statement.", InputSchema: schema,
			Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}},
			func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "archived"}}}, nil
			})
	})
	more, err := mcp.Tools(ctx, gone, mcp.Server("billing"), mcp.Transport("memory"),
		mcp.Policy(weft.Timeout(3*time.Second)))
	if err != nil {
		die("mcp import 2: %v", err)
	}
	_ = gone.Close()
	opts := []weft.Option{weft.Name("billing-desk")}
	for _, td := range append(tools, more...) {
		opts = append(opts, td)
	}
	a := weft.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "get_invoice", Args: `{"id":"INV-9"}`},
			wefttest.Call{Name: "charge_card", Args: `{"id":"c-1"}`},
			wefttest.Call{Name: "archive_statement", Args: `{"id":"s-3"}`}),
		wefttest.Say("Billing is having trouble: one tool error, one refusal, one server unreachable.")), opts...)
	res, err := a.Generate(ctx, weft.Prompt("Settle invoice INV-9."))
	if err != nil {
		die("mcp run: %v", err)
	}
	say("mcp", res.ID, "billing-desk", "")
}

func onionRun(ctx context.Context, url, tok string) {
	// The arena's mw.governance-patterns closures, plus the named layers.
	scrub := func(next weft.ToolCaller) weft.ToolCaller {
		return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
			out, err := next(ctx, call)
			return strings.ReplaceAll(out, "4111-1111-1111-1111", "****-****-****-****"), err
		}
	}
	fetch := weft.Tool("fetch_record", "Fetch a customer record.",
		func(_ context.Context, in idIn) (string, error) { return "card 4111-1111-1111-1111 exp 04/28", nil })
	del := weft.Tool("delete_record", "Delete a customer record.",
		func(_ context.Context, in idIn) (string, error) { return "deleted " + in.ID, nil })
	agent := weft.New(wefttest.Script(
		wefttest.Fail(fmt.Errorf("upstream: %w", weft.ErrStreamIdle)),
		wefttest.ToolCalls(
			wefttest.Call{Name: "fetch_record", Args: `{"id":"42`},
			wefttest.Call{Name: "delete_record", Args: `{"id":"42"}`}),
		wefttest.Say("Record 42 shown masked; deleting it is not allowed.")),
		weft.Name("gov-desk"),
		weft.WrapModel(mw.Retry(mw.BaseDelay(20*time.Millisecond)), mw.RepairJSON()),
		weft.WrapTools(scrub, mw.Allow(func(c weft.Call) bool { return c.Name != "delete_record" })),
		fetch, del)
	stopLink := runtime.Install(runtime.Studio(url, tok), runtime.Agents(agent), runtime.Enabled(true))
	defer stopLink()
	time.Sleep(3 * time.Second) // the registration
	res, err := agent.Generate(ctx, weft.Prompt("Show record 42, then delete it."))
	if err != nil {
		die("onion run: %v", err)
	}
	say("onion", res.ID, "gov-desk", "")
}
