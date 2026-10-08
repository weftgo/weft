// The live page's rows (S4.7): everything running right now, kept
// current by the live lane. One stream per AGENT — the agent selector
// carries every run of that agent, record and run frames alike — and
// rows are updated in place from the run frames — run frames only: an
// agent stream asking for events is backfilled, on every browser
// reconnect, with every stored event of every run the agent ever made
// (the server's resume replays the whole selector). (A stream per run is
// a connection per run: a browser allows six per origin over
// HTTP/1.1, and a seventh running run — subagents count — left every
// later request of the tab queued behind the streams.) The agent
// streams are capped too; the rest of a large fleet rides the list's
// own 5 s poll.
import { useEffect, useMemo, useRef, useState } from "react"
import { Link } from "@tanstack/react-router"

import type { RunRow } from "@/lib/api"
import { openLive, throttle } from "@/lib/live"
import { elapsed, relativeTime } from "@/lib/format"
import { StatusChip } from "@/components/studio/runs-table"
import { runLink } from "@/lib/links"

/** Agent streams held open at once; a browser's per-origin budget is
 * six, and the page still needs its own requests. */
export const MAX_AGENT_STREAMS = 4

/** The fresher of the polled row and the last run frame for it. */
function newest(polled: RunRow, frame: RunRow | undefined): RunRow {
  if (!frame) return polled
  return Date.parse(frame.last_seen) >= Date.parse(polled.last_seen)
    ? frame
    : polled
}

function RunLane({ row }: { row: RunRow }) {
  return (
    <div
      className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-lg border bg-background px-3 py-2"
      data-run={row.id}
    >
      <StatusChip status={row.status} />
      <Link
        {...runLink(row.id)}
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
        title={`${row.event_count} durable event${row.event_count === 1 ? "" : "s"} recorded`}
      >
        {row.status === "running"
          ? `${elapsed(row.started)} · ${row.event_count} evt`
          : "ended"}
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

export function LiveRuns({
  runs,
  onStale,
}: {
  /** The running runs, as the list query last read them. */
  runs: RunRow[]
  /** The list is out of date (a run started or ended, or frames were
   * missed): refetch it. Called at most once a second. */
  onStale: () => void
}) {
  const [frames, setFrames] = useState<Record<string, RunRow>>({})
  const agents = useMemo(
    () => Array.from(new Set(runs.map((r) => r.agent).filter(Boolean))).sort(),
    [runs]
  )
  const streamed = agents.slice(0, MAX_AGENT_STREAMS)
  const streamedKey = streamed.join("\u0000")
  const known = useRef(new Set<string>())
  known.current = new Set(runs.map((r) => r.id))
  const onStaleRef = useRef(onStale)
  onStaleRef.current = onStale

  useEffect(() => {
    if (!streamedKey) return
    const stale = throttle(() => onStaleRef.current(), 1000)
    const handles = streamedKey.split("\u0000").map((agent) =>
      openLive({
        selector: { agent },
        kinds: ["run"],
        onRun: (f) => {
          setFrames((cur) => ({ ...cur, [f.run.id]: f.run }))
          // The list changes only when a run joins or leaves it.
          if (!known.current.has(f.run.id) || f.run.status !== "running")
            stale()
        },
        onOverflow: stale,
      })
    )
    return () => {
      stale.cancel()
      handles.forEach((h) => h.close())
    }
  }, [streamedKey])

  return (
    <div className="space-y-2">
      {runs.map((r) => (
        <RunLane
          key={r.id}
          row={newest(r, frames[r.id] as RunRow | undefined)}
        />
      ))}
      {agents.length > streamed.length ? (
        <p className="font-mono text-[11px] text-faint">
          streaming {streamed.length} of {agents.length} agents live (
          {streamed.join(", ")}); the others refresh with the list every 5 s
        </p>
      ) : null}
    </div>
  )
}
