package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/studio"
)

// TestCutConsistency (P9-pre-go-fix4) closes the retention cut's class:
// one database, merges stopped (the expired rows stay on disk, so only
// the reads' cut leaves them out), the cut's clock moved a window less
// an hour ahead, so a run last seen two hours ago is past weft_runs'
// TTL and one seen a minute ago is not. It holds
//
//   - a cut root (cc_cut, its public id pub_cut);
//   - a kept root cc_e with a cut intermediate child cc_e/1/call_a,
//     under it a stored-parent descendant …/0/call_b and an id-shaped
//     one …/0/call_c (no weft.parent.run.id), both seen a minute ago,
//     and a kept child cc_e/2/call_k;
//   - a cut derived run (stock spans, otel-<trace>-<span>);
//   - a session s_cc of a cut t1 beside a kept t2 (public id pub_s), and
//     a session s_gone of one cut turn (public id pub_gone);
//   - runs cc_e's tools started, one cut, one kept (weft.caller.run.id);
//   - a kept parked run cc_parked whose resume cc_resume is cut, and a
//     session s_pk whose newest turn t2 is the cut resume of its kept,
//     parked t1 (P9-pre-go-fix5).
//
// Every read — Run, Runs (top-level, all, SessionID, CallerRunID,
// IDPrefix), Search, ChildRuns, RunTreeSpans, Sessions, Session,
// ResolvePublicID, CountLogs, and Studio's routes over them (runs,
// search, runs/{id} and its children[] and caller_linked, ?tree=1,
// sessions, sessions/{id}, public/{id}, traces' root_run) — agrees: a
// cut run is in none of them, a kept run in every one that lists it.
// The cut child's descendants are out of cc_e's tree, by either link,
// though their own rows are kept (they open on their own). A cut
// resume is no resumer anywhere (P9-pre-go-fix5): its parked run reads
// parked — no resumed_by, no decided block, in Runs/Search
// status=parked — and s_pk awaits a decision in Sessions (Awaiting,
// Status, Pending, Outcomes), Session and their routes, as SQLite reads
// a parked run whose resume was deleted (TestDeletedResumeReadsParked).
func TestCutConsistency(t *testing.T) {
	db := freshDB(t)
	ctx := context.Background()
	stopMerges(t, db)
	now := time.Now().UTC()
	old, fresh := now.Add(-2*time.Hour), now.Add(-time.Minute)
	const (
		trace    = "0102030405060708090a0b0c0d0ecc01"
		stockTr  = "0102030405060708090a0b0c0d0ecc02"
		stockSp  = "cc00000000000099"
		cutRoot  = "cc_cut"
		root     = "cc_e"
		cutChild = "cc_e/1/call_a"
		byRow    = "cc_e/1/call_a/0/call_b"
		byID     = "cc_e/1/call_a/0/call_c"
		kidKept  = "cc_e/2/call_k"
		callCut  = "cc_called_cut"
		callKept = "cc_called_kept"
		parked   = "cc_parked"
		resume   = "cc_resume"
	)
	derived := obsdb.StockRunID(stockTr, stockSp)
	seq := 0
	span := func(runID string, at time.Time, extra map[string]any) obsdb.Span {
		seq++
		attrs := map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "cc-agent", "weft.run.id": runID}
		for k, v := range extra {
			attrs[k] = v
		}
		return obsdb.Span{TraceID: trace, SpanID: "cc0000000000" + string(rune('a'+seq/16)) + string("0123456789abcdef"[seq%16]) + "00",
			Name: "invoke_agent cc", Kind: 1, Start: at, End: at.Add(time.Second), StatusCode: 1, Service: "cc-svc",
			Attrs: attrs, Resource: map[string]any{"service.name": "cc-svc"}}
	}
	spans := []obsdb.Span{
		span(cutRoot, old, map[string]any{"weft.public_id": "pub_cut"}),
		span(root, fresh, nil),
		span(cutChild, old, map[string]any{"weft.parent.run.id": root}),
		span(byRow, fresh, map[string]any{"weft.parent.run.id": cutChild}),
		span(byID, fresh, nil),
		span(kidKept, fresh, map[string]any{"weft.parent.run.id": root}),
		span(callCut, old, map[string]any{"weft.caller.run.id": root, "weft.caller.call.id": "c1"}),
		span(callKept, fresh, map[string]any{"weft.caller.run.id": root, "weft.caller.call.id": "c2"}),
		span(parked, fresh, map[string]any{"weft.run.pending": 2}),
		span(resume, old, map[string]any{"weft.resumed_from": parked}),
		{TraceID: stockTr, SpanID: stockSp, Name: "invoke_agent stock", Kind: 1, Start: old, End: old.Add(time.Second),
			StatusCode: 1, Service: "py-cc", Resource: map[string]any{"service.name": "py-cc"},
			Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "stock-cc", "gen_ai.conversation.id": "conv_cc_cut"}},
	}
	// turnOf is one session turn's run_start and run_finish; from names
	// the run it resumes, pending a call it parks.
	turnOf := func(sid, pub string, n int64, at time.Time, from string, pending bool) []obsdb.Record {
		id := sid + "-t" + string(rune('0'+n))
		attrs := func(ev string, pos int64) map[string]any {
			a := map[string]any{"weft.record": "event", "weft.run.id": id, "weft.session.id": sid, "weft.turn": n,
				"weft.public_id": pub, "gen_ai.agent.name": "cc-agent", "weft.event.type": ev, "weft.event.pos": pos}
			if from != "" {
				a["weft.resumed_from"] = from
			}
			return a
		}
		park := ""
		if pending {
			park = `,"pending":[{"type":"tool_call","id":"call_p","name":"refund","args":{}}]`
		}
		return []obsdb.Record{
			{Time: at, EventName: "weft.event", Severity: 9, Service: "cc-svc", Attrs: attrs("run_start", 0), Body: `{"type":"run_start","id":"` + id + `"}`},
			{Time: at.Add(time.Second), EventName: "weft.event", Severity: 9, Service: "cc-svc", Attrs: attrs("run_finish", 1),
				Body: `{"type":"run_finish","run_id":"` + id + `","steps":1` + park + `}`},
		}
	}
	turn := func(sid, pub string, n int64, at time.Time) []obsdb.Record { return turnOf(sid, pub, n, at, "", false) }
	var recs []obsdb.Record
	recs = append(recs, turn("s_cc", "pub_s", 1, old)...)
	recs = append(recs, turn("s_cc", "pub_s", 2, fresh)...)
	recs = append(recs, turn("s_gone", "pub_gone", 1, old)...)
	recs = append(recs, turnOf("s_pk", "pub_pk", 1, fresh, "", true)...)
	recs = append(recs, turnOf("s_pk", "pub_pk", 2, old, "s_pk-t1", false)...)
	if err := db.Write(ctx, obsdb.Batch{Spans: spans, Records: recs}); err != nil {
		t.Fatal(err)
	}
	cut := []string{cutRoot, cutChild, derived, "s_cc-t1", "s_gone-t1", callCut, resume, "s_pk-t2"}
	kept := []string{root, kidKept, byRow, byID, callKept, "s_cc-t2", parked, "s_pk-t1"}
	topLevel := []string{root, byID, "s_cc-t2", parked, "s_pk-t1"}
	parkedRuns := []string{parked, "s_pk-t1"}

	// Inside the window every run opens: the fixture is whole.
	for _, id := range append(slices.Clone(cut), kept...) {
		if _, err := db.Run(ctx, id); err != nil {
			t.Fatalf("Run(%s) before the cut: %v", id, err)
		}
	}
	// Before the cut both parks are decided: their resumes name them.
	resumedBy := func(when string, want map[string]string) {
		t.Helper()
		for id, by := range want {
			d, err := db.Run(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if d.ResumedBy != by || d.Pending == 0 {
				t.Errorf("%s: Run(%s) resumed_by %q (pending %d), want %q", when, id, d.ResumedBy, d.Pending, by)
			}
		}
	}
	resumedBy("before the cut", map[string]string{parked: resume, "s_pk-t1": "s_pk-t2"})
	if page, err := db.Runs(ctx, obsdb.RunQuery{Status: obsdb.StatusParked, ParentRunID: "*"}); err != nil || page.Total != 0 {
		t.Fatalf("Runs{parked} before the cut = %v (Total %d), %v; want none", rowIDs(page.Runs), page.Total, err)
	}
	if sp, err := db.Sessions(ctx, obsdb.SessionQuery{Awaiting: true}); err != nil || sp.Total != 0 {
		t.Fatalf("Sessions{Awaiting} before the cut = %d, %v; want none", sp.Total, err)
	}

	// The cut: a window less an hour on, so the boundary is now − 1h.
	db.retention.hooks = &retentionHooks{now: func() time.Time { return time.Now().Add(DefaultMetaTTL - time.Hour) }}
	t.Cleanup(func() { db.retention.hooks = nil })

	// Run and RunTreeSpans: a cut run is not found, a kept run opens.
	for _, id := range cut {
		if _, err := db.Run(ctx, id); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("Run(%s) = %v, want not found (cut)", id, err)
		}
		if _, err := db.RunTreeSpans(ctx, obsdb.TreeQuery{RunID: id}); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("RunTreeSpans(%s) = %v, want not found as Run's (cut)", id, err)
		}
		if _, err := db.CountLogs(ctx, id, 0); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("CountLogs(%s) = %v, want not found as Run's (cut)", id, err)
		}
	}
	for _, id := range kept {
		if _, err := db.Run(ctx, id); err != nil {
			t.Errorf("Run(%s) = %v, want it (kept)", id, err)
		}
		if _, err := db.RunTreeSpans(ctx, obsdb.TreeQuery{RunID: id}); err != nil {
			t.Errorf("RunTreeSpans(%s) = %v, want it (kept)", id, err)
		}
	}

	// cc_e's children and tree: the kept child alone — the cut child
	// out, and both its descendants with it (by row and by id).
	det, err := db.Run(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if got := rowIDs(det.Children); !slices.Equal(got, []string{kidKept}) {
		t.Errorf("Run(%s).Children = %v, want [%s]", root, got, kidKept)
	}
	kids, err := db.ChildRuns(ctx, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got := rowIDs(kids[root]); !slices.Equal(got, []string{kidKept}) {
		t.Errorf("ChildRuns(%s) = %v, want [%s]", root, got, kidKept)
	}
	tree, err := db.RunTreeSpans(ctx, obsdb.TreeQuery{RunID: root})
	if err != nil {
		t.Fatal(err)
	}
	treeRuns := map[string]bool{}
	for _, s := range tree.Spans {
		treeRuns[s.RunID] = true
	}
	for _, r := range tree.Missing {
		treeRuns[r.ID] = true
	}
	if got := sortedKeys(treeRuns); !slices.Equal(got, []string{root, kidKept}) {
		t.Errorf("RunTreeSpans(%s) runs = %v, want [%s %s]: the cut child and its descendants out", root, got, root, kidKept)
	}

	// The lists.
	listed := func(label string, q obsdb.RunQuery, want ...string) {
		t.Helper()
		page, err := db.Runs(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		got := rowIDs(page.Runs)
		sort.Strings(got)
		sort.Strings(want)
		if !slices.Equal(got, want) || page.Total != len(want) {
			t.Errorf("Runs{%s} = %v (Total %d), want %v", label, got, page.Total, want)
		}
		sp, err := db.Search(ctx, obsdb.SearchQuery{RunQuery: q})
		if err != nil {
			t.Fatal(err)
		}
		if got := rowIDs(sp.Runs); sp.Total != len(want) || len(got) != len(want) {
			t.Errorf("Search{%s} = %v (Total %d), want %v", label, got, sp.Total, want)
		}
	}
	// Top-level: no stored parent, no caller link — the id-shaped call_c
	// is one by its row (the id rule is the tree's, not the list's).
	listed("top-level", obsdb.RunQuery{}, topLevel...)
	listed("all", obsdb.RunQuery{ParentRunID: "*"}, kept...)
	listed("SessionID s_cc", obsdb.RunQuery{SessionID: "s_cc", ParentRunID: "*"}, "s_cc-t2")
	listed("SessionID s_gone", obsdb.RunQuery{SessionID: "s_gone", ParentRunID: "*"})
	listed("CallerRunID cc_e", obsdb.RunQuery{CallerRunID: root, ParentRunID: "*"}, callKept)
	listed("IDPrefix cc_e/1/", obsdb.RunQuery{IDPrefix: "cc_e/1/", ParentRunID: "*"}, byRow, byID)
	listed("IDPrefix otel-", obsdb.RunQuery{IDPrefix: "otel-", ParentRunID: "*"})
	listed("PublicID pub_cut", obsdb.RunQuery{PublicID: "pub_cut", ParentRunID: "*"})

	// A cut resume is no resumer: both parks read open again — in Run,
	// in Runs and Search status=parked, and in every row a list fills.
	resumedBy("after the cut", map[string]string{parked: "", "s_pk-t1": ""})
	listed("status parked", obsdb.RunQuery{Status: obsdb.StatusParked, ParentRunID: "*"}, parkedRuns...)
	listed("status parked, top-level", obsdb.RunQuery{Status: obsdb.StatusParked}, parkedRuns...)
	if page, err := db.Runs(ctx, obsdb.RunQuery{ParentRunID: "*", Limit: 100}); err != nil {
		t.Fatal(err)
	} else {
		for _, r := range page.Runs {
			if r.ResumedBy != "" {
				t.Errorf("Runs: %s resumed_by %q, want none (its resume is cut)", r.ID, r.ResumedBy)
			}
		}
	}
	if kids, err := db.ChildRuns(ctx, []string{root}); err != nil {
		t.Fatal(err)
	} else {
		for _, r := range kids[root] {
			if r.ResumedBy != "" {
				t.Errorf("ChildRuns: %s resumed_by %q", r.ID, r.ResumedBy)
			}
		}
	}

	// Sessions, Session, ResolvePublicID.
	sp, err := db.Sessions(ctx, obsdb.SessionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var sids []string
	for _, s := range sp.Sessions {
		sids = append(sids, s.ID)
		if s.ID == "s_cc" && s.Turns != 1 {
			t.Errorf("Sessions' s_cc row = %d turns, want 1 (t1 cut)", s.Turns)
		}
		if s.ID == "s_pk" {
			awaits(t, "Sessions' s_pk row", s)
		}
	}
	sort.Strings(sids)
	if !slices.Equal(sids, []string{"s_cc", "s_pk"}) || sp.Total != 2 {
		t.Errorf("Sessions = %v (Total %d), want [s_cc s_pk]: s_gone and the derived run's conversation cut", sids, sp.Total)
	}
	if sd, err := db.Session(ctx, "s_cc"); err != nil || !slices.Equal(rowIDs(sd.Runs), []string{"s_cc-t2"}) {
		t.Errorf("Session(s_cc) = %v, %v; want the kept turn alone", rowIDs(sd.Runs), err)
	}
	// s_pk's newest kept turn is the open park: it awaits a decision.
	if sd, err := db.Session(ctx, "s_pk"); err != nil || !slices.Equal(rowIDs(sd.Runs), []string{"s_pk-t1"}) {
		t.Errorf("Session(s_pk) = %v, %v; want the parked turn alone", rowIDs(sd.Runs), err)
	} else {
		awaits(t, "Session(s_pk)", sd.SessionRow)
		if sd.Runs[0].ResumedBy != "" {
			t.Errorf("Session(s_pk) t1 resumed_by %q, want none", sd.Runs[0].ResumedBy)
		}
	}
	sessionsOf := func(label string, q obsdb.SessionQuery, want ...string) {
		t.Helper()
		page, err := db.Sessions(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, s := range page.Sessions {
			got = append(got, s.ID)
		}
		sort.Strings(got)
		if !slices.Equal(got, want) || page.Total != len(want) {
			t.Errorf("Sessions{%s} = %v (Total %d), want %v", label, got, page.Total, want)
		}
	}
	sessionsOf("Awaiting", obsdb.SessionQuery{Awaiting: true}, "s_pk")
	sessionsOf("Awaiting, pub_pk", obsdb.SessionQuery{Awaiting: true, PublicID: "pub_pk"}, "s_pk")
	sessionsOf("Status succeeded", obsdb.SessionQuery{Status: obsdb.StatusSucceeded}, "s_cc")
	for _, sid := range []string{"s_gone", "conv_cc_cut"} {
		if _, err := db.Session(ctx, sid); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("Session(%s) = %v, want not found (every turn cut)", sid, err)
		}
	}
	if sid, err := db.ResolvePublicID(ctx, "pub_s"); err != nil || sid != "s_cc" {
		t.Errorf("ResolvePublicID(pub_s) = %q, %v; want s_cc", sid, err)
	}
	if _, err := db.ResolvePublicID(ctx, "pub_gone"); !errors.Is(err, obsdb.ErrNotFound) {
		t.Errorf("ResolvePublicID(pub_gone) = %v, want not found", err)
	}

	// Studio's routes over the same reads, setup A on loopback.
	srv := studio.New(studio.DB(db))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	get := func(path string, into any) int {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if into != nil && resp.StatusCode == http.StatusOK {
			if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
		}
		return resp.StatusCode
	}
	type idRow struct {
		ID string `json:"id"`
	}
	ids := func(rows []idRow) []string {
		out := []string{}
		for _, r := range rows {
			out = append(out, r.ID)
		}
		sort.Strings(out)
		return out
	}
	routeList := func(path string, want ...string) {
		t.Helper()
		var page struct {
			Runs  []idRow `json:"runs"`
			Total int     `json:"total"`
		}
		if code := get(path, &page); code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, code)
		}
		sort.Strings(want)
		if got := ids(page.Runs); !slices.Equal(got, want) {
			t.Errorf("GET %s = %v, want %v", path, got, want)
		}
	}
	routeList("/api/runs?all=1&limit=100", kept...)
	routeList("/api/runs", topLevel...)
	routeList("/api/runs?status=parked&all=1", parkedRuns...)
	routeList("/api/runs?status=parked", parkedRuns...)
	routeList("/api/search?status=parked&all=1", parkedRuns...)
	routeList("/api/runs?session=s_cc&all=1", "s_cc-t2")
	routeList("/api/runs?id_prefix="+url.QueryEscape("cc_e/1/")+"&all=1", byRow, byID)
	routeList("/api/search?all=1&limit=100", kept...)
	for _, id := range cut {
		if code := get("/api/runs/"+url.PathEscape(id), nil); code != http.StatusNotFound {
			t.Errorf("GET /api/runs/%s = %d, want 404 (cut)", id, code)
		}
		if code := get("/api/runs/"+url.PathEscape(id)+"/spans?tree=1", nil); code != http.StatusNotFound {
			t.Errorf("GET /api/runs/%s/spans?tree=1 = %d, want 404 (cut)", id, code)
		}
	}
	var doc struct {
		Children     []idRow `json:"children"`
		CallerLinked []idRow `json:"caller_linked"`
	}
	if code := get("/api/runs/"+root, &doc); code != http.StatusOK {
		t.Fatalf("GET /api/runs/%s = %d", root, code)
	}
	if got := ids(doc.Children); !slices.Equal(got, []string{kidKept}) {
		t.Errorf("GET /api/runs/%s children = %v, want [%s]", root, got, kidKept)
	}
	if got := ids(doc.CallerLinked); !slices.Equal(got, []string{callKept}) {
		t.Errorf("GET /api/runs/%s caller_linked = %v, want [%s]", root, got, callKept)
	}
	for _, id := range parkedRuns {
		var pdoc struct {
			Pending   int             `json:"pending"`
			ResumedBy string          `json:"resumed_by"`
			Decided   json.RawMessage `json:"decided"`
		}
		if code := get("/api/runs/"+id, &pdoc); code != http.StatusOK {
			t.Fatalf("GET /api/runs/%s = %d", id, code)
		}
		if pdoc.Pending == 0 || pdoc.ResumedBy != "" || pdoc.Decided != nil {
			t.Errorf("GET /api/runs/%s: pending %d, resumed_by %q, decided %s; want parked, no resumer, no decided block",
				id, pdoc.Pending, pdoc.ResumedBy, pdoc.Decided)
		}
	}
	var treeDoc struct {
		Spans []struct {
			RunID string `json:"run_id"`
		} `json:"spans"`
	}
	if code := get("/api/runs/"+root+"/spans?tree=1", &treeDoc); code != http.StatusOK {
		t.Fatalf("GET /api/runs/%s/spans?tree=1 = %d", root, code)
	}
	routeRuns := map[string]bool{}
	for _, s := range treeDoc.Spans {
		routeRuns[s.RunID] = true
	}
	if got := sortedKeys(routeRuns); !slices.Equal(got, []string{root, kidKept}) {
		t.Errorf("GET /api/runs/%s/spans?tree=1 runs = %v, want [%s %s]", root, got, root, kidKept)
	}
	var sessions struct {
		Sessions []idRow `json:"sessions"`
		Total    int     `json:"total"`
	}
	if code := get("/api/sessions", &sessions); code != http.StatusOK || !slices.Equal(ids(sessions.Sessions), []string{"s_cc", "s_pk"}) || sessions.Total != 2 {
		t.Errorf("GET /api/sessions = %d %v (total %d), want [s_cc s_pk]", code, ids(sessions.Sessions), sessions.Total)
	}
	type awaitDoc struct {
		ID       string   `json:"id"`
		Status   string   `json:"status"`
		Pending  int      `json:"pending"`
		Resumes  int      `json:"resumes"`
		Outcomes []string `json:"outcomes"`
	}
	var awaiting struct {
		Sessions []awaitDoc `json:"sessions"`
		Total    int        `json:"total"`
	}
	if code := get("/api/sessions?awaiting=1", &awaiting); code != http.StatusOK || awaiting.Total != 1 || len(awaiting.Sessions) != 1 {
		t.Errorf("GET /api/sessions?awaiting=1 = %d %+v, want s_pk alone", code, awaiting)
	} else if a := awaiting.Sessions[0]; a.ID != "s_pk" || a.Status != "parked" || a.Pending != 1 || a.Resumes != 0 || !slices.Equal(a.Outcomes, []string{"parked"}) {
		t.Errorf("GET /api/sessions?awaiting=1 row = %+v, want s_pk parked, pending 1, outcomes [parked]", a)
	}
	var succ struct {
		Sessions []idRow `json:"sessions"`
	}
	if code := get("/api/sessions?status=succeeded", &succ); code != http.StatusOK || !slices.Equal(ids(succ.Sessions), []string{"s_cc"}) {
		t.Errorf("GET /api/sessions?status=succeeded = %d %v, want [s_cc]: s_pk's newest kept turn is a park", code, ids(succ.Sessions))
	}
	var pk awaitDoc
	if code := get("/api/sessions/s_pk", &pk); code != http.StatusOK || pk.Status != "parked" || pk.Pending != 1 || !slices.Equal(pk.Outcomes, []string{"parked"}) {
		t.Errorf("GET /api/sessions/s_pk = %d %+v, want parked, pending 1, outcomes [parked]", code, pk)
	}
	var sdoc struct {
		Runs []idRow `json:"runs"`
	}
	if code := get("/api/sessions/s_cc", &sdoc); code != http.StatusOK || !slices.Equal(ids(sdoc.Runs), []string{"s_cc-t2"}) {
		t.Errorf("GET /api/sessions/s_cc = %d %v, want the kept turn alone", code, ids(sdoc.Runs))
	}
	for _, path := range []string{"/api/sessions/s_gone", "/api/sessions/conv_cc_cut", "/api/public/pub_gone"} {
		if code := get(path, nil); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 (every turn cut)", path, code)
		}
	}
	if code := get("/api/public/pub_s", nil); code != http.StatusOK {
		t.Errorf("GET /api/public/pub_s = %d, want 200", code)
	}
	var traces struct {
		Traces []struct {
			TraceID string `json:"trace_id"`
			RootRun *idRow `json:"root_run"`
		} `json:"traces"`
	}
	if code := get("/api/traces?limit=100", &traces); code != http.StatusOK {
		t.Fatalf("GET /api/traces = %d", code)
	}
	for _, tr := range traces.Traces {
		if tr.RootRun != nil && slices.Contains(cut, tr.RootRun.ID) {
			t.Errorf("trace %s: root_run %s is cut, want it absent", tr.TraceID, tr.RootRun.ID)
		}
		if tr.TraceID == stockTr && tr.RootRun != nil {
			t.Errorf("the derived run's trace: root_run %+v, want none (cut)", tr.RootRun)
		}
	}

	// The clock back: every run opens again — the cut is the read's.
	db.retention.hooks = nil
	resumedBy("with the clock back", map[string]string{parked: resume, "s_pk-t1": "s_pk-t2"})
	for _, id := range cut {
		if _, err := db.Run(ctx, id); err != nil {
			t.Errorf("Run(%s) with the clock back = %v", id, err)
		}
	}
	if tree, err := db.RunTreeSpans(ctx, obsdb.TreeQuery{RunID: root}); err != nil {
		t.Fatal(err)
	} else {
		runs := map[string]bool{}
		for _, s := range tree.Spans {
			runs[s.RunID] = true
		}
		if want := []string{root, cutChild, byRow, byID, kidKept}; !slices.Equal(sortedKeys(runs), sortedStrings(want)) {
			t.Errorf("the tree with the clock back = %v, want %v (by row and by id under the child)", sortedKeys(runs), sortedStrings(want))
		}
	}
}

// awaits checks a session row whose newest kept turn is an open park:
// one pending call, no resume counted, the strip one parked mark.
func awaits(t *testing.T, label string, s obsdb.SessionRow) {
	t.Helper()
	if s.Turns != 1 || s.Pending != 1 || s.Resumes != 0 || !slices.Equal(s.Outcomes, []obsdb.TurnOutcome{obsdb.OutcomeParked}) {
		t.Errorf("%s: turns %d, pending %d, resumes %d, outcomes %v; want 1, 1, 0, [parked] (its resume is cut)",
			label, s.Turns, s.Pending, s.Resumes, s.Outcomes)
	}
}

func rowIDs(rows []obsdb.RunRow) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedStrings(s []string) []string {
	out := slices.Clone(s)
	sort.Strings(out)
	return out
}
