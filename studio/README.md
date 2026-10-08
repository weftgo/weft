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
go install github.com/weftgo/weft/cmd/weft@latest
weft studio                            # UI + OTLP + SQLite + playground + dev token on 127.0.0.1:7331
./my-go-app                            # otel.Install() finds it through the discovery file
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:7331 python app.py
```

The dev token is stable per database — `<db>.token` beside the SQLite
file, 0600, never printed (`--rotate-token` renews it;
`WEFT_STUDIO_TOKEN` or `--token` overrides it); a Go app with
`otel.Install()` / `runtime.Install()` finds the running Studio through
the discovery file `studio.json` (`./.weft`, `$XDG_RUNTIME_DIR/weft`,
the user cache directory; `WEFT_STUDIO_URL` always wins,
`WEFT_DISCOVERY=off` ignores it); ingest is open on loopback;
`127.0.0.1:7331` is the one default port: started again on the same
database, the binary finds the running Studio through `/api/meta`
(`db.path`, `pid`, read with the stable token — sent only to an
address a trusted discovery file names, so from another directory with
`--db` on the same file the probe goes out bare and a second Studio
starts) and reuses it (`studio
already running at … (pid n), reusing`, exit 0); anything else on the port
moves it to the next free one in 7331–7340 with one line saying so.
`--addr` (or `WEFT_STUDIO_ADDR`) pins: a busy pinned address is an
error, never a fallback; `--db sqlite://path` picks the
file, `--db clickhouse://user:pass@host:9000/db` the hosted backend
(`obsdb/clickhouse`, the one place the driver is imported). The
playground is on (`studio.Playground(true)`), inert until an app's
`weft/runtime` connects; `--no-playground` turns it off. The manifest
is `--manifest` (`WEFT_MANIFEST`), else the nearest `weft.json` from
the working directory upward — one line says which file, or that none
was found. `--open` (default on when stdout is a terminal) opens the
UI with the token in the URL fragment.

