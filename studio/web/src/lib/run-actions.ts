// The run page's export and delete verbs (U26; UX-A-15): their rules
// and words, pure. The routes are studio/export.go's and
// studio/delete.go's:
//
//   - export: GET /api/runs/{id}/export?format=json|jsonl|otlp|wefttest
//     (capability "export") — those four and no other: "xml" is a 400.
//     wefttest is "save as test" (lib/saves.ts); this file offers the
//     other three. A read-scoped panel token exports json and jsonl
//     (the request block badged hidden) and is refused otlp, which
//     carries the request records.
//   - delete: DELETE /api/runs/{id} (capability "delete") — the run with
//     its subagent children, from studio's database (obsdb's tables)
//     only; the app's own conversation store is untouched. The server
//     answers the dev token, or a loopback Host with no token (setup A),
//     and never a panel token: meta's live_all is that same rule
//     (mayReadDBPath), so the verb is offered exactly where it works,
//     and where it is not, one faint line says why (replayOffWhy's
//     pattern), never a dead button.

import { tokenScopeOf } from "./replay"

/** One export format the run page offers beside save as test. */
export interface ExportFormat {
  format: "json" | "jsonl" | "otlp"
  label: string
  title: string
  /** It carries the request records: a read-scoped token is refused. */
  prompts: boolean
}

export const EXPORT_FORMATS: readonly ExportFormat[] = [
  {
    format: "json",
    label: "json",
    title:
      "download the whole run as one JSON document: the run, its events, transcript, compactions, requests, spans and holes",
    prompts: false,
  },
  {
    format: "jsonl",
    label: "jsonl",
    title: "download the same records as JSON lines, one per line",
    prompts: false,
  },
  {
    format: "otlp",
    label: "otlp",
    title:
      "download the run's records and spans as OTLP/JSON — POST them to another Studio's /v1/logs and /v1/traces to re-ingest the run",
    prompts: true,
  },
]

export const EXPORT_LABEL = "export"

/** exportFormatsFor is what the identity may download: none without
 * capability "export"; a read-scoped panel token no otlp. */
export function exportFormatsFor(
  capabilities: readonly string[],
  token: string
): readonly ExportFormat[] {
  if (!capabilities.includes("export")) return []
  if (tokenScopeOf(token) === "read")
    return EXPORT_FORMATS.filter((f) => !f.prompts)
  return EXPORT_FORMATS
}

/** An export's outcome line (the toast). */
export function exportWords(
  o: { ok: true; file: string } | { ok: false; message: string }
): string {
  return o.ok ? `saved ${o.file}` : `export refused: ${o.message}`
}

export const DELETE_LABEL = "delete run…"

/** Why the delete verb is not offered, one line each. */
export const DELETE_OFF = {
  capability: 'delete needs the capability "delete"',
  panel:
    "deletion answers the dev token (or loopback in setup A) only, never a panel token",
  host: "deletion answers the dev token, or a loopback Host when no studio.Token is configured",
} as const

/** deleteOffWhy is that line, or undefined when the verb is offered:
 * the capability first (with capabilities_off's reason when meta gave
 * one), then the identity — a panel token, else a non-loopback Host
 * without the server token (meta live_all false: mayReadDBPath). */
export function deleteOffWhy(
  capabilities: readonly string[],
  token: string,
  liveAll: boolean | undefined,
  /** meta's capabilities_off.delete, when it gave one. */
  deleteOff?: string
): string | undefined {
  if (!capabilities.includes("delete"))
    return deleteOff
      ? `${DELETE_OFF.capability} — ${deleteOff}`
      : DELETE_OFF.capability
  if (tokenScopeOf(token) !== "") return DELETE_OFF.panel
  if (!liveAll) return DELETE_OFF.host
  return undefined
}

/** The confirm's words: what goes, and what stays. The server deletes
 * the run's whole closure — every run whose parent chain reaches it,
 * at any depth (obsdb/sqlite/delete.go's closure) — and the derived
 * runs read from its tools' spans (a stock agent's run under one of
 * its tools, obsdb.StockRunsOf). The page knows the children (one
 * level) and each child's children_count (the next), so the count is
 * those known, a floor ("N+ known") when a grandchild may have its
 * own. */
export function deleteConfirmWords(run: {
  id: string
  children: readonly { id: string; children_count?: number }[]
}): {
  title: string
  body: string
  children: string
  derived: string
  action: string
  cancel: string
} {
  const grand = run.children.reduce((n, c) => n + (c.children_count ?? 0), 0)
  const known = run.children.length + grand
  const floor = grand > 0 ? "+" : ""
  return {
    title: `Delete run ${run.id}?`,
    body: "Its spans, records, app logs and search index are removed from studio's database only — the app's own conversation store is untouched. This cannot be undone.",
    children:
      known === 0
        ? "No subagent run is known under it; any recorded under it goes with it."
        : `It goes with every subagent run under it, at any depth (${known}${floor} known):`,
    derived:
      "Derived runs read from its tools' spans (a stock agent under one of its tools) go too.",
    action: "delete run",
    cancel: "cancel",
  }
}

/** A listed child's own subagent runs, beside it in the confirm. */
export function childrenUnder(n: number): string {
  return n === 1
    ? "(+1 subagent run under it)"
    : `(+${n} subagent runs under it)`
}

/** The delete's outcome line (the toast). */
export function deletedWords(
  o: { ok: true; id: string } | { ok: false; message: string }
): string {
  return o.ok ? `deleted run ${o.id}` : `delete refused: ${o.message}`
}
