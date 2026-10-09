// Package obsdbtest is the shared conformance table for obsdb.DB
// backends — the executable form of S3.5's promises, the storetest
// pattern. Every backend runs it (sqlite here, clickhouse in its own
// module); Run takes a factory so each subtest gets a fresh database.
package obsdbtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

// Run executes the conformance table against a backend. open returns a
// fresh DB for each subtest; the table never shares state between them.
func Run(t *testing.T, open func(t *testing.T) obsdb.DB) {
	t.Run("RoundTrip", roundTrip(open))
	t.Run("Idempotence", idempotence(open))
	t.Run("Reordering", reordering(open))
	t.Run("Gaps", gaps(open))
	t.Run("EventContentAttrs", eventContentAttrs(open))
	t.Run("StatusAtBoundaries", status(open))
	t.Run("Sessions", sessions(open))
	t.Run("Children", children(open))
	t.Run("Paging", paging(open))
	t.Run("DeltaRule", deltaRule(open))
	t.Run("HeartbeatRule", heartbeatRule(open))
	t.Run("NonWeft", nonWeft(open))
	t.Run("NotFound", notFound(open))
	t.Run("Experiments", experiments(open))
	t.Run("PipelineSpellings", pipelineSpellings(open))
	t.Run("PagingTies", pagingTies(open))
	t.Run("SessionPaging", sessionPaging(open))
	t.Run("TranscriptRebuilt", transcriptRebuilt(open))
	t.Run("TranscriptSteps", transcriptSteps(open))
	t.Run("Compactions", compactions(open))
	t.Run("MessagesAsOf", messagesAsOf(open))
	t.Run("RequestRecords", requestRecords(open))
	t.Run("RequestsManySteps", requestsManySteps(open))
	t.Run("RequestRunRows", requestRunRows(open))
	t.Run("NonFiniteAttrs", nonFiniteAttrs(open))
	t.Run("ZeroTimes", zeroTimes(open))
	t.Run("OutOfOrder", outOfOrder(open))
	t.Run("HeartbeatOnly", heartbeatOnly(open))
	t.Run("Strings", stringsRoundTrip(open))
	t.Run("MetaFilter", metaFilter(open))
	t.Run("SessionUncapped", sessionUncapped(open))
	t.Run("ReadErrorIsNotNotFound", readErrorIsNotNotFound(open))
	t.Run("FilterCombinations", filterCombinations(open))
	t.Run("PagingBigTie", pagingBigTie(open))
	t.Run("ManyDaysOneBatch", manyDaysOneBatch(open))
	t.Run("OtherLogs", otherLogs(open))
	t.Run("CloseRace", closeRace(open))
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
		if r.Usage != (core.Usage{InputTokens: 100, OutputTokens: 20}) {
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
		// The (trace, span) half of the promise, read back: a backend
		// that stored the retried span twice must not return it twice.
		spans, err := db.RunSpans(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		if len(spans) != 1 {
			t.Errorf("RunSpans after a rewrite = %d spans, want 1 per span id", len(spans))
		}
		byTrace, err := db.Trace(ctx(), "0102030405060708090a0b0c0d0e0f10")
		if err != nil {
			t.Fatal(err)
		}
		if len(byTrace) != 1 {
			t.Errorf("Trace after a rewrite = %d spans, want 1 per span id", len(byTrace))
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

// eventContentAttrs: an event carries the content attributes its
// destination's chain stamped (ADR 0028 §11) — weft.content from a
// content-off chain, weft.content.truncated_bytes from a cap — as a
// number or a numeric string; an event with neither reads zero.
func eventContentAttrs(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		recs := []obsdb.Record{
			record("ca1", "event", "run_start", 0, `{"type":"run_start","id":"ca1"}`, map[string]any{"weft.content": "stripped"}),
			record("ca1", "event", "tool_finish", 1, `{"type":"tool_finish","run_id":"ca1"}`, map[string]any{"weft.content.truncated_bytes": int64(12595)}),
			record("ca1", "event", "tool_finish", 2, `{"type":"tool_finish","run_id":"ca1"}`, map[string]any{"weft.content.truncated_bytes": "7"}),
			record("ca1", "event", "step_finish", 3, `{"type":"step_finish","run_id":"ca1"}`, nil),
		}
		if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
			t.Fatal(err)
		}
		page, err := db.Events(ctx(), "ca1", -1, 100)
		if err != nil {
			t.Fatal(err)
		}
		type got struct {
			content string
			cut     int64
		}
		want := []got{{"stripped", 0}, {"", 12595}, {"", 7}, {"", 0}}
		if len(page.Events) != len(want) {
			t.Fatalf("events = %d, want %d", len(page.Events), len(want))
		}
		for i, ev := range page.Events {
			if g := (got{ev.Content, ev.TruncatedBytes}); g != want[i] {
				t.Errorf("event %d content attrs = %+v, want %+v", i, g, want[i])
			}
		}
	}
}

// status at every boundary, the crash included: a fresh open run reads
// running; the error span reads failed; run_finish reads succeeded;
// a crashed run (no terminal anywhere, stale last-seen) reads running
// while fresh and interrupted once older than InterruptedAfter —
// DeriveStatus is the exported rule every backend reads through, so
// the boundary is pinned here in time, and the stored rows on either
// side of it read through each backend. The non-terminal fixtures sit
// on the wall clock, not the fixture clock: anchored at process start,
// "fresh" expired once the test binary had run for 30 s (a slow CI
// machine, -count=N).
func status(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		now := time.Now().UTC()
		liveStart := record("live", "event", "run_start", 0, `{"type":"run_start","id":"live"}`, nil)
		liveStart.Time = now
		// Either side of the boundary, by a margin only a stalled
		// machine could eat (the read follows the write within ms).
		fresh := record("fresh", "event", "run_start", 0, `{"type":"run_start","id":"fresh"}`, nil)
		fresh.Time = now.Add(-obsdb.InterruptedAfter + 10*time.Second)
		stale := record("stale", "event", "run_start", 0, `{"type":"run_start","id":"stale"}`, nil)
		stale.Time = now.Add(-obsdb.InterruptedAfter - time.Second)
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{liveStart, fresh, stale}}); err != nil {
			t.Fatal(err)
		}
		for id, want := range map[string]obsdb.Status{"fresh": obsdb.StatusRunning, "stale": obsdb.StatusInterrupted} {
			r, err := db.Run(ctx(), id)
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != want {
				t.Errorf("%s (last seen %v ago) = %q, want %q", id, time.Since(r.LastSeen).Round(time.Second), r.Status, want)
			}
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
		for st, want := range map[obsdb.Status]string{
			obsdb.StatusRunning: "live,fresh", obsdb.StatusSucceeded: "done",
			obsdb.StatusFailed: "bad", obsdb.StatusInterrupted: "stale",
		} {
			page, err := db.Runs(ctx(), obsdb.RunQuery{Status: st})
			if err != nil {
				t.Fatal(err)
			}
			got := idsOf(page.Runs)
			sort.Strings(got)
			w := strings.Split(want, ",")
			sort.Strings(w)
			if strings.Join(got, ",") != strings.Join(w, ",") || page.Total != len(w) {
				t.Errorf("status filter %q = %v (total %d), want %v", st, got, page.Total, w)
				continue
			}
			for _, r := range page.Runs {
				if r.Status != st {
					t.Errorf("status filter %q returned a %q row", st, r.Status)
				}
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
		// On the wall clock: a run_start older than InterruptedAfter reads
		// interrupted until the heartbeat — now — refreshes it.
		now := time.Now().UTC()
		start := record("c1", "event", "run_start", 0, `{"type":"run_start","id":"c1"}`, nil)
		start.Time = now.Add(-obsdb.InterruptedAfter - 10*time.Second)
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{start}}); err != nil {
			t.Fatal(err)
		}
		before, err := db.Run(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		if before.Status != obsdb.StatusInterrupted {
			t.Errorf("a run last seen %v ago = %q, want interrupted", obsdb.InterruptedAfter+10*time.Second, before.Status)
		}
		hb := record("c1", "heartbeat", "", 0, "", nil)
		hb.Time = now
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

// experiments pins the saved-experiment rows (WEFT-PLAYGROUND §10.4,
// PQ4) every backend must serve: an upsert keeps the id and refreshes
// the definition, the list reads newest-first, a missing id is
// ErrNotFound, and RunQuery.ExperimentID selects the runs the
// experiment's commands labelled.
func experiments(open func(t *testing.T) obsdb.DB) func(t *testing.T) {
	return func(t *testing.T) {
		ctx := context.Background()
		db := open(t)
		defer func() { _ = db.Close() }()

		e := obsdb.Experiment{
			ID: "exp_1", Name: "tracking-link prompt", Agent: "acme-support",
			Variants: []obsdb.ExperimentVariant{
				{Key: "A", Overrides: json.RawMessage(`{}`)},
				{Key: "B", Overrides: json.RawMessage(`{"instructions":"new"}`)},
			},
			Inputs: []obsdb.ExperimentInput{
				{Key: "1", SourceRunID: "s_1-t1"},
				{Key: "2", Text: "angry user"},
			},
		}
		if err := db.SaveExperiment(ctx, e); err != nil {
			t.Fatal(err)
		}
		e.Name = "tracking-link prompt v2"
		if err := db.SaveExperiment(ctx, e); err != nil {
			t.Fatal(err)
		}

		got, err := db.Experiment(ctx, "exp_1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "tracking-link prompt v2" || got.Agent != "acme-support" {
			t.Errorf("experiment = %+v, want the refreshed definition", got)
		}
		if len(got.Variants) != 2 || got.Variants[1].Key != "B" || string(got.Variants[1].Overrides) != `{"instructions":"new"}` {
			t.Errorf("variants = %+v, want the wire shapes verbatim", got.Variants)
		}
		if len(got.Inputs) != 2 || got.Inputs[1].Text != "angry user" {
			t.Errorf("inputs = %+v", got.Inputs)
		}

		// An older experiment (saved before exp_1's updates) sorts
		// behind it.
		older := obsdb.Experiment{ID: "exp_0", Name: "older"}
		if err := db.SaveExperiment(ctx, older); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveExperiment(ctx, older); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveExperiment(ctx, e); err != nil { // exp_1's refresh is newest
			t.Fatal(err)
		}
		list, err := db.Experiments(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 2 || list[0].ID != "exp_1" {
			t.Errorf("list = %+v, want the newest update first", list)
		}

		if _, err := db.Experiment(ctx, "exp_nope"); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("missing experiment err = %v, want ErrNotFound", err)
		}

		// The runs of one experiment: labelled with its id, excluded
		// from the others'.
		writeRun(ctx, t, db, "r_exp1", "exp_1")
		writeRun(ctx, t, db, "r_exp2", "exp_2")
		page, err := db.Runs(ctx, obsdb.RunQuery{ExperimentID: "exp_1", ParentRunID: "*"})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Runs) != 1 || page.Runs[0].ID != "r_exp1" {
			t.Errorf("experiment runs = %+v, want exactly r_exp1", page.Runs)
		}
		if page.Runs[0].ExperimentID != "exp_1" || !page.Runs[0].Playground {
			t.Errorf("run row = %+v, want the experiment id and the playground flag", page.Runs[0])
		}
	}
}

// writeRun lands one minimal playground run row labelled with the
// experiment, through the ordinary write path (the record helper's
// shapes; the extra attrs carry the experiment identity the row keeps).
func writeRun(ctx context.Context, t *testing.T, db obsdb.DB, runID, experiment string) {
	t.Helper()
	extra := map[string]any{
		"weft.playground":    true,
		"weft.experiment.id": experiment,
	}
	start := record(runID, "event", "run_start", 0, `{"type":"run_start","id":"`+runID+`"}`, extra)
	finish := record(runID, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+runID+`"}`, extra)
	finish.Time = at(2 * time.Second)
	if err := db.Write(ctx, obsdb.Batch{Records: []obsdb.Record{start, finish}}); err != nil {
		t.Fatal(err)
	}
}

// pipelineSpellings pins the attribute spellings the real pipeline
// produces, not the ones a fixture would hand-build: weft.turn and
// weft.playground travel as run metadata, and the core renders every
// metadata value as a string attribute — "2" and "true". A backend
// reading only the typed spellings reports turn 0 on every row and
// lists every playground run as a session turn.
func pipelineSpellings(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		write := func(id string, extra map[string]any) {
			if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
				record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, extra),
				record(id, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+id+`","steps":1}`, extra),
			}}); err != nil {
				t.Fatal(err)
			}
		}
		write("s_conf-t2", map[string]any{"weft.turn": "2"})
		write("pg_1", map[string]any{"weft.turn": "3", "weft.playground": "true", "weft.experiment.id": "exp_1"})
		turn, err := db.Run(ctx(), "s_conf-t2")
		if err != nil {
			t.Fatal(err)
		}
		if turn.Turn != 2 || turn.Playground {
			t.Errorf("turn row = turn %d, playground %v; want 2, false", turn.Turn, turn.Playground)
		}
		pg, err := db.Run(ctx(), "pg_1")
		if err != nil {
			t.Fatal(err)
		}
		if !pg.Playground || pg.Turn != 3 || pg.ExperimentID != "exp_1" {
			t.Errorf("playground row = playground %v, turn %d, experiment %q; want true, 3, exp_1",
				pg.Playground, pg.Turn, pg.ExperimentID)
		}
		yes := true
		page, err := db.Runs(ctx(), obsdb.RunQuery{Playground: &yes})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Runs) != 1 || page.Runs[0].ID != "pg_1" {
			t.Errorf("Playground filter = %d runs, want exactly pg_1", len(page.Runs))
		}
		det, err := db.Session(ctx(), "s_conf")
		if err != nil {
			t.Fatal(err)
		}
		if det.Turns != 1 || len(det.Runs) != 1 || det.Runs[0].ID != "s_conf-t2" {
			t.Errorf("session = %d turns, runs %+v; want the one real turn (the playground run excluded)", det.Turns, det.Runs)
		}
	}
}

