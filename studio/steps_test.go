package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
)

// namedModel is a scripted model with a name of its own, so a fallback
// chain's attempts say which model each one asked.
type namedModel struct {
	*wefttest.Model
	name string
}

func (m namedModel) Info() core.ModelInfo { return core.ModelInfo{Provider: "wefttest", Name: m.name} }

// recordStepsRun drives the A7 step scenario through the real pipeline
// (otel → OTLP ingest → obsdb): three steps under mw.Retry over
// mw.Fallback(glm-b), primary glm-a.
//
//   - step 0: attempts glm-a, glm-b, glm-a, glm-b — three fail
//     (stream_idle), glm-b answers with a lookup_order call;
//   - step 1: glm-a answers with a call to the research Subagent, whose
//     child run is the step's one child;
//   - step 2: a PrepareStep trims the middle of the messages (a
//     run-scope compaction view), and glm-a calls refund, which needs
//     approval: the call parks and the run ends with it pending.
func recordStepsRun(t *testing.T, url, runID string, meta map[string]string, dest ...otel.DestOption) {
	t.Helper()
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(url, "", dest...), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	prov := []core.Option{core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider())}
	type orderIn struct {
		OrderID string `json:"order_id" jsonschema:"the order"`
	}
	lookup := core.Tool("lookup_order", "Look up an order.", func(context.Context, orderIn) (string, error) {
		return "order 42 shipped", nil
	})
	refund := core.Tool("refund", "Refund an order.", func(context.Context, orderIn) (string, error) {
		return "refunded", nil
	}, core.RequireApproval())
	child := core.New(wefttest.Script(wefttest.Say("the carrier lost it")),
		append([]core.Option{core.Name("researcher"), core.Instructions("You research orders.")}, prov...)...)
	a := namedModel{wefttest.Script(
		wefttest.Fail(core.ErrStreamIdle),
		wefttest.Fail(core.ErrStreamIdle),
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"why is order 42 late?"}`, ID: "c_sub"}),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"42"}`, ID: "c_refund"}),
	), "glm-a"}
	b := namedModel{wefttest.Script(
		wefttest.Fail(core.ErrStreamIdle),
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "c_lookup"}),
	), "glm-b"}
	agent := core.New(a, append([]core.Option{
		core.Name("orders"), core.Instructions("You are a support agent."),
		lookup, refund, core.Subagent("research", "Research an order.", child),
		core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
			if step != 2 || len(req.Messages) < 4 {
				return req, nil
			}
			m := req.Messages
			req.Messages = []core.Message{m[0], core.User("summary: the order was looked up"), m[len(m)-2], m[len(m)-1]}
			return req, nil
		}),
		core.WrapModel(mw.Retry(mw.MaxRetries(3), mw.BaseDelay(0)), mw.Fallback(b)),
	}, prov...)...)
	res, err := agent.Generate(ctx, core.RunID(runID), core.Prompt("refund order 42"), core.Metadata(meta))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 || res.Pending[0].ID != "c_refund" {
		t.Fatalf("pending = %+v, want the refund parked", res.Pending)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

// stepNorm normalizes what a real run stamps differently each time —
// times, span ids, measured milliseconds — so the goldens are
// byte-stable; every field stays, with its JSON type.
var stepNorm = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`"(time|started|finished)": "[^"]*"`), `"$1": "(time)"`},
	{regexp.MustCompile(`"(id|span_id)": "[0-9a-f]{16}"`), `"$1": "(span)"`},
	{regexp.MustCompile(`"(latency_ms|ttft_ms)": [0-9]+`), `"$1": 1`},
}

func stepGolden(t *testing.T, name, body string) {
	t.Helper()
	out := pretty(t, body)
	for _, n := range stepNorm {
		out = n.re.ReplaceAllString(out, n.with)
	}
	wefttest.Golden(t, "testdata/api/"+name, []byte(out))
}

