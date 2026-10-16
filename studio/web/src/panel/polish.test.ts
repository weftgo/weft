// The Dv3 polish (WEFT-DEVTOOLS §10): ⤢ deep links carrying run and
// step, subagents lazy, the timing waterfall from /spans, and §5.2's
// keyboard with the Q4 collision decided (Alt+W primary; Chrome eats
// Ctrl+Shift+W as close-window before any page can see it).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { studioLink, WeftDevtools } from "./element"
import { idle } from "./testkit"
import { waterfallRows } from "../lib/waterfall"
import type { Span } from "../lib/api"
import { withLiveGrant } from "../test/fake-live-grant"

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
  db: { kind: "sqlite" },
  title: "t",
  has_manifest: false,
  ingest_open: true,
  interrupted_after_ms: 30000,
  capabilities: ["live"],
}

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal("EventSource", FakeEventSource)
  ;(globalThis as { __WEFT_PANEL_VERSION__?: string }).__WEFT_PANEL_VERSION__ =
    "v0.2.1"
})

afterEach(() => {
  delete (globalThis as { __WEFT_PANEL_VERSION__?: string })
    .__WEFT_PANEL_VERSION__
  vi.unstubAllGlobals()
  document.body.innerHTML = ""
})

describe("the timing waterfall (§2 Timing, from /spans)", () => {
  // The bars are lib/waterfall.ts's rows (U8.1): one model for both
  // renderers, placed over the run's own window.
  const span = (name: string, start: string, end: string) =>
    ({ name, start, end }) as Span
  const bars = (spans: Span[]) => {
    const wf = waterfallRows({ runId: "r", spans }, "time")
    const end = wf.domain[1]
    return wf.rows
      .filter((r) => r.span)
      .map((r) => ({
        left: r.start / end,
        width: (r.took ?? 0) / end,
        ms: r.took,
      }))
  }
  it("lays bars over the run's own window", () => {
    const b = bars([
      span(
        "invoke_agent",
        "2026-10-01T09:00:00.000Z",
        "2026-10-01T09:00:10.000Z"
      ),
      span(
        "chat gpt-4o",
        "2026-10-01T09:00:01.000Z",
        "2026-10-01T09:00:05.000Z"
      ),
      span("kb_lookup", "2026-10-01T09:00:05.500Z", "2026-10-01T09:00:07.000Z"),
    ])
    expect(b).toHaveLength(3)
    expect(b[0].left).toBe(0)
    expect(b[0].width).toBeCloseTo(1, 5)
    expect(b[1].left).toBeCloseTo(0.1, 5)
    expect(b[1].ms).toBe(4000)
    expect(b[2].width).toBeCloseTo(0.15, 5)
  })

  it("yields nothing without parseable spans", () => {
    expect(bars([])).toEqual([])
    expect(bars([span("x", "nope", T0)])).toEqual([])
  })

  // The rows as the Timeline draws them: timeline.test.ts (U8.3).
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
      return new Response(body, {
        headers: { "content-type": "application/json" },
      })
    })
    vi.stubGlobal("fetch", withLiveGrant(fetchMock))
    if (!customElements.get("weft-devtools"))
      customElements.define("weft-devtools", WeftDevtools)
    const el = document.createElement("weft-devtools")
    el.setAttribute("data-endpoint", "http://studio.test/studio/")
    el.setAttribute("data-public-id", "pub_orders")
    document.body.appendChild(el) // data-open unset: starts collapsed
    await idle()
    return el as WeftDevtools
  }

  // Alt+W is the window's; every other key is heard inside the panel
  // (D1), so it is pressed on the dock.
  const press = (el: WeftDevtools, ev: Partial<KeyboardEventInit>) =>
    (ev.altKey || ev.ctrlKey
      ? window
      : (el.shadowRoot?.querySelector(".weft-dock") ?? window)
    ).dispatchEvent(
      new KeyboardEvent("keydown", { bubbles: true, composed: true, ...ev })
    )

  it("starts collapsed and Alt+W opens the dock (Q4's pick)", async () => {
    const el = await mountClosed()
    expect(el.shadowRoot?.querySelector(".weft-fab")).toBeTruthy()
    press(el, { code: "KeyW", altKey: true })
    await idle()
    expect(el.shadowRoot?.querySelector(".weft-dock")).toBeTruthy()
  })

  it("Ctrl+Shift+W toggles too (where the browser delivers it)", async () => {
    const el = await mountClosed()
    press(el, { code: "KeyW", ctrlKey: true, shiftKey: true })
    await idle()
    expect(el.shadowRoot?.querySelector(".weft-dock")).toBeTruthy()
  })

  it("? shows the shortcuts and Esc closes them, then the dock", async () => {
    const el = await mountClosed()
    press(el, { code: "KeyW", altKey: true })
    await idle()
    press(el, { key: "?" })
    await idle()
    expect(el.shadowRoot?.querySelector(".weft-keys")?.textContent).toContain(
      "Alt+W"
    )
    press(el, { key: "Escape" })
    await idle()
    expect(el.shadowRoot?.querySelector(".weft-keys")).toBeNull()
    press(el, { key: "Escape" })
    await idle()
    expect(el.shadowRoot?.querySelector(".weft-fab")).toBeTruthy()
  })

  it("keys never fire while the host page's input has focus", async () => {
    const el = await mountClosed()
    const input = document.createElement("input")
    document.body.appendChild(input)
    input.focus()
    input.dispatchEvent(
      new KeyboardEvent("keydown", {
        code: "KeyW",
        altKey: true,
        bubbles: true,
      })
    )
    await idle()
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
