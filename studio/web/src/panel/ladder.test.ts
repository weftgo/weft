// Plan C2: configuration without a filename regex. Every field
// resolves on its own through one ladder — mount(opts) → the
// <weft-devtools> element's attributes → <meta name="weft:…"> → the
// script tag (document.currentScript, else data-weft, any src) →
// panel-config.json next to the script → the script's own directory —
// and a mount the host made says "Studio not reachable at … · retry"
// where the panel's own dock removes itself silently.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { discoverEndpoint, findPanelScript, readConfig } from "./config"
import type { MountOptions } from "./config"
import { mount as mountPanel, WeftDevtools } from "./element"
import { $, baseRoutes, click, fakeStudio, json, META, settle, setup, teardown, text } from "./testkit"

const RENAMED = "https://proxy.example/assets/devtools.abc123.js"

function script(attrs: Record<string, string>, src = RENAMED): HTMLScriptElement {
  const s = document.createElement("script")
  s.type = "module"
  if (src) s.setAttribute("src", src)
  for (const [k, v] of Object.entries(attrs)) s.setAttribute(k, v)
  document.head.appendChild(s)
  return s
}

function meta(name: string, content: string) {
  const m = document.createElement("meta")
  m.name = `weft:${name}`
  m.content = content
  document.head.appendChild(m)
}

function element(attrs: Record<string, string>, options: MountOptions | null = null): WeftDevtools {
  if (!customElements.get("weft-devtools")) customElements.define("weft-devtools", WeftDevtools)
  const el = document.createElement("weft-devtools") as WeftDevtools
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v)
  el.options = options
  return el
}

beforeEach(setup)
afterEach(() => {
  teardown()
  document.head.querySelectorAll("script, meta").forEach((n) => n.remove())
  vi.restoreAllMocks()
})

// ── The ladder, one case per rung ─────────────────────────────────

/** The rungs a case sets, each with its own value for the field. */
type Rung = 1 | 2 | 3 | 4
interface Case {
  name: string
  set: Rung[]
  want: string
}

/** stage writes the field's value into each rung the case sets
 * (rung 1: mount options, 2: the element, 3: a meta tag, 4: the
 * data-weft script tag) and reads the configuration back. */
function stage(
  field: "endpoint" | "token" | "public-id" | "position" | "open" | "auto",
  set: Rung[],
  values: Record<Rung, string>
) {
  const scriptAttrs: Record<string, string> = { "data-weft": "" }
  if (set.includes(4)) scriptAttrs[`data-${field}`] = values[4]
  script(scriptAttrs)
  if (set.includes(3)) meta(field, values[3])
  const attrs: Record<string, string> = {}
  if (set.includes(2)) attrs[`data-${field}`] = values[2]
  const key = { endpoint: "endpoint", token: "token", "public-id": "publicId", position: "position", open: "open", auto: "auto" }[field]
  // mount(opts) takes booleans for open and auto.
  const v1 = field === "open" || field === "auto" ? values[1] === "true" : values[1]
  const options = set.includes(1) ? ({ [key]: v1 } as MountOptions) : null
  return readConfig(element(attrs, options))
}

