// Cost as the pages read it (plan A5): the TS mirrors against the Go
// goldens' cost keys, the USD words, the cost sort, the variant total
// and the compare's cost cell — the step compare's own words.
import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

import type {
  CostFields,
  DelegatedCostFields,
  MetaPricing,
  ModelPrice,
  PricingSettings,
  RunDoc,
  RunRow,
  RunsPage,
  SearchPage,
  SessionsPage,
  StepDoc,
} from "./api"
import { atLeastCost, costCell, costCellWords, costView, delegatedView, formatUSD, LOST_CELL_REASON, lostCellCost, runTotal, sortByCost, sumCost } from "./cost"
import { CELL_REASONS, CELL_WORDS } from "./stepdiff"

const API = resolve(process.cwd(), "../testdata/api")
const golden = <T>(name: string): T => JSON.parse(readFileSync(resolve(API, `${name}.golden.json`), "utf8")) as T

const COST_KEYS: Record<keyof CostFields, true> = {
  cost_usd: true,
  cost_known: true,
  cost_badge: true,
  cost_reason: true,
  cache: true,
}
const DELEGATED_KEYS: Record<keyof DelegatedCostFields, true> = {
  delegated_cost_usd: true,
  delegated_cost_known: true,
  delegated_cost_reason: true,
}
const META_PRICING_KEYS: Record<keyof MetaPricing, true> = { source: true, path: true, dated: true, models: true }
const SETTINGS_KEYS: Record<keyof PricingSettings, true> = { source: true, path: true, dated: true, note: true, models: true }
const PRICE_KEYS: Record<keyof ModelPrice, true> = {
  provider: true,
  model_glob: true,
  input: true,
  output: true,
  cached_input: true,
  cache_write: true,
  reasoning: true,
}
const SEARCH_KEYS: Record<keyof SearchPage, true> = { total: true, runs: true, sort: true, next_offset: true, cost_sort_cap: true }

/** The cost-ish keys an object carries that the mirror does not know. */
function unknownCostKeys(obj: object, known: Record<string, true>): string[] {
  return Object.keys(obj).filter((k) => /cost|^cache$/.test(k) && !(k in known))
}
function unknownKeys(obj: object, known: Record<string, true>): string[] {
  return Object.keys(obj).filter((k) => !(k in known))
}

describe("the TS mirrors know every cost key the Go goldens carry", () => {
  const both = { ...COST_KEYS, ...DELEGATED_KEYS }
  it("run rows (lists, a run document and its children, search hits)", () => {
    const rows: RunRow[] = [
      ...golden<RunsPage>("runs").runs,
      ...golden<RunsPage>("runs-children").runs,
      ...golden<SearchPage>("search").runs,
    ]
    const doc = golden<RunDoc>("run-sub")
    rows.push(doc, ...doc.children)
    for (const r of rows) {
      expect(unknownCostKeys(r, both)).toEqual([])
      expect(r.cost_known).toBeTypeOf("boolean")
      expect(r.delegated_cost_known).toBeTypeOf("boolean")
    }
  })
  it("a step and its children", () => {
    for (const f of ["step-0", "step-1", "step-2"]) {
      const s = golden<StepDoc>(f)
      expect(unknownCostKeys(s, COST_KEYS)).toEqual([])
      expect(s.cost_known).toBeTypeOf("boolean")
      for (const c of s.children) expect(unknownCostKeys(c, both)).toEqual([])
    }
    expect(golden<StepDoc>("step-1").children.length).toBeGreaterThan(0)
  })
  it("session rows: own block only, no delegated figure", () => {
    for (const s of golden<SessionsPage>("sessions").sessions) {
      expect(unknownCostKeys(s, COST_KEYS)).toEqual([])
      expect(s.cost_known).toBeTypeOf("boolean")
    }
  })
  it("meta.pricing, the settings route and search's body", () => {
    const meta = golden<{ pricing: MetaPricing; capabilities: string[] }>("meta")
    expect(unknownKeys(meta.pricing, META_PRICING_KEYS)).toEqual([])
    expect(meta.capabilities).toContain("pricing")
    const settings = golden<PricingSettings>("settings-pricing")
    expect(unknownKeys(settings, SETTINGS_KEYS)).toEqual([])
    for (const m of settings.models) expect(unknownKeys(m, PRICE_KEYS)).toEqual([])
    expect(unknownKeys(golden<SearchPage>("search"), SEARCH_KEYS)).toEqual([])
  })
})

