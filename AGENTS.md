# weft — guide for coding agents and contributors

This file is the shortest accurate description of the package for anyone
(human or model) writing code with or inside weft. The godoc is the
authority; this is the map.

Two Go modules (ADR 0027). `github.com/weftgo/weft` is the framework:
the root package is the loop, and every layer below — `openai`,
`anthropic`, `google`, `mcp`, `mw`, `wefttest`, `thread`, `otel`,
`obsdb`, `studio`, `runtime`, `scope` — is a package of it, one version, one
`go get`. `github.com/weftgo/weft/core` is the loop alone (plus
`core/wefttest` and `core/mw`), the module to import when nothing else
is wanted; its only dependency is the OTel API. The root package, `mw`,
`wefttest` and `wefttest/conformance` are generated facades over
`core` — every name an alias of or a wrapper around the same name there
(`weft.Agent` is `core.Agent`) — so inside this repo the layers import
`core`, examples and user code import `weft`, and `make generate`
follows any change to core's exported API.

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
// weft.PromptSnippet("…"), weft.Replay(weft.ReplaySafe) (the side-effect class for
// re-runs: unannotated counts as never — substitute-or-park, never silently re-fired),
// weft.WrapTools(mw...).

// 1b. Tools defined outside Go source: explicit schema, raw args.
//     weft.RawTool("parse_invoice", "…", schema, func(ctx, raw) (string, error))
//     A registry that changes mid-run: the loop re-fetches per step.
//     weft.ToolSource(func() []*weft.ToolDef { return reg.Tools() })

// 1c. Delegate to another agent: a tool whose handler runs it.
//     weft.Subagent("research", "Research a topic in depth.", researcher, weft.Timeout(2*time.Minute))
//     Child events arrive as weft.Nested{CallID, Event}; usage rolls into res.Usage.

// 1d. MCP, both ways (package weft/mcp; alias the SDK as sdk):
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
    // weft.OnMessages(func(ctx context.Context, step int, msgs []weft.Message) {...}) is a RunOption — pass it to Generate/Stream, not New:
    //   the transcript observer — exact messages as they join, for incremental persistence
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
// Inside the model chain, weft.ReportFromContext(ctx).Attempt(weft.AttemptInfo{…}) / .Raw(…)
// reports an attempt (an `attempt` span under `chat`) — reporting, not a seam;
// a no-op outside a run's model call; mw.Retry/mw.Fallback use it, adapters may.

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
//       weft.ParkAllExcept("lookup"),          // default-deny: every other call parks — ToolSource tools and Subagent child runs included
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

// 6. Record runs (package weft/otel — the store is gone, ADR 0024 step 5; the
//    pipeline is the recorder now, block 9 has the full destination menu):
//    defer otel.Install()()  // local sink ./.weft/weft.db, content on, no network —
//    // every event, delta, transcript record and span leaves as it happens
//    // (weft.heartbeat records — otel.Heartbeat(d), default 10 s — keep a quiet
//    // run reading running); the durable run reads back through obsdb —
//    // otel.LocalDB() — never through the process.
//    // With WEFT_STUDIO_URL unset (and WEFT_DISCOVERY not "off") Install also reads
//    // the discovery file a running `weft studio`/`weft dev` wrote (./.weft, then
//    // $XDG_RUNTIME_DIR/weft, then the user cache dir; internal/discovery): the app
//    // exports to that Studio with no configuration, one INFO line names it. Trusted
//    // only when its url is loopback, fresh (pid alive, < 24 h) and, on unix, 0600 and
//    // this user's; else ignored at Debug. Explicit otel.Studio(...) > WEFT_STUDIO_URL >
//    // the file: either switches the read off, as do WEFT_DISCOVERY=off and NoEnv().
//    weft.Metadata(map[string]string{"cwd": wd})  // run option: caller pairs on every record/span of the run
//    // (inherited by subagents; thread sessions stamp weft.session.id,
//    // weft.public_id, weft.turn — block 8).

