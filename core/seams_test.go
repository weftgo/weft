package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// --- §4.1 the model seam ---

// tagModel records the order middleware saw a request in.
func tagMW(name string, trace *[]string) core.ModelMiddleware {
	return func(next core.Model) core.Model {
		return &tagModel{name: name, next: next, trace: trace}
	}
}

type tagModel struct {
	name  string
	next  core.Model
	trace *[]string
}

func (m *tagModel) Info() core.ModelInfo { return core.InfoOf(m.next) }

func (m *tagModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	*m.trace = append(*m.trace, m.name+":"+req.System)
	return m.next.Stream(ctx, req)
}

func TestWrapModelOrderIsOutermostFirst(t *testing.T) {
	var trace []string
	agt := core.New(wefttest.Script(wefttest.Say("hi")),
		core.Instructions("sys"),
		core.WrapModel(tagMW("a", &trace), tagMW("b", &trace)),
		core.WrapModel(nil, tagMW("c", &trace)),
	)
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Text() != "hi" {
		t.Errorf("text = %q", res.Text())
	}
	if want := []string{"a:sys", "b:sys", "c:sys"}; strings.Join(trace, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v (a(b(c(model))))", trace, want)
	}
	// Identity is forwarded through the chain.
	var seen core.RunStart
	agt2 := core.New(wefttest.Script(wefttest.Say("hi")),
		core.WrapModel(tagMW("a", &trace)),
		core.Tap(func(_ context.Context, ev core.Event) {
			if rs, ok := ev.(core.RunStart); ok {
				seen = rs
			}
		}))
	if _, err := agt2.Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if seen.Model != (core.ModelInfo{Provider: "wefttest", Name: "script"}) {
		t.Errorf("RunStart.Model through middleware = %+v", seen.Model)
	}
}

func TestWrapModelNilResultPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New did not panic on middleware returning a nil Model")
		}
	}()
	core.New(wefttest.Script(), core.WrapModel(func(core.Model) core.Model { return nil }))
}

// --- §5.2a ToolError ---

func TestToolErrorRendering(t *testing.T) {
	cause := errors.New("pg: relation orders does not exist")
	lookup := core.Tool("lookup", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", &core.ToolError{Code: "ORDER_NOT_FOUND", Message: "order 1234 does not exist", Err: cause}
	})
	fmtd := core.Tool("fmtd", "", func(_ context.Context, _ struct{}) (string, error) {
		return "", core.Errorf("UPSTREAM", "billing said no: %w", cause)
	})
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "lookup"},
			wefttest.Call{Name: "fmtd"},
			wefttest.Call{Name: "lookup", Args: `{"x":`},
			wefttest.Call{Name: "ghost"},
		),
		wefttest.Say("ok"),
	), lookup, fmtd)
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	got := res.Steps[0].Results
	want := []string{
		"ORDER_NOT_FOUND: order 1234 does not exist",
		"UPSTREAM: billing said no: pg: relation orders does not exist",
		`INVALID_INPUT: tool "lookup": invalid JSON: unexpected end of input`,
		`NO_SUCH_TOOL: no tool named "ghost"`,
	}
	for i, w := range want {
		if !got[i].IsError || got[i].Content != w {
			t.Errorf("result %d = %q (err=%v), want %q", i, got[i].Content, got[i].IsError, w)
		}
	}
	// The cause never reaches the model but is available through the
	// seam and to CallTool callers.
	if strings.Contains(got[0].Content, "relation") {
		t.Error("ToolError.Err leaked into the model-visible content")
	}
	_, cerr := agt.CallTool(context.Background(), core.ToolCallPart{ID: "c", Name: "lookup", Args: json.RawMessage(`{}`)})
	var te *core.ToolError
	if !errors.As(cerr, &te) || te.Code != "ORDER_NOT_FOUND" || !errors.Is(cerr, cause) {
		t.Errorf("CallTool error = %v, want the *ToolError with its cause", cerr)
	}
	_, cerr = agt.CallTool(context.Background(), core.ToolCallPart{Name: "lookup", Args: json.RawMessage(`[]`)})
	if !errors.Is(cerr, core.ErrInvalidToolInput) || !errors.As(cerr, &te) || te.Code != "INVALID_INPUT" {
		t.Errorf("decode failure = %v, want INVALID_INPUT wrapping ErrInvalidToolInput", cerr)
	}
	_, cerr = agt.CallTool(context.Background(), core.ToolCallPart{Name: "ghost"})
	if !errors.Is(cerr, core.ErrNoSuchTool) || !errors.As(cerr, &te) || te.Code != "NO_SUCH_TOOL" {
		t.Errorf("unknown tool = %v, want NO_SUCH_TOOL wrapping ErrNoSuchTool", cerr)
	}
	if e := (&core.ToolError{Code: "X"}).Error(); e != "X" {
		t.Errorf("message-less ToolError renders %q, want the bare code", e)
	}
	// Several %w verbs keep every cause reachable.
	other := errors.New("upstream two")
	both := core.Errorf("X", "both %w and %w", cause, other)
	if both.Error() != "X: both "+cause.Error()+" and "+other.Error() ||
		!errors.Is(both, cause) || !errors.Is(both, other) {
		t.Errorf("Errorf with two %%w = %q (is causes: %v/%v), want both reachable",
			both.Error(), errors.Is(both, cause), errors.Is(both, other))
	}
}

