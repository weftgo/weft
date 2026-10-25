package obsdb_test

import (
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// TestErrorClass pins the normaliser on the arena's real error texts
// (weft-arena runs on v0.18.x) and the rules' edges: numbers N, ids
// <id>, quoted strings and model names kept — and the Done line's
// grouping, every usage-limit failure one class whatever its counts and
// step.
func TestErrorClass(t *testing.T) {
	usage := "weft: run failed at step N: weft: run exceeded its usage limit (input N/N, output N/N)"
	for _, c := range []struct{ in, want string }{
		{"weft: run failed at step 2: weft: run exceeded its usage limit (input 30/25, output 15/0)", usage},
		{"weft: run failed at step 0: weft: run exceeded its usage limit (input 1234/1000, output 0/0)", usage},
		{"weft: run failed at step 1: model stream: mw: giving up after 3 retries: server error",
			"weft: run failed at step N: model stream: mw: giving up after N retries: server error"},
		{"weft: run failed at step 3: weft: the model repeated the same tool calls too many times: 3 identical steps in a row",
			"weft: run failed at step N: weft: the model repeated the same tool calls too many times: N identical steps in a row"},
		{`weft: run failed at step 0: model stream: anthropic: POST "https://api.anthropic.com/v1/messages": 429 Too Many Requests (request_id: req_011CSHk4m9vZr8x2YbQp)`,
			`weft: run failed at step N: model stream: anthropic: POST "https://api.anthropic.com/v1/messages": N Too Many Requests (request_id: <id>)`},
		{"openai: 429 rate limit reached for gpt-4o-mini in organization org-AbCdEf123456 on tokens per min: Limit 30000, Used 29512, Requested 1200. Please try again in 1.5s.",
			"openai: N rate limit reached for gpt-4o-mini in organization <id> on tokens per min: Limit N, Used N, Requested N. Please try again in Ns."},
		{`weft: a tool asked the model to retry too many times: tool "lookup_42" retried 3 times, past MaxModelRetries(2)`,
			`weft: a tool asked the model to retry too many times: tool "lookup_42" retried N times, past MaxModelRetries(N)`},
		{"zai/glm-5.3-flash: context deadline exceeded after 30s", "zai/glm-5.3-flash: context deadline exceeded after Ns"},
		{"run 3f2a9c1e4b5d6a7f8e9d0c1b2a3f4e5d: cancelled", "run <id>: cancelled"},
		{"trace 0f8fad5b-d9cb-469f-a165-70867728950e lost", "trace <id> lost"},
		{"child 3f2a9c1e4b5d6a7f/0/call_8f3a9b2c11 failed", "child <id>/N/<id> failed"},
		{"agent r_9 failed", "agent r_9 failed"}, // a word with letters stays
		{"deadbeef is no id, cafe1234 is", "deadbeef is no id, <id> is"},
		{"  -1 items, `raw 42` kept, unclosed \"quote 7", "N items, `raw 42` kept, unclosed \"quote 7"},
		{"", ""},
	} {
		if got := obsdb.ErrorClass(c.in); got != c.want {
			t.Errorf("ErrorClass(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
	}
	long := strings.Repeat("é", 300)
	if got := obsdb.ErrorClass(long); len(got) > obsdb.MaxErrorClassBytes+len("…") || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "éé") {
		t.Errorf("a long class = %d bytes %q…, want cut at %d bytes on a rune boundary with …", len(got), got[:10], obsdb.MaxErrorClassBytes)
	}
}

// TestErrorClasses: the fold counts runs per class, most first, and a
// class resolves back to its texts — none for a class no text has.
func TestErrorClasses(t *testing.T) {
	counts := []obsdb.ErrorCount{
		{Err: "weft: run failed at step 2: weft: run exceeded its usage limit (input 30/25, output 15/0)", Count: 4},
		{Err: "boom 1", Count: 5},
		{Err: "weft: run failed at step 1: weft: run exceeded its usage limit (input 41/25, output 7/0)", Count: 3},
		{Err: "", Count: 9},
	}
	got := obsdb.ErrorClasses(counts, 0)
	if len(got) != 2 || got[0].Count != 7 || !strings.Contains(got[0].Class, "usage limit") || got[1] != (obsdb.ErrorClassCount{Class: "boom N", Count: 5}) {
		t.Errorf("ErrorClasses = %v", got)
	}
	if got := obsdb.ErrorClasses(counts, 1); len(got) != 1 {
		t.Errorf("ErrorClasses(limit 1) = %v", got)
	}
	if texts := obsdb.ErrorClassTexts(counts, got[0].Class); len(texts) != 2 {
		t.Errorf("ErrorClassTexts(usage) = %v, want both texts", texts)
	}
	if texts := obsdb.ErrorClassTexts(counts, "nothing"); texts == nil || len(texts) != 0 {
		t.Errorf("ErrorClassTexts(unknown) = %#v, want an empty non-nil set", texts)
	}
}

// TestBuildStats: cells of one key and bucket add up, a bucket past the
// series clamps into it, by=step sorts by step index, and the cut past
// MaxStatsGroups is counted.
func TestBuildStats(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0).UTC()
	q := obsdb.StatsQuery{By: obsdb.StatsByStep, Since: t0, Until: t0.Add(4 * time.Hour), Buckets: 4}
	page := obsdb.BuildStats(q, []obsdb.StatsCell{
		{Key: "10", Bucket: 0, Runs: 1, Calls: 1},
		{Key: "2", Bucket: 9, Runs: 1, Calls: 2},
		{Key: "2", Bucket: 3, Runs: 1, Calls: 1, InputTokens: 5},
	}, []obsdb.StatsModelPart{{Key: "2", ModelUsage: obsdb.ModelUsage{Provider: "p", Model: "m"}}},
		[]obsdb.StatsPercentiles{{Key: "3", Duration: obsdb.Percentiles{P50: 1, P95: 2, N: 2}}})
	if page.Bucket != time.Hour || len(page.Groups) != 3 || page.Groups[0].Key != "2" || page.Groups[1].Key != "3" || page.Groups[2].Key != "10" {
		t.Fatalf("BuildStats = %+v", page)
	}
	g := page.Groups[0]
	if g.Runs != 2 || g.Calls != 3 || g.Series[3].Runs != 2 || g.Series[3].InputTokens != 5 || len(g.Models) != 1 {
		t.Errorf("group 2 = %+v", g)
	}
	q.By = obsdb.StatsByTool
	var cells []obsdb.StatsCell
	for i := range obsdb.MaxStatsGroups + 3 {
		cells = append(cells, obsdb.StatsCell{Key: string(rune('a'+i%26)) + strings.Repeat("x", i/26), Runs: int64(i)})
	}
	if page := obsdb.BuildStats(q, cells, nil, nil); len(page.Groups) != obsdb.MaxStatsGroups || page.GroupsCut != 3 || page.Groups[0].Runs != int64(obsdb.MaxStatsGroups+2) {
		t.Errorf("cut = %d groups, %d cut, first %d runs", len(page.Groups), page.GroupsCut, page.Groups[0].Runs)
	}
	if obsdb.PercentileRank(50, 1) != 1 || obsdb.PercentileRank(50, 4) != 2 || obsdb.PercentileRank(95, 4) != 4 || obsdb.PercentileRank(95, 20) != 19 {
		t.Error("PercentileRank off the nearest-rank rule")
	}
}
