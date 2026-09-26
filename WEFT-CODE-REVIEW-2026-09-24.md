
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
