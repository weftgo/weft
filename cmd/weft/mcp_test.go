package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	srv, err := newServer("sqlite://"+filepath.Join(t.TempDir(), "weft.db"), "tok")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); _ = srv.Close() })

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
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	c := newClient(ts.URL, "tok")
	for id, status := range map[string]string{"r_ok": `"status":"succeeded"`, "r_fail": `"status":"failed"`, "r_max": `"status":"failed"`} {
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
	return ts.URL
}

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
		!strings.Contains(line, "failed after step 0 (its last recorded step)") || !strings.Contains(line, "lookup_order ok") {
		t.Errorf("explain_failure r_max = %q\n%+v\ncalls %+v", line, ex, calls)
	}
	line, ex = explain("r_ok")
	if ex.Status != "succeeded" || ex.Step == nil || *ex.Step != 1 || ex.LastAssistantText == nil ||
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
	ctx := context.Background()
	srv := newMCPServer(c, slog.New(slog.DiscardHandler))
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
	}{
		{"request", map[string]any{"id": "r_ok"}},
		{"run", map[string]any{"id": "r_fail"}},
	} {
		isErr, got := call(c.tool, c.args)
		if !isErr || !strings.Contains(got[0], "403") {
			t.Errorf("%s %v under a read token = %v %q, want the 403 as an error", c.tool, c.args, isErr, got)
		}
	}
}

// TestMCPCommand pins the command line: -h lists the tools (exit 0),
// --read-only=false is refused (there is no write mode), arguments are
// refused, and with no Studio answering /api/meta it exits 1 with the
// line saying so.
func TestMCPCommand(t *testing.T) {
	t.Setenv("WEFT_STUDIO_URL", "")
	t.Setenv("WEFT_STUDIO_TOKEN", "")
	_, errb, code := weft("mcp", "-h")
	for _, tool := range mcpTools {
		if !strings.Contains(errb, "  "+tool.name+" ") {
			t.Errorf("mcp -h lacks %s", tool.name)
		}
	}
	if code != 0 || !strings.Contains(errb, "claude mcp add weft -- weft mcp --url http://127.0.0.1:7331") {
		t.Errorf("mcp -h = exit %d\n%s", code, errb)
	}
	if _, errb, code := weft("mcp", "--read-only=false"); code != 2 || !strings.Contains(errb, "no write mode") {
		t.Errorf("--read-only=false = exit %d %q", code, errb)
	}
	if _, errb, code := weft("mcp", "extra"); code != 2 || !strings.Contains(errb, "mcp takes no arguments") {
		t.Errorf("an argument = exit %d %q", code, errb)
	}
	out, errb, code := weft("mcp", "--url", "http://127.0.0.1:1", "--token", "secret-tok")
	if code != 1 || out != "" || !strings.Contains(errb, "weft: weft mcp: no Studio answers /api/meta: studio not reachable at http://127.0.0.1:1") ||
		strings.Contains(errb, "secret-tok") {
		t.Errorf("no Studio = exit %d %q %q", code, out, errb)
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
