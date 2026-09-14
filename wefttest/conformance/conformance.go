// Package conformance is the provider adapter contract as an executable
// table. Every first-party adapter (weft/openai, weft/anthropic,
// weft/google) runs Run in its own tests: offline against recorded
// fixtures and live behind the `live` build tag. When Run is green for
// an adapter, the adapter honours the contract — the suite *is* the
// documented adapter contract (ADR 0013).
//
// The package lives in the root module and imports no vendor SDK: it
// drives the model through weft's public API only, so it can also run
// against wefttest models (proving the harness itself).
package conformance

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weftgo/weft"
)

// Caps declares what the adapter under test supports, so the suite
// asserts the right behaviour instead of skipping silently.
type Caps struct {
	// Reasoning: the adapter emits ModelReasoningDelta and accepts a
	// ReasoningPart (with signature) back.
	Reasoning bool
	// Files: the adapter accepts a FilePart image input.
	Files bool
	// Sequential: the adapter forwards SequentialTools and the provider
	// honours the hint (emits at most one call per step).
	Sequential bool
	// Usage: the provider reports non-zero token usage. False for
	// compatible servers that never send usage — the adapter must not
	// fake numbers, so the caller declares the gap instead.
	Usage bool
	// Live: the model is reached over the network. The suite relaxes
	// determinism (a live model may make a different but valid choice;
	// those cases t.Skip rather than fail) and skips fixture-only cases
	// (idle_timeout).
	Live bool
}

// probeIn and probeOut are the suite's tool contract: a struct input
// reflected into a schema, and a struct output the model must read back.
type probeIn struct {
	N int `json:"n" jsonschema:"the number to report and double"`
}

type probeOut struct {
	N       int `json:"n"`
	Doubled int `json:"doubled"`
}

// probeTool is the suite's own tool: call it with n, get n and 2n back.
func probeTool() *weft.ToolDef {
	return weft.Tool("probe", "Reports n and its double.", func(ctx context.Context, in probeIn) (probeOut, error) {
		return probeOut{N: in.N, Doubled: in.N * 2}, nil
	})
}

// parallelPrompt is owned by the suite so every adapter is asked the
// same thing: three independent probe calls in one turn.
const parallelPrompt = "Call `probe` three times, once each with n=1, n=2, n=3, in one turn."

// contractDetector collects ErrModelContract sightings across a whole
// Run and fails the suite at Cleanup — the never_contract_violation
// guarantee has to hold for every case, not one.
type contractDetector struct {
	violated bool
}

func (d *contractDetector) check(err error) {
	if err != nil && errors.Is(err, weft.ErrModelContract) {
		d.violated = true
	}
}

func (d *contractDetector) failIfViolated(t *testing.T) {
	t.Helper()
	if d.violated {
		t.Error("adapter violated the model stream contract (ErrModelContract)")
	}
}

