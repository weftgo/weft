package studio

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
	"github.com/weftgo/weft/obsdb/sqlite"
)

// The arena's real failure texts (weft-arena runs, v0.18.x): two
// usage-limit failures over different token counts at different steps,
// a retry give-up, a loop detection and a provider 429.
const (
	arenaUsage1 = "weft: run failed at step 2: weft: run exceeded its usage limit (input 30/25, output 15/0)"
	arenaUsage2 = "weft: run failed at step 1: weft: run exceeded its usage limit (input 41/25, output 7/0)"
	arenaRetry  = "weft: run failed at step 0: model stream: mw: giving up after 3 retries: server error"
	arenaLoop   = "weft: run failed at step 3: weft: the model repeated the same tool calls too many times: 3 identical steps in a row"
	arena429    = `weft: run failed at step 0: model stream: anthropic: POST "https://api.anthropic.com/v1/messages": 429 Too Many Requests (request_id: req_011CSHk4m9vZr8x2YbQp)`
	usageClass  = "weft: run failed at step N: weft: run exceeded its usage limit (input N/N, output N/N)"
)

// statsT0 is the stats fixture's day: its runs start in
// [statsT0, statsT0+24h), the window the goldens read.
var statsT0 = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

const statsWindow = "since=2020-01-01T00:00:00Z&until=2020-01-02T00:00:00Z"

// statsRun is one arena-shaped fixture run.
type statsRun struct {
	id, agent, public, provider, model string
	at                                 time.Duration // after statsT0
	dur                                time.Duration
	errType, err                       string
	steps                              []statsStep
	tools                              []statsTool
	attempts                           int // on step 0's call, the last one deciding it
	playground                         bool
}

type statsStep struct {
	in, out, ttft int64 // ttft 0: none
	dur           time.Duration
	failed        bool
}

type statsTool struct {
	name, errType string
	dur           time.Duration
}

func statsHex(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%016x", h.Sum64())
}

func (r statsRun) batch() obsdb.Batch {
	trace := statsHex(r.id+"/a") + statsHex(r.id+"/b")
	start := statsT0.Add(r.at)
	attrs := func(op string) map[string]any {
		a := map[string]any{"weft.run.id": r.id, "gen_ai.operation.name": op}
		if r.public != "" {
			a["weft.public_id"] = r.public
		}
		return a
	}
	root := attrs("invoke_agent")
	root["gen_ai.agent.name"], root["gen_ai.provider.name"], root["gen_ai.request.model"] = r.agent, r.provider, r.model
	root["weft.run.steps"] = int64(len(r.steps))
	if r.playground {
		root["weft.playground"] = true
	}
	if i := strings.LastIndex(r.id, "/"); i > 0 {
		root["weft.parent.run.id"] = r.id[:strings.LastIndex(r.id[:i], "/")]
		root["weft.parent.call.id"] = r.id[i+1:]
	}
	status := 1
	if r.err != "" {
		status, root["error.type"] = 2, r.errType
	}
	rootID := statsHex(r.id + "/root")
	spans := []obsdb.Span{}
	off := 10 * time.Millisecond
	for i, st := range r.steps {
		a := attrs("chat")
		a["weft.step.index"], a["gen_ai.provider.name"], a["gen_ai.request.model"] = int64(i), r.provider, r.model
		a["gen_ai.usage.input_tokens"], a["gen_ai.usage.output_tokens"] = st.in, st.out
		if st.ttft > 0 {
			a["weft.ttft_ms"] = st.ttft
		}
		cs := 1
		if st.failed {
			cs, a["error.type"] = 2, "model_error"
		}
		id := statsHex(fmt.Sprintf("%s/chat/%d", r.id, i))
		spans = append(spans, obsdb.Span{TraceID: trace, SpanID: id, ParentSpanID: rootID, Name: "chat " + r.model, Kind: 3,
			Start: start.Add(off), End: start.Add(off + st.dur), StatusCode: cs, Service: "arena", Attrs: a})
		if i == 0 {
			for n := 1; n <= r.attempts; n++ {
				aa := map[string]any{"weft.run.id": r.id, "weft.step.index": int64(0), "weft.attempt.index": int64(n)}
				as := 2
				if n == r.attempts && !st.failed {
					as = 1
				}
				spans = append(spans, obsdb.Span{TraceID: trace, SpanID: statsHex(fmt.Sprintf("%s/attempt/%d", r.id, n)), ParentSpanID: id,
					Name: "attempt", Kind: 3, Start: start.Add(off), End: start.Add(off + st.dur), StatusCode: as, Service: "arena", Attrs: aa})
			}
		}
		off += st.dur
	}
	for i, tl := range r.tools {
		a := attrs("execute_tool")
		a["gen_ai.tool.name"], a["gen_ai.tool.call.id"], a["weft.step.index"] = tl.name, fmt.Sprintf("call_%d", i), int64(0)
		ts := 1
		if tl.errType != "" {
			ts, a["error.type"] = 2, tl.errType
		}
		spans = append(spans, obsdb.Span{TraceID: trace, SpanID: statsHex(fmt.Sprintf("%s/tool/%d", r.id, i)), ParentSpanID: rootID,
			Name: "execute_tool " + tl.name, Kind: 1, Start: start.Add(off), End: start.Add(off + tl.dur), StatusCode: ts, Service: "arena", Attrs: a})
		off += tl.dur
	}
	spans = append(spans, obsdb.Span{TraceID: trace, SpanID: rootID, Name: "invoke_agent " + r.agent, Kind: 1,
		Start: start, End: start.Add(r.dur), StatusCode: status, StatusMessage: r.err, Service: "arena", Attrs: root})
	return obsdb.Batch{Spans: spans}
}

