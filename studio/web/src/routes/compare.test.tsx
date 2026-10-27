// The step-aligned compare in Studio (plan E3, E3.2): the compare page
// (N-way: N−1 calls of GET /api/diff against one base), the run
// header's "compare with…" links, gated on capability "diff" — every
// response served from the E3.1 goldens (studio/testdata/api/diff*).
import { cleanup, configure, fireEvent, waitFor } from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunsPage } from "@/lib/api"
import type { DiffDoc } from "@/lib/stepdiff"
import { CAUSES } from "@/lib/honesty"
import { PICK_FOUND_NONE, WHY_PUBLIC } from "@/lib/compare-picker"
import { CHANGED_ONLY } from "@/lib/compare-view"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import {
  FakeStudio,
  apiError,
  golden,
  pagedEvents,
  transcriptOf,
} from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const SAME = {
  system: "same",
  tool_calls: "same",
  tool_results: "same",
  text: "same",
  usage: "same",
}

let studio: FakeStudio

/** serve answers /api/diff from the given responses, keyed "a|b". */
function serve(
  capabilities: string[],
  diffs: Partial<Record<string, DiffDoc>> = {}
) {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  studio = new FakeStudio()
    .on("GET meta", {
      ...golden<Record<string, unknown>>("meta"),
      capabilities,
    })
    .on("GET diff", (req) => {
      const doc = diffs[`${req.query.get("a")}|${req.query.get("b")}`]
      return doc ?? apiError(404, "not_found", "no such run")
    })
    .install()
  return studio
}

beforeEach(() => setStudioToken(""))
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

/** cells reads each row's column states for one compared run. */
function cells(run: string): Record<string, Record<string, string>> {
  const out: Record<string, Record<string, string>> = {}
  for (const tr of document.querySelectorAll("[data-step-diff] tbody tr")) {
    const row: Record<string, string> = {}
    for (const td of tr.querySelectorAll(
      `[data-diff-run="${run}"][data-diff-state]`
    ))
      row[td.getAttribute("data-diff-cell")!] =
        td.getAttribute("data-diff-state")!
    out[tr.getAttribute("data-diff-step")!] = row
  }
  return out
}

const markers = () =>
  [...document.querySelectorAll("[data-diff-marker]")].map((m) => ({
    step: m.getAttribute("data-diff-marker"),
    run: m.getAttribute("data-diff-run"),
    text: m.querySelector("a")!.textContent,
  }))

/** axe's violations under el, one line each (P8-pre-web #3). */
async function axeLines(el: Element): Promise<string[]> {
  const res = await axe.run(el)
  return res.violations.map(
    (v) => `${v.id}: ${v.nodes.map((n) => n.html.slice(0, 120)).join(" | ")}`
  )
}

/** The compare page starts on the changed steps (U30.2-fix): the
 * full grid is a click away. */
function showAllSteps() {
  const t = document.querySelector<HTMLButtonElement>(
    "[data-diff-changed-only]"
  )
  if (t?.getAttribute("aria-pressed") === "true") fireEvent.click(t)
}

