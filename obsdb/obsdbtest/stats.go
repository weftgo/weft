package obsdbtest

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The aggregates table (obsdb.Aggregator; UX plan D7, A4, A2): both
// backends answer the same numbers over one fixture — SQLite from its
// span_stats rollup written at ingest, ClickHouse with native GROUP BY
// over otel_traces — percentiles included: both are nearest-rank and
// exact, so the tolerance is zero.

// statRun is one fixture run: identity, its start (from the fixture
// clock) and duration, its failure, and its spans.
type statRun struct {
	id, agent, public string
	meta              map[string]string
	start, dur        time.Duration
	err, errType      string
	denied            int64 // the root's weft.run.denied
	spans             func(r statRun) []obsdb.Span
	late              func(r statRun) []obsdb.Span // written in a second batch
}

func (r statRun) trace() string            { return hex16(r.id+"/t1") + hex16(r.id+"/t2") }
func (r statRun) spanID(key string) string { return hex16(r.id + "/" + key) }

func (r statRun) attrs(op string) map[string]any {
	a := map[string]any{"weft.run.id": r.id}
	if op != "" {
		a["gen_ai.operation.name"] = op
	}
	if r.public != "" {
		a["weft.public_id"] = r.public
	}
	for k, v := range r.meta {
		a[k] = v
	}
	return a
}

func (r statRun) root() obsdb.Span {
	a := r.attrs("invoke_agent")
	a["gen_ai.agent.name"] = r.agent
	a["gen_ai.provider.name"] = "p"
	a["gen_ai.request.model"] = "m-small"
	a["weft.run.steps"] = int64(1)
	status := 1
	if r.err != "" {
		status = 2
		a["error.type"] = r.errType
	}
	if r.denied > 0 {
		a["weft.run.denied"] = r.denied
	}
	return obsdb.Span{TraceID: r.trace(), SpanID: r.spanID("root"), Name: "invoke_agent " + r.agent, Kind: 1,
		Start: at(r.start), End: at(r.start + r.dur), StatusCode: status, StatusMessage: r.err,
		Service: "stats-svc", Attrs: a, Resource: map[string]any{"service.name": "stats-svc"}}
}

// chat is a model call at step on model, off after the run's start;
// ttft < 0 carries none, status 2 fails it.
func (r statRun) chat(key string, step int, model string, off, dur time.Duration, in, out, ttft int64, status int) obsdb.Span {
	a := r.attrs("chat")
	a["weft.step.index"] = int64(step)
	a["gen_ai.provider.name"] = "p"
	a["gen_ai.request.model"] = model
	a["gen_ai.usage.input_tokens"] = in
	a["gen_ai.usage.output_tokens"] = out
	if ttft >= 0 {
		a["weft.ttft_ms"] = ttft
	}
	if status == 2 {
		a["error.type"] = "model_error"
	}
	return obsdb.Span{TraceID: r.trace(), SpanID: r.spanID(key), ParentSpanID: r.spanID("root"), Name: "chat " + model, Kind: 3,
		Start: at(r.start + off), End: at(r.start + off + dur), StatusCode: status, Service: "stats-svc", Attrs: a}
}

// attempt is an attempt span under the chat span key.
func (r statRun) attempt(chat string, idx int64, status int) obsdb.Span {
	a := r.attrs("")
	a["weft.attempt.index"] = idx
	a["gen_ai.provider.name"] = "p"
	key := fmt.Sprintf("%s/attempt/%d", chat, idx)
	return obsdb.Span{TraceID: r.trace(), SpanID: r.spanID(key), ParentSpanID: r.spanID(chat), Name: "attempt", Kind: 3,
		Start: at(r.start + 100*time.Millisecond), End: at(r.start + 200*time.Millisecond), StatusCode: status,
		Service: "stats-svc", Attrs: a}
}

// tool is a tool call; errType "" succeeds.
func (r statRun) tool(key, name string, off, dur time.Duration, errType string, extra map[string]any) obsdb.Span {
	a := r.attrs("execute_tool")
	a["gen_ai.tool.name"] = name
	a["gen_ai.tool.call.id"] = "call_" + key
	a["weft.step.index"] = int64(0)
	status := 1
	if errType != "" {
		status = 2
		a["error.type"] = errType
	}
	for k, v := range extra {
		a[k] = v
	}
	return obsdb.Span{TraceID: r.trace(), SpanID: r.spanID(key), ParentSpanID: r.spanID("root"), Name: "execute_tool " + name, Kind: 1,
		Start: at(r.start + off), End: at(r.start + off + dur), StatusCode: status, Service: "stats-svc", Attrs: a}
}

