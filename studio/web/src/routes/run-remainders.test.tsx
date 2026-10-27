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
import { BREAKPOINT, BREAKPOINT_CLEAR } from "@/lib/cards"
import { BARRIER } from "@/lib/concurrency"
import { renderApp, stubBrowser } from "@/test/app"
import { shape, studioFor } from "@/test/cards-fixture"
import { FakeEventSource } from "@/test/fake-event-source"
import { golden, json } from "@/test/fake-studio"

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

  it("runtimelab.option-lab: each tag names the agent's configured value from the run's stored manifest (UX-S5b-03)", async () => {
    const run = shape("runtimelab.option-lab").run
    open("runtimelab.option-lab", "view=story&step=0&pane=request", (fake) =>
      fake
        .on(`GET runs/${run.id}`, {
          ...run,
          manifest_hash: "m1",
          manifest_available: true,
        })
        .on("GET manifests/m1", {
          manifest_hash: "m1",
          agent: run.agent,
          source: "runtime",
          first_seen: run.started,
          current_manifest_hash: "m1",
          manifest: {
            weft: 1,
            agents: [
              {
                name: run.agent,
                model: run.model,
                policy: {
                  parallelism: 4,
                  max_steps: 10,
                  max_result_bytes: 65536,
                  max_model_retries: 3,
                },
                tools: [],
                params: { temperature: 0.7 },
              },
            ],
          },
        })
    )
    await waitFor(() =>
      expect(
        document.querySelector('[data-param-override="temperature"]')
          ?.textContent
      ).toBe("◂ override (agent: 0.7)")
    )
    expect(
      document.querySelector('[data-param-override="top_p"]')?.textContent
    ).toBe("◂ override (agent: default)")
  })

  // A runtime of the run's service, as GET /api/runtimes lists it.
  const runtime = (id: string, breakpoints: string[], connected = true) => ({
    id,
    service: shape("appr.breakpoint-own").run.service,
    breakpoints_own: true,
    breakpoints,
    connected,
    agents: [],
  })
  // The run page's Studio for the breakpoint park, with capability
  // breakpoints on and the runtimes the handler answers.
  const parkWith = (
    runtimes: () => object[],
    caps = ["requests", "steps", "ingest", "breakpoints"]
  ) =>
    open("appr.breakpoint-own", undefined, (fake) =>
      fake
        .on("GET meta", {
          ...golden<Record<string, unknown>>("meta"),
          capabilities: caps,
        })
        .on("GET runtimes", () => json({ runtimes: runtimes() }))
        .on("PUT runtimes/rt_1/breakpoints", { tools: ["deploy"] })
    )
  const clearLink = () =>
    document.querySelector<HTMLElement>("[data-breakpoint-clear]")

  it("appr.breakpoint-own: the parked card clears the breakpoint through the runtime's breakpoints PUT, on the set read just before (UX-H-18)", async () => {
    let reads = 0
    const studio = parkWith(() =>
      // A breakpoint set on "deploy2" since the page's first read.
      ++reads === 1
        ? [runtime("rt_1", ["status", "deploy"])]
        : [runtime("rt_1", ["status", "deploy", "deploy2"])]
    )
    await waitFor(() =>
      expect(clearLink()?.getAttribute("data-breakpoint-clear")).toBe("rt_1")
    )
    const before = studio.calls("GET runtimes").length
    fireEvent.click(
      within(clearLink()!).getByRole("button", {
        name: BREAKPOINT_CLEAR.clear("status"),
      })
    )
    await waitFor(() =>
      expect(clearLink()?.getAttribute("data-breakpoint-clear")).toBe("cleared")
    )
    expect(studio.calls("GET runtimes").length).toBeGreaterThan(before)
    const put = studio.calls("PUT runtimes/rt_1/breakpoints")
    expect(put).toHaveLength(1)
    expect(put[0].body).toEqual({
      tools: ["deploy", "deploy2"],
      surface: "run",
    })
  })

  it.each([
    ["read", "read"],
    ["playground", "playground"],
  ])(
    "appr.breakpoint-own, a %s panel token: the parked card says why it cannot clear the breakpoint (UX-H-18)",
    async (_, scope) => {
      setStudioToken(
        `weft_pt.${btoa(JSON.stringify({ public_id: "pub_1", scope, exp: "2099-01-01T00:00:00Z" })).replace(/=+$/, "")}.sig`
      )
      const studio = parkWith(() => [runtime("rt_1", ["status"])])
      await waitFor(() =>
        expect(clearLink()?.textContent).toBe(BREAKPOINT_CLEAR.readOnly)
      )
      expect(clearLink()!.getAttribute("data-breakpoint-clear")).toBe(
        "read-only"
      )
      expect(studio.calls("PUT runtimes/rt_1/breakpoints")).toHaveLength(0)
    }
  )

  it("appr.breakpoint-own: no link for a disconnected runtime, nor with capability breakpoints off (UX-H-18)", async () => {
    parkWith(() => [runtime("rt_1", ["status"], false)])
    // The park still reads as a breakpoint: only the link is withheld.
    await waitFor(() =>
      expect(
        document.querySelector("[data-parked] .eyebrow")?.textContent
      ).toBe(BREAKPOINT)
    )
    await new Promise((r) => setTimeout(r, 300))
    expect(clearLink()).toBeNull()
    cleanup()
    parkWith(
      () => [runtime("rt_1", ["status"])],
      ["requests", "steps", "ingest"]
    )
    // The park still reads as a breakpoint: only the link is withheld.
    await waitFor(() =>
      expect(
        document.querySelector("[data-parked] .eyebrow")?.textContent
      ).toBe(BREAKPOINT)
    )
    await new Promise((r) => setTimeout(r, 300))
    expect(clearLink()).toBeNull()
  })

  it("appr.breakpoint-own: two runtimes of the service holding it — no guess, a pointer to the runtimes page (UX-H-18)", async () => {
    const studio = parkWith(() => [
      runtime("rt_1", ["status"]),
      runtime("rt_2", ["status"]),
    ])
    await waitFor(() =>
      expect(clearLink()?.getAttribute("data-breakpoint-clear")).toBe("several")
    )
    expect(clearLink()!.textContent).toBe(
      BREAKPOINT_CLEAR.several(2, shape("appr.breakpoint-own").run.service)
    )
    expect(clearLink()!.getAttribute("href")).toContain("/runtimes")
    expect(studio.calls("PUT runtimes/rt_1/breakpoints")).toHaveLength(0)
  })
})
