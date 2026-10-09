// A session page (S4.7): the thread's turns in order, each a run
// card; experiments nest under their source turn via forked_from.
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"

import { sessionQuery } from "@/lib/api"
import { fetchSessionForks, nestExperiments } from "@/lib/experiments"
import { absoluteTime, relativeTime, tokens } from "@/lib/format"
import { StatusChip } from "@/components/studio/runs-table"
import { SessionTurns } from "@/components/studio/session-turns"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { useDocumentTitle } from "@/hooks/use-document-title"

export const Route = createFileRoute("/sessions/$id")({
  component: SessionPage,
})

function SessionPage() {
  const { id } = Route.useParams()
  useDocumentTitle({ page: "session", id })
  const q = useQuery(sessionQuery(id))
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
  const experiments = nestExperiments(turns, forks.data?.runs ?? [])
  const forked = [...experiments.values()].reduce((n, l) => n + l.length, 0)

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
        <SessionTurns turns={turns} experiments={experiments} />
      </div>
      {turns.length === 0 ? (
        <p className="py-8 text-center font-mono text-xs text-faint">
          no top-level turns recorded
        </p>
      ) : null}
      {s.turns > turns.length ? (
        <p className="text-center font-mono text-[11px] text-faint">
          showing {turns.length.toLocaleString()} of{" "}
          {s.turns.toLocaleString()} turns — the session document is capped;
          the runs list filtered by this session pages through all of them
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
