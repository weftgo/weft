// Plan C3.4: detection rung 4 — the page URL (?weft_scope= / #weft_scope=,
// the hand-off Studio's dev links write) — the live fallback when no
// rung names a scope, and the collapsed pill's activity signal. The URL
// is read and listened to (hashchange, popstate), never written; it is
// never gated by data-detect or a token; it sits below the explicit
// forms and above the marker and header rungs.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { pageURL, readConfig, urlScope } from "./config"
import { SCOPE_HEADER } from "./detect"
import { HOW_TO_SCOPE, NO_SCOPE_LABEL, WeftDevtools } from "./element"
import type { PanelModel } from "./state"
import { MARKER_ATTR } from "./markers"
import { parseScope } from "../lib/scope"
import type { Scope } from "../lib/scope"
import {
  $,
  all,
  apiError,
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
  T0,
  teardown,
  text,
  transcript,
  user,
  assistant,
} from "./testkit"
import type { Route } from "./testkit"

const golden = JSON.parse(readFileSync(resolve(process.cwd(), "../testdata/scope.golden.json"), "utf8")) as {
  roundtrip: { scope: Scope; string: string }[]
}

/** A Studio off loopback: the header rung off by default. */
const REMOTE = "https://studio.example/studio/"
/** A Studio on loopback: the header rung on by default. */
const LOCAL = `${location.origin}/studio/`
/** A read-scoped panel token (setup C's default mint). */
const PANEL_TOKEN = `weft_pt.${btoa(JSON.stringify({ public_id: "pub_a", exp: "2099-01-01T00:00:00Z" })).replace(/=+$/, "")}.sig`

/** routesFor: a Studio that knows one finished turn of each public id,
 * and a dev list of those turns (one agent). */
function routesFor(...ids: string[]): Record<string, Route> {
  const routes: Record<string, Route> = { ...baseRoutes() }
  const rows: ReturnType<typeof runRow>[] = []
  ids.forEach((pid, i) => {
    const r = runRow({ id: `r_${pid}`, public_id: pid, session_id: `s_${pid}`, started: `2026-10-01T09:0${i}:00Z` })
    rows.unshift(r)
    routes[`sessions?public_id=${pid}`] = { total: 1, sessions: [], next_before: null }
    routes[`runs?public_id=${pid}&limit=50`] = { total: 1, runs: [r], next_before: null }
    routes[`runs/${r.id}`] = { ...r, children: [] }
    routes[`runs/${r.id}/events?after=0&limit=500`] = page(runEvents(r.id))
    routes[`runs/${r.id}/transcript`] = transcript([user(`q ${pid}`)], [assistant(`a ${pid}`)])
    routes[`runs/${r.id}/spans`] = { spans: [] }
  })
  routes["runs?limit=10"] = { total: rows.length, runs: rows, next_before: null }
  return routes
}

/** setURL moves the page's own URL (search and hash) without a reload. */
const setURL = (rest: string) => history.replaceState(null, "", `/${rest}`)

async function mountWith(attrs: Record<string, string>): Promise<WeftDevtools> {
  const el = create(attrs)
  document.body.appendChild(el)
  await settle()
  return el
}

/** following is the scope the panel's model follows now. */
const following = (el: WeftDevtools): Scope => (el as unknown as { model: PanelModel }).model.following

function chat(scope: string): HTMLElement {
  const box = document.createElement("section")
  box.setAttribute(MARKER_ATTR, scope)
  box.appendChild(document.createElement("input"))
  document.body.appendChild(box)
  return box
}

const consoleSpies = () =>
  (["log", "info", "warn", "error", "debug"] as const).map((m) => vi.spyOn(console, m).mockImplementation(() => {}))

beforeEach(setup)
afterEach(() => {
  teardown()
  setURL("")
  document.head.querySelectorAll("script, meta").forEach((n) => n.remove())
  vi.restoreAllMocks()
})

// ── Rung 4: the URL ───────────────────────────────────────────────

