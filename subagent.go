package weft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"sync/atomic"
)

// Codes the Subagent tool renders its failures with — model-visible
// contract, pinned by tests (ADR 0002's table; ADR 0014). Exported like
// the loop's own codes so policy middleware can branch on them.
const (
	// CodeSubagentFailed marks a child run that failed: its *RunError is
	// the cause, reachable through errors.As on ToolError.Err and never
	// shown to the model.
	CodeSubagentFailed = "SUBAGENT_FAILED"
	// CodeSubagentPending marks a child run that ended awaiting an
	// approval decision. Approvals belong in the orchestrator, not in a
	// child: the parent's transcript has nowhere to carry the child's
	// pending call, so the delegation is refused loudly instead of
	// silently (ADR 0014).
	CodeSubagentPending = "SUBAGENT_PENDING"
	// CodeSubagentCycle marks a delegation to an agent already running
	// in this call chain — refused before any model call.
	CodeSubagentCycle = "SUBAGENT_CYCLE"
)

// nest is how a dispatched tool call reports child-run events and usage
// to the run that dispatched it (ADR 0014). execTools puts one on every
// call's context — subagent or not — so the dispatcher stays ignorant of
// what a subagent is. emit wraps one child event in a Nested event
// numbered from the parent run's counter, under the parent's
// event-ordering lock; usage records into the step's per-call usage map.
// closed is the late-event rule (ADR 0004): set under the same lock as
// the call's ToolFinish, so no Nested event for the call is ever
// delivered after its finish — a replayed stream must not show a call
// continuing past its own completion. A nil nest — a handler invoked
// outside the loop — discards everything: the child simply runs, as any
// tool does.
type nest struct {
	emit   func(Event)
	usage  func(Usage)
	closed atomic.Bool
}

// sink returns the event sink a child run reports through. A nil nest
// (a handler invoked outside the loop) discards.
func (n *nest) sink() func(Event) {
	if n == nil {
		return func(Event) {}
	}
	return n.emit
}

// record adds u to the dispatching step's per-call usage. A nil nest
// drops it.
func (n *nest) record(u Usage) {
	if n != nil {
		n.usage(u)
	}
}

type nestKey struct{}

func withNest(ctx context.Context, n *nest) context.Context {
	return context.WithValue(ctx, nestKey{}, n)
}

func nestFromContext(ctx context.Context) (*nest, bool) {
	n, ok := ctx.Value(nestKey{}).(*nest)
	return n, ok
}

// ancestry carries the agents running above a context, root first.
// execute pushes itself on before running, so a Subagent handler can
// refuse a delegation that would recurse. Unexported, no accessor: the
// cycle guard is its one consumer.
type ancestryKey struct{}

func withAncestry(ctx context.Context, chain []*Agent) context.Context {
	return context.WithValue(ctx, ancestryKey{}, chain)
}

func ancestryOf(ctx context.Context) []*Agent {
	chain, _ := ctx.Value(ancestryKey{}).([]*Agent)
	return chain
}

// childRunID derives a child run's id from the parent call that owns
// it: <parent>/<step>/<callID> for a call dispatched by a step, and
// <parent>/resume/<callID> for one executed under Approve (resumed
// calls report Step 0; the literal segment keeps them from colliding
// with step 0's own calls). Deterministic and self-describing, so a
// child run's records are addressable under a stable key. Outside the
// loop there is no parent: the empty id makes runConfig.finish generate
// a fresh one.
func childRunID(c Call) string {
	if c.RunID == "" {
		return ""
	}
	seg := "resume"
	if !c.Approved {
		seg = strconv.Itoa(c.Step)
	}
	return c.RunID + "/" + seg + "/" + c.CallID
}

// usageOf returns a finished (or failed) run's total usage: the partial
// usage of a failed child counts toward its parent's bill, so the
// RunError's result is consulted when the run returned no result.
func usageOf(res *RunResult, err error) Usage {
	if res != nil {
		return res.Usage
	}
	var re *RunError
	if errors.As(err, &re) && re.Result != nil {
		return re.Result.Usage
	}
	return Usage{}
}

