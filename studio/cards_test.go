package studio

// The run cards' fixture (U25): each named seeded run shape the item's
// Done line lists — loop.max-tokens-tools, out.invalid-repaired,
// mw.retry, tools.timeout, appr.park and two breakpoint parks,
// runtimelab.option-lab, msg.parts, sub.failed, opt.prepare-step, and
// U23.2's mw.repairjson, mw.allow and rw.mcp-mesh —
// recorded through the real pipeline (core → otel → OTLP ingest →
// obsdb) and read back through the routes both renderers read: the
// run document, events, transcript, spans, requests and every step.
// One golden, studio/testdata/api/cards.golden.json, normalised to be
// byte-stable; the web tests (src/test/cards-fixture.ts) draw each
// shape on the run page and in the panel's Story and assert the named
// fact. The Go half pins what the record says.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/mcp"
)

// cardStatusErr is a provider error carrying an HTTP status the way the
// vendor SDKs' errors do (mw.HTTPStatus reads StatusCode).
type cardStatusErr struct{ StatusCode int }

func (e cardStatusErr) Error() string {
	return fmt.Sprintf("%d Service Unavailable: overloaded", e.StatusCode)
}

// cardWireModel declares a thinking wire form (an adapter's
// ThinkingWire, O16).
type cardWireModel struct{ core.Model }

func (cardWireModel) ThinkingWire(core.ThinkingConfig) string { return "reasoning_effort" }

// cardShapes are the shapes in the golden, by the arena scenario they
// stand for, with their run ids.
var cardShapes = []struct{ name, run string }{
	{"loop.max-tokens-tools", "r_maxtok"},
	{"out.invalid-repaired", "r_output"},
	{"out.no-output", "r_nooutput"},
	{"mw.retry", "r_retry"},
	{"tools.timeout", "r_timeout"},
	{"appr.park", "r_park"},
	{"appr.breakpoint-own", "r_bp_own"},
	{"appr.breakpoint-playground", "r_bp_pg"},
	{"runtimelab.option-lab", "r_lab"},
	{"msg.parts", "r_parts"},
	{"sub.failed", "r_subfail"},
	{"opt.prepare-step", "r_prepare"},
	// U23.2: the onion shown and MCP identity.
	{"mw.repairjson", "r_repair"},
	{"mw.allow", "r_allow"},
	{"rw.mcp-mesh", "r_mesh"},
	// U30.3: an exhausted Retry's give-up (UX-S2-02) and a step's
	// Parallelism dial beside a Sequential barrier (UX-S2-07).
	{"mw.retry-giveup", "r_giveup"},
	{"conc.parallelism", "r_par"},
}

