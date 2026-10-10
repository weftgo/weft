// Cost in Studio's pages (plan A5, web half): the runs table's Cost
// column (a priced figure, "—" with the reason, the sort through
// /api/search?sort=cost or the loaded page), the run header's own and
// delegated figures, the step card, the compare table's cost column,
// the playground matrix's per-variant total, the sessions list — and
// none of it without capability "pricing".
import { cleanup, configure, fireEvent, screen, waitFor, within } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { setStudioToken } from "@/lib/api"
import type { RunDoc, RunRow, RunsPage, SessionsPage, StepDoc } from "@/lib/api"
import type { DiffDoc } from "@/lib/stepdiff"
import { renderApp, stubBrowser } from "@/test/app"
import { valueOf } from "@/test/select"
import { FakeEventSource } from "@/test/fake-event-source"
import { FakeStudio, apiError, golden, pagedEvents, transcriptOf } from "@/test/fake-studio"

configure({ asyncUtilTimeout: 10_000 })
vi.setConfig({ testTimeout: 30_000 })

const meta = (capabilities: string[]) => ({ ...golden<Record<string, unknown>>("meta"), capabilities })
const runs = golden<RunsPage>("runs")
const rOK = runs.runs.find((r) => r.id === "r_ok")!
/** A run whose end never arrived: cost unknown (badge "unknown"). */
const rStale = runs.runs.find((r) => r.id === "r_stale")!
/** A finished run on a model no pricing row matches: unpriced. */
const rFail = runs.runs.find((r) => r.id === "r_fail")!
const row = (over: Partial<RunRow>): RunRow => ({ ...rOK, ...over })
/** A run priced at usd (own) and del (its subagents'; null: unpriced). */
const priced = (id: string, usd: number, del: number | null = 0, over: Partial<RunRow> = {}): RunRow =>
  row({
    id,
    model: { provider: "anthropic", name: "claude-opus-5-5" },
    cost_usd: usd,
    cost_known: true,
    cost_badge: undefined,
    cost_reason: undefined,
    cache: "exact",
    delegated_cost_usd: del,
    delegated_cost_known: del !== null,
    delegated_cost_reason: del === null ? "a subagent run is not priced: c1 (no row of the default pricing table matches x/y)" : undefined,
    ...over,
  })

let studio: FakeStudio
beforeEach(() => {
  stubBrowser()
  FakeEventSource.reset()
  vi.stubGlobal("EventSource", FakeEventSource)
  setStudioToken("")
  studio = new FakeStudio().install()
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setStudioToken("")
})

const costOf = (id: string) => {
  const tr = screen.getByText(id, { selector: "span" }).closest("tr")!
  return tr.querySelector<HTMLElement>("[data-cost-cell]")
}
const listed = () =>
  [...document.querySelectorAll("tbody tr")].map((tr) => tr.querySelector("a span[title]")?.getAttribute("title"))

