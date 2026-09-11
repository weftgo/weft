package weft_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

type verdict struct {
	Approved bool   `json:"approved"`
	Score    int    `json:"score"`
	Reason   string `json:"reason,omitempty"`
}

// An invalid submission is an ordinary tool error the model repairs; a
// valid one ends the run and GenerateAs decodes it.
func TestOutputRepairsThenStops(t *testing.T) {
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "submit_output", Args: `{"approved":true,"score":"high"}`}),
		wefttest.ToolCalls(wefttest.Call{Name: "submit_output", Args: `{"approved":true,"score":9,"reason":"fine"}`}),
		wefttest.Say("this step must never run"),
	)
	agt := weft.New(model, weft.Output[verdict]())

	v, res, err := weft.GenerateAs[verdict](context.Background(), agt, weft.Prompt("review"))
	if err != nil {
		t.Fatal(err)
	}
	if v != (verdict{Approved: true, Score: 9, Reason: "fine"}) {
		t.Errorf("output = %+v", v)
	}
	if res.NumSteps() != 2 {
		t.Errorf("steps = %d, want 2: the run stops on the first valid submission", res.NumSteps())
	}
	first := res.Steps[0].Results[0]
	if !first.IsError || !strings.Contains(first.Content, `field "score": expected integer, got string`) {
		t.Errorf("invalid submission result = %+v", first)
	}
	if got := res.Steps[1].Results[0].Content; got != "recorded" {
		t.Errorf("valid submission result = %q", got)
	}

	// The same value is reachable after a streamed run.
	v2, err := weft.OutputOf[verdict](res)
	if err != nil || v2 != v {
		t.Errorf("OutputOf = %+v, %v", v2, err)
	}
}

// A run that ends in text has no output: ErrNoOutput, with the result
// still returned for inspection.
func TestOutputMissing(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.Say("I'd rather not.")), weft.Output[verdict]())
	_, res, err := weft.GenerateAs[verdict](context.Background(), agt, weft.Prompt("review"))
	if !errors.Is(err, weft.ErrNoOutput) {
		t.Fatalf("err = %v, want ErrNoOutput", err)
	}
	if res == nil || res.Text() != "I'd rather not." {
		t.Errorf("result not returned alongside ErrNoOutput: %+v", res)
	}
}

// The output tool is advertised to the model with its reflected schema
// and named in the manifest's stop conditions.
func TestOutputToolContract(t *testing.T) {
	model := wefttest.Script(wefttest.Say("x"))
	agt := weft.New(model, weft.Name("reviewer"), weft.Output[verdict]())
	if _, err := agt.Generate(context.Background(), weft.Prompt("go")); err != nil {
		t.Fatal(err)
	}
	req := model.Requests()[0]
	if len(req.Tools) != 1 || req.Tools[0].Name != "submit_output" {
		t.Fatalf("advertised tools = %+v", req.Tools)
	}
	tool := req.Tools[0]
	if tool.Description != "Submit your final answer. Call this exactly once, when you are done: the run ends with it." {
		t.Errorf("description = %q", tool.Description)
	}
	if tool.InputSchema.Properties["score"].Type != "integer" {
		t.Errorf("schema not reflected from Out: %+v", tool.InputSchema)
	}
	b, err := weft.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"output_submitted"`) {
		t.Errorf("manifest lacks the output stop condition:\n%s", b)
	}
	if strings.Contains(string(b), "output.go") {
		t.Errorf("manifest source should be Output's caller, not output.go:\n%s", b)
	}
}

func TestOutputRejectsNonStruct(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Output[string]() should panic: providers require an object schema")
		}
	}()
	weft.Output[string]()
}
