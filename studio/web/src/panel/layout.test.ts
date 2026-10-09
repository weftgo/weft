// Plan D1: the layout — float / dock on any side / pill / hidden,
// drag and resize clamped to the viewport, remembered per origin in
// localStorage["weft.devtools"], data-push, data-z-index, the bottom
// sheet, and the keyboard heard only inside the panel.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  $,
  all,
  assistant,
  baseRoutes,
  create,
  FakeEventSource,
  fakeStudio,
  mount,
  page,
  runEvents,
  runRow,
  settle,
  setup,
  teardown,
  text,
  transcript,
  user,
} from "./testkit"
import { SHORTCUTS } from "./element"
import type { WeftDevtools } from "./element"
import { debugForced } from "./config"
import { STORE_KEY } from "./layout"
import { PANEL_CSS } from "./styles"

beforeEach(setup)
afterEach(() => {
  vi.restoreAllMocks()
  teardown()
  document.documentElement.removeAttribute("style")
  viewport(1024, 768)
})

const BASE = { "data-endpoint": "http://studio.test/studio/", "data-public-id": "pub_orders" }
const open = (extra: Record<string, string> = {}) => mount({ ...BASE, "data-open": "true", ...extra })

function viewport(w: number, h: number) {
  Object.defineProperty(window, "innerWidth", { value: w, configurable: true, writable: true })
  Object.defineProperty(window, "innerHeight", { value: h, configurable: true, writable: true })
  window.dispatchEvent(new Event("resize"))
}

const root = (el: WeftDevtools) => el.shadowRoot!.querySelector(".weft-root") as HTMLElement
const dock = (el: WeftDevtools) => $(el, ".weft-dock") as HTMLElement
const stored = () => JSON.parse(localStorage.getItem(STORE_KEY) ?? "null") as Record<string, unknown> | null

function key(target: EventTarget, init: KeyboardEventInit) {
  const e = new KeyboardEvent("keydown", { bubbles: true, composed: true, cancelable: true, ...init })
  target.dispatchEvent(e)
  return e
}

function pointer(target: EventTarget, type: string, x: number, y: number) {
  target.dispatchEvent(new PointerEvent(type, { bubbles: true, composed: true, cancelable: true, clientX: x, clientY: y, pointerId: 1, button: 0 }))
}

/** drag presses a handle at (x, y), moves it by (dx, dy), releases. */
async function drag(handle: Element, x: number, y: number, dx: number, dy: number) {
  pointer(handle, "pointerdown", x, y)
  pointer(handle, "pointermove", x + dx, y + dy)
  pointer(handle, "pointerup", x + dx, y + dy)
  await settle()
}

/** remount: the element goes, a fresh one comes with only the
 * endpoint and scope — what a reload does. */
async function remount(el: WeftDevtools) {
  el.remove()
  await settle()
  return mount({ ...BASE })
}

