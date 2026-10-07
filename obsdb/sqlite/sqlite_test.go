package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"

	// The test writes a fake newer migration by hand.
	_ "modernc.org/sqlite"
)

// sqlOpen opens a raw handle on the file, for tests that poke behind
// the backend.
func sqlOpen(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(30000)")
}

func openMem(t *testing.T) obsdb.DB {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// fixture helpers: one scripted run's spans and records, shaped exactly
// as the core emits them (S1.2/S1.3 attribute names).

func spanAttrs(extra map[string]any) map[string]any {
	attrs := map[string]any{
		"gen_ai.operation.name": "invoke_agent",
		"weft.run.id":           "r1",
		"gen_ai.agent.name":     "demo",
		"weft.session.id":       "s1",
		"weft.public_id":        "pub1",
		"weft.turn":             int64(1),
		"weft.manifest.hash":    "sha256:aa",
		"weft.version":          "v0.6.0",
		"gen_ai.provider.name":  "wefttest",
		"gen_ai.request.model":  "script",
	}
	for k, v := range extra {
		attrs[k] = v
	}
	return attrs
}

func invokeAgentSpan(extra map[string]any) obsdb.Span {
	return obsdb.Span{
		TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "0102030405060708",
		Name: "invoke_agent demo", Kind: 1,
		Start: base(0), End: base(5 * time.Second),
		StatusCode: 1, Service: "svc",
		Attrs:    spanAttrs(extra),
		Resource: map[string]any{"service.name": "svc"},
	}
}

func rec(runID, kind, eventType string, pos int64, body string, extra map[string]any) obsdb.Record {
	attrs := map[string]any{
		"weft.record": kind,
		"weft.run.id": runID,
	}
	if eventType != "" {
		attrs["weft.event.type"] = eventType
	}
	switch kind {
	case "event":
		attrs["weft.event.pos"] = pos
	case "delta":
		attrs["weft.delta.pos"] = pos
	case "messages":
		attrs["weft.messages.index"] = pos
		attrs["weft.messages.count"] = int64(1)
		if pos == 0 {
			attrs["weft.messages.input"] = true
		}
	}
	if eventType == "run_start" {
		attrs["gen_ai.agent.name"] = "demo"
		attrs["weft.session.id"] = "s1"
		attrs["weft.public_id"] = "pub1"
		attrs["weft.turn"] = int64(1)
		attrs["weft.manifest.hash"] = "sha256:aa"
		attrs["weft.version"] = "v0.6.0"
	}
	for k, v := range extra {
		attrs[k] = v
	}
	return obsdb.Record{
		Time: base(time.Duration(pos) * time.Second), EventName: "weft." + kind,
		TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "0102030405060708",
		Severity: 9, Body: body, Service: "svc", Attrs: attrs,
		Resource: map[string]any{"service.name": "svc"},
	}
}

// The fixture clock is anchored once per process at init to the real
// wall clock (the f74eee1 obsdbtest pattern): these tests read through
// DeriveStatus with the real now, so a frozen epoch expires the moment
// wall time passes lastSeen+InterruptedAfter — the heartbeat, session,
// and status-filter running assertions would flip to interrupted. One
// package-level anchor keeps every fixture time on a single
// deterministic timeline; UTC strips the monotonic reading and matches
// what the read paths return, so round-tripped times compare equal to
// the fixture.
var testBase = time.Now().UTC()

func base(d time.Duration) time.Time {
	return testBase.Add(d)
}

func scriptedRun() []obsdb.Record {
	return []obsdb.Record{
		rec("r1", "event", "run_start", 0, `{"type":"run_start","id":"r1","model":{"provider":"wefttest","name":"script"},"agent":"demo"}`, nil),
		rec("r1", "event", "step_start", 1, `{"type":"step_start","run_id":"r1","index":0}`, map[string]any{"weft.step.index": int64(0)}),
		rec("r1", "delta", "text_delta", 0, `{"type":"text_delta","run_id":"r1","text":"hi"}`, nil),
		rec("r1", "messages", "", 0, `[{"role":"user","content":[{"type":"text","text":"go"}]}]`, map[string]any{"weft.step.index": int64(0)}),
		rec("r1", "event", "step_finish", 2, `{"type":"step_finish","run_id":"r1","index":0,"reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`, map[string]any{"weft.step.index": int64(0)}),
		rec("r1", "event", "run_finish", 3, `{"type":"run_finish","run_id":"r1","usage":{"input_tokens":10,"output_tokens":5},"steps":1}`, nil),
	}
}

// The round trip: a scripted run plus its span write, then read back —
// status, usage, counts, identity, transcript, events, spans.
func TestWriteThenRead(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	if err := db.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{invokeAgentSpan(map[string]any{
		"gen_ai.usage.input_tokens":  int64(10),
		"gen_ai.usage.output_tokens": int64(5),
		"weft.run.steps":             int64(1),
		"weft.run.stop_reason":       "end_turn",
		"tenant":                     "acme",
	})}, Records: scriptedRun()}); err != nil {
		t.Fatal(err)
	}
	det, err := db.Run(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	r := det.RunRow
	if r.Status != obsdb.StatusSucceeded {
		t.Errorf("status = %q, want succeeded", r.Status)
	}
	if r.Agent != "demo" || r.SessionID != "s1" || r.PublicID != "pub1" || r.Turn != 1 {
		t.Errorf("identity = %+v", r)
	}
	if r.Provider != "wefttest" || r.Model != "script" {
		t.Errorf("provider/model = %q/%q", r.Provider, r.Model)
	}
	if r.ManifestHash != "sha256:aa" || r.WeftVersion != "v0.6.0" || r.Service != "svc" {
		t.Errorf("hash/version/service = %q/%q/%q", r.ManifestHash, r.WeftVersion, r.Service)
	}
	if r.Meta["tenant"] != "acme" {
		t.Errorf("meta = %v", r.Meta)
	}
	if r.Usage.InputTokens != 10 || r.Usage.OutputTokens != 5 {
		t.Errorf("usage = %+v", r.Usage)
	}
	if r.Steps != 1 || r.StopReason != "end_turn" {
		t.Errorf("steps/stop = %d/%q", r.Steps, r.StopReason)
	}
	if r.EventCount != 4 || r.MessageCount != 1 || r.DeltaCount != 1 {
		t.Errorf("counts event/message/delta = %d/%d/%d, want 4/1/1", r.EventCount, r.MessageCount, r.DeltaCount)
	}
	if r.TraceID != "0102030405060708090a0b0c0d0e0f10" {
		t.Errorf("trace = %q", r.TraceID)
	}
	if r.Started != base(0) || r.LastSeen != base(5*time.Second) {
		t.Errorf("window = %v..%v", r.Started, r.LastSeen)
	}
	if r.Finished == nil || !r.Finished.Equal(base(5*time.Second)) {
		t.Errorf("finished = %v, want the span end (max of span and record)", r.Finished)
	}
	// Events: the four durable ones, positioned; the delta absent.
	page, err := db.Events(ctx, "r1", -1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 4 {
		t.Fatalf("events = %d, want 4 (deltas excluded)", len(page.Events))
	}
	if page.Events[0].Pos != 0 || page.Events[3].Pos != 3 {
		t.Errorf("positions = %d..%d", page.Events[0].Pos, page.Events[3].Pos)
	}
	if !page.Done {
		t.Error("page.Done = false on a terminal run with everything returned")
	}
	if len(page.Gaps) != 0 {
		t.Errorf("gaps = %v, want none", page.Gaps)
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(page.Events[0].Event, &head); err != nil || head.Type != "run_start" {
		t.Errorf("event body = %s", page.Events[0].Event)
	}
	// Transcript: the messages body, verbatim, in index order.
	tr, err := db.Transcript(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr) != 1 || string(tr[0]) != `[{"role":"user","content":[{"type":"text","text":"go"}]}]` {
		t.Errorf("transcript = %s", tr)
	}
	spans, err := db.RunSpans(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 || spans[0].Name != "invoke_agent demo" {
		t.Errorf("spans = %+v", spans)
	}
	// Trace finds it too, and non-weft spans are stored beside it.
	if err := db.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{{
		TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "aabbccdd00000000",
		Name: "GET /x", Kind: 2, Start: base(time.Second), End: base(2 * time.Second),
		Attrs: map[string]any{"http.request.method": "GET"},
	}}}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Trace(ctx, "0102030405060708090a0b0c0d0e0f10")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("trace spans = %d, want 2 (weft and non-weft)", len(got))
	}
}

// Idempotence (I4): the same batch twice changes nothing — no double
// counts, no duplicate rows.
func TestWriteIdempotent(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	batch := obsdb.Batch{Records: scriptedRun()}
	for i := 0; i < 2; i++ {
		if err := db.Write(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	det, err := db.Run(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if det.EventCount != 4 || det.MessageCount != 1 {
		t.Errorf("counts after rewrite = %d/%d, want 4/1 (retries don't inflate)", det.EventCount, det.MessageCount)
	}
	page, _ := db.Events(ctx, "r1", -1, 100)
	if len(page.Events) != 4 {
		t.Errorf("events after rewrite = %d, want 4", len(page.Events))
	}
}

// The delta rule: counted, never stored (Q4). KeepDeltas turns storage
// on, keyed by the delta's own counter.
func TestDeltaRule(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{
		rec("r1", "event", "run_start", 0, `{"type":"run_start","id":"r1"}`, nil),
		rec("r1", "delta", "text_delta", 0, `{"type":"text_delta","run_id":"r1","text":"a"}`, nil),
		rec("r1", "delta", "text_delta", 1, `{"type":"text_delta","run_id":"r1","text":"b"}`, nil),
		rec("r1", "event", "run_finish", 1, `{"type":"run_finish","run_id":"r1","usage":{"input_tokens":1,"output_tokens":1},"steps":1}`, nil),
	}}); err != nil {
		t.Fatal(err)
	}
	page, err := db.Events(ctx, "r1", -1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || len(page.Gaps) != 0 {
		t.Fatalf("events = %d gaps = %v: deltas must leave no trace and no hole", len(page.Events), page.Gaps)
	}
	det, _ := db.Run(ctx, "r1")
	if det.DeltaCount != 2 {
		t.Errorf("delta count = %d, want 2 (counted, not kept)", det.DeltaCount)
	}

	// KeepDeltas: the rows exist, on their own counter.
	path := filepath.Join(t.TempDir(), "deltas.db")
	keep, err := sqlite.Open(path, sqlite.KeepDeltas())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = keep.Close() }()
	if err := keep.Write(ctx, obsdb.Batch{Records: []obsdb.Record{
		rec("r1", "delta", "text_delta", 0, `{"type":"text_delta","run_id":"r1","text":"a"}`, nil),
		rec("r1", "delta", "text_delta", 1, `{"type":"text_delta","run_id":"r1","text":"b"}`, nil),
	}}); err != nil {
		t.Fatal(err)
	}
	kd, err := keep.Run(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if kd.DeltaCount != 2 {
		t.Errorf("KeepDeltas: delta count = %d, want 2", kd.DeltaCount)
	}
	// The rows are readable behind the scenes, keyed by their own
	// counter — the durable sequence is untouched either way.
	raw, err := sqlOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var n int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM records WHERE run_id='r1' AND kind='delta'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("KeepDeltas stored %d delta rows, want 2", n)
	}
}

// The heartbeat rule: never a row, only last-seen moves.
func TestHeartbeatRule(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{
		rec("r1", "event", "run_start", 0, `{"type":"run_start","id":"r1"}`, nil),
	}}); err != nil {
		t.Fatal(err)
	}
	before, _ := db.Run(ctx, "r1")
	hb := rec("r1", "heartbeat", "", 0, "", nil)
	hb.Time = time.Now().UTC() // the wall clock: "running" must not depend on how long the binary has run
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{hb}}); err != nil {
		t.Fatal(err)
	}
	after, _ := db.Run(ctx, "r1")
	if !after.LastSeen.Equal(hb.Time) {
		t.Errorf("last seen = %v, want moved by the heartbeat", after.LastSeen)
	}
	if after.EventCount != before.EventCount {
		t.Errorf("event count moved by a heartbeat: %d → %d", before.EventCount, after.EventCount)
	}
	if after.Status != obsdb.StatusRunning {
		t.Errorf("status = %q, want running (the heartbeat is fresh evidence)", after.Status)
	}
	page, _ := db.Events(ctx, "r1", -1, 100)
	if len(page.Events) != 1 {
		t.Errorf("events = %d, want 1 (heartbeats are never rows)", len(page.Events))
	}
}

