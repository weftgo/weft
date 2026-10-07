// The panel's step line carries the step's attempts and timing (plan
// A4.2, parity with the run page): "attempt 4 of 4 · fallback to glm-b"
// from the request rows the Request line already reads, "1.2 s · ttft
// 180 ms" from the folded step_finish — no new fetch; a run from before
// A4 (no step_finish latency, no attempt rows) shows the not_recorded
// badge from the shared table.
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import type { RequestRow } from "../lib/api"
import { golden } from "../test/fake-studio"
import { $, baseRoutes, fakeStudio, META, mount, page, runRow, setup, teardown } from "./testkit"

beforeEach(setup)
afterEach(teardown)

const RUN = "s_01-t1"

/** A turn of `steps` steps, each finished with `timing` — or, with
 * timing null, one step that never finished (the run failed in it). */
function routes(timing: Record<string, number> | null, requests: unknown, steps = 1) {
  const r = baseRoutes()
  const evs: unknown[] = [{ type: "run_start", id: RUN, model: { provider: "wefttest", name: "glm-a" }, agent: "acme-support" }]
  for (let i = 0; i < steps; i++) {
    evs.push({ type: "step_start", run_id: RUN, index: i })
    if (timing)
      evs.push({
        type: "step_finish",
        run_id: RUN,
        index: i,
        reason: i < steps - 1 ? "tool_calls" : "stop",
        usage: { input_tokens: 10, output_tokens: 5 },
        ...timing,
      })
  }
  if (timing) evs.push({ type: "run_finish", run_id: RUN, usage: { input_tokens: 10, output_tokens: 5 }, steps })
  r[`runs/${RUN}/events?after=0&limit=500`] = page(evs)
  if (requests !== undefined) r[`runs/${RUN}/requests?limit=1000`] = requests
  return r
}

/** The recorded retry-over-fallback run's request record (A7's
 * scenario, TestRequestsStepsGolden): step 0's four attempts glm-a,
 * glm-b, glm-a, glm-b, then steps 1 and 2 on their first attempt. */
const recorded = () => golden<{ requests: RequestRow[] }>("requests-steps")
const step0Rows = () => ({ requests: recorded().requests.filter((r) => r.step === 0) })

const metaWithRequests = { ...META, capabilities: [...META.capabilities, "requests"] }

describe("the panel's step line (A4.2)", () => {
  it("a retried step reads attempt 4 of 4 · fallback to glm-b and its latency and TTFT", async () => {
    const studio = fakeStudio(routes({ latency_ms: 1200, ttft_ms: 180 }, step0Rows()), metaWithRequests)
    const el = await mount()
    const head = $(el, '[data-weft-step="0"] .weft-step-h')!
    expect(head.querySelector("[data-weft-attempts]")?.textContent).toBe("attempt 4 of 4 · fallback to glm-b")
    expect(head.querySelector("[data-weft-timing]")?.textContent).toBe("1.2 s · ttft 180 ms")
    expect(head.querySelector('[data-weft-hole="not_recorded"]')).toBeNull()
    // No fetch of its own: the step route is never asked.
    expect(studio.gets(`runs/${RUN}/steps/0`)).toEqual([])
  })

  it("a pre-A4 run shows the not_recorded badge from the shared table and no timing", async () => {
    fakeStudio(routes({}, golden("requests-not-recorded")), metaWithRequests)
    const el = await mount()
    const head = $(el, '[data-weft-step="0"] .weft-step-h')!
    const badge = head.querySelector<HTMLElement>('[data-weft-hole="not_recorded"]')
    expect(badge?.textContent).toBe("not recorded")
    expect(head.querySelector("[data-weft-timing]")).toBeNull()
    expect(head.querySelector("[data-weft-attempts]")).toBeNull()
    // The pre-A1 run's own not_recorded and the attempts' are one badge.
    expect(head.querySelectorAll('[data-weft-hole="not_recorded"]').length).toBe(1)
    expect(badge?.title).toContain("fix: upgrade weft and re-run")
  })

  it("a first-attempt step shows its timing and no attempt line", async () => {
    fakeStudio(routes({ latency_ms: 40 }, { requests: step0Rows().requests.slice(0, 1) }), metaWithRequests)
    const el = await mount()
    const head = $(el, '[data-weft-step="0"] .weft-step-h')!
    expect(head.querySelector("[data-weft-attempts]")).toBeNull()
    expect(head.querySelector("[data-weft-timing]")?.textContent).toBe("40 ms")
  })

  it("a step the run failed in says its attempts and that none answered", async () => {
    const r = routes(null, step0Rows())
    // The turn's run row reads failed (the stream simply ends).
    r["runs?public_id=pub_orders&limit=50"] = { total: 1, runs: [runRow({ status: "failed" })], next_before: null }
    r[`runs/${RUN}`] = { ...runRow({ status: "failed" }), children: [] }
    fakeStudio(r, metaWithRequests)
    const el = await mount()
    const head = $(el, '[data-weft-step="0"] .weft-step-h')!
    expect(head.querySelector("[data-weft-attempts]")?.textContent).toBe("4 attempts · none answered")
  })

  it("a pre-A4 run badges not_recorded on a Studio without the requests capability too", async () => {
    const studio = fakeStudio(routes({}, undefined))
    const el = await mount()
    const badge = $(el, '[data-weft-step="0"] .weft-step-h [data-weft-hole="not_recorded"]')
    expect(badge?.getAttribute("title")).toContain("its request record no attempt rows")
    expect(studio.gets(`runs/${RUN}/requests`)).toEqual([])
  })

  it("a record cut at the page cap says no total for the step it may cut short", async () => {
    const rows = recorded().requests
    const r = routes({ latency_ms: 40 }, undefined, 2)
    // Ten full pages: step 0's attempts 1 and 2, then step 1's rows —
    // the walk stops at the cap with more to read.
    for (let i = 0; i < 10; i++) {
      const row = i < 2 ? { ...rows[i], index: i } : { ...rows[4], index: i, attempt: i - 1 }
      r[`runs/${RUN}/requests?limit=1000${i ? `&from=${i}` : ""}`] = { requests: [row], next_from: i + 1 }
    }
    fakeStudio(r, metaWithRequests)
    const el = await mount()
    // Step 0 is whole: its line stands.
    expect($(el, '[data-weft-step="0"] [data-weft-attempts]')?.textContent).toBe("attempt 2 of 2 · fallback to glm-b")
    // Step 1 holds the record's last row: it may be short — no line.
    expect($(el, '[data-weft-step="1"] [data-weft-attempts]')).toBeNull()
    expect($(el, '[data-weft-step="1"] [data-weft-timing]')?.textContent).toBe("40 ms")
  })
})