describe("the modes (D1)", () => {
  it("float is the default: a box with its inline geometry, dragged by the header", async () => {
    fakeStudio(baseRoutes())
    const el = await open()
    expect(root(el).getAttribute("data-mode")).toBe("float")
    const d = dock(el)
    expect(d.classList.contains("weft-float")).toBe(true)
    expect([d.style.left, d.style.top, d.style.width, d.style.height]).toEqual(["488px", "192px", "520px", "560px"])
    expect($(el, ".weft-head.weft-drag")).toBeTruthy()
    expect($(el, ".weft-grip")).toBeTruthy()
  })

  it("dock: data-position right-dock, and the other three sides", async () => {
    fakeStudio(baseRoutes())
    for (const [pos, side, prop] of [
      ["right-dock", "right", "width"],
      ["left-dock", "left", "width"],
      ["top-dock", "top", "height"],
      ["bottom-dock", "bottom", "height"],
    ] as const) {
      const el = await open({ "data-position": pos })
      expect(root(el).getAttribute("data-mode")).toBe("dock")
      const d = dock(el)
      expect(d.classList.contains(`weft-side-${side}`), pos).toBe(true)
      expect(d.style[prop]).toBe("460px")
      expect($(el, `.weft-edge.weft-edge-${side}`)).toBeTruthy()
      el.remove()
      localStorage.clear()
    }
  })

  it("pill: collapsed by default (and by data-mode=pill), at the dock's side", async () => {
    fakeStudio(baseRoutes())
    const el = await mount({ ...BASE })
    expect(root(el).getAttribute("data-mode")).toBe("pill")
    expect($(el, ".weft-fab")!.className).toBe("weft-fab weft-fab-bottom-right")
    el.remove()
    const left = await mount({ ...BASE, "data-open": "true", "data-mode": "pill", "data-position": "left-dock" })
    expect(root(left).getAttribute("data-mode")).toBe("pill")
    expect($(left, ".weft-fab")!.className).toBe("weft-fab weft-fab-bottom-left")
    left.remove()
    const top = await mount({ ...BASE, "data-position": "top-dock" })
    expect($(top, ".weft-fab")!.className).toBe("weft-fab weft-fab-top-right")
  })

  it("the pill carries the current turn's cost: tokens in→out once known, — while it runs", async () => {
    const routes = baseRoutes()
    fakeStudio(routes)
    const el = await mount({ ...BASE })
    expect(text(el, ".weft-fab-cost")).toBe(" · 10→4")
    el.remove()
    const running = runRow({ status: "running", finished: null })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 1, runs: [running], next_before: null }
    routes["runs/s_01-t1"] = { ...running, children: [] }
    routes["runs/s_01-t1/events?after=0&limit=500"] = page(runEvents("s_01-t1").slice(0, 2), { done: false })
    const live = await mount({ ...BASE })
    expect(text(live, ".weft-fab-count")).toBe(" ● 1")
    expect(text(live, ".weft-fab-cost")).toBe(" · —")
  })

  it("hidden: nothing drawn, the API and events keep working; open() and Alt+W leave it", async () => {
    fakeStudio(baseRoutes())
    const el = await mount({ ...BASE, "data-mode": "hidden" })
    expect(root(el).getAttribute("data-mode")).toBe("hidden")
    expect(root(el).children).toHaveLength(0)
    expect(el.isOpen).toBe(false)
    expect(el.studioLink("s_01-t1", 0)).toBe("http://studio.test/studio/runs/s_01-t1?step=0&view=story")
    expect((window as unknown as { weft?: { devtools?: unknown } }).weft?.devtools).toBe(el.api) // the global, while hidden
    const seen: string[] = []
    el.on("run", (d) => seen.push(`${d.runId} ${d.status}`))
    FakeEventSource.last("public_id=pub_orders")!.emit("run", {
      run: runRow({ id: "s_01-t3", turn: 3, status: "running", finished: null, steps: 0, started: "2026-10-01T09:09:00Z" }),
    })
    await settle()
    expect(seen).toEqual(["s_01-t3 running"]) // events keep flowing while hidden
    expect(el.shadowRoot!.querySelectorAll(".weft-dock, .weft-fab")).toHaveLength(0)
    el.open()
    await settle()
    expect(root(el).getAttribute("data-mode")).toBe("float")
    expect(el.isOpen).toBe(true)
    // Alt+W from hidden opens too.
    el.remove()
    localStorage.clear()
    const again = await mount({ ...BASE, "data-mode": "hidden" })
    key(window, { code: "KeyW", altKey: true })
    await settle()
    expect(dock(again)).toBeTruthy()
  })
})

