package core_test

// The run cards' facts (U25; docs/record-contract.md §3, §4): what the
// loop and the model middleware record so a step and a tool card can
// tell the whole truth — an attempt's wait and HTTP status, a call's
// park source, deadline and queue wait, a parked Subagent child's
// calls, an Output run's submission, the parts a PrepareStep changed
// and the adapter's thinking wire. Reporting only: every run here sees
// the transcript it would see without a tracer or a logger.

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
)

// statusError carries an HTTP status the way the vendor SDKs' errors
// do (the exported StatusCode field mw.HTTPStatus reads).
type statusError struct{ StatusCode int }

func (e statusError) Error() string { return "503 Service Unavailable: overloaded" }

// spansOf returns the recorded spans with the given name, flattened.
func spansOf(tp *recProvider, name string) []map[string]string {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	var out []map[string]string
	for _, s := range tp.spans {
		if s.name == name || (name == "execute_tool" && len(s.name) > 13 && s.name[:13] == "execute_tool ") {
			out = append(out, s.attrsMap())
		}
	}
	return out
}

// TestAttemptWaitAndStatus: mw.Retry reports each try's HTTP status
// (mw.HTTPStatus on its error) and, from the second try on, the backoff
// it slept before it — weft.attempt.wait_ms, rounded up, never 0; a
// first try carries no wait.
func TestAttemptWaitAndStatus(t *testing.T) {
	tp := newRecProvider()
	m := wefttest.Script(wefttest.Fail(statusError{503}), wefttest.Say("ok"))
	agt := core.New(m, core.TracerProvider(tp),
		core.WrapModel(mw.Retry(mw.BaseDelay(time.Millisecond), mw.Classifier(func(error) bool { return true }))))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	att := spansOf(tp, "attempt")
	if len(att) != 2 {
		t.Fatalf("attempt spans = %d, want 2", len(att))
	}
	if att[0]["http.response.status_code"] != "503" || att[0]["weft.attempt.wait_ms"] != "" {
		t.Errorf("attempt 1 = status %q wait %q, want 503 and no wait", att[0]["http.response.status_code"], att[0]["weft.attempt.wait_ms"])
	}
	if att[1]["weft.attempt.wait_ms"] == "" || att[1]["weft.attempt.wait_ms"] == "0" || att[1]["http.response.status_code"] != "" {
		t.Errorf("attempt 2 = wait %q status %q, want a wait and no status", att[1]["weft.attempt.wait_ms"], att[1]["http.response.status_code"])
	}
}

// TestRetryWaitReachesFallback: under WrapModel(Retry, Fallback) the
// Fallback reports the attempts; the backoff Retry slept rides to the
// primary's next try, never to the backup's.
func TestRetryWaitReachesFallback(t *testing.T) {
	tp := newRecProvider()
	primary := wefttest.Script(wefttest.Fail(statusError{503}), wefttest.Fail(statusError{503}))
	backup := wefttest.Script(wefttest.Fail(statusError{503}), wefttest.Say("from backup"))
	agt := core.New(primary, core.TracerProvider(tp), core.WrapModel(
		mw.Retry(mw.MaxRetries(1), mw.BaseDelay(time.Millisecond), mw.Classifier(func(error) bool { return true })),
		mw.Fallback(backup)))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	var waited []bool
	for _, a := range spansOf(tp, "attempt") {
		waited = append(waited, a["weft.attempt.wait_ms"] != "")
	}
	// try 1: primary (no wait), backup (no wait); try 2: primary (the
	// backoff), backup answering (no wait).
	if !slices.Equal(waited, []bool{false, false, true, false}) {
		t.Errorf("attempts waited = %v, want [false false true false]", waited)
	}
}

