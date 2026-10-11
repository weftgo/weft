# Reading runs from Claude Code and Cursor: the `weft mcp` recipe

`weft mcp` is a stdio MCP server over a running Studio's read API. A
coding agent (Claude Code, Cursor, VS Code) calls it to answer "why did
run X fail" while you debug, reading the same JSON routes the Studio UI
and `weft runs`/`open`/`export` read. It adds no route, computes
nothing Studio does not serve, and never writes: every tool is a GET.

## 1. Set it up: three commands

```sh
go install github.com/weftgo/weft/cmd/weft@main     # the one binary: studio, dev, mcp, …
weft studio                                          # or: weft dev -- go run ./cmd/app
claude mcp add weft -- weft mcp                      # Claude Code: the tools appear as mcp__weft__*
```

`weft mcp` ships from v0.15.0 (unreleased at the time of writing:
`go install github.com/weftgo/weft/cmd/weft@main` until then, or `go
install ./cmd/weft` from a checkout; `@latest` resolves to v0.14.2, which
has no `weft mcp`). Once v0.15.0 is tagged, `@latest` is the line.

`weft studio` (or `weft dev`, which runs Studio and your app together)
listens on `127.0.0.1:7331` and writes the discovery file `studio.json`
with its URL and dev token. `weft mcp`, started by Claude Code with no
flags, reads that file and talks to that Studio with that token. Your
app finds the same Studio the same way (`otel.Install()`), so its runs
are already there.

To name a Studio yourself:

```sh
claude mcp add weft -- weft mcp --url http://127.0.0.1:7331
claude mcp add weft -e WEFT_STUDIO_TOKEN=<token> -- weft mcp --url http://127.0.0.1:7331
```

`--token <token>` works too. Both forms land in the client's config in
clear (`claude mcp add` stores the `-e` value as it stores the
arguments); the difference is at run time: an argument shows in `ps`
to every user of the machine, an environment value does not — so
prefer `-e WEFT_STUDIO_TOKEN=…`. Keep the token out of a file you
commit: Claude Code expands `${VAR}` in a project-scoped `.mcp.json`,
so a shared one reads
`"env": {"WEFT_STUDIO_TOKEN": "${WEFT_STUDIO_TOKEN}"}` and each
developer exports their own. With the discovery file (no `--url`, no
token anywhere) there is nothing to store at all.

**Cursor**, `.cursor/mcp.json` (Claude Code's project-scoped `.mcp.json`
is the same shape):

```json
{"mcpServers": {"weft": {"command": "weft", "args": ["mcp"]}}}
```

with `"args": ["mcp", "--url", "http://127.0.0.1:7331"]` to name a
Studio, and `"env": {"WEFT_STUDIO_TOKEN": "…"}` beside `args` for a
token.

**VS Code**, `.vscode/mcp.json`, is *not* the same shape: its top-level
key is `servers`:

```json
{"servers": {"weft": {"type": "stdio", "command": "weft", "args": ["mcp"]}}}
```

Every convenience is escapable:

| What | Order (first wins) |
|---|---|
| the Studio | `--url`, `WEFT_STUDIO_URL`, the discovery file (skipped under `WEFT_DISCOVERY=off`), `http://127.0.0.1:7331` |
| the token | `--token`, `WEFT_STUDIO_TOKEN`, the discovery file's — only when that file names the Studio resolved above |

The token travels in the `Authorization` header only; `weft mcp` never
prints it (its one stderr line says where the token came from: `token
from --token`, `from WEFT_STUDIO_TOKEN`, `from the discovery file`).

## 2. Which tool answers which question

The thirteen tools, as `weft mcp --help` lists them: `explain_failure`
(run + step + transcript composed), `runs`, `search`, `run`, `step`,
`request`, `events`, `tools`, `diff`, `export` (json, jsonl, otlp),
`sessions`, `session`, `meta`. Every answer is three parts: a one-line
summary, the route's JSON (compacted), and a note when the output was
cut.

| The question | Call | Then |
|---|---|---|
| Why did run X fail? | `explain_failure {"id": X}` | follow its `next`: `step`, `request`, `events` |
| What did the model see at step N? | `request {"id": X, "step": N, "transcript": true}` | `step {"id": X, "n": N}` for the step whole |
| Find the runs that called refund and failed | `search {"tool": "refund", "status": "failed"}` (or `"q": "<run error text>"`) | `explain_failure` on a hit |
| Where did the refund tool return an error? | `step {"id": X, "n": N}` (each call's `result.is_error`) or `events {"id": X}` (`tool_finish` with `is_error`) | — search does not filter on tool results |
| Compare run A and B | `diff {"a": A, "b": B}` | `step` on the first changed step |
| What can this Studio do? | `meta {}` | a 404 or 403 elsewhere: is the capability off, and why |
| Which runs just happened? | `runs {"agent": "…", "status": "failed", "limit": 10}` | `explain_failure` |
| Is this run parked? | `explain_failure {"id": X}` | approve or deny in Studio (§3) |

### Worked examples

These are the output of `make mcp-check` (§7): a real `weft studio`, the
record contract's fixtures (`obsdb/testdata/contract/`, real recorded
pipeline output, renumbered per case so they share one database), and
the real `weft mcp` binary driven over stdio by the MCP Go SDK's client,
the way Claude Code drives it. The Studio sat on a free port
(`127.0.0.1:33933`); yours is `127.0.0.1:7331`. Each example is the call,
the summary line, and the start of the JSON (cut here with `…`).

