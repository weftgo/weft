package weft_test

// The per-run configuration RunOptions (ADR 0024 D5/D7,
// WEFT-PLAYGROUND §10.6's P0 core rows): OnlyTools narrows what the
// model is offered, UseModel rebuilds the middleware chain over the
// alternate, ParkOn parks at the approval boundary and Approve resumes,
// and weft.override.hash fingerprints the experiment.

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

func lookupTool() *weft.ToolDef {
	return weft.Tool("lookup", "Look up.", func(ctx context.Context, in struct {
		ID string `json:"id"`
	}) (string, error) {
		return `{"status":"shipped"}`, nil
	})
}

func refundTool() *weft.ToolDef {
	return weft.Tool("refund", "Refund an order.", func(ctx context.Context, in struct{}) (string, error) {
		return "refunded", nil
	})
}

// OnlyTools narrows the run to the named tools: the step's advertisement
// (what the model was shown) is the subset, in registration order, and
// dispatch resolves against the same snapshot — a call to a narrowed-out
// tool fails as unknown, the standing rule.
func TestOnlyToolsNarrows(t *testing.T) {
	var offered [][]string
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup", Args: `{"id":"4411"}`, ID: "c1"}),
		wefttest.Say("done"),
		wefttest.Say("done"),
	), lookupTool(), refundTool(),
		weft.PrepareStep(func(_ context.Context, _ int, req weft.ModelRequest) (weft.ModelRequest, error) {
			var names []string
			for _, td := range req.Tools {
				names = append(names, td.Name)
			}
			offered = append(offered, names)
			return req, nil
		}))
	res, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.OnlyTools("lookup"))
	if err != nil {
		t.Fatal(err)
	}
	if len(offered) != 2 || len(offered[0]) != 1 || offered[0][0] != "lookup" {
		t.Fatalf("offered = %v, want only lookup on each step", offered)
	}
	if offered[1][0] != "lookup" {
		t.Errorf("step 1 offered %v, want only lookup", offered[1])
	}
	if len(res.Steps[0].Results) != 1 || res.Steps[0].Results[0].Name != "lookup" {
		t.Errorf("results = %v, want the lookup call served", res.Steps[0].Results)
	}
	// The next run keeps the full set: the narrowing was the run's.
	if _, err := agt.Generate(context.Background(), weft.Prompt("y")); err != nil {
		t.Fatal(err)
	}
	if len(offered[2]) != 2 {
		t.Errorf("plain run offered %v, want the full set back", offered[2])
	}
}

// A call to a narrowed-out tool fails as unknown — the model sees the
// subset, so it cannot call what it never saw; scripting a call anyway
// (a replayed transcript, a stale fixture) proves the dispatch side.
func TestOnlyToolsDispatchRefusesNarrowedOut(t *testing.T) {
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c1"}),
		wefttest.Say("done"),
	), lookupTool(), refundTool())
	res, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.OnlyTools("lookup"))
	if err != nil {
		t.Fatal(err)
	}
	r := res.Steps[0].Results[0]
	if !r.IsError {
		t.Errorf("narrowed-out call succeeded: %+v", r)
	}
}

func TestOnlyToolsUnknownNameFailsBeforeModelCall(t *testing.T) {
	var steps int
	agt := weft.New(wefttest.Script(wefttest.Say("ok")), lookupTool(),
		weft.Tap(func(_ context.Context, ev weft.Event) {
			if _, ok := ev.(weft.StepStart); ok {
				steps++
			}
		}))
	_, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.OnlyTools("lookup", "nonexistent"))
	if !errors.Is(err, weft.ErrInvalidRunOption) {
		t.Fatalf("err = %v, want ErrInvalidRunOption", err)
	}
	if steps != 0 {
		t.Errorf("%d steps started; the unknown name must fail before any model call", steps)
	}
}

// A ToolSource's snapshot narrows too — OnlyTools keeps meaning against
// a dynamic registry (the subset of whatever the source returns).
func TestOnlyToolsWithToolSource(t *testing.T) {
	reg := []*weft.ToolDef{lookupTool(), refundTool()}
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c1"}),
		wefttest.Say("done"),
	), weft.ToolSource(func() []*weft.ToolDef { return reg }))
	res, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.OnlyTools("refund"))
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Steps[0].Results[0]; r.Name != "refund" || r.IsError {
		t.Errorf("refund result = %+v, want it served", r)
	}
}

// countingModel wraps a model and counts its steps: proof the WrapModel
// chain ran over whatever model the loop called.
type countingModel struct {
	next  weft.Model
	calls *int
}

func (m countingModel) Stream(ctx context.Context, req weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	*m.calls++
	return m.next.Stream(ctx, req)
}

