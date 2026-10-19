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
import {
  atLeastCost,
  costCell,
  costCellWords,
  costNotRead,
  costView,
  COST_NOT_READ_HERE,
  COST_NOT_READ_YET,
  COST_READ_FAILED,
  delegatedView,
  formatUSD,
  LOST_CELL_REASON,
  lostCellCost,
  runTotal,
  sortByCost,
  costSortNote,
  sumCost,
  unpricedLine,
} from "./cost"
import { CELL_REASONS, CELL_WORDS } from "./stepdiff"
import { derivedBodies } from "../test/derived-fixture"

const API = resolve(process.cwd(), "../testdata/api")
const golden = <T>(name: string): T =>
  JSON.parse(readFileSync(resolve(API, `${name}.golden.json`), "utf8")) as T

const COST_KEYS: Record<keyof CostFields, true> = {
  cost_usd: true,
  cost_known: true,
  cost_badge: true,
  cost_reason: true,
  cache: true,
  unpriced_models: true,
}
const DELEGATED_KEYS: Record<keyof DelegatedCostFields, true> = {
  delegated_cost_usd: true,
  delegated_cost_known: true,
  delegated_unpriced_models: true,
  delegated_cost_reason: true,
}
const META_PRICING_KEYS: Record<keyof MetaPricing, true> = {
  source: true,
  path: true,
  dated: true,
  models: true,
}
const SETTINGS_KEYS: Record<keyof PricingSettings, true> = {
  source: true,
  path: true,
  dated: true,
  note: true,
  models: true,
}
const PRICE_KEYS: Record<keyof ModelPrice, true> = {
  provider: true,
  model_glob: true,
  input: true,
  output: true,
  cached_input: true,
  cache_write: true,
  reasoning: true,
}
const SEARCH_KEYS: Record<keyof SearchPage, true> = {
  total: true,
  runs: true,
  sort: true,
  next_offset: true,
  cost_sort_cap: true,
}

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
    const meta = golden<{ pricing: MetaPricing; capabilities: string[] }>(
      "meta"
    )
    expect(unknownKeys(meta.pricing, META_PRICING_KEYS)).toEqual([])
    expect(meta.capabilities).toContain("pricing")
    const settings = golden<PricingSettings>("settings-pricing")
    expect(unknownKeys(settings, SETTINGS_KEYS)).toEqual([])
    for (const m of settings.models)
      expect(unknownKeys(m, PRICE_KEYS)).toEqual([])
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

const goldenRun = (id: string): RunRow =>
  golden<RunsPage>("runs").runs.find((r) => r.id === id)!
/** A finished run on a model no row matches: unpriced (the fixture's
 * subagent, openai/kimi-k2.7-code). */
const rFail = (): RunRow =>
  golden<RunsPage>("runs-children").runs.find((r) => r.id === "r_sub/0/call_3")!
/** A finished run on wefttest/script: scripted, an exact $0 (U7). */
const rScripted = () => goldenRun("r_fail")
const KIMI = "openai/kimi-k2.7-code has no price"
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
  delegated_cost_reason:
    del === null
      ? "a subagent run is not priced: c1 (no row matches x/y)"
      : undefined,
})

