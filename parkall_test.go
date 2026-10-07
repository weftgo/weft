package weft_test

// ParkAllExcept — the default-deny park rule (ADR 0007 applied per run,
// ADR 0024 D7; WEFT-PLAYGROUND §6 rule 3: side effects never re-fire
// silently). ParkOn names what parks, so a tool the caller could not
// name — one a ToolSource supplies, one a Subagent's child run owns —
// runs for real. ParkAllExcept names what may run; everything else
// parks, in this run and in the runs started inside it.

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// countedTool counts its executions.
func countedTool(name string, ran *atomic.Int64) *weft.ToolDef {
	return weft.Tool(name, name+".", func(context.Context, struct{}) (string, error) {
		ran.Add(1)
		return name + " done", nil
	})
}

// A tool only a ToolSource supplies is invisible to Agent.Tools — the
// list a caller builds a ParkOn set from — and still parks: the rule is
// evaluated against the step's dispatch snapshot. The named tool runs.
func TestParkAllExceptParksToolSourceToolsAndRunsNamedOnes(t *testing.T) {
	var wired, looked atomic.Int64
	reg := []*weft.ToolDef{countedTool("lookup", &looked), countedTool("wire_money", &wired)}
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "lookup", ID: "c1"},
			wefttest.Call{Name: "wire_money", ID: "c2"},
		),
	), weft.ToolSource(func() []*weft.ToolDef { return reg }))
	if n := len(agt.Tools()); n != 0 {
		t.Fatalf("Agent.Tools() = %d tools, want 0 (the source's tools are not the static set)", n)
	}
	res, err := agt.Generate(context.Background(), weft.Prompt("x"),
		// "not_registered" is no error: an except-list names what may
		// run, and a dynamic registry's names are not known up front.
		weft.ParkAllExcept("lookup", "not_registered"))
	if err != nil {
		t.Fatalf("a parked run fails: %v", err)
	}
	if wired.Load() != 0 {
		t.Error("the unnamed ToolSource tool executed")
	}
	if looked.Load() != 1 {
		t.Errorf("the named tool ran %d times, want 1", looked.Load())
	}
	if len(res.Pending) != 1 || res.Pending[0].ID != "c2" {
		t.Fatalf("pending = %v, want the wire_money call parked", res.Pending)
	}
}

// With no names, every tool call of the run parks.
func TestParkAllExceptWithNoNamesParksEverything(t *testing.T) {
	var ran atomic.Int64
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "lookup", ID: "c1"}),
	), countedTool("lookup", &ran))
	res, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.ParkAllExcept())
	if err != nil {
		t.Fatal(err)
	}
	if ran.Load() != 0 || len(res.Pending) != 1 {
		t.Errorf("ran = %d, pending = %v; want the call parked", ran.Load(), res.Pending)
	}
}

// A Subagent's child run inherits the rule and applies it to its own
// tools: the child's unnamed tool parks, and the parked child surfaces
// the way a child approval boundary always has — SUBAGENT_PENDING on
// the delegating call (ADR 0014). Under ParkOn the child's tool ran.
func TestParkAllExceptReachesSubagentChildRuns(t *testing.T) {
	var wired atomic.Int64
	child := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "wire_money", ID: "k1"}),
		wefttest.Say("wired"),
	), countedTool("wire_money", &wired))
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "treasury", Args: `{"prompt":"pay the invoice"}`, ID: "c1"}),
		wefttest.Say("could not pay"),
	), weft.Subagent("treasury", "Moves money.", child))
	res, err := parent.Generate(context.Background(), weft.Prompt("x"), weft.ParkAllExcept("treasury"))
	if err != nil {
		t.Fatal(err)
	}
	if wired.Load() != 0 {
		t.Error("the child run's unnamed tool executed")
	}
	got := res.Steps[0].Results[0]
	if !got.IsError || !strings.HasPrefix(got.Content, weft.CodeSubagentPending) {
		t.Errorf("delegating call result = %+v, want %s", got, weft.CodeSubagentPending)
	}
	if len(res.Pending) != 0 {
		t.Errorf("parent pending = %v, want none (the child's boundary is the child's)", res.Pending)
	}
}

