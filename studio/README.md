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

**B · local binary** — any language's app, several services:

```sh
go run ./studio/cmd                    # UI + OTLP + SQLite + dev token on 127.0.0.1:7331
WEFT_STUDIO_URL=http://127.0.0.1:7331 ./my-go-app
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:7331 python app.py
```

The dev token is printed at start (`WEFT_STUDIO_TOKEN` or `--token`
fixes it); ingest is open on loopback; `--db sqlite://path` picks the
file (`clickhouse://` arrives with step 6b).

**C · hosted** — the same handler behind `studio.Token`: the panel's
scoped tokens are HMAC-signed `{public_id, scope, exp}` minted by your
backend through `POST /api/panel-tokens`; every data route refuses
anything outside the token's public id.

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
status, session, public id, playground and tag filters, cursor-paged),
`runs/{id}` (the row and the subagent children — never events),
`runs/{id}/events?after=&limit=` (the paged durable stream),
`runs/{id}/transcript` (the messages bodies), `runs/{id}/spans`,
`traces/{trace_id}` (any trace), `sessions`, `sessions/{id}` (turns in
order), `public/{public_id}`, `manifest`, `POST /api/panel-tokens`
(mint; the panel itself is step 7), and `GET /api/live` — the SSE
stream whose frame ids are the hub's Seq: exactly one selector
(`run`/`session`/`public_id`/`agent`), `kinds` over
event/delta/messages/run (default `event,run`; deltas are opt-in,
heartbeats never), a ping every 15 s, `Last-Event-ID` resume with the
gap backfilled from the database and deduped on `(run, kind, pos)`,
and `event: overflow` when a slow subscriber's queue drops it. OTLP
ingest is `POST /v1/traces` and `/v1/logs` (protobuf and JSON, gzip,
16 MiB after decompression, publish-then-write, 503 on a write failure
so the exporter retries).

Capabilities are computed from the registered route groups
(`routes.go`) — never hard-coded: `live`, `ingest`, `auth` today, plus
anything a hosting wrapper declares with `studio.Capabilities(…)`.
Later steps add their groups in their own files (`panel.go`,
`playground.go`) through the package's group hooks; nothing edits
`routes.go`.

Errors are `{"error": {"code", "message"}}` with the codes `not_found`,
`bad_request`, `unauthorized`, `forbidden`, `conflict`, `unsupported`,
`unavailable`, `internal`.

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
(currently ~329 KiB). The build is deterministic — two builds from
one tree are byte-identical (`scripts/clean-dist.ts` pins the router's
prerender timestamp and keeps `<base href>` first in `<head>`).

The dev loop is two terminals: `go run ./studio/examples/basic
-serve` (API + embedded UI on :7331) and `cd studio/web && bun run
dev -- --base /studio/` (Vite on :3000 proxying `/studio/api`).

Go module: `github.com/weftgo/weft/studio`, requiring the tagged
`weft` and (until they tag, step 8's release) `weft/obsdb` through a
directory `replace` that the release step drops — standalone-importable
after that, no `replace`. The binary is its own module
([cmd/](./cmd)), the only place that will import the clickhouse
driver (step 6b).
