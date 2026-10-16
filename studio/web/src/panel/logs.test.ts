// U11.2's panel half (audit §6.9, UX-E-06, UX-S2-03): each of the
// turn's own Story steps hands its app log lines off to Studio — "📄 ⤢"
// on the step's head, Studio's run page with the step's row selected
// and its drawer's Logs tab open (lib/links.ts's logsLink) — one click
// from the step. The panel reads no log line itself: no logs request.
// Without capability "logs" no link. The run is shaped like
// examples/studio-local's: two steps, mw.Log's lines under each step's
// chat span on the Studio side.
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import {
  $,
  all,
  baseRoutes,
  fakeStudio,
  META,
  mount,
  page,
  setup,
  teardown,
} from "./testkit"

beforeEach(setup)
afterEach(teardown)

const RUN = "s_01-t1"

function routes() {
  const r = baseRoutes()
  const evs: unknown[] = [
    {
      type: "run_start",
      id: RUN,
      model: { provider: "wefttest", name: "echo" },
      agent: "acme-support",
    },
  ]
  for (let i = 0; i < 2; i++) {
    evs.push({ type: "step_start", run_id: RUN, index: i })
    evs.push({
      type: "step_finish",
      run_id: RUN,
      index: i,
      reason: i === 0 ? "tool_calls" : "stop",
      usage: { input_tokens: 10, output_tokens: 5 },
    })
  }
  evs.push({
    type: "run_finish",
    run_id: RUN,
    usage: { input_tokens: 20, output_tokens: 10 },
    steps: 2,
  })
  r[`runs/${RUN}/events?after=0&limit=500`] = page(evs)
  return r
}

const LOGS_META = { ...META, capabilities: [...META.capabilities, "logs"] }

describe("the panel's logs hand-off (U11.2)", () => {
  it("each step's head links to Studio's drawer Logs tab on that step", async () => {
    const studio = fakeStudio(routes(), LOGS_META)
    const el = await mount()
    for (const n of [0, 1]) {
      const a = $(el, `[data-weft-step="${n}"] .weft-step-h [data-weft-logs]`)
      expect(a, `step ${n}`).toBeTruthy()
      const link = a as HTMLAnchorElement
      expect(link.textContent).toBe("📄 ⤢")
      const u = new URL(link.href)
      expect(u.pathname).toBe(`/studio/runs/${RUN}`)
      expect(u.searchParams.get("sel")).toBe(`s${n}`)
      expect(u.searchParams.get("tab")).toBe("logs")
      expect(u.searchParams.get("token")).toBeNull()
      expect(link.target).toBe("_blank")
    }
    // A hand-off only: the panel never reads the lines.
    expect(studio.calls.filter((c) => c.path.includes("/logs"))).toEqual([])
  })

  it("no link without capability logs", async () => {
    fakeStudio(routes(), META)
    const el = await mount()
    expect($(el, '[data-weft-step="0"]')).toBeTruthy()
    expect(all(el, "[data-weft-logs]")).toEqual([])
  })
})
