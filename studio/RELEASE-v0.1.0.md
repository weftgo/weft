# studio/v0.1.0 — release notes

Tagged 2026-09-28. One tag: the root and store need no change, so
`studio/v0.1.0` stands against a `go.mod` that requires the tagged
`weft v0.3.6` and `store v0.1.1` with no `replace` (TODO §1.1).

---

weft studio v0.1.0 — the Inspector (T1).

Studio is the read-only UI over a run store: mount one
`http.Handler` beside your agent, record with `store.Record`, open
`/studio/`. No build step, no Node at runtime, no account, nothing
leaves the process — every resource the app loads is same-origin.

```go
mux.Handle("/studio/", http.StripPrefix("/studio",
    studio.Handler(store, studio.Manifest(weftManifestBytes)))
```

What's in:

- **Runs list** — id, agent, model, steps, tokens (cached/reasoning
  splits on hover), status, duration. Crash-orphaned runs read
  *interrupted* and are shown, never hidden. Filters and the paging
  cursor live in the URL.
- **Run page** — the prompt, the answer (or the error) and the facts
  first; then the step story: model text, reasoning collapsed by
  default, tool calls as one-line rows (name, args, `ok · 29 B` or
  the `ToolError` code) opening to coloured, folded args and result
  windows, per-step and total usage, pending approvals. Tool errors
  are data — coded, mustard, never red. Truncation is badged with
  what was cut. A step the run died in says so. Subagent runs render
  inline under their call with their own steps and usage, and every
  child is a link to a full run page. Every step and call can send
  replay to the moment it happened.
- **Trace** — the default view: a flow strip of the loop, then the
  run as a waterfall (steps, tool calls and subagent runs as spans on
  the event axis, one tree, one playhead) beside a detail panel that
  reads the selected span as detail, events or json — and renders it
  at the playhead, so replay plays inside the panel too. It opens on
  whatever went wrong. The step story is one key away (`s`).
- **Guaranteed replay** — play the event stream at 1×/4×, scrub the
  bucketed gutter, read the event at the playhead, jump by step or
  tool event with `[` and `]`, move one event with `,` and `.`.
  Correct by construction: replay is indexed by the stream's total
  order, not timestamps.
- **Raw** — an events explorer (position, kind, type, one-line
  summary; filter by kind, search any JSON text, open a row, replay
  to it) and the run document as a collapsible tree; copy or download
  either. One `r` away.
- **Agent & tool cards** — the manifest (weft.json) drawn: schemas,
  per-tool policy chips, source file:line.
- **Keyboard-first** — `⌘K` jumps to any recent run, `/` filters,
  `j`/`k` move, `enter` opens, `space` replays, `r` raw, `?` help,
  `g r`/`g a` navigate. Keys fire only on a bare press outside a text
  box; ctrl/⌘ always go to the browser.

Built for hosting from day one without hosting code (ADR 0018 §8):
`api/meta` reports capabilities — the open handler reports none — and
a run's events are paged, never inline, so response size never grows
with run length and a live tail is the same endpoint. A hosted
Studio is this bundle plus a server that declares what it carries.

Engineering: the UI is a TanStack Start SPA prebuilt into a
committed, embedded `dist` (~319 KiB gzipped of a 600 KiB budget);
`make studio-check` fails CI on a stale dist, an over-budget build,
or a non-deterministic one. The JSON API is golden-pinned on the Go
side and type-mirrored in TypeScript against the same fixtures. The
theme is the landing page's paper loom (Geist self-hosted) with a
contrast-checked event palette; dark and light follow the system.

Fixed in the release review: a past-the-end events cursor no longer
panics the handler; the runs list and the ⌘K palette no longer share
a cache entry (the list crashed after the palette opened); a run that
finishes mid-burst drains every remaining page; pause writes `t`;
subagent call spans key apart from the parent's; missing assets 404;
`Title`/`Base` are escaped. CI now runs `make studio-check`.

Known limits (by design, T1): no live view (T2a), no chat view, no
lanes or durations — events carry no timestamps; the store's order is
the truth. No auth yet — bind loopback; `WEFT_STUDIO_TOKEN` arrives
when exposure does.
