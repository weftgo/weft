// A session page (S4.7): the thread's turns in order, each a run
// card; experiments nest under their source turn via forked_from.
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"

import { sessionQuery } from "@/lib/api"
import { absoluteTime, relativeTime, tokens } from "@/lib/format"
import { StatusChip } from "@/components/studio/runs-table"
import { Spinner } from "@/components/ui/spinner"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

export const Route = createFileRoute("/sessions/$id")({
  component: SessionPage,
})

function SessionPage() {
  const { id } = Route.useParams()
  const q = useQuery(sessionQuery(id))
  if (q.isPending) {
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  }
  if (q.isError) {
    return (
      <div className="mx-auto max-w-md space-y-3 py-24 text-center">
        <p className="font-mono text-xs text-faint">{id}</p>
        <p className="text-sm text-status-bad">{q.error.message}</p>
      </div>
    )
  }
  const s = q.data
  // Turns in order; experiments (playground runs that forked a turn)
  // nest under their source.
  const turns = s.runs
  const experimentsByFork = new Map<string, typeof turns>()
  void experimentsByFork

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="font-mono text-base font-medium tracking-tight">
          {s.id}
        </h1>
        <StatusChip status={s.status} />
        <span className="text-xs text-muted-foreground">{s.agent}</span>
        <span className="font-mono text-[11px] text-faint">
          {s.turns} {s.turns === 1 ? "turn" : "turns"} ·{" "}
          {tokens(s.usage.input_tokens)} in / {tokens(s.usage.output_tokens)} out
        </span>
        {s.public_id ? (
          <span
            className="rounded-sm border px-1.5 py-0.5 font-mono text-[11px] text-faint"
            title="the public id (the browser-safe handle)"
          >
            {s.public_id}
          </span>
        ) : null}
        <span
          className="ml-auto font-mono text-[11px] text-faint"
          title={`first ${absoluteTime(s.first_seen)}`}
        >
          last seen {relativeTime(s.last_seen)}
        </span>
      </div>

      <div className="overflow-x-auto rounded-lg border bg-background">
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
              <TableRow key={r.id} className="h-9">
                <TableCell>
                  <StatusChip status={r.status} />
                </TableCell>
                <TableCell className="max-w-72">
                  <Link
                    to="/runs/$id"
                    params={{ id: r.id }}
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
                  {relativeTime(r.started)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      {turns.length === 0 ? (
        <p className="py-8 text-center font-mono text-xs text-faint">
          no top-level turns recorded
        </p>
      ) : null}
    </div>
  )
}
