// The runs list's filters (plan H1): the URL's search params read into
// the API's shapes, the chips they draw, and which list answers them.
// Every filter is a URL chip; the list is GET /api/search when a text
// or search-only filter (or a non-default sort) is set and the Studio
// searches (capability "search"), else GET /api/runs — and a filter
// /api/runs cannot apply is applied to the loaded rows, or not at all,
// and its chip says which. Plain values, no React: the page and its
// tests share it.
import type { RunRow, RunsFilters, RunStatus, SearchFilters } from "./api"
import { runTotal, sortByCost } from "./cost"
import type { RunsSearch } from "./links"
import { isDate } from "./settings"

/** The derived statuses a row carries. */
export const RUN_STATUSES: RunStatus[] = [
  "running",
  "succeeded",
  "failed",
  "interrupted",
]
/** The status picks (I3): the derived four and the read-time parked and
 * cancelled (lib/status.ts's words) — every one a server-side filter of
 * /api/runs and /api/search (obsdb.StatusWords: parked an open park —
 * pending calls, no run whose resumed_from names it; cancelled a failed
 * run whose error_type is cancelled), so paging and the total count the
 * filtered set; any other word is the server's 400. */
export const STATUS_PICKS: NonNullable<RunsSearch["status"]>[] = [
  ...RUN_STATUSES,
  "parked",
  "cancelled",
]
export const RUN_SORTS = ["duration", "tokens", "cost"] as const

/** What the Studio can do, from api/meta's capabilities. */
export interface FilterCaps {
  search: boolean
  pricing: boolean
  /** The database aggregates (capability "stats"): the error_class
   * filter and facet. */
  stats?: boolean
}

/** A non-empty string, or a number the router parsed from one. */
function str(v: unknown): string | undefined {
  if (typeof v === "string") return v.trim() || undefined
  if (typeof v === "number" && Number.isFinite(v)) return String(v)
  return undefined
}

/** A non-negative finite number (the router parses digits as one). */
function num(v: unknown): number | undefined {
  const n =
    typeof v === "number"
      ? v
      : typeof v === "string" && v.trim()
        ? Number(v)
        : NaN
  return Number.isFinite(n) && n >= 0 ? n : undefined
}

const DAY = /^\d{4}-\d{2}-\d{2}$/
/** RFC 3339 as Go's time.RFC3339 parses it: a zone is required. */
const TIME =
  /^(\d{4}-\d{2}-\d{2})T(\d{2}):(\d{2}):(\d{2})(\.\d+)?(Z|[+-](\d{2}):(\d{2}))$/

/** parseWhen is a since/until value as the server takes it: a real day
 * (YYYY-MM-DD, round-tripped — never V8's 2026-02-30 → March 2) or an
 * RFC 3339 time with its zone (a zone-less time would be read as local
 * here and refused there), else undefined. */
export function parseWhen(v: unknown): string | undefined {
  const s = str(v)
  if (!s) return undefined
  if (DAY.test(s)) return isDate(s) ? s : undefined
  const m = TIME.exec(s)
  if (!m || !isDate(m[1])) return undefined
  const [h, mi, sec] = [Number(m[2]), Number(m[3]), Number(m[4])]
  if (h > 23 || mi > 59 || sec > 59) return undefined
  if (m[6] !== "Z" && (Number(m[7]) > 23 || Number(m[8]) > 59)) return undefined
  return s
}

/** whenNotes says each since/until the search carries that parseWhen
 * refused: kept in the URL (the router drops a key validation leaves
 * undefined, and the reader's text with it), ignored, never sent — the
 * server would answer 400 and the list land in its error state. */
export function whenNotes(raw: Record<string, unknown>): string[] {
  const out: string[] = []
  for (const k of ["since", "until"] as const) {
    const s = str(raw[k])
    if (s && parseWhen(s) === undefined)
      out.push(
        `${k} “${s}” is ignored: not a real day (YYYY-MM-DD) or an RFC 3339 time with a zone`
      )
  }
  return out
}

