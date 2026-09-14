// Package mw holds the reference middleware for weft's two seams:
// model middleware (weft.WrapModel) and tool middleware
// (weft.WrapTools). Each is a small, dependency-free value that shows
// the shape of its seam; copy it, compose it, or write your own.
//
// Model seam — around every model call the loop makes:
//
//	weft.New(model, weft.WrapModel(
//	    mw.Log(logger),                 // outermost: sees retries and fallbacks
//	    mw.Fallback(backupModel),       // fail over once Retry has given up
//	    mw.Retry(mw.MaxRetries(3)),     // transient failures, backoff, retry-after
//	    mw.RepairJSON(),                // innermost: fixes truncated tool-call args
//	))
//
// Order matters: Fallback outside Retry retries the primary to exhaustion
// before switching models (retry, then fail over). The reverse — Retry
// outside Fallback — retries the fallback chain as a whole, so the primary
// gets exactly one attempt and Retry never sees its transient failures.
//
// Tool seam — around every tool call the loop dispatches:
//
//	weft.New(model, weft.WrapTools(
//	    mw.Audit(logger),               // observation: every call, with its cause
//	    mw.Allow(policy.Permits),       // decision: DENIED results the model sees
//	    mw.MapErrors(nil),              // shaping: plain errors → INTERNAL codes
//	), tools...)
//
// Middleware that verifies something (a user, a tenant, a quota) adds it
// to ctx before calling next, and tools read it back through a typed
// accessor — the "typed request context" convention documented in
// docs/life-of-a-call.md and weft's ExampleWrapTools_context.
package mw
