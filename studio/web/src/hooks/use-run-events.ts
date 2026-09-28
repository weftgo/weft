// The paged events reader behind the run page (ADR 0018 §8): walk
// api/runs/{id}/events page by page (foldMore semantics — every page
// appends and the fold is recomputed), and while the run is live,
// re-read from the last position every 2 s — the T2a live tail is this
// same endpoint streamed instead of polled.
import { useMemo } from "react"
import { useQuery } from "@tanstack/react-query"

import { apiBase } from "@/lib/api"
import type { EventsPage, WireEvent } from "@/lib/api"
import { fold } from "@/lib/events"
import type { FoldedRun } from "@/lib/events"

async function fetchPage(id: string, after: number): Promise<EventsPage> {
  const url = new URL(
    `runs/${encodeURIComponent(id)}/events?after=${after}&limit=500`,
    apiBase()
  )
  const res = await fetch(url.toString(), {
    headers: { Accept: "application/json" },
  })
  if (!res.ok) throw new Error(`events ${res.status}: ${res.statusText}`)
  return (await res.json()) as EventsPage
}

export interface RunStream {
  events: WireEvent[]
  folded: FoldedRun
  /** True when the endpoint says the run is over and drained. */
  done: boolean
}

export function useRunEvents(id: string, status: string): RunStream {
  // One query key per run: the walk restarts when invalidated (the
  // 2 s refetch of a running run's document invalidates it).
  const q = useQuery({
    queryKey: ["events-walk", id],
    refetchOnWindowFocus: false,
    refetchInterval: status === "running" ? 2000 : false,
    queryFn: async (): Promise<RunStream> => {
      const events: WireEvent[] = []
      let after = 0
      for (;;) {
        const page = await fetchPage(id, after)
        events.push(...page.events)
        if (page.next_after == null) {
          return { events, folded: fold(events), done: page.done }
        }
        after = page.next_after
      }
    },
  })
  const data = q.data
  return useMemo(
    () => data ?? { events: [], folded: fold([]), done: false },
    [data]
  )
}
