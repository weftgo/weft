// The live page (S4.7): everything streaming right now. The list of
// running runs is one query (refreshed every 5 s); the live lane keeps
// its rows current through one agent-scoped stream per agent — see
// components/studio/live-runs for why not one per run.
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"

import { fetchRuns, metaQuery } from "@/lib/api"
import { LiveRuns } from "@/components/studio/live-runs"
import { useCapabilities } from "@/hooks/use-capabilities"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"

export const Route = createFileRoute("/live")({
  component: LivePage,
})

function LivePage() {
  const { has, loading } = useCapabilities()
  if (loading) {
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  }
  if (!has("live")) {
    return (
      <div className="mx-auto max-w-md space-y-2 py-24 text-center">
        <p className="text-sm">This Studio reports no live capability.</p>
        <p className="text-xs text-muted-foreground">
          The live lane needs the serving handle's hub (setup A) or the
          receiver (setup B).
        </p>
      </div>
    )
  }
  return <RunningNow />
}

function RunningNow() {
  const queryClient = useQueryClient()
  const meta = useQuery(metaQuery())
  const q = useQuery({
    queryKey: ["runs", "live-page"],
    queryFn: () => fetchRuns({ status: "running", parent: "*", limit: 200 }),
    refetchInterval: 5_000,
  })
  const runs = q.data?.runs ?? []
  const total = q.data?.total ?? runs.length

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-sm font-medium tracking-tight">Live</h1>
        <span className="font-mono text-xs text-faint tabular-nums">
          {q.isPending
            ? "…"
            : q.isError
              ? ""
              : `${total.toLocaleString()} running${
                  total > runs.length
                    ? ` · showing the newest ${runs.length.toLocaleString()}`
                    : ""
                }`}
        </span>
        {meta.data?.ingest_open ? (
          <span
            className="font-mono text-[11px] text-faint"
            title="OTLP ingest answers loopback without a token (setup B)"
          >
            ingest open
          </span>
        ) : null}
      </div>
      {q.isPending ? (
        <p className="py-16 text-center font-mono text-xs text-faint">
          loading…
        </p>
      ) : q.isError && runs.length === 0 ? (
        <div className="mx-auto max-w-md space-y-2 py-16 text-center">
          <p className="text-sm text-status-bad">{q.error.message}</p>
          <Button variant="outline" size="sm" onClick={() => void q.refetch()}>
            retry
          </Button>
        </div>
      ) : runs.length === 0 ? (
        <p className="py-16 text-center font-mono text-xs text-faint">
          nothing is streaming right now — run something and it lands
          here the moment its run_start arrives
        </p>
      ) : (
        <LiveRuns
          runs={runs}
          onStale={() =>
            void queryClient.invalidateQueries({ queryKey: ["runs", "live-page"] })
          }
        />
      )}
    </div>
  )
}
