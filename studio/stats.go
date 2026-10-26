package studio

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
)

// GET /api/stats (UX plan A4, D7; UX-E-25, UX-A-14): numbers across
// runs — runs, failures, tokens, cost, latency, TTFT, retries, denials —
// grouped by agent, model, tool, error type or step, with a series
// over the window for sparklines. Read through the database's
// obsdb.Aggregator (SQLite's span_stats rollup written at ingest,
// ClickHouse's native GROUP BY); capability "stats", present when the
// database aggregates — which also turns on /api/search's error_class
// facet and the error_class filter on /api/search and /api/runs.
//
// The parameters:
//
//	by          agent | model | tool | error_type | step (required)
//	since       the window's start: RFC 3339, or a duration back from
//	            until (90m, 24h, 7d); default 24h
//	until       the window's end, RFC 3339; default now
//	agent       the agent's name, exact
//	tag.<k>=<v> metadata matches, /api/runs' meaning
//	public_id   /api/runs' meaning; a panel token's own is forced
//	parent      "*" (the default: every run, subagent children
//	            included — each counted under its own agent) | "" the
//	            top-level runs | a run id its children. Note the
//	            default differs from /api/runs', which lists the
//	            top-level runs: a group's runs_query says all=1
//	playground  false (the default: the app's own runs alone) | true
//	            (playground runs alone, /api/runs' meaning) | all
//	            (both). Not /api/runs' default (both): health numbers —
//	            rates, percentiles, call counts — must not mix scripted
//	            or playground replays into the app's own; a group's
//	            runs_query carries the effective filter, so its link
//	            lands on the same runs
//	experiment_id the runs of one experiment (default: any)
//	status      /api/runs' status words (default: any); an unknown
//	            value is 400, as since, until, by and playground are
//
// since and until must lie in 1678..2262 (Unix nanoseconds), else 400.
//
// Two keys to read with care: by=model keys on the model name alone
// (gen_ai.request.model), so two providers' models of one name are one
// group — its cost still priced per provider/model part; by=step keys a
// call on its own run's step index, and a replay numbers its steps from
// 0 at its from_step (not the source's ordinal the diff aligns on), so
// a replay from step 3 counts under step 0 — playground=false, the
// default, leaves replays out.
//
// A run is in the window when it started in [since, until); its spans
// count with it, in its bucket. The series has statsBuckets buckets of
// bucket_ms each (the window cut in 24 equal parts: an hour over a day,
// seven over a week). What each number counts per by is
// obsdb.StatsGroup's table; on the wire:
//
//	runs, failed        the group's runs, those that failed
//	failure_rate        failed / runs for agent and error_type,
//	                    failed_calls / calls for model, tool and step;
//	                    null with nothing to divide
//	calls, failed_calls model calls (tool calls for by=tool)
//	input_tokens,
//	output_tokens       the model calls' own usage (never a run's
//	                    rolled-up total: a subagent's tokens are its own)
//	cost_usd …          the pricing block (pricing.go), each model's
//	                    usage priced at its model at read time; unknown
//	                    — never $0 — when a model with usage has no
//	                    price; null for by=tool (no model usage)
//	duration_ms         {p50, p95, n}: run durations for agent and
//	                    error_type, call durations otherwise; null when n=0
//	ttft_ms             {p50, p95, n} over the model calls' weft.ttft_ms;
//	                    null when none
//	retry_rate          retried / calls (model calls that took more
//	                    than one attempt); null for by=tool or no calls
//	denials             refused tool calls (problems.denied's rule)
//	series[]            {runs, failed, calls, failed_calls, input_tokens,
//	                    output_tokens} per bucket, oldest first
//	delegated           by=agent alone: {runs, input_tokens,
//	                    output_tokens, cost block} — the model calls of the
//	                    runs below the agent's runs in their Subagent
//	                    trees whose agent is another (what its subagents
//	                    spent for it; never in its own figures above;
//	                    a call can be in several groups' delegated, so
//	                    never sum delegated across groups)
//	runs_query          agent and error_type groups with a key: the
//	                    /api/search query string (all=1, the window as
//	                    since/until, every filter read) whose total is
//	                    the group's runs — what a card links to
//
// Percentiles are nearest-rank and exact on both backends. Content-off
// safe: every number is a span attribute or a run column — no prompt,
// answer or tool argument is read. Groups past obsdb.MaxStatsGroups are
// cut, the body says how many (groups_cut).

