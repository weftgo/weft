package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
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

// TestScriptedMissPastTheRecordedEnd (U18.1-fix item 1): a source that
// ended on a tool call — its model failed, or it was canceled, before
// the next step — replays every recorded step and then asks the step
// it never answered: the miss names that step (the source's count) and
// `unanswered`, as the preview's scripted_hit does, never step 0 and
// messages.
func TestScriptedMissPastTheRecordedEnd(t *testing.T) {
	msgs := scriptedTranscript()[:3] // the prompt, the lookup call, its result
	src := &sourceRun{input: msgs[:1], steps: msgs[1:]}
	lookup := core.Tool("lookup_order", "Look up.", func(ctx context.Context, in struct{}) (string, error) {
		return "shipped", nil
	})
	_, err := core.New(newScriptedModel(src, []string{"lookup_order"}), core.Name("a"), lookup).
		Generate(context.Background(), core.Prompt("where is my order #4411?"))
	key := scriptedRequestKeyOfMiss(t, err)
	if want := "weft/runtime: no recorded turn for step 1 (scripted engine, key " + key + "): the source recorded no turn for it [unanswered]; the scripted engine answers only what the source recorded: re-run on engine live"; missText(err) != want {
		t.Errorf("past-the-end miss =\n  %s\nwant\n  %s", missText(err), want)
	}
}

// TestScriptedMissNamesAToolTheAgentLacks (U18.1-fix item 6): a
// recorded call to a tool the replayed agent no longer has gets the
// loop's unknown-tool result, so the next step's messages differ — the
// miss names that step and `tools` (the preview's words), not messages.
func TestScriptedMissNamesAToolTheAgentLacks(t *testing.T) {
	recorded := []core.Message{
		core.User("find the docs"),
		{Role: core.RoleAssistant, Content: []core.Part{core.ToolCallPart{ID: "c1", Name: "search_docs", Args: []byte(`{}`)}}},
		{Role: core.RoleTool, Content: []core.Part{core.ToolResultPart{CallID: "c1", Name: "search_docs", Content: "found"}}},
		core.Assistant("here they are"),
	}
	src := &sourceRun{input: recorded[:1], steps: recorded[1:]}
	web := core.Tool("web_search", "Search.", func(ctx context.Context, in struct{}) (string, error) { return "x", nil })
	_, err := core.New(newScriptedModel(src, []string{"web_search"}), core.Name("a"), web).
		Generate(context.Background(), core.Prompt("find the docs"))
	key := scriptedRequestKeyOfMiss(t, err)
	if want := "weft/runtime: no recorded turn for step 1 (scripted engine, key " + key + "): the tool list differs from the recording [tools]; the scripted engine answers only what the source recorded: re-run on engine live"; missText(err) != want {
		t.Errorf("missing-tool miss =\n  %s\nwant\n  %s", missText(err), want)
	}
}

// TestFinishedAckCarriesTheTypedMiss (U18.1-fix item 5): a run that
// failed with the scripted miss posts its finished ack with reason
// {kind, step, components} beside the error text — which maxAckError
// may cut — and a run that failed otherwise posts none.
func TestFinishedAckCarriesTheTypedMiss(t *testing.T) {
	f := newFakeStudio(t)
	ts := httptest.NewServer(f.handler())
	t.Cleanup(ts.Close)
	cfg := &config{}
	l := newLink(cfg, newRegistry(cfg), ts.URL, "")
	miss := fmt.Errorf("weft: run failed at step 0: model stream: %w", &scriptedMiss{step: 2, components: []string{"tools", "messages"}, key: "00"})
	for _, c := range []struct {
		id  string
		err error
	}{{"cmd_miss", miss}, {"cmd_plain", errors.New("boom")}} {
		status, errText := l.outcome(command{CommandID: c.id}, "pg_"+c.id, nil, c.err, core.Usage{}, nil)
		l.postAck(ack{CommandID: c.id, State: "finished", RunID: "pg_" + c.id, Status: status, Error: errText})
	}
	got := f.ackOf(t, "cmd_miss", "finished")
	if r := got.Reason; r == nil || r.Kind != "scripted_miss" || r.Step != 2 || fmt.Sprint(r.Components) != "[tools messages]" {
		t.Errorf("miss ack reason = %+v, want scripted_miss step 2 [tools messages]", r)
	}
	if r := f.ackOf(t, "cmd_plain", "finished").Reason; r != nil {
		t.Errorf("plain failure ack reason = %+v, want none", r)
	}
	l.mu.Lock()
	left := len(l.misses)
	l.mu.Unlock()
	if left != 0 {
		t.Errorf("%d misses left after their acks, want 0", left)
	}
}

// TestRegistrationCarriesTheThinkingBudget (U18.1-fix item 2): a
// budget-only default thinking (no level) registers its budget, so
// Studio's preview knows the request sets thinking, which the scripted
// engine keys on.
func TestRegistrationCarriesTheThinkingBudget(t *testing.T) {
	agent := core.New(wefttest.Script(), core.Name("a"), core.Thinking(core.ThinkingConfig{Budget: 2048}))
	cfg := &config{agents: []*core.Agent{agent}}
	d := newRegistry(cfg).entries["a"].Defaults
	if d.Thinking != "" || d.ThinkingBudget != 2048 {
		t.Errorf("defaults thinking = %q budget %d, want \"\" and 2048", d.Thinking, d.ThinkingBudget)
	}
}
