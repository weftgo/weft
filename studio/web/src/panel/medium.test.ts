// U28, the panel's medium polish, driven on arena-shaped fixtures (the
// rw.* rows the arena seeds: acme-support, wefttest/script, a replay of
// t1 nested under it): the turn column resizes with the dock and its
// rows never overflow (UX-D-11); the Studio-only views each get their ⤢
// (UX-F-03); a Studio that stops answering after boot is said under the
// header, retried on a bounded backoff, and cleared when it answers
// again (UX-F-09). The refusal said once (UX-D-17) is appturn.test.ts's.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  $,
  all,
  baseRoutes,
  click,
  FakeEventSource,
  fakeStudio,
  mount,
  runRow,
  settle,
  setup,
  teardown,
  text,
} from "./testkit"
import type { Route } from "./testkit"
import { PANEL_CSS } from "./styles"
import { NARROW_W } from "./layout"
import { agentsLink, HANDOFF, href, liveLink, runsLink } from "../lib/links"

beforeEach(setup)
afterEach(teardown)

const BASE = "http://studio.test/studio/"
const LIST = "runs?public_id=pub_orders&limit=50"

/** The arena's rw.deep-delegation shape: t1 and a replay of it from
 * step 0 (an experiment), whose source link reads "↖ t1 step 0". */
function withReplays(ids: [string, string][]): Record<string, Route> {
  const r = baseRoutes()
  r[LIST] = {
    total: 1 + ids.length,
    runs: [
      runRow({ steps: 2 }),
      ...ids.map(([id, exp]) =>
        runRow({
          id,
          playground: true,
          session_id: "",
          turn: 0,
          experiment_id: exp,
          forked_from: "s_01-t1#0",
        })
      ),
    ],
    next_before: null,
  }
  return r
}

