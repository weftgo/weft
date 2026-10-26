// U29.2 across the shell against a fake Studio: the skip link
// (UX-F-25), the token wall's heading and its sidebar that keeps its
// shape (UX-F-26), the one not-found state (UX-F-31), and the live
// page's ingest words (UX-A-34).
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunsPage } from "@/lib/api"
import { notFoundWords, SEARCH_FOR_IT } from "@/lib/not-found"
import { INGEST_WORDS, SKIP_TO_CONTENT, TOKEN_WALL } from "@/lib/settings"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, apiError, golden } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const meta = (capabilities: string[] = ["live", "ingest"]) => ({
  ...golden<Record<string, unknown>>("meta"),
  capabilities,
})
const runs = golden<RunsPage>("runs")

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  studio = new FakeStudio().on("GET meta", meta()).install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

describe("the skip link (UX-F-25)", () => {
  it("is the first tab stop and lands past the runs list's filters", async () => {
    studio.on("GET runs", runs)
    renderApp("/runs")
    await screen.findByText("r_ok")
    const focusable = document.querySelectorAll<HTMLElement>(
      "a[href], button, input, [tabindex]:not([tabindex='-1'])"
    )
    expect(focusable[0].textContent).toBe(SKIP_TO_CONTENT)
    fireEvent.click(focusable[0])
    expect(document.activeElement).toBe(
      document.querySelector("[data-skip-target]")
    )
  })
})

describe("the token wall (UX-F-26)", () => {
  it("has a heading, and the sidebar lists the walled pages disabled until the token is in", async () => {
    studio.requireToken("dev-secret").on("GET runs", runs)
    renderApp("/runs")
    expect(
      await screen.findByRole("heading", { level: 1, name: TOKEN_WALL.heading })
    ).toBeTruthy()
    const locked = [
      ...document.querySelectorAll<HTMLElement>("[data-nav-locked]"),
    ]
    expect(locked.map((n) => n.dataset.navLocked)).toEqual([
      "Traces",
      "Agents",
      "Playground",
      "Experiments",
      "Runtimes",
    ])
    for (const n of locked) {
      expect(n.getAttribute("aria-disabled")).toBe("true")
      expect(n.textContent).toContain(TOKEN_WALL.needs)
    }
    fireEvent.change(screen.getByLabelText("API token"), {
      target: { value: "dev-secret" },
    })
    fireEvent.click(screen.getByRole("button", { name: "unlock" }))
    await screen.findByText("r_ok")
    expect(document.querySelector("[data-nav-locked]")).toBeNull()
  })
})

describe("the one not-found state (UX-F-31)", () => {
  it("a run 404: its words, back to runs, search for it opens ⌘K with the id — no retry", async () => {
    studio
      .on("GET runs/nope", () => apiError(404, "not_found", "no run nope"))
      .on("GET runs", runs)
    renderApp("/runs/nope")
    const w = notFoundWords("run", "nope")
    expect(
      await screen.findByRole("heading", { level: 1, name: w.title })
    ).toBeTruthy()
    expect(screen.getByText(w.back.label)).toBeTruthy()
    expect(screen.queryByText("retry")).toBeNull()
    fireEvent.click(screen.getByText(SEARCH_FOR_IT))
    await waitFor(() =>
      expect(
        document.querySelector<HTMLInputElement>("[cmdk-input]")?.value
      ).toBe("nope")
    )
  })

  for (const [kind, url, route] of [
    ["session", "/sessions/s_gone", "GET sessions/s_gone"],
    ["experiment", "/experiments/exp_gone", "GET experiments/exp_gone"],
  ] as const)
    it(`a ${kind} 404 says it the same way`, async () => {
      studio.on(route, () => apiError(404, "not_found", `unknown ${kind}`))
      if (kind === "experiment")
        studio.on("GET meta", meta(["live", "playground", "experiments"]))
      renderApp(url)
      const w = notFoundWords(kind, url.split("/").pop())
      expect(
        await screen.findByRole("heading", { level: 1, name: w.title })
      ).toBeTruthy()
      expect(screen.getByText(w.back.label)).toBeTruthy()
      expect(screen.queryByText("retry")).toBeNull()
    })

  it("an unknown page draws the same state", async () => {
    renderApp("/nowhere")
    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: notFoundWords("page").title,
      })
    ).toBeTruthy()
  })

  it("a 500 is no 404: the error and retry stay", async () => {
    studio.on("GET runs/r_x", () => apiError(500, "internal", "db down"))
    renderApp("/runs/r_x")
    expect(await screen.findByText("db down")).toBeTruthy()
    expect(screen.getByText("retry")).toBeTruthy()
    expect(document.querySelector("[data-not-found]")).toBeNull()
  })
})

describe("the live page's ingest words (UX-A-34)", () => {
  it("names the open rule in plain words and links to Settings", async () => {
    studio.on("GET runs", runs)
    renderApp("/live")
    const chip = await waitFor(() => {
      const el = document.querySelector<HTMLAnchorElement>("[data-ingest-open]")
      if (!el) throw new Error("no chip yet")
      return el
    })
    expect(chip.textContent).toBe(INGEST_WORDS.open)
    expect(chip.getAttribute("href")).toContain("/settings")
    expect(document.body.textContent).not.toContain("ingest open")
  })
})
