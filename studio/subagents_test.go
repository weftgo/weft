package studio

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/weftgo/weft/otel"
)

// TestSubagentsOnThePage pins plan A10's share of the record over the
// subagent example run through the real pipeline (recordStepsRun: the
// research Subagent at step 1): the runs list is top-level only by
// default, parent=<run id> lists the child with its parent linkage,
// all=1 lists both; the parent's document names the child; and the
// child's own request record is read by the child's id — its prompt,
// not the parent's (requests-child.golden.json, the web's fixture for
// the child's Request on the parent's page).
func TestSubagentsOnThePage(t *testing.T) {
	ts, _ := requestsServer(t)
	recordStepsRun(t, ts.URL, "r_steps", nil)
	const kid = "r_steps/1/c_sub"
	fetchJSON(t, ts, "/api/runs/r_steps", func(b string) bool {
		return strings.Contains(b, `"request_count":6`) && strings.Contains(b, `"id":"`+kid+`"`)
	})
	fetchJSON(t, ts, "/api/runs/"+kid, func(b string) bool { return strings.Contains(b, `"request_count":1`) })

	type row struct {
		ID           string `json:"id"`
		ParentRunID  string `json:"parent_run_id"`
		ParentCallID string `json:"parent_call_id"`
		Agent        string `json:"agent"`
	}
	list := func(q string) []row {
		t.Helper()
		var page struct {
			Total int   `json:"total"`
			Runs  []row `json:"runs"`
		}
		decode(t, fetchJSON(t, ts, "/api/runs"+q, nil), &page)
		if page.Total != len(page.Runs) {
			t.Errorf("runs%s total %d, %d rows", q, page.Total, len(page.Runs))
		}
		return page.Runs
	}
	if got := list(""); len(got) != 1 || got[0].ID != "r_steps" {
		t.Errorf("runs (default) = %+v, want the parent alone: top-level only", got)
	}
	if got := list("?parent=r_steps"); len(got) != 1 || got[0] != (row{kid, "r_steps", "c_sub", "researcher"}) {
		t.Errorf("runs?parent=r_steps = %+v, want the researcher's run linked to r_steps/c_sub", got)
	}
	for _, q := range []string{"?all=1", "?parent=*"} {
		got := list(q)
		ids := []string{}
		for _, r := range got {
			ids = append(ids, r.ID)
		}
		if len(got) != 2 || !strings.Contains(strings.Join(ids, ","), kid) || !strings.Contains(strings.Join(ids, ","), "r_steps") {
			t.Errorf("runs%s = %v, want the parent and the child", q, ids)
		}
	}

	// The child's prompt by the child's id, never the parent's.
	body := fetchJSON(t, ts, "/api/runs/"+kid+"/requests", nil)
	requestsGolden(t, "requests-child.golden.json", body)
	var page requestsDoc
	decode(t, body, &page)
	if len(page.Requests) != 1 || page.Requests[0].Step != 0 || page.Badge != "" {
		t.Fatalf("child requests = %+v, want its one step-0 row", page)
	}
	var p struct{ Text string }
	if err := json.Unmarshal(page.Requests[0].PromptRaw, &p); err != nil || p.Text != "You research orders." {
		t.Errorf("child prompt = %s (%v), want the child's own instructions", page.Requests[0].PromptRaw, err)
	}
	if strings.Contains(body, "You are a support agent.") {
		t.Error("the child's request record carries the parent's prompt")
	}
	decode(t, fetchJSON(t, ts, "/api/runs/"+kid+"/requests?step=0", nil), &page)
	if len(page.Requests) != 1 || page.Requests[0].Step != 0 {
		t.Errorf("child requests?step=0 = %+v, want the one row", page.Requests)
	}
}

// TestChildRowHoles: the run document's children[] rows carry each
// child's own holes (runHoles, plan A10), so the parent's page badges a
// content-off child before the child's document is read; a child with
// none carries no holes field (list rows never do). The content-off
// parent's document is the golden run-children-holes.golden.json.
func TestChildRowHoles(t *testing.T) {
	ts, _ := requestsServer(t)
	recordStepsRun(t, ts.URL, "r_off", nil, otel.NoContent())
	recordStepsRun(t, ts.URL, "r_on", nil)
	type kid struct {
		ID    string `json:"id"`
		Holes []struct {
			Hole, Reason, Fix string
		} `json:"holes"`
	}
	read := func(id string) (string, []kid) {
		body := fetchJSON(t, ts, "/api/runs/"+id, func(b string) bool {
			return strings.Contains(b, `"request_count":6`) && strings.Contains(b, `"id":"`+id+`/1/c_sub"`)
		})
		var doc struct {
			Children []kid `json:"children"`
		}
		decode(t, body, &doc)
		return body, doc.Children
	}
	body, off := read("r_off")
	if len(off) != 1 || len(off[0].Holes) == 0 || off[0].Holes[0].Hole != "stripped" || off[0].Holes[0].Reason == "" {
		t.Errorf("content-off child row = %+v, want its stripped hole with a reason", off)
	}
	stepGolden(t, "run-children-holes.golden.json", regexp.MustCompile(`"(last_seen|trace_id)": ?"[^"]*"`).ReplaceAllString(body, `"$1":"(norm)"`))
	_, on := read("r_on")
	if len(on) != 1 || on[0].Holes != nil {
		t.Errorf("content-on child row = %+v, want no holes field", on)
	}
	if list := fetchJSON(t, ts, "/api/runs?all=1", nil); strings.Contains(list, `"holes"`) {
		t.Errorf("list rows carry holes: %s", list)
	}
}
