package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
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
	// The F2 kinds (ADR 0029 §8): an args rewrite inside the range is
	// refused in the same words; a user edit of the turn's prompt and an
	// insert at the range's end lie outside it.
	code, body = post(`{"kind":"tool_args","step":0,"call_id":"c_lookup","args":{"order_id":"1"}}`)
	if code != http.StatusBadRequest || !strings.Contains(body, want) {
		t.Errorf("args edit inside the view = %d %s\nwant 400 with %s", code, body, want)
	}
	if code, body := post(`{"kind":"user","step":0,"content":"x"},{"kind":"insert","step":1,"content":"y"}`); strings.Contains(body, "compacted") || strings.Contains(body, "kept prefix") {
		t.Errorf("user edit and insert outside the view = %d %s, want the transcript checks passed", code, body)
	}
	if code, body := post(`{"step":1,"call_id":"c_sub","tool_result":"x"}`); strings.Contains(body, "compacted") || strings.Contains(body, "kept prefix") {
		t.Errorf("edit outside the view = %d %s, want the transcript checks passed", code, body)
	}
}

// recordAnsweredRun records, through the real pipeline into Studio, a
// run whose last step's call has its (error) result but no reply: step
// 0 looks up, step 1's refund errors, then the run stops before step 2
// answers — the model call failing (failed: step 2 has a request
// record) or the step budget breached (budget: no request record for
// step 2). A third shape (done) answers with a call-free reply.
func recordAnsweredRun(t *testing.T, url, runID, shape string) {
	t.Helper()
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(url, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	type in struct {
		OrderID string `json:"order_id"`
	}
	lookup := core.Tool("lookup_order", "Look up.", func(context.Context, in) (string, error) { return "shipped", nil })
	refund := core.Tool("refund", "Refund.", func(context.Context, in) (string, error) {
		return "", &core.ToolError{Code: "RATE_LIMITED", Message: "429"}
	})
	turns := []wefttest.Turn{
		wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{"order_id":"42"}`, ID: "c1"}),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{"order_id":"42"}`, ID: "c2"}),
	}
	opts := []core.Option{core.Name("acme-support"), core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()), lookup, refund}
	switch shape {
	case "failed":
		turns = append(turns, wefttest.Fail(errors.New("provider down")))
	case "budget":
		opts = append(opts, core.MaxSteps(2))
	case "done":
		turns = append(turns, wefttest.Say("sorry, the refund failed"))
	}
	_, err = core.New(wefttest.Script(turns...), opts...).Generate(ctx, core.RunID(runID), core.Prompt("refund 42"))
	if (err == nil) != (shape == "done") {
		t.Fatalf("%s run: err = %v", shape, err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestFromStepAtTheStepCount (ADR 0029): from_step equal to the run's
// step count is fresh when the last step ended in answered tool calls —
// the replay's first model call answers them — so a run that failed (or
// hit its step budget) after step 1's refund errored replays from step
// 2 with step 1's result edited: the playground accepts it, and
// transcript?step=2 serves the prefix (the failed call's own request; a
// derived whole transcript when no request was recorded). After a
// call-free reply the same from_step has nothing to answer: still 400.
func TestFromStepAtTheStepCount(t *testing.T) {
	pt := newPlaygroundTestServer(t)
	go func() {
		for range pt.cmd {
		}
	}()
	for _, shape := range []string{"failed", "budget", "done"} {
		recordAnsweredRun(t, pt.ts.URL, "r_"+shape, shape)
	}
	for shape, want := range map[string]struct {
		count int
		badge string
	}{"failed": {5, ""}, "budget": {5, "derived"}} {
		body := fetchJSON(t, pt.ts, "/api/runs/r_"+shape+"/transcript?step=2", nil)
		var doc transcriptAsOfT
		decode(t, body, &doc)
		if len(doc.Messages) != want.count || doc.Badge != want.badge || doc.CompactedAt != nil ||
			doc.Messages[len(doc.Messages)-1].Role != core.RoleTool {
			t.Errorf("%s: transcript?step=2 = %d messages, badge %q; want %d ending in the refund's result, badge %q", shape, len(doc.Messages), doc.Badge, want.count, want.badge)
		}
	}
	for _, c := range []struct {
		run  string
		from int
		code int
	}{
		{"r_failed", 2, http.StatusAccepted},
		{"r_budget", 2, http.StatusAccepted},
		{"r_failed", 3, http.StatusBadRequest},
		{"r_done", 3, http.StatusBadRequest},
	} {
		code, out := pt.post(t, fmt.Sprintf(`{"runtime":"rt_test","agent":"acme-support","source":{"run_id":%q,"from_step":%d},`+
			`"engine":"live","side_effects":"substitute","thread":"ephemeral","transcript_edits":[{"step":1,"call_id":"c2","tool_result":"refunded"}]}`, c.run, c.from))
		if code != c.code || (c.code == http.StatusBadRequest && !strings.Contains(out, "beyond the source run's last step")) {
			t.Errorf("%s from_step %d = %d %s, want %d", c.run, c.from, code, strings.TrimSpace(out), c.code)
		}
	}
	// The scripted engine has nothing recorded for the step the source
	// never answered: 400 in weft/runtime's words; from a recorded step
	// it is accepted.
	for from, want := range map[int]int{2: http.StatusBadRequest, 1: http.StatusAccepted} {
		code, out := pt.post(t, fmt.Sprintf(`{"runtime":"rt_test","agent":"acme-support","source":{"run_id":"r_failed","from_step":%d},`+
			`"engine":"scripted","side_effects":"substitute","thread":"ephemeral"}`, from))
		if code != want || (want == http.StatusBadRequest && !strings.Contains(out, "the scripted engine has no recorded turn for step 2: the source never answered it (use engine live)")) {
			t.Errorf("scripted from_step %d = %d %s, want %d", from, code, strings.TrimSpace(out), want)
		}
	}
}
