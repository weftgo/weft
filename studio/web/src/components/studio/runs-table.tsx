// The runs list (A1): status, id, agent, model, steps, tokens,
// started, duration. Interrupted rows are shown, never hidden — a
// crash leaves evidence. Status is a dot AND a word (colour alone is
// not a signal); a failed row carries its error under the id, so the
// list already answers "what broke". Numbers are tabular mono; ids
// are mono and copyable; the whole row opens the run. The running dot
// pulses once per 2 s (reduced motion stops it, styles.css).
import { Check, Copy } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { Link, useNavigate } from "@tanstack/react-router"

import type { RunRow } from "@/lib/api"
import {
  absoluteTime,
  duration,
  elapsed,
  relativeTime,
  tokens,
} from "@/lib/format"
import { copyText } from "@/lib/json"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  HoverCard,
  HoverCardContent,
  HoverCardTrigger,
} from "@/components/ui/hover-card"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

const statusColor: Record<RunRow["status"], string> = {
  running: "bg-status-run",
  succeeded: "bg-status-ok",
  failed: "bg-status-bad",
  interrupted: "bg-status-int",
}
const statusText: Record<RunRow["status"], string> = {
  running: "text-status-run",
  succeeded: "text-status-ok",
  failed: "text-status-bad",
  interrupted: "text-status-int",
}
const statusHint: Record<RunRow["status"], string> = {
  running: "the recorder's heartbeat is fresh",
  succeeded: "finished with a result",
  failed: "the loop returned an error",
  interrupted:
    "stored as running, but the heartbeat went stale — a crash or kill",
}

export function StatusDot({ status }: { status: RunRow["status"] }) {
  return (
    <span
      title={status}
      aria-label={status}
      className={`inline-block size-2 shrink-0 rounded-full ${statusColor[status]} ${status === "running" ? "pulse-dot" : ""}`}
    />
  )
}

/** Dot + word. dotOnly keeps just the dot (with a title) for tight spots. */
export function StatusChip({
  status,
  dotOnly,
}: {
  status: RunRow["status"]
  dotOnly?: boolean
}) {
  if (dotOnly) return <StatusDot status={status} />
  return (
    <span
      className={`inline-flex items-center gap-1.5 font-mono text-xs ${statusText[status]}`}
      title={statusHint[status]}
    >
      <StatusDot status={status} />
      {status}
    </span>
  )
}

function CopyId({ id }: { id: string }) {
  const [done, setDone] = useState(false)
  return (
    <Button
      variant="ghost"
      size="icon-xs"
      className="opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
      aria-label={`copy run id ${id}`}
      title="copy run id"
      onClick={(e) => {
        e.preventDefault()
        e.stopPropagation()
        void copyText(id).then((ok) => {
          if (!ok) return
          setDone(true)
          setTimeout(() => setDone(false), 1200)
        })
      }}
    >
      {done ? <Check data-slot="icon" /> : <Copy data-slot="icon" />}
    </Button>
  )
}

function TokensCell({ usage }: { usage: RunRow["usage"] }) {
  const splits =
    usage.cached_input_tokens ||
    usage.cache_write_tokens ||
    usage.reasoning_tokens
  const cell = (
    <span
      className="font-mono tabular-nums"
      title={`${usage.input_tokens.toLocaleString()} in / ${usage.output_tokens.toLocaleString()} out`}
    >
      {tokens(usage.input_tokens)} / {tokens(usage.output_tokens)}
    </span>
  )
  if (!splits) return cell
  return (
    <HoverCard>
      <HoverCardTrigger className="underline decoration-dotted underline-offset-4">
        {cell}
      </HoverCardTrigger>
      <HoverCardContent className="w-56 font-mono text-xs">
        <div className="grid gap-0.5 tabular-nums">
          <span>input {usage.input_tokens.toLocaleString()}</span>
          {usage.cached_input_tokens ? (
            <span className="text-muted-foreground">
              ├ cached {usage.cached_input_tokens.toLocaleString()}
            </span>
          ) : null}
          {usage.cache_write_tokens ? (
            <span className="text-muted-foreground">
              ├ cache write {usage.cache_write_tokens.toLocaleString()}
            </span>
          ) : null}
          <span>output {usage.output_tokens.toLocaleString()}</span>
          {usage.reasoning_tokens ? (
            <span className="text-muted-foreground">
              ├ reasoning {usage.reasoning_tokens.toLocaleString()}
            </span>
          ) : null}
        </div>
      </HoverCardContent>
    </HoverCard>
  )
}

