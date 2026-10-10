package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/version"
)

// TestRegistryRegistration pins the §10.3 register payload the
// registry builds: process identity fields, the per-runtime budget
// and threads flag, and per agent the manifest, the model allow-list,
// the manifest-derived limits, every tool's real side-effect class
// (its ReplayPolicy; unannotated is "never") and the AllowSideEffects
// set.
func TestRegistryRegistration(t *testing.T) {
	lookup := core.Tool("lookup_order", "Look up an order.", func(ctx context.Context, in struct{ ID string }) (string, error) {
		return "shipped", nil
	}, core.Replay(core.ReplaySafe))
	refund := core.Tool("refund", "Refund an order.", func(ctx context.Context, in struct{ ID string }) (string, error) {
		return "refunded", nil
	})
	agent := core.New(
		wefttest.Script(wefttest.Say("ok")),
		core.Name("acme-support"),
		core.Instructions("You are Acme's support agent."),
		core.MaxSteps(7),
		core.Parallelism(3),
		lookup, refund,
	)
	alt := wefttest.Script(wefttest.Say("alt"))
	reg := newRegistry(&config{
		agents: []*core.Agent{agent},
		models: map[string]core.Model{"glm-5.3-flash": alt, "claude-sonnet-5": alt},
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
	// The one source's tag (TestWeftVersionMatchesRoot pins it to the
	// core's, so a missed release bump fails there).
	if payload.WeftVersion != version.Version {
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
		t.Errorf("manifest = %s, want the agent's core.Manifest", a.Manifest)
	}
	if !strings.Contains(a.Manifest, `"replay_policy": "safe"`) {
		t.Errorf("manifest = %s, want the safe class recorded", a.Manifest)
	}
}

// TestAllowedTools pins §6 rule 3's except-list — the only tools a
// playground run executes unasked: in every mode the ones whose code
// vouched ReplaySafe and an Output agent's submission; under
// side_effects "allow" also the names the runtime opted in
// (AllowSideEffects, whether or not the agent registers them — a
// ToolSource may supply one later). Everything else parks by default
// (core.ParkAllExcept), so the list is empty, not absent, when nothing
// is vouched.
func TestAllowedTools(t *testing.T) {
	lookup := core.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) { return "", nil },
		core.Replay(core.ReplaySafe))
	refund := core.Tool("refund", "Refund.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	escalate := core.Tool("escalate", "Escalate.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	agent := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("a"), lookup, refund, escalate)
	bare := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("bare"), refund)
	typed := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("typed"), core.Output[struct {
		OK bool `json:"ok"`
	}](), refund)

	reg := newRegistry(&config{agents: []*core.Agent{agent, bare, typed}})
	for _, allow := range []bool{false, true} {
		if got := reg.allowedTools("a", allow); !reflect.DeepEqual(got, []string{"lookup_order"}) {
			t.Errorf("allowed (allow mode %v) = %v, want [lookup_order] — the safe tool alone", allow, got)
		}
		if got := reg.allowedTools("bare", allow); got == nil || len(got) != 0 {
			t.Errorf("allowed (nothing vouched, allow mode %v) = %#v, want empty: everything parks", allow, got)
		}
		if got := reg.allowedTools("typed", allow); !reflect.DeepEqual(got, []string{"submit_output"}) {
			t.Errorf("allowed (Output agent, allow mode %v) = %v, want [submit_output] — the run's answer is not a side effect", allow, got)
		}
	}
	opted := newRegistry(&config{agents: []*core.Agent{agent},
		allow: map[string]bool{"escalate": true, "dynamic_tool": true}})
	if got := opted.allowedTools("a", false); !reflect.DeepEqual(got, []string{"lookup_order"}) {
		t.Errorf("allowed (opted in, substitute/park) = %v, want [lookup_order] — an opt-in runs only under allow", got)
	}
	if got := opted.allowedTools("a", true); !reflect.DeepEqual(got, []string{"dynamic_tool", "escalate", "lookup_order"}) {
		t.Errorf("allowed (opted in, allow) = %v, want [dynamic_tool escalate lookup_order]", got)
	}
}

