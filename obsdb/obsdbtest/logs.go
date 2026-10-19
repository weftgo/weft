package obsdbtest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/internal/caps"
)

// otherLogs pins DB.OtherLogs (plan A7): a run's app log records — the
// non-weft records of its traces — attributed through the spans they
// were emitted under, in time order, paged, filtered by severity, with
// a retried duplicate read once. The run's own spans and the app's
// span below its tool span count; another run's span below it (a
// subagent's) does not, nor a line with no span, another trace's, or
// any weft record — a thread record or compaction marker filed under no
// run (held at Close) included, though it rides the run's trace and
// span with no weft.run.id: it is the session's (SessionEventReader),
// never an app line, never counted, and DeleteRun leaves it to its
// session.
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
				// A line under a span that was never stored: a gap.
				appLog("dead00000000beef", 2*time.Second, 9, "orphan line", nil),
				obsdb.Record{Time: at(2 * time.Second), TraceID: "ff" + trace[2:], SpanID: tool, Severity: 17, Body: "another trace"},
				// Runless session records under the run's spans: an
				// approval.refused (WARN) and a compaction marker.
				obsdb.Record{Time: at(2 * time.Second), TraceID: trace, SpanID: tool, Severity: 13, EventName: "weft.thread.approval.refused",
					Body: `{"kind":"approval.refused","session":"s_logs","entry":"e2","reason":"bad_signature"}`, Service: "conf-svc",
					Attrs: map[string]any{"weft.record": obsdb.RecordThread, "weft.run.id": "", "weft.session.id": "s_logs", "weft.content": "none"}},
				obsdb.Record{Time: at(3 * time.Second), TraceID: trace, SpanID: invoke, Severity: 9, EventName: "weft.compaction",
					Body: `{"scope":"session","hash":"sha256:logs"}`, Service: "conf-svc",
					Attrs: map[string]any{"weft.record": obsdb.RecordCompaction, "weft.run.id": "", "weft.session.id": "s_logs",
						"weft.compaction.hash": "sha256:logs", "weft.compaction.scope": "session", "weft.content": "none"}},
			),
		}
		if err := db.Write(ctx(), batch); err != nil {
			t.Fatal(err)
		}
		// A retried transport's duplicate of the warning.
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{warn}}); err != nil {
			t.Fatal(err)
		}

		page, err := db.OtherLogs(ctx(), "r_logs", obsdb.LogQuery{})
		if err != nil {
			t.Fatal(err)
		}
		got := page.Logs
		// Finished: not partial, nothing cut; the orphan line is the gap.
		if page.Partial || page.Truncated || page.Gap != 1 {
			t.Errorf("page = partial %v truncated %v gap %d, want false false 1", page.Partial, page.Truncated, page.Gap)
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
		p1p, err := db.OtherLogs(ctx(), "r_logs", obsdb.LogQuery{Limit: 2})
		p1 := p1p.Logs
		if err != nil || !equalRows(rows(p1), want[:2]) {
			t.Fatalf("page 1 = %+v, %v", rows(p1), err)
		}
		p2, err := db.OtherLogs(ctx(), "r_logs", obsdb.LogQuery{From: p1[1].Index + 1, Limit: 2})
		if err != nil || !equalRows(rows(p2.Logs), want[2:]) {
			t.Fatalf("page 2 = %+v, %v", rows(p2.Logs), err)
		}

		// The severity filter keeps the indexes: a filtered walk
		// continues with From like an unfiltered one.
		warnUp, err := db.OtherLogs(ctx(), "r_logs", obsdb.LogQuery{MinSeverity: 13})
		if err != nil || !equalRows(rows(warnUp.Logs), want[1:]) {
			t.Fatalf("severity >= 13 = %+v, %v", rows(warnUp.Logs), err)
		}
		errOnly, err := db.OtherLogs(ctx(), "r_logs", obsdb.LogQuery{MinSeverity: 17, From: 2, Limit: 1})
		if err != nil || !equalRows(rows(errOnly.Logs), want[2:]) {
			t.Fatalf("severity >= 17 from 2 = %+v, %v", rows(errOnly.Logs), err)
		}

		// CountLogs (U4-fix): the same attribution, counted in one
		// severity-narrowed scan — the duplicate once, the child's,
		// the spanless and the other trace's lines never.
		if c, err := db.CountLogs(ctx(), "r_logs", 13); err != nil || c != (obsdb.LogCounts{Total: 2, Warn: 1, Error: 1}) {
			t.Errorf("CountLogs(r_logs, 13) = %+v, %v; want 2 lines: 1 WARN, 1 ERROR", c, err)
		}
		if c, err := db.CountLogs(ctx(), "r_logs", 17); err != nil || c != (obsdb.LogCounts{Total: 1, Error: 1}) {
			t.Errorf("CountLogs(r_logs, 17) = %+v, %v; want the 1 ERROR", c, err)
		}
		if _, err := db.CountLogs(ctx(), "nope", 13); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("CountLogs of an unknown run = %v, want ErrNotFound", err)
		}

		// The subagent's line is the child's.
		kid, err := db.OtherLogs(ctx(), "r_logs/0/c1", obsdb.LogQuery{})
		if err != nil || !equalRows(rows(kid.Logs), []row{{0, 17, "the child's line", child}}) {
			t.Errorf("child's logs = %+v, %v", rows(kid.Logs), err)
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
		if err != nil || none.Logs == nil || len(none.Logs) != 0 || none.Gap != 0 || none.Partial || !none.NoBridge {
			t.Errorf("a run without app logs = %#v, %v; want an empty slice, NoBridge (its root carries no weft.logs.bridged), nothing else", none, err)
		}
		// The same run recorded with the slog bridge on (U11.1): its root
		// carries weft.logs.bridged, so the empty page is the app's
		// silence, no hint.
		hushed := invokeSpan("r_hushed", 1, map[string]any{"weft.logs.bridged": int64(0)})
		hushed.TraceID, hushed.SpanID = "b2"+trace[2:], "2000000000000002"
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{hushed}, Records: finishedRun("r_hushed")}); err != nil {
			t.Fatal(err)
		}
		if got, err := db.OtherLogs(ctx(), "r_hushed", obsdb.LogQuery{}); err != nil || len(got.Logs) != 0 || got.NoBridge {
			t.Errorf("a bridged run without app logs = %#v, %v; want an empty page without NoBridge", got, err)
		}
		if got, err := db.OtherLogs(ctx(), "r_logs", obsdb.LogQuery{}); err != nil || got.NoBridge {
			t.Errorf("a run with app logs = NoBridge %v, %v; want false (lines arrived, whatever their path)", got.NoBridge, err)
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
		// A running one reads empty and partial: its spans may still be
		// on the way.
		young := record("r_young", "event", "run_start", 0, `{"type":"run_start","id":"r_young"}`, nil)
		young.Time = time.Now().UTC()
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{young}}); err != nil {
			t.Fatal(err)
		}
		if got, err := db.OtherLogs(ctx(), "r_young", obsdb.LogQuery{}); err != nil || got.Logs == nil || len(got.Logs) != 0 || !got.Partial {
			t.Errorf("a running run without spans = %#v, %v; want an empty, partial page", got, err)
		}
		// A running run with spans: partial, and a line under a span not
		// stored yet is no gap (the span may still arrive).
		live := invokeSpan("r_live", 1, map[string]any{"gen_ai.operation.name": "chat"})
		// A chat span, not the root: a stored invoke_agent span ends
		// the run (DeriveStatus's ended), and this one is still running.
		live.Name = "chat conf"
		live.TraceID, live.SpanID = "c1"+trace[2:], "3000000000000001"
		start := record("r_live", "event", "run_start", 0, `{"type":"run_start","id":"r_live"}`, nil)
		start.Time = time.Now().UTC()
		live.Start, live.End = start.Time, start.Time.Add(time.Second)
		pending := obsdb.Record{Time: start.Time.Add(500 * time.Millisecond), TraceID: live.TraceID,
			SpanID: "3000000000000002", Severity: 9, Body: "under an open tool span", Service: "conf-svc"}
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{live}, Records: []obsdb.Record{start, pending}}); err != nil {
			t.Fatal(err)
		}
		if got, err := db.OtherLogs(ctx(), "r_live", obsdb.LogQuery{}); err != nil || !got.Partial || got.Gap != 0 || len(got.Logs) != 0 || got.NoBridge {
			t.Errorf("a running run = %#v, %v; want partial, no gap, no lines yet, no NoBridge (its root is not stored)", got, err)
		}

		// Past MaxLogCandidates lines the read is cut and says so, at a
		// lowered cap (internal/caps): tens of lines, not ten thousand.
		lowerCaps(t, map[*int]int{&caps.LogCandidates: 20})
		many := invokeSpan("r_many", 1, nil)
		many.TraceID, many.SpanID = "d1"+trace[2:], "4000000000000001"
		lines := make([]obsdb.Record, 0, caps.LogCandidates+1)
		for i := 0; i <= caps.LogCandidates; i++ {
			lines = append(lines, obsdb.Record{Time: at(time.Second + time.Duration(i)*time.Microsecond),
				TraceID: many.TraceID, SpanID: many.SpanID, Severity: 9, Body: fmt.Sprintf("line %d", i), Service: "conf-svc"})
		}
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{many}, Records: append(finishedRun("r_many"), lines...)}); err != nil {
			t.Fatal(err)
		}
		last, err := db.OtherLogs(ctx(), "r_many", obsdb.LogQuery{From: int64(caps.LogCandidates) - 1, Limit: 10})
		if err != nil || !last.Truncated || len(last.Logs) != 1 || last.Logs[0].Index != int64(caps.LogCandidates)-1 ||
			last.Logs[0].Body != fmt.Sprintf("line %d", caps.LogCandidates-1) {
			t.Errorf("past the cap = truncated %v, %d rows %+v, %v; want the first %d lines and truncated", last.Truncated, len(last.Logs), last.Logs, err, caps.LogCandidates)
		}
		// Counting: the cap's lines, capped (a floor); none at WARN — the
		// severity narrows the scan, so INFO lines never reach the cap.
		if c, err := db.CountLogs(ctx(), "r_many", 1); err != nil || c != (obsdb.LogCounts{Total: int64(caps.LogCandidates), Capped: true}) {
			t.Errorf("CountLogs(r_many, 1) = %+v, %v; want %d, capped", c, err, caps.LogCandidates)
		}
		if c, err := db.CountLogs(ctx(), "r_many", 13); err != nil || c != (obsdb.LogCounts{}) {
			t.Errorf("CountLogs(r_many, 13) = %+v, %v; want none, not capped", c, err)
		}

		// The cap applies before attribution: a subagent flooding the
		// shared trace with MaxLogCandidates earlier lines leaves the
		// parent's own later line unread — truncated, not shown.
		parent := invokeSpan("r_flood", 1, nil)
		parent.TraceID, parent.SpanID = "e1"+trace[2:], "5000000000000001"
		kidSpan := parent
		kidSpan.SpanID, kidSpan.ParentSpanID = "5000000000000002", parent.SpanID
		kidSpan.Attrs = map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": "r_flood/0/c1"}
		flood := append(finishedRun("r_flood"), obsdb.Record{Time: at(2 * time.Second), TraceID: parent.TraceID,
			SpanID: parent.SpanID, Severity: 9, Body: "the parent's own line", Service: "conf-svc"})
		for i := 0; i < caps.LogCandidates; i++ {
			flood = append(flood, obsdb.Record{Time: at(time.Second + time.Duration(i)*time.Microsecond),
				TraceID: parent.TraceID, SpanID: kidSpan.SpanID, Severity: 9, Body: fmt.Sprintf("child %d", i), Service: "conf-svc"})
		}
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{parent, kidSpan}, Records: flood}); err != nil {
			t.Fatal(err)
		}
		fl, err := db.OtherLogs(ctx(), "r_flood", obsdb.LogQuery{})
		if err != nil || !fl.Truncated || len(fl.Logs) != 0 {
			t.Errorf("a flooded trace = truncated %v, %d rows, %v; want truncated and none of the parent's", fl.Truncated, len(fl.Logs), err)
		}

		// DeleteRun takes the run's app lines, never the session's
		// runless records under its spans.
		if er, ok := db.(obsdb.SessionEventReader); ok {
			if err := db.DeleteRun(ctx(), "r_logs"); err != nil {
				t.Fatal(err)
			}
			if p, err := er.SessionEvents(ctx(), obsdb.SessionEventQuery{SessionID: "s_logs"}); err != nil || p.Total != 2 {
				t.Errorf("s_logs' events after DeleteRun(r_logs) = %d (%v), want its 2 runless records", p.Total, err)
			}
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
