// The Live page's table (plan H6): one row per run the page knows —
// streamed (the all stream's rows, lib/live-rows) or polled (the 5 s
// fallback) — with the run's current step, tokens so far, cost (A5.2's
// cell: "—" with the reason while running), TTFT, started/elapsed and
// session, a filter over agent and status, a peek (the row's story in
// the side pane) and, under capability "cancel", the cancel verb:
// runtime-started (playground) runs only, an inline confirm (a second
// click within CANCEL_CONFIRM_MS — no window.confirm), "cancelling…"
// until the run's end arrives (looked for over every row, a filtered-out
// one included) or CANCEL_WAIT_MS pass without it — then the button is
// back, "no end seen — cancel again" (a cancel is best effort) — and the
// server's refusal toasted in its own words. The rows come in tree
// order (lib/live-rows' rowsOf): a subagent is indented under its
// parent (H9, UX-A-18); the step cell carries the run's newest attempt
// frame when it says something (lib/live-attempts' attemptNote: a
// failed try while the run still runs reads retrying); the session cell
// is the session's id, its public id a quieter line under it.
import { useEffect, useState } from "react"
import { Link } from "@tanstack/react-router"

import { ApiError, postCancel } from "@/lib/api"
import {
  duration,
  elapsed,
  relativeTime,
  tokens as fmtTokens,
} from "@/lib/format"
import {
  CANCEL_CONFIRM_MS,
  CANCEL_WAIT_MS,
  costFieldsOf,
  ended,
  stepText,
  tokensOf,
} from "@/lib/live-rows"
import type { LiveRowView } from "@/lib/live-rows"
import { attemptNote } from "@/lib/live-attempts"
import { runLink, sessionLink } from "@/lib/links"
import { shortId } from "@/lib/title"
import { notify } from "@/lib/notify"
import { STATUS, statusOf } from "@/lib/status"
import { statusClass } from "@/lib/status-class"
import { Cost, useCostCapable } from "@/components/studio/cost"
import { DerivedChip, HoleBadge } from "@/components/studio/hole-badge"
import { StatusChip, UnknownTokens } from "@/components/studio/runs-table"
import { APP_CANCEL, derivedUsageUnknown } from "@/lib/honesty"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/** The disabled cancel's words on an app-owned run (plan F4.3: the runtime refuses it). */
export const APP_RUN_CANCEL = `the app's own run: ${APP_CANCEL}`

/** The ready button's title once a sent cancel saw no end in
 * CANCEL_WAIT_MS. */
export const CANCEL_AGAIN = "no end seen — cancel again"

let presses = 0

function CancelCell({
  run,
  runs,
  cancelling,
  stale,
  onCancelling,
}: {
  run: LiveRowView["run"]
  /** The held rows: where a park's resume is looked for. */
  runs: LiveRowView["run"][]
  cancelling: boolean
  /** A sent cancel saw no end in CANCEL_WAIT_MS. */
  stale: boolean
  onCancelling: (id: string, on: boolean) => void
}) {
  const [armed, setArmed] = useState(false)
  const [posting, setPosting] = useState(false)
  useEffect(() => {
    if (!armed) return
    const t = setTimeout(() => setArmed(false), CANCEL_CONFIRM_MS)
    return () => clearTimeout(t)
  }, [armed])

  // Parked while nothing is known to have resumed it (U1-fix): a
  // decided park is over like any ended run.
  if (statusOf(run, runs).key === "parked")
    return (
      <Link
        {...runLink(run.id)}
        className={`font-mono text-[11px] whitespace-nowrap ${statusClass(STATUS.parked.role).text} underline-offset-2 hover:underline`}
        title="a parked run is over until its pending calls are decided: approve or deny on its page"
      >
        parked — decide instead
      </Link>
    )
  if (run.status !== "running")
    return <span className="font-mono text-[11px] text-faint">—</span>
  if (!run.playground)
    return (
      <Button
        variant="outline"
        size="xs"
        disabled
        title={APP_RUN_CANCEL}
        aria-label={`cancel run ${run.id} — ${APP_RUN_CANCEL}`}
        data-cancel="app"
      >
        cancel
      </Button>
    )
  if (cancelling || posting)
    return (
      <Button
        variant="outline"
        size="xs"
        disabled
        aria-label={`cancelling… run ${run.id}`}
        data-cancel="cancelling"
      >
        cancelling…
      </Button>
    )
  const press = () => {
    if (!armed) {
      setArmed(true)
      return
    }
    setArmed(false)
    setPosting(true)
    const n = ++presses
    postCancel(run.id).then(
      () => {
        setPosting(false)
        onCancelling(run.id, true)
        notify({ kind: "cancel", runID: run.id, n, outcome: "sent" })
      },
      (e: unknown) => {
        setPosting(false)
        onCancelling(run.id, false)
        notify({
          kind: "cancel",
          runID: run.id,
          n,
          outcome: "refused",
          // The server's words, verbatim (S4.2's error.message).
          message:
            e instanceof ApiError || e instanceof Error ? e.message : String(e),
        })
      }
    )
  }
  return (
    <Button
      variant={armed ? "destructive" : "outline"}
      size="xs"
      onClick={press}
      aria-label={
        armed ? `confirm cancel run ${run.id}` : `cancel run ${run.id}`
      }
      title={
        armed
          ? "click again to cancel this run"
          : stale
            ? CANCEL_AGAIN
            : "ask the runtime that started this run to stop it"
      }
      data-cancel={armed ? "armed" : "ready"}
    >
      {armed ? "confirm cancel" : "cancel"}
    </Button>
  )
}

