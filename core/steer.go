package core

import "context"

// SteerFunc returns the messages to deliver at a safe point, or nil. It
// must not block: drain a queue, do not wait on one — the loop calls it
// between steps, so a source that waits stalls the run. A source with
// nothing to deliver returns nil. A panic in a SteerFunc fails the run
// like a panic in a PrepareStep function (the deferred recover ends the
// run span and re-panics): it is arbitrary user code the caller owns.
//
// The returned messages become ordinary transcript messages — the
// transcript, the record, and the next turn all see them — and are
// reported as a Steered event. Ownership passes to the run, like the
// messages given to Messages: the source must not reuse or mutate them.
type SteerFunc func(ctx context.Context, at SteerPoint) []Message

// SteerPoint tells a steering source where the run is. RunID is the run
// being steered; Step is the step that just finished; Final is true when
// the run would otherwise end here, so a non-empty return redirects the
// run into one more step instead of ending it.
type SteerPoint struct {
	RunID string
	Step  int
	Final bool
}

type steeringOption SteerFunc

func (o steeringOption) applyRun(c *runConfig) { c.steer = SteerFunc(o) }

// Steering installs a steering source for this run: a pull hook the loop
// drains at two fixed points — after a step's tool batch, once every
// call of the batch has its result, and at a final step, where a
// delivered message redirects the run into one more step. Delivered
// messages are appended to the transcript before the next step's
// PrepareStep chain runs, so request rewrites see them, and reported as
// a Steered event between that step's StepFinish and the next StepStart.
//
// The hook is never drained when the run ends at the approval boundary
// or through a StopWhen condition: those ends stay ends, and the source
// keeps its messages for a follow-up. A redirect at a final point
// consumes a step and goes through the same continuation checks as any
// continuation (MaxSteps, UsageLimit, DetectLoops); if they fail, the
// steer is in RunError.Result.Messages, delivered but unanswered.
//
// It is a run option on purpose (ADR 0019): the run it steers is the
// one its queue belongs to, and a child run started by a Subagent tool
// does not inherit it — forwarding a steer to a child is a session
// decision made explicitly. A delivered message with any role other
// than RoleUser fails the run with ErrInvalidSteer: the model's own
// turns come from the model.
func Steering(fn SteerFunc) RunOption { return steeringOption(fn) }