// TestNestedRetryKeepsOuterWait: under Retry(Fallback(Retry(primary)))
// the inner Retry's first try is the request the outer Retry's backoff
// preceded — it keeps that wait instead of clearing it — and the
// backup, sent at once, carries none.
func TestNestedRetryKeepsOuterWait(t *testing.T) {
	tp := newRecProvider()
	all := mw.Classifier(func(error) bool { return true })
	primary := wefttest.Script(wefttest.Fail(statusError{503}), wefttest.Say("ok"))
	backup := wefttest.Script(wefttest.Fail(statusError{503}), wefttest.Fail(statusError{503}))
	agt := core.New(primary, core.TracerProvider(tp), core.WrapModel(
		mw.Retry(mw.MaxRetries(1), mw.BaseDelay(time.Millisecond), all),
		mw.Fallback(backup),
		mw.Retry(mw.MaxRetries(0), all)))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	var waited []bool
	for _, a := range spansOf(tp, "attempt") {
		waited = append(waited, a["weft.attempt.wait_ms"] != "")
	}
	// try 1: primary, backup (no wait); try 2: primary answering after
	// the outer backoff.
	if !slices.Equal(waited, []bool{false, false, true}) {
		t.Errorf("attempts waited = %v, want [false false true]", waited)
	}
}

// TestToolSpanFacts pins the execute_tool span's U25 facts: the
// deadline in force, the park source of each way a call parks, and a
// parked Subagent child's calls.
func TestToolSpanFacts(t *testing.T) {
	ctx := context.Background()
	noop := func(name string, opts ...core.ToolOption) *core.ToolDef {
		return core.Tool(name, "Do it.", func(context.Context, struct{}) (string, error) { return "done", nil }, opts...)
	}
	call := func(name string) core.Model {
		return wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: name, ID: "c1"}), wefttest.Say("ok"))
	}
	source := func(tp *recProvider) string {
		for _, s := range spansOf(tp, "execute_tool") {
			if s["weft.tool.pending"] == "true" {
				return s["weft.tool.pending.source"]
			}
		}
		return ""
	}
	gate := func(next core.ToolCaller) core.ToolCaller {
		return func(ctx context.Context, c core.ToolCallPart) (string, error) {
			if cc, _ := core.CallFromContext(ctx); !cc.Approved {
				return "", core.ErrApprovalRequired
			}
			return next(ctx, c)
		}
	}
	for _, c := range []struct {
		name string
		opts []core.Option
		run  []core.RunOption
		want string
	}{
		{"approval", []core.Option{noop("t", core.RequireApproval())}, nil, "approval"},
		{"park_on", []core.Option{noop("t")}, []core.RunOption{core.ParkOn("t")}, "park_on"},
		{"park_source", []core.Option{noop("t"), core.ParkSource(func(context.Context) []string { return []string{"t"} })}, nil, "park_source"},
		{"park_on wins over a source", []core.Option{noop("t"), core.ParkSource(func(context.Context) []string { return []string{"t"} })}, []core.RunOption{core.ParkOn("t")}, "park_on"},
		{"park_all_except", []core.Option{noop("t")}, []core.RunOption{core.ParkAllExcept()}, "park_all_except"},
		{"middleware", []core.Option{noop("t"), core.WrapTools(gate)}, nil, "middleware"},
	} {
		tp := newRecProvider()
		agt := core.New(call("t"), append([]core.Option{core.TracerProvider(tp)}, c.opts...)...)
		res, err := agt.Generate(ctx, append([]core.RunOption{core.Prompt("go")}, c.run...)...)
		if err != nil || len(res.Pending) != 1 {
			t.Fatalf("%s: pending %d err %v, want one parked call", c.name, len(res.Pending), err)
		}
		if got := source(tp); got != c.want {
			t.Errorf("%s: weft.tool.pending.source = %q, want %q", c.name, got, c.want)
		}
	}

	// The deadline: the tool's own Timeout, else the agent's; none
	// without either.
	tp := newRecProvider()
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "own", ID: "c1"}),
		wefttest.ToolCalls(wefttest.Call{Name: "inherits", ID: "c2"}),
		wefttest.ToolCalls(wefttest.Call{Name: "tiny", ID: "c3"}),
		wefttest.Say("ok"),
	), core.TracerProvider(tp), core.Timeout(2*time.Second),
		noop("own", core.Timeout(250*time.Millisecond)), noop("inherits"),
		noop("tiny", core.Timeout(500*time.Microsecond)))
	if _, err := agt.Generate(ctx, core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	var limits []string
	for _, s := range spansOf(tp, "execute_tool") {
		limits = append(limits, s["weft.tool.timeout_ms"])
	}
	// A sub-millisecond deadline rounds up: a limit is never recorded
	// as none.
	if !slices.Equal(limits, []string{"250", "2000", "1"}) {
		t.Errorf("timeout_ms = %q, want [250 2000 1]", limits)
	}

	// A parked Subagent child: SUBAGENT_PENDING names its parked calls.
	tp = newRecProvider()
	child := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "k1"})),
		noop("refund", core.RequireApproval()))
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "delegate", ID: "c1", Args: `{"prompt":"refund it"}`}),
		wefttest.Say("waiting"),
	), core.TracerProvider(tp), core.Subagent("delegate", "Delegate.", child))
	if _, err := parent.Generate(ctx, core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	var pend []string
	for _, s := range spansOf(tp, "execute_tool") {
		if s["error.type"] == core.CodeSubagentPending {
			pend = append(pend, s["weft.subagent.pending"])
		}
	}
	if !slices.Equal(pend, []string{"refund"}) {
		t.Errorf("weft.subagent.pending = %q, want [refund]", pend)
	}
}

