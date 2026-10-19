// A session page (S4.7): the thread's turns in order. Its default view
// is the timeline (U15.1, components/studio/session-timeline.tsx): the
// turns on a shared time axis, resumes grouped under their park, the
// state log's marks under the turn each is filed under and a drawer per
// mark (?mark=); the table of turns, experiments nested under their
// source turn via forked_from, is ?view=table. The header and the tab
// lead with the session's title (U6, lib/title.ts's sessionTitle), the
// id a copyable line under it.
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"
import { useCallback } from "react"

import { metaQuery, sessionQuery, timelineQuery } from "@/lib/api"
import { fetchSessionForks, nestExperiments } from "@/lib/experiments"
import { absoluteTime, relativeTime, tokens, UNNAMED_AGENT } from "@/lib/format"
import { IdChip, StatusChip, TitleText } from "@/components/studio/runs-table"
import { SessionTurns } from "@/components/studio/session-turns"
import { Button } from "@/components/ui/button"
import { NotFound } from "@/components/studio/not-found"
import { isNotFound } from "@/lib/not-found"
import { Spinner } from "@/components/ui/spinner"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { sessionTitle } from "@/lib/title"
import {
  CHILD_TITLES,
  DELEGATED_BLOCK,
  delegatedHoles,
  LINEAGE,
  lineageOfSession,
  OPEN_SESSION,
  turnsWord,
} from "@/lib/delegation"
import type { SessionDoc, SessionRow } from "@/lib/api"
import {
  runLink,
  sessionLink,
  sessionSearch,
  sessionViewLink,
} from "@/lib/links"
import type { SessionView } from "@/lib/links"
import { summarizerView } from "@/lib/cost"
import {
  decisionsWords,
  exchangesWords,
  grantsWords,
  TIMELINE_OFF,
  timelineUnread,
  TREE_PENDING,
  turnOfRow,
  VIEWS,
} from "@/lib/timeline"
import { SessionTimeline } from "@/components/studio/session-timeline"
import { LineageLine } from "@/components/studio/lineage"
import { Cost, CostText, useCostCapable } from "@/components/studio/cost"
import { HoleBadge } from "@/components/studio/hole-badge"

export const Route = createFileRoute("/sessions/$id")({
  validateSearch: sessionSearch,
  component: SessionPage,
})

