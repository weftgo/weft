package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// TestLogsRoute pins GET /api/runs/{id}/logs (plan A7) over the
// fixture database: r_ok's two app log lines in time order, each with
// its index, severity name and number, body, attributes and span; the
// page, the severity filter that keeps indexes, the bad parameters, a
// run with spans and no logs (empty, no badge), a run with no span
// (the not_recorded badge with this route's reason and the tracer
// fix) and an unknown run (404).
func TestLogsRoute(t *testing.T) {
	h := Handler(DB(fixtureDB(t)))

	code, _, body := get(t, h, "/studio/api/runs/r_ok/logs")
	if code != http.StatusOK {
		t.Fatalf("logs: %d %s", code, body)
	}
	golden(t, "logs-ok.golden.json", body)
	if strings.Contains(body, "partial") || strings.Contains(body, "badge") || !strings.Contains(body, `"holes":[]`) {
		t.Errorf("a finished run's logs = %s, want neither partial nor a badge, and no holes", body)
	}

	code, _, body = get(t, h, "/studio/api/runs/r_ok/logs?limit=1")
	if code != http.StatusOK {
		t.Fatalf("logs?limit=1: %d %s", code, body)
	}
	golden(t, "logs-ok-paged.golden.json", body)
	code, _, body = get(t, h, "/studio/api/runs/r_ok/logs?from=1&limit=1")
	if code != http.StatusOK || !strings.Contains(body, `"index":1`) || strings.Contains(body, `"index":0`) || !strings.Contains(body, `"next_from":2`) {
		t.Errorf("logs?from=1&limit=1 = %d %s, want index 1 and next_from 2 (a full page)", code, body)
	}
	code, _, body = get(t, h, "/studio/api/runs/r_ok/logs?from=2")
	if code != http.StatusOK || strings.TrimSpace(body) != `{"logs":[],"holes":[]}` {
		t.Errorf("logs?from=2 = %d %q, want an empty last page", code, body)
	}

	for _, sev := range []string{"warn", "WARN", "warning", "13"} {
		code, _, body = get(t, h, "/studio/api/runs/r_ok/logs?severity="+sev)
		if code != http.StatusOK || !strings.Contains(body, `"index":1`) || strings.Contains(body, `"index":0`) ||
			!strings.Contains(body, `"severity":"WARN"`) {
			t.Errorf("logs?severity=%s = %d %s, want the warning alone at index 1", sev, code, body)
		}
	}
	code, _, body = get(t, h, "/studio/api/runs/r_ok/logs?severity=error")
	if code != http.StatusOK || !strings.HasPrefix(body, `{"logs":[]`) {
		t.Errorf("logs?severity=error = %d %s, want none", code, body)
	}
	for _, q := range []string{"severity=loud", "severity=25", "from=-1", "from=x", "limit=-2"} {
		if code, _, body := get(t, h, "/studio/api/runs/r_ok/logs?"+q); code != http.StatusBadRequest {
			t.Errorf("logs?%s = %d %s, want 400", q, code, body)
		}
	}

	// r_fail has a span and logged nothing: empty, no badge.
	code, _, body = get(t, h, "/studio/api/runs/r_fail/logs")
	if code != http.StatusOK || strings.Contains(body, "badge") || !strings.HasPrefix(body, `{"logs":[]`) || !strings.Contains(body, `"holes":[]`) {
		t.Errorf("r_fail logs = %d %s, want an empty list without a badge", code, body)
	}
	// r_stale was recorded without a tracer: nothing to attribute
	// through.
	code, _, body = get(t, h, "/studio/api/runs/r_stale/logs")
	if code != http.StatusOK {
		t.Fatalf("r_stale logs: %d %s", code, body)
	}
	golden(t, "logs-not-recorded.golden.json", body)
	// The child id's slashes route like every run sub-route.
	if code, _, body := get(t, h, "/studio/api/runs/r_sub/0/call_3/logs"); code != http.StatusOK || !strings.HasPrefix(body, `{"logs":[]`) {
		t.Errorf("child logs = %d %s", code, body)
	}
	if code, _, body := get(t, h, "/studio/api/runs/r_nope/logs"); code != http.StatusNotFound {
		t.Errorf("unknown run logs = %d %s, want 404", code, body)
	}
}

// TestRunRowDeltaCount: the run row carries delta_count, the deltas
// obsdb counted and never stored.
func TestRunRowDeltaCount(t *testing.T) {
	h := Handler(DB(fixtureDB(t)))
	for _, path := range []string{"/studio/api/runs/r_ok", "/studio/api/runs"} {
		code, _, body := get(t, h, path)
		if code != http.StatusOK || !strings.Contains(body, `"delta_count":0`) {
			t.Errorf("%s = %d %s, want delta_count on the row", path, code, body)
		}
	}
}

