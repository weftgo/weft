package studio

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
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
	wantCap := fmt.Sprintf("too many to price at read time; delegated_usage counts the first %d sessions", delegatedTreeCap)
	if capped.CostKnown || capped.CostUSD != nil || !strings.Contains(capped.CostReason, wantCap) {
		t.Errorf("past the tree cap: cost %v known %v reason %q; want unknown, the cap named and the usage a floor (%q)", capped.CostUSD, capped.CostKnown, capped.CostReason, wantCap)
	}
	if capped.DelegatedUsage.InputTokens != delegatedTreeCap {
		t.Errorf("past the tree cap: delegated usage %+v, want the first %d sessions' (the floor the reason names)", capped.DelegatedUsage, delegatedTreeCap)
	}

	// A level that cannot be read: the figures are not known, the
	// reason names the level and says the usage stops above it.
	fsrv := New(DB(failDeepSessions{DB: db, root: "s_lead"}))
	t.Cleanup(func() { _ = fsrv.Close() })
	fts := httptest.NewServer(fsrv.Handler())
	t.Cleanup(fts.Close)
	var failed sessionDocBody
	getUntil(t, fts, "/api/sessions/s_lead", &failed, func() bool { return true })
	wantFail := "the sessions delegated 2 level(s) under this one could not be read; delegated_usage stops at the levels read"
	if failed.CostKnown || failed.CostUSD != nil || !strings.Contains(failed.CostReason, wantFail) {
		t.Errorf("a failed level: cost %v known %v reason %q; want unknown, %q", failed.CostUSD, failed.CostKnown, failed.CostReason, wantFail)
	}
	if failed.DelegatedUsage.InputTokens != n {
		t.Errorf("a failed level: delegated usage %+v, want the direct level's %d alone", failed.DelegatedUsage, n)
	}
}

// failDeepSessions fails every delegated read below root's own level.
type failDeepSessions struct {
	obsdb.DB
	root string
}

func (f failDeepSessions) Sessions(ctx context.Context, q obsdb.SessionQuery) (obsdb.SessionPage, error) {
	if len(q.Parents) > 0 && !slices.Equal(q.Parents, []string{f.root}) {
		return obsdb.SessionPage{}, errors.New("deep level unreadable")
	}
	return f.DB.Sessions(ctx, q)
}

// TestDelegatedCache (U14-fix2): delegated_sessions_cache is kept from
// each delegated session's own cost, never from the summed usage — one
// session with a cache split and one without read not_reported, though
// the sum carries a split; only an all-exact set reads exact.
func TestDelegatedCache(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/cache.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// A real model's turns (seedRun's scripted model reads exact $0
	// whatever its usage): one with a cache split, one without.
	at := time.Now().UTC()
	turn := func(session, parent string, cached int) {
		t.Helper()
		run := session + "-t1"
		attrs := func(evType string, pos int64) map[string]any {
			a := map[string]any{"weft.record": "event", "weft.run.id": run, "gen_ai.agent.name": "acme-support",
				"weft.session.id": session, "weft.turn": "1", "weft.event.type": evType, "weft.event.pos": pos}
			if parent != "" {
				a["weft.session.parent"] = parent
			}
			return a
		}
		rec := func(evType string, pos int64, body string) obsdb.Record {
			return obsdb.Record{Time: at.Add(time.Duration(pos) * time.Millisecond), EventName: "weft.event", Severity: 9,
				Body: body, Service: "svc", Attrs: attrs(evType, pos), Resource: map[string]any{"service.name": "svc"}}
		}
		if err := db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{
			rec("run_start", 0, `{"type":"run_start","id":"`+run+`","model":{"provider":"openai","name":"gpt-5"},"agent":"acme-support"}`),
			rec("run_finish", 1, fmt.Sprintf(`{"type":"run_finish","run_id":%q,"usage":{"input_tokens":10,"output_tokens":1,"cached_input_tokens":%d},"steps":1}`, run, cached)),
		}}); err != nil {
			t.Fatal(err)
		}
	}
	turn("s_mixed", "", 0)
	turn("s_exact", "s_mixed", 4)
	turn("s_plain", "s_mixed", 0)
	turn("s_clean", "", 0)
	turn("s_e2", "s_clean", 4)
	srv := New(DB(db))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	type body struct {
		Delegated      []struct{ ID, Cache string } `json:"delegated"`
		DelegatedUsage core.Usage                   `json:"delegated_usage"`
		Cache          string                       `json:"delegated_sessions_cache"`
	}
	var mixed body
	getUntil(t, ts, "/api/sessions/s_mixed", &mixed, func() bool { return true })
	if len(mixed.Delegated) != 2 || mixed.DelegatedUsage.CachedInputTokens != 4 || mixed.Cache != cacheNotReported {
		t.Errorf("one exact, one not_reported: rows %+v, usage %+v, cache %q; want 2 rows, the split summed, %q",
			mixed.Delegated, mixed.DelegatedUsage, mixed.Cache, cacheNotReported)
	}
	var clean body
	getUntil(t, ts, "/api/sessions/s_clean", &clean, func() bool { return true })
	if clean.Cache != cacheExact {
		t.Errorf("every delegated session exact: cache %q, want %q", clean.Cache, cacheExact)
	}
}
