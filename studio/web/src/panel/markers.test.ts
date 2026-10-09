// Plan C3.3: detection rung 3, the DOM marker — the production rung.
// The page's chat element carries data-weft-scope (the helpers set
// it); the panel reads those attributes through one MutationObserver
// and one passive focusin listener, follows the marker nearest focus,
// and lists every conversation it knows in a header switcher. No
// global is touched: window.fetch stays the page's.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { detectSetting, markerRungOn, pageURL, readConfig } from "./config"
import { SCOPE_HEADER } from "./detect"
import { WeftDevtools } from "./element"
import { installMarkerRung, MARKER_ATTR, MAX_WAIT_MS, markerOf, scanMarkers, UNTRUSTED_ATTR } from "./markers"
import {
  $,
  all,
  baseRoutes,
  click,
  create,
  FakeEventSource,
  fakeStudio,
  page,
  pause,
  runEvents,
  runRow,
  settle,
  setup,
  teardown,
  text,
  transcript,
  user,
  assistant,
} from "./testkit"
import type { Route } from "./testkit"

/** A Studio off loopback, no token: the header rung is off by default
 * (its endpoint is not loopback), the marker rung on (the page is). */
const REMOTE = "https://studio.example/studio/"
/** A Studio on loopback: the header rung on by default. */
const LOCAL = `${location.origin}/studio/`
/** A read-scoped panel token (setup C's default mint). */
const PANEL_TOKEN = `weft_pt.${btoa(JSON.stringify({ pid: "pub_a" })).replace(/=+$/, "")}.sig`

/** routesFor: a Studio that knows one turn of each public id. */
function routesFor(...ids: string[]): Record<string, Route> {
  const routes: Record<string, Route> = { ...baseRoutes() }
  routes["runs?limit=10"] = { total: 0, runs: [], next_before: null }
  ids.forEach((pid, i) => {
    const r = runRow({ id: `r_${pid}`, public_id: pid, session_id: `s_${pid}`, started: `2026-10-01T09:0${i}:00Z` })
    routes[`sessions?public_id=${pid}`] = { total: 1, sessions: [], next_before: null }
    routes[`runs?public_id=${pid}&limit=50`] = { total: 1, runs: [r], next_before: null }
    routes[`runs/${r.id}`] = { ...r, children: [] }
    routes[`runs/${r.id}/events?after=0&limit=500`] = page(runEvents(r.id))
    routes[`runs/${r.id}/transcript`] = transcript([user(`q ${pid}`)], [assistant(`a ${pid}`)])
    routes[`runs/${r.id}/spans`] = { spans: [] }
  })
  return routes
}

/** chat appends a chat widget: a section carrying the marker, with an
 * input inside. */
function chat(scope: string | null): { box: HTMLElement; input: HTMLInputElement } {
  const box = document.createElement("section")
  if (scope !== null) box.setAttribute(MARKER_ATTR, scope)
  const input = document.createElement("input")
  box.appendChild(input)
  document.body.appendChild(box)
  return { box, input }
}

async function mountWith(attrs: Record<string, string>): Promise<WeftDevtools> {
  const el = create(attrs)
  document.body.appendChild(el)
  await settle()
  return el
}

/** The public ids the open conversation streams follow right now. */
const streams = () =>
  FakeEventSource.instances
    .filter((i) => i.readyState !== 2 && i.url.includes("public_id="))
    .map((i) => new URL(i.url).searchParams.get("public_id"))
const switcher = (el: WeftDevtools) => $(el, "select.weft-switch") as HTMLSelectElement | null
const options = (el: WeftDevtools) =>
  all(el, "select.weft-switch option").map((o) => [o.textContent, o.getAttribute("data-weft-source")])
/** pick chooses the switcher entry whose label starts with label. */
function pick(el: WeftDevtools, label: string) {
  const sel = switcher(el)!
  const o = Array.from(sel.options).find((x) => x.textContent.replace(/^[●○] /, "").startsWith(label))!
  sel.value = o.value
  sel.dispatchEvent(new Event("change", { bubbles: true }))
}
const settleScan = () => settle(150)

/** draws counts the panel's redraws (the private draw every render ends in). */
const draws = () => vi.spyOn(WeftDevtools.prototype as unknown as { draw: () => void }, "draw")

const consoleSpies = () =>
  (["log", "info", "warn", "error", "debug"] as const).map((m) => vi.spyOn(console, m).mockImplementation(() => {}))

