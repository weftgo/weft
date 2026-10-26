package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
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
			return nil, fmt.Errorf("weft: Manifest: agent %d has no name (set one with the Name option)", i)
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
	// ModelMiddleware and ToolMiddleware are the onion (U23.1): the
	// WrapModel chain as its Unwrap walk reads it and the agent's
	// WrapTools list, outer to inner; Hooks the user functions the loop
	// calls. Each omitted when empty, so an agent without them renders
	// exactly as before.
	ModelMiddleware []MiddlewareInfo `json:"model_middleware,omitempty"`
	ToolMiddleware  []MiddlewareInfo `json:"tool_middleware,omitempty"`
	Hooks           *manifestHooks   `json:"hooks,omitempty"`
}

// manifestHooks are the agent's hook functions, each named as a tool
// middleware is (funcInfo), in registration order.
type manifestHooks struct {
	PrepareStep []MiddlewareInfo `json:"prepare_step,omitempty"`
	Tap         []MiddlewareInfo `json:"tap,omitempty"`
	OnRunEnd    []MiddlewareInfo `json:"on_run_end,omitempty"`
	ToolSource  *MiddlewareInfo  `json:"tool_source,omitempty"`
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
	ReplayPolicy    string  `json:"replay_policy,omitempty"` // safe; never is the default and omitted
	PromptSnippet   string  `json:"prompt_snippet,omitempty"`
	Source          string  `json:"source,omitempty"`
	// Subagent names the child agent of a Subagent tool, so Studio can
	// draw the delegation edge by name when both agents are in the
	// fleet. The manifest does not recurse into the child: it describes
	// the agents it was given, not the fleet (ADR 0012).
	Subagent string `json:"subagent,omitempty"`
	// Middleware is the tool's own WrapTools list, outer to inner; it
	// runs inside the agent's (U23.1).
	Middleware []MiddlewareInfo `json:"middleware,omitempty"`
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
		Tools:           make([]manifestTool, 0, len(a.toolList)),
		ModelMiddleware: modelLayers(a.model, a.base, len(a.modelMW)),
		ToolMiddleware:  toolLayers(a.toolMW),
	}
	hooks := manifestHooks{
		PrepareStep: funcInfos(a.prepare),
		Tap:         funcInfos(a.taps),
		OnRunEnd:    funcInfos(a.onRunEnd),
	}
	if a.toolSource != nil {
		ts := funcInfo(a.toolSource)
		hooks.ToolSource = &ts
	}
	if hooks.PrepareStep != nil || hooks.Tap != nil || hooks.OnRunEnd != nil || hooks.ToolSource != nil {
		ma.Hooks = &hooks
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
			Middleware:      toolLayers(t.mw),
		}
		if t.capSet {
			resultCap := t.resultCap
			mt.MaxResultBytes = &resultCap
		}
		// The replay class renders only when it is safe: never is the
		// default, so an unannotated tool keeps rendering exactly as
		// before (the committed weft.json must not churn).
		if t.ReplayPolicy() == ReplaySafe {
			mt.ReplayPolicy = string(ReplaySafe)
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
	return sourceRel(t.sourceFile, t.sourceFunc) + ":" + strconv.Itoa(t.sourceLine)
}

// sourceRel renders a source file for the manifest, never as an
// absolute path, so the document — and weft.manifest.hash — is the same
// on every machine (ADR 0012's 2026-10-26 amendment, U23.1-fix):
//
//   - a file in the module cache (…/pkg/mod/<module>@<version>/<dir>/f.go)
//     reads <module>/<dir>/f.go, the version dropped and the cache's
//     case escapes (!x) undone;
//   - a file under a go.mod reads relative to that module's root (the
//     nearest ancestor directory holding go.mod);
//   - anything else — a binary run where its sources are not, a build
//     host's path — reads as the function's import path and the file's
//     base name (github.com/x/y/f.go), or the base name alone when the
//     function is unknown.
//
// function is the runtime name of a function defined in file ("" when
// unknown). Separators are always "/".
func sourceRel(file, function string) string {
	slashed := filepath.ToSlash(file)
	if i := strings.LastIndex(slashed, "/pkg/mod/"); i >= 0 {
		rest := slashed[i+len("/pkg/mod/"):]
		if at := strings.Index(rest, "@"); at >= 0 {
			if slash := strings.Index(rest[at:], "/"); slash >= 0 {
				rest = rest[:at] + rest[at+slash:]
			} else {
				rest = rest[:at]
			}
		}
		return unescapeModPath(rest)
	}
	if rel, ok := moduleRel(file); ok {
		return rel
	}
	if pkg := funcPackage(function); pkg != "" {
		return pkg + "/" + path.Base(slashed)
	}
	return path.Base(slashed)
}

// moduleRel renders file relative to the nearest ancestor directory
// holding go.mod; ok is false when no ancestor does.
func moduleRel(file string) (string, bool) {
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if rel, err := filepath.Rel(dir, file); err == nil {
				return filepath.ToSlash(rel), true
			}
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// funcPackage is a runtime function name's import path:
// "github.com/x/y.F.func1" reads "github.com/x/y", "main.main" "main".
func funcPackage(function string) string {
	if function == "" {
		return ""
	}
	slash := strings.LastIndex(function, "/")
	if dot := strings.Index(function[slash+1:], "."); dot >= 0 {
		return function[:slash+1+dot]
	}
	return ""
}

// unescapeModPath undoes the module cache's case encoding: "!x" is "X".
func unescapeModPath(p string) string {
	if !strings.Contains(p, "!") {
		return p
	}
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] == '!' && i+1 < len(p) && p[i+1] >= 'a' && p[i+1] <= 'z' {
			b.WriteByte(p[i+1] - 'a' + 'A')
			i++
			continue
		}
		b.WriteByte(p[i])
	}
	return b.String()
}
