# Wire-format decision input (feeds ADR 0009, TODO §10)

- Status: research notes, gathered 2026-09-19. Not a decision. ADR 0009
  decides; this file is the raw material plus the analysis that led to the
  lean recorded in the open-questions register ("AG-UI vs bespoke SSE:
  evaluate AG-UI first", `TODO.md`).
- Origin: a working session that asked four questions in sequence: what
  §10 is and whether it touches code; which other protocols exist; why
  AG-UI leads and what an adapter costs at runtime; whether a native
  protocol can ship beside the wrapper. This file records the answers
  with sources.

## 1. The decision being made

Weft's events are Go structs with a JSON form, used in process. The
`serve` module (post-v1 in the plan, `TODO.md` "wire per §10") will
stream them over HTTP to a browser or another service. §10 asks one
question with three possible answers:

1. Adopt AG-UI as the wire format.
2. Wrap it: keep weft's internal events, translate to AG-UI at the HTTP
   edge.
3. Bespoke: define weft's own HTTP event format.

The deliverable ("Done") is ADR 0009 with a mapping table. No code
changes are part of §10 itself. ADR 0009 does not exist yet; the number
is reserved (`docs/adr/` jumps from 0008 to 0012; ADR 0013 notes that
0006 through 0011 are reserved by TODO items).

Why decide before the code exists: three later consumers share the
format. `store` (§11) records events as JSON, the Inspector (§12)
replays recorded events, and `serve` streams them. A format flip after
those ship means a migration in three modules. Deciding now costs one
document.

## 2. What already exists: the internal wire format

The core already has a versioned-by-contract JSON wire format for
events (ADR 0004 amendments). Any HTTP format decision starts from
this, because the strongest "bespoke" candidate is this format plus
SSE framing, not a new invention.

Ten event types (`events.go`), each with a `type` discriminator:

| Type | Wire `type` | Carries | Notes |
|---|---|---|---|
| `RunStart` | `run_start` | run `id`, `Model`, agent name | always first |
| `StepStart` | `step_start` | step `index` | |
| `TextDelta` | `text_delta` | text increment | no `Seq` (single loop goroutine) |
| `ReasoningDelta` | `reasoning_delta` | reasoning increment | same, ordered against `TextDelta` |
| `ToolArgsDelta` | `tool_args_delta` | args increment while the model writes a call | precedes its `ToolStart` by construction |
| `ToolStart` | `tool_start` | `Seq`, `CallID`, name, `Args` | parallel tools interleave; pair by `CallID`, order by `Seq` |
| `ToolFinish` | `tool_finish` | `Seq`, `CallID`, content, `IsError` | absent for a call parked on the approval boundary |
| `StepFinish` | `step_finish` | stop `Reason`, `Usage`, raw | |
| `RunFinish` | `run_finish` | totals, `Steps`, `Pending` | `Pending` lists calls awaiting Approve/Deny (ADR 0007) |
| `Nested` | `nested` | parent `RunID`/`Seq`/`CallID` + inner `Event` | child-run events, recursive; late-event rule (ADR 0014) |

Properties already pinned by tests:

- Every event except `RunStart` carries `run_id` (amendment of
  2026-09-18) because concurrent runs on one agent interleave and `Seq`
  is unique only within a run.
- `Seq` is a per-run counter assigned at emission; it totally orders
  the stream, including nested child events, which carry the parent's
  `Seq` (amendment of 2026-09-19).
- Exact bytes are a compatibility contract: `TestEventJSONRoundTrip`
  pins them; unknown `type` is an error, never a silent drop;
  additive-only within the set (`FuzzUnmarshalEvent` in CI).
- The core has no `SchemaVersion` envelope, deliberately. The store
  module's envelope may layer its own (ADR 0004; future-vision risk
  table: "SchemaVersion from day one" for the wire-facing module).

## 3. The candidates

### 3.1 AG-UI

Open protocol from the CopilotKit team for streaming agent events into
a UI, launched early 2026. AWS Bedrock AgentCore added native support
in March 2026, so an agent speaking AG-UI plugs into that runtime and
existing agent UIs without custom work. It is the only open protocol
whose whole purpose is this exact layer.

### 3.2 Vercel AI SDK UIMessage stream

SSE-encoded typed chunks, messages as `parts[]` arrays (text, tool
runs with lifecycle states, reasoning, files), resumable streams via
Redis-backed stream ids, backpressure on chunk callbacks
(`docs/frameworks/vercel-ai-sdk.md` lines 251 and 253). Default wire
format of the Next.js ecosystem. Tied to one vendor's ecosystem, which
is why it is the runner-up rather than the lead.

