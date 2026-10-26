// Experiments under their source turn (S4.7, WEFT-PLAYGROUND §5.4): a
// playground run names the turn it was taken from in forked_from —
// "<source run id>#<from_step>" (weft/runtime stamps it) — and, when
// the command carried one, the session's public id. An ephemeral
// experiment has no session id, so a session's own runs never include
// it: the session page finds the forks by asking for playground runs
// and joining on forked_from.
import { fetchRuns, nextCursor } from "./api"
import { isScripted } from "./cost"
import { editMarks } from "./edits"
import { producedTexts } from "./events"
import { plural, tokenPair, tokenTotal } from "./format"
import type { ExperimentRow, PageCursor, RunRow, SessionRow } from "./api"
import type { TranscriptBatch } from "./events"
import { stepDiffView } from "./stepdiff"
import { forkSource } from "./run-row"
import type { DiffDoc } from "./stepdiff"

// forkSource is lib/run-row.ts's (U21.1): the lineage chips read it.
export { forkSource }

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
export const counted = (n: number, noun: string): string => plural(n, noun)

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

// ── The grid cell and the replays list (UX-C-16, UX-C-15) ──────────

/** The longest answer line a cell draws; the rest is "…". */
export const ANSWER_CHARS = 80

/** answerLine is a run's answer in one line: the first non-empty line
 * of its last own assistant text, cut at ANSWER_CHARS — null when the
 * run produced no text (a parked or failed run, a content-off one). */
export function answerLine(batches: TranscriptBatch[]): string | null {
  const texts = producedTexts(batches)
  const last = texts.at(-1)
  if (!last) return null
  const line = last
    .split("\n")
    .map((l) => l.trim())
    .find(Boolean)
  if (!line) return null
  return line.length > ANSWER_CHARS
    ? `${line.slice(0, ANSWER_CHARS - 1)}…`
    : line
}

/** answerText is a run's whole last own assistant text (the pass
 * predicate's answer, lib/predicate.ts) — null, never "", when it is
 * not known: no assistant text (content off, stripped, a run that never
 * answered) or a batch whose body did not read as messages
 * (asTranscript's unreadable). */
export function answerText(batches: TranscriptBatch[]): string | null {
  if (batches.some((b) => b.unreadable)) return null
  return producedTexts(batches).at(-1) || null
}

/** usageWords is a run's tokens in a cell: "15 tokens" (in + out,
 * lib/format.ts's tokenTotal), the split in its title (usageTitle). */
export function usageWords(r: Pick<RunRow, "usage">): string {
  return tokenTotal(r.usage.input_tokens + r.usage.output_tokens)
}

export function usageTitle(r: Pick<RunRow, "usage">): string {
  return tokenPair(r.usage.input_tokens, r.usage.output_tokens, true)
}

/** idTail is a run id as a cell shows it: its tail (ULIDs share their
 * head, so cells of one matrix would all read alike), the whole id in
 * the title. */
export function idTail(id: string, n = 6): string {
  return id.length > n + 1 ? `…${id.slice(-n)}` : id
}

/** The grid cell's and the replays list's words. */
export const REPLAY_WORDS = {
  noAnswer: "no answer text",
  noAnswerHint:
    "the run produced no assistant text: it parked, failed, called tools only, or its content was not recorded",
  reading: "…",
  tabs: { matrices: "matrices", replays: "replays" },
  replaysHint:
    "every playground run, grouped by the run it replays (weft.forked_from)",
  noReplays:
    "A replay from the drawer (replay from here) is listed here under its source run.",
  noSource: "no source run (typed inputs)",
  fromStep: (n: number) => `from step ${n}`,
  compare: "compare",
  edits: (n: number) => plural(n, "edit"),
  prompt: "prompt",
  model: "model",
  unsaved:
    "no saved definition: these runs carry the experiment id, but no matrix was saved under it",
  cut: (n: number) =>
    `the newest ${n} playground runs: older ones are not listed`,
  latencyHint: "the run's wall time, start to finish",
  same: "same as source",
  sameRecorded: "same where both recorded",
  changedAt: (n: number) => `changed at step ${n}`,
  vsNotRead: (n: number) =>
    `not read: the grid reads the compare of its first ${n} cells; open the compare`,
} as const

