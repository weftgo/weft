# weft studio — the Inspector

The UI, the JSON API, the live stream and the OTLP receiver over one
[obsdb](../obsdb) database: the runs list, the run page (steps, tool
calls and results, subagents lazy, truncation badged, replay over the
event index, the run tree on a time scale when the run has spans), sessions
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
a proxy, the `Host` the proxy forwards is the one checked. A proxy that
rewrites `Host` (Vite's `changeOrigin: true`) must list its front origin
in `AllowOrigins`, since the `Origin`'s `host:port` is compared with the
`Host`. The UI shell
and `/panel.js` carry no data and are not checked; with a `Token` the
bearer is the defence and the `Host` does not matter.

The loopback `Host` stops a rebound page, not a cross-site one: any
site's page can POST to `localhost` without a preflight. So setup A
also refuses a **cross-origin write** — an unsafe method (anything but
GET, HEAD, OPTIONS) whose `Origin` is neither one whose `host:port` is
the request's `Host` nor an `AllowOrigins` entry, or that sends no
`Origin` but `Sec-Fetch-Site: cross-site` — with a 403 "cross-origin
write refused (setup A)" naming the Origin. Same-origin pages, clients
that send neither header (curl, the runtime link) and `AllowOrigins`
origins are let through; reads need no rule, since setup A sends no
CORS headers and the browser hides a foreign page's answer.

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
picking the Studio: `weft runs [--agent] [--since] [--failed] [--status] [--limit]
[--json]` (`GET /api/runs`; a limit that hid runs says so on stderr),
`weft open <run id> [--open] [--with-token]` (checks `GET
/api/runs/{id}`, prints the bare `<url>/runs/<id>`; the token goes only
to the browser, or to stdout on `--with-token`), `weft export <run id>
[--format json|jsonl|otlp]` and `weft export <run id> --wefttest
./testdata [--test TestName] [--force]` (`GET /api/runs/{id}/export`,
the fixtures unzipped where `wefttest.Replay(t, "testdata")` reads
them; `--force` replaces a non-empty target's fixtures), `weft doctor`
(`GET /api/meta`), and `weft mcp` — the same read routes as a stdio MCP
server for a coding agent, GET only (tools `runs`, `search`, `run`,
`step`, `request`, `events`, `tools`, `diff`, `export`, `sessions`,
`session`, `meta`, and `explain_failure`, a composition of `run` +
`step` + the transcript; outputs the route's JSON, cut at `max_bytes`
with a note; with no `--url` it reads the discovery file). `claude mcp
add weft -- weft mcp`; Cursor's `.cursor/mcp.json`:
`{"mcpServers":{"weft":{"command":"weft","args":["mcp"]}}}` — the
discovery file finds the running Studio; `--url http://127.0.0.1:7331`
names another. The recipe, with worked examples and troubleshooting:
[docs/mcp-recipe.md](../docs/mcp-recipe.md).

**C · hosted** — the same handler behind `studio.Token`: the panel's
scoped tokens are HMAC-signed `{public_id, scope, exp}` minted by your
backend through `POST /api/panel-tokens`; every data route refuses
anything outside the token's public id. A run the token may read can
have child runs that carry another public id, or none (a nested
`Generate` or thread turn inside a tool handler, a pool child session,
a child whose `Metadata` cleared `weft.public_id`): every route that
reaches runs through another — the run document's `children[]` and the
export's run block, a step's `children` and its calls' `child_run_id`,
every row's `children_count` / `children_failed` (runs list, search, a
session's turns, the live lane's run frames), `?tree=1` on spans and
the export — keeps the token's public id's children alone (a child row
must carry it, the tree's rule) and says the rest as one `hidden` hole
with a `count` (the run document's and the step's `holes`, the tree
export's `subagent_holes`), never by id, public id or prompt. The
run's own cost still takes their usage out (the core rolled it in);
its delegated figure is not known to the token (`delegated_cost_known:
false`, the reason counting the runs it cannot read). A token is read-only unless
minted with `"playground": true` — a read-scoped one neither acts nor
reads the agents' system prompts (`/api/manifest`, the runtimes'
instructions) — and experiments (`experiment_id` included) are the
server token's alone. "Open in Studio" from the panel (⤢, `g s`) under a
playground-scoped panel token, when `/api/meta` lists `handoff`, asks
`GET /api/panel-handoff?to=` — the deep link's path under the base — with
the bearer and `Accept: application/json` and opens the answer's
`location`, a path under the base resolved against the endpoint (never
the host page) — its fragment carries a 5-minute read token for the same
public id, so the hosted Studio opens on the run without a token pasted.
That handed-over token (any panel token in a `#token=`) serves its tab
alone: it lives in that tab's `sessionStorage` (kept across a reload; a
duplicated tab inherits it) and never replaces the dev token a browser keeps in
`localStorage`, which every other tab goes on using. Settings shows it
as "this tab only" and its clear drops it alone; when Studio refuses it
(its 5 minutes are over) the tab falls back to the stored token, or the
token prompt says the hand-off token expired.
The tab opens after the hand-off answers; a browser that blocks a popup
after a slow answer leaves the plain link on the note line (put there
once the answer takes over a second). Without the
capability, under a read-scoped panel token (its Studio page is already a
read view), the dev token or none, the plain link opens; a refusal opens
the plain link too (a 403 or 404 silently, anything else with the note
line). The host API's `studioLink()` stays the plain URL.

**Retention.** Studio keeps everything until told otherwise.
`studio.Retain(720*time.Hour)` — `--retain 720h` or `WEFT_RETAIN=720h`
on `weft studio` and `weft dev` — starts a sweeper that deletes, as
`DELETE /api/runs/{id}` does, every run that is not running
(succeeded, failed, interrupted) last seen longer ago than the
retention, with its children that are not running: once at start, then
every `min(retention/10, 1h)`, at least a minute apart. Interrupted
counts as not running and is swept: a run silent for 30 s reads
interrupted, which is why the retention must be at least `1h` (`New`
panics below it; `--retain` / `WEFT_RETAIN` under `1h` is a usage error,
exit 2, on `weft studio`, `weft dev` and `weft vacuum`) — a shorter one
would sweep a live run paused at a breakpoint. A running child of a
swept parent stays and is listed only under `all=1` / `parent=*` until
it is swept itself. Each sweep logs one Debug line with its count; a
failed one logs a Warn line and the server keeps serving. The sweeper
runs until `Server.Close`, so a Studio with `Retain` is built with
`studio.New` and closed; under `studio.Handler(...)` nothing can close
it, and it stops (one Debug line) only once the DB is closed. The sweep
runs in batches of 500 root runs (`obsdb.SweepBatch`), one writer
transaction each on SQLite, so ingest proceeds between them. `/api/meta`'s
`retention` reports it. `weft vacuum [--db sqlite://path|clickhouse://…]
[--retain d]` is the one-shot form — one sweep when a retention is
given, then VACUUM (SQLite: the file shrinks; it waits on the busy
timeout while another process writes) or `OPTIMIZE TABLE … FINAL` on
the tables deletions mutate (ClickHouse: every part merged — heavy on a
large database) — and `make vacuum` runs it on `$WEFT_DB`, else
`./.weft/weft.db`. All of it touches Studio's tables only. On
ClickHouse deletion is a lightweight `DELETE FROM … WHERE` (ClickHouse
23.3 or later) and the tables' TTLs (`clickhouse.TTL`) stay as they are:
retention is additive.

**Cost (plan A5).** Studio prices usage at read time — never stored,
never sent, nothing fetched. The table is a plain JSON file, USD per 1M
tokens:

```json
{"dated": "2026-10-10", "note": "…", "models": [
  {"provider": "anthropic", "model_glob": "claude-opus-5-5",
   "input": 4, "output": 20, "cached_input": 0.2, "cache_write": 5}]}
```

A run's model is looked up as `provider/model` against each row's
`provider/model_glob` with `path.Match` semantics; the first match wins,
so the most specific row comes first. A row with no `provider` (or
`"provider": "*"`) matches on its `model_glob` alone, whatever the
transport — `{"model_glob": "kimi-k2.7-code", …}` prices that model reached
through the openai adapter at an OpenAI-compatible base URL, or under any
provider name (a `*` in the glob never crosses a `/`). A scripted model no
row matches — every `wefttest` model and the playground's scripted engine
(`weft/runtime` + `scripted`), nothing else (a provider that wraps other
models ships its own row) — is an exact `$0` by a rule in Studio, not a
row (so an override file cannot lose it): `cost_known`
true, `cache` `exact`, `cost_badge` `scripted`. `input` and `output` are required;
a missing `cached_input`, `cache_write` or `reasoning` prices those
tokens at the input (or output) rate. The default is `pricing.json`
(`studio.DefaultPricing`), generated by `go generate ./studio/...` from
`internal/pricingdata` with the day its rates were read; models whose
price has a long-context tier (Claude Haiku 5.5, Gemini 2.5 Pro) and any
the author was unsure of are left out — they read unpriced, never a
guess. `studio.Pricing(table)` replaces it, as does
`WEFT_PRICING=<path>` (read by `New` when no `Pricing` is given) or
`--pricing <path>` on `weft studio` and `weft dev` (the flag wins);
`studio.LoadPricing` reads such a file and the override replaces the
default whole — start from `pricing.json`, the template. A file that
does not load (does not parse, an unknown field, a missing `model_glob` or `input`, a
negative rate, a bad glob, a `dated` that is not `YYYY-MM-DD`) stops
Studio with an error naming the path and the field; it never falls back.

The formula, exact where the adapter reports `core.Usage`'s splits
(subsets of the totals): `input × (InputTokens − CachedInputTokens −
CacheWriteTokens) + cached_input × CachedInputTokens + cache_write ×
CacheWriteTokens + output × (OutputTokens − ReasoningTokens) + reasoning
× ReasoningTokens`, ÷ 1,000,000, rounded to 6 decimals on the wire. With
no cache split reported every input token prices at the input rate and
the row says `cache: "not_reported"`. Every priced object carries `cost_usd` (null when
unknown, never 0), `cost_known`, `cache` (`exact` | `not_reported`) and, when
unknown, `cost_badge` (`unpriced`: no row matches — the reason is one line
naming `provider/model` alone, "openai/x has no price"; `unknown`: the figure
could not be computed) with `cost_reason`; `scripted` marks a known `$0`
(above). A figure that sums runs — a delegated figure, a session's, an
experiment's — says why it is unknown in one line counting them ("2 of 9
turns unpriced: no price for openai/x, acme/y", "1 of 3 subagent runs'
cost not known yet (…)"), never a clause per run, and `unpriced_models`
(a run row's `delegated_unpriced_models` for its subagents) lists the
models without a price behind a figure, deduped, so a page can say it once
— five at most, a sixth and more one last element `"(+n)"` (the reason's
list is cut the same way). A session's or an experiment's reason names no
run id (its turns are the rows beside it); a delegated reason names the
child. Run rows — lists, documents and their `children[]`,
search hits, live `run` frames, experiment runs, the export's run block —
split the bill in two, never folded: a run's usage includes its subagent
children's (the core rolls them up), so `cost_usd` is its own model calls
(its usage minus its direct children's, at its own model's rates) and
`delegated_cost_usd` (`delegated_cost_known`, `delegated_cost_reason`) is
its descendants' own costs summed — null when any of them is unpriced,
`0` with no children. A session row's `cost_usd` is its turns' own +
delegated, where a turn is every run of the session that is not a Subagent
child of another of its runs; a `thread/pool` child session's run is a turn
of the child session, not part of the parent session's row (obsdb groups
sessions over top-level runs only, so a pool child's session does not
appear in `/api/sessions` today). The run's own figure is priced at the
run's model, so a step a Fallback or UseModel answered with another model
is priced at the primary's rate there; the step route
(`GET /api/runs/{id}/steps/{n}`) prices its own model call (step_finish's
usage) at the model that answered it, else the requested one, and its
`children[]` carry own and delegated. Only core Subagent children (run ids
`<parent>/<step>/<call>`) are subtracted from a parent's own cost; a pool
child's run and another agent's `Generate` inside a tool handler are
linked to the parent but never rolled up, so they keep their own figures.
`cache` is `exact` or `not_reported` (no cache split reported — every
input token priced at the input rate, the figure possibly overstated); a
running run's own cost, and one whose end never arrived, is unknown
("running: usage arrives at run end"). `gcp.gemini`, the span's provider
name, prices as `google`. The table's `cache_write` is the 5-minute rate
(1.25× input), so a 1-hour cache write (2×) is understated; the Gemini
rows have no `cached_input`, so cached Gemini prompts are priced at the
input rate; Claude Opus 5.5 fast mode and long-context tiers are not
modelled, and models with a long-context tier (Claude Haiku 5.5, Claude
Sonnet 4.5, Gemini 2.5 Pro) are left out. `--pricing` / `WEFT_PRICING`
paths are shown absolute in `/api/meta` and `/api/settings/pricing`. A
page of runs (the runs list, search, an experiment's runs, a run
document's children) reads its subagent trees for the own/delegated
split one `obsdb.TreeReader.ChildRuns` read per tree level, never one
per row (U9.1); a database without a TreeReader reads each run's
children one run at a time, to the same figures. An experiment's runs are priced one by one (each cell is a
run row); obsdb records no variant key on a run, so a per-variant total
is the client's sum. `GET /api/settings/pricing` (capability `pricing`,
read-only, every valid identity) answers the effective table in the
file's shape plus `source` (`default` | `file` | `option`) and the file's
`path` under `db.path`'s rule — the body saves as a `WEFT_PRICING` file
and loads back. `/api/meta`'s `pricing` is `{source, path?, dated,
models}`. The Studio UI shows cost, under capability `pricing` only, in the runs
table (a Cost column, sortable through `sort=cost` when `search` is
there too, else over the loaded rows), the run header (own · delegated),
the step cards, the compare page's cost column, the playground's result
cards and matrix (a per-variant total) and the sessions list — an
unknown figure as "—" with `cost_reason` as its tooltip, never `$0.00`; an
unpriced one with a small "unpriced" chip (the sessions list's cell is a
number or "—" alone); a scripted one as "$0 · scripted". What is unpriced
is said once per page, not per row (U7, `src/lib/cost.ts`'s
`unpricedLine`): above the runs list, above the sessions list and in the
run header, "N runs unpriced: no row for openai/x, acme/y (+2)" — the
models deduped, every one in its title — beside "price this model…", a
link to Settings' pricing table (`/settings?price=<provider/model>`) that
leads the table with a row for it, its rates to fill (an edit kept in the
browser like any other: download and restart with `--pricing` to apply). With the server cost sort on, a live frame updates its run's row in place
without re-sorting until the next refetch; load more re-reads up to 2,000
runs; and offset paging over a set that is changing can repeat or skip a
row.

## The devtools panel (WEFT-DEVTOOLS.md)

One script tag puts the run loop in the corner of your own page —
`/panel.js` is served by the same handler:

    <script type="module" src="/studio/panel.js" data-weft data-scope="pub_…"></script>

Rung 1 is a viewer scoped to that conversation: the turns (parked
shown), the step story with tool calls, usage splits, each step's
attempts and timing ("attempt 4 of 4 · fallback to glm-b", "1.2 s ·
ttft 180 ms"), approvals (read-only — or decided from the panel on
the app's own turns, below), truncation/gap/stripped honesty, the raw JSON, a live tail,
⤢ deep links into Studio, lazy subagents (inline at any depth, each
read only once opened, with the child's request line and an "open in
Studio" hand-off; past two levels the indent stops and the row says
"depth N"; a child's final words that are its call's result are drawn
once, on the call — UX-D-13) and a spans waterfall.

The step read (U10.2, the audit's §6.3): the Story's selected step —
else, unpicked, the run page's landing step: the one the run failed at
(lib/summarize.ts's failedAtStep; a step no card draws — a cancel
before it began — falls back to the last drawn, landStep), else step 0
(P8-fix-a) — opens answer first, the
run page's drawer at panel width: `lib/summarize.ts`'s verdict line
(`[data-verdict]`: what stopped the run there, the finish, tokens,
latency, first token) and its sent / replied / tools ran / then lines
(`[data-answer-line]`), the same words `parity.test.ts` holds to
Studio's drawer on opt.usage-limit; its Logs and Raw OTel hand off
(the step head's `📄 ⤢` — U11.2's `logsLink`, under capability logs,
for no token or the dev token only: the logs route refuses every panel
token —
and the answer's `Raw OTel ⤢`: the run page at the step's row,
`?sel=s<n>&tab=logs|raw`, lib/links.ts's `runLink` — no token in the
URL; a playground panel token opens it through the hand-off, as every
`[data-weft-handoff]` link does). Each step's request is one line by
default (UX-D-12: the attempts, the changed marks, the badges) and opens
on a click to the prompt, the catalog and the params — the params only
when one is not the adapter's default; the "insert a message" lines
show on hover or focus.

The views (plan D4). The open turn has four tabs, a `role="tablist"`
(`←`/`→`, `Home`/`End` move and select; one tab stop): **Story** (the
step story above, and the playground's drawer and result), **Request**
(the step's request, below), **Timeline** (the spans waterfall
at the column's full width over a time axis — ms from the run's first
span — or, on a run without spans, its steps and tool calls placed by
the event sequence, `seq`, the position in the run's event stream, a
step before its calls) and **Raw**. Its rows are `src/lib/waterfall.ts`'s
run tree (U8.1), the one row model both renderers draw:
`waterfallRows({runId, status, spans, fold, total, children, expanded}, scale?)`
gives run › step › model call (› attempt) / tool (lane) › child run on a
`time` scale (ms from the earliest span, sub-millisecond kept) or an
`order` scale (stream positions, a run without spans), each row with its
selection key (`rowKeys`, the one scheme the trace page keys by too),
offset and duration
(`took`: 15µs, 9.5ms, 7.7s), a gist from the folded events and
transcript, its status, its lane and whether it is on the critical path;
a child run is one collapsed row until expanded, its rows from its own
spans (each carrying its `weft.run.id`, the `?tree=1` shape) or fold,
hung under the tool span its `invoke_agent` span names as parent (call
ids repeat per step; `weft.parent.*` only when that span is absent) — a
nested Generate's run, or a live delegation whose tool has not ended, is
a child row too, and a running run's in-flight steps and calls are open
rows on the time scale;
`dimRows` dims (never hides) by errors, slow or tools. The Timeline
draws those rows as Studio's run page does, one level deep (U8.3): the
same keys, labels (with the gist as the label's title), kinds
(`ROW_KIND`, glyphs `KIND_ICON`: ▣ run, ↻ step, ◆ model, ◇ attempt, ⚙
tool, ↳ child run, ○ span) and tones (`rowTone`, coloured through
`lib/palette.ts`'s `TONE` — Studio's variable for each, the same hex,
`palette.test.ts`), indented by depth, the critical path under the bar,
each row's extent with its offset as the title, a row no span covers
"spans not reported yet" or badged `not_recorded`; a child run is one
row whose ⤢ opens Studio's run page at it (`?sel=r:<child>`, where it
expands in place), never its subtree; a click on a step's row selects
that step in Story (what the header's ⤢ carries) — a step's rows are
buttons on one tab stop (the selected step's, else the first; `↑`/`↓`
move it, `Enter`/`Space` select; the run row is no control). The panel
reads `/spans` without `?tree=1`: a child row's extent is its tool
call's, its label its agent from the run document (`waterfallRows`'
`agents`, else its tool's name — one rule on both scales and surfaces,
the label Studio reads off the child's `invoke_agent` span).
`parity.test.ts`'s "the run tree on both surfaces" holds the two
renderers to the same rows on the director run. Studio's span list
(`lib/trace.ts`'s `spansFromTimed`) walks the same span tree
(`spanTree`) and time domain. Raw is a JSON tree (`src/panel/tree.ts`, hand-written) of
`{doc, events, transcript, spans}`, plus `requests` when the panel
already read them: `▸`/`▾` per node (`aria-expanded`; `→` opens, `←`
closes); only open nodes are built — a small document opens whole, a
large one its top rows (150 by default, breadth-first), a container
shows 200 children then "… +N more", a string over 2,048 characters
(UTF-16 units, so a CJK or emoji string is cut by characters, never
inside a surrogate pair) its head and "… +N bytes" (the rest's UTF-8
bytes, counted once per string) — so a 10 MB transcript costs only what
is open, and a redraw that changes no record walks nothing again. The
filter box (`/`) matches keys and values, case-insensitively, 100 ms
after the last keystroke; it opens the path to each match (the first
200), marks it and says "n matches" (every match counted; past 200,
"n matches · first 200 opened"; a match past a container's 200 is drawn
anyway). `⧉` copies that node's JSON (the whole node, past any cap),
`copy all` the document, through `navigator.clipboard`; "copied" goes
after 2 s or with the next action; where the clipboard is missing or
refuses, the text is shown selected to copy by hand, and that box goes
once you leave it (or on `Esc`). The Story tab's node is kept across a
tab switch (hidden, not rebuilt), so what you opened in it stays open;
the Timeline's axis is the waterfall's own window, and spans it cannot
place (unreadable times, an end before the start) are counted in a
note. `download` saves the document
as `<run id>.json` (a `/` in a child's id becomes `_`). The turn list
has its own filter above it — text over the run id, the error and each
loaded row's `prompt_preview` (every run row carries one since 0.14.0,
absent for a content-off run) — a status (`running`, `succeeded`,
`failed`, `interrupted`, `parked` — open or not yet linked, never a decided one —
`parked → (decided)`, `cancelled`) and has error — over the loaded rows, never stored, "n of m turns" while it
narrows ("n of m loaded · the filter applies to the loaded turns" while
older pages remain, and then nothing loads by itself: the button does;
the narrow dropdown keeps the selected turn listed).
The list pages: when it scrolls to its end, a sentinel row (one
passive `IntersectionObserver` inside the panel's own list,
disconnected with the element) reads the next older page through the
runs route's cursor (`before=`/`before_id=`, 50 a page) — no cap: it
reads "loading older turns…" while a page is read and "all n turns
loaded" at the end. The sentinel is keyed by the cursor, so a page
that adds no row (all experiments, another session's) still arms the
next read. Where there is no observer, and in the narrow layout, the
sentinel is a button (`older turns ↓`): the default 520 px float and
the 460 px right dock are narrow (the turn column is a dropdown there),
so in them older turns load by the button — three clicks for 200
turns. A refresh (the live lane reconnecting, a stale read) keeps the
pages already read and adds new rows at the top.
The dev list (no public id) stays the newest 10 runs.
Cost (plan A5.3). Under capability `pricing` only — off, the panel draws no
figure and no empty cell — each turn row carries its cost beside its tokens
in the run page's words (`src/lib/cost.ts`, shared): `$0.0029`, and its
subagents' as a second figure, `$0.0029 + $0.0011 delegated`, never folded,
by the runs table's rule (shown when unknown or > 0; the run header also
draws a known `$0.00` delegated when the run has children — a run row
carries no child count, so the turn row shows its own figure alone there,
the same total); Studio's visible "cache not reported" mark follows a known
figure priced without cache splits. An unknown figure is "—" with the API's
`cost_reason` as its title and in its accessible text (" not known: …"): an
unpriced model ("openai/x has no price"), a running turn
("running: usage arrives at run end"), a derived turn whose spans reported
no usage. The
header's total beside "N turns · T tok" sums the turns' own + delegated
(`atLeastCost`): "≥ $x · k of n turns priced" when a turn's is unknown,
the reasons in its title only (a turn whose delegated part alone is
unknown still adds its own figure: "delegated: …"; never "<" under "≥"),
"—" when the known part is $0 (none priced, or only scripted turns —
never "≥ $0.00"); a scripted turn reads "$0 · scripted"; beside it the
header's one unpriced line (U7: "1 turn unpriced: no row for openai/x", a
link to Studio's pricing table prefilled for the first model), counting
the loaded turns the picker counts; and "≥" with "the N loaded turns only — load older turns for more"
while older turns load on demand; a partial sum's accessible text says
"partial: …", never "not known". The experiment
result pane shows the replayed run's whole bill under the playground card's
title ("the run's whole bill: its own model calls + its subagents'") and
"N replays of this turn: $x" — every playground or forked run off this turn
on the loaded page, not Studio's saved experiment ("the experiment's
cost"), summed by `atLeastCost` so an unpriced (scripted) replay leaves the
priced ones "≥" — and, while older turns load on demand, "(loaded only)"
with the header's words. A derived run's unknown figure reads "not known",
never "not known yet", keyed on the row's `derived` mark. `parity.test.ts` holds a turn's figure, reason and cache
mark to the run header's, and the result pane's figure and title to the
playground's result card.
Honesty (plan D5). Every badge the panel draws is one of the A3
table's ten (`src/lib/honesty.ts`, `obsdb.HoleNote`'s words, pinned by
`studio/testdata/holes.golden.json`), drawn by one module,
`src/panel/badges.ts`: a `<span class="weft-badge" data-hole="<hole>">`
whose text is the table's label and whose title is the reason and
"— fix: …" (the response's `reason`/`fix` when the route sent them, a
known cause's — `result_cap`, `no_public_id`, … — else the table's;
`fix: ""` says none applies). The same words ride in the badge's
accessible text, visually hidden, so a keyboard or screen-reader user
reads them too. The run page's `HoleBadge` draws the same span, the loop's
cuts included (its `TruncationBadge` is the table's `truncated`, cause
`result_cap`, or `max_tokens` for an unrun call). They sit on the turn's
header (its holes said in full: `interrupted`, `gap`, `max_tokens`,
`not_recorded`, `derived`, `stripped`), on each step card's header, on
the tool call whose result was cut (a recorder cap's cut; a result
cap's, `truncated` with cause `result_cap`; or a call a `max_tokens` step
never ran, `max_tokens` with no fix, since the loop already retried), on
compaction markers and on each request line (`not_recorded`, `hidden`,
`stripped`, and `gap` for a finished step no request row names, "this
step ran, but no request record names it" on both surfaces). E1.2's
Request tab calls the same `badge(hole, note)`. No other panel source
spells a hole's words or builds a `weft-badge` outside a named list of
non-hole badges (parked, the attempt line, a tool error, …), and the
`lib/` modules the panel imports name no other hole: `badges.test.ts`
greps for both. `weft.content: "redacted"` is reserved in Go and never
emitted by weft's pipeline, so `redacted` comes only from a non-weft
writer's records. Both surfaces still badge it.
A run derived from stock OTel GenAI spans (plan I1.3, `derived: true`,
docs/record-contract.md §9) reads `derived` in the `stock_spans` cause's
words ("derived from spans · no events", its fix naming the record
contract) everywhere it appears, never "0 events" or an empty pane: a
chip on its row in the runs table, search results, a session's turns and
the live page (from the live frame's `derived`), and on the panel's turn
(`turnChips`); the run header's hole in full (no event count); the story
a banner with the fix and one card per step from `runs/{id}/steps/{n}` —
the `chat` span's model, usage and cost as served (priced, or "—" with
the API's `cost_reason` when the spans reported none; the panel
header's token total leaves such a turn out, "≥" or "—"), its
`execute_tool` calls badged `derived`, its request and messages blocks
the badge (the panel's Story hands off: "2 steps · open in Studio ⤢",
the run page's story at step 0); the raw view and the panel's Timeline
and Raw tabs the badge above what they hold, with no "no events yet"
line and no "replay here" or space-replay hint; the Request tab `request: derived`; the waterfall
and the Timeline the real spans on the time axis (no event playhead;
until the spans are read, the derived note in the waterfall's place). A
derived source has no transcript, so the replay verbs, the editor, the
drawer and save as test are not offered: they are drawn disabled with
the badge's words (`derivedWords`), which is what the server's 400 would
say. The compare page reads every column against a derived side "not
comparable", cost too (it follows usage). `parity.test.ts` holds both
surfaces to the same holes, chips and refusals on the server's own
bodies (`src/test/derived-fixture.ts`).
The footer's content line replaces the old suffix and is said only on
evidence: "content on · 2 events shortened (24.6 KiB cut)" (a recorder
cap cut the open turn's events, so content was there; on a capped walk,
"· first 5000 events"), "content on" for a recorded `full` mark (the
latest run's, from `/api/meta`'s `content`, when that run is the open
turn or no turn is open), "content off · otel.NoContent()" for a
destination's `stripped`, and "content off · captured none
(weft.Content(false), or no destination takes content)" for the core's
`none`. The core marks `none` when no destination takes content too,
not only for the option (`/api/meta` words it the same). For a mark read from
meta, its `note` and `fix` win. When nothing marks the content (a stored
event carries no mark when full, an unmarked run, another run's mark),
the line says nothing. The cap itself is the app's
(`otel.Content(otel.ContentConfig{MaxBytes: …})`) and no record carries
it, so the line does not name it. Turn rows carry chips read from the run
row, each titled with its source: "scripted · 0 tokens" (the run's model
is `weft/runtime/scripted`, the scripted engine's own, pinned by
`runtime`'s `TestScriptedModelInfo`, because no `weft.*` attribute names the
engine; plain "scripted" when the row's usage is not zero, as when a
Subagent child ran a real model), "fork of s_…#e_…"
(`weft.session.forked_from`, from the row's `meta`) and "experiment of
t3" (`weft.forked_from`, the source turn named by its turn number when
the list holds it; "experiment" alone with only `weft.experiment.id`).
`src/panel/parity.test.ts` serves one run per hole of the table, and per
loop cut, to both surfaces from one fake Studio. It asserts each shows
`data-hole="<hole>"` where both mark its source (run header, step card,
tool call, request section) with the table's words. It also asserts that
every badge the run page shows is one the panel shows, and that a
hole-free baseline (timed steps, a request row) shows none on either.
And a pre-A1 run's request section says `not_recorded` on the run page
and in the panel's Request tab.
Studio-only, by the panel's byte budget (not a parity gap): the
story's end card ("■ max steps reached at step 9", `endCard`), a loop's
"repeats step K" chip (`loopRepeats`), the tool failure pill
(`toolFailure` / `retryPill`, "retry 3/2 — budget spent"), the header's
concurrency chip and the "last step stopped on" relabel of a stop
reason that contradicts `stopped_by` — the panel's turn chip names what
ended the turn, the run page draws the rest.

The Request tab (plan E1.2) is the run page's Request pane in the panel
(`src/panel/request.ts`, the same readings: `lib/request-pane.ts`,
`lib/requests.ts`, `lib/diff.ts`'s bounded diff). It shows one step — the
one `J`/`K` move, a click on a step card or the tab's own step buttons
picks, and `⤢` carries — with its header (the model, the attempt line,
the chips **changed by PrepareStep**, the neutral **prompt changed at
this step**, **overridden by experiment**, **catalog changed at this
step**, decided by the same hash rules as the pane), an attempt picker
when the step retried, the system prompt (12 lines, then "show all")
with its diff — against the previous step when the hash moved, at the
first step against the registered instructions when the panel can read
`/api/manifest` (setup A, a dev token; captioned "not verified for this
run" from a name-only weft.json, and when no manifest is readable the
tab says so instead of diffing), never over a cut record, "too large to
diff" past 2,000 differing lines a side — the messages sent (count ·
bytes from the transcript the panel holds, the last three inline, the
rest in D4's tree), the tool catalog (each name expands to its
description, its policy chips and its schema in D4's tree), every param
("adapter default" when nil), tool choice, thinking, a subagent call's
child request (its step 0, read by the child's id on demand — "read its
turn"; a child whose history cannot be read says so and links to its
Studio page) and the step's compaction marker. The step shown is the one
⤢ carries and `J`/`K` move from (over the steps the record names too).
The manifest is read per endpoint and token, asked again 30 s after a
failure and once more when it does not list the run's manifest hash (a
redeploy under `weft dev`). A derived prompt or catalog record (its
body did not parse) is badged `derived` and never diffed over, here and
on the run page. A Studio without the `requests` capability is
`not_recorded`, cause `not_served` (obsdb's table; `/api/meta`'s
`capabilities_off.requests` words first). `/` on the tab focuses an open tree's filter,
else the turn list's. Every absent block is a badge from `badges.ts`
with its reason; a run-wide hole is that badge and nothing else — a
read-scoped token sees `hidden` and never asks for the record or the
manifest, so no prompt byte reaches the page. The provider wire pair
(plan A6) is never drawn here: a step whose request row carries `raw`
gets one **wire ↗** link — `lib/links.ts`'s `runLink(id, {step, view:
"story", pane: "wire"})` off the endpoint, in a new tab like `⤢` — to the
run page's Wire tab for that step; a step whose rows have `raw: null`
gets no link.

Replay from here (plan F1). The Story view's step cards, tool calls,
steers, the subagent call's child row and the child's own steps carry
the run page's verbs, shown on hover and on keyboard focus
(`aria-label`s; Enter/Space stop at the verb, Escape goes on to the
panel; Escape on a drawer control closes the drawer and returns focus
to its verb): **replay from this step**, **edit this result and
replay** (`from_step` N+1 with the call's recorded result pre-filled —
drawn only up to the highest `from_step` the server accepts for the
transcript, `lib/experiment-body.ts`'s `replayBounds`: on its last step
only when that step's calls are all answered there; no transcript, no
edit or steer verb; an empty result seeds no edit), **edit the prompt
and replay** (the step's system prompt from its request record;
unreadable or cut, the registered prompt and a line saying so, with
the hole's badge),
**re-run** (the actions row's ↻ too: it opens the drawer, it never
posts at once) and **continue here with a new message** (a fork, on a
session's top-level turn only; Run waits for a message that is not
blank). A child replayed from its row is read first, and Run waits
while a step's catalog is being read. `from_step`
and an edit's step are the step ordinal. Each opens the experiment
drawer pre-filled from `lib/replay.ts`'s draft — the same draft Studio's
replay drawer opens on, posting byte for byte the same command — with
focus on the verb's field, and back on the verb when it closes. Above
Run, the ack preview: per tool of the step's catalog (the request
record's replay classes) "runs", "substituted" or "parked" and why, in
`lib/replay.ts`'s words ("replay never (unannotated) · substituted from
the recorded result when the call repeats, else parked"), redrawn as
the tools, mode and breakpoints change; a catalog it cannot read is
its badge, the registered tools judged instead; and the prefix line —
"steps 0–2 kept", "· the compacted prefix (what the model saw at step
3)" when a run-scope compaction view sits at or before the step. Run is
held while side effects allow would be refused (the registered tools
left on) or a pre-filled edit has no field (shown, badged). A child's
verbs replay the child as its own run of the child row's agent; an
agent no runtime registers is said and nothing is posted. The result
pane links the new run to its source step (selected in the panel, ⤢ to
Studio's run page), and an experiment's turn row has a "↖ t3 step 2"
button beside its "experiment of" chip (a ⤢ link when the source turn
is not listed). The verbs and the drawer are drawn only under the
`playground` capability and a token that may act — `lib/replay.ts`'s
`canReplay`, the run page's gate: a read-scoped panel token sees none.
`scripts/panel-gate.ts` opens the tab on `examples/studio-local`'s
trimmed turn and checks the chip and the deleted guidance line.
Deep links (plan G1) follow one scheme, `src/lib/links.ts`, shared by
the panel and Studio's app (an ESLint rule refuses a Studio URL built
anywhere else): `runs/<id>?step=<n>&view=story|raw&pane=request|wire&sel=…&scale=time|order|graph&t=…`,
`sessions/<id>`, `traces/<id>?sel=<row key>&view=chat`,
`playground#run=…` (the hand-off in the fragment),
`playground?run=…&step=…&agent=…&engine=…` (the page's own state, G2)
and `playground?experiment=<id>`; the page state each one carries is
the table under "Every view is a URL" below. `step` is
the step's ordinal — its index as the loop counts it, the `n` of
`runs/{id}/steps/{n}` — never an event position or a transcript batch
index; a call is named with its step (`sel=c:<step>:<call id>`,
`c:resume:<call id>`). ⤢ carries the step being read (else the running
one); each tool call, subagent badge, session and trace in the panel is
a link. A turn's **save as test** (capability `export`, not under a
read-scoped token) is one too: `runs/<id>?save=test`
(`saveTestLink`), where the run page downloads the wefttest zip with
Studio's own bearer — under a panel token the chip still opens the run
page, whose bearer must be able to export (the dev token): a read-scoped
token there is refused with the notice — an in-panel fetch-and-save measured ~0.8 KiB of
`panel.js`. The result pane's **keep as prompt ⤴** copies the drawer's
prompt and, under `proposals` with the dev token (or none), also
proposes it (`PUT /api/agents/{name}/proposal`), the outcome on the
panel's note line in Studio's toast words (`lib/saves.ts`); a panel
token copies only and is told "a proposal needs the dev token". No link carries a token, and no request does in its URL: the
token travels in the `Authorization` header, and each live stream is
opened with a grant (`POST /api/live-grant`, the bearer in the header;
the stream URL carries only the `sig`). The browser's own reconnect is
left to resume with `Last-Event-ID` while the grant lasts; one after
its 60 s, or one Studio refused (a restarted Studio's new grant key),
gets one new grant and a fresh connection (the panel refetches what it
lists). A panel token's stream that ends with `event: expired` is not
asked again: the live dot goes out, history stays, nothing reaches the
console — a new token is a new `data-token`, and the panel restarts
its connection on it.
`Alt+W` toggles (Q4); the other keys, the dock's layout and what it
remembers are under "Layout" below. Setups B/C add
`data-endpoint` and `data-token` (a dev token, or a panel token your
backend mints per page via `POST /api/panel-tokens`). A single-page app
that switches conversations sets `window.__WEFT__ = { scope }` (or
`{ publicId }`) instead of `data-scope`; the tag then carries no other
knob, so keep `data-weft` on it — it is how the panel finds its own tag
(and with it the endpoint) whatever the file is called.

The panel follows a scope, `pub_…;session=s_…;flow=f_…;run=r_…`
(`src/lib/scope.ts`; only the public id is required). The public id
selects the conversation; `run` pins the selected turn, and its live
tail, to that run once (your clicks own the selection afterwards), or
says `run r_… not in this conversation` when it is not among the
conversation's runs; `session` narrows the turn list to that session's
runs (a run without a session id is not judged, and a list whose runs
carry none says it is not narrowed); `flow` shows as a chip in the
header and filters nothing yet (it waits for `weft/flow`).
`data-public-id="pub_…"` is still read, as the scope `pub_…`, but it
is deprecated: `data-scope` is the form.

Where the scope comes from (plan C3, scope detection). Each rung after
the first is passive, and `data-detect="off"` turns rungs 2 and 3 off;
rungs 1 and 4 always work — no `data-detect` value and no token turns
them off. The order is explicit (rung 1) > the URL (rung 4) > a marker
(rung 3) > a header (rung 2): the numbers are the order the rungs were
added, the precedence is how deliberate each one is — the host's own
word, then a hand-off a link made (Studio's dev links), then what the
page shows passively. The footer says which is in effect in one word:
the rungs installed — `detect: headers` (with `(chained)` when the
`fetch` it wrapped was not the browser's own), `detect: markers` or
`detect: headers+markers` — led by `url` (`detect: url`,
`detect: url+markers`, …) while the scope followed is the URL's, else
`detect: off`, `detect: explicit` (rung 1 names the scope, nothing
detects) or `detect: none` (nothing names one, and rungs 2 and 3 are
off by default here).

| # | Rung | When it is on | The explicit alternative |
|---|---|---|---|
| 1 | explicit: `data-scope` (element, `weft:scope` meta, script tag), `window.__WEFT__ = { scope }` or `{ publicId }`, `scope()` / `mount({scope})` from `@weftgo/devtools`, the deprecated `data-public-id` | always; it wins over every detected scope | — |
| 2 | response headers: the `Weft-Scope` header of the page's own same-origin `fetch` responses (the scope header below) | by default only with the page and the endpoint on loopback (`localhost`, `*.localhost`, `127.0.0.0/8`, `[::1]`) and no token or a dev/server token, and only once Studio has answered; anywhere with `data-detect="headers"` (or `mount({detect: "headers"})`); never under a panel token (`weft_pt.`) unless asked | `data-scope` |
| 3 | the DOM marker: `data-weft-scope="pub_…;session=…;flow=…;run=…"` on any element of the page — the framework helpers (`useWeftDevtools` for React and Vue, the Svelte `weftDevtools` action) set it on the element they are given | by default everywhere except a page off loopback under a read-scoped panel token (a playground-scoped panel token, the dev token, no token, or any token on loopback keep it on); anywhere with `data-detect="markers"` (or `"headers,markers"`, `mount({detect: "markers"})`) | `data-scope` |
| 4 | the page URL: `?weft_scope=…` (read first), else `#weft_scope=…` — the same string form, URL-decoded (`#weft_scope=pub_x%3Bflow%3Df_1`); a value without a public id is ignored | always, every setup and token, `data-detect="off"` included; below rung 1, above rungs 2 and 3; re-read on `hashchange` and `popstate` | `data-scope` |
| 5 | the fallback: no rung names a scope — the header reads "no conversation detected on this page · how to scope" and lists the newest runs | when 1–4 name nothing | any line under "how to scope": `data-scope="pub_…" on the panel's <script> tag (or <weft-devtools>)`, `scope("pub_…") from @weftgo/devtools`, `data-weft-scope="pub_…" on the chat's element`, `scope.Header(h, …) on the app's handler (Go, package weft/scope)` |

Rung 3 trusts the page's DOM: a `data-weft-scope` attribute anywhere
in it names a conversation, and an HTML sanitizer (DOMPurify among
them) keeps `data-*` attributes by default — so markers must not
appear in untrusted HTML. A page that renders user or model HTML marks
the container `data-weft-untrusted`: a marker on or inside such an
element is ignored (focus inside it names the chat around it, if that
one is marked), and setting or lifting the attribute later rescans. A
host that cannot mark it sets `data-detect="off"` (markers and headers
off — a `data-detect` naming only `headers` leaves markers at their
default, on) and names the scope explicitly.

Rung 2 patches a global of your page, `window.fetch` (shadow DOM
scopes DOM and CSS, not JavaScript), so it is held to these rules
(`src/panel/detect.ts`): it reads the response URL and the
`Weft-Scope` header only, never a body, never a clone, and never sends
anything; it reads a same-origin response, or a cross-origin one only
when the page and the response are both on loopback (a dev server on
`:5173` calling the app on `:8080`) — any other cross-origin response
is ignored even when it exposes the header, so a cross-origin
production app uses `data-scope` or the DOM marker; the panel's own
Studio requests are never read; it is installed only once Studio has
answered, so a panel whose Studio never answers never touches `fetch`;
it chains to the `fetch` it found (the browser's own or
another library's patch) and returns the same response or rejection;
it is put back exactly as found when the panel disconnects or the rung
is turned off, unless another patcher wrapped it after the panel (then
that stack is left alone, the wrapper goes inert and the footer says
`fetch not restored`) — so with two panels on one page, the first to
disconnect leaves its inert wrapper in the chain, a pass-through; and
nothing it does reaches the console. It is
fetch-only: a page cannot read an `EventSource`'s response headers, so
an SSE chat app names its scope with rung 1 or rung 3.
WebSocket is never wrapped. A detected scope of the conversation the
panel follows (the same public id, session and flow — the next turn's
header) is a narrowing, never a restart: its run is pinned unless you
have clicked a turn since the last pin. Another conversation is
followed only from the request path that set the current one, so two
widgets polling for different conversations do not thrash; the panel
keeps the newest scope per path and one entry per conversation seen
(with its newest run), for the switcher below. A run the scope names
that is not listed yet (a streaming handler's header lands before its
run row) is looked for once more a second later before the panel says
it is not in the conversation.

Rung 3, the DOM marker, is the production rung: it touches no global
of your page (`window.fetch` stays as you left it) — it reads
attributes only, through one `MutationObserver` on
`document.documentElement` (only changes that add, remove or re-mark a
marked element count; rescans debounced ~100 ms, trailing, but at most
500 ms apart while such changes keep coming, and none while the page is
hidden — one when it is shown), one passive, capturing `focusin`
listener and one `visibilitychange` listener on `document` (event
listeners, not patches), all removed when the panel disconnects or the
rung is turned off (`src/panel/markers.ts`). It never writes to your
DOM, never reads the panel's own tree (a `<weft-devtools>` and
anything inside it are skipped, and focus inside the panel — even a
panel mounted inside a marked chat — is never a chat's), ignores an
empty value or one without a public id, and prints nothing; a scan
that changes nothing draws nothing. The marker must sit in the light
DOM: one inside a widget's own shadow root is invisible to the scan
(and to focus's `closest()`) — put it on the host element at or above
the widget. With several markers on one page the
one nearest focus wins: the closest marker around the focused element,
else the one focus was last in, else the one followed now, else the
first in document order. A marker's scope is a conversation (public id,
session, flow) whose run narrows it, so following it is C3.2's rescope
— the same conversation narrows, another restarts, a turn you clicked
stays. A followed marker that leaves the page stays followed — only
focus or the switcher moves the panel — except on a route change: when
every old marker is gone and exactly one new one appeared in the same
scan, the new one is followed. An explicit `data-scope` (or
`window.__WEFT__`) wins over a marker, unless a marker carries the
same conversation — the helpers call `scope()` and set the marker, so
focus moves between the helpers' chats; from then until the explicit
scope changes it counts as a marker's, even after that marker leaves,
and its run (a helper's next `scope()`) pins as an explicit run does.

The scope switcher: when more than one conversation is known (or one,
while the panel follows a conversation no longer on the page) — the
explicit scope, then the markers in document order, then the
header-detected scopes in first-seen order, each conversation once
under the first source that names it, at most 20 — the panel header
shows a `<select aria-label="conversation">` listing each by its public
id (with its session and flow when set) and its source (`explicit`,
`marker`, `header`); `●` (live) or `○` marks the one followed.
Choosing one follows it and pins its run (a forced rescope) until focus
next goes into a marker or the explicit scope changes. A followed
conversation that is no longer known is shown as "not on the page",
not offered. One known conversation shows no switcher.

| `data-detect` | rung 2 (headers) | rung 3 (markers) |
|---|---|---|
| unset | page and endpoint on loopback, any token but a panel token (`weft_pt.`) | on, except a read-scoped panel token off loopback |
| `headers` | on anywhere | as unset |
| `markers` | as unset | on anywhere |
| `headers,markers` | on anywhere | on anywhere |
| `off` (named anywhere: `off,headers` is `off`) | off | off (rungs 1 and 4 stay on) |

Empty words are skipped (`markers,` is `markers`); a value naming
anything else (`bogus`, `headers,bogus`) is unset: the defaults apply.

Rung 4, the URL, reads `location` and listens to `hashchange` and
`popstate` — two passive listeners on `window`, removed when the panel
disconnects (event listeners, not patches); it never writes the URL
(no `pushState`, `replaceState` or hash change, whatever the switcher
does). A new URL scope drops a switcher choice and pins its run, as a
new explicit scope does; an explicit scope set later wins over it, and
removing that hands the panel back to the URL. Focus in a marked chat
does not move the panel off the URL's scope; the switcher (which lists
it as `url`, after `explicit`) does. The parameter is read from the
query and from the fragment, a hash router's own query included
(`#/chat?weft_scope=pub_…`); a value ends at the next `&` or `?`, is
decoded once (a value encoded twice, whose public id still holds `;` or
`=`, is ignored), and one without a public id is ignored. Because the
rung listens instead of patching, a navigation the page makes with
`pushState` or `replaceState` is not seen until the next `hashchange`
or `popstate`, or an attribute change on the panel.

Rung 5, the fallback: with no scope from any rung, the header says
`no conversation detected on this page · how to scope`, and "how to
scope" opens the four one-line fixes above. The newest runs it lists
follow `/api/live?agent=<agent of the newest listed run>` (run frames
only, opened through a grant like every stream) instead of a poll: the
live API has no selector for everything (exactly one of `run`,
`session`, `public_id`, `agent`), and an app's dev page usually runs
one agent. While that stream is up the list says `live: agent X · the
other agents' runs every 30 s` and is still read every 30 s (dock open,
page visible), so another agent's runs that start later appear. The
10 s poll (dock open, page visible) takes over while no stream is up —
no run listed yet (no agent to name), the stream gone (it is reopened
on the bounded backoff — 5 s, doubling, capped at a minute, five times at most; the poll does
not reopen it in between), or a grant refused (`streaming needs the
server token · polling`; a panel token is never granted an agent's
stream, so under one the panel never asks and says the same). In the
fallback only the streamed agent's runs light the pill below.

The collapsed pill is an activity signal: while the stream the panel
holds anyway (its scope's `public_id` stream, or the fallback's agent
stream — no second one is opened) lists a run as running, the pill
pulses (a CSS animation, none under `prefers-reduced-motion`) and shows
the run's live step count, `● 3`, with `aria-label` and title
`weft devtools · running, step 3`. The count is the open tail's steps
when that run is the turn the panel follows, else its row's (unknown
while it runs: `●` alone, `weft devtools · running`). When the run
ends the next draw is the plain pill.

Layout (plan D1). The dock floats — dragged by its header, resized
from its corner, between 360×280 and the viewport minus 16 px, clamped
again on every window resize (a passive listener, removed on
disconnect) — or docks to the `left`, `right`, `bottom` or `top` edge,
resized along that edge (pointer events with pointer capture, no
library; touch works through them). Collapsed it is the pill: the live
dot and step count above, and the current turn's cost — its tokens
in→out once the run has finished, `—` while it runs (Studio prices no
usage, so tokens are the cost). Hidden draws nothing: the element, its
API and its events stay, and `Alt+W` or `open()` bring the dock back.
Under 640 px of panel width the turn column is a dropdown and the
header's actions wrap — the default 520 px float is that narrow, so the
dropdown is what it shows until you widen it; under a 480 px viewport
the panel is a full-width bottom sheet, 70 vh at most. The resize
handles are pointer-only (`aria-hidden`; a keyboard resize is D3's). The header's `⇆` button and
`Alt+Shift+W` go to the next layout: float, dock right, bottom, left,
top.

| Attribute | What it sets | Default |
|---|---|---|
| `data-position` | the initial place: `bottom-right` or `bottom-left` (the float's corner), `right-dock`, `left-dock`, `top-dock`, `bottom-dock`; set later, it moves the dock | `bottom-right` |
| `data-open` | start expanded | collapsed (the pill) |
| `data-mode` | the initial mode: `float`, `dock`, `pill` or `hidden` | from the two above |
| `data-push="true"` | while docked and open, pads `<html>` on the docked side by the dock's size — `padding-<side>: var(--weft-devtools-inset)`, the variable set to the size (the bottom sheet: `padding-bottom` by its `70vh`) — so the page's flow never sits under the panel. It replaces the inline padding on that side while docked; the inline values from before (or one your page set meanwhile) are put back exactly on close, on a mode change and on disconnect. The panel's one write to your document, opt-in. Padding does not move `position: fixed` or `sticky` elements: a fixed or sticky composer keeps clear with the variable in your own CSS, `bottom: var(--weft-devtools-inset, 0)` (and the matching `right`/`left`/`top` for those docks) — push sets the variable as well as the padding | off |
| `data-z-index` | the dock's and the pill's z-index (an integer) | `--weft-z` on the element, else 2147483000 |
| `data-theme` | `light` or `dark` names the theme (above everything); `auto` leaves it to the order below | `auto` |

Theme (plan D2). The panel is light or dark, resolved in one order:
(1) explicit — `data-theme="light|dark"` on the element, `weft:theme`,
the script tag, or `mount({theme})`; (2) stored — the header's `◐`
button cycles auto → light → dark (disabled, and saying so, under an
explicit theme), kept as `theme` in the key below, and auto clears it;
(3) auto — your page's `<html data-theme="dark|light">`, else
`<html class="dark|light">`, else `<html>`'s computed `color-scheme`
when it is exactly `dark` or `light`, then the system's
`prefers-color-scheme`, else dark. A theme toggle on your page is
followed (one passive attributes-only observer on `<html>`'s `class`
and `data-theme`, and one media-query listener, both removed on
disconnect); `<html>` and `matchMedia` are only read. The result is
`data-theme-resolved` on the `<weft-devtools>` element itself. The
palette is the Studio app's (`src/lib/palette.ts`, which names the
`src/styles.css` variable each shared token equals — a test fails on
drift), and both themes clear WCAG AA (`theme.test.ts`: 4.5:1 text,
3:1 UI). Every colour, radius and font is a custom property on the
panel's `:host`, so your CSS overrides any of them — an outer rule
beats the shadow tree's `:host`, in either theme:
`weft-devtools { --weft-bg: #fff }`. The tokens: `--weft-bg` (panes),
`--weft-bg2` (header, steps, footer), `--weft-bg3` (selected row,
fields), `--weft-fg`, `--weft-dim`, `--weft-faint`, `--weft-line`,
`--weft-accent`, `--weft-on-accent` (text on the accent), `--weft-warn`,
`--weft-err`, `--weft-info`, `--weft-ok`, `--weft-parked`,
`--weft-scrim` (the `?` overlay), `--weft-shadow`, `--weft-font`,
`--weft-font-prose` (a step's text, the prompt and the notes: `system-ui,
sans-serif`, no webfont; ids, JSON and numbers stay in `--weft-font`),
`--weft-radius`, `--weft-radius-sm` (and `--weft-z`, above).
The panel's own tokens, not Studio's: light `--weft-faint` `#766f61`
(Studio's `--faint` reads 2.6:1 on paper, under AA; dark `--weft-faint`
is Studio's `--code-mut`), `--weft-parked` (Studio has no purple),
`--weft-scrim` and `--weft-shadow`. Two consequences of the Studio
palette: in light, `--weft-bg2` equals `--weft-bg` (Studio's `--card`
is its `--background`), so the header, steps and footer differ from
the panes by their line only; in dark, `--weft-bg3` equals
`--weft-line`, so fields (`.weft-input`, the narrow turn dropdown) draw
their border in `--weft-faint` (3:1 on their fill). `color-scheme` on
`:host` gives the panel's native controls and scrollbars the resolved
theme. The one thing drawn in your page's flow — the "Studio not
reachable" line a mount you made shows — sits on your background, so
it takes your page's text colour (`color: inherit`, the retry link
underlined), not a theme's.

The panel remembers, per origin, in `localStorage["weft.devtools"]`
— one JSON object, `{v: 1, mode, side, open, hidden, x, y, w, h, d,
run, theme, raw, tab, debug}`: the mode (`float`/`dock`), the docked side,
open, hidden, the float's box (`x`, `y`, `w`, `h`, px), the dock's size
(`d`), the last selected turn (`run`, by run id), `theme` (the `◐`
choice: `light`, `dark`, or `""` for auto), the raw view, `tab` (D4,
additive to v1: the turn view's tab — `story`, `request`, `timeline` or
`raw`; `raw` mirrors it, and a document without `tab` opens Raw when
`raw` is true, else Story), and `debug` (the old `localStorage.weft_debug=1`
switch, migrated into the key and dropped). The placement fields are
written only once you place the panel (toggle, drag, resize, re-dock):
until then the `data-*` attributes above decide on every load; after,
the stored placement wins over them. Another version is ignored; every
read and write is guarded, so a private window just forgets.
`localStorage.removeItem("weft.devtools")` resets everything (and turns
`debug` off).

The keyboard (`src/panel/element.ts`'s `SHORTCUTS`, the `?` list).
`Alt+W` is the one key the panel hears on `window`; every other key is
heard on its shadow root, so it fires only while focus is inside the
panel — a key typed into your page never reaches it, and keys typed
into the panel's own fields are typing. `Alt+W` that opens the dock
puts focus in it, so the keys below work at once.

| Key | Does |
|---|---|
| `Alt+W` | toggle the dock from the page — not while a text field has focus (`Ctrl+Shift+W` too, where the browser delivers it) |
| `Alt+Shift+W` | next layout: float, dock right, bottom, left, top |
| `Esc` | close (the `?` list first); it is not stopped, so your page's own `document` Esc handlers (a modal) still run — the panel never blocks them |
| `j` / `k` | next / previous turn |
| `J` / `K` | next / previous step (⤢ carries it) |
| `g s` | open the turn and step in Studio (`lib/links.ts`, the link ⤢ carries), in a new tab |
| `r` | the Raw tab (again: back to the tab before it) |
| `/` | focus the filter: the raw tree's on the Raw tab, else the turn list's |
| `←` / `→` | on the tabs: previous / next tab (`Home` / `End`: first / last) |
| `?` | the key list |
| `↓` / `↑` | on a turn row: move the list's one tab stop to the next / previous row (`Enter` or a click selects) |
| `Tab` | in an open, focused float: wraps from the last control Tab can reach to the first (`Shift+Tab` back) — controls inside a closed `<details>` or hidden by CSS (the narrow float's list) are skipped. The float is non-modal (`role="complementary"`, no backdrop, the page stays live), so this is the one focus trap, and `Esc` collapses the dock to leave it; docked, the pill and the sheet never trap |

The renderer (plan D3). A draw builds the dock afresh and patches it
into the nodes on screen (`src/panel/render.ts`'s `patch`, about 1 KiB,
no framework, no `innerHTML` — text nodes only): a child matches by its
`data-key` — a turn row by its run id, a step card by its step ordinal
(`s<n>`), a tool call or a parked call's line by its call id, a
turn-dropdown option by its run id, a switcher option by its
conversation key, the dock and the footer by name — else by tag and
class (a key repeated under one parent pairs in order); a kept node is updated in place
(attributes, text, a field's value only when the build says otherwise),
and handlers ride `on()`, so a kept node answers with the newest
closure. While a turn streams only its running step's card changes,
and focus, the caret, the panes' scroll and the `<details>` you opened
stay because their nodes do — nothing is restored by hand; a focused
node is never moved (a move would blur it). The 100 ms live-draw
throttle stays.

| Element | ARIA |
|---|---|
| the dock | `role="complementary"`, `aria-label="weft devtools"` |
| the turn list | `.weft-rows`: `role="list"` (`aria-label="turns"`); each run a `role="listitem"` holding its row button, the selected one `aria-current="true"`, its `aria-label` the row in words (`turn 3, parked, <title>, 2 steps, 60 in 30 out, 3h ago`, U29.5); roving tabindex (one row `tabindex="0"`) |
| the running step's text | `aria-live="polite"` and `aria-busy="true"` while it streams (a reader waits instead of re-reading each draw) — the one live text region |
| the step-end status | a visually hidden `role="status"` line, always in the dock, written once when the running step ends: `step N finished · n words` |
| expanders | `aria-expanded` on how to scope, a subagent's summary, and each raw tree node's `▸`/`▾`; Raw is a tab (`r` toggles it; the header's `raw` button is gone, U29.5) |
| the turn view's tabs (D4) | `role="tablist"` (`aria-label="turn views"`) of `role="tab"` buttons with `aria-selected`, one `tabindex="0"`, `aria-controls` on the selected one; the drawn panel `role="tabpanel"`, `aria-labelledby` its tab |
| the filters | `aria-label="filter turns"`, `aria-label="status"`, `aria-label="filter keys and values"`; `⧉` labelled `copy <key>` |
| icon buttons | an `aria-label` on each: `⤢`, `⇆`, `◐`, `–`, `↺`, and the pill (its visible words, `weft devtools · 10→4`); from 480 px of dock width `⤢`, `⇆`, `◐` also show their word (`Studio`, `layout`, `theme` — a `data-w` attribute as `:before` content under a container query; each word inside its label) |
| the switcher, the turn dropdown | `aria-label="conversation"`, `aria-label="turn"` |
| the experiment drawer | the system prompt, input and edit fields inside their `<label>`; the selects `aria-label`led `model`, `thinking`, `side effects`, `engine`, `thread`; the result pane's `compare with`; tool and breakpoint checkboxes inside their labels; `–` / `↺` labelled; the option lab's fields inside their `<label>`, its checkboxes `aria-label`led `only tools: <tool>` / `park on: <tool>` in a labelled group, each `↺` `reset <knob> to the agent's default` |

The accessibility budget. `src/panel/a11y.test.ts` runs axe-core (a
devDependency only — a test asserts `panel.js` holds no `axe`) with its
default rules over the panel's shadow root in each mode — float open,
docked right, the bottom sheet at 400 px, the pill, the Raw tab with
its tree open and filtered, the Timeline tab, the experiment drawer
open (and its option lab with an override, a refusal and the named tool
picker), and the Raw tree under the `?` shortcuts overlay — in both themes,
and fails on any violation, listing them. jsdom has no layout, so the
rules that need one come back incomplete, never as a pass:
`color-contrast` (D2's `theme.test.ts` holds the palette to WCAG AA
instead) and `label-content-name-mismatch` (visible text needs layout);
any other incomplete rule fails the budget. What jsdom cannot see —
contrast as rendered over your page, focus-ring visibility, real
screen-reader announcements (whether a reader waits on the busy stream
and speaks the step-end status once is a manual browser-gate check) —
is the browser gate's (`scripts/panel-gate.ts` against a real Studio,
and a manual pass).

Where the configuration comes from (plan C2). Each field — `endpoint`,
`scope` (or the deprecated `public-id`; `scope` wins at the same rung),
`token`, `detect`, `position`, `open`, `auto`, `global`, `mode`, `push`,
`z-index`, `theme` — resolves on its
own; the first source that sets it wins, so a meta tag can carry the
token while the script tag carries the endpoint. The panel never reads
its own file name: a bundle served as `/assets/devtools.abc123.js`
behind a proxy configures itself the same way.

| # | Source | The explicit form |
|---|---|---|
| 1 | `mount(opts)` — a programmatic mount (`import { mount } from "@weftgo/devtools"`, the npm entry; not in the script-tag bundle) | `mount({endpoint, scope, publicId, token, detect, position, mode, push, zIndex, open, auto, theme, target, enabled})` — `mode`/`push`/`zIndex` written as `data-mode`/`data-push`/`data-z-index`; `enabled: false` mounts nothing (`null`) |
| 2 | the `<weft-devtools>` element's attributes | `<weft-devtools data-endpoint="…" data-token="…">` |
| 3 | meta tags | `<meta name="weft:endpoint" content="…">` (also `weft:scope`, `weft:public-id`, `weft:token`, `weft:detect`, `weft:position`, `weft:open`, `weft:auto`, `weft:global`, `weft:mode`, `weft:push`, `weft:z-index`, `weft:theme`) |
| 4 | the panel's `<script>` tag: the running classic script, else the first with a `data-weft` attribute (any `src`, any value), else the module script whose `src` resolves to the bundle's own URL (`import.meta.url`) — never a tag because it carries a `data-endpoint`, `data-token` or other generic `data-*` word: a third-party widget's tag is not the panel's configuration. The running script and the own-URL match apply to the standalone `panel.js` only (`/studio/panel.js`, or the package's `panel.js` served as its own file): imported through `@weftgo/devtools` the panel is part of the app's chunk, whose URL and `<script>` are the app's, so only `data-weft` counts and the endpoint default stays the page's directory | `<script type="module" src="…" data-weft data-endpoint="…">` |
| 5 | `panel-config.json` beside the script (`/studio/panel.js` → `/studio/panel-config.json`), asked only when rungs 1–4 named no endpoint; its `endpoint` is taken on the script's own origin only, and it never carries a token | name the endpoint at any rung above |
| 6 | the script's own origin + directory (setup A) | name the endpoint at any rung above |

A tag whose `src` is not the bundle's own URL (a bundler that inlined
or re-served the panel, a loader script) needs `data-weft` on it (or a
higher rung); without a tag to find, the endpoint is the page's own
directory.

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
committed `studio/dist/panel/panel.js`, 271,112 B raw / 90,966 B gzip
(88.8 KiB, the bundle of the ledger's row "U13.2-fix2"; 7,338 B — 7.2 KiB — of headroom under
the 98,304 B, 96 KiB, cap — figures updated with every ledger row and the release bump, a
test holding them to the ledger's last row; A5.4 raised it from 80 KiB at 78,098 B, the row "phase 6 review fixes (I)"). It ships fully minified — whitespace,
identifiers and syntax (plan A5.3.0; before it the es-format library
build kept its indentation and newlines: 281,470 B raw / 81,358 B gzip,
562 B under the cap; since P8-fix-a its stylesheet's whitespace too,
`vite.panel.config.ts`'s `minifyCss`, and no `/* @__PURE__ */`
annotation — the npm package's thin modules keep theirs; since P9-trim
its own member names too: oxc's mangleProps over the names
`studio/web/scripts/panel-mangle.ts` proves are the bundle's alone —
private members, the internal classes' and internal types' members,
checked with the TypeScript checker on every build — while the host
API, the npm entry's calls and every wire, DOM and storage name stay
verbatim) — and with no
sourcemap, so it is unreadable in a
host page's devtools: that is the accepted cost of the headroom. To
read the panel, read its source, `studio/web/src/panel`; a sourcemap
is deliberately not shipped (it would be a second file, against the
one-file artifact, and the npm package carries the same bytes). The scope-detection ladder (C3: rungs
2 to 5 and the activity pill) cost +6.0 KiB gzip against its +3 KiB
estimate: nothing deferrable supplies the first scope and the
deferrable remainder is under 1 KiB, so the overrun is accepted rather
than split. The host API below cost about +3.1 KiB against +1 KiB (with its
review fixes); the
layout (D1) +3.6 KiB against +4 KiB (with its review fixes); the keyed
renderer and ARIA (D3) +1.5 KiB against +2 KiB (with its review fixes); the views (D4: tabs,
the JSON tree and its filter, the turn filter, paging) +5.2 KiB against
+5 KiB (with its review fixes). The Request tab (E1) measured +6.1 KiB
against its +4 KiB estimate (with its review fixes): the overrun is the
review's honesty work (derived records, the manifest asked again, the
unreachable child, `not_served`), nothing in it is deferrable at first
paint, and minifying the bundle was held in reserve (since pulled), so it is accepted
rather than cut. The transcript editor and its preview (F2) measured
+6.3 KiB against +4 KiB with its review fixes: the overrun is
review-driven honesty and correctness (the compacted-range pre-check,
the pending and held Run states, steers by step, the int64 and 2^53
refusals, the fork hold), nothing deferrable supplies the editor's
first paint, and 2.2 KiB of headroom remained at the time, so it is accepted.
`make studio-panel-asset` stages it as `panel-<version>.js` + sha256
for non-Go backends.

The size ledger. Every panel build (`bun run build`, `make
studio-build`, `make studio-check`) prints the per-item size table,
measured with the version stamp normalized — the stamped version
string (`scripts/weft-version.ts`) replaced by the fixed-length
placeholder `"v0.00.0"` (`normalizeStamp` in `scripts/panel-budget.ts`)
before gzip, in the build and in `budget.test.ts` alike, so a version
bump moves no ledger byte — and gzipped by `gzipSize` there, pako's
pure-JS deflate at level 6, not `node:zlib`, whose bytes differ across
the host Node versions vite and vitest run under (the ledger's
`measure` note says which rows were measured which way):
each plan item's estimate beside its measured gzip delta, `over` when
the delta is more than half again the estimate (`over (accepted)` when
the plan recorded why it was not split), the unbudgeted items, the
baseline, and the total against the cap with the headroom left. The
cap is 96 KiB (98,304 B) since phase 7, by the UX audit's decision 1
(§10, 2026-10-12; item A5.4, the ledger's row "A5.4 cap raised to
96 KiB", the bundle unchanged); it was 80 KiB (81,920 B) before. The
ledger's `cap` is the one source: the build's failure
(`vite.panel.config.ts`), the printed table and
`scripts/panel-asset.ts` all read it, and `budget.test.ts` pins it.
The deltas come from `studio/web/panel-budget.json`, a committed,
append-only ledger: one row per landed item — `{"label": "D1", "item":
"D1", "gzip": <panel.js's gzip bytes after it>}`, `item` the budget
line it counts against (`""` for none) — and earlier rows are never
edited. An item appends its row in the same commit as its rebuilt
`studio/dist/panel/panel.js`: the build says when the built file
differs from the last row, and `src/panel/budget.test.ts` — run by
`make studio-check` — fails until the last row is the committed file,
so a rebuild that forgets its row fails studio-check. The row
`"minified"` (73,706 B) is the minify lever, not an item: from it on,
every row is measured on the minified bundle and is not comparable
byte for byte with the rows before it, which stay as history (the
table folds its −7.5 KiB into the unbudgeted line, −0.1 KiB net with
the unbudgeted rows after it, as `budget.test.ts` pins). The per-item
marks are evidence, not a gate; the build itself fails only over the
cap (`vite.panel.config.ts`).

The same file is on npm as `@weftgo/devtools`, for apps that bundle
everything and ship no `<script>` tag (Vite, Next, SvelteKit). This
is a second delivery, not a replacement: the handler above is still the
canonical install. `import "@weftgo/devtools"` loads the package's
`panel.js`, which has the same sha256 as `/studio/panel.js` of the same
version, and so does what the tag does. Beside it are a thin typed
module (`mount`, `scope`, `open`, `close`, `toggle`, `on`,
`serializeScope`/`parseScope`, and the host API below: `select`,
`isOpen`, `studioLink`) and the `/react`, `/vue` and `/svelte`
helpers, which set the `data-weft-scope` marker and are not components.
The package has zero runtime dependencies and its version is the weft
version. `studio/web/npm/README.md` has the API table. The root entry
is import-safe on a server (the assembled `index.js` imports `panel.js`
between `ssr-guard.js` and `ssr-unguard.js`, which lend its element
class a placeholder `HTMLElement` where there is none; every export is
a no-op without a DOM). `mount()` is idempotent (a second call reuses
the element the first made, with the new options), waits for `<body>`
when called from `<head>`, and with the helpers takes `enabled`
(`enabled: import.meta.env.DEV` keeps production visitors from loading
the panel). `make devtools-npm` assembles the package in
`studio/web/npm`, runs the package suite against the assembled files
and lists the tarball. `make studio-check` fails if the package's
`panel.js` is not the served one, an `exports`/`types` target is
missing, or `package.json` or the package README names another
version; the same check is the package's `prepublishOnly`, and
`publishConfig.access` is `public`. Publishing is done by hand. A Vite app
that installs the packed package is in `examples/devtools-vite`
(`make devtools-vite-check`).

The host API (plan C4) drives the panel from the page's own UI — a
"debug this" button beside a reply, a "report this run" link — with no
Studio URL in the page. It is the `<weft-devtools>` element's own
methods, one implementation for both installs: under the script tag the
bundle publishes them as `window.weft.devtools`, the one global the
panel adds (below); from npm the package's exports call them on every
panel on the page.

| Method | What it does |
|---|---|
| `open()`, `close()`, `toggle()`, `isOpen` | expand, collapse or flip the dock; `isOpen` reads it (from npm, `isOpen()`) |
| `scope(s)` | the explicit scope (rung 1: it wins over the URL, markers and headers, and its run pins): a `Scope` or its string form (`"pub_…;session=…;run=…"`); `sessionId`/`runId` are read as `session`/`run`, so a `run` event's detail can be fed back; one naming neither a public id nor a session reads `scope: no public id or session`. `scope(null)` clears what `scope()` set and puts back what `mount()` named, so the ladder decides again (a `data-scope` in your markup stays yours) |
| `select(runId, step?)` | selects that turn, and the step by its ordinal (G1), once a `scope()` just before it has settled; ⤢ then carries the step. A run the list does not show is read by id (`GET /api/runs/{id}`, scoped by the token) and joins the list when it is the conversation's (a run with no session id — a playground run — is not judged by a session narrowing, as in the list); else the line `run r_… not in this conversation` (`run r_…: Studio did not answer` when the read failed). A step the run does not have reads `step n not in run r_… · showing step m`, and the last step is carried. A call made while the panel's start hangs waits 10 s, then reads `select r_…: the panel is not connected` |
| `on(event, cb)` → unsubscribe | follows one event (below); a `cb` that throws is swallowed |
| `studioLink(runId, step?)` | the Studio page of that run (and step) through `lib/links.ts` — the link ⤢ carries, never with a token; `""` before the panel knows its endpoint |

A scope naming a session and no public id (`scope({publicId: "",
session: "s_…"})`, or `";session=s_…"`) is resolved through `GET
/api/sessions/{id}/public_id`, which only setup A and the dev token may
ask: under a panel token the panel does not ask, and says `session s_…:
the session lookup needs the dev token` (a 403 reads the same); a
session created without `thread.PublicID` reads `session s_… has no
public id · not recorded …`, an unknown one `… · unknown session`, a
lookup Studio did not answer `session s_…: Studio did not answer the
lookup`. The panel keeps its scope and nothing is thrown.

The events are `CustomEvent`s named `weft:run`, `weft:parked` and
`weft:error`, dispatched from the element (`bubbles`, `composed`) after
the panel's own work (a microtask), so `document.addEventListener`
hears them too. Only runs of the conversation followed are reported
(none in the fallback), and only transitions the panel sees: a
conversation's first read of its list is history — only a run reading
running there, and the run the scope pins, are reported.

| Event | `detail` | When |
|---|---|---|
| `run` | `{runId, status, publicId?, sessionId?, step?}` — `status` the plain word (`running`, `succeeded`, `failed`, `parked` — succeeded with pending calls, a later resume does not unsay it — `interrupted`; the turn list itself reads `parked → tN` and `cancelled`, U1), `step` the run's last step ordinal the panel knows | a run starts, and each status change, once per transition |
| `parked` | `{runId, callId, ackId, name}` — `ackId` is the id the approval names, the call id `weft.Approve`/`Deny`/`Resolve` and `POST /api/runs/{runId}/approvals` (`call_id`) take, so it equals `callId`. The app decides its own turns through its Session, `s.Decide(ctx, thread.Approve(ackId))` — or, when it set `runtime.Sessions(resolver)` at `runtime.Install` and that runtime of the run's service is connected, the panel and the run page decide them through `POST /api/runs/{runId}/approvals` (plan F4.3, below); otherwise the route answers 403 "app-owned turn" with the fix | once per call a run parked on (read from its `run_finish`'s pending list) |
| `error` | `{message, runId}` | a run of the conversation failed: its error, once per run — run errors only (the panel's own trouble, Studio not answering, is a line in the panel) |

The global: while a `<weft-devtools>` from the script tag is connected,
`window.weft.devtools` is its API object (`window.weft` is created only
when absent — an existing plain object gets the one property, anything
else is left alone and the footer says `global: window.weft is the
page's`; a `devtools` of your own is never replaced). It is deleted on
disconnect (with `window.weft` when the panel created it and nothing
else is in it). `data-global="off"` on the tag or element (or `<meta
name="weft:global" content="off">`) keeps it off; the npm package adds
none. `examples/studio-local`'s page uses it: "debug this" per reply,
"report this run" from `on("run")` and `studioLink`, and `on("parked")`
counted into `<body data-parked-count>` (ask it about a refund: the
turn parks on `refund_order`, a `weft.RequireApproval` tool).

The app's own turns (plan F4.3). A parked turn the app started — no
`weft.playground`, a turn of a thread session with a public id — takes
the panel's continue / skip / resolve (the field is skip's reason or
resolve's result) while a connected runtime of the run's own service
registered `runtime.Sessions(resolver)` (`GET /api/runtimes`' `sessions`
— the rule the route itself applies; `meta.debug_scope` is global, so
`breakpoints_own` alone, or another service's Sessions runtime, leaves
the turn read-only) and the turn is its session's latest. The verbs post
to the playground's own route, `POST /api/runs/{id}/approvals`; the
202's command is polled (`GET /api/playground/commands/{id}`) until it
ends — Studio settles a silent runtime's command as lost within
max(10 min, 3 beats), so the poll is bounded — then the resumed turn
shows as the session's next turn and the parked one reads `decided —
resumed as <session>-tN` (a link; a reload says the same, with no verb),
or the runtime's rejection word for word (`rejected: the app is writing
this session; fork instead`). A running app turn takes steer the same
way, its own run id posted — a delivered one reads `steered`; a steer
that missed its turn reads `failed: no running turn to steer for run …
(…)`. Cancel is never offered on an
app turn. Otherwise the block reads `awaiting a decision (read-only)`: the
verbs drawn disabled (`badges.ts`'s refused verb,
`data-weft-refused="app-owned"`) and one line in the server's words —
`app-owned turn · read-only: no runtime of service <svc> is connected —
fix: open the runtime link in the app: WEFT_ENV=dev (or
runtime.Enabled(true)) at runtime.Install, with runtime.Sessions(resolver)`
when the app's link is closed or it is not running, `… run <id> is the
app's own, and no connected runtime of its service <svc> acts on the
app's sessions — fix: set runtime.Sessions(resolver) …` when one is
connected without it, auth.go's `this token is read-only: mint it with
"playground": true to act` for a read-scoped token, and the 403's own
once one has answered (`lib/honesty.ts` holds them; a test pins them to
`studio/appturn.go` and `studio/auth.go`) — until a later runtimes read
says the verbs' gate holds again (the runtime reconnected, the fix
applied), which gives them back; the panel reads `GET /api/runtimes`
at most every 10 s while such a turn is drawn, on a timer too, so a
quiet parked turn follows the link opening without a click. A plain Generate's park (no
session) reads `parked · app-owned run · the app resumes it
(weft.Approve)` with the `thread.PublicID(...)` fix; a turn of a session
created without a public id reads appturn.go's `… run <id> is not a turn
of a thread session with a public id` with the same fix. A child run
is one of three, told apart in this order (`parkedKind`'s table, in
`lib/honesty.ts`): a `weft.Subagent` child (`<parent>/<step>/<call>`)
reads `parked · a subagent's call — decide on the parent turn <parent>`
(a link on the run page), no verb — only the parent's SUBAGENT_PENDING
call is pending on the session; a `thread/pool` child, recognised by
the `weft.session.parent` thread stamps on every pool child's run (in
the run row's `meta`) when the run has no parent run id or is its own
session's turn (`<session>-tN`), reads
`parked · a delegated session's turn — decide on the parent session
<session>` — and, when it ran under a delegating tool call, `· delegated
by turn <parent>` — no verb and no fix: its requests are mirrored onto
the parent session's `Pending()` and decided there (`p.Decide`, ADR 0022
§7). A child `p.Submit` started from app code outside a run carries no
parent run id at all (core stamps `weft.parent.run.id` only from a
running call's context) and reads the same, the session alone. On the
run page the session is a link to `/sessions/<session>` and the turn a
link to its run; the block reads neither the child's session nor the
runtimes for it. Another agent's `Generate` inside a tool handler (a
parent run id, an id of its own, whatever session id, public id and
`weft.session.parent` it inherited — core merges a run's context
metadata into every run started under it, so one nested under a pool
child's turn carries that key too) never parks onto the parent and
reads the plain Generate's line; a session's own `Send` inside a tool
handler (`<session>-tN`, no `weft.session.parent`) is a turn of that
session, gated as any other. A known caveat: a session created without
`thread.PublicID` whose `Send` runs inside another session's tool
handler inherits that session's public id through core's context
metadata and reads decidable; the runtime refuses the decision in its
own words (`the session resolver answered session … for public id …, not
the run's session …`, shown verbatim) — give such a session its own
`thread.PublicID`.
The panel's turn lists are top-level runs, so it draws none of these
children; reached another way, it reads the same words through the same
`parkedKind`/`refusalWords`, without the links. On a
Studio without the playground the line is `… the playground is off on
this Studio` with `meta.capabilities_off`'s fix (both surfaces hand
their meta to `refusalWords`), and a failed read — meta, the session,
the runtimes — is said as such (`could not read the session: …`), never
a verdict and never an endless check; under a failed meta the run page
still draws the run's events and the parked block (`could not read the
Studio's capabilities: …`). A 403 refusal holds, on both surfaces, until
a runtimes read started after it has applied and the gate says decide —
then the verbs are back, even when the runtimes read the same as before
(runtime ids are stable per service, cwd and agents, so a restarted app
registers under the same id); a read that fails, or one still in flight,
keeps it. While the turn is parked or running the parked block reads
the session again every 5 s — not once the next turn is known (resumed
is final) or the session read was refused (a 4xx): a turn another
surface resumed reads `decided`. The runtimes need no poll of the
block's own: while the turn is parked or running and not yet resumed the
block holds the shell's poll of the same query at 5 s
(`use-notices.ts`'s `useRuntimesNeeded`). A failed meta read is held
until meta answers: a block mounted again, or a refetch with its retry,
reads the failure, never `checking`. The run page draws the
same block under the same gate, in the same words, the 403's lifetime
included (`parity.test.ts`). Studio's other capability-gated pages
(traces, live, experiments, playground, compare) say a failed meta read
as `could not read the Studio's capabilities: …`, never that the
capability is off. The demo is `thread/examples/approvals -serve`:
`WEFT_ENV=dev go run ./thread/examples/approvals -serve 127.0.0.1:7331`,
ask "deploy to prod", decide it in the panel docked on the page; a
restart over the same `-dir` resumes the session stored under its
public id (one public id, one session), and a serve whose cached Session
another serve's write left stale (`thread.ErrStale`) closes it and opens
the session again, in its ask and its resolver.
Without `WEFT_ENV=dev` the turn is read-only because the link is closed:
the options are set, but no runtime of the service is connected, and the
line says to open the link.

The scope header (plan C3, rung 2, the development rung) is set by the
app's own handler, since `thread` has no HTTP layer: one line,
`mux.Handle("POST /chat", scope.Header(chat, func(r *http.Request)
scope.Scope { return scope.Scope{PublicID: pubOf(r)} }))` (package
`github.com/weftgo/weft/scope`), sets `Weft-Scope: pub_…` on every
response, and a handler that knows the run once its turn starts calls
`scope.Set(w, scope.Scope{PublicID: …, RunID: turn.RunID()})` before
writing (`examples/studio-local`'s `/run` answers `Weft-Scope:
pub_demo;run=<id>`). It never carries a token. The value is the
`data-weft-scope` marker's string form, pinned for both sides by
`studio/testdata/scope.golden.json`. Both helpers append `Weft-Scope`
to `Access-Control-Expose-Headers`; a page on another origin also
needs your CORS policy to allow its origin. Put `scope.Header` inside
your CORS middleware, or list `Weft-Scope` in its exposed headers. The panel reads the header
through rung 2 of the scope-detection ladder above (on by default with the page and the endpoint on
loopback and no or a dev token, `data-detect="headers"` elsewhere;
same-origin responses, or cross-origin ones between two loopback
origins) — `examples/studio-local`'s tag names no
scope, and its first `/run` scopes the panel to `pub_demo` and pins
that run.
`scope.Header` also learns the page the request came from — its
`Referer`, else its `Origin` + "/" — sanitised (http/https with a host
only; userinfo, query and fragment dropped; a URL over 1024 bytes dropped
whole, never cut) and stamps it as `weft.page_url` on every run the handler
starts: it puts the pairs on the request's context with `weft.WithMetadata`,
so `Generate` and thread's `Send` under `r.Context()` carry it with no option
passed (a run's own `weft.Metadata` wins). `scope.PageURLFromContext(ctx)`
reads it in the handler; `scope.PageURL(r)` applies the rule to any request.
Studio's run row serves it in `meta`; the run page's "open in app" (plan
G3, forthcoming with its web half) returns to that page with `#weft_scope=`.

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
serves `GET /api/runtimes` (the registered runtimes, each with
`connected`: whether it holds a live command stream now — one whose
stream dropped stays listed, `connected: false`, until its commands
resolve, and a reconnect re-registers under the same id),
`POST /api/playground/runs` (the §10.4 validation table: 400 unknown
tool/model names and unsupported 8b modes, 403 raised limits or a
refused side-effect tool, 404 unknown runtime/agent/source run, 409 a
reused command id, 503 with no runtime connected) and
`GET /api/playground/commands/{id}`, plus the runtime link's own
routes (`POST /api/runtime/register`, `GET /api/runtime/commands` SSE,
`POST /api/runtime/acks`). Registration carries `heartbeat_ms`;
while a command is held the runtime re-posts its accepted ack at that
cadence, and a repeated accepted ack is liveness: on an accepted row
it restarts the finish watch (max(10 min, 3 beats)), on a lost one it
is a 409, on a finished one a no-op. Safety: off unless `WEFT_ENV=dev` or
`runtime.Enabled(true)`; overrides only narrow; a side-effect tool's
call is substituted with its recorded result or parked
(`weft.Replay(weft.ReplaySafe)` vouches a read, `AllowSideEffects`
opts a tool into allow mode); budgets cap each experiment; the app's
own runs are never touched. The command's `overrides` carry
`instructions`, `tools_enabled`, `model`, `thinking`, the numeric
`options` (`max_steps` and `parallelism` lower only, `temperature`
0..2) and the option lab's typed knobs: `params` `{top_p?, max_tokens?,
stop?, seed?}` (neutral; laid over the agent's own params — an absent
field keeps the agent's value, and a command cannot clear the agent's
`stop` or `max_tokens`; `top_p` 0..1, `max_tokens` positive, at most
four non-empty `stop` sequences), `tool_choice` `{mode:
auto|any|none|named, name?}` (a named choice must name a tool the
command keeps on and does not park; with none sent, an agent default
naming a tool the command turns off is refused — send `tool_choice`),
`park_on` (a parked call stops there even in substitute mode) and `only_tools` (tools
of the agent; `only_tools` inside `tools_enabled` when both are sent,
403 otherwise). `model` is the agent's own, a `runtime.Models` name,
or — when the runtime registered `runtime.ModelResolver(func(ctx,
name) (weft.Model, error))` — any name the app's resolver accepts: it
runs before the ack (bounded at 10 s: "resolver timed out"; a resolver
that ignores ctx is abandoned at the bound), its error
text is the rejected command's reason
(`model <name>: …`; the app writes that text, so it carries no key or
URL), and without a resolver Studio answers 400 `unknown model … :
register it with runtime.Models or add runtime.ModelResolver`.
`GET /api/runtimes` gives each agent `resolver` and `defaults`
`{max_steps, parallelism, thinking, temperature?, top_p?, max_tokens?,
seed?, stop?, tool_choice}` — the run defaults the form greys beside
each override. Version skew: a runtime older than the option lab
registers no defaults and would run a command without `params`,
`tool_choice`, `park_on` or `only_tools` (it does not know them), so
Studio refuses any of the four for it (400 `runtime predates the option
lab: upgrade weft/runtime …`); an old-shape command still runs. A subagent's child run replays as its own
agent, registered on the runtime by that name (`runtime.Agents(parent,
child)`): the replay carries `weft.forked_from="<child id>#<from_step>"`
and no parent linkage (ADR 0029). A replay from step N is fed what step
N's model call carried, compaction view included; the replayed agent's
own PrepareStep then runs over it, so the model's input is exact when
the agent's PrepareStep is idempotent over its own output — a count- or
index-keyed PrepareStep re-shapes the replay's input. `from_step` may
equal the run's step count when its last step ended in answered tool
calls (a run that failed answering them): the replay's first model call
answers them, so "edit the result, replay from the next step" works.

P1–P5 ride the same command: `transcript_edits` (validated on both
sides — a patch names a call in the kept prefix, the prefix ends at a
step boundary with every call answered, and an edit to a message
`from_step`'s compaction view replaced is refused: the replay prefix is
what the model saw at that step, ADR 0029), `engine: scripted` (the
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
says so. `POST /api/runs/{id}/cancel` (capability `cancel`, plan H6) asks
the runtime holding a runtime-started run to stop it — the link's cancel
frame for the command whose acks named the run; the runtime cancels that
command's own context, never kills, and the run ends failed with `context
canceled` and its partial transcript, its command finished failed; the
live stream carries the failed run row (a cancelled run has no run_finish).
202 `{run_id, command_id, runtime_id, state: "cancelling"}`; 404 an unknown
run; 403 "app-owned turn" the app's own run with no Sessions runtime (plan F4.1, the paragraph below), 409 one already finished
(a parked run answers 409 "decide its pending calls instead"); 503 its
runtime is disconnected; best effort — a cancel lost to a stream reconnect
is not retried, cancel again. Auth is steer's:
the dev token or setup A's loopback, a playground-scoped panel token inside
its public id; a read panel token is 403.

The app's own turns (plan F4.1). A run no runtime started is the app's: its approvals, steer and cancel go through a connected runtime of the run's service (the run row's `service`, the registration's `service`) installed with `runtime.Sessions(resolver)` — `/api/runtimes` reports it as `"sessions": true`, and `meta.debug_scope` reads `"own+runtime"` while one is connected. The route then forwards the verb as an `event: session` command (`{command_id, verb: decide|steer|cancel, public_id, session_id, run_id, call_id?, decision?, reason?, content?, message?, actor?}`, addressed by the run row's `weft.public_id` and `weft.session.id`) and answers **202** `{command_id, state: "queued", run_id, runtime_id}` at once; the client polls `GET /api/playground/commands/{id}`. The runtime claims the app's Session writer lease before it acks, so its refusals arrive on the row as `state: "rejected"` with `error`: `the app is writing this session; fork instead` (another writer holds the session — nothing written), `no session for this public id`, `the session resolver answered session <id> for public id <pub>, not the run's session <id>` (the resolver handed back another session under that public id — one public id naming two sessions, e.g. an app that creates a new session per process start; nothing written), `this session requires signed decisions: …` (a `thread.RequireSigned` session — the app decides it with `DecideSigned`), `cancel is not supported on an app turn` (thread has no per-turn cancel), or the resolver's own error. Two answers arrive after the ack, as `state: "finished"`, `status: "failed"`, nothing written: a steer whose run is not the turn running now (`no running turn to steer for run <id> (…)`, naming the run running instead when there is one — `thread.Session.Steer` decides it under the session's lock with the run's id, so the call never starts a turn and never lands in a newer one; a delivered steer that misses its drain point — StopWhen, a park, the run ending first — still becomes a follow-up turn under ADR 0019's rule: the finished ack then reads `succeeded` for the named run, and the follow-up turn shows up later as the session's next run), and a lease lost between the claim and the write (the locked sentence). With several replicas of one service, Studio picks the Sessions runtime seen most recently, which may not be the process holding the session: it answers `the app is writing this session; fork instead` (a shared store) or `no session for this public id` (per-process stores) — misleading, but safe: nothing is written. A decision's finished ack names the resumed turn (`<session>-tN`, the app's run — never `weft.playground`). Without such a runtime the three verbs answer alike, **403** `{"error":{"code":"forbidden","message":"app-owned turn · read-only: …"},"fix":"set runtime.Sessions(resolver) at runtime.Install in the app …"}`; a run that is not a turn of a thread session with a public id is 403 "app-owned turn" with the fix `thread.PublicID(...)`. Identity rules are every playground verb's: the dev token or setup A loopback; a playground panel token inside its own public id (another's 403); a read token 403.

**Derived runs.** A stock OTel app (no weft) that exports GenAI spans with
`gen_ai.conversation.id` gets run rows (docs/record-contract.md §9): one per
root `invoke_agent` span, id `otel-<trace id>-<root span id>`, `derived: true`
on every run row (list, page, search, session), `requests_badge: "derived"`.
Its page's `holes` is `[{"hole":"derived","reason":"derived from spans · no
events","fix":"the producer exports stock OTel GenAI spans only: emit weft's
records (docs/record-contract.md) for events, the transcript and requests"}]`.
What each route answers for it: `runs/{id}/events` — `events: []`, `done:
true` and that badge; `runs/{id}/transcript` — `batches: []` and the badge
(`?step=N`: 409 with it); `runs/{id}/spans` — the real spans (the waterfall),
`weft.step.index` / `weft.tool.seq` filled by the reader; `runs/{id}/requests`
and `/tools` — empty, badge `derived`; `runs/{id}/steps/{n}` — the step
assembled from its `chat` span (its holes include `derived`; steps.go is not
yet derived-aware, see I1.2's open items); `runs/{id}/logs` — the app
logs under its spans; `runs/{id}/export` — json/jsonl/otlp carry the row
and spans with every other block badged, `wefttest` is 400 with the badge;
`diff` — every column `unknown` against a derived side; the playground's
fixtures and preview refuse a derived source 400 with the badge (there is no
transcript to replay). A derived run has no public id, so a panel token never
reads it (403).

The Live page derives a running
row from the event frames it already receives: step = the last
step_finish's index, tokens = the sum of step_finish usage, TTFT = its
ttft_ms; cost stays unknown while running. The panel's experiment drawer (the §3 form, the live result
with the inline diff, the approvals, the 2-way compare) and the
Studio `/playground` page (variants side by side with the metrics, the
E9 variants × inputs matrix, the experiment history) both render them,
each control only for its reported capability. The run page's replay
drawer (plan F1: its verbs on every step, call, steer and child row, the
ack preview naming what each tool's calls would do) posts the same
command, and the playground's "continue from step" is a picker over the
source run's steps.

Every matrix is saved as an experiment (plan F5). Experiments in the
sidebar (`/experiments`) lists them with their agent, size, run count and
cost. `/experiments/{id}` draws the variants × inputs grid, each cell its
latest run with status and cost. From there you can re-run one cell or all
of them under the same experiment id, compare two cells on the compare page,
or "clone into the form", which reopens the playground on a draft holding the
matrix's form — exactly, in any browser: the experiment saves the whole
command (engine, side effects and thread per variant, `from_step`, the source
run, the transcript edits), and only a definition saved before that is read back
from its runs. The matrix's name is free text; its id follows command_id's
rule (`[A-Za-z0-9._:-]{1,128}`): a name that fits is its own id, any other is
slugged with a per-page nonce, an empty one is `exp_<time>`, and a reopened
experiment keeps its id until you rename it. Because of that nonce, the same
out-of-rule name in another tab or after a reload makes a separate
experiment; only a name that fits the rule re-runs the same one. "Its runs" opens
`/runs?experiment_id=<id>`, which `/api/runs` filters. The playground
keeps your form as a draft in this browser once you change it. `?exp=<draft
id>` reopens it, and the drafts list opens or deletes the others. Drafts
never leave the browser. "Copy share link" carries the whole form in the URL
fragment (`#state=`, no token, at most 8,000 characters encoded). It opens
against the same Studio, restores the form, and runs nothing. The page has
one step (U26): the source block's step picker sets the step Run and every
matrix cell continue from (the source run's step ordinal), and the page's
transcript edits go with each cell past step 0 — a cell is Run with its
row's input; past step 0 the rows' text is not sent. The form's format is
v2 (`FORM_VERSION`): a v1 share link or draft, which carried a second
step for the matrix, is refused, never read with one step dropped. When its last cell settles, one toast says
how many succeeded and failed, with the bill when pricing is on (an
experiment page's "re-run all" re-reads the experiment at most three times
for a run not yet listed, then says "cost not read yet"). A share link's
value is refused past 8,000 characters, or when it inflates past 256 KiB.

The option lab (plan F3) is one form on both surfaces — Studio's
experiment form (the playground page and the replay drawer) and the
panel's experiment drawer — with a knob for every override the command
carries: max steps, parallelism and temperature (`options`), top_p, max
tokens, stop (one sequence per line) and seed (`params`), tool choice
(auto, any, none, named and its tool), only tools and park on, beside
the prompt, tools, model and thinking. Each knob shows the agent's
default from `GET /api/runtimes` greyed (the placeholder, or the
default option's words); a value that differs from it is drawn in
colour with a `↺` back to the default; an empty knob, or one set to the
default, sends nothing ("absent keeps the agent's value" — the form
cannot clear the agent's `stop` or `max_tokens` either). The model is a
select over the runtime's models, plus free text when the agent's
runtime holds a `runtime.ModelResolver` (the app resolves the name; its
refusal is the rejected command's reason, shown as given). Widening is
never silently sent: `src/lib/experiment-body.ts`'s `labOverrides`
mirrors the server's rules by name — caps lower only, the ranges, a tool
the agent lacks, only tools inside the tools left on, a named choice the
command turns off or parks, the agent's own named default turned off —
says each refusal on its knob in the server's words and holds Run; a
tool turned off is greyed out of only tools (a box already ticked stays
clearable) and the named tool picker; park on lists every tool of the
agent, unrestricted. A cap of 0 is no bound: no default shown, no
refusal. The server stays the authority: its 400 is shown verbatim where
the command's error goes. A registration without `defaults` (a runtime
older than the option lab, whose `defaults` `/api/runtimes` omits, or a
Studio older than the field) shows the knobs with no default and greys
the seven option-lab controls the old runtime cannot carry — top_p, max
tokens, seed, stop, tool choice, only tools, park on — saying why
(`runtime predates the option lab …`). The model's free text is kept as
typed (the name sent is its trim). The ack preview follows park on
(those calls park) and only tools (the others are off). Both surfaces
build the command's overrides through `labOverrides`, so they post
byte-identical JSON for the same choices (`src/panel/optionlab.test.ts`:
a table over `testdata/api/runtimes.golden.json` and the parity check).

The transcript editor (plan F2) is the Story view's, on both surfaces —
the run page and the panel's Story tab — under the replay drawer's gate
(`canReplay`) and on a run that is over: step 0's prompt, a steer, a
call's args, a tool result and a call-free reply turn into a textarea in
place (click the thing, or Enter on its pencil), with revert and a red
"edited" chip, and every step boundary up to the highest `from_step` the
server accepts takes an inserted message ("· insert a message before
step N" — a steer at rest). The edits are the five kinds of
`transcript_edits` — `user` (`step`, `index`), `tool_args` (`step`,
`call_id`, `args` an object), `tool_result`, `reply`, `insert` — and
accumulate into one command: the first opens the replay drawer ("Edit
the transcript and replay"), which lists them by kind (each droppable),
keeps `from_step` at what they imply (an edit of step N keeps N, so
N + 1; an insert at boundary N, N) and runs them on engine live, saying
why (the scripted engine replays recorded turns, which answered a
different prompt). Args are JSON, checked in the editor against the
tool's input schema from the step's catalog record (`src/lib/edits.ts`'s
`checkArgs`, `obsdb.CheckToolArgs`' rules by name — type and type lists,
integer literals, enum, required, properties, additionalProperties,
items, a null field absent): a refusal is said on the field in the
loop's `INVALID_INPUT: tool "x": field "f": expected …, got …` words,
never sent, and holds Run — the check matters most for a read-scoped
preview, whose server check is the object shape only. Both surfaces
build `transcript_edits` through `lib/edits.ts`'s `wireEdits` (`kind`
always), so they post byte-identical JSON. Above Run, "will be sent"
(capability `preview`; nothing asked for without it, nor for a fork):
`POST /api/playground/preview` with the very body Run would post,
re-read 300 ms after the form rests, drawn through `src/lib/preview.ts`
— the system's state (`same`, `changed`, `unknown`, `hidden`), the
messages one row each with the server's op (`same`, `changed` with was
struck through above will, `added`, `removed`), tools added and removed,
the knobs that changed (model, params, thinking, tool choice), every
`*_badge` from the honesty table, the warnings one line each
(`prepare_step` when a request record of the source names what a
`PrepareStep` changed — its `prepared` parts listed — or the source
recorded no request; `catalog` when the registered agent's tools differ
from the recorded step's; `agent` when the replay's agent is not the
source run's; `compacted`, `derived`, `scripted`,
`instructions`) and `unchecked` as a line naming what only a runtime
checks. Its 400 is the command's own sentence, shown verbatim, and
holds Run. A replayed run whose row carries `weft.edits` opens its Story
with "kept prefix edited": one chip per token — "args edited", "result
edited", "edited" (a user message or a reply), "inserted" — at its
source coordinates, a call's edited args or result read from the
replay's input by call id (the kept prefix is positional); a child run
inherits the mark and draws none.
An editor follows its command: Escape inside a Story textarea is the
field's (the drawer stays); closing the drawer, another verb's opening,
a drop from the list or the drawer's own field rewriting the edit resets
the textarea to what the command holds (the recorded text when nothing).
A steer's user edit is the steer's own step's (its stored step, then its
place among that step's user messages). The schema is the run's last
catalog naming the tool (studio's server checks against it; the edited
step's own catalog is a Go-side debt). Numbers the browser would change
(past 2^53 exactly, out of range) are refused; without JSON.parse source
access the integer-literal check is marked unchecked, never passed. A
fork never carries edits — the drawer says so and holds Run. Run also
waits while the preview of a changed command is pending, and an edit
inside the compaction view `transcript?step=<from_step>` names
(`compacted_at`) is refused in the server's words before anything is
posted.

The step compare (plan E3, capability `diff`; hidden when off, like
every capability-gated control): two or more runs side by side, one row
per step ordinal, a column per compared field — system, tool calls,
tool results, text, usage — each cell `same`, `changed` (with the
value), `not comparable` (the server's `unknown`: a side did not record
the column, never read as the same) or `missing` (one run has no such
step), and above the table one "changed at step N" marker per changed
step, linking to that step. Each side's marks (`compacted`, `subagent`,
`max_tokens`, `parked`, `running`, `interrupted`, `error`) are chips
beside it — never a change by themselves — and its holes are badges
from the one table, as is the response's own `truncated`
(`response_cap`). Both surfaces read `GET /api/diff` through one module,
`src/lib/stepdiff.ts`: Studio's `/compare?a=<run>&b=["<run>",…]&step=N` page
(`b=r1,r2` reads the same; it opens with a run picker — H9,
`lib/compare-picker.ts`: the newest runs as a base, then the base's
replay source and its agent's runs to compare with, one click each;
the table scrolls inside its own frame, never the page;
`lib/links.ts`'s `compareLink`; N-way is N−1 calls of the 2-way route
against the base run `a`, drawn in the first column; `step` highlights
and scrolls to that ordinal's row; a mark the side's holes already
carry is drawn once, as its badge; a system prompt withheld from a read
token is the `hidden` badge in its cell, beside the state the hashes
gave) — reached from the
run header ("compare with source" for a replayed run, via
`weft.forked_from`, and "compare with…"), the replay drawer's finished
result, and the playground (every finished variant against the source
run, beside the final-text diff); the panel's experiment result pane
draws the 2-way table (the replayed run against its source) at panel
width — "reading the step compare…" while it loads, each marker a link
to its step — with a ⤢ hand-off to the same compare page at the first
changed step.
`src/panel/parity.test.ts` holds both to the same rows, markers and
badges from the same goldens (`studio/testdata/api/diff*.golden.json`).

Status words (U1) are one table, `src/lib/status.ts`, on every surface
— the runs list, the run header, a session's turns and its header, the
live table, the panel's turn list, picker and filter: `running`,
`succeeded`, `failed`, `interrupted`, and two read-time words over the
stored four — `cancelled` (failed with `error_type` cancelled, the cancel
cause in its title; the `[cancelled]` kind chip is not drawn beside it)
and `parked` (succeeded with pending calls) while nothing is known to
have resumed it; once a later turn of its thread session is among the
rows read (or, from U13.1, the row's `resumed_by`) it reads
`parked → t2`, a link to that turn (in the panel, a click on the word
selects it). The certainty is in the detail, never the word: "1 call
awaiting a decision" only where the resume was looked for and could not
be missed — a session turn read against its session's whole turn list
(the session page, the run header once its session loaded, the panel's
session-scoped list); elsewhere (the runs list under a filter or sort,
the live table, an experiment's grid) "1 call pending · no resume
recorded here", and for a plain `Generate` "1 call pending · a resume,
if any, is not linked yet". A decided park's detail says "1 call decided
· resumed in t2" only on the stored `resumed_by`; a park found by the
next turn alone says "1 call · next turn t2 (decided or dropped)" — after
a Branch, a Rollback or an undecided Send that turn is no resume. The
sessions list's status is the newest turn's stored word (no parked /
cancelled until U16): its row carries only `status`. The runs list's status filter has `parked`
too — the API's `status=parked` (I3): the open parks by the stored
link, no run whose `resumed_from` names it. The page's own word reads a
park decided also when a later turn of its session followed it
(`nextTurn`), so a run recorded before U13.1, or resumed without
`weft.ResumeOf`, is listed there while it reads `parked → tN`: such a
row says "decided by a later turn the record does not link"
(`lib/status.ts`'s `unlinkedDecided`, P8-fix-b); the live table's filter matches a status
word by its key, not its text — `parked` never matches a decided park,
and `failed` no longer matches a cancelled run (whose key is
`cancelled`); any other word matches the agent or the shown word as
text. Each word has one colour role (`src/lib/palette.ts`'s `ROLE`), the
same hex on both surfaces: running `--status-run`, succeeded
`--status-ok`, failed `--status-bad`, interrupted and cancelled
`--status-int` (the words differ), parked `--status-parked`, a decided
park muted; the live dot is the succeeded colour. A role's word is drawn
in its ink (`INK`): the running word in `--status-run-ink`, 4.5:1 on
both themes, the dot, pulse and pill border in `--status-run`.

The runs list (light): status as a dot and a word, the error under a
failed run's id, session/public-id/experiment filters that mirror the
URL, the session column linking each turn to its thread, and a follow
toggle that streams an agent's runs as they happen:

![runs list, light theme](screenshots/runs-light.png)

The run page (light) — the prompt and the answer first (the answer now
comes from the transcript: deltas are live-only), then the facts; the
**waterfall** (the default view, U8.2) is the run tree —
`src/lib/waterfall.ts`'s `waterfallRows`, projected by
`src/lib/run-tree.ts`: run › step › model call (› attempt) / tool lane ›
child run, each row its label ("model · script", the tool's name, never
the OTel span name or a per-row service), a gist ("1 tool: ops_manager",
"→ 1 call", a reply's or result's first 60 characters), a badge (the
finish reason, the error's kind, ok) and its duration under "ms" (its
offset on hover), the critical path a thin accent under the bars with a
legend. It draws on the **time** scale when the run has spans and the
**order** scale (stream positions, the replay playhead's) otherwise;
`?scale=time|order` picks and the switch writes either value (a pre-U8.2
`?axis=time|events` link reads as time|order); the `t` key steps the
scale (time → order → graph; `s` stays the story's). A step or call the
run's events have and no span covers is drawn, never left out (U8.2-fix):
while the run goes on it sits after the row before it, no duration,
marked **spans not reported yet**; once the run ended it is badged
`not_recorded` (dropped on the way or never exported — `truncated` when
the tree's own cap cut it). A max_tokens step's call the loop never ran
(rule 11) has no span by design: it reads **not executed**, no badge
(P8-gate-fix; the panel's Timeline the same). The dim filters (`?dim=errors,slow,tools`,
the **dim all but** chips) dim every row the active filters do not keep
— never hide it; a kept row's ancestors stay lit. With the `spans_tree`
capability the page reads `runs/{id}/spans?tree=1` and a delegation's
child run is one collapsed row under its tool call with **open run ↗**;
selecting it shows the call's detail, the subagent block. Its chevron
(or →/←) expands the child's subtree in place (U9.2): its steps, model
calls and tools from the same `?tree=1` spans, its own children again
one collapsed row each — drawn on expand, never fetched, unless the
tree's `truncated` hole says the server cut it, when the expanded
child's own `?tree=1` is read and merged; the child's events (every
page, up to 20,000 — past that a `truncated` badge on its row; its gaps
a `gap` badge) and run document are read on expand too (a time row's
gists, the order scale's rows), and read again every 2 s while the
child runs (before its document answers, while the run runs) — its
stream from the position past the last one held, from the start again
only when that read held gaps; a finished child is never read again.
Selection crosses the boundary: a `?sel=` naming a row inside a
child (its run-qualified `rowKeys` key, `s:<run>:<n>`, `c:<run>:<at>:<call>`,
or `<child run>:<span id>`) expands the children above it — a key inside
a child no row draws selects that child's row — and folding a
child that holds the selection moves it to the child's row. The tree's
holes are badges above it (`truncated`, `not_recorded` / `no_spans` —
also on the finished child rows it names, in the server's words; a
running child has no span yet and reads running —, `hidden`). **whole
trace ↗** opens `/traces/<trace id>` with the selection kept, the run's
own rows always run-qualified (the trace page keys bare only its own
root run, and reads that root's qualified keys back). The third scale,
`?scale=graph`, is the loop graph (U34, `src/lib/loopgraph.ts` +
`components/studio/loop-graph.tsx`, inline SVG, no layout library): the
selected run's loop off the run tree's rows — the spans on a run with
them, the fold on a span-less one (the switch then offers order and
graph) — prompt → model ⇄ one node per tool (its calls ×N, failures ✗,
denials ✋, parks ⏸ with a dashed result edge, the slowest and p50 on
hover; MCP tools in an `mcp:<server>` frame; at most 12, then `+N
tools`), the end in the stopped-by words, the tools the request catalog
offered and the run never called dashed below (a child run's catalog is
not read here: none drawn, never "none"). Above it, at most three
insights, each selecting its node — the run's budget end (max_steps,
loop_detected, usage_limit, model_retries_exceeded), the output limit, a
repeated identical call (the fold's arguments; content-off, none
claimed), a tool that always failed, the tools never called. A subagent
node expands in place (⊕, or → on it): its own loop, with the breadcrumb
`director › ops-manager`, three levels deep, then `open run ↗`. Below,
the step scrubber (`?step=`, J / K, ▶ play at 0.8 s per step, off under
reduced motion) lights only that step's edges. A node is a button on
one roving tab stop (→ follows the calls, ← back, ↑ ↓ along the tools,
Enter selects, Esc clears); it selects in `?sel=` — a tool its first
call's row, with `‹ call i of n ›` above the drawer walking every call.
A tool's calls are in call order (step, then `weft.tool.seq` — never
span start, which parallel calls reach in any order); a RETRY result is
a retry (↻ "retried", lib/error-kind.ts's tool_errors rule), never a
failure; a repeated call is matched on its arguments by run, step and
call id (an id repeats across steps), content-off's null arguments
never matching. The end node selects the last step's model call; an
edge selects the first call along it; a never-called insight focuses
its node and marks it pressed until any other selection clears it (one
node pressed, never two). Selecting the last step's model row (from the
tree, the drawer or J / K) lights the end node, not the model node: that
row is the end's — accepted. Unknown offered tools are a `not recorded`
badge (a child loop's catalog is not read on this page); a failed
requests read is said in words, in the status colour ("… could not be
read: <error>", as the attempts pane says one; no badge — `gap` is a
dropped record), never "none". A child loop's scrubber
selects its own step rows (`s:<run>:<n>`), `?step=` staying the root's.
A subagent node carries its child run's own cost (lib/cost.ts's words,
`—` with the reason when unknown; capability `pricing`), on both
levels; a child row not read draws `—` with the reason, never nothing:
"a run's cost is not read yet" while the document that carries it is in
flight, else "not read on this page" (a panel token's hidden child, an
agents-level grandchild under no expanded child). `?level=agents` is the call graph (`src/lib/graph.ts`) over
the same spans: one node per agent (its runs, the slowest, failures,
tokens when the spans carry them) in call-depth columns, one per MCP
server (a tool whose request-catalog source starts `mcp`, the run's own
calls; a child run's MCP calls are said not shown, its catalog not read
here), each edge its call count and slowest call (the slowest, not a
percentile; an agent node's runs' own cost), a self-call a loop over
its node; off on a run without spans — a `not recorded` badge only
when the spans read back empty once the 30 s late-spans poll after the
end is over (or the run has no trace), the read's error in words when
it failed, nothing while it reads or polls; a node
selects its first run's row and the selected row lights its node —
switching scales keeps the selection. Pool children are sessions, not
runs of the trace: not drawn. The selected row opens the drawer beside
the tree, answer first (U10.1, audit §6.3): its kind label (RUN, STEP,
MODEL, ATTEMPT, TOOL, CHILD RUN, SPAN), the verbs drawn with their words
— **replay from here ▸**, **edit…** (the prompt, or a tool row's result),
**compare…** (`/compare` landing on the step; capability `diff`), the
child row's **replay the child ▸** — hidden, not disabled, without the
playground, under a read token or while the run runs, one faint line in
their place saying which ("replay needs the playground — <capabilities_off's
reason>", "a read token cannot replay", "replay after the run ends";
a derived run the stock_spans words; `src/lib/replay.ts`'s
`replayOffWhy`); the tabs are a tablist (ids, roving tabindex, ←/→,
Home/End); then, for a step of the run or its
model call, attempt or tool, the answer lines from `src/lib/summarize.ts`:
**verdict** (where the run failed at this step, its stop and the error's
detail — "✗ usage limit reached (input 300/250, output 150/250) — the
loop refused step 3", the refusal said for a guard's stop only:
usage_limit, loop_detected, model_retries_exceeded, max_steps; a step
the error names that the run never reached — max_steps' "at step N"
after steps 0…N−1, a cancel before a step began — reads as the last
step — then finish, tokens, latency, first token),
**sent** (messages, how many new, tools offered, the record's
changed-at-this-step marks), **replied**, **tools ran**, **then** (the
next step, or the run's end card) — on a derived run read from the
step's spans (finish and tokens from the chat span's gen_ai attributes,
its execute_tool spans as the calls; what no span carries reads "— a
derived run has no event record"), the run landing by the same rule;
then the row's facts (a model call's
who-answered and attempts, a tool's failure kind). Below, the lazy tabs
**Request · Response · Tool I/O · Logs · Raw OTel** — only the open one
is drawn; Logs reads `runs/{id}/logs` when it opens — every page, up to
10,000 lines (`src/lib/logs.ts`), a longer list badged `truncated` — and
lists the lines under the row's spans (the route's `not_recorded` hint
for a run without `otel.Slog()`; a running run's row with no line yet
says its spans are not reported yet, and the page's `partial_reason`;
read again every 5 s while it runs); Raw OTel holds the span's attributes and events (and, for
a row the fold holds, its slice of the event stream and the folded node).
`?tab=` names the open tab (absent: a step or run its Response, a model
call its Request, a tool its Tool I/O; a pre-U10.1 `?d=` reads as Raw
OTel). The run's **logs** view (`?view=logs`, key `l`, U11.2) lists
every line with a severity filter (`?lsev=info|warn|error`, the route's
`severity=`) and per line **jump to span** — `?sel=t:<span id>`, the row
that span (or its nearest drawn ancestor) is behind, its Logs tab open.
A tree row a WARN / ERROR line landed on carries a tick ("E1 W2", read
at `severity=warn` only when the run document counts such lines;
"≥E1 W2" when that read hit the 10,000-line cap); its click opens that
row's Logs tab. While the run runs the lines are read again every 5 s
from the line after the last held (never from 0, never once cut); the
finished run's read is whole, once. The devtools panel hands each Story
step off with "📄 ⤢" (`lib/links.ts`'s `logsLink`: the step's row, its
Logs tab), reading no line itself. With no `?sel=`/`?step=` the page lands where something went
wrong (UX-A-03): the first failed row (never the root, never an attempt),
else a tool that never finished, else the step a failed run failed at
("run failed at step N": a model error or a cancel during step N lands
on step N, the open one; a step no row draws, on the last drawn before
it), else step 0 — on the scale shown; "past the
playhead" is said only while replaying. **read in the story** opens the
story on the selection's step: the story is the read mode of the same
selection (a `?sel=` names the card it highlights). "trace" names the
`/traces` page only. In the story,
subagent children sit as nested rows under the step
that called them (agent, status, usage, holes, a link) and expand
lazily — their events and their request record (the child's prompt,
read by the child's id) are their own runs', fetched on demand. Each
step card's **Request** pane shows what the step called the model with:
the system prompt (diffed against the previous step, or at the first
step against the registered instructions when they differ; never over
a cut record, and "too large to diff" past 2,000 differing lines a
side), the tool catalog (description, schema, policy chips), every
param ("adapter default" when nil), tool choice, thinking, the attempts
and the messages sent (count and bytes computed from the transcript as
JSON, the last three inline, the rest as a raw tree), with the chips the
hashes decide — **changed by PrepareStep** (`system_hash` moved with
the tool set unchanged, or at the first step is not `instructions_hash`
plus the offered tools' snippets as a manifest verified for the run
names them), the neutral **prompt changed at this step** when a changed
tool set may explain the move (a ToolSource tool, an unverified
weft.json), **overridden by experiment** (the invoke_agent span carries
`weft.override.instructions`; the story view reads the spans once the
run ends) and **catalog changed at this step**; a read-scoped token
sees the `hidden` badge and nothing else
(`examples/studio-local`'s PrepareStep trim is the live demo; a derived
prompt or catalog record is badged and never diffed over). These chips
are what the record attributes to user code today: only effects are
recorded — a PrepareStep, a WrapModel or WrapTools middleware, a Tap is
never named as the hook that acted (`docs/record-contract.md` §6, "What
the record attributes to user code today"; the hook on the request
record is U25's, the middleware onion U23's). The open
pane has two tabs, **request** and **Wire** (plan A6.2,
`components/studio/step-wire.tsx`): per attempt, the provider request
(method and URL, headers, the body — pretty-printed when its media type
is JSON and it parses, the bar then saying "pretty-printed" while Copy
copies the wire bytes; "binary, N bytes" with a copy when base64) and response
(status, media type, headers, body or raw SSE text), from the requests
row's `raw`; the withheld credentials are listed ("2 headers withheld:
…", "1 query parameter withheld: …"), the 256 KiB cap's cut is a
`truncated` badge naming the bytes, a body not read to its end
(`complete: false`) a `truncated` badge "incomplete: the body was not
read to the end"; a row without `raw` shows its `raw_badge`,
`raw_reason` and `raw_fix` (`not_recorded`, cause `raw_off`), a raw
record that did not parse the `derived` badge, a read-scoped token the
`hidden` badge once, in the pane's header; each pair downloads as .json. The
tab is remembered per step and URL-addressable: `?step=N&pane=wire`
(`pane=request` for the request tab) opens that step's pane on it, in
the story view. A long run
scrolls (plan H2): past 60 steps the story is virtualised, past 200 rows
the waterfall and the raw table are, mounting only the rows in view plus
an overscan (and the row the URL names); a **jump to step** scrubber
heads the story (range and number box, `n`; one `?step=` history entry
per scrub, written in place while the drag goes on); and the page keeps the stream's
last 50,000 events in a ring (`lib/ring.ts`'s `EVENT_RING_CAPACITY`) — a
run past it says which positions it shows, the earlier ones read from
`api/runs/{id}/events?after=`, and turns replay off. Live deltas are folded
into the story's words but never kept in the ring: they have no position on
the API, so the window's positions, its `base` and the eviction note count
positioned events only:

![run page with the waterfall, light theme](screenshots/run-light.png)

Sessions list a thread's turns in order with usage and the newest
status; `/live` shows everything streaming right now (plan H6, H9 — one `/api/live?all=1&kinds=event,run,attempt` stream opened with a grant `{"all":true}` when `/api/meta`'s `live_all` is true, the dev token or setup A's loopback: run frames add and refresh the rows, event frames keep each row's step — the newest step index, "…" while a step runs — tokens so far (input + output from step_finish usage, one figure per step index so a re-delivered frame replaces rather than adds, "≥" when the page joined mid-run) and TTFT; cost is the row's own, "—" with "running: usage arrives at run end" until the final run frame prices it; the running list seeds the rows since run frames are never backfilled; the header shows the stream's state — connecting, live, reconnecting, overflow, stopped with a retry; a reconnect resumes with `Last-Event-ID` and the footer states the bound, runs running or seen in the last hour, newest 500; a run the resume could not read gets a "could not read" `gap` badge; the rows are trees — a subagent under its parent, indented — and a step's newest attempt frame is a line under its step ("↻ attempt 1 failed" while the run runs, the error type and the provider's wait in its title; "ok on attempt 2" after); the session cell is the session's short id with the public id under it; finished runs stay a minute, dimmed, then drop unless peeked, and the first load also lists the runs that ended in the last minute (a reload keeps them); a filter box narrows by agent or status — a status word (`running`, `failed`, `cancelled`, `parked`, …) matches by its key, so typing `failed` no longer matches a cancelled run and `parked` no decided park, any other word the agent or the shown word as text; without `live_all` — any panel token — the page polls the running list every 5 s and says "live: polling every 5 s — the all stream needs the dev token or loopback"; under capability `cancel` a runtime-started run's row gets a cancel button — the first click arms it for 3 s, the second posts `POST /api/runs/{id}/cancel` and the row reads "cancelling…" until the run's row ends — looked for among every row, the filter aside — or 30 s pass without an end, when the button is back titled "no end seen — cancel again" (a cancel is best effort); the end is toasted as "run X cancelled · ended failed", or "run X ended <status>" when it ended otherwise, and a refusal in the server's own words; an app-owned run's button is disabled, "cancel is not supported on an app turn" (the runtime refuses it: thread has no per-turn cancel), a parked run's says "parked — decide instead" and links to the run page; clicking a row or its peek button opens that run's story beside the list in a resizable pane remembered per device as split `live-peek`, its run id in `?peek=`, Escape or the close button closing it); `/traces` lists
every trace the database holds (`GET /api/traces`, capability `traces`:
root span, service, start, duration, span count, error, the runs it
holds linked; since/until/service chips, paged by the exact cursor and
deduped by trace id, a rootless trace carrying its `gap` badge);
`/traces/{id}` renders any trace — a stock OTel GenAI app's included —
as a span tree plus a chat view when semconv content was captured;
`/settings` says what this Studio is and serves.

The trace page (plan H7): every selection is a URL, `?sel=` the row's
step-qualified key (`src/lib/trace-sel.ts`), the run tree's own
(`src/lib/waterfall.ts`'s `rowKeys`, U8.1-fix) so a link names the same
row on the run page — `r:<run>` an invoke_agent span, `s<step>:m` a chat
span (a step's second `:m2`), `s<step>:a<n>` an attempt of it,
`c:<step>:<call id>` a tool execution (`c:resume:<call id>` a resumed or
denied call, its span the one carrying `weft.tool.approved` or
`weft.tool.denied`), the trace's root run bare and every other run
run-qualified (`s:<run>:<step>:m`, `c:<run>:<step>:<call id>`),
`t:<span id>` for any other span and for a span wanting a taken key; each
row's label links to it (a plain click selects in place, replacing the
URL; `y` copies it). An old bare `c:<call id>` lands on the first tool
span of that id, saying "an old link: this call id repeats across steps;
showing the first" when it repeats; a pre-H7 `?span=<span id>` still
selects, and so do the pre-U8.1-fix forms (a chat span's `s<step>`, the
root run qualified, a denied call's `c:<step>:<call id>`). A model call's chat
section draws its messages role by role from either key family —
`gen_ai.input.messages` / `gen_ai.output.messages` (JSON `{role, parts}`
arrays, `tool_call` parts folded as name + arguments) with
`gen_ai.system_instructions`, else the legacy `gen_ai.prompt` /
`gen_ai.completion` (plain, `gen_ai.content.*`, or indexed
`.N.role|content`, `.N.tool_calls.M.*` on either side and a tool
result's `gen_ai.prompt.N.tool_call_id`) — and a
model call carrying neither says `not_recorded` (weft's own chat spans
carry no content; nothing is reconstructed). The raw attributes stay
listed beneath. An errored chat span of a playground run
(`RunRow.playground`) offers "replay from this step": the run page's own
drawer, pre-filled with the run and step ordinal, shown under the
`playground` capability and a token that may act (`canReplay`); on the
app's own run it is disabled, titled "the app's own run: replay runs
through the playground". The header lists the trace's root, service,
start and error, and its runs (the spans' `weft.run.id`, sorted) linked;
"all traces" under the `traces` capability.

Settings (plan H4): `/settings` (a sidebar entry, the palette's Settings
item) is one card per section — Studio (endpoint, versions, pid,
`interrupted_after_ms`, ingest), Token, Database, Retention, Pricing,
Capabilities, Runtimes, Content policy, Appearance, Keyboard. Served
(read from `/api/meta`, `/api/settings/pricing` under `pricing` and
`/api/runtimes` under `runtimes`): the versions, the database (its path,
size and the pid badged `hidden` to a caller `db.path`'s rule refuses),
retention, the pricing table, each capability off with its
`capabilities_off` reason, the runtimes with their breakpoints (the
playground's own editor, under `breakpoints`), the latest run's content
mark with its note and fix. Per browser: the token (`studio.token` in
localStorage — masked, shown on request, copied, cleared, or switched by
pasting another, which re-reads everything with it; its identity is
read from its shape: a `weft_pt.` panel token names its scope and public
id in its claims, any other bearer a walled Studio accepts is the server
token, confirmed when meta served `db.path`'s fields), theme
(auto/light/dark), density and the pricing edits. Studio never writes a
file, so the pricing table's edits stay in localStorage (keyed by the
served table's source and dated; "reset to the server's table" drops
them) and are never sent; "download pricing.json" writes them as the
override file — `{dated, note, models: [{provider, model_glob, input,
output, cached_input?, cache_write?, reasoning?}]}`, refused while a
field `LoadPricing` would reject is wrong — to run Studio with through
`--pricing <file>` or `WEFT_PRICING`. Applying an edit is that download
and a restart: a live `PUT /api/settings/pricing` is not offered.

Search and the palette (plan H1): the Runs page has a free-text box
(`?q=`, debounced; every term must match the prompt, answer, error or a
tool name, a "quoted phrase" one term) and filters by agent, status,
tool, model, error, date range (a day is UTC, until inclusive; a
day that does not exist or a time without its zone — RFC 3339 as the
server parses it — stays a "not applied" chip with a note, never sent),
duration, tokens, cost (with `pricing`) and any number of tags, plus
column sorting on started, duration, tokens and cost — each filter a
removable chip in the URL (`src/lib/run-filters.ts`). The toolbar is
one line (H9): search, agent, status, "+ filter" (the others —
experiments, tool, model, error, the range, the minimums, tags, session,
public id — folded behind it, its button counting the set ones; a set
filter is its chip, so the fold stays closed), the top-level toggle and
a "? keys" chip; the page never scrolls sideways at any width (the
table scrolls inside its frame, and a narrow window folds the quieter
columns: session, model, steps, tokens, cost, started, agent, in that
order). A text or
search-only filter, or a sort, reads `/api/search` (offset-paged);
otherwise `/api/runs`. Duration is always applied to the loaded rows
(the API has no duration filter); without `search` the box says why
(`capabilities_off.search`), model, error, the range and the minimums
filter the loaded rows and say so, and text and tool chips are drawn
struck through as not applied. The sessions list's text box (`?q=`)
is `/api/sessions?q=` (`obsdb.SessionQuery.Q`): every term in the
session's title, its first turn's prompt, its id, public id or agent.
Titles, not ids (U6): a run row, a session's turn and the run page are
titled by `prompt_preview` (else the agent, else "(content not
captured)" for a content-off run, else the id), a session by its
`title` — `thread`'s `weft.session.title`, metadata that survives
content-off — else its first turn's prompt (`first_prompt`), else
"(content not captured)" when that first turn stored no content (the
row's `content_off`), else the id; the id rides under the title as a
copyable mono line (not repeated when it is the title), and the
browser tab reads `director · How many units… · succeeded`. One helper,
`src/lib/title.ts` (`runTitle`, `sessionTitle`), words both Studio and
the panel's turn list and its narrow dropdown
(`● parked t3 How many units… · 2 steps · 811ms`; an experiment
`↳ replay of t3 · scripted · ● failed · 2 steps · 2.5s`); a thread
resume reads `resume of t3 · 1 decided call` (`turnTitle`) on the
session page and in the panel alike. ⌘K (or Ctrl+K) opens the palette
anywhere: runs through `/api/search?q=` (by id and prompt over the
recent runs without `search`), sessions (by title or first prompt too),
traces (the
newest page's ids, root names, services) under `traces`, experiments
by name under `playground` (never under a panel token, which the
experiments routes refuse), the pages — Playground under `playground`,
Settings (plan H4's page) — and the Studio commands; ↑↓ move, Enter
opens, Escape closes. A pasted id is looked up, never fuzzy-matched
(U26; `src/lib/finder.ts`): one word of four characters or more is an id
prefix (`GET /api/runs?id_prefix=…&all=1`, subagent children included),
`pub_…` a public id, `key=value` a tag, and their runs rank first under
**Exact match** — Enter opens the first; every other item matches by
contiguous substrings of its words (never a subsequence), so text that
matches nothing opens nothing. A run's metadata pairs are keywords: a
scenario tag's value finds the loaded runs it labels.

The run page's header carries the API's run verbs (U26;
`src/lib/run-actions.ts`): **export json · jsonl · otlp** beside save as
test (capability `export`; each a download through
`GET /api/runs/{id}/export?format=` with the bearer — the route serves
those four formats and no other, `xml` is a 400; a read-scoped token is
offered json and jsonl, otlp carrying the request records), and **delete
run…** (capability `delete`), which asks first in an in-page dialog
naming the subagent runs that go with it and saying the rows leave
Studio's database only, the app's conversation store untouched; it is
offered where the server answers it — the dev token, or a loopback Host
with no token (meta `live_all`, the same rule) — and elsewhere one faint
line says why. The Agents page titles every policy chip with what it
does and the option that set it (`src/lib/policy.ts`), `stop_when`
("stops when output_submitted") and `usage_limit` ("usage ≤ 25.0k in")
included.

Every view is a URL (plan G2): each piece of page state lives in a
search param, built and read through `src/lib/links.ts`. A cursor move
replaces the history entry; a view change pushes one — the back button
walks views, never cursor moves:

| page | key | state | history |
| --- | --- | --- | --- |
| runs list | `q`, `agent`, `status`, `session`, `public_id`, `tool`, `model`, `error`, `since`, `until`, `tag` (repeatable `k=v`), `min_duration`, `min_tokens`, `min_cost`, `experiments=on\|off`, `experiment_id`, `subagents=all`, `parent` | the filters (a change drops `before`) | push |
| runs list | `sort=duration\|tokens\|cost` | the order (search's; newest first without it) | push |
| runs list | `before` | the first page's cursor | — (a link's landing) |
| live | `peek` | the run whose story the side pane shows | push |
| sessions | `agent`, `public_id`, `q` | the filters | push |
| traces | `since`, `until`, `service` | the filters (a refused `since`/`until` kept, ignored and said) | push |
| run | `view=story\|raw` | the view (the waterfall is the default) | push |
| run | `raw=doc` | the raw view's surface | push |
| run | `step` | the step (its ordinal) | — (a link's landing) |
| run | `pane=request\|wire` | the highlighted step's pane (its tab); dropped when the step or the view changes | — (a link's landing; not written back) |
| run | `sel`, `d`, `scale` | the selected row (a run tree key), detail mode, the scale (`time\|order`) | replace |
| run | `t` | the replay playhead (seeks and pauses) | replace |
| run | `q`, `hide`, `ev` | the raw view's search, hidden kinds (`hide=step,delta`), open event's position | replace |
| run | `replay`, `from`, `of` | the replay drawer: its verb, its from_step, the child run it replays (a fresh load reopens it on that step, from the verb's draft — edits are not in the link) | replace |
| run | `save=test` | download the run's wefttest zip on landing (the panel's save as test), then dropped | — (a link's landing; replace) |
| experiments | `/experiments` | the saved experiments: agent, size, run count, cost — the newest 50, "newest N of total" past them with a load-more up to 200 (the API's cut); never under a panel token (the hidden badge, nothing fetched) | — |
| experiment | `/experiments/{id}` | one experiment's variants × inputs grid: re-run a cell or all, clone into the form, compare two cells | — |
| playground | `?exp=<draft id>` | a draft of the form in this browser's IndexedDB (never leaves the browser) | the drafts list |
| playground | `#state=<compact>` | a shared form — deflated or base64url JSON, ≤ 8,000 characters, no token — restored on arrival, never run | "copy share link" |
| trace | `view=chat` | the GenAI chat view (the tree is the default) | push |
| trace | `sel` | the selected row: its step-qualified key (H7; a pre-H7 `span=<span id>` is still read) | replace |
| playground | `run`, `step`, `agent`, `runtime`, `engine` | the source run, its step, the target, the first variant's engine — written back as they change | replace |
| playground | `experiment` | a saved experiment, highlighted in the history | push |
| compare | `a`, `b`, `step` | the base, the runs compared, the step | push |

Two names that read like URL keys are not: `offset` is `/api/search`'s
paging parameter (the runs list pages by `before`; search pages are
asked by offset, never written to the URL), and the panel's `tab` is
stored in `localStorage["weft.devtools"]` with the rest of its layout
(D4), never in a URL.

The panel hands the playground its prompt and other fields in the
fragment (`playground#run=…&instructions=…`), read once and stripped.
The page never writes a prompt into the query (an older link's
`?instructions=` / `?input=` are still read). The playground writes
its state from its controls; a change from outside (Back, Forward, a
link followed while it is open) is adopted into the controls and never
written back. **Copy link** — the button in the run header, `y`
anywhere, or "Copy link to this page" in `⌘K` — copies the page's
canonical link: the bare URL with its state, never a fragment, a token,
a panel scope or prompt text (`lib/links.ts`'s `canonical` drops
`token`, `access_token`, `sig`, `weft_scope`, `input`, `instructions`);
on a run page it carries the `pane` the URL landed with, not a later
switch of the step card's tab (the tab is remembered on the card, never
written to the URL);
the header says "link copied". Where the browser refuses the clipboard
(plain http off loopback, a denied permission) it falls back to
`execCommand("copy")`, then shows the link in a prompt, and says "copy
failed". Each page
titles its tab through `lib/title.ts`: `run s_…-t3 · succeeded · weft
studio`, `trace 0af76519…319c · weft studio`, `playground · weft
studio`.

**Open in app** in the run header (plan G3): a run whose request came
through `scope.Header` carries `meta["weft.page_url"]` (the sanitised
Referer/Origin); the verb opens that page in a new tab with
`#weft_scope=<encodeURIComponent(serializeScope({publicId, session,
run}))>` — `lib/links.ts`'s `appLink`, the serialised scope encoded once —
so the devtools panel there scopes itself (its URL rung). The panel's URL
rung needs a public id: a run without one opens the bare page. Without a
page URL the verb stays, disabled, titled "the run carries no
weft.page_url: wrap the app's handler in scope.Header". A read-scoped
panel token sees it too.

Saves (plan F6): Studio writes nothing to your files — the download is
the hand-off. **Save as test** in the run header (capability `export`;
hidden to a read-scoped panel token, which the format refuses) fetches
`GET /api/runs/{id}/export?format=wefttest` with the bearer in the
`Authorization` header — never a token in a URL — and saves the zip
under its `Content-Disposition` name (`<run>.zip`: a generated
`<agent>_replay_test.go` beside `testdata/TestReplay_<run>/`); the toast
says "saved <file> — unzip into a package directory and go test", or a
refusal in the server's words. **Keep as prompt** beside a step's system
prompt in the Request pane copies and, under `proposals` and an
identity that may propose (the dev token, or setup A's loopback with no
token), proposes the run's agent's instructions: when the manifest
verifies the agent (`weft.manifest.hash`) and its offered tools'
PromptSnippets end the recorded prompt, they are stripped (adopting a
composed prompt as Instructions would compose them twice); otherwise the
text goes as recorded and the toast says what it may carry ("may include
N tool snippets or a PrepareStep rewrite"). The proposal body is the
text alone, so the Agents page cannot tell: a proposal kept from a step
whose prompt was not verified may carry snippets. The toast says
the outcome, a 404 ("no agent … in the manifest file or any registered
manifest") or 409 ("the served manifest file does not name agent …") in
the server's words, and a panel token copies only ("a proposal needs the
dev token"). The playground card's keep as prompt copies the variant's
instructions and proposes them the same way. The **Agents page** shows each agent's proposal beside its
registered prompt: a `registered → proposed` line with the proposal's
`since` and hash (12 characters, the full one on hover), the line diff
from registered to proposed (the Request pane's prompt-diff renderer),
**copy proposed** (toasting "copied"), and **withdraw** (`DELETE`, the same gating — a panel
token sees it disabled with the hint); once on the page, the pointer
"the durable path is WEFT-PROMPT.md P3: a propose command over the
runtime link". A read-scoped panel token never reads the manifest (403,
hidden), so it never sees a proposal.

Notifications (plan H5): one toaster, bottom-right, in the app's theme,
with no hotkey (it never takes focus or a key from the page), and every
toast's words in `src/lib/notify.ts` — raised once per instance (a
command, a run, a runtime, a stream's give-up, a press), never again on
a re-render or refetch. **experiment … finished · status** — a
playground variant's or the replay drawer's command settled
(`useCommandTracking`), with "open" to the run; a matrix raises one
when its last cell settles, with "open" to the experiment. **run …
parked at tool** — a run a page follows parked (`useRunEvents`: a
playground card's run, or a run that parks while its page is open — a
run opened already parked says so on its page only; the toast goes
when this page decides it, or when its approve finds the park already
decided — it reads the run's row first, then follows the command its
post became and says a rejection), with "open" and,
for one parked call of a runtime-started run where the playground is on
and the token may act, "approve" (`POST /api/runs/{id}/approvals`).
**runtime … connected / disconnected** — the live stream has no runtime
kind, so the shell reads `GET /api/runtimes` (playground capability;
never under a read-scoped panel token) every 5 s while a surface needs
it now — the playground, the replay drawer, an experiment's run
controls, a parked or running app turn's block — and otherwise on a
quiet poll that doubles per read that changed nothing, 5 s up to 60 s
(a connected runtime's `last_seen`, moved by its stream's keep-alive,
is no change), back to 5 s on a change, a key or pointer press, or a
surface letting go of it; never while the tab is hidden. A quiet page
reads it 4 times in two minutes, a runtime connected or not. It raises one toast per transition of a runtime's
`connected` (a runtime gone from the list counts as disconnected, once;
a failed read says nothing; each flap of the same id is its own toast).
A runtime that goes down and comes back inside one poll interval is not
noticed; on a quiet page the toast comes up to a minute late.
**live updates stopped after 6 reconnects** — `lib/live.ts` gave up
(never on a clean close or a panel token's `expired`), with "retry",
which starts every stream that gave up over. **link copied** / **copy
failed** — copy link, beside the header's own words. A toast is a view:
it names ids, tools and counts, never a token or a prompt, and acts only
when its action is clicked.

Panes and density (plan H3): three splits resize — the trace page's
span tree beside the selected span, a run step's story beside its open
Request pane (one size for every card), and the playground's config
column beside its runs column. Drag the handle, or focus it (`Tab`) and
use the arrow keys, `Home`/`End`; a double click puts both panes back
at their defaults and forgets the saved size (the split's other
instances follow). A split never remounts what is in its panes — not
when a step's Request pane opens or closes beside its story, not when
the window crosses the phone width — so a half-typed draft (an edit in
the Story, the playground's free model name) survives. Each
pane keeps a minimum size, so nothing — a badge, a hole mark — is
squeezed out of sight. The sizes are a per-device preference, never
the URL: `localStorage["studio.panes.<split>"]` (`trace-detail`,
`story-request`, `playground`) holds the panes' percentages, and a
missing, unreadable or garbled value opens the split at its default.
**Reset pane layout** in `⌘K` forgets them all and puts the open
splits back. **Density** — comfortable (the default) or compact — is
the toggle beside the theme toggle in the sidebar footer and
"Compact density" / "Comfortable density" in `⌘K`: `data-density` on
`<html>`, set before paint by the bootstrap and stored as
`localStorage["studio.density"]` (the toggles, like the theme toggle,
draw the default on the hydrating render and the stored choice right
after, so the prerendered shell always hydrates cleanly); compact scales the theme's own
tokens (Tailwind's `--spacing` step, `--text-xs`/`--text-sm`/`--text-base`,
the body's `--studio-text`) under `[data-density="compact"]`, so every
page follows. At phone width (≤ 640 px) the splits stack: the trace
detail under its tree, the playground's config column above its runs
column, the page scrolling as one with no horizontal scroll; its header
controls wrap, its result cards go one per row, and the cards and the
matrix table scroll inside their own box. A step's Request pane opens
beside its story from the `lg` width (1024 px) up, inline above it
below that. The playground's controls are the shared
`components/ui` Select (`select-field.tsx`) in theme-token colours: the
lint rule `controlRules` (eslint.config.js, pinned by
`src/lib/controls.lint.test.ts`) refuses a native `<select>`, a raw
Tailwind palette class (`text-red-500`, `bg-amber-100`, Tailwind 4.3's
mauve/mist/olive/taupe, …) and an arbitrary colour (`text-[#…]`,
`bg-[rgb(…)]`, `border-[oklch(…)]`) in the
playground, the experiment form and the split, density and select
components.

Keyboard: `⌘K` jumps to any recent run, `/` filters, `j`/`k` move,
`enter` opens, `w`/`s`/`r` switch waterfall/story/raw, `t` steps the
waterfall's scale (time/order/graph), `space` replays,
`[`/`]` jump by step or tool event, `,`/`.` move one event, `y` copies
the page's link, `?` lists everything. Keys fire only on a bare press
outside a text box.

### After a Studio restart

The runtime link's bookkeeping is written through to Studio's database
when the backend implements `obsdb.CommandStore` (both first-party
backends do: SQLite migration 0007, ClickHouse 0008); `studio.New`
passes its DB itself. A backend without it keeps everything in memory,
as before — a restart then forgets it. One Studio per command store: two
Studios over one ClickHouse database never see each other's acks and
beats, so a restarted one would load the other's queued and accepted rows
and decide them lost (nothing runs twice; the other's live commands read lost).

**What survives.** Every command (its wire body, state, run id, the
accepted and last-heartbeat times, the last ack), every run → runtime
route (cancel, steer and the debugger's verbs keep reaching the runtime
that started a run), each runtime's breakpoint set and each runtime's
latest registration. A restarted Studio lists the runtimes it stored as
`connected: false, restored: true` — with their breakpoints and agents —
until each registers again, for at least 10 min (FinishDeadline) after the
restart whatever their last-seen, then by the usual prune (a disconnected
runtime with nothing pending and no breakpoint set goes); it answers `GET /api/playground/commands/{id}`
for a command issued before the restart with its stored state, and the
experiments/runs joins as before.

**What is decided lost, never re-run.** At load, with *now* the restart:

- a `queued` command whose runtime has no stored row (or when the stored
  runtimes could not be read) is lost — "Studio restarted; the runtime's
  identity is not stored";
- a `queued` command older than its ack window (30 s since it was
  created) is lost — "Studio restarted; no ack since <created>"; one still
  inside the window stays queued with its ack timer re-armed for what is
  left, and is re-sent only to the process it was queued for when that
  process reconnects (that process's at-most-once set refuses a frame it
  already admitted). Another process registering under the id meanwhile
  (the app restarted) is given `<id>-<pid>` and never sees the row, which
  ends lost by its ack window.
- an `accepted` command is watched from its stored last word,
  max(last heartbeat, accepted ack): lost max(10 min, 3 beats) after it
  for a runtime that heartbeats (10 min otherwise), as if Studio had never
  restarted. Already past: lost at load — "Studio restarted; no heartbeat
  since <t>". An accepted command is never re-sent: the runtime, not
  Studio, decides to run on an accepted ack Studio took (an accepted ack
  Studio did not take runs nothing).
- `finished`, `rejected` and `lost` commands are history.

**When the store lags or fails.** The writes leave the request path: the
in-memory transition and the HTTP answer never wait for the store. One
writer goroutine applies them in order (the latest write per key; the
prune is a queue item, never beside a write), each call bounded at 2 s; a
failure or timeout trips a breaker that holds the queue for 30 s — it keeps
coalescing per key, so it is bounded by the live keys (the memory prune
forgets the writes of the rows it removes) and a cap of 20 000 keys: a
Studio whose store stays down for hours drops route writes past it first
(a route is rebuilt from its command at load), then the oldest finished
commands' writes — so under the cap a finished command may read lost or
unknown after a restart; a WARN carries the running total on the first
drop of an outage and each time it doubles — and then re-applies all of
it, the failed write included; a write that
fails again trips it again, so a dead store holds instead of spinning. One
WARN, then Debug. A store failure changes no command's fate in this
Studio. What it can do is leave the store older than the memory when
Studio stops, and an older row is always the safe one: an accepted
command stored with an older beat is decided lost sooner, never re-run;
one stored still queued reaches no process but the one it was queued for,
which refuses a frame it already admitted. `studio.Server.Close` ends the
pause and drains the queue once (bounded at 5 s) before the database
closes — a store that has recovered is written at once; a store still
failing when the Studio stops loses the writes still held (one WARN names
how many). Those are the two ways a row goes absent rather than older: a
Close over a still-failing store, and the queue's cap. A third, minor
edge: the memory prune also discards the pending write of a row it
removes, so that row's older stored state stands until `PruneCommands`
deletes it. The writer is
stopped by `studio.Server.Close` alone: a server built with
`studio.Handler` is never closed and keeps that one goroutine for the
process's life (harmless); a write it takes after its DB was closed fails
and logs the one WARN.

**Retention.** The command rows' only retention is
`obsdb.CommandStore.PruneCommands`, run from the server's own prune
(24 h for terminal commands, 10 000 kept at most; queued and accepted
never), at most once a minute; run deletion, `Retain` and `weft vacuum`
never touch these rows. A runtime's LastSeen from the stream's keep-alive
pings is written at most once a minute (registration, stream open and
stream end always write).

**Stable runtime ids.** A runtime's id is `rt_` + the first 16 hex digits
of sha256(service NUL cwd NUL sorted agent names joined by NUL) —
service is `OTEL_SERVICE_NAME` or the binary's name, cwd the process's
working directory — so an app restarted in the same place registers under
the same id and a link to `/runtimes/<id>` survives the restart. When an
id is held by another process's live stream (two replicas of one service
in one cwd, or two links in one process), Studio answers the registration
`<id>-<pid>` (then `<id>-<pid>-2`, …) in `runtime_id`, and the link adopts
it; a reconnect of the same process keeps its id. A disconnected id
whose process still holds unfinished work — a queued or accepted
command — stays that process's: another process registering under it is
given `<id>-<pid>`, so the old process coming back finds its id and those
commands. A finished command's run route does not hold the id: a
disconnected entry holding no unfinished work is reclaimed by whoever
registers under its id next, Studio's breakpoint set kept — an app
restarted minutes after a playground run (`weft dev` restarts on every
save) keeps its id and its `/runtimes/<id>` link. So an approval for a
run parked on a runtime that went away may reach the process that took
the id since, which refuses it ("no parked run …").

**The `cwd` field.** `/api/runtimes` rows carry `cwd`, the app's working
directory — a filesystem path, so under `db.path`'s rule: the server token,
or setup A's loopback without a Token; never a panel token (read or
playground scope). `/api/meta`'s `runtimes` still counts connected runtimes
only.

**Wire (for the TS/Python runtime clients).** The registration gains
`cwd` (string, optional) and `instance` (string: a random value per link,
kept across its reconnects — with host and pid it is what makes a
re-registration "the same process"). A client **must** send `instance`
to keep at-most-once across its own restarts: without it Studio tells
processes apart by host and pid alone, and a restarted container reusing
pid 1 would read as the process that held the old commands (Studio WARNs
once per server on a registration without one). A client must adopt
`RegisterResponse.runtime_id` when it differs from the id it sent, and
open `commands_url` (which names it).

## The API (S4.2/S4.3)

JSON under `{base}api/`: `meta` (versions, `db` {kind, path, size —
path and size to loopback and the server token only}, `pid`,
`live_all` (true when this caller may open `/api/live?all=1` — setup
A's loopback Host and the server token, `db.path`'s rule; false for a
panel token and an `AllowOrigins` host), `ingest_open`, `interrupted_after_ms`, capabilities, `capabilities_off`
{capability: why}, `manifest_sources`, `retention` — `studio.Retain`'s
duration as a Go duration string, `"720h0m0s"`, `null` when off, the
default — `pricing` {source, path?, dated, models}, the cost table;
see Cost above — and `ingest` {dropped: [{service, records,
last_heard}], accepted, refused, last_error?, last_at?} (the counters:
the OTLP batches this Studio answered, see the server log below), the ingest health: the largest `weft.records.dropped`
each exporting service's heartbeats and `run_start`/`run_finish`
records reported on the OTLP path, in memory, per `service.name` —
lost on a Studio restart, until each app's next carrier reports it
again (UX-E-38: not persisted, accepted);
`manifest_check`'s rule — the dev token's or setup A's loopback, omitted
for a panel token, whose public id is no reason to read every service's
name and losses), `settings/pricing` (the effective pricing table,
capability `pricing`), `runs` (agent,
status — one parser for `/api/runs` and `/api/search` (I3): running,
succeeded, failed, interrupted, and the read-time `parked` (an open
park: succeeded, pending calls, no run whose `resumed_from` names it)
and `cancelled` (failed with `error_type` cancelled), each mapped onto
stored columns on both backends so `total` and the cursor count the
filtered set; any other word is a 400 naming them, never an empty
list — session, public id, playground, parent, experiment_id,
`stopped_by` (what ended the run — exact, or a prefix when the value
ends in `:`: `has_tool_call:` is every tool-call stop; `/api/search`
takes it too) and `tag.<k>=<v>` filters, cursor-paged; every run row
carries `stopped_by` and a canceled run's `cancel_cause`, omitted when
none was recorded (docs/record-contract.md §6), and `settling: true`
while its root span is stored and its `run_finish` is not, heard from
within 30 s — its records may still arrive, so the UI follows it as it
follows a running run (omitted otherwise; §6); top-level only by default — `parent=<run id>`
lists one run's subagent children, `all=1` (or `parent=*`) every run,
children included; every priced run row (lists, search, the run
document) carries `children_count` and `children_failed` (H9: the runs
whose `parent_run_id` names it, one level deep, and those of them that
failed — read from the children read the cost already makes, one
`ChildRuns` read per level of a page, never per row; each omitted when
0; `children_unknown: true` when that read failed — the count is then
not known, the chip "⑂ ?", never an absent count read as "no
subagents"; a session document's turn rows count every parent-linked
run too, a pool child session's included, from one more `ChildRuns`
read) — the runs list's "⑂ n" chip, a disclosure that reads
`?parent=<id>` and draws the first 100 children under the row, one level deeper
(grandchildren read when opened), "+N more · open the list" linking
`/runs?parent=<id>` when the row counts more or the read has a next
page; in the "with subagents" view the
loaded rows are drawn as trees, a child under its parent; every run row carries `prompt_preview` — the turn's
own prompt: the input record's last user message with text (on a resume
that carries a new prompt, that prompt: the next record's user text
before its first assistant message), else the first user message with
text, cut to 200 runes — absent when the records carried
no content, the run's `stripped`/`not_recorded` holes saying why),
`search` (plan A7, under the `search` capability: `?q=` terms —
whitespace-separated, a `"quoted phrase"` one term, each a
case-insensitive substring of the run's prompt (the turn's own, as
above), answer (its last assistant text), error or a tool name it
called, every term required, each in any field; a term under three
runes is matched with LIKE on SQLite, case-folded for ASCII only
(`lower()` on both sides; a short non-ASCII term matches its own case
alone there — ClickHouse folds UTF-8: the one difference between the
backends), `%` and `_` literal; never the system prompt, a tool result or the
catalog — with `agent`, `tool` (exact name), `status`, `model` (exact),
`error` (substring), `since`/`until` (RFC 3339, started at-or-after /
before), `min_tokens` (input + output), `sort=started|duration|tokens`
(newest, longest, most first), `limit`, `offset` and `/api/runs`' own
`session`, `public_id`, `parent`, `all`, `playground`, `experiment_id`,
`tag.<k>=<v>`;
`min_cost` (USD at least, the run's whole bill — own + delegated; an
unpriced run never matches) and `sort=cost` (most first, unpriced runs
last, newest first) are applied in Studio, one rule for both backends:
it reads the match's first 2,000 runs in the order asked (newest first
for `sort=cost`), prices them, filters, orders and pages them, and says
`cost_sort_cap: 2000` when the match ran past it (`total` then counts the
capped set). Answers
`{total, runs, sort, next_offset, cost_sort_cap?}` — run rows only, no snippet — scoped
like `runs`: a panel token searches its own public id, a foreign
`public_id` is 403. SQLite searches an FTS5 trigram index the writer
keeps (migration 0004: `run_search`, `run_tools`, `runs_fts`, runs
stored before it indexed when it applies); ClickHouse derives the fields at
read — once per search, for the runs the other filters leave, joined to
the run rows — and tests every term with `positionCaseInsensitiveUTF8`,
no migration),
`runs/{id}` (the row and the subagent children, each child row with its own `holes` — never events; with
`delta_count` (the streamed deltas, counted and never stored),
`instructions_hash`, `catalog_hash`, `request_count` and, for a run
written before ADR 0028, `requests_badge: "not_recorded"`; and
`compactions` — ADR 0028 §8's run-scope views `{scope: "run", index,
step, from_seq, to_seq, hash, replaced, entries}` and thread's session
markers `{scope: "session", hash, replaced, entries, tokens_before?,
tokens_after?, reason}`, counts and hashes only, `[]` when none),
`runs/{id}/events?after=&limit=` (the paged durable stream),
`runs/{id}/transcript` (the messages bodies; with `?step=N` also
`step`, `messages` — what step N's model call carried, the record's
truth and the replay prefix for `from_step` N > 0 (ADR 0029; `from_step`
0 re-runs step 0's PrepareStep over the original input) — and
`compacted_at`, the run-scope view that request carried `{index, step,
from_seq, to_seq, hash, replaced, entries}` or `null`; `badge:
"derived"` when no request record placed them, `messages: null` with
`badge: "hidden"` for a read-scoped panel token over a view; 404 past
the run's last step, 409 with the hole — `gap`, `stripped` — when the
records do not rebuild it), `runs/{id}/spans` (each span with
`run_id`, the run's; `?tree=1` — capability `spans_tree`, U9.1, the
database an `obsdb.TreeReader`, else 501 and `capabilities_off` says
why — adds every descendant run's spans, the subagent tree by the
`weft.parent.run.id` chain and, for a run no row or span names a parent
of, core's exact child id shape (`<parent>/<digits>/<call>` or
`<parent>/resume/<call>`),
found from the run rows and read by run id — never the whole trace;
a derived run's tree is cut from its trace's first 50,000 spans —
in start order, each keyed by `run_id` + `span_id` with its run's
`parent_run_id`, at most 10,000 spans (`obsdb.TreeSpanCap`), and
`holes` (always a list): `truncated` (cause `response_cap`) past the
cap or past a derived run's trace read, `not_recorded` (cause
`no_spans`, `runs` naming the first 100, `count` all) for finished
children with a row and no span (a running child is not missing),
`hidden` for a panel token — which reads the
run's own spans and its public id's descendants, every other
descendant left out with its subtree before the cap, unnamed but
counted (the hole's `count`, its subtrees' runs included), never a
403 for the tree; `testdata/api/spans-tree.golden.json` pins the shape;
docs/record-contract.md §6 "A run tree"; for a
read-scoped panel token — here, in `traces/{trace_id}` and in the
export alike — without the tool names a run's overrides put on its
invoke_agent span: `weft.override.tools`, `park_on`,
`park_all_except`, and a named `tool_choice` cut to its mode),
`runs/{id}/requests?step=&from=&limit=&refs=1` (one row per model-call
attempt — each row carries `raw`: the attempt's wire exchange
(`request`/`response` with method, url, status, headers, media_type, body —
base64 when `encoding` is `base64` — bytes and truncated_bytes, plus
`redacted_headers`/`redacted_query`), recorded only under
`otel.RawBodies(true)` with `WEFT_ENV=dev`; `null` with `raw_badge:
"not_recorded"`, `raw_reason` and `raw_fix` when the run holds none; the
route is refused to read-scoped panel tokens, so a wire body is never shown
to one; the response side's `complete` is `false` when its body was not
read to the end — closed unread by a transport retry, a timeout, a stopped
reader — and `truncated_bytes` then counts the unread rest when
Content-Length was known; a raw record is up to ≈ 650 KiB and Studio's
ingest refuses bodies over 16 MiB, so under RawBodies the pipeline exports
to Studio in batches of 16 records — the prompt and catalog its hashes name inline unless `refs=1`,
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
zipped as `<agent>_replay_test.go` (a generated test, plan F6) beside
`testdata/TestReplay_<run>/` — unzip into a package directory and
`go test` replays the run, `wefttest.Replay(t, "testdata")` answering;
the skeleton's header repeats each step that replays from a compacted
view; results keyed by (step, call id); steered steps replayed through a
generated `wefttest.NewSteers` source; a step the recorded run rewrote
replays its recorded messages (replayed, not re-derived); a step whose request carried a compaction view is keyed on it
and noted `compacted_at`. The run block's children carry their `holes`
as `runs/{id}` serves them. A read-scoped panel token gets `json` and
`jsonl` with the request block `{badge: "hidden", …}` and each
compaction view's `messages` null under the same badge; `otlp` and
`wefttest` are 403 with that badge),
`diff?a=<run id>&b=<run id>` (two runs compared step by step, under the
`diff` capability — plan E3: `{a, b, steps, summary, holes}`, `a`/`b`
`{run_id, steps, status}`; one row per step ordinal from 0 to the
longer run's last — the ordinal is the alignment key — `{step, changed,
a, b, changes, unknown}`, each side the step as `runs/{id}/steps/{n}` assembles
it, reduced to `{status, system_hash, system, tool_calls: [{name,
args}], tool_results: [{call_id, name, content, is_error}], text,
usage, marks, holes}`, or `null` on the side whose run has no such
step (`changes: ["missing"]`); `changes` lists what differs, in the
order `system` (by hash when both sides have one), `tool_calls`
(`name(args)`, the args canonicalised: keys sorted), `tool_results`
(content and `is_error`, in call order — call ids are not compared),
`text` (the step's assistant text from the transcript, exact) and
`usage` (the numbers); status, timing, marks and holes are never
compared. A column a side could not record is never compared with the
other side's value: it goes to `unknown` — "not comparable", neither
changed nor the same — and the side's `holes` say why: `system` when a
side has neither a hash nor the text (a run before the request record,
a dropped request record; `hidden` and content-off keep the hash, so
they still compare); `tool_calls`, `tool_results` and `usage` when a
side's events are not all stored (a lost `step_start`, missing
positions inside the step); `tool_calls` by name alone and
`tool_results` unknown when a side was content-off; `text` when a
side's text is null for a hole. `marks` say what the side's step was — `compacted` (a
compaction view: the model saw other messages, which the diff does not
compare), `subagent` (a call started a child run), `max_tokens`,
`parked`, `running`, `interrupted`, `error` — and never flip `changed`
by themselves: a compaction or a subagent marks its side, never forced
to align. Every column a side lacks is a hole in its `holes`, from the
same table — `system: null` under `hidden` for a read-scoped panel
token (the hash stays: `instructions_hash` is read-scoped too, so the
column still compares), `not_recorded` for a run written before ADR
0028, `text: null` under `gap` when no messages record of a finished
step was stored, or when a messages record of the run does not parse;
`summary` is `{changed_steps, first_changed}`
(`first_changed: null` when nothing differs); the top-level `holes` is
`truncated` with the table's `response_cap` cause when a run has more
than 500 steps (the first 500 are compared; each side's `steps` is still
the run's count). Both runs must be inside a panel token's public id (403
whichever side is outside; an unknown run 404; a missing `a` or `b`
400); `a == b` is every row unchanged. The route is two-way: an N-way
compare calls it N−1 times against one base run),
`traces` (plan A7, under the `traces` capability: the trace list —
every trace the database holds, weft or not, one row each, newest
first — `{total, traces, next_before, next_before_id}`, each row
`{trace_id, root: {span_id, name} | null, service, start, end,
duration_ms, span_count, error, run_ids}`: the root is the earliest span
with no parent, `null` with `badge: "gap"` (and its reason and fix)
when only child spans arrived; `service` is the root's, else the
earliest span's; `error` is any span ended in error; `run_ids` the weft
runs the trace holds, a subagent child beside its parent — the join key
to `runs/{id}`, `[]` for non-weft spans only. `since`/`until` (RFC 3339,
on the trace's start: its earliest span), `service` (exact; traces
holding a span of it), `public_id`, `before`/`before_id`/`limit` paged
as `runs` (default 50, max 500). Scoped like `runs`: a panel token lists
the traces every span of which carries its public id — the rule
`traces/{trace_id}` checks, so the list offers no trace the detail
refuses and no other public id's run id — and a foreign `public_id` is
403 — a trace holding any span without its `weft.public_id` (an
instrumented HTTP root, a client span under a tool) is not listed to a
panel token: its detail would be refused. A row carries no span
attribute, so the read scope has nothing to redact; a trace whose
earlier span arrives between page reads can be listed again on a later
page (never skipped); clients dedupe by `trace_id`. `before_id` without
`before` is ignored), `traces/{trace_id}` (any trace), `sessions`
(`tag.<k>=<v>`: the sessions one of whose own turns carries the pair,
U12.2), `sessions/{id}` (turns in order), `sessions/{id}/timeline` and
`sessions/{id}/entries` (U12.2, capability `timeline`: the turns beside
the session's state log — thread records and compaction markers from
the `session_events` index, on lanes, with their holes; a read token
gets `content_hidden` instead of a mark's content; `summarizer` on a
turn and on the session: thread's summarizer calls priced at read time,
never in a turn's usage, U15.1; docs/record-contract.md §6 — the
session page's default view draws it: exchanges on a time axis, a
drawer per mark at `?mark=`, the table at `?view=table`),
`sessions/{id}/public_id` (the reverse of `public/`: `{session_id,
public_id}`, the public id `thread.PublicID` stamped on the session's
turns — MAX(`weft.public_id`) over its top-level turns: one value per
thread session; a session whose turns carry several answers the
greatest, unbadged; `public_id: ""` with `badge: "not_recorded"` when the session was
created without one, 404 for an unknown session; the dev token's and
setup A's alone — every panel token, read or playground, its own
session included, is 403 with `badge: "hidden"`; session ids carry no
`/`, so the sub-route shadows none), `public/{public_id}`, `manifest` (each agent entry carries `proposed: {instructions,
instructions_hash, since}` when a proposal is held — plan F6, the
`PUT`/`DELETE /api/agents/{name}/proposal` `{instructions}` routes under the
`proposals` capability: an agent's proposed instructions held in Studio's
memory, at most 64 agents with the oldest forgotten first, lost on restart,
never sent to the app or written to files; 200 `{agent, proposed, note}`,
404 for an agent no manifest names or, on DELETE, one with no proposal, 409
when a manifest file is served and does not name the agent; the
dev token's and setup A's loopback alone, every panel token 403; it carries the agents' system
prompts: 403 with `badge: "hidden"` to a read-scoped panel token),
`manifests/{hash}` (U27, the `manifests` capability, present when the
database is an `obsdb.ManifestStore` — both first-party backends: the
agent configuration a run's `manifest_hash` names, after it stopped
being the current one — `{manifest_hash, agent, source, first_seen,
current_manifest_hash, manifest}`, `source` `file` or `runtime` (where
Studio first saw it), `current_manifest_hash` the hash the current
manifest gives the same agent (`""` when it names none; equal to
`manifest_hash` when the run's configuration is current), `manifest`
the one-agent `core.Manifest` document; Studio stores each agent of the
weft.json it serves once, at `New` (a weft.json rewritten while Studio
runs is stored after the next restart — or by the next registration of
an app running that configuration: under `weft dev` an app with
`runtime.Install` re-registers its agents on every save-and-restart, so
the dev loop is covered) and every runtime
registration's agents, content-addressed (the hash is the document's sha256, the first write
kept) and kept forever — deletion and `Retain` never touch them; 404
for a hash never stored; `/api/manifest`'s rule: 403 with `badge:
"hidden"` to a read-scoped panel token; the run document carries
`manifest_available: true` when its hash resolves),
`DELETE /api/runs/{id}` and `DELETE /api/sessions/{id}` (plan A7, the
`delete` capability: a run with its subagent children, or every run of
a session with theirs, removed from Studio's database — run rows,
records, the search index, the spans, the non-weft spans below the
runs' own and the app log lines under them; 200 with `{deleted: "run" |
"session", run_id | session_id, note}`, 404 for an unknown id. Studio's
tables only: the session verb removes the session's *observability*
rows and says so — the app's conversation store is untouched, and a
SQLite file that also holds `thread`'s sessions keeps them
byte-identical. The dev token's, or a loopback `Host`'s in setup A —
never an AllowOrigins host's, never a panel token's: every panel token,
read or playground, is 403 before anything is looked up. A live stream
following a deleted run emits nothing more unless the app keeps writing;
a later write starts a new run row; a later read is 404),
`POST /api/panel-tokens`
(mint; the panel's scoped tokens — see the devtools panel above), and
`GET /api/live` — the SSE
stream whose frame ids are the hub's Seq: exactly one selector
(`run`/`session`/`public_id`/`agent`/`all`), `kinds` over
event/delta/messages/run/attempt (default `event,run`; deltas and
attempts are opt-in, heartbeats never), a ping every 15 s,
`Last-Event-ID` resume with the gap backfilled from the database and
deduped on `(run, kind, pos)`, and `event: overflow` when a slow
subscriber's queue drops it. Kind `attempt` (H9, O17) is a live-only
frame: `event: attempt` with the hub's Seq as its id and
`{run_id, session_id, public_id, step, attempt, layer?, status
ok|error, error_type?, provider?, model?, start, end, wait_ms?,
trace_id, span_id}` — one try inside a model call, read from its
`attempt` span (record contract §3) the moment the span lands (ingest
or the database's own write, deduped on run, step and attempt), so a
step that is retrying or falling back is visible before its `chat`
span ends. `wait_ms` is the provider's retry-after ask
(`weft.attempt.retry_after_ms`), absent when it sent none (`mw.Retry`'s
own backoff is not recorded); the selector matches the span's identity
(`weft.run.id`, the session and public id it carries — not `agent`:
an attempt span names no agent). Never stored as a frame and never
backfilled: a resume replays records only; the span itself is stored
and read by the step routes.
`all=1` (plan A7, the Live page) streams every run in the database as
its frames are published — subagent child runs included, each frame
naming its own `run_id` (a record that names no run is not forwarded);
a resume (`Last-Event-ID`, however old) backfills a bounded set — the
runs running or last seen within the last hour, newest first, at most
500 — never the whole database, never an error; a resume names a run it
could not read with `event: backfill_error {run_id, kind}` (the cause in
the log). A grant names one
stream: a run grant's `sig` never opens `all`, nor an `all` grant's a
run. It is setup A's on a loopback
Host and the server token's alone: every panel token, read or
playground, is 403 `forbidden` on the stream and on its grant (the
body form is `{"all":true}`), as is a Host let in only by
`AllowOrigins`; `/api/meta`'s `live_all` tells the UI whether to offer
it. A token
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
A grant is a 60 s window, not a single-use ticket: any number of
streams may open with that sig while it lasts (a browser's
`EventSource` reconnect reuses the same URL, so a single-use sig would
end the tail at the first reconnect), and each runs until it is
closed. A leaked grant URL is therefore live for 60 s — the bearer is
never in it, the grant answer is `Cache-Control: no-store`, and Studio
never logs the URL. A panel token's stream (opened with a grant or
with the bearer) ends at the token's expiry with one final `event:
expired` frame (`data: {}`) and closes; a server token's does not (nor
setup A's). After `expired`, reopen only with a
bearer that is still valid — a new panel token, then a new grant.
Both clients do exactly this (`lib/live.ts`'s `openLive` for the UI,
`openPanelLive` for the panel): a grant before every connection, the
browser's own `Last-Event-ID` reconnect kept inside the grant's 60 s —
measured on the page's clock from the answer's `Date` header, so a
skewed client clock neither spends every grant at once nor trusts a
spent one — a new grant (and a fresh connection, refetched) after it or
after a refused stream, on the backoff when it keeps failing. After
`expired` the UI asks again only with a fresh bearer (a pasted token);
the panel stops. A grant refused 403 (a panel token asking an agent's
stream: the live page, the runs list's follow) is asked once; the page
says "streaming needs the server token · polling" and polls. A fresh
connection carries no resume cursor — the header is the only way
`Last-Event-ID` is read — so the clients refetch pages instead, the
run page's tail on its first open too. The UI adopts a token from the
link's `#token=` fragment only; a `?token=` in the address is stripped,
never kept.
The `#token=` fragment `weft open` and `weft studio --open` hand the
browser never reaches the server; the UI reads it. OTLP
ingest is `POST /v1/traces` and `/v1/logs` (protobuf and JSON, gzip,
16 MiB after decompression, publish-then-write, 503 on a write failure
so the exporter retries). Every response carries `X-Request-Id` (the
client's when it sent one of at most 64 `[A-Za-z0-9._:-]`, else a
generated one), and a 5xx body carries it as `error.request_id`.
Studio's server log (`slog.Default`; `studio/internal/reqlog`, UX-E-37)
writes one WARN line per refused ingest batch — `studio: ingest batch
refused` with `request_id`, `status`, `path`, `remote`, `code`,
`reason` (the error body's message: a bad token, a size, a malformed
body), `bytes` — at most one a minute per remote, route, code and
status, the route its class (`/v1/traces`, `/v1/logs`, else `/v1/*`:
a scan of distinct paths shares one limit; the next line counts the
rest, `suppressed=n`). The limiter holds 1,024 keys: full, it drops
the expired ones, else logs no new key until a window turns and the
next line says how many it skipped (`suppressed_overflow=n`). Behind a
reverse proxy every client is the proxy's one remote, so they share one
limit. One ERROR line per 5xx, `studio: request failed` with the same
fields, is never limited; a 5xx whose body is masked for a panel token
still logs the real error as its `reason`; a handler panic is one
ERROR line, `studio: handler panicked` with the request id and the
panic. `X-Request-Id` is in `Access-Control-Expose-Headers`, so a
cross-origin panel can read it. `weft studio` / `weft dev`
`--log-level debug|info|warn|error` (`WEFT_LOG_LEVEL`) and
`--log-format text|json` (`WEFT_LOG_FORMAT`) pick that log's handler on
stderr. `/api/meta`'s `ingest` adds `accepted` / `refused` (the OTLP
POSTs answered 2xx / 4xx–5xx since start, in memory) and the last
refusal, `last_error` ("<status> <code>: <reason>") with `last_at`,
omitted before the first. Under `Playground(true)` the playground's
routes join (`GET /api/runtimes`, `POST /api/playground/runs`, `GET
/api/playground/commands/{id}`, `POST /api/runs/{id}/approvals`,
`POST /api/playground/fixtures`, `GET/POST /api/experiments` (the 50 most recently updated by default, `?limit=` up to 200, with `total`; id: `[A-Za-z0-9._:-]{1,128}`, as command_id; each row with `created`, `run_count`, `succeeded` and `last_run` (the top-level runs that succeeded and the newest one's start; omitted past the summary's 10,000-run walk),
`cost_usd`/`cost_known`; a POST creates or replaces the whole definition —
`engine`/`side_effects`/`thread`/`from_step`/`source_run_id`/`transcript_edits`
kept as sent, per variant its overrides + modes), `GET
/api/experiments/{id}` (the definition with its whole command and its runs), `PUT /api/runtimes/{id}/breakpoints`, `POST
/api/runs/{id}/steer` — see the playground above) beside the runtime
link's own (`POST /api/runtime/register`, `GET /api/runtime/commands`
SSE, `POST /api/runtime/acks` — server token only, never a panel
token); `/panel.js` serves the devtools panel bundle — static and
unauthenticated; `/panel-config.json` (`{endpoint, version,
capabilities}`, capability `panel-config`) answers a loopback (or
`AllowOrigins`) Host and a same-origin, `AllowOrigins` or loopback
Origin only, else 404.

`GET /api/panel-handoff?to=<path>` (capability `handoff`; registered only
with a `Token`, else `capabilities_off.handoff` says why): setup C's "open in
Studio". A playground-scoped panel token gets a 303 to `<base><to>#token=<t>`,
where `t` is a read-scoped panel token for the same public id, signed like
`/api/panel-tokens`'s, expiring in 5 minutes or at the asking token's own
expiry if sooner, logged as a hand-off without the token; `Accept:
application/json` gets the same as `{"location","exp"}` for a fetch. The
server token gets the 303 with no fragment. A read-scoped panel token is 403:
it cannot widen, and its page links to Studio directly. `to` must be a path
under the base — a single leading `/`, no scheme, host, fragment, backslash,
white space or `.`/`..` segment (raw or `%2e`) — else 400: never an open
redirect.

Capabilities are computed from the registered route groups
(`routes.go`) — never hard-coded: `requests`, `steps`, `export`,
`logs`, `diff`, `traces` and `pricing` (always present), `live` (the stream and its grant),
`ingest`, `auth` (with a
token), `search` (when the database implements `obsdb.Searcher` — both
first-party backends do; `capabilities_off.search` says so otherwise), and `runtimes`, `breakpoints`, `steer`, `cancel` + `playground` under
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
make studio-check   # rebuild, prove dist is fresh, check the 700 KiB gzip budget, typecheck + test
```

`make studio-check` is the freshness gate (ADR 0018 §4): it fails if
`dist/` does not match `web/` or if the gzipped total exceeds 700 KiB
(currently ~599 KiB, the panel bundle's ~85 included).
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
