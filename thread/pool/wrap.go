package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
)

// The model-visible texts the pool produces (AGENTS.md rule 5: a
// contract; ADR 0022's amendment lists them, TestModelVisibleTexts
// pins them byte for byte). Everything else a wrapped tool returns is
// the child's own answer.
const (
	// CodeSubagentDepth marks a delegation refused because it would
	// exceed the pool's MaxDepth — refused before any child is
	// created. The core has no depth limit (ADR 0014); the code is
	// the pool's.
	CodeSubagentDepth = "SUBAGENT_DEPTH"
	// CodeSubagentCanceled marks a sync delegation whose child the
	// pool canceled — Cancel on its receipt, or the pool's Close —
	// as against the parent's own cancellation, which ends the
	// parent's run instead.
	CodeSubagentCanceled = "SUBAGENT_CANCELED"

	// receiptLine is the result an async delegation returns (ADR 0022
	// §2): the receipt, not the answer.
	receiptLine = "background task accepted; receipt %s"
	// cycleMessage is the core's SUBAGENT_CYCLE message (subagent.go),
	// reported for cycles only: the agent is already running above
	// the call.
	cycleMessage = "agent %q is already running in this call chain"
	// depthMessage is the SUBAGENT_DEPTH message: the depth the
	// delegation would have, and the pool's limit.
	depthMessage = "delegation depth %d exceeds the pool's limit %d"
	// failedMessage is the core's SUBAGENT_FAILED message
	// (subagent.go), byte for byte — the pool replaces the mechanism,
	// not the contract; TestFailureTextMatchesCore holds the two
	// together.
	failedMessage = "agent %q failed at step %d: %v"
	// failedPlainMessage is SUBAGENT_FAILED for a delegation that
	// failed outside a child run — the pool's own bookkeeping, or a
	// failure recovered from the ledger after a restart, where no
	// step is known.
	failedPlainMessage = "agent %q failed: %s"
	// canceledMessage is the SUBAGENT_CANCELED message.
	canceledMessage = "agent %q was canceled before it finished"
	// unmirroredMessage is SUBAGENT_FAILED for a child that parked at
	// an approval the pool could not record on the parent session.
	unmirroredMessage = "agent %q parked at an approval the pool could not surface: %v"
	// cancelDenyReason is the deny reason a canceled, parked child's
	// pending calls are recorded with; the child's model would read
	// "DENIED: " + this if the session were ever resumed.
	cancelDenyReason = "the delegation was canceled while awaiting approval"
	// decodeMessage is the tool error for arguments that are not the
	// subagent schema's {"prompt": "..."}.
	decodeMessage = "thread/pool: decode %q arguments: %w"
	// unnamed stands in for the wrap name in the texts above when a
	// delegation has none — a Submit child whose parent call was
	// parked by a restart's recovery.
	unnamed = "delegate"
)

// metaAgent is the child-header metadata key recording the wrap name
// a child was made under — the resume key (ADR 0022 §7).
const metaAgent = "pool_agent"

// A WrapOption configures one wrapped delegation tool.
type WrapOption interface{ applyWrap(*wrapConfig) }

type wrapConfig struct {
	async    bool
	toolOpts []weft.ToolOption
}

type asyncOption bool

func (o asyncOption) applyWrap(c *wrapConfig) { c.async = bool(o) }

// Async makes the wrapped tool's result an acceptance receipt instead
// of the child's answer (ADR 0022 D1): the middleware submits the
// child and returns at once — the receipt line is the tool result,
// model-visible bytes pinned by a golden — and the answer is
// delivered by the application in a later turn. Without it the
// delegation is sync: the call waits for the child session's turn and
// the result is the child's answer, as an ordinary subagent's is.
func Async() WrapOption { return asyncOption(true) }

type toolOptionsOption struct{ opts []weft.ToolOption }

func (o toolOptionsOption) applyWrap(c *wrapConfig) { c.toolOpts = append(c.toolOpts, o.opts...) }

// ToolOptions forwards weft tool options to the underlying subagent
// tool — Timeout, MaxResultBytes, a snippet — which otherwise the
// wrap would hide. RequireApproval composes as on any tool: it gates
// the act of delegating (ADR 0014).
func ToolOptions(opts ...weft.ToolOption) WrapOption { return toolOptionsOption{opts} }

