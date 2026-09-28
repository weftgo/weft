// Shortcut gating: bare keys only, never with a modifier or in a box.
import { describe, expect, it } from "vitest"

import { isPlainShortcut } from "./keys"

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
