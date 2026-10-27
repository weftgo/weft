package studio

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

//go:generate go run ./internal/pricinggen -o pricing.json

// Cost (plan A5): Studio prices usage at read time from a pricing table
// — never stored, never sent anywhere, nothing fetched. The table is a
// plain JSON file; the default ships in the repo with the day its rates
// were read (pricing.json, generated from internal/pricingdata), and an
// operator replaces it with Pricing, WEFT_PRICING=<path> or weft studio
// --pricing <path>.
//
// # The table
//
//	{"dated": "2026-10-10", "note": "…", "models": [
//	  {"provider": "anthropic", "model_glob": "claude-opus-5-5",
//	   "input": 4, "output": 20, "cached_input": 0.2, "cache_write": 5}, …]}
//
// Rates are USD per 1M tokens. A run's model is looked up as
// "provider/model" against each row's "provider/model_glob" with
// path.Match semantics (so either half may be a glob: "*" matches any
// run of characters but "/"); the first match wins, so the most specific
// row comes first. The provider is the table's spelling: a row whose
// provider came from the invoke_agent span's semconv name ("gcp.gemini")
// is looked up as the adapter's ("google"). A row with no provider (or
// provider "*") matches on its model glob alone, whatever the transport:
// {"model_glob": "kimi-k2.7-code", …} prices that model reached through
// the openai adapter at an OpenAI-compatible base URL, a provider
// spelled with a "/", or any other. input and output are
// required; cached_input, cache_write and reasoning are optional, and a
// missing one prices that token kind at the base rate (cached_input and
// cache_write at input, reasoning at output). An override file replaces
// the default table whole — there is no merging; start from
// pricing.json, the template.
//
// # The formula
//
// core.Usage's splits are subsets of its totals (CachedInputTokens and
// CacheWriteTokens of InputTokens, ReasoningTokens of OutputTokens), so
//
//	cost = input       × (InputTokens − CachedInputTokens − CacheWriteTokens)
//	     + cached_input × CachedInputTokens
//	     + cache_write  × CacheWriteTokens
//	     + output      × (OutputTokens − ReasoningTokens)
//	     + reasoning   × ReasoningTokens          (all ÷ 1,000,000)
//
// exact where the adapter reports the splits. When it reported no cache
// split (CachedInputTokens and CacheWriteTokens both zero) every input
// token is priced at the input rate, and the object says cache
// "not_reported" with cost_reason "no cache split reported; a cached
// prompt may be overstated" (an uncached prompt reads the same, its
// figure exact). Figures are float64, rounded to 6 decimals on the wire.
//
// # On the wire
//
// Every priced object carries cost_usd (null when unknown — never 0 for
// an unpriced model), cost_known, cache ("exact" | "not_reported"), and
// when unknown cost_badge ("unpriced": no row matches the model;
// "unknown": the figure cannot be computed yet or at all) with
// cost_reason naming why in one short line ("openai/x has no price";
// a set's "2 of 9 turns unpriced: no price for openai/x, acme/y" — never
// one clause per run), and unpriced_models: the provider/model pairs
// without a price behind the figure, deduped and sorted (a run row's
// delegated_unpriced_models the same for its subagents), so a page can
// say it once over its rows. Both lists, and the reason's, stop at five
// models: a sixth and more read as one last element "(+n)".
//
// A scripted model — every model of the wefttest provider, and the
// playground's scripted engine (weft/runtime + scripted) — that no row
// of the table matches is priced by a built-in
// rule: an exact $0 (cost_known true, cache "exact") with cost_badge
// "scripted". Nothing bills it; the rule is Go code, not a pricing.json
// row, so an override file cannot lose it (a row that matches — a test
// pricing its scripted model — wins, as every first match does). Any
// other provider that wraps a scripted model ships its own row: the rule
// covers only the models this repository knows nothing bills.
//
// A running run is "unknown" ("running: usage
// arrives at run end" — the core reports a run's usage once, at its
// end), as is one whose end never arrived (no run_finish, no
// invoke_agent span: nothing recorded its usage).
//
// Run rows (lists, documents, children, search hits, live frames,
// experiment runs, the export) and a step's children[] add
// delegated_cost_usd / delegated_cost_known / delegated_cost_reason. A
// run's usage includes its core Subagent children's (rule 13: the core
// rolls them up) — the runs whose id is <run id>/<step>/<call> — so
// cost_usd is the run's own model calls: its usage minus those direct
// children's, priced at the run's model (a step that a Fallback or
// UseModel answered with another model is priced at the run's model in
// this figure; the step route prices it at the answering model), and
// delegated_cost_usd is the sum of its Subagent descendants' own costs —
// two numbers, never folded (null and known false when any is unpriced
// or running). The runs linked to it by weft.caller.run.id (O11) — a
// thread/pool child session's run, another agent's Generate inside a
// tool handler; before weft 0.18 linked by weft.parent.run.id too —
// were never rolled up: they are neither subtracted nor delegated, and
// carry their own figures on their own rows.
//
// A session row's cost_usd is its turns' own + delegated (sessionCost:
// every run of the session that is not a Subagent child of another of
// its runs — a pool child session's run is a turn of the child
// session). The pool child's cost is on its own session, never folded
// into the parent session's row; the parent's session document carries
// its delegated sessions' figures beside its own
// (delegated_sessions_cost_usd, U14 — thread's Usage().Delegated, read
// from obsdb's sessions). A step's cost_usd prices its own model call
// (step_finish's usage) at the model that answered it, else the model
// it requested, else the run's.

// PricingTable is a pricing table: pricing.json's shape (and a
// WEFT_PRICING override file's). Build one in Go, or read one with
// LoadPricing; DefaultPricing is the shipped table.
type PricingTable struct {
	// Dated is the day the rates were read, ISO 8601 (2026-10-10).
	Dated string `json:"dated"`
	// Note is the file's comment: what the rates are and where from.
	Note string `json:"note,omitempty"`
	// Models are the rows, first match wins.
	Models []ModelPrice `json:"models"`

	source, path string // "default" | "file" | "option"; the file's path
}

// ModelPrice is one row of a PricingTable: a model glob and its rates
// in USD per 1M tokens. A nil optional rate prices that token kind at
// the base rate (CachedInput and CacheWrite at Input, Reasoning at
// Output).
type ModelPrice struct {
	// Provider is the run's provider the row matches ("anthropic");
	// "*" — what a file's missing provider loads as — matches any, the
	// row then looked up on ModelGlob against the model alone.
	Provider    string   `json:"provider"`
	ModelGlob   string   `json:"model_glob"`
	Input       float64  `json:"input"`
	Output      float64  `json:"output"`
	CachedInput *float64 `json:"cached_input,omitempty"`
	CacheWrite  *float64 `json:"cache_write,omitempty"`
	Reasoning   *float64 `json:"reasoning,omitempty"`
}

