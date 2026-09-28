// The trace: a folded run as spans on one axis. T1's axis is the
// stream position (events carry no timestamps, D6), so a span is
// "from event #a to event #b" — order, not duration. The shape is
// deliberately axis-agnostic: when T2a spans carry times, the same
// spans render on a time domain and the Waterfall component does not
// change. Subagents fold in with their parent's positions, so one
// trace covers the whole tree and one playhead replays all of it.
import type { FoldedRun, FoldedStep, FoldedToolCall } from "./events"

export type SpanKind = "run" | "step" | "tool" | "subagent"

export type SpanTone =
  | "step" // neutral boundaries
  | "tool" // a tool call that finished ok
  | "result"
  | "error" // error-as-data (tool is_error)
  | "bad" // the run failed here
  | "running" // still open while the run is live
  | "never" // open on a run that ended: never completed

export interface Span {
  id: string
  /** Nesting: rows indent by depth; the parent's id for folding. */
  parent?: string
  depth: number
  kind: SpanKind
  label: string
  /** A quieter second label: reason, size, usage. */
  sub?: string
  tone: SpanTone
  /** Inclusive start position. */
  from: number
  /** Inclusive end position, or null while open (drawn to the edge). */
  to: number | null
  /** What a "select" should land on: a step index or a call id. */
  target?: { step?: number; call?: string }
}

function usage(u?: { input_tokens: number; output_tokens: number }): string {
  return u ? `${u.input_tokens} in / ${u.output_tokens} out` : ""
}

function bytesOf(s: string): number {
  return new TextEncoder().encode(s).length
}

function callSpans(
  call: FoldedToolCall,
  parent: string,
  depth: number,
  runStatus: string,
  out: Span[]
) {
  const open = call.finishPos === undefined
  const tone: SpanTone = open
    ? runStatus === "running"
      ? "running"
      : "never"
    : call.result?.isError
      ? "error"
      : "tool"
  const sub = open
    ? runStatus === "running"
      ? "running…"
      : "never completed"
    : call.result
      ? `${call.result.isError ? "error" : "ok"} · ${bytesOf(call.result.content)} B`
      : ""
  const id = `${parent}/call:${call.callId}`
  out.push({
    id,
    parent,
    depth,
    kind: "tool",
    label: call.name,
    sub,
    tone,
    from: call.startPos,
    to: open ? null : (call.finishPos ?? null),
    target: { call: call.callId },
  })
  if (call.child) runSpans(call.child, id, depth + 1, runStatus, out, true)
}

function stepSpans(
  step: FoldedStep,
  parent: string,
  depth: number,
  runStatus: string,
  out: Span[]
) {
  const id = `${parent}/step:${step.index}`
  const failedHere = !step.finish && runStatus === "failed"
  out.push({
    id,
    parent,
    depth,
    kind: "step",
    label: `step ${step.index}`,
    sub: step.finish
      ? `${step.finish.reason} · ${usage(step.finish.usage)}`
      : runStatus === "running"
        ? "in flight…"
        : failedHere
          ? "failed here"
          : "no step_finish",
    tone: failedHere
      ? "bad"
      : step.finish
        ? "step"
        : runStatus === "running"
          ? "running"
          : "never",
    from: step.from,
    to: step.finish || runStatus !== "running" ? step.to : null,
    target: { step: step.index },
  })
  for (const call of step.toolCalls)
    callSpans(call, id, depth + 1, runStatus, out)
}

function runSpans(
  run: FoldedRun,
  parent: string | undefined,
  depth: number,
  runStatus: string,
  out: Span[],
  sub = false
) {
  const id = parent ? `${parent}/run:${run.runId}` : `run:${run.runId}`
  const first = run.startPos ?? (run.steps.length ? run.steps[0].from : 0)
  const last =
    run.finishPos ??
    (run.steps.length ? Math.max(...run.steps.map((s) => s.to)) : first)
  out.push({
    id,
    parent,
    depth,
    kind: sub ? "subagent" : "run",
    label: sub ? `↳ ${run.agent || "subagent"}` : run.agent || "run",
    sub: run.finished
      ? `${run.steps.length} ${run.steps.length === 1 ? "step" : "steps"} · ${usage(run.usage)}`
      : runStatus === "running"
        ? "running…"
        : "did not finish",
    tone: run.finished ? "step" : runStatus === "running" ? "running" : "never",
    from: first,
    to: run.finished || runStatus !== "running" ? last : null,
  })
  for (const step of run.steps) stepSpans(step, id, depth + 1, runStatus, out)
}

/**
 * spansFromFold flattens a folded run (subagents included) into rows
 * in document order — parent before children, steps in order, calls
 * in order — ready for the Waterfall. total is the stream length,
 * which closes the top-level run's span when run_finish was seen.
 */
export function spansFromFold(
  folded: FoldedRun,
  total: number,
  runStatus: string
): Span[] {
  const out: Span[] = []
  runSpans(folded, undefined, 0, runStatus, out)
  // The top-level run owns the whole stream: from the first event to
  // run_finish (the last) when it finished, else to the edge.
  out[0].from = 0
  if (folded.finished) out[0].to = Math.max(out[0].to ?? 0, total - 1)
  return out
}
