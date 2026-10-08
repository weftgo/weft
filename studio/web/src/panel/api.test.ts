// Plan C4.2: the host API — the element's own methods (open, close,
// toggle, isOpen, scope, select, on, studioLink), the events it
// dispatches (weft:run, weft:parked, weft:error: bubbling, composed,
// asynchronous, only for the conversation followed) and the one global
// the script-tag install adds, window.weft.devtools, present only while
// a panel is connected.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { href, runLink } from "../lib/links"
import type { Scope } from "../lib/scope"
import { SETTLE_MS } from "./element"
import type { DevtoolsAPI, DevtoolsEvents, WeftDevtools } from "./element"
import type { PanelModel } from "./state"
import {
  $,
  apiError,
  ATTRS,
  baseRoutes,
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
  trap,
  user,
  assistant,
} from "./testkit"
import type { Route } from "./testkit"

const STUDIO = "http://studio.test/studio/"
/** A read-scoped panel token (setup C's default mint). */
const PANEL_TOKEN = `weft_pt.${btoa(JSON.stringify({ public_id: "pub_orders", exp: "2099-01-01T00:00:00Z" })).replace(/=+$/, "")}.sig`

type W = { weft?: unknown }
const win = () => window as unknown as W

beforeEach(setup)
afterEach(() => {
  teardown()
  delete win().weft
  history.replaceState(null, "", "/")
  document.head.querySelectorAll("script, meta").forEach((n) => n.remove())
  vi.restoreAllMocks()
})

async function mountWith(attrs: Record<string, string> = ATTRS): Promise<WeftDevtools> {
  const el = create(attrs)
  document.body.appendChild(el)
  await settle()
  return el
}

const following = (el: WeftDevtools): Scope => (el as unknown as { model: PanelModel }).model.following
const state = (el: WeftDevtools) => (el as unknown as { model: PanelModel }).model.state

/** A run of pub_orders, with its routes. */
function addRun(routes: Record<string, Route>, id: string, over: Parameters<typeof runRow>[0] = {}) {
  const r = runRow({ id, ...over })
  routes[`runs/${id}`] = { ...r, children: [] }
  routes[`runs/${id}/events?after=0&limit=500`] = page(runEvents(id))
  routes[`runs/${id}/transcript`] = transcript([user(`q ${id}`)], [assistant(`a ${id}`)])
  routes[`runs/${id}/spans`] = { spans: [] }
  return r
}

/** record follows every host event the element dispatches. */
function record(el: WeftDevtools) {
  const seen: { type: string; detail: unknown }[] = []
  for (const k of ["run", "parked", "error"] as const)
    el.addEventListener(`weft:${k}`, (e) => seen.push({ type: k, detail: (e as CustomEvent).detail }))
  return seen
}

const lane = () => FakeEventSource.last("public_id=")!

describe("open, close, toggle, isOpen", () => {
  it("drive the dock, and the getter reads it", async () => {
    fakeStudio(baseRoutes())
    const el = await mountWith({ "data-endpoint": STUDIO, "data-public-id": "pub_orders" })
    expect(el.isOpen).toBe(false)
    expect($(el, ".weft-fab")).not.toBeNull()
    el.open()
    el.open() // idempotent
    await settle()
    expect(el.isOpen).toBe(true)
    expect($(el, ".weft-dock")).not.toBeNull()
    el.close()
    await settle()
    expect(el.isOpen).toBe(false)
    expect($(el, ".weft-fab")).not.toBeNull()
    el.toggle()
    expect(el.isOpen).toBe(true)
    expect(el.api.isOpen).toBe(true)
    el.api.close()
    expect(el.api.isOpen).toBe(false)
  })
})