export interface RunsTableProps {
  runs: RunRow[]
  selectedId?: string
  now?: number
}

export function RunsTable({ runs, selectedId, now }: RunsTableProps) {
  const clock = now ?? Date.now()
  const navigate = useNavigate()
  const selectedRow = useRef<HTMLTableRowElement>(null)
  // j/k keeps the selected row in view.
  useEffect(() => {
    selectedRow.current?.scrollIntoView({ block: "nearest" })
  }, [selectedId])
  return (
    <Table>
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead className="w-28">status</TableHead>
          <TableHead>run</TableHead>
          <TableHead>agent</TableHead>
          <TableHead title="the thread this run is a turn of">session</TableHead>
          <TableHead>model</TableHead>
          <TableHead className="text-right">steps</TableHead>
          <TableHead className="text-right" title="input / output tokens">
            tokens in / out
          </TableHead>
          <TableHead className="text-right">started</TableHead>
          <TableHead className="text-right">duration</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {runs.map((run) => {
          const selected = selectedId === run.id
          return (
            <TableRow
              key={run.id}
              ref={selected ? selectedRow : undefined}
              data-selected={selected ? "" : undefined}
              className={`group h-9 cursor-pointer ${
                selected ? "bg-accent shadow-[inset_2px_0_0_var(--thread)]" : ""
              }`}
              onClick={(e) => {
                // A click anywhere on the row opens the run; links and
                // buttons inside keep their own behaviour (middle-click,
                // copy).
                if ((e.target as HTMLElement).closest("a,button")) return
                void navigate({ to: "/runs/$id", params: { id: run.id } })
              }}
            >
              <TableCell>
                <StatusChip status={run.status} />
              </TableCell>
              <TableCell className="max-w-64">
                <Link
                  to="/runs/$id"
                  params={{ id: run.id }}
                  className="flex items-center gap-1 font-mono text-[13px] hover:text-thread-ink"
                >
                  <span className="truncate" title={run.id}>
                    {run.id}
                  </span>
                  <CopyId id={run.id} />
                </Link>
                {run.err ? (
                  <div
                    className="truncate font-mono text-[11px] text-status-bad/80"
                    title={run.err}
                  >
                    {run.err}
                  </div>
                ) : null}
              </TableCell>
              <TableCell className="whitespace-nowrap">
                {run.agent || <span className="text-faint">—</span>}
              </TableCell>
              <TableCell className="whitespace-nowrap">
                {run.session_id ? (
                  <Link
                    to="/sessions/$id"
                    params={{ id: run.session_id }}
                    className="font-mono text-[11px] text-muted-foreground hover:text-thread-ink hover:underline"
                    title={`the session (${run.public_id || "no public id"})`}
                  >
                    {run.session_id}
                    {run.turn ? ` ·t${run.turn}` : ""}
                  </Link>
                ) : (
                  <span className="text-faint">—</span>
                )}
              </TableCell>
              <TableCell>
                <Badge
                  variant="outline"
                  className="font-mono text-[11px] font-normal"
                >
                  {run.model.provider}/{run.model.name}
                </Badge>
              </TableCell>
              <TableCell className="text-right font-mono tabular-nums">
                {run.steps}
              </TableCell>
              <TableCell className="text-right">
                <TokensCell usage={run.usage} />
              </TableCell>
              <TableCell
                className="text-right font-mono whitespace-nowrap tabular-nums"
                title={absoluteTime(run.started)}
              >
                {relativeTime(run.started, clock)}
              </TableCell>
              <TableCell className="text-right font-mono whitespace-nowrap tabular-nums">
                {run.status === "running" ? (
                  <span className="text-status-run" title="elapsed so far">
                    {elapsed(run.started, clock)}…
                  </span>
                ) : (
                  duration(run.started, run.finished)
                )}
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}
