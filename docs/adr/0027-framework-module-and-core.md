# ADR 0027: one framework module, and `core` for the loop alone

Date: 2026-10-07. Status: accepted. Supersedes the module layout of
ADR 0005 (one module per adapter, tagged independently) and the
per-module release process built on it.

## Context

Through v0.8.0 the import path `github.com/weftgo/weft` was the agent
loop alone, and every layer — the adapters, `thread`, `otel`, `obsdb`,
`studio`, `runtime` — was its own Go module with its own tag. The
layout kept the loop's dependency list at the OpenTelemetry API, which
is a property worth keeping. It cost two things:

- **The name said the wrong thing.** A reader who `go get`s "weft" got
  the loop and had to discover that sessions, recording and Studio were
  six more modules at six more versions. The landing page's "Four
  layers. Use one, or all" was true but the import path did not say
  it: `weft` read as the framework and was the smallest piece.
- **Version skew was the release process.** Each release note ended in
  a compatibility sentence ("requires weft v0.8.0 and thread v0.9.1");
  a two-phase tag order (root first, then every sub-module tidied
  against it) existed only to keep those sentences true; examples and
  go.mod files carried a matrix of pins.

## Decision

1. **`github.com/weftgo/weft` is the framework.** One module holds every
   package: the loop's facade at the root, `openai`, `anthropic`,
   `google`, `mcp`, `mw`, `wefttest`, `thread` (+ `sqlite`, `jsonl`,
   `pool`, …), `otel`, `obsdb` (+ `clickhouse`), `studio` (+ `cmd`),
   `runtime`. One `go get`, one version, one tag (`vX.Y.Z`), one
   CHANGELOG section per release. Import paths of the layers do not
   change.
2. **`github.com/weftgo/weft/core` is the loop alone.** A separate
   module (`core/go.mod`) whose only dependency is the OpenTelemetry
   API, holding what the root package held before: `core.New`,
   `core.Tool`, the message, error and tool contracts, `core/wefttest`,
   `core/mw`. It is tagged `core/vX.Y.Z` at the framework's version.
3. **The root package is a generated facade over `core`.** Every
   exported name of `core` exists in `weft` as a type alias, a
   re-declared constant or variable bound to the original, or a
   one-line wrapper function (so signatures, generics and doc comments
   survive on pkg.go.dev). `mw`, `wefttest` and `wefttest/conformance`
   are the same kind of facade over their `core` counterparts.
   `internal/facadegen` writes them from source; `go generate ./...`
   regenerates; `TestFacadesAreComplete` fails the build when a facade
   lacks a name its source exports or differs from what the generator
   writes.
4. **Layers depend on `core`, not on the facade.** Their signatures
   name `core.Model`, `core.Agent`, …; an application using `weft.Model`
   passes the same type. Examples and the godoc examples of the layers
   use the facade, because they are what a user copies.
5. **Definition sites skip the facade.** `core.Tool`, `Output` and
   `Subagent` record the caller's file and line for the manifest; the
   recorder walks past frames in the facade package, so a manifest
   names the user's file, as before.

## Consequences

- A consumer of the framework's go.mod carries the dependency graph of
  every layer (ClickHouse driver, SQLite, the vendor SDKs) in its
  module graph. Only imported packages are compiled and linked; the
  cost is go.sum lines and download, the standard trade of a
  batteries-included Go module. A consumer who objects imports `core`.
- The per-module tags that exist (`thread/v0.9.1`, `otel/v0.2.1`, …)
  stay valid forever and keep requiring `weft v0.8.0`. No new ones are
  cut. A go.mod that requires both a retired module path and the
  framework at 0.9.0 fails with an ambiguous-import error; the
  migration guide (`MIGRATION-0.9.md`) is the fix and is written to be
  applied by a coding agent.
- Releases are two tags in order: `core/vX.Y.Z` first, then the root
  tidied against it and tagged `vX.Y.Z`. The root's go.mod requires
  `core` at that exact version and carries no `replace`; `go.work`
  joins the two for development.
- The apidiff gate has two modules to gate: `core` (enforce, against
  its last `core/v*` tag) and the root (report; pre-freeze layers live
  in it, and the facade's aliases read as changes against v0.8.0).
- `studio/web` keeps its empty go.mod: it exists to keep Bun's
  node_modules out of the Go package walk, not to version anything.
- The thread API, storage format and the observability modules are
  not frozen by this decision; ADR 0024's programme and the thread
  review's freeze criteria stand.
