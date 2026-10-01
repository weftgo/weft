package runtime

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// TestRegistryRegistration pins the §10.3 register payload the
// registry builds: process identity fields, the per-runtime budget
// and threads flag, and per agent the manifest, the model allow-list,
// the manifest-derived limits, every tool's real side-effect class
// (its ReplayPolicy; unannotated is "never") and the AllowSideEffects
// set.
func TestRegistryRegistration(t *testing.T) {
	lookup := weft.Tool("lookup_order", "Look up an order.", func(ctx context.Context, in struct{ ID string }) (string, error) {
		return "shipped", nil
	}, weft.Replay(weft.ReplaySafe))
	refund := weft.Tool("refund", "Refund an order.", func(ctx context.Context, in struct{ ID string }) (string, error) {
		return "refunded", nil
	})
	agent := weft.New(
		wefttest.Script(wefttest.Say("ok")),
		weft.Name("acme-support"),
		weft.Instructions("You are Acme's support agent."),
		weft.MaxSteps(7),
		weft.Parallelism(3),
		lookup, refund,
	)
	alt := wefttest.Script(wefttest.Say("alt"))
	reg := newRegistry(&config{
		agents: []*weft.Agent{agent},
		models: map[string]weft.Model{"glm-5.3-flash": alt, "claude-sonnet-5": alt},
		allow:  map[string]bool{"lookup_order": true},
		budget: Budget{MaxTokensPerExperiment: 200_000, MaxRunsPerExperiment: 60},
	})

	payload := reg.registration("rt_test")
	if payload.RuntimeID != "rt_test" {
		t.Errorf("runtime_id = %q", payload.RuntimeID)
	}
	if payload.Host == "" || payload.Pid == 0 {
		t.Errorf("host/pid empty: %q/%d", payload.Host, payload.Pid)
	}
	// The literal is the pin: the release step bumps weftVersion()
	// with the tag and this line must follow it (0.7.0 missed it).
	if payload.WeftVersion != "v0.7.0" {
		t.Errorf("weft_version = %q", payload.WeftVersion)
	}
	if payload.Budget != (budgetWire{MaxTokensPerExperiment: 200_000, MaxRunsPerExperiment: 60}) {
		t.Errorf("budget = %+v", payload.Budget)
	}
	if payload.Threads {
		t.Error("threads = true without runtime.Threads")
	}
	if len(payload.Agents) != 1 {
		t.Fatalf("agents = %d", len(payload.Agents))
	}
	a := payload.Agents[0]
	if a.Name != "acme-support" {
		t.Errorf("agent name = %q", a.Name)
	}
	if want := []string{"claude-sonnet-5", "glm-5.3-flash"}; !reflect.DeepEqual(a.Models, want) {
		t.Errorf("models = %v, want %v (sorted)", a.Models, want)
	}
	if a.Limits != (agentLimits{MaxSteps: 7, Parallelism: 3}) {
		t.Errorf("limits = %+v, want the manifest policy's caps", a.Limits)
	}
	if want := map[string]string{"lookup_order": "safe", "refund": "never"}; !reflect.DeepEqual(a.SideEffects, want) {
		t.Errorf("side_effects = %v, want each tool's ReplayPolicy (safe/never), got %v", a.SideEffects, want)
	}
	if want := []string{"lookup_order"}; !reflect.DeepEqual(a.Allow, want) {
		t.Errorf("allow = %v, want %v", a.Allow, want)
	}
	if !strings.Contains(a.Manifest, `"acme-support"`) || !strings.Contains(a.Manifest, `"lookup_order"`) {
		t.Errorf("manifest = %s, want the agent's weft.Manifest", a.Manifest)
	}
	if !strings.Contains(a.Manifest, `"replay_policy": "safe"`) {
		t.Errorf("manifest = %s, want the safe class recorded", a.Manifest)
	}
}

