package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// echoTool is the steering tests' ordinary tool: one call, one result.
func echoTool() *core.ToolDef {
	return core.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
}

// steerLog records every drain point a SteerFunc sees, thread-safely —
// the loop calls it on the run goroutine, but tests read it after Wait.
type steerLog struct {
	points []core.SteerPoint
	calls  int
}

func (l *steerLog) fn(_ context.Context, at core.SteerPoint) []core.Message {
	l.calls++
	l.points = append(l.points, at)
	return nil
}

// A steer delivered after a tool batch is an ordinary user message in
// the transcript and in the next model request — appended after the
// tool message, before the next step's PrepareStep chain (ADR 0019 §3).
func TestSteeringDeliversAfterToolBatch(t *testing.T) {
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("done"),
	)
	steer := wefttest.NewSteers().At(0, core.User("switch to metric units"))
	agt := core.New(model, echoTool())
	res, err := agt.Generate(context.Background(), core.Prompt("convert this"), steer.Option())
	if err != nil {
		t.Fatal(err)
	}
	// The transcript: prompt, assistant call, tool result, steer, reply.
	wantRoles := []core.Role{core.RoleUser, core.RoleAssistant, core.RoleTool, core.RoleUser, core.RoleAssistant}
	gotRoles := make([]core.Role, len(res.Messages))
	for i, m := range res.Messages {
		gotRoles[i] = m.Role
	}
	if !slices.Equal(gotRoles, wantRoles) {
		t.Fatalf("transcript roles = %v, want %v", gotRoles, wantRoles)
	}
	if got := res.Messages[3].Text(); got != "switch to metric units" {
		t.Errorf("steer message = %q, want the delivered text", got)
	}
	// The model saw exactly the same placement.
	reqs := model.Requests()
	if len(reqs) != 2 {
		t.Fatalf("model called %d times, want 2", len(reqs))
	}
	second := reqs[1].Messages
	if last := second[len(second)-1]; last.Role != core.RoleUser || last.Text() != "switch to metric units" {
		t.Errorf("next request ends with %+v, want the steer", last)
	}
	if n := second[len(second)-2]; n.Role != core.RoleTool {
		t.Errorf("message before the steer = %v, want the tool message (call/result pairing intact)", n.Role)
	}
}

// A steer at a final step redirects the run into one more step instead
// of ending it (ADR 0019 §2.3) — the pi/Pydantic redirect.
func TestSteeringFinalStepRedirects(t *testing.T) {
	model := wefttest.Script(wefttest.Say("done"), wefttest.Say("done, metric"))
	steer := wefttest.NewSteers().At(0, core.User("actually, metric units"))
	agt := core.New(model)
	res, err := agt.Generate(context.Background(), core.Prompt("convert this"), steer.Option())
	if err != nil {
		t.Fatal(err)
	}
	if res.NumSteps() != 2 {
		t.Fatalf("steps = %d, want the redirect to spend one more", res.NumSteps())
	}
	if res.Text() != "done, metric" {
		t.Errorf("final text = %q, want the second answer", res.Text())
	}
	if got := res.Messages[2]; got.Role != core.RoleUser || got.Text() != "actually, metric units" {
		t.Errorf("redirect message in transcript = %+v, want the steer between the answers", got)
	}
}

// The approval boundary is never drained: a run that parks calls keeps
// its messages in the source, for after the decision or a follow-up
// (ADR 0019 §2.1).
func TestSteeringNotDrainedAtApprovalBoundary(t *testing.T) {
	var log steerLog
	pay := core.Tool("pay", "", func(_ context.Context, _ struct{}) (string, error) {
		return "paid", nil
	}, core.RequireApproval())
	agt := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "pay"})), pay)
	res, err := agt.Generate(context.Background(), core.Prompt("pay it"), core.Steering(log.fn))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Pending) != 1 {
		t.Fatalf("pending = %d, want the parked call", len(res.Pending))
	}
	if log.calls != 0 {
		t.Errorf("SteerFunc called %d times at the approval boundary; the boundary is not a drain point", log.calls)
	}
}