// UseModel replaces the agent's model for the run and the WrapModel
// chain runs over it — the counting model sees the run's calls, the
// alternate's answer comes back, and the agent's own model is untouched.
func TestUseModelRebuildsChainOverAlternate(t *testing.T) {
	var altCalls int
	alt := countingModel{next: wefttest.Script(wefttest.Say("alternate")), calls: &altCalls}
	agt := weft.New(wefttest.Script(wefttest.Say("ok")))
	res, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.UseModel(alt))
	if err != nil {
		t.Fatal(err)
	}
	if altCalls == 0 {
		t.Error("the alternate model was never called")
	}
	if res.Text() != "alternate" {
		t.Errorf("run text = %q, want the alternate's answer", res.Text())
	}
}

// UseModel as a run option: one run goes through the alternate (with
// the WrapModel chain rebuilt over it), the next through the agent's
// own model.
func TestUseModelRunOptionRebuildsChain(t *testing.T) {
	var inner, outer int
	alt := wefttest.Script(wefttest.Say("alternate"))
	agt := weft.New(wefttest.Script(wefttest.Say("ok")),
		weft.WrapModel(func(next weft.Model) weft.Model {
			outer++
			return countingModel{next: next, calls: &inner}
		}))
	res, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.UseModel(alt))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "alternate" {
		t.Errorf("run text = %q, want the alternate's answer", res.Text())
	}
	if inner == 0 {
		t.Error("the WrapModel chain never ran over the alternate model")
	}
	res2, err := agt.Generate(context.Background(), weft.Prompt("y"))
	if err != nil {
		t.Fatal(err)
	}
	if res2.Text() != "ok" {
		t.Errorf("plain run text = %q, want the agent's own model", res2.Text())
	}
}

// RunStart.Model names the run's model, not the agent's — the run
// answered through the alternate.
func TestUseModelReportsRunModel(t *testing.T) {
	var started *weft.ModelInfo
	alt := wefttest.Script(wefttest.Say("alt"))
	agt := weft.New(wefttest.Script(wefttest.Say("base")),
		weft.Tap(func(_ context.Context, ev weft.Event) {
			if rs, ok := ev.(weft.RunStart); ok {
				started = &rs.Model
			}
		}))
	if _, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.UseModel(alt)); err != nil {
		t.Fatal(err)
	}
	if started == nil {
		t.Fatal("no RunStart observed")
	}
	// wefttest models report provider wefttest / name script for both;
	// the observable difference is that the run used the alternate —
	// pinned by the text in the test above. Here: identity present.
	if started.Provider == "" || started.Name == "" {
		t.Errorf("RunStart.Model = %+v, want the run model's identity", *started)
	}
}

// ParkOn parks the named tool's call at the approval boundary: the run
// ends successfully with the call pending, and Approve on a resuming
// run executes it.
func TestParkOnParksAndApproveResumes(t *testing.T) {
	ran := false
	refund := weft.Tool("refund", "Refund an order.", func(ctx context.Context, in struct{}) (string, error) {
		ran = true
		return "refunded", nil
	})
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c1"}),
		wefttest.Say("all set"),
	), refund)
	res1, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.ParkOn("refund"))
	if err != nil {
		t.Fatalf("a parked run fails: %v", err)
	}
	if len(res1.Pending) != 1 || res1.Pending[0].ID != "c1" {
		t.Fatalf("pending = %v, want the refund call parked", res1.Pending)
	}
	if ran {
		t.Error("the parked call executed")
	}
	res2, err := agt.Generate(context.Background(),
		weft.Messages(res1.Messages...), weft.Approve("c1"))
	if err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("Approve did not execute the parked call")
	}
	if res2.Text() != "all set" {
		t.Errorf("resumed run text = %q, want the script's final answer", res2.Text())
	}
	// A plain run of the same agent executes the tool without parking.
	ran = false
	if _, err := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c1"}),
		wefttest.Say("done"),
	), refund).Generate(context.Background(), weft.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("a plain run parked the call; ParkOn is per run")
	}
}