export function LiveTable({
  rows,
  allRows,
  now,
  peek,
  onPeek,
  canCancel,
}: {
  rows: LiveRowView[]
  /** Every row the page holds, the filter aside: where a cancelled
   * run's end is looked for (default: rows). */
  allRows?: LiveRowView[]
  /** The clock the elapsed cells read (the page ticks it). */
  now: number
  /** The peeked run id, if any. */
  peek?: string
  onPeek: (id: string | undefined) => void
  /** Capability "cancel": the cancel column is drawn. */
  canCancel: boolean
}) {
  const priced = useCostCapable()
  // A sent cancel, by run: when it was sent (Date.now()).
  const [cancelling, setCancelling] = useState<Partial<Record<string, number>>>(
    {}
  )
  const onCancelling = (id: string, on: boolean) =>
    setCancelling((cur) => {
      if (!on && cur[id] === undefined) return cur
      const next = { ...cur }
      if (on) next[id] = Date.now()
      else delete next[id]
      return next
    })
  // A cancelled run's end is its row ending — failed, or another end
  // the cancel raced: the cell lets go, and the toast says how it ended.
  // Every row is looked at, not the filtered ones only.
  const watched = allRows ?? rows
  // Where a parked row's resume is looked for (U1's statusOf).
  const runsAll = watched.map((v) => v.run)
  useEffect(() => {
    for (const r of watched) {
      if (cancelling[r.run.id] !== undefined && ended(r.run)) {
        onCancelling(r.run.id, false)
        notify({
          kind: "cancel",
          runID: r.run.id,
          n: ++presses,
          outcome: "ended",
          status: statusOf(r.run, runsAll).word,
        })
      }
    }
  }, [watched, cancelling])
  const waiting = (id: string) => {
    const at = cancelling[id]
    return at !== undefined && now - at < CANCEL_WAIT_MS
  }
  const staleCancel = (id: string) => {
    const at = cancelling[id]
    return at !== undefined && now - at >= CANCEL_WAIT_MS
  }

  return (
    <Table aria-label="runs streaming now">
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead>run</TableHead>
          <TableHead>agent</TableHead>
          <TableHead className="w-28">status</TableHead>
          <TableHead
            className="text-right"
            title="the current step (and the total once the run ended)"
          >
            step
          </TableHead>
          <TableHead
            className="text-right"
            title="input + output tokens so far"
          >
            tokens
          </TableHead>
          {priced ? (
            <TableHead
              className="text-right"
              title="the run's own cost: unknown while running (usage arrives at run end)"
            >
              cost
            </TableHead>
          ) : null}
          <TableHead
            className="text-right"
            title="time to first token of the last finished step"
          >
            TTFT
          </TableHead>
          <TableHead>started</TableHead>
          <TableHead title="the conversation this run is a turn of (its public id under it)">
            session
          </TableHead>
          <TableHead>
            <span className="sr-only">actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((v) => {
          const r = v.run
          const step = stepText(v)
          const tok = tokensOf(v)
          const ttft = v.stats?.ttftMs ?? null
          const note = attemptNote(v.attempt, !ended(r), v.stats?.step)
          const peeked = peek === r.id
          return (
            <TableRow
              key={r.id}
              data-run={r.id}
              data-selected={peeked ? "" : undefined}
              className={`group cursor-pointer ${peeked ? "bg-accent shadow-[inset_2px_0_0_var(--thread)]" : ""} ${
                v.endedAt !== null ? "opacity-70" : ""
              }`}
              onClick={(e) => {
                // A click on the row peeks; its links and buttons keep
                // their own behaviour.
                if ((e.target as HTMLElement).closest("a,button,input")) return
                onPeek(peeked ? undefined : r.id)
              }}
            >
              <TableCell
                className="max-w-56"
                data-depth={v.depth}
                style={
                  v.depth ? { paddingLeft: `${0.5 + v.depth * 1.25}rem` } : {}
                }
              >
                <div className="flex flex-wrap items-center gap-1.5">
                  {v.depth ? (
                    <span
                      className="font-mono text-[11px] text-faint"
                      title={`a child run of ${r.parent_run_id}`}
                      aria-label="child run of the row above"
                    >
                      ↳
                    </span>
                  ) : null}
                  <Link
                    {...runLink(r.id)}
                    className="truncate font-mono text-[13px] hover:text-thread-ink"
                  >
                    {r.id}
                  </Link>
                  <DerivedChip row={r} />
                  {v.unreadable.length ? (
                    <HoleBadge
                      hole="gap"
                      label="could not read"
                      reason={`a reconnect's backfill could not read this run's stored ${v.unreadable.join(", ")} records (Studio's log has the cause): its figures here may be incomplete`}
                      fix="open the run's page: it reads the run from the API"
                    />
                  ) : null}
                </div>
              </TableCell>
              <TableCell className="text-xs">{r.agent || "—"}</TableCell>
              <TableCell>
                <StatusChip row={r} runs={runsAll} />
              </TableCell>
              <TableCell
                className="text-right font-mono text-xs tabular-nums"
                title={step.title}
                data-cell="step"
              >
                {step.text}
                {note ? (
                  <div
                    className={`text-[11px] whitespace-nowrap ${note.retrying ? statusClass(STATUS.interrupted.role).text : "text-faint"}`}
                    title={note.title}
                    data-attempt={v.attempt?.attempt}
                    data-retrying={note.retrying ? "" : undefined}
                  >
                    {note.text}
                  </div>
                ) : null}
              </TableCell>
              <TableCell
                className="text-right font-mono text-xs tabular-nums"
                data-cell="tokens"
                title={
                  tok
                    ? `${tok.input.toLocaleString()} in · ${tok.output.toLocaleString()} out${
                        tok.partial
                          ? " · counted from the steps this page saw (it joined the run mid-way)"
                          : ""
                      }`
                    : "no step has reported usage yet"
                }
              >
                {derivedUsageUnknown(r) ? (
                  <UnknownTokens reason={r.cost_reason} />
                ) : tok ? (
                  `${tok.partial ? "≥" : ""}${fmtTokens(tok.n)}`
                ) : (
                  "—"
                )}
              </TableCell>
              {priced ? (
                <TableCell className="text-right text-xs" data-cell="cost">
                  <Cost fields={costFieldsOf(r)} />
                </TableCell>
              ) : null}
              <TableCell
                className="text-right font-mono text-xs tabular-nums"
                data-cell="ttft"
                title={
                  ttft !== null
                    ? "the last finished step's time to first token"
                    : "no step_finish with a TTFT seen yet"
                }
              >
                {ttft !== null ? `${ttft.toLocaleString()} ms` : "—"}
              </TableCell>
              <TableCell
                className="font-mono text-[11px] whitespace-nowrap text-muted-foreground"
                title={r.started}
              >
                {relativeTime(r.started, now)} ·{" "}
                {r.status === "running"
                  ? elapsed(r.started, now)
                  : duration(r.started, r.finished)}
              </TableCell>
              <TableCell className="max-w-40 truncate font-mono text-[11px]">
                {r.session_id ? (
                  <Link
                    {...sessionLink(r.session_id)}
                    className="hover:text-thread-ink"
                    title={`the session ${r.session_id}`}
                    data-session-cell
                  >
                    {shortId(r.session_id)}
                  </Link>
                ) : (
                  <span className="text-faint">—</span>
                )}
                {r.public_id ? (
                  <div
                    className="truncate text-faint"
                    title={`public id ${r.public_id}`}
                    data-public-id-cell
                  >
                    {r.public_id}
                  </div>
                ) : null}
              </TableCell>
              <TableCell>
                <div className="flex items-center justify-end gap-1.5">
                  <Button
                    variant="ghost"
                    size="xs"
                    aria-pressed={peeked}
                    aria-label={`peek run ${r.id}`}
                    title="open this run's story beside the list"
                    onClick={() => onPeek(peeked ? undefined : r.id)}
                  >
                    peek
                  </Button>
                  {canCancel ? (
                    <CancelCell
                      run={r}
                      runs={runsAll}
                      cancelling={waiting(r.id)}
                      stale={staleCancel(r.id)}
                      onCancelling={onCancelling}
                    />
                  ) : null}
                </div>
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}
