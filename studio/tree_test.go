package studio_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio"
)

// countingDB counts the reads the run tree replaces: a run's children
// read one run at a time (Runs with a concrete ParentRunID — the
// coster's per-row path) and RunSpans. It implements no TreeReader:
// countingTreeDB adds it.
type countingDB struct {
	obsdb.DB
	mu                         sync.Mutex
	perRunChildReads, runSpans int
	childRunsCalls, treeReads  int
	childRunsParents           [][]string
}

func (c *countingDB) Runs(ctx context.Context, q obsdb.RunQuery) (obsdb.RunPage, error) {
	if q.ParentRunID != "" && q.ParentRunID != "*" {
		c.mu.Lock()
		c.perRunChildReads++
		c.mu.Unlock()
	}
	return c.DB.Runs(ctx, q)
}

func (c *countingDB) RunSpans(ctx context.Context, id string) ([]obsdb.Span, error) {
	c.mu.Lock()
	c.runSpans++
	c.mu.Unlock()
	return c.DB.RunSpans(ctx, id)
}

// reset zeroes the counters and returns the ones before.
func (c *countingDB) reset() (perRun, spans, childRuns, trees int, parents [][]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	perRun, spans, childRuns, trees, parents = c.perRunChildReads, c.runSpans, c.childRunsCalls, c.treeReads, c.childRunsParents
	c.perRunChildReads, c.runSpans, c.childRunsCalls, c.treeReads, c.childRunsParents = 0, 0, 0, 0, nil
	return
}

type countingTreeDB struct {
	*countingDB
	tree obsdb.TreeReader
}

func (c countingTreeDB) RunTreeSpans(ctx context.Context, id string) ([]obsdb.TreeSpan, error) {
	c.mu.Lock()
	c.treeReads++
	c.mu.Unlock()
	return c.tree.RunTreeSpans(ctx, id)
}

func (c countingTreeDB) ChildRuns(ctx context.Context, ids []string) (map[string][]obsdb.RunRow, error) {
	c.mu.Lock()
	c.childRunsCalls++
	c.childRunsParents = append(c.childRunsParents, slices.Clone(ids))
	c.mu.Unlock()
	return c.tree.ChildRuns(ctx, ids)
}

// treeSpanDoc is the fields of a ?tree=1 span the test reads.
type treeSpanDoc struct {
	TraceID      string         `json:"trace_id"`
	SpanID       string         `json:"span_id"`
	ParentSpanID string         `json:"parent_span_id"`
	Name         string         `json:"name"`
	Start        time.Time      `json:"start"`
	RunID        string         `json:"run_id"`
	ParentRunID  string         `json:"parent_run_id"`
	Attrs        map[string]any `json:"attrs"`
}

// deepDelegation runs rw.deep-delegation's shape n times through the
// real pipeline into srv's database: director › ops_manager (a
// Subagent, run ops-manager) › worker (a Subagent of ops-manager, run
// worker). It returns the director runs' ids.
func deepDelegation(t *testing.T, url string, n int) []string {
	t.Helper()
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(url, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	prov := []core.Option{core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider())}
	repeat := func(turns ...wefttest.Turn) []wefttest.Turn {
		var out []wefttest.Turn
		for range n {
			out = append(out, turns...)
		}
		return out
	}
	worker := core.New(wefttest.Script(repeat(wefttest.Say("42 units of A-17 in Lyon."))...),
		append(prov, core.Name("worker"))...)
	ops := core.New(wefttest.Script(repeat(
		wefttest.ToolCalls(wefttest.Call{Name: "worker", Args: `{"prompt":"count A-17 in Lyon"}`, ID: "call_worker"}),
		wefttest.Say("Lyon holds 42 units of A-17."))...),
		append(prov, core.Name("ops-manager"), core.Subagent("worker", "Count stock.", worker))...)
	director := core.New(wefttest.Script(repeat(
		wefttest.ToolCalls(wefttest.Call{Name: "ops_manager", Args: `{"prompt":"how many A-17 in Lyon?"}`, ID: "call_ops"}),
		wefttest.Say("We have 42 units of A-17 in Lyon."))...),
		append(prov, core.Name("director"), core.Subagent("ops_manager", "Ask operations.", ops))...)
	var ids []string
	for range n {
		res, err := director.Generate(ctx, core.Prompt("How many A-17 do we have in Lyon?"))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.ID)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	return ids
}

func getJSON(t *testing.T, url string, v any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if v != nil && resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatalf("GET %s: %v: %s", url, err, b)
		}
	}
	return resp.StatusCode
}

