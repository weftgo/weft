# weft studio — the Inspector

A read-only UI over a [run store](../store): the runs list, the run
page (steps, tool calls and results, subagents inline, truncation
badged), guaranteed replay over the event index, raw JSON, and agent
and tool cards from the manifest. One `http.Handler`, no build step
for users, fully offline.

## Use it

```go
mux.Handle("/studio/", http.StripPrefix("/studio",
    studio.Handler(myStore, studio.Manifest(manifestBytes)))
```

Then record runs with `store.Record` (see
[examples/basic](./examples/basic)) and open
`http://127.0.0.1:7331/studio/`. Try the example end to end:

```sh
go run ./studio/examples/basic        # records three demo runs, serves :7331
go run ./studio/examples/basic -serve # serve an existing database only
```

The runs list (light): status as a dot and a word, the error under a
failed run's id, filters that mirror the URL, the whole row opens the
run:

![runs list, light theme](screenshots/runs-light.png)

The run page (light) — the prompt and the answer first, then the
facts; the replay bar with its bucketed gutter; a step with its tool
call as a one-line row (name, args, `ok · 29 B`) opening to the args
window, the subagent block with the child's own steps, and the
result:

![run page, light theme](screenshots/run-light.png)

The trace (light) — the run, its steps, tool calls and subagents as
spans on one axis, the event position (events carry no timestamps,
so this is order, not duration; T2a's spans put time on the same
component). The replay playhead crosses every row, and everything
past it is veiled; click a bar to seek, a label to land on that step
or call:

![trace waterfall, light theme](screenshots/trace-light.png)

A failed run (dark): the error at the top, the `ORDER_NOT_FOUND`
tool error as data (mustard, never red), and the step that had no
`step_finish` saying so:

![run page, dark theme](screenshots/run-dark.png)

Raw (light) — the events explorer: every event by position with its
kind, type and a one-line summary, filtered by kind, searched by any
JSON text, opened to the full event, and sent to the replay playhead
with one click. The `document` surface is the run document as a
collapsible tree; both copy and download whole:

![raw events explorer, light theme](screenshots/raw-light.png)

Replay (dark), scrubbed to event 6 of 14 — the readout names the
event at the playhead, the steps show exactly the folded prefix, and
a call without its finish yet reads *running…*:

![replay, dark theme](screenshots/replay-dark.png)

Dark and light follow the system; the sidebar footer's toggle (or the
palette) cycles system → light → dark:

![runs list, dark theme](screenshots/runs-dark.png)

Every view is a URL: filters, the selected step, `view=raw` (and
`raw=doc`), and the replay position `t` all live in search params —
paste a link into an issue and it reproduces exactly. Keyboard: `⌘K`
jumps to any recent run, `/` filters, `j`/`k` move, `enter` opens,
`space` replays, `[`/`]` jump by step or tool event, `,`/`.` move one
event, `r` toggles raw, `?` lists everything, `g r`/`g a` navigate.
Keys fire only on a bare press outside a text box — ctrl/⌘ always go
to the browser.

## The API (plan §3 of docs/phase2b-studio-plan.md)

Read-only JSON under `{base}api/`: `meta` (versions, capabilities),
`runs` (list, filtered, paged by cursor), `runs/{id}` (the run
document — never events), `runs/{id}/events?after=&limit=` (the paged
event stream; a live tail is the same endpoint read from the last
position), `manifest`. Capabilities are the hosting seam (ADR 0018
§8): the open handler reports none; a hosted server declares them
with `studio.Capabilities(…)`.

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
(currently ~313 KiB). The build is deterministic — two builds from
one tree are byte-identical (`scripts/clean-dist.ts` pins the router's
prerender timestamp and keeps `<base href>` first in `<head>`).

The dev loop is two terminals: `go run ./studio/examples/basic
-serve` (API + embedded UI on :7331) and `cd studio/web && bun run
dev -- --base /studio/` (Vite on :3000 proxying `/studio/api`).

Go module: `github.com/weftgo/weft/studio`, requiring the tagged
`weft` and `weft/store` — standalone-importable, no `replace`.
