// The run page (B1, B2, B7, B9, B10; S4.5/S4.7): three views, all
// URLs (A3):
//
//   trace  (default) the flow strip, then a waterfall | detail split.
//          The waterfall's axis is time when the run has spans
//          (S4.7), positions otherwise — ?axis= picks, and replay
//          (the playhead) belongs to the position axis.
//   story  the step cards top to bottom, subagents lazy (S4.3).
//   raw    the events explorer and the document tree.
//
// The replay playhead (?t=) drives every view on the position axis.
// A running run's tail is the /api/live stream when the server has
// the lane (S4.5) — one stream per page: deltas stream, run frames
// refresh the document, a lost stream falls back to the paged poll. The document and transcript
// refresh every 2 s while running as the poll-shaped fallback.
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Columns2, Rows3 } from "lucide-react"

import {
  requestsQuery,
  runQuery,
  transcriptQuery,
  spansQuery,
} from "@/lib/api"
import type { RunRow, Span as TimedSpan } from "@/lib/api"
import { applyTranscript, fold } from "@/lib/events"
import { isPlainShortcut } from "@/lib/keys"
import {
  defaultSelection,
  flowFromFold,
  spansFromFold,
  spansFromTimed,
  timeDomain,
} from "@/lib/trace"
import { FlowStrip } from "@/components/studio/flow-strip"
import { RunHeader } from "@/components/studio/run-header"
import { RawView } from "@/components/studio/raw-view"
import { ReplayBar } from "@/components/studio/replay-bar"
import { SpanDetail } from "@/components/studio/span-detail"
import type { DetailMode } from "@/components/studio/span-detail"
import { StepList } from "@/components/studio/step-list"
import { runRequests } from "@/components/studio/step-request"
import { Waterfall } from "@/components/studio/waterfall"
import { Button } from "@/components/ui/button"
import { Kbd } from "@/components/ui/kbd"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useCapabilities } from "@/hooks/use-capabilities"
import { useRunEvents } from "@/hooks/use-run-events"

type View = "trace" | "story" | "raw"

/** The trace's layout: side by side, stacked, or by the width. */
type Layout = "auto" | "split" | "stack"
const LAYOUT_KEY = "studio.trace.layout"
function readLayout(): Layout {
  try {
    const v = localStorage.getItem(LAYOUT_KEY)
    if (v === "split" || v === "stack") return v
  } catch {
    // unreadable storage: auto
  }
  return "auto"
}

interface RunSearch {
  step?: number
  view?: View
  raw?: "events" | "doc"
  /** The selected span's key (trace view): s0, c:call_1, t:<span id>. */
  sel?: string
  /** The detail panel's mode (trace view). */
  d?: DetailMode
  /** The waterfall's axis: positions (replay) or time (spans). */
  axis?: "events" | "time"
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
    axis: search.axis === "time" ? "time" : undefined,
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
  const [layout, setLayout] = useState<Layout>(readLayout)
  const { has, loading: capsLoading } = useCapabilities()
  const liveCapable = has("live")
  const cycleLayout = () =>
    setLayout((l) => {
      const next: Layout =
        l === "auto" ? "split" : l === "split" ? "stack" : "auto"
      try {
        if (next === "auto") localStorage.removeItem(LAYOUT_KEY)
        else localStorage.setItem(LAYOUT_KEY, next)
      } catch {
        // unwritable storage: this page only
      }
      return next
    })
  const gridCols =
    layout === "split"
      ? "grid-cols-[minmax(0,1.05fr)_minmax(0,1fr)]"
      : layout === "stack"
        ? ""
        : "@3xl/trace:grid-cols-[minmax(0,1.05fr)_minmax(0,1fr)]"
  const stickyDetail =
    layout === "split"
      ? "sticky top-3 max-h-[calc(100vh-8rem)]"
      : layout === "stack"
        ? ""
        : "@3xl/trace:sticky @3xl/trace:top-3 @3xl/trace:max-h-[calc(100vh-8rem)]"

