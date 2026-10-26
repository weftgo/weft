package obsdb

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/weftgo/weft/core"
)

// Aggregator is the optional aggregates capability of a DB (UX plan
// D7, A4, A2): numbers across runs rather than one run's records. Both
// first-party backends implement it — obsdb/sqlite over its span_stats
// rollup (a derived index written at ingest beside every stored weft
// span, rebuildable from the spans table: migration 0023), obsdb/clickhouse
// with native GROUP BY over otel_traces and weft_runs, no table of its
// own — and the obsdbtest conformance table holds them to the same
// answers. A backend that does not implement it is a database without
// aggregates: Studio then leaves its stats capability off and says why.
type Aggregator interface {
	// Stats answers StatsQuery: one StatsGroup per key of q.By over the
	// runs started in [q.Since, q.Until) that q.RunQuery's filters keep,
	// with a per-bucket series. ErrInvalidStats for a query outside
	// CheckStats' bounds.
	Stats(ctx context.Context, q StatsQuery) (StatsPage, error)
	// ErrorCounts counts the distinct error texts (RunRow.Err, never
	// "") of the runs q matches — Searcher.Search's match exactly, its
	// sort, offset and limit not read — most frequent first (ties by
	// text), at most MaxErrorTexts of them; capped is true when the
	// match held more distinct texts than that. ErrorClasses folds the
	// answer into the error_class facet, ErrorClassTexts resolves one
	// class back to its texts (RunQuery.Errs).
	ErrorCounts(ctx context.Context, q SearchQuery) (counts []ErrorCount, capped bool, err error)
}

// StatsBy is what Stats groups by.
type StatsBy string

const (
	// StatsByAgent groups runs by their agent name (RunRow.Agent; ""
	// for an unnamed agent).
	StatsByAgent StatsBy = "agent"
	// StatsByErrorType groups the failed runs by RunRow.ErrorType (""
	// for a failure whose root carried none).
	StatsByErrorType StatsBy = "error_type"
	// StatsByModel groups model calls (chat spans) by the model they
	// asked for (gen_ai.request.model).
	StatsByModel StatsBy = "model"
	// StatsByTool groups tool calls (execute_tool spans) by tool name.
	StatsByTool StatsBy = "tool"
	// StatsByStep groups model calls by their step index
	// (weft.step.index, the key its decimal string).
	StatsByStep StatsBy = "step"
)

// Stats bounds.
const (
	// StatsBuckets is the series' default bucket count: 24 — an hour a
	// bucket over a day's window, seven hours over a week's.
	StatsBuckets = 24
	// MaxStatsBuckets bounds StatsQuery.Buckets.
	MaxStatsBuckets = 366
	// MaxStatsGroups bounds a StatsPage's groups: past it the groups
	// with the fewest runs (then calls) are cut, and StatsPage.GroupsCut
	// says how many.
	MaxStatsGroups = 200
)

// The window's bounds: the years Unix nanoseconds hold in an int64
// (2262 up to its April 11th; past that instant a time is refused too).
const (
	MinStatsYear = 1678
	MaxStatsYear = 2262
)

// ErrInvalidStats is returned by CheckStats and Stats for a query
// outside its bounds; Studio answers it 400.
var ErrInvalidStats = errors.New("obsdb: invalid stats query")

// StatsQuery selects what Stats aggregates. Its RunQuery carries the
// filters /api/runs reads — agent, public id (the panel token's scope),
// metadata (tag.<k>), parent ("*" every run, children included; "" the
// top-level runs only), playground, experiment, status — with the same
// meaning; RunQuery's Before, BeforeID, Limit and Oldest are not read.
// A derived run (RunRow.Derived: built from stock spans) counts as a
// run — so a group's Runs is the runs list's count under the same
// filters — with no calls: it has no weft spans to aggregate.
//
// The window is the runs' start: a run is in when Since ≤ Started <
// Until, and every span of a run in the window is counted with it, in
// its run's bucket — so a run's model calls are never split across
// buckets and Runs sums over the series.
type StatsQuery struct {
	RunQuery
	By           StatsBy
	Since, Until time.Time // both required, Since before Until
	Buckets      int       // 0 = StatsBuckets, max MaxStatsBuckets
}

