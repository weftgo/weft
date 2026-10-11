package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/internal/discovery"
	"github.com/weftgo/weft/otel"
)

// mcpStudio is setup B's server under httptest (the token wall on,
// token "tok") with three runs ingested through the otel pipeline:
// r_ok (orders: a tool call, then an answer; session s_mcp, public id
// pub_mcp), r_fail (triage: the model fails at step 0) and r_max
// (orders under MaxSteps(1): step 0 calls the tool, the budget ends
// the run at the continuation point).
func mcpSeedStudio(t *testing.T) string {
	t.Helper()
	return mcpSeed(t).base
}

// seededStudio is mcpSeedStudio's server with its database path and a
// stop (idempotent; also at cleanup), for the tests that kill or
// restart it.
type seededStudio struct {
	base, db string
	stop     func()
}

// mcpSeed is mcpSeedStudio, the handle kept.
func mcpSeed(t *testing.T) *seededStudio {
	t.Helper()
	db := "sqlite://" + filepath.Join(t.TempDir(), "weft.db")
	srv, err := newServer(db, "tok")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	var once sync.Once
	stop := func() { once.Do(func() { ts.Close(); _ = srv.Close() }) }
	t.Cleanup(stop)

	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	prov := []core.Option{core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider())}
	ok := ordersAgent(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "c1"}),
		wefttest.Say("Order 42 shipped."),
	), prov...)
	if _, err := ok.Generate(ctx, core.RunID("r_ok"), core.Prompt("where is order 42?"),
		core.Metadata(map[string]string{"weft.session.id": "s_mcp", "weft.public_id": "pub_mcp", "weft.turn": "1"})); err != nil {
		t.Fatal(err)
	}
	fail := core.New(wefttest.Script(wefttest.Fail(errors.New("upstream down"))),
		append([]core.Option{core.Name("triage")}, prov...)...)
	if _, err := fail.Generate(ctx, core.RunID("r_fail"), core.Prompt("triage")); err == nil {
		t.Fatal("the failing run succeeded")
	}
	maxed := ordersAgent(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"7"}`, ID: "m1"}),
		wefttest.Say("never reached"),
	), append([]core.Option{core.MaxSteps(1)}, prov...)...)
	if _, err := maxed.Generate(ctx, core.RunID("r_max"), core.Prompt("where is order 7?")); !errors.Is(err, core.ErrMaxSteps) {
		t.Fatalf("r_max = %v, want ErrMaxSteps", err)
	}
	refund := core.Tool("refund", "Refund an order.", func(context.Context, orderIn) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	parker := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"9"}`, ID: "p1"}),
	), append([]core.Option{core.Name("refunds"), refund}, prov...)...)
	if res, err := parker.Generate(ctx, core.RunID("r_park"), core.Prompt("refund order 9")); err != nil || len(res.Pending) != 1 {
		t.Fatalf("r_park = %v, want one parked call", err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	// The record contract's stock_genai fixture through Studio's own
	// OTLP ingest: a stock GenAI app's spans, two derived runs, one failed.
	traces, err := os.ReadFile("../../obsdb/testdata/contract/stock_genai/traces.json")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ts.URL+"/v1/traces", "application/json", strings.NewReader(string(traces)))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ingest stock_genai: %s", resp.Status)
	}
	c := newClient(ts.URL, "tok")
	for id, status := range map[string]string{"r_ok": `"status":"succeeded"`, "r_fail": `"status":"failed"`, "r_max": `"status":"failed"`,
		"r_park": `"pending":1`, derivedFailID: `"status":"failed"`} {
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			b, err := c.get(ctx, runPath(id), nil)
			if err == nil && strings.Contains(string(b), status) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("run %s never settled to %s: %s %v", id, status, b, err)
			}
		}
	}
	return &seededStudio{base: ts.URL, db: db, stop: stop}
}

// restartOn starts s's database again on s's own address under token —
// the same Studio restarted with a new token (a fileless DB's
// per-process one, --rotate-token) — after s stopped.
func restartOn(t *testing.T, s *seededStudio, token string) {
	t.Helper()
	s.stop()
	srv, err := newServer(s.db, token)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", strings.TrimPrefix(s.base, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(srv.Handler())
	_ = ts.Listener.Close()
	ts.Listener = ln
	ts.Start()
	t.Cleanup(func() { ts.Close(); _ = srv.Close() })
}

// pointDiscovery replaces the discovery file with one naming base and
// token, removed at cleanup.
func pointDiscovery(t *testing.T, base, token string) {
	t.Helper()
	w, err := discovery.Write(discovery.Info{URL: base, Token: token, PID: os.Getpid(), Started: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Remove)
}

// mcpCallRun calls the run tool on r_ok: isError and the texts.
func mcpCallRun(t *testing.T, cs *sdk.ClientSession) (bool, []string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "run", Arguments: map[string]any{"id": "r_ok"}})
	if err != nil {
		t.Fatal(err)
	}
	return res.IsError, texts(t, res)
}