// TestSideEffectModes pins WEFT-PLAYGROUND §5.1/§6.3's three modes for
// an opted-in tool and a ReplaySafe one, through execute: an
// AllowSideEffects tool runs for real only when the command asks for
// side_effects "allow" — under "substitute" (the default) a recorded
// call is answered from the record, under "park" it waits at the
// boundary, its handler never runs; a ReplaySafe tool runs in all
// three. Before the fix the opted-in tool ran for real in every mode.
func TestSideEffectModes(t *testing.T) {
	var escalated, looked atomic.Int64
	escalate := core.Tool("escalate", "Escalate.", func(ctx context.Context, in struct {
		ID string `json:"id"`
	}) (string, error) {
		escalated.Add(1)
		return "escalated for real", nil
	})
	lookup := core.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct {
		ID string `json:"id"`
	}) (string, error) {
		looked.Add(1)
		return "shipped", nil
	}, core.Replay(core.ReplaySafe))
	record := &sourceRun{
		input: []core.Message{core.User("escalate 1")},
		steps: []core.Message{
			{Role: core.RoleAssistant, Content: []core.Part{core.ToolCallPart{ID: "c0", Name: "escalate", Args: []byte(`{"id":"1"}`)}}},
			{Role: core.RoleTool, Content: []core.Part{core.ToolResultPart{CallID: "c0", Name: "escalate", Content: "escalated (recorded)"}}},
			core.Assistant("done"),
		},
	}
	in := "escalate 1"
	run := func(tool, mode string) (*link, string) {
		t.Helper()
		agent := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: tool, Args: `{"id":"1"}`}),
			wefttest.Say("done"),
		), core.Name("a"), escalate, lookup)
		l := newExecLink(map[string]bool{"escalate": true}, agent)
		cmd := command{CommandID: "cmd_" + mode, Agent: "a", Engine: "live", Thread: "ephemeral",
			Input: &in, SideEffects: mode, Source: &sourceSpec{RunID: "s_x-t1"}, src: record,
			Overrides: overrides{ToolsEnabled: []string{tool}}}
		if reason, ok := l.validate(context.Background(), &command{CommandID: cmd.CommandID, Agent: "a",
			Engine: "live", Thread: "ephemeral", SideEffects: mode, Input: &in,
			Overrides: overrides{ToolsEnabled: []string{tool}}}); !ok {
			t.Fatalf("%s under %q rejected: %s", tool, mode, reason)
		}
		status, runID, errText := l.execute(context.Background(), cmd, "pg_"+mode)
		if status != "succeeded" {
			t.Fatalf("%s under %q = %s (%s)", tool, mode, status, errText)
		}
		return l, runID
	}

	if l, id := run("escalate", "substitute"); l.parked[id] != nil || escalated.Load() != 0 {
		t.Errorf("opted-in tool under substitute: parked %v, handler runs %d — want substituted from the record, 0", l.parked[id] != nil, escalated.Load())
	}
	if l, id := run("escalate", ""); l.parked[id] != nil || escalated.Load() != 0 {
		t.Errorf("opted-in tool under the default mode: parked %v, handler runs %d — want substituted, 0", l.parked[id] != nil, escalated.Load())
	}
	if l, id := run("escalate", "park"); l.parked[id] == nil || escalated.Load() != 0 {
		t.Errorf("opted-in tool under park: parked %v, handler runs %d — want parked, 0", l.parked[id] != nil, escalated.Load())
	}
	if l, id := run("escalate", "allow"); l.parked[id] != nil || escalated.Load() != 1 {
		t.Errorf("opted-in tool under allow: parked %v, handler runs %d — want it to run once", l.parked[id] != nil, escalated.Load())
	}
	for i, mode := range []string{"substitute", "park", "allow"} {
		if l, id := run("lookup_order", mode); l.parked[id] != nil || looked.Load() != int64(i+1) {
			t.Errorf("ReplaySafe tool under %q: parked %v, handler runs %d — want it to run (%d)", mode, l.parked[id] != nil, looked.Load(), i+1)
		}
	}
}

// newExecLink builds a link over agents for the executor tests below,
// with allow as the AllowSideEffects set.
func newExecLink(allow map[string]bool, agents ...*core.Agent) *link {
	cfg := &config{agents: agents, allow: allow}
	return newLink(cfg, newRegistry(cfg), "", "")
}

// TestToolSourceToolsPark pins §6 rule 3 for a tool the registry cannot
// see: one that reaches the run only through core.ToolSource. It parks
// like any unannotated tool — before the default-deny rule the parked
// set was built from Agent.Tools, the source's tool was not in it, and
// its handler ran for real in a playground command. In substitute mode
// a call the source recorded is answered from the record; a miss stays
// parked. The opted-in name runs under side_effects "allow".
func TestToolSourceToolsPark(t *testing.T) {
	var wired atomic.Int64
	wire := core.Tool("wire_money", "Wire money.", func(ctx context.Context, in struct {
		To string `json:"to"`
	}) (string, error) {
		wired.Add(1)
		return "sent for real", nil
	})
	script := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "wire_money", Args: `{"to":"acme"}`}), // 1: parks
		wefttest.ToolCalls(wefttest.Call{Name: "wire_money", Args: `{"to":"acme"}`}), // 2: substituted…
		wefttest.Say("wired, on the record"),                                         // …and the chain's reply
		wefttest.ToolCalls(wefttest.Call{Name: "wire_money", Args: `{"to":"evil"}`}), // 3: a miss parks
		wefttest.ToolCalls(wefttest.Call{Name: "wire_money", Args: `{"to":"acme"}`}), // 4: opted in, runs
		wefttest.Say("wired"),
	)
	agent := core.New(script, core.Name("a"),
		core.ToolSource(func() []*core.ToolDef { return []*core.ToolDef{wire} }))
	if len(agent.Tools()) != 0 {
		t.Fatal("the registry would see the source's tool (test bug)")
	}
	l := newExecLink(nil, agent)
	in := "wire it"
	run := func(id string, mutate func(*command)) string {
		t.Helper()
		cmd := command{CommandID: id, Agent: "a", Input: &in}
		if mutate != nil {
			mutate(&cmd)
		}
		status, runID, errText := l.execute(context.Background(), cmd, "pg_"+id)
		if status != "succeeded" {
			t.Fatalf("command %s = %s (%s)", id, status, errText)
		}
		return runID
	}

	if id := run("park", nil); l.parked[id] == nil || wired.Load() != 0 {
		t.Fatalf("a ToolSource tool nobody vouched: parked %v, handler runs %d — want parked, 0", l.parked[id] != nil, wired.Load())
	}

	// Substitute: the source run recorded this very call.
	record := &sourceRun{
		input: []core.Message{core.User("wire it")},
		steps: []core.Message{
			{Role: core.RoleAssistant, Content: []core.Part{core.ToolCallPart{ID: "c0", Name: "wire_money", Args: []byte(`{"to":"acme"}`)}}},
			{Role: core.RoleTool, Content: []core.Part{core.ToolResultPart{CallID: "c0", Name: "wire_money", Content: "sent (recorded)"}}},
			core.Assistant("wired"),
		},
	}
	sub := func(c *command) { c.Source = &sourceSpec{RunID: "s_x-t1"}; c.src = record }
	if id := run("sub", sub); l.parked[id] != nil || wired.Load() != 0 {
		t.Errorf("a recorded ToolSource call: parked %v, handler runs %d — want substituted, 0", l.parked[id] != nil, wired.Load())
	}
	if got := script.LastRequest().Messages; len(got) != 3 || got[2].Content[0].(core.ToolResultPart).Content != "sent (recorded)" {
		t.Errorf("the substituted run fed the model %+v, want the recorded result", got)
	}
	if id := run("miss", sub); l.parked[id] == nil || wired.Load() != 0 {
		t.Errorf("an unrecorded ToolSource call: parked %v, handler runs %d — want parked, 0", l.parked[id] != nil, wired.Load())
	}

	// Opted in by name, the source's tool runs under side_effects
	// "allow" (in the other modes an opt-in is a side effect like any).
	optedIn := newExecLink(map[string]bool{"wire_money": true}, agent)
	cmd := command{CommandID: "opted", Agent: "a", Input: &in, SideEffects: "allow"}
	if status, _, errText := optedIn.execute(context.Background(), cmd, "pg_opted"); status != "succeeded" || wired.Load() != 1 {
		t.Errorf("AllowSideEffects(wire_money): %s (%s), handler runs %d — want it to run once", status, errText, wired.Load())
	}
}

