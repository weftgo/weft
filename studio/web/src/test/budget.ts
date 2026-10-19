// A time budget that holds under load (U34-fix3). A spec number like
// "the loop graph's model in 50 ms" is the work's time on an idle
// machine; a wall-clock assertion of it flakes when the suite runs
// three times at once beside `go test` (96 ms read once against 50).
// So the budget is calibrated, not absolute:
//
//   - a fixed baseline workload (baseline(), below: allocation, string
//     keys, a Map, a sort — the model's kind of work) is timed in the
//     same test, its samples interleaved with the work's, each side's
//     best of n taken. Oversubscription slows both alike.
//   - the assertion is work / baseline ≤ budgetMs / BASELINE_MS, where
//     BASELINE_MS is the baseline's standalone time on the machine the
//     spec number was measured on: in isolation it is exactly
//     "work < budgetMs"; under k× load both sides scale by about k and
//     the ratio holds.
//   - and an absolute ceiling (default 5× the budget, 250 ms for 50)
//     still fails a real regression the ratio might hide when the
//     baseline itself reads slow.
//
// BASELINE_MS was measured standalone (WEFT_BUDGET_LOG=1 bunx vitest
// run src/test/budget.test.ts, idle, the machine the spec's 9–18 ms
// model figure was read on): 9.6–10.0 ms best of 25, taken as 10.
// So 50 ms reads as a ratio of 5: the model standalone is ~1–2.

/** The baseline's standalone time, ms (see above). */
export const BASELINE_MS = 10

/** baseline is the fixed reference workload: 20,000 records keyed and
 * sorted, deterministic, its result returned so it is not elided. */
export function baseline(): number {
  const m = new Map<string, { i: number; k: string; v: number[] }>()
  for (let i = 0; i < 20_000; i++) {
    const k = `r_${(i * 7919) % 20_000}/step/${i % 97}`
    m.set(k, { i, k, v: [i, i * 2, i % 13] })
  }
  const rows = Array.from(m.values()).sort((a, b) =>
    a.k < b.k ? -1 : a.k > b.k ? 1 : a.i - b.i
  )
  let acc = 0
  for (const r of rows) acc += r.v[2] + r.k.length
  return acc
}

export interface Budget {
  /** The work's best sample, ms. */
  work: number
  /** The baseline's best sample, ms. */
  base: number
  /** work / base. */
  ratio: number
  /** The ratio the budget allows: budgetMs / BASELINE_MS. */
  maxRatio: number
  /** The absolute ceiling, ms. */
  ceiling: number
}

/** measure times fn and the baseline, interleaved, n samples each
 * after one warm-up of both, and returns the bests with the limits
 * budgetMs implies. Assert with withinBudget. */
export function measure(
  fn: () => unknown,
  budgetMs: number,
  { samples = 25, ceiling = budgetMs * 5 } = {}
): Budget {
  fn()
  baseline()
  let work = Infinity
  let base = Infinity
  for (let i = 0; i < samples; i++) {
    const t0 = performance.now()
    fn()
    const t1 = performance.now()
    baseline()
    const t2 = performance.now()
    work = Math.min(work, t1 - t0)
    base = Math.min(base, t2 - t1)
  }
  return {
    work,
    base,
    ratio: work / base,
    maxRatio: budgetMs / BASELINE_MS,
    ceiling,
  }
}

/** loadFactor is how much slower than standalone the machine runs
 * the baseline now (best of n, never below 1): for a work that cannot
 * be re-sampled (a stateful frame), its spec number times this. */
export function loadFactor(samples = 5): number {
  baseline()
  let base = Infinity
  for (let i = 0; i < samples; i++) {
    const t0 = performance.now()
    baseline()
    base = Math.min(base, performance.now() - t0)
  }
  return Math.max(1, base / BASELINE_MS)
}

/** withinBudget is the calibrated check: the ratio under maxRatio and
 * the work under the ceiling; the message names every figure. */
export function withinBudget(b: Budget): { ok: boolean; why: string } {
  const why = `work ${b.work.toFixed(1)} ms, baseline ${b.base.toFixed(1)} ms: ratio ${b.ratio.toFixed(2)} (≤ ${b.maxRatio.toFixed(2)}), ceiling ${b.ceiling} ms`
  if (process.env.WEFT_BUDGET_LOG) process.stderr.write(`budget: ${why}\n`)
  return { ok: b.ratio <= b.maxRatio && b.work < b.ceiling, why }
}