// pagingTies: runs sharing one Started (a foreign SDK's ms-truncated
// clock, a batch import) must survive a page boundary. The cursor is a
// time, so a page never ends inside a tie — the tied rows ride along —
// and the walk sees every run exactly once.
func pagingTies(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		const n = 5
		var recs []obsdb.Record
		for i := 0; i < n; i++ {
			id := "tie-" + string(rune('a'+i))
			recs = append(recs, record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, nil)) // all at(0)
		}
		older := record("older", "event", "run_start", 0, `{"type":"run_start","id":"older"}`, nil)
		older.Time = at(-time.Minute)
		if err := db.Write(ctx(), obsdb.Batch{Records: append(recs, older)}); err != nil {
			t.Fatal(err)
		}
		seen := map[string]int{}
		q := obsdb.RunQuery{Limit: 2}
		for pages := 0; ; pages++ {
			if pages > n+2 {
				t.Fatal("cursor walk does not terminate")
			}
			page, err := db.Runs(ctx(), q)
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != n+1 {
				t.Errorf("total = %d on page %d, want %d", page.Total, pages, n+1)
			}
			for _, r := range page.Runs {
				seen[r.ID]++
			}
			if page.NextBefore == nil {
				break
			}
			q.Before = *page.NextBefore
		}
		if len(seen) != n+1 {
			t.Errorf("cursor walk saw %d of %d runs (%v): tied Started values lost at a page boundary", len(seen), n+1, seen)
		}
		for id, times := range seen {
			if times != 1 {
				t.Errorf("run %s on %d pages", id, times)
			}
		}
	}
}

