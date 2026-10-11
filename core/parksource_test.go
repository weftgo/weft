package core_test

// ParkSource — a dynamic park set the agent reads once per run (ADR
// 0030): the playground runtime's debugger breakpoints reaching the
// runs the app starts itself. The ParkOn tests are the model: what a
// source answers parks exactly as ParkOn(names...) would.

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// The source's names join the run's ParkOn names: both park. Several
// sources add up; a nil fn is ignored.
func TestParkSourceMergesWithParkOn(t *testing.T) {
	var a, b, c atomic.Int64
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "a", ID: "c1"},
			wefttest.Call{Name: "b", ID: "c2"},
			wefttest.Call{Name: "c", ID: "c3"},
		),
	), countedTool("a", &a), countedTool("b", &b), countedTool("c", &c),
		core.ParkSource(func(context.Context) []string { return []string{"a"} }),
		core.ParkSource(nil),
		core.ParkSource(func(context.Context) []string { return []string{"b"} }))
	if n := len(agt.ParkSources()); n != 2 {
		t.Errorf("ParkSources() = %d, want 2 (the nil fn ignored)", n)
	}
	res, err := agt.Generate(context.Background(), core.Prompt("x"), core.ParkOn("c"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Load()+b.Load()+c.Load() != 0 || len(res.Pending) != 3 {
		t.Errorf("ran a=%d b=%d c=%d, pending %v; want all three parked", a.Load(), b.Load(), c.Load(), res.Pending)
	}
}

// A nil answer, an empty one and a panicking source park nothing and
// leave the run as it is without a source: events, transcript and
// span attributes byte-equal. The panic is one Debug line.
func TestParkSourceNothingChangesNothing(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	script := func() core.Model {
		return wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "c1"}),
			wefttest.Say("refunded"),
		)
	}
	run := func(id string, opts ...core.Option) (string, map[string]string) {
		t.Helper()
		tp := newRecProvider()
		var ran atomic.Int64
		var toolMD map[string]string
		// The handler's metadata, not only RunStart's: what the run
		// passes on past the source's read (handlers, child runs).
		refund := core.Tool("refund", "refund.", func(ctx context.Context, _ struct{}) (string, error) {
			ran.Add(1)
			toolMD = core.MetadataFromContext(ctx)
			return "refund done", nil
		})
		all := append([]core.Option{core.Name("demo"), core.TracerProvider(tp), core.Logger(logger), refund}, opts...)
		agt := core.New(script(), all...)
		var evs []string
		for ev, err := range agt.Stream(context.Background(), core.RunID(id), core.Prompt("x"),
			core.Metadata(map[string]string{"tenant": "acme"})).Events() {
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(ev)
			evs = append(evs, string(b))
		}
		if ran.Load() != 1 {
			t.Errorf("%s: refund ran %d times, want 1", id, ran.Load())
		}
		b, _ := json.Marshal(toolMD)
		evs = append(evs, "tool metadata "+string(b))
		return strings.Join(evs, "\n"), spanAttrsByID(t, tp, id)
	}
	base, baseAttrs := run("r")
	for name, src := range map[string]func(context.Context) []string{
		"nil":      func(context.Context) []string { return nil },
		"empty":    func(context.Context) []string { return []string{} },
		"panicked": func(context.Context) []string { panic("boom") },
	} {
		got, attrs := run("r", core.ParkSource(src))
		if got != base {
			t.Errorf("%s source: events differ\n got %s\nwant %s", name, got, base)
		}
		for k, v := range baseAttrs {
			if attrs[k] != v {
				t.Errorf("%s source: span attr %s = %q, want %q", name, k, attrs[k], v)
			}
		}
		if len(attrs) != len(baseAttrs) {
			t.Errorf("%s source: %d span attrs, want %d", name, len(attrs), len(baseAttrs))
		}
	}
	if n := strings.Count(logs.String(), "park source panicked"); n != 1 {
		t.Errorf("%d 'park source panicked' lines, want 1:\n%s", n, logs.String())
	}
}

// A configured source is behaviour, not an override: the run carries no
// weft.override.* attribute for it.
func TestParkSourceIsNotAnOverride(t *testing.T) {
	tp := newRecProvider()
	agt := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("demo"), core.TracerProvider(tp), lookupTool(),
		core.ParkSource(func(context.Context) []string { return []string{"lookup"} }))
	if _, err := agt.Generate(context.Background(), core.RunID("p"), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	for k := range spanAttrsByID(t, tp, "p") {
		if strings.HasPrefix(k, "weft.override.") {
			t.Errorf("a park source wrote %s", k)
		}
	}
}

// A tool only a ToolSource supplies parks by name; the source receives
// the run's context (its metadata placed).
func TestParkSourceParksToolSourceTools(t *testing.T) {
	var wired atomic.Int64
	reg := []*core.ToolDef{countedTool("wire_money", &wired)}
	var tenant string
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "wire_money", ID: "c1"}),
	), core.ToolSource(func() []*core.ToolDef { return reg }),
		core.ParkSource(func(ctx context.Context) []string {
			tenant = core.MetadataFromContext(ctx)["tenant"]
			return []string{"wire_money"}
		}))
	res, err := agt.Generate(context.Background(), core.Prompt("x"), core.Metadata(map[string]string{"tenant": "acme"}))
	if err != nil {
		t.Fatal(err)
	}
	if wired.Load() != 0 || len(res.Pending) != 1 || res.Pending[0].ID != "c1" {
		t.Errorf("ran %d, pending %v; want the ToolSource tool parked", wired.Load(), res.Pending)
	}
	if tenant != "acme" {
		t.Errorf("source saw metadata tenant %q, want acme", tenant)
	}
}

// A Subagent tool's call parks before its child runs; and, like ParkOn,
// a parent's source does not reach the child's run — a child built with
// its own source parks its own tool (SUBAGENT_PENDING to the parent).
func TestParkSourceAndSubagents(t *testing.T) {
	var wired atomic.Int64
	child := func(opts ...core.Option) *core.Agent {
		return core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "wire_money", ID: "k1"}),
			wefttest.Say("wired"),
		), append([]core.Option{countedTool("wire_money", &wired)}, opts...)...)
	}
	parent := func(c *core.Agent, park string) *core.Agent {
		return core.New(wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "treasury", Args: `{"prompt":"pay"}`, ID: "c1"}),
			wefttest.Say("done"),
		), core.Subagent("treasury", "Moves money.", c),
			core.ParkSource(func(context.Context) []string { return []string{park} }))
	}
	// The delegating call itself parks.
	res, err := parent(child(), "treasury").Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if wired.Load() != 0 || len(res.Pending) != 1 || res.Pending[0].Name != "treasury" {
		t.Fatalf("ran %d, pending %v; want the treasury call parked", wired.Load(), res.Pending)
	}
	// The parent's source names the child's tool: the child does not read it.
	if _, err := parent(child(), "wire_money").Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if wired.Load() != 1 {
		t.Fatalf("child tool ran %d times, want 1 (a parent's source covers its own run)", wired.Load())
	}
	// A child built with its own source parks its own call.
	res, err = parent(child(core.ParkSource(func(context.Context) []string { return []string{"wire_money"} })), "none").
		Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	got := res.Steps[0].Results[0]
	if wired.Load() != 1 || !got.IsError || !strings.HasPrefix(got.Content, core.CodeSubagentPending) {
		t.Errorf("ran %d, delegating result %+v; want the child's call parked (%s)", wired.Load(), got, core.CodeSubagentPending)
	}
}
