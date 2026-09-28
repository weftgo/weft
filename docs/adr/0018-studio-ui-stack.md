# ADR 0018 — Studio UI stack: a TanStack Start SPA, prebuilt and embedded

- Status: decided (2026-09-28, TODO §12)
- Supersedes: TODO §12's "embedded via `go:embed`, no build step,
  vanilla JS" line (never shipped; no code depended on it)
- Implementation plan: `docs/phase2b-studio-plan.md`
- Inputs: `WEFT-STUDIO-FEATURES.md` (the catalog: about 90 features across
  T1–T4, of which T1 is 13), the weft landing page `site/index.html`
  (design system v6, "paper loom") as the brand source of truth

## Context

§12 planned the Inspector as hand-written vanilla JS with no build step.
That fits T1 (a runs list and a run page), but T1 is only the first of five
tiers. The features catalog commits Studio to a live view, run diffs, a fork
editor, a playground, eval matrices, an approvals inbox and an analyst
agent (about 90 features). Writing that in vanilla JS means building our own
component system, router, data cache and accessibility layer. The arena's
single-file `index.html` already shows the cost of doing that.

Nothing about the *users'* side changes: Studio has to install with
`go install`/`go get`, run offline, need no Node or Bun at runtime, and
mount as one `http.Handler`.

## Decision

1. **Stack.** React + TypeScript on **TanStack Start in SPA mode**
   (TanStack Router file routes, TanStack Query for data), styled with
   **shadcn/ui** on Tailwind v4, built with **Bun**. The project is created
   with:

   ```sh
   bunx --bun shadcn@latest init --preset b311momZs0 --template start
   ```

   and themed to the landing page's tokens (plan §5).
2. **Go is the only server.** No SSR and no TanStack server functions or
   server routes. The build output is static files. Every piece of data comes
   from the Go handler's JSON API (`{base}api/…`, plan §3). A
   TanStack Start feature that needs a JS server at runtime is out of bounds.
3. **Prebuilt assets are committed and embedded.** The web project lives at
   `studio/web/` and builds into `studio/dist/`, which is committed and
   embedded with `//go:embed all:dist`. Users of the Go module never run Bun.
   Contributors run it only when they change the UI.
4. **A freshness gate enforces (3).** `make studio-check` rebuilds and runs
   `git diff --exit-code studio/dist`. CI fails if `dist` does not match
   `web/`. The build must be deterministic: no timestamps or absolute paths in
   the output, and a pinned `bun.lock`.
5. **Offline by construction.** No CDN, Google Fonts or runtime fetch outside
   `{base}api/`. Geist and Geist Mono ship inside `dist` (copied from
   `site/assets/fonts`, OFL 1.1). The kill-switch test of the moat map
   applies: Studio renders fully with the network off.
6. **Mount anywhere.** The router's basepath and the asset base come from a
   `<base href>` the Go handler writes into the shell per request. The built
   assets use relative URLs, and the handler serves the shell for every
   non-asset, non-API path under the mount (SPA fallback).
7. **Budgets.** T1's `dist` stays under **600 KiB gzipped in total** (JS +
   CSS + fonts). A breach is a CI failure until someone raises it here with
   a reason.
8. **Built for hosting from day one, without hosting code.** Two API
   rules make the same bundle serve local, self-hosted and hosted
   Studio (`WEFTGO-PRICING-AND-TIERS.md` §10: one artifact):
   - **Capabilities, not builds.** `api/meta` reports `capabilities`. The
     UI gates every deployment-specific screen (login, org switcher,
     approvals inbox, live, ingest, fleet) on a named capability. The
     open handler reports none. A private hosted server wraps `Handler`,
     adds auth and routes, and declares them with `Capabilities(…)`.
     There is no hosted-only UI build.
   - **Events are paged.** A run's events come from
     `api/runs/{id}/events?after=&limit=`, never inline in the run
     document, so neither response size nor client memory grows with run
     length, and the live tail (T2a) is the same endpoint polled from the
     last position. Scale work after that is server-side and behind the
     `store.Store` seam: a ranged event read, a Postgres or column-store
     backend, delta coalescing on ingest, and pub/sub between replicas for
     live views. None of it touches the UI.
9. **Dependency hygiene.** shadcn components are copied into the repo (that
   is shadcn's model), not taken as a dependency. Runtime npm dependencies
   are the ones shadcn pulls in plus TanStack. Anything else needs a line in
   the plan's dependency ledger.

## Consequences

- The "no build step" stance becomes "**no build step for users**."
  Contributors to `studio/web` need Bun. The committed `dist` plus the
  freshness gate keep the Go module self-contained.
- The Go module zip carries the web sources and `dist`. `node_modules` is
  gitignored and never enters the module.
- The API contract (plan §3) is now a real boundary. Its JSON types are
  written by hand in Go and mirrored in `studio/web/src/lib/api.ts`. A
  golden test pins the Go side, and the TS side type-checks against
  recorded fixtures of the same responses.
- The arena's vanilla UI is prior art to port (trace view, playground,
  engine pill), not code to reuse.
- `brand/README.md` still describes v5 (IBM Plex, near-black). Studio follows
  the landing page (v6: Geist, paper/ink, one vermilion thread). The brand
  README should be updated to v6 separately.

## Rejected

- **Vanilla JS (the old §12 line).** Fine for T1, wrong for T2–T4. Rewriting
  later costs more than starting on the stack the catalog needs.
- **htmx / Go templates (templ).** No build step, but the live view,
  replay scrubber, fork editor and playground are client-state heavy.
  Server-rendered fragments fight them.
- **Next.js / TanStack Start with SSR.** Needs a Node/Bun server at runtime,
  which breaks "one `http.Handler`, `go install` and go."
- **Building at `go generate` time on the user's machine.** Makes Bun a user
  dependency.