// recordCardRuns records every shape. skew forces conc.parallelism's
// other completion order (a, b, c: c returns only after b has), so
// TestRunCardsGoldenSkewed proves the golden does not depend on how the
// parallel calls interleave.
func recordCardRuns(t *testing.T, url string, skew bool) {
	t.Helper()
	ctx := context.Background()
	type echoIn struct {
		Msg string `json:"msg"`
	}
	type verdict struct {
		Approved bool   `json:"approved"`
		Reason   string `json:"reason"`
	}
	type orderIn struct {
		OrderID string `json:"order_id"`
	}
	withPipeline(t, url, true, func(prov []core.Option) {
		agent := func(name string, m core.Model, opts ...core.Option) *core.Agent {
			return core.New(m, append(append([]core.Option{core.Name(name)}, prov...), opts...)...)
		}
		run := func(a *core.Agent, id string, opts ...core.RunOption) {
			t.Helper()
			if _, err := a.Generate(ctx, append([]core.RunOption{core.RunID(id)}, opts...)...); err != nil {
				t.Fatalf("%s: %v", id, err)
			}
		}
		echo := core.Tool("echo", "Echo.", func(_ context.Context, in echoIn) (string, error) { return in.Msg, nil })
		lookup := core.Tool("lookup_order", "Look up an order.", func(_ context.Context, in orderIn) (string, error) {
			return "order " + in.OrderID + " shipped", nil
		})

		// loop.max-tokens-tools: the cut call, its truncated args.
		run(agent("echoer", wefttest.Script(
			wefttest.Raw(
				core.ModelToolCall{ID: "t1", Name: "echo", Args: json.RawMessage(`{"msg":"never`)},
				core.ModelFinish{Reason: core.StopMaxTokens, Usage: core.Usage{InputTokens: 12, OutputTokens: 4}},
			),
			wefttest.ToolCalls(wefttest.Call{ID: "t1", Name: "echo", Args: `{"msg":"retry"}`}),
			wefttest.Say("done")), echo), "r_maxtok", core.Prompt("echo something"))

		// out.invalid-repaired / out.no-output.
		run(agent("reviewer", wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{ID: "o1", Name: "submit_output", Args: `{"approved":"yes please","reason":"x"}`}),
			wefttest.ToolCalls(wefttest.Call{ID: "o2", Name: "submit_output", Args: `{"approved":true,"reason":"fixed"}`}),
		), core.Output[verdict]()), "r_output", core.Prompt("review"))
		run(agent("reviewer", wefttest.Script(wefttest.Say("looks fine to me")), core.Output[verdict]()),
			"r_nooutput", core.Prompt("review"))

		// mw.retry: two 503s, then an answer.
		run(agent("support", wefttest.Script(
			wefttest.Fail(cardStatusErr{503}), wefttest.Fail(cardStatusErr{503}), wefttest.Say("hello after two retries")),
			core.WrapModel(mw.Retry(mw.MaxRetries(3), mw.BaseDelay(time.Millisecond)))), "r_retry", core.Prompt("hi"))

		// mw.retry-giveup: two 429s under MaxRetries(1) — the run fails,
		// the last attempt giving up on its budget.
		if _, err := agent("support", wefttest.Script(
			wefttest.Fail(cardStatusErr{429}), wefttest.Fail(cardStatusErr{429})),
			core.WrapModel(mw.Retry(mw.MaxRetries(1), mw.BaseDelay(time.Millisecond)))).Generate(ctx,
			core.RunID("r_giveup"), core.Prompt("hi")); err == nil {
			t.Fatal("r_giveup: the run succeeded; want the provider's 429")
		}

		// conc.parallelism: three calls under Parallelism(2), then a
		// Sequential barrier tool. The handlers return in the order a,
		// c, b (skew: a, b, c): c starts once a returned, b returns once
		// c closed its signal, each 20 ms after it. How the events
		// interleave still moves with the scheduler, so the golden puts
		// a parallel step's calls in call order (normParallel).
		aDone, bDone, cDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
		nap := func(name string, wait <-chan struct{}, done chan struct{}, after <-chan struct{}, opts ...core.ToolOption) *core.ToolDef {
			return core.Tool(name, "Nap.", func(context.Context, struct{}) (string, error) {
				if wait != nil {
					<-wait
					time.Sleep(20 * time.Millisecond)
				}
				if done != nil {
					close(done)
				}
				if after != nil {
					<-after
					time.Sleep(20 * time.Millisecond)
				}
				return name + " done", nil
			}, opts...)
		}
		var cAfter <-chan struct{}
		if skew {
			cAfter = bDone
		}
		run(agent("fanout", wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{ID: "p1", Name: "a"}, wefttest.Call{ID: "p2", Name: "b"},
				wefttest.Call{ID: "p3", Name: "c"}, wefttest.Call{ID: "p4", Name: "barrier"}),
			wefttest.Say("all done")),
			core.Parallelism(2), nap("a", nil, aDone, nil), nap("b", cDone, bDone, nil),
			nap("c", aDone, cDone, cAfter), nap("barrier", nil, nil, nil, core.Sequential())),
			"r_par", core.Prompt("fan out"))

		// tools.timeout: a tool cut at its 50 ms limit.
		slow := core.Tool("slow", "Slow.", func(c context.Context, _ struct{}) (string, error) {
			<-c.Done()
			return "", c.Err()
		}, core.Timeout(50*time.Millisecond))
		run(agent("support", wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{ID: "s1", Name: "slow"}), wefttest.Say("it timed out")), slow),
			"r_timeout", core.Prompt("go slow"))

		// appr.park: an approval gate; then the two breakpoint parks —
		// the app's own run through a ParkSource (runtime.Breakpoints()
		// is one), and a playground run's ParkOn named as breakpoints.
		deploy := core.Tool("deploy", "Deploy.", func(context.Context, struct{}) (string, error) { return "deployed", nil },
			core.RequireApproval())
		run(agent("ops", wefttest.Script(wefttest.ToolCalls(wefttest.Call{ID: "d1", Name: "deploy"})), deploy),
			"r_park", core.Prompt("deploy it"))
		status := core.Tool("status", "Status.", func(context.Context, struct{}) (string, error) { return "green", nil })
		run(agent("ops", wefttest.Script(wefttest.ToolCalls(wefttest.Call{ID: "b1", Name: "status"})), status,
			core.ParkSource(func(context.Context) []string { return []string{"status"} })),
			"r_bp_own", core.Prompt("status?"))
		run(agent("ops", wefttest.Script(wefttest.ToolCalls(wefttest.Call{ID: "b2", Name: "status"})), status),
			"r_bp_pg", core.Prompt("status?"), core.ParkOn("status"),
			core.Metadata(map[string]string{"weft.playground": "true", "weft.playground.breakpoints": "status"}))

		// runtimelab.option-lab: params, tool choice, park_on, only_tools
		// and the model overridden for one run.
		temp, topP, maxTok, seed := 0.4, 0.9, 64, int64(7)
		alt := namedModel{wefttest.Script(wefttest.Say("from alt")), "alt-model"}
		run(agent("orders", namedModel{wefttest.Script(wefttest.Say("ok")), "orders-model"}, lookup, echo),
			"r_lab", core.Prompt("where is 42?"),
			core.Params(core.RequestParams{Temperature: &temp, TopP: &topP, MaxTokens: &maxTok, Stop: []string{"END"}, Seed: &seed}),
			core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceNone}),
			core.ParkOn("echo"), core.OnlyTools("lookup_order", "echo"), core.UseModel(alt),
			core.Metadata(map[string]string{"weft.playground": "true"}))

		// msg.parts: a user message with a file part and a file URL.
		run(agent("vision", wefttest.Script(wefttest.Say("a receipt"))), "r_parts",
			core.Messages(core.UserParts(
				core.TextPart{Text: "What is in this image? "},
				core.FilePart{MediaType: "text/plain", Data: []byte("a tiny receipt")},
				core.FilePart{MediaType: "image/png", URL: "https://example.com/pic.png"},
			)))

		// sub.failed: the child's model fails; the parent reads it.
		child := agent("worker", wefttest.Script(wefttest.Fail(errors.New("child model outage"))))
		run(agent("lead", wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{ID: "w1", Name: "work", Args: `{"prompt":"do the work"}`}),
			wefttest.Say("worker failed, handling it myself")),
			core.Subagent("work", "Delegate.", child, core.Timeout(2*time.Second))), "r_subfail", core.Prompt("delegate"))

		// opt.prepare-step: step 0 forced to a named tool, its system
		// text and params rewritten; step 1's system text rewritten.
		ping := core.Tool("ping", "Ping.", func(context.Context, struct{}) (string, error) { return "pong", nil })
		cold := 0.0
		run(agent("classifier", cardWireModel{wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{ID: "p1", Name: "ping"}), wefttest.Say("done"))}, ping,
			core.Instructions("baseline system"),
			core.Thinking(core.ThinkingConfig{Level: core.ThinkLow}),
			core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
				if step == 0 {
					req.System = "baseline system; step 0 must classify"
					req.ToolChoice = core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "ping"}
					req.Params.Temperature = &cold
				} else {
					req.System = "baseline system; wrap up"
				}
				return req, nil
			})), "r_prepare", core.Prompt("x"))

		// mw.repairjson (U23.2): the model's arguments arrive fenced and
		// mw.RepairJSON strips the fence — the call's args_repaired, the
		// chat span's weft.repair event.
		run(agent("echoer", mw.RepairJSON()(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{ID: "f1", Name: "echo", Args: "```json\n{\"msg\":\"fenced\"}\n```"}),
			wefttest.Say("ok"))), echo), "r_repair", core.Prompt("x"))

		// mw.allow (U23.2): the ops shell may check disk usage but never
		// delete — the denied call never runs (weft.tool.denied_by allow).
		type pathIn struct {
			Path string `json:"path"`
		}
		rm := core.Tool("delete_path", "rm -rf a path on the host.", func(_ context.Context, in pathIn) (string, error) {
			return "deleted " + in.Path, nil
		})
		df := core.Tool("disk_usage", "df -h for a mount.", func(_ context.Context, in pathIn) (string, error) {
			return in.Path + ": 91% used", nil
		})
		run(agent("ops-shell", wefttest.Script(
			wefttest.ToolCalls(
				wefttest.Call{ID: "a1", Name: "delete_path", Args: `{"path":"/var/lib/postgresql"}`},
				wefttest.Call{ID: "a2", Name: "disk_usage", Args: `{"path":"/var"}`}),
			wefttest.Say("I may not delete data; /var is 91% full.")),
			core.WrapTools(mw.Allow(func(c core.Call) bool { return c.Name != "delete_path" })), rm, df,
			core.Parallelism(1)), // one order in the golden
			"r_allow", core.Prompt("/var is full, free some space."))

		// rw.mcp-mesh (U23.2, O8): a front desk imports a billing
		// server's tools under mcp.Server("billing") beside a local tool;
		// the ledger answers isError, the statement export fails the
		// exchange, and the listing's odd tool is skipped.
		mesh := meshTools(t, ctx)
		profile := core.Tool("customer_profile", "Profile of a customer.", func(_ context.Context, in orderIn) (string, error) {
			return "customer " + in.OrderID + ": Acme SARL", nil
		})
		opts := []core.Option{profile, core.Parallelism(1)}
		for _, td := range mesh {
			opts = append(opts, td)
		}
		run(agent("front-desk", wefttest.Script(
			wefttest.ToolCalls(
				wefttest.Call{ID: "m1", Name: "customer_profile", Args: `{"order_id":"42"}`},
				wefttest.Call{ID: "m2", Name: "svc_get_invoice", Args: `{"id":"INV-9"}`},
				wefttest.Call{ID: "m3", Name: "svc_legacy_ledger", Args: `{"id":"42"}`},
				wefttest.Call{ID: "m4", Name: "svc_export_statement", Args: `{"id":"42"}`},
				wefttest.Call{ID: "m5", Name: "svc_archive_statement", Args: `{"id":"42"}`}),
			wefttest.Say("INV-9 is unpaid; the ledger and the export are down.")), opts...),
			"r_mesh", core.Prompt("Customer 42 asks about invoice INV-9."))
	})
}

