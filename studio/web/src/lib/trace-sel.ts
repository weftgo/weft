// The trace page's selection keys (plan H7, G1's scheme): a weft span
// is named by its run, step and call — never by a call id alone, which
// repeats across steps (core/loop.go numbers call_1, call_2, … per
// step) and across runs. Plain TypeScript, no React.
//
//   r:<run>                     the run's invoke_agent span
//   s<step> / c:<step>:<call>   a model call / a tool execution, when
//                               the trace holds one run
//   c:resume:<call>             a resumed (approved) call: run before
//                               step 0's model call, its execute_tool
//                               span the only one carrying
//                               weft.tool.approved — the run page's key
//   s:<run>:<step>              the same, run-qualified, when the trace
//   c:<run>:<step>:<call>       holds several (the run page's child form;
//   c:<run>:resume:<call>       a resumed call's too)
//   t:<span id>                 any other span, and any span whose key
//                               another span took first
//
// A pre-H7 link may carry ?span=<span id> or a bare c:<call id>: both
// still resolve (resolveSel).
import type { Span as TimedSpan } from "./api"
import type { Span as TraceRow } from "./trace"

/** The weft run ids the spans carry, deduped and sorted. */
export function traceRuns(spans: readonly TimedSpan[]): string[] {
  const ids = new Set<string>()
  for (const s of spans) {
    const r = s.attrs["weft.run.id"]
    if (typeof r === "string" && r) ids.add(r)
  }
  return [...ids].sort()
}

function stepOf(attrs: Record<string, unknown>): string | undefined {
  const v = attrs["weft.step.index"]
  if (typeof v === "number" && Number.isInteger(v) && v >= 0) return String(v)
  if (typeof v === "string" && /^\d+$/.test(v)) return v
  return undefined
}

/** The key a span would take, before collisions are settled. */
function wantedKey(sp: TimedSpan, qualify: boolean): string | undefined {
  const a = sp.attrs
  const run = typeof a["weft.run.id"] === "string" ? a["weft.run.id"] : ""
  if (!run) return undefined
  const op = a["gen_ai.operation.name"]
  if (op === "invoke_agent") return `r:${run}`
  const step = stepOf(a)
  if (step === undefined) return undefined
  if (op === "chat") return qualify ? `s:${run}:${step}` : `s${step}`
  const call = a["gen_ai.tool.call.id"]
  if (op === "execute_tool" && typeof call === "string" && call) {
    // core/loop.go runs a resumed call through the tool batch at step 0,
    // weft.tool.approved=true on its span alone: the run page keys it
    // "resume" (lib/trace.ts), so a step-0 call of the same id is apart.
    const approved = a["weft.tool.approved"]
    const at = approved === true || approved === "true" ? "resume" : step
    return qualify ? `c:${run}:${at}:${call}` : `c:${at}:${call}`
  }
  return undefined
}

/**
 * keyRows gives each time-axis row (lib/trace's spansFromTimed, whose
 * rows are keyed t:<span id>) its step-qualified key. Keys are unique:
 * a second span wanting a taken key (a retried model call of the same
 * step, say) keeps its t:<span id>. The row's id is unchanged.
 */
export function keyRows(rows: readonly TraceRow[]): TraceRow[] {
  const runs = new Set<string>()
  for (const r of rows) if (r.timed && r.runId) runs.add(r.runId)
  const qualify = runs.size > 1
  const taken = new Set<string>()
  return rows.map((r) => {
    const want = r.timed ? wantedKey(r.timed.span, qualify) : undefined
    if (!want || taken.has(want)) return r
    taken.add(want)
    return { ...r, key: want }
  })
}

export interface Resolved {
  /** The row's id (t:<span id>): what the Waterfall selects by. */
  id?: string
  /** The row's own key: what the page's URL should say. */
  key?: string
  /** An old link's bare call id named more than one span. */
  ambiguous?: boolean
}

/**
 * resolveSel finds the row a ?sel= names: its key, or a span id (t:…,
 * as a pre-H7 ?span= reads); else a bare c:<call id> (a pre-G1 key)
 * names the first tool span of that call id in tree order — ambiguous
 * when the id repeats. A key no row has selects nothing.
 */
export function resolveSel(
  rows: readonly TraceRow[],
  sel: string | undefined
): Resolved {
  if (!sel) return {}
  const exact = rows.find((r) => r.key === sel || r.id === sel)
  if (exact) return { id: exact.id, key: exact.key }
  if (!sel.startsWith("c:")) return {}
  const call = sel.slice(2)
  const calls = rows.filter(
    (r) => r.timed?.span.attrs["gen_ai.tool.call.id"] === call
  )
  const first = calls.at(0)
  if (!first) return {}
  return {
    id: first.id,
    key: first.key,
    ...(calls.length > 1 ? { ambiguous: true } : {}),
  }
}

/** The note an ambiguous old link shows. */
export const OLD_KEY_NOTE =
  "an old link: this call id repeats across steps; showing the first"