// --- §4.2 the tool seam ---

func traceTool(name string, trace *[]string) core.ToolMiddleware {
	return func(next core.ToolCaller) core.ToolCaller {
		return func(ctx context.Context, call core.ToolCallPart) (string, error) {
			*trace = append(*trace, name+">"+call.Name)
			out, err := next(ctx, call)
			*trace = append(*trace, name+"<"+call.Name)
			return out, err
		}
	}
}

func TestWrapToolsOrderAndLevels(t *testing.T) {
	var mu sync.Mutex
	var trace []string
	locked := func(name string) core.ToolMiddleware {
		inner := traceTool(name, &trace)
		return func(next core.ToolCaller) core.ToolCaller {
			wrapped := inner(next)
			return func(ctx context.Context, call core.ToolCallPart) (string, error) {
				mu.Lock()
				defer mu.Unlock()
				return wrapped(ctx, call)
			}
		}
	}
	echo := core.Tool("echo", "", func(_ context.Context, _ struct{}) (string, error) { return "e", nil },
		core.WrapTools(traceTool("tool", &trace)))
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo"}, wefttest.Call{Name: "ghost"}),
		wefttest.Say("ok"),
	), core.Sequential(), echo,
		core.WrapTools(locked("a"), traceTool("b", &trace)),
		core.WrapTools(traceTool("c", &trace)),
	)
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	want := "a>echo,b>echo,c>echo,tool>echo,tool<echo,c<echo,b<echo,a<echo," +
		"a>ghost,b>ghost,c>ghost,c<ghost,b<ghost,a<ghost"
	if got := strings.Join(trace, ","); got != want {
		t.Errorf("order:\n got %s\nwant %s", got, want)
	}
	if r := res.Steps[0].Results[1]; !r.IsError || !strings.HasPrefix(r.Content, "NO_SUCH_TOOL") {
		t.Errorf("unknown tool through the chain = %+v", r)
	}
	// CallTool runs the same chain.
	trace = nil
	if out, err := agt.CallTool(context.Background(), core.ToolCallPart{ID: "m", Name: "echo"}); err != nil || out != "e" {
		t.Fatalf("CallTool = %q, %v", out, err)
	}
	if got := strings.Join(trace, ","); got != "a>echo,b>echo,c>echo,tool>echo,tool<echo,c<echo,b<echo,a<echo" {
		t.Errorf("CallTool chain order = %s", got)
	}
}

func TestWrapToolsDenialAndPanicAreToolErrors(t *testing.T) {
	deny := func(next core.ToolCaller) core.ToolCaller {
		return func(ctx context.Context, call core.ToolCallPart) (string, error) {
			c, ok := core.CallFromContext(ctx)
			if !ok || c.Name != call.Name || c.CallID != call.ID {
				t.Errorf("CallFromContext in middleware = %+v (%v), want call %s", c, ok, call.ID)
			}
			if call.Name == "rm" {
				return "", &core.ToolError{Code: "DENIED", Message: "rm is not allowed"}
			}
			return next(ctx, call)
		}
	}
	boom := func(core.ToolCaller) core.ToolCaller {
		return func(context.Context, core.ToolCallPart) (string, error) { panic("middleware bug") }
	}
	ran := false
	rm := core.Tool("rm", "", func(_ context.Context, _ struct{}) (string, error) { ran = true; return "gone", nil })
	ls := core.Tool("ls", "", func(_ context.Context, _ struct{}) (string, error) { return "files", nil })
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "rm"}, wefttest.Call{Name: "ls"}),
		wefttest.Say("ok"),
	), rm, ls, core.WrapTools(deny, boom))
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatalf("a middleware panic must be a tool error, not a run error: %v", err)
	}
	r := res.Steps[0].Results
	if ran || !r[0].IsError || r[0].Content != "DENIED: rm is not allowed" {
		t.Errorf("denied call: ran=%v result=%+v", ran, r[0])
	}
	if !r[1].IsError || !strings.Contains(r[1].Content, `tool "ls" panicked: middleware bug`) {
		t.Errorf("panicking middleware result = %+v", r[1])
	}
}

