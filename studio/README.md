# weft studio — the Inspector

The UI, the JSON API, the live stream and the OTLP receiver over one
[obsdb](../obsdb) database: the runs list, the run page (steps, tool
calls and results, subagents lazy, truncation badged, replay over the
event index, a time-axis waterfall when the run has spans), sessions
(threads), any trace — weft or not — a live page, and agent and tool
cards from the manifest. One `http.Handler`, no build step for users,
fully offline.

## The three setups (S4.6)

**A · embedded** — the five lines beside your app
([examples/studio-local](../examples/studio-local) is the whole thing,
with a thread session whose turns stream in live):

```go
defer otel.Install()() // local sink ./.weft/weft.db, content on, no network
mux.Handle("/studio/", http.StripPrefix("/studio",
    studio.Handler(studio.DB(otel.LocalDB())))) // same DB, same live hub
```

Passing the pipeline's handle is what makes it live: writes publish to
the handle's hub and `/api/live` follows, sub-100 ms, no network.
`studio.Open(path)` opens an obsdb sqlite file; with neither, New
opens `$WEFT_DB` or `./.weft/weft.db` (history only — a second handle
on the file, no live lane).

Without a `Token`, setup A's API (the whole `/api` tree: reads, the
live stream, the playground and debugger verbs, the runtime link)
answers only a **loopback `Host`** — `localhost`, `*.localhost`,
`127.0.0.0/8`, `[::1]`, any port — so a DNS-rebinding page
(`http://evil.example:7331` resolving to 127.0.0.1, same-origin to the
browser) gets a 403 instead of your transcripts and your playground.
An app served on a real hostname (`http://myapp.internal:8080/studio/`)
either lists that origin — `studio.AllowOrigins("http://myapp.internal:8080")`,
whose `host:port` must equal the request's `Host` — or sets
`studio.Token`. `X-Forwarded-Host`/`Forwarded` are never trusted; behind
a proxy, the `Host` the proxy forwards is the one checked. The UI shell
and `/panel.js` carry no data and are not checked; with a `Token` the
bearer is the defence and the `Host` does not matter.

**B · local binary** — any language's app, several services:

```sh
go run ./studio/cmd                    # UI + OTLP + SQLite + dev token on 127.0.0.1:7331
WEFT_STUDIO_URL=http://127.0.0.1:7331 ./my-go-app
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:7331 python app.py
```

The dev token is printed at start (`WEFT_STUDIO_TOKEN` or `--token`
fixes it); ingest is open on loopback; `--db sqlite://path` picks the
file, `--db clickhouse://user:pass@host:9000/db` the hosted backend
(`obsdb/clickhouse`, the one place the driver is imported).

**C · hosted** — the same handler behind `studio.Token`: the panel's
scoped tokens are HMAC-signed `{public_id, scope, exp}` minted by your
backend through `POST /api/panel-tokens`; every data route refuses
anything outside the token's public id. A token is read-only unless
minted with `"playground": true` — a read-scoped one neither acts nor
reads the agents' system prompts (`/api/manifest`, the runtimes'
instructions) — and experiments (`experiment_id` included) are the
server token's alone.

## The devtools panel (WEFT-DEVTOOLS.md)

One script tag puts the run loop in the corner of your own page —
`/panel.js` is served by the same handler:

    <script type="module" src="/studio/panel.js" data-public-id="pub_…"></script>

Rung 1 is a viewer scoped to that conversation: the turns (parked
shown), the step story with tool calls, usage splits, approvals
read-only, truncation/gap/stripped honesty, the raw JSON, a live tail,
⤢ deep links into Studio, lazy subagents and a spans waterfall.
`Alt+W` toggles (Q4), `?` lists keys, `r` flips raw. Setups B/C add
`data-endpoint` and `data-token` (a dev token, or a panel token your
backend mints per page via `POST /api/panel-tokens`). No Studio
answering: the panel removes itself silently. The artifact is built by
`studio/web/vite.panel.config.ts` (a separate library-mode build), the
committed `studio/dist/panel/panel.js`, 88,514 B raw / 23.9 KiB gzip;
`make studio-panel-asset` stages it as `panel-<version>.js` + sha256
for non-Go backends.

## The playground (WEFT-PLAYGROUND.md)

Your app dials Studio out and executes experiment commands as runs of
the agents it registers — setup A is five lines
([runtime/examples/local](../runtime/examples/local) is the runnable
demo behind the P0 curl gate):

