// Cost in Studio's pages (plan A5, web half): the runs table's Cost
// column (a priced figure, "—" with the reason, the sort through
// /api/search?sort=cost or the loaded page), the run header's own and
// delegated figures, the step card, the compare table's cost column,
// the playground matrix's per-variant total, the sessions list — and
// none of it without capability "pricing".
import {
  cleanup,
  configure,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import axe from "axe-core"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { CostText, RunCost } from "@/components/studio/cost"
import { setStudioToken } from "@/lib/api"
import { costView, LOST_CELL_REASON } from "@/lib/cost"
import type { RunDoc, RunRow, RunsPage, SessionsPage, StepDoc } from "@/lib/api"
import type { DiffDoc } from "@/lib/stepdiff"
import { renderApp, stubBrowser } from "@/test/app"
import { valueOf } from "@/test/select"
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

const meta = (capabilities: string[]) => ({
  ...golden<Record<string, unknown>>("meta"),
  capabilities,
})
const runs = golden<RunsPage>("runs")
const rOK = runs.runs.find((r) => r.id === "r_ok")!
/** A run whose end never arrived: cost unknown (badge "unknown"). */
const rStale = runs.runs.find((r) => r.id === "r_stale")!
/** A finished run on wefttest/script: scripted, an exact $0 (U7). */
const rScripted = runs.runs.find((r) => r.id === "r_fail")!
const KIMI_WHY = "openai/kimi-k2.7-code has no price"
/** A finished run on a model no pricing row matches: unpriced — the
 * fixture's run on an OpenAI-compatible model, as pricing.go writes it. */
const rFail: RunRow = {
  ...rScripted,
  model: { provider: "openai", name: "kimi-k2.7-code" },
  cost_usd: null,
  cost_known: false,
  cost_badge: "unpriced",
  cost_reason: KIMI_WHY,
  unpriced_models: ["openai/kimi-k2.7-code"],
  cache: "not_reported",
}
const row = (over: Partial<RunRow>): RunRow => ({ ...rOK, ...over })
/** A run priced at usd (own) and del (its subagents'; null: unpriced). */
const priced = (
  id: string,
  usd: number,
  del: number | null = 0,
  over: Partial<RunRow> = {}
): RunRow =>
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
    delegated_cost_reason:
      del === null
        ? "a subagent run is not priced: c1 (no row of the default pricing table matches x/y)"
        : undefined,
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

/** The words a sighted reader sees: the element's text less its
 * visually hidden (sr-only) parts. */
const visible = (el: Element | null | undefined): string => {
  if (!el) return ""
  const c = el.cloneNode(true) as Element
  c.querySelectorAll(".sr-only").forEach((n) => n.remove())
  return c.textContent
}

const costOf = (id: string) => {
  const tr = screen.getByText(id, { selector: "span" }).closest("tr")!
  return tr.querySelector<HTMLElement>("[data-cost-cell]")
}
const listed = () =>
  [...document.querySelectorAll("tbody tr")].map((tr) =>
    tr.querySelector("[data-run-link]")?.getAttribute("data-run-link")
  )

describe("the runs table's Cost column", () => {
  const list = [priced("r_cheap", 0.0042), priced("r_dear", 0.5, 0.25), rStale]
  const withUnpriced = [...list, rFail]

  it("a priced run shows the API's cost_usd; an unpriced one — with its reason; delegated apart", async () => {
    studio
      .on("GET meta", meta(["pricing", "search"]))
      .on("GET runs", { total: 4, runs: withUnpriced, next_before: null })
    renderApp("/runs")
    await screen.findByText("r_cheap")
    expect(
      within(costOf("r_cheap")!).getByText("$0.0042").getAttribute("data-cost")
    ).toBe("0.0042")
    const dear = costOf("r_dear")!
    expect(within(dear).getByText("$0.50")).toBeTruthy()
    expect(dear.querySelector("[data-delegated-cost]")!.textContent).toContain(
      "$0.25"
    )
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
    // U7: the unpriced cell carries its small chip; the reason is said
    // once over the list — one line, the model, the pricing link.
    expect(
      costOf("r_fail")!.querySelector("[data-cost-chip]")!.textContent
    ).toBe("unpriced")
    const lines = document.querySelectorAll("[data-unpriced-line]")
    expect(lines).toHaveLength(1)
    expect(lines[0].firstElementChild!.textContent).toBe(
      "1 run unpriced: no row for openai/kimi-k2.7-code"
    )
    expect(lines[0].querySelector("[data-pricing-link]")!.textContent).toBe(
      "price this model…"
    )
    expect(
      lines[0].querySelector("[data-pricing-link]")!.getAttribute("href")
    ).toContain("/settings?price=")
  })

  it("a scripted run reads $0 · scripted; no unpriced line (U7)", async () => {
    studio
      .on("GET meta", meta(["pricing", "search"]))
      .on("GET runs", { total: 2, runs: [rScripted, rOK], next_before: null })
    renderApp("/runs")
    await screen.findByText("r_ok")
    const v = within(costOf("r_fail")!).getByText("$0 · scripted")
    expect(v.getAttribute("data-cost-badge")).toBe("scripted")
    expect(v.getAttribute("data-cost")).toBe("0")
    expect(document.querySelector("[data-unpriced-line]")).toBeNull()
  })

  it("sorting by cost asks /api/search?sort=cost when the Studio searches and prices", async () => {
    studio
      .on("GET meta", meta(["pricing", "search"]))
      .on("GET runs", { total: 3, runs: list, next_before: null })
      .on("GET search", {
        total: 3,
        runs: [list[1], list[0], rStale],
        sort: "cost",
        next_offset: null,
      })
    renderApp("/runs")
    await screen.findByText("r_cheap")
    const head = document.querySelector<HTMLElement>("[data-cost-sort]")!
    expect(head.getAttribute("data-cost-sort")).toBe("server")
    fireEvent.click(head)
    await waitFor(() => expect(studio.calls("GET search")).toHaveLength(1))
    expect(studio.calls("GET search")[0].query.get("sort")).toBe("cost")
    await waitFor(() =>
      expect(listed()).toEqual(["r_dear", "r_cheap", "r_stale"])
    )
  })

  it("the server sort keeps the page's filters on /api/search, pages by next_offset and says its cap", async () => {
    studio
      .on("GET meta", meta(["pricing", "search"]))
      .on("GET runs", { total: 3, runs: list, next_before: null })
      .on("GET search", (req) =>
        req.query.get("offset") === "2"
          ? {
              total: 3,
              runs: [rStale],
              sort: "cost",
              next_offset: null,
              cost_sort_cap: 2000,
            }
          : {
              total: 3,
              runs: [list[1], list[0]],
              sort: "cost",
              next_offset: 2,
              cost_sort_cap: 2000,
            }
      )
    renderApp(
      "/runs?agent=orders&status=succeeded&session=s_1&public_id=pub_1&tag=tier%3Dgold&sort=cost"
    )
    await waitFor(() => expect(listed()).toEqual(["r_dear", "r_cheap"]))
    const q = studio.calls("GET search")[0].query
    expect(Object.fromEntries(q)).toMatchObject({
      sort: "cost",
      agent: "orders",
      status: "succeeded",
      session: "s_1",
      public_id: "pub_1",
      "tag.tier": "gold",
    })
    expect(q.has("offset")).toBe(false)
    expect(q.has("before")).toBe(false)
    // The cap note.
    const cap = document.querySelector<HTMLElement>("[data-cost-sort-cap]")!
    expect(cap.textContent).toContain(
      "sorted by cost among the newest 2,000 matching runs"
    )
    // The next page: offset 2, the same filters.
    fireEvent.click(screen.getByRole("button", { name: "load more" }))
    await waitFor(() =>
      expect(listed()).toEqual(["r_dear", "r_cheap", "r_stale"])
    )
    const q2 = studio.calls("GET search")[1].query
    expect(q2.get("offset")).toBe("2")
    expect(q2.get("agent")).toBe("orders")
    expect(q2.get("tag.tier")).toBe("gold")
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "load more" })).toBeNull()
    )
  })

  it("without search, the column sorts the loaded rows only and says so", async () => {
    studio.on("GET meta", meta(["pricing"])).on("GET runs", {
      total: 3,
      runs: [rStale, list[0], list[1]],
      next_before: null,
    })
    renderApp("/runs")
    await screen.findByText("r_cheap")
    const head = document.querySelector<HTMLElement>("[data-cost-sort]")!
    expect(head.getAttribute("data-cost-sort")).toBe("page")
    expect(head.getAttribute("title")).toContain("sorts the loaded rows only")
    fireEvent.click(head)
    await waitFor(() =>
      expect(listed()).toEqual(["r_dear", "r_cheap", "r_stale"])
    )
    expect(studio.calls("GET search")).toHaveLength(0)
  })

  it("draws no cost cell, header or sort without capability pricing", async () => {
    studio
      .on("GET meta", meta(["search"]))
      .on("GET runs", { total: 3, runs: list, next_before: null })
    renderApp("/runs")
    await screen.findByText("r_cheap")
    expect(document.querySelector("[data-cost-cell]")).toBeNull()
    expect(document.querySelector("[data-cost-sort]")).toBeNull()
    expect(screen.queryByText("$0.0042")).toBeNull()
    expect(
      [...document.querySelectorAll("thead th")].map((th) => th.textContent)
    ).not.toContain("cost")
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
    expect(cells[0].querySelector("[data-cost]")!.getAttribute("title")).toBe(
      sessions.sessions[0].cost_reason
    )

    studio.on("GET meta", meta([]))
    renderApp("/sessions")
    await screen.findByText("s_orders")
    expect(document.querySelector("[data-cost-cell]")).toBeNull()
  })

  it("U7: the cost cell is a number or —; the unpriced turns are said once over the list, with the pricing link", async () => {
    const sessions = golden<SessionsPage>("sessions")
    const unpriced = {
      ...sessions.sessions[0],
      cost_usd: null,
      cost_known: false,
      cost_badge: "unpriced" as const,
      cost_reason:
        "9 of 9 turns unpriced: no price for openai/kimi-k2.7-code, acme/x, acme/y",
      unpriced_models: ["openai/kimi-k2.7-code", "acme/x", "acme/y"],
    }
    studio.on("GET meta", meta(["pricing"])).on("GET sessions", {
      ...sessions,
      sessions: [unpriced, sessions.sessions[1]],
    })
    renderApp("/sessions")
    await screen.findByText("s_orders")
    const cell = document.querySelectorAll("[data-cost-cell]")[0]
    expect(visible(cell)).toBe("—")
    expect(cell.querySelector("[data-cost-chip]")).toBeNull()
    const lines = document.querySelectorAll("[data-unpriced-line]")
    expect(lines).toHaveLength(1)
    expect(lines[0].firstElementChild!.textContent).toBe(
      "1 session unpriced: no row for openai/kimi-k2.7-code, acme/x (+1)"
    )
    expect(lines[0].firstElementChild!.getAttribute("title")).toBe(
      "no row of the pricing table matches openai/kimi-k2.7-code, acme/x, acme/y"
    )
    expect(lines[0].querySelector("[data-pricing-link]")!.textContent).toBe(
      "price these models…"
    )
  })
})

