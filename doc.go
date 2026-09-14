// Package weft is a thin, opinionated core for building agents in Go.
//
// Weft runs the agent loop — call a model, execute its tool calls in
// parallel with defined failure semantics, stream typed events while work is
// in flight — and nothing else. It is designed the way the standard library
// is: small interfaces, context everywhere, functional options, wrapped
// errors, and no required configuration.
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
// A run ends when the model replies without tool calls or a StopWhen
// condition is met; MaxSteps is the safety budget behind both. Streaming
// is a range loop over typed events (Agent.Stream). Tool failures are data
// the model sees; only model failures, cancellation, and the step budget
// reach the caller, as *RunError. Output constrains the final answer to
// a struct (GenerateAs), and trailing options on Tool — Timeout,
// MaxResultBytes, StrictInput — set per-tool policy. Thinking sets the
// reasoning depth (agent default, run override).
//
// The model seam (Model) is streaming-first; provider adapters translate
// vendor wire formats into weft's events. The wefttest package provides a
// scriptable Model for offline tests.
package weft
