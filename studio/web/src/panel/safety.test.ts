// The panel inside someone else's page (WEFT-DEVTOOLS §5.2–§5.4): it
// must never break, slow or leak into the host. Pinned here: the
// element's lifecycle (attribute changes, upgrade, a Studio that does
// not answer), nothing thrown or rejected into the page's own error
// handlers over malformed records, the keyboard never taking the
// page's keys, focus and open state surviving the redraw, the streams
// closing for good, and the token going nowhere but data-endpoint.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { openPanelLive } from "./client"
import { studioLink, WeftDevtools } from "./element"
import {
  $,
  all,
  apiError,
  assistant,
  ATTRS,
  baseRoutes,
  button,
  click,
  create,
  FakeEventSource,
  fakeStudio,
  json,
  META,
  mount,
  page,
  pause,
  runEvents,
  runRow,
  settle,
  setup,
  T0,
  teardown,
  text,
  transcript,
  trap,
  user,
} from "./testkit"

beforeEach(setup)
afterEach(teardown)

const metaCalls = (s: ReturnType<typeof fakeStudio>) => s.gets("meta").length

describe("the element's lifecycle", () => {
  it("an attribute change while meta is in flight rescopes: the panel stays, one meta request", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_other&limit=50"] = { total: 0, runs: [], next_before: null }
    const studio = fakeStudio(routes, async () => {
      await pause(20)
      return META
    })
    const el = create()
    document.body.appendChild(el)
    await pause(5) // meta is in flight
    el.setAttribute("data-public-id", "pub_other") // window.__WEFT__ = { publicId } lands here
    await settle(80)
    expect(el.isConnected).toBe(true)
    expect(metaCalls(studio)).toBe(1)
    expect(text(el, ".weft-title")).toContain("pub_other")
    expect(studio.gets("runs?public_id=pub_other").length).toBe(1)
  })

  it("an upgrade's attribute callbacks and its connect are one start", async () => {
    const studio = fakeStudio(baseRoutes())
    const el = create({ "data-open": "true" })
    document.body.appendChild(el)
    // What the upgrade of <weft-devtools data-…> delivers: every
    // attribute, on a connected element, in one turn.
    for (const [k, v] of Object.entries(ATTRS)) el.setAttribute(k, v)
    await settle(60)
    expect(el.isConnected).toBe(true)
    expect(metaCalls(studio)).toBe(1)
    expect(text(el, ".weft-title")).toContain("pub_orders")
  })

  it("a public id change after connect rescopes on the same connection; nothing of the old conversation stays", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_other&limit=50"] = { total: 0, runs: [], next_before: null }
    const studio = fakeStudio(routes)
    const el = await mount()
    expect(text(el, ".weft-turns")).toContain("s_01-t1")
    el.setAttribute("data-public-id", "pub_other")
    await settle()
    expect(metaCalls(studio)).toBe(1)
    expect(text(el, ".weft-turns")).not.toContain("s_01-t1")
    expect(FakeEventSource.live("public_id=pub_orders")).toHaveLength(0)
    expect(FakeEventSource.live("public_id=pub_other")).toHaveLength(1)
    // data-position redraws, and asks the server nothing.
    const before = studio.calls.length
    el.setAttribute("data-position", "bottom-left")
    await settle()
    expect($(el, ".weft-dock.weft-bottom-left")).toBeTruthy()
    expect(studio.calls.length).toBe(before)
  })

  it("markup Studio does not answer goes dormant in place — never removed from under a framework; one quiet line says why", async () => {
    const t = trap()
    const fetchMock = vi.fn(async () => new Response("no studio", { status: 404 }))
    vi.stubGlobal("fetch", fetchMock)
    const el = await mount({ ...ATTRS, "data-open": "false" })
    await settle()
    expect(el.isConnected).toBe(true) // the page's node (a framework may own it)
    expect(el.shadowRoot?.querySelector(".weft-fab, .weft-dock")).toBeNull()
    expect(text(el, ".weft-unreachable")).toBe("Studio not reachable at http://studio.test/studio/ · retry")
    expect(fetchMock).toHaveBeenCalledTimes(1)
    t.release()
    expect(t.escaped).toEqual([])
  })

  it("a data-endpoint that is not an http(s) URL starts nothing and throws nothing", async () => {
    const t = trap()
    const studio = fakeStudio(baseRoutes())
    for (const bad of ["http://", "javascript:alert(1)", "data:text/html,x"]) {
      const el = create({ "data-endpoint": bad, "data-token": "tok_secret" })
      document.body.appendChild(el)
      await settle()
      el.setAttribute("data-endpoint", bad + " ")
      await settle()
      el.remove()
    }
    t.release()
    expect(t.escaped).toEqual([])
    expect(studio.calls).toHaveLength(0) // the token went nowhere
  })

  it("something that answers meta with other JSON is not a Studio: gone, nothing rejected", async () => {
    for (const notMeta of [{}, null, { studio_version: 3 }, [1, 2]]) {
      const t = trap()
      fakeStudio(baseRoutes(), json(notMeta))
      const el = create()
      el.autoMounted = true
      document.body.appendChild(el)
      await settle()
      t.release()
      expect(t.escaped).toEqual([])
      expect(el.isConnected).toBe(false)
    }
  })

  it("removing the element closes every stream and stops every timer", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_orders&limit=50"] = {
      total: 1,
      runs: [runRow({ status: "running", finished: null })],
      next_before: null,
    }
    routes["runs/s_01-t1"] = { ...runRow({ status: "running", finished: null }), children: [] }
    const studio = fakeStudio(routes)
    const el = await mount()
    expect(FakeEventSource.live("live?")).toHaveLength(2) // scope + tail
    vi.useFakeTimers()
    FakeEventSource.last("run=")!.fail()
    el.remove()
    const before = studio.calls.length
    const opened = FakeEventSource.instances.length
    await vi.advanceTimersByTimeAsync(5 * 60_000)
    expect(FakeEventSource.live("live?")).toHaveLength(0)
    expect(studio.calls.length).toBe(before)
    expect(FakeEventSource.instances.length).toBe(opened)
  })
})

