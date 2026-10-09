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
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
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
		Unknown []string   `json:"unknown"`
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
	recordDiffRunTo(t, url, runID, third, calls, meta, nil, extra...)
}

// recordDiffRunTo is recordDiffRun through destination options
// (otel.NoContent: the content-off twin).
func recordDiffRunTo(t *testing.T, url, runID, third string, calls int, meta map[string]string, dest []otel.DestOption, extra ...core.Option) {
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
	}, dest...)
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
		if row.Changed != (row.Step == 3) || !slices.Equal(row.Changes, want) || row.Unknown == nil || len(row.Unknown) != 0 {
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
		if want := []string{"system", "tool_calls", "tool_results", "text", "usage"}; row.Changed || !slices.Equal(row.Unknown, want) {
			t.Errorf("pre-A1 step %d = changed %v unknown %v, want unchanged and every column unknown %v", row.Step, row.Changed, row.Unknown, want)
		}
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

// TestDiffUnknownColumns (review finding 1): a column one side could
// not record is never compared with the other side's value — it is
// listed in unknown, neither changed nor the same. A run written before
// the request record against a recorded one; a content-off run against
// its content-on twin; a run whose step 1 lost its step_start against
// a whole one.
func TestDiffUnknownColumns(t *testing.T) {
	path := oldDB(t, "old1")
	srv := New(Open(path))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	recordDiffRun(t, ts.URL, "r_on", "order 3 shipped", 4, nil)
	recordDiffRunTo(t, ts.URL, "r_off", "order 3 shipped", 4, nil, []otel.DestOption{otel.NoContent()})
	waitRun(t, ts, "r_on", 5, "")
	waitRun(t, ts, "r_off", 5, "")

	// Pre-A1 against recorded: steps 0 and 1 compare nothing (the old
	// run stored no event, request or message of them), 2..4 missing.
	d, _ := getDiff(t, ts, "old1", "r_on", "")
	if !slices.Equal(d.Summary.ChangedSteps, []int{2, 3, 4}) {
		t.Errorf("pre-A1 vs recorded changed = %v, want [2 3 4] (missing only)", d.Summary.ChangedSteps)
	}
	for _, row := range d.Steps[:2] {
		if row.Changed || !slices.Contains(row.Unknown, "usage") || !slices.Contains(row.Unknown, "system") {
			t.Errorf("pre-A1 vs recorded step %d = changes %v unknown %v, want none changed, system and usage unknown", row.Step, row.Changes, row.Unknown)
		}
	}

	// Content-off against content-on: the hashes compare the system,
	// the usage compares; calls, results and text are unknown.
	d, _ = getDiff(t, ts, "r_off", "r_on", "")
	if len(d.Summary.ChangedSteps) != 0 {
		t.Errorf("content-off vs on changed = %v (%+v), want none", d.Summary.ChangedSteps, d.Steps)
	}
	for _, row := range d.Steps {
		want := []string{"tool_calls", "tool_results", "text"}
		if row.Step == 4 {
			want = []string{"text"} // the final step made no call
		}
		if !slices.Equal(row.Unknown, want) || row.A.SystemHash == "" || row.A.SystemHash != row.B.SystemHash {
			t.Errorf("content-off vs on step %d unknown = %v (hashes %q / %q), want %v", row.Step, row.Unknown, row.A.SystemHash, row.B.SystemHash, want)
		}
		if !slices.Contains(row.A.holes(), "stripped") {
			t.Errorf("content-off step %d holes = %v, want stripped", row.Step, row.A.holes())
		}
	}

	// A lost step_start inside the run (finding 5): the row is the gap
	// on its side, unknown for calls, results and usage, not changed.
	t0 := time.Now().UTC()
	evs := func(id string, lost bool) []handRec {
		out := []handRec{
			ev(0, `{"type":"run_start","id":"`+id+`","model":{"provider":"p","name":"m"},"agent":"hand"}`),
			ev(1, `{"type":"step_start","run_id":"`+id+`","index":0}`),
			ev(2, `{"type":"tool_start","run_id":"`+id+`","seq":1,"call_id":"c_a","name":"t","args":{}}`),
			ev(3, `{"type":"tool_finish","run_id":"`+id+`","seq":2,"call_id":"c_a","name":"t","content":"a","is_error":false}`),
			ev(4, `{"type":"step_finish","run_id":"`+id+`","index":0,"reason":"tool_calls","usage":{"input_tokens":1,"output_tokens":1}}`),
			ev(5, `{"type":"step_start","run_id":"`+id+`","index":1}`),
			ev(6, `{"type":"tool_start","run_id":"`+id+`","seq":3,"call_id":"c_b","name":"t","args":{}}`),
			ev(7, `{"type":"tool_finish","run_id":"`+id+`","seq":4,"call_id":"c_b","name":"t","content":"b","is_error":false}`),
			ev(8, `{"type":"step_finish","run_id":"`+id+`","index":1,"reason":"stop","usage":{"input_tokens":1,"output_tokens":1}}`),
			ev(9, `{"type":"run_finish","run_id":"`+id+`","usage":{"input_tokens":2,"output_tokens":2},"steps":2}`),
		}
		if lost {
			out = slices.Delete(out, 5, 6)
		}
		return out
	}
	writeHand(t, srv.db, "r_whole", t0, map[string]any{}, evs("r_whole", false))
	writeHand(t, srv.db, "r_lost", t0, map[string]any{}, evs("r_lost", true))
	d, _ = getDiff(t, ts, "r_lost", "r_whole", "")
	if len(d.Steps) != 2 || len(d.Summary.ChangedSteps) != 0 {
		t.Fatalf("lost step_start diff = %d rows changed %v (%+v), want 2 rows, none changed", len(d.Steps), d.Summary.ChangedSteps, d.Steps)
	}
	if r := d.Steps[1]; !slices.Contains(r.A.holes(), "gap") || !slices.Contains(r.Unknown, "tool_calls") || !slices.Contains(r.Unknown, "tool_results") ||
		!slices.Contains(r.Unknown, "usage") || len(r.A.ToolCalls) != 0 || len(r.B.ToolCalls) != 1 {
		t.Errorf("lost step 1 = a holes %v, unknown %v, calls %d / %d; want the gap, calls/results/usage unknown", r.A.holes(), r.Unknown, len(r.A.ToolCalls), len(r.B.ToolCalls))
	}
}

// TestDiffStepCap (finding 3): a run with more steps than the diff
// reads is compared that far; the top-level hole is truncated with the
// table's response-cap cause — its reason and fix, not a destination
// cap's — and each side's steps is the run's own count.
func TestDiffStepCap(t *testing.T) {
	ts, _ := requestsServer(t)
	recordDiffRun(t, ts.URL, "r_ca", "order 3 shipped", 4, nil)
	recordDiffRun(t, ts.URL, "r_cb", "order 3 lost", 4, nil)
	waitRun(t, ts, "r_ca", 5, "")
	waitRun(t, ts, "r_cb", 5, "")
	defer func(n int) { maxDiffSteps = n }(maxDiffSteps)
	maxDiffSteps = 2
	d, _ := getDiff(t, ts, "r_ca", "r_cb", "")
	reason, fix := obsdb.HoleNoteFor(obsdb.HoleTruncated, obsdb.CauseResponseCap)
	if def, _ := obsdb.HoleNote(obsdb.HoleTruncated); reason == def {
		t.Fatal("the response cap has no reason of its own in the table")
	}
	if len(d.Steps) != 2 || d.A.Steps != 5 || d.B.Steps != 5 || len(d.Summary.ChangedSteps) != 0 ||
		len(d.Holes) != 1 || d.Holes[0].Hole != "truncated" || d.Holes[0].Reason != reason || d.Holes[0].Fix != fix {
		t.Errorf("capped diff = %d rows, steps %d/%d, changed %v, holes %+v; want 2 rows, 5/5, none, the response-cap truncated", len(d.Steps), d.A.Steps, d.B.Steps, d.Summary.ChangedSteps, d.Holes)
	}
	maxDiffSteps = 5 // exactly the run's count: nothing cut
	if d, _ := getDiff(t, ts, "r_ca", "r_cb", ""); len(d.Steps) != 5 || len(d.Holes) != 0 {
		t.Errorf("cap at the count = %d rows holes %+v, want 5, none", len(d.Steps), d.Holes)
	}
}

// TestDiffUnreadableTranscript (finding 4): a messages record that
// does not parse makes every step's text unknown, said as such — never
// "no messages record was stored".
func TestDiffUnreadableTranscript(t *testing.T) {
	ts, srv := requestsServer(t)
	t0 := time.Now().UTC()
	for _, id := range []string{"r_bad", "r_good"} {
		writeHand(t, srv.db, id, t0, map[string]any{}, []handRec{
			ev(0, `{"type":"run_start","id":"`+id+`","model":{"provider":"p","name":"m"},"agent":"hand"}`),
			ev(1, `{"type":"step_start","run_id":"`+id+`","index":0}`),
			ev(2, `{"type":"step_finish","run_id":"`+id+`","index":0,"reason":"stop","usage":{"input_tokens":1,"output_tokens":1}}`),
			ev(3, `{"type":"run_finish","run_id":"`+id+`","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`),
		})
		body := `[{"role":"assistant","content":[{"type":"text","text":"hi"}]}]`
		if id == "r_bad" {
			body = `[{"role":"assistant","content":[{"type":"no_such_part"}]}]`
		}
		if err := srv.db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{{
			Time: t0, EventName: "weft.messages", Severity: 9, Body: body, Service: "svc",
			Attrs: map[string]any{"weft.record": "messages", "weft.run.id": id, "gen_ai.agent.name": "hand",
				"weft.messages.index": int64(0), "weft.messages.count": int64(1), "weft.step.index": int64(0)},
			Resource: map[string]any{"service.name": "svc"},
		}}}); err != nil {
			t.Fatal(err)
		}
	}
	d, _ := getDiff(t, ts, "r_bad", "r_good", "")
	if len(d.Steps) != 1 {
		t.Fatalf("rows = %d, want 1", len(d.Steps))
	}
	r := d.Steps[0]
	if r.B.Text == nil || *r.B.Text != "hi" || r.A.Text != nil || !slices.Contains(r.Unknown, "text") || slices.Contains(r.Changes, "text") {
		t.Errorf("unreadable vs readable = text %v / %v unknown %v changes %v, want null / hi, text unknown", r.A.Text, r.B.Text, r.Unknown, r.Changes)
	}
	var gap string
	for _, h := range r.A.Holes {
		if h.Hole == "gap" {
			gap = h.Reason
		}
	}
	if !strings.Contains(gap, "does not parse") || strings.Contains(gap, "was stored") {
		t.Errorf("unreadable side's gap = %q, want the does-not-parse reason", gap)
	}
}