```go
defer runtime.Install(runtime.Local(srv), // or runtime.Studio(url, tok)
    runtime.Agents(support), runtime.Limits(runtime.Budget{MaxTokensPerExperiment: 200_000}),
    runtime.AllowSideEffects("send_email"))() // a write tool; a read is weft.Replay(weft.ReplaySafe)
```

Studio's side is `studio.New(..., studio.Playground(true))`, which
serves `GET /api/runtimes` (the connected runtimes),
`POST /api/playground/runs` (the §10.4 validation table: 400 unknown
tool/model names and unsupported 8b modes, 403 raised limits or a
refused side-effect tool, 404 unknown runtime/agent/source run, 409 a
reused command id, 503 with no runtime connected) and
`GET /api/playground/commands/{id}`, plus the runtime link's own
routes (`POST /api/runtime/register`, `GET /api/runtime/commands` SSE,
`POST /api/runtime/acks`). Safety: off unless `WEFT_ENV=dev` or
`runtime.Enabled(true)`; overrides only narrow; a side-effect tool's
call is substituted with its recorded result or parked
(`weft.Replay(weft.ReplaySafe)` vouches a read, `AllowSideEffects`
opts a tool into allow mode); budgets cap each experiment; the app's
own runs are never touched.

P1–P5 ride the same command: `transcript_edits` (validated on both
sides — a patch names a call in the kept prefix, the prefix ends at a
step boundary with every call answered), `engine: scripted` (the
source run's recorded turns at zero tokens; scripted + an
instructions/model override is refused — the §5.5 prompt trap), thread
`fork` (a new session with lineage the panel can keep chatting in),
`POST /api/runs/{id}/approvals` (a parked run's continue/skip/resolve
— ADR 0007's own verbs), `POST /api/playground/fixtures` (the run's
records as wefttest replay fixtures), and `GET/POST /api/experiments`
with `GET /api/experiments/{id}` (the saved groups, PQ4). The
debugger's rungs 3–4 act on runtime-started runs only (D7, PQ7):
`PUT /api/runtimes/{id}/breakpoints` (capability `breakpoints`) and
`POST /api/runs/{id}/steer` (capability `steer`) — `meta.debug_scope`
says so. The panel's experiment drawer (the §3 form, the live result
with the inline diff, the approvals, the 2-way compare) and the
Studio `/playground` page (variants side by side with the metrics, the
E9 variants × inputs matrix, the experiment history) both render them,
each control only for its reported capability.

The runs list (light): status as a dot and a word, the error under a
failed run's id, session/public-id/experiment filters that mirror the
URL, the session column linking each turn to its thread, and a follow
toggle that streams an agent's runs as they happen:

![runs list, light theme](screenshots/runs-light.png)

The run page (light) — the prompt and the answer first (the answer now
comes from the transcript: deltas are live-only), then the facts; the
**trace** draws on the **time axis** when the run has spans (real
durations, `?axis=` picks; the position axis keeps the replay
playhead), and subagent children expand lazily — their events are
their own runs', fetched on demand:

![run page with the trace, light theme](screenshots/run-light.png)

Sessions list a thread's turns in order with usage and the newest
status; `/live` shows everything streaming right now; `/traces/{id}`
renders any trace — a stock OTel GenAI app's included — as a span tree
plus a chat view when semconv content was captured.

Every view is a URL: filters, `view=story|raw` (and `raw=doc`), the
selected span `sel` and detail mode `d`, the axis, the selected step,
and the replay position `t` all live in search params. Keyboard:
`⌘K` jumps to any recent run, `/` filters, `j`/`k` move, `enter`
opens, `e`/`s`/`r` switch trace/story/raw, `space` replays, `[`/`]`
jump by step or tool event, `,`/`.` move one event, `?` lists
everything. Keys fire only on a bare press outside a text box.

## The API (S4.2/S4.3)

