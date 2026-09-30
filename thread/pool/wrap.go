package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
)

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

// Wrap returns a subagent tool whose child runs hold one pool slot
// (ADR 0022 §1–§2): a delegation tool over agent, built on the core's
// Subagent, run by the pool as a child session of the session whose
// run is calling it — linked by lineage, receipted in the parent, and
// bounded by the semaphore.
//
// Inside a session's run (the parent found through the run context)
// the child is a session of its own in the parent's storage: sync by
// default — the call waits and the result is the child's answer; with
// Async the result is the receipt and the child runs on. A wrapped
// tool called outside any session — a bare Generate — has no parent
// to receipt into and falls back to the ordinary subagent path under
// the slot, ADR 0014's semantics unchanged.
//
// A delegation chain deeper than the pool's max is refused with the
// core's SUBAGENT_CYCLE code before any child starts: every sync
// level holds a slot while it waits, so a chain of max+1 could only
// deadlock — the pool's bound doubles as the depth guard the core
// deliberately left unset (ADR 0014).
func (p *Pool) Wrap(name, description string, agent *weft.Agent, opts ...WrapOption) *weft.ToolDef {
	if agent == nil {
		panic(fmt.Sprintf("thread/pool: Wrap %q called with a nil agent", name))
	}
	var cfg wrapConfig
	for _, o := range opts {
		if o != nil {
			o.applyWrap(&cfg)
		}
	}
	// The wrap's name is the resume key: children it makes record the
	// name in their header metadata, and a restarted process that
	// re-Wraps the same names restores the register (ADR 0022 §7).
	p.mu.Lock()
	p.nameAgents[name] = agent
	p.mu.Unlock()
	mw := func(next weft.ToolCaller) weft.ToolCaller {
		return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
			parent := thread.SessionFromContext(ctx)
			if parent == nil {
				// No session run above: bound the ordinary subagent
				// call with the slot and let it run as-is.
				if err := p.acquire(ctx); err != nil {
					return "", err
				}
				defer p.release()
				return next(ctx, call)
			}
			var in struct {
				Prompt string `json:"prompt"`
			}
			if err := json.Unmarshal(call.Args, &in); err != nil {
				return "", fmt.Errorf("thread/pool: decode %q arguments: %w", name, err)
			}
			if depth := depthFromContext(ctx); depth >= p.max {
				return "", &weft.ToolError{Code: weft.CodeSubagentCycle,
					Message: fmt.Sprintf("agent %q is already running in this call chain (pool depth %d of %d)",
						name, depth, p.max)}
			}
			if cfg.async {
				r, _, err := p.submit(ctx, parent, agent, in.Prompt, call.ID, name, depthFromContext(ctx)+1, true)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf(receiptLine, r.ID), nil
			}
			r, out, err := p.submit(ctx, parent, agent, in.Prompt, call.ID, name, depthFromContext(ctx)+1, false)
			if err != nil {
				return "", err
			}
			switch {
			case out.pending > 0:
				// Park the parent's call (ADR 0022 §7): the child's
				// requests are mirrored durable on the parent, and
				// this call completes — resolved with the child's
				// answer — when they are decided. The park is the one
				// rule tool middleware already had (ADR 0007).
				return "", fmt.Errorf("thread/pool: agent %q awaits approval in child session %s: %w",
					name, r.Child, weft.ErrApprovalRequired)
			case out.err != nil:
				if errors.Is(out.err, context.Canceled) || errors.Is(out.err, context.DeadlineExceeded) {
					// The parent's own cancellation or the tool's
					// timeout: return it raw, the ordinary machinery
					// renders the result (ADR 0014's rule).
					return "", out.err
				}
				var re *weft.RunError
				if !errors.As(out.err, &re) {
					// The child failed outside a run: its session
					// refused the prompt, or the delegation panicked.
					return "", &weft.ToolError{Code: weft.CodeSubagentFailed,
						Message: fmt.Sprintf("agent %q failed: %v", name, out.err),
						Err:     out.err}
				}
				return "", &weft.ToolError{Code: weft.CodeSubagentFailed,
					Message: fmt.Sprintf("agent %q failed at step %d: %v", name, re.Step, re.Err),
					Err:     out.err}
			}
			return out.answer, nil
		}
	}
	// The middleware wraps the subagent tool's own chain from the
	// outside (a tool-level WrapTools, ADR 0006): agent middleware →
	// this wrap → the tool's own options → handler.
	all := append([]weft.ToolOption{weft.WrapTools(mw)}, cfg.toolOpts...)
	return weft.Subagent(name, description, agent, all...)
}

// depthKey carries the delegation chain's depth on the context the
// child runs under, so a child that itself delegates measures its own
// chain against the pool's bound.
type depthKey struct{}

func withDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, depthKey{}, depth)
}

func depthFromContext(ctx context.Context) int {
	d, _ := ctx.Value(depthKey{}).(int)
	return d
}