describe("the compare page", () => {
  it("diff.golden.json: one 'changed at step 3' marker (tool results) and every other row the same", async () => {
    const doc = golden<DiffDoc>("diff")
    serve(["diff"], { "r_da|r_db": doc })
    renderApp('/compare?a=r_da&b=["r_db"]')
    await waitFor(() =>
      expect(document.querySelector("[data-step-diff]")).toBeTruthy()
    )
    showAllSteps()
    expect(markers()).toEqual([
      { step: "3", run: "r_db", text: "changed at step 3 · tool results" },
    ])
    expect(cells("r_db")).toEqual({
      "0": SAME,
      "1": SAME,
      "2": SAME,
      "3": { ...SAME, tool_results: "changed" },
      "4": SAME,
    })
    const changed = document.querySelector(
      '[data-diff-step="3"] [data-diff-run="r_db"][data-diff-cell="tool_results"]'
    )!
    expect(changed.textContent).toBe("order 3 lost")
    expect(
      document.querySelector(
        '[data-diff-step="3"] [data-diff-run="r_da"][data-diff-cell="tool_results"]'
      )!.textContent
    ).toBe("order 3 shipped")
    expect(
      document.querySelector('[data-diff-marker="3"] a')!.getAttribute("href")
    ).toBe("/runs/r_db?step=3")
    expect(studio.calls("GET diff").map((r) => r.query.toString())).toEqual([
      "a=r_da&b=r_db",
    ])
    // P8-pre-web: axe finds nothing on the compare page.
    expect(await axeLines(document.querySelector("main")!)).toEqual([])
  })

  it("changed only narrows to the changed steps and columns; the usage delta sums each side (U30.2, UX-A-16)", async () => {
    const base = golden<DiffDoc>("diff")
    const doc: DiffDoc = {
      ...base,
      steps: base.steps.map((r) =>
        r.step === 4
          ? {
              ...r,
              changed: true,
              changes: ["usage"],
              b: { ...r.b!, usage: { input_tokens: 10, output_tokens: 9 } },
            }
          : r
      ),
      summary: { ...base.summary, changed_steps: [3, 4] },
    }
    serve(["diff"], { "r_da|r_db": doc })
    renderApp("/compare?a=r_da&b=r_db")
    await waitFor(() =>
      expect(document.querySelector("[data-step-diff]")).toBeTruthy()
    )
    expect(document.querySelector("[data-diff-count]")!.textContent).toBe(
      "2 of 5 steps differ"
    )
    const usage = () =>
      [...document.querySelectorAll("[data-usage-run]")].map(
        (li) => li.textContent
      )
    expect(usage()).toEqual([
      "usage r_da · base: 50 in → 25 out",
      "usage r_db: 50 in → 29 out (±0 in · +4 out)",
    ])
    const steps = () =>
      [...document.querySelectorAll("[data-step-diff] tbody tr")].map((tr) =>
        tr.getAttribute("data-diff-step")
      )
    const cols = () =>
      [
        ...document.querySelectorAll(
          '[data-step-diff] tbody tr:first-child [data-diff-run="r_db"][data-diff-cell]'
        ),
      ].map((td) => td.getAttribute("data-diff-cell"))
    // On by default: a step differs (U30.2-fix, the card's proposal).
    const toggle = document.querySelector<HTMLButtonElement>(
      "[data-diff-changed-only]"
    )!
    expect(toggle.getAttribute("aria-pressed")).toBe("true")
    expect(steps()).toEqual(["3", "4"])
    expect(cols()).toEqual(["tool_results", "usage"])
    expect(await axeLines(document.querySelector("main")!)).toEqual([])
    fireEvent.click(toggle)
    expect(toggle.getAttribute("aria-pressed")).toBe("false")
    expect(steps()).toEqual(["0", "1", "2", "3", "4"])
    expect(cols()).toEqual([
      "system",
      "tool_calls",
      "tool_results",
      "text",
      "usage",
    ])
  })

  it("a linked step the changed-only view hides says so, and show all brings it back (U30.2-fix)", async () => {
    serve(["diff"], { "r_da|r_db": golden<DiffDoc>("diff") })
    renderApp("/compare?a=r_da&b=r_db&step=1")
    await waitFor(() =>
      expect(document.querySelector("[data-step-diff]")).toBeTruthy()
    )
    const note = document.querySelector("[data-diff-focus-hidden]")!
    expect(note.textContent).toContain(CHANGED_ONLY.focusHidden(1))
    expect(document.querySelector('[data-diff-step="1"]')).toBeNull()
    fireEvent.click(
      document.querySelector<HTMLElement>("[data-diff-show-all]")!
    )
    expect(document.querySelector('[data-diff-step="1"]')).toBeTruthy()
    expect(document.querySelector("[data-diff-focus-hidden]")).toBeNull()
    // The changed step a link names stays: no note.
    cleanup()
    renderApp("/compare?a=r_da&b=r_db&step=3")
    await waitFor(() =>
      expect(document.querySelector('[data-diff-step="3"]')).toBeTruthy()
    )
    expect(document.querySelector("[data-diff-focus-hidden]")).toBeNull()
  })

  it("no step changed: the toggle is off and says so", async () => {
    const base = golden<DiffDoc>("diff")
    const same: DiffDoc = {
      ...base,
      steps: base.steps.map((r) => ({ ...r, changed: false, changes: [] })),
      summary: { ...base.summary, changed_steps: [], first_changed: null },
    }
    serve(["diff"], { "r_da|r_db": same })
    renderApp("/compare?a=r_da&b=r_db")
    await waitFor(() =>
      expect(document.querySelector("[data-step-diff]")).toBeTruthy()
    )
    showAllSteps()
    expect(
      document.querySelector<HTMLButtonElement>("[data-diff-changed-only]")!
        .disabled
    ).toBe(true)
    expect(document.querySelector("[data-diff-count]")!.textContent).toBe(
      "0 of 5 steps differ"
    )
  })

  it("N-way: one call per compared run against the same base, side by side", async () => {
    const base = golden<DiffDoc>("diff")
    // r_dc lost step 4 (a missing row), and kept step 3's result.
    const short: DiffDoc = {
      ...base,
      b: { ...base.b, run_id: "r_dc", steps: 4 },
      steps: base.steps.map((s) =>
        s.step === 4
          ? { ...s, changed: true, b: null, changes: ["missing"], unknown: [] }
          : s.step === 3
            ? { ...s, changed: false, b: s.a, changes: [] }
            : s
      ),
      summary: { changed_steps: [4], first_changed: 4 },
    }
    serve(["diff"], { "r_da|r_db": base, "r_da|r_dc": short })
    renderApp('/compare?a=r_da&b=["r_db","r_dc"]')
    await waitFor(() =>
      expect(document.querySelector("[data-step-diff]")).toBeTruthy()
    )
    showAllSteps()
    expect(studio.calls("GET diff").map((r) => r.query.get("a"))).toEqual([
      "r_da",
      "r_da",
    ])
    expect(markers()).toEqual([
      { step: "3", run: "r_db", text: "changed at step 3 · tool results" },
      {
        step: "4",
        run: "r_dc",
        text: "changed at step 4 · only in the base run",
      },
    ])
    expect(cells("r_dc")["4"]).toEqual({
      system: "missing",
      tool_calls: "missing",
      tool_results: "missing",
      text: "missing",
      usage: "missing",
    })
    expect(cells("r_dc")["3"]).toEqual(SAME)
  })

  it("a column a side did not record is not comparable — never same — and its holes are badges", async () => {
    serve(["diff"], { "old1|old2": golden<DiffDoc>("diff-not-recorded") })
    renderApp('/compare?a=old1&b=["old2"]')
    await waitFor(() =>
      expect(document.querySelector("[data-step-diff]")).toBeTruthy()
    )
    showAllSteps()
    expect(markers()).toEqual([])
    for (const row of Object.values(cells("old2")))
      for (const s of Object.values(row)) expect(s).toBe("unknown")
    expect(
      document.querySelector(
        '[data-diff-step="0"] [data-diff-run="old2"][data-diff-cell="text"]'
      )!.textContent
    ).toBe("not comparable")
    expect(document.querySelector("[data-diff-none]")!.textContent).toBe(
      "old2: no step changed among the columns both runs recorded"
    )
    expect(
      document.querySelectorAll(
        '[data-diff-step="0"] [data-hole="not_recorded"]'
      ).length
    ).toBe(2)
  })

  it("a read token's diff: system hidden, badged on each side; a truncated response badged response_cap", async () => {
    const doc = golden<DiffDoc>("diff-hidden")
    doc.holes = [
      // The words the server sends for it (lib/honesty.ts's CAUSES,
      // pinned by holes.golden.json), never a fixture's own.
      {
        hole: "truncated",
        cause: "response_cap",
        ...CAUSES.truncated!.response_cap,
      },
    ]
    serve(["diff"], { "r_ta|r_tb": doc })
    renderApp('/compare?a=r_ta&b=["r_tb"]')
    await waitFor(() =>
      expect(document.querySelector("[data-step-diff]")).toBeTruthy()
    )
    showAllSteps()
    expect(
      document.querySelectorAll(
        '[data-step-diff] tbody [data-diff-marks] [data-hole="hidden"]'
      ).length
    ).toBe(10)
    // The system cells: the hidden badge, never the hash as words; the
    // compared cell keeps the server's state (the hashes match).
    const sys = document.querySelector<HTMLElement>(
      '[data-diff-step="0"] [data-diff-run="r_tb"][data-diff-cell="system"]'
    )!
    expect(sys.getAttribute("data-diff-state")).toBe("same")
    expect(sys.querySelector('[data-hole="hidden"]')).toBeTruthy()
    expect(
      document.querySelector(
        '[data-diff-step="0"] [data-diff-run="r_ta"][data-diff-cell="system"] [data-hole="hidden"]'
      )
    ).toBeTruthy()
    expect(
      document.querySelector('[data-diff-holes] [data-hole="truncated"]')
    ).toBeTruthy()
    expect(markers().map((m) => m.step)).toEqual(["3"])
  })

  it("a link's step: the row is targeted, highlighted and scrolled to", async () => {
    serve(["diff"], { "r_da|r_db": golden<DiffDoc>("diff") })
    const scroll = vi.fn()
    Element.prototype.scrollIntoView = scroll
    renderApp('/compare?a=r_da&b=["r_db"]&step=3')
    const row = await waitFor(() => {
      const el = document.querySelector("[data-diff-target]")
      expect(el).toBeTruthy()
      return el!
    })
    expect(row.getAttribute("data-diff-step")).toBe("3")
    expect(document.querySelectorAll("[data-diff-target]")).toHaveLength(1)
    expect(scroll.mock.contexts).toContain(row)
  })

  it("a failed background re-read keeps the drawn table; an error replaces nothing it had", async () => {
    // A side still running is re-read every 2 s (diffQuery).
    const doc = golden<DiffDoc>("diff")
    doc.b = { ...doc.b, status: "running" }
    let calls = 0
    serve(["diff"])
    studio.on("GET diff", () =>
      ++calls === 1 ? doc : apiError(500, "internal", "the database is gone")
    )
    renderApp('/compare?a=r_da&b=["r_db"]')
    await waitFor(() =>
      expect(document.querySelector("[data-step-diff]")).toBeTruthy()
    )
    showAllSteps()
    await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2), {
      timeout: 6_000,
    })
    await new Promise((r) => setTimeout(r, 100))
    expect(document.querySelector("[data-step-diff]")).toBeTruthy()
    expect(document.querySelector("[data-step-diff-error]")).toBeNull()
  })

  it("an error before any response arrived is one line", async () => {
    serve(["diff"])
    studio.on("GET diff", () =>
      apiError(500, "internal", "the database is gone")
    )
    renderApp('/compare?a=r_da&b=["r_db"]')
    const line = await waitFor(() => {
      const el = document.querySelector("[data-step-diff-error]")
      expect(el).toBeTruthy()
      return el!
    })
    expect(line.textContent).toContain("the database is gone")
  })

  it("one failed side is badged; the sides that answered are still drawn", async () => {
    serve(["diff"], { "r_da|r_db": golden<DiffDoc>("diff") })
    renderApp('/compare?a=r_da&b=["r_db","r_typo"]')
    await waitFor(() =>
      expect(document.querySelector("[data-step-diff]")).toBeTruthy()
    )
    showAllSteps()
    const bad = document.querySelector("[data-step-diff-side-error]")!
    expect(bad.getAttribute("data-step-diff-side-error")).toBe("r_typo")
    expect(bad.textContent).toContain("not compared")
    expect(bad.textContent).toContain("no such run")
    expect(
      document.querySelectorAll("[data-step-diff-side-error]")
    ).toHaveLength(1)
    expect(Object.keys(cells("r_db")).length).toBeGreaterThan(0)
    expect(document.querySelector('[data-diff-run="r_typo"]')).toBeNull()
  })

  it("a repeated b, or b equal to a, is one compared run (one call, one column)", async () => {
    serve(["diff"], { "r_da|r_db": golden<DiffDoc>("diff") })
    const { router } = renderApp('/compare?a=r_da&b=["r_db","r_db","r_da"]')
    await waitFor(() =>
      expect(document.querySelector("[data-step-diff]")).toBeTruthy()
    )
    showAllSteps()
    expect(router.state.location.search).toMatchObject({
      a: "r_da",
      b: "r_db",
    })
    expect(studio.calls("GET diff")).toHaveLength(1)
    expect(
      document.querySelectorAll('thead th[scope="colgroup"]')
    ).toHaveLength(2)
  })

  it("the address is the normalised one (UX-S5b-07): a repeated b and a b equal to a leave the URL, replaced", async () => {
    serve(["diff"], { "r_da|r_db": golden<DiffDoc>("diff") })
    const { router } = renderApp("/compare?a=r_da&b=r_db,r_db,r_da")
    await waitFor(() =>
      expect(router.state.location.searchStr).toBe("?a=r_da&b=r_db")
    )
  })

  it("UX-S5b-07: a b that is only a leaves the URL with it", async () => {
    serve(["diff"], {})
    const { router } = renderApp("/compare?a=r_da&b=r_da,r_da")
    await waitFor(() =>
      expect(router.state.location.searchStr).toBe("?a=r_da")
    )
  })

  it("H9: opens with a run picker — a base, then the base agent's runs — and ?b= takes commas", async () => {
    const rows = golden<RunsPage>("runs").runs
    const rOK = rows.find((r) => r.id === "r_ok")!
    const mine = { ...rOK, id: "r_mine", agent: "orders" }
    const other = { ...rOK, id: "r_other", agent: "billing" }
    serve(["diff"], { "r_da|r_db": golden<DiffDoc>("diff") })
    studio
      .on("GET runs", { total: 3, runs: [other, mine], next_before: null })
      .on("GET runs/r_base", {
        ...rOK,
        id: "r_base",
        agent: "orders",
        children: [],
        holes: [],
        compactions: [],
      })
    const { router } = renderApp("/compare")
    const pick = await waitFor(() => {
      const b = document.querySelector<HTMLElement>('[data-pick="r_other"]')
      expect(b).toBeTruthy()
      return b!
    })
    expect(
      document.querySelector("[data-run-picker] h2")!.textContent
    ).toContain("pick a base run")
    pick.click()
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ a: "r_other" })
    )
    cleanup()

    // A base named: its agent's runs first, the base itself never.
    serve(["diff"])
    studio
      .on("GET runs", {
        total: 3,
        runs: [other, mine, { ...mine, id: "r_base" }],
        next_before: null,
      })
      .on("GET runs/r_base", {
        ...rOK,
        id: "r_base",
        agent: "orders",
        public_id: "",
        children: [],
        holes: [],
        compactions: [],
      })
    renderApp("/compare?a=r_base")
    await waitFor(() =>
      expect(
        [...document.querySelectorAll<HTMLElement>("[data-pick]")].map(
          (b) => b.dataset.pick
        )
      ).toEqual(["r_mine", "r_other"])
    )
    expect(
      document.querySelector('[data-pick="r_mine"]')!.textContent
    ).toContain("same agent")
    cleanup()

    // b=r_db,r_dc: two compared runs, as the array form would be.
    serve(["diff"], { "r_da|r_db": golden<DiffDoc>("diff") })
    const second = renderApp("/compare?a=r_da&b=r_db,r_dc")
    await waitFor(() =>
      expect(second.router.state.location.search).toMatchObject({
        a: "r_da",
        b: "r_db,r_dc",
      })
    )
  })

  it("P8-fix-b #13: the base's conversation first, and a find box past the newest 50", async () => {
    const rows = golden<RunsPage>("runs").runs
    const rOK = rows.find((r) => r.id === "r_ok")!
    const newest = { ...rOK, id: "r_new", agent: "billing", public_id: "" }
    const turn = { ...rOK, id: "r_turn2", agent: "billing", public_id: "pub_c" }
    const old = { ...rOK, id: "r_old_deep", agent: "archivist", public_id: "" }
    serve(["diff"])
    studio
      .on("GET runs", (req) => {
        if (req.query.get("public_id") === "pub_c")
          return { total: 1, runs: [turn], next_before: null }
        if (req.query.get("id_prefix") === "r_old")
          return { total: 1, runs: [old], next_before: null }
        if (req.query.get("id_prefix") || req.query.get("agent"))
          return { total: 0, runs: [], next_before: null }
        return { total: 1, runs: [newest], next_before: null }
      })
      .on("GET runs/r_base", {
        ...rOK,
        id: "r_base",
        agent: "orders",
        public_id: "pub_c",
        children: [],
        holes: [],
        compactions: [],
      })
    renderApp("/compare?a=r_base")
    const picks = () =>
      [...document.querySelectorAll<HTMLElement>("[data-pick]")].map(
        (b) => b.dataset.pick
      )
    await waitFor(() => expect(picks()).toEqual(["r_turn2", "r_new"]))
    expect(
      document.querySelector('[data-pick="r_turn2"]')!.textContent
    ).toContain(WHY_PUBLIC)
    // The find box asks the server: a run past the newest page.
    const find = document.querySelector<HTMLInputElement>("[data-pick-find]")!
    fireEvent.change(find, { target: { value: "r_old" } })
    await waitFor(() => expect(picks()[0]).toBe("r_old_deep"))
    expect(
      studio
        .calls("GET runs")
        .some(
          (c) =>
            c.query.get("id_prefix") === "r_old" && c.query.get("all") === "1"
        )
    ).toBe(true)
    fireEvent.change(find, { target: { value: "zzzz" } })
    await waitFor(() =>
      expect(
        document.querySelector("[data-pick-found-none]")?.textContent
      ).toBe(PICK_FOUND_NONE)
    )
  })

  it("without capability diff: no call, the page says why", async () => {
    serve(["playground"], { "r_da|r_db": golden<DiffDoc>("diff") })
    renderApp('/compare?a=r_da&b=["r_db"]')
    await waitFor(() =>
      expect(document.querySelector("[data-compare-why]")).toBeTruthy()
    )
    expect(document.querySelector("[data-step-diff]")).toBeNull()
    expect(studio.calls("GET diff")).toHaveLength(0)
  })
})