describe("C3's Done line, the URL leg of the flow round-trip", () => {
  it("#weft_scope=pub_x%3Bflow%3Df_1 scopes the panel: its followed scope carries flow f_1 and the header chip shows it", async () => {
    fakeStudio(routesFor("pub_x"))
    setURL("#weft_scope=pub_x%3Bflow%3Df_1")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(following(el)).toEqual(parseScope("pub_x;flow=f_1"))
    expect(following(el)).toEqual({ publicId: "pub_x", flow: "f_1" })
    expect(text(el, ".weft-title")).toContain("pub_x")
    expect(all(el, ".weft-scope-chip").map((n) => n.textContent)).toEqual(["flow f_1"])
    expect(text(el, ".weft-detect")).toBe(" · detect: url+markers")
  })

  it("?weft_scope= scopes it the same way", async () => {
    fakeStudio(routesFor("pub_x"))
    setURL("?weft_scope=pub_x%3Bflow%3Df_1")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(following(el)).toEqual({ publicId: "pub_x", flow: "f_1" })
    expect(all(el, ".weft-scope-chip").map((n) => n.textContent)).toEqual(["flow f_1"])
  })

  it("every golden row with a public id reaches urlScope unchanged, from the query and from the fragment", () => {
    const rows = golden.roundtrip.filter((r) => r.scope.publicId)
    expect(rows.some((r) => r.scope.flow)).toBe(true)
    for (const r of rows) {
      const v = encodeURIComponent(r.string)
      expect(urlScope(`http://localhost/?weft_scope=${v}`)).toEqual(parseScope(r.string))
      expect(urlScope(`http://localhost/#weft_scope=${v}`)).toEqual(r.scope)
    }
  })

  it("urlScope: search first, then hash; no public id is no scope; other parameters are left alone", () => {
    expect(urlScope("http://localhost/?weft_scope=pub_q#weft_scope=pub_h")).toEqual({ publicId: "pub_q" })
    expect(urlScope("http://localhost/?weft_scope=%3Brun%3Dr_1#weft_scope=pub_h")).toEqual({ publicId: "pub_h" })
    expect(urlScope("http://localhost/?a=1&weft_scope=pub_q;run=r+1&b=2")).toEqual({ publicId: "pub_q", run: "r+1" })
    expect(urlScope("http://localhost/#tab=2&weft_scope=pub_h")).toEqual({ publicId: "pub_h" })
    expect(urlScope("http://localhost/?weft_scope=")).toBeNull()
    expect(urlScope("http://localhost/?weft_scopes=pub_q")).toBeNull()
    expect(urlScope("http://localhost/")).toBeNull()
    expect(urlScope("not a url")).toBeNull()
    expect(urlScope("http://localhost/?weft_scope=%E0%A4%A")).toEqual({ publicId: "%E0%A4%A" })
  })
})

describe("the ladder's order: explicit > URL > marker > header", () => {
  const cases: [string, "explicit" | "url" | "marker", "url" | "marker" | "header"][] = [
    ["explicit over URL", "explicit", "url"],
    ["explicit over a marker", "explicit", "marker"],
    ["explicit over a header", "explicit", "header"],
    ["URL over a marker", "url", "marker"],
    ["URL over a header", "url", "header"],
    ["a marker over a header", "marker", "header"],
  ]
  for (const [name, win, lose] of cases) {
    it(name, async () => {
      const routes = routesFor("pub_w", "pub_l")
      routes["POST /chat"] = () => new Response("ok", { status: 200, headers: { [SCOPE_HEADER]: "pub_l" } })
      fakeStudio(routes)
      const attrs: Record<string, string> = { "data-endpoint": LOCAL, "data-open": "true" }
      const put = (source: string, pid: string) => {
        if (source === "explicit") attrs["data-scope"] = pid
        if (source === "url") setURL(`#weft_scope=${pid}`)
        if (source === "marker") chat(pid)
      }
      put(win, "pub_w")
      put(lose, "pub_l")
      const el = await mountWith(attrs)
      if (lose === "header") {
        await window.fetch("/chat", { method: "POST" })
        await settle()
        expect(el.detectedScopes().map((d) => d.key)).toEqual(["pub_l"]) // seen, not followed
      }
      expect(following(el).publicId).toBe("pub_w")
      expect(text(el, ".weft-title")).toContain("pub_w")
      // Both are known: the switcher lists them in ladder order.
      expect(el.conversations().map((c) => [c.key, c.source])).toEqual([
        ["pub_w", win],
        ["pub_l", lose],
      ])
    })
  }

  it("focus in a marked chat does not move the panel off the URL's scope; the switcher still does", async () => {
    fakeStudio(routesFor("pub_w", "pub_l"))
    setURL("#weft_scope=pub_w")
    const box = chat("pub_l")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    box.querySelector("input")!.focus()
    await settle(150)
    expect(following(el).publicId).toBe("pub_w")
    const sel = $(el, "select.weft-switch") as HTMLSelectElement
    sel.value = "1"
    sel.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    expect(following(el).publicId).toBe("pub_l")
  })
})