// meshTools serves rw.mcp-mesh's billing server on an in-memory MCP
// transport and imports its tools as mcp:billing.
func meshTools(t *testing.T, ctx context.Context) []*core.ToolDef {
	t.Helper()
	schema := json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)
	ok := func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "INV-9: 120 EUR, unpaid"}}}, nil
	}
	srv := sdk.NewServer(&sdk.Implementation{Name: "billing-service", Version: "1"}, nil)
	srv.AddTool(&sdk.Tool{Name: "get_invoice", Description: "Status of an invoice.", InputSchema: schema}, ok)
	srv.AddTool(&sdk.Tool{Name: "legacy_ledger", Description: "Ledger balance (legacy).", InputSchema: schema},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: "ledger offline: maintenance window"}}}, nil
		})
	srv.AddTool(&sdk.Tool{Name: "export_statement", Description: "Export a statement.", InputSchema: schema},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return nil, errors.New("statement service unreachable")
		})
	odd := &sdk.Tool{Name: "odd_report", Description: "Odd.", InputSchema: json.RawMessage(`{"type":"object"}`)}
	srv.AddTool(odd, ok)
	odd.InputSchema = json.RawMessage(`{"type":["object","null"]}`)
	st, ct := sdk.NewInMemoryTransports()
	runCtx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	go func() { _ = srv.Run(runCtx, st) }()
	sess, err := sdk.NewClient(&sdk.Implementation{Name: "front-desk", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	tools, err := mcp.Tools(ctx, sess, mcp.Server("billing"), mcp.Transport("memory"), mcp.Prefix("svc_"))
	var ie *mcp.ImportError
	if !errors.As(err, &ie) || len(tools) != 3 {
		t.Fatalf("mesh import: %d tools, %v", len(tools), err)
	}
	// The billing archive: a second session to the billing service,
	// closed before the run — its call never completes (mcp_transport).
	archive := sdk.NewServer(&sdk.Implementation{Name: "billing-archive", Version: "1"}, nil)
	archive.AddTool(&sdk.Tool{Name: "archive_statement", Description: "Archive a statement.", InputSchema: schema}, ok)
	ast, act := sdk.NewInMemoryTransports()
	go func() { _ = archive.Run(runCtx, ast) }()
	asess, err := sdk.NewClient(&sdk.Implementation{Name: "front-desk", Version: "1"}, nil).Connect(ctx, act, nil)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := mcp.Tools(ctx, asess, mcp.Server("billing"), mcp.Transport("memory"), mcp.Prefix("svc_"))
	if err != nil || len(archived) != 1 {
		t.Fatalf("archive import: %d tools, %v", len(archived), err)
	}
	_ = asess.Close()
	return append(tools, archived...)
}

// cardNorm makes the golden byte-stable: times, ids the pipeline mints,
// measured milliseconds and sizes that move with the clock.
var cardNorm = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`"\d{4}-\d\d-\d\dT[0-9:.]+(Z|[+-]\d\d:\d\d)"`), `"2026-10-01T09:00:00Z"`},
	{regexp.MustCompile(`"(latency_ms|ttft_ms|duration_ms|weft\.ttft_ms|weft\.latency_ms|weft\.attempt\.wait_ms|wait_ms)": ?"?[0-9]+"?`), `"$1": 1`},
	// weft.tool.queue_ms is on the span only when a call queued (a
	// timing fact): its presence moves with the scheduler, so the key is
	// dropped whole, then the comma it leaves before a closing bracket.
	{regexp.MustCompile(`(?m)^\s*"weft\.tool\.queue_ms": ?"?[0-9]+"?,?\n`), ``},
	{regexp.MustCompile(`,(\n\s*[}\]])`), `$1`},
	{regexp.MustCompile(`"(retry_after_ms)": ?[0-9]+`), `"$1": 1`},
	{regexp.MustCompile(`"(weft\.version|weft_version|service)": ?"[^"]*"`), `"$1": "(test)"`},
	{regexp.MustCompile(`"(weft\.manifest\.hash|manifest_hash)": ?"[0-9a-f]{64}"`), `"$1": "(manifest)"`},
	{regexp.MustCompile(`"(size)": ?[0-9]+`), `"$1": 0`},
}

