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

The runs list (light):

![runs list, light theme](screenshots/runs-light.png)

The run page (dark) — a step with its tool call, the args code
window, the subagent block with the child's usage rollup, and the
replay bar whose gutter shows the event stream in order:

![run page, dark theme](screenshots/run-dark.png)

Dark and light follow the system; the sidebar footer's toggle cycles
system → light → dark:

![runs list, dark theme](screenshots/runs-dark.png)

![run page, light theme](screenshots/run-light.png)

Every view is a URL: filters, the selected step, `view=raw`, and the
replay position `t` all live in search params — paste a link into an
issue and it reproduces exactly. Keyboard: `/` filters, `j`/`k` move,
`enter` opens, `space` replays, `[`/`]` step, `r` toggles raw JSON,
`?` helps, `g r`/`g a` navigate.

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
(currently ~305 KiB). The build is deterministic — two builds from
one tree are byte-identical (`scripts/clean-dist.ts` pins the router's
prerender timestamp and keeps `<base href>` first in `<head>`).

The dev loop is two terminals: `go run ./studio/examples/basic
-serve` (API + embedded UI on :7331) and `cd studio/web && bun run
dev -- --base /studio/` (Vite on :3000 proxying `/studio/api`).

Go module: `github.com/weftgo/weft/studio`, requiring the tagged
`weft` and `weft/store` — standalone-importable, no `replace`.
