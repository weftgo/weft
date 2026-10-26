// U29.2 on the Settings page against a fake Studio: the section index
// (UX-A-32) and the ingest fact's loopback words (UX-A-34). The
// breakpoints' clear verb, all-tools warning and set-at line (UX-A-33)
// moved with the editor to /runtimes (runtimes.test.tsx, U20.1).
import {
  cleanup,
  configure,
  fireEvent,
  screen,
  within,
} from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import { INGEST_WORDS, SETTINGS_SECTIONS } from "@/lib/settings"
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

beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  localStorage.clear()
  setStudioToken("")
  new FakeStudio()
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
