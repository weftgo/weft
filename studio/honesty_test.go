package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/wefttest"
)

// The honesty surface (plan A3, ADR 0028 §11): events and live frames
// carry the weft.content.* attributes the destination's chain stamped,
// and the run document carries the run's own holes, each with the
// reason and fix of obsdb.HoleNote's table.

// recordAttrsRun drives one tool call whose result is longer than a
// small cap, then an answer, through the pipeline into Studio.
func recordAttrsRun(t *testing.T, url, runID string, dest ...otel.DestOption) {
	t.Helper()
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(url, "", dest...), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	type in struct {
		Q string `json:"q" jsonschema:"the query"`
	}
	logs := core.Tool("read_logs", "Read the logs.", func(context.Context, in) (string, error) {
		return strings.Repeat("log line ", 12), nil
	})
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "read_logs", Args: `{"q":"1"}`, ID: "c1"}),
		wefttest.Say("ok"),
	), core.Name("logs"), core.Instructions("Read logs."), logs,
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	if _, err := agent.Generate(ctx, core.RunID(runID), core.Prompt("read")); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

type attrsPage struct {
	Events []struct {
		Pos   int64           `json:"pos"`
		Event json.RawMessage `json:"event"`
		Attrs map[string]any  `json:"attrs"`
	} `json:"events"`
}

type holesDoc struct {
	Status string `json:"status"`
	Holes  []struct {
		Hole   string `json:"hole"`
		Reason string `json:"reason"`
		Fix    string `json:"fix"`
	} `json:"holes"`
}

// TestEventAttrs: a capped run's events carry
// weft.content.truncated_bytes where the cap cut (the tool result),
// and nothing where it did not; a content-off run's events all carry
// weft.content = stripped, and its run document badges the run
// stripped. Only weft.content.* keys ride along.
func TestEventAttrs(t *testing.T) {
	ts, _ := requestsServer(t)
	recordAttrsRun(t, ts.URL, "r_cap", otel.WithContent(otel.ContentConfig{MaxBytes: 16}))
	recordAttrsRun(t, ts.URL, "r_strip", otel.NoContent())
	done := func(b string) bool { return strings.Contains(b, `"type":"run_finish"`) }

	capped := fetchJSON(t, ts, "/api/runs/r_cap/events?limit=1000", done)
	requestsGolden(t, "events-capped.golden.json", capped)
	var cp attrsPage
	decode(t, capped, &cp)
	cut := 0
	for _, e := range cp.Events {
		var head struct{ Type string }
		_ = json.Unmarshal(e.Event, &head)
		for k := range e.Attrs {
			if k != "weft.content.truncated_bytes" {
				t.Errorf("capped event %d carries attr %q", e.Pos, k)
			}
		}
		if n, ok := e.Attrs["weft.content.truncated_bytes"].(float64); ok {
			cut++
			if n <= 0 {
				t.Errorf("event %d truncated_bytes = %v", e.Pos, n)
			}
		}
		if head.Type == "tool_finish" && e.Attrs == nil {
			t.Errorf("the capped tool result carries no truncated_bytes: %s", e.Event)
		}
		if head.Type == "run_start" && e.Attrs != nil {
			t.Errorf("run_start was not cut, yet carries attrs %v", e.Attrs)
		}
	}
	if cut == 0 {
		t.Fatalf("no capped event: %s", capped)
	}

	stripped := fetchJSON(t, ts, "/api/runs/r_strip/events?limit=1000", done)
	requestsGolden(t, "events-stripped.golden.json", stripped)
	var sp attrsPage
	decode(t, stripped, &sp)
	for _, e := range sp.Events {
		if len(e.Attrs) != 1 || e.Attrs["weft.content"] != "stripped" {
			t.Errorf("content-off event %d attrs = %v, want {weft.content: stripped}", e.Pos, e.Attrs)
		}
	}

	var doc holesDoc
	decode(t, fetchJSON(t, ts, "/api/runs/r_strip", nil), &doc)
	reason, fix := obsdb.HoleNote(obsdb.HoleStripped)
	if len(doc.Holes) != 1 || doc.Holes[0].Hole != "stripped" || doc.Holes[0].Reason != reason || doc.Holes[0].Fix != fix {
		t.Errorf("content-off run holes = %+v, want [stripped] with the table's words", doc.Holes)
	}
	decode(t, fetchJSON(t, ts, "/api/runs/r_cap", nil), &doc)
	if len(doc.Holes) != 0 {
		t.Errorf("capped run holes = %+v, want none (the cut is the events')", doc.Holes)
	}
}

