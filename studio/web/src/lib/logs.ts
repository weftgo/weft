// A run's app logs on the run page (U11.2, audit §6.9; UX-E-06,
// UX-A-07, UX-S2-03): the lines GET /api/runs/{id}/logs attributes to
// the run through its spans, read page by page (1,000 a page, the
// route's max) up to LOG_PAGES pages — a cut list carries a truncated
// badge, never a silent end —, their severity's tone, the WARN / ERROR
// ticks a run tree row carries (logTicks: a line ticks the deepest row
// its span, or the nearest ancestor span a row draws, is behind) and
// the "jump to span" selection (jumpSel: t:<span id>, which
// lib/run-tree.ts's resolveRowSel reads back, an ancestor's row when
// no row draws the span itself). Studio only: the panel hands the step
// off to Studio's drawer Logs tab (lib/links.ts's logsLink).
import { plural } from "./format"
import { queryOptions } from "@tanstack/react-query"

import { fetchLogs } from "./api"
import type { LogRow, LogsPage, StepHole } from "./api"

/** The route's largest page. */
export const LOG_PAGE = 1000

/** The pages the run page reads before it stops (10,000 lines, the
 * reader's own candidate cap): a run with more is cut, badged. */
export const LOG_PAGES = 10

/** The severity filter's levels (the route's severity=, the lowest
 * kept): "" is every line. */
export const LOG_LEVELS = ["", "info", "warn", "error"] as const
export type LogLevel = (typeof LOG_LEVELS)[number]

/** A ?lsev= value the filter knows, else undefined (every line). */
export function logLevelOf(v: unknown): Exclude<LogLevel, ""> | undefined {
  return v === "info" || v === "warn" || v === "error" ? v : undefined
}

/** The run page's words for the logs (one place, both views). */
export const LOGS = {
  view: "logs",
  level: "severity",
  levelWord: (l: LogLevel) => (l === "" ? "all" : `${l}+`),
  levelHint: (l: LogLevel) =>
    l === "" ? "every line" : `${l.toUpperCase()} and above only`,
  jump: "jump to span",
  jumpHint:
    "select the run tree row this line was logged under (its drawer's Logs tab)",
  noSpan: "no span",
  none: "no app log line in this run",
  /** A running run's row with no line yet (its spans export when they
   * end): never "no line". */
  pending:
    "this row's spans are not reported yet (spans export when they end): its lines appear once they are",
  noneAtLevel: (l: LogLevel) =>
    `no app log line at ${l.toUpperCase()} or above in this run`,
  count: (n: number) => plural(n, "line"),
  cutReason: (n: number) =>
    `the first ${n.toLocaleString("en-US")} lines are shown (${LOG_PAGES} pages of ${LOG_PAGE.toLocaleString("en-US")}): later lines of this run were not read here, nothing was lost`,
  cutFix:
    "filter by severity, or page the route: GET /api/runs/{id}/logs?from=<next_from>",
  /** A row's tick: "E1 W2" (the problem badges' letters); "≥E1 W2"
   * when the warn read was cut — counts that are floors. */
  tick: (t: { warn: number; error: number }, cut = false) =>
    (cut ? "≥" : "") +
    [t.error ? `E${t.error}` : "", t.warn ? `W${t.warn}` : ""]
      .filter(Boolean)
      .join(" "),
  tickHint: (warn: number, err: number, cut = false) =>
    (cut ? "at least " : "") +
    [
      err ? plural(err, "app log error") : "",
      warn ? plural(warn, "app log warning") : "",
    ]
      .filter(Boolean)
      .join(", ") +
    " under this row (its Logs tab)" +
    (cut
      ? `: the first ${(LOG_PAGES * LOG_PAGE).toLocaleString("en-US")} WARN+ lines were read`
      : ""),
} as const

/** A severity number's tone class: ERROR and above, WARN, the rest. */
export function severityClass(n: number): string {
  return n >= 17 ? "text-status-bad" : n >= 13 ? "text-ev-error" : "text-faint"
}

/** Every page read, merged: cut when the last page read was full and
 * named a next one past LOG_PAGES. */
export interface AllLogs extends LogsPage {
  cut: boolean
}

/** fetchAllLogs reads the run's lines page by page (at or above
 * severity), up to LOG_PAGES pages; the holes are every page's,
 * deduped, the badge the first page's (a hidden or not_recorded page
 * answers with no lines and no next page). Given prev (a running run's
 * last read), it reads on from the line after prev's last, its pages
 * counted against the same cap; a cut prev is kept as it is. */
