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
  runRow,
  setup,
  teardown,
  trap,
} from "./testkit"
import { badgeLabel } from "./badges"
import { REQUEST_NO_RECORD_REASON } from "../lib/requests"

beforeEach(setup)
afterEach(teardown)

const RUN = "s_01-t1"
const REQS = `runs/${RUN}/requests?limit=1000`
const PROMPT0 = "You are a support agent."
const PROMPT1 = "You are a support agent. Refunds need a reason."

const claims = (scope: string) =>
  `weft_pt.${btoa(JSON.stringify({ public_id: "pub_orders", scope, exp: "2099-01-01T00:00:00Z" })).replace(/=+$/, "")}.c2ln`

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
    for (const l of lines) {
      expect(l.querySelector(".weft-badge")?.textContent).toBe(
        "request: hidden by your token scope"
      )
      // The hole says why and what to do, with nothing asked.
      expect(l.querySelector(".weft-reason")?.textContent).toBe(
        "your token's scope may not read this: a read-scoped panel token does not read system prompts or tool catalogs — fix: use a playground-scoped token"
      )
    }
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
      for (const l of lines) {
        expect(l.querySelector(".weft-badge")?.textContent).toBe(
          "request: hidden by your token scope"
        )
        expect(l.querySelector(".weft-reason")?.textContent).toContain(
          "fix: use a playground-scoped token"
        )
      }
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
    for (const l of all(el, "[data-weft-request]")) {
      expect(l.querySelector(".weft-badge")?.textContent).toBe(
        "request not recorded by weft v0.9.0 or earlier"
      )
      expect(l.querySelector(".weft-reason")?.textContent).toContain(
        "fix: upgrade weft and re-run"
      )
    }
    el.remove()
    fakeStudio(routes(golden("requests-stripped")), metaWithRequests)
    el = await mount()
    const l0 = $(el, '[data-weft-request="0"]')!
    expect(l0.textContent).toContain(
      "content not captured by this app"
    )
    expect(l0.textContent).toContain("system prompt 57e8f485cbb5")
    expect(l0.querySelector(".weft-reason")?.textContent).toContain(
      "fix: turn content on"
    )
    expect(l0.querySelector("details")).toBeNull()
  })

  it("a 403 without the hidden badge is an error, not the hidden hole", async () => {
    const other = () =>
      json(
        {
          error: {
            code: "forbidden",
            message: "token is scoped to public id pub_other",
          },
        },
        403
      )
    const errors = vi.spyOn(console, "error")
    const warns = vi.spyOn(console, "warn")
    const caught = trap()
    try {
      fakeStudio(routes(other), metaWithRequests)
      const el = await mount({ ...ATTRS, "data-token": claims("playground") })
      const l0 = $(el, '[data-weft-request="0"]')!
      expect(l0.textContent).toBe(
        "request could not be read: token is scoped to public id pub_other"
      )
      expect(el.shadowRoot!.innerHTML).not.toContain(
        "hidden by your token scope"
      )
      // Words in the view, nothing into the host page.
      expect(caught.escaped).toEqual([])
      expect(errors).not.toHaveBeenCalled()
      expect(warns).not.toHaveBeenCalled()
    } finally {
      caught.release()
    }
  })

  it("a running turn's step without a row reads 'not stored yet', not a gap", async () => {
    const r = routes({
      requests: golden<{ requests: unknown[] }>("requests-ok").requests.slice(
        0,
        1
      ),
    })
    const running = runRow({ status: "running", finished: null })
    r["runs?public_id=pub_orders&limit=50"] = {
      total: 1,
      runs: [running],
      next_before: null,
    }
    r[`runs/${RUN}`] = { ...running, children: [] }
    fakeStudio(r, metaWithRequests)
    const el = await mount()
    expect($(el, '[data-weft-request="0"]')!.textContent).toContain("attempt 1")
    expect($(el, '[data-weft-request="2"]')!.textContent).toBe(
      "request: not stored yet — the run is still running"
    )
  })

  it("the page cap reads truncated, never a gap; a cursor that does not move ends the walk", async () => {
    const one = golden<{ requests: Record<string, unknown>[] }>("requests-ok")
      .requests[0]
    const r = routes(undefined)
    delete r[REQS]
    for (let i = 0; i < 12; i++)
      r[`runs/${RUN}/requests?limit=1000${i ? `&from=${i}` : ""}`] = {
        requests: [{ ...one, index: i, attempt: i + 1 }],
        next_from: i + 1,
      }
    let studio = fakeStudio(r, metaWithRequests)
    let el = await mount()
    expect(studio.gets(`runs/${RUN}/requests`).length).toBe(10)
    expect(badgeLabel($(el, '[data-weft-request="2"] [data-hole="truncated"]'))).toBe(
      "request: truncated — first 10 000 requests"
    )
    el.remove()

    const stuck = routes({ requests: [one], next_from: 0 })
    studio = fakeStudio(stuck, metaWithRequests)
    el = await mount()
    expect(studio.gets(`runs/${RUN}/requests`).length).toBe(1)
    // A finished step no row names is the run page's gap (D5 review),
    // with the shared reason.
    const gap = $(el, '[data-weft-request="2"] [data-hole="gap"]')
    expect(badgeLabel(gap)).toBe("request: gap")
    expect($(el, '[data-weft-request="2"]')!.textContent).toContain(REQUEST_NO_RECORD_REASON)
  })

  it("without the requests capability, no line and no request", async () => {
    const studio = fakeStudio(routes(golden("requests-ok")))
    const el = await mount()
    expect(all(el, "[data-weft-request]")).toEqual([])
    expect(studio.gets(`runs/${RUN}/requests`)).toEqual([])
  })
})
