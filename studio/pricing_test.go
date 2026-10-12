package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/studio/internal/pricingdata"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/pool"
)

// TestPricingJSONIsGenerated: the committed pricing.json is exactly the
// generator's output (go generate ./studio/... — the weft.json golden
// pattern), and the generator is deterministic, so a second run
// changes nothing.
func TestPricingJSONIsGenerated(t *testing.T) {
	want, err := pricingdata.JSON()
	if err != nil {
		t.Fatal(err)
	}
	again, err := pricingdata.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, again) {
		t.Fatal("pricingdata.JSON is not deterministic: go generate would not be idempotent")
	}
	got, err := os.ReadFile("pricing.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("studio/pricing.json is stale: edit studio/internal/pricingdata, then run go generate ./studio/... and commit both")
	}
}

// TestDefaultPricingLoads: the shipped table loads (DefaultPricing
// panics otherwise), carries its date, and no row is shadowed by an
// earlier one — every literal model id is matched by its own row first.
func TestDefaultPricingLoads(t *testing.T) {
	d := DefaultPricing()
	if d.source != pricingDefault || d.Dated != pricingdata.Dated || len(d.Models) != len(pricingdata.Models) || d.Note == "" {
		t.Fatalf("DefaultPricing = source %q dated %q %d models, want default, %s, %d and a note",
			d.source, d.Dated, len(d.Models), pricingdata.Dated, len(pricingdata.Models))
	}
	for i, m := range d.Models {
		if strings.ContainsAny(m.ModelGlob, "*?[\\") {
			continue
		}
		got, ok := d.lookup(m.Provider, m.ModelGlob)
		if !ok || got.ModelGlob != m.ModelGlob || got.Provider != m.Provider {
			t.Errorf("row %d (%s/%s) is shadowed by %s/%s", i, m.Provider, m.ModelGlob, got.Provider, got.ModelGlob)
		}
	}
	if _, ok := d.lookup("anthropic", "claude-haiku-4-5-20251001"); !ok {
		t.Error("a dated claude-haiku-4-5 snapshot is not priced")
	}
	if _, ok := d.lookup("openai", "gpt-5.9"); ok {
		t.Error("an unknown future model inherits a price")
	}
}

// costUsage writes one run: run_start naming provider/model, a
// step_start/step_finish pair per step usage, run_finish with total
// (the core's RunResult.Usage: the steps plus every child's total).
// parent links a subagent child; session stamps a turn's identity (a
// child inherits it, as core.Metadata does).
func costUsage(t *testing.T, db obsdb.DB, id, parent, session, provider, model string, at time.Time, total core.Usage, steps ...core.Usage) {
	t.Helper()
	var recs []obsdb.Record
	pos := int64(0)
	add := func(evType string, body any) {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		attrs := map[string]any{
			"weft.record": "event", "weft.run.id": id, "gen_ai.agent.name": "billing",
			"weft.event.type": evType, "weft.event.pos": pos,
		}
		if parent != "" {
			attrs["weft.parent.run.id"] = parent
			attrs["weft.parent.call.id"] = "call_" + id[len(id)-1:]
		}
		if session != "" {
			attrs["weft.session.id"] = session
			if parent == "" {
				attrs["weft.turn"] = "1"
			}
		}
		recs = append(recs, obsdb.Record{
			Time: at.Add(time.Duration(pos) * time.Millisecond), EventName: "weft.event", Severity: 9,
			Body: string(b), Service: "svc", Attrs: attrs, Resource: map[string]any{"service.name": "svc"},
		})
		pos++
	}
	add("run_start", map[string]any{"type": "run_start", "id": id, "agent": "billing",
		"model": map[string]string{"provider": provider, "name": model}})
	for i, u := range steps {
		add("step_start", map[string]any{"type": "step_start", "run_id": id, "index": i})
		add("step_finish", map[string]any{"type": "step_finish", "run_id": id, "index": i, "reason": "stop", "usage": u})
	}
	add("run_finish", map[string]any{"type": "run_finish", "run_id": id, "usage": total, "steps": len(steps)})
	if err := db.Write(context.Background(), obsdb.Batch{Records: recs}); err != nil {
		t.Fatal(err)
	}
}

// costRow is the cost block of a run row, as the wire carries it.
type costRow struct {
	ID                  string   `json:"id"`
	CostUSD             *float64 `json:"cost_usd"`
	CostKnown           bool     `json:"cost_known"`
	CostBadge           string   `json:"cost_badge"`
	CostReason          string   `json:"cost_reason"`
	Cache               string   `json:"cache"`
	DelegatedCostUSD    *float64 `json:"delegated_cost_usd"`
	DelegatedCostKnown  bool     `json:"delegated_cost_known"`
	DelegatedCostReason string   `json:"delegated_cost_reason"`
}

