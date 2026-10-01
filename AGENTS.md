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

// 1d. MCP, both ways (module weft/mcp; alias the SDK as sdk):
//     tools, _ := mcp.Tools(ctx, sess, mcp.Prefix("gh_"))   // a server's tools as weft tools (RawTool; schema verbatim)
//     mcp.Serve(srv, agt, "Support agent.")                 // an agent (and its tools) as an MCP server

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
    weft.ToolChoice(weft.ToolChoiceConfig{Mode: weft.ToolChoiceAny}), // force a tool call every step (Named/None too; PrepareStep can rewrite per step)
    weft.Params(weft.RequestParams{Temperature: ptr(0.2)}), // per-run/step sampling (TopP, MaxTokens, Stop, Seed; nil = construction default; a negative MaxTokens fails the step)
    weft.Tap(func(ctx context.Context, ev weft.Event) {...}), // observer: sees every event, changes nothing
    weft.OnRunEnd(func(ctx context.Context, res *weft.RunResult, err error) {...}), // outcome observer: once per run, even on failure — the otel pipeline pairs it with Tap
    weft.OnMessages(func(ctx context.Context, step int, msgs []weft.Message) {...}), // run option: transcript observer — exact messages as they join, for incremental persistence
    // In any observer: weft.AgentFromContext(ctx) → the running *Agent (nil outside a run); agt.Logger() → the run lines' sink.
    weft.TracerProvider(tp),                   // OTel spans: invoke_agent › chat / execute_tool (default: the global provider; no-op until an SDK registers)
    weft.LoggerProvider(lp),                   // OTel records through the Logs API (ADR 0024): events, deltas, transcript batches; same default
    weft.Content(true),                        // records carry content: true always, false never; default: as the logger's Enabled answers
    weft.Logger(logger),                       // one Debug line per run, model call, tool call (default: slog.Default, silent unless Debug is on)
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
// Per-run configuration (ADR 0024 D5): Instructions/MaxSteps/Parallelism are dual Option/RunOption like
// Thinking; MaxSteps/Parallelism lower only per run — a raise is ErrInvalidRunOption, before any model call.
//   agt.Generate(ctx, weft.Prompt("..."), weft.Instructions("one-run prompt"), weft.MaxSteps(6),
//       weft.OnlyTools("lookup", "refund"),   // narrow to registered tools; unknown name → ErrInvalidRunOption
//       weft.UseModel(alt),                    // the WrapModel chain rebuilt over alt, this run alone
//       weft.ParkOn("refund"),                 // park at the approval boundary (ADR 0007 per run); Approve resumes
//       weft.Metadata(map[string]string{"tenant": "acme"})) // on every span and record; subagent runs inherit
//   weft.MetadataFromContext(ctx)   // read the merged pairs back (Tap, tool handler, child run)
//   weft.StripContent(ev)           // the one content-shaping table: what a content-off destination receives
// A changed run carries weft.override.hash + weft.override.* on its invoke_agent span (the experiment's fingerprint).
// res.Text(), res.Messages (full transcript), res.Steps, res.Usage, res.ID

// 3a. Steer a running turn (ADR 0019): a pull source the loop drains at two
//     fixed points — after the tool batch (every call paired with its
//     result), and at what would be the final step, where a delivery
//     redirects into one more step. Never at the approval boundary or
//     after StopWhen: those ends stay ends. Delivered messages are ordinary
//     transcript; non-user roles fail the run with ErrInvalidSteer.
//     Run option only; a Subagent's child run does not inherit it.
//   agt.Generate(ctx, weft.Prompt("…"), weft.Steering(func(ctx, at weft.SteerPoint) []weft.Message { … }))
//   wefttest.NewSteers().At(0, weft.User("…")).Option()  // the deterministic source, replay-safe

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
    case weft.Steered:       // Seq, Step, Messages — delivered by the run's steering source
    case weft.RunFinish:     // Usage, Steps, Pending (calls awaiting Approve/Deny)
    }
}

// 4. Continue a conversation: feed the transcript back.
res2, err := agt.Generate(ctx, weft.Messages(res.Messages...), weft.Prompt("And order 5678?"))

// 4a. Approval: a RequireApproval tool parks its call; the run ends
//     successfully with res.Pending set. Resume with a decision:
//     agt.Generate(ctx, weft.Messages(res.Messages...), weft.Approve(id), weft.Deny(id, "why"))
//     weft.Resolve(id, content) resumes with a result computed outside the process — the
//     handler never runs; Resolve on a non-pending id is a loud run error.
//     Middleware parks any call by returning an error wrapping ErrApprovalRequired.

