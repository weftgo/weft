package obsdbtest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// threadRecordsAreNotRunFacts (P9-pre-go-fix; record contract §4, §6):
// thread's records and compaction markers filed under a run are the
// session's state log, never the run's own facts, on both backends:
//
//   - its meta is the run's own: a later thread record carrying fewer
//     pairs than the turn's Send-level ones never wins it (ClickHouse's
//     max over Meta), so RunQuery.Meta and the sessions' tag filter
//     still match the run;
//   - its last_seen does not move: an ended run stays succeeded, an
//     interrupted one interrupted, whatever late record lands;
//   - one landing after DeleteRun recreates no row;
//   - a compaction marker re-sent with another body and time under the
//     same hash reads once (SessionEvents);
//   - a runless one carrying no weft.session.id is no session event: an
//     app log line of the run whose span it rides, as any other.
func threadRecordsAreNotRunFacts(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		const sid = "s_trf"
		pairs := map[string]any{"arena.batch": "b_trf", "tenant": "acme"}
		ident := func(turn int64) map[string]any {
			m := map[string]any{"weft.session.id": sid, "weft.public_id": "pub_trf", "weft.turn": turn, "gen_ai.agent.name": "trf"}
			for k, v := range pairs {
				m[k] = v
			}
			return m
		}
		shift := func(r obsdb.Record, d time.Duration) obsdb.Record {
			r.Time = at(d)
			return r
		}
		thread := func(runID string, when time.Time, entry string, attrs map[string]any) obsdb.Record {
			a := map[string]any{"weft.record": obsdb.RecordThread, "weft.run.id": runID, "weft.content": "none",
				"weft.session.id": sid, "weft.public_id": "pub_trf", "gen_ai.agent.name": "trf"}
			for k, v := range attrs {
				a[k] = v
			}
			return obsdb.Record{Time: when, EventName: "weft.thread.label", Severity: 9, Service: "conf-svc",
				TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "0102030405060708",
				Body: `{"kind":"label","session":"` + sid + `","entry":"` + entry + `","target":"e0","name":"x"}`, Attrs: a}
		}
		// t1 succeeded long ago; t2 started long ago and never finished
		// (interrupted); t3 is deleted, then a late record lands.
		t1, t2, t3 := sid+"-t1", sid+"-t2", sid+"-t3"
		recs := []obsdb.Record{
			shift(record(t1, "event", "run_start", 0, `{"type":"run_start","id":"`+t1+`"}`, ident(1)), -10*time.Minute),
			shift(record(t1, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+t1+`","steps":1}`, ident(1)), -10*time.Minute+time.Second),
			shift(record(t2, "event", "run_start", 0, `{"type":"run_start","id":"`+t2+`"}`, ident(2)), -9*time.Minute),
			shift(record(t3, "event", "run_start", 0, `{"type":"run_start","id":"`+t3+`"}`, ident(3)), -8*time.Minute),
			shift(record(t3, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+t3+`","steps":1}`, ident(3)), -8*time.Minute+time.Second),
		}
		if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
			t.Fatal(err)
		}
		before := map[string]obsdb.RunRow{}
		for _, id := range []string{t1, t2} {
			det, err := db.Run(ctx(), id)
			if err != nil {
				t.Fatal(err)
			}
			before[id] = det.RunRow
		}
		if err := db.DeleteRun(ctx(), t3); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		// Late records: the session's state log, filed under each run now —
		// one meta pair only (a record of a run the Session did not see
		// stamps what it has), and a compaction marker sent twice.
		marker := func(when time.Time, body string) obsdb.Record {
			return obsdb.Record{Time: when, EventName: "weft.compaction", Severity: 9, Service: "conf-svc", Body: body,
				Attrs: map[string]any{"weft.record": obsdb.RecordCompaction, "weft.run.id": t1, "weft.compaction.hash": "sha256:trf",
					"weft.compaction.scope": "session", "weft.content": "none", "weft.session.id": sid, "weft.public_id": "pub_trf",
					"tenant": "acme"}}
		}
		late := []obsdb.Record{
			thread(t1, now, "e1", map[string]any{"tenant": "acme"}),
			thread(t2, now, "e2", map[string]any{"tenant": "acme"}),
			thread(t3, now, "e3", map[string]any{"tenant": "acme"}),
			marker(now, `{"scope":"session","hash":"sha256:trf","entry":"e4","replaced":3}`),
			marker(now.Add(time.Second), `{"scope":"session","hash":"sha256:trf","entry":"e4","replaced":3,"held":true}`),
		}
		// A runless thread record with no session id, under t1's span: an
		// app log line, not a session event.
		stray := thread("", at(-10*time.Minute+500*time.Millisecond), "e9", nil)
		delete(stray.Attrs, "weft.session.id")
		stray.SpanID = "0a0b0c0d0e0f0102" // t1's invoke span
		stray.Body = `{"kind":"label","entry":"e9"}`
		late = append(late, stray)
		inv := invokeSpan(t1, 1, ident(1))
		inv.Start, inv.End = at(-10*time.Minute), at(-10*time.Minute+time.Second)
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{inv}, Records: late}); err != nil {
			t.Fatal(err)
		}

		for _, id := range []string{t1, t2} {
			det, err := db.Run(ctx(), id)
			if err != nil {
				t.Fatal(err)
			}
			b, r := before[id], det.RunRow
			if !r.LastSeen.Equal(maxTime(b.LastSeen, inv.End, id == t1)) {
				t.Errorf("%s last_seen = %v, want %v: a thread record never moves it", id, r.LastSeen, b.LastSeen)
			}
			if r.Meta["arena.batch"] != "b_trf" || r.Meta["tenant"] != "acme" {
				t.Errorf("%s meta = %v, want its own Send-level pairs", id, r.Meta)
			}
		}
		if det, _ := db.Run(ctx(), t1); det.Status != obsdb.StatusSucceeded {
			t.Errorf("%s status = %s, want succeeded", t1, det.Status)
		}
		if det, _ := db.Run(ctx(), t2); det.Status != obsdb.StatusInterrupted {
			t.Errorf("%s status = %s, want interrupted: a late thread record is no sign of life", t2, det.Status)
		}
		if _, err := db.Run(ctx(), t3); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("Run(%s) after DeleteRun and a late record: err = %v, want ErrNotFound (no stub row)", t3, err)
		}
		page, err := db.Runs(ctx(), obsdb.RunQuery{Meta: map[string]string{"arena.batch": "b_trf"}, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range page.Runs {
			ids = append(ids, r.ID)
		}
		slices.Sort(ids)
		if !slices.Equal(ids, []string{t1, t2}) {
			t.Errorf("RunQuery.Meta arena.batch = %v, want [%s %s]", ids, t1, t2)
		}
		sp, err := db.Sessions(ctx(), obsdb.SessionQuery{Meta: map[string]string{"arena.batch": "b_trf"}, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(sp.Sessions) != 1 || sp.Sessions[0].ID != sid {
			t.Errorf("SessionQuery.Meta arena.batch = %+v, want %s", sp.Sessions, sid)
		}
		if er, ok := db.(obsdb.SessionEventReader); ok {
			ev, err := er.SessionEvents(ctx(), obsdb.SessionEventQuery{SessionID: sid})
			if err != nil {
				t.Fatal(err)
			}
			markers := 0
			for _, e := range ev.Events {
				if e.Kind == obsdb.RecordCompaction {
					markers++
				}
			}
			if markers != 1 {
				t.Errorf("compaction markers of one hash = %d, want 1 (the marker's hash is its identity)", markers)
			}
			for _, e := range ev.Events {
				if e.RunID == "" {
					t.Errorf("a runless record with no session id read as session %s's event: %+v", sid, e)
				}
			}
		}
		logs, err := db.OtherLogs(ctx(), t1, obsdb.LogQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if len(logs.Logs) != 1 || logs.Logs[0].Body != stray.Body {
			t.Errorf("t1's app logs = %+v, want the runless thread record with no session id", logs.Logs)
		}
	}
}

// maxTime is the run's last_seen once its root span landed (t1's
// invoke span, written beside the late records), else before's.
func maxTime(before, spanEnd time.Time, withSpan bool) time.Time {
	if withSpan && spanEnd.After(before) {
		return spanEnd
	}
	return before
}

// sessionIDsFilter (P9-pre-go-fix): RunQuery.SessionIDs keeps the runs
// of any of the sessions named — one read for a set — with every other
// filter applying beside it (PublicID here), on both backends.
func sessionIDsFilter(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		var recs []obsdb.Record
		for _, r := range []struct{ id, sid, pub string }{
			{"sa-t1", "sa", "pub_x"}, {"sa-t2", "sa", "pub_x"}, {"sb-t1", "sb", "pub_y"}, {"sc-t1", "sc", "pub_x"}, {"lone", "", "pub_x"},
		} {
			extra := map[string]any{"weft.session.id": r.sid, "weft.public_id": r.pub}
			recs = append(recs, record(r.id, "event", "run_start", 0, `{"type":"run_start","id":"`+r.id+`"}`, extra))
		}
		if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
			t.Fatal(err)
		}
		ids := func(q obsdb.RunQuery) []string {
			t.Helper()
			q.ParentRunID, q.Limit = "*", 50
			page, err := db.Runs(ctx(), q)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, r := range page.Runs {
				out = append(out, r.ID)
			}
			slices.Sort(out)
			return out
		}
		if got := ids(obsdb.RunQuery{SessionIDs: []string{"sa", "sb"}}); !slices.Equal(got, []string{"sa-t1", "sa-t2", "sb-t1"}) {
			t.Errorf("SessionIDs [sa sb] = %v", got)
		}
		if got := ids(obsdb.RunQuery{SessionIDs: []string{"sa", "sb", "sc"}, PublicID: "pub_x"}); !slices.Equal(got, []string{"sa-t1", "sa-t2", "sc-t1"}) {
			t.Errorf("SessionIDs [sa sb sc] under pub_x = %v", got)
		}
	}
}
