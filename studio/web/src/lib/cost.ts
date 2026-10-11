// Cost as Studio's pages draw it (plan A5): the figures the API
// prices at read time (studio/pricing.go) — cost_usd, cost_known,
// cost_badge, cost_reason, cache, and a run's delegated_* beside its
// own — read, never re-derived. Plain TypeScript, no React. The rules:
//   - an unknown figure is "—" with its reason, never $0.00;
//   - a run's own and delegated cost are two numbers, never folded,
//     except where a total is asked for (a sort, a variant's sum) —
//     and then one unknown part makes the total unknown;
//   - cache other than "exact" is a hint: the adapter reported no
//     cache splits, so every input token was priced at the input rate.
// The devtools panel imports it too (A5.3: a turn's cost, the header's
// sum, the result pane's): the same figures and words on both surfaces.
import type { CostFields, DelegatedCostFields, RunRow } from "./api"
import { CELL_REASONS, CELL_WORDS } from "./stepdiff"
import type { CellState } from "./stepdiff"

/** "$1.23"; under a cent four decimals ("$0.0042"), under that
 * "<$0.0001"; a known zero "$0.00". Only ever called on a known
 * figure — an unknown one is "—" (costView). */
export function formatUSD(v: number): string {
  if (!Number.isFinite(v)) return "—"
  if (v === 0) return "$0.00"
  const abs = Math.abs(v)
  const sign = v < 0 ? "-" : ""
  if (abs < 0.0001) return `${sign}<$0.0001`
  if (abs < 0.01) return `${sign}$${abs.toFixed(4)}`
  return `${sign}$${abs.toFixed(2)}`
}

/** The hint a figure carries when the adapter reported no cache
 * splits (cache "not_reported"; "unknown" before the rename). */
export const CACHE_HINT =
  "the adapter reported no cache splits: every input token was priced at the input rate, which may overstate a cached prompt"

/** cacheNotReported: the figure was priced without cache splits. */
export function cacheNotReported(f: Pick<CostFields, "cache"> | null | undefined): boolean {
  return !!f?.cache && f.cache !== "exact"
}

/** A figure as drawn: its words, its tooltip, whether it is known. */
export interface CostView {
  known: boolean
  /** formatUSD's words, or "—" when unknown. */
  text: string
  /** The exact figure (known) or why there is none (unknown). */
  title: string
  /** unpriced | unknown when not known. */
  badge?: string
  usd?: number
}

const UNKNOWN_WHY = "this Studio did not price it"

function view(usd: number | null | undefined, known: boolean | undefined, reason: string | undefined, badge?: string, cache?: string): CostView {
  if (known && typeof usd === "number") {
    const exact = `$${usd.toFixed(6)} USD`
    return {
      known: true,
      text: formatUSD(usd),
      title: cache && cache !== "exact" ? `${exact} · ${CACHE_HINT}` : exact,
      usd,
    }
  }
  return {
    known: false,
    text: "—",
    title: reason || (badge === "unpriced" ? "no row of the pricing table matches the model" : UNKNOWN_WHY),
    badge: badge || "unknown",
  }
}

/** costView reads an object's own cost block. */
export function costView(f: CostFields | null | undefined): CostView {
  return view(f?.cost_usd, f?.cost_known, f?.cost_reason, f?.cost_badge, f?.cache)
}

/** The reason words studio/pricing.go gives a model no pricing row
 * matches (or a run that names none): what makes a figure "unpriced"
 * rather than "unknown". */
const UNPRICED_WORDS = /\bno row\b.*\bmatches\b|reported no model to price/

/** delegatedView reads a run's delegated block (its subagents'). The
 * API sends no delegated badge, only delegated_cost_reason: the badge
 * is "unpriced" when that reason names an unpriced model, else
 * "unknown" — a child still running (usage arrives at its end), one
 * whose end never arrived, children that could not be read — and the
 * title is the API's reason either way. */
export function delegatedView(f: DelegatedCostFields | null | undefined): CostView {
  const known = f?.delegated_cost_known
  const reason = f?.delegated_cost_reason
  const badge = known === false ? (reason && !UNPRICED_WORDS.test(reason) ? "unknown" : "unpriced") : undefined
  return view(f?.delegated_cost_usd, known, reason, badge)
}

/** The reason a lost matrix cell's cost carries: Studio stopped
 * following its command, so the run it may have started is not read
 * and the variant's total cannot be known. */
export const LOST_CELL_REASON = "a lost cell has no cost: Studio stopped following its command, so the run it started is not read"

/** lostCellCost is a lost cell's figure: unknown, its own reason. */
export function lostCellCost(): CostView {
  return view(null, false, LOST_CELL_REASON, "unknown")
}

/** runTotal is a run's whole bill, own + delegated — search's sort
 * key (pricing.go's runCost.total): unknown when either part is. A
 * row without a delegated block (a Studio that sent none) counts own
 * alone only when it carries no delegated fields at all. */
