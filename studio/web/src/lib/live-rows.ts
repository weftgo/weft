// The Live page's rows (plan H6): what one `all` stream (/api/live?
// all=1, kinds event,run) says about every run, folded into one row per
// run. Plain TypeScript, no React — the page holds a LiveRows value and
// replaces it per frame.
//
// A row is born from a run frame (the run row that changed) or from the
// running list the page seeds itself with (run frames are never
// backfilled: a run already running when the page opened says nothing
// until its next write). Event frames keep its live figures (H6.1's
// rule): the step is the last step_start's or step_finish's index (in
// flight between the two), tokens so far the sum of step_finish usage
// kept per step index (a re-delivered step_finish — a resume's backfill
// past live.ts's dedup window — replaces its step's, never adds),
// TTFT the last step_finish's ttft_ms. Cost is the row's own block —
// unknown while running (A5.1: usage arrives at run end), known on the
// final run frame. A row that ended stays LIVE_GRACE_MS (a minute),
// then drops (unless the reader is peeking at it); a page opened (or
// reloaded) lists the runs that ended within that minute too
// (applyRecent, H9), so a reload does not forget them. Attempt frames
// (kind attempt, O17: lib/live-attempts.ts) keep each run's newest
// attempt — a retrying step is seen before its chat span ends. The rows
// are drawn in tree order (lib/delegation.ts's nest): a subagent right
// under its parent, indented.
import type { RunRow } from "./api"
import { nest } from "./delegation"
import type { LiveRecord } from "./live"
import type { LiveAttempt } from "./live-attempts"
import { newerAttempt } from "./live-attempts"
import { STATUS, statusOf } from "./status"

/** How long a finished run stays listed: a minute (H9, UX-A-18). */
export const LIVE_GRACE_MS = 60_000

/** What the footer says about the rows that ended. */
export const LIVE_GRACE_LINE =
  "a run that ended stays a minute, dimmed — a reload lists the runs that ended in the last minute"

/** How long an armed cancel waits for its confirming click. */
export const CANCEL_CONFIRM_MS = 3_000

/** How long a sent cancel shows "cancelling…" without seeing the run
 * end (a cancel is best effort): then the button is back. */
export const CANCEL_WAIT_MS = 30_000

/** The polling fallback's interval (no all stream for this caller). */
export const LIVE_POLL_MS = 5_000

/** The resume backfill's bound as studio/live.go keeps it (allBackfill
 * Window, allBackfillMax): what the footer says. */
export const LIVE_BACKFILL_LINE =
  "a reconnect resumes: runs running or seen in the last hour, newest 500"

/** What the event frames said about one run. */
export interface LiveStats {
  /** The newest step index seen (step_start or step_finish), or null. */
  step: number | null
  /** A step_start (or a tool call) with no step_finish after it yet. */
  inFlight: boolean
  /** The sums of perStep (and of unindexed). */
  input: number
  output: number
  /** Each step's usage, by step index (a re-delivery replaces). */
  perStep: Record<number, { input: number; output: number }>
  /** Usage from step_finish frames without an index (summed). */
  unindexed: { input: number; output: number }
  /** At least one step_finish carried usage. */
  sawUsage: boolean
  ttftMs: number | null
  /** The first frame seen for this run was not its run_start (the page
   * joined mid-run, or a resume's backfill began past it): the token
   * sum covers only the steps this page saw. */
  partial: boolean
  /** Date.now() of the last frame (prunes stats with no row). */
  touched: number
}

export interface LiveEntry {
  run: RunRow
  /** Date.now() when this page first saw the run ended; null while it
   * runs. */
  endedAt: number | null
}

export interface LiveRows {
  entries: Record<string, LiveEntry>
  stats: Record<string, LiveStats>
  /** The kinds a resume could not read, by run (backfill_error) — kept
   * apart from the rows: the frame may name a run listed later. */
  unreadable: Record<string, string[]>
  /** Each run's newest attempt frame (O17), by run. */
  attempts: Record<string, LiveAttempt>
}

