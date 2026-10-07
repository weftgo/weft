package clickhouse_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// eventRec is one weft event record at base+off, with the attributes
// the real pipeline stamps on every record of a run (extra carries the
// run's metadata — session, turn — and, on run_start only, the parent
// linkage: observe.go adds weft.parent.run.id to the RunStart record
// and the invoke_agent span, never to the records that follow).
func eventRec(runID, eventType string, pos int64, at time.Time, body string, extra map[string]any) obsdb.Record {
	attrs := map[string]any{
		"weft.record": "event", "weft.run.id": runID,
		"weft.event.type": eventType, "weft.event.pos": pos,
		"gen_ai.agent.name": "conf",
	}
	for k, v := range extra {
		attrs[k] = v
	}
	return obsdb.Record{
		Time: at, EventName: "weft.event", Severity: 9, Body: body,
		Service: "conf-svc", Attrs: attrs,
		Resource: map[string]any{"service.name": "conf-svc"},
	}
}

// The list filters must read a run's merged row, not the single rows
// each insert leaves in weft_runs until a background merge collapses
// them. A subagent run names its parent only on its run_start record
// (and its invoke_agent span); every later record of the child carries
// the inherited session metadata and no parent. Filtering the unmerged
// rows let those later rows pass `ParentRunID = ”`: the child showed up
// in the top-level list and as an extra turn of its parent's session
// (its usage summed in a second time), and a `ParentRunID = <parent>`
// read aggregated only the run_start row — the child read running
// forever. Merges are stopped so the pre-merge state the bug needs is
// the state the test reads, not a race with the merge scheduler.
func TestFiltersReadMergedRunsAcrossBatches(t *testing.T) {
	db, dsn := openFresh(t)
	conn := openRaw(t, dsn)
	defer func() { _ = conn.Close() }()
	if err := conn.Exec(ctx(), "SYSTEM STOP MERGES weft_runs"); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-10 * time.Second)
	session := map[string]any{"weft.session.id": "s_1", "weft.public_id": "pub_1", "weft.turn": "1"}
	child := map[string]any{"weft.parent.run.id": "parent", "weft.parent.call.id": "call_1"}
	for k, v := range session {
		child[k] = v
	}
	// Batch one: both runs start. Batch two: the child's later records
	// (no parent attributes — the real shape) and both finishes.
	if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
		eventRec("parent", "run_start", 0, base, `{"type":"run_start","id":"parent"}`, session),
		eventRec("kid", "run_start", 0, base.Add(time.Second), `{"type":"run_start","id":"kid"}`, child),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
		eventRec("kid", "step_start", 1, base.Add(2*time.Second), `{"type":"step_start","run_id":"kid","index":0}`, session),
		eventRec("kid", "run_finish", 2, base.Add(3*time.Second),
			`{"type":"run_finish","run_id":"kid","usage":{"input_tokens":7,"output_tokens":3},"steps":1}`, session),
		eventRec("parent", "run_finish", 1, base.Add(4*time.Second),
			`{"type":"run_finish","run_id":"parent","usage":{"input_tokens":10,"output_tokens":2},"steps":1}`, session),
	}}); err != nil {
		t.Fatal(err)
	}

	top, err := db.Runs(ctx(), obsdb.RunQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if top.Total != 1 || len(top.Runs) != 1 || top.Runs[0].ID != "parent" {
		t.Errorf("top-level runs = total %d %v, want only parent (a child's later records must not surface it)",
			top.Total, idsOf(top.Runs))
	}
	kids, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(kids.Runs) != 1 || kids.Runs[0].ID != "kid" {
		t.Fatalf("children of parent = %v, want kid", idsOf(kids.Runs))
	}
	if k := kids.Runs[0]; k.Status != obsdb.StatusSucceeded || k.Usage.InputTokens != 7 || k.EventCount != 3 ||
		!k.LastSeen.Equal(base.Add(3*time.Second)) {
		t.Errorf("kid row = status %q usage %+v events %d last-seen %v, want the whole run's aggregates (succeeded, 7 in, 3 events, +3s)",
			k.Status, k.Usage, k.EventCount, k.LastSeen)
	}
	byAgent, err := db.Runs(ctx(), obsdb.RunQuery{Agent: "conf", ParentRunID: "*", Status: obsdb.StatusSucceeded})
	if err != nil {
		t.Fatal(err)
	}
	if byAgent.Total != 2 {
		t.Errorf("agent+status filter = %d runs, want 2", byAgent.Total)
	}

	sess, err := db.Sessions(ctx(), obsdb.SessionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if sess.Total != 1 || len(sess.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", sess.Total)
	}
	if s := sess.Sessions[0]; s.Turns != 1 || s.Usage.InputTokens != 10 || s.Usage.OutputTokens != 2 {
		t.Errorf("session = %d turns, usage %+v — want 1 turn, 10/2 (the subagent run is not a turn; its usage is already in the parent's)",
			s.Turns, s.Usage)
	}
	det, err := db.Session(ctx(), "s_1")
	if err != nil {
		t.Fatal(err)
	}
	if det.Turns != 1 || det.Usage.InputTokens != 10 || len(det.Runs) != 1 {
		t.Errorf("session detail = %d turns, usage %+v, %d runs — want 1, 10 in, 1", det.Turns, det.Usage, len(det.Runs))
	}
}

// A failed run forwarded by the pinned collector must read failed. The
// exporter at v0.162.0 appends span.Kind().String() and
// spanStatus.Code().String() — pdata's spellings "Internal", "Error" —
// not the OTLP proto value names migration 0001's view compared against
// (STATUS_CODE_ERROR), so a collector-fed failure never set Failed and
// the run read running, then interrupted. The span itself read back
// with kind and status code 0.
func TestCollectorFailedRunReadsFailed(t *testing.T) {
	db, dsn := openFresh(t)
	conn := openRaw(t, dsn)
	defer func() { _ = conn.Close() }()
	at := time.Now().UTC().Add(-time.Minute)
	batch, err := conn.PrepareBatch(ctx(), fmt.Sprintf(collectorTracesInsert, parseDB(t, dsn), "otel_traces"))
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Append(
		at, "0102030405060708090a0b0c0d0e0f10", "0a0b0c0d0e0f0102", "", "",
		"invoke_agent conf", "Internal", "conf-svc",
		map[string]string{"service.name": "conf-svc"}, "", "",
		map[string]string{
			"gen_ai.operation.name": "invoke_agent", "weft.run.id": "collector-failed",
			"gen_ai.agent.name": "conf", "error.type": "model",
		},
		uint64(2000000000), "Error", "model: 500",
		[]time.Time{}, []string{}, []map[string]string{},
		[]string{}, []string{}, []string{}, []map[string]string{},
	); err != nil {
		t.Fatal(err)
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	det, err := db.Run(ctx(), "collector-failed")
	if err != nil {
		t.Fatal(err)
	}
	if det.Status != obsdb.StatusFailed || det.Err != "model: 500" {
		t.Errorf("collector-fed failed run = status %q err %q, want failed / the span's status message", det.Status, det.Err)
	}
	spans := mustTrace(t, db, "0102030405060708090a0b0c0d0e0f10")
	if len(spans) != 1 || spans[0].StatusCode != 2 || spans[0].Kind != 1 {
		t.Errorf("collector span read = %+v, want status code 2 (error), kind 1 (internal)", spans)
	}
	// A weft-written failure reads the same, and lands in the collector's
	// own spelling — one shape in the table, whoever wrote the row.
	if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{{
		TraceID: "aa02030405060708090a0b0c0d0e0f10", SpanID: "aa0b0c0d0e0f0102",
		Name: "invoke_agent conf", Kind: 1, Start: at, End: at.Add(time.Second),
		StatusCode: 2, StatusMessage: "boom", Service: "conf-svc",
		Attrs: map[string]any{"gen_ai.operation.name": "invoke_agent", "weft.run.id": "weft-failed"},
	}}}); err != nil {
		t.Fatal(err)
	}
	own, err := db.Run(ctx(), "weft-failed")
	if err != nil {
		t.Fatal(err)
	}
	if own.Status != obsdb.StatusFailed || own.Err != "boom" {
		t.Errorf("weft-written failed run = status %q err %q", own.Status, own.Err)
	}
	var kind, code string
	if err := conn.QueryRow(ctx(),
		"SELECT SpanKind, StatusCode FROM otel_traces WHERE SpanId = 'aa0b0c0d0e0f0102'").Scan(&kind, &code); err != nil {
		t.Fatal(err)
	}
	if kind != "Internal" || code != "Error" {
		t.Errorf("weft-written span stored kind %q / status %q, want the pinned exporter's spelling Internal / Error", kind, code)
	}
}

