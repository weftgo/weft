// The panel's compaction marker (plan A9.2): the run page's words on
// the turn — thread's session marker at the top of the turn it is
// filed under, a PrepareStep's run-scope view on its step's line —
// each with the `compacted` badge and "show original" collapsed by
// default; the view's original is exactly the replaced transcript
// range, read from the turn's growth records. Built on the goldens the
// Go side records from real runs (TestRunCompactionsView, …Session).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { golden } from "../test/fake-studio"
import type { RunDoc } from "../lib/api"
import { $, all, baseRoutes, fakeStudio, mount, page, runRow, setup, teardown } from "./testkit"

beforeEach(setup)
afterEach(teardown)

const RUN = "s_01-t1"

/** The step run's routes under the panel's turn id: its document (no
 * children — the subagent is not what this pins), events and
 * transcript, as the real run recorded them. */
function trimRoutes(doc: Partial<RunDoc> = {}) {
  const r = baseRoutes()
  const recorded = golden<RunDoc>("run-compacted")
  r[`runs/${RUN}`] = { ...runRow({}), children: [], compactions: recorded.compactions, ...doc }
  r[`runs/${RUN}/events?after=0&limit=500`] = page(golden<{ events: unknown[] }>("events-compacted").events)
  r[`runs/${RUN}/transcript`] = golden("transcript-compacted")
  return r
}

describe("the panel's compaction marker (A9.2)", () => {
  it("a PrepareStep trim shows on its step line with the counts, the badge, and show original collapsed", async () => {
    const errors = vi.spyOn(console, "error")
    fakeStudio(trimRoutes())
    const el = await mount()
    const marks = all(el, "[data-weft-compaction]")
    expect(marks.length).toBe(1)
    const m = $(el, '[data-weft-step="2"] [data-weft-compaction="2"]')!
    expect(m).not.toBeNull()
    expect(m.textContent).toContain("2 messages rewritten into 1 by PrepareStep")
    expect(m.querySelector('[data-weft-hole="compacted"]')?.textContent).toBe("compacted")
    const d = m.querySelector("details")!
    expect(d.hasAttribute("open")).toBe(false)
    expect(d.querySelector("summary")?.textContent).toBe("show original")
    // Exactly the replaced range [1, 3): step 0's lookup call and its
    // result — not the prompt before it, not the research after it.
    const orig = Array.from(d.querySelectorAll("[data-weft-original]"))
    expect(orig.map((o) => o.getAttribute("data-weft-original"))).toEqual(["1", "2"])
    expect(orig[0].textContent).toMatch(/^assistant: lookup_order\(/)
    expect(orig[1].textContent).toMatch(/^tool: lookup_order → order 42 shipped/)
    expect(d.textContent).toContain("in their place, 1 message")
    expect(errors).not.toHaveBeenCalled()
  })

  it("thread's session marker sits at the top of the turn with the token line", async () => {
    const r = baseRoutes()
    r[`runs/${RUN}`] = {
      ...runRow({}),
      children: [],
      compactions: golden<RunDoc>("run-session-compacted").compactions,
    }
    fakeStudio(r)
    const el = await mount()
    const m = $(el, '[data-weft-compaction="session"]')!
    expect(m).not.toBeNull()
    // Before the first step card: the top of the turn.
    expect(m.compareDocumentPosition($(el, "[data-weft-step]")!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(m.textContent).toContain("3 messages compacted into 1 · 2.1k → 1.0k tokens")
    expect(m.querySelector('[data-weft-hole="compacted"]')).not.toBeNull()
    expect(m.querySelector("details")?.hasAttribute("open")).toBe(false)
    expect(all(el, "[data-weft-step] [data-weft-compaction]")).toEqual([])
  })

  it("a run from before A9 shows no marker and no error", async () => {
    const errors = vi.spyOn(console, "error")
    fakeStudio(baseRoutes())
    const el = await mount()
    expect(all(el, "[data-weft-step]").length).toBeGreaterThan(0)
    expect(all(el, "[data-weft-compaction]")).toEqual([])
    expect(errors).not.toHaveBeenCalled()
  })

  it("a view whose earlier records are missing shows the gap badge, never an empty original", async () => {
    const r = trimRoutes()
    const t = golden<{ batches: { index: number }[] }>("transcript-compacted")
    r[`runs/${RUN}/transcript`] = { batches: t.batches.filter((b) => b.index !== 1) }
    fakeStudio(r)
    const el = await mount()
    const d = $(el, '[data-weft-compaction="2"] details')!
    expect(d.querySelector('[data-weft-hole="gap"]')).not.toBeNull()
    expect(d.querySelectorAll("[data-weft-original]").length).toBe(0)
    expect(d.textContent).toContain("messages record 1 before the view is missing")
  })
})