// TestMCPReresolvesAfterStudioMoves pins the mid-session re-resolve:
// Studio A answers a call, A dies, the discovery file now names Studio
// B on another port — the next call isErrors (the transport error with
// the "looks again" hint), the one after reads B, and stderr says the
// switch once.
func TestMCPReresolvesAfterStudioMoves(t *testing.T) {
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	t.Setenv("WEFT_DISCOVERY", "")
	a := mcpSeed(t)
	pointDiscovery(t, a.base, "tok")
	var stderr syncBuffer
	srv, err := startMCP(context.Background(), "", "", &stderr)
	if err != nil {
		t.Fatal(err)
	}
	cs := connect(t, srv)
	if isErr, got := mcpCallRun(t, cs); isErr || got[1] != routeBody(t, a.base, "tok", "/api/runs/r_ok") {
		t.Fatalf("on Studio A = %v %q", isErr, got)
	}
	b := mcpSeed(t)
	a.stop()
	pointDiscovery(t, b.base, "tok")
	isErr, got := mcpCallRun(t, cs)
	if !isErr || !strings.Contains(got[0], "studio not reachable at "+a.base) || !strings.Contains(got[0], "the next call looks again") ||
		!strings.Contains(got[0], "no restart needed") {
		t.Errorf("the call that finds A dead = %v %q, want the transport error with the hint", isErr, got)
	}
	if isErr, got := mcpCallRun(t, cs); isErr || got[1] != routeBody(t, b.base, "tok", "/api/runs/r_ok") {
		t.Errorf("after the move = %v %q, want B's row", isErr, got)
	}
	if n := strings.Count(stderr.String(), "now reading the Studio at "+b.base); n != 1 {
		t.Errorf("stderr %q: the switch said %d times, want once", stderr.String(), n)
	}
}

// TestMCPReresolvesAfterTokenRotation: the same Studio restarted on its
// port with a new token, the discovery file rewritten with it, and no
// --token or WEFT_STUDIO_TOKEN — the next call is the 401 (with the
// hint), the one after reads the row with the file's new token.
func TestMCPReresolvesAfterTokenRotation(t *testing.T) {
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	t.Setenv("WEFT_DISCOVERY", "")
	s := mcpSeed(t)
	pointDiscovery(t, s.base, "tok")
	srv, err := startMCP(context.Background(), "", "", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	cs := connect(t, srv)
	if isErr, got := mcpCallRun(t, cs); isErr {
		t.Fatalf("before the restart = %q", got)
	}
	restartOn(t, s, "tok2")
	pointDiscovery(t, s.base, "tok2")
	isErr, got := mcpCallRun(t, cs)
	if !isErr || !strings.Contains(got[0], "401") || !strings.Contains(got[0], "the next call looks again") {
		t.Errorf("the first call after the rotation = %v %q, want the 401 with the hint", isErr, got)
	}
	if isErr, got := mcpCallRun(t, cs); isErr || got[1] != routeBody(t, s.base, "tok2", "/api/runs/r_ok") {
		t.Errorf("after the file's new token = %v %q, want the row", isErr, got)
	}
}

// derivedFailID is the stock_genai fixture's failed derived run
// (docs/record-contract.md §9).
const derivedFailID = "otel-5c0a3e1f9b8d4c2e8a7f6b5d4c3e2f10-1a2b3c4d5e6f7081"

// mcpCall is one tool call of the tests: the arguments and the route
// whose body the answer must carry.
type mcpCall struct {
	tool  string
	args  map[string]any
	route string   // GET path?query, "" for explain_failure
	extra []string // further routes whose bodies follow (request's transcript)
}

// mcpCalls is every registered tool called once against mcpSeedStudio.
func mcpCalls() []mcpCall {
	return []mcpCall{
		{"meta", nil, "/api/meta", nil},
		{"runs", map[string]any{"agent": "orders"}, "/api/runs?agent=orders", nil},
		{"search", map[string]any{"q": "order", "status": "succeeded"}, "/api/search?q=order&status=succeeded", nil},
		{"run", map[string]any{"id": "r_ok"}, "/api/runs/r_ok", nil},
		{"step", map[string]any{"id": "r_ok", "n": 0}, "/api/runs/r_ok/steps/0", nil},
		{"request", map[string]any{"id": "r_ok", "step": 1, "transcript": true}, "/api/runs/r_ok/requests?step=1",
			[]string{"/api/runs/r_ok/transcript?step=1"}},
		{"events", map[string]any{"id": "r_ok", "limit": 3}, "/api/runs/r_ok/events?limit=3", nil},
		{"tools", map[string]any{"id": "r_ok"}, "/api/runs/r_ok/tools", nil},
		{"diff", map[string]any{"a": "r_ok", "b": "r_max"}, "/api/diff?a=r_ok&b=r_max", nil},
		{"export", map[string]any{"id": "r_ok", "format": "jsonl", "max_bytes": mcpMaxBytes}, "/api/runs/r_ok/export?format=jsonl", nil},
		{"sessions", nil, "/api/sessions", nil},
		{"session", map[string]any{"id": "s_mcp"}, "/api/sessions/s_mcp", nil},
		{"explain_failure", map[string]any{"id": "r_fail"}, "", nil},
	}
}

// texts is a result's text items.
func texts(t *testing.T, res *sdk.CallToolResult) []string {
	t.Helper()
	var out []string
	for _, c := range res.Content {
		tc, ok := c.(*sdk.TextContent)
		if !ok {
			t.Fatalf("content %T, want text", c)
		}
		out = append(out, tc.Text)
	}
	return out
}

// routeBody is GET route against the Studio, as the tool must answer
// it (compacted).
func routeBody(t *testing.T, base, token, route string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+route, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d %s", route, resp.StatusCode, b)
	}
	return string(compact(b))
}

