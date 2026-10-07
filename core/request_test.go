package core_test

// The request record tests (ADR 0028): the request, prompt and tools
// record kinds, their hashes, dedupe, attempts, content policy in the
// core, subagent scoping and the instructions hash on RunStart and the
// invoke_agent span. The Logs API provider is records_test.go's.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
)

// emptySHA256 is the instructions hash of a run with no instructions
// (ADR 0028 §4).
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// requestBody decodes a request record's body.
type requestBody struct {
	Step        int    `json:"step"`
	Attempt     int64  `json:"attempt"`
	SystemHash  string `json:"system_hash"`
	MessagesRef struct {
		Index *int64 `json:"index"`
		Count int    `json:"count"`
	} `json:"messages_ref"`
	Tools struct {
		CatalogHash string   `json:"catalog_hash"`
		Names       []string `json:"names"`
	} `json:"tools"`
	ToolChoice *struct {
		Mode string `json:"mode"`
		Name string `json:"name"`
	} `json:"tool_choice"`
	Thinking *struct {
		Level  string `json:"level"`
		Budget int64  `json:"budget"`
	} `json:"thinking"`
	SequentialTools bool `json:"sequential_tools"`
	Params          struct {
		Temperature *float64 `json:"temperature"`
		TopP        *float64 `json:"top_p"`
		MaxTokens   *int     `json:"max_tokens"`
		Stop        []string `json:"stop"`
		Seed        *int64   `json:"seed"`
	} `json:"params"`
	Model struct {
		Provider string `json:"provider"`
		Name     string `json:"name"`
	} `json:"model"`
	Stream bool `json:"stream"`
}

func decodeRequest(t *testing.T, r recLogRecord) requestBody {
	t.Helper()
	var b requestBody
	if err := json.Unmarshal([]byte(r.body), &b); err != nil {
		t.Fatalf("request body %s: %v", r.body, err)
	}
	return b
}

func reqEcho(name string, opts ...core.ToolOption) *core.ToolDef {
	return core.Tool(name, "Echo "+name+".", func(_ context.Context, in struct {
		Msg string `json:"msg"`
	}) (string, error) {
		return "echo: " + in.Msg, nil
	}, opts...)
}

// toolTurns scripts n steps that each call echo, then one final answer.
func toolTurns(n int) []wefttest.Turn {
	turns := make([]wefttest.Turn, 0, n+1)
	for i := range n {
		turns = append(turns, wefttest.ToolCalls(wefttest.Call{ID: fmt.Sprintf("c%d", i), Name: "echo", Args: `{"msg":"x"}`}))
	}
	return append(turns, wefttest.Say("done"))
}

