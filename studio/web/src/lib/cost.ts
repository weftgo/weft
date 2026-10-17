// Cost as Studio's pages draw it (plan A5): the figures the API
// prices at read time (studio/pricing.go) — cost_usd, cost_known,
// cost_badge, cost_reason, cache, and a run's delegated_* beside its
// own — read, never re-derived. Plain TypeScript, no React. The rules:
//   - an unknown figure is "—" with its reason, never $0.00;
//   - a run's own and delegated cost are two numbers, never folded,
//     except where a total is asked for (a sort, a variant's sum) —
//     and then one unknown part makes the total unknown;
//   - cache other than "exact" is a hint: the adapter reported no
//     cache splits, so every input token was priced at the input rate;
//   - a scripted model's figure (badge "scripted") is an exact $0 and
//     reads "$0 · scripted"; an unpriced one is "—", its title the model
//     alone, and a page says what is unpriced once over its rows
//     (unpricedLine), never a sentence per row (U7).
// The devtools panel imports it too (A5.3: a turn's cost, the header's
// sum, the result pane's): the same figures and words on both surfaces.
import type { CostFields, DelegatedCostFields, RunRow } from "./api"
import { isDerived } from "./honesty"
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
export function cacheNotReported(
  f: Pick<CostFields, "cache"> | null | undefined
): boolean {
  return !!f?.cache && f.cache !== "exact"
}

/** A figure as drawn: its words, its tooltip, whether it is known. */
export interface CostView {
  known: boolean
  /** formatUSD's words, or "—" when unknown. */
  text: string
  /** The exact figure (known) or why there is none (unknown). */
  title: string
  /** unpriced | unknown when not known; scripted on a known $0. */
  badge?: string
  usd?: number
}

const UNKNOWN_WHY = "this Studio did not price it"

/** A scripted model's figure: an exact $0 nothing bills. */
export const SCRIPTED_TEXT = "$0 · scripted"

/** The scripted engine's own ModelInfo (runtime/scripted.go; pinned
 * there by TestScriptedModelInfo). */
export const SCRIPTED_MODEL = { provider: "weft/runtime", name: "scripted" }

/** isScripted: the run answered as the scripted engine's model. */
export const isScripted = (r: Pick<RunRow, "model">) =>
  r.model.provider === SCRIPTED_MODEL.provider &&
  r.model.name === SCRIPTED_MODEL.name

/** The scripted chip's hint: the attribute it is read from. */
export const SCRIPTED_HINT =
  "model weft/runtime/scripted: the source run's recorded turns, replayed"

/** scriptedChip is a scripted run's chip (UX-C-42), the same words on
 * the panel's turn row, Studio's run header and experiment grid:
 * "scripted · 0 tokens" only when the row's usage is zero — a scripted
 * parent's Subagent child on a real model rolls real usage in. */
export function scriptedChip(
  r: Pick<RunRow, "model" | "usage">
): string | undefined {
  if (!isScripted(r)) return undefined
  return r.usage.input_tokens + r.usage.output_tokens
    ? "scripted"
    : "scripted · 0 tokens"
}

function view(
  usd: number | null | undefined,
  known: boolean | undefined,
  reason: string | undefined,
  badge?: string,
  cache?: string
): CostView {
  if (known && typeof usd === "number") {
    if (badge === "scripted" && !usd)
      return {
        known: true,
        text: SCRIPTED_TEXT,
        title: reason || "",
        badge,
        usd,
      }
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
    title:
      reason ||
      (badge === "unpriced"
        ? "no row of the pricing table matches the model"
        : UNKNOWN_WHY),
    badge: badge || "unknown",
  }
}

/** costView reads an object's own cost block. */
export function costView(f: CostFields | null | undefined): CostView {
  return view(
    f?.cost_usd,
    f?.cost_known,
    f?.cost_reason,
    f?.cost_badge,
    f?.cache
  )
}