func usd(p *float64) string {
	if p == nil {
		return "null"
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

func costDB(t *testing.T) obsdb.DB {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestCostHandCalculation is the Done line's first two clauses (plan
// A5): a run on a model in the default table costs what a hand
// calculation from its usage says, exact with the splits and at the
// input rate (cache "not_reported") without them; a run on an unlisted
// model is null with the unpriced badge and a reason naming it —
// never 0.
func TestCostHandCalculation(t *testing.T) {
	db := costDB(t)
	t0 := time.Now().Add(-time.Hour).UTC()
	// claude-opus-5-5 in the default table: input $4, output $20, cached
	// input $0.20, cache write $5 per 1M tokens; no reasoning rate
	// (reasoning bills at output).
	split := core.Usage{InputTokens: 120_000, CachedInputTokens: 80_000, CacheWriteTokens: 20_000,
		OutputTokens: 9_000, ReasoningTokens: 3_000}
	costUsage(t, db, "r_split", "", "", "anthropic", "claude-opus-5-5", t0, split, split)
	// By hand:
	//   plain input  (120,000 − 80,000 − 20,000) = 20,000 × $4    =  80,000
	//   cached input                              80,000 × $0.20 =  16,000
	//   cache write                               20,000 × $5    = 100,000
	//   plain output (9,000 − 3,000)            =  6,000 × $20   = 120,000
	//   reasoning (at output: no reasoning rate)   3,000 × $20   =  60,000
	//                                                      total = 376,000 ÷ 1,000,000 = $0.376
	const wantSplit = 0.376
	flat := core.Usage{InputTokens: 120_000, OutputTokens: 9_000}
	costUsage(t, db, "r_flat", "", "", "anthropic", "claude-opus-5-5", t0.Add(time.Second), flat, flat)
	// No splits reported: 120,000 × $4 + 9,000 × $20 = 480,000 + 180,000
	// = 660,000 ÷ 1,000,000 = $0.66, cache "not_reported".
	const wantFlat = 0.66
	costUsage(t, db, "r_unpriced", "", "", "acme", "mystery-1", t0.Add(2*time.Second), flat, flat)

	h := Handler(DB(db))
	for _, c := range []struct {
		id, cache string
		want      float64
	}{{"r_split", cacheExact, wantSplit}, {"r_flat", cacheNotReported, wantFlat}} {
		_, _, body := get(t, h, "/studio/api/runs/"+c.id)
		var r costRow
		decode(t, body, &r)
		if r.CostUSD == nil || *r.CostUSD != c.want || !r.CostKnown || r.Cache != c.cache || r.CostBadge != "" ||
			usd(r.DelegatedCostUSD) != "0" || !r.DelegatedCostKnown {
			t.Errorf("%s: cost %s known %v cache %q badge %q, delegated %s; want %v known, cache %s, delegated 0",
				c.id, usd(r.CostUSD), r.CostKnown, r.Cache, r.CostBadge, usd(r.DelegatedCostUSD), c.want, c.cache)
		}
		// The step prices its own model call the same way.
		_, _, body = get(t, h, "/studio/api/runs/"+c.id+"/steps/0")
		var st costRow
		decode(t, body, &st)
		if st.CostUSD == nil || *st.CostUSD != c.want || st.Cache != c.cache {
			t.Errorf("%s step 0: cost %s cache %q, want %v %s", c.id, usd(st.CostUSD), st.Cache, c.want, c.cache)
		}
	}

	_, _, body := get(t, h, "/studio/api/runs/r_unpriced")
	if !strings.Contains(body, `"cost_usd":null`) {
		t.Errorf("an unpriced run's cost_usd is not null: %s", body)
	}
	var r costRow
	decode(t, body, &r)
	if r.CostKnown || r.CostBadge != costUnpriced || !strings.Contains(r.CostReason, "acme/mystery-1") {
		t.Errorf("unpriced run: known %v badge %q reason %q, want unknown, unpriced, a reason naming acme/mystery-1", r.CostKnown, r.CostBadge, r.CostReason)
	}
	_, _, body = get(t, h, "/studio/api/runs/r_unpriced/steps/0")
	var st costRow
	decode(t, body, &st)
	if st.CostUSD != nil || st.CostBadge != costUnpriced || !strings.Contains(st.CostReason, "acme/mystery-1") {
		t.Errorf("unpriced step: %s", body)
	}

	// A reasoning rate of its own, from a table built in Go.
	reasoning := 30.0
	h2 := Handler(DB(db), Pricing(PricingTable{Dated: "2026-10-01", Models: []ModelPrice{
		{Provider: "anthropic", ModelGlob: "claude-*", Input: 4, Output: 20, Reasoning: &reasoning},
	}}))
	_, _, body = get(t, h2, "/studio/api/runs/r_split")
	decode(t, body, &r)
	// No cached_input or cache_write rate: those price at input.
	//   100,000 input tokens (plain 20,000 + cached 80,000) … all 120,000 × $4 = 480,000
	//   6,000 × $20 + 3,000 × $30 = 120,000 + 90,000                           = 210,000
	//                                                       total = 690,000 ÷ 1,000,000 = $0.69
	if r.CostUSD == nil || *r.CostUSD != 0.69 || r.Cache != cacheExact {
		t.Errorf("r_split under a reasoning rate: cost %s cache %q, want 0.69 exact", usd(r.CostUSD), r.Cache)
	}
	_, _, body = get(t, h2, "/studio/api/runs/r_unpriced")
	decode(t, body, &r)
	if r.CostUSD != nil || !strings.Contains(r.CostReason, "option pricing table") {
		t.Errorf("unpriced under a Go table: %s", body)
	}
}

// TestCostSubagents is the Done line's third clause: a parent's own
// cost and its subagents' are two numbers — own is the parent's usage
// minus its children's (the core rolls them up) at its own model's
// rates, delegated the sum of its descendants' own costs, recursively —
// and an unpriced descendant makes delegated null and unknown, with a
// reason naming it, while the parent's own figure stands.
func TestCostSubagents(t *testing.T) {
	db := costDB(t)
	t0 := time.Now().Add(-time.Hour).UTC()
	// p1 (claude-opus-5-5: $4/$20) → p1/0/c1 (gpt-4o: $2.50/$10) →
	// p1/0/c1/0/g (claude-haiku-4-5: $1/$5).
	grand := core.Usage{InputTokens: 1_000, OutputTokens: 100}        // 1,000×1 + 100×5      =  1,500 → $0.0015
	childOwn := core.Usage{InputTokens: 5_000, OutputTokens: 500}     // 5,000×2.5 + 500×10  = 17,500 → $0.0175
	parentOwn := core.Usage{InputTokens: 10_000, OutputTokens: 1_000} // 10,000×4 + 1,000×20 = 60,000 → $0.06
	child := childOwn.Add(grand)
	costUsage(t, db, "p1", "", "s_bill", "anthropic", "claude-opus-5-5", t0, parentOwn.Add(child), parentOwn)
	costUsage(t, db, "p1/0/c1", "p1", "s_bill", "openai", "gpt-4o", t0.Add(time.Second), child, childOwn)
	costUsage(t, db, "p1/0/c1/0/g", "p1/0/c1", "s_bill", "anthropic", "claude-haiku-4-5", t0.Add(2*time.Second), grand, grand)
	// p2 (priced) → p2/0/x (unpriced).
	costUsage(t, db, "p2", "", "", "anthropic", "claude-opus-5-5", t0.Add(3*time.Second), parentOwn.Add(childOwn), parentOwn)
	costUsage(t, db, "p2/0/x", "p2", "", "acme", "mystery-1", t0.Add(4*time.Second), childOwn, childOwn)

	h := Handler(DB(db))
	var doc struct {
		costRow
		Children []costRow `json:"children"`
	}
	_, _, body := get(t, h, "/studio/api/runs/p1")
	decode(t, body, &doc)
	if usd(doc.CostUSD) != "0.06" || usd(doc.DelegatedCostUSD) != "0.019" || !doc.DelegatedCostKnown {
		t.Errorf("p1: own %s delegated %s (known %v), want own 0.06 and delegated 0.0175 + 0.0015 = 0.019",
			usd(doc.CostUSD), usd(doc.DelegatedCostUSD), doc.DelegatedCostKnown)
	}
	if len(doc.Children) != 1 || usd(doc.Children[0].CostUSD) != "0.0175" || usd(doc.Children[0].DelegatedCostUSD) != "0.0015" {
		t.Errorf("p1's children[] = %+v, want p1/0/c1 own 0.0175, delegated 0.0015", doc.Children)
	}
	// The list row says the same as the document.
	_, _, body = get(t, h, "/studio/api/runs")
	var list struct {
		Runs []costRow `json:"runs"`
	}
	decode(t, body, &list)
	for _, r := range list.Runs {
		if r.ID == "p1" && (usd(r.CostUSD) != "0.06" || usd(r.DelegatedCostUSD) != "0.019") {
			t.Errorf("p1's list row: own %s delegated %s", usd(r.CostUSD), usd(r.DelegatedCostUSD))
		}
	}
	// The step's children carry the child's own and delegated figures.
	_, _, body = get(t, h, "/studio/api/runs/p1/steps/0")
	var step struct {
		costRow
		Children []costRow `json:"children"`
	}
	decode(t, body, &step)
	if usd(step.CostUSD) != "0.06" || len(step.Children) != 1 || usd(step.Children[0].CostUSD) != "0.0175" ||
		usd(step.Children[0].DelegatedCostUSD) != "0.0015" {
		t.Errorf("p1 step 0 = own %s, children %+v; want 0.06 and the child 0.0175 + 0.0015", usd(step.CostUSD), step.Children)
	}

	_, _, body = get(t, h, "/studio/api/runs/p2")
	decode(t, body, &doc)
	if usd(doc.CostUSD) != "0.06" || doc.DelegatedCostUSD != nil || doc.DelegatedCostKnown ||
		!strings.Contains(doc.DelegatedCostReason, "p2/0/x") || !strings.Contains(doc.DelegatedCostReason, "acme/mystery-1") {
		t.Errorf("p2: own %s delegated %s known %v reason %q; want own 0.06, delegated null and unknown naming p2/0/x's acme/mystery-1",
			usd(doc.CostUSD), usd(doc.DelegatedCostUSD), doc.DelegatedCostKnown, doc.DelegatedCostReason)
	}

	// The session: its turn's own + delegated (0.06 + 0.019 = 0.079).
	_, _, body = get(t, h, "/studio/api/sessions/s_bill")
	var sess costRow
	decode(t, body, &sess)
	if usd(sess.CostUSD) != "0.079" || !sess.CostKnown {
		t.Errorf("session s_bill: cost %s known %v, want 0.079", usd(sess.CostUSD), sess.CostKnown)
	}
	_, _, body = get(t, h, "/studio/api/sessions")
	var sessions struct {
		Sessions []costRow `json:"sessions"`
	}
	decode(t, body, &sessions)
	if len(sessions.Sessions) != 1 || usd(sessions.Sessions[0].CostUSD) != "0.079" {
		t.Errorf("sessions = %s, want s_bill at 0.079", body)
	}

	// Search by cost: the whole bill (own + delegated) orders the hits —
	// p1 0.079 over p2 (own 0.06, delegated unknown: unpriced, so last)
	// — and min_cost keeps the priced bills at or above it.
	ids := func(path string) (out []string, raw string) {
		code, _, body := get(t, h, path)
		if code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, code, body)
		}
		var page struct {
			Runs []costRow `json:"runs"`
			Sort string    `json:"sort"`
		}
		decode(t, body, &page)
		for _, r := range page.Runs {
			out = append(out, r.ID)
		}
		return out, body
	}
	if got, body := ids("/studio/api/search?sort=cost"); strings.Join(got, ",") != "p1,p2" || !strings.Contains(body, `"sort":"cost"`) ||
		strings.Contains(body, "cost_sort_cap") {
		t.Errorf("sort=cost = %v (%s), want p1 then p2, no cap", got, body)
	}
	if got, _ := ids("/studio/api/search?min_cost=0.07"); strings.Join(got, ",") != "p1" {
		t.Errorf("min_cost=0.07 = %v, want p1 alone (p2's bill is unknown)", got)
	}
	if got, _ := ids("/studio/api/search?min_cost=0.08"); len(got) != 0 {
		t.Errorf("min_cost=0.08 = %v, want none", got)
	}
	if got, _ := ids("/studio/api/search?sort=cost&all=1"); strings.Join(got, ",") != "p1,p1/0/c1,p1/0/c1/0/g,p2/0/x,p2" {
		t.Errorf("sort=cost&all=1 = %v, want the bills 0.079, 0.019, 0.0015, then the unknown ones newest first", got)
	}
}

// TestPricingFile pins WEFT_PRICING's loader: a file replaces the
// default whole, reports source file and its path; a malformed file is
// an error naming the path and the field — and New panics on it rather
// than falling back.
func TestPricingFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write("good.json", `{"dated":"2026-09-01","models":[{"provider":"acme","model_glob":"mystery-*","input":1,"output":2,"cached_input":0.5}]}`)
	tbl, err := LoadPricing(good)
	if err != nil {
		t.Fatal(err)
	}
	if tbl.source != pricingFile || tbl.path != good || len(tbl.Models) != 1 {
		t.Fatalf("LoadPricing = %+v", tbl)
	}
	// Replace, not merge: anthropic is unpriced under the file.
	if _, ok := tbl.lookup("anthropic", "claude-opus-5-5"); ok {
		t.Error("a pricing file merged with the default table; it must replace it")
	}
	if _, ok := tbl.lookup("acme", "mystery-1"); !ok {
		t.Error("the file's own row does not match")
	}

	for _, c := range []struct{ name, body, field string }{
		{"syntax.json", `{"dated":`, "does not parse"},
		{"nodate.json", `{"models":[]}`, "dated: missing"},
		{"baddate.json", `{"dated":"10/10/2026","models":[]}`, "dated: \"10/10/2026\" is not a YYYY-MM-DD date"},
		{"nomodels.json", `{"dated":"2026-10-10"}`, "models: missing"},
		{"unknown.json", `{"dated":"2026-10-10","models":[{"provider":"a","model_glob":"b","input":1,"output":1,"inptu":2}]}`, "inptu: unknown field"},
		{"extends.json", `{"dated":"2026-10-10","extends":"default","models":[]}`, "extends: unknown field"},
		{"noinput.json", `{"dated":"2026-10-10","models":[{"provider":"a","model_glob":"b","output":1}]}`, "models[0].input: missing"},
		{"negative.json", `{"dated":"2026-10-10","models":[{"provider":"a","model_glob":"b","input":1,"output":1,"cache_write":-1}]}`, "models[0].cache_write: -1 is not a non-negative rate"},
		{"badglob.json", `{"dated":"2026-10-10","models":[{"provider":"a","model_glob":"[","input":1,"output":1}]}`, "models[0].model_glob"},
		{"noprovider.json", `{"dated":"2026-10-10","models":[{"model_glob":"b","input":1,"output":1}]}`, "models[0].provider: missing"},
		{"type.json", `{"dated":"2026-10-10","models":[{"provider":"a","model_glob":"b","input":"1","output":1}]}`, "models[0].input: want a float64"},
	} {
		p := write(c.name, c.body)
		_, err := LoadPricing(p)
		if err == nil || !strings.Contains(err.Error(), p) || !strings.Contains(err.Error(), c.field) {
			t.Errorf("LoadPricing(%s) = %v, want an error naming %s and %q", c.name, err, p, c.field)
		}
	}
	if _, err := LoadPricing(filepath.Join(dir, "absent.json")); err == nil || !strings.Contains(err.Error(), "absent.json") {
		t.Errorf("LoadPricing(absent) = %v", err)
	}

	// The settings route's body loads back as a file (source and path
	// are accepted): the export is the override's template.
	h := Handler(DB(costDB(t)), Pricing(tbl))
	code, _, body := get(t, h, "/studio/api/settings/pricing")
	if code != http.StatusOK || !strings.Contains(body, `"source":"file"`) || !strings.Contains(body, `"dated":"2026-09-01"`) {
		t.Fatalf("settings/pricing = %d %s", code, body)
	}
	if _, err := LoadPricing(write("export.json", body)); err != nil {
		t.Errorf("the settings route's body does not load back: %v", err)
	}

	// WEFT_PRICING: New reads it when no Pricing option is given; the
	// option wins over it; a malformed file panics, naming it.
	t.Setenv("WEFT_PRICING", good)
	srv := New(DB(costDB(t)))
	if srv.pricing.source != pricingFile || srv.pricing.path != good {
		t.Errorf("New under WEFT_PRICING: pricing %+v", srv.pricing)
	}
	srv = New(DB(costDB(t)), Pricing(DefaultPricing()))
	if srv.pricing.source != pricingDefault {
		t.Errorf("Pricing did not win over WEFT_PRICING: %+v", srv.pricing.source)
	}
	bad := write("bad.json", `{"dated":"2026-10-10","models":[{"provider":"a","model_glob":"b","output":1}]}`)
	t.Setenv("WEFT_PRICING", bad)
	func() {
		defer func() {
			msg, _ := recover().(string)
			if !strings.Contains(msg, "WEFT_PRICING") || !strings.Contains(msg, bad) || !strings.Contains(msg, "models[0].input") {
				t.Errorf("New under a malformed WEFT_PRICING panicked with %q, want the path and the field", msg)
			}
		}()
		New(DB(costDB(t)))
	}()
}

