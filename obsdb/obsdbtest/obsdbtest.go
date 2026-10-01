// Package obsdbtest is the shared conformance table for obsdb.DB
// backends — the executable form of S3.5's promises, the storetest
// pattern. Every backend runs it (sqlite here, clickhouse in its own
// module); Run takes a factory so each subtest gets a fresh database.
package obsdbtest

import (
	"context"

	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/obsdb"
)

// Run executes the conformance table against a backend. open returns a
// fresh DB for each subtest; the table never shares state between them.
func Run(t *testing.T, open func(t *testing.T) obsdb.DB) {
	t.Run("RoundTrip", roundTrip(open))
	t.Run("Idempotence", idempotence(open))
	t.Run("Reordering", reordering(open))
	t.Run("Gaps", gaps(open))
	t.Run("StatusAtBoundaries", status(open))
	t.Run("Sessions", sessions(open))
	t.Run("Children", children(open))
	t.Run("Paging", paging(open))
	t.Run("DeltaRule", deltaRule(open))
	t.Run("HeartbeatRule", heartbeatRule(open))
	t.Run("NonWeft", nonWeft(open))
	t.Run("NotFound", notFound(open))
}

func ctx() context.Context { return context.Background() }

// The fixture clock is anchored once per process at init to the real
// wall clock, so reads that derive status with the real now always see
// the fixture as fresh — the table can never expire the way a frozen
// epoch does. One package-level anchor keeps the table deterministic:
// every backend in a process derives identical times. UTC strips the
// monotonic reading and matches what the read paths return, so
// round-tripped times compare equal to the fixture.
var base = time.Now().UTC()

func at(d time.Duration) time.Time {
	return base.Add(d)
}

func record(runID, kind, eventType string, pos int64, body string, extra map[string]any) obsdb.Record {
	attrs := map[string]any{"weft.record": kind, "weft.run.id": runID}
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
	}
	if eventType == "run_start" {
		attrs["gen_ai.agent.name"] = "conf"
		attrs["weft.session.id"] = "s_conf"
		attrs["weft.public_id"] = "pub_conf"
		attrs["weft.turn"] = int64(1)
		attrs["weft.manifest.hash"] = "sha256:conf"
		attrs["weft.version"] = "v0.6.0"
		attrs["tenant"] = "conftest"
	}
	for k, v := range extra {
		attrs[k] = v
	}
	return obsdb.Record{
		Time: at(time.Duration(pos) * time.Second), EventName: "weft." + kind,
		TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "0102030405060708",
		Severity: 9, Body: body, Service: "conf-svc", Attrs: attrs,
		Resource: map[string]any{"service.name": "conf-svc"},
	}
}

func invokeSpan(runID string, status int, extra map[string]any) obsdb.Span {
	attrs := map[string]any{
		"gen_ai.operation.name":      "invoke_agent",
		"weft.run.id":                runID,
		"gen_ai.agent.name":          "conf",
		"weft.session.id":            "s_conf",
		"weft.public_id":             "pub_conf",
		"weft.turn":                  int64(1),
		"gen_ai.usage.input_tokens":  int64(100),
		"gen_ai.usage.output_tokens": int64(20),
		"weft.run.steps":             int64(1),
	}
	for k, v := range extra {
		attrs[k] = v
	}
	return obsdb.Span{
		TraceID: "0102030405060708090a0b0c0d0e0f10", SpanID: "0a0b0c0d0e0f0102",
		Name: "invoke_agent conf", Kind: 1,
		Start: at(0), End: at(9 * time.Second),
		StatusCode: status, Service: "conf-svc", Attrs: attrs,
		Resource: map[string]any{"service.name": "conf-svc"},
	}
}

