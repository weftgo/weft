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


## Amendment (2026-09-12): per-tool policy and schema-shaped decode errors

**Trailing options on `Tool` and `RawTool`** (`opts ...ToolOption`) set
policy for one tool. `Timeout`, `MaxResultBytes`, and `StrictInput` are
`PolicyOption`s — both `Option` and `ToolOption` — so the same name sets
the agent-wide default in `New` and the override in `Tool`. The loop
resolves tool-then-agent; `Agent.CallTool`/`ToolDef.Invoke` apply only
the tool's own `StrictInput` (timeouts and caps are run policy). The
manifest records both levels, and a tool without options renders
exactly as before.

**`Timeout`** puts the deadline on the handler's ctx; on expiry the loop
records `tool "X" timed out after d` as an error result and abandons the
handler's goroutine. A hung tool must not hang the run; a handler that
ignores ctx leaks its goroutine, which is the handler's bug. A run-ctx
cancellation during a timed call is reported as that cancellation.

**Decode errors name the field.** `ErrInvalidToolInput` results now read
`field "days": expected integer, got string`, `expected object at the
top level, got array`, `invalid JSON at offset N: …`, or `unknown field
"units": not in the schema` — the schema's own vocabulary, so the model
can map the error back to the schema it was shown. Lenient decoding
stays the default (stray keys cost a round trip, not accuracy);
`StrictInput` is the opt-in rejection.

Model-visible strings pinned by tests: the timeout message, the four
decode messages.


## Amendment (2026-09-14): the rest of the per-tool policy — TODO §4.3

With the tool seam landed (ADR 0006), the options that were blocked on
it ship, all as trailing options on `Tool`/`RawTool`:

- **`Sequential()`** on a tool is a barrier (in-flight calls finish, it
  runs alone, the step resumes). `Sequential` now returns
  `PolicyOption`, like `Timeout`/`MaxResultBytes`/`StrictInput`.
- **`WrapTools(mw...)`** on a tool wraps that tool alone, inside the
  agent-level chain.
- **`PromptSnippet(text)`**: the loop composes the advertised tools'
  snippets into `ModelRequest.System` after the instructions, one
  paragraph per tool, blank-line separated. Model-visible (the system
  prompt), pinned by `TestPromptSnippetsComposeIntoInstructions`.
- **`Replay(ReplaySafe|ReplayNever)`**: an annotation for checkpoint
  restart (§11); accessor `ToolDef.ReplayPolicy()`. (Retracted 2026-09-18 until the store ships — ADR 0006 amendment.)
- **`RequireApproval()`** — ADR 0007.

The manifest records each (`sequential`, `require_approval`, `replay`,
`prompt_snippet`); tools without them render exactly as before.
Decode failures are now coded `INVALID_INPUT: tool "x": …` (ADR 0002).


## Amendment (2026-09-18, from the 2026-09-18 code review — the schema
## derivation rules the code shipped without their ADR)

Three model-visible derivation rules are contract as of this
amendment; each was already on the wire.

- **`json:",string"` scalars derive `{"type":"string"}`** for the kinds
  that support the option (strings, bools, integers, floats): the wire
  form is a quoted value, and advertising the bare type would invite
  exactly the unquoted value decoding rejects. The property description
  still comes from the `jsonschema` tag.
- **Embedded shadowing follows encoding/json's dominance rules in
  full**: the shallowest embedding depth wins; at equal depth exactly
  one json-tagged claim beats untagged ones, and any other tie cancels
  the name (encoding/json drops it from the wire, so the schema must
  not advertise it). Two fields of the same struct claiming one JSON
  name — always both tagged — panic at construction, at any *named*
  nesting depth: each named struct derives its own depth-0 space, and
  its unreachable handler field is no less a bug than the input
  struct's — the duplicate-name and non-struct-input precedent. The
  same collision inside an embedded struct does not panic: its claims
  flatten into the parent's dominance rules and the name drops,
  keeping encoding/json's drop. One deliberate,
  conservative divergence: in a double diamond (the same field reached
  through two same-depth paths via a shared intermediate) the schema
  cancels the name while encoding/json's breadth-first resolver may
  keep it — the cancelled side never advertises what decode cannot
  reliably deliver. Pinned in `schema_conflict_test.go`.
- **Maps derive `additionalProperties` typing the values** (JSON Schema
  draft 2020-12): `map[string]int` → object of integers, with
  `map[string]any` a bare object (an any value type has nothing to
  say; an empty `additionalProperties:{}` would say nothing new). The
  boolean false form is not expressible — reflection always has a
  value type.

Decode-error text now derives from the same mapping as the schema
(`schemaTypeName` is gone as a second source of truth): a `[]byte`
field reports "expected string", matching the advertised schema, and
a json ",string" mismatch renders as `expected string: a ",string"
field's value must arrive inside quotes` — the schema's vocabulary,
not the Go type (encoding/json reports it as a plain error naming
the Go type inside the quotes).


## Amendment (2026-09-18 — `Replay` retracted)

