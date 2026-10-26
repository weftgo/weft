package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// keptPoll is a poller's turn: the same poll_job call at every step.
func keptPoll(n int) []wefttest.Turn {
	turns := make([]wefttest.Turn, n)
	for i := range turns {
		turns[i] = wefttest.ToolCalls(wefttest.Call{Name: "poll_job", Args: `{"job_id":"J-7"}`, ID: "call_1"})
	}
	return turns
}

var keptPollTool = core.Tool("poll_job", "Check a batch job's status.", func(context.Context, struct {
	JobID string `json:"job_id"`
}) (string, error) {
	return "status: pending — poll again with the same job_id", nil
})

// keptRun runs the turn from step n: the source's input and steps
// 0..n-1 as the kept prefix, KeptSteps(n), the rest scripted.
func keptRun(t *testing.T, src []core.Message, n int, turns []wefttest.Turn, opts ...core.Option) (*core.RunError, error) {
	t.Helper()
	agt := core.New(wefttest.Script(turns...), append([]core.Option{keptPollTool}, opts...)...)
	_, err := agt.Generate(context.Background(), core.Messages(src[:1+2*n]...), core.KeptSteps(n))
	var re *core.RunError
	if !errors.As(err, &re) {
		t.Fatalf("from step %d: err = %v, want a RunError", n, err)
	}
	return re, err
}

// TestKeptStepsReachTheTurnsLoopStop (P10a-gate-fix1): a turn stopped
// by DetectLoops at step 2, continued from step 1 or 2 with the kept
// steps counted (KeptSteps), stops the same way at the same ordinal —
// the streak runs over the kept steps' tool calls — with the same
// transcript; without KeptSteps it would ask past the stop.
func TestKeptStepsReachTheTurnsLoopStop(t *testing.T) {
	src := core.New(wefttest.Script(keptPoll(3)...), keptPollTool, core.DetectLoops(3))
	_, err := src.Generate(context.Background(), core.Prompt("poll J-7 until done"))
	var sre *core.RunError
	if !errors.As(err, &sre) || !errors.Is(err, core.ErrLoopDetected) || sre.Step != 2 {
		t.Fatalf("source = %v, want loop_detected at step 2", err)
	}
	for _, n := range []int{1, 2} {
		re, err := keptRun(t, sre.Result.Messages, n, keptPoll(3-n), core.DetectLoops(3))
		if !errors.Is(err, core.ErrLoopDetected) || re.Step != 2-n || len(re.Result.Steps) != 3-n {
			t.Errorf("from step %d: %v (own step %d, %d steps), want loop_detected at own step %d after %d steps",
				n, err, re.Step, len(re.Result.Steps), 2-n, 3-n)
		}
		if !slices.EqualFunc(re.Result.Messages, sre.Result.Messages, func(a, b core.Message) bool { return a.Role == b.Role && len(a.Content) == len(b.Content) }) {
			t.Errorf("from step %d: transcript %d messages, want the source's %d", n, len(re.Result.Messages), len(sre.Result.Messages))
		}
	}
	// The control: the same continuation without KeptSteps asks a step
	// the turn never made (its script runs out).
	agt := core.New(wefttest.Script(keptPoll(2)...), keptPollTool, core.DetectLoops(3))
	_, err = agt.Generate(context.Background(), core.Messages(sre.Result.Messages[:3]...))
	if errors.Is(err, core.ErrLoopDetected) {
		t.Errorf("without KeptSteps the continuation stopped on the loop: %v", err)
	}
}

// TestKeptStepsSpendTheTurnsStepBudget: MaxSteps budgets the whole turn
// — a turn stopped by MaxSteps(2) at step 2, continued from step 1,
// makes one model call and stops max_steps; from step 2, none.
func TestKeptStepsSpendTheTurnsStepBudget(t *testing.T) {
	src := core.New(wefttest.Script(keptPoll(3)...), keptPollTool, core.MaxSteps(2))
	_, err := src.Generate(context.Background(), core.Prompt("poll J-7"))
	var sre *core.RunError
	if !errors.As(err, &sre) || !errors.Is(err, core.ErrMaxSteps) || sre.Step != 2 {
		t.Fatalf("source = %v, want max_steps at step 2", err)
	}
	for _, n := range []int{1, 2} {
		re, err := keptRun(t, sre.Result.Messages, n, keptPoll(3), core.MaxSteps(2))
		if !errors.Is(err, core.ErrMaxSteps) || re.Step != 2-n || len(re.Result.Steps) != 2-n {
			t.Errorf("from step %d: %v (own step %d, %d steps), want max_steps after %d steps", n, err, re.Step, len(re.Result.Steps), 2-n)
		}
	}
	// A larger budget leaves the continuation what the kept steps did not spend.
	re, err := keptRun(t, sre.Result.Messages, 1, keptPoll(3), core.MaxSteps(4))
	if errors.Is(err, core.ErrMaxSteps) && len(re.Result.Steps) == 3 {
		return
	}
	t.Errorf("MaxSteps(4) from step 1: %v after %d steps, want max_steps after 3", err, len(re.Result.Steps))
}