// Run executes every conformance case as a subtest named after its
// fixture key. newModel is called once per case (cases share no state)
// and receives the case name so it can pick its fixture recording or
// apply case-specific construction — max_tokens, notably, must build
// the adapter with its token-limit option.
func Run(t *testing.T, caps Caps, newModel func(t *testing.T, name string) weft.Model) {
	t.Helper()

	detector := new(contractDetector)
	t.Cleanup(func() { detector.failIfViolated(t) })

	// generate runs one case's exchange through the public API, with the
	// suite's tool registered and a generous per-case deadline (the live
	// mode needs it; offline cases finish instantly).
	generate := func(t *testing.T, m weft.Model, agentOpts []weft.Option, runOpts ...weft.RunOption) (*weft.RunResult, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		opts := append([]weft.Option{probeTool()}, agentOpts...)
		res, err := weft.New(m, opts...).Generate(ctx, runOpts...)
		detector.check(err)
		return res, err
	}

	t.Run("text_only", func(t *testing.T) {
		res, err := generate(t, newModel(t, "text_only"), nil, weft.Prompt("Reply with one short sentence."))
		if err != nil {
			t.Fatal(err)
		}
		if res.NumSteps() != 1 {
			t.Errorf("steps = %d, want 1", res.NumSteps())
		}
		if res.StopReason != weft.StopEndTurn {
			t.Errorf("StopReason = %q, want %q", res.StopReason, weft.StopEndTurn)
		}
		if res.Text() == "" {
			t.Error("Text() is empty")
		}
		if caps.Usage && res.Usage.Total() == 0 {
			t.Errorf("usage = %+v, want non-zero", res.Usage)
		}
	})

	// The run-level thinking option must thread through the public API
	// without failing the exchange — wire-shape assertions (the exact
	// param each provider receives) live in the adapters' own unit
	// tests, where the request body is recordable.
	t.Run("thinking_option", func(t *testing.T) {
		res, err := generate(t, newModel(t, "text_only"), nil,
			weft.Thinking(weft.ThinkingConfig{Level: weft.ThinkOff}),
			weft.Prompt("Reply with one short sentence."))
		if err != nil {
			t.Fatal(err)
		}
		if res.NumSteps() != 1 || res.Text() == "" {
			t.Errorf("steps = %d, text = %q; the thinking option broke the exchange", res.NumSteps(), res.Text())
		}
	})

	t.Run("tool_roundtrip_struct", func(t *testing.T) {
		res, err := generate(t, newModel(t, "tool_roundtrip_struct"), nil,
			weft.Prompt("Call `probe` with n=3, then tell me the doubled value it returned."))
		if err != nil {
			t.Fatal(err)
		}
		var sawCall, sawResult bool
		for _, c := range res.Steps[0].ToolCalls {
			if c.Name == "probe" && len(c.Args) > 0 {
				sawCall = true
			}
		}
		for _, msg := range res.Messages {
			if msg.Role != weft.RoleTool {
				continue
			}
			for _, p := range msg.Content {
				if tr, ok := p.(weft.ToolResultPart); ok && tr.Name == "probe" && strings.Contains(tr.Content, "6") {
					sawResult = true
				}
			}
		}
		if !sawCall {
			if caps.Live {
				t.Skip("model answered without calling the tool (valid choice)")
			}
			t.Fatal("the first step did not call probe with arguments")
		}
		if !sawResult {
			t.Error("no probe result carries the doubled value; the struct output did not round-trip")
		}
		if !strings.Contains(res.Text(), "6") {
			t.Errorf("final text %q does not mention the tool's doubled value", res.Text())
		}
	})

	t.Run("parallel_three_calls", func(t *testing.T) {
		res, err := generate(t, newModel(t, "parallel_three_calls"), nil, weft.Prompt(parallelPrompt))
		if err != nil {
			t.Fatal(err)
		}
		calls := res.Steps[0].ToolCalls
		if len(calls) == 0 {
			if caps.Live {
				t.Skip("model made no tool calls (valid choice)")
			}
			t.Fatal("the first step made no tool calls")
		}
		if len(calls) != 3 {
			if caps.Live && len(calls) < 3 {
				t.Skipf("model made %d calls, not 3 (valid choice; the fixture pins 3)", len(calls))
			}
			t.Errorf("the first step made %d calls, want 3", len(calls))
		}
		ids := map[string]bool{}
		for _, c := range calls {
			if c.ID == "" || c.Name == "" {
				t.Errorf("call %+v has an empty ID or name", c)
			}
			ids[c.ID] = true
		}
		if len(calls) == 3 && len(ids) != 3 {
			t.Errorf("%d distinct call IDs for 3 calls", len(ids))
		}
		// The step's results are batched on one RoleTool message (a core
		// rule, but the adapter must not have upset it).
		for _, msg := range res.Messages {
			if msg.Role != weft.RoleTool {
				continue
			}
			if got := len(msg.Content); got != len(calls) {
				t.Errorf("a tool message carries %d results, want %d on one batched message", got, len(calls))
			}
			break
		}
	})

	if caps.Usage {
		t.Run("usage_nonzero", func(t *testing.T) {
			res, err := generate(t, newModel(t, "usage_nonzero"), nil,
				weft.Prompt("Call `probe` with n=1, then tell me the doubled value."))
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Steps) == 0 {
				t.Fatal("no steps ran")
			}
			for i, s := range res.Steps {
				if s.Usage.InputTokens <= 0 || s.Usage.OutputTokens <= 0 {
					t.Errorf("step %d usage = %+v, want input and output > 0", i, s.Usage)
				}
			}
		})
	}

	t.Run("cancel_mid_stream", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var (
			runErr     error
			errs       int
			afterError int
			sawDelta   bool
		)
		// Keep draining after the error: the contract is exactly one
		// terminal error and nothing after it, so the loop must not
		// stop at the first one.
		for ev, err := range weft.New(newModel(t, "cancel_mid_stream"), probeTool()).
			Stream(ctx, weft.Prompt("Count slowly from 1 to 50.")).Events() {
			if err != nil {
				errs++
				if runErr == nil {
					runErr = err
				}
				continue
			}
			if runErr != nil {
				afterError++
				continue
			}
			if _, ok := ev.(weft.TextDelta); ok && !sawDelta {
				sawDelta = true
				cancel()
			}
		}
		detector.check(runErr)
		if runErr == nil {
			if caps.Live {
				t.Skip("the run completed before cancellation took effect")
			}
			t.Fatal("cancelling mid-stream produced no run error")
		}
		if !sawDelta {
			t.Fatal("no TextDelta was observed before cancellation")
		}
		if !errors.Is(runErr, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", runErr)
		}
		if errs != 1 {
			t.Errorf("%d errors after cancellation, want exactly one terminal error", errs)
		}
		if afterError != 0 {
			t.Errorf("%d events after the terminal error, want none", afterError)
		}
	})

	t.Run("max_tokens", func(t *testing.T) {
		// newModel must construct the adapter with its token-limit option
		// (e.g. openai.MaxTokens(16)); the suite checks the mapping only.
		res, err := generate(t, newModel(t, "max_tokens"), nil,
			weft.Prompt("Describe a tree in as many words as you like."))
		if err != nil {
			t.Fatal(err)
		}
		if res.StopReason != weft.StopMaxTokens {
			if caps.Live && res.StopReason == weft.StopEndTurn {
				t.Skip("model finished naturally under the cap (valid choice)")
			}
			t.Errorf("StopReason = %q, want %q", res.StopReason, weft.StopMaxTokens)
		}
	})

	if caps.Sequential {
		t.Run("sequential_hint", func(t *testing.T) {
			res, err := generate(t, newModel(t, "sequential_hint"), []weft.Option{weft.Sequential()}, weft.Prompt(parallelPrompt))
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range res.Steps {
				if len(s.ToolCalls) > 1 {
					if caps.Live {
						t.Skipf("step %d emitted %d calls despite the sequential hint (best-effort)", s.Index, len(s.ToolCalls))
					}
					t.Errorf("step %d emitted %d calls under SequentialTools, want at most 1", s.Index, len(s.ToolCalls))
				}
			}
		})
	}

	if caps.Reasoning {
		t.Run("reasoning_passthrough", func(t *testing.T) {
			m := newModel(t, "reasoning_passthrough")
			res, err := generate(t, m, nil, weft.Prompt("Think briefly, then reply in one sentence."))
			if err != nil {
				t.Fatal(err)
			}
			var found bool
			for _, msg := range res.Messages {
				if msg.Role != weft.RoleAssistant {
					continue
				}
				for i, p := range msg.Content {
					if _, ok := p.(weft.ReasoningPart); ok {
						found = true
						if i > 0 {
							t.Errorf("reasoning part at index %d, want first in the message", i)
						}
					}
				}
			}
			if !found {
				if caps.Live {
					t.Skip("model returned no reasoning this time")
				}
				t.Fatal("no ReasoningPart in the transcript")
			}
			// The signature round-trips: feeding the transcript back must
			// not fail the next model call.
			if _, err := generate(t, m, nil, weft.Messages(res.Messages...), weft.Prompt("Continue in one sentence.")); err != nil {
				t.Fatalf("second run with the reasoning transcript failed: %v", err)
			}
		})
	}

	t.Run("file_input", func(t *testing.T) {
		// A minimal valid 1×1 PNG.
		png := []byte{
			0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
			0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
			0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
			0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
			0x42, 0x60, 0x82,
		}
		msg := weft.UserParts(
			weft.TextPart{Text: "What is in this image?"},
			weft.FilePart{MediaType: "image/png", Data: png},
		)
		res, err := generate(t, newModel(t, "file_input"), nil, weft.Messages(msg))
		if caps.Files {
			if err != nil {
				t.Fatalf("file input rejected: %v", err)
			}
			if res.Text() == "" {
				t.Error("empty reply for an image prompt")
			}
			return
		}
		if !errors.Is(err, weft.ErrUnsupported) {
			t.Errorf("err = %v, want ErrUnsupported", err)
		}
	})

	if !caps.Live {
		t.Run("idle_timeout", func(t *testing.T) {
			_, err := generate(t, newModel(t, "idle_timeout"), nil, weft.Prompt("Say something."))
			if !errors.Is(err, weft.ErrStreamIdle) {
				t.Errorf("err = %v, want ErrStreamIdle", err)
			}
		})
	}

	t.Run("kill_switch", func(t *testing.T) {
		// newModel should point this case at NoRequestServer (offline)
		// so a request slipping past the switch fails the test rather
		// than reaching a fixture; the sentinel alone is not the proof.
		t.Setenv("WEFT_MODEL_REQUESTS", "deny")
		_, err := generate(t, newModel(t, "kill_switch"), nil, weft.Prompt("Say something."))
		if !errors.Is(err, weft.ErrModelRequestsDenied) {
			t.Errorf("err = %v, want ErrModelRequestsDenied (and no request made)", err)
		}
	})

	t.Run("never_contract_violation", func(t *testing.T) {
		// Every case's run errors flow through the detector; the Cleanup
		// fails the suite on any ErrModelContract. This case runs one
		// plain exchange through the same path to keep the guarantee
		// exercised even in reduced runs.
		res, err := generate(t, newModel(t, "never_contract_violation"), nil, weft.Prompt("Reply with one short sentence."))
		if err != nil {
			t.Fatal(err)
		}
		if res.NumSteps() != 1 {
			t.Errorf("steps = %d, want 1", res.NumSteps())
		}
	})
}
