package runtime

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"github.com/weftgo/weft/core"
)

// registry is what a runtime exposes: the agents (found by name), the
// model allow-list, the per-agent lower-only bounds, the side-effect
// allow-list, and the per-experiment budget tally. Built once from
// Install's options; the tally mutates as commands run.
type registry struct {
	cfg     *config
	agents  map[string]*core.Agent
	order   []string // agent names, registration order
	entries map[string]agentRegistration
}

// newRegistry builds the registration payload's agent list. An agent
// without a name cannot register (its manifest would be nameless) and
// is skipped with a WARN; a duplicate name keeps the first.
func newRegistry(c *config) *registry {
	r := &registry{cfg: c, agents: map[string]*core.Agent{}, entries: map[string]agentRegistration{}}
	for _, a := range c.agents {
		if a == nil {
			continue
		}
		name := a.Name()
		if name == "" {
			slog.Warn("weft/runtime: agent without core.Name is not playable", "hint", "core.Name names an agent in its manifest")
			continue
		}
		if _, dup := r.agents[name]; dup {
			continue
		}
		r.agents[name] = a
		r.order = append(r.order, name)
		r.entries[name] = r.entryFor(a, name)
	}
	return r
}

// entryFor builds one agent's registration: its core.Manifest bytes
// (the description of the code — names, instructions, policy, tools),
// the alternate models the runtime allows, its own caps as the
// lower-only bounds, each tool's real side-effect class (its
// ReplayPolicy; unannotated is "never", WEFT-PLAYGROUND §6 rule 3),
// and the AllowSideEffects set.
func (r *registry) entryFor(a *core.Agent, name string) agentRegistration {
	manifest, err := core.Manifest(a)
	if err != nil {
		// Manifest only errors on nil/unnamed/duplicate agents; the
		// name is checked above, so this is unreachable in practice —
		// logged, never fatal (Install must not fail the program).
		slog.Warn("weft/runtime: manifest for agent failed", "agent", name, "err", err)
	}
	e := agentRegistration{
		Name:        name,
		Manifest:    string(manifest),
		SideEffects: map[string]string{},
	}
	for _, t := range a.Tools() {
		e.SideEffects[t.Name] = string(t.ReplayPolicy())
		if r.cfg.allow[t.Name] {
			e.Allow = append(e.Allow, t.Name)
		}
	}
	e.Limits = manifestLimits(manifest)
	e.Models = append(e.Models, r.modelNames()...)
	return e
}

