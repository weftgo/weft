// U22 (UX-D-05, UX-D-08, UX-D-06, UX-S5a-03): the scope ladder's rung
// for a page of unrelated runs — a tag (`tag.<k>=<v>`), agent or flow
// scope with no conversation behind it — the flow that filters, the
// fallback that is a list (not a story), and a token-walled Studio
// that says so instead of vanishing.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { WeftDevtools } from "./element"
import { mount as mountPanel } from "./element"
import {
  $,
  all,
  apiError,
  baseRoutes,
  create,
  FakeEventSource,
  fakeStudio,
  runRow,
  settle,
  setup,
  teardown,
  text,
} from "./testkit"
import type { Route } from "./testkit"
import { grantLedger } from "../test/fake-live-grant"

const REMOTE = "https://studio.example/studio/"

/** A batch of 196 runs (the Done line's arena batch): the list's first
 * page holds two — of two agents, one a session turn — and the
 * runs page's total counts the whole match. */
function batchRoutes(): Record<string, Route> {
  const routes: Record<string, Route> = { ...baseRoutes() }
  routes["runs?tag.arena.batch=cli-20261012-115753&limit=50"] = {
    total: 196,
    runs: [
      runRow({
        id: "r_2",
        agent: "director",
        public_id: "arena:cli:rw.support",
        session_id: "s_1",
        turn: 3,
        started: "2026-10-01T09:05:00Z",
      }),
      runRow({ id: "r_1", agent: "incident-analyst", public_id: "" }),
    ],
    next_before: "2026-10-01T09:00:00Z",
  }
  routes["runs?flow=f_checkout&limit=50"] = {
    total: 1,
    runs: [runRow({ id: "r_flow", agent: "checkout", public_id: "" })],
    next_before: null,
  }
  return routes
}

async function mountWith(attrs: Record<string, string>): Promise<WeftDevtools> {
  const el = create(attrs)
  document.body.appendChild(el)
  await settle()
  return el
}

beforeEach(setup)
afterEach(() => {
  teardown()
  vi.restoreAllMocks()
})

describe("a tag scope: a page of unrelated runs (UX-D-05)", () => {
  it('data-scope="tag.arena.batch=…" reads "batch … · 196 runs", rows say "run" and name their agent, the live lane asks the tag', async () => {
    const studio = fakeStudio(batchRoutes())
    const el = await mountWith({
      "data-endpoint": REMOTE,
      "data-scope": "tag.arena.batch=cli-20261012-115753",
      "data-open": "true",
    })
    expect(text(el, ".weft-title")).toBe("batch cli-20261012-115753")
    expect(text(el, "[data-weft-stats]")).toMatch(/^196 runs · /)
    expect(text(el, ".weft-head")).not.toContain("how to scope")
    // No conversation: no sessions read, the list by the tag alone.
    expect(studio.gets("sessions")).toEqual([])
    expect(
      studio.gets("runs?tag.arena.batch=cli-20261012-115753&limit=50")
    ).toHaveLength(1)
    // Rows: "run", never "turn" — a session turn among them included.
    const rows = all(el, "[data-weft-run]")
    expect(
      rows.map((r) => r.getAttribute("aria-label")!.split(",")[0])
    ).toEqual(["run", "run"])
    expect(
      rows.map(
        (r) => (r.querySelector(".weft-ttl")?.textContent ?? "").split(" ")[0]
      )
    ).toEqual(["director", "incident-analyst"])
    expect($(el, ".weft-rows")!.getAttribute("aria-label")).toBe(
      "runs, newest first"
    )
    expect(text(el, ".weft-older")).toBe("older runs ↓")
    // The live lane: run frames of the batch (A5's selector).
    expect(
      FakeEventSource.last("tag.arena.batch=cli-20261012-115753")
    ).toBeTruthy()
  })

  it("a tag scope with a flow keeps the flow's chip (the title names the batch)", async () => {
    const routes = batchRoutes()
    routes["runs?flow=f_checkout&tag.arena.batch=b1&limit=50"] = {
      total: 0,
      runs: [],
      next_before: null,
    }
    fakeStudio(routes)
    const el = await mountWith({
      "data-endpoint": REMOTE,
      "data-scope": "tag.arena.batch=b1;flow=f_checkout",
      "data-open": "true",
    })
    expect(text(el, ".weft-title")).toBe("batch b1")
    expect(text(el, ".weft-scope-chip")).toBe("flow f_checkout")
    expect(text(el, ".weft-splash")).toBe("no runs yet — run your app")
  })

  it("an agent scope stands alone too", async () => {
    const routes = batchRoutes()
    routes["runs?agent=director&limit=50"] = {
      total: 1,
      runs: [runRow({ id: "r_d", agent: "director", public_id: "" })],
      next_before: null,
    }
    fakeStudio(routes)
    const el = await mountWith({
      "data-endpoint": REMOTE,
      "data-scope": ";agent=director",
      "data-open": "true",
    })
    expect(text(el, ".weft-title")).toBe("agent director")
    expect(text(el, "[data-weft-stats]")).toMatch(/^1 run · /)
  })
})

