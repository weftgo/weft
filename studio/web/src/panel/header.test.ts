// U29.5: the panel's header actions in words (UX-F-22), the turn row's
// accessible name (UX-F-32), the empty states (UX-F-33) and a note
// hole's neutral outline (UX-F-21).
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import {
  $,
  all,
  baseRoutes,
  fakeStudio,
  mount,
  runRow,
  setup,
  teardown,
  text,
} from "./testkit"
import { badge } from "./badges"
import { PANEL_CSS } from "./styles"

beforeEach(setup)
afterEach(teardown)

describe("the header's actions (UX-F-22)", () => {
  it("say their word from 480 px of dock width, the glyph and title below; no raw button", async () => {
    fakeStudio(baseRoutes())
    const el = await mount()
    const head = $(el, ".weft-head")!
    const words = all(el, ".weft-head [data-w]").map((n) => [
      n.getAttribute("data-w"),
      n.textContent,
      n.getAttribute("aria-label"),
    ])
    expect(words).toEqual([
      ["Studio", "⤢", "open in Studio"],
      ["layout", "⇆", expect.stringMatching(/^layout: /)],
      ["theme", "◐", expect.stringMatching(/^theme: /)],
    ])
    // The word is generated content under a container query on the
    // header: the glyph alone in a narrow dock.
    expect(PANEL_CSS).toMatch(/\.weft-head \{[^}]*container-type: inline-size/)
    expect(PANEL_CSS).toContain(
      '@container (min-width: 480px) { [data-w]:before { content: attr(data-w) " "; } }'
    )
    // r and the Raw tab remain; the header's own raw button is gone.
    expect(
      all(el, ".weft-head button").some((b) => b.textContent === "raw")
    ).toBe(false)
    expect($(el, "#weft-tab-raw")).not.toBeNull()
    expect(head.textContent).not.toContain("raw")
  })
})

describe("a turn row's accessible name (UX-F-32)", () => {
  it("is its fields in words, comma-separated, not run together", async () => {
    fakeStudio(baseRoutes())
    const el = await mount()
    const row = $(el, '.weft-turn[data-weft-run="s_01-t1"]')!
    const name = row.getAttribute("aria-label") ?? ""
    expect(name).toMatch(/^turn 1, succeeded, .+, 1 step, 10 in · 4 out, \S.*$/)
    // The visible title and when are the row's own words.
    expect(name).toContain(text(el, ".weft-turn .weft-when"))
  })

  it("leaves the tokens out when a derived run's usage is not known", async () => {
    const routes = baseRoutes()
    const runs = routes["runs?public_id=pub_orders&limit=50"] as {
      runs: Record<string, unknown>[]
    }
    runs.runs[0] = {
      ...runs.runs[0],
      derived: true,
      usage: { input_tokens: 0, output_tokens: 0 },
      cost_reason: "the spans reported no usage",
    }
    fakeStudio(routes)
    const el = await mount()
    const name = $(el, ".weft-turn")!.getAttribute("aria-label") ?? ""
    expect(name).toContain("1 step, ")
    expect(name).not.toContain(" in ")
  })
})

describe("turn or run (U21.3, UX-F-13)", () => {
  it("a public id's session turns are turns, in the header, the filter and the picker", async () => {
    fakeStudio(baseRoutes())
    const el = await mount()
    expect(text(el, "[data-weft-stats]")).toMatch(/^1 turn · /)
    const q = $(el, ".weft-turn-q") as HTMLInputElement
    expect(q.placeholder).toBe("/ filter turns")
  })

  it("a public id's harness runs (no session) are runs, never '1 turns'", async () => {
    const routes = baseRoutes()
    routes["sessions?public_id=pub_orders"] = {
      total: 0,
      sessions: [],
      next_before: null,
    }
    routes["runs?public_id=pub_orders&limit=50"] = {
      total: 1,
      runs: [runRow({ session_id: "", turn: 0 })],
      next_before: null,
    }
    fakeStudio(routes)
    const el = await mount()
    expect(text(el, "[data-weft-stats]")).toMatch(/^1 run · /)
    const q = $(el, ".weft-turn-q") as HTMLInputElement
    expect(q.placeholder).toBe("/ filter runs")
    expect(q.getAttribute("aria-label")).toBe("filter runs")
    expect($(el, ".weft-rows")!.getAttribute("aria-label")).toBe(
      "runs, newest first"
    )
  })

  it("a public id mixing harness and session runs says runs (U21.3-fix: every run has a session, or run)", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_orders&limit=50"] = {
      total: 2,
      runs: [runRow({ id: "h_01", session_id: "", turn: 0 }), runRow({})],
      next_before: null,
    }
    fakeStudio(routes)
    const el = await mount()
    expect(text(el, "[data-weft-stats]")).toMatch(/^2 runs · /)
    expect(($(el, ".weft-turn-q") as HTMLInputElement).placeholder).toBe(
      "/ filter runs"
    )
  })

  it("the turn view's words follow the noun: a session's turn views, a harness run's run views", async () => {
    fakeStudio(baseRoutes())
    let el = await mount()
    expect($(el, ".weft-tabs")?.getAttribute("aria-label")).toBe("turn views")
    teardown()
    setup()
    const routes = baseRoutes()
    routes["runs?public_id=pub_orders&limit=50"] = {
      total: 1,
      runs: [runRow({ session_id: "", turn: 0 })],
      next_before: null,
    }
    fakeStudio(routes)
    el = await mount()
    expect($(el, ".weft-tabs")?.getAttribute("aria-label")).toBe("run views")
  })
})