// --- §4.3 barrier, snippet, replay ---

func TestSequentialToolIsABarrier(t *testing.T) {
	var inflight, maxInflight atomic.Int32
	var mu sync.Mutex
	var order []string
	slow := func(name string, d time.Duration) *core.ToolDef {
		return core.Tool(name, "", func(_ context.Context, _ struct{}) (string, error) {
			n := inflight.Add(1)
			for {
				m := maxInflight.Load()
				if n <= m || maxInflight.CompareAndSwap(m, n) {
					break
				}
			}
			mu.Lock()
			order = append(order, name+">")
			mu.Unlock()
			time.Sleep(d)
			mu.Lock()
			order = append(order, name+"<")
			mu.Unlock()
			inflight.Add(-1)
			return "ok", nil
		})
	}
	alone := core.Tool("alone", "", func(_ context.Context, _ struct{}) (string, error) {
		if n := inflight.Load(); n != 0 {
			t.Errorf("barrier tool ran with %d other calls in flight", n)
		}
		mu.Lock()
		order = append(order, "alone")
		mu.Unlock()
		return "ok", nil
	}, core.Sequential())
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{Name: "p1"}, wefttest.Call{Name: "p2"},
			wefttest.Call{Name: "alone"},
			wefttest.Call{Name: "p3"}, wefttest.Call{Name: "p4"},
		),
		wefttest.Say("ok"),
	), core.Parallelism(4), slow("p1", 30*time.Millisecond), slow("p2", 30*time.Millisecond), alone,
		slow("p3", 30*time.Millisecond), slow("p4", 30*time.Millisecond))
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	if maxInflight.Load() < 2 {
		t.Errorf("max in flight = %d; parallel tools around the barrier should still overlap", maxInflight.Load())
	}
	joined := strings.Join(order, " ")
	i := strings.Index(joined, "alone")
	before, after := joined[:i], joined[i:]
	if strings.Contains(before, "p3") || strings.Contains(before, "p4") {
		t.Errorf("calls after the barrier started before it: %s", joined)
	}
	if !strings.Contains(before, "p1<") || !strings.Contains(before, "p2<") {
		t.Errorf("barrier ran before in-flight calls finished: %s", joined)
	}
	if !strings.Contains(after, "p3>") || !strings.Contains(after, "p4>") {
		t.Errorf("calls after the barrier did not run: %s", joined)
	}
	// Results stay in call order.
	for i, want := range []string{"p1", "p2", "alone", "p3", "p4"} {
		if res.Steps[0].Results[i].Name != want {
			t.Errorf("result %d = %s, want %s", i, res.Steps[0].Results[i].Name, want)
		}
	}
}

func TestPromptSnippetsComposeIntoInstructions(t *testing.T) {
	a := core.Tool("a", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil },
		core.PromptSnippet("Use a only for apples."))
	b := core.Tool("b", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil })
	c := core.Tool("c", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil },
		core.PromptSnippet("c needs a citation."))
	model := wefttest.Script(wefttest.Say("ok"))
	if _, err := core.New(model, core.Instructions("You help."), a, b, c).Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if got, want := model.Requests()[0].System, "You help.\n\nUse a only for apples.\n\nc needs a citation."; got != want {
		t.Errorf("System = %q, want %q", got, want)
	}
	model = wefttest.Script(wefttest.Say("ok"))
	if _, err := core.New(model, a).Generate(context.Background(), core.Prompt("x")); err != nil {
		t.Fatal(err)
	}
	if got := model.Requests()[0].System; got != "Use a only for apples." {
		t.Errorf("System without Instructions = %q", got)
	}
	if a.PromptSnippet() != "Use a only for apples." || b.PromptSnippet() != "" {
		t.Error("PromptSnippet accessor")
	}
}

