package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// namedStop is a custom StopCondition with a name of its own: its
// String() is its stopped_by, as it is its manifest name.
type namedStop struct{}

func (namedStop) Stop(steps []core.StepRecord) bool { return len(steps) >= 1 }
func (namedStop) String() string                    { return "budget_spent" }

// TestStoppedByEveryExit pins weft.run.stopped_by and RunFinish.StoppedBy
// (ADR 0016, amendment 2026-10-12): a scripted run under each of the
// loop's exits carries its value on the invoke_agent span and, for a
// successful run, in run_finish — the same string, from one vocabulary
// with the manifest's policy.stop_when. A failed run has no run_finish
// (rule 4): the span is its carrier. A canceled run carries
// weft.cancel.cause when the cause names itself.
func TestStoppedByEveryExit(t *testing.T) {
	submit := core.Tool("submit", "Submit.", func(context.Context, struct{}) (string, error) { return "ok", nil })
	refund := core.Tool("refund", "Refund.", func(context.Context, struct{}) (string, error) { return "ok", nil },
		core.RequireApproval())
	retry := core.Tool("retry", "Always asks again.", func(context.Context, struct{}) (string, error) {
		return "", core.ModelRetry("try again")
	})
	calls := func(name string, n int) []wefttest.Turn {
		steps := make([]wefttest.Turn, n)
		for i := range steps {
			steps[i] = wefttest.ToolCalls(wefttest.Call{Name: name, Args: `{}`})
		}
		return steps
	}
	// cancelIn is a tool that cancels the run's own context from inside
	// its handler, with cause, then waits for it.
	cancelIn := func(cancel func(error), cause error) *core.ToolDef {
		return core.Tool("block", "Cancels the run.", func(ctx context.Context, _ struct{}) (string, error) {
			cancel(cause)
			<-ctx.Done()
			return "", ctx.Err()
		})
	}

	type tc struct {
		name      string
		model     *wefttest.Model
		opts      []core.Option
		ctx       func(t *testing.T) (context.Context, []core.Option)
		wantBy    string
		wantCause string
		wantErr   error // nil: a successful run, with its run_finish
	}
	cases := []tc{
		{name: "has_tool_call", model: wefttest.Script(calls("submit", 1)...),
			opts: []core.Option{submit, core.StopWhen(core.HasToolCall("submit"))}, wantBy: "has_tool_call:submit"},
		{name: "step_count_is", model: wefttest.Script(calls("echo", 2)...),
			opts: []core.Option{spanEcho, core.StopWhen(core.StepCountIs(2))}, wantBy: "step_count_is:2"},
		{name: "output_submitted", model: wefttest.Script(wefttest.ToolCalls(wefttest.Call{Name: "submit_output", Args: `{"approved":true,"score":1}`})),
			opts: []core.Option{core.Output[verdict]()}, wantBy: "output_submitted"},
		{name: "final_answer", model: wefttest.Script(wefttest.Say("done")), wantBy: "final_answer"},
		{name: "parked", model: wefttest.Script(calls("refund", 1)...),
			opts: []core.Option{refund}, wantBy: "parked"},
		{name: "custom StopFunc", model: wefttest.Script(calls("echo", 1)...),
			opts:   []core.Option{spanEcho, core.StopWhen(core.StopFunc(func(s []core.StepRecord) bool { return len(s) >= 1 }))},
			wantBy: "custom"},
		{name: "custom named condition", model: wefttest.Script(calls("echo", 1)...),
			opts: []core.Option{spanEcho, core.StopWhen(core.StepCountIs(9), namedStop{})}, wantBy: "budget_spent"},
		{name: "max_steps", model: wefttest.Script(calls("echo", 2)...),
			opts: []core.Option{spanEcho, core.MaxSteps(1)}, wantBy: "max_steps", wantErr: core.ErrMaxSteps},
		{name: "usage_limit", model: wefttest.Script(calls("echo", 2)...),
			opts: []core.Option{spanEcho, core.UsageLimit(core.Usage{InputTokens: 5})}, wantBy: "usage_limit", wantErr: core.ErrUsageLimit},
		{name: "loop_detected", model: wefttest.Script(calls("echo", 3)...),
			opts: []core.Option{spanEcho, core.DetectLoops(2)}, wantBy: "loop_detected", wantErr: core.ErrLoopDetected},
		{name: "model_retries_exceeded", model: wefttest.Script(calls("retry", 3)...),
			opts: []core.Option{retry, core.MaxModelRetries(1)}, wantBy: "model_retries_exceeded", wantErr: core.ErrModelRetriesExceeded},
		{name: "canceled, typed cause", model: wefttest.Script(calls("block", 1)...),
			ctx: func(t *testing.T) (context.Context, []core.Option) {
				ctx, cancel := context.WithCancelCause(context.Background())
				t.Cleanup(func() { cancel(nil) })
				return ctx, []core.Option{cancelIn(cancel, core.CancelCause("studio.cancel"))}
			}, wantBy: "canceled", wantCause: "studio.cancel", wantErr: context.Canceled},
		{name: "canceled, no cause", model: wefttest.Script(calls("block", 1)...),
			ctx: func(t *testing.T) (context.Context, []core.Option) {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				return ctx, []core.Option{cancelIn(func(error) { cancel() }, nil)}
			}, wantBy: "canceled", wantErr: context.Canceled},
		{name: "canceled, deadline", model: wefttest.Script(calls("block", 1)...),
			ctx: func(t *testing.T) (context.Context, []core.Option) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				t.Cleanup(cancel)
				return ctx, []core.Option{cancelIn(func(error) {}, nil)}
			}, wantBy: "canceled", wantCause: "deadline", wantErr: context.DeadlineExceeded},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			opts := append([]core.Option(nil), c.opts...)
			if c.ctx != nil {
				var extra []core.Option
				ctx, extra = c.ctx(t)
				opts = append(opts, extra...)
			}
			tp := newRecProvider()
			var finishes []core.RunFinish
			opts = append(opts, core.Name("demo"), core.TracerProvider(tp),
				core.Tap(func(_ context.Context, ev core.Event) {
					if f, ok := ev.(core.RunFinish); ok {
						finishes = append(finishes, f)
					}
				}))
			_, err := core.New(c.model, opts...).Generate(ctx, core.Prompt("go"))
			switch {
			case c.wantErr == nil && err != nil:
				t.Fatalf("run failed: %v", err)
			case c.wantErr != nil && !errors.Is(err, c.wantErr):
				t.Fatalf("run error = %v, want %v", err, c.wantErr)
			}
			attrs := tp.find(t, "invoke_agent demo").attrsMap()
			if got := attrs["weft.run.stopped_by"]; got != c.wantBy {
				t.Errorf("weft.run.stopped_by = %q, want %q", got, c.wantBy)
			}
			if got, ok := attrs["weft.cancel.cause"]; got != c.wantCause || ok != (c.wantCause != "") {
				t.Errorf("weft.cancel.cause = %q (set %v), want %q", got, ok, c.wantCause)
			}
			if c.wantErr != nil {
				// Rule 4: a failed run has no RunFinish; the span is the carrier.
				if len(finishes) != 0 {
					t.Errorf("a failed run emitted RunFinish %+v", finishes)
				}
				return
			}
			if len(finishes) != 1 {
				t.Fatalf("RunFinish events = %d, want 1", len(finishes))
			}
			if got := finishes[0].StoppedBy; got != c.wantBy {
				t.Errorf("RunFinish.StoppedBy = %q, want %q", got, c.wantBy)
			}
			// The run_finish record's body is this wire form: stopped_by
			// round-trips through UnmarshalEvent.
			b, err := json.Marshal(finishes[0])
			if err != nil {
				t.Fatal(err)
			}
			back, err := core.UnmarshalEvent(b)
			if err != nil {
				t.Fatalf("UnmarshalEvent(%s): %v", b, err)
			}
			if got := back.(core.RunFinish).StoppedBy; got != c.wantBy {
				t.Errorf("run_finish wire %s: stopped_by = %q, want %q", b, got, c.wantBy)
			}
		})
	}
}