// Rows written before migration 0003 carry the OTLP proto value names
// (this package's own spelling until then, and what older exporters
// wrote): they must keep reading failed after the upgrade.
func TestLegacyStatusSpellingStillReads(t *testing.T) {
	db, dsn := openFresh(t)
	conn := openRaw(t, dsn)
	defer func() { _ = conn.Close() }()
	at := time.Now().UTC().Add(-time.Minute)
	batch, err := conn.PrepareBatch(ctx(), fmt.Sprintf(collectorTracesInsert, parseDB(t, dsn), "otel_traces"))
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Append(
		at, "0102030405060708090a0b0c0d0e0f10", "0a0b0c0d0e0f0102", "", "",
		"invoke_agent conf", "SPAN_KIND_INTERNAL", "conf-svc",
		map[string]string{}, "", "",
		map[string]string{"gen_ai.operation.name": "invoke_agent", "weft.run.id": "legacy-failed"},
		uint64(2000000000), "STATUS_CODE_ERROR", "old spelling",
		[]time.Time{}, []string{}, []map[string]string{},
		[]string{}, []string{}, []string{}, []map[string]string{},
	); err != nil {
		t.Fatal(err)
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	det, err := db.Run(ctx(), "legacy-failed")
	if err != nil {
		t.Fatal(err)
	}
	if det.Status != obsdb.StatusFailed || det.Err != "old spelling" {
		t.Errorf("legacy-spelled failed run = status %q err %q, want failed", det.Status, det.Err)
	}
	spans := mustTrace(t, db, "0102030405060708090a0b0c0d0e0f10")
	if len(spans) != 1 || spans[0].StatusCode != 2 || spans[0].Kind != 1 {
		t.Errorf("legacy span read = %+v, want status code 2, kind 1", spans)
	}
}

// Created and Updated keep the save's sub-second time, like the SQLite
// backend's nanosecond columns: a positional time.Time bind renders at
// whole seconds (the driver's rule the cursor fix already met), so both
// read back floored — Updated earlier than the instant before the save.
func TestExperimentTimesKeepSubSecond(t *testing.T) {
	db, _ := openFresh(t)
	// Not on a second boundary: the floor must be visible.
	for time.Now().Nanosecond() < int(50*time.Millisecond) {
		time.Sleep(10 * time.Millisecond)
	}
	before := time.Now().UTC()
	if err := db.SaveExperiment(ctx(), obsdb.Experiment{ID: "exp_t", Name: "n"}); err != nil {
		t.Fatal(err)
	}
	first, err := db.Experiment(ctx(), "exp_t")
	if err != nil {
		t.Fatal(err)
	}
	if first.Updated.Before(before) || first.Created.Before(before) {
		t.Errorf("created %v / updated %v precede the save's start %v — floored to the second",
			first.Created, first.Updated, before)
	}
	if err := db.SaveExperiment(ctx(), obsdb.Experiment{ID: "exp_t", Name: "n2"}); err != nil {
		t.Fatal(err)
	}
	second, err := db.Experiment(ctx(), "exp_t")
	if err != nil {
		t.Fatal(err)
	}
	if !second.Created.Equal(first.Created) {
		t.Errorf("created moved on update: %v → %v", first.Created, second.Created)
	}
	if !second.Updated.After(first.Updated) {
		t.Errorf("updated did not advance: %v → %v", first.Updated, second.Updated)
	}
}

// ResolvePublicID picks the newest turn by (turn, started) — the SQLite
// backend's order. Two sessions sharing a public id at the same turn:
// the later-started one wins even when the earlier one was seen last.
func TestResolvePublicIDOrder(t *testing.T) {
	db, _ := openFresh(t)
	base := time.Now().UTC().Add(-time.Minute)
	meta := func(session string) map[string]any {
		return map[string]any{"weft.session.id": session, "weft.public_id": "pub_shared", "weft.turn": "1"}
	}
	if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
		eventRec("a-t1", "run_start", 0, base, `{"type":"run_start","id":"a-t1"}`, meta("s_a")),
		eventRec("b-t1", "run_start", 0, base.Add(5*time.Second), `{"type":"run_start","id":"b-t1"}`, meta("s_b")),
		eventRec("a-t1", "step_start", 1, base.Add(10*time.Second), `{"type":"step_start","run_id":"a-t1","index":0}`, meta("s_a")),
	}}); err != nil {
		t.Fatal(err)
	}
	sid, err := db.ResolvePublicID(ctx(), "pub_shared")
	if err != nil {
		t.Fatal(err)
	}
	if sid != "s_b" {
		t.Errorf("resolve = %q, want s_b (the later-started turn, sqlite's turn DESC, started DESC)", sid)
	}
}