func TestToolPolicyInManifest(t *testing.T) {
	a := core.Tool("a", "", func(_ context.Context, _ struct{}) (string, error) { return "", nil },
		core.Sequential(), core.RequireApproval(), core.PromptSnippet("hint"))
	b, err := core.Manifest(core.New(wefttest.Script(), core.Name("m"), a))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"sequential": true`, `"require_approval": true`, `"prompt_snippet": "hint"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("manifest lacks %s:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "replay") {
		t.Errorf("manifest still carries a replay key:\n%s", b)
	}
}

// --- §4.4 the approval boundary ---

func TestApprovalPendingStopsTheRunAndResumes(t *testing.T) {
	var refunds, lookups int
	refund := core.Tool("refund", "", func(ctx context.Context, in struct {
		Order string `json:"order"`
	}) (string, error) {
		c, _ := core.CallFromContext(ctx)
		if !c.Approved {
			t.Error("approved call ran without Call.Approved")
		}
		refunds++
		return "refunded " + in.Order, nil
	}, core.RequireApproval())
	lookup := core.Tool("lookup", "", func(_ context.Context, _ struct{}) (string, error) { lookups++; return "found", nil })
	model := wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{ID: "r1", Name: "refund", Args: `{"order":"1"}`},
			wefttest.Call{ID: "l1", Name: "lookup"},
			wefttest.Call{ID: "r2", Name: "refund", Args: `{"order":"2"}`},
			wefttest.Call{ID: "r3", Name: "refund", Args: `{"order":"3"}`},
		),
		wefttest.Say("done"),
	)
	var events []core.Event
	agt := core.New(model, refund, lookup, core.StopWhen(core.HasToolCall("never")),
		core.Tap(func(_ context.Context, ev core.Event) { events = append(events, ev) }))

	// 1. The step's other tools run; the run ends successfully, pending.
	res, err := agt.Generate(context.Background(), core.Prompt("refund 1, 2 and 3"))
	if err != nil {
		t.Fatal(err)
	}
	if refunds != 0 || lookups != 1 {
		t.Errorf("refunds=%d lookups=%d; only the unrestricted tool may run", refunds, lookups)
	}
	if ids := callIDs(res.Pending); ids != "r1,r2,r3" {
		t.Errorf("Pending = %s, want r1,r2,r3", ids)
	}
	if res.NumSteps() != 1 || len(res.Steps[0].Results) != 1 || res.Steps[0].Results[0].CallID != "l1" {
		t.Errorf("step results = %+v, want only the lookup", res.Steps[0].Results)
	}
	last := res.Messages[len(res.Messages)-1]
	if last.Role != core.RoleTool || len(last.Content) != 1 {
		t.Errorf("transcript must end with the executed result only (dangling calls): %+v", last)
	}
	var starts, finishes int
	var fin core.RunFinish
	for _, ev := range events {
		switch e := ev.(type) {
		case core.ToolStart:
			starts++
		case core.ToolFinish:
			finishes++
		case core.RunFinish:
			fin = e
		}
	}
	if starts != 4 || finishes != 1 {
		t.Errorf("starts=%d finishes=%d; pending calls start and never finish", starts, finishes)
	}
	if callIDs(fin.Pending) != "r1,r2,r3" {
		t.Errorf("RunFinish.Pending = %s", callIDs(fin.Pending))
	}

	// 2. Resume: approve one, deny one, forget one. The approved call
	// runs; the others are denied visibly; the loop continues.
	events = nil
	res2, err := agt.Generate(context.Background(),
		core.Messages(res.Messages...), core.Approve("r1"), core.Deny("r2", "customer withdrew"))
	if err != nil {
		t.Fatal(err)
	}
	if refunds != 1 || lookups != 1 {
		t.Errorf("after resume refunds=%d lookups=%d", refunds, lookups)
	}
	if res2.Text() != "done" || len(res2.Pending) != 0 {
		t.Errorf("resumed run = %q pending %v", res2.Text(), res2.Pending)
	}
	// The results landed on the earlier step's tool message, in call
	// order after the result it already had, and the model saw them.
	req := model.Requests()[1]
	var toolMsg core.Message
	for _, m := range req.Messages {
		if m.Role == core.RoleTool {
			toolMsg = m
		}
	}
	got := map[string]core.ToolResultPart{}
	var order []string
	for _, p := range toolMsg.Content {
		r := p.(core.ToolResultPart)
		got[r.CallID] = r
		order = append(order, r.CallID)
	}
	if len(got) != 4 {
		t.Fatalf("model saw %d results, want 4: %+v", len(got), toolMsg.Content)
	}
	// ADR 0007 §3: the results complete the earlier step's tool message
	// in call order — [r1, l1, r2, r3], the assistant's order — not
	// appended after the earlier ones. Gemini matches responses by name
	// and position, so order is correctness, not style.
	if strings.Join(order, ",") != "r1,l1,r2,r3" {
		t.Errorf("result order = %v, want the assistant's call order [r1 l1 r2 r3]", order)
	}
	if r := got["r1"]; r.IsError || r.Content != "refunded 1" {
		t.Errorf("approved = %+v", r)
	}
	if r := got["r2"]; !r.IsError || r.Content != "DENIED: customer withdrew" {
		t.Errorf("denied = %+v", r)
	}
	if r := got["r3"]; !r.IsError || r.Content != "DENIED: no decision" {
		t.Errorf("undecided = %+v", r)
	}
	if got["l1"].Content != "found" {
		t.Errorf("earlier result lost: %+v", got["l1"])
	}
	if len(req.Messages) != 3 || req.Messages[2].Role != core.RoleTool {
		t.Errorf("resumed transcript shape = %d messages; want user, assistant, tool", len(req.Messages))
	}
	// Resumed calls stream their events into the new run.
	var starts2, finishes2 int
	for _, ev := range events {
		switch ev.(type) {
		case core.ToolStart:
			starts2++
		case core.ToolFinish:
			finishes2++
		}
	}
	if starts2 != 1 || finishes2 != 1 {
		t.Errorf("resume events: starts=%d finishes=%d, want 1/1 for the approved call", starts2, finishes2)
	}
}

