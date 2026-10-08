package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
// table's reason and fix — the run header's badge. The file's row
// counts two steps but holds no event: gap first (lostEvents, the
// export's events block says the same).
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
	if len(doc.Holes) != 2 || doc.Holes[0].Hole != "gap" || doc.Holes[1].Hole != "not_recorded" ||
		doc.Holes[1].Reason != reason || doc.Holes[1].Fix != fix {
		t.Errorf("pre-A1 run holes = %+v, want [gap, not_recorded], not_recorded with the table's words", doc.Holes)
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

// v090File copies studio/testdata/v0.9.0.db — a database weft v0.9.0
// wrote (its README says how) — into a temp dir: Studio migrates the
// file it opens, and the committed one must stay as v0.9.0 left it.
func v090File(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "v0.9.0.db"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "v0.9.0.db")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// migrationMax is the highest obsdb migration a sqlite file records.
func migrationMax(t *testing.T, path string) int {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var v int
	if err := raw.QueryRow(`SELECT max(version) FROM obsdb_migrations`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestRunHolesV090: the database weft v0.9.0 really wrote opens in this
// Studio — migrated to the current schema — and every pane that cannot
// be filled says why: the run reads not_recorded, every step's
// assembled holes carry it, and the requests and tools routes answer
// it with the table's reason and fix. Its run, events and transcript
// are goldened for the web's page test.
func TestRunHolesV090(t *testing.T) {
	path := v090File(t)
	if got := migrationMax(t, path); got != 2 {
		t.Fatalf("v0.9.0.db records migration %d, want 2 (v0.9.0's last)", got)
	}
	srv := New(Open(path))
	t.Cleanup(func() { _ = srv.Close() })
	h := srv.Handler()
	code, _, body := get(t, h, "/studio/api/runs/r_v090")
	if code != http.StatusOK {
		t.Fatalf("run: %d %s", code, body)
	}
	migs, err := filepath.Glob(filepath.Join("..", "obsdb", "sqlite", "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if got := migrationMax(t, path); got != len(migs) {
		t.Errorf("after open: migration %d, want the current %d", got, len(migs))
	}
	golden(t, "run-v090.golden.json", body)
	var doc struct {
		holesDoc
		Steps int `json:"steps"`
	}
	decode(t, body, &doc)
	reason, fix := obsdb.HoleNote(obsdb.HoleNotRecorded)
	if doc.Status != "succeeded" || doc.Steps != 2 || len(doc.Holes) != 1 || doc.Holes[0].Hole != "not_recorded" ||
		doc.Holes[0].Reason != reason || doc.Holes[0].Fix != fix {
		t.Fatalf("v0.9.0 run = %s, want a succeeded 2-step run with holes [not_recorded]", body)
	}
	_, _, events := get(t, h, "/studio/api/runs/r_v090/events?limit=1000")
	golden(t, "run-v090-events.golden.json", events)
	_, _, transcript := get(t, h, "/studio/api/runs/r_v090/transcript")
	golden(t, "run-v090-transcript.golden.json", transcript)
	for n := range doc.Steps {
		code, _, step := get(t, h, fmt.Sprintf("/studio/api/runs/r_v090/steps/%d", n))
		if code != http.StatusOK {
			t.Fatalf("step %d: %d %s", n, code, step)
		}
		var sd struct {
			Holes []struct{ Hole string } `json:"holes"`
		}
		decode(t, step, &sd)
		found := false
		for _, hl := range sd.Holes {
			found = found || hl.Hole == "not_recorded"
		}
		if !found {
			t.Errorf("step %d holes = %+v, want not_recorded among them", n, sd.Holes)
		}
	}
	for _, route := range []string{"requests", "tools"} {
		_, _, b := get(t, h, "/studio/api/runs/r_v090/"+route)
		var env struct{ Badge, Reason, Fix string }
		decode(t, b, &env)
		if env.Badge != "not_recorded" || env.Reason != reason || env.Fix != fix {
			t.Errorf("%s = %s, want the not_recorded badge with the table's words", route, b)
		}
	}
}

// TestContentOffAgent: an agent with core.Content(false) captures no
// content itself — its events carry weft.content = none through a
// content-on destination — and the run reads stripped from its
// run_start.
func TestContentOffAgent(t *testing.T) {
	ts, _ := requestsServer(t)
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	agent := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("quiet"), core.Content(false),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	if _, err := agent.Generate(ctx, core.RunID("r_none"), core.Prompt("hi")); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	body := fetchJSON(t, ts, "/api/runs/r_none/events?limit=1000", func(b string) bool { return strings.Contains(b, `"type":"run_finish"`) })
	var page attrsPage
	decode(t, body, &page)
	for _, e := range page.Events {
		if len(e.Attrs) != 1 || e.Attrs["weft.content"] != "none" {
			t.Errorf("event %d attrs = %v, want {weft.content: none}", e.Pos, e.Attrs)
		}
	}
	var doc holesDoc
	decode(t, fetchJSON(t, ts, "/api/runs/r_none", nil), &doc)
	if len(doc.Holes) != 1 || doc.Holes[0].Hole != "stripped" {
		t.Errorf("content-off agent run holes = %+v, want [stripped]", doc.Holes)
	}
}

// TestStepHolesFromAttrs: the assembled step reads its events' attrs —
// a capped tool result is truncated with the bytes cut; a content-off
// run written before the request record is stripped and not_recorded.
func TestStepHolesFromAttrs(t *testing.T) {
	ts, _ := requestsServer(t)
	recordAttrsRun(t, ts.URL, "r_cap", otel.WithContent(otel.ContentConfig{MaxBytes: 16}))
	fetchJSON(t, ts, "/api/runs/r_cap", func(b string) bool { return strings.Contains(b, `"status":"succeeded"`) })
	var sd struct {
		Holes []struct{ Hole, Reason, Fix string } `json:"holes"`
	}
	decode(t, fetchJSON(t, ts, "/api/runs/r_cap/steps/0", nil), &sd)
	_, fix := obsdb.HoleNote(obsdb.HoleTruncated)
	// The 16-byte cap cut the tool catalog (its specific reason comes
	// first) and the tool result (the events' cut, beside it); both
	// carry the table's truncated fix, once.
	if len(sd.Holes) == 0 || sd.Holes[0].Hole != "truncated" ||
		!strings.Contains(sd.Holes[0].Reason, "the step's tool catalog was cut by a destination's cap") ||
		!strings.Contains(sd.Holes[0].Reason, "92 bytes") || sd.Holes[0].Fix != fix {
		t.Errorf("capped step holes = %+v, want truncated: the catalog's reason and the events' 92 bytes, with the table's fix %q", sd.Holes, fix)
	}

	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	at := time.Now().UTC()
	var recs []obsdb.Record
	for i, ev := range []struct{ typ, body string }{
		{"run_start", `{"type":"run_start","id":"r_off"}`},
		{"step_start", `{"type":"step_start","run_id":"r_off","index":0}`},
		{"step_finish", `{"type":"step_finish","run_id":"r_off","index":0,"reason":"stop","usage":{"input_tokens":1,"output_tokens":1}}`},
		{"run_finish", `{"type":"run_finish","run_id":"r_off","steps":1}`},
	} {
		r := fxRecord("r_off", "event", ev.typ, int64(i), at, ev.body)
		delete(r.Attrs, "weft.instructions.hash") // written before the request record
		r.Attrs["weft.content"] = "stripped"
		recs = append(recs, r)
	}
	if err := db.Write(context.Background(), obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
	code, _, body := get(t, Handler(DB(db)), "/studio/api/runs/r_off/steps/0")
	if code != http.StatusOK {
		t.Fatalf("step: %d %s", code, body)
	}
	decode(t, body, &sd)
	var got []string
	for _, hl := range sd.Holes {
		got = append(got, hl.Hole)
	}
	if !strings.Contains(" "+strings.Join(got, " ")+" ", " stripped ") || !strings.Contains(" "+strings.Join(got, " ")+" ", " not_recorded ") {
		t.Errorf("content-off pre-A1 step holes = %v, want stripped and not_recorded", got)
	}
}

// TestLiveBackfillAttrs: a resume's backfill frames (Last-Event-ID set,
// read from the database) carry the stored events' attrs as the live
// ones do.
func TestLiveBackfillAttrs(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	start := fxRecord("r_bf", "event", "run_start", 0, at, `{"type":"run_start","id":"r_bf"}`)
	start.Attrs["weft.content"] = "stripped"
	fin := fxRecord("r_bf", "event", "tool_finish", 1, at, `{"type":"tool_finish","run_id":"r_bf"}`)
	fin.Attrs["weft.content.truncated_bytes"] = int64(12595)
	plain := fxRecord("r_bf", "event", "step_finish", 2, at, `{"type":"step_finish","run_id":"r_bf"}`)
	if err := db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{start, fin, plain}}); err != nil {
		t.Fatal(err)
	}
	h := Handler(DB(db), Live(obsdb.NewHub()))
	frames := readSSE(t, subscribeLive(t, h, "?run=r_bf", "1"), 3, 5*time.Second)
	if len(frames) != 3 {
		t.Fatalf("got %d backfill frames, want 3", len(frames))
	}
	for i, want := range []string{`"attrs":{"weft.content":"stripped"}`, `"attrs":{"weft.content.truncated_bytes":12595}`, ``} {
		if want == "" {
			if strings.Contains(frames[i].data, `"attrs"`) {
				t.Errorf("backfill frame %d carries attrs: %s", i, frames[i].data)
			}
		} else if !strings.Contains(frames[i].data, want) {
			t.Errorf("backfill frame %d misses %s: %s", i, want, frames[i].data)
		}
	}
}

// TestStepHolesBothCuts: a step with one tool result the core's
// MaxResultBytes cut and another a destination's cap cut names both
// causes on its one truncated badge — the core's first,
// with its weft.MaxResultBytes fix, then the destination's.
func TestStepHolesBothCuts(t *testing.T) {
	ts, _ := requestsServer(t)
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, "", otel.WithContent(otel.ContentConfig{MaxBytes: 48})), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	type in struct {
		Q string `json:"q" jsonschema:"the query"`
	}
	big := core.Tool("big", "Big.", func(context.Context, in) (string, error) { return strings.Repeat("x", 100), nil },
		core.MaxResultBytes(10)) // the core cuts it: 10 bytes and the marker, under the destination's 48
	long := core.Tool("long", "Long.", func(context.Context, in) (string, error) { return strings.Repeat("y", 100), nil })
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "big", Args: `{"q":"1"}`, ID: "c1"},
			wefttest.Call{Name: "long", Args: `{"q":"2"}`, ID: "c2"}),
		wefttest.Say("ok"),
	), core.Name("both"), big, long, core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	if _, err := agent.Generate(ctx, core.RunID("r_both"), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	fetchJSON(t, ts, "/api/runs/r_both", func(b string) bool { return strings.Contains(b, `"status":"succeeded"`) })
	var sd struct {
		Holes []struct{ Hole, Reason, Fix string } `json:"holes"`
	}
	decode(t, fetchJSON(t, ts, "/api/runs/r_both/steps/0", nil), &sd)
	var tr *struct{ Hole, Reason, Fix string }
	for i := range sd.Holes {
		if sd.Holes[i].Hole == "truncated" {
			tr = &sd.Holes[i]
		}
	}
	if tr == nil {
		t.Fatalf("holes = %+v, want truncated", sd.Holes)
	}
	byCore, byDest := strings.Index(tr.Reason, "a tool result was cut by its result cap"), strings.Index(tr.Reason, "a destination's cap cut")
	if byCore < 0 || byDest < 0 || byCore > byDest {
		t.Errorf("truncated reason = %q, want the core's result cap, then the destination's cut", tr.Reason)
	}
	if !strings.HasPrefix(tr.Fix, "raise the tool's weft.MaxResultBytes") {
		t.Errorf("truncated fix = %q, want the core cut's weft.MaxResultBytes first", tr.Fix)
	}
}