describe("drag and resize (pointer events, clamped to the viewport)", () => {
  it("dragging the header moves the float; it never leaves the viewport", async () => {
    fakeStudio(baseRoutes())
    const el = await open()
    await drag($(el, ".weft-head")!, 600, 200, -100, -50)
    expect([dock(el).style.left, dock(el).style.top]).toEqual(["388px", "142px"])
    await drag($(el, ".weft-head")!, 500, 200, -5000, -5000)
    expect([dock(el).style.left, dock(el).style.top]).toEqual(["0px", "0px"])
    await drag($(el, ".weft-head")!, 100, 100, 5000, 5000)
    expect([dock(el).style.left, dock(el).style.top]).toEqual(["504px", "208px"]) // 1024-520, 768-560
  })

  it("a press on a header control is the control's, not a drag", async () => {
    fakeStudio(baseRoutes())
    const el = await open()
    const raw = all(el, ".weft-head button").find((b) => b.textContent === "raw")!
    await drag(raw, 600, 200, -100, -50)
    expect(dock(el).style.left).toBe("488px")
  })

  it("the corner resizes the float between 360×280 and the viewport minus 16 px", async () => {
    fakeStudio(baseRoutes())
    const el = await open()
    await drag($(el, ".weft-grip")!, 1000, 740, -1000, -1000)
    expect([dock(el).style.width, dock(el).style.height]).toEqual(["360px", "280px"])
    await drag($(el, ".weft-grip")!, 800, 400, 5000, 5000)
    expect([dock(el).style.width, dock(el).style.height]).toEqual(["1008px", "752px"])
    expect([dock(el).style.left, dock(el).style.top]).toEqual(["16px", "16px"]) // pushed back inside
  })

  it("a dock resizes along its edge: right and bottom grow toward the page, left and top away", async () => {
    fakeStudio(baseRoutes())
    const cases = [
      ["right-dock", -100, 0, "width", "560px"],
      ["left-dock", 100, 0, "width", "560px"],
      ["bottom-dock", 0, -100, "height", "560px"],
      ["top-dock", 0, 100, "height", "560px"],
      ["right-dock", 5000, 0, "width", "360px"], // the minimum
      ["bottom-dock", 0, 5000, "height", "280px"],
      ["left-dock", 5000, 0, "width", "1008px"], // the viewport minus 16
    ] as const
    for (const [pos, dx, dy, prop, want] of cases) {
      const el = await open({ "data-position": pos })
      await drag($(el, ".weft-edge")!, 500, 400, dx, dy)
      expect(dock(el).style[prop], `${pos} ${dx},${dy}`).toBe(want)
      el.remove()
      localStorage.clear()
    }
  })

  it("a drag that never ends on its handle still ends: a lost capture, a release elsewhere, a disconnect", async () => {
    fakeStudio(baseRoutes())
    const el = await open()
    pointer($(el, ".weft-head")!, "pointerdown", 600, 200)
    pointer($(el, ".weft-head")!, "pointermove", 500, 150)
    $(el, ".weft-head")!.dispatchEvent(new PointerEvent("lostpointercapture", { pointerId: 1 }))
    await settle()
    expect(dock(el).style.left).toBe("388px")
    expect(stored()).toMatchObject({ x: 388 }) // ended: saved
    // Released outside the handle (no capture): the window's release ends it.
    pointer($(el, ".weft-head")!, "pointerdown", 600, 200)
    window.dispatchEvent(new PointerEvent("pointerup", { pointerId: 1 }))
    el.close()
    await settle()
    expect($(el, ".weft-fab")).toBeTruthy() // drawn: no drag holds the draws
    // Disconnected mid-drag, then reconnected: it draws again.
    el.open()
    await settle()
    pointer($(el, ".weft-head")!, "pointerdown", 600, 200)
    el.remove()
    document.body.appendChild(el)
    await settle()
    el.close()
    await settle()
    expect($(el, ".weft-fab")).toBeTruthy()
  })

  it("a viewport resize clamps the float again (a passive listener, removed on disconnect)", async () => {
    fakeStudio(baseRoutes())
    const add = vi.spyOn(window, "addEventListener")
    const rm = vi.spyOn(window, "removeEventListener")
    const el = await open()
    expect(add.mock.calls.some(([t, , o]) => t === "resize" && (o as AddEventListenerOptions | undefined)?.passive === true)).toBe(true)
    viewport(700, 500)
    await settle()
    expect([dock(el).style.left, dock(el).style.top, dock(el).style.height]).toEqual(["180px", "16px", "484px"])
    el.remove()
    expect(rm.mock.calls.some(([t]) => t === "resize")).toBe(true)
  })
})