// Reordering: run_finish before run_start in separate batches. The run
// gets a provisional start, corrected when run_start lands; the
// terminal flags survive.
func TestWriteReordered(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	fin := rec("r1", "event", "run_finish", 3, `{"type":"run_finish","run_id":"r1","usage":{"input_tokens":7,"output_tokens":3},"steps":2}`, nil)
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{fin}}); err != nil {
		t.Fatal(err)
	}
	early, err := db.Run(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if early.Status != obsdb.StatusSucceeded || early.Usage.OutputTokens != 3 {
		t.Fatalf("reordered finish: %+v", early.RunRow)
	}
	if !early.Started.Equal(base(3 * time.Second)) {
		t.Errorf("provisional start = %v, want the record's time", early.Started)
	}
	start := rec("r1", "event", "run_start", 0, `{"type":"run_start","id":"r1","agent":"demo"}`, nil)
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{start}}); err != nil {
		t.Fatal(err)
	}
	late, _ := db.Run(ctx, "r1")
	if !late.Started.Equal(base(0)) {
		t.Errorf("corrected start = %v, want run_start's time", late.Started)
	}
	if late.Agent != "demo" {
		t.Errorf("identity not filled from the late run_start: %q", late.Agent)
	}
}

// Gaps: durable positions missing below the high-water mark are
// reported — a lost batch, never a delta.
func TestEventGaps(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	var recs []obsdb.Record
	for _, p := range []int64{0, 1, 2, 5, 6} { // 3 and 4 lost
		et := "step_start"
		body := `{"type":"step_start","run_id":"r1","index":0}`
		if p == 0 {
			et = "run_start"
			body = `{"type":"run_start","id":"r1"}`
		}
		recs = append(recs, rec("r1", "event", et, p, body, nil))
	}
	if err := db.Write(ctx, obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
	page, err := db.Events(ctx, "r1", -1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Gaps) != 2 || page.Gaps[0] != 3 || page.Gaps[1] != 4 {
		t.Errorf("gaps = %v, want [3 4]", page.Gaps)
	}
}

// The failed path: the invoke_agent span ended in error — failed, with
// the span's status message, outranking anything else.
func TestFailedRunFromSpan(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	s := invokeAgentSpan(map[string]any{"gen_ai.usage.input_tokens": int64(4)})
	s.StatusCode = 2
	s.StatusMessage = "provider down"
	if err := db.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{s}}); err != nil {
		t.Fatal(err)
	}
	det, err := db.Run(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if det.Status != obsdb.StatusFailed || det.Err != "provider down" {
		t.Errorf("failed run = %q err=%q", det.Status, det.Err)
	}
	// Events on a failed, complete run: Done even without run_finish.
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{
		rec("r1", "event", "run_start", 0, `{"type":"run_start","id":"r1"}`, nil),
	}}); err != nil {
		t.Fatal(err)
	}
	page, err := db.Events(ctx, "r1", -1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !page.Done {
		t.Error("Done = false on a failed run with everything returned")
	}
}