/** How many grid cells read their vs-source chip: one GET /api/diff
 * each, the grid's first cells (inputs × variants order). */
export const VS_CHIP_CELLS = 24

/** vsSourceWords is a cell's chip from its diff against the source
 * (aligned on the source's ordinals, A8): "changed at step N" at the
 * first change, else "same as source" — "same where both recorded"
 * when some cells were not comparable. */
export function vsSourceWords(doc: DiffDoc): string {
  const v = stepDiffView(doc)
  if (v.markers.length) return REPLAY_WORDS.changedAt(v.markers[0])
  return v.unknown ? REPLAY_WORDS.sameRecorded : REPLAY_WORDS.same
}

/** replayChanges is what a replay changed against its source, as
 * chips: the transcript edits its weft.edits names, a prompt whose
 * instructions hash differs from the source's, a model it answered as
 * other than the source's (a live replay). The source row is optional:
 * without it only the edits are known. */
export function replayChanges(
  r: RunRow,
  source?: Pick<RunRow, "instructions_hash" | "model">
): string[] {
  const out: string[] = []
  const edits = editMarks(r)
  const n = edits.marks.length + edits.more
  if (n) out.push(REPLAY_WORDS.edits(n))
  if (
    source &&
    r.instructions_hash &&
    source.instructions_hash &&
    r.instructions_hash !== source.instructions_hash
  )
    out.push(REPLAY_WORDS.prompt)
  if (
    source &&
    !isScripted(r) &&
    (r.model.provider !== source.model.provider ||
      r.model.name !== source.model.name)
  )
    out.push(REPLAY_WORDS.model)
  return out
}

/** One source run and the replays taken from it, oldest first. */
export interface ReplayGroup {
  /** The source run id; "" for playground runs with no source. */
  source: string
  replays: { run: RunRow; fromStep: number }[]
}

/** replayGroups groups playground runs by the run they replay
 * (forkSource), the group with the newest replay first; runs with no
 * source are one last group (source ""). A subagent child of a replay
 * inherits its parent's lineage and is left out: it is reached through
 * its parent. */
export function replayGroups(runs: RunRow[]): ReplayGroup[] {
  const groups = new Map<string, ReplayGroup>()
  for (const r of runs) {
    if (r.parent_run_id || r.caller_run_id) continue
    const src = forkSource(r)
    const key = src?.runID ?? ""
    let g = groups.get(key)
    if (!g) {
      g = { source: key, replays: [] }
      groups.set(key, g)
    }
    g.replays.push({ run: r, fromStep: src?.fromStep ?? 0 })
  }
  const newest = (g: ReplayGroup) =>
    Math.max(...g.replays.map((x) => Date.parse(x.run.started)))
  for (const g of groups.values())
    g.replays.sort(
      (a, b) => Date.parse(a.run.started) - Date.parse(b.run.started)
    )
  return [...groups.values()].sort((a, b) =>
    !a.source !== !b.source ? (a.source ? -1 : 1) : newest(b) - newest(a)
  )
}

/** fetchReplays reads the playground runs, newest first, a bounded
 * number of pages (as fetchSessionForks), saying when it stopped early. */
export async function fetchReplays(): Promise<{
  runs: RunRow[]
  truncated: boolean
}> {
  const runs: RunRow[] = []
  let cursor: PageCursor | undefined
  for (let page = 0; page < PAGES; page++) {
    const doc = await fetchRuns({
      playground: true,
      limit: LIMIT,
      before: cursor?.before,
      before_id: cursor?.before_id,
    })
    runs.push(...doc.runs)
    const next = nextCursor(doc, cursor)
    if (!next) return { runs, truncated: false }
    cursor = next
  }
  return { runs, truncated: true }
}
