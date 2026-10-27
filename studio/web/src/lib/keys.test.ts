// Shortcut gating: bare keys only, never with a modifier or in a box.
import { describe, expect, it } from "vitest"

import {
  CHORD_MS,
  goChord,
  isPlainShortcut,
  PANEL_KEYMAP,
  STUDIO_KEYMAP,
} from "./keys"

function key(init: KeyboardEventInit, target?: HTMLElement): KeyboardEvent {
  const e = new KeyboardEvent("keydown", init)
  if (target) Object.defineProperty(e, "target", { value: target })
  return e
}

describe("isPlainShortcut", () => {
  it("accepts a bare key on the page", () => {
    expect(isPlainShortcut(key({ key: "r" }, document.body))).toBe(true)
  })
  it("refuses modifiers (ctrl-r must reload)", () => {
    expect(isPlainShortcut(key({ key: "r", ctrlKey: true }))).toBe(false)
    expect(isPlainShortcut(key({ key: "k", metaKey: true }))).toBe(false)
  })
  it("refuses keys typed into a box", () => {
    expect(
      isPlainShortcut(key({ key: "j" }, document.createElement("input")))
    ).toBe(false)
  })
})

describe("goChord (P8-pre-web #4)", () => {
  const at = (k: string, now: number, init: KeyboardEventInit = {}) =>
    goChord(key({ key: k, ...init }, document.body), now)
  it("g then r or a within the window: the destination", () => {
    expect(at("g", 1000)).toBe("start")
    expect(at("r", 1100)).toBe("/runs")
    expect(at("g", 2000)).toBe("start")
    expect(at("a", 2000 + CHORD_MS - 1)).toBe("/agents")
  })
  it("any other key in the window is the chord's (swallowed); a modifier is not", () => {
    expect(at("g", 1000)).toBe("start")
    expect(at("Shift", 1050)).toBeNull()
    expect(at("j", 1100)).toBe("swallow")
    // The chord is over: j is the page's again.
    expect(at("j", 1200)).toBeNull()
  })
  it("past the window, r is the page's own key", () => {
    expect(at("g", 1000)).toBe("start")
    expect(at("r", 1000 + CHORD_MS + 1)).toBeNull()
  })
  it("a g typed into a box or with a modifier starts nothing", () => {
    expect(
      goChord(key({ key: "g" }, document.createElement("input")), 1000)
    ).toBeNull()
    expect(at("r", 1001)).toBeNull()
    expect(at("g", 1000, { ctrlKey: true })).toBeNull()
  })
  it("the armed chord disarms on a typing target or a modifier, which keep their key", () => {
    expect(at("g", 1000)).toBe("start")
    // Focus moved into a field: the r is typed, not a destination.
    expect(
      goChord(key({ key: "r" }, document.createElement("textarea")), 1100)
    ).toBeNull()
    // ...and the chord is over.
    expect(at("r", 1200)).toBeNull()
    expect(at("g", 2000)).toBe("start")
    // ctrl-r reloads: neither the chord's destination nor swallowed.
    expect(at("r", 2100, { ctrlKey: true })).toBeNull()
    expect(at("a", 2200)).toBeNull()
    expect(at("g", 3000)).toBe("start")
    expect(at("k", 3100, { metaKey: true })).toBeNull()
    expect(at("r", 3200)).toBeNull()
  })
})

describe("the / filter's words", () => {
  // Studio binds / on the runs list alone (routes/runs.index.tsx): its
  // ? dialog promises no raw-view filter; the panel's keeps its own.
  it("Studio: the runs list only", () => {
    const slash = STUDIO_KEYMAP.filter((k) => k.keys.includes("/"))
    expect(slash).toEqual([
      { keys: ["/"], action: "filter the runs list", where: "runs" },
    ])
  })
  it("the panel: the list in view, the raw view's tree", () => {
    const slash = PANEL_KEYMAP.find((k) => k.keys.includes("/"))
    expect(slash?.action).toBe(
      "filter the list in view (on the raw view: its tree)"
    )
  })
})
