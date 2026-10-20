package obsdbtest

import (
	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// summarizerSpanIsNotRunFact (P9-pre-go-fix2; record contract §3, §6):
// a summarizer chat span (weft.chat.role summarizer) thread files under
// the session's last run — a manual Compact between turns, a
// SummarizeLeft branch — is stored, and the summarizer usage reads sum
// it, but it is never the run's own fact, on both backends:
//
//   - its last_seen does not move: an ended run stays succeeded, an
//     interrupted one interrupted, whatever late summarizer span lands;
//   - one landing after DeleteRun recreates no row, and its session is
//     listed nowhere (Sessions, Session);
//   - SessionRow.SummarizerUsage still sums the spans of the runs kept.
func summarizerSpanIsNotRunFact(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) { SummarizerSpanIsNotRunFact(t, open(t)) }
}

// SummarizerSpanIsNotRunFact is the SummarizerSpanIsNotRunFact case of
// Run on one database db, empty of the case's sessions: a backend's
// upgrade test runs it on a database built at an older schema and
// opened by this binary (ClickHouse's 0027 restating 0024's view).
func SummarizerSpanIsNotRunFact(t *testing.T, db obsdb.DB) {
	t.Helper()
	const sid, gone = "s_sum", "s_sum_gone"
	ident := func(session string, turn int64) map[string]any {
		return map[string]any{"weft.session.id": session, "weft.public_id": "pub_" + session, "weft.turn": turn, "gen_ai.agent.name": "sum"}
	}
	shift := func(r obsdb.Record, d time.Duration) obsdb.Record {
		r.Time = at(d)
		return r
	}
	// t1 succeeded long ago; t2 started long ago and never finished
	// (interrupted); t3, alone in its session, is deleted, then a late
	// summarizer span lands under it.
	t1, t2, t3 := sid+"-t1", sid+"-t2", gone+"-t1"
	recs := []obsdb.Record{
		shift(record(t1, "event", "run_start", 0, `{"type":"run_start","id":"`+t1+`"}`, ident(sid, 1)), -10*time.Minute),
		shift(record(t1, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+t1+`","steps":1}`, ident(sid, 1)), -10*time.Minute+time.Second),
		shift(record(t2, "event", "run_start", 0, `{"type":"run_start","id":"`+t2+`"}`, ident(sid, 2)), -9*time.Minute),
		shift(record(t3, "event", "run_start", 0, `{"type":"run_start","id":"`+t3+`"}`, ident(gone, 1)), -8*time.Minute),
		shift(record(t3, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+t3+`","steps":1}`, ident(gone, 1)), -8*time.Minute+time.Second),
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
	summarizer := func(runID, session, traceID, spanID string, in int64) obsdb.Span {
		attrs := ident(session, 0)
		delete(attrs, "weft.turn")
		for k, v := range map[string]any{
			"gen_ai.operation.name": "chat", "weft.run.id": runID, obsdb.AttrChatRole: obsdb.ChatRoleSummarizer,
			"gen_ai.provider.name": "xai", "gen_ai.request.model": "summary-mini",
			"gen_ai.usage.input_tokens": in, "gen_ai.usage.output_tokens": int64(10),
		} {
			attrs[k] = v
		}
		return obsdb.Span{TraceID: traceID, SpanID: spanID,
			Name: "chat summary-mini", Kind: 3, Start: now, End: now.Add(time.Second), Service: "conf-svc", Attrs: attrs,
			Resource: map[string]any{"service.name": "conf-svc"}}
	}
	late := []obsdb.Span{
		summarizer(t1, sid, "7ace0000000000000000000000000001", "5ba0000000000001", 1800),
		summarizer(t2, sid, "7ace0000000000000000000000000002", "5ba0000000000002", 900),
		summarizer(t3, gone, "7ace0000000000000000000000000003", "5ba0000000000003", 700),
	}
	if err := db.Write(ctx(), obsdb.Batch{Spans: late}); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{t1, t2} {
		det, err := db.Run(ctx(), id)
		if err != nil {
			t.Fatal(err)
		}
		if b, r := before[id], det.RunRow; !r.LastSeen.Equal(b.LastSeen) {
			t.Errorf("%s last_seen = %v, want %v: a summarizer span never moves it", id, r.LastSeen, b.LastSeen)
		}
		if det.Provider == "xai" || det.Model == "summary-mini" || det.TraceID != before[id].TraceID {
			t.Errorf("%s model = %s/%s trace %q: the summarizing call is not the run's", id, det.Provider, det.Model, det.TraceID)
		}
	}
	if det, _ := db.Run(ctx(), t1); det.Status != obsdb.StatusSucceeded {
		t.Errorf("%s status = %s, want succeeded", t1, det.Status)
	}
	if det, _ := db.Run(ctx(), t2); det.Status != obsdb.StatusInterrupted {
		t.Errorf("%s status = %s, want interrupted: a late summarizer span is no sign of life", t2, det.Status)
	}
	if _, err := db.Run(ctx(), t3); !errors.Is(err, obsdb.ErrNotFound) {
		t.Errorf("Run(%s) after DeleteRun and a late summarizer span: err = %v, want ErrNotFound (no stub row)", t3, err)
	}
	if _, err := db.Session(ctx(), gone); !errors.Is(err, obsdb.ErrNotFound) {
		t.Errorf("Session(%s) after its one run was deleted: err = %v, want ErrNotFound", gone, err)
	}
	page, err := db.Sessions(ctx(), obsdb.SessionQuery{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var kept *obsdb.SessionRow
	for i, s := range page.Sessions {
		switch s.ID {
		case gone:
			t.Errorf("Sessions lists %s: a late summarizer span recreated its deleted run", gone)
		case sid:
			kept = &page.Sessions[i]
		}
	}
	if page.Total != 1 {
		t.Errorf("Sessions total = %d, want 1 (%s alone)", page.Total, sid)
	}
	if kept == nil {
		t.Fatalf("Sessions does not list %s", sid)
	}
	if kept.SummarizerUsage.InputTokens != 2700 || kept.SummarizerUsage.OutputTokens != 20 {
		t.Errorf("%s summarizer usage = %+v, want 2700 in / 20 out: the spans are stored and summed", sid, kept.SummarizerUsage)
	}
	if kept.Usage.InputTokens != 0 {
		t.Errorf("%s usage = %+v: the summarizer's is never the runs' own", sid, kept.Usage)
	}
	spans, err := db.RunSpans(ctx(), t1)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 || spans[0].SpanID != "5ba0000000000001" {
		t.Errorf("%s spans = %+v, want the summarizer span stored", t1, spans)
	}
}