// A 50-step run with an unchanged prompt and catalog records exactly
// one prompt and one tools record, and one request record per step:
// contiguous indices, the step and attempt, the hashes of the prompt
// and tools records, the params, the model and the messages reference
// (the latest messages record, the request's message count).
func TestRequestRecordsDedupeAcrossFiftySteps(t *testing.T) {
	lp := newRecLogProvider()
	temp, seed := 0.2, int64(7)
	agt := core.New(wefttest.Script(toolTurns(49)...), reqEcho("echo"),
		core.Instructions("You are terse."),
		core.MaxSteps(50),
		core.Params(core.RequestParams{Temperature: &temp, Seed: &seed, Stop: []string{"END"}}),
		core.LoggerProvider(lp))
	res, err := agt.Generate(context.Background(), core.RunID("r50"), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 50 {
		t.Fatalf("steps = %d, want 50", len(res.Steps))
	}
	prompts, tools, reqs := lp.ofKind(t, "prompt"), lp.ofKind(t, "tools"), lp.ofKind(t, "request")
	if len(prompts) != 1 || len(tools) != 1 {
		t.Fatalf("prompt/tools records = %d/%d, want 1/1", len(prompts), len(tools))
	}
	if len(reqs) != 50 {
		t.Fatalf("request records = %d, want 50", len(reqs))
	}
	sysHash := sha("You are terse.")
	if got := prompts[0].attr("weft.system.hash"); got != sysHash {
		t.Errorf("prompt weft.system.hash = %q, want %q", got, sysHash)
	}
	if idx, ok := prompts[0].intAttr("weft.prompt.index"); !ok || idx != 0 {
		t.Errorf("prompt index = %d,%v", idx, ok)
	}
	if prompts[0].body != `{"hash":"`+sysHash+`","text":"You are terse."}` {
		t.Errorf("prompt body = %s", prompts[0].body)
	}
	catalog := tools[0].attr("weft.catalog.hash")
	if len(catalog) != 64 {
		t.Fatalf("tools weft.catalog.hash = %q", catalog)
	}
	msgs := lp.ofKind(t, "messages")
	for i, r := range reqs {
		b := decodeRequest(t, r)
		if idx, _ := r.intAttr("weft.request.index"); idx != int64(i) {
			t.Errorf("request %d: index %d", i, idx)
		}
		if step, _ := r.intAttr("weft.step.index"); step != int64(i) || b.Step != i {
			t.Errorf("request %d: step attr %d body %d", i, step, b.Step)
		}
		if at, _ := r.intAttr("weft.attempt.index"); at != 1 || b.Attempt != 1 {
			t.Errorf("request %d: attempt attr %d body %d, want 1", i, at, b.Attempt)
		}
		if r.attr("weft.system.hash") != sysHash || b.SystemHash != sysHash {
			t.Errorf("request %d: system hash %q/%q", i, r.attr("weft.system.hash"), b.SystemHash)
		}
		if r.attr("weft.catalog.hash") != catalog || b.Tools.CatalogHash != catalog {
			t.Errorf("request %d: catalog hash %q/%q", i, r.attr("weft.catalog.hash"), b.Tools.CatalogHash)
		}
		if r.attr("weft.content") != "full" {
			t.Errorf("request %d: weft.content = %q", i, r.attr("weft.content"))
		}
		if !slices.Equal(b.Tools.Names, []string{"echo"}) {
			t.Errorf("request %d: names %v", i, b.Tools.Names)
		}
		if b.Params.Temperature == nil || *b.Params.Temperature != 0.2 || b.Params.Seed == nil || *b.Params.Seed != 7 ||
			!slices.Equal(b.Params.Stop, []string{"END"}) || b.Params.TopP != nil || b.Params.MaxTokens != nil {
			t.Errorf("request %d: params %+v", i, b.Params)
		}
		if b.Model.Provider != "wefttest" || b.Model.Name != "script" || !b.Stream {
			t.Errorf("request %d: model %+v stream %v", i, b.Model, b.Stream)
		}
		// Step i's request carries the input plus two messages per
		// earlier step; the latest messages record before it is 2i.
		if b.MessagesRef.Count != 1+2*i {
			t.Errorf("request %d: messages_ref.count = %d, want %d", i, b.MessagesRef.Count, 1+2*i)
		}
		if b.MessagesRef.Index == nil || *b.MessagesRef.Index != int64(2*i) {
			t.Errorf("request %d: messages_ref.index = %v, want %d", i, b.MessagesRef.Index, 2*i)
		}
	}
	// The reference resolves: growth records up to the index,
	// concatenated, are count messages long.
	last := decodeRequest(t, reqs[49])
	n := 0
	for _, m := range msgs {
		if idx, _ := m.intAttr("weft.messages.index"); idx <= *last.MessagesRef.Index {
			c, _ := m.intAttr("weft.messages.count")
			n += int(c)
		}
	}
	if n != last.MessagesRef.Count {
		t.Errorf("messages up to index %d = %d, want count %d", *last.MessagesRef.Index, n, last.MessagesRef.Count)
	}
}

// The emission order at each step is prompt (when new), tools (when
// new), then request — after step_start, before the step's deltas and
// messages; every record carries the identity chain.
func TestRequestRecordsOrderAndIdentity(t *testing.T) {
	lp := newRecLogProvider()
	agt := core.New(wefttest.Script(wefttest.Say("done")), reqEcho("echo"),
		core.Name("bot"), core.Instructions("Be kind."), core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.RunID("r1"), core.Prompt("hi"),
		core.Metadata(map[string]string{"tenant": "acme"})); err != nil {
		t.Fatal(err)
	}
	lp.mu.Lock()
	var seq []string
	for _, r := range lp.records {
		k := r.attr("weft.record")
		if et := r.attr("weft.event.type"); et != "" {
			k += ":" + et
		}
		seq = append(seq, k)
		if r.attr("weft.run.id") != "r1" || r.attr("gen_ai.agent.name") != "bot" || r.attr("tenant") != "acme" {
			t.Errorf("record %s lacks the identity chain: %v", k, r.attrs)
		}
	}
	lp.mu.Unlock()
	want := []string{"event:run_start", "messages", "event:step_start", "prompt", "tools", "request",
		"delta:text_delta", "messages", "event:step_finish", "event:run_finish"}
	if !slices.Equal(seq, want) {
		t.Errorf("record order:\n got %v\nwant %v", seq, want)
	}
	for _, r := range append(lp.ofKind(t, "request"), append(lp.ofKind(t, "prompt"), lp.ofKind(t, "tools")...)...) {
		want := map[string]string{"request": "weft.request", "prompt": "weft.prompt", "tools": "weft.tools"}[r.attr("weft.record")]
		if r.eventName != want {
			t.Errorf("%s record EventName = %q, want %q", r.attr("weft.record"), r.eventName, want)
		}
	}
}

// A PrepareStep that rewrites the system text from step 3 records a
// second prompt record (index 1) at step 3 and none after; the request
// records name the hash in force at each step.
func TestRequestRecordsPromptChangesAtStep3(t *testing.T) {
	lp := newRecLogProvider()
	agt := core.New(wefttest.Script(toolTurns(5)...), reqEcho("echo"),
		core.Instructions("base"),
		core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
			if step >= 3 {
				req.System = "rewritten"
			}
			return req, nil
		}),
		core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	prompts := lp.ofKind(t, "prompt")
	if len(prompts) != 2 {
		t.Fatalf("prompt records = %d, want 2", len(prompts))
	}
	if prompts[1].attr("weft.system.hash") != sha("rewritten") {
		t.Errorf("second prompt hash = %q", prompts[1].attr("weft.system.hash"))
	}
	if idx, _ := prompts[1].intAttr("weft.prompt.index"); idx != 1 {
		t.Errorf("second prompt index = %d, want 1", idx)
	}
	if n := len(lp.ofKind(t, "tools")); n != 1 {
		t.Errorf("tools records = %d, want 1: PrepareStep cloned the same catalog", n)
	}
	for i, r := range lp.ofKind(t, "request") {
		want := sha("base")
		if i >= 3 {
			want = sha("rewritten")
		}
		if got := decodeRequest(t, r).SystemHash; got != want {
			t.Errorf("request %d system_hash = %q, want %q", i, got, want)
		}
	}
	// The instructions hash is the raw configured text, fixed for the
	// run, whatever PrepareStep did.
	for _, r := range lp.ofKind(t, "event") {
		if r.attr("weft.event.type") == "run_start" && r.attr("weft.instructions.hash") != sha("base") {
			t.Errorf("run_start weft.instructions.hash = %q", r.attr("weft.instructions.hash"))
		}
	}
}

// The system hash is over the composed text: PromptSnippets appended.
func TestRequestRecordsPromptIsComposed(t *testing.T) {
	lp := newRecLogProvider()
	model := wefttest.Script(wefttest.Say("done"))
	agt := core.New(model, reqEcho("echo", core.PromptSnippet("Use echo to echo.")),
		core.Instructions("base"), core.LoggerProvider(lp))
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	composed := model.LastRequest().System
	if composed != "base\n\nUse echo to echo." {
		t.Fatalf("composed system = %q", composed)
	}
	prompts := lp.ofKind(t, "prompt")
	if len(prompts) != 1 || prompts[0].attr("weft.system.hash") != sha(composed) {
		t.Fatalf("prompt records = %+v", prompts)
	}
	var body struct{ Hash, Text string }
	if err := json.Unmarshal([]byte(prompts[0].body), &body); err != nil || body.Text != composed {
		t.Errorf("prompt body %s (%v)", prompts[0].body, err)
	}
	_ = res
}

