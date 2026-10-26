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
