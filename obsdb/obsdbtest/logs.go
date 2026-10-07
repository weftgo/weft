package obsdbtest

import (
	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// otherLogs pins DB.OtherLogs (plan A7): a run's app log records — the
// non-weft records of its traces — attributed through the spans they
// were emitted under, in time order, paged, filtered by severity, with
// a retried duplicate read once. The run's own spans and the app's
// span below its tool span count; another run's span below it (a
// subagent's) does not, nor a line with no span, another trace's, or
// any weft record.
func otherLogs(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		const (
			trace  = "a1a2a3a4a5a6a7a8a9aaabacadaeaf10"
			invoke = "1000000000000001"
			tool   = "1000000000000002"
			app    = "1000000000000003" // the app's own span inside the tool
			child  = "1000000000000004" // a subagent's invoke span, below the tool
		)
		span := func(id, parent, runID string, start time.Duration) obsdb.Span {
			attrs := map[string]any{"gen_ai.operation.name": "execute_tool"}
			if runID != "" {
				attrs["weft.run.id"] = runID
			} else {
				attrs = map[string]any{"db.system": "postgresql"}
			}
			return obsdb.Span{
				TraceID: trace, SpanID: id, ParentSpanID: parent, Name: "span " + id, Kind: 1,
				Start: at(start), End: at(start + time.Second), StatusCode: 1, Service: "conf-svc",
				Attrs: attrs, Resource: map[string]any{"service.name": "conf-svc"},
			}
		}
		inv := invokeSpan("r_logs", 1, nil)
		inv.TraceID, inv.SpanID = trace, invoke
		appLog := func(spanID string, d time.Duration, sev int, body string, attrs map[string]any) obsdb.Record {
			return obsdb.Record{
				Time: at(d), TraceID: trace, SpanID: spanID, Severity: sev, Body: body,
				Service: "conf-svc", Attrs: attrs, Resource: map[string]any{"service.name": "conf-svc"},
			}
		}
		warn := appLog(tool, 2*time.Second, 13, "slow lookup", map[string]any{"order": "42"})
		batch := obsdb.Batch{
			Spans: []obsdb.Span{inv, span(tool, invoke, "r_logs", time.Second), span(app, tool, "", 2*time.Second),
				span(child, tool, "r_logs/0/c1", 2*time.Second)},
			Records: append(finishedRun("r_logs"),
				appLog(invoke, time.Second, 9, "starting", map[string]any{"user": "u1"}),
				warn,
				appLog(app, 3*time.Second, 17, "db timeout", nil),
				appLog(child, 2500*time.Millisecond, 17, "the child's line", nil),
				appLog("", 2*time.Second, 17, "no span context", nil),
				obsdb.Record{Time: at(2 * time.Second), TraceID: "ff" + trace[2:], SpanID: tool, Severity: 17, Body: "another trace"},
			),
		}
		if err := db.Write(ctx(), batch); err != nil {
			t.Fatal(err)
		}
		// A retried transport's duplicate of the warning.
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{warn}}); err != nil {
			t.Fatal(err)
		}

		got, err := db.OtherLogs(ctx(), "r_logs", obsdb.LogQuery{})
		if err != nil {
			t.Fatal(err)
		}
		type row struct {
			index int64
			sev   int
			body  string
			span  string
		}
		rows := func(ls []obsdb.OtherLog) []row {
			out := []row{}
			for _, l := range ls {
				out = append(out, row{l.Index, l.Severity, l.Body, l.SpanID})
			}
			return out
		}
		want := []row{{0, 9, "starting", invoke}, {1, 13, "slow lookup", tool}, {2, 17, "db timeout", app}}
		if g := rows(got); !equalRows(g, want) {
			t.Fatalf("OtherLogs = %+v, want %+v", g, want)
		}
		if l := got[1]; !l.Time.Equal(at(2*time.Second)) || l.TraceID != trace || l.Service != "conf-svc" || l.Attrs["order"] != "42" {
			t.Errorf("the warning = %+v", l)
		}
		if len(got[2].Attrs) != 0 {
			t.Errorf("a line without attributes reads %v", got[2].Attrs)
		}

		// Paged: a page shorter than the limit is the last.
		p1, err := db.OtherLogs(ctx(), "r_logs", obsdb.LogQuery{Limit: 2})
		if err != nil || !equalRows(rows(p1), want[:2]) {
			t.Fatalf("page 1 = %+v, %v", rows(p1), err)
		}
		p2, err := db.OtherLogs(ctx(), "r_logs", obsdb.LogQuery{From: p1[1].Index + 1, Limit: 2})
		if err != nil || !equalRows(rows(p2), want[2:]) {
			t.Fatalf("page 2 = %+v, %v", rows(p2), err)
		}

		// The severity filter keeps the indexes: a filtered walk
		// continues with From like an unfiltered one.
		warnUp, err := db.OtherLogs(ctx(), "r_logs", obsdb.LogQuery{MinSeverity: 13})
		if err != nil || !equalRows(rows(warnUp), want[1:]) {
			t.Fatalf("severity >= 13 = %+v, %v", rows(warnUp), err)
		}
		errOnly, err := db.OtherLogs(ctx(), "r_logs", obsdb.LogQuery{MinSeverity: 17, From: 2, Limit: 1})
		if err != nil || !equalRows(rows(errOnly), want[2:]) {
			t.Fatalf("severity >= 17 from 2 = %+v, %v", rows(errOnly), err)
		}

		// The subagent's line is the child's.
		kid, err := db.OtherLogs(ctx(), "r_logs/0/c1", obsdb.LogQuery{})
		if err != nil || !equalRows(rows(kid), []row{{0, 17, "the child's line", child}}) {
			t.Errorf("child's logs = %+v, %v", rows(kid), err)
		}

		// An unknown run is ErrNotFound.
		if _, err := db.OtherLogs(ctx(), "nope", obsdb.LogQuery{}); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("unknown run: %v", err)
		}

		// A run with spans and no app logs: empty, not nil.
		quiet := invokeSpan("r_quiet", 1, nil)
		quiet.TraceID, quiet.SpanID = "b1"+trace[2:], "2000000000000001"
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{quiet}, Records: finishedRun("r_quiet")}); err != nil {
			t.Fatal(err)
		}
		none, err := db.OtherLogs(ctx(), "r_quiet", obsdb.LogQuery{})
		if err != nil || none == nil || len(none) != 0 {
			t.Errorf("a run without app logs = %#v, %v; want an empty slice", none, err)
		}

		// A finished run with no span has nothing to attribute through:
		// the not_recorded hole, an ErrNotFound.
		if err := db.Write(ctx(), obsdb.Batch{Records: finishedRun("r_bare")}); err != nil {
			t.Fatal(err)
		}
		_, err = db.OtherLogs(ctx(), "r_bare", obsdb.LogQuery{})
		var he *obsdb.HoleError
		if !errors.As(err, &he) || he.Kind != "logs" || he.Hole != obsdb.HoleNotRecorded || !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("a run without spans = %v, want a logs HoleError not_recorded", err)
		}
		// A running one reads empty: its spans may still be on the way.
		young := record("r_young", "event", "run_start", 0, `{"type":"run_start","id":"r_young"}`, nil)
		young.Time = time.Now().UTC()
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{young}}); err != nil {
			t.Fatal(err)
		}
		if got, err := db.OtherLogs(ctx(), "r_young", obsdb.LogQuery{}); err != nil || got == nil || len(got) != 0 {
			t.Errorf("a running run without spans = %#v, %v; want an empty slice", got, err)
		}
	}
}

func equalRows[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
