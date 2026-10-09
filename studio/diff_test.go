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
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// diffDocT is GET /api/diff as a client decodes it.
type diffDocT struct {
	A, B struct {
		RunID  string `json:"run_id"`
		Steps  int    `json:"steps"`
		Status string `json:"status"`
	}
	Steps []struct {
		Step    int        `json:"step"`
		Changed bool       `json:"changed"`
		A       *diffSideT `json:"a"`
		B       *diffSideT `json:"b"`
		Changes []string   `json:"changes"`
	} `json:"steps"`
	Summary struct {
		ChangedSteps []int `json:"changed_steps"`
		FirstChanged *int  `json:"first_changed"`
	} `json:"summary"`
	Holes []struct{ Hole, Reason, Fix string } `json:"holes"`
}

type diffSideT struct {
	Status     string  `json:"status"`
	SystemHash string  `json:"system_hash"`
	System     *string `json:"system"`
	ToolCalls  []struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"tool_calls"`
	ToolResults []struct {
		CallID  string `json:"call_id"`
		Name    string `json:"name"`
		Content string `json:"content"`
		IsError bool   `json:"is_error"`
	} `json:"tool_results"`
	Text  *string    `json:"text"`
	Usage core.Usage `json:"usage"`
	Marks []string   `json:"marks"`
	Holes []struct {
		Hole, Reason, Fix string
	} `json:"holes"`
}

func (s *diffSideT) holes() []string {
	out := []string{}
	for _, h := range s.Holes {
		out = append(out, h.Hole)
	}
	return out
}

// checkDiffHoles fails on a hole without a reason, on any side.
func checkDiffHoles(t *testing.T, where string, d diffDocT) {
	t.Helper()
	for _, row := range d.Steps {
		for _, side := range []*diffSideT{row.A, row.B} {
			if side == nil {
				continue
			}
			for _, h := range side.Holes {
				if h.Reason == "" {
					t.Errorf("%s: step %d hole %s has no reason", where, row.Step, h.Hole)
				}
			}
		}
	}
}

type diffOrderIn struct {
	OrderID string `json:"order_id" jsonschema:"the order"`
}

// recordDiffRun records the E3 scenario through the real pipeline
// (otel → OTLP ingest → obsdb): steps 0..calls-1 each call lookup_order
// for order k (call id c<k>), the last step says "all looked up". Order
// 3's result is third — the one thing two runs of the Done line differ
// in. extra adds agent options (a PrepareStep compaction).
func recordDiffRun(t *testing.T, url, runID, third string, calls int, meta map[string]string, extra ...core.Option) {
	t.Helper()
	withPipeline(t, url, true, func(prov []core.Option) {
		lookup := core.Tool("lookup_order", "Look up an order.", func(_ context.Context, in diffOrderIn) (string, error) {
			if in.OrderID == "3" {
				return third, nil
			}
			return "order " + in.OrderID + " shipped", nil
		})
		var turns []wefttest.Turn
		for k := range calls {
			id := strconv.Itoa(k)
			turns = append(turns, wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"` + id + `"}`, ID: "c" + id}))
		}
		turns = append(turns, wefttest.Say("all looked up"))
		opts := append([]core.Option{core.Name("orders"), core.Instructions("You are a support agent."), lookup}, prov...)
		agent := core.New(wefttest.Script(turns...), append(opts, extra...)...)
		if _, err := agent.Generate(context.Background(), core.RunID(runID), core.Prompt("look up orders 0 to 3"), core.Metadata(meta)); err != nil {
			t.Fatal(err)
		}
	})
}

// waitRun waits until the run's row counts steps steps and its
// finish is stored (ingest lands asynchronously).
func waitRun(t *testing.T, ts *httptest.Server, id string, steps int, bearer string) {
	t.Helper()
	fetchJSONAs(t, ts, "/api/runs/"+id, bearer, func(b string) bool {
		return strings.Contains(b, `"steps":`+strconv.Itoa(steps)+`,`) && !strings.Contains(b, `"status":"running"`)
	})
}

