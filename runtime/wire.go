package runtime

import (
	"encoding/json"

	"github.com/weftgo/weft/core"
)

// The runtime link's wire protocol (WEFT-PLAYGROUND.md §10.3): all
// JSON over HTTP. Commands come down an SSE stream; everything else
// goes up as POSTs. These are the client's copy of the shapes — the
// server's is studio/runtime's, and the JSON between them is the
// contract (pinned by the integration tests, which run both sides).
//
// The client never imports studio/runtime: this module touches the
// studio module for *studio.Server alone (runtime.Local), so the wire
// shapes are duplicated by design and the contract lives in tests.

// registration is the POST /api/runtime/register body: who the runtime
// is, what it may spend, and the agents it exposes with everything a
// command is validated against. Sent on connect and again after every
// reconnect — the copy Studio holds must always be the runtime's own.
type registration struct {
	RuntimeID   string     `json:"runtime_id"`
	Host        string     `json:"host"`
	Pid         int        `json:"pid"`
	Service     string     `json:"service"`
	Env         string     `json:"env"`
	WeftVersion string     `json:"weft_version"`
	Budget      budgetWire `json:"budget"`
	Threads     bool       `json:"threads"`
	// Breakpoints is the debugger's tool set this runtime holds right
	// now (§8.3) — sorted, empty rather than null. The set lives in
	// this process, so it outlives a Studio restart; reporting it at
	// every registration lets Studio show the rule that is parking the
	// runtime's runs instead of an empty set that is not true.
	Breakpoints []string            `json:"breakpoints"`
	Agents      []agentRegistration `json:"agents"`
	// HeartbeatMS is the link's ackHeartbeat in milliseconds: while a command is
	// in this runtime's hands, its accepted ack is re-posted at this
	// cadence (link.heartbeat), and Studio watches the command from the
	// accepted ack on — a runtime that ran nothing and could not say so
	// is marked lost, a long run that keeps beating is not.
	HeartbeatMS int64 `json:"heartbeat_ms"`
}

// agentRegistration is one exposed agent: its core.Manifest (names,
// instructions, policy, tools), the alternate models a command may
// switch to, the lower-only bounds on its knobs, every tool's side
// effect class (its ReplayPolicy; unannotated is "never",
// WEFT-PLAYGROUND §6 rule 3), and the tools opted in with
// AllowSideEffects.
//
// Resolver says the runtime holds a ModelResolver: a command may name
// a model outside Models and the runtime decides whether it exists.
// Defaults are the agent's own run defaults — what a command's
// overrides replace, shown greyed beside them by the option lab.
type agentRegistration struct {
	Name        string            `json:"name"`
	Manifest    string            `json:"manifest"` // core.Manifest JSON for this one agent
	Models      []string          `json:"models"`
	Resolver    bool              `json:"resolver"`
	Limits      agentLimits       `json:"limits"`
	Defaults    agentDefaults     `json:"defaults"`
	SideEffects map[string]string `json:"side_effects"`
	Allow       []string          `json:"allow"`
}

// agentDefaults are the agent's run defaults as the command's override
// vocabulary spells them: the caps (the manifest policy's), the
// thinking level ("" is the provider default, never sent), the
// sampling knobs its core.Params set (absent: the adapter's own), and
// its tool choice (mode "auto" when none was set).
type agentDefaults struct {
	MaxSteps    int            `json:"max_steps"`
	Parallelism int            `json:"parallelism"`
	Thinking    string         `json:"thinking"`
	Temperature *float64       `json:"temperature,omitempty"`
	TopP        *float64       `json:"top_p,omitempty"`
	MaxTokens   *int           `json:"max_tokens,omitempty"`
	Seed        *int64         `json:"seed,omitempty"`
	Stop        []string       `json:"stop,omitempty"`
	ToolChoice  toolChoiceWire `json:"tool_choice"`
}

// agentLimits are the agent's own caps — the lower-only bounds a
// command's max_steps and parallelism are checked against.
type agentLimits struct {
	MaxSteps    int `json:"max_steps"`
	Parallelism int `json:"parallelism"`
}

// budgetWire is runtime.Limits on the wire (§10.3's "budget").
type budgetWire struct {
	MaxTokensPerExperiment int64 `json:"max_tokens_per_experiment"`
	MaxRunsPerExperiment   int64 `json:"max_runs_per_experiment"`
}

// registerResponse is what Studio answers a registration with.
type registerResponse struct {
	RuntimeID   string `json:"runtime_id"`
	CommandsURL string `json:"commands_url"`
}

// command is one `event: run` frame's data: the whole experiment —
// source turn, input, overrides, engine, side-effect mode, thread
// mode — plus who asked (actor) and which page scoped it (public_id).
type command struct {
	CommandID       string           `json:"command_id"`
	Agent           string           `json:"agent"`
	Source          *sourceSpec      `json:"source"`
	Input           *string          `json:"input"`
	Overrides       overrides        `json:"overrides"`
	TranscriptEdits []transcriptEdit `json:"transcript_edits"`
	Engine          string           `json:"engine"`       // "live" | "scripted"
	SideEffects     string           `json:"side_effects"` // "substitute" | "park" | "allow"
	Thread          string           `json:"thread"`       // "ephemeral" | "fork"
	ExperimentID    string           `json:"experiment_id"`
	Actor           string           `json:"actor"`
	PublicID        string           `json:"public_id"`

	// src is the source run's transcript, resolved once when the command
	// is dispatched and read by everything after (validation, the kept
	// prefix, the scripted engine, the substitute lookup) — one read, so
	// they cannot disagree about a source that is still growing. Off the
	// wire; nil without a source or when it could not be resolved.
	src *sourceRun
	// prefix is the transcript the run is fed before its input: the
	// source's kept part with the edits applied, composed (and so
	// validated) once at dispatch.
	prefix []core.Message
	// model is the model a ModelResolver returned for the command's
	// model override, resolved once in validate (before the ack) and
	// carried by every run of the command, a resume's included. Nil when
	// the override is the agent's own or an allow-list name.
	model core.Model
	// schemas are the agent's tools' input schemas, read in validate:
	// what a tool_args edit is checked against (obsdb.CheckToolArgs).
	schemas map[string]json.RawMessage
	// kept is the source's kept steps with the edits applied (nil
	// without edits): the substitute lookup's kept calls.
	kept []core.Message
}

