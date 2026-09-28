// The runs list (A1): id, agent, model, steps, tokens, status,
// started, duration. Interrupted rows are shown, never hidden — a
// crash leaves evidence. Numbers are tabular mono; ids are mono and
// copyable. The running dot pulses once per 2 s (reduced motion stops
// it, styles.css).
import { Check, Copy } from "lucide-react"
import { useState } from "react"
import { Link } from "@tanstack/react-router"

import type { RunRow } from "@/lib/api"
import { absoluteTime, duration, relativeTime, tokens } from "@/lib/format"
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

export function StatusDot({ status }: { status: RunRow["status"] }) {
  return (
    <span
      title={status}
      aria-label={status}
      className={`inline-block size-2 rounded-full ${statusColor[status]} ${status === "running" ? "pulse-dot" : ""}`}
    />
  )
}

function CopyId({ id }: { id: string }) {
  const [done, setDone] = useState(false)
  return (
    <Button
      variant="ghost"
      size="icon-xs"
      className="opacity-0 group-hover:opacity-100"
      aria-label={`copy run id ${id}`}
      onClick={(e) => {
        e.preventDefault()
        try {
          void navigator.clipboard.writeText(id)
          setDone(true)
          setTimeout(() => setDone(false), 1200)
        } catch {
          // clipboard unavailable — the id is selectable text anyway
        }
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
    <span className="font-mono tabular-nums">
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
  return (
    <Table>
      <TableHeader>
        <TableRow className="hover:bg-transparent">
          <TableHead className="w-6" aria-label="status" />
          <TableHead>run</TableHead>
          <TableHead>agent</TableHead>
          <TableHead>model</TableHead>
          <TableHead className="text-right">steps</TableHead>
          <TableHead className="text-right">tokens</TableHead>
          <TableHead className="text-right">started</TableHead>
          <TableHead className="text-right">dur</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {runs.map((run) => (
          <TableRow
            key={run.id}
            className={`group h-9 ${selectedId === run.id ? "bg-accent" : ""}`}
          >
            <TableCell>
              <StatusDot status={run.status} />
            </TableCell>
            <TableCell className="max-w-40">
              <Link
                to="/runs/$id"
                params={{ id: run.id }}
                className="flex items-center gap-1 font-mono text-[13px] hover:text-thread-ink"
              >
                <span className="truncate">{run.id}</span>
                <CopyId id={run.id} />
              </Link>
            </TableCell>
            <TableCell className="whitespace-nowrap">
              {run.agent || "—"}
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
              className="text-right font-mono tabular-nums"
              title={absoluteTime(run.started)}
            >
              {relativeTime(run.started, clock)}
            </TableCell>
            <TableCell className="text-right font-mono tabular-nums">
              {duration(run.started, run.finished)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