/** A tag pair "k=v" with a key. */
function isTag(t: string): boolean {
  return t.indexOf("=") > 0
}

/** validateRunsSearch is /runs' validateSearch: unknown or malformed
 * values are dropped, never guessed at. */
export function validateRunsSearch(
  search: Record<string, unknown>
): RunsSearch {
  const rawTags = Array.isArray(search.tag) ? search.tag : [search.tag]
  const tags = [
    ...new Set(rawTags.map(str).filter((t): t is string => !!t && isTag(t))),
  ]
  const sort = RUN_SORTS.find((s) => s === search.sort)
  const out: RunsSearch = {
    q: str(search.q),
    agent: str(search.agent),
    status: (STATUS_PICKS as unknown[]).includes(search.status)
      ? (search.status as RunsSearch["status"])
      : undefined,
    session: str(search.session),
    public_id: str(search.public_id),
    experiments:
      search.experiments === "on" || search.experiments === "off"
        ? search.experiments
        : undefined,
    tag: withTags(tags),
    tool: str(search.tool),
    model: str(search.model),
    error: str(search.error),
    // Kept as typed; parseWhen decides, where each is used, whether it
    // applies (usable) — a refused one is a not-applied chip and a note.
    since: str(search.since),
    until: str(search.until),
    min_duration: num(search.min_duration),
    min_tokens: num(search.min_tokens),
    min_cost: num(search.min_cost),
    parent: str(search.parent),
    subagents: search.subagents === "all" ? "all" : undefined,
    before: str(search.before),
    sort,
    experiment_id: str(search.experiment_id),
    error_type: str(search.error_type),
    error_class: str(search.error_class),
  }
  if (out.min_tokens !== undefined) out.min_tokens = Math.floor(out.min_tokens)
  return out
}

/** tagsOf is the search's tag pairs, in URL order. */
export function tagsOf(s: Pick<RunsSearch, "tag">): string[] {
  if (!s.tag) return []
  return (Array.isArray(s.tag) ? s.tag : [s.tag]).filter(isTag)
}

/** withTags is tag pairs as the URL carries them: one string, several
 * an array, none absent. */
export function withTags(tags: string[]): string | string[] | undefined {
  return tags.length === 0 ? undefined : tags.length === 1 ? tags[0] : tags
}

/** filtersFromSearch is the search's /api/runs filters (the ones both
 * routes take). */
export function filtersFromSearch(s: RunsSearch): RunsFilters {
  const filters: RunsFilters = {}
  if (s.agent) filters.agent = s.agent
  if (s.status) filters.status = s.status
  if (s.session) filters.session = s.session
  if (s.public_id) filters.public_id = s.public_id
  if (s.experiments) filters.playground = s.experiments === "on"
  // An experiment's runs are playground runs (plan F5): the server
  // filters by experiment_id (F5.1), within the playground runs.
  if (s.experiment_id) {
    filters.experiment_id = s.experiment_id
    if (!s.experiments) filters.playground = true
  }
  if (s.error_type) filters.error_type = s.error_type
  if (s.error_class) filters.error_class = s.error_class
  if (s.before) filters.before = s.before
  if (s.parent) filters.parent = s.parent
  else if (s.subagents === "all") filters.all = true
  const tags = tagsOf(s)
  if (tags.length) {
    filters.tag = {}
    for (const t of tags) {
      const i = t.indexOf("=")
      filters.tag[t.slice(0, i)] = t.slice(i + 1)
    }
  }
  return filters
}

/** rangeTime is a since/until value as the API takes it (RFC 3339): a
 * day is UTC midnight — since's own day, until's next (the day itself
 * included). */