// Approve on the resuming run executes the parked call once, even
// though the resume carries the same rule — the ParkOn resume path —
// and a later call to the same tool parks again.
func TestParkAllExceptApproveResumesOnce(t *testing.T) {
	var ran atomic.Int64
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c1"}),
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c2"}),
	), countedTool("refund", &ran))
	res1, err := agt.Generate(context.Background(), weft.Prompt("x"), weft.ParkAllExcept())
	if err != nil {
		t.Fatal(err)
	}
	if ran.Load() != 0 || len(res1.Pending) != 1 {
		t.Fatalf("ran = %d, pending = %v; want c1 parked", ran.Load(), res1.Pending)
	}
	res2, err := agt.Generate(context.Background(),
		weft.Messages(res1.Messages...), weft.Approve("c1"), weft.ParkAllExcept())
	if err != nil {
		t.Fatal(err)
	}
	if ran.Load() != 1 {
		t.Errorf("the approved call ran %d times, want exactly 1", ran.Load())
	}
	if len(res2.Pending) != 1 || res2.Pending[0].ID != "c2" {
		t.Errorf("pending after the resume = %v, want the next call (c2) parked", res2.Pending)
	}
}

// The rules add up toward parking: ParkOn parks a tool the except-list
// names, a RequireApproval tool parks whether named or not, several
// ParkAllExcept options let through only what every one of them names,
// and OnlyTools keeps narrowing on its own (a narrowed-out tool is
// unknown, named or not).
func TestParkAllExceptComposes(t *testing.T) {
	var looked, refunded, guarded atomic.Int64
	guard := weft.Tool("guarded", "Guarded.", func(context.Context, struct{}) (string, error) {
		guarded.Add(1)
		return "ok", nil
	}, weft.RequireApproval())
	build := func() *weft.Agent {
		return weft.New(wefttest.Script(
			wefttest.ToolCalls(
				wefttest.Call{Name: "lookup", ID: "c1"},
				wefttest.Call{Name: "refund", ID: "c2"},
				wefttest.Call{Name: "guarded", ID: "c3"},
			),
		), countedTool("lookup", &looked), countedTool("refund", &refunded), guard)
	}
	pendingIDs := func(res *weft.RunResult) string {
		var ids []string
		for _, c := range res.Pending {
			ids = append(ids, c.ID)
		}
		return strings.Join(ids, ",")
	}

	res, err := build().Generate(context.Background(), weft.Prompt("x"),
		weft.ParkAllExcept("lookup", "refund", "guarded"), weft.ParkOn("refund"))
	if err != nil {
		t.Fatal(err)
	}
	if got := pendingIDs(res); got != "c2,c3" || looked.Load() != 1 || refunded.Load() != 0 || guarded.Load() != 0 {
		t.Errorf("with ParkOn: pending = %q, lookup ran %d, refund ran %d, guarded ran %d; want c2,c3 parked and lookup alone run",
			got, looked.Load(), refunded.Load(), guarded.Load())
	}

	looked.Store(0)
	res, err = build().Generate(context.Background(), weft.Prompt("x"),
		weft.ParkAllExcept("lookup", "refund"), weft.ParkAllExcept("lookup"))
	if err != nil {
		t.Fatal(err)
	}
	if got := pendingIDs(res); got != "c2,c3" || looked.Load() != 1 || refunded.Load() != 0 {
		t.Errorf("two except-lists: pending = %q, lookup ran %d, refund ran %d; want only the tool both name to run",
			got, looked.Load(), refunded.Load())
	}

	// OnlyTools still validates its own names and narrows first.
	_, err = build().Generate(context.Background(), weft.Prompt("x"),
		weft.OnlyTools("nope"), weft.ParkAllExcept("nope"))
	if !errors.Is(err, weft.ErrInvalidRunOption) {
		t.Errorf("OnlyTools with an unknown name under ParkAllExcept: err = %v, want ErrInvalidRunOption", err)
	}
}