// Sessions and public-id resolution (S3.4): a GROUP BY over top-level
// non-playground runs.
func TestSessions(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	// Session s1: two turns, one still running; an experiment excluded.
	writeTurn := func(runID string, turn int, finish bool) {
		extra := map[string]any{"weft.turn": int64(turn)}
		recs := []obsdb.Record{rec(runID, "event", "run_start", 0, `{"type":"run_start","id":"`+runID+`"}`, extra)}
		if finish {
			recs = append(recs, rec(runID, "event", "run_finish", 1,
				`{"type":"run_finish","run_id":"`+runID+`","usage":{"input_tokens":10,"output_tokens":2},"steps":1}`, extra))
		} else {
			recs[0].Time = time.Now().UTC() // running on the wall clock, however long the binary has run
		}
		if err := db.Write(ctx, obsdb.Batch{Records: recs}); err != nil {
			t.Fatal(err)
		}
	}
	writeTurn("s1-t1", 1, true)
	writeTurn("s1-t2", 2, false)
	writeTurn("s1-t3", 3, true) // a playground experiment on the same session
	pg := rec("s1-t3", "event", "run_start", 0, `{"type":"run_start","id":"s1-t3"}`,
		map[string]any{"weft.turn": int64(3), "weft.playground": true, "weft.session.id": "s1"})
	pg.Time = base(30 * time.Second)
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{pg}}); err != nil {
		t.Fatal(err)
	}

	page, err := db.Sessions(ctx, obsdb.SessionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Sessions) != 1 {
		t.Fatalf("sessions = %d/%d, want 1", page.Total, len(page.Sessions))
	}
	s := page.Sessions[0]
	if s.ID != "s1" || s.PublicID != "pub1" || s.Agent != "demo" {
		t.Errorf("session row = %+v", s)
	}
	if s.Turns != 2 {
		t.Errorf("turns = %d, want 2 (non-playground turns)", s.Turns)
	}
	if s.Usage.InputTokens != 10 || s.Usage.OutputTokens != 2 {
		t.Errorf("usage = %+v, want the finished turn's", s.Usage)
	}
	if s.Status != obsdb.StatusRunning {
		t.Errorf("status = %q, want the newest non-playground turn's (running)", s.Status)
	}
	// Detail: the turns in order, experiments excluded.
	det, err := db.Session(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(det.Runs) != 2 || det.Runs[0].ID != "s1-t1" || det.Runs[1].ID != "s1-t2" {
		t.Errorf("detail runs = %+v", det.Runs)
	}
	// Public-id resolution.
	sid, err := db.ResolvePublicID(ctx, "pub1")
	if err != nil || sid != "s1" {
		t.Errorf("resolve pub1 = %q, %v", sid, err)
	}
	if _, err := db.ResolvePublicID(ctx, "nope"); !errors.Is(err, obsdb.ErrNotFound) {
		t.Errorf("unknown public id err = %v, want ErrNotFound", err)
	}
}