// statsBuckets is the series' bucket count (obsdb.StatsBuckets).
const statsBuckets = obsdb.StatsBuckets

// statsDefaultWindow is since's default.
const statsDefaultWindow = 24 * time.Hour

type statsBody struct {
	By        string       `json:"by"`
	Since     time.Time    `json:"since"`
	Until     time.Time    `json:"until"`
	Buckets   int          `json:"buckets"`
	BucketMS  int64        `json:"bucket_ms"`
	Groups    []statsGroup `json:"groups"`
	GroupsCut int          `json:"groups_cut,omitempty"`
}

type statsGroup struct {
	Key          string   `json:"key"`
	Runs         int64    `json:"runs"`
	Failed       int64    `json:"failed"`
	FailureRate  *float64 `json:"failure_rate"`
	Calls        int64    `json:"calls"`
	FailedCalls  int64    `json:"failed_calls"`
	InputTokens  int64    `json:"input_tokens"`
	OutputTokens int64    `json:"output_tokens"`
	costFields
	DurationMS *statsPercentiles `json:"duration_ms"`
	TTFTMS     *statsPercentiles `json:"ttft_ms"`
	Retried    int64             `json:"retried"`
	RetryRate  *float64          `json:"retry_rate"`
	Denials    int64             `json:"denials"`
	Series     []statsBucket     `json:"series"`
	// Delegated is by=agent's: what the agent's subagents spent for it
	// (obsdb.StatsGroup.Delegated), beside its own figures above.
	Delegated *statsDelegated `json:"delegated,omitempty"`
	// RunsQuery is the /api/search query string whose total is this
	// group's runs (agent and error_type groups with a key; absent
	// otherwise — no runs filter reproduces a model's, tool's or step's).
	RunsQuery string `json:"runs_query,omitempty"`
}

// statsDelegated is an agent group's subagent bill: the descendant
// runs that made a model call, their calls' tokens and the cost block.
type statsDelegated struct {
	Runs         int64 `json:"runs"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	costFields
}

type statsPercentiles struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	N   int64   `json:"n"`
}

type statsBucket struct {
	Runs         int64 `json:"runs"`
	Failed       int64 `json:"failed"`
	Calls        int64 `json:"calls"`
	FailedCalls  int64 `json:"failed_calls"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// statsGroupRoute is the stats capability's group (registered when the
// database is an obsdb.Aggregator).
func statsGroupRoute() routeGroup {
	return routeGroup{
		name:       "stats",
		capability: "stats",
		register: func(mux *http.ServeMux, s *Server) {
			mux.HandleFunc("GET /api/stats", s.serveStats)
		},
	}
}

// windowSince reads since: RFC 3339, or a duration back from until —
// Go's (90m, 24h) or whole days (7d).
func windowSince(v string, until time.Time) (time.Time, error) {
	if v == "" {
		return until.Add(-statsDefaultWindow), nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if days, ok := strings.CutSuffix(v, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n > 0 && n <= 100_000 { // a Duration holds ~292 years
			return until.Add(-time.Duration(n) * 24 * time.Hour), nil
		}
	} else if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return until.Add(-d), nil
	}
	return time.Time{}, errors.New("since must be RFC 3339 or a positive duration (90m, 24h, 7d)")
}

func (s *Server) serveStats(w http.ResponseWriter, r *http.Request) {
	agg, ok := s.db.(obsdb.Aggregator)
	if !ok { // the group registers only when it is; kept for a swapped DB
		writeError(w, r, http.StatusNotImplemented, "not_implemented", offStats)
		return
	}
	q := r.URL.Query()
	query := obsdb.StatsQuery{
		RunQuery: obsdb.RunQuery{Agent: q.Get("agent"), PublicID: q.Get("public_id"), ParentRunID: "*", Meta: tagParams(q)},
		By:       obsdb.StatsBy(q.Get("by")),
		Buckets:  statsBuckets,
	}
	if _, set := q["parent"]; set {
		query.ParentRunID = parentParam(q)
	}
	// The scope first: a foreign public id's 403 wins over any 400.
	if !scopeRunsQuery(w, r, &query.RunQuery, q.Get("public_id")) {
		return
	}
	// /api/runs' other filters, its words and its defaults — experiment
	// runs included, any status — except playground: the app's own runs
	// unless asked (all: both).
	query.ExperimentID = q.Get("experiment_id")
	no := false
	query.Playground = &no
	switch v := q.Get("playground"); v {
	case "":
	case "all":
		query.Playground = nil
	default:
		b, err := strconv.ParseBool(v)
		if err != nil {
			badRequest(w, r, "playground must be true, false or all")
			return
		}
		query.Playground = &b
	}
	if query.Status, ok = statusParam(w, r, q); !ok {
		return
	}
	query.Until = time.Now().UTC()
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			badRequest(w, r, "until must be RFC 3339: "+err.Error())
			return
		}
		query.Until = t
	}
	since, err := windowSince(q.Get("since"), query.Until)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	query.Since = since
	page, err := agg.Stats(r.Context(), query)
	if errors.Is(err, obsdb.ErrInvalidStats) {
		badRequest(w, r, err.Error())
		return
	}
	if err != nil {
		dbError(w, r, "stats", "", err)
		return
	}
	out := statsBody{By: string(page.By), Since: page.Since, Until: page.Until, Buckets: page.Buckets,
		BucketMS: page.Bucket.Milliseconds(), Groups: make([]statsGroup, 0, len(page.Groups)), GroupsCut: page.GroupsCut}
	for _, g := range page.Groups {
		sg := s.statsGroupOf(page.By, g)
		sg.RunsQuery = runsQueryOf(q, query, page.By, g.Key)
		out.Groups = append(out.Groups, sg)
	}
	writeJSON(w, r, http.StatusOK, out)
}