// sourceSpec names the run to re-run: its id and the step to continue
// from. from_step N keeps the transcript through step N−1 (tool
// results included) and runs step N fresh; 0 re-runs the whole turn.
type sourceSpec struct {
	RunID    string `json:"run_id"`
	FromStep int    `json:"from_step"`
}

// overrides are the experiment's changes (§5.1): a replacement system
// prompt, a subset of the agent's tools (narrowing only), an alternate
// model's name, a thinking level, and the option lab's knobs — the
// numeric options (the arena playground's OptionsSpec vocabulary) and,
// beside them, the typed ones (plan F3): sampling params, a tool
// choice, tools to park, and only_tools (a subset of tools_enabled
// when both are sent). Every one narrows or is neutral.
type overrides struct {
	Instructions string             `json:"instructions,omitempty"`
	ToolsEnabled []string           `json:"tools_enabled,omitempty"`
	Model        string             `json:"model,omitempty"`
	Thinking     string             `json:"thinking,omitempty"` // off|low|medium|high
	Options      map[string]float64 `json:"options,omitempty"`  // max_steps, parallelism, temperature
	Params       *paramsWire        `json:"params,omitempty"`
	ToolChoice   *toolChoiceWire    `json:"tool_choice,omitempty"`
	ParkOn       []string           `json:"park_on,omitempty"`
	OnlyTools    []string           `json:"only_tools,omitempty"`
}

// paramsWire is the sampling override beside options.temperature: the
// rest of core.RequestParams. Absent fields keep the agent's own — a
// command cannot clear the agent's stop or max_tokens.
type paramsWire struct {
	TopP      *float64 `json:"top_p,omitempty"`
	MaxTokens *int     `json:"max_tokens,omitempty"`
	Stop      []string `json:"stop,omitempty"`
	Seed      *int64   `json:"seed,omitempty"`
}

// toolChoiceWire is a tool choice on the wire: mode auto | any | none
// | named, and the tool's name under named (core.ToolChoiceConfig).
type toolChoiceWire struct {
	Mode string `json:"mode"`
	Name string `json:"name,omitempty"`
}

// transcriptEdit is a D2/D3 edit of the kept prefix (ADR 0029 §8).
// Kind is the discriminator; absent, the fields decide as before F2 —
// tool_result + call_id patches a result ("tool_result"), content
// rewrites a call-free reply ("reply"). The F2 kinds: "user" rewrites
// a user message of the step (content; Index picks among the step's
// user messages, step 0's turn prompt first), "tool_args" rewrites a
// call's arguments (call_id + args, checked against the tool's
// schema), "insert" adds a user message (content) at the boundary
// before step Step's model call. Step is the source run's own step
// index (for insert, the boundary 0..from_step).
type transcriptEdit struct {
	Kind       string          `json:"kind,omitempty"`
	Step       int             `json:"step"`
	ToolResult string          `json:"tool_result,omitempty"`
	CallID     string          `json:"call_id,omitempty"`
	Content    string          `json:"content,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
	Index      int             `json:"index,omitempty"`
}

// cancelCommand is an `event: cancel` frame's data.
type cancelCommand struct {
	CommandID string `json:"command_id"`
}

// approvalDecision is an `event: approve` frame's data (WEFT-DEVTOOLS
// §8.2): a human decision on one parked call of a run this runtime
// started. The runtime resumes the parked run with the core's own
// verbs — Approve runs the handler, Deny skips it, Resolve pastes a
// result computed outside the process (ADR 0007) — under the same
// at-most-once ack path as a run command.
type approvalDecision struct {
	CommandID string `json:"command_id"`
	RunID     string `json:"run_id"`
	CallID    string `json:"call_id"`
	Decision  string `json:"decision"` // approve | deny | resolve
	Reason    string `json:"reason,omitempty"`
	Content   string `json:"content,omitempty"`
	Actor     string `json:"actor,omitempty"`
}

// breakpointsFrame is an `event: breakpoints` frame's data (§8.3):
// the runtime parks calls to these tools on every run it starts from
// then on — the debugger's breakpoint, rule-driven parking over ADR
// 0007's boundary. An empty set clears.
type breakpointsFrame struct {
	Tools []string `json:"tools"`
}

// steerFrame is an `event: steer` frame's data (§8.4): one user
// message delivered into a runtime-started run, mid-flight.
type steerFrame struct {
	RunID   string `json:"run_id"`
	Message string `json:"message"`
}

// ack is the POST /api/runtime/acks body, sent before execution
// ("accepted"/"rejected", the at-most-once rule) and again when the
// run ends ("finished" with its status). The run's content itself
// never travels this path — OTel carries it, like any other run.
type ack struct {
	CommandID string `json:"command_id"`
	State     string `json:"state"` // accepted | rejected | finished
	RunID     string `json:"run_id,omitempty"`
	Status    string `json:"status,omitempty"` // succeeded | failed (finished only)
	Error     string `json:"error,omitempty"`  // why: a rejection's reason, a failed run's error
}
