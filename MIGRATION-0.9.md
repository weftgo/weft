# Migrating to weft 0.9.0

weft 0.9.0 changes what the import path `github.com/weftgo/weft` means.
It used to be the agent loop alone, with every other layer a separate
Go module tagged on its own. It is now the whole framework — one
module, one version — and the loop alone lives at
`github.com/weftgo/weft/core`.

This file is written to be followed mechanically, by a person or by a
coding agent. Every rule is a find-and-replace or a command; the
checklist at the end says when you are done.

## The two shapes

| You want | Import | go.mod |
|---|---|---|
| The whole framework, or any layer of it | `github.com/weftgo/weft`, `…/weft/thread`, `…/weft/otel`, `…/weft/studio`, … | `require github.com/weftgo/weft v0.9.0` — the one line |
| The loop alone, no other dependency than the OpenTelemetry API | `github.com/weftgo/weft/core` (and `…/core/wefttest`, `…/core/mw`) | `require github.com/weftgo/weft/core v0.9.0` |

Every name in `weft` is an alias of, or a one-line wrapper around, the
same name in `core`: `weft.Agent` **is** `core.Agent`, `weft.New` calls
`core.New`. Values flow between the two without conversion, so a
library written against `core` composes with an application written
against `weft`.

## Rules for code that imported `github.com/weftgo/weft`

Your Go source needs **no change**. `weft.New`, `weft.Tool`,
`weft.Prompt` and the rest exist with the same signatures. The same is
true of `github.com/weftgo/weft/wefttest` and `github.com/weftgo/weft/mw`.

Only go.mod changes: see "Rules for go.mod".

If you would rather depend on the loop alone, apply these rewrites
instead (they are optional):

| Find | Replace with |
|---|---|
| `"github.com/weftgo/weft"` (the import) | `"github.com/weftgo/weft/core"` |
| `"github.com/weftgo/weft/wefttest"` | `"github.com/weftgo/weft/core/wefttest"` |
| `"github.com/weftgo/weft/wefttest/conformance"` | `"github.com/weftgo/weft/core/wefttest/conformance"` |
| `"github.com/weftgo/weft/mw"` | `"github.com/weftgo/weft/core/mw"` |
| `weft.` followed by an exported name (`weft.New`, `weft.Tool`, `weft.Prompt`, `*weft.Agent`, …) | `core.` + the same name |

Regular expression for the last rule, applied to `.go` files only:
`\bweft\.([A-Z])` → `core.$1`. It does not touch `wefttest.` (no word
boundary before the dot) or strings such as `weft.db`.

## Rules for code that imported a layer

Nothing in the import paths of the layers changed:

```
github.com/weftgo/weft/openai     github.com/weftgo/weft/thread
github.com/weftgo/weft/anthropic  github.com/weftgo/weft/thread/sqlite
github.com/weftgo/weft/google     github.com/weftgo/weft/otel
github.com/weftgo/weft/mcp        github.com/weftgo/weft/obsdb
github.com/weftgo/weft/mw         github.com/weftgo/weft/obsdb/clickhouse
github.com/weftgo/weft/wefttest   github.com/weftgo/weft/studio
                                  github.com/weftgo/weft/runtime
```

Their APIs are the same as their last per-module tags (thread 0.9.1,
otel 0.2.1, obsdb 0.2.0, studio 0.4.1, runtime 0.2.0, thread/sqlite
0.3.1, the adapters at their last tags). Signatures that named
`weft.X` now name `core.X`; that is the same type, so no call site
changes.

## Rules for go.mod

Before 0.9.0 a project that used several layers required several
modules. Those module paths are now **packages inside the one
framework module**. A go.mod that still requires one of them beside
`github.com/weftgo/weft v0.9.0` fails to build with
`ambiguous import: found package github.com/weftgo/weft/thread in multiple modules`.

Do this, in order:

```sh
# 1. Drop every retired module requirement (each line is harmless if absent).
go mod edit \
  -droprequire=github.com/weftgo/weft/thread \
  -droprequire=github.com/weftgo/weft/thread/sqlite \
  -droprequire=github.com/weftgo/weft/otel \
  -droprequire=github.com/weftgo/weft/obsdb \
  -droprequire=github.com/weftgo/weft/obsdb/clickhouse \
  -droprequire=github.com/weftgo/weft/studio \
  -droprequire=github.com/weftgo/weft/studio/cmd \
  -droprequire=github.com/weftgo/weft/runtime \
  -droprequire=github.com/weftgo/weft/openai \
  -droprequire=github.com/weftgo/weft/anthropic \
  -droprequire=github.com/weftgo/weft/google \
  -droprequire=github.com/weftgo/weft/mcp

# 2. Take the framework at 0.9.0.
go get github.com/weftgo/weft@v0.9.0

# 3. Let Go settle the rest (core becomes an indirect requirement).
go mod tidy
```

A project that wants the loop alone runs
`go get github.com/weftgo/weft/core@v0.9.0` in step 2 and drops
`github.com/weftgo/weft` too, after applying the optional rewrites
above.

Remove any `replace` directive that pointed one of the retired module
paths at a local checkout; the path is a directory of the framework
module now.

The `studio` binary installs from the framework module:
`go install github.com/weftgo/weft/studio/cmd@v0.9.0`.

## What a tool manifest records

`weft.Manifest` names each tool's definition site (`"source":
"file.go:19"`). The site is still your file, not the facade's: the
loop skips the framework's wrapper frame when it records the caller.
A committed manifest golden file does not change.

## Checklist

- [ ] `grep -rn 'weftgo/weft/\(thread\|otel\|obsdb\|studio\|runtime\|openai\|anthropic\|google\|mcp\)' go.mod` prints nothing.
- [ ] `go build ./... && go vet ./...` pass.
- [ ] `go list -m all | grep weftgo` shows `github.com/weftgo/weft v0.9.0` and `github.com/weftgo/weft/core v0.9.0`, nothing else under `weftgo`.
- [ ] Tests that compare a manifest golden file still pass.

## For a coding agent

Paste the block below as the task; it is the whole migration.

```
Migrate this Go project to weft 0.9.0. The module github.com/weftgo/weft
now contains every weft package (thread, otel, obsdb, studio, runtime,
openai, anthropic, google, mcp, mw, wefttest); those paths are no
longer separate modules. The loop alone is github.com/weftgo/weft/core.
1. In go.mod, remove every require line whose path starts with
   github.com/weftgo/weft/ (keep github.com/weftgo/weft itself) and
   every replace of such a path.
2. Run: go get github.com/weftgo/weft@v0.9.0 && go mod tidy
3. Do not change Go source: every identifier in github.com/weftgo/weft,
   weft/wefttest and weft/mw still exists with the same signature, and
   every layer's import path is unchanged.
4. Run go build ./... and go vet ./... and report the result.
```