// Pricing sets the table Studio prices usage with (plan A5). Without
// it, New reads the file WEFT_PRICING names (LoadPricing; a file that
// does not load panics at New, naming the path and the field), else
// uses DefaultPricing. The table replaces the default whole.
func Pricing(t PricingTable) Option {
	return func(c *config) {
		t.Models = slices.Clone(t.Models)
		if t.source == "" {
			t.source = pricingOption
		}
		c.pricing = &t
	}
}

// Pricing sources api/meta and the settings route report.
const (
	pricingDefault = "default" // the shipped pricing.json
	pricingFile    = "file"    // WEFT_PRICING / --pricing (LoadPricing)
	pricingOption  = "option"  // a table built in Go and passed to Pricing
)

//go:embed pricing.json
var defaultPricingJSON []byte

var (
	defaultPricingOnce sync.Once
	defaultPricingVal  PricingTable
)

// DefaultPricing is the table Studio ships: pricing.json, generated
// from the vendors' public list prices on its Dated day. Nothing is
// fetched — a newer price arrives with a newer weft, or with an
// override file.
func DefaultPricing() PricingTable {
	defaultPricingOnce.Do(func() {
		t, err := parsePricing(defaultPricingJSON, "pricing.json (embedded)")
		if err != nil {
			panic("studio: " + err.Error()) // pinned by TestDefaultPricingLoads
		}
		t.source = pricingDefault
		defaultPricingVal = t
	})
	t := defaultPricingVal
	t.Models = slices.Clone(t.Models)
	return t
}

// LoadPricing reads a pricing table file (pricing.json's shape). A file
// that does not parse, names an unknown field, misses input or output,
// carries a negative rate, a bad glob or a dated that is not
// YYYY-MM-DD is an error naming the path and the field — never a
// silent fallback to the default. A row's missing provider loads as
// "*": the row matches on its model glob alone, any transport. The table it returns reports source
// "file" and its path.
func LoadPricing(file string) (PricingTable, error) {
	if abs, err := filepath.Abs(file); err == nil {
		file = abs
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return PricingTable{}, fmt.Errorf("pricing %s: %w", file, err)
	}
	t, err := parsePricing(b, file)
	if err != nil {
		return PricingTable{}, err
	}
	t.source, t.path = pricingFile, file
	return t, nil
}

// pricingFileShape is what a file may hold: the table, plus the source
// and path the settings route adds (so its export loads back).
type pricingFileShape struct {
	Dated  *string            `json:"dated"`
	Note   string             `json:"note"`
	Models *[]json.RawMessage `json:"models"`
	Source string             `json:"source"`
	Path   string             `json:"path"`
}

type modelPriceFile struct {
	Provider    *string  `json:"provider"`
	ModelGlob   *string  `json:"model_glob"`
	Input       *float64 `json:"input"`
	Output      *float64 `json:"output"`
	CachedInput *float64 `json:"cached_input"`
	CacheWrite  *float64 `json:"cache_write"`
	Reasoning   *float64 `json:"reasoning"`
}

// indexRe turns encoding/json's "models.0.input" into "models[0].input".
var indexRe = regexp.MustCompile(`\.(\d+)`)

// parsePricing decodes and validates a table; name is the file named in
// errors.
func parsePricing(b []byte, name string) (PricingTable, error) {
	fail := func(field, format string, args ...any) (PricingTable, error) {
		return PricingTable{}, fmt.Errorf("pricing %s: %s: "+format, append([]any{name, field}, args...)...)
	}
	// Each row is decoded on its own so the field an error names is
	// "models[i].<field>" whatever encoding/json puts in its own Field
	// (Go 1.26 names "models.input", 1.27 "models.0.input").
	decode := func(raw []byte, v any, prefix string) (PricingTable, error, bool) {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			var te *json.UnmarshalTypeError
			if errors.As(err, &te) && te.Field != "" {
				pt, e := fail(prefix+indexRe.ReplaceAllString(te.Field, "[$1]"), "want a %s, got %s", te.Type, te.Value)
				return pt, e, true
			}
			if msg, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
				pt, e := fail(prefix+strings.Trim(msg, `"`), "unknown field (the shape is {dated, note, models: [{provider, model_glob, input, output, cached_input, cache_write, reasoning}]})")
				return pt, e, true
			}
			return PricingTable{}, fmt.Errorf("pricing %s: does not parse: %w", name, err), true
		}
		if dec.More() {
			return PricingTable{}, fmt.Errorf("pricing %s: does not parse: more than one JSON value", name), true
		}
		return PricingTable{}, nil, false
	}
	var f pricingFileShape
	if pt, err, bad := decode(b, &f, ""); bad {
		return pt, err
	}
	if f.Dated == nil || *f.Dated == "" {
		return fail("dated", "missing (the day the rates were read, YYYY-MM-DD)")
	}
	if _, err := time.Parse(time.DateOnly, *f.Dated); err != nil {
		return fail("dated", "%q is not a YYYY-MM-DD date", *f.Dated)
	}
	if f.Models == nil {
		return fail("models", "missing (a list of rows; [] prices nothing)")
	}
	t := PricingTable{Dated: *f.Dated, Note: f.Note, Models: make([]ModelPrice, 0, len(*f.Models))}
	for i, raw := range *f.Models {
		at := func(field string) string { return "models[" + strconv.Itoa(i) + "]." + field }
		var m modelPriceFile
		if pt, err, bad := decode(raw, &m, at("")); bad {
			return pt, err
		}
		if m.Provider == nil || *m.Provider == "" {
			star := anyProvider
			m.Provider = &star
		}
		if m.ModelGlob == nil || *m.ModelGlob == "" {
			return fail(at("model_glob"), "missing")
		}
		if _, err := path.Match(rowPattern(*m.Provider, *m.ModelGlob), ""); err != nil {
			return fail(at("model_glob"), "%q is not a path.Match pattern: %v", rowPattern(*m.Provider, *m.ModelGlob), err)
		}
		for _, rate := range []struct {
			field    string
			v        *float64
			required bool
		}{{"input", m.Input, true}, {"output", m.Output, true}, {"cached_input", m.CachedInput, false},
			{"cache_write", m.CacheWrite, false}, {"reasoning", m.Reasoning, false}} {
			switch {
			case rate.v == nil && rate.required:
				return fail(at(rate.field), "missing (USD per 1M tokens)")
			case rate.v != nil && (*rate.v < 0 || math.IsNaN(*rate.v) || math.IsInf(*rate.v, 0)):
				return fail(at(rate.field), "%v is not a non-negative rate", *rate.v)
			}
		}
		t.Models = append(t.Models, ModelPrice{
			Provider: *m.Provider, ModelGlob: *m.ModelGlob, Input: *m.Input, Output: *m.Output,
			CachedInput: m.CachedInput, CacheWrite: m.CacheWrite, Reasoning: m.Reasoning,
		})
	}
	return t, nil
}

