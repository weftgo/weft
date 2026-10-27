// The Live page's tree aids (U30.2; UX-A-18's remainder), pure and
// Studio-only: each parent row's collapsed "+N subagents" counter and
// each row's mini waterfall bar. The rows come in lib/live-rows' tree
// order (rowsOf: a run's children right under it, one level deeper),
// so a row's descendants are the rows after it that sit deeper.
//
//   - A parent's children are drawn while any of them still runs (the
//     live work stays in view) and folded once every one has ended; the
//     reader's own toggle wins either way. A folded parent says how many
//     runs it hides and how many of them failed.
//   - The mini bar places each run on one shared time axis — the
//     earliest start the page holds to the latest end (now, for a run
//     still running) — so a child's bar sits under its parent's span:
//     the shape of a multi-agent turn at a glance (lib/waterfall's
//     instantMs reads the instants; a run carries no per-step times
//     here, so it is one bar per run, live from the stream).
import type { LiveRowView } from "./live-rows"
import { ended } from "./live-rows"
import { instantMs } from "./waterfall"

export const LIVE_TREE = {
  more: (n: number) => `+${n} ${n === 1 ? "subagent" : "subagents"}`,
  failed: (n: number) => `${n} failed`,
  show: "show its child runs",
  hide: "hide its child runs",
  bar: "the run on the page's time axis: the earliest start held to the latest end (now, while a run runs)",
} as const

/** One parent's fold: its descendants, how many still run or failed,
 * and whether they are drawn. */
export interface Fold {
  n: number
  running: number
  failed: number
  open: boolean
}

/** foldLive is the rows to draw — each run under a folded ancestor
 * left out — and each parent's fold, by run id. toggled holds the
 * reader's choices (true open, false folded). */
export function foldLive(
  rows: readonly LiveRowView[],
  toggled: Readonly<Record<string, boolean>>
): { rows: LiveRowView[]; folds: Record<string, Fold> } {
  const folds: Record<string, Fold> = {}
  rows.forEach((v, i) => {
    let n = 0
    let running = 0
    let failed = 0
    for (let j = i + 1; j < rows.length && rows[j].depth > v.depth; j++) {
      n++
      if (!ended(rows[j].run)) running++
      if (rows[j].run.status === "failed") failed++
    }
    if (n)
      folds[v.run.id] = {
        n,
        running,
        failed,
        open: toggled[v.run.id] ?? running > 0,
      }
  })
  const out: LiveRowView[] = []
  let hideBelow = Infinity // the depth a folded ancestor hides past
  for (const v of rows) {
    if (v.depth > hideBelow) continue
    hideBelow = Infinity
    out.push(v)
    const f = folds[v.run.id] as Fold | undefined
    if (f && !f.open) hideBelow = v.depth
  }
  return { rows: out, folds }
}

/** The page's time axis: the earliest start to the latest end (now
 * for a run still running), in epoch ms. */
export function liveWindow(
  rows: readonly LiveRowView[],
  now: number
): [number, number] {
  let lo = Infinity
  let hi = -Infinity
  for (const v of rows) {
    const s = instantMs(v.run.started)
    const e = v.run.finished && ended(v.run) ? instantMs(v.run.finished) : now
    if (Number.isFinite(s)) lo = Math.min(lo, s)
    if (Number.isFinite(e)) hi = Math.max(hi, e)
  }
  return Number.isFinite(lo) && hi > lo ? [lo, hi] : [0, 0]
}

/** miniBar is a run's place on the axis, in percent of its width (at
 * least 1%, so an instant run still shows); undefined when the run's
 * start does not parse or the axis is empty. */
export function miniBar(
  v: LiveRowView,
  [lo, hi]: [number, number],
  now: number
): { left: number; width: number } | undefined {
  const s = instantMs(v.run.started)
  if (!(hi > lo) || !Number.isFinite(s)) return undefined
  const e = v.run.finished && ended(v.run) ? instantMs(v.run.finished) : now
  const left = ((s - lo) / (hi - lo)) * 100
  const width = Math.max(((Math.max(e, s) - s) / (hi - lo)) * 100, 1)
  return {
    left: Math.min(Math.max(left, 0), 99),
    width: Math.min(width, 100 - Math.min(Math.max(left, 0), 99)),
  }
}
