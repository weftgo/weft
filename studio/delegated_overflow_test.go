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
	// batch: past the budget at 20 pages, then two halves of 5,250 (11
	// pages each) — 43 reads.
	t.Run("a level past one batch", func(t *testing.T) { delegatedWide(t, 400, 30, 40) })
	t.Run("an overflowing batch halves", func(t *testing.T) { delegatedWide(t, 50, 210, 50) })
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