// idRe is a span or trace id the pipeline mints: renumbered in
// first-seen order, its length kept.
var idRe = regexp.MustCompile(`"([0-9a-f]{16}|[0-9a-f]{32})"`)

func normCards(s string) string {
	for _, n := range cardNorm {
		s = n.re.ReplaceAllString(s, n.with)
	}
	ids := map[string]string{}
	return idRe.ReplaceAllStringFunc(s, func(m string) string {
		id := m[1 : len(m)-1]
		if v, ok := ids[id]; ok {
			return v
		}
		n := len(ids) + 1
		var v string
		if len(id) == 16 {
			v = fmt.Sprintf(`"5ba0%012x"`, n)
		} else {
			v = fmt.Sprintf(`"7ace%028x"`, n)
		}
		ids[id] = v
		return v
	})
}

// shapeDoc is one shape in the golden: the routes it is read through.
type shapeDoc struct {
	Run        json.RawMessage            `json:"run"`
	Events     json.RawMessage            `json:"events"`
	Transcript json.RawMessage            `json:"transcript"`
	Spans      json.RawMessage            `json:"spans"`
	Requests   json.RawMessage            `json:"requests"`
	Tools      json.RawMessage            `json:"tools"`
	Steps      []json.RawMessage          `json:"steps"`
	Children   map[string]json.RawMessage `json:"children,omitempty"`
}