// modelNames lists the allowed alternates, sorted for a deterministic
// payload (a map's iteration order must not reach the wire).
func (r *registry) modelNames() []string {
	names := make([]string, 0, len(r.cfg.models))
	for name := range r.cfg.models {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// manifestLimits reads the agent's own caps back out of its manifest
// policy — the lower-only bounds a command is validated against. The
// manifest is a description of the code (never configuration input);
// reading the caps here re-uses the one public description rather
// than a second source of truth. Missing fields read 0: no bound.
type manifestDoc struct {
	Agents []struct {
		Name  string `json:"name"`
		Model struct {
			Provider string `json:"provider"`
			Name     string `json:"name"`
		} `json:"model"`
		Policy struct {
			MaxSteps    int `json:"max_steps"`
			Parallelism int `json:"parallelism"`
		} `json:"policy"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	} `json:"agents"`
}

// manifestDoc parses one agent's manifest bytes.
func manifestLimits(manifest []byte) agentLimits {
	var doc manifestDoc
	if err := json.Unmarshal(manifest, &doc); err != nil || len(doc.Agents) == 0 {
		return agentLimits{}
	}
	return agentLimits{
		MaxSteps:    doc.Agents[0].Policy.MaxSteps,
		Parallelism: doc.Agents[0].Policy.Parallelism,
	}
}

// parseManifest is the manifest reader the validators share: names,
// the agent's own model, and the tool names.
func parseManifest(manifest []byte) (model string, tools []string, err error) {
	var doc manifestDoc
	if err := json.Unmarshal(manifest, &doc); err != nil {
		return "", nil, err
	}
	if len(doc.Agents) == 0 {
		return "", nil, errNoAgents
	}
	for _, t := range doc.Agents[0].Tools {
		tools = append(tools, t.Name)
	}
	return doc.Agents[0].Model.Name, tools, nil
}

var errNoAgents = &usageError{"weft/runtime: manifest carries no agent"}

// usageError is a plain error type for the small failures this module
// reports (kept local: nothing outside needs to match on them).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// registration renders the whole POST /api/runtime/register payload:
// who this runtime is (stable id, host, pid, service, env, weft
// version), its budget caps and thread availability, and its agents.
func (r *registry) registration(runtimeID string) registration {
	host, _ := os.Hostname()
	reg := registration{
		RuntimeID:   runtimeID,
		Host:        host,
		Pid:         os.Getpid(),
		Service:     serviceName(),
		Env:         os.Getenv("WEFT_ENV"),
		WeftVersion: weftVersion(),
		Budget: budgetWire{
			MaxTokensPerExperiment: r.cfg.budget.MaxTokensPerExperiment,
			MaxRunsPerExperiment:   r.cfg.budget.MaxRunsPerExperiment,
		},
		Threads: r.cfg.threads != nil,
	}
	for _, name := range r.order {
		reg.Agents = append(reg.Agents, r.entries[name])
	}
	return reg
}

// serviceName names the process the way observability would:
// OTEL_SERVICE_NAME when set, else the binary's name.
func serviceName() string {
	if s := os.Getenv("OTEL_SERVICE_NAME"); s != "" {
		return s
	}
	if len(os.Args) > 0 && os.Args[0] != "" {
		return filepath.Base(os.Args[0])
	}
	return ""
}

// agent returns the registered agent by name.
func (r *registry) agent(name string) (*core.Agent, bool) {
	a, ok := r.agents[name]
	return a, ok
}

// entry returns one agent's registration data.
func (r *registry) entry(name string) (agentRegistration, bool) {
	e, ok := r.entries[name]
	return e, ok
}

// allowedTools names the tools a playground run may really execute —
// the except-list of the run's default-deny park rule
// (core.ParkAllExcept, WEFT-PLAYGROUND §6 rule 3): the agent's tools
// whose code vouched core.Replay(core.ReplaySafe) and the
// structured-output submission of an agent built with core.Output (a
// submission is the run's answer, not a side effect), in every
// side_effects mode; and, only when the command asked for
// side_effects "allow" (allowMode), every name the runtime opted in
// with AllowSideEffects (registered on the agent or supplied later by a
// ToolSource — the rule matches by name). In "substitute" and "park" an
// opted-in tool is a side effect like any other: substituted from the
// record or parked. Everything else a run can reach parks: an
// unannotated tool, a tool only a ToolSource supplies, a Subagent
// child's own tools. Sorted; empty when nothing may run.
func (r *registry) allowedTools(agent string, allowMode bool) []string {
	e, ok := r.entries[agent]
	if !ok {
		return nil
	}
	set := map[string]bool{}
	for tool, class := range e.SideEffects {
		if class == string(core.ReplaySafe) || tool == outputTool {
			set[tool] = true
		}
	}
	if allowMode {
		for tool := range r.cfg.allow {
			set[tool] = true
		}
	}
	allowed := make([]string, 0, len(set))
	for tool := range set {
		allowed = append(allowed, tool)
	}
	sort.Strings(allowed) // a map's iteration order must not reach the run's options
	return allowed
}

// outputTool is the tool core.Output registers on an agent (the core's
// submit_output; its name is model-visible contract).
const outputTool = "submit_output"

// mayRunForReal reports whether tool may execute under side_effects
// "allow": it is on this agent's AllowSideEffects list, or it is not a
// side effect at all (vouched core.Replay(core.ReplaySafe), or an
// Output agent's submission) — those run in every mode, "allow"
// included.
func (e agentRegistration) mayRunForReal(tool string) bool {
	if e.SideEffects[tool] == string(core.ReplaySafe) || tool == outputTool {
		return true
	}
	for _, t := range e.Allow {
		if t == tool {
			return true
		}
	}
	return false
}

// ownModel returns the agent's own model name from its manifest.
func (r *registry) ownModel(agent string) string {
	e, ok := r.entries[agent]
	if !ok {
		return ""
	}
	model, _, err := parseManifest([]byte(e.Manifest))
	if err != nil {
		return ""
	}
	return model
}

// model resolves a display name from the allow-list.
func (r *registry) model(name string) (core.Model, bool) {
	m, ok := r.cfg.models[name]
	return m, ok
}

// budgetState is the per-experiment tally: tokens and runs this
// runtime already spent (§6 rule 6).
type budgetState struct {
	tokens int64
	runs   int64
}

// reserve counts one admitted command against its experiment's run
// cap. The count lands at admission, not at the run's end: a matrix
// dispatched all at once would otherwise pass the check sixty times
// before the first run finished and counted.
func (b *budgetState) reserve() { b.runs++ }

// spend records what one command's runs cost against its experiment —
// every run it made: a failed run's partial usage and every leg of a
// substitute chain included.
func (b *budgetState) spend(tokens int64) { b.tokens += tokens }

// over reports whether spending one more run of tokens would breach
// the caps. Zero caps never breach.
func (b *budgetState) over(cap Budget, nextTokens int64) bool {
	if cap.MaxTokensPerExperiment > 0 && b.tokens+nextTokens > cap.MaxTokensPerExperiment {
		return true
	}
	if cap.MaxRunsPerExperiment > 0 && b.runs+1 > cap.MaxRunsPerExperiment {
		return true
	}
	return false
}
