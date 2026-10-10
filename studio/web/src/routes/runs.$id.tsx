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
  studioToken,
  transcriptQuery,
  spansQuery,
} from "@/lib/api"
import type { RunRow, Span as TimedSpan } from "@/lib/api"
import { compactionsOf } from "@/lib/compaction"
import { applyTranscript, fold, linkView } from "@/lib/events"
import { isPlainShortcut } from "@/lib/keys"
import { overrideOf } from "@/lib/request-pane"
import { canReplay, continueHere, editPromptAndReplay, editTranscript, replayFromStep, rerun } from "@/lib/replay"
import { replayFromSearch, replaySearch } from "@/lib/links"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { editKey, impliedFromStep, putEdit, schemaOf } from "@/lib/edits"
import type { ReplayEdit } from "@/lib/edits"
import { replayBounds } from "@/lib/experiment-body"
import type { ReplayLinkState, RunSearch } from "@/lib/links"
import type { Span as TraceSpan } from "@/lib/trace"
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
import { ReplayContext, ReplayDrawer } from "@/components/studio/replay-drawer"
import type { ReplayRequest } from "@/components/studio/replay-drawer"
import { SpanDetail } from "@/components/studio/span-detail"
import { StepList } from "@/components/studio/step-list"
import { EditorContext } from "@/components/studio/transcript-editor"
import type { Editor } from "@/components/studio/transcript-editor"
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

// The page's search is lib/links.ts's RunSearch: step is the step
// ordinal (the loop's step index, the n of api/runs/{id}/steps/{n}),
// never an event position — the page maps it to the step's card
// (story) and span (trace) itself.

/**
 * knownSel is ?sel= when it names a span the page has: a key no row
 * carries (a call gone from the run, a typo) selects nothing and is
 * dropped, so ?step= or the default decides instead. A pre-G1 call key
 * (c:<call id>, ambiguous once a call id repeats across steps) maps to
 * the call in the linked step when there is one, else to the first
 * c:<step>:<call id> key.
 */
function knownSel(
  sel: string | undefined,
  step: number | undefined,
  fullSpans: TraceSpan[],
  timeSpans: TraceSpan[]
): string | undefined {
  if (!sel) return undefined
  if (fullSpans.some((s) => s.key === sel) || timeSpans.some((s) => s.key === sel)) return sel
  const call = /^c:([^:]+)$/.exec(sel)?.[1]
  if (!call) return undefined
  // A top-level call's key is c:<step|resume>:<call id>; a child's
  // carries its run id too and never matches this shape.
  const calls = fullSpans.filter((s) => {
    const at = s.key.split(":")[1]
    return s.kind === "tool" && s.key === `c:${at}:${call}`
  })
  return (calls.find((s) => s.key === `c:${step}:${call}`) ?? calls.at(0))?.key
}

/** How long after the end the run page waits for the run's
 * invoke_agent span (an OTLP exporter's batch lands after run_finish). */
const INVOKE_AGENT_WAIT_MS = 30_000

/** hasInvokeAgent: the run's own invoke_agent span is among these. */
function hasInvokeAgent(spans: TimedSpan[] | undefined, runId: string): boolean {
  return (spans ?? []).some(
    (s) => s.attrs["gen_ai.operation.name"] === "invoke_agent" && s.attrs["weft.run.id"] === runId
  )
}

/** timeStepKey is the time-axis row of step n: its chat span (the
 * core stamps weft.step.index on it), else any span of that step. */
function timeStepKey(timeSpans: TraceSpan[], n: number): string | undefined {
  const ofStep = timeSpans.filter((s) => {
    const v = s.timed?.span.attrs["weft.step.index"]
    return (typeof v === "number" || typeof v === "string") && String(v) === String(n)
  })
  const chat = ofStep.find((s) => {
    const sp = s.timed?.span
    return sp?.attrs["gen_ai.operation.name"] === "chat" || sp?.name.startsWith("chat")
  })
  return (chat ?? ofStep.at(0))?.key
}

export const Route = createFileRoute("/runs/$id")({
  validateSearch: (search: Record<string, unknown>): RunSearch => ({
    step:
      typeof search.step === "number" && Number.isInteger(search.step) && search.step >= 0
        ? search.step
        : undefined,
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
    // The replay drawer (G2): its verb, from_step and source run.
    ...replaySearch(replayFromSearch(search)),
  }),
  // One page per run: /runs/A → /runs/B is a fresh page, so nothing of
  // A's (an open drawer and its form, the transcript edits held, the
  // events explorer's linked row) survives onto B. The page's state is
  // its run's; B's URL decides B's drawer (the linked effect).
  remountDeps: ({ params }) => params.id,
  component: RunPage,
})

