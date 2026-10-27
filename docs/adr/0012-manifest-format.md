# ADR 0012 — The manifest format (`weft.json`)

- Status: decided (TODO §2.9, 2026-09-10)
- Sources: `docs/tool-schema-design.md` §9 ("Swagger for agents"),
  Crush's schema-as-generated-artifact with a CI gate.

## Context

Studio, docs, review, and compatibility checks need a description of
every agent and tool. Reading a live process couples them to a running
binary; hand-writing a file creates a second source of truth that
drifts. The manifest is the third way: **generated from the code,
committed, diffed, gated** — the code stays the only source of truth,
and the file is output, never input (nothing is ever configured from
it; that is a red flag in the playbook).

## Decision

`weft.Manifest(agents ...*Agent) ([]byte, error)` renders version 1 of
the format: `weft` (format version), then `agents` in argument order —
each with `name`, `model` (ModelInfo), `instructions` (inline string,
`omitempty`), `policy` (`parallelism`, `max_steps`,
`max_result_bytes`, `stop_when` by name), and `tools` in registration
order — each tool with `name`, `description`, `input_schema`,
`output_schema` (omitted for a verbatim `string` Out), and `source`
(the defining `Tool(...)` call site, `file:line`).

Determinism: `json.MarshalIndent(v, "", "  ")` plus a trailing newline;
`encoding/json` sorts map keys (schemas); tools and agents keep their
orders. No timestamps, no self-hash. The same agents produce the same
bytes (`TestManifestIsStable`); the format itself is pinned by exact
bytes (`TestManifestPinsFormat`).

Generation is a golden test, not a build step:

```go
func TestManifest(t *testing.T) {
	b, err := weft.Manifest(newAgent())
	if err != nil { t.Fatal(err) }
	wefttest.Golden(t, "weft.json", b)
}
```

