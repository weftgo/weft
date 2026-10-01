// The live page (S4.7): everything streaming right now. The runs
// list of running runs feeds one live subscription per run (the
// agent-scoped stream covers whole fleets; per-run streams keep each
// row's lane honest and let one row be followed without the others'
// noise). Rows update in place through the (run) frames; the newest
// running runs arrive by refetch when a run frame says something
// started.
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link } from "@tanstack/react-router"
import { useEffect, useRef, useState } from "react"

import { fetchRuns, metaQuery } from "@/lib/api"
import type { RunRow } from "@/lib/api"
import { openLive } from "@/lib/live"
import { elapsed, relativeTime } from "@/lib/format"
import { StatusChip } from "@/components/studio/runs-table"
import { useCapabilities } from "@/hooks/use-capabilities"
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

/** One running run's live row: the row, refreshed by run frames. */
function RunLane({ run }: { run: RunRow }) {
  const [row, setRow] = useState(run)
  const [pulsed, setPulsed] = useState(0)
  useEffect(() => {
    const live = openLive({
      selector: { run: run.id },
      kinds: ["event", "run"],
      onRun: (f) => setRow(f.run),
      onRecord: () => setPulsed((n) => n + 1),
    })
    return () => live.close()
  }, [run.id])
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border bg-background px-3 py-2">
      <StatusChip status={row.status} />
      <Link
        to="/runs/$id"
        params={{ id: row.id }}
        className="font-mono text-[13px] hover:text-thread-ink"
      >
        {row.id}
      </Link>
      <span className="text-xs">{row.agent || "—"}</span>
      <span className="font-mono text-[11px] text-muted-foreground">
        {row.model.provider}/{row.model.name}
      </span>
      <span
        className="font-mono text-[11px] text-status-run tabular-nums"
        title={`${pulsed} record frame${pulsed === 1 ? "" : "s"} this connection`}
      >
        {row.status === "running" ? `${elapsed(row.started)} · ${pulsed} evt` : "ended"}
      </span>
      <span
        className="ml-auto font-mono text-[11px] text-faint"
        title={row.last_seen}
      >
        {relativeTime(row.last_seen)}
      </span>
    </div>
  )
}

function RunningNow() {
  const queryClient = useQueryClient()
  const meta = useQuery(metaQuery())
  const q = useQuery({
    queryKey: ["runs", "live-page"],
    queryFn: () => fetchRuns({ status: "running", parent: "*" }),
    refetchInterval: 5_000,
  })
  // An agent-wide subscription refreshes the list when runs start or
  // finish without waiting for the poll.
  const agents = Array.from(
    new Set((q.data?.runs ?? []).map((r) => r.agent).filter(Boolean))
  )
  const agentsKey = agents.join(",")
  useEffect(() => {
    if (!agentsKey) return
    const handles = agents.map((agent) =>
      openLive({
        selector: { agent },
        kinds: ["run"],
        onRun: () => {
          void queryClient.invalidateQueries({ queryKey: ["runs", "live-page"] })
        },
        onOverflow: () => {
          void queryClient.invalidateQueries({ queryKey: ["runs", "live-page"] })
        },
      })
    )
    return () => handles.forEach((h) => h.close())
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentsKey, queryClient])
  const firstRender = useRef(true)
  useEffect(() => {
    firstRender.current = false
  }, [])
  void firstRender
  const runs = q.data?.runs ?? []

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="text-sm font-medium tracking-tight">Live</h1>
        <span className="font-mono text-xs text-faint tabular-nums">
          {q.isPending ? "…" : `${runs.length.toLocaleString()} running`}
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
      ) : runs.length === 0 ? (
        <p className="py-16 text-center font-mono text-xs text-faint">
          nothing is streaming right now — run something and it lands
          here the moment its run_start arrives
        </p>
      ) : (
        <div className="space-y-2">
          {runs.map((r) => (
            <RunLane key={r.id} run={r} />
          ))}
        </div>
      )}
    </div>
  )
}