// TestSubagentChildToolsPark pins the rule's reach (§6 rule 3): a
// delegation the code vouched for (or the runtime opted in) runs, and
// the child agent it starts is under the same rule — the child's own
// unannotated tool parks, the delegating call reads SUBAGENT_PENDING,
// and nothing fired. Before the default-deny rule the child ran with
// no park set at all.
func TestSubagentChildToolsPark(t *testing.T) {
	var refunds atomic.Int64
	refund := core.Tool("refund", "Refund.", func(ctx context.Context, in struct{}) (string, error) {
		refunds.Add(1)
		return "refunded for real", nil
	})
	child := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{}`}),
		wefttest.Say("refunded"),
	), core.Name("billing"), refund)
	script := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "billing", Args: `{"prompt":"refund order 1"}`}),
		wefttest.Say("billing could not finish"),
	)
	parent := core.New(script, core.Name("a"),
		core.Subagent("billing", "Billing.", child, core.Replay(core.ReplaySafe)))
	l := newExecLink(nil, parent)

	in := "refund order 1"
	cmd := command{CommandID: "cmd_child", Agent: "a", Input: &in}
	status, runID, errText := l.execute(context.Background(), cmd, "pg_child")
	if status != "succeeded" {
		t.Fatalf("execute = %s (%s)", status, errText)
	}
	if n := refunds.Load(); n != 0 {
		t.Errorf("the child agent's never tool ran %d times in a playground run, want 0", n)
	}
	last := script.LastRequest().Messages
	tr, ok := last[len(last)-1].Content[0].(core.ToolResultPart)
	if !ok || !tr.IsError || !strings.Contains(tr.Content, core.CodeSubagentPending) {
		t.Errorf("the delegating call's result = %+v, want %s", last[len(last)-1], core.CodeSubagentPending)
	}
	// The parent run itself did not park: the child's boundary is not
	// the panel's to decide.
	if l.parked[runID] != nil {
		t.Error("the parent run parked on the child's call")
	}
}

// TestOutputSubmissionDoesNotPark pins the except-list's third member:
// an agent built with core.Output ends its run by calling
// submit_output, which is the answer, not a side effect — a re-run of
// such an agent must reach its output instead of parking on it.
func TestOutputSubmissionDoesNotPark(t *testing.T) {
	type verdict struct {
		OK bool `json:"ok"`
	}
	agent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "submit_output", Args: `{"ok":true}`}),
	), core.Name("a"), core.Output[verdict]())
	l := newExecLink(nil, agent)
	in := "judge"
	res, err := agent.Generate(context.Background(), l.runOptions(command{CommandID: "cmd_out", Agent: "a", Input: &in}, "pg_out")...)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 0 {
		t.Fatalf("the structured-output submission parked: %+v", res.Pending)
	}
	if v, err := core.OutputOf[verdict](res); err != nil || !v.OK {
		t.Errorf("output = %+v, %v — want the submitted verdict", v, err)
	}
}

// TestExecuteParksEnabledNonOptInTool pins §6 rule 3 through the
// executor's own option list: a command that enables a tool the runtime
// has not opted in runs with that tool parked — the scripted model's
// call lands on RunResult.Pending and the tool's handler never
// executes — while the opted-in tool, enabled the same way under
// side_effects "allow", runs for real (a turned-off tool parks nothing:
// it is not offered at all).
func TestExecuteParksEnabledNonOptInTool(t *testing.T) {
	var refundRan, lookupRan atomic.Bool
	lookup := core.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) {
		lookupRan.Store(true)
		return "shipped", nil
	})
	refund := core.Tool("refund", "Refund.", func(ctx context.Context, in struct{}) (string, error) {
		refundRan.Store(true)
		return "refunded", nil
	})
	agent := core.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "refund", Args: `{}`}),
			wefttest.Say("refunded"), // never reached: the run parks at the call
			wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{}`}),
			wefttest.Say("shipped"),
		),
		core.Name("a"), lookup, refund)
	l := newLink(&config{agents: []*core.Agent{agent}},
		newRegistry(&config{agents: []*core.Agent{agent}, allow: map[string]bool{"lookup_order": true}}), "", "")

	run := func(id, input, tool, mode string) *core.RunResult {
		t.Helper()
		in := input
		cmd := command{CommandID: id, Agent: "a", Engine: "live", Thread: "ephemeral", SideEffects: mode,
			Input: &in, Overrides: overrides{ToolsEnabled: []string{tool}}}
		res, err := agent.Generate(context.Background(), l.runOptions(cmd, "pg_"+id)...)
		if err != nil {
			t.Fatalf("command %s failed: %v", id, err)
		}
		return res
	}

	// The enabled non-opted-in tool parks: the call is Pending.
	if res := run("park", "refund it", "refund", ""); len(res.Pending) != 1 || res.Pending[0].Name != "refund" {
		t.Errorf("pending = %+v, want the parked refund call", res.Pending)
	}

	// The opted-in tool enabled the same way under "allow" parks
	// nothing (run 2's model turn is the Say the parked run never
	// reached) and runs for real on the next one (run 3 calls
	// lookup_order).
	if res := run("live", "where is it?", "lookup_order", "allow"); len(res.Pending) != 0 {
		t.Errorf("pending = %+v, want none for the opted-in tool", res.Pending)
	}
	if res := run("live", "and again", "lookup_order", "allow"); res.Text() == "" || !lookupRan.Load() {
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
	lookup := core.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	refund := core.Tool("refund", "Refund.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	agent := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("a"), core.MaxSteps(6), core.Parallelism(4), lookup, refund)
	alt := wefttest.Script(wefttest.Say("alt"))
	l := newLink(&config{
		agents: []*core.Agent{agent},
		models: map[string]core.Model{"glm-5.3-flash": alt},
		budget: Budget{MaxRunsPerExperiment: 1},
	}, newRegistry(&config{
		agents: []*core.Agent{agent},
		models: map[string]core.Model{"glm-5.3-flash": alt},
		allow:  map[string]bool{"lookup_order": true},
	}), "", "")
	l.cfg.threads = thread.Memory() // fork mode's storage requirement
	agentCmd := func(mutate func(*command)) *command {
		cmd := command{CommandID: "cmd_t", Agent: "a", Engine: "live", Thread: "ephemeral"}
		if mutate != nil {
			mutate(&cmd)
		}
		return &cmd
	}

	if _, ok := l.validate(context.Background(), agentCmd(nil)); !ok {
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
			reason, ok := l.validate(context.Background(), agentCmd(tc.mutate))
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
	if _, ok := l.validate(context.Background(), agentCmd(func(c *command) { c.Overrides.Model = own })); !ok {
		t.Error("the agent's own model name was refused")
	}
	if _, ok := l.validate(context.Background(), agentCmd(func(c *command) { c.Overrides.Model = "glm-5.3-flash" })); !ok {
		t.Error("a registered alternate was refused")
	}

	// Fork mode without thread storage is rejected before anything
	// else (this link carries none).
	bare := newLink(&config{agents: []*core.Agent{agent}}, newRegistry(&config{agents: []*core.Agent{agent}}), "", "")
	if reason, ok := bare.validate(context.Background(), agentCmd(func(c *command) { c.Thread = "fork" })); ok || !strings.Contains(reason, "runtime.Threads") {
		t.Errorf("fork without threads: reason = %q ok = %v, want the Threads requirement", reason, ok)
	}

	// The budget cap: one run per experiment, so the second command of
	// that experiment is rejected with budget_exceeded and the app's
	// own runs are untouched (they never pass through validate).
	l.tally["exp_1"] = &budgetState{runs: 1}
	if reason, ok := l.validate(context.Background(), agentCmd(func(c *command) { c.ExperimentID = "exp_1" })); ok || reason != "budget_exceeded" {
		t.Errorf("breached budget: reason = %q ok = %v, want budget_exceeded", reason, ok)
	}
	if _, ok := l.validate(context.Background(), agentCmd(func(c *command) { c.ExperimentID = "exp_2" })); !ok {
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
	b.reserve()
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
		t.Error("runs miscounted before the first admission")
	}
	r.reserve()
	r.reserve()
	if !r.over(cap, 1) {
		t.Error("run-count breach not detected")
	}
	var zero Budget
	if (&budgetState{tokens: 1 << 40, runs: 1 << 20}).over(zero, 1<<40) {
		t.Error("zero caps must never breach")
	}
}

// TestCutAtStep pins §5.1's from_step semantics over a run's own
// steps: keep them through step N−1 (tool results included) and run
// step N fresh; the cut is the index where the Nth assistant message
// begins.
func TestCutAtStep(t *testing.T) {
	steps := []core.Message{
		core.Assistant("checking"), // step 0's assistant
		{Role: core.RoleTool, Content: []core.Part{core.ToolResultPart{CallID: "c1", Content: "shipped"}}},
		core.Assistant("it shipped"),     // step 1's assistant
		core.Assistant("anything else?"), // step 2's assistant
	}
	if got := cutAtStep(steps, 0); got != 0 {
		t.Errorf("from_step 0 cut = %d, want 0 (re-run the whole turn)", got)
	}
	if got := cutAtStep(steps, 1); got != 2 {
		t.Errorf("from_step 1 cut = %d, want 2 (step 0 kept: its assistant, its tool result)", got)
	}
	if got := cutAtStep(steps, 2); got != 3 {
		t.Errorf("from_step 2 cut = %d, want 3 (steps 0 and 1 kept, tool result included)", got)
	}
	if got := cutAtStep(steps, 9); got != len(steps) {
		t.Errorf("from_step beyond the transcript cut = %d, want len %d", got, len(steps))
	}
	// What a resumed run recorded before its first model call (the
	// completed tool message) is not a step's: step 0 starts after it.
	resumed := append([]core.Message{{Role: core.RoleTool,
		Content: []core.Part{core.ToolResultPart{CallID: "c0", Content: "approved"}}}}, steps...)
	if got := cutAtStep(resumed, 0); got != 1 {
		t.Errorf("from_step 0 cut on a resumed run = %d, want 1 (the joined tool message stays)", got)
	}
}

// TestBudgetCountsSubagentUsageOnce pins the budget's count for a
// command whose run delegates (§6 rule 6): a child run's usage rolls
// into its parent's result (ADR 0014), the tally reads that one result,
// and the child — which inherits the experiment's metadata by design —
// is never a second command of the experiment.
func TestBudgetCountsSubagentUsageOnce(t *testing.T) {
	child := core.New(wefttest.Script(
		wefttest.Say("found it").WithUsage(core.Usage{InputTokens: 20, OutputTokens: 10}),
	), core.Name("researcher"))
	parent := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"look"}`}).WithUsage(core.Usage{InputTokens: 4, OutputTokens: 1}),
		wefttest.Say("done").WithUsage(core.Usage{InputTokens: 3, OutputTokens: 2}),
	), core.Name("a"), core.Subagent("research", "Research.", child))
	cfg := &config{agents: []*core.Agent{parent}, allow: map[string]bool{"research": true}}
	l := newLink(cfg, newRegistry(cfg), "", "")

	in := "go"
	// "allow": the opted-in delegation runs for real only there.
	cmd := command{CommandID: "cmd_sub", Agent: "a", Input: &in, ExperimentID: "exp_sub", SideEffects: "allow"}
	if reason, ok := l.validate(context.Background(), &cmd); !ok {
		t.Fatalf("validate: %s", reason)
	}
	if !l.reserve(cmd) {
		t.Fatal("reserve refused the first command")
	}
	status, runID, errText := l.execute(context.Background(), cmd, "pg_sub")
	if status != "succeeded" || runID != "pg_sub" || errText != "" {
		t.Fatalf("execute = %q %q %q", status, runID, errText)
	}
	st := l.tally["exp_sub"]
	if st.tokens != 40 || st.runs != 1 {
		t.Errorf("tally = %d tokens / %d runs, want 40 / 1 (parent 10 + child 30, one command)", st.tokens, st.runs)
	}
	if len(l.parked) != 0 || len(l.steerQ) != 0 {
		t.Errorf("bookkeeping left behind: parked %d, steer queues %d", len(l.parked), len(l.steerQ))
	}
}

// TestValidateOptionLab pins the runtime's re-validation of the option
// lab's typed overrides (plan F3): each tool name is the agent's, only_tools
// stays inside tools_enabled, a named tool_choice names a tool the run
// keeps on and does not park, the mode is one of the four, top_p sits in
// 0..1 and stop carries at most four non-empty sequences — each refusal
// naming its rule before the ack. Valid values pass, and a command
// without the new fields validates as before.
func TestValidateOptionLab(t *testing.T) {
	lookup := core.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	refund := core.Tool("refund", "Refund.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	track := core.Tool("track_parcel", "Track.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	agent := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("a"), core.MaxSteps(6), lookup, refund, track)
	l := newExecLink(nil, agent)
	f := func(v float64) *float64 { return &v }
	n := func(v int) *int { return &v }
	cmd := func(o overrides) *command {
		return &command{CommandID: "cmd_lab", Agent: "a", Engine: "live", Thread: "ephemeral", Overrides: o}
	}
	for _, tc := range []struct {
		name string
		o    overrides
	}{
		{"old shape", overrides{ToolsEnabled: []string{"refund"}, Options: map[string]float64{"max_steps": 3, "temperature": 0.4}}},
		{"only_tools alone", overrides{OnlyTools: []string{"lookup_order"}}},
		{"only_tools inside tools_enabled", overrides{ToolsEnabled: []string{"lookup_order", "refund"}, OnlyTools: []string{"refund"}}},
		{"park_on", overrides{ParkOn: []string{"track_parcel"}}},
		{"tool_choice auto", overrides{ToolChoice: &toolChoiceWire{Mode: "auto"}}},
		{"tool_choice any", overrides{ToolChoice: &toolChoiceWire{Mode: "any"}}},
		{"tool_choice none", overrides{ToolChoice: &toolChoiceWire{Mode: "none"}}},
		{"tool_choice named", overrides{OnlyTools: []string{"lookup_order"}, ToolChoice: &toolChoiceWire{Mode: "named", Name: "lookup_order"}}},
		{"params", overrides{Params: &paramsWire{TopP: f(0.9), MaxTokens: n(256), Stop: []string{"END", "STOP"}, Seed: new(int64)}}},
		{"params edges", overrides{Params: &paramsWire{TopP: f(0), Stop: []string{"a", "b", "c", "d"}}}},
	} {
		if reason, ok := l.validate(context.Background(), cmd(tc.o)); !ok {
			t.Errorf("%s: rejected (%s), want accepted", tc.name, reason)
		}
	}
	for _, tc := range []struct {
		name, want string
		o          overrides
	}{
		{"only_tools unknown", `unknown tool "nope" in only_tools`, overrides{OnlyTools: []string{"nope"}}},
		{"only_tools widens tools_enabled", `only_tools may only narrow tools_enabled: tool "refund" is not enabled`,
			overrides{ToolsEnabled: []string{"lookup_order"}, OnlyTools: []string{"refund"}}},
		{"park_on unknown", `unknown tool "nope" in park_on`, overrides{ParkOn: []string{"nope"}}},
		{"tool_choice mode", `unknown tool_choice mode "tool"`, overrides{ToolChoice: &toolChoiceWire{Mode: "tool"}}},
		{"tool_choice name without named", `takes no name`, overrides{ToolChoice: &toolChoiceWire{Mode: "any", Name: "refund"}}},
		{"tool_choice named without a name", "needs a tool name", overrides{ToolChoice: &toolChoiceWire{Mode: "named"}}},
		{"tool_choice named unknown", `unknown tool "nope" in tool_choice`, overrides{ToolChoice: &toolChoiceWire{Mode: "named", Name: "nope"}}},
		{"tool_choice named off", `tool_choice names "refund", which this command turns off`,
			overrides{OnlyTools: []string{"lookup_order"}, ToolChoice: &toolChoiceWire{Mode: "named", Name: "refund"}}},
		{"tool_choice named off by tools_enabled", `which this command turns off`,
			overrides{ToolsEnabled: []string{"lookup_order"}, ToolChoice: &toolChoiceWire{Mode: "named", Name: "refund"}}},
		{"tool_choice named parked", `which park_on parks: every forced call would park`,
			overrides{ParkOn: []string{"refund"}, ToolChoice: &toolChoiceWire{Mode: "named", Name: "refund"}}},
		{"top_p high", "top_p 1.5 is outside 0..1", overrides{Params: &paramsWire{TopP: f(1.5)}}},
		{"top_p negative", "top_p -0.1 is outside 0..1", overrides{Params: &paramsWire{TopP: f(-0.1)}}},
		{"stop too many", "stop takes at most 4 sequences, got 5", overrides{Params: &paramsWire{Stop: []string{"a", "b", "c", "d", "e"}}}},
		{"stop empty", "stop sequences must be non-empty", overrides{Params: &paramsWire{Stop: []string{""}}}},
		{"max_tokens zero", "max_tokens must be positive", overrides{Params: &paramsWire{MaxTokens: n(0)}}},
		{"max_tokens negative", "max_tokens must be positive", overrides{Params: &paramsWire{MaxTokens: n(-5)}}},
		{"raised max_steps", "raises the agent's cap", overrides{Options: map[string]float64{"max_steps": 7}}},
		{"unknown model, no resolver", `unknown model "claude-haiku-4-5": not on this runtime's allow-list (register it with runtime.Models or add runtime.ModelResolver)`,
			overrides{Model: "claude-haiku-4-5"}},
	} {
		reason, ok := l.validate(context.Background(), cmd(tc.o))
		if ok {
			t.Errorf("%s: accepted, want rejected with %q", tc.name, tc.want)
			continue
		}
		if !strings.Contains(reason, tc.want) {
			t.Errorf("%s: reason = %q, want it to contain %q", tc.name, reason, tc.want)
		}
	}
}