// TestToolQueueWait: under Parallelism(1) the second call of a batch
// waits for the first's slot — weft.tool.queue_ms on its span only.
func TestToolQueueWait(t *testing.T) {
	tp := newRecProvider()
	slow := core.Tool("slow", "Sleep.", func(context.Context, struct{}) (string, error) {
		time.Sleep(5 * time.Millisecond)
		return "done", nil
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "slow", ID: "a"}, wefttest.Call{Name: "slow", ID: "b"}),
		wefttest.Say("ok"),
	), core.TracerProvider(tp), core.Parallelism(1), slow)
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	queued := map[string]string{}
	for _, s := range spansOf(tp, "execute_tool") {
		queued[s["gen_ai.tool.call.id"]] = s["weft.tool.queue_ms"]
	}
	if queued["a"] != "" || queued["b"] == "" || queued["b"] == "0" {
		t.Errorf("queue_ms = %v, want none on a and a wait on b", queued)
	}

	// A barrier call waits only when something is in flight: s1 first
	// and s2 after another barrier start at once; s3 waits for p.
	tp = newRecProvider()
	seq := core.Tool("seq", "Alone.", func(context.Context, struct{}) (string, error) { return "done", nil }, core.Sequential())
	agt = core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "seq", ID: "s1"}, wefttest.Call{Name: "seq", ID: "s2"},
			wefttest.Call{Name: "slow", ID: "p"}, wefttest.Call{Name: "seq", ID: "s3"}),
		wefttest.Say("ok"),
	), core.TracerProvider(tp), slow, seq)
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	queued = map[string]string{}
	for _, s := range spansOf(tp, "execute_tool") {
		queued[s["gen_ai.tool.call.id"]] = s["weft.tool.queue_ms"]
	}
	if queued["s1"] != "" || queued["s2"] != "" || queued["p"] != "" || queued["s3"] == "" {
		t.Errorf("barrier queue_ms = %v, want a wait on s3 only", queued)
	}
}

