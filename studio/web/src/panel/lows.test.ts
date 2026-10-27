// U30.1, the panel's low findings: the footer names the rung that chose
// the scope (UX-D-23), the Story's model is the turn row's (UX-D-24),
// the narrow header keeps its stats in the title (UX-D-32), an
// explicit public id with no run says it may be wrong (UX-D-37), and a
// replay run without a tracer is said once in the step compare's head
// (UX-D-40). UX-D-28 is ladder.test.ts's, UX-D-29 a11y.test.ts's,
// UX-D-30 layout.test.ts's.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { CAUSES, refusalWords } from "../lib/honesty"
import { HANDOFF, href, runsLink } from "../lib/links"
import { RUNS_IN } from "../lib/runs-in"
import { FakeStudio } from "../test/fake-studio"
import {
  CALL,
  NOPUB,
  PLAIN,
  runtimeRow,
  serveAppTurn,
} from "../test/appturn-fixture"
import type { DiffDoc, DiffSide } from "../lib/stepdiff"
import { UNTRACED } from "../lib/stepdiff"
import { badgeLabel } from "./badges"
import { stepDiffBlock } from "./compare"
import { PANEL_CSS } from "./styles"
import {
  $,
  all,
  baseRoutes,
  click,
  create,
  fakeStudio,
  META,
  mount,
  page,
  runEvents,
  runRow,
  settle,
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

  it("marker: a data-weft-scope on the page (U30.1-fix)", async () => {
    const box = document.createElement("section")
    box.setAttribute("data-weft-scope", "pub_orders")
    document.body.appendChild(box)
    try {
      fakeStudio(baseRoutes())
      const el = await mount({
        "data-endpoint": "https://studio.example/studio/",
        "data-open": "true",
      })
      expect(text(el, ".weft-footer")).toMatch(/^scope: marker · /)
    } finally {
      box.remove()
    }
  })

  it("switcher: the reader's pick (U30.1-fix)", async () => {
    history.pushState(null, "", "/shop?weft_scope=pub_other")
    fakeStudio(baseRoutes())
    const el = await mount({ ...BASE, "data-public-id": "pub_orders" })
    expect(text(el, ".weft-footer")).toMatch(/^scope: explicit · /)
    const sel = $(el, "select.weft-switch") as HTMLSelectElement
    const other = [...sel.options].find((o) => o.text.includes("pub_other"))!
    sel.value = other.value
    sel.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    expect(text(el, ".weft-footer")).toMatch(/^scope: switcher · /)
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
  it("a narrow dock hides the stats and the cost from sight, not from a screen reader; the title's tooltip carries both", async () => {
    fakeStudio(baseRoutes(), {
      ...META,
      capabilities: [...META.capabilities, "pricing"],
    })
    const el = await mount({ ...BASE, "data-public-id": "pub_orders" })
    const stats = text(el, "[data-weft-stats]")
    expect(stats).toMatch(/^1 turn · /)
    const cost = text(el, "[data-weft-cost-total]")
    expect(cost).not.toBe("")
    expect($(el, ".weft-title")!.getAttribute("title")).toBe(
      `acme-support · pub_orders · ${stats} · ${cost}`
    )
    // Visually hidden (the .weft-sr rule), never display: none.
    expect(PANEL_CSS).toContain(
      ".weft-sr, .weft-narrow [data-weft-stats], .weft-narrow [data-weft-cost-total] { position: absolute;"
    )
    expect(PANEL_CSS).not.toMatch(/weft-cost-total\] \{ display: none/)
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

  it("not before the first list read lands, nor while Studio is down (U30.1-fix)", async () => {
    const r = empty()
    let land = (_: unknown) => {}
    r["runs?public_id=pub_orders&limit=50"] = () =>
      new Promise((res) => (land = res))
    fakeStudio(r)
    const el = await mount({ ...BASE, "data-public-id": "pub_orders" })
    expect(text(el, ".weft-splash")).not.toContain("check the id")
    land({ total: 0, runs: [], next_before: null })
    await settle()
    expect(text(el, ".weft-splash")).toContain("check the id")
    teardown()
    setup()
    const down = empty()
    down["runs?public_id=pub_orders&limit=50"] = () => {
      throw new TypeError("Failed to fetch")
    }
    fakeStudio(down)
    const gone = await mount({ ...BASE, "data-public-id": "pub_orders" })
    expect(text(gone, ".weft-splash")).not.toContain("check the id")
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

describe("the runtimes ⤢ follows the refusal's kind, not its words (U30.1-fix)", () => {
  it("refusalWords: rt on the runtime refusals only", () => {
    const row = { id: "r", service: "app", public_id: "", session_id: "s" }
    expect(refusalWords("no-public-id", row, [], false).rt).toBeFalsy()
    // Its words name runtime.Sessions: the old word test drew the ⤢.
    expect(refusalWords("no-public-id", row, [], false).fix).toMatch(/runtime/)
    expect(refusalWords("plain", row, [], false).rt).toBeFalsy()
    expect(refusalWords("read-only", row, [], true).rt).toBeFalsy()
    expect(refusalWords("read-only", row, [], false).rt).toBe(true)
    expect(
      refusalWords("read-only", row, [runtimeRow({ sessions: false })], false)
        .rt
    ).toBe(true)
  })

  for (const [name, run] of [
    ["a session turn without a public id", NOPUB],
    ["a plain Generate's park", PLAIN],
  ] as const)
    it(`${name}: no runtimes ⤢`, async () => {
      serveAppTurn(new FakeStudio(), {}).install()
      const el = await mount(BASE)
      await vi.waitFor(
        () =>
          expect(
            all(el, ".weft-turn").some((b) =>
              (b.getAttribute("data-weft-run") ?? "").includes(run)
            )
          ).toBe(true),
        { timeout: 5_000 }
      )
      click(
        all(el, ".weft-turn").find((b) =>
          (b.getAttribute("data-weft-run") ?? "").includes(run)
        )
      )
      await vi.waitFor(
        () => expect($(el, `.weft-call[data-key="${CALL.id}"]`)).toBeTruthy(),
        { timeout: 5_000 }
      )
      await settle()
      expect($(el, "[data-weft-app-owned]")).toBeTruthy()
      expect(all(el, "a").some((a) => a.textContent === RUNS_IN.manage)).toBe(
        false
      )
    })
})

describe("the filters ⤢ carries the page's scope (U30.1-fix)", () => {
  const REMOTE = "https://studio.example/studio/"
  const filter = async (el: Awaited<ReturnType<typeof mount>>) => {
    const q = $(el, ".weft-turn-q") as HTMLInputElement
    q.value = "order"
    q.dispatchEvent(new Event("input", { bubbles: true }))
    const err = $(el, ".weft-tq-err") as HTMLInputElement
    err.checked = true
    err.dispatchEvent(new Event("change", { bubbles: true }))
    await settle()
    return all(el, "a")
      .find((a) => a.textContent === HANDOFF.filters)!
      .getAttribute("href")
  }
  const listOf = (key: string) => {
    const r = baseRoutes()
    r[key] = {
      total: 1,
      runs: [runRow({ id: "r_1", status: "failed", err: "order lost" })],
      next_before: null,
    }
    return r
  }
  const mountScope = async (scope: string) => {
    const el = create({
      "data-endpoint": REMOTE,
      "data-scope": scope,
      "data-open": "true",
    })
    document.body.appendChild(el)
    await settle()
    return el
  }

  it("a tag scope: its tags; has error: failed", async () => {
    fakeStudio(listOf("runs?tag.arena.batch=b1&limit=50"))
    const el = await mountScope("tag.arena.batch=b1")
    expect(await filter(el)).toBe(
      href(
        REMOTE,
        runsLink({ tag: ["arena.batch=b1"], q: "order", status: "failed" })
      )
    )
  })

  it("a flow scope: its weft.flow tag", async () => {
    fakeStudio(listOf("runs?flow=f_checkout&limit=50"))
    const el = await mountScope("flow=f_checkout")
    expect(await filter(el)).toBe(
      href(
        REMOTE,
        runsLink({
          tag: ["weft.flow=f_checkout"],
          q: "order",
          status: "failed",
        })
      )
    )
  })

  it("an agent scope: its agent", async () => {
    fakeStudio(listOf("runs?agent=director&limit=50"))
    const el = await mountScope("agent=director")
    expect(await filter(el)).toBe(
      href(
        REMOTE,
        runsLink({ agent: "director", q: "order", status: "failed" })
      )
    )
  })
})
