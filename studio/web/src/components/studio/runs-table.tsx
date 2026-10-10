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
import { forkSource } from "@/lib/experiments"
import { costView, delegatedView } from "@/lib/cost"
import { CacheHint, CostText } from "@/components/studio/cost"
import { runLink, sessionLink } from "@/lib/links"

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

export function StatusDot({ status, decorative }: { status: RunRow["status"]; decorative?: boolean }) {
  // Alone, the dot is an image named for the status (a bare span may not
  // carry aria-label: axe aria-prohibited-attr); beside the status word
  // it is decoration.
  return (
    <span
      title={status}
      {...(decorative ? { "aria-hidden": true } : { role: "img", "aria-label": status })}
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
      <StatusDot status={status} decorative />
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

/** The list's order (plan H1): started (newest first, the default),
 * duration, tokens or cost (most first). server is true when the page
 * sorts through /api/search?sort= (capability search), false when it
 * sorts the loaded rows only. */
export type RunSortKey = "started" | "duration" | "tokens" | "cost"
export interface RunsSort {
  active: RunSortKey
  server: boolean
  onSort: (key: RunSortKey) => void
}

export interface RunsTableProps {
  runs: RunRow[]
  selectedId?: string
  now?: number
  /** Draw the Cost column (capability "pricing"); absent, no cell. */
  showCost?: boolean
  /** The column sort; absent, the headers are plain. */
  sort?: RunsSort
}

/** CostCell is a run's own cost and, when its subagents cost anything
 * (or could not be priced), their delegated figure under it. */
function CostCell({ run }: { run: RunRow }) {
  const del = delegatedView(run)
  const showDel = !del.known || (del.usd ?? 0) > 0
  return (
    <div className="flex flex-col items-end">
      <CostText v={costView(run)} />
      {showDel ? (
        <span className="text-[11px] text-faint" data-delegated-cost>
          + <CostText v={del} /> delegated
        </span>
      ) : null}
      <CacheHint fields={run} />
    </div>
  )
}

const COST_SORT_SERVER =
  "sorted by Studio (/api/search?sort=cost): each run's whole bill, own + delegated, most first; unpriced runs last"
const COST_SORT_LOCAL =
  "sorts the loaded rows only (own + delegated, most first; unpriced last): this Studio cannot sort by cost itself (it needs the search capability) — load more to sort more"
const SORT_WORDS: Record<Exclude<RunSortKey, "cost">, { server: string; local: string }> = {
  started: { server: "newest first (the default order)", local: "newest first (the default order)" },
  duration: {
    server: "sorted by Studio (/api/search?sort=duration): longest first",
    local: "sorts the loaded rows only (longest first): this Studio cannot sort by duration itself (it needs the search capability) — load more to sort more",
  },
  tokens: {
    server: "sorted by Studio (/api/search?sort=tokens): input + output tokens, most first",
    local: "sorts the loaded rows only (input + output tokens, most first): this Studio cannot sort by tokens itself (it needs the search capability) — load more to sort more",
  },
}

/** SortHead is a sortable column's header: a toggle between its order
 * and newest first; aria-sort says which is on. */
function SortHead({
  sortKey,
  sort,
  label,
  title,
  className = "text-right",
}: {
  sortKey: RunSortKey
  sort?: RunsSort
  label: string
  title?: string
  className?: string
}) {
  if (!sort)
    return (
      <TableHead className={className} title={title}>
        {label}
      </TableHead>
    )
  const active = sort.active === sortKey
  const why =
    sortKey === "cost"
      ? sort.server
        ? COST_SORT_SERVER
        : COST_SORT_LOCAL
      : SORT_WORDS[sortKey][sort.server ? "server" : "local"]
  const toggle = sortKey === "started" || !active ? sortKey : "started"
  return (
    <TableHead className={className} aria-sort={active ? "descending" : "none"}>
      <button
        type="button"
        className={`font-medium hover:text-foreground ${active ? "text-foreground" : ""}`}
        title={active && sortKey !== "started" ? `${why} — click for newest first` : why}
        onClick={() => sort.onSort(toggle)}
        data-sort={sortKey}
        data-cost-sort={sortKey === "cost" ? (sort.server ? "server" : "page") : undefined}
        aria-pressed={active}
      >
        {label}
        {active ? " ↓" : ""}
        {active && sortKey !== "started" && !sort.server ? <span className="text-faint"> (page)</span> : null}
      </button>
    </TableHead>
  )
}

/** A replayed run's source step (plan F1): forked_from's run and
 * from_step — the step ordinal — linked literally. */
function ReplayChip({ run }: { run: Pick<RunRow, "forked_from"> }) {
  const src = forkSource(run)
  if (!src) return null
  return (
    <div className="truncate font-mono text-[11px] text-faint" data-replay-chip>
      ↻ replay of{" "}
      <Link
        {...runLink(src.runID, { step: src.fromStep })}
        className="text-muted-foreground hover:text-thread-ink hover:underline"
      >
        {src.runID} · step {src.fromStep}
      </Link>
    </div>
  )
}

export function RunsTable({ runs, selectedId, now, showCost, sort }: RunsTableProps) {
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
          <SortHead sortKey="tokens" sort={sort} label="tokens in / out" title="input / output tokens" />
          {showCost ? (
            <SortHead
              sortKey="cost"
              sort={sort}
              label="cost"
              title="the run's own cost, priced at read time; its subagents' apart"
            />
          ) : null}
          <SortHead sortKey="started" sort={sort} label="started" />
          <SortHead sortKey="duration" sort={sort} label="duration" />
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
                void navigate(runLink(run.id))
              }}
            >
              <TableCell>
                <StatusChip status={run.status} />
              </TableCell>
              <TableCell className="max-w-64">
                <Link
                  {...runLink(run.id)}
                  className="flex items-center gap-1 font-mono text-[13px] hover:text-thread-ink"
                >
                  <span className="truncate" title={run.id}>
                    {run.id}
                  </span>
                  <CopyId id={run.id} />
                </Link>
                {run.parent_run_id ? (
                  <div
                    className="flex items-center gap-1 truncate font-mono text-[11px] text-faint"
                    data-parent-link
                  >
                    ↳ subagent of
                    <Link
                      {...runLink(run.parent_run_id)}
                      className="truncate text-muted-foreground hover:text-thread-ink hover:underline"
                      title={`the parent run (call ${run.parent_call_id})`}
                    >
                      {run.parent_run_id}
                    </Link>
                  </div>
                ) : null}
                <ReplayChip run={run} />
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
                    {...sessionLink(run.session_id)}
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
              {showCost ? (
                <TableCell className="text-right whitespace-nowrap" data-cost-cell>
                  <CostCell run={run} />
                </TableCell>
              ) : null}
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