// A ToolSource that changes the offered set records a second tools
// record when it changes, and none when it changes back to a catalog
// already recorded in the run.
func TestRequestRecordsCatalogChangesWithToolSource(t *testing.T) {
	lp := newRecLogProvider()
	echo, extra := reqEcho("echo"), reqEcho("extra")
	var step atomic.Int64
	src := core.ToolSource(func() []*core.ToolDef {
		switch step.Add(1) - 1 {
		case 2:
			return []*core.ToolDef{echo, extra}
		default:
			return []*core.ToolDef{echo}
		}
	})
	agt := core.New(wefttest.Script(toolTurns(4)...), src, core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	tools := lp.ofKind(t, "tools")
	if len(tools) != 2 {
		t.Fatalf("tools records = %d, want 2", len(tools))
	}
	if idx, _ := tools[1].intAttr("weft.tools.index"); idx != 1 {
		t.Errorf("second tools index = %d", idx)
	}
	reqs := lp.ofKind(t, "request")
	if len(reqs) != 5 {
		t.Fatalf("request records = %d, want 5", len(reqs))
	}
	first, second := tools[0].attr("weft.catalog.hash"), tools[1].attr("weft.catalog.hash")
	for i, want := range []string{first, first, second, first, first} {
		b := decodeRequest(t, reqs[i])
		if b.Tools.CatalogHash != want {
			t.Errorf("request %d catalog = %q, want %q", i, b.Tools.CatalogHash, want)
		}
	}
	if names := decodeRequest(t, reqs[2]).Tools.Names; !slices.Equal(names, []string{"echo", "extra"}) {
		t.Errorf("request 2 names = %v", names)
	}
}

// mw.Retry over a script that fails twice and then succeeds reports
// three attempts: the loop's own request record is attempt 1, and the
// reporter adds attempts 2 and 3 — same step, same hashes and messages.
func TestRequestRecordsPerReportedAttempt(t *testing.T) {
	lp := newRecLogProvider()
	agt := core.New(wefttest.Script(
		wefttest.Fail(core.ErrStreamIdle), wefttest.Fail(core.ErrStreamIdle), wefttest.Say("done"),
	), core.Instructions("x"), core.WrapModel(mw.Retry(mw.BaseDelay(0))), core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	reqs := lp.ofKind(t, "request")
	if len(reqs) != 3 {
		t.Fatalf("request records = %d, want 3 (one per reported attempt)", len(reqs))
	}
	first := decodeRequest(t, reqs[0])
	for i, r := range reqs {
		b := decodeRequest(t, r)
		if at, _ := r.intAttr("weft.attempt.index"); at != int64(i+1) || b.Attempt != int64(i+1) {
			t.Errorf("record %d attempt = %d/%d, want %d", i, at, b.Attempt, i+1)
		}
		if idx, _ := r.intAttr("weft.request.index"); idx != int64(i) {
			t.Errorf("record %d request index = %d", i, idx)
		}
		if b.Step != 0 || b.SystemHash != first.SystemHash || b.MessagesRef.Count != first.MessagesRef.Count ||
			*b.MessagesRef.Index != *first.MessagesRef.Index {
			t.Errorf("record %d differs from attempt 1 beyond the attempt: %+v", i, b)
		}
		if b.Model.Provider != "wefttest" || b.Model.Name != "script" {
			t.Errorf("record %d model = %+v", i, b.Model)
		}
	}
}

// A report that arrives after its model call ended adds no record.
func TestRequestRecordsLateReportAddsNone(t *testing.T) {
	lp := newRecLogProvider()
	var kept core.Reporter
	agt := core.New(wefttest.Script(wefttest.Say("done")), core.LoggerProvider(lp),
		core.WrapModel(reportVia(func(ctx context.Context) { kept = core.ReportFromContext(ctx) })))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	before := len(lp.ofKind(t, "request"))
	kept.Attempt(core.AttemptInfo{Model: "late"})
	kept.Attempt(core.AttemptInfo{Model: "later"})
	if after := len(lp.ofKind(t, "request")); after != before {
		t.Errorf("late reports added %d request records", after-before)
	}
}

// A request that was never sent is never recorded: a PrepareStep error,
// a validation failure, a run cancelled before the call.
func TestRequestRecordsNotForUnsentRequests(t *testing.T) {
	cases := map[string]struct {
		opts []core.Option
		ctx  func() context.Context
	}{
		"prepare error": {opts: []core.Option{core.PrepareStep(func(context.Context, int, core.ModelRequest) (core.ModelRequest, error) {
			return core.ModelRequest{}, errors.New("no")
		})}},
		"invalid tool choice": {opts: []core.Option{core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "missing"})}},
		"cancelled": {ctx: func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			lp := newRecLogProvider()
			ctx := context.Background()
			if c.ctx != nil {
				ctx = c.ctx()
			}
			agt := core.New(wefttest.Script(wefttest.Say("done")), append(c.opts, core.LoggerProvider(lp), core.Instructions("x"))...)
			if _, err := agt.Generate(ctx, core.Prompt("x")); err == nil {
				t.Fatal("run succeeded")
			}
			for _, k := range []string{"request", "prompt", "tools"} {
				if n := len(lp.ofKind(t, k)); n != 0 {
					t.Errorf("%d %s records for an unsent request", n, k)
				}
			}
		})
	}
}