describe("scope", () => {
  function twoConversations() {
    const routes = baseRoutes()
    const b = runRow({ id: "r_b", public_id: "pub_b", session_id: "s_b" })
    routes["sessions?public_id=pub_b"] = { total: 0, sessions: [], next_before: null }
    routes["runs?public_id=pub_b&limit=50"] = { total: 1, runs: [b], next_before: null }
    routes["runs/r_b"] = { ...b, children: [] }
    routes["runs/r_b/events?after=0&limit=500"] = page(runEvents("r_b"))
    routes["runs/r_b/transcript"] = transcript([user("q b")], [assistant("a b")])
    routes["runs/r_b/spans"] = { spans: [] }
    return routes
  }

  it("a string is the serialised form, a Scope is followed whole, and the element carries it", async () => {
    fakeStudio(twoConversations())
    const el = await mountWith({ "data-endpoint": STUDIO, "data-open": "true" })
    el.scope("pub_b;run=r_b")
    await settle()
    expect(following(el)).toEqual({ publicId: "pub_b", run: "r_b" })
    expect(el.getAttribute("data-weft-scope")).toBe("pub_b;run=r_b")
    expect(text(el, ".weft-sel .weft-id")).toBe("r_b")
    el.scope({ publicId: "pub_orders", flow: "f_1" })
    await settle()
    expect(following(el)).toEqual({ publicId: "pub_orders", flow: "f_1" })
    expect(el.getAttribute("data-weft-scope")).toBe("pub_orders;flow=f_1")
  })

  it("scope(null) clears the explicit override: the ladder (here the page URL) decides again", async () => {
    fakeStudio(twoConversations())
    history.replaceState(null, "", "/#weft_scope=pub_b")
    const el = await mountWith({ "data-endpoint": STUDIO, "data-open": "true" })
    expect(following(el).publicId).toBe("pub_b")
    el.scope("pub_orders")
    await settle()
    expect(following(el).publicId).toBe("pub_orders")
    el.scope(null)
    await settle()
    expect(following(el).publicId).toBe("pub_b")
    expect(el.hasAttribute("data-weft-scope")).toBe(false)
    expect(text(el, ".weft-detect")).toContain("url")
  })

  describe("a session and no public id: resolved through sessions/{id}/public_id", () => {
    it("200: follows the public id it names, narrowed to the session", async () => {
      const routes = twoConversations()
      routes["sessions/s_b/public_id"] = { session_id: "s_b", public_id: "pub_b" }
      const studio = fakeStudio(routes)
      const el = await mountWith({ "data-endpoint": STUDIO, "data-open": "true", "data-public-id": "pub_orders" })
      el.scope({ publicId: "", session: "s_b" })
      await settle()
      expect(studio.gets("sessions/s_b/public_id")).toHaveLength(1)
      expect(following(el)).toEqual({ publicId: "pub_b", session: "s_b" })
      expect($(el, ".weft-api-note")).toBeNull()
    })

    it("the string form too, before Studio has answered (the lookup waits for the start)", async () => {
      const routes = twoConversations()
      routes["sessions/s_b/public_id"] = { session_id: "s_b", public_id: "pub_b" }
      fakeStudio(routes)
      const el = create({ "data-endpoint": STUDIO, "data-open": "true" })
      document.body.appendChild(el)
      el.scope(";session=s_b")
      await settle()
      expect(following(el)).toEqual({ publicId: "pub_b", session: "s_b" })
    })

    it.each([
      ["not_recorded", () => ({ session_id: "s_b", public_id: "", badge: "not_recorded" }), "session s_b has no public id · not recorded"],
      ["403", () => json403(), "session s_b: the session lookup needs the dev token"],
      ["404", () => apiError(404, "not_found", "no such session"), "session s_b has no public id · unknown session"],
    ])("%s: says so in one line and keeps its scope", async (_name, answer, line) => {
      const routes = twoConversations()
      routes["sessions/s_b/public_id"] = answer
      const t = trap()
      fakeStudio(routes)
      const el = await mountWith({ "data-endpoint": STUDIO, "data-open": "true", "data-public-id": "pub_orders" })
      expect(() => el.scope({ publicId: "", session: "s_b" })).not.toThrow()
      await settle()
      expect(following(el).publicId).toBe("pub_orders")
      expect(text(el, ".weft-api-note")).toContain(line)
      t.release()
      expect(t.escaped).toEqual([])
    })

    it("under a panel token the lookup is not asked: the line says it needs the dev token", async () => {
      const routes = twoConversations()
      routes["sessions/s_b/public_id"] = { session_id: "s_b", public_id: "pub_b" }
      const studio = fakeStudio(routes)
      const el = await mountWith({ "data-endpoint": STUDIO, "data-open": "true", "data-public-id": "pub_orders", "data-token": PANEL_TOKEN })
      el.scope({ publicId: "", session: "s_b" })
      await settle()
      expect(studio.gets("sessions/s_b")).toHaveLength(0)
      expect(following(el).publicId).toBe("pub_orders")
      expect(text(el, ".weft-api-note")).toBe("session s_b: the session lookup needs the dev token")
    })
  })
})