describe("the runs table's Cost column", () => {
  const list = [priced("r_cheap", 0.0042), priced("r_dear", 0.5, 0.25), rStale]
  const withUnpriced = [...list, rFail]

  it("a priced run shows the API's cost_usd; an unpriced one — with its reason; delegated apart", async () => {
    studio.on("GET meta", meta(["pricing", "search"])).on("GET runs", { total: 4, runs: withUnpriced, next_before: null })
    renderApp("/runs")
    await screen.findByText("r_cheap")
    expect(within(costOf("r_cheap")!).getByText("$0.0042").getAttribute("data-cost")).toBe("0.0042")
    const dear = costOf("r_dear")!
    expect(within(dear).getByText("$0.50")).toBeTruthy()
    expect(dear.querySelector("[data-delegated-cost]")!.textContent).toContain("$0.25")
    const unpriced = within(costOf("r_fail")!).getByText("—")
    expect(unpriced.getAttribute("title")).toBe(rFail.cost_reason)
    expect(unpriced.getAttribute("data-cost-badge")).toBe("unpriced")
    expect(costOf("r_fail")!.textContent).not.toContain("$0.00")
    // The run whose end never arrived: — as well, badged unknown, its reason the title.
    const unknown = within(costOf("r_stale")!).getByText("—")
    expect(unknown.getAttribute("data-cost-badge")).toBe("unknown")
    expect(unknown.getAttribute("title")).toBe(rStale.cost_reason)
    expect(unknown.getAttribute("title")).toContain("never arrived")
    expect(costOf("r_stale")!.textContent).not.toContain("$0.00")
  })

  it("sorting by cost asks /api/search?sort=cost when the Studio searches and prices", async () => {
    studio
      .on("GET meta", meta(["pricing", "search"]))
      .on("GET runs", { total: 3, runs: list, next_before: null })
      .on("GET search", { total: 3, runs: [list[1], list[0], rStale], sort: "cost", next_offset: null })
    renderApp("/runs")
    await screen.findByText("r_cheap")
    const head = document.querySelector<HTMLElement>("[data-cost-sort]")!
    expect(head.getAttribute("data-cost-sort")).toBe("server")
    fireEvent.click(head)
    await waitFor(() => expect(studio.calls("GET search")).toHaveLength(1))
    expect(studio.calls("GET search")[0].query.get("sort")).toBe("cost")
    await waitFor(() => expect(listed()).toEqual(["r_dear", "r_cheap", "r_stale"]))
  })

  it("without search, the column sorts the loaded rows only and says so", async () => {
    studio.on("GET meta", meta(["pricing"])).on("GET runs", { total: 3, runs: [rStale, list[0], list[1]], next_before: null })
    renderApp("/runs")
    await screen.findByText("r_cheap")
    const head = document.querySelector<HTMLElement>("[data-cost-sort]")!
    expect(head.getAttribute("data-cost-sort")).toBe("page")
    expect(head.getAttribute("title")).toContain("sorts the loaded rows only")
    fireEvent.click(head)
    await waitFor(() => expect(listed()).toEqual(["r_dear", "r_cheap", "r_stale"]))
    expect(studio.calls("GET search")).toHaveLength(0)
  })

  it("draws no cost cell, header or sort without capability pricing", async () => {
    studio.on("GET meta", meta(["search"])).on("GET runs", { total: 3, runs: list, next_before: null })
    renderApp("/runs")
    await screen.findByText("r_cheap")
    expect(document.querySelector("[data-cost-cell]")).toBeNull()
    expect(document.querySelector("[data-cost-sort]")).toBeNull()
    expect(screen.queryByText("$0.0042")).toBeNull()
    expect([...document.querySelectorAll("thead th")].map((th) => th.textContent)).not.toContain("cost")
  })
})

describe("the sessions list", () => {
  it("a cost column under pricing, — with the reason; none without", async () => {
    const sessions = golden<SessionsPage>("sessions")
    studio.on("GET meta", meta(["pricing"])).on("GET sessions", sessions)
    renderApp("/sessions")
    await screen.findByText("s_orders")
    const cells = document.querySelectorAll("[data-cost-cell]")
    expect(cells).toHaveLength(2)
    expect(cells[0].querySelector("[data-cost]")!.getAttribute("title")).toBe(sessions.sessions[0].cost_reason)

    studio.on("GET meta", meta([]))
    renderApp("/sessions")
    await screen.findByText("s_orders")
    expect(document.querySelector("[data-cost-cell]")).toBeNull()
  })
})