describe("the run header", () => {
  const stored = (id: string) =>
    [
      {
        type: "run_start",
        id,
        model: { provider: "anthropic", name: "claude-opus-5-5" },
        agent: "orders",
      },
      { type: "step_start", run_id: id, index: 0 },
      {
        type: "step_finish",
        run_id: id,
        index: 0,
        reason: "stop",
        usage: { input_tokens: 9, output_tokens: 3 },
      },
      {
        type: "run_finish",
        run_id: id,
        usage: { input_tokens: 9, output_tokens: 3 },
        steps: 1,
      },
    ].map((event, pos) => ({ pos, time: rOK.started, event }))
  const serveRun = (doc: RunDoc, caps: string[]) =>
    studio
      .on("GET meta", meta(caps))
      .on(`GET runs/${doc.id}`, doc)
      .on(`GET runs/${doc.id}/events`, pagedEvents(stored(doc.id)))
      .on(`GET runs/${doc.id}/spans`, { spans: [] })
      .on(`GET runs/${doc.id}/transcript`, transcriptOf([]))

  it("a run with subagents shows own and delegated as two numbers", async () => {
    const child = priced("r_p/0/call_1", 0.005, 0, {
      parent_run_id: "r_p",
      parent_call_id: "call_1",
    })
    serveRun(
      {
        ...priced("r_p", 0.01, 0.005, { steps: 1, event_count: 4 }),
        children: [child],
      },
      ["pricing"]
    )
    renderApp("/runs/r_p?view=story")
    const cost = await waitFor(() => {
      const el = document.querySelector<HTMLElement>("[data-run-cost]")
      expect(el).toBeTruthy()
      return el!
    })
    expect(
      cost.querySelector('[data-cost-part="own"] [data-cost]')!.textContent
    ).toBe("$0.01")
    expect(
      cost.querySelector('[data-cost-part="delegated"] [data-cost]')!
        .textContent
    ).toBe("$0.0050")
    expect(cost.textContent).toMatch(
      /own\s*\$0\.01\s*·\s*delegated\s*\$0\.0050/
    )
  })

  it("a delegated figure unknown because a child is running is badged unknown, the API's reason its words", async () => {
    const running =
      "a subagent run's cost is not known yet: r_rd/0/call_1 (running: usage arrives at run end)"
    const child = {
      ...priced("r_rd/0/call_1", 0, 0, {
        parent_run_id: "r_rd",
        parent_call_id: "call_1",
      }),
      status: "running" as const,
    }
    serveRun(
      {
        ...priced("r_rd", 0.01, null, { steps: 1, event_count: 4 }),
        delegated_cost_reason: running,
        children: [child],
      },
      ["pricing"]
    )
    renderApp("/runs/r_rd?view=story")
    const del = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(
        '[data-run-cost] [data-cost-part="delegated"] [data-cost]'
      )
      expect(el).toBeTruthy()
      return el!
    })
    expect(del.getAttribute("data-cost-badge")).toBe("unknown")
    expect(del.getAttribute("title")).toBe(running)
    expect(visible(del)).toBe("—")
    expect(del.querySelector(".sr-only")!.textContent).toBe(
      ` not known: ${running}`
    )
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
    serveRun(
      {
        ...priced("r_nc", 0.02, 0, { steps: 1, event_count: 4 }),
        cache: "not_reported",
        children: [],
      },
      ["pricing"]
    )
    renderApp("/runs/r_nc?view=story")
    await waitFor(() =>
      expect(document.querySelector("[data-cache-hint]")).toBeTruthy()
    )
  })

  it("a step card shows its own call's cost once the step doc is read; unpriced is — with the reason", async () => {
    serveRun(
      {
        ...priced("r_st", 0.01, 0, { steps: 1, event_count: 4 }),
        children: [],
      },
      ["pricing", "steps"]
    )
    const step0: StepDoc = {
      ...golden<StepDoc>("step-0"),
      cost_usd: null,
      cost_known: false,
      cost_badge: "unpriced",
      cost_reason: KIMI_WHY,
    }
    studio.on("GET runs/r_st/steps/0", { ...step0, run_id: "r_st" })
    renderApp("/runs/r_st?view=story")
    const card = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-step="0"]')
      expect(el?.querySelector('[data-attempts="0"]')).toBeTruthy()
      return el!
    })
    expect(card.querySelector("[data-step-cost]")).toBeNull()
    fireEvent.click(
      within(card.querySelector<HTMLElement>('[data-attempts="0"]')!).getByRole(
        "button",
        { name: /attempts/ }
      )
    )
    const cost = await waitFor(() => {
      const el = card.querySelector<HTMLElement>("[data-step-cost] [data-cost]")
      expect(el).toBeTruthy()
      return el!
    })
    expect(visible(cost)).toBe("—unpriced")
    expect(cost.getAttribute("title")).toBe(step0.cost_reason)
    // The label and the reason ride in the accessible text, never an
    // aria-label; the chip is aria-hidden (the words say it).
    expect(cost.hasAttribute("aria-label")).toBe(false)
    expect(cost.textContent).toBe(
      `step cost — not known: ${step0.cost_reason}unpriced`
    )
  })

  it("no cost without capability pricing", async () => {
    serveRun(
      {
        ...priced("r_off", 0.01, 0, { steps: 1, event_count: 4 }),
        children: [],
      },
      []
    )
    renderApp("/runs/r_off?view=story")
    await screen.findAllByText("r_off")
    await waitFor(() =>
      expect(document.querySelector("[data-step]")).toBeTruthy()
    )
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
  const known = (usd: number) => ({
    cost_usd: usd,
    cost_known: true,
    cost_badge: undefined,
    cost_reason: undefined,
    cache: "exact",
  })
  const unpriced = {
    cost_usd: null,
    cost_known: false,
    cost_badge: "unpriced" as const,
    cost_reason: "no row matches x/glm",
    cache: "exact",
  }

  function serveCompare(caps: string[]) {
    const doc = golden<DiffDoc>("diff")
    studio
      .on("GET meta", meta(caps))
      .on("GET diff", (req) =>
        req.query.get("a") === "r_da" && req.query.get("b") === "r_db"
          ? doc
          : apiError(404, "not_found", "no such run")
      )
    for (let n = 0; n < 5; n++) {
      studio.on(`GET runs/r_da/steps/${n}`, step("r_da", n, known(0.01)))
      // r_db: step 1 dearer, step 2 unpriced, the rest the same.
      studio.on(
        `GET runs/r_db/steps/${n}`,
        step(
          "r_db",
          n,
          n === 1 ? known(0.02) : n === 2 ? unpriced : known(0.01)
        )
      )
    }
  }
  const costCell = (n: number, run: string) =>
    document.querySelector<HTMLElement>(
      `[data-diff-step="${n}"] [data-diff-run="${run}"][data-diff-cell="cost"]`
    )

  it("each run's step cost, compared by the step compare's rule: unknown is not comparable, never a change", async () => {
    serveCompare(["diff", "pricing", "steps"])
    renderApp('/compare?a=r_da&b=["r_db"]')
    await waitFor(() =>
      expect(costCell(2, "r_db")?.getAttribute("data-diff-state")).toBe(
        "unknown"
      )
    )
    expect(costCell(0, "r_da")!.textContent).toBe("$0.01")
    expect(costCell(0, "r_db")!.getAttribute("data-diff-state")).toBe("same")
    expect(costCell(1, "r_db")!.getAttribute("data-diff-state")).toBe("changed")
    expect(costCell(1, "r_db")!.textContent).toBe("$0.02")
    expect(visible(costCell(2, "r_db"))).toBe("not comparable")
    expect(costCell(2, "r_db")!.getAttribute("title")).toContain(
      "no row matches x/glm"
    )
    expect(costCell(2, "r_db")!.querySelector(".sr-only")!.textContent).toBe(
      " not known: no row matches x/glm"
    )
    // A different figure is drawn neutral: the cost column never marks a change.
    expect(costCell(1, "r_db")!.className).not.toContain("bg-ev-error")
    // Cost is informational: only step 3 (tool results) is a change.
    expect(
      [...document.querySelectorAll("[data-diff-marker]")].map((m) =>
        m.getAttribute("data-diff-marker")
      )
    ).toEqual(["3"])
  })

  it("no cost column without capability pricing", async () => {
    serveCompare(["diff", "steps"])
    renderApp('/compare?a=r_da&b=["r_db"]')
    await waitFor(() =>
      expect(document.querySelector("[data-step-diff]")).toBeTruthy()
    )
    expect(document.querySelector('[data-diff-cell="cost"]')).toBeNull()
    expect(studio.requests.some((r) => /\/steps\//.test(r.path))).toBe(false)
  })
})

describe("the playground matrix's cost", () => {
  const tool = (name: string, side_effects = "never") => ({
    name,
    side_effects,
    allow: false,
  })
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
        agents: [
          {
            name: "orders",
            models: ["glm"],
            tools: [tool("lookup_order", "safe")],
            instructions: "You are support.",
          },
        ],
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

  async function runMatrix(caps: string[], b: RunRow, bState = "finished") {
    let n = 0
    studio
      .on("GET meta", meta(["live", "playground", "runtimes", ...caps]))
      .on("GET runtimes", runtimes)
      .on("GET experiments", { experiments: [] })
      .on("GET runs/r_ok", { ...rOK, children: [] })
      .on("GET runs/r_ok/transcript", golden("transcript-ok"))
      .on("POST experiments", (req) => req.body)
      .on("POST playground/runs", () => command(`cmd_m${++n}`, "queued"))
      .on(
        "GET playground/commands/cmd_m1",
        command("cmd_m1", "finished", "pg_m1")
      )
      .on("GET playground/commands/cmd_m2", command("cmd_m2", bState, "pg_m2"))
      .on("GET runs/pg_m1", {
        ...priced("pg_m1", 0.03, 0.01, { playground: true }),
        children: [],
      })
      .on("GET runs/pg_m2", { ...b, children: [] })
    renderApp("/playground?run=r_ok")
    await waitFor(() =>
      expect(valueOf(screen.getByLabelText("agent"))).toBe("orders")
    )
    fireEvent.click(screen.getByRole("button", { name: "+ variant" }))
    const go = screen.getByRole("button", { name: "Run matrix" })
    await waitFor(() => expect(go).toHaveProperty("disabled", false))
    fireEvent.click(go)
    await waitFor(() =>
      expect(studio.calls("POST playground/runs")).toHaveLength(2)
    )
  }
  const total = (v: string) =>
    document.querySelector<HTMLElement>(`[data-variant-cost="${v}"]`)

  it("each cell's run cost and each variant's total, summed here", async () => {
    await runMatrix(["pricing"], priced("pg_m2", 0.02, 0, { playground: true }))
    await waitFor(() => expect(total("B")?.textContent).toBe("$0.02"), {
      timeout: 10_000,
    })
    // Variant A's one run: own $0.03 + delegated $0.01.
    await waitFor(() => expect(total("A")!.textContent).toBe("$0.04"))
    expect(
      document.querySelector('[data-cell="A×1"] [data-cell-cost]')!.textContent
    ).toBe("$0.04")
  })

  it("a variant with any unpriced run totals —, with the reason", async () => {
    await runMatrix(["pricing"], {
      ...rFail,
      id: "pg_m2",
      status: "succeeded",
      playground: true,
    })
    await waitFor(
      () =>
        expect(
          total("B")?.querySelector("[data-cost]")?.getAttribute("title")
        ).toBe(KIMI_WHY),
      { timeout: 10_000 }
    )
    expect(visible(total("B"))).toBe("—unpriced")
    expect(
      total("B")!.querySelector("[data-cost]")!.getAttribute("data-cost")
    ).toBe("unknown")
    await waitFor(() => expect(total("A")?.textContent).toBe("$0.04"))
  })

  it("a lost cell that had started a run makes its variant's total unknown, in its own words", async () => {
    await runMatrix(
      ["pricing"],
      priced("pg_m2", 0.02, 0, { playground: true }),
      "lost"
    )
    await waitFor(
      () =>
        expect(
          total("B")?.querySelector("[data-cost]")?.getAttribute("title")
        ).toBe(LOST_CELL_REASON),
      {
        timeout: 10_000,
      }
    )
    expect(visible(total("B"))).toBe("—")
    expect(
      total("B")!.querySelector("[data-cost]")!.getAttribute("data-cost-badge")
    ).toBe("unknown")
    await waitFor(() => expect(total("A")?.textContent).toBe("$0.04"))
  })

  it("no cost row without capability pricing", async () => {
    await runMatrix([], priced("pg_m2", 0.02, 0, { playground: true }))
    await waitFor(
      () =>
        expect(
          document.querySelector('[data-cell="A×1"]')?.textContent
        ).toContain("finished"),
      {
        timeout: 10_000,
      }
    )
    expect(total("A")).toBeNull()
    expect(document.querySelector("[data-cell-cost]")).toBeNull()
  })
})

describe("the cost figures' accessibility", () => {
  it("an unknown figure says its reason in the accessible text; axe finds nothing", async () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null)
    const { container } = render(
      <div>
        <RunCost
          run={{
            ...rFail,
            delegated_cost_known: false,
            delegated_cost_reason: "running: usage arrives at run end",
          }}
          hasChildren
        />
        <CostText v={costView(rStale)} label="step cost" />
        <CostText v={costView(priced("r_k", 0.5))} />
      </div>
    )
    const res = await axe.run(container)
    expect(
      res.violations.map(
        (v) => `${v.id}: ${v.nodes.map((n) => n.html).join(" | ")}`
      )
    ).toEqual([])
    expect(container.querySelectorAll("[aria-label]")).toHaveLength(0)
    const words = [...container.querySelectorAll("[data-cost-words]")].map(
      (n) => n.textContent
    )
    expect(words).toEqual([
      ` not known: ${rFail.cost_reason}`,
      " not known: running: usage arrives at run end",
      ` not known: ${rStale.cost_reason}`,
    ])
    vi.restoreAllMocks()
  })
})
