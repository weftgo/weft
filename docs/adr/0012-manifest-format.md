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
