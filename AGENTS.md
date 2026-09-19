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
//   INVALID_INPUT: tool "lookup_order": field "days": expected integer, got string
// Give the model a code to branch on; the cause stays in logs:
//   return "", &weft.ToolError{Code: "ORDER_NOT_FOUND", Message: "order 42 does not exist", Err: err}
// Ask the model to fix its arguments; the loop counts and bounds it:
//   return "", weft.ModelRetry("date must be ISO-8601")   // RETRY: … (MaxModelRetries, default 3)
// More per-tool options: weft.Sequential() (barrier), weft.RequireApproval(),
// weft.PromptSnippet("…"), weft.WrapTools(mw...).

// 1b. Tools defined outside Go source: explicit schema, raw args.
//     weft.RawTool("parse_invoice", "…", schema, func(ctx, raw) (string, error))
//     A registry that changes mid-run: the loop re-fetches per step.
//     weft.ToolSource(func() []*weft.ToolDef { return reg.Tools() })

// 1c. Delegate to another agent: a tool whose handler runs it.
//     weft.Subagent("research", "Research a topic in depth.", researcher, weft.Timeout(2*time.Minute))
//     Child events arrive as weft.Nested{CallID, Event}; usage rolls into res.Usage.

// 2. An agent is a value. Build once, run many times, concurrently.
agt := weft.New(model,                       // any weft.Model (adapters, or wefttest.Script)
    weft.Name("support-bot"),                // on RunStart.Agent; weft.Manifest requires it
    weft.Instructions("You are a support agent."),
    weft.StopWhen(weft.HasToolCall("submit")), // intended end (optional)
    weft.MaxSteps(20),                         // safety budget → ErrMaxSteps (default 10)
    weft.UsageLimit(weft.Usage{OutputTokens: 50_000}), // token budget, subagents included → ErrUsageLimit
    weft.DetectLoops(5),                       // identical steps → ErrLoopDetected (off by default)
    weft.PrepareStep(trim),                    // the one loop knob: rewrite the request per step (messages, tools, system)
    weft.Options(Orders(svc)),                 // compose options: a plugin is func(deps) weft.Option
    weft.MaxResultBytes(64 << 10),             // tool-result cap (default 64 KiB; 0 = off)
    weft.Timeout(30*time.Second),              // default per-call deadline (none by default)
    weft.Parallelism(4),                       // or weft.Sequential()
    weft.Thinking(weft.ThinkingConfig{Level: weft.ThinkOff}), // reasoning default (adapters map what they can)
    weft.Tap(func(ctx context.Context, ev weft.Event) {...}), // observer: sees every event, changes nothing
    weft.WrapModel(mw.Retry(), mw.Fallback(backup)),           // model seam: first listed = outermost
    weft.WrapTools(mw.Audit(logger), mw.Allow(permits), mw.MapErrors(nil)), // tool seam, same rule
    lookup,                                    // tools are options
)
// A ToolMiddleware is func(next weft.ToolCaller) weft.ToolCaller; a
// ModelMiddleware is func(next weft.Model) weft.Model. Middleware that
// verifies something puts it on ctx before next; handlers read it via a
// typed accessor (ExampleWrapTools_context). docs/life-of-a-call.md
// shows where every phase sits — phases are docs, seams are code.

// 3. Run it.
res, err := agt.Generate(ctx, weft.Prompt("Where is order 1234?"))
// Thinking also works per run, overriding the agent default — fast by default, think on demand:
//   agt.Generate(ctx, weft.Thinking(weft.ThinkingConfig{Level: weft.ThinkHigh}), weft.Prompt("..."))
// res.Text(), res.Messages (full transcript), res.Steps, res.Usage, res.ID

// 3b. Or stream it.
for ev, err := range agt.Stream(ctx, weft.Prompt("...")).Events() {
    if err != nil { return err }            // non-nil at most once, as the last element
    switch ev := ev.(type) {
    case weft.RunStart:      // ID, Model (ModelInfo when the model reports one)
    case weft.ReasoningDelta: // Text (provider reasoning; signatures stay on the part)
    case weft.TextDelta:     // Text
    case weft.ToolArgsDelta: // Name, Args — progress while the model writes a tool call
    case weft.ToolStart:     // Seq, CallID, Name, Args
    case weft.ToolFinish:    // Seq, CallID, Name, Content, IsError
    case weft.Nested:        // Seq, CallID, Event — a subagent's event, numbered from this run's counter
    case weft.StepFinish:    // Index, Reason, Usage
    case weft.RunFinish:     // Usage, Steps, Pending (calls awaiting Approve/Deny)
    }
}

// 4. Continue a conversation: feed the transcript back.
res2, err := agt.Generate(ctx, weft.Messages(res.Messages...), weft.Prompt("And order 5678?"))

// 4a. Approval: a RequireApproval tool parks its call; the run ends
//     successfully with res.Pending set. Resume with a decision:
//     agt.Generate(ctx, weft.Messages(res.Messages...), weft.Approve(id), weft.Deny(id, "why"))
//     Middleware parks any call by returning an error wrapping ErrApprovalRequired.

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
network. The switch guards clients the adapters build themselves from
credentials; a client injected via the adapters' `Client(c)` option is
a test double by construction and stays reachable, so weft's own
offline suites run under deny too (`make offline`). `wefttest` models
ignore it.

## The rules (do not break these; tests pin them)

1. **Tool error = data the model sees; run error = Go error.** A handler
   error, panic, unknown tool, or bad arguments becomes
   `ToolResultPart{IsError: true}`; siblings keep running. Model
   failure, context cancellation, `MaxSteps`, and a malformed
   tool-source snapshot (`ErrDuplicateTool`, `ErrNilTool`) return an
   error, always `*RunError` with the partial transcript in `.Result`.
   Use `errors.Is`.
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
10. **Two behavioural seams, one tap, no more.** `WrapModel` and
    `WrapTools` (chi-style, first listed = outermost) are where
    behaviour attaches; `Tap` observes. Panic containment and timeouts
    sit outside the tool chain. A third seam, or a phase turned into a
    hook, needs an ADR (ADR 0006).
11. **A `max_tokens` step with tool calls executes none of them**: every
    call gets `tool call X was not executed: the response hit the
    output token limit` and the model retries with a full budget.
12. **Approval is a run boundary**: pending calls end the run
    successfully (`RunResult.Pending`, `RunFinish.Pending`); the next
    run resumes with `Approve`/`Deny`; undecided calls are `DENIED: no
    decision`. A policy seam, not a security boundary (ADR 0007).
13. **Budgets fail at the continuation point.** `MaxSteps`,
    `UsageLimit`, `MaxModelRetries`, `DetectLoops` are checked only
    when the loop would call the model again; a step that ends the
    run succeeds. A breach is `*RunError` with the partial transcript.
    Child runs are tools: their failure is data, their usage is yours.

## Working in this repo

- Run `make test` (`go test -race ./...`), `make vet`, `make lint`
  before claiming done. Add a test in `contract_test.go` for any promise
  you add or change; add a godoc example for any public feature.
- Keep the public surface small: functional options, sealed interfaces
  (`Option`, `Event`, `Part`, `ModelEvent`), no config structs, no
  globals, `context.Context` first in every signature.
- Prefer additive change. CI runs the apidiff gate (`make apidiff`,
  `scripts/apidiff.sh`) against the last tag and fails on incompatible
  changes; pre-1.0 a deliberate source-compatible widening is
  acknowledged line-by-line in `.apidiff-allow`. Renames always fail.
- Docs: `README.md` (usage), `docs/adr/` (why), this file (map). Update
  the one that applies in the same change.
