// The trace page's selection keys (plan H7, G1's scheme): a weft span
// is named by its run, step and call — never by a call id alone, which
// repeats across steps (core/loop.go numbers call_1, call_2, … per
// step) and across runs. The keys are the run tree's — lib/waterfall.ts's
// rowKeys (U8.1-fix) — so a ?sel= written here names the same row on
// the run page. The trace's root run (its first invoke_agent span in
// tree order) keys bare, every other run run-qualified. Plain
// TypeScript, no React.
//
//   r:<run>            the run's invoke_agent span
//   s<step>:m          a model call (a second one in the step: :m2)
//   s<step>:a<n>       an attempt of it
//   c:<step>:<call>    a tool execution
//   c:resume:<call>    a resumed (approved) or denied call: executed or
//                      refused at the resume, before step 0's model call
//   s:<run>:<step>:m, c:<run>:<step>:<call>, …   another run's
//   t:<span id>        any other span, and any span whose key another
//                      span took first
//
// Old links still resolve (resolveSel): a pre-H7 ?span=<span id>, a bare
// c:<call id>, and the pre-U8.1-fix forms — a chat span's s<step>, the
// root run's qualified keys, a denied call's c:<step>:<call>.
import type { Span as TimedSpan } from "./api"
import type { Span as TraceRow } from "./trace"
import {
  attemptKey,
  attrsOf,
  int,
  resumedCall,
  rowKeys,
  str,
} from "./waterfall"

/** The weft run ids the spans carry, deduped and sorted. */
export function traceRuns(spans: readonly TimedSpan[]): string[] {
  const ids = new Set<string>()
  for (const s of spans) {
    const r = s.attrs["weft.run.id"]
    if (typeof r === "string" && r) ids.add(r)
  }
  return [...ids].sort()
}

const runOf = (r: TraceRow) => str(attrsOf(r.timed?.span)["weft.run.id"])

/** The trace's root run: the first invoke_agent row's, else the first
 * row's that carries a run id. */
function rootRun(rows: readonly TraceRow[]): string {
  const inv = rows.filter(
    (r) => attrsOf(r.timed?.span)["gen_ai.operation.name"] === "invoke_agent"
  )
  return inv.map(runOf).find(Boolean) ?? rows.map(runOf).find(Boolean) ?? ""
}

/**
 * keyRows gives each time-axis row (lib/trace's spansFromTimed, whose
 * rows are keyed t:<span id>) its rowKeys key. Keys are unique: a second
 * model call of a step takes :m2; any other span wanting a taken key
 * keeps its t:<span id>. The row's id is unchanged.
 */
export function keyRows(rows: readonly TraceRow[]): TraceRow[] {
  const root = rootRun(rows)
  const taken = new Set<string>()
  // A chat span's key, by span id: its attempts' keys extend it.
  const models = new Map<string, string>()
  return rows.map((r) => {
    const sp = r.timed?.span
    const a = attrsOf(sp)
    const run = runOf(r)
    if (!sp || !run) return r
    const k = rowKeys(run, run !== root)
    const op = a["gen_ai.operation.name"]
    const step = int(a["weft.step.index"])
    const call = str(a["gen_ai.tool.call.id"])
    const n = int(a["weft.attempt.index"])
    const model = models.get(sp.parent_span_id)
    let key = k.span(sp.span_id)
    if (op === "invoke_agent") key = `r:${run}`
    else if (op === "chat" && step !== undefined)
      for (let i = 0; taken.has((key = k.model(step, i))); i++);
    else if (op === "attempt" && model && n !== undefined)
      key = attemptKey(model, n)
    else if (op === "execute_tool" && step !== undefined && call)
      key = k.call(resumedCall(a) ? "resume" : step, call)
    if (taken.has(key)) return r
    taken.add(key)
    if (op === "chat") models.set(sp.span_id, key)
    return { ...r, key }
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
 * as a pre-H7 ?span= reads); else an old form of a key as the key it is
 * now (a chat span's s<step> → s<step>:m; the root run's qualified
 * s:<run>:… / c:<run>:… → bare; a denied call's c:<step>:<call> →
 * c:resume:<call>); else a bare c:<call id> (a pre-G1 key) names the
 * first tool span of that call id in tree order — ambiguous when the id
 * repeats. A key no row has selects nothing.
 */
export function resolveSel(
  rows: readonly TraceRow[],
  sel: string | undefined
): Resolved {
  if (!sel) return {}
  const root = rootRun(rows)
  const now =
    root && /^[sc]:/.test(sel) && sel.startsWith(`${sel[0]}:${root}:`)
      ? sel.replace(`:${root}:`, sel[0] === "s" ? "" : ":")
      : sel
  const denied = /^(c:(?:[^:]+:)?)\d+:(.+)$/.exec(now)
  for (const want of [
    sel,
    now,
    /^s(?:\d+|:.+:\d+)$/.test(now) ? `${now}:m` : "",
    denied ? `${denied[1]}resume:${denied[2]}` : "",
  ]) {
    const hit = want ? rows.find((r) => r.key === want || r.id === want) : null
    if (hit) return { id: hit.id, key: hit.key }
  }
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
