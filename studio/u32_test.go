package studio

import (
	"strings"
	"testing"
)

// The record contract's retries_taps fixture through Studio's own
// ingest (U32.1): the run document carries tap_panics (UX-E-51) and the
// truncated hole with cause metadata_cap's counted words (UX-E-42); the
// requests route serves the prompt record's segments (UX-S2-11); the
// spans carry the retry count and budget the story's pill reads
// (UX-S2-10).
func TestRetriesTapsFixture(t *testing.T) {
	srv := New(Open(t.TempDir() + "/u32.db"))
	t.Cleanup(func() { _ = srv.Close() })
	h := srv.Handler()
	ingestFile(t, h, "/studio/v1/traces", "../obsdb/testdata/contract/retries_taps/traces.json")
	ingestFile(t, h, "/studio/v1/logs", "../obsdb/testdata/contract/retries_taps/logs.json")
	_, _, runs := get(t, h, "/studio/api/runs")
	var list struct {
		Runs []struct {
			ID        string `json:"id"`
			TapPanics int64  `json:"tap_panics"`
		} `json:"runs"`
	}
	decode(t, runs, &list)
	if len(list.Runs) != 1 || list.Runs[0].TapPanics != 1 {
		t.Fatalf("runs = %s, want one run with tap_panics 1", runs)
	}
	id := list.Runs[0].ID
	_, _, body := get(t, h, "/studio/api/runs/"+id)
	var doc struct {
		TapPanics       int64 `json:"tap_panics"`
		MetadataDropped int64 `json:"metadata_dropped"`
		Holes           []struct {
			Hole, Reason, Fix string
		} `json:"holes"`
	}
	decode(t, body, &doc)
	if doc.TapPanics != 1 || doc.MetadataDropped != 0 {
		t.Errorf("doc tap_panics %d, metadata_dropped %d; want 1 and no counter on the wire", doc.TapPanics, doc.MetadataDropped)
	}
	found := false
	for _, hl := range doc.Holes {
		if hl.Hole == "truncated" && strings.HasPrefix(hl.Reason, "1 metadata pair dropped (limit 64 keys, 128-byte keys, 1 KiB values)") &&
			strings.Contains(hl.Fix, "weft.Metadata") {
			found = true
		}
	}
	if !found {
		t.Errorf("holes = %+v, want truncated with the metadata_cap words", doc.Holes)
	}
	_, _, reqs := get(t, h, "/studio/api/runs/"+id+"/requests")
	if !strings.Contains(reqs, `"segments":[{"source":"instructions","text":"You are a support agent."},{"source":"tool:flaky","text":"Call flaky for orders; it may ask you to retry."}]`) {
		t.Errorf("requests = %s, want the prompt's segments", reqs)
	}
	_, _, spans := get(t, h, "/studio/api/runs/"+id+"/spans")
	for _, k := range []string{`"weft.tool.retry":2`, `"weft.tool.retry_budget":1`, `"weft.tap.panics":1`} {
		if !strings.Contains(spans, k) {
			t.Errorf("spans lack %s", k)
		}
	}
}