export const emptyLiveRows: LiveRows = {
  entries: {},
  stats: {},
  unreadable: {},
  attempts: {},
}

/** ended: the run row says the run is over (finished, or parked). */
export function ended(run: Pick<RunRow, "status" | "pending">): boolean {
  return run.status !== "running" || run.pending > 0
}

/** The fresher of two rows of one run (last_seen; ties keep `b`). */
function fresher(a: RunRow | undefined, b: RunRow): RunRow {
  if (!a) return b
  return Date.parse(a.last_seen) > Date.parse(b.last_seen) ? a : b
}

function put(
  state: LiveRows,
  run: RunRow,
  now: number,
  endedAt0?: number
): LiveRows {
  const prev = state.entries[run.id] as LiveEntry | undefined
  const row = fresher(prev?.run, run)
  const endedAt = ended(row) ? (prev?.endedAt ?? endedAt0 ?? now) : null
  return {
    ...state,
    entries: { ...state.entries, [run.id]: { run: row, endedAt } },
  }
}

/** applyRun folds one run frame: the row, created or refreshed. */
export function applyRun(
  state: LiveRows,
  run: RunRow,
  now = Date.now()
): LiveRows {
  // A derived row folded into another (removed): dropped from the page.
  if (run.removed) {
    if (!Object.hasOwn(state.entries, run.id)) return state
    const entries = { ...state.entries }
    delete entries[run.id]
    return { ...state, entries }
  }
  return put(state, run, now)
}

/** applySeed folds a read of the running list: rows the stream has
 * not told about are added; a row it has is kept when fresher. */
export function applySeed(
  state: LiveRows,
  runs: RunRow[],
  now = Date.now()
): LiveRows {
  let next = state
  for (const r of runs) next = put(next, r, now)
  return next
}

/** applyRecent folds a read of the latest runs on the page's first
 * load: those that ended within LIVE_GRACE_MS (by their finished time,
 * else last seen) are listed as just ended, their grace counted from
 * that time — a reload keeps them; older ones are not. */
export function applyRecent(
  state: LiveRows,
  runs: RunRow[],
  now = Date.now()
): LiveRows {
  let next = state
  for (const r of runs) {
    if (!ended(r)) continue
    const at = Date.parse(r.finished ?? r.last_seen)
    if (!Number.isFinite(at) || now - at > LIVE_GRACE_MS) continue
    next = put(next, r, now, Math.min(at, now))
  }
  return next
}

/** applyAttempt folds one attempt frame: the run's newest attempt. */
export function applyAttempt(state: LiveRows, a: LiveAttempt): LiveRows {
  const cur = state.attempts[a.run_id] as LiveAttempt | undefined
  const next = newerAttempt(cur, a)
  if (next === cur) return state
  return { ...state, attempts: { ...state.attempts, [a.run_id]: next } }
}

function num(v: unknown): number {
  return typeof v === "number" && Number.isFinite(v) ? v : 0
}

/** applyRecord folds one record frame (only event records count:
 * deltas and messages carry no step or usage). */