// TestParkedTools pins the §6 rule 3 set the executor parks on: every
// side-effect tool the command leaves on (ReplayPolicy never, the
// unannotated default) unless the runtime opted in
// (AllowSideEffects) — the on-set minus the opted-in minus the safe
// tools. A tool the command turned off is not offered and cannot fire,
// so it needs no park; a safe tool is not a side effect and never
// parks (WEFT-PLAYGROUND §6 rule 3).
func TestParkedTools(t *testing.T) {
	lookup := weft.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) { return "", nil },
		weft.Replay(weft.ReplaySafe))
	refund := weft.Tool("refund", "Refund.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	escalate := weft.Tool("escalate", "Escalate.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	agent := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("a"), lookup, refund, escalate)
	reg := newRegistry(&config{agents: []*weft.Agent{agent}, allow: map[string]bool{"lookup_order": true}})

	if got := reg.parkedTools("a", nil); !reflect.DeepEqual(got, []string{"escalate", "refund"}) {
		t.Errorf("parked (no narrowing) = %v, want [escalate refund] — the safe tool does not park", got)
	}
	if got := reg.parkedTools("a", []string{"refund"}); !reflect.DeepEqual(got, []string{"refund"}) {
		t.Errorf("parked (refund on) = %v, want [refund] — the enabled non-opted-in tool parks", got)
	}
	if got := reg.parkedTools("a", []string{"refund", "escalate", "lookup_order"}); !reflect.DeepEqual(got, []string{"escalate", "refund"}) {
		t.Errorf("parked (all on) = %v, want [escalate refund] — every non-opted-in side-effect tool the command enabled parks", got)
	}
	if got := reg.parkedTools("a", []string{"lookup_order"}); got != nil {
		t.Errorf("parked (only the safe, opted-in tool on) = %v, want none", got)
	}
}

// TestExecuteParksEnabledNonOptInTool pins §6 rule 3 through the
// executor's own option list: a command that enables a tool the runtime
// has not opted in runs with that tool parked — the scripted model's
// call lands on RunResult.Pending and the tool's handler never
// executes — while the opted-in tool, enabled the same way, runs for
// real (a turned-off tool parks nothing: it is not offered at all).
func TestExecuteParksEnabledNonOptInTool(t *testing.T) {
	var refundRan, lookupRan atomic.Bool
	lookup := weft.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) {
		lookupRan.Store(true)
		return "shipped", nil
	})
	refund := weft.Tool("refund", "Refund.", func(ctx context.Context, in struct{}) (string, error) {
		refundRan.Store(true)
		return "refunded", nil
	})
	agent := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{}`}),
			wefttest.Say("refunded"), // never reached: the run parks at the call
			wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{}`}),
			wefttest.Say("shipped"),
		),
		weft.Name("a"), lookup, refund)
	l := newLink(&config{agents: []*weft.Agent{agent}},
		newRegistry(&config{agents: []*weft.Agent{agent}, allow: map[string]bool{"lookup_order": true}}), "", "")

	run := func(id, input, tool string) *weft.RunResult {
		t.Helper()
		in := input
		cmd := command{CommandID: id, Agent: "a", Engine: "live", Thread: "ephemeral",
			Input: &in, Overrides: overrides{ToolsEnabled: []string{tool}}}
		res, err := agent.Generate(context.Background(), l.runOptions(cmd, "pg_"+id)...)
		if err != nil {
			t.Fatalf("command %s failed: %v", id, err)
		}
		return res
	}

	// The enabled non-opted-in tool parks: the call is Pending.
	if res := run("park", "refund it", "refund"); len(res.Pending) != 1 || res.Pending[0].Name != "refund" {
		t.Errorf("pending = %+v, want the parked refund call", res.Pending)
	}

	// The opted-in tool enabled the same way parks nothing (run 2's
	// model turn is the Say the parked run never reached) and runs for
	// real on the next one (run 3 calls lookup_order).
	if res := run("live", "where is it?", "lookup_order"); len(res.Pending) != 0 {
		t.Errorf("pending = %+v, want none for the opted-in tool", res.Pending)
	}
	if res := run("live", "and again", "lookup_order"); res.Text() == "" || !lookupRan.Load() {
		t.Errorf("the opted-in run: text = %q, lookup ran = %v — want the handler to have run", res.Text(), lookupRan.Load())
	}
	if refundRan.Load() {
		t.Error("the refund handler ran although the runtime did not opt in")
	}
}

