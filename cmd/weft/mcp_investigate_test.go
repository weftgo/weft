package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
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
// the bulky ones, cuts hashes, keeps what says a figure is unknown
// (cost_known false, a cache that is not exact), and says each cost
// reason once with the ids of the rows that gave it.
func TestMCPCompactRows(t *testing.T) {
	row := func(id, cache string) string {
		return `{"id":"` + id + `","parent_run_id":"","trace_id":"7ace0000000000000000000000000001","agent":"support",` +
			`"model":{"provider":"wefttest","name":"script"},"manifest_hash":"3a21f0000000000000000000000000000000","session_id":"s1",` +
			`"public_id":"pub","turn":1,"meta":{"k":"v"},"started":"2026-10-01T09:00:00Z","finished":"2026-10-01T09:00:01.5Z",` +
			`"status":"failed","err":"` + strings.Repeat("e", 300) + `","steps":2,"pending":0,"usage":{"input_tokens":10,"output_tokens":0},` +
			`"cost_usd":null,"cost_known":false,"cost_badge":"unpriced","cost_reason":"no price for x/y","unpriced_models":["x/y"],` +
			`"cache":"` + cache + `","delegated_cost_usd":0,"delegated_cost_known":true,"weft_version":"v1","service":"svc",` +
			`"forked_from_hash":"abcdefabcdefabcdef","holes":[]}`
	}
	b := []byte(`{"runs":[` + row("a", "exact") + `,` + row("b", "not_reported") + `],"next_before":null}`)
	got := string(compactList(b, "runs", nil))
	var doc struct {
		Runs        []map[string]any `json:"runs"`
		CostReasons []costReason     `json:"cost_reasons"`
	}
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.CostReasons) != 1 || doc.CostReasons[0].Reason != "no price for x/y" || strings.Join(doc.CostReasons[0].Rows, ",") != "a,b" {
		t.Errorf("cost_reasons %+v, want the one reason once, naming rows a and b", doc.CostReasons)
	}
	r := doc.Runs[0]
	for _, k := range []string{"parent_run_id", "trace_id", "manifest_hash", "meta", "pending", "cost_usd", "cost_reason",
		"unpriced_models", "holes", "finished", "cache", "delegated_cost_known", "delegated_cost_usd", "weft_version", "service"} {
		if _, ok := r[k]; ok {
			t.Errorf("compact row keeps %s: %v", k, r)
		}
	}
	if r["model"] != "wefttest/script" || r["duration_ms"] != float64(1500) || r["cost_badge"] != "unpriced" || r["cost_known"] != false ||
		r["forked_from_hash"] != "abcdefabcdef" || len([]rune(r["err"].(string))) != rowErrRunes+1 || r["agent"] != "support" {
		t.Errorf("compact row %v", r)
	}
	if doc.Runs[1]["cache"] != "not_reported" {
		t.Errorf("a cache that is not exact dropped: %v", doc.Runs[1])
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

// TestMCPCompactSession (UX-H-26): a session's turns omit what the
// session says and what turn_defaults says once; a turn that differs
// keeps it ("none" when it has none); the session row's outcomes strip
// is counted and its known flags omitted when true.
func TestMCPCompactSession(t *testing.T) {
	turn := func(id, stop string) string {
		return `{"id":"` + id + `","agent":"support","session_id":"s1","public_id":"pub","model":{"provider":"p","name":"m"},` +
			`"status":"succeeded","stop_reason":"` + stop + `","cost_badge":"scripted","started":"2026-10-01T09:00:00Z",` +
			`"prompt_preview":"` + strings.Repeat("p", 200) + `"}`
	}
	b := []byte(`{"id":"s1","agent":"support","public_id":"pub","cache":"exact","cost_known":true,"delegated_sessions_cost_known":true,` +
		`"outcomes":["succeeded","succeeded","failed"],"first_prompt":"` + strings.Repeat("f", 200) + `",` +
		`"runs":[` + turn("s1-t1", "stop") + `,` + turn("s1-t2", "stop") + `,` + turn("s1-t3", "") + `]}`)
	got := string(compactList(b, "runs", sessionSame))
	var doc struct {
		Runs          []map[string]any `json:"runs"`
		TurnDefaults  map[string]any   `json:"turn_defaults"`
		OutcomeCounts map[string]any   `json:"outcome_counts"`
		FirstPrompt   string           `json:"first_prompt"`
	}
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.TurnDefaults["model"] != "p/m" || doc.TurnDefaults["stop_reason"] != "stop" || doc.TurnDefaults["cost_badge"] != "scripted" {
		t.Errorf("turn_defaults %v", doc.TurnDefaults)
	}
	for i, r := range doc.Runs {
		for _, k := range []string{"agent", "session_id", "public_id", "model", "cost_badge"} {
			if _, ok := r[k]; ok {
				t.Errorf("turn %d keeps %s: %v", i, k, r)
			}
		}
		if n := len([]rune(r["prompt_preview"].(string))); n != promptRunes+1 {
			t.Errorf("turn %d prompt_preview %d runes", i, n)
		}
	}
	if _, ok := doc.Runs[1]["stop_reason"]; ok || doc.Runs[2]["stop_reason"] != "none" {
		t.Errorf("stop_reason: t2 %v t3 %v, want t2's omitted and t3's none", doc.Runs[1]["stop_reason"], doc.Runs[2]["stop_reason"])
	}
	if doc.OutcomeCounts["succeeded"] != float64(2) || doc.OutcomeCounts["failed"] != float64(1) || len([]rune(doc.FirstPrompt)) != promptRunes+1 {
		t.Errorf("outcome_counts %v first_prompt %d runes", doc.OutcomeCounts, len([]rune(doc.FirstPrompt)))
	}
	for _, k := range []string{`"cache"`, `"cost_known"`, `"delegated_sessions_cost_known"`, `"outcomes"`} {
		if strings.Contains(got, k+":") {
			t.Errorf("session keeps %s: %s", k, got)
		}
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

// countingStudio is fakeStudio counting the reads it answered.
type countingStudio struct {
	fakeStudio
	n *int
}

func (c countingStudio) get(ctx context.Context, path string, q url.Values) (int, []byte, error) {
	*c.n++
	return c.fakeStudio.get(ctx, path, q)
}

// TestMCPExplainReusedCallIDs: call ids repeat across steps (wefttest
// and the adapters number call_1 every step), so a child is matched by
// the step's child_run_id or core's <run>/<step>/<call> id, never by
// call id alone: the first error descends into step 1's child, the
// digest names each child by its own step's tool, in step order.
func TestMCPExplainReusedCallIDs(t *testing.T) {
	_, ex := explainOn(t, fakeStudio{
		"/api/runs/r": {200, `{"id":"r","agent":"lead","status":"succeeded","steps":3,"holes":[],"children":[` +
			`{"id":"r/1/call_1","parent_call_id":"call_1","agent":"auditor","status":"failed","err":"weft: run failed at step 0: ledger locked"},` +
			`{"id":"r/0/call_1","parent_call_id":"call_1","agent":"researcher","status":"succeeded"}]}`},
		"/api/runs/r/steps/0":              {200, `{"status":"ok","tool_calls":[{"call_id":"call_1","name":"research","result":{"content":"found it"}}],"holes":[]}`},
		"/api/runs/r/steps/1":              {200, `{"status":"ok","tool_calls":[{"call_id":"call_1","name":"audit","result":{"content":"SUBAGENT_FAILED: ledger locked","is_error":true}}],"holes":[]}`},
		"/api/runs/r/steps/2":              {200, `{"status":"ok","reason":"stop","holes":[]}`},
		"/api/runs/r/transcript":           {200, `{"batches":[]}`},
		"/api/runs/r%2F1%2Fcall_1":         {200, `{"id":"r/1/call_1","agent":"auditor","status":"failed","err":"weft: run failed at step 0: ledger locked","steps":0,"holes":[]}`},
		"/api/runs/r%2F1%2Fcall_1/steps/0": {200, `{"status":"error","attempts":[{"attempt":1,"outcome":"error","error":"ledger locked"}],"holes":[]}`},
	}, "r")
	if f := ex.FirstError; f == nil || f.Run != "r/1/call_1" || f.Via != "step 1 audit" {
		t.Fatalf("first_error %+v, want step 1's child", ex.FirstError)
	}
	d := strings.Join(ex.Digest, "\n")
	i0, i1 := strings.Index(d, "child r/0/call_1 (researcher) ← research"), strings.Index(d, "child r/1/call_1 (auditor) ← audit")
	if i0 < 0 || i1 < 0 || i0 > i1 {
		t.Errorf("children mislabelled or out of step order:\n%s", d)
	}
}

// TestMCPExplainPrefersUnrecovered: a model attempt a retry recovered
// from is not the first error when a later call failed for good; alone,
// it is, marked retried.
func TestMCPExplainPrefersUnrecovered(t *testing.T) {
	steps := `{"status":"ok","attempts":[{"attempt":1,"outcome":"error","http_status":429,"error":"rate limited"},{"attempt":2,"outcome":"ok"}],"holes":[]}`
	_, ex := explainOn(t, fakeStudio{
		"/api/runs/r":            {200, `{"id":"r","agent":"a","status":"succeeded","steps":2,"holes":[]}`},
		"/api/runs/r/steps/0":    {200, steps},
		"/api/runs/r/steps/1":    {200, `{"status":"ok","tool_calls":[{"call_id":"c","name":"refund","result":{"content":"card declined","is_error":true}}],"holes":[]}`},
		"/api/runs/r/transcript": {200, `{"batches":[]}`},
	}, "r")
	if f := ex.FirstError; f == nil || f.Tool != "refund" || *f.Step != 1 {
		t.Errorf("first_error %+v, want step 1's refund", ex.FirstError)
	}
	_, ex = explainOn(t, fakeStudio{
		"/api/runs/r":            {200, `{"id":"r","agent":"a","status":"succeeded","steps":1,"holes":[]}`},
		"/api/runs/r/steps/0":    {200, steps},
		"/api/runs/r/transcript": {200, `{"batches":[]}`},
	}, "r")
	if f := ex.FirstError; f == nil || !strings.Contains(f.Error, "rate limited (retried, attempt 2 ok)") {
		t.Errorf("first_error %+v, want the recovered attempt marked retried", ex.FirstError)
	}
}

// TestMCPExplainDigestLineCap: a step of 200 calls is one line of at
// most ~1 KB, the rest counted; the digest sits after next and the
// text, so a cut at max_bytes takes it first.
func TestMCPExplainDigestLineCap(t *testing.T) {
	var calls []string
	for i := range 200 {
		calls = append(calls, fmt.Sprintf(`{"call_id":"c%d","name":"lookup_%03d","result":{"content":"%s","is_error":true}}`, i, i, strings.Repeat("x", 100)))
	}
	f := fakeStudio{
		"/api/runs/r":            {200, `{"id":"r","agent":"a","status":"succeeded","steps":1,"holes":[]}`},
		"/api/runs/r/steps/0":    {200, `{"status":"ok","tool_calls":[` + strings.Join(calls, ",") + `],"holes":[]}`},
		"/api/runs/r/transcript": {200, `{"batches":[{"input":false,"messages":[{"role":"assistant","content":[{"type":"text","text":"done"}]}]}]}`},
	}
	_, ex := explainOn(t, f, "r")
	if len(ex.Digest) == 0 || len(ex.Digest[0]) > explainLineBytes+80 || !strings.Contains(ex.Digest[0], "more (step {n:…} has them all)") {
		t.Errorf("digest line %d bytes: %.200s…", len(ex.Digest[0]), ex.Digest[0])
	}
	res, _, err := (&mcpReader{s: f}).explainFailure(context.Background(), nil, mcpRunIn{ID: "r", mcpBound: mcpBound{MaxBytes: 1500}})
	if err != nil {
		t.Fatal(err)
	}
	body := texts(t, res)[1]
	if !strings.Contains(body, `"last_assistant_text":"done"`) || !strings.Contains(body, `"next":[`) {
		t.Errorf("the cut took next or the text before the digest: %.400s", body)
	}
}

// TestMCPExplainCaps: 30 steps and 15 children, each child with three
// steps and a tool error. The walk reads at most explainFetchCap
// (transcript included), walks 25 steps, lists 12 children, and says
// every cap at the digest's end: the steps not walked, the children
// not listed by id, the children the budget left unread by id.
func TestMCPExplainCaps(t *testing.T) {
	f := fakeStudio{"/api/runs/r/transcript": {200, `{"batches":[]}`}}
	var children, calls []string
	for i := range 15 {
		id := fmt.Sprintf("r/0/c%02d", i)
		children = append(children, fmt.Sprintf(`{"id":%q,"parent_call_id":"c%02d","agent":"w","status":"succeeded","tool_errors":1,"steps":3}`, id, i))
		calls = append(calls, fmt.Sprintf(`{"call_id":"c%02d","name":"work","child_run_id":%q,"result":{"content":"ok"}}`, i, id))
		esc := strings.ReplaceAll(id, "/", "%2F")
		f["/api/runs/"+esc] = struct {
			status int
			body   string
		}{200, fmt.Sprintf(`{"id":%q,"agent":"w","status":"succeeded","steps":3,"holes":[]}`, id)}
		for n := range 3 {
			f[fmt.Sprintf("/api/runs/%s/steps/%d", esc, n)] = struct {
				status int
				body   string
			}{200, `{"status":"ok","tool_calls":[{"call_id":"call_1","name":"lookup","result":{"content":"nope","is_error":true}}],"holes":[]}`}
		}
	}
	f["/api/runs/r"] = struct {
		status int
		body   string
	}{200, `{"id":"r","agent":"lead","status":"succeeded","steps":30,"holes":[],"children":[` + strings.Join(children, ",") + `]}`}
	f["/api/runs/r/steps/0"] = struct {
		status int
		body   string
	}{200, `{"status":"ok","tool_calls":[` + strings.Join(calls, ",") + `],"holes":[]}`}
	for n := 1; n < 30; n++ {
		f[fmt.Sprintf("/api/runs/r/steps/%d", n)] = struct {
			status int
			body   string
		}{200, `{"status":"ok","holes":[]}`}
	}
	reads := 0
	res, _, err := (&mcpReader{s: countingStudio{f, &reads}}).explainFailure(context.Background(), nil, mcpRunIn{ID: "r"})
	if err != nil {
		t.Fatal(err)
	}
	var ex explanation
	if err := json.Unmarshal([]byte(texts(t, res)[1]), &ex); err != nil {
		t.Fatal(err)
	}
	if reads > explainFetchCap {
		t.Errorf("%d reads, at most %d", reads, explainFetchCap)
	}
	if ex.LastAssistantNote == "" && ex.TranscriptError == "" {
		t.Error("the transcript was not read: its read is budgeted")
	}
	d := ex.Digest
	steps := 0
	for _, l := range d {
		if strings.HasPrefix(l, "step ") {
			steps++
		}
	}
	tail := strings.Join(d[len(d)-3:], "\n")
	if steps != explainStepCap || !strings.Contains(tail, "run r: steps 25–30 not walked") ||
		!strings.Contains(tail, "3 more children not listed (at most 12): r/0/c12, r/0/c13, r/0/c14") ||
		!strings.Contains(tail, "the walk stopped at this answer's 60 reads") {
		t.Errorf("%d step lines; digest tail:\n%s", steps, tail)
	}
	if !strings.Contains(strings.Join(d, "\n"), `not walked (the read budget is spent): explain_failure {id:"r/0/c11"}`) {
		t.Errorf("a child the budget left unread is not named:\n%s", strings.Join(d, "\n"))
	}
}
