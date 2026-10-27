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
import { plural, tokenPair } from "./format"
import type { CostFields, DelegatedCostFields, RunRow } from "./api"
import { isDerived } from "./honesty"
import { CELL_REASONS, CELL_WORDS } from "./stepdiff"
import type { CellState } from "./stepdiff"

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

/** The hint a figure carries when the adapter reported no cache
 * splits (cache "not_reported"; "unknown" before the rename). */
export const CACHE_HINT =
  "the adapter reported no cache splits: every input token was priced at the input rate, which may overstate a cached prompt"

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

/** The summarizer's figure (UX-B-13): thread's compaction and branch
 * summaries are model calls of their own, never in a turn's usage —
 * drawn beside a turn's and the session's cost as "+ summarizer $x"
 * (studio/timeline.go prices it: each call at its summarizer model's
 * rates when recorded, else the turn's). The title names its tokens and
 * the figure's own title (its reason when it is not known). */
export function summarizerView(
  f:
    | (CostFields & { usage: { input_tokens: number; output_tokens: number } })
    | null
    | undefined
): CostView | null {
  if (!f) return null
  const v = costView(f)
  const toks = tokenPair(f.usage.input_tokens, f.usage.output_tokens, true)
  // An estimate (cost_badge "estimate"): known, priced at a stand-in
  // model's rates — "≈", its reason in the title.
  const est = v.known && f.cost_badge === ESTIMATE_BADGE
  return {
    ...v,
    badge: est ? ESTIMATE_BADGE : v.badge,
    text: `+ summarizer ${est ? ESTIMATE_MARK : ""}${v.text}`,
    title: [
      `the summarizer's model calls (compaction, branch summaries): ${toks} tokens, never in the turns' usage`,
      est ? `${ESTIMATE_WORD}: ${f.cost_reason ?? ""}` : "",
      v.title,
    ]
      .filter(Boolean)
      .join(" · "),
  }
}

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

/** The bill's two parts in words (U21.1, UX-F-15, UX-F-16): "$0.0012
 * own · $0.0040 delegated" — the figure, then its word — on the
 * panel's turn row and Studio's run header alike; delegated is a cost
 * and usage word only (lib/glossary.ts). */
export const COST_PARTS = { own: "own", delegated: "delegated", sep: " · " }

/** costLine is the two parts as one line: "$x own · $y delegated". */
export const costLine = (own: string, delegated: string) =>
  `${own} ${COST_PARTS.own}${COST_PARTS.sep}${delegated} ${COST_PARTS.delegated}`

/** A whole bill's hover (a replayed run's result, both surfaces). */
export const BILL_TITLE =
  "the run's whole bill: own + delegated (its model calls and its child runs')"

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

/** An estimate's badge, mark and word (studio/pricing.go's
 * costEstimate): a known figure priced at a stand-in model's rates — a
 * summarizer call whose own model was not recorded, at its turn's. */
export const ESTIMATE_BADGE = "estimate"
export const ESTIMATE_MARK = "≈"
export const ESTIMATE_WORD = "an estimate"

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

/** The reason words studio/pricing.go gives a model no pricing row
 * matches (or a run that names none): what makes a figure "unpriced"
 * rather than "unknown" (tally's "n of m child runs unpriced", a
 * Studio before U7's "no row … matches"). */
const UNPRICED_WORDS =
  /unpriced|has no price|\bno row\b.*\bmatches\b|reported no model/

/** The words studio/pricing.go's tally gives a delegated reason when
 * any child's part is unknown ("1 of 3 child runs' cost not known
 * yet (…)"; before U7 "…'s cost is not known yet: …"): the whole is
 * unknown then, whatever unpriced child it goes on to count. */
const UNKNOWN_LEAD = /not known yet/

/** scriptedTitle is a scripted sum's title: sumCost's words. */
function scriptedTitle(rows: readonly unknown[]): string {
  return rows.length > 1 ? "every run a scripted model's" : ""
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
  return `${plural(n, "run")} with no known cost: listed last, newest first — not sorted by cost${more ? " · the loaded rows" : ""}`
}

