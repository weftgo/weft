package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// treeHoleDoc is the ?tree=1 answer's fields these tests read.
type treeHoleDoc struct {
	Spans []struct {
		RunID string `json:"run_id"`
	} `json:"spans"`
	Holes []treeHole `json:"holes"`
}

// getTree reads runID's ?tree=1 with tok ("" none).
func getTree(t *testing.T, url, runID, tok string) (int, treeHoleDoc, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url+"/api/runs/"+runID+"/spans?tree=1", nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var d treeHoleDoc
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(b, &d); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, d, string(b)
}

// treeTestSpan is one invoke_agent span of runID under parentRun, with
// pub as its weft.public_id ("" none), ms after base.
func treeTestSpan(trace, spanID, runID, parentRun, pub string, base time.Time, ms int) obsdb.Span {
	attrs := map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": runID}
	if parentRun != "" {
		attrs["weft.parent.run.id"] = parentRun
	}
	if pub != "" {
		attrs["weft.public_id"] = pub
	}
	at := base.Add(time.Duration(ms) * time.Millisecond)
	return obsdb.Span{TraceID: trace, SpanID: spanID, Name: "invoke_agent a", Kind: 1,
		Start: at, End: at.Add(time.Millisecond), StatusCode: 1, Service: "svc",
		Attrs: attrs, Resource: map[string]any{"service.name": "svc"}}
}

