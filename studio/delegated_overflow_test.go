package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
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

// countRuns counts the Runs reads a page makes.
type countRuns struct {
	obsdb.DB
	n *atomic.Int64
}

func (c countRuns) Runs(ctx context.Context, q obsdb.RunQuery) (obsdb.RunPage, error) {
	c.n.Add(1)
	return c.DB.Runs(ctx, q)
}

// TestDelegatedReadsPerLevel (P9-pre-go-fix): a session document prices
// its delegated tree with one Runs read per level (RunQuery.SessionIDs),
// never one per delegated session — a hundred and five sessions and a
// grandchild cost a handful of reads, the figures what they were.
func TestDelegatedReadsPerLevel(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/levels.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedRun(t, db, "s_lead-t1", "", "s_lead", "", nil)
	const n = delegatedCap + 5
	for i := range n {
		id := fmt.Sprintf("s_c%03d", i)
		seedRun(t, db, id+"-t1", "", id, "", map[string]any{"weft.session.parent": "s_lead", "weft.caller.run.id": "s_lead-t1"})
	}
	seedRun(t, db, "s_g-t1", "", "s_g", "", map[string]any{"weft.session.parent": "s_c000", "weft.caller.run.id": "s_c000-t1"})
	var reads atomic.Int64
	srv := New(DB(countRuns{DB: db, n: &reads}))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	var doc struct {
		DelegatedUsage core.Usage `json:"delegated_usage"`
		CostKnown      bool       `json:"delegated_sessions_cost_known"`
	}
	reads.Store(0)
	getUntil(t, ts, "/api/sessions/s_lead", &doc, func() bool { return true })
	if doc.DelegatedUsage.InputTokens != n+1 || !doc.CostKnown {
		t.Errorf("delegated figures = %+v known %v, want %d in, known", doc.DelegatedUsage, doc.CostKnown, n+1)
	}
	if got := reads.Load(); got > 10 {
		t.Errorf("GET /api/sessions/s_lead made %d Runs reads; want a few per tree level, not one per delegated session (%d)", got, n+1)
	}
	// P9-pre-go-fix2: a level past one batch's budget. 400 sessions × 30
	// turns, 12,000 runs: 8 batches of 50 × 1,500 runs, 4 pages each
	// (33 reads with the lead's own walk). 50 × 210, 10,500 runs in one
	// batch: past the bound on its first page's Total, then two halves
	// of 5,250 (11 pages each) — 23 reads of the level's budget of 40.
	t.Run("a level past one batch", func(t *testing.T) { delegatedWide(t, 400, 30, 40) })
	t.Run("an overflowing batch halves", func(t *testing.T) { delegatedWide(t, 50, 210, 50) })
	// P9-pre-go-fix3: a session of exactly 10,000 runs is whole, never
	// "more than 10000" (its 20th page reads the count out: no 21st);
	// the lead's walk, the level read, 20 pages.
	t.Run("exactly 10,000 runs is known", func(t *testing.T) { delegatedWide(t, 1, sessionCostPages*500, 25) })
	// P9-pre-go-fix3: every session past the bound. The reads stay in
	// sessionCosts' budget — 20 pages a batch and 20 for the halvings,
	// not 20 a session — and every session answers unknown.
	t.Run("every session past the bound", func(t *testing.T) {
		for _, n := range []int{50, 500} {
			delegatedHuge(t, n, int64(sessionCostReads(n)))
		}
	})
}

// hugeRuns answers every RunQuery.SessionIDs read as if each session
// held sessionCostPages × 500 + 1 runs: a Total past the bound, a full
// page of 500 and a cursor on every page.
type hugeRuns struct {
	obsdb.DB
	n *atomic.Int64
}

func (h hugeRuns) Runs(ctx context.Context, q obsdb.RunQuery) (obsdb.RunPage, error) {
	h.n.Add(1)
	if len(q.SessionIDs) == 0 {
		return h.DB.Runs(ctx, q)
	}
	at := time.Now().Add(-time.Hour)
	page := obsdb.RunPage{Total: len(q.SessionIDs) * (sessionCostPages*500 + 1), NextBefore: &at, NextBeforeID: "x"}
	for i := range 500 {
		sid := q.SessionIDs[i%len(q.SessionIDs)]
		page.Runs = append(page.Runs, obsdb.RunRow{ID: fmt.Sprintf("%s-t%d", sid, i), SessionID: sid, Started: at, LastSeen: at})
	}
	return page, nil
}

