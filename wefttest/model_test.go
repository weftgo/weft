package wefttest_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// The mock's own contract: turns play in order, events arrive whole,
// requests are recorded as independent copies, and running off the end
// fails loudly with ErrScriptExhausted.
func TestScriptPlaysTurnsAndRecordsRequests(t *testing.T) {
	model := wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "echo", Args: `{"msg":"hi"}`},
			wefttest.Call{ID: "x2", Name: "echo"},
		),
		wefttest.Say("done"),
	)
	req := weft.ModelRequest{System: "sys", SequentialTools: false}

	// count also collects the turn's tool-call ids, pinning the
	// documented default-ID rule: "call_1", "call_2", ... within the
	// turn unless set explicitly.
	var ids []string
	count := func() int {
		t.Helper()
		n := 0
		for ev, err := range model.Stream(context.Background(), req) {
			if err != nil {
				t.Fatalf("stream error: %v", err)
			}
			if c, ok := ev.(weft.ModelToolCall); ok {
				ids = append(ids, c.ID)
			}
			n++
		}
		return n
	}
	if n := count(); n != 3 { // two tool calls + finish
		t.Errorf("first stream yielded %d events, want 3", n)
	}
	if got := strings.Join(ids, ","); got != "call_1,x2" {
		t.Errorf("call ids = %q, want the default call_1 and the explicit x2", got)
	}
	if n := count(); n != 2 { // text delta + finish
		t.Errorf("second stream yielded %d events, want 2", n)
	}
	var exhausted error
	for _, err := range model.Stream(context.Background(), req) {
		exhausted = err
	}
	if !errors.Is(exhausted, wefttest.ErrScriptExhausted) {
		t.Errorf("exhausted error = %v, want ErrScriptExhausted", exhausted)
	}

	reqs := model.Requests()
	if len(reqs) != 3 { // the exhausted call is a real request and records too
		t.Fatalf("Requests() recorded %d calls, want 3", len(reqs))
	}
	if reqs[0].System != "sys" || reqs[0].SequentialTools {
		t.Errorf("first recorded request = %+v, want the request verbatim", reqs[0])
	}
	reqs[0].System = "mutated"
	if model.Requests()[0].System != "sys" {
		t.Error("Requests() exposed mutable state, not copies")
	}
}

func TestFailTurnYieldsError(t *testing.T) {
	boom := errors.New("provider down")
	model := wefttest.Script(wefttest.Fail(boom))
	var got error
	for _, err := range model.Stream(context.Background(), weft.ModelRequest{}) {
		got = err
	}
	if !errors.Is(got, boom) {
		t.Errorf("Fail error = %v, want %v", got, boom)
	}
}

func TestThinkPrefixesTurn(t *testing.T) {
	model := wefttest.Script(wefttest.Think("plan", wefttest.Say("done")))
	var first weft.ModelEvent
	n := 0
	for ev, err := range model.Stream(context.Background(), weft.ModelRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			first = ev
		}
		n++
	}
	if d, ok := first.(weft.ModelReasoningDelta); !ok || d.Text != "plan" {
		t.Errorf("first event = %+v, want ModelReasoningDelta{Text: plan}", first)
	}
	if n != 3 { // reasoning delta, text delta, finish
		t.Errorf("yielded %d events, want 3", n)
	}
}

func TestMaxTokensTurnShape(t *testing.T) {
	model := wefttest.Script(wefttest.MaxTokens("partial"))
	var finish weft.ModelFinish
	for ev, err := range model.Stream(context.Background(), weft.ModelRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
		if f, ok := ev.(weft.ModelFinish); ok {
			finish = f
		}
	}
	if finish.Reason != weft.StopMaxTokens || finish.Usage.Total() != 15 {
		t.Errorf("MaxTokens finish = %+v, want reason max_tokens and fixed usage", finish)
	}
}

// The Model contract, enforced before anything scripted: a ctx that is
// already done yields ctx.Err() — not the scripted error, not
// ErrScriptExhausted — the same rule the adapters enforce (the
// reference double must not teach middleware the wrong shape).
func TestScriptYieldsContextErrorWhenDone(t *testing.T) {
	model := wefttest.Script(wefttest.Fail(errors.New("scripted")))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var got error
	for _, err := range model.Stream(ctx, weft.ModelRequest{}) {
		got = err
	}
	if !errors.Is(got, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", got)
	}
}
