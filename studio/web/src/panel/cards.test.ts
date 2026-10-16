// U25's Done line in the panel's Story (and its Request tab, which the
// run page's Request pane mirrors): each named seeded run shape —
// recorded through the real pipeline (studio/cards_test.go), served
// from its golden — shows the named fact in lib/cards.ts's words, the
// words the run page draws (routes/run-cards.test.tsx).
import { afterEach, beforeEach, describe, expect, it } from "vitest"

import { BREAKPOINT, OUTPUT, repairedWords } from "../lib/cards"
import { UNRUN_CALL_REASON } from "../lib/events"
import { AWAITING } from "../lib/status"
import { panelRoutes } from "../test/cards-fixture"
import type { WeftDevtools } from "./element"
import {
  $,
  all,
  click,
  fakeStudio,
  META,
  mount,
  settle,
  setup,
  teardown,
} from "./testkit"

beforeEach(setup)
afterEach(teardown)

const CAPS = {
  ...META,
  capabilities: [...META.capabilities, "requests", "steps"],
}

async function story(name: string): Promise<WeftDevtools> {
  fakeStudio(panelRoutes(name), CAPS)
  const el = await mount()
  await settle()
  return el
}
const call = (el: WeftDevtools, id: string) =>
  $(el, `.weft-call[data-key="${id}"]`) as HTMLElement | null
async function requestTab(el: WeftDevtools) {
  click($(el, "#weft-tab-request"))
  await settle()
}

describe("the Story's cards tell the whole truth (U25)", () => {
  it("loop.max-tokens-tools: the cut call with its truncated args and 'call never executed'", async () => {
    const el = await story("loop.max-tokens-tools")
    const c = call(el, "t1")!
    expect(c.textContent).toContain('({"msg":"never')
    expect(c.querySelector('[data-hole="max_tokens"]')).not.toBeNull()
    expect(c.textContent).toContain(UNRUN_CALL_REASON)
  })

  it("out.invalid-repaired / out.no-output: the typed-output chip and the repair summary", async () => {
    let el = await story("out.invalid-repaired")
    expect($(el, "[data-weft-output]")?.textContent).toBe(
      `${OUTPUT.typed} · ${repairedWords(1)}`
    )
    teardown()
    setup()
    el = await story("out.no-output")
    expect($(el, "[data-weft-output]")?.textContent).toBe(OUTPUT.none)
  })

  it("mw.retry: the attempts picker names each attempt's status, cause and wait", async () => {
    const el = await story("mw.retry")
    await requestTab(el)
    expect(all(el, "[data-weft-rq-attempt]").map((b) => b.textContent)).toEqual(
      [
        "attempt 1 · 503 · Service Unavailable: overloaded",
        "attempt 2 · 503 · Service Unavailable: overloaded · waited 1 ms",
        "attempt 3 · waited 1 ms",
      ]
    )
  })

  it("tools.timeout: the tool card names the limit it ran under", async () => {
    const el = await story("tools.timeout")
    expect(call(el, "s1")!.textContent).toContain("0 ms / 50 ms limit")
  })

  it("appr.park: a parked call awaits a decision, never 'never completed'", async () => {
    const el = await story("appr.park")
    expect(call(el, "d1")!.querySelector(".weft-warn")?.textContent).toBe(
      AWAITING
    )
  })

  it.each([
    ["appr.breakpoint-own", "b1"],
    ["appr.breakpoint-playground", "b2"],
  ])(
    "%s: a breakpoint park is distinct from an approval gate",
    async (name, id) => {
      const el = await story(name)
      expect(call(el, id)!.querySelector(".weft-warn")?.textContent).toBe(
        BREAKPOINT
      )
    }
  )

  it("runtimelab.option-lab: the Request tab's 'overridden by experiment' chip", async () => {
    const el = await story("runtimelab.option-lab")
    await requestTab(el)
    expect($(el, '[data-weft-mark="experiment"]')?.textContent).toBe(
      "overridden by experiment"
    )
  })

  it("msg.parts: the prompt's file parts as attachments", async () => {
    const el = await story("msg.parts")
    expect($(el, "[data-weft-files]")?.textContent).toBe(
      "file text/plain · 14 B · file image/png · url"
    )
  })

  it("sub.failed: the delegation's failure is a typed outcome", async () => {
    const el = await story("sub.failed")
    expect(
      Array.from(
        call(el, "w1")!.querySelectorAll(".weft-err"),
        (b) => b.textContent
      )
    ).toContain("child failed")
  })

  it("opt.prepare-step: the PrepareStep chip and the thinking wire", async () => {
    const el = await story("opt.prepare-step")
    await requestTab(el)
    expect($(el, '[data-weft-mark="prepared"]')?.textContent).toBe(
      "PrepareStep: system, tool_choice, params"
    )
    expect($(el, '[data-weft-rq="thinking"]')?.textContent).toContain(
      "low → reasoning_effort"
    )
  })
})
