// The run page (B1, B7, B9, B10; replay lands next step). Every view
// is a URL: step selection, view=steps|raw, and the replay position
// live in search params (A3) — a paste into an issue reproduces the
// exact view. A running run's document and stream refresh every 2 s
// until the status leaves running (plan §4.5); that is the only
// live-ish behaviour in T1.
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useEffect, useState } from "react"

import { runQuery } from "@/lib/api"
import { crossCheck } from "@/lib/events"
import { RunHeader } from "@/components/studio/run-header"
import { RawView } from "@/components/studio/raw-view"
import { ReplayBar } from "@/components/studio/replay-bar"
import { StepList } from "@/components/studio/step-list"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useRunEvents } from "@/hooks/use-run-events"

interface RunSearch {
  step?: number
  view?: "steps" | "raw"
  t?: number
}

export const Route = createFileRoute("/runs/$id")({
  validateSearch: (search: Record<string, unknown>): RunSearch => ({
    step: typeof search.step === "number" ? search.step : undefined,
    view: search.view === "raw" ? "raw" : undefined,
    t: typeof search.t === "number" ? search.t : undefined,
  }),
  component: RunPage,
})

function RunPage() {
  const { id } = Route.useParams()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: "/runs/$id" })

  const run = useQuery({
    ...runQuery(id),
    refetchInterval: (q) => (q.state.data?.status === "running" ? 2000 : false),
  })
  const stream = useRunEvents(id, run.data?.status ?? "running")

  // The replay playhead: null = live. Seeks and pauses write t to the
  // URL (a paste reproduces the exact view, A3); playback ticks do
  // not — 60 ms of history churn is noise, and a reload mid-play
  // landing near the playhead is fine.
  const [playhead, setPlayhead] = useState<number | null>(search.t ?? null)
  const seek = (t: number | null) => {
    setPlayhead(t)
    void navigate({ search: (prev) => ({ ...prev, t: t ?? undefined }) })
  }

  // r toggles raw JSON (A4).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement
      if (
        el.tagName === "INPUT" ||
        el.tagName === "TEXTAREA" ||
        el.isContentEditable
      )
        return
      if (e.key === "r" && run.data) {
        void navigate({
          search: (prev) => ({
            ...prev,
            view: prev.view === "raw" ? undefined : "raw",
          }),
        })
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [navigate, run.data])

  // Dev-only bug signal: the folded stream and the store's result
  // document are two recordings of one run — mismatches are ours.
  useEffect(() => {
    if (import.meta.env.DEV && run.data?.result) {
      for (const m of crossCheck(stream.folded, run.data.result)) {
        console.warn(`studio fold mismatch (step ${m.step}): ${m.detail}`)
      }
    }
  }, [stream.folded, run.data?.result])

  if (run.isPending) {
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  }
  if (run.isError) {
    return (
      <div className="py-24 text-center text-sm text-status-bad">
        {run.error.message}
      </div>
    )
  }
  const doc = run.data

  return (
    <div className="space-y-4">
      <RunHeader doc={doc} />
      <ReplayBar
        events={stream.events}
        playhead={playhead}
        onTick={setPlayhead}
        onSeek={seek}
      />
      <Tabs
        value={search.view ?? "steps"}
        onValueChange={(v) =>
          void navigate({
            search: (prev) => ({
              ...prev,
              view: v === "raw" ? "raw" : undefined,
            }),
          })
        }
      >
        <TabsList>
          <TabsTrigger value="steps">steps</TabsTrigger>
          <TabsTrigger value="raw">raw</TabsTrigger>
        </TabsList>
        <TabsContent value="steps" className="mt-3">
          <StepList
            events={stream.events}
            folded={stream.folded}
            doc={doc}
            upTo={playhead ?? undefined}
          />
        </TabsContent>
        <TabsContent value="raw" className="mt-3">
          <RawView doc={doc} events={stream.events} />
        </TabsContent>
      </Tabs>
    </div>
  )
}
