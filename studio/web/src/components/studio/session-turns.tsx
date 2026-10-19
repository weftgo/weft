// A session's turns (S4.7): the thread's runs, each a row, with the
// experiments forked from a turn nested under it — playground runs join
// their source on forked_from (lib/experiments). A row leads with "t3"
// and the turn's title (U6, lib/title.ts's turnTitle: its prompt, or
// "resume of t1 · 1 decided call" for a resume — the session id is the
// page's header), the run id a copyable line under it. U31.2: newest
// first by default, as the devtools panel's turn list (UX-S3-16; the
// caption names the order, ?order=oldest the other); the start to the
// millisecond with the wait before it — "+0.5s waited for a decision"
// after a park (UX-B-19) — and the turn's duration; its own cost
// (UX-B-18, capability "pricing"); no agent column — the header names
// the session's agent and a chip marks a turn that ran another
// (UX-B-20); each turn link named "turn 3: … (succeeded)" (UX-B-40).
// Words are lib/session-states.ts's.
import { Fragment } from "react"
import { Link } from "@tanstack/react-router"
import { FlaskConical } from "lucide-react"

import type { RunRow } from "@/lib/api"
import { forkSource } from "@/lib/experiments"
import { clockTime, duration, elapsed, tokens } from "@/lib/format"
import {
  CostCell,
  IdChip,
  RunTitle,
  StatusChip,
  TitleText,
  UnknownTokens,
} from "@/components/studio/runs-table"
import { useCostCapable } from "@/components/studio/cost"
import { DerivedChip } from "@/components/studio/hole-badge"
import { derivedUsageUnknown } from "@/lib/honesty"
import {
  Table,
  TableBody,
  TableCaption,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { runLink } from "@/lib/links"
import {
  otherAgentTitle,
  startedTitle,
  TURN_ORDER,
  turnGap,
  turnLinkLabel,
  turnsCaption,
} from "@/lib/session-states"
import type { TurnOrder } from "@/lib/session-states"
import { statusOf } from "@/lib/status"
import { runTitle, turnLabel, turnTitle } from "@/lib/title"

export function SessionTurns({
  turns,
  experiments,
  now,
  agent,
  order = "newest",
}: {
  /** The session's turns in the order they ran (oldest first). */
  turns: RunRow[]
  /** Source turn id → the playground runs forked from it. */
  experiments: Map<string, RunRow[]>
  now?: number
  /** The session's agent: a turn that ran another carries a chip. */
  agent?: string
  order?: TurnOrder
}) {
  const clock = now ?? Date.now()
  const costCapable = useCostCapable()
  const sessionAgent = agent ?? (turns.length ? turns[0].agent : "")
  // Titles and gaps read the turn before in run order — of the same
  // thread session: a conversation's runs may span several sessions,
  // and a gap between two sessions' turns means nothing. A run of no
  // session (a plain Generate on the public id) reads the public id's
  // run before it that has none either: that is its conversation.
  // The rows are drawn in the order asked.
  const lineOf = (r: RunRow) =>
    r.session_id ? `s:${r.session_id}` : r.public_id ? `p:${r.public_id}` : ""
  const rows = turns.map((r, i) => {
    let prev: RunRow | undefined
    const line = lineOf(r)
    for (let j = i - 1; line && j >= 0 && !prev; j--)
      if (lineOf(turns[j]) === line) prev = turns[j]
    return { r, prev }
  })
  if (order === "newest") rows.reverse()
  return (
    <Table>
      <TableCaption className="sr-only">{turnsCaption(order)}</TableCaption>
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead className="w-28">status</TableHead>
          <TableHead>turn</TableHead>
          <TableHead className="text-right">steps</TableHead>
          <TableHead className="text-right" title="input / output tokens">
            tokens in / out
          </TableHead>
          {costCapable ? (
            <TableHead className="text-right" title="the turn's own cost">
              cost
            </TableHead>
          ) : null}
          <TableHead className="hidden text-right sm:table-cell">
            started
          </TableHead>
          <TableHead className="hidden text-right sm:table-cell">
            duration
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map(({ r, prev }) => {
          const t = turnTitle(prev, r)
          const gap = turnGap(prev, r)
          const other = !!r.agent && !!sessionAgent && r.agent !== sessionAgent
          return (
            <Fragment key={r.id}>
              <TableRow className="h-9" data-turn={r.id}>
                <TableCell>
                  <StatusChip row={r} runs={turns} complete />
                </TableCell>
                <TableCell className="max-w-[min(24rem,60vw)]">
                  <RunTitle
                    run={r}
                    lead={turnLabel(r) || undefined}
                    t={t}
                    label={turnLinkLabel(r, t.text, statusOf(r, turns, true))}
                  />
                  <DerivedChip row={r} />
                  {other ? (
                    <span
                      className="mr-1 inline-block rounded-sm border px-1 font-mono text-[11px] text-muted-foreground"
                      title={otherAgentTitle(r.agent, sessionAgent)}
                      data-turn-agent={r.agent}
                    >
                      {r.agent}
                    </span>
                  ) : null}
                  {r.err ? (
                    <div
                      className="truncate font-mono text-[11px] text-status-bad/80"
                      title={r.err}
                    >
                      {r.err}
                    </div>
                  ) : null}
                </TableCell>
                <TableCell className="text-right font-mono tabular-nums">
                  {r.steps}
                </TableCell>
                <TableCell className="text-right font-mono tabular-nums">
                  {derivedUsageUnknown(r) ? (
                    <UnknownTokens reason={r.cost_reason} />
                  ) : (
                    <>
                      {tokens(r.usage.input_tokens)} /{" "}
                      {tokens(r.usage.output_tokens)}
                    </>
                  )}
                </TableCell>
                {costCapable ? (
                  <TableCell className="text-right">
                    <CostCell run={r} />
                  </TableCell>
                ) : null}
                <TableCell className="hidden text-right font-mono whitespace-nowrap tabular-nums sm:table-cell">
                  <span title={startedTitle(r.started, clock)} data-started>
                    {clockTime(r.started)}
                  </span>
                  {gap ? (
                    <div
                      className="text-[11px] text-faint"
                      title={gap.title}
                      data-turn-gap
                    >
                      {gap.text}
                    </div>
                  ) : null}
                </TableCell>
                <TableCell
                  className="hidden text-right font-mono whitespace-nowrap tabular-nums sm:table-cell"
                  data-duration
                >
                  {r.finished
                    ? duration(r.started, r.finished)
                    : r.status === "running"
                      ? elapsed(r.started, clock)
                      : "—"}
                </TableCell>
              </TableRow>
              {(experiments.get(r.id) ?? []).map((x) => {
                const src = forkSource(x)
                return (
                  <TableRow
                    key={x.id}
                    className="h-8 bg-secondary/30"
                    data-experiment-of={r.id}
                  >
                    <TableCell className="pl-6">
                      <StatusChip row={x} />
                    </TableCell>
                    <TableCell className="max-w-[min(24rem,60vw)]">
                      <span className="flex items-center gap-1.5 pl-3">
                        <FlaskConical
                          className="size-3 shrink-0 text-faint"
                          aria-label="experiment"
                        />
                        <Link
                          {...runLink(x.id)}
                          className="flex min-w-0 text-[12px] hover:text-thread-ink"
                          title={`an experiment forked from this turn (${x.forked_from})`}
                        >
                          <TitleText t={runTitle(x)} />
                        </Link>
                        <span className="shrink-0 font-mono text-[11px] text-faint">
                          {src && src.fromStep > 0
                            ? `from step ${src.fromStep}`
                            : "whole turn"}
                          {x.experiment_id ? ` · ${x.experiment_id}` : ""}
                          {x.session_id ? " · fork" : ""}
                          {x.model.name ? ` · ${x.model.name}` : ""}
                        </span>
                      </span>
                      <div className="pl-7">
                        <IdChip id={x.id} />
                      </div>
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {x.steps}
                    </TableCell>
                    <TableCell className="text-right font-mono tabular-nums">
                      {tokens(x.usage.input_tokens)} /{" "}
                      {tokens(x.usage.output_tokens)}
                    </TableCell>
                    {costCapable ? (
                      <TableCell className="text-right">
                        <CostCell run={x} />
                      </TableCell>
                    ) : null}
                    <TableCell
                      className="hidden text-right font-mono whitespace-nowrap tabular-nums sm:table-cell"
                      title={startedTitle(x.started, clock)}
                    >
                      {clockTime(x.started)}
                    </TableCell>
                    <TableCell className="hidden text-right font-mono whitespace-nowrap tabular-nums sm:table-cell">
                      {x.finished ? duration(x.started, x.finished) : "—"}
                    </TableCell>
                  </TableRow>
                )
              })}
            </Fragment>
          )
        })}
      </TableBody>
    </Table>
  )
}

/** OrderSwitch is the turn table's order (UX-S3-16): newest first —
 * the devtools panel's order — or oldest first, one press apart (the
 * session table and the conversation page alike). */
export function OrderSwitch({
  order,
  onChange,
}: {
  order: TurnOrder
  onChange: (o: TurnOrder) => void
}) {
  return (
    <div
      className="flex items-center gap-1 text-xs"
      role="group"
      aria-label="turn order"
      data-turn-order={order}
    >
      {(Object.keys(TURN_ORDER) as TurnOrder[]).map((o) => (
        <button
          key={o}
          type="button"
          aria-pressed={order === o}
          title={TURN_ORDER[o].title}
          onClick={() => onChange(o)}
          className={`min-h-6 rounded-sm border px-2 py-0.5 font-mono ${order === o ? "bg-secondary text-foreground" : "text-muted-foreground hover:text-foreground"}`}
        >
          {TURN_ORDER[o].word}
        </button>
      ))}
    </div>
  )
}