const json403 = () =>
  new Response(JSON.stringify({ error: { code: "forbidden", message: "hidden" }, badge: "hidden" }), {
    status: 403,
    headers: { "content-type": "application/json" },
  })

describe("select", () => {
  function conversation() {
    const routes = baseRoutes()
    const t1 = runRow({})
    const t2 = addRun(routes, "s_01-t2", { started: "2026-10-01T09:05:00Z", steps: 3 })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 2, runs: [t2, t1], next_before: null }
    return routes
  }

  it("a listed turn: selected, at the step given, and ⤢ carries that step", async () => {
    fakeStudio(conversation())
    const el = await mountWith()
    expect(state(el).selected).toBe("s_01-t2")
    el.select("s_01-t1", 0)
    await settle()
    expect(state(el).selected).toBe("s_01-t1")
    expect(state(el).selectedStep).toBe(0)
    const a = Array.from(el.shadowRoot!.querySelectorAll("a")).find((n) => n.textContent === "⤢")!
    expect(a.getAttribute("href")).toBe(href(STUDIO, runLink("s_01-t1", { step: 0, view: "story" })))
  })

  it("right after scope(): acts in the new scope's list", async () => {
    fakeStudio(conversation())
    const el = await mountWith({ "data-endpoint": STUDIO, "data-open": "true" })
    el.scope("pub_orders;run=s_01-t2")
    el.select("s_01-t1", 0)
    await settle()
    expect(following(el).publicId).toBe("pub_orders")
    expect(state(el).selected).toBe("s_01-t1")
    expect(state(el).selectedStep).toBe(0)
  })

  it("not listed but the conversation's: read by id, added and selected", async () => {
    const routes = conversation()
    addRun(routes, "s_01-t0", { started: "2026-09-01T09:00:00Z" })
    const studio = fakeStudio(routes)
    const el = await mountWith()
    el.select("s_01-t0", 0)
    await settle()
    expect(studio.gets("runs/s_01-t0")).not.toHaveLength(0)
    expect(state(el).turns.map((r) => r.id)).toContain("s_01-t0")
    expect(state(el).selected).toBe("s_01-t0")
    expect($(el, ".weft-api-note")).toBeNull()
  })

  it("another conversation's run (or none): the honest line, the selection kept", async () => {
    const routes = conversation()
    addRun(routes, "r_other", { public_id: "pub_other" })
    fakeStudio(routes)
    const el = await mountWith()
    el.select("r_other")
    await settle()
    expect(state(el).selected).toBe("s_01-t2")
    expect(text(el, ".weft-api-note")).toBe("run r_other not in this conversation")
    el.select("r_gone")
    await settle()
    expect(text(el, ".weft-api-note")).toBe("run r_gone not in this conversation")
    el.select("s_01-t1")
    await settle()
    expect($(el, ".weft-api-note")).toBeNull()
  })
})

