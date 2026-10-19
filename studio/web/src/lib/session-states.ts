// A session's state over time on the sessions list and the session
// header (U16; UX-B-15, UX-B-16, UX-B-28): the outcome strip, the
// "awaiting a decision" state, the failed and busy-ended counts, the
// exchanges, and the list's turn-state filters — the words and roles
// of obsdb.SessionTurnFacts' fields. Studio only: the devtools panel
// imports none of it (its turn chips are lib/turn-chips.ts's).
import type { SessionRow, TurnOutcome } from "./api"
import { plural } from "./format"
import type { Role } from "./palette"
import { AWAITING, STATUS } from "./status"
import { END_WORDS } from "./timeline"

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
  ended: { glyph: "✗", word: END_WORDS.byNewer, role: "resumed" },
  parked: { glyph: "⏸", word: AWAITING, role: "parked" },
  resumed: { glyph: "⏸", word: "parked · decided", role: "resumed" },
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
  return n ? `${n}✗ ${END_WORDS.interrupted}` : ""
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
    "turns a newer message ended on purpose (thread's Interrupt or Rollback busy policy): not failures",
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
      "sessions thread compacted (a compaction marker in the session's state log)",
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
