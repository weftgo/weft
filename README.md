# weft

[![CI](https://github.com/weftgo/weft/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/weftgo/weft/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/weftgo/weft.svg)](https://pkg.go.dev/github.com/weftgo/weft)
[![Version](https://img.shields.io/badge/version-v0.12.0-orange)](https://github.com/weftgo/weft/releases/tag/v0.12.0)

A modular framework for building AI agents in Go — designed the way the
standard library is: small interfaces, `context` everywhere, functional
options, wrapped errors, and zero required configuration. One `go get`
is the whole framework; one import path is the loop alone:

```sh
go get github.com/weftgo/weft@v0.12.0       # the framework: every package below, one version
go get github.com/weftgo/weft/core@v0.12.0  # the loop alone: its only dependency is the OTel API
```

| Package | What it gives you |
|---|---|
| `weft` | The agent loop: tools from plain Go functions, parallel tool calls with defined failure semantics, typed streaming events, structured output, approvals, steering, subagents; OpenAI, Anthropic, Google adapters (`weft/openai`, …), MCP both ways (`weft/mcp`), reference middleware (`weft/mw`) and the offline test double (`weft/wefttest`) |
| `weft/core` | The same loop as a module of its own, for a service that wants nothing else: `core.New`, `core.Tool`, `core/wefttest`, `core/mw`. `weft` re-exports it name for name, so `weft.Agent` *is* `core.Agent` |
| `weft/thread` | Durable sessions: an append-only conversation tree with branching, compaction, approvals that survive restarts, steering and a bounded pool of child agents (jsonl, SQLite or memory storage) |
| `weft/otel` | Recording in one line (`defer otel.Install()()`): every event, transcript and span exported over OpenTelemetry — to a local database, Studio, or any OTLP backend — with per-destination content policy and redaction |
| `weft/obsdb` | The queryable store those records land in: SQLite locally, ClickHouse hosted (`weft/obsdb/clickhouse`) |
| `weft/studio` | The Inspector: runs, sessions, traces and live streams in a web UI, an in-app devtools panel for your own pages, and a playground that re-runs a turn with an edited prompt, model or tools; the `weft` binary (`weft/cmd/weft`: `weft studio`, `weft dev`) serves it for apps in any language |
| `weft/scope` | The devtools' scope header: `scope.Header(handler, …)` sets `Weft-Scope` on your own chat endpoint so the in-page panel follows the conversation and run a response belongs to |
| `weft/runtime` | The playground's in-app side: your app executes experiment commands safely — side-effect tools are substituted or parked unless you opt them in |

Concurrency is the point, not a feature: a step's tools fan out over
goroutines, parallelism is a one-line dial, and tool failures never cancel
their siblings.

> **Status:** v0.12.0 — experimental, pre-1.0, released as one module
> (plus `core`; see [Releases](https://github.com/weftgo/weft/releases),
> `CHANGELOG.md` and, coming from 0.8, [`MIGRATION-0.9.md`](MIGRATION-0.9.md)). The core's three load-bearing contracts — message
> model, error model, tool contract — are implemented and tested, and
> every layer has been through a production-readiness review. Serving,
> eval and the `weft` CLI come next — see the roadmap below.

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
        mw.Fallback(backupModel),     // fail over once Retry has given up on the primary
        mw.Retry(mw.MaxRetries(3)),   // 429/5xx/net errors; retry-after honoured; backoff with jitter
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

#### Seams are the product

The governance features other frameworks ship as processors — PII
scrubbing, prompt-injection heuristics, moderation, token limits,
response caching — need no processor layer here; each is a closure at
one of the two seams, and each carries its own dependencies and policy
stances rather than importing yours. `mw` stays a reference set; the
patterns live as tested examples:

- **PII scrub** — a tool middleware that masks what results carry
  ([`Example_piiScrubMiddleware`](https://pkg.go.dev/github.com/weftgo/weft/mw#example-package-PiiScrubMiddleware)).
- **Allowlist** — shipped as `mw.Allow(permits)`.
- **Token limiter** — a model middleware that refuses the call before
  the provider bills it
  ([`Example_tokenLimitMiddleware`](https://pkg.go.dev/github.com/weftgo/weft/mw#example-package-TokenLimitMiddleware));
  `weft.UsageLimit` covers the measured side.
- **Response cache** — a model middleware keyed on the `ModelRequest`
  ([`Example_responseCacheMiddleware`](https://pkg.go.dev/github.com/weftgo/weft/mw#example-package-ResponseCacheMiddleware));
  invalidation policy is the caller's.

A named `mw` package ships only when a pattern needs a dependency or a
policy stance weft should own — the `mw.RateLimit` precedent (rate
limiting stays an example because `golang.org/x/time` would be the
module's first dependency). Revisit when a consumer asks for one by
name.

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

### Recording runs

The pipeline is the recorder ([ADR 0024](docs/adr/0024-observability-data.md)):
`defer otel.Install()()` writes the local sink (`./.weft/weft.db`,
content on, no network) and every event, delta, transcript record and
span leaves as it happens — a crash loses nothing emitted. Caller
pairs ride `weft.Metadata`; a quiet run heartbeats, so a live run
reads `running` and a crashed one `interrupted`. The recorder's
database is package `weft/obsdb`: the OTLP-shaped model with the
derived weft identity, read back through one `obsdb.DB` interface —
runs, sessions, positioned event pages, transcripts, spans by run or
trace, public ids — with `obsdb/sqlite` as the default backend and
`obsdb/clickhouse` as the hosted one (column-compatible with the OTel
Collector's ClickHouse exporter, so a stock collector can feed the
same database).

```go
defer otel.Install()()                          // the recorder: local sink, content on
db := otel.LocalDB()                            // the same handle Studio reads [D4]
page, _ := db.Runs(ctx, obsdb.RunQuery{})       // identity chain, status derived
rec, _ := db.Run(ctx, page.Runs[0].ID)          // the row + its subagent children
evs, _ := db.Events(ctx, rec.ID, -1, 50)        // positioned events after the cursor; -1 = from the start
```

### Inspecting runs

Module `weft/studio` is the Inspector, rewritten on obsdb (ADR 0024
S4): the UI, a JSON API (runs with their filters, transcripts, spans,
traces, sessions, public ids), the OTLP/HTTP ingest receiver, and the
live SSE stream — served as one `http.Handler` with the UI embedded,
no build step, nothing leaves the process. Setup A embeds it beside
the app and passes the pipeline's handle for the live lane; a token
(`studio.Token`) walls the API when it leaves loopback. Setup B is the
`weft` binary's `weft studio` — package `weft/cmd/weft`, the one place
that imports the ClickHouse driver — for any language's OTel app: UI +
ingest + the playground + a dev token on `127.0.0.1:7331`,
`--db sqlite://path` or `clickhouse://user:pass@host:9000/db`
([studio/README.md](studio/README.md)). The in-page devtools panel
is also on npm as `@weftgo/devtools`, for bundled apps with no
`<script>` tag: `npm install @weftgo/devtools`, then
`import { mount } from "@weftgo/devtools"; mount({ enabled: import.meta.env.DEV })`.

```go
mux.Handle("/studio/", http.StripPrefix("/studio",
    studio.Handler(studio.DB(otel.LocalDB()))))   // history + live [D4]
```

```sh
go install github.com/weftgo/weft/cmd/weft@latest
weft studio --db sqlite://.weft/dev.db            # OTel app: point
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:7331 # its exporter here
```

#### The `weft` command

Every subcommand is a thin client of the Studio API or of `studio.New`
— a convenience over options the app can set itself, so a team that
skips the CLI loses nothing. Every connection flag mirrors an
environment variable one to one (the table below).

| Command | What it does |
|---|---|
| `weft studio [--addr] [--db] [--token] [--rotate-token] [--manifest] [--open] [--no-playground]` | Setup B: `studio.New` with the UI, OTLP ingest, the playground (inert until an app's `weft/runtime` connects; `--no-playground` turns it off) and a dev token stable per database. Reuses a Studio already serving the same database on 7331, else takes the next free port in 7331–7340; `--addr` pins. Writes the discovery file apps find it through (below). The manifest is `--manifest`, else the nearest `weft.json` upward (one line says which). `--open` (default on a terminal) opens the UI with the token in the URL fragment. |
| `weft dev [studio's flags] [--no-watch] [--watch dir] [-- go run ./cmd/app]` | Studio (as `weft studio`, in-process, same port policy) plus your app run beside it and restarted on a `.go` save. Prints one line per start: `studio http://127.0.0.1:7331/ · app pid 4242 · runtime rt_… registered`. See below. |
| `weft runs [--agent] [--since 2h\|RFC3339] [--failed] [--limit] [--json]` | One row per run (id, agent, status, started, steps) from `GET /api/runs`; `--limit` defaults to 50 (0 lists all) and says so on stderr when it hid runs; `--json` for scripts. |
| `weft open <run id> [--open] [--with-token]` | Prints the run's page, `<url>/runs/<id>`, bare — a fixed token may be the panel tokens' signing key, so it stays out of logs; `--with-token` prints the `#token=` fragment too; `--open` hands the browser the link with the token. |
| `weft export <run id> [--format json\|jsonl\|otlp]` | `GET /api/runs/<id>/export` to stdout. |
| `weft export <run id> --wefttest ./testdata [--test TestName] [--force]` | The run's wefttest replay fixtures unzipped into `./testdata/<TestName>/` (default: the run id), where `wefttest.Replay(t, "testdata")` reads them; a non-empty target needs `--force`, which replaces its `*.json` fixtures (never merges). |
| `weft doctor` | One line per check of a running Studio, each read from `GET /api/meta`. |
| `weft version` | The weft version this binary was built from. |

| Flag | Environment | Default |
|---|---|---|
| `--addr` | `WEFT_STUDIO_ADDR` | `127.0.0.1:7331` (unpinned) |
| `--db` | `WEFT_DB` | `./.weft/weft.db` |
| `--token` | `WEFT_STUDIO_TOKEN` | `weft studio`, `weft dev`: the database's stable token, `<db>.token` (never printed); a generated one, printed, for a database with no file |
| `--manifest` | `WEFT_MANIFEST` | the nearest `weft.json` upward |
| `--url` (runs, open, export, doctor) | `WEFT_STUDIO_URL` | `http://127.0.0.1:7331` |

`weft dev`'s `--watch` and `--no-watch` shape the dev loop only and
have no environment mirror; like `--open` and `--no-playground` they
are not connection settings. `--rotate-token` has none either: it is an
action, not a setting.

##### The dev token and the discovery file

The dev token is stable per database: the first start on a SQLite file
writes 32 random bytes (base64url) beside it — `<db>.token`, so
`./.weft/weft.db.token` by default, mode 0600 — and every later start
on that file serves the same token, so a restart keeps the browser
tab, the app's `WEFT_STUDIO_TOKEN` and a second start's reuse probe
valid. `--rotate-token` writes a new one (panel tokens signed with the
old one stop verifying); `--token` / `WEFT_STUDIO_TOKEN` override it
and leave the file alone. The stable token is the panel tokens'
signing key, like a fixed one, so it is never printed: the banner
names its file, `weft open --with-token` prints the fragment, `--open`
hands it to the browser. A database with no file (`:memory:`,
ClickHouse) gets a token generated for that process, printed as
before.

Once its listener is bound, `weft studio` (and `weft dev`) writes
`studio.json` — `{"url","token","db","pid","started","version"}`, mode
0600, the real port in it — and removes it on a clean exit. It goes to
the first of `./.weft/` (when that directory exists: the project's
own), `$XDG_RUNTIME_DIR/weft/` (Linux), `os.UserCacheDir()/weft/`
(macOS, Windows, bare containers). `otel.Install()` and
`runtime.Install()` read the same order when `WEFT_STUDIO_URL` is
unset, so an app with `defer otel.Install()()` exports to the running
Studio, and `runtime.Install` dials it, with no configuration — one
INFO line names the Studio joined. After a default start `./.weft`
always exists, so the file lands there: run the app from the directory
you ran `weft studio` in, or set `WEFT_STUDIO_URL`. The file is trusted
only when its url is a loopback address (127.0.0.0/8, `::1`,
`localhost`), it is fresh (its pid alive, under 24 hours old) and, on
unix, it is mode 0600 and owned by you — a `studio.json` that arrived
through a git checkout (0644) or names another host is never used.
Anything untrusted is skipped without a word above Debug and the next
writer removes it. The writer also drops a `.gitignore` (`*`) into
`./.weft` when it has none, so the database, its token and the file
stay out of commits. Two Studios never erase each other's file: the
second one's exit puts the first's back. A reusing start writes nothing
(the running Studio owns the file) and says so when that file is
missing; its port probe sends the stable token only to an address your
discovery file names (anything else on the port is probed bare). `weft
studio` stops gracefully on SIGHUP too, so a closed terminal removes
the file.

The file is a convenience, never a requirement: `WEFT_STUDIO_URL`
always wins (the file is not read), `WEFT_DISCOVERY=off` turns the read
off, `otel.NoEnv()` ignores it with the rest of the environment, and a
Studio named in code — `otel.Studio(url, tok)` (which switches the read
off: explicit > environment > file) or `runtime.Studio(url, tok)` —
needs no file.

Setup A's handler serves `GET <base>/panel-config.json` —
`{"endpoint", "version", "capabilities"}` — to a loopback (or
`AllowOrigins`) Host and a same-origin, `AllowOrigins` or loopback
Origin only (loopback by the Host's rule — `localhost`, `*.localhost`,
127.0.0.0/8, `[::1]` — even with `AllowOrigins` set), a 404 to anyone
else, so the devtools panel reads its endpoint instead of
inferring it from its own `src`; `api/meta` lists the `panel-config`
capability.

##### `weft dev`

```sh
weft dev -- go run ./cmd/app      # default command: go run .
```

`weft dev` starts Studio exactly as `weft studio` does (same flags,
same port policy, the playground on) and runs the command after `--`
with four environment variables added to yours:

| Variable | Value | What reads it |
|---|---|---|
| `WEFT_ENV` | `dev` (kept when you already set it non-empty) | `runtime.Install` opens its link |
| `WEFT_STUDIO_URL` | the Studio's URL | `otel`'s Studio destination; `runtime.Install`'s default endpoint |
| `WEFT_STUDIO_TOKEN` | the Studio's token (the real one: stable, fixed or generated) | the same two |
| `WEFT_DB` | the Studio's SQLite file, absolute (not set for ClickHouse) | `otel.Local("")`'s path: the app's local sink and Studio share one file |

The last three override whatever your shell had. Everything here is
an env var or option the app can set itself — `weft studio` in one
terminal and `WEFT_ENV=dev WEFT_STUDIO_URL=… WEFT_STUDIO_TOKEN=… go
run ./cmd/app` in another is the same thing without the command.

The app runs in its own process group; a `.go` change under the
working directory (`--watch dir`, repeatable, replaces it; `.git`,
`node_modules`, `vendor`, `testdata`, `.weft` and `dist` are skipped)
restarts it after 300 ms of quiet: SIGTERM to the group, five seconds,
then SIGKILL, then the command again. A build failure is printed and
the next save retries; an app that exits on its own is reported with
its exit code and the next save restarts it. `--no-watch` turns the
watching off, and `weft dev` then exits with the app's exit code (127
when the command cannot start at all). A directory that arrives with
`.go` files in it (a checkout, a `mv`) restarts too; editor lock files
(`.#name.go`) do not; a directory that cannot be watched (inotify's
`max_user_watches` spent) is said in one line. Ctrl-C, SIGTERM and
SIGHUP (the terminal closing) stop the app first — the same signal,
SIGHUP sent as SIGTERM — then Studio. A SIGKILL of `weft dev` cannot be
caught: on Linux `go run` still gets SIGTERM (Pdeathsig), but the
binary it started — the grandchild — can be orphaned; elsewhere the
whole group can.

Each start prints one line: the UI link (bare by default — the stable
token and a fixed `--token` / `WEFT_STUDIO_TOKEN` stay out of the log;
`#token=` only for a token generated for a database with no file), the
app's pid, and the first runtime that registered with
this Studio within five seconds — else `no runtime registered yet (the
app needs runtime.Install; WEFT_ENV=dev is set)` and a later line when
one does. A Studio bound to every interface (`--addr 0.0.0.0:7331`) is
handed to the app, and printed, as `127.0.0.1`.

Reuse follows the port policy, whose probe carries the fixed token
(`--token` / `WEFT_STUDIO_TOKEN`), else the database's stable token —
sent only to an address a trusted discovery file names (from another
directory with `--db` on the same file the probe goes out bare and a
second Studio starts on the next port): a Studio already serving the
same database is reused (the app gets that
URL and token; `weft dev` stops only the app), so two bare starts in a
row reuse. A running Studio walled by another token answers the probe
401 and is skipped — `weft dev` takes the next port with its own
Studio.

`go run ./studio/examples/basic` records demo runs (a tool call, a
subagent, a failure) into an obsdb database and serves Studio on
`127.0.0.1:7331`; `examples/studio-local` is setup A's five lines with
a live thread session, and its agent's `PrepareStep` trims a first-step
paragraph from the system prompt from step 1 on — the Request pane's
"changed by PrepareStep" diff (`TestPrepareStepTrimsThePrompt` pins the
record).

## Sessions — `weft/thread`

The core is stateless on purpose; `weft/thread` is the layer above it:
a conversation as an append-only tree of entries, durable through a
`Storage` backend, with branching, compaction and approvals built on
the same tree. A session is a file you can read with `jq` and
back up with `cp` — one header line, then one line per entry; nothing
is ever rewritten or deleted in place.

```go
st, _ := jsonl.Open(dir)                       // or thread.Memory() in tests
s, _ := thread.Create(ctx, st, agent)          // the agent is the session's own

turn, _ := s.Send(ctx, weft.User("Where is order 1234?"))
res, _ := turn.Wait()                          // prompt durable before the run;
                                               // the reply and the turn's ledger after
for ev, err := range turn.Events() { ... }     // forwarded run events, replayable

s.Branch(ctx, entryID)                         // navigate the tree; nothing lost
fork, _ := s.Fork(ctx, entryID)                // a new session, self-contained
s.Close(ctx)                                   // drain, seal, give up the writer's lease
again, _ := thread.Open(ctx, st, s.ID(), agent) // reopen from disk, same context
```

**One Session writes a session, and `Close` is how it stops.** A
`Session` takes its session's writer lease with its first write
(`Create` is one) and holds it until `s.Close(ctx)`, which stops new
work, drains the running turn and the queue, and seals the value
(`thread.ErrClosed`). Until then a second `Session` on the same
session opens and reads, and its writes fail with `thread.ErrLocked`;
one that fell behind another writer fails with `thread.ErrStale` —
open the session again. `Open` itself only reads: it writes nothing,
locks nothing and starts no run. A `Turn` can be waited on three ways
— `turn.Wait()`, `turn.WaitContext(ctx)`, `<-turn.Done()` — and
`turn.Outcome()` names how it ended; `s.WaitIdle(ctx)` also waits for
the compaction a turn may trigger after it is decided.

Durability is per step (ADR 0011 §7): the turn's messages are appended
as they join the run — through `weft.OnMessages`, the core's transcript
observer — so a crash mid-turn loses nothing emitted; the prompt was
already durable before the run started. Two backends carry it:
`thread/jsonl` (one file per session) and `thread/sqlite` (one SQLite
file, WAL; `thread` itself never imports the driver). A
crash mid-append leaves at most a torn final line, which the next
writer removes before it appends. Across processes and `Storage`
values a second writer gets `thread.ErrLocked`, while readers never
lock, including the live tail:

```go
w := st.(thread.Watcher)
tail, _ := w.Watch(ctx, s.ID(), lastEntryID)       // entries after lastEntryID
for e, err := range tail { … }                     // another process's tail

p, _ := st.List(ctx, thread.Query{
    Meta:        map[string]string{"env": "prod"}, // every pair present, exactly
    TitleSearch: "checkout",                       // the session's current title
})                                                 // newest first; page with Before + BeforeID
```

[docs/thread-operations.md](docs/thread-operations.md) is the
operator's page: file layout and backups, locks and leases, shutdown,
torn-tail repair, costs and limits, and which errors to retry.

Delegation is bounded and receipted (ADR 0022): a `thread/pool` admits
at most `max` child runs at work at once — the bound is the `Pool`
value's, FIFO, and a run that is only waiting on its own child holds
no slot. `p.MustWrap` turns any agent into a delegation tool (`p.Wrap`
returns the error instead of panicking) — sync by default, the call
waiting for the child session's answer; with `pool.Async()` the tool
result is the receipt and the child runs on — and `p.Submit` hands
background work to a child session directly:

```go
pl := pool.New(4)                                       // at most 4 child runs at work
research := pl.MustWrap("research", "Research a topic.", researcher)
rc, _ := pl.Submit(ctx, s, researcher, "survey the options") // a receipt, at once
rc2, _ := pl.Wait(ctx, s, rc.ID)                        // settled, or parked at an approval
err := pl.Decide(ctx, s, thread.Approve(childCallID))   // records and arms; does not wait
err = pl.Recover(ctx, s)                                // after a restart, once per parent
err = pl.Close(ctx)                                     // cancel what runs, drain
```

Every child is a session of its own, linked to the parent by lineage,
its cost in the parent's `Usage.Delegated` bucket (not in the parent
run's usage). A child that parks at an approval surfaces on the
parent's `Pending()`; `pl.Decide` records the decisions in the parent
and queues the child's resume — follow it with `pl.Wait` — and the
parent's parked call completes with the child's answer. `pl.Cancel`
ends one delegation, `pool.Receipts(s)` reads the ledger, and
`pl.Recover` reattaches, settles or fails what a dead process left
unsettled; it never re-runs a child.

Approvals (ADR 0021) make the core's run boundary durable: a gated
call parks as a request entry written with its turn, `Pending()`
survives restarts, and `Decide` records the decision and resumes on
its own — the decision chain (grants, then a bounded Approver, then
the park) runs before anything parks:

```go
turn, _ = s.Send(ctx, weft.User("Deploy to prod."))
res, _ = turn.Wait()                       // res.Pending: the gated calls
for _, r := range s.Pending() { notify(r) } // durable, restart-safe

rt, _ := s.Decide(ctx, thread.Approve(id)) // resumes when the boundary completes
follow := turn.Next()                      // the same resume, from the parked turn

s.Grant(ctx, thread.Grant{                 // "always allow go test"
    Tool: "run",
    Args: []thread.Arg{thread.ArgGlob("/command", "go test*")},
})

// Decisions that cross a process boundary are signed.
ring, _ := thread.NewKeyring(thread.Key{ID: "k1", Secret: secret, Active: true})
s, _ = thread.Create(ctx, st, agent,
    thread.WithKeyring(ring),
    thread.RequireSigned(),                       // stored in the header: every Open enforces it
    thread.WithApprover(ask, 30*time.Second))     // the live step and the time it is given
r, _ := s.Request(callID)                         // a challenge bound to this request entry
sd, _ := ring.Sign(r, thread.Approve(callID))     // or key.Sign: one key is one approver
rt, _ = s.DecideSigned(ctx, sd)                   // verified fail-closed, single-use
```

Grants match tool plus argument predicates (`ArgEquals`, `ArgPrefix`,
`ArgGlob`), expire, count uses, revoke, and can deny outright;
`Quorum(n)` needs n distinct approvers (a signed approval counts as its
key); `RequestExpiry(d)` lapses a request nobody decided;
`RequireSigned()` closes the unsigned door for the session's whole
life. A signed decision is bound to the request entry it was minted
for, so it can never approve a later call that reuses the id.
`s.Audit()` returns the approval trail from the file — an index of
what the session recorded, not tamper-evidence. A `Send` while
approvals pend queues behind them.

`go run ./thread/examples/approvals` parks a call, restarts, decides
signed, resumes, and replays a rejected signature — offline, pinned.

Steering (ADR 0019) is what a Send does when the session is busy — the
busy policy, per session or per Send:

```go
s, _ := thread.Create(ctx, st, agent, thread.BusyPolicy(thread.Steer))

steer, _ := s.Send(ctx, weft.User("wait — metric units"))  // mid-run
steer.Wait()                                  // nil result: a steer has no run of its own
steer.Outcome()                               // delivered, deferred or dropped
s.Queue()                                     // steers and sends accepted, not yet run
n, _ := s.ClearQueue(ctx)                     // drop them: receipts, Turns end ErrDropped

turn, _ := s.Send(ctx, weft.User("stop, do this instead"),
    thread.As(thread.Interrupt))              // cancel the run, run this next
turn, _ = s.Send(ctx, weft.User("no — this road"),
    thread.As(thread.Rollback))               // …and branch back before it
```

`Steer` delivers into the running turn at the loop's drain points —
after the tool batch, or at what would have been the final step —
through a receipt that is durable from acceptance: `queued → delivered
| deferred | dropped`, entries in the file. A steer that meets an
intended end (`StopWhen`) or an open approval boundary never drains:
it defers to a follow-up turn (`steer.Next()`). A send that waits for
a turn of its own — the default `Queue` policy on a busy session — is
durable the same way (an `accepted` receipt), so a crash loses no
accepted message: the next `Open` restores it to `s.Queue()`, and it
runs ahead of the next `Send` or at once with `s.Continue(ctx)`.
`Interrupt` cancels the in-flight run — its dangling calls record the
interruption text — and denies a parked boundary it supersedes;
`Rollback` also branches the leaf back, so the follow-up answers as
though the interrupted turn never happened (its entries stay on their
own line: nothing lost). A turn that overflows the window
(`weft.ErrContextOverflow`, mapped by every adapter) compacts —
reason `overflow` — and re-runs once (`thread.ReRunOnOverflow(false)`
to turn it off); a second overflow fails the turn with both errors
joined.

Compaction (ADR 0020) keeps long sessions inside the window without
losing anything: the older part is summarized behind a fixed marker,
the recent part stays raw, and the summarized entries stay in the
file. `thread.ContextWindow(n)` arms the automatic trigger — the
provider-reported input of the last step plus an estimated delta
against `window − Reserve`, never a chars-per-token guess — and every
layer is replaceable:

```go
s, _ = thread.Create(ctx, st, agent,
    thread.ContextWindow(200_000),       // arms the trigger; ModelWindows per model
    thread.SummaryModel(cheap),          // falls back to the session model
    thread.SummaryFocus("keep file paths"),
    thread.ClearOldToolResults(4),       // stub old tool results before summarizing
    thread.BeforeCompact(hook),          // thread.Proceed() / Cancel() / Replace(c)
)
plan, _ := s.PreviewCompaction(ctx)      // the cut and the summary, no write
s.ApplyCompaction(ctx, plan)             // or s.Compact(ctx) for both
s.Compact(ctx, thread.SummaryInstructions("focus on the API design")) // per-call guidance
s.Uncompact(ctx)                         // branch back — undo is a navigation
```

`thread.NoAutoCompact()` turns the automatic trigger off and leaves the
manual calls. Compaction is a between-turns operation: `Compact`,
`ApplyCompaction` and `Uncompact` fail with `thread.ErrBusy` while a
turn runs. Hooks run without the session's lock and may call the
session. Each compaction also emits one informational marker record
(kind `compaction`, scope `session`: counts and the entry's hash, no
messages) through the agent's `LoggerProvider` when it lands, under the
last run of this session that produced the compacted context — held for
the next run when none did — which `obsdb.DB.Compactions` and Studio's
run page read back.

`go run ./thread/examples/session` walks a session through turns, a
label, a branch, a fork, a previewed compaction and a reopen from
disk, offline through a scripted model.

`weft/thread` is pre-1.0 and **not frozen**: its API and its stored
format may still change between minor versions, each change recorded
in the CHANGELOG with what to write instead. What holds today: a
reader fails loudly (`ErrNewerFormat`) on an entry kind or version it
does not know, never skipping it, and golden files pin every format
version the current build reads
([ADR 0011](docs/adr/0011-thread-sessions.md), format reference).

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

Without a `weft.json`, Studio's Agents page shows the manifests your
app's `runtime.Install` registers (`weft studio` has the playground on
by default), each hash labelled live or remembered; Studio keeps them in
memory, so a restart forgets them until the runtime registers again.
With `--no-playground` the Agents page, the playground and the debugger
say why they are off (`/api/meta`'s `capabilities_off`).

A replay from step N may edit what it keeps before it runs: a kept
step's user message (step 0's is the turn's prompt), a call's
arguments (checked against the tool's schema, refused in the loop's
`INVALID_INPUT` words), a tool result, a call-free reply, or a user
message inserted at a step boundary — one `transcript_edits` list on
the command. `POST /api/playground/preview` takes the same body and
answers the exact first request that replay will send, diffed against
the one step N recorded, without calling a model or needing a runtime;
the replayed run carries `weft.edits` naming every edit
([ADR 0029](docs/adr/0029-replay-input.md) decision 8).

## MCP: both ways

`weft/mcp` (a package over the official Go MCP SDK, aliased
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
tools, err := mcp.Tools(ctx, sess, mcp.Prefix("gh_"), mcp.Policy(weft.Timeout(10*time.Second)))
// A tool the bridge cannot import fails that tool, not the listing:
// the good ones are in tools, the skipped ones are named. Warning or
// stop is your call; a listing failure (transport, ctx) is a plain err.
var skipped *mcp.ImportError
if errors.As(err, &skipped) {
    slog.Warn("mcp: tools skipped", "err", skipped)
    err = nil
}
if err != nil {
    return err
}

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
owns an HTTP client — and ship in the framework module. One line
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

Sampling is per run or per step the same way — `weft.Params(weft.
RequestParams{…})` (Temperature, TopP, MaxTokens, Stop, Seed) folds
over the adapter's construction options — and every adapter carries
`ExtraBody`/`ExtraHeaders`, the caller-wins escape hatch for vendor
knobs weft has no option for. Forcing a step's tool calls is
`weft.ToolChoice` (`any`, a named tool, or `none` with the catalogue
still advertised — the router shape).

**Prompt caching (Anthropic):** `anthropic.PromptCache()` marks the
request's stable prefix edges — the system block, the final tool
definition, the trailing conversation edge — with Anthropic's
ephemeral `cache_control`. Cache writes bill 1.25× and reads 0.1× the
base input price, so a long transcript whose prefix repeats across
steps saves from the second step on; `Usage.CachedInputTokens` and
`Usage.CacheWriteTokens` show it measured. The prefix is the caller's
to keep stable: a `PrepareStep` that trims messages invalidates the
trailing breakpoint on purpose, and `weft.ToolChoiceNone` is how you
forbid calls on a final step without dropping the tool definitions —
and the cache prefix they anchor — from the request.

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
- **One dependency** in the core module (`weft/core`): the OTel API
  (trace and logs — ADR 0016, ADR 0024), a no-op until an SDK
  registers — the zero-config instrumentation THE-END-GOAL sanctions
  as the core's single exception. The vendor SDKs, the OTel SDK and its
  exporters, the SQLite and ClickHouse drivers are dependencies of the
  framework module, never of `core` (ADR 0027).

## Layout

```
facade.go             the framework's root package: generated aliases and wrappers over core
core/                 the loop, a module of its own (go.mod; its one dependency is the OTel API):
  message.go            message model (roles, parts, versioned JSON)
  errors.go, env.go     error model (sentinels, RunError, kill switch)
  tool.go, schema.go    tool contract, per-tool policy, schema reflection
  output.go             structured output (Output, GenerateAs, OutputOf)
  model.go              provider seam (streaming-first Model interface)
  events.go             sealed run-event set
  agent.go, run.go      agent construction options, run/stream/result
  loop.go               the loop: model call → tool chain fan-out → repeat; approval resume
  mw/                   reference middleware: Retry, Fallback, Log, RepairJSON, Allow, Audit, MapErrors
  wefttest/             scripted mock model + the conformance suite
mw/, wefttest/        the framework's facades over core/mw and core/wefttest (generated)
internal/facadegen/   the facade generator (internal/cmd/genfacade runs it under go generate)
openai/               OpenAI Chat Completions (+ compatible servers)
anthropic/            Anthropic Messages (thinking, signatures)
google/               Gemini via genai
thread/ (+ sqlite/)   sessions: the append-only conversation tree, durable Storage backends
otel/                 the observability pipeline: destinations, content policies, heartbeats
obsdb/                the observability database: model, DB interface, sqlite backend, obsdbtest
obsdb/clickhouse/     the hosted backend (collector-compatible schema, materialized views)
studio/               the Inspector on obsdb: UI + JSON API + OTLP ingest + live (web/ is its
                      Bun source)
cmd/weft/             the weft binary: `weft studio` (setup B), dev, runs, open, export, doctor
runtime/              the playground's in-app side: the Studio link, the experiment executor
examples/             runnable examples (getting-started, approval, otel, studio-local;
                      per-adapter: <adapter>/example)
docs/adr/             decision records for the contracts
```

Two Go modules (ADR 0027): the root is the framework — every directory
above except `core/` is a package of it — and `core/` is the loop
alone. A release is two tags, `core/vX.Y.Z` then `vX.Y.Z`, and the
root requires `core` at that exact version with no replace; `go.work`
joins them for development. The root package, `mw`, `wefttest` and
`wefttest/conformance` are generated facades over `core`
(`make generate` after changing core's exported API). The whole
recorder-and-inspector story is two lines: `defer otel.Install()()`
and `studio.Handler(studio.DB(otel.LocalDB()))` (Recording runs and
Inspecting runs above); `weft/runtime` adds the playground with one
deferred `runtime.Install(...)` call.

## Development

```sh
make test   # go test -race ./... in every workspace module
make vet
make lint   # golangci-lint (CI uses .golangci.yml)
make live   # adapter conformance against real keys (-tags live)
make generate  # regenerate the facades after changing core's API
make apidiff   # public API of the framework module vs the last tag (reported)
make apidiff-core  # core vs its last core/v* tag (enforced)
make apidiff-all   # every workspace module vs its own last tag (CI runs it)
make fuzz    # 10 s per fuzz target; FUZZTIME=1m make fuzz for longer
make fmt
```

Requires Go 1.26 or newer; the current and previous Go releases are
supported and both are tested in CI. A fuzz crasher fails CI, its
input is uploaded, and the fix PR commits it under `core/testdata/fuzz/` as
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
        return wefttest.Record(t, "testdata/replay", openai.Model("gpt-5", openai.APIKey(key)))
    }
    return wefttest.Replay(t, "testdata/replay")
}
```

Fixtures are pretty JSON a reviewer reads in a diff — re-recording is
the review (ADR 0017). The adapters' own wire-format parsing is proven
by `wefttest/conformance` against recorded `.sse` fixtures, not by
replay (ADR 0013).

**API stability is enforced, not aspired to.** CI runs
`scripts/apidiff.sh` over both modules, each compared against its own
last tag. For `core` (the last `core/v*` tag) any incompatible change
fails the build. Pre-1.0, a deliberate source-compatible evolution
(widening a return type to a superset interface, adding a trailing
variadic) can be acknowledged by adding apidiff's exact line to
`core/.apidiff-allow` with a justification; the file is emptied at
each tag. Renaming or removing an exported symbol always fails. The
framework module holds the pre-freeze layers — `thread`, `obsdb`,
`otel`, `runtime`, `studio` — so the gate reports its incompatible
changes instead of failing, and such a change makes the next tag a
minor bump with a breaking CHANGELOG entry.

## Roadmap

1. ~~First provider adapters~~ — **done** (OpenAI + compatible servers,
   Anthropic, Google; ADR 0013).
2. ~~The two middleware seams~~ — **done** (`WrapModel`/`WrapTools`,
   package `mw`, the approval boundary; ADR 0006, ADR 0007).
3. ~~Loop refinements~~ — **done** (subagents as tools, `ModelRetry`,
   usage limits, loop detection, `PrepareStep`; ADR 0014).
4. ~~MCP interop~~ — **done** (consume and expose; ADR 0015). ~~Core
   observability~~ — **done** (OTel spans + slog lines; ADR 0016).
5. The satellites: ~~`store`~~ — **removed** (step 5 of ADR 0024;
   v0.1.3 remains on the module proxy). ~~`studio`~~ — **done** (the
   Inspector, v0.1.0, ADR 0018; rewritten on obsdb, ADR 0024 S4).
   ~~`thread`~~ — **shipped** (sessions, branching, compaction,
   approvals, steering, pool; [ADR 0011](docs/adr/0011-thread-sessions.md)
   and ADRs 0019–0022; the sandbox, ADR 0023, was abandoned). Pre-1.0
   and not frozen: the 2026-10-01 review's fix train shipped as
   thread v0.9.0 (thread/sqlite v0.3.0), and a field trial in real use
   comes before any API or format freeze.
   ~~`obsdb`, `otel`, `runtime`~~ — **done** (ADR 0024: the recorder,
   its database, the playground). Next: `serve` and the
   eval/prompt/mem/trace modules.

## License

MIT — see [LICENSE](LICENSE).
