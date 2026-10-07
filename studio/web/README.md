# weft studio — the web source

The Bun project behind [`studio/dist`](../dist): the Studio UI (TanStack
Start in SPA mode, React, shadcn/ui on Tailwind v4) and the devtools
panel (`src/panel/`, a separate library-mode build with no React in the
bundle). Users of the Go module never need this directory — the build
output is committed and embedded with `go:embed`.
[`../README.md`](../README.md) "Contributing to the UI" is the full
story; this is the command list.

```sh
bun install --frozen-lockfile
bun run dev -- --base /studio/   # Vite on :3000, proxying /studio/api to 127.0.0.1:7331
bun run build                    # the app, then vite.panel.config.ts (the panel), then scripts/clean-dist.ts
bun run typecheck                # tsc --noEmit
bun run test                     # vitest run
```

The dev server needs an API to talk to: `go run ./studio/examples/basic
-serve` (from the repo root) serves one on `127.0.0.1:7331`.

From the repo root, `make studio-build` runs the install and the build,
and `make studio-check` rebuilds and fails when the committed
`studio/dist` is stale.

Layout: `src/routes/` (the pages), `src/lib/api.ts` (the hand-written
mirror of the Go API types in `../api.go`), `src/lib/live.ts` (the SSE
client), `src/panel/` (the `<weft-devtools>` element), `scripts/`
(`clean-dist.ts` makes the build deterministic, `panel-asset.ts` stages
the panel release asset, `panel-gate.ts` drives the panel gates).

Adding a shadcn/ui component: `bunx shadcn@latest add button` — it
lands in `src/components/ui/`.