func finishedRun(id string) []obsdb.Record {
	return []obsdb.Record{
		record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`","model":{"provider":"wefttest","name":"script"},"agent":"conf"}`, nil),
		record(id, "delta", "text_delta", 0, `{"type":"text_delta","run_id":"`+id+`","text":"chunk"}`, nil),
		record(id, "messages", "", 0, `[{"role":"user","content":[{"type":"text","text":"conform"}]}]`, nil),
		record(id, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+id+`","usage":{"input_tokens":100,"output_tokens":20},"steps":1}`, nil),
	}
}

// roundTrip pins the format's first promise: what Write accepted, Read
// returns — bodies verbatim, transcript in messages-index order, spans
// with their attributes, and the run row assembled from the pieces.
func roundTrip(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		if err := db.Write(ctx(), obsdb.Batch{
			Spans:   []obsdb.Span{invokeSpan("c1", 1, nil)},
			Records: finishedRun("c1"),
		}); err != nil {
			t.Fatal(err)
		}
		det, err := db.Run(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		r := det.RunRow
		if r.ID != "c1" || r.Agent != "conf" || r.SessionID != "s_conf" ||
			r.PublicID != "pub_conf" || r.Turn != 1 {
			t.Errorf("identity = %+v", r)
		}
		if r.Provider != "wefttest" || r.Model != "script" {
			t.Errorf("provider/model = %q/%q (from the run_start body)", r.Provider, r.Model)
		}
		if r.ManifestHash != "sha256:conf" || r.WeftVersion != "v0.6.0" || r.Service != "conf-svc" {
			t.Errorf("hash/version/service = %q/%q/%q", r.ManifestHash, r.WeftVersion, r.Service)
		}
		if r.Meta["tenant"] != "conftest" {
			t.Errorf("meta = %v", r.Meta)
		}
		if r.Status != obsdb.StatusSucceeded {
			t.Errorf("status = %q, want succeeded", r.Status)
		}
		if r.Usage != (weft.Usage{InputTokens: 100, OutputTokens: 20}) {
			t.Errorf("usage = %+v", r.Usage)
		}
		if r.Steps != 1 || r.EventCount != 2 || r.MessageCount != 1 || r.DeltaCount != 1 {
			t.Errorf("steps/counts = %d, %d/%d/%d", r.Steps, r.EventCount, r.MessageCount, r.DeltaCount)
		}
		if r.TraceID != "0102030405060708090a0b0c0d0e0f10" {
			t.Errorf("trace = %q", r.TraceID)
		}
		page, err := db.Events(ctx(), "c1", -1, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Events) != 2 || string(page.Events[0].Event) != `{"type":"run_start","id":"c1","model":{"provider":"wefttest","name":"script"},"agent":"conf"}` {
			t.Fatalf("events = %+v", page.Events)
		}
		if page.Events[0].Time != at(0) || page.Events[1].Pos != 1 {
			t.Errorf("positions/times = %+v", page.Events)
		}
		tr, err := db.Transcript(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		if len(tr) != 1 || string(tr[0]) != `[{"role":"user","content":[{"type":"text","text":"conform"}]}]` {
			t.Fatalf("transcript = %s", tr)
		}
		spans, err := db.RunSpans(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		if len(spans) != 1 || spans[0].Attrs["gen_ai.usage.input_tokens"] != int64(100) {
			t.Fatalf("spans = %+v", spans)
		}
		byTrace, err := db.Trace(ctx(), "0102030405060708090a0b0c0d0e0f10")
		if err != nil || len(byTrace) != 1 {
			t.Fatalf("trace lookup = %v, %v", byTrace, err)
		}
	}
}

// idempotence: the same batch twice is a no-op — the transport-level
// retry (I4) must not double counts or rows.
func idempotence(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		batch := obsdb.Batch{
			Spans:   []obsdb.Span{invokeSpan("c1", 1, nil)},
			Records: finishedRun("c1"),
		}
		for i := 0; i < 2; i++ {
			if err := db.Write(ctx(), batch); err != nil {
				t.Fatal(err)
			}
		}
		det, err := db.Run(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		if det.EventCount != 2 || det.MessageCount != 1 || det.DeltaCount != 1 {
			t.Errorf("counts after a rewrite = %d/%d/%d, want 2/1/1", det.EventCount, det.MessageCount, det.DeltaCount)
		}
		page, _ := db.Events(ctx(), "c1", -1, 100)
		if len(page.Events) != 2 {
			t.Errorf("events after a rewrite = %d, want 2", len(page.Events))
		}
		tr, _ := db.Transcript(ctx(), "c1")
		if len(tr) != 1 {
			t.Errorf("transcript after a rewrite = %d batches, want 1", len(tr))
		}
	}
}

// reordering: a batch that arrives finish-before-start. The terminal
// facts survive; run_start corrects the provisional start when it
// lands.
func reordering(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		recs := finishedRun("c1")
		// Finish first (the tail of the stream).
		if err := db.Write(ctx(), obsdb.Batch{Records: recs[3:]}); err != nil {
			t.Fatal(err)
		}
		early, err := db.Run(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		if early.Status != obsdb.StatusSucceeded {
			t.Errorf("reordered finish: status = %q, want succeeded", early.Status)
		}
		// Then the head, out of order.
		if err := db.Write(ctx(), obsdb.Batch{Records: recs[:3]}); err != nil {
			t.Fatal(err)
		}
		late, err := db.Run(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		if !late.Started.Equal(at(0)) {
			t.Errorf("start after reordering = %v, want run_start's time", late.Started)
		}
		if late.EventCount != 2 || late.MessageCount != 1 {
			t.Errorf("counts after reordering = %d/%d", late.EventCount, late.MessageCount)
		}
		page, _ := db.Events(ctx(), "c1", -1, 100)
		if len(page.Events) != 2 || len(page.Gaps) != 0 {
			t.Errorf("events/gaps after reordering = %d/%v", len(page.Events), page.Gaps)
		}
	}
}

// gaps: durable positions missing below the high-water mark are
// reported — a lost batch, never a delta.
func gaps(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		var recs []obsdb.Record
		for _, p := range []int64{0, 1, 4} { // 2 and 3 lost
			et, body := "step_start", `{"type":"step_start","run_id":"c1","index":1}`
			if p == 0 {
				et, body = "run_start", `{"type":"run_start","id":"c1"}`
			}
			recs = append(recs, record("c1", "event", et, p, body, nil))
		}
		if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
			t.Fatal(err)
		}
		// A delta above the hole must not close it.
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record("c1", "delta", "text_delta", 9, `{"type":"text_delta","run_id":"c1","text":"x"}`, nil),
		}}); err != nil {
			t.Fatal(err)
		}
		page, err := db.Events(ctx(), "c1", -1, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Gaps) != 2 || page.Gaps[0] != 2 || page.Gaps[1] != 3 {
			t.Errorf("gaps = %v, want [2 3]", page.Gaps)
		}
	}
}

