package wefttest_test

import (
	"context"
	"errors"
	"slices"
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

// Flatten unwraps Nested events recursively, preserving order.
func TestFlatten(t *testing.T) {
	evs := []weft.Event{
		weft.RunStart{ID: "r1"},
		weft.Nested{RunID: "r1", Seq: 2, CallID: "c1", Event: weft.Nested{
			RunID: "r1/0/c1", Seq: 1, CallID: "call_1",
			Event: weft.TextDelta{RunID: "r1/0/c1/0/call_1", Text: "deep"},
		}},
		weft.StepFinish{RunID: "r1"},
	}
	got := wefttest.Flatten(evs)
	want := []weft.Event{
		weft.RunStart{ID: "r1"},
		weft.TextDelta{RunID: "r1/0/c1/0/call_1", Text: "deep"},
		weft.StepFinish{RunID: "r1"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Flatten = %v, want %v", got, want)
	}
}

// --- TODO §9.3: the scripting helpers ---

type argsIn struct {
	OrderID int    `json:"order_id"`
	Reason  string `json:"reason,omitempty"`
}

func TestArgsMarshals(t *testing.T) {
	if got := wefttest.Args(argsIn{OrderID: 42, Reason: "broken"}); got != `{"order_id":42,"reason":"broken"}` {
		t.Errorf("Args = %s, want the marshalled input struct", got)
	}
	// An empty struct still marshals to the object a Call expects, not "".
	if got := wefttest.Args(struct{}{}); got != `{}` {
		t.Errorf("Args(struct{}{}) = %s, want {}", got)
	}
}

func TestArgsPanicsOnUnmarshalable(t *testing.T) {
	defer func() {
		p, ok := recover().(string)
		if !ok || !strings.Contains(p, "chan") {
			t.Errorf("panic = %v, want the value's type named", p)
		}
	}()
	wefttest.Args(make(chan int))
	t.Error("Args did not panic on an unmarshalable value")
}

// Raw plays exactly the events given — verbatim, nothing appended.
func TestRawPlaysVerbatim(t *testing.T) {
	given := []weft.ModelEvent{
		weft.ModelReasoningDelta{Text: "considering", Signature: "sig1"},
		weft.ModelTextDelta{Text: "hi"},
	}
	model := wefttest.Script(wefttest.Raw(given...))
	var got []weft.ModelEvent
	for ev, err := range model.Stream(context.Background(), weft.ModelRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev)
	}
	if !slices.Equal(got, given) {
		t.Errorf("Raw played %v, want %v verbatim", got, given)
	}
	// The caller's slice is not retained: mutating it after construction
	// cannot change the script.
	model = wefttest.Script(wefttest.Raw(given...))
	given[0] = weft.ModelTextDelta{Text: "mutated"}
	first := func(m *wefttest.Model) weft.ModelEvent {
		for ev, err := range m.Stream(context.Background(), weft.ModelRequest{}) {
			if err != nil {
				t.Fatal(err)
			}
			return ev
		}
		return nil
	}
	if _, ok := first(model).(weft.ModelReasoningDelta); !ok {
		t.Error("Raw retained the caller's slice")
	}
}

// Raw is the way to script a contract violation: a stream with no
// ModelFinish makes the loop fail the run wrapping ErrModelContract.
func TestRawScriptsContractViolation(t *testing.T) {
	agt := weft.New(wefttest.Script(wefttest.Raw(
		weft.ModelTextDelta{Text: "hi"},
	)))
	_, err := agt.Generate(context.Background(), weft.Prompt("q"))
	if !errors.Is(err, weft.ErrModelContract) {
		t.Errorf("err = %v, want ErrModelContract", err)
	}
}

func TestSayThenFailShape(t *testing.T) {
	boom := errors.New("provider cut the stream")
	model := wefttest.Script(wefttest.SayThenFail("partial", boom))
	var (
		texts []string
		got   error
	)
	for ev, err := range model.Stream(context.Background(), weft.ModelRequest{}) {
		if err != nil {
			got = err
			continue
		}
		if d, ok := ev.(weft.ModelTextDelta); ok {
			texts = append(texts, d.Text)
		}
	}
	if len(texts) != 1 || texts[0] != "partial" {
		t.Errorf("deltas = %v, want the one scripted text", texts)
	}
	if !errors.Is(got, boom) {
		t.Errorf("stream error = %v, want the scripted error after the events", got)
	}
}

func TestWithUsage(t *testing.T) {
	u := weft.Usage{InputTokens: 1000, OutputTokens: 40}
	turn := wefttest.Say("x").WithUsage(u)
	var finish weft.ModelFinish
	for ev, err := range wefttest.Script(turn).Stream(context.Background(), weft.ModelRequest{}) {
		if err != nil {
			t.Fatal(err)
		}
		if f, ok := ev.(weft.ModelFinish); ok {
			finish = f
		}
	}
	if finish.Usage != u {
		t.Errorf("usage = %+v, want %+v", finish.Usage, u)
	}
	// A turn without a finish is a no-op: Fail keeps its error, and no
	// finish appears because there was none to rewrite.
	boom := errors.New("boom")
	var gotErr error
	for _, err := range wefttest.Script(wefttest.Fail(boom).WithUsage(u)).Stream(context.Background(), weft.ModelRequest{}) {
		gotErr = err
	}
	if !errors.Is(gotErr, boom) {
		t.Errorf("Fail(…).WithUsage error = %v, want the scripted error kept", gotErr)
	}
}

func TestLastRequestMatchers(t *testing.T) {
	echo := weft.Tool("echo", "Echo.", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	probe := weft.Tool("probe", "Probe.", func(_ context.Context, _ struct{}) (string, error) {
		return "ok", nil
	})
	m := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}),
		wefttest.Say("done"),
	)
	if _, err := weft.New(m, echo, probe).Generate(context.Background(), weft.Prompt("run echo")); err != nil {
		t.Fatal(err)
	}
	if len(m.Requests()) != 2 {
		t.Fatalf("%d requests, want 2", len(m.Requests()))
	}
	first := wefttest.Request{ModelRequest: m.Requests()[0]}
	if !first.HasTool("echo") || !first.HasTool("probe") || first.HasTool("absent") {
		t.Errorf("HasTool disagrees with the advertised catalogue")
	}
	if got := first.ToolNames(); !slices.Equal(got, []string{"echo", "probe"}) {
		t.Errorf("ToolNames = %v, want sorted [echo probe]", got)
	}
	if got := first.LastText(); got != "run echo" {
		t.Errorf("first LastText = %q, want the prompt", got)
	}
	// After the tool step the last message is the tool message: no text
	// part, so LastText is "".
	last := m.LastRequest()
	if got := last.LastText(); got != "" {
		t.Errorf("last LastText = %q, want \"\" (a tool message has no text part)", got)
	}
	if !last.HasTool("echo") {
		t.Error("LastRequest lost the tool catalogue")
	}
}

func TestLastRequestZero(t *testing.T) {
	m := wefttest.Script(wefttest.Say("x"))
	r := m.LastRequest()
	if r.HasTool("echo") || len(r.ToolNames()) != 0 || r.LastText() != "" {
		t.Errorf("zero Request matchers = true/non-empty; want false and empty")
	}
}