// sessionPaging: the Sessions cursor walks newest activity first, sees
// every session exactly once even when several share one LastSeen, and
// Total is the whole match on every page — the cursor never shrinks it.
func sessionPaging(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		const n = 5
		var recs []obsdb.Record
		for i := 0; i < n; i++ {
			id := "sess-" + string(rune('a'+i))
			recs = append(recs, record(id+"-t1", "event", "run_start", 0, `{"type":"run_start","id":"`+id+`-t1"}`,
				map[string]any{"weft.session.id": id, "weft.public_id": "pub_" + id})) // all at(0)
		}
		old := record("sess-old-t1", "event", "run_start", 0, `{"type":"run_start","id":"sess-old-t1"}`,
			map[string]any{"weft.session.id": "sess-old", "weft.public_id": "pub_old"})
		old.Time = at(-time.Minute)
		if err := db.Write(ctx(), obsdb.Batch{Records: append(recs, old)}); err != nil {
			t.Fatal(err)
		}
		seen := map[string]int{}
		q := obsdb.SessionQuery{Limit: 2}
		for pages := 0; ; pages++ {
			if pages > n+2 {
				t.Fatal("cursor walk does not terminate")
			}
			page, err := db.Sessions(ctx(), q)
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != n+1 {
				t.Errorf("total = %d on page %d, want %d (the cursor does not narrow Total)", page.Total, pages, n+1)
			}
			for _, s := range page.Sessions {
				seen[s.ID]++
			}
			if page.NextBefore == nil {
				break
			}
			q.Before = *page.NextBefore
		}
		if len(seen) != n+1 {
			t.Errorf("cursor walk saw %d of %d sessions (%v): tied LastSeen values lost at a page boundary", len(seen), n+1, seen)
		}
		for id, times := range seen {
			if times != 1 {
				t.Errorf("session %s on %d pages", id, times)
			}
		}
	}
}

// transcriptSteps: a messages record's weft.step.index is stored and
// read back by TranscriptBatches (ADR 0028 §8) — the step it joined,
// whatever its neighbours hold — with the input flag and the index; a
// record without the attribute reads -1, never a guess. The resumed
// run's rebuilt tool message joins step 0 before any assistant message
// of the run's own (the partial-resume shape), and the steered batch
// joins the step that just finished.
func transcriptSteps(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		msg := func(role, text string) string {
			return `[{"role":"` + role + `","content":[{"type":"text","text":"` + text + `"}]}]`
		}
		step := func(n int) map[string]any { return map[string]any{"weft.step.index": int64(n)} }
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record("c1", "event", "run_start", 0, `{"type":"run_start","id":"c1"}`, nil),
			// A partial resume (ADR 0028 §8): the input record stops at
			// the last assistant message with tool calls; the rebuilt
			// tool message is the next growth record, step 0, not input.
			record("c1", "messages", "", 0, `[{"role":"user","content":[{"type":"text","text":"q"}]},`+
				`{"role":"assistant","content":[{"type":"tool_call","id":"c_e","name":"echo","args":{}},{"type":"tool_call","id":"c_r","name":"refund","args":{}}]}]`,
				map[string]any{"weft.messages.input": true, "weft.step.index": int64(0)}),
			record("c1", "messages", "", 1, `[{"role":"tool","content":[`+
				`{"type":"tool_result","call_id":"c_e","name":"echo","content":"x","is_error":false},`+
				`{"type":"tool_result","call_id":"c_r","name":"refund","content":"refunded","is_error":false}]}]`, step(0)),
			record("c1", "messages", "", 2, msg("assistant", "a0"), step(0)),
			record("c1", "messages", "", 3, msg("user", "steer"), step(0)),
			record("c1", "messages", "", 4, msg("assistant", "a1"), step(1)),
			record("c1", "messages", "", 5, msg("assistant", "unstamped"), nil),
		}}); err != nil {
			t.Fatal(err)
		}
		got, err := db.TranscriptBatches(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		wantStep := []int{0, 0, 0, 0, 1, -1}
		if len(got) != len(wantStep) {
			t.Fatalf("batches = %d, want %d", len(got), len(wantStep))
		}
		for i, b := range got {
			if b.Index != int64(i) || b.Step != wantStep[i] || b.Input != (i == 0) {
				t.Errorf("batch %d = index %d step %d input %v, want index %d step %d input %v",
					i, b.Index, b.Step, b.Input, i, wantStep[i], i == 0)
			}
		}
		if string(got[4].Messages) != msg("assistant", "a1") {
			t.Errorf("batch 4 body = %s", got[4].Messages)
		}
		tr, err := db.Transcript(ctx(), "c1")
		if err != nil || len(tr) != len(got) {
			t.Fatalf("Transcript = %d bodies, %v; want %d, the batches' bodies", len(tr), err, len(got))
		}
		for i := range tr {
			if string(tr[i]) != string(got[i].Messages) {
				t.Errorf("Transcript body %d = %s, batch body %s", i, tr[i], got[i].Messages)
			}
		}
		for _, b := range got {
			if b.InputDerived && b.Index != 0 {
				t.Errorf("batch %d: an inferred input flag off index 0", b.Index)
			}
		}

		// A run fed no messages: the core writes no input record, so
		// index 0 is step 0's assistant batch — never the input, whether
		// the backend reads the flag or infers it.
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record("c2", "event", "run_start", 0, `{"type":"run_start","id":"c2"}`, nil),
			record("c2", "messages", "", 0, msg("assistant", "unprompted"), step(0)),
			record("c2", "messages", "", 1, msg("assistant", "again"), step(1)),
		}}); err != nil {
			t.Fatal(err)
		}
		empty, err := db.TranscriptBatches(ctx(), "c2")
		if err != nil {
			t.Fatal(err)
		}
		if len(empty) != 2 || empty[0].Input || empty[1].Input || empty[0].Step != 0 || empty[1].Step != 1 {
			t.Errorf("empty-input run batches = %+v, want steps 0 and 1, neither the input", empty)
		}
		if _, err := db.TranscriptBatches(ctx(), "nope"); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("TranscriptBatches of an unknown run = %v, want ErrNotFound", err)
		}
	}
}