// TestPricingSettingsGolden pins GET /api/settings/pricing's shape over
// a small Go table (setup A on loopback: no path for a Go-built table).
func TestPricingSettingsGolden(t *testing.T) {
	cached := 0.1
	h := Handler(DB(costDB(t)), Pricing(PricingTable{Dated: "2026-10-01", Note: "test rates", Models: []ModelPrice{
		{Provider: "anthropic", ModelGlob: "claude-*", Input: 1, Output: 5, CachedInput: &cached},
		{Provider: "*", ModelGlob: "*", Input: 2, Output: 8},
	}}))
	code, _, body := get(t, h, "/studio/api/settings/pricing")
	if code != http.StatusOK {
		t.Fatalf("settings/pricing = %d %s", code, body)
	}
	golden(t, "settings-pricing.golden.json", body)
}

// TestCostSubagentRealRun drives a parent with a Subagent through the
// real pipeline (core → otel → OTLP ingest → obsdb) and reads its cost:
// the parent's stored usage includes the child's (core rolls it up), and
// the row's own figure takes the child's out. wefttest models bill 10
// input / 5 output tokens per step.
func TestCostSubagentRealRun(t *testing.T) {
	srv := New(Open(filepath.Join(t.TempDir(), "w.db")), Pricing(PricingTable{Dated: "2026-10-01", Models: []ModelPrice{
		{Provider: "wefttest", ModelGlob: "parent-m", Input: 1000, Output: 2000},
		{Provider: "wefttest", ModelGlob: "child-m", Input: 100, Output: 200},
	}}))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	child := core.New(namedModel{wefttest.Script(wefttest.Say("found it")), "child-m"}, core.Name("researcher"),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	parent := core.New(namedModel{wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"task":"look"}`, ID: "c_r"}),
		wefttest.Say("done"),
	), "parent-m"}, core.Name("orders"), core.Subagent("research", "Research.", child),
		core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider()))
	res, err := parent.Generate(ctx, core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.InputTokens != 30 || res.Usage.OutputTokens != 15 {
		t.Fatalf("parent usage = %+v, want 30/15: two own steps and the child's one rolled up", res.Usage)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		costRow
		Usage    core.Usage `json:"usage"`
		Children []costRow  `json:"children"`
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		resp, err := http.Get(ts.URL + "/api/runs/" + res.ID)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			decode(t, string(b), &doc)
			if len(doc.Children) == 1 && doc.Usage.InputTokens == 30 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET run = %d %s", resp.StatusCode, b)
		}
	}
	// own: 20 × $1000 + 10 × $2000 = 40,000 ÷ 1M = $0.04;
	// delegated: the child's 10 × $100 + 5 × $200 = 2,000 ÷ 1M = $0.002.
	if usd(doc.CostUSD) != "0.04" || usd(doc.DelegatedCostUSD) != "0.002" || usd(doc.Children[0].CostUSD) != "0.002" {
		t.Errorf("real subagent run: own %s delegated %s child %s, want 0.04, 0.002, 0.002",
			usd(doc.CostUSD), usd(doc.DelegatedCostUSD), usd(doc.Children[0].CostUSD))
	}
}

// pricedServer is a Studio over a fresh file, pricing the test models,
// with the pipeline that feeds it.
func pricedServer(t *testing.T, rows ...ModelPrice) (*Server, *httptest.Server, *otel.Pipeline) {
	t.Helper()
	srv := New(Open(filepath.Join(t.TempDir(), "w.db")), Pricing(PricingTable{Dated: "2026-10-01", Models: rows}))
	t.Cleanup(func() { _ = srv.Close() })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	p, err := otel.Start(context.Background(), otel.Studio(ts.URL, ""), otel.NoGlobal())
	if err != nil {
		t.Fatal(err)
	}
	return srv, ts, p
}

// getUntil GETs path until ok accepts the decoded body (ingest lands
// asynchronously) and returns the body.
func getUntil(t *testing.T, ts *httptest.Server, path string, v any, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			decode(t, string(b), v)
			if ok() {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s = %d %s", path, resp.StatusCode, b)
		}
	}
}

// TestCostPoolChild: a thread/pool child session's run carries
// weft.parent.run.id = the parent run (obsdb lists it as a child), but
// the core never rolled its usage into the parent's (rule 13's pool
// exception). The parent's own figure is its own usage priced, nothing
// is delegated, and the child's session is priced on its own run.
func TestCostPoolChild(t *testing.T) {
	srv, ts, p := pricedServer(t,
		ModelPrice{Provider: "wefttest", ModelGlob: "parent-m", Input: 1000, Output: 2000},
		ModelPrice{Provider: "wefttest", ModelGlob: "child-m", Input: 100, Output: 200})
	ctx := context.Background()
	obs := []core.Option{core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider())}
	pl := pool.New(2)
	child := core.New(namedModel{wefttest.Script(wefttest.Say("the answer is 42")), "child-m"}, append(obs, core.Name("researcher"))...)
	parent := core.New(namedModel{wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: wefttest.Args(struct{ Prompt string }{"find it"})}),
		wefttest.Say("done"),
	), "parent-m"}, append(obs, core.Name("orders"), pl.MustWrap("research", "delegates research", child))...)
	s, err := thread.Create(ctx, thread.Memory(), parent)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, core.User("go"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := turn.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.InputTokens != 20 || res.Usage.OutputTokens != 10 {
		t.Fatalf("parent usage = %+v, want 20/10: a pool child is never rolled up", res.Usage)
	}
	receipts := pool.Receipts(s)
	if len(receipts) != 1 {
		t.Fatalf("receipts = %+v", receipts)
	}
	childSession := receipts[0].Child
	if err := pl.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	var doc struct {
		costRow
		Children []costRow `json:"children"`
	}
	getUntil(t, ts, "/api/runs/"+res.ID, &doc, func() bool { return len(doc.Children) == 1 && doc.CostKnown })
	// own: 20 × $1000 + 10 × $2000 = 40,000 ÷ 1M = $0.04 — not reduced by
	// the pool child's 10/5; delegated: 0, the child is not a Subagent.
	if usd(doc.CostUSD) != "0.04" || usd(doc.DelegatedCostUSD) != "0" || !doc.DelegatedCostKnown {
		t.Errorf("pool parent: own %s delegated %s, want 0.04 and 0 (the pool child %s is listed, not subtracted)",
			usd(doc.CostUSD), usd(doc.DelegatedCostUSD), doc.Children[0].ID)
	}
	// The pool child's own row: 10 × $100 + 5 × $200 = 2,000 ÷ 1M = $0.002.
	if usd(doc.Children[0].CostUSD) != "0.002" {
		t.Errorf("pool child row: own %s, want 0.002", usd(doc.Children[0].CostUSD))
	}
	// The child's session is priced on that run: a turn of its session,
	// its parent being in another session. (obsdb groups a session over
	// top-level runs only, so /api/sessions/{child} is 404 today — the
	// figure is read through sessionCost, the session routes' rule.)
	var childRow struct {
		SessionID string `json:"session_id"`
	}
	getUntil(t, ts, "/api/runs/"+doc.Children[0].ID, &childRow, func() bool { return true })
	if childRow.SessionID != childSession {
		t.Fatalf("the pool child's run carries session %q, want the child session %s", childRow.SessionID, childSession)
	}
	sc, _, _ := srv.sessionCost(ctx, childSession, "")
	if f := sc.fields(); usd(f.CostUSD) != "0.002" || !f.CostKnown {
		t.Errorf("pool child session %s: cost %s known %v (%s), want 0.002", childSession, usd(f.CostUSD), f.CostKnown, f.CostReason)
	}
	// And the parent's session is its own turn's bill alone.
	pc, _, _ := srv.sessionCost(ctx, s.ID(), "")
	if f := pc.fields(); usd(f.CostUSD) != "0.04" {
		t.Errorf("pool parent session: cost %s, want 0.04", usd(f.CostUSD))
	}
}

// TestCostNestedGenerate: another agent's Generate inside a tool
// handler is linked to the run (weft.parent.run.id) but never rolled
// up: the parent's own figure is its own usage, nothing delegated.
func TestCostNestedGenerate(t *testing.T) {
	_, ts, p := pricedServer(t,
		ModelPrice{Provider: "wefttest", ModelGlob: "parent-m", Input: 1000, Output: 2000},
		ModelPrice{Provider: "wefttest", ModelGlob: "helper-m", Input: 100, Output: 200})
	ctx := context.Background()
	obs := []core.Option{core.TracerProvider(p.TracerProvider()), core.LoggerProvider(p.LoggerProvider())}
	helper := core.New(namedModel{wefttest.Script(wefttest.Say("helped")), "helper-m"}, append(obs, core.Name("helper"))...)
	ask := core.Tool("ask", "Ask the helper.", func(ctx context.Context, in struct {
		Q string `json:"q"`
	}) (string, error) {
		r, err := helper.Generate(ctx, core.Prompt(in.Q))
		if err != nil {
			return "", err
		}
		return r.Text(), nil
	})
	parent := core.New(namedModel{wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "ask", Args: `{"q":"help"}`}),
		wefttest.Say("done"),
	), "parent-m"}, append(obs, core.Name("orders"), ask)...)
	res, err := parent.Generate(ctx, core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		costRow
		Children []costRow `json:"children"`
	}
	getUntil(t, ts, "/api/runs/"+res.ID, &doc, func() bool { return len(doc.Children) == 1 && doc.CostKnown })
	if strings.HasPrefix(doc.Children[0].ID, res.ID+"/") {
		t.Fatalf("the nested run %s carries the Subagent id shape", doc.Children[0].ID)
	}
	if usd(doc.CostUSD) != "0.04" || usd(doc.DelegatedCostUSD) != "0" || usd(doc.Children[0].CostUSD) != "0.002" {
		t.Errorf("nested Generate: own %s delegated %s nested %s, want 0.04, 0 and 0.002 on its own row",
			usd(doc.CostUSD), usd(doc.DelegatedCostUSD), usd(doc.Children[0].CostUSD))
	}
}

// TestCostProviderAlias: a Gemini run whose row kept the invoke_agent
// span's semconv provider name (gcp.gemini) prices as google.
func TestCostProviderAlias(t *testing.T) {
	db := costDB(t)
	u := core.Usage{InputTokens: 100_000, OutputTokens: 10_000}
	costUsage(t, db, "r_gem", "", "", "gcp.gemini", "gemini-2.5-flash", time.Now().Add(-time.Hour).UTC(), u, u)
	_, _, body := get(t, Handler(DB(db)), "/studio/api/runs/r_gem")
	var r costRow
	decode(t, body, &r)
	// 100,000 × $0.30 + 10,000 × $2.50 = 30,000 + 25,000 = 55,000 ÷ 1M = $0.055.
	if usd(r.CostUSD) != "0.055" {
		t.Errorf("gcp.gemini/gemini-2.5-flash: cost %s (%s), want 0.055 at google's rates", usd(r.CostUSD), r.CostReason)
	}
}

// TestCostRunning: a running run's usage arrives at its end, so its own
// figure is unknown — never 0 — on the document and on a live run frame;
// a finished frame carries the priced figure.
func TestCostRunning(t *testing.T) {
	db := costDB(t)
	// A run_start alone: running (last seen now).
	costUsageOpen(t, db, "r_run", "anthropic", "claude-opus-5-5")
	hub := obsdb.NewHub()
	h := Handler(DB(db), Live(hub))
	_, _, body := get(t, h, "/studio/api/runs/r_run")
	var r costRow
	decode(t, body, &r)
	if r.CostUSD != nil || r.CostKnown || r.CostBadge != costUnknown || r.CostReason != "running: usage arrives at run end" ||
		usd(r.DelegatedCostUSD) != "0" {
		t.Errorf("running run: %s", body)
	}

	resp := subscribeLive(t, h, "?run=r_live&kinds=run", "")
	hub.Publish(context.Background(), obsdb.RunFrame(obsdb.RunRow{ID: "r_live", Provider: "anthropic", Model: "claude-opus-5-5",
		Status: obsdb.StatusRunning, Usage: core.Usage{InputTokens: 10}}))
	fin := time.Now().UTC()
	hub.Publish(context.Background(), obsdb.RunFrame(obsdb.RunRow{ID: "r_live", Provider: "anthropic", Model: "claude-opus-5-5",
		Status: obsdb.StatusSucceeded, Finished: &fin, Usage: core.Usage{InputTokens: 100_000, OutputTokens: 10_000}}))
	frames := readSSE(t, resp, 2, 5*time.Second)
	if len(frames) != 2 {
		t.Fatalf("frames = %+v", frames)
	}
	var running, done struct {
		Run costRow `json:"run"`
	}
	decode(t, frames[0].data, &running)
	decode(t, frames[1].data, &done)
	if running.Run.CostUSD != nil || running.Run.CostBadge != costUnknown || running.Run.CostReason != "running: usage arrives at run end" {
		t.Errorf("running frame: %s", frames[0].data)
	}
	// 100,000 × $4 + 10,000 × $20 = 600,000 ÷ 1M = $0.6, cache not_reported.
	if usd(done.Run.CostUSD) != "0.6" || done.Run.Cache != cacheNotReported || done.Run.CostReason != cacheNote {
		t.Errorf("finished frame: %s", frames[1].data)
	}
}

// costUsageOpen writes a run_start alone: a run in flight.
func costUsageOpen(t *testing.T, db obsdb.DB, id, provider, model string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"type": "run_start", "id": id, "agent": "billing",
		"model": map[string]string{"provider": provider, "name": model}})
	rec := obsdb.Record{Time: time.Now().UTC(), EventName: "weft.event", Severity: 9, Body: string(b), Service: "svc",
		Attrs: map[string]any{"weft.record": "event", "weft.run.id": id, "gen_ai.agent.name": "billing",
			"weft.event.type": "run_start", "weft.event.pos": int64(0)},
		Resource: map[string]any{"service.name": "svc"}}
	if err := db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{rec}}); err != nil {
		t.Fatal(err)
	}
}

// TestCostSortCap: a match larger than the cap is priced and listed up
// to it, and the body says so. The cap is lowered to 3 through the
// package var (2,000 in production).
func TestCostSortCap(t *testing.T) {
	old := costSortCap
	costSortCap = 3
	t.Cleanup(func() { costSortCap = old })
	db := costDB(t)
	t0 := time.Now().Add(-time.Hour).UTC()
	for i := range 5 {
		u := core.Usage{InputTokens: int64(1000 * (i + 1))}
		costUsage(t, db, "r_cap"+strconv.Itoa(i), "", "", "anthropic", "claude-opus-5-5", t0.Add(time.Duration(i)*time.Second), u, u)
	}
	h := Handler(DB(db))
	_, _, body := get(t, h, "/studio/api/search?sort=cost")
	var page struct {
		Total       int       `json:"total"`
		Runs        []costRow `json:"runs"`
		CostSortCap *int      `json:"cost_sort_cap"`
	}
	decode(t, body, &page)
	var ids []string
	for _, r := range page.Runs {
		ids = append(ids, r.ID)
	}
	// The newest three (r_cap4..2) are read, then ordered by cost.
	if page.CostSortCap == nil || *page.CostSortCap != 3 || page.Total != 3 || strings.Join(ids, ",") != "r_cap4,r_cap3,r_cap2" {
		t.Errorf("capped sort=cost = total %d cap %v %v, want 3, cap 3, r_cap4,r_cap3,r_cap2", page.Total, page.CostSortCap, ids)
	}
	_, _, body = get(t, h, "/studio/api/search?sort=cost&limit=10&agent=billing&q=nothing-matches")
	if strings.Contains(body, "cost_sort_cap") {
		t.Errorf("an uncapped match carries cost_sort_cap: %s", body)
	}
}

// TestCostZeroUsageIsExact: a set with no usage — a run-less
// experiment — is a known $0, and nothing about it is overstated: its
// cache reads exact, with no cache note for a reason.
func TestCostZeroUsageIsExact(t *testing.T) {
	ts := httptest.NewServer(New(Open(filepath.Join(t.TempDir(), "w.db")), Playground(true)).Handler())
	t.Cleanup(ts.Close)
	def := `{"id":"exp_empty","name":"n","agent":"a","variants":[{"key":"A","overrides":{}}],"inputs":[{"key":"1","text":"x"}]}`
	resp, err := http.Post(ts.URL+"/api/experiments", "application/json", strings.NewReader(def))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save = %d %s", resp.StatusCode, b)
	}
	var r costRow
	decode(t, string(b), &r)
	if usd(r.CostUSD) != "0" || !r.CostKnown || r.Cache != cacheExact || r.CostReason != "" {
		t.Errorf("run-less experiment: cost %s known %v cache %q reason %q; want 0, known, exact, no reason",
			usd(r.CostUSD), r.CostKnown, r.Cache, r.CostReason)
	}
}

// TestCostRunningChildIsUnknown: a turn whose subagent is still running
// has a delegated figure that is unknown — not unpriced: the model is
// in the table, its usage has not arrived — and the session's summed
// badge says so.
func TestCostRunningChildIsUnknown(t *testing.T) {
	db := costDB(t)
	t0 := time.Now().Add(-time.Minute).UTC()
	own := core.Usage{InputTokens: 10_000, OutputTokens: 1_000}
	costUsage(t, db, "p", "", "s_run", "anthropic", "claude-opus-5-5", t0, own, own)
	b, _ := json.Marshal(map[string]any{"type": "run_start", "id": "p/0/c", "agent": "billing",
		"model": map[string]string{"provider": "anthropic", "name": "claude-opus-5-5"}})
	rec := obsdb.Record{Time: time.Now().UTC(), EventName: "weft.event", Severity: 9, Body: string(b), Service: "svc",
		Attrs: map[string]any{"weft.record": "event", "weft.run.id": "p/0/c", "gen_ai.agent.name": "billing",
			"weft.event.type": "run_start", "weft.event.pos": int64(0), "weft.session.id": "s_run",
			"weft.parent.run.id": "p", "weft.parent.call.id": "call_c"},
		Resource: map[string]any{"service.name": "svc"}}
	if err := db.Write(context.Background(), obsdb.Batch{Records: []obsdb.Record{rec}}); err != nil {
		t.Fatal(err)
	}
	h := Handler(DB(db))
	_, _, body := get(t, h, "/studio/api/sessions/s_run")
	var sess costRow
	decode(t, body, &sess)
	if sess.CostUSD != nil || sess.CostKnown || sess.CostBadge != costUnknown || !strings.Contains(sess.CostReason, "running") {
		t.Errorf("session with a running subagent: cost %s known %v badge %q reason %q; want null, unknown, naming running",
			usd(sess.CostUSD), sess.CostKnown, sess.CostBadge, sess.CostReason)
	}
	// An unknown figure says it is not known yet, never "not priced":
	// the running child has a price, its usage has not arrived.
	if want := "a turn's cost is not known yet: p's subagents (a subagent run's cost is not known yet: p/0/c (running: "; !strings.HasPrefix(sess.CostReason, want) {
		t.Errorf("session cost reason = %q, want prefix %q", sess.CostReason, want)
	}
	_, _, body = get(t, h, "/studio/api/runs/p")
	var run costRow
	decode(t, body, &run)
	if want := "a subagent run's cost is not known yet: p/0/c (running: "; run.DelegatedCostKnown || !strings.HasPrefix(run.DelegatedCostReason, want) {
		t.Errorf("run's delegated cost: known %v reason %q; want unknown, prefix %q", run.DelegatedCostKnown, run.DelegatedCostReason, want)
	}
}
