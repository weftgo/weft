// A session's state over time on the sessions list and the session
// header (U16; UX-B-15, UX-B-16, UX-B-28): the outcome strip, the
// "awaiting a decision" state, the failed and busy-ended counts, the
// exchanges, and the list's turn-state filters — the words and roles
// of obsdb.SessionTurnFacts' fields. Studio only: the devtools panel
// imports none of it (its turn chips are lib/turn-chips.ts's).
import type { RunRow, SessionRow, TurnOutcome } from "./api"
import { absoluteTime, plural, relativeTime, spanMs } from "./format"
import type { Role } from "./palette"
import { AWAITING, DECIDED_PARK, STATUS } from "./status"
import type { Status } from "./status"
/** Either busy policy's end (an interrupt or a rollback): a count over
 * both, and an outcome that names no policy (lib/turn-chips.ts's chips
 * name which). */
const ENDED_ANY = "ended by a newer message"

/** One outcome's mark on the strip: a glyph, its word (the hover and
 * the strip's accessible text) and its colour role — red for a real
 * failure only; a busy-policy end (interrupt / rollback) neutral. */
export const OUTCOME: Record<
  TurnOutcome,
  { glyph: string; word: string; role: Role }
> = {
  succeeded: { glyph: "✓", word: STATUS.succeeded.word, role: "succeeded" },
  failed: { glyph: "✗", word: STATUS.failed.word, role: "failed" },
  running: { glyph: "●", word: STATUS.running.word, role: "running" },
  interrupted: {
    glyph: "◌",
    word: STATUS.interrupted.word,
    role: "interrupted",
  },
  cancelled: { glyph: "⊘", word: STATUS.cancelled.word, role: "cancelled" },
  // An interrupt or a rollback (the row's outcome names no policy).
  ended: { glyph: "✗", word: ENDED_ANY, role: "resumed" },
  parked: { glyph: "⏸", word: AWAITING, role: "parked" },
  resumed: { glyph: "⏸", word: DECIDED_PARK, role: "resumed" },
}

/** An outcome the strip does not know (a newer Studio's word): its
 * word as is, neutral. */
export const outcomeOf = (o: string) =>
  Object.hasOwn(OUTCOME, o)
    ? OUTCOME[o as TurnOutcome]
    : { glyph: "?", word: o, role: "resumed" as Role }

/** The strip's accessible name: "outcomes of the newest 7 turns, oldest
 * first: succeeded, …" (+ how many older turns it leaves out). */
export function stripLabel(row: Pick<SessionRow, "turns" | "outcomes">) {
  const os = row.outcomes ?? []
  const older = row.turns - os.length
  return `outcomes of the newest ${plural(os.length, "turn")}, oldest first: ${os.map((o) => outcomeOf(o).word).join(", ")}${older > 0 ? ` — ${older} older not shown` : ""}`
}

/** The session awaits a decision (its newest turn is an open park):
 * "⏸ awaiting a decision · 1 call". */
export const awaitingWords = (n: number) =>
  `⏸ ${AWAITING} · ${plural(n, "call")}`

/** The hover on the awaiting state. */
export const AWAITING_TITLE =
  "the newest turn parked at an approval and no turn resumed it yet: open the parked turn to decide"

/** The header's way to the parked turn. */
export const DECIDE = "decide…"

/** A session's counts beside the strip: real failures red, busy-policy
 * ends apart and neutral ("2✗ interrupted by a newer message") — the
 * words, "" for none. */
export function failedWords(n: number | undefined) {
  return n ? `${n}✗ ${STATUS.failed.word}` : ""
}
export function endedWords(n: number | undefined) {
  return n ? `${n}✗ ${ENDED_ANY}` : ""
}
/** The other cancelled turns (thread.closed, studio.cancel, a
 * deadline; U16-fix): apart again, neutral, the strip's ⊘. */
export function cancelledWords(n: number | undefined) {
  return n ? `${n}${OUTCOME.cancelled.glyph} ${STATUS.cancelled.word}` : ""
}