// compacting is a PrepareStep that trims the middle of step 3's
// messages: a run-scope compaction view on that step only.
func compacting() core.Option {
	return core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
		if step != 3 || len(req.Messages) < 4 {
			return req, nil
		}
		m := req.Messages
		req.Messages = []core.Message{m[0], core.User("summary: orders 0 to 1 were looked up"), m[len(m)-2], m[len(m)-1]}
		return req, nil
	})
}

func getDiff(t *testing.T, ts *httptest.Server, a, b, bearer string) (diffDocT, string) {
	t.Helper()
	body := fetchJSONAs(t, ts, "/api/diff?a="+a+"&b="+b, bearer, nil)
	var d diffDocT
	decode(t, body, &d)
	checkDiffHoles(t, a+" vs "+b, d)
	return d, body
}

// TestDiffRoute is plan E3's Done line on the API: two runs that
// differ only in a tool result at step 3 answer one changed step — 3,
// changes [tool_results] — and identical rows elsewhere, every column
// present on both sides. Pinned as a golden (the response both the
// panel's two columns and Studio's compare view render). Also: a == b
// is no change; a missing parameter 400; an unknown run 404.
func TestDiffRoute(t *testing.T) {
	ts, _ := requestsServer(t)
	recordDiffRun(t, ts.URL, "r_da", "order 3 shipped", 4, nil)
	recordDiffRun(t, ts.URL, "r_db", "order 3 lost", 4, nil)
	waitRun(t, ts, "r_da", 5, "")
	waitRun(t, ts, "r_db", 5, "")

	d, body := getDiff(t, ts, "r_da", "r_db", "")
	stepGolden(t, "diff.golden.json", body)
	if d.A.RunID != "r_da" || d.B.RunID != "r_db" || d.A.Steps != 5 || d.B.Steps != 5 || d.A.Status != "succeeded" || len(d.Steps) != 5 {
		t.Fatalf("diff = a %+v b %+v, %d rows; want both 5 steps, succeeded, 5 rows", d.A, d.B, len(d.Steps))
	}
	if !slices.Equal(d.Summary.ChangedSteps, []int{3}) || d.Summary.FirstChanged == nil || *d.Summary.FirstChanged != 3 {
		t.Errorf("summary = %+v, want changed_steps [3], first_changed 3", d.Summary)
	}
	for _, row := range d.Steps {
		want := []string{}
		if row.Step == 3 {
			want = []string{"tool_results"}
		}
		if row.Changed != (row.Step == 3) || !slices.Equal(row.Changes, want) {
			t.Errorf("step %d = changed %v %v, want %v %v", row.Step, row.Changed, row.Changes, row.Step == 3, want)
		}
		for _, side := range []*diffSideT{row.A, row.B} {
			if side == nil || side.Status != "ok" || side.System == nil || *side.System != "You are a support agent." || side.SystemHash == "" ||
				side.Text == nil || side.Usage.InputTokens == 0 || len(side.Marks) != 0 || len(side.Holes) != 0 {
				t.Errorf("step %d side = %+v, want every column present, no mark, no hole", row.Step, side)
			}
		}
		if row.Step < 4 {
			a := row.A
			if len(a.ToolCalls) != 1 || a.ToolCalls[0].Name != "lookup_order" || string(a.ToolCalls[0].Args) != `{"order_id":"`+strconv.Itoa(row.Step)+`"}` ||
				len(a.ToolResults) != 1 || a.ToolResults[0].CallID != "c"+strconv.Itoa(row.Step) {
				t.Errorf("step %d a = calls %+v results %+v, want the lookup and its result", row.Step, a.ToolCalls, a.ToolResults)
			}
		} else if *row.A.Text != "all looked up" || len(row.A.ToolCalls) != 0 {
			t.Errorf("step 4 a = text %q calls %+v, want the final text, no call", *row.A.Text, row.A.ToolCalls)
		}
	}
	if r3 := d.Steps[3]; r3.A.ToolResults[0].Content != "order 3 shipped" || r3.B.ToolResults[0].Content != "order 3 lost" {
		t.Errorf("step 3 results = %+v / %+v", r3.A.ToolResults, r3.B.ToolResults)
	}

	// a == b: every row unchanged.
	same, _ := getDiff(t, ts, "r_da", "r_da", "")
	if len(same.Steps) != 5 || len(same.Summary.ChangedSteps) != 0 || same.Summary.FirstChanged != nil {
		t.Errorf("a == b = %+v, want 5 rows, no change", same.Summary)
	}
	for _, row := range same.Steps {
		if row.Changed || len(row.Changes) != 0 {
			t.Errorf("a == b step %d = %v %v, want unchanged", row.Step, row.Changed, row.Changes)
		}
	}

	for path, want := range map[string]int{
		"/api/diff":                  http.StatusBadRequest,
		"/api/diff?a=r_da":           http.StatusBadRequest,
		"/api/diff?b=r_da":           http.StatusBadRequest,
		"/api/diff?a=r_da&b=":        http.StatusBadRequest,
		"/api/diff?a=r_da&b=nope":    http.StatusNotFound,
		"/api/diff?a=nope&b=r_da":    http.StatusNotFound,
		"/api/diff?a=r_da&b=r_db&x=": http.StatusOK,
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
}

// TestDiffMarks: a compaction view and a subagent call are marks on
// their side, never changes by themselves (decision 12) — a compaction
// on one side, on both, and a call that is a Subagent on one side and
// a plain tool with the same answer on the other. Unequal step counts
// are rows with one side null and changes [missing].
func TestDiffMarks(t *testing.T) {
	ts, _ := requestsServer(t)
	recordDiffRun(t, ts.URL, "r_plain", "order 3 shipped", 4, nil)
	recordDiffRun(t, ts.URL, "r_comp", "order 3 shipped", 4, nil, compacting())
	recordDiffRun(t, ts.URL, "r_comp2", "order 3 shipped", 4, nil, compacting())
	recordDiffRun(t, ts.URL, "r_short", "order 3 shipped", 2, nil)
	for id, n := range map[string]int{"r_plain": 5, "r_comp": 5, "r_comp2": 5, "r_short": 3} {
		waitRun(t, ts, id, n, "")
	}

	// A compaction on one side: step 3's b is marked, nothing changed.
	d, _ := getDiff(t, ts, "r_plain", "r_comp", "")
	if len(d.Summary.ChangedSteps) != 0 {
		t.Errorf("plain vs compacted: changed %v (%+v), want none", d.Summary.ChangedSteps, d.Steps)
	}
	for _, row := range d.Steps {
		wantB := []string{}
		if row.Step == 3 {
			wantB = []string{"compacted"}
		}
		if !slices.Equal(row.B.Marks, wantB) || len(row.A.Marks) != 0 {
			t.Errorf("plain vs compacted step %d marks = %v / %v, want [] / %v", row.Step, row.A.Marks, row.B.Marks, wantB)
		}
		if row.Step == 3 && !slices.Contains(row.B.holes(), "compacted") {
			t.Errorf("compacted side's holes = %v, want compacted", row.B.holes())
		}
	}
	// On both sides: still no change.
	d, _ = getDiff(t, ts, "r_comp", "r_comp2", "")
	if len(d.Summary.ChangedSteps) != 0 || !slices.Equal(d.Steps[3].A.Marks, []string{"compacted"}) || !slices.Equal(d.Steps[3].B.Marks, []string{"compacted"}) {
		t.Errorf("compacted vs compacted = %v, step 3 marks %v / %v; want no change, both marked", d.Summary.ChangedSteps, d.Steps[3].A.Marks, d.Steps[3].B.Marks)
	}

	// Unequal step counts: rows past the shorter run are missing.
	d, _ = getDiff(t, ts, "r_plain", "r_short", "")
	if d.A.Steps != 5 || d.B.Steps != 3 || len(d.Steps) != 5 || !slices.Equal(d.Summary.ChangedSteps, []int{2, 3, 4}) {
		t.Fatalf("plain vs short = a %d b %d rows %d changed %v, want 5, 3, 5 rows, [2 3 4]", d.A.Steps, d.B.Steps, len(d.Steps), d.Summary.ChangedSteps)
	}
	if r := d.Steps[2]; !slices.Equal(r.Changes, []string{"tool_calls", "tool_results", "text", "usage"}) && !slices.Equal(r.Changes, []string{"tool_calls", "tool_results", "text"}) {
		t.Errorf("step 2 (a call vs the final text) changes = %v", r.Changes)
	}
	for _, n := range []int{3, 4} {
		if r := d.Steps[n]; !r.Changed || !slices.Equal(r.Changes, []string{"missing"}) || r.A == nil || r.B != nil {
			t.Errorf("step %d = changed %v %v a %v b %v, want missing on b", n, r.Changed, r.Changes, r.A != nil, r.B != nil)
		}
	}
	d, body := getDiff(t, ts, "r_short", "r_plain", "")
	if d.Steps[4].A != nil || d.Steps[4].B == nil || !strings.Contains(body, `"a":null`) {
		t.Errorf("short vs plain step 4 = a %v b %v, want a null", d.Steps[4].A, d.Steps[4].B)
	}

	// A subagent on one side: the same call, the same answer — a plain
	// tool here, a Subagent's child run there. Marked, not changed.
	research := func(prov []core.Option, sub bool) *core.Agent {
		type in struct {
			Prompt string `json:"prompt" jsonschema:"the question"`
		}
		var tool *core.ToolDef
		if sub {
			child := core.New(wefttest.Script(wefttest.Say("the carrier lost it")),
				append([]core.Option{core.Name("researcher")}, prov...)...)
			tool = core.Subagent("research", "Research an order.", child)
		} else {
			tool = core.Tool("research", "Research an order.", func(context.Context, in) (string, error) { return "the carrier lost it", nil })
		}
		return core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"why is order 42 late?"}`, ID: "c_r"}),
			wefttest.Say("lost"),
		), append([]core.Option{core.Name("orders"), core.Instructions("You are a support agent."), tool}, prov...)...)
	}
	for id, sub := range map[string]bool{"r_tool": false, "r_sub": true} {
		withPipeline(t, ts.URL, true, func(prov []core.Option) {
			if _, err := research(prov, sub).Generate(context.Background(), core.RunID(id), core.Prompt("why late?")); err != nil {
				t.Fatal(err)
			}
		})
	}
	waitRun(t, ts, "r_tool", 2, "")
	fetchJSON(t, ts, "/api/runs/r_sub", func(b string) bool {
		return strings.Contains(b, `"id":"r_sub/0/c_r"`) && strings.Contains(b, `"steps":2,`)
	})
	d, _ = getDiff(t, ts, "r_tool", "r_sub", "")
	if len(d.Steps) != 2 || len(d.Summary.ChangedSteps) != 0 {
		t.Errorf("tool vs subagent = %d rows changed %v (%+v), want 2 rows, none changed", len(d.Steps), d.Summary.ChangedSteps, d.Steps)
	} else if !slices.Equal(d.Steps[0].B.Marks, []string{"subagent"}) || len(d.Steps[0].A.Marks) != 0 {
		t.Errorf("step 0 marks = %v / %v, want [] / [subagent]", d.Steps[0].A.Marks, d.Steps[0].B.Marks)
	}
}

// oldDB writes a sqlite file as a weft before ADR 0028 left it
// (migrations 0001 and 0002 by hand, run rows, no records or spans).
func oldDB(t *testing.T, rows ...string) string {
	t.Helper()
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
	for _, id := range rows {
		if _, err := raw.Exec(`INSERT INTO runs (run_id, agent, started_ns, last_seen_ns, finished_ok, steps)
			VALUES (?, 'support', 100, 200, 1, 2)`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDiffNotRecorded: two runs written before the request record —
// each side's system is null under not_recorded, its text null with
// the gap that says why, and nothing reads as a change: what neither
// side recorded is not a difference.
func TestDiffNotRecorded(t *testing.T) {
	srv := New(Open(oldDB(t, "old1", "old2")))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	d, body := getDiff(t, ts, "old1", "old2", "")
	stepGolden(t, "diff-not-recorded.golden.json", body)
	if len(d.Steps) != 2 || len(d.Summary.ChangedSteps) != 0 {
		t.Fatalf("pre-A1 diff = %d rows changed %v, want 2, none", len(d.Steps), d.Summary.ChangedSteps)
	}
	for _, row := range d.Steps {
		for _, side := range []*diffSideT{row.A, row.B} {
			h := side.holes()
			if side.System != nil || side.SystemHash != "" || side.Text != nil || !slices.Contains(h, "not_recorded") || !slices.Contains(h, "gap") {
				t.Errorf("pre-A1 step %d side = system %v text %v holes %v, want both null under not_recorded and gap", row.Step, side.System, side.Text, h)
			}
		}
	}
}

// TestDiffPanelTokens: a read-scoped panel token reads the diff of two
// runs inside its public id with every side's system null under the
// hidden hole (the hash kept — the run row's instructions_hash is
// read-scoped too) and every other column as the server token reads
// it; a run outside its public id is 403 whichever side names it. A
// playground-scoped token reads the system text.
func TestDiffPanelTokens(t *testing.T) {
	const tok = "srv-token"
	srv := New(Open(filepath.Join(t.TempDir(), "weft.db")), Token(tok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	pubA := map[string]string{"weft.public_id": "pub_a"}
	recordDiffRun(t, ts.URL, "r_ta", "order 3 shipped", 4, pubA)
	recordDiffRun(t, ts.URL, "r_tb", "order 3 lost", 4, pubA)
	recordDiffRun(t, ts.URL, "r_tother", "order 3 lost", 4, map[string]string{"weft.public_id": "pub_b"})
	for _, id := range []string{"r_ta", "r_tb", "r_tother"} {
		waitRun(t, ts, id, 5, tok)
	}
	sign := func(scope string) string {
		s, err := signPanelToken([]byte(tok), panelClaims{PublicID: "pub_a", Scope: scope, Exp: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	full, _ := getDiff(t, ts, "r_ta", "r_tb", tok)
	read, body := getDiff(t, ts, "r_ta", "r_tb", sign(scopeRead))
	stepGolden(t, "diff-hidden.golden.json", body)
	if strings.Contains(body, "You are a support agent.") {
		t.Errorf("the read token's diff carries the system prompt: %s", body)
	}
	if !slices.Equal(read.Summary.ChangedSteps, []int{3}) || !slices.Equal(read.Steps[3].Changes, []string{"tool_results"}) {
		t.Errorf("read token summary %+v step 3 %v, want [3], [tool_results]", read.Summary, read.Steps[3].Changes)
	}
	for i, row := range read.Steps {
		for j, side := range []*diffSideT{row.A, row.B} {
			fs := []*diffSideT{full.Steps[i].A, full.Steps[i].B}[j]
			if side.System != nil || !slices.Equal(side.holes(), []string{"hidden"}) || side.Holes[0].Fix != "use a playground-scoped token" ||
				side.SystemHash == "" || side.SystemHash != fs.SystemHash {
				t.Errorf("read token step %d side = system %v hash %q holes %v, want null, the hash, hidden", row.Step, side.System, side.SystemHash, side.holes())
			}
			rs, _ := json.Marshal([]any{side.ToolCalls, side.ToolResults, side.Text, side.Usage})
			fb, _ := json.Marshal([]any{fs.ToolCalls, fs.ToolResults, fs.Text, fs.Usage})
			if string(rs) != string(fb) {
				t.Errorf("read token step %d columns = %s, want the server token's %s", row.Step, rs, fb)
			}
		}
	}
	pg, _ := getDiff(t, ts, "r_ta", "r_tb", sign(scopePlayground))
	if s := pg.Steps[0].A.System; s == nil || *s != "You are a support agent." || len(pg.Steps[0].A.Holes) != 0 {
		t.Errorf("playground token step 0 system = %v holes %v, want the text, no hole", s, pg.Steps[0].A.holes())
	}
	for _, path := range []string{"/api/diff?a=r_ta&b=r_tother", "/api/diff?a=r_tother&b=r_ta"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+sign(scopeRead))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || strings.Contains(string(b), "order 3") {
			t.Errorf("%s as a read token = %d %s, want 403 and nothing of either run", path, resp.StatusCode, b)
		}
	}
}