export function applyRecord(
  state: LiveRows,
  rec: LiveRecord,
  now = Date.now()
): LiveRows {
  if (rec.kind !== "event" || !rec.run_id) return state
  const ev = rec.event as {
    type?: string
    index?: unknown
    usage?: { input_tokens?: unknown; output_tokens?: unknown }
    ttft_ms?: unknown
  } | null
  const prev = state.stats[rec.run_id] as LiveStats | undefined
  const s: LiveStats = prev
    ? { ...prev, touched: now }
    : {
        step: null,
        inFlight: false,
        input: 0,
        output: 0,
        perStep: {},
        unindexed: { input: 0, output: 0 },
        sawUsage: false,
        ttftMs: null,
        partial: ev?.type !== "run_start",
        touched: now,
      }
  switch (ev?.type) {
    case "step_start":
      if (typeof ev.index === "number")
        s.step = Math.max(s.step ?? -1, ev.index)
      s.inFlight = true
      break
    case "tool_start":
      s.inFlight = true
      break
    case "step_finish":
      if (typeof ev.index === "number")
        s.step = Math.max(s.step ?? -1, ev.index)
      s.inFlight = false
      if (ev.usage && typeof ev.usage === "object") {
        const u = {
          input: num(ev.usage.input_tokens),
          output: num(ev.usage.output_tokens),
        }
        if (typeof ev.index === "number")
          s.perStep = { ...s.perStep, [ev.index]: u }
        else
          s.unindexed = {
            input: s.unindexed.input + u.input,
            output: s.unindexed.output + u.output,
          }
        let input = s.unindexed.input
        let output = s.unindexed.output
        for (const v of Object.values(s.perStep)) {
          input += v.input
          output += v.output
        }
        s.input = input
        s.output = output
        s.sawUsage = true
      }
      if (typeof ev.ttft_ms === "number" && ev.ttft_ms > 0)
        s.ttftMs = ev.ttft_ms
      break
    case "run_finish":
      s.inFlight = false
      break
    default:
      break
  }
  return { ...state, stats: { ...state.stats, [rec.run_id]: s } }
}

/** applyBackfillError marks a run whose stored records a resume could
 * not read: its row shows a "could not read" badge (now, or when the
 * row arrives). */
export function applyBackfillError(
  state: LiveRows,
  f: { run_id: string; kind: string }
): LiveRows {
  const kind = f.kind || "records"
  const have = state.unreadable[f.run_id] ?? []
  if (have.includes(kind)) return state
  return {
    ...state,
    unreadable: { ...state.unreadable, [f.run_id]: [...have, kind] },
  }
}

/** prune drops the rows that ended more than LIVE_GRACE_MS ago (keep
 * excepted: the peeked run), and the stats of runs with no row that
 * have been quiet as long. Returns the same value when nothing goes. */
export function prune(state: LiveRows, now = Date.now(), keep = ""): LiveRows {
  let changed = false
  const entries: Record<string, LiveEntry> = {}
  for (const [id, e] of Object.entries(state.entries)) {
    if (e.endedAt !== null && now - e.endedAt > LIVE_GRACE_MS && id !== keep) {
      changed = true
      continue
    }
    entries[id] = e
  }
  const stats: Record<string, LiveStats> = {}
  for (const [id, s] of Object.entries(state.stats)) {
    if (!(id in entries) && now - s.touched > LIVE_GRACE_MS) {
      changed = true
      continue
    }
    stats[id] = s
  }
  if (!changed) return state
  const unreadable: Record<string, string[]> = {}
  for (const [id, ks] of Object.entries(state.unreadable))
    if (id in entries || id in stats) unreadable[id] = ks
  const attempts: Record<string, LiveAttempt> = {}
  for (const [id, a] of Object.entries(state.attempts))
    if (id in entries || id in stats) attempts[id] = a
  return { entries, stats, unreadable, attempts }
}

/** A row as the table draws it. */
export interface LiveRowView {
  run: RunRow
  stats: LiveStats | null
  /** Kinds a resume could not read (the "could not read" badge). */
  unreadable: string[]
  endedAt: number | null
  /** The run's newest attempt frame (O17), when one arrived. */
  attempt?: LiveAttempt
  /** Its depth in the tree the rows are drawn in (0 a root). */
  depth: number
}

/** rowsOf lists the rows in tree order (lib/delegation.ts's nest): the
 * roots newest first (started, then id), each run's children right
 * under it, newest first, one level deeper. */
export function rowsOf(state: LiveRows): LiveRowView[] {
  const sorted = Object.values(state.entries)
    .map((e) => e.run)
    .sort((a, b) => {
      const d = Date.parse(b.started) - Date.parse(a.started)
      return d !== 0 && Number.isFinite(d) ? d : a.id < b.id ? 1 : -1
    })
  return nest(sorted).map(({ row, depth }) => ({
    run: row,
    stats: state.stats[row.id] ?? null,
    unreadable: state.unreadable[row.id] ?? [],
    endedAt: state.entries[row.id].endedAt,
    ...(row.id in state.attempts ? { attempt: state.attempts[row.id] } : {}),
    depth,
  }))
}