// TestValidateAgentDefaultToolChoice pins the agent's own tool choice
// under a command that sends none: a named default the command turns
// off (only_tools or tools_enabled) would fail the run's first step
// after the ack — refused before it, naming the fix; a default the
// command keeps on, or a tool_choice sent over it, passes.
func TestValidateAgentDefaultToolChoice(t *testing.T) {
	classify := core.Tool("classify", "Classify.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	lookup := core.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	agent := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("router"), classify, lookup,
		core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "classify"}))
	l := newExecLink(nil, agent)
	cmd := func(o overrides) *command {
		return &command{CommandID: "cmd_def", Agent: "router", Engine: "live", Thread: "ephemeral", Overrides: o}
	}
	for _, o := range []overrides{{OnlyTools: []string{"lookup_order"}}, {ToolsEnabled: []string{"lookup_order"}}} {
		reason, ok := l.validate(context.Background(), cmd(o))
		if want := `the agent's default tool_choice names "classify", which this command turns off; send tool_choice`; ok || reason != want {
			t.Errorf("%+v: reason = %q ok = %v, want %q", o, reason, ok, want)
		}
	}
	for _, o := range []overrides{
		{},
		{OnlyTools: []string{"classify"}},
		{OnlyTools: []string{"lookup_order"}, ToolChoice: &toolChoiceWire{Mode: "auto"}},
	} {
		if reason, ok := l.validate(context.Background(), cmd(o)); !ok {
			t.Errorf("%+v: rejected (%s)", o, reason)
		}
	}
	// A named default park_on parks: every forced call would park — the
	// explicit case's rule and sentence, the tool named.
	reason, ok := l.validate(context.Background(), cmd(overrides{ParkOn: []string{"classify"}}))
	if want := "tool_choice names classify, which park_on parks: every forced call would park"; ok || reason != want {
		t.Errorf("default named tool parked: reason = %q ok = %v, want %q", reason, ok, want)
	}
	// park_on another tool, or a tool_choice sent over the default, passes.
	for _, o := range []overrides{
		{ParkOn: []string{"lookup_order"}},
		{ParkOn: []string{"classify"}, ToolChoice: &toolChoiceWire{Mode: "auto"}},
	} {
		if reason, ok := l.validate(context.Background(), cmd(o)); !ok {
			t.Errorf("%+v: rejected (%s)", o, reason)
		}
	}
}