beforeEach(setup)
afterEach(() => {
  teardown()
  document.head.querySelectorAll("script, meta").forEach((n) => n.remove())
  vi.restoreAllMocks()
})

// ── The Done line ─────────────────────────────────────────────────

describe("C3's Done line: a page with two chats shows a scope switcher", () => {
  it("lists both, follows the first in document order, follows the other on focus, and the switcher rescopes", async () => {
    const studio = fakeStudio(routesFor("pub_a", "pub_b"))
    const original = window.fetch
    const a = chat("pub_a")
    const b = chat("pub_b;session=s_pub_b")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(window.fetch).toBe(original) // rung 3 touches no global
    expect(text(el, ".weft-detect")).toBe(" · detect: markers")
    expect(text(el, ".weft-title")).toContain("pub_a") // the first in document order
    expect(switcher(el)?.getAttribute("aria-label")).toBe("conversation")
    expect(options(el)).toEqual([
      ["● pub_a · marker", "marker"],
      ["pub_b · session s_pub_b · marker", "marker"],
    ])
    // The user goes to the second chat.
    b.input.focus()
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_b")
    expect(text(el, ".weft-scope-chip")).toBe("session s_pub_b")
    expect(streams()).toEqual(["pub_b"])
    expect(options(el)[1][0]).toBe("● pub_b · session s_pub_b · marker")
    // Focus leaving the chats keeps the one focus was last in.
    b.input.blur()
    a.box.setAttribute("data-x", "1")
    await settleScan()
    expect(text(el, ".weft-title")).toContain("pub_b")
    // The switcher: the user's choice rescopes.
    pick(el, "pub_a")
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_a")
    expect(streams()).toEqual(["pub_a"])
    expect(studio.gets("runs?public_id=pub_b")).not.toHaveLength(0)
    // …until focus goes into a chat again.
    b.input.focus()
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_b")
  })

  it("the marker nearest the focused element wins: the closest ancestor carrying the attribute", async () => {
    fakeStudio(routesFor("pub_a", "pub_b"))
    const outer = chat("pub_a")
    const inner = document.createElement("div")
    inner.setAttribute(MARKER_ATTR, "pub_b")
    const field = document.createElement("textarea")
    inner.appendChild(field)
    outer.box.appendChild(inner)
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(text(el, ".weft-title")).toContain("pub_a")
    field.focus()
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_b")
    expect(markerOf(field)).toBe(inner)
  })

  it("the chat focus was last in keeps the panel when it moves to another conversation, focus gone", async () => {
    fakeStudio(routesFor("pub_a", "pub_b", "pub_c"))
    chat("pub_a")
    const b = chat("pub_b")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    b.input.focus()
    b.input.blur()
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_b")
    b.box.setAttribute(MARKER_ATTR, "pub_c") // the same widget, its next conversation
    await settleScan()
    expect(text(el, ".weft-title")).toContain("pub_c")
  })

  it("a marker's run narrows its conversation: following it pins the run unless the user clicked a turn", async () => {
    const routes = routesFor("pub_a")
    const r2 = runRow({ id: "r_2", public_id: "pub_a", session_id: "s_pub_a", started: "2026-10-01T09:30:00Z" })
    routes["runs?public_id=pub_a&limit=50"] = { total: 2, runs: [r2, runRow({ id: "r_pub_a", public_id: "pub_a", session_id: "s_pub_a" })], next_before: null }
    routes["runs/r_2"] = { ...r2, children: [] }
    routes["runs/r_2/events?after=0&limit=500"] = page(runEvents("r_2"))
    routes["runs/r_2/transcript"] = transcript([user("q2")], [assistant("a2")])
    routes["runs/r_2/spans"] = { spans: [] }
    fakeStudio(routes)
    const a = chat("pub_a;run=r_pub_a")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(text(el, ".weft-sel .weft-id")).toBe("r_pub_a") // pinned, though r_2 is newer
    expect(switcher(el)).toBeNull()
    a.box.setAttribute(MARKER_ATTR, "pub_a;run=r_2")
    await settleScan()
    expect(text(el, ".weft-sel .weft-id")).toBe("r_2") // same conversation: narrowed, no restart
  })
})

// ── The rung itself ───────────────────────────────────────────────