// Content(false): the request record survives with its hashes and
// numbers, params.stop emptied, weft.content=stripped and no messages
// index; prompt and tools records are not emitted at all.
func TestRequestRecordsContentOff(t *testing.T) {
	lp := newRecLogProvider()
	agt := core.New(wefttest.Script(wefttest.Say("done")), reqEcho("echo"),
		core.Instructions("secret prompt"),
		core.Params(core.RequestParams{Stop: []string{"secret stop"}}),
		core.Content(false), core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if n := len(lp.ofKind(t, "prompt")) + len(lp.ofKind(t, "tools")); n != 0 {
		t.Errorf("%d prompt/tools records with Content(false)", n)
	}
	reqs := lp.ofKind(t, "request")
	if len(reqs) != 1 {
		t.Fatalf("request records = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.attr("weft.content") != "stripped" {
		t.Errorf("weft.content = %q, want stripped (ADR 0028 §6/§11)", r.attr("weft.content"))
	}
	if strings.Contains(r.body, "secret") {
		t.Errorf("content-off request carries text: %s", r.body)
	}
	b := decodeRequest(t, r)
	if b.SystemHash != sha("secret prompt") || len(b.Tools.CatalogHash) != 64 || b.MessagesRef.Index != nil || b.MessagesRef.Count != 1 {
		t.Errorf("content-off request lost its numbers: %s", r.body)
	}
	// A logger that wants request records but no content: the same.
	lp2 := newRecLogProvider()
	lp2.enabled = func(name string) bool {
		return name != "weft.messages" && name != "weft.prompt" && name != "weft.tools"
	}
	agt2 := core.New(wefttest.Script(wefttest.Say("done")), reqEcho("echo"), core.Instructions("p"), core.LoggerProvider(lp2))
	if _, err := agt2.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if n := len(lp2.ofKind(t, "prompt")) + len(lp2.ofKind(t, "tools")); n != 0 {
		t.Errorf("%d prompt/tools records with content not wanted", n)
	}
	if reqs := lp2.ofKind(t, "request"); len(reqs) != 1 || reqs[0].attr("weft.content") != "stripped" {
		t.Errorf("request records = %+v", reqs)
	}
}

// Nothing is emitted for a kind its Enabled refuses; a prompt not
// emitted is not marked seen, so it is recorded the first time a
// destination wants it.
func TestRequestRecordsEnabledPerKind(t *testing.T) {
	lp := newRecLogProvider()
	var promptOn atomic.Bool
	lp.enabled = func(name string) bool { return name != "weft.prompt" || promptOn.Load() }
	agt := core.New(wefttest.Script(toolTurns(2)...), reqEcho("echo"), core.Instructions("p"),
		core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
			if step == 1 {
				promptOn.Store(true)
			}
			return req, nil
		}), core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	prompts := lp.ofKind(t, "prompt")
	if len(prompts) != 1 {
		t.Fatalf("prompt records = %d, want 1 (recorded once a destination wanted it)", len(prompts))
	}
	if idx, _ := prompts[0].intAttr("weft.prompt.index"); idx != 0 {
		t.Errorf("prompt index = %d, want 0", idx)
	}
}

// A subagent's child run records its own prompt, tools and request
// under its own run id, numbered from 0; the parent's never describe
// the child's request.
func TestRequestRecordsSubagentOwnRun(t *testing.T) {
	lp := newRecLogProvider()
	child := core.New(wefttest.Script(wefttest.Say("child done")), core.Name("child"),
		core.Instructions("child prompt"), reqEcho("echo"), core.LoggerProvider(lp))
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"x"}`, ID: "c_r"}),
		wefttest.Say("final"),
	), core.Name("parent"), core.Instructions("parent prompt"),
		core.Subagent("research", "Do the research.", child), core.LoggerProvider(lp))
	res, err := parent.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	byRun := map[string]map[string][]recLogRecord{}
	for _, k := range []string{"request", "prompt", "tools"} {
		for _, r := range lp.ofKind(t, k) {
			id := r.attr("weft.run.id")
			if byRun[id] == nil {
				byRun[id] = map[string][]recLogRecord{}
			}
			byRun[id][k] = append(byRun[id][k], r)
		}
	}
	if len(byRun) != 2 {
		t.Fatalf("records under %d run ids, want 2", len(byRun))
	}
	p := byRun[res.ID]
	if len(p["request"]) != 2 || len(p["prompt"]) != 1 || len(p["tools"]) != 1 {
		t.Errorf("parent records: %d request, %d prompt, %d tools", len(p["request"]), len(p["prompt"]), len(p["tools"]))
	}
	if p["prompt"][0].attr("weft.system.hash") != sha("parent prompt") {
		t.Error("parent prompt record names another prompt")
	}
	for id, c := range byRun {
		if id == res.ID {
			continue
		}
		if len(c["request"]) != 1 || len(c["prompt"]) != 1 || len(c["tools"]) != 1 {
			t.Errorf("child records: %d request, %d prompt, %d tools", len(c["request"]), len(c["prompt"]), len(c["tools"]))
		}
		if c["prompt"][0].attr("weft.system.hash") != sha("child prompt") || c["prompt"][0].attr("gen_ai.agent.name") != "child" {
			t.Error("child prompt record is not the child's")
		}
		for _, k := range []string{"request", "prompt", "tools"} {
			if idx, _ := c[k][0].intAttr("weft." + k + ".index"); idx != 0 {
				t.Errorf("child %s index = %d, want its own counter from 0", k, idx)
			}
		}
	}
}

// RunStart carries the raw instructions' hash: the agent's, or the run
// override's; the empty string's when there are none. It round-trips
// the event wire.
func TestRunStartInstructionsHash(t *testing.T) {
	cases := []struct {
		agent []core.Option
		run   []core.RunOption
		want  string
	}{
		{want: emptySHA256},
		{agent: []core.Option{core.Instructions("a")}, want: sha("a")},
		{agent: []core.Option{core.Instructions("a")}, run: []core.RunOption{core.Instructions("b")}, want: sha("b")},
	}
	for i, c := range cases {
		tp := newRecProvider()
		agt := core.New(wefttest.Script(wefttest.Say("done")), append(c.agent, core.TracerProvider(tp))...)
		r := agt.Stream(context.Background(), append(c.run, core.Prompt("x"))...)
		var start core.RunStart
		for ev, err := range r.Events() {
			if err != nil {
				t.Fatal(err)
			}
			if rs, ok := ev.(core.RunStart); ok {
				start = rs
			}
		}
		if start.InstructionsHash != c.want {
			t.Errorf("case %d: InstructionsHash = %q, want %q", i, start.InstructionsHash, c.want)
		}
		if got := tp.find(t, "invoke_agent").attrsMap()["weft.instructions.hash"]; got != c.want {
			t.Errorf("case %d: span weft.instructions.hash = %q, want %q", i, got, c.want)
		}
		b, err := json.Marshal(start)
		if err != nil {
			t.Fatal(err)
		}
		back, err := core.UnmarshalEvent(b)
		if err != nil || back != core.Event(start) {
			t.Errorf("case %d: round trip %s → %#v (%v)", i, b, back, err)
		}
		if !bytes.Contains(b, []byte(`"instructions_hash":"`+c.want+`"`)) {
			t.Errorf("case %d: wire %s", i, b)
		}
		// Not content: StripContent keeps it.
		if core.StripContent(start).(core.RunStart).InstructionsHash != c.want {
			t.Errorf("case %d: StripContent dropped the hash", i)
		}
	}
}

// The catalog hash is ADR 0028 §5's procedure, re-implemented here from
// the text: decode each schema with UseNumber, maps keyed description/
// name/schema sorted by name, SetEscapeHTML(false), no trailing
// newline. The order the tools are offered in, and their policy chips,
// do not change it; numbers keep their source lexeme.
func TestCatalogHashProcedure(t *testing.T) {
	schema, err := core.ParseSchema(json.RawMessage(`{"type":"object","properties":{"n":{"type":"number","maximum":1.0,"description":"<a & b>"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	raw := func() *core.ToolDef {
		return core.RawTool("zeta", "Zeta <tool>.", schema, func(context.Context, json.RawMessage) (string, error) { return "", nil })
	}
	alpha := reqEcho("alpha")
	catalogOf := func(tools []*core.ToolDef, opts ...core.Option) string {
		lp := newRecLogProvider()
		opts = append(opts, core.ToolSource(func() []*core.ToolDef { return tools }), core.LoggerProvider(lp))
		if _, err := core.New(wefttest.Script(wefttest.Say("x")), opts...).Generate(context.Background(), core.Prompt("x")); err != nil {
			t.Fatal(err)
		}
		reqs := lp.ofKind(t, "request")
		if len(reqs) != 1 {
			t.Fatalf("request records = %d", len(reqs))
		}
		return reqs[0].attr("weft.catalog.hash")
	}

	// The procedure from the ADR's text.
	var entries []map[string]any
	for _, td := range []*core.ToolDef{raw(), alpha} {
		b, _ := json.Marshal(td.InputSchema)
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, map[string]any{"description": td.Description, "name": td.Name, "schema": v})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i]["name"].(string) < entries[j]["name"].(string) })
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(entries); err != nil {
		t.Fatal(err)
	}
	canon := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	if !bytes.Contains(canon, []byte(`"maximum":1.0`)) || !bytes.Contains(canon, []byte(`<a & b>`)) {
		t.Fatalf("canonical bytes lost a lexeme or escaped HTML: %s", canon)
	}
	want := sha(string(canon))

	if got := catalogOf([]*core.ToolDef{raw(), alpha}); got != want {
		t.Errorf("catalog hash = %s, want %s", got, want)
	}
	if got := catalogOf([]*core.ToolDef{alpha, raw()}); got != want {
		t.Errorf("offer order changed the hash: %s", got)
	}
	chipped := core.RawTool("zeta", "Zeta <tool>.", schema, func(context.Context, json.RawMessage) (string, error) { return "", nil },
		core.Timeout(time.Second), core.RequireApproval(), core.Replay(core.ReplaySafe), core.MaxResultBytes(9), core.Sequential())
	if got := catalogOf([]*core.ToolDef{chipped, alpha}); got != want {
		t.Errorf("policy chips changed the hash: %s", got)
	}
	if got := catalogOf(nil); got != "" {
		t.Errorf("no tools: catalog hash = %q, want absent", got)
	}
}