// statsFixtureRuns: the arena's day in nine runs — researcher on an
// unpriced zai model (one ok, two usage-limit failures, a loop), the
// director on a priced anthropic model (one ok, a retry give-up, a
// 429), orders on the scripted wefttest model (an exact $0, two tools),
// and a scoped run under pub_other.
func statsFixtureRuns() []statsRun {
	zai := func(r statsRun) statsRun {
		r.agent, r.provider, r.model = "researcher", "zai", "glm-5.3-flash"
		return r
	}
	haiku := func(r statsRun) statsRun {
		r.agent, r.provider, r.model = "director", "anthropic", "claude-haiku-4-5"
		return r
	}
	return []statsRun{
		zai(statsRun{id: "st_r1", at: 9 * time.Hour, dur: 4 * time.Second,
			steps: []statsStep{{in: 20, out: 10, ttft: 900, dur: 1500 * time.Millisecond}, {in: 30, out: 12, ttft: 700, dur: 1200 * time.Millisecond}},
			tools: []statsTool{{name: "web_search", dur: 800 * time.Millisecond}}}),
		zai(statsRun{id: "st_r2", at: 10 * time.Hour, dur: 3 * time.Second, errType: "usage_limit", err: arenaUsage1,
			steps: []statsStep{{in: 10, out: 5, ttft: 1100, dur: time.Second}, {in: 10, out: 5, ttft: 800, dur: time.Second}, {in: 10, out: 5, ttft: 600, dur: 900 * time.Millisecond}}}),
		zai(statsRun{id: "st_r3", at: 11 * time.Hour, dur: 2 * time.Second, errType: "usage_limit", err: arenaUsage2,
			steps: []statsStep{{in: 21, out: 4, ttft: 1000, dur: time.Second}, {in: 20, out: 3, ttft: 500, dur: 800 * time.Millisecond}}}),
		zai(statsRun{id: "st_r4", at: 12 * time.Hour, dur: 5 * time.Second, errType: "loop_detected", err: arenaLoop,
			steps: []statsStep{{in: 5, out: 2, ttft: 400, dur: time.Second}, {in: 5, out: 2, ttft: 400, dur: time.Second}, {in: 5, out: 2, ttft: 400, dur: time.Second}},
			tools: []statsTool{{name: "web_search", dur: 300 * time.Millisecond}, {name: "web_search", dur: 300 * time.Millisecond}, {name: "web_search", errType: "timeout", dur: 2 * time.Second}}}),
		haiku(statsRun{id: "st_d1", at: 9*time.Hour + 30*time.Minute, dur: 2 * time.Second,
			steps: []statsStep{{in: 1000, out: 200, ttft: 350, dur: 1800 * time.Millisecond}}, attempts: 2}),
		// director's subagent: a researcher run below st_d1 (its
		// delegated bill — unpriced, the zai model).
		zai(statsRun{id: "st_d1/0/call_9", at: 9*time.Hour + 30*time.Minute + 100*time.Millisecond, dur: time.Second,
			steps: []statsStep{{in: 40, out: 8, ttft: 300, dur: 500 * time.Millisecond}}}),
		haiku(statsRun{id: "st_d2", at: 13 * time.Hour, dur: 7 * time.Second, errType: "model_error", err: arenaRetry,
			steps: []statsStep{{dur: 6 * time.Second, failed: true}}, attempts: 3}),
		haiku(statsRun{id: "st_d3", at: 14 * time.Hour, dur: 500 * time.Millisecond, errType: "model_error", err: arena429,
			steps: []statsStep{{dur: 400 * time.Millisecond, failed: true}}}),
		{id: "st_o1", agent: "orders", provider: "wefttest", model: "script", at: 15 * time.Hour, dur: time.Second,
			steps: []statsStep{{in: 10, out: 5, ttft: 1, dur: 2 * time.Millisecond}, {in: 10, out: 5, ttft: 1, dur: 2 * time.Millisecond}},
			tools: []statsTool{{name: "lookup_order", dur: 5 * time.Millisecond}, {name: "refund", errType: "DENIED", dur: time.Millisecond}}},
		{id: "st_scoped", agent: "orders", public: "pub_other", provider: "wefttest", model: "script", at: 16 * time.Hour, dur: time.Second,
			steps: []statsStep{{in: 10, out: 5, ttft: 2, dur: 3 * time.Millisecond}}},
	}
}