**Why did run X fail?** The `failed` fixture: `MaxSteps(1)` against a
model that keeps calling a tool.

```text
> explain_failure {"id":"a1100000000000000000000000000001"}
run a1100000000000000000000000000001 (support): failed before step 1 started (step 0, its last recorded step, finished): weft: run failed at step 1: weft: run exceeded the maximum number of steps; stop_reason tool_calls; tool calls: lookup_order ok — http://127.0.0.1:33933/runs/a1100000000000000000000000000001
{"run_id":"a1100000000000000000000000000001","url":"http://127.0.0.1:33933/runs/a1100000000000000000000000000001","agent":"support","status":"failed","error":"weft: run failed at step 1: weft: run exceeded the maximum number of steps","stop…
next: step {id:"a1100000000000000000000000000001", n:0} for the step whole | request {id:"a1100000000000000000000000000001", step:0, transcript:true} for the exact model input | events {id:"a1100000000000000000000000000001"} for the event stream
```

(`next:` is the explanation's `next` array, joined.) The answer names
the error, the step it is about and why that step (here: the budget
stopped the run *between* steps, so the step shown is the last one that
ran), that step's tool calls with their outcome, and the Studio page.
Then the step whole:

```text
> step {"id":"a1100000000000000000000000000001","n":0}
run a1100000000000000000000000000001 step 0: ok (tool_calls); tool calls: lookup_order ok
{"run_id":"a1100000000000000000000000000001","step":0,"status":"ok","reason":"tool_calls","started":"2026-10-01T09:09:00.002Z","finished":"2026-10-01T09:09:00.01Z","latency_ms":1,"model":{"provider":"wefttest","requested":"script","answered…
```

A parked run is not a failure, and says so:

```text
> explain_failure {"id":"a1100002000000000000000000000001"}
run a1100002000000000000000000000001 (support): succeeded, last step 0; stop_reason tool_calls; parked: 1 calls awaiting approval (refund_order); tool calls: refund_order pending — http://127.0.0.1:33933/runs/a1100002000000000000000000000001
```

A run whose process died mid-tool (no end ever arrived) reads
interrupted, the call it was in listed without a result:

```text
> explain_failure {"id":"a1100003000000000000000000000001"}
run a1100003000000000000000000000001 (support): interrupted at step 0; tool calls: lookup_order no result; holes: interrupted — http://127.0.0.1:33933/runs/a1100003000000000000000000000001
```

A run derived from a stock OpenTelemetry GenAI app (no weft
instrumentation) answers from its spans, its holes saying what is
absent:

```text
> explain_failure {"id":"otel-5c0a3e1f9b8d4c2e8a7f6b5d4c3e2f10-1a2b3c4d5e6f7081"}
run otel-5c0a3e1f9b8d4c2e8a7f6b5d4c3e2f10-1a2b3c4d5e6f7081 (support): failed at step 0: agent cancelled by the user; holes: derived — http://127.0.0.1:33933/runs/otel-5c0a3e1f9b8d4c2e8a7f6b5d4c3e2f10-1a2b3c4d5e6f7081
```

**What did the model see at step N?** The request records (prompt hash,
catalog, params) and, with `transcript: true`, the messages step N's
model call carried, as a second JSON value after the first:

```text
> request {"id":"a1100000000000000000000000000001","step":0,"transcript":true}
run a1100000000000000000000000000001 requests: 1 requests; then the transcript of step 0
{"requests":[{"index":0,"step":0,"attempt":1,"time":"2026-10-01T09:09:00.005Z","system_hash":"57e8f485cbb5aab3a684a972d8cc3981c066cbf52ef8706d75f2bc0a831c78f7","catalog_hash":"d90d08d79c38775c5a61ba7037f26bb5e9f84ef466c82dae2a056cdd8614a1e8…
```

**Find the runs that called a tool and failed.** `search` takes text
(`q`, over the run's prompt, answer and error and the names of the tools
it called) and filters (`tool` — a tool the run called —, `status`,
`agent`, `model`, `since`/`until`, `min_tokens`, `min_cost`, `sort`).
It finds runs, not tool results: a tool's error is data the model sees
(the run usually goes on and succeeds), so `q`/`error` match the *run's*
error, never a tool result's. To see a tool's error result, read the
step (`step`: each call's `result` with `is_error`) or the event stream
(`events`: `tool_finish` with `is_error`).

```text
> search {"q":"exceeded"}
search: 1 runs of 1
{"total":1,"runs":[{"id":"a1100000000000000000000000000001","parent_run_id":"","parent_call_id":"","trace_id":"7ace0000000000000000000000000001","agent":"support","model":{"provider":"wefttest","name":"script"},"manifest_hash":"3a21f0000000…

> search {"status":"failed","tool":"lookup_order"}
search: 1 runs of 1
{"total":1,"runs":[{"id":"a1100000000000000000000000000001", …
```

**Compare run A and B.** Two runs aligned by step ordinal; a column a
side did not record is "not comparable", never a change:

```text
> diff {"a":"a1100000000000000000000000000001","b":"a1100004000000000000000000000001"}
diff a1100000000000000000000000000001 → a1100004000000000000000000000001: 1 changed steps, first at step 1
{"a":{"run_id":"a1100000000000000000000000000001","steps":1,"status":"failed"},"b":{"run_id":"a1100004000000000000000000000001","steps":2,"status":"succeeded"},"steps":[{"step":0,"changed":false,"a":{"status":"ok","system_hash":"57e8f485cbb…
```

**What can this Studio do?**

```text
> meta {}
Studio at http://127.0.0.1:33933: capabilities requests, steps, export, logs, diff, traces, search, pricing, delete, proposals, live, ingest, auth, handoff, panel-config
{"weft_version":"v0.14.2","studio_version":"v0.14.2","panel_version":"v0.14.2","db":{"kind":"sqlite","path":"/tmp/TestMCPLiveCheck3582215505/001/weft.db","size":922888},"title":"weft studio","has_manifest":false,"ingest_open":true,"interrup…
```

`capabilities_off` in the JSON names each capability that is off and
the option or flag that turned it off.

## 3. Read only, by construction

`weft mcp` never approves, denies, resolves, steers, cancels, deletes,
replays, edits or starts anything. Two walls hold it:

- no write tool is registered — there is nothing to call;
- under every tool sits an HTTP transport that refuses any method but
  GET and HEAD before the request leaves the process
  (`weft mcp is read-only: POST refused`), whatever a handler does.

`--read-only=false` is a usage error. Write verbs stay out until weft
has an approval story for agents. Until then the agent hands off to
you: every `explain_failure` answer ends with the run's Studio page
(`— http://127.0.0.1:7331/runs/<id>`, the `url` field), where you
approve or deny a parked call, steer or cancel a running one, replay
from a step, edit a result or delete the run.

## 4. Tokens: what each sees

| Token | Where it comes from | What `weft mcp` reads with it |
|---|---|---|
| the dev token | `weft studio`/`weft dev`: `<db>.token` beside the SQLite file (0600, never printed), in the discovery file; `--token`/`WEFT_STUDIO_TOKEN` override it | everything every tool offers |
| a read-scoped panel token | minted for one public id (`/api/panel-tokens`, setup C) | that public id's runs only; system prompts, request records and app logs `hidden`; session reverse lookups refused |

A route a token may not read answers 403, and the tool returns it as an
error (`isError`): Studio's own line, then its body — the error, the
badge, the reason and the `fix` when the route gives one — never an
empty result. `explain_failure` under a read token still answers: the
step it cannot read is named in `step_note` (`hidden (403)`), and its
`next` leaves out the `request` it could not follow. `meta` says which
token you hold (`auth_required`, `debug_scope`).

## 5. Limits

Every tool takes `max_bytes`: default 64 KiB (weft's own tool-result
cap), at most 4 MiB. A cut output is never silent — it ends with a
note, and the cut JSON does not parse:

```text
> export {"format":"jsonl","id":"a1100000000000000000000000000001","max_bytes":2048}
run a1100000000000000000000000000001 exported as jsonl (6535 bytes)
{"record":"run","format":"weft.run.export/1","run":{"id":"a1100000000000000000000000000001", …
[truncated: showing 2048 of 6535 bytes — the JSON above is cut and does not parse; raise max_bytes (at most 4194304) or narrow the query (limit, step, refs)]
```

Prefer the narrow tools (`run`, `step`, `request`) to `export`; page
`runs` and `events` with `limit` and their cursors.

**A budget failure is about earlier steps.** `MaxSteps`, `UsageLimit`
and `DetectLoops` fail at the continuation point — when the loop would
call the model again — so the step `explain_failure` shows is the last
one that ran, finished cleanly (`failed before step N started (step
N-1, its last recorded step, finished)`), and the calls that spent the
budget sit in that step and the ones before it. Follow `next`: `events
{"id": X}` walks the whole run (`after`, `limit` to page), `step {"id":
X, "n": k}` for each earlier step, `diff` against a run that finished.
A loop (`DetectLoops`) shows as identical `tool_calls` across those
steps.

## 6. Troubleshooting

**`no Studio answers /api/meta: studio not reachable at
http://127.0.0.1:7331: Get "http://127.0.0.1:7331/api/meta": dial tcp
127.0.0.1:7331: connect: connection refused (weft studio starts one;
--url or WEFT_STUDIO_URL points at another); the next call looks again
(WEFT_STUDIO_URL, the discovery file): no restart needed`** as a tool's
error (and a `level=WARN … no Studio answers /api/meta yet; tools will
retry per call` line in the client's MCP log at startup) — no Studio is
running there. `weft mcp` does not exit: it serves, and the next tool
call after a failure looks again — with no `--url`, it re-reads
`WEFT_STUDIO_URL` and the discovery file, so a Studio started after the
coding session (or `weft dev` on 7332–7340) is found without
restarting the client; an explicit `--url` is retried as given. Start
one (`weft studio` or `weft dev`), or point at yours: `--url`,
`WEFT_STUDIO_URL`. If a Studio *is* running elsewhere, the discovery
file was not used: check that `WEFT_DISCOVERY` is not `off` and
`WEFT_STUDIO_URL` is unset in the environment the client starts `weft
mcp` in (Claude Code passes its own environment, plus `-e` pairs).

**`weft: mcp: no Studio answers /api/meta: GET /api/meta: 401 the API
requires a token: Authorization: Bearer <token> (--token or
WEFT_STUDIO_TOKEN)`** — that Studio walls its API
and no token reached it. Give `-e WEFT_STUDIO_TOKEN=…` or `--token`; with
`--url`, the discovery file's token is used only when the file names
the same URL (a token goes only to the Studio it belongs to). A 401
mid-session, with no `--url`, `--token` or `WEFT_STUDIO_TOKEN`, ends
"…; the next call looks again (WEFT_STUDIO_URL, the discovery file): no
restart needed": a Studio restarted with a new token (a fileless
database's per-process one, `--rotate-token`) is read with the
discovery file's new token on the next call.

**The discovery file is ignored.** `studio.json` lives in `./.weft`
(only from the directory the Studio was started in), then
`$XDG_RUNTIME_DIR/weft`, then the user cache directory. It is trusted
only when its URL is loopback, it is fresh (the Studio's pid alive,
younger than 24 h) and, on unix, it is mode 0600 and yours; anything
else is skipped. `weft studio` removes it on a clean exit. `WEFT_STUDIO_URL`
or `--url` always wins over it, and `WEFT_DISCOVERY=off` turns the read off.

**A tool answers 404.** The run id is wrong (ids are 32 hex characters,
`otel-…` for runs derived from stock spans, `<run>/0/call_1` for a
subagent child), or the route's capability is off — ask `meta`.

## 7. `make mcp-check`: the live check

`make mcp-check` (`cmd/weft/mcp_check_test.go`, build tag `livecheck`)
proves "`weft mcp` answers 'why did run X fail'" end to end without a
model: it builds `weft`, starts `weft studio --db sqlite://<tmp>
--addr 127.0.0.1:<free port> --no-playground` in a scratch directory
with `HOME`, `XDG_RUNTIME_DIR` and `XDG_CACHE_HOME` inside it (so its
`studio.json` lands there; your own discovery files are compared before
and after and must not change), POSTs the `failed`, `parked`,
`interrupted`, `tool_calls`, `session_turns` and `stock_genai` fixtures
to `/v1/traces` and `/v1/logs` with the dev token read from
`<db>.token`, then:

- spawns `weft mcp --url …` with the token in `WEFT_STUDIO_TOKEN` and
  `WEFT_DISCOVERY=off`, over the MCP SDK's `CommandTransport`, and calls
  every tool — `meta`, `runs`, `explain_failure`, `run`, `step`,
  `request`, `events`, `tools`, `search`, `diff`, `export`,
  `explain_failure` on the parked, interrupted and derived runs,
  `sessions`, `session` — asserting each summary line;
- spawns `weft mcp` with no flag and no token (the `claude mcp add weft
  -- weft mcp` path), which finds the scratch Studio through its
  discovery file, and asserts `meta` and the stderr line `token from the
  discovery file`.

It prints each summary line and ends `MCP CHECK PASS`. It is not part
of `make test`: it listens on a port.

What it does not prove is Claude Code itself: the check speaks the same
MCP protocol over stdio that Claude Code speaks, through the official
Go SDK's client; the registration is the documented `claude mcp add`
command above.