// TestRunCardsGolden records the shapes and writes the golden the web
// tests read; it pins the facts on the Go side as it goes.
func TestRunCardsGolden(t *testing.T) { runCardsGolden(t, false) }

// TestRunCardsGoldenSkewed records conc.parallelism with its calls
// returning in the other order and must match the same golden: the
// interleaving of parallel calls is not a fact the golden pins.
func TestRunCardsGoldenSkewed(t *testing.T) { runCardsGolden(t, true) }

func runCardsGolden(t *testing.T, skew bool) {
	t.Setenv("WEFT_MODEL_REQUESTS", "deny")
	ts, _ := requestsServer(t)
	recordCardRuns(t, ts.URL, skew)
	finished := func(b string) bool {
		return strings.Contains(b, `"status":"succeeded"`) || strings.Contains(b, `"status":"failed"`)
	}

	out := map[string]shapeDoc{}
	read := func(id string) shapeDoc {
		run := fetchJSON(t, ts, "/api/runs/"+id, finished)
		var row struct {
			Steps    int `json:"steps"`
			Children []struct {
				ID string `json:"id"`
			} `json:"children"`
		}
		decode(t, run, &row)
		d := shapeDoc{
			Run:        json.RawMessage(run),
			Events:     json.RawMessage(fetchJSON(t, ts, "/api/runs/"+id+"/events?after=0&limit=500", nil)),
			Transcript: json.RawMessage(fetchJSON(t, ts, "/api/runs/"+id+"/transcript", nil)),
			Spans:      json.RawMessage(fetchJSON(t, ts, "/api/runs/"+id+"/spans", nil)),
			Requests:   json.RawMessage(fetchJSON(t, ts, "/api/runs/"+id+"/requests?limit=1000", nil)),
			Tools:      json.RawMessage(fetchJSON(t, ts, "/api/runs/"+id+"/tools", nil)),
		}
		// A run that failed in its first model call counts no step;
		// its step 0 still holds the attempts.
		for n := 0; n < max(row.Steps, 1); n++ {
			d.Steps = append(d.Steps, json.RawMessage(fetchJSON(t, ts, fmt.Sprintf("/api/runs/%s/steps/%d", id, n), nil)))
		}
		for _, c := range row.Children {
			if d.Children == nil {
				d.Children = map[string]json.RawMessage{}
			}
			child := fetchJSON(t, ts, "/api/runs/"+c.ID, func(b string) bool { return strings.Contains(b, `"status":"failed"`) })
			d.Children[c.ID] = json.RawMessage(child)
		}
		return d
	}
	for _, s := range cardShapes {
		// A step whose calls ran in parallel: its events, seqs, step
		// rows and spans in call order (normParallel); every other run
		// keeps its exact order.
		out[s.name] = normParallel(t, s.name, read(s.run))
	}

	// The Go half of the facts, on what the routes serve.
	spansOf := func(name string) []map[string]any {
		var doc struct {
			Spans []struct {
				Name          string         `json:"name"`
				StatusMessage string         `json:"status_message"`
				Attrs         map[string]any `json:"attrs"`
			} `json:"spans"`
		}
		decode(t, string(out[name].Spans), &doc)
		var all []map[string]any
		for _, sp := range doc.Spans {
			a := sp.Attrs
			a["_name"], a["_status_message"] = sp.Name, sp.StatusMessage
			all = append(all, a)
		}
		return all
	}
	attr := func(name, span, key string) []string {
		var got []string
		for _, a := range spansOf(name) {
			if n, _ := a["_name"].(string); strings.HasPrefix(n, span) {
				if v, ok := a[key]; ok {
					got = append(got, fmt.Sprint(v))
				}
			}
		}
		sort.Strings(got)
		return got
	}
	for _, c := range []struct {
		shape, span, key string
		want             []string
	}{
		{"mw.retry", "attempt", "http.response.status_code", []string{"503", "503"}},
		{"tools.timeout", "execute_tool", "weft.tool.timeout_ms", []string{"50"}},
		{"tools.timeout", "execute_tool", "error.type", []string{"timeout"}},
		{"appr.park", "execute_tool", "weft.tool.pending.source", []string{"approval"}},
		{"appr.breakpoint-own", "execute_tool", "weft.tool.pending.source", []string{"park_source"}},
		{"appr.breakpoint-playground", "execute_tool", "weft.tool.pending.source", []string{"park_on"}},
		{"out.invalid-repaired", "invoke_agent", "weft.output.submitted", []string{"true"}},
		{"out.no-output", "invoke_agent", "weft.output.submitted", []string{"false"}},
		{"sub.failed", "execute_tool", "error.type", []string{"SUBAGENT_FAILED"}},
		{"runtimelab.option-lab", "invoke_agent", "weft.override.params", []string{`{"temperature":0.4,"top_p":0.9,"max_tokens":64,"stop":["END"],"seed":7}`}},
		{"mw.repairjson", "execute_tool", "weft.tool.args_repaired", []string{"true"}},
		{"mw.allow", "execute_tool", "weft.tool.denied_by", []string{"allow"}},
		{"rw.mcp-mesh", "execute_tool", "weft.tool.origin", []string{"mcp:billing", "mcp:billing", "mcp:billing", "mcp:billing"}},
		{"rw.mcp-mesh", "execute_tool", "error.type", []string{"mcp_rpc_error", "mcp_tool_error", "mcp_transport"}},
		{"mw.retry", "attempt", "error.type", []string{"overloaded", "overloaded"}},
		{"mw.retry-giveup", "attempt", "error.type", []string{"rate_limit", "rate_limit"}},
		{"mw.retry-giveup", "attempt", "weft.attempt.giveup_reason", []string{"budget"}},
		{"out.invalid-repaired", "invoke_agent", "weft.output.type", []string{"studio.verdict"}},
	} {
		if got := attr(c.shape, c.span, c.key); strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s %s %s = %q, want %q", c.shape, c.span, c.key, got, c.want)
		}
	}
	if !strings.Contains(string(out["opt.prepare-step"].Requests), `\"prepared\":[\"system\",\"tool_choice\",\"params\"]`) &&
		!strings.Contains(string(out["opt.prepare-step"].Requests), `"prepared":["system","tool_choice","params"]`) {
		t.Errorf("opt.prepare-step step 0 request carries no prepared [system tool_choice params]:\n%s", out["opt.prepare-step"].Requests)
	}
	if !strings.Contains(string(out["opt.prepare-step"].Requests), `"wire":"reasoning_effort"`) {
		t.Errorf("opt.prepare-step request carries no thinking.wire")
	}
	if !strings.Contains(string(out["mw.repairjson"].Spans), `"weft.repair"`) {
		t.Errorf("mw.repairjson's chat span carries no weft.repair event")
	}
	if tools := string(out["rw.mcp-mesh"].Tools); !strings.Contains(tools, `"source_detail"`) ||
		!strings.Contains(tools, `"skipped"`) || !strings.Contains(tools, `"odd_report"`) {
		t.Errorf("rw.mcp-mesh's catalog lacks source_detail or skipped[]:\n%s", tools)
	}
	if !strings.Contains(string(out["mw.retry"].Steps[0]), `"http_status":503`) ||
		!strings.Contains(string(out["mw.retry"].Steps[0]), `"wait_ms"`) ||
		!strings.Contains(string(out["mw.retry"].Steps[0]), `overloaded`) {
		t.Errorf("mw.retry step 0 attempts lack status, wait or cause:\n%s", out["mw.retry"].Steps[0])
	}

	if !strings.Contains(string(out["mw.retry-giveup"].Steps[0]), `"giveup_reason":"budget"`) {
		t.Errorf("mw.retry-giveup step 0 attempts lack the give-up:\n%s", out["mw.retry-giveup"].Steps[0])
	}
	if !strings.Contains(string(out["conc.parallelism"].Requests), `"parallelism":2`) {
		t.Errorf("conc.parallelism's request records lack parallelism 2:\n%s", out["conc.parallelism"].Requests)
	}

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	stable := normCards(pretty(t, string(b)))
	wefttest.Golden(t, "testdata/api/cards.golden.json", []byte(stable))
}