// ratio is n / d, null when d is 0, rounded to 6 decimals.
func ratio(n, d int64) *float64 {
	if d == 0 {
		return nil
	}
	v := round6(float64(n) / float64(d))
	return &v
}

func percentilesOf(p obsdb.Percentiles) *statsPercentiles {
	if p.N == 0 {
		return nil
	}
	return &statsPercentiles{P50: round6(p.P50), P95: round6(p.P95), N: p.N}
}

func (s *Server) statsGroupOf(by obsdb.StatsBy, g obsdb.StatsGroup) statsGroup {
	out := statsGroup{Key: g.Key, Runs: g.Runs, Failed: g.Failed, Calls: g.Calls, FailedCalls: g.FailedCalls,
		InputTokens: g.Usage.InputTokens, OutputTokens: g.Usage.OutputTokens,
		costFields: s.statsCost(by, g).fields(),
		DurationMS: percentilesOf(g.Duration), TTFTMS: percentilesOf(g.TTFT),
		Retried: g.Retried, Denials: g.Denials, Series: make([]statsBucket, len(g.Series))}
	if by.RunLevel() {
		out.FailureRate = ratio(g.Failed, g.Runs)
	} else {
		out.FailureRate = ratio(g.FailedCalls, g.Calls)
	}
	if by != obsdb.StatsByTool {
		out.RetryRate = ratio(g.Retried, g.Calls)
	}
	if by == obsdb.StatsByAgent {
		d := &statsDelegated{Runs: g.DelegatedRuns, InputTokens: g.Delegated.InputTokens, OutputTokens: g.Delegated.OutputTokens}
		if g.DelegatedRuns == 0 {
			d.costFields = cost{known: true, cache: cacheExact}.fields() // nothing delegated: an exact $0
		} else {
			d.costFields = s.priceModels(g.DelegatedModels, g.Delegated).fields()
		}
		out.Delegated = d
	}
	for i, b := range g.Series {
		out.Series[i] = statsBucket{Runs: b.Runs, Failed: b.Failed, Calls: b.Calls, FailedCalls: b.FailedCalls,
			InputTokens: b.InputTokens, OutputTokens: b.OutputTokens}
	}
	return out
}