function SessionPage() {
  const { id } = Route.useParams()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const view: SessionView = search.view ?? "timeline"
  const q = useQuery(sessionQuery(id))
  const meta = useQuery(metaQuery())
  const timelineCapable = !!meta.data?.capabilities.includes("timeline")
  const tl = useQuery({ ...timelineQuery(id), enabled: timelineCapable })
  const costCapable = useCostCapable()
  const select = useCallback(
    (mark: string | undefined) =>
      void navigate({
        search: (prev) => ({ ...prev, mark }),
        replace: true,
      }),
    [navigate]
  )
  const title = q.data ? sessionTitle(q.data, q.data.runs[0]) : undefined
  useDocumentTitle({
    page: "session",
    id,
    title: title && title.kind !== "id" ? title.text : undefined,
    turns: q.data?.turns,
  })
  // The experiments that forked this thread's turns: playground runs
  // are not turns of the session (an ephemeral one has no session id),
  // so they are read beside it and joined on forked_from.
  const forks = useQuery({
    queryKey: ["session-forks", id, q.data?.public_id, q.data?.agent],
    enabled: q.isSuccess,
    staleTime: 5_000,
    queryFn: () =>
      fetchSessionForks({
        public_id: q.data?.public_id ?? "",
        agent: q.data?.agent ?? "",
      }),
  })
  if (q.isPending) {
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  }
  if (q.isError) {
    if (isNotFound(q.error)) return <NotFound kind="session" id={id} />
    return (
      <div className="mx-auto max-w-md space-y-3 py-24 text-center">
        <p className="font-mono text-xs text-faint">{id}</p>
        <p className="text-sm text-status-bad">{q.error.message}</p>
        <div className="flex justify-center gap-2">
          <Button variant="outline" size="sm" onClick={() => void q.refetch()}>
            retry
          </Button>
          <Button variant="ghost" size="sm" render={<Link to="/sessions" />}>
            back to sessions
          </Button>
        </div>
      </div>
    )
  }
  const s = q.data
  const turns = s.runs
  const lineage = lineageOfSession(s)
  const delegated = s.delegated ?? []
  const du = s.delegated_usage
  const experiments = nestExperiments(turns, forks.data?.runs ?? [])
  const forked = [...experiments.values()].reduce((n, l) => n + l.length, 0)
  // The timeline's turns are the session document's when the state log
  // is not read (capability off, or the read failed): no turn is lost
  // because its marks are.
  const marks = tl.data?.marks ?? []
  const tlTurns = tl.data?.turns ?? turns.map(turnOfRow)
  const sum = summarizerView(tl.data?.summarizer)
  const decided = decisionsWords(marks)
  const grants = grantsWords(marks)
  const tlNote = !meta.data
    ? ""
    : !timelineCapable
      ? TIMELINE_OFF
      : tl.isError
        ? timelineUnread(tl.error.message)
        : ""

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <div className="min-w-0">
          <h1 className="flex min-w-0 text-base font-medium tracking-tight">
            <TitleText t={sessionTitle(s, turns[0])} />
          </h1>
          <IdChip id={s.id} noun="session id" />
          {lineage ? (
            <LineageLine
              lineage={lineage}
              className="flex min-w-0 flex-wrap items-center gap-1 text-xs text-faint"
            />
          ) : null}
        </div>
        {/* The session's status is its newest turn's word (U1): parked
            while that turn's calls await a decision. */}
        <StatusChip row={turns.at(-1) ?? s} runs={turns} complete detail />
        <span className="text-xs text-muted-foreground">{s.agent}</span>
        <span className="font-mono text-[11px] text-faint" data-session-counts>
          {exchangesWords(tlTurns)} · {tokens(s.usage.input_tokens)} in /{" "}
          {tokens(s.usage.output_tokens)} out
        </span>
        {decided ? (
          <span
            className="font-mono text-[11px] text-faint"
            data-session-decisions
          >
            {decided}
          </span>
        ) : null}
        {grants ? (
          <span className="font-mono text-[11px] text-faint">{grants}</span>
        ) : null}
        {costCapable ? (
          <span
            className="flex items-baseline gap-1.5 text-[11px]"
            data-session-cost
          >
            <Cost fields={s} chip={false} label="the session's cost" />
            {sum ? <CostText v={sum} chip={false} label="summarizer" /> : null}
          </span>
        ) : null}
        {du && (du.input_tokens > 0 || du.output_tokens > 0) ? (
          <span
            className="font-mono text-[11px] text-faint"
            title={DELEGATED_BLOCK.usageTitle}
            data-delegated-usage
          >
            {DELEGATED_BLOCK.usage(
              tokens(du.input_tokens),
              tokens(du.output_tokens)
            )}
          </span>
        ) : null}
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

      <nav
        aria-label="session views"
        className="flex items-center gap-1 text-xs"
      >
        {VIEWS.map((v) => (
          <Link
            key={v.view}
            {...sessionViewLink(id, { view: v.view })}
            aria-current={view === v.view ? "page" : undefined}
            title={v.title}
            className={`rounded-sm border px-2 py-0.5 font-mono ${view === v.view ? "bg-secondary text-foreground" : "text-muted-foreground hover:text-foreground"}`}
            data-session-view={v.view}
          >
            {v.word}
          </Link>
        ))}
      </nav>

      {view === "timeline" ? (
        <>
          {tlNote ? (
            <p
              className="font-mono text-[11px] text-muted-foreground"
              data-timeline-note
            >
              {tlNote}
            </p>
          ) : null}
          {tl.data && tl.data.holes.length > 0 ? (
            <div className="space-y-1" data-timeline-holes>
              {tl.data.holes.map((h) => (
                <HoleBadge key={h.hole} {...h} detail />
              ))}
            </div>
          ) : null}
          {timelineCapable && tl.isPending ? (
            <div className="flex justify-center py-6">
              <Spinner />
            </div>
          ) : tlTurns.length > 0 || marks.length > 0 ? (
            <SessionTimeline
              sessionID={id}
              doc={{ turns: tlTurns, marks }}
              rows={turns}
              selected={search.mark}
              onSelect={select}
            />
          ) : null}
        </>
      ) : view === "table" ? (
        <div className="overflow-x-auto rounded-lg border bg-background">
          <SessionTurns turns={turns} experiments={experiments} />
        </div>
      ) : (
        <p
          className="py-6 text-center font-mono text-xs text-muted-foreground"
          data-tree-pending
        >
          {TREE_PENDING}
        </p>
      )}
      {delegated.length > 0 || s.delegated_unknown ? (
        <DelegatedSessions doc={s} rows={delegated} />
      ) : null}
      {turns.length === 0 ? (
        <p className="py-8 text-center font-mono text-xs text-faint">
          no turns of this session recorded
        </p>
      ) : null}
      {s.turns > turns.length ? (
        <p className="text-center font-mono text-[11px] text-faint">
          showing {turns.length.toLocaleString()} of {s.turns.toLocaleString()}{" "}
          turns — the session document is capped; the runs list filtered by this
          session pages through all of them
        </p>
      ) : null}
      <p className="text-center font-mono text-[11px] text-faint">
        {forks.isError
          ? `experiments could not be read: ${forks.error.message}`
          : forks.isPending
            ? "looking for experiments…"
            : `${forked} ${forked === 1 ? "experiment" : "experiments"} forked from these turns${
                forks.data.truncated
                  ? " — only the newest 2,000 playground runs were searched"
                  : ""
              }`}
      </p>
    </div>
  )
}