// TestOptionLabReachesTheModel pins that the option lab's overrides are
// the run's: the sampling params (laid over the agent's own — the
// knobs a command leaves alone keep the agent's values), the tool
// choice, the tool list narrowed by only_tools, and park_on parking a
// tool the code vouched safe.
func TestOptionLabReachesTheModel(t *testing.T) {
	var lookupRan atomic.Bool
	lookup := core.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) {
		lookupRan.Store(true)
		return "shipped", nil
	}, core.Replay(core.ReplaySafe))
	refund := core.Tool("refund", "Refund.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	track := core.Tool("track_parcel", "Track.", func(ctx context.Context, in struct{}) (string, error) { return "", nil },
		core.Replay(core.ReplaySafe))
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "lookup_order", Args: `{}`}), wefttest.Say("never reached"))
	temp, maxTok := 0.3, 900
	agent := core.New(model, core.Name("a"), lookup, refund, track,
		core.Params(core.RequestParams{Temperature: &temp, MaxTokens: &maxTok}))
	l := newExecLink(nil, agent)

	topP, seed := 0.5, int64(42)
	in := "where is it?"
	cmd := command{CommandID: "cmd_lab", Agent: "a", Engine: "live", Thread: "ephemeral", Input: &in, Overrides: overrides{
		ToolsEnabled: []string{"lookup_order", "track_parcel"},
		OnlyTools:    []string{"lookup_order", "track_parcel"},
		Options:      map[string]float64{"temperature": 0.9},
		Params:       &paramsWire{TopP: &topP, Stop: []string{"END"}, Seed: &seed},
		ToolChoice:   &toolChoiceWire{Mode: "any"},
		ParkOn:       []string{"lookup_order"},
	}}
	if reason, ok := l.validate(context.Background(), &cmd); !ok {
		t.Fatal(reason)
	}
	res, err := agent.Generate(context.Background(), l.runOptions(cmd, "pg_lab")...)
	if err != nil {
		t.Fatal(err)
	}
	reqs := model.Requests()
	if len(reqs) != 1 {
		t.Fatalf("model calls = %d, want 1 (the run parks at the call)", len(reqs))
	}
	req := reqs[0]
	p := req.Params
	if p.Temperature == nil || *p.Temperature != 0.9 || p.TopP == nil || *p.TopP != 0.5 || p.Seed == nil || *p.Seed != 42 ||
		!reflect.DeepEqual(p.Stop, []string{"END"}) {
		t.Errorf("params = %+v, want the command's temperature/top_p/seed/stop", p)
	}
	if p.MaxTokens == nil || *p.MaxTokens != 900 {
		t.Errorf("max_tokens = %v, want the agent's own 900 kept (a run-level Params replaces the struct whole)", p.MaxTokens)
	}
	if req.ToolChoice != (core.ToolChoiceConfig{Mode: core.ToolChoiceAny}) {
		t.Errorf("tool choice = %+v, want any", req.ToolChoice)
	}
	var names []string
	for _, td := range req.Tools {
		names = append(names, td.Name)
	}
	if !reflect.DeepEqual(names, []string{"lookup_order", "track_parcel"}) {
		t.Errorf("tools = %v, want only_tools' set", names)
	}
	if len(res.Pending) != 1 || res.Pending[0].Name != "lookup_order" || lookupRan.Load() {
		t.Errorf("pending = %+v, lookup ran = %v — want park_on to park the safe tool", res.Pending, lookupRan.Load())
	}

	// A named choice reaches the request too.
	named := wefttest.Script(wefttest.Say("ok"))
	l2 := newExecLink(nil, core.New(named, core.Name("b"), lookup, refund))
	cmd2 := command{CommandID: "cmd_named", Agent: "b", Engine: "live", Thread: "ephemeral", Input: &in,
		Overrides: overrides{ToolChoice: &toolChoiceWire{Mode: "named", Name: "refund"}}}
	if reason, ok := l2.validate(context.Background(), &cmd2); !ok {
		t.Fatal(reason)
	}
	b, _ := l2.reg.agent("b")
	if _, err := b.Generate(context.Background(), l2.runOptions(cmd2, "pg_named")...); err != nil {
		t.Fatal(err)
	}
	if got := named.Requests()[0].ToolChoice; got != (core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "refund"}) {
		t.Errorf("named tool choice = %+v", got)
	}
}