// normParallel makes a shape's record independent of how its parallel
// calls interleaved. A step runs its calls concurrently when its
// request says so (parallelism other than 1, sequential_tools off) and
// it made two or more: their tool events interleave as the scheduler
// runs them, and the run's pos and seq numbers, the step's tool rows
// and the spans route's tie order (by span id) follow. The golden
// takes such a step's call events grouped in call order — each call's
// own events in the order they came — on the same pos and seq slots,
// and the seq a call's row and execute_tool span carry moves with its
// tool_start. Before that it checks what the scheduler does guarantee
// (checkParallel), so a real ordering bug still fails. A run with no
// such step is returned as read: a sequential run keeps its exact
// order.
func normParallel(t *testing.T, shape string, d shapeDoc) shapeDoc {
	t.Helper()
	var reqs struct {
		Requests []struct {
			Step int `json:"step"`
			Body struct {
				SequentialTools bool `json:"sequential_tools"`
				Parallelism     int  `json:"parallelism"`
			} `json:"body"`
			Tools struct {
				Tools []struct {
					Name       string `json:"name"`
					Sequential bool   `json:"sequential"`
				} `json:"tools"`
			} `json:"tools"`
		} `json:"requests"`
	}
	decode(t, string(d.Requests), &reqs)
	limit := map[int]int{}               // a step that may run calls at once → its parallelism
	barrier := map[int]map[string]bool{} // its Sequential tools
	for _, r := range reqs.Requests {
		if r.Body.Parallelism == 1 || r.Body.SequentialTools {
			continue
		}
		limit[r.Step] = r.Body.Parallelism
		barrier[r.Step] = map[string]bool{}
		for _, td := range r.Tools.Tools {
			barrier[r.Step][td.Name] = td.Sequential
		}
	}
	var tr struct {
		Batches []struct {
			Step     int `json:"step"`
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content"`
			} `json:"messages"`
		} `json:"batches"`
	}
	decode(t, string(d.Transcript), &tr)
	calls := map[int][]parCall{} // step → its calls, in call order
	for _, b := range tr.Batches {
		if _, ok := limit[b.Step]; !ok {
			continue
		}
		for _, m := range b.Messages {
			for _, p := range m.Content {
				if m.Role == "assistant" && p.Type == "tool_call" {
					calls[b.Step] = append(calls[b.Step], parCall{p.ID, barrier[b.Step][p.Name]})
				}
			}
		}
	}
	for st, cs := range calls {
		if len(cs) < 2 {
			delete(calls, st)
		}
	}
	if len(calls) == 0 {
		return d
	}

	// The events: each parallel step's call events, regrouped.
	var evDoc map[string]json.RawMessage
	decode(t, string(d.Events), &evDoc)
	var entries []json.RawMessage
	decode(t, string(evDoc["events"]), &entries)
	heads := make([]evHead, len(entries))
	window := map[int][]int{} // step → indexes of its call events
	step := -1
	for i, e := range entries {
		decode(t, string(e), &heads[i])
		ev := heads[i].Event
		if ev.Type == "step_start" {
			step = ev.Index
		}
		if _, ok := calls[step]; ok && ev.CallID != "" {
			window[step] = append(window[step], i)
		}
	}
	posMap, seqMap := map[int64]int64{}, map[int64]int64{}
	from := make([]int, len(entries)) // the entry each slot takes
	for i := range from {
		from[i] = i
	}
	for st, idx := range window {
		checkParallel(t, shape, st, heads, idx, calls[st], limit[st])
		rank := map[string]int{}
		for r, c := range calls[st] {
			rank[c.id] = r
		}
		sorted := append([]int(nil), idx...)
		sort.SliceStable(sorted, func(i, j int) bool {
			return rank[heads[sorted[i]].Event.CallID] < rank[heads[sorted[j]].Event.CallID]
		})
		for k, slot := range idx {
			src := sorted[k]
			from[slot] = src
			posMap[heads[src].Pos] = heads[slot].Pos
			seqMap[heads[src].Event.Seq] = heads[slot].Event.Seq
		}
	}
	moved := make([]json.RawMessage, len(entries))
	for slot, src := range from {
		moved[slot] = renum(t, entries[src], posMap, seqMap, heads[src])
	}
	d.Events = replaceRaw(t, d.Events, evDoc["events"], joinRaw(moved))

	// Each parallel step's own events and tool rows.
	steps := make([]json.RawMessage, len(d.Steps))
	for i, sd := range d.Steps {
		steps[i] = sd
		var doc map[string]json.RawMessage
		decode(t, string(sd), &doc)
		var n int
		decode(t, string(doc["step"]), &n)
		if _, ok := window[n]; !ok {
			continue
		}
		var evs []json.RawMessage
		decode(t, string(doc["events"]), &evs)
		hs := make([]evHead, len(evs))
		for k, e := range evs {
			decode(t, string(e), &hs[k])
			evs[k] = renum(t, e, posMap, seqMap, hs[k])
			if p, ok := posMap[hs[k].Pos]; ok {
				hs[k].Pos = p
			}
		}
		order := make([]int, len(evs))
		for k := range order {
			order[k] = k
		}
		sort.SliceStable(order, func(a, b int) bool { return hs[order[a]].Pos < hs[order[b]].Pos })
		sortedEvs := make([]json.RawMessage, len(evs))
		for k, o := range order {
			sortedEvs[k] = evs[o]
		}
		sd = replaceRaw(t, sd, doc["events"], joinRaw(sortedEvs))

		var rows []json.RawMessage
		decode(t, string(doc["tool_calls"]), &rows)
		seqs := make([]int64, len(rows))
		for k, r := range rows {
			var h struct {
				Seq int64 `json:"seq"`
			}
			decode(t, string(r), &h)
			seqs[k] = h.Seq
			if s, ok := seqMap[h.Seq]; ok {
				rows[k] = swapNum(t, r, seqKeyRe, h.Seq, s)
				seqs[k] = s
			}
		}
		idx := make([]int, len(rows))
		for k := range idx {
			idx[k] = k
		}
		sort.SliceStable(idx, func(a, b int) bool { return seqs[idx[a]] < seqs[idx[b]] })
		sortedRows := make([]json.RawMessage, len(rows))
		for k, o := range idx {
			sortedRows[k] = rows[o]
		}
		steps[i] = replaceRaw(t, sd, doc["tool_calls"], joinRaw(sortedRows))
	}
	d.Steps = steps

	// The spans: a tie in start time is ordered by span id (random), so
	// they go by name, then call id; weft.tool.seq follows its start.
	var doc map[string]any
	decode(t, string(d.Spans), &doc)
	spans, _ := doc["spans"].([]any)
	key := func(i int) string {
		sp, _ := spans[i].(map[string]any)
		n, _ := sp["name"].(string)
		a, _ := sp["attrs"].(map[string]any)
		id, _ := a["gen_ai.tool.call.id"].(string)
		return n + "\x00" + id
	}
	sort.SliceStable(spans, func(i, j int) bool { return key(i) < key(j) })
	for _, s := range spans {
		a, _ := s.(map[string]any)["attrs"].(map[string]any)
		if v, ok := a["weft.tool.seq"].(float64); ok {
			if n, ok := seqMap[int64(v)]; ok {
				a["weft.tool.seq"] = n
			}
		}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	d.Spans = b
	return d
}