const (
	statsUsageA2 = "weft: run failed at step 0: weft: run exceeded its usage limit (input 30/25, output 15/0)"
	statsUsageB1 = "weft: run failed at step 0: weft: run exceeded its usage limit (input 40/25, output 9/0)"
	statsRetries = "weft: run failed at step 0: model stream: mw: giving up after 3 retries: server error"
)

// statsFixture is six runs; the window [-1h, +1h) in two buckets holds
// five of them:
//
//   - st-a1 (alpha, team=x, -30m, 4 s, ok): two m-small calls (step 0:
//     100/20 tokens, TTFT 300, 1 s, two attempts written in a later
//     batch — the first failed; step 1: 50/10, TTFT 100, 500 ms), tool
//     lookup twice (200 ms ok, 100 ms tool_error), tool pay DENIED by a
//     policy (10 ms).
//   - st-a2 (alpha, team=x, +10m, 3 s, failed usage_limit): one m-big
//     call (30/15, TTFT 500, 2 s) and a summarizer call (weft.chat.role:
//     never counted).
//   - st-b1 (beta, +20m, 2 s, failed usage_limit, two calls a resume
//     denied: weft.run.denied 2): one m-small call (40/9, no TTFT,
//     800 ms) and the zero-length weft.tool.denied span (never counted).
//   - st-o1 (beta, public pub_x, +5m, 1 s, ok): one m-small call (7/3,
//     TTFT 50, 300 ms).
//   - st-c1 (gamma, +30m, 1600 ms, failed model_error): one m-big call
//     that failed after three attempts (stored before it), 1500 ms.
//   - st-old (alpha, -2h): outside the window.
func statsFixture() []statRun {
	team := map[string]string{"team": "x"}
	return []statRun{
		{id: "st-a1", agent: "alpha", meta: team, start: -30 * time.Minute, dur: 4 * time.Second,
			spans: func(r statRun) []obsdb.Span {
				return []obsdb.Span{
					r.chat("c0", 0, "m-small", 100*time.Millisecond, time.Second, 100, 20, 300, 1),
					r.tool("t0", "lookup", 1200*time.Millisecond, 200*time.Millisecond, "", nil),
					r.tool("t1", "lookup", 1400*time.Millisecond, 100*time.Millisecond, "tool_error", nil),
					r.tool("t2", "pay", 1450*time.Millisecond, 10*time.Millisecond, "DENIED", nil),
					r.chat("c1", 1, "m-small", 1500*time.Millisecond, 500*time.Millisecond, 50, 10, 100, 1),
					r.root(),
				}
			},
			late: func(r statRun) []obsdb.Span {
				return []obsdb.Span{r.attempt("c0", 1, 2), r.attempt("c0", 2, 1)}
			}},
		{id: "st-a2", agent: "alpha", meta: team, start: 10 * time.Minute, dur: 3 * time.Second,
			err: statsUsageA2, errType: "usage_limit",
			spans: func(r statRun) []obsdb.Span {
				sum := r.chat("sum", 0, "m-sum", 2500*time.Millisecond, 100*time.Millisecond, 1000, 100, 20, 1)
				sum.Attrs["weft.chat.role"] = "summarizer"
				delete(sum.Attrs, "weft.step.index")
				return []obsdb.Span{r.chat("c0", 0, "m-big", 100*time.Millisecond, 2*time.Second, 30, 15, 500, 1), sum, r.root()}
			}},
		{id: "st-b1", agent: "beta", start: 20 * time.Minute, dur: 2 * time.Second, err: statsUsageB1, errType: "usage_limit", denied: 2,
			spans: func(r statRun) []obsdb.Span {
				return []obsdb.Span{
					r.chat("c0", 0, "m-small", 100*time.Millisecond, 800*time.Millisecond, 40, 9, -1, 1),
					r.tool("d0", "pay", 0, 0, "DENIED", map[string]any{"weft.tool.denied": true, "weft.tool.denied_kind": "denied"}),
					r.root(),
				}
			}},
		{id: "st-o1", agent: "beta", public: "pub_x", start: 5 * time.Minute, dur: time.Second,
			spans: func(r statRun) []obsdb.Span {
				return []obsdb.Span{r.chat("c0", 0, "m-small", 100*time.Millisecond, 300*time.Millisecond, 7, 3, 50, 1), r.root()}
			}},
		{id: "st-c1", agent: "gamma", start: 30 * time.Minute, dur: 1600 * time.Millisecond, err: statsRetries, errType: "model_error",
			spans: func(r statRun) []obsdb.Span {
				return []obsdb.Span{
					r.attempt("c0", 1, 2), r.attempt("c0", 2, 2), r.attempt("c0", 3, 2),
					r.chat("c0", 0, "m-big", 50*time.Millisecond, 1500*time.Millisecond, 0, 0, -1, 2),
					r.root(),
				}
			}},
		{id: "st-old", agent: "alpha", start: -2 * time.Hour, dur: time.Second,
			spans: func(r statRun) []obsdb.Span {
				return []obsdb.Span{r.chat("c0", 0, "m-small", 0, 100*time.Millisecond, 999, 999, 10, 1), r.root()}
			}},
	}
}

