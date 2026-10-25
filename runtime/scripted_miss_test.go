package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/weftgo/weft/core"
)

// TestScriptedMissNamesStepAndComponent pins R2 (UX-C-07): the scripted
// engine's miss names the source step whose recorded turn was nearest
// the request, the key components that differ (in brackets: the words
// Studio maps to its sentence) and the remedy — never only a key.
func TestScriptedMissNamesStepAndComponent(t *testing.T) {
	lookup := core.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) {
		return "shipped", nil
	})
	refund := core.Tool("refund", "Refund.", func(ctx context.Context, in struct{}) (string, error) {
		return "ok", nil
	})
	const remedy = "; the scripted engine answers only what the source recorded: re-run on engine live"
	for _, c := range []struct {
		name  string
		tools []string // the list the record is keyed with
		opts  []core.Option
		run   []core.RunOption
		want  string
	}{
		{
			name:  "changed input",
			tools: []string{"lookup_order"},
			run:   []core.RunOption{core.Prompt("a different question entirely")},
			want:  "the messages differ from the recording (a changed input or tool result) [messages]",
		},
		{
			name:  "tool list",
			tools: []string{"lookup_order"},
			opts:  []core.Option{refund},
			run:   []core.RunOption{core.Prompt("where is my order #4411?")},
			want:  "the tool list differs from the recording [tools]",
		},
		{
			name:  "thinking",
			tools: []string{"lookup_order"},
			run:   []core.RunOption{core.Prompt("where is my order #4411?"), core.Thinking(core.ThinkingConfig{Level: core.ThinkHigh})},
			want:  "the request sets a thinking level the recording does not hold [thinking]",
		},
		{
			name:  "tool choice",
			tools: []string{"lookup_order"},
			opts:  []core.Option{core.ToolChoice(core.ToolChoiceConfig{Mode: core.ToolChoiceAny})},
			run:   []core.RunOption{core.Prompt("where is my order #4411?")},
			want:  "the request sets a tool choice the recording does not hold [tool_choice]",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			model := newScriptedModel(scriptedSource(), c.tools)
			agt := core.New(model, append([]core.Option{core.Name("a"), lookup}, c.opts...)...)
			_, err := agt.Generate(context.Background(), c.run...)
			if err == nil {
				t.Fatal("no miss")
			}
			key := scriptedRequestKeyOfMiss(t, err)
			want := "weft/runtime: no recorded turn for step 0 (scripted engine, key " + key + "): " + c.want + remedy
			if got := missText(err); got != want {
				t.Errorf("miss =\n  %s\nwant\n  %s", got, want)
			}
		})
	}

	// A miss past step 0: the tool result changed, so step 1's request
	// holds messages no recorded request does — the error names step 1,
	// the source's own step, not the replay's.
	other := core.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) {
		return "lost in transit", nil
	})
	_, err := core.New(newScriptedModel(scriptedSource(), []string{"lookup_order"}), core.Name("a"), other).
		Generate(context.Background(), core.Prompt("where is my order #4411?"))
	key := scriptedRequestKeyOfMiss(t, err)
	if want := "weft/runtime: no recorded turn for step 1 (scripted engine, key " + key + "): the messages differ from the recording (a changed input or tool result) [messages]" + remedy; missText(err) != want {
		t.Errorf("step-1 miss =\n  %s\nwant\n  %s", missText(err), want)
	}

	// No recorded turn at all: "this request", unanswered.
	_, err = core.New(newScriptedModel(&sourceRun{}, nil), core.Name("a")).
		Generate(context.Background(), core.Prompt("hi"))
	key = scriptedRequestKeyOfMiss(t, err)
	if want := "weft/runtime: no recorded turn for this request (scripted engine, key " + key + "): the source recorded no turn for it [unanswered]" + remedy; missText(err) != want {
		t.Errorf("empty-record miss =\n  %s\nwant\n  %s", missText(err), want)
	}
}

// missText is the scripted miss inside a run error.
func missText(err error) string {
	var m *scriptedMiss
	if !errors.As(err, &m) {
		return "not a scripted miss: " + err.Error()
	}
	return m.Error()
}

func scriptedRequestKeyOfMiss(t *testing.T, err error) string {
	t.Helper()
	var m *scriptedMiss
	if err == nil || !errors.As(err, &m) {
		t.Fatalf("err = %v, want a scripted miss", err)
	}
	return m.key
}