describe("formatUSD", () => {
  it("four decimals under a cent, two above, never $0.00 for a nonzero figure", () => {
    expect(formatUSD(0.0042)).toBe("$0.0042")
    expect(formatUSD(0.01)).toBe("$0.01")
    expect(formatUSD(1.234)).toBe("$1.23")
    expect(formatUSD(0.00001)).toBe("<$0.0001")
    expect(formatUSD(0)).toBe("$0.00")
  })
})

const goldenRun = (id: string): RunRow => golden<RunsPage>("runs").runs.find((r) => r.id === id)!
/** A finished run on a model no row matches: unpriced. */
const rFail = () => goldenRun("r_fail")
/** A run whose end never arrived: its cost is unknown, not unpriced. */
const rStale = () => goldenRun("r_stale")

const priced = (usd: number, del: number | null = 0): RunRow => ({
  ...golden<RunsPage>("runs").runs[0],
  cost_usd: usd,
  cost_known: true,
  cost_badge: undefined,
  cost_reason: undefined,
  cache: "exact",
  delegated_cost_usd: del,
  delegated_cost_known: del !== null,
  delegated_cost_reason: del === null ? "a subagent run is not priced: c1 (no row matches x/y)" : undefined,
})

describe("costView", () => {
  it("an unpriced figure is — with its reason, never $0.00", () => {
    const r = rFail()
    const v = costView(r)
    expect(v).toMatchObject({ known: false, text: "—", badge: "unpriced" })
    expect(v.title).toBe(r.cost_reason)
    expect(v.title).toContain("no row of the default pricing table matches wefttest/script")
  })
  it("a run whose end never arrived is — too, badged unknown with its reason", () => {
    const r = rStale()
    const v = costView(r)
    expect(v).toMatchObject({ known: false, text: "—", badge: "unknown" })
    expect(v.title).toBe(r.cost_reason)
    expect(v.title).toContain("never arrived")
  })
  it("a priced figure is formatted, the exact USD its title; cache not reported is said", () => {
    expect(costView(priced(0.0042))).toMatchObject({ known: true, text: "$0.0042", title: "$0.004200 USD" })
    expect(costView({ ...priced(0.5), cache: "not_reported" }).title).toContain("no cache splits")
  })
  it("delegated reads its own block", () => {
    expect(delegatedView(priced(1, 0.25)).text).toBe("$0.25")
    expect(delegatedView(priced(1, null))).toMatchObject({ known: false, text: "—", badge: "unpriced" })
  })
  it("a delegated figure unknown for another cause than an unpriced model is badged unknown, the API's reason its title", () => {
    const running = "a subagent run's cost is not known yet: r/0/c1 (running: usage arrives at run end)"
    const v = delegatedView({ ...priced(1, null), delegated_cost_reason: running })
    expect(v).toMatchObject({ known: false, text: "—", badge: "unknown", title: running })
    const unread = "the run's subagent children could not be read, so its own usage cannot be told from theirs"
    expect(delegatedView({ ...priced(1, null), delegated_cost_reason: unread }).badge).toBe("unknown")
    // A mix names an unpriced model: unpriced.
    const mixed = "a subagent run's cost is not known yet: c1 (running: usage arrives at run end); c2 (no row of the default pricing table matches x/y)"
    expect(delegatedView({ ...priced(1, null), delegated_cost_reason: mixed }).badge).toBe("unpriced")
    // A total over it says the same.
    expect(sumCost([{ ...priced(1, null), delegated_cost_reason: running }])).toMatchObject({ known: false, badge: "unknown" })
  })
})