describe("the flow filters (UX-D-08, user decision 2)", () => {
  it('a page declaring ";flow=f_checkout" lists only the runs stamped weft.flow=f_checkout', async () => {
    const studio = fakeStudio(batchRoutes())
    const el = await mountWith({
      "data-endpoint": REMOTE,
      "data-scope": ";flow=f_checkout",
      "data-open": "true",
    })
    expect(studio.gets("runs?flow=f_checkout&limit=50")).toHaveLength(1)
    expect(studio.gets("runs?limit=10")).toEqual([]) // never the fallback
    expect(
      all(el, "[data-weft-run]").map((r) => r.getAttribute("data-weft-run"))
    ).toEqual(["r_flow"])
    expect(text(el, ".weft-title")).toBe("flow f_checkout")
    // The title names the flow: no second chip saying it again.
    expect($(el, ".weft-scope-chip")).toBeNull()
    expect(FakeEventSource.last("flow=f_checkout")).toBeTruthy()
  })
})

describe("the fallback is a list, not a story (UX-D-06)", () => {
  it("no scope: rows name their agent, none is selected, no summed cost", async () => {
    const routes = baseRoutes()
    routes["runs?limit=10"] = {
      total: 2,
      runs: [
        runRow({ id: "r_a", agent: "incident-analyst" }),
        runRow({ id: "r_b", agent: "director" }),
      ],
      next_before: null,
    }
    fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(
      all(el, "[data-weft-run]").map(
        (r) => (r.querySelector(".weft-ttl")?.textContent ?? "").split(" ")[0]
      )
    ).toEqual(["incident-analyst", "director"])
    expect($(el, "[data-weft-run][aria-current]")).toBeNull()
    expect($(el, "[data-weft-cost-total]")).toBeNull()
  })
})

describe("a token-walled Studio says so (UX-S5a-03)", () => {
  it("the script tag's own dock is not removed on a 401: one line, the fix, retry", async () => {
    fakeStudio(baseRoutes(), () =>
      apiError(401, "unauthorized", "this Studio needs its API token")
    )
    const dock = create({ "data-endpoint": REMOTE })
    dock.autoMounted = true
    document.body.appendChild(dock)
    await settle()
    expect(dock.isConnected).toBe(true)
    expect(text(dock, ".weft-unreachable")).toContain(
      `Studio needs a token at ${REMOTE} — add data-token (the dev token) or a panel token (POST /api/panel-tokens)`
    )
    expect($(dock, ".weft-unreachable button")).toBeTruthy()
  })

  it("a 403 alike; a network failure off loopback still removes the dock silently", async () => {
    fakeStudio(baseRoutes(), () => apiError(403, "forbidden", "no"))
    const walled = create({ "data-endpoint": REMOTE })
    walled.autoMounted = true
    document.body.appendChild(walled)
    await settle()
    expect(text(walled, ".weft-unreachable")).toContain("needs a token")
    walled.remove()
    fakeStudio(baseRoutes(), new Response("no studio", { status: 404 }))
    const gone = create({ "data-endpoint": REMOTE })
    gone.autoMounted = true
    document.body.appendChild(gone)
    await settle()
    expect(gone.isConnected).toBe(false)
  })

  it("a loopback Studio that is not running is said, not removed (dev)", async () => {
    fakeStudio(baseRoutes(), new Response("no studio", { status: 404 }))
    const dock = create({ "data-endpoint": "http://127.0.0.1:7341/" })
    dock.autoMounted = true
    document.body.appendChild(dock)
    await settle()
    expect(dock.isConnected).toBe(true)
    expect(text(dock, ".weft-unreachable")).toContain(
      "Studio not reachable at http://127.0.0.1:7341/"
    )
  })

  it("mount() against a 401 reads the same as the script tag", async () => {
    fakeStudio(baseRoutes(), () => apiError(401, "unauthorized", "token"))
    const el = mountPanel({ endpoint: REMOTE, open: true })
    await settle()
    expect(text(el, ".weft-unreachable")).toContain("Studio needs a token at")
  })
})

