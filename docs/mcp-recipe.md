# Reading runs from Claude Code and Cursor: the `weft mcp` recipe

`weft mcp` is a stdio MCP server over a running Studio's read API. A
coding agent (Claude Code, Cursor, VS Code) calls it to answer "why did
run X fail" while you debug, reading the same JSON routes the Studio UI
and `weft runs`/`open`/`export` read. It adds no route, computes
nothing Studio does not serve, and never writes: every tool is a GET.

## 1. Set it up: three commands

```sh
go install github.com/weftgo/weft/cmd/weft@latest   # the one binary: studio, dev, mcp, …
weft studio                                          # or: weft dev -- go run ./cmd/app
claude mcp add weft -- weft mcp                      # Claude Code: the tools appear as mcp__weft__*
```

`weft mcp` ships in every release since v0.15.0, so `@latest` has it, as
does `go install ./cmd/weft` from a checkout; `weft version` says which
one you run. The tools below are the current release's: `weft mcp --help`
lists the ones your binary has.

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

The tools, as `weft mcp --help` lists them: `explain_failure` (the run,
every step, its child runs and the transcript composed), `runs`,
`search`, `stats`, `run`, `step`, `request`, `events`, `tools`, `diff`,
`export` (json, jsonl, otlp), `sessions`, `session`, `logs`, `spans`,
`trace`, `transcript`, `live`, `agents`, `experiments`, `runtimes`,
`meta`. Every
answer is three parts: a one-line summary, the route's JSON — compact
rows on the list tools and compact spans on `spans`/`trace` (below;
`full: true` answers the JSON as served) — and a note when the output
was cut.

**Compact rows.** A coding agent pays for every byte, so the list tools
(`runs`, `search`, `sessions`, `session`, `live`, and `stats`' groups) answer a projection of
the route's JSON: empty, zero, false and null fields are omitted (absent
reads as that); a run row keeps its identity, outcome and size — id,
parent, agent, model as `provider/name`, session and turn, status, `err`
(200 characters), steps, pending, stop reason, the resume links, usage,
cost and its badge, the problem counters, children counts, the prompt
preview, `started` and `duration_ms` — and leaves out the trace id, the
content hashes, the record counts and `meta` (the `run` tool has them);
a hash a row keeps is cut to 12 characters; every cost reason is lifted
out of the rows into one `cost_reasons` list at the top (`{reason,
rows}`, `rows` the ids of the rows that gave it), never one per row;
a `*_known` flag is kept only when false and `cache` only when it is not
`exact`; a session's turns omit the agent, session
and public id the session names, cut `prompt_preview` to 80 characters
and omit their model, cost badge, stop reason and stopped-by when equal
to `turn_defaults` (said once: per key the value most turns share — a
tie to the earlier turn's — and only a value two turns or more share, so
a key no two turns agree on stays on each turn; a turn with none where
the default has one reads `none`); a session's outcomes strip is
counted (`outcome_counts`); a `stats` group leaves out its 24-bucket
`series`. `events` renames the route's `done` to `run_terminal` — the
run has ended, which is not "this is the last page": follow
`next_after` until it is absent (the summary says both). `spans` and `trace` answer each span
as its name, ids, `start_ms` / `dur_ms` from the first span (`t0`),
status and the attributes that say what it was (model, tokens, tool,
step, attempt, error, HTTP status), content attributes and events left
out. `full: true` on any of them answers the route's JSON as served. A
nine-turn session — each turn a tool call and an answer, every third
tool call failing, the prompts ~220 characters — answers 3.2 KB compact,
13.6 KB as served.

