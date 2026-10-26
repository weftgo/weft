package core_test

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
)

// cacheModel is a model middleware that describes itself for the
// manifest (the optional Describe convention) and declares the model
// it wraps (Unwrap), so the walk goes on past it.
type cacheModel struct {
	next core.Model
	size int
}

func (m cacheModel) Stream(ctx context.Context, req core.ModelRequest) iter.Seq2[core.ModelEvent, error] {
	return m.next.Stream(ctx, req)
}
func (m cacheModel) Unwrap() core.Model { return m.next }
func (m cacheModel) Describe() core.MiddlewareInfo {
	return core.MiddlewareInfo{Name: "cache", Params: map[string]string{"size": fmt.Sprint(m.size)}}
}

// The manifest lists an agent's model middleware outer to inner: weft's
// own describe themselves, a layer of yours may (Describe), and any
// other is named by its type.
func ExampleMiddlewareInfo() {
	cache := func(next core.Model) core.Model { return cacheModel{next: next, size: 128} }
	agt := core.New(wefttest.Script(), core.Name("support"),
		core.WrapModel(mw.Retry(mw.MaxRetries(2)), cache, mw.RepairJSON()))
	b, _ := core.Manifest(agt)
	var doc struct {
		Agents []struct {
			ModelMiddleware []core.MiddlewareInfo `json:"model_middleware"`
		} `json:"agents"`
	}
	_ = json.Unmarshal(b, &doc)
	for _, l := range doc.Agents[0].ModelMiddleware {
		fmt.Println(l.Name, l.Params["max_retries"]+l.Params["size"])
	}
	// Output:
	// mw.Retry 2
	// cache 128
	// mw.RepairJSON
}

// A tool middleware tells the call's execute_tool span what it decided
// — here a refusal, recorded as weft.tool.denied_by "budget" — without
// changing the result the model sees.
func ExampleToolReportFromContext() {
	budget := func(next core.ToolCaller) core.ToolCaller {
		return func(ctx context.Context, call core.ToolCallPart) (string, error) {
			if call.Name == "wire_money" {
				core.ToolReportFromContext(ctx).Denied("budget")
				return "", &core.ToolError{Code: core.CodeDenied, Message: "over budget"}
			}
			return next(ctx, call)
		}
	}
	wire := core.Tool("wire_money", "", func(context.Context, struct{}) (string, error) { return "sent", nil })
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "wire_money"}),
		wefttest.Say("done")), wire, core.WrapTools(budget))
	res, _ := agt.Generate(context.Background(), core.Prompt("pay"))
	fmt.Println(res.Steps[0].Results[0].Content)
	// Output:
	// DENIED: over budget
}

// DenyBy denies a parked call as Deny does, naming the deny grant that
// decided on its execute_tool span (weft.tool.denied_by grant,
// weft.grant.id).
func ExampleDenyBy() {
	gated := core.Tool("refund", "", func(context.Context, struct{}) (string, error) { return "refunded", nil },
		core.RequireApproval())
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{ID: "c1", Name: "refund"}),
		wefttest.Say("ok")), gated)
	first, _ := agt.Generate(context.Background(), core.Prompt("refund it"))
	second, _ := agt.Generate(context.Background(), core.Messages(first.Messages...),
		core.DenyBy("c1", "refunds are closed", "grant", "g-1"))
	fmt.Println(second.Messages[2].Content[0].(core.ToolResultPart).Content)
	// Output:
	// DENIED: refunds are closed
}

// A model middleware that rewrites a tool call's arguments reports the
// repair (Reporter.Repair): a weft.repair event on the step's chat span
// and weft.tool.args_repaired on the call — what mw.RepairJSON does.
func ExampleReporter_Repair() {
	unquote := func(next core.Model) core.Model {
		return yieldModel(func(ctx context.Context, req core.ModelRequest, yield func(core.ModelEvent, error) bool) {
			for ev, err := range next.Stream(ctx, req) {
				if call, ok := ev.(core.ModelToolCall); ok && len(call.Args) > 1 && call.Args[0] == '\'' {
					fixed := json.RawMessage(`{}`)
					core.ReportFromContext(ctx).Repair(core.RepairInfo{
						CallID: call.ID, Kind: "unquote", Original: call.Args, Repaired: fixed})
					call.Args = fixed
					ev = call
				}
				if !yield(ev, err) {
					return
				}
			}
		})
	}
	echo := core.Tool("echo", "", func(context.Context, struct{}) (string, error) { return "ok", nil })
	agt := core.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `'{}'`}),
		wefttest.Say("done")), echo, core.WrapModel(unquote))
	res, _ := agt.Generate(context.Background(), core.Prompt("go"))
	fmt.Println(res.Steps[0].Results[0].Content)
	// Output:
	// ok
}