// statsCost prices a group's model usage, each provider/model part at
// its own row (PricingTable.price): known when every part with usage
// is priced, else unpriced naming the models — never $0 for a model
// without a price. A part with no usage costs nothing whatever its
// price. A tool group has no model usage: unknown, saying so.
func (s *Server) statsCost(by obsdb.StatsBy, g obsdb.StatsGroup) cost {
	if by == obsdb.StatsByTool {
		return cost{badge: costUnknown, reason: "tool calls carry no model usage to price", cache: cacheExact}
	}
	return s.priceModels(g.Models, g.Usage)
}

// priceModels prices usage split by model, each part at its own row:
// statsCost's rule, for a group's own calls and its delegated ones.
func (s *Server) priceModels(models []obsdb.ModelUsage, usage core.Usage) cost {
	out := cost{known: true, cache: cacheOf(usage)}
	var unpriced, parts, scriptedParts, scriptedIdle int
	for _, m := range models {
		if m.Usage == (core.Usage{}) {
			// A scripted model reports no usage by design: its part is an
			// exact $0, not "no usage recorded".
			if s.pricing.price(m.Provider, m.Model, m.Usage).badge == costScripted {
				scriptedIdle++
			}
			continue
		}
		parts++
		c := s.pricing.price(m.Provider, m.Model, m.Usage)
		if c.badge == costScripted {
			scriptedParts++
		}
		if !c.known {
			unpriced++
			name := m.Provider + "/" + m.Model
			if m.Provider == "" && m.Model == "" {
				name = "(no model)"
			}
			out.models = addModels(out.models, name)
			continue
		}
		out.usd += c.usd
	}
	switch {
	case parts == 0 && scriptedIdle > 0:
		// Every call was scripted (zero usage by design): an exact $0,
		// as each of its runs reads.
		return cost{known: true, badge: costScripted, reason: scriptedReason, cache: cacheExact}
	case parts == 0:
		// No call reported usage (none ran, or every one failed before
		// its usage arrived): nothing to price, not a $0.
		return cost{badge: costUnknown, reason: "no model usage recorded", cache: cacheExact}
	case unpriced > 0:
		out.known, out.usd, out.badge = false, 0, costUnpriced
		out.reason = fmt.Sprintf("%d of %d models unpriced: no price for %s", unpriced, parts, strings.Join(capModels(out.models), ", "))
	case parts > 0 && scriptedParts == parts:
		// Every model is scripted: the exact $0 says so (pricing.go).
		out.badge, out.reason, out.cache = costScripted, scriptedReason, cacheExact
	}
	return out
}

// runsQueryOf is a run-level group's /api/search query string — its
// key and every filter the stats query read, the window as since/until
// on the start — whose total is the group's runs; "" for a group no
// runs filter reproduces (model, tool, step; an unnamed agent, a
// failure with no error type).
func runsQueryOf(params url.Values, q obsdb.StatsQuery, by obsdb.StatsBy, key string) string {
	v := url.Values{}
	switch {
	case key == "":
		return ""
	case by == obsdb.StatsByAgent:
		v.Set("agent", key)
	case by == obsdb.StatsByErrorType:
		if q.Agent != "" {
			v.Set("agent", q.Agent)
		}
		v.Set("error_type", key)
		v.Set("status", string(obsdb.StatusFailed))
	default:
		return ""
	}
	switch q.ParentRunID {
	case "*":
		v.Set("all", "1")
	case "":
	default:
		v.Set("parent", q.ParentRunID)
	}
	for k, val := range q.Meta {
		v.Set("tag."+k, val)
	}
	for _, k := range []string{"public_id", "experiment_id"} {
		if p := params.Get(k); p != "" {
			v.Set(k, p)
		}
	}
	// The effective playground filter (the default false included), in
	// /api/search's spelling; both is its default, no parameter.
	if q.Playground != nil {
		v.Set("playground", strconv.FormatBool(*q.Playground))
	}
	if q.Status != "" && by != obsdb.StatsByErrorType {
		v.Set("status", string(q.Status))
	}
	v.Set("since", q.Since.UTC().Format(time.RFC3339Nano))
	v.Set("until", q.Until.UTC().Format(time.RFC3339Nano))
	return v.Encode()
}
