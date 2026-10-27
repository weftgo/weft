// The compare page's reading aids (U30.2; UX-A-16's remainder), pure
// and Studio-only (the panel's 2-way block draws every row): a
// "changed only" view — the steps where a compared run changed and the
// columns that changed somewhere — and the usage delta of each compared
// run against the base, summed over the steps each side recorded.
// lib/stepdiff.ts's view is read, never re-derived: a row is changed
// exactly when its view says so, a column when one of its cells is.
import { DIFF_COLUMNS } from "./stepdiff"
import type { DiffColumn, NWayView } from "./stepdiff"
import { tokens } from "./format"

/** The toggle's words. */
export const CHANGED_ONLY = {
  label: "changed only",
  title:
    "show only the steps a compared run changed and the columns that changed in any of them; the marks stay (on by default when any step differs)",
  focusHidden: (step: number) =>
    `step ${step}, the step this link named, is the same on every run: hidden under changed only`,
  showAll: "show all steps",
} as const

/** stepsDiffer is "3 of 41 steps differ" (the toggle's count). */
export function stepsDiffer(view: NWayView): string {
  const n = view.rows.filter((r) => r.changed).length
  return `${n} of ${view.rows.length} ${view.rows.length === 1 ? "step differs" : "steps differ"}`
}

/** changedView narrows the view to its changed rows and the columns
 * one of their cells changed in (every column when none did: an empty
 * table says nothing). */
export function changedView(view: NWayView): {
  rows: NWayView["rows"]
  columns: readonly DiffColumn[]
} {
  const rows = view.rows.filter((r) => r.changed)
  const columns = DIFF_COLUMNS.filter((c) =>
    rows.some((r) => r.others.some((o) => o.cells[c] === "changed"))
  )
  return { rows, columns: columns.length ? columns : DIFF_COLUMNS }
}

/** One run's summed usage over the steps its side recorded. */
export interface UsageSum {
  input: number
  output: number
  steps: number
}

/** One compared run's usage against the base: its sum, and the signed
 * differences. */
export interface UsageDelta {
  run_id: string
  sum: UsageSum
  input: number
  output: number
}

/** usageDelta sums each run's usage over the rows its side holds (a
 * kept step — not re-run — counts nothing for the replay) and gives
 * each compared run's difference from the base. */
export function usageDelta(view: NWayView): {
  base: UsageSum
  others: UsageDelta[]
} {
  const add = (
    s: UsageSum,
    u?: { input_tokens?: number; output_tokens?: number }
  ) => {
    if (!u) return
    s.input += u.input_tokens ?? 0
    s.output += u.output_tokens ?? 0
    s.steps++
  }
  const base: UsageSum = { input: 0, output: 0, steps: 0 }
  for (const r of view.rows) add(base, r.base.side?.usage)
  const others = view.others.map((o, i) => {
    const sum: UsageSum = { input: 0, output: 0, steps: 0 }
    for (const r of view.rows) {
      const c = r.others[i]
      if (c.state !== "kept") add(sum, c.side.side?.usage)
    }
    return {
      run_id: o.run_id,
      sum,
      input: sum.input - base.input,
      output: sum.output - base.output,
    }
  })
  return { base, others }
}

/** signed is a token difference with its sign: "+1.2k", "−30", "±0". */
export function signed(n: number): string {
  if (n === 0) return "±0"
  return `${n > 0 ? "+" : "−"}${tokens(Math.abs(n))}`
}

/** usageWords is one run's line: "in 1.2k → out 300", and against the
 * base "(+150 in · −20 out)". */
export function usageWords(s: UsageSum, d?: UsageDelta): string {
  const own = `${tokens(s.input)} in → ${tokens(s.output)} out`
  return d ? `${own} (${signed(d.input)} in · ${signed(d.output)} out)` : own
}

/** The usage line's heading. */
export const USAGE_DELTA = {
  label: "usage",
  title:
    "each run's tokens summed over the steps it recorded (a kept step, not re-run, counts nothing), and each compared run's difference from the base",
} as const