// CheckStats validates a StatsQuery: a known By, a window, a bucket
// count in bounds. It returns the bucket count and width — the window
// cut into Buckets equal parts, the width rounded up to a whole
// nanosecond so the last bucket ends at or after Until.
func CheckStats(q StatsQuery) (buckets int, width time.Duration, err error) {
	switch q.By {
	case StatsByAgent, StatsByErrorType, StatsByModel, StatsByTool, StatsByStep:
	default:
		return 0, 0, fmt.Errorf("%w: by must be agent, model, tool, error_type or step", ErrInvalidStats)
	}
	if q.Since.IsZero() || q.Until.IsZero() || !q.Since.Before(q.Until) {
		return 0, 0, fmt.Errorf("%w: the window needs since before until", ErrInvalidStats)
	}
	// The window is compared in Unix nanoseconds: an int64 holds
	// 1678-2262, and a time outside it would wrap.
	for _, t := range []time.Time{q.Since, q.Until} {
		if y := t.UTC().Year(); y < MinStatsYear || y > MaxStatsYear || !time.Unix(0, t.UnixNano()).Equal(t) {
			return 0, 0, fmt.Errorf("%w: since and until must lie in the years %d..%d", ErrInvalidStats, MinStatsYear, MaxStatsYear)
		}
	}
	buckets = q.Buckets
	if buckets == 0 {
		buckets = StatsBuckets
	}
	if buckets < 1 || buckets > MaxStatsBuckets {
		return 0, 0, fmt.Errorf("%w: buckets must be 1..%d", ErrInvalidStats, MaxStatsBuckets)
	}
	span := q.Until.Sub(q.Since)
	width = (span + time.Duration(buckets) - 1) / time.Duration(buckets)
	return buckets, max(width, 1), nil
}

// RunLevel says the groups of by are runs (agent, error_type) rather
// than spans (model, tool, step).
func (by StatsBy) RunLevel() bool { return by == StatsByAgent || by == StatsByErrorType }

// StatsPage is one Stats answer: the window and its buckets as read,
// the groups — most runs first, then most calls, then by key (by=step:
// by step index, ascending) — and how many groups MaxStatsGroups cut.
type StatsPage struct {
	By           StatsBy
	Since, Until time.Time
	Buckets      int
	Bucket       time.Duration // each bucket's width
	Groups       []StatsGroup
	GroupsCut    int
}

// StatsGroup is one key's numbers over the window. What a field
// counts depends on the query's By:
//
//   - Runs: the runs in the group (agent, error_type: the runs; model,
//     tool, step: the distinct runs that made a call in it). Failed:
//     those of them whose run failed (RunRow.Status failed).
//   - Calls: model calls (chat spans) — the group's runs' for agent and
//     error_type, the group's own for model and step — or tool calls
//     (execute_tool spans) for tool; a summarizer's call (weft.chat.role)
//     and a call a resume denied (weft.tool.denied, never run) are none.
//     FailedCalls: those that failed — status Error; for a tool call
//     the record contract's tool_errors rule (DENIED, RETRY and the
//     context errors are no failure).
//   - Retried: the model calls that took more than one attempt (their
//     attempt spans' highest weft.attempt.index above 1); 0 for tool.
//   - Denials: the record contract's problems.denied rule over the
//     group's spans — execute_tool spans with error.type DENIED plus
//     each invoke_agent span's weft.run.denied (agent, error_type, tool;
//     0 for model and step, whose spans carry no denial).
//   - Usage and Models: the model calls' own usage (chat spans'
//     gen_ai.usage.*), split by the provider and model each call asked
//     for, sorted by provider then model — never a run's rolled-up
//     total, so a subagent's tokens are its own agent's, not its
//     parent's too. Zero for tool.
//   - Duration: run durations (Finished − Started, the runs that
//     finished) for agent and error_type; the calls' span durations for
//     model, tool and step. TTFT: the model calls' weft.ttft_ms (none
//     for tool). Both in milliseconds, nearest-rank percentiles.
//   - Delegated, DelegatedModels and DelegatedRuns (by=agent alone):
//     the model calls of the runs below the group's runs in their
//     Subagent trees (core child ids: <run id>/<step>/<call>, at any
//     depth) whose own agent is another — what the agent's subagents
//     spent for it, apart from its own Usage — each call once however
//     many of the group's runs it sits under, and how many such runs
//     made a call. A descendant past a panel token's public id is not
//     read. Zero for every other By. A call can sit in several groups'
//     delegated figures (boss → helper → boss: the grandchild's call is
//     helper's delegated, and the helper's call boss's), so delegated
//     figures are never summed across groups — the groups' own Usage
//     sums to the true total.
//   - Series: the same sums per bucket of the window, Buckets of them.
type StatsGroup struct {
	Key                         string
	Runs, Failed                int64
	Calls, FailedCalls, Retried int64
	Denials                     int64
	Usage                       core.Usage
	Models                      []ModelUsage
	Delegated                   core.Usage
	DelegatedModels             []ModelUsage
	DelegatedRuns               int64
	Duration, TTFT              Percentiles
	Series                      []StatsBucket
}

