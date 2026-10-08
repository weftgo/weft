// The panel's live streams open with a live grant (plan C5): POST
// {endpoint}api/live-grant with the bearer in the header, then
// GET api/live?<selector>&kinds=…&sig=… — never a token in a URL. The
// fake Studio mirrors studio/livegrant.go (one selector, the exact
// kinds set, 60 s, ?token= refused), so a stream opened any other way
// is refused here as it would be there.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { openPanelLive } from "./client"
import {
  $,
  baseRoutes,
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
  ATTRS,
} from "./testkit"

beforeEach(setup)
afterEach(teardown)

const panelToken = (scope: "read" | "playground", exp = Date.now() + 3600_000, publicId = "pub_orders") =>
  `weft_pt.${btoa(JSON.stringify({ public_id: publicId, scope, exp: new Date(exp).toISOString() }))
    .replace(/=+$/, "")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")}.c2ln`

const EP = "http://studio.test/studio/"

/** A conversation whose one turn is still running: the panel holds the
 * conversation's stream (public_id) and the turn's tail (run). */
function liveRoutes() {
  const routes = baseRoutes()
  const running = runRow({ status: "running", finished: null })
  routes["runs?public_id=pub_orders&limit=50"] = { total: 1, runs: [running], next_before: null }
  routes["runs/s_01-t1"] = { ...running, children: [] }
  routes["runs/s_01-t1/events?after=0&limit=500"] = page(runEvents("s_01-t1").slice(0, 2), { done: false })
  return routes
}

describe("every setup opens its streams with a grant", () => {
  const setups: [string, string][] = [
    ["setup A (no token: the grant is asked for with no header)", ""],
    ["a server token", "dev_server_tok"],
    ["a read panel token", panelToken("read")],
    ["a playground panel token", panelToken("playground")],
  ]
  for (const [name, token] of setups) {
    it(name, async () => {
      const studio = fakeStudio(liveRoutes())
      const el = await mount(token ? { ...ATTRS, "data-token": token } : ATTRS)
      const grants = studio.posts("live-grant")
      expect(grants.map((g) => g.body)).toEqual(
        expect.arrayContaining([
          { public_id: "pub_orders", kinds: "run" },
          { run: "s_01-t1", kinds: "event,delta,run" },
        ])
      )
      for (const g of grants) {
        expect(g.path).toBe("live-grant") // the stream named in the body, not the query
        expect(g.headers.Authorization).toBe(token ? `Bearer ${token}` : undefined)
      }
      const streams = FakeEventSource.instances
      expect(streams.length).toBe(2)
      for (const es of streams) {
        expect(es.refused).toBeNull()
        expect(es.url).not.toContain("token=")
        if (token) expect(es.url).not.toContain(token)
        es.opened()
      }
      // The tail works: a delta folds into the open turn.
      sendDelta(FakeEventSource.last("run=s_01-t1"))
      await settle()
      expect(text(el, ".weft-main")).toContain("Checking")
      expect($(el, ".weft-dot")?.className).toMatch(/weft-(on|run)/)
    })
  }
})

const sendDelta = (es: FakeEventSource | undefined) =>
  es?.emit("record", {
    run_id: "s_01-t1",
    kind: "delta",
    pos: 0,
    time: "2026-10-01T09:00:00Z",
    event: { type: "text_delta", run_id: "s_01-t1", text: "Checking" },
  })