// parCall is one call of a parallel step: its id, and whether its tool
// is Sequential (a barrier).
type parCall struct {
	id         string
	sequential bool
}

// evHead is what normParallel reads of an event entry.
type evHead struct {
	Pos   int64 `json:"pos"`
	Event struct {
		Type   string `json:"type"`
		Index  int    `json:"index"`
		Seq    int64  `json:"seq"`
		CallID string `json:"call_id"`
	} `json:"event"`
}

// checkParallel pins what the scheduler guarantees for a parallel
// step's call events, as they came: pos and seq rise together, every
// call's events carry a seq, starts are in call order (they are
// emitted on the dispatching goroutine), no more than parallelism
// calls are open at once, and a Sequential call runs alone.
func checkParallel(t *testing.T, shape string, step int, heads []evHead, idx []int, calls []parCall, limit int) {
	t.Helper()
	rank, barrier := map[string]int{}, map[string]bool{}
	for r, c := range calls {
		rank[c.id], barrier[c.id] = r, c.sequential
	}
	open := map[string]bool{}
	lastStart, lastPos, lastSeq := -1, int64(-1), int64(0)
	for _, i := range idx {
		h := heads[i]
		ev := h.Event
		r, ok := rank[ev.CallID]
		switch {
		case !ok:
			t.Fatalf("%s step %d: event %s for call %q the step did not make", shape, step, ev.Type, ev.CallID)
		case ev.Seq <= lastSeq || h.Pos <= lastPos:
			t.Fatalf("%s step %d: %s %s at pos %d seq %d after pos %d seq %d", shape, step, ev.Type, ev.CallID, h.Pos, ev.Seq, lastPos, lastSeq)
		}
		lastPos, lastSeq = h.Pos, ev.Seq
		switch ev.Type {
		case "tool_start":
			if r <= lastStart {
				t.Errorf("%s step %d: %s started out of call order", shape, step, ev.CallID)
			}
			lastStart = r
			for o := range open {
				if barrier[o] || barrier[ev.CallID] {
					t.Errorf("%s step %d: %s started while %s was open; a Sequential call runs alone", shape, step, ev.CallID, o)
				}
			}
			open[ev.CallID] = true
			if limit > 0 && len(open) > limit {
				t.Errorf("%s step %d: %d calls open at once under parallelism %d", shape, step, len(open), limit)
			}
		case "tool_finish":
			if !open[ev.CallID] {
				t.Errorf("%s step %d: %s finished before it started", shape, step, ev.CallID)
			}
			delete(open, ev.CallID)
		}
	}
}