// directBody is GET route's raw body, whatever the status.
func directBody(t *testing.T, base, token, route string) []byte {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+route, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return b
}

// TestMCPStdio pins `weft mcp` over real stdio: this binary re-executed
// as `weft mcp --url <studio>` (the token from WEFT_STUDIO_TOKEN),
// driven by the SDK's client over the command transport. Every tool is
// listed and called once; each answer is a summary line, then the
// route's own body (compacted) — request's transcript body after its
// requests body — and nothing cut. explain_failure on the model
// failure answers the error and the failing step; on the MaxSteps run,
// the step whose tool call ran and the budget's error.
func TestMCPStdio(t *testing.T) {
	base := mcpSeedStudio(t)
	cmd := exec.Command(os.Args[0], "mcp", "--url", base)
	cmd.Env = append(os.Environ(), "WEFT_MCP_HELPER=1", "WEFT_STUDIO_TOKEN=tok", "WEFT_DISCOVERY=off", "WEFT_STUDIO_URL=")
	var stderr syncBuffer
	cmd.Stderr = &stderr
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).
		Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v (stderr %q)", err, stderr.String())
	}
	defer func() { _ = cs.Close() }()

	init := cs.InitializeResult()
	if !strings.Contains(init.Instructions, "READ-ONLY") {
		t.Errorf("instructions %q, want READ-ONLY", init.Instructions)
	}
	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, tool := range lt.Tools {
		listed = append(listed, tool.Name)
	}
	var called []string
	for _, c := range mcpCalls() {
		called = append(called, c.tool)
	}
	slices.Sort(listed)
	slices.Sort(called)
	if !slices.Equal(listed, called) {
		t.Fatalf("listed tools %v, the test calls %v: every tool is called once", listed, called)
	}

	for _, c := range mcpCalls() {
		if c.route == "" {
			continue
		}
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: c.tool, Arguments: c.args})
		if err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
		got := texts(t, res)
		if res.IsError {
			t.Errorf("%s: error %q", c.tool, got)
			continue
		}
		want := append([]string{routeBody(t, base, "tok", c.route)}, nil...)
		for _, r := range c.extra {
			want = append(want, routeBody(t, base, "tok", r))
		}
		if len(got) != 1+len(want) || got[0] == "" {
			t.Errorf("%s: %d items %q, want a summary and %d bodies", c.tool, len(got), got, len(want))
			continue
		}
		for i, w := range want {
			if got[1+i] != w {
				t.Errorf("%s: body %d =\n%.400s\nwant GET %s's\n%.400s", c.tool, i, got[1+i], c.route, w)
			}
		}
		if c.tool == "diff" {
			// The summary line counts Studio's changed_steps list and
			// names its first_changed (a list, never an int: I4.2's fix).
			var d struct {
				Summary struct {
					ChangedSteps []int `json:"changed_steps"`
					FirstChanged *int  `json:"first_changed"`
				} `json:"summary"`
			}
			if err := json.Unmarshal([]byte(want[0]), &d); err != nil || len(d.Summary.ChangedSteps) == 0 || d.Summary.FirstChanged == nil {
				t.Fatalf("diff r_ok r_max: %v %s, want changed steps to count", err, want[0])
			}
			line := fmt.Sprintf("diff r_ok → r_max: %d changed steps, first at step %d", len(d.Summary.ChangedSteps), *d.Summary.FirstChanged)
			if got[0] != line {
				t.Errorf("diff summary %q, want %q", got[0], line)
			}
		}
	}

	explain := func(id string) (string, explanation) {
		t.Helper()
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "explain_failure", Arguments: map[string]any{"id": id}})
		if err != nil || res.IsError {
			t.Fatalf("explain_failure %s: %v %v", id, err, texts(t, res))
		}
		got := texts(t, res)
		var ex explanation
		if err := json.Unmarshal([]byte(got[1]), &ex); err != nil {
			t.Fatalf("explain_failure %s: %v\n%s", id, err, got[1])
		}
		t.Logf("explain_failure %s: %s", id, got[0])
		return got[0], ex
	}
	line, ex := explain("r_fail")
	if ex.Status != "failed" || !strings.Contains(ex.Error, "upstream down") || ex.Step == nil || *ex.Step != 0 || ex.StepRole != "in_flight" ||
		!strings.Contains(line, "run r_fail (triage): failed at step 0: ") || !strings.Contains(line, "upstream down") ||
		!strings.HasSuffix(line, base+"/runs/r_fail") {
		t.Errorf("explain_failure r_fail = %q\n%+v", line, ex)
	}
	line, ex = explain("r_max")
	var calls []struct {
		Name   string `json:"name"`
		Result struct {
			Content string `json:"content"`
		} `json:"result"`
	}
	_ = json.Unmarshal(ex.ToolCalls, &calls)
	if ex.Status != "failed" || !strings.Contains(ex.Error, "maximum number of steps") || ex.StepRole != "last" || ex.Step == nil || *ex.Step != 0 ||
		len(calls) != 1 || calls[0].Name != "lookup_order" || calls[0].Result.Content != "order 42 shipped" ||
		!strings.Contains(line, "failed before step 1 started (step 0, its last recorded step, finished): weft: run failed at step 1") ||
		ex.StepNote != "" || strings.Contains(line, "in-flight") || !strings.Contains(line, "lookup_order ok") {
		t.Errorf("explain_failure r_max = %q\n%+v\ncalls %+v", line, ex, calls)
	}
	line, ex = explain("r_park")
	if ex.Pending != 1 || ex.StepStatus != "parked" || !strings.Contains(line, "; parked: 1 calls awaiting approval (refund)") {
		t.Errorf("explain_failure r_park = %q\n%+v", line, ex)
	}
	line, ex = explain(derivedFailID)
	if ex.Status != "failed" || ex.StepStatus != "error" || ex.Step == nil || !strings.Contains(line, "failed at step ") ||
		strings.Contains(line, "after step") {
		t.Errorf("explain_failure %s (derived) = %q\n%+v", derivedFailID, line, ex)
	}
	line, ex = explain("r_ok")
	if ex.Status != "succeeded" || ex.Step == nil || *ex.Step != 1 || ex.LastAssistantText == nil ||
		ex.StepNote != "" || strings.Contains(line, "in-flight") ||
		*ex.LastAssistantText != "Order 42 shipped." || !strings.Contains(line, "last step 1") {
		t.Errorf("explain_failure r_ok = %q\n%+v", line, ex)
	}

	// An unknown run is Studio's 404 as the tool's error, never empty.
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "run", Arguments: map[string]any{"id": "nope"}})
	if err != nil || !res.IsError || !strings.Contains(texts(t, res)[0], "404") {
		t.Errorf("unknown run = %v %+v, want an isError 404", err, res)
	}
	_ = cs.Close()
	if s := stderr.String(); strings.Contains(s, "tok)") || !strings.Contains(s, "read-only tools over stdio") ||
		!strings.Contains(s, "token from WEFT_STUDIO_TOKEN") {
		t.Errorf("stderr %q: want the start line naming where the token came from, never the token", s)
	}
}

