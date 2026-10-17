// Experiments under their source turn (S4.7, WEFT-PLAYGROUND §5.4): a
// playground run names the turn it was taken from in forked_from —
// "<source run id>#<from_step>" (weft/runtime stamps it) — and, when
// the command carried one, the session's public id. An ephemeral
// experiment has no session id, so a session's own runs never include
// it: the session page finds the forks by asking for playground runs
// and joining on forked_from.
import { fetchRuns, nextCursor } from "./api"
import type { ExperimentRow, PageCursor, RunRow, SessionRow } from "./api"

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

/** counted is "1 variant" / "2 variants": a count with its noun, the
 * plural only past one. */
export const counted = (n: number, noun: string): string =>
  `${n} ${noun}${n === 1 ? "" : "s"}`

/** matrixSize is a matrix's shape in words (UX-C-44): "1 variant × 1
 * input", "2 variants × 3 inputs" — the playground's Matrix header and
 * the experiment page's. */
export function matrixSize(variants: number, inputs: number): string {
  return `${counted(variants, "variant")} × ${counted(inputs, "input")}`
}

/** The experiments list's outcome, engine and source cells (UX-C-36). */
export const EXPERIMENT_CELLS = {
  passHint: "the experiment's top-level runs that succeeded, of all its runs",
  passUnknown:
    "not counted: this Studio predates the list row's outcome, or the experiment holds too many runs to read",
  noSource: "no source run: typed inputs",
  filter: "filter by agent or source run",
} as const

/** passLine is the pass rate, "2 / 3 passed", or undefined when the row
 * carries no succeeded count. */
export function passLine(
  e: Pick<ExperimentRow, "succeeded" | "run_count">
): string | undefined {
  if (e.succeeded === undefined || e.run_count === undefined) return undefined
  return `${e.succeeded} / ${e.run_count} passed`
}

/** experimentEngines is every engine the experiment's cells ran on —
 * the command's and each variant's own — deduped, in order ("live",
 * "live · scripted"); "" when none was saved (a pre-F5.1 definition). */
export function experimentEngines(
  e: Pick<ExperimentRow, "engine" | "variants">
): string {
  const all = [e.engine, ...e.variants.map((v) => v.engine ?? e.engine)]
  return [...new Set(all.filter((x): x is string => !!x))].join(" · ")
}

/** experimentSources is the source runs the experiment replays: the
 * command's and each input's, deduped. */
export function experimentSources(
  e: Pick<ExperimentRow, "source_run_id" | "inputs">
): string[] {
  const all = [e.source_run_id, ...e.inputs.map((i) => i.source_run_id)]
  return [...new Set(all.filter((x): x is string => !!x))]
}

/** matchesExperiment is the list's filter (UX-C-36): every term a
 * substring of the agent, the id, the name or a source run id. */
export function matchesExperiment(e: ExperimentRow, q: string): boolean {
  const hay = [e.agent, e.id, e.name, ...experimentSources(e)]
    .join("\n")
    .toLowerCase()
  return q
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((t) => hay.includes(t))
}

/** matrixStepLine is the matrix's line past step 0 (U26; UX-C-21): the
 * page's one step — the source block's picker, Run's too — and the
 * page's transcript edits every cell carries. */
export function matrixStepLine(
  run: string,
  step: number,
  edits: number
): string {
  const carried =
    edits > 0
      ? ` with the page's ${edits === 1 ? "transcript edit" : `${edits} transcript edits`}`
      : ""
  return `every cell continues ${run} from step ${step} (the page's step, picked above)${carried}; the rows' text is not sent past step 0`
}
