// A fresh Studio's pages (U21.2; UX-F-11, UX-S5a-10, UX-A-20): every
// page's empty state names how to connect an app to THIS Studio — its
// own URL in WEFT_STUDIO_URL, never "this command starts Studio" — and
// /runs carries a dismissible first-run card; the ? dialog and
// /settings#concepts hold the glossary.
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
import { GROUPS, TERMS } from "@/lib/glossary"
import { FIRST_RUN_KEY } from "@/components/studio/empty-state"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, golden } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const meta = (over: Record<string, unknown> = {}) => ({
  ...golden<Record<string, unknown>>("meta"),
  ...over,
})
/** Every list route, empty: each page reads its own key. */
const EMPTY = {
  total: 0,
  runs: [],
  sessions: [],
  traces: [],
  experiments: [],
  runtimes: [],
  next_before: null,
  next_before_id: null,
}

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  studio = new FakeStudio()
    .on("GET meta", meta())
    .on("GET runs", EMPTY)
    .on("GET sessions", EMPTY)
    .on("GET traces", EMPTY)
    .on("GET experiments", EMPTY)
    .on("GET runtimes", EMPTY)
    .install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
  try {
    localStorage.removeItem(FIRST_RUN_KEY)
  } catch {
    // nothing stored
  }
})

const caps = golden<{ capabilities: string[] }>("meta").capabilities

// Each page, the meta it needs, and its empty state's title.
const PAGES: [string, Record<string, unknown>, RegExp][] = [
  ["/runs", {}, /No runs yet/],
  ["/sessions", {}, /No sessions yet/],
  ["/traces", {}, /No traces yet/],
  ["/experiments", { capabilities: [...caps, "playground"] }, /No replays yet/],
  ["/live", { live_all: false }, /Nothing is running right now/],
  ["/agents", {}, /No agents to show/],
  [
    "/playground",
    { capabilities: [...caps, "playground"] },
    /No runtime connected/,
  ],
  [
    "/runtimes",
    { capabilities: [...caps, "playground", "runtimes"] },
    /No runtime has registered/,
  ],
]

/** The URL this page was served at, at the root: the document's own
 * origin — never studioURL() itself. */
const servedAt = () => window.location.origin

describe("every page's empty state names this Studio's URL (U21.2)", () => {
  it.each(PAGES)("%s", async (path, over, title) => {
    studio.on("GET meta", meta(over))
    renderApp(path)
    await screen.findByText(title)
    // The page's own origin (jsdom's), not a fixed port.
    const url = window.location.origin
    const box = await waitFor(() => {
      const b = document.querySelector<HTMLElement>(`[data-connect="${url}"]`)
      expect(b).not.toBeNull()
      return b!
    })
    expect(box.textContent).toContain(`WEFT_STUDIO_URL=${url}`)
    expect(document.body.textContent).not.toContain(
      "This command starts Studio"
    )
  })

  it("under a mount (/studio/), the lines carry the mount", async () => {
    // Studio served under /studio/: the document's own URL there.
    const was = window.location.pathname
    history.replaceState(null, "", "/studio/runs")
    try {
      renderApp("/runs")
      await screen.findByText(/No runs yet/)
      const url = `${window.location.origin}/studio`
      const box = await waitFor(() => {
        const b = document.querySelector<HTMLElement>(`[data-connect="${url}"]`)
        expect(b).not.toBeNull()
        return b!
      })
      expect(box.textContent).toContain(`WEFT_STUDIO_URL=${url}\n`)
      expect(box.textContent).toContain(`src="${url}/panel.js"`)
    } finally {
      cleanup()
      history.replaceState(null, "", was)
    }
  })

  it("the panel line names the token's place, never a token, on a walled Studio", async () => {
    studio
      .requireToken("dev-secret")
      .on("GET meta", meta({ auth_required: true }))
    setStudioToken("dev-secret")
    renderApp("/runs")
    await screen.findByText(/No runs yet/)
    const panel = await waitFor(() => {
      const p = document.querySelector<HTMLElement>(
        '[data-connect-line="panel"]'
      )
      expect(p?.textContent).toContain('data-token="<token>"')
      return p!
    })
    expect(panel.textContent).toContain("<db>.token")
    expect(document.body.textContent).not.toContain("dev-secret")
  })
})

