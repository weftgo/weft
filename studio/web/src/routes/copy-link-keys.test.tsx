// G2 review fix 4: the copy-link key `y` fires only on a bare press
// outside a text box, and once per press (a held key repeats). Its own
// file: it opens no palette, and shares no document with a test that
// did (a Radix dialog's leftovers in one jsdom document stalled it).
import { cleanup, configure, fireEvent, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

let writeText: ReturnType<typeof vi.fn>
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true })
  new FakeStudio()
    .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities: ["live"] })
    .on("GET runs", { total: 0, runs: [], next_before: null })
    .install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe("the y key (G2)", () => {
  it("y is a key only outside a text box, and once per press", async () => {
    renderApp("/runs")
    await waitFor(() => expect(document.querySelector("header")).toBeTruthy())
    for (const tag of ["input", "textarea"] as const) {
      // The key's target is the box (focus is not needed: the rule reads
      // the event's target).
      const box = document.body.appendChild(document.createElement(tag))
      fireEvent.keyDown(box, { key: "y" })
      box.remove()
    }
    // A held y repeats: not a copy each time.
    fireEvent.keyDown(window, { key: "y", repeat: true })
    await new Promise((r) => setTimeout(r, 50))
    expect(writeText).not.toHaveBeenCalled()
  })

  it("fires on a bare press elsewhere", async () => {
    renderApp("/runs")
    await waitFor(() => expect(document.querySelector("header")).toBeTruthy())
    fireEvent.keyDown(window, { key: "y" })
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
  })
})