### 3.3 ACP (Agent Client Protocol)

Streams agent events to editors and canvases. Used by Zed and
OpenHands; Deer-flow 2.0 made it a hard dependency so Codex, Claude
Code, and OpenClaw can drive it ("One protocol"). Targets IDE-style
clients, not web pages. Relevant if weft ever needs a canvas or editor
frontend. Beware the name collision with IBM/BeeAI's Agent
Communication Protocol, which is an agent-to-agent protocol
(`AGENTIC-STACK-2026.md` section 9).

### 3.4 Bespoke native: weft event JSON over SSE

The internal format of section 2, framed as SSE. This is "invent"
only in the sense of specifying framing, resume semantics, and an HTTP
contract; the event bytes already exist and are test-pinned.

### 3.5 Adjacent protocols that are not candidates

- MCP (agent to tools): covered by ADR 0015. "Agents-as-MCP-servers"
  is a post-v1 `serve` idea but it exposes tools, not the run stream.
- MCP Apps (Jan 2026): tools return interactive HTML. Different
  problem.
- A2A (agent to agent, 1.0 April 2026): delegation and discovery.
- LangGraph Platform SSE: what Deer-flow speaks natively. Instructive
  mainly as a churn warning; three stream-event generations ship
  simultaneously in 1.2.x (`docs/frameworks/langgraph.md` line 231).

## 4. Evidence from the research corpus

| Framework | Wire choice | Data point for weft |
|---|---|---|
| Pydantic AI | one internal event union; `PA/ui` adapters translate to AG-UI and Vercel data stream (`docs/frameworks/pydantic-ai.md` line 74) | the wrap pattern supports multiple wire formats behind one adapter interface |
| Deer-flow | custom SSE, then LangGraph Platform SSE, then +ACP; the arc is cited as evidence for adopting an existing protocol (`docs/frameworks/deer-flow.md` line 912) | bespoke first cost them a rewrite of the wire layer |
| pi | own CBOR protocol, "no compatibility guarantees"; own `pi-messages` provider format; called their biggest maintenance bill (`docs/frameworks/pi.md` line 63) | owning a wire format is a permanent bill |
| Vercel AI SDK | four breaking majors in about 20 months; wire-format churn named as the cause | the adapter edge contains churn damage |
| Crush | generated OpenAPI SSE spec, one consumer; notes it could adopt AG-UI externally while keeping JSON internally (`docs/frameworks/crush.md` line 843) | internal JSON plus external protocol is a shipping pattern |
| Mastra | own evented pubsub for Studio | own format, works, but the only consumer is their own UI |

The corpus contains no case of a bespoke HTTP event format that won
outside its own product, and three cases (pi, LangGraph, Vercel) where
owning one generated steady cost.

## 5. Performance: where the time goes

An agent event stream is tens to hundreds of small events per turn,
under an LLM that takes hundreds of milliseconds to seconds per turn.
Rough budget per event decision:

- JSON encode of one small event: microseconds.
- SSE framing and network: milliseconds.
- The model generating the tokens the events describe: hundreds to
  thousands of milliseconds.

A binary format (CBOR, protobuf) optimizes the first line, which is
the noise floor. Pi, which owns a 4-byte-framed CBOR protocol, still
fixed its worst streaming bug by switching from cumulative to
delta-only events (v0.84.0 quadratic-output fix): a semantic choice,
available to JSON unchanged.

Design levers in order of measured impact, all of them semantic:

1. Delta-only events, never cumulative snapshots.
2. `Seq` ordering so consumers can sort interleaved parallel streams.
3. Backpressure: bounded buffers so a slow client cannot balloon
   server memory.
4. Resumability: reconnect with `Last-Event-ID` or `Seq` and replay
   from the store instead of rerunning.
5. Small payloads: no state resend.
6. Last, only with a measured bottleneck: binary encoding. Brotli
   compression over SSE usually buys more than a format swap and
   changes nothing architectural.

The adapter translation itself is a struct-to-struct field map,
microseconds per event, with no extra serialization pass if it maps
structs before encoding. Next to the model's latency it does not
register.

## 6. The architecture that makes the choice reversible

The standing rule "the core never knows about HTTP" already forces the
shape: a translator lives only in `serve`. Consequences:

- Store and Inspector consume internal events forever; they never see
  the wire format.
- Adding a protocol later (Vercel format, ACP) means adding one
  translator file at the edge. Pydantic AI's `PA/ui` is this pattern
  with two implementations today.
- A protocol swap or a major-version bump of AG-UI touches one module;
  recorded runs and the Inspector do not migrate. That containment,
  not the microseconds, is the performance argument.