// recorder is a transport that records every request's method and
// URL, and whether a token travelled anywhere but the header.
type recorder struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	return http.DefaultTransport.RoundTrip(req)
}

// inProcess connects an SDK client to newMCPServer over c, in memory.
func inProcess(t *testing.T, c *client) *sdk.ClientSession {
	t.Helper()
	return connect(t, newMCPServer(c, slog.New(slog.DiscardHandler)))
}

// connect is an SDK client over srv, in memory.
func connect(t *testing.T, srv *sdk.Server) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := sdk.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestMCPReadOnlyMethods proves the method set: every registered tool
// called (each once, some with arguments a route refuses, some twice
// over), through a client whose transport records each request — and
// every request is a GET, its token in the Authorization header and
// never in a URL.
func TestMCPReadOnlyMethods(t *testing.T) {
	base := mcpSeedStudio(t)
	rec := &recorder{}
	c := newClient(base, "tok")
	c.http.Transport = rec
	cs := inProcess(t, c)
	ctx := context.Background()
	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(lt.Tools) != len(mcpTools) {
		t.Errorf("%d tools registered, the table lists %d", len(lt.Tools), len(mcpTools))
	}
	calls := mcpCalls()
	calls = append(calls,
		mcpCall{tool: "explain_failure", args: map[string]any{"id": "r_max"}},
		mcpCall{tool: "step", args: map[string]any{"id": "r_ok", "n": 99}},
		mcpCall{tool: "export", args: map[string]any{"id": "r_ok", "format": "wefttest"}},
	)
	for _, tool := range lt.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %s: not annotated read-only", tool.Name)
		}
		found := false
		for _, cl := range calls {
			found = found || cl.tool == tool.Name
		}
		if !found {
			t.Errorf("tool %s is registered but not exercised", tool.Name)
		}
	}
	for _, cl := range calls {
		if _, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: cl.tool, Arguments: cl.args}); err != nil {
			t.Fatalf("%s: %v", cl.tool, err)
		}
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.reqs) < len(lt.Tools) {
		t.Fatalf("%d requests for %d tools", len(rec.reqs), len(lt.Tools))
	}
	methods := map[string]bool{}
	for _, r := range rec.reqs {
		methods[r.Method] = true
		if r.Header.Get("Authorization") != "Bearer tok" || strings.Contains(r.URL.String(), "tok") {
			t.Errorf("%s %s: the token travels in the Authorization header only", r.Method, r.URL)
		}
		if r.URL.Query().Has("token") {
			t.Errorf("%s: a token in the URL", r.URL)
		}
	}
	if len(methods) != 1 || !methods[http.MethodGet] {
		t.Errorf("methods %v, want {GET}", methods)
	}
	// wefttest is refused before any request.
	res, _ := cs.CallTool(ctx, &sdk.CallToolParams{Name: "export", Arguments: map[string]any{"id": "r_ok", "format": "wefttest"}})
	if !res.IsError || !strings.Contains(texts(t, res)[0], "weft export <id> --wefttest") {
		t.Errorf("export wefttest = %q, want the refusal naming the terminal command", texts(t, res))
	}
}

