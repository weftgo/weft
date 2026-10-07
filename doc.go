// Package weft is a modular framework for building AI agents in Go,
// designed the way the standard library is: small interfaces, context
// everywhere, functional options, wrapped errors, and zero required
// configuration.
//
// This package is the framework's front door: it re-exports the agent
// loop that github.com/weftgo/weft/core implements — tools from plain
// Go functions, parallel tool calls with defined failure semantics,
// typed streaming events, structured output, approvals, steering,
// subagents — so one import gives the loop and one module gives the
// whole framework:
//
//	github.com/weftgo/weft            the loop (this package)
//	github.com/weftgo/weft/openai     OpenAI and OpenAI-compatible servers
//	github.com/weftgo/weft/anthropic  Anthropic
//	github.com/weftgo/weft/google     Google Gemini
//	github.com/weftgo/weft/mcp        MCP both ways
//	github.com/weftgo/weft/mw         reference middleware
//	github.com/weftgo/weft/wefttest   the scripted model for offline tests
//	github.com/weftgo/weft/thread     durable sessions
//	github.com/weftgo/weft/otel       recording over OpenTelemetry
//	github.com/weftgo/weft/obsdb      the store the records land in
//	github.com/weftgo/weft/studio     the Inspector, devtools panel, playground
//	github.com/weftgo/weft/runtime    the playground's in-app side
//
// A tool is a plain function; its JSON Schema is derived from the input
// struct. An agent is a value built once with options and run many times:
//
//	echo := weft.Tool("echo", "Echo a message",
//		func(ctx context.Context, in struct {
//			Msg string `json:"msg"`
//		}) (string, error) {
//			return "echo: " + in.Msg, nil
//		})
//
//	agt := weft.New(model, weft.Instructions("You are helpful."), echo)
//	res, err := agt.Generate(ctx, weft.Prompt("Say hi."))
//
// Every name here is an alias of, or a one-line wrapper around, the
// same name in core, so values flow between the two without
// conversion; the godoc of each is the authority. A service that wants
// the loop alone, with the OpenTelemetry API as its only dependency,
// imports github.com/weftgo/weft/core instead and writes core.New.
package weft