describe("nothing escapes into the host page", () => {
  it("a live frame that does not parse is skipped, not thrown", async () => {
    fakeStudio(baseRoutes())
    await mount()
    const scope = FakeEventSource.last("public_id=")!
    expect(() => scope.emitRaw("run", "{not json")).not.toThrow()
    expect(() => scope.emitRaw("run", "null")).not.toThrow()
    expect(() => scope.emitRaw("record", "<html>")).not.toThrow()
    expect(() => scope.emit("run", { run: null })).not.toThrow()
  })

  it("records stored as ingested — null bodies, bare strings, typeless events — draw without a throw", async () => {
    const t = trap()
    const routes = baseRoutes()
    routes["runs/s_01-t1/events?after=0&limit=500"] = page([
      ...runEvents("s_01-t1").slice(0, 2),
      null,
      "a bare string body",
      { no: "type" },
      { type: "steered", run_id: "s_01-t1", seq: 1, step: 0, messages: [{ role: "user", content: null }] },
      { type: "tool_start", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", args: {} },
      { type: "tool_finish", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "lookup_order", content: 42, is_error: false },
      ...runEvents("s_01-t1").slice(2),
    ])
    routes["runs/s_01-t1/transcript"] = {
      batches: [
        { index: 0, step: 0, messages: null },
        { index: 1, step: 0, messages: "not a list" },
        { index: 2, step: 0, messages: [null, 7, { role: "user" }, user("where is my order #4411?")] },
        { index: 3, step: 0, messages: [assistant("Your order shipped yesterday.")] },
      ],
    }
    fakeStudio(routes)
    const el = await mount()
    await settle()
    t.release()
    expect(t.escaped).toEqual([])
    expect(text(el, ".weft-main")).toContain("lookup_order")
    expect(text(el, ".weft-main")).toContain("Your order shipped yesterday.")
  })

  it("run content is text, never markup (XSS)", async () => {
    const evil = `<img src=x onerror="window.__pwned=1"><script>window.__pwned=1</script>`
    const routes = baseRoutes()
    routes["runs?public_id=pub_orders&limit=50"] = {
      total: 1,
      runs: [runRow({ err: evil, agent: evil, model: { provider: evil, name: evil } })],
      next_before: null,
    }
    routes["runs/s_01-t1/events?after=0&limit=500"] = page([
      ...runEvents("s_01-t1").slice(0, 2),
      { type: "tool_start", run_id: "s_01-t1", seq: 1, call_id: "c1", name: evil, args: { q: evil } },
      { type: "tool_finish", run_id: "s_01-t1", seq: 1, call_id: "c1", name: evil, content: evil, is_error: true },
      ...runEvents("s_01-t1").slice(2),
    ])
    routes["runs/s_01-t1/transcript"] = transcript([user(evil)], [assistant(evil)])
    fakeStudio(routes)
    const el = await mount()
    click(button(el, "raw"))
    await settle()
    expect(el.shadowRoot?.querySelector("img, script")).toBeNull()
    expect((window as { __pwned?: number }).__pwned).toBeUndefined()
    expect(text(el, ".weft-main")).toContain("<img src=x")
  })

  it("a clipboard that refuses is not the page's unhandled rejection", async () => {
    const t = trap()
    vi.stubGlobal("navigator", {
      clipboard: { writeText: () => Promise.reject(new Error("NotAllowedError")) },
    })
    const routes = baseRoutes()
    routes["POST playground/runs"] = apiError(503, "unavailable", "runtime rt_01 is not connected")
    fakeStudio(routes)
    const el = await mount()
    click(button(el, "✎ Experiment"))
    await settle()
    click(button(el, "Run experiment ▶"))
    await settle()
    click(button(el, "keep as prompt ⤴"))
    await settle()
    t.release()
    expect(t.escaped).toEqual([])
  })
})

describe("the keyboard never takes the page's keys", () => {
  const key = (target: EventTarget, init: KeyboardEventInit) => {
    const e = new KeyboardEvent("keydown", { bubbles: true, composed: true, cancelable: true, ...init })
    target.dispatchEvent(e)
    return e
  }

  async function withDrawer() {
    fakeStudio(baseRoutes())
    const el = await mount()
    click(button(el, "✎ Experiment"))
    await settle()
    return el
  }

  it("r and ? typed into the panel's own fields are typing, not shortcuts", async () => {
    const el = await withDrawer()
    const prompt = $(el, ".weft-drawer textarea") as HTMLTextAreaElement
    prompt.focus()
    const r = key(prompt, { key: "r" })
    const q = key(prompt, { key: "?" })
    const esc = key(prompt, { key: "Escape" })
    await settle()
    expect(r.defaultPrevented).toBe(false)
    expect(q.defaultPrevented).toBe(false)
    expect(esc.defaultPrevented).toBe(false)
    expect($(el, ".weft-raw")).toBeNull()
    expect($(el, ".weft-keys")).toBeNull()
    expect($(el, ".weft-dock")).toBeTruthy()
  })

  it("Ctrl+R and Cmd+R stay the browser's; a composing key is ignored; a handled key is left alone", async () => {
    const el = await withDrawer()
    for (const init of [
      { key: "r", ctrlKey: true },
      { key: "r", metaKey: true },
      { key: "R", ctrlKey: true, shiftKey: true },
      { key: "r", isComposing: true },
      { key: "?", isComposing: true },
    ]) {
      const e = key(window, init)
      expect(e.defaultPrevented, JSON.stringify(init)).toBe(false)
    }
    // The page handled it first (its own shortcut): not the panel's.
    const taken = new KeyboardEvent("keydown", { key: "r", cancelable: true })
    const first = (e: Event) => e.preventDefault()
    window.addEventListener("keydown", first, true)
    window.dispatchEvent(taken)
    window.removeEventListener("keydown", first, true)
    await settle()
    expect($(el, ".weft-raw")).toBeNull()
  })

  it("a bare key aimed at one of the page's own widgets is the page's", async () => {
    const el = await withDrawer()
    const widget = document.createElement("div")
    widget.tabIndex = 0
    document.body.appendChild(widget)
    widget.focus()
    const r = key(widget, { key: "r" })
    await settle()
    expect(r.defaultPrevented).toBe(false)
    expect($(el, ".weft-raw")).toBeNull()
    // …and with nothing focused, r is the raw toggle.
    const idle = key(document.body, { key: "r" })
    await settle()
    expect(idle.defaultPrevented).toBe(true)
    expect($(el, ".weft-raw")).toBeTruthy()
  })

  it("Esc aimed at one of the page's own widgets (its dialog) is the page's: the dock stays open", async () => {
    const el = await withDrawer()
    const dialog = document.createElement("div")
    dialog.tabIndex = 0
    document.body.appendChild(dialog)
    dialog.focus()
    key(dialog, { key: "Escape" })
    await settle()
    expect($(el, ".weft-dock")).toBeTruthy()
    // With nothing focused, Esc closes the dock.
    dialog.blur()
    key(document.body, { key: "Escape" })
    await settle()
    expect($(el, ".weft-dock")).toBeNull()
  })
})

describe("the redraw keeps what is the user's", () => {
  it("typing in the drawer keeps focus and caret, and a frame arriving mid-edit does not take them", async () => {
    fakeStudio(baseRoutes())
    const el = await mount()
    click(button(el, "✎ Experiment"))
    await settle()
    const prompt = $(el, ".weft-drawer textarea") as HTMLTextAreaElement
    prompt.focus()
    prompt.value = "Always include the tracking link."
    prompt.setSelectionRange(6, 6)
    prompt.dispatchEvent(new Event("input", { bubbles: true }))
    await settle()
    let active = el.shadowRoot?.activeElement as HTMLTextAreaElement | null
    expect(active?.tagName).toBe("TEXTAREA")
    expect(active?.value).toBe("Always include the tracking link.")
    // A run frame redraws the whole dock under the field.
    FakeEventSource.last("public_id=")!.emit("run", { run: runRow({ steps: 7 }) })
    await settle()
    expect(text(el, ".weft-turns")).toContain("7 steps")
    active = el.shadowRoot?.activeElement as HTMLTextAreaElement | null
    expect(active?.tagName).toBe("TEXTAREA")
    expect(active?.value).toBe("Always include the tracking link.")
    expect(active?.selectionStart).toBe(6)
  })

  it("a press inside the dock holds the redraw until it is released (the click lands on the pressed button)", async () => {
    fakeStudio(baseRoutes())
    const el = await mount()
    const close = button(el, "–")!
    close.dispatchEvent(new Event("pointerdown", { bubbles: true, composed: true }))
    FakeEventSource.last("public_id=")!.emit("run", { run: runRow({ steps: 7 }) })
    await settle()
    expect(close.isConnected).toBe(true) // still the node under the pointer
    expect(text(el, ".weft-turns")).not.toContain("7 steps")
    window.dispatchEvent(new Event("pointerup"))
    await settle()
    expect(text(el, ".weft-turns")).toContain("7 steps")
  })

  it("a release the window never reports (the pointer left the page) does not freeze the dock", async () => {
    fakeStudio(baseRoutes())
    const el = await mount()
    vi.useFakeTimers({ shouldAdvanceTime: true })
    button(el, "–")!.dispatchEvent(new Event("pointerdown", { bubbles: true, composed: true }))
    FakeEventSource.last("public_id=")!.emit("run", { run: runRow({ steps: 7 }) })
    await vi.advanceTimersByTimeAsync(100)
    expect(text(el, ".weft-turns")).not.toContain("7 steps")
    await vi.advanceTimersByTimeAsync(2_000) // no pointerup, no pointercancel
    expect(text(el, ".weft-turns")).toContain("7 steps")
  })

  it("a composition that never reports its end (the field lost focus) does not freeze the dock", async () => {
    fakeStudio(baseRoutes())
    const el = await mount()
    click(button(el, "✎ Experiment"))
    await settle()
    const prompt = $(el, ".weft-drawer textarea") as HTMLTextAreaElement
    prompt.focus()
    prompt.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true, composed: true }))
    FakeEventSource.last("public_id=")!.emit("run", { run: runRow({ steps: 7 }) })
    await settle()
    expect(text(el, ".weft-turns")).not.toContain("7 steps") // composing: the field is left alone
    prompt.blur() // no compositionend arrives
    await settle()
    expect(text(el, ".weft-turns")).toContain("7 steps")
    // …and a later frame draws at once.
    FakeEventSource.last("public_id=")!.emit("run", { run: runRow({ steps: 8 }) })
    await settle()
    expect(text(el, ".weft-turns")).toContain("8 steps")
  })

  it("reasoning the user opened stays open across a redraw", async () => {
    const routes = baseRoutes()
    routes["runs/s_01-t1/transcript"] = transcript(
      [user("where is my order #4411?")],
      [{ role: "assistant", content: [{ type: "reasoning", text: "think first" }, { type: "text", text: "Shipped." }] }]
    )
    fakeStudio(routes)
    const el = await mount()
    const details = $(el, "details.weft-collapsible") as HTMLDetailsElement
    expect(details.textContent).toContain("think first")
    details.setAttribute("open", "")
    details.dispatchEvent(new Event("toggle")) // as the browser fires it: it does not bubble
    click($(el, "[data-weft-step]")) // marks the step: a redraw
    await settle()
    expect(($(el, "details.weft-collapsible") as HTMLDetailsElement).hasAttribute("open")).toBe(true)
  })

  it("the subagent expander loads on the toggle a browser fires (it does not bubble)", async () => {
    const child = { id: "s_01-t1/0/c1", parent_call_id: "c1" }
    const routes = baseRoutes()
    routes["runs/s_01-t1"] = { ...runRow({}), children: [runRow({ ...child, session_id: "" })] }
    routes["runs/s_01-t1/events?after=0&limit=500"] = page([
      ...runEvents("s_01-t1").slice(0, 2),
      { type: "tool_start", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "research", args: {} },
      { type: "tool_finish", run_id: "s_01-t1", seq: 1, call_id: "c1", name: "research", content: "done", is_error: false },
      ...runEvents("s_01-t1").slice(2),
    ])
    routes["runs/s_01-t1/0/c1/events?after=0&limit=500"] = page(runEvents("s_01-t1/0/c1"))
    routes["runs/s_01-t1/0/c1/transcript"] = transcript([user("dig")], [assistant("the child's findings")])
    const studio = fakeStudio(routes)
    const el = await mount()
    const expander = $(el, "[data-weft-child]") as HTMLDetailsElement
    expander.setAttribute("open", "")
    expander.dispatchEvent(new Event("toggle"))
    await settle()
    expect(text(el, "[data-weft-child]")).toContain("the child's findings")
    // Every redraw re-creates the open expander; the child loads once.
    FakeEventSource.last("public_id=")!.emit("run", { run: runRow({ steps: 2 }) })
    await settle()
    expect(studio.gets("runs/s_01-t1/0/c1/events").length).toBe(1)
  })

  it("the raw view and the shortcuts overlay sit inside the dock, not over the page", async () => {
    fakeStudio(baseRoutes())
    const el = await mount()
    click(button(el, "raw"))
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "?" }))
    await settle()
    expect($(el, ".weft-dock > .weft-raw")).toBeTruthy()
    expect($(el, ".weft-dock > .weft-keys")).toBeTruthy()
  })
})