// 7. Serve the Inspector (package weft/studio; S4 on obsdb — UI, JSON API,
//    OTLP ingest, live, the devtools panel, the playground): setup A,
//    embedded beside the app (§10.1's five lines):
//    mux.Handle("/studio/", http.StripPrefix("/studio",
//        studio.Handler(studio.DB(otel.LocalDB()))))  // the pipeline's handle: history + live [D4]
//    // or studio.New(opts...) *Server with Handler()/Close()/Runtime()
//    //    (the runtime link's server under Playground(true), else nil),
//    //    options DB/Open/Base/Manifest/Title/Capabilities/Token/Live/NoIngest/
//    //    IngestToken/AllowOrigins/Playground; routes register through
//    //    routes.go's groups (panel.go/playground.go add theirs in their own files).
//    // API: meta (db {kind, path, size: path/size to loopback and the server token only},
//    //    auth_required, content, runtimes, manifest_check, pricing, retention — `weft doctor` prints it
//    //    line by line; capabilities_off {capability: why} names the option/flag that left one off;
//    //    manifest_sources), manifest (the weft.json, else the manifests runtimes registered with —
//    //    remembered in memory by (service, manifest_hash), each in sources[] live or remembered; a
//    //    Studio restart forgets them until the next registration; manifest_check.source file|runtime),
//    //    runs (+session/public/playground filters; all=1 lists child
//    //    runs too), runs/{id} (with its compactions: run-scope views, then session markers),
//    //    runs/{id}/events|transcript|spans|requests|tools, runs/{id}/steps/{n} (one step
//    //    assembled: request, attempts, events, tool calls, children, holes; capability
//    //    "steps"), runs/{id}/export?format=json|jsonl|otlp|wefttest (the whole run as one
//    //    download, capability "export"; otlp re-ingests through /v1/*, wefttest is a zip
//    //    wefttest.Replay reads — a compacted step keys on its view, noted compacted_at),
//    //    runs/{id}/logs?from=&limit=&severity= (the app's own log lines under the run's
//    //    spans, capability "logs"; refused to a read-scoped token: they may carry prompts;
//    //    the run row carries delta_count), traces/{id},
//    //    sessions, sessions/{id}, sessions/{id}/public_id (the reverse of public/{public_id}: the dev token's
//    //    alone, every panel token 403 hidden), public/{public_id}, /api/live (SSE), /api/panel-tokens (with a Token),
//    //    /panel.js (the devtools panel, WEFT-DEVTOOLS §5 — its host API, plan C4: the element's
//    //    open/close/toggle/isOpen/scope/select/on/studioLink, window.weft.devtools while connected
//    //    unless data-global="off", events weft:run|parked|error; configured by a data-weft
//    //    script tag, weft:* meta tags, or <base>/panel-config.json; studio/README.md
//    //    has the ladder; it follows a scope — data-scope="pub_…;session=…;flow=…;run=…" or
//    //    window.__WEFT__ (data-public-id deprecated), else the Weft-Scope header of the page's
//    //    same-origin fetches: a read-only window.fetch wrapper, on by default only on loopback with
//    //    no or a dev token, restored on disconnect, or a data-weft-scope DOM marker (the helpers set it;
//    //    attributes read through one MutationObserver, the marker holding focus wins, a header switcher
//    //    when several conversations are known; off only under a read panel token off loopback),
//    //    data-detect="headers"|"markers"|"headers,markers"|"off" (off: those two rungs only), or the page
//    //    URL's ?weft_scope= / #weft_scope= (below data-scope, above markers and headers; never gated,
//    //    never written); with none, "no conversation detected on this page · how to scope" over the latest
//    //    runs, streamed on agent=; the collapsed pill pulses "● step" while a run runs; the same bytes on npm as @weftgo/devtools — import "@weftgo/devtools",
//    //    mount/scope/open/close/on, /react /vue /svelte marker helpers; studio/web/npm,
//    //    make devtools-npm, never published by the build), /panel-config.json
//    //    ({endpoint, version, capabilities}; loopback Host / same-origin only, else
//    //    404; capability "panel-config"); Token(tok) walls the
//    //    /api tree (the Authorization bearer only: ?token= is refused on every /api route, in every setup;
//    EventSource opens /api/live with POST /api/live-grant's 60 s sig: ?<selector>&kinds=…&sig=; a panel
//    token's stream ends at its expiry with `event: expired`) — the UI shell and /panel.js are static,
//    //    OTLP ingest (/v1/traces, /v1/logs) carries its own IngestToken.
//    // Setup B, any language (package cmd/weft — `go install github.com/weftgo/weft/cmd/weft@latest`;
//    // the one place the clickhouse driver lives): weft studio --db sqlite://path |
//    //    clickhouse://user:pass@host:9000/db [--addr --token --manifest weft.json (or
//    //    WEFT_MANIFEST; else the nearest weft.json upward) --open --no-playground] serves UI +
//    //    OTLP ingest + the playground on 127.0.0.1:7331. The dev token is stable per DB
//    //    (<db>.token beside the file, 0600, never printed; --rotate-token renews it,
//    //    --token / WEFT_STUDIO_TOKEN override; a fileless DB gets a printed per-process
//    //    one); studio.json {url, token, db, pid, started, version} (0600, removed on exit)
//    //    is the discovery file apps find it through (block 6). The API over a terminal (--url
//    //    WEFT_STUDIO_URL, --token WEFT_STUDIO_TOKEN): weft doctor checks one; weft runs
//    //    [--agent --since --failed --limit --json]; weft open <run id> [--open --with-token]
//    //    (prints the bare <url>/runs/<id>; the token only to the browser or on --with-token);
//    //    weft export <run id> [--format json|jsonl|otlp | --wefttest ./testdata --test Name
//    //    --force] (fixtures where wefttest.Replay reads them; --force replaces, never
//    //    merges); weft version.
//    //    weft dev [studio's flags --no-watch --watch dir] [-- go run ./cmd/app] (default: go run .):
//    //    Studio in-process + the app with WEFT_ENV=dev (kept if set), WEFT_STUDIO_URL,
//    //    WEFT_STUDIO_TOKEN, WEFT_DB set (plain env vars the app may set itself), restarted
//    //    on a .go save (300 ms debounce; its process group stopped: SIGTERM, 5 s, SIGKILL);
//    //    one line per start: studio <url> (bare; #token= only for a fileless DB's generated
//    //    token) · app pid n · runtime rt_… registered.
//    //    Port policy (internal/listen): busy 7331 + a Studio on the same DB → reused
//    //    ("studio already running at … (pid n), reusing", exit 0); anything else → the
//    //    next free port in 7331–7340, said in one line; --addr / WEFT_STUDIO_ADDR pins
//    //    (busy → exit 1 naming it). /api/meta's pid follows db.path's guard.

