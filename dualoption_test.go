package weft_test

// The dual options (ADR 0024 D5, WEFT-PLAYGROUND §10.1): Instructions,
// MaxSteps and Parallelism are Option and RunOption at once, the
// Thinking shape. A run's value replaces the agent's for that run
// alone; the two limits may only lower — a raise is ErrInvalidRunOption
// before any model call.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

func TestInstructionsRunOptionReplacesForOneRun(t *testing.T) {
	var systems []string
	agt := weft.New(wefttest.Script(wefttest.Say("ok"), wefttest.Say("ok"), wefttest.Say("ok")),
		weft.Instructions("agent default"),
		weft.PrepareStep(func(_ context.Context, _ int, req weft.ModelRequest) (weft.ModelRequest, error) {
			systems = append(systems, req.System)
			return req, nil
		}))
	if _, err := agt.Generate(context.Background(), weft.Prompt("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := agt.Generate(context.Background(), weft.Prompt("b"),
		weft.Instructions("run override")); err != nil {
		t.Fatal(err)
	}
	if _, err := agt.Generate(context.Background(), weft.Prompt("c")); err != nil {
		t.Fatal(err)
	}
	want := []string{"agent default", "run override", "agent default"}
	if len(systems) != len(want) {
		t.Fatalf("systems = %v, want %v", systems, want)
	}
	for i := range want {
		if systems[i] != want[i] {
			t.Errorf("run %d system = %q, want %q", i, systems[i], want[i])
		}
	}
}

func TestMaxStepsRunOptionLowers(t *testing.T) {
	echo := weft.Tool("echo", "Echo.", func(ctx context.Context, in struct{}) (string, error) {
		return "ok", nil
	})
	// A script that always wants tools burns steps; each case gets a
	// fresh agent so the scripts stay independent.
	newAgent := func() *weft.Agent {
		return weft.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
			wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
			wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
			wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		), weft.MaxSteps(3), echo)
	}
	// The agent's own budget works as ever: three steps, then the budget.
	if _, err := newAgent().Generate(context.Background(), weft.Prompt("a")); !errors.Is(err, weft.ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps", err)
	}
	// A run may tighten it: one step, then the budget.
	if _, err := newAgent().Generate(context.Background(), weft.Prompt("b"), weft.MaxSteps(1)); !errors.Is(err, weft.ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps", err)
	}
	// An equal value is not a raise.
	if _, err := newAgent().Generate(context.Background(), weft.Prompt("c"), weft.MaxSteps(3)); !errors.Is(err, weft.ErrMaxSteps) {
		t.Fatalf("equal max steps rejected or miscounted: %v", err)
	}
}

func TestMaxStepsRaiseIsInvalidRunOptionBeforeModelCall(t *testing.T) {
	var steps int
	agt := weft.New(wefttest.Script(wefttest.Say("ok")),
		weft.MaxSteps(2),
		weft.Tap(func(_ context.Context, ev weft.Event) {
			if _, ok := ev.(weft.StepStart); ok {
				steps++
			}
		}))
	_, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.MaxSteps(5))
	if !errors.Is(err, weft.ErrInvalidRunOption) {
		t.Fatalf("err = %v, want ErrInvalidRunOption", err)
	}
	var re *weft.RunError
	if !errors.As(err, &re) {
		t.Fatalf("err = %T, want *RunError", err)
	}
	if steps != 0 {
		t.Errorf("%d steps started; the raise must fail before any model call", steps)
	}
}

// Parallelism lowered to 1 serializes the step's calls, exactly as
// Sequential() would: the second call starts only after the first
// finishes, observable as the flag the first call sets.
func TestParallelismRunOptionLowers(t *testing.T) {
	set := false
	slow := weft.Tool("slow", "Sets the flag after a beat.", func(ctx context.Context, in struct{}) (string, error) {
		time.Sleep(20 * time.Millisecond)
		set = true
		return "set", nil
	})
	look := weft.Tool("look", "Reads the flag.", func(ctx context.Context, in struct{}) (string, error) {
		if !set {
			return "not yet", nil
		}
		return "set", nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "slow"}, wefttest.Call{Name: "look"}),
		wefttest.Say("done"),
	), weft.Parallelism(4), slow, look)
	res, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.Parallelism(1))
	if err != nil {
		t.Fatal(err)
	}
	var lookResult string
	for _, r := range res.Steps[0].Results {
		if r.Name == "look" {
			lookResult = r.Content
		}
	}
	if lookResult != "set" {
		t.Errorf("parallel look result = %q; under parallelism 1 the flag must be set first", lookResult)
	}
}

func TestParallelismRaiseIsInvalidRunOption(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.Say("ok")), weft.Parallelism(2))
	_, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.Parallelism(8))
	if !errors.Is(err, weft.ErrInvalidRunOption) {
		t.Fatalf("err = %v, want ErrInvalidRunOption", err)
	}
	if _, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.Parallelism(2)); err != nil {
		t.Fatalf("equal parallelism rejected: %v", err)
	}
}