func callIDs(calls []core.ToolCallPart) string {
	ids := make([]string, 0, len(calls))
	for _, c := range calls {
		ids = append(ids, c.ID)
	}
	return strings.Join(ids, ",")
}

func TestApprovalFromMiddlewareAndPromptAfterPending(t *testing.T) {
	gate := func(next core.ToolCaller) core.ToolCaller {
		return func(ctx context.Context, call core.ToolCallPart) (string, error) {
			c, _ := core.CallFromContext(ctx)
			if strings.Contains(string(call.Args), "prod") && !c.Approved {
				return "", fmt.Errorf("%w: deploy to prod", core.ErrApprovalRequired)
			}
			return next(ctx, call)
		}
	}
	deploys := 0
	deploy := core.Tool("deploy", "", func(_ context.Context, in struct {
		Env string `json:"env"`
	}) (string, error) {
		deploys++
		return "deployed " + in.Env, nil
	})
	model := wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "d1", Name: "deploy", Args: `{"env":"prod"}`}),
		wefttest.Say("ok"),
	)
	agt := core.New(model, deploy, core.WrapTools(gate))
	res, err := agt.Generate(context.Background(), core.Prompt("ship it"))
	if err != nil {
		t.Fatal(err)
	}
	if deploys != 0 || callIDs(res.Pending) != "d1" {
		t.Errorf("deploys=%d pending=%s", deploys, callIDs(res.Pending))
	}
	// No tool message at all when every call of the step is pending.
	if last := res.Messages[len(res.Messages)-1]; last.Role != core.RoleAssistant {
		t.Errorf("transcript ends in %s, want the dangling assistant message", last.Role)
	}
	// Resume with a follow-up prompt after the transcript: the results
	// are inserted after the assistant message, not at the end.
	res2, err := agt.Generate(context.Background(), core.Messages(res.Messages...), core.Approve("d1"), core.Prompt("and?"))
	if err != nil {
		t.Fatal(err)
	}
	if deploys != 1 || res2.Text() != "ok" {
		t.Errorf("deploys=%d text=%q", deploys, res2.Text())
	}
	msgs := model.Requests()[1].Messages
	roles := make([]string, 0, len(msgs))
	for _, m := range msgs {
		roles = append(roles, string(m.Role))
	}
	if got := strings.Join(roles, ","); got != "user,assistant,tool,user" {
		t.Errorf("roles = %s, want user,assistant,tool,user", got)
	}
	if r := msgs[2].Content[0].(core.ToolResultPart); r.Content != "deployed prod" {
		t.Errorf("approved result = %+v", r)
	}
}