// stepDocT is the step document as a client decodes it.
type stepDocT struct {
	RunID     string  `json:"run_id"`
	Step      int     `json:"step"`
	Status    string  `json:"status"`
	Reason    string  `json:"reason"`
	Started   *string `json:"started"`
	Finished  *string `json:"finished"`
	LatencyMS int64   `json:"latency_ms"`
	Model     struct {
		Requested string `json:"requested"`
		Answered  string `json:"answered"`
	} `json:"model"`
	Request *struct {
		Index   *int64          `json:"index"`
		Attempt int64           `json:"attempt"`
		Content string          `json:"content"`
		Prompt  json.RawMessage `json:"prompt"`
		Tools   json.RawMessage `json:"tools"`
		Badge   string          `json:"badge"`
		Reason  string          `json:"reason"`
		Fix     string          `json:"fix"`
	} `json:"request"`
	Attempts []struct {
		Attempt      int64  `json:"attempt"`
		Model        string `json:"model"`
		Outcome      string `json:"outcome"`
		ErrorType    string `json:"error_type"`
		SpanID       string `json:"span_id"`
		RequestIndex *int64 `json:"request_index"`
		Badge        string `json:"badge"`
	} `json:"attempts"`
	AttemptsBadge *struct {
		Badge, Reason, Fix string
	} `json:"attempts_badge"`
	MessagesIn struct {
		Index *int64 `json:"index"`
		Count int    `json:"count"`
		Badge string `json:"badge"`
	} `json:"messages_in"`
	Events []struct {
		Pos   int64 `json:"pos"`
		Event struct {
			Type string `json:"type"`
		} `json:"event"`
	} `json:"events"`
	ToolCalls []struct {
		CallID string          `json:"call_id"`
		Name   string          `json:"name"`
		Args   json.RawMessage `json:"args"`
		Result *struct {
			Content string `json:"content"`
			Bytes   int64  `json:"bytes"`
		} `json:"result"`
		Span *struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"span"`
		ChildRunID string `json:"child_run_id"`
		Pending    bool   `json:"pending"`
		Badge      string `json:"badge"`
	} `json:"tool_calls"`
	Children []struct {
		ID     string     `json:"id"`
		CallID string     `json:"call_id"`
		Agent  string     `json:"agent"`
		Status string     `json:"status"`
		Usage  core.Usage `json:"usage"`
	} `json:"children"`
	Usage      core.Usage `json:"usage"`
	Compaction *struct {
		Scope    string `json:"scope"`
		FromSeq  int64  `json:"from_seq"`
		ToSeq    int64  `json:"to_seq"`
		Hash     string `json:"hash"`
		Badge    string `json:"badge"`
		Replaced int    `json:"replaced"`
		Entries  int    `json:"entries"`
	} `json:"compaction"`
	Holes []struct {
		Hole, Reason, Fix string
	} `json:"holes"`
}

func (d stepDocT) holes() []string {
	out := []string{}
	for _, h := range d.Holes {
		out = append(out, h.Hole)
	}
	return out
}

// TestStepRoute pins GET /api/runs/{id}/steps/{n} (plan A7, A4's
// attempts, A10's children) over a run recorded through the real
// pipeline: step 0's four attempts (glm-a, glm-b, glm-a, glm-b — three
// errors, glm-b answering) joined to their request records, step 1's
// Subagent child, step 2's compaction view and parked call. Goldens for
// all three steps; 404s past the last step; 400 for a bad ordinal.
func TestStepRoute(t *testing.T) {
	ts, _ := requestsServer(t)
	recordStepsRun(t, ts.URL, "r_steps", nil)
	fetchJSON(t, ts, "/api/runs/r_steps", func(b string) bool {
		return strings.Contains(b, `"request_count":6`) && strings.Contains(b, `"id":"r_steps/1/c_sub"`)
	})

	var docs [3]stepDocT
	for n := range docs {
		path := "/api/runs/r_steps/steps/" + string(rune('0'+n))
		body := fetchJSON(t, ts, path, nil)
		stepGolden(t, "step-"+string(rune('0'+n))+".golden.json", body)
		decode(t, body, &docs[n])
		checkHoles(t, path, docs[n])
	}

	// Step 0: every attempt with its model and outcome, joined by
	// attempt number to its request record; glm-b answered.
	s0 := docs[0]
	if s0.Status != "ok" || s0.Model.Requested != "glm-a" || s0.Model.Answered != "glm-b" || s0.Started == nil || s0.Finished == nil || s0.LatencyMS < 1 {
		t.Errorf("step 0 = status %q model %+v started %v finished %v latency %d, want ok, glm-a answered by glm-b, timed", s0.Status, s0.Model, s0.Started, s0.Finished, s0.LatencyMS)
	}
	wantModels := []string{"glm-a", "glm-b", "glm-a", "glm-b"}
	if len(s0.Attempts) != 4 || s0.AttemptsBadge != nil {
		t.Fatalf("step 0 attempts = %+v badge %+v, want 4, no badge", s0.Attempts, s0.AttemptsBadge)
	}
	for i, a := range s0.Attempts {
		wantOutcome, wantErr := "error", "stream_idle"
		if i == 3 {
			wantOutcome, wantErr = "ok", ""
		}
		if a.Attempt != int64(i+1) || a.Model != wantModels[i] || a.Outcome != wantOutcome || a.ErrorType != wantErr ||
			a.SpanID == "" || a.RequestIndex == nil || *a.RequestIndex != int64(i) {
			t.Errorf("step 0 attempt %d = %+v, want model %s, %s %s, a span, request %d", i+1, a, wantModels[i], wantOutcome, wantErr, i)
		}
	}
	if s0.Request == nil || s0.Request.Index == nil || *s0.Request.Index != 0 || s0.Request.Attempt != 1 || s0.Request.Badge != "" ||
		!strings.Contains(string(s0.Request.Prompt), "You are a support agent.") || !strings.Contains(string(s0.Request.Tools), `"lookup_order"`) {
		t.Errorf("step 0 request = %+v, want attempt 1's row with the prompt and catalog inline", s0.Request)
	}
	if s0.MessagesIn.Count != 1 || s0.MessagesIn.Index == nil || *s0.MessagesIn.Index != 0 || s0.MessagesIn.Badge != "" {
		t.Errorf("step 0 messages_in = %+v, want index 0, count 1", s0.MessagesIn)
	}
	if len(s0.Events) != 4 || s0.Events[0].Event.Type != "step_start" || s0.Events[3].Event.Type != "step_finish" || s0.Events[0].Pos != 1 {
		t.Errorf("step 0 events = %+v, want step_start..step_finish from pos 1", s0.Events)
	}
	if len(s0.ToolCalls) != 1 || s0.ToolCalls[0].CallID != "c_lookup" || s0.ToolCalls[0].Result == nil ||
		s0.ToolCalls[0].Result.Content != "order 42 shipped" || s0.ToolCalls[0].Span == nil || s0.ToolCalls[0].Span.Status != "ok" {
		t.Errorf("step 0 tool calls = %+v, want c_lookup with its result and span", s0.ToolCalls)
	}
	if len(s0.Children) != 0 || s0.Compaction != nil || len(s0.Holes) != 0 || s0.Usage.InputTokens != 10 {
		t.Errorf("step 0 children %+v compaction %+v holes %v usage %+v, want none, none, none, 10 in", s0.Children, s0.Compaction, s0.holes(), s0.Usage)
	}

	// Step 1: the Subagent call and its child run, nested.
	s1 := docs[1]
	if len(s1.Children) != 1 || s1.Children[0].ID != "r_steps/1/c_sub" || s1.Children[0].CallID != "c_sub" ||
		s1.Children[0].Agent != "researcher" || s1.Children[0].Status != "succeeded" || s1.Children[0].Usage.InputTokens != 10 {
		t.Errorf("step 1 children = %+v, want the researcher's run", s1.Children)
	}
	if len(s1.ToolCalls) != 1 || s1.ToolCalls[0].ChildRunID != "r_steps/1/c_sub" || len(s1.Attempts) != 1 || s1.Attempts[0].Outcome != "ok" {
		t.Errorf("step 1 tool calls %+v attempts %+v, want the call naming its child, one ok attempt", s1.ToolCalls, s1.Attempts)
	}

	// Step 2: the compaction view, and the parked call.
	s2 := docs[2]
	if s2.Status != "parked" || len(s2.ToolCalls) != 1 || !s2.ToolCalls[0].Pending || s2.ToolCalls[0].Result != nil {
		t.Errorf("step 2 = status %q calls %+v, want parked with the refund pending", s2.Status, s2.ToolCalls)
	}
	if s2.Compaction == nil || s2.Compaction.Scope != "run" || s2.Compaction.Badge != "compacted" || s2.Compaction.Hash == "" || s2.Compaction.Replaced != 2 {
		t.Errorf("step 2 compaction = %+v, want the run-scope view, badged", s2.Compaction)
	}
	if s2.MessagesIn.Badge != "compacted" || s2.MessagesIn.Count != 4 || strings.Join(s2.holes(), ",") != "compacted" {
		t.Errorf("step 2 messages_in %+v holes %v, want the compacted view of 4 and the compacted hole", s2.MessagesIn, s2.holes())
	}
	for _, e := range s2.Events {
		if e.Event.Type == "run_finish" {
			t.Error("step 2's events include the run's run_finish")
		}
	}

	// Past the last step, a malformed ordinal, an unknown run.
	for path, want := range map[string]int{
		"/api/runs/r_steps/steps/3":         http.StatusNotFound,
		"/api/runs/r_steps/steps/99":        http.StatusNotFound,
		"/api/runs/nope/steps/0":            http.StatusNotFound,
		"/api/runs/r_steps/steps/x":         http.StatusBadRequest,
		"/api/runs/r_steps/steps/-1":        http.StatusBadRequest,
		"/api/runs/r_steps/steps/01":        http.StatusBadRequest,
		"/api/runs/r_steps/1/c_sub/steps/0": http.StatusOK,
		"/api/runs/r_steps/1/c_sub/steps/1": http.StatusNotFound,
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != want || (want != http.StatusOK && !strings.Contains(string(b), `"error":{"code":`)) {
			t.Errorf("%s = %d %s, want %d in the error shape", path, resp.StatusCode, b, want)
		}
	}
	// Setup A: the loopback Host only.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs/r_steps/steps/0", nil)
	req.Host = "evil.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("steps with a foreign Host = %d, want 403", resp.StatusCode)
	}
}

// TestStepRunning: a run still inside step 1 answers step 1 running —
// no step_finish, no usage yet — and its not-yet-started step 2 is 404.
func TestStepRunning(t *testing.T) {
	ts, srv := requestsServer(t)
	now := time.Now().UTC()
	rec := func(pos int64, body string) obsdb.Record {
		var h struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(body), &h)
		return obsdb.Record{
			Time: now.Add(time.Duration(pos) * time.Millisecond), EventName: "weft.event", Severity: 9, Body: body, Service: "svc",
			Attrs: map[string]any{"weft.record": "event", "weft.run.id": "r_live", "gen_ai.agent.name": "a",
				"weft.event.type": h.Type, "weft.event.pos": pos},
			Resource: map[string]any{"service.name": "svc"},
		}
	}
	if err := srv.db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{
		rec(0, `{"type":"run_start","id":"r_live","model":{"provider":"p","name":"m"},"agent":"a"}`),
		rec(1, `{"type":"step_start","run_id":"r_live","index":0}`),
		rec(2, `{"type":"step_finish","run_id":"r_live","index":0,"reason":"tool_calls","usage":{"input_tokens":3,"output_tokens":1}}`),
		rec(3, `{"type":"step_start","run_id":"r_live","index":1}`),
	}}); err != nil {
		t.Fatal(err)
	}
	var d stepDocT
	decode(t, fetchJSON(t, ts, "/api/runs/r_live/steps/1", nil), &d)
	if d.Status != "running" || d.Finished != nil || d.Usage != (core.Usage{}) || len(d.Events) != 1 {
		t.Errorf("running step = %+v, want running, unfinished, one event", d)
	}
	var d0 stepDocT
	decode(t, fetchJSON(t, ts, "/api/runs/r_live/steps/0", nil), &d0)
	if d0.Status != "ok" || d0.Usage.InputTokens != 3 || len(d0.Events) != 2 {
		t.Errorf("finished step 0 of a running run = %+v, want ok with its usage", d0)
	}
	resp, err := http.Get(ts.URL + "/api/runs/r_live/steps/2")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a running run's next step = %d, want 404", resp.StatusCode)
	}
}

// TestStepContentOff: a run recorded through a content-off destination
// — the request block is the stripped row (prompt and catalog {hash,
// badge: stripped}), messages_in keeps its count under the stripped
// badge, tool calls carry the stripped badge, and everything else —
// attempts, the answering model, children, usage, timing — is present.
func TestStepContentOff(t *testing.T) {
	ts, _ := requestsServer(t)
	recordStepsRun(t, ts.URL, "r_off", nil, otel.NoContent())
	fetchJSON(t, ts, "/api/runs/r_off", func(b string) bool {
		return strings.Contains(b, `"request_count":6`) && strings.Contains(b, `"id":"r_off/1/c_sub"`)
	})
	body := fetchJSON(t, ts, "/api/runs/r_off/steps/0", nil)
	stepGolden(t, "step-stripped.golden.json", body)
	var d stepDocT
	decode(t, body, &d)
	checkHoles(t, "content-off", d)
	if d.Request == nil || d.Request.Content != "stripped" || !strings.Contains(string(d.Request.Prompt), `"badge":"stripped"`) ||
		!strings.Contains(string(d.Request.Tools), `"badge":"stripped"`) {
		t.Errorf("content-off request = %+v, want the stripped row", d.Request)
	}
	if d.MessagesIn.Badge != "stripped" || d.MessagesIn.Count != 1 || d.MessagesIn.Index != nil {
		t.Errorf("content-off messages_in = %+v, want count 1 under the stripped badge, no index", d.MessagesIn)
	}
	if len(d.Attempts) != 4 || d.Attempts[3].Outcome != "ok" || d.Model.Answered != "glm-b" || d.Status != "ok" || d.Usage.InputTokens != 10 {
		t.Errorf("content-off step 0 = attempts %+v model %+v status %q usage %+v, want everything but content", d.Attempts, d.Model, d.Status, d.Usage)
	}
	if len(d.ToolCalls) != 1 || d.ToolCalls[0].Badge != "stripped" || d.ToolCalls[0].Result == nil || d.ToolCalls[0].Result.Bytes != 16 {
		t.Errorf("content-off tool calls = %+v, want the stripped badge and the span's byte count", d.ToolCalls)
	}
	if got := strings.Join(d.holes(), ","); got != "stripped" {
		t.Errorf("content-off holes = %s, want stripped", got)
	}
	var d1 stepDocT
	decode(t, fetchJSON(t, ts, "/api/runs/r_off/steps/1", nil), &d1)
	if len(d1.Children) != 1 || d1.Children[0].ID != "r_off/1/c_sub" {
		t.Errorf("content-off step 1 children = %+v, want the child", d1.Children)
	}
}

// TestStepNotRecorded: the file written before ADR 0028 (migrations
// 0001 and 0002 by hand, one run row, no records or spans): step 0 is
// there by the run's step count, its request and attempts are
// not_recorded, its missing events a gap, its model the run's
// (derived) — every hole listed, never an empty block alone.
func TestStepNotRecorded(t *testing.T) {
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

	body := fetchJSON(t, ts, "/api/runs/old1/steps/0", nil)
	stepGolden(t, "step-not-recorded.golden.json", body)
	var d stepDocT
	decode(t, body, &d)
	if d.Request == nil || d.Request.Badge != "not_recorded" || d.Request.Reason == "" || d.Request.Fix == "" {
		t.Errorf("pre-A1 request = %+v, want the not_recorded badge with its reason and fix", d.Request)
	}
	if d.AttemptsBadge == nil || d.AttemptsBadge.Badge != "not_recorded" || len(d.Attempts) != 0 {
		t.Errorf("pre-A1 attempts = %+v badge %+v, want [] under not_recorded", d.Attempts, d.AttemptsBadge)
	}
	if d.MessagesIn.Badge != "not_recorded" {
		t.Errorf("pre-A1 messages_in = %+v, want not_recorded", d.MessagesIn)
	}
	if got := strings.Join(d.holes(), ","); got != "gap,not_recorded,derived" {
		t.Errorf("pre-A1 holes = %s, want gap,not_recorded,derived", got)
	}
	checkHoles(t, "pre-A1", d)
	if !strings.Contains(body, `"attempts":[]`) || !strings.Contains(body, `"events":[]`) || !strings.Contains(body, `"tool_calls":[]`) || !strings.Contains(body, `"children":[]`) {
		t.Errorf("empty lists must be [], never null: %s", body)
	}
	resp, err := http.Get(ts.URL + "/api/runs/old1/steps/2")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a step past the run's count = %d, want 404", resp.StatusCode)
	}
}

// TestStepPanelTokens: a read-scoped panel token reads the whole step
// — attempts, events, tool calls, children — with the request block
// replaced by {badge: hidden, reason, fix} (never a 403 on the step);
// a playground-scoped token and the server token read the request.
func TestStepPanelTokens(t *testing.T) {
	const tok = "srv-token"
	srv := New(Open(filepath.Join(t.TempDir(), "weft.db")), Token(tok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	recordStepsRun(t, ts.URL, "r_tok", map[string]string{"weft.public_id": "pub_a"})

	get := func(bearer string) (int, string) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/runs/r_tok/steps/1", nil)
			req.Header.Set("Authorization", "Bearer "+bearer)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if (resp.StatusCode == http.StatusOK && strings.Contains(string(b), `"r_tok/1/c_sub"`)) || time.Now().After(deadline) {
				return resp.StatusCode, string(b)
			}
		}
	}
	sign := func(scope string) string {
		s, err := signPanelToken([]byte(tok), panelClaims{PublicID: "pub_a", Scope: scope, Exp: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	code, body := get(sign(scopeRead))
	if code != http.StatusOK {
		t.Fatalf("read token = %d %s, want 200", code, body)
	}
	stepGolden(t, "step-hidden.golden.json", body)
	var d stepDocT
	decode(t, body, &d)
	checkHoles(t, "read token", d)
	if d.Request == nil || d.Request.Badge != "hidden" || d.Request.Fix != "use a playground-scoped token" || d.Request.Prompt != nil || strings.Contains(body, "You are a support agent.") {
		t.Errorf("read token request = %+v, want the hidden badge and no system prompt", d.Request)
	}
	if len(d.Attempts) != 1 || len(d.Events) == 0 || len(d.ToolCalls) != 1 || len(d.Children) != 1 || d.MessagesIn.Count != 3 {
		t.Errorf("read token step = %+v, want everything but the request", d)
	}
	if got := strings.Join(d.holes(), ","); got != "hidden" {
		t.Errorf("read token holes = %s, want hidden", got)
	}
	for _, bearer := range []string{sign(scopePlayground), tok} {
		code, body := get(bearer)
		var d stepDocT
		decode(t, body, &d)
		if code != http.StatusOK || d.Request == nil || d.Request.Badge != "" || !strings.Contains(string(d.Request.Prompt), "You are a support agent.") {
			t.Errorf("a prompt-reading identity = %d request %+v, want the row with the prompt", code, d.Request)
		}
	}
}

// withPipeline runs fn with an agent's observability options wired to
// a fresh pipeline into url (traced: the tracer too), then flushes.
func withPipeline(t *testing.T, url string, traced bool, fn func(prov []core.Option), dest ...otel.DestOption) {
	t.Helper()
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(url, "", dest...), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	prov := []core.Option{core.LoggerProvider(p.LoggerProvider())}
	if traced {
		prov = append(prov, core.TracerProvider(p.TracerProvider()))
	}
	fn(prov)
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

// checkHoles fails on a listed hole without a reason: no hole ever
// goes out unexplained.
func checkHoles(t *testing.T, where string, d stepDocT) {
	t.Helper()
	for _, h := range d.Holes {
		if h.Reason == "" {
			t.Errorf("%s: hole %s has no reason", where, h.Hole)
		}
	}
}

func getStep(t *testing.T, ts *httptest.Server, path string, cond func(string) bool) stepDocT {
	t.Helper()
	var d stepDocT
	decode(t, fetchJSON(t, ts, path, cond), &d)
	checkHoles(t, path, d)
	return d
}

// TestStepMaxTokensCalls: a step that hit the output token limit with
// tool calls runs none of them (rule 11) — no tool events, no spans —
// yet the step lists each call from its transcript batch with the
// "not executed" result and the max_tokens badge; content-off, the
// stripped badge.
func TestStepMaxTokensCalls(t *testing.T) {
	ts, _ := requestsServer(t)
	run := func(id string, dest ...otel.DestOption) {
		withPipeline(t, ts.URL, true, func(prov []core.Option) {
			touch := core.Tool("touch", "Touch.", func(context.Context, struct{}) (string, error) { return "ok", nil })
			agent := core.New(wefttest.Script(
				wefttest.Raw(
					core.ModelToolCall{ID: "c1", Name: "touch", Args: json.RawMessage(`{}`)},
					core.ModelToolCall{ID: "c2", Name: "touch", Args: json.RawMessage(`{}`)},
					core.ModelFinish{Reason: core.StopMaxTokens, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
				),
				wefttest.Say("recovered"),
			), append([]core.Option{touch}, prov...)...)
			if _, err := agent.Generate(context.Background(), core.RunID(id), core.Prompt("go")); err != nil {
				t.Fatal(err)
			}
		}, dest...)
	}
	run("r_max")
	run("r_max_off", otel.NoContent())
	for _, c := range []struct{ id, badge string }{{"r_max", "max_tokens"}, {"r_max_off", "stripped"}} {
		d := getStep(t, ts, "/api/runs/"+c.id+"/steps/0", func(b string) bool { return strings.Contains(b, `"step_finish"`) })
		if d.Reason != "max_tokens" || !slices.Contains(d.holes(), "max_tokens") {
			t.Errorf("%s step 0 = reason %q holes %v, want max_tokens", c.id, d.Reason, d.holes())
		}
		if c.badge == "stripped" {
			// A content-off chain stores no messages: the calls' ids and
			// arguments are unknown, the chat span counts them — two
			// entries, stripped.
			if !slices.Contains(d.holes(), "stripped") || len(d.ToolCalls) != 2 {
				t.Errorf("%s holes = %v calls %+v, want stripped and two calls", c.id, d.holes(), d.ToolCalls)
			}
			for _, tc := range d.ToolCalls {
				if tc.Badge != "stripped" || tc.CallID != "" || tc.Result != nil {
					t.Errorf("%s call %+v, want an anonymous stripped call", c.id, tc)
				}
			}
			continue
		}
		if len(d.ToolCalls) != 2 {
			t.Fatalf("%s tool calls = %+v, want c1 and c2", c.id, d.ToolCalls)
		}
		for i, tc := range d.ToolCalls {
			if tc.CallID != []string{"c1", "c2"}[i] || tc.Badge != c.badge || tc.Span != nil || tc.Result == nil ||
				tc.Result.Content != "tool call touch was not executed: the response hit the output token limit" {
				t.Errorf("%s call %d = %+v, want the not-executed result badged %s", c.id, i, tc, c.badge)
			}
		}
	}
}

// handRec is one hand-written record: an event (its type read from the
// body) or a request record, at pos, with extra attributes.
type handRec struct {
	kind  string // "event" | "request"
	pos   int64
	body  string
	extra map[string]any
}

// writeHand stores hand-written records (and spans) for one run, all
// stamped at t0 + pos ms with attrs on every record — the shapes the
// real producer writes, minus what a test means to drop.
func writeHand(t *testing.T, db obsdb.DB, runID string, t0 time.Time, attrs map[string]any, recs []handRec, spans ...obsdb.Span) {
	t.Helper()
	var out []obsdb.Record
	for _, r := range recs {
		a := map[string]any{"weft.record": r.kind, "weft.run.id": runID, "gen_ai.agent.name": "hand"}
		switch r.kind {
		case "event":
			var h struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal([]byte(r.body), &h)
			a["weft.event.type"], a["weft.event.pos"] = h.Type, r.pos
		case "request":
			a["weft.request.index"] = r.pos
		}
		for k, v := range attrs {
			a[k] = v
		}
		for k, v := range r.extra {
			a[k] = v
		}
		out = append(out, obsdb.Record{
			Time: t0.Add(time.Duration(r.pos) * time.Millisecond), EventName: "weft." + r.kind, Severity: 9,
			Body: r.body, Service: "svc", Attrs: a, Resource: map[string]any{"service.name": "svc"},
		})
	}
	for i := range spans {
		spans[i].Attrs["weft.run.id"] = runID
		if spans[i].TraceID == "" {
			spans[i].TraceID = "abababababababababababababababab"
		}
		if spans[i].Start.IsZero() {
			spans[i].Start, spans[i].End = t0, t0.Add(time.Millisecond)
		}
		spans[i].Service, spans[i].Resource = "svc", map[string]any{"service.name": "svc"}
	}
	if err := db.Write(context.Background(), obsdb.Batch{Records: out, Spans: spans}); err != nil {
		t.Fatal(err)
	}
}

func ev(pos int64, body string) handRec { return handRec{kind: "event", pos: pos, body: body} }

// TestStepReadTokenNotRecorded: under a read-scoped token the request
// block is hidden, and the run predates the request record (run_start
// without weft.instructions.hash) — messages_in's not_recorded badge
// still reaches holes beside hidden.
func TestStepReadTokenNotRecorded(t *testing.T) {
	const tok = "srv-token"
	srv := New(Open(filepath.Join(t.TempDir(), "weft.db")), Token(tok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	writeHand(t, srv.db, "r_old", time.Now().UTC(), map[string]any{"weft.public_id": "pub_a"}, []handRec{
		ev(0, `{"type":"run_start","id":"r_old","model":{"provider":"p","name":"m"},"agent":"hand"}`),
		ev(1, `{"type":"step_start","run_id":"r_old","index":0}`),
		ev(2, `{"type":"step_finish","run_id":"r_old","index":0,"reason":"stop","usage":{"input_tokens":1,"output_tokens":1}}`),
		ev(3, `{"type":"run_finish","run_id":"r_old","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`),
	})
	read, err := signPanelToken([]byte(tok), panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	var d stepDocT
	decode(t, fetchJSON(t, ts, "/api/runs/r_old/steps/0?token="+read, nil), &d)
	checkHoles(t, "read token", d)
	if d.Request == nil || d.Request.Badge != "hidden" || d.MessagesIn.Badge != "not_recorded" {
		t.Errorf("request %+v messages_in %+v, want hidden and not_recorded", d.Request, d.MessagesIn)
	}
	if got := d.holes(); !slices.Contains(got, "hidden") || !slices.Contains(got, "not_recorded") {
		t.Errorf("holes = %v, want hidden and not_recorded", got)
	}
}

// TestStepLogsOnly: a run recorded without a tracer has its events and
// requests but no spans — the attempts block says not_recorded with the
// fix to install a tracer (not to upgrade weft), and the answering
// model, read from the chat span, is absent.
func TestStepLogsOnly(t *testing.T) {
	ts, _ := requestsServer(t)
	withPipeline(t, ts.URL, false, func(prov []core.Option) {
		agent := core.New(wefttest.Script(wefttest.Say("hi")), append([]core.Option{core.Instructions("x")}, prov...)...)
		if _, err := agent.Generate(context.Background(), core.RunID("r_logs"), core.Prompt("go")); err != nil {
			t.Fatal(err)
		}
	})
	d := getStep(t, ts, "/api/runs/r_logs/steps/0", func(b string) bool { return strings.Contains(b, `"step_finish"`) })
	if d.AttemptsBadge == nil || d.AttemptsBadge.Badge != "not_recorded" || d.AttemptsBadge.Fix != "install a tracer (otel.Install records spans)" {
		t.Errorf("logs-only attempts_badge = %+v, want not_recorded with the tracer fix", d.AttemptsBadge)
	}
	if d.Model.Answered != "" || d.Model.Requested != "script" || len(d.Attempts) != 1 || d.Attempts[0].Outcome != "" {
		t.Errorf("logs-only model %+v attempts %+v, want requested only and one attempt without an outcome", d.Model, d.Attempts)
	}
}

// TestStepAttemptEdges: a request record whose body did not parse is
// still an attempt, badged derived, so the count agrees with the
// requests route. The step's only record, unnumbered, joins the first
// attempt the spans time (one call is never two attempts); an attempt
// span with no request record beside it puts gap on attempts_badge.
// With the chat span alone, the record is attempt 1 and nothing is a
// gap.
func TestStepAttemptEdges(t *testing.T) {
	ts, srv := requestsServer(t)
	chat := obsdb.Span{SpanID: "c000000000000001", Name: "chat m", Kind: 3, StatusCode: 1,
		Attrs: map[string]any{"gen_ai.operation.name": "chat", "weft.step.index": int64(0), "weft.stream": true, "gen_ai.request.model": "m"}}
	att := func(id string, n int64, status int) obsdb.Span {
		return obsdb.Span{SpanID: id, ParentSpanID: chat.SpanID, Name: "attempt", Kind: 3, StatusCode: status,
			Attrs: map[string]any{"weft.step.index": int64(0), "weft.attempt.index": n, "gen_ai.request.model": "m"}}
	}
	writeHand(t, srv.db, "r_edge", time.Now().UTC(), map[string]any{}, []handRec{
		{kind: "event", pos: 0, body: `{"type":"run_start","id":"r_edge","model":{"provider":"p","name":"m"},"agent":"hand"}`,
			extra: map[string]any{"weft.instructions.hash": "aa"}},
		ev(1, `{"type":"step_start","run_id":"r_edge","index":0}`),
		{kind: "request", pos: 0, body: `not json`, extra: map[string]any{"weft.step.index": int64(0)}},
		ev(2, `{"type":"step_finish","run_id":"r_edge","index":0,"reason":"stop","usage":{"input_tokens":1,"output_tokens":1}}`),
		ev(3, `{"type":"run_finish","run_id":"r_edge","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`),
	}, chat, att("a000000000000001", 1, 2), att("a000000000000002", 2, 1))
	var reqs requestsDoc
	decode(t, fetchJSON(t, ts, "/api/runs/r_edge/requests", nil), &reqs)
	d := getStep(t, ts, "/api/runs/r_edge/steps/0", nil)
	if len(d.Attempts) != 2 || len(reqs.Requests) != 1 {
		t.Fatalf("attempts = %+v (requests route: %d rows), want spans 1 and 2, the record joined to 1", d.Attempts, len(reqs.Requests))
	}
	first := d.Attempts[0]
	if first.Attempt != 1 || first.RequestIndex == nil || *first.RequestIndex != 0 || first.Badge != "derived" || first.Outcome != "error" {
		t.Errorf("attempt 1 = %+v, want the unnumbered record joined to span 1: request 0, derived, error", first)
	}
	if d.Attempts[1].Attempt != 2 || d.Attempts[1].Outcome != "ok" || d.Attempts[1].RequestIndex != nil {
		t.Errorf("attempt 2 = %+v, want span 2's ok, no request", d.Attempts[1])
	}
	if d.AttemptsBadge == nil || d.AttemptsBadge.Badge != "gap" || !slices.Contains(d.holes(), "derived") || !slices.Contains(d.holes(), "gap") {
		t.Errorf("attempts_badge %+v holes %v, want gap (span 2 has no record), and derived listed", d.AttemptsBadge, d.holes())
	}

	// The chat span alone: the unnumbered record is attempt 1, timed by
	// the chat span — one attempt, no gap.
	chat1 := obsdb.Span{SpanID: "c000000000000002", Name: "chat m", Kind: 3, StatusCode: 1,
		Attrs: map[string]any{"gen_ai.operation.name": "chat", "weft.step.index": int64(0), "weft.stream": true, "gen_ai.request.model": "m"}}
	writeHand(t, srv.db, "r_edge1", time.Now().UTC(), map[string]any{}, []handRec{
		{kind: "event", pos: 0, body: `{"type":"run_start","id":"r_edge1","model":{"provider":"p","name":"m"},"agent":"hand"}`,
			extra: map[string]any{"weft.instructions.hash": "aa"}},
		ev(1, `{"type":"step_start","run_id":"r_edge1","index":0}`),
		{kind: "request", pos: 0, body: `not json`, extra: map[string]any{"weft.step.index": int64(0)}},
		ev(2, `{"type":"step_finish","run_id":"r_edge1","index":0,"reason":"stop","usage":{"input_tokens":1,"output_tokens":1}}`),
		ev(3, `{"type":"run_finish","run_id":"r_edge1","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`),
	}, chat1)
	d1 := getStep(t, ts, "/api/runs/r_edge1/steps/0", nil)
	if len(d1.Attempts) != 1 || d1.Attempts[0].Attempt != 1 || d1.Attempts[0].SpanID != chat1.SpanID ||
		d1.Attempts[0].RequestIndex == nil || d1.Attempts[0].Badge != "derived" {
		t.Errorf("chat-span attempts = %+v, want one: attempt 1, the chat span, request 0, derived", d1.Attempts)
	}
	if d1.AttemptsBadge != nil || slices.Contains(d1.holes(), "gap") || !slices.Contains(d1.holes(), "derived") {
		t.Errorf("chat-span attempts_badge %+v holes %v, want no badge, no gap, derived listed", d1.AttemptsBadge, d1.holes())
	}
}

// TestStepLostStepStart: step 1's step_start was dropped. Step 0 ends
// at the first event past its step_finish (step 1's tool call never
// folds into it) and lists the gap; step 1 still answers, from the
// run's count, with the gap hole.
func TestStepLostStepStart(t *testing.T) {
	ts, srv := requestsServer(t)
	writeHand(t, srv.db, "r_lost", time.Now().UTC(), map[string]any{}, []handRec{
		ev(0, `{"type":"run_start","id":"r_lost","model":{"provider":"p","name":"m"},"agent":"hand"}`),
		ev(1, `{"type":"step_start","run_id":"r_lost","index":0}`),
		ev(2, `{"type":"tool_start","run_id":"r_lost","seq":1,"call_id":"c_a","name":"t","args":{}}`),
		ev(3, `{"type":"tool_finish","run_id":"r_lost","seq":2,"call_id":"c_a","name":"t","content":"a","is_error":false}`),
		ev(4, `{"type":"step_finish","run_id":"r_lost","index":0,"reason":"tool_calls","usage":{"input_tokens":1,"output_tokens":1}}`),
		// pos 5, step 1's step_start, never arrived
		ev(6, `{"type":"tool_start","run_id":"r_lost","seq":3,"call_id":"c_b","name":"t","args":{}}`),
		ev(7, `{"type":"tool_finish","run_id":"r_lost","seq":4,"call_id":"c_b","name":"t","content":"b","is_error":false}`),
		ev(8, `{"type":"step_finish","run_id":"r_lost","index":1,"reason":"stop","usage":{"input_tokens":1,"output_tokens":1}}`),
		ev(9, `{"type":"run_finish","run_id":"r_lost","usage":{"input_tokens":2,"output_tokens":2},"steps":2}`),
	})
	d0 := getStep(t, ts, "/api/runs/r_lost/steps/0", nil)
	if len(d0.Events) != 4 || len(d0.ToolCalls) != 1 || d0.ToolCalls[0].CallID != "c_a" || !slices.Contains(d0.holes(), "gap") {
		t.Errorf("step 0 = %d events, calls %+v, holes %v; want its 4 events, c_a alone, and the gap", len(d0.Events), d0.ToolCalls, d0.holes())
	}
	d1 := getStep(t, ts, "/api/runs/r_lost/steps/1", nil)
	if !slices.Contains(d1.holes(), "gap") {
		t.Errorf("step 1 holes = %v, want gap", d1.holes())
	}
}

// TestStepInterruptedAndPreA4: a run that stopped reporting inside step
// 0 reads status error with the interrupted hole; its chat span has no
// weft.stream and no attempt spans (a weft before A4), so attempts come
// from the chat span under the not_recorded badge.
func TestStepInterruptedAndPreA4(t *testing.T) {
	ts, srv := requestsServer(t)
	chat := obsdb.Span{SpanID: "c000000000000009", Name: "chat m", Kind: 3, StatusCode: 1,
		Attrs: map[string]any{"gen_ai.operation.name": "chat", "weft.step.index": int64(0), "gen_ai.request.model": "m"}}
	writeHand(t, srv.db, "r_int", time.Now().UTC().Add(-2*time.Minute), map[string]any{}, []handRec{
		ev(0, `{"type":"run_start","id":"r_int","model":{"provider":"p","name":"m"},"agent":"hand"}`),
		ev(1, `{"type":"step_start","run_id":"r_int","index":0}`),
	}, chat)
	d := getStep(t, ts, "/api/runs/r_int/steps/0", nil)
	if d.Status != "error" || !slices.Contains(d.holes(), "interrupted") {
		t.Errorf("interrupted step = status %q holes %v, want error with interrupted", d.Status, d.holes())
	}
	if d.AttemptsBadge == nil || d.AttemptsBadge.Badge != "not_recorded" || len(d.Attempts) != 1 || d.Attempts[0].SpanID != chat.SpanID {
		t.Errorf("pre-A4 attempts = %+v badge %+v, want attempt 1 from the chat span under not_recorded", d.Attempts, d.AttemptsBadge)
	}
}

// TestStepRealEdges, through the real pipeline: a capped tool result
// reads truncated with the hole; a steer delivered after step 0 is
// filed under step 0; a resume run's approved call, executed before its
// step 0's step_start, is step 0's.
func TestStepRealEdges(t *testing.T) {
	ts, _ := requestsServer(t)
	withPipeline(t, ts.URL, true, func(prov []core.Option) {
		big := core.Tool("big", "Big.", func(context.Context, struct{}) (string, error) { return strings.Repeat("x", 100), nil },
			core.MaxResultBytes(10))
		refund := core.Tool("refund", "Refund.", func(context.Context, struct{}) (string, error) { return "refunded", nil },
			core.RequireApproval())
		agent := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "big", Args: `{}`, ID: "c_big"}),
			wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{}`, ID: "c_ref"}),
			wefttest.Say("done"),
		), append([]core.Option{big, refund}, prov...)...)
		first, err := agent.Generate(context.Background(), core.RunID("r_edges"), core.Prompt("go"),
			wefttest.NewSteers().At(0, core.User("and refund it")).Option())
		if err != nil || len(first.Pending) != 1 {
			t.Fatalf("first run: %v, pending %+v", err, first.Pending)
		}
		if _, err := agent.Generate(context.Background(), core.RunID("r_resume"), core.Messages(first.Messages...), core.Approve("c_ref")); err != nil {
			t.Fatal(err)
		}
	})
	d := getStep(t, ts, "/api/runs/r_edges/steps/0", func(b string) bool { return strings.Contains(b, `"steered"`) })
	var truncated bool
	for _, c := range d.ToolCalls {
		truncated = truncated || (c.CallID == "c_big" && c.Result != nil && strings.Contains(c.Result.Content, "[truncated 90 bytes]"))
	}
	body := fetchJSON(t, ts, "/api/runs/r_edges/steps/0", nil)
	if !truncated || !strings.Contains(body, `"truncated":true`) || !slices.Contains(d.holes(), "truncated") {
		t.Errorf("capped result: calls %+v holes %v, want truncated", d.ToolCalls, d.holes())
	}
	if last := d.Events[len(d.Events)-1].Event.Type; last != "steered" {
		t.Errorf("step 0's last event = %s, want the steer delivered after it", last)
	}
	d1 := getStep(t, ts, "/api/runs/r_edges/steps/1", nil)
	for _, e := range d1.Events {
		if e.Event.Type == "steered" {
			t.Error("the steer of step 0 is filed under step 1 too")
		}
	}
	r0 := getStep(t, ts, "/api/runs/r_resume/steps/0", func(b string) bool { return strings.Contains(b, `"step_finish"`) })
	if len(r0.Events) == 0 || r0.Events[0].Event.Type != "tool_start" || len(r0.ToolCalls) == 0 || r0.ToolCalls[0].CallID != "c_ref" ||
		r0.ToolCalls[0].Result == nil || r0.ToolCalls[0].Result.Content != "refunded" {
		t.Errorf("resume step 0 = events %+v calls %+v, want the approved refund ahead of step_start", r0.Events, r0.ToolCalls)
	}
}

// TestStepOldContentOffCalls: a content-off run written before the
// request record (no request records; its tool events carry
// weft.content = stripped) badges each call stripped from its own
// events, with the result's bytes from the execute_tool span.
func TestStepOldContentOffCalls(t *testing.T) {
	ts, srv := requestsServer(t)
	off := map[string]any{"weft.content": "stripped"}
	tool := obsdb.Span{SpanID: "e000000000000001", Name: "execute_tool t", Kind: 1, StatusCode: 1,
		Attrs: map[string]any{"gen_ai.operation.name": "execute_tool", "weft.step.index": int64(0),
			"gen_ai.tool.call.id": "c_old", "weft.tool.result_bytes": int64(42)}}
	writeHand(t, srv.db, "r_oldoff", time.Now().UTC(), map[string]any{}, []handRec{
		ev(0, `{"type":"run_start","id":"r_oldoff","model":{"provider":"p","name":"m"},"agent":"hand"}`),
		ev(1, `{"type":"step_start","run_id":"r_oldoff","index":0}`),
		{kind: "event", pos: 2, body: `{"type":"tool_start","run_id":"r_oldoff","seq":1,"call_id":"c_old","name":"t","args":null}`, extra: off},
		{kind: "event", pos: 3, body: `{"type":"tool_finish","run_id":"r_oldoff","seq":2,"call_id":"c_old","name":"t","content":"","is_error":false}`, extra: off},
		ev(4, `{"type":"step_finish","run_id":"r_oldoff","index":0,"reason":"tool_calls","usage":{"input_tokens":1,"output_tokens":1}}`),
		ev(5, `{"type":"run_finish","run_id":"r_oldoff","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`),
	}, tool)
	d := getStep(t, ts, "/api/runs/r_oldoff/steps/0", nil)
	if len(d.ToolCalls) != 1 || d.ToolCalls[0].Badge != "stripped" || d.ToolCalls[0].Result == nil || d.ToolCalls[0].Result.Bytes != 42 {
		t.Errorf("old content-off call = %+v, want badge stripped, 42 bytes from the span", d.ToolCalls)
	}
	if got := d.holes(); !slices.Contains(got, "stripped") || !slices.Contains(got, "not_recorded") {
		t.Errorf("holes = %v, want stripped and not_recorded", got)
	}
}

// reqBody is a hand-written request record body for step n, attempt 1;
// ref is the messages_ref JSON.
func reqBody(step int, ref string) string {
	return `{"step":` + strconv.Itoa(step) + `,"attempt":1,"messages_ref":` + ref +
		`,"tools":{"catalog_hash":"","names":[]},"model":{"provider":"p","name":"m"}}`
}

// TestStepChildrenByStep: call ids may repeat across steps — step 0's
// Subagent call "same" started child r_same/0/same; step 1's plain call
// is also "same". The child is step 0's by its id alone: step 1 lists
// no child and its call no child_run_id.
func TestStepChildrenByStep(t *testing.T) {
	ts, srv := requestsServer(t)
	at := time.Now().UTC().Add(-time.Hour)
	writeHand(t, srv.db, "r_same", at, map[string]any{}, []handRec{
		ev(0, `{"type":"run_start","id":"r_same","model":{"provider":"p","name":"m"},"agent":"hand"}`),
		ev(1, `{"type":"step_start","run_id":"r_same","index":0}`),
		ev(2, `{"type":"tool_start","run_id":"r_same","seq":1,"call_id":"same","name":"research","args":{}}`),
		ev(3, `{"type":"tool_finish","run_id":"r_same","seq":2,"call_id":"same","name":"research","content":"found","is_error":false}`),
		ev(4, `{"type":"step_finish","run_id":"r_same","index":0,"reason":"tool_calls","usage":{"input_tokens":1,"output_tokens":1}}`),
		ev(5, `{"type":"step_start","run_id":"r_same","index":1}`),
		ev(6, `{"type":"tool_start","run_id":"r_same","seq":3,"call_id":"same","name":"t","args":{}}`),
		ev(7, `{"type":"tool_finish","run_id":"r_same","seq":4,"call_id":"same","name":"t","content":"ok","is_error":false}`),
		ev(8, `{"type":"step_finish","run_id":"r_same","index":1,"reason":"stop","usage":{"input_tokens":1,"output_tokens":1}}`),
		ev(9, `{"type":"run_finish","run_id":"r_same","usage":{"input_tokens":2,"output_tokens":2},"steps":2}`),
	})
	writeHand(t, srv.db, "r_same/0/same", at, map[string]any{"weft.parent.run.id": "r_same", "weft.parent.call.id": "same"}, []handRec{
		ev(0, `{"type":"run_start","id":"r_same/0/same","model":{"provider":"p","name":"m"},"agent":"hand"}`),
		ev(1, `{"type":"run_finish","run_id":"r_same/0/same","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`),
	})
	d0 := getStep(t, ts, "/api/runs/r_same/steps/0", nil)
	if len(d0.Children) != 1 || d0.Children[0].ID != "r_same/0/same" || len(d0.ToolCalls) != 1 || d0.ToolCalls[0].ChildRunID != "r_same/0/same" {
		t.Errorf("step 0 children %+v calls %+v, want the child joined to its call", d0.Children, d0.ToolCalls)
	}
	d1 := getStep(t, ts, "/api/runs/r_same/steps/1", nil)
	if len(d1.Children) != 0 || len(d1.ToolCalls) != 1 || d1.ToolCalls[0].ChildRunID != "" {
		t.Errorf("step 1 children %+v calls %+v, want none: the child is step 0's", d1.Children, d1.ToolCalls)
	}
}

// TestStepRunningNoFalseHoles: a running run's step is in flight, not lost. Step 1
// of r_live has started (its chat span is not exported until the call
// ends, and a position below the high-water mark is still on its way):
// status running, no attempts badge, no gap. r_live2 has a request
// record of step 0 but no step_start stored yet and no span at all:
// running too — never not_recorded ("install a tracer") or gap.
func TestStepRunningNoFalseHoles(t *testing.T) {
	ts, srv := requestsServer(t)
	now := time.Now().UTC()
	hash := map[string]any{"weft.instructions.hash": "aa"}
	chat := obsdb.Span{SpanID: "c0000000000000a1", Name: "chat m", Kind: 3, StatusCode: 1,
		Attrs: map[string]any{"gen_ai.operation.name": "chat", "weft.step.index": int64(0), "weft.stream": true, "gen_ai.request.model": "m"}}
	writeHand(t, srv.db, "r_live", now, map[string]any{}, []handRec{
		{kind: "event", pos: 0, body: `{"type":"run_start","id":"r_live","model":{"provider":"p","name":"m"},"agent":"hand"}`, extra: hash},
		ev(1, `{"type":"step_start","run_id":"r_live","index":0}`),
		{kind: "request", pos: 0, body: reqBody(0, `{"count":1}`), extra: map[string]any{"weft.step.index": int64(0)}},
		ev(2, `{"type":"step_finish","run_id":"r_live","index":0,"reason":"tool_calls","usage":{"input_tokens":1,"output_tokens":1}}`),
		ev(3, `{"type":"step_start","run_id":"r_live","index":1}`),
		{kind: "request", pos: 1, body: reqBody(1, `{"count":2}`), extra: map[string]any{"weft.step.index": int64(1)}},
		// pos 4 is in flight
		ev(5, `{"type":"tool_start","run_id":"r_live","seq":1,"call_id":"c1","name":"t","args":{}}`),
	}, chat)
	d := getStep(t, ts, "/api/runs/r_live/steps/1", nil)
	if d.Status != "running" || d.AttemptsBadge != nil || slices.Contains(d.holes(), "gap") {
		t.Errorf("running step 1 = status %q attempts_badge %+v holes %v, want running, no badge, no gap", d.Status, d.AttemptsBadge, d.holes())
	}
	writeHand(t, srv.db, "r_live2", now, map[string]any{}, []handRec{
		{kind: "event", pos: 0, body: `{"type":"run_start","id":"r_live2","model":{"provider":"p","name":"m"},"agent":"hand"}`, extra: hash},
		{kind: "request", pos: 0, body: reqBody(0, `{"count":1}`), extra: map[string]any{"weft.step.index": int64(0)}},
	})
	d2 := getStep(t, ts, "/api/runs/r_live2/steps/0", nil)
	if d2.Status != "running" || d2.AttemptsBadge != nil || slices.Contains(d2.holes(), "gap") || slices.Contains(d2.holes(), "not_recorded") {
		t.Errorf("running step 0 without its step_start = status %q attempts_badge %+v holes %v, want running, no badge, no gap",
			d2.Status, d2.AttemptsBadge, d2.holes())
	}
}

// TestStepCountedCallsBounded: a content-off max_tokens step lists its
// calls from the chat span's weft.model.tool_calls — a count, so a
// hostile value is clamped (maxStepCountedCalls) and the clamp badged
// derived, never allocated.
func TestStepCountedCallsBounded(t *testing.T) {
	ts, srv := requestsServer(t)
	chat := obsdb.Span{SpanID: "c0000000000000b1", Name: "chat m", Kind: 3, StatusCode: 1,
		Attrs: map[string]any{"gen_ai.operation.name": "chat", "weft.step.index": int64(0), "weft.stream": true,
			"gen_ai.request.model": "m", "weft.model.tool_calls": int64(1) << 40}}
	writeHand(t, srv.db, "r_huge", time.Now().UTC().Add(-time.Hour), map[string]any{}, []handRec{
		ev(0, `{"type":"run_start","id":"r_huge","model":{"provider":"p","name":"m"},"agent":"hand"}`),
		ev(1, `{"type":"step_start","run_id":"r_huge","index":0}`),
		ev(2, `{"type":"step_finish","run_id":"r_huge","index":0,"reason":"max_tokens","usage":{"input_tokens":1,"output_tokens":1}}`),
		ev(3, `{"type":"run_finish","run_id":"r_huge","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`),
	}, chat)
	d := getStep(t, ts, "/api/runs/r_huge/steps/0", nil)
	if len(d.ToolCalls) != maxStepCountedCalls || !slices.Contains(d.holes(), "derived") {
		t.Fatalf("calls = %d holes %v, want %d and derived", len(d.ToolCalls), d.holes(), maxStepCountedCalls)
	}
	for _, h := range d.Holes {
		if h.Hole == "derived" && !strings.Contains(h.Reason, "1099511627776 tool calls") {
			t.Errorf("derived reason = %q, want the span's count named", h.Reason)
		}
	}
}

// TestStepMessagesRefDropped: a request whose messages_ref.index names
// a messages record neither stored as a growth batch nor as a
// compaction view reads gap on messages_in, and in holes.
func TestStepMessagesRefDropped(t *testing.T) {
	ts, srv := requestsServer(t)
	writeHand(t, srv.db, "r_ref", time.Now().UTC().Add(-time.Hour), map[string]any{}, []handRec{
		{kind: "event", pos: 0, body: `{"type":"run_start","id":"r_ref","model":{"provider":"p","name":"m"},"agent":"hand"}`,
			extra: map[string]any{"weft.instructions.hash": "aa"}},
		ev(1, `{"type":"step_start","run_id":"r_ref","index":0}`),
		{kind: "request", pos: 0, body: reqBody(0, `{"index":5,"count":2}`), extra: map[string]any{"weft.step.index": int64(0)}},
		ev(2, `{"type":"step_finish","run_id":"r_ref","index":0,"reason":"stop","usage":{"input_tokens":1,"output_tokens":1}}`),
		ev(3, `{"type":"run_finish","run_id":"r_ref","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`),
	})
	d := getStep(t, ts, "/api/runs/r_ref/steps/0", nil)
	if d.MessagesIn.Badge != "gap" || d.MessagesIn.Count != 2 || d.MessagesIn.Index == nil || *d.MessagesIn.Index != 5 || !slices.Contains(d.holes(), "gap") {
		t.Errorf("messages_in = %+v holes %v, want index 5, count 2 under gap, gap listed", d.MessagesIn, d.holes())
	}
}