describe("the configuration ladder (C2): each field, one case per rung", () => {
  const endpoints: Record<Rung, string> = {
    1: "http://opts.test/studio",
    2: "http://attr.test/studio",
    3: "http://meta.test/studio",
    4: "http://script.test/studio",
  }
  const cases: Case[] = [
    { name: "1. mount(opts) wins over every rung below", set: [1, 2, 3, 4], want: "http://opts.test/studio/" },
    { name: "2. the element's attribute, when opts set none", set: [2, 3, 4], want: "http://attr.test/studio/" },
    { name: "3. <meta name=weft:endpoint>, when neither does", set: [3, 4], want: "http://meta.test/studio/" },
    { name: "4. the data-weft script tag's attribute", set: [4], want: "http://script.test/studio/" },
    { name: "1 over 3: a gap in the middle is skipped", set: [1, 3], want: "http://opts.test/studio/" },
    // Rungs 5 and 6: nothing named it — the script's own directory, with
    // panel-config.json beside it to ask (the async rung, below).
    { name: "5/6. none: the script's directory, panel-config.json beside it", set: [], want: "https://proxy.example/assets/" },
  ]
  for (const c of cases) {
    it(`endpoint — ${c.name}`, () => {
      const cfg = stage("endpoint", c.set, endpoints)
      expect(cfg.endpoint).toBe(c.want)
      expect(cfg.endpointExplicit).toBe(c.set.length > 0)
      expect(cfg.configURL).toBe(c.set.length ? "" : "https://proxy.example/assets/panel-config.json")
    })
  }

  const tokens: Record<Rung, string> = { 1: "tok_opts", 2: "tok_attr", 3: "tok_meta", 4: "tok_script" }
  const tokenCases: Case[] = [
    { name: "1. mount(opts)", set: [1, 2, 3, 4], want: "tok_opts" },
    { name: "2. the element", set: [2, 3, 4], want: "tok_attr" },
    { name: "3. the meta tag", set: [3, 4], want: "tok_meta" },
    { name: "4. the script tag", set: [4], want: "tok_script" },
    // Rungs 5 and 6 never carry a token: panel-config.json has none.
    { name: "5/6. none: no token", set: [], want: "" },
  ]
  for (const c of tokenCases) {
    it(`token — ${c.name}`, () => {
      expect(stage("token", c.set, tokens).token).toBe(c.want)
    })
  }

  const ids: Record<Rung, string> = { 1: "pub_opts", 2: "pub_attr", 3: "pub_meta", 4: "pub_script" }
  for (const c of [
    { name: "1", set: [1, 2, 3, 4], want: "pub_opts" },
    { name: "2", set: [2, 3, 4], want: "pub_attr" },
    { name: "3", set: [3, 4], want: "pub_meta" },
    { name: "4", set: [4], want: "pub_script" },
  ] as Case[]) {
    it(`public id — rung ${c.name}`, () => {
      expect(stage("public-id", c.set, ids).publicId).toBe(c.want)
    })
  }

  const positions: Record<Rung, string> = { 1: "right-dock", 2: "bottom-left", 3: "right-dock", 4: "bottom-left" }
  for (const c of [
    { name: "1", set: [1, 2], want: "right-dock" },
    { name: "2", set: [2, 3], want: "bottom-left" },
    { name: "3", set: [3, 4], want: "right-dock" },
    { name: "4", set: [4], want: "bottom-left" },
    { name: "none: the default", set: [], want: "bottom-right" },
  ] as Case[]) {
    it(`position — rung ${c.name}`, () => {
      expect(stage("position", c.set, positions).position).toBe(c.want)
    })
  }

  const flags = (field: "open" | "auto", set: Rung[], v: string) =>
    stage(field, set, { 1: v, 2: v, 3: v, 4: v } as Record<Rung, string>)
  for (const c of [
    { rung: "1", set: [1] as Rung[] },
    { rung: "2", set: [2] as Rung[] },
    { rung: "3", set: [3] as Rung[] },
    { rung: "4", set: [4] as Rung[] },
  ]) {
    it(`open — rung ${c.rung}: "true" opens; a higher rung's "false" wins over a lower "true"`, () => {
      expect(flags("open", c.set, "true").open).toBe(true)
      document.head.querySelectorAll("script, meta").forEach((n) => n.remove())
      const lower = (c.set[0] + 1) as Rung
      if (lower <= 4) {
        const vals = { 1: "true", 2: "true", 3: "true", 4: "true" } as Record<Rung, string>
        vals[c.set[0]] = "false"
        expect(stage("open", [c.set[0], lower], vals).open).toBe(false)
      }
    })
    it(`auto — rung ${c.rung}: "false" turns the auto mount off; a higher rung's "true" wins over a lower "false"`, () => {
      expect(flags("auto", c.set, "false").auto).toBe(false)
      document.head.querySelectorAll("script, meta").forEach((n) => n.remove())
      const lower = (c.set[0] + 1) as Rung
      if (lower <= 4) {
        const vals = { 1: "false", 2: "false", 3: "false", 4: "false" } as Record<Rung, string>
        vals[c.set[0]] = "true"
        expect(stage("auto", [c.set[0], lower], vals).auto).toBe(true)
      }
    })
  }

  it("public id — none at any rung: window.__WEFT__.publicId", () => {
    ;(window as { __WEFT__?: unknown }).__WEFT__ = { publicId: "pub_weft" }
    try {
      expect(stage("public-id", [], ids).publicId).toBe("pub_weft")
      document.head.querySelectorAll("script, meta").forEach((n) => n.remove())
      expect(stage("public-id", [4], ids).publicId).toBe("pub_script") // any rung beats it
    } finally {
      delete (window as { __WEFT__?: unknown }).__WEFT__
    }
  })

  it("an empty endpoint at any rung is unset: it falls through to the next rung, and to panel-config.json", () => {
    script({ "data-weft": "", "data-endpoint": "http://script.test/studio" })
    meta("endpoint", "")
    let cfg = readConfig(element({ "data-endpoint": "" }, { endpoint: "" }))
    expect(cfg.endpoint).toBe("http://script.test/studio/")
    document.head.querySelectorAll("script, meta").forEach((n) => n.remove())
    script({ "data-weft": "", "data-endpoint": " " })
    cfg = readConfig(element({ "data-endpoint": "" }))
    expect(cfg.endpointExplicit).toBe(false)
    expect(cfg.endpoint).toBe("https://proxy.example/assets/")
    expect(cfg.configURL).toBe("https://proxy.example/assets/panel-config.json")
  })

  it("fields resolve independently: the token from a meta tag, the endpoint from the script tag, the scope from the element", () => {
    script({ "data-weft": "", "data-endpoint": "http://script.test/studio/" })
    meta("token", "tok_meta")
    meta("open", "true")
    const cfg = readConfig(element({ "data-public-id": "pub_attr" }))
    expect(cfg.endpoint).toBe("http://script.test/studio/")
    expect(cfg.token).toBe("tok_meta")
    expect(cfg.publicId).toBe("pub_attr")
    expect(cfg.open).toBe(true)
  })

  it("weft:auto=false on a meta tag turns the auto mount off", () => {
    meta("auto", "false")
    expect(readConfig().auto).toBe(false)
  })
})