describe("remembered per origin (localStorage[\"weft.devtools\"])", () => {
  it("the four docks and the float survive a reload in the same place and size", async () => {
    fakeStudio(baseRoutes())
    let el = await open()
    await drag($(el, ".weft-head")!, 600, 200, -200, -100)
    await drag($(el, ".weft-grip")!, 900, 600, 40, 20)
    el = await remount(el)
    expect(dock(el).classList.contains("weft-float")).toBe(true)
    expect([dock(el).style.left, dock(el).style.top, dock(el).style.width, dock(el).style.height]).toEqual(["288px", "92px", "560px", "580px"])
    for (const side of ["right", "bottom", "left", "top"]) {
      key(dock(el), { code: "KeyW", altKey: true, shiftKey: true }) // next layout
      await settle()
      await drag($(el, ".weft-edge")!, 500, 400, side === "right" ? -10 : side === "left" ? 10 : 0, side === "bottom" ? -10 : side === "top" ? 10 : 0)
      const before = dock(el).getAttribute("style")
      el = await remount(el)
      expect(dock(el).classList.contains(`weft-side-${side}`), side).toBe(true)
      expect(dock(el).getAttribute("style")).toBe(before)
    }
    expect(stored()).toMatchObject({ v: 1, mode: "dock", side: "top", open: true, hidden: false, w: 560, h: 580 })
  })

  it("open, the selected turn and raw are remembered; a stored placement wins over data-open", async () => {
    fakeStudio(twoTurns())
    let el = await mount({ ...BASE })
    key(window, { code: "KeyW", altKey: true }) // the user opens it: a placement
    await settle()
    expect(text(el, ".weft-turn.weft-sel .weft-id")).toBe("s_01-t2")
    key(dock(el), { key: "j" })
    await settle()
    key(dock(el), { key: "r" })
    await settle()
    el.remove()
    await settle()
    el = await mount({ ...BASE, "data-open": "false" })
    expect(dock(el)).toBeTruthy()
    expect(text(el, ".weft-turn.weft-sel .weft-id")).toBe("s_01-t1")
    expect($(el, ".weft-raw")).toBeTruthy()
    expect(stored()).toMatchObject({ v: 1, run: "s_01-t1", raw: true, open: true })
  })

  it("until the user places it, only the turn and raw are stored: the page's data-* keep deciding", async () => {
    fakeStudio(twoTurns())
    let el = await open()
    key(dock(el), { key: "j" })
    await settle()
    expect(stored()).toEqual({ v: 1, run: "s_01-t1", theme: "", raw: false, tab: "story", debug: false })
    el = await remount(el)
    expect(root(el).getAttribute("data-mode")).toBe("pill") // no data-open now: collapsed
    el.remove()
    el = await mount({ ...BASE, "data-open": "true", "data-position": "left-dock" })
    expect(dock(el).classList.contains("weft-side-left")).toBe(true)
    expect(text(el, ".weft-turn.weft-sel .weft-id")).toBe("s_01-t1")
  })

  it("a running turn outranks the remembered one on reload", async () => {
    const routes = twoTurns()
    const t2 = runRow({ id: "s_01-t2", turn: 2, status: "running", finished: null, steps: 0, started: "2026-10-01T09:05:00Z" })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 2, runs: [t2, runRow({})], next_before: null }
    routes["runs/s_01-t2"] = { ...t2, children: [] }
    fakeStudio(routes)
    localStorage.setItem(STORE_KEY, JSON.stringify({ v: 1, run: "s_01-t1" }))
    const el = await open()
    expect(text(el, ".weft-turn.weft-sel .weft-id")).toBe("s_01-t2")
  })

  it("an explicit data-mode float|dock wins over what data-position implies", async () => {
    fakeStudio(baseRoutes())
    let el = await open({ "data-position": "left-dock", "data-mode": "float" })
    expect(dock(el).classList.contains("weft-float")).toBe(true)
    el.remove()
    el = await open({ "data-position": "bottom-left", "data-mode": "dock" })
    expect(dock(el).classList.contains("weft-side-right")).toBe(true)
  })

  it("an unknown version is ignored; a private window (storage throws) just forgets", async () => {
    fakeStudio(baseRoutes())
    localStorage.setItem(STORE_KEY, JSON.stringify({ v: 2, open: true, mode: "dock" }))
    const el = await mount({ ...BASE })
    expect(root(el).getAttribute("data-mode")).toBe("pill")
    el.remove()
    const get = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("denied", "SecurityError")
    })
    const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("denied", "QuotaExceededError")
    })
    const priv = await open()
    key(dock(priv), { code: "KeyW", altKey: true, shiftKey: true })
    await settle()
    expect(dock(priv).classList.contains("weft-side-right")).toBe(true)
    priv.close()
    await settle()
    expect($(priv, ".weft-fab")).toBeTruthy()
    expect(set).toHaveBeenCalled()
    get.mockRestore()
    set.mockRestore()
  })

  it("localStorage.weft_debug=1 is migrated into the key (debug) and dropped; the switch still works", () => {
    localStorage.setItem("weft_debug", "1")
    expect(debugForced()).toBe(true)
    expect(localStorage.getItem("weft_debug")).toBeNull()
    expect(stored()).toMatchObject({ v: 1, debug: true })
    expect(debugForced()).toBe(true)
    localStorage.removeItem(STORE_KEY) // the documented off switch
    expect(debugForced()).toBe(false)
  })

  it("weft_debug=1 still forces the panel when the migration cannot write", () => {
    localStorage.setItem("weft_debug", "1")
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("full", "QuotaExceededError")
    })
    expect(debugForced()).toBe(true)
    expect(localStorage.getItem("weft_debug")).toBe("1") // not migrated, not dropped
    vi.restoreAllMocks()
    localStorage.removeItem("weft_debug")
  })
})

