package wefttest

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/weftgo/weft/core"
)

// Steers delivers the messages registered for a step, at that step's
// drain points only, as a copy the source does not alias.
func TestSteersDeliversByStep(t *testing.T) {
	src := NewSteers().At(0, core.User("left")).At(2, core.User("right"))
	got := src.Steer(context.Background(), core.SteerPoint{RunID: "r", Step: 0})
	if len(got) != 1 || got[0].Text() != "left" {
		t.Errorf("step 0 = %+v, want the registered message", got)
	}
	if got := src.Steer(context.Background(), core.SteerPoint{Step: 1}); got != nil {
		t.Errorf("step 1 = %+v, want nothing registered", got)
	}
	got = src.Steer(context.Background(), core.SteerPoint{Step: 2, Final: true})
	if len(got) != 1 || got[0].Text() != "right" {
		t.Errorf("step 2 = %+v, want the registered message", got)
	}
	// The registration is not aliased: rewriting the delivered slice
	// changes nothing the next drain returns.
	got[0].Content = nil
	again := src.Steer(context.Background(), core.SteerPoint{Step: 2})
	if again[0].Text() != "right" {
		t.Errorf("after mutating a delivery, step 2 = %+v; registrations must not alias", again)
	}
	// At with no messages clears the step.
	src.At(0)
	if got := src.Steer(context.Background(), core.SteerPoint{Step: 0}); got != nil {
		t.Errorf("cleared step 0 = %+v, want nil", got)
	}
}

// Steers.Option wires the source into a run end to end: a step-0 steer
// reaches the next model request's transcript.
func TestSteersOptionSteersARun(t *testing.T) {
	model := Script(ToolCalls(Call{Name: "noop"}), Say("done"))
	noop := core.Tool("noop", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	src := NewSteers().At(0, core.User("switch to metric units"))
	res, err := core.New(model, noop).Generate(context.Background(), core.Prompt("x"), src.Option())
	if err != nil {
		t.Fatal(err)
	}
	if n := len(res.Messages); n != 5 {
		t.Fatalf("transcript = %d messages, want 5 (steer included)", n)
	}
	if m := res.Messages[3]; m.Role != core.RoleUser || m.Text() != "switch to metric units" {
		t.Errorf("message 3 = %+v, want the steer after the tool message", m)
	}
	if reqs := model.Requests(); len(reqs) != 2 {
		t.Errorf("model called %d times, want the drain to spend another step", len(reqs))
	}
}

// Registration and draining from different goroutines stay race-clean:
// a live test may push into the source while the run drains it.
func TestSteersConcurrent(t *testing.T) {
	src := NewSteers()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(i int) { defer wg.Done(); src.At(i, core.User("m")) }(i)
		go func(i int) {
			defer wg.Done()
			if got := src.Steer(context.Background(), core.SteerPoint{Step: i}); len(got) > 1 {
				t.Error("a drain returned more than one delivery")
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < 8; i++ {
		got := src.Steer(context.Background(), core.SteerPoint{Step: i})
		if !slices.EqualFunc(got, []core.Message{core.User("m")}, func(a, b core.Message) bool { return a.Text() == b.Text() }) {
			t.Errorf("step %d = %+v after the race; want the one registered message", i, got)
		}
	}
}
