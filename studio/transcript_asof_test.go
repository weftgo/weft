package studio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/otel"
)

// transcriptAsOfT is transcript?step=N as a client decodes it.
type transcriptAsOfT struct {
	Batches     []json.RawMessage `json:"batches"`
	Step        int               `json:"step"`
	Messages    []core.Message    `json:"messages"`
	CompactedAt *compactedNote    `json:"compacted_at"`
	Badge       string            `json:"badge"`
}

// TestTranscriptAsOfStep (ADR 0029): GET /api/runs/{id}/transcript
// with ?step=N answers the bare route's batches and the messages step
// N's model call carried. A9's step run compacts step 2 (a PrepareStep
// trim): step 2's messages are the compacted ones — the user's prompt,
// the summary, step 1's call and result — and compacted_at names the
// view (index, range [1, 3), 2 → 1, hash; counts only, no body); step 1
// carried the plain transcript (compacted_at null). A step past the
// run's last is 404, a bad ordinal 400; without step the route is
// unchanged (transcript-compacted.golden.json pins it).
func TestTranscriptAsOfStep(t *testing.T) {
	ts, _ := requestsServer(t)
	recordStepsRun(t, ts.URL, "r_steps", nil)
	fetchJSON(t, ts, "/api/runs/r_steps", func(b string) bool { return strings.Contains(b, `"request_count":6`) })

	body := fetchJSON(t, ts, "/api/runs/r_steps/transcript?step=2", nil)
	stepGolden(t, "transcript-as-of-step.golden.json", body)
	var doc transcriptAsOfT
	decode(t, body, &doc)
	var run struct {
		Compactions []runCompactionT `json:"compactions"`
	}
	decode(t, fetchJSON(t, ts, "/api/runs/r_steps", nil), &run)
	c := doc.CompactedAt
	if c == nil || len(run.Compactions) != 1 || c.Index != *run.Compactions[0].Index || c.Hash != run.Compactions[0].Hash ||
		c.Step != 2 || c.FromSeq != 1 || c.ToSeq != 3 || c.Replaced != 2 || c.Entries != 1 {
		t.Fatalf("compacted_at = %+v, want the run's one view (%+v)", c, run.Compactions)
	}
	if doc.Step != 2 || len(doc.Messages) != 4 || doc.Messages[1].Text() != "summary: the order was looked up" || doc.Badge != "" {
		t.Errorf("step 2's messages = %+v, want [prompt, summary, step 1's call, its result]", doc.Messages)
	}
	var bare transcript
	decode(t, fetchJSON(t, ts, "/api/runs/r_steps/transcript", nil), &bare)
	if len(doc.Batches) != len(bare.Batches) {
		t.Errorf("?step= batches = %d, the bare route's %d", len(doc.Batches), len(bare.Batches))
	}

	decode(t, fetchJSON(t, ts, "/api/runs/r_steps/transcript?step=1", nil), &doc)
	if doc.CompactedAt != nil || len(doc.Messages) != 3 || doc.Step != 1 {
		t.Errorf("step 1 = compacted_at %+v, %d messages; want the plain prefix of 3", doc.CompactedAt, len(doc.Messages))
	}
	for path, want := range map[string]int{
		"/api/runs/r_steps/transcript?step=9":  http.StatusNotFound,
		"/api/runs/r_steps/transcript?step=x":  http.StatusBadRequest,
		"/api/runs/r_steps/transcript?step=-1": http.StatusBadRequest,
		"/api/runs/r_nope/transcript?step=0":   http.StatusNotFound,
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
}

// TestTranscriptAsOfStepHoles (ADR 0029): a step whose messages the
// records cannot rebuild is 409 with the hole by cause — gap when the
// growth record a request names was never stored, stripped for a
// content-off run (no messages captured) — never derived, which the
// 200 answer keeps for the cut rule.
func TestTranscriptAsOfStepHoles(t *testing.T) {
	ts, srv := requestsServer(t)
	now := time.Now().UTC()
	rec := func(kind string, attrs map[string]any, body string) obsdb.Record {
		a := map[string]any{"weft.record": kind, "weft.run.id": "r_gap"}
		for k, v := range attrs {
			a[k] = v
		}
		return obsdb.Record{Time: now, EventName: "weft." + kind, Body: body, Attrs: a}
	}
	if err := srv.db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{
		rec("event", map[string]any{"weft.event.type": "run_start", "weft.event.pos": int64(0), "weft.instructions.hash": "ih"}, `{"type":"run_start","id":"r_gap"}`),
		rec("messages", map[string]any{"weft.messages.index": int64(0), "weft.step.index": int64(0), "weft.messages.input": true},
			`[{"role":"user","content":[{"type":"text","text":"hi"}]}]`),
		// Step 0's request names messages record 3, which never arrived.
		rec("request", map[string]any{"weft.request.index": int64(0), "weft.step.index": int64(0), "weft.attempt.index": int64(1)},
			`{"step":0,"attempt":1,"messages_ref":{"index":3,"count":1},"tools":{"catalog_hash":"","names":[]},"params":{},"model":{"name":"m"}}`),
	}}); err != nil {
		t.Fatal(err)
	}
	recordStepsRun(t, ts.URL, "r_off", nil, otel.NoContent())
	fetchJSON(t, ts, "/api/runs/r_off", func(b string) bool { return strings.Contains(b, `"request_count":6`) })
	for path, hole := range map[string]obsdb.Hole{
		"/api/runs/r_gap/transcript?step=0": obsdb.HoleGap,
		"/api/runs/r_off/transcript?step=1": obsdb.HoleStripped,
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusConflict || !strings.Contains(string(b), `"badge":"`+string(hole)+`"`) {
			t.Errorf("GET %s = %d %s, want 409 with badge %s", path, resp.StatusCode, b, hole)
		}
	}
}

// TestPlaygroundEditInsideCompactionRefused (ADR 0029): from_step 2 of
// A9's step run replays the compacted prefix step 2's request carried,
// which no longer holds step 0's lookup call and result — an edit of
// that result is a 400 in weft/runtime's exact words (its
// TestReplayAcrossCompactionReproducesTheModelsInput pins the same
// sentence). Step 1's research call lies outside the replaced range:
// its edit passes the transcript checks.
func TestPlaygroundEditInsideCompactionRefused(t *testing.T) {
	srv := New(Open(filepath.Join(t.TempDir(), "weft.db")), Playground(true))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	recordStepsRun(t, ts.URL, "r_steps", nil)
	fetchJSON(t, ts, "/api/runs/r_steps", func(b string) bool { return strings.Contains(b, `"request_count":6`) })

	post := func(edit string) (int, string) {
		t.Helper()
		body := `{"runtime":"rt_x","agent":"orders","source":{"run_id":"r_steps","from_step":2},` +
			`"transcript_edits":[` + edit + `],"engine":"live","thread":"ephemeral"}`
		resp, err := http.Post(ts.URL+"/api/playground/runs", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	const want = `call \"c_lookup\" of step 0 was compacted away before step 2's request (messages [1, 3) replaced by 1): the model never saw it there; edit from an earlier from_step`
	code, body := post(`{"step":0,"call_id":"c_lookup","tool_result":"x"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, want) {
		t.Errorf("edit inside the view = %d %s\nwant 400 with %s", code, body, want)
	}
	if code, body := post(`{"step":1,"call_id":"c_sub","tool_result":"x"}`); strings.Contains(body, "compacted") || strings.Contains(body, "kept prefix") {
		t.Errorf("edit outside the view = %d %s, want the transcript checks passed", code, body)
	}
}