// The fingerprint names the rule on its own attribute — a sorted set,
// in the hash — and leaves weft.override.park_on to ParkOn. A child run
// that only inherited the rule changed nothing itself and carries none.
func TestParkAllExceptOverrideFingerprint(t *testing.T) {
	tp := newRecProvider()
	agt := weft.New(wefttest.Script(
		wefttest.Say("ok"), wefttest.Say("ok"), wefttest.Say("ok"), wefttest.Say("ok"),
	), weft.Name("demo"), weft.TracerProvider(tp), lookupTool(), refundTool())
	run := func(id string, opts ...weft.RunOption) map[string]string {
		t.Helper()
		all := append([]weft.RunOption{weft.RunID(id)}, opts...)
		if _, err := agt.Generate(context.Background(), all...); err != nil {
			t.Fatal(err)
		}
		return spanAttrsByID(t, tp, id)
	}
	a := run("p1", weft.ParkAllExcept("refund", "lookup"))
	b := run("p2", weft.ParkAllExcept("lookup", "refund", "lookup"))
	c := run("p3", weft.ParkAllExcept("lookup"))
	d := run("p4", weft.ParkAllExcept())
	if got := a["weft.override.park_all_except"]; got != "lookup,refund" {
		t.Errorf("weft.override.park_all_except = %q, want %q", got, "lookup,refund")
	}
	if _, has := a["weft.override.park_on"]; has {
		t.Error("ParkAllExcept wrote weft.override.park_on; that attribute is ParkOn's")
	}
	if a["weft.override.hash"] == "" || a["weft.override.hash"] != b["weft.override.hash"] {
		t.Errorf("the same except-set hashed %q and %q, want one non-empty hash", a["weft.override.hash"], b["weft.override.hash"])
	}
	if a["weft.override.hash"] == c["weft.override.hash"] {
		t.Error("a different except-set hashed equal")
	}
	if v, has := d["weft.override.park_all_except"]; !has || v != "" {
		t.Errorf("an empty except-list: weft.override.park_all_except = %q (present %v), want present and empty", v, has)
	}
	if d["weft.override.hash"] == "" || d["weft.override.hash"] == c["weft.override.hash"] {
		t.Error("park-everything carries no hash of its own")
	}
}

// A Subagent child built with Output answers through submit_output —
// the run's answer, not a side effect. Under an inherited ParkAllExcept
// that does not name submit_output (the parent has no Output of its
// own, so nothing put the name on the list) the submission parked: the
// child ended pending and every delegation came back SUBAGENT_PENDING,
// so a playground re-run of any agent delegating to an Output child
// could never get its answer.
func TestParkAllExceptLetsTheOutputSubmissionThrough(t *testing.T) {
	type verdict struct {
		OK bool `json:"ok"`
	}
	judge := func() *weft.Agent {
		return weft.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "submit_output", Args: `{"ok":true}`, ID: "k1"}),
		), weft.Output[verdict]())
	}
	parent := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "judge", Args: `{"prompt":"judge it"}`, ID: "c1"}),
		wefttest.Say("judged"),
	), weft.Subagent("judge", "Judges.", judge()))
	res, err := parent.Generate(context.Background(), weft.Prompt("x"), weft.ParkAllExcept("judge"))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Steps[0].Results[0]; got.IsError || got.Content != `{"ok":true}` {
		t.Errorf("delegating call result = %+v, want the child's submission", got)
	}

	// The agent's own submission, top level, with nothing named.
	if v, _, err := weft.GenerateAs[verdict](context.Background(), judge(), weft.Prompt("x"), weft.ParkAllExcept()); err != nil || !v.OK {
		t.Errorf("GenerateAs under ParkAllExcept() = %+v, %v; want the submission", v, err)
	}

	// ParkOn still parks it when asked to by name: only the default-deny
	// rule treats the submission as the answer it is.
	res, err = judge().Generate(context.Background(), weft.Prompt("x"), weft.ParkOn("submit_output"))
	if err != nil || len(res.Pending) != 1 {
		t.Errorf("ParkOn(submit_output): pending %v, err %v; want it parked", res.Pending, err)
	}

	// A tool merely named submit_output on an agent without Output is an
	// ordinary tool: it parks.
	var ran atomic.Int64
	plain := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "submit_output", ID: "p1"}),
	), countedTool("submit_output", &ran))
	res, err = plain.Generate(context.Background(), weft.Prompt("x"), weft.ParkAllExcept())
	if err != nil || ran.Load() != 0 || len(res.Pending) != 1 {
		t.Errorf("a plain submit_output tool: ran %d, pending %v, err %v; want it parked", ran.Load(), res.Pending, err)
	}
}
