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
  count: (n: number) => `${n} line${n === 1 ? "" : "s"}`,
  cutReason: (n: number) =>
    `the first ${n.toLocaleString("en-US")} lines are shown (${LOG_PAGES} pages of ${LOG_PAGE.toLocaleString("en-US")}): later lines of this run were not read here, nothing was lost`,
  cutFix:
    "filter by severity, or page the route: GET /api/runs/{id}/logs?from=<next_from>",
  /** A row's tick: "E1 W2" (the problem badges' letters). */
  tick: (t: { warn: number; error: number }) =>
    [t.error ? `E${t.error}` : "", t.warn ? `W${t.warn}` : ""]
      .filter(Boolean)
      .join(" "),
  tickHint: (warn: number, err: number) =>
    [
      err ? `${err} app log error${err === 1 ? "" : "s"}` : "",
      warn ? `${warn} app log warning${warn === 1 ? "" : "s"}` : "",
    ]
      .filter(Boolean)
      .join(", ") + " under this row (its Logs tab)",
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
 * answers with no lines and no next page). */
export async function fetchAllLogs(
  runId: string,
  severity?: string,
  pages = LOG_PAGES
): Promise<AllLogs> {
  const read = (from?: number) =>
    fetchLogs(runId, {
      limit: LOG_PAGE,
      ...(from === undefined ? {} : { from }),
      ...(severity ? { severity } : {}),
    })
  const first = await read()
  const logs: LogRow[] = []
  const holes: StepHole[] = []
  const seen = new Set<string>()
  let cut = false
  for (let page = first, i = 1; ; i++) {
    logs.push(...page.logs)
    for (const h of page.holes ?? [])
      if (!seen.has(`${h.hole}|${h.reason}`)) {
        seen.add(`${h.hole}|${h.reason}`)
        holes.push(h)
      }
    if (page.next_from === undefined) break
    if (i >= pages) {
      cut = true
      break
    }
    page = await read(page.next_from)
  }
  return { ...first, logs, holes, cut, next_from: undefined }
}

/** The run's lines (at or above severity), every page up to the cap:
 * the drawer's Logs tab (no severity) and the run-level view share
 * the read. */
export function allLogsQuery(id: string, severity?: string, running = false) {
  return queryOptions({
    queryKey: ["logs", id, severity ?? ""],
    staleTime: 30_000,
    queryFn: () => fetchAllLogs(id, severity),
    // A running run's lines arrive as its spans end: read again.
    refetchInterval: running ? LOGS_POLL_MS : false,
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
