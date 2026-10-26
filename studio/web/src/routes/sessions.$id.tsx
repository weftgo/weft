// A session page (S4.7): the thread's turns in order. Its default view
// is the timeline (U15.1, components/studio/session-timeline.tsx): the
// turns on a shared time axis, resumes grouped under their park, the
// state log's marks under the turn each is filed under and a drawer per
// mark (?mark=); the table of turns, experiments nested under their
// source turn via forked_from, is ?view=table. The header and the tab
// lead with the session's title (U6, lib/title.ts's sessionTitle), the
// id a copyable line under it. U15.2: the timeline's lane filter
// (?lanes=, dims never hides) and context lane, the tree (?view=tree:
// the lines the turns continue, abandoned turns marked, forks on the
// origin), a forked session's lineage, the turn-number line and the
// page live — a run frame of this session refetches the session and
// its timeline. U31.2: the header's verbs (copy the public id, export
// the conversation as JSON lines, open in app, delete — UX-B-29) and
// its storage line (UX-S3-21, components/studio/session-actions.tsx);
// the table newest first like the panel, ?order=oldest the other
// (UX-S3-16). Every turn is in the document (no cap to say).
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"
import { useCallback, useEffect, useState } from "react"

import { metaQuery, sessionQuery, timelineQuery } from "@/lib/api"
import { openLive, throttle } from "@/lib/live"
import { useCapabilities } from "@/hooks/use-capabilities"
import { fetchSessionForks, nestExperiments } from "@/lib/experiments"
import {
  absoluteTime,
  relativeTime,
  tokenPair,
  tokens,
  UNNAMED_AGENT,
} from "@/lib/format"
import { IdChip, StatusChip, TitleText } from "@/components/studio/runs-table"
import { OrderSwitch, SessionTurns } from "@/components/studio/session-turns"
import { NotFound } from "@/components/studio/not-found"
import { LoadFailed } from "@/components/studio/empty-state"
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
  conversationLink,
  runLink,
  sessionLink,
  sessionViewLink,
} from "@/lib/links"
import type { SessionPageView } from "@/lib/timeline"
import { summarizerView } from "@/lib/cost"
import {
  cutOf,
  decisionsWords,
  exchangesWords,
  FORK_NO_PAGE,
  forkOf,
  forkedIntoWords,
  FORKS_TITLE,
  forksWords,
  grantsWords,
  LIVE_WORDS,
  pageOrder,
  pageView,
  parseLanes,
  sessionPageLink,
  sessionPageSearch,
  TIMELINE_OFF,
  timelineUnread,
  TURN_IDS_TITLE,
  turnIdsWords,
  turnOfRow,
  VIEWS,
} from "@/lib/timeline"
import {
  ContextLane,
  ForkState,
  LaneFilter,
  SessionTimeline,
} from "@/components/studio/session-timeline"
import { SessionTree } from "@/components/studio/session-tree"
import { SessionApprovals } from "@/components/studio/session-approvals"
import { ForkOfLine } from "@/components/studio/fork-of"
import { LineageLine } from "@/components/studio/lineage"
import { Cost, CostText, useCostCapable } from "@/components/studio/cost"
import { HoleBadge } from "@/components/studio/hole-badge"
import { THREAD_LINK, STATUS_TEXT } from "@/lib/status-class"
import { AWAITING_TITLE, DECIDE } from "@/lib/session-states"
import { SessionCounts } from "@/components/studio/session-state"
import { SessionActions } from "@/components/studio/session-actions"
import type { TurnOrder } from "@/lib/session-states"
import { isPlainShortcut } from "@/lib/keys"

type LiveState = "off" | "on" | "down"

export const Route = createFileRoute("/sessions/$id")({
  validateSearch: sessionPageSearch,
  component: SessionPage,
})

