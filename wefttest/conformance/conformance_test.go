package conformance_test

import (
	"context"
	"fmt"
	"iter"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
	"github.com/weftgo/weft/wefttest/conformance"
)

// scriptedFor maps each case to a wefttest script or inline model. The
// harness proof: Run must be green against these, without any adapter —
// proving the suite drives models through the public API correctly.
// Case-specific model *behaviour* (stalling, denying, refusing files)
// is provided by the small inline models below, exactly the shapes an
// adapter produces from its fixtures.
func scriptedFor(t *testing.T, name string) weft.Model {
	t.Helper()
	switch name {
	case "text_only", "usage_nonzero", "never_contract_violation":
		return wefttest.Script(wefttest.Say("Hello from the model."))
	case "tool_roundtrip_struct":
		return wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "probe", Args: `{"n":3}`}),
			wefttest.Say("The doubled value is 6."),
		)
	case "parallel_three_calls":
		return wefttest.Script(
			wefttest.ToolCalls(
				wefttest.Call{Name: "probe", Args: `{"n":1}`},
				wefttest.Call{Name: "probe", Args: `{"n":2}`},
				wefttest.Call{Name: "probe", Args: `{"n":3}`},
			),
			wefttest.Say("Called all three."),
		)
	case "cancel_mid_stream":
		return stallModel{}
	case "max_tokens":
		return wefttest.Script(wefttest.MaxTokens("Four score and se"))
	case "sequential_hint":
		return wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "probe", Args: `{"n":1}`}),
			wefttest.ToolCalls(wefttest.Call{Name: "probe", Args: `{"n":2}`}),
			wefttest.ToolCalls(wefttest.Call{Name: "probe", Args: `{"n":3}`}),
			wefttest.Say("Done, one at a time."),
		)
	case "reasoning_passthrough":
		return wefttest.Script(
			wefttest.Think("plan: answer briefly", wefttest.Say("Hello.")),
			wefttest.Say("Continued."),
		)
	case "file_input":
		// An adapter without file support yields a stream error wrapping
		// ErrUnsupported — the shape the no-Files branch asserts.
		return wefttest.Script(wefttest.Fail(fmt.Errorf("%w: file parts", weft.ErrUnsupported)))
	case "idle_timeout":
		return wefttest.Script(wefttest.Fail(fmt.Errorf("%w after 60s", weft.ErrStreamIdle)))
	case "slow_stream":
		// A scripted model cannot drip; the case's substance (gaps under
		// the timeout, total over it) is the adapters' SlowServer wiring,
		// and the harness proof needs only a slow-but-succeeding shape.
		return wefttest.Script(wefttest.Say("still streaming."))
	case "tool_args_delta":
		// wefttest plays whole events only; the fragment stream the case
		// needs is the adapters' fixture. Reachable through the every-cap
		// run below with an adapter-shaped scripted model.
		return scriptedArgDeltas()
	case "kill_switch":
		return killSwitchModel{}
	}
	t.Fatalf("no scripted model for case %q", name)
	return nil
}

// TestSuiteGreenAgainstScriptedModels is the harness proof: every case
// green with Caps{} (nothing supported) against wefttest-backed and
// inline models (TODO §3.5, step "skeleton first").
func TestSuiteGreenAgainstScriptedModels(t *testing.T) {
	conformance.Run(t, conformance.Caps{}, scriptedFor)
}

// TestSuiteGreenWithEveryCap exercises the full table the way adapters
// with full capability sets run it.
func TestSuiteGreenWithEveryCap(t *testing.T) {
	conformance.Run(t, conformance.Caps{Reasoning: true, Files: true, Sequential: true, ToolArgDeltas: true, Usage: true}, func(t *testing.T, name string) weft.Model {
		switch name {
		case "file_input":
			return wefttest.Script(wefttest.Say("A transparent square."))
		default:
			return scriptedFor(t, name)
		}
	})
}

// stallModel yields one text delta, then holds the stream open until
// ctx ends and yields ctx.Err() — the scripted shape of a stream
// cancelled mid-flight.
type stallModel struct{}

func (stallModel) Stream(ctx context.Context, _ weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		if !yield(weft.ModelTextDelta{Text: "one"}, nil) {
			return
		}
		<-ctx.Done()
		yield(nil, ctx.Err())
	}
}

// killSwitchModel honours the kill switch the way every first-party
// adapter must: the check comes first, no request follows, and denial
// yields ErrModelRequestsDenied.
type killSwitchModel struct{}

func (killSwitchModel) Stream(context.Context, weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	return func(yield func(weft.ModelEvent, error) bool) {
		if !weft.ModelRequestsAllowed() {
			yield(nil, weft.ErrModelRequestsDenied)
			return
		}
		yield(weft.ModelFinish{Reason: weft.StopEndTurn}, nil)
	}
}

// argDeltaModel plays a tool call's argument fragments as
// ModelToolCallDelta progress, then the whole call and the finish; its
// second call answers in text — the scripted shape of a provider that
// streams arguments (the tool_args_delta case, proof the harness
// itself checks the rule).
type argDeltaModel struct{ calls int }

func (m *argDeltaModel) Stream(_ context.Context, _ weft.ModelRequest) iter.Seq2[weft.ModelEvent, error] {
	m.calls++
	var events []weft.ModelEvent
	if m.calls == 1 {
		events = []weft.ModelEvent{
			weft.ModelToolCallDelta{Index: 0, Name: "probe", Args: `{"n"`},
			weft.ModelToolCallDelta{Index: 0, Name: "probe", Args: `:3}`},
			weft.ModelToolCall{ID: "call_1", Name: "probe", Args: []byte(`{"n":3}`)},
			weft.ModelFinish{Reason: weft.StopToolCalls},
		}
	} else {
		events = []weft.ModelEvent{
			weft.ModelTextDelta{Text: "The doubled value is 6."},
			weft.ModelFinish{Reason: weft.StopEndTurn},
		}
	}
	return func(yield func(weft.ModelEvent, error) bool) {
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

func scriptedArgDeltas() weft.Model { return &argDeltaModel{} }
