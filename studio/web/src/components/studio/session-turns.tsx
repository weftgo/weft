// A session's turns (S4.7): the thread's runs in order, each a row,
// with the experiments forked from a turn nested under it — playground
// runs join their source on forked_from (lib/experiments).
import { Fragment } from "react"
import { Link } from "@tanstack/react-router"
import { FlaskConical } from "lucide-react"

import type { RunRow } from "@/lib/api"
import { forkSource } from "@/lib/experiments"
import { absoluteTime, relativeTime, tokens } from "@/lib/format"
import { StatusChip } from "@/components/studio/runs-table"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { runLink } from "@/lib/links"

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
          <TableHead>agent</TableHead>
          <TableHead className="text-right">steps</TableHead>
          <TableHead className="text-right" title="input / output tokens">
            tokens in / out
          </TableHead>
          <TableHead className="text-right">started</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {turns.map((r) => (
          <Fragment key={r.id}>
            <TableRow className="h-9" data-turn={r.id}>
              <TableCell>
                <StatusChip status={r.status} />
              </TableCell>
              <TableCell className="max-w-72">
                <Link
                  {...runLink(r.id)}
                  className="font-mono text-[13px] hover:text-thread-ink"
                >
                  <span className="truncate">{r.id}</span>
                </Link>
                {r.err ? (
                  <div
                    className="truncate font-mono text-[11px] text-status-bad/80"
                    title={r.err}
                  >
                    {r.err}
                  </div>
                ) : null}
              </TableCell>
              <TableCell className="whitespace-nowrap">
                {r.agent || <span className="text-faint">—</span>}
              </TableCell>
              <TableCell className="text-right font-mono tabular-nums">
                {r.steps}
              </TableCell>
              <TableCell className="text-right font-mono tabular-nums">
                {tokens(r.usage.input_tokens)} / {tokens(r.usage.output_tokens)}
              </TableCell>
              <TableCell
                className="text-right font-mono whitespace-nowrap tabular-nums"
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
                    <StatusChip status={x.status} />
                  </TableCell>
                  <TableCell className="max-w-72">
                    <span className="flex items-center gap-1.5 pl-3">
                      <FlaskConical
                        className="size-3 shrink-0 text-faint"
                        aria-label="experiment"
                      />
                      <Link
                        {...runLink(x.id)}
                        className="truncate font-mono text-[12px] hover:text-thread-ink"
                        title={`an experiment forked from this turn (${x.forked_from})`}
                      >
                        {x.id}
                      </Link>
                      <span className="shrink-0 font-mono text-[11px] text-faint">
                        {src && src.fromStep > 0
                          ? `from step ${src.fromStep}`
                          : "whole turn"}
                        {x.experiment_id ? ` · ${x.experiment_id}` : ""}
                        {x.session_id ? " · fork" : ""}
                      </span>
                    </span>
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {x.model.name || x.agent || "—"}
                  </TableCell>
                  <TableCell className="text-right font-mono tabular-nums">
                    {x.steps}
                  </TableCell>
                  <TableCell className="text-right font-mono tabular-nums">
                    {tokens(x.usage.input_tokens)} / {tokens(x.usage.output_tokens)}
                  </TableCell>
                  <TableCell
                    className="text-right font-mono whitespace-nowrap tabular-nums"
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