// Children: RunDetail carries the runs whose parent is this run.
func TestChildren(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	kid := rec("r2", "event", "run_start", 0, `{"type":"run_start","id":"r2"}`,
		map[string]any{"weft.parent.run.id": "r1", "weft.parent.call.id": "call_1"})
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{
		rec("r1", "event", "run_start", 0, `{"type":"run_start","id":"r1"}`, nil), kid,
	}}); err != nil {
		t.Fatal(err)
	}
	det, err := db.Run(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(det.Children) != 1 || det.Children[0].ID != "r2" ||
		det.Children[0].ParentCallID != "call_1" {
		t.Errorf("children = %+v", det.Children)
	}
	// The default Runs query is top-level only; "*" lists everything.
	page, _ := db.Runs(ctx, obsdb.RunQuery{})
	if page.Total != 1 {
		t.Errorf("top-level runs = %d, want 1", page.Total)
	}
	all, _ := db.Runs(ctx, obsdb.RunQuery{ParentRunID: "*"})
	if all.Total != 2 {
		t.Errorf("all runs = %d, want 2", all.Total)
	}
	kids, _ := db.Runs(ctx, obsdb.RunQuery{ParentRunID: "r1"})
	if kids.Total != 1 {
		t.Errorf("children of r1 = %d, want 1", kids.Total)
	}
}

// A hub subscriber sees a record before Write returns (the S7 step-4
// gate; setup A's live lane).
func TestHubSeesRecordBeforeWriteReturns(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	hb := db.(interface{ Hub() obsdb.Hub }).Hub()
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	frames, err := hb.Subscribe(subCtx, obsdb.Selector{RunID: "r1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan obsdb.Frame, 1)
	go func() {
		for f := range frames {
			seen <- f
		}
	}()
	written := make(chan error, 1)
	go func() {
		written <- db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{
			rec("r1", "event", "run_start", 0, `{"type":"run_start","id":"r1"}`, nil),
		}})
	}()
	select {
	case f := <-seen:
		if f.Kind != obsdb.FrameRecord || f.Record == nil || f.Seq == 0 {
			t.Fatalf("frame = %+v", f)
		}
		// The record arrived; now the write itself must still be
		// in flight or just done — the point is it did not wait for a
		// later flush.
		if err := <-written; err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no frame before Write returned")
	}
	// The run frame follows within the same Write call.
	select {
	case f := <-seen:
		if f.Kind != obsdb.FrameRun || f.Run == nil || f.Run.ID != "r1" {
			t.Fatalf("run frame = %+v", f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no run frame before Write returned")
	}
}

// ErrNotFound shapes.
func TestNotFound(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	if _, err := db.Run(ctx, "nope"); !errors.Is(err, obsdb.ErrNotFound) {
		t.Errorf("Run: %v", err)
	}
	if _, err := db.Events(ctx, "nope", -1, 10); !errors.Is(err, obsdb.ErrNotFound) {
		t.Errorf("Events: %v", err)
	}
	if _, err := db.Transcript(ctx, "nope"); !errors.Is(err, obsdb.ErrNotFound) {
		t.Errorf("Transcript: %v", err)
	}
	if _, err := db.RunSpans(ctx, "nope"); !errors.Is(err, obsdb.ErrNotFound) {
		t.Errorf("RunSpans: %v", err)
	}
	if _, err := db.Session(ctx, "nope"); !errors.Is(err, obsdb.ErrNotFound) {
		t.Errorf("Session: %v", err)
	}
	// Trace of an unknown trace is empty, not an error (polyglot).
	if spans, err := db.Trace(ctx, "00"); err != nil || spans != nil {
		t.Errorf("Trace unknown = %v, %v", spans, err)
	}
}

// A file database survives Close and reopens (the Studio read path
// opens another process's file).
func TestFileReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "obs.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.Write(ctx, obsdb.Batch{Records: scriptedRun()}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Close() }()
	det, err := again.Run(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if det.Status != obsdb.StatusSucceeded {
		t.Errorf("reopened status = %q", det.Status)
	}
}

// A file migrated by a newer binary refuses to open here.
func TestNewerSchemaRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "newer.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Hand-apply a migration ahead of this binary's highest.
	raw, err := sqlOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO obsdb_migrations (version, applied_at) VALUES (9999, '2030-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if _, err := sqlite.Open(path); !errors.Is(err, sqlite.ErrNewerSchema) {
		t.Errorf("Open on newer schema = %v, want ErrNewerSchema", err)
	}
}

// Meta subset filtering (the Tags replacement).
func TestRunsMetaFilter(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{
		rec("r1", "event", "run_start", 0, `{"type":"run_start","id":"r1"}`, map[string]any{"tenant": "acme", "pr": "77"}),
		rec("r2", "event", "run_start", 0, `{"type":"run_start","id":"r2"}`, map[string]any{"tenant": "globex"}),
	}}); err != nil {
		t.Fatal(err)
	}
	page, err := db.Runs(ctx, obsdb.RunQuery{Meta: map[string]string{"tenant": "acme"}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Runs[0].ID != "r1" {
		t.Fatalf("meta filter = %d rows", page.Total)
	}
	if page.Runs[0].Meta["pr"] != "77" {
		t.Errorf("meta = %v", page.Runs[0].Meta)
	}
}

// Status filter agreement: what Runs{Status} returns matches what the
// rows' derived Status says.
func TestRunsStatusFilter(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	write := func(id string, finish, fail bool) {
		recs := []obsdb.Record{rec(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, nil)}
		var spans []obsdb.Span
		if finish {
			recs = append(recs, rec(id, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+id+`","steps":1}`, nil))
		}
		if fail {
			s := invokeAgentSpan(nil)
			s.Attrs["weft.run.id"] = id
			s.StatusCode = 2
			s.StatusMessage = "boom"
			spans = append(spans, s)
		}
		if err := db.Write(ctx, obsdb.Batch{Spans: spans, Records: recs}); err != nil {
			t.Fatal(err)
		}
	}
	write("ok", true, false)
	write("bad", false, true)
	write("live", false, false)
	// "live" reads running on the wall clock, not the process-start
	// fixture clock (which expires 30 s into the binary's run).
	live := rec("live", "heartbeat", "", 0, "", nil)
	live.Time = time.Now().UTC()
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{live}}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []obsdb.Status{obsdb.StatusSucceeded, obsdb.StatusFailed, obsdb.StatusRunning} {
		page, err := db.Runs(ctx, obsdb.RunQuery{Status: want})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 1 {
			t.Errorf("status %q: %d runs, want 1", want, page.Total)
			continue
		}
		if page.Runs[0].Status != want {
			t.Errorf("status %q filter returned a %q row", want, page.Runs[0].Status)
		}
	}
}