// The manifest's policy.stop_when and weft.run.stopped_by are one
// vocabulary: every built-in condition's stopped_by is its manifest name.
func TestStoppedByIsTheManifestName(t *testing.T) {
	agt := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("stops"),
		core.StopWhen(core.HasToolCall("submit"), core.StepCountIs(2), namedStop{}),
		core.Output[verdict]())
	b, err := core.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Agents []struct {
			Policy struct {
				StopWhen []string `json:"stop_when"`
			} `json:"policy"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprint(m.Agents[0].Policy.StopWhen)
	if want := "[has_tool_call:submit step_count_is:2 budget_spent output_submitted]"; got != want {
		t.Errorf("manifest stop_when = %s, want %s", got, want)
	}
}

// A CancelCause is a cancellation: errors.Is matches context.Canceled,
// and its Kind is the weft.cancel.cause value.
func TestCancelCauseIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(core.CancelCause("thread.interrupted"))
	cause := context.Cause(ctx)
	if !errors.Is(cause, context.Canceled) {
		t.Errorf("errors.Is(%v, context.Canceled) = false", cause)
	}
	var k interface{ Kind() string }
	if !errors.As(cause, &k) || k.Kind() != "thread.interrupted" {
		t.Errorf("cause %v does not name itself thread.interrupted", cause)
	}
}

// A layer that cancels a run says why with a CancelCause; the run's
// invoke_agent span records it as weft.cancel.cause.
func ExampleCancelCause() {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(core.CancelCause("app.shutdown")) // e.g. on SIGTERM

	agt := core.New(wefttest.Script(wefttest.Say("never")))
	_, err := agt.Generate(ctx, core.Prompt("hi"))
	fmt.Println(errors.Is(err, context.Canceled))
	fmt.Println(context.Cause(ctx))
	// Output:
	// true
	// context canceled: app.shutdown
}
