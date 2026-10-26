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
    expect(text(el, ".weft-turns")).toContain("no turns yet — run your app")
    expect(text(el, ".weft-main")).not.toContain("select a turn")
  })

  it("the dev list says what fills it", async () => {
    const routes = empty()
    routes["runs?limit=10"] = { total: 0, runs: [], next_before: null }
    fakeStudio(routes)
    const el = await mount({
      "data-endpoint": "http://studio.test/studio/",
      "data-open": "true",
    })
    expect(text(el, ".weft-turns")).toContain(
      "no runs yet — connect your app (otel.Install)"
    )
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