// compactions: ADR 0028 §8's two compaction records. A run whose
// PrepareStep trimmed the middle at step 3 holds a compaction view —
// a messages record with weft.messages.reason = compacted, its index
// on the one counter — beside its growth records: the plain transcript
// (Transcript, TranscriptBatches) is the growth records alone, byte for
// byte; the run's messages count excludes the view; Compactions returns
// it with its half-open range, hash, step and body. A run that was the
// first after a session compaction holds thread's marker (kind
// compaction, no messages), returned first, never applied. A view with
// a reason this build does not know fails Compactions loudly.
func compactions(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		msg := func(role, text string) string {
			return `[{"role":"` + role + `","content":[{"type":"text","text":"` + text + `"}]}]`
		}
		step := func(n int) map[string]any { return map[string]any{"weft.step.index": int64(n)} }
		recs := []obsdb.Record{
			record("k1", "event", "run_start", 0, `{"type":"run_start","id":"k1"}`, nil),
			record("k1", "messages", "", 0, msg("user", "u0"), map[string]any{"weft.messages.input": true, "weft.step.index": int64(0)}),
		}
		growth := []string{msg("user", "u0")}
		idx := int64(1)
		for s := 0; s < 3; s++ {
			for _, role := range []string{"assistant", "tool"} {
				body := msg(role, fmt.Sprintf("%s%d", role[:1], s))
				recs = append(recs, record("k1", "messages", "", idx, body, step(s)))
				growth = append(growth, body)
				idx++
			}
		}
		const view = `[{"role":"user","content":[{"type":"text","text":"summary"}]}]`
		recs = append(recs, record("k1", "messages", "", idx, view, map[string]any{
			"weft.step.index": int64(3), "weft.messages.count": int64(1),
			"weft.messages.reason": "compacted", "weft.messages.from_seq": int64(1), "weft.messages.to_seq": int64(5),
			"weft.compaction.scope": "run", "weft.compaction.hash": "h_view",
		}))
		idx++
		recs = append(recs, record("k1", "messages", "", idx, msg("assistant", "a3"), step(3)))
		growth = append(growth, msg("assistant", "a3"))
		// A malformed producer's growth record without its index: -1 on
		// both backends, in neither the transcript nor the count — never
		// the first batch on one backend and dropped on the other.
		stray := record("k1", "messages", "", 0, msg("user", "stray"), step(3))
		delete(stray.Attrs, "weft.messages.index")
		recs = append(recs, stray)
		recs = append(recs, record("k1", "event", "run_finish", 1, `{"type":"run_finish","id":"k1","steps":4}`, nil))
		if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
			t.Fatal(err)
		}
		tr, err := db.Transcript(ctx(), "k1")
		if err != nil {
			t.Fatal(err)
		}
		if len(tr) != len(growth) {
			t.Fatalf("Transcript = %d bodies, want the %d growth records", len(tr), len(growth))
		}
		for i := range tr {
			if string(tr[i]) != growth[i] {
				t.Errorf("Transcript body %d = %s, want %s", i, tr[i], growth[i])
			}
		}
		batches, err := db.TranscriptBatches(ctx(), "k1")
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range batches {
			if b.Index == 7 {
				t.Errorf("TranscriptBatches holds the view (index 7): %s", b.Messages)
			}
		}
		if len(batches) != len(growth) || batches[len(batches)-1].Index != 8 {
			t.Errorf("batches = %d, last index %d; want %d, 8", len(batches), batches[len(batches)-1].Index, len(growth))
		}
		det, err := db.Run(ctx(), "k1")
		if err != nil {
			t.Fatal(err)
		}
		if det.MessageCount != int64(len(growth)) {
			t.Errorf("MessageCount = %d, want %d (the view excluded)", det.MessageCount, len(growth))
		}
		page, err := db.Runs(ctx(), obsdb.RunQuery{})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Runs {
			if r.ID == "k1" && r.MessageCount != int64(len(growth)) {
				t.Errorf("Runs MessageCount = %d, want %d", r.MessageCount, len(growth))
			}
		}
		cs, err := db.Compactions(ctx(), "k1")
		if err != nil {
			t.Fatal(err)
		}
		if len(cs) != 1 {
			t.Fatalf("Compactions = %+v, want the one view", cs)
		}
		c := cs[0]
		if c.Scope != obsdb.CompactionRun || c.Hash != "h_view" || c.Index != 7 || c.Step != 3 ||
			c.FromSeq != 1 || c.ToSeq != 5 || c.Replaced != 4 || c.Entries != 1 || string(c.Messages) != view {
			t.Errorf("view = %+v", c)
		}

		// The session marker: the first run after a thread compaction.
		marker := record("k2", "compaction", "", 0,
			`{"scope":"session","hash":"h_sess","entry":"e1","reason":"threshold","replaced":12,"entries":1,"messages_before":14,"messages_after":3,"tokens_before":8100,"tokens_after":1200}`,
			map[string]any{"weft.compaction.scope": "session", "weft.compaction.hash": "h_sess", "weft.content": "none"})
		marker.EventName = "weft.compaction"
		marker.Time = at(500 * time.Millisecond)
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record("k2", "event", "run_start", 0, `{"type":"run_start","id":"k2"}`, nil),
			marker,
			record("k2", "messages", "", 0, msg("user", "the compacted context"), map[string]any{"weft.messages.input": true, "weft.step.index": int64(0)}),
		}}); err != nil {
			t.Fatal(err)
		}
		// A retried transport's duplicate is still one marker.
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{marker}}); err != nil {
			t.Fatal(err)
		}
		cs, err = db.Compactions(ctx(), "k2")
		if err != nil {
			t.Fatal(err)
		}
		if len(cs) != 1 {
			t.Fatalf("k2 Compactions = %+v, want the one marker", cs)
		}
		// A second compaction filed under the same run (a trim, then a
		// summary, with no run between): both kept, in emission order,
		// after the run's views.
		second := record("k2", "compaction", "", 0,
			`{"scope":"session","hash":"f00d","reason":"manual","replaced":2,"entries":1}`,
			map[string]any{"weft.compaction.scope": "session", "weft.compaction.hash": "f00d000000000000000000000000000000000000000000000000000000000000"})
		second.EventName = "weft.compaction"
		second.Time = at(900 * time.Millisecond)
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{second}}); err != nil {
			t.Fatal(err)
		}
		both, err := db.Compactions(ctx(), "k2")
		if err != nil || len(both) != 2 || both[0].Hash != "h_sess" || both[1].Reason != "manual" {
			t.Errorf("k2 Compactions after a second marker = %+v, %v; want both, in emission order", both, err)
		}
		if m := cs[0]; m.Scope != obsdb.CompactionSession || m.Hash != "h_sess" || m.Index != -1 || m.Step != -1 ||
			m.Reason != "threshold" || m.Replaced != 12 || m.Entries != 1 || m.TokensBefore != 8100 || m.TokensAfter != 1200 ||
			m.Messages != nil {
			t.Errorf("marker = %+v", m)
		}
		if tr, err := db.Transcript(ctx(), "k2"); err != nil || len(tr) != 1 {
			t.Errorf("k2 Transcript = %d bodies, %v; the marker is not transcript", len(tr), err)
		}
		if det, err := db.Run(ctx(), "k2"); err != nil || det.MessageCount != 1 || det.Started != at(0) {
			t.Errorf("k2 run = count %d started %v (%v); want 1 and run_start's time", det.MessageCount, det.Started, err)
		}

		// A run that never compacted: empty, not an error; unknown: ErrNotFound.
		if cs, err := db.Compactions(ctx(), "k2x"); !errors.Is(err, obsdb.ErrNotFound) {
			t.Errorf("Compactions of an unknown run = %v, %v; want ErrNotFound", cs, err)
		}
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record("k3", "event", "run_start", 0, `{"type":"run_start","id":"k3"}`, nil),
			record("k3", "messages", "", 0, msg("user", "plain"), step(0)),
		}}); err != nil {
			t.Fatal(err)
		}
		if cs, err := db.Compactions(ctx(), "k3"); err != nil || len(cs) != 0 {
			t.Errorf("Compactions of a plain run = %+v, %v; want none", cs, err)
		}

		// A view selected by its reason alone — no weft.compaction.scope
		// (a producer that stamped only §8's required pair): both
		// backends find it, and its scope reads run.
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record("k5", "event", "run_start", 0, `{"type":"run_start","id":"k5"}`, nil),
			record("k5", "messages", "", 0, msg("user", "u"), step(0)),
			record("k5", "messages", "", 1, msg("user", "s"), map[string]any{
				"weft.step.index": int64(1), "weft.messages.reason": "compacted",
				"weft.messages.from_seq": int64(0), "weft.messages.to_seq": int64(1)}),
		}}); err != nil {
			t.Fatal(err)
		}
		if cs, err := db.Compactions(ctx(), "k5"); err != nil || len(cs) != 1 || cs[0].Scope != obsdb.CompactionRun || cs[0].Index != 1 || cs[0].ToSeq != 1 {
			t.Errorf("Compactions of a scope-less view = %+v, %v; want the view, scope run", cs, err)
		}

		// A view without its index (a malformed producer): stored apart
		// from the growth records (position -1, never the input's 0),
		// out of the transcript and the count, and reported by
		// Compactions as an error on both backends.
		noIndex := record("k6", "messages", "", 0, msg("user", "s"), map[string]any{
			"weft.step.index": int64(1), "weft.messages.reason": "compacted",
			"weft.messages.from_seq": int64(0), "weft.messages.to_seq": int64(1)})
		delete(noIndex.Attrs, "weft.messages.index")
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record("k6", "event", "run_start", 0, `{"type":"run_start","id":"k6"}`, nil),
			record("k6", "messages", "", 0, msg("user", "u"), map[string]any{"weft.messages.input": true, "weft.step.index": int64(0)}),
			noIndex,
		}}); err != nil {
			t.Fatal(err)
		}
		if tr, err := db.Transcript(ctx(), "k6"); err != nil || len(tr) != 1 || string(tr[0]) != msg("user", "u") {
			t.Errorf("k6 Transcript = %s, %v; want the input record alone", tr, err)
		}
		if det, err := db.Run(ctx(), "k6"); err != nil || det.MessageCount != 1 {
			t.Errorf("k6 MessageCount = %d, %v; want 1", det.MessageCount, err)
		}
		if _, err := db.Compactions(ctx(), "k6"); err == nil || !strings.Contains(err.Error(), "weft.messages.index") {
			t.Errorf("Compactions over an index-less view = %v, want an error naming weft.messages.index", err)
		}

		// A reason this build does not know: readers fail loudly.
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record("k4", "event", "run_start", 0, `{"type":"run_start","id":"k4"}`, nil),
			record("k4", "messages", "", 0, msg("user", "u"), step(0)),
			record("k4", "messages", "", 1, msg("user", "v"), map[string]any{
				"weft.step.index": int64(1), "weft.messages.reason": "rewound", "weft.compaction.scope": "run"}),
		}}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Compactions(ctx(), "k4"); err == nil || !strings.Contains(err.Error(), "rewound") {
			t.Errorf("Compactions over an unknown reason = %v, want an error naming it", err)
		}
		if tr, err := db.Transcript(ctx(), "k4"); err != nil || len(tr) != 1 {
			t.Errorf("k4 Transcript = %d, %v; a view of any reason is never transcript", len(tr), err)
		}
	}
}