export function rangeTime(v: string, edge: "since" | "until"): string {
  if (!DAY.test(v)) return v
  if (edge === "since") return `${v}T00:00:00Z`
  const d = new Date(`${v}T00:00:00Z`)
  d.setUTCDate(d.getUTCDate() + 1)
  return d.toISOString().replace(".000Z", "Z")
}

/** usable is the search with a since/until the server would refuse
 * dropped: what the lists and the loaded rows are asked. */
export function usable(s: RunsSearch): RunsSearch {
  if (s.since === undefined && s.until === undefined) return s
  return { ...s, since: parseWhen(s.since), until: parseWhen(s.until) }
}

/** The effective sort: cost needs pricing. */
export function effectiveSort(
  s: RunsSearch,
  caps: FilterCaps
): RunsSearch["sort"] {
  return s.sort === "cost" && !caps.pricing ? undefined : s.sort
}

/** The filters only /api/search can apply server-side. */
const SEARCH_ONLY = [
  "q",
  "tool",
  "model",
  "error",
  "since",
  "until",
  "min_tokens",
  "min_cost",
] as const

/** viaSearch: the list is /api/search's — a text or search-only filter
 * or a non-default sort is set, and the Studio searches. */
export function viaSearch(search: RunsSearch, caps: FilterCaps): boolean {
  if (!caps.search) return false
  const s = usable(search)
  if (effectiveSort(s, caps)) return true
  // The failures' error-class facet is search's (UX-C-37): a failed
  // list, or one class of it, reads /api/search where the database
  // aggregates.
  if (caps.stats && (s.status === "failed" || s.error_class)) return true
  return SEARCH_ONLY.some((k) =>
    k !== "min_cost"
      ? s[k] !== undefined
      : caps.pricing && s.min_cost !== undefined
  )
}

/** searchFiltersFrom is the search's /api/search filters (no time
 * cursor: a search pages by offset). */
export function searchFiltersFrom(
  search: RunsSearch,
  caps: FilterCaps
): SearchFilters {
  const s = usable(search)
  const { before: _b, before_id: _bi, ...base } = filtersFromSearch(s)
  const out: SearchFilters = { ...base }
  if (!caps.stats) delete out.error_class
  if (s.q) out.q = s.q
  if (s.tool) out.tool = s.tool
  if (s.model) out.model = s.model
  if (s.error) out.error = s.error
  if (s.since) out.since = rangeTime(s.since, "since")
  if (s.until) out.until = rangeTime(s.until, "until")
  if (s.min_tokens !== undefined) out.min_tokens = s.min_tokens
  if (caps.pricing && s.min_cost !== undefined) out.min_cost = s.min_cost
  const sort = effectiveSort(s, caps)
  if (sort) out.sort = sort
  return out
}

/** A run's duration in ms: finished − started, or elapsed so far. */
export function runDurationMs(
  r: Pick<RunRow, "started" | "finished">,
  now = Date.now()
): number {
  const end = r.finished ? Date.parse(r.finished) : now
  return Math.max(0, end - Date.parse(r.started))
}

const tokensOf = (r: RunRow) => r.usage.input_tokens + r.usage.output_tokens

/** localKeys is the filters the loaded rows answer here: duration
 * always (/api/runs and /api/search have none), and search's own when
 * the Studio cannot search (text and tool cannot be: a row carries
 * neither). experiment_id is the API's (F5.1). */
export function localKeys(
  search: RunsSearch,
  caps: FilterCaps
): (keyof RunsSearch)[] {
  const s = usable(search)
  const keys: (keyof RunsSearch)[] = []
  if (s.min_duration !== undefined) keys.push("min_duration")
  if (!caps.search) {
    for (const k of ["model", "error", "since", "until", "min_tokens"] as const)
      if (s[k] !== undefined) keys.push(k)
    if (caps.pricing && s.min_cost !== undefined) keys.push("min_cost")
  }
  return keys
}

/** applyLocal filters loaded rows by localKeys, and — when the list is
 * not search's — sorts them by the search's sort (the loaded rows
 * only). */
