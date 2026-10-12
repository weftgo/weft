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
