package weft

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Manifest renders the agents as their `weft.json` document: one
// generated, committed, diffable description of every agent and tool.
// Studio, docs, review, and compatibility checks read a file instead of
// a live process; the code stays the only source of truth. The file is
// output, never input — nothing is configured from it. Generate it in a
// golden test (wefttest.Golden) so a stale file fails `go test`; run
// that test with `-update` to regenerate.
//
// Tools are listed in registration order, agents in argument order, and
// encoding/json sorts map keys, so the bytes are deterministic for the
// same agents. Unnamed and duplicate agent names are errors: the file
// is a review artifact, and `agent_1` in a diff is noise.
func Manifest(agents ...*Agent) ([]byte, error) {
	names := map[string]bool{}
	doc := manifestDoc{Version: 1, Agents: make([]manifestAgent, 0, len(agents))}
	for i, a := range agents {
		if a == nil {
			return nil, fmt.Errorf("weft: Manifest: agent %d is nil", i)
		}
		if a.name == "" {
			return nil, fmt.Errorf("weft: Manifest: agent %d has no name (set one with weft.Name)", i)
		}
		if names[a.name] {
			return nil, fmt.Errorf("weft: Manifest: duplicate agent name %q", a.name)
		}
		names[a.name] = true
		doc.Agents = append(doc.Agents, a.manifestEntry())
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

type manifestDoc struct {
	Version int             `json:"weft"`
	Agents  []manifestAgent `json:"agents"`
}

type manifestAgent struct {
	Name         string         `json:"name"`
	Model        ModelInfo      `json:"model"`
	Instructions string         `json:"instructions,omitempty"`
	Policy       manifestPolicy `json:"policy"`
	Tools        []manifestTool `json:"tools"`
}

type manifestPolicy struct {
	Parallelism    int            `json:"parallelism"`
	MaxSteps       int            `json:"max_steps"`
	MaxResultBytes int            `json:"max_result_bytes"`
	Timeout        string         `json:"timeout,omitempty"`
	StrictInput    bool           `json:"strict_input,omitempty"`
	StopWhen       []string       `json:"stop_when,omitempty"`
	UsageLimit     *manifestUsage `json:"usage_limit,omitempty"`
	// MaxModelRetries is always written, like MaxSteps: it has a
	// non-zero default the manifest should not hide.
	MaxModelRetries int `json:"max_model_retries"`
	DetectLoops     int `json:"detect_loops,omitempty"`
}

// manifestUsage is UsageLimit's policy record: zero fields are omitted,
// and no limit at all omits the whole object.
type manifestUsage struct {
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
}

// manifestTool carries the tool's contract and, when set, its own
// policy: the keys appear only for tools that override the agent's
// defaults, so a tool without options renders exactly as before.
type manifestTool struct {
	Name            string  `json:"name"`
	Description     string  `json:"description,omitempty"`
	InputSchema     *Schema `json:"input_schema,omitempty"`
	OutputSchema    *Schema `json:"output_schema,omitempty"`
	Timeout         string  `json:"timeout,omitempty"`
	MaxResultBytes  *int    `json:"max_result_bytes,omitempty"`
	StrictInput     bool    `json:"strict_input,omitempty"`
	Sequential      bool    `json:"sequential,omitempty"`
	RequireApproval bool    `json:"require_approval,omitempty"`
	PromptSnippet   string  `json:"prompt_snippet,omitempty"`
	Source          string  `json:"source,omitempty"`
	// Subagent names the child agent of a Subagent tool, so Studio can
	// draw the delegation edge by name when both agents are in the
	// fleet. The manifest does not recurse into the child: it describes
	// the agents it was given, not the fleet (ADR 0012).
	Subagent string `json:"subagent,omitempty"`
}

func (a *Agent) manifestEntry() manifestAgent {
	ma := manifestAgent{
		Name:         a.name,
		Model:        a.modelInfo(),
		Instructions: a.system,
		Policy: manifestPolicy{
			Parallelism:     a.parallelism,
			MaxSteps:        a.maxSteps,
			MaxResultBytes:  a.resultCap,
			Timeout:         durationName(a.toolTimeout),
			StrictInput:     a.strict,
			MaxModelRetries: a.maxModelRetries,
			DetectLoops:     a.detectLoops,
		},
		Tools: make([]manifestTool, 0, len(a.toolList)),
	}
	if a.usageLimit != (Usage{}) {
		ma.Policy.UsageLimit = &manifestUsage{
			InputTokens:  a.usageLimit.InputTokens,
			OutputTokens: a.usageLimit.OutputTokens,
		}
	}
	for _, c := range a.stops {
		ma.Policy.StopWhen = append(ma.Policy.StopWhen, stopName(c))
	}
	for _, t := range a.toolList {
		mt := manifestTool{
			Name:            t.Name,
			Description:     t.Description,
			InputSchema:     t.InputSchema,
			OutputSchema:    t.OutputSchema,
			Timeout:         toolTimeoutName(t),
			StrictInput:     t.strict,
			Sequential:      t.sequential,
			RequireApproval: t.approval,
			PromptSnippet:   t.snippet,
			Source:          toolSource(t),
			Subagent:        t.subagent,
		}
		if t.capSet {
			resultCap := t.resultCap
			mt.MaxResultBytes = &resultCap
		}
		ma.Tools = append(ma.Tools, mt)
	}
	return ma
}

// durationName renders a timeout for the manifest, "" when unset.
func durationName(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

// toolTimeoutName renders a tool's timeout for the manifest. A set
// timeout renders even when zero: "0s" records an explicit lift of the
// agent's default — the per-tool analogue of max_result_bytes: 0.
func toolTimeoutName(t *ToolDef) string {
	if t.timeoutSet {
		return t.timeout.String()
	}
	return ""
}

// stopName renders a stop condition for the manifest: the built-ins
// name themselves (HasToolCall, StepCountIs implement fmt.Stringer);
// anything else is "custom" — Go cannot recover a closure's captured
// arguments.
func stopName(c StopCondition) string {
	if s, ok := c.(interface{ String() string }); ok {
		if name := s.String(); name != "" {
			return name
		}
	}
	return "custom"
}

// toolSource renders the tool's defining call site as file:line, or ""
// for a ToolDef that did not come through Tool (a RawTool, a literal
// ToolDef) — the key is then omitted rather than rendered as ":0".
func toolSource(t *ToolDef) string {
	if t.sourceFile == "" {
		return ""
	}
	return moduleRel(t.sourceFile) + ":" + strconv.Itoa(t.sourceLine)
}

// moduleRel renders file relative to the module root — the nearest
// ancestor directory holding go.mod — so a committed manifest is
// machine-independent. A path outside any module (the module cache, a
// sibling checkout) keeps its absolute form; the manifest is then
// machine-dependent for out-of-module tools, which is accepted and
// recorded (ADR 0012). Separators are always "/", so the committed file
// does not depend on the generating OS.
func moduleRel(file string) string {
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if rel, err := filepath.Rel(dir, file); err == nil {
				return filepath.ToSlash(rel)
			}
			return filepath.ToSlash(file)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.ToSlash(file)
		}
		dir = parent
	}
}
