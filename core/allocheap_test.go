package core_test

// The whole-run no-SDK allocation bound (ADR 0024 S1.3's acceptance,
// beside TestObserverNoopAllocations — which bounds only one span's
// start+end). A run with no tracer provider and no logger provider —
// the default program's configuration — must allocate no more than the
// bound below, which was measured on the commit that introduced the
// record emission. The no-SDK path is the default path: Enabled answers
// false, nothing is marshalled, and the records cost nothing; if a
// change regresses this number it made the default path pay for
// observability nobody asked for.

import (
	"context"
	"iter"
	"testing"

	"github.com/weftgo/weft/core"
)

// alternatingModel serves any number of runs: each run asks once with a
// tool call, then answers in text — a deterministic two-step run that
// needs no script state across Generate calls.
type alternatingModel struct{}

func (alternatingModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return func(yield func(core.ModelEvent, error) bool) {
		// The fresh input is one user message; the second call sees the
		// tool result.
		seen := false
		for _, m := range req.Messages {
			if m.Role == core.RoleTool {
				seen = true
			}
		}
		if !seen {
			yield(core.ModelToolCall{ID: "c1", Name: "echo", Args: []byte(`{"msg":"hi"}`)}, nil)
			yield(core.ModelFinish{Reason: core.StopToolCalls, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}}, nil)
			return
		}
		yield(core.ModelTextDelta{Text: "done"}, nil)
		yield(core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}}, nil)
	}
}

func allocEcho() *core.ToolDef {
	return core.Tool("echo", "Echo.", func(ctx context.Context, in struct {
		Msg string `json:"msg"`
	}) (string, error) {
		return "echo: " + in.Msg, nil
	})
}

func TestWholeRunNoSDKAllocations(t *testing.T) {
	// No TracerProvider, no LoggerProvider: both resolve to the global
	// no-ops, spans are non-recording and Enabled answers false, so the
	// record path marshals nothing.
	agt := core.New(alternatingModel{}, allocEcho())
	// Bound measured 2026-09-30 on the commit that introduced the
	// record emission: 128 allocs/op for one two-step run (input
	// prompt, tool call, result, final text) — the same count as the
	// commit before it, because Enabled answers false before anything
	// is marshalled. 160 leaves headroom for platform noise.
	const bound = 160.0
	n := testing.AllocsPerRun(50, func() {
		if _, err := agt.Generate(context.Background(), core.Prompt("hello")); err != nil {
			t.Fatal(err)
		}
	})
	if n > bound {
		t.Errorf("whole run allocated %.0f times, bound %.0f — the default path pays for observability nobody asked for", n, bound)
	}
}

func BenchmarkWholeRunNoSDK(b *testing.B) {
	agt := core.New(alternatingModel{}, allocEcho())
	b.ReportAllocs()
	for b.Loop() {
		if _, err := agt.Generate(context.Background(), core.Prompt("hello")); err != nil {
			b.Fatal(err)
		}
	}
}
