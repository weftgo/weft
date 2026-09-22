package weft_test

import (
	"context"
	"encoding/json"
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

type formOut struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Count int    `json:"count"`
}

// The golden partial sequence: each delta grows the closed prefix, and
// Feed reports the partial exactly when it changed. Deltas scripted
// with Raw, since Script's turns emit calls whole.
func TestOutputDecoderPartialSequence(t *testing.T) {
	turn := wefttest.Raw(
		weft.ModelToolCallDelta{Index: 0, Name: "submit_output", Args: `{"na`},
		weft.ModelToolCallDelta{Index: 0, Name: "submit_output", Args: `me":"Ada",`},
		weft.ModelToolCallDelta{Index: 0, Name: "other_tool", Args: `{"x":1}`}, // parallel call: ignored
		weft.ModelToolCallDelta{Index: 0, Name: "submit_output", Args: `"count":4`},
		weft.ModelToolCall{ID: "call_1", Name: "submit_output", Args: json.RawMessage(`{"name":"Ada","count":4}`)},
		weft.ModelFinish{Reason: weft.StopToolCalls, Usage: weft.Usage{InputTokens: 3, OutputTokens: 2}},
	)
	script := wefttest.Script(turn, turn) // the second turn serves the OutputOf comparison run
	agt := weft.New(script, weft.Output[formOut]())
	dec := weft.NewOutputDecoder[formOut]()
	var partials []formOut
	for ev, err := range agt.Stream(context.Background(), weft.Prompt("fill the form")).Events() {
		if err != nil {
			t.Fatal(err)
		}
		// Nested events are not the decoder's concern either.
		if _, ok := ev.(weft.Nested); ok {
			continue
		}
		if p, ok := dec.Feed(ev); ok {
			partials = append(partials, p)
		}
	}
	want := []formOut{
		{Name: "Ada"},           // after the first member's comma
		{Name: "Ada", Count: 4}, // after the second member closes
	}
	if len(partials) != len(want) {
		t.Fatalf("partials = %+v, want %+v", partials, want)
	}
	for i, w := range want {
		if partials[i] != w {
			t.Errorf("partial %d = %+v, want %+v", i, partials[i], w)
		}
	}
	// Result follows the OutputOf rule on the same run's result.
	res, err := agt.Generate(context.Background(), weft.Prompt("fill the form"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := dec.Result()
	if err != nil {
		t.Fatal(err)
	}
	viaOf, err := weft.OutputOf[formOut](res)
	if err != nil {
		t.Fatal(err)
	}
	if got != viaOf {
		t.Errorf("Result = %+v, OutputOf = %+v; the two rules disagree", got, viaOf)
	}
}

// Two submit_output calls within one step are degenerate — the stop
// condition ends the run on the first valid one — and the decoder
// treats the second as a fresh buffer: Result follows the last call,
// the OutputOf rule (pinned by the 2026-09-22 review; concatenating
// two distinct calls' arguments could only ever produce bytes that do
// not decode). Whole-call arrival rides the same reset.
func TestOutputDecoderTwoCallsInOneStep(t *testing.T) {
	dec := weft.NewOutputDecoder[formOut]()
	feed := func(ev weft.Event) (formOut, bool) { return dec.Feed(ev) }
	feed(weft.StepStart{})
	p1, ok := feed(weft.ToolArgsDelta{Name: "submit_output", Args: `{"name":"Ada","count":4`})
	if !ok || p1.Name != "Ada" || p1.Count != 4 {
		t.Fatalf("first call: (%+v, %v), want the full partial", p1, ok)
	}
	feed(weft.ToolFinish{Name: "submit_output"})
	// The second call starts the buffer over (its ToolStart, as any
	// adapter emits one); its deltas are its own, not a concatenation.
	p2, ok := feed(weft.ToolStart{Name: "submit_output"})
	if ok || p2.Name != "Ada" || p2.Count != 4 {
		t.Fatalf("second ToolStart: (%+v, %v), want the carried partial, no change", p2, ok)
	}
	p3, ok := feed(weft.ToolArgsDelta{Name: "submit_output", Args: `{"name":"Grace","count":0`})
	if !ok || p3.Name != "Grace" || p3.Count != 0 {
		t.Errorf("second call: (%+v, %v), want Grace/0 — the fresh buffer", p3, ok)
	}
	feed(weft.ToolArgsDelta{Name: "submit_output", Args: `}`})
	feed(weft.ToolFinish{Name: "submit_output"})
	got, err := dec.Result()
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Grace" || got.Count != 0 {
		t.Errorf("Result = %+v, want the last call (the OutputOf rule)", got)
	}
	// A whole-call arrival (ToolStart with the complete args, the
	// Google shape) replaces the buffer the same way and decodes once.
	feed(weft.ToolStart{Name: "submit_output", Args: json.RawMessage(`{"name":"Whole","count":9}`)})
	feed(weft.ToolFinish{Name: "submit_output"})
	got, err = dec.Result()
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Whole" || got.Count != 9 {
		t.Errorf("Result = %+v, want the whole-call arrival", got)
	}
}

// A mid-stream garbage prefix keeps the last good partial; errors
// surface only at Result, which returns ErrNoOutput when no valid
// submit_output call finished.
func TestOutputDecoderGarbageAndNoOutput(t *testing.T) {
	dec := weft.NewOutputDecoder[formOut]()
	feed := func(ev weft.Event) (formOut, bool) { return dec.Feed(ev) }
	// A key with no value yet decodes nothing; the parallel garbage
	// neither breaks nor reports.
	if _, ok := feed(weft.ToolArgsDelta{Name: "submit_output", Args: `{"na`}); ok {
		t.Error("an incomplete first key reported a partial")
	}
	p, ok := feed(weft.ToolArgsDelta{Name: "submit_output", Args: `me":"Ada",`})
	if !ok || p.Name != "Ada" {
		t.Errorf("after the member closes: (%+v, %v), want Ada/true", p, ok)
	}
	if _, ok := feed(weft.ToolArgsDelta{Name: "submit_output", Args: `zzz`}); ok {
		t.Error("garbage reported a change")
	}
	if _, err := dec.Result(); !errors.Is(err, weft.ErrNoOutput) {
		t.Errorf("Result before any finish = %v, want ErrNoOutput", err)
	}
	// Nested events are ignored wholesale — a subagent's structured
	// output is its own decoder's job.
	if _, ok := feed(weft.Nested{Event: weft.ToolFinish{Name: "submit_output"}}); ok {
		t.Error("a Nested event reported a change")
	}
	if _, err := dec.Result(); !errors.Is(err, weft.ErrNoOutput) {
		t.Errorf("Result after only Nested = %v, want ErrNoOutput", err)
	}
}