describe("data-push (opt-in: padding on <html> while docked)", () => {
  it("is off by default: the host document is never written", async () => {
    fakeStudio(baseRoutes())
    await open({ "data-position": "right-dock" })
    expect(document.documentElement.getAttribute("style")).toBeNull()
  })

  it("pads the docked side by the dock size through --weft-devtools-inset, restored exactly", async () => {
    fakeStudio(baseRoutes())
    const html = document.documentElement
    html.style.setProperty("padding-right", "3px", "important")
    const el = await open({ "data-position": "right-dock", "data-push": "true" })
    expect(html.style.getPropertyValue("padding-right")).toBe("var(--weft-devtools-inset)")
    expect(html.style.getPropertyValue("--weft-devtools-inset")).toBe("460px")
    await drag($(el, ".weft-edge")!, 500, 400, -40, 0)
    expect(html.style.getPropertyValue("--weft-devtools-inset")).toBe("500px")
    el.close()
    await settle()
    expect(html.getAttribute("style")).toBe("padding-right: 3px !important;")
    el.open()
    await settle()
    expect(html.style.getPropertyValue("padding-right")).toBe("var(--weft-devtools-inset)")
    // A mode change moves it: bottom pads the bottom, the right is restored.
    key(dock(el), { code: "KeyW", altKey: true, shiftKey: true })
    await settle()
    expect(html.style.getPropertyValue("padding-bottom")).toBe("var(--weft-devtools-inset)")
    expect(html.style.getPropertyValue("padding-right")).toBe("3px")
    el.remove()
    expect(html.getAttribute("style")).toBe("padding-right: 3px !important;")
  })

  it("left and top docks pad their own side", async () => {
    fakeStudio(baseRoutes())
    const html = document.documentElement
    for (const side of ["left", "top"]) {
      const el = await open({ "data-position": `${side}-dock`, "data-push": "true" })
      expect(html.style.getPropertyValue(`padding-${side}`), side).toBe("var(--weft-devtools-inset)")
      expect(html.style.getPropertyValue("--weft-devtools-inset")).toBe("460px")
      el.remove()
      expect(html.style.cssText).toBe("")
      localStorage.clear()
    }
  })

  it("a host padding set while docked is the host's: replaced while docked, given back on undock", async () => {
    fakeStudio(baseRoutes())
    const html = document.documentElement
    const el = await open({ "data-position": "right-dock", "data-push": "true" })
    html.style.setProperty("padding-right", "9px")
    // The next redraw (a stream event) takes the side again…
    FakeEventSource.last("public_id=pub_orders")!.emit("run", { run: runRow({ steps: 2 }) })
    await settle()
    expect(html.style.getPropertyValue("padding-right")).toBe("var(--weft-devtools-inset)")
    // …and further redraws leave it alone (written only when not ours).
    const set = vi.spyOn(html.style, "setProperty")
    FakeEventSource.last("public_id=pub_orders")!.emit("run", { run: runRow({ steps: 3 }) })
    await settle()
    expect(set).not.toHaveBeenCalled()
    el.close()
    await settle()
    expect(html.getAttribute("style")).toBe("padding-right: 9px;")
  })

  it("a float never pushes", async () => {
    fakeStudio(baseRoutes())
    await open({ "data-push": "true" })
    expect(document.documentElement.getAttribute("style")).toBeNull()
  })
})