// ── Finding the panel's own script tag ────────────────────────────

describe("the panel's script tag (C2: never by its file name)", () => {
  it("a renamed bundle behind a proxy, marked data-weft, is found and its attributes read", () => {
    const tag = script({ "data-weft": "", "data-public-id": "pub_7", "data-token": "tok" })
    expect(findPanelScript()).toBe(tag)
    const cfg = readConfig()
    expect(cfg.publicId).toBe("pub_7")
    expect(cfg.endpoint).toBe("https://proxy.example/assets/")
  })

  it("document.currentScript null (a module script): the data-weft scan finds it", () => {
    expect(document.currentScript).toBeNull()
    script({}, "/js/app.js") // the host's own
    const tag = script({ "data-weft": "" })
    expect(findPanelScript()).toBe(tag)
  })

  it("several data-weft scripts: the first in document order wins", () => {
    const first = script({ "data-weft": "", "data-public-id": "pub_first" }, "/a/one.js")
    script({ "data-weft": "", "data-public-id": "pub_second" }, "/b/two.js")
    expect(findPanelScript()).toBe(first)
    expect(readConfig().publicId).toBe("pub_first")
  })

  it("the running classic script (document.currentScript) is the panel's, whatever it is called", async () => {
    script({ "data-weft": "", "data-public-id": "pub_other" }, "/other.js")
    const mine = script({ "data-public-id": "pub_mine" }, "/cdn/x9f.js")
    Object.defineProperty(document, "currentScript", { configurable: true, get: () => mine })
    try {
      vi.resetModules()
      const fresh = await import("./config")
      expect(fresh.findPanelScript()).toBe(mine)
      expect(fresh.readConfig().publicId).toBe("pub_mine")
    } finally {
      delete (document as { currentScript?: unknown }).currentScript // the prototype's getter again
    }
  })

  it("a tag written before data-weft (panel.js with data-public-id) still configures it; a bare host script does not", () => {
    script({}, "/js/app.js")
    const tag = script({ "data-public-id": "pub_demo" }, "/studio/panel.js")
    expect(findPanelScript()).toBe(tag)
    tag.remove()
    expect(findPanelScript()).toBeNull()
  })
})