// delegatedHuge prices n delegated sessions every one past the bound:
// at most maxReads Runs reads, every session unknown.
func delegatedHuge(t *testing.T, n int, maxReads int64) {
	t.Helper()
	db, err := sqlite.Open(t.TempDir() + "/huge.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var reads atomic.Int64
	srv := New(DB(hugeRuns{DB: db, n: &reads}))
	t.Cleanup(func() { _ = srv.Close() })
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("s_h%03d", i)
	}
	out := srv.sessionCosts(context.Background(), ids, "")
	if got := reads.Load(); got > maxReads {
		t.Errorf("%d sessions past the bound: %d Runs reads, want at most %d (the call's budget)", n, got, maxReads)
	}
	if len(out) != n {
		t.Fatalf("%d sessions priced, want every one of %d", len(out), n)
	}
	// P9-pre-go-fix4: the budget's reason names the pages the budget
	// read — 20 per batch and 20 more — never "more than 20".
	wantBudget := fmt.Sprintf("(%d pages of 500 read)", sessionCostReads(n))
	for _, id := range ids {
		c, ok := out[id]
		if !ok || c.known || c.badge != costUnknown || c.reason == "" {
			t.Errorf("session %s: %+v; want unknown with a reason", id, c)
		}
		if strings.Contains(c.reason, "pages of 500") && !strings.Contains(c.reason, wantBudget) {
			t.Errorf("session %s: reason %q, want the budget's figure %q", id, c.reason, wantBudget)
		}
	}
}

// shrinkingRuns answers a session's runs as a store that held 1,500
// runs when the first page was read and lost 500 of the runs that page
// returned before the second (a concurrent delete): pages of 500 under
// a cursor, the first counting 1,500, every later one 1,000 — so after
// two pages the runs read (1,000) reach the fresh count while a third
// page of 500 is still unread.
type shrinkingRuns struct {
	obsdb.DB
	n *atomic.Int64
}

func (s shrinkingRuns) Runs(ctx context.Context, q obsdb.RunQuery) (obsdb.RunPage, error) {
	s.n.Add(1)
	sid := q.SessionID
	if len(q.SessionIDs) > 0 {
		sid = q.SessionIDs[0]
	}
	page := 0
	if !q.Before.IsZero() {
		_, _ = fmt.Sscanf(q.BeforeID, "p%d", &page)
	}
	out := obsdb.RunPage{Total: 1000}
	if page == 0 {
		out.Total = 1500
	}
	if page >= 3 {
		return out, nil
	}
	at := time.Now().Add(-time.Duration(page+1) * time.Hour)
	for i := range 500 {
		out.Runs = append(out.Runs, obsdb.RunRow{ID: fmt.Sprintf("%s-p%d-%d", sid, page, i), SessionID: sid, Started: at, LastSeen: at})
	}
	cursor := fmt.Sprintf("p%d", page+1)
	out.NextBefore, out.NextBeforeID = &at, cursor
	return out, nil
}

// TestSessionCostSurvivesADeleteBetweenPages (P9-pre-go-fix4): the walk
// ends on the cursor or an empty page, never on a count that fell
// between pages: sessionCost reads all three pages (1,500 runs, then
// the empty fourth) and sessionCosts' batch reads them too, where a
// "read ≥ Total" end would have stopped after two and priced the
// session as known without its last 500 runs. The count only decides
// at the bound (exactly 10,000 runs known: TestDelegatedReadsPerLevel).
func TestSessionCostSurvivesADeleteBetweenPages(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/shrink.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var reads atomic.Int64
	srv := New(DB(shrinkingRuns{DB: db, n: &reads}))
	t.Cleanup(func() { _ = srv.Close() })
	_, _, runs := srv.sessionCost(context.Background(), "s_shrink", "")
	if len(runs) != 1500 || reads.Load() != 4 {
		t.Errorf("sessionCost read %d runs in %d reads; want 1,500 in 4 (three pages, then the empty one)", len(runs), reads.Load())
	}
	reads.Store(0)
	out := srv.sessionCosts(context.Background(), []string{"s_shrink"}, "")
	if _, ok := out["s_shrink"]; !ok || reads.Load() != 4 {
		t.Errorf("sessionCosts: %v in %d reads; want an answer after 4 reads", out, reads.Load())
	}
}