// Percentiles are nearest-rank percentiles over N values (the value at
// rank ⌈p·N/100⌉ of the sorted list — both backends compute exactly
// this, so their answers are equal, not close). Zero N: no value, P50
// and P95 zero.
type Percentiles struct {
	P50, P95 float64
	N        int64
}

// StatsBucket is one bucket of a group's series: the sums StatsGroup
// names, over the runs that started in the bucket.
type StatsBucket struct {
	Runs, Failed, Calls, FailedCalls int64
	InputTokens, OutputTokens        int64
}

// StatsCell is one group's sums in one bucket, as a backend's GROUP BY
// answers them; BuildStats adds the cells of one (Key, Bucket) together
// (a run-side cell and a span-side cell, say). Bucket is clamped into
// the series.
type StatsCell struct {
	Key                                       string
	Bucket                                    int
	Runs, Failed, Calls, FailedCalls, Retried int64
	Denials                                   int64
	InputTokens, OutputTokens                 int64
	CachedInputTokens, CacheWriteTokens       int64
	ReasoningTokens                           int64
	// DelegatedRuns is summed into the group, never into a bucket.
	DelegatedRuns int64
}

// StatsModelPart is one group's usage on one provider/model; with
// Delegated, its subagents' (StatsGroup.DelegatedModels).
type StatsModelPart struct {
	Key       string
	Delegated bool
	ModelUsage
}

// StatsPercentiles is one group's duration and TTFT percentiles.
type StatsPercentiles struct {
	Key            string
	Duration, TTFT Percentiles
}

