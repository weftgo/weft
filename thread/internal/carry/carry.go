// Package carry builds the context a run continues on when it runs on
// behalf of another one — a resume over a parked boundary, a deferred
// steer's follow-up, an async pool child: cancellation and deadline
// from one context, and, for the keys that context does not carry, the
// values of the context the work began on.
//
// The values are what the core and its callers place on a run's
// context for the runs started inside it — the ParkAllExcept list in
// force, the metadata, the delegating tool call — and none of them has
// an exported copy helper, so the context keeps them whole: the origin
// is consulted only for a key the live context lacks, and its
// cancellation never reaches the result.
package carry

import "context"

// Values returns a context that answers Deadline, Done and Err from
// live and Value from live first, then from origin. origin's
// cancellation does not reach it. A nil origin returns live itself.
func Values(live, origin context.Context) context.Context {
	if origin == nil {
		return live
	}
	return valuesCtx{Context: live, origin: context.WithoutCancel(origin)}
}

// valuesCtx is Values' context. origin is wrapped in WithoutCancel, so
// the standard library's own lookups through Value — the cancel
// propagation of a context derived from this one, context.Cause — find
// live's cancellation or none, never origin's.
type valuesCtx struct {
	context.Context
	origin context.Context
}

func (c valuesCtx) Value(key any) any {
	if v := c.Context.Value(key); v != nil {
		return v
	}
	return c.origin.Value(key)
}