// status at every boundary, the crash included: a fresh open run reads
// running; the error span reads failed; run_finish reads succeeded;
// a crashed run (no terminal anywhere, stale last-seen) reads running
// while fresh and interrupted once older than InterruptedAfter —
// DeriveStatus is the exported rule every backend reads through, so
// the boundary is pinned here in time.
func status(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record("live", "event", "run_start", 0, `{"type":"run_start","id":"live"}`, nil),
		}}); err != nil {
			t.Fatal(err)
		}
		live, err := db.Run(ctx(), "live")
		if err != nil {
			t.Fatal(err)
		}
		if live.Status != obsdb.StatusRunning {
			t.Errorf("fresh open run = %q, want running", live.Status)
		}
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{invokeSpan("bad", 2, nil)}}); err != nil {
			t.Fatal(err)
		}
		bad, _ := db.Run(ctx(), "bad")
		if bad.Status != obsdb.StatusFailed {
			t.Errorf("error span = %q, want failed", bad.Status)
		}
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record("done", "event", "run_start", 0, `{"type":"run_start","id":"done"}`, nil),
			record("done", "event", "run_finish", 1, `{"type":"run_finish","run_id":"done","steps":1}`, nil),
		}}); err != nil {
			t.Fatal(err)
		}
		done, _ := db.Run(ctx(), "done")
		if done.Status != obsdb.StatusSucceeded {
			t.Errorf("run_finish = %q, want succeeded", done.Status)
		}
		// The crash: the same "live" row, no terminal, last-seen aged
		// past InterruptedAfter — the four-row table's last row, read
		// through the exported derivation every backend must use.
		lastSeen := live.LastSeen
		if got := obsdb.DeriveStatus(false, false, lastSeen, lastSeen.Add(obsdb.InterruptedAfter+time.Second)); got != obsdb.StatusInterrupted {
			t.Errorf("DeriveStatus(crash) = %q, want interrupted", got)
		}
		if got := obsdb.DeriveStatus(false, false, lastSeen, lastSeen.Add(obsdb.InterruptedAfter-time.Second)); got != obsdb.StatusRunning {
			t.Errorf("DeriveStatus(fresh crash window) = %q, want running", got)
		}
		// Status filters agree with the derived rows.
		for _, st := range []obsdb.Status{obsdb.StatusRunning, obsdb.StatusSucceeded, obsdb.StatusFailed} {
			page, err := db.Runs(ctx(), obsdb.RunQuery{Status: st})
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != 1 || page.Runs[0].Status != st {
				t.Errorf("status filter %q = %d rows (first %q)", st, page.Total, page.Runs[0].Status)
			}
		}
	}
}

