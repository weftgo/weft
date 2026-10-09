// Pane sizes (plan H3) are a per-device preference: localStorage, a
// layout only when it is one, every access survivable.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import {
  PANES_CHANGED_EVENT,
  PANES_RESET_EVENT,
  paneKey,
  readPaneSizes,
  resetPaneSizes,
  writePaneSizes,
} from "@/lib/pane-sizes"

beforeEach(() => localStorage.clear())
afterEach(() => vi.restoreAllMocks())

describe("readPaneSizes", () => {
  it("reads back what writePaneSizes saved, rounded", () => {
    writePaneSizes("trace-detail", [61.23456, 38.76544])
    expect(localStorage.getItem(paneKey("trace-detail"))).toBe("[61.23,38.77]")
    expect(readPaneSizes("trace-detail", 2)).toEqual([61.23, 38.77])
  })

  it.each([
    ["nothing saved", null],
    ["not JSON", "{oops"],
    ["not an array", '{"a":50}'],
    ["the wrong pane count", "[30,30,40]"],
    ["a string", '["50","50"]'],
    ["a negative", "[-10,110]"],
    ["not summing to 100", "[10,10]"],
    ["NaN smuggled as null", "[null,100]"],
  ])("%s reads as no layout", (_name, raw) => {
    if (raw !== null) localStorage.setItem(paneKey("playground"), raw)
    expect(readPaneSizes("playground", 2)).toBeNull()
  })

  it("a storage that throws reads as no layout and a write is survived", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied")
    })
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied")
    })
    expect(readPaneSizes("playground", 2)).toBeNull()
    const seen: unknown[] = []
    const on = (e: Event) => seen.push((e as CustomEvent).detail)
    window.addEventListener(PANES_CHANGED_EVENT, on)
    expect(() => writePaneSizes("playground", [40, 60], "me")).not.toThrow()
    window.removeEventListener(PANES_CHANGED_EVENT, on)
    // The mounted splits still follow it.
    expect(seen).toEqual([{ id: "playground", sizes: [40, 60], from: "me" }])
  })
})

describe("resetPaneSizes", () => {
  it("forgets every split's layout, keeps every other key, and tells the mounted splits", () => {
    writePaneSizes("trace-detail", [70, 30])
    writePaneSizes("story-request", [50, 50])
    localStorage.setItem("theme", "dark")
    const reset = vi.fn()
    window.addEventListener(PANES_RESET_EVENT, reset)
    resetPaneSizes()
    window.removeEventListener(PANES_RESET_EVENT, reset)
    expect(readPaneSizes("trace-detail", 2)).toBeNull()
    expect(readPaneSizes("story-request", 2)).toBeNull()
    expect(localStorage.getItem("theme")).toBe("dark")
    expect(reset).toHaveBeenCalledTimes(1)
  })
})