// TestAuthMatrixTreeScopeCap (U9.1-fix2): a panel token's tree is cut
// to its scope before the cap — a hidden child's 10,001 spans neither
// make the visible tree truncated nor push a visible span out (and so
// never say how large the hidden run is); the server token reads the
// whole tree and is truncated.
func TestAuthMatrixTreeScopeCap(t *testing.T) {
	const serverTok = "srv-token"
	srv := New(Open(t.TempDir()+"/treecap.db"), Token(serverTok))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	const (
		trace  = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeee1"
		root   = "cap_root"
		theirs = "cap_root/0/c_theirs"
		mine   = "cap_root/0/c_mine"
	)
	now := time.Now().UTC()
	spans := []obsdb.Span{treeTestSpan(trace, "0a00000000000001", root, "", "pub_a", now, 0)}
	for i := range obsdb.TreeSpanCap + 1 {
		spans = append(spans, treeTestSpan(trace, fmt.Sprintf("0b%014x", i), theirs, root, "pub_b", now, 1))
	}
	spans = append(spans, treeTestSpan(trace, "0c00000000000001", mine, root, "pub_a", now, 5))
	if err := srv.db.Write(context.Background(), obsdb.Batch{Spans: spans}); err != nil {
		t.Fatal(err)
	}
	seedRun(t, srv.db, root, "pub_a", "", "", nil)
	tok, err := signPanelToken([]byte(serverTok), panelClaims{PublicID: "pub_a", Scope: scopePlayground, Exp: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}

	code, d, body := getTree(t, ts.URL, root, tok)
	if code != http.StatusOK || len(d.Spans) != 2 || d.Spans[0].RunID != root || d.Spans[1].RunID != mine {
		t.Fatalf("pub_a's tree = %d, %d spans, want 200 with root's and mine's: %.400s", code, len(d.Spans), body)
	}
	if len(d.Holes) != 1 || d.Holes[0].Hole != string(obsdb.HoleHidden) {
		t.Errorf("pub_a's tree holes = %+v, want hidden alone (no truncated: the visible tree fits)", d.Holes)
	}
	if strings.Contains(body, theirs) || strings.Contains(body, "pub_b") {
		t.Errorf("pub_a's tree names the hidden run")
	}
	code, d, _ = getTree(t, ts.URL, root, serverTok)
	if code != http.StatusOK || len(d.Spans) != obsdb.TreeSpanCap || len(d.Holes) != 1 || d.Holes[0].Hole != string(obsdb.HoleTruncated) {
		t.Errorf("the server token's tree = %d, %d spans, holes %+v; want truncated at the cap", code, len(d.Spans), d.Holes)
	}
}

// TestTreeRunningChildNotMissing (U9.1-fix2): a child with a row and no
// span that is still running is not a no_spans hole — spans export
// when they end, so every live delegation would otherwise read as a
// loss; once it finished with no span it is one.
func TestTreeRunningChildNotMissing(t *testing.T) {
	srv := New(Open(t.TempDir() + "/treelive.db"))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	const (
		trace = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeee2"
		root  = "live_root"
		child = "live_root/0/call_live"
	)
	now := time.Now().UTC()
	if err := srv.db.Write(context.Background(), obsdb.Batch{
		Spans: []obsdb.Span{treeTestSpan(trace, "0d00000000000001", root, "", "", now, 0)},
		Records: []obsdb.Record{{
			Time: now, EventName: "weft.event", Severity: 9, Service: "svc",
			Body: `{"type":"run_start","id":"` + child + `"}`,
			Attrs: map[string]any{"weft.record": "event", "weft.run.id": child, "weft.event.type": "run_start",
				"weft.event.pos": int64(0), "weft.parent.run.id": root},
			Resource: map[string]any{"service.name": "svc"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if code, d, body := getTree(t, ts.URL, root, ""); code != http.StatusOK || len(d.Holes) != 0 {
		t.Errorf("a running child's tree = %d, holes %+v, want none: %s", code, d.Holes, body)
	}
	seedRun(t, srv.db, child, "", "", "", map[string]any{"weft.parent.run.id": root})
	_, d, _ := getTree(t, ts.URL, root, "")
	if len(d.Holes) != 1 || d.Holes[0].Cause != string(obsdb.CauseNoSpans) || len(d.Holes[0].Runs) != 1 || d.Holes[0].Count != 1 {
		t.Errorf("a finished span-less child's tree holes = %+v, want no_spans naming it, count 1", d.Holes)
	}
}

// goldenTreeDB answers RunTreeSpans with a fixed tree holding every
// hole the answer can carry, so the golden pins each field the web
// reads (lib/api.ts's SpansDoc / TreeHole).
type goldenTreeDB struct {
	obsdb.DB
	tree obsdb.RunTree
}

func (g goldenTreeDB) RunTreeSpans(context.Context, obsdb.TreeQuery) (obsdb.RunTree, error) {
	return g.tree, nil
}

func (g goldenTreeDB) ChildRuns(ctx context.Context, ids []string) (map[string][]obsdb.RunRow, error) {
	return g.DB.(obsdb.TreeReader).ChildRuns(ctx, ids)
}

// TestSpansTreeGolden (U9.1-fix2) pins api/runs/{id}/spans?tree=1 as
// the web client reads it (testdata/api/spans-tree.golden.json; the
// TS mirror's test, src/lib/spans-tree.test.ts, reads the same file):
// a span with run_id and parent_run_id, and the holes — truncated
// (the span cap and a derived run's trace cap), not_recorded/no_spans
// with runs and count (the list capped), hidden.
func TestSpansTreeGolden(t *testing.T) {
	db, err := sqlite.Open(t.TempDir() + "/treegolden.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seedRun(t, db, "tg_root", "", "", "", nil)
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	tree := obsdb.RunTree{
		Spans: []obsdb.TreeSpan{
			{Span: treeTestSpan("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeee3", "0f00000000000001", "tg_root", "", "", base, 0), RunID: "tg_root"},
			{Span: treeTestSpan("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeee3", "0f00000000000002", "tg_root/0/call_a", "tg_root", "", base, 1),
				RunID: "tg_root/0/call_a", ParentRunID: "tg_root"},
		},
		Truncated: true, TraceTruncated: true, Hidden: true,
		Missing:      []obsdb.TreeRun{{ID: "tg_root/1/call_b", ParentRunID: "tg_root", Started: base}},
		MissingCount: 101,
	}
	srv := New(DB(goldenTreeDB{db, tree}))
	t.Cleanup(func() { _ = srv.Close() })
	code, _, body := get(t, srv.Handler(), "/studio/api/runs/tg_root/spans?tree=1")
	if code != http.StatusOK {
		t.Fatalf("?tree=1 = %d %s", code, body)
	}
	wefttest.Golden(t, "testdata/api/spans-tree.golden.json", []byte(pretty(t, body)))
	var d treeHoleDoc
	decode(t, body, &d)
	var kinds []string
	for _, h := range d.Holes {
		kinds = append(kinds, h.Hole+":"+h.Cause)
	}
	if got, want := strings.Join(kinds, ","), "truncated:response_cap,truncated:response_cap,not_recorded:no_spans,hidden:"; got != want {
		t.Errorf("holes = %s, want %s", got, want)
	}
	if h := d.Holes[2]; h.Count != 101 || len(h.Runs) != 1 || !strings.Contains(h.Reason, "the first 1 by start are named") {
		t.Errorf("no_spans hole = %+v, want count 101, one run named, the reason saying so", h)
	}
}