// sessions, public-id resolution, and the turn list.
func sessions(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		write := func(id string, turn int64, finish bool, extra map[string]any) {
			ex := map[string]any{"weft.turn": turn}
			for k, v := range extra {
				ex[k] = v
			}
			recs := []obsdb.Record{record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, ex)}
			if finish {
				recs = append(recs, record(id, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+id+`","usage":{"input_tokens":10,"output_tokens":2},"steps":1}`, ex))
			}
			if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
				t.Fatal(err)
			}
		}
		write("s_conf-t1", 1, true, nil)
		write("s_conf-t2", 2, true, nil)
		write("s2-t1", 1, true, map[string]any{"weft.session.id": "s2", "weft.public_id": "pub2"})
		// An experiment on s1: excluded from the session's turns.
		write("s_conf-x", 3, true, map[string]any{"weft.playground": true})
		page, err := db.Sessions(ctx(), obsdb.SessionQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 2 {
			t.Fatalf("sessions = %d, want 2", page.Total)
		}
		var s1 obsdb.SessionRow
		for _, s := range page.Sessions {
			if s.ID == "s_conf" {
				s1 = s
			}
		}
		if s1.ID != "s_conf" || s1.PublicID != "pub_conf" || s1.Agent != "conf" || s1.Turns != 2 {
			t.Errorf("s_conf = %+v", s1)
		}
		if s1.Usage.InputTokens != 20 || s1.Usage.OutputTokens != 4 {
			t.Errorf("s_conf usage = %+v, want summed over turns", s1.Usage)
		}
		if s1.Status != obsdb.StatusSucceeded {
			t.Errorf("s_conf status = %q, want the newest turn's", s1.Status)
		}
		det, err := db.Session(ctx(), "s_conf")
		if err != nil {
			t.Fatal(err)
		}
		if len(det.Runs) != 2 || det.Runs[0].ID != "s_conf-t1" || det.Runs[1].ID != "s_conf-t2" {
			t.Errorf("s_conf turns = %+v (ordered by turn, experiments excluded)", det.Runs)
		}
		if _, err := db.Session(ctx(), "nope"); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("unknown session err = %v", err)
		}
		sid, err := db.ResolvePublicID(ctx(), "pub_conf")
		if err != nil || sid != "s_conf" {
			t.Errorf("resolve pub_conf = %q, %v", sid, err)
		}
		if _, err := db.ResolvePublicID(ctx(), "nope"); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("unknown public id err = %v", err)
		}
		// The agent filter.
		byAgent, err := db.Sessions(ctx(), obsdb.SessionQuery{Agent: "conf"})
		if err != nil || byAgent.Total != 2 {
			t.Errorf("agent filter = %d, %v", byAgent.Total, err)
		}
	}
}