var (
	posKeyRe = regexp.MustCompile(`^\{"pos":(\d+)`)
	seqKeyRe = regexp.MustCompile(`"seq":(\d+)`)
)

// renum gives an event entry its new pos and seq, when they moved.
func renum(t *testing.T, e json.RawMessage, posMap, seqMap map[int64]int64, h evHead) json.RawMessage {
	t.Helper()
	if p, ok := posMap[h.Pos]; ok {
		e = swapNum(t, e, posKeyRe, h.Pos, p)
	}
	if s, ok := seqMap[h.Event.Seq]; ok && h.Event.Seq != 0 {
		e = swapNum(t, e, seqKeyRe, h.Event.Seq, s)
	}
	return e
}

// swapNum rewrites the first number re matches in b — which must be
// was — to now, leaving every other byte as it was.
func swapNum(t *testing.T, b []byte, re *regexp.Regexp, was, now int64) json.RawMessage {
	t.Helper()
	m := re.FindSubmatchIndex(b)
	if m == nil || string(b[m[2]:m[3]]) != strconv.FormatInt(was, 10) {
		t.Fatalf("renumber %d → %d: %s does not lead with it in %s", was, now, re, b)
	}
	out := append([]byte(nil), b[:m[2]]...)
	out = strconv.AppendInt(out, now, 10)
	return append(out, b[m[3]:]...)
}

// joinRaw is a JSON array of entries.
func joinRaw(entries []json.RawMessage) json.RawMessage {
	out := []byte{'['}
	for i, e := range entries {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, e...)
	}
	return append(out, ']')
}

// replaceRaw swaps the one occurrence of was in doc for now.
func replaceRaw(t *testing.T, doc, was, now json.RawMessage) json.RawMessage {
	t.Helper()
	if n := bytes.Count(doc, was); n != 1 {
		t.Fatalf("replace: %d occurrences of %.80s", n, was)
	}
	return bytes.Replace(doc, was, now, 1)
}