// Gaps are bounded (obsdb.MaxGaps, lowest first): positions are the
// sender's numbers, and one stray high position must not turn a page
// read into a list of every position below it.
func TestEventGapsAreBounded(t *testing.T) {
	db, _ := openFresh(t)
	at := time.Now().UTC().Add(-time.Minute)
	if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
		eventRec("g1", "run_start", 0, at, `{"type":"run_start","id":"g1"}`, nil),
		eventRec("g1", "step_start", 5000, at.Add(time.Second), `{"type":"step_start","run_id":"g1","index":0}`, nil),
	}}); err != nil {
		t.Fatal(err)
	}
	page, err := db.Events(ctx(), "g1", -1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(page.Events))
	}
	if len(page.Gaps) != obsdb.MaxGaps || page.Gaps[0] != 1 || page.Gaps[obsdb.MaxGaps-1] != int64(obsdb.MaxGaps) {
		t.Errorf("gaps = %d (first %v), want the lowest %d: 1..%d", len(page.Gaps), page.Gaps[:1], obsdb.MaxGaps, obsdb.MaxGaps)
	}
}

// Session returns every turn (SessionDetail's contract): the 500-row
// cap kept the oldest turns and silently dropped the newest.
func TestSessionReturnsEveryTurn(t *testing.T) {
	db, _ := openFresh(t)
	base := time.Now().UTC().Add(-time.Hour)
	const turns = 501
	var recs []obsdb.Record
	for i := 1; i <= turns; i++ {
		id := fmt.Sprintf("s_long-t%d", i)
		recs = append(recs, eventRec(id, "run_start", 0, base.Add(time.Duration(i)*time.Second),
			`{"type":"run_start","id":"`+id+`"}`,
			map[string]any{"weft.session.id": "s_long", "weft.turn": fmt.Sprint(i)}))
	}
	if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
	det, err := db.Session(ctx(), "s_long")
	if err != nil {
		t.Fatal(err)
	}
	if det.Turns != turns || len(det.Runs) != turns || det.Runs[turns-1].Turn != turns {
		t.Errorf("session = %d turns, %d runs (last turn %d), want all %d",
			det.Turns, len(det.Runs), det.Runs[len(det.Runs)-1].Turn, turns)
	}
}