// transcriptRebuilt: a resume that rebuilds a partial tool message,
// stored in the shape a core before ADR 0028 §8 wrote — the input as
// fed (partial tool message included) and then the rebuilt message.
// The current core ends the input record at the last assistant message
// with tool calls and records the rebuilt message as step 0's growth,
// so no copy repeats; runs stored in the old shape still read through
// obsdb.DedupTranscript, which takes the later record as the
// authoritative one: the bodies concatenate to the transcript the run
// held — one tool message, not the partial and the rebuilt side by
// side — one body per record still.
func transcriptRebuilt(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	const (
		user      = `{"role":"user","content":[{"type":"text","text":"refund please"}]}`
		assistant = `{"role":"assistant","content":[{"type":"tool_call","id":"c_e","name":"echo","args":{"msg":"x"}},{"type":"tool_call","id":"c_r","name":"refund","args":{}}]}`
		partial   = `{"role":"tool","content":[{"type":"tool_result","call_id":"c_e","name":"echo","content":"x","is_error":false}]}`
		rebuilt   = `{"role":"tool","content":[{"type":"tool_result","call_id":"c_e","name":"echo","content":"x","is_error":false},{"type":"tool_result","call_id":"c_r","name":"refund","content":"refunded","is_error":false}]}`
		final     = `{"role":"assistant","content":[{"type":"text","text":"all set"}]}`
	)
	return func(t *testing.T) {
		db := open(t)
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record("c1", "event", "run_start", 0, `{"type":"run_start","id":"c1"}`, nil),
			record("c1", "messages", "", 0, "["+user+","+assistant+","+partial+"]",
				map[string]any{"weft.messages.input": true, "weft.step.index": int64(0)}),
			record("c1", "messages", "", 1, "["+rebuilt+"]", map[string]any{"weft.step.index": int64(0)}),
			record("c1", "messages", "", 2, "["+final+"]", map[string]any{"weft.step.index": int64(0)}),
		}}); err != nil {
			t.Fatal(err)
		}
		tr, err := db.Transcript(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		if len(tr) != 3 {
			t.Fatalf("transcript = %d bodies, want 3 (one per record)", len(tr))
		}
		var got []string
		for _, body := range tr {
			var batch []json.RawMessage
			if err := json.Unmarshal(body, &batch); err != nil {
				t.Fatalf("body %s: %v", body, err)
			}
			for _, m := range batch {
				got = append(got, string(m))
			}
		}
		want := []string{user, assistant, rebuilt, final}
		if len(got) != len(want) {
			t.Fatalf("transcript = %d messages, want %d (the rebuilt tool message supersedes the partial one): %v",
				len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("message %d = %s, want %s", i, got[i], want[i])
			}
		}
	}
}

// nonFiniteAttrs: NaN and ±Inf are legal OTLP doubles with no JSON
// spelling. One of them must neither fail the batch it rides in nor
// cost the span's other attributes their types: every backend stores
// them under the protobuf JSON mapping's names and keeps the rest exact.
func nonFiniteAttrs(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		span := invokeSpan("c1", 1, map[string]any{
			"score": math.NaN(), "hi": math.Inf(1), "lo": math.Inf(-1),
			"nested": []any{1.5, math.NaN()},
		})
		span.Events = []obsdb.SpanEvent{{Time: at(time.Second), Name: "probe", Attrs: map[string]any{"x": math.NaN(), "n": int64(7)}}}
		span.Resource = map[string]any{"service.name": "conf-svc", "r": math.Inf(1)}
		recs := finishedRun("c1")
		recs[0].Attrs["weird"] = math.NaN()
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{span}, Records: recs}); err != nil {
			t.Fatalf("a non-finite attribute failed the batch: %v", err)
		}
		det, err := db.Run(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		if det.Status != obsdb.StatusSucceeded || det.EventCount != 2 {
			t.Errorf("run = %q, %d events; want the batch's other rows stored", det.Status, det.EventCount)
		}
		spans, err := db.RunSpans(ctx(), "c1")
		if err != nil || len(spans) != 1 {
			t.Fatalf("spans = %v, %v", spans, err)
		}
		a := spans[0].Attrs
		if a["gen_ai.usage.input_tokens"] != int64(100) {
			t.Errorf("input tokens = %#v, want int64(100): a non-finite sibling cost the span its types", a["gen_ai.usage.input_tokens"])
		}
		if a["score"] != "NaN" || a["hi"] != "Infinity" || a["lo"] != "-Infinity" {
			t.Errorf("non-finite values = %#v/%#v/%#v, want NaN/Infinity/-Infinity", a["score"], a["hi"], a["lo"])
		}
		if n, _ := a["nested"].([]any); len(n) != 2 || n[0] != 1.5 || n[1] != "NaN" {
			t.Errorf("nested = %#v", a["nested"])
		}
		if spans[0].Resource["r"] != "Infinity" || spans[0].Resource["service.name"] != "conf-svc" {
			t.Errorf("resource = %#v", spans[0].Resource)
		}
		if ev := spans[0].Events; len(ev) != 1 || ev[0].Attrs["x"] != "NaN" || ev[0].Attrs["n"] != int64(7) {
			t.Errorf("events = %#v", ev)
		}
	}
}

