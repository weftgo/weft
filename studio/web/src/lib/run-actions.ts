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
import { exportPath } from "./saves"

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
      "download the whole run as one JSON document: the run, its events, transcript, compactions, requests, spans and gaps",
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

/** The "with child runs" toggle beside the formats (UX-A-31: &tree=1),
 * offered on a run with children: the export carries every subagent
 * run under it, each its own document, and the file is named
 * <run>.tree.<ext>. */
export const EXPORT_TREE = {
  label: "with child runs",
  title:
    "also export every child run under this one (at most 200), each with its own events, transcript, requests and spans — a cut or a run another public id holds is said in subagent_holes",
} as const

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

/** "copy as curl" (U30.2; UX-A-15's remainder): the export as a
 * command line. It never carries a token — a token-walled Studio's
 * command names $WEFT_STUDIO_TOKEN, the env var weft dev sets and the
 * CLI reads — and -OJ saves under the server's own file name. */
export const CURL = {
  label: "copy as curl",
  title:
    "copy a curl command that downloads this export — it names $WEFT_STUDIO_TOKEN, never the token itself",
  copied: "copied a curl command",
  refused: "the browser refused the clipboard",
} as const

/** curlFor is that command for one format: the API base the page talks
 * to (lib/api's apiBase, absolute), the route, &tree=1 when asked; the
 * Authorization header only where the Studio walls its API. */
export function curlFor(o: {
  apiBase: string
  runID: string
  format: string
  tree?: boolean
  authRequired?: boolean
}): string {
  const url =
    new URL(exportPath(o.runID, o.format), o.apiBase).toString() +
    (o.tree ? "&tree=1" : "")
  const auth = o.authRequired
    ? ` -H "Authorization: Bearer $WEFT_STUDIO_TOKEN"`
    : ""
  return `curl -fsSL -OJ${auth} '${url.replaceAll("'", "%27")}'`
}

/** The run header's actions under NARROW (U30.2; UX-A-21's
 * remainder): one "⋯ actions" disclosure instead of a row that wraps
 * past the header at phone width. */
export const RUN_MENU = {
  label: "actions",
  title:
    "the run's actions: open in app, save as test, export, copy link, delete",
  narrow: "(max-width: 480px)",
} as const

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
        ? "No child run is known under it; any recorded under it goes with it."
        : `It goes with every child run under it, at any depth (${known}${floor} known):`,
    derived:
      "Derived runs read from its tools' spans (a stock agent under one of its tools) go too.",
    action: "delete run",
    cancel: "cancel",
  }
}

/** A listed child's own child runs, beside it in the confirm. */
export function childrenUnder(n: number): string {
  return n === 1 ? "(+1 child run under it)" : `(+${n} child runs under it)`
}

/** The delete's outcome line (the toast). */
export function deletedWords(
  o: { ok: true; id: string } | { ok: false; message: string }
): string {
  return o.ok ? `deleted run ${o.id}` : `delete refused: ${o.message}`
}
