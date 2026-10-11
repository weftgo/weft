// A run derived from stock OTel GenAI spans (plan I1.3) in the devtools
// panel: the dev turn list (no public id: the newest runs) holds it,
// and its turn chip and all four tabs say derived in the stock_spans
// words through badges.ts (badge / holeLine) — never an empty tab; the
// Timeline draws its real spans; the replay verbs, the edits and save
// as test are refused with the badge's words, never offered. Every
// response is the server's (src/test/derived-fixture.ts).
import { cleanup } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { derivedWords } from "../lib/honesty"
import { SAVE_TEST_LABEL } from "../lib/saves"
import { FakeStudio } from "../test/fake-studio"
import { DERIVED, STOCK, serveDerived } from "../test/derived-fixture"
import { $, all, click, META, mount, settle, setup, teardown } from "./testkit"
import { badgeLabel } from "./badges"
import type { WeftDevtools } from "./element"

vi.setConfig({ testTimeout: 30_000 })

beforeEach(() => setup())
afterEach(() => {
  cleanup()
  teardown()
})

/** The panel in dev mode (no public id: the turn list is the newest
 * runs), on the derived fixture, every verb's capability on. */
async function open(): Promise<WeftDevtools> {
  serveDerived(
    new FakeStudio().on("GET meta", { ...META, capabilities: [...META.capabilities, "requests", "steps", "export", "pricing"] })
  ).install()
  const el = await mount({ "data-endpoint": "http://studio.test/studio/", "data-open": "true" })
  await vi.waitFor(() => expect(all(el, ".weft-turn").length).toBe(2), { timeout: 5_000 })
  click(all(el, ".weft-turn").find((b) => b.textContent.includes(DERIVED)))
  await vi.waitFor(() => expect($(el, '[data-weft-turn-holes] [data-hole="derived"]')).toBeTruthy(), { timeout: 5_000 })
  await settle()
  return el
}

async function tab(el: WeftDevtools, id: string): Promise<HTMLElement> {
  click($(el, `#weft-tab-${id}`))
  await settle()
  const tp = $(el, `#weft-tp-${id}`) as HTMLElement
  expect(tp, id).toBeTruthy()
  return tp
}

function saysStock(text: string | null | undefined, where: string) {
  expect(text, where).toContain(STOCK.reason)
  expect(text, where).toContain(STOCK.fix)
}

describe("a derived turn in the panel", () => {
  it("its turn chip is the derived badge in the stock words, on every derived row", async () => {
    const el = await open()
    const chips = all(el, '.weft-turn [data-weft-chip="derived"]')
    expect(chips).toHaveLength(2)
    for (const c of chips) {
      expect(c.getAttribute("data-hole")).toBe("derived")
      expect(badgeLabel(c)).toBe("derived")
      saysStock(c.getAttribute("title"), "chip title")
      saysStock(c.textContent, "chip accessible text")
    }
  })

  it("Story: the turn's hole said in full; the replay verbs, edits and save as test refused with the badge's words", async () => {
    const el = await open()
    const story = await tab(el, "story")
    const holes = story.querySelector<HTMLElement>("[data-weft-turn-holes]")!
    expect([...holes.querySelectorAll("[data-hole]")].map((b) => b.getAttribute("data-hole"))).toEqual(["derived"])
    saysStock(holes.textContent, "story notes")
    // No verb that would replay or edit it …
    expect(story.querySelector("[data-weft-verb]")).toBeNull()
    expect(story.querySelector("textarea")).toBeNull()
    for (const label of ["✎ Experiment", "↻ Re-run"]) {
      const b = all(el, "#weft-tp-story button[data-weft-refused]").find((n) => n.firstChild?.textContent === label) as HTMLButtonElement
      expect(b, label).toBeTruthy()
      expect(b.disabled).toBe(true)
      expect(b.getAttribute("title")).toBe(derivedWords())
    }
    // … and the wefttest export greyed, not a link.
    const save = story.querySelector<HTMLElement>('[data-weft-refused="derived"]:not(button)')!
    expect(save.firstChild?.textContent).toBe(SAVE_TEST_LABEL)
    expect(save.getAttribute("aria-disabled")).toBe("true")
    expect(save.tagName).not.toBe("A")
    saysStock(save.getAttribute("title"), "save as test")
    expect(story.querySelector("a[data-weft-save-test]")).toBeNull()
  })

  it("Request: the request badged derived with its reason and fix, no prompt claimed", async () => {
    const el = await open()
    const tp = await tab(el, "request")
    await vi.waitFor(() => expect(tp.querySelector('[data-hole="derived"]')).toBeTruthy())
    saysStock(tp.textContent, "request tab")
    expect(tp.querySelector(".weft-rq-prompt")).toBeNull()
  })

  it("Timeline: the derived line, then the real spans on a time axis", async () => {
    const el = await open()
    const tp = await tab(el, "timeline")
    await vi.waitFor(() => expect(tp.querySelector(".weft-timeline[data-axis='time']")).toBeTruthy())
    const line = tp.querySelector<HTMLElement>('.weft-note [data-hole="derived"]')!.parentElement!
    saysStock(line.textContent, "timeline")
    const names = [...tp.querySelectorAll(".weft-wf .weft-wf-name")].map((n) => n.textContent)
    expect(names).toEqual(["invoke_agent support", "chat gpt-5", "execute_tool lookup_order", "chat gpt-5"])
  })

  it("Raw: the derived line above the tree", async () => {
    const el = await open()
    const tp = await tab(el, "raw")
    const line = tp.querySelector<HTMLElement>('.weft-note [data-hole="derived"]')!.parentElement!
    saysStock(line.textContent, "raw")
    expect(tp.textContent).toContain(`"${DERIVED}"`)
  })
})
