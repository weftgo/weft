# weft

[![CI](https://github.com/weftgo/weft/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/weftgo/weft/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/weftgo/weft.svg)](https://pkg.go.dev/github.com/weftgo/weft)
[![Version](https://img.shields.io/badge/version-v0.2.0-orange)](https://github.com/weftgo/weft/releases/tag/v0.2.0)

A thin, opinionated core for building AI agents in Go — designed the way
the standard library is: small interfaces, `context` everywhere, functional
options, wrapped errors, and zero required configuration.

Weft runs the agent loop — call a model, execute its tool calls in parallel
with defined failure semantics, stream typed events while work is in
flight — and nothing else. Concurrency is the point, not a feature: a
step's tools fan out over goroutines, parallelism is a one-line dial, and
tool failures never cancel their siblings.

> **Status:** v0.2.0 — experimental, pre-1.0. The three load-bearing
> contracts — message model, error model, tool contract — are implemented
> and tested; the provider adapters (OpenAI + compatible servers,
> Anthropic, Google) wrap the vendors' official Go SDKs; the two
> middleware seams (`WrapModel`/`WrapTools`, package `mw`), the approval
> boundary, subagents as tools, MCP interop both ways (`weft/mcp`),
> observability (OTel spans, slog lines), and wefttest record/replay
> are in; see `docs/adr/` and the roadmap below.
> The surrounding modules (serving, ops, devtools, cli) come next, in
> that order of demand.

## Quick start

A tool is a plain function — the JSON Schema is reflected from the input
struct, so the struct is both the contract and the documentation:

```go
type EchoInput struct {
    Msg string `json:"msg" jsonschema:"the message to echo"`
}

echo := weft.Tool("echo", "Echo a message back, uppercased.",
    func(ctx context.Context, in EchoInput) (string, error) {
        return strings.ToUpper(in.Msg), nil
    })
```

An agent is a value: build it once, run it many times, concurrently.
Offline, the model comes from `wefttest`: a scripted, deterministic
stand-in for a real provider (an adapter slots into the same `Model`
seam):

```go
model := wefttest.Script(
    wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hello"}`}),
    wefttest.Say("HELLO"),
)
agt := weft.New(
    model,                                   // or anthropic.Model("claude-sonnet-5") — see Providers
    weft.Instructions("You are a support agent."),
    echo,
)

res, err := agt.Generate(ctx, weft.Prompt("Echo hello."))
fmt.Println(res.Text(), res.Usage.Total())
```

For a one-off tool the input struct can be written inline in the
handler signature; a named type reads better and is reusable across
tools and tests.

Streaming is Go iteration — typed events, cancel via context, exactly one
terminal error:

```go
for ev, err := range agt.Stream(ctx, weft.Prompt("Echo hello.")).Events() {
    if err != nil {
        return err
    }
    switch ev := ev.(type) {
    case weft.TextDelta:
        io.WriteString(w, ev.Text)
    case weft.ToolArgsDelta: // progress: the model is still writing the call
    case weft.ToolStart:
        slog.Info("tool", "name", ev.Name, "seq", ev.Seq)
    case weft.RunFinish:
        slog.Info("done", "steps", ev.Steps, "tokens", ev.Usage.Total())
    }
}
```

Ending a run is a predicate, and the step budget is a separate safety net:

```go
agt := weft.New(model,
    weft.StopWhen(weft.HasToolCall("submit_answer")), // intended end
    weft.MaxSteps(20),                                // runaway guard → ErrMaxSteps
    submit, search,
)
```

### Structured output

`Output[T]` constrains the final answer to a struct: a `submit_output`
tool with `T`'s reflected schema is advertised, and the run ends when
the model calls it with arguments that decode. An invalid submission is
an ordinary tool error the model repairs. `GenerateAs` returns the
value; `OutputOf` reads it from a streamed run's result.

```go
type Verdict struct {
    Approved bool   `json:"approved"`
    Reason   string `json:"reason" jsonschema:"one sentence"`
}

agt := weft.New(model, weft.Output[Verdict](), lookup)
v, res, err := weft.GenerateAs[Verdict](ctx, agt, weft.Prompt("Review order 42."))
```

It is tool mode, so it works on every provider; a run that ends in text
returns `ErrNoOutput` with the transcript attached.

### Per-tool policy

Trailing options on `Tool` set policy for that tool alone. The same
names on `New` set the agent-wide default:

```go
run := weft.Tool("run_command", "Run a shell command.", runCommand,
    weft.Timeout(30*time.Second),   // deadline on ctx; expiry is an error result
    weft.MaxResultBytes(0),         // this tool's output arrives whole
    weft.StrictInput(),             // undeclared argument fields are rejected
)
agt := weft.New(model, weft.Timeout(10*time.Second), run, grep)
```

Bad arguments come back to the model in the schema's own words —
`field "days": expected integer, got string` — so it can map the error
to the schema it was shown. Undeclared fields are ignored by default;
`StrictInput` rejects them by name.

### The two seams

Behaviour attaches at two chi-style seams; observation at `weft.Tap`.
`WrapModel` wraps the model, `WrapTools` wraps every tool call (on the
agent, or on one tool). First listed is outermost. Package `mw` holds
the reference set:

```go
agt := weft.New(model,
    weft.WrapModel(
        mw.Log(logger),               // request summary + finish, Debug level
        mw.Retry(mw.MaxRetries(3)),   // 429/5xx/net errors; retry-after honoured; backoff with jitter
        mw.Fallback(backupModel),     // another model when this one fails before yielding
        mw.RepairJSON(),              // close a truncated tool-call argument object once
    ),
    weft.WrapTools(
        mw.Audit(logger),             // every call: run, step, tool, duration, error + cause
        mw.Allow(policy.Permits),     // DENIED: tool "rm" is not allowed — the model sees it
        mw.MapErrors(nil),            // plain errors → INTERNAL: tool "x" failed (cause kept)
    ),
    tools...,
)
```

Middleware that verifies something puts it on ctx before `next`, and
the handler reads it back through a typed accessor — the same shape as
`weft.CallFromContext` (see `ExampleWrapTools_context`). A middleware
panic is a tool error result, never a run error. Handlers give the
model a code to branch on with `*weft.ToolError` (`ORDER_NOT_FOUND:
order 42 does not exist`; the cause stays in logs); the loop's own
failures are coded `INVALID_INPUT` and `NO_SUCH_TOOL`.
[docs/life-of-a-call.md](docs/life-of-a-call.md) shows where each
thing sits; [ADR 0006](docs/adr/0006-seams.md) is the decision.

### Delegating to another agent

A subagent is a tool whose handler runs another agent — the
orchestrator-worker pattern with zero new machinery. The child sees
only the prompt; its events arrive in the parent's stream wrapped in
`weft.Nested` (Seq from the parent's counter, so the stream stays
replayable); its usage rolls into `res.Usage` and is recorded per call
on `StepRecord.SubagentUsage`:

```go
researcher := weft.New(model, weft.Tool("deep_search", "…", DeepSearch))
orchestrator := weft.New(model,
    weft.Subagent("research", "Research a question in depth.", researcher,
        weft.Timeout(2*time.Minute)),
)
```

Everything the tool contract offers applies: `Timeout` bounds the child
run, `Parallelism` bounds concurrent delegations (four research calls in
one step run four children), `RequireApproval` gates the delegation
itself. A failed child is data the parent model sees
(`SUBAGENT_FAILED: …`, the child's `*RunError` on `ToolError.Err`), a
child that ends awaiting approval is `SUBAGENT_PENDING` — approval-gated
tools belong in the orchestrator, not in a child — and a delegation to
an agent already running in the call chain is refused
(`SUBAGENT_CYCLE`). [ADR 0014](docs/adr/0014-subagents-as-tools.md)
records the mechanics, the lineage ids, and pi's AgentLanes
counterpoint.

### Composing agents

A plugin is `func(deps) weft.Option` — a family of tools and its
policy closed over its dependencies as one value, composed with
`weft.Options` (and `weft.ToolOptions` for the per-tool counterpart).
Nothing registers itself, so there is no registry, no scopes, no
dedup; dependencies are parameters, never globals, and the
"registered twice" mistake panics at `New` as always:

```go
func Orders(svc *OrderService) weft.Option {
    return weft.Options(
        weft.Instructions("You handle orders."),
        svc.Lookup(), svc.Refund(),
        weft.WrapTools(mw.Allow(svc.Permitted)),
    )
}
agt := weft.New(model, base, Orders(orders))
```

### Approval

`weft.RequireApproval()` on a tool parks its calls: the run ends
successfully with them on `RunResult.Pending`, the step's other tools
having run. Resume with the transcript and a decision — over any
transport, with no persistence required:

```go
res, _ := agt.Generate(ctx, weft.Prompt("Refund order 42"))
for _, call := range res.Pending { /* ask someone */ }
res, _ = agt.Generate(ctx, weft.Messages(res.Messages...),
    weft.Approve(call.ID), weft.Deny(other.ID, "over the limit"))
```

Approved calls run (handlers see `Call.Approved`); denied ones become
`DENIED: <reason>` results the model sees; undecided ones `DENIED: no
decision`. Middleware can park any call by returning an error wrapping
`ErrApprovalRequired`. It is a policy seam, not a security boundary
([ADR 0007](docs/adr/0007-approval-boundary.md); `examples/approval`).

### Observability

Set up an OpenTelemetry SDK and every run emits the full span tree —
one `invoke_agent` span per run, one `chat` span per model call, one
`execute_tool` span per executed tool call, a subagent's run nested
under its delegating tool span — with the GenAI semantic attributes
(provider, model, tokens, finish reasons). No weft option is needed:
the core instruments through the OTel API, which is a no-op until your
SDK registers. Exporters and backends are not weft's business.

```go
tp := sdktrace.NewTracerProvider( /* your exporter */ )
defer tp.Shutdown(ctx)
// no weft option: the global provider is picked up, spans appear
agt := weft.New(model, tools...)
// or explicitly, without touching the global:
agt = weft.New(model, append(tools, weft.TracerProvider(tp))...)
```

For logs, `weft.Logger(l)` writes one Debug line per phase — run start,
run finish, model call, tool call — with ids, the model, durations,
usage, and outcomes; never message text or tool arguments. The default
(`slog.Default`, resolved at log time) is silent until your handler
enables Debug; `slog.New(slog.DiscardHandler)` turns the lines off. A
handler that bridges slog to OTel correlates the lines with the spans
for free: they are logged on the span-carrying context.
`examples/otel` runs the whole thing against the real SDK offline.
No prompt, message, tool argument or tool result reaches a span: ids,
names, counts, durations, reasons, and error types only. Error *text*
does travel — a failed run or model call records its error as the
span's exception, and the log lines carry the error text a tool or
model returned, because a log is the caller's
([ADR 0016](docs/adr/0016-observability.md)).

## The manifest — `weft.json`

One generated, committed, diffable description of every agent and tool
(the code stays the only source of truth; the file is output, never
input). Gate it with a golden test so it cannot go stale:

```go
func TestManifest(t *testing.T) {
    b, err := weft.Manifest(newAgent())
    if err != nil {
        t.Fatal(err)
    }
    wefttest.Golden(t, "weft.json", b) // regenerate: go test ./... -update
}
```

A tool or policy change without regenerating fails `go test`; the diff
is the review artifact. ([ADR 0012](docs/adr/0012-manifest-format.md))

## MCP: both ways

`weft/mcp` (its own module over the official Go MCP SDK, aliased
`sdk`) is the bridge in both directions, with no adapter layer — the
tool contract is the same shape ([ADR 0015](docs/adr/0015-mcp-interop.md)):

```go
import (
    sdk "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/weftgo/weft/mcp"
)

// Consume: a server's tools as ordinary weft tools. The schema bytes
// cross whole (an enum or oneOf reaches the model as sent), the
// calls forward the model's arguments verbatim, and every remote
// failure is a tool result the model sees — data, never a run error.
tools, _ := mcp.Tools(ctx, sess, mcp.Prefix("gh_"), mcp.Policy(weft.Timeout(10*time.Second)))

// Expose: weft tools — or a whole agent, as one named tool with your
// description — on any MCP server.
srv := sdk.NewServer(&sdk.Implementation{Name: "weft", Version: "0"}, nil)
mcp.AddTools(srv, lookup)
mcp.Serve(srv, agent, "Support agent.")   // agent + its tools, under its chain
```

Two warnings the godoc repeats. **A server's tool descriptions are
untrusted content** — they land in your model's tool list, a surface
you did not author; filter with `mw.Allow` or read `Tools()`' output
before registering it. **A foreign tool runs sequentially unless its
server marks it `readOnlyHint`** — the conservative reading of an
untrusted hint for a tool whose handler you cannot read.

The loop, the seams, the manifest and `wefttest` treat an imported
tool like any other (it is a `RawTool`); `examples/` for both
directions live in `mcp/examples/` and run offline over in-memory
transports. `TestRoundTripIsLossless` pins the property: export →
import keeps the schema and the answers identical.

## Providers

First-party adapters wrap the vendors' official Go SDKs — weft never
owns an HTTP client — and are versioned as their own modules. One line
per vendor, one adapter for the whole OpenAI-compatible long tail:

```go
import (
    "github.com/weftgo/weft/anthropic"
    "github.com/weftgo/weft/google"
    "github.com/weftgo/weft/openai"
)

openai.Model("gpt-4o-mini")                          // or any compatible server via openai.BaseURL
anthropic.Model("claude-sonnet-5", anthropic.Thinking(true))
google.Model("gemini-2.5-flash")
```

Reasoning depth is per run: `weft.Thinking(weft.ThinkingConfig{Level:
weft.ThinkHigh})` — an agent option sets every run's default, a run
option overrides it for one — maps to whatever the provider expresses
(`reasoning_effort`, `budget_tokens`, `thinkingBudget`); adapters
document what they drop. The openai adapter picks the thinking wire
form from the base URL; `openai.Dialect` pins it when detection can't.

Every adapter passes the same executable contract
(`wefttest/conformance`): streaming tool-call fragments are assembled
into whole calls and — where the provider streams fragments at all
(`Caps.ToolArgDeltas`; Google's calls arrive whole) — also surface live
as `ToolArgsDelta` progress, provider errors pass through unchanged
for `errors.As`, cancellation surfaces as `ctx.Err()`, a stalled
stream fails with `ErrStreamIdle` while a slow-but-streaming one never
does (both are pinned cases), and `WEFT_MODEL_REQUESTS=deny` refuses
every self-built client's call before any network I/O — test suites
that must stay offline get loud failures, not surprise bills.
([ADR 0013](docs/adr/0013-adapter-contract.md))

## The rules that matter

- **Tool error = data; run error = Go error.** A failing (or panicking)
  tool becomes a result the model sees; siblings keep running. Only model
  failures, cancellation, and step exhaustion reach the caller, as
  `*RunError` with the partial transcript attached. ([ADR 0002](docs/adr/0002-error-model.md))
- **Messages are role + typed parts**, with a versioned JSON contract:
  every part carries a `type` discriminator and transcripts round-trip
  through `encoding/json`. ([ADR 0001](docs/adr/0001-message-model.md))
- **Tools are generic functions** whose schema derives from struct tags,
  shape-compatible with the official Go MCP SDK. ([ADR 0003](docs/adr/0003-tool-contract.md))
- **Concurrent tool events carry a total order** (`Seq`), assigned and
  emitted atomically, so streams replay exactly. ([ADR 0004](docs/adr/0004-event-ordering.md))
- **Parallel by default, bounded (4)**; tools always *start* in call
  order; `weft.Sequential()` runs them one at a time for shared state;
  `weft.Parallelism(n)` for anything else.
- **String tool outputs are sent verbatim**, everything else as JSON.
- **Every run has an id** (`RunStart`, `Run.ID()`, `RunResult.ID`); every
  tool call can learn its own via `weft.CallFromContext(ctx)`.
- **Truncation is visible, never silent**: a `max_tokens` finish is
  recorded on `RunResult.StopReason` (the run still succeeds — callers
  decide what truncated text means); a `max_tokens` step *with* tool
  calls executes none of them — each gets a visible failure and the
  model retries with a full budget; and oversized tool results are
  capped (64 KiB by default, `weft.MaxResultBytes(n)` to change, `0` to
  disable, per tool or per agent) with a marker the model sees.
- **Two behavioural seams, one observation tap.** `WrapModel` and
  `WrapTools` change; `Tap` sees. A third seam needs an ADR.
  ([ADR 0006](docs/adr/0006-seams.md))
- **A hung tool never hangs the run**: `weft.Timeout(d)` on a tool or
  agent turns an overdue call into an error result and moves on.
- **The Model stream contract is enforced**: a stream that ends without
  `ModelFinish`, continues after it, carries a tool call with an empty
  ID or name, or panics fails the run wrapping `ErrModelContract` — a
  broken adapter cannot corrupt a transcript.
- **One dependency** in the core module: the OTel API package (ADR
  0016), a no-op until an SDK registers — the zero-config
  instrumentation THE-END-GOAL sanctions as the core's single
  exception. The vendor SDKs live in the adapter modules (their own
  `go.mod`); the OTel SDK itself lives only in `examples/otel`; exporters
  stay in a satellite.

## Layout

```
doc.go, message.go    message model (roles, parts, versioned JSON)
errors.go, env.go     error model (sentinels, RunError, kill switch)
tool.go, schema.go    tool contract, per-tool policy, schema reflection
output.go             structured output (Output, GenerateAs, OutputOf)
model.go              provider seam (streaming-first Model interface)
events.go             sealed run-event set
agent.go, run.go      agent construction options, run/stream/result
loop.go               the loop: model call → tool chain fan-out → repeat; approval resume
mw/                   reference middleware: Retry, Fallback, Log, RepairJSON, Allow, Audit, MapErrors
wefttest/             scripted mock model + the conformance suite
openai/               OpenAI Chat Completions (+ compatible servers)
anthropic/            Anthropic Messages (thinking, signatures)
google/               Gemini via genai
examples/             runnable core example (per-adapter: <adapter>/example)
docs/adr/             decision records for the contracts
```

## Development

```sh
make test   # go test -race ./... in every workspace module
make vet
make lint   # golangci-lint (CI uses .golangci.yml)
make live   # adapter conformance against real keys (-tags live)
make apidiff  # public API of the root module vs the last tag (CI runs it)
make fuzz    # 10 s per fuzz target; FUZZTIME=1m make fuzz for longer
make fmt
```

Requires Go 1.26 or newer; the current and previous Go releases are
supported and both are tested in CI. A fuzz crasher fails CI, its
input is uploaded, and the fix PR commits it under `testdata/fuzz/` as
a regression seed — the existing `FuzzRepair` seed got there that way.

### Testing

In reach order: script the dialogue with
`wefttest.Script(wefttest.ToolCalls(...), wefttest.Say(...))` —
offline, deterministic, no key; compare bytes with `wefttest.Golden`;
and when the question is "what does my agent do with what the model
*actually* said", record once and replay forever:

```go
func model(t *testing.T) weft.Model {
    if os.Getenv("WEFT_RECORD") != "" { // the suite's own switch; wefttest never reads it
        return wefttest.Record(t, "testdata/replay", openai.New(key, "gpt-5"))
    }
    return wefttest.Replay(t, "testdata/replay")
}
```

Fixtures are pretty JSON a reviewer reads in a diff — re-recording is
the review (ADR 0017). The adapters' own wire-format parsing is proven
by `wefttest/conformance` against recorded `.sse` fixtures, not by
replay (ADR 0013).

**API stability is enforced, not aspired to.** CI runs
`scripts/apidiff.sh`: the root module's exported API is compared
against the last `v*` tag and any incompatible change fails the build.
Pre-1.0, a deliberate source-compatible evolution (widening a return
type to a superset interface, adding a trailing variadic) can be
acknowledged by adding apidiff's exact line to `.apidiff-allow` with a
justification; the file is emptied at each tag. Renaming or removing
an exported symbol always fails.

## Roadmap

1. ~~First provider adapters~~ — **done** (OpenAI + compatible servers,
   Anthropic, Google; ADR 0013).
2. ~~The two middleware seams~~ — **done** (`WrapModel`/`WrapTools`,
   package `mw`, the approval boundary; ADR 0006, ADR 0007).
3. ~~Loop refinements~~ — **done** (subagents as tools, `ModelRetry`,
   usage limits, loop detection, `PrepareStep`; ADR 0014).
4. ~~MCP interop~~ — **done** (consume and expose; ADR 0015). ~~Core
   observability~~ — **done** (OTel spans + slog lines; ADR 0016).
5. The satellites: `runtime` (sessions, approvals), `store`, `serve`,
   `studio`, and the eval/prompt/mem/trace modules.

## License

MIT — see [LICENSE](LICENSE).
