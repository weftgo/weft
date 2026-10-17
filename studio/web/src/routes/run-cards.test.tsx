// U25's Done line on the run page: each named seeded run shape —
// recorded through the real pipeline (studio/cards_test.go) and served
// from its golden — shows its named fact in the story, its header or
// its step's Request pane, in lib/cards.ts's words.
import {
  cleanup,
  configure,
  fireEvent,
  waitFor,
  within,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import { BREAKPOINT, BY_SOURCE, OUTPUT } from "@/lib/cards"
import { NOT_EXECUTED } from "@/lib/summarize"
import { AWAITING } from "@/lib/status"
import { renderApp, stubBrowser } from "@/test/app"
import { shape, studioFor } from "@/test/cards-fixture"
import { pagedEvents } from "@/test/fake-studio"
import { FakeEventSource } from "@/test/fake-event-source"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function open(
  name: string,
  query = "view=story",
  more?: (st: ReturnType<typeof studioFor>) => void
) {
  const st = studioFor(name)
  more?.(st)
  st.install()
  renderApp(`/runs/${shape(name).run.id}?${query}`)
}

const card = (step = 0) =>
  document.querySelector<HTMLElement>(`[data-step="${step}"]`)
const call = (id: string, step = 0) =>
  card(step)?.querySelector<HTMLElement>(`[data-call="${id}"]`) ?? null

describe("the run cards tell the whole truth (U25)", () => {
  it("loop.max-tokens-tools: the cut call drawn with its truncated args and 'not executed'", async () => {
    open("loop.max-tokens-tools")
    await waitFor(() => expect(call("t1")).not.toBeNull())
    const c = call("t1")!
    expect(c.textContent).toContain('{"msg":"never')
    expect(c.textContent).toContain("call never executed")
    expect(c.querySelector('[data-hole="max_tokens"]')).not.toBeNull()
    expect(c.textContent).toContain("args · cut at the output token limit")
  })

  it("out.invalid-repaired: an OUTPUT card with the typed-output mark and the repair summary", async () => {
    open("out.invalid-repaired")
    await waitFor(() =>
      expect(document.querySelector("[data-output-card]")).not.toBeNull()
    )
    const out = document.querySelector<HTMLElement>("[data-output-card]")!
    expect(out.textContent).toContain('"approved": true')
    expect(out.textContent).toContain("typed output (submit_output)")
    expect(out.querySelector("[data-output-repairs]")?.textContent).toBe(
      "repaired after 1 invalid submission"
    )
  })

  it("out.no-output: an Output run that answered in text says no output was submitted", async () => {
    open("out.no-output")
    await waitFor(() =>
      expect(document.querySelector("[data-output-none]")?.textContent).toBe(
        "no output submitted"
      )
    )
  })

  it("mw.retry: each attempt row names its HTTP status, cause and wait", async () => {
    open("mw.retry")
    await waitFor(() => expect(card()).not.toBeNull())
    const section = card()!.querySelector<HTMLElement>('[data-attempts="0"]')!
    fireEvent.click(within(section).getByRole("button", { name: /attempts/ }))
    await waitFor(() =>
      expect(section.querySelectorAll("[data-attempt-cause]").length).toBe(3)
    )
    expect(
      Array.from(
        section.querySelectorAll("[data-attempt-cause]"),
        (n) => n.textContent
      )
    ).toEqual([
      "503 · Service Unavailable: overloaded",
      "503 · Service Unavailable: overloaded · waited 1 ms",
      "waited 1 ms",
    ])
  })

  it("tools.timeout: the tool card names the limit it ran under", async () => {
    open("tools.timeout")
    await waitFor(() =>
      expect(call("s1")?.querySelector("[data-exec-facts]")?.textContent).toBe(
        "0 ms / 50 ms limit"
      )
    )
    expect(call("s1")!.textContent).toContain("timeout")
  })

  it("appr.park: the parked call awaits a decision, never 'never completed'", async () => {
    open("appr.park")
    await waitFor(() =>
      expect(call("d1")?.querySelector("[data-parked-call]")?.textContent).toBe(
        AWAITING
      )
    )
    expect(call("d1")!.textContent).not.toContain("never completed")
    expect(document.querySelector("[data-parked] .eyebrow")?.textContent).toBe(
      "pending approval"
    )
  })

  it.each([
    ["appr.breakpoint-own", "b1", "park_source", true],
    ["appr.breakpoint-playground", "b2", "park_on", false],
  ])(
    "%s: a breakpoint park is distinct from an approval gate",
    async (name, id, source, own) => {
      // A ParkSource park is a breakpoint when the run's service has a
      // runtime that registered breakpoints_own (runtime.Breakpoints()).
      open(name, undefined, (st) =>
        own
          ? st.on("GET runtimes", {
              runtimes: [
                {
                  id: "rt_1",
                  service: shape(name).run.service,
                  breakpoints_own: true,
                  connected: true,
                  agents: [],
                },
              ],
            })
          : undefined
      )
      await waitFor(() =>
        expect(call(id)?.querySelector("[data-parked-call]")?.textContent).toBe(
          BREAKPOINT
        )
      )
      expect(
        call(id)!
          .querySelector("[data-parked-call]")!
          .getAttribute("data-parked-call")
      ).toBe(source)
      expect(
        document.querySelector("[data-parked] .eyebrow")?.textContent
      ).toBe(BREAKPOINT)
    }
  )

  it("appr.breakpoint-own with no breakpoints_own runtime: paused by ParkSource, never a claimed breakpoint", async () => {
    open("appr.breakpoint-own")
    await waitFor(() =>
      expect(call("b1")?.querySelector("[data-parked-call]")?.textContent).toBe(
        BY_SOURCE
      )
    )
  })

  it("out.invalid-repaired, content off: the submitted output reads typed + stripped, never 'no output submitted'", async () => {
    const s = shape("out.invalid-repaired")
    open("out.invalid-repaired", undefined, (st) =>
      st
        .on(`GET runs/${s.run.id}/transcript`, { batches: [] })
        .on(
          `GET runs/${s.run.id}/events`,
          pagedEvents(
            s.events.events.map((e) =>
              (e.event as { type: string }).type === "tool_start"
                ? { ...e, event: { ...(e.event as object), args: null } }
                : e
            )
          )
        )
    )
    await waitFor(() =>
      expect(document.querySelector("[data-output-stripped]")).not.toBeNull()
    )
    const out = document.querySelector<HTMLElement>("[data-output-stripped]")!
    expect(out.textContent).toContain(OUTPUT.typed)
    expect(out.querySelector('[data-hole="stripped"]')).not.toBeNull()
    expect(out.querySelector("[data-output-repairs]")?.textContent).toBe(
      "repaired after 1 invalid submission"
    )
    expect(document.querySelector("[data-output-none]")).toBeNull()
  })

  it("runtimelab.option-lab: 'overridden by experiment' names every overridden knob", async () => {
    open("runtimelab.option-lab", "view=story&step=0&pane=request")
    await waitFor(() =>
      expect(card()?.querySelector('[data-mark="experiment"]')).toBeTruthy()
    )
    const chip = card()!.querySelector<HTMLElement>('[data-mark="experiment"]')!
    for (const k of ["model", "params", "park_on", "tool_choice", "tools"])
      expect(chip.title).toContain(k)
  })

  it("msg.parts: the prompt's file parts as attachments", async () => {
    open("msg.parts")
    await waitFor(() =>
      expect(document.querySelector("[data-attachments]")).not.toBeNull()
    )
    expect(document.querySelector("[data-attachments]")!.textContent).toBe(
      "file text/plain · 14 Bfile image/png · url"
    )
  })

  it("sub.failed: the delegation's failure is a typed outcome", async () => {
    open("sub.failed")
    await waitFor(() =>
      expect(call("w1")?.textContent).toContain("child failed")
    )
    expect(call("w1")!.textContent).toContain("child model outage")
  })

  it("opt.prepare-step: a PrepareStep chip on every changed part, the thinking wire beside the level", async () => {
    open("opt.prepare-step", "view=story&step=0&pane=request")
    await waitFor(() =>
      expect(card()?.querySelector('[data-mark="prepared"]')?.textContent).toBe(
        "PrepareStep: system, tool_choice, params"
      )
    )
    await waitFor(() =>
      expect(card()!.querySelector("[data-thinking-wire]")?.textContent).toBe(
        "low → reasoning_effort"
      )
    )
  })
})

// The run tree's drawer reads the story's run cards (P8-gate-fix): its
// Tool I/O rows and its answer lines carry the same park words, tool
// limits and typed child outcomes — one source (RunCards), never
// "NEVER COMPLETED" / "never finished" for a parked call.
describe("the drawer draws the run cards too (P8-gate-fix)", () => {
  const drawer = () => document.querySelector<HTMLElement>("[data-drawer]")
  const drawerCall = (id: string) =>
    drawer()?.querySelector<HTMLElement>(`[data-call="${id}"]`) ?? null
  const ownRuntime = (name: string) => (st: ReturnType<typeof studioFor>) =>
    st.on("GET runtimes", {
      runtimes: [
        {
          id: "rt_1",
          service: shape(name).run.service,
          breakpoints_own: true,
          connected: true,
          agents: [],
        },
      ],
    })

  it.each([
    ["appr.park", "d1", AWAITING, false],
    ["appr.breakpoint-own", "b1", BY_SOURCE, false],
    ["appr.breakpoint-own", "b1", BREAKPOINT, true],
  ] as const)(
    "%s: the landing step's answer and the call's Tool I/O read %s",
    async (name, id, word, own) => {
      open(name, "tab=tools", own ? ownRuntime(name) : undefined)
      await waitFor(() =>
        expect(
          drawerCall(id)?.querySelector("[data-parked-call]")?.textContent
        ).toBe(word)
      )
      expect(drawer()!.textContent).not.toMatch(/never completed/i)
      const tools = document.querySelector<HTMLElement>(
        '[data-answer-line="tools"]'
      )!
      expect(tools.textContent).toContain(`1 ${AWAITING}`)
      expect(tools.textContent).not.toContain("never finished")
    }
  )

  it("appr.park: the call row's facts say it awaits a decision, never 'finished at never'", async () => {
    open("appr.park", "sel=c:0:d1&tab=tools")
    await waitFor(() =>
      expect(
        drawerCall("d1")?.querySelector("[data-parked-call]")?.textContent
      ).toBe(AWAITING)
    )
    const answer = drawer()!.querySelector<HTMLElement>("[data-drawer-answer]")!
    expect(answer.textContent).toContain(`finished at${AWAITING}`)
    expect(drawer()!.textContent).not.toMatch(/never completed/i)
  })

  it("tools.timeout: the drawer's call row names the limit it ran under", async () => {
    open("tools.timeout", "sel=c:0:s1&tab=tools")
    await waitFor(() =>
      expect(
        drawerCall("s1")?.querySelector("[data-exec-facts]")?.textContent
      ).toBe("0 ms / 50 ms limit")
    )
  })

  it("loop.max-tokens-tools: the run tree draws the call never run 'not executed', no loss hole", async () => {
    open("loop.max-tokens-tools", "")
    await waitFor(() =>
      expect(
        document.querySelector('[data-untimed-note="unrun"]')?.textContent
      ).toBe(NOT_EXECUTED)
    )
    const tree = document.querySelector<HTMLElement>(".waterfall")!
    expect(tree.querySelector('[data-hole="not_recorded"]')).toBeNull()
  })

  it("H9 run@360: stacked, the tree's grid is one minmax(0,1fr) column — never an auto track its widest row grows past the page", async () => {
    open("appr.park", "")
    await waitFor(() =>
      expect(document.querySelector("[data-trace-grid]")).not.toBeNull()
    )
    const grid = document.querySelector<HTMLElement>("[data-trace-grid]")!
    expect(grid.getAttribute("data-trace-grid")).toBe("auto")
    // jsdom lays nothing out: the rule is the class (scripts/ux-gate/
    // h9.js measures it in a browser).
    expect(grid.className.split(" ")).toContain("grid-cols-[minmax(0,1fr)]")
  })

  it("sub.failed: the drawer's call row reads the typed child outcome", async () => {
    open("sub.failed", "sel=c:0:w1&tab=tools")
    await waitFor(() =>
      expect(
        drawerCall("w1")?.querySelector("[data-tool-pill]")?.textContent
      ).toBe("child failed")
    )
  })
})