// zeroTimes: OTLP spells "unknown" as a zero timestamp. A record's zero
// Time reads its Observed time (the OTLP rule); a record with no time at
// all reads back as the zero time — never as a 1970 or 1754 instant.
// (Not pinned, because the backends differ: an untimed record or span
// lends a run no start in SQLite, while ClickHouse's views take the
// epoch as its min(Started); and an untimed span sits in ClickHouse's
// 1970 partition, which the TTL drops on insert. Neither shape is one a
// conforming OTel SDK or collector emits.)
func zeroTimes(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		start := record("z1", "event", "run_start", 0, `{"type":"run_start","id":"z1"}`, nil)
		start.Time, start.Observed = time.Time{}, at(3*time.Second)
		finish := record("z1", "event", "run_finish", 1, `{"type":"run_finish","run_id":"z1","steps":1}`, nil)
		finish.Time = at(4 * time.Second)
		// z2: a step with no time at all inside a timed run.
		start2 := record("z2", "event", "run_start", 0, `{"type":"run_start","id":"z2"}`, nil)
		step2 := record("z2", "event", "step_start", 1, `{"type":"step_start","run_id":"z2"}`, nil)
		step2.Time = time.Time{}
		finish2 := record("z2", "event", "run_finish", 2, `{"type":"run_finish","run_id":"z2","steps":1}`, nil)
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{start, finish, start2, step2, finish2}}); err != nil {
			t.Fatal(err)
		}
		page, err := db.Events(ctx(), "z1", -1, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Events) != 2 || !page.Events[0].Time.Equal(at(3*time.Second)) {
			t.Errorf("z1 events = %+v, want run_start at its observed time", page.Events)
		}
		z1, err := db.Run(ctx(), "z1")
		if err != nil {
			t.Fatal(err)
		}
		if !z1.Started.Equal(at(3*time.Second)) || z1.Status != obsdb.StatusSucceeded {
			t.Errorf("z1 = started %v, %q; want the observed time, succeeded", z1.Started, z1.Status)
		}
		page2, err := db.Events(ctx(), "z2", -1, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(page2.Events) != 3 || !page2.Events[1].Time.IsZero() || !page2.Events[2].Time.Equal(at(2*time.Second)) {
			t.Errorf("z2 events = %+v, want the untimed step read back as the zero time", page2.Events)
		}
		z2, err := db.Run(ctx(), "z2")
		if err != nil {
			t.Fatal(err)
		}
		if !z2.LastSeen.Equal(at(2*time.Second)) || z2.Finished == nil || !z2.Finished.Equal(at(2*time.Second)) {
			t.Errorf("z2 = last seen %v, finished %v; want run_finish's time", z2.LastSeen, z2.Finished)
		}
	}
}

// outOfOrder: batches arrive in any order — a span before the records,
// a child before its parent, a batch retried after later ones landed.
// The run rows converge to the same answer whatever the order.
func outOfOrder(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		recs := finishedRun("c1")
		span := invokeSpan("c1", 1, nil)
		kidStart := record("kid", "event", "run_start", 0, `{"type":"run_start","id":"kid"}`,
			map[string]any{"weft.parent.run.id": "c1", "weft.parent.call.id": "call_1", "weft.session.id": ""})
		kidFinish := record("kid", "event", "run_finish", 1, `{"type":"run_finish","run_id":"kid","steps":1}`,
			map[string]any{"weft.session.id": ""})
		for i, b := range []obsdb.Batch{
			{Spans: []obsdb.Span{span}},          // the span first
			{Records: []obsdb.Record{kidFinish}}, // the child's tail, before anything of its parent
			{Records: recs[2:]},                  // parent's tail
			{Records: []obsdb.Record{kidStart}},  // the child's head
			{Records: recs[:2]},                  // parent's head
			{Records: recs[2:]},                  // a retried batch, after later ones landed
			{Spans: []obsdb.Span{span}},          // and the span again
		} {
			if err := db.Write(ctx(), b); err != nil {
				t.Fatalf("batch %d: %v", i, err)
			}
		}
		det, err := db.Run(ctx(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		if det.Status != obsdb.StatusSucceeded || !det.Started.Equal(at(0)) || det.Finished == nil {
			t.Errorf("c1 = %q, started %v, finished %v", det.Status, det.Started, det.Finished)
		}
		if det.EventCount != 2 || det.MessageCount != 1 || det.DeltaCount != 1 || det.Usage.InputTokens != 100 {
			t.Errorf("c1 counts/usage = %d/%d/%d, %+v", det.EventCount, det.MessageCount, det.DeltaCount, det.Usage)
		}
		if len(det.Children) != 1 || det.Children[0].ID != "kid" || det.Children[0].Status != obsdb.StatusSucceeded {
			t.Errorf("children = %+v", det.Children)
		}
		top, err := db.Runs(ctx(), obsdb.RunQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if top.Total != 1 || len(top.Runs) != 1 || top.Runs[0].ID != "c1" {
			t.Errorf("top-level = %d %v, want c1 alone (the child's parentless tail is still the child's)", top.Total, idsOf(top.Runs))
		}
		kids, err := db.Runs(ctx(), obsdb.RunQuery{ParentRunID: "c1"})
		if err != nil || kids.Total != 1 || len(kids.Runs) != 1 || kids.Runs[0].Status != obsdb.StatusSucceeded {
			t.Errorf("children list = %+v, %v", kids, err)
		}
		spans, err := db.RunSpans(ctx(), "c1")
		if err != nil || len(spans) != 1 {
			t.Errorf("spans = %d, %v", len(spans), err)
		}
	}
}

// heartbeatOnly: a run seen through nothing but heartbeats (its durable
// records lost or still in flight) exists, reads running while fresh,
// started at the first beat, and has an empty — not missing — event
// page.
func heartbeatOnly(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		// The wall clock, not the fixture's: "running" must hold however
		// long the process has been up before this subtest.
		now := time.Now().UTC()
		h1 := record("hb", "heartbeat", "", 0, "", nil)
		h1.Time = now.Add(-2 * time.Second)
		h2 := record("hb", "heartbeat", "", 0, "", nil)
		h2.Time = now.Add(-time.Second)
		// One batch, the later beat first: the start is the earliest
		// time seen, whatever the arrival order.
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{h2, h1}}); err != nil {
			t.Fatal(err)
		}
		det, err := db.Run(ctx(), "hb")
		if err != nil {
			t.Fatal(err)
		}
		if det.Status != obsdb.StatusRunning || det.EventCount != 0 || det.MessageCount != 0 {
			t.Errorf("hb = %q, %d/%d", det.Status, det.EventCount, det.MessageCount)
		}
		if !det.Started.Equal(h1.Time) || !det.LastSeen.Equal(h2.Time) {
			t.Errorf("hb = started %v, last seen %v; want the first and the last beat", det.Started, det.LastSeen)
		}
		page, err := db.Events(ctx(), "hb", -1, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Events) != 0 || page.Done || len(page.Gaps) != 0 {
			t.Errorf("hb events = %+v", page)
		}
		tr, err := db.Transcript(ctx(), "hb")
		if err != nil || len(tr) != 0 {
			t.Errorf("hb transcript = %v, %v", tr, err)
		}
		// A run seen only through its invoke_agent span (its records lost
		// or still in flight) started when the span did.
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{invokeSpan("sp", 1, nil)}}); err != nil {
			t.Fatal(err)
		}
		sp, err := db.Run(ctx(), "sp")
		if err != nil {
			t.Fatal(err)
		}
		if !sp.Started.Equal(at(0)) || !sp.LastSeen.Equal(at(9*time.Second)) || sp.Finished == nil {
			t.Errorf("span-only run = started %v, last seen %v, finished %v; want the span's start and end", sp.Started, sp.LastSeen, sp.Finished)
		}
	}
}

