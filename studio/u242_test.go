package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb/sqlite"
)

// usageLimitRuns is the U24.2 fixture: seven researcher runs that hit
// the usage limit at different steps over different token counts — one
// failure class — beside a loop detection and a success.
func usageLimitRuns() []statsRun {
	out := []statsRun{}
	for i := range 7 {
		step := i % 3
		steps := make([]statsStep, step+1)
		for s := range steps {
			steps[s] = statsStep{in: int64(100 + 10*i), out: int64(40 + i), ttft: 300, dur: 400 * time.Millisecond}
		}
		out = append(out, statsRun{
			id: fmt.Sprintf("ul_%d", i), agent: "researcher", provider: "zai", model: "glm-5.3-flash",
			at: time.Duration(i+1) * time.Hour, dur: 2 * time.Second, errType: "usage_limit",
			err:   fmt.Sprintf("weft: run failed at step %d: weft: run exceeded its usage limit (input %d/250, output %d/0)", step, 260+i, 40+i),
			steps: steps,
		})
	}
	out = append(out,
		statsRun{id: "ul_loop", agent: "researcher", provider: "zai", model: "glm-5.3-flash", at: 9 * time.Hour, dur: time.Second,
			errType: "loop_detected", err: arenaLoop, steps: []statsStep{{in: 5, out: 2, dur: time.Second}}},
		statsRun{id: "ul_ok", agent: "researcher", provider: "zai", model: "glm-5.3-flash", at: 10 * time.Hour, dur: time.Second,
			steps: []statsStep{{in: 5, out: 2, dur: time.Second}}},
	)
	return out
}

// TestUsageLimitOneFacetOneMatrix is U24.2's Done line through the real
// routes: the seven usage-limit failures are one error_class facet of 7,
// the class filters /api/search to exactly them, and one matrix over the
// seven — each row its own source run from its own failing step, the
// models as variants, a pass predicate — saves and reads back whole
// (no cell is run: the live cells are never run in a test). The facet
// page is the golden the runs list's facet test reads.
func TestUsageLimitOneFacetOneMatrix(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, r := range usageLimitRuns() {
		if err := db.Write(context.Background(), r.batch()); err != nil {
			t.Fatal(err)
		}
	}
	h := Handler(DB(db), Playground(true))
	code, _, body := get(t, h, "/studio/api/search?status=failed&agent=researcher")
	if code != http.StatusOK {
		t.Fatalf("search: %d %s", code, body)
	}
	if !strings.Contains(body, `"facets":{"error_class":[{"class":"`+usageClass+`","count":7},`) {
		t.Errorf("facet: want the usage-limit class first with 7 runs:\n%s", body)
	}
	golden(t, "search-usage-limit.golden.json", body)

	ids, total, _ := searchIDs(t, h, "/studio/api/search?status=failed&agent=researcher&error_class="+url.QueryEscape(usageClass))
	if total != 7 || len(ids) != 7 {
		t.Fatalf("search?error_class=<usage limit> = %v (total %d), want the 7 runs", ids, total)
	}

	// One matrix over the seven: rows = source runs, each from its own
	// failing step (lib/matrix.ts's failingStep reads "at step N").
	type input struct {
		Key         string `json:"key"`
		SourceRunID string `json:"source_run_id"`
		FromStep    int    `json:"from_step"`
	}
	inputs := []input{}
	for i, id := range ids {
		var n int
		if _, err := fmt.Sscanf(strings.TrimPrefix(id, "ul_"), "%d", &n); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, input{Key: fmt.Sprint(i + 1), SourceRunID: id, FromStep: n % 3})
	}
	def, _ := json.Marshal(map[string]any{
		"id": "ux-C-cheapest", "name": "cheapest model passing", "agent": "researcher",
		"variants": []map[string]any{
			{"key": "glm-5.3-flash", "overrides": map[string]any{"model": "zai/glm-5.3-flash"}, "engine": "live"},
			{"key": "kimi-k2.6", "overrides": map[string]any{"model": "kimi/kimi-k2.6"}, "engine": "live"},
		},
		"inputs":         inputs,
		"engine":         "live",
		"pass_predicate": `status = succeeded AND answer ~ /\d+ units/`,
	})
	if code, _, b := post(t, h, "/studio/api/experiments", "application/json", string(def)); code != http.StatusOK {
		t.Fatalf("save the matrix: %d %s", code, b)
	}
	code, _, body = get(t, h, "/studio/api/experiments/ux-C-cheapest")
	if code != http.StatusOK {
		t.Fatalf("read the matrix: %d %s", code, body)
	}
	var got struct {
		Inputs []struct {
			SourceRunID string `json:"source_run_id"`
			FromStep    *int   `json:"from_step"`
		} `json:"inputs"`
		PassPredicate string `json:"pass_predicate"`
		RunCount      int    `json:"run_count"`
	}
	decode(t, body, &got)
	if len(got.Inputs) != 7 || got.PassPredicate != `status = succeeded AND answer ~ /\d+ units/` || got.RunCount != 0 {
		t.Fatalf("matrix = %+v, want 7 rows, the predicate verbatim and no run", got)
	}
	for i, in := range got.Inputs {
		if in.SourceRunID != inputs[i].SourceRunID || in.FromStep == nil || *in.FromStep != inputs[i].FromStep {
			t.Errorf("row %d = %+v, want %s from step %d", i, in, inputs[i].SourceRunID, inputs[i].FromStep)
		}
	}
}
