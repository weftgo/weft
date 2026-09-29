
## 6. Fix round (2026-09-26, landed as v0.3.1)

All six P2s landed, one commit per review step, each with its pin
test; both mcp reproducions double-checked by temporarily disabling
the fix and watching the new tests fail the old way (the panic and
the 30s wedge). The doc batch and the mechanical P3 edges rode along:
retry-after overflow (falls back to backoff), mcp raw argument bytes,
the governance-example mutex and error-text scrubbing, the replay
sequence pad widened to five digits (ADR 0017 amendment, committed
fixtures renamed). Left for a later round, all P3: google's
ToolUsePromptTokenCount fold, the pre-headers idle-timeout gap in
openai/anthropic, the mcp schema-import skip-and-aggregate (needs a
design decision with ADR 0015 — fail-the-tool-not-the-listing), the
remaining consistency nits (ToolChoice empty-catalog guard on
openai/google, empty-user-message cross-references, mw.Allow(nil)
doc, nil ToolDef option, corpus rows).

Gates at the release commit: build/vet/test/-race green in all six
modules; `WEFT_MODEL_REQUESTS=deny go test ./...` green workspace-
wide; golangci-lint 0 issues; apidiff clean against v0.3.0 with no
allowances; `make fuzz` 10s/target clean. Tags cut and pushed:
root/openai/anthropic/google at v0.3.1, mcp at v0.1.1.

## 7. Review of the fix round (2026-09-26, landed as v0.3.2)

An independent pass over the nine v0.3.1 commits against the diff,
the SDK sources, and the running suites. Every P2 fix verified
correct and complete: §2.1 keys on `.resolved` so Approve/Deny's
ignore-unknown asymmetry survives the hoist; §2.2 pinned on both
adapters with a capture server; §2.3's `> 0` is safe because the core
rejects a negative MaxTokens at `agent.go:655` before any fold; §2.4's
`OutputTokensDetails.ThinkingTokens` exists in anthropic-sdk-go
v1.72.0; §2.5 is complete because `RawTool` panics on nothing but an
empty name and a nil handler; §2.6 is sufficient because the SDK's
`callTool` (go-sdk v1.8.0 `server.go:1005`) forwards the handler
result without schema validation, so valid-JSON-wrong-shape
serializes and cannot wedge. The replay loader keys on the file's
contents, so ADR 0017's "old-pad fixtures still replay" claim holds.

Two findings, both fixed in v0.3.2:

- **P1 — no sub-module tag was ever installable.** `openai`,
  `anthropic`, `google`, `mcp` required the root as
  `v0.0.0-00010101000000-000000000000` through a `replace => ../`
  that consumers ignore. Reproduced from a scratch module:
  `go get github.com/weftgo/weft/anthropic@v0.3.1` → `invalid
  version: unknown revision 000000000000`. Present since v0.1.0
  (checked every tag); the `go.mod` comment said to drop the replace
  "once weftgo/weft is pushed and tagged", which had been true since
  2026-09-11. Blocks THE-END-GOAL's clean-machine criterion outright.
  Fix: each sub-module requires `github.com/weftgo/weft v0.3.2`, no
  replace; `go.work` keeps in-repo dev on the local root. Two-phase
  release (root tag first, sub-modules tidied against it) recorded
  in TODO §1.1. Verified: a clean module with `GOPROXY=direct` gets
  all five modules at their tags and builds a program importing them.
- **P3 nit — `fitsDuration` bound admitted the exact boundary**, where
  `float64(MaxInt64)` rounds up to 2^63 and the `int64` conversion is
  implementation-defined. Made strict, pinned.

Also noted: the v0.3.1 record commit (`9416d05`) was tagged as
pushed but `main` was one ahead of origin — carried by the v0.3.2
push. Gates at both v0.3.2 commits: build/vet/test/-race green in all
six modules, deny gate green, golangci-lint 0 issues, apidiff clean
against v0.3.1. Tags cut and pushed: root/openai/anthropic/google at
v0.3.2, mcp at v0.1.2.

## 8. The deferred P3s (2026-09-26, landed as v0.3.3)

Every §3 item the v0.3.1 round left open, landed — except the mcp
schema-import skip-and-aggregate: ADR 0015 decides the opposite ("a
schema that cannot parse fails the whole import naming the tool"), so
that one waits for an ADR, not a patch. What landed, each pinned and
each pin mutation-checked (fails with the fix reverted):

- google folds `toolUsePromptTokenCount` into InputTokens (9+3 on the
  tool round-trip fixture).
- The idle timer covers the pre-headers stall in openai and anthropic:
  the request opens on the reader goroutine, as google's did. New
  conformance case `idle_timeout_before_headers` (SilentServer), green
  on all three — the openai and anthropic pins fail on the old code.
- ToolChoice with no catalog sends nothing on openai and google (the
  anthropic guard ported); empty-user-message rules cross-referenced.
- A nil `*ToolDef` at `New` panics, matching the runtime `ErrNilTool`.
- `Golden -update` logs what it created or rewrote; `mw.Allow(nil)`
  documented; the mcp float64 rounding beyond 2^53 pinned.
- Conformance `provider_error`: a 429 with Retry-After reaches the
  caller with the SDK's error type on the chain (`mw.HTTPStatus` on
  all three; `mw.RetryAfter` where the SDK keeps the response —
  `Caps.ErrorHeaders`, false for genai whose APIError carries no
  headers). Retry's header extraction now runs against real SDK error
  types offline: part of the live debt retired.