/** The hovers on the counts. */
export const COUNT_TITLES = {
  failed: "turns whose loop returned an error",
  ended:
    "turns a newer message ended on purpose (weft/thread's Interrupt or Rollback busy policy): not failures",
  cancelled:
    "turns whose context was cancelled for another reason (the session closed, a cancel from Studio, a deadline): not failures of the agent",
}

/** "3 exchanges · 5 turns" when resumes fold into exchanges, else
 * "5 turns" (the session header's exchangesWords over a row's
 * counts). */
export function exchangesOfRow(row: Pick<SessionRow, "turns" | "resumes">) {
  const r = row.resumes ?? 0
  return r > 0 && r < row.turns
    ? `${plural(row.turns - r, "exchange")} · ${plural(row.turns, "turn")}`
    : plural(row.turns, "turn")
}

/** The sessions list's turn-state filters (U16, UX-B-28): each one URL
 * key, a chip that toggles it. */
export const SESSION_FILTERS: {
  key: "awaiting" | "failed" | "compacted" | "child" | "fork"
  word: string
  title: string
}[] = [
  {
    key: "awaiting",
    word: `⏸ ${AWAITING}`,
    title: "sessions whose newest turn is parked with no resume yet",
  },
  {
    key: "failed",
    word: "✗ has a failed turn",
    title:
      "sessions with a turn whose loop returned an error (an interrupt by a newer message, or a cancel, is not one)",
  },
  {
    key: "compacted",
    word: "⌗ compacted",
    title:
      "sessions weft/thread compacted (a compaction marker in the session's state log)",
  },
  {
    key: "child",
    word: "↳ delegated",
    title: "sessions another session delegated (thread/pool)",
  },
  {
    key: "fork",
    word: "⑃ forks",
    title: "sessions a Fork made from another session",
  },
]

/** The newest turn's status filter (U16-fix, /api/sessions?status=):
 * the four derived statuses, each a chip; parked is "awaiting". */
export const SESSION_STATUSES = (
  ["succeeded", "failed", "running", "interrupted"] as const
).map((key) => ({
  key,
  word: `${OUTCOME[key].glyph} ${STATUS[key].word}`,
  title: `sessions whose newest turn is ${STATUS[key].word}`,
  role: OUTCOME[key].role,
}))

/** The since chips (U16-fix, /api/sessions?since=): last seen within. */
export const SESSION_SINCE: { key: string; word: string; ms: number }[] = [
  { key: "1h", word: "last hour", ms: 3_600_000 },
  { key: "24h", word: "last 24 h", ms: 86_400_000 },
  { key: "7d", word: "last 7 days", ms: 7 * 86_400_000 },
]

/** sinceOf is a since chip's RFC 3339 bound now, undefined for none. */
export function sinceOf(key: string | undefined, now = Date.now()) {
  const c = SESSION_SINCE.find((s) => s.key === key)
  return c ? new Date(now - c.ms).toISOString() : undefined
}

// ── The session table (U31.2) ───────────────────────────────────────

/** The table's two orders (UX-S3-16): newest first, as the devtools
 * panel's turn list ("turns, newest first"), with oldest first one
 * click away (?order=oldest); the timeline is a time axis, oldest at
 * its start. */
export const TURN_ORDER = {
  newest: {
    word: "newest first",
    title:
      "turns newest first — the devtools panel's turn list reads the same order",
  },
  oldest: {
    word: "oldest first",
    title: "turns in the order they ran, the first turn at the top",
  },
} as const

export type TurnOrder = keyof typeof TURN_ORDER

/** The table's caption: what it lists, in which order. */
export const turnsCaption = (o: TurnOrder) => `turns, ${TURN_ORDER[o].word}`

/** A turn's start (UX-B-19): the time of day to the millisecond is the
 * cell; this, its hover — the date, the zone and how long ago. */
export const startedTitle = (iso: string, now?: number) =>
  `${absoluteTime(iso)} · ${relativeTime(iso, now)}`

/** turnGap is the wait before a turn (UX-B-19): from the end of the
 * turn before it (its finish, else its last record) to this one's
 * start — "+0.5s waited for a decision" after a turn that parked,
 * "+12.0s after t2" otherwise, "started while t2 ran" when it began
 * first (a steer, a queued send). null for the first turn or a time
 * that does not parse. */
