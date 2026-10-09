// The step-aligned compare in Studio (plan E3, E3.2): the compare page
// (N-way: N−1 calls of GET /api/diff against one base), the run
// header's "compare with…" links, gated on capability "diff" — every
// response served from the E3.1 goldens (studio/testdata/api/diff*).
import { cleanup, configure, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunsPage } from "@/lib/api"
import type { DiffDoc } from "@/lib/stepdiff"
import { renderApp, stubBrowser } from "@/test/app"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, apiError, golden, pagedEvents, transcriptOf } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const SAME = { system: "same", tool_calls: "same", tool_results: "same", text: "same", usage: "same" }

let studio: FakeStudio

/** serve answers /api/diff from the given responses, keyed "a|b". */
function serve(capabilities: string[], diffs: Partial<Record<string, DiffDoc>> = {}) {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  studio = new FakeStudio()
    .on("GET meta", { ...golden<Record<string, unknown>>("meta"), capabilities })
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
    for (const td of tr.querySelectorAll(`[data-diff-run="${run}"][data-diff-state]`))
      row[td.getAttribute("data-diff-cell")!] = td.getAttribute("data-diff-state")!
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

describe("the compare page", () => {
  it("diff.golden.json: one 'changed at step 3' marker (tool results) and every other row the same", async () => {
    const doc = golden<DiffDoc>("diff")
    serve(["diff"], { "r_da|r_db": doc })
    renderApp('/compare?a=r_da&b=["r_db"]')
    await waitFor(() => expect(document.querySelector("[data-step-diff]")).toBeTruthy())
    expect(markers()).toEqual([{ step: "3", run: "r_db", text: "changed at step 3 · tool results" }])
    expect(cells("r_db")).toEqual({ "0": SAME, "1": SAME, "2": SAME, "3": { ...SAME, tool_results: "changed" }, "4": SAME })
    const changed = document.querySelector('[data-diff-step="3"] [data-diff-run="r_db"][data-diff-cell="tool_results"]')!
    expect(changed.textContent).toBe("order 3 lost")
    expect(document.querySelector('[data-diff-step="3"] [data-diff-run="r_da"][data-diff-cell="tool_results"]')!.textContent).toBe("order 3 shipped")
    expect(document.querySelector('[data-diff-marker="3"] a')!.getAttribute("href")).toBe("/runs/r_db?step=3")
    expect(studio.calls("GET diff").map((r) => r.query.toString())).toEqual(["a=r_da&b=r_db"])
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
    await waitFor(() => expect(document.querySelector("[data-step-diff]")).toBeTruthy())
    expect(studio.calls("GET diff").map((r) => r.query.get("a"))).toEqual(["r_da", "r_da"])
    expect(markers()).toEqual([
      { step: "3", run: "r_db", text: "changed at step 3 · tool results" },
      { step: "4", run: "r_dc", text: "changed at step 4 · only in the base run" },
    ])
    expect(cells("r_dc")["4"]).toEqual({ system: "missing", tool_calls: "missing", tool_results: "missing", text: "missing", usage: "missing" })
    expect(cells("r_dc")["3"]).toEqual(SAME)
  })

  it("a column a side did not record is not comparable — never same — and its holes are badges", async () => {
    serve(["diff"], { "old1|old2": golden<DiffDoc>("diff-not-recorded") })
    renderApp('/compare?a=old1&b=["old2"]')
    await waitFor(() => expect(document.querySelector("[data-step-diff]")).toBeTruthy())
    expect(markers()).toEqual([])
    for (const row of Object.values(cells("old2"))) for (const s of Object.values(row)) expect(s).toBe("unknown")
    expect(document.querySelector('[data-diff-step="0"] [data-diff-run="old2"][data-diff-cell="text"]')!.textContent).toBe("not comparable")
    expect(document.querySelector("[data-diff-none]")!.textContent).toBe("old2: no step changed among the columns both runs recorded")
    expect(document.querySelectorAll('[data-diff-step="0"] [data-hole="not_recorded"]').length).toBe(2)
  })

  it("a read token's diff: system hidden, badged on each side; a truncated response badged response_cap", async () => {
    const doc = golden<DiffDoc>("diff-hidden")
    doc.holes = [{ hole: "truncated", reason: "this response reads a bounded number of steps", fix: "open the later steps" }]
    serve(["diff"], { "r_ta|r_tb": doc })
    renderApp('/compare?a=r_ta&b=["r_tb"]')
    await waitFor(() => expect(document.querySelector("[data-step-diff]")).toBeTruthy())
    expect(document.querySelectorAll('[data-step-diff] tbody [data-hole="hidden"]').length).toBe(10)
    expect(document.querySelector('[data-diff-holes] [data-hole="truncated"]')).toBeTruthy()
    expect(markers().map((m) => m.step)).toEqual(["3"])
  })

  it("without capability diff: no call, the page says why", async () => {
    serve(["playground"], { "r_da|r_db": golden<DiffDoc>("diff") })
    renderApp('/compare?a=r_da&b=["r_db"]')
    await waitFor(() => expect(document.querySelector("[data-compare-why]")).toBeTruthy())
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
    expect(JSON.parse(u.searchParams.get("b")!)).toEqual(["pg_new"])
    expect(other.textContent).toBe("compare with…")
    expect(new URL(other.getAttribute("href")!, "http://x").searchParams.get("b")).toBeNull()
  })

  it("hidden without capability diff", async () => {
    page([])
    await waitFor(() => expect(document.querySelector("[data-replay-of]")).toBeTruthy())
    expect(document.querySelector("[data-compare-with]")).toBeNull()
  })
})