**A defect the corpus row found (P2 by the review's own scale):** an
embedded pointer to an unexported struct type (`struct{ *base }`) was
flattened into the schema, but encoding/json can never decode into it
("cannot set embedded pointer to unexported struct type") — every weft
schema decodes into a zero value, so the fields were advertised yet
unreachable, and every argument the model sent came back as
`field "id": expected string, got string`. `Tool`/`Output[T]` now
panic at construction naming the type and the fix; an exported
embedded pointer, `time.Duration` (integer nanoseconds), and nested
maps are pinned working.

Harness note: `SilentServer` must drain the request body before
parking — net/http starts the disconnect-detecting background read
only once the body is consumed, and a handler that writes nothing
otherwise never returns, hanging `Server.Close` (found as a 10-minute
test timeout during this round; the adapter code was never at fault).

Gates: build/vet/-race/lint/deny green in all six modules, fuzz clean,
apidiff additive against v0.3.2 (SilentServer, ErrorServer,
Caps.ErrorHeaders). Tags cut and pushed: root/openai/anthropic/google
at v0.3.3, mcp at v0.1.3; sub-modules require root v0.3.3.

## 9. The open item, decided (2026-09-27, landed as mcp v0.1.4)

The mcp schema-import policy §3 questioned. Surveyed in the research
checkout: Vercel AI SDK, Mastra and pydantic-ai all pass `inputSchema`
through unparsed — a malformed schema is invisible at import and
surfaces as a provider 400 at the model call, failing the whole step.
weft parses on purpose and so knows at import what they learn from a
400; failing the whole import over it held a server's good tools
hostage to one, and silent skipping is what ADR 0015 forbade. Decision
(ADR 0015 amendment 2026-09-27): per-tool isolation with a typed
report — `Tools` returns the importable tools plus an `*ImportError`
naming each skipped tool, `*weft.RunError`'s partial-result shape.
Nil entries and empty names join the report by index; listing
failures stay plain errors. Pinned (a type-array root beside two good
tools — served by swapping the schema after `AddTool`, since the Go
SDK's server refuses a non-object root — and the empty-name case),
both pins failing on the old code. Only `weft/mcp` changed; tagged
`mcp/v0.1.4`.

## 10. Review of mcp v0.1.4 (2026-09-27, landed as mcp v0.1.5)

Two defects in the untrusted-input class, both pinned and both pins
failing on v0.1.4: the empty-name check tested the composed name, so
`{"name": ""}` under `Prefix("gh_")` imported as a tool called `gh_`
calling the remote tool `""` (present since the 0.3.1 fix); and a name
repeated in one listing imported twice, leaving `weft.New` to panic on
the duplicate. The first occurrence now stands and the repeat is
reported; the example client shows the `errors.As` idiom.

## 11. Pass over 0.3.1–0.3.3 and mcp 0.1.4–0.1.5 (2026-09-27, landed as v0.3.4 / mcp v0.1.6)

Every fix in the three rounds re-read against its claim and its pin;
build, vet, and every test green in the workspace and per module; all
tags on the expected commits and pushed; the sub-module tags resolve
outside the workspace (`GOWORK=off`: mcp reports the root at v0.3.3,
anthropic builds from the proxy). The reader-goroutine open is
race-free (the stream field is written before the first handshake and
read only after a chunk or after wait); the fixture rename to five
digits breaks no recording (the replayer matches by hashed request
key, not filename); `fitsDuration` is strict at the boundary (checked
the largest float below the bound for both units — neither wraps).

One P2, a behaviour change the 0.3.3 fix did not document: the
pre-headers wait now under the idle timer contains the SDK's own
transport retries, and the Anthropic SDK honours any `retry-after`
uncapped (the OpenAI SDK any under a minute), two retries by default —
so a long ask that used to be slept out and retried now fails at
`IdleTimeout` as `ErrStreamIdle` with the 429 discarded. Decided: keep
the timer (the stall it closed is real), close the documented gap
instead — `MaxRetries(0)` now forwards 0 so `mw.Retry` can own retries
and honour the ask itself; `IdleTimeout` and `mw.Retry` docs say what
the first gap contains. Pinned (one request under 0, three under the
default). Two P3 doc drifts: the README and `mcp/doc.go` snippets
discarded the `*ImportError` with `_`; "first occurrence stands" is
the first importable one, now stated and pinned.


## 12. Deep review of thread v0.2 (2026-09-29, this branch)

A four-axis pass over the whole `thread-v0.2` diff (38 commits, 84
files) — standards, spec, concurrency/lifecycle, and the
security-sensitive semantics of approvals/signing/grants/compaction —
each finding verified against the code before fixing. The full test
suite was green before and after; every fix below is pinned by a test
that fails on the old code (mutation-checked by stashing the source
and running the pins).

Three P1s, all in the approvals logic:

- **Quorum broke the chain's own decisions.** The chain-decided
  hand-off (turn.go) armed the auto-resume whenever the chain parked
  nothing — without the `pendingLocked` gate the runner's own pickup
  and Decide apply. Under `Quorum(n≥2)`, a grant match (Who `""`, one
  identity) or an Approver's single approval armed a resume that then
  denied the undecided calls as "no decision", discarding the approval
  and burning a model call per retry. The hand-off now arms only when
  every dangling call holds an effective decision; an unmet quorum
  leaves the boundary open for the second identity, exactly like a
  parked one.
- **`Always` on a refusal minted a standing approval.** `Decide` and
  `DecideSigned` lacked the `Kind == approve` gate the Approver path
  has: a `Deny` (or `Resolve`) built with `Always: true` recorded a
  grant for the very thing refused — the identical next call then
  auto-ran. Both doors now mint grants only on approvals (ADR 0021 §4).
- **Repeated call ids inherited earlier occurrences' verdicts.** The
  approval walk scoped decisions by the parked request's RunID, but a
  chain-decided call writes no request: ADR 0007 lets call ids repeat
  across turns, so a re-issued id folded the old occurrence's
  approve+deny into the new one's grant approval and read "DENIED:
  conflicting decisions". The walk now resets a call's request and
  decisions at the message that carries it — occurrences are scoped by
  position, the run-id filter kept as defense.

P2s: an approve beside a resolve now conflicts (the fold's documented
words, both orders — the approve was silently discarded); a deny-grant's
matches count against `MaxUses` (only "approved" audits were counted,
so a bounded standing refusal never expired); `Branch` while a turn
runs fails with `ErrBusy` instead of stranding the run's transcript on
a branch whose context the model never saw (branching off a parked
boundary remains the documented escape hatch — the runner is gone by
then); `Decide`/`DecideSigned`/`Resume`'s expiry denials and every
`appendLocked` write now flush under the Flusher capability — a
decision (its doc's own "durably") and above all a revocation (whose
crash-loss re-activates the grant) no longer live only in the page
cache under `FsyncOnFlush`; the `Estimator` hook moved outside the
session lock — one that calls `Leaf()`/`Pending()` deadlocked the
turn's own persistence, where every other caller hook is deliberately
consulted unlocked.

P3s: `nativeOf` never hashes a Model (an unhashable wrapper panicked
the map; a self-wrapping non-comparable one now terminates under a
depth cap — found live when the first version of the fix itself
looped forever and the pin hung); `jsonl.readHeader` uses
`io.ReadFull` (a short read made a valid header look torn and List
skipped the session); the write-only `grantRef.shared` field is gone.

Docs caught up with the code: `SummarizeLeft`'s "Not in this build"
paragraph and `ErrNotImplemented`'s "returned by SummarizeLeft" (it is
returned by nothing now) corrected; the "stubbed until step 2.2"
comment and the phantom `testdata/approvals` golden reference fixed;
`ArgPrefix` documents its empty-prefix sharp edge; the compaction log
lines carry the `trace` attribute set ADR 0020 §4 promised; ADR 0020
gains a dated amendment recording that the split turn is summarized in
one pass; the README status line catches up to v0.3.6.

Two findings deliberately not changed: arming the resume under
`WithoutCancel` in Decide/Resume (the asymmetry with the queued-send
path is real, but `TestResumeRetryAfterCanceledArm` pins the loud-
failure-and-retry contract on purpose — the entries are durable, so a
dead decider's context costs one retry, and changing it broke the
pin); and the trim record carrying no payload (a reopen without the
trimmer configured resurfaces raw results — a documented consequence
of the re-derive-on-read design; persisting stubs is a format change
that wants its own ADR). `thread.Proceed`/`thread.Cancel` remain
package vars — technically against the "no globals" rule, but they are
value sentinels with unexported fields in the `io.EOF` tradition, and
changing their shape is API churn on a reviewed branch; noted for the
v0.2 release decision. `Preparation.SplitPrefix` stays declared and
nil — speculative until a two-pass split mode exists, but exported API.

Gates after the round: build/vet/test/-race green in all six modules,
golangci-lint 0 issues, `WEFT_MODEL_REQUESTS=deny` green, apidiff-thread
skips (no tag yet, as designed).
