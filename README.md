# weft

[![CI](https://github.com/weftgo/weft/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/weftgo/weft/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/weftgo/weft.svg)](https://pkg.go.dev/github.com/weftgo/weft)
[![Version](https://img.shields.io/badge/version-v0.1.0-orange)](https://github.com/weftgo/weft/releases/tag/v0.1.0)

A thin, opinionated core for building AI agents in Go — designed the way
the standard library is: small interfaces, `context` everywhere, functional
options, wrapped errors, and zero required configuration.

Weft runs the agent loop — call a model, execute its tool calls in parallel
with defined failure semantics, stream typed events while work is in
flight — and nothing else. Concurrency is the point, not a feature: a
step's tools fan out over goroutines, parallelism is a one-line dial, and
tool failures never cancel their siblings.

> **Status:** v0.1.0 — experimental, pre-1.0. The three load-bearing
> contracts — message model, error model, tool contract — are implemented
> and tested, and the first provider adapters (OpenAI + compatible
> servers, Anthropic, Google) wrap the vendors' official Go SDKs; see
> `docs/adr/`. Middleware seams, MCP interop, and the surrounding
> modules (serving, ops, devtools, cli) come next, in that order of
> demand.

## Quick start

A tool is a plain function — the JSON Schema is reflected from the input
struct. Offline, the model comes from `wefttest`: a scripted,
deterministic stand-in for a real provider (an adapter slots into the
same `Model` seam):

```go
model := wefttest.Script(
    wefttest.ToolCalls(wefttest.Call{Name: "echo", Args: `{"msg":"hello"}`}),
    wefttest.Say("HELLO"),
)
agt := weft.New(
    model,                                   // or openai.Model("gpt-4o-mini") — see Providers
    weft.Instructions("You are a support agent."),
    echo,
)

res, err := agt.Generate(ctx, weft.Prompt("Echo hello."))
fmt.Println(res.Text(), res.Usage.Total())
```

where `echo` is defined once and reused:

```go
echo := weft.Tool("echo", "Echo a message back, uppercased.",
    func(ctx context.Context, in struct {
        Msg string `json:"msg" jsonschema:"the message to echo"`
    }) (string, error) {
        return strings.ToUpper(in.Msg), nil
    })
```

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
anthropic.Model("claude-sonnet-4-5", anthropic.Thinking(true))
google.Model("gemini-2.5-flash")
```

Every adapter passes the same executable contract
(`wefttest/conformance`): streaming tool-call fragments are assembled
into whole calls, provider errors pass through unchanged for
`errors.As`, cancellation surfaces as `ctx.Err()`, a stalled stream
fails with `ErrStreamIdle` while a slow-but-streaming one never does,
and `WEFT_MODEL_REQUESTS=deny` refuses every call before any network
I/O — test suites that must stay offline get loud failures, not
surprise bills. ([ADR 0013](docs/adr/0013-adapter-contract.md))

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
  decide what truncated text means), and oversized tool results are
  capped (64 KiB by default, `weft.MaxResultBytes(n)` to change, `0` to
  disable) with a marker the model sees.
- **The Model stream contract is enforced**: a stream that ends without
  `ModelFinish`, continues after it, carries a tool call with an empty
  ID or name, or panics fails the run wrapping `ErrModelContract` — a
  broken adapter cannot corrupt a transcript.
- **No dependencies** in the core module. The vendor SDKs live in the
  adapter modules (their own `go.mod`); the one dependency the design
  permits in the core is the OTel API package (a no-op tracer until an
  SDK registers), which lands with instrumentation; exporters stay in a
  satellite.

## Layout

```
doc.go, message.go    message model (roles, parts, versioned JSON)
errors.go, env.go     error model (sentinels, RunError, kill switch)
tool.go, schema.go    tool contract + schema reflection
model.go              provider seam (streaming-first Model interface)
events.go             sealed run-event set
agent.go, run.go      agent construction options, run/stream/result
loop.go               the loop: model call → tool fan-out → repeat
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
make fmt
```

Requires Go 1.26 or newer; the current and previous Go releases are
supported and both are tested in CI.

## Roadmap

1. ~~First provider adapters~~ — **done** (OpenAI + compatible servers,
   Anthropic, Google; ADR 0013).
2. The two middleware seams (model call + tool call, chi-style).
3. MCP interop: consume MCP servers as tools, expose weft tools as MCP.
4. Subagents as tools; execution-policy refinements.
5. The satellites: `runtime` (sessions, approvals), `store`, `serve`,
   `studio`, and the eval/prompt/mem/trace modules.

## License

MIT — see [LICENSE](LICENSE).
