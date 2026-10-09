package studio

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
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
