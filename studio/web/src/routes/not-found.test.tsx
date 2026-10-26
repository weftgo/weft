// UX-A-29 beside U29.2's one not-found state (shell-polish.test.tsx):
// a 404 is NotFound — no retry, it answers the same again; any other
// failed read keeps retry and gains the way back to its list (the trace
// page had none). UX-A-37: the skip link lands on the run page's title.
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
} from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunsPage } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import {
  apiError,
  FakeStudio,
  golden,
  pagedEvents,
  transcriptOf,
} from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

function serve(route: string, res: () => Response | object) {
  stubBrowser()
  setStudioToken("")
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  return new FakeStudio().on("GET meta", golden<object>("meta")).on(route, res)
}

const failed = () => document.querySelector<HTMLElement>("[data-load-failed]")

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe("a detail page whose read failed", () => {
  it("a run 404 is the not-found state: no retry", async () => {
    serve("GET runs/nope", () =>
      apiError(404, "not_found", "no run nope")
    ).install()
    renderApp("/runs/nope")
    await waitFor(() =>
      expect(document.querySelector('[data-not-found="run"]')).toBeTruthy()
    )
    expect(failed()).toBeNull()
    expect(screen.queryByRole("button", { name: "retry" })).toBeNull()
  })

  it("any other failure keeps retry and the way back to runs", async () => {
    serve("GET runs/r_x", () => apiError(500, "internal", "boom")).install()
    renderApp("/runs/r_x")
    await waitFor(() => expect(failed()).toBeTruthy(), { timeout: 20_000 })
    expect(screen.getByRole("button", { name: "retry" })).toBeTruthy()
    expect(
      screen.getByRole("link", { name: "back to runs" }).getAttribute("href")
    ).toBe("/runs")
  })

  it("a failed trace read has its way back to the traces", async () => {
    serve("GET traces/t_x", () => apiError(500, "internal", "boom")).install()
    renderApp("/traces/t_x")
    await waitFor(() => expect(failed()).toBeTruthy(), { timeout: 20_000 })
    expect(screen.getByRole("button", { name: "retry" })).toBeTruthy()
    expect(
      screen.getByRole("link", { name: "back to traces" }).getAttribute("href")
    ).toBe("/traces")
  })
})

describe("the run page's focus (UX-A-37)", () => {
  it("starts at the title, and the skip link lands there", async () => {
    const ok = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
    const doc = {
      ...ok,
      meta: {},
      trace_id: "",
      forked_from: "",
      experiment_id: "",
      playground: false,
      children: [],
      holes: [],
      compactions: [],
    } as unknown as RunDoc
    serve(`GET runs/${doc.id}`, () => doc)
      .on(`GET runs/${doc.id}/events`, pagedEvents([], { done: true }))
      .on(`GET runs/${doc.id}/transcript`, transcriptOf([]))
      .on(`GET runs/${doc.id}/spans`, { spans: [] })
      .install()
    renderApp(`/runs/${doc.id}`)
    const title = await waitFor(() => {
      const h = document.querySelector<HTMLElement>("[data-run-title]")
      expect(h).toBeTruthy()
      return h!
    })
    await waitFor(() => expect(document.activeElement).toBe(title))
    expect(title.hasAttribute("data-skip-target")).toBe(true)
    ;(document.activeElement as HTMLElement).blur()
    fireEvent.click(document.querySelector("[data-skip-link]")!)
    expect(document.activeElement).toBe(title)
  })
})