// The tools record's body: entries in name order, the schema verbatim,
// and the chips as the run applies them — the tool's own policy, else
// the agent's; approval from RequireApproval or the run's park rule;
// the replay class (unannotated is never); source local or subagent.
func TestToolsRecordChips(t *testing.T) {
	lp := newRecLogProvider()
	child := core.New(wefttest.Script(wefttest.Say("c")))
	agt := core.New(wefttest.Script(wefttest.Say("done")),
		reqEcho("b_plain"),
		reqEcho("a_tuned", core.Timeout(2*time.Second), core.MaxResultBytes(100), core.Replay(core.ReplaySafe), core.Sequential(), core.RequireApproval()),
		reqEcho("c_parked"),
		core.Subagent("d_sub", "Delegate.", child),
		reqEcho("e_mcp", core.Origin("mcp")),
		core.Timeout(5*time.Second), core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.Prompt("x"), core.ParkOn("c_parked")); err != nil {
		t.Fatal(err)
	}
	tools := lp.ofKind(t, "tools")
	if len(tools) != 1 {
		t.Fatalf("tools records = %d", len(tools))
	}
	var body struct {
		Hash  string `json:"hash"`
		Tools []struct {
			Name           string          `json:"name"`
			Description    string          `json:"description"`
			Schema         json.RawMessage `json:"schema"`
			TimeoutMS      int64           `json:"timeout_ms"`
			Approval       bool            `json:"approval"`
			Replay         string          `json:"replay"`
			MaxResultBytes int             `json:"max_result_bytes"`
			Sequential     bool            `json:"sequential"`
			Source         string          `json:"source"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(tools[0].body), &body); err != nil {
		t.Fatal(err)
	}
	if body.Hash != tools[0].attr("weft.catalog.hash") {
		t.Error("body hash differs from weft.catalog.hash")
	}
	got := map[string]string{}
	var names []string
	for _, e := range body.Tools {
		names = append(names, e.Name)
		got[e.Name] = fmt.Sprintf("%d/%v/%s/%d/%v/%s", e.TimeoutMS, e.Approval, e.Replay, e.MaxResultBytes, e.Sequential, e.Source)
	}
	if !slices.Equal(names, []string{"a_tuned", "b_plain", "c_parked", "d_sub", "e_mcp"}) {
		t.Errorf("entries not in name order: %v", names)
	}
	want := map[string]string{
		"a_tuned":  "2000/true/safe/100/true/local",
		"b_plain":  "5000/false/never/65536/false/local",
		"c_parked": "5000/true/never/65536/false/local",
		"d_sub":    "5000/false/never/65536/false/subagent",
		"e_mcp":    "5000/false/never/65536/false/mcp",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s chips = %s, want %s", k, got[k], v)
		}
	}
	if string(body.Tools[1].Schema) != `{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}` {
		t.Errorf("schema not verbatim: %s", body.Tools[1].Schema)
	}
}

// The three record kinds' exact shape — body and attributes — pinned as
// golden files (testdata/records), the contract A1.2's store reads.
func TestRequestRecordGoldenShape(t *testing.T) {
	lp := newRecLogProvider()
	temp, maxTok := 0.5, 256
	agt := core.New(wefttest.Script(wefttest.Say("done")),
		reqEcho("lookup", core.PromptSnippet("Use lookup for orders."), core.Timeout(time.Second)),
		core.Name("support"), core.Instructions("You are a support agent."),
		core.Thinking(core.ThinkingConfig{Level: core.ThinkHigh, Budget: 1024}),
		core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "lookup"}),
		core.Params(core.RequestParams{Temperature: &temp, MaxTokens: &maxTok, Stop: []string{"STOP"}}),
		core.Sequential(),
		core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.RunID("run-golden"), core.Prompt("where is 42?")); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"request", "prompt", "tools"} {
		recs := lp.ofKind(t, kind)
		if len(recs) != 1 {
			t.Fatalf("%s records = %d, want 1", kind, len(recs))
		}
		r := recs[0]
		attrs := map[string]string{}
		for _, kv := range r.attrs {
			attrs[string(kv.Key)] = kv.Value.Type().String() + ":" + kv.Value.String()
		}
		var body any
		if err := json.Unmarshal([]byte(r.body), &body); err != nil {
			t.Fatal(err)
		}
		doc, err := json.MarshalIndent(map[string]any{
			"event_name": r.eventName,
			"severity":   r.severity.String(),
			"attributes": attrs,
			"body":       body,
		}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		wefttest.Golden(t, "testdata/records/"+kind+".golden.json", append(doc, '\n'))
	}
}

// A logger that panics on the request kinds breaks neither the run nor
// its model-visible output: the panic is contained and counted.
func TestRequestRecordsPanicContained(t *testing.T) {
	agt := core.New(wefttest.Script(
		wefttest.Fail(core.ErrStreamIdle), wefttest.Say("done"),
	), reqEcho("echo"), core.Instructions("p"),
		core.WrapModel(mw.Retry(mw.BaseDelay(0))),
		core.LoggerProvider(panicOnKinds{}))
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "done" {
		t.Errorf("text = %q", res.Text())
	}
	if agt.TapPanics() < 2 {
		t.Errorf("TapPanics = %d, want the request records' panics counted", agt.TapPanics())
	}
}

type panicOnKinds struct{ embedded.LoggerProvider }

func (panicOnKinds) Logger(string, ...log.LoggerOption) log.Logger { return panicLogger{} }

type panicLogger struct{ embedded.Logger }

func (panicLogger) Enabled(context.Context, log.EnabledParameters) bool { return true }
func (panicLogger) Emit(_ context.Context, r log.Record) {
	switch r.EventName() {
	case "weft.request", "weft.prompt", "weft.tools":
		panic("broken exporter")
	}
}

// The request record changes nothing the model sees: the requests the
// model receives, the transcript and the events are byte-identical with
// every record kind on, with them off, and with a logger that refuses
// them — through mw.Retry's reported attempts too.
func TestRequestRecordsChangeNothingModelVisible(t *testing.T) {
	run := func(lp log.LoggerProvider, content bool) (reqs, msgs, events []byte) {
		t.Helper()
		model := wefttest.Script(wefttest.Fail(core.ErrStreamIdle),
			wefttest.ToolCalls(wefttest.Call{ID: "c1", Name: "echo", Args: `{"msg":"hi"}`}), wefttest.Say("done"))
		opts := []core.Option{reqEcho("echo", core.PromptSnippet("snip")), core.Instructions("p"),
			core.WrapModel(mw.Retry(mw.BaseDelay(0))), core.Content(content),
			core.PrepareStep(func(_ context.Context, step int, req core.ModelRequest) (core.ModelRequest, error) {
				if step == 1 {
					req.System += " step1"
				}
				return req, nil
			})}
		if lp != nil {
			opts = append(opts, core.LoggerProvider(lp))
		}
		r := core.New(model, opts...).Stream(context.Background(), core.RunID("r"), core.Prompt("hello"))
		var evs []core.Event
		for ev, err := range r.Events() {
			if err != nil {
				t.Fatal(err)
			}
			evs = append(evs, ev)
		}
		res, err := r.Wait()
		if err != nil {
			t.Fatal(err)
		}
		var e error
		if reqs, e = json.Marshal(model.Requests()); e != nil {
			t.Fatal(e)
		}
		if msgs, e = json.Marshal(res.Messages); e != nil {
			t.Fatal(e)
		}
		if events, e = json.Marshal(evs); e != nil {
			t.Fatal(e)
		}
		return reqs, msgs, events
	}
	off := newRecLogProvider()
	off.enabled = func(string) bool { return false }
	br, bm, be := run(nil, false)
	on := newRecLogProvider()
	for name, lp := range map[string]log.LoggerProvider{"records on": on, "records refused": off, "panicking": panicOnKinds{}} {
		for _, content := range []bool{true, false} {
			r, m, e := run(lp, content)
			if !bytes.Equal(r, br) || !bytes.Equal(m, bm) || !bytes.Equal(e, be) {
				t.Errorf("%s (content %v): model-visible output differs", name, content)
			}
		}
	}
	if len(on.ofKind(t, "request")) == 0 {
		t.Fatal("the recording run recorded no request")
	}
}

// ExampleRunStart_instructionsHash: every run names its raw configured
// instructions by hash on RunStart — the prompt itself is content and
// travels only in the prompt record, under the content policy.
func ExampleRunStart_instructionsHash() {
	agt := core.New(wefttest.Script(wefttest.Say("ok")), core.Instructions("You are terse."))
	for ev := range agt.Stream(context.Background(), core.Prompt("hi")).Events() {
		if rs, ok := ev.(core.RunStart); ok {
			fmt.Println(rs.InstructionsHash == sha("You are terse."))
		}
	}
	// Output: true
}

// BenchmarkRequestRecord measures a run's cost with the request records
// off (no SDK: every Enabled answers false — nothing is hashed or
// marshalled per step; one sha256 of the instructions per run) and on
// (every kind recorded).
func BenchmarkRequestRecord(b *testing.B) {
	turns := func() []wefttest.Turn {
		ts := make([]wefttest.Turn, 0, 10)
		for i := range 9 {
			ts = append(ts, wefttest.ToolCalls(wefttest.Call{ID: fmt.Sprintf("c%d", i), Name: "echo", Args: `{"msg":"x"}`}))
		}
		return append(ts, wefttest.Say("done"))
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, c := range []struct {
		name string
		lp   func() log.LoggerProvider
	}{
		{"off", func() log.LoggerProvider {
			lp := newRecLogProvider()
			lp.enabled = func(string) bool { return false }
			return lp
		}},
		{"requests-only", func() log.LoggerProvider {
			lp := newRecLogProvider()
			lp.enabled = func(n string) bool { return n == "weft.request" }
			return lp
		}},
		{"all", func() log.LoggerProvider { return newRecLogProvider() }},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				agt := core.New(wefttest.Script(turns()...), reqEcho("echo"), core.Instructions("You are terse."),
					core.LoggerProvider(c.lp()), core.Logger(quiet))
				if _, err := agt.Generate(context.Background(), core.Prompt("go")); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/10, "ns/step")
		})
	}
}

// resolveRef returns the messages a request's messages_ref names: the
// growth records up to its index, concatenated (no compaction records
// exist before A9).
func resolveRef(t *testing.T, lp *recLogProvider, runID string, index int64) []core.Message {
	t.Helper()
	var out []core.Message
	for _, m := range lp.ofKind(t, "messages") {
		if m.attr("weft.run.id") != runID {
			continue
		}
		if idx, _ := m.intAttr("weft.messages.index"); idx <= index {
			var batch []core.Message
			if err := json.Unmarshal([]byte(m.body), &batch); err != nil {
				t.Fatal(err)
			}
			out = append(out, batch...)
		}
	}
	return out
}

// After a resume that rebuilds a partial tool message (one call ran,
// one parked), the step-0 request's messages_ref resolves to exactly
// its count — the input record stops at the assistant message and the
// rebuilt tool message is the next growth record — and the resolved
// messages are the request's.
func TestRequestRecordsRefResolvesAfterMixedResume(t *testing.T) {
	park := reqEcho("park", core.RequireApproval())
	model := wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{ID: "a", Name: "echo", Args: `{"msg":"1"}`},
			wefttest.Call{ID: "b", Name: "park", Args: `{"msg":"2"}`}),
		wefttest.Say("done"))
	agt := core.New(model, reqEcho("echo"), park)
	res, err := agt.Generate(context.Background(), core.Prompt("go"))
	if err != nil || len(res.Pending) != 1 || len(res.Messages) != 3 {
		t.Fatalf("park: %v, pending %d, messages %d", err, len(res.Pending), len(res.Messages))
	}
	lp := newRecLogProvider()
	agt2 := core.New(model, reqEcho("echo"), park, core.LoggerProvider(lp))
	res2, err := agt2.Generate(context.Background(), core.RunID("resume"), core.Messages(res.Messages...), core.Approve("b"))
	if err != nil {
		t.Fatal(err)
	}
	reqs := lp.ofKind(t, "request")
	if len(reqs) != 1 {
		t.Fatalf("request records = %d", len(reqs))
	}
	b := decodeRequest(t, reqs[0])
	if b.MessagesRef.Index == nil {
		t.Fatal("no messages_ref.index with capture on")
	}
	got := resolveRef(t, lp, "resume", *b.MessagesRef.Index)
	if len(got) != b.MessagesRef.Count {
		t.Fatalf("records ≤ %d hold %d messages, messages_ref.count = %d", *b.MessagesRef.Index, len(got), b.MessagesRef.Count)
	}
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(model.LastRequest().Messages)
	if !bytes.Equal(gb, wb) {
		t.Errorf("ref resolves to\n%s\nthe request carried\n%s", gb, wb)
	}
	// And the whole stream still concatenates to the transcript.
	all := resolveRef(t, lp, "resume", 1<<30)
	ab, _ := json.Marshal(all)
	tb, _ := json.Marshal(res2.Messages)
	if !bytes.Equal(ab, tb) {
		t.Errorf("records rebuild\n%s\ntranscript\n%s", ab, tb)
	}
}

// A steered batch is growth: the next step's request points at it.
func TestRequestRecordsRefPointsAtSteeredBatch(t *testing.T) {
	lp := newRecLogProvider()
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "c1", Name: "echo", Args: `{"msg":"x"}`}),
		wefttest.Say("done")), reqEcho("echo"), core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.RunID("st"), core.Prompt("go"),
		wefttest.NewSteers().At(0, core.User("metric")).Option()); err != nil {
		t.Fatal(err)
	}
	reqs := lp.ofKind(t, "request")
	if len(reqs) != 2 {
		t.Fatalf("request records = %d", len(reqs))
	}
	b := decodeRequest(t, reqs[1])
	if b.MessagesRef.Index == nil || *b.MessagesRef.Index != 3 || b.MessagesRef.Count != 4 {
		t.Fatalf("step 1 messages_ref = %v/%d, want {3, 4}", b.MessagesRef.Index, b.MessagesRef.Count)
	}
	for _, m := range lp.ofKind(t, "messages") {
		if idx, _ := m.intAttr("weft.messages.index"); idx == 3 && !strings.Contains(m.body, "metric") {
			t.Errorf("messages record 3 is not the steered batch: %s", m.body)
		}
	}
	if n := len(resolveRef(t, lp, "st", 3)); n != 4 {
		t.Errorf("ref resolves to %d messages, want 4", n)
	}
}

// Two schemas equal but for their key order hash identically: the
// catalog hash is over decoded values, written with sorted keys.
func TestCatalogHashIgnoresSchemaKeyOrder(t *testing.T) {
	hashOf := func(schema string) string {
		s, err := core.ParseSchema(json.RawMessage(schema))
		if err != nil {
			t.Fatal(err)
		}
		lp := newRecLogProvider()
		tool := core.RawTool("t", "T.", s, func(context.Context, json.RawMessage) (string, error) { return "", nil })
		if _, err := core.New(wefttest.Script(wefttest.Say("x")), tool, core.LoggerProvider(lp)).Generate(context.Background(), core.Prompt("x")); err != nil {
			t.Fatal(err)
		}
		return lp.ofKind(t, "request")[0].attr("weft.catalog.hash")
	}
	a := hashOf(`{"type":"object","properties":{"q":{"type":"string","description":"d"}},"required":["q"]}`)
	b := hashOf(`{"required":["q"],"properties":{"q":{"description":"d","type":"string"}},"type":"object"}`)
	if a == "" || a != b {
		t.Errorf("key order changed the catalog hash: %s vs %s", a, b)
	}
}

// Retry over Fallback: only the layer next to the models reports (the
// ReportsAttempts gate), so a primary failure and a backup success are
// two request records — attempt 2 naming the backup — never doubled.
func TestRequestRecordsRetryOverFallbackNotDoubled(t *testing.T) {
	lp := newRecLogProvider()
	backup := infoModel{Model: wefttest.Script(wefttest.Say("from backup")), info: core.ModelInfo{Provider: "backup-co", Name: "b-1"}}
	agt := core.New(wefttest.Script(wefttest.Fail(core.ErrStreamIdle)),
		core.WrapModel(mw.Retry(mw.BaseDelay(0)), mw.Fallback(backup)), core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	reqs := lp.ofKind(t, "request")
	if len(reqs) != 2 {
		t.Fatalf("request records = %d, want 2 (one per reported provider request)", len(reqs))
	}
	b := decodeRequest(t, reqs[1])
	if b.Attempt != 2 || b.Model.Provider != "backup-co" || b.Model.Name != "b-1" {
		t.Errorf("attempt 2 record = %+v", b)
	}
}

// An attempt that reports only its model keeps the call's provider, and
// one that reports only its provider keeps the call's model name.
func TestRequestRecordsAttemptKeepsUnreportedModelFields(t *testing.T) {
	lp := newRecLogProvider()
	agt := core.New(wefttest.Script(wefttest.Say("done")), core.LoggerProvider(lp),
		core.WrapModel(func(next core.Model) core.Model {
			// The reporting layer names the call's model, as Info would.
			return infoModel{Model: reportVia(func(ctx context.Context) {
				r := core.ReportFromContext(ctx)
				r.Attempt(core.AttemptInfo{})
				r.Attempt(core.AttemptInfo{Model: "other"})
				r.Attempt(core.AttemptInfo{Provider: "elsewhere"})
			})(next), info: core.InfoOf(next)}
		}))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	reqs := lp.ofKind(t, "request")
	if len(reqs) != 3 {
		t.Fatalf("request records = %d, want 3", len(reqs))
	}
	if m := decodeRequest(t, reqs[1]).Model; m.Provider != "wefttest" || m.Name != "other" {
		t.Errorf("model-only attempt = %+v", m)
	}
	if m := decodeRequest(t, reqs[2]).Model; m.Provider != "elsewhere" || m.Name != "script" {
		t.Errorf("provider-only attempt = %+v", m)
	}
}

// A prompt or tools record whose emission panicked is not marked
// recorded: it is emitted again at the next step.
func TestRequestRecordsRetriedAfterEmitPanic(t *testing.T) {
	lp := &flakyFirstProvider{}
	agt := core.New(wefttest.Script(toolTurns(1)...), reqEcho("echo"), core.Instructions("p"), core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if lp.prompts.Load() != 1 || lp.tools.Load() != 1 {
		t.Errorf("prompt/tools records delivered = %d/%d, want 1/1 (retried at step 1)", lp.prompts.Load(), lp.tools.Load())
	}
	if agt.TapPanics() != 2 {
		t.Errorf("TapPanics = %d, want 2", agt.TapPanics())
	}
}

// flakyFirstProvider panics on the first prompt and the first tools
// record, and counts the ones delivered after.
type flakyFirstProvider struct {
	embedded.LoggerProvider
	prompts, tools        atomic.Int64
	promptSeen, toolsSeen atomic.Bool
}

func (p *flakyFirstProvider) Logger(string, ...log.LoggerOption) log.Logger { return flakyLogger{p: p} }

type flakyLogger struct {
	embedded.Logger
	p *flakyFirstProvider
}

func (flakyLogger) Enabled(context.Context, log.EnabledParameters) bool { return true }
func (l flakyLogger) Emit(_ context.Context, r log.Record) {
	switch r.EventName() {
	case "weft.prompt":
		if !l.p.promptSeen.Swap(true) {
			panic("first prompt")
		}
		l.p.prompts.Add(1)
	case "weft.tools":
		if !l.p.toolsSeen.Swap(true) {
			panic("first tools")
		}
		l.p.tools.Add(1)
	}
}

// Four subagents running in parallel each record under their own run
// id, with their own counters and their own tools record.
func TestRequestRecordsParallelSubagents(t *testing.T) {
	lp := newRecLogProvider()
	var opts []core.Option
	var calls []wefttest.Call
	for i := range 4 {
		child := core.New(wefttest.Script(wefttest.Say("child")), core.Name(fmt.Sprintf("child%d", i)),
			reqEcho(fmt.Sprintf("tool%d", i)), core.LoggerProvider(lp))
		name := fmt.Sprintf("sub%d", i)
		opts = append(opts, core.Subagent(name, "Delegate.", child))
		calls = append(calls, wefttest.Call{ID: fmt.Sprintf("c%d", i), Name: name, Args: `{"prompt":"x"}`})
	}
	opts = append(opts, core.Parallelism(4), core.LoggerProvider(lp))
	parent := core.New(wefttest.Script(wefttest.ToolCalls(calls...), wefttest.Say("final")), opts...)
	res, err := parent.Generate(context.Background(), core.Prompt("go"))
	if err != nil {
		t.Fatal(err)
	}
	type counts struct{ req, tools int }
	byRun := map[string]*counts{}
	catalogs := map[string]bool{}
	for _, k := range []string{"request", "tools"} {
		for _, r := range lp.ofKind(t, k) {
			id := r.attr("weft.run.id")
			if byRun[id] == nil {
				byRun[id] = &counts{}
			}
			idx, _ := r.intAttr("weft." + k + ".index")
			if k == "request" {
				if id != res.ID && idx != 0 {
					t.Errorf("child %s request index %d, want its own counter from 0", id, idx)
				}
				byRun[id].req++
			} else {
				if idx != 0 {
					t.Errorf("run %s tools index %d", id, idx)
				}
				byRun[id].tools++
				catalogs[r.attr("weft.catalog.hash")] = true
			}
		}
	}
	if len(byRun) != 5 {
		t.Fatalf("records under %d run ids, want 5", len(byRun))
	}
	for id, c := range byRun {
		want := 1
		if id == res.ID {
			want = 2
		}
		if c.req != want || c.tools != 1 {
			t.Errorf("run %s: %d request, %d tools records", id, c.req, c.tools)
		}
	}
	if len(catalogs) != 5 {
		t.Errorf("%d distinct catalogs, want 5 (each child its own)", len(catalogs))
	}
}

// ExampleOrigin: a tool registered from somewhere other than Go source
// names its origin, which the tools record carries as its source chip.
func ExampleOrigin() {
	lp := newRecLogProvider()
	plugin := core.RawTool("lookup", "Look up an order.", nil,
		func(context.Context, json.RawMessage) (string, error) { return "ok", nil },
		core.Origin("plugin"))
	agt := core.New(wefttest.Script(wefttest.Say("ok")), plugin, core.LoggerProvider(lp))
	if _, err := agt.Generate(context.Background(), core.Prompt("hi")); err != nil {
		return
	}
	lp.mu.Lock()
	defer lp.mu.Unlock()
	for _, r := range lp.records {
		if r.attr("weft.record") == "tools" {
			var body struct {
				Tools []struct{ Name, Source string } `json:"tools"`
			}
			_ = json.Unmarshal([]byte(r.body), &body)
			fmt.Println(body.Tools[0].Name, body.Tools[0].Source)
		}
	}
	// Output: lookup plugin
}

// A resume cancelled while its approved tool runs still records its
// held tail (accepted input, emitted with WithoutCancel): the records
// concatenate to RunError.Result.Messages. A resume cancelled before
// it starts records nothing at all.
func TestRequestRecordsCancelledResumeKeepsHeldTail(t *testing.T) {
	started := make(chan struct{})
	park := core.Tool("park", "Blocks.", func(ctx context.Context, _ struct{}) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}, core.RequireApproval())
	model := wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{ID: "a", Name: "echo", Args: `{"msg":"1"}`},
			wefttest.Call{ID: "b", Name: "park", Args: `{}`}),
		wefttest.Say("done"))
	res, err := core.New(model, reqEcho("echo"), park).Generate(context.Background(), core.Prompt("go"))
	if err != nil || len(res.Pending) != 1 {
		t.Fatalf("park: %v, pending %d", err, len(res.Pending))
	}

	lp := newRecLogProvider()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	_, err = core.New(model, reqEcho("echo"), park, core.LoggerProvider(lp)).
		Generate(ctx, core.RunID("cancelled"), core.Messages(res.Messages...), core.Approve("b"))
	var re *core.RunError
	if !errors.As(err, &re) || !errors.Is(err, context.Canceled) {
		t.Fatalf("resume err = %v, want a cancelled RunError", err)
	}
	got, _ := json.Marshal(resolveRef(t, lp, "cancelled", 1<<30))
	want, _ := json.Marshal(re.Result.Messages)
	if !bytes.Equal(got, want) {
		t.Errorf("records rebuild\n%s\nRunError.Result.Messages\n%s", got, want)
	}

	lp2 := newRecLogProvider()
	dead, kill := context.WithCancel(context.Background())
	kill()
	_, _ = core.New(model, reqEcho("echo"), park, core.LoggerProvider(lp2)).
		Generate(dead, core.RunID("dead"), core.Messages(res.Messages...), core.Approve("b"))
	if n := len(lp2.ofKind(t, "messages")); n != 0 {
		t.Errorf("a resume cancelled before it started recorded %d messages records", n)
	}
}