describe("the live streams end", () => {
  it("a stream that closed for good is reopened a bounded number of times, then left alone", async () => {
    const studio = fakeStudio(baseRoutes())
    await mount()
    vi.useFakeTimers()
    // A refused token: every EventSource closes at once, for an hour.
    for (let minute = 0; minute < 60; minute++) {
      for (const es of FakeEventSource.live("public_id=")) es.fail()
      await vi.advanceTimersByTimeAsync(60_000)
    }
    const opened = FakeEventSource.instances.filter((i) => i.url.includes("public_id=")).length
    expect(opened).toBe(6) // the first, and five reopens (5 s, doubling)
    expect(studio.gets("runs?public_id=").length).toBe(6)
  })

  it("a tail the panel closed itself never reports: selecting another turn is not undone", async () => {
    const live = runRow({ id: "s_01-t2", turn: 2, status: "running", finished: null })
    const routes = baseRoutes()
    routes["runs?public_id=pub_orders&limit=50"] = { total: 2, runs: [live, runRow({})], next_before: null }
    routes["runs/s_01-t2"] = { ...live, children: [] }
    routes["runs/s_01-t2/events?after=0&limit=500"] = page(runEvents("s_01-t2").slice(0, 2))
    fakeStudio(routes)
    const el = await mount()
    expect(text(el, ".weft-sel")).toContain("s_01-t2") // the running turn opens
    vi.useFakeTimers()
    FakeEventSource.last("run=s_01-t2")!.fail(FakeEventSource.CONNECTING) // a blip: the browser retries
    await vi.advanceTimersByTimeAsync(2_000)
    click(all(el, ".weft-turn").find((n) => n.textContent.includes("s_01-t1")))
    await vi.advanceTimersByTimeAsync(30_000)
    expect(text(el, ".weft-sel")).toContain("s_01-t1")
    expect(FakeEventSource.live("run=s_01-t2")).toHaveLength(0)
  })

  it("the dev list (no public id) has no subscription and does not paint the live dot", async () => {
    const routes = baseRoutes()
    routes["runs?limit=10"] = { total: 1, runs: [runRow({})], next_before: null }
    fakeStudio(routes)
    const el = await mount({ "data-endpoint": ATTRS["data-endpoint"], "data-open": "true" })
    expect(text(el, ".weft-title")).toBe("latest (dev)")
    expect(FakeEventSource.instances).toHaveLength(0)
    expect($(el, ".weft-dot")?.getAttribute("title")).toBe("history")
    expect($(el, ".weft-dot.weft-on, .weft-dot.weft-run")).toBeNull()
  })

  it("a stream's dedup set is bounded: a tail open for a long run does not grow by one key per delta", () => {
    const got: number[] = []
    const h = openPanelLive(
      { base: "http://studio.test/studio/", token: "" },
      { selector: { run: "r1" }, kinds: ["delta"], onRecord: (r) => got.push(r.pos) }
    )
    const es = FakeEventSource.last("run=r1")!
    const delta = (pos: number) =>
      es.emit("record", { run_id: "r1", kind: "delta", pos, time: T0, event: { type: "text_delta", run_id: "r1", text: "x" } })
    const max = 1 << 16 // studio/live.go's liveDedupSize: the same window on both sides
    for (let pos = 0; pos <= max; pos++) delta(pos)
    delta(max) // a repeat inside the window: deduped
    delta(0) // a repeat older than the window: delivered again (bounded, never wrong — S4.5)
    h.close()
    expect(got.length).toBe(max + 2)
    expect(got.at(-1)).toBe(0)
  })

  it("the conversation's stream carries run frames only", async () => {
    fakeStudio(baseRoutes())
    await mount()
    const scope = new URL(FakeEventSource.last("public_id=")!.url)
    expect(scope.searchParams.get("kinds")).toBe("run")
  })
})

