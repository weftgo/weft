// U30.3's Studio remainders on the run page, each on a shape recorded
// through the real pipeline (studio/cards_test.go's cards golden): an
// exhausted Retry's "gave up" line and classified causes (UX-S2-02),
// the wait hatch before a retried attempt (UX-S2-02), a step's
// parallelism and a Sequential barrier (UX-S2-07), the typed output's
// type (UX-S1-06), the per-param override tag (UX-S5b-03) and the
// parked card's clear-the-breakpoint link (UX-H-18).
import {
  cleanup,
  configure,
  fireEvent,
  waitFor,
  within,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import { BREAKPOINT_CLEAR } from "@/lib/cards"
import { BARRIER } from "@/lib/concurrency"
import { renderApp, stubBrowser } from "@/test/app"
import { shape, studioFor } from "@/test/cards-fixture"
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
  setStudioToken("")
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
  return st
}

const card = (step = 0) =>
  document.querySelector<HTMLElement>(`[data-step="${step}"]`)
const row = (key: string) =>
  document.querySelector<HTMLElement>(`[data-span="${key}"]`)

describe("the Studio remainders, part 2 (U30.3)", () => {
  it("mw.retry-giveup: each attempt names its classified cause, then the line that says why Retry gave up (UX-S2-02)", async () => {
    open("mw.retry-giveup")
    await waitFor(() => expect(card()).not.toBeNull())
    const section = card()!.querySelector<HTMLElement>('[data-attempts="0"]')!
    fireEvent.click(within(section).getByRole("button", { name: /attempts/ }))
    await waitFor(() =>
      expect(section.querySelector("[data-gave-up]")).not.toBeNull()
    )
    expect(
      Array.from(
        section.querySelectorAll("[data-attempt-error]"),
        (n) => n.textContent
      )
    ).toEqual(["rate limited (429)", "rate limited (429)"])
    const gave = section.querySelector("[data-gave-up]")!
    expect(gave.getAttribute("data-gave-up")).toBe("budget")
    expect(gave.textContent).toBe("gave up: retries spent (2 attempts)")
  })

  it("mw.retry: the retried attempt's wait is a hatch before its bar (UX-S2-02)", async () => {
    open("mw.retry", "view=waterfall")
    await waitFor(() => expect(row("s0:a2")).not.toBeNull())
    const hatch = row("s0:a2")!.querySelector<HTMLElement>("[data-wait]")!
    expect(hatch).not.toBeNull()
    expect(Number(hatch.dataset.wait)).toBeGreaterThan(0)
    expect(hatch.title).toMatch(/^waited \S+ \S+ before this attempt$/)
    // A first try waits for nothing.
    expect(row("s0:a1")!.querySelector("[data-wait]")).toBeNull()
  })

  it("conc.parallelism: the step says its parallelism, the Sequential tool its barrier (UX-S2-07)", async () => {
    open("conc.parallelism", "view=waterfall")
    await waitFor(() => expect(row("s0")).not.toBeNull())
    await waitFor(() =>
      expect(row("s0")!.textContent).toContain("parallelism 2")
    )
    const barriers = Array.from(
      document.querySelectorAll("[data-barrier]"),
      (n) => n.closest<HTMLElement>("[data-span]")!.dataset.span
    )
    expect(barriers).toEqual(["c:0:p4"])
    expect(document.querySelector("[data-barrier]")!.textContent).toBe(BARRIER)
    // A step that ran no tool says nothing of the dial.
    expect(row("s1")!.textContent).not.toContain("parallelism")
  })

  it("out.invalid-repaired: the OUTPUT card names the type it decodes into (UX-S1-06)", async () => {
    open("out.invalid-repaired")
    await waitFor(() =>
      expect(document.querySelector("[data-output-type]")).not.toBeNull()
    )
    expect(document.querySelector("[data-output-type]")!.textContent).toBe(
      "studio.verdict"
    )
  })

  it("runtimelab.option-lab: each overridden param carries its override tag (UX-S5b-03)", async () => {
    open("runtimelab.option-lab", "view=story&step=0&pane=request")
    await waitFor(() =>
      expect(
        document.querySelector('[data-param-override="temperature"]')
      ).not.toBeNull()
    )
    const tags = Array.from(
      document.querySelectorAll<HTMLElement>("[data-param-override]"),
      (n) => [n.dataset.paramOverride, n.textContent]
    )
    expect(tags).toEqual(
      expect.arrayContaining([
        ["temperature", "◂ override"],
        ["top_p", "◂ override"],
        ["tool_choice", "◂ override"],
      ])
    )
  })

  it("appr.breakpoint-own: the parked card clears the breakpoint through the runtime's breakpoints PUT (UX-H-18)", async () => {
    const studio = open("appr.breakpoint-own", undefined, (fake) =>
      fake
        .on("GET runtimes", {
          runtimes: [
            {
              id: "rt_1",
              service: shape("appr.breakpoint-own").run.service,
              breakpoints_own: true,
              breakpoints: ["status", "deploy"],
              connected: true,
              agents: [],
            },
          ],
        })
        .on("PUT runtimes/rt_1/breakpoints", { tools: ["deploy"] })
    )
    await waitFor(() =>
      expect(
        document.querySelector('[data-breakpoint-clear="rt_1"]')
      ).not.toBeNull()
    )
    const link = document.querySelector<HTMLElement>(
      '[data-breakpoint-clear="rt_1"]'
    )!
    const tool = "status"
    fireEvent.click(
      within(link).getByRole("button", { name: BREAKPOINT_CLEAR.clear(tool) })
    )
    await waitFor(() =>
      expect(
        document.querySelector('[data-breakpoint-clear="cleared"]')
      ).not.toBeNull()
    )
    const put = studio.calls("PUT runtimes/rt_1/breakpoints")
    expect(put).toHaveLength(1)
    expect(put[0].body).toEqual({ tools: ["deploy"], surface: "run" })
  })

  it("appr.breakpoint-own, a read token: the parked card says why it cannot clear the breakpoint (UX-H-18)", async () => {
    setStudioToken(
      `weft_pt.${btoa(JSON.stringify({ public_id: "pub_1", scope: "read", exp: "2099-01-01T00:00:00Z" })).replace(/=+$/, "")}.sig`
    )
    open("appr.breakpoint-own", undefined, (st) =>
      st.on("GET runtimes", {
        runtimes: [
          {
            id: "rt_1",
            service: shape("appr.breakpoint-own").run.service,
            breakpoints_own: true,
            breakpoints: ["status"],
            connected: true,
            agents: [],
          },
        ],
      })
    )
    await waitFor(() =>
      expect(
        document.querySelector('[data-breakpoint-clear="read-only"]')
          ?.textContent
      ).toBe(BREAKPOINT_CLEAR.readOnly)
    )
  })
})