export function turnGap(
  prev: Pick<RunRow, "turn" | "finished" | "last_seen" | "pending"> | undefined,
  r: Pick<RunRow, "started">
): { text: string; title: string } | null {
  if (!prev) return null
  const end = Date.parse(prev.finished ?? prev.last_seen)
  const start = Date.parse(r.started)
  if (Number.isNaN(end) || Number.isNaN(start)) return null
  const t = prev.turn ? `t${prev.turn}` : "the turn before"
  if (start < end)
    return {
      text: `started while ${t} ran`,
      title: `began ${spanMs(end - start)} before ${t} ended (a steer or a queued send)`,
    }
  const gap = spanMs(start - end)
  return prev.pending > 0
    ? {
        text: `+${gap} waited for a decision`,
        title: `${t} parked; this turn started ${gap} after it — the park → decision latency`,
      }
    : {
        text: `+${gap} after ${t}`,
        title: `this turn started ${gap} after ${t} ended`,
      }
}

/** A turn link's accessible name (UX-B-40): "t3: Here is the photo
 * of the cracked dripper (succeeded)", the status's detail after its
 * word — never the 30-character run id. */
export function turnLinkLabel(
  r: { turn?: number; id: string },
  title: string,
  s: Pick<Status, "word" | "detail">
): string {
  const head = r.turn ? `t${r.turn}` : r.id
  return `${head}: ${title} (${s.word}${s.detail ? ` · ${s.detail}` : ""})`
}

/** The chip on a turn whose agent is not the session's (UX-B-20): the
 * agent column is gone, the header names the session's agent. */
export const otherAgentTitle = (agent: string, session: string) =>
  `this turn ran ${agent}, not the session's ${session || "agent"} (a handoff)`

/** STORAGE (UX-S3-21): the session's storage backend, durability and
 * writer lease live in the app's process — no record carries them.
 * The header reads the one metadata key the fix names, and says
 * not_recorded without it. */
export const STORAGE = {
  key: "storage",
  label: "storage",
  title: (v: string) =>
    `the app's own words for the session's storage (its turns' "storage" metadata, thread.WithRunMetadata): ${v}`,
  hole: {
    hole: "not_recorded",
    label: "storage · not recorded",
    reason:
      "the session's storage backend (jsonl · sqlite · memory), its durability (per step, thread.FsyncOnFlush, FsyncEveryAppend) and its writer lease are the app's process state: no record carries them",
    fix: 'stamp them on every turn: thread.WithRunMetadata(map[string]string{"storage": "jsonl · per-step durable"}) — an ErrLocked is diagnosed in docs/thread-operations.md (the lock)',
  },
} as const

/** storageOf is the newest turn's "storage" metadata, "" when none. */
export function storageOf(turns: readonly { meta?: Record<string, string> }[]) {
  for (let i = turns.length - 1; i >= 0; i--) {
    const v = turns[i].meta?.[STORAGE.key]
    if (v) return v
  }
  return ""
}

/** The session header's verbs (UX-B-29). */
export const SESSION_ACTIONS = {
  export: {
    label: "export jsonl",
    title:
      "download the conversation as JSON lines: a session line, every turn's run export in turn order (at most 200), then the session's state log and its gaps",
  },
  exported: (file: string) => `saved ${file}`,
  exportRefused: (m: string) => `export refused: ${m}`,
  copyPublic: "copy public id",
  copyPublicTitle:
    "copy the public id — the browser-safe handle the app and the devtools panel name this conversation by",
  delete: "delete session…",
  deleteTitle: (id: string) => `Delete session ${id}?`,
  deleteBody: (turns: number) =>
    `Its ${plural(turns, "turn")} — their spans, records, app logs, search index and the session's state log — are removed from studio's database only. The app's weft/thread store (the jsonl file or sqlite database the session lives in) is untouched: the conversation itself goes on. This cannot be undone.`,
  deleteAction: "delete session",
  deleted: (id: string) => `deleted session ${id}`,
  deleteRefused: (m: string) => `delete refused: ${m}`,
  cancel: "cancel",
} as const