A stale file fails `go test`; regenerate with `go test ./... -update`
and commit. (The `-update` flag must come after the package list —
before it, the go tool routes it to the wrong package's binary.) The
future `weft manifest` command (§13) runs the same test with `-update`;
Studio (§12) reads the file. Both are owed by their own items.

The decisions the TODO spec left open, resolved:

1. **Unnamed and duplicate agent names are errors** from `Manifest`,
   not auto-names. The file is a review artifact; `agent_1` in a diff
   is noise, and fail-loud matches `New`'s panics. Hence the `Name`
   option, which also surfaces on `RunStart.Agent`.
2. **`source` is captured in `Tool(...)`** with `runtime.Caller(1)` and
   rendered module-relative by walking up from the source file's
   directory to the nearest `go.mod` (`go list -m` is not available at
   run time, but the file is). A path outside any module — the module
   cache, a sibling checkout — keeps its absolute form; the manifest is
   then machine-dependent for out-of-module tools. Accepted: the
   relativisation is by design, not by accident (a longest-common-prefix
   with the test binary's working directory was rejected — it yields
   the package dir for same-package tools and an arbitrary ancestor for
   others). Reflection on function *names* stays forbidden (playbook
   §4); a file:line is not a name.
3. **`output_schema` is reflected from `Out`** and stored on
   `ToolDef.OutputSchema`, which also serves MCP exposition (§7.3). A
   `string` `Out` has **no** output schema — it is sent verbatim
   (ADR 0003); other scalars get their scalar schema, structs the
   object schema.
4. **`instructions` is the inline string** (`omitempty`). When
   `PromptFile` exists it becomes the `{file, sha256}` object; a string
   or an object on the same key is a documented union, and readers
   (Studio) handle both from day one.
5. **Stop conditions are named.** This required a type change (taken
   deliberately, pre-release and pre-push; see the separate commit
   `core: StopCondition as interface with StopFunc`): `StopCondition`
   became an interface with `Stop([]StepRecord) bool`, `StopFunc`
   adapts ordinary functions (the `http.HandlerFunc` shape), and
   `HasToolCall`/`StepCountIs` return unexported structs that also
   implement `fmt.Stringer`. The manifest prints `String()` —
   `has_tool_call:submit`, `step_count_is:3` — else `custom`; Go cannot
   recover a closure's captured arguments, so `custom` is the honest
   floor. Once the apidiff gate (§1.7) exists, this change would be a
   permanent CI failure — it landed in the last window where it was
   free. The rejected fallback, `"stop_when": {"count": n}`, would have
   made the field useless for the built-ins.
6. **Format versioning is additive.** `"weft": 1` bumps only on a
   breaking shape change; new fields (per-tool policy, subagent
   children, MCP servers, instructions-as-file) are additive additions
   from the items that produce them, not from this one.
7. **`wefttest.Golden` owns the `-update` flag**, registered with
   `flag.Bool` at package init — the Go golden-file convention, and
   `wefttest` is only ever linked into test binaries. It writes with
   `os.WriteFile(path, got, 0o644)` after creating parent directories;
   a mismatch fails with a hand-rolled line diff (no dependency); a
   missing file without `-update` fails with the regenerate hint.
   `source` is rendered with `/` separators on every OS, and is
   omitted for a `ToolDef` that did not come through `Tool(...)`.

## Amendment (2026-09-19 — the `subagent` field, TODO §5.1 / ADR 0014)

`manifestTool` gains `subagent` (`omitempty`): the child agent's name
when the tool came from `Subagent`, omitted for ordinary tools and for
an unnamed child — so every existing golden file is unchanged. The
manifest does **not** recurse into the child: it describes the agents
it was given, and Studio draws the delegation edge by name when both
ends are in the fleet.

`policy` gains three keys for the §5 budgets: `max_model_retries`,
always written like `max_steps` because it has a non-zero default (the
one §5 change that touched a committed golden —
`examples/getting-started/weft.json` regenerated); `usage_limit`
(`{"input_tokens": n, "output_tokens": n}`, zero fields omitted, the
whole object omitted when no limit is set); and `detect_loops`
(`omitempty`, so off-by-default agents write nothing). `PrepareStep`
is not described: it is code, like middleware.

## Alternatives considered

- **A registry the Studio queries live**: couples consumers to a
  running process; no review artifact; rejected.
- **Hand-maintained spec files**: a second source of truth; drift is
  guaranteed; rejected (and forbidden by the playbook).
- **`go:generate` instead of a test**: a build step nobody runs locally
  and CI cannot gate cheaply; a golden test fails exactly when the
  committed file lies.

## Consequences

- Reviewing an agent change means reading the `weft.json` diff: tools
  added/removed, schemas changed, policy moved — visible at a glance.
- Compatibility checks diff two manifests to find removed tools or
  changed schemas (tool-schema-design.md §9).
- The line number in `source` moves with edits — expected churn, same
  as any pinned file:line.


## Amendment (2026-09-19 — no MCP field; M14)

Imported MCP tools are ordinary tools: registered statically they list
with name, description and the raw foreign schema (`input_schema`
emits `Schema.MarshalJSON`'s verbatim bytes), no `source` (the RawTool
rule). The format gains no MCP field: a server's tool count is runtime
data, not code, so TODO 2.9's "MCP servers with tool counts" sentence
is retired to Studio's run view (ADR 0015).

## Amendment (2026-10-26 — the middleware onion and the hooks; UX plan U23.1)

Additive. Each agent may carry `model_middleware` and `tool_middleware`
(outer to inner), each tool its own `middleware`, and `hooks`
(`prepare_step`, `tap`, `on_run_end` lists, `tool_source`); every key is
omitted when empty, so an agent without them renders byte for byte as
before. An entry is `{name, params?, source?, opaque?}` — a model
layer's optional `Describe() core.MiddlewareInfo`, else its type; a
function by `runtime.FuncForPC`, weft's own by name alone, any other with
its `source` file:line (the import-path form, as a tool's `source` is); a
model layer without `Unwrap` ends the walk `opaque`. Configuration only,
never a captured value. A `source` is never an absolute path (U23.1-fix;
a tool's `source` neither, the same helper), and since U23.2-fix it has
one form: the defining function's package import path and the file's
base name (`github.com/x/app/tools.go:12`; a main package by the
binary's build-info path, else `main/f.go`), the module path without
its `@version` for a module-cache file whose function is unknown. No
go.mod is consulted — U23.1-fix's module-relative form on a host with
the sources and the import-path form on one without made one binary two
manifests, the deploy host reading a false "stale" — so the document,
and `weft.manifest.hash`, are the same on every machine. One build
reads differently: `go run main.go` (a file list) has no build-info
path, so a main package's sources read `main/f.go` and its runs read
stale against a weft.json generated by `go test` or a module build —
run a package (`go run .`; `weft dev`'s help says so). A package path
the linker escapes (a dot in its last element: `yaml%2ev3`) is
unescaped (U23.2-fix2). The price is churn: a hook's or a non-weft middleware's
`source` is a file:line, as a tool's is, so moving the function — an
edit above it — changes the agent's hash, and the runs after it read
as another version until weft.json is regenerated. weft's own
middleware are named without a source, so a weft upgrade that moves
them does not. An agent whose chain or hooks change now hashes
differently (`weft.manifest.hash`), which is the point: which onion ran
is part of which version of the agent ran. The record contract's "The
onion in the manifest" paragraph is the reader's spec.