describe("the run header", () => {
  const stored = (id: string) =>
    [
      { type: "run_start", id, model: { provider: "anthropic", name: "claude-opus-5-5" }, agent: "orders" },
      { type: "step_start", run_id: id, index: 0 },
      { type: "step_finish", run_id: id, index: 0, reason: "stop", usage: { input_tokens: 9, output_tokens: 3 } },
      { type: "run_finish", run_id: id, usage: { input_tokens: 9, output_tokens: 3 }, steps: 1 },
    ].map((event, pos) => ({ pos, time: rOK.started, event }))
  const serveRun = (doc: RunDoc, caps: string[]) =>
    studio
      .on("GET meta", meta(caps))
      .on(`GET runs/${doc.id}`, doc)
      .on(`GET runs/${doc.id}/events`, pagedEvents(stored(doc.id)))
      .on(`GET runs/${doc.id}/spans`, { spans: [] })
      .on(`GET runs/${doc.id}/transcript`, transcriptOf([]))

  it("a run with subagents shows own and delegated as two numbers", async () => {
    const child = priced("r_p/0/call_1", 0.005, 0, { parent_run_id: "r_p", parent_call_id: "call_1" })
    serveRun({ ...priced("r_p", 0.01, 0.005, { steps: 1, event_count: 4 }), children: [child] }, ["pricing"])
    renderApp("/runs/r_p?view=story")
    const cost = await waitFor(() => {
      const el = document.querySelector<HTMLElement>("[data-run-cost]")
      expect(el).toBeTruthy()
      return el!
    })
    expect(within(cost).getByLabelText(/^own cost/).textContent).toBe("$0.01")
    expect(within(cost).getByLabelText(/^delegated cost/).textContent).toBe("$0.0050")
    expect(cost.textContent).toMatch(/own\s*\$0\.01\s*·\s*delegated\s*\$0\.0050/)
  })

  it("an unpriced run is — with the reason, never $0.00; cache not reported is a hint", async () => {
    serveRun({ ...rFail, id: "r_u", children: [] }, ["pricing"])
    renderApp("/runs/r_u?view=story")
    const cost = await waitFor(() => {
      const el = document.querySelector<HTMLElement>("[data-run-cost]")
      expect(el).toBeTruthy()
      return el!
    })
    const v = within(cost).getByText("—")
    expect(v.getAttribute("title")).toBe(rFail.cost_reason)
    expect(cost.textContent).not.toContain("$")

    cleanup()
    serveRun({ ...priced("r_nc", 0.02, 0, { steps: 1, event_count: 4 }), cache: "not_reported", children: [] }, ["pricing"])
    renderApp("/runs/r_nc?view=story")
    await waitFor(() => expect(document.querySelector("[data-cache-hint]")).toBeTruthy())
  })

  it("a step card shows its own call's cost once the step doc is read; unpriced is — with the reason", async () => {
    serveRun({ ...priced("r_st", 0.01, 0, { steps: 1, event_count: 4 }), children: [] }, ["pricing", "steps"])
    const step0 = golden<StepDoc>("step-0")
    studio.on("GET runs/r_st/steps/0", { ...step0, run_id: "r_st" })
    renderApp("/runs/r_st?view=story")
    const card = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-step="0"]')
      expect(el?.querySelector('[data-attempts="0"]')).toBeTruthy()
      return el!
    })
    expect(card.querySelector("[data-step-cost]")).toBeNull()
    fireEvent.click(within(card.querySelector<HTMLElement>('[data-attempts="0"]')!).getByRole("button", { name: /attempts/ }))
    const cost = await waitFor(() => {
      const el = card.querySelector<HTMLElement>("[data-step-cost] [data-cost]")
      expect(el).toBeTruthy()
      return el!
    })
    expect(cost.textContent).toBe("—")
    expect(cost.getAttribute("title")).toBe(step0.cost_reason)
  })

  it("no cost without capability pricing", async () => {
    serveRun({ ...priced("r_off", 0.01, 0, { steps: 1, event_count: 4 }), children: [] }, [])
    renderApp("/runs/r_off?view=story")
    await screen.findAllByText("r_off")
    await waitFor(() => expect(document.querySelector("[data-step]")).toBeTruthy())
    expect(document.querySelector("[data-run-cost]")).toBeNull()
    expect(document.querySelector("[data-step-cost]")).toBeNull()
  })
})

