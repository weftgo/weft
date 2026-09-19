# ADR 0015 — MCP interop

- Status: decided (2026-09-19, TODO §7; plan `docs/phase2-mcp-plan.md`)
- Numbering: 0015, after 0014 (subagents) — the TODO items reserving
  0008–0011 have shipped or own their numbers.
- THE-END-GOAL checkable property 3: "MCP-native both ways — the tool
  contract is shape-compatible with the official Go MCP SDK, so tools
  are consumable *and* exposable without adapters." This ADR records
  what shipped and every open decision the plan resolved with a guess.

## Context

ADR 0003 bet that making the tool contract *shape*-compatible with the
SDK (`Tool[In, Out]` mirrors `AddTool[In, Out]`, the schema reflected
from the struct) would make the bridge **mechanical** — a conversion of
already-identical shapes, not a translation layer with its own
semantics. §7.1 measured the bet (ADR 0003's 2026-09-19 amendment: the
hand-rolled reflector stays; jsonschema-go v0.4.3 is wire-wrong on
`[]byte`, `,string` and tagged embeds and cannot derive recursive
types) and §7.2/§7.3 cashed it. The deep dive (ch. 22 "Annotations",
ch. 24 "Protocols", ch. 5's two-channel note) argued the mapping; this
ADR records the mechanics.

## Decision

**One satellite module, `weft/mcp`, over the official SDK
(`github.com/modelcontextprotocol/go-sdk` v1.8.0), three functions and
two option constructors.** The core gains three additive exports
(`ParseSchema`, `Agent.Name`, `ToolDef.RequiresApproval`) plus
`Schema.MarshalJSON` — and no dependency, no import edge upward, no
new interface, event, option kind, or manifest field. The SDK's
package is also named `mcp`; ours keeps the name and the SDK is
aliased `sdk` in godoc, examples and tests — the bridge is what a
weft user is reading about (the alternative, `weftmcp`, breaks the
package-name-equals-path-element convention for every module to save
one alias line).

### Expose — `AddTools(s *sdk.Server, tools ...*weft.ToolDef)` and `Serve(s, a, description)`

`AddTools` registers each tool under its own name, description, input
schema (nil normalised to the empty object — the SDK panics on nil)
and, for a non-string `Out`, output schema. A call runs
`ToolDef.Invoke` through the SDK's raw `ToolHandler` — not the generic
`AddTool[In, Out]`, which would reflect a second schema and decode a
second time. The result text is one `TextContent` plus
`structuredContent` holding the same JSON when there is an output
schema; **every failure — undecodable arguments, a handler error, a
panic — is an `isError` result carrying the same text weft's loop
would show its own model** (ADR 0002's pinned bytes, over the wire).
Panic containment matches the loop's `invokeContained` (`tool "x"
panicked: …`), so a panicking tool is an error result, not a dead
server. Run policy (Timeout, MaxResultBytes, WrapTools) is not
applied: the exposition is the tool, not an agent. `AddTools` panics
on a nil tool, a duplicate name (the SDK replaces same-named tools
silently; weft fails loud, like `New`), **and a `RequireApproval`
tool** — MCP has no approval channel, and running a gated tool
unapproved would be a silent policy bypass.

`Serve` is `weft.Subagent(a.Name(), description, a)` — literally: the
same `{"prompt": string}` schema, the same `Output` handling, the same
final-text rule — plus the agent's tools, each dispatched through
`Agent.CallTool` so `WrapTools` and the approval policy apply: a gated
tool answers with the `ErrApprovalRequired` text (`weft: tool call
requires approval: tool "x"`) instead of running — loud where AddTools
refuses outright, because the gate is answering, not bypassed.
**`Serve` takes the description** (a departure from the TODO's
`Serve(s, a)`): the agent has no description of itself — instructions
are *for* the model inside the agent, not *about* the agent to a
caller — and a routing surface synthesised from them would be the one
place in weft where model-visible text is invented rather than
written. `Subagent` made the same call. Serve panics on an unnamed
agent (the manifest's rule) and runs nothing of the child back over
MCP in v1 (no streaming; the SDK's progress notifications are recorded
below as the later path).

**No `ToolAnnotations` are emitted on export in v1.** Mapping
`RequireApproval`/`Sequential` to `readOnlyHint`/`destructiveHint`
would need three more read accessors and is not one-to-one (weft's
parallel default is not a `readOnlyHint: true` claim), and the spec's
defaults treat an un-annotated tool exactly as weft want an unknown
tool treated by a careful client. Revisit when a consumer asks for
`idempotentHint` (`Replay`'s return).

### Consume — `Tools(ctx, sess, opts ...Option)` with `Prefix` and `Policy`

The TODO's `Tools(ctx, sess)` gained a variadic `Option`: `weft.ToolOption`
is sealed, so the satellite cannot define one, and a tool's policy
applies only at construction (`RawTool(..., opts...)`) — `Policy` is
how "everything the tool contract offers applies" (the
imported-tools-are-ordinary principle) is true for a tool the caller
never constructs. `Prefix("gh_")` renames for the model and the
manifest while the remote call keeps the server's name — without it
two servers exposing `search` panic at `New`, and a renamed ToolDef
would call the wrong tool. An allow/deny list is **not** an option:
the caller filters the returned slice — plain Go. Both options are
earned constructors on a sealed `mcp.Option`, the weft idiom.

Listing goes through the SDK's paginating iterator; **a schema that
cannot parse fails the whole import naming the tool** — one bad tool
fails loudly rather than dropping silently. The SDK's client hands the
schema over as decoded JSON, so the bytes `ParseSchema` keeps are the
server's document in `encoding/json`'s canonical key order — every
keyword, nothing degraded; that is what the model sees, consistently.
Argument bytes cross to `tools/call` verbatim (`json.RawMessage`
marshals as its own bytes; empty or null go as `{}`, which the
protocol requires).

**Result rendering, pinned by `TestRenderContent`:**

| server sent | the model sees |
|---|---|
| `structuredContent` present | its JSON, compact, regardless of `content` — the spec calls text the backwards-compatible duplicate |
| text items only | the texts joined by `"\n"` |
| non-text items (image, audio, resource) | one line per item: `[image image/png, 9 bytes]`, `[resource <uri>]` — the model learns a payload existed; weft's transcript has no binary tool results (ADR 0001), so this is the honest rendering, not a silent drop |
| `isError: true` | the same rendering, as the error text |

**Errors: two channels, one boundary.** A remote `isError` result is a
tool error whose text is the server's, verbatim, with
`errors.Is(err, mcp.ErrToolError)` true for middleware. A transport or
protocol failure is a tool error reading `mcp: <err>`. Both are data
the model sees and can stop calling — never run errors (ADR 0002; the
deep dive's ch. 5).

**Annotations → policy.** An imported tool without `readOnlyHint:
true` is `Sequential()`; one with it stays parallel. The spec defaults
`readOnlyHint` to false and says annotations "MUST be considered
untrusted"; weft's rule for its own tools is parallel by default. They
reconcile the way Claude Code reconciles them: *the burden of proof
inverts for a tool whose handler you cannot read* — a foreign tool is
sequential until its server says otherwise, the conservative reading
of an untrusted hint (trusting a false `readOnlyHint` risks running a
mutation in parallel; distrusting a missing one costs latency).
`Policy` applies after the annotation defaults, so the default cannot
be undone and `RequireApproval` is added, never removed.
`destructiveHint` (default true) maps to nothing automatic — honouring
the default would gate every un-annotated tool behind approval, and
`Policy(weft.RequireApproval())` is one line for the caller who wants
exactly that. The rejected alternative — ignore annotations entirely,
parallel like any tool — would make the only safety signal the
protocol carries for a black-box tool inert by default.

**Descriptions are untrusted content.** A server's tool descriptions
land in the model's tool list, a prompt-injection surface the caller
did not author. The godoc and README say so and point at `mw.Allow`
and at reading `Tools()`' output before registering it. No
sanitising: that would be silent behaviour.

**`listChanged`, connection fan-in, elicitation, sampling, the kill
switch.** A changing list is caller code: the SDK's
`ToolListChangedHandler` re-runs `Tools` into a slice served through
`weft.ToolSource` (the pattern is `ExampleTools_toolSource`); a
`mcp.Source` helper was rejected — it would own a goroutine and a
mutex inside the satellite, and `ToolSource` exists so that ownership
stays with the list's owner. Connecting several servers concurrently
under a budget is the caller's `errgroup` + ctx (`examples/client`
shows the shape with stdlib, since the satellite imports nothing
beyond weft/adapterkit/SDK/stdlib); `Tools` respects ctx and never
owns or closes a session. Elicitation is not answered: a server that
asks a client without a handler gets the SDK's refusal, which surfaces
as a tool error — loud; mapping it to a parked call belongs to
`runtime` (ADR 0007's nested case). Sampling — a server asking *our*
model to generate — is recorded, not built: it is the first MCP
feature that would make a *model* call, so it is also the first that
`WEFT_MODEL_REQUESTS` must govern, and that gets its own ADR 0013 line
when built. `tools/call` itself is not a model request; imported tools
run under `deny` — they are the caller's tools, like any handler that
makes an HTTP call.

### What the manifest records

Nothing new. An imported tool registered statically lists with its
name, description and raw schema — the `RawTool` rule: no `source`
(the definition is not in Go source), no MCP field. A server's tool
count is runtime data, not code: TODO 2.9's "MCP servers with tool
counts" sentence is retired to Studio's run view. A non-object output
schema (legal per SEP-2106) has no weft representation and is not
recorded; the tool's result text is its output either way.

## The property, tested

`TestRoundTripIsLossless`: a weft tool exported through `AddTools` to
an in-memory server and imported back through `Tools` keeps its schema
document (canonical key order aside) and answers identically — for a
reflected tool and for a `RawTool` with a foreign schema. Property 3
is true of the release in the only sense that can be tested.

## Out of scope, deliberately

MCP resources and prompts (not tools), OAuth and transport
configuration (the SDK's job; the caller hands over a connected
session), a server registry or connection pool (Crush's per-server
config is `cli`), streaming an exposed agent's events as progress
notifications, sampling (above), `idempotentHint` (waits for
`Replay`). Nothing here makes them harder.

## Consequences

- `weft/mcp` is the fourth satellite module; `go.work`, `make
  test`/`vet`/`lint`/`offline` and CI pick it up with no workflow
  change (they loop `go list -m`).
- Every test runs over `sdk.NewInMemoryTransports()` — offline by
  construction, green under `WEFT_MODEL_REQUESTS=deny`.
- Examples live at `mcp/examples/{server,client}` (the root module is
  dependency-free; ADR 0013 decision 7's pattern), each tested
  in-memory; the TODO's `examples/mcp-*` paths are reconciled to them.
- The bridge is two marshal calls per direction (`toSDK`, `fromSDK`):
  P1 held. Where the SDK's client re-decodes (schema bytes arrive as a
  map), the canonical form is what is kept — recorded here so "verbatim"
  is never over-claimed.
