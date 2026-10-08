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
import { mount as mountPanel } from "./element"
import {
  $,
  all,
  baseRoutes,
  click,
  create,
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
    expect(el.detectedScopes().map((d) => d.key)).toEqual(["pub_demo;run=r_1"])
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
    expect(text(el, ".weft-detect")).toContain("detect: explicit")
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

describe("the panel's detected scopes", () => {
  it("follows the newest scope seen, keeps the newest per path and lists every distinct one (for C3.3's switcher)", async () => {
    const routes = demoRoutes()
    routes["runs?public_id=pub_other&limit=50"] = { total: 0, runs: [], next_before: null }
    routes["sessions?public_id=pub_other"] = { total: 0, sessions: [], next_before: null }
    let answer = "pub_demo;run=r_1"
    routes["POST /run"] = () => scoped(answer)
    routes["POST /chat"] = () => scoped(answer)
    routes["/plain"] = () => scoped(null)
    fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": LOCAL, "data-open": "true" })
    await window.fetch("/run", { method: "POST" })
    await settle()
    answer = "pub_other"
    await window.fetch("/chat", { method: "POST" })
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_other") // the newest wins
    await window.fetch("/plain") // no header: nothing moves
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_other")
    answer = "pub_demo;run=r_2"
    await window.fetch("/run", { method: "POST" })
    await settle()
    expect(text(el, ".weft-title")).toContain("pub_demo")
    expect(selected(el)).toBe("r_2")
    answer = "pub_demo;run=r_1"
    await window.fetch("/chat", { method: "POST" })
    await settle()
    const byPath = el.detectedByPath()
    expect([...byPath.keys()].sort()).toEqual(["/chat", "/run"])
    expect(byPath.get("/run")).toEqual({ publicId: "pub_demo", run: "r_2" })
    expect(byPath.get("/chat")).toEqual({ publicId: "pub_demo", run: "r_1" })
    // Distinct by serialised form, first-seen order, newest path each.
    expect(el.detectedScopes().map((d) => [d.key, d.path])).toEqual([
      ["pub_demo;run=r_1", "/chat"],
      ["pub_other", "/chat"],
      ["pub_demo;run=r_2", "/run"],
    ])
    expect(selected(el)).toBe("r_1")
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
    expect(el.detectedScopes().map((d) => d.key)).toEqual(["pub_demo;run=r_1"])
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
    fakeStudio(demoRoutes())
    const el = await mountWith({ "data-endpoint": REMOTE, "data-scope": "pub_demo;run=r_9", "data-open": "true" })
    expect(text(el, ".weft-pin-missing")).toBe("run r_9 not in this conversation")
    expect(selected(el)).toBe("r_2") // the default selection stands
    el.setAttribute("data-scope", "pub_demo;run=r_1") // same conversation, another pin: re-narrowed in place
    await settle()
    expect($(el, ".weft-pin-missing")).toBeNull()
    expect(selected(el)).toBe("r_1")
  })

  it("the scope's run heard live as it starts is pinned", async () => {
    const routes = demoRoutes()
    fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": REMOTE, "data-scope": "pub_demo;run=r_3", "data-open": "true" })
    expect(text(el, ".weft-pin-missing")).toContain("r_3")
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
