// The run page (B1, B2, B7, B9, B10). Three views, all URLs (A3):
//
//   trace  (default) the flow strip, then a waterfall | detail split:
//          steps, tool calls and subagents as spans on the event axis
//          on the left, the selected span's data on the right
//          (detail / events / json). ?sel= names the span.
//   story  the step cards top to bottom.
//   raw    the events explorer and the document tree.
//
// The replay playhead (?t=) drives every view: the waterfall veils
// what is past it, and the story and the detail panel render the fold
// AT the playhead, so scrubbing replays the whole page. A running
// run's document and stream refresh every 2 s until the status leaves
// running (plan §4.5); that is the only live-ish behaviour in T1.
import { useQuery } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { useCallback, useEffect, useMemo, useState } from "react"

import { runQuery } from "@/lib/api"
import { crossCheck, fold } from "@/lib/events"
import { isPlainShortcut } from "@/lib/keys"
import { defaultSelection, flowFromFold, spansFromFold } from "@/lib/trace"
import { FlowStrip } from "@/components/studio/flow-strip"
import { RunHeader } from "@/components/studio/run-header"
import { RawView } from "@/components/studio/raw-view"
import { ReplayBar } from "@/components/studio/replay-bar"
import { SpanDetail } from "@/components/studio/span-detail"
import type { DetailMode } from "@/components/studio/span-detail"
import { StepList } from "@/components/studio/step-list"
import { Waterfall } from "@/components/studio/waterfall"
import { Button } from "@/components/ui/button"
import { Kbd } from "@/components/ui/kbd"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useRunEvents } from "@/hooks/use-run-events"

type View = "trace" | "story" | "raw"

interface RunSearch {
  step?: number
  view?: View
  raw?: "events" | "doc"
  /** The selected span's key (trace view): s0, c:call_1, r:<runId>. */
  sel?: string
  /** The detail panel's mode (trace view). */
  d?: DetailMode
  t?: number
}

export const Route = createFileRoute("/runs/$id")({
  validateSearch: (search: Record<string, unknown>): RunSearch => ({
    step: typeof search.step === "number" ? search.step : undefined,
    view:
      search.view === "raw" || search.view === "story"
        ? search.view
        : undefined,
    raw: search.raw === "doc" ? "doc" : undefined,
    sel: typeof search.sel === "string" && search.sel ? search.sel : undefined,
    d: search.d === "events" || search.d === "json" ? search.d : undefined,
    t:
      typeof search.t === "number" && search.t >= 0
        ? Math.floor(search.t)
        : undefined,
  }),
  component: RunPage,
})

