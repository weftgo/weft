# weft — guide for coding agents and contributors

This file is the shortest accurate description of the package for anyone
(human or model) writing code with or inside weft. The godoc is the
authority; this is the map.

## Using weft (the whole API on one screen)

```go
// 1. A tool is a plain function. Schema comes from the input struct's tags.
type LookupInput struct {
    OrderID string `json:"order_id" jsonschema:"the order to look up"`
}
lookup := weft.Tool("lookup_order", "Look up an order by ID.",
    func(ctx context.Context, in LookupInput) (Order, error) { // string Out → verbatim; other Out → JSON
        call, _ := weft.CallFromContext(ctx) // RunID, Step, CallID, Name
        return db.Find(ctx, in.OrderID)
    },
    weft.Timeout(5*time.Second),   // per-tool policy: deadline → error result
    weft.MaxResultBytes(0),        // this tool's output is never capped
    weft.StrictInput(),            // undeclared argument fields are rejected
)
// Bad arguments are ErrInvalidToolInput results naming the field:
//   field "days": expected integer, got string

// 1b. Tools defined outside Go source: explicit schema, raw args.
//     weft.RawTool("parse_invoice", "…", schema, func(ctx, raw) (string, error))
//     A registry that changes mid-run: the loop re-fetches per step.
//     weft.ToolSource(func() []*weft.ToolDef { return reg.Tools() })

// 2. An agent is a value. Build once, run many times, concurrently.
agt := weft.New(model,                       // any weft.Model (adapters, or wefttest.Script)
    weft.Name("support-bot"),                // on RunStart.Agent; weft.Manifest requires it
    weft.Instructions("You are a support agent."),
    weft.StopWhen(weft.HasToolCall("submit")), // intended end (optional)
    weft.MaxSteps(20),                         // safety budget → ErrMaxSteps (default 10)
    weft.MaxResultBytes(64 << 10),             // tool-result cap (default 64 KiB; 0 = off)
    weft.Timeout(30*time.Second),              // default per-call deadline (none by default)
    weft.Parallelism(4),                       // or weft.Sequential()
    weft.Tap(func(ctx context.Context, ev weft.Event) {...}), // observer: sees every event, changes nothing
    lookup,                                    // tools are options
)

// 3. Run it.
res, err := agt.Generate(ctx, weft.Prompt("Where is order 1234?"))
// res.Text(), res.Messages (full transcript), res.Steps, res.Usage, res.ID

// 3b. Or stream it.
for ev, err := range agt.Stream(ctx, weft.Prompt("...")).Events() {
    if err != nil { return err }            // non-nil at most once, as the last element
    switch ev := ev.(type) {
    case weft.RunStart:      // ID, Model (ModelInfo when the model reports one)
    case weft.ReasoningDelta: // Text (provider reasoning; signatures stay on the part)
    case weft.TextDelta:     // Text
    case weft.ToolStart:     // Seq, CallID, Name, Args
    case weft.ToolFinish:    // Seq, CallID, Name, Content, IsError
    case weft.StepFinish:    // Index, Reason, Usage
    case weft.RunFinish:     // Usage, Steps
    }
}

// 4. Continue a conversation: feed the transcript back.
res2, err := agt.Generate(ctx, weft.Messages(res.Messages...), weft.Prompt("And order 5678?"))

// 4b. Structured output: a submit_output tool with T's schema; the run
//     ends on a valid call. Invalid → ErrInvalidToolInput result, model repairs.
agt := weft.New(model, weft.Output[Verdict](), lookup)
v, res, err := weft.GenerateAs[Verdict](ctx, agt, weft.Prompt("..."))   // ErrNoOutput if none
v, err := weft.OutputOf[Verdict](res)                                    // after Stream + Wait

// 5. Describe the fleet: weft.Manifest(agents...) → weft.json (generated,
//    committed, golden-gated; never read back).
```

Test offline with `wefttest.Script(wefttest.ToolCalls(...), wefttest.Say(...))`.
Provider adapters (`weft/openai`, `weft/anthropic`, `weft/google` — own
modules, official vendor SDKs) pass the shared executable contract
`wefttest/conformance` (ADR 0013); run it live behind `-tags live`.
Set `WEFT_MODEL_REQUESTS=deny` to make every first-party adapter refuse
to call its provider (`weft.ModelRequestsAllowed()` reads it on every
call, so suites can toggle it per test; adapters yield
`weft.ErrModelRequestsDenied`) — for suites that must never reach the
network. `wefttest` models ignore it.

## The rules (do not break these; tests pin them)

1. **Tool error = data the model sees; run error = Go error.** A handler
   error, panic, unknown tool, or bad arguments becomes
   `ToolResultPart{IsError: true}`; siblings keep running. Only model
   failure, context cancellation, and `MaxSteps` return an error, always
   `*RunError` with the partial transcript in `.Result`. Use `errors.Is`.
2. **Messages are `role` + typed parts, events are tagged structs**, both
   with a `type` discriminator on the wire (`weft.UnmarshalEvent` restores
   events; unknown types are errors, never drops). Roles: `user`,
   `assistant`, `tool`. No system role.
   Multimodal input is a part: `weft.UserParts(TextPart{...},
   FilePart{MediaType: "image/png", ...})`; an adapter that cannot carry
   it fails wrapping `ErrUnsupported`. A transcript fed back in is
   repaired first — every tool call gets a result, orphans are dropped;
   see `weft.Repair`.
3. **Tools start in call order; results are in call order; events carry
   `Seq`** so streams replay exactly. `Sequential()` = one at a time.
   Taps (`weft.Tap`) see the same order as `Events()`.
4. **Every run has an id.** `RunStart` is the first event; `RunFinish` the
   last of a successful run.
5. **Model-visible behaviour is a contract** (tool result text, schema,
   transcript shape). Changing it needs an ADR in `docs/adr/`.
6. **Nothing above the core is imported by the core.** No HTTP, no
   database, no tracing backend. The OTel API package is the one
   permitted future dependency.
7. **The Model stream contract is enforced by the loop**: exactly one
   `ModelFinish`, nothing after it, panics converted to run errors —
   all wrapping `ErrModelContract`. A broken adapter cannot corrupt a
   transcript.
8. **Truncation is visible, never silent**: a `max_tokens` finish is
   recorded on `RunResult.StopReason`; tool results are capped (64 KiB
   default, `weft.MaxResultBytes`, per agent or per tool) with a marker
   the model sees.
9. **A hung tool never hangs the run**: `weft.Timeout` on a tool or
   agent records "tool X timed out after d" as an error result and
   abandons the handler's goroutine; handlers must honour ctx.

## Working in this repo

- Run `make test` (`go test -race ./...`), `make vet`, `make lint`
  before claiming done. Add a test in `contract_test.go` for any promise
  you add or change; add a godoc example for any public feature.
- Keep the public surface small: functional options, sealed interfaces
  (`Option`, `Event`, `Part`, `ModelEvent`), no config structs, no
  globals, `context.Context` first in every signature.
- Prefer additive change. Post-1.0 the apidiff gate fails incompatible
  changes; pre-1.0 we still avoid renames (naming stability is API
  stability).
- Docs: `README.md` (usage), `docs/adr/` (why), this file (map). Update
  the one that applies in the same change.
