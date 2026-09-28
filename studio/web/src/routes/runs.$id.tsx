// The run page (B1, B2, B7, B9, B10). Every view is a URL: step
// selection, view=steps|raw (and raw=events|doc), and the replay
// position live in search params (A3) — a paste into an issue
// reproduces the exact view. A running run's document and stream
// refresh every 2 s until the status leaves running (plan §4.5);
// that is the only live-ish behaviour in T1.
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { useCallback, useEffect, useMemo, useState } from "react"
import { ChevronRight } from "lucide-react"

import { runQuery } from "@/lib/api"
import { crossCheck } from "@/lib/events"
import { isPlainShortcut } from "@/lib/keys"
import { spansFromFold } from "@/lib/trace"
import type { Span } from "@/lib/trace"
import { RunHeader } from "@/components/studio/run-header"
import { RawView } from "@/components/studio/raw-view"
import { ReplayBar } from "@/components/studio/replay-bar"
import { StepList } from "@/components/studio/step-list"
import { Waterfall } from "@/components/studio/waterfall"
import { Button } from "@/components/ui/button"
import { Kbd } from "@/components/ui/kbd"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useRunEvents } from "@/hooks/use-run-events"

interface RunSearch {
  step?: number
  view?: "steps" | "raw"
  raw?: "events" | "doc"
  t?: number
  /** The trace panel, folded away: trace=0. */
  trace?: 0
}

export const Route = createFileRoute("/runs/$id")({
  validateSearch: (search: Record<string, unknown>): RunSearch => ({
    step: typeof search.step === "number" ? search.step : undefined,
    view: search.view === "raw" ? "raw" : undefined,
    raw: search.raw === "doc" ? "doc" : undefined,
    t:
      typeof search.t === "number" && search.t >= 0
        ? Math.floor(search.t)
        : undefined,
    trace: search.trace === 0 ? 0 : undefined,
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
  const seek = useCallback(
    (t: number | null) => {
      setPlayhead(t)
      void navigate({
        search: (prev) => ({ ...prev, t: t ?? undefined }),
        replace: true,
      })
    },
    [navigate]
  )
  // The URL owns the playhead: after a remount or back/forward
  // navigation whose search commit lands late, local state catches up
  // from t instead of silently flipping back to live.
  useEffect(() => {
    setPlayhead(search.t ?? null)
  }, [search.t])

  // A "replay to here" from a step, call or raw row: seek there and
  // show the steps, so the jump has something to land on.
  const jump = useCallback(
    (t: number) => {
      setPlayhead(t)
      void navigate({
        search: (prev) => ({ ...prev, t, view: undefined }),
      })
      window.scrollTo({ top: 0, behavior: "smooth" })
    },
    [navigate]
  )

  // The trace: the fold as spans (subagents included) on the stream
  // position axis, sharing the replay playhead. Selecting a span
  // lands on its step card or call row.
  const spans = useMemo(
    () =>
      spansFromFold(
        stream.folded,
        stream.events.length,
        run.data?.status ?? "running"
      ),
    [stream.folded, stream.events.length, run.data?.status]
  )
  const selectSpan = useCallback(
    (sp: Span) => {
      if (sp.target?.step !== undefined) {
        void navigate({
          search: (prev) => ({
            ...prev,
            step: sp.target?.step,
            view: undefined,
          }),
        })
      }
      if (sp.target?.call) {
        void navigate({ search: (prev) => ({ ...prev, view: undefined }) })
        const callId = sp.target.call
        requestAnimationFrame(() => {
          const el = document.querySelector(
            `[data-call="${CSS.escape(callId)}"]`
          )
          el?.scrollIntoView({ block: "center", behavior: "smooth" })
          el?.classList.add("flash")
          setTimeout(() => el?.classList.remove("flash"), 1200)
        })
      }
    },
    [navigate]
  )
  const traceOpen = search.trace !== 0

  // r toggles raw JSON (A4).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!isPlainShortcut(e)) return
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
    if (import.meta.env.DEV && run.data?.result && stream.done) {
      for (const m of crossCheck(stream.folded, run.data.result)) {
        console.warn(`studio fold mismatch (step ${m.step}): ${m.detail}`)
      }
    }
  }, [stream.folded, stream.done, run.data?.result])

  if (run.isPending) {
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  }
  if (run.isError) {
    return (
      <div className="mx-auto max-w-md space-y-3 py-24 text-center">
        <p className="font-mono text-xs text-faint">{id}</p>
        <p className="text-sm text-status-bad">{run.error.message}</p>
        <div className="flex justify-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => void run.refetch()}
          >
            retry
          </Button>
          <Button variant="ghost" size="sm" render={<Link to="/runs" />}>
            back to runs
          </Button>
        </div>
      </div>
    )
  }
  const doc = run.data

  return (
    <div className="space-y-4">
      <RunHeader
        doc={doc}
        folded={stream.folded}
        eventCount={Math.max(doc.event_count, stream.events.length)}
      />
      <ReplayBar
        events={stream.events}
        eventCount={doc.event_count}
        playhead={playhead}
        onTick={setPlayhead}
        onSeek={seek}
      />
      {stream.error && (
        <div className="rounded-md border border-status-bad/30 px-3 py-2 font-mono text-xs text-status-bad">
          event stream: {stream.error}
        </div>
      )}
      <section className="space-y-1.5">
        <button
          type="button"
          className="group/trace flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
          aria-expanded={traceOpen}
          onClick={() =>
            void navigate({
              search: (prev) => ({ ...prev, trace: traceOpen ? 0 : undefined }),
              replace: true,
            })
          }
        >
          <ChevronRight
            className={`size-3 transition-transform ${traceOpen ? "rotate-90" : ""}`}
          />
          <span className="eyebrow">trace</span>
          <span className="font-mono text-[11px] text-faint">
            {spans.length} spans · steps, tool calls and subagents on the event
            axis
          </span>
        </button>
        {traceOpen ? (
          <Waterfall
            spans={spans}
            domain={[0, Math.max(0, stream.events.length - 1)]}
            playhead={playhead}
            onSeek={seek}
            onSelect={selectSpan}
          />
        ) : null}
      </section>
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
        <div className="flex flex-wrap items-center gap-3">
          <TabsList>
            <TabsTrigger value="steps">steps</TabsTrigger>
            <TabsTrigger value="raw">raw</TabsTrigger>
          </TabsList>
          <span className="hidden items-center gap-1 text-[11px] text-faint md:flex">
            <Kbd>r</Kbd> raw · <Kbd>space</Kbd> replay · <Kbd>[</Kbd>
            <Kbd>]</Kbd> step · <Kbd>?</Kbd> all keys
          </span>
          {stream.loading ? (
            <span className="ml-auto flex items-center gap-1.5 font-mono text-[11px] text-faint">
              <Spinner className="size-3" /> loading events…
            </span>
          ) : null}
        </div>
        <TabsContent value="steps" className="mt-3">
          <StepList
            events={stream.events}
            folded={stream.folded}
            doc={doc}
            upTo={playhead ?? undefined}
            highlight={search.step}
            onJump={jump}
          />
        </TabsContent>
        <TabsContent value="raw" className="mt-3">
          <RawView
            doc={doc}
            events={stream.events}
            playhead={playhead}
            onJump={jump}
            surface={search.raw ?? "events"}
            onSurface={(s) =>
              void navigate({
                search: (prev) => ({
                  ...prev,
                  raw: s === "doc" ? "doc" : undefined,
                }),
              })
            }
          />
        </TabsContent>
      </Tabs>
    </div>
  )
}