// AttemptIndex is an attempt span's weft.attempt.index as every
// aggregate reads it: an integer (a whole float64 is one — JSON decodes
// numbers so), or a string of decimal digits, the spelling a stock
// collector's stringified attributes carry. Anything else is no index
// (ok false). SQLite's span_stats derivation and ClickHouse's statsSpans
// restate the rule; the conformance suite's Stats case pins all three.
func AttemptIndex(attrs map[string]any) (int64, bool) {
	switch v := attrs["weft.attempt.index"].(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		if v == float64(int64(v)) {
			return int64(v), true
		}
	case string:
		if v != "" && strings.Trim(v, "0123456789") == "" {
			n, err := strconv.ParseInt(v, 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// PercentileRank is the 1-based nearest rank of percentile p over n
// sorted values, ⌈p·n/100⌉ in integers: the rank both backends' SQL
// reads ((n·p + 99) / 100).
func PercentileRank(p, n int64) int64 { return (n*p + 99) / 100 }

// BuildStats is a backend's answer assembled from its GROUP BY rows:
// the cells summed per key and bucket, the model parts and percentiles
// attached, the groups sorted and cut at MaxStatsGroups. A key present
// in models or pcts alone still gets its group. One rule for every
// backend, so their pages differ only where their rows do.
func BuildStats(q StatsQuery, cells []StatsCell, models []StatsModelPart, pcts []StatsPercentiles) StatsPage {
	buckets, width, _ := CheckStats(q)
	page := StatsPage{By: q.By, Since: q.Since, Until: q.Until, Buckets: buckets, Bucket: width}
	byKey := map[string]*StatsGroup{}
	group := func(k string) *StatsGroup {
		g := byKey[k]
		if g == nil {
			g = &StatsGroup{Key: k, Series: make([]StatsBucket, buckets)}
			byKey[k] = g
		}
		return g
	}
	for _, c := range cells {
		g := group(c.Key)
		g.Runs += c.Runs
		g.Failed += c.Failed
		g.Calls += c.Calls
		g.FailedCalls += c.FailedCalls
		g.Retried += c.Retried
		g.Denials += c.Denials
		g.Usage.InputTokens += c.InputTokens
		g.Usage.OutputTokens += c.OutputTokens
		g.Usage.CachedInputTokens += c.CachedInputTokens
		g.Usage.CacheWriteTokens += c.CacheWriteTokens
		g.Usage.ReasoningTokens += c.ReasoningTokens
		g.DelegatedRuns += c.DelegatedRuns
		b := &g.Series[min(max(c.Bucket, 0), buckets-1)]
		b.Runs += c.Runs
		b.Failed += c.Failed
		b.Calls += c.Calls
		b.FailedCalls += c.FailedCalls
		b.InputTokens += c.InputTokens
		b.OutputTokens += c.OutputTokens
	}
	for _, m := range models {
		g := group(m.Key)
		if m.Delegated {
			g.DelegatedModels = append(g.DelegatedModels, m.ModelUsage)
			continue
		}
		g.Models = append(g.Models, m.ModelUsage)
	}
	for _, p := range pcts {
		g := group(p.Key)
		g.Duration, g.TTFT = p.Duration, p.TTFT
	}
	for _, g := range byKey {
		_, g.Models = SumModelUsage(g.Models)
		g.Delegated, g.DelegatedModels = SumModelUsage(g.DelegatedModels)
		page.Groups = append(page.Groups, *g)
	}
	slices.SortFunc(page.Groups, func(a, b StatsGroup) int {
		if q.By == StatsByStep {
			ai, _ := strconv.Atoi(a.Key)
			bi, _ := strconv.Atoi(b.Key)
			if c := cmp.Compare(ai, bi); c != 0 {
				return c
			}
			return strings.Compare(a.Key, b.Key)
		}
		if c := cmp.Compare(b.Runs, a.Runs); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Calls, a.Calls); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
	if len(page.Groups) > MaxStatsGroups {
		page.GroupsCut = len(page.Groups) - MaxStatsGroups
		page.Groups = page.Groups[:MaxStatsGroups]
	}
	return page
}

// The error-class facet (UX plan A2, UX-C-37): failures grouped by
// their error text with the numbers and ids taken out, so the seven
// runs that exceeded their usage limit by different token counts are
// one class. Computed at read time from RunRow.Err — nothing stored —
// on every backend alike: ErrorCounts reads the distinct texts, Go
// normalises them.

// MaxErrorTexts bounds the distinct error texts ErrorCounts reads for
// one facet or class filter: past it the rarest texts are not read, and
// the answer says capped.
const MaxErrorTexts = 2000

// MaxErrorClassBytes caps an error class: a longer one is cut at a rune
// boundary and ends in "…" — which keeps every class inside the search
// filters' MaxSearchTermBytes, so it is usable as a filter value.
const MaxErrorClassBytes = 240

// ErrorCount is one distinct error text and how many runs carry it.
type ErrorCount struct {
	Err   string
	Count int64
}

// ErrorClassCount is one error class and how many runs' texts fold into
// it.
type ErrorClassCount struct {
	Class string
	Count int64
}

// ErrorClass normalises an error text into its class. The text is read
// as words — runs of letters, digits and _ . - — and the rest kept as
// it stands:
//
//   - a word that is a number (digits with optional . or , groups and
//     a sign) becomes N, one with a unit suffix (ms, s, KiB, %, …) N
//     followed by the unit: "input 30/25, output 15/0" reads
//     "input N/N, output N/N", "after 1.5s" "after Ns";
//   - a word that is an id becomes <id>: a UUID, a hex string of 8 or
//     more characters with a digit (a run id, a trace id), or a word
//     one of whose _ / - separated parts is 8 or more letters and
//     digits mixed (call_8f3a9b2c11, toolu_01ABCdefGH);
//   - any other word stays — a model name like glm-5.3-flash keeps its
//     digits;
//   - a double-quoted or backquoted span is kept verbatim, quotes
//     included (a tool's name in %q), an unclosed quote to the end.
//
// The class is trimmed and cut at MaxErrorClassBytes. "" for "".
func ErrorClass(err string) string {
	var b strings.Builder
	i := 0
	for i < len(err) {
		c := err[i]
		switch {
		case c == '"' || c == '`':
			j := strings.IndexByte(err[i+1:], c)
			end := len(err)
			if j >= 0 {
				end = i + 1 + j + 1
			}
			b.WriteString(err[i:end])
			i = end
		case wordByte(c):
			j := i
			for j < len(err) && wordByte(err[j]) {
				j++
			}
			b.WriteString(classWord(err[i:j]))
			i = j
		default:
			_, size := utf8.DecodeRuneInString(err[i:])
			b.WriteString(err[i : i+size])
			i += size
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > MaxErrorClassBytes {
		cut := MaxErrorClassBytes
		for cut > 0 && !utf8.RuneStart(out[cut]) {
			cut--
		}
		out = out[:cut] + "…"
	}
	return out
}

func wordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-'
}

// units are the suffixes a number keeps (ErrorClass): durations, sizes.
var units = []string{"ns", "us", "ms", "s", "m", "h", "B", "KB", "MB", "GB", "KiB", "MiB", "GiB", "k", "x"}

func classWord(w string) string {
	// Trailing sentence punctuation is no part of the word.
	body := strings.TrimRight(w, ".")
	tail := w[len(body):]
	if body == "" {
		return w
	}
	if isNumber(body) {
		return "N" + tail
	}
	for _, u := range units {
		if n, ok := strings.CutSuffix(body, u); ok && n != "" && isNumber(n) {
			return "N" + u + tail
		}
	}
	if isID(body) {
		return "<id>" + tail
	}
	return w
}

// isNumber: an optional sign, digits, and further digit groups after
// '.' or ','.
func isNumber(s string) bool {
	s = strings.TrimPrefix(s, "-")
	if s == "" {
		return false
	}
	digits := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits++
		case (c == '.' || c == ',') && i > 0 && i < len(s)-1 && s[i-1] != '.' && s[i-1] != ',':
		default:
			return false
		}
	}
	return digits > 0
}

// isID: a UUID, a long hex string with a digit, or a word with a long
// mixed letters-and-digits part.
func isID(s string) bool {
	if len(s) == 36 && s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-' && isHex(strings.ReplaceAll(s, "-", "")) {
		return true
	}
	if len(s) >= 8 && isHex(s) && strings.ContainsAny(s, "0123456789") {
		return true
	}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' || r == '.' }) {
		if len(part) < 8 {
			continue
		}
		var letters, digits bool
		for i := 0; i < len(part); i++ {
			switch c := part[i]; {
			case c >= '0' && c <= '9':
				digits = true
			default:
				letters = true
			}
		}
		if letters && digits {
			return true
		}
	}
	return false
}

// ErrorClasses folds error counts into classes (ErrorClass), most runs
// first, ties by class; at most limit of them (0 = all).
func ErrorClasses(counts []ErrorCount, limit int) []ErrorClassCount {
	by := map[string]int64{}
	for _, c := range counts {
		if c.Err == "" {
			continue
		}
		by[ErrorClass(c.Err)] += c.Count
	}
	out := make([]ErrorClassCount, 0, len(by))
	for k, n := range by {
		out = append(out, ErrorClassCount{Class: k, Count: n})
	}
	slices.SortFunc(out, func(a, b ErrorClassCount) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return strings.Compare(a.Class, b.Class)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ErrorClassTexts is the texts among counts whose class is class, in
// counts' order: RunQuery.Errs for an error_class filter. Never nil — no
// text of the class is an empty set, which matches no run.
func ErrorClassTexts(counts []ErrorCount, class string) []string {
	out := []string{}
	for _, c := range counts {
		if c.Err != "" && ErrorClass(c.Err) == class {
			out = append(out, c.Err)
		}
	}
	return out
}