// children: RunDetail carries the subagent runs; the default Runs list
// is top-level only.
func children(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record("parent", "event", "run_start", 0, `{"type":"run_start","id":"parent"}`, nil),
			record("kid", "event", "run_start", 0, `{"type":"run_start","id":"kid"}`,
				map[string]any{"weft.parent.run.id": "parent", "weft.parent.call.id": "call_1", "weft.session.id": ""}),
		}}); err != nil {
			t.Fatal(err)
		}
		det, err := db.Run(ctx(), "parent")
		if err != nil {
			t.Fatal(err)
		}
		if len(det.Children) != 1 || det.Children[0].ID != "kid" || det.Children[0].ParentCallID != "call_1" {
			t.Fatalf("children = %+v", det.Children)
		}
		page, _ := db.Runs(ctx(), obsdb.RunQuery{})
		if page.Total != 1 || page.Runs[0].ID != "parent" {
			t.Errorf("top-level = %d (children hidden)", page.Total)
		}
		all, _ := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*"})
		if all.Total != 2 {
			t.Errorf("all = %d, want 2", all.Total)
		}
	}
}

// paging cursors: Before walks newest-first without offsets, and the
// event page's after/NextAfter pair walks positions.
func paging(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		for i := 0; i < 4; i++ {
			id := "run-" + string(rune('a'+i))
			r := record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, nil)
			r.Time = at(time.Duration(i) * time.Second)
			if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{r}}); err != nil {
				t.Fatal(err)
			}
		}
		page, err := db.Runs(ctx(), obsdb.RunQuery{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Runs) != 2 || page.Total != 4 || page.NextBefore == nil {
			t.Fatalf("page 1 = %d rows, total %d", len(page.Runs), page.Total)
		}
		if !page.Runs[0].Started.After(page.Runs[1].Started) {
			t.Error("page not newest-first")
		}
		seen := map[string]bool{page.Runs[0].ID: true, page.Runs[1].ID: true}
		before := *page.NextBefore
		for {
			next, err := db.Runs(ctx(), obsdb.RunQuery{Limit: 2, Before: before})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range next.Runs {
				if seen[r.ID] {
					t.Fatalf("run %s on two pages", r.ID)
				}
				seen[r.ID] = true
			}
			if next.NextBefore == nil {
				break
			}
			before = *next.NextBefore
		}
		if len(seen) != 4 {
			t.Errorf("cursor walk saw %d of 4 runs", len(seen))
		}
		// Event paging.
		var recs []obsdb.Record
		for p := int64(1); p < 5; p++ {
			recs = append(recs, record("run-a", "event", "step_start", p, `{"type":"step_start","run_id":"run-a","index":1}`, nil))
		}
		recs = append(recs, record("run-a", "event", "run_finish", 5, `{"type":"run_finish","run_id":"run-a","steps":1}`, nil))
		if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
			t.Fatal(err)
		}
		first, err := db.Events(ctx(), "run-a", -1, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.Events) != 2 || first.NextAfter == nil || *first.NextAfter != 1 {
			t.Fatalf("event page 1 = %+v", first)
		}
		rest, err := db.Events(ctx(), "run-a", *first.NextAfter, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(rest.Events) != 4 || !rest.Done {
			t.Fatalf("event page 2 = %d events, done %v", len(rest.Events), rest.Done)
		}
	}
}

