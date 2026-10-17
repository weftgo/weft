// The trace: a run as spans on one axis. The position axis (the
// stream's own order, D6) is what replay scrubs; the time axis is
// what /spans serves — timed spans exist, S4.7 says the trace draws
// on time when they do. The shape is deliberately axis-agnostic: the
// Waterfall draws Span[] over a numeric domain either way, and the
// run page picks (spansFromTimed for time, spansFromFold for
// positions).
import type { Span as TimedSpan } from "./api"
import type { FoldedRun, FoldedStep, FoldedToolCall } from "./events"
import { UNNAMED_AGENT } from "./format"
import { earliestStart, instantMs, spanTree } from "./waterfall"

/** The time axis's domain, lib/waterfall.ts's (one implementation). */
export { timeDomain } from "./waterfall"

/** run/step/tool/subagent: the fold's rows; model, attempt and span:
 * the run tree's (lib/waterfall.ts) — a model call, its attempt, a
 * span weft does not name. */
export type SpanKind =
  "run" | "step" | "tool" | "subagent" | "model" | "attempt" | "span"

export type SpanTone =
  | "step" // neutral boundaries
  | "tool" // a tool call that finished ok
  | "result"
  | "error" // error-as-data (tool is_error)
  | "bad" // the run failed here
  | "running" // still open while the run is live
  | "never" // open on a run that ended: never completed

export interface Span {
  /** Unique within the trace; the tree path. */
  id: string
  /** Short and URL-safe: s0, c:call_1, r:<runId>. What ?sel= carries. */
  key: string
  /** Nesting: rows indent by depth; the parent's id for folding. */
  parent?: string
  depth: number
  kind: SpanKind
  label: string
  /** A quieter second label: reason, size, usage. */
  sub?: string
  /** A short badge: the stop reason, ok, the error code. */
  badge?: string
  tone: SpanTone
  /** Inclusive start position. */
  from: number
  /** Inclusive end position, or null while open (drawn to the edge). */
  to: number | null
  /** What a "select" should land on: a step index or a call id. */
  target?: { step?: number; call?: string }
  /** The folded node behind the span, for the detail panel (absent on
   * the time axis, where rows come from timed spans). */
  node?: FoldedRun | FoldedStep | FoldedToolCall
  /** The timed span behind a time-axis row, for the detail panel. */
  timed?: TimedRow
  /** For steps and calls inside a subagent: which run they belong to. */
  runId: string
  /** The run tree's marks (lib/run-tree.ts): on the critical path; a
   * tool's lane, a step's lane count; a child run row's run id. */
  critical?: boolean
  lane?: number
  lanes?: number
  childRun?: string
  /** A child run row: drawn collapsed (true) or expanded in place
   * (false) — the run page's expand toggles it (U9.2). */
  collapsed?: boolean
  /** A run tree row no span covers (lib/waterfall.ts's untimed): its
   * time is not known — pending its spans, missing, or a call the
   * loop never ran (unrun). */
  untimed?: "pending" | "missing" | "unrun"
}

/** A time-axis row's own data: the span, with from/to in milliseconds
 * since the trace's earliest start. */
export interface TimedRow {
  span: TimedSpan
  fromMs: number
  toMs: number | null
}

function usage(u?: { input_tokens: number; output_tokens: number }): string {
  return u ? `${u.input_tokens} in / ${u.output_tokens} out` : ""
}

function bytesOf(s: string): number {
  return new TextEncoder().encode(s).length
}

/** A ToolError renders "CODE: message" — the code is the badge. */
const CODED = /^([A-Z][A-Z0-9_]+): /

function callSpans(
  call: FoldedToolCall,
  stepIndex: number,
  parent: string,
  depth: number,
  runStatus: string,
  runId: string,
  isChild: boolean,
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
  const coded = call.result?.isError ? CODED.exec(call.result.content) : null
  const badge = open
    ? runStatus === "running"
      ? "running"
      : "never"
    : call.result?.isError
      ? (coded?.[1] ?? "error")
      : "ok"
  const sub = open ? "" : call.result ? `${bytesOf(call.result.content)} B` : ""
  // Call ids repeat across steps (core/loop.go) and, in a resumed
  // run's step 0, beside the resumed call: the key names the step (or
  // "resume", as the child run's id does), so ?sel= and a click land on
  // the call they name.
  const at = call.resumed ? "resume" : String(stepIndex)
  const id = `${parent}/call:${call.resumed ? "resume:" : ""}${call.callId}`
  out.push({
    id,
    key: isChild ? `c:${runId}:${at}:${call.callId}` : `c:${at}:${call.callId}`,
    parent,
    depth,
    kind: "tool",
    label: call.name,
    sub,
    badge,
    tone,
    from: call.startPos,
    to: open ? null : (call.finishPos ?? null),
    target: { call: call.callId },
    node: call,
    runId,
  })
  // A subagent's child run is its own run (S4.3): its span appears
  // only on the time axis (from /spans) or its own page — never folded
  // into the parent's positions.
}

