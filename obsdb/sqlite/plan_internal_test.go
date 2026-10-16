package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// queryPlan is EXPLAIN QUERY PLAN's detail column, joined.
func queryPlan(t *testing.T, d *DB, q string, args ...any) string {
	t.Helper()
	rs, err := d.reads.QueryContext(context.Background(), `EXPLAIN QUERY PLAN `+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	var plan []string
	for rs.Next() {
		var id, parent, notused int
		var detail string
		if err := rs.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(plan, "; ")
}

func planDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db.(*DB)
}

// runListSQL is Runs' two statements over runWhere: the count and the
// first page.
func runListSQL(q obsdb.RunQuery) (count, page string, args []any) {
	where, args := runWhere(q, time.Now())
	return `SELECT COUNT(*) FROM runs WHERE ` + where,
		`SELECT run_id FROM runs WHERE ` + where + runOrder(q) + ` LIMIT 50`, args
}

// TestExperimentRunsUseTheIndex (M2): an experiment's run list — the
// filter GET /api/experiments reads once per experiment — is planned
// on runs_experiment (0006, rebuilt in 0014), never a scan of runs;
// the count and the page alike.
func TestExperimentRunsUseTheIndex(t *testing.T) {
	d := planDB(t)
	count, page, args := runListSQL(obsdb.RunQuery{ExperimentID: "exp_1"})
	for _, q := range []string{count, page} {
		if p := queryPlan(t, d, q, args...); !strings.Contains(p, "runs_experiment") {
			t.Errorf("plan of %q = %s, want the runs_experiment index", q, p)
		}
	}
}

// TestRunListsUseTheirIndexes (U27; UX-E-23, UX-E-36): each run list is
// planned on the index made for it (migration 0014), never a scan of
// runs or of every top-level run:
//
//   - the public id's list (the panel's) and a session's runs on the
//     partial indexes runs_public and runs_session_started — runWhere
//     states their own condition (<> ”) beside the equality, without
//     which sqlite never uses a partial index (the finding: both were
//     planned on runs_started, every top-level run walked);
//   - the all-runs list (all=1) in runs_all's order, no temp B-tree;
//   - the top-level list in runs_started's whole order (run_id
//     included), no temp B-tree;
//   - the live backfill's LastSeenAfter window on runs_last_seen, the
//     page too: runOrder spells its order +started_ns so runs_all's
//     order cannot win, and the few matches sort in a temp B-tree (the
//     finding, U27-fix: the page walked runs_all, testing every row
//     until the LIMIT filled).
//   - not under a public id or session equality (P7-fix): runOrder
//     keeps the plain spelling there and runs_public /
//     runs_session(_started) narrow the read — in their order under a
//     parent equality; with all=1 they lead with parent_run_id before
//     started_ns, so the matches of one public id or session sort in a
//     temp B-tree whichever spelling (pinned on the index only).
//
// The status filter, honestly: statusExpr's CASE takes a bind parameter
// (the interrupted cutoff), so no index serves it as such. runWhere
// adds the condition each status implies where an index can use it —
// failed: failed = 1, the partial index runs_failed (top-level, under
// the parent equality it leads with) and runs_failed_all (0015, the
// all-runs page's whole order over the failed rows alone); running:
// last_seen_ns >= cutoff, runs_last_seen (the all-runs page through
// runOrder's +started_ns as for LastSeenAfter; the top-level running
// list stays on runs_started, testing the CASE per row in order). succeeded and interrupted are
// most rows and imply nothing narrower: their lists walk the order
// index (runs_all / runs_started) and test the CASE per row — no
// covering index helps a CASE over four columns, and their counts read
// every candidate row (pinned below so a change is seen). Tag (meta)
// filters scan through json_each; a tag table is UX-E-27's (U22).
func TestRunListsUseTheirIndexes(t *testing.T) {
	d := planDB(t)
	for _, c := range []struct {
		name      string
		q         obsdb.RunQuery
		count     string // "" = not pinned
		page      string
		noTempBy  bool // the page's order is the index's: no temp B-tree
		countScan bool // the count reads every row (pinned honestly)
		search    bool // the page is a SEARCH on its index (a range, not a walk)
	}{
		{name: "public id", q: obsdb.RunQuery{PublicID: "pub_1"}, count: "runs_public", page: "runs_public", noTempBy: true},
		{name: "public id, all", q: obsdb.RunQuery{PublicID: "pub_1", ParentRunID: "*"}, count: "runs_public", page: "runs_public"},
		{name: "session", q: obsdb.RunQuery{SessionID: "s_1"}, count: "runs_session_started", page: "runs_session_started", noTempBy: true},
		{name: "all runs", q: obsdb.RunQuery{ParentRunID: "*"}, page: "runs_all", noTempBy: true},
		{name: "top-level", q: obsdb.RunQuery{}, count: "runs_started", page: "runs_started", noTempBy: true},
		{name: "last seen after", q: obsdb.RunQuery{ParentRunID: "*", LastSeenAfter: time.Now()}, count: "runs_last_seen", page: "runs_last_seen", search: true},
		{name: "failed", q: obsdb.RunQuery{Status: obsdb.StatusFailed}, count: "runs_failed", page: "runs_failed", noTempBy: true},
		{name: "failed, all", q: obsdb.RunQuery{Status: obsdb.StatusFailed, ParentRunID: "*"}, count: "runs_failed_all", page: "runs_failed_all", noTempBy: true},
		{name: "running, all", q: obsdb.RunQuery{Status: obsdb.StatusRunning, ParentRunID: "*"}, count: "runs_last_seen", page: "runs_last_seen", search: true},
		{name: "running", q: obsdb.RunQuery{Status: obsdb.StatusRunning}, count: "runs_started", page: "runs_started", noTempBy: true},
		{name: "public id, all, last seen after", q: obsdb.RunQuery{PublicID: "pub_1", ParentRunID: "*", LastSeenAfter: time.Now()}, count: "runs_public", page: "runs_public"},
		{name: "public id, last seen after", q: obsdb.RunQuery{PublicID: "pub_1", LastSeenAfter: time.Now()}, count: "runs_public", page: "runs_public", noTempBy: true},
		{name: "session, all, running", q: obsdb.RunQuery{SessionID: "s_1", ParentRunID: "*", Status: obsdb.StatusRunning}, count: "runs_session_started", page: "runs_session"},
		{name: "session, running", q: obsdb.RunQuery{SessionID: "s_1", Status: obsdb.StatusRunning}, count: "runs_session_started", page: "runs_session_started", noTempBy: true},
		{name: "succeeded, all", q: obsdb.RunQuery{Status: obsdb.StatusSucceeded, ParentRunID: "*"}, page: "runs_all", countScan: true},
	} {
		count, page, args := runListSQL(c.q)
		cp, pp := queryPlan(t, d, count, args...), queryPlan(t, d, page, args...)
		if c.count != "" && !usesIndex(cp, c.count) {
			t.Errorf("%s: count plan = %s, want the %s index", c.name, cp, c.count)
		}
		if c.page != "" && !usesIndex(pp, c.page) {
			t.Errorf("%s: page plan = %s, want the %s index", c.name, pp, c.page)
		}
		if c.search && !strings.HasPrefix(pp, "SEARCH runs USING INDEX "+c.page+" (") {
			t.Errorf("%s: page plan = %s, want a SEARCH on %s", c.name, pp, c.page)
		}
		if c.noTempBy && strings.Contains(pp, "TEMP B-TREE") {
			t.Errorf("%s: page plan = %s, want the index's order (no temp B-tree)", c.name, pp)
		}
		if c.countScan && cp != "SCAN runs" {
			t.Errorf("%s: count plan = %s; the pin says it reads every row — update the doc above if an index now serves it", c.name, cp)
		}
	}
}

// usesIndex reports whether plan names index whole — runs_failed is not
// runs_failed_all.
func usesIndex(plan, index string) bool {
	for _, step := range strings.Split(plan, "; ") {
		if strings.Contains(step+" ", "INDEX "+index+" ") {
			return true
		}
	}
	return false
}

// TestSessionListUsesTheSessionIndex (U27; UX-E-23): the session list's
// per-session reads — the newest turn's status, the title, the first
// prompt, content off — are correlated subqueries keyed on session_id;
// stating r.session_id <> ” lets each use runs_session (0001) or
// runs_session_started (0014) instead
// of walking every top-level run once per listed session. The list
// scoped to a public id groups on runs_public, and ResolvePublicID
// uses it too.
func TestSessionListUsesTheSessionIndex(t *testing.T) {
	d := planDB(t)
	for _, pub := range []string{"", "pub_1"} {
		where, wargs := sessionScope, []any(nil)
		if pub != "" {
			where += ` AND public_id = ? AND public_id <> ''`
			wargs = []any{pub}
		}
		q, args := sessionRowsSQL(0, pub, where, wargs, nil, nil, ` ORDER BY MAX(last_seen_ns) DESC, session_id DESC LIMIT ?`)
		p := queryPlan(t, d, q, append(args, 50)...)
		for _, alias := range []string{"r2", "r3", "r4", "r5"} {
			if !strings.Contains(p, "SEARCH "+alias+" USING INDEX runs_session") {
				t.Errorf("public id %q: session list plan = %s; want %s on runs_session or runs_session_started", pub, p, alias)
			}
		}
		if pub != "" && !strings.Contains(p, "SEARCH runs USING INDEX runs_public") {
			t.Errorf("public id %q: session list plan = %s; want the grouping on runs_public", pub, p)
		}
	}
	if p := queryPlan(t, d, `SELECT session_id FROM runs WHERE public_id = ? AND public_id <> '' AND session_id <> ''
		ORDER BY turn DESC, started_ns DESC, run_id DESC LIMIT 1`, "pub_1"); !strings.Contains(p, "runs_public") {
		t.Errorf("ResolvePublicID plan = %s, want runs_public", p)
	}
}

// TestTracesWindowUsesSpansStart (U32.2, UX-E-64): the traces list's
// Since window — the count and the page alike — narrows on spans_start
// (migration 0019), a SEARCH over the range, each candidate trace's
// group then read on the primary key; without a window the list is one
// scan of the primary key's order, pinned so a change is seen.
func TestTracesWindowUsesSpansStart(t *testing.T) {
	d := planDB(t)
	group, having, args := traceGroup(obsdb.TraceQuery{Since: time.Now().Add(-time.Hour)})
	for _, q := range []string{
		`SELECT COUNT(*) FROM (` + group + having + `)`,
		group + having + ` ORDER BY MIN(start_ns) DESC, trace_id DESC LIMIT 50`,
	} {
		p := queryPlan(t, d, q, args...)
		if !strings.Contains(p, "SEARCH spans USING COVERING INDEX spans_start (start_ns>?)") ||
			!strings.Contains(p, "SEARCH spans USING PRIMARY KEY (trace_id=?)") {
			t.Errorf("plan of %q = %s, want a SEARCH on spans_start for the window and the groups on the primary key", q, p)
		}
	}
	group, having, args = traceGroup(obsdb.TraceQuery{})
	if p := queryPlan(t, d, `SELECT COUNT(*) FROM (`+group+having+`)`, args...); strings.Contains(p, "spans_start") {
		t.Errorf("unwindowed count plan = %s; the pin says one scan of the primary key — update the doc above", p)
	}
}

// TestResumedByUsesItsIndex (U13.1-fix): the run row's resumed_by is a
// correlated lookup per row read — the latest resumer by start time —
// planned on runs_resumed_from (0018) by its equality, never a scan of
// runs per listed row. The few resumers the equality finds are sorted.
func TestResumedByUsesItsIndex(t *testing.T) {
	d := planDB(t)
	plan := queryPlan(t, d, `SELECT run_id, `+resumedByColumn+` FROM runs WHERE run_id = ?`, "r_1")
	if !usesIndex(plan, "runs_resumed_from") {
		t.Errorf("resumed_by lookup does not use runs_resumed_from: %s", plan)
	}
	if strings.Contains(plan, "SCAN rb") {
		t.Errorf("resumed_by lookup scans runs: %s", plan)
	}
}

// TestRunSpansUseTheirIndexes (U9.1; UX-E-22, UX-E-34): a run's spans
// are planned on the partial index spans_run (0001) — runSpansWhere
// states its condition (run_id <> ”) beside the equality, without
// which sqlite scanned the whole spans table and sorted it — and a run
// tree's one read (treeSpansSQL, GET /api/runs/{id}/spans?tree=1) on
// spans_trace_start (0020): the trace in start order, no temp B-tree,
// each span's run row by runs' primary key. Trace and a derived run's
// spans read the same trace order.
func TestRunSpansUseTheirIndexes(t *testing.T) {
	d := planDB(t)
	if p := queryPlan(t, d, `SELECT `+spanColumns+` FROM spans WHERE `+runSpansWhere, "r_1"); !strings.HasPrefix(p, "SEARCH spans USING INDEX spans_run (run_id=?)") {
		t.Errorf("RunSpans plan = %s, want a SEARCH on spans_run", p)
	}
	tree := queryPlan(t, d, treeSpansSQL, "0102030405060708090a0b0c0d0e0f10")
	if !strings.Contains(tree, "SEARCH s USING INDEX spans_trace_start (trace_id=?)") ||
		!strings.Contains(tree, "SEARCH r USING INDEX sqlite_autoindex_runs_1 (run_id=?)") ||
		strings.Contains(tree, "TEMP B-TREE") || strings.Contains(tree, "SCAN") {
		t.Errorf("RunTreeSpans plan = %s, want spans_trace_start in its order and runs by key, no scan, no sort", tree)
	}
	if p := queryPlan(t, d, `SELECT `+spanColumns+` FROM spans WHERE trace_id = ? ORDER BY start_ns, span_id`, "t"); !strings.Contains(p, "spans_trace_start") || strings.Contains(p, "TEMP B-TREE") {
		t.Errorf("Trace plan = %s, want spans_trace_start's order", p)
	}
}

// TestChildRunsUseTheIndex (U9.1; UX-E-24): the runs list's children —
// a page's parents in one statement — are planned on runs_started
// (0014: parent_run_id leads it), never a scan of runs.
func TestChildRunsUseTheIndex(t *testing.T) {
	d := planDB(t)
	p := queryPlan(t, d, `SELECT run_id FROM runs WHERE `+childRunsWhere(3), "a", "b", "c")
	if !usesIndex(p, "runs_started") || strings.Contains(p, "SCAN runs") {
		t.Errorf("ChildRuns plan = %s, want runs_started, no scan", p)
	}
}
