// U30.2 on the runs list against a fake Studio: the compare picks
// (UX-A-16's remainder) — a checkbox per row and x on the j/k-selected
// row, "compare N" opening /compare with the first pick as the base —
// and the tokens cell's own + delegated split (UX-A-19's remainder:
// "3 in · 1 out +9 in · 5 out" from the row's own_usage / delegated_usage).
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
} from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunsPage } from "@/lib/api"
import { PICKS } from "@/lib/compare-picker"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const runs = golden<RunsPage>("runs")
const baseMeta = golden<{ capabilities: string[] }>("meta")

function serve(capabilities = baseMeta.capabilities) {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  return new FakeStudio()
    .on("GET meta", { ...baseMeta, capabilities })
    .on("GET runs", runs)
    .install()
}

beforeEach(() => setStudioToken(""))
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

const boxes = () => [
  ...document.querySelectorAll<HTMLInputElement>("[data-pick-run]"),
]

describe("the runs list's compare picks (U30.2, UX-A-16)", () => {
  it("checkboxes pick runs in order; compare N opens /compare with the first as the base", async () => {
    serve()
    const { router } = renderApp("/runs")
    await screen.findByText("r_ok")
    await waitFor(() => expect(boxes().length).toBe(runs.runs.length))
    expect(boxes()[0].getAttribute("aria-label")).toBe(
      PICKS.row(runs.runs[0].id)
    )
    const pick = (id: string) =>
      fireEvent.click(
        document.querySelector<HTMLElement>(`[data-pick-run="${id}"]`)!
      )
    pick("r_ok")
    expect(router.state.location.pathname).toBe("/runs") // a pick opens nothing
    expect(document.querySelector("[data-picks]")!.textContent).toContain(
      PICKS.needTwo
    )
    pick("r_fail")
    const compare = document.querySelector<HTMLElement>("[data-picks-compare]")!
    expect(compare.textContent).toBe(PICKS.compare(2))
    expect(await axe.run(document.querySelector("main")!)).toMatchObject({
      violations: [],
    })
    fireEvent.click(compare)
    await waitFor(() => expect(router.state.location.pathname).toBe("/compare"))
    expect(router.state.location.search).toMatchObject({ a: "r_ok" })
    expect(JSON.stringify(router.state.location.search)).toContain("r_fail")
  })

  it("x picks the j/k-selected row; clear drops every pick", async () => {
    serve()
    renderApp("/runs")
    await screen.findByText("r_ok")
    fireEvent.keyDown(window, { key: "j" })
    fireEvent.keyDown(window, { key: "x" })
    await waitFor(() =>
      expect(
        boxes()
          .filter((b) => b.checked)
          .map((b) => b.dataset.pickRun)
      ).toEqual([runs.runs[0].id])
    )
    expect(document.querySelector("[data-picks-live]")!.textContent).toBe(
      PICKS.count(1)
    )
    fireEvent.click(document.querySelector<HTMLElement>("[data-picks-clear]")!)
    expect(boxes().some((b) => b.checked)).toBe(false)
    expect(document.querySelector("[data-picks]")).toBeNull()
    // P10b-pre-web-fix: focus lands on the table, the live region stays.
    expect(document.activeElement).toBe(
      document.querySelector("[data-skip-target]")
    )
    expect(document.querySelector("[data-picks-live]")!.textContent).toBe("")
  })

  it("without capability diff: no checkbox", async () => {
    serve(baseMeta.capabilities.filter((c) => c !== "diff"))
    renderApp("/runs")
    await screen.findByText("r_ok")
    expect(boxes()).toHaveLength(0)
  })
})

describe("the tokens cell's own + delegated (U30.2, UX-A-19)", () => {
  it("a delegating row reads own then +delegated; the title says the usage is both", async () => {
    serve()
    renderApp("/runs")
    await screen.findByText("r_ok")
    const cell = await waitFor(() => {
      const c = document.querySelector<HTMLElement>("[data-token-split]")
      expect(c).toBeTruthy()
      return c!
    })
    // The delegated figure on its own line under the own one (P020.2).
    expect([...cell.children].map((c) => c.textContent)).toEqual([
      "3 in · 1 out",
      "+9 in · 5 out",
    ])
    expect(cell.title).toBe(
      "own 3 in · 1 out · + 9 in · 5 out by its child runs · 12 in · 6 out in all (the run's usage counts both)"
    )
    // One row delegates in the fixture; the others read the plain pair.
    expect(document.querySelectorAll("[data-token-split]")).toHaveLength(1)
  })
})

describe("the runs list's fit (P020.2)", () => {
  it("the refresh button stands outside the wrapping filter row; the agent column holds a longer name", async () => {
    serve()
    renderApp("/runs")
    await screen.findByText("r_ok")
    const refresh = screen.getByRole("button", { name: "refresh" })
    // Its row does not wrap: the filters wrap among themselves, never
    // the button alone onto a line of its own.
    expect(refresh.parentElement!.className).not.toContain("flex-wrap")
    expect(
      refresh.parentElement!.querySelector(":scope > .flex-wrap h1")
    ).toBeTruthy()
    const agent = [...document.querySelectorAll("tbody td")].find((td) =>
      td.className.includes("truncate whitespace-nowrap")
    )!
    expect(agent.className).toContain("max-w-36")
  })
})