// A StopWhen end is an intended end: no drain, the source keeps its
// messages (ADR 0019 §2.4) — with or without tool calls in the step.
func TestSteeringNotDrainedAfterStopWhen(t *testing.T) {
	submit := core.Tool("submit", "", func(_ context.Context, _ struct{}) (string, error) {
		return "submitted", nil
	})
	for _, tc := range []struct {
		name  string
		turns []wefttest.Turn
	}{
		{"stop tool", []wefttest.Turn{wefttest.ToolCalls(wefttest.Call{Name: "submit"})}},
		{"stop text", []wefttest.Turn{wefttest.Say("DONE")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var log steerLog
			agt := core.New(wefttest.Script(tc.turns...), submit,
				core.StopWhen(core.HasToolCall("submit")),
				core.StopWhen(core.StepCountIs(1)))
			res, err := agt.Generate(context.Background(), core.Prompt("finish"), core.Steering(log.fn))
			if err != nil {
				t.Fatal(err)
			}
			if res.NumSteps() != 1 {
				t.Fatalf("steps = %d, want the intended end to end", res.NumSteps())
			}
			if log.calls != 0 {
				t.Errorf("SteerFunc called %d times after StopWhen fired; an intended end is not a drain point", log.calls)
			}
		})
	}
}

// A delivered message with any role other than RoleUser fails the run
// with ErrInvalidSteer; nothing from that drain reaches the transcript
// (ADR 0019 §3).
func TestSteeringNonUserRoleFails(t *testing.T) {
	bad := func(_ context.Context, _ core.SteerPoint) []core.Message {
		return []core.Message{core.User("fine"), {Role: core.RoleAssistant, Content: []core.Part{core.TextPart{Text: "I speak for the model"}}}}
	}
	var events []core.Event
	agt := core.New(wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "echo"})), echoTool(),
		core.Tap(func(_ context.Context, ev core.Event) { events = append(events, ev) }))
	_, err := agt.Generate(context.Background(), core.Prompt("x"), core.Steering(bad))
	if !errors.Is(err, core.ErrInvalidSteer) {
		t.Fatalf("err = %v, want ErrInvalidSteer", err)
	}
	var re *core.RunError
	if !errors.As(err, &re) || re.Step != 0 {
		t.Fatalf("err = %v, want a RunError at the drain's step", err)
	}
	for _, m := range re.Result.Messages {
		if m.Role == core.RoleAssistant && m.Text() == "I speak for the model" {
			t.Error("the invalid message reached the transcript")
		}
	}
	for _, ev := range events {
		if _, ok := ev.(core.Steered); ok {
			t.Error("a Steered event was emitted for an invalid drain")
		}
	}
}