describe("costView", () => {
  it("an unpriced figure is — with its reason, never $0.00", () => {
    const r = rFail()
    const v = costView(r)
    expect(v).toMatchObject({ known: false, text: "—", badge: "unpriced" })
    expect(v.title).toBe(r.cost_reason)
    expect(v.title).toContain(KIMI)
  })
  it("a scripted model's figure is an exact $0 · scripted (U7)", () => {
    expect(costView(rScripted())).toMatchObject({
      known: true,
      text: "$0 · scripted",
      badge: "scripted",
      usd: 0,
    })
    expect(sumCost([rScripted(), rScripted()])).toMatchObject({
      known: true,
      text: "$0 · scripted",
    })
    // Beside a priced run the total is a plain figure.
    expect(sumCost([rScripted(), priced(0.02)]).text).toBe("$0.02")
  })
  it("a scripted parent whose subagents bill is not scripted: its whole bill counts (U5U7-fix 1)", () => {
    const parent: RunRow = {
      ...rScripted(),
      delegated_cost_usd: 0.5,
      delegated_cost_known: true,
      delegated_cost_reason: undefined,
    }
    expect(sumCost([parent])).toMatchObject({ known: true, text: "$0.50" })
    expect(sumCost([parent, rScripted()]).text).toBe("$0.50")
    // A $0 delegated part keeps the scripted words.
    expect(sumCost([{ ...parent, delegated_cost_usd: 0 }]).text).toBe(
      "$0 · scripted"
    )
  })
  it("a run whose end never arrived is — too, badged unknown with its reason", () => {
    const r = rStale()
    const v = costView(r)
    expect(v).toMatchObject({ known: false, text: "—", badge: "unknown" })
    expect(v.title).toBe(r.cost_reason)
    expect(v.title).toContain("never arrived")
  })
  it("a priced figure is formatted, the exact USD its title; cache not reported is said", () => {
    expect(costView(priced(0.0042))).toMatchObject({
      known: true,
      text: "$0.0042",
      title: "$0.004200 USD",
    })
    expect(costView({ ...priced(0.5), cache: "not_reported" }).title).toContain(
      "no cache splits"
    )
  })
  it("delegated reads its own block", () => {
    expect(delegatedView(priced(1, 0.25)).text).toBe("$0.25")
    expect(delegatedView(priced(1, null))).toMatchObject({
      known: false,
      text: "—",
      badge: "unpriced",
    })
  })
  it("a delegated figure unknown for another cause than an unpriced model is badged unknown, the API's reason its title", () => {
    const running =
      "a subagent run's cost is not known yet: r/0/c1 (running: usage arrives at run end)"
    const v = delegatedView({
      ...priced(1, null),
      delegated_cost_reason: running,
    })
    expect(v).toMatchObject({
      known: false,
      text: "—",
      badge: "unknown",
      title: running,
    })
    const unread =
      "the run's subagent children could not be read, so its own usage cannot be told from theirs"
    expect(
      delegatedView({ ...priced(1, null), delegated_cost_reason: unread }).badge
    ).toBe("unknown")
    // A mix: pricing.go's compute leads with notPriced's unknown words
    // whenever any child is unknown, and the badge it carries is
    // unknown — the unpriced child named inside the list does not make
    // the whole unpriced.
    const mixed =
      "a subagent run's cost is not known yet: c1 (running: usage arrives at run end); c2 (no row of the default pricing table matches x/y)"
    expect(
      delegatedView({ ...priced(1, null), delegated_cost_reason: mixed }).badge
    ).toBe("unknown")
    // The same, the unpriced child first (the list's order is the children's).
    const goMixed =
      "a subagent run's cost is not known yet: " +
      [
        "r/0/c1 (no row of the default pricing table matches demo/ops)",
        "r/0/c2 (running: usage arrives at run end)",
      ].join("; ")
    expect(
      delegatedView({ ...priced(1, null), delegated_cost_reason: goMixed })
    ).toMatchObject({ badge: "unknown", title: goMixed })
    // Every child unpriced: notPriced's other lead, unpriced.
    const allUnpriced =
      "a subagent run is not priced: r/0/c1 (no row of the default pricing table matches demo/ops)"
    expect(
      delegatedView({ ...priced(1, null), delegated_cost_reason: allUnpriced })
        .badge
    ).toBe("unpriced")
    // A total over it says the same.
    expect(
      sumCost([{ ...priced(1, null), delegated_cost_reason: running }])
    ).toMatchObject({ known: false, badge: "unknown" })
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
    expect(sortByCost([u1, a, u2, b]).map((r) => r.id)).toEqual([
      "b",
      "a",
      "u1",
      "u2",
    ])
  })
  it("costSortNote counts the rows sorted last, not by cost (UX-A-35)", () => {
    const a = { ...priced(1, 0), id: "a" }
    expect(costSortNote([a])).toBeNull()
    expect(costSortNote([a, rFail()])).toBe(
      "1 run with no known cost: listed last, newest first — not sorted by cost"
    )
    expect(costSortNote([rFail(), rStale(), a], true)).toBe(
      "2 runs with no known cost: listed last, newest first — not sorted by cost · the loaded rows"
    )
  })
  it("sumCost totals a variant's runs and is — when any is unknown or unread", () => {
    expect(sumCost([priced(0.01, 0.005), priced(0.02)])).toMatchObject({
      known: true,
      text: "$0.04",
    })
    const one = sumCost([priced(0.01), rFail()])
    expect(one).toMatchObject({ known: false, text: "—" })
    expect(one.title).toBe(rFail().cost_reason)
    expect(sumCost([priced(0.01), undefined])).toMatchObject({
      known: false,
      text: "—",
    })
  })
  it("atLeastCost (the panel's header, A5.3): ≥ the known part with the reasons; — when none is known; sumCost when all are", () => {
    expect(atLeastCost([priced(0.01, 0.005), priced(0.02)])).toEqual(
      sumCost([priced(0.01, 0.005), priced(0.02)])
    )
    const part = atLeastCost([priced(0.02, 0.01), rFail(), rFail()])
    expect(part).toMatchObject({
      known: false,
      text: "≥ $0.03 · 1 of 3 runs priced",
      usd: 0.03,
      badge: "unpriced",
    })
    // Each reason once, sumCost's words for it, in the title only.
    const why = sumCost([rFail()]).title
    expect(part.title).toBe(`1 of 3 runs priced · ${why}`)
    expect(atLeastCost([rFail()])).toMatchObject({
      known: false,
      text: "—",
      title: `0 of 1 runs priced · ${why}`,
    })
    // U7: a partial $0 (only scripted runs priced) is "—", never "≥ $0.00";
    // "<" never follows "≥".
    expect(atLeastCost([rScripted(), rFail()])).toMatchObject({
      text: "—",
      title: `1 of 2 runs priced · ${why}`,
    })
    // U5U7-fix 3: a known part under $0.0001 is "> $0", never a "≥
    // $0.0001" above the real figure; the exact figure leads the title.
    const tiny = atLeastCost([priced(0.00003), rFail()], undefined, "turn")
    expect(tiny.text).toBe("> $0 · 1 of 2 turns priced")
    expect(tiny.title).toBe(
      `known part $0.000030 USD · 1 of 2 turns priced · ${why}`
    )
    expect(atLeastCost([])).toMatchObject({
      known: false,
      text: "—",
      title: "no run to total",
    })
  })
  it("atLeastCost keeps a run's known own figure when only its delegated part is unknown", () => {
    const running =
      "a subagent run's cost is not known yet: r/0/c1 (running: usage arrives at run end)"
    const v = atLeastCost([
      priced(0.004),
      { ...priced(0.002, null), delegated_cost_reason: running },
    ])
    expect(v).toMatchObject({
      known: false,
      text: "≥ $0.0060 · 1 of 2 runs priced",
      badge: "unknown",
    })
    // The whole bill of one is known: the count says so, beside the delegated part's reason.
    expect(v.title).toBe(`1 of 2 runs priced · delegated: ${running}`)
    // Own known, delegated unknown, nothing else: still ≥ its own, never —.
    expect(
      atLeastCost([{ ...priced(0.002, null), delegated_cost_reason: running }])
    ).toMatchObject({
      text: "≥ $0.0020 · 0 of 1 runs priced",
      title: `0 of 1 runs priced · delegated: ${running}`,
    })
  })
  it("atLeastCost over a paged list: ≥ even when every loaded row is known, the note its title's last words", () => {
    expect(
      atLeastCost([priced(0.01), priced(0.02)], "the 2 loaded turns only")
    ).toMatchObject({
      known: false,
      text: "≥ $0.03",
      title: "the 2 loaded turns only",
    })
    const why = sumCost([rFail()]).title
    expect(atLeastCost([priced(0.01), rFail()], "loaded only").title).toBe(
      `1 of 2 runs priced · ${why} · loaded only`
    )
    expect(atLeastCost([rFail()], "loaded only")).toMatchObject({
      text: "—",
      title: `0 of 1 runs priced · ${why} · loaded only`,
    })
    // U5U7-fix 8e: every loaded row known at $0 — the scripted words when
    // all are scripted, else "$0 · k of n priced"; never "—".
    expect(
      atLeastCost([rScripted(), rScripted()], "loaded only", "turn")
    ).toMatchObject({
      known: false,
      text: "$0 · scripted",
      title: "every run a scripted model's · loaded only",
      badge: "scripted",
      usd: 0,
    })
    expect(
      atLeastCost([rScripted(), priced(0)], "loaded only", "turn")
    ).toMatchObject({
      known: false,
      text: "$0 · 2 of 2 turns priced",
      title: "2 of 2 turns priced · loaded only",
      usd: 0,
    })
  })
  it("a derived run's no-usage cost is not known — never 'yet': it never arrives; keyed on the row's derived mark, not the words", () => {
    // The derived fixture's no-usage row, as Studio serves it.
    const derived = derivedBodies.runNoUsage as RunRow
    expect(derived.derived).toBe(true)
    expect(sumCost([derived]).title).toBe(
      `a run's cost is not known: ${derived.cost_reason}`
    )
    expect(atLeastCost([derived])).toMatchObject({
      text: "—",
      title: `0 of 1 runs priced · a run's cost is not known: ${derived.cost_reason}`,
    })
    // Another wording on a derived row: still no "yet"; the same words on a weft row: "yet".
    expect(
      sumCost([{ ...derived, cost_reason: "reworded by a later Go" }]).title
    ).toBe("a run's cost is not known: reworded by a later Go")
    expect(
      sumCost([{ ...derived, derived: false, requests_badge: undefined }]).title
    ).toMatch(/^a run's cost is not known yet: /)
    expect(sumCost([rStale()]).title).toMatch(
      /^a run's cost is not known yet: /
    )
  })
  it("a lost cell is its own reason, and the total is unknown", () => {
    const v = sumCost([priced(0.01), lostCellCost()])
    expect(v).toMatchObject({
      known: false,
      text: "—",
      badge: "unknown",
      title: LOST_CELL_REASON,
    })
  })
})

describe("the compare's cost cell (lib/stepdiff.ts's rule and words)", () => {
  const known = (usd: number): CostFields => ({
    cost_usd: usd,
    cost_known: true,
    cache: "exact",
  })
  const unpriced: CostFields = {
    cost_usd: null,
    cost_known: false,
    cost_badge: "unpriced",
    cost_reason: "no row matches x/y",
  }
  it("same, changed, not comparable, missing", () => {
    expect(costCell(known(0.1), known(0.1))).toBe("same")
    expect(costCell(known(0.1), known(0.2))).toBe("changed")
    expect(costCell(known(0.1), unpriced)).toBe("unknown")
    expect(costCell(unpriced, known(0.1))).toBe("unknown")
    expect(costCell(known(0.1), known(0.1), true)).toBe("missing")
  })
  it("draws the step compare's words: unknown reads not comparable, never same", () => {
    expect(costCellWords("unknown", unpriced).text).toBe(CELL_WORDS.unknown)
    expect(costCellWords("unknown", unpriced).title).toContain(
      "no row matches x/y"
    )
    expect(costCellWords("unknown", unpriced).why).toBe("no row matches x/y")
    expect(costCellWords("same", known(0.1))).toEqual({
      text: CELL_WORDS.same,
      title: CELL_REASONS.same,
      why: "",
    })
    expect(costCellWords("missing", null)).toEqual({
      text: CELL_WORDS.missing,
      title: CELL_REASONS.missing,
      why: "",
    })
    expect(costCellWords("changed", known(0.2)).text).toBe("$0.20")
  })
})

describe("a page's one unpriced line (U7)", () => {
  it("counts the rows unpriced, own or delegated; the models deduped, two shown, all in the title", () => {
    const sub = golden<RunsPage>("runs").runs.find((r) => r.id === "r_sub")!
    expect(sub.delegated_unpriced_models).toEqual(["openai/kimi-k2.7-code"])
    expect(unpricedLine(golden<RunsPage>("runs").runs)).toEqual({
      text: "1 run unpriced: no row for openai/kimi-k2.7-code",
      title: "no row of the pricing table matches openai/kimi-k2.7-code",
      models: ["openai/kimi-k2.7-code"],
    })
    const m = (x: string): RunRow => ({
      ...rFail(),
      unpriced_models: [x],
    })
    const line = unpricedLine(
      [m("a/x"), m("b/y"), m("a/x"), m("c/z"), rScripted(), rStale()],
      "turn"
    )!
    expect(line.text).toBe("4 turns unpriced: no row for a/x, b/y (+1)")
    expect(line.title).toBe("no row of the pricing table matches a/x, b/y, c/z")
    expect(unpricedLine([rScripted(), priced(1), rStale()])).toBeNull()
    // A paged list with rows not loaded: the count is the loaded rows'
    // (W7-prep).
    expect(unpricedLine([m("a/x")], "run", true)!.text).toBe(
      "1 run unpriced: no row for a/x · the loaded rows"
    )
  })
  it('the API\'s cut list (five models, then "(+n)") is never a model: the line says "and more" (U5U7-fix 8b)', () => {
    const cut: RunRow = {
      ...rFail(),
      unpriced_models: ["a/1", "a/2", "a/3", "a/4", "a/5", "(+2)"],
    }
    const line = unpricedLine([cut])!
    expect(line.models).toEqual(["a/1", "a/2", "a/3", "a/4", "a/5"])
    expect(line.text).toBe("1 run unpriced: no row for a/1, a/2 (+3 and more)")
    expect(line.title).toBe(
      "no row of the pricing table matches a/1, a/2, a/3, a/4, a/5 and more"
    )
  })
  it("delegatedView badges pricing.go's one-line tally words", () => {
    const d = (reason: string) =>
      delegatedView({ ...priced(1, null), delegated_cost_reason: reason }).badge
    expect(d("2 of 3 subagent runs unpriced: no price for x/y")).toBe(
      "unpriced"
    )
    expect(
      d(
        "1 of 3 subagent runs' cost not known yet (r/0/c1: running: usage arrives at run end); 1 unpriced: no price for x/y"
      )
    ).toBe("unknown")
  })
})

describe("the scripted chip (UX-C-42)", () => {
  it("names a scripted run, its zero only when the row billed nothing", async () => {
    const { SCRIPTED_MODEL, isScripted, scriptedChip } = await import("./cost")
    const zero = { input_tokens: 0, output_tokens: 0 }
    expect(scriptedChip({ model: SCRIPTED_MODEL, usage: zero })).toBe(
      "scripted · 0 tokens"
    )
    expect(
      scriptedChip({
        model: SCRIPTED_MODEL,
        usage: { input_tokens: 10, output_tokens: 5 },
      })
    ).toBe("scripted")
    expect(
      scriptedChip({ model: { provider: "openai", name: "gpt" }, usage: zero })
    ).toBeUndefined()
    expect(isScripted({ model: { provider: "weft/runtime", name: "x" } })).toBe(
      false
    )
  })
})

describe("a row not read (U34-fix3)", () => {
  it("'not read yet' only while a read is in flight, else 'not read on this page'", () => {
    // sumCost spells COST_NOT_READ_YET's words out (the panel's bundle).
    expect(sumCost([undefined]).title).toBe(COST_NOT_READ_YET)
    expect(costNotRead(true)).toMatchObject({
      known: false,
      text: "—",
      title: COST_NOT_READ_YET,
    })
    expect(costNotRead(false)).toMatchObject({
      known: false,
      text: "—",
      title: COST_NOT_READ_HERE,
    })
    expect(COST_NOT_READ_HERE).not.toContain("yet")
  })
  it("a failed read is not in flight: its words, never 'yet' (U34-fix4)", () => {
    for (const inFlight of [true, false])
      expect(costNotRead(inFlight, true)).toMatchObject({
        known: false,
        text: "—",
        title: COST_READ_FAILED,
      })
    expect(COST_READ_FAILED).not.toContain("yet")
    expect(COST_READ_FAILED).toContain("failed")
  })
})
