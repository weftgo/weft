package runtime

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
	RuntimeID   string              `json:"runtime_id"`
	Host        string              `json:"host"`
	Pid         int                 `json:"pid"`
	Service     string              `json:"service"`
	Env         string              `json:"env"`
	WeftVersion string              `json:"weft_version"`
	Budget      budgetWire          `json:"budget"`
	Threads     bool                `json:"threads"`
	Agents      []agentRegistration `json:"agents"`
}

// agentRegistration is one exposed agent: its weft.Manifest (names,
// instructions, policy, tools), the alternate models a command may
// switch to, the lower-only bounds on its knobs, every tool's side
// effect class ("never" for all of them until ReplayPolicy exists,
// 8b), and the tools opted in with AllowSideEffects.
type agentRegistration struct {
	Name        string            `json:"name"`
	Manifest    string            `json:"manifest"` // weft.Manifest JSON for this one agent
	Models      []string          `json:"models"`
	Limits      agentLimits       `json:"limits"`
	SideEffects map[string]string `json:"side_effects"`
	Allow       []string          `json:"allow"`
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
// model's display name, a thinking level, and the option lab's knobs
// (the arena playground's OptionsSpec vocabulary).
type overrides struct {
	Instructions string             `json:"instructions,omitempty"`
	ToolsEnabled []string           `json:"tools_enabled,omitempty"`
	Model        string             `json:"model,omitempty"`
	Thinking     string             `json:"thinking,omitempty"` // off|low|medium|high
	Options      map[string]float64 `json:"options,omitempty"`  // max_steps, parallelism, temperature
}

// transcriptEdit is a D2/D3 edit — rewrite a model reply, patch a tool
// result. Accepted by the schema (the wire shape is final), answered
// "not yet available" by Studio until 8b implements them.
type transcriptEdit struct {
	Step       int    `json:"step"`
	ToolResult string `json:"tool_result,omitempty"`
	CallID     string `json:"call_id,omitempty"`
	Content    string `json:"content,omitempty"`
}

// cancelCommand is an `event: cancel` frame's data.
type cancelCommand struct {
	CommandID string `json:"command_id"`
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
	Error     string `json:"error,omitempty"`  // rejected only
}