// resolvePricing is New's rule: the Pricing option, else WEFT_PRICING's
// file (a file that does not load panics, as Open's database does),
// else the default.
func resolvePricing(c *config) {
	if c.pricing != nil {
		// A Go-built row without a provider is the any-provider row, as
		// a file's missing provider loads.
		for i := range c.pricing.Models {
			if c.pricing.Models[i].Provider == "" {
				c.pricing.Models[i].Provider = anyProvider
			}
		}
		return
	}
	if file := os.Getenv("WEFT_PRICING"); file != "" {
		t, err := LoadPricing(file)
		if err != nil {
			panic("studio: WEFT_PRICING: " + err.Error())
		}
		c.pricing = &t
		return
	}
	t := DefaultPricing()
	c.pricing = &t
}

// anyProvider is a row's provider when it matches every transport.
const anyProvider = "*"

// rowPattern is the path.Match pattern a row is looked up with: its
// model glob alone under the any-provider row, else provider/model_glob.
func rowPattern(provider, glob string) string {
	if provider == anyProvider {
		return glob
	}
	return provider + "/" + glob
}

// lookup is the first row matching provider/model (an any-provider
// row: the model alone).
func (t *PricingTable) lookup(provider, model string) (ModelPrice, bool) {
	key := pricingProvider(provider) + "/" + model
	for _, m := range t.Models {
		k := key
		if m.Provider == anyProvider {
			k = model
		}
		if ok, _ := path.Match(rowPattern(m.Provider, m.ModelGlob), k); ok {
			return m, true
		}
	}
	return ModelPrice{}, false
}

// scriptedProviders are the providers whose every model is scripted:
// wefttest's Script and Replay models. A provider outside this
// repository (a wrapper that may wrap any model) is never assumed $0:
// it ships its own pricing row.
var scriptedProviders = map[string]bool{"wefttest": true}

// scripted says provider/model is a scripted model nothing bills: the
// scriptedProviders' models and the playground's scripted engine
// (runtime/scripted.go's ModelInfo{Provider: "weft/runtime", Name:
// "scripted"}).
func scripted(provider, model string) bool {
	return scriptedProviders[provider] || provider == "weft/runtime" && model == "scripted"
}

// scriptedReason is a scripted figure's cost_reason.
const scriptedReason = "a scripted model: nothing bills it, an exact $0"

// providerAliases maps the provider names a run row can carry onto the
// table's: run_start writes the adapter's ModelInfo.Provider ("google"),
// but the invoke_agent span writes the semconv gen_ai.provider.name
// (core/observe.go's providerNames: "google" → "gcp.gemini"; openai and
// anthropic are spelled the same both ways), and obsdb keeps whichever
// arrived first.
var providerAliases = map[string]string{"gcp.gemini": "google"}

// pricingProvider is provider under the table's spelling.
func pricingProvider(p string) string {
	if a, ok := providerAliases[p]; ok {
		return a
	}
	return p
}

// Cost badges (cost_badge).
const (
	costUnpriced = "unpriced" // no row matches the model
	costUnknown  = "unknown"  // the figure could not be computed
	costScripted = "scripted" // a known, exact $0: a scripted model
	// costEstimate is a known figure priced at a stand-in model's rates:
	// a summarizer call whose own model was not recorded, priced at its
	// turn's (U15.1-fix, UX-B-13).
	costEstimate = "estimate"
)

// estimateReason is cost_reason's words on a summarizer figure priced
// at its turn's model (costEstimate).
const estimateReason = "priced at the turn's model: the summarizer's model was not recorded"

// Cache marks (cache).
const (
	cacheExact       = "exact"
	cacheNotReported = "not_reported"
)

// costFields is the cost block every priced object carries.
type costFields struct {
	CostUSD    *float64 `json:"cost_usd"`
	CostKnown  bool     `json:"cost_known"`
	CostBadge  string   `json:"cost_badge,omitempty"`
	CostReason string   `json:"cost_reason,omitempty"`
	Cache      string   `json:"cache"`
	// UnpricedModels are the provider/model pairs without a price
	// behind the figure, deduped in order (absent when none).
	UnpricedModels []string `json:"unpriced_models,omitempty"`
	// CostPartial is an unknown set figure's known part (a session's,
	// an experiment's; UX-H-07): the parts priced, of how many, and
	// their sum — what the devtools panel's header draws as "≥ $x · k
	// of n turns priced" from its turns, so Studio's set rows say the
	// same figure. Absent on a known figure, on a single run's, and
	// when no part is priced.
	CostPartial *costPartial `json:"cost_partial,omitempty"`
}

// costPartial is cost_partial: usd is the priced parts' sum (rounded
// as cost_usd), priced of of the set's parts are known.
type costPartial struct {
	USD    float64 `json:"usd"`
	Priced int     `json:"priced"`
	Of     int     `json:"of"`
}

// delegatedFields is a run's subagent bill beside its own.
type delegatedFields struct {
	DelegatedCostUSD    *float64 `json:"delegated_cost_usd"`
	DelegatedCostKnown  bool     `json:"delegated_cost_known"`
	DelegatedCostReason string   `json:"delegated_cost_reason,omitempty"`
	// DelegatedUnpricedModels are the subagents' unpriced models.
	DelegatedUnpricedModels []string `json:"delegated_unpriced_models,omitempty"`
}

// cost is one computed figure before it goes on the wire.
type cost struct {
	usd           float64
	known         bool
	badge, reason string
	cache         string
	// models are the unpriced models behind the figure (deduped). A
	// tally's figure also counts its runs, those not known yet and
	// those without a price; why is its first unknown part's reason
	// ("<run id>: running: …"), cause the same without the run id.
	models                  []string
	runs, unknown, unpriced int
	why, cause              string
	// A tally's known part (UX-H-07): floor sums, and priced counts,
	// the parts whose whole bill (own + delegated) is known; never counts
	// the unknown parts that will never be known (a derived run whose
	// stock spans report no usage), so "not known yet" is not said of
	// them.
	floor         float64
	priced, never int
}

// addModels adds ms to the deduped list, kept sorted: a set's (or a
// run's subagents') models read in one order whatever order the runs
// were summed in (a children read's order is not the page's), so
// unpriced_models, delegated_unpriced_models and a reason's list are
// stable — and the five a cap keeps are the same five.
func addModels(list []string, ms ...string) []string {
	for _, m := range ms {
		if i, found := slices.BinarySearch(list, m); !found {
			list = slices.Insert(list, i, m)
		}
	}
	return list
}