// TestValidate pins the runtime's re-validation (§10.4: its copy is
// authoritative after a reconnect): unknown tool or model, a raised
// limit, the 8b-deferred modes, and a budget breach each reject with a
// named reason — never a run.
func TestValidate(t *testing.T) {
	lookup := weft.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	refund := weft.Tool("refund", "Refund.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	agent := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name("a"), weft.MaxSteps(6), weft.Parallelism(4), lookup, refund)
	alt := wefttest.Script(wefttest.Say("alt"))
	l := newLink(&config{
		agents: []*weft.Agent{agent},
		models: map[string]weft.Model{"glm-5.3-flash": alt},
		budget: Budget{MaxRunsPerExperiment: 1},
	}, newRegistry(&config{
		agents: []*weft.Agent{agent},
		models: map[string]weft.Model{"glm-5.3-flash": alt},
		allow:  map[string]bool{"lookup_order": true},
	}), "", "")
	l.cfg.threads = thread.Memory() // fork mode's storage requirement
	agentCmd := func(mutate func(*command)) command {
		cmd := command{CommandID: "cmd_t", Agent: "a", Engine: "live", Thread: "ephemeral"}
		if mutate != nil {
			mutate(&cmd)
		}
		return cmd
	}

	if _, ok := l.validate(agentCmd(nil)); !ok {
		t.Error("a plain command was rejected")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*command)
		wantIn string
	}{
		{"unknown agent", func(c *command) { c.Agent = "nope" }, `unknown agent "nope"`},
		{"unknown tool", func(c *command) { c.Overrides.ToolsEnabled = []string{"nope"} }, `unknown tool "nope"`},
		{"unknown model", func(c *command) { c.Overrides.Model = "gpt-9" }, "not on this runtime's allow-list"},
		{"raised max_steps", func(c *command) { c.Overrides.Options = map[string]float64{"max_steps": 9} }, "raises the agent's cap"},
		{"raised parallelism", func(c *command) { c.Overrides.Options = map[string]float64{"parallelism": 8} }, "raises the agent's cap"},
		{"scripted without a source", func(c *command) { c.Engine = "scripted" }, "source run is required"},
		{"scripted with an instructions override", func(c *command) {
			c.Engine = "scripted"
			c.Source = &sourceSpec{RunID: "s_x", FromStep: 0}
			c.Overrides.Instructions = "new prompt"
		}, "silently replay"},
		{"scripted with a model override", func(c *command) {
			c.Engine = "scripted"
			c.Source = &sourceSpec{RunID: "s_x", FromStep: 0}
			c.Overrides.Model = "glm-5.3-flash"
		}, "silently replay"},
		// fork-without-threads is asserted below on its own link (this
		// one carries storage for the from_step case).
		{"thread fork with from_step", func(c *command) {
			c.Thread = "fork"
			c.Source = &sourceSpec{RunID: "s_x", FromStep: 2}
			c.Input = &[]string{"hi"}[0]
		}, "the ephemeral verb"},
		{"transcript edits without a source", func(c *command) { c.TranscriptEdits = []transcriptEdit{{Step: 1}} }, "need a source run"},
		{"side-effects allow not opted in", func(c *command) {
			c.SideEffects = "allow"
			c.Overrides.ToolsEnabled = []string{"refund"}
		}, "not opted in"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason, ok := l.validate(agentCmd(tc.mutate))
			if ok {
				t.Fatalf("validate accepted; want rejection")
			}
			if !strings.Contains(reason, tc.wantIn) {
				t.Errorf("reason = %q, want it to mention %q", reason, tc.wantIn)
			}
		})
	}

	// The agent's own model name and a registered alternate both pass.
	own := l.reg.ownModel("a")
	if own == "" {
		t.Fatal("agent's own model name missing from its manifest")
	}
	if _, ok := l.validate(agentCmd(func(c *command) { c.Overrides.Model = own })); !ok {
		t.Error("the agent's own model name was refused")
	}
	if _, ok := l.validate(agentCmd(func(c *command) { c.Overrides.Model = "glm-5.3-flash" })); !ok {
		t.Error("a registered alternate was refused")
	}

	// Fork mode without thread storage is rejected before anything
	// else (this link carries none).
	bare := newLink(&config{agents: []*weft.Agent{agent}}, newRegistry(&config{agents: []*weft.Agent{agent}}), "", "")
	if reason, ok := bare.validate(agentCmd(func(c *command) { c.Thread = "fork" })); ok || !strings.Contains(reason, "runtime.Threads") {
		t.Errorf("fork without threads: reason = %q ok = %v, want the Threads requirement", reason, ok)
	}

	// The budget cap: one run per experiment, so the second command of
	// that experiment is rejected with budget_exceeded and the app's
	// own runs are untouched (they never pass through validate).
	l.tally["exp_1"] = &budgetState{runs: 1}
	if reason, ok := l.validate(agentCmd(func(c *command) { c.ExperimentID = "exp_1" })); ok || reason != "budget_exceeded" {
		t.Errorf("breached budget: reason = %q ok = %v, want budget_exceeded", reason, ok)
	}
	if _, ok := l.validate(agentCmd(func(c *command) { c.ExperimentID = "exp_2" })); !ok {
		t.Error("another experiment was refused after a breach elsewhere")
	}
}