describe("the turn column (UX-D-11)", () => {
  /** The column's width at a dock width, as the CSS clamp computes it. */
  const column = (dock: number) => {
    const m = /\.weft-turns \{\s*width: clamp\((\d+)px, (\d+)%, (\d+)px\)/.exec(
      PANEL_CSS
    )!
    const [lo, pct, hi] = m.slice(1).map(Number)
    return Math.min(Math.max((dock * pct) / 100, lo), hi)
  }

  it("resizes with the dock: 240 px at its narrowest two-column width, 28% between, 420 px at most", () => {
    expect(column(NARROW_W)).toBe(240)
    expect(column(1440)).toBeCloseTo(403.2)
    expect(column(2560)).toBe(420)
    // Under NARROW_W the column is a dropdown and the rows' column
    // takes the dock's width (the narrow rules).
    expect(PANEL_CSS).toContain(
      ".weft-narrow .weft-turns { width: auto; border-right: none; max-height: 30%; }"
    )
  })

  it("its rows never overflow: row 2 wraps its fields, each field and the source link ellipsize", () => {
    expect(PANEL_CSS).toMatch(/\.weft-turn \{[^}]*overflow: hidden;/)
    expect(PANEL_CSS).toContain(
      ".weft-turn .weft-row2 { display: flex; flex-wrap: wrap; gap: 0 8px;"
    )
    expect(PANEL_CSS).toContain(
      ".weft-turn .weft-row1 > *, .weft-turn .weft-row2 > *, .weft-src { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 100%; }"
    )
    expect(PANEL_CSS).toMatch(
      /\.weft-src \{[^}]*display: inline-block; max-width: calc\(100% - 24px\);/
    )
  })

  it("a replay's way back reads '↖ t1 step 0', never the source's whole id", async () => {
    fakeStudio(withReplays([["pg_1c06797f5df3b4e908d846af222", "exp_1"]]))
    const el = await mount()
    const src = all(el, ".weft-src").find((n) => n.textContent.startsWith("↖"))!
    expect(src.textContent).toBe("↖ t1 step 0")
  })
})

describe("the Studio-only views' hand-offs (UX-F-03)", () => {
  const link = (el: Element, label: string) =>
    all(el as never, "a").find((a) => a.textContent === label) as
      HTMLAnchorElement | undefined

  it("the header's agent opens its card on /agents", async () => {
    fakeStudio(baseRoutes())
    const el = await mount()
    const a = $(el, ".weft-title a") as HTMLAnchorElement
    expect(text(el, ".weft-title")).toBe("acme-support · pub_orders")
    expect(a.textContent).toBe("acme-support")
    expect(a.getAttribute("href")).toBe(href(BASE, agentsLink("acme-support")))
    expect(a.getAttribute("href")).toBe(`${BASE}agents?agent=acme-support`)
    expect(a.getAttribute("target")).toBe("_blank")
  })

  it("a running turn's dot opens the live page on its agent; idle, the dot is no link", async () => {
    const r = baseRoutes()
    r[LIST] = {
      total: 1,
      runs: [runRow({ status: "running", finished: null })],
      next_before: null,
    }
    r["runs/s_01-t1"] = {
      ...runRow({ status: "running", finished: null }),
      children: [],
    }
    fakeStudio(r)
    const el = await mount()
    const dot = $(el, ".weft-head a.weft-dot") as HTMLAnchorElement
    expect(dot.getAttribute("href")).toBe(
      href(BASE, liveLink({ agent: "acme-support" }))
    )
    expect(dot.getAttribute("aria-label")).toBe(HANDOFF.live)
    teardown()
    setup()
    fakeStudio(baseRoutes())
    const idle = await mount()
    expect($(idle, ".weft-head a.weft-dot")).toBeNull()
    expect($(idle, ".weft-head span.weft-dot")).not.toBeNull()
  })

  it("the turn filter carries its text and status to the runs list's filters (a decided park, a read-time word only the panel has, is left out)", async () => {
    fakeStudio(baseRoutes())
    const el = await mount()
    const q = $(el, ".weft-turn-q") as HTMLInputElement
    q.value = "order"
    q.dispatchEvent(new Event("input", { bubbles: true }))
    const st = $(el, ".weft-tq-status") as HTMLSelectElement
    st.value = "failed"
    st.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    const a = link(el, HANDOFF.filters)!
    expect(a.getAttribute("href")).toBe(
      href(
        BASE,
        runsLink({ public_id: "pub_orders", q: "order", status: "failed" })
      )
    )
    expect(new URL(a.href).pathname).toBe("/studio/runs")
    st.value = "resumed"
    st.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    expect(link(el, HANDOFF.filters)!.getAttribute("href")).toBe(
      href(BASE, runsLink({ public_id: "pub_orders", q: "order" }))
    )
    // Not filtering: no count line, no hand-off.
    q.value = ""
    q.dispatchEvent(new Event("input", { bubbles: true }))
    st.value = ""
    st.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    expect(link(el, HANDOFF.filters)).toBeUndefined()
  })

  it("a turn's replays open in the runs list: their experiment's runs when they share one", async () => {
    fakeStudio(withReplays([["pg_a", "exp_1"]]))
    const el = await mount()
    const a = link(el, HANDOFF.replays(1))!
    expect(a.textContent).toBe("1 replay in Studio ⤢")
    expect(a.getAttribute("href")).toBe(
      href(
        BASE,
        runsLink({
          public_id: "pub_orders",
          experiments: "on",
          experiment_id: "exp_1",
        })
      )
    )
    // Inside t1's own list item: a role=list holds only its items.
    expect(a.closest('[role="listitem"]')?.getAttribute("data-key")).toBe(
      "s_01-t1"
    )
  })

  it("replays of several experiments: the conversation's experiments", async () => {
    fakeStudio(
      withReplays([
        ["pg_a", "exp_1"],
        ["pg_b", "exp_2"],
      ])
    )
    const el = await mount()
    expect(link(el, HANDOFF.replays(2))!.getAttribute("href")).toBe(
      href(BASE, runsLink({ public_id: "pub_orders", experiments: "on" }))
    )
  })
})

describe("Studio stops answering after boot (UX-F-09)", () => {
  /** A fake Studio that can be stopped: every request then fails as a
   * refused connection does (fetch rejects), or answers 401 when
   * walled; the fake still counts each one. */
  function stoppable(routes = baseRoutes()) {
    const st = {
      down: false,
      walled: false,
      /** Set: a meta read waits for it (a read in flight). */
      hold: null as Promise<void> | null,
    }
    const studio = fakeStudio(routes)
    vi.stubGlobal(
      "fetch",
      async (input: RequestInfo | URL, init?: RequestInit) => {
        if (st.hold && String(input).includes("/api/meta")) {
          await st.hold
          return studio.fetchMock(input, init)
        }
        if (st.walled) {
          await studio.fetchMock(input, init)
          return new Response(JSON.stringify({ error: "unauthorized" }), {
            status: 401,
          })
        }
        if (st.down) {
          await studio.fetchMock(input, init)
          throw new TypeError("Failed to fetch")
        }
        return studio.fetchMock(input, init)
      }
    )
    return { st, studio }
  }
  const line = (el: Element) => $(el as never, ".weft-dock .weft-unreachable")

  it("says so under the header, keeps the list dimmed, retries on the bounded backoff and clears by itself", async () => {
    const { st, studio } = stoppable()
    const el = await mount()
    expect(line(el)).toBeNull()
    vi.useFakeTimers()
    st.down = true
    for (const es of FakeEventSource.live("public_id=")) es.fail()
    await vi.advanceTimersByTimeAsync(6_000)
    const said = line(el)!
    expect(said.getAttribute("role")).toBe("status")
    expect(said.textContent).toBe(`Studio not reachable at ${BASE} · retry`)
    // The list stays as it was, dimmed (the dock's weft-down).
    expect($(el, ".weft-dock")!.classList).toContain("weft-down")
    expect(all(el, ".weft-turn")).toHaveLength(1)
    // Bounded: an hour down reads the list a handful of times, no more.
    await vi.advanceTimersByTimeAsync(3_600_000)
    const reads = studio.gets(LIST).length
    expect(reads).toBeLessThanOrEqual(8)
    await vi.advanceTimersByTimeAsync(3_600_000)
    expect(studio.gets(LIST).length).toBe(reads)
    // Back: the retry reads it again, the line goes, the list is live.
    st.down = false
    click($(el, ".weft-dock .weft-retry"))
    await vi.advanceTimersByTimeAsync(100)
    expect(line(el)).toBeNull()
    expect($(el, ".weft-dock")!.classList).not.toContain("weft-down")
    expect(studio.gets(LIST).length).toBe(reads + 1)
  })

  it("recovers by itself when Studio answers within the backoff", async () => {
    const { st } = stoppable()
    const el = await mount()
    vi.useFakeTimers()
    st.down = true
    for (const es of FakeEventSource.live("public_id=")) es.fail()
    await vi.advanceTimersByTimeAsync(6_000)
    expect(line(el)).not.toBeNull()
    st.down = false
    // The next step of the lane's backoff (10 s) reopens and reads.
    await vi.advanceTimersByTimeAsync(11_000)
    expect(line(el)).toBeNull()
  })

  it("past the fast retries, a meta read a minute brings it back by itself (a weft dev restart minutes later)", async () => {
    const { st, studio } = stoppable()
    const el = await mount()
    vi.useFakeTimers()
    st.down = true
    for (const es of FakeEventSource.live("public_id=")) es.fail()
    // The lanes' bounded backoff is spent long before 9 minutes.
    await vi.advanceTimersByTimeAsync(9 * 60_000)
    expect(line(el)).not.toBeNull()
    const lists = studio.gets(LIST).length
    const metas = studio.gets("meta").length
    await vi.advanceTimersByTimeAsync(60_000)
    // Only the slow poll asked, one meta read.
    expect(studio.gets(LIST).length).toBe(lists)
    expect(studio.gets("meta").length).toBe(metas + 1)
    st.down = false
    await vi.advanceTimersByTimeAsync(60_000)
    expect(line(el)).toBeNull()
    expect(studio.gets(LIST).length).toBeGreaterThan(lists)
  })

  it("meta answering while the list still fails restarts no lane backoff each minute (P10b-pre-web-fix)", async () => {
    const { st, studio } = stoppable()
    const el = await mount()
    vi.useFakeTimers()
    st.down = true
    for (const es of FakeEventSource.live("public_id=")) es.fail()
    await vi.advanceTimersByTimeAsync(9 * 60_000)
    expect(line(el)).not.toBeNull()
    const lane = () =>
      studio.calls.filter((c) => /live/.test(c.path)).length +
      FakeEventSource.instances.length
    const lanes = lane()
    // Meta answers again; the list still does not (a network failure).
    const real = globalThis.fetch
    vi.stubGlobal(
      "fetch",
      async (input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).includes("/api/meta")) {
          st.down = false
          try {
            return await real(input, init)
          } finally {
            st.down = true
          }
        }
        return real(input, init)
      }
    )
    const streams = FakeEventSource.instances.length
    for (let i = 0; i < 5; i++) {
      await vi.advanceTimersByTimeAsync(60_000)
      // Every lane that opens still fails: the spent backoff stays spent.
      for (const es of FakeEventSource.live("public_id=")) es.fail()
    }
    expect(line(el)).not.toBeNull()
    expect(FakeEventSource.instances.length).toBe(streams)
    expect(lane()).toBe(lanes)
  })

  it("removed while Studio is down: nothing reads again, nothing reopens (U30.1-fix)", async () => {
    const { st, studio } = stoppable()
    const el = await mount()
    vi.useFakeTimers()
    st.down = true
    for (const es of FakeEventSource.live("public_id=")) es.fail()
    await vi.advanceTimersByTimeAsync(6_000)
    expect(line(el)).not.toBeNull()
    el.remove()
    const reads = studio.calls.length
    const streams = FakeEventSource.instances.length
    await vi.advanceTimersByTimeAsync(30 * 60_000)
    expect(studio.calls.length).toBe(reads)
    expect(FakeEventSource.instances.length).toBe(streams)
  })

  it("a slow poll's meta read that lands after the element left opens no stream and reads no list (U30.1-fix)", async () => {
    const { st, studio } = stoppable()
    const el = await mount()
    vi.useFakeTimers()
    st.down = true
    for (const es of FakeEventSource.live("public_id=")) es.fail()
    await vi.advanceTimersByTimeAsync(9 * 60_000)
    let land = () => {}
    st.hold = new Promise<void>((r) => (land = r))
    const metas = studio.gets("meta").length
    await vi.advanceTimersByTimeAsync(60_000)
    // The read is in flight (counted once it lands): the element leaves.
    el.remove()
    const lists = studio.gets(LIST).length
    const streams = FakeEventSource.instances.length
    st.down = false
    land()
    await vi.advanceTimersByTimeAsync(5 * 60_000)
    expect(studio.gets("meta").length).toBe(metas + 1)
    expect(studio.gets(LIST).length).toBe(lists)
    expect(FakeEventSource.instances.length).toBe(streams)
    expect(FakeEventSource.live("public_id=")).toHaveLength(0)
  })

  it("a closed dock makes no slow-poll reads (U30.1-fix)", async () => {
    const { st, studio } = stoppable()
    const el = await mount()
    vi.useFakeTimers()
    st.down = true
    for (const es of FakeEventSource.live("public_id=")) es.fail()
    await vi.advanceTimersByTimeAsync(9 * 60_000)
    el.close()
    await vi.advanceTimersByTimeAsync(100)
    const metas = studio.gets("meta").length
    await vi.advanceTimersByTimeAsync(10 * 60_000)
    expect(studio.gets("meta").length).toBe(metas)
    // Open again: the next tick reads.
    el.open()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(studio.gets("meta").length).toBe(metas + 1)
  })

  it("a revoked token (meta 200, the list 401): the line and its retry, no slow poll resetting the backoff (U30.1-fix)", async () => {
    const { st, studio } = stoppable()
    const el = await mount()
    vi.useFakeTimers()
    st.walled = true
    for (const es of FakeEventSource.live("public_id=")) es.fail()
    await vi.advanceTimersByTimeAsync(9 * 60_000)
    expect(line(el)!.textContent).toContain("Studio needs a token")
    const metas = studio.gets("meta").length
    const lists = studio.gets(LIST).length
    await vi.advanceTimersByTimeAsync(60 * 60_000)
    expect(studio.gets("meta").length).toBe(metas)
    expect(studio.gets(LIST).length).toBe(lists)
  })

  it("a hidden page makes no reads while Studio is down", async () => {
    const { st, studio } = stoppable()
    const el = await mount()
    vi.useFakeTimers()
    st.down = true
    for (const es of FakeEventSource.live("public_id=")) es.fail()
    await vi.advanceTimersByTimeAsync(5 * 60_000)
    expect(line(el)).not.toBeNull()
    const vis = Object.getOwnPropertyDescriptor(
      Document.prototype,
      "visibilityState"
    )!
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      get: () => "hidden",
    })
    try {
      const reads = studio.calls.length
      await vi.advanceTimersByTimeAsync(10 * 60_000)
      expect(studio.calls.length).toBe(reads)
    } finally {
      delete (document as { visibilityState?: unknown }).visibilityState
      expect(
        Object.getOwnPropertyDescriptor(Document.prototype, "visibilityState")
      ).toEqual(vis)
    }
  })

  it("a 401 after boot is the token it wants, in the boot probe's words", async () => {
    const { st } = stoppable()
    const el = await mount()
    vi.useFakeTimers()
    st.walled = true
    for (const es of FakeEventSource.live("public_id=")) es.fail()
    await vi.advanceTimersByTimeAsync(6_000)
    expect(line(el)!.textContent).toBe(
      `Studio needs a token at ${BASE} — add data-token (the dev token) or a panel token (POST /api/panel-tokens) · retry`
    )
  })

  it("the dev list (no public id) polls once a minute while Studio is down, five times at most", async () => {
    const r = baseRoutes()
    r["runs?limit=10"] = { total: 1, runs: [runRow({})], next_before: null }
    const { st, studio } = stoppable(r)
    const el = await mount({
      "data-endpoint": BASE,
      "data-open": "true",
    })
    expect(all(el, ".weft-turn")).toHaveLength(1)
    vi.useFakeTimers()
    st.down = true
    for (const es of FakeEventSource.live("agent=")) es.fail()
    await vi.advanceTimersByTimeAsync(31_000)
    expect(line(el)).not.toBeNull()
    const before = studio.gets("runs?limit=10").length
    await vi.advanceTimersByTimeAsync(3_600_000)
    const polled = studio.gets("runs?limit=10").length - before
    expect(polled).toBeGreaterThan(0)
    expect(polled).toBeLessThanOrEqual(12)
    await vi.advanceTimersByTimeAsync(3_600_000)
    expect(studio.gets("runs?limit=10").length - before).toBe(polled)
  })
})