// maxModels caps a model list on the wire and in a reason: the first
// maxModels, then "(+n)" for the rest — the truncation said, never
// silent.
const maxModels = 5

// capModels is list cut at maxModels, its last element "(+n)" when cut.
func capModels(list []string) []string {
	if len(list) <= maxModels {
		return list
	}
	return append(slices.Clone(list[:maxModels]), fmt.Sprintf("(+%d)", len(list)-maxModels))
}

// fields is c on the wire: rounded, null when unknown.
func (c cost) fields() costFields {
	out := costFields{CostKnown: c.known, CostBadge: c.badge, CostReason: c.reason, Cache: c.cache, UnpricedModels: capModels(c.models)}
	if !c.known && c.priced > 0 {
		out.CostPartial = &costPartial{USD: round6(c.floor), Priced: c.priced, Of: c.runs}
	}
	if c.known && c.cache == cacheNotReported && out.CostReason == "" {
		out.CostReason = cacheNote
	}
	if c.known {
		v := round6(c.usd)
		out.CostUSD = &v
	}
	return out
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// cacheOf reads whether the adapter reported a cache split: with
// neither CachedInputTokens nor CacheWriteTokens every input token is
// priced at the input rate, which may overstate a cached prompt.
// (A zero split cannot be told from an unreported one: an uncached
// prompt reads not_reported too, and its figure is exact.) Reasoning
// tokens are left out — they bill at the output rate at every vendor
// the default table lists.
func cacheOf(u core.Usage) string {
	if u.CachedInputTokens == 0 && u.CacheWriteTokens == 0 {
		return cacheNotReported
	}
	return cacheExact
}

// cacheNote is cost_reason's words for a not_reported cache on a
// known figure.
const cacheNote = "no cache split reported; a cached prompt may be overstated"

// price is u's cost on provider/model under t: the formula above, or
// the unpriced badge naming the model when no row matches.
func (t *PricingTable) price(provider, model string, u core.Usage) cost {
	out := cost{cache: cacheOf(u)}
	if provider == "" && model == "" {
		out.badge, out.reason = costUnpriced, "the run reported no model to price"
		return out
	}
	m, ok := t.lookup(provider, model)
	if !ok && scripted(provider, model) {
		return cost{known: true, badge: costScripted, reason: scriptedReason, cache: cacheExact}
	}
	if !ok {
		out.badge = costUnpriced
		out.reason = provider + "/" + model + " has no price"
		out.models = []string{provider + "/" + model}
		return out
	}
	rate := func(r *float64, base float64) float64 {
		if r != nil {
			return *r
		}
		return base
	}
	plainIn := max(u.InputTokens-u.CachedInputTokens-u.CacheWriteTokens, 0)
	plainOut := max(u.OutputTokens-u.ReasoningTokens, 0)
	out.usd = (m.Input*float64(plainIn) +
		rate(m.CachedInput, m.Input)*float64(u.CachedInputTokens) +
		rate(m.CacheWrite, m.Input)*float64(u.CacheWriteTokens) +
		m.Output*float64(plainOut) +
		rate(m.Reasoning, m.Output)*float64(u.ReasoningTokens)) / 1e6
	out.known = true
	return out
}

// sub is u − o field by field, never below zero: a parent's own usage
// once its children's (rolled up into it by the core) are taken out.
func sub(u, o core.Usage) core.Usage {
	return core.Usage{
		InputTokens:       max(u.InputTokens-o.InputTokens, 0),
		OutputTokens:      max(u.OutputTokens-o.OutputTokens, 0),
		CachedInputTokens: max(u.CachedInputTokens-o.CachedInputTokens, 0),
		CacheWriteTokens:  max(u.CacheWriteTokens-o.CacheWriteTokens, 0),
		ReasoningTokens:   max(u.ReasoningTokens-o.ReasoningTokens, 0),
	}
}

// coster prices run rows for one request: it reads each run's subagent
// children once (obsdb's Runs with the run as parent) and memoizes each
// run's split, so a page of rows and their trees costs one children
// read per run.
type coster struct {
	ctx   context.Context
	db    obsdb.DB
	table *PricingTable
	kids  map[string][]obsdb.RunRow
	memo  map[string]runCost
	// partKids marks the runs whose children in kids are a part only:
	// a set's (setCost) whose one-level read (primeChildren) failed —
	// the row's children_count is then not known (children_unknown).
	partKids map[string]bool
	// capped marks the runs whose children primeTrees stopped short of
	// (its primeChildCap, or ChildRuns' obsdb.ErrChildRunsCapped): their
	// figures and counts read not known, never read one at a time.
	capped map[string]bool
	primed int // child rows primeTrees read so far
	// scope is the request's panel token scope (scopeOf), the zero
	// scope for any other identity. Under it, kids holds each run's children inScope
	// only — every count, children list and delegated figure read from
	// the coster is the token's — and hidden counts the rest, per run;
	// hiddenUsage sums the hidden core Subagent children's usage, which
	// the run's own figure still takes out (the core rolled it in).
	scope       readScope
	hidden      map[string]int
	hiddenSubs  map[string]int
	hiddenUsage map[string]core.Usage
}

// runCost is one run's own and delegated figures.
type runCost struct {
	own, delegated cost
	id             string // the run's, named in a tally's reason
	// split says the usage below is known: the children were read and
	// the run has a core Subagent child (or one out of scope); ownUsage
	// is the run's usage less theirs, delUsage its in-scope Subagent
	// children's summed (each already its whole subtree's, rule 13).
	split              bool
	ownUsage, delUsage core.Usage
}

// total is own + delegated: the run's whole bill (search's sort key).
func (rc runCost) total() (float64, bool) {
	return rc.own.usd + rc.delegated.usd, rc.own.known && rc.delegated.known
}

func (s *Server) coster(ctx context.Context) *coster {
	return &coster{ctx: ctx, db: s.db, table: s.pricing, kids: map[string][]obsdb.RunRow{}, memo: map[string]runCost{}, partKids: map[string]bool{}, capped: map[string]bool{},
		scope: scopeOf(ctx), hidden: map[string]int{}, hiddenSubs: map[string]int{}, hiddenUsage: map[string]core.Usage{}}
}

// prime records a run's children already read (a run document's),
// cut to the coster's scope (scopeKids): the rest are counted, never
// kept.
func (c *coster) prime(id string, kids []obsdb.RunRow) {
	if kids == nil {
		kids = []obsdb.RunRow{}
	}
	visible, n := scopeKids(c.scope, kids)
	c.kids[id] = visible
	delete(c.hidden, id)
	delete(c.hiddenSubs, id)
	delete(c.hiddenUsage, id)
	if n == 0 {
		return
	}
	c.hidden[id] = n
	var u core.Usage
	for _, k := range subagents(id, kids) {
		if !inScope(c.scope, k) {
			c.hiddenSubs[id]++
			u = u.Add(k.Usage)
		}
	}
	c.hiddenUsage[id] = u
}

// visibleKids is a primed run's children in the coster's scope and the
// count of the rest (prime): a run document's children[] and its
// hidden hole.
func (c *coster) visibleKids(id string) ([]obsdb.RunRow, int) {
	return c.kids[id], c.hidden[id]
}

// primeTrees reads the subagent trees of a page of runs before they are
// priced (U9.1, UX-E-24): one obsdb.TreeReader.ChildRuns read per
// level of the trees — the page's runs, then their core Subagent
// children, … — instead of a children read per run, so a runs list or
// a search page costs as many reads as its deepest tree has levels. A
// run already primed is not read again. Without a TreeReader, or when
// a level's read fails, it stops: the runs left unprimed read their
// children one at a time (children), as before. Bounded: once
// primeChildCap child rows have been read for this coster (or a level
// holds more than ChildRuns reads, obsdb.ErrChildRunsCapped), the
// level's runs are capped — their cost and children counts read not
// known (children_unknown), not read again run by run.
func (c *coster) primeTrees(recs []obsdb.RunRow) {
	tr, ok := c.db.(obsdb.TreeReader)
	if !ok {
		return
	}
	var ids []string
	for _, r := range recs {
		if _, done := c.kids[r.ID]; !done {
			ids = append(ids, r.ID)
		}
	}
	for len(ids) > 0 {
		kids, err := tr.ChildRuns(c.ctx, ids)
		n := 0
		for _, k := range kids {
			n += len(k)
		}
		if errors.Is(err, obsdb.ErrChildRunsCapped) || (err == nil && c.primed+n > primeChildCap) {
			slog.DebugContext(c.ctx, "studio: a page's subagent trees hold too many child runs to read: their cost and counts are not known",
				"cap", primeChildCap, "runs", len(ids))
			for _, id := range ids {
				c.capped[id] = true
			}
			return
		}
		if err != nil {
			slog.DebugContext(c.ctx, "studio: a page's subagent children could not be read at once; reading them per run", "err", err)
			return
		}
		c.primed += n
		var next []string
		for _, id := range ids {
			c.prime(id, kids[id])
			for _, k := range subagents(id, c.kids[id]) { // the scope's: a hidden child is never walked
				if _, done := c.kids[k.ID]; !done {
					next = append(next, k.ID)
				}
			}
		}
		ids = next
	}
}

// primeChildren reads the runs' children, one level, in one
// obsdb.TreeReader.ChildRuns read, over what a set primed (setCost: a
// session's runs only — a thread/pool child session's run or a nested
// Generate carries another session id, so the set misses it). The
// cost is unchanged (it reads the core Subagent children, which the
// set holds); the rows' children_count / children_failed count every
// parent-linked run, as the runs list's do. Without a TreeReader, or
// when the read fails, each run's count is marked unknown.
func (c *coster) primeChildren(recs []obsdb.RunRow) {
	ids := make([]string, 0, len(recs))
	for _, r := range recs {
		ids = append(ids, r.ID)
	}
	if len(ids) == 0 {
		return
	}
	tr, ok := c.db.(obsdb.TreeReader)
	var kids map[string][]obsdb.RunRow
	var err error
	if ok {
		kids, err = tr.ChildRuns(c.ctx, ids)
	}
	if !ok || err != nil {
		slog.DebugContext(c.ctx, "studio: a set's child runs could not be read: their counts are unknown", "err", err)
		for _, id := range ids {
			c.partKids[id] = true
		}
		return
	}
	for _, id := range ids {
		c.prime(id, kids[id])
	}
}

// primeChildCap bounds the child rows primeTrees reads for one coster
// (a request): obsdb.ChildRunsCap, the bound the per-run walk had
// (costerChildPages × 500). A variable for tests.
var primeChildCap = obsdb.ChildRunsCap

// errChildrenCapped is children's answer for a run primeTrees capped.
var errChildrenCapped = fmt.Errorf("more than %d child runs under the runs read: too many to read at once", obsdb.ChildRunsCap)

// costerChildPages bounds the children walk of one run: 20 pages of 500.
const costerChildPages = 20

// children is the run's direct subagent children.
func (c *coster) children(id string) ([]obsdb.RunRow, error) {
	if k, ok := c.kids[id]; ok {
		return k, nil
	}
	if c.capped[id] {
		return nil, errChildrenCapped
	}
	var out []obsdb.RunRow
	q := obsdb.RunQuery{ParentRunID: id, Limit: 500}
	for pages := 0; ; pages++ {
		if pages == costerChildPages {
			return nil, fmt.Errorf("more than %d children", costerChildPages*500)
		}
		page, err := c.db.Runs(c.ctx, q)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Runs...)
		if page.NextBefore == nil || len(page.Runs) == 0 {
			break
		}
		q.Before, q.BeforeID = *page.NextBefore, page.NextBeforeID
	}
	c.prime(id, out)
	return c.kids[id], nil
}