// TestDiffWalkResumes (finding 2): assembling a run's steps in order
// starts each step's event walk where the last one stopped — never at
// page 0 — and yields exactly what assembling each step alone does.
func TestDiffWalkResumes(t *testing.T) {
	ts, srv := requestsServer(t)
	recordStepsRun(t, ts.URL, "r_walk", nil)
	fetchJSON(t, ts, "/api/runs/r_walk", func(b string) bool {
		return strings.Contains(b, `"request_count":6`) && strings.Contains(b, `"id":"r_walk/1/c_sub"`)
	})
	ctx := context.Background()
	seq, err := srv.loadStepRun(ctx, "r_walk")
	if err != nil {
		t.Fatal(err)
	}
	for n := range 3 {
		if n > 0 {
			r := seq.resume
			if r == nil || r.n != n {
				t.Fatalf("before step %d resume = %+v, want a point for step %d", n, r, n)
			}
			var h eventHead
			_ = json.Unmarshal(seq.pages[r.pi].Events[r.i].Event, &h)
			if h.Type != "step_start" || h.Index != n {
				t.Errorf("step %d resumes at a %s of step %d, want its step_start", n, h.Type, h.Index)
			}
		}
		got, err := seq.assembleStep(n, true)
		if err != nil {
			t.Fatal(err)
		}
		alone, err := srv.loadStepRun(ctx, "r_walk")
		if err != nil {
			t.Fatal(err)
		}
		want, err := alone.assembleStep(n, true)
		if err != nil {
			t.Fatal(err)
		}
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(want)
		if string(gb) != string(wb) {
			t.Errorf("step %d in sequence differs from step %d alone:\n%s\n%s", n, n, gb, wb)
		}
	}
}