describe("the empty states (UX-F-33)", () => {
  const empty = () => {
    const routes = baseRoutes()
    routes["sessions?public_id=pub_orders"] = {
      total: 0,
      sessions: [],
      next_before: null,
    }
    routes["runs?public_id=pub_orders&limit=50"] = {
      total: 0,
      runs: [],
      next_before: null,
    }
    return routes
  }

  it("no turn reads no usage (—, not 0→0) and draws no 'select a turn'", async () => {
    fakeStudio(empty())
    const el = await mount()
    expect(text(el, "[data-weft-stats]")).toBe("0 runs · —")
    // An explicit public id with no run (UX-D-37): it may be a wrong
    // one — said, with the rung that named it.
    expect(text(el, ".weft-splash")).toBe(
      "Studio has no runs for pub_orders yet — check the id (scope: explicit)"
    )
    expect(text(el, ".weft-main")).not.toContain("select a turn")
  })

  it("a session scope whose turns have not loaded says turn, not run (U23.2-fix)", async () => {
    const routes = baseRoutes()
    routes["runs?public_id=pub_orders&limit=50"] = {
      total: 0,
      runs: [],
      next_before: null,
    }
    fakeStudio(routes)
    const el = await mount()
    expect(text(el, "[data-weft-stats]")).toMatch(/^0 turns · /)
  })

  it("the dev list says what fills it", async () => {
    const routes = empty()
    routes["runs?limit=10"] = { total: 0, runs: [], next_before: null }
    fakeStudio(routes)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-open": "true",
    })
    // How to connect, to this Studio (UX-F-11): its URL, and the
    // connect card through the hand-off.
    expect(text(el, ".weft-turns")).toContain(
      "no runs yet — run your app with WEFT_STUDIO_URL=http://studio.test/studio go run . · how to connect ⤢"
    )
    expect(
      ($(el, ".weft-splash a") as HTMLAnchorElement).getAttribute("href")
    ).toBe("http://studio.test/studio/settings#settings-connect")
    expect(text(el, ".weft-main")).not.toContain("select a turn")
  })
})

describe("a note hole's outline (UX-F-21)", () => {
  it("is the neutral tag outline (line border, dim text), as Studio's border-border", () => {
    const b = badge("stripped") // a "note" tone
    expect(b.className).toBe("weft-badge weft-tag")
    expect(PANEL_CSS).toContain(
      ".weft-tag { border: 1px solid var(--weft-line); color: var(--weft-dim); }"
    )
    // A loss keeps its warning outline.
    expect(badge("gap").className).toContain("weft-warn-badge")
  })
})

describe("the header's figures and a running row (P020.2)", () => {
  it("the stats and the cost sit in one group after the title, none in the title", async () => {
    fakeStudio(baseRoutes())
    const el = await mount()
    const group = $(el, ".weft-head > .weft-hs")!
    expect(group).not.toBeNull()
    // Each figure is a child of the group (shown whole or wrapped onto
    // its hidden line), the buttons after it, never inside.
    expect(group.querySelector("[data-weft-stats]")).not.toBeNull()
    expect(group.querySelector("button, a.weft-btn")).toBeNull()
    expect($(el, ".weft-head > .weft-grow")).toBeNull()
  })

  it("a running row's duration reads … — not the — of a run with no end", async () => {
    const routes = baseRoutes()
    const runs = routes["runs?public_id=pub_orders&limit=50"] as {
      runs: Record<string, unknown>[]
    }
    runs.runs[0] = { ...runs.runs[0], status: "running", finished: null }
    routes["runs/s_01-t1"] = {
      ...runRow({ status: "running", finished: null }),
      children: [],
    }
    fakeStudio(routes)
    const el = await mount()
    const row2 = $(el, ".weft-turn .weft-row2")!
    expect(row2.lastElementChild?.textContent).toBe("…")
    expect(row2.textContent).not.toContain("— —")
  })
})
