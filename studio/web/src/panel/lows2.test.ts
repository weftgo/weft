// U33.2, the panel's half of the Studio / panel-side lows (batch 2):
// a RequireApproval tool says "needs approval" beside its replay class
// (UX-H-22), a replay's lineage chip names its edits (UX-S5b-04), a
// bare script tag says why its dock left (UX-S5b-10), a session-only
// scope names its session (UX-H-06) and the pill reads the turn's
// priced figure (UX-H-31).
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { NEEDS_APPROVAL } from "../lib/drawer-spec"
import { noEndpoint, NO_SCOPE_LABEL } from "./element"
import {
  $,
  all,
  baseRoutes,
  button,
  click,
  create,
  fakeStudio,
  json,
  META,
  mount,
  runRow,
  RUNTIMES,
  settle,
  setup,
  teardown,
  text,
} from "./testkit"

beforeEach(setup)
afterEach(teardown)

describe("a RequireApproval tool in the drawer (UX-H-22)", () => {
  it("says needs approval beside its ⚠, the catalog row's approval read", async () => {
    const routes = baseRoutes()
    const rt = RUNTIMES.runtimes[0]
    routes.runtimes = {
      runtimes: [
        {
          ...rt,
          agents: rt.agents.map((a) => ({
            ...a,
            tools: [
              { name: "lookup_order", side_effects: "safe", allow: true },
              {
                name: "refund",
                side_effects: "never",
                allow: false,
                approval: true,
              },
            ],
          })),
        },
      ],
    }
    fakeStudio(routes, META)
    const el = await mount()
    click(button(el, "✎ Replay…"))
    await settle()
    const tools = all(el, ".weft-drawer .weft-tool")
    const refund = tools.find((t) => t.textContent.includes("refund"))!
    const lookup = tools.find((t) => t.textContent.includes("lookup_order"))!
    expect(refund.textContent).toContain("⚠")
    expect(refund.textContent).toContain(NEEDS_APPROVAL)
    expect(lookup.textContent).not.toContain(NEEDS_APPROVAL)
  })
})

describe("a replay's lineage chip names its edits (UX-S5b-04)", () => {
  it("the chip's title appends the edit summary from weft.edits", async () => {
    const routes = baseRoutes()
    const replay = runRow({
      id: "pg_1",
      playground: true,
      session_id: "s_01",
      turn: 2,
      forked_from: "s_01-t1#1",
      experiment_id: "exp_1",
      meta: { "weft.edits": "0:user,0:c1:args,1:insert" },
    })
    routes["runs?public_id=pub_orders&limit=50"] = {
      total: 2,
      runs: [replay, runRow({})],
      next_before: null,
    }
    routes["runs/pg_1"] = { ...replay, children: [] }
    fakeStudio(routes)
    const el = await mount()
    const chip = all(el, '[data-weft-chip="experiment"]')[0]
    expect(chip.getAttribute("title")).toContain("3 edits (user, args, insert)")
  })
})

describe("a bare script tag's dock (UX-S5b-10)", () => {
  const down = () => vi.fn(async () => json({}, 503))
  it("says once, in the console, why it left: no endpoint and no data-weft", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {})
    vi.stubGlobal("fetch", down())
    const el = create({})
    el.autoMounted = true
    document.body.appendChild(el)
    await settle()
    expect(el.isConnected).toBe(false)
    expect(warn.mock.calls).toHaveLength(1)
    const ep = String(warn.mock.calls[0][0]).replace(/^.* at (\S+);.*$/, "$1")
    expect(warn.mock.calls[0]).toEqual([noEndpoint(ep)])
    expect(ep).toMatch(/^http:\/\/(localhost|127\.0\.0\.1)/)
    warn.mockRestore()
  })

  it("off loopback a bare tag's dock leaves silently (U22: a production page)", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {})
    vi.stubGlobal("fetch", down())
    const base = document.createElement("base")
    base.href = "https://shop.example/app/"
    document.head.appendChild(base)
    const el = create({})
    el.autoMounted = true
    document.body.appendChild(el)
    await settle()
    base.remove()
    expect(el.isConnected).toBe(false)
    expect(warn).not.toHaveBeenCalled()
    warn.mockRestore()
  })

  it("stays silent where the tag asked: data-weft, or an endpoint named", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {})
    vi.stubGlobal("fetch", down())
    const tag = document.createElement("script")
    tag.setAttribute("data-weft", "")
    document.head.appendChild(tag)
    const a = create({})
    a.autoMounted = true
    document.body.appendChild(a)
    await settle()
    expect(a.isConnected).toBe(false)
    tag.remove()
    const b = create({ "data-endpoint": "https://studio.example/" })
    b.autoMounted = true
    document.body.appendChild(b)
    await settle()
    expect(b.isConnected).toBe(false)
    expect(warn).not.toHaveBeenCalled()
    warn.mockRestore()
  })
})

describe("the header and the pill (UX-H-06, UX-H-31)", () => {
  it("a session-only scope names the session, never the fallback's words", async () => {
    const routes = baseRoutes()
    const turn = runRow({ public_id: "" })
    for (const k of [
      "runs?session_id=s_01&limit=50",
      "runs?session=s_01&limit=50",
    ])
      routes[k] = { total: 1, runs: [turn], next_before: null }
    fakeStudio(routes)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-scope": ";session=s_01",
      "data-open": "true",
    })
    expect(text(el, ".weft-title")).toBe("session s_01")
    expect(text(el, ".weft-title")).not.toBe(NO_SCOPE_LABEL)
    expect($(el, ".weft-title")!.getAttribute("title")).toContain(
      "(no public id)"
    )
    expect(button(el, "how to scope")).toBeUndefined()
    // The session is the title: no second session chip.
    expect(text(el, ".weft-head")).not.toContain("session s_01session s_01")
  })

  it("the pill reads the turn's priced figure, its tokens where none", async () => {
    const routes = baseRoutes()
    const priced = runRow({
      cost_usd: 0.0029,
      cost_known: true,
      delegated_cost_usd: 0,
      delegated_cost_known: true,
    })
    routes["runs?public_id=pub_orders&limit=50"] = {
      total: 1,
      runs: [priced],
      next_before: null,
    }
    routes["runs/s_01-t1"] = { ...priced, children: [] }
    fakeStudio(routes)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-public-id": "pub_orders",
      "data-mode": "pill",
    })
    expect(text(el, ".weft-fab-cost")).toBe(" · $0.0029")
    expect($(el, ".weft-fab-cost")!.getAttribute("title")).toContain("cost")
  })
})
