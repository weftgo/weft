package studio

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// TestRootBeforeRecords: a root span stored before the records that
// trail it (a log exporter's retry or backlog) settles for
// obsdb.InterruptedAfter — the events route keeps done false so the
// client keeps tailing, and no hole calls the records lost — then the
// records land and the stream is done (docs/record-contract.md §6, the
// U2 review's finding 2).
func TestRootBeforeRecords(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	if err := db.Write(context.Background(), obsdb.Batch{
		Spans: []obsdb.Span{fxSpanRec("r_ok", now.Add(-time.Second), now, 1, "", 1, 1)},
	}); err != nil {
		t.Fatal(err)
	}
	h := Handler(DB(db))
	var page struct {
		Events []posEvent `json:"events"`
		Done   bool       `json:"done"`
	}
	code, _, body := get(t, h, "/studio/api/runs/r_ok/events")
	if code != http.StatusOK {
		t.Fatalf("events: %d %s", code, body)
	}
	decode(t, body, &page)
	if page.Done {
		t.Errorf("events of a settling run = done, want the tail followed: %s", body)
	}
	var doc holesDoc
	_, _, body = get(t, h, "/studio/api/runs/r_ok")
	decode(t, body, &doc)
	if doc.Status != "succeeded" {
		t.Errorf("settling run status = %s, want succeeded (from its root)", doc.Status)
	}
	for _, hl := range doc.Holes {
		if hl.Hole == string(obsdb.HoleNotRecorded) || hl.Hole == string(obsdb.HoleGap) {
			t.Errorf("settling run holes = %+v, want no record called lost", doc.Holes)
		}
	}

	// The records land: the stream is whole and done.
	if err := db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{
		fxRecord("r_ok", "event", "run_start", 0, now.Add(-time.Second), `{"type":"run_start","id":"r_ok"}`),
		fxRecord("r_ok", "event", "run_finish", 1, now, `{"type":"run_finish","run_id":"r_ok","steps":1}`),
	}}); err != nil {
		t.Fatal(err)
	}
	_, _, body = get(t, h, "/studio/api/runs/r_ok/events")
	page.Events, page.Done = nil, false
	decode(t, body, &page)
	if !page.Done || len(page.Events) != 2 {
		t.Errorf("events after the records landed = %d, done %v; want 2, done", len(page.Events), page.Done)
	}
}