describe("/settings' Connect an app (U21.2-fix, UX-S5a-10)", () => {
  it("leads the page with the three lines and this Studio's URL", async () => {
    studio.on("GET settings/pricing", golden<object>("settings-pricing"))
    renderApp("/settings")
    const h = await screen.findByRole("heading", {
      level: 2,
      name: "Connect an app",
    })
    const section = h.closest("section")!
    expect(
      document.querySelector("[data-section]")?.getAttribute("data-section")
    ).toBe("connect")
    const url = servedAt()
    expect(
      [...section.querySelectorAll("[data-connect-line]")].map((l) =>
        l.getAttribute("data-connect-line")
      )
    ).toEqual(["app", "panel", "mcp"])
    expect(section.textContent).toContain(`WEFT_STUDIO_URL=${url}`)
    expect(section.textContent).toContain(`weft mcp --url ${url}`)
  })
})

describe("the /runs first-run card (U21.2)", () => {
  it("shows until dismissed, and stays dismissed in this browser", async () => {
    renderApp("/runs")
    const card = await screen.findByRole("region", { name: "first run" })
    expect(card.textContent).toContain(`WEFT_STUDIO_URL=${servedAt()}`)
    expect(card.textContent).toContain("press ?")
    fireEvent.click(within(card).getByRole("button", { name: "dismiss" }))
    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "first run" })).toBeNull()
    )
    expect(localStorage.getItem(FIRST_RUN_KEY)).toBe("dismissed")
    renderApp("/runs")
    await screen.findByText(/No runs yet/)
    expect(screen.queryByRole("region", { name: "first run" })).toBeNull()
  })

  it("storage refused: it still draws and dismisses for the view", async () => {
    const get = vi
      .spyOn(Storage.prototype, "getItem")
      .mockImplementation(() => {
        throw new Error("denied")
      })
    const set = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("denied")
      })
    try {
      renderApp("/runs")
      const card = await screen.findByRole("region", { name: "first run" })
      fireEvent.click(within(card).getByRole("button", { name: "dismiss" }))
      await waitFor(() =>
        expect(screen.queryByRole("region", { name: "first run" })).toBeNull()
      )
    } finally {
      get.mockRestore()
      set.mockRestore()
    }
  })
})

describe("Concepts: the ? dialog and /settings#concepts (U21.2)", () => {
  it("the ? dialog's concepts tab lists every term by group", async () => {
    renderApp("/runs")
    await screen.findByText(/No runs yet/)
    fireEvent.keyDown(window, { key: "?" })
    const dialog = await screen.findByRole("dialog")
    fireEvent.click(within(dialog).getByRole("tab", { name: "concepts" }))
    const list = await waitFor(() => {
      const l = dialog.querySelector<HTMLElement>("[data-concepts]")
      expect(l).not.toBeNull()
      return l!
    })
    for (const g of Object.values(GROUPS)) expect(list.textContent).toContain(g)
    expect(list.querySelectorAll("[data-concept]")).toHaveLength(
      Object.keys(TERMS).length
    )
  })

  it("/settings#concepts holds each term at concept-<key>", async () => {
    studio.on("GET settings/pricing", golden<object>("settings-pricing"))
    renderApp("/settings#concept-session")
    await screen.findByRole("heading", { level: 2, name: "Concepts" })
    const entry = await waitFor(() => {
      const e = document.getElementById("concept-session")
      expect(e).not.toBeNull()
      return e!
    })
    expect(entry.textContent).toContain(TERMS.session.term)
    expect(document.getElementById("concepts")).not.toBeNull()
  })
})
