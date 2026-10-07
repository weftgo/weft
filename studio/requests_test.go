package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
)

// requestsServer is a loopback Studio (setup A) over a fresh sqlite
// file, fed by the real pipeline: otel → OTLP ingest → obsdb.
func requestsServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	srv := New(Open(filepath.Join(t.TempDir(), "weft.db")))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })
	return ts, srv
}

// recordRequestsRun drives the A1 scenario through the pipeline: three
// steps; a PrepareStep rewrite of step 1's system text (two prompts); a
// ToolSource that grows a tool after step 0 (two catalogs); mw.Retry
// over a script whose step-1 call fails once (step 1 has two attempts)
// — four request records. Destination options pass through (NoContent
// makes the content-off run).
func recordRequestsRun(t *testing.T, url, runID string, dest ...otel.DestOption) {
	t.Helper()
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(url, "", dest...), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	type orderIn struct {
		OrderID string `json:"order_id" jsonschema:"the order"`
	}
	var grown atomic.Bool
	lookup := core.Tool("lookup_order", "Look up an order.", func(context.Context, orderIn) (string, error) {
		grown.Store(true)
		return "order shipped", nil
	})
	refund := core.Tool("refund", "Refund an order.", func(context.Context, orderIn) (string, error) {
		return "refunded", nil
	})
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "c1"}),
		wefttest.Fail(core.ErrStreamIdle),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"42"}`, ID: "c2"}),
		wefttest.Say("Refunded order 42."),
	), core.Name("orders"), core.Instructions("You are a support agent."),
		core.ToolSource(func() []*core.ToolDef {
			if grown.Load() {
				return []*core.ToolDef{lookup, refund}
			}
			return []*core.ToolDef{lookup}
		}),
		core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
			if step == 1 {
				req.System += " Refunds need a reason."
			}
			return req, nil
		}),
		core.WrapModel(mw.Retry(mw.BaseDelay(0))),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	if _, err := agent.Generate(ctx, core.RunID(runID), core.Prompt("refund order 42")); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

// fetchJSON GETs path until it answers 200 and cond holds on the body
// (ingest lands asynchronously), then returns the body.
func fetchJSON(t *testing.T, ts *httptest.Server, path string, cond func(string) bool) string {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK && (cond == nil || cond(string(b))) {
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s = %d %s", path, resp.StatusCode, b)
		}
	}
}

// timeRe normalizes the record times a real run stamps, so the
// goldens are byte-stable.
var timeRe = regexp.MustCompile(`"time": ?"[^"]*"`)

func requestsGolden(t *testing.T, name, body string) {
	t.Helper()
	wefttest.Golden(t, "testdata/api/"+name, []byte(timeRe.ReplaceAllString(pretty(t, body), `"time": "(time)"`)))
}

type requestsDoc struct {
	Requests []struct {
		Index          int64           `json:"index"`
		Step           int             `json:"step"`
		Attempt        int64           `json:"attempt"`
		SystemHash     string          `json:"system_hash"`
		CatalogHash    string          `json:"catalog_hash"`
		Content        string          `json:"content"`
		Body           json.RawMessage `json:"body"`
		PromptRaw      json.RawMessage `json:"prompt"`
		ToolsRaw       json.RawMessage `json:"tools"`
		TruncatedBytes int64           `json:"truncated_bytes"`
	} `json:"requests"`
	NextFrom *int64 `json:"next_from"`
	Badge    string `json:"badge"`
	Reason   string `json:"reason"`
	Fix      string `json:"fix"`
}