/** A run's row is being read (a document in flight): its cost comes. */
export const COST_NOT_READ_YET = "a run's cost is not read yet"
/** A run's row this page never reads (U34-fix3): a panel token's hidden
 * child, a grandchild at the agents level under no expanded child —
 * "yet" would promise a read that never comes. */
export const COST_NOT_READ_HERE =
  "a run's cost is not read on this page — open the run for its own"

/** A run's row whose read failed (U34-fix4): no read is in flight, so
 * never "yet" — the failure, and where the figure still is. */
export const COST_READ_FAILED =
  "a run's cost is not read: its document's read failed — open the run for its own"

/** costNotRead is the "—" a missing row draws: COST_NOT_READ_YET while
 * a read that would carry it is in flight, COST_READ_FAILED when that
 * read failed (not in flight, never "yet"), else COST_NOT_READ_HERE. */
export function costNotRead(inFlight: boolean, failed = false): CostView {
  return view(
    null,
    false,
    failed
      ? COST_READ_FAILED
      : inFlight
        ? COST_NOT_READ_YET
        : COST_NOT_READ_HERE
  )
}

/** sumCost totals runs' whole bills (a playground variant's runs):
 * unknown — with the first reason — when any run is missing or
 * unknown; an empty list is unknown too (nothing to total). */
export function sumCost(
  rows: readonly (CostRow | CostView | null | undefined)[]
): CostView {
  return total(rows, (r) => {
    // COST_NOT_READ_YET's words, spelt out: a shared const would be
    // hoisted into the panel's bundle (cost.test.ts holds them equal).
    if (!r) return view(null, false, "a run's cost is not read yet")
    // An already drawn figure (lostCellCost): known adds, unknown is the total's reason.
    if ("text" in r)
      return r.known
        ? [r.usd ?? 0, r.badge === "scripted" && !r.usd]
        : view(null, false, r.title, r.badge)
    return runPart(r)
  })
}

/** sumRuns is sumCost over run rows alone — every one read, none a
 * figure already drawn: atLeastCost's total and the devtools panel's,
 * whose bundle so carries no other kind of row. */
export const sumRuns = (rows: readonly CostRow[]): CostView =>
  total(rows, runPart)

/** One row's part of a total: its known figure and whether it is a
 * scripted model's exact $0, or the unknown view that is the total. */
type Part = [usd: number, scripted: boolean] | CostView

/** runPart is a run's whole bill as a total's part. A scripted parent
 * whose subagents bill is not scripted: its whole bill (runTotal,
 * delegated included) must be an exact $0 too — pricing.go's setCost
 * rule (U5U7-fix 1). */
function runPart(r: CostRow): Part {
  const t = runTotal(r)
  return t.known
    ? [t.usd, r.cost_badge === "scripted" && t.usd === 0]
    : totalUnknown(r)
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

/** cacheNotReported: the figure was priced without cache splits. */
export function cacheNotReported(
  f: Pick<CostFields, "cache"> | null | undefined
): boolean {
  return !!f?.cache && f.cache !== "exact"
}

/** A row sumCost and atLeastCost read: its cost blocks and, on a run
 * row, its derived mark (lib/honesty.ts's isDerived). */
type CostRow = CostFields &
  DelegatedCostFields & { derived?: boolean; requests_badge?: string }

/** total adds the rows' parts, in order: the first unknown part is the
 * total; every run a scripted model's $0 is "$0 · scripted" too. */
function total<TRow>(rows: readonly TRow[], part: (r: TRow) => Part): CostView {
  if (!rows.length) return view(null, false, "no run to total")
  let usd = 0
  let scripted = true
  for (const r of rows) {
    const p = part(r)
    if (!Array.isArray(p)) return p
    usd += p[0]
    scripted &&= p[1]
  }
  return scripted
    ? view(0, true, scriptedTitle(rows) || undefined, "scripted")
    : view(usd, true, undefined)
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
  if (!why.length && !more) return sumRuns(rows)
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
