// The panel's cost (plan A5.3): each turn's figure beside its tokens,
// own + delegated as two numbers, "—" with its reason (unpriced, a
// running run, unknown) — never a 0 — the header's summed cost ("≥"
// when a turn's is not known), the replayed run's and its experiments'
// figures in the result pane (its replays of the turn, not Studio's
// saved experiment), all gated on capability "pricing".
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import type { RunRow } from "../lib/api"
import { CACHE_HINT } from "../lib/cost"
import {
  $,
  all,
  assistant,
  ATTRS,
  baseRoutes,
  fakeStudio,
  META,
  mount,
  page,
  runEvents,
  runExperiment,
  runRow,
  T0,
  teardown,
  setup,
  transcript,
  user,
} from "./testkit"
import type { Route } from "./testkit"

beforeEach(setup)
afterEach(teardown)

const PRICED = { ...META, capabilities: [...META.capabilities, "pricing"] }
const UNPRICED_WHY =
  "openai/kimi-k2.7-code has no price (no row of the default pricing table matches it)"
const RUNNING_WHY = "running: usage arrives at run end"

/** priced is a run row's cost block as studio/pricing.go writes it. */
const priced = (usd: number, delegated = 0): Partial<RunRow> => ({
  cost_usd: usd,
  cost_known: true,
  cache: "exact",
  delegated_cost_usd: delegated,
  delegated_cost_known: true,
})
const unpriced: Partial<RunRow> = {
  cost_usd: null,
  cost_known: false,
  cost_badge: "unpriced",
  cost_reason: UNPRICED_WHY,
  unpriced_models: ["openai/kimi-k2.7-code"],
  delegated_cost_usd: 0,
  delegated_cost_known: true,
}
/** A scripted model's figure (U7): an exact $0, badge scripted. */
const scripted: Partial<RunRow> = {
  cost_usd: 0,
  cost_known: true,
  cost_badge: "scripted",
  cost_reason: "a scripted model: nothing bills it, an exact $0",
  cache: "exact",
  delegated_cost_usd: 0,
  delegated_cost_known: true,
}

async function turns(
  rows: RunRow[],
  meta: Route = PRICED,
  more: { next_before: string; next_before_id: string } | null = null
) {
  const routes = baseRoutes()
  routes["runs?public_id=pub_orders&limit=50"] = {
    total: rows.length + (more ? 50 : 0),
    runs: rows,
    next_before: null,
    ...more,
  }
  // A running turn is followed: its row is re-read from the run's own doc.
  for (const r of rows) routes[`runs/${r.id}`] = { ...r, children: [] }
  fakeStudio(routes, meta)
  const el = await mount(ATTRS)
  return el
}

/** cost reads a figure's visible words and its title. */
const cost = (n: Element | null | undefined) => ({
  text: n?.firstChild?.textContent,
  title: n?.getAttribute("title"),
})

