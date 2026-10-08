// The entry module (§5.1–§5.3): what one <script> tag does to a page.
// window.__WEFT__ stays the page's own object, both ways of writing
// its publicId rescope the dock, and the dock the entry mounts is the
// only node the panel will ever remove.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { WeftDevtools } from "./element"
import { baseRoutes, FakeEventSource, fakeStudio, settle, setup, teardown } from "./testkit"

type Weft = { publicId?: string; build?: string }
const page = window as unknown as { __WEFT__?: Weft }

beforeEach(() => {
  setup()
  vi.resetModules()
  const s = document.createElement("script")
  s.type = "module"
  s.src = "http://studio.test/studio/panel.js"
  // The tag §5.2 documents for the __WEFT__ default: no data-* knob, so
  // data-weft is what lets the panel find it (C2: no file-name match).
  s.setAttribute("data-weft", "")
  document.head.appendChild(s)
})

afterEach(() => {
  teardown()
  document.head.querySelectorAll("script").forEach((s) => s.remove())
  delete page.__WEFT__
})

const dock = () => document.querySelector<WeftDevtools>("weft-devtools")
/** The public ids the open conversation streams follow right now. */
const scopes = () =>
  FakeEventSource.instances
    .filter((i) => i.readyState !== 2 && i.url.includes("public_id="))
    .map((i) => new URL(i.url).searchParams.get("public_id"))
    .sort()

describe("window.__WEFT__ (§5.2)", () => {
  it("stays the page's own object: its other properties survive the panel loading", async () => {
    const studio = fakeStudio(baseRoutes())
    const mine = { publicId: "pub_orders", build: "2026.10" }
    page.__WEFT__ = mine
    await import("./main")
    await settle()
    expect(page.__WEFT__).toBe(mine)
    expect(page.__WEFT__.build).toBe("2026.10")
    expect(page.__WEFT__.publicId).toBe("pub_orders")
    expect(scopes()).toEqual(["pub_orders"])
    expect(dock()?.hasAttribute("data-public-id")).toBe(false) // the page's markup is not written to
    expect(dock()?.autoMounted).toBe(true)
    // The endpoint is the script's own directory — origin and path, not
    // the page's (the fake Studio routes on the path alone, so only the
    // URL itself can tell).
    const metas = studio.fetchMock.mock.calls.map((c) => String(c[0])).filter((u) => u.includes("/api/meta"))
    expect(metas).toEqual(["http://studio.test/studio/api/meta"])
  })

  it("rescopes on reassignment and on setting publicId on the object; none returns to the default", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_b&limit=50"] = { total: 0, runs: [], next_before: null }
    routes["runs?public_id=pub_c&limit=50"] = { total: 0, runs: [], next_before: null }
    const studio = fakeStudio(routes)
    await import("./main")
    await settle()
    expect(scopes()).toEqual([])
    page.__WEFT__ = { publicId: "pub_b" } // the documented way
    await settle()
    expect(scopes()).toEqual(["pub_b"])
    const kept = (page.__WEFT__ as Weft | undefined) ?? {} // window.__WEFT__ = window.__WEFT__ || {}
    page.__WEFT__ = kept
    kept.publicId = "pub_c" // the other way a page writes it
    expect(page.__WEFT__.publicId).toBe("pub_c")
    await settle()
    expect(scopes()).toEqual(["pub_c"])
    expect(studio.gets("meta")).toHaveLength(1) // rescoped, not restarted
    page.__WEFT__ = {} // the user left the conversation
    await settle()
    expect(scopes()).toEqual([])
    expect(studio.gets("runs?limit=10").length).toBeGreaterThan(0) // the dev list again
  })

  it("window.__WEFT__ = {scope} (C3.2) rescopes too, and setting scope on the object does", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_b&limit=50"] = { total: 0, runs: [], next_before: null }
    fakeStudio(routes)
    await import("./main")
    await settle()
    ;(page as { __WEFT__?: unknown }).__WEFT__ = { scope: "pub_b;flow=f_1" }
    await settle()
    expect(scopes()).toEqual(["pub_b"])
    const chips = () =>
      Array.from(dock()?.shadowRoot?.querySelectorAll(".weft-scope-chip") ?? []).map((n) => n.textContent)
    dock()?.toggle()
    await settle()
    expect(chips()).toEqual(["flow f_1"])
    ;(page.__WEFT__ as { scope?: string }).scope = "pub_orders"
    await settle()
    expect(scopes()).toEqual(["pub_orders"])
    expect(chips()).toEqual([])
  })

  it("markup the page re-renders (a framework remount) follows __WEFT__ too; an explicit data-public-id is never overridden", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_b&limit=50"] = { total: 0, runs: [], next_before: null }
    routes["runs?public_id=pub_c&limit=50"] = { total: 0, runs: [], next_before: null }
    fakeStudio(routes)
    const markup = () => {
      const n = document.createElement("weft-devtools")
      n.setAttribute("data-open", "true")
      document.body.appendChild(n)
      return n
    }
    const first = markup()
    await import("./main")
    await settle()
    first.remove() // the framework re-renders the route…
    const second = markup() // …and mounts a new element
    page.__WEFT__ = { publicId: "pub_b" }
    await settle()
    expect(scopes()).toEqual(["pub_b"])
    // Mounted after the assignment: it reads the scope on connect.
    second.remove()
    const third = markup()
    await settle()
    expect(scopes()).toEqual(["pub_b"])
    // The page's own explicit id wins over __WEFT__, always.
    const pinned = markup()
    pinned.setAttribute("data-public-id", "pub_orders")
    await settle()
    page.__WEFT__ = { publicId: "pub_c" }
    await settle()
    expect(scopes()).toEqual(["pub_c", "pub_orders"])
    expect(third.hasAttribute("data-public-id")).toBe(false)
    expect(pinned.getAttribute("data-public-id")).toBe("pub_orders")
  })

  it("a script that runs while the page is still parsing (async, or included twice) mounts one panel, after the markup", async () => {
    fakeStudio(baseRoutes())
    const state = Object.getOwnPropertyDescriptor(document, "readyState")
    Object.defineProperty(document, "readyState", { configurable: true, get: () => "loading" })
    try {
      await import("./main")
      vi.resetModules()
      await import("./main") // the tag twice (another URL: a second module instance)
      // The parser reaches the page's own markup after the script ran.
      const markup = document.createElement("weft-devtools")
      markup.setAttribute("data-public-id", "pub_orders")
      document.body.appendChild(markup)
    } finally {
      if (state) Object.defineProperty(document, "readyState", state)
      else delete (document as { readyState?: unknown }).readyState
    }
    document.dispatchEvent(new Event("DOMContentLoaded"))
    await settle()
    expect(document.querySelectorAll("weft-devtools")).toHaveLength(1)
    expect(dock()?.autoMounted).toBe(false) // the page's markup is the panel
  })

  it("is watched even when the panel's own script tag cannot be found (a renamed bundle)", async () => {
    document.head.querySelectorAll("script").forEach((s) => s.remove())
    fakeStudio(baseRoutes())
    await import("./main")
    await settle()
    page.__WEFT__ = { publicId: "pub_orders" }
    await settle()
    expect(scopes()).toEqual(["pub_orders"])
  })
})