/** The reason words studio/pricing.go gives a model no pricing row
 * matches (or a run that names none): what makes a figure "unpriced"
 * rather than "unknown" (tally's "n of m subagent runs unpriced", a
 * Studio before U7's "no row … matches"). */
const UNPRICED_WORDS =
  /unpriced|has no price|\bno row\b.*\bmatches\b|reported no model/

/** The words studio/pricing.go's tally gives a delegated reason when
 * any child's part is unknown ("1 of 3 subagent runs' cost not known
 * yet (…)"; before U7 "…'s cost is not known yet: …"): the whole is
 * unknown then, whatever unpriced child it goes on to count. */
const UNKNOWN_LEAD = /not known yet/

/** delegatedView reads a run's delegated block (its subagents'). The
 * API sends no delegated badge, only delegated_cost_reason: the badge
 * is "unknown" when the reason leads with pricing.go's unknown words —
 * a child still running (usage arrives at its end), one whose end never
 * arrived, children that could not be read — even when the list names
 * an unpriced child too; else "unpriced" when it names an unpriced
 * model, else "unknown". The title is the API's reason either way. */
export function delegatedView(
  f: DelegatedCostFields | null | undefined
): CostView {
  const known = f?.delegated_cost_known
  const reason = f?.delegated_cost_reason
  const badge =
    known === false
      ? reason && (UNKNOWN_LEAD.test(reason) || !UNPRICED_WORDS.test(reason))
        ? "unknown"
        : "unpriced"
      : undefined
  return view(f?.delegated_cost_usd, known, reason, badge)
}

/** The reason a lost matrix cell's cost carries: Studio stopped
 * following its command, so the run it may have started is not read
 * and the variant's total cannot be known. */
export const LOST_CELL_REASON =
  "a lost cell has no cost: Studio stopped following its command, so the run it started is not read"

/** lostCellCost is a lost cell's figure: unknown, its own reason. */
export function lostCellCost(): CostView {
  return view(null, false, LOST_CELL_REASON, "unknown")
}

/** runTotal is a run's whole bill, own + delegated — search's sort
 * key (pricing.go's runCost.total): unknown when either part is. A
 * row without a delegated block (a Studio that sent none) counts own
 * alone only when it carries no delegated fields at all. */