// ── Rung 5: panel-config.json ─────────────────────────────────────

describe("panel-config.json (rung 5)", () => {
  const stub = (body: unknown, status = 200) => {
    const f = vi.fn(async (_u: RequestInfo | URL, _i?: RequestInit) => json(body, status))
    vi.stubGlobal("fetch", f)
    return f
  }

  it("names the endpoint: same origin, normalized; no token, no credentials go with the request", async () => {
    const f = stub({ endpoint: "https://proxy.example/studio", version: "v0.7.0", capabilities: [], token: "nope" })
    expect(await discoverEndpoint("https://proxy.example/assets/panel-config.json")).toBe("https://proxy.example/studio/")
    const init = f.mock.calls[0][1] as RequestInit
    expect((init.headers as Record<string, string>).Authorization).toBeUndefined()
    expect(init.credentials).toBe("omit")
  })

  it("another origin, a 404, not JSON, not http(s): no endpoint (the caller falls to the script's directory)", async () => {
    stub({ endpoint: "https://elsewhere.example/studio/" })
    expect(await discoverEndpoint("https://proxy.example/assets/panel-config.json")).toBe("")
    stub({ endpoint: "javascript:alert(1)" })
    expect(await discoverEndpoint("https://proxy.example/assets/panel-config.json")).toBe("")
    stub({ error: {} }, 404)
    expect(await discoverEndpoint("https://proxy.example/assets/panel-config.json")).toBe("")
    vi.stubGlobal("fetch", vi.fn(async () => new Response("<html>", { status: 200 })))
    expect(await discoverEndpoint("https://proxy.example/assets/panel-config.json")).toBe("")
    vi.stubGlobal("fetch", vi.fn(async () => { throw new TypeError("offline") }))
    expect(await discoverEndpoint("https://proxy.example/assets/panel-config.json")).toBe("")
  })
})

// ── The renamed bundle end to end (the Done line) ─────────────────

/** Where the panel asked for meta, and with what. */
const metaURLs = (s: ReturnType<typeof fakeStudio>) =>
  s.fetchMock.mock.calls.map((c) => String(c[0])).filter((u) => u.endsWith("/api/meta"))