// delegatedWide (P9-pre-go-fix2): a level of sessions delegated
// sessions of turns turns each prices every session known — the level
// read in batches (sessionCostBatch), each within sessionCostPages × 500
// runs, a batch past it halved — and the Runs reads stay at most
// maxReads: a few per batch, never one per session.
func delegatedWide(t *testing.T, sessions, turns int, maxReads int64) {
	db, err := sqlite.Open(t.TempDir() + "/wide.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedRun(t, db, "s_lead-t1", "", "s_lead", "", nil)
	now := time.Now().UTC().Add(-time.Hour)
	var recs []obsdb.Record
	for i := range sessions {
		sid := fmt.Sprintf("s_w%03d", i)
		for k := 1; k <= turns; k++ {
			id := fmt.Sprintf("%s-t%d", sid, k)
			at := now.Add(time.Duration(i*turns+k) * time.Millisecond)
			attrs := func(ev string, pos int64) map[string]any {
				return map[string]any{"weft.record": "event", "weft.run.id": id, "gen_ai.agent.name": "acme-support",
					"weft.session.id": sid, "weft.turn": int64(k), "weft.session.parent": "s_lead",
					"weft.caller.run.id": "s_lead-t1", "weft.event.type": ev, "weft.event.pos": pos}
			}
			recs = append(recs,
				obsdb.Record{Time: at, EventName: "weft.event", Severity: 9, Service: "svc", Attrs: attrs("run_start", 0),
					Body: `{"type":"run_start","id":"` + id + `","model":{"provider":"wefttest","name":"script"},"agent":"acme-support"}`},
				obsdb.Record{Time: at, EventName: "weft.event", Severity: 9, Service: "svc", Attrs: attrs("run_finish", 1),
					Body: `{"type":"run_finish","run_id":"` + id + `","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`})
		}
	}
	if err := db.Write(context.Background(), obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int64
	srv := New(DB(countRuns{DB: db, n: &reads}))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	var doc struct {
		Delegated []struct {
			ID        string `json:"id"`
			CostKnown bool   `json:"cost_known"`
		} `json:"delegated"`
		DelegatedUsage core.Usage `json:"delegated_usage"`
		CostKnown      bool       `json:"delegated_sessions_cost_known"`
		CostReason     string     `json:"delegated_sessions_cost_reason"`
	}
	reads.Store(0)
	getUntil(t, ts, "/api/sessions/s_lead", &doc, func() bool { return true })
	if !doc.CostKnown || doc.DelegatedUsage.InputTokens != int64(sessions*turns) {
		t.Errorf("delegated figures = usage %+v known %v reason %q; want %d in, known", doc.DelegatedUsage, doc.CostKnown, doc.CostReason, sessions*turns)
	}
	if len(doc.Delegated) == 0 {
		t.Fatal("no delegated rows")
	}
	for _, d := range doc.Delegated {
		if !d.CostKnown {
			t.Errorf("delegated session %s: cost unknown; want every session of the level priced", d.ID)
		}
	}
	if got := reads.Load(); got > maxReads {
		t.Errorf("GET /api/sessions/s_lead made %d Runs reads; want a few per batch, not one per session (%d)", got, sessions)
	}
}

// TestCallerLinkedScoped (P9-pre-go-fix): under a panel token a run's
// caller_linked rows are read in its public id's scope — its own rows
// listed though another public id's fill the first page, the rest
// counted hidden, caller_linked_more only when more of its own exist.
func TestCallerLinkedScoped(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/scoped.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedRun(t, db, "lead", "pub_a", "", "", nil)
	for i := range callerLinkedCap {
		seedRun(t, db, fmt.Sprintf("b%03d", i), "pub_b", "", "", map[string]any{"weft.caller.run.id": "lead", "weft.caller.call.id": "c"})
	}
	for _, id := range []string{"z1", "z2"} {
		seedRun(t, db, id, "pub_a", "", "", map[string]any{"weft.caller.run.id": "lead", "weft.caller.call.id": "c"})
	}
	h := Handler(DB(db), Token("srv"))
	panel, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	code, _, body := getWith(t, h, "/studio/api/runs/lead", panel, "")
	if code != 200 {
		t.Fatalf("run page: %d %s", code, body)
	}
	var run struct {
		CallerLinked       []struct{ ID string } `json:"caller_linked"`
		CallerLinkedMore   bool                  `json:"caller_linked_more"`
		CallerLinkedHidden int                   `json:"caller_linked_hidden"`
	}
	if err := json.Unmarshal([]byte(body), &run); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range run.CallerLinked {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(ids, []string{"z1", "z2"}) || run.CallerLinkedMore || run.CallerLinkedHidden != callerLinkedCap {
		t.Errorf("panel caller_linked = %v more %v hidden %d; want [z1 z2], no more, %d hidden", ids, run.CallerLinkedMore, run.CallerLinkedHidden, callerLinkedCap)
	}
}

// TestDelegatedHiddenScoped (P9-pre-web-fix): a pool parent's session
// read with a panel token of its public id, one of whose delegated
// sessions carries another public id: the delegated rows are its own,
// the other counted in a hidden hole (never named), and the delegated
// cost is not known, the reason saying why — never "no delegation,
// exactly $0". The server token sees both, no hole, cost known.
func TestDelegatedHiddenScoped(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/hidden.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedRun(t, db, "s_p-t1", "pub_a", "s_p", "", nil)
	child := func(id, pub string) {
		seedRun(t, db, id+"-t1", pub, id, "", map[string]any{
			"weft.session.parent": "s_p", "weft.session.parent_call": "d_" + id,
			"weft.caller.run.id": "s_p-t1", "weft.caller.call.id": "d_" + id,
		})
	}
	child("s_mine", "pub_a")
	child("s_other", "pub_b")
	h := Handler(DB(db), Token("srv"))
	panel, err := signPanelToken([]byte("srv"), panelClaims{PublicID: "pub_a", Scope: scopeRead, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	type hole struct {
		Hole, Reason, Fix string
		Count             int
	}
	type doc struct {
		Delegated  []struct{ ID string } `json:"delegated"`
		Holes      []hole                `json:"delegated_holes"`
		CostUSD    *float64              `json:"delegated_sessions_cost_usd"`
		CostKnown  bool                  `json:"delegated_sessions_cost_known"`
		CostReason string                `json:"delegated_sessions_cost_reason"`
	}
	read := func(tok string) doc {
		t.Helper()
		code, _, body := getWith(t, h, "/studio/api/sessions/s_p", tok, "")
		if code != 200 {
			t.Fatalf("session: %d %s", code, body)
		}
		if tok == panel && strings.Contains(body, "s_other") {
			t.Errorf("the hidden session is named to the panel token: %s", body)
		}
		var d doc
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	p := read(panel)
	if len(p.Delegated) != 1 || p.Delegated[0].ID != "s_mine" {
		t.Errorf("panel delegated = %+v, want [s_mine]", p.Delegated)
	}
	if len(p.Holes) != 1 || p.Holes[0].Hole != string(obsdb.HoleHidden) || p.Holes[0].Count != 1 || p.Holes[0].Fix == "" {
		t.Errorf("panel delegated_holes = %+v, want one hidden hole, count 1", p.Holes)
	}
	if p.CostKnown || p.CostUSD != nil || !strings.Contains(p.CostReason, "another public id") {
		t.Errorf("panel delegated cost = %v known %v reason %q; want unknown, the hidden session said", p.CostUSD, p.CostKnown, p.CostReason)
	}
	s := read("srv")
	if len(s.Delegated) != 2 || len(s.Holes) != 0 || !s.CostKnown || s.CostUSD == nil {
		t.Errorf("server delegated = %+v holes %+v known %v usd %v; want both, no hole, known", s.Delegated, s.Holes, s.CostKnown, s.CostUSD)
	}
	// The hidden session alone: no row of its own, still not "$0, known".
	db2, err := sqlite.Open(t.TempDir() + "/hidden2.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	db = db2
	seedRun(t, db, "s_p-t1", "pub_a", "s_p", "", nil)
	child("s_other", "pub_b")
	h = Handler(DB(db), Token("srv"))
	p = read(panel)
	if len(p.Delegated) != 0 || len(p.Holes) != 1 || p.CostKnown || p.CostUSD != nil {
		t.Errorf("only a hidden child: delegated %+v holes %+v known %v usd %v; want none listed, one hole, unknown", p.Delegated, p.Holes, p.CostKnown, p.CostUSD)
	}
	// P9-pre-web-fix2 6: a visible child's own delegated session under
	// another public id — a grandchild — is counted in the hole too, the
	// cost then unknown; never a silent drop with the cost known.
	db3, err := sqlite.Open(t.TempDir() + "/hidden3.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db3.Close() })
	db = db3
	seedRun(t, db, "s_p-t1", "pub_a", "s_p", "", nil)
	child("s_mine", "pub_a")
	seedRun(t, db, "s_gc-t1", "pub_b", "s_gc", "", map[string]any{
		"weft.session.parent": "s_mine", "weft.session.parent_call": "d_gc",
		"weft.caller.run.id": "s_mine-t1", "weft.caller.call.id": "d_gc",
	})
	h = Handler(DB(db), Token("srv"))
	p = read(panel)
	if strings.Contains(fmt.Sprint(p), "s_gc") || len(p.Delegated) != 1 || len(p.Holes) != 1 || p.Holes[0].Count != 1 ||
		p.CostKnown || p.CostUSD != nil || !strings.Contains(p.CostReason, "another public id") {
		t.Errorf("a hidden grandchild: delegated %+v holes %+v known %v usd %v reason %q; want [s_mine], one hole counting 1, unknown, said",
			p.Delegated, p.Holes, p.CostKnown, p.CostUSD, p.CostReason)
	}
	if s := read("srv"); len(s.Holes) != 0 || !s.CostKnown {
		t.Errorf("a hidden grandchild, server token: holes %+v known %v; want none, known", s.Holes, s.CostKnown)
	}
	// P9-pre-web-fix3 F6: a hidden child's own delegated sessions are
	// counted too, at every depth — a hidden grandchild under it, and
	// below that one of the token's own public id (under a hidden
	// session, so hidden as well): 3, exact, never 1.
	db4, err := sqlite.Open(t.TempDir() + "/hidden4.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db4.Close() })
	db = db4
	seedRun(t, db, "s_p-t1", "pub_a", "s_p", "", nil)
	child("s_other", "pub_b")
	under := func(id, pub, parent string) {
		seedRun(t, db, id+"-t1", pub, id, "", map[string]any{
			"weft.session.parent": parent, "weft.session.parent_call": "d_" + id,
			"weft.caller.run.id": parent + "-t1", "weft.caller.call.id": "d_" + id,
		})
	}
	under("s_ogc", "pub_b", "s_other")
	under("s_oggc", "pub_a", "s_ogc")
	h = Handler(DB(db), Token("srv"))
	p = read(panel)
	if len(p.Delegated) != 0 || len(p.Holes) != 1 || p.Holes[0].Count != 3 || p.CostKnown ||
		strings.Contains(fmt.Sprint(p), "s_ogc") || strings.Contains(fmt.Sprint(p), "s_oggc") {
		t.Errorf("a hidden child's subtree: delegated %+v holes %+v known %v; want none listed, one hole counting 3, unknown", p.Delegated, p.Holes, p.CostKnown)
	}
	if s := read("srv"); len(s.Holes) != 0 || !s.CostKnown {
		t.Errorf("a hidden child's subtree, server token: holes %+v known %v; want none, known", s.Holes, s.CostKnown)
	}
}

// TestHiddenDelegatedFloor (P9-pre-web-fix3 F6): past the walk's cap
// the hidden count is a floor, said so — count_floor and "at least".
func TestHiddenDelegatedFloor(t *testing.T) {
	h := hiddenDelegatedHole(7, true)
	if !h.CountFloor || h.Count != 7 || !strings.Contains(h.Reason, "at least 7") {
		t.Errorf("floor hole = %+v, want count_floor, 7, at least", h)
	}
	if h := hiddenDelegatedHole(1, false); h.CountFloor || !strings.HasPrefix(h.Reason, "1 delegated session ") {
		t.Errorf("exact hole = %+v", h)
	}
}

// countHiddenReads counts the unscoped Sessions pages of 500 a
// delegated walk reads for its hidden ids (hiddenDelegated's paging).
type countHiddenReads struct {
	obsdb.DB
	n *atomic.Int64
}

func (c countHiddenReads) Sessions(ctx context.Context, q obsdb.SessionQuery) (obsdb.SessionPage, error) {
	if len(q.Parents) > 0 && q.PublicID == "" && q.Limit == 500 {
		c.n.Add(1)
	}
	return c.DB.Sessions(ctx, q)
}

// delegatedChildren writes one turn of each delegated session of s_p
// named in pubs (session id → public id), the i-th at base + i ms.
func delegatedChildren(t *testing.T, db obsdb.DB, base time.Time, sessions []string, pubs map[string]string) {
	t.Helper()
	var recs []obsdb.Record
	for i, sid := range sessions {
		id := sid + "-t1"
		at := base.Add(time.Duration(i) * time.Millisecond)
		attrs := func(ev string, pos int64) map[string]any {
			return map[string]any{"weft.record": "event", "weft.run.id": id, "gen_ai.agent.name": "acme-support",
				"weft.session.id": sid, "weft.turn": int64(1), "weft.session.parent": "s_p", "weft.public_id": pubs[sid],
				"weft.caller.run.id": "s_p-t1", "weft.event.type": ev, "weft.event.pos": pos}
		}
		recs = append(recs,
			obsdb.Record{Time: at, EventName: "weft.event", Severity: 9, Service: "svc", Attrs: attrs("run_start", 0),
				Body: `{"type":"run_start","id":"` + id + `","model":{"provider":"wefttest","name":"script"},"agent":"acme-support"}`},
			obsdb.Record{Time: at, EventName: "weft.event", Severity: 9, Service: "svc", Attrs: attrs("run_finish", 1),
				Body: `{"type":"run_finish","run_id":"` + id + `","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`})
	}
	if err := db.Write(context.Background(), obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
}

// TestHiddenDelegatedBounded (P9-pre-go-fix6): the hidden-id walk is
// bounded — 10,000 visible children and one hidden one, the oldest,
// cost at most hiddenDelegatedPages pages of 500 for a panel token's
// read, never the ~21 that skipping every visible one would page
// through; the hidden one still counted, the count a floor. One hidden
// child among a few visible ones is found on the first page and the
// paging stops there: exact.
func TestHiddenDelegatedBounded(t *testing.T) {
	read := func(n int, hiddenNewest bool) (int64, sessionDoc) {
		t.Helper()
		db, err := sqlite.Open(t.TempDir() + "/bounded.db")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		seedRun(t, db, "s_p-t1", "pub_a", "s_p", "", nil)
		pubs := map[string]string{"s_hidden": "pub_b"}
		var ids []string
		for i := range n {
			id := fmt.Sprintf("s_v%05d", i)
			pubs[id] = "pub_a"
			ids = append(ids, id)
		}
		if hiddenNewest {
			ids = append(ids, "s_hidden")
		} else {
			ids = append([]string{"s_hidden"}, ids...)
		}
		delegatedChildren(t, db, time.Now().UTC().Add(-time.Hour), ids, pubs)
		var reads atomic.Int64
		srv := New(DB(countHiddenReads{DB: db, n: &reads}))
		t.Cleanup(func() { _ = srv.Close() })
		var doc sessionDoc
		srv.delegatedSessions(context.Background(), "s_p", "pub_a", &doc)
		return reads.Load(), doc
	}
	got, doc := read(10_000, false)
	if got > hiddenDelegatedPages {
		t.Errorf("10,000 visible + 1 hidden: %d hidden-id pages read, want at most %d", got, hiddenDelegatedPages)
	}
	if len(doc.DelegatedHoles) != 1 || doc.DelegatedHoles[0].Count != 1 || !doc.DelegatedHoles[0].CountFloor {
		t.Errorf("10,000 visible + 1 hidden: holes %+v, want one hidden hole counting 1, a floor", doc.DelegatedHoles)
	}
	got, doc = read(3, true)
	if got != 1 {
		t.Errorf("3 visible + 1 hidden: %d hidden-id pages read, want 1", got)
	}
	if len(doc.DelegatedHoles) != 1 || doc.DelegatedHoles[0].Count != 1 || doc.DelegatedHoles[0].CountFloor {
		t.Errorf("3 visible + 1 hidden: holes %+v, want one hidden hole counting 1, exact", doc.DelegatedHoles)
	}
}

// TestHiddenDelegatedEarlyStopFloor (P9-pre-go-fix6): a walk that
// stops early — here a level below the first that cannot be read —
// leaves the hidden sessions below it uncounted, so a panel token's
// hidden count is a floor: count_floor and "at least", never an exact
// "1 delegated session".
func TestHiddenDelegatedEarlyStopFloor(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/early.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedRun(t, db, "s_p-t1", "pub_a", "s_p", "", nil)
	for _, c := range []struct{ id, pub string }{{"s_mine", "pub_a"}, {"s_other", "pub_b"}} {
		seedRun(t, db, c.id+"-t1", c.pub, c.id, "", map[string]any{"weft.session.parent": "s_p", "weft.caller.run.id": "s_p-t1"})
	}
	seedRun(t, db, "s_gc-t1", "pub_b", "s_gc", "", map[string]any{"weft.session.parent": "s_mine", "weft.caller.run.id": "s_mine-t1"})
	srv := New(DB(failDeepSessions{DB: db, root: "s_p"}))
	t.Cleanup(func() { _ = srv.Close() })
	var doc sessionDoc
	srv.delegatedSessions(context.Background(), "s_p", "pub_a", &doc)
	if len(doc.DelegatedHoles) != 1 || doc.DelegatedHoles[0].Count != 1 || !doc.DelegatedHoles[0].CountFloor ||
		!strings.Contains(doc.DelegatedHoles[0].Reason, "at least 1") {
		t.Errorf("an early stop: holes %+v, want one hidden hole, at least 1, a floor", doc.DelegatedHoles)
	}
	// The server token's walk stops the same way, with no hole.
	var all sessionDoc
	srv.delegatedSessions(context.Background(), "s_p", "", &all)
	if len(all.DelegatedHoles) != 0 {
		t.Errorf("an early stop, unscoped: holes %+v, want none", all.DelegatedHoles)
	}
}

// cycleSessions answers the delegated reads of a forged hidden cycle:
// s_p delegated s_h1 (pub_b), s_h1 delegated s_h2, and s_h2 claims
// s_h1 as its child again — none in pub_a's scope.
type cycleSessions struct {
	obsdb.DB
	reads *atomic.Int64
}

func (c cycleSessions) Sessions(ctx context.Context, q obsdb.SessionQuery) (obsdb.SessionPage, error) {
	if len(q.Parents) == 0 {
		return c.DB.Sessions(ctx, q)
	}
	c.reads.Add(1)
	if q.PublicID != "" {
		return obsdb.SessionPage{}, nil
	}
	kids := map[string][]obsdb.SessionRow{
		"s_p":  {{ID: "s_h1", PublicID: "pub_b", ParentSession: "s_p"}},
		"s_h1": {{ID: "s_h2", PublicID: "pub_b", ParentSession: "s_h1"}},
		"s_h2": {{ID: "s_h1", PublicID: "pub_b", ParentSession: "s_h2"}},
	}
	var page obsdb.SessionPage
	for _, p := range q.Parents {
		page.Sessions = append(page.Sessions, kids[p]...)
	}
	page.Total = len(page.Sessions)
	if q.Limit > 0 && len(page.Sessions) > q.Limit {
		page.Sessions = page.Sessions[:q.Limit]
	}
	return page, nil
}

// TestHiddenDelegatedCycle (P9-pre-go-fix6): forged lineage among
// hidden sessions — a two-session cycle — is counted once per session:
// 2, exact, never the ~500 the budget would let a re-count reach.
func TestHiddenDelegatedCycle(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/cycle.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var reads atomic.Int64
	srv := New(DB(cycleSessions{DB: db, reads: &reads}))
	t.Cleanup(func() { _ = srv.Close() })
	var doc sessionDoc
	srv.delegatedSessions(context.Background(), "s_p", "pub_a", &doc)
	if len(doc.DelegatedHoles) != 1 || doc.DelegatedHoles[0].Count != 2 || doc.DelegatedHoles[0].CountFloor {
		t.Errorf("a hidden cycle: holes %+v, want one hidden hole counting 2, exact", doc.DelegatedHoles)
	}
	if got := reads.Load(); got > 10 {
		t.Errorf("a hidden cycle: %d delegated reads, want a handful", got)
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
