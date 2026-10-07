package studio

import (
	"context"
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
	if strings.Contains(body, "partial") || strings.Contains(body, "badge") {
		t.Errorf("a finished run's logs = %s, want neither partial nor a badge", body)
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
	if code != http.StatusOK || strings.TrimSpace(body) != `{"logs":[]}` {
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
	if code != http.StatusOK || strings.Contains(body, "badge") || !strings.HasPrefix(body, `{"logs":[]`) {
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
// running run's page is partial with the reason (no badge: a live
// condition, not a hole); a finished run's line under a span that was
// never stored is the gap badge with its count; past
// obsdb.MaxLogCandidates lines the page is truncated.
func TestLogsLiveGapTruncated(t *testing.T) {
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

	h := Handler(DB(db))
	code, _, body := get(t, h, "/studio/api/runs/r_live/logs")
	if code != http.StatusOK || !strings.Contains(body, `"partial":true`) ||
		!strings.Contains(body, `"reason":"`+logsPartialReason+`"`) || strings.Contains(body, "badge") ||
		!strings.Contains(body, `"body":"started"`) || strings.Contains(body, "inside the open tool") {
		t.Errorf("a running run's logs = %d %s, want the attributable line, partial and its reason, no badge", code, body)
	}
	code, _, body = get(t, h, "/studio/api/runs/r_gap/logs")
	if code != http.StatusOK || strings.Contains(body, "partial") || !strings.Contains(body, `"badge":"gap"`) ||
		!strings.Contains(body, `"reason":"1 log lines in the run's trace name a span that was never stored"`) ||
		!strings.Contains(body, `"body":"kept"`) || strings.Contains(body, "lost span") {
		t.Errorf("a gap = %d %s, want the kept line and the gap badge", code, body)
	}
	code, _, body = get(t, h, fmt.Sprintf("/studio/api/runs/r_many/logs?from=%d", obsdb.MaxLogCandidates-1))
	if code != http.StatusOK || !strings.Contains(body, `"badge":"truncated"`) ||
		!strings.Contains(body, `"reason":"the first 10 000 app log lines are shown"`) ||
		!strings.Contains(body, fmt.Sprintf(`"index":%d`, obsdb.MaxLogCandidates-1)) || strings.Contains(body, fmt.Sprintf("line %d", obsdb.MaxLogCandidates)) {
		t.Errorf("past the cap = %d %.400s, want the last kept line and the truncated badge", code, body)
	}
}
