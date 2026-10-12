package core

import "context"

// CancelCause is a typed cancellation cause: what a layer passes to the
// CancelCauseFunc of a context.WithCancelCause it fells a run with, so
// the run's record says who cancelled it. Its value is a short,
// low-cardinality dotted name — "thread.interrupted", "studio.cancel" —
// recorded as the invoke_agent span's weft.cancel.cause when the run
// ends canceled (ADR 0016, amendment 2026-10-12).
//
// The loop reads context.Cause(ctx) and records any cause with a
// Kind() string method, so a layer may bring its own type; CancelCause
// is the one weft's own layers use. A cause the loop cannot name is
// not recorded, except a deadline, which reads "deadline". It matches
// context.Canceled under errors.Is: a CancelCause is a cancellation.
type CancelCause string

// Kind returns the cause's name, the weft.cancel.cause value.
func (c CancelCause) Kind() string { return string(c) }

// Error makes the cause an error, as context.WithCancelCause wants.
func (c CancelCause) Error() string { return "context canceled: " + string(c) }

// Is reports a CancelCause as context.Canceled.
func (c CancelCause) Is(target error) bool { return target == context.Canceled }