// 4b. Structured output: a submit_output tool with T's schema; the run
//     ends on a valid call. Invalid → ErrInvalidToolInput result, model repairs.
agt := weft.New(model, weft.Output[Verdict](), lookup)
v, res, err := weft.GenerateAs[Verdict](ctx, agt, weft.Prompt("..."))   // ErrNoOutput if none
v, err := weft.OutputOf[Verdict](res)                                    // after Stream + Wait
dec := weft.NewOutputDecoder[Verdict]()                                 // partials while it streams:
//   for ev := range run.Events() { if p, ok := dec.Feed(ev); ok { render(p) } }; dec.Result()

// 5. Describe the fleet: weft.Manifest(agents...) → weft.json (generated,
//    committed, golden-gated; never read back).

// 6. Record runs (module weft/otel — the store is gone, ADR 0024 step 5; the
//    pipeline is the recorder now, block 9 has the full destination menu):
//    defer otel.Install()()  // local sink ./.weft/weft.db, content on, no network —
//    // every event, delta, transcript record and span leaves as it happens
//    // (weft.Heartbeat keeps a quiet run reading running); the durable run
//    // reads back through obsdb — otel.LocalDB() — never through the process.
//    weft.Metadata({"cwd": wd})  // caller pairs on every record/span of the run
//    // (inherited by subagents; thread sessions stamp weft.session.id,
//    // weft.public_id, weft.turn — block 8).

// 7. Serve the Inspector (module weft/studio; S4 on obsdb — UI, JSON API,
//    OTLP ingest, live, the devtools panel, the playground): setup A,
//    embedded beside the app (§10.1's five lines):
//    mux.Handle("/studio/", http.StripPrefix("/studio",
//        studio.Handler(studio.DB(otel.LocalDB()))))  // the pipeline's handle: history + live [D4]
//    // or studio.New(opts...) *Server with Handler()/Close()/Runtime()
//    //    (the runtime link's server under Playground(true), else nil),
//    //    options DB/Open/Base/Manifest/Title/Capabilities/Token/Live/NoIngest/
//    //    IngestToken/AllowOrigins/Playground; routes register through
//    //    routes.go's groups (panel.go/playground.go add theirs in their own files).
//    // API: meta, runs (+session/public/playground filters), runs/{id}/transcript,
//    //    spans, traces/{id}, sessions, public/{public_id}, /api/live (SSE),
//    //    /api/panel-tokens, /panel.js (the devtools panel, WEFT-DEVTOOLS §5);
//    //    Token(tok) walls everything but /panel.js (bearer or ?token=).
//    // Setup B, any language (module studio/cmd — the one place the clickhouse
//    // driver lives): studio --db sqlite://path | clickhouse://user:pass@host:9000/db
//    //    [--addr --token] serves UI + OTLP ingest on 127.0.0.1:7331.

// 7a. The playground (module weft/runtime): your app dials Studio out and
//     executes experiment commands as runs of the agents you register:
//     defer runtime.Install(runtime.Studio(url, tok) /* or runtime.Local(srv) */,
//         runtime.Agents(support), runtime.Models(map[string]weft.Model{"glm": m}),
//         runtime.Limits(runtime.Budget{MaxTokensPerExperiment: 200_000}),
//         runtime.AllowSideEffects("lookup_order"), runtime.Threads(store))()
//     // WEFT_ENV=dev (or runtime.Enabled(true)) opens the link; commands ack
//     // before they run (at-most-once), tools park unless opted in, budgets
//     // cap each experiment; runs carry weft.playground and never touch
//     // weft.session.id. Studio side: studio.New(..., studio.Playground(true)).