// TestMCPTransportRefusesWrites pins the wall under the tools: the
// client newMCPServer serves over cannot send anything but GET or HEAD.
// A tool mutated to POST — a branch no other test reaches, written here
// as a tool registered beside the real ones that reaches the client's
// http.Client directly — is refused in the process, and Studio sees
// nothing.
func TestMCPTransportRefusesWrites(t *testing.T) {
	var hits []string
	var mu sync.Mutex
	studio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.Method)
		mu.Unlock()
		_, _ = io.WriteString(w, "{}")
	}))
	defer studio.Close()
	c := newClient(studio.URL, "tok")
	srv := newMCPServer(c, slog.New(slog.DiscardHandler))
	addTool(srv, "mutant", "a tool mutated to write", func(ctx context.Context, _ *sdk.CallToolRequest, in mcpRunIn) (*sdk.CallToolResult, any, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/runs/"+in.ID+"/cancel", strings.NewReader("{}"))
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, nil, err
		}
		_ = resp.Body.Close()
		return out(0, "sent")
	})
	cs := connect(t, srv)
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "mutant", Arguments: map[string]any{"id": "r"}})
	if err != nil {
		t.Fatal(err)
	}
	got := texts(t, res)
	t.Logf("mutated tool: isError=%v %q", res.IsError, got)
	if !res.IsError || !strings.Contains(got[0], "weft mcp is read-only: POST refused") {
		t.Errorf("a POST through the tools' client = %v %q, want the refusal", res.IsError, got)
	}
	for _, m := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req, _ := http.NewRequest(m, studio.URL+"/api/runs/r", nil)
		if _, err := c.http.Do(req); err == nil || !strings.Contains(err.Error(), "weft mcp is read-only: "+m+" refused") {
			t.Errorf("%s through the transport = %v, want the refusal", m, err)
		}
	}
	if _, err := c.get(context.Background(), "/api/meta", nil); err != nil {
		t.Errorf("a GET through the transport = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 1 || hits[0] != http.MethodGet {
		t.Errorf("Studio saw %v, want the one GET", hits)
	}
}

// TestMCPNoWriteVerbs pins the read-only promise by name and by
// declaration: no tool's name carries a write verb (run and runs are
// the reads of /api/runs — the verb "run" as starting one is the
// playground's, absent), the instructions say read-only, and the
// server declares it in its capabilities.
func TestMCPNoWriteVerbs(t *testing.T) {
	cs := inProcess(t, newClient("http://127.0.0.1:1", ""))
	lt, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	verbs := []string{"approve", "deny", "steer", "cancel", "delete", "propose", "run", "replay", "experiment",
		"fixtures", "preview", "breakpoints", "vacuum", "resolve", "decide", "start", "send", "post", "put", "write"}
	for _, tool := range lt.Tools {
		if tool.Name == "run" || tool.Name == "runs" {
			continue // GET /api/runs/{id} and GET /api/runs: nouns, pinned GET by TestMCPReadOnlyMethods
		}
		for _, v := range verbs {
			if strings.Contains(tool.Name, v) {
				t.Errorf("tool %q carries the write verb %q", tool.Name, v)
			}
		}
	}
	init := cs.InitializeResult()
	if !strings.Contains(init.Instructions, "READ-ONLY") || !strings.Contains(init.Instructions, "never approves, denies, steers, cancels, deletes") {
		t.Errorf("instructions %q", init.Instructions)
	}
	w, _ := init.Capabilities.Experimental["weft"].(map[string]any)
	if w["read_only"] != true {
		t.Errorf("capabilities.experimental = %v, want weft.read_only", init.Capabilities.Experimental)
	}
}

// TestMCPTruncation pins the bound: an answer past max_bytes is cut at
// it and a final note says so; under it, nothing is added.
func TestMCPTruncation(t *testing.T) {
	body := []byte(`{"runs":[` + strings.Repeat(`{"id":"r"},`, 50) + `{"id":"é"}]}`)
	res, _, _ := out(100, "s", body)
	got := texts(t, res)
	if len(got) != 3 || len(got[1]) > 100 || !strings.HasPrefix(string(body), got[1]) ||
		!strings.Contains(got[2], "truncated: showing") || !strings.Contains(got[2], "raise max_bytes") {
		t.Errorf("cut = %q", got)
	}
	// A bound smaller than nothing useful: no empty item, the note stays.
	res, _, _ = out(1, "s", []byte(`{"a":1}`), []byte(`{"b":2}`))
	if got := texts(t, res); len(got) != 3 || got[1] != "{" || !strings.Contains(got[2], "truncated") {
		t.Errorf("max_bytes 1 = %q, want the summary, one byte, the note", got)
	}
	res, _, _ = out(0, "s", body)
	if got := texts(t, res); len(got) != 2 || got[1] != string(body) {
		t.Errorf("under the default bound = %q", got)
	}
	if c := cut([]byte("aé"), 2); string(c) != "a" {
		t.Errorf("cut mid-rune = %q", c)
	}
}