The `Replay` option above is deleted until the checkpoint store ships
(ADR 0006's same-day amendment carries the reasoning); the manifest's
`replay` key goes with it. Everything else in the 2026-09-14 list
stands.


## Amendment (2026-09-19 — §7.1: the schema corpus, the verdict,
## ParseSchema, and one derivation bug the corpus found)

TODO §7.1 measured the hand-rolled reflector against
`google/jsonschema-go` v0.4.3 (`jsonschema.For[T]`, the reflector
behind the official MCP SDK) on a corpus of tool input structs — one
per derivation rule this ADR pins (`mcp/schema_corpus_test.go`; the
table prints under `go test -v`).

**Verdict: keep the hand-rolled, zero-dependency reflector.** The
plan's prediction held, and the evidence is stronger than predicted —
three rows show jsonschema-go v0.4.3 deriving a schema that does not
match the wire `encoding/json` produces, and it cannot derive
recursive types at all:

| Row class | Rows | Finding |
|---|---|---|
| identical (policy-only differences) | 17 of 22 | scalars, pointers, `omitempty`, naming, descriptions, arrays, maps, nested structs, `time.Time`, `any`/interface fields, untagged and shadowing embeds, pointer-to-struct `In`, the repo's own shapes, empty struct — identical once weft's documented policies are set aside: struct objects left open (the loop decodes leniently; a schema must not advertise a constraint the decoder does not enforce), pointer ⇒ optional (this ADR), `format: date-time` (this ADR's 2026-09-09 amendment), optional narrowing bounds omitted (`minimum`/`maximum` on integer widths, `minItems`/`maxItems` on fixed arrays — decode still rejects out-of-range values, so nothing is silently accepted) |
| jsonschema-go wire-wrong | `[]byte`, `json:",string"`, tagged embed | `[]byte` derives an array of 0..255 integers where the wire is a base64 string; `,string` fields derive the bare type where the wire demands quotes; an embedded struct with a json name tag is flattened where the wire nests it under the tag. weft's renderings are the wire forms (the `,string` rule is this ADR's 2026-09-18 amendment) |
| jsonschema-go cannot derive | recursive types | `For` fails with "cycle detected"; weft terminates with the bare object the loop decodes into (this ADR's original rule) |

One real weft bug surfaced and is fixed: **a tagged embed whose type
name is unexported** (`type input struct{ hidden `json:"cfg"}`) is
marshalled by `encoding/json` as a nested `"cfg"` object but was
dropped from the schema — the `IsExported` skip applied to anonymous
fields, which encoding/json exempts. The schema now advertises it
(`schema_conflict_test.go` pins it). No golden changed: no existing
input used the shape.

**`weft.ParseSchema(b json.RawMessage) (*Schema, error)`** reads a
JSON Schema document from outside Go — an MCP server's `inputSchema`,
a plugin manifest: the structured fields it knows are populated for
readers that walk the tree, and the document's own bytes are kept in
an unexported field that the new `Schema.MarshalJSON` re-emits
verbatim. An `enum`, `oneOf`, `minimum`, `pattern` or `$ref` the
`Schema` type cannot express therefore reaches the model exactly as
the server wrote it, instead of being degraded to the struct's
vocabulary — the import half of the `oneOf` residue (§7.4). The
top-level type must be an object, enforced at parse (fail at import,
not at the first model call). A reflected schema has no stored bytes
and marshals exactly as before, so every committed golden is
unchanged.

**One rendering change rode the new single schema path:** the
adapters' hand-built schema maps (`adapterkit.SchemaMap`, and
google's `genaiSchema`) were replaced by render-through-`json.Marshal`
(so a parsed schema crosses whole; one rendering instead of two). The
hand-built map always wrote `"type"`, so an unconstrained node (a
recursion cut, an interface field) reached OpenAI and Anthropic as
`{"type": ""}` and now reaches them as `{}` — the same thing the
reflected encoding always said. No test pinned the old bytes; the new
rendering is pinned in `adapterkit_test.go`. Gemini's conversion goes
through the same round trip and drops what `genai.Schema` has no field
for (`additionalProperties`, `oneOf`, …), the documented per-field
Gemini limit, as before.

The same-depth cancellation row could not sit in the corpus (declaring
the conflicting embeds trips go vet's structtag check); it stays
pinned in `schema_conflict_test.go`.


## Amendment (2026-09-19 — §7.4: the `oneOf` residue, resolved)

The corpus above cannot show demand for `oneOf` — it compares two
reflectors on Go structs, and neither can express a union; demand
comes from consumers, not from a table of structs. The honest split:

- **Import is solved** by bytes-through: a foreign `oneOf`, `enum`,
  `const`, `pattern` or `$ref` reaches the model whole through
  `ParseSchema` (the amendment above). That was the case with real
  demand — every MCP server in the wild uses `enum` — and it needed no
  reflector change.
- **Export keeps the stated limits**: Go's type system has no sum
  types; model a union as separate tools or a discriminated string
  field. An optional `go:generate` step remains the planned answer
  *if* a consumer arrives with a struct that cannot be expressed —
  the decision is deferred to that consumer, not to a date. No
  codegen ships in v1.

The open-questions register's richer-tag-grammar row resolves the same
way: decided direction (an `enum` tag is a core change to
model-visible bytes with its own amendment and pinned tests), not
scheduled — it ships when a consumer's tool needs it.