// TestCanonicalJSON pins the args rule (finding 5): object keys sorted
// at every depth, array order kept, a \u escape and its literal
// character equal, numbers as written — 1 and 1.0 differ.
func TestCanonicalJSON(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{`{"b":1,"a":2}`, `{"a":2,"b":1}`, true},
		{`{"o":{"z":[1,{"y":2,"x":3}],"a":null}}`, ` { "o" : { "a" : null , "z" : [ 1 , { "x" : 3 , "y" : 2 } ] } } `, true},
		{`[1,2]`, `[2,1]`, false},
		{`{"s":"\u00e9\u003c"}`, `{"s":"é<"}`, true},
		{`{"n":1}`, `{"n":1.0}`, false},
		{`{"n":1e2}`, `{"n":100}`, false},
		{`{"n":12345678901234567890}`, `{"n":12345678901234567890}`, true},
		{``, `null`, true},
	} {
		ca, cb := string(canonicalJSON(json.RawMessage(c.a))), string(canonicalJSON(json.RawMessage(c.b)))
		if (ca == cb) != c.same {
			t.Errorf("canonical(%s) = %s, canonical(%s) = %s; same = %v, want %v", c.a, ca, c.b, cb, ca == cb, c.same)
		}
	}
	if got := string(canonicalJSON(json.RawMessage(`{"b":{"d":1,"c":"é"},"a":[3,1]}`))); got != `{"a":[3,1],"b":{"c":"é","d":1}}` {
		t.Errorf("canonical = %s", got)
	}
	if got := string(canonicalJSON(json.RawMessage(`{not json`))); got != `{not json` {
		t.Errorf("an unparseable value = %s, want it verbatim", got)
	}
}

