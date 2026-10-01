package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/weftgo/weft"
)

// scriptedTranscript is one recorded turn: a lookup call, its result,
// and the reply.
func scriptedTranscript() []weft.Message {
	return []weft.Message{
		weft.User("where is my order #4411?"),
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ToolCallPart{ID: "c1", Name: "lookup_order", Args: []byte(`{"order_id":"4411"}`)},
		}},
		{Role: weft.RoleTool, Content: []weft.Part{weft.ToolResultPart{CallID: "c1", Name: "lookup_order", Content: "shipped"}}},
		weft.Assistant("Your order shipped yesterday."),
	}
}

// TestScriptedEngine pins §5.5: the recorded assistant messages answer
// matching requests byte-for-byte (zero tokens), a request that
// changed the model's input misses with "no recorded turn" — never a
// silent wrong answer — and the guard refuses the prompt trap.
func TestScriptedEngine(t *testing.T) {
	lookup := weft.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) {
		return "shipped", nil
	})
	// The key's tool list is what the agent will advertise — the
	// manifest's set (a different set is a miss, pinned below).
	tools := []string{"lookup_order"}

	// The same input replays the same answer, tool calls included.
	model := newScriptedModel(scriptedTranscript(), tools)
	agt := weft.New(model, weft.Name("a"), lookup)
	res, err := agt.Generate(context.Background(), weft.Prompt("where is my order #4411?"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "Your order shipped yesterday." {
		t.Errorf("scripted reply = %q, want the recorded one", res.Text())
	}
	if res.Steps[0].Results[0].Content != "shipped" {
		t.Errorf("the tool ran for real (%q) — a scripted run re-executes the agent's tools", res.Steps[0].Results[0].Content)
	}
	if res.Usage.InputTokens != 0 || res.Usage.OutputTokens != 0 {
		t.Errorf("scripted usage = %+v, want zero tokens (D1/C6)", res.Usage)
	}

	// A narrowed tool set changes the key: the miss is loud.
	narrow := weft.New(newScriptedModel(scriptedTranscript(), []string{"lookup_order", "refund"}), weft.Name("a"), lookup)
	_, err = narrow.Generate(context.Background(), weft.Prompt("where is my order #4411?"),
		weft.OnlyTools("lookup_order"))
	if err == nil || !strings.Contains(err.Error(), "no recorded turn") {
		t.Errorf("narrowed tools err = %v, want the no-recorded-turn miss", err)
	}

	// An edited input changes the messages: the miss is loud.
	_, err = weft.New(newScriptedModel(scriptedTranscript(), tools), weft.Name("a"), lookup).
		Generate(context.Background(), weft.Prompt("a different question entirely"))
	if err == nil || !strings.Contains(err.Error(), "no recorded turn") {
		t.Errorf("changed input err = %v, want the no-recorded-turn miss", err)
	}

	// Continued from step 1 (the tool message is the new input's
	// prefix): the recorded reply answers.
	prefix := scriptedTranscript()[:3]
	cont := weft.New(newScriptedModel(scriptedTranscript(), tools), weft.Name("a"), lookup)
	res, err = cont.Generate(context.Background(), weft.Messages(prefix...))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "Your order shipped yesterday." {
		t.Errorf("continued scripted reply = %q", res.Text())
	}
}

// TestScriptedEngineNoSilentPromptReplay pins the trap directly: the
// key ignores the system prompt (wefttest's rule), so the same
// messages under a different prompt still replay — which is exactly
// why the executor refuses scripted + an instructions override
// (pinned in TestValidate) rather than trusting the miss.
func TestScriptedEngineNoSilentPromptReplay(t *testing.T) {
	lookup := weft.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) {
		return "shipped", nil
	})
	model := newScriptedModel(scriptedTranscript(), []string{"lookup_order"})
	agt := weft.New(model, weft.Name("a"), lookup)
	res, err := agt.Generate(context.Background(), weft.Prompt("where is my order #4411?"),
		weft.Instructions("a completely different system prompt"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "Your order shipped yesterday." {
		t.Errorf("reply = %q — the scripted engine answered a prompt experiment from the record", res.Text())
	}
}

// TestScriptedModelReasoningParts documents ADR 0004's consequence:
// signatures never stream back, so a scripted run's rebuilt transcript
// lacks them.
func TestScriptedModelReasoningParts(t *testing.T) {
	msgs := []weft.Message{
		weft.User("think and answer"),
		{Role: weft.RoleAssistant, Content: []weft.Part{
			weft.ReasoningPart{Text: "hmm", Signature: "sig-should-not-return"},
			weft.TextPart{Text: "the answer"},
		}},
	}
	var got []weft.ModelEvent
	for ev, err := range newScriptedModel(msgs, nil).Stream(context.Background(), weft.ModelRequest{
		Messages: msgs[:1],
	}) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev)
	}
	if len(got) != 3 {
		t.Fatalf("events = %d, want reasoning, text, finish", len(got))
	}
	rd, ok := got[0].(weft.ModelReasoningDelta)
	if !ok || rd.Text != "hmm" || rd.Signature != "" {
		t.Errorf("reasoning event = %+v, want the text with no signature", got[0])
	}
	// A repeated identical request drains the queue in recorded order;
	// once drained, the next one misses loudly.
	model := newScriptedModel(msgs, nil)
	model.Stream(context.Background(), weft.ModelRequest{Messages: msgs[:1]})(
		func(weft.ModelEvent, error) bool { return true })
	var drainErr error
	model.Stream(context.Background(), weft.ModelRequest{Messages: msgs[:1]})(
		func(_ weft.ModelEvent, err error) bool {
			drainErr = err
			return true
		})
	if drainErr == nil || !strings.Contains(drainErr.Error(), "no recorded turn") {
		t.Errorf("drained queue err = %v, want the no-recorded-turn miss", drainErr)
	}
}
