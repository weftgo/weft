// A session's state on the sessions list (U16; UX-B-15, UX-B-16): the
// newest turn's word — "⏸ awaiting a decision · 1 call" while it is an
// open park — the failed turns red and the busy-policy ends neutral
// beside it, and the outcome strip under it (lib/session-states.ts).
import type { SessionRow } from "@/lib/api"
import {
  AWAITING_TITLE,
  awaitingWords,
  cancelledWords,
  COUNT_TITLES,
  endedWords,
  failedWords,
  outcomeOf,
  stripLabel,
} from "@/lib/session-states"
import { statusClass } from "@/lib/status-class"
import { StatusChip } from "@/components/studio/runs-table"

/** The outcomes that are a run's own status word: the run chip says them. */
const RUN_WORDS: readonly string[] = [
  "succeeded",
  "failed",
  "running",
  "interrupted",
]

export function SessionState({ row }: { row: SessionRow }) {
  // The newest turn's outcome is the session page's word (a cancelled
  // turn reads cancelled, a busy-policy end ended, a decided park
  // decided) — the row's status is that outcome, the run words' chip
  // standing for the four a run reads alone.
  const last = row.outcomes?.at(-1) ?? row.status
  const o = RUN_WORDS.includes(last) ? undefined : outcomeOf(last)
  return (
    <div className="flex flex-wrap items-center gap-x-1.5 gap-y-0.5">
      {row.pending ? (
        <span
          className={`font-mono text-xs ${statusClass("parked").text}`}
          title={AWAITING_TITLE}
          data-status="awaiting"
        >
          {awaitingWords(row.pending)}
        </span>
      ) : o ? (
        <span
          className={`font-mono text-xs ${statusClass(o.role).text}`}
          title="the newest turn's outcome"
          data-status={last}
        >
          <span aria-hidden>{o.glyph} </span>
          {o.word}
        </span>
      ) : (
        <StatusChip row={row} />
      )}
      <SessionCounts row={row} />
    </div>
  )
}

/** SessionCounts: the failed turns red, the busy-policy ends and the
 * other cancelled turns apart and neutral; nothing when there are none. */
export function SessionCounts({
  row,
}: {
  row: Pick<SessionRow, "failed_turns" | "ended_turns" | "cancelled_turns">
}) {
  const failed = failedWords(row.failed_turns)
  const ended = endedWords(row.ended_turns)
  const cancelled = cancelledWords(row.cancelled_turns)
  return (
    <>
      {failed ? (
        <span
          className={`font-mono text-[11px] ${statusClass("failed").text}`}
          title={COUNT_TITLES.failed}
          data-session-failed={row.failed_turns}
        >
          {failed}
        </span>
      ) : null}
      {ended ? (
        <span
          className="font-mono text-[11px] text-muted-foreground"
          title={COUNT_TITLES.ended}
          data-session-ended={row.ended_turns}
        >
          {ended}
        </span>
      ) : null}
      {cancelled ? (
        <span
          className="font-mono text-[11px] text-muted-foreground"
          title={COUNT_TITLES.cancelled}
          data-session-cancelled={row.cancelled_turns}
        >
          {cancelled}
        </span>
      ) : null}
    </>
  )
}

/** OutcomeStrip is the newest turns' outcomes, oldest first, one glyph
 * each in its role's colour; one accessible name in words. */
export function OutcomeStrip({ row }: { row: SessionRow }) {
  const os = row.outcomes ?? []
  if (os.length < 2) return null
  return (
    <div
      className="flex max-w-40 flex-wrap font-mono text-[11px] leading-tight"
      role="img"
      aria-label={stripLabel(row)}
      data-session-strip={os.join(",")}
    >
      {row.turns > os.length ? (
        <span aria-hidden className="text-muted-foreground">
          …
        </span>
      ) : null}
      {os.map((o, i) => {
        const m = outcomeOf(o)
        return (
          <span
            key={i}
            aria-hidden
            title={`${m.word}`}
            className={statusClass(m.role).text}
          >
            {m.glyph}
          </span>
        )
      })}
    </div>
  )
}