// TestTailLostAndSpansOnly: past the settling window a succeeded root
// with events and no run_finish reads gap in tail_lost's words — on the
// run and on the step whose step_finish was dropped (status ok, not
// error) — and a spans-only run reads not_recorded in no_logger's words
// on its step documents and its requests pane, never a step gap (the U2
// review's findings 1 and 4). Its events route answers done (finding 3).
func TestTailLostAndSpansOnly(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	old := time.Now().Add(-time.Hour).UTC()
	tail := fxSpanRec("r_ok", old, old.Add(time.Second), 1, "", 1, 1)
	spans := fxSpanRec("r_ok", old, old.Add(time.Second), 1, "", 1, 1)
	spans.Attrs["weft.run.id"] = "r_spans"
	spans.SpanID = "0a0b0c0d0e0f1013"
	if err := db.Write(context.Background(), obsdb.Batch{
		Records: []obsdb.Record{
			fxRecord("r_ok", "event", "run_start", 0, old, `{"type":"run_start","id":"r_ok"}`),
			fxRecord("r_ok", "event", "step_start", 1, old, `{"type":"step_start","run_id":"r_ok","index":0}`),
		},
		Spans: []obsdb.Span{tail, spans},
	}); err != nil {
		t.Fatal(err)
	}
	h := Handler(DB(db))
	tailReason, tailFix := obsdb.HoleNoteFor(obsdb.HoleGap, obsdb.CauseTailLost)
	noLogger, noLoggerFix := obsdb.HoleNoteFor(obsdb.HoleNotRecorded, obsdb.CauseNoLogger)
	has := func(doc holesDoc, hole obsdb.Hole, reason, fix string) bool {
		for _, hl := range doc.Holes {
			if hl.Hole == string(hole) && hl.Reason == reason && hl.Fix == fix {
				return true
			}
		}
		return false
	}

	var run holesDoc
	_, _, body := get(t, h, "/studio/api/runs/r_ok")
	decode(t, body, &run)
	if run.Status != "succeeded" || !has(run, obsdb.HoleGap, tailReason, tailFix) {
		t.Errorf("tail-lost run = %s %+v, want succeeded with gap in tail_lost's words", run.Status, run.Holes)
	}
	var step holesDoc
	code, _, body := get(t, h, "/studio/api/runs/r_ok/steps/0")
	if code != http.StatusOK {
		t.Fatalf("step 0: %d %s", code, body)
	}
	decode(t, body, &step)
	if step.Status != stepOK || !has(step, obsdb.HoleGap, tailReason, tailFix) {
		t.Errorf("tail-lost step 0 = %s %+v, want ok with gap in tail_lost's words", step.Status, step.Holes)
	}
	var page struct {
		Done bool `json:"done"`
	}
	_, _, body = get(t, h, "/studio/api/runs/r_ok/events")
	decode(t, body, &page)
	if !page.Done {
		t.Errorf("events of a root-ended run past the window: done false, want done")
	}

	step = holesDoc{}
	code, _, body = get(t, h, "/studio/api/runs/r_spans/steps/0")
	if code != http.StatusOK {
		t.Fatalf("spans-only step 0: %d %s", code, body)
	}
	decode(t, body, &step)
	if !has(step, obsdb.HoleNotRecorded, noLogger, noLoggerFix) {
		t.Errorf("spans-only step 0 holes = %+v, want not_recorded in no_logger's words", step.Holes)
	}
	for _, hl := range step.Holes {
		if hl.Hole == string(obsdb.HoleGap) {
			t.Errorf("spans-only step 0 has a gap: %+v", hl)
		}
	}
	var reqs struct {
		Badge, Reason, Fix string
	}
	_, _, body = get(t, h, "/studio/api/runs/r_spans/requests")
	decode(t, body, &reqs)
	if reqs.Badge != string(obsdb.HoleNotRecorded) || reqs.Reason != noLogger || reqs.Fix != noLoggerFix {
		t.Errorf("spans-only requests = %+v, want not_recorded in no_logger's words", reqs)
	}
}