// The Steered event sits between the step's StepFinish and the next
// StepStart, numbered from the run's Seq counter, and the delivered
// messages are a snapshot of the transcript's (ADR 0019 §4).
func TestSteeringEventOrderAndSeq(t *testing.T) {
	steerMsg := core.User("switch to metric units")
	steer := wefttest.NewSteers().At(0, steerMsg)
	run := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("done"),
	), echoTool()).Stream(context.Background(), core.RunID("r1"), core.Prompt("x"), steer.Option())
	var got []core.Event
	for ev, err := range run.Events() {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev)
	}
	want := []core.Event{
		core.RunStart{ID: "r1", Model: core.ModelInfo{Provider: "wefttest", Name: "script"}, InstructionsHash: emptySHA256},
		core.StepStart{RunID: "r1", Index: 0},
		core.ToolStart{RunID: "r1", Seq: 1, CallID: "call_1", Name: "echo", Args: json.RawMessage(`{}`)},
		core.ToolFinish{RunID: "r1", Seq: 2, CallID: "call_1", Name: "echo", Content: "ok"},
		core.StepFinish{RunID: "r1", Index: 0, Reason: core.StopToolCalls, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
		core.Steered{RunID: "r1", Seq: 3, Step: 0, Messages: []core.Message{steerMsg}},
		core.StepStart{RunID: "r1", Index: 1},
		core.ToolStart{RunID: "r1", Seq: 4, CallID: "call_1", Name: "echo", Args: json.RawMessage(`{}`)},
		core.ToolFinish{RunID: "r1", Seq: 5, CallID: "call_1", Name: "echo", Content: "ok"},
		core.StepFinish{RunID: "r1", Index: 1, Reason: core.StopToolCalls, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
		core.StepStart{RunID: "r1", Index: 2},
		core.TextDelta{RunID: "r1", Text: "done"},
		core.StepFinish{RunID: "r1", Index: 2, Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
		core.RunFinish{RunID: "r1", Usage: core.Usage{InputTokens: 30, OutputTokens: 15}, Steps: 3},
	}
	if got := stripStepTiming(got); !reflect.DeepEqual(got, want) {
		t.Fatalf("events:\n got  %s\n want %s", renderEvents(got), renderEvents(want))
	}
}

// A redirect when MaxSteps is exactly reached fails with the usual
// ErrMaxSteps, the steer delivered but unanswered on the partial
// transcript (ADR 0019 §5).
func TestSteeringRedirectAtMaxSteps(t *testing.T) {
	steer := wefttest.NewSteers().At(0, core.User("one more thing"))
	agt := core.New(wefttest.Script(wefttest.Say("done")), core.MaxSteps(1))
	_, err := agt.Generate(context.Background(), core.Prompt("x"), steer.Option())
	if !errors.Is(err, core.ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps", err)
	}
	var re *core.RunError
	if !errors.As(err, &re) {
		t.Fatal(err)
	}
	last := re.Result.Messages[len(re.Result.Messages)-1]
	if last.Role != core.RoleUser || last.Text() != "one more thing" {
		t.Errorf("partial transcript ends with %+v, want the delivered steer", last)
	}
}

// A redirect goes through the continuation checks: a breached UsageLimit
// fails the run with the steer on the partial transcript, delivered but
// unanswered (ADR 0019 §5).
func TestSteeringRedirectThroughGuard(t *testing.T) {
	steer := wefttest.NewSteers().At(0, core.User("one more thing"))
	agt := core.New(wefttest.Script(wefttest.Say("done")), core.UsageLimit(core.Usage{InputTokens: 9}))
	_, err := agt.Generate(context.Background(), core.Prompt("x"), steer.Option())
	if !errors.Is(err, core.ErrUsageLimit) {
		t.Fatalf("err = %v, want ErrUsageLimit (the redirect is a continuation)", err)
	}
	var re *core.RunError
	if !errors.As(err, &re) {
		t.Fatal(err)
	}
	last := re.Result.Messages[len(re.Result.Messages)-1]
	if last.Role != core.RoleUser || last.Text() != "one more thing" {
		t.Errorf("partial transcript ends with %+v, want the delivered steer", last)
	}
}

// A child run started by a Subagent tool does not inherit the steering
// source: the hook belongs to the run it was passed to (ADR 0019 §7).
func TestSteeringChildRunsDoNotInherit(t *testing.T) {
	childModel := wefttest.Script(wefttest.Say("child done"))
	child := core.New(childModel)
	helper := core.Subagent("helper", "runs the helper.", child)
	var log steerLog
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "helper", Args: `{"prompt":"do it"}`}),
		wefttest.Say("ok"),
	), helper)
	res, err := agt.Generate(context.Background(), core.Prompt("delegate"), core.RunID("p1"), core.Steering(log.fn))
	if err != nil {
		t.Fatal(err)
	}
	if res.NumSteps() != 2 {
		t.Fatalf("steps = %d, want 2", res.NumSteps())
	}
	// The parent's own drain points only: after the child's batch and
	// at the final step. Never a point naming a child run.
	for _, p := range log.points {
		if p.RunID != "p1" {
			t.Errorf("drain point reported run %q; a child run never drains the parent's source", p.RunID)
		}
	}
	if len(log.points) != 2 || log.points[0].Final || !log.points[1].Final {
		t.Errorf("drain points = %+v, want step 0 (batch) and step 1 (final) of the parent", log.points)
	}
	// The child saw only its prompt, no steer.
	if reqs := childModel.Requests(); len(reqs) != 1 || len(reqs[0].Messages) != 1 || reqs[0].Messages[0].Text() != "do it" {
		t.Errorf("child requests = %+v, want exactly its prompt", reqs)
	}
}