describe("the run header's compare links", () => {
  const rOK = golden<RunsPage>("runs").runs.find((r) => r.id === "r_ok")!
  const doc: RunDoc = {
    ...rOK,
    id: "pg_new",
    forked_from: "r_src#3",
    experiment_id: "",
    playground: true,
    trace_id: "",
    children: [],
    holes: [],
    compactions: [],
  }
  const page = (capabilities: string[]) => {
    serve(capabilities)
    studio
      .on("GET runs/pg_new", doc)
      .on("GET runs/pg_new/events", pagedEvents([], { done: true }))
      .on("GET runs/pg_new/transcript", transcriptOf([]))
      .on("GET runs/pg_new/spans", { spans: [] })
    renderApp("/runs/pg_new")
  }

  it("a replayed run: compare with source (the source the base), and compare with…", async () => {
    page(["diff"])
    const box = await waitFor(() => {
      const el = document.querySelector("[data-compare-with]")
      expect(el).toBeTruthy()
      return el!
    })
    const [src, other] = [...box.querySelectorAll("a")]
    expect(src.textContent).toBe("compare with source")
    const u = new URL(src.getAttribute("href")!, "http://x")
    expect(u.pathname).toBe("/compare")
    expect(u.searchParams.get("a")).toBe("r_src")
    expect(u.searchParams.get("step")).toBe("3") // the step the fork continued from
    expect(u.searchParams.get("b")).toBe("pg_new") // UX-F-30: no JSON array
    expect(other.textContent).toBe("compare with…")
    expect(
      new URL(other.getAttribute("href")!, "http://x").searchParams.get("b")
    ).toBeNull()
  })

  it("hidden without capability diff", async () => {
    page([])
    await waitFor(() =>
      expect(document.querySelector("[data-replay-of]")).toBeTruthy()
    )
    expect(document.querySelector("[data-compare-with]")).toBeNull()
  })
})