// submittedJSON returns the raw arguments of the last valid submit_output
// call — exactly the bytes OutputOf would decode, unre-marshalled, so a
// parent sees the child model's own bytes. ok is false when the run
// never submitted; the caller falls back to the child's final text.
func submittedJSON(res *RunResult) (json.RawMessage, bool) {
	if res == nil {
		return nil, false
	}
	for i := len(res.Steps) - 1; i >= 0; i-- {
		step := res.Steps[i]
		for j := len(step.ToolCalls) - 1; j >= 0; j-- {
			call := step.ToolCalls[j]
			if call.Name != outputToolName {
				continue
			}
			k := slices.IndexFunc(step.Results, func(r ToolResultPart) bool {
				return r.CallID == call.ID
			})
			if k >= 0 && !step.Results[k].IsError && len(call.Args) > 0 {
				return call.Args, true
			}
		}
	}
	return nil, false
}

// subagentInput is the argument schema of every Subagent tool: one
// prompt, stated in full, because the child sees nothing of the parent's
// conversation. The tool's description is the parent model's entire
// routing surface.
type subagentInput struct {
	Prompt string `json:"prompt" jsonschema:"The task, stated in full: the agent sees only this prompt, not the conversation."`
}

// Subagent defines a tool that delegates to another agent. The model
// calls it with one argument, prompt; the child runs on a fresh
// transcript holding only that prompt, on the parent call's context,
// and the tool's result is the child's final text (or, for a child
// built with Output, the submitted JSON). The child's events arrive in
// the parent's stream wrapped in Nested; its usage is added to the
// parent's RunResult.Usage and recorded on StepRecord.SubagentUsage.
//
// A subagent is an ordinary tool: Timeout bounds the child run,
// MaxResultBytes caps its answer, RequireApproval gates the delegation,
// Sequential makes it a barrier, WrapTools wraps the delegation once
// (the child's own seams govern inside it), and the manifest lists it.
//
// A child run that fails is a tool error the parent model sees
// (SUBAGENT_FAILED), never a parent run error; a child that ends
// awaiting approval is SUBAGENT_PENDING; a child that is already
// running above this call is refused with SUBAGENT_CYCLE. Subagent
// panics if child is nil.
func Subagent(name, description string, child *Agent, opts ...ToolOption) *ToolDef {
	if child == nil {
		panic(fmt.Sprintf("weft: Subagent %q called with a nil agent", name))
	}
	t := Tool(name, description, func(ctx context.Context, in subagentInput) (string, error) {
		if slices.Contains(ancestryOf(ctx), child) {
			return "", &ToolError{Code: CodeSubagentCycle,
				Message: fmt.Sprintf("agent %q is already running in this call chain", name)}
		}
		call, _ := CallFromContext(ctx)
		n, _ := nestFromContext(ctx)
		cfg := runConfig{id: childRunID(call), messages: []Message{User(in.Prompt)}}
		cfg.finish()
		res, err := child.execute(ctx, cfg, n.sink())
		n.record(usageOf(res, err))
		switch {
		case err != nil:
			re := &RunError{Err: err}
			errors.As(err, &re)
			return "", &ToolError{Code: CodeSubagentFailed,
				Message: fmt.Sprintf("agent %q failed at step %d: %v", name, re.Step, re.Err),
				Err:     err}
		case len(res.Pending) > 0:
			return "", &ToolError{Code: CodeSubagentPending,
				Message: fmt.Sprintf("agent %q ended awaiting approval of %d call(s)", name, len(res.Pending))}
		case child.hasOutput:
			if b, ok := submittedJSON(res); ok {
				return string(b), nil
			}
		}
		return res.Text(), nil
	}, opts...)
	// Tool recorded this frame as the source; the manifest should name
	// Subagent's caller, where the child is chosen (Output does the
	// same for its schema type).
	if _, file, line, ok := runtime.Caller(1); ok {
		t.sourceFile, t.sourceLine = file, line
	}
	t.subagent = child.name
	return t
}
