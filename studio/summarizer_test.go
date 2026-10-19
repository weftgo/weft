package studio

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

// TestTimelineSummarizerCost (U15.1, UX-B-13): the session timeline
// prices thread's summarizer calls — never in a turn's usage — each
// mark's at its summarizer_model's rates when recorded, else at its
// turn's model's — an estimate, said so; the session's figure adds what of SummarizerUsage no mark names
// (a call past the cap, a summary without a marker) at each span
// model's rates (the newest turn's, an estimate, when the spans named
// none); an unpriced part makes the figure not known, its reason and
// model kept.
func TestTimelineSummarizerCost(t *testing.T) {
	s := New(DB(fixtureDB(t)))
	t.Cleanup(func() { _ = s.Close() })
	run := func(id, provider, model string) obsdb.RunRow {
		return obsdb.RunRow{ID: id, Provider: provider, Model: model}
	}
	str := func(v string) *string { return &v }
	mark := func(runID *string, fields map[string]any) sessionEntry {
		return sessionEntry{Kind: "compaction", RunID: runID, Fields: fields}
	}
	usage := map[string]any{"input_tokens": float64(1_000_000), "output_tokens": float64(0)}
	det := obsdb.SessionDetail{
		SessionRow: obsdb.SessionRow{ID: "s", SummarizerUsage: core.Usage{InputTokens: 3_000_000}},
		Runs:       []obsdb.RunRow{run("s-t1", "openai", "gpt-4o-mini"), run("s-t2", "openai", "gpt-4o")},
	}
	doc := timelineDoc{
		Turns: []timelineTurn{{RunID: "s-t1"}, {RunID: "s-t2"}},
		Marks: []sessionEntry{
			// t1's at its own recorded summarizer model (gpt-4o-mini: $0.15 / 1M in).
			mark(str("s-t1"), map[string]any{"summarizer_usage": usage,
				"summarizer_model": map[string]any{"provider": "openai", "name": "gpt-4o-mini"}}),
			// t2's names no model: the turn's (gpt-4o: $2.50 / 1M in).
			mark(str("s-t2"), map[string]any{"summarizer_usage": usage}),
			// A mark without a summarizer call is no part of it.
			mark(str("s-t2"), map[string]any{"replaced": float64(3)}),
		},
	}
	s.priceSummarizer(&doc, det)
	if doc.Turns[0].Summarizer == nil || doc.Turns[1].Summarizer == nil {
		t.Fatalf("turn figures = %+v, %+v", doc.Turns[0].Summarizer, doc.Turns[1].Summarizer)
	}
	if got := *doc.Turns[0].Summarizer.CostUSD; got != 0.15 {
		t.Errorf("t1's summarizer = %v, want 0.15 (its recorded model)", got)
	}
	if got := *doc.Turns[1].Summarizer.CostUSD; got != 2.5 {
		t.Errorf("t2's summarizer = %v, want 2.5 (the turn's model)", got)
	}
	// The turn's model is a stand-in: the figure is known, an estimate,
	// and says why (U15.1-fix).
	if f := doc.Turns[1].Summarizer; !f.CostKnown || f.CostBadge != costEstimate || f.CostReason != estimateReason {
		t.Errorf("t2's summarizer = %+v, want a known estimate", f)
	}
	if f := doc.Turns[0].Summarizer; f.CostBadge != "" {
		t.Errorf("t1's summarizer (its recorded model) = %+v, want no badge", f)
	}
	// The whole: both marks plus the 1M input no mark names, at the
	// newest turn's model (gpt-4o).
	if doc.Summarizer == nil || doc.Summarizer.Usage.InputTokens != 3_000_000 {
		t.Fatalf("session figure = %+v", doc.Summarizer)
	}
	if got := *doc.Summarizer.CostUSD; got != 0.15+2.5+2.5 {
		t.Errorf("session summarizer = %v, want 5.15", got)
	}
	if doc.Summarizer.CostBadge != costEstimate || doc.Summarizer.CostReason != estimateReason {
		t.Errorf("session summarizer = %+v, want an estimate (parts at a stand-in model)", doc.Summarizer)
	}

	// The spans carry the model (obsdb.SessionRow.SummarizerModels): a
	// mark that recorded none stays an estimate at its turn's model, and
	// the rest no mark names is priced at each span model's own rates.
	spans := det
	// Two span models: an unmarked remainder priced per model — 1M at
	// gpt-4o-mini (0.15) and 2M at gpt-4o (5.00) beside t1's marked 1M.
	spans.SummarizerModels = []obsdb.ModelUsage{
		{Provider: "openai", Model: "gpt-4o", Usage: core.Usage{InputTokens: 2_000_000}},
		{Provider: "openai", Model: "gpt-4o-mini", Usage: core.Usage{InputTokens: 2_000_000}},
	}
	spans.SummarizerUsage = core.Usage{InputTokens: 4_000_000}
	doc = timelineDoc{
		Turns: []timelineTurn{{RunID: "s-t1"}, {RunID: "s-t2"}},
		Marks: []sessionEntry{mark(str("s-t1"), map[string]any{"summarizer_usage": usage,
			"summarizer_model": map[string]any{"provider": "openai", "name": "gpt-4o-mini"}})},
	}
	s.priceSummarizer(&doc, spans)
	if f := doc.Summarizer; f == nil || f.Usage.InputTokens != 4_000_000 || *f.CostUSD != 0.15+0.15+5 || f.CostBadge != "" {
		t.Errorf("session summarizer per span model = %+v, want 5.30 exact", f)
	}

	// An unpriced summarizer model: the turn's and the session's figure
	// are not known, the model named.
	doc = timelineDoc{
		Turns: []timelineTurn{{RunID: "s-t1"}},
		Marks: []sessionEntry{mark(str("s-t1"), map[string]any{"summarizer_usage": usage,
			"summarizer_model": map[string]any{"provider": "acme", "name": "x"}})},
	}
	s.priceSummarizer(&doc, obsdb.SessionDetail{Runs: det.Runs[:1]})
	for _, f := range []*summarizerCost{doc.Turns[0].Summarizer, doc.Summarizer} {
		if f == nil || f.CostKnown || f.CostUSD != nil || f.CostBadge != costUnpriced || len(f.UnpricedModels) != 1 || f.UnpricedModels[0] != "acme/x" {
			t.Errorf("unpriced figure = %+v", f)
		}
	}

	// Nothing summarized: no figure at all.
	doc = timelineDoc{Turns: []timelineTurn{{RunID: "s-t1"}}}
	s.priceSummarizer(&doc, obsdb.SessionDetail{Runs: det.Runs[:1]})
	if doc.Summarizer != nil || doc.Turns[0].Summarizer != nil {
		t.Errorf("no summarizer call, figures = %+v, %+v", doc.Summarizer, doc.Turns[0].Summarizer)
	}
}

// TestRunDocSummarizer (U15.1-fix, UX-B-13): a session turn's run
// document carries what the summarizer calls filed under it cost — the
// timeline's turn figure, from the session events filed under that run
// alone — and a turn that summarized nothing carries none.
func TestRunDocSummarizer(t *testing.T) {
	h := Handler(DB(timelineDB(t)))
	read := func(id string) runPage {
		t.Helper()
		code, _, body := get(t, h, "/studio/api/runs/"+id)
		if code != http.StatusOK {
			t.Fatalf("run %s: %d %s", id, code, body)
		}
		var p runPage
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	f := read("s_acme-t4").Summarizer
	if f == nil || f.Usage.InputTokens != 1800 || f.Usage.OutputTokens != 60 || f.CostUSD == nil || *f.CostUSD != 0.000306 || f.CostBadge != "" {
		t.Errorf("t4's summarizer = %+v, want 1800/60 at gpt-4o-mini, $0.000306 exact", f)
	}
	if f := read("s_acme-t1").Summarizer; f != nil {
		t.Errorf("t1's summarizer = %+v, want none", f)
	}
}