// cost is rec's own and delegated figures (memoized).
func (c *coster) cost(rec obsdb.RunRow) runCost {
	if rc, ok := c.memo[rec.ID]; ok {
		return rc
	}
	c.memo[rec.ID] = runCost{} // a cycle (never written by the core) reads as zero, not a hang
	rc := c.compute(rec)
	c.memo[rec.ID] = rc
	return rc
}

func (c *coster) compute(rec obsdb.RunRow) runCost {
	all, err := c.children(rec.ID)
	if err != nil {
		slog.DebugContext(c.ctx, "studio: a run's subagent children could not be read: its cost is not known", "run", rec.ID, "err", err)
		why := "the run's subagent children could not be read, so its own usage cannot be told from theirs"
		if errors.Is(err, errChildrenCapped) {
			why = "the run's subagent children are past the " + strconv.Itoa(obsdb.ChildRunsCap) + " child runs one read takes, so its own usage cannot be told from theirs"
		}
		return runCost{
			own:       cost{badge: costUnknown, reason: why, cache: cacheOf(rec.Usage)},
			delegated: cost{badge: costUnknown, reason: why, why: rec.ID + ": " + why, cause: why},
		}
	}
	own := rec.Usage
	var parts []runCost
	var delUsage core.Usage
	subs := subagents(rec.ID, all)
	for _, k := range subs {
		own = sub(own, k.Usage)
		delUsage = delUsage.Add(k.Usage)
		kc := c.cost(k)
		kc.id = k.ID
		parts = append(parts, kc)
	}
	// A child outside a panel token's scope (prime) is out of every
	// figure but the own split: its usage is still the core's roll-up.
	own = sub(own, c.hiddenUsage[rec.ID])
	// The cause is carried: one child whose figure is unknown (running,
	// its end never arrived, its children unreadable) makes the
	// delegated figure unknown, not unpriced.
	del := tally(parts, "child run", true)
	if n := c.hiddenSubs[rec.ID]; n > 0 {
		del = scopeCutDelegated(del, n, len(c.scope.Tags) > 0)
	}
	// The split is served only when every Subagent child is in scope: a
	// child out of a panel token's scope makes usage − own − delegated
	// its hidden subtree's figure (I4's rule), and the delegated cost is
	// unknown there already.
	split := len(subs) > 0 && c.hiddenSubs[rec.ID] == 0
	if why := usagePending(rec); why != "" {
		// A running run carries no usage yet: no split to show.
		return runCost{own: cost{badge: costUnknown, reason: why, cache: cacheOf(own)}, delegated: del}
	}
	return runCost{own: c.table.price(rec.Provider, rec.Model, own), delegated: del, split: split, ownUsage: own, delUsage: delUsage}
}