// Paging: the Before cursor walks pages newest-first without offsets.
func TestRunsPaging(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		id := string(rune('a'+i)) + "1"
		r := rec(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, nil)
		r.Time = base(time.Duration(i) * time.Second)
		if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{r}}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := db.Runs(ctx, obsdb.RunQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Runs) != 2 || page.Total != 3 || page.NextBefore == nil {
		t.Fatalf("page 1 = %d rows, total %d", len(page.Runs), page.Total)
	}
	if !page.Runs[0].Started.After(page.Runs[1].Started) {
		t.Error("page not newest-first")
	}
	next, err := db.Runs(ctx, obsdb.RunQuery{Limit: 2, Before: *page.NextBefore})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Runs) != 1 {
		t.Fatalf("page 2 = %d rows, want 1", len(next.Runs))
	}
}

// Non-weft records land in other_logs (an slog line through the same
// pipeline) — stored, invisible to Runs.
func TestNonWeftRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	line := obsdb.Record{
		Time: base(time.Second), TraceID: "0102030405060708090a0b0c0d0e0f10",
		Severity: 9, EventName: "", Body: "worker up", Service: "svc",
		Attrs: map[string]any{"worker": "payments"},
	}
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{line}}); err != nil {
		t.Fatal(err)
	}
	page, _ := db.Runs(ctx, obsdb.RunQuery{ParentRunID: "*"})
	if page.Total != 0 {
		t.Errorf("a non-weft record created run rows: %d", page.Total)
	}
	spans, err := db.Trace(ctx, "0102030405060708090a0b0c0d0e0f10")
	if err != nil || len(spans) != 0 {
		t.Errorf("trace = %v, %v", spans, err)
	}
	// The row is there (other_logs), reachable through the raw handle.
	raw, err := sqlOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var body string
	if err := raw.QueryRow(`SELECT body FROM other_logs`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "worker up" {
		t.Errorf("other_logs body = %q", body)
	}
}

// The turn attr arrives as a string in the real chain: thread mints
// weft.turn through metadata (strconv.Itoa in turn.go) and the core
// stamps every metadata value as attribute.String (observe.go) — the
// int64 fixtures everywhere else are a spelling the pipeline never
// produces. The row must carry turn 3, not the 0 that made every
// studio run row read "t0" (the programme audit's P1-1).
func TestTurnStringAttrEndToEnd(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	extra := map[string]any{"weft.turn": "3"}
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{
		rec("s1-t3", "event", "run_start", 0, `{"type":"run_start","id":"s1-t3","model":{"provider":"wefttest","name":"script"},"agent":"demo"}`, extra),
		rec("s1-t3", "event", "run_finish", 1, `{"type":"run_finish","run_id":"s1-t3","usage":{"input_tokens":10,"output_tokens":2},"steps":1}`, extra),
	}}); err != nil {
		t.Fatal(err)
	}
	det, err := db.Run(ctx, "s1-t3")
	if err != nil {
		t.Fatal(err)
	}
	if det.Turn != 3 {
		t.Errorf("run row turn = %d, want 3 (the string attr's number)", det.Turn)
	}
	page, err := db.Runs(ctx, obsdb.RunQuery{SessionID: "s1"})
	if err != nil || page.Total != 1 {
		t.Fatalf("runs = %d/%d, %v", page.Total, len(page.Runs), err)
	}
	if page.Runs[0].Turn != 3 {
		t.Errorf("run list turn = %d, want 3", page.Runs[0].Turn)
	}
}

// Session(id) reads the session's own row directly: the old
// implementation scanned the newest 500 sessions, so an older session
// 404'd in the detail while the list still showed it (the audit's
// P2-1).
func TestSessionBeyondNewestPage(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	// 502 sessions; the target is the oldest.
	const target = "s_target"
	write := func(sessionID string, at time.Duration) {
		extra := map[string]any{"weft.session.id": sessionID}
		start := rec(sessionID+"-t1", "event", "run_start", 0,
			`{"type":"run_start","id":"`+sessionID+`-t1","model":{"provider":"wefttest","name":"script"},"agent":"demo"}`, extra)
		start.Time = base(at)
		finish := rec(sessionID+"-t1", "event", "run_finish", 1,
			`{"type":"run_finish","run_id":"`+sessionID+`-t1","usage":{"input_tokens":3,"output_tokens":1},"steps":1}`, extra)
		finish.Time = base(at + time.Second)
		if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{start, finish}}); err != nil {
			t.Fatal(err)
		}
	}
	write(target, 0)
	for i := 0; i < 501; i++ {
		write(fmt.Sprintf("s_new_%03d", i), time.Duration(i+2)*time.Minute)
	}
	list, err := db.Sessions(ctx, obsdb.SessionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 502 {
		t.Fatalf("sessions total = %d, want 502", list.Total)
	}
	det, err := db.Session(ctx, target)
	if err != nil {
		t.Fatalf("Session(%s): %v (a session older than the newest page must still resolve)", target, err)
	}
	if det.ID != target || det.Turns != 1 || det.Status != obsdb.StatusSucceeded {
		t.Errorf("detail = %+v", det.SessionRow)
	}
	if len(det.Runs) != 1 || det.Runs[0].ID != target+"-t1" {
		t.Errorf("runs = %+v", det.Runs)
	}
	if _, err := db.Session(ctx, "nope"); !errors.Is(err, obsdb.ErrNotFound) {
		t.Errorf("unknown session: %v, want ErrNotFound", err)
	}
}