describe("the URL rung's listeners", () => {
  it("re-reads on hashchange and popstate; the listeners are passive and removed on disconnect", async () => {
    fakeStudio(routesFor("pub_a", "pub_b", "pub_c"))
    const add = vi.spyOn(window, "addEventListener")
    const remove = vi.spyOn(window, "removeEventListener")
    setURL("#weft_scope=pub_a")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(following(el).publicId).toBe("pub_a")
    const added = add.mock.calls.filter((c) => c[0] === "hashchange" || c[0] === "popstate")
    expect(added.map((c) => [c[0], c[2]])).toEqual([
      ["hashchange", { passive: true }],
      ["popstate", { passive: true }],
    ])
    setURL("#weft_scope=pub_b")
    window.dispatchEvent(new HashChangeEvent("hashchange"))
    await settle()
    expect(following(el).publicId).toBe("pub_b")
    setURL("?weft_scope=pub_c")
    window.dispatchEvent(new PopStateEvent("popstate"))
    await settle()
    expect(following(el).publicId).toBe("pub_c")
    // The URL dropped: the fallback.
    setURL("")
    window.dispatchEvent(new PopStateEvent("popstate"))
    await settle()
    expect(following(el).publicId).toBe("")
    el.remove()
    const removed = remove.mock.calls.filter((c) => c[0] === "hashchange" || c[0] === "popstate")
    expect(removed.map((c) => c[0])).toEqual(["hashchange", "popstate"])
    for (const r of removed) expect(r[1]).toBe(added.find((a) => a[0] === r[0])![1]) // the same function
  })

  it("an unchanged URL on hashchange draws nothing", async () => {
    fakeStudio(routesFor("pub_a"))
    setURL("#weft_scope=pub_a")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    const draws = vi.spyOn(WeftDevtools.prototype as unknown as { draw: () => void }, "draw")
    window.dispatchEvent(new HashChangeEvent("hashchange"))
    await settle()
    expect(draws).not.toHaveBeenCalled()
    expect(following(el).publicId).toBe("pub_a")
  })

  it("data-detect=\"off\" leaves rung 4 on; the footer says url", async () => {
    fakeStudio(routesFor("pub_a"))
    setURL("#weft_scope=pub_a")
    const el = await mountWith({ "data-endpoint": LOCAL, "data-open": "true", "data-detect": "off" })
    expect(readConfig(el).detect).toBe("off")
    expect(following(el).publicId).toBe("pub_a")
    expect(text(el, ".weft-detect")).toBe(" · detect: url")
  })

  it("a read-scoped panel token off loopback still honours the URL (rungs 2 and 3 are off there)", async () => {
    fakeStudio(routesFor("pub_a"))
    vi.spyOn(pageURL, "href").mockReturnValue("https://shop.example/chat#weft_scope=pub_a%3Bsession%3Ds_pub_a")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-token": PANEL_TOKEN, "data-open": "true" })
    expect(following(el)).toEqual({ publicId: "pub_a", session: "s_pub_a" })
    expect(text(el, ".weft-detect")).toBe(" · detect: url")
  })

  it("the URL is never written: no pushState, no replaceState, no hash or search change", async () => {
    fakeStudio(routesFor("pub_a", "pub_b"))
    setURL("?x=1#weft_scope=pub_a")
    const before = location.href
    const push = vi.spyOn(history, "pushState")
    const replace = vi.spyOn(history, "replaceState")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    chat("pub_b")
    await settle(150)
    const sel = $(el, "select.weft-switch") as HTMLSelectElement
    sel.value = "1"
    sel.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    expect(following(el).publicId).toBe("pub_b")
    window.dispatchEvent(new HashChangeEvent("hashchange"))
    await settle()
    el.remove()
    expect(push).not.toHaveBeenCalled()
    expect(replace).not.toHaveBeenCalled()
    expect(location.href).toBe(before)
  })

  it("an explicit scope set later wins over the URL; removing it hands the panel back to the URL", async () => {
    fakeStudio(routesFor("pub_a", "pub_b"))
    setURL("#weft_scope=pub_a")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    el.setAttribute("data-scope", "pub_b")
    await settle()
    expect(following(el).publicId).toBe("pub_b")
    expect(text(el, ".weft-detect")).toBe(" · detect: markers")
    el.removeAttribute("data-scope")
    await settle()
    expect(following(el).publicId).toBe("pub_a")
  })
})

// ── Rung 5: the fallback, live ────────────────────────────────────