export function applyLocal(
  runs: RunRow[],
  search: RunsSearch,
  caps: FilterCaps,
  now = Date.now()
): RunRow[] {
  const s = usable(search)
  const keys = new Set(localKeys(s, caps))
  let out = runs
  if (keys.size) {
    const since =
      keys.has("since") && s.since
        ? Date.parse(rangeTime(s.since, "since"))
        : undefined
    const until =
      keys.has("until") && s.until
        ? Date.parse(rangeTime(s.until, "until"))
        : undefined
    const err = s.error?.toLowerCase()
    out = runs.filter((r) => {
      if (
        keys.has("min_duration") &&
        runDurationMs(r, now) < (s.min_duration ?? 0) * 1000
      )
        return false
      if (keys.has("model") && r.model.name !== s.model) return false
      if (keys.has("error") && !r.err.toLowerCase().includes(err ?? ""))
        return false
      if (since !== undefined && Date.parse(r.started) < since) return false
      if (until !== undefined && Date.parse(r.started) >= until) return false
      if (keys.has("min_tokens") && tokensOf(r) < (s.min_tokens ?? 0))
        return false
      if (keys.has("min_cost")) {
        const t = runTotal(r)
        if (!t.known || t.usd < (s.min_cost ?? 0)) return false
      }
      return true
    })
  }
  if (viaSearch(s, caps)) return out
  switch (effectiveSort(s, caps)) {
    case "cost":
      return sortByCost(out)
    case "duration":
      return out
        .map((r, i) => ({ r, i, d: runDurationMs(r, now) }))
        .sort((a, b) => b.d - a.d || a.i - b.i)
        .map((x) => x.r)
    case "tokens":
      return out
        .map((r, i) => ({ r, i, t: tokensOf(r) }))
        .sort((a, b) => b.t - a.t || a.i - b.i)
        .map((x) => x.r)
    default:
      return out
  }
}

/** One URL chip: the key it clears, the tag pair for a tag, its words,
 * whether the list applies it, and a note when it is applied to the
 * loaded rows only or not at all. */
export interface RunChip {
  key: keyof RunsSearch
  /** A tag chip's pair (removing it keeps the others). */
  value?: string
  label: string
  applied: boolean
  note?: string
}

const ROWS_ONLY = "the loaded rows only"
const NEEDS_SEARCH = "needs the search capability"
const NEEDS_PRICING = "needs the pricing capability"
const NEEDS_STATS = "needs the stats capability"
const BAD_WHEN = "not a real day (YYYY-MM-DD) or an RFC 3339 time with a zone"

/** runChips is every set filter as a chip, in the toolbar's order. */
export function runChips(s: RunsSearch, caps: FilterCaps): RunChip[] {
  const local = new Set(localKeys(s, caps))
  const chips: RunChip[] = []
  const push = (
    key: keyof RunsSearch,
    label: string,
    extra: Partial<RunChip> = {}
  ) =>
    chips.push({
      key,
      label,
      applied: true,
      ...(local.has(key) ? { note: ROWS_ONLY } : {}),
      ...extra,
    })
  if (s.q)
    push(
      "q",
      `“${s.q}”`,
      caps.search ? {} : { applied: false, note: NEEDS_SEARCH }
    )
  if (s.agent) push("agent", `agent ${s.agent}`)
  if (s.status) push("status", `status ${s.status}`)
  if (s.tool)
    push(
      "tool",
      `tool ${s.tool}`,
      caps.search ? {} : { applied: false, note: NEEDS_SEARCH }
    )
  if (s.model) push("model", `model ${s.model}`)
  if (s.error) push("error", `error ~ ${s.error}`)
  if (s.error_type) push("error_type", `kind ${s.error_type}`)
  if (s.error_class)
    push(
      "error_class",
      `error class “${s.error_class}”`,
      caps.stats ? {} : { applied: false, note: NEEDS_STATS }
    )
  for (const k of ["since", "until"] as const) {
    const v = s[k]
    if (v)
      push(
        k,
        `${k} ${v}`,
        parseWhen(v) === undefined ? { applied: false, note: BAD_WHEN } : {}
      )
  }
  if (s.min_duration !== undefined)
    push("min_duration", `duration ≥ ${s.min_duration}s`)
  if (s.min_tokens !== undefined)
    push("min_tokens", `tokens ≥ ${s.min_tokens.toLocaleString("en-US")}`)
  if (s.min_cost !== undefined)
    push(
      "min_cost",
      `cost ≥ $${s.min_cost}`,
      caps.pricing ? {} : { applied: false, note: NEEDS_PRICING }
    )
  if (s.session) push("session", `session ${s.session}`)
  if (s.public_id) push("public_id", `public id ${s.public_id}`)
  if (s.experiments)
    push(
      "experiments",
      s.experiments === "on" ? "experiments only" : "experiments off"
    )
  if (s.experiment_id) push("experiment_id", `experiment ${s.experiment_id}`)
  for (const t of tagsOf(s)) push("tag", `tag ${t}`, { value: t })
  if (s.parent) push("parent", `children of ${s.parent}`)
  return chips
}

