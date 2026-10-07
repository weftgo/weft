// The panel's per-step request line (ADR 0028 §10, plan A1.4): the
// same route the Studio run page reads (parity), through the panel's
// client and token. A read-scoped token never asks for it and shows
// only the hidden badge; a 403 the panel could not foresee renders the
// same badge, silently; a playground-scoped token reads the prompt
// (collapsed), the catalog's names and the params on one line.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { golden } from "../test/fake-studio"
import {
  $,
  all,
  ATTRS,
  baseRoutes,
  fakeStudio,
  json,
  META,
  mount,
  page,
  setup,
  T0,
  teardown,
  trap,
} from "./testkit"

beforeEach(setup)
afterEach(teardown)

const RUN = "s_01-t1"
const REQS = `runs/${RUN}/requests?limit=1000`
const PROMPT0 = "You are a support agent."
const PROMPT1 = "You are a support agent. Refunds need a reason."

const claims = (scope: string) =>
  `weft_pt.${btoa(JSON.stringify({ public_id: "pub_orders", scope, exp: T0 })).replace(/=+$/, "")}.c2ln`

/** The turn as TestRequestsRoutes recorded it: three steps. */
function routes(requests: unknown) {
  const r = baseRoutes()
  const evs: unknown[] = [
    {
      type: "run_start",
      id: RUN,
      model: { provider: "wefttest", name: "script" },
      agent: "acme-support",
    },
  ]
  for (let i = 0; i < 3; i++) {
    evs.push({ type: "step_start", run_id: RUN, index: i })
    evs.push({
      type: "step_finish",
      run_id: RUN,
      index: i,
      reason: i < 2 ? "tool_calls" : "stop",
      usage: { input_tokens: 5, output_tokens: 2 },
    })
  }
  evs.push({
    type: "run_finish",
    run_id: RUN,
    usage: { input_tokens: 15, output_tokens: 6 },
    steps: 3,
  })
  r[`runs/${RUN}/events?after=0&limit=500`] = page(evs)
  r[REQS] = requests
  return r
}

const metaWithRequests = {
  ...META,
  capabilities: [...META.capabilities, "requests"],
}

const hidden403 = () =>
  json(
    {
      error: {
        code: "forbidden",
        message: "a read-scoped panel token does not read it",
      },
      badge: "hidden",
      reason:
        "a read-scoped panel token does not read system prompts or tool catalogs",
      fix: "use a playground-scoped token",
    },
    403
  )

describe("the panel's request line (A1.4)", () => {
  it("a read-scoped token never asks, and shows the hidden badge and nothing else", async () => {
    const studio = fakeStudio(routes(golden("requests-ok")), metaWithRequests)
    const el = await mount({ ...ATTRS, "data-token": claims("read") })
    const lines = all(el, "[data-weft-request]")
    expect(lines.length).toBe(3)
    for (const l of lines)
      expect(l.textContent).toBe("request: hidden by your token scope")
    expect(studio.gets(`runs/${RUN}/requests`)).toEqual([])
    expect(el.shadowRoot!.innerHTML).not.toContain(PROMPT0)
  })

  it("a 403 it could not foresee renders the hidden badge, with no prompt text and no noise", async () => {
    const errors = vi.spyOn(console, "error")
    const warns = vi.spyOn(console, "warn")
    const caught = trap()
    try {
      const studio = fakeStudio(routes(hidden403), metaWithRequests)
      const el = await mount()
      expect(studio.gets(`runs/${RUN}/requests`).length).toBe(1)
      const lines = all(el, "[data-weft-request]")
      expect(lines.length).toBe(3)
      for (const l of lines)
        expect(l.textContent).toBe("request: hidden by your token scope")
      expect(el.shadowRoot!.innerHTML).not.toContain(PROMPT0)
      expect(caught.escaped).toEqual([])
      expect(errors).not.toHaveBeenCalled()
      expect(warns).not.toHaveBeenCalled()
    } finally {
      caught.release()
    }
  })

  it("a playground-scoped token reads the prompt (collapsed), the catalog's names and the params", async () => {
    fakeStudio(routes(golden("requests-ok")), metaWithRequests)
    const el = await mount({ ...ATTRS, "data-token": claims("playground") })
    const line = (step: number) => $(el, `[data-weft-request="${step}"]`)!
    // The prompt: in a collapsed <details>, the text itself.
    const d1 = line(1).querySelector("details")!
    expect(d1.hasAttribute("open")).toBe(false)
    expect(d1.querySelector(".weft-res")?.textContent).toBe(PROMPT1)
    expect(line(0).querySelector("details .weft-res")?.textContent).toBe(
      PROMPT0
    )
    // The catalog's names; the params on one line, every field named.
    expect(line(1).textContent).toContain("tools: lookup_order, refund")
    expect(line(0).textContent).toContain("tools: lookup_order")
    expect(line(1).textContent).toContain(
      "params: temperature adapter default · top_p adapter default · max_tokens adapter default · stop adapter default · seed adapter default"
    )
    // The step a PrepareStep rewrote, and its retry.
    expect(line(1).textContent).toContain("prompt changed at this step")
    expect(line(1).textContent).toContain("catalog changed at this step")
    expect(line(1).textContent).toContain("attempt 1 · attempt 2")
    expect(line(0).textContent).not.toContain("changed at this step")
  })

  it("a pre-A1 run says so on every step; a content-off one shows the hash and the badge", async () => {
    fakeStudio(routes(golden("requests-not-recorded")), metaWithRequests)
    let el = await mount()
    for (const l of all(el, "[data-weft-request]"))
      expect(l.textContent).toBe(
        "request not recorded by weft v0.9.0 or earlier"
      )
    el.remove()
    fakeStudio(routes(golden("requests-stripped")), metaWithRequests)
    el = await mount()
    const l0 = $(el, '[data-weft-request="0"]')!
    expect(l0.textContent).toContain(
      "content not recorded for this destination"
    )
    expect(l0.textContent).toContain("system prompt 57e8f485cbb5")
    expect(l0.querySelector("details")).toBeNull()
  })

  it("without the requests capability, no line and no request", async () => {
    const studio = fakeStudio(routes(golden("requests-ok")))
    const el = await mount()
    expect(all(el, "[data-weft-request]")).toEqual([])
    expect(studio.gets(`runs/${RUN}/requests`)).toEqual([])
  })
})
