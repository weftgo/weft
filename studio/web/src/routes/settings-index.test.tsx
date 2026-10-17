// U29.2 on the Settings page against a fake Studio: the section index
// (UX-A-32), the ingest fact's loopback words (UX-A-34), and the
// breakpoints' clear verb, all-tools warning and set-at line (UX-A-33).
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import {
  BREAKPOINT_WORDS,
  INGEST_WORDS,
  SETTINGS_SECTIONS,
} from "@/lib/settings"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

type MetaDoc = Record<string, unknown> & {
  capabilities: string[]
  capabilities_off: Record<string, string>
}
const metaG = () => golden<MetaDoc>("meta")
const runtimesG = golden<{ runtimes: Record<string, unknown>[] }>("runtimes")
const RT = "rt_01K7Q2W3E4R5T6Y7U8I9O0PABC"

const playgroundMeta = (): MetaDoc => {
  const m = metaG()
  const on = ["playground", "runtimes", "breakpoints"]
  const off = { ...m.capabilities_off }
  for (const c of on) delete off[c]
  return {
    ...m,
    capabilities: [...m.capabilities, ...on],
    capabilities_off: off,
    runtimes: 1,
  }
}

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  localStorage.clear()
  setStudioToken("")
  studio = new FakeStudio()
    .on("GET meta", metaG())
    .on("GET settings/pricing", golden<object>("settings-pricing"))
    .on("GET runtimes", runtimesG)
    .install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  setStudioToken("")
  localStorage.clear()
})

const section = (id: string) =>
  document.querySelector<HTMLElement>(`[data-section="${id}"]`)!

/** need: the runtimes section's element, or a throw waitFor retries. */
const need = (sel: string): HTMLElement => {
  const el = document
    .querySelector<HTMLElement>('[data-section="runtimes"]')
    ?.querySelector<HTMLElement>(sel)
  if (!el) throw new Error(`no ${sel} yet`)
  return el
}

async function openSettings(): Promise<void> {
  renderApp("/settings")
  await screen.findByRole("heading", { level: 1, name: "Settings" })
}

describe("the settings section index (UX-A-32)", () => {
  it("lists every section in page order; an entry focuses its heading", async () => {
    await openSettings()
    const index = document.querySelector<HTMLElement>("[data-settings-index]")!
    expect([...index.querySelectorAll("a")].map((a) => a.textContent)).toEqual(
      SETTINGS_SECTIONS.map((s) => s.title)
    )
    expect(
      [...document.querySelectorAll("section[data-section] h2")].map(
        (h) => h.textContent
      )
    ).toEqual(SETTINGS_SECTIONS.map((s) => s.title))
    Element.prototype.scrollIntoView = vi.fn()
    fireEvent.click(within(index).getByText("Pricing"))
    expect(document.activeElement?.id).toBe("settings-pricing")
  })
})

describe("the ingest fact (UX-A-34)", () => {
  it("says loopback, from lib/settings.ts", async () => {
    await openSettings()
    expect(
      section("studio").querySelector('[data-fact="ingest"] dd')!.textContent
    ).toBe(INGEST_WORDS.openFact)
  })
})

describe("the runtime's breakpoints (UX-A-33)", () => {
  it("every tool on warns, clear all PUTs no tools, the set says when it was set", async () => {
    studio
      .on("GET meta", playgroundMeta())
      .on("GET runtimes", {
        runtimes: [
          {
            ...runtimesG.runtimes[0],
            breakpoints: ["lookup_order", "refund"],
            breakpoints_set_at: new Date(Date.now() - 60_000).toISOString(),
          },
        ],
      })
      .on(`PUT runtimes/${RT}/breakpoints`, { tools: [] })
    await openSettings()
    const card = await waitFor(() => need("[data-runtime]"))
    await waitFor(() =>
      expect(card.querySelector("[data-breakpoints-all]")!.textContent).toBe(
        BREAKPOINT_WORDS.all
      )
    )
    expect(
      card.querySelector("[data-breakpoints-set-at]")!.textContent
    ).toMatch(/^set .+ through this Studio$/)
    fireEvent.click(card.querySelector("[data-breakpoints-clear]")!)
    await waitFor(() =>
      expect(studio.calls(`PUT runtimes/${RT}/breakpoints`)[0]?.body).toEqual({
        tools: [],
      })
    )
  })

  it("a set this Studio did not deliver says so, never a made-up time", async () => {
    studio.on("GET meta", playgroundMeta()).on("GET runtimes", {
      runtimes: [{ ...runtimesG.runtimes[0], breakpoints: ["refund"] }],
    })
    await openSettings()
    const line = await waitFor(() => need("[data-breakpoints-set-at]"))
    expect(line.textContent).toBe(BREAKPOINT_WORDS.setAtUnknown)
    expect(
      section("runtimes").querySelector("[data-breakpoints-all]")
    ).toBeNull()
  })
})