// TestModelResolver pins runtime.ModelResolver: a model name outside
// the allow-list is resolved in validate (before the ack) and the run
// uses the model it returned; the resolver's error rejects the command
// with its text; a nil model is refused; the allow-list and the
// agent's own name never reach the resolver; and the registration
// reports the flag.
func TestModelResolver(t *testing.T) {
	agent := core.New(wefttest.Script(wefttest.Say("own")), core.Name("a"))
	haiku := wefttest.Script(wefttest.Say("from haiku"))
	alt := wefttest.Script(wefttest.Say("alt"))
	var asked []string
	cfg := &config{agents: []*core.Agent{agent}, models: map[string]core.Model{"glm-5.3-flash": alt}}
	ModelResolver(func(ctx context.Context, name string) (core.Model, error) {
		asked = append(asked, name)
		switch name {
		case "anthropic/claude-haiku-4-5":
			return haiku, nil
		case "nil-model":
			return nil, nil
		}
		return nil, errors.New("not a model this app serves")
	})(cfg)
	l := newLink(cfg, newRegistry(cfg), "", "")
	in := "hi"
	mk := func(model string) *command {
		return &command{CommandID: "cmd_m", Agent: "a", Engine: "live", Thread: "ephemeral", Input: &in,
			Overrides: overrides{Model: model}}
	}

	cmd := mk("anthropic/claude-haiku-4-5")
	if reason, ok := l.validate(context.Background(), cmd); !ok {
		t.Fatalf("resolved model rejected: %s", reason)
	}
	res, err := agent.Generate(context.Background(), l.runOptions(*cmd, "pg_m")...)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "from haiku" || len(haiku.Requests()) != 1 {
		t.Errorf("text = %q, haiku calls = %d — want the resolved model to answer", res.Text(), len(haiku.Requests()))
	}
	if reason, ok := l.validate(context.Background(), mk("gpt-9")); ok || reason != "model gpt-9: not a model this app serves" {
		t.Errorf("resolver error: reason = %q ok = %v, want the resolver's text", reason, ok)
	}
	if reason, ok := l.validate(context.Background(), mk("nil-model")); ok || !strings.Contains(reason, "returned no model") {
		t.Errorf("nil model: reason = %q ok = %v", reason, ok)
	}
	asked = nil
	for _, name := range []string{"glm-5.3-flash", l.reg.ownModel("a")} {
		if reason, ok := l.validate(context.Background(), mk(name)); !ok {
			t.Errorf("%s rejected: %s", name, reason)
		}
	}
	if len(asked) != 0 {
		t.Errorf("the resolver was asked %v; the allow-list and the agent's own name never reach it", asked)
	}
	scripted := mk("anthropic/claude-haiku-4-5")
	scripted.Engine, scripted.Source = "scripted", &sourceSpec{RunID: "s_x"}
	if reason, ok := l.validate(context.Background(), scripted); ok || !strings.Contains(reason, "silently replay") {
		t.Errorf("scripted + resolved model: reason = %q ok = %v, want the §5.5 refusal", reason, ok)
	}
	if !l.reg.registration("rt").Agents[0].Resolver {
		t.Error("registration resolver = false with a ModelResolver")
	}
	if newRegistry(&config{agents: []*core.Agent{agent}}).registration("rt").Agents[0].Resolver {
		t.Error("registration resolver = true without one")
	}
}