describe("a bundle renamed to devtools.abc123.js behind a proxy finds its config", () => {
  it("endpoint from the script tag's attributes", async () => {
    script({ "data-weft": "", "data-endpoint": "https://proxy.example/studio/", "data-public-id": "pub_orders" })
    const studio = fakeStudio(baseRoutes())
    const el = mountPanel({ open: true })
    await settle()
    expect(metaURLs(studio)).toEqual(["https://proxy.example/studio/api/meta"])
    expect(studio.calls.some((c) => c.path.endsWith("panel-config.json"))).toBe(false) // explicit: never asked
    expect(text(el, ".weft-title")).toContain("pub_orders")
  })

  it("endpoint from /assets/panel-config.json next to it", async () => {
    script({ "data-weft": "", "data-public-id": "pub_orders" })
    const studio = fakeStudio({
      ...baseRoutes(),
      "/assets/panel-config.json": { endpoint: "https://proxy.example/studio", version: "v0.2.1", capabilities: [] },
    })
    const el = mountPanel({ open: true })
    await settle()
    expect(studio.fetchMock.mock.calls[0][0]).toBe("https://proxy.example/assets/panel-config.json")
    expect(metaURLs(studio)).toEqual(["https://proxy.example/studio/api/meta"])
    // The deep links follow the discovered endpoint.
    expect($(el, "a")?.getAttribute("href")?.startsWith("https://proxy.example/studio/")).toBe(true)
  })

  it("endpoint from its own directory, the last rung", async () => {
    script({ "data-weft": "", "data-public-id": "pub_orders" })
    const studio = fakeStudio(baseRoutes()) // panel-config.json: 404
    mountPanel({ open: true })
    await settle()
    expect(metaURLs(studio)).toEqual(["https://proxy.example/assets/api/meta"])
  })

  it("panel-config.json never overrides an explicit endpoint, never supplies a token, never moves the token to another origin", async () => {
    // Explicit (a meta tag): panel-config.json is not even asked.
    meta("endpoint", "https://proxy.example/studio/")
    script({ "data-weft": "" })
    let studio = fakeStudio({ ...baseRoutes(), "/assets/panel-config.json": { endpoint: "https://proxy.example/x/" } })
    let el = mountPanel()
    await settle()
    expect(metaURLs(studio)).toEqual(["https://proxy.example/studio/api/meta"])
    expect(studio.calls.some((c) => c.path.endsWith("panel-config.json"))).toBe(false)
    el.remove()
    document.head.querySelectorAll("meta").forEach((n) => n.remove())

    // Not explicit, a token from the script tag: the file names
    // another origin and carries a token of its own — both refused.
    studio = fakeStudio({
      ...baseRoutes(),
      "/assets/panel-config.json": { endpoint: "https://evil.example/studio/", token: "evil_tok" },
    })
    document.head.querySelector("script")!.setAttribute("data-token", "tok_secret")
    el = mountPanel()
    await settle()
    expect(metaURLs(studio)).toEqual(["https://proxy.example/assets/api/meta"])
    for (const [u, init] of studio.fetchMock.mock.calls) {
      const auth = ((init?.headers ?? {}) as Record<string, string>).Authorization
      if (String(u).endsWith("panel-config.json")) expect(auth).toBeUndefined()
      else {
        expect(new URL(String(u)).origin).toBe("https://proxy.example")
        expect(auth).toBe("Bearer tok_secret")
      }
    }
  })
})

// ── Studio not answering ──────────────────────────────────────────

