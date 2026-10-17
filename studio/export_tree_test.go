package studio

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// TestExportTree pins &tree=1 (UX-A-31): a delegation exports whole —
// json carries every subagent run's own document under "subagents"
// (with "subagent_holes" [] when the tree is whole), jsonl each run's
// lines after its own "run" record, otlp every run in the same two
// requests, which re-ingest as the whole tree; the file is named
// <run>.tree.<ext>; wefttest refuses tree, a malformed tree is a 400.
func TestExportTree(t *testing.T) {
	ts, _ := requestsServer(t)
	recordStepsRun(t, ts.URL, "r_tree", nil)
	const child = "r_tree/1/c_sub"
	resp, body := exportGet(t, ts, "/api/runs/r_tree/export?format=json&tree=1", settled("r_tree"))
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="r_tree.tree.json"` {
		t.Errorf("Content-Disposition = %q, want r_tree.tree.json", cd)
	}
	var d struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
		Subagents []struct {
			Run struct {
				ID          string `json:"id"`
				ParentRunID string `json:"parent_run_id"`
			} `json:"run"`
			Events struct {
				Events []json.RawMessage `json:"events"`
			} `json:"events"`
			Spans []json.RawMessage `json:"spans"`
		} `json:"subagents"`
		SubagentHoles []json.RawMessage `json:"subagent_holes"`
	}
	decode(t, string(body), &d)
	if d.Run.ID != "r_tree" || len(d.Subagents) != 1 || d.Subagents[0].Run.ID != child || d.Subagents[0].Run.ParentRunID != "r_tree" {
		t.Fatalf("tree export = run %q, subagents %+v; want r_tree with %s under it", d.Run.ID, d.Subagents, child)
	}
	if len(d.Subagents[0].Events.Events) == 0 || len(d.Subagents[0].Spans) == 0 {
		t.Errorf("the subagent's export has %d events, %d spans; want its own records", len(d.Subagents[0].Events.Events), len(d.Subagents[0].Spans))
	}
	if d.SubagentHoles == nil || len(d.SubagentHoles) != 0 {
		t.Errorf("subagent_holes = %v, want [] for a whole tree", d.SubagentHoles)
	}
	// Without tree the document is unchanged: no subagents key.
	_, plain := exportGet(t, ts, "/api/runs/r_tree/export?format=json", nil)
	if strings.Contains(string(plain), `"subagents"`) {
		t.Error("a plain json export carries subagents")
	}

	_, lines := exportGet(t, ts, "/api/runs/r_tree/export?format=jsonl&tree=1", nil)
	var runs []string
	for _, l := range strings.Split(strings.TrimSpace(string(lines)), "\n") {
		var rec struct {
			Record string `json:"record"`
			Run    struct {
				ID string `json:"id"`
			} `json:"run"`
		}
		decode(t, l, &rec)
		if rec.Record == "run" {
			runs = append(runs, rec.Run.ID)
		}
	}
	if !slices.Equal(runs, []string{"r_tree", child}) {
		t.Errorf("jsonl run records = %v, want the run then its subagent", runs)
	}

	_, otlpBody := exportGet(t, ts, "/api/runs/r_tree/export?format=otlp&tree=1", nil)
	var parts struct {
		Logs   json.RawMessage `json:"logs"`
		Traces json.RawMessage `json:"traces"`
	}
	decode(t, string(otlpBody), &parts)
	fresh, _ := requestsServer(t)
	for path, part := range map[string]json.RawMessage{"/v1/logs": parts.Logs, "/v1/traces": parts.Traces} {
		resp, err := http.Post(fresh.URL+path, "application/json", bytes.NewReader(part))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST %s = %d", path, resp.StatusCode)
		}
	}
	// The subagent run re-ingested beside its parent.
	exportGet(t, fresh, "/api/runs/"+child+"/export?format=json", nil)

	for path, want := range map[string]int{
		"/api/runs/r_tree/export?format=wefttest&tree=1": http.StatusBadRequest,
		"/api/runs/r_tree/export?format=json&tree=maybe": http.StatusBadRequest,
		"/api/runs/r_tree/export?format=json&tree=0":     http.StatusOK,
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s = %d, want %d", path, resp.StatusCode, want)
		}
	}
}
