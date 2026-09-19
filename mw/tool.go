package mw

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/weftgo/weft"
)

// Allow gates every tool call on a predicate over its weft.Call. A
// denied call never runs: the model sees the error result `DENIED:
// tool "x" is not allowed` and the loop moves on — there is no retry,
// no prompt, no exception. Approval is a different seam
// (weft.RequireApproval): Allow is the fixed policy, approval the
// deferred decision. Outside the loop (Agent.CallTool with a bare
// ctx) the Call is built from the call part alone.
func Allow(permit func(weft.Call) bool) weft.ToolMiddleware {
	return func(next weft.ToolCaller) weft.ToolCaller {
		return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
			c, ok := weft.CallFromContext(ctx)
			if !ok {
				c = weft.Call{CallID: call.ID, Name: call.Name}
			}
			if permit != nil && !permit(c) {
				return "", &weft.ToolError{Code: weft.CodeDenied, Message: fmt.Sprintf("tool %q is not allowed", call.Name)}
			}
			return next(ctx, call)
		}
	}
}

// Audit logs one line per tool call at Info level on the given logger
// (slog.Default when nil): run, step, call id, tool, duration, and the
// outcome — the model-visible error text, plus the internal cause when
// the error is a *weft.ToolError with one. It changes nothing.
func Audit(l *slog.Logger) weft.ToolMiddleware {
	return func(next weft.ToolCaller) weft.ToolCaller {
		return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
			log := l
			if log == nil {
				log = slog.Default()
			}
			c, _ := weft.CallFromContext(ctx)
			start := time.Now()
			out, err := next(ctx, call)
			attrs := []any{
				"run", c.RunID,
				"step", c.Step,
				"call", call.ID,
				"tool", call.Name,
				"dur", time.Since(start),
			}
			switch {
			case err == nil:
				attrs = append(attrs, "result_bytes", len(out))
			case errors.Is(err, weft.ErrApprovalRequired):
				attrs = append(attrs, "pending", true)
			default:
				attrs = append(attrs, "err", err.Error())
				var te *weft.ToolError
				if errors.As(err, &te) && te.Err != nil {
					attrs = append(attrs, "cause", te.Err.Error())
				}
			}
			log.InfoContext(ctx, "tool call", attrs...)
			return out, err
		}
	}
}

// MapErrors shapes handler errors centrally before the loop renders
// them for the model. Errors that are already *weft.ToolError pass
// through untouched, as do context errors and ErrApprovalRequired (the
// loop reads those). Every other error goes through fn; a nil fn
// installs the default, which hides the text — database messages,
// stack fragments, wrapped internals — behind `INTERNAL: tool "x"
// failed` while keeping the original as the ToolError's cause for
// Audit and errors.As.
func MapErrors(fn func(error) error) weft.ToolMiddleware {
	return func(next weft.ToolCaller) weft.ToolCaller {
		return func(ctx context.Context, call weft.ToolCallPart) (string, error) {
			out, err := next(ctx, call)
			if err == nil || passThrough(err) {
				return out, err
			}
			if fn == nil {
				return out, &weft.ToolError{Code: "INTERNAL", Message: fmt.Sprintf("tool %q failed", call.Name), Err: err}
			}
			mapped := fn(err)
			if mapped == nil {
				mapped = err
			}
			return out, mapped
		}
	}
}

func passThrough(err error) bool {
	var te *weft.ToolError
	return errors.As(err, &te) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, weft.ErrApprovalRequired)
}