describe("no Studio: a line for the host's mount, silence for the panel's own", () => {
  const consoleSpies = () =>
    (["log", "info", "warn", "error", "debug"] as const).map((m) =>
      vi.spyOn(console, m).mockImplementation(() => {})
    )

  it("mount(opts): 'Studio not reachable at <endpoint> · retry'; retry probes again and the panel comes up", async () => {
    const spies = consoleSpies()
    let up = false
    const studio = fakeStudio(baseRoutes(), () => (up ? META : new Response("no studio", { status: 404 })))
    const el = mountPanel({ endpoint: "http://studio.test/studio", publicId: "pub_orders", open: true })
    await settle()
    expect(el.isConnected).toBe(true)
    const line = text(el, ".weft-unreachable")
    expect(line).toContain("Studio not reachable at http://studio.test/studio/")
    expect(line).toContain("retry")
    expect(studio.gets("meta")).toHaveLength(1)
    // Still down: retry asks once more, the line stays.
    click($(el, ".weft-unreachable button"))
    await settle()
    expect(studio.gets("meta")).toHaveLength(2)
    expect($(el, ".weft-unreachable")).toBeTruthy()
    // Studio is up: retry brings the panel.
    up = true
    click($(el, ".weft-unreachable button"))
    await settle()
    expect(studio.gets("meta")).toHaveLength(3)
    expect($(el, ".weft-unreachable")).toBeNull()
    expect(text(el, ".weft-title")).toContain("pub_orders")
    for (const s of spies) expect(s).not.toHaveBeenCalled()
  })

  it("a retry keeps the line (checking…) while the probe is in flight — never the fab, never an empty dock", async () => {
    let release: () => void = () => {}
    let held = false
    const studio = fakeStudio(baseRoutes(), async () => {
      if (!held) return new Response("no studio", { status: 404 })
      await new Promise<void>((r) => (release = r))
      return new Response("no studio", { status: 404 })
    })
    for (const open of [false, true]) {
      held = false
      const el = mountPanel({ endpoint: "http://studio.test/studio/", open })
      await settle()
      expect($(el, ".weft-unreachable button")).toBeTruthy()
      held = true
      click($(el, ".weft-unreachable button"))
      await new Promise((r) => setTimeout(r, 20)) // the probe is in flight
      expect(text(el, ".weft-unreachable")).toBe("Studio not reachable at http://studio.test/studio/ · checking…")
      expect($(el, ".weft-fab, .weft-dock")).toBeNull()
      release()
      await settle()
      expect(text(el, ".weft-unreachable")).toContain("· retry")
      expect($(el, ".weft-fab, .weft-dock")).toBeNull()
      el.remove()
    }
    expect(studio.gets("meta")).toHaveLength(4)
  })

  it("retry re-reads the configuration: a meta tag changed after the first failure is used", async () => {
    meta("endpoint", "http://down.test/studio/")
    const studio = fakeStudio(baseRoutes())
    // The first request (meta at down.test) gets no Studio.
    studio.fetchMock.mockImplementationOnce(async () => new Response("no studio", { status: 404 }))
    const el = mountPanel({ open: true, publicId: "pub_orders" })
    await settle()
    expect(text(el, ".weft-unreachable")).toContain("http://down.test/studio/")
    document.head.querySelector("meta")!.setAttribute("content", "http://up.test/studio/")
    click($(el, ".weft-unreachable button"))
    await settle()
    expect($(el, ".weft-unreachable")).toBeNull()
    expect(metaURLs(studio)).toEqual(["http://down.test/studio/api/meta", "http://up.test/studio/api/meta"])
    expect(text(el, ".weft-title")).toContain("pub_orders")
  })

  it("markup with data-auto=false shows the line too", async () => {
    fakeStudio(baseRoutes(), new Response("no studio", { status: 404 }))
    const el = element({ "data-endpoint": "http://studio.test/studio/", "data-auto": "false" })
    document.body.appendChild(el)
    await settle()
    expect(text(el, ".weft-unreachable")).toContain("Studio not reachable at http://studio.test/studio/")
  })

  it("the panel's own auto-mounted dock removes itself silently: no line, no console", async () => {
    const spies = consoleSpies()
    script({ "data-weft": "" })
    const studio = fakeStudio(baseRoutes(), new Response("no studio", { status: 404 }))
    const dock = element({})
    dock.autoMounted = true
    document.body.appendChild(dock)
    await settle()
    expect(dock.isConnected).toBe(false)
    expect(document.querySelector("weft-devtools")).toBeNull()
    expect(studio.gets("meta")).toHaveLength(1)
    // At most two requests: panel-config.json, then meta.
    expect(studio.fetchMock).toHaveBeenCalledTimes(2)
    for (const s of spies) expect(s).not.toHaveBeenCalled()
  })

  it("a held panel-config.json is bounded: aborted after 3 s, then the script's directory", async () => {
    vi.useFakeTimers()
    script({ "data-weft": "" })
    let aborted = false
    const studio = fakeStudio({
      ...baseRoutes(),
      "/assets/panel-config.json": (init?: RequestInit) =>
        new Promise((_, reject) =>
          init?.signal?.addEventListener("abort", () => {
            aborted = true
            reject(new DOMException("aborted", "AbortError"))
          })
        ),
    })
    const el = mountPanel()
    await vi.advanceTimersByTimeAsync(2_900)
    expect(metaURLs(studio)).toEqual([])
    await vi.advanceTimersByTimeAsync(200)
    expect(aborted).toBe(true)
    expect(metaURLs(studio)).toEqual(["https://proxy.example/assets/api/meta"])
    vi.useRealTimers()
    el.remove()
  })

  it("removing the element aborts its panel-config.json request", async () => {
    script({ "data-weft": "" })
    let aborted = false
    const studio = fakeStudio({
      ...baseRoutes(),
      "/assets/panel-config.json": (init?: RequestInit) =>
        new Promise((_, reject) =>
          init?.signal?.addEventListener("abort", () => {
            aborted = true
            reject(new DOMException("aborted", "AbortError"))
          })
        ),
    })
    const el = mountPanel()
    await new Promise((r) => setTimeout(r, 10))
    el.remove()
    await settle()
    expect(aborted).toBe(true)
    expect(metaURLs(studio)).toEqual([]) // nothing after the disconnect
  })
})