// TestRegistrationDefaults pins the agent's run defaults on the
// registration — what the option lab greys beside each override.
func TestRegistrationDefaults(t *testing.T) {
	temp, seed := 0.2, int64(9)
	tool := core.Tool("classify", "Classify.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	agent := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("a"), tool,
		core.MaxSteps(7), core.Parallelism(2),
		core.Thinking(core.ThinkingConfig{Level: core.ThinkLow}),
		core.Params(core.RequestParams{Temperature: &temp, Seed: &seed, Stop: []string{"END"}}),
		core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "classify"}))
	bare := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("bare"))
	reg := newRegistry(&config{agents: []*core.Agent{agent, bare}}).registration("rt")
	got, _ := json.Marshal(reg.Agents[0].Defaults)
	if want := `{"max_steps":7,"parallelism":2,"thinking":"low","temperature":0.2,"seed":9,"stop":["END"],"tool_choice":{"mode":"named","name":"classify"}}`; string(got) != want {
		t.Errorf("defaults = %s\nwant       %s", got, want)
	}
	got, _ = json.Marshal(reg.Agents[1].Defaults)
	if want := `{"max_steps":10,"parallelism":4,"thinking":"","tool_choice":{"mode":"auto"}}`; string(got) != want {
		t.Errorf("bare defaults = %s\nwant            %s", got, want)
	}
}