function RunPage() {
  const { id } = Route.useParams()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: "/runs/$id" })
  const view: View = search.view ?? "trace"
  const [layout, setLayout] = useState<Layout>(readLayout)
  const { has, caps, loading: capsLoading } = useCapabilities()
  const liveCapable = has("live")
  // The replay drawer (plan F1): its verbs are drawn when the server
  // has the playground and the bearer may act (a read-scoped panel
  // token may not) — hidden, not disabled, otherwise.
  const replayable = canReplay(caps, studioToken())
  const [replay, setReplay] = useState<{ req: ReplayRequest | null; n: number }>({
    req: null,
    n: 0,
  })
  // The transcript editor's edits (plan F2): one command's, made in
  // the Story and listed in the drawer; a verb's opening starts them
  // over from its draft, closing the drawer drops them.
  const [edits, setEdits] = useState<{ list: ReplayEdit[]; invalid: Record<string, string> }>({ list: [], invalid: {} })
  // The drawer's state is in the URL (G2): written with replace — an
  // opening is not a history entry — and read back on a fresh load.
  const writeReplay = useCallback(
    (r: ReplayLinkState | null) =>
      void navigate({ search: (prev) => ({ ...prev, ...replaySearch(r) }), replace: true }),
    [navigate]
  )
  const openReplay = useCallback(
    (req: ReplayRequest) => {
      setEdits({ list: req.draft.edits, invalid: {} })
      setReplay((cur) => ({ req, n: cur.n + 1 }))
      writeReplay({ verb: req.draft.verb, from: req.draft.fromStep, ...(req.runID !== id ? { of: req.runID } : {}) })
    },
    [writeReplay, id]
  )
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
  // The tab is titled while the run loads too (lib/title.ts).
  useDocumentTitle({ page: "run", id, status: run.data?.status })
  // A link with the drawer's state reopens it once, on that step, from
  // the verb's own draft (the edits were never in the link).
  // Once per run id: an in-app navigation to another run's link
  // reopens there too.
  const linked = useRef("")
  useEffect(() => {
    if (linked.current === id || capsLoading) return
    linked.current = id
    const r = replayFromSearch(search)
    if (!r) return
    // No drawer for this page (no playground, a read-scoped token): the
    // keys would name a drawer that is not open — cleared.
    if (!replayable) {
      writeReplay(null)
      return
    }
    const draft =
      r.verb === "rerun" ? rerun() : r.verb === "continue" ? continueHere() : r.verb === "edit_prompt" ? editPromptAndReplay(r.from) : replayFromStep(r.from)
    setEdits({ list: draft.edits, invalid: {} })
    setReplay((cur) => ({ req: { runID: r.of ?? id, draft }, n: cur.n + 1 }))
    // The link's edits were never in it: a reopened edit / edit_result
    // drawer is the rebuilt draft's verb, and the URL says so.
    if (draft.verb !== r.verb || draft.fromStep !== r.from)
      writeReplay({ verb: draft.verb, from: draft.fromStep, ...(r.of ? { of: r.of } : {}) })
  }, [capsLoading, replayable, search, id, writeReplay])
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
  // When the run ended for the spans' wait below: the transition above
  // when the page saw it, else — a cold open — the row's own end time
  // when that is inside the wait (the runs list → click flow, where
  // spans batch after run_finish), never earlier than now. An older
  // finished run opened cold never polls.
  const endSeen = useRef<{ id: string; at: number | null }>({ id, at: null })
  const loadedStatus = run.data?.status
  const loadedFinished = run.data?.finished
  useEffect(() => {
    const was = seenStatus.current.id === id ? seenStatus.current.status : undefined
    seenStatus.current = { id, status: loadedStatus }
    if (endSeen.current.id !== id) endSeen.current = { id, at: null }
    if (was === undefined && loadedStatus && loadedStatus !== "running" && loadedFinished) {
      const fin = Date.parse(loadedFinished)
      const now = Date.now()
      // A server clock ahead of this one counts as now: the window is
      // never longer than INVOKE_AGENT_WAIT_MS from the open.
      if (Number.isFinite(fin) && now - fin <= INVOKE_AGENT_WAIT_MS) endSeen.current.at = Math.min(fin, now)
    }
    if (was === "running" && loadedStatus && loadedStatus !== "running") {
      endSeen.current.at = Date.now()
      void queryClient.invalidateQueries({ queryKey: ["requests", id] })
      // The invoke_agent span (the override fingerprint) ships at run
      // end: the spans are read again too — joining a read the status
      // change itself started (the story view enables it now), never a
      // second one.
      void queryClient.invalidateQueries({ queryKey: ["spans", id] }, { cancelRefetch: false })
      // A step doc read while running carries running-time holes (no
      // spans yet): every cached step of the run is read again.
      void queryClient.invalidateQueries({ queryKey: ["step", id] })
    }
  }, [loadedStatus, loadedFinished, id, queryClient])
  // The run's timed spans (the time axis's rows, S4.7), fetched when
  // the trace view is on and the run has a trace — and, under the
  // requests capability, for the Request pane's "overridden by
  // experiment" chip: the run's invoke_agent span carries the
  // weft.override.* fingerprint (one query, shared with the trace).
  // That span ships only when the run ends, so the story view reads
  // the spans once the run is over (the end-of-run invalidation above
  // reads them again) and never polls them while it runs. Over OTLP
  // (weft dev, setup B) spans batch later than the run_finish record
  // that flips the status, so a read just after the end may come back
  // without the run's invoke_agent span: until one is seen, the spans
  // are read again every 2 s for 30 s after the end (endSeen) — a
  // transition the page observed, or a cold open of a run whose row
  // says it finished inside those 30 s; a run that finished earlier,
  // opened cold, reads its spans once and never polls.
  const spans = useQuery({
    ...spansQuery(id),
    enabled:
      (view === "trace" || (requestsCapable && runStatus !== "running")) &&
      Boolean(run.data?.trace_id),
    refetchInterval: (q) => {
      if (runStatus === "running") return view === "trace" ? 5000 : false
      const at = endSeen.current.id === id ? endSeen.current.at : null
      if (at === null || Date.now() - at > INVOKE_AGENT_WAIT_MS) return false
      return hasInvokeAgent(q.state.data?.spans, id) ? false : 2000
    },
  })
  const override = useMemo(() => overrideOf(spans.data?.spans, id), [spans.data, id])
  const requests = useMemo(
    () =>
      requestsCapable
        ? runRequests(requestsQ.data, {
            loading: requestsQ.isPending,
            error: requestsQ.isError ? requestsQ.error.message : undefined,
            running: runStatus === "running",
            ctx: {
              runId: id,
              agent: run.data?.agent,
              manifestHash: run.data?.manifest_hash,
              instructionsHash: run.data?.instructions_hash,
              override,
              transcript: transcript.data,
              compactions: compactionsOf(run.data),
            },
          })
        : undefined,
    [
      requestsCapable,
      requestsQ.data,
      requestsQ.isPending,
      requestsQ.isError,
      requestsQ.error,
      runStatus,
      id,
      run.data,
      override,
      transcript.data,
    ]
  )

  // The time axis is the trace view's: the story reads the spans only
  // for the override chip, and stays on the position axis (replay).
  const timed: TimedSpan[] = useMemo(
    () => (view === "trace" ? (spans.data?.spans ?? []) : []),
    [view, spans.data]
  )
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
  // events' attrs), survive scrubbing. Only steps whose step_finish
  // is at or before the playhead take their final words — replay stays
  // a prefix. Its unplaced batches are the whole run's: a batch whose
  // step the playhead has not reached yet is not a hole.
  const batches = transcript.data?.batches
  const atPlayhead = useMemo(() => {
    if (!replaying) return foldedNow
    const prefix = fold(stream.events, playhead, stream.attrs)
    if (!batches || runStatus === "running") return prefix
    // Only the finished steps take the overlay; it returns clones,
    // which replace the prefix's own by index.
    const overlaid = applyTranscript(
      { ...prefix, steps: prefix.steps.filter((st) => st.finish) },
      batches,
      { replace: true }
    )
    const byIndex = new Map(overlaid.steps.map((st) => [st.index, st]))
    return {
      ...prefix,
      steps: prefix.steps.map((st) => byIndex.get(st.index) ?? st),
      unplaced: foldedNow.unplaced,
    }
  }, [replaying, stream.events, stream.attrs, foldedNow, playhead, batches, runStatus])
  // While scrubbing the run reads as running: calls past the playhead
  // are "running", not "never completed".
  const viewStatus = replaying ? "running" : runStatus

  // The trace's calls carry their child's id (linkView, as the story's
  // do): the call detail's subagent block joins on it — never on the
  // bare call id, which may repeat across steps (A10).
  const children = run.data?.children
  const posSpans = useMemo(
    () =>
      spansFromFold(
        children ? linkView(atPlayhead, children) : atPlayhead,
        stream.events.length,
        viewStatus
      ),
    [atPlayhead, children, stream.events.length, viewStatus]
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
  // ?step= (a deep link to a step, lib/links.ts) selects that step's
  // row when no (known) span is named: on the position axis the step's
  // own span (the ordinal is its key's number), on the time axis the
  // step's chat span (weft.step.index), else the default.
  const onTime = axis === "time" && haveTime
  const stepKey =
    search.step === undefined
      ? undefined
      : onTime
        ? timeStepKey(timeSpans, search.step)
        : fullSpans.some((s) => s.key === `s${search.step}`)
          ? `s${search.step}`
          : undefined
  const selKey =
    knownSel(search.sel, search.step, fullSpans, timeSpans) ??
    stepKey ??
    defaultSelection(fullSpans)?.key
  const selected = traceSpans.find((s) => s.key === selKey)
  const select = useCallback(
    (key: string) =>
      void navigate({
        search: (prev) => ({ ...prev, sel: key, view: undefined }),
        replace: true,
      }),
    [navigate]
  )

  // A "jump to here" from a step, call or raw row: seek the playhead
  // there ("replay" is the playground's word: the replay drawer).
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
  // The editor (plan F2): on the run's own steps, where a replay can
  // keep them — only with the drawer's verbs, never on a running run.
  const maxFrom = transcript.data ? replayBounds(transcript.data.batches).max : null
  const editor: Editor | null =
    replayable && transcript.data && doc.status !== "running"
      ? {
          edits: edits.list,
          invalid: edits.invalid,
          maxFrom,
          schema: (tool) => schemaOf(requests?.steps, tool),
          put: (target, next, error) => {
            const k = editKey(target)
            const list = putEdit(edits.list, next ?? target, !next)
            const invalid = { ...edits.invalid }
            if (error) invalid[k] = error
            else delete invalid[k]
            setEdits({ list, invalid })
            // The edits feed the drawer's one command: opened (on this
            // run) when the first edit is made.
            if (replay.req?.runID !== doc.id) {
              const from = impliedFromStep(list)
              setReplay((cur) => ({
                req: { runID: doc.id, agent: doc.agent || undefined, draft: editTranscript(from, list) },
                n: cur.n + 1,
              }))
              writeReplay({ verb: "edit", from })
            }
          },
        }
      : null

  return (
    <ReplayContext.Provider value={replayable ? openReplay : null}>
    <EditorContext.Provider value={editor}>
    <div className="space-y-4">
      <RunHeader
        doc={doc}
        folded={atPlayhead}
        eventCount={Math.max(doc.event_count, stream.events.length)}
        transcript={transcript.data}
        gaps={stream.gaps}
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
      {/* While running: positions may still be in flight. Once over,
          the header's gap badge (the run's holes) says it. */}
      {runStatus === "running" && stream.gaps.length > 0 && (
        <div className="rounded-md border border-status-bad/30 px-3 py-2 font-mono text-xs text-status-bad">
          {stream.gaps.length >= 1000 ? "1,000+" : stream.gaps.length} recorded{" "}
          {stream.gaps.length === 1 ? "event is" : "events are"} missing from the
          database (position{stream.gaps.length === 1 ? "" : "s"}{" "}
          {stream.gaps.slice(0, 8).join(", ")}
          {stream.gaps.length > 8 ? ", …" : ""}) — still in flight, or lost
          on the way
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
                timed={timed}
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
            transcript={transcript.data}
            transcriptError={transcript.isError ? transcript.error.message : undefined}
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
      {replayable ? (
        <ReplayDrawer
          request={replay.req}
          requestKey={replay.n}
          edits={edits.list}
          invalid={Object.values(edits.invalid)}
          setEdits={(list) => setEdits((cur) => ({ ...cur, list }))}
          onClose={() => {
            setReplay((cur) => ({ ...cur, req: null }))
            setEdits({ list: [], invalid: {} })
            writeReplay(null)
          }}
          onStep={(from) =>
            void navigate({ search: (prev) => (prev.replay && prev.from !== from ? { ...prev, from } : prev), replace: true })
          }
        />
      ) : null}
    </div>
    </EditorContext.Provider>
    </ReplayContext.Provider>
  )
}