// TestSpansTreeFromARealRun (U9.1; UX-A-01, UX-E-34): the director run
// of rw.deep-delegation's shape, recorded through the real pipeline:
// GET /api/runs/{id}/spans?tree=1 answers its own spans and
// ops-manager's and worker's beside them — each keyed by run_id +
// span_id with its run's parent_run_id, in start order, the child's
// invoke_agent under the director's execute_tool span — from one
// RunTreeSpans read (one query by trace id, plan pinned in obsdb). The
// plain route keeps the run's own spans, each with run_id.
func TestSpansTreeFromARealRun(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "weft.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cdb := countingTreeDB{&countingDB{DB: db}, db.(obsdb.TreeReader)}
	srv := studio.New(studio.DB(cdb))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })
	director := deepDelegation(t, ts.URL, 1)[0]
	ops := director + "/0/call_ops"
	worker := ops + "/0/call_worker"

	var meta struct {
		Capabilities []string `json:"capabilities"`
	}
	getJSON(t, ts.URL+"/api/meta", &meta)
	if !slices.Contains(meta.Capabilities, "spans_tree") {
		t.Errorf("capabilities = %v, want spans_tree over sqlite", meta.Capabilities)
	}

	cdb.reset()
	var doc struct {
		Spans []treeSpanDoc `json:"spans"`
	}
	if code := getJSON(t, ts.URL+"/api/runs/"+director+"/spans?tree=1", &doc); code != http.StatusOK {
		t.Fatalf("?tree=1 = %d", code)
	}
	if _, spans, _, trees, _ := cdb.reset(); trees != 1 || spans != 0 {
		t.Errorf("?tree=1 read RunTreeSpans %d times and RunSpans %d, want one tree read and no per-run read", trees, spans)
	}
	type key struct{ run, span string }
	seen := map[key]bool{}
	count := map[string]int{}
	byID := map[string]treeSpanDoc{}
	for i, s := range doc.Spans {
		k := key{s.RunID, s.SpanID}
		if seen[k] {
			t.Errorf("span %v twice", k)
		}
		seen[k] = true
		count[s.RunID]++
		byID[s.SpanID] = s
		if got, _ := s.Attrs["weft.run.id"].(string); got != s.RunID {
			t.Errorf("span %s: run_id %q, its weft.run.id %q", s.SpanID, s.RunID, got)
		}
		if want := map[string]string{director: "", ops: director, worker: ops}[s.RunID]; s.ParentRunID != want {
			t.Errorf("span %s of %s: parent_run_id %q, want %q", s.Name, s.RunID, s.ParentRunID, want)
		}
		if i > 0 && s.Start.Before(doc.Spans[i-1].Start) {
			t.Errorf("span %d (%s) starts before span %d: not in start order", i, s.Name, i-1)
		}
	}
	// Each run: its invoke_agent, a chat per step; the two delegating
	// runs an execute_tool each.
	if count[director] != 4 || count[ops] != 4 || count[worker] != 2 || len(count) != 3 {
		t.Errorf("tree spans per run = %v, want director 4, ops-manager 4, worker 2 and no other run", count)
	}
	for _, s := range doc.Spans {
		if s.RunID == director || !strings.HasPrefix(s.Name, "invoke_agent") {
			continue
		}
		parent := byID[s.ParentSpanID]
		if parent.RunID != s.ParentRunID || !strings.HasPrefix(parent.Name, "execute_tool") {
			t.Errorf("%s's invoke_agent hangs under %q of run %q, want its parent run's execute_tool", s.RunID, parent.Name, parent.RunID)
		}
	}

	// The plain route: the run's own spans, run_id on each, no parent.
	doc.Spans = nil
	getJSON(t, ts.URL+"/api/runs/"+director+"/spans", &doc)
	for _, s := range doc.Spans {
		if s.RunID != director || s.ParentRunID != "" {
			t.Errorf("plain span %s: run_id %q parent %q, want the run's, none", s.Name, s.RunID, s.ParentRunID)
		}
	}
	if len(doc.Spans) != 4 {
		t.Errorf("plain spans = %d, want the director's 4", len(doc.Spans))
	}
	// A child's tree is its own subtree.
	doc.Spans = nil
	getJSON(t, ts.URL+"/api/runs/"+ops+"/spans?tree=1", &doc)
	if len(doc.Spans) != 6 || doc.Spans[0].RunID != ops {
		t.Errorf("ops-manager's tree = %d spans from %q, want its 4 and worker's 2", len(doc.Spans), doc.Spans[0].RunID)
	}
	for path, want := range map[string]int{
		"/api/runs/" + director + "/spans?tree=maybe": http.StatusBadRequest,
		"/api/runs/nope/spans?tree=1":                 http.StatusNotFound,
		"/api/runs/" + director + "/spans?tree=0":     http.StatusOK,
	} {
		if code := getJSON(t, ts.URL+path, nil); code != want {
			t.Errorf("GET %s = %d, want %d", path, code, want)
		}
	}
}