// Wrap returns a subagent tool whose children the pool runs (ADR 0022
// §1–§2): a delegation tool over agent, built on the core's Subagent,
// run as a child session of the session whose run is calling it —
// linked by lineage, receipted in the parent, bounded by the pool.
//
// Inside a session's run (the parent found through the run context)
// the child is a session of its own in the parent's storage: sync by
// default — the call waits and the result is the child's answer; with
// Async the result is the receipt and the child runs on. While a sync
// call waits, the run that made it holds no slot (see Pool). A
// wrapped tool called outside any session — a bare Generate — has no
// parent to receipt into and falls back to the ordinary subagent path
// under a slot, ADR 0014's semantics unchanged, with the same
// hand-off when such calls nest.
//
// What the calling model reads, besides the child's answer:
//
//   - SUBAGENT_CYCLE when agent is already running above the call —
//     the delegation chain's real ancestry, checked before any child
//     is created;
//   - SUBAGENT_DEPTH when the delegation would exceed MaxDepth;
//   - SUBAGENT_FAILED when the child's run fails, the core's text;
//   - SUBAGENT_CANCELED when the pool canceled the child (Cancel,
//     Close). The delegating call's own cancellation or timeout is
//     not that: the ordinary machinery reports it.
//
// name is also the resume key: children the wrap makes record it, and
// a restarted process that wraps the same agents under the same names
// can resume them. One name therefore names one agent — wrapping a
// different agent under a name the pool already holds fails with
// ErrDuplicateWrap; wrapping the same agent again returns another
// tool for it. A nil agent or an empty name is an error. MustWrap is
// Wrap for wiring that cannot fail.
func (p *Pool) Wrap(name, description string, agent *weft.Agent, opts ...WrapOption) (*weft.ToolDef, error) {
	if agent == nil {
		return nil, fmt.Errorf("thread/pool: Wrap %q with a nil agent", name)
	}
	if name == "" {
		return nil, fmt.Errorf("thread/pool: Wrap with an empty name")
	}
	var cfg wrapConfig
	for _, o := range opts {
		if o != nil {
			o.applyWrap(&cfg)
		}
	}
	p.mu.Lock()
	if held, ok := p.nameAgents[name]; ok && held != agent {
		p.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrDuplicateWrap, name)
	}
	p.nameAgents[name] = agent
	p.mu.Unlock()
	mw := func(next weft.ToolCaller) weft.ToolCaller {
		return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
			parent := thread.SessionFromContext(ctx)
			if parent == nil {
				return p.bare(ctx, call, next)
			}
			var in struct {
				Prompt string `json:"prompt"`
			}
			if err := json.Unmarshal(call.Args, &in); err != nil {
				return "", fmt.Errorf(decodeMessage, name, err)
			}
			info, err := p.admit(ctx, agent)
			switch {
			case errors.Is(err, ErrCycle):
				return "", &weft.ToolError{Code: weft.CodeSubagentCycle,
					Message: fmt.Sprintf(cycleMessage, name), Err: err}
			case errors.Is(err, ErrDepth):
				return "", &weft.ToolError{Code: CodeSubagentDepth,
					Message: fmt.Sprintf(depthMessage, runFromContext(ctx).depth+1, p.maxDepth), Err: err}
			case err != nil:
				return "", err
			}
			if cfg.async {
				r, _, err := p.submit(ctx, parent, agent, in.Prompt, call.ID, name, info, true)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf(receiptLine, r.ID), nil
			}
			// The hand-off: the run making this call gives its slot
			// up for the wait and takes one back — through the queue
			// — before it works again.
			mine := runFromContext(ctx).slot
			mine.yield()
			r, out, err := p.submit(ctx, parent, agent, in.Prompt, call.ID, name, info, false)
			regained := mine.regain()
			switch {
			case err != nil:
				return "", err
			case out.parked:
				// Park the parent's call (ADR 0022 §7): the child's
				// requests are mirrored durable on the parent, and
				// this call completes — resolved with the child's
				// answer — when they are decided. The park is the one
				// rule tool middleware already had (ADR 0007).
				return "", fmt.Errorf("thread/pool: agent %q awaits approval in child session %s: %w",
					name, r.Child, weft.ErrApprovalRequired)
			case out.err != nil:
				return "", delegationError(name, out)
			case regained != nil:
				// The calling run was canceled while it queued for
				// its slot: its own cancellation, reported as such.
				return "", regained
			}
			return out.answer, nil
		}
	}
	// The middleware wraps the subagent tool's own chain from the
	// outside (a tool-level WrapTools, ADR 0006): agent middleware →
	// this wrap → the tool's own options → handler.
	all := append([]weft.ToolOption{weft.WrapTools(mw)}, cfg.toolOpts...)
	return weft.Subagent(name, description, agent, all...), nil
}