describe("the fallback: no conversation detected", () => {
  it("says so, with the one-line fixes behind \"how to scope\"", async () => {
    fakeStudio(routesFor("pub_a"))
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(text(el, ".weft-title")).toBe(NO_SCOPE_LABEL)
    expect(text(el, ".weft-head")).toContain("no conversation detected on this page · how to scope")
    expect($(el, ".weft-howto")).toBeNull()
    const how = $(el, ".weft-howto-btn")!
    expect(how.getAttribute("aria-expanded")).toBe("false")
    click(how)
    await settle()
    expect(all(el, ".weft-howto code").map((n) => n.textContent)).toEqual([...HOW_TO_SCOPE])
    expect(HOW_TO_SCOPE.join("\n")).toMatch(/data-scope="pub_…"[\s\S]*scope\("pub_…"\)[\s\S]*data-weft-scope="pub_…"[\s\S]*scope\.Header/)
    click($(el, ".weft-howto-btn"))
    await settle()
    expect($(el, ".weft-howto")).toBeNull()
  })

  it("a scoped panel shows no fallback label", async () => {
    fakeStudio(routesFor("pub_a"))
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true", "data-scope": "pub_a" })
    expect(text(el, ".weft-head")).not.toContain("how to scope")
  })

  it("the list follows the live stream (one grant, agent=<the newest run's agent>): no runs refetch in 30 s while frames arrive", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const studio = fakeStudio(routesFor("pub_a"))
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(studio.posts("live-grant").map((g) => g.body)).toEqual([{ agent: "acme-support", kinds: "run" }])
    const es = FakeEventSource.last("agent=acme-support")!
    es.opened()
    const reads = () => studio.gets("runs?limit=10").length
    expect(reads()).toBe(1)
    for (let i = 1; i <= 6; i++) {
      es.emit("run", { run: runRow({ id: `r_new${i}`, public_id: `pub_n${i}`, started: `2026-10-01T10:0${i}:00Z` }) }, String(i))
      await vi.advanceTimersByTimeAsync(5_000)
    }
    await settle()
    expect(reads()).toBe(1)
    expect(all(el, ".weft-turn .weft-id").map((n) => n.textContent).slice(0, 2)).toEqual(["r_new6", "r_new5"])
    expect($(el, ".weft-dot.weft-on")).toBeTruthy() // the live dot
    expect($(el, ".weft-dev-poll")).toBeNull()
  })

  it("a refused stream (403) keeps the 10 s poll and says \"polling\"", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const routes = routesFor("pub_a")
    routes["POST live-grant"] = () => apiError(403, "forbidden", "not this stream")
    const studio = fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(text(el, ".weft-dev-poll")).toBe("streaming needs the server token · polling")
    expect(FakeEventSource.instances).toHaveLength(0)
    await vi.advanceTimersByTimeAsync(11_000)
    await settle()
    expect(studio.gets("runs?limit=10").length).toBe(2)
    expect(studio.posts("live-grant")).toHaveLength(1) // a 403 is not asked again
  })

  it("a panel token never asks for an agent's stream: it polls and says so", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const studio = fakeStudio(routesFor("pub_a"))
    const el = await mountWith({ "data-endpoint": REMOTE, "data-token": PANEL_TOKEN, "data-open": "true" })
    expect(studio.posts("live-grant").filter((g) => (g.body as { agent?: string }).agent)).toEqual([])
    expect(text(el, ".weft-dev-poll")).toBe("streaming needs the server token · polling")
    await vi.advanceTimersByTimeAsync(11_000)
    await settle()
    expect(studio.gets("runs?limit=10").length).toBe(2)
  })

  it("a list holding a second agent's runs keeps the poll for them, and says which agent is live", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const routes = routesFor("pub_a")
    routes["runs?limit=10"] = {
      total: 2,
      runs: [runRow({ id: "r_1", started: "2026-10-01T09:05:00Z" }), runRow({ id: "r_2", agent: "billing" })],
      next_before: null,
    }
    const studio = fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(text(el, ".weft-dev-poll")).toBe("live: agent acme-support · the other agents' runs every 10 s")
    await vi.advanceTimersByTimeAsync(11_000)
    await settle()
    expect(studio.gets("runs?limit=10").length).toBe(2)
  })
})

// ── The activity pill ─────────────────────────────────────────────