// TestDiffNoInstructionsReadToken: an agent with no instructions sends
// no system text — a fact every identity reads, so under a read token
// the system column compares ("" on both sides, hash ""), never unknown.
func TestDiffNoInstructionsReadToken(t *testing.T) {
	const tok = "srv-token"
	srv := New(Open(filepath.Join(t.TempDir(), "weft.db")), Token(tok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	for _, id := range []string{"r_ni1", "r_ni2"} {
		withPipeline(t, ts.URL, true, func(prov []core.Option) {
			agent := core.New(wefttest.Script(wefttest.Say("hi")), prov...)
			if _, err := agent.Generate(context.Background(), core.RunID(id), core.Prompt("go"),
				core.Metadata(map[string]string{"weft.public_id": "pub_a"})); err != nil {
				t.Fatal(err)
			}
		})
		waitRun(t, ts, id, 1, tok)
	}
	read, err := signPanelToken([]byte(tok), panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for _, bearer := range []string{read, tok} {
		d, _ := getDiff(t, ts, "r_ni1", "r_ni2", bearer)
		if len(d.Steps) != 1 {
			t.Fatalf("rows = %d, want 1", len(d.Steps))
		}
		r := d.Steps[0]
		if slices.Contains(r.Unknown, "system") || r.Changed || r.A.System == nil || *r.A.System != "" || r.A.SystemHash != "" {
			t.Errorf("no instructions (read token %v) = system %v hash %q unknown %v changes %v, want \"\" compared, unchanged",
				bearer == read, r.A.System, r.A.SystemHash, r.Unknown, r.Changes)
		}
	}
}

// TestDiffWalkResumesPastLostStepStart: step 1's step_start was
// dropped from a 3-step run. Assembled in sequence (each walk resuming
// where the last stopped), every step equals the same step assembled
// alone, and step 2 is still found by its step_start.
func TestDiffWalkResumesPastLostStepStart(t *testing.T) {
	_, srv := requestsServer(t)
	const id = "r_lost3"
	writeHand(t, srv.db, id, time.Now().UTC(), map[string]any{}, []handRec{
		ev(0, `{"type":"run_start","id":"r_lost3","model":{"provider":"p","name":"m"},"agent":"hand"}`),
		ev(1, `{"type":"step_start","run_id":"r_lost3","index":0}`),
		ev(2, `{"type":"tool_start","run_id":"r_lost3","seq":1,"call_id":"c_a","name":"t","args":{}}`),
		ev(3, `{"type":"tool_finish","run_id":"r_lost3","seq":2,"call_id":"c_a","name":"t","content":"a","is_error":false}`),
		ev(4, `{"type":"step_finish","run_id":"r_lost3","index":0,"reason":"tool_calls","usage":{"input_tokens":1,"output_tokens":1}}`),
		// pos 5, step 1's step_start, never arrived
		ev(6, `{"type":"tool_start","run_id":"r_lost3","seq":3,"call_id":"c_b","name":"t","args":{}}`),
		ev(7, `{"type":"tool_finish","run_id":"r_lost3","seq":4,"call_id":"c_b","name":"t","content":"b","is_error":false}`),
		ev(8, `{"type":"step_finish","run_id":"r_lost3","index":1,"reason":"tool_calls","usage":{"input_tokens":1,"output_tokens":1}}`),
		ev(9, `{"type":"step_start","run_id":"r_lost3","index":2}`),
		ev(10, `{"type":"step_finish","run_id":"r_lost3","index":2,"reason":"stop","usage":{"input_tokens":1,"output_tokens":1}}`),
		ev(11, `{"type":"run_finish","run_id":"r_lost3","usage":{"input_tokens":3,"output_tokens":3},"steps":3}`),
	})
	ctx := context.Background()
	seq, err := srv.loadStepRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for n := range 3 {
		got, err := seq.assembleStep(n, true)
		if err != nil {
			t.Fatalf("step %d in sequence: %v", n, err)
		}
		alone, err := srv.loadStepRun(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		want, err := alone.assembleStep(n, true)
		if err != nil {
			t.Fatalf("step %d alone: %v", n, err)
		}
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(want)
		if string(gb) != string(wb) {
			t.Errorf("step %d in sequence differs from step %d alone:\n%s\n%s", n, n, gb, wb)
		}
		if n == 2 && (got.Started == nil || got.Status != "ok" || got.eventsLost) {
			t.Errorf("step 2 = started %v status %q eventsLost %v, want found by its step_start, ok", got.Started, got.Status, got.eventsLost)
		}
		if n == 1 && !got.eventsLost {
			t.Error("step 1 (its step_start lost) reads eventsLost false")
		}
	}
	if _, err := seq.assembleStep(3, true); !isNoStep(err) {
		t.Errorf("step 3 in sequence = %v, want no such step", err)
	}
}
