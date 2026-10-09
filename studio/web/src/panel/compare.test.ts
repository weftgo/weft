// The panel's 2-way step compare (plan E3, E3.2): the experiment's run
// against its source, from GET /api/diff (served from the E3.1 goldens,
// studio/testdata/api/diff*.golden.json), drawn through lib/stepdiff.ts.
import { afterEach, beforeEach, describe, expect, it } from "vitest"

import { golden } from "../test/fake-studio"
import { $, all, DIFF_META, fakeStudio, META, mount, runExperiment, setup, stepDiffRoutes, teardown, text } from "./testkit"

beforeEach(setup)
afterEach(teardown)

const SAME = { system: "same", tool_calls: "same", tool_results: "same", text: "same", usage: "same" }

/** cells reads each drawn row's column states, by ordinal. */
function cells(el: Awaited<ReturnType<typeof mount>>): Record<string, Record<string, string>> {
  const out: Record<string, Record<string, string>> = {}
  for (const tr of all(el, "[data-weft-step-diff] tbody tr")) {
    const row: Record<string, string> = {}
    for (const td of Array.from(tr.querySelectorAll("[data-weft-diff-cell]")))
      row[td.getAttribute("data-weft-diff-cell")!] = td.getAttribute("data-state")!
    out[tr.getAttribute("data-weft-diff-step")!] = row
  }
  return out
}

describe("the result pane's step compare", () => {
  it("diff.golden.json: one marker, changed at step 3 on tool results; every other row the same", async () => {
    const studio = fakeStudio(stepDiffRoutes(golden("diff")), DIFF_META)
    const el = await mount()
    await runExperiment(el)
    expect(studio.gets("diff")).toHaveLength(1)
    expect(studio.gets("diff")[0].path).toBe("diff?a=s_01-t1&b=pg_x1")
    const markers = all(el, "[data-weft-diff-marker]")
    expect(markers.map((m) => m.textContent)).toEqual(["changed at step 3 · tool results"])
    expect(markers[0].getAttribute("data-weft-diff-marker")).toBe("3")
    expect(cells(el)).toEqual({
      "0": SAME,
      "1": SAME,
      "2": SAME,
      "3": { ...SAME, tool_results: "changed" },
      "4": SAME,
    })
    const changed = $(el, '[data-weft-diff-step="3"] [data-weft-diff-cell="tool_results"]')!
    expect(changed.textContent).toContain("a order 3 shipped")
    expect(changed.textContent).toContain("b order 3 lost")
    expect(text(el, "[data-weft-step-diff] .weft-diff-h")).toContain("1 of 5 steps changed · first at step 3")
    // The hand-off: Studio's compare page with the same pair.
    const open = $(el, "[data-weft-step-diff] .weft-diff-h a")!.getAttribute("href")!
    expect(new URL(open).pathname).toBe("/studio/compare")
  })

  it("a column a side did not record reads not comparable — never same — with its holes as badges", async () => {
    fakeStudio(stepDiffRoutes(golden("diff-not-recorded")), DIFF_META)
    const el = await mount()
    await runExperiment(el)
    expect(all(el, "[data-weft-diff-marker]")).toHaveLength(0)
    for (const row of Object.values(cells(el))) for (const s of Object.values(row)) expect(s).toBe("unknown")
    expect(text(el, '[data-weft-diff-step="0"] [data-weft-diff-cell="text"]')).toBe("not comparable")
    expect(all(el, '[data-weft-diff-step="0"] [data-weft-diff-side="a"] [data-hole]').map((b) => b.getAttribute("data-hole"))).toEqual([
      "gap",
      "not_recorded",
      "derived",
    ])
  })

  it("a read token's diff: the system prompt hidden, a badge per side", async () => {
    fakeStudio(stepDiffRoutes(golden("diff-hidden")), DIFF_META)
    const el = await mount()
    await runExperiment(el)
    expect(all(el, "[data-weft-diff-marker]").map((m) => m.getAttribute("data-weft-diff-marker"))).toEqual(["3"])
    expect(all(el, '[data-weft-step-diff] [data-hole="hidden"]')).toHaveLength(10)
  })

  it("a truncated response says so with the response_cap badge", async () => {
    const doc = golden<Record<string, unknown>>("diff")
    doc.holes = [{ hole: "truncated", reason: "this response reads a bounded number of steps", fix: "open the later steps" }]
    fakeStudio(stepDiffRoutes(doc), DIFF_META)
    const el = await mount()
    await runExperiment(el)
    expect($(el, '[data-weft-step-diff] > .weft-note [data-hole="truncated"]')).not.toBeNull()
  })

  it("is not asked for, nor drawn, without capability diff", async () => {
    const studio = fakeStudio(stepDiffRoutes(golden("diff")), META)
    const el = await mount()
    await runExperiment(el)
    expect(text(el, ".weft-xres")).toContain("It is delayed until Friday.")
    expect(studio.gets("diff")).toHaveLength(0)
    expect($(el, "[data-weft-step-diff]")).toBeNull()
  })
})