// TestAutoToolChoiceOverAutoIsNoOverride pins that tool_choice auto over
// an agent whose default is already auto writes no
// weft.override.tool_choice (it would be empty) and leaves the run
// un-overridden, while auto over a named default is a real override.
func TestAutoToolChoiceOverAutoIsNoOverride(t *testing.T) {
	classify := core.Tool("classify", "Classify.", func(ctx context.Context, in struct{}) (string, error) { return "", nil })
	attrsOf := func(agentOpts ...core.Option) map[string]string {
		rec := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
		opts := append([]core.Option{core.Name("a"), classify, core.TracerProvider(tp)}, agentOpts...)
		agent := core.New(wefttest.Script(wefttest.Say("ok")), opts...)
		l := newExecLink(nil, agent)
		in := "hi"
		cmd := command{CommandID: "cmd_auto", Agent: "a", Engine: "live", Thread: "ephemeral", Input: &in,
			Overrides: overrides{ToolChoice: &toolChoiceWire{Mode: "auto"}}}
		if _, err := agent.Generate(context.Background(), l.runOptions(cmd, "pg_auto")...); err != nil {
			t.Fatal(err)
		}
		for _, s := range rec.Ended() {
			if strings.HasPrefix(s.Name(), "invoke_agent") {
				out := map[string]string{}
				for _, kv := range s.Attributes() {
					out[string(kv.Key)] = kv.Value.String()
				}
				return out
			}
		}
		t.Fatal("no invoke_agent span")
		return nil
	}
	if v, ok := attrsOf()["weft.override.tool_choice"]; ok {
		t.Errorf("auto over auto wrote weft.override.tool_choice = %q", v)
	}
	if _, ok := attrsOf(core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceNamed, Name: "classify"}))["weft.override.tool_choice"]; !ok {
		t.Error("auto over a named default is an override: weft.override.tool_choice missing")
	}
}
