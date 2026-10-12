// Experiments under their source turn (S4.7, WEFT-PLAYGROUND §5.4): a
// playground run names the turn it was taken from in forked_from —
// "<source run id>#<from_step>" (weft/runtime stamps it) — and, when
// the command carried one, the session's public id. An ephemeral
// experiment has no session id, so a session's own runs never include
// it: the session page finds the forks by asking for playground runs
// and joining on forked_from.
import { fetchRuns, nextCursor } from "./api"
import type { PageCursor, RunRow, SessionRow } from "./api"

/** The run an experiment was forked from, and the step it continued
 * at: forked_from is "<run id>#<from_step>". A run id may itself hold
 * a "#", so the step is whatever follows the LAST one. */
export function forkSource(row: Pick<RunRow, "forked_from">): {
  runID: string
  fromStep: number
} | null {
  const ff = row.forked_from
  if (!ff) return null
  const i = ff.lastIndexOf("#")
  if (i < 0) return { runID: ff, fromStep: 0 }
  const step = Number(ff.slice(i + 1))
  if (!Number.isInteger(step) || step < 0) return { runID: ff, fromStep: 0 }
  return { runID: ff.slice(0, i), fromStep: step }
}

/** nestExperiments groups the playground runs under the turns they
 * forked: source run id → its experiments, oldest first. Runs that
 * name a turn outside the list are left out. */
export function nestExperiments(
  turns: Pick<RunRow, "id">[],
  runs: RunRow[]
): Map<string, RunRow[]> {
  const ids = new Set(turns.map((t) => t.id))
  const out = new Map<string, RunRow[]>()
  for (const r of runs) {
    const src = forkSource(r)
    if (!src || !ids.has(src.runID) || ids.has(r.id)) continue
    const list = out.get(src.runID)
    if (list) list.push(r)
    else out.set(src.runID, [r])
  }
  for (const list of out.values())
    list.sort((a, b) => Date.parse(a.started) - Date.parse(b.started))
  return out
}

/** How far the forks search pages: PAGES × LIMIT playground runs. */
const PAGES = 4
const LIMIT = 500

/**
 * fetchSessionForks reads the playground runs that may hang off a
 * session's turns: scoped to the session's public id when it has one
 * (the experiments the panel starts carry it, S4.6), else to its
 * agent. It follows the page cursor (next_before, next_before_id) for a bounded number of pages and
 * says when it stopped early — the list is then incomplete, and the
 * page says so rather than implying there are no more.
 */
export async function fetchSessionForks(
  session: Pick<SessionRow, "public_id" | "agent">
): Promise<{ runs: RunRow[]; truncated: boolean }> {
  const scope = session.public_id
    ? { public_id: session.public_id }
    : session.agent
      ? { agent: session.agent }
      : {}
  const runs: RunRow[] = []
  let cursor: PageCursor | undefined
  for (let page = 0; page < PAGES; page++) {
    const doc = await fetchRuns({
      ...scope,
      playground: true,
      limit: LIMIT,
      before: cursor?.before,
      before_id: cursor?.before_id,
    })
    runs.push(...doc.runs)
    // The exact cursor must move, or the walk ends (never spin on a
    // page).
    const next = nextCursor(doc, cursor)
    if (!next) return { runs, truncated: false }
    cursor = next
  }
  return { runs, truncated: true }
}

// ── An experiment's id (F5 review) ──────────────────────────────────

/** POST /api/experiments' id and a command's experiment_id follow
 * command_id's rule (studio/playground.go): 400 otherwise. */
export const EXPERIMENT_ID_RE = /^[A-Za-z0-9._:-]{1,128}$/

export const validExperimentID = (id: string | undefined): boolean =>
  !!id && EXPERIMENT_ID_RE.test(id)

/** mintExperimentID is the id a matrix is saved under, never the typed
 * name sent raw: a name that already fits the rule is its own id (the
 * same name re-runs the same experiment); any other name is slugged
 * into the rule's charset with the page's nonce; no name is
 * exp_<stamp>. The name itself is kept as the experiment's name. */
export function mintExperimentID(
  name: string,
  stamp: string,
  nonce: string
): string {
  const n = name.trim()
  if (validExperimentID(n)) return n
  const clean = (s: string) => s.replace(/[^A-Za-z0-9._:-]+/g, "-")
  if (!n) return clean(`exp_${stamp}`).slice(0, 128)
  const slug =
    clean(n)
      .replace(/^-+|-+$/g, "")
      .slice(0, 100) || "exp"
  return `${slug}_${clean(nonce)}`.slice(0, 128)
}
