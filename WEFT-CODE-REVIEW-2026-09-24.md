
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