describe("installMarkerRung", () => {
  it("debounces: 50 attribute flips settle to one trailing scan", async () => {
    const box = chat("pub_a").box
    const onScopes = vi.fn()
    const rung = installMarkerRung({ onScopes, debounceMs: 100 })!
    expect(onScopes).toHaveBeenCalledTimes(1) // the install's scan
    for (let i = 0; i < 50; i++) box.setAttribute(MARKER_ATTR, i % 2 ? "pub_a" : "pub_b")
    await pause(20)
    expect(onScopes).toHaveBeenCalledTimes(1)
    await pause(150)
    expect(onScopes).toHaveBeenCalledTimes(2)
    expect(onScopes.mock.calls[1][0].map((m: { scope: { publicId: string } }) => m.scope.publicId)).toEqual(["pub_a"])
    rung.disconnect()
  })

  it("review: a marker on or inside a data-weft-untrusted element is not read — scan, focus, or a mark set later", async () => {
    const chatBox = chat("pub_chat").box
    // A rendered model reply (sanitized HTML keeps data-*) carries a forged marker.
    const reply = document.createElement("div")
    reply.setAttribute(UNTRUSTED_ATTR, "")
    reply.innerHTML = `<p ${MARKER_ATTR}="pub_forged"><a href="#" id="forged">x</a></p>`
    chatBox.appendChild(reply)
    const self = document.createElement("div")
    self.setAttribute(UNTRUSTED_ATTR, "")
    self.setAttribute(MARKER_ATTR, "pub_self")
    document.body.appendChild(self)
    expect(scanMarkers().map((m) => m.scope.publicId)).toEqual(["pub_chat"])
    // Focus inside the untrusted reply names the chat around it, not the forgery.
    expect(markerOf(document.getElementById("forged"))).toBe(chatBox)
    expect(markerOf(self)).toBeNull()
    // The hatch set later rescans; lifted, the marker counts again.
    const onScopes = vi.fn()
    const rung = installMarkerRung({ onScopes, debounceMs: 10 })!
    const plain = chat("pub_plain").box
    await pause(40)
    expect(onScopes.mock.calls.at(-1)![0].map((m: { scope: { publicId: string } }) => m.scope.publicId)).toEqual(["pub_chat", "pub_plain"])
    plain.setAttribute(UNTRUSTED_ATTR, "")
    await pause(40)
    expect(onScopes.mock.calls.at(-1)![0].map((m: { scope: { publicId: string } }) => m.scope.publicId)).toEqual(["pub_chat"])
    reply.removeAttribute(UNTRUSTED_ATTR)
    await pause(40)
    expect(onScopes.mock.calls.at(-1)![0].map((m: { scope: { publicId: string } }) => m.scope.publicId)).toEqual(["pub_chat", "pub_forged"])
    rung.disconnect()
  })

  it("ignores an empty or unparsable value (no public id)", () => {
    chat("")
    chat(";session=s_1")
    chat("   ")
    const ok = chat("pub_ok;flow=f_1").box
    expect(scanMarkers()).toEqual([{ scope: { publicId: "pub_ok", flow: "f_1" }, element: ok }])
  })

  it("never scans the panel's own tree: <weft-devtools>, its children, its shadow root — nor scans on its attribute", async () => {
    fakeStudio(routesFor("pub_panel"))
    const panel = create({ "data-endpoint": REMOTE })
    panel.setAttribute(MARKER_ATTR, "pub_panel") // what scope()/mount() set
    const inside = document.createElement("div")
    inside.setAttribute(MARKER_ATTR, "pub_light")
    panel.appendChild(inside)
    const shadowMark = document.createElement("div")
    shadowMark.setAttribute(MARKER_ATTR, "pub_shadow")
    panel.shadowRoot?.appendChild(shadowMark)
    const host = document.createElement("div")
    document.body.appendChild(host)
    host.appendChild(panel)
    const onScopes = vi.fn()
    const rung = installMarkerRung({ onScopes, debounceMs: 10 })!
    expect(onScopes.mock.calls[0][0]).toEqual([])
    panel.setAttribute(MARKER_ATTR, "pub_panel2")
    await pause(40)
    expect(onScopes).toHaveBeenCalledTimes(1)
    expect(markerOf(inside)).toBeNull()
    rung.disconnect()
  })

  it("reads attributes only: never writes to the host DOM", async () => {
    const { box } = chat("pub_a")
    const before = document.documentElement.outerHTML
    const writes = new MutationObserver(() => {})
    writes.observe(document.documentElement, { subtree: true, childList: true, attributes: true })
    const rung = installMarkerRung({ onScopes: () => {} })!
    box.querySelector("input")?.focus()
    await pause(20)
    expect(writes.takeRecords()).toEqual([])
    expect(document.documentElement.outerHTML).toBe(before)
    rung.disconnect()
    writes.disconnect()
  })

  it("disconnect removes the observer, the focusin listener and a pending scan", async () => {
    const add = vi.spyOn(document, "addEventListener")
    const remove = vi.spyOn(document, "removeEventListener")
    const box = chat("pub_a").box
    const onScopes = vi.fn()
    const onFocus = vi.fn()
    const rung = installMarkerRung({ onScopes, onFocus, debounceMs: 10 })!
    const listener = add.mock.calls.find((c) => c[0] === "focusin")!
    expect(listener[2]).toEqual({ capture: true, passive: true })
    box.setAttribute(MARKER_ATTR, "pub_b")
    rung.disconnect()
    expect(remove.mock.calls.find((c) => c[0] === "focusin")?.[1]).toBe(listener[1])
    box.setAttribute(MARKER_ATTR, "pub_c")
    box.querySelector("input")?.focus()
    await pause(40)
    expect(onScopes).toHaveBeenCalledTimes(1)
    expect(onFocus).not.toHaveBeenCalled()
  })
})

