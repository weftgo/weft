// The Dv3 polish (WEFT-DEVTOOLS §10): ⤢ deep links carrying run and
// step, subagents lazy, the timing waterfall from /spans, and §5.2's
// keyboard with the Q4 collision decided (Alt+W primary; Chrome eats
// Ctrl+Shift+W as close-window before any page can see it).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { renderWaterfall, studioLink, WeftDevtools } from "./element"
import { waterfall } from "./render"

class FakeEventSource {
  static instances: FakeEventSource[] = []
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 2
  readyState = 0
  onerror: ((e: unknown) => void) | null = null
  private listeners = new Map()
  constructor(public url: string) {
    FakeEventSource.instances.push(this)
  }
  addEventListener(type: string, cb: unknown) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set())
    this.listeners.get(type).add(cb)
  }
  close() {
    this.readyState = 2
  }
}

const T0 = "2026-10-01T09:00:00Z"
const meta = {
  weft_version: "v0.6.0",
  studio_version: "v0.2.1",
  db: "sqlite",
  title: "t",
  has_manifest: false,
  ingest_open: true,
  interrupted_after_ms: 30000,
  capabilities: ["live"],
}

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal("EventSource", FakeEventSource)
  ;(globalThis as { __WEFT_PANEL_VERSION__?: string }).__WEFT_PANEL_VERSION__ = "v0.2.1"
})

afterEach(() => {
  delete (globalThis as { __WEFT_PANEL_VERSION__?: string }).__WEFT_PANEL_VERSION__
  vi.unstubAllGlobals()
  document.body.innerHTML = ""
})

describe("the timing waterfall (§2 Timing, from /spans)", () => {
  it("lays bars over the run's own window", () => {
    const bars = waterfall([
      { name: "invoke_agent", start: "2026-10-01T09:00:00.000Z", end: "2026-10-01T09:00:10.000Z" },
      { name: "chat gpt-4o", start: "2026-10-01T09:00:01.000Z", end: "2026-10-01T09:00:05.000Z" },
      { name: "kb_lookup", start: "2026-10-01T09:00:05.500Z", end: "2026-10-01T09:00:07.000Z" },
    ])
    expect(bars).toHaveLength(3)
    expect(bars[0].left).toBe(0)
    expect(bars[0].width).toBeCloseTo(1, 5)
    expect(bars[1].left).toBeCloseTo(0.1, 5)
    expect(bars[1].ms).toBe(4000)
    expect(bars[2].width).toBeCloseTo(0.15, 5)
  })

  it("yields nothing without parseable spans", () => {
    expect(waterfall([])).toEqual([])
    expect(waterfall([{ name: "x", start: "nope", end: T0 }])).toEqual([])
  })

  it("renders one row per span with its wall time", () => {
    const box = renderWaterfall([{ name: "chat gpt-4o", left: 0.1, width: 0.5, ms: 2500 }])
    const row = box.querySelector(".weft-wf-row")
    expect(row?.querySelector(".weft-wf-name")?.textContent).toBe("chat gpt-4o")
    expect(row?.querySelector(".weft-wf-ms")?.textContent).toBe("2500ms")
    const bar = row?.querySelector(".weft-wf-bar") as HTMLElement
    expect(bar.style.left).toBe("10%")
    expect(bar.style.width).toBe("50%")
  })
})

describe("the keyboard (§5.2, Q4 decided: Alt+W primary)", () => {
  async function mountClosed(): Promise<WeftDevtools> {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const u = new URL(String(input))
      const body = JSON.stringify(
        u.pathname.endsWith("/meta")
          ? meta
          : u.pathname.endsWith("/runs")
            ? { total: 0, runs: [], next_before: null }
            : { error: { code: "not_found", message: "" } }
      )
      return new Response(body, { headers: { "content-type": "application/json" } })
    })
    vi.stubGlobal("fetch", fetchMock)
    if (!customElements.get("weft-devtools")) customElements.define("weft-devtools", WeftDevtools)
    const el = document.createElement("weft-devtools")
    el.setAttribute("data-endpoint", "http://studio.test/studio/")
    el.setAttribute("data-public-id", "pub_orders")
    document.body.appendChild(el) // data-open unset: starts collapsed
    await new Promise((r) => setTimeout(r, 30))
    return el as WeftDevtools
  }

  const press = (el: WeftDevtools, ev: Partial<KeyboardEventInit>) =>
    el.shadowRoot && window.dispatchEvent(new KeyboardEvent("keydown", ev))

  it("starts collapsed and Alt+W opens the dock (Q4's pick)", async () => {
    const el = await mountClosed()
    expect(el.shadowRoot?.querySelector(".weft-fab")).toBeTruthy()
    press(el, { code: "KeyW", altKey: true })
    await new Promise((r) => setTimeout(r, 10))
    expect(el.shadowRoot?.querySelector(".weft-dock")).toBeTruthy()
  })

  it("Ctrl+Shift+W toggles too (where the browser delivers it)", async () => {
    const el = await mountClosed()
    press(el, { code: "KeyW", ctrlKey: true, shiftKey: true })
    await new Promise((r) => setTimeout(r, 10))
    expect(el.shadowRoot?.querySelector(".weft-dock")).toBeTruthy()
  })

  it("? shows the shortcuts and Esc closes them, then the dock", async () => {
    const el = await mountClosed()
    press(el, { code: "KeyW", altKey: true })
    await new Promise((r) => setTimeout(r, 10))
    press(el, { key: "?" })
    await new Promise((r) => setTimeout(r, 10))
    expect(el.shadowRoot?.querySelector(".weft-keys")?.textContent).toContain("Alt+W")
    press(el, { key: "Escape" })
    await new Promise((r) => setTimeout(r, 10))
    expect(el.shadowRoot?.querySelector(".weft-keys")).toBeNull()
    press(el, { key: "Escape" })
    await new Promise((r) => setTimeout(r, 10))
    expect(el.shadowRoot?.querySelector(".weft-fab")).toBeTruthy()
  })

  it("keys never fire while the host page's input has focus", async () => {
    const el = await mountClosed()
    const input = document.createElement("input")
    document.body.appendChild(input)
    input.focus()
    input.dispatchEvent(
      new KeyboardEvent("keydown", { code: "KeyW", altKey: true, bubbles: true })
    )
    await new Promise((r) => setTimeout(r, 10))
    expect(el.shadowRoot?.querySelector(".weft-fab")).toBeTruthy() // still collapsed
  })
})

describe("the ⤢ deep link carries the step (Dv3)", () => {
  it("links run + step + story view, exactly what runs/$id validates", () => {
    expect(studioLink("http://studio.test/studio/", "r_ok", 2)).toBe(
      "http://studio.test/studio/runs/r_ok?step=2&view=story"
    )
  })
})