/** DelegatedSessions is the session page's delegated block (U14,
 * UX-B-05, UX-B-35): each session this one delegated, with the turn
 * and call that started it, its own turns, tokens and cost, and the
 * whole delegated tree's cost beside the session's own. */
function DelegatedSessions({
  doc,
  rows,
}: {
  doc: SessionDoc
  rows: SessionRow[]
}) {
  const costCapable = useCostCapable()
  return (
    <section className="space-y-1.5" data-delegated-sessions>
      <div className="flex flex-wrap items-center gap-2">
        <span className="eyebrow" title={CHILD_TITLES.delegated}>
          {DELEGATED_BLOCK.eyebrow}
        </span>
        {costCapable && rows.length > 0 ? (
          <Cost
            fields={{
              cost_usd: doc.delegated_sessions_cost_usd ?? null,
              cost_known: doc.delegated_sessions_cost_known ?? false,
              cost_badge: doc.delegated_sessions_cost_known
                ? undefined
                : "unknown",
              cost_reason: doc.delegated_sessions_cost_reason,
              // The server's cacheOf over the delegated usage, as every
              // figure's: not_reported draws the cost lib's hint.
              cache: doc.delegated_sessions_cache,
            }}
            chip={false}
          />
        ) : null}
        {delegatedHoles(doc).map((m) => (
          <HoleBadge key={m.hole} {...m} />
        ))}
      </div>
      <ul className="divide-y rounded-lg border bg-background">
        {rows.map((d) => (
          <li
            key={d.id}
            className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-xs"
            data-delegated-session={d.id}
          >
            <StatusChip row={d} />
            <Link
              {...sessionLink(d.id)}
              className="flex min-w-0 items-center gap-1 text-thread-ink hover:underline"
              title={`${OPEN_SESSION} ${d.id}`}
            >
              <span aria-hidden>↳</span>
              <span>{d.agent || UNNAMED_AGENT}</span>
              <span className="truncate font-mono text-faint">{d.id}</span>
            </Link>
            {d.parent_turn && d.parent_run ? (
              <Link
                {...runLink(d.parent_run)}
                className="font-mono text-faint hover:text-foreground hover:underline"
              >
                {LINEAGE.turn(d.parent_turn)}
              </Link>
            ) : null}
            {d.parent_call ? (
              <span className="font-mono text-faint">
                {LINEAGE.call(d.parent_call)}
              </span>
            ) : null}
            <span className="font-mono text-[11px] text-faint">
              {turnsWord(d.turns)} · {tokens(d.usage.input_tokens)} in /{" "}
              {tokens(d.usage.output_tokens)} out
            </span>
            {costCapable ? <Cost fields={d} chip={false} /> : null}
          </li>
        ))}
      </ul>
    </section>
  )
}