export function runTotal(r: CostFields & DelegatedCostFields): { usd: number; known: boolean } {
  const own = costView(r)
  if (!own.known) return { usd: 0, known: false }
  if (r.delegated_cost_known === undefined && r.delegated_cost_usd === undefined) return { usd: own.usd ?? 0, known: true }
  const del = delegatedView(r)
  if (!del.known) return { usd: 0, known: false }
  return { usd: (own.usd ?? 0) + (del.usd ?? 0), known: true }
}

/** sortByCost orders runs as /api/search?sort=cost does: the whole
 * bill, most first; unpriced runs after every priced one, in their
 * given order (stable). */
export function sortByCost<T extends RunRow>(runs: readonly T[]): T[] {
  return runs
    .map((r, i) => ({ r, i, t: runTotal(r) }))
    .sort((a, b) => {
      if (a.t.known !== b.t.known) return a.t.known ? -1 : 1
      if (a.t.known && a.t.usd !== b.t.usd) return b.t.usd - a.t.usd
      return a.i - b.i
    })
    .map((x) => x.r)
}

/** sumCost totals runs' whole bills (a playground variant's runs):
 * unknown — with the first reason — when any run is missing or
 * unknown; an empty list is unknown too (nothing to total). */
export function sumCost(rows: readonly ((CostFields & DelegatedCostFields) | CostView | null | undefined)[]): CostView {
  if (!rows.length) return view(null, false, "no run to total")
  let usd = 0
  for (const r of rows) {
    if (!r) return view(null, false, "a run's cost is not read yet")
    if ("text" in r) {
      // An already drawn figure (lostCellCost): known adds, unknown is the total's reason.
      if (!r.known) return view(null, false, r.title, r.badge)
      usd += r.usd ?? 0
      continue
    }
    const t = runTotal(r)
    if (!t.known) return totalUnknown(r)
    usd += t.usd
  }
  return view(usd, true, undefined)
}

/** totalUnknown is a run's whole bill when a part is not known: the
 * part's reason, led by what it means for a total. */
function totalUnknown(r: CostFields & DelegatedCostFields): CostView {
  const own = costView(r)
  const part = own.known ? delegatedView(r) : own
  const badge = part.badge ?? "unpriced"
  const what = badge === "unknown" ? "a run's cost is not known yet" : "a run is not priced"
  return view(null, false, `${what}: ${part.title}`, badge)
}

/** atLeastCost totals runs' whole bills where an unknown run must not
 * hide the known ones (the panel's header over its turns, A5.3): every
 * run known → sumCost; some unknown → "≥ $x" (not known; usd the known
 * part), how many runs are priced and each unknown run's reason
 * (sumCost's words) as its title — never a figure claimed whole; none
 * known → "—" with the reasons. */
export function atLeastCost(rows: readonly (CostFields & DelegatedCostFields)[]): CostView {
  let usd = 0
  let n = 0
  const why: string[] = []
  let badge = ""
  for (const r of rows) {
    const t = runTotal(r)
    if (t.known) {
      usd += t.usd
      n++
      continue
    }
    const u = totalUnknown(r)
    badge ||= u.badge ?? ""
    if (!why.includes(u.title)) why.push(u.title)
  }
  if (!why.length) return sumCost(rows)
  const reasons = why.join("; ")
  if (!n) return view(null, false, reasons, badge)
  return { known: false, text: `≥ ${formatUSD(usd)}`, title: `${n} of ${rows.length} runs priced — ${reasons}`, badge, usd }
}

// ── The compare table's cost column ───────────────────────────────

/** costCell compares one step's cost on two sides by the step
 * compare's own rule (lib/stepdiff.ts): a side lacking the step is
 * "missing"; an unknown figure on either side is "not comparable" —
 * never "same"; else same or changed. It never marks a step changed:
 * cost follows usage, already a compared column. */
export function costCell(
  a: CostFields | null | undefined,
  b: CostFields | null | undefined,
  missing = false
): CellState {
  if (missing) return "missing"
  const x = costView(a)
  const y = costView(b)
  if (!x.known || !y.known) return "unknown"
  return x.usd === y.usd ? "same" : "changed"
}

/** The words and tooltip a compared cost cell carries: the step
 * compare's (CELL_WORDS, CELL_REASONS), the figure where it changed,
 * the side's reason where it is unknown. */
export function costCellWords(
  state: CellState,
  side: CostFields | null | undefined
): { text: string; title: string; why: string } {
  const v = costView(side)
  if (state === "changed") return { text: v.text, title: `${CELL_REASONS.changed} · ${v.title}`, why: "" }
  if (state === "unknown") {
    const why = v.known ? "the base run's is not" : v.title
    return { text: CELL_WORDS.unknown, title: `a side's cost is not known: ${why}`, why }
  }
  return { text: CELL_WORDS[state], title: CELL_REASONS[state], why: "" }
}