function SessionPage() {
  const { id } = Route.useParams()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const view: SessionPageView = pageView(search.view)
  const order: TurnOrder = pageOrder(search.order)
  const q = useQuery(sessionQuery(id))
  const meta = useQuery(metaQuery())
  const timelineCapable = !!meta.data?.capabilities.includes("timeline")
  const tl = useQuery({ ...timelineQuery(id), enabled: timelineCapable })
  const costCapable = useCostCapable()
  // Live (UX-B-22): a run frame of this session — a turn started,
  // stepped, parked or ended — refetches the session and its timeline
  // (marks ride with the run's records), at most once a second.
  const qc = useQueryClient()
  const { has } = useCapabilities()
  const liveCapable = has("live")
  // The live state is the session's it was set for: another session's
  // page starts off until its own stream opens.
  const [liveOf, setLive] = useState<{ id: string; state: LiveState }>({
    id,
    state: "off",
  })
  const liveState: LiveState = liveOf.id === id ? liveOf.state : "off"
  useEffect(() => {
    if (!liveCapable) return
    const setLiveState = (state: LiveState) => setLive({ id, state })
    const refetch = throttle(() => {
      void qc.invalidateQueries({ queryKey: ["session", id] })
      void qc.invalidateQueries({ queryKey: ["session-timeline", id] })
      // A new turn moves every turn's stepper (turn n of m).
      void qc.invalidateQueries({ queryKey: ["turn-neighbours", id] })
    }, 1000)
    const live = openLive({
      selector: { session: id },
      kinds: ["run"],
      onRun: () => refetch(),
      onOpen: (again) => {
        setLiveState("on")
        if (again) refetch()
      },
      onOverflow: () => {
        setLiveState(live.overflowed() ? "down" : "on")
        refetch()
      },
      onRefused: () => setLiveState("off"),
      onGaveUp: () => setLiveState("down"),
    })
    return () => {
      refetch.cancel()
      live.close()
    }
  }, [id, liveCapable, qc])
  const lanes = parseLanes(search.lanes)
  const setLanes = useCallback(
    (l: string) =>
      void navigate({
        search: (prev) => ({ ...prev, lanes: l || undefined }),
        replace: true,
      }),
    [navigate]
  )
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
  // ?turn= (U21.2, UX-F-28): the table's selected turn; j / k move it
  // through the rows as drawn (newest first unless oldest).
  const turnNos = (q.data?.runs ?? []).map((r) => r.turn)
  const turnKey = turnNos.join(",")
  useEffect(() => {
    if (view !== "table") return
    const onKey = (e: KeyboardEvent) => {
      if (!isPlainShortcut(e) || (e.key !== "j" && e.key !== "k")) return
      const nos = turnKey ? turnKey.split(",").map(Number) : []
      const shown = order === "newest" ? [...nos].reverse() : nos
      if (!shown.length) return
      e.preventDefault()
      const down = e.key === "j"
      // From the URL as it is now: two quick presses both move.
      void navigate({
        search: (prev) => {
          const at = prev.turn === undefined ? -1 : shown.indexOf(prev.turn)
          const next = down
            ? Math.min(at + 1, shown.length - 1)
            : Math.max(at - 1, 0)
          return { ...prev, turn: shown[next] }
        },
        replace: true,
      })
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [view, order, turnKey, navigate])
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
      <LoadFailed
        kind="session"
        id={id}
        error={q.error}
        onRetry={() => void q.refetch()}
      />
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
  // Past the mark cap the counts are floors and every view says so.
  const cut = cutOf(tl.data)
  const decided = decisionsWords(marks, cut)
  const grants = grantsWords(marks, cut)
  const holes =
    tl.data && tl.data.holes.length > 0 ? (
      <div className="space-y-1" data-timeline-holes>
        {tl.data.holes.map((h) => (
          <HoleBadge key={h.hole} {...h} detail />
        ))}
      </div>
    ) : null
  const ids = turnIdsWords(tlTurns, marks)
  const forksOut = tl.data?.forks ?? []
  // A forked session's lineage (UX-B-07): its turns carry
  // weft.session.forked_from (<origin>#<entry>).
  const forkedFrom = forkOf(turns.find((r) => forkOf(r.meta))?.meta)
  const running = tlTurns.some((t) => t.status === "running")
  const compacting = (tl.data?.lanes ?? []).some(
    (l) => l.lane === "compaction" && l.marks > 0
  )
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
          {forkedFrom ? (
            <p className="min-w-0 font-mono text-xs break-words text-muted-foreground">
              <ForkOfLine
                origin={forkedFrom.session}
                entry={forkedFrom.entry}
                self={s.id}
              />
            </p>
          ) : null}
        </div>
        {/* The session's status is its newest turn's word (U1): parked
            while that turn's calls await a decision. */}
        <StatusChip row={turns.at(-1) ?? s} runs={turns} complete detail />
        {/* U16 (UX-B-15): an open park at the newest turn is the
            session's state — the way to decide it is the parked turn's
            page (the approvals verbs, F4.3). */}
        {s.pending && turns.at(-1) ? (
          <Link
            {...runLink(turns.at(-1)!.id)}
            className={`font-mono text-[11px] ${THREAD_LINK}`}
            title={AWAITING_TITLE}
            data-session-decide={turns.at(-1)!.id}
          >
            {DECIDE}
          </Link>
        ) : null}
        <SessionCounts row={s} />
        <span className="text-xs text-muted-foreground">{s.agent}</span>
        <span className="font-mono text-[11px] text-faint" data-session-counts>
          {exchangesWords(tlTurns)} ·{" "}
          {tokenPair(s.usage.input_tokens, s.usage.output_tokens)}
        </span>
        {ids ? (
          <span
            className="font-mono text-[11px] text-faint"
            title={TURN_IDS_TITLE}
            data-turn-ids
          >
            {ids}
          </span>
        ) : null}
        {liveState !== "off" ? (
          <span
            className={`font-mono text-[11px] ${running ? `${STATUS_TEXT.running}` : "text-faint"}`}
            title={LIVE_WORDS.title}
            data-session-live={liveState}
          >
            {liveState === "down"
              ? LIVE_WORDS.down
              : running
                ? LIVE_WORDS.running
                : LIVE_WORDS.on}
          </span>
        ) : null}
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
          <Link
            {...conversationLink(s.public_id)}
            className="rounded-sm border px-1.5 py-0.5 font-mono text-[11px] [overflow-wrap:anywhere] text-faint hover:text-foreground"
            title="the public id (the browser-safe handle): its conversation — every run carrying it, experiments nested"
            data-conversation-link={s.public_id}
          >
            {s.public_id}
          </Link>
        ) : null}
        <span
          className="ml-auto font-mono text-[11px] text-faint"
          title={`first ${absoluteTime(s.first_seen)}`}
        >
          last seen {relativeTime(s.last_seen)}
        </span>
      </div>

      <SessionActions doc={s} turns={turns} />

      {forksOut.length ? (
        <div
          className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-xs"
          data-forked-into
        >
          <span className="font-mono text-faint" title={FORK_NO_PAGE}>
            {forkedIntoWords(forksOut.length)}
          </span>
          {forksOut.map((f) => (
            <ForkState key={`${f.session_id}:${f.entry ?? ""}`} f={f} />
          ))}
        </div>
      ) : null}

      <nav
        aria-label="session views"
        className="flex items-center gap-1 text-xs"
      >
        {VIEWS.map((v) => (
          <Link
            key={v.view}
            {...sessionPageLink(id, { view: v.view, lanes: search.lanes })}
            // Exact: the timeline's link (no view) matches every view's
            // search partially; the current tab is ours to say.
            activeOptions={{ exact: true }}
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
          {holes}
          {tl.data ? (
            <LaneFilter
              lanes={lanes}
              counts={tl.data.lanes}
              onChange={setLanes}
            />
          ) : null}
          {timelineCapable && tl.isPending ? (
            <div className="flex justify-center py-6">
              <Spinner />
            </div>
          ) : tlTurns.length > 0 || marks.length > 0 ? (
            <SessionTimeline
              sessionID={id}
              doc={{ turns: tlTurns, marks, forks: forksOut }}
              rows={turns}
              selected={search.mark}
              onSelect={select}
              lanes={lanes}
            />
          ) : null}
          {compacting && tlTurns.length > 0 ? (
            <ContextLane
              turns={tlTurns}
              marks={marks}
              onSelect={(k) => select(k)}
            />
          ) : null}
        </>
      ) : view === "table" ? (
        <>
          <OrderSwitch
            order={order}
            onChange={(o) =>
              void navigate({
                search: (prev) => ({
                  ...prev,
                  order: o === "oldest" ? "oldest" : undefined,
                }),
                replace: true,
              })
            }
          />
          <div className="overflow-x-auto rounded-lg border bg-background">
            <SessionTurns
              turns={turns}
              experiments={experiments}
              agent={s.agent}
              order={order}
              selected={search.turn}
            />
          </div>
        </>
      ) : view === "approvals" ? (
        <>
          {tlNote ? (
            <p
              className="font-mono text-[11px] text-muted-foreground"
              data-timeline-note
            >
              {tlNote}
            </p>
          ) : null}
          {holes}
          {timelineCapable && tl.isPending ? (
            <div className="flex justify-center py-6">
              <Spinner />
            </div>
          ) : (
            <SessionApprovals
              sessionID={id}
              turns={tlTurns}
              marks={marks}
              cut={cut}
            />
          )}
        </>
      ) : (
        <>
          {tlNote ? (
            <p
              className="font-mono text-[11px] text-muted-foreground"
              data-timeline-note
            >
              {tlNote}
            </p>
          ) : null}
          {holes}
          {timelineCapable && tl.isPending ? (
            <div className="flex justify-center py-6">
              <Spinner />
            </div>
          ) : (
            <SessionTree
              sessionID={id}
              doc={{ turns: tlTurns, marks, forks: forksOut }}
              rows={turns}
            />
          )}
        </>
      )}
      {delegated.length > 0 ||
      s.delegated_unknown ||
      s.delegated_holes?.length ? (
        <DelegatedSessions doc={s} rows={delegated} />
      ) : null}
      {turns.length === 0 ? (
        <p className="py-8 text-center font-mono text-xs text-faint">
          no turns of this session recorded
        </p>
      ) : null}
      <p
        className="text-center font-mono text-[11px] text-faint"
        data-session-forks
      >
        {forks.isError ? (
          `experiments could not be read: ${forks.error.message}`
        ) : forks.isPending ? (
          "looking for experiments…"
        ) : (
          <>
            {/* The experiments nest under their source turn in the
                table: off it, the count is the way there. */}
            {view === "table" || forked === 0 ? (
              forksWords(forked)
            ) : (
              <Link
                {...sessionViewLink(id, { view: "table" })}
                className={THREAD_LINK}
                title={FORKS_TITLE}
              >
                {forksWords(forked)}
              </Link>
            )}
            {forks.data.truncated
              ? " — only the newest 2,000 playground runs were searched"
              : ""}
          </>
        )}
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
        {costCapable && (rows.length > 0 || doc.delegated_holes?.length) ? (
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
              {turnsWord(d.turns)} ·{" "}
              {tokenPair(d.usage.input_tokens, d.usage.output_tokens)}
            </span>
            {costCapable ? <Cost fields={d} chip={false} /> : null}
          </li>
        ))}
      </ul>
    </section>
  )
}
