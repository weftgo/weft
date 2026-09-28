# studio/v0.1.0 — release notes (DRAFT, not tagged)

Release is two-phase (TODO §1.1): the root and store need no change,
so after this branch merges, tag `studio/v0.1.0` against a `go.mod`
that requires the tagged `weft v0.3.6` and `store v0.1.1` with no
`replace`. The tag message is this file.

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
- **Run page** — the step story: model text, reasoning collapsed by
  default, tool calls with their args and results, per-step and total
  usage, pending approvals. Tool errors are data — coded, mustard,
  never red. Truncation is badged with what was cut. Raw JSON is one
  `r` away. Subagent runs render inline under their call with the
  child's usage rollup, and every child is a link to a full run page.
- **Guaranteed replay** — play the event stream at 1×/4×, scrub the
  seq gutter, step with `[` and `]`. Correct by construction: replay
  is indexed by the stream's total order, not timestamps.
- **Agent & tool cards** — the manifest (weft.json) drawn: schemas,
  per-tool policy chips, source file:line.
- **Keyboard-first** — `/` filters, `j`/`k` move, `enter` opens,
  `space` replays, `r` raw, `?` help, `g r`/`g a` navigate.

Built for hosting from day one without hosting code (ADR 0018 §8):
`api/meta` reports capabilities — the open handler reports none — and
a run's events are paged, never inline, so response size never grows
with run length and a live tail is the same endpoint. A hosted
Studio is this bundle plus a server that declares what it carries.

Engineering: the UI is a TanStack Start SPA prebuilt into a
committed, embedded `dist` (~305 KiB gzipped of a 600 KiB budget);
`make studio-check` fails CI on a stale dist, an over-budget build,
or a non-deterministic one. The JSON API is golden-pinned on the Go
side and type-mirrored in TypeScript against the same fixtures. The
theme is the landing page's paper loom (Geist self-hosted) with a
contrast-checked event palette; dark and light follow the system.

Known limits (by design, T1): no live view (T2a), no chat view, no
lanes or durations — events carry no timestamps; the store's order is
the truth. No auth yet — bind loopback; `WEFT_STUDIO_TOKEN` arrives
when exposure does.
