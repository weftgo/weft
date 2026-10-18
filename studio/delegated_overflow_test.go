package studio

import (
	"fmt"

	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// TestDelegatedOverflow (U14-fix): a session delegating more sessions
// than a document lists. The rows stop at delegatedCap with
// delegated_more, but the delegated figures are the whole tree's — the
// direct level paged whole, a deeper level read once (Parents IN) —
// so the cost stays known over every one of them, never over the
// listed hundred alone. The lead turn's caller_linked rows are the
// oldest callerLinkedCap, in start order, with caller_linked_more.
// Past delegatedTreeCap the figures are not known and the reason says
// why.
func TestDelegatedOverflow(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/overflow.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedRun(t, db, "s_lead-t1", "", "s_lead", "", nil)
	const n = delegatedCap + 5
	for i := range n {
		id := fmt.Sprintf("s_c%03d", i)
		seedRun(t, db, id+"-t1", "", id, "", map[string]any{
			"weft.session.parent": "s_lead", "weft.session.parent_call": fmt.Sprintf("d%d", i),
			"weft.caller.run.id": "s_lead-t1", "weft.caller.call.id": fmt.Sprintf("d%d", i),
		})
	}
	seedRun(t, db, "s_g-t1", "", "s_g", "", map[string]any{
		"weft.session.parent": "s_c000", "weft.session.parent_call": "g1",
		"weft.caller.run.id": "s_c000-t1", "weft.caller.call.id": "g1",
	})
	srv := New(DB(db))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	type sessionDocBody struct {
		Delegated      []struct{ ID string } `json:"delegated"`
		DelegatedMore  bool                  `json:"delegated_more"`
		DelegatedUsage core.Usage            `json:"delegated_usage"`
		CostUSD        *float64              `json:"delegated_sessions_cost_usd"`
		CostKnown      bool                  `json:"delegated_sessions_cost_known"`
		CostReason     string                `json:"delegated_sessions_cost_reason"`
	}
	var doc sessionDocBody
	getUntil(t, ts, "/api/sessions/s_lead", &doc, func() bool { return true })
	if len(doc.Delegated) != delegatedCap || !doc.DelegatedMore {
		t.Errorf("delegated rows = %d, more %v; want %d and more", len(doc.Delegated), doc.DelegatedMore, delegatedCap)
	}
	if doc.DelegatedUsage.InputTokens != n+1 || !doc.CostKnown || doc.CostUSD == nil || *doc.CostUSD != 0 || strings.Contains(doc.CostReason, "too many") {
		t.Errorf("delegated figures: usage %+v, cost %v known %v reason %q; want %d in (every delegated session, the grandchild too), $0 known",
			doc.DelegatedUsage, doc.CostUSD, doc.CostKnown, doc.CostReason, n+1)
	}

	var run struct {
		CallerLinked     []struct{ ID string } `json:"caller_linked"`
		CallerLinkedMore bool                  `json:"caller_linked_more"`
	}
	getUntil(t, ts, "/api/runs/s_lead-t1", &run, func() bool { return true })
	if len(run.CallerLinked) != callerLinkedCap || !run.CallerLinkedMore ||
		run.CallerLinked[0].ID != "s_c000-t1" || run.CallerLinked[callerLinkedCap-1].ID != fmt.Sprintf("s_c%03d-t1", callerLinkedCap-1) {
		t.Errorf("caller_linked = %d rows (more %v); want the oldest %d in start order, s_c000-t1 first", len(run.CallerLinked), run.CallerLinkedMore, callerLinkedCap)
	}

	// The tree cap: past delegatedTreeCap sessions the figures are not
	// known, the reason names the cap.
	for i := range delegatedTreeCap {
		id := fmt.Sprintf("s_x%03d", i)
		seedRun(t, db, id+"-t1", "", id, "", map[string]any{
			"weft.session.parent": "s_c001", "weft.caller.run.id": "s_c001-t1",
		})
	}
	var capped sessionDocBody
	getUntil(t, ts, "/api/sessions/s_lead", &capped, func() bool { return true })
	if capped.CostKnown || capped.CostUSD != nil || !strings.Contains(capped.CostReason, "too many to price") {
		t.Errorf("past the tree cap: cost %v known %v reason %q; want unknown, the cap named", capped.CostUSD, capped.CostKnown, capped.CostReason)
	}
}