export async function fetchAllLogs(
  runId: string,
  severity?: string,
  pages = LOG_PAGES,
  prev?: AllLogs
): Promise<AllLogs> {
  if (prev?.cut) return prev
  // The cap held already (a running run's reads reached pages ×
  // LOG_PAGE lines): no page more, the read is cut — never a page per
  // poll past the cap (P8-fix-b #9).
  if (prev && prev.logs.length >= pages * LOG_PAGE)
    return { ...prev, cut: true }
  const read = (from?: number) =>
    fetchLogs(runId, {
      limit: LOG_PAGE,
      ...(from === undefined ? {} : { from }),
      ...(severity ? { severity } : {}),
    })
  const logs: LogRow[] = [...(prev?.logs ?? [])]
  const holes: StepHole[] = [...(prev?.holes ?? [])]
  const seen = new Set(holes.map((h) => `${h.hole}|${h.reason}`))
  const tail = logs.at(-1)?.index
  const first = await read(tail === undefined ? undefined : tail + 1)
  const budget = Math.max(1, pages - Math.ceil(logs.length / LOG_PAGE))
  let cut = false
  for (let page = first, i = 1; ; i++) {
    logs.push(...page.logs)
    for (const h of page.holes ?? [])
      if (!seen.has(`${h.hole}|${h.reason}`)) {
        seen.add(`${h.hole}|${h.reason}`)
        holes.push(h)
      }
    if (page.next_from === undefined) break
    if (i >= budget) {
      cut = true
      break
    }
    page = await read(page.next_from)
  }
  return { ...first, logs, holes, cut, next_from: undefined }
}

/** The run's lines (at or above severity), every page up to the cap:
 * the drawer's Logs tab (no severity), the run-level view and the
 * ticks (warn) share the read where they ask the same. While the run
 * runs its lines arrive as its spans end: read again every
 * LOGS_POLL_MS from the last line held, never from 0, and not once the
 * read is cut. A line's index is its place in time among the lines its
 * spans attribute, so a span ending late can slot lines below the last
 * held: the finished run's read (its own key) is whole, once. */
export function allLogsQuery(id: string, severity?: string, running = false) {
  return queryOptions({
    queryKey: ["logs", id, severity ?? "", running],
    staleTime: 30_000,
    queryFn: ({ client, queryKey }) =>
      fetchAllLogs(
        id,
        severity,
        LOG_PAGES,
        running ? client.getQueryData<AllLogs>(queryKey) : undefined
      ),
    // The live read's lines stay drawn while the finished read loads.
    placeholderData: (held) => held,
    refetchInterval: (q) =>
      running && !q.state.data?.cut ? LOGS_POLL_MS : false,
  })
}

/** How often a running run's lines are read again. */
export const LOGS_POLL_MS = 5000

/** jumpSel is the ?sel= a line's "jump to span" writes: t:<span id>,
 * the row that span (or its nearest drawn ancestor) is behind, in
 * whichever run of the tree draws it. */
export const jumpSel = (spanId: string) => `t:${spanId}`

/** A run tree row as the ticks read it: its key and its timed span. */
export interface TickRow {
  key: string
  spanId?: string
}

/** A row's WARN / ERROR line counts. */
export interface Tick {
  warn: number
  error: number
}

/** logTicks counts each WARN / ERROR line on the row it landed on: the
 * row whose span the line names, else the row of its nearest ancestor
 * span (parents: span id → parent span id). A line no row is above
 * ticks nothing. */
export function logTicks(
  lines: readonly LogRow[],
  rows: readonly TickRow[],
  parents: ReadonlyMap<string, string>
): Map<string, Tick> {
  const bySpan = new Map<string, string>()
  for (const r of rows) if (r.spanId) bySpan.set(r.spanId, r.key)
  const out = new Map<string, Tick>()
  for (const l of lines) {
    if (l.severity_number < 13 || !l.span_id) continue
    let key: string | undefined
    for (
      let s: string | undefined = l.span_id, hops = 0;
      s && !key && hops < 64;
      s = parents.get(s), hops++
    )
      key = bySpan.get(s)
    if (!key) continue
    const t = out.get(key) ?? { warn: 0, error: 0 }
    if (l.severity_number >= 17) t.error++
    else t.warn++
    out.set(key, t)
  }
  return out
}
