// The conversation page (U16, UX-F-02): every run of a public id — what
// the devtools panel follows — newest first like the panel and the
// session table (?order=oldest one press away), the experiments taken from
// a turn nested under it (lib/conversation.ts), the header's turns,
// tokens and cost, the thread sessions carrying the public id linked
// to their own pages, and the page live (a run frame of the public id
// refetches it). A public id with no thread session (a harness, a
// non-thread app) has this page and no other; the runs list is its flat
// view.
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"
import { useEffect, useState } from "react"

import { fetchRuns, fetchSessions } from "@/lib/api"
import type { RunRow } from "@/lib/api"
import { openLive, throttle } from "@/lib/live"
import { useCapabilities } from "@/hooks/use-capabilities"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { tokens } from "@/lib/format"
import {
  CONVERSATION,
  conversationCost,
  splitConversation,
} from "@/lib/conversation"
import { runLink, runsLink, sessionLink } from "@/lib/links"
import { LIVE_WORDS, pageOrder } from "@/lib/timeline"
import { THREAD_LINK } from "@/lib/status-class"
import { CostText, useCostCapable } from "@/components/studio/cost"
import { OrderSwitch, SessionTurns } from "@/components/studio/session-turns"
import { SessionState } from "@/components/studio/session-state"
import { Button } from "@/components/ui/button"
import { FORBIDDEN_NOTE, isForbidden } from "@/lib/not-found"
import { Spinner } from "@/components/ui/spinner"

/** The runs one read takes (the API's page cap). */
const LIMIT = 500

export const Route = createFileRoute("/conversations/$id")({
  // The turn table's order (UX-S3-16): newest first, as the panel and
  // the session table; ?order=oldest the other (any other value reads
  // newest).
  validateSearch: (s: Record<string, unknown>): { order?: "oldest" } =>
    s.order === "oldest" ? { order: "oldest" } : {},
  component: ConversationPage,
})

/** The header's cost: the turns' own figure and, apart, their
 * delegated one ("+ $y delegated"), as the session header and the
 * panel say them. */
function ConversationCost({ turns }: { turns: RunRow[] }) {
  const { own, delegated } = conversationCost(turns)
  return (
    <span
      className="flex items-baseline gap-1 text-[11px]"
      data-conversation-cost
    >
      <span className="font-mono text-faint" title={CONVERSATION.ownTitle}>
        {CONVERSATION.own}
      </span>
      <CostText v={own} chip={false} />
      {delegated ? (
        <span
          className="flex items-baseline gap-1"
          title={CONVERSATION.delegatedTitle}
          data-conversation-delegated
        >
          <span className="font-mono text-faint">+</span>
          <CostText v={delegated} chip={false} />
          <span className="font-mono text-faint">{CONVERSATION.delegated}</span>
        </span>
      ) : null}
    </span>
  )
}