/** removeChip is the search patch that clears one chip (a tag chip
 * keeps the other pairs). */
export function removeChip(s: RunsSearch, chip: RunChip): Partial<RunsSearch> {
  if (chip.key === "tag" && chip.value !== undefined)
    return { tag: withTags(tagsOf(s).filter((t) => t !== chip.value)) }
  return { [chip.key]: undefined }
}

/** The filters behind the toolbar's "+ filter" fold (H9, UX-A-10): every
 * one but the search box, agent and status, which stay on the bar. A
 * set one is its chip, so the fold stays closed. */
export const MORE_FILTER_KEYS = [
  "experiments",
  "tool",
  "model",
  "error",
  "since",
  "until",
  "min_duration",
  "min_tokens",
  "min_cost",
  "tag",
  "session",
  "public_id",
] as const satisfies readonly (keyof RunsSearch)[]

/** moreFilterCount is how many of the folded filters are set (min_cost
 * only where the cost column is drawn). */
export function moreFilterCount(s: RunsSearch, costCapable: boolean): number {
  let n = 0
  for (const k of MORE_FILTER_KEYS) {
    if (k === "min_cost" && !costCapable) continue
    if (k === "tag") n += tagsOf(s).length
    else if (s[k] !== undefined) n++
  }
  return n
}

/** The fold's button: "+ filter", and how many it holds once set. */
export function moreFilterLabel(n: number): string {
  return n > 0 ? `+ filter · ${n} set` : "+ filter"
}

/** The fold's title. */
export const FILTERS_HINT =
  "more filters: experiments, tool, model, error, since, until, duration, tokens, cost, tag, session, public id — each set one is a chip below"

/** CLEARED is the patch that clears every chip (the sort and the
 * subagents toggle are views, not filters: kept). */
export const CLEARED: Partial<RunsSearch> = {
  q: undefined,
  agent: undefined,
  status: undefined,
  tool: undefined,
  model: undefined,
  error: undefined,
  since: undefined,
  until: undefined,
  min_duration: undefined,
  min_tokens: undefined,
  min_cost: undefined,
  session: undefined,
  public_id: undefined,
  experiments: undefined,
  tag: undefined,
  parent: undefined,
  experiment_id: undefined,
  error_type: undefined,
  error_class: undefined,
}

/** dedupeTraces keeps each trace's first row, in page order: a trace
 * whose earlier span arrives between page reads is listed again on a
 * later page (studio/traces.go). */
export function dedupeTraces<T extends { trace_id: string }>(rows: T[]): T[] {
  const seen = new Set<string>()
  return rows.filter((t) =>
    seen.has(t.trace_id) ? false : (seen.add(t.trace_id), true)
  )
}