describe("narrow panels and small viewports", () => {
  it("under 640 px of panel the turn column is a dropdown; at 640 and over it is the column", async () => {
    fakeStudio(twoTurns())
    const el = await open()
    expect(dock(el).classList.contains("weft-narrow")).toBe(true) // the 520 px float
    const pick = $(el, "select.weft-turn-pick") as HTMLSelectElement
    expect(Array.from(pick.options).map((o) => o.value)).toEqual(["s_01-t2", "s_01-t1"])
    pick.value = "s_01-t1"
    pick.dispatchEvent(new Event("change"))
    await settle()
    expect(text(el, ".weft-turn.weft-sel .weft-id")).toBe("s_01-t1")
    await drag($(el, ".weft-grip")!, 900, 700, 200, 0)
    expect(dock(el).classList.contains("weft-narrow")).toBe(false)
    expect($(el, "select.weft-turn-pick")).toBeNull()
  })

  it("a 400 px viewport: a full-width bottom sheet, the dropdown, no inline box, no grips", async () => {
    viewport(400, 800)
    fakeStudio(baseRoutes())
    const el = await open({ "data-position": "right-dock", "data-push": "true" })
    const d = dock(el)
    expect(d.classList.contains("weft-sheet")).toBe(true)
    expect(d.classList.contains("weft-narrow")).toBe(true)
    expect([d.style.left, d.style.top, d.style.width, d.style.height]).toEqual(["", "", "", ""])
    expect($(el, "select.weft-turn-pick")).toBeTruthy()
    expect($(el, ".weft-grip, .weft-edge")).toBeNull()
    // data-push: the sheet pads the bottom by its 70vh, restored exactly.
    const html = document.documentElement
    expect(html.style.getPropertyValue("padding-bottom")).toBe("var(--weft-devtools-inset)")
    expect(html.style.getPropertyValue("--weft-devtools-inset")).toBe("70vh")
    el.close()
    await settle()
    expect(html.style.cssText).toBe("")
    el.open()
    await settle()
    expect(PANEL_CSS).toMatch(/\.weft-sheet \{[^}]*max-height: 70vh/)
    expect(PANEL_CSS).toMatch(/\.weft-narrow \.weft-head \{[^}]*flex-wrap: wrap/)
    viewport(1024, 768)
    await settle()
    expect(dock(el).classList.contains("weft-side-right")).toBe(true)
  })
})

describe("the z-index", () => {
  it("data-z-index sets it on the dock and the pill; --weft-z is the stylesheet's override", async () => {
    fakeStudio(baseRoutes())
    const el = await open({ "data-z-index": "7" })
    expect(dock(el).style.zIndex).toBe("7")
    el.close()
    await settle()
    expect(($(el, ".weft-fab") as HTMLElement).style.zIndex).toBe("7")
    expect(PANEL_CSS.match(/z-index: var\(--weft-z, 2147483000\)/g)).toHaveLength(2)
    expect(PANEL_CSS).not.toMatch(/z-index: 2147483000/)
  })

  it("an unusable data-z-index is ignored", async () => {
    fakeStudio(baseRoutes())
    const el = await open({ "data-z-index": "high" })
    expect(dock(el).style.zIndex).toBe("")
  })
})

/** Two turns, the newer with two steps. */
function twoTurns() {
  const routes = baseRoutes()
  const t2 = runRow({ id: "s_01-t2", turn: 2, steps: 2, started: "2026-10-01T09:05:00Z" })
  routes["runs?public_id=pub_orders&limit=50"] = { total: 2, runs: [t2, runRow({})], next_before: null }
  routes["runs/s_01-t2"] = { ...t2, children: [] }
  routes["runs/s_01-t2/events?after=0&limit=500"] = page([
    { type: "run_start", id: "s_01-t2", model: { provider: "wefttest", name: "script" }, agent: "acme-support" },
    { type: "step_start", run_id: "s_01-t2", index: 0 },
    { type: "step_finish", run_id: "s_01-t2", index: 0, reason: "tool_calls", usage: { input_tokens: 5, output_tokens: 2 } },
    { type: "step_start", run_id: "s_01-t2", index: 1 },
    { type: "step_finish", run_id: "s_01-t2", index: 1, reason: "stop", usage: { input_tokens: 5, output_tokens: 2 } },
    { type: "run_finish", run_id: "s_01-t2", usage: { input_tokens: 10, output_tokens: 4 }, steps: 2 },
  ])
  routes["runs/s_01-t2/transcript"] = transcript([user("q")], [assistant("a")], [assistant("b")])
  routes["runs/s_01-t2/spans"] = { spans: [] }
  return routes
}