The same binary is the API over a terminal, each subcommand one
route, `--url` (`WEFT_STUDIO_URL`) and `--token` (`WEFT_STUDIO_TOKEN`)
picking the Studio: `weft runs [--agent] [--since] [--failed] [--limit]
[--json]` (`GET /api/runs`; a limit that hid runs says so on stderr),
`weft open <run id> [--open] [--with-token]` (checks `GET
/api/runs/{id}`, prints the bare `<url>/runs/<id>`; the token goes only
to the browser, or to stdout on `--with-token`), `weft export <run id>
[--format json|jsonl|otlp]` and `weft export <run id> --wefttest
./testdata [--test TestName] [--force]` (`GET /api/runs/{id}/export`,
the fixtures unzipped where `wefttest.Replay(t, "testdata")` reads
them; `--force` replaces a non-empty target's fixtures), `weft doctor`
(`GET /api/meta`).

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

    <script type="module" src="/studio/panel.js" data-weft data-public-id="pub_…"></script>

Rung 1 is a viewer scoped to that conversation: the turns (parked
shown), the step story with tool calls, usage splits, each step's
attempts and timing ("attempt 4 of 4 · fallback to glm-b", "1.2 s ·
ttft 180 ms"), approvals
read-only, truncation/gap/stripped honesty, the raw JSON, a live tail,
⤢ deep links into Studio, lazy subagents (one level inline, with the
child's request line and an "open in Studio" hand-off; a grandchild is
the hand-off only) and a spans waterfall.
Deep links (plan G1) follow one scheme, `src/lib/links.ts`, shared by
the panel and Studio's app (an ESLint rule refuses a Studio URL built
anywhere else): `runs/<id>?step=<n>&view=story|raw&sel=…&axis=time&t=…`,
`sessions/<id>`, `traces/<id>?span=<span id>`, `playground#run=…` (the
hand-off in the fragment) and `playground?experiment=<id>`. `step` is
the step's ordinal — its index as the loop counts it, the `n` of
`runs/{id}/steps/{n}` — never an event position or a transcript batch
index; a call is named with its step (`sel=c:<step>:<call id>`,
`c:resume:<call id>`). ⤢ carries the step being read (else the running
one); each tool call, subagent badge, session and trace in the panel is
a link. No link carries a token, and no request does in its URL: the
token travels in the `Authorization` header, and each live stream is
opened with a grant (`POST /api/live-grant`, the bearer in the header;
the stream URL carries only the `sig`). The browser's own reconnect is
left to resume with `Last-Event-ID` while the grant lasts; one after
its 60 s gets a new grant and a fresh connection (the panel refetches
what it lists). A panel token's stream that ends with `event: expired`
is reopened only when the host has handed over a fresh token; else the
live dot goes out, history stays, nothing reaches the console.
`Alt+W` toggles (Q4), `?` lists keys, `r` flips raw. Setups B/C add
`data-endpoint` and `data-token` (a dev token, or a panel token your
backend mints per page via `POST /api/panel-tokens`). A single-page app
that switches conversations sets `window.__WEFT__ = { publicId }`
instead of `data-public-id`; the tag then carries no other knob, so
keep `data-weft` on it — it is how the panel finds its own tag (and
with it the endpoint) whatever the file is called.

Where the configuration comes from (plan C2). Each field — `endpoint`,
`public-id`, `token`, `position`, `open`, `auto` — resolves on its
own; the first source that sets it wins, so a meta tag can carry the
token while the script tag carries the endpoint. The panel never reads
its own file name: a bundle served as `/assets/devtools.abc123.js`
behind a proxy configures itself the same way.

| # | Source | The explicit form |
|---|---|---|
| 1 | `mount(opts)` — a programmatic mount (`import { mount } from "@weftgo/devtools"`, the npm entry; not in the script-tag bundle) | `mount({endpoint, publicId, token, position, open, auto, target})` |
| 2 | the `<weft-devtools>` element's attributes | `<weft-devtools data-endpoint="…" data-token="…">` |
| 3 | meta tags | `<meta name="weft:endpoint" content="…">` (also `weft:public-id`, `weft:token`, `weft:position`, `weft:open`, `weft:auto`) |
| 4 | the panel's `<script>` tag: the running classic script, else the first with a `data-weft` attribute (any `src`, any value), else the first carrying one of the `data-*` attributes above | `<script type="module" src="…" data-weft data-endpoint="…">` |
| 5 | `panel-config.json` beside the script (`/studio/panel.js` → `/studio/panel-config.json`), asked only when rungs 1–4 named no endpoint; its `endpoint` is taken on the script's own origin only, and it never carries a token | name the endpoint at any rung above |
| 6 | the script's own origin + directory (setup A) | name the endpoint at any rung above |

A renamed bundle whose tag carries none of the panel's `data-*`
attributes needs `data-weft` on it (or a higher rung); without a tag to
find, the endpoint is the page's own directory.

A `weft:token` meta tag, like `data-token`, is a token in the page's
source: in HTML you ship, use a per-page panel token your backend
mints (`POST /api/panel-tokens`), never a dev or server token.

No Studio answering: the dock the script mounted by itself removes
itself silently — no console, at most two requests (`panel-config.json`,
then meta; `panel-config.json` gives up after 3 s). A mount you made (a
`<weft-devtools>` element in your markup, `mount(opts)`, or
`data-auto="false"`) shows one quiet line instead, `Studio not
reachable at <endpoint> · retry`, where `retry` asks again (the line
reads `checking…` while it does). The
artifact is built by
`studio/web/vite.panel.config.ts` (a separate library-mode build), the
committed `studio/dist/panel/panel.js`, 111,622 B raw / 30.8 KiB gzip;
`make studio-panel-asset` stages it as `panel-<version>.js` + sha256
for non-Go backends.

The same file is on npm as `@weftgo/devtools`, for apps that bundle
everything and ship no `<script>` tag (Vite, Next, SvelteKit). This
is a second delivery, not a replacement: the handler above is still the
canonical install. `import "@weftgo/devtools"` loads the package's
`panel.js`, which has the same sha256 as `/studio/panel.js` of the same
version, and so does what the tag does. Beside it are a thin typed
module (`mount`, `scope`, `open`, `close`, `toggle`, `on`,
`serializeScope`/`parseScope`) and the `/react`, `/vue` and `/svelte`
helpers, which set the `data-weft-scope` marker and are not components.
The package has zero runtime dependencies and its version is the weft
version. `studio/web/npm/README.md` has the API table, including which
exports C4 completes. `make devtools-npm` assembles the package in
`studio/web/npm`, runs the package suite against the assembled files
and lists the tarball. `make studio-check` fails if the package's
`panel.js` is not the served one. Publishing is done by hand.

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
records as wefttest replay fixtures; refused to a read-scoped panel token, 403 with badge `hidden`; 409 with its badge when there is nothing to fixture), and `GET/POST /api/experiments`
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
playhead), and subagent children sit as nested rows under the step
that called them (agent, status, usage, holes, a link) and expand
lazily — their events and their request record (the child's prompt,
read by the child's id) are their own runs', fetched on demand:

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

JSON under `{base}api/`: `meta` (versions, `db` {kind, path, size —
path and size to loopback and the server token only}, `pid`,
`ingest_open`, `interrupted_after_ms`, capabilities, `capabilities_off`
{capability: why}, `manifest_sources`), `runs` (agent,
status, session, public id, playground, parent and `tag.<k>=<v>`
filters, cursor-paged; top-level only by default — `parent=<run id>`
lists one run's subagent children, `all=1` (or `parent=*`) every run,
children included),
`runs/{id}` (the row and the subagent children, each child row with its own `holes` — never events; with
`delta_count` (the streamed deltas, counted and never stored),
`instructions_hash`, `catalog_hash`, `request_count` and, for a run
written before ADR 0028, `requests_badge: "not_recorded"`; and
`compactions` — ADR 0028 §8's run-scope views `{scope: "run", index,
step, from_seq, to_seq, hash, replaced, entries}` and thread's session
markers `{scope: "session", hash, replaced, entries, tokens_before?,
tokens_after?, reason}`, counts and hashes only, `[]` when none),
`runs/{id}/events?after=&limit=` (the paged durable stream),
`runs/{id}/transcript` (the messages bodies), `runs/{id}/spans` (for a
read-scoped panel token — here, in `traces/{trace_id}` and in the
export alike — without the tool names a run's overrides put on its
invoke_agent span: `weft.override.tools`, `park_on`,
`park_all_except`, and a named `tool_choice` cut to its mode),
`runs/{id}/requests?step=&from=&limit=&refs=1` (one row per model-call
attempt, the prompt and catalog its hashes name inline unless `refs=1`,
`next_from` while pages are full) and `runs/{id}/tools` (the run's
catalogs by hash) — both under the `requests` capability, refused to a
read-scoped panel token (403, `badge: "hidden"`), and badged
`not_recorded` / `stripped` rather than empty when there is nothing to
show for a reason (a child run id whose last segment is itself one of
these sub-route names — `events`, `transcript`, `spans`, `requests`,
`tools`, `export`, `logs` — is shadowed by the route; provider call ids never collide),
`runs/{id}/logs?from=&limit=&severity=` (the app's own log lines — the
non-weft records an `slog` bridge or the OTel Logs API emitted under one
of the run's spans, or an app span below one — in time order, `{index,
time, severity, severity_number, body, attrs, span_id?}`, `next_from`
while pages are full; `severity` keeps a level and above without
renumbering; under the `logs` capability; `holes: [{hole, reason,
fix?}]` lists every hole of the page (`[]` when none) and
`badge`/`reason`/`fix` repeat the first: `not_recorded` (a run recorded
without a tracer), `truncated` (the run's traces hold more than 10 000
log lines in its window — the cap applies before attribution, so a
flooding subagent or sibling counts, and later lines of this run may be
missing), `gap` (lines in the run's trace and window name a span that
is not stored — dropped, or still open — and may be this run's or
another's); a running run's page is `partial: true` with a
`partial_reason` — lines under in-flight spans appear once those spans
end, and indexes may shift — a live condition, not a hole, so
`partial` can appear with no badge (then `reason` carries it too); app logs may carry prompts, so a
read-scoped panel token is refused them, 403 with `badge: "hidden"`),
`runs/{id}/steps/{n}` (one step assembled server-side, `n` its ordinal,
under the `steps` capability: the step's status, timing, the model
requested and the one that answered, attempt 1's request row, every
attempt with its model and outcome, `messages_in`, its events, its tool
calls with their spans and results, the child runs they started, usage,
the compaction view it saw and `holes` — every absent block a badge
with its reason and fix; scoped like `events`, except that a read-scoped
panel token gets the request block as `{badge: "hidden", …}` inside a
200; a step past the run's last, or not yet started, is 404),
`runs/{id}/export?format=json|jsonl|otlp|wefttest` (the whole run as
one download, `Content-Disposition: attachment; filename="<run
id>.<ext>"`, under the `export` capability: `json` is one document —
the run as `runs/{id}` serves it, every event, the transcript batches,
the compactions, the request records with the prompts and catalogs
they name keyed by hash, the spans and `holes`, every absent block
badged; `jsonl` is the same records one per line, each with a
`"record"` kind, in a stable order; `otlp` is
`{"logs": ExportLogsServiceRequest, "traces": ExportTraceServiceRequest}`
in OTLP/JSON, which `POST /v1/logs` and `/v1/traces` read back into the
same run; `wefttest` is the run's model calls as replay fixtures,
zipped flat — unzip into `testdata/<TestName>/` and `wefttest.Replay`
answers, a step whose request carried a compaction view keyed on it
and noted `compacted_at`. The run block's children carry their `holes`
as `runs/{id}` serves them. A read-scoped panel token gets `json` and
`jsonl` with the request block `{badge: "hidden", …}` and each
compaction view's `messages` null under the same badge; `otlp` and
`wefttest` are 403 with that badge),
`traces/{trace_id}` (any trace), `sessions`, `sessions/{id}` (turns in
order), `public/{public_id}`, `manifest` (it carries the agents' system
prompts: 403 with `badge: "hidden"` to a read-scoped panel token),
`POST /api/panel-tokens`