describe("on and the events", () => {
  it('"run": once per transition the panel sees — history is not one, the pinned run is', async () => {
    const routes = baseRoutes()
    addRun(routes, "s_01-t2")
    fakeStudio(routes)
    const el = create({ ...ATTRS, "data-scope": "pub_orders;run=s_01-t1" })
    const seen = record(el)
    document.body.appendChild(el)
    await settle()
    // The pinned run, first seen finished: reported once.
    expect(seen).toEqual([
      { type: "run", detail: { runId: "s_01-t1", status: "succeeded", publicId: "pub_orders", sessionId: "s_01", step: 0 } },
    ])
    const running = runRow({ id: "s_01-t2", status: "running", finished: null, started: "2026-10-01T09:05:00Z", steps: 1 })
    lane().emit("run", { run: running })
    await settle()
    lane().emit("run", { run: running }) // the same status again: nothing
    await settle()
    lane().emit("run", { run: { ...running, status: "succeeded", finished: "2026-10-01T09:06:00Z", steps: 2 } })
    await settle()
    expect(seen.slice(1).map((e) => [(e.detail as DevtoolsEvents["run"]).runId, (e.detail as DevtoolsEvents["run"]).status])).toEqual([
      ["s_01-t2", "running"],
      ["s_01-t2", "succeeded"],
    ])
    // Another conversation's run on the lane is not this one's.
    lane().emit("run", { run: runRow({ id: "r_x", public_id: "pub_other", status: "running" }) })
    await settle()
    expect(seen).toHaveLength(3)
  })

  it("history alone fires nothing: a finished list read at the start is not a transition", async () => {
    fakeStudio(baseRoutes())
    const el = create()
    const seen = record(el)
    document.body.appendChild(el)
    await settle()
    expect(seen).toEqual([])
  })

  it('"parked": once per parked call, with the ack id the approval takes', async () => {
    const routes = baseRoutes()
    const calls = [
      { type: "tool_call", id: "call_refund", name: "refund", args: { order: "4411" } },
      { type: "tool_call", id: "call_mail", name: "send_email", args: {} },
    ]
    addRun(routes, "s_01-t2")
    routes["runs/s_01-t2/events?after=0&limit=500"] = page(runEvents("s_01-t2", { pending: calls }))
    const studio = fakeStudio(routes)
    const el = create()
    const seen = record(el)
    const got: DevtoolsEvents["parked"][] = []
    el.on("parked", (d) => got.push(d))
    document.body.appendChild(el)
    await settle()
    const t2 = runRow({ id: "s_01-t2", status: "running", finished: null, started: "2026-10-01T09:05:00Z" })
    lane().emit("run", { run: t2 })
    await settle()
    const parked = { ...t2, status: "succeeded" as const, finished: "2026-10-01T09:06:00Z", pending: 2 }
    lane().emit("run", { run: parked })
    await settle()
    lane().emit("run", { run: parked }) // the same parked row again
    await settle()
    expect(got).toEqual([
      { runId: "s_01-t2", callId: "call_refund", ackId: "call_refund", name: "refund" },
      { runId: "s_01-t2", callId: "call_mail", ackId: "call_mail", name: "send_email" },
    ])
    expect(seen.filter((e) => e.type === "run").map((e) => (e.detail as DevtoolsEvents["run"]).status)).toEqual(["running", "parked"])
    // Read from the run's stored events once (its run_finish's pending).
    expect(studio.gets("runs/s_01-t2/events")).toHaveLength(1)
  })

  it('"error": a failed run\'s error, once per run', async () => {
    fakeStudio(baseRoutes())
    const el = create()
    const errors: DevtoolsEvents["error"][] = []
    el.on("error", (d) => errors.push(d))
    document.body.appendChild(el)
    await settle()
    const t2 = runRow({ id: "s_01-t2", status: "running", finished: null, started: "2026-10-01T09:05:00Z" })
    lane().emit("run", { run: t2 })
    await settle()
    const failed = { ...t2, status: "failed" as const, err: "model: 503 overloaded" }
    lane().emit("run", { run: failed })
    await settle()
    lane().emit("run", { run: { ...failed, steps: 2 } })
    await settle()
    expect(errors).toEqual([{ message: "model: 503 overloaded", runId: "s_01-t2" }])
  })

  it("events bubble from the element to document, asynchronously; on() unsubscribes", async () => {
    fakeStudio(baseRoutes())
    const el = await mountWith()
    const onDoc: unknown[] = []
    document.addEventListener("weft:run", (e) => onDoc.push((e as CustomEvent).detail))
    const viaOn: unknown[] = []
    const off = el.on("run", (d) => viaOn.push(d))
    lane().emit("run", { run: runRow({ id: "s_01-t2", status: "running", started: "2026-10-01T09:05:00Z" }) })
    expect(onDoc).toEqual([]) // not inside the panel's own work
    await settle()
    expect(onDoc).toHaveLength(1)
    expect(viaOn).toHaveLength(1)
    off()
    lane().emit("run", { run: runRow({ id: "s_01-t2", status: "succeeded", started: "2026-10-01T09:05:00Z" }) })
    await settle()
    expect(onDoc).toHaveLength(2)
    expect(viaOn).toHaveLength(1)
    expect(el.on("nope" as "run", () => {})).toBeTypeOf("function")
  })

  it("a throwing listener does not break the panel and prints nothing", async () => {
    const spies = (["log", "info", "warn", "error", "debug"] as const).map((m) => vi.spyOn(console, m).mockImplementation(() => {}))
    const t = trap()
    fakeStudio(baseRoutes())
    const el = await mountWith()
    el.on("run", () => {
      throw new Error("the host's bug")
    })
    const after: unknown[] = []
    el.on("run", (d) => after.push(d))
    lane().emit("run", { run: runRow({ id: "s_01-t2", status: "running", started: "2026-10-01T09:05:00Z", steps: 4 }) })
    await settle()
    expect(after).toHaveLength(1)
    expect(text(el, ".weft-turns")).toContain("4 steps") // the panel drew on
    t.release()
    expect(t.escaped).toEqual([])
    for (const s of spies) expect(s).not.toHaveBeenCalled()
  })
})