// TestOutputSubmittedAttr: an Output agent's run says whether it ended
// with a valid submission; a run of an agent without Output says
// nothing; the tools record marks submit_output as the output.
func TestOutputSubmittedAttr(t *testing.T) {
	type verdict struct {
		Label string `json:"label"`
	}
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		model core.Model
		want  string
	}{
		{"submitted", wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "submit_output", Args: `{"label":"pass"}`})), "true"},
		{"text only", wefttest.Script(wefttest.Say("it passes")), "false"},
	} {
		tp, lp := newRecProvider(), newRecLogProvider()
		agt := core.New(c.model, core.TracerProvider(tp), core.LoggerProvider(lp), core.Output[verdict]())
		_, _, err := core.GenerateAs[verdict](ctx, agt, core.Prompt("grade"))
		if c.want == "false" && !errors.Is(err, core.ErrNoOutput) {
			t.Fatalf("%s: err = %v, want ErrNoOutput", c.name, err)
		}
		if got := tp.find(t, "invoke_agent").attrsMap()["weft.output.submitted"]; got != c.want {
			t.Errorf("%s: weft.output.submitted = %q, want %q", c.name, got, c.want)
		}
		tools := lp.ofKind(t, "tools")
		if len(tools) != 1 {
			t.Fatalf("%s: tools records = %d", c.name, len(tools))
		}
		var body struct {
			Tools []struct {
				Name   string `json:"name"`
				Output bool   `json:"output"`
			} `json:"tools"`
		}
		if err := json.Unmarshal([]byte(tools[0].body), &body); err != nil || len(body.Tools) != 1 ||
			body.Tools[0].Name != "submit_output" || !body.Tools[0].Output {
			t.Errorf("%s: tools record = %s, want submit_output marked output", c.name, tools[0].body)
		}
	}
	tp := newRecProvider()
	agt := core.New(wefttest.Script(wefttest.Say("hi")), core.TracerProvider(tp))
	if _, err := agt.Generate(ctx, core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if _, ok := tp.find(t, "invoke_agent").attrsMap()["weft.output.submitted"]; ok {
		t.Error("a run without Output carries weft.output.submitted")
	}
}

// wireModel is a scripted model that declares its thinking wire form
// (an adapter's optional ThinkingWire method, O16).
type wireModel struct{ core.Model }

func (wireModel) ThinkingWire(t core.ThinkingConfig) string {
	if t.Level == core.ThinkOff {
		return "none"
	}
	return "reasoning_effort"
}

// TestRequestRecordPreparedAndWire: the request record names the parts
// the PrepareStep chain changed (O9) — none when it changed nothing,
// an in-place message edit counted — and the thinking wire the model
// chain declares (O16), found through middleware by Unwrap.
func TestRequestRecordPreparedAndWire(t *testing.T) {
	type reqBody struct {
		Step     int      `json:"step"`
		Prepared []string `json:"prepared"`
		Thinking *struct {
			Level string `json:"level"`
			Wire  string `json:"wire"`
		} `json:"thinking"`
	}
	lp := newRecLogProvider()
	cold := 0.0
	agt := core.New(wireModel{wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "t", ID: "c1"}),
		wefttest.ToolCalls(wefttest.Call{Name: "t", ID: "c2"}),
		wefttest.Say("done"),
	)}, core.LoggerProvider(lp), core.Instructions("Be brief."),
		core.Thinking(core.ThinkingConfig{Level: core.ThinkLow}),
		core.WrapModel(mw.Retry()),
		core.Tool("t", "Do it.", func(context.Context, struct{}) (string, error) { return "done", nil }),
		core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
			switch step {
			case 0:
				req.System += " Step 0."
				req.ToolChoice = core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "t"}
				req.Params.Temperature = &cold
			case 1:
				req.Messages[0] = core.User("rewritten in place")
				req.Thinking = core.ThinkingConfig{Level: core.ThinkOff}
			}
			return req, nil
		}))
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	var got []reqBody
	for _, r := range lp.ofKind(t, "request") {
		var b reqBody
		if err := json.Unmarshal([]byte(r.body), &b); err != nil {
			t.Fatal(err)
		}
		got = append(got, b)
	}
	if len(got) != 3 {
		t.Fatalf("request records = %d, want 3", len(got))
	}
	want := [][]string{{"system", "tool_choice", "params"}, {"messages", "thinking"}, nil}
	wires := []string{"reasoning_effort", "none", "reasoning_effort"}
	for i, b := range got {
		if !slices.Equal(b.Prepared, want[i]) {
			t.Errorf("step %d prepared = %q, want %q", b.Step, b.Prepared, want[i])
		}
		if b.Thinking == nil || b.Thinking.Wire != wires[i] {
			t.Errorf("step %d thinking = %+v, want wire %q", b.Step, b.Thinking, wires[i])
		}
	}
	// No PrepareStep: no prepared; a model without the method: no wire.
	lp = newRecLogProvider()
	plain := core.New(wefttest.Script(wefttest.Say("hi")), core.LoggerProvider(lp),
		core.Thinking(core.ThinkingConfig{Level: core.ThinkHigh}))
	if _, err := plain.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	var b reqBody
	if err := json.Unmarshal([]byte(lp.ofKind(t, "request")[0].body), &b); err != nil {
		t.Fatal(err)
	}
	if b.Prepared != nil || b.Thinking == nil || b.Thinking.Wire != "" {
		t.Errorf("plain request = %+v, want no prepared and no wire", b)
	}
}