// TestMCPPanelToken pins a read-scoped panel token: what it may read
// answers, what it may not is the route's 403 as the tool's error text
// (with Studio's body), never an empty result.
func TestMCPPanelToken(t *testing.T) {
	base := mcpSeedStudio(t)
	req, _ := http.NewRequest(http.MethodPost, base+"/api/panel-tokens", strings.NewReader(`{"public_id":"pub_mcp","ttl":"1h"}`))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var minted struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&minted)
	_ = resp.Body.Close()
	if minted.Token == "" {
		t.Fatalf("mint: %s", resp.Status)
	}
	cs := inProcess(t, newClient(base, minted.Token))
	ctx := context.Background()
	call := func(tool string, args map[string]any) (bool, []string) {
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res.IsError, texts(t, res)
	}
	if isErr, got := call("run", map[string]any{"id": "r_ok"}); isErr || got[1] != routeBody(t, base, minted.Token, "/api/runs/r_ok") {
		t.Errorf("run r_ok under its panel token = %v %q", isErr, got)
	}
	for _, c := range []struct {
		tool string
		args map[string]any
		fix  bool // the route's 403 carries the badge's fix
	}{
		{"request", map[string]any{"id": "r_ok"}, true},
		{"run", map[string]any{"id": "r_fail"}, false},
	} {
		isErr, got := call(c.tool, c.args)
		if !isErr || !strings.Contains(got[0], "403") {
			t.Errorf("%s %v under a read token = %v %q, want the 403 as an error", c.tool, c.args, isErr, got)
		}
		if c.fix {
			var body struct {
				Fix string `json:"fix"`
			}
			raw := directBody(t, base, minted.Token, "/api/runs/r_ok/requests")
			if err := json.Unmarshal(raw, &body); err != nil || body.Fix == "" || !strings.Contains(got[0], body.Fix) {
				t.Errorf("%s: the 403 error %q lacks Studio's fix %q (body %s)", c.tool, got[0], body.Fix, raw)
			}
		}
	}
	// explain_failure under the read token: the step's request block is
	// the hidden badge, so no request hint the token would be refused.
	isErr, got := call("explain_failure", map[string]any{"id": "r_ok"})
	var ex explanation
	if isErr || json.Unmarshal([]byte(got[1]), &ex) != nil || ex.Step == nil {
		t.Fatalf("explain_failure under a read token = %v %q", isErr, got)
	}
	for _, n := range ex.Next {
		if strings.HasPrefix(n, "request ") {
			t.Errorf("next %q offers the request tool to a read token", ex.Next)
		}
	}
}

// TestMCPCommand pins the command line: -h lists the tools (exit 0),
// --read-only=false is refused (there is no write mode), arguments are
// refused, and with no Studio answering /api/meta it serves, WARNs
// once, and a tool call isErrors with the line saying so.
func TestMCPCommand(t *testing.T) {
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	_, errb, code := weft("mcp", "-h")
	for _, tool := range mcpTools {
		if !strings.Contains(errb, "  "+tool.name+" ") {
			t.Errorf("mcp -h lacks %s", tool.name)
		}
	}
	if code != 0 || !strings.Contains(errb, "claude mcp add weft -- weft mcp\n") || !strings.Contains(errb, `"args":["mcp"]`) ||
		!strings.Contains(errb, "claude mcp add weft -- weft mcp --url http://127.0.0.1:7331") {
		t.Errorf("mcp -h = exit %d\n%s", code, errb)
	}
	if _, errb, code := weft("mcp", "--read-only=false"); code != 2 || !strings.Contains(errb, "no write mode") {
		t.Errorf("--read-only=false = exit %d %q", code, errb)
	}
	if _, errb, code := weft("mcp", "extra"); code != 2 || !strings.Contains(errb, "mcp takes no arguments") {
		t.Errorf("an argument = exit %d %q", code, errb)
	}
	// No Studio answering /api/meta: it serves anyway (an MCP client
	// starts its servers once, maybe before the Studio), says so in one
	// WARN, and each tool call isErrors with the line and its fix.
	cmd := exec.Command(os.Args[0], "mcp", "--url", "http://127.0.0.1:1", "--token", "secret-tok")
	cmd.Env = append(os.Environ(), "WEFT_MCP_HELPER=1", "WEFT_DISCOVERY=off", "WEFT_STUDIO_URL=", "WEFT_STUDIO_TOKEN=")
	var stderr syncBuffer
	cmd.Stderr = &stderr
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).
		Connect(ctx, &sdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("no Studio: connect: %v (stderr %q), want it to serve", err, stderr.String())
	}
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "run", Arguments: map[string]any{"id": "r_ok"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(t, res); !res.IsError || !strings.Contains(got[0], "no Studio answers /api/meta: studio not reachable at http://127.0.0.1:1") ||
		!strings.Contains(got[0], "the next call retries the --url given") || strings.Contains(got[0], "secret-tok") {
		t.Errorf("a tool call with no Studio = %v %q, want the isError line with its fix", res.IsError, got)
	}
	_ = cs.Close()
	if errb := stderr.String(); strings.Count(errb, "level=WARN") != 1 || !strings.Contains(errb, "no Studio answers /api/meta yet; tools will retry per call") ||
		!strings.Contains(errb, "read-only tools over stdio") || strings.Contains(errb, "secret-tok") {
		t.Errorf("no Studio: stderr %q, want one WARN and the start line, never the token", errb)
	}
}

