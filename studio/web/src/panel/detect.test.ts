// Plan C3.2: the scope inside the panel and detection rung 2, the
// response-header rung. The panel follows a whole Scope (publicId
// selects the conversation, session narrows the list, run pins the
// selected turn, flow is a chip); on loopback with no or a dev token
// — or wherever data-detect="headers" says so — it wraps window.fetch
// to read the Weft-Scope header of same-origin responses, never
// anything else, and puts fetch back exactly as found.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { headerRungOn, isLoopback, readConfig } from "./config"
import { installHeaderRung, isNativeFetch, SCOPE_HEADER } from "./detect"
import type { WeftDevtools } from "./element"
import { PIN_RECHECK_MS } from "./state"
import { mount as mountPanel } from "./element"
import {
  $,
  all,
  baseRoutes,
  click,
  create,
  apiError,
  META,
  pause,
  FakeEventSource,
  fakeStudio,
  page,
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

/** The page's own origin under jsdom: loopback (vitest's default URL). */
const HERE = location.origin
/** A Studio on loopback (setup A's shape: the page's own origin). */
const LOCAL = `${HERE}/studio/`
/** A Studio somewhere else (setup C's shape). */
const REMOTE = "https://studio.example/studio/"
/** A setup C panel token (read scope: the claims carry none). */
const PANEL_TOKEN = `weft_pt.${btoa(JSON.stringify({ pid: "pub_demo" })).replace(/=+$/, "")}.sig`

/** A response the page's own app sends, carrying the header. */
const scoped = (value: string | null, body = "run r_1 ok") =>
  new Response(body, { status: 200, headers: value === null ? {} : { [SCOPE_HEADER]: value } })

/** pub_demo's conversation: two turns, r_1 (older) and r_2 (newest),
 * in session s_a and s_b. */
function demoRoutes(): Record<string, Route> {
  const r1 = runRow({ id: "r_1", session_id: "s_a", public_id: "pub_demo", turn: 1, started: "2026-10-01T09:00:00Z" })
  const r2 = runRow({ id: "r_2", session_id: "s_b", public_id: "pub_demo", turn: 2, started: "2026-10-01T09:05:00Z" })
  const routes: Record<string, Route> = { ...baseRoutes() }
  routes["sessions?public_id=pub_demo"] = { total: 2, sessions: [], next_before: null }
  routes["runs?public_id=pub_demo&limit=50"] = { total: 2, runs: [r2, r1], next_before: null }
  routes["runs?limit=10"] = { total: 0, runs: [], next_before: null }
  for (const r of [r1, r2]) {
    routes[`runs/${r.id}`] = { ...r, children: [] }
    routes[`runs/${r.id}/events?after=0&limit=500`] = page(runEvents(r.id))
    routes[`runs/${r.id}/transcript`] = transcript([user(`question of ${r.id}`)], [assistant(`answer of ${r.id}`)])
    routes[`runs/${r.id}/spans`] = { spans: [] }
  }
  return routes
}

/** script writes the panel's script tag (C2's rung 4). */
function script(attrs: Record<string, string>, src = "/studio/panel.js"): HTMLScriptElement {
  const s = document.createElement("script")
  s.type = "module"
  s.setAttribute("src", src)
  for (const [k, v] of Object.entries(attrs)) s.setAttribute(k, v)
  document.head.appendChild(s)
  return s
}

/** The selected turn's id ("" when none). */
const selected = (el: WeftDevtools) => text(el, ".weft-sel .weft-id")
const ids = (el: WeftDevtools) => all(el, ".weft-turn .weft-id").map((n) => n.textContent)

beforeEach(setup)
afterEach(() => {
  teardown()
  document.head.querySelectorAll("script, meta").forEach((n) => n.remove())
  delete (window as { __WEFT__?: unknown }).__WEFT__
  vi.restoreAllMocks()
})

// ── The Done line ─────────────────────────────────────────────────

describe("C3's Done line: the studio-local page without data-public-id", () => {
  it("scopes to pub_demo and pins run r_1 from the /run response header, within one turn (one fetch)", async () => {
    const routes = demoRoutes()
    routes["POST /run"] = () => scoped("pub_demo;run=r_1", "run r_1 s_x\nYour order shipped.")
    const studio = fakeStudio(routes)
    // The example's tag, as served: data-weft, data-open, no scope at all.
    script({ "data-weft": "", "data-open": "true" })
    const el = mountPanel()
    await settle()
    expect(text(el, ".weft-title")).toBe("latest (dev)")
    expect(text(el, ".weft-detect")).toContain("detect: headers")
    // The page's own chat surface: one turn, one fetch.
    const res = await window.fetch("/run", { method: "POST", body: JSON.stringify("where is order 42?") })
    expect(await res.text()).toContain("Your order shipped.") // the page's body, untouched
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_demo")
    expect(selected(el)).toBe("r_1") // pinned, though r_2 is newer
    expect(studio.gets("runs/r_1/transcript")).toHaveLength(1)
    expect(studio.posts("/run")).toHaveLength(1)
    expect(el.detectedScopes().map((d) => [d.key, d.scope.run])).toEqual([["pub_demo", "r_1"]])
  })
})

// ── C5's clause: the fetch patch only where the host said so ──────

describe("when the header rung is on (C5's clause)", () => {
  it("a setup C page under a panel token, off loopback: window.fetch is never touched", async () => {
    fakeStudio(demoRoutes())
    const original = window.fetch
    const source = original.toString()
    const el = await mountWith({ "data-endpoint": REMOTE, "data-token": PANEL_TOKEN, "data-open": "true" })
    expect(window.fetch).toBe(original)
    expect(window.fetch.toString()).toBe(source)
    expect(text(el, ".weft-detect")).toBe(" · detect: none") // nothing names a scope, nothing detects
    el.setAttribute("data-scope", "pub_demo")
    await settle()
    expect(text(el, ".weft-detect")).toBe(" · detect: explicit")
    el.remove()
    expect(window.fetch).toBe(original)
  })

  it("the same page with data-detect=\"headers\" wraps it; data-detect=\"off\" never does", async () => {
    fakeStudio(demoRoutes())
    const original = window.fetch
    const on = await mountWith({ "data-endpoint": REMOTE, "data-token": PANEL_TOKEN, "data-detect": "headers", "data-open": "true" })
    expect(window.fetch).not.toBe(original)
    expect(text(on, ".weft-detect")).toContain("detect: headers")
    on.remove()
    expect(window.fetch).toBe(original)
    const off = await mountWith({ "data-endpoint": LOCAL, "data-detect": "off", "data-open": "true" })
    expect(window.fetch).toBe(original)
    expect(text(off, ".weft-detect")).toContain("detect: off")
  })

  it("on loopback with no token or the dev token it is on by default; a panel token on loopback is not", async () => {
    fakeStudio(demoRoutes())
    const original = window.fetch
    for (const token of ["", "dev-token-abc"]) {
      const el = await mountWith({ "data-endpoint": LOCAL, ...(token ? { "data-token": token } : {}), "data-open": "true" })
      expect(window.fetch).not.toBe(original)
      el.remove()
      expect(window.fetch).toBe(original)
    }
    const pt = await mountWith({ "data-endpoint": LOCAL, "data-token": PANEL_TOKEN, "data-open": "true" })
    expect(window.fetch).toBe(original)
    pt.remove()
  })

  it("turning the rung off on a running panel restores fetch at once, and the mount option turns it on", async () => {
    fakeStudio(demoRoutes())
    const original = window.fetch
    const el = await mountWith({ "data-endpoint": LOCAL, "data-open": "true" })
    expect(window.fetch).not.toBe(original)
    el.setAttribute("data-detect", "off")
    await settle()
    expect(window.fetch).toBe(original)
    expect(text(el, ".weft-detect")).toBe(" · detect: off")
    el.remove()
    const opt = mountPanel({ endpoint: REMOTE, token: PANEL_TOKEN, detect: "headers", open: true })
    await settle()
    expect(window.fetch).not.toBe(original)
    opt.remove()
    expect(window.fetch).toBe(original)
  })

  it("two panels on one page: the first to disconnect leaves an inert pass-through in the chain; fetch keeps working and reading", async () => {
    const routes = demoRoutes()
    routes["POST /run"] = () => scoped("pub_demo;run=r_1")
    fakeStudio(routes)
    const original = window.fetch
    const a = await mountWith({ "data-endpoint": LOCAL, "data-open": "true" })
    const afterA = window.fetch
    const b = await mountWith({ "data-endpoint": LOCAL, "data-open": "true" })
    expect(text(b, ".weft-detect")).toContain("detect: headers (chained)") // B chained to A's wrapper
    a.remove() // A first: window.fetch is B's wrapper, A's is left in place, inert
    expect(window.fetch).not.toBe(afterA)
    const res = await window.fetch("/run", { method: "POST" })
    expect(await res.text()).toBe("run r_1 ok")
    await settle()
    expect(text(b, ".weft-title")).toContain("pub_demo") // B reads through the chain
    b.remove()
    expect(window.fetch).toBe(afterA) // B put back what it found: A's inert wrapper…
    expect((await window.fetch("/run", { method: "POST" })).status).toBe(200) // …a pass-through to the original
    expect(original).not.toBe(afterA)
  })

  it("headerRungOn: the page must be on loopback too", () => {
    expect(headerRungOn({ detect: "", endpoint: LOCAL, token: "" }, "https://shop.example/")).toBe(false)
    expect(headerRungOn({ detect: "headers", endpoint: LOCAL, token: "" }, "https://shop.example/")).toBe(true)
    expect(headerRungOn({ detect: "", endpoint: LOCAL, token: "" }, "http://127.0.0.1:5173/")).toBe(true)
  })

  it("headerRungOn and isLoopback, case by case", () => {
    for (const u of ["http://localhost:7331/studio/", "http://app.localhost/", "http://127.0.0.1:8080/", "http://127.9.3.1/", "http://[::1]:7331/"])
      expect(isLoopback(u), u).toBe(true)
    for (const u of ["http://studio.example/", "http://128.0.0.1/", "http://localhost.example/", "not a url", ""])
      expect(isLoopback(u), u).toBe(false)
    expect(headerRungOn({ detect: "", endpoint: LOCAL, token: "" })).toBe(true)
    expect(headerRungOn({ detect: "", endpoint: LOCAL, token: "server-token" })).toBe(true)
    expect(headerRungOn({ detect: "", endpoint: LOCAL, token: PANEL_TOKEN })).toBe(false)
    expect(headerRungOn({ detect: "", endpoint: REMOTE, token: "" })).toBe(false)
    expect(headerRungOn({ detect: "headers", endpoint: REMOTE, token: PANEL_TOKEN })).toBe(true)
    expect(headerRungOn({ detect: "off", endpoint: LOCAL, token: "" })).toBe(false)
  })
})

/** mountWith appends a <weft-devtools> with attrs and waits for rest. */
async function mountWith(attrs: Record<string, string>): Promise<WeftDevtools> {
  const el = create(attrs)
  document.body.appendChild(el)
  await settle()
  return el
}

// ── The wrapper itself ────────────────────────────────────────────

describe("installHeaderRung", () => {
  /** stubFetch installs a test double as window.fetch: not native. */
  function stubFetch(answer: (input: RequestInfo | URL) => Response | Promise<Response>) {
    const calls: { self: unknown; args: unknown[] }[] = []
    const fn = function (this: unknown, ...args: unknown[]) {
      calls.push({ self: this, args })
      return Promise.resolve(answer(args[0] as RequestInfo | URL))
    }
    vi.stubGlobal("fetch", fn)
    return { fn, calls }
  }

  it("restores exactly what it replaced; another patcher after it is left in place", async () => {
    const { fn } = stubFetch(() => scoped("pub_a"))
    const seen: string[] = []
    const rung = installHeaderRung({ onScope: (s) => seen.push(s.publicId) })!
    expect(window.fetch).not.toBe(fn)
    expect(rung.restore()).toBe(true)
    expect(window.fetch).toBe(fn)
    // A second install, then another library wraps over it.
    const again = installHeaderRung({ onScope: (s) => seen.push(s.publicId) })!
    const ours = window.fetch
    const theirs = function (this: unknown, ...a: Parameters<typeof fetch>) {
      return ours.apply(this, a)
    }
    window.fetch = theirs
    expect(again.restore()).toBe(false)
    expect(window.fetch).toBe(theirs) // the stack is left alone
    await window.fetch("/x")
    expect(seen).toEqual([]) // and our wrapper is inert from then on
  })

  it("a panel whose fetch was wrapped after it says so in its footer once the rung is off", async () => {
    fakeStudio(demoRoutes())
    const el = await mountWith({ "data-endpoint": LOCAL, "data-open": "true" })
    const ours = window.fetch
    const theirs = function (this: unknown, ...a: Parameters<typeof fetch>) {
      return ours.apply(this, a)
    }
    window.fetch = theirs
    el.setAttribute("data-detect", "off")
    await settle()
    expect(window.fetch).toBe(theirs)
    expect(text(el, ".weft-detect")).toContain("detect: off (fetch not restored")
  })

  it("chains a non-native prior patch: the page's this and arguments, the same response, and the header read", async () => {
    const { fn, calls } = stubFetch(() => scoped("pub_a;session=s_1"))
    expect(isNativeFetch(fn)).toBe(false)
    const seen: [string, string][] = []
    const rung = installHeaderRung({ onScope: (s, path) => seen.push([JSON.stringify(s), path]) })!
    expect(rung.chained).toBe(true)
    const self = { mine: true }
    const init = { method: "POST", body: "hi" }
    const res = await window.fetch.call(self, "/chat?c=1", init)
    expect(calls).toHaveLength(1)
    expect(calls[0].self).toBe(self)
    expect(calls[0].args).toEqual(["/chat?c=1", init])
    expect(await res.text()).toBe("run r_1 ok") // the body is the page's, unread
    expect(seen).toEqual([[JSON.stringify({ publicId: "pub_a", session: "s_1" }), "/chat"]])
    rung.restore()
  })

  it("a URL or a Request as the input is read the same way; the response's own URL wins when it has one", async () => {
    stubFetch((input) => {
      const r = scoped("pub_req")
      if (String(input).includes("redirect")) Object.defineProperty(r, "url", { value: "https://elsewhere.example/landed" })
      return r
    })
    const seen: string[] = []
    const rung = installHeaderRung({ onScope: (_s, path) => seen.push(path) })!
    await window.fetch(new URL("/by-url", location.href))
    await window.fetch(new Request(new URL("/by-request", location.href)))
    await window.fetch("/redirect-me") // landed cross-origin: ignored
    expect(seen).toEqual(["/by-url", "/by-request"])
    rung.restore()
  })

  it("ignores cross-origin responses, even when they expose the header", async () => {
    stubFetch(() => scoped("pub_other"))
    const seen: string[] = []
    const rung = installHeaderRung({ onScope: (s) => seen.push(s.publicId) })!
    await window.fetch("https://api.elsewhere.example/run")
    await window.fetch("//api.elsewhere.example/run")
    expect(seen).toEqual([])
    await window.fetch("/run")
    expect(seen).toEqual(["pub_other"])
    rung.restore()
  })

  it("a response without the header (or with an empty one) changes nothing", async () => {
    let value: string | null = null
    stubFetch(() => scoped(value))
    const seen: string[] = []
    const rung = installHeaderRung({ onScope: (s) => seen.push(s.publicId) })!
    await window.fetch("/plain")
    value = ""
    await window.fetch("/empty")
    value = ";;"
    await window.fetch("/nothing-in-it")
    expect(seen).toEqual([])
    rung.restore()
  })

  it("cross-origin: read when the page and the response are both on loopback, else ignored", async () => {
    stubFetch(() => scoped("pub_dev"))
    let pageAt = "http://localhost:5173/chat" // a Vite dev server…
    const seen: string[] = []
    const rung = installHeaderRung({ onScope: (_s, path) => seen.push(path), pageURL: () => pageAt })!
    await window.fetch("http://127.0.0.1:8080/run") // …calling the Go app on another port
    expect(seen).toEqual(["/run"])
    await window.fetch("https://api.example/run") // loopback page, non-loopback response
    expect(seen).toEqual(["/run"])
    pageAt = "https://app.example/chat" // a production page
    await window.fetch("http://localhost:8080/run")
    await window.fetch("https://api.example/run")
    expect(seen).toEqual(["/run"])
    await window.fetch("https://app.example/same") // same origin is always read
    expect(seen).toEqual(["/run", "/same"])
    rung.restore()
  })

  it("a host setter that stores something other than the wrapper: nothing installed, the host's fetch back in place", () => {
    const orig = function hostFetch() {
      return Promise.resolve(scoped("pub_x"))
    } as unknown as typeof fetch
    let stored = orig
    const before = Object.getOwnPropertyDescriptor(window, "fetch")
    Object.defineProperty(window, "fetch", {
      configurable: true,
      get: () => stored,
      set: (v: typeof fetch) => {
        stored = v === orig ? orig : ((...a: Parameters<typeof fetch>) => v(...a)) // a guard
      },
    })
    try {
      expect(installHeaderRung({ onScope: () => {} })).toBeNull()
      expect(window.fetch).toBe(orig)
    } finally {
      if (before) Object.defineProperty(window, "fetch", before)
      else delete (window as { fetch?: unknown }).fetch
    }
  })

  it("passes an AbortSignal through: an aborted request rejects as the fetch below rejects it", async () => {
    const seenSignals: unknown[] = []
    vi.stubGlobal("fetch", (_i: unknown, init?: RequestInit) => {
      seenSignals.push(init?.signal)
      return init?.signal?.aborted ? Promise.reject(new DOMException("aborted", "AbortError")) : Promise.resolve(scoped(null))
    })
    const rung = installHeaderRung({ onScope: () => {} })!
    const ac = new AbortController()
    ac.abort()
    await expect(window.fetch("/x", { signal: ac.signal })).rejects.toMatchObject({ name: "AbortError" })
    expect(seenSignals).toEqual([ac.signal])
    rung.restore()
  })

  it("refuses to install when window.fetch is not a function", () => {
    vi.stubGlobal("fetch", undefined)
    expect(installHeaderRung({ onScope: () => {} })).toBeNull()
    expect(window.fetch).toBeUndefined()
  })

  it("no console output and no change to the page's outcome on any failure path", async () => {
    const spies = (["log", "info", "warn", "error", "debug"] as const).map((k) => vi.spyOn(console, k).mockImplementation(() => {}))
    const bad: Record<string, unknown> = {
      throwingGet: { url: "", headers: { get: () => { throw new Error("boom") } } },
      opaque: { type: "opaque", url: "", headers: new Headers() },
      noHeaders: { url: "/x" },
      notObject: 42,
      badURL: { url: "http://[bad", headers: new Headers({ [SCOPE_HEADER]: "pub_x" }) },
    }
    let mode = "throwingGet"
    vi.stubGlobal("fetch", () => Promise.resolve(bad[mode]))
    const rung = installHeaderRung({
      onScope: () => {
        throw new Error("a panel callback that throws")
      },
    })!
    for (mode of Object.keys(bad)) {
      const got = await window.fetch("/x")
      expect(got).toBe(bad[mode]) // the page receives what the fetch below it answered
    }
    // onScope itself throwing:
    vi.stubGlobal("fetch", () => Promise.resolve(scoped("pub_x")))
    rung.restore()
    const thrower = installHeaderRung({ onScope: () => { throw new Error("x") } })!
    await window.fetch("/x")
    // A rejection reaches the page as the same rejection.
    const err = new TypeError("network down")
    vi.stubGlobal("fetch", () => Promise.reject(err))
    const rejecting = installHeaderRung({ onScope: () => {} })!
    await expect(window.fetch("/x")).rejects.toBe(err)
    rejecting.restore()
    thrower.restore()
    for (const s of spies) expect(s).not.toHaveBeenCalled()
  })
})

// ── Newest scope per path, the distinct list ──────────────────────

/** A conversation with one more turn the next /run will name. */
function addRun(routes: Record<string, Route>, id: string, minute: number) {
  const r = runRow({ id, session_id: "s_a", public_id: "pub_demo", started: `2026-10-01T09:${String(minute).padStart(2, "0")}:00Z` })
  const list = routes["runs?public_id=pub_demo&limit=50"] as { runs: unknown[]; total: number }
  routes["runs?public_id=pub_demo&limit=50"] = { ...list, total: list.total + 1, runs: [r, ...list.runs] }
  routes[`runs/${id}`] = { ...r, children: [] }
  routes[`runs/${id}/events?after=0&limit=500`] = page(runEvents(id))
  routes[`runs/${id}/transcript`] = transcript([user(`question of ${id}`)], [assistant(`answer of ${id}`)])
  routes[`runs/${id}/spans`] = { spans: [] }
}

describe("the panel's detected scopes", () => {
  it("each turn's header re-pins while the user has not clicked; one restart for the conversation; one list entry per conversation", async () => {
    const routes = demoRoutes()
    let answer = "pub_demo;run=r_1"
    routes["POST /run"] = () => scoped(answer)
    const studio = fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": LOCAL, "data-open": "true" })
    await window.fetch("/run", { method: "POST" })
    await settle()
    expect(selected(el)).toBe("r_1")
    addRun(routes, "r_3", 10)
    answer = "pub_demo;run=r_3"
    await window.fetch("/run", { method: "POST" })
    await settle()
    expect(selected(el)).toBe("r_3") // the next turn, pinned
    expect(studio.gets("sessions?public_id=pub_demo")).toHaveLength(2) // the restart's, then the re-read for the unlisted run — no third
    expect(FakeEventSource.instances.filter((i) => i.url.includes("public_id=pub_demo"))).toHaveLength(1) // never restarted
    expect(el.detectedScopes().map((d) => [d.key, d.scope.run, d.path])).toEqual([["pub_demo", "r_3", "/run"]])
  })

  it("a user's click then a new turn: the selection stays where the user put it", async () => {
    const routes = demoRoutes()
    let answer = "pub_demo;run=r_1"
    routes["POST /run"] = () => scoped(answer)
    fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": LOCAL, "data-open": "true" })
    await window.fetch("/run", { method: "POST" })
    await settle()
    click(all(el, ".weft-turn").find((n) => n.textContent.includes("r_2")))
    await settle()
    expect(selected(el)).toBe("r_2")
    addRun(routes, "r_3", 10)
    answer = "pub_demo;run=r_3"
    await window.fetch("/run", { method: "POST" })
    await settle()
    expect(ids(el)).toContain("r_3") // listed…
    expect(selected(el)).toBe("r_2") // …but the user's pick stands
  })

  it("two paths reporting different conversations do not thrash: the followed path's conversation stays, the other is listed", async () => {
    const routes = demoRoutes()
    routes["runs?public_id=pub_other&limit=50"] = { total: 0, runs: [], next_before: null }
    routes["sessions?public_id=pub_other"] = { total: 0, sessions: [], next_before: null }
    routes["/a"] = () => scoped("pub_demo;run=r_1")
    routes["/b"] = () => scoped("pub_other")
    routes["/plain"] = () => scoped(null)
    const studio = fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": LOCAL, "data-open": "true" })
    for (let i = 0; i < 3; i++) {
      await window.fetch("/a")
      await window.fetch("/b")
      await settle()
    }
    await window.fetch("/plain") // no header: nothing moves
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_demo")
    expect(studio.gets("runs?public_id=pub_other")).toHaveLength(0)
    expect(FakeEventSource.instances.filter((i) => i.url.includes("public_id="))).toHaveLength(1)
    const byPath = el.detectedByPath()
    expect([...byPath.keys()].sort()).toEqual(["/a", "/b"])
    expect(byPath.get("/a")).toEqual({ publicId: "pub_demo", run: "r_1" })
    expect(el.detectedScopes().map((d) => [d.key, d.path])).toEqual([
      ["pub_demo", "/a"],
      ["pub_other", "/b"],
    ])
    // The followed path moving to another conversation is followed.
    routes["/a"] = () => scoped("pub_other")
    await window.fetch("/a")
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_other")
  })

  it("an explicit scope wins over a detected one; the rung still records what it saw", async () => {
    const routes = demoRoutes()
    routes["POST /run"] = () => scoped("pub_demo;run=r_1")
    fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": LOCAL, "data-scope": "pub_orders", "data-open": "true" })
    expect(text(el, ".weft-title")).toContain("pub_orders")
    await window.fetch("/run", { method: "POST" })
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_orders")
    expect(el.detectedScopes().map((d) => d.key)).toEqual(["pub_demo"])
  })

  it("the panel's own Studio responses are never a scope source, even when the app's mux sets the header on them", async () => {
    const routes = demoRoutes()
    routes["runs?limit=10"] = () =>
      new Response(JSON.stringify({ total: 0, runs: [], next_before: null }), {
        headers: { "content-type": "application/json", [SCOPE_HEADER]: "pub_demo" },
      })
    fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": LOCAL, "data-open": "true" })
    await window.fetch(`${LOCAL}api/runs?limit=10`) // a request under the endpoint, through the wrapper
    await settle()
    expect(text(el, ".weft-title")).toBe("latest (dev)")
    expect(el.detectedScopes()).toEqual([])
  })
})

// ── Only once Studio answers (review finding 1) ───────────────────

describe("the rung waits for Studio", () => {
  it("a host mount whose Studio does not answer never patches fetch; a retry that finds Studio installs it", async () => {
    let up = false
    fakeStudio(demoRoutes(), () => (up ? META : apiError(503, "down", "down")))
    const original = window.fetch
    const el = await mountWith({ "data-endpoint": LOCAL, "data-open": "true" })
    expect($(el, ".weft-retry")).not.toBeNull() // dormant: the quiet line
    expect(window.fetch).toBe(original)
    up = true
    click($(el, ".weft-retry"))
    await settle()
    expect(window.fetch).not.toBe(original)
    expect(text(el, ".weft-detect")).toContain("detect: headers")
  })

  it("the auto dock is not patched while its probe is in flight", async () => {
    let answer: (v: unknown) => void = () => {}
    fakeStudio(demoRoutes(), () => new Promise((r) => (answer = r)))
    const original = window.fetch
    const el = create({ "data-endpoint": LOCAL, "data-open": "true" })
    el.autoMounted = true
    document.body.appendChild(el)
    await pause(30)
    expect(window.fetch).toBe(original)
    answer(META)
    await settle()
    expect(window.fetch).not.toBe(original)
  })
})

// ── The panel following a full scope ─────────────────────────────

describe("the panel follows the scope (rung 1)", () => {
  it("run pins the selected turn (and its tail) once; the user's click owns it after", async () => {
    const studio = fakeStudio(demoRoutes())
    const el = await mountWith({ "data-endpoint": REMOTE, "data-scope": "pub_demo;run=r_1", "data-open": "true" })
    expect(selected(el)).toBe("r_1")
    expect(studio.gets("runs/r_2/transcript")).toHaveLength(0) // never opened the newest first
    const r2 = all(el, ".weft-turn").find((n) => n.textContent.includes("r_2"))
    click(r2)
    await settle()
    expect(selected(el)).toBe("r_2")
    el.rescan() // a re-read of the same scope is not a re-pin
    await settle()
    expect(selected(el)).toBe("r_2")
  })

  it("a run that is not among the conversation's runs is said, not silently ignored", async () => {
    const studio = fakeStudio(demoRoutes())
    const el = await mountWith({ "data-endpoint": REMOTE, "data-scope": "pub_demo;run=r_9", "data-open": "true" })
    expect($(el, ".weft-pin-missing")).toBeNull() // one look is not enough to say so
    await settle(PIN_RECHECK_MS + 100)
    expect(studio.gets("runs?public_id=pub_demo")).toHaveLength(2) // the one re-read
    expect(text(el, ".weft-pin-missing")).toBe("run r_9 not in this conversation")
    expect(selected(el)).toBe("r_2") // the default selection stands
    el.setAttribute("data-scope", "pub_demo;run=r_1") // same conversation, another pin: re-narrowed in place
    await settle()
    expect($(el, ".weft-pin-missing")).toBeNull()
    expect(selected(el)).toBe("r_1")
  })

  it("a header that lands before its run row says nothing false; the run heard live as it starts is pinned", async () => {
    const routes = demoRoutes()
    fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": REMOTE, "data-scope": "pub_demo;run=r_3", "data-open": "true" })
    expect($(el, ".weft-pin-missing")).toBeNull() // not listed yet: no "not in this conversation"
    const lane = FakeEventSource.last("public_id=pub_demo")!
    lane.opened()
    const r3 = runRow({ id: "r_3", session_id: "s_a", public_id: "pub_demo", status: "running", started: "2026-10-01T09:09:00Z" })
    routes["runs/r_3"] = { ...r3, children: [] }
    routes["runs/r_3/events?after=0&limit=500"] = page([])
    routes["runs/r_3/transcript"] = transcript([user("third")])
    routes["runs/r_3/spans"] = { spans: [] }
    lane.emit("run", { run: r3 })
    await settle()
    expect(selected(el)).toBe("r_3")
    expect($(el, ".weft-pin-missing")).toBeNull()
  })

  it("session narrows the turn list when the runs carry a session id; flow is a chip that filters nothing", async () => {
    fakeStudio(demoRoutes())
    const el = await mountWith({ "data-endpoint": REMOTE, "data-scope": "pub_demo;session=s_a;flow=f_1", "data-open": "true" })
    expect(ids(el)).toEqual(["r_1"])
    expect(all(el, ".weft-scope-chip").map((n) => n.textContent)).toEqual(["session s_a", "flow f_1"])
    el.setAttribute("data-scope", "pub_demo;flow=f_1")
    await settle()
    expect(ids(el)).toEqual(["r_2", "r_1"]) // flow alone filters nothing
  })

  it("a session the runs do not record is said, and the list is not narrowed", async () => {
    const routes = demoRoutes()
    routes["runs?public_id=pub_demo&limit=50"] = {
      total: 1,
      runs: [runRow({ id: "r_1", session_id: "", public_id: "pub_demo" })],
      next_before: null,
    }
    fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": REMOTE, "data-scope": "pub_demo;session=s_a", "data-open": "true" })
    expect(ids(el)).toEqual(["r_1"])
    expect(text(el, ".weft-turns")).toContain("session s_a: these runs carry no session id — not narrowed")
  })
})

// ── Rung 1's explicit forms ───────────────────────────────────────

describe("the explicit scope forms", () => {
  it("data-scope at each rung of C2's ladder, and above data-public-id at the same rung", () => {
    const meta = document.createElement("meta")
    meta.name = "weft:scope"
    meta.content = "pub_meta;session=s_m"
    script({ "data-weft": "", "data-scope": "pub_script;run=r_s" })
    expect(readConfig().scope).toEqual({ publicId: "pub_script", run: "r_s" })
    document.head.appendChild(meta)
    expect(readConfig().scope).toEqual({ publicId: "pub_meta", session: "s_m" })
    const el = create({ "data-public-id": "pub_el", "data-scope": "pub_el2;flow=f" })
    expect(readConfig(el).scope).toEqual({ publicId: "pub_el2", flow: "f" })
    expect(readConfig(el).publicId).toBe("pub_el2")
    const legacy = create({ "data-public-id": "pub_legacy" })
    expect(readConfig(legacy).scope).toEqual({ publicId: "pub_legacy" }) // deprecated, still read
    ;(legacy).options = { scope: { publicId: "pub_opt", run: "r_o" } }
    expect(readConfig(legacy).scope).toEqual({ publicId: "pub_opt", run: "r_o" })
    expect(readConfig(legacy).scopeExplicit).toBe(true)
  })

  it("window.__WEFT__ = {scope} (a string or a Scope), else {publicId}", () => {
    const w = window as { __WEFT__?: unknown }
    w.__WEFT__ = { scope: "pub_w;run=r_w" }
    expect(readConfig().scope).toEqual({ publicId: "pub_w", run: "r_w" })
    w.__WEFT__ = { scope: { publicId: "pub_o", session: "s_o" } }
    expect(readConfig().scope).toEqual({ publicId: "pub_o", session: "s_o" })
    w.__WEFT__ = { publicId: "pub_p" }
    expect(readConfig().scope).toEqual({ publicId: "pub_p" })
    w.__WEFT__ = {}
    expect(readConfig().scopeExplicit).toBe(false)
  })

  it("data-detect resolves through the same ladder", () => {
    script({ "data-weft": "", "data-detect": "headers" })
    expect(readConfig().detect).toBe("headers")
    const meta = document.createElement("meta")
    meta.name = "weft:detect"
    meta.content = "off"
    document.head.appendChild(meta)
    expect(readConfig().detect).toBe("off")
    expect(readConfig(create({ "data-detect": "bogus" })).detect).toBe("")
  })
})
