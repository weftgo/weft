package studio

import (
	"context"
	"errors"
	"slices"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/obsdb"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// The preview's scripted check (A6, UX-D-03, UX-C-06): before Run,
// whether the scripted engine holds a recorded answer for each step the
// replay would ask, computed from recorded facts and the registration —
// the scripted engine's own key (weft/runtime's scriptedKeyDoc: the
// messages verbatim, the tool names, thinking, tool choice, the
// sequential flag; never the system prompt), never by calling a model.
// The engine keys every recorded turn with the registered agent's tool
// list and with thinking, tool choice and sequential unset (the
// transcript does not record them), so a request that sets any of them,
// narrows the tools, or carries other messages misses. Computed
// whatever the engine, so a client can warn before one switches to
// scripted.

// scriptedStep is one step of scripted_hit: the source step (its own
// index), whether the scripted engine answers it, and the key
// components that differ when it does not.
type scriptedStep struct {
	Step    int      `json:"step"`
	Hit     bool     `json:"hit"`
	Because []string `json:"because"`
}

// scriptedMatch is scripted_key_match: ok when every step the replay
// reaches hits; else the first step that misses, its components, and
// Studio's sentence (the one a command's failed run reads,
// linkruntime.ReasonOf).
type scriptedMatch struct {
	OK      bool     `json:"ok"`
	Because []string `json:"because"`
	Step    *int     `json:"step"`
	Message string   `json:"message,omitempty"`
}

// scriptedInputs is what the check reads, gathered by assemblePreview.
type scriptedInputs struct {
	from      int
	own       []stepMessage
	will, was []core.Message
	willSend  previewRequest
	agent     *linkruntime.AgentRegistration
	req       runRequest
	// first is the request record from_step's prefix was taken from
	// (nil: none recorded); answering is each step's answering attempt.
	first     *obsdb.RequestRecord
	answering map[int]obsdb.RequestRecord
	// stoppedBy and pending are the source run row's: how it ended
	// decides whether the replay asks past its last recorded step.
	stoppedBy string
	pending   int
}

// checkScripted fills scripted_hit (from_step through the first miss:
// the run fails there, so no later step is asked) and
// scripted_key_match. The steps after from_step are predicted from the
// record: their messages are taken as the recording's when every
// earlier step hit and each recorded call names a tool the replay
// offers (a missing tool's result is an error: tools). That holds for
// certain only for a substituted call, whose recorded result is pasted
// in; a ReplaySafe or AllowSideEffects tool runs for real, and an
// answer other than the recorded one misses that step at run time
// though the check said hit. A source that ended on a tool call for a
// reason the replay does not repeat asks one step more (asksPastEnd):
// unanswered;
// their tools, thinking, tool choice and sequential flag are the first
// request's, except where the step's own request record says the
// agent's PrepareStep changed that part (it runs again at replay).
func checkScripted(in scriptedInputs) ([]scriptedStep, scriptedMatch) {
	index := in.indexTools()
	willTools := toolNameList(in.willSend.Tools)
	keep := onlyTools(in.req.Overrides)
	count := stepCount(in.own)
	steps := []scriptedStep{}
	missingCall := false // a recorded call before this step names a tool the replay does not offer
	for k := in.from; ; k++ {
		rec, recorded := in.answering[k]
		prepared := func(part string) bool { return recorded && slices.Contains(rec.Body.Prepared, part) }
		var because []string
		tools := willTools
		if prepared("tools") {
			tools = narrowNames(rec.Body.Tools.Names, keep)
		}
		if !sortedEqual(tools, index) || missingCall {
			because = append(because, "tools")
		}
		thinking := in.thinkingSet()
		if prepared("thinking") {
			thinking = rec.Body.Thinking != nil && (rec.Body.Thinking.Level != "" || rec.Body.Thinking.Budget != 0)
		}
		if thinking {
			because = append(because, "thinking")
		}
		choice := in.willSend.ToolChoice.Mode != "auto" || in.willSend.ToolChoice.Name != ""
		if prepared("tool_choice") {
			choice = rec.Body.ToolChoice != nil && (rec.Body.ToolChoice.Mode != "auto" && rec.Body.ToolChoice.Mode != "" || rec.Body.ToolChoice.Name != "")
		}
		if choice {
			because = append(because, "tool_choice")
		}
		sequential := in.sequential()
		if prepared("sequential_tools") {
			sequential = rec.Body.SequentialTools
		}
		if sequential {
			because = append(because, "sequential")
		}
		if k == in.from && !jsonEqual(in.will, in.was) {
			because = append(because, "messages")
		}
		if k >= count {
			because = append(because, "unanswered")
		}
		if because == nil {
			because = []string{}
		}
		steps = append(steps, scriptedStep{Step: k, Hit: len(because) == 0, Because: because})
		// After the last recorded step the replay asks one more only
		// when the source ended on a tool call (asksPastEnd).
		pastEnd := k+1 == count && in.asksPastEnd(count)
		if len(because) > 0 || k+1 >= count && !pastEnd {
			break
		}
		for _, m := range in.own {
			if m.step != k || m.msg.Role != core.RoleAssistant {
				continue
			}
			for _, p := range m.msg.Content {
				if c, ok := p.(core.ToolCallPart); ok && !slices.Contains(tools, c.Name) {
					missingCall = true
				}
			}
		}
	}
	last := steps[len(steps)-1]
	if last.Hit {
		return steps, scriptedMatch{OK: true, Because: []string{}}
	}
	step := last.Step
	return steps, scriptedMatch{Because: last.Because, Step: &step,
		Message: linkruntime.ScriptedMissReason(step, last.Because).Message}
}

// asksPastEnd reports whether a replay that hits every recorded step
// goes on to ask step count, which the source never answered: its last
// recorded step called tools and the source ended there for a reason
// the replay does not repeat (a failure, a cancel, a retry or usage
// limit — weft.run.stopped_by "", canceled, model_retries_exceeded,
// usage_limit), not at a boundary it does (parked, max_steps, a
// StopWhen condition), and the replay's max_steps leaves it a step.
func (in scriptedInputs) asksPastEnd(count int) bool {
	if count == 0 || in.pending > 0 {
		return false
	}
	switch in.stoppedBy {
	case "", "canceled", "model_retries_exceeded", "usage_limit":
	default:
		return false
	}
	if max := in.maxSteps(); max > 0 && count >= max {
		return false
	}
	for i := len(in.own) - 1; i >= 0; i-- {
		m := in.own[i]
		if m.msg.Role != core.RoleAssistant {
			continue
		}
		if m.step != count-1 {
			return false
		}
		return slices.ContainsFunc(m.msg.Content, func(p core.Part) bool {
			_, ok := p.(core.ToolCallPart)
			return ok
		})
	}
	return false
}

// maxSteps is the replay's step budget: the command's max_steps, else
// the registered agent's; 0 when neither is known.
func (in scriptedInputs) maxSteps() int {
	if v, ok := in.req.Overrides.Options["max_steps"]; ok && v > 0 {
		return int(v)
	}
	if in.agent != nil {
		return in.agent.Defaults.MaxSteps
	}
	return 0
}

// indexTools is the tool list the scripted engine keys the recording
// with: the registered agent's (the runtime builds it from the agent's
// tools), else — no registration — the recorded first request's
// catalog, the stand-in the preview's will_send uses too.
func (in scriptedInputs) indexTools() []string {
	if in.agent != nil {
		if names := in.agent.ManifestToolNames(); names != nil {
			return names
		}
	}
	if in.first != nil {
		return in.first.Body.Tools.Names
	}
	return nil
}

// thinkingSet is whether the replay's request carries a thinking
// config (the engine keys req.Thinking != {}): a level (the override,
// else the agent's default), or — with no override, which replaces the
// whole config with its level — the agent's default budget alone; no
// registration, the recorded first request's.
func (in scriptedInputs) thinkingSet() bool {
	if in.willSend.Thinking != "" {
		return true
	}
	if in.req.Overrides.Thinking != "" {
		return false
	}
	if in.agent != nil {
		return in.agent.Defaults.ThinkingBudget != 0
	}
	return in.first != nil && in.first.Body.Thinking != nil && in.first.Body.Thinking.Budget != 0
}

// sequential is the replay's SequentialTools (the loop's
// parallelism == 1): the command's parallelism option, else the
// registered agent's default, else the recorded first request's.
func (in scriptedInputs) sequential() bool {
	if v, ok := in.req.Overrides.Options["parallelism"]; ok {
		return v == 1
	}
	if in.agent != nil && in.agent.Defaults.Parallelism > 0 {
		return in.agent.Defaults.Parallelism == 1
	}
	return in.first != nil && in.first.Body.SequentialTools
}

// answeringRequests is each step's answering attempt (its highest
// index) among the run's request records, paged to the end.
func (s *Server) answeringRequests(ctx context.Context, run string) (map[int]obsdb.RequestRecord, error) {
	out := map[int]obsdb.RequestRecord{}
	q := obsdb.RequestQuery{Limit: maxRequestsLimit}
	for {
		page, err := s.db.Requests(ctx, run, q)
		if errors.Is(err, obsdb.ErrNotFound) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		for _, r := range page {
			if cur, ok := out[r.Step]; !ok || r.Index > cur.Index {
				out[r.Step] = r
			}
		}
		if len(page) < q.PageLimit() {
			return out, nil
		}
		q.From = page[len(page)-1].Index + 1
	}
}

func toolNameList(ts []previewTool) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

// narrowNames keeps the names in keep (nil keep: all).
func narrowNames(names, keep []string) []string {
	if keep == nil {
		return names
	}
	out := []string{}
	for _, n := range names {
		if slices.Contains(keep, n) {
			out = append(out, n)
		}
	}
	return out
}

// sortedEqual compares two name lists as sets in sorted order — the
// scripted key sorts its tool names.
func sortedEqual(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}
