// The Live page's peek (plan H6): a run's story beside the list — the
// run page's own reader (useRunEvents: the pages, then the run's live
// tail) and its StepList, never a copy — under a header with the run
// link and a close. The pane is a complementary region named for the
// run; Escape closes it (the page binds the key).
import { useCallback } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { X } from "lucide-react"

import { runQuery } from "@/lib/api"
import type { RunRow } from "@/lib/api"
import { runLink } from "@/lib/links"
import { useCapabilities } from "@/hooks/use-capabilities"
import { storyRunID, useRunEvents } from "@/hooks/use-run-events"
import { StepList } from "@/components/studio/step-list"
import { DerivedStory } from "@/components/studio/derived-story"
import { isDerived } from "@/lib/honesty"
import { StatusChip } from "@/components/studio/runs-table"
import { Button } from "@/components/ui/button"

export function LivePeek({ id, onClose }: { id: string; onClose: () => void }) {
  const { has, loading } = useCapabilities()
  const queryClient = useQueryClient()
  const run = useQuery({
    ...runQuery(id),
    refetchInterval: (q) => (q.state.data?.status === "running" ? 2000 : false),
  })
  const status = run.data?.status ?? "running"
  // Run frames of this run refresh its document in place (the run
  // page's rule): the row is written into the cache, no refetch.
  const onRun = useCallback(
    (row: RunRow) => {
      queryClient.setQueryData(runQuery(id).queryKey, (old) =>
        old ? { ...old, ...row } : old
      )
    },
    [id, queryClient]
  )
  const stream = useRunEvents(loading ? "" : storyRunID(id, run), status, {
    live: has("live"),
    onRun,
  })

  return (
    <aside
      aria-label={`story of run ${id}`}
      data-live-peek={id}
      className="flex min-w-0 flex-col gap-2 rounded-lg border bg-background p-3"
    >
      <div className="flex flex-wrap items-center gap-2">
        {run.data ? <StatusChip status={run.data.status} /> : null}
        <Link
          {...runLink(id)}
          className="font-mono text-[13px] hover:text-thread-ink"
        >
          {id}
        </Link>
        {run.data?.agent ? (
          <span className="text-xs text-muted-foreground">
            {run.data.agent}
          </span>
        ) : null}
        <Button
          variant="ghost"
          size="icon-xs"
          className="ml-auto"
          aria-label="close the peek"
          title="close (Escape)"
          onClick={onClose}
        >
          <X />
        </Button>
      </div>
      {run.isError ? (
        <p className="font-mono text-xs text-status-bad">{run.error.message}</p>
      ) : !run.data || (stream.loading && stream.events.length === 0) ? (
        <p className="py-8 text-center font-mono text-xs text-faint">
          loading…
        </p>
      ) : (
        <div className="min-w-0" data-peek-story>
          {stream.error ? (
            <p className="font-mono text-xs text-status-bad">{stream.error}</p>
          ) : null}
          {/* A derived run (plan I1.3): no events to fold — its story
              is the steps route's, under the derived badge. */}
          {isDerived(run.data) ? (
            <DerivedStory doc={run.data} replayable={false} />
          ) : (
            <StepList
              events={stream.events}
              folded={stream.folded}
              doc={run.data}
            />
          )}
        </div>
      )}
    </aside>
  )
}