// A NaN or infinite double attribute is legal OTLP (and a legal
// attribute.Float64), but JSON has no spelling for it: the attrs column
// could not encode, and that one value failed the whole batch — every
// other span and record in it lost, and an OTLP exporter retrying a
// 500 forever. The value is stored under its protojson name instead.
func TestNonFiniteAttrDoesNotFailTheBatch(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	sp := invokeAgentSpan(map[string]any{
		"score": math.NaN(), "ceiling": math.Inf(1),
		"nested": []any{math.Inf(-1), int64(1)},
	})
	sp.Events = []obsdb.SpanEvent{{Time: base(time.Second), Name: "sample", Attrs: map[string]any{"p": math.NaN()}}}
	recs := scriptedRun()
	recs[1].Attrs["temperature"] = math.NaN()
	other := obsdb.Record{Time: base(0), Body: "line", Attrs: map[string]any{"ratio": math.NaN()}}
	if err := db.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{sp}, Records: append(recs, other)}); err != nil {
		t.Fatalf("Write with non-finite attrs: %v", err)
	}
	det, err := db.Run(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if det.EventCount != 4 || det.Status != obsdb.StatusSucceeded {
		t.Errorf("run = %d events, %q; want the whole batch stored", det.EventCount, det.Status)
	}
	spans, err := db.RunSpans(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	a := spans[0].Attrs
	if a["score"] != "NaN" || a["ceiling"] != "Infinity" {
		t.Errorf("non-finite attrs = %v / %v, want NaN / Infinity", a["score"], a["ceiling"])
	}
	if n, _ := a["nested"].([]any); len(n) != 2 || n[0] != "-Infinity" || n[1] != int64(1) {
		t.Errorf("nested = %v, want [-Infinity 1]", a["nested"])
	}
	if a["weft.run.id"] != "r1" || spans[0].Events[0].Attrs["p"] != "NaN" {
		t.Errorf("the rest of the span did not survive: %v, events %v", a, spans[0].Events)
	}
}

// OTLP spells "unknown" as a zero timestamp, and time.Time's zero is
// outside UnixNano's range: stored raw it read back as a date in 1754.
// A zero time is stored as 0 and reads back zero; a record with only an
// observed time (a log bridge that never set Time) takes that.
func TestZeroTimestamps(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	sp := obsdb.Span{TraceID: "aa", SpanID: "bb", Name: "untimed"}
	observed := rec("z1", "event", "run_start", 0, `{"type":"run_start","id":"z1"}`, nil)
	observedAt := time.Now().UTC() // the wall clock: the status below must read running whenever this runs
	observed.Time, observed.Observed = time.Time{}, observedAt
	untimed := rec("z1", "event", "step_start", 1, `{"type":"step_start","run_id":"z1","index":0}`, nil)
	untimed.Time = time.Time{}
	if err := db.Write(ctx, obsdb.Batch{Spans: []obsdb.Span{sp}, Records: []obsdb.Record{observed, untimed}}); err != nil {
		t.Fatal(err)
	}
	spans, err := db.Trace(ctx, "aa")
	if err != nil || len(spans) != 1 {
		t.Fatalf("trace = %v, %v", spans, err)
	}
	if !spans[0].Start.IsZero() || !spans[0].End.IsZero() {
		t.Errorf("untimed span = %v → %v, want zero times", spans[0].Start, spans[0].End)
	}
	det, err := db.Run(ctx, "z1")
	if err != nil {
		t.Fatal(err)
	}
	if !det.Started.Equal(observedAt) || !det.LastSeen.Equal(observedAt) {
		t.Errorf("run started/last-seen = %v / %v, want the observed time %v", det.Started, det.LastSeen, observedAt)
	}
	if det.Status != obsdb.StatusRunning {
		t.Errorf("status = %q, want running (last seen just now, by the observed time)", det.Status)
	}
	page, err := db.Events(ctx, "z1", -1, 10)
	if err != nil || len(page.Events) != 2 {
		t.Fatalf("events = %+v, %v", page, err)
	}
	if !page.Events[0].Time.Equal(observedAt) {
		t.Errorf("event 0 time = %v, want the observed time", page.Events[0].Time)
	}
	if !page.Events[1].Time.IsZero() {
		t.Errorf("event 1 time = %v, want zero (no time at all)", page.Events[1].Time)
	}
}

// Done means the run is terminal AND every stored event was returned.
// The page's events and the run's terminal flags are separate reads: a
// batch committing between them (the last events plus run_finish) used
// to produce Done with those events missing, so a reader following the
// run to Done stopped short of its tail.
func TestEventsDoneNeverHidesStoredEvents(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	for iter := 0; iter < 60; iter++ {
		db, err := sqlite.Open(filepath.Join(dir, fmt.Sprintf("race-%d.db", iter)))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{
			rec("r1", "event", "run_start", 0, `{"type":"run_start","id":"r1"}`, nil),
		}}); err != nil {
			t.Fatal(err)
		}
		wrote := make(chan error, 1)
		go func() {
			wrote <- db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{
				rec("r1", "event", "step_start", 1, `{"type":"step_start","run_id":"r1","index":0}`, nil),
				rec("r1", "event", "run_finish", 2, `{"type":"run_finish","run_id":"r1","steps":1}`, nil),
			}})
		}()
		for {
			page, err := db.Events(ctx, "r1", -1, 100)
			if err != nil {
				t.Fatal(err)
			}
			if page.Done {
				if len(page.Events) != 3 {
					t.Fatalf("iteration %d: Done with %d of 3 events returned", iter, len(page.Events))
				}
				break
			}
		}
		if err := <-wrote; err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
	}
}

