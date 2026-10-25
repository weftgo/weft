// StepDiffTable's error branch (plan E3 leftover): an N-way view built
// from responses whose base runs differ cannot be laid out side by
// side — the table says why in its alert line instead of drawing rows.
import { cleanup, waitFor } from "@testing-library/react"
import { createElement } from "react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { ARRIVAL, COST_FOLLOWS_USAGE, nWayView } from "@/lib/stepdiff"
import type { DiffDoc } from "@/lib/stepdiff"
import { FakeStudio, apiError, golden } from "@/test/fake-studio"
import { renderWithRouter } from "@/test/render"
import { StepCompare, StepDiffTable } from "@/components/studio/step-diff"

afterEach(cleanup)

describe("StepDiffTable", () => {
  it("two responses whose a differs draw the error line, no table", async () => {
    const one = golden<DiffDoc>("diff")
    const two: DiffDoc = {
      ...one,
      a: { ...one.a, run_id: `${one.a.run_id}-other` },
    }
    const view = nWayView([one, two])
    expect(view.error).toBeTruthy()
    const { container } = await renderWithRouter(
      createElement(StepDiffTable, { view })
    )
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
    [
      "a pre-A1 pair (diff-not-recorded)",
      () => golden<DiffDoc>("diff-not-recorded"),
    ],
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
      const costs = () => ({
        fields: { cost_usd: 0.001 * ++n, cost_known: true },
      })
      const { container } = await renderWithRouter(
        createElement(StepDiffTable, { view, costs })
      )
      const td = await waitFor(() => {
        const c = container.querySelector(
          `tr[data-diff-step="${step}"] [data-diff-run="${d.b.run_id}"][data-diff-cell="cost"]`
        )
        expect(c).toBeTruthy()
        return c!
      })
      expect(td.getAttribute("data-diff-state")).toBe("unknown")
      expect(td.getAttribute("title")).toBe(COST_FOLLOWS_USAGE)
      expect(td.textContent).toContain("not comparable")
    })
})

// A8's Done line on the web table (UX-C-04, UX-D-15): an identical
// scripted replay from step 1 reads "kept · same" — step 0 the
// source's, not re-run, step 1 the same (its 0→0 usage shown, never a
// change) — and nothing is marked changed.
describe("StepDiffTable over diff-replay.golden.json", () => {
  it("reads kept · same on every row, no changed marker", async () => {
    const view = nWayView([golden<DiffDoc>("diff-replay")])
    const { container } = await renderWithRouter(
      createElement(StepDiffTable, { view })
    )
    const rows = await waitFor(() => {
      const r = container.querySelectorAll("tr[data-diff-step]")
      expect(r.length).toBe(2)
      return [...r]
    })
    expect(
      rows.map(
        (r) => r.querySelector("[data-diff-row-state]")?.textContent ?? "(none)"
      )
    ).toEqual(["kept", "same"])
    expect(container.querySelector("[data-changed]")).toBeNull()
    expect(container.querySelector("[data-diff-marker]")).toBeNull()
    const usage = rows[1].querySelector(
      '[data-diff-run="pg_replay"][data-diff-cell="usage"]'
    )!
    expect(usage.getAttribute("data-diff-state")).toBe("differs")
    expect(usage.textContent).toContain("0→0")
    expect(
      rows[0]
        .querySelector('[data-diff-run="pg_replay"][data-diff-cell="text"]')!
        .getAttribute("data-diff-state")
    ).toBe("kept")
    expect(container.textContent).toContain("pg_replay: no step changed")
  })
})

// UX-C-10: the replay drawer's compare is read right after the command
// finished, before Studio may have the run row: a 404 there is the run
// arriving — retried (ARRIVAL), "recording the run…" meanwhile — and
// past the retries an honest "not recorded yet" with a read-again,
// never an error that never recovers.
describe("StepCompare arriving (the drawer's compare right after a replay)", () => {
  afterEach(() => vi.unstubAllGlobals())
  const serveDiff = (answer: () => unknown) =>
    new FakeStudio()
      .on("GET meta", {
        ...golden<Record<string, unknown>>("meta"),
        capabilities: ["diff"],
      })
      .on("GET diff", answer)
      .install()

  it("a run not ingested yet: retried on 404, then drawn", async () => {
    let n = 0
    serveDiff(() =>
      ++n < 3
        ? apiError(404, "not_found", "no run pg_replay")
        : golden<DiffDoc>("diff-replay")
    )
    const { container } = await renderWithRouter(
      createElement(StepCompare, {
        base: "r_src",
        others: ["pg_replay"],
        arriving: true,
      })
    )
    await waitFor(
      () =>
        expect(
          container.querySelector('[data-step-diff-reading="arriving"]')
            ?.textContent
        ).toContain(ARRIVAL.reading),
      { timeout: 5_000 }
    )
    await waitFor(
      () =>
        expect(
          [...container.querySelectorAll("[data-diff-row-state]")].map(
            (e) => e.textContent
          )
        ).toEqual(["kept", "same"]),
      { timeout: 8_000 }
    )
    expect(n).toBe(3)
    expect(container.querySelector("[data-step-diff-error]")).toBeNull()
  }, 15_000)

  it("still absent past the retries: not recorded yet, with a read-again", async () => {
    let n = 0
    serveDiff(() => {
      n++
      return apiError(404, "not_found", "no run pg_replay")
    })
    const { container } = await renderWithRouter(
      createElement(StepCompare, {
        base: "r_src",
        others: ["pg_replay"],
        arriving: true,
      })
    )
    const notYet = await waitFor(
      () => {
        const e = container.querySelector("[data-step-diff-not-yet]")
        expect(e).toBeTruthy()
        return e!
      },
      { timeout: 10_000 }
    )
    expect(notYet.textContent).toBe(ARRIVAL.notYet("pg_replay"))
    expect(n).toBe(ARRIVAL.retries + 1)
    expect(container.textContent).toContain(ARRIVAL.again)
  }, 20_000)

  it("without arriving a 404 is not retried (a mistyped id on /compare)", async () => {
    let n = 0
    serveDiff(() => {
      n++
      return apiError(404, "not_found", "no run nope")
    })
    const { container } = await renderWithRouter(
      createElement(StepCompare, { base: "r_src", others: ["nope"] })
    )
    await waitFor(() =>
      expect(container.querySelector("[data-step-diff-error]")).toBeTruthy()
    )
    expect(n).toBe(1)
    expect(container.querySelector("[data-step-diff-not-yet]")).toBeNull()
  })
})