// ── The panel's use of it ─────────────────────────────────────────

describe("the panel's marker rung", () => {
  it("a marker added later is followed; a removed one is dropped from the switcher and stays followed", async () => {
    fakeStudio(routesFor("pub_a", "pub_b"))
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(text(el, ".weft-title")).toBe("no conversation detected on this page")
    const a = chat("pub_a")
    await settleScan()
    expect(text(el, ".weft-title")).toContain("pub_a")
    const b = chat("pub_b")
    await settleScan()
    expect(text(el, ".weft-title")).toContain("pub_a") // a new marker does not take over
    expect(options(el).map((o) => o[1])).toEqual(["marker", "marker"])
    a.box.remove()
    await settleScan()
    expect(text(el, ".weft-title")).toContain("pub_a") // kept: only focus or the switcher moves it
    expect(el.conversations().map((c) => c.key)).toEqual(["pub_b"])
    // The followed one is said gone, the one left offered.
    expect(options(el)).toEqual([
      ["● pub_a · not on the page", null],
      ["pub_b · marker", "marker"],
    ])
    b.input.focus()
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_b")
  })

  it("the followed conversation gone from the page is said in the switcher, not offered as a choice", async () => {
    fakeStudio(routesFor("pub_a", "pub_b", "pub_c"))
    const a = chat("pub_a")
    chat("pub_b")
    chat("pub_c")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    a.box.remove()
    await settleScan()
    expect(text(el, ".weft-title")).toContain("pub_a")
    expect(options(el)).toEqual([
      ["● pub_a · not on the page", null],
      ["pub_b · marker", "marker"],
      ["pub_c · marker", "marker"],
    ])
    expect((all(el, "select.weft-switch option")[0] as HTMLOptionElement).disabled).toBe(true)
  })

  it("with one conversation there is no switcher; an explicit scope its marker repeats is one entry", async () => {
    fakeStudio(routesFor("pub_a"))
    chat("pub_a")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-scope": "pub_a", "data-open": "true" })
    expect(el.conversations()).toEqual([{ scope: { publicId: "pub_a" }, key: "pub_a", source: "explicit" }])
    expect(switcher(el)).toBeNull()
    expect(all(el, ".weft-head select")).toHaveLength(0)
  })

  it("an explicit scope no marker carries wins; the markers are listed after it, and choosing one rescopes", async () => {
    fakeStudio(routesFor("pub_x", "pub_a"))
    const a = chat("pub_a")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-scope": "pub_x", "data-open": "true" })
    expect(text(el, ".weft-title")).toContain("pub_x")
    expect(options(el)).toEqual([
      ["● pub_x · explicit", "explicit"],
      ["pub_a · marker", "marker"],
    ])
    a.input.focus()
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_x") // the page's own word stands
    pick(el, "pub_a")
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_a")
  })

  it("data-detect=\"off\" installs no marker observer and no focusin listener; disconnect removes them", async () => {
    fakeStudio(routesFor("pub_a"))
    const spy = vi.spyOn(MutationObserver.prototype, "observe")
    // The marker rung's observer only (D2's theme observer watches <html>'s own attributes).
    const observe = { get mock() { return { calls: spy.mock.calls.filter((c) => c[1]?.subtree) } } }
    const disconnect = vi.spyOn(MutationObserver.prototype, "disconnect")
    const add = vi.spyOn(document, "addEventListener")
    const remove = vi.spyOn(document, "removeEventListener")
    chat("pub_a")
    const off = await mountWith({ "data-endpoint": REMOTE, "data-detect": "off", "data-open": "true" })
    expect(observe.mock.calls).toHaveLength(0)
    expect(add.mock.calls.filter((c) => c[0] === "focusin")).toHaveLength(0)
    expect(text(off, ".weft-title")).toBe("no conversation detected on this page")
    expect(text(off, ".weft-detect")).toBe(" · detect: off")
    off.remove()
    const on = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(observe.mock.calls).toHaveLength(1)
    expect(observe.mock.calls[0][0]).toBe(document.documentElement)
    expect(observe.mock.calls[0][1]).toEqual({ subtree: true, childList: true, attributes: true, attributeFilter: [MARKER_ATTR, UNTRUSTED_ATTR] })
    const focusin = add.mock.calls.find((c) => c[0] === "focusin")!
    on.remove()
    expect(disconnect).toHaveBeenCalled()
    expect(remove.mock.calls.find((c) => c[0] === "focusin")?.[1]).toBe(focusin[1])
  })

  it("turning the rung off on a running panel removes it; a read-scoped panel token off loopback leaves it off unless data-detect names markers", async () => {
    fakeStudio(routesFor("pub_a"))
    chat("pub_a")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(text(el, ".weft-title")).toContain("pub_a")
    el.setAttribute("data-detect", "off")
    await settle()
    expect(text(el, ".weft-detect")).toBe(" · detect: off")
    expect(el.markerScopes()).toEqual([])
    const shop = "https://shop.example/"
    expect(markerRungOn({ detect: "", token: PANEL_TOKEN }, shop)).toBe(false)
    expect(markerRungOn({ detect: "markers", token: PANEL_TOKEN }, shop)).toBe(true)
    expect(markerRungOn({ detect: "headers,markers", token: PANEL_TOKEN }, shop)).toBe(true)
    expect(markerRungOn({ detect: "headers", token: PANEL_TOKEN }, shop)).toBe(false)
    expect(markerRungOn({ detect: "off", token: "" }, "http://localhost/")).toBe(false)
    // A playground-scoped panel token, the dev token or none: on anywhere.
    const playground = `weft_pt.${btoa(JSON.stringify({ pid: "pub_a", scope: "playground" })).replace(/=+$/, "")}.sig`
    for (const token of [playground, "dev-token", ""]) expect(markerRungOn({ detect: "", token }, shop)).toBe(true)
    // Any token on loopback.
    expect(markerRungOn({ detect: "", token: PANEL_TOKEN }, "http://127.0.0.1:5173/")).toBe(true)
  })

  it("data-detect's accepted values: off wins where named, empty words skipped, anything else unset", () => {
    const cases: [string, string][] = [
      ["headers", "headers"],
      ["markers", "markers"],
      ["headers,markers", "headers,markers"],
      ["markers, headers", "headers,markers"],
      ["markers,", "markers"],
      ["headers,,markers", "headers,markers"],
      ["OFF", "off"],
      ["off,headers", "off"],
      ["off,markers", "off"],
      ["off,bogus", "off"],
      ["", ""],
      [",", ""],
      ["bogus", ""],
      ["headers,bogus", ""],
    ]
    for (const [v, want] of cases) expect([v, detectSetting(v)]).toEqual([v, want])
    const el = create({ "data-detect": "markers,headers" })
    expect(readConfig(el).detect).toBe("headers,markers")
  })

  it("headers and markers together: each conversation once, explicit-free order markers then headers, with the right source hint", async () => {
    const routes = routesFor("pub_a", "pub_b", "pub_c")
    let next = "pub_b"
    routes["POST /chat"] = () => new Response("ok", { status: 200, headers: { [SCOPE_HEADER]: next } })
    fakeStudio(routes)
    chat("pub_a")
    chat("pub_b")
    const el = await mountWith({ "data-endpoint": LOCAL, "data-open": "true" })
    expect(text(el, ".weft-detect")).toContain("detect: headers+markers")
    await window.fetch("/chat", { method: "POST" }) // pub_b: already a marker
    next = "pub_c"
    await window.fetch("/chat", { method: "POST" }) // pub_c: headers alone
    await settle()
    expect(el.conversations().map((c) => [c.key, c.source])).toEqual([
      ["pub_a", "marker"],
      ["pub_b", "marker"],
      ["pub_c", "header"],
    ])
    expect(options(el).map((o) => o[1])).toEqual(["marker", "marker", "header"])
    expect(text(el, ".weft-title")).toContain("pub_a") // the marker followed; headers are listed
    pick(el, "pub_c")
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_c")
  })

  it("no console output on any of it", async () => {
    const spies = consoleSpies()
    fakeStudio(routesFor("pub_a", "pub_b"))
    const a = chat("pub_a")
    chat("not;a=valid;;=")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    a.input.focus()
    pick(el, "not")
    a.box.remove()
    await settleScan()
    el.remove()
    for (const s of spies) expect(s).not.toHaveBeenCalled()
  })
})