function stepSpans(
  step: FoldedStep,
  parent: string,
  depth: number,
  runStatus: string,
  runId: string,
  isChild: boolean,
  out: Span[]
) {
  const id = `${parent}/step:${step.index}`
  const failedHere = !step.finish && runStatus === "failed"
  out.push({
    id,
    key: isChild ? `s:${runId}:${step.index}` : `s${step.index}`,
    parent,
    depth,
    kind: "step",
    label: `step ${step.index}`,
    sub: step.finish
      ? usage(step.finish.usage)
      : runStatus === "running"
        ? "in flight…"
        : failedHere
          ? "failed here"
          : "no step_finish",
    badge: step.finish?.reason,
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
    node: step,
    runId,
  })
  for (const call of step.toolCalls)
    callSpans(call, step.index, id, depth + 1, runStatus, runId, isChild, out)
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
    key: sub ? `r:${run.runId}` : "run",
    parent,
    depth,
    kind: sub ? "subagent" : "run",
    label: run.agent || UNNAMED_AGENT,
    sub: `${run.steps.length} ${run.steps.length === 1 ? "step" : "steps"}${
      run.usage ? ` · ${usage(run.usage)}` : ""
    }`,
    badge: run.finished
      ? sub
        ? "done"
        : runStatus
      : runStatus === "running"
        ? "running"
        : sub
          ? "unfinished"
          : runStatus,
    tone: run.finished ? "step" : runStatus === "running" ? "running" : "never",
    from: first,
    to: run.finished || runStatus !== "running" ? last : null,
    node: run,
    runId: run.runId,
  })
  for (const step of run.steps)
    stepSpans(step, id, depth + 1, runStatus, run.runId, sub, out)
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

/** One pill per top-level step for the flow strip: the loop at a glance. */
export interface FlowPill {
  key: string
  index: number
  /** The step's gist: its text, or its first tool call. */
  gist: string
  finish?: string
  bad: boolean
  open: boolean
}

export function flowFromFold(folded: FoldedRun, runStatus: string): FlowPill[] {
  return folded.steps.map((s) => {
    const first = s.toolCalls.at(0)
    const gist = s.text
      ? s.text.replace(/\s+/g, " ").trim()
      : first
        ? `${first.name}(${first.args !== undefined ? JSON.stringify(first.args) : "…"})`
        : s.reasoning
          ? "reasoning…"
          : ""
    return {
      key: `s${s.index}`,
      index: s.index,
      gist: gist.length > 60 ? `${gist.slice(0, 59)}…` : gist,
      finish: s.finish?.reason,
      bad: !s.finish && runStatus === "failed",
      open: !s.finish && runStatus === "running",
    }
  })
}

/**
 * spansFromTimed builds the time-axis trace from /spans (S4.7): rows
 * are the timed spans themselves — the invoke_agent span, the chat
 * calls, the tool executions — in lib/waterfall.ts's span tree order
 * (spanTree: a span, then its subtree, siblings by start; a parent
 * cycle cut at the revisit), positioned in milliseconds since the
 * earliest start. The domain is timeDomain(spans); replay does not
 * apply here (nothing to scrub: these are durations), so the playhead
 * is null. The run tree's own rows (run › step › model / tool) are
 * lib/waterfall.ts's waterfallRows; this is its flat span projection
 * for the trace page and the run page's span list.
 */
export function spansFromTimed(timed: TimedSpan[]): Span[] {
  const t0 = earliestStart(timed)
  return spanTree(timed).map(({ span: sp, parent, depth }) => {
    const a = instantMs(sp.start)
    const b = instantMs(sp.end)
    const fromMs =
      Number.isFinite(a) && Number.isFinite(t0) ? Math.max(0, a - t0) : 0
    const toMs = Number.isFinite(b) && Number.isFinite(t0) ? b - t0 : null
    const runID =
      typeof sp.attrs["weft.run.id"] === "string" ? sp.attrs["weft.run.id"] : ""
    // The core names a span "<operation> <name>" ("invoke_agent
    // planner"): the operation attribute says what it is, the name's
    // first word is the fallback for spans that carry none.
    const op =
      typeof sp.attrs["gen_ai.operation.name"] === "string"
        ? sp.attrs["gen_ai.operation.name"]
        : sp.name.split(" ")[0]
    return {
      id: `t:${sp.span_id}`,
      key: `t:${sp.span_id}`,
      parent: parent !== undefined ? `t:${parent}` : undefined,
      depth,
      kind: op === "invoke_agent" ? "run" : "tool",
      label: sp.name,
      sub: sp.service || undefined,
      badge:
        sp.status === "error" ? "error" : sp.status === "ok" ? "ok" : undefined,
      tone:
        sp.status === "error" ? "bad" : sp.status === "ok" ? "tool" : "step",
      from: fromMs,
      to: toMs,
      timed: { span: sp, fromMs, toMs },
      runId: runID,
    }
  })
}

/** Whether a span is a GenAI semconv span (the chat view's rows). */
export function isGenAISpan(sp: TimedSpan): boolean {
  return typeof sp.attrs["gen_ai.operation.name"] === "string"
}