// 7b. The scope header (package weft/scope; plan C3): the app's own chat handler tells the
//     devtools panel which conversation and run a response belongs to, in one line —
//     mux.Handle("POST /chat", scope.Header(chat, func(r *http.Request) scope.Scope { return scope.Scope{PublicID: pub} }))
//     // Weft-Scope: pub_…;session=…;flow=…;run=… (scope.Scope.String / scope.Parse, the web
//     // lib/scope.ts form, both pinned by studio/testdata/scope.golden.json); scope.Set(w, s) once
//     // the run id is known (before the first write); Access-Control-Expose-Headers gains
//     // Weft-Scope; never a token. Not in the root package: that one is core's generated facade.

// 7a. The playground (package weft/runtime): your app dials Studio out and
//     executes experiment commands as runs of the agents you register:
//     defer runtime.Install(runtime.Studio(url, tok) /* or runtime.Local(srv) */,
//         runtime.Agents(support), runtime.Models(map[string]weft.Model{"glm": m}),
//         runtime.Limits(runtime.Budget{MaxTokensPerExperiment: 200_000}),
//         runtime.AllowSideEffects("send_email"), runtime.Threads(store))()
//     // WEFT_ENV=dev (or runtime.Enabled(true)) opens the link; with no Studio option
//     // and no otel Studio destination it dials the discovery file's Studio (block 6's
//     // rule: WEFT_STUDIO_URL wins, WEFT_DISCOVERY=off, stale ignored); commands ack
//     // before they run (at-most-once), a never-class tool's call is substituted
//     // with its recorded result or parked (weft.Replay(weft.ReplaySafe) vouches a
//     // read: it runs in every mode); side_effects substitute (default) | park |
//     // allow — only allow runs the AllowSideEffects tools for real, and is refused
//     // unless every tool left on is opted in or ReplaySafe; budgets cap each
//     // experiment; runs carry weft.playground and never touch weft.session.id
//     // (ephemeral). Engines live | scripted (the source
//     // run's recorded turns, zero tokens); thread ephemeral | fork (a new session
//     // with lineage, the panel keeps chatting in it). Breakpoints and steer act
//     // on the runs this runtime starts only (D7). Studio side:
//     // studio.New(..., studio.Playground(true)): /api/playground/runs (the §5.1
//     // command, transcript_edits validated on both sides), /api/playground/
//     // commands/{id}, /api/runs/{id}/approvals (a parked run's own verbs),
//     // /api/playground/fixtures (wefttest replay fixtures from a run's records),
//     // /api/experiments (the saved groups + the runs they label), /api/runtimes/
//     // {id}/breakpoints, /api/runs/{id}/steer; the panel drawer and /playground
//     // (the Studio UI) render them, gated on capabilities.