describe("window.weft.devtools (the script-tag install's one global)", () => {
  const api = () => (win().weft as { devtools?: DevtoolsAPI } | undefined)?.devtools

  it("is present only while a panel is connected, and is that panel's API", async () => {
    fakeStudio(baseRoutes())
    expect(win().weft).toBeUndefined()
    const el = await mountWith()
    expect(api()).toBe(el.api)
    api()!.close()
    expect(el.isOpen).toBe(false)
    api()!.open()
    expect(el.isOpen).toBe(true)
    el.remove()
    expect(win().weft).toBeUndefined()
  })

  it("never overwrites another library's window.weft: added to a plain object, absent (and said) on anything else", async () => {
    fakeStudio(baseRoutes())
    const theirs = { version: "their lib" }
    win().weft = theirs
    const el = await mountWith()
    expect(win().weft).toBe(theirs)
    expect(api()).toBe(el.api)
    el.remove()
    expect(win().weft).toEqual({ version: "their lib" })
    class Lib {}
    const lib = new Lib()
    win().weft = lib
    const el2 = await mountWith()
    expect(win().weft).toBe(lib)
    expect((lib as { devtools?: unknown }).devtools).toBeUndefined()
    expect(text(el2, ".weft-global")).toContain("window.weft is the page's")
    el2.remove()
    win().weft = { devtools: "mine" }
    const el3 = await mountWith()
    expect((win().weft as { devtools: unknown }).devtools).toBe("mine")
    expect(text(el3, ".weft-global")).toContain("not replaced")
  })

  it('data-global="off" (or the weft:global meta tag) keeps it off', async () => {
    fakeStudio(baseRoutes())
    const el = await mountWith({ ...ATTRS, "data-global": "off" })
    expect(win().weft).toBeUndefined()
    el.removeAttribute("data-global")
    expect(api()).toBe(el.api)
    el.remove()
    const meta = document.createElement("meta")
    meta.name = "weft:global"
    meta.content = "off"
    document.head.appendChild(meta)
    await mountWith()
    expect(win().weft).toBeUndefined()
  })

  it("hands over to the next connected panel when the one that published it leaves", async () => {
    fakeStudio(baseRoutes())
    const a = await mountWith()
    const b = await mountWith()
    expect(api()).toBe(a.api)
    a.remove()
    expect(api()).toBe(b.api)
    b.remove()
    expect(win().weft).toBeUndefined()
  })
})

