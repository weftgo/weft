package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// onRecorder collects an OnMessages observer's calls.
type onRecorder struct {
	calls [][]core.Message
	steps []int
}

func (r *onRecorder) fn(_ context.Context, step int, msgs []core.Message) {
	r.calls = append(r.calls, msgs)
	r.steps = append(r.steps, step)
}

func (r *onRecorder) all() []core.Message {
	var out []core.Message
	for _, batch := range r.calls {
		out = append(out, batch...)
	}
	return out
}

// onEcho is a one-word tool for the scripted calls to hit.
func onEcho() *core.ToolDef {
	return core.Tool("echo", "Echo its argument.", func(_ context.Context, in struct {
		Text string `json:"text"`
	}) (string, error) {
		return "echo: " + in.Text, nil
	})
}

// The contract's core: everything the observers see, concatenated, is
// exactly the messages the run appended beyond its input — the same
// bytes RunResult.Messages holds, including a signed reasoning block's
// signature and the batched tool message, in transcript order. That is
// the whole point of OnMessages (TODO §5.12): what a consumer persists
// per step equals what the result holds, without rebuilding from
// deltas.
func TestOnMessagesEqualsResult(t *testing.T) {
	m := wefttest.Script(
		wefttest.Raw(
			core.ModelReasoningDelta{Text: "planning", Signature: "sig-1"},
			core.ModelToolCall{ID: "call_1", Name: "echo", Args: []byte(`{"text":"hi"}`)},
			core.ModelFinish{Reason: core.StopToolCalls, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
		),
		wefttest.Raw(
			core.ModelReasoningDelta{Text: "wrapping up", Signature: "sig-2"},
			core.ModelTextDelta{Text: "done"},
			core.ModelFinish{Reason: core.StopEndTurn, Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
		),
	)
	var rec onRecorder
	agt := core.New(m, onEcho())
	res, err := agt.Generate(context.Background(),
		core.Messages(core.User("start")), core.OnMessages(rec.fn))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(rec.calls), 3; got != want { // assistant, tool message, assistant
		t.Fatalf("%d observer calls, want %d", got, want)
	}
	if !reflect.DeepEqual(rec.all(), res.Messages[1:]) {
		t.Errorf("the observers saw a different transcript than the result:\n got %+v\nwant %+v", rec.all(), res.Messages[1:])
	}
	// The signatures especially: they are never streamed, and they are
	// here — the lossiness TODO §5.12 records is what this option ends.
	var sigs []string
	for _, batch := range rec.calls {
		for _, msg := range batch {
			for _, p := range msg.Content {
				if r, ok := p.(core.ReasoningPart); ok {
					sigs = append(sigs, r.Signature)
				}
			}
		}
	}
	if !reflect.DeepEqual(sigs, []string{"sig-1", "sig-2"}) {
		t.Errorf("reasoning signatures: got %v, want [sig-1 sig-2]", sigs)
	}
	if !reflect.DeepEqual(rec.steps, []int{0, 0, 1}) {
		t.Errorf("steps: got %v, want [0 0 1]", rec.steps)
	}
}

// A step whose assistant message never forms — a stream that dies
// before ModelFinish — fires nothing: no message joined the transcript,
// and the run error carries no partial messages either.
func TestOnMessagesNoFireOnStreamError(t *testing.T) {
	boom := errors.New("provider exploded")
	m := wefttest.Script(wefttest.SayThenFail("partial", boom))
	var rec onRecorder
	agt := core.New(m)
	_, err := agt.Generate(context.Background(), core.Messages(core.User("go")), core.OnMessages(rec.fn))
	if err == nil {
		t.Fatal("the scripted failure did not fail the run")
	}
	if len(rec.calls) != 0 {
		t.Errorf("%d observer calls on a failed stream, want 0", len(rec.calls))
	}
}

// A step that produces no assistant message at all (a finish with no
// text, no calls, no formed reasoning) is not appended — ADR 0001's
// rule — and therefore fires nothing, while the run still succeeds.
func TestOnMessagesNoFireOnEmptyAssistant(t *testing.T) {
	m := wefttest.Script(
		wefttest.Raw(core.ModelFinish{Reason: core.StopEndTurn}),
	)
	var rec onRecorder
	agt := core.New(m)
	res, err := agt.Generate(context.Background(), core.Messages(core.User("go")), core.OnMessages(rec.fn))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Messages) != 1 || len(rec.calls) != 0 {
		t.Errorf("%d messages, %d observer calls — an empty assistant step appends and fires nothing",
			len(res.Messages), len(rec.calls))
	}
}

// The observer's slice is a snapshot: mutating it — or holding it —
// changes nothing the run sees and sees nothing the run later does.
func TestOnMessagesSnapshot(t *testing.T) {
	m := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"text":"hi"}`}),
		wefttest.Say("done"),
	)
	var held []core.Message
	agt := core.New(m, onEcho())
	res, err := agt.Generate(context.Background(), core.Messages(core.User("go")),
		core.OnMessages(func(_ context.Context, _ int, msgs []core.Message) {
			msgs[0].Role = core.RoleUser // the observer's copy is the observer's
			if tp, ok := msgs[0].Content[0].(core.ToolCallPart); ok {
				tp.Name = "mutated"
				msgs[0].Content[0] = tp
			}
			held = append(held, msgs...)
		}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Messages[1].Role != core.RoleAssistant {
		t.Errorf("the observer's write reached the transcript: %+v", res.Messages[1])
	}
	if c, _ := res.Messages[1].Content[len(res.Messages[1].Content)-1].(core.ToolCallPart); c.Name != "echo" {
		t.Errorf("the observer's part mutation reached the transcript: %+v", res.Messages[1].Content)
	}
	if held[0].Role != core.RoleUser {
		t.Errorf("the held snapshot changed after the observer returned: %+v", held[0])
	}
}

// Steered messages are transcript (ADR 0019 §3) and fire like any other
// join, naming the step whose drain delivered them.
func TestOnMessagesSteered(t *testing.T) {
	m := wefttest.Script(
		wefttest.Say("first"),
		wefttest.Say("second"),
	)
	var rec onRecorder
	// Deliver once, at the final step — the redirect that makes one more
	// step.
	sent := false
	source := func(_ context.Context, p core.SteerPoint) []core.Message {
		if p.Final && !sent {
			sent = true
			return []core.Message{core.User("and one more thing")}
		}
		return nil
	}
	agt := core.New(m)
	if _, err := agt.Generate(context.Background(),
		core.Messages(core.User("go")), core.Steering(source), core.OnMessages(rec.fn)); err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, batch := range rec.calls {
		for _, msg := range batch {
			texts = append(texts, msg.Text())
		}
	}
	want := []string{"first", "and one more thing", "second"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Errorf("observed texts: got %v, want %v (the steer fires between the steps)", texts, want)
	}
}

// A panicking observer is contained and counted like a tap; the run
// completes and the observers after it still run.
func TestOnMessagesPanicContained(t *testing.T) {
	m := wefttest.Script(wefttest.Say("hello"))
	var rec onRecorder
	agt := core.New(m)
	res, err := agt.Generate(context.Background(), core.Messages(core.User("go")),
		core.OnMessages(func(context.Context, int, []core.Message) { panic("broken observer") }),
		core.OnMessages(rec.fn))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "hello" {
		t.Errorf("the run's answer changed under a broken observer: %q", res.Text())
	}
	if len(rec.calls) != 1 {
		t.Errorf("the observer after the panicking one did not run: %d calls", len(rec.calls))
	}
	if n := agt.TapPanics(); n != 1 {
		t.Errorf("TapPanics = %d, want 1", n)
	}
}

// A Subagent's child run does not inherit the parent's observers: the
// child's transcript belongs to the child's own caller (the same rule
// as Steering).
func TestOnMessagesChildrenDoNotInherit(t *testing.T) {
	child := core.New(wefttest.Script(wefttest.Say("the child's answer")))
	m := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "ask_child", Args: `{}`}),
		wefttest.Say("parent done"),
	)
	var rec onRecorder
	agt := core.New(m, core.Subagent("ask_child", "Ask the child.", child))
	res, err := agt.Generate(context.Background(), core.Messages(core.User("go")), core.OnMessages(rec.fn))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text(), "parent done") {
		t.Fatalf("the run did not complete: %q", res.Text())
	}
	for _, batch := range rec.calls {
		for _, msg := range batch {
			if msg.Text() == "the child's answer" {
				t.Error("a child transcript message reached the parent's observer")
			}
		}
	}
}

// Generate mirrors its scripted dialogue: the run's answer, then each
// batch the observer saw.
func ExampleOnMessages() {
	m := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"text":"hi"}`}),
		wefttest.Say("done"),
	)
	echo := core.Tool("echo", "Echo its argument.", func(_ context.Context, in struct {
		Text string `json:"text"`
	}) (string, error) {
		return "echo: " + in.Text, nil
	})
	agt := core.New(m, echo)
	res, _ := agt.Generate(context.Background(), core.Messages(core.User("go")),
		core.OnMessages(func(_ context.Context, step int, msgs []core.Message) {
			for _, msg := range msgs {
				switch msg.Role {
				case core.RoleAssistant:
					for _, p := range msg.Content {
						if c, ok := p.(core.ToolCallPart); ok {
							fmt.Printf("step %d: assistant calls %s\n", step, c.Name)
						}
					}
					if text := msg.Text(); text != "" {
						fmt.Printf("step %d: assistant %q\n", step, text)
					}
				case core.RoleTool:
					for _, p := range msg.Content {
						if r, ok := p.(core.ToolResultPart); ok {
							fmt.Printf("step %d: tool %q\n", step, r.Content)
						}
					}
				}
			}
		}))
	fmt.Println("answer:", res.Text())
	// Output:
	// step 0: assistant calls echo
	// step 0: tool "echo: hi"
	// step 1: assistant "done"
	// answer: done
}
