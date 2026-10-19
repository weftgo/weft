// The calibrated budget (src/test/budget.ts): the baseline is a fixed,
// deterministic workload, and the check is the ratio plus a ceiling.
import { describe, expect, it } from "vitest"

import {
  baseline,
  BASELINE_MS,
  loadFactor,
  measure,
  withinBudget,
} from "./budget"

describe("the calibrated budget (U34-fix3)", () => {
  it("the baseline is deterministic work", () => {
    expect(baseline()).toBe(baseline())
  })

  it("in isolation the ratio is the spec number; under load both sides scale", () => {
    // A work that is k× the baseline reads ratio ≈ k however loaded
    // the machine: the limit is budgetMs / BASELINE_MS.
    const b = measure(() => {
      baseline()
      baseline()
    }, 50)
    if (process.env.WEFT_BUDGET_LOG)
      process.stderr.write(`budget ${JSON.stringify(b)}\n`)
    expect(b.maxRatio).toBe(50 / BASELINE_MS)
    // Twice the baseline reads above 1 (≈ 2 idle; one loaded run read
    // 4.1 — noise the model's ≈ 1 against a limit of 5 absorbs).
    expect(b.ratio).toBeGreaterThan(1.2)
    expect(withinBudget({ ...b, work: 20, ratio: 2 }).ok).toBe(true)
    // The ceiling fails a work the ratio alone would pass.
    expect(withinBudget({ ...b, work: 300, ratio: 1 }).ok).toBe(false)
    expect(withinBudget({ ...b, ratio: b.maxRatio + 0.01 }).ok).toBe(false)
    // 50 samples a side outlast vitest's 5 s default under load.
  }, 60_000)

  it("loadFactor is never below 1", () => {
    expect(loadFactor()).toBeGreaterThanOrEqual(1)
  })
})