// tally sums a set of runs' whole bills (each part's own + delegated)
// into one figure, known when every part is. Otherwise its badge is
// "unknown" when any part is not known yet (a run still running, its
// end never arrived: it has a price, its usage has not arrived), else
// "unpriced"; and its reason is one line counting the runs — never one
// clause per run: "2 of 3 child runs unpriced: no price for
// openai/x, acme/y", "1 of 3 child runs' cost not known yet (p/0/c:
// running: …)". noun names a run of the set ("child run", "turn",
// "run"). nested counts each part's own subagents as runs of the set (a
// run's delegated figure covers every descendant); without it a part is
// one run, unknown or unpriced by its whole bill (a session's turn, an
// experiment's run).
func tally(parts []runCost, noun string, nested bool) cost {
	out := cost{known: true}
	for _, p := range parts {
		out.usd += p.own.usd + p.delegated.usd
		// The floor is the k priced parts' sum (U33.2-fix): a part whose
		// delegated cost is unknown adds nothing, its own figure included.
		if p.own.known && p.delegated.known {
			out.floor += p.own.usd + p.delegated.usd
			out.priced++
		}
		if p.own.reason == derivedUsageUnknown {
			out.never++
		}
		out.models = addModels(out.models, p.own.models...)
		out.models = addModels(out.models, p.delegated.models...)
		out.runs++
		if p.own.badge == costUnknown && out.why == "" {
			out.why, out.cause = p.id+": "+p.own.reason, p.own.reason
		}
		if out.why == "" {
			out.why, out.cause = p.delegated.why, p.delegated.cause
		}
		switch {
		case p.own.badge == costUnknown, !nested && !p.delegated.known && p.delegated.badge == costUnknown:
			out.unknown++
		case !p.own.known, !nested && !p.delegated.known:
			out.unpriced++
		}
		if nested {
			out.runs += p.delegated.runs
			out.unknown += p.delegated.unknown
			out.unpriced += p.delegated.unpriced
			if !p.delegated.known && p.delegated.unknown+p.delegated.unpriced == 0 {
				out.unknown++ // its children could not be read: none counted
			}
		}
	}
	if out.unknown+out.unpriced == 0 {
		return out
	}
	out.known, out.usd = false, 0
	if out.unknown > 0 {
		out.badge = costUnknown
		// A subagent's reason names the child (one run of a tree the
		// page does not list); a session's or an experiment's names
		// none — its turns are the rows beside it (UX-B-27).
		why := out.cause
		if nested {
			why = out.why
		}
		// A derived run whose spans report no usage is never known
		// (the run row's own words, README's rule): "not known", not
		// "not known yet", when every unknown part is one.
		yet := " yet"
		if out.never >= out.unknown {
			yet = ""
		}
		out.reason = fmt.Sprintf("%d of %d %ss' cost not known%s (%s)", out.unknown, out.runs, noun, yet, why)
		if out.unpriced > 0 {
			out.reason += fmt.Sprintf("; %d unpriced", out.unpriced)
		}
	} else {
		out.badge = costUnpriced
		out.reason = fmt.Sprintf("%d of %d %ss unpriced", out.unpriced, out.runs, noun)
	}
	if len(out.models) > 0 {
		out.reason += ": no price for " + strings.Join(capModels(out.models), ", ")
	}
	return out
}

// scopeCutDelegated is a delegated figure (tally's, over the subagent
// runs in scope) once n more were left out by a panel token's scope
// (coster.prime): not known — the token reads no part of their bill —
// counted, the hidden runs never named; a reason the visible parts had
// follows the scope's.
func scopeCutDelegated(del cost, n int, tagged bool) cost {
	del.runs += n
	del.unknown += n
	del.known, del.usd, del.badge = false, 0, costUnknown
	why := fmt.Sprintf("%d of %d child runs carry another public id, or none: a panel token does not read their cost", n, del.runs)
	if tagged { // a tag-scoped token (I4-fix)
		why = fmt.Sprintf("%d of %d child runs are without your token's tags (or of another public id): your token's access does not reach their cost", n, del.runs)
	}
	if del.reason != "" {
		why += "; " + del.reason
	}
	del.reason = why
	if del.why == "" {
		del.why, del.cause = why, why
	}
	return del
}

// subagents is the core Subagent children among a run's parent-linked
// runs: those whose id is <parent>/<step>/<call> (core's childRunID) —
// the only ones whose usage the core rolled into the parent's (rule
// 13). Since O11 the core links nothing else by weft.parent.run.id; a
// thread/pool child session's run or a nested Generate recorded before
// weft 0.18 is linked so too but was never rolled up: neither
// subtracted nor counted as delegated — the id rule keeps old records
// right.
func subagents(id string, kids []obsdb.RunRow) []obsdb.RunRow {
	var out []obsdb.RunRow
	for _, k := range kids {
		if strings.HasPrefix(k.ID, id+"/") {
			out = append(out, k)
		}
	}
	return out
}

// usagePending says why a run's usage is not its bill yet: the core
// reports a run's usage once, at its end (run_finish, the invoke_agent
// span), so a running run — or one whose end never arrived (Finished
// unset: the same records set both) — carries none to price; a derived
// run whose stock spans report no usage has none either
// (derivedUsageUnknown). "" when the usage is recorded.
func usagePending(rec obsdb.RunRow) string {
	switch {
	case rec.Status == obsdb.StatusRunning:
		return "running: usage arrives at run end"
	case rec.Finished == nil:
		return "the run's end (run_finish or its invoke_agent span) never arrived: its usage is not recorded"
	case rec.Derived && rec.Usage == (core.Usage{}):
		return derivedUsageUnknown
	}
	return ""
}

// derivedUsageUnknown is the cost reason of a derived run (or step)
// whose stock spans report no usage, or only zeros (docs/record-contract.md
// §9): its cost is unknown, never a known $0. A derived run's row holds
// the fold, which cannot tell absent keys from zero ones, so both read
// this way — on the row and on its steps alike.
const derivedUsageUnknown = "the stock spans report no (or zero) usage (gen_ai.usage.* absent or 0 on the root and its chat spans): the cost is unknown"

