// U30.1, the panel's low findings: the footer names the rung that chose
// the scope (UX-D-23), the Story's model is the turn row's (UX-D-24),
// the narrow header keeps its stats in the title (UX-D-32), an
// explicit public id with no run says it may be wrong (UX-D-37), and a
// replay run without a tracer is said once in the step compare's head
// (UX-D-40). UX-D-28 is ladder.test.ts's, UX-D-29 a11y.test.ts's,
// UX-D-30 layout.test.ts's.
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import { CAUSES } from "../lib/honesty"
import type { DiffDoc, DiffSide } from "../lib/stepdiff"
import { UNTRACED } from "../lib/stepdiff"
import { badgeLabel } from "./badges"
import { stepDiffBlock } from "./compare"
import { PANEL_CSS } from "./styles"
import {
  $,
  all,
  baseRoutes,
  fakeStudio,
  mount,
  page,
  runEvents,
  runRow,
  setup,
  teardown,
  text,
} from "./testkit"

beforeEach(setup)
afterEach(() => {
  teardown()
  history.pushState(null, "", "/")
})

const BASE = {
  "data-endpoint": "http://studio.test/studio/",
  "data-open": "true",
}

/** pub_orders with no run at all. */
function empty() {
  const r = baseRoutes()
  r["sessions?public_id=pub_orders"] = {
    total: 0,
    sessions: [],
    next_before: null,
  }
  r["runs?public_id=pub_orders&limit=50"] = {
    total: 0,
    runs: [],
    next_before: null,
  }
  return r
}

describe("the footer says the rung that chose the scope (UX-D-23)", () => {
  it("explicit: data-public-id; the static sentence is gone", async () => {
    fakeStudio(baseRoutes())
    const el = await mount({ ...BASE, "data-public-id": "pub_orders" })
    const foot = text(el, ".weft-footer")
    expect(foot).toMatch(/^scope: explicit · /)
    expect(foot).toContain("detect: ")
    expect(foot).not.toContain("prompts, args and results")
  })

  it("url: ?weft_scope= on the page", async () => {
    history.pushState(null, "", "/shop?weft_scope=pub_orders")
    fakeStudio(baseRoutes())
    const el = await mount(BASE)
    expect(text(el, ".weft-footer")).toMatch(/^scope: url · /)
  })

  it("none: the dev list", async () => {
    fakeStudio(baseRoutes())
    const el = await mount(BASE)
    expect(text(el, ".weft-footer")).toMatch(/^scope: none · /)
  })
})

describe("the Story names the turn row's model (UX-D-24)", () => {
  it("a run row's gcp.gemini reads the same in the Story, whatever its events say", async () => {
    const r = baseRoutes()
    const row = runRow({ model: { provider: "gcp.gemini", name: "gemini-x" } })
    r["runs?public_id=pub_orders&limit=50"] = {
      total: 1,
      runs: [row],
      next_before: null,
    }
    r["runs/s_01-t1"] = { ...row, children: [] }
    r["runs/s_01-t1/events?after=0&limit=500"] = page(
      runEvents("s_01-t1").map((e) =>
        (e as { type: string }).type === "run_start"
          ? {
              ...(e as object),
              model: { provider: "google", name: "gemini-x" },
            }
          : e
      )
    )
    fakeStudio(r)
    const el = await mount({ ...BASE, "data-public-id": "pub_orders" })
    expect(text(el, ".weft-row2")).toContain("gcp.gemini/gemini-x")
    const said = all(el, ".weft-main .weft-reason").map((n) => n.textContent)
    expect(said).toContain("gcp.gemini/gemini-x")
    expect(said.join(" ")).not.toContain("google/")
  })
})

describe("the narrow header (UX-D-32)", () => {
  it("the stats and the cost hide in a narrow dock; the title's tooltip carries the stats", async () => {
    fakeStudio(baseRoutes())
    const el = await mount({ ...BASE, "data-public-id": "pub_orders" })
    const stats = text(el, "[data-weft-stats]")
    expect(stats).toMatch(/^1 turn · /)
    expect($(el, ".weft-title")!.getAttribute("title")).toBe(
      `acme-support · pub_orders · ${stats}`
    )
    expect(PANEL_CSS).toContain(
      ".weft-narrow [data-weft-stats], .weft-narrow [data-weft-cost-total] { display: none; }"
    )
  })
})

describe("an explicit scope that never had a run (UX-D-37)", () => {
  it("data-public-id: Studio has no runs for it — check the id, its rung named", async () => {
    fakeStudio(empty())
    const el = await mount({ ...BASE, "data-public-id": "pub_orders" })
    expect(text(el, ".weft-splash")).toBe(
      "Studio has no runs for pub_orders yet — check the id (scope: explicit)"
    )
  })

  it("?weft_scope=: the url rung", async () => {
    history.pushState(null, "", "/shop?weft_scope=pub_orders")
    fakeStudio(empty())
    const el = await mount(BASE)
    expect(text(el, ".weft-splash")).toContain("(scope: url)")
  })
})

describe("a replay run without a tracer (UX-D-40)", () => {
  const nospans = {
    hole: "not_recorded",
    reason: CAUSES.not_recorded!.no_spans.reason,
    fix: CAUSES.not_recorded!.no_spans.fix,
  }
  const side = (holes: unknown[]) =>
    ({
      status: "ok",
      system: "s",
      system_hash: "h",
      tool_calls: [],
      tool_results: [],
      text: "t",
      usage: { input_tokens: 1, output_tokens: 1 },
      marks: [],
      holes,
    }) as unknown as DiffSide
  const doc = (bHoles: unknown[][]): DiffDoc =>
    ({
      a: { run_id: "src", steps: bHoles.length, status: "succeeded" },
      b: { run_id: "pg_1", steps: bHoles.length, status: "succeeded" },
      steps: bHoles.map((h, i) => ({
        step: i,
        changed: false,
        a: side([]),
        b: side(h),
        changes: [],
      })),
      summary: { changed_steps: [], first_changed: null },
      holes: [],
    }) as unknown as DiffDoc

  it("every step's no_spans hole: said once in the head, its words in the title, no badge per step", () => {
    const box = stepDiffBlock(doc([[nospans], [nospans]]), "http://s/")
    const head = box.querySelectorAll(':scope > [data-hole="not_recorded"]')
    expect(head).toHaveLength(1)
    expect(badgeLabel(head[0])).toBe(UNTRACED)
    expect(head[0].getAttribute("title")).toContain(nospans.reason)
    expect(head[0].getAttribute("title")).toContain(nospans.fix)
    expect(
      box.querySelectorAll('tbody [data-hole="not_recorded"]')
    ).toHaveLength(0)
  })

  it("one traced step: the badges stay per step, nothing in the head", () => {
    const box = stepDiffBlock(doc([[nospans], []]), "http://s/")
    expect(box.querySelector(':scope > [data-hole="not_recorded"]')).toBeNull()
    expect(
      box.querySelectorAll(
        'tbody [data-weft-diff-side="b"] [data-hole="not_recorded"]'
      )
    ).toHaveLength(1)
  })
})