// TestBudgetState pins the cap arithmetic: tokens and runs count from
// what a command spent, zero caps never breach, and the breach is
// detected before the next run is allowed.
func TestBudgetState(t *testing.T) {
	var b budgetState
	cap := Budget{MaxTokensPerExperiment: 100, MaxRunsPerExperiment: 2}
	if b.over(cap, 60) {
		t.Error("first run breached")
	}
	b.spend(60)
	if b.over(cap, 40) {
		t.Error("exact fit breached")
	}
	b.spend(40)
	if !b.over(cap, 1) {
		t.Error("token breach not detected")
	}

	var r budgetState
	if r.over(cap, 1) {
		t.Error("runs miscounted before spend")
	}
	r.spend(0)
	r.spend(0)
	if !r.over(cap, 1) {
		t.Error("run-count breach not detected")
	}
	var zero Budget
	if (&budgetState{tokens: 1 << 40, runs: 1 << 20}).over(zero, 1<<40) {
		t.Error("zero caps must never breach")
	}
}

// TestCutAtStep pins §5.1's from_step semantics: keep the transcript
// through step N−1 (tool results included) and run step N fresh; the
// cut is the message index where the Nth assistant message begins.
func TestCutAtStep(t *testing.T) {
	msgs := []weft.Message{
		weft.User("where is my order?"), // input (step 0's input)
		weft.Assistant("checking"),      // step 0's assistant
		{Role: weft.RoleTool, Content: []weft.Part{weft.ToolResultPart{CallID: "c1", Content: "shipped"}}},
		weft.Assistant("it shipped"),     // step 1's assistant
		weft.Assistant("anything else?"), // step 2's assistant
	}
	if got := cutAtStep(msgs, 0); got != 0 {
		t.Errorf("from_step 0 cut = %d, want 0 (re-run the whole turn)", got)
	}
	if got := cutAtStep(msgs, 1); got != 3 {
		t.Errorf("from_step 1 cut = %d, want 3 (step 0 kept: input, its assistant, its tool result)", got)
	}
	if got := cutAtStep(msgs, 2); got != 4 {
		t.Errorf("from_step 2 cut = %d, want 4 (steps 0 and 1 kept, tool result included)", got)
	}
	if got := cutAtStep(msgs, 3); got != 5 {
		t.Errorf("from_step 3 cut = %d, want 5 (the whole transcript kept)", got)
	}
	if got := cutAtStep(msgs, 9); got != len(msgs) {
		t.Errorf("from_step beyond the transcript cut = %d, want len %d", got, len(msgs))
	}
}