- A weft program that never runs `serve` pays nothing; the translator
  is not in its binary.

So the decision is reversible in the direction that matters. What is
not cheap to reverse is the semantic contract the store records
against, which is why the decision is front-loaded.

## 7. The gaps AG-UI must cover (mapping-table seed)

This is the section ADR 0009 must finish. Preliminary, to verify
against the AG-UI spec before the ADR is written:

| weft | AG-UI (expected) | Gap |
|---|---|---|
| `RunStart` | `RUN_STARTED` | weft carries `Model` + agent name |
| `StepStart` / `StepFinish` | `STEP_STARTED` / `STEP_FINISHED` | weft `StepFinish` carries `Usage` + stop reason |
| `TextDelta` | `TEXT_MESSAGE_CONTENT` | AG-UI needs a message id; weft has none (deltas are not messages) |
| `ReasoningDelta` | thinking/reasoning support (verify) | |
| `ToolArgsDelta` | `TOOL_CALL_ARGS` | |
| `ToolStart` / `ToolFinish` | `TOOL_CALL_START` / `TOOL_CALL_END` | weft `Seq` has no AG-UI field |
| `RunFinish` | `RUN_FINISHED` | `Pending` (approval boundary) has no AG-UI event |
| `Nested` | none | child-run nesting needs `CUSTOM` events or an extension |
| `run_id` on every event | session/thread level | attribution of interleaved runs |

Three hard gaps, matching §10's list: `Seq` (total ordering of
interleaved parallel tools), `Nested` (child runs under a parent's
call), `Pending` (run ended awaiting approval). Each must be answered
one of three ways in the ADR: AG-UI custom/extension events carry it,
the consumer reconstructs it, or it is a reason to prefer bespoke for
the default path. The ADR must also decide whether the missing
`Seq` ordering is a correctness problem for AG-UI consumers that
receive interleaved tool events out of order.

## 8. Lean from the working session (not a decision)

Dual path, if the mapping table confirms the gaps:

- Native default: the internal event JSON of section 2 over SSE, with
  `SchemaVersion` on the serve envelope, delta-only, `Seq`, bounded
  buffers, resume from the store. Near-zero new format work; the bytes
  are already pinned by tests.
- AG-UI as an optional adapter module at the `serve` edge for
  ecosystem interop (Bedrock AgentCore, existing agent UIs), gaps
  carried via custom events where possible.

This keeps the fast path lossless, keeps interop, and confines each
format's maintenance to one translator. The bill the ADR must
acknowledge: a bespoke default means weft owns an HTTP contract and,
sooner or later, a TypeScript client SDK for browser consumers. That
is the pi bill, bounded to one edge module.

## 9. Checklist for ADR 0009

- [ ] Verify the section 7 mapping against the current AG-UI spec;
      fill every row, including `ReasoningDelta`.
- [ ] For each of `Seq`, `Nested`, `Pending`, `run_id`: carried,
      reconstructed, or bespoke-only. Cite the spec.
- [ ] Decide adopt / wrap / bespoke for the default path, and whether
      AG-UI ships as an adapter in the first `serve` release or waits
      for demand.
- [ ] Transport: SSE, chunk framing, content type, compression.
- [ ] Backpressure rule (bound, and what happens when a client stalls).
- [ ] Resume semantics and how they read from `store`.
- [ ] Envelope: `SchemaVersion` placement (serve envelope vs store
      envelope vs both), additive-only policy, apidiff coverage.
- [ ] TS client SDK: in scope, generated, or explicitly not shipped.
- [ ] Acceptance: which tests pin the HTTP wire (round-trip golden
      files like `TestEventJSONRoundTrip`, one per format).
- [ ] Update the open-questions register row and TODO §10 status.

## 10. Sources

- `weft/TODO.md` §10 (line 1099), serve post-v1 entry (line 1244),
  open-questions register (line 1289)
- `weft/events.go` (event union, `Seq`, `Nested`, `Pending`)
- `weft/docs/adr/0004-event-ordering.md` (wire-format amendments,
  2026-09-10 through 2026-09-19)
- `weft/docs/adr/0013-adapter-contract.md` (number reservation note)
- `docs/frameworks/pydantic-ai.md`, `vercel-ai-sdk.md`, `deer-flow.md`,
  `pi.md`, `mastra.md`, `langgraph.md`, `crush.md`
- `AGENTIC-STACK-2026.md` section 9 (protocol layering, adoption
  dates)
- `weft-docs/13-future-vision.md` (wire-format churn risk and its
  mitigations)
