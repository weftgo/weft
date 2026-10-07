// The panel's step line carries the step's attempts and timing (plan
// A4.2, parity with the run page): "attempt 4 of 4 · fallback to glm-b"
// from the request rows the Request line already reads, "1.2 s · ttft
// 180 ms" from the folded step_finish — no new fetch; a run from before
// A4 (no step_finish latency, no attempt rows) shows the not_recorded
// badge from the shared table.
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import type { RequestRow, StepDoc } from "../lib/api"
import { golden } from "../test/fake-studio"
import { $, baseRoutes, fakeStudio, META, mount, page, setup, teardown } from "./testkit"

beforeEach(setup)
afterEach(teardown)

const RUN = "s_01-t1"

function routes(timing: Record<string, number>, requests: unknown) {
  const r = baseRoutes()
  r[`runs/${RUN}/events?after=0&limit=500`] = page([
    { type: "run_start", id: RUN, model: { provider: "wefttest", name: "glm-a" }, agent: "acme-support" },
    { type: "step_start", run_id: RUN, index: 0 },
    {
      type: "step_finish",
      run_id: RUN,
      index: 0,
      reason: "stop",
      usage: { input_tokens: 10, output_tokens: 5 },
      ...timing,
    },
    { type: "run_finish", run_id: RUN, usage: { input_tokens: 10, output_tokens: 5 }, steps: 1 },
  ])
  r[`runs/${RUN}/requests?limit=1000`] = requests
  return r
}

/** step-0's attempts as the request record holds them: one row per
 * attempt, each naming its own model (glm-a, glm-b, glm-a, glm-b). */
function retriedRows() {
  const step = golden<StepDoc>("step-0")
  const first = step.request as RequestRow
  return {
    requests: step.attempts.map((a) => ({
      ...first,
      index: a.request_index ?? 0,
      attempt: a.attempt,
      body: { ...first.body, attempt: a.attempt, model: { provider: a.provider ?? "", name: a.model } },
    })),
  }
}

const metaWithRequests = { ...META, capabilities: [...META.capabilities, "requests"] }

describe("the panel's step line (A4.2)", () => {
  it("a retried step reads attempt 4 of 4 · fallback to glm-b and its latency and TTFT", async () => {
    const studio = fakeStudio(routes({ latency_ms: 1200, ttft_ms: 180 }, retriedRows()), metaWithRequests)
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
    const rows = retriedRows()
    fakeStudio(routes({ latency_ms: 40 }, { requests: rows.requests.slice(0, 1) }), metaWithRequests)
    const el = await mount()
    const head = $(el, '[data-weft-step="0"] .weft-step-h')!
    expect(head.querySelector("[data-weft-attempts]")).toBeNull()
    expect(head.querySelector("[data-weft-timing]")?.textContent).toBe("40 ms")
  })
})
