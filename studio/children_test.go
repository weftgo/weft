package studio

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// failingChildren is an obsdb.DB whose children reads fail: a Runs
// query naming a parent run errors, and it is no obsdb.TreeReader (the
// embedded interface hides the backend's ChildRuns), so the coster
// reads each run's children through that failing query.
type failingChildren struct{ obsdb.DB }

func (f failingChildren) Runs(ctx context.Context, q obsdb.RunQuery) (obsdb.RunPage, error) {
	if q.ParentRunID != "" && q.ParentRunID != "*" {
		return obsdb.RunPage{}, errors.New("children read refused")
	}
	return f.DB.Runs(ctx, q)
}

// TestChildrenCountKnown (P8-fix-b #8): a row whose children read
// failed carries children_unknown (the UI's "⑂ ?"), never an absent
// count that reads as "no subagents"; a known count omits it, the
// failures counted in children_failed.
func TestChildrenCountKnown(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	at := time.Now().Add(-time.Hour)
	seedAt(t, db, "p", "", "", at, true)
	seedAt(t, db, "p/0/c1", "", "p", at.Add(time.Second), true)
	seedAt(t, db, "p/0/c2", "", "p", at.Add(2*time.Second), false) // its end never arrived: stale, interrupted

	type row struct {
		ID              string `json:"id"`
		ChildrenCount   *int   `json:"children_count"`
		ChildrenFailed  *int   `json:"children_failed"`
		ChildrenUnknown *bool  `json:"children_unknown"`
	}
	var page struct{ Runs []row }
	_, _, body := get(t, Handler(DB(db)), "/studio/api/runs")
	decode(t, body, &page)
	if len(page.Runs) != 1 || page.Runs[0].ChildrenCount == nil || *page.Runs[0].ChildrenCount != 2 || page.Runs[0].ChildrenUnknown != nil {
		t.Fatalf("known: runs = %s, want p with children_count 2 and no children_unknown", body)
	}

	page.Runs = nil
	_, _, body = get(t, Handler(DB(failingChildren{db})), "/studio/api/runs")
	decode(t, body, &page)
	if len(page.Runs) != 1 {
		t.Fatalf("failing: runs = %s, want p", body)
	}
	r := page.Runs[0]
	if r.ChildrenUnknown == nil || !*r.ChildrenUnknown || r.ChildrenCount != nil || r.ChildrenFailed != nil {
		t.Errorf("failing: row = %s, want children_unknown true and no count", body)
	}
}

// TestChildrenPrimeCapped: past the coster's child-row bound the page's
// trees are not read further, nor run by run: the capped runs' counts
// read children_unknown and their parent's delegated cost not known.
func TestChildrenPrimeCapped(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	prev := primeChildCap
	primeChildCap = 2
	t.Cleanup(func() { primeChildCap = prev })
	at := time.Now().Add(-time.Hour)
	seedAt(t, db, "p", "", "", at, true)
	seedAt(t, db, "p/0/c1", "", "p", at.Add(time.Second), true)
	seedAt(t, db, "p/0/c1/0/g1", "", "p/0/c1", at.Add(2*time.Second), true)
	seedAt(t, db, "p/0/c1/0/g2", "", "p/0/c1", at.Add(3*time.Second), true)
	seedAt(t, db, "p/0/c1/0/g3", "", "p/0/c1", at.Add(4*time.Second), true)

	var page struct {
		Runs []struct {
			ID                 string `json:"id"`
			ChildrenCount      *int   `json:"children_count"`
			ChildrenUnknown    *bool  `json:"children_unknown"`
			DelegatedCostKnown bool   `json:"delegated_cost_known"`
			DelegatedReason    string `json:"delegated_cost_reason"`
		}
	}
	_, _, body := get(t, Handler(DB(db)), "/studio/api/runs")
	decode(t, body, &page)
	if len(page.Runs) != 1 {
		t.Fatalf("runs = %s", body)
	}
	r := page.Runs[0]
	// p's one child was read (1 ≤ 2); c1's three were past the bound.
	if r.ChildrenCount == nil || *r.ChildrenCount != 1 || r.ChildrenUnknown != nil || r.DelegatedCostKnown ||
		!strings.Contains(r.DelegatedReason, "past the 10000 child runs") {
		t.Errorf("row = %s, want p's one child counted, its delegated cost not known past the cap", body)
	}
}

// TestSessionTurnChildren (P8-fix-b #8): /api/sessions/{id}'s turn rows
// count every parent-linked run, as the runs list's do — a run under a
// turn that carries another session id (a thread/pool child session's
// run) included, which the session's own walk does not hold.
func TestSessionTurnChildren(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	at := time.Now().Add(-time.Hour)
	seedAt(t, db, "s_a-t1", "s_a", "", at, true)
	seedAt(t, db, "s_a-t1/0/c1", "s_a", "s_a-t1", at.Add(time.Second), true)
	seedAt(t, db, "s_pool-t1", "s_pool", "s_a-t1", at.Add(2*time.Second), true)

	var doc struct {
		Runs []struct {
			ID            string `json:"id"`
			ChildrenCount int    `json:"children_count"`
		} `json:"runs"`
	}
	_, _, body := get(t, Handler(DB(db)), "/studio/api/sessions/s_a")
	decode(t, body, &doc)
	for _, r := range doc.Runs {
		if r.ID == "s_a-t1" {
			if r.ChildrenCount != 2 {
				t.Errorf("s_a-t1 children_count = %d, want 2 (its Subagent child and the pool child session's run): %s", r.ChildrenCount, body)
			}
			return
		}
	}
	t.Fatalf("no s_a-t1 among the session's runs: %s", body)
}