describe("U22-fix", () => {
  it("pub_…;agent=… opens its live lane: public_id and agent, the agent narrowing (finding 1)", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_orders&agent=acme-support&limit=50"] =
      routes["runs?public_id=pub_orders&limit=50"]
    const studio = fakeStudio(routes)
    await mountWith({
      "data-endpoint": REMOTE,
      "data-scope": "pub_orders;agent=acme-support",
      "data-open": "true",
    })
    expect(studio.posts("live-grant").map((g) => g.body)).toContainEqual(
      expect.objectContaining({
        public_id: "pub_orders",
        agent: "acme-support",
      })
    )
    const lane = FakeEventSource.last("public_id=pub_orders&agent=acme-support")
    expect(lane).toBeTruthy()
    expect(lane!.readyState).not.toBe(2) // the grant opened it
  })

  it("the header's session read and the experiments read carry the scope's filters (finding 2)", async () => {
    const routes = baseRoutes()
    const t1 = runRow({})
    routes["runs?session=s_01&flow=f_1&limit=50"] = {
      total: 1,
      runs: [t1],
      next_before: null,
    }
    const studio = fakeStudio(routes)
    await mountWith({
      "data-endpoint": REMOTE,
      "data-scope": "pub_orders;session=s_01;flow=f_1",
      "data-open": "true",
    })
    expect(studio.gets("sessions?q=s_01&flow=f_1")).toHaveLength(1)
    expect(
      studio.gets("runs?public_id=pub_orders&playground=1&flow=f_1&limit=50")
    ).toHaveLength(1)
  })

  it("a public id with a tag: one noun — the header, the list and the rows say run — and the tag is a chip (finding 3)", async () => {
    const routes = baseRoutes()
    routes["sessions?public_id=pub_orders&tag.arena.batch=b1"] =
      routes["sessions?public_id=pub_orders"]
    routes["runs?public_id=pub_orders&tag.arena.batch=b1&limit=50"] = {
      total: 1,
      runs: [runRow({ turn: 3 })],
      next_before: null,
    }
    fakeStudio(routes)
    const el = await mountWith({
      "data-endpoint": REMOTE,
      "data-scope": "pub_orders;tag.arena.batch=b1",
      "data-open": "true",
    })
    expect(text(el, "[data-weft-stats]")).toMatch(/^1 run · /)
    expect($(el, ".weft-rows")!.getAttribute("aria-label")).toBe(
      "runs, newest first"
    )
    expect(
      $(el, "[data-weft-run]")!.getAttribute("aria-label")!.split(",")[0]
    ).toBe("run")
    expect(all(el, ".weft-scope-chip").map((n) => n.textContent)).toEqual([
      "tag arena.batch=b1",
    ])
  })

  it("a second tag the title does not name is a chip too", async () => {
    const routes = baseRoutes()
    routes["runs?tag.arena.batch=b1&tag.ci.job=7&limit=50"] = {
      total: 0,
      runs: [],
      next_before: null,
    }
    fakeStudio(routes)
    const el = await mountWith({
      "data-endpoint": REMOTE,
      "data-scope": "tag.arena.batch=b1;tag.ci.job=7",
      "data-open": "true",
    })
    expect(text(el, ".weft-title")).toBe("batch b1")
    expect(all(el, ".weft-scope-chip").map((n) => n.textContent)).toEqual([
      "tag ci.job=7",
    ])
  })

  it("the fallback groups its rows by agent and keeps the live words behind 'how to scope' (UX-D-06)", async () => {
    const routes = baseRoutes()
    routes["runs?limit=10"] = {
      total: 3,
      runs: [
        runRow({ id: "r_1", agent: "incident-analyst" }),
        runRow({ id: "r_2", agent: "director" }),
        runRow({ id: "r_3", agent: "incident-analyst" }),
      ],
      next_before: null,
    }
    fakeStudio(routes)
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    expect(
      all(el, "[data-weft-run]").map((r) => r.getAttribute("data-weft-run"))
    ).toEqual(["r_1", "r_3", "r_2"])
    const lane = FakeEventSource.last("agent=incident-analyst")
    lane?.opened()
    await settle()
    expect($(el, ".weft-dev-poll")).toBeNull()
    ;($(el, ".weft-howto-btn") as HTMLElement).click()
    await settle()
    expect(text(el, ".weft-dev-poll")).toBe(
      "live: newest agent only (incident-analyst) · the others every 30 s"
    )
  })

  it("each 'how to scope' line has a copy button (UX-D-07)", async () => {
    const writeText = vi.fn(() => Promise.resolve())
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } })
    fakeStudio(baseRoutes())
    const el = await mountWith({ "data-endpoint": REMOTE, "data-open": "true" })
    ;($(el, ".weft-howto-btn") as HTMLElement).click()
    await settle()
    const buttons = all(el, ".weft-howto button")
    expect(buttons).toHaveLength(all(el, ".weft-howto code").length)
    ;(buttons[0] as HTMLElement).click()
    await settle()
    expect(writeText).toHaveBeenCalledWith(
      "add ?weft_scope=pub_… to this page's URL (no code)"
    )
    expect(text(el, ".weft-api-note")).toBe("copied")
  })
})