// TestLiveFrameAttrs: a live record frame carries the record's
// weft.content.* attributes — a numeric string normalized to a number
// — and none of its other attributes.
func TestLiveFrameAttrs(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	hub := obsdb.NewHub()
	h := Handler(DB(db), Live(hub))
	resp := subscribeLive(t, h, "?run=r_1", "")

	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	rec := fxRecord("r_1", "event", "tool_finish", 0, at, `{"type":"tool_finish","run_id":"r_1"}`)
	rec.Attrs["weft.content.truncated_bytes"] = "12595"
	plain := fxRecord("r_1", "event", "step_finish", 1, at, `{"type":"step_finish","run_id":"r_1"}`)
	off := fxRecord("r_1", "event", "run_finish", 2, at, `{"type":"run_finish","run_id":"r_1"}`)
	off.Attrs["weft.content"] = "stripped"
	for _, r := range []obsdb.Record{rec, plain, off} {
		hub.Publish(context.Background(), obsdb.RecordFrame(r))
	}
	frames := readSSE(t, resp, 3, 5*time.Second)
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3", len(frames))
	}
	for i, want := range []string{
		`"attrs":{"weft.content.truncated_bytes":12595}`,
		``,
		`"attrs":{"weft.content":"stripped"}`,
	} {
		if want == "" {
			if strings.Contains(frames[i].data, `"attrs"`) {
				t.Errorf("frame %d carries attrs: %s", i, frames[i].data)
			}
			continue
		}
		if !strings.Contains(frames[i].data, want) {
			t.Errorf("frame %d misses %s: %s", i, want, frames[i].data)
		}
	}
}

// TestRunHolesPreA1: the run of a database written by the previous
// release (TestRequestsNotRecorded's file) reads not_recorded with the
// table's reason and fix — the run header's badge.
func TestRunHolesPreA1(t *testing.T) {
	srv := New(Open(preA1File(t)))
	t.Cleanup(func() { _ = srv.Close() })
	code, _, body := get(t, srv.Handler(), "/studio/api/runs/old1")
	if code != http.StatusOK {
		t.Fatalf("run: %d %s", code, body)
	}
	// The run document the web's page test serves for a pre-A1 run.
	golden(t, "run-pre-a1.golden.json", body)
	var doc holesDoc
	decode(t, body, &doc)
	reason, fix := obsdb.HoleNote(obsdb.HoleNotRecorded)
	if len(doc.Holes) != 1 || doc.Holes[0].Hole != "not_recorded" || doc.Holes[0].Reason != reason || doc.Holes[0].Fix != fix {
		t.Errorf("pre-A1 run holes = %+v, want [not_recorded] with the table's words", doc.Holes)
	}
}

// TestRunHolesInterrupted: a run that stopped reporting reads
// interrupted, and the event position its lost batch left reads gap —
// in the table's order. A run with spans and no events reads derived:
// its row was built from the spans.
func TestRunHolesInterrupted(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	old := time.Now().Add(-time.Hour).UTC()
	sp := fxSpanRec("r_ok", old, old.Add(time.Second), 0, "", 1, 1)
	sp.Attrs["weft.run.id"] = "r_spans"
	sp.SpanID = "0a0b0c0d0e0f1011"
	if err := db.Write(context.Background(), obsdb.Batch{
		Records: []obsdb.Record{
			fxRecord("r_int", "event", "run_start", 0, old, `{"type":"run_start","id":"r_int"}`),
			fxRecord("r_int", "event", "step_start", 2, old, `{"type":"step_start","run_id":"r_int","index":0}`),
		},
		Spans: []obsdb.Span{sp},
	}); err != nil {
		t.Fatal(err)
	}
	h := Handler(DB(db))
	for run, want := range map[string][]obsdb.Hole{
		"r_int":   {obsdb.HoleInterrupted, obsdb.HoleGap},
		"r_spans": {obsdb.HoleInterrupted, obsdb.HoleNotRecorded, obsdb.HoleDerived},
	} {
		code, _, body := get(t, h, "/studio/api/runs/"+run)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", run, code, body)
		}
		var doc holesDoc
		decode(t, body, &doc)
		var got []string
		for _, hl := range doc.Holes {
			got = append(got, hl.Hole)
			if hl.Reason == "" {
				t.Errorf("%s: hole %s has no reason", run, hl.Hole)
			}
		}
		var wantS []string
		for _, w := range want {
			wantS = append(wantS, string(w))
		}
		if strings.Join(got, " ") != strings.Join(wantS, " ") || doc.Status != "interrupted" {
			t.Errorf("%s (%s) holes = %v, want %v", run, doc.Status, got, wantS)
		}
	}
}