describe("studioLink", () => {
  it("is links.ts's run link at the step, and never carries the token", async () => {
    fakeStudio(baseRoutes())
    const el = await mountWith({ ...ATTRS, "data-token": "dev_secret" })
    const link = el.studioLink("s_01-t1", 2)
    expect(link).toBe(href(STUDIO, runLink("s_01-t1", { step: 2, view: "story" })))
    expect(link).toContain("step=2")
    expect(link).not.toContain("dev_secret")
    expect(el.studioLink("s_01-t1")).toBe(href(STUDIO, runLink("s_01-t1")))
    expect(el.studioLink("a/b?c#d")).toContain("a%2Fb%3Fc%23d")
    expect(el.studioLink("")).toBe("")
    expect(el.api.studioLink("s_01-t1", -1)).toBe(href(STUDIO, runLink("s_01-t1")))
  })
})

// ── C4.2's review fixes ───────────────────────────────────────────

describe("review fixes", () => {
  function two() {
    const routes = baseRoutes()
    const b = runRow({ id: "r_b", public_id: "pub_b", session_id: "s_b" })
    routes["sessions?public_id=pub_b"] = { total: 0, sessions: [], next_before: null }
    routes["runs?public_id=pub_b&limit=50"] = { total: 1, runs: [b], next_before: null }
    routes["runs/r_b"] = { ...b, children: [] }
    routes["runs/r_b/events?after=0&limit=500"] = page(runEvents("r_b"))
    routes["runs/r_b/transcript"] = transcript([user("q b")], [assistant("a b")])
    routes["runs/r_b/spans"] = { spans: [] }
    routes["runs?public_id=pub_b&session_id=s_b&limit=50"] = routes["runs?public_id=pub_b&limit=50"]
    return routes
  }

  it("1. scope({session}) — no publicId — is looked up like the string form", async () => {
    const routes = two()
    routes["sessions/s_b/public_id"] = { session_id: "s_b", public_id: "pub_b" }
    const studio = fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": STUDIO, "data-open": "true", "data-public-id": "pub_orders" })
    el.scope({ session: "s_b" } as unknown as Scope)
    await settle()
    expect(studio.gets("sessions/s_b/public_id")).toHaveLength(1)
    expect(following(el)).toEqual({ publicId: "pub_b", session: "s_b" })
  })

  it("1. a run event's detail fed back (sessionId, runId) keeps the narrowing", async () => {
    fakeStudio(two())
    const el = await mountWith({ "data-endpoint": STUDIO, "data-open": "true" })
    el.scope({ publicId: "pub_b", sessionId: "s_b", runId: "r_b" } as unknown as Scope)
    await settle()
    expect(following(el)).toEqual({ publicId: "pub_b", session: "s_b", run: "r_b" })
  })

  it("1. a scope naming neither a public id nor a session is said, not dropped", async () => {
    fakeStudio(baseRoutes())
    const el = await mountWith()
    el.scope({ flow: "f_1" } as unknown as Scope)
    await settle()
    expect(text(el, ".weft-api-note")).toBe("scope: no public id or session")
    expect(following(el).publicId).toBe("pub_orders")
  })

  it("2. scope(null) restores what mount() named before the first scope()", async () => {
    fakeStudio(two())
    const el = create({ "data-endpoint": STUDIO, "data-open": "true" })
    el.options = { publicId: "pub_orders" }
    document.body.appendChild(el)
    await settle()
    el.scope("pub_b")
    await settle()
    el.scope({ publicId: "pub_b", run: "r_b" })
    await settle()
    expect(following(el).publicId).toBe("pub_b")
    el.scope(null)
    await settle()
    expect(following(el)).toEqual({ publicId: "pub_orders" })
    expect(el.options).toEqual({ publicId: "pub_orders" })
    expect(el.getAttribute("data-weft-scope")).toBe("pub_orders")
  })

  it("3. a frozen plain window.weft gets no global, and the footer says so", async () => {
    fakeStudio(baseRoutes())
    const theirs = Object.freeze({ v: 1 })
    win().weft = theirs
    const t = trap()
    const el = await mountWith()
    t.release()
    expect(t.escaped).toEqual([])
    expect(win().weft).toBe(theirs)
    expect(text(el, ".weft-global")).toContain("window.weft is the page's: no window.weft.devtools")
  })

  it("4. returning to a conversation is not a new sighting: A→B→A→B→A reports a running run once", async () => {
    const routes = two()
    const t2 = runRow({ id: "s_01-t2", status: "running", finished: null, started: "2026-10-01T09:05:00Z" })
    routes["runs?public_id=pub_orders&limit=50"] = { total: 2, runs: [t2, runRow({})], next_before: null }
    addRun(routes, "s_01-t2", { status: "running", finished: null, started: "2026-10-01T09:05:00Z" })
    fakeStudio(routes)
    const el = create({ "data-endpoint": STUDIO, "data-open": "true", "data-public-id": "pub_orders" })
    const seen = record(el)
    document.body.appendChild(el)
    await settle()
    for (const pub of ["pub_b", "pub_orders", "pub_b", "pub_orders"]) {
      el.scope(pub)
      await settle()
    }
    const runs = seen.map((e) => e.detail as DevtoolsEvents["run"]).filter((d) => d.runId === "s_01-t2")
    expect(runs.map((d) => d.status)).toEqual(["running"])
  })

  it("5. a step the run does not have is said, and the last step is carried instead", async () => {
    fakeStudio(baseRoutes())
    const el = await mountWith()
    el.select("s_01-t1", 99)
    await settle()
    expect(state(el).selectedStep).toBe(0)
    expect(text(el, ".weft-api-note")).toBe("step 99 not in run s_01-t1 · showing step 0")
    const a = Array.from(el.shadowRoot!.querySelectorAll("a")).find((n) => n.textContent === "⤢")!
    expect(a.getAttribute("href")).toContain("step=0")
  })

  it("6. a lookup Studio did not answer says so (nothing was learned about the session)", async () => {
    const routes = two()
    routes["sessions/s_b/public_id"] = () => apiError(500, "internal", "boom")
    fakeStudio(routes)
    const el = await mountWith()
    el.scope({ publicId: "", session: "s_b" })
    await settle()
    expect(text(el, ".weft-api-note")).toBe("session s_b: Studio did not answer the lookup")
  })

  it("6. select over a run read that failed in transport is not 'not in this conversation'", async () => {
    const routes = baseRoutes()
    routes["runs/r_x"] = () => apiError(503, "unavailable", "down")
    routes["runs/r_y"] = () => Promise.reject(new TypeError("network"))
    fakeStudio(routes)
    const el = await mountWith()
    el.select("r_x")
    await settle()
    expect(text(el, ".weft-api-note")).toBe("run r_x: Studio did not answer")
    el.select("r_y")
    await settle()
    expect(text(el, ".weft-api-note")).toBe("run r_y: Studio did not answer")
  })

  it("8. a start that hangs holds a select() SETTLE_MS at most, then says the panel is not connected", async () => {
    vi.useFakeTimers()
    fakeStudio(baseRoutes(), () => new Promise(() => {}))
    const el = create()
    document.body.appendChild(el)
    await vi.advanceTimersByTimeAsync(10)
    el.select("s_01-t1")
    await vi.advanceTimersByTimeAsync(SETTLE_MS - 100)
    expect($(el, ".weft-api-note")).toBeNull()
    await vi.advanceTimersByTimeAsync(200)
    expect(text(el, ".weft-api-note")).toBe("select s_01-t1: the panel is not connected")
  })
})