  const queryClient = useQueryClient()
  const run = useQuery({
    ...runQuery(id),
    refetchInterval: (q) => (q.state.data?.status === "running" ? 2000 : false),
  })
  const transcript = useQuery({
    ...transcriptQuery(id),
    // The transcript is the finished words: refresh while running,
    // settle when terminal.
    refetchInterval: () => (run.data?.status === "running" ? 2000 : false),
  })
  const runStatus = run.data?.status ?? "running"
  // Run frames refresh the document (usage, status, counts) without
  // waiting for the poll. The frame carries the row itself, so it is
  // written into the cache — a refetch per frame is a request per
  // written batch on a busy run. The children (and the finished
  // words, the spans) are refetched once, when the status changes.
  const onRunFrame = useCallback(
    (row: RunRow) => {
      const prev = queryClient.getQueryData(runQuery(id).queryKey)
      queryClient.setQueryData(runQuery(id).queryKey, (old) =>
        old ? { ...old, ...row } : old
      )
      if (!prev || prev.status !== row.status) {
        void queryClient.invalidateQueries({ queryKey: ["run", id] })
        void queryClient.invalidateQueries({ queryKey: ["transcript", id] })
        void queryClient.invalidateQueries({ queryKey: ["spans", id] })
      }
    },
    [id, queryClient]
  )
  // The walk waits for api/meta (the shell asks for it at the same
  // time): started without the live capability known, it would start
  // over from position 0 when the capability arrived — the whole
  // stream read twice.
  const stream = useRunEvents(capsLoading ? "" : id, runStatus, {
    live: liveCapable,
    onRun: onRunFrame,
  })

  // The run's request record (ADR 0028 §10), every page: each step
  // shows what it called the model with. Only where the server reports
  // the requests capability — the section is not drawn otherwise.
  const requestsCapable = has("requests")
  const requestsQ = useQuery({
    ...requestsQuery(id),
    enabled: requestsCapable && Boolean(run.data),
    refetchInterval: runStatus === "running" ? 2000 : false,
  })
  // The run ended (a live frame, or the 2 s poll of the row): read the
  // record once more — a request stored after the last poll must not
  // read as a gap on the last step.
  // Only a seen transition counts: a page opened on a finished run
  // reads the record once.
  const seenStatus = useRef<{ id: string; status?: string }>({ id })
  const loadedStatus = run.data?.status
  useEffect(() => {
    const was = seenStatus.current.id === id ? seenStatus.current.status : undefined
    seenStatus.current = { id, status: loadedStatus }
    if (was === "running" && loadedStatus && loadedStatus !== "running")
      void queryClient.invalidateQueries({ queryKey: ["requests", id] })
  }, [loadedStatus, id, queryClient])
  const requests = useMemo(
    () =>
      requestsCapable
        ? runRequests(requestsQ.data, {
            loading: requestsQ.isPending,
            error: requestsQ.isError ? requestsQ.error.message : undefined,
            running: runStatus === "running",
          })
        : undefined,
    [
      requestsCapable,
      requestsQ.data,
      requestsQ.isPending,
      requestsQ.isError,
      requestsQ.error,
      runStatus,
    ]
  )

  // The run's timed spans (the time axis's rows, S4.7), fetched when
  // the trace view is on and the run has a trace.
  const spans = useQuery({
    ...spansQuery(id),
    enabled: view === "trace" && Boolean(run.data?.trace_id),
    refetchInterval: runStatus === "running" ? 5000 : false,
  })
  const timed: TimedSpan[] = spans.data?.spans ?? []
  const haveTime = timed.length > 0
  const axis: "events" | "time" = search.axis ?? (haveTime ? "time" : "events")

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

  // Everything below renders the fold AT the playhead on the position
  // axis: one fold, one shape, whether live or scrubbed (B2). The
  // time axis has no playhead — durations do not replay.
  const replaying =
    axis === "events" && playhead !== null && playhead < stream.events.length
  const foldedNow =
    transcript.data && runStatus !== "running"
      ? applyTranscript(stream.folded, transcript.data.batches, { replace: true })
      : stream.folded
  // The replay fold goes through the same overlay: the transcript's
  // words and `derived` placements, and the recorder's badges (the
  // events' attrs), survive scrubbing. Its unplaced batches are the
  // whole run's — a batch whose step the playhead has not reached yet
  // is not a hole.
  const batches = transcript.data?.batches
  const atPlayhead = useMemo(() => {
    if (!replaying) return foldedNow
    const prefix = fold(stream.events, playhead, stream.attrs)
    if (!batches || runStatus === "running") return prefix
    const overlaid = applyTranscript(prefix, batches, { replace: true })
    overlaid.unplaced = foldedNow.unplaced
    return overlaid
  }, [replaying, stream.events, stream.attrs, foldedNow, playhead, batches, runStatus])
  // While scrubbing the run reads as running: calls past the playhead
  // are "running", not "never completed".
  const viewStatus = replaying ? "running" : runStatus