describe("the collapsed pill's activity signal", () => {
  /** pub_orders with its newest turn running: one step folded so far. */
  function runningRoutes() {
    const routes = baseRoutes()
    const running = runRow({ id: "s_01-t2", turn: 2, status: "running", finished: null, steps: 0, started: "2026-10-01T09:05:00Z" })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 2, runs: [running, runRow({})], next_before: null }
    routes["runs/s_01-t2"] = { ...running, children: [] }
    routes["runs/s_01-t2/events?after=0&limit=500"] = page(runEvents("s_01-t2").slice(0, 2), { done: false })
    routes["runs/s_01-t2/transcript"] = transcript([user("q")])
    routes["runs/s_01-t2/spans"] = { spans: [] }
    return routes
  }
  const fab = (el: WeftDevtools) => $(el, ".weft-fab") as HTMLElement

  it("pulses and shows the live step count while a run runs; back to the plain pill when it ends", async () => {
    fakeStudio(runningRoutes())
    const el = await mountWith({ "data-endpoint": REMOTE, "data-scope": "pub_orders" })
    expect(fab(el).classList.contains("weft-fab-pulse")).toBe(true)
    expect(text(el, ".weft-fab-count")).toBe(" ● 1")
    expect(fab(el).getAttribute("aria-label")).toBe("weft devtools · running, step 1")
    const tail = FakeEventSource.last("run=s_01-t2")!
    tail.opened()
    tail.emit("record", { run_id: "s_01-t2", kind: "event", pos: 2, time: T0, event: { type: "step_finish", run_id: "s_01-t2", index: 0, reason: "tool_calls", usage: { input_tokens: 1, output_tokens: 1 } } }, "3")
    tail.emit("record", { run_id: "s_01-t2", kind: "event", pos: 3, time: T0, event: { type: "step_start", run_id: "s_01-t2", index: 1 } }, "4")
    await settle()
    expect(text(el, ".weft-fab-count")).toBe(" ● 2")
    expect(fab(el).getAttribute("aria-label")).toBe("weft devtools · running, step 2")
    FakeEventSource.last("public_id=pub_orders")!.emit("run", { run: runRow({ id: "s_01-t2", turn: 2, steps: 2, started: "2026-10-01T09:05:00Z" }) }, "9")
    await settle()
    expect(fab(el).className).toBe("weft-fab weft-fab-bottom-right")
    expect(fab(el).textContent).toBe("devtools")
    expect(fab(el).getAttribute("aria-label")).toBeNull()
  })

  it("a run heard starting on the scope's stream pulses the pill (its steps unknown until it is followed)", async () => {
    fakeStudio({ ...baseRoutes(), "runs/s_01-t3": { ...runRow({ id: "s_01-t3" }), children: [] } })
    const el = await mountWith({ "data-endpoint": REMOTE, "data-scope": "pub_orders" })
    expect(fab(el).classList.contains("weft-fab-running")).toBe(false)
    FakeEventSource.last("public_id=pub_orders")!.emit("run", { run: runRow({ id: "s_01-t3", turn: 3, status: "running", finished: null, steps: 0, started: "2026-10-01T09:09:00Z" }) })
    await settle()
    expect(fab(el).classList.contains("weft-fab-pulse")).toBe(true)
    expect(text(el, ".weft-fab-count")).toBe(" ●")
    expect(fab(el).getAttribute("aria-label")).toBe("weft devtools · running")
  })

  it("prefers-reduced-motion: the count without the animation class", async () => {
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: q.includes("reduce"), media: q, addEventListener() {}, removeEventListener() {} }))
    fakeStudio(runningRoutes())
    const el = await mountWith({ "data-endpoint": REMOTE, "data-scope": "pub_orders" })
    expect(fab(el).classList.contains("weft-fab-running")).toBe(true)
    expect(fab(el).classList.contains("weft-fab-pulse")).toBe(false)
    expect(text(el, ".weft-fab-count")).toBe(" ● 1")
  })

  it("no stream, no signal: a dev list that only polls shows the plain pill over a running row", async () => {
    const routes = routesFor("pub_a")
    routes["runs?limit=10"] = { total: 1, runs: [runRow({ status: "running", finished: null })], next_before: null }
    routes["POST live-grant"] = () => apiError(403, "forbidden", "no")
    fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": REMOTE })
    expect(fab(el).className).toBe("weft-fab weft-fab-bottom-right")
  })
})

describe("C3.4 is silent", () => {
  it("no console output across the URL rung, the fallback and the pill", async () => {
    const spies = consoleSpies()
    const routes = routesFor("pub_a")
    routes["POST live-grant"] = () => apiError(403, "forbidden", "no")
    fakeStudio(routes)
    setURL("#weft_scope=%E0%A4%A;;=")
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    setURL("")
    window.dispatchEvent(new HashChangeEvent("hashchange"))
    await settle()
    click($(el, ".weft-howto-btn"))
    click($(el, ".weft-btn[title^='collapse']"))
    await settle()
    el.remove()
    for (const s of spies) expect(s).not.toHaveBeenCalled()
  })
})