// 8. Sessions (package weft/thread) — the map; godoc is the reference, docs/thread-operations.md the
//    operator's page; pre-1.0, API and format not frozen. st: jsonl.Open(dir) | sqlite.Open(path) (own
//    package) | thread.Memory(); options thread.Salvage(), FsyncOnFlush(), NoLock(), OpenLogger(l); the live
//    tail is st.(thread.Watcher).Watch(ctx, id, afterEntryID). s, _ := thread.Create(ctx, st, agent, opts...)
//    | thread.Open(ctx, st, id, agent); defer s.Close(ctx); thread.List(ctx, st, thread.Query{…}); thread.Delete.
//    Turns: turn, _ := s.Send(ctx, weft.User("…")[, thread.As(p), thread.RunOptions(…)]) — prompt durable
//    before the run; turn.Wait() | WaitContext(ctx) | Done() | Events() | Outcome() | Next(); s.WaitIdle(ctx).
//    Runs carry weft.session.id, weft.turn and, with thread.PublicID(id) (Create only), weft.public_id.
//    Busy (ADR 0019): thread.BusyPolicy(Queue | Reject → ErrBusy | Steer | Interrupt | Rollback), or
//    thread.As(p) per Send; queued sends and steers are receipt entries, durable at acceptance —
//    s.Queue(), s.ClearQueue(ctx) (their Turns end ErrDropped), s.Continue(ctx) runs what Open restored.
//    Tree: s.Context(), s.Entries(), s.Path(id), s.Branch(ctx, id[, thread.SummarizeLeft()]), s.Fork(ctx,
//    id), s.Label, s.SetInfo (rejects "weft." keys), s.Custom, s.CustomMessage, s.Pin.
//    Compaction (ADR 0020): thread.ContextWindow(n) arms it; s.Compact(ctx[, thread.SummaryInstructions(
//    "…")]), s.PreviewCompaction, s.ApplyCompaction, s.Uncompact; thread.NoAutoCompact(), SummaryModel,
//    ClearOldToolResults(n), BeforeCompact (thread.Proceed() | Cancel() | Replace(c)); overflow re-runs once.
//    Each compaction emits a session marker record (kind compaction, informational, no messages) at
//    ApplyCompaction, under the last run of this session that produced the context (held for the next run
//    when none did); obsdb.DB.Compactions and the run page read it back.
//    Approvals (ADR 0021): s.Pending(); s.Decide(ctx, thread.Approve(id) | Deny | Resolve | ResolveError |
//    ApproveAlways) → auto-resume (turn.Next()) or s.Resume(ctx); s.Grant, s.Revoke, thread.WithApprover(a,
//    timeout), Quorum(n), RequestExpiry(d), OnRequest(fn), s.Audit(). Signed: NewKeyring + WithKeyring (+
//    RequireSigned(), durable in the header); s.Request(callID) → ring.Sign(r, d) | key.Sign → s.DecideSigned.
//    Pool (ADR 0022, thread/pool): p := pool.New(max), the bound per Pool value; p.MustWrap(name, desc,
//    agent[, pool.Async()]) (p.Wrap returns (tool, error)); p.Submit(ctx, parent, agent, prompt);
//    p.Decide(ctx, parent, ds...) error records and arms, p.Wait(ctx, parent, id) follows; p.Cancel,
//    p.Forward, p.Recover(ctx, parent) after a restart, p.Close(ctx); pool.Receipts, Children, Descendants.
//    Errors: retry ErrBusy, ErrLocked · reopen ErrStale, ErrClosed · terminal ErrCorrupt, ErrNewerFormat.

