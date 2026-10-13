// The runs list (A1): status, run, agent, model, steps, tokens,
// started, duration. Interrupted rows are shown, never hidden — a
// crash leaves evidence. Status is a dot AND a word (colour alone is
// not a signal). The run cell leads with its title — the prompt
// (lib/title.ts's runTitle, U6) — the id a mono, copyable line under
// it; a failed row carries its error below, so the list already
// answers "what broke". Numbers are tabular mono; the whole row opens
// the run. The running dot pulses once per 2 s (reduced motion stops
// it, styles.css).
import { Check, Copy } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { Link, useNavigate } from "@tanstack/react-router"

import type { RunRow } from "@/lib/api"
import { stoppedByWords } from "@/lib/stopped-by"
import { ProblemBadges } from "@/components/studio/problem-badges"
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
import { DerivedChip } from "@/components/studio/hole-badge"
import { derivedUsageUnknown } from "@/lib/honesty"
import { runLink, sessionLink } from "@/lib/links"
import { statusClass } from "@/lib/status-class"
import { statusOf } from "@/lib/status"
import type { Status, StatusRow } from "@/lib/status"
import { runTitle, shortId } from "@/lib/title"
import type { Title } from "@/lib/title"

const roleClass = (s: Status) => statusClass(s.role)

export function StatusDot({
  row,
  runs,
  complete,
  decorative,
}: {
  row: StatusRow
  runs?: readonly StatusRow[] | null
  complete?: boolean
  decorative?: boolean
}) {
  const s = statusOf(row, runs, complete)
  // Alone, the dot is an image named for the status (a bare span may not
  // carry aria-label: axe aria-prohibited-attr); beside the status word
  // it is decoration.
  return (
    <span
      title={s.word}
      {...(decorative
        ? { "aria-hidden": true }
        : { role: "img", "aria-label": s.word })}
      className={`inline-block size-2 shrink-0 rounded-full ${roleClass(s).dot} ${s.key === "running" ? "pulse-dot" : ""}`}
    />
  )
}

/** Dot + word, in lib/status.ts's words (U1): parked while nothing is
 * known to have resumed the calls, "parked → tN" — a link to the
 * resume — once a turn in runs did, cancelled for a cancelled failure.
 * complete: runs is the row's session's whole turn list (statusOf).
 * dotOnly keeps just the dot (with a title) for tight spots; detail
 * adds the header's "· 1 call awaiting a decision" (complete) or
 * "· 1 call pending · no resume recorded here". */
export function StatusChip({
  row,
  runs,
  complete,
  dotOnly,
  detail,
}: {
  row: StatusRow
  runs?: readonly StatusRow[] | null
  complete?: boolean
  dotOnly?: boolean
  detail?: boolean
}) {
  if (dotOnly) return <StatusDot row={row} runs={runs} complete={complete} />
  const s = statusOf(row, runs, complete)
  const word = s.resumedBy ? (
    <Link
      {...runLink(s.resumedBy)}
      className="hover:text-thread-ink hover:underline"
      data-resumed-by={s.resumedBy}
    >
      {s.word}
    </Link>
  ) : (
    s.word
  )
  return (
    <span
      className={`inline-flex items-center gap-1.5 font-mono text-xs ${roleClass(s).text}`}
      title={s.hint}
      data-status={s.key}
    >
      <StatusDot row={row} runs={runs} complete={complete} decorative />
      {word}
      {detail && s.detail ? (
        <span className="text-muted-foreground">· {s.detail}</span>
      ) : null}
    </span>
  )
}