// stringsRoundTrip: bodies are stored verbatim — unicode, bytes that are
// not UTF-8, a megabyte — and identity and metadata strings survive.
func stringsRoundTrip(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		id := "run-ünï-世界-🎉"
		uni := `{"type":"run_start","id":"` + id + `","note":"héllo 世界 🎉 \u0000"}`
		invalid := "{\"type\":\"step_start\",\"x\":\"\xff\xfe\"}"
		big := `[{"role":"user","content":[{"type":"text","text":"` + strings.Repeat("abcdefgh", 1<<17) + `"}]}]`
		if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
			record(id, "event", "run_start", 0, uni, map[string]any{"note": "naïve — ✓", "bad": "a\xffb", "gen_ai.agent.name": "agënt"}),
			record(id, "event", "step_start", 1, invalid, nil),
			record(id, "messages", "", 0, big, nil),
		}}); err != nil {
			t.Fatal(err)
		}
		det, err := db.Run(ctx(), id)
		if err != nil {
			t.Fatal(err)
		}
		// Metadata is JSON on both backends: a byte that is not UTF-8
		// reads back as U+FFFD, the same on each.
		if det.Agent != "agënt" || det.Meta["note"] != "naïve — ✓" || det.Meta["bad"] != "a\uFFFDb" {
			t.Errorf("identity/meta = %q / %v", det.Agent, det.Meta)
		}
		page, err := db.Events(ctx(), id, -1, 10)
		if err != nil || len(page.Events) != 2 {
			t.Fatalf("events = %v, %v", page.Events, err)
		}
		if string(page.Events[0].Event) != uni || string(page.Events[1].Event) != invalid {
			t.Errorf("bodies not verbatim: %q / %q", page.Events[0].Event, page.Events[1].Event)
		}
		tr, err := db.Transcript(ctx(), id)
		if err != nil || len(tr) != 1 || string(tr[0]) != big {
			t.Errorf("1 MiB transcript body: %d bodies, %v", len(tr), err)
		}
		list, err := db.Runs(ctx(), obsdb.RunQuery{Agent: "agënt"})
		if err != nil || len(list.Runs) != 1 || list.Runs[0].ID != id {
			t.Errorf("agent filter = %v, %v", list.Runs, err)
		}
	}
}

// metaFilter: RunQuery.Meta is an exact subset match on key and value,
// whatever characters either holds — dots, quotes, backslashes,
// wildcards, unicode.
func metaFilter(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		meta := map[string]any{
			"a.b": "x.y", `q"k`: `v"al`, `back\slash`: `c:\dir`, "pct%_": "50%_", "ünï": "✓", "app.tenant": "acme",
		}
		write := func(id string, extra map[string]any) {
			if err := db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
				record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, extra),
			}}); err != nil {
				t.Fatal(err)
			}
		}
		write("m1", meta)
		write("m2", map[string]any{"a": map[string]any{"b": "x.y"}, "pct%_": "50%x", "app.tenant": "other"})
		for k, v := range meta {
			page, err := db.Runs(ctx(), obsdb.RunQuery{Meta: map[string]string{k: v.(string)}})
			if err != nil {
				t.Fatalf("meta %q: %v", k, err)
			}
			if len(page.Runs) != 1 || page.Runs[0].ID != "m1" {
				t.Errorf("meta %q=%q matched %v, want m1 alone", k, v, idsOf(page.Runs))
			}
		}
		page, err := db.Runs(ctx(), obsdb.RunQuery{Meta: map[string]string{"a.b": "x.y", "app.tenant": "acme"}})
		if err != nil || len(page.Runs) != 1 {
			t.Errorf("two-key subset = %v, %v", idsOf(page.Runs), err)
		}
		none, err := db.Runs(ctx(), obsdb.RunQuery{Meta: map[string]string{"app.tenant": "acme", "pct%_": "50%x"}})
		if err != nil || len(none.Runs) != 0 {
			t.Errorf("a pair from each run = %v, %v; want none", idsOf(none.Runs), err)
		}
		missing, err := db.Runs(ctx(), obsdb.RunQuery{Meta: map[string]string{"absent": ""}})
		if err != nil || len(missing.Runs) != 0 {
			t.Errorf("an absent key matched %v, %v", idsOf(missing.Runs), err)
		}
	}
}

// sessionUncapped: Session returns every turn, the newest included, past
// any list page size.
func sessionUncapped(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		const n = 520
		recs := make([]obsdb.Record, 0, n)
		for i := 1; i <= n; i++ {
			id := fmt.Sprintf("big-t%03d", i)
			r := record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`,
				map[string]any{"weft.session.id": "big", "weft.turn": int64(i)})
			r.Time = at(time.Duration(i) * time.Millisecond)
			recs = append(recs, r)
		}
		if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
			t.Fatal(err)
		}
		det, err := db.Session(ctx(), "big")
		if err != nil {
			t.Fatal(err)
		}
		if det.Turns != n || len(det.Runs) != n || det.Runs[n-1].ID != fmt.Sprintf("big-t%03d", n) {
			t.Errorf("session = %d turns, %d runs; want all %d, newest last", det.Turns, len(det.Runs), n)
		}
	}
}

// readErrorIsNotNotFound: a read that failed is not "no such id" —
// callers branch on ErrNotFound (SaveExperiment's first save, Studio's
// 404), so a cancelled or broken read must surface as itself.
func readErrorIsNotNotFound(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		if err := db.Write(ctx(), obsdb.Batch{Records: finishedRun("c1")}); err != nil {
			t.Fatal(err)
		}
		if err := db.SaveExperiment(ctx(), obsdb.Experiment{ID: "exp"}); err != nil {
			t.Fatal(err)
		}
		dead, cancel := context.WithCancel(ctx())
		cancel()
		check := func(name string, err error) {
			t.Helper()
			if err == nil || errors.Is(err, obsdb.ErrNotFound) {
				t.Errorf("%s on a cancelled context = %v, want the read's own error", name, err)
			}
		}
		_, err := db.Run(dead, "c1")
		check("Run", err)
		_, err = db.Events(dead, "c1", -1, 10)
		check("Events", err)
		_, err = db.Transcript(dead, "c1")
		check("Transcript", err)
		_, err = db.RunSpans(dead, "c1")
		check("RunSpans", err)
		_, err = db.Session(dead, "s_conf")
		check("Session", err)
		_, err = db.ResolvePublicID(dead, "pub_conf")
		check("ResolvePublicID", err)
		_, err = db.Experiment(dead, "exp")
		check("Experiment", err)
	}
}

// filterCombinations: every RunQuery filter, alone and combined, agrees
// with the rows it returns — the list and Total both.
func filterCombinations(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		write := func(id string, finish bool, extra map[string]any) {
			recs := []obsdb.Record{record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`, extra)}
			if !finish {
				recs[0].Time = time.Now().UTC() // running, on the wall clock
			}
			if finish {
				recs = append(recs, record(id, "event", "run_finish", 1, `{"type":"run_finish","run_id":"`+id+`","steps":1}`, nil))
			}
			if err := db.Write(ctx(), obsdb.Batch{Records: recs}); err != nil {
				t.Fatal(err)
			}
		}
		write("f-top-done", true, nil)
		write("f-top-live", false, map[string]any{"gen_ai.agent.name": "other"})
		write("f-pg", true, map[string]any{"weft.playground": "true", "weft.experiment.id": "e1"})
		write("f-kid", true, map[string]any{"weft.parent.run.id": "f-top-done", "weft.session.id": ""})
		if err := db.Write(ctx(), obsdb.Batch{Spans: []obsdb.Span{invokeSpan("f-bad", 2, map[string]any{"weft.session.id": "s_other"})}}); err != nil {
			t.Fatal(err)
		}
		yes, no := true, false
		for _, c := range []struct {
			name string
			q    obsdb.RunQuery
			want []string
		}{
			{"default", obsdb.RunQuery{}, []string{"f-bad", "f-pg", "f-top-done", "f-top-live"}},
			{"all", obsdb.RunQuery{ParentRunID: "*"}, []string{"f-bad", "f-kid", "f-pg", "f-top-done", "f-top-live"}},
			{"parent", obsdb.RunQuery{ParentRunID: "f-top-done"}, []string{"f-kid"}},
			{"succeeded", obsdb.RunQuery{Status: obsdb.StatusSucceeded}, []string{"f-pg", "f-top-done"}},
			{"succeeded all", obsdb.RunQuery{Status: obsdb.StatusSucceeded, ParentRunID: "*"}, []string{"f-kid", "f-pg", "f-top-done"}},
			{"running", obsdb.RunQuery{Status: obsdb.StatusRunning}, []string{"f-top-live"}},
			{"failed", obsdb.RunQuery{Status: obsdb.StatusFailed}, []string{"f-bad"}},
			{"interrupted", obsdb.RunQuery{Status: obsdb.StatusInterrupted}, nil},
			{"playground", obsdb.RunQuery{Playground: &yes}, []string{"f-pg"}},
			{"not playground", obsdb.RunQuery{Playground: &no, Status: obsdb.StatusSucceeded}, []string{"f-top-done"}},
			{"agent", obsdb.RunQuery{Agent: "conf", ParentRunID: "*"}, []string{"f-bad", "f-kid", "f-pg", "f-top-done"}},
			{"agent other", obsdb.RunQuery{Agent: "other"}, []string{"f-top-live"}},
			{"session", obsdb.RunQuery{SessionID: "s_conf"}, []string{"f-pg", "f-top-done", "f-top-live"}},
			{"session other", obsdb.RunQuery{SessionID: "s_other", Status: obsdb.StatusFailed}, []string{"f-bad"}},
			{"public", obsdb.RunQuery{PublicID: "pub_conf", Playground: &no}, []string{"f-bad", "f-top-done", "f-top-live"}},
			{"experiment", obsdb.RunQuery{ExperimentID: "e1", Status: obsdb.StatusSucceeded}, []string{"f-pg"}},
			{"meta", obsdb.RunQuery{Meta: map[string]string{"tenant": "conftest"}, Agent: "conf"}, []string{"f-pg", "f-top-done"}},
			{"everything", obsdb.RunQuery{Agent: "conf", SessionID: "s_conf", PublicID: "pub_conf", ParentRunID: "*",
				Status: obsdb.StatusSucceeded, Playground: &no, Meta: map[string]string{"tenant": "conftest"}}, []string{"f-top-done"}},
		} {
			page, err := db.Runs(ctx(), c.q)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			got := idsOf(page.Runs)
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(c.want, ",") || page.Total != len(c.want) {
				t.Errorf("%s = %v (total %d), want %v", c.name, got, page.Total, c.want)
			}
		}
	}
}

