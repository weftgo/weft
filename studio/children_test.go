package studio

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
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
// run as weft recorded it before 0.18, linked by weft.parent.run.id)
// included, which the session's own walk does not hold.
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

// TestOwnDelegatedUsage (U30.2-fix, UX-A-19): own_usage / delegated_usage
// split a delegating run's usage only where every Subagent child is
// read and in scope. A panel token that cannot see one child gets
// neither (usage − own − delegated would be the hidden subtree's
// figure, I4's rule); a failed or capped children read omits both.
func TestOwnDelegatedUsage(t *testing.T) {
	db := costDB(t)
	t0 := time.Now().Add(-time.Hour).UTC()
	own := core.Usage{InputTokens: 100, OutputTokens: 10}
	mine := core.Usage{InputTokens: 20, OutputTokens: 2}
	theirs := core.Usage{InputTokens: 7, OutputTokens: 3}
	costUsagePub(t, db, "p", "", "", "pub_a", "anthropic", "claude-opus-5-5", t0, own.Add(mine).Add(theirs), own)
	costUsagePub(t, db, "p/0/c1", "p", "", "pub_a", "anthropic", "claude-opus-5-5", t0.Add(time.Second), mine, mine)
	costUsagePub(t, db, "p/0/c2", "p", "", "pub_b", "anthropic", "claude-opus-5-5", t0.Add(2*time.Second), theirs, theirs)
	h := Handler(DB(db), Token("srv"))
	panel, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	type split struct {
		Own *core.Usage `json:"own_usage"`
		Del *core.Usage `json:"delegated_usage"`
	}
	read := func(path, tok string) split {
		t.Helper()
		code, _, body := getWith(t, h, path, tok, "")
		if code != 200 {
			t.Fatalf("%s: %d %s", path, code, body)
		}
		var s split
		if strings.HasPrefix(path, "/studio/api/runs?") {
			var page struct{ Runs []split }
			decode(t, body, &page)
			if len(page.Runs) != 1 {
				t.Fatalf("%s: %s", path, body)
			}
			return page.Runs[0]
		}
		decode(t, body, &s)
		return s
	}
	for _, path := range []string{"/studio/api/runs/p", "/studio/api/runs?public_id=pub_a"} {
		s := read(path, "srv")
		if s.Own == nil || *s.Own != own || s.Del == nil || *s.Del != mine.Add(theirs) {
			t.Errorf("server %s: own %+v delegated %+v; want %+v and %+v", path, s.Own, s.Del, own, mine.Add(theirs))
		}
		if s := read(path, panel); s.Own != nil || s.Del != nil {
			t.Errorf("panel token, a hidden child, %s: own %+v delegated %+v; want neither", path, s.Own, s.Del)
		}
	}
	// A failed children read (the list's per-row read): neither.
	code, _, body := get(t, Handler(DB(failingChildren{db})), "/studio/api/runs")
	if code != 200 || !strings.Contains(body, `"children_unknown":true`) || strings.Contains(body, "own_usage") || strings.Contains(body, "delegated_usage") {
		t.Errorf("failing children read: %d %s; want no split", code, body)
	}
}

// TestOwnDelegatedUsageCapped: a run whose children are past the
// coster's bound carries no split (its own usage cannot be told).
func TestOwnDelegatedUsageCapped(t *testing.T) {
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
	for i, g := range []string{"g1", "g2", "g3"} {
		seedAt(t, db, "p/0/c1/0/"+g, "", "p/0/c1", at.Add(time.Duration(2+i)*time.Second), true)
	}
	_, _, body := get(t, Handler(DB(db)), "/studio/api/runs?all=1")
	var page struct {
		Runs []struct {
			ID  string      `json:"id"`
			Own *core.Usage `json:"own_usage"`
			Del *core.Usage `json:"delegated_usage"`
		}
	}
	decode(t, body, &page)
	found := false
	for _, r := range page.Runs {
		found = found || r.ID == "p/0/c1"
		if r.ID == "p/0/c1" && (r.Own != nil || r.Del != nil) {
			t.Errorf("a capped children read: %s carries own %+v delegated %+v; want neither", r.ID, r.Own, r.Del)
		}
	}
	if !found {
		t.Fatalf("p/0/c1 not listed: %s", body)
	}
}