export function CopyId({ id, noun = "run id" }: { id: string; noun?: string }) {
  const [done, setDone] = useState(false)
  return (
    <Button
      variant="ghost"
      size="icon-xs"
      className="opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
      aria-label={`copy ${noun} ${id}`}
      title={`copy ${noun}`}
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

/** TitleText is a title's words, styled by what they are: the prompt
 * or the session's title plain, an agent name or the content-off
 * placeholder muted, a bare id mono. */
export function TitleText({ t }: { t: Title }) {
  return (
    <span
      className={`truncate ${t.kind === "id" ? "font-mono" : t.kind === "absent" || t.kind === "agent" || t.kind === "resume" ? "text-muted-foreground" : ""}`}
      title={t.full}
      data-title-kind={t.kind}
    >
      {t.text}
    </span>
  )
}

/** IdChip is the id under a title (U6): mono, shortened (the full id
 * in its title attribute), what rides beside it (a public id), and a
 * copy button for the whole id. */
export function IdChip({
  id,
  extra,
  noun = "run id",
}: {
  id: string
  extra?: string
  noun?: string
}) {
  return (
    <div
      className="flex items-center gap-1 font-mono text-[11px] text-faint"
      data-id-chip={id}
    >
      <span className="truncate" title={id}>
        <span>{shortId(id)}</span>
        {extra ? <span> · {extra}</span> : null}
      </span>
      <CopyId id={id} noun={noun} />
    </div>
  )
}

/** RunTitle is a run row's head (U6): its title linking to the run,
 * the id chip under it — or, when the id is all there is, the id with
 * its copy button. */
export function RunTitle({
  run,
  extra,
  lead,
  t = runTitle(run),
}: {
  run: RunRow
  extra?: string
  /** Words before the title inside the link (a session's "t3"). */
  lead?: string
  t?: Title
}) {
  return (
    <>
      <Link
        {...runLink(run.id)}
        className="flex min-w-0 items-center gap-1.5 text-[13px] hover:text-thread-ink"
        data-run-link={run.id}
      >
        {lead ? (
          <span className="shrink-0 font-mono text-muted-foreground">
            {lead}
          </span>
        ) : null}
        <TitleText t={t} />
        {t.kind === "id" ? <CopyId id={run.id} /> : null}
      </Link>
      {t.kind === "id" ? (
        extra ? (
          <div className="truncate font-mono text-[11px] text-faint">
            {extra}
          </div>
        ) : null
      ) : (
        <IdChip id={run.id} extra={extra} />
      )}
    </>
  )
}

/** UnknownTokens is a derived row's usage when its spans reported none
 * (plan I1.3): "—" with the API's cost reason, never "0 / 0". */
export function UnknownTokens({ reason }: { reason?: string }) {
  return (
    <span className="font-mono text-faint" title={reason} data-usage="unknown">
      —<span className="sr-only">{` usage not known: ${reason ?? ""}`}</span>
    </span>
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
const SORT_WORDS: Record<
  Exclude<RunSortKey, "cost">,
  { server: string; local: string }
> = {
  started: {
    server: "newest first (the default order)",
    local: "newest first (the default order)",
  },
  duration: {
    server: "sorted by Studio (/api/search?sort=duration): longest first",
    local:
      "sorts the loaded rows only (longest first): this Studio cannot sort by duration itself (it needs the search capability) — load more to sort more",
  },
  tokens: {
    server:
      "sorted by Studio (/api/search?sort=tokens): input + output tokens, most first",
    local:
      "sorts the loaded rows only (input + output tokens, most first): this Studio cannot sort by tokens itself (it needs the search capability) — load more to sort more",
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
        title={
          active && sortKey !== "started"
            ? `${why} — click for newest first`
            : why
        }
        onClick={() => sort.onSort(toggle)}
        data-sort={sortKey}
        data-cost-sort={
          sortKey === "cost" ? (sort.server ? "server" : "page") : undefined
        }
        aria-pressed={active}
      >
        {label}
        {active ? " ↓" : ""}
        {active && sortKey !== "started" && !sort.server ? (
          <span className="text-faint"> (page)</span>
        ) : null}
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

export function RunsTable({
  runs,
  selectedId,
  now,
  showCost,
  sort,
}: RunsTableProps) {
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
          <TableHead title="the thread this run is a turn of">
            session
          </TableHead>
          <TableHead>model</TableHead>
          <TableHead className="text-right">steps</TableHead>
          <SortHead
            sortKey="tokens"
            sort={sort}
            label="tokens in / out"
            title="input / output tokens"
          />
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
                <StatusChip row={run} runs={runs} />
              </TableCell>
              <TableCell className="max-w-80">
                <RunTitle run={run} extra={run.public_id || undefined} />
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
                <DerivedChip row={run} />
                <StoppedByLine run={run} />
                <ProblemBadges run={run} />
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
                    title={`the session ${run.session_id} (${run.public_id || "no public id"})`}
                  >
                    {shortId(run.session_id)}
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
                {derivedUsageUnknown(run) ? (
                  <UnknownTokens reason={run.cost_reason} />
                ) : (
                  <TokensCell usage={run.usage} />
                )}
              </TableCell>
              {showCost ? (
                <TableCell
                  className="text-right whitespace-nowrap"
                  data-cost-cell
                >
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
                  <span className="text-status-run-ink" title="elapsed so far">
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

/** StoppedByLine is what ended the run (U3.2), lib/stopped-by.ts's
 * word under the id; the error line beside it carries the detail. The
 * default exit — a succeeded run the model answered — gets no line (the
 * run header's chip still names it): the table's line is for the
 * non-default exits. */
function StoppedByLine({ run }: { run: RunRow }) {
  const w =
    run.stopped_by === "final_answer" && run.status === "succeeded"
      ? null
      : stoppedByWords(run.stopped_by, run.cancel_cause)
  return w ? (
    <div
      className="truncate font-mono text-[11px] text-faint"
      title={w.hint}
      data-stopped-by={run.stopped_by}
    >
      {w.word}
    </div>
  ) : null
}