type toolsDocT struct {
	Catalogs []struct {
		Hash  string `json:"hash"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Content string `json:"content"`
	} `json:"catalogs"`
	Badge  string `json:"badge"`
	Reason string `json:"reason"`
	Fix    string `json:"fix"`
}

func decode(t *testing.T, body string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

// TestRequestsRoutes pins GET /api/runs/{id}/requests and /tools (ADR
// 0028 §10, plan A1) over a run recorded through the real pipeline:
// four attempts over three steps, two prompts, two catalogs, each
// distinct hash resolved inline; refs=1 drops the inline objects;
// paging and the step filter; the run document's three columns.
func TestRequestsRoutes(t *testing.T) {
	ts, _ := requestsServer(t)
	recordRequestsRun(t, ts.URL, "r_req")

	run := fetchJSON(t, ts, "/api/runs/r_req", func(b string) bool { return strings.Contains(b, `"request_count":4`) })
	var doc struct {
		InstructionsHash string  `json:"instructions_hash"`
		CatalogHash      string  `json:"catalog_hash"`
		RequestCount     int64   `json:"request_count"`
		RequestsBadge    *string `json:"requests_badge"`
	}
	decode(t, run, &doc)
	if doc.InstructionsHash == "" || doc.CatalogHash == "" || doc.RequestCount != 4 || doc.RequestsBadge != nil {
		t.Errorf("run document = %+v, want both hashes, request_count 4 and no requests_badge", doc)
	}

	body := fetchJSON(t, ts, "/api/runs/r_req/requests", nil)
	requestsGolden(t, "requests-ok.golden.json", body)
	var page requestsDoc
	decode(t, body, &page)
	type at struct {
		step    int
		attempt int64
	}
	wantAt := []at{{0, 1}, {1, 1}, {1, 2}, {2, 1}}
	if len(page.Requests) != len(wantAt) || page.NextFrom != nil || page.Badge != "" {
		t.Fatalf("requests = %d rows, next_from %v, badge %q; want 4 rows, the last page, no badge", len(page.Requests), page.NextFrom, page.Badge)
	}
	prompts, catalogs := map[string]bool{}, map[string]bool{}
	for i, r := range page.Requests {
		if r.Index != int64(i) || (at{r.Step, r.Attempt}) != wantAt[i] || r.Content != "" {
			t.Errorf("request %d = index %d step %d attempt %d content %q, want %+v as emitted", i, r.Index, r.Step, r.Attempt, r.Content, wantAt[i])
		}
		var p struct {
			Hash, Text, Badge string
		}
		var c struct {
			Hash  string
			Tools []struct{ Name string }
			Badge string
		}
		decode(t, string(r.PromptRaw), &p)
		decode(t, string(r.ToolsRaw), &c)
		if p.Hash != r.SystemHash || !strings.HasPrefix(p.Text, "You are a support agent.") || p.Badge != "" {
			t.Errorf("request %d prompt = %+v, want the resolved text of %s", i, p, r.SystemHash)
		}
		if c.Hash != r.CatalogHash || len(c.Tools) == 0 || c.Badge != "" {
			t.Errorf("request %d tools = %+v, want the resolved catalog of %s", i, c, r.CatalogHash)
		}
		prompts[r.SystemHash], catalogs[r.CatalogHash] = true, true
	}
	if len(prompts) != 2 || len(catalogs) != 2 {
		t.Errorf("distinct prompts %d, catalogs %d; want 2 and 2 (the PrepareStep rewrite, the ToolSource growth)", len(prompts), len(catalogs))
	}
	if page.Requests[1].SystemHash == page.Requests[0].SystemHash || page.Requests[3].SystemHash != page.Requests[0].SystemHash {
		t.Error("step 1's rewrite should name its own prompt, step 2 the original")
	}

	// refs=1: hashes only, no inline prompt or tools.
	var refs requestsDoc
	decode(t, fetchJSON(t, ts, "/api/runs/r_req/requests?refs=1", nil), &refs)
	if len(refs.Requests) != 4 {
		t.Fatalf("refs=1 = %d rows, want 4", len(refs.Requests))
	}
	for i, r := range refs.Requests {
		if r.PromptRaw != nil || r.ToolsRaw != nil || r.SystemHash == "" || r.CatalogHash == "" {
			t.Errorf("refs=1 request %d inlines prompt %s tools %s (or lost its hashes)", i, r.PromptRaw, r.ToolsRaw)
		}
	}

	// Paging: limit=2 → two full pages, next_from on each but the last.
	var p1, p2, p3 requestsDoc
	decode(t, fetchJSON(t, ts, "/api/runs/r_req/requests?limit=2&refs=1", nil), &p1)
	if len(p1.Requests) != 2 || p1.NextFrom == nil || *p1.NextFrom != 2 {
		t.Fatalf("page 1 = %d rows, next_from %v; want 2 rows, next_from 2", len(p1.Requests), p1.NextFrom)
	}
	decode(t, fetchJSON(t, ts, "/api/runs/r_req/requests?limit=2&refs=1&from=2", nil), &p2)
	if len(p2.Requests) != 2 || p2.Requests[0].Index != 2 || p2.NextFrom == nil || *p2.NextFrom != 4 {
		t.Fatalf("page 2 = %d rows, next_from %v; want indexes 2-3, next_from 4", len(p2.Requests), p2.NextFrom)
	}
	decode(t, fetchJSON(t, ts, "/api/runs/r_req/requests?limit=2&refs=1&from=4", nil), &p3)
	if len(p3.Requests) != 0 || p3.NextFrom != nil {
		t.Errorf("page 3 = %d rows, next_from %v; want the empty last page", len(p3.Requests), p3.NextFrom)
	}
	var short requestsDoc
	decode(t, fetchJSON(t, ts, "/api/runs/r_req/requests?limit=3&from=2", nil), &short)
	if len(short.Requests) != 2 || short.NextFrom != nil {
		t.Errorf("a short page = %d rows, next_from %v; want 2 rows and no next_from", len(short.Requests), short.NextFrom)
	}

	// step=: one step's attempts.
	var s1 requestsDoc
	decode(t, fetchJSON(t, ts, "/api/runs/r_req/requests?step=1", nil), &s1)
	if len(s1.Requests) != 2 || s1.Requests[0].Attempt != 1 || s1.Requests[1].Attempt != 2 || s1.Requests[0].Step != 1 {
		t.Errorf("step=1 = %+v, want its two attempts", s1.Requests)
	}

	// The catalogs, in index order.
	tools := fetchJSON(t, ts, "/api/runs/r_req/tools", nil)
	requestsGolden(t, "tools-ok.golden.json", tools)
	var td toolsDocT
	decode(t, tools, &td)
	if len(td.Catalogs) != 2 || len(td.Catalogs[0].Tools) != 1 || len(td.Catalogs[1].Tools) != 2 || td.Badge != "" ||
		td.Catalogs[0].Hash != doc.CatalogHash {
		t.Errorf("tools = %+v, want catalog 0 (lookup) then 1 (lookup, refund), no badge", td)
	}

	// Bad parameters are 400s in the error shape.
	for _, q := range []string{"step=x", "step=-1", "from=-2", "limit=-1", "refs=maybe"} {
		resp, err := http.Get(ts.URL + "/api/runs/r_req/requests?" + q)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), `"bad_request"`) {
			t.Errorf("?%s = %d %s, want 400", q, resp.StatusCode, b)
		}
	}
	// Setup A (no Token): this server answers the loopback Host every
	// read above used, and refuses any other, as every /api route does.
	for _, path := range []string{"/api/runs/r_req/requests", "/api/runs/r_req/tools"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Host = "evil.example"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s with a foreign Host = %d, want 403", path, resp.StatusCode)
		}
	}
	// An unknown run is a 404 on both routes.
	for _, path := range []string{"/api/runs/nope/requests", "/api/runs/nope/tools"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, resp.StatusCode)
		}
	}
}

// TestRequestsContentOff: a run recorded through a content-off
// destination keeps its request records (stripped) but no prompt or
// tools record — each inline object is {hash, badge: stripped}, and
// the tools route is an empty list with the stripped badge and its fix.
func TestRequestsContentOff(t *testing.T) {
	ts, _ := requestsServer(t)
	recordRequestsRun(t, ts.URL, "r_off", otel.NoContent())
	fetchJSON(t, ts, "/api/runs/r_off", func(b string) bool { return strings.Contains(b, `"request_count":4`) })

	body := fetchJSON(t, ts, "/api/runs/r_off/requests", nil)
	requestsGolden(t, "requests-stripped.golden.json", body)
	var page requestsDoc
	decode(t, body, &page)
	if len(page.Requests) != 4 || page.Badge != "" {
		t.Fatalf("content-off requests = %d rows, badge %q; want 4 rows", len(page.Requests), page.Badge)
	}
	for i, r := range page.Requests {
		if r.Content != string(obsdb.HoleStripped) {
			t.Errorf("request %d content = %q, want stripped", i, r.Content)
		}
		for what, raw := range map[string]json.RawMessage{"prompt": r.PromptRaw, "tools": r.ToolsRaw} {
			var ref map[string]any
			decode(t, string(raw), &ref)
			if len(ref) != 2 || ref["badge"] != "stripped" || ref["hash"] == "" {
				t.Errorf("request %d %s = %s, want {hash, badge: stripped}", i, what, raw)
			}
		}
	}

	tools := fetchJSON(t, ts, "/api/runs/r_off/tools", nil)
	requestsGolden(t, "tools-stripped.golden.json", tools)
	var td toolsDocT
	decode(t, tools, &td)
	if len(td.Catalogs) != 0 || td.Badge != "stripped" || td.Reason == "" ||
		!strings.Contains(td.Fix, "enable content for this destination: otel.Local(...)") {
		t.Errorf("content-off tools = %+v, want [] with the stripped badge, reason and fix", td)
	}
}

// TestRequestsNoModelCall: a run that recorded its instructions hash
// but made no model call (PrepareStep failed at step 0) reads an empty
// list on both routes, with no badge — ADR 0028 §10's second reading.
func TestRequestsNoModelCall(t *testing.T) {
	ts, _ := requestsServer(t)
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	agent := core.New(wefttest.Script(wefttest.Say("never")), core.Instructions("x"),
		core.PrepareStep(func(context.Context, int, core.ModelRequest) (core.ModelRequest, error) {
			return core.ModelRequest{}, errors.New("no call today")
		}),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	if _, err := agent.Generate(ctx, core.RunID("r_none"), core.Prompt("hi")); err == nil {
		t.Fatal("want the PrepareStep failure")
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	run := fetchJSON(t, ts, "/api/runs/r_none", func(b string) bool { return strings.Contains(b, `"status":"failed"`) })
	if !strings.Contains(run, `"request_count":0`) || strings.Contains(run, `"instructions_hash":""`) || strings.Contains(run, `"requests_badge"`) {
		t.Errorf("run = %s, want an instructions_hash, request_count 0 and no requests_badge", run)
	}
	if got := fetchJSON(t, ts, "/api/runs/r_none/requests", nil); strings.TrimSpace(got) != `{"requests":[]}` {
		t.Errorf("requests = %s, want {\"requests\":[]}", got)
	}
	if got := fetchJSON(t, ts, "/api/runs/r_none/tools", nil); strings.TrimSpace(got) != `{"catalogs":[]}` {
		t.Errorf("tools = %s, want {\"catalogs\":[]}", got)
	}
}

// TestRequestsNotRecorded: a file written before ADR 0028 (migrations
// 0001 and 0002 applied by hand, one run inserted) opens through 0003;
// its run reads requests_badge not_recorded, and both routes answer
// 200 with the badge, its reason and fix beside an empty list — never
// an empty list alone.
func TestRequestsNotRecorded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE obsdb_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for v, name := range []string{"0001_init.sql", "0002_experiments.sql"} {
		body, err := os.ReadFile(filepath.Join("..", "obsdb", "sqlite", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(body)); err != nil {
			t.Fatalf("migration %s: %v", name, err)
		}
		if _, err := raw.Exec(`INSERT INTO obsdb_migrations (version, applied_at) VALUES (?, '2026-10-01T00:00:00Z')`, v+1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO runs (run_id, agent, started_ns, last_seen_ns, finished_ok, steps)
		VALUES ('old1', 'support', 100, 200, 1, 2)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	srv := New(Open(path))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	run := fetchJSON(t, ts, "/api/runs/old1", nil)
	for _, want := range []string{`"instructions_hash":""`, `"catalog_hash":""`, `"request_count":0`, `"requests_badge":"not_recorded"`} {
		if !strings.Contains(run, want) {
			t.Errorf("pre-A1 run lacks %s: %s", want, run)
		}
	}
	reqs := fetchJSON(t, ts, "/api/runs/old1/requests", nil)
	requestsGolden(t, "requests-not-recorded.golden.json", reqs)
	tools := fetchJSON(t, ts, "/api/runs/old1/tools", nil)
	requestsGolden(t, "tools-not-recorded.golden.json", tools)
	var rd requestsDoc
	decode(t, reqs, &rd)
	var td toolsDocT
	decode(t, tools, &td)
	if len(rd.Requests) != 0 || rd.Badge != "not_recorded" || rd.Reason == "" || rd.Fix == "" {
		t.Errorf("pre-A1 requests = %s, want [] with not_recorded, a reason and a fix", reqs)
	}
	if len(td.Catalogs) != 0 || td.Badge != "not_recorded" || td.Reason == "" || td.Fix == "" {
		t.Errorf("pre-A1 tools = %s, want [] with not_recorded, a reason and a fix", tools)
	}
	if !strings.Contains(reqs, `"requests":[]`) || !strings.Contains(tools, `"catalogs":[]`) {
		t.Errorf("empty lists must be [], never null: %s %s", reqs, tools)
	}
}