// TestSettlingServed: the settling window reaches the API — the run
// document and the run rows carry settling while a root-ended run's
// records may still land, a failed spans-only root's steps read error
// (never running under a failed header) with no record called lost,
// and a settling spans-only step's Messages-in pane names no gap (the
// U2 re-review's findings 1–3).
func TestSettlingServed(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	ok := fxSpanRec("r_ok", now.Add(-time.Second), now, 1, "", 1, 1)
	chat := fxSpanRec("r_ok", now.Add(-time.Second), now, 1, "", 1, 1)
	chat.SpanID, chat.ParentSpanID, chat.Name = "0a0b0c0d0e0f1020", ok.SpanID, "chat script"
	chat.Attrs["gen_ai.operation.name"] = "chat"
	chat.Attrs["weft.step.index"] = int64(0)
	failed := fxSpanRec("r_fail", now.Add(-time.Second), now, 2, "model exploded", 1, 1)
	if err := db.Write(context.Background(), obsdb.Batch{Spans: []obsdb.Span{ok, chat, failed}}); err != nil {
		t.Fatal(err)
	}
	h := Handler(DB(db))

	var doc struct {
		Status   string `json:"status"`
		Settling bool   `json:"settling"`
	}
	_, _, body := get(t, h, "/studio/api/runs/r_ok")
	decode(t, body, &doc)
	if doc.Status != "succeeded" || !doc.Settling {
		t.Errorf("run doc = %s settling %v, want succeeded and settling: %s", doc.Status, doc.Settling, body)
	}
	var list struct {
		Runs []struct {
			ID       string `json:"id"`
			Settling bool   `json:"settling"`
		} `json:"runs"`
	}
	_, _, body = get(t, h, "/studio/api/runs")
	decode(t, body, &list)
	if len(list.Runs) != 2 {
		t.Fatalf("runs = %s, want two rows", body)
	}
	for _, r := range list.Runs {
		if !r.Settling {
			t.Errorf("run row %s: settling false, want true", r.ID)
		}
	}

	// Finding 2: a failed root decides its steps' outcome at once.
	var step struct {
		holesDoc
		MessagesIn struct {
			Badge  string `json:"badge"`
			Reason string `json:"reason"`
		} `json:"messages_in"`
	}
	code, _, body := get(t, h, "/studio/api/runs/r_fail/steps/0")
	if code != http.StatusOK {
		t.Fatalf("failed step 0: %d %s", code, body)
	}
	decode(t, body, &step)
	if step.Status != stepError {
		t.Errorf("failed settling step 0 = %s, want error", step.Status)
	}
	for _, hl := range step.Holes {
		if hl.Hole == string(obsdb.HoleGap) || hl.Hole == string(obsdb.HoleNotRecorded) {
			t.Errorf("failed settling step 0 calls a record lost: %+v", hl)
		}
	}

	// Finding 3: a settling step whose chat span arrived and request
	// record not yet names no gap in its Messages-in pane.
	step.holesDoc, step.MessagesIn.Badge = holesDoc{}, ""
	code, _, body = get(t, h, "/studio/api/runs/r_ok/steps/0")
	if code != http.StatusOK {
		t.Fatalf("settling step 0: %d %s", code, body)
	}
	decode(t, body, &step)
	if step.MessagesIn.Badge != "" {
		t.Errorf("settling step 0 messages_in = %+v, want no badge", step.MessagesIn)
	}
	for _, hl := range step.Holes {
		if hl.Hole == string(obsdb.HoleGap) {
			t.Errorf("settling step 0 holes = %+v, want no gap", step.Holes)
		}
	}

	// Past the window the field is gone (omitempty).
	old := now.Add(-time.Hour)
	stale := fxSpanRec("r_sub", old.Add(-time.Second), old, 1, "", 1, 1)
	if err := db.Write(context.Background(), obsdb.Batch{Spans: []obsdb.Span{stale}}); err != nil {
		t.Fatal(err)
	}
	_, _, body = get(t, h, "/studio/api/runs/r_sub")
	if strings.Contains(string(body), `"settling"`) {
		t.Errorf("run doc past the window carries settling: %s", body)
	}
}

// TestTailLostAndCounterGap: a run that lost a mid-run batch (a hole
// below its high-water mark) and its record tail says both reasons on
// its one gap badge (the U2 re-review's finding 4).
func TestTailLostAndCounterGap(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	old := time.Now().Add(-time.Hour).UTC()
	if err := db.Write(context.Background(), obsdb.Batch{
		Records: []obsdb.Record{
			fxRecord("r_ok", "event", "run_start", 0, old, `{"type":"run_start","id":"r_ok"}`),
			fxRecord("r_ok", "event", "step_start", 2, old, `{"type":"step_start","run_id":"r_ok","index":0}`),
		},
		Spans: []obsdb.Span{fxSpanRec("r_ok", old, old.Add(time.Second), 1, "", 1, 1)},
	}); err != nil {
		t.Fatal(err)
	}
	h := Handler(DB(db))
	var doc holesDoc
	_, _, body := get(t, h, "/studio/api/runs/r_ok")
	decode(t, body, &doc)
	tailReason, _ := obsdb.HoleNoteFor(obsdb.HoleGap, obsdb.CauseTailLost)
	var gap string
	for _, hl := range doc.Holes {
		if hl.Hole == string(obsdb.HoleGap) {
			gap = hl.Reason
		}
	}
	if !strings.Contains(gap, tailReason) || !strings.Contains(gap, "event positions are missing") {
		t.Errorf("gap reason = %q, want tail_lost's words and the counter's", gap)
	}
}