// the delta rule: counted, never stored; their absence never opens a
// gap in the durable sequence (D3).
func deltaRule(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record("c1", "event", "run_start", 0, `{"type":"run_start","id":"c1"}`, nil),
			record("c1", "delta", "text_delta", 0, `{"type":"text_delta","run_id":"c1","text":"a"}`, nil),
			record("c1", "delta", "reasoning_delta", 1, `{"type":"reasoning_delta","run_id":"c1","text":"b"}`, nil),
			record("c1", "event", "run_finish", 1, `{"type":"run_finish","run_id":"c1","steps":1}`, nil),
		}}); err != nil {
			t.Fatal(err)
		}
		det, err := db.Run(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		if det.DeltaCount != 2 {
			t.Errorf("delta count = %d, want 2 (counted)", det.DeltaCount)
		}
		page, _ := db.Events(ctx(), "c1", -1, 100)
		if len(page.Events) != 2 || len(page.Gaps) != 0 {
			t.Errorf("events/gaps = %d/%v: two deltas left no hole", len(page.Events), page.Gaps)
		}
	}
}

// the heartbeat rule: no rows, last-seen moves.
func heartbeatRule(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record("c1", "event", "run_start", 0, `{"type":"run_start","id":"c1"}`, nil),
		}}); err != nil {
			t.Fatal(err)
		}
		before, err := db.Run(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		hb := record("c1", "heartbeat", "", 0, "", nil)
		hb.Time = at(45 * time.Second)
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{hb}}); err != nil {
			t.Fatal(err)
		}
		after, err := db.Run(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		if !after.LastSeen.After(before.LastSeen) {
			t.Errorf("last-seen did not move: %v → %v", before.LastSeen, after.LastSeen)
		}
		if after.EventCount != before.EventCount || after.MessageCount != before.MessageCount {
			t.Errorf("counts moved by a heartbeat: %d/%d → %d/%d",
				before.EventCount, before.MessageCount, after.EventCount, after.MessageCount)
		}
		if after.Status != obsdb.StatusRunning {
			t.Errorf("a heartbeated run = %q, want running", after.Status)
		}
	}
}

// non-weft spans and records are stored and returned by Trace — the
// polyglot promise.
func nonWeft(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		span := obsdb.Span{
			TraceID: "99" + "0102030405060708090a0b0c0d0e0f", SpanID: "99aa0b0c0d0e0f0102",
			Name: "chat stock-model", Kind: 3, Start: at(0), End: at(time.Second),
			StatusCode: 1, Service: "other-svc",
			Attrs:    map[string]any{"gen_ai.operation.name": "chat", "gen_ai.request.model": "stock-model"},
			Resource: map[string]any{"service.name": "other-svc"},
			Events:   []obsdb.SpanEvent{{Time: at(500 * time.Millisecond), Name: "exception", Attrs: map[string]any{"exception.type": "timeout"}}},
		}
		line := obsdb.Record{
			Time: at(time.Second), TraceID: "99" + "0102030405060708090a0b0c0d0e0f",
			Severity: 9, Body: "unrelated log", Service: "other-svc",
			Attrs: map[string]any{"app": "python"},
		}
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{span}, Records: []obsdb.Record{line}}); err != nil {
			t.Fatal(err)
		}
		got, err := db.Trace(ctx(), "99"+"0102030405060708090a0b0c0d0e0f")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("trace = %d spans, want the non-weft one stored and returned", len(got))
		}
		if got[0].Name != "chat stock-model" || len(got[0].Events) != 1 || got[0].Events[0].Name != "exception" {
			t.Errorf("span = %+v", got[0])
		}
		// And no run rows appeared for either.
		page, _ := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "*"})
		if page.Total != 0 {
			t.Errorf("non-weft data created %d run rows", page.Total)
		}
	}
}

// notFound: the sentinel on every id-taking read.
func notFound(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		if _, err := db.Run(ctx(), "nope"); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("Run: %v", err)
		}
		if _, err := db.Events(ctx(), "nope", -1, 10); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("Events: %v", err)
		}
		if _, err := db.Transcript(ctx(), "nope"); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("Transcript: %v", err)
		}
		if _, err := db.RunSpans(ctx(), "nope"); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("RunSpans: %v", err)
		}
	}
}