func idsOf(rows []obsdb.RunRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

// pagingBigTie: thousands of rows sharing one timestamp (a bulk import
// at a truncated clock) must neither blow a page up nor be skipped. A
// page runs at most MaxTies past its Limit; the (Before, BeforeID)
// cursor resumes inside the tie and the walk sees every run and every
// session exactly once.
func pagingBigTie(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		const n = obsdb.MaxTies + 100
		recs := make([]obsdb.Record, 0, n+1)
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("bulk-%04d", i)
			recs = append(recs, record(id, "event", "run_start", 0, `{"type":"run_start","id":"`+id+`"}`,
				map[string]any{"weft.session.id": "sess-" + id})) // all at(0)
		}
		older := record("older", "event", "run_start", 0, `{"type":"run_start","id":"older"}`,
			map[string]any{"weft.session.id": "sess-older"})
		older.Time = at(-time.Minute)
		if err := db.Write(ctx(), obsdb.Batch{Records: append(recs, older)}); err != nil {
			t.Fatal(err)
		}
		const limit = 40
		seen := map[string]int{}
		var prev string
		q := obsdb.RunQuery{Limit: limit}
		for pages := 0; ; pages++ {
			if pages > n {
				t.Fatal("run walk does not terminate")
			}
			page, err := db.Runs(ctx(), q)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Runs) > limit+obsdb.MaxTies {
				t.Fatalf("page %d = %d runs, want at most Limit+MaxTies = %d", pages, len(page.Runs), limit+obsdb.MaxTies)
			}
			for _, r := range page.Runs {
				seen[r.ID]++
				if prev != "" && r.ID >= prev && r.ID != "older" {
					t.Fatalf("page %d: %s after %s breaks the (Started, ID) descending order", pages, r.ID, prev)
				}
				prev = r.ID
			}
			if page.NextBefore == nil {
				break
			}
			if page.NextBeforeID == "" {
				t.Fatal("NextBefore without NextBeforeID")
			}
			q.Before, q.BeforeID = *page.NextBefore, page.NextBeforeID
		}
		if len(seen) != n+1 {
			t.Errorf("run walk saw %d of %d runs", len(seen), n+1)
		}
		for id, times := range seen {
			if times != 1 {
				t.Errorf("run %s on %d pages", id, times)
			}
		}
		sessions := map[string]int{}
		sq := obsdb.SessionQuery{Limit: limit}
		for pages := 0; ; pages++ {
			if pages > n {
				t.Fatal("session walk does not terminate")
			}
			page, err := db.Sessions(ctx(), sq)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Sessions) > limit+obsdb.MaxTies {
				t.Fatalf("session page %d = %d rows, want at most %d", pages, len(page.Sessions), limit+obsdb.MaxTies)
			}
			for _, s := range page.Sessions {
				sessions[s.ID]++
			}
			if page.NextBefore == nil {
				break
			}
			sq.Before, sq.BeforeID = *page.NextBefore, page.NextBeforeID
		}
		if len(sessions) != n+1 {
			t.Errorf("session walk saw %d of %d sessions", len(sessions), n+1)
		}
		for id, times := range sessions {
			if times != 1 {
				t.Errorf("session %s on %d pages", id, times)
			}
		}
	}
}

// manyDaysOneBatch: one batch whose rows span hundreds of days (a
// backfill, a sender with a broken clock) is stored whole — a storage
// engine's per-insert partition limit must not fail the batch and take
// its sound rows with it.
func manyDaysOneBatch(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		const days = 150
		var recs []obsdb.Record
		var spans []obsdb.Span
		for i := 0; i < days; i++ {
			r := record("days", "event", "step_start", int64(i), `{"type":"step_start","run_id":"days"}`, nil)
			r.Time = at(time.Duration(i) * 24 * time.Hour)
			recs = append(recs, r)
			sp := invokeSpan("days", 1, map[string]any{"gen_ai.operation.name": "chat"})
			sp.SpanID = fmt.Sprintf("%016x", i+1)
			sp.Start, sp.End = r.Time, r.Time.Add(time.Second)
			spans = append(spans, sp)
		}
		if err := db.Write(ctx(), obsdb.Batch{Spans: spans, Records: recs}); err != nil {
			t.Fatalf("a batch spanning %d days: %v", days, err)
		}
		page, err := db.Events(ctx(), "days", -1, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Events) != days {
			t.Errorf("events = %d, want %d", len(page.Events), days)
		}
		got, err := db.RunSpans(ctx(), "days")
		if err != nil || len(got) != days {
			t.Errorf("spans = %d, %v; want %d", len(got), err, days)
		}
	}
}

// closeRace: a call that races Close fails with ErrClosed — the
// documented sentinel — never with whatever the driver says about a
// handle closed under it.
func closeRace(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		if err := db.Write(ctx(), obsdb.Batch{Records: finishedRun("c1")}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 64)
		started := make(chan struct{}, 8)
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; ; i++ {
					var err error
					switch (g + i) % 4 {
					case 0:
						_, err = db.Runs(ctx(), obsdb.RunQuery{})
					case 1:
						_, err = db.Run(ctx(), "c1")
					case 2:
						_, err = db.Events(ctx(), "c1", -1, 10)
					default:
						err = db.Write(ctx(), obsdb.Batch{Records: []obsdb.Record{
							record(fmt.Sprintf("w%d-%d", g, i), "event", "run_start", 0, `{"type":"run_start"}`, nil)}})
					}
					if i == 0 {
						started <- struct{}{}
					}
					if err != nil {
						errs <- err
						return
					}
				}
			}(g)
		}
		for g := 0; g < 8; g++ {
			<-started
		}
		_ = db.Close()
		wg.Wait()
		close(errs)
		for err := range errs {
			if !errors.Is(err, obsdb.ErrClosed) {
				t.Errorf("a call racing Close = %v, want obsdb.ErrClosed", err)
			}
		}
	}
}