describe("the grant's lifetime", () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  const flush = () => vi.advanceTimersByTimeAsync(0)

  it("a drop inside the grant's 60 s is left to the browser (Last-Event-ID resumes it)", async () => {
    const studio = fakeStudio({})
    const onOpen = vi.fn()
    const h = openPanelLive({ base: EP, token: "dev_server_tok" }, { selector: { run: "r1" }, onOpen })
    await flush()
    const es = FakeEventSource.last("run=r1")!
    es.opened()
    vi.advanceTimersByTime(30_000)
    es.fail(FakeEventSource.CONNECTING)
    vi.advanceTimersByTime(3000)
    es.retry()
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(studio.posts("live-grant")).toHaveLength(1)
    expect(onOpen.mock.calls).toEqual([[false], [true]])
    h.close()
  })

  it("a reconnect after the grant's 60 s asks for a new grant and reopens (onOpen(true): refetch)", async () => {
    const studio = fakeStudio({})
    const onOpen = vi.fn()
    const onOverflow = vi.fn()
    const h = openPanelLive(
      { base: EP, token: "dev_server_tok" },
      { selector: { run: "r1" }, kinds: ["event", "delta", "run"], onOpen, onOverflow }
    )
    await flush()
    const first = FakeEventSource.last("run=r1")!
    first.opened()
    vi.advanceTimersByTime(61_000)
    first.fail(FakeEventSource.CONNECTING) // the browser would retry with the spent sig
    expect(first.readyState).toBe(FakeEventSource.CLOSED)
    await flush()
    const grants = studio.posts("live-grant")
    expect(grants).toHaveLength(2)
    expect(grants[1].headers.Authorization).toBe("Bearer dev_server_tok")
    const second = FakeEventSource.instances[1]
    expect(second.refused).toBeNull()
    second.opened()
    expect(onOpen.mock.calls).toEqual([[false], [true]])
    expect(onOverflow).not.toHaveBeenCalled()
    h.close()
  })

  it("the browser's own retry refused for a spent sig is replaced by a new grant too", async () => {
    const studio = fakeStudio({})
    const h = openPanelLive({ base: EP, token: "" }, { selector: { run: "r1" } })
    await flush()
    const first = FakeEventSource.last("run=r1")!
    first.opened()
    vi.advanceTimersByTime(59_000)
    first.fail(FakeEventSource.CONNECTING) // inside the grant: left to the browser
    expect(studio.posts("live-grant")).toHaveLength(1)
    vi.advanceTimersByTime(3000)
    first.retry() // 62 s: the fake's /api/live refuses the sig
    await flush()
    expect(studio.posts("live-grant")).toHaveLength(2)
    FakeEventSource.instances[1].opened()
    vi.advanceTimersByTime(60_000)
    expect(FakeEventSource.instances[1].readyState).toBe(FakeEventSource.OPEN)
    h.close()
  })

  it("a refused grant is silence: the caller hears closed once, nothing reaches the console", async () => {
    const quiet = [vi.spyOn(console, "error"), vi.spyOn(console, "warn"), vi.spyOn(console, "log")]
    fakeStudio({ "POST live-grant": () => new Response('{"error":{"code":"unauthorized","message":"bad or expired token"}}', { status: 401 }) })
    const onOverflow = vi.fn()
    openPanelLive({ base: EP, token: "stale" }, { selector: { run: "r1" }, onOverflow })
    await flush()
    expect(FakeEventSource.instances).toHaveLength(0)
    expect(onOverflow.mock.calls).toEqual([["closed"]])
    for (const spy of quiet) expect(spy).not.toHaveBeenCalled()
  })

  it("an expired frame with no fresh bearer stops quietly: expired, no grant, no console", async () => {
    const quiet = [vi.spyOn(console, "error"), vi.spyOn(console, "warn"), vi.spyOn(console, "log")]
    const studio = fakeStudio({})
    const tok = panelToken("read", Date.now() + 30_000)
    const onOverflow = vi.fn()
    openPanelLive({ base: EP, token: tok }, { selector: { public_id: "pub_orders" }, kinds: ["run"], onOverflow })
    await flush()
    const es = FakeEventSource.last("public_id=")!
    es.opened()
    vi.advanceTimersByTime(30_000)
    es.expire()
    await vi.advanceTimersByTimeAsync(10 * 60_000)
    expect(es.readyState).toBe(FakeEventSource.CLOSED)
    expect(onOverflow.mock.calls).toEqual([["expired"]])
    expect(studio.posts("live-grant")).toHaveLength(1)
    expect(FakeEventSource.instances).toHaveLength(1)
    for (const spy of quiet) expect(spy).not.toHaveBeenCalled()
  })

  it("an expired frame with a fresh bearer from the host asks for a new grant with it and reopens", async () => {
    const studio = fakeStudio({})
    const ep = { base: EP, token: panelToken("read", Date.now() + 30_000) }
    const onOpen = vi.fn()
    const onOverflow = vi.fn()
    const h = openPanelLive(ep, { selector: { public_id: "pub_orders" }, kinds: ["run"], onOpen, onOverflow })
    await flush()
    const es = FakeEventSource.last("public_id=")!
    es.opened()
    vi.advanceTimersByTime(30_000)
    ep.token = panelToken("read", Date.now() + 3600_000) // a new token, handed over
    es.expire()
    await flush()
    const grants = studio.posts("live-grant")
    expect(grants).toHaveLength(2)
    expect(grants[1].headers.Authorization).toBe(`Bearer ${ep.token}`)
    FakeEventSource.instances[1].opened()
    expect(onOpen.mock.calls).toEqual([[false], [true]])
    expect(onOverflow).not.toHaveBeenCalled()
    h.close()
  })

  it("close() before the grant answers opens nothing", async () => {
    fakeStudio({})
    const h = openPanelLive({ base: EP, token: "" }, { selector: { run: "r1" } })
    h.close()
    await flush()
    expect(FakeEventSource.instances).toHaveLength(0)
  })
})

describe("a panel token's stream that expired, in the panel", () => {
  it("shows no live, asks nothing again, and says nothing to the console", async () => {
    const quiet = [vi.spyOn(console, "error"), vi.spyOn(console, "warn"), vi.spyOn(console, "log")]
    const studio = fakeStudio(baseRoutes())
    const el = await mount({ ...ATTRS, "data-token": panelToken("read") })
    const es = FakeEventSource.last("public_id=")!
    es.opened()
    await settle()
    expect($(el, ".weft-dot")?.className).toContain("weft-on")
    const asked = studio.posts("live-grant").length
    es.expire()
    await settle(50)
    expect($(el, ".weft-dot")?.className).not.toContain("weft-on")
    expect(studio.posts("live-grant").length).toBe(asked)
    expect(FakeEventSource.live("public_id=")).toHaveLength(0)
    expect(text(el, ".weft-title")).toContain("pub_orders") // the panel stays, history only
    for (const spy of quiet) expect(spy).not.toHaveBeenCalled()
  })
})
