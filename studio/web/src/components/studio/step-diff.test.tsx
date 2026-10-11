// StepDiffTable's error branch (plan E3 leftover): an N-way view built
// from responses whose base runs differ cannot be laid out side by
// side — the table says why in its alert line instead of drawing rows.
import { cleanup, waitFor } from "@testing-library/react"
import { createElement } from "react"
import { afterEach, describe, expect, it } from "vitest"

import { COST_FOLLOWS_USAGE, nWayView } from "@/lib/stepdiff"
import type { DiffDoc } from "@/lib/stepdiff"
import { golden } from "@/test/fake-studio"
import { renderWithRouter } from "@/test/render"
import { StepDiffTable } from "@/components/studio/step-diff"

afterEach(cleanup)

describe("StepDiffTable", () => {
  it("two responses whose a differs draw the error line, no table", async () => {
    const one = golden<DiffDoc>("diff")
    const two: DiffDoc = { ...one, a: { ...one.a, run_id: `${one.a.run_id}-other` } }
    const view = nWayView([one, two])
    expect(view.error).toBeTruthy()
    const { container } = await renderWithRouter(createElement(StepDiffTable, { view }))
    const err = await waitFor(() => {
      const e = container.querySelector("[data-step-diff-error]")
      expect(e).toBeTruthy()
      return e!
    })
    expect(err.textContent).toBe(view.error)
    expect(err.getAttribute("role")).toBe("alert")
    expect(container.querySelector("[data-step-diff]")).toBeNull()
  })

  // I1.3 fix: the cost column follows the server's usage column. A side
  // whose usage the server did not compare (a pre-A1 run's, a gap's —
  // not only a derived run's) reads cost "not comparable", never
  // "changed", even when both figures were priced.
  for (const [name, doc] of [
    ["a pre-A1 pair (diff-not-recorded)", () => golden<DiffDoc>("diff-not-recorded")],
    [
      "a gap side (usage unknown at step 1)",
      () => {
        const d = golden<DiffDoc>("diff")
        d.steps[1] = { ...d.steps[1], unknown: ["usage"], changes: [] }
        return d
      },
    ],
  ] as const)
    it(`${name}: two priced figures are not comparable when usage is not`, async () => {
      const d = doc()
      const view = nWayView([d])
      const step = d.steps.find((s) => s.unknown.includes("usage"))!.step
      let n = 0
      const costs = () => ({ fields: { cost_usd: 0.001 * ++n, cost_known: true } })
      const { container } = await renderWithRouter(createElement(StepDiffTable, { view, costs }))
      const td = await waitFor(() => {
        const c = container.querySelector(`tr[data-diff-step="${step}"] [data-diff-run="${d.b.run_id}"][data-diff-cell="cost"]`)
        expect(c).toBeTruthy()
        return c!
      })
      expect(td.getAttribute("data-diff-state")).toBe("unknown")
      expect(td.getAttribute("title")).toBe(COST_FOLLOWS_USAGE)
      expect(td.textContent).toContain("not comparable")
    })
})