// A max_tokens step with truncated calls is a safe drain point: every
// call of the batch carries its error result, so the steer lands after
// the tool message and the pairing is intact (ADR 0019 §2.2).
func TestSteeringMaxTokensStepIsADrainPoint(t *testing.T) {
	model := &turnModel{turns: [][]core.ModelEvent{
		{
			core.ModelToolCall{ID: "c1", Name: "echo", Args: json.RawMessage(`{}`)},
			core.ModelToolCall{ID: "c2", Name: "echo", Args: json.RawMessage(`{"cut`)},
			core.ModelFinish{Reason: core.StopMaxTokens, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		{
			core.ModelTextDelta{Text: "recovered"},
			core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	}}
	steer := wefttest.NewSteers().At(0, core.User("switch to metric units"))
	agt := core.New(model, echoTool())
	res, err := agt.Generate(context.Background(), core.Prompt("x"), steer.Option())
	if err != nil {
		t.Fatal(err)
	}
	// prompt, assistant(calls), tool(error results), steer, reply.
	if n := len(res.Messages); n != 5 {
		t.Fatalf("transcript = %d messages, want 5", n)
	}
	if m := res.Messages[2]; m.Role != core.RoleTool || len(m.Content) != 2 {
		t.Errorf("message 2 = %+v, want the tool message with both error results", m)
	}
	if m := res.Messages[3]; m.Role != core.RoleUser || m.Text() != "switch to metric units" {
		t.Errorf("message 3 = %+v, want the steer after the tool message", m)
	}
}

// A steering source that always returns nothing leaves a run
// byte-identical to one without steering installed.
func TestSteeringNilReturnIsIdentical(t *testing.T) {
	run := func(steer core.RunOption) (*core.RunResult, []core.Event) {
		var events []core.Event
		agt := core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
			wefttest.Say("done"),
		), echoTool(), core.Tap(func(_ context.Context, ev core.Event) { events = append(events, ev) }))
		res, err := agt.Generate(context.Background(), core.RunID("r"), core.Prompt("x"), steer)
		if err != nil {
			t.Fatal(err)
		}
		return res, events
	}
	plainRes, plainEvents := run(nil)
	var log steerLog
	steeredRes, steeredEvents := run(core.Steering(log.fn))
	if log.calls != 2 {
		t.Errorf("SteerFunc called %d times, want one per step (batch, final)", log.calls)
	}
	if !reflect.DeepEqual(plainEvents, steeredEvents) {
		t.Error("events differ between an unsteered run and one whose source drains nothing")
	}
	if !reflect.DeepEqual(plainRes.Messages, steeredRes.Messages) {
		t.Error("transcripts differ between an unsteered run and one whose source drains nothing")
	}
}

// Steering(nil) installs no source: the option is a no-op, the same
// ignore-don't-panic rule as an empty RunID.
func TestSteeringNilOptionIsNoop(t *testing.T) {
	res, err := core.New(wefttest.Script(wefttest.Say("done"))).
		Generate(context.Background(), core.Prompt("x"), core.Steering(nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.NumSteps() != 1 || res.Text() != "done" {
		t.Errorf("run under Steering(nil) = %d steps, %q; want an ordinary one-step run", res.NumSteps(), res.Text())
	}
}

// A SteerFunc panic reaches the caller like a PrepareStep panic: the
// span guard re-panics, OnRunEnd never fires — a crash is not an
// outcome (ADR 0016's rule, applied to the drain point).
func TestSteeringPanicReachesCaller(t *testing.T) {
	calls := 0
	agt := core.New(wefttest.Script(wefttest.Say("ok")),
		core.OnRunEnd(func(context.Context, *core.RunResult, error) { calls++ }))
	defer func() {
		if recover() == nil {
			t.Fatal("the SteerFunc panic should have reached the caller")
		}
		if calls != 0 {
			t.Errorf("OnRunEnd fired %d times on a panicked run; a crash is not an outcome", calls)
		}
	}()
	_, _ = agt.Generate(context.Background(), core.Prompt("hi"), core.Steering(
		func(context.Context, core.SteerPoint) []core.Message { panic("user code bug") }))
}

// A steered conversation's transcript is pinned: a delivered steer is a
// user message at a new position in the model's input — a
// model-visible change, golden-tested (ADR 0019 consequences).
func TestSteeringGoldenTranscript(t *testing.T) {
	steer := wefttest.NewSteers().At(0, core.User("switch to metric units"))
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("done"),
	), echoTool())
	res, err := agt.Generate(context.Background(), core.Prompt("convert this"), core.RunID("r1"), steer.Option())
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(res.Messages, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	wefttest.Golden(t, "testdata/steering-transcript.json", append(b, '\n'))
}

// renderEvents renders one event per line for failure messages.
func renderEvents(evs []core.Event) string {
	var b strings.Builder
	for _, ev := range evs {
		fmt.Fprintf(&b, "%T %+v\n", ev, ev)
	}
	return b.String()
}