describe("the keyboard (only inside the panel, except Alt+W)", () => {
  it("Alt+W from the page opens the dock with focus in it: Esc then closes it", async () => {
    fakeStudio(baseRoutes())
    const el = await mount({ ...BASE })
    key(window, { code: "KeyW", altKey: true })
    await settle()
    expect(el.shadowRoot!.activeElement).toBe(dock(el))
    key(el.shadowRoot!.activeElement!, { key: "Escape" })
    await settle()
    expect($(el, ".weft-fab")).toBeTruthy()
  })

  it("review: Alt+W prefers a live, page-mounted panel — markup the page adds after the auto dock or a dormant mount toggles", async () => {
    const studio = fakeStudio(baseRoutes())
    // The dock the bundle mounted by itself, connected first (and the
    // publisher of window.weft.devtools).
    const auto = create({ ...BASE })
    auto.autoMounted = true
    document.body.appendChild(auto)
    await settle()
    // A host mount whose Studio does not answer: dormant, in place.
    const answer = studio.fetchMock.getMockImplementation()!
    studio.fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) =>
      String(input).startsWith("http://down.test/") ? new Response("no studio", { status: 404 }) : answer(input, init)
    )
    const down = await mount({ "data-endpoint": "http://down.test/studio/", "data-public-id": "pub_orders" })
    expect(text(down, ".weft-unreachable")).toContain("Studio not reachable")
    // The page's own markup, added later.
    const markup = await mount({ ...BASE })
    expect([auto.isOpen, markup.isOpen]).toEqual([false, false])
    key(window, { code: "KeyW", altKey: true })
    await settle()
    expect(markup.isOpen).toBe(true)
    expect(auto.isOpen).toBe(false)
    // Without the page's markup, the auto dock answers again.
    markup.remove()
    await settle()
    key(window, { code: "KeyW", altKey: true })
    await settle()
    expect(auto.isOpen).toBe(true)
  })

  it("j / k move through the turns, J / K through the steps; ⤢ carries the step", async () => {
    fakeStudio(twoTurns())
    const el = await open()
    const d = () => dock(el)
    d().focus()
    expect(key(d(), { key: "J" }).defaultPrevented).toBe(true)
    await settle()
    expect($(el, ".weft-head a")!.getAttribute("href")).toBe("http://studio.test/studio/runs/s_01-t2?step=0&view=story")
    key(d(), { key: "J" })
    await settle()
    expect($(el, ".weft-head a")!.getAttribute("href")).toContain("step=1")
    key(d(), { key: "K" })
    await settle()
    expect($(el, ".weft-head a")!.getAttribute("href")).toContain("step=0")
    key(d(), { key: "j" })
    await settle()
    expect(text(el, ".weft-turn.weft-sel .weft-id")).toBe("s_01-t1")
    key(d(), { key: "j" }) // the end of the list stays
    await settle()
    expect(text(el, ".weft-turn.weft-sel .weft-id")).toBe("s_01-t1")
    key(d(), { key: "k" })
    await settle()
    expect(text(el, ".weft-turn.weft-sel .weft-id")).toBe("s_01-t2")
    // Focus stayed in the panel across the redraws.
    expect(el.shadowRoot!.activeElement).toBe(d())
  })

  it("g s opens the selected turn and step in Studio (lib/links.ts), in a new tab", async () => {
    fakeStudio(twoTurns())
    const opened = vi.fn()
    vi.stubGlobal("open", opened)
    const el = await open()
    key(dock(el), { key: "J" })
    await settle()
    key(dock(el), { key: "J" })
    await settle()
    key(dock(el), { key: "g" })
    key(dock(el), { key: "s" })
    expect(opened).toHaveBeenCalledWith(el.studioLink("s_01-t2", 1), "_blank", "noopener")
    expect(opened.mock.calls[0][0]).toBe("http://studio.test/studio/runs/s_01-t2?step=1&view=story")
    key(dock(el), { key: "s" }) // s alone is nothing
    expect(opened).toHaveBeenCalledTimes(1)
  })

  it("Alt+Shift+W cycles float → right → bottom → left → top → float; the header button does the same", async () => {
    fakeStudio(baseRoutes())
    const el = await open()
    const seen: string[] = []
    for (let i = 0; i < 5; i++) {
      key(dock(el), { code: "KeyW", altKey: true, shiftKey: true })
      await settle()
      seen.push([...dock(el).classList].filter((c) => !["weft-dock", "weft-open", "weft-narrow"].includes(c)).join(" "))
    }
    expect(seen).toEqual(["weft-docked weft-side-right", "weft-docked weft-side-bottom", "weft-docked weft-side-left", "weft-docked weft-side-top", "weft-float"])
    ;($(el, ".weft-layout") as HTMLElement).dispatchEvent(new Event("click", { bubbles: true }))
    await settle()
    expect(dock(el).classList.contains("weft-side-right")).toBe(true)
  })

  it("/ (D4) focuses the turn list's filter, and on the Raw tab the tree's; the list says so", async () => {
    fakeStudio(baseRoutes())
    const el = await open()
    expect(key(dock(el), { key: "/" }).defaultPrevented).toBe(true)
    expect(el.shadowRoot!.activeElement?.classList.contains("weft-turn-q")).toBe(true)
    dock(el).focus()
    key(dock(el), { key: "r" })
    await settle()
    expect(key(dock(el), { key: "/" }).defaultPrevented).toBe(true)
    expect(el.shadowRoot!.activeElement?.classList.contains("weft-tree-q")).toBe(true)
    dock(el).focus()
    key(dock(el), { key: "?" })
    await settle()
    const keys = all(el, ".weft-keys dt").map((n) => n.textContent)
    expect(keys).toEqual(SHORTCUTS.map(([k]) => k))
    expect(all(el, ".weft-keys dt").find((n) => n.textContent === "/")!.hasAttribute("title")).toBe(false)
  })

  it("a j typed into the host page's input never reaches the panel (no key capture on the host)", async () => {
    fakeStudio(twoTurns())
    const add = vi.spyOn(window, "addEventListener")
    const docAdd = vi.spyOn(document, "addEventListener")
    const el = await open()
    const input = document.createElement("input")
    document.body.appendChild(input)
    input.focus()
    for (const k of ["j", "k", "J", "K", "g", "s", "r", "?", "Escape", "/"]) expect(key(input, { key: k }).defaultPrevented, k).toBe(false)
    // …and not from the page's body either.
    input.blur()
    for (const k of ["j", "r", "Escape"]) expect(key(document.body, { key: k }).defaultPrevented, k).toBe(false)
    await settle()
    expect(text(el, ".weft-turn.weft-sel .weft-id")).toBe("s_01-t2")
    expect($(el, ".weft-raw, .weft-keys")).toBeNull()
    expect(dock(el)).toBeTruthy()
    // The window hears keydown for Alt+W alone; the document never.
    expect(add.mock.calls.filter(([t]) => t === "keydown")).toHaveLength(1)
    expect(docAdd.mock.calls.filter(([t]) => t === "keydown")).toHaveLength(0)
  })
})

