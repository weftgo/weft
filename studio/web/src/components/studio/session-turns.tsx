// A session's turns (S4.7): the thread's runs in order, each a row,
// with the experiments forked from a turn nested under it — playground
// runs join their source on forked_from (lib/experiments). A row leads
// with "t3" and the turn's title (U6, lib/title.ts's turnTitle: its
// prompt, or "resume of t1 · 1 decided call" for a resume — the
// session id is the page's header), the run id a copyable line under
// it. Narrow, the agent and started columns fold away.
import { Fragment } from "react"
import { Link } from "@tanstack/react-router"
import { FlaskConical } from "lucide-react"

import type { RunRow } from "@/lib/api"
import { forkSource } from "@/lib/experiments"
import { absoluteTime, relativeTime, tokens } from "@/lib/format"
import {
  IdChip,
  RunTitle,
  StatusChip,
  TitleText,
  UnknownTokens,
} from "@/components/studio/runs-table"
import { DerivedChip } from "@/components/studio/hole-badge"
import { derivedUsageUnknown } from "@/lib/honesty"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { runLink } from "@/lib/links"
import { runTitle, turnLabel, turnTitle } from "@/lib/title"

export function SessionTurns({
  turns,
  experiments,
  now,
}: {
  turns: RunRow[]
  /** Source turn id → the playground runs forked from it. */
  experiments: Map<string, RunRow[]>
  now?: number
}) {
  const clock = now ?? Date.now()
  return (
    <Table>
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead className="w-28">status</TableHead>
          <TableHead>turn</TableHead>
          <TableHead className="hidden sm:table-cell">agent</TableHead>
          <TableHead className="text-right">steps</TableHead>
          <TableHead className="text-right" title="input / output tokens">
            tokens in / out
          </TableHead>
          <TableHead className="hidden text-right sm:table-cell">
            started
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {turns.map((r, i) => (
          <Fragment key={r.id}>
            <TableRow className="h-9" data-turn={r.id}>
              <TableCell>
                <StatusChip row={r} runs={turns} complete />
              </TableCell>
              <TableCell className="max-w-[min(24rem,60vw)]">
                <RunTitle
                  run={r}
                  lead={turnLabel(r) || undefined}
                  t={turnTitle(turns[i - 1], r)}
                />
                <DerivedChip row={r} />
                {r.err ? (
                  <div
                    className="truncate font-mono text-[11px] text-status-bad/80"
                    title={r.err}
                  >
                    {r.err}
                  </div>
                ) : null}
              </TableCell>
              <TableCell className="hidden whitespace-nowrap sm:table-cell">
                {r.agent || <span className="text-faint">—</span>}
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
              <TableCell
                className="hidden text-right font-mono whitespace-nowrap tabular-nums sm:table-cell"
                title={absoluteTime(r.started)}
              >
                {relativeTime(r.started, clock)}
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
                      </span>
                    </span>
                    <div className="pl-7">
                      <IdChip id={x.id} />
                    </div>
                  </TableCell>
                  <TableCell className="hidden whitespace-nowrap text-muted-foreground sm:table-cell">
                    {x.model.name || x.agent || "—"}
                  </TableCell>
                  <TableCell className="text-right font-mono tabular-nums">
                    {x.steps}
                  </TableCell>
                  <TableCell className="text-right font-mono tabular-nums">
                    {tokens(x.usage.input_tokens)} /{" "}
                    {tokens(x.usage.output_tokens)}
                  </TableCell>
                  <TableCell
                    className="hidden text-right font-mono whitespace-nowrap tabular-nums sm:table-cell"
                    title={absoluteTime(x.started)}
                  >
                    {relativeTime(x.started, clock)}
                  </TableCell>
                </TableRow>
              )
            })}
          </Fragment>
        ))}
      </TableBody>
    </Table>
  )
}