| The question | Call | Then |
|---|---|---|
| Why did run X fail? | `explain_failure {"id": X}` | its `first_error` and `digest`; then its `next`: `step`, `request`, `events` |
| It succeeded, but something went wrong inside | `explain_failure {"id": X}` | `first_error` names the run (a child's own) and the step; the `digest` has every step and child |
| What did the model see at step N? | `request {"id": X, "step": N, "transcript": true}` | `step {"id": X, "n": N}` for the step whole |
| Find the runs that called refund and failed | `search {"tool": "refund", "status": "failed"}` (or `"q": "<run error text>"`) | `explain_failure` on a hit |
| Which failures keep happening? | `search {"status": "failed", "all": true}` — `facets.error_class`, the failures grouped by their normalised error text, the summary naming the commonest | `search {"error_class": "<class>"}` or `runs {"error_class": "<class>"}` for that class's runs |
| How is agent X doing — failure rate, cost, latency? | `stats {"by": "agent", "since": "7d"}` (or `by` model, tool, error_type, step; `agent`, `tag`, `public_id` narrow it) | a group's `runs_query` is the `search` that lists its runs |
| Where did the refund tool return an error? | `step {"id": X, "n": N}` (each call's `result.is_error`) or `events {"id": X}` (`tool_finish` with `is_error`) | — search does not filter on tool results |
| Compare run A and B | `diff {"a": A, "b": B}` | `step` on the first changed step |
| What can this Studio do? | `meta {}` | a 404 or 403 elsewhere: is the capability off, and why |
| Which runs just happened? | `runs {"agent": "…", "status": "failed", "limit": 10}` | `explain_failure` |
| Is this run parked? | `explain_failure {"id": X}` | "awaiting a decision": approve or deny in Studio (§3); "decided: … denied by alice → resumed as Y": read Y |
| What did this turn answer? | `explain_failure {"id": X}` (`last_assistant_text`: this run's own text, never an earlier turn's) | `transcript {"id": X}` (each batch marked input or the run's own) |
| Where did the time go? | `spans {"id": X}` (`tree: true` with the child runs) | `trace {"trace_id": T}` for a stock OTel app's trace |
| What did the app log? | `logs {"id": X, "severity": "warn"}` | — a `not_recorded` hole names `otel.Slog()` |
| What is running now? | `live {}` (a snapshot; call again) | `explain_failure` on one |
| Which agents, experiments, runtimes? | `agents {}`, `experiments {}`, `runtimes {}` | a 404: the playground or the manifest is off (`meta`) |

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
run a1100000000000000000000000000001 (support): failed before step 1 started (step 0, its last recorded step, finished): weft: run failed at step 1: weft: run exceeded the maximum number of steps; stop_reason tool_calls; tool calls: lookup_order ok; no assistant text this run — http://127.0.0.1:33933/runs/a1100000000000000000000000000001
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

**It succeeded, but a child failed.** The arena's `sub.failed`, built
in the check on scripted models: the shipping-quote subagent's
provider is down, the parent reads `SUBAGENT_FAILED` and answers with a
fallback. The run succeeded; `first_error` is the child's own, naming
the child run and its step:

```text
> explain_failure {"id":"c5b0000000000000000000000000f001"}
run c5b0000000000000000000000000f001 (checkout-assistant): succeeded, last step 1; stop_reason stop; first error: step 0 shipping_quote → child c5b0000000000000000000000000f001/0/q1 (shipping-quoter) step 0: model attempt 1: model stream: carrier API outage: DHL rates unavailable; digest: 2 of 3 lines report an error, a park or a failed child; last assistant text "Express quotes are down; standard shipping is €7.90, 4–6 days." — http://127.0.0.1:33933/runs/c5b0000000000000000000000000f001
digest:
  step 0: ok (tool_calls): shipping_quote error "SUBAGENT_FAILED: agent \"shipping_quote\" failed at step 0: model stream: carrier API outage: DHL rates unavailable"
  step 1: ok (stop)
  child c5b0000000000000000000000000f001/0/q1 (shipping-quoter) ← shipping_quote: failed (model_error) "weft: run failed at step 0: model stream: carrier API outage: DHL rates unavailable"
```

The arena's `agentic.chaos` — six reviewers in one step, every
failure shape at once — reads each child's outcome in the digest:

```text
> explain_failure {"id":"c5b0000000000000000000000000c405"}
run c5b0000000000000000000000000c405 (deal-lead): succeeded, last step 1; stop_reason stop; first error: step 0 review_ip → child c5b0000000000000000000000000c405/0/i1 (ip-reviewer) step 0: model attempt 1: model stream: provider maintenance window; digest: 4 of 8 lines report an error, a park or a failed child; last assistant text "Data room: …" — …
digest:
  step 0: ok (tool_calls): review_financials ok; review_hr ok; review_ip error "SUBAGENT_FAILED: …provider maintenance window"; review_tax error "tool \"review_tax\" timed out after 150ms"; sign_nda error "SUBAGENT_PENDING: agent \"sign_nda\" ended awaiting approval of 1 call(s)"; review_litigation ok
  step 1: ok (stop)
  child …/0/f1 (financials-reviewer) ← review_financials: succeeded
  child …/0/h1 (hr-reviewer) ← review_hr: succeeded
  child …/0/i1 (ip-reviewer) ← review_ip: failed (model_error) "weft: run failed at step 0: model stream: provider maintenance window"
  child …/0/t1 (tax-reviewer) ← review_tax: failed (cancelled) "weft: run failed at step 0: model stream: context deadline exceeded"
  child …/0/s1 (nda-signer) ← sign_nda: succeeded; parked: 1 call awaiting a decision
  child …/0/l1 (litigation-reviewer) ← review_litigation: succeeded; its first error: step 0 ocr_exhibit: tool "ocr_exhibit" panicked: ocr engine: nil page
```

`first_error` is the first error nothing recovered from — a failed
call, a model attempt no retry fixed, the run's own failure — and only
when there is none, the first attempt a retry recovered from, marked
`(retried, attempt N ok)`. A child is matched to its call by the step's
`child_run_id` (call ids repeat across steps).

The walk is bounded: at most 25 steps per run (a child's too), 12
children listed, two child levels for a first error, 60 reads per
answer (the run row and the transcript included), each digest line at
most 1 KB (`+n more`); every cap it hit is said at the digest's end —
the steps a run did not walk, the children not listed (by id) — and a
child the budget left unread says so on its own line, by id; each with
the read that goes further. The
digest comes after `next` and `last_assistant_text` in the JSON, so a
cut at `max_bytes` takes it first.

A parked run is not a failure, and says so:

```text
> explain_failure {"id":"a1100002000000000000000000000001"}
run a1100002000000000000000000000001 (support): succeeded, last step 0; stop_reason tool_calls; parked: 1 call awaiting a decision (refund_order); tool calls: refund_order pending; digest: 1 of 1 lines report an error, a park or a failed child; no assistant text this run — http://127.0.0.1:33933/runs/a1100002000000000000000000000001
```

Once a resume decided it, it reads decided — the outcome, who and why,
and the turn that resumed it (the run document's `decided` block) —
never "awaiting". A thread turn parked on `deploy`, denied by alice:

```text
> explain_failure {"id":"s_chk_ops-t1"}
run s_chk_ops-t1 (ops-bot): succeeded, last step 0; stop_reason tool_calls; parked, decided: deploy denied by alice: change freeze until Monday → resumed as s_chk_ops-t2; tool calls: deploy denied by alice; digest: 1 of 1 lines report an error, a park or a failed child; no assistant text this run — http://127.0.0.1:33933/runs/s_chk_ops-t1

> explain_failure {"id":"s_chk_ops-t2"}
run s_chk_ops-t2 (ops-bot): succeeded, last step 0; stop_reason stop; resumed s_chk_ops-t1: deploy denied by alice: change freeze until Monday; last assistant text "Deploy to prod cancelled: alice denied it (change freeze)." — http://127.0.0.1:33933/runs/s_chk_ops-t2
```

`last_assistant_text` is the run's own: a thread turn's input carries
the earlier turns' answers, and a turn that wrote none answers "no
assistant text this run" (`last_assistant_note`), never the previous
turn's.

A run whose process died mid-tool (no end ever arrived) reads
interrupted, the call it was in listed without a result:

```text
> explain_failure {"id":"a1100003000000000000000000000001"}
run a1100003000000000000000000000001 (support): interrupted at step 0; tool calls: lookup_order no result; holes: interrupted; no assistant text this run — http://127.0.0.1:33933/runs/a1100003000000000000000000000001
```

A run derived from a stock OpenTelemetry GenAI app (no weft
instrumentation) answers from its spans, its holes saying what is
absent:

```text
> explain_failure {"id":"otel-5c0a3e1f9b8d4c2e8a7f6b5d4c3e2f10-1a2b3c4d5e6f7081"}
run otel-5c0a3e1f9b8d4c2e8a7f6b5d4c3e2f10-1a2b3c4d5e6f7081 (support): failed at step 0: agent cancelled by the user; holes: derived; no assistant text this run — http://127.0.0.1:33933/runs/otel-5c0a3e1f9b8d4c2e8a7f6b5d4c3e2f10-1a2b3c4d5e6f7081
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
{"cost_reasons":[{"reason":"a scripted model: nothing bills it, an exact $0","rows":["a1100000000000000000000000000001"]}],"runs":[{"agent":"support","cost_badge":"scripted","duration_ms":6,"err":"weft: run failed at step 1: weft: run excee…

> search {"status":"failed","tool":"lookup_order"}
search: 1 runs of 1
{"cost_reasons":[…],"runs":[{"agent":"support", …
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
Studio at http://127.0.0.1:33933: capabilities requests, steps, export, logs, diff, traces, search, manifests, spans_tree, timeline, pricing, delete, proposals, live, ingest, auth, handoff, panel-config
{"weft_version":"v0.20.0","studio_version":"v0.20.0","panel_version":"v0.20.0","db":{"kind":"sqlite","path":"/tmp/TestMCPLiveCheck3582215505/001/weft.db","size":922888},"title":"weft studio","has_manifest":false,"ingest_open":true,"interrup…
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
| a read-only panel token | minted for one public id (`/api/panel-tokens`, setup C) | that public id's runs only; system prompts, request records and app logs `hidden`; session reverse lookups refused |

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
run a1100000000000000000000000000001 exported as jsonl (6678 bytes)
{"record":"run","format":"weft.run.export/1","run":{"id":"a1100000000000000000000000000001", …
[truncated: showing 2048 of 6678 bytes — the JSON above is cut and does not parse; raise max_bytes (at most 4194304) or narrow the query (limit, step, refs)]
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
`<db>.token`, then — later, through the otel pipeline on wefttest
scripted models — four shapes built in the check from the arena's
scenarios: `sub.failed`, `agentic.chaos`, a thread turn denied by alice
and resumed, and a nine-turn session; then:

- spawns `weft mcp --url …` with the token in `WEFT_STUDIO_TOKEN` and
  `WEFT_DISCOVERY=off`, over the MCP SDK's `CommandTransport`, and calls
  every tool — `meta`, `runs`, `explain_failure`, `run`, `step`,
  `request`, `events`, `tools`, `search`, `diff`, `export`,
  `explain_failure` on the parked, interrupted and derived runs,
  `sessions`, `session` — asserting each summary line — and then
  `explain_failure` on the four shapes: `sub.failed` answers the
  child's error (its `first_error` the child run), `agentic.chaos` its
  four failure shapes (SUBAGENT_FAILED, a timeout, SUBAGENT_PENDING, a
  panicking tool inside a child that recovered), the denied turn
  "denied by alice … → resumed as …-t2" with no text of its own, a
  session turn its own text; the nine-turn `session` under 4 KB; and
  `spans` (a tree), `logs`, `transcript`, `live`;
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

## 8. Your own agents over MCP: the caller link (`weft/mcp`)

Not `weft mcp` itself — the `weft/mcp` package, when one of your agents
calls another you serve with `mcp.Serve`. To see the served run linked
to the call that made it, opt in on the client:

    tools, err := mcp.Tools(ctx, sess, mcp.Server("helpers"), mcp.CallerMeta())

Every call those tools make then carries, in `params._meta`, the W3C
`traceparent` (and `tracestate`) of its `execute_tool` span and
`dev.weftgo/caller` `{run_id, call_id}`. The served run records
`weft.caller.run.id` / `weft.caller.call.id` with `weft.caller.remote`,
and its `invoke_agent` span links (an OpenTelemetry link) to the caller's
span — its own trace, never the caller's child, so the client's
sampling never decides whether the server records it. Studio keeps a
remote link off the run's place: the served run is top-level and its
session's own turn, whatever ids a client states (record contract §2).

`CallerMeta` is **off by default**: the run and call ids and the trace
state name your runs to the server. Turn it on for servers you run, not
for a third party's.