describe("a turn's cost beside its tokens", () => {
  it("a priced turn reads the API's figure in Studio's format, its exact figure the title", async () => {
    const el = await turns([runRow(priced(0.0029))])
    const c = $(el, ".weft-turn [data-weft-turn-cost]")!
    expect(cost(c.querySelector("[data-weft-cost]"))).toEqual({
      text: "$0.0029",
      title: "$0.002900 USD",
    })
    expect(c.textContent).toBe("$0.0029")
    // Beside the tokens, in the second line.
    expect($(el, ".weft-turn .weft-row2")!.textContent).toContain("10→4")
  })

  it("an unpriced model: — with pricing.go's reason as the title and in the accessible text, never $0.00", async () => {
    const el = await turns([runRow(unpriced)])
    const c = $(el, ".weft-turn [data-weft-turn-cost] [data-weft-cost]")!
    expect(c.getAttribute("data-weft-cost")).toBe("unknown")
    expect(cost(c)).toEqual({ text: "—", title: UNPRICED_WHY })
    expect(c.querySelector(".weft-sr")!.textContent).toBe(
      ` not known: ${UNPRICED_WHY}`
    )
    expect($(el, ".weft-turn")!.textContent).not.toContain("$0.00")
  })

  it("a running turn: — with the API's reason (usage arrives at run end)", async () => {
    const el = await turns([
      runRow({
        status: "running",
        finished: null,
        cost_usd: null,
        cost_known: false,
        cost_badge: "unknown",
        cost_reason: RUNNING_WHY,
        delegated_cost_usd: 0,
        delegated_cost_known: true,
      }),
    ])
    expect(
      cost($(el, ".weft-turn [data-weft-turn-cost] [data-weft-cost]"))
    ).toEqual({ text: "—", title: RUNNING_WHY })
  })

  it("own + delegated: two figures, never folded — and an unknown delegated part says why", async () => {
    const why =
      "a subagent run is still running: s_01-t1/c1 (running: usage arrives at run end)"
    const el = await turns([
      runRow({ id: "s_01-t2", turn: 2, ...priced(0.0029, 0.0011) }),
      runRow({
        id: "s_01-t1",
        ...priced(0.002),
        delegated_cost_usd: null,
        delegated_cost_known: false,
        delegated_cost_reason: why,
      }),
    ])
    const rows = all(el, ".weft-turn [data-weft-turn-cost]")
    expect(rows[0].textContent).toBe("$0.0029 + $0.0011 delegated")
    const parts = rows[1].querySelectorAll("[data-weft-cost]")
    expect(cost(parts[0])).toEqual({ text: "$0.0020", title: "$0.002000 USD" })
    expect(cost(parts[1])).toEqual({ text: "—", title: why })
  })

  it("priced without cache splits: Studio's visible mark beside the figure, its words the title", async () => {
    const el = await turns([
      runRow({ ...priced(0.5), cache: "not_reported" }),
      runRow({ id: "s_01-t0", ...unpriced, cache: "not_reported" }),
    ])
    const [a, b] = all(el, ".weft-turn [data-weft-turn-cost]")
    const hint = a.querySelector("[data-weft-cache-hint]")!
    expect(hint.textContent).toBe("cache not reported")
    expect(hint.getAttribute("title")).toBe(CACHE_HINT)
    // An unknown figure carries no mark (CacheHint's rule).
    expect(b.querySelector("[data-weft-cache-hint]")).toBeNull()
  })

  it("a row without the delegated fields reads its own figure alone (runTotal's rule)", async () => {
    const el = await turns([
      runRow({ cost_usd: 0.0029, cost_known: true, cache: "exact" }),
    ])
    expect($(el, ".weft-turn [data-weft-turn-cost]")!.textContent).toBe(
      "$0.0029"
    )
  })
})

