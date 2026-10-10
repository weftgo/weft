package studio

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
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
// is looked up as the adapter's ("google"). input and output are
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
// cost_reason naming why. A running run is "unknown" ("running: usage
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
// or running). Other runs linked to it by weft.parent.run.id — a
// thread/pool child session's run, another agent's Generate inside a
// tool handler — were never rolled up: they are neither subtracted nor
// delegated, and carry their own figures on their own rows.
//
// A session row's cost_usd is its turns' own + delegated (sessionCost:
// every run of the session that is not a Subagent child of another of
// its runs — a pool child session's run is a turn of the child
// session). The pool child's cost is on its own session, not on the
// parent session's row (thread's Usage.Delegated is in the session
// file, not in obsdb). A step's cost_usd prices its own model call
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
// silent fallback to the default. The table it returns reports source
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
	Dated  *string           `json:"dated"`
	Note   string            `json:"note"`
	Models *[]modelPriceFile `json:"models"`
	Source string            `json:"source"`
	Path   string            `json:"path"`
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
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var f pricingFileShape
	if err := dec.Decode(&f); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) && te.Field != "" {
			return fail(indexRe.ReplaceAllString(te.Field, "[$1]"), "want a %s, got %s", te.Type, te.Value)
		}
		if msg, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			return fail(strings.Trim(msg, `"`), "unknown field (the shape is {dated, note, models: [{provider, model_glob, input, output, cached_input, cache_write, reasoning}]})")
		}
		return PricingTable{}, fmt.Errorf("pricing %s: does not parse: %w", name, err)
	}
	if dec.More() {
		return PricingTable{}, fmt.Errorf("pricing %s: does not parse: more than one JSON value", name)
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
	for i, m := range *f.Models {
		at := func(field string) string { return "models[" + strconv.Itoa(i) + "]." + field }
		if m.Provider == nil || *m.Provider == "" {
			return fail(at("provider"), "missing")
		}
		if m.ModelGlob == nil || *m.ModelGlob == "" {
			return fail(at("model_glob"), "missing")
		}
		if _, err := path.Match(*m.Provider+"/"+*m.ModelGlob, ""); err != nil {
			return fail(at("model_glob"), "%q is not a path.Match pattern: %v", *m.Provider+"/"+*m.ModelGlob, err)
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

// lookup is the first row matching provider/model.
func (t *PricingTable) lookup(provider, model string) (ModelPrice, bool) {
	key := pricingProvider(provider) + "/" + model
	for _, m := range t.Models {
		if ok, _ := path.Match(m.Provider+"/"+m.ModelGlob, key); ok {
			return m, true
		}
	}
	return ModelPrice{}, false
}

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
)

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
}

// delegatedFields is a run's subagent bill beside its own.
type delegatedFields struct {
	DelegatedCostUSD    *float64 `json:"delegated_cost_usd"`
	DelegatedCostKnown  bool     `json:"delegated_cost_known"`
	DelegatedCostReason string   `json:"delegated_cost_reason,omitempty"`
}

// cost is one computed figure before it goes on the wire.
type cost struct {
	usd           float64
	known         bool
	badge, reason string
	cache         string
}

// fields is c on the wire: rounded, null when unknown.
func (c cost) fields() costFields {
	out := costFields{CostKnown: c.known, CostBadge: c.badge, CostReason: c.reason, Cache: c.cache}
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
	if !ok {
		out.badge = costUnpriced
		out.reason = "no row of the " + t.source + " pricing table matches " + provider + "/" + model
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
}

// runCost is one run's own and delegated figures.
type runCost struct {
	own, delegated cost
}

// total is own + delegated: the run's whole bill (search's sort key).
func (rc runCost) total() (float64, bool) {
	return rc.own.usd + rc.delegated.usd, rc.own.known && rc.delegated.known
}

func (s *Server) coster(ctx context.Context) *coster {
	return &coster{ctx: ctx, db: s.db, table: s.pricing, kids: map[string][]obsdb.RunRow{}, memo: map[string]runCost{}}
}

// prime records a run's children already read (a run document's).
func (c *coster) prime(id string, kids []obsdb.RunRow) {
	if kids == nil {
		kids = []obsdb.RunRow{}
	}
	c.kids[id] = kids
}

// costerChildPages bounds the children walk of one run: 20 pages of 500.
const costerChildPages = 20

// children is the run's direct subagent children.
func (c *coster) children(id string) ([]obsdb.RunRow, error) {
	if k, ok := c.kids[id]; ok {
		return k, nil
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
		why := "the run's subagent children could not be read, so its own usage cannot be told from theirs"
		return runCost{
			own:       cost{badge: costUnknown, reason: why, cache: cacheOf(rec.Usage)},
			delegated: cost{badge: costUnknown, reason: why},
		}
	}
	own := rec.Usage
	del := cost{known: true}
	var unpriced []string
	for _, k := range subagents(rec.ID, all) {
		own = sub(own, k.Usage)
		kc := c.cost(k)
		del.usd += kc.own.usd + kc.delegated.usd
		if !kc.own.known {
			unpriced = append(unpriced, k.ID+" ("+kc.own.reason+")")
		}
		if !kc.delegated.known {
			unpriced = append(unpriced, k.ID+"'s subagents ("+kc.delegated.reason+")")
		}
	}
	if len(unpriced) > 0 {
		del = cost{badge: costUnpriced, reason: "a subagent run is not priced: " + strings.Join(unpriced, "; ")}
	}
	if why := usagePending(rec); why != "" {
		return runCost{own: cost{badge: costUnknown, reason: why, cache: cacheOf(own)}, delegated: del}
	}
	return runCost{own: c.table.price(rec.Provider, rec.Model, own), delegated: del}
}

// subagents is the core Subagent children among a run's parent-linked
// runs: those whose id is <parent>/<step>/<call> (core's childRunID) —
// the only ones whose usage the core rolled into the parent's (rule
// 13). A thread/pool child session's run, or another agent's Generate
// called inside a tool handler, is linked by weft.parent.run.id too but
// was never rolled up: neither subtracted nor counted as delegated.
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
// unset: the same records set both) — carries none to price. "" when
// the usage is recorded.
func usagePending(rec obsdb.RunRow) string {
	switch {
	case rec.Status == obsdb.StatusRunning:
		return "running: usage arrives at run end"
	case rec.Finished == nil:
		return "the run's end (run_finish or its invoke_agent span) never arrived: its usage is not recorded"
	}
	return ""
}

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
}

func (c cost) delegatedFields() delegatedFields {
	out := delegatedFields{DelegatedCostKnown: c.known, DelegatedCostReason: c.reason}
	if c.known {
		v := round6(c.usd)
		out.DelegatedCostUSD = &v
	}
	return out
}

// sessionCostPages bounds a session's runs walk: 20 pages of 500.
const sessionCostPages = 20

// sessionCost is a session's cost: its turns' own + delegated, from
// one walk of every run carrying the session id (Subagent children
// inherit it), so the tree costs no children reads. A turn is every run
// of the set that is not a core Subagent child of another run in it: a
// top-level turn, a thread/pool child session's run (its parent run is
// in the parent's session), and another agent's Generate inside a tool
// handler (linked but never rolled up, so no other figure carries it).
// The coster it returns is primed with that tree for the turn rows.
func (s *Server) sessionCost(ctx context.Context, id string) (cost, *coster) {
	c := s.coster(ctx)
	f := false
	q := obsdb.RunQuery{SessionID: id, ParentRunID: "*", Playground: &f, Limit: 500}
	var runs []obsdb.RunRow
	for pages := 0; ; pages++ {
		if pages == sessionCostPages {
			return cost{badge: costUnknown, reason: fmt.Sprintf("the session holds more than %d runs: too many to price at read time", sessionCostPages*500)}, c
		}
		page, err := s.db.Runs(ctx, q)
		if err != nil {
			return cost{badge: costUnknown, reason: "the session's runs could not be read"}, c
		}
		runs = append(runs, page.Runs...)
		if page.NextBefore == nil || len(page.Runs) == 0 {
			break
		}
		q.Before, q.BeforeID = *page.NextBefore, page.NextBeforeID
	}
	return c.setCost(runs, "a turn"), c
}

// setCost is the bill of a set of runs read whole — a session's, an
// experiment's — with their subagent children among them: each root's
// own + delegated, where a root is every run of the set that is not a
// core Subagent child of another run in it. It primes c with the tree,
// so pricing the roots costs no children reads. what names a root in
// the unpriced reason ("a turn", "a run").
func (c *coster) setCost(runs []obsdb.RunRow, what string) cost {
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
	out := cost{known: true}
	var usage core.Usage
	var unpriced []string
	badge := costUnpriced
	for _, r := range runs {
		if inSet[r.ParentRunID] && strings.HasPrefix(r.ID, r.ParentRunID+"/") {
			continue // a Subagent child: in its parent's delegated figure
		}
		usage = usage.Add(r.Usage)
		rc := c.cost(r)
		out.usd += rc.own.usd + rc.delegated.usd
		if !rc.own.known {
			unpriced = append(unpriced, r.ID+" ("+rc.own.reason+")")
			if rc.own.badge == costUnknown {
				badge = costUnknown
			}
		}
		if !rc.delegated.known {
			unpriced = append(unpriced, r.ID+"'s subagents ("+rc.delegated.reason+")")
		}
	}
	out.cache = cacheOf(usage)
	if len(unpriced) > 0 {
		out.known, out.usd = false, 0
		out.badge, out.reason = badge, what+" is not priced: "+strings.Join(unpriced, "; ")
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