  const posSpans = useMemo(
    () => spansFromFold(atPlayhead, stream.events.length, viewStatus),
    [atPlayhead, stream.events.length, viewStatus]
  )
  const timeSpans = useMemo(() => spansFromTimed(timed), [timed])
  const traceSpans = axis === "time" && haveTime ? timeSpans : posSpans
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
  const selected = traceSpans.find((s) => s.key === selKey)
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
      void navigate({
        search: (prev) => ({
          ...prev,
          t,
          axis: prev.axis === "time" ? undefined : prev.axis,
        }),
      })
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
        const i = traceSpans.findIndex((s) => s.key === selKey)
        const next = traceSpans.at(e.key === "j" ? i + 1 : Math.max(i - 1, 0))
        if (next) select(next.key)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [navigate, run.data, view, traceSpans, selKey, select])

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
        folded={atPlayhead}
        eventCount={Math.max(doc.event_count, stream.events.length)}
        transcript={transcript.data}
      />
      {axis === "events" ? (
        <ReplayBar
          events={stream.events}
          eventCount={doc.event_count}
          playhead={playhead}
          onTick={setPlayhead}
          onSeek={seek}
        />
      ) : null}
      {stream.gaps.length > 0 && (
        <div className="rounded-md border border-status-bad/30 px-3 py-2 font-mono text-xs text-status-bad">
          {stream.gaps.length >= 1000 ? "1,000+" : stream.gaps.length} recorded{" "}
          {stream.gaps.length === 1 ? "event is" : "events are"} missing from the
          database (position{stream.gaps.length === 1 ? "" : "s"}{" "}
          {stream.gaps.slice(0, 8).join(", ")}
          {stream.gaps.length > 8 ? ", …" : ""}) —{" "}
          {runStatus === "running"
            ? "still in flight, or lost on the way"
            : "lost on the way: the story has holes there"}
        </div>
      )}
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
          {view === "trace" && haveTime ? (
            <div className="flex gap-1">
              {(["events", "time"] as const).map((a) => (
                <button
                  key={a}
                  type="button"
                  className={`rounded-md border px-2 py-1 font-mono text-[11px] ${
                    axis === a
                      ? "border-thread/60 bg-secondary"
                      : "text-muted-foreground hover:text-foreground"
                  }`}
                  title={
                    a === "events"
                      ? "the event axis: order, and the replay playhead"
                      : "the time axis: real durations from the run's spans"
                  }
                  onClick={() =>
                    void navigate({
                      search: (prev) => ({
                        ...prev,
                        axis: a === "time" ? "time" : undefined,
                        sel: undefined,
                      }),
                      replace: true,
                    })
                  }
                >
                  {a}
                </button>
              ))}
            </div>
          ) : null}
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

        <TabsContent value="trace" className="@container/trace mt-3 space-y-3">
          <div className="flex items-start gap-2">
            <div className="min-w-0 flex-1">
              <FlowStrip
                pills={flow}
                runStatus={viewStatus}
                selectedKey={selKey}
                onSelect={select}
              />
            </div>
            <Button
              variant="ghost"
              size="icon-sm"
              className="shrink-0 text-muted-foreground"
              aria-label={`layout: ${layout}`}
              title={
                layout === "auto"
                  ? "layout: side by side when the page is wide enough (click: always side by side)"
                  : layout === "split"
                    ? "layout: side by side (click: stacked)"
                    : "layout: stacked (click: automatic)"
              }
              onClick={cycleLayout}
            >
              {layout === "stack" ? (
                <Rows3 data-slot="icon" />
              ) : (
                <Columns2 data-slot="icon" />
              )}
            </Button>
          </div>
          <div className={`grid items-start gap-3 ${gridCols}`}>
            <Waterfall
              spans={traceSpans}
              domain={
                axis === "time" && haveTime
                  ? timeDomain(timed)
                  : [0, Math.max(0, stream.events.length - 1)]
              }
              unit={axis === "time" ? "ms" : "event"}
              playhead={axis === "events" ? playhead : null}
              onSeek={axis === "events" ? seek : undefined}
              onSelect={(sp) => select(sp.key)}
              selectedId={selected?.id}
              className="max-h-[60vh] @3xl/trace:max-h-[calc(100vh-8rem)]"
            />
            <div className={stickyDetail}>
              <SpanDetail
                span={selected}
                events={stream.events}
                doc={doc}
                runStatus={viewStatus}
                playhead={axis === "events" ? playhead : null}
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
                requests={requests}
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
            folded={foldedNow}
            atPlayhead={atPlayhead}
            doc={doc}
            upTo={axis === "events" ? (playhead ?? undefined) : undefined}
            highlight={search.step}
            onJump={jump}
            requests={requests}
          />
        </TabsContent>
        <TabsContent value="raw" className="mt-3">
          <RawView
            doc={doc}
            events={stream.events}
            eventHoles={stream.folded.eventHoles}
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
