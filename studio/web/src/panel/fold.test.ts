// UX-D-12's remainder: the Story's whole step card folds to its one
// head line — the answer's step (the step read, linkable through
// ?weft_step=, else the landing rule) and the streaming one open, the
// rest closed — its label a button (keyboard reachable, aria-expanded),
// the head's facts and verbs kept on the line, and what the user
// chose kept across redraws, whichever step is read.
import { afterEach, beforeEach, describe, expect, it } from "vitest"

import { panelRoutes } from "../test/cards-fixture"
import type { WeftDevtools } from "./element"
import {
  $,
  click,
  fakeStudio,
  META,
  mount,
  settle,
  setup,
  teardown,
} from "./testkit"

beforeEach(setup)
afterEach(teardown)

const CAPS = {
  ...META,
  capabilities: [...META.capabilities, "requests", "steps"],
}

const card = (el: WeftDevtools, n: number) =>
  $(el, `.weft-step[data-key="s${n}"]`) as HTMLElement
const fold = (el: WeftDevtools, n: number) =>
  card(el, n).querySelector<HTMLButtonElement>(".weft-fold")!
const body = (el: WeftDevtools, n: number) =>
  card(el, n).querySelector<HTMLElement>(".weft-step-b")!

describe("the Story's step card folds to one line (UX-D-12)", () => {
  it("opens the answer's step, folds the others to their head line, and keeps what the user chose", async () => {
    fakeStudio(panelRoutes("out.invalid-repaired"), CAPS)
    const el = await mount()
    await settle()
    // Step 0 is the landing step: open. Step 1 folded, its head line
    // still saying what it did.
    expect(fold(el, 0).tagName).toBe("BUTTON")
    expect(fold(el, 0).getAttribute("aria-expanded")).toBe("true")
    expect(body(el, 0).hidden).toBe(false)
    expect(fold(el, 1).getAttribute("aria-expanded")).toBe("false")
    expect(fold(el, 1).textContent).toBe("step 1")
    expect(body(el, 1).hidden).toBe(true)
    expect(card(el, 1).querySelector(".weft-step-h")!.textContent).toMatch(
      /in · \d+ out/
    )
    // Open step 1 (the click also reads it): it stays open, and so
    // does step 0, which the user never closed.
    click(fold(el, 1))
    await settle()
    expect(fold(el, 1).getAttribute("aria-expanded")).toBe("true")
    expect(body(el, 1).hidden).toBe(false)
    expect(fold(el, 0).getAttribute("aria-expanded")).toBe("true")
    // Close step 0: kept closed across the next redraws, though it is
    // the landing step.
    click(fold(el, 0))
    await settle()
    expect(fold(el, 0).getAttribute("aria-expanded")).toBe("false")
    expect(body(el, 0).hidden).toBe(true)
    click(fold(el, 1))
    await settle()
    expect(fold(el, 1).getAttribute("aria-expanded")).toBe("false")
    expect(fold(el, 0).getAttribute("aria-expanded")).toBe("false")
  })
  it("the step read opens its card: a ?weft_step= link's, or a click's (UX-D-12)", async () => {
    fakeStudio(panelRoutes("out.invalid-repaired"), CAPS)
    const el = await mount()
    await settle()
    expect(fold(el, 1).getAttribute("aria-expanded")).toBe("false")
    ;(
      el as unknown as { model: { selectStep: (n: number) => void } }
    ).model.selectStep(1)
    await settle()
    expect(fold(el, 1).getAttribute("aria-expanded")).toBe("true")
    expect(body(el, 1).hidden).toBe(false)
  })
})