// TestMCPServesBeforeStudio pins the order an MCP client imposes: the
// server starts first (nothing answers: a WARN, it serves), a Studio
// comes up later on a port only the discovery file names (weft dev
// on 7332–7340), and the next tool call finds it — no restart.
func TestMCPServesBeforeStudio(t *testing.T) {
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	t.Setenv("WEFT_DISCOVERY", "")
	// A file naming a dead port first, so the default address (maybe
	// the developer's own Studio) is never the one read.
	dead, err := discovery.Write(discovery.Info{URL: "http://127.0.0.1:1", PID: os.Getpid(), Started: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	defer dead.Remove()
	var stderr syncBuffer
	srv, err := startMCP(context.Background(), "", "", &stderr)
	if err != nil {
		t.Fatalf("startMCP with no Studio: %v, want it to serve", err)
	}
	if s := stderr.String(); !strings.Contains(s, "level=WARN") || !strings.Contains(s, "tools will retry per call") {
		t.Errorf("stderr %q, want the WARN", s)
	}
	cs := connect(t, srv)
	ctx := context.Background()
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "run", Arguments: map[string]any{"id": "r_ok"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(t, res); !res.IsError || !strings.Contains(got[0], "no Studio answers /api/meta") ||
		!strings.Contains(got[0], "no restart needed") {
		t.Errorf("before the Studio = %v %q, want the isError line", res.IsError, got)
	}

	base := mcpSeedStudio(t)
	dead.Remove()
	w, err := discovery.Write(discovery.Info{URL: base, Token: "tok", PID: os.Getpid(), Started: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Remove()
	res, err = cs.CallTool(ctx, &sdk.CallToolParams{Name: "run", Arguments: map[string]any{"id": "r_ok"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(t, res); res.IsError || len(got) != 2 || got[1] != routeBody(t, base, "tok", "/api/runs/r_ok") {
		t.Errorf("after the Studio came up = %v %q, want r_ok's row", res.IsError, got)
	}
	if s := stderr.String(); !strings.Contains(s, "now reading the Studio at "+base) || strings.Contains(s, "tok\n") {
		t.Errorf("stderr %q, want the switch said once", s)
	}
	// explain_failure's link names the Studio found, not the dead one.
	res, _ = cs.CallTool(ctx, &sdk.CallToolParams{Name: "explain_failure", Arguments: map[string]any{"id": "r_ok"}})
	if got := texts(t, res); res.IsError || !strings.HasSuffix(got[0], base+"/runs/r_ok") {
		t.Errorf("explain_failure after the switch = %q", got)
	}
}

// TestMCPExplainTailSurvivesCut pins explain_failure's field order: a
// step whose tool results run to ~100 KB, under the default max_bytes,
// is cut in its bulk (attempts, tool calls, children) — next and
// last_assistant_text, ahead of it, survive the cut.
func TestMCPExplainTailSurvivesCut(t *testing.T) {
	big := strings.Repeat("x", 1000)
	var calls []string
	for i := range 100 {
		calls = append(calls, fmt.Sprintf(`{"call_id":"c%d","name":"lookup","result":{"content":%q}}`, i, big))
	}
	m := &mcpReader{s: fakeStudio{
		"/api/runs/r":            {200, `{"id":"r","agent":"a","status":"failed","err":"boom","steps":1,"holes":[]}`},
		"/api/runs/r/steps/1":    {200, `{"status":"error","tool_calls":[` + strings.Join(calls, ",") + `],"attempts":[],"children":[],"holes":[]}`},
		"/api/runs/r/transcript": {200, `{"batches":[{"messages":[{"role":"assistant","content":[{"type":"text","text":"looking it up"}]}]}]}`},
	}}
	res, _, err := m.explainFailure(context.Background(), nil, mcpRunIn{ID: "r"})
	if err != nil {
		t.Fatal(err)
	}
	got := texts(t, res)
	if len(got) != 3 || !strings.Contains(got[2], "truncated") {
		t.Fatalf("want the body cut at the default bound: %d items, last %.200q", len(got), got[len(got)-1])
	}
	for _, want := range []string{`"last_assistant_text":"looking it up"`, `"next":["step {id:\"r\", n:1}`, `"step_holes":[]`} {
		if !strings.Contains(got[1], want) {
			t.Errorf("the cut body lacks %s", want)
		}
	}
}

// TestMCPStudioResolution pins where the Studio and token come from:
// --url and --token first, then the env, then a trusted discovery file
// (its token only for the Studio it names), WEFT_DISCOVERY=off skipping
// it, then the default address with no token.
func TestMCPStudioResolution(t *testing.T) {
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	t.Setenv("WEFT_DISCOVERY", "")
	w, err := discovery.Write(discovery.Info{URL: "http://127.0.0.1:7339", Token: "disc-tok", PID: os.Getpid(), Started: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Remove()
	for _, c := range []struct {
		name, url, tok, env, envTok, disc string
		wantURL, wantTok, wantFrom        string
	}{
		{"discovery", "", "", "", "", "", "http://127.0.0.1:7339", "disc-tok", "from the discovery file"},
		{"url names the file's Studio", "http://127.0.0.1:7339/", "", "", "", "", "http://127.0.0.1:7339/", "disc-tok", "from the discovery file"},
		{"another url: no file token", "http://127.0.0.1:7000", "", "", "", "", "http://127.0.0.1:7000", "", "none"},
		{"env url", "", "", "http://127.0.0.1:7001", "", "", "http://127.0.0.1:7001", "", "none"},
		{"flags win", "http://h:1", "f", "http://127.0.0.1:7001", "e", "", "http://h:1", "f", "from --token"},
		{"env token", "", "", "", "e", "", "http://127.0.0.1:7339", "e", "from WEFT_STUDIO_TOKEN"},
		{"discovery off", "", "", "", "", "off", defaultURL, "", "none"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("WEFT_STUDIO_URL", c.env)
			t.Setenv("WEFT_STUDIO_TOKEN", c.envTok)
			t.Setenv("WEFT_DISCOVERY", c.disc)
			u, tok, from := mcpStudio(c.url, c.tok)
			if u != c.wantURL || tok != c.wantTok || from != c.wantFrom {
				t.Errorf("mcpStudio(%q, %q) = %q %q %q, want %q %q %q", c.url, c.tok, u, tok, from, c.wantURL, c.wantTok, c.wantFrom)
			}
		})
	}
}

// fakeStudio is a studioGetter answering fixed bodies per path.
type fakeStudio map[string]struct {
	status int
	body   string
}

func (f fakeStudio) get(_ context.Context, path string, _ url.Values) (int, []byte, error) {
	a, ok := f[path]
	if !ok {
		return http.StatusNotFound, []byte(`{"error":{"code":"not_found","message":"no"}}`), &statusError{Status: 404, line: "GET " + path + ": 404"}
	}
	if a.status != http.StatusOK {
		return a.status, []byte(a.body), &statusError{Status: a.status, Body: []byte(a.body), line: fmt.Sprintf("GET %s: %d", path, a.status)}
	}
	return a.status, []byte(a.body), nil
}
func (fakeStudio) runLink(id string) string { return "http://s/runs/" + id }
func (fakeStudio) redacted() string         { return "http://s" }

// TestMCPExplainInFlightHidden pins the fallback's honesty: an
// in-flight step that answers 403 or 5xx is said, never a silent
// switch to the last recorded step.
func TestMCPExplainInFlightHidden(t *testing.T) {
	for _, c := range []struct {
		status int
		word   string
	}{{http.StatusForbidden, "hidden (403)"}, {http.StatusBadGateway, "unavailable (502)"}} {
		m := &mcpReader{s: fakeStudio{
			"/api/runs/r":            {200, `{"id":"r","agent":"a","status":"failed","err":"boom","steps":1,"holes":[]}`},
			"/api/runs/r/steps/1":    {c.status, `{"error":{"code":"x","message":"y"},"badge":"hidden"}`},
			"/api/runs/r/steps/0":    {200, `{"status":"ok","tool_calls":[],"holes":[]}`},
			"/api/runs/r/transcript": {200, `{"batches":[]}`},
		}}
		res, _, err := m.explainFailure(context.Background(), nil, mcpRunIn{ID: "r"})
		if err != nil {
			t.Fatal(err)
		}
		got := texts(t, res)
		want := fmt.Sprintf("the in-flight step 1 is %s; showing the last recorded step", c.word)
		if !strings.Contains(got[0], want) || !strings.Contains(got[1], want) {
			t.Errorf("%d: %q, want %q in the line and the step_note", c.status, got, want)
		}
	}
}

// failingStudio is fakeStudio with a transport failure (status 0) on
// the listed paths.
type failingStudio struct {
	fakeStudio
	down map[string]bool
}

func (f failingStudio) get(ctx context.Context, path string, q url.Values) (int, []byte, error) {
	if f.down[path] {
		return 0, nil, errors.New("studio not reachable")
	}
	return f.fakeStudio.get(ctx, path, q)
}

// TestMCPExplainBothStepsFail pins the note when the fallback fails
// too: the in-flight note kept, the fallback's failure appended, and a
// transport failure read "unreachable", never "(0)".
func TestMCPExplainBothStepsFail(t *testing.T) {
	m := &mcpReader{s: failingStudio{fakeStudio{
		"/api/runs/r":            {200, `{"id":"r","agent":"a","status":"failed","err":"boom","steps":1,"holes":[]}`},
		"/api/runs/r/steps/1":    {http.StatusForbidden, `{"error":{"code":"forbidden","message":"no"}}`},
		"/api/runs/r/transcript": {200, `{"batches":[]}`},
	}, map[string]bool{"/api/runs/r/steps/0": true}}}
	res, _, err := m.explainFailure(context.Background(), nil, mcpRunIn{ID: "r"})
	if err != nil {
		t.Fatal(err)
	}
	got := texts(t, res)
	want := "the in-flight step 1 is hidden (403); showing the last recorded step; step 0 is unreachable"
	if !strings.Contains(got[0], want) || strings.Contains(got[0], "(0)") {
		t.Errorf("%q, want %q", got[0], want)
	}
}

// TestMCPSourceOneClient guards the wall by source: the read-only
// transport covers the one client startMCP builds, so mcp.go may build
// no other — no default client, no package-level request helper, no
// second http.Client, exactly one newClient call (a re-resolved Studio
// is a copy of that client, sharing its walled http.Client).
func TestMCPSourceOneClient(t *testing.T) {
	b, err := os.ReadFile("mcp.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, banned := range []string{"http.DefaultClient", "http.Get(", "http.Post(", "http.PostForm(", "http.Head(", "&http.Client", "http.Client{", "http.DefaultTransport.RoundTrip", "net.Dial"} {
		if strings.Contains(src, banned) {
			t.Errorf("mcp.go uses %s: every request goes through the one walled client", banned)
		}
	}
	if n := strings.Count(src, "newClient("); n != 1 {
		t.Errorf("mcp.go calls newClient %d times, want the one in startMCP", n)
	}
	i := strings.Index(src, "func startMCP(")
	j := strings.Index(src[i+1:], "\nfunc ")
	if i < 0 || j < 0 || !strings.Contains(src[i:i+1+j], "newClient(") {
		t.Error("the one newClient call is not in startMCP")
	}
}