// TestOverrideParamsFlat: weft.override.params is the set knobs by the
// request record's lower-case names (UX-S5b-03), and a named tool
// choice with no name never reads "tool:".
func TestOverrideParamsFlat(t *testing.T) {
	tp := newRecProvider()
	agt := core.New(wefttest.Script(wefttest.Say("hi")), core.TracerProvider(tp))
	temp, seed := 0.4, int64(7)
	if _, err := agt.Generate(context.Background(), core.Prompt("x"),
		core.Params(core.RequestParams{Temperature: &temp, Seed: &seed, Stop: []string{"END"}})); err != nil {
		t.Fatal(err)
	}
	if got := tp.find(t, "invoke_agent").attrsMap()["weft.override.params"]; got != `{"temperature":0.4,"stop":["END"],"seed":7}` {
		t.Errorf("weft.override.params = %s", got)
	}
	plainHash := tp.find(t, "invoke_agent").attrsMap()["weft.override.hash"]
	// Params replaces the agent's struct whole: a knob the agent set
	// that the override leaves unset reads null, so a one-knob override
	// never reads like a merge — and the fingerprint is the override's
	// alone, equal to the same override over an agent with no defaults.
	tp = newRecProvider()
	topP, mt := 0.9, 64
	agt = core.New(wefttest.Script(wefttest.Say("hi")), core.TracerProvider(tp),
		core.Params(core.RequestParams{TopP: &topP, MaxTokens: &mt}))
	if _, err := agt.Generate(context.Background(), core.Prompt("x"),
		core.Params(core.RequestParams{Temperature: &temp, Seed: &seed, Stop: []string{"END"}})); err != nil {
		t.Fatal(err)
	}
	root := tp.find(t, "invoke_agent").attrsMap()
	if got := root["weft.override.params"]; got != `{"temperature":0.4,"top_p":null,"max_tokens":null,"stop":["END"],"seed":7}` {
		t.Errorf("weft.override.params over agent defaults = %s", got)
	}
	if root["weft.override.hash"] != plainHash {
		t.Errorf("weft.override.hash = %s over agent defaults, %s without: want equal", root["weft.override.hash"], plainHash)
	}
	tp = newRecProvider()
	agt = core.New(wefttest.Script(wefttest.Say("hi")), core.TracerProvider(tp))
	_, _ = agt.Generate(context.Background(), core.Prompt("x"), core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceNamed}))
	if got := tp.find(t, "invoke_agent").attrsMap()["weft.override.tool_choice"]; got != "tool" {
		t.Errorf("weft.override.tool_choice = %q, want tool", got)
	}
}

// countingWire counts how often the loop asks for its thinking wire.
type countingWire struct {
	core.Model
	n *int
}

func (m countingWire) ThinkingWire(core.ThinkingConfig) string { *m.n++; return "effort" }

