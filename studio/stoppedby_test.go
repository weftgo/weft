package studio

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/weftgo/weft/obsdb/sqlite"
)

// TestStoppedByOnTheRunRow pins U3.2's API half: every run row (list,
// search, the run document) carries stopped_by from its run_finish, and
// cancel_cause beside it, both omitted when empty; ?stopped_by= filters
// /api/runs and /api/search exactly, or by prefix for a value ending in
// ':' (docs/record-contract.md §6).
func TestStoppedByOnTheRunRow(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	finish := func(id, by string) string {
		return `{"type":"run_finish","run_id":"` + id + `","steps":2,"stopped_by":"` + by + `"}`
	}
	seedRun(t, db, "r_submit", "pub_a", "", finish("r_submit", "has_tool_call:submit"), nil)
	seedRun(t, db, "r_steps", "pub_a", "", finish("r_steps", "step_count_is:2"), nil)
	seedRun(t, db, "r_plain", "pub_a", "", `{"type":"run_finish","run_id":"r_plain","steps":1}`, nil)
	h := Handler(DB(db))

	ids := func(path string) string {
		t.Helper()
		code, _, body := get(t, h, path)
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, code, body)
		}
		var page struct {
			Runs []map[string]any `json:"runs"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range page.Runs {
			out = append(out, r["id"].(string)+"="+strOf(r["stopped_by"]))
			if _, ok := r["cancel_cause"]; ok {
				t.Errorf("GET %s: %s carries cancel_cause, want it omitted when empty", path, r["id"])
			}
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	for path, want := range map[string]string{
		"/studio/api/runs":                                   "r_plain=,r_steps=step_count_is:2,r_submit=has_tool_call:submit",
		"/studio/api/runs?stopped_by=has_tool_call:":         "r_submit=has_tool_call:submit",
		"/studio/api/runs?stopped_by=has_tool_call:submit":   "r_submit=has_tool_call:submit",
		"/studio/api/runs?stopped_by=step_count_is:2":        "r_steps=step_count_is:2",
		"/studio/api/runs?stopped_by=has_tool_call":          "",
		"/studio/api/search?stopped_by=has_tool_call:":       "r_submit=has_tool_call:submit",
		"/studio/api/search?stopped_by=step_count_is:2&q=do": "r_steps=step_count_is:2",
	} {
		if got := ids(path); got != want {
			t.Errorf("GET %s = %q, want %q", path, got, want)
		}
	}
	_, _, body := get(t, h, "/studio/api/runs/r_submit")
	if !strings.Contains(body, `"stopped_by":"has_tool_call:submit"`) {
		t.Errorf("run document lacks stopped_by: %s", body)
	}
	_, _, body = get(t, h, "/studio/api/runs/r_plain")
	if strings.Contains(body, `"stopped_by"`) {
		t.Errorf("run document of a run with none carries stopped_by: %s", body)
	}
}

func strOf(v any) string {
	s, _ := v.(string)
	return s
}