// 9. Observability pipeline (package weft/otel; several destinations at once,
//    each with its own content policy; defer on exit):
//    defer otel.Install(
//        otel.Local("weft.db"),                       // local sink: replay-grade, content on
//        otel.Studio("https://studio.example", token), // OTLP/HTTP, content on
//        otel.Datadog(),                               // the Agent's OTLP intake, content off
//        otel.Exporters(spanExporter, logExporter),    // your own exporters (either may be nil)
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
by `wefttest/conformance` fixtures, not by replay; `make fuzz` runs the five
fuzz targets (a new message part or event type adds a seed; a crasher becomes
a committed seed).
Provider adapters (`weft/openai`, `weft/anthropic`, `weft/google` —
packages of the framework module over the official vendor SDKs) pass the shared executable contract
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
    Exception: a `thread/pool` child is a session, not a tool-call
    child — its usage is on its receipt and in the parent session's
    `Usage.Delegated`, never on the parent run's `RunResult.Usage` or
    `StepRecord.SubagentUsage`, so a budget that must cover delegated
    work reads `Delegated`.

### Thread rules (package `weft/thread`; its tests pin them)

- **T1. One writer per Session.** A `Session` takes its session's
  writer lease with its first write (`Create` and `Fork` are one) and
  holds it until `Close`. Another Session's write fails with
  `ErrLocked` and changes nothing; one whose view fell behind fails
  with `ErrStale` — open the session again. Never write a session
  behind its Session (`st.Append` on a session a Session holds).
- **T2. Close what you opened to write.** `s.Close(ctx)` stops new
  work, drains the running turn and the queue, seals the Session
  (`ErrClosed`) and gives the lease up. A second Session opened while
  the writer is still open reads fine and fails its first write with
  `ErrLocked`.
- **T3. `Open` is read-only.** It writes no entry, takes no lock and
  starts no run; what a stopped writer left queued is restored to
  `s.Queue()` and runs with the next `Send` or `s.Continue(ctx)`.
- **T4. Accepted input is durable input.** A `Send` that returns a
  `Turn` has written and flushed its prompt or its receipt entry; a
  run's messages are appended as they join it, each step once; nothing
  is deleted or rewritten in place.
- **T5. Readers fail loudly.** An unknown entry kind or a newer `"v"`
  is `ErrNewerFormat` — never skipped, `thread.Salvage()` or not. A
  damaged line is `ErrCorrupt`; under `Salvage` it is skipped and the
  skip reported (`s.LoadReport()`), as a dropped torn tail always is.
  The format is documented in ADR 0011's format reference; it is not
  frozen before 1.0.

## Working in this repo

- Run `make test` (`go test -race ./...`), `make vet`, `make lint`
  before claiming done. Add a test in `contract_test.go` for any promise
  you add or change; add a godoc example for any public feature.
- Keep the public surface small: functional options, sealed interfaces
  (`Option`, `Event`, `Part`, `ModelEvent`), no config structs, no
  globals, `context.Context` first in every signature.
- Prefer additive change. CI runs the apidiff gate (`make apidiff-all`,
  `scripts/apidiff.sh`) over both modules, each against its own last
  tag. `core` fails on incompatible changes; pre-1.0 a deliberate
  source-compatible widening is acknowledged line-by-line in
  `core/.apidiff-allow`, and renames always fail. The framework module
  holds the pre-freeze layers (`thread` is not frozen yet; `obsdb`,
  `otel`, `runtime` and `studio` follow ADR 0024's programme), so the
  gate reports its incompatible changes without failing — such a
  change makes the next tag a minor bump with a breaking CHANGELOG
  entry and, for `thread`, a line in the CHANGELOG's migration
  checklist saying what to write instead. A module added to `go.work`
  needs a policy in `scripts/apidiff.sh` or the gate fails.
- After changing core's exported API, `make generate` regenerates the
  facades; `TestFacadesAreComplete` (root) fails otherwise.
- Docs: `README.md` (usage), `docs/adr/` (why), this file (map),
  `docs/thread-operations.md` (running `weft/thread` in production).
  Update the one that applies in the same change.