// TestRequestFactsOffComputeNothing: with no destination wanting
// request records, the request record's derived facts — prepared[]
// (the PrepareStep comparison, which encodes cloned schemas) and
// thinking.wire — are never computed (ADR 0016: observability off
// costs nothing); with the request kind on they are, once per step.
func TestRequestFactsOffComputeNothing(t *testing.T) {
	for _, on := range []bool{false, true} {
		lp := newRecLogProvider()
		lp.enabled = func(n string) bool { return on && n == "weft.request" }
		n := 0
		agt := core.New(countingWire{wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "t", ID: "c1"}), wefttest.Say("done"),
		), &n}, core.LoggerProvider(lp),
			core.Thinking(core.ThinkingConfig{Level: core.ThinkLow}),
			core.Tool("t", "Do it.", func(context.Context, struct{ X int }) (string, error) { return "ok", nil }),
			core.PrepareStep(func(_ context.Context, _ int, req core.ModelRequest) (core.ModelRequest, error) {
				req.System += " More."
				return req, nil
			}))
		if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
			t.Fatal(err)
		}
		if want := map[bool]int{false: 0, true: 2}[on]; n != want {
			t.Errorf("request records on=%v: ThinkingWire asked %d times, want %d", on, n, want)
		}
	}
}

// codeError carries an HTTP status the way the vendor SDKs' errors do,
// with its own code.
type codeError struct{ StatusCode int }

func (e codeError) Error() string { return "provider said no" }

// TestAttemptErrorTypeClassified (UX-S2-02): a failed attempt's
// error.type is classified by what the reporter knows — the provider's
// HTTP status, weft's own sentinels — a closed set finer than the
// run's model_error; and the last failed attempt of an exhausted
// mw.Retry carries weft.attempt.giveup_reason.
func TestAttemptErrorTypeClassified(t *testing.T) {
	all := mw.Classifier(func(error) bool { return true })
	for _, c := range []struct {
		err  error
		want string
	}{
		{codeError{429}, "rate_limit"},
		{codeError{503}, "overloaded"},
		{codeError{529}, "overloaded"},
		{codeError{504}, "timeout"},
		{codeError{401}, "auth"},
		{codeError{500}, "server_error"},
		{codeError{400}, "model_error"},
		{core.ErrStreamIdle, "stream_idle"},
		{core.ErrContextOverflow, "context_overflow"},
		{errors.New("no status"), "model_error"},
	} {
		tp := newRecProvider()
		m := wefttest.Script(wefttest.Fail(c.err), wefttest.Fail(c.err))
		agt := core.New(m, core.TracerProvider(tp), core.WrapModel(mw.Retry(mw.MaxRetries(1), mw.BaseDelay(0), all)))
		if _, err := agt.Generate(context.Background(), core.Prompt("x")); err == nil {
			t.Fatalf("%v: the run succeeded", c.err)
		}
		att := spansOf(tp, "attempt")
		if len(att) != 2 {
			t.Fatalf("%v: attempt spans = %d, want 2", c.err, len(att))
		}
		for i, a := range att {
			if a["error.type"] != c.want {
				t.Errorf("%v: attempt %d error.type = %q, want %q", c.err, i+1, a["error.type"], c.want)
			}
		}
		if att[0]["weft.attempt.giveup_reason"] != "" || att[1]["weft.attempt.giveup_reason"] != "budget" {
			t.Errorf("%v: giveup = %q, %q; want none, then budget", c.err, att[0]["weft.attempt.giveup_reason"], att[1]["weft.attempt.giveup_reason"])
		}
	}
	// The run's own error.type keeps the run's closed table.
	tp := newRecProvider()
	agt := core.New(wefttest.Script(wefttest.Fail(codeError{429})), core.TracerProvider(tp))
	_, _ = agt.Generate(context.Background(), core.Prompt("x"))
	runs := spansOf(tp, "invoke_agent")
	if len(runs) != 1 {
		t.Fatalf("invoke_agent spans = %d, want 1", len(runs))
	}
	for _, s := range runs {
		if s["error.type"] != "model_error" {
			t.Errorf("run error.type = %q, want model_error", s["error.type"])
		}
	}
}

