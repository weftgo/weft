// A step's concurrency in the run tree (UX-S2-07): the request
// record's Parallelism dial beside the lanes the spans draw —
// "parallelism 2" on the step row of a step that ran tool calls — and
// a Sequential tool's barrier mark on its rows ("▮ sequential", the
// tools record's sequential). Read from the request rows the run page
// already holds; a record written before the dial says nothing.
import type { RequestRow } from "./api"
import { isHoleRef } from "./api"

/** The barrier mark and its hover. */
export const BARRIER = "▮ sequential"
export const BARRIER_HINT =
  "a Sequential tool: it waited for the calls before it and ran alone"

/** parallelismWord is the dial in words, one form wherever it is said:
 * the step row, the Request pane's chip, the Agents card's policy. */
export const parallelismWord = (n: number) => `parallelism ${n}`

/** concurrencyMarks adds the step rows' "parallelism N" (a step with a
 * tool row under it) and marks the tool rows of a Sequential tool
 * (barrier) — the run's own rows (runId), from its request rows' first
 * attempt per step. Rows are returned as given when no row says. */
export function concurrencyMarks<
  T extends {
    key: string
    runId: string
    kind: string
    label: string
    sub?: string
    barrier?: boolean
  },
>(rows: T[], reqs: RequestRow[] | undefined, runId: string): T[] {
  if (!reqs?.length) return rows
  const dial = new Map<number, number>()
  const seq = new Map<number, Set<string>>()
  for (const r of reqs) {
    if (r.attempt > 1) continue
    const n = r.body.parallelism
    if (n && n > 0) dial.set(r.step, n)
    const c = r.tools
    if (c && !isHoleRef(c))
      seq.set(
        r.step,
        new Set(c.tools.filter((t) => t.sequential).map((t) => t.name))
      )
  }
  const withTools = new Set<number>()
  for (const r of rows) {
    const m = /^c:(\d+):/.exec(r.key)
    if (r.runId === runId && r.kind === "tool" && m) withTools.add(Number(m[1]))
  }
  return rows.map((r) => {
    if (r.runId !== runId) return r
    const s = /^s(\d+)$/.exec(r.key)
    if (s && r.kind === "step") {
      const n = dial.get(Number(s[1]))
      if (!n || !withTools.has(Number(s[1]))) return r
      return {
        ...r,
        sub: [r.sub, parallelismWord(n)].filter(Boolean).join(" · "),
      }
    }
    const c = /^c:(\d+):/.exec(r.key)
    if (c && r.kind === "tool" && seq.get(Number(c[1]))?.has(r.label))
      return { ...r, barrier: true }
    return r
  })
}
