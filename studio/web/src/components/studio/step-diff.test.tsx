// StepDiffTable's error branch (plan E3 leftover): an N-way view built
// from responses whose base runs differ cannot be laid out side by
// side — the table says why in its alert line instead of drawing rows.
import { cleanup, waitFor } from "@testing-library/react"
import { createElement } from "react"
import { afterEach, describe, expect, it } from "vitest"

import { nWayView } from "@/lib/stepdiff"
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
})