function RunPage() {
  const { id } = Route.useParams()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: "/runs/$id" })
  const view: View = search.view ?? "trace"

  const run = useQuery({
    ...runQuery(id),
    refetchInterval: (q) => (q.state.data?.status === "running" ? 2000 : false),
  })
  const stream = useRunEvents(id, run.data?.status ?? "running")
  const runStatus = run.data?.status ?? "running"

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

  // Everything below renders the fold AT the playhead: one fold, one
  // shape, whether live or scrubbed (B2).
  const replaying = playhead !== null && playhead < stream.events.length
  const atPlayhead = useMemo(
    () => (replaying ? fold(stream.events, playhead) : stream.folded),
    [replaying, stream.events, stream.folded, playhead]
  )
  // While scrubbing the run reads as running: calls past the playhead
  // are "running", not "never completed".
  const viewStatus = replaying ? "running" : runStatus

  const spans = useMemo(
    () => spansFromFold(atPlayhead, stream.events.length, viewStatus),
    [atPlayhead, stream.events.length, viewStatus]
  )
  const flow = useMemo(
    () => flowFromFold(atPlayhead, viewStatus),
    [atPlayhead, viewStatus]
  )
  // The selection: ?sel= names a span key; absent, start where
  // something went wrong (the full fold decides, not the prefix).
  const fullSpans = useMemo(
    () => spansFromFold(stream.folded, stream.events.length, runStatus),
    [stream.folded, stream.events.length, runStatus]
  )
  const selKey = search.sel ?? defaultSelection(fullSpans)?.key
  const selected = spans.find((s) => s.key === selKey)
  const select = useCallback(
    (key: string) =>
      void navigate({
        search: (prev) => ({ ...prev, sel: key, view: undefined }),
        replace: true,
      }),
    [navigate]
  )

  // A "replay to here" from a step, call or raw row: seek there.
  const jump = useCallback(
    (t: number) => {
      setPlayhead(t)
      void navigate({ search: (prev) => ({ ...prev, t }) })
    },
    [navigate]
  )

  // Keys: r toggles raw, s the story, e the trace; j/k walk the
  // trace's rows (A4).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!isPlainShortcut(e) || !run.data) return
      const setView = (v: View) =>
        void navigate({
          search: (prev) => ({
            ...prev,
            view: prev.view === v || v === "trace" ? undefined : v,
          }),
        })
      if (e.key === "r") setView("raw")
      else if (e.key === "s") setView("story")
      else if (e.key === "e") setView("trace")
      else if ((e.key === "j" || e.key === "k") && view === "trace") {
        const i = spans.findIndex((s) => s.key === selKey)
        const next = spans.at(e.key === "j" ? i + 1 : Math.max(i - 1, 0))
        if (next) select(next.key)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [navigate, run.data, view, spans, selKey, select])

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
      <Tabs
        value={view}
        onValueChange={(v) =>
          void navigate({
            search: (prev) => ({
              ...prev,
              view: v === "raw" || v === "story" ? v : undefined,
            }),
          })
        }
      >
        <div className="flex flex-wrap items-center gap-3">
          <TabsList>
            <TabsTrigger value="trace">trace</TabsTrigger>
            <TabsTrigger value="story">story</TabsTrigger>
            <TabsTrigger value="raw">raw</TabsTrigger>
          </TabsList>
          <span className="hidden items-center gap-1 text-[11px] text-faint md:flex">
            <Kbd>e</Kbd>
            <Kbd>s</Kbd>
            <Kbd>r</Kbd> views · <Kbd>j</Kbd>
            <Kbd>k</Kbd> spans · <Kbd>space</Kbd> replay · <Kbd>[</Kbd>
            <Kbd>]</Kbd> step · <Kbd>?</Kbd> all keys
          </span>
          {stream.loading ? (
            <span className="ml-auto flex items-center gap-1.5 font-mono text-[11px] text-faint">
              <Spinner className="size-3" /> loading events…
            </span>
          ) : null}
        </div>

        <TabsContent value="trace" className="mt-3 space-y-3">
          <FlowStrip
            pills={flow}
            runStatus={viewStatus}
            selectedKey={selKey}
            onSelect={select}
          />
          <div className="grid items-start gap-3 lg:grid-cols-[minmax(0,1.05fr)_minmax(0,1fr)]">
            <Waterfall
              spans={spans}
              domain={[0, Math.max(0, stream.events.length - 1)]}
              playhead={playhead}
              onSeek={seek}
              onSelect={(sp) => select(sp.key)}
              selectedId={selected?.id}
              className="max-h-[60vh] lg:max-h-[calc(100vh-8rem)]"
            />
            <div className="lg:sticky lg:top-3 lg:max-h-[calc(100vh-8rem)]">
              <SpanDetail
                span={selected}
                view={atPlayhead}
                events={stream.events}
                doc={doc}
                runStatus={viewStatus}
                playhead={playhead}
                mode={search.d ?? "detail"}
                onMode={(m) =>
                  void navigate({
                    search: (prev) => ({
                      ...prev,
                      d: m === "detail" ? undefined : m,
                    }),
                    replace: true,
                  })
                }
                onJump={jump}
              />
            </div>
          </div>
          {selKey && !selected ? (
            <p className="font-mono text-[11px] text-faint">
              the selected span ({selKey}) is past the playhead — play forward
              or press End for live
            </p>
          ) : null}
        </TabsContent>
        <TabsContent value="story" className="mt-3">
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