// row is row(rec) with its cost.
func (c *coster) row(rec obsdb.RunRow) runRow {
	out := row(rec)
	c.fill(&out, rec)
	return out
}

// fill sets a run row's cost blocks.
func (c *coster) fill(out *runRow, rec obsdb.RunRow) {
	rc := c.cost(rec)
	out.costFields = rc.own.fields()
	out.delegatedFields = rc.delegated.delegatedFields()
	if rc.split {
		own, del := rc.ownUsage, rc.delUsage
		out.OwnUsage, out.DelegatedUsage = &own, &del
	}
	// The children the cost read (primed for a page, else read for this
	// row): their count and failures ride on the row (H9), no read more.
	// When that read failed (or only a set's part is held) the count is
	// not known: children_unknown, never an absent count that reads as
	// "no subagents".
	kids, read := c.kids[rec.ID]
	if !read || c.partKids[rec.ID] {
		out.ChildrenUnknown = true
	}
	for _, k := range kids {
		out.ChildrenCount++
		if k.Status == obsdb.StatusFailed {
			out.ChildrenFailed++
		}
	}
}

func (c cost) delegatedFields() delegatedFields {
	out := delegatedFields{DelegatedCostKnown: c.known, DelegatedCostReason: c.reason, DelegatedUnpricedModels: capModels(c.models)}
	if c.known {
		v := round6(c.usd)
		out.DelegatedCostUSD = &v
	}
	return out
}

// sessionCostPages bounds a session's runs walk: 20 pages of 500.
const sessionCostPages = 20

// tooManyRuns is the cost of a session past sessionCostPages × 500 runs.
func tooManyRuns() cost {
	return cost{badge: costUnknown, reason: fmt.Sprintf("the session holds more than %d runs: too many to price at read time", sessionCostPages*500)}
}

// sessionCost is a session's cost: its turns' own + delegated, from
// one walk of every run carrying the session id (Subagent children
// inherit it), so the tree costs no children reads. A turn is every run
// of the set that is not a core Subagent child of another run in it: a
// top-level turn, a thread/pool child session's run (its parent run is
// in the parent's session), and another agent's Generate inside a tool
// handler (linked but never rolled up, so no other figure carries it).
// The coster it returns is primed with that tree for the turn rows.
// scope, when set (a panel token's), keeps only the runs in it (its
// public id, its tags) — the runs list's rule (scopeRunsQuery): a run of another app
// that shares the session id (a derived run's gen_ai.conversation.id)
// is never priced into a panel token's session. The runs it read come
// back too, for a panel token's session row: nil when the walk failed
// or passed its bound (the row cannot be scoped), non-nil and maybe
// empty when it read the whole set.
func (s *Server) sessionCost(ctx context.Context, id string, scope readScope) (cost, *coster, []obsdb.RunRow) {
	c := s.coster(ctx)
	f := false
	q := obsdb.RunQuery{SessionID: id, PublicID: scope.PublicID, Meta: scope.Tags, ParentRunID: "*", Playground: &f, Limit: 500}
	runs := []obsdb.RunRow{} // non-nil: a whole walk, even of no run
	total := 0
	for pages := 0; ; pages++ {
		if pages == sessionCostPages {
			// The bound: whole only when the last count says every run
			// was read (a set of exactly 10,000 runs is whole, never
			// "more"). Only here: below it a count that fell between
			// pages (a concurrent delete) would end the walk early.
			if len(runs) >= total {
				break
			}
			return tooManyRuns(), c, nil
		}
		page, err := s.db.Runs(ctx, q)
		if err != nil {
			slog.DebugContext(ctx, "studio: a session's runs could not be read to price it", "session", id, "err", err)
			return cost{badge: costUnknown, reason: "the session's runs could not be read"}, c, nil
		}
		if pages == 0 && page.Total > sessionCostPages*500 {
			return tooManyRuns(), c, nil // the count says so: no 19 reads more
		}
		total = page.Total
		runs = append(runs, page.Runs...)
		// The last page: no cursor, or an empty page.
		if page.NextBefore == nil || len(page.Runs) == 0 {
			break
		}
		q.Before, q.BeforeID = *page.NextBefore, page.NextBeforeID
	}
	return c.setCost(runs, "turn"), c, runs
}

// sessionCostBatch is how many sessions one batched runs read of
// sessionCosts starts with.
const sessionCostBatch = 50

// sessionCosts is sessionCost for several sessions at once: their runs
// in batched paged reads (RunQuery.SessionIDs; the same filters, pub's
// scope when set), each session's set priced by setCost on a coster of
// its own — what sessionCost answers per session, a read per page of a
// batch rather than per session. A batch holds sessionCostBatch
// sessions; one whose runs pass sessionCostPages × 500 (its first
// page's Total says so) is halved and each half read again, down to one
// session, so a session is priced whole up to sessionCost's own bound
// and a level of many small sessions prices every one. The whole call
// reads at most sessionCostPages Runs pages per batch it starts with,
// and sessionCostPages more for the halvings (sessionCostReads: 40 for
// 50 sessions, 220 for 500), so a level of sessions every one past the
// bound costs that budget, never O(sessions × 20). Every id gets an answer: a session
// alone past the bound, or one whose read fails, is unknown with
// sessionCost's reason; once the budget is spent the sessions not yet
// priced are unknown with costReadBudget's.
func (s *Server) sessionCosts(ctx context.Context, ids []string, scope readScope) map[string]cost {
	out := make(map[string]cost, len(ids))
	n := sessionCostReads(len(ids))
	budget := &readBudget{left: n, total: n}
	for len(ids) > 0 {
		n := min(sessionCostBatch, len(ids))
		s.priceSessionBatch(ctx, ids[:n], scope, out, budget)
		ids = ids[n:]
	}
	return out
}

// readBudget is sessionCosts' read budget: the Runs pages left to read,
// of total.
type readBudget struct{ left, total int }

// sessionCostReads is sessionCosts' read budget for n sessions:
// sessionCostPages Runs pages per batch of sessionCostBatch, and one
// batch's more for the halvings (a batch of 10,500 runs reads 23).
func sessionCostReads(n int) int {
	return sessionCostPages * ((n+sessionCostBatch-1)/sessionCostBatch + 1)
}

// costReadBudget is the cost of a session sessionCosts' read budget —
// reads Runs pages of 500, sessionCostReads' figure — left unpriced.
func costReadBudget(reads int) cost {
	return cost{badge: costUnknown, reason: fmt.Sprintf("the delegated sessions hold too many runs to price at read time (%d pages of 500 read)", reads)}
}