// ── Review fixes (C3.3 round 2) ───────────────────────────────────

describe("the marker rung, review fixes", () => {
  it("a panel nested in a marked chat: focus in its own fields is not the chat's — bounded draws, the switcher choice kept", async () => {
    fakeStudio(routesFor("pub_a", "pub_b"))
    const a = chat("pub_a")
    chat("pub_b")
    const el = create({ "data-endpoint": REMOTE, "data-open": "true" })
    a.box.appendChild(el) // mount({target}) into the chat container
    await settle()
    expect(markerOf(el)).toBeNull()
    pick(el, "pub_b")
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_b")
    const spy = draws()
    switcher(el)?.focus() // the switcher's data-weft-k field, refocused by every draw
    await settle()
    expect(spy.mock.calls.length).toBeLessThan(3)
    expect(text(el, ".weft-title")).toContain("pub_b") // the choice stands
  })

  it("a helper's explicit scope keeps being a marker's after that marker leaves: focus in the other chat stands", async () => {
    fakeStudio(routesFor("pub_a", "pub_b"))
    const a = chat("pub_a")
    const b = chat("pub_b")
    // B's helper called scope("pub_b") last.
    const el = await mountWith({ "data-endpoint": REMOTE, "data-scope": "pub_b", "data-open": "true" })
    expect(text(el, ".weft-title")).toContain("pub_b")
    a.input.focus()
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_a")
    b.box.remove() // B unmounts; scope() is never cleared
    await settleScan()
    expect(text(el, ".weft-title")).toContain("pub_a")
  })

  it("with the rung on, a helper's run change after a user's click pins the run", async () => {
    const routes = routesFor("pub_a")
    const r2 = runRow({ id: "r_2", public_id: "pub_a", session_id: "s_pub_a", started: "2026-10-01T09:30:00Z" })
    routes["runs?public_id=pub_a&limit=50"] = { total: 2, runs: [r2, runRow({ id: "r_pub_a", public_id: "pub_a", session_id: "s_pub_a" })], next_before: null }
    routes["runs/r_2"] = { ...r2, children: [] }
    routes["runs/r_2/events?after=0&limit=500"] = page(runEvents("r_2"))
    routes["runs/r_2/transcript"] = transcript([user("q2")], [assistant("a2")])
    routes["runs/r_2/spans"] = { spans: [] }
    fakeStudio(routes)
    const a = chat("pub_a")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-scope": "pub_a", "data-open": "true" })
    expect(text(el, ".weft-sel .weft-id")).toBe("r_2") // the newest
    click(all(el, ".weft-turn").find((n) => n.textContent.includes("r_pub_a")))
    await settle()
    expect(text(el, ".weft-sel .weft-id")).toBe("r_pub_a") // the user's click
    // The helper: the marker and scope() together; the scan has not run.
    a.box.setAttribute(MARKER_ATTR, "pub_a;run=r_2")
    el.setAttribute("data-scope", "pub_a;run=r_2")
    await settle()
    expect(text(el, ".weft-sel .weft-id")).toBe("r_2") // pinned over the click
    expect(text(el, ".weft-detect")).toBe(" · detect: markers")
  })

  it("a route change: the followed marker and every old one gone, one new one — the new one is followed", async () => {
    fakeStudio(routesFor("pub_a", "pub_c"))
    const a = chat("pub_a")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(text(el, ".weft-title")).toContain("pub_a")
    a.box.remove()
    chat("pub_c")
    await settleScan()
    expect(text(el, ".weft-title")).toContain("pub_c")
    expect(switcher(el)).toBeNull()
  })

  it("unrelated DOM churn draws nothing: five inserts and a marker-less attribute change", async () => {
    fakeStudio(routesFor("pub_a", "pub_b"))
    chat("pub_a")
    chat("pub_b")
    const onScopes = vi.fn()
    const rung = installMarkerRung({ onScopes, debounceMs: 10 })!
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    const spy = draws()
    const list = document.createElement("ul")
    document.body.appendChild(list)
    for (let i = 0; i < 5; i++) {
      list.appendChild(document.createElement("li"))
      await pause(30)
    }
    await settleScan()
    expect(spy).not.toHaveBeenCalled()
    expect(onScopes).toHaveBeenCalledTimes(1) // the pre-filter: no scan at all
    expect(text(el, ".weft-title")).toContain("pub_a")
    rung.disconnect()
  })

  it("continuous churn does not starve the scan: at most MAX_WAIT_MS between scans while marker changes keep coming", async () => {
    const box = chat("pub_a").box
    const other = chat("pub_x").box
    const seen: string[][] = []
    const rung = installMarkerRung({ onScopes: (ms) => seen.push(ms.map((m) => m.scope.publicId)), debounceMs: 100 })!
    box.setAttribute(MARKER_ATTR, "pub_b")
    const t0 = Date.now()
    let i = 0
    while (Date.now() - t0 < MAX_WAIT_MS + 150) {
      other.setAttribute(MARKER_ATTR, `pub_x${i++ % 2}`) // a streaming widget's churn, 16 ms apart
      await pause(16)
    }
    expect(seen.length).toBeGreaterThanOrEqual(2)
    expect(seen[1][0]).toBe("pub_b")
    rung.disconnect()
  })

  it("no scan while the page is hidden; one when it is shown", async () => {
    const box = chat("pub_a").box
    const onScopes = vi.fn()
    let hidden = false
    Object.defineProperty(document, "hidden", { configurable: true, get: () => hidden })
    try {
      const rung = installMarkerRung({ onScopes, debounceMs: 10 })!
      hidden = true
      box.setAttribute(MARKER_ATTR, "pub_b")
      await pause(40)
      expect(onScopes).toHaveBeenCalledTimes(1)
      hidden = false
      document.dispatchEvent(new Event("visibilitychange"))
      expect(onScopes).toHaveBeenCalledTimes(2)
      expect(onScopes.mock.calls[1][0][0].scope.publicId).toBe("pub_b")
      rung.disconnect()
    } finally {
      delete (document as { hidden?: boolean }).hidden
    }
  })

  it("mounted on a page off loopback under a read-scoped panel token: neither rung; data-detect=\"markers\" installs the markers alone", async () => {
    vi.spyOn(pageURL, "href").mockReturnValue("https://shop.example/chat")
    fakeStudio(routesFor("pub_a"))
    const original = window.fetch
    const spy = vi.spyOn(MutationObserver.prototype, "observe")
    const observe = { get mock() { return { calls: spy.mock.calls.filter((c) => c[1]?.subtree) } } }
    chat("pub_a")
    const off = await mountWith({ "data-endpoint": `${location.origin}/studio/`, "data-token": PANEL_TOKEN, "data-open": "true" })
    expect(observe.mock.calls).toHaveLength(0)
    expect(window.fetch).toBe(original)
    expect(text(off, ".weft-detect")).toBe(" · detect: none")
    expect(text(off, ".weft-title")).toBe("no conversation detected on this page")
    off.setAttribute("data-scope", "pub_a")
    await settle()
    expect(text(off, ".weft-detect")).toBe(" · detect: explicit")
    off.remove()
    const on = await mountWith({ "data-endpoint": REMOTE, "data-token": PANEL_TOKEN, "data-detect": "markers", "data-open": "true" })
    expect(observe.mock.calls).toHaveLength(1)
    expect(window.fetch).toBe(original)
    expect(text(on, ".weft-detect")).toBe(" · detect: markers")
    expect(text(on, ".weft-title")).toContain("pub_a")
  })
})
