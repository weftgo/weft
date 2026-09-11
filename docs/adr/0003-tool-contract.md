# ADR 0003 — The tool contract

- Status: decided (v0 skeleton, 2026-09-09)
- Phase-1 blocker from THE-END-GOAL.md: MCP interop and the whole DX story
  hang off this contract.

## Context

The Go ecosystem has settled the mechanism: the official Go MCP SDK
(`modelcontextprotocol/go-sdk`) registers tools as
`AddTool[In, Out any](s, t, h ToolHandlerFor[In, Out])` and reflects input/
output schemas from the structs via `google/jsonschema-go`. Adopting the
same shape makes every weft tool exposable as an MCP tool for free —
checkable property #3 in the vision doc.

## Decision

**Tools are generic over their input and output structs; the schema is
reflected from the input struct and its tags.**

```go
weft.Tool("refund_order", "Refund a customer's order",
    func(ctx context.Context, in RefundInput) (Receipt, error) { ... })
```

- `weft.Tool[In, Out]` infers both type parameters from the handler; the
  compiler, not reflection, checks the handler's shape.
- Schema derivation (see `schema.go`):
  - `json` tag names the property; `jsonschema` tag is the description;
  - pointer or `omitempty` ⇒ optional, everything else ⇒ required;
  - embedded structs contribute their fields, matching `encoding/json`
    (including embedded unexported struct types);
  - `time.Time` ⇒ string, `[]byte` ⇒ string, maps ⇒ object.
- The derived schema subset intentionally tracks what
  `google/jsonschema-go` produces, so conversion to MCP tool definitions
  is mechanical.
- **What the model sees**: a `string` output is sent verbatim; any other
  output is JSON-encoded. Most tools return prose, and `"sunny"` with
  JSON quotes is noise the model has to see through (Pydantic AI and the
  MCP SDK send text as text). `ToolDef.Invoke` and `Agent.CallTool`
  return that same text.

**Zero external dependencies for now.** The core currently hand-rolls the
subset above; when MCP exposition lands, we evaluate depending on
`google/jsonschema-go` directly (one well-known dependency, same output)
versus keeping the hand-rolled subset and converting. Either way the public
`weft.Tool` call site does not change.

## Amendment (2026-09-09, from REVIEW-v0 findings 17 and 22)

- **`Tool` rejects non-struct inputs at construction.** Providers and
  MCP require an object at the top level of a tool schema; a scalar `In`
  would derive `{"type":"string"}` and fail every real call. `Tool` now
  panics on non-struct `In` (structs and pointers to structs are
  allowed), matching the duplicate-name panic: fail loud, fail early.
- **`time.Time` carries `"format":"date-time"`.** The `Schema` type has
  a `Format` field; `time.Time` derives `{"type":"string",
  "format":"date-time"}`, closing one fidelity gap toward
  `google/jsonschema-go` ahead of the MCP round-trip measurement.

## Known gaps (deliberate)

- No union types / `oneOf` — Go's type system doesn't express them; model as
  separate tools or a discriminated string field for now.
- Interface fields degrade to unconstrained values.
- If real usage demands stricter schemas, an optional `go:generate` step
  (not runtime reflection) is the planned answer — codegen for the edges,
  reflection for the common case.

## Consequences

- Tool authors write one function; name, schema, and JSON plumbing are
  derived — nothing written twice.
- `ToolDef.Invoke` is public, so manual dispatchers and future MCP server
  exposition share the same path as the loop.
- Duplicate tool names panic at construction: fail loud, fail early.


## Amendment (2026-09-11): RawTool and ToolSource — tools defined
outside Go source, tools that change while the agent runs

Motivated by bobina, weft's first consumer (bobina SPEC §9 W-1/W-2).

**`RawTool(name, description, *Schema, fn)`** constructs a `ToolDef`
from an explicit schema instead of reflection. `fn` receives the
model's raw JSON arguments verbatim — no unmarshalling, no
`ErrInvalidToolInput`: validation belongs to `fn`. Nil schema becomes
the empty object schema. The manifest records no source line: the
definition is not in Go source (plugin manifests, MCP remotes §7.3).

**`ToolSource(fn func() []*ToolDef)`** replaces the list the loop
advertises and dispatches against with `fn`'s fresh return value,
fetched once per step (and per `CallTool`). The Agent stays immutable;
ownership of synchronization and name uniqueness stays with the
registry behind the source. `Tools()` and `Manifest` remain the static
construction-time set — a manifest describes the code, not a registry.

The decision — tools are values, reflection is a convenience — is
unchanged: RawTool is the same value with the schema supplied instead
of derived, and ToolSource is the same list, fetched instead of stored.