func writeStatsFixture(t *testing.T, db obsdb.DB) {
	t.Helper()
	runs := statsFixture()
	for _, r := range runs {
		if err := db.Write(ctx(), obsdb.Batch{Spans: r.spans(r)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range runs {
		if r.late != nil {
			if err := db.Write(ctx(), obsdb.Batch{Spans: r.late(r)}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// statsQ is the fixture's window over every run, children included.
func statsQ(by obsdb.StatsBy) obsdb.StatsQuery {
	return obsdb.StatsQuery{RunQuery: obsdb.RunQuery{ParentRunID: "*"}, By: by,
		Since: at(-time.Hour), Until: at(time.Hour), Buckets: 2}
}

func aggregator(t *testing.T, db obsdb.DB) obsdb.Aggregator {
	t.Helper()
	a, ok := db.(obsdb.Aggregator)
	if !ok {
		t.Fatalf("%T does not implement obsdb.Aggregator: every first-party backend does", db)
	}
	return a
}

// statsLines renders a page one line per group: the totals, the
// percentiles, the models and the series — the table compares strings.
func statsLines(p obsdb.StatsPage) []string {
	out := []string{}
	for _, g := range p.Groups {
		var models, series []string
		for _, m := range g.Models {
			models = append(models, fmt.Sprintf("%s/%s %d/%d", m.Provider, m.Model, m.Usage.InputTokens, m.Usage.OutputTokens))
		}
		for _, b := range g.Series {
			series = append(series, fmt.Sprintf("%d/%d %d/%d %d/%d", b.Runs, b.Failed, b.Calls, b.FailedCalls, b.InputTokens, b.OutputTokens))
		}
		out = append(out, fmt.Sprintf("%s runs=%d/%d calls=%d/%d retried=%d denied=%d tok=%d/%d dur=%g/%g/%d ttft=%g/%g/%d models=[%s] series=[%s]",
			g.Key, g.Runs, g.Failed, g.Calls, g.FailedCalls, g.Retried, g.Denials, g.Usage.InputTokens, g.Usage.OutputTokens,
			g.Duration.P50, g.Duration.P95, g.Duration.N, g.TTFT.P50, g.TTFT.P95, g.TTFT.N,
			strings.Join(models, ", "), strings.Join(series, " | ")))
	}
	return out
}

func wantStats(t *testing.T, a obsdb.Aggregator, q obsdb.StatsQuery, want ...string) {
	t.Helper()
	page, err := a.Stats(ctx(), q)
	if err != nil {
		t.Fatalf("Stats(%s): %v", q.By, err)
	}
	if page.Buckets != q.Buckets || page.Bucket != time.Hour || !page.Since.Equal(q.Since) || !page.Until.Equal(q.Until) {
		t.Errorf("Stats(%s) window = %d × %v [%v, %v), want the query's", q.By, page.Buckets, page.Bucket, page.Since, page.Until)
	}
	if want == nil {
		want = []string{}
	}
	if got := statsLines(page); !slices.Equal(got, want) {
		t.Errorf("Stats(%s, %+v):\n got  %s\n want %s", q.By, q.RunQuery, strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}
}

func statsTable(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		writeStatsFixture(t, db)
		a := aggregator(t, db)

		alpha := "alpha runs=2/1 calls=3/0 retried=1 denied=1 tok=180/45 dur=3000/4000/2 ttft=300/500/3 models=[p/m-big 30/15, p/m-small 150/30] series=[1/0 2/0 150/30 | 1/1 1/0 30/15]"
		wantStats(t, a, statsQ(obsdb.StatsByAgent),
			alpha,
			"beta runs=2/1 calls=2/0 retried=0 denied=2 tok=47/12 dur=1000/2000/2 ttft=50/50/1 models=[p/m-small 47/12] series=[0/0 0/0 0/0 | 2/1 2/0 47/12]",
			"gamma runs=1/1 calls=1/1 retried=1 denied=0 tok=0/0 dur=1600/1600/1 ttft=0/0/0 models=[p/m-big 0/0] series=[0/0 0/0 0/0 | 1/1 1/1 0/0]")
		wantStats(t, a, statsQ(obsdb.StatsByErrorType),
			"usage_limit runs=2/2 calls=2/0 retried=0 denied=2 tok=70/24 dur=2000/3000/2 ttft=500/500/1 models=[p/m-big 30/15, p/m-small 40/9] series=[0/0 0/0 0/0 | 2/2 2/0 70/24]",
			"model_error runs=1/1 calls=1/1 retried=1 denied=0 tok=0/0 dur=1600/1600/1 ttft=0/0/0 models=[p/m-big 0/0] series=[0/0 0/0 0/0 | 1/1 1/1 0/0]")
		wantStats(t, a, statsQ(obsdb.StatsByModel),
			"m-small runs=3/1 calls=4/0 retried=1 denied=0 tok=197/42 dur=500/1000/4 ttft=100/300/3 models=[p/m-small 197/42] series=[1/0 2/0 150/30 | 2/1 2/0 47/12]",
			"m-big runs=2/2 calls=2/1 retried=1 denied=0 tok=30/15 dur=1500/2000/2 ttft=500/500/1 models=[p/m-big 30/15] series=[0/0 0/0 0/0 | 2/2 2/1 30/15]")
		wantStats(t, a, statsQ(obsdb.StatsByTool),
			"lookup runs=1/0 calls=2/1 retried=0 denied=0 tok=0/0 dur=100/200/2 ttft=0/0/0 models=[] series=[1/0 2/1 0/0 | 0/0 0/0 0/0]",
			"pay runs=1/0 calls=1/0 retried=0 denied=1 tok=0/0 dur=10/10/1 ttft=0/0/0 models=[] series=[1/0 1/0 0/0 | 0/0 0/0 0/0]")
		wantStats(t, a, statsQ(obsdb.StatsByStep),
			"0 runs=5/3 calls=5/1 retried=2 denied=0 tok=177/47 dur=1000/2000/5 ttft=300/500/3 models=[p/m-big 30/15, p/m-small 147/32] series=[1/0 1/0 100/20 | 4/3 4/1 77/27]",
			"1 runs=1/0 calls=1/0 retried=0 denied=0 tok=50/10 dur=500/500/1 ttft=100/100/1 models=[p/m-small 50/10] series=[1/0 1/0 50/10 | 0/0 0/0 0/0]")

		// The /api/runs filters, with their meaning there.
		q := statsQ(obsdb.StatsByAgent)
		q.Agent = "alpha"
		wantStats(t, a, q, alpha)
		q = statsQ(obsdb.StatsByModel)
		q.Meta = map[string]string{"team": "x"}
		wantStats(t, a, q,
			"m-small runs=1/0 calls=2/0 retried=1 denied=0 tok=150/30 dur=500/1000/2 ttft=100/300/2 models=[p/m-small 150/30] series=[1/0 2/0 150/30 | 0/0 0/0 0/0]",
			"m-big runs=1/1 calls=1/0 retried=0 denied=0 tok=30/15 dur=2000/2000/1 ttft=500/500/1 models=[p/m-big 30/15] series=[0/0 0/0 0/0 | 1/1 1/0 30/15]")
		// A panel token's scope: its public id's runs alone.
		q = statsQ(obsdb.StatsByAgent)
		q.PublicID = "pub_x"
		wantStats(t, a, q,
			"beta runs=1/0 calls=1/0 retried=0 denied=0 tok=7/3 dur=1000/1000/1 ttft=50/50/1 models=[p/m-small 7/3] series=[0/0 0/0 0/0 | 1/0 1/0 7/3]")
		q.PublicID = "pub_nobody"
		wantStats(t, a, q)
		// The window is the run's start: st-old alone, its bucket 0.
		q = statsQ(obsdb.StatsByAgent)
		q.Since, q.Until = at(-3*time.Hour), at(-90*time.Minute)
		page, err := a.Stats(ctx(), q)
		if err != nil {
			t.Fatal(err)
		}
		if got := statsLines(page); len(got) != 1 || !strings.HasPrefix(got[0], "alpha runs=1/0 calls=1/0 retried=0 denied=0 tok=999/999") {
			t.Errorf("Stats over st-old's window = %v, want st-old alone", got)
		}
		// Bounds.
		for _, bad := range []obsdb.StatsQuery{
			{By: "nope", Since: at(-time.Hour), Until: at(0)},
			{By: obsdb.StatsByAgent},
			{By: obsdb.StatsByAgent, Since: at(0), Until: at(-time.Hour)},
			{By: obsdb.StatsByAgent, Since: at(-time.Hour), Until: at(0), Buckets: obsdb.MaxStatsBuckets + 1},
		} {
			if _, err := a.Stats(ctx(), bad); err == nil || !strings.Contains(err.Error(), obsdb.ErrInvalidStats.Error()) {
				t.Errorf("Stats(%+v) = %v, want ErrInvalidStats", bad, err)
			}
		}
	}
}

// statsDeleted: the aggregates follow deletion and retention — a
// deleted run's spans leave every group, a swept window leaves none.
func statsDeleted(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		writeStatsFixture(t, db)
		a := aggregator(t, db)
		if err := db.DeleteRun(ctx(), "st-a1"); err != nil {
			t.Fatal(err)
		}
		wantStats(t, a, statsQ(obsdb.StatsByTool))
		q := statsQ(obsdb.StatsByAgent)
		q.Agent = "alpha"
		wantStats(t, a, q,
			"alpha runs=1/1 calls=1/0 retried=0 denied=0 tok=30/15 dur=3000/3000/1 ttft=500/500/1 models=[p/m-big 30/15] series=[0/0 0/0 0/0 | 1/1 1/0 30/15]")
		if _, err := db.Sweep(ctx(), at(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		for _, by := range []obsdb.StatsBy{obsdb.StatsByAgent, obsdb.StatsByModel, obsdb.StatsByTool, obsdb.StatsByStep, obsdb.StatsByErrorType} {
			wantStats(t, a, statsQ(by))
		}
	}
}

// errorCounts: the error-class facet's reads — the distinct texts of a
// search match, folded into classes in Go, and a class's texts back as
// RunQuery.Errs (UX-C-37): the two usage-limit failures are one class.
func errorCounts(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		writeStatsFixture(t, db)
		a := aggregator(t, db)
		all := obsdb.SearchQuery{RunQuery: obsdb.RunQuery{ParentRunID: "*"}}
		counts, capped, err := a.ErrorCounts(ctx(), all)
		if err != nil {
			t.Fatal(err)
		}
		want := []obsdb.ErrorCount{{Err: statsRetries, Count: 1}, {Err: statsUsageA2, Count: 1}, {Err: statsUsageB1, Count: 1}}
		if !slices.Equal(counts, want) || capped {
			t.Errorf("ErrorCounts = %v (capped %v), want %v", counts, capped, want)
		}
		usage := "weft: run failed at step N: weft: run exceeded its usage limit (input N/N, output N/N)"
		classes := obsdb.ErrorClasses(counts, 0)
		wantClasses := []obsdb.ErrorClassCount{{Class: usage, Count: 2},
			{Class: "weft: run failed at step N: model stream: mw: giving up after N retries: server error", Count: 1}}
		if !slices.Equal(classes, wantClasses) {
			t.Errorf("ErrorClasses = %v, want %v", classes, wantClasses)
		}
		// The match narrows the counts: beta's failures alone.
		beta := all
		beta.Agent = "beta"
		if counts, _, err := a.ErrorCounts(ctx(), beta); err != nil || !slices.Equal(counts, []obsdb.ErrorCount{{Err: statsUsageB1, Count: 1}}) {
			t.Errorf("ErrorCounts(agent beta) = %v, %v", counts, err)
		}
		// A class back to its texts, as a filter on Runs and Search.
		rq := obsdb.RunQuery{ParentRunID: "*", Errs: obsdb.ErrorClassTexts(counts, usage)}
		page, err := db.Runs(ctx(), rq)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range page.Runs {
			ids = append(ids, r.ID)
		}
		if strings.Join(ids, ",") != "st-b1,st-a2" || page.Total != 2 {
			t.Errorf("Runs(Errs: the usage class) = %v (total %d), want st-b1,st-a2", ids, page.Total)
		}
		if srch, ok := db.(obsdb.Searcher); ok {
			sp, err := srch.Search(ctx(), obsdb.SearchQuery{RunQuery: rq})
			if err != nil || sp.Total != 2 {
				t.Errorf("Search(Errs: the usage class) = %d, %v; want 2", sp.Total, err)
			}
		}
		rq.Errs = []string{}
		if page, err := db.Runs(ctx(), rq); err != nil || page.Total != 0 {
			t.Errorf("Runs(Errs: empty) = %d, %v; want none", page.Total, err)
		}
	}
}

// statsAttemptSpelling: weft.attempt.index read one way on every
// backend (obsdb.AttemptIndex) — an integer or a string of decimal
// digits, before or after its chat span; a fraction or text is no index.
func statsAttemptSpelling(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		r := statRun{id: "st-spell", agent: "speller", start: -10 * time.Minute, dur: 5 * time.Second}
		att := func(chat string, n int, idx any) obsdb.Span {
			s := r.attempt(chat, int64(n), 1)
			s.Attrs["weft.attempt.index"] = idx
			return s
		}
		early := []obsdb.Span{
			att("c0", 2, "2"), // a collector's string, before its chat span
			att("c2", 9, 2.5), // a fraction: no index
			att("c3", 3, int64(3)),
			r.chat("c0", 0, "m", 0, time.Second, 1, 1, -1, 1),
			r.chat("c1", 1, "m", time.Second, time.Second, 1, 1, -1, 1),
			r.chat("c2", 2, "m", 2*time.Second, time.Second, 1, 1, -1, 1),
			r.chat("c3", 3, "m", 3*time.Second, time.Second, 1, 1, -1, 1),
			r.root(),
		}
		late := []obsdb.Span{
			att("c1", 3, "3"),   // a string after its chat span
			att("c2", 8, "x8"),  // text: no index
			att("c3", 4, "04x"), // text: no index
		}
		for _, b := range [][]obsdb.Span{early, late} {
			if err := db.Write(ctx(), obsdb.Batch{Spans: b}); err != nil {
				t.Fatal(err)
			}
		}
		wantStats(t, aggregator(t, db), statsQ(obsdb.StatsByStep),
			"0 runs=1/0 calls=1/0 retried=1 denied=0 tok=1/1 dur=1000/1000/1 ttft=0/0/0 models=[p/m 1/1] series=[1/0 1/0 1/1 | 0/0 0/0 0/0]",
			"1 runs=1/0 calls=1/0 retried=1 denied=0 tok=1/1 dur=1000/1000/1 ttft=0/0/0 models=[p/m 1/1] series=[1/0 1/0 1/1 | 0/0 0/0 0/0]",
			"2 runs=1/0 calls=1/0 retried=0 denied=0 tok=1/1 dur=1000/1000/1 ttft=0/0/0 models=[p/m 1/1] series=[1/0 1/0 1/1 | 0/0 0/0 0/0]",
			"3 runs=1/0 calls=1/0 retried=1 denied=0 tok=1/1 dur=1000/1000/1 ttft=0/0/0 models=[p/m 1/1] series=[1/0 1/0 1/1 | 0/0 0/0 0/0]")
	}
}

// statsSpanBeforeRun: a model call stored with a start before its
// run's other spans — and before the window — moves the run's start
// with it on both backends: the window reads the run as it stands.
func statsSpanBeforeRun(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		in := statRun{id: "st-in", agent: "edge", start: -50 * time.Minute, dur: time.Second}
		early := statRun{id: "st-early", agent: "edge", start: -50 * time.Minute, dur: time.Second}
		writes := [][]obsdb.Span{
			{in.chat("c0", 0, "m", 0, time.Second, 5, 5, -1, 1), in.root()},
			{early.chat("c0", 0, "m", 0, time.Second, 7, 7, -1, 1), early.root()},
			// Later: a call of st-early that started 20 minutes before
			// the window opens.
			{early.chat("c1", 1, "m", -30*time.Minute, time.Second, 100, 100, -1, 1)},
		}
		for _, b := range writes {
			if err := db.Write(ctx(), obsdb.Batch{Spans: b}); err != nil {
				t.Fatal(err)
			}
		}
		a := aggregator(t, db)
		for _, by := range []obsdb.StatsBy{obsdb.StatsByAgent, obsdb.StatsByModel} {
			page, err := a.Stats(ctx(), statsQ(by))
			if err != nil {
				t.Fatal(err)
			}
			if got := statsLines(page); len(got) != 1 || !strings.Contains(got[0], "runs=1/0 calls=1/0") || !strings.Contains(got[0], "tok=5/5") {
				t.Errorf("Stats(%s) = %v, want st-in alone: st-early started before the window", by, got)
			}
		}
	}
}

// statsDelegated: an agent's subagents' calls (by=agent's Delegated) —
// every Subagent descendant of its runs whose agent is another, each
// call once — beside its own usage; a descendant of the same agent is
// its own run, and a panel token's scope leaves out a descendant of
// another public id, or one without its tags.
func statsDelegated(open func(t *testing.T) obsdb.DB) func(*testing.T) {
	return func(t *testing.T) {
		db := open(t)
		// Each run's tag stats.batch on its spans (run metadata on every
		// span), so its row's meta; "" none. dl-p/2/call_4 carries the
		// key with another value (b2); dl-p/3/call_5 is untagged with a
		// tagged child (I4-fix2: tagged → untagged → tagged).
		tags := map[string]string{"dl-p": "b1", "dl-p/0/call_1": "b1", "dl-p/0/call_1/0/call_2": "b1",
			"dl-p/2/call_4": "b2", "dl-p/3/call_5/0/call_6": "b1", "dl-q": "b1"}
		run := func(id, agent, public string, at time.Duration, in, out int64) statRun {
			r := statRun{id: id, agent: agent, public: public, start: at, dur: time.Second}
			r.spans = func(r statRun) []obsdb.Span {
				root := r.root()
				if i := strings.LastIndex(r.id, "/"); i > 0 {
					p := r.id[:strings.LastIndex(r.id[:i], "/")]
					root.Attrs["weft.parent.run.id"], root.Attrs["weft.parent.call.id"] = p, r.id[i+1:]
				}
				spans := []obsdb.Span{r.chat("c0", 0, "m-"+agent, 10*time.Millisecond, 100*time.Millisecond, in, out, -1, 1), root}
				if tag := tags[r.id]; tag != "" {
					for _, sp := range spans {
						sp.Attrs["stats.batch"] = tag
					}
				}
				return spans
			}
			return r
		}
		for _, r := range []statRun{
			run("dl-p", "boss", "pub_d", -40*time.Minute, 100, 10),
			run("dl-p/0/call_1", "helper", "pub_d", -40*time.Minute+time.Second, 20, 2),
			run("dl-p/0/call_1/0/call_2", "boss", "pub_d", -40*time.Minute+2*time.Second, 3, 1),
			run("dl-p/1/call_3", "helper", "pub_other", -40*time.Minute+3*time.Second, 40, 4),
			run("dl-p/2/call_4", "helper", "pub_d", -40*time.Minute+4*time.Second, 4, 1),
			run("dl-p/3/call_5", "helper", "pub_d", -40*time.Minute+5*time.Second, 5, 1),
			run("dl-p/3/call_5/0/call_6", "scout", "pub_d", -40*time.Minute+6*time.Second, 6, 1),
			run("dl-q", "boss", "pub_d", -20*time.Minute, 50, 5),
		} {
			if err := db.Write(ctx(), obsdb.Batch{Spans: r.spans(r)}); err != nil {
				t.Fatal(err)
			}
		}
		a := aggregator(t, db)
		delegated := func(q obsdb.StatsQuery) []string {
			page, err := a.Stats(ctx(), q)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, g := range page.Groups {
				var ms []string
				for _, m := range g.DelegatedModels {
					ms = append(ms, fmt.Sprintf("%s %d/%d", m.Model, m.Usage.InputTokens, m.Usage.OutputTokens))
				}
				out = append(out, fmt.Sprintf("%s own=%d/%d delegated=%d/%d runs=%d [%s]", g.Key, g.Usage.InputTokens, g.Usage.OutputTokens,
					g.Delegated.InputTokens, g.Delegated.OutputTokens, g.DelegatedRuns, strings.Join(ms, ", ")))
			}
			slices.Sort(out) // by key: the page's own order is by runs
			return out
		}
		// boss: its own three runs' calls (100+3+50); below dl-p every
		// other agent's descendant — the helpers (20, 40, 4, 5) and the
		// scout (6); the boss grandchild is boss's own. helper: below
		// dl-p/0/call_1 the boss grandchild (3), below dl-p/3/call_5
		// the scout (6). scout: none.
		want := []string{"boss own=153/16 delegated=75/9 runs=5 [m-helper 69/8, m-scout 6/1]",
			"helper own=69/8 delegated=9/2 runs=2 [m-boss 3/1, m-scout 6/1]", "scout own=6/1 delegated=0/0 runs=0 []"}
		if got := delegated(statsQ(obsdb.StatsByAgent)); !slices.Equal(got, want) {
			t.Errorf("delegated =\n %v\nwant\n %v", got, want)
		}
		// pub_d: dl-p/1/call_3 (pub_other) is left out.
		q := statsQ(obsdb.StatsByAgent)
		q.PublicID = "pub_d"
		want = []string{"boss own=153/16 delegated=35/5 runs=4 [m-helper 29/4, m-scout 6/1]",
			"helper own=29/4 delegated=9/2 runs=2 [m-boss 3/1, m-scout 6/1]", "scout own=6/1 delegated=0/0 runs=0 []"}
		if got := delegated(q); !slices.Equal(got, want) {
			t.Errorf("delegated under pub_d =\n %v\nwant\n %v", got, want)
		}
		// A tag scope (RunQuery.Meta, a tag-scoped panel token: I4-fix)
		// holds the descendants to it as the public id does: below dl-p
		// the untagged helpers (call_3, call_5) and the b2 one (call_4)
		// are not delegated, and neither is the tagged scout under the
		// untagged call_5 (I4-fix2: the tree's rule — a run under a
		// hidden one is hidden with its subtree, as the run document
		// and the span tree show it), with or without a public id. The
		// scout's own run is still its own group's: it carries the tag.
		want = []string{"boss own=153/16 delegated=20/2 runs=1 [m-helper 20/2]",
			"helper own=20/2 delegated=3/1 runs=1 [m-boss 3/1]", "scout own=6/1 delegated=0/0 runs=0 []"}
		for _, pub := range []string{"", "pub_d"} {
			q := statsQ(obsdb.StatsByAgent)
			q.PublicID, q.Meta = pub, map[string]string{"stats.batch": "b1"}
			if got := delegated(q); !slices.Equal(got, want) {
				t.Errorf("delegated under tag stats.batch=b1 (public id %q) =\n %v\nwant\n %v", pub, got, want)
			}
		}
		// b2: dl-p carries b1, so no group has a run of b2 but call_4,
		// whose ancestors are out of the scope: its own group alone.
		q = statsQ(obsdb.StatsByAgent)
		q.Meta = map[string]string{"stats.batch": "b2"}
		if got, want := delegated(q), []string{"helper own=4/1 delegated=0/0 runs=0 []"}; !slices.Equal(got, want) {
			t.Errorf("delegated under tag stats.batch=b2 =\n %v\nwant\n %v", got, want)
		}
		// U23.2-fix2: an out-of-scope intermediate that started before
		// the window still hides its subtree, on both backends (the
		// tree has no window): zw-p (pub_d) in the window, its helper
		// (pub_other) 90 minutes ago, the scout under it (pub_d) in the
		// window — under pub_d, zw-p's agent delegates nothing.
		for _, r := range []statRun{
			run("zw-p", "zprobe", "pub_d", -30*time.Minute, 7, 1),
			run("zw-p/0/call_1", "zhelper", "pub_other", -90*time.Minute, 8, 1),
			run("zw-p/0/call_1/0/call_1", "zscout", "pub_d", -29*time.Minute, 9, 1),
		} {
			if err := db.Write(ctx(), obsdb.Batch{Spans: r.spans(r)}); err != nil {
				t.Fatal(err)
			}
		}
		q = statsQ(obsdb.StatsByAgent)
		q.PublicID = "pub_d"
		var probe []string
		for _, l := range delegated(q) {
			if strings.HasPrefix(l, "zprobe ") {
				probe = append(probe, l)
			}
		}
		if want := []string{"zprobe own=7/1 delegated=0/0 runs=0 []"}; !slices.Equal(probe, want) {
			t.Errorf("delegated under pub_d past an early out-of-scope helper =\n %v\nwant\n %v", probe, want)
		}
		// Every other by reads none.
		page, err := a.Stats(ctx(), statsQ(obsdb.StatsByModel))
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range page.Groups {
			if g.DelegatedRuns != 0 || g.Delegated.InputTokens != 0 {
				t.Errorf("by=model %s carries delegated %+v", g.Key, g.Delegated)
			}
		}
	}
}