describe("the compare table's cost column", () => {
  const step = (run: string, n: number, cost: Partial<StepDoc>) => ({
    ...golden<StepDoc>("step-0"),
    run_id: run,
    step: n,
    ...cost,
  })
  const known = (usd: number) => ({ cost_usd: usd, cost_known: true, cost_badge: undefined, cost_reason: undefined, cache: "exact" })
  const unpriced = { cost_usd: null, cost_known: false, cost_badge: "unpriced" as const, cost_reason: "no row matches x/glm", cache: "exact" }

  function serveCompare(caps: string[]) {
    const doc = golden<DiffDoc>("diff")
    studio.on("GET meta", meta(caps)).on("GET diff", (req) =>
      req.query.get("a") === "r_da" && req.query.get("b") === "r_db" ? doc : apiError(404, "not_found", "no such run")
    )
    for (let n = 0; n < 5; n++) {
      studio.on(`GET runs/r_da/steps/${n}`, step("r_da", n, known(0.01)))
      // r_db: step 1 dearer, step 2 unpriced, the rest the same.
      studio.on(`GET runs/r_db/steps/${n}`, step("r_db", n, n === 1 ? known(0.02) : n === 2 ? unpriced : known(0.01)))
    }
  }
  const costCell = (n: number, run: string) =>
    document.querySelector<HTMLElement>(`[data-diff-step="${n}"] [data-diff-run="${run}"][data-diff-cell="cost"]`)

  it("each run's step cost, compared by the step compare's rule: unknown is not comparable, never a change", async () => {
    serveCompare(["diff", "pricing", "steps"])
    renderApp('/compare?a=r_da&b=["r_db"]')
    await waitFor(() => expect(costCell(2, "r_db")?.getAttribute("data-diff-state")).toBe("unknown"))
    expect(costCell(0, "r_da")!.textContent).toBe("$0.01")
    expect(costCell(0, "r_db")!.getAttribute("data-diff-state")).toBe("same")
    expect(costCell(1, "r_db")!.getAttribute("data-diff-state")).toBe("changed")
    expect(costCell(1, "r_db")!.textContent).toBe("$0.02")
    expect(costCell(2, "r_db")!.textContent).toBe("not comparable")
    expect(costCell(2, "r_db")!.getAttribute("title")).toContain("no row matches x/glm")
    // Cost is informational: only step 3 (tool results) is a change.
    expect([...document.querySelectorAll("[data-diff-marker]")].map((m) => m.getAttribute("data-diff-marker"))).toEqual(["3"])
  })

  it("no cost column without capability pricing", async () => {
    serveCompare(["diff", "steps"])
    renderApp('/compare?a=r_da&b=["r_db"]')
    await waitFor(() => expect(document.querySelector("[data-step-diff]")).toBeTruthy())
    expect(document.querySelector('[data-diff-cell="cost"]')).toBeNull()
    expect(studio.requests.some((r) => /\/steps\//.test(r.path))).toBe(false)
  })
})

describe("the playground matrix's cost", () => {
  const tool = (name: string, side_effects = "never") => ({ name, side_effects, allow: false })
  const runtimes = {
    runtimes: [
      {
        id: "rt_1",
        host: "laptop",
        pid: 1,
        service: "acme-api",
        env: "dev",
        connected_since: rOK.started,
        last_seen: rOK.started,
        breakpoints: [],
        agents: [{ name: "orders", models: ["glm"], tools: [tool("lookup_order", "safe")], instructions: "You are support." }],
      },
    ],
  }
  const command = (id: string, state: string, run_id = "") => ({
    command_id: id,
    state,
    run_id,
    status: "succeeded",
    error: null,
    created: rOK.started,
    updated: rOK.started,
  })

  async function runMatrix(caps: string[], b: RunRow) {
    let n = 0
    studio
      .on("GET meta", meta(["live", "playground", "runtimes", ...caps]))
      .on("GET runtimes", runtimes)
      .on("GET experiments", { experiments: [] })
      .on("GET runs/r_ok", { ...rOK, children: [] })
      .on("GET runs/r_ok/transcript", golden("transcript-ok"))
      .on("POST experiments", (req) => req.body)
      .on("POST playground/runs", () => command(`cmd_m${++n}`, "queued"))
      .on("GET playground/commands/cmd_m1", command("cmd_m1", "finished", "pg_m1"))
      .on("GET playground/commands/cmd_m2", command("cmd_m2", "finished", "pg_m2"))
      .on("GET runs/pg_m1", { ...priced("pg_m1", 0.03, 0.01, { playground: true }), children: [] })
      .on("GET runs/pg_m2", { ...b, children: [] })
    renderApp("/playground?run=r_ok")
    await waitFor(() => expect(valueOf(screen.getByLabelText("agent"))).toBe("orders"))
    fireEvent.click(screen.getByRole("button", { name: "+ variant" }))
    const go = screen.getByRole("button", { name: "Run matrix" })
    await waitFor(() => expect(go).toHaveProperty("disabled", false))
    fireEvent.click(go)
    await waitFor(() => expect(studio.calls("POST playground/runs")).toHaveLength(2))
  }
  const total = (v: string) => document.querySelector<HTMLElement>(`[data-variant-cost="${v}"]`)

  it("each cell's run cost and each variant's total, summed here", async () => {
    await runMatrix(["pricing"], priced("pg_m2", 0.02, 0, { playground: true }))
    await waitFor(() => expect(total("B")?.textContent).toBe("$0.02"), { timeout: 10_000 })
    // Variant A's one run: own $0.03 + delegated $0.01.
    await waitFor(() => expect(total("A")!.textContent).toBe("$0.04"))
    expect(document.querySelector('[data-cell="A×1"] [data-cell-cost]')!.textContent).toBe("$0.04")
  })

  it("a variant with any unpriced run totals —, with the reason", async () => {
    await runMatrix(["pricing"], { ...rFail, id: "pg_m2", status: "succeeded", playground: true })
    await waitFor(
      () =>
        expect(total("B")?.querySelector("[data-cost]")?.getAttribute("title")).toContain(
          "no row of the default pricing table matches wefttest/script"
        ),
      { timeout: 10_000 }
    )
    expect(total("B")!.textContent).toBe("—")
    expect(total("B")!.querySelector("[data-cost]")!.getAttribute("data-cost")).toBe("unknown")
    await waitFor(() => expect(total("A")?.textContent).toBe("$0.04"))
  })

  it("no cost row without capability pricing", async () => {
    await runMatrix([], priced("pg_m2", 0.02, 0, { playground: true }))
    await waitFor(() => expect(document.querySelector('[data-cell="A×1"]')?.textContent).toContain("finished"), {
      timeout: 10_000,
    })
    expect(total("A")).toBeNull()
    expect(document.querySelector("[data-cell-cost]")).toBeNull()
  })
})