func TestApprovalWithoutDecisionsIsRepairedAsInterrupted(t *testing.T) {
	refund := core.Tool("refund", "", func(_ context.Context, _ struct{}) (string, error) { return "no", nil }, core.RequireApproval())
	model := wefttest.Script(wefttest.ToolCalls(wefttest.Call{ID: "r1", Name: "refund"}), wefttest.Say("ok"))
	agt := core.New(model, refund)
	res, err := agt.Generate(context.Background(), core.Prompt("x"))
	if err != nil {
		t.Fatal(err)
	}
	// Feeding the transcript back with no decision: ordinary repair.
	if _, err := agt.Generate(context.Background(), core.Messages(res.Messages...)); err != nil {
		t.Fatal(err)
	}
	msgs := model.Requests()[1].Messages
	if r := msgs[2].Content[0].(core.ToolResultPart); !r.IsError || r.Content != "no result recorded: the call was interrupted" {
		t.Errorf("undecided without decisions = %+v, want the repair synthesis", r)
	}
	// CallTool surfaces the boundary as an error, not a result.
	if _, err := agt.CallTool(context.Background(), core.ToolCallPart{Name: "refund"}); !errors.Is(err, core.ErrApprovalRequired) {
		t.Errorf("CallTool on a RequireApproval tool = %v", err)
	}
}

// Cancellation wins at the approval boundary too: a run whose ctx is
// canceled while calls are parked fails with the cancellation error,
// the parked calls riding on RunError.Result.Pending so the transcript
// stays resumable.
func TestCanceledRunWithPendingCallsFails(t *testing.T) {
	slow := core.Tool("slow", "", func(ctx context.Context, _ struct{}) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	refund := core.Tool("refund", "", func(_ context.Context, _ struct{}) (string, error) {
		return "r", nil
	}, core.RequireApproval())
	model := wefttest.Script(
		wefttest.ToolCalls(
			wefttest.Call{ID: "s1", Name: "slow"},
			wefttest.Call{ID: "r1", Name: "refund"},
		),
		wefttest.Say("done"),
	)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, err := core.New(model, slow, refund).Generate(ctx, core.Prompt("x"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled even with a call parked", err)
	}
	var re *core.RunError
	if !errors.As(err, &re) {
		t.Fatalf("err = %T, want *RunError", err)
	}
	if ids := callIDs(re.Result.Pending); ids != "r1" {
		t.Errorf("RunError.Result.Pending = %q, want the parked call resumable", ids)
	}
	// The resumable claim: a later run resolves it with a decision.
	if _, err := core.New(wefttest.Script(wefttest.Say("done")), refund).
		Generate(context.Background(), core.Messages(re.Result.Messages...), core.Deny("r1", "canceled earlier")); err != nil {
		t.Fatalf("resume after cancellation failed: %v", err)
	}
}

// Agent.CallTool applies the agent-level StrictInput the way the loop
// does — the manual dispatch seam must not be laxer than the loop.
func TestCallToolAppliesAgentStrictInput(t *testing.T) {
	tool := core.Tool("t", "", func(_ context.Context, in struct{ A int }) (string, error) {
		return fmt.Sprint(in.A), nil
	})
	agt := core.New(wefttest.Script(wefttest.Say("ok")), tool, core.StrictInput())
	_, err := agt.CallTool(context.Background(), core.ToolCallPart{ID: "c", Name: "t", Args: json.RawMessage(`{"A":1,"typo":2}`)})
	var te *core.ToolError
	if !errors.Is(err, core.ErrInvalidToolInput) || !errors.As(err, &te) || te.Code != "INVALID_INPUT" {
		t.Errorf("CallTool = %v, want INVALID_INPUT wrapping ErrInvalidToolInput (agent-level StrictInput)", err)
	}
	// Without StrictInput the same call is accepted, as in the loop.
	lax := core.New(wefttest.Script(wefttest.Say("ok")), tool)
	if out, err := lax.CallTool(context.Background(), core.ToolCallPart{ID: "c", Name: "t", Args: json.RawMessage(`{"A":1,"typo":2}`)}); err != nil || out != "1" {
		t.Errorf("lax CallTool = %q, %v; want the stray field ignored", out, err)
	}
}

func TestRunFinishPendingRoundTrips(t *testing.T) {
	ev := core.RunFinish{Steps: 1, Pending: []core.ToolCallPart{{ID: "a", Name: "t", Args: json.RawMessage(`{}`)}}}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	back, err := core.UnmarshalEvent(b)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.(core.RunFinish); len(got.Pending) != 1 || got.Pending[0].ID != "a" {
		t.Errorf("round trip = %+v", got)
	}
	if b, _ := json.Marshal(core.RunFinish{}); strings.Contains(string(b), "pending") {
		t.Errorf("empty Pending must be omitted: %s", b)
	}
}