describe("the header's summed cost", () => {
  it("every turn priced: the sum of own + delegated", async () => {
    const el = await turns([
      runRow({ id: "s_01-t2", turn: 2, ...priced(0.0029, 0.0011) }),
      runRow(priced(0.006)),
    ])
    expect(cost($(el, "[data-weft-cost-total] [data-weft-cost]"))).toEqual({
      text: "$0.01",
      title: "$0.010000 USD",
    })
  })

  it("a turn not priced: ≥ the known part, how many are priced and the reason — never a whole figure", async () => {
    const el = await turns([
      runRow({ id: "s_01-t2", turn: 2, ...priced(0.0029, 0.0011) }),
      runRow(unpriced),
    ])
    const c = $(el, "[data-weft-cost-total] [data-weft-cost]")!
    expect(c.firstChild?.textContent).toBe("≥ $0.0040 · 1 of 2 turns priced")
    expect(c.getAttribute("title")).toBe(
      `1 of 2 turns priced · ${UNPRICED_WHY}`
    )
    expect(c.querySelector(".weft-sr")!.textContent).toContain(UNPRICED_WHY)
    // U7's Done line: the header carries one unpriced line — the model
    // once, a link to Studio's pricing table prefilled for it.
    const u = all(el, "[data-weft-unpriced]")
    expect(u).toHaveLength(1)
    expect(u[0].textContent).toBe(
      "1 turn unpriced: no row for openai/kimi-k2.7-code"
    )
    expect(u[0].getAttribute("title")).toBe(
      "no row of the pricing table matches openai/kimi-k2.7-code · price in Studio"
    )
    expect(u[0].getAttribute("href")).toContain(
      `/settings?price=${encodeURIComponent("openai/kimi-k2.7-code")}`
    )
    // The tokens' total beside it is untouched.
    expect($(el, "[data-weft-stats]")!.textContent).toBe("2 turns · 20→8 tok")
  })

  it("older turns not loaded: ≥ the loaded turns' sum, never a whole figure, the title says so", async () => {
    const el = await turns(
      [
        runRow({ id: "s_01-t2", turn: 2, ...priced(0.0029, 0.0011) }),
        runRow(priced(0.006)),
      ],
      PRICED,
      { next_before: T0, next_before_id: "s_01-t1" }
    )
    expect($(el, "[data-weft-stats]")!.textContent).toContain("2+ turns")
    const c = $(el, "[data-weft-cost-total] [data-weft-cost]")!
    expect(cost(c)).toEqual({
      text: "≥ $0.01",
      title: "the 2 loaded turns only — load older turns for more",
    })
    // A partial sum is not "not known": its accessible words say partial.
    expect(c.querySelector(".weft-sr")!.textContent).toBe(
      " partial: the 2 loaded turns only — load older turns for more"
    )
  })

  it("one loaded turn of more: singular", async () => {
    const el = await turns([runRow(priced(0.006))], PRICED, {
      next_before: T0,
      next_before_id: "s_01-t0",
    })
    expect(cost($(el, "[data-weft-cost-total] [data-weft-cost]"))).toEqual({
      text: "≥ $0.0060",
      title: "the 1 loaded turn only — load older turns for more",
    })
  })

  it("a turn whose delegated part alone is unknown keeps its own figure in the sum", async () => {
    const why =
      "a subagent run's cost is not known yet: s_01-t1/0/c1 (running: usage arrives at run end)"
    const el = await turns([
      runRow({ id: "s_01-t2", turn: 2, ...priced(0.004) }),
      runRow({
        ...priced(0.002),
        delegated_cost_usd: null,
        delegated_cost_known: false,
        delegated_cost_reason: why,
      }),
    ])
    const c = $(el, "[data-weft-cost-total] [data-weft-cost]")!
    expect(c.firstChild?.textContent).toBe("≥ $0.0060 · 1 of 2 turns priced")
    expect(c.getAttribute("title")).toBe(
      `1 of 2 turns priced · delegated: ${why}`
    )
    // Not known yet is not unpriced: no unpriced line.
    expect(all(el, "[data-weft-unpriced]")).toHaveLength(0)
  })

  it("no turn known: —, the reasons its title", async () => {
    const el = await turns([runRow(unpriced)])
    expect(cost($(el, "[data-weft-cost-total] [data-weft-cost]"))).toEqual({
      text: "—",
      title: `0 of 1 turns priced · ${UNPRICED_WHY}`,
    })
  })

  it("scripted turns (U7): $0 · scripted on each row and in the header, no unpriced line", async () => {
    const el = await turns([
      runRow({ id: "s_01-t2", turn: 2, ...scripted }),
      runRow(scripted),
    ])
    expect(
      all(el, ".weft-turn [data-weft-turn-cost]").map((n) => n.textContent)
    ).toEqual(["$0 · scripted", "$0 · scripted"])
    expect(cost($(el, "[data-weft-cost-total] [data-weft-cost]")).text).toBe(
      "$0 · scripted"
    )
    expect(all(el, "[data-weft-unpriced]")).toHaveLength(0)
  })

  it("only scripted turns priced beside an unpriced one (U7): —, never ≥ $0.00", async () => {
    const el = await turns([
      runRow({ id: "s_01-t2", turn: 2, ...scripted }),
      runRow(unpriced),
    ])
    expect(cost($(el, "[data-weft-cost-total] [data-weft-cost]"))).toEqual({
      text: "—",
      title: `1 of 2 turns priced · ${UNPRICED_WHY}`,
    })
  })
})

describe("the pricing capability gates every figure", () => {
  it("off: no cost on a turn row nor in the header — no empty cell, no —", async () => {
    const el = await turns(
      [runRow(priced(0.0029)), runRow({ id: "s_01-t0", ...unpriced })],
      META
    )
    expect(all(el, ".weft-turn").length).toBe(2)
    expect(
      all(el, "[data-weft-turn-cost], [data-weft-cost-total], [data-weft-cost]")
    ).toHaveLength(0)
    expect($(el, ".weft-turn")!.textContent).not.toContain("$")
  })
})

