package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// I5's promises, each on fixed Studio bodies (fakeStudio): the
// explain_failure walk, its own-text rule, decided parks, compact rows
// and compact spans. make mcp-check proves the same on real recorded
// runs (sub.failed, agentic.chaos, a denied thread turn, a nine-turn
// session).

func explainOn(t *testing.T, f fakeStudio, id string) (string, explanation) {
	t.Helper()
	res, _, err := (&mcpReader{s: f}).explainFailure(context.Background(), nil, mcpRunIn{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	got := texts(t, res)
	var ex explanation
	if err := json.Unmarshal([]byte(got[1]), &ex); err != nil {
		t.Fatalf("%v\n%s", err, got[1])
	}
	return got[0], ex
}

// TestMCPExplainOwnTextOnly (UX-H-23): a thread turn's input carries
// the previous turn's answer; a turn that produced no text of its own
// answers no text — never the earlier turn's.
func TestMCPExplainOwnTextOnly(t *testing.T) {
	line, ex := explainOn(t, fakeStudio{
		"/api/runs/s-t3":         {200, `{"id":"s-t3","agent":"a","status":"succeeded","steps":1,"holes":[]}`},
		"/api/runs/s-t3/steps/0": {200, `{"status":"ok","tool_calls":[],"holes":[]}`},
		"/api/runs/s-t3/transcript": {200, `{"batches":[` +
			`{"index":0,"step":0,"input":true,"messages":[{"role":"user","content":[{"type":"text","text":"deploy"}]},{"role":"assistant","content":[{"type":"text","text":"Done: deployed to prod."}]},{"role":"user","content":[{"type":"text","text":"status?"}]}]},` +
			`{"index":1,"step":0,"input":false,"messages":[{"role":"assistant","content":[{"type":"tool_call","id":"c","name":"status","args":{}}]}]}]}`},
	}, "s-t3")
	if ex.LastAssistantText != nil || ex.LastAssistantNote == "" || strings.Contains(line, "Done: deployed") ||
		!strings.Contains(line, "no assistant text this run") {
		t.Errorf("line %q, text %v note %q: want no text, never the earlier turn's", line, ex.LastAssistantText, ex.LastAssistantNote)
	}
	_, ex = explainOn(t, fakeStudio{
		"/api/runs/r":            {200, `{"id":"r","agent":"a","status":"succeeded","steps":1,"holes":[]}`},
		"/api/runs/r/steps/0":    {200, `{"status":"ok","holes":[]}`},
		"/api/runs/r/transcript": {200, `{"batches":[{"input":true,"messages":[{"role":"assistant","content":[{"type":"text","text":"old"}]}]},{"input":false,"messages":[{"role":"assistant","content":[{"type":"text","text":"mine"}]}]}]}`},
	}, "r")
	if ex.LastAssistantText == nil || *ex.LastAssistantText != "mine" {
		t.Errorf("own text = %v, want mine", ex.LastAssistantText)
	}
}

// TestMCPExplainDecided (UX-H-24): a parked run a resume decided reads
// decided — the call's outcome, who and why, and the resume — never
// "awaiting a decision"; the resume names the calls it decided.
func TestMCPExplainDecided(t *testing.T) {
	decided := `{"run":"s-t2","actor":"","source":"resume_calls","calls":[{"call_id":"d1","name":"deploy","outcome":"denied","via":"user","who":"alice","reason":"change freeze"}]}`
	line, ex := explainOn(t, fakeStudio{
		"/api/runs/s-t1":            {200, `{"id":"s-t1","agent":"ops","status":"succeeded","steps":1,"pending":1,"resumed_by":"s-t2","holes":[],"decided":` + decided + `}`},
		"/api/runs/s-t1/steps/0":    {200, `{"status":"parked","tool_calls":[{"call_id":"d1","name":"deploy","pending":true}],"holes":[]}`},
		"/api/runs/s-t1/transcript": {200, `{"batches":[]}`},
	}, "s-t1")
	for _, want := range []string{"parked, decided: deploy denied by alice: change freeze → resumed as s-t2", "tool calls: deploy denied by alice"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
	if strings.Contains(line, "awaiting") || strings.Contains(strings.Join(ex.Digest, "|"), "awaiting") {
		t.Errorf("a decided park reads awaiting: %q %q", line, ex.Digest)
	}
	if len(ex.Decisions) != 1 || ex.ResumedBy != "s-t2" {
		t.Errorf("decisions %q resumed_by %q", ex.Decisions, ex.ResumedBy)
	}
	line, _ = explainOn(t, fakeStudio{
		"/api/runs/s-t2":            {200, `{"id":"s-t2","agent":"ops","status":"succeeded","steps":1,"resumed_from":"s-t1","holes":[],"resumes":` + decided + `}`},
		"/api/runs/s-t2/steps/0":    {200, `{"status":"ok","holes":[]}`},
		"/api/runs/s-t2/transcript": {200, `{"batches":[]}`},
	}, "s-t2")
	if !strings.Contains(line, "resumed s-t1: deploy denied by alice: change freeze") {
		t.Errorf("resume line %q", line)
	}
	// An undecided park still reads awaiting.
	line, _ = explainOn(t, fakeStudio{
		"/api/runs/p":            {200, `{"id":"p","agent":"ops","status":"succeeded","steps":1,"pending":1,"holes":[]}`},
		"/api/runs/p/steps/0":    {200, `{"status":"parked","tool_calls":[{"call_id":"d1","name":"deploy","pending":true}],"holes":[]}`},
		"/api/runs/p/transcript": {200, `{"batches":[]}`},
	}, "p")
	if !strings.Contains(line, "parked: 1 calls awaiting a decision (deploy)") {
		t.Errorf("undecided line %q", line)
	}
}

// TestMCPExplainWalksStepsAndChildren (UX-H-25): a run that succeeded
// after a failed delegation: the walk reads every step, the error in
// step 0 — not the last step — and descends into the child: the first
// error is the child's own, naming the child run and its step.
func TestMCPExplainWalksStepsAndChildren(t *testing.T) {
	line, ex := explainOn(t, fakeStudio{
		"/api/runs/r": {200, `{"id":"r","agent":"lead","status":"succeeded","steps":2,"holes":[],` +
			`"children":[{"id":"r/0/w1","parent_call_id":"w1","agent":"worker","status":"failed","err":"weft: run failed at step 0: worker crashed","steps":0}]}`},
		"/api/runs/r/steps/0": {200, `{"status":"ok","reason":"tool_calls","attempts":[{"attempt":1,"outcome":"ok"}],` +
			`"tool_calls":[{"call_id":"w1","name":"work","child_run_id":"r/0/w1","result":{"content":"SUBAGENT_FAILED: worker crashed","is_error":true}}],"holes":[]}`},
		"/api/runs/r/steps/1":             {200, `{"status":"ok","reason":"stop","holes":[]}`},
		"/api/runs/r/transcript":          {200, `{"batches":[{"input":false,"messages":[{"role":"assistant","content":[{"type":"text","text":"worker failed, handling it myself"}]}]}]}`},
		"/api/runs/r%2F0%2Fw1":            {200, `{"id":"r/0/w1","agent":"worker","status":"failed","err":"weft: run failed at step 0: worker crashed","steps":0,"holes":[]}`},
		"/api/runs/r%2F0%2Fw1/steps/0":    {200, `{"status":"error","attempts":[{"attempt":1,"outcome":"error","error":"worker crashed"}],"holes":[]}`},
		"/api/runs/r%2F0%2Fw1/transcript": {200, `{"batches":[]}`},
	}, "r")
	f := ex.FirstError
	if f == nil || f.Run != "r/0/w1" || f.Step == nil || *f.Step != 0 || !strings.Contains(f.Error, "worker crashed") || f.Via != "step 0 work" {
		t.Fatalf("first_error %+v, want the child's step 0", f)
	}
	if !strings.Contains(line, "first error: step 0 work → child r/0/w1 (worker) step 0: model attempt 1: worker crashed") {
		t.Errorf("line %q", line)
	}
	digest := strings.Join(ex.Digest, "\n")
	for _, want := range []string{`step 0: ok (tool_calls): work error "SUBAGENT_FAILED: worker crashed"`, "step 1: ok (stop)",
		`child r/0/w1 (worker) ← work: failed "weft: run failed at step 0: worker crashed"`} {
		if !strings.Contains(digest, want) {
			t.Errorf("digest lacks %q:\n%s", want, digest)
		}
	}
}

// TestMCPCompactRows (UX-H-26): a list answer omits empty fields and
// the bulky ones, cuts hashes, says each cost reason once, and a
// session's turns omit what the session says.
func TestMCPCompactRows(t *testing.T) {
	row := func(id string) string {
		return `{"id":"` + id + `","parent_run_id":"","trace_id":"7ace0000000000000000000000000001","agent":"support",` +
			`"model":{"provider":"wefttest","name":"script"},"manifest_hash":"3a21f0000000000000000000000000000000","session_id":"s1",` +
			`"public_id":"pub","turn":1,"meta":{"k":"v"},"started":"2026-10-01T09:00:00Z","finished":"2026-10-01T09:00:01.5Z",` +
			`"status":"failed","err":"` + strings.Repeat("e", 300) + `","steps":2,"pending":0,"usage":{"input_tokens":10,"output_tokens":0},` +
			`"cost_usd":null,"cost_known":false,"cost_badge":"unpriced","cost_reason":"no price for x/y","unpriced_models":["x/y"],` +
			`"forked_from_hash":"abcdefabcdefabcdef","holes":[]}`
	}
	b := []byte(`{"id":"s1","agent":"support","public_id":"pub","runs":[` + row("a") + `,` + row("b") + `],"next_before":null}`)
	got := string(compactList(b, "runs", sessionSame))
	var doc struct {
		Runs        []map[string]any `json:"runs"`
		CostReasons []costReason     `json:"cost_reasons"`
	}
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.CostReasons) != 1 || doc.CostReasons[0] != (costReason{Reason: "no price for x/y", Rows: 2}) {
		t.Errorf("cost_reasons %+v, want the one reason once, 2 rows", doc.CostReasons)
	}
	r := doc.Runs[0]
	for _, k := range []string{"parent_run_id", "trace_id", "manifest_hash", "meta", "pending", "cost_usd", "cost_known", "cost_reason",
		"unpriced_models", "holes", "agent", "session_id", "public_id", "finished"} {
		if _, ok := r[k]; ok {
			t.Errorf("compact row keeps %s: %v", k, r)
		}
	}
	if r["model"] != "wefttest/script" || r["duration_ms"] != float64(1500) || r["cost_badge"] != "unpriced" ||
		r["forked_from_hash"] != "abcdefabcdef" || len([]rune(r["err"].(string))) != rowErrRunes+1 {
		t.Errorf("compact row %v", r)
	}
	if u := r["usage"].(map[string]any); len(u) != 1 || u["input_tokens"] != float64(10) {
		t.Errorf("usage %v, want the zero dropped", u)
	}
	if strings.Contains(got, "next_before") {
		t.Errorf("a null cursor kept: %s", got)
	}
	if got := string(compactList([]byte(`{"runs":[],"total":0}`), "runs", nil)); got != `{"runs":[]}` {
		t.Errorf("an empty page = %s, want its empty list", got)
	}
}

// TestMCPCompactSpans (UX-H-27): spans answer timing and the attributes
// that say what a span was; content attributes and events stay out.
func TestMCPCompactSpans(t *testing.T) {
	b := []byte(`{"spans":[` +
		`{"trace_id":"t","span_id":"s1","parent_span_id":"","name":"invoke_agent a","start":"2026-10-01T09:00:00Z","end":"2026-10-01T09:00:02Z","status":"unset","status_message":"","attrs":{"gen_ai.agent.name":"a","gen_ai.input.messages":"[secret]","weft.cwd":"/x"},"events":[],"run_id":"r"},` +
		`{"trace_id":"t","span_id":"s2","parent_span_id":"s1","name":"chat script","start":"2026-10-01T09:00:00.5Z","end":"2026-10-01T09:00:01Z","status":"error","status_message":"boom","attrs":{"gen_ai.usage.input_tokens":7,"error.type":"model_error"},"events":[{"name":"exception"}],"run_id":"r/0/c"}]}`)
	got := string(compactSpans(b, "r"))
	for _, want := range []string{`"dur_ms":2000`, `"start_ms":500`, `"dur_ms":500`, `"status":"error"`, `"status_message":"boom"`,
		`"gen_ai.usage.input_tokens":7`, `"error.type":"model_error"`, `"run_id":"r/0/c"`, `"events":1`, `"t0":"2026-10-01T09:00:00Z"`} {
		if !strings.Contains(got, want) {
			t.Errorf("compact spans lack %s:\n%s", want, got)
		}
	}
	for _, not := range []string{"secret", "weft.cwd", `"run_id":"r"`, `"unset"`, "trace_id"} {
		if strings.Contains(got, not) {
			t.Errorf("compact spans keep %s:\n%s", not, got)
		}
	}
}
