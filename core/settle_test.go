package core

// settleToolError's order, pinned where the scheduler cannot reorder
// it (P9-errortype-fix): this file is internal because the Timeout
// path's two branches race on an expired deadline, and only the
// settlement itself is deterministic.

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

// countedDeadlineErr matches context.DeadlineExceeded; its Error panics
// and is counted.
type countedDeadlineErr struct{ calls *atomic.Int32 }

func (e countedDeadlineErr) Error() string {
	e.calls.Add(1)
	panic("countedDeadlineErr.Error")
}
func (countedDeadlineErr) Is(target error) bool { return target == context.DeadlineExceeded }

// isPanicsErr's Is panics.
type isPanicsErr struct{}

func (isPanicsErr) Error() string { return "is" }
func (isPanicsErr) Is(error) bool { panic("isPanicsErr.Is") }

// The deadline fact comes first on the Timeout path: an error matching
// DeadlineExceeded under an expired deadline is settled expired —
// before the park check, with Error never called. Off the Timeout path
// (expired nil), or before the deadline, the park check comes first and
// a panicking Error is the handler's contained panic. A panic in Is,
// the first read, is the contained panic either way.
func TestSettleReadsTheDeadlineFirst(t *testing.T) {
	yes := func() bool { return true }
	no := func() bool { return false }
	parkAndDeadline := fmt.Errorf("%w: %w", ErrApprovalRequired, context.DeadlineExceeded)

	if f := settleToolError("x", parkAndDeadline, new(handlerMark), yes); !f.expired || !f.deadline || f.park || f.text != "" {
		t.Errorf("park+deadline, expired: %+v, want expired and nothing else read", f)
	}
	for name, expired := range map[string]func() bool{"no timeout": nil, "not expired": no} {
		if f := settleToolError("x", parkAndDeadline, new(handlerMark), expired); !f.park || f.expired {
			t.Errorf("park+deadline, %s: %+v, want parked", name, f)
		}
	}

	var calls atomic.Int32
	err := countedDeadlineErr{calls: &calls}
	if f := settleToolError("x", err, new(handlerMark), yes); !f.expired || calls.Load() != 0 {
		t.Errorf("Error-panicking deadline error, expired: %+v, %d Error calls; want expired, none", f, calls.Load())
	}
	mark := new(handlerMark)
	mark.returning(err)
	f := settleToolError("x", err, mark, no)
	if want := `tool "x" panicked: countedDeadlineErr.Error`; f.text != want || f.errType != "panic" || f.panicLayer != panicLayerHandler || f.expired {
		t.Errorf("Error-panicking deadline error, not expired: %+v, want %q from the handler", f, want)
	}

	for name, expired := range map[string]func() bool{"no timeout": nil, "expired": yes} {
		f := settleToolError("x", isPanicsErr{}, new(handlerMark), expired)
		if want := `tool "x" panicked: isPanicsErr.Is`; f.text != want || f.panicLayer != panicLayerMiddleware {
			t.Errorf("Is panics, %s: %+v, want %q from the middleware (the handler returned nothing)", name, f, want)
		}
	}
}
