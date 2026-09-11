# ADR 0008 — Structured output

- Status: decided (2026-09-12, TODO §6)

## Context

The most-requested feature when porting from Pydantic AI (`result_type`)
or the AI SDK (`generateObject`) is a final answer constrained to a
type. Providers differ: some have a native JSON-schema response mode,
some do not, and the ones that do disagree on the details. Weft's core
must work on every adapter without special cases.

## Decision

**Structured output is a tool.** `weft.Output[Out]()` is an `Option`
that registers a tool named `submit_output` whose input schema is
reflected from `Out` exactly as `Tool` reflects a handler's input, and
adds the stop condition `output_submitted`: the run ends after the step
in which a `submit_output` call produced a non-error result.

- `weft.GenerateAs[Out](ctx, agent, opts...) (Out, *RunResult, error)`
  runs the agent and decodes the last valid `submit_output` call's
  arguments.
- `weft.OutputOf[Out](*RunResult) (Out, error)` does the decode alone,
  for callers that streamed the run.
- A run that ends without a valid submission returns `ErrNoOutput`, with
  the `RunResult` still returned so the transcript is inspectable.
- Invalid arguments are `ErrInvalidToolInput` tool results naming the
  field (ADR 0003 amendment), so repair is the ordinary tool-error loop:
  the model sees the error and calls again. This is why the stop
  condition checks for a *non-error* result rather than `HasToolCall`.
- `Out` must be a struct (or pointer to one), for the reason `Tool`
  requires it: providers require an object schema at the top level.

The tool's description — "Submit your final answer. Call this exactly
once, when you are done: the run ends with it." — and the result text
"recorded" are model-visible contract, pinned by tests.

## Consequences

- Works on every provider today; no adapter changes.
- The manifest shows the output schema as a tool and the stop condition
  by name, so a `weft.json` diff reviews output-type changes.
- Adapters with native JSON-schema mode may later carry
  `ModelRequest.OutputSchema` as an optimisation behind the same API.
- The tool name `submit_output` is reserved; registering another tool
  by that name panics as any duplicate does.
