package studio

import (
	"strings"
	"testing"
)

// The record contract's resumed fixture through Studio's own ingest
// (U13.1, UX-E-17) — rw.support-desk's shape, a parked run resumed by a
// GenerateAs with Approve, Resolve and core.ResumeOf: the list and the
// run document serve resumed_by on the parked run and resumed_from on
// the resume (the status word's seam, lib/status.ts); the actor rides
// the resume's meta; the resume's spans carry weft.resume.calls. Then
// the approval_denied fixture: its denied calls' zero-length spans
// carry weft.tool.denied and their kind, and the row's denied counter
// counts each call once.
func TestResumeLinkFixtures(t *testing.T) {
	srv := New(Open(t.TempDir() + "/u13.db"))
	t.Cleanup(func() { _ = srv.Close() })
	h := srv.Handler()
	ingestFile(t, h, "/studio/v1/traces", "../obsdb/testdata/contract/resumed/traces.json")
	ingestFile(t, h, "/studio/v1/logs", "../obsdb/testdata/contract/resumed/logs.json")
	_, _, runs := get(t, h, "/studio/api/runs")
	var list struct {
		Runs []struct {
			ID          string            `json:"id"`
			Status      string            `json:"status"`
			Pending     int               `json:"pending"`
			ResumedFrom string            `json:"resumed_from"`
			ResumedBy   string            `json:"resumed_by"`
			Meta        map[string]string `json:"meta"`
		} `json:"runs"`
	}
	decode(t, runs, &list)
	if len(list.Runs) != 2 {
		t.Fatalf("runs = %s, want the parked run and its resume", runs)
	}
	resume, parked := list.Runs[0], list.Runs[1] // newest first
	if parked.Pending != 2 || parked.ResumedBy != resume.ID || parked.ResumedFrom != "" {
		t.Errorf("parked row = %+v, want pending 2, resumed_by %s", parked, resume.ID)
	}
	if resume.ResumedFrom != parked.ID || resume.ResumedBy != "" || resume.Meta["weft.approval.actor"] != "op_42" {
		t.Errorf("resume row = %+v, want resumed_from %s and the actor in meta", resume, parked.ID)
	}
	_, _, doc := get(t, h, "/studio/api/runs/"+parked.ID)
	if !strings.Contains(doc, `"resumed_by":"`+resume.ID+`"`) {
		t.Errorf("parked run document lacks resumed_by: %s", doc)
	}
	_, _, spans := get(t, h, "/studio/api/runs/"+resume.ID+"/spans")
	if !strings.Contains(spans, `"weft.resume.calls":"call_1=approved,call_2=resolved"`) ||
		!strings.Contains(spans, `"weft.resumed_from":"`+parked.ID+`"`) {
		t.Errorf("resume spans lack the link: %s", spans)
	}

	srv2 := New(Open(t.TempDir() + "/u13d.db"))
	t.Cleanup(func() { _ = srv2.Close() })
	h2 := srv2.Handler()
	ingestFile(t, h2, "/studio/v1/traces", "../obsdb/testdata/contract/approval_denied/traces.json")
	ingestFile(t, h2, "/studio/v1/logs", "../obsdb/testdata/contract/approval_denied/logs.json")
	_, _, runs2 := get(t, h2, "/studio/api/runs")
	var list2 struct {
		Runs []struct {
			ID     string `json:"id"`
			Denied int64  `json:"denied"`
		} `json:"runs"`
	}
	decode(t, runs2, &list2)
	if len(list2.Runs) != 2 || list2.Runs[0].Denied != 2 {
		t.Fatalf("runs = %s, want the resume with denied 2", runs2)
	}
	_, _, spans2 := get(t, h2, "/studio/api/runs/"+list2.Runs[0].ID+"/spans")
	for _, k := range []string{`"weft.tool.denied":true`, `"weft.tool.denied_kind":"denied"`, `"weft.tool.denied_kind":"no_decision"`} {
		if !strings.Contains(spans2, k) {
			t.Errorf("denied spans lack %s", k)
		}
	}
}