// The override fingerprint: weft.override.hash is stable for equal
// changes, differs for different ones, and is absent on a plain run —
// with the per-knob attributes beside it.
func TestOverrideHashOnRunSpan(t *testing.T) {
	tp := newRecProvider()
	agt := weft.New(wefttest.Script(
		wefttest.Say("ok"), wefttest.Say("ok"), wefttest.Say("ok"), wefttest.Say("ok"),
	), weft.Name("demo"), weft.TracerProvider(tp), lookupTool(), refundTool())

	// A plain run carries none of them.
	if _, err := agt.Generate(context.Background(), weft.Prompt("plain"), weft.RunID("r0")); err != nil {
		t.Fatal(err)
	}
	got := tp.find(t, "invoke_agent demo").attrsMap()
	if _, has := got["weft.override.hash"]; has {
		t.Error("a plain run carries weft.override.hash")
	}

	run := func(id string, opts ...weft.RunOption) string {
		t.Helper()
		all := append([]weft.RunOption{weft.RunID(id)}, opts...)
		if _, err := agt.Generate(context.Background(), all...); err != nil {
			t.Fatal(err)
		}
		attrs := spanAttrsByID(t, tp, id)
		if attrs["weft.run.id"] != id {
			t.Fatalf("found span for %q got %q", id, attrs["weft.run.id"])
		}
		return attrs["weft.override.hash"]
	}
	h1 := run("r1", weft.Instructions("custom prompt"), weft.OnlyTools("lookup"), weft.ParkOn("refund"))
	h2 := run("r2", weft.Instructions("custom prompt"), weft.OnlyTools("lookup"), weft.ParkOn("refund"))
	h3 := run("r3", weft.Instructions("another prompt"), weft.OnlyTools("lookup"), weft.ParkOn("refund"))
	if h1 == "" {
		t.Fatal("no weft.override.hash on an experiment run")
	}
	if h1 != h2 {
		t.Errorf("equal changes hashed differently: %q vs %q", h1, h2)
	}
	if h1 == h3 {
		t.Error("different instructions hashed equal")
	}
	if len(h1) != 64 {
		t.Errorf("hash = %q, want 64 hex chars", h1)
	}

	// The per-knob attributes beside the hash.
	attrs := spanAttrsByID(t, tp, "r1")
	for k, want := range map[string]string{
		"weft.override.instructions": "true",
		"weft.override.tools":        "lookup",
		"weft.override.park_on":      "refund",
	} {
		if attrs[k] != want {
			t.Errorf("%s = %q, want %q", k, attrs[k], want)
		}
	}
}

// spanAttrsByID finds the run span of one run (weft.run.id) — several
// runs share the agent, so the name alone is ambiguous.
func spanAttrsByID(t *testing.T, tp *recProvider, runID string) map[string]string {
	t.Helper()
	tp.mu.Lock()
	defer tp.mu.Unlock()
	for _, s := range tp.spans {
		if s.name != "invoke_agent demo" {
			continue
		}
		attrs := s.attrsMap()
		if attrs["weft.run.id"] == runID {
			return attrs
		}
	}
	t.Fatalf("no invoke_agent span for run %q", runID)
	return nil
}

// The fingerprint is over what changed, not how the options were
// spelled: the tool subset and the parked tools are sets, so the same
// names in another order — or named twice, as two ParkOn options for
// one tool do — hash equal and render the same sorted list. Sibling
// runs of one experiment built by different callers must not read as
// different experiments.
func TestOverrideHashIgnoresNameOrderAndDuplicates(t *testing.T) {
	tp := newRecProvider()
	agt := weft.New(wefttest.Script(
		wefttest.Say("ok"), wefttest.Say("ok"), wefttest.Say("ok"),
	), weft.Name("demo"), weft.TracerProvider(tp), lookupTool(), refundTool())
	run := func(id string, opts ...weft.RunOption) map[string]string {
		t.Helper()
		all := append([]weft.RunOption{weft.RunID(id)}, opts...)
		if _, err := agt.Generate(context.Background(), all...); err != nil {
			t.Fatal(err)
		}
		return spanAttrsByID(t, tp, id)
	}
	a := run("o1", weft.OnlyTools("lookup", "refund"), weft.ParkOn("refund", "lookup"))
	b := run("o2", weft.OnlyTools("refund"), weft.OnlyTools("lookup", "refund"),
		weft.ParkOn("lookup"), weft.ParkOn("refund"), weft.ParkOn("refund"))
	c := run("o3", weft.OnlyTools("lookup"), weft.ParkOn("refund", "lookup"))
	if a["weft.override.hash"] == "" {
		t.Fatal("no weft.override.hash on an experiment run")
	}
	if a["weft.override.hash"] != b["weft.override.hash"] {
		t.Errorf("the same tool subset and parked set hashed differently: %q vs %q",
			a["weft.override.hash"], b["weft.override.hash"])
	}
	if a["weft.override.hash"] == c["weft.override.hash"] {
		t.Error("a different tool subset hashed equal")
	}
	for id, attrs := range map[string]map[string]string{"o1": a, "o2": b} {
		if got := attrs["weft.override.tools"]; got != "lookup,refund" {
			t.Errorf("%s: weft.override.tools = %q, want %q", id, got, "lookup,refund")
		}
		if got := attrs["weft.override.park_on"]; got != "lookup,refund" {
			t.Errorf("%s: weft.override.park_on = %q, want %q", id, got, "lookup,refund")
		}
	}
}