// 8. Sessions (module weft/thread; jsonl.Open(dir) | thread.Memory()):
//    s, _ := thread.Create(ctx, st, agent) — the append-only entry tree; every write
//    through Storage.Append; s.Context() is the leaf's messages, repaired.
//    turn, _ := s.Send(ctx, weft.User("…")) — prompt durable before the run;
//    turn.Wait(); busy: Queue (default) or thread.BusyPolicy(thread.Reject) → ErrBusy.
//    s.Branch(ctx, entryID[, thread.SummarizeLeft()]), s.Fork(ctx, entryID) — the tree,
//    nothing lost; s.Label, s.SetInfo, s.Custom, s.CustomMessage, s.Pin.
//    thread.PublicID(id) — the session's browser-safe handle, create-time only: on
//    every run as weft.public_id beside weft.session.id and weft.turn; List's Meta
//    filter matches the header's copy, so a SetInfo-written id never matches.
//    Compaction (ADR 0020): thread.ContextWindow(n) arms the trigger (reported input +
//    estimated delta > window − Reserve); s.Compact(ctx[, thread.Instructions("…")]),
//    s.PreviewCompaction, s.ApplyCompaction, s.Uncompact; five layers (SummaryModel,
//    SummaryPrompt/Focus/MaxTokens, WithSummarizer/Compactor/Trimmer, hooks,
//    thread.PreferNative), thread.ClearOldToolResults(n) — nothing ever deleted.
//    Approvals (ADR 0021): s.Pending() (restart-safe), s.Decide(ctx, thread.Approve(id) |
//    Deny/Resolve/ResolveError | ApproveAlways) → auto-resume, turn.Next() the resumed
//    turn, s.Resume(ctx); chain: s.Grant(ctx, thread.Grant{Tool, Args: ArgEquals/ArgPrefix/
//    ArgGlob, Deny}), s.Revoke, WithGrantStore, WithApprover + ApproverTimeout, Quorum(n),
//    thread.RequestExpiry, thread.OnRequest, s.Audit(); signed: thread.NewKeyring +
//    WithKeyring, s.Request(id) → thread.SignDecision(key, r, d) → s.DecideSigned (fail-
//    closed: ErrBadSignature/ErrExpired/ErrReplay/ErrArgsChanged/ErrUnknownKey),
//    thread.RequireSigned(); a Send while approvals pend queues behind them.
//    Busy policies (ADR 0019): thread.BusyPolicy(Queue | Reject | Steer | Interrupt |
//    Rollback), or per Send with thread.As(p). Steer delivers mid-run at the drain
//    points — receipts are durable entries (queued → delivered | deferred | dropped);
//    s.Queue(), s.ClearQueue(ctx); a steer meeting StopWhen or approvals defers to a
//    follow-up (Turn.Next). Interrupt cancels the run (dangling calls record the
//    interruption text; a parked boundary is denied); Rollback also branches back.
//    Overflow: ErrContextOverflow → compact (reason overflow) + one re-run
//    (thread.ReRunOnOverflow(false) off); a second failure joins both errors.
//    Durability (ADR 0011 §7): the turn's messages append as they join the run
//    (weft.OnMessages) — a crash mid-turn loses nothing emitted; a failed turn's
//    repaired tail is rewritten on a fresh line. Backends: jsonl.Open(dir) |
//    sqlite.Open(path) (own module, modernc) | thread.Memory(); one writer per
//    session (ErrLocked), readers never lock. Live tail: st.(thread.Watcher).
//    Pool (ADR 0022): pool.New(max) — the one FIFO bound (also the depth guard);
//    p.Wrap(name, desc, agent[, pool.Async()]): sync waits for the child session's
//    answer, async returns the receipt line; p.Submit/Cancel/Close/Receipts/Forward.
//    Children are sessions (Header.Lineage), their cost in Usage.Delegated; a parked
//    child mirrors onto Pending — p.Decide resumes it and resolves the parked call.
//    Watch(ctx, id, afterEntryID). List filters: thread.Query{Meta, TitleSearch}
//    (title = last info entry's, case-insensitive substring), Before/Limit page.

// 9. Observability pipeline (module weft/otel; several destinations at once,
//    each with its own content policy; defer on exit):
//    defer otel.Install(
//        otel.Local("weft.db"),                       // local sink: replay-grade, content on
//        otel.Studio("https://studio.example", token), // OTLP/HTTP, content on
//        otel.Datadog(),                               // the Agent's OTLP intake, content off
//        otel.Exporters(myLogExporter),                // your own exporters
//        otel.Content(otel.ContentConfig{MaxBytes: 32 << 10, Redact: redact}),
//    )()
//    p, err := otel.Start(ctx, opts...)               // Install with errors; otel.NoGlobal() for tests
//    otel.LocalDB()   // the installed pipeline's obsdb.DB (nil without a Local destination)
//    otel.StudioEndpoint()  // the Studio destination weft/runtime dials
//    studio.Handler(studio.DB(otel.LocalDB()))  // setup A (block 7): the Inspector over the
//                               // local sink's handle, history + live [D4]
```

Test offline with `wefttest.Script(wefttest.ToolCalls(...), wefttest.Say(...))`;
replay a recorded real transcript with `wefttest.Replay(t, dir)` (record it
once with `wefttest.Record`; ADR 0017) — the adapters' own parsing is proven
by `wefttest/conformance` fixtures, not by replay; `make fuzz` runs the four
fuzz targets (a new message part or event type adds a seed; a crasher becomes
a committed seed).
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
   permitted dependency (ADR 0016); exporters stay in a satellite.
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
    behaviour attaches; `Tap` observes. The loop reports its own spans
    and log lines at its phases (ADR 0016); that is reporting, not a
    seam. Panic containment and timeouts sit outside the tool chain. A
    third seam, or a phase turned into a hook, needs an ADR (ADR 0006).
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