describe("a tag-scoped panel token (I4)", () => {
  const BATCH = "cli-20261012-115753"
  /** A read panel token minted with {"tags": {"arena.batch": BATCH}}
   * and no public id (POST /api/panel-tokens, server token). */
  const TAG_TOKEN = `weft_pt.${btoa(
    JSON.stringify({
      tags: { "arena.batch": BATCH },
      scope: "read",
      exp: new Date(Date.now() + 3600_000).toISOString(),
    })
  ).replace(/=+$/, "")}.sig`

  async function expectTheBatch(
    el: WeftDevtools,
    studio: ReturnType<typeof fakeStudio>
  ) {
    expect(text(el, ".weft-title")).toBe(`batch ${BATCH}`)
    // The panel's one read: the batch by its tag, under the token.
    expect(studio.gets(`runs?tag.arena.batch=${BATCH}&limit=50`)).toHaveLength(
      1
    )
    expect(studio.gets("sessions")).toEqual([])
    // Rows of every public id of the batch (one of none at all).
    expect(
      all(el, "[data-weft-run]").map((r) => r.getAttribute("data-weft-run"))
    ).toEqual(["r_2", "r_1"])
    // The live lane: granted to the tag token on the batch's stream.
    expect(
      grantLedger.some(
        (g) =>
          g.bearer === TAG_TOKEN &&
          g.stream.includes(`"tag.arena.batch","${BATCH}"`)
      )
    ).toBe(true)
    const lane = FakeEventSource.last(`tag.arena.batch=${BATCH}`)
    expect(lane).toBeTruthy()
    expect(lane!.readyState).not.toBe(2)
    // A run of the batch under yet another public id arrives live and
    // is listed (Studio's filter let it through: it carries the tag).
    lane!.opened()
    lane!.emit("run", {
      run: runRow({
        id: "r_3",
        agent: "director",
        public_id: "arena:cli:rw.other",
        started: "2026-10-01T09:06:00Z",
      }),
    })
    await settle()
    expect(
      all(el, "[data-weft-run]").map((r) => r.getAttribute("data-weft-run"))
    ).toContain("r_3")
  }

  it('data-scope="tag.arena.batch=<id>" with a tag token lists exactly that batch, live lane included', async () => {
    const studio = fakeStudio(batchRoutes())
    const el = await mountWith({
      "data-endpoint": REMOTE,
      "data-scope": `tag.arena.batch=${BATCH}`,
      "data-token": TAG_TOKEN,
      "data-open": "true",
    })
    await expectTheBatch(el, studio)
  })

  it("a tag token alone seeds the scope from its claims (rung 4b): the same batch", async () => {
    const studio = fakeStudio(batchRoutes())
    const el = await mountWith({
      "data-endpoint": REMOTE,
      "data-token": TAG_TOKEN,
      "data-open": "true",
    })
    expect(text(el, ".weft-head")).not.toContain("how to scope")
    await expectTheBatch(el, studio)
  })
})