/** A polled row as the table draws it (no stream: the row's own
 * figures only). */
export function polledView(run: RunRow): LiveRowView {
  return { run, stats: null, unreadable: [], endedAt: null, depth: 0 }
}

/** The step cell: current (the newest index seen, 1-based) and, once
 * the run ended, the total the row counts. */
export function stepText(v: LiveRowView): { text: string; title: string } {
  const s = v.stats
  const total = ended(v.run) && v.run.steps > 0 ? v.run.steps : null
  if (s && s.step !== null) {
    const cur = s.step + 1
    if (total !== null)
      return {
        text: `${cur} / ${total}`,
        title: `${total} step${total === 1 ? "" : "s"} in all`,
      }
    return s.inFlight
      ? { text: `${cur}…`, title: `step ${cur} in flight` }
      : {
          text: String(cur),
          title: `${cur} step${cur === 1 ? "" : "s"} finished`,
        }
  }
  if (v.run.steps > 0)
    return total !== null
      ? {
          text: `${total} / ${total}`,
          title: `${total} step${total === 1 ? "" : "s"} in all`,
        }
      : {
          text: String(v.run.steps),
          title: "the row's step count (no step event seen on this page yet)",
        }
  return { text: "—", title: "no step finished yet" }
}

/** The tokens cell: input + output so far. */
export function tokensOf(
  v: LiveRowView
): { n: number; input: number; output: number; partial: boolean } | null {
  const s = v.stats
  // A row from an older Studio may come without usage.
  const u = v.run.usage as RunRow["usage"] | undefined
  const rowIn = u?.input_tokens ?? 0
  const rowOut = u?.output_tokens ?? 0
  // The final row's usage is the run's own total: it wins once ended.
  if (ended(v.run) && rowIn + rowOut > 0)
    return { n: rowIn + rowOut, input: rowIn, output: rowOut, partial: false }
  if (s?.sawUsage)
    return {
      n: s.input + s.output,
      input: s.input,
      output: s.output,
      partial: s.partial,
    }
  if (rowIn + rowOut > 0)
    return { n: rowIn + rowOut, input: rowIn, output: rowOut, partial: false }
  return null
}

/** The reason a running run's cost is unknown (A5.1's words). */
export const RUNNING_COST_REASON = "running: usage arrives at run end"

/** The cost block the cell draws: the row's own, with A5.1's reason
 * when a running row came without one. */
export function costFieldsOf(run: RunRow): RunRow {
  if (run.status === "running" && !run.cost_known && !run.cost_reason)
    return {
      ...run,
      cost_known: false,
      cost_usd: null,
      cost_badge: "unknown",
      cost_reason: RUNNING_COST_REASON,
    }
  return run
}

/** The ?peek= run id, validated: an id-shaped string or nothing. */
export function validPeek(v: unknown): string | undefined {
  return typeof v === "string" && /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/.test(v)
    ? v
    : undefined
}

/** Matches a row against the filter box, case-folded, every word
 * matching: a status word (lib/status.ts's keys: running · succeeded ·
 * failed · interrupted · parked · cancelled · resumed) matches the
 * row's status key — parked an open or not-yet-linked park, never a
 * decided one (its resume looked for among runs, the held rows); any
 * other word a substring of the agent or the status word. */
export function matchesFilter(
  run: RunRow,
  q: string,
  runs?: readonly RunRow[]
): boolean {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean)
  if (!words.length) return true
  const st = statusOf(run, runs)
  const hay = `${run.agent} ${st.word}`.toLowerCase()
  return words.every((w) =>
    Object.hasOwn(STATUS, w) ? st.key === w : hay.includes(w)
  )
}
