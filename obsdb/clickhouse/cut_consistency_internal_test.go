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
//   - runs cc_e's tools started, one cut, one kept (weft.caller.run.id).
//
// Every read — Run, Runs (top-level, all, SessionID, CallerRunID,
// IDPrefix), Search, ChildRuns, RunTreeSpans, Sessions, Session,
// ResolvePublicID, CountLogs, and Studio's routes over them (runs,
// search, runs/{id} and its children[] and caller_linked, ?tree=1,
// sessions, sessions/{id}, public/{id}, traces' root_run) — agrees: a
// cut run is in none of them, a kept run in every one that lists it.
// The cut child's descendants are out of cc_e's tree, by either link,
// though their own rows are kept (they open on their own).
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
		{TraceID: stockTr, SpanID: stockSp, Name: "invoke_agent stock", Kind: 1, Start: old, End: old.Add(time.Second),
			StatusCode: 1, Service: "py-cc", Resource: map[string]any{"service.name": "py-cc"},
			Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent", "gen_ai.agent.name": "stock-cc", "gen_ai.conversation.id": "conv_cc_cut"}},
	}
	turn := func(sid, pub string, n int64, at time.Time) []obsdb.Record {
		id := sid + "-t" + string(rune('0'+n))
		attrs := func(ev string, pos int64) map[string]any {
			return map[string]any{"weft.record": "event", "weft.run.id": id, "weft.session.id": sid, "weft.turn": n,
				"weft.public_id": pub, "gen_ai.agent.name": "cc-agent", "weft.event.type": ev, "weft.event.pos": pos}
		}
		return []obsdb.Record{
			{Time: at, EventName: "weft.event", Severity: 9, Service: "cc-svc", Attrs: attrs("run_start", 0), Body: `{"type":"run_start","id":"` + id + `"}`},
			{Time: at.Add(time.Second), EventName: "weft.event", Severity: 9, Service: "cc-svc", Attrs: attrs("run_finish", 1),
				Body: `{"type":"run_finish","run_id":"` + id + `","steps":1}`},
		}
	}
	var recs []obsdb.Record
	recs = append(recs, turn("s_cc", "pub_s", 1, old)...)
	recs = append(recs, turn("s_cc", "pub_s", 2, fresh)...)
	recs = append(recs, turn("s_gone", "pub_gone", 1, old)...)
	if err := db.Write(ctx, obsdb.Batch{Spans: spans, Records: recs}); err != nil {
		t.Fatal(err)
	}
	cut := []string{cutRoot, cutChild, derived, "s_cc-t1", "s_gone-t1", callCut}
	kept := []string{root, kidKept, byRow, byID, callKept, "s_cc-t2"}

	// Inside the window every run opens: the fixture is whole.
	for _, id := range append(slices.Clone(cut), kept...) {
		if _, err := db.Run(ctx, id); err != nil {
			t.Fatalf("Run(%s) before the cut: %v", id, err)
		}
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
	listed("top-level", obsdb.RunQuery{}, root, byID, "s_cc-t2")
	listed("all", obsdb.RunQuery{ParentRunID: "*"}, kept...)
	listed("SessionID s_cc", obsdb.RunQuery{SessionID: "s_cc", ParentRunID: "*"}, "s_cc-t2")
	listed("SessionID s_gone", obsdb.RunQuery{SessionID: "s_gone", ParentRunID: "*"})
	listed("CallerRunID cc_e", obsdb.RunQuery{CallerRunID: root, ParentRunID: "*"}, callKept)
	listed("IDPrefix cc_e/1/", obsdb.RunQuery{IDPrefix: "cc_e/1/", ParentRunID: "*"}, byRow, byID)
	listed("IDPrefix otel-", obsdb.RunQuery{IDPrefix: "otel-", ParentRunID: "*"})
	listed("PublicID pub_cut", obsdb.RunQuery{PublicID: "pub_cut", ParentRunID: "*"})

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
	}
	if !slices.Equal(sids, []string{"s_cc"}) || sp.Total != 1 {
		t.Errorf("Sessions = %v (Total %d), want [s_cc]: s_gone and the derived run's conversation cut", sids, sp.Total)
	}
	if sd, err := db.Session(ctx, "s_cc"); err != nil || !slices.Equal(rowIDs(sd.Runs), []string{"s_cc-t2"}) {
		t.Errorf("Session(s_cc) = %v, %v; want the kept turn alone", rowIDs(sd.Runs), err)
	}
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
	routeList("/api/runs", root, byID, "s_cc-t2")
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
	if code := get("/api/sessions", &sessions); code != http.StatusOK || !slices.Equal(ids(sessions.Sessions), []string{"s_cc"}) || sessions.Total != 1 {
		t.Errorf("GET /api/sessions = %d %v (total %d), want [s_cc]", code, ids(sessions.Sessions), sessions.Total)
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
