// Command otel demonstrates weft's out-of-the-box observability: set up
// an OpenTelemetry SDK, pass its provider to the agent (or register it
// globally), and every run emits the full span tree — one invoke_agent
// span per run, one chat span per model call, one execute_tool span per
// executed tool call, children nested under their parents — with the
// GenAI semantic attributes and no message content.
//
// The agent here is scripted (wefttest), so the demo runs offline; swap
// the model for a provider adapter and nothing else changes.
package main

import (
	"context"
	"log"

	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

func main() {
	exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		log.Fatal(err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	if _, err := run(context.Background(), tp); err != nil {
		log.Fatal(err)
	}
}

// run drives the demo agent under the given provider — the shape the
// test reuses with a real SDK and an in-memory exporter. A subagent
// shows the nesting: the child run's invoke_agent span hangs under the
// delegating execute_tool span.
func run(ctx context.Context, tp trace.TracerProvider) (*weft.RunResult, error) {
	echo := weft.Tool("echo", "Echo a message.", func(_ context.Context, in struct {
		Msg string `json:"msg"`
	}) (string, error) {
		return "echo: " + in.Msg, nil
	})
	research := weft.New(
		wefttest.Script(wefttest.Say("The adapters wrap the vendors' official SDKs.")),
		weft.Name("research"),
		weft.TracerProvider(tp),
	)
	agt := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "research", Args: `{"prompt":"How do the adapters handle HTTP?"}`}),
			wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hi"}`}),
			wefttest.Say("done"),
		),
		weft.Name("demo"),
		weft.TracerProvider(tp),
		echo,
		weft.Subagent("research", "Research a topic in depth.", research),
	)
	return agt.Generate(ctx, weft.Prompt("Research the adapters, then echo hi."))
}