(mint; the panel's scoped tokens — see the devtools panel above), and
`GET /api/live` — the SSE
stream whose frame ids are the hub's Seq: exactly one selector
(`run`/`session`/`public_id`/`agent`), `kinds` over
event/delta/messages/run (default `event,run`; deltas are opt-in,
heartbeats never), a ping every 15 s, `Last-Event-ID` resume with the
gap backfilled from the database and deduped on `(run, kind, pos)`,
and `event: overflow` when a slow subscriber's queue drops it. A token
travels in the `Authorization` header only — `?token=` is refused on
every `/api` route, in every setup (401 naming the grant) — so a
browser's `EventSource`, which cannot set headers,
opens the stream with a grant: `POST /api/live-grant` (authenticated
like every route; the selector and `kinds` as a JSON body
`{"run":"r_1","kinds":"event,run"}` or as the query, not both) answers
`{sig, exp}`, and `GET /api/live?run=r_1&kinds=event,run&sig=<sig>`
opens that one stream as the identity that asked. The sig is an
HMAC-SHA256 over that identity (the server token, a panel token's
public id and scope, or setup A's open API), the selector, the kinds
set (order-free) and `exp` — 60 s, never past a panel token's own
expiry; another selector, another kinds set, a tampered or an expired
sig is 401, and a panel token is refused a stream outside its public
id at grant time (403), as the stream would refuse it. Without a
`Token` the key is random per process: one mechanism in every setup.
A grant opens one stream within 60 s; a panel token's stream (opened
with a grant or with the bearer) ends at the token's expiry with one
final `event: expired` frame (`data: {}`) and closes; a server
token's does not (nor setup A's). After `expired`, reopen only with a
bearer that is still valid — a new panel token, then a new grant.
Both clients do exactly this (`lib/live.ts`'s `openLive` for the UI,
`openPanelLive` for the panel): a grant before every connection, the
browser's own `Last-Event-ID` reconnect kept inside the grant's 60 s,
a new grant (and a fresh connection, refetched) after it or after a
refused stream, and after `expired` a new grant only for a fresh
bearer. A fresh connection carries no resume cursor — the header is the
only way `Last-Event-ID` is read — so the clients refetch pages
instead. The UI's export link carries no token: it opens as is in
setup A.
The `#token=` fragment `weft open` and `weft studio --open` hand the
browser never reaches the server; the UI reads it. OTLP
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
unauthenticated; `/panel-config.json` (`{endpoint, version,
capabilities}`, capability `panel-config`) answers a loopback (or
`AllowOrigins`) Host and a same-origin, `AllowOrigins` or loopback
Origin only, else 404.

Capabilities are computed from the registered route groups
(`routes.go`) — never hard-coded: `live` (the stream and its grant),
`ingest`, `auth` (with a
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
(currently ~388 KiB: the app's ~358 plus the panel bundle's ~30).
The build is deterministic — two builds from
one tree are byte-identical (`scripts/clean-dist.ts` pins the router's
prerender timestamp and keeps `<base href>` first in `<head>`).

The dev loop is two terminals: `go run ./studio/examples/basic
-serve` (API + embedded UI on :7331) and `cd studio/web && bun run
dev -- --base /studio/` (Vite on :3000 proxying `/studio/api`).

Import path: `github.com/weftgo/weft/studio`, a package of the
framework module (`go get github.com/weftgo/weft`); it imports `core`,
`obsdb` and, for its tests, `otel`. The binary is
[cmd/weft](../cmd/weft) (`go install
github.com/weftgo/weft/cmd/weft@latest`, `make studio-bin` in this
repo), the one place that imports the clickhouse driver.