export function runTotal(r: CostFields & DelegatedCostFields): {
  usd: number
  known: boolean
} {
  const own = costView(r)
  if (!own.known) return { usd: 0, known: false }
  if (
    r.delegated_cost_known === undefined &&
    r.delegated_cost_usd === undefined
  )
    return { usd: own.usd ?? 0, known: true }
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

/** costSortNote is the runs list's line under sort=cost (UX-A-35):
 * the rows whose whole bill is not known (unpriced, or running) are
 * listed after every priced one, newest first — not sorted by cost —
 * and the line says how many, "· the loaded rows" when more are
 * paged. null when every row is priced. */
export function costSortNote(
  rows: readonly (CostFields & DelegatedCostFields)[],
  more?: boolean
): string | null {
  const n = rows.filter((r) => !runTotal(r).known).length
  if (!n) return null
  return `${n} ${n === 1 ? "run" : "runs"} with no known cost: listed last, newest first — not sorted by cost${more ? " · the loaded rows" : ""}`
}

/** sumCost totals runs' whole bills (a playground variant's runs):
 * unknown — with the first reason — when any run is missing or
 * unknown; an empty list is unknown too (nothing to total). */
export function sumCost(
  rows: readonly (CostRow | CostView | null | undefined)[]
): CostView {
  if (!rows.length) return view(null, false, "no run to total")
  let usd = 0
  // Every run a scripted model's $0: the total is "$0 · scripted" too.
  let scripted = true
  for (const r of rows) {
    if (!r) return view(null, false, "a run's cost is not read yet")
    if ("text" in r) {
      // An already drawn figure (lostCellCost): known adds, unknown is the total's reason.
      if (!r.known) return view(null, false, r.title, r.badge)
      usd += r.usd ?? 0
      scripted &&= r.badge === "scripted" && !r.usd
      continue
    }
    const t = runTotal(r)
    if (!t.known) return totalUnknown(r)
    usd += t.usd
    // A scripted parent whose subagents bill is not scripted: its whole
    // bill (runTotal, delegated included) must be an exact $0 too —
    // pricing.go's setCost rule (U5U7-fix 1).
    scripted &&= r.cost_badge === "scripted" && t.usd === 0
  }
  return scripted
    ? view(0, true, scriptedTitle(rows) || undefined, "scripted")
    : view(usd, true, undefined)
}

/** scriptedTitle is a scripted sum's title: sumCost's words. */
function scriptedTitle(rows: readonly unknown[]): string {
  return rows.length > 1 ? "every run a scripted model's" : ""
}

/** A row sumCost and atLeastCost read: its cost blocks and, on a run
 * row, its derived mark (lib/honesty.ts's isDerived). */
type CostRow = CostFields &
  DelegatedCostFields & { derived?: boolean; requests_badge?: string }

/** totalUnknown is a run's whole bill when a part is not known: the
 * part's reason, led by what it means for a total. */
function totalUnknown(r: CostRow): CostView {
  const own = costView(r)
  const part = own.known ? delegatedView(r) : own
  const badge = part.badge ?? "unpriced"
  // A derived run's own figure never resolves (its spans carry what usage
  // there is): "not known", never "not known yet" — keyed on the row's
  // derived mark, not on the API's words.
  // An unpriced part's title names its model already: no lead.
  if (badge !== "unknown") return view(null, false, part.title, badge)
  const what = `a run's cost is not known${!own.known && isDerived(r) ? "" : " yet"}`
  return view(null, false, `${what}: ${part.title}`, badge)
}

/** atLeastCost totals runs' whole bills where an unknown run must not
 * hide the known ones (the panel's header over its turns, its replays
 * of a turn — A5.3): every run known → sumCost; some unknown → "≥ $x ·
 * k of n runs priced" (not known; usd the known part — a run's known
 * own figure counts when only its delegated part is unknown; never
 * "<" under "≥"), the reasons in its title only, deduped; a partial
 * figure of $0 (nothing priced, or only scripted $0s) is "—", never
 * "≥ $0.00" (U7); a known part under $0.0001 is "> $0 · …", never a
 * "≥ $0.0001" above the real figure (U5U7-fix 3). more says the rows are
 * not all there is (a paged list): the figure is then "≥" even when
 * every row is known, more its title's last words — and when every
 * loaded row is known at $0, "$0 · scripted" (all scripted) or "$0 · k
 * of n runs priced", never "—" (U5U7-fix 8e). noun is what a row is
 * ("run", the panel's "turn"). */
export function atLeastCost(
  rows: readonly CostRow[],
  more?: string,
  noun = "run"
): CostView {
  let usd = 0
  let k = 0
  const why: string[] = []
  let badge = ""
  let scripted = rows.length > 0
  for (const r of rows) {
    const t = runTotal(r)
    scripted &&= t.known && t.usd === 0 && r.cost_badge === "scripted"
    const own = costView(r)
    if (own.known) usd += t.known ? t.usd : own.usd!
    if (t.known) {
      k++
      continue
    }
    const u = own.known ? delegatedView(r) : totalUnknown(r)
    badge ||= u.badge ?? ""
    const w = own.known ? `delegated: ${u.title}` : u.title
    if (!why.includes(w)) why.push(w)
  }
  if (!why.length && !more) return sumCost(rows)
  // "k of n runs priced": plural always, as pricing.go's tally counts.
  const counted = `${k} of ${rows.length} ${noun}s priced`
  const priced = why.length ? counted : ""
  const title = [priced, ...why, more].filter(Boolean).join(" · ")
  if (!usd && !why.length) {
    // Every loaded row known at $0, more not loaded: the loaded rows'
    // exact words, the title saying what is missing — a partial figure
    // (not known, usd its known part).
    if (scripted)
      return {
        known: false,
        text: SCRIPTED_TEXT,
        title: [scriptedTitle(rows), more].filter(Boolean).join(" · "),
        badge: "scripted",
        usd: 0,
      }
    return {
      known: false,
      text: `$0 · ${counted}`,
      title: [counted, more].filter(Boolean).join(" · "),
      badge: "unknown",
      usd: 0,
    }
  }
  if (!usd) return view(null, false, title, badge || "unknown")
  // Under $0.0001 formatUSD says "<$0.0001": "≥" over it would claim
  // more than is known, so the known part reads "> $0" (exact in the title).
  const tiny = usd < 0.0001
  return {
    known: false,
    text: `${tiny ? "> $0" : `≥ ${formatUSD(usd)}`}${priced ? ` · ${priced}` : ""}`,
    title: tiny ? `known part $${usd.toFixed(6)} USD · ${title}` : title,
    badge: badge || "unknown",
    usd,
  }
}

// ── A page's one unpriced line (U7) ───────────────────────────────

/** A page's unpriced line: its words, the full model list as title,
 * the first model (the pricing link's prefill). */
export interface UnpricedLine {
  text: string
  title: string
  models: string[]
}

/** The API's "(+n)": a model list cut at five (pricing.go's capModels). */
const CUT_MARK = /^\(\+\d+\)$/

/** unpricedLine is the one line a page draws over its rows (the runs
 * list, the sessions list, a run header, the panel's header) when any
 * is unpriced: "N runs unpriced: no row for openai/x, acme/y (+2)" —
 * the models deduped from each row's unpriced_models and
 * delegated_unpriced_models, two shown, every one in the title. A row
 * counts when its own or its delegated figure is unpriced; a row not
 * known yet (running) does not. null when no row is unpriced. noun is
 * what a row is ("run", "turn", "session"). The API lists five models
 * per field, a sixth and more as a last "(+n)" (pricing.go's capModels):
 * such a mark is never a model — the line then says "and more". more
 * says the list is paged and has rows not loaded: the count is the
 * loaded rows' only, said so — "· the loaded rows" (W7-prep). */
export function unpricedLine(
  rows: readonly (CostFields & DelegatedCostFields)[],
  noun = "run",
  more?: boolean
): UnpricedLine | null {
  let n = 0
  let cut = false
  const models: string[] = []
  for (const r of rows) {
    const ms = [
      ...(r.unpriced_models ?? []),
      ...(r.delegated_unpriced_models ?? []),
    ]
    if (
      !ms.length &&
      r.cost_badge !== "unpriced" &&
      delegatedView(r).badge !== "unpriced"
    )
      continue
    n++
    for (const m of ms) {
      if (CUT_MARK.test(m)) cut = true
      else if (!models.includes(m)) models.push(m)
    }
  }
  if (!n) return null
  const extra = models.length > 2 ? `+${models.length - 2}` : ""
  const tail = cut
    ? ` (${extra ? `${extra} ` : ""}and more)`
    : extra
      ? ` (${extra})`
      : ""
  return {
    text: `${n} ${noun}${n > 1 ? "s" : ""} unpriced${models.length ? `: no row for ${models.slice(0, 2).join(", ")}${tail}` : ""}${more ? " · the loaded rows" : ""}`,
    title: models.length
      ? `no row of the pricing table matches ${models.join(", ")}${cut ? " and more" : ""}`
      : "no model reported to price",
    models,
  }
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
  if (state === "changed")
    return {
      text: v.text,
      title: `${CELL_REASONS.changed} · ${v.title}`,
      why: "",
    }
  if (state === "unknown") {
    const why = v.known ? "the base run's is not" : v.title
    return {
      text: CELL_WORDS.unknown,
      title: `a side's cost is not known: ${why}`,
      why,
    }
  }
  return { text: CELL_WORDS[state], title: CELL_REASONS[state], why: "" }
}