// priceSessionBatch prices one batch of sessionCosts into out, halving
// it when its runs pass sessionCost's bound, each read taken from
// budget; with the budget spent the batch's sessions are unknown.
func (s *Server) priceSessionBatch(ctx context.Context, ids []string, scope readScope, out map[string]cost, budget *readBudget) {
	f := false
	q := obsdb.RunQuery{SessionIDs: ids, PublicID: scope.PublicID, Meta: scope.Tags, ParentRunID: "*", Playground: &f, Limit: 500}
	bySession := make(map[string][]obsdb.RunRow, len(ids))
	read, total := 0, 0
	for pages := 0; ; pages++ {
		if pages == sessionCostPages {
			// The bound: whole only when the last count says every run
			// was read (exactly 10,000 runs are whole, never "more");
			// else the count grew past it while the batch was read.
			// Only here: below it a count that fell between pages (a
			// concurrent delete) would end the walk early.
			if read >= total {
				break
			}
			s.splitSessionBatch(ctx, ids, scope, out, budget)
			return
		}
		if budget.left <= 0 {
			for _, id := range ids {
				out[id] = costReadBudget(budget.total)
			}
			return
		}
		budget.left--
		page, err := s.db.Runs(ctx, q)
		if err != nil {
			slog.DebugContext(ctx, "studio: delegated sessions' runs could not be read to price them", "sessions", len(ids), "err", err)
			for _, id := range ids {
				out[id] = cost{badge: costUnknown, reason: "the session's runs could not be read"}
			}
			return
		}
		if pages == 0 && page.Total > sessionCostPages*500 {
			s.splitSessionBatch(ctx, ids, scope, out, budget)
			return
		}
		for _, r := range page.Runs {
			bySession[r.SessionID] = append(bySession[r.SessionID], r)
		}
		read, total = read+len(page.Runs), page.Total
		// The last page: no cursor, or an empty page.
		if page.NextBefore == nil || len(page.Runs) == 0 {
			break
		}
		q.Before, q.BeforeID = *page.NextBefore, page.NextBeforeID
	}
	for _, id := range ids {
		runs := bySession[id]
		if runs == nil {
			runs = []obsdb.RunRow{}
		}
		out[id] = s.coster(ctx).setCost(runs, "turn")
	}
}

// splitSessionBatch is priceSessionBatch past the bound: two halves
// read again, or one session's tooManyRuns.
func (s *Server) splitSessionBatch(ctx context.Context, ids []string, scope readScope, out map[string]cost, budget *readBudget) {
	if len(ids) == 1 {
		out[ids[0]] = tooManyRuns()
		return
	}
	half := len(ids) / 2
	s.priceSessionBatch(ctx, ids[:half], scope, out, budget)
	s.priceSessionBatch(ctx, ids[half:], scope, out, budget)
}

// setCost is the bill of a set of runs read whole — a session's, an
// experiment's — with their subagent children among them: each root's
// own + delegated, where a root is every run of the set that is not a
// core Subagent child of another run in it. It primes c with the tree,
// so pricing the roots costs no children reads. noun names a root in
// the reason ("turn", "run": tally's words). A set whose every root is
// a scripted model's, its subagents $0, is the scripted badge's exact $0.
func (c *coster) setCost(runs []obsdb.RunRow, noun string) cost {
	kids := map[string][]obsdb.RunRow{}
	inSet := map[string]bool{}
	for _, r := range runs {
		inSet[r.ID] = true
		if r.ParentRunID != "" {
			kids[r.ParentRunID] = append(kids[r.ParentRunID], r)
		}
	}
	for _, r := range runs {
		c.prime(r.ID, kids[r.ID])
	}
	var usage core.Usage
	var parts []runCost
	allScripted := true
	for _, r := range runs {
		if inSet[r.ParentRunID] && strings.HasPrefix(r.ID, r.ParentRunID+"/") {
			continue // a Subagent child: in its parent's delegated figure
		}
		usage = usage.Add(r.Usage)
		rc := c.cost(r)
		rc.id = r.ID
		parts = append(parts, rc)
		allScripted = allScripted && rc.own.badge == costScripted && rc.delegated.known && rc.delegated.usd == 0
	}
	out := tally(parts, noun, false)
	out.cache = cacheOf(usage)
	switch {
	case len(parts) > 0 && allScripted:
		// Every root a scripted model's — the playground's scripted
		// engine reports zero usage, so this precedes the zero case:
		// the set's $0 is exact too.
		out.badge, out.reason, out.cache = costScripted, scriptedReason, cacheExact
	case usage == (core.Usage{}):
		// No usage at all (a run-less experiment): a $0 that nothing
		// can have overstated, so no cache note.
		out.cache = cacheExact
	}
	return out
}

// ── api/meta and GET /api/settings/pricing ─────────────────────────

// metaPricing is api/meta's pricing block.
type metaPricing struct {
	Source string `json:"source"`
	// Path is the override file's, under db.path's rule (mayReadDBPath);
	// absent for the default table, a Go-built one, or another caller.
	Path   string `json:"path,omitempty"`
	Dated  string `json:"dated"`
	Models int    `json:"models"`
}

func (s *Server) metaPricing(r *http.Request) metaPricing {
	out := metaPricing{Source: s.pricing.source, Dated: s.pricing.Dated, Models: len(s.pricing.Models)}
	if mayReadDBPath(s, r) {
		out.Path = s.pricing.path
	}
	return out
}

// pricingDoc is GET /api/settings/pricing: the effective table in the
// override file's shape (so it saves as a WEFT_PRICING file and loads
// back — LoadPricing accepts the source and path keys), plus where it
// came from.
type pricingDoc struct {
	Source string       `json:"source"`
	Path   string       `json:"path,omitempty"`
	Dated  string       `json:"dated"`
	Note   string       `json:"note,omitempty"`
	Models []ModelPrice `json:"models"`
}

// servePricing answers GET /api/settings/pricing (capability pricing):
// every valid identity may read it — prices are not secrets — and the
// file's path follows db.path's rule.
func (s *Server) servePricing(w http.ResponseWriter, r *http.Request) {
	doc := pricingDoc{Source: s.pricing.source, Dated: s.pricing.Dated, Note: s.pricing.Note, Models: s.pricing.Models}
	if doc.Models == nil {
		doc.Models = []ModelPrice{}
	}
	if mayReadDBPath(s, r) {
		doc.Path = s.pricing.path
	}
	writeJSON(w, r, http.StatusOK, doc)
}

// pricingGroup is the settings route's group (capability pricing).
func pricingGroup() routeGroup {
	return routeGroup{
		name:       "pricing",
		capability: "pricing",
		register: func(mux *http.ServeMux, s *Server) {
			mux.HandleFunc("GET /api/settings/pricing", s.servePricing)
		},
	}
}