func statsFixtureDB(t *testing.T) obsdb.DB {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, r := range statsFixtureRuns() {
		if err := db.Write(context.Background(), r.batch()); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestStatsGoldens pins GET /api/stats' shape, one golden per by, over
// the arena-shaped fixture: researcher's unpriced zai model reads
// unknown (never $0), director's haiku priced, orders' scripted model
// an exact $0; the retry give-up's three attempts and the 429 under
// director; the tools' p50 and failure rate.
func TestStatsGoldens(t *testing.T) {
	h := Handler(DB(statsFixtureDB(t)))
	for _, by := range []string{"agent", "model", "tool", "error_type", "step"} {
		code, _, body := get(t, h, "/studio/api/stats?by="+by+"&"+statsWindow)
		if code != http.StatusOK {
			t.Fatalf("stats?by=%s: %d %s", by, code, body)
		}
		golden(t, "stats-"+strings.ReplaceAll(by, "_", "-")+".golden.json", body)
	}
	// The numbers the Agents page reads, spot-checked.
	_, _, body := get(t, h, "/studio/api/stats?by=agent&"+statsWindow)
	for _, want := range []string{
		`"key":"researcher","runs":5,"failed":3,"failure_rate":0.6`,
		`"unpriced_models":["zai/glm-5.3-flash"]`,
		`"key":"director","runs":3,"failed":2`,
		`"retried":2,"retry_rate":0.666667`,
		`"key":"orders","runs":2,"failed":0`, `"cost_badge":"scripted"`,
		// director's subagent bill, apart from its own: unpriced.
		`"delegated":{"runs":1,"input_tokens":40,"output_tokens":8,"cost_usd":null,"cost_known":false,"cost_badge":"unpriced"`,
		`"runs_query":"agent=director\u0026all=1\u0026playground=false\u0026since=2020-01-01T00%3A00%3A00Z\u0026until=2020-01-02T00%3A00%3A00Z"`,
		`"buckets":24,"bucket_ms":3600000`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("stats?by=agent missing %s:\n%s", want, body)
		}
	}
}

// TestStatsRoute pins the parameters: by is required and closed, the
// window reads RFC 3339 or a duration, agent / tag / parent narrow,
// and a panel token's public id is forced (pinned in the auth matrix).
func TestStatsRoute(t *testing.T) {
	h := Handler(DB(statsFixtureDB(t)))
	for _, c := range []struct {
		path string
		code int
		want string
	}{
		{"", http.StatusBadRequest, "by must be agent, model, tool, error_type or step"},
		{"by=nope", http.StatusBadRequest, "by must be"},
		{"by=agent&since=yesterday", http.StatusBadRequest, "since must be RFC 3339 or a positive duration"},
		{"by=agent&until=soon", http.StatusBadRequest, "until must be RFC 3339"},
		{"by=agent&since=2020-01-02T00:00:00Z&until=2020-01-01T00:00:00Z", http.StatusBadRequest, "since before until"},
		{"by=agent&since=7d", http.StatusOK, `"groups":[]`}, // the fixture is from 2020
		{"by=agent&since=24h&until=2020-01-02T00:00:00Z", http.StatusOK, `"key":"researcher"`},
		{"by=agent&agent=director&" + statsWindow, http.StatusOK, `"groups":[{"key":"director"`},
		{"by=agent&public_id=pub_other&" + statsWindow, http.StatusOK, `"groups":[{"key":"orders","runs":1,`},
		{"by=tool&agent=orders&" + statsWindow, http.StatusOK, `"key":"lookup_order","runs":1,"failed":0,"failure_rate":0,"calls":1`},
		{"by=agent&parent=&" + statsWindow, http.StatusOK, `"key":"researcher"`},
		{"by=agent&playground=maybe", http.StatusBadRequest, "playground must be true, false or all"},
		{"by=agent&status=nope", http.StatusBadRequest, "status"},
		{"by=agent&since=1000-01-01T00:00:00Z&until=2020-01-02T00:00:00Z", http.StatusBadRequest, "years 1678..2262"},
		{"by=agent&since=2020-01-01T00:00:00Z&until=3000-01-01T00:00:00Z", http.StatusBadRequest, "years 1678..2262"},
		{"by=agent&since=999999999d", http.StatusBadRequest, "since must be"},
		{"by=agent&experiment_id=exp_none&" + statsWindow, http.StatusOK, `"groups":[]`},
		{"by=agent&playground=false&status=failed&" + statsWindow, http.StatusOK, `"key":"researcher","runs":3,"failed":3`},
	} {
		code, _, body := get(t, h, "/studio/api/stats?"+c.path)
		if code != c.code || !strings.Contains(body, c.want) {
			t.Errorf("stats?%s = %d %s, want %d with %s", c.path, code, body, c.code, c.want)
		}
	}
	// A tool group has no model usage: no cost, said so; DENIED is a
	// denial, not a failure.
	_, _, body := get(t, h, "/studio/api/stats?by=tool&"+statsWindow)
	for _, want := range []string{`"key":"refund","runs":1,"failed":0,"failure_rate":0,"calls":1,"failed_calls":0`,
		`"cost_usd":null,"cost_known":false,"cost_badge":"unknown","cost_reason":"tool calls carry no model usage to price"`,
		`"retry_rate":null,"denials":1`} {
		if !strings.Contains(body, want) {
			t.Errorf("stats?by=tool missing %s:\n%s", want, body)
		}
	}
	checkRunsQueries(t, h)
	// The meta capability.
	if _, _, meta := get(t, h, "/studio/api/meta"); !strings.Contains(meta, `"stats"`) {
		t.Errorf("meta without the stats capability: %s", meta)
	}
}

// TestErrorClassFacet is the Done line's second half (UX-C-37): the
// search's error_class facet groups the arena's two usage-limit
// failures — different token counts, different steps — into one class,
// whose string filters /api/search and /api/runs to exactly those runs.
func TestErrorClassFacet(t *testing.T) {
	h := Handler(DB(statsFixtureDB(t)))
	code, _, body := get(t, h, "/studio/api/search?status=failed")
	if code != http.StatusOK {
		t.Fatalf("search: %d %s", code, body)
	}
	if !strings.Contains(body, `"facets":{"error_class":[{"class":"`+usageClass+`","count":2},`) {
		t.Errorf("facet: want the usage-limit class first with 2 runs:\n%s", body)
	}
	for _, class := range []string{
		"weft: run failed at step N: model stream: mw: giving up after N retries: server error",
		"weft: run failed at step N: weft: the model repeated the same tool calls too many times: N identical steps in a row",
		`weft: run failed at step N: model stream: anthropic: POST \"https://api.anthropic.com/v1/messages\": N Too Many Requests (request_id: \u003cid\u003e)`, // <id>, HTML-escaped by encoding/json
	} {
		if !strings.Contains(body, `{"class":"`+class+`","count":1}`) {
			t.Errorf("facet missing %s:\n%s", class, body)
		}
	}
	v := url.QueryEscape(usageClass)
	ids, total, _ := searchIDs(t, h, "/studio/api/search?error_class="+v)
	if strings.Join(ids, ",") != "st_r3,st_r2" || total != 2 {
		t.Errorf("search?error_class=<usage> = %v (total %d), want st_r3,st_r2", ids, total)
	}
	// The facet counts the match without its own filter: still every class.
	if _, _, b := get(t, h, "/studio/api/search?error_class="+v); !strings.Contains(b, `"count":1}`) {
		t.Errorf("search?error_class= facet narrowed by its own filter: %s", b)
	}
	ids, total, _ = searchIDs(t, h, "/studio/api/runs?error_class="+v)
	if strings.Join(ids, ",") != "st_r3,st_r2" || total != 2 {
		t.Errorf("runs?error_class=<usage> = %v (total %d), want st_r3,st_r2", ids, total)
	}
	if ids, total, _ = searchIDs(t, h, "/studio/api/runs?agent=director&error_class="+v); len(ids) != 0 || total != 0 {
		t.Errorf("runs?agent=director&error_class=<usage> = %v, want none", ids)
	}
	if ids, _, _ = searchIDs(t, h, "/studio/api/runs?error_class=no+such+class"); len(ids) != 0 {
		t.Errorf("runs?error_class=<unknown> = %v, want none", ids)
	}
}

// noAggregates is a DB that does not implement obsdb.Aggregator.
type noAggregates struct{ obsdb.DB }

// TestStatsOffWithoutAggregator: a database that cannot aggregate has
// no stats route, no facet, and says why; the class filter is 501.
func TestStatsOffWithoutAggregator(t *testing.T) {
	h := Handler(DB(noAggregates{statsFixtureDB(t)}))
	if code, _, body := get(t, h, "/studio/api/stats?by=agent"); code != http.StatusNotFound {
		t.Errorf("stats without an Aggregator = %d %s, want 404", code, body)
	}
	if _, _, meta := get(t, h, "/studio/api/meta"); !strings.Contains(meta, `"stats":"the database cannot aggregate`) {
		t.Errorf("meta.capabilities_off without stats' reason: %s", meta)
	}
	if code, _, body := get(t, h, "/studio/api/runs?error_class=x"); code != http.StatusNotImplemented {
		t.Errorf("runs?error_class= without an Aggregator = %d %s, want 501", code, body)
	}
}

// TestPassPredicateBounded: the predicate is kept verbatim up to
// maxPassPredicateBytes; a longer one is 400.
func TestPassPredicateBounded(t *testing.T) {
	h := Handler(DB(statsFixtureDB(t)), Playground(true))
	exp := func(pred string) string {
		return `{"id":"exp_p","agent":"orders","variants":[{"key":"A","overrides":{}}],"inputs":[{"key":"1","text":"hi"}],"pass_predicate":"` + pred + `"}`
	}
	if code, _, body := post(t, h, "/studio/api/experiments", "application/json", exp(`status = succeeded`)); code != http.StatusOK ||
		!strings.Contains(body, `"pass_predicate":"status = succeeded"`) {
		t.Errorf("save with a predicate = %d %s", code, body)
	}
	if code, _, body := post(t, h, "/studio/api/experiments", "application/json", exp(strings.Repeat("x", maxPassPredicateBytes+1))); code != http.StatusBadRequest ||
		!strings.Contains(body, "pass_predicate is 4097 bytes, at most 4096") {
		t.Errorf("save with a long predicate = %d %s, want 400", code, body)
	}
}

// TestStatsGoldensClickHouse: the same fixture through the ClickHouse
// backend answers the same bodies, byte for byte — the goldens hold on
// both backends (gated on WEFT_CLICKHOUSE_DSN, as the conformance
// table is).
func TestStatsGoldensClickHouse(t *testing.T) {
	if os.Getenv("WEFT_CLICKHOUSE_DSN") == "" {
		t.Skip("WEFT_CLICKHOUSE_DSN not set: the ClickHouse half needs a server")
	}
	db := freshClickHouse(t)
	for _, r := range statsFixtureRuns() {
		if err := db.Write(context.Background(), r.batch()); err != nil {
			t.Fatal(err)
		}
	}
	h := Handler(DB(db))
	for _, by := range []string{"agent", "model", "tool", "error_type", "step"} {
		code, _, body := get(t, h, "/studio/api/stats?by="+by+"&"+statsWindow)
		if code != http.StatusOK {
			t.Fatalf("stats?by=%s on ClickHouse: %d %s", by, code, body)
		}
		golden(t, "stats-"+strings.ReplaceAll(by, "_", "-")+".golden.json", body)
	}
	_, _, body := get(t, h, "/studio/api/search?status=failed")
	if !strings.Contains(body, `"facets":{"error_class":[{"class":"`+usageClass+`","count":2},`) {
		t.Errorf("ClickHouse facet: want the usage-limit class first with 2 runs:\n%s", body)
	}
	ids, total, _ := searchIDs(t, h, "/studio/api/runs?error_class="+url.QueryEscape(usageClass))
	if strings.Join(ids, ",") != "st_r3,st_r2" || total != 2 {
		t.Errorf("ClickHouse runs?error_class=<usage> = %v (total %d), want st_r3,st_r2", ids, total)
	}
	checkRunsQueries(t, h)
}

// checkRunsQueries: every agent and error_type group's runs_query,
// sent to /api/search, totals the group's runs — the card's link
// lands on the same N — whatever filters the stats query read.
func checkRunsQueries(t *testing.T, h http.Handler) {
	t.Helper()
	for _, path := range []string{"by=agent&" + statsWindow, "by=error_type&" + statsWindow,
		"by=agent&parent=&" + statsWindow, "by=agent&status=failed&" + statsWindow} {
		_, _, body := get(t, h, "/studio/api/stats?"+path)
		var page struct {
			Groups []struct {
				Key       string `json:"key"`
				Runs      int    `json:"runs"`
				RunsQuery string `json:"runs_query"`
			} `json:"groups"`
		}
		decode(t, body, &page)
		for _, g := range page.Groups {
			if g.RunsQuery == "" {
				t.Errorf("stats?%s: group %q has no runs_query", path, g.Key)
				continue
			}
			if _, total, _ := searchIDs(t, h, "/studio/api/search?"+g.RunsQuery); total != g.Runs {
				t.Errorf("stats?%s: group %q counts %d runs, its runs_query %q lists %d", path, g.Key, g.Runs, g.RunsQuery, total)
			}
		}
	}
	// No runs filter reproduces a model's, tool's or step's group.
	for _, by := range []string{"model", "tool", "step"} {
		if _, _, body := get(t, h, "/studio/api/stats?by="+by+"&"+statsWindow); strings.Contains(body, "runs_query") {
			t.Errorf("stats?by=%s carries a runs_query: %s", by, body)
		}
	}
}

// TestStatsPlaygroundDefault (P10a-pre-go-fix item 3): health numbers
// never mix replays into the app's own — one real run and two scripted
// replays of the same agent count 1 by default, 2 with playground=true,
// 3 with playground=all, and each group's runs_query (the effective
// filter carried) lands on the same N.
func TestStatsPlaygroundDefault(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for i, pg := range []bool{false, true, true} {
		r := statsRun{id: fmt.Sprintf("pg_%d", i), agent: "orders", provider: "wefttest", model: "script",
			at: time.Duration(i+1) * time.Hour, dur: time.Second, playground: pg,
			steps: []statsStep{{in: 10, out: 5, ttft: 1, dur: 2 * time.Millisecond}}}
		if err := db.Write(context.Background(), r.batch()); err != nil {
			t.Fatal(err)
		}
	}
	h := Handler(DB(db))
	for _, c := range []struct {
		param, query string
		runs         int
	}{
		{"", "playground=false", 1},
		{"&playground=false", "playground=false", 1},
		{"&playground=true", "playground=true", 2},
		{"&playground=all", "", 3},
	} {
		code, _, body := get(t, h, "/studio/api/stats?by=agent&"+statsWindow+c.param)
		var page struct {
			Groups []struct {
				Runs      int    `json:"runs"`
				Calls     int    `json:"calls"`
				RunsQuery string `json:"runs_query"`
			} `json:"groups"`
		}
		decode(t, body, &page)
		if code != http.StatusOK || len(page.Groups) != 1 || page.Groups[0].Runs != c.runs || page.Groups[0].Calls != c.runs {
			t.Errorf("stats?by=agent%s = %d %s, want one group of %d runs and calls", c.param, code, body, c.runs)
			continue
		}
		g := page.Groups[0]
		if (c.query == "") == strings.Contains(g.RunsQuery, "playground=") || c.query != "" && !strings.Contains(g.RunsQuery, c.query) {
			t.Errorf("stats?by=agent%s runs_query = %q, want the filter %q", c.param, g.RunsQuery, c.query)
		}
		if _, total, _ := searchIDs(t, h, "/studio/api/search?"+g.RunsQuery); total != c.runs {
			t.Errorf("stats?by=agent%s: runs_query %q lists %d, want %d", c.param, g.RunsQuery, total, c.runs)
		}
	}
}

// TestStatsScriptedZeroUsage (P10a-pre-go-fix item 4): an all-scripted
// group — the scripted engine reports no usage by design — reads the
// exact $0 its runs read, never "no model usage recorded".
func TestStatsScriptedZeroUsage(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for i := range 2 {
		r := statsRun{id: fmt.Sprintf("sz_%d", i), agent: "orders", provider: "weft/runtime", model: "scripted",
			at: time.Duration(i+1) * time.Hour, dur: time.Second,
			steps: []statsStep{{dur: 2 * time.Millisecond}}}
		if err := db.Write(context.Background(), r.batch()); err != nil {
			t.Fatal(err)
		}
	}
	h := Handler(DB(db))
	const want = `"cost_usd":0,"cost_known":true,"cost_badge":"scripted","cost_reason":"a scripted model: nothing bills it, an exact $0","cache":"exact"`
	for _, by := range []string{"model", "agent", "step"} {
		if _, _, body := get(t, h, "/studio/api/stats?by="+by+"&"+statsWindow); !strings.Contains(body, want) {
			t.Errorf("stats?by=%s, an all-scripted group: want the exact $0:\n%s", by, body)
		}
	}
}

// TestStatsScriptedBesideIdleModel (P10a-pre-go carried LOW): a group
// whose scripted calls sit beside a real model's calls that reported no
// usage is not the scripted $0 — the real model's part is unknown, so
// the group's cost is too.
func TestStatsScriptedBesideIdleModel(t *testing.T) {
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	runs := []statsRun{
		{id: "sm_0", agent: "orders", provider: "weft/runtime", model: "scripted", playground: true,
			at: time.Hour, dur: time.Second, steps: []statsStep{{dur: 2 * time.Millisecond}}},
		{id: "sm_1", agent: "orders", provider: "anthropic", model: "claude-sonnet-4-5", playground: true,
			at: 2 * time.Hour, dur: time.Second, steps: []statsStep{{dur: 2 * time.Millisecond, failed: true}}},
	}
	for _, r := range runs {
		if err := db.Write(context.Background(), r.batch()); err != nil {
			t.Fatal(err)
		}
	}
	h := Handler(DB(db))
	for _, pg := range []string{"true", "all"} {
		_, _, body := get(t, h, "/studio/api/stats?by=agent&playground="+pg+"&"+statsWindow)
		if strings.Contains(body, `"cost_badge":"scripted"`) {
			t.Errorf("playground=%s: a real model's idle part beside scripted ones read the scripted $0:\n%s", pg, body)
		}
		if !strings.Contains(body, `"cost_known":false,"cost_badge":"unknown","cost_reason":"no model usage recorded"`) {
			t.Errorf("playground=%s: want the unknown cost, no usage recorded:\n%s", pg, body)
		}
	}
}