// TestRunsListReadsChildrenPerLevel (U9.1; UX-E-24): the runs list
// prices a page's subagent trees from one ChildRuns read per tree level
// — the page, then its children, then theirs — never a children read
// per row; the run document's tree alike. The figures are the per-row
// path's: a database without a TreeReader answers the same costs (and
// reads children per run, which is the N+1 the item removes), leaves
// spans_tree off and answers ?tree=1 with 501.
func TestRunsListReadsChildrenPerLevel(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "weft.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cdb := countingTreeDB{&countingDB{DB: db}, db.(obsdb.TreeReader)}
	srv := studio.New(studio.DB(cdb))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	t.Cleanup(func() { _ = srv.Close() })
	directors := deepDelegation(t, ts.URL, 3)

	type costs struct {
		Runs []struct {
			ID               string   `json:"id"`
			CostUSD          *float64 `json:"cost_usd"`
			CostBadge        string   `json:"cost_badge"`
			DelegatedCostUSD *float64 `json:"delegated_cost_usd"`
			DelegatedKnown   bool     `json:"delegated_cost_known"`
			DelegatedReason  string   `json:"delegated_cost_reason"`
		} `json:"runs"`
	}
	cdb.reset()
	var tree costs
	if code := getJSON(t, ts.URL+"/api/runs", &tree); code != http.StatusOK || len(tree.Runs) != 3 {
		t.Fatalf("/api/runs = %d, %d rows; want the 3 director runs", code, len(tree.Runs))
	}
	perRun, _, childRuns, _, parents := cdb.reset()
	if perRun != 0 {
		t.Errorf("/api/runs read children per row %d times, want none", perRun)
	}
	// Three levels: the page's 3 directors, their 3 ops-managers, their
	// 3 workers (whose read finds no child and ends the walk).
	if childRuns != 3 || len(parents) != 3 || len(parents[0]) != 3 || len(parents[1]) != 3 || len(parents[2]) != 3 {
		t.Errorf("/api/runs read ChildRuns %d times over %v, want 3 reads of 3 parents (one per level)", childRuns, parents)
	}
	for _, r := range tree.Runs {
		if r.DelegatedCostUSD == nil || !r.DelegatedKnown || r.CostBadge != "scripted" {
			t.Errorf("run %s: cost %v %q, delegated %v %v %q; want the scripted $0 with its delegated figure known",
				r.ID, r.CostUSD, r.CostBadge, r.DelegatedCostUSD, r.DelegatedKnown, r.DelegatedReason)
		}
	}
	// The run document: the run's tree from ChildRuns, no per-run read.
	if code := getJSON(t, ts.URL+"/api/runs/"+directors[0], nil); code != http.StatusOK {
		t.Fatalf("run document = %d", code)
	}
	if perRun, _, _, _, _ := cdb.reset(); perRun != 0 {
		t.Errorf("the run document read children per run %d times, want none", perRun)
	}

	// Without a TreeReader: the same figures, the per-row path, no tree.
	plain := &countingDB{DB: db}
	srv2 := studio.New(studio.DB(plain))
	ts2 := httptest.NewServer(srv2.Handler())
	t.Cleanup(ts2.Close)
	t.Cleanup(func() { _ = srv2.Close() })
	var flat costs
	getJSON(t, ts2.URL+"/api/runs", &flat)
	a, _ := json.Marshal(tree)
	b, _ := json.Marshal(flat)
	if string(a) != string(b) {
		t.Errorf("costs from the tree reads differ from the per-row path's:\n tree %s\n rows %s", a, b)
	}
	if perRun, _, _, _, _ := plain.reset(); perRun < 6 {
		t.Errorf("the per-row path read children %d times, want one per run of the trees (the N+1 the tree reads remove)", perRun)
	}
	var meta struct {
		Capabilities    []string          `json:"capabilities"`
		CapabilitiesOff map[string]string `json:"capabilities_off"`
	}
	getJSON(t, ts2.URL+"/api/meta", &meta)
	if slices.Contains(meta.Capabilities, "spans_tree") || !strings.Contains(meta.CapabilitiesOff["spans_tree"], "obsdb.TreeReader") {
		t.Errorf("meta without a TreeReader: capabilities %v, off %v; want spans_tree off with its reason", meta.Capabilities, meta.CapabilitiesOff)
	}
	if code := getJSON(t, ts2.URL+"/api/runs/"+directors[0]+"/spans?tree=1", nil); code != http.StatusNotImplemented {
		t.Errorf("?tree=1 without a TreeReader = %d, want 501", code)
	}
	if code := getJSON(t, ts2.URL+"/api/runs/"+directors[0]+"/spans", nil); code != http.StatusOK {
		t.Errorf("plain spans without a TreeReader = %d, want 200", code)
	}
}