describe("review: resize and Alt+W", () => {
  it("a burst of resize events is one clamp and one redraw per animation frame", async () => {
    fakeStudio(baseRoutes())
    const el = await open()
    const inner = el as unknown as { render: (s: unknown) => void }
    const spy = vi.spyOn(inner, "render")
    for (const w of [900, 880, 860, 840, 820]) {
      Object.defineProperty(window, "innerWidth", { value: w, configurable: true, writable: true })
      window.dispatchEvent(new Event("resize"))
    }
    expect(spy).not.toHaveBeenCalled() // nothing synchronous
    await new Promise((r) => requestAnimationFrame(() => r(null)))
    await new Promise((r) => requestAnimationFrame(() => r(null)))
    expect(spy).toHaveBeenCalledTimes(1)
    el.remove()
  })

  it("two panels on one page: Alt+W toggles one of them (the first connected, the global's), never both", async () => {
    fakeStudio(baseRoutes())
    const a = await mount(BASE)
    const b = await mount(BASE)
    expect([a.isOpen, b.isOpen]).toEqual([false, false])
    key(window, { code: "KeyW", altKey: true })
    await settle()
    expect([a.isOpen, b.isOpen]).toEqual([true, false])
    key(window, { code: "KeyW", altKey: true })
    await settle()
    expect([a.isOpen, b.isOpen]).toEqual([false, false])
    // The owner leaves: the other answers.
    a.remove()
    await settle()
    key(window, { code: "KeyW", altKey: true })
    await settle()
    expect(b.isOpen).toBe(true)
  })
})