describe("the cost order and totals", () => {
  it("runTotal is own + delegated, unknown when either is", () => {
    expect(runTotal(priced(1, 0.5))).toEqual({ usd: 1.5, known: true })
    expect(runTotal(priced(1, null)).known).toBe(false)
  })
  it("sortByCost: the whole bill, most first; unpriced last in their order", () => {
    const a = { ...priced(1, 0), id: "a" }
    const b = { ...priced(0.2, 2), id: "b" }
    const u1 = { ...rFail(), id: "u1" }
    const u2 = { ...rStale(), id: "u2" }
    expect(sortByCost([u1, a, u2, b]).map((r) => r.id)).toEqual(["b", "a", "u1", "u2"])
  })
  it("sumCost totals a variant's runs and is — when any is unknown or unread", () => {
    expect(sumCost([priced(0.01, 0.005), priced(0.02)])).toMatchObject({ known: true, text: "$0.04" })
    const one = sumCost([priced(0.01), rFail()])
    expect(one).toMatchObject({ known: false, text: "—" })
    expect(one.title).toContain("no row of the default pricing table matches wefttest/script")
    expect(sumCost([priced(0.01), undefined])).toMatchObject({ known: false, text: "—" })
  })
  it("atLeastCost (the panel's header, A5.3): ≥ the known part with the reasons; — when none is known; sumCost when all are", () => {
    expect(atLeastCost([priced(0.01, 0.005), priced(0.02)])).toEqual(sumCost([priced(0.01, 0.005), priced(0.02)]))
    const part = atLeastCost([priced(0.02, 0.01), rFail(), rFail()])
    expect(part).toMatchObject({ known: false, text: "≥ $0.03", usd: 0.03, badge: "unpriced" })
    // Each reason once, sumCost's words for it.
    const why = sumCost([rFail()]).title
    expect(part.title).toBe(`1 of 3 runs priced — ${why}`)
    expect(atLeastCost([rFail()])).toMatchObject({ known: false, text: "—", title: why })
    expect(atLeastCost([])).toMatchObject({ known: false, text: "—", title: "no run to total" })
  })
  it("atLeastCost keeps a run's known own figure when only its delegated part is unknown", () => {
    const running = "a subagent run's cost is not known yet: r/0/c1 (running: usage arrives at run end)"
    const v = atLeastCost([priced(0.004), { ...priced(0.002, null), delegated_cost_reason: running }])
    expect(v).toMatchObject({ known: false, text: "≥ $0.0060", badge: "unknown" })
    expect(v.title).toBe(`1 of 2 runs priced — a run's cost is not known yet: ${running}`)
    // Own known, delegated unknown, nothing else: still ≥ its own, never —.
    expect(atLeastCost([{ ...priced(0.002, null), delegated_cost_reason: running }]).text).toBe("≥ $0.0020")
  })
  it("atLeastCost over a paged list: ≥ even when every loaded row is known, the note its title's last words", () => {
    expect(atLeastCost([priced(0.01), priced(0.02)], "the 2 loaded turns only")).toMatchObject({ known: false, text: "≥ $0.03", title: "the 2 loaded turns only" })
    const why = sumCost([rFail()]).title
    expect(atLeastCost([priced(0.01), rFail()], "loaded only").title).toBe(`1 of 2 runs priced — ${why} · loaded only`)
    expect(atLeastCost([rFail()], "loaded only")).toMatchObject({ text: "—", title: `${why} · loaded only` })
  })
  it("a derived run's no-usage cost is not known — never 'yet': it never arrives", () => {
    const reason = "the stock spans report no (or zero) usage (gen_ai.usage.* absent or 0 on the root and its chat spans): the cost is unknown"
    const derived: RunRow = { ...priced(0), cost_usd: null, cost_known: false, cost_badge: "unknown", cost_reason: reason }
    expect(sumCost([derived]).title).toBe(`a run's cost is not known: ${reason}`)
    expect(sumCost([rStale()]).title).toMatch(/^a run's cost is not known yet: /)
  })
  it("a lost cell is its own reason, and the total is unknown", () => {
    const v = sumCost([priced(0.01), lostCellCost()])
    expect(v).toMatchObject({ known: false, text: "—", badge: "unknown", title: LOST_CELL_REASON })
  })
})

describe("the compare's cost cell (lib/stepdiff.ts's rule and words)", () => {
  const known = (usd: number): CostFields => ({ cost_usd: usd, cost_known: true, cache: "exact" })
  const unpriced: CostFields = { cost_usd: null, cost_known: false, cost_badge: "unpriced", cost_reason: "no row matches x/y" }
  it("same, changed, not comparable, missing", () => {
    expect(costCell(known(0.1), known(0.1))).toBe("same")
    expect(costCell(known(0.1), known(0.2))).toBe("changed")
    expect(costCell(known(0.1), unpriced)).toBe("unknown")
    expect(costCell(unpriced, known(0.1))).toBe("unknown")
    expect(costCell(known(0.1), known(0.1), true)).toBe("missing")
  })
  it("draws the step compare's words: unknown reads not comparable, never same", () => {
    expect(costCellWords("unknown", unpriced).text).toBe(CELL_WORDS.unknown)
    expect(costCellWords("unknown", unpriced).title).toContain("no row matches x/y")
    expect(costCellWords("unknown", unpriced).why).toBe("no row matches x/y")
    expect(costCellWords("same", known(0.1))).toEqual({ text: CELL_WORDS.same, title: CELL_REASONS.same, why: "" })
    expect(costCellWords("missing", null)).toEqual({ text: CELL_WORDS.missing, title: CELL_REASONS.missing, why: "" })
    expect(costCellWords("changed", known(0.2)).text).toBe("$0.20")
  })
})