// MustWrap is Wrap for package-level and constructor wiring, where
// the arguments are the program's own: it panics on the errors Wrap
// returns — a nil agent, an empty name, a name already wrapping a
// different agent.
func (p *Pool) MustWrap(name, description string, agent *weft.Agent, opts ...WrapOption) *weft.ToolDef {
	t, err := p.Wrap(name, description, agent, opts...)
	if err != nil {
		panic(err)
	}
	return t
}

// delegationError renders a failed sync delegation as the error the
// wrapped tool returns: a coded ToolError the model reads, or — for
// the delegating call's own cancellation or timeout — the context
// error raw, which the ordinary machinery renders (ADR 0014's rule).
func delegationError(name string, out outcome) error {
	var um *unmirroredError
	var re *weft.RunError
	switch {
	case errors.As(out.err, &um):
		return &weft.ToolError{Code: weft.CodeSubagentFailed,
			Message: fmt.Sprintf(unmirroredMessage, name, um.err), Err: out.err}
	case out.canceled:
		return &weft.ToolError{Code: CodeSubagentCanceled,
			Message: fmt.Sprintf(canceledMessage, name), Err: out.err}
	case errors.Is(out.err, context.Canceled), errors.Is(out.err, context.DeadlineExceeded):
		return out.err
	case errors.As(out.err, &re):
		return &weft.ToolError{Code: weft.CodeSubagentFailed,
			Message: fmt.Sprintf(failedMessage, name, re.Step, re.Err), Err: out.err}
	}
	return &weft.ToolError{Code: weft.CodeSubagentFailed,
		Message: fmt.Sprintf(failedPlainMessage, name, out.err.Error()), Err: out.err}
}

// bare runs a wrapped call made outside any session — a bare
// Generate, no parent to receipt into — as the ordinary subagent call
// it is, under a slot (ADR 0022 §2). It is pool work like any other:
// a closed pool refuses it with ErrClosed, Close cancels it and waits
// for it, and when such calls nest the outer one hands its slot back
// for the wait, exactly as a session-run delegation does.
func (p *Pool) bare(ctx context.Context, call weft.ToolCallPart, next weft.ToolCaller) (string, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return "", ErrClosed
	}
	p.wg.Add(1)
	id := p.nextCall
	p.nextCall++
	ctx, cancel := context.WithCancel(ctx)
	p.calls[id] = cancel
	p.mu.Unlock()
	defer func() {
		cancel()
		p.mu.Lock()
		delete(p.calls, id)
		p.mu.Unlock()
		p.wg.Done()
	}()

	above := runFromContext(ctx)
	above.slot.yield()
	if err := p.sem.enqueue().wait(ctx); err != nil {
		_ = above.slot.regain() // the outer run's own wait; its error is its run's
		return "", err
	}
	sl := &slot{f: p.sem, ctx: ctx, held: true}
	res, err := next(withRun(ctx, runInfo{depth: above.depth, chain: above.chain, slot: sl}), call)
	sl.finish()
	if rerr := above.slot.regain(); rerr != nil && err == nil {
		return "", rerr
	}
	return res, err
}

// runInfo is what a pool child's run context carries for the
// delegations that run makes: its depth in the delegation chain, the
// agents above it, and its slot.
type runInfo struct {
	// depth is how many pool delegations stand between a top-level
	// run and this one: 1 for a child, 2 for its child.
	depth int
	// chain holds the agents whose runs delegated down to this one,
	// root first — the cycle guard's ancestry. The run's own agent is
	// read off the context when it delegates.
	chain []*weft.Agent
	slot  *slot
}

type runKey struct{}

func withRun(ctx context.Context, info runInfo) context.Context {
	return context.WithValue(ctx, runKey{}, info)
}

func runFromContext(ctx context.Context) runInfo {
	info, _ := ctx.Value(runKey{}).(runInfo)
	return info
}
