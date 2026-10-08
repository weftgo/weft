// The trace: a run as spans on one axis. The position axis (the
// stream's own order, D6) is what replay scrubs; the time axis is
// what /spans serves — timed spans exist, S4.7 says the trace draws
// on time when they do. The shape is deliberately axis-agnostic: the
// Waterfall draws Span[] over a numeric domain either way, and the
// run page picks (spansFromTimed for time, spansFromFold for
// positions).
import type { Span as TimedSpan } from "./api"
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
    label: sub ? run.agent || "subagent" : run.agent || "run",
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

/**
 * Where to start reading: the first thing that went wrong (a tool
 * error, a step the run died in), else the first step, else the run.
 */
export function defaultSelection(spans: Span[]): Span | undefined {
  return (
    spans.find((s) => s.tone === "error" || s.tone === "bad") ??
    spans.find((s) => s.tone === "never" && s.kind === "tool") ??
    spans.find((s) => s.kind === "step") ??
    spans.at(0)
  )
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

/** The earliest parseable start, in epoch ms; NaN when none parses.
 * A loop, not Math.min(...spread): a large trace overflows the
 * argument stack. */
function earliestStart(timed: TimedSpan[]): number {
  let t0 = NaN
  for (const s of timed) {
    const t = Date.parse(s.start)
    if (Number.isFinite(t) && !(t >= t0)) t0 = t
  }
  return t0
}

/**
 * spansFromTimed builds the time-axis trace from /spans (S4.7): rows
 * are the timed spans themselves — the invoke_agent span, the chat
 * calls, the tool executions — nested by parent_span_id, positioned
 * in milliseconds since the earliest start. The domain is
 * timeDomain(spans); replay does not apply here (nothing to scrub:
 * these are durations), so the playhead is null.
 *
 * Rows come out in tree order — a span, then its whole subtree,
 * siblings by start — whatever order the API returned (it sorts by
 * start, which interleaves parallel siblings' children). A span whose
 * parent is not in the set is a root; a parent chain that loops (a
 * span naming itself, two naming each other — ingest stores what it
 * is sent) is cut where the walk would revisit, so every span gets
 * exactly one row and no consumer can loop on `parent`.
 */
export function spansFromTimed(timed: TimedSpan[]): Span[] {
  if (timed.length === 0) return []
  const t0 = earliestStart(timed)
  const startOf = (s: TimedSpan): number => {
    const t = Date.parse(s.start)
    return Number.isFinite(t) && Number.isFinite(t0) ? Math.max(0, t - t0) : 0
  }
  const byID = new Map<string, TimedSpan>()
  for (const s of timed) if (!byID.has(s.span_id)) byID.set(s.span_id, s)
  const kids = new Map<string, TimedSpan[]>()
  const roots: TimedSpan[] = []
  for (const s of timed) {
    const p = s.parent_span_id
    if (p && p !== s.span_id && byID.has(p)) {
      const list = kids.get(p)
      if (list) list.push(s)
      else kids.set(p, [s])
    } else roots.push(s)
  }
  const byStart = (a: TimedSpan, b: TimedSpan) => startOf(a) - startOf(b)

  const out: Span[] = []
  const placed = new Set<TimedSpan>()
  const row = (sp: TimedSpan, parent: string | undefined, depth: number) => {
    const fromMs = startOf(sp)
    const endMs = Date.parse(sp.end)
    const toMs =
      Number.isFinite(endMs) && Number.isFinite(t0) ? endMs - t0 : null
    const runID = typeof sp.attrs["weft.run.id"] === "string"
      ? (sp.attrs["weft.run.id"])
      : ""
    // The core names a span "<operation> <name>" ("invoke_agent
    // planner"): the operation attribute says what it is, the name's
    // first word is the fallback for spans that carry none.
    const op =
      typeof sp.attrs["gen_ai.operation.name"] === "string"
        ? (sp.attrs["gen_ai.operation.name"])
        : sp.name.split(" ")[0]
    out.push({
      id: `t:${sp.span_id}`,
      key: `t:${sp.span_id}`,
      parent,
      depth,
      kind: op === "invoke_agent" ? "run" : "tool",
      label: sp.name,
      sub: sp.service || undefined,
      badge: sp.status === "error" ? "error" : sp.status === "ok" ? "ok" : undefined,
      tone: sp.status === "error" ? "bad" : sp.status === "ok" ? "tool" : "step",
      from: fromMs,
      to: toMs,
      timed: { span: sp, fromMs, toMs },
      runId: runID,
    })
  }
  // Depth-first without recursion (a deep trace must not overflow the
  // stack): the work list holds a span with the row it hangs under.
  const walk = (start: TimedSpan[]) => {
    const stack: { sp: TimedSpan; parent: string | undefined; depth: number }[] =
      [...start].sort(byStart).reverse().map((sp) => ({ sp, parent: undefined, depth: 0 }))
    for (let item = stack.pop(); item; item = stack.pop()) {
      const { sp, parent, depth } = item
      if (placed.has(sp)) continue
      placed.add(sp)
      row(sp, parent, depth)
      const children = (kids.get(sp.span_id) ?? []).slice().sort(byStart)
      for (let i = children.length - 1; i >= 0; i--)
        stack.push({ sp: children[i], parent: `t:${sp.span_id}`, depth: depth + 1 })
    }
  }
  walk(roots)
  // Whatever no root reaches sits on a parent cycle: each remaining
  // span opens its own tree (the cycle is cut at the revisit).
  for (const sp of timed) if (!placed.has(sp)) walk([sp])
  return out
}

/** The time axis's domain: [0, the latest end] in milliseconds. */
export function timeDomain(timed: TimedSpan[]): [number, number] {
  const t0 = earliestStart(timed)
  if (!Number.isFinite(t0)) return [0, 1]
  let hi = 1
  for (const s of timed) {
    const end = Date.parse(s.end)
    if (Number.isFinite(end) && end - t0 > hi) hi = end - t0
  }
  return [0, hi]
}

/** Whether a span is a GenAI semconv span (the chat view's rows). */
export function isGenAISpan(sp: TimedSpan): boolean {
  return typeof sp.attrs["gen_ai.operation.name"] === "string"
}