// A session's detail carries all of its turns: the read used to stop at
// 500 ordered by turn, silently dropping the newest ones.
func TestSessionDetailCarriesEveryTurn(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	const turns = 503
	var recs []obsdb.Record
	for i := 1; i <= turns; i++ {
		id := fmt.Sprintf("s_long-t%d", i)
		r := rec(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`,
			map[string]any{"weft.session.id": "s_long", "weft.turn": int64(i)})
		r.Time = base(time.Duration(i) * time.Second)
		recs = append(recs, r)
	}
	if err := db.Write(ctx, obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
	det, err := db.Session(ctx, "s_long")
	if err != nil {
		t.Fatal(err)
	}
	if det.Turns != turns || len(det.Runs) != turns {
		t.Fatalf("session = %d turns, %d runs listed; want %d of each", det.Turns, len(det.Runs), turns)
	}
	if last := det.Runs[len(det.Runs)-1]; last.Turn != turns {
		t.Errorf("last listed turn = %d, want the newest (%d)", last.Turn, turns)
	}
}

// The gap detector's fast path (the run's event count against the
// highest position) must never hide a hole — a negative position from a
// foreign sender makes the count match while a real position is
// missing.
func TestEventGapsWithStrayPositions(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	var recs []obsdb.Record
	for _, p := range []int64{-1, 0, 2} { // 1 lost; three rows, highest position 2
		recs = append(recs, rec("r1", "event", "step_start", p, `{"type":"step_start","run_id":"r1","index":0}`, nil))
	}
	if err := db.Write(ctx, obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
	page, err := db.Events(ctx, "r1", -1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Gaps) != 1 || page.Gaps[0] != 1 {
		t.Errorf("gaps = %v, want [1]", page.Gaps)
	}
}

// Positions are the sender's numbers: one stray high weft.event.pos
// must not make every page read of the run build a list of each
// position below it. Gaps lists the first obsdb.MaxGaps.
func TestEventGapsAreBounded(t *testing.T) {
	db := openMem(t)
	ctx := context.Background()
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{
		rec("r1", "event", "run_start", 0, `{"type":"run_start","id":"r1"}`, nil),
		rec("r1", "event", "step_start", 5_000_000, `{"type":"step_start","run_id":"r1","index":0}`, nil),
	}}); err != nil {
		t.Fatal(err)
	}
	page, err := db.Events(ctx, "r1", -1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Gaps) != obsdb.MaxGaps || page.Gaps[0] != 1 || page.Gaps[len(page.Gaps)-1] != obsdb.MaxGaps {
		t.Errorf("gaps = %d listed, want the first %d (1..%d)", len(page.Gaps), obsdb.MaxGaps, obsdb.MaxGaps)
	}
}

// Writers and readers share the handle (the local sink writes on the
// runs' goroutines while Studio reads): on both handle shapes
// concurrent use neither errors nor loses a row, and Close afterwards
// turns every further call into ErrClosed.
func TestConcurrentWritersAndReaders(t *testing.T) {
	for name, path := range map[string]string{
		"memory": ":memory:",
		"file":   filepath.Join(t.TempDir(), "conc.db"),
	} {
		t.Run(name, func(t *testing.T) {
			db, err := sqlite.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			const writers, events = 6, 40
			errs := make(chan error, writers*2)
			stop := make(chan struct{})
			var readers sync.WaitGroup
			for r := 0; r < 3; r++ {
				readers.Add(1)
				go func() {
					defer readers.Done()
					for {
						select {
						case <-stop:
							return
						default:
						}
						if _, err := db.Runs(ctx, obsdb.RunQuery{}); err != nil {
							errs <- err
							return
						}
						if _, err := db.Sessions(ctx, obsdb.SessionQuery{}); err != nil {
							errs <- err
							return
						}
						if _, err := db.Events(ctx, "w0", -1, 10); err != nil && !errors.Is(err, obsdb.ErrNotFound) {
							errs <- err
							return
						}
					}
				}()
			}
			var wg sync.WaitGroup
			for w := 0; w < writers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					id := fmt.Sprintf("w%d", w)
					for p := int64(0); p < events; p++ {
						et := "step_start"
						if p == 0 {
							et = "run_start"
						}
						if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{
							rec(id, "event", et, p, `{"type":"`+et+`"}`, map[string]any{"weft.session.id": "s_" + id}),
							rec(id, "delta", "text_delta", p, `{"type":"text_delta"}`, nil),
						}}); err != nil {
							errs <- err
							return
						}
					}
				}()
			}
			wg.Wait()
			close(stop)
			readers.Wait()
			close(errs)
			for err := range errs {
				t.Fatalf("concurrent use: %v", err)
			}
			page, err := db.Runs(ctx, obsdb.RunQuery{})
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != writers {
				t.Fatalf("runs = %d, want %d", page.Total, writers)
			}
			for _, r := range page.Runs {
				if r.EventCount != events || r.DeltaCount != events {
					t.Errorf("run %s = %d events, %d deltas; want %d of each", r.ID, r.EventCount, r.DeltaCount, events)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := db.Write(ctx, obsdb.Batch{Records: scriptedRun()}); !errors.Is(err, obsdb.ErrClosed) {
				t.Errorf("Write after Close = %v, want ErrClosed", err)
			}
			if _, err := db.Runs(ctx, obsdb.RunQuery{}); !errors.Is(err, obsdb.ErrClosed) {
				t.Errorf("Runs after Close = %v, want ErrClosed", err)
			}
		})
	}
}

// The path is a file path, not a URI: the characters SQLite's URI form
// gives meaning to ('%', '?', '#') must reach it escaped, or the
// database lands in a different file than the one asked for ("C#/app.db"
// opened a file named "C").
func TestOpenPathWithURICharacters(t *testing.T) {
	for _, name := range []string{"q?x.db", "h#x.db", "p%41.db", "a b.db"} {
		path := filepath.Join(t.TempDir(), name)
		db, err := sqlite.Open(path)
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if err := db.Write(context.Background(), obsdb.Batch{Records: scriptedRun()}); err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%q: the database is not at the path it was opened with: %v", name, err)
		}
	}
}

// A path that begins with two slashes is a valid POSIX path (it names
// the same file as one slash), but "file://x/..." is a URI authority to
// SQLite: the open failed with "invalid uri authority".
func TestOpenPathWithLeadingDoubleSlash(t *testing.T) {
	dir := t.TempDir()
	if !filepath.IsAbs(dir) || filepath.Separator != '/' {
		t.Skip("POSIX paths only")
	}
	path := "/" + filepath.Join(dir, "dbl.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("%q: %v", path, err)
	}
	if err := db.Write(context.Background(), obsdb.Batch{Records: scriptedRun()}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dbl.db")); err != nil {
		t.Errorf("the database is not at %s: %v", path, err)
	}
}

// Migration 0003 (ADR 0028): a fresh file carries the request record's
// run columns at their "not recorded" defaults, records.step stays, and
// the three new record kinds land under their own per-run index with
// their step.
func TestRequestRecordSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "req.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	kindRec := func(kind, posKey string, pos, step int64) obsdb.Record {
		attrs := map[string]any{"weft.record": kind, "weft.run.id": "r1", posKey: pos}
		if step >= 0 {
			attrs["weft.step.index"] = step
		}
		return obsdb.Record{Time: time.Unix(1790845923, pos).UTC(), EventName: "weft." + kind, Body: `{}`, Attrs: attrs}
	}
	batch := obsdb.Batch{Records: append(scriptedRun(),
		kindRec("prompt", "weft.prompt.index", 0, -1),
		kindRec("tools", "weft.tools.index", 0, -1),
		kindRec("request", "weft.request.index", 0, 1),
		kindRec("request", "weft.request.index", 1, 2),
		obsdb.Record{Time: time.Unix(1790845923, 9).UTC(), EventName: "weft.tools", Body: `{}`,
			Attrs: map[string]any{"weft.record": "tools", "weft.run.id": "r1"}}, // no index: Pos -1
	)}
	if err := db.Write(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := sqlOpen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	columns := func(table string) map[string]string {
		rows, err := raw.Query(`SELECT name, type, "notnull", COALESCE(dflt_value, '') FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		out := map[string]string{}
		for rows.Next() {
			var name, typ, dflt string
			var notNull int
			if err := rows.Scan(&name, &typ, &notNull, &dflt); err != nil {
				t.Fatal(err)
			}
			out[name] = fmt.Sprintf("%s notnull=%d default=%s", typ, notNull, dflt)
		}
		return out
	}
	runs, records := columns("runs"), columns("records")
	for col, want := range map[string]string{
		"instructions_hash": "TEXT notnull=1 default=''",
		"catalog_hash":      "TEXT notnull=1 default=''",
		"request_count":     "INTEGER notnull=1 default=0",
	} {
		if runs[col] != want {
			t.Errorf("runs.%s = %q, want %q", col, runs[col], want)
		}
	}
	if want := "INTEGER notnull=1 default=-1"; records["step"] != want {
		t.Errorf("records.step = %q, want %q", records["step"], want)
	}

	var version int
	if err := raw.QueryRow(`SELECT MAX(version) FROM obsdb_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Errorf("obsdb_migrations max = %d, want 3 (0003 request record)", version)
	}
	var instructions, catalog string
	var requests int
	if err := raw.QueryRow(`SELECT instructions_hash, catalog_hash, request_count FROM runs WHERE run_id = 'r1'`).
		Scan(&instructions, &catalog, &requests); err != nil {
		t.Fatal(err)
	}
	// The write path fills them (ADR 0028 §10): scriptedRun's run_start
	// carries no instructions hash (a pre-0028 shape) and request 0 no
	// catalog hash, so only the high-water mark moves — 2 requests, the
	// index-less tools record counting nothing.
	if instructions != "" || catalog != "" || requests != 2 {
		t.Errorf("run columns = %q, %q, %d; want \"\", \"\", 2", instructions, catalog, requests)
	}
	rows, err := raw.Query(`SELECT kind, pos, step FROM records
		WHERE run_id = 'r1' AND kind IN ('request', 'prompt', 'tools') ORDER BY kind, pos`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var kind string
		var pos, step int64
		if err := rows.Scan(&kind, &pos, &step); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s/%d/%d", kind, pos, step))
	}
	if want := "prompt/0/-1 request/0/1 request/1/2 tools/-1/-1 tools/0/-1"; strings.Join(got, " ") != want {
		t.Errorf("records = %v, want %s", got, want)
	}
}

// SQLite reads the input flag from the record's attributes: exact, and
// never marked inferred — a run fed no messages has no input batch.
func TestTranscriptInputStored(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Write(context.Background(), obsdb.Batch{Records: inputRecs()}); err != nil {
		t.Fatal(err)
	}
	for run, want := range map[string][]bool{"c_in": {true, false}, "c_none": {false}} {
		got, err := db.TranscriptBatches(context.Background(), run)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: %d batches, want %d", run, len(got), len(want))
		}
		for i, b := range got {
			if b.Input != want[i] || b.InputDerived {
				t.Errorf("%s batch %d = input %v derived %v, want input %v, not derived", run, i, b.Input, b.InputDerived, want[i])
			}
		}
	}
}

// inputRecs is a run fed one message (c_in) and a run fed none
// (c_none): the transcript input flag's two shapes.
func inputRecs() []obsdb.Record {
	rec := func(run string, pos int64, body string, attrs map[string]any) obsdb.Record {
		a := map[string]any{"weft.record": "messages", "weft.run.id": run, "weft.messages.index": pos, "weft.step.index": int64(0)}
		for k, v := range attrs {
			a[k] = v
		}
		return obsdb.Record{
			Time: time.Unix(0, 1790845923120000000+pos).UTC(), EventName: "weft.messages",
			Severity: 9, Body: body, Service: "conf-svc", Attrs: a,
			Resource: map[string]any{"service.name": "conf-svc"},
		}
	}
	user := `[{"role":"user","content":[{"type":"text","text":"q"}]}]`
	reply := `[{"role":"assistant","content":[{"type":"text","text":"a"}]}]`
	return []obsdb.Record{
		rec("c_in", 0, user, map[string]any{"weft.messages.input": true}),
		rec("c_in", 1, reply, nil),
		rec("c_none", 0, reply, nil),
	}
}