// TestRequestRecordParallelism (UX-S2-07): the request record carries
// the step's Parallelism dial — the agent's, the run's lower one, 1
// under Sequential() — so a reader can say "parallelism N" beside the
// lanes the spans draw.
func TestRequestRecordParallelism(t *testing.T) {
	for _, c := range []struct {
		name string
		opts []core.Option
		run  []core.RunOption
		want int
	}{
		{"agent", []core.Option{core.Parallelism(3)}, nil, 3},
		{"run lowers it", []core.Option{core.Parallelism(3)}, []core.RunOption{core.Parallelism(2)}, 2},
		{"sequential", []core.Option{core.Sequential()}, nil, 1},
	} {
		lp := newRecLogProvider()
		agt := core.New(wefttest.Script(wefttest.Say("hi")), append(c.opts, core.LoggerProvider(lp))...)
		if _, err := agt.Generate(context.Background(), append(c.run, core.Prompt("x"))...); err != nil {
			t.Fatal(err)
		}
		var b struct {
			Parallelism int `json:"parallelism"`
		}
		if err := json.Unmarshal([]byte(lp.ofKind(t, "request")[0].body), &b); err != nil {
			t.Fatal(err)
		}
		if b.Parallelism != c.want {
			t.Errorf("%s: parallelism = %d, want %d", c.name, b.Parallelism, c.want)
		}
	}
}

// TestOutputTypeRecorded (UX-S1-06): an Output agent's run names its
// typed answer's type on invoke_agent (weft.output.type) and the
// manifest marks submit_output with it (output_type); an agent without
// Output carries neither.
func TestOutputTypeRecorded(t *testing.T) {
	type Invoice struct {
		Number string `json:"number"`
	}
	tp := newRecProvider()
	agt := core.New(wefttest.Script(wefttest.Say("x")), core.Name("billing"), core.TracerProvider(tp), core.Output[*Invoice]())
	_, _ = agt.Generate(context.Background(), core.Prompt("x"))
	if got := tp.find(t, "invoke_agent billing").attrsMap()["weft.output.type"]; got != "core_test.Invoice" {
		t.Errorf("weft.output.type = %q, want core_test.Invoice", got)
	}
	raw, err := core.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Agents []struct {
			Tools []struct {
				Name       string `json:"name"`
				OutputType string `json:"output_type"`
			} `json:"tools"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || len(m.Agents) != 1 || len(m.Agents[0].Tools) != 1 ||
		m.Agents[0].Tools[0].OutputType != "core_test.Invoice" {
		t.Errorf("manifest = %s, want submit_output with output_type core_test.Invoice", raw)
	}
	tp = newRecProvider()
	plain := core.New(wefttest.Script(wefttest.Say("x")), core.TracerProvider(tp))
	_, _ = plain.Generate(context.Background(), core.Prompt("x"))
	if _, ok := tp.find(t, "invoke_agent").attrsMap()["weft.output.type"]; ok {
		t.Error("a run without Output carries weft.output.type")
	}
}

// TestManifestParams (UX-S5b-03): the manifest carries the agent's
// configured sampling knobs by the request record's names — what a run
// option's override is read against — and none for an agent that sets
// none.
func TestManifestParams(t *testing.T) {
	temp, seed := 0.7, int64(7)
	agt := core.New(wefttest.Script(), core.Name("writer"),
		core.Params(core.RequestParams{Temperature: &temp, Seed: &seed}))
	raw, err := core.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Agents []struct {
			Params map[string]any `json:"params"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || len(m.Agents) != 1 {
		t.Fatalf("manifest = %s (%v)", raw, err)
	}
	if got := m.Agents[0].Params; len(got) != 2 || got["temperature"] != 0.7 || got["seed"] != 7.0 {
		t.Errorf("params = %v, want temperature 0.7 and seed 7", got)
	}
	plain, err := core.Manifest(core.New(wefttest.Script(), core.Name("plain")))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), `"params"`) {
		t.Errorf("an agent with no params: manifest %s carries params", plain)
	}
}