describe("the token goes to data-endpoint and nowhere else (§6)", () => {
  it("bearer on fetches, ?token= on the EventSource only, never in a link to Studio", async () => {
    const routes = baseRoutes()
    routes["POST playground/runs"] = apiError(503, "unavailable", "runtime rt_01 is not connected")
    const studio = fakeStudio(routes)
    const el = await mount({ ...ATTRS, "data-token": "dev_secret_tok" })
    click(button(el, "✎ Experiment"))
    await settle()
    click(button(el, "Run experiment ▶"))
    await settle()
    expect(studio.calls.length).toBeGreaterThan(5)
    for (const c of studio.calls) expect(c.headers.Authorization).toBe("Bearer dev_secret_tok")
    for (const call of studio.fetchMock.mock.calls) {
      const u = new URL(String(call[0]))
      expect(u.origin + u.pathname.slice(0, 12)).toBe("http://studio.test/studio/api/")
      expect(u.search).not.toContain("secret")
    }
    for (const es of FakeEventSource.instances) {
      const u = new URL(es.url)
      expect(u.origin).toBe("http://studio.test")
      expect(u.searchParams.get("token")).toBe("dev_secret_tok")
    }
    const links = all(el, "a").map((a) => a.getAttribute("href") ?? "")
    expect(links.length).toBe(3) // ⤢, save as fixture, compare in Studio
    for (const href of links) {
      expect(href.startsWith("http://studio.test/studio/")).toBe(true)
      expect(href).not.toContain("secret")
    }
  })

  it("a run id is one encoded path segment of the deep link, whatever it holds", () => {
    const base = "http://studio.test/studio/"
    expect(studioLink(base, "a/b?x=1#frag")).toBe(`${base}runs/a%2Fb%3Fx%3D1%23frag`)
    expect(studioLink(base, "../../evil")).toBe(`${base}runs/..%2F..%2Fevil`)
    expect(new URL(studioLink(base, "javascript:alert(1)")).protocol).toBe("http:")
  })
})

// The instance check keeps the import a value import (the class is
// what the testkit registers).
it("the element under test is the panel's", async () => {
  fakeStudio(baseRoutes())
  expect(await mount()).toBeInstanceOf(WeftDevtools)
})
