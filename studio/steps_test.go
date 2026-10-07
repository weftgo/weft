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
	for _, h := range d.Holes {
		if h.Reason == "" {
			t.Errorf("hole %s has no reason", h.Hole)
		}
	}
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