function ConversationPage() {
  const { id } = Route.useParams()
  const order = pageOrder(Route.useSearch().order)
  const navigate = Route.useNavigate()
  const costCapable = useCostCapable()
  const runs = useQuery({
    queryKey: ["conversation", id],
    staleTime: 5_000,
    queryFn: () => fetchRuns({ public_id: id, limit: LIMIT }),
  })
  const sessions = useQuery({
    queryKey: ["conversation-sessions", id],
    staleTime: 5_000,
    queryFn: () => fetchSessions({ public_id: id }),
  })
  const split = splitConversation(runs.data?.runs ?? [])
  useDocumentTitle({
    page: "conversation",
    id,
    turns: runs.data ? split.turns.length : undefined,
  })
  // Live: a run frame of this public id (a turn or an experiment
  // started, stepped, parked or ended) refetches, at most once a second.
  const qc = useQueryClient()
  const { has } = useCapabilities()
  const liveCapable = has("live")
  const [live, setLive] = useState<"off" | "on" | "down">("off")
  useEffect(() => {
    if (!liveCapable) return
    const refetch = throttle(() => {
      void qc.invalidateQueries({ queryKey: ["conversation", id] })
      void qc.invalidateQueries({ queryKey: ["conversation-sessions", id] })
    }, 1000)
    const l = openLive({
      selector: { public_id: id },
      kinds: ["run"],
      onRun: () => refetch(),
      onOpen: (again) => {
        setLive("on")
        if (again) refetch()
      },
      onOverflow: () => {
        setLive(l.overflowed() ? "down" : "on")
        refetch()
      },
      onRefused: () => setLive("off"),
      onGaveUp: () => setLive("down"),
    })
    return () => {
      refetch.cancel()
      l.close()
    }
  }, [id, liveCapable, qc])

  if (runs.isPending)
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  if (runs.isError)
    return (
      <div className="mx-auto max-w-md space-y-3 py-24 text-center">
        <p className="font-mono text-xs text-faint">{id}</p>
        <p className="text-sm text-status-bad">{runs.error.message}</p>
        {isForbidden(runs.error) ? (
          // A 403 answers the same next time: no retry.
          <p className="text-xs text-muted-foreground" data-no-retry>
            {FORBIDDEN_NOTE}
          </p>
        ) : (
          <Button
            variant="outline"
            size="sm"
            onClick={() => void runs.refetch()}
          >
            retry
          </Button>
        )}
      </div>
    )
  const { turns, experiments, orphans } = split
  const nested = [...experiments.values()].reduce((n, l) => n + l.length, 0)
  const usage = turns.reduce(
    (u, r) => ({
      input_tokens: u.input_tokens + r.usage.input_tokens,
      output_tokens: u.output_tokens + r.usage.output_tokens,
    }),
    { input_tokens: 0, output_tokens: 0 }
  )
  const running = turns.some((r) => r.status === "running")
  const threads = sessions.data?.sessions ?? []
  const capped = runs.data.next_before != null

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <div className="min-w-0">
          <p className="eyebrow" title={CONVERSATION.title}>
            {CONVERSATION.eyebrow}
          </p>
          <h1 className="font-mono text-base font-medium tracking-tight break-all">
            {id}
          </h1>
        </div>
        <span
          className="font-mono text-[11px] text-faint"
          data-conversation-counts
        >
          {CONVERSATION.counts(turns, nested + orphans.length)} ·{" "}
          {tokens(usage.input_tokens)} in / {tokens(usage.output_tokens)} out
        </span>
        {costCapable && turns.length ? (
          <ConversationCost turns={turns} />
        ) : null}
        {live !== "off" ? (
          <span
            className={`font-mono text-[11px] ${running ? "text-status-run-ink" : "text-faint"}`}
            title={LIVE_WORDS.title}
            data-conversation-live={live}
          >
            {live === "down"
              ? LIVE_WORDS.down
              : running
                ? LIVE_WORDS.running
                : LIVE_WORDS.on}
          </span>
        ) : null}
        <Link
          {...runsLink({ public_id: id })}
          className={`ml-auto font-mono text-[11px] ${THREAD_LINK}`}
        >
          {CONVERSATION.runsList}
        </Link>
      </div>

      {sessions.isSuccess ? (
        threads.length ? (
          <div
            className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs"
            data-conversation-sessions
          >
            <span className="font-mono text-faint">
              {CONVERSATION.sessions}
            </span>
            {threads.map((t) => (
              <span key={t.id} className="flex items-center gap-1.5">
                <Link
                  {...sessionLink(t.id)}
                  className={`font-mono [overflow-wrap:anywhere] ${THREAD_LINK}`}
                  title={CONVERSATION.sessionTitle}
                >
                  {t.id}
                </Link>
                <SessionState row={t} />
              </span>
            ))}
          </div>
        ) : (
          <p
            className="font-mono text-[11px] text-muted-foreground"
            data-conversation-no-session
          >
            {CONVERSATION.noSession}
          </p>
        )
      ) : null}

      {turns.length ? (
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
              order={order}
            />
          </div>
        </>
      ) : (
        <p className="py-8 text-center font-mono text-xs text-faint">
          {CONVERSATION.empty}
        </p>
      )}
      {orphans.length ? (
        <div className="space-y-1 text-xs" data-conversation-orphans>
          <p className="font-mono text-faint">
            {CONVERSATION.orphans(orphans.length)}
          </p>
          <ul className="flex flex-wrap gap-x-3 gap-y-1">
            {orphans.map((r) => (
              <li key={r.id}>
                <Link
                  {...runLink(r.id)}
                  className={`font-mono [overflow-wrap:anywhere] ${THREAD_LINK}`}
                >
                  {r.id}
                </Link>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {capped ? (
        <p className="text-center font-mono text-[11px] text-faint">
          {CONVERSATION.capped(LIMIT)}
        </p>
      ) : null}
    </div>
  )
}