JSON under `{base}api/`: `meta` (versions, the DB kind,
`ingest_open`, `interrupted_after_ms`, capabilities), `runs` (agent,
status, session, public id, playground, parent and `tag.<k>=<v>`
filters, cursor-paged),
`runs/{id}` (the row and the subagent children — never events; with
`instructions_hash`, `catalog_hash`, `request_count` and, for a run
written before ADR 0028, `requests_badge: "not_recorded"`),
`runs/{id}/events?after=&limit=` (the paged durable stream),
`runs/{id}/transcript` (the messages bodies), `runs/{id}/spans`,
`runs/{id}/requests?step=&from=&limit=&refs=1` (one row per model-call
attempt, the prompt and catalog its hashes name inline unless `refs=1`,
`next_from` while pages are full) and `runs/{id}/tools` (the run's
catalogs by hash) — both under the `requests` capability, refused to a
read-scoped panel token (403, `badge: "hidden"`), and badged
`not_recorded` / `stripped` rather than empty when there is nothing to
show for a reason (a child run id whose last segment is itself one of
these sub-route names — `events`, `transcript`, `spans`, `requests`,
`tools` — is shadowed by the route; provider call ids never collide),
`runs/{id}/steps/{n}` (one step assembled server-side, `n` its ordinal,
under the `steps` capability: the step's status, timing, the model
requested and the one that answered, attempt 1's request row, every
attempt with its model and outcome, `messages_in`, its events, its tool
calls with their spans and results, the child runs they started, usage,
the compaction view it saw and `holes` — every absent block a badge
with its reason and fix; scoped like `events`, except that a read-scoped
panel token gets the request block as `{badge: "hidden", …}` inside a
200; a step past the run's last, or not yet started, is 404),
`traces/{trace_id}` (any trace), `sessions`, `sessions/{id}` (turns in
order), `public/{public_id}`, `manifest`, `POST /api/panel-tokens`
(mint; the panel's scoped tokens — see the devtools panel above), and
`GET /api/live` — the SSE
stream whose frame ids are the hub's Seq: exactly one selector
(`run`/`session`/`public_id`/`agent`), `kinds` over
event/delta/messages/run (default `event,run`; deltas are opt-in,
heartbeats never), a ping every 15 s, `Last-Event-ID` resume with the
gap backfilled from the database and deduped on `(run, kind, pos)`,
and `event: overflow` when a slow subscriber's queue drops it. OTLP
ingest is `POST /v1/traces` and `/v1/logs` (protobuf and JSON, gzip,
16 MiB after decompression, publish-then-write, 503 on a write failure
so the exporter retries). Under `Playground(true)` the playground's
routes join (`GET /api/runtimes`, `POST /api/playground/runs`, `GET
/api/playground/commands/{id}`, `POST /api/runs/{id}/approvals`,
`POST /api/playground/fixtures`, `GET/POST /api/experiments`, `GET
/api/experiments/{id}`, `PUT /api/runtimes/{id}/breakpoints`, `POST
/api/runs/{id}/steer` — see the playground above) beside the runtime
link's own (`POST /api/runtime/register`, `GET /api/runtime/commands`
SSE, `POST /api/runtime/acks` — server token only, never a panel
token); `/panel.js` serves the devtools panel bundle — static and
unauthenticated.

Capabilities are computed from the registered route groups
(`routes.go`) — never hard-coded: `live`, `ingest`, `auth` (with a
token), and `runtimes`, `breakpoints`, `steer` + `playground` under
`Playground(true)`, plus anything a hosting wrapper declares with
`studio.Capabilities(…)`.
The panel group registers always on and names no capability;
`panel.go` and `playground.go` add their groups through the package's
hooks — nothing edits `routes.go`.

Errors are `{"error": {"code", "message"}}` with the codes `not_found`,
`bad_request`, `unauthorized`, `forbidden`, `method_not_allowed`,
`conflict`, `unsupported`, `unavailable`, `internal`.

## Contributing to the UI

The web app lives in [web/](./web) — TanStack Start in SPA mode,
React, shadcn/ui on Tailwind v4, built with Bun. Users never need it:
the build output is committed at [dist/](./dist) and embedded with
`go:embed`.

```sh
make studio-build   # cd studio/web && bun install && bun run build
make studio-check   # rebuild, prove dist is fresh, check the 600 KiB gzip budget, typecheck + test
```

`make studio-check` is the freshness gate (ADR 0018 §4): it fails if
`dist/` does not match `web/` or if the gzipped total exceeds 600 KiB
(currently ~371 KiB: the app's ~347 plus the panel bundle's ~24).
The build is deterministic — two builds from
one tree are byte-identical (`scripts/clean-dist.ts` pins the router's
prerender timestamp and keeps `<base href>` first in `<head>`).

The dev loop is two terminals: `go run ./studio/examples/basic
-serve` (API + embedded UI on :7331) and `cd studio/web && bun run
dev -- --base /studio/` (Vite on :3000 proxying `/studio/api`).

Import path: `github.com/weftgo/weft/studio`, a package of the
framework module (`go get github.com/weftgo/weft`); it imports `core`,
`obsdb` and, for its tests, `otel`. The binary is [cmd/](./cmd)
(`go install github.com/weftgo/weft/studio/cmd@latest`), the one
place that imports the clickhouse driver.