// Session is uncapped, so its count read must hold any number of runs:
// fillCounts listed every run id as a bound literal in one IN (...), and
// past ~10k turns the query text outgrew the server's max_query_size —
// the whole session detail failed.
func TestSessionCountsBeyondQuerySize(t *testing.T) {
	db, _ := openFresh(t)
	base := time.Now().UTC().Add(-time.Hour)
	const turns = 14000
	recs := make([]obsdb.Record, 0, turns)
	for i := 1; i <= turns; i++ {
		id := fmt.Sprintf("s_huge-run-with-a-long-identifier-t%05d", i)
		recs = append(recs, eventRec(id, "run_start", 0, base.Add(time.Duration(i)*time.Millisecond),
			`{"type":"run_start","id":"`+id+`"}`,
			map[string]any{"weft.session.id": "s_huge", "weft.turn": fmt.Sprint(i)}))
	}
	if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
	det, err := db.Session(ctx(), "s_huge")
	if err != nil {
		t.Fatalf("a %d-turn session: %v", turns, err)
	}
	if len(det.Runs) != turns || det.Runs[0].EventCount != 1 || det.Runs[turns-1].EventCount != 1 {
		t.Errorf("session = %d runs (event counts %d…%d), want %d with their counts",
			len(det.Runs), det.Runs[0].EventCount, det.Runs[len(det.Runs)-1].EventCount, turns)
	}
}