describe("the experiment's cost in the result pane", () => {
  /** An experiment of s_01-t1 that ended before its command named it,
   * beside an earlier one the turn list already holds. */
  function routes(): Record<string, Route> {
    const r = baseRoutes()
    const pg = (id: string, over: Partial<RunRow>) =>
      runRow({
        id,
        playground: true,
        session_id: "",
        forked_from: "s_01-t1#0",
        ...over,
      })
    r["runs?public_id=pub_orders&limit=50"] = {
      total: 3,
      runs: [
        runRow(priced(0.0029)),
        pg("pg_old", { started: T0, ...priced(0.002) }),
        pg("pg_x1", priced(0.004, 0.001)),
      ],
      next_before: null,
    }
    r["POST playground/runs"] = { command_id: "cmd_1", state: "queued" }
    r["playground/commands/cmd_1"] = {
      command_id: "cmd_1",
      state: "finished",
      run_id: "pg_x1",
      status: "succeeded",
      error: null,
      created: T0,
      updated: T0,
    }
    r["runs/pg_x1"] = { ...pg("pg_x1", priced(0.004, 0.001)), children: [] }
    r["runs/pg_x1/events?after=0&limit=500"] = page(runEvents("pg_x1"))
    r["runs/pg_x1/transcript"] = transcript(
      [user("where is my order #4411?")],
      [assistant("It is delayed until Friday.")]
    )
    return r
  }

  it("the replayed run's whole bill beside its tokens, and the experiments' sum over the runs listed", async () => {
    fakeStudio(routes(), PRICED)
    const el = await mount(ATTRS)
    await runExperiment(el)
    const res = $(el, ".weft-xres [data-weft-result-cost] [data-weft-cost]")
    expect(cost(res)).toEqual({ text: "$0.0050", title: "$0.005000 USD" })
    // The folded figure says what it folds (the playground's wrapper title).
    expect(
      $(el, ".weft-xres [data-weft-result-cost]")!.getAttribute("title")
    ).toBe("the run's whole bill: its own model calls + its subagents'")
    const x = $(el, ".weft-xres [data-weft-experiment-cost]")!
    expect(x.textContent).toBe("2 replays of this turn: $0.0070")
  })

  it("a replay not priced (a scripted one): the priced replays still sum, ≥, with sumCost's reason", async () => {
    const r = routes()
    const list = r["runs?public_id=pub_orders&limit=50"] as { runs: RunRow[] }
    list.runs[1] = { ...list.runs[1], ...unpriced }
    fakeStudio(r, PRICED)
    const el = await mount(ATTRS)
    await runExperiment(el)
    const c = $(el, ".weft-xres [data-weft-experiment-cost] [data-weft-cost]")
    expect(cost(c)).toEqual({
      text: "≥ $0.0050 · 1 of 2 runs priced",
      title: `1 of 2 runs priced · ${UNPRICED_WHY}`,
    })
  })

  it("older turns not loaded: the replays' sum says loaded only, ≥, the header's words", async () => {
    const r = routes()
    Object.assign(r["runs?public_id=pub_orders&limit=50"] as object, {
      next_before: T0,
      next_before_id: "s_01-t0",
    })
    fakeStudio(r, PRICED)
    const el = await mount(ATTRS)
    await runExperiment(el)
    const x = $(el, ".weft-xres [data-weft-experiment-cost]")!
    expect(x.firstChild?.textContent).toBe(
      "2 replays of this turn (loaded only): "
    )
    expect(cost(x.querySelector("[data-weft-cost]"))).toEqual({
      text: "≥ $0.0070",
      title: "the 2 loaded replays only — load older turns for more",
    })
  })

  it("pricing off: the pane draws no figure", async () => {
    fakeStudio(routes(), META)
    const el = await mount(ATTRS)
    await runExperiment(el)
    expect($(el, ".weft-xres")).toBeTruthy()
    expect(all(el, ".weft-xres [data-weft-cost]")).toHaveLength(0)
  })
})