// TestLogsLiveGapTruncated: what the page cannot show is said. A
// running run's page is partial with its reason (no badge: a live
// condition, not a hole); a finished run's lines under a span that is
// not stored are the gap hole with their count, worded as anyone's;
// past obsdb.MaxLogCandidates lines in the trace the page is
// truncated — even when a subagent's lines filled the cap and the
// page shows none; holes lists every hole, the badge the first.
func TestLogsLiveGapTruncated(t *testing.T) {
	h0 := func(db obsdb.DB) http.Handler { return Handler(DB(db)) }
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	span := func(run, trace, id string, start time.Time) obsdb.Span {
		return obsdb.Span{TraceID: trace, SpanID: id, Name: "invoke_agent orders", Kind: 1,
			Start: start, End: start.Add(time.Second), StatusCode: 1, Service: "studio-test",
			Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": run}}
	}
	line := func(trace, spanID string, at time.Time, body string) obsdb.Record {
		return obsdb.Record{Time: at, TraceID: trace, SpanID: spanID, Severity: 9, Body: body, Service: "studio-test"}
	}
	write := func(b obsdb.Batch) {
		t.Helper()
		if err := db.Write(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	// r_live: running (no finish, seen now), one line under its span and
	// one under a tool span still open.
	now := time.Now().UTC()
	const liveTrace = "11111111111111111111111111111111"
	write(obsdb.Batch{Spans: []obsdb.Span{span("r_live", liveTrace, "1111111111111111", now)}, Records: []obsdb.Record{
		fxRecord("r_live", "event", "run_start", 0, now, `{"type":"run_start","id":"r_live"}`),
		line(liveTrace, "1111111111111111", now.Add(time.Millisecond), "started"),
		line(liveTrace, "1111111111111112", now.Add(2*time.Millisecond), "inside the open tool"),
	}})
	// r_gap: finished, a line under a span never stored.
	const gapTrace = "22222222222222222222222222222222"
	write(obsdb.Batch{Spans: []obsdb.Span{span("r_gap", gapTrace, "2222222222222221", fixtureT0)}, Records: []obsdb.Record{
		fxRecord("r_gap", "event", "run_start", 0, fixtureT0, `{"type":"run_start","id":"r_gap"}`),
		fxRecord("r_gap", "event", "run_finish", 1, fixtureT0.Add(time.Second), `{"type":"run_finish","run_id":"r_gap"}`),
		line(gapTrace, "2222222222222221", fixtureT0.Add(time.Millisecond), "kept"),
		line(gapTrace, "dead00000000beef", fixtureT0.Add(2*time.Millisecond), "lost span"),
	}})
	// r_many: finished, one line past the cap.
	const manyTrace = "33333333333333333333333333333333"
	many := []obsdb.Record{
		fxRecord("r_many", "event", "run_start", 0, fixtureT0, `{"type":"run_start","id":"r_many"}`),
		fxRecord("r_many", "event", "run_finish", 1, fixtureT0.Add(time.Second), `{"type":"run_finish","run_id":"r_many"}`),
	}
	for i := 0; i <= obsdb.MaxLogCandidates; i++ {
		many = append(many, line(manyTrace, "3333333333333331", fixtureT0.Add(time.Duration(i)*time.Microsecond), fmt.Sprintf("line %d", i)))
	}
	write(obsdb.Batch{Spans: []obsdb.Span{span("r_many", manyTrace, "3333333333333331", fixtureT0)}, Records: many})

	// r_gap2: finished, two lines under spans never stored.
	const gap2Trace = "44444444444444444444444444444444"
	write(obsdb.Batch{Spans: []obsdb.Span{span("r_gap2", gap2Trace, "4444444444444441", fixtureT0)}, Records: []obsdb.Record{
		fxRecord("r_gap2", "event", "run_start", 0, fixtureT0, `{"type":"run_start","id":"r_gap2"}`),
		fxRecord("r_gap2", "event", "run_finish", 1, fixtureT0.Add(time.Second), `{"type":"run_finish","run_id":"r_gap2"}`),
		line(gap2Trace, "dead00000000bee1", fixtureT0.Add(time.Millisecond), "lost 1"),
		line(gap2Trace, "dead00000000bee2", fixtureT0.Add(2*time.Millisecond), "lost 2"),
	}})
	// r_flood: finished; its subagent floods the shared trace with
	// MaxLogCandidates early lines, so the parent's own later line is
	// past the cap — the cap applies before attribution.
	const floodTrace = "55555555555555555555555555555555"
	child := span("r_flood/0/c1", floodTrace, "5555555555555552", fixtureT0)
	child.ParentSpanID = "5555555555555551"
	flood := []obsdb.Record{
		fxRecord("r_flood", "event", "run_start", 0, fixtureT0, `{"type":"run_start","id":"r_flood"}`),
		fxRecord("r_flood", "event", "run_finish", 1, fixtureT0.Add(time.Second), `{"type":"run_finish","run_id":"r_flood"}`),
		line(floodTrace, "5555555555555551", fixtureT0.Add(500*time.Millisecond), "the parent's own line"),
	}
	for i := 0; i < obsdb.MaxLogCandidates; i++ {
		flood = append(flood, line(floodTrace, "5555555555555552", fixtureT0.Add(time.Duration(i)*time.Microsecond), fmt.Sprintf("child %d", i)))
	}
	write(obsdb.Batch{Spans: []obsdb.Span{span("r_flood", floodTrace, "5555555555555551", fixtureT0), child}, Records: flood})

	type hole struct {
		Hole, Reason, Fix string
	}
	type doc struct {
		Logs          []logRow `json:"logs"`
		Partial       bool     `json:"partial"`
		PartialReason string   `json:"partial_reason"`
		Holes         []hole   `json:"holes"`
		Badge         string   `json:"badge"`
		Reason        string   `json:"reason"`
		Fix           string   `json:"fix"`
	}
	read := func(path string) doc {
		t.Helper()
		code, _, body := get(t, h0(db), path)
		var d doc
		if code != http.StatusOK || json.Unmarshal([]byte(body), &d) != nil || d.Holes == nil {
			t.Fatalf("%s = %d %s, want 200 with holes", path, code, body)
		}
		return d
	}
	bodies := func(d doc) []string {
		out := []string{}
		for _, l := range d.Logs {
			out = append(out, l.Body)
		}
		return out
	}

	// Running: partial with its reason, and — no hole — reason too.
	live := read("/studio/api/runs/r_live/logs")
	if !live.Partial || live.PartialReason != logsPartialReason || live.Reason != logsPartialReason || live.Badge != "" ||
		len(live.Holes) != 0 || strings.Join(bodies(live), "|") != "started" {
		t.Errorf("a running run's logs = %+v, want the attributable line, partial with its reason, no hole", live)
	}
	// A gap: one line, singular; the lines may be anyone's.
	gap := read("/studio/api/runs/r_gap/logs")
	const gap1 = "1 log line in this run's trace and time window names a span that is not stored (dropped, or still open): it may belong to this run or to another in the same trace"
	if gap.Partial || gap.Badge != "gap" || gap.Reason != gap1 || gap.Fix == "" || len(gap.Holes) != 1 ||
		gap.Holes[0] != (hole{"gap", gap1, gap.Fix}) || strings.Join(bodies(gap), "|") != "kept" {
		t.Errorf("a gap = %+v, want the kept line and the gap hole", gap)
	}
	gap2 := read("/studio/api/runs/r_gap2/logs")
	if want := "2 log lines in this run's trace and time window name a span that is not stored (dropped, or still open): they may belong to this run or to another in the same trace"; gap2.Reason != want {
		t.Errorf("two gap lines: reason %q, want %q", gap2.Reason, want)
	}
	// Past the cap: truncated with the honest reason and a fix.
	const truncReason = "the run's traces hold more than 10 000 log lines in its window: only lines among the first 10 000 by time were read; later lines of this run may be missing"
	capped := read(fmt.Sprintf("/studio/api/runs/r_many/logs?from=%d", obsdb.MaxLogCandidates-1))
	if capped.Badge != "truncated" || capped.Reason != truncReason || capped.Fix != logsTruncatedFix || len(capped.Holes) != 1 ||
		len(capped.Logs) != 1 || capped.Logs[0].Index != obsdb.MaxLogCandidates-1 {
		t.Errorf("past the cap = %+v holes %+v, want the last kept line and the truncated hole", capped.Badge, capped.Holes)
	}
	// A flooding subagent: the parent's own line is past the cap; the
	// page is empty and says why, never "10 000 shown".
	fl := read("/studio/api/runs/r_flood/logs")
	if len(fl.Logs) != 0 || fl.Badge != "truncated" || fl.Reason != truncReason || strings.Contains(fl.Reason, "shown") {
		t.Errorf("a flooded trace = %d lines, badge %q reason %q; want none, truncated, the honest reason", len(fl.Logs), fl.Badge, fl.Reason)
	}
	// Every hole a page has, deduped, in the table's order: a truncated
	// page with a lost span lists truncated then gap.
	write(obsdb.Batch{Records: []obsdb.Record{line(floodTrace, "dead00000000beef", fixtureT0.Add(time.Microsecond/2), "lost early")}})
	both := read("/studio/api/runs/r_flood/logs")
	if len(both.Holes) != 2 || both.Holes[0].Hole != "truncated" || both.Holes[1].Hole != "gap" || both.Badge != "truncated" {
		t.Errorf("truncated and a gap = %+v, want [truncated gap] with the badge the first", both.Holes)
	}
}
