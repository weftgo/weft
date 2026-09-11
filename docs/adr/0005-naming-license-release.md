# ADR 0005 — Module path, option vocabulary, license

- Status: decided (2026-09-09, applying REVIEW-v0.md R1 findings 1–6)

## Context

REVIEW-v0.md's R1 findings block the first push: the module path was
`github.com/wajih/weft` in `go.mod` while the design brief (`weft.html`)
leaned `github.com/weftgo/weft`; the README's quick start showed a
nonexistent adapter; README and TODO disagreed on satellite module names;
no LICENSE existed; and the option vocabulary existed in two dialects
(`Parallelism(n)`/`Sequential()` in code vs `Execute(Parallel(4))` in the
brief). An import path moved after consumers exist is the one break Go
cannot soften, so this must be settled before the first tag.

## Decision

- **Module path: `github.com/weftgo/weft`.** The org-level path matches
  the design brief and leaves room for satellite modules
  (`weftgo/weft/openai`, …) under one namespace. Renaming later would be
  a breaking change for every consumer; doing it now, at zero consumers,
  is free. Prerequisite: the `weft-go` GitHub org must exist before the
  first push (the TODO §1.1 checklist owns the remaining org/domain
  checks).
- **Option vocabulary: the code's names win.** `Parallelism(n)`,
  `Sequential()`, `StopWhen`, `wefttest.Script/Say/ToolCalls/Fail/MaxTokens`.
  Shorter, no nested constructors; `weft.html`'s
  `Execute(Parallel(4))` dialect is retired. Naming stability is API
  stability — freeze before v0.1.0, then no renames.
- **License: MIT** (the "weft authors", 2026). Matches the positioning
  ("we win the way echo/chi won" — both MIT), keeps adoption friction at
  zero, and the repo has no external contributions or patent-clause
  concerns yet. Revisit only if a significant contributor requires
  Apache-2.0.
- **Layout: monorepo, one module per adapter** (TODO §3.1). A committed
  `go.work` at the repo root uses the root module plus `openai/`,
  `anthropic/`, `google/` — the workspace *is* the monorepo layout, and
  CI must build the same graph. Each adapter is its own module that
  imports only the root module and its vendor SDK, and tags
  independently (`openai/v0.1.0`). During development the workspace
  resolves the root module from disk; a tagged adapter requires a tagged
  root. `make test`/`vet`/`lint` loop over `go list -m` so every module
  is built and tested on every change.
- **Go support policy: current release and N-1.** `go.mod` declares
  `go 1.26` (the minimum), CI tests a `1.26.x`/`stable` matrix.
- **README is a compile-checked document**: quick starts use only APIs
  that exist (the scripted model); future adapters appear as comments,
  never as call sites.

## Consequences

- Import paths everywhere reference `github.com/weftgo/weft` (done via
  the TODO §1.1 sed step; tests, examples, and the README agree).
- Satellite names follow TODO's header: `runtime`, `store`, `serve`,
  `studio`, with `ops` dissolved into `eval`, `prompt`, `mem`, `trace`.
  The README roadmap was updated to match.
- Tag v0.1.0 remains gated on TODO §2 (first adapter) and §3 (middleware
  seams); the apidiff gate turns on immediately after that tag.

## Amendment (2026-09-11 — public release as v0.1.0)

The GitHub org was created as `weftgo` (not `weft-go`), and the repo
lives at `github.com/weftgo/weft` — matching the module path exactly.
v0.1.0 is tagged with the core plus the three adapters; the middleware
seams follow in a later v0.x. Internal working notes (build plan, phase
plans, reviews) moved out of the published tree at the same time.
