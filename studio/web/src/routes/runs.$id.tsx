// The run page (B1, B2, B7, B9, B10; S4.5/S4.7; U8.2; U11.2): four views,
// all URLs (A3):
//
//   waterfall  (default) the flow strip, then the run tree | detail
//              split. The tree is lib/waterfall.ts's waterfallRows
//              (run › step › model call / tool lane › child run, gists,
//              durations, the critical path) on the time scale when
//              the run has spans, the order scale (stream positions)
//              otherwise — ?scale=time|order picks, and replay (the
//              playhead) belongs to the order scale.
//   story      the step cards top to bottom, subagents lazy (S4.3).
//   raw        the events explorer and the document tree.
//   logs       the run's app log lines (U11.2, audit §6.9): a severity
//              filter (?lsev=), every page up to lib/logs.ts's cap, and
//              per line "jump to span" — the run tree row it was logged
//              under, its drawer's Logs tab open. The tree's rows carry
//              a WARN / ERROR tick where such a line landed.
//
// "trace" is the /traces page's word (an OTel trace); the run page
// draws a run tree.
//
// The replay playhead (?t=) drives every view on the order scale.
// A running run's tail is the /api/live stream when the server has
// the lane (S4.5) — one stream per page: deltas stream, run frames
// refresh the document, a lost stream falls back to the paged poll. The document and transcript
// refresh every 2 s while running as the poll-shaped fallback.
import { storyChildren } from "@/lib/delegation"
import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  createFileRoute,
  Link,
  useNavigate,
  useRouter,
} from "@tanstack/react-router"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Columns2, Rows3 } from "lucide-react"

import {
  CHILD_EVENTS_CAP,
  childStreamQuery,
  requestsQuery,
  runQuery,
  studioToken,
  transcriptQuery,
  spansQuery,
} from "@/lib/api"
import type {
  ChildStream,
  CostFields,
  RunRow,
  Span as TimedSpan,
  TreeHole,
} from "@/lib/api"
import { compactionsOf } from "@/lib/compaction"
import { compactionBadges } from "@/lib/compaction-studio"
import { approvalFactsOf } from "@/lib/approvals-view"
import { busyEndSpans } from "@/lib/timeline"
import { useBusyEnd } from "@/components/studio/busy-end"
import { applyTranscript, fold, linkView } from "@/lib/events"
import { isPlainShortcut, VIEW_KEYS } from "@/lib/keys"
import { overrideOf, overrideParallelism } from "@/lib/request-pane"
import { callFactsOf } from "@/lib/cards"
import { stepEffectsOf } from "@/lib/onion"
import { toolFactsOf } from "@/lib/error-kind"
import {
  canReplay,
  continueHere,
  editPromptAndReplay,
  editTranscript,
  replayFromStep,
  rerun,
} from "@/lib/replay"
import {
  replayFromSearch,
  replaySearch,
  runLink,
  traceSelectionLink,
  VIEWS,
} from "@/lib/links"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { editKey, impliedFromStep, putEdit, schemaOf } from "@/lib/edits"
import type { ReplayEdit } from "@/lib/edits"
import { replayBounds } from "@/lib/experiment-body"
import type {
  AppTab,
  ReplayLinkState,
  RunScale,
  RunSearch,
  ViewName,
} from "@/lib/links"
import { failedAtStep } from "@/lib/summarize"
import { flowFromFold, spansFromFold } from "@/lib/trace"
import {
  ALL_RUNS,
  CHILD_CAPPED_FIX,
  childCappedReason,
  childStreamPolls,
  defaultRowSel,
  detailSpan,
  drawerTabOf,
  stepRowOf,
  gapNote,
  expandFor,
  qualifyKey,
  resolveRowSel,
  TRACE_HINT,
  TRACE_LINK,
  treeSpans,
} from "@/lib/run-tree"
import {
  CRITICAL,
  CRITICAL_HINT,
  DIM_LABEL,
  DIM_WORDS,
  DIMS,
  dimParam,
  dimRows,
  OPEN_RUN,
  parseDim,
  rowKeys,
  SCALE_HINT,
  str,
  UNTIMED_HOLE,
  UNTIMED_NOTE,
} from "@/lib/waterfall"
import type { Scale, WaterfallRow } from "@/lib/waterfall"
import { costNotRead, costView, sumCost } from "@/lib/cost"
import type { HoleMark } from "@/lib/honesty"
// The ?tree=1 read: the descendant rules too (lib/waterfall-tree.ts).
import { treeRows as waterfallRows } from "@/lib/waterfall-tree"
import type { FoldedRun } from "@/lib/events"
import {
  ROWS_LEVEL,
  callGraph,
  catalogSources,
  nodeOfRun,
  rowsCallGraph,
} from "@/lib/graph"
import { CallGraphView } from "@/components/studio/call-graph"
import { LoopGraphView } from "@/components/studio/loop-graph"
import {
  callOf,
  callStepper,
  catalogTools,
  DEEP_HANDOFF_DEPTH,
  LEVEL_HINT,
  LEVELS,
  levelOf,
  LOOP_HINT,
  loopGraph,
  loopSig,
  nodeOfKey,
  NO_SPANS_LEVEL,
  OFFERED_NONE,
  OFFERED_UNREAD,
  offeredUnreadable,
  spansUnreadable,
  runChain,
  NO_SEL,
} from "@/lib/loopgraph"
import type { GraphLevel } from "@/lib/loopgraph"
import { HoleBadge, HoleBadges } from "@/components/studio/hole-badge"
import { StepStrip } from "@/components/studio/step-strip"
import { TermWrap } from "@/components/studio/term"
import { RunHeader, runPlace } from "@/components/studio/run-header"
import { saveTest } from "@/components/studio/saves"
import { notifySave } from "@/lib/notify"
import { mayExportTestWith } from "@/lib/saves"
import { RawView } from "@/components/studio/raw-view"
import { LogsView } from "@/components/studio/logs-view"
import {
  allLogsQuery,
  jumpSel,
  logLevelOf,
  LOGS,
  logTicks,
  severityClass,
} from "@/lib/logs"
import { DerivedNote, DerivedStory } from "@/components/studio/derived-story"
import { derivedWords, isDerived, isLive } from "@/lib/honesty"
import { ReplayBar } from "@/components/studio/replay-bar"
import { LoadFailed } from "@/components/studio/empty-state"
import { ReplayContext, ReplayDrawer } from "@/components/studio/replay-drawer"
import type { ReplayRequest } from "@/components/studio/replay-drawer"
import { SpanDetail } from "@/components/studio/span-detail"
import { RunCards, StepList } from "@/components/studio/step-list"
import { EditorContext } from "@/components/studio/transcript-editor"
import type { Editor } from "@/components/studio/transcript-editor"
import { runRequests } from "@/components/studio/step-request"
import { Waterfall } from "@/components/studio/waterfall"
import { Button } from "@/components/ui/button"
import { NotFound } from "@/components/studio/not-found"
import { isNotFound } from "@/lib/not-found"
import { Kbd } from "@/components/ui/kbd"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useCapabilities } from "@/hooks/use-capabilities"
import { storyRunID, useRunEvents } from "@/hooks/use-run-events"
import { STATUS_TEXT } from "@/lib/status-class"

type View = "waterfall" | "story" | "raw" | "logs"

/** The waterfall's layout: side by side, stacked, or by the width. */
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
// never an event position — the page maps it to the step's row
// itself. ?sel= is a run tree row key (lib/waterfall.ts's rowKeys); a
// key no row carries (a call gone from the run, a typo) selects
// nothing, so ?step= or the default decides instead; old forms
// resolve (lib/run-tree.ts's resolveRowSel).

/** useQueries' combine: the data alone (structurally shared, so a
 * memo on it moves only when an answer does). */
const dataOf = <T,>(rs: { data?: T }[]) => rs.map((r) => r.data)

/** The scales the switch offers, in its order. */
const SCALES = ["time", "order", "graph"] as const
const scaleHint = (a: RunScale) => (a === "graph" ? LOOP_HINT : SCALE_HINT[a])

/** How long after the end the run page waits for the run's
 * invoke_agent span (an OTLP exporter's batch lands after run_finish). */
const INVOKE_AGENT_WAIT_MS = 30_000

/** hasInvokeAgent: the run's own invoke_agent span is among these. */
function hasInvokeAgent(
  spans: TimedSpan[] | undefined,
  runId: string
): boolean {
  return (spans ?? []).some(
    (s) =>
      s.attrs["gen_ai.operation.name"] === "invoke_agent" &&
      s.attrs["weft.run.id"] === runId
  )
}

/** A scale a search value names; "events" is the old axis's order. */
function scaleOf(v: unknown): RunScale | undefined {
  return v === "time" || v === "order" || v === "graph"
    ? v
    : v === "events"
      ? "order"
      : undefined
}

/** appAt is where "open in app" lands the panel (U21.2, UX-F-28): the
 * page's step and its view as the panel's tab — a step's Request pane
 * is the panel's request tab; logs, which the panel has not, none. */
function appAt(search: RunSearch): { step?: number; tab?: AppTab } {
  const tab: AppTab | undefined =
    search.pane === "request" || search.pane === "wire"
      ? "request"
      : search.view === "story" || search.view === "raw"
        ? search.view
        : search.view === undefined
          ? "waterfall"
          : undefined
  return { step: search.step, tab }
}

export const Route = createFileRoute("/runs/$id")({
  validateSearch: (search: Record<string, unknown>): RunSearch => ({
    step:
      typeof search.step === "number" &&
      Number.isInteger(search.step) &&
      search.step >= 0
        ? search.step
        : undefined,
    view:
      search.view === "raw" || search.view === "story" || search.view === "logs"
        ? search.view
        : undefined,
    // The logs view's severity filter (U11.2).
    lsev: logLevelOf(search.lsev),
    raw: search.raw === "doc" ? "doc" : undefined,
    pane:
      search.pane === "request" || search.pane === "wire"
        ? search.pane
        : undefined,
    sel: typeof search.sel === "string" && search.sel ? search.sel : undefined,
    d: search.d === "events" || search.d === "json" ? search.d : undefined,
    // The drawer's tab (U10.1): round-trips; an unknown one is dropped.
    tab: drawerTabOf(search.tab),
    // The run tree's scale (U8.2, UX-A-02: both values round-trip); a
    // pre-U8.2 link's ?axis=time|events reads as time|order.
    scale: scaleOf(search.scale) ?? scaleOf(search.axis),
    // The graph's level (U34): the loop (absent) or the agents' call graph.
    level: levelOf(search.level) === "agents" ? "agents" : undefined,
    // The tree's dim filters (UX-A-04): written in DIMS's order.
    dim: dimParam(parseDim(search.dim)),
    t:
      typeof search.t === "number" && search.t >= 0
        ? Math.floor(search.t)
        : undefined,
    // The replay drawer (G2): its verb, from_step and source run.
    ...replaySearch(replayFromSearch(search)),
    // Save as test on landing (plan F6, the panel's hand-off).
    save: search.save === "test" ? "test" : undefined,
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
  const router = useRouter()
  // A step's pane (A6.2) lives on its story card: ?pane= opens there.
  const view: View =
    search.view ??
    (search.pane && search.step !== undefined ? "story" : "waterfall")
  // The view a view key toggles from (U21.1-fix): the page's view, a
  // step's open Request pane reading as the request view (lib/keys.ts's
  // VIEW_KEYS); the view before it is where a second press goes back,
  // as the panel's tabs do.
  const keyView: ViewName =
    view === "story" && search.pane === "request" ? "request" : view
  const lastView = useRef<ViewName>(keyView)
  const prevView = useRef<ViewName>("waterfall")
  useEffect(() => {
    if (keyView === lastView.current) return
    prevView.current = lastView.current
    lastView.current = keyView
  }, [keyView])
  const [layout, setLayout] = useState<Layout>(readLayout)
  const { has, caps, why: capsWhy, loading: capsLoading } = useCapabilities()
  const liveCapable = has("live")
  // The replay drawer (plan F1): its verbs are drawn when the server
  // has the playground and the bearer may act (a read-scoped panel
  // token may not) — hidden, not disabled, otherwise.
  const replayable = canReplay(caps, studioToken())
  const [replay, setReplay] = useState<{
    req: ReplayRequest | null
    n: number
  }>({
    req: null,
    n: 0,
  })
  // The transcript editor's edits (plan F2): one command's, made in
  // the Story and listed in the drawer; a verb's opening starts them
  // over from its draft, closing the drawer drops them.
  const [edits, setEdits] = useState<{
    list: ReplayEdit[]
    invalid: Record<string, string>
  }>({ list: [], invalid: {} })
  // The drawer's state is in the URL (G2): written with replace — an
  // opening is not a history entry — and read back on a fresh load.
  const writeReplay = useCallback(
    (r: ReplayLinkState | null) =>
      void navigate({
        search: (prev) => ({ ...prev, ...replaySearch(r) }),
        replace: true,
      }),
    [navigate]
  )
  const openReplay = useCallback(
    (req: ReplayRequest) => {
      setEdits({ list: req.draft.edits, invalid: {} })
      setReplay((cur) => ({ req, n: cur.n + 1 }))
      writeReplay({
        verb: req.draft.verb,
        from: req.draft.fromStep,
        ...(req.runID !== id ? { of: req.runID } : {}),
      })
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
  // Stacked, the one column is minmax(0,1fr), never the implicit auto
  // track: an auto track grows to its widest item's min-content (the
  // drawer's call rows), past a 360 px page (H9, P8-gate-fix).
  const gridCols =
    layout === "split"
      ? "grid-cols-[minmax(0,1.05fr)_minmax(0,1fr)]"
      : layout === "stack"
        ? "grid-cols-[minmax(0,1fr)]"
        : "grid-cols-[minmax(0,1fr)] @3xl/trace:grid-cols-[minmax(0,1.05fr)_minmax(0,1fr)]"
  const stickyDetail =
    layout === "split"
      ? "sticky top-3 max-h-[calc(100vh-8rem)]"
      : layout === "stack"
        ? ""
        : "@3xl/trace:sticky @3xl/trace:top-3 @3xl/trace:max-h-[calc(100vh-8rem)]"

  const queryClient = useQueryClient()
  const run = useQuery({
    ...runQuery(id),
    // Running or settling (lib/honesty.ts's isLive): a run opened while
    // its records trail its root is read again until it settles.
    refetchInterval: (q) => (isLive(q.state.data) ? 2000 : false),
  })

  // ?save=test (plan F6: the devtools panel's "save as test" hands off
  // here): the zip downloads once with this page's bearer, and the key
  // is dropped so a reload or a copied link does not save again. It
  // waits for the run: a derived run (plan I1.3) recorded no transcript
  // and the export refuses it, so it is refused here in the disabled
  // button's words and never fetched.
  const saved = useRef("")
  const runPending = run.isPending
  const runDerived = isDerived(run.data)
  useEffect(() => {
    if (
      search.save !== "test" ||
      capsLoading ||
      runPending ||
      saved.current === id
    )
      return
    saved.current = id
    void navigate({
      search: (prev) => ({ ...prev, save: undefined }),
      replace: true,
    })
    if (runDerived)
      notifySave({ kind: "save-test", ok: false, message: derivedWords() })
    else if (mayExportTestWith(caps, studioToken())) void saveTest(id)
    else
      notifySave({
        kind: "save-test",
        ok: false,
        message: caps.includes("export")
          ? "a read-scoped panel token does not read the run's prompts"
          : `this Studio has no export capability${capsWhy("export") ? `: ${capsWhy("export")}` : ""}`,
      })
  }, [
    search.save,
    capsLoading,
    runPending,
    runDerived,
    caps,
    capsWhy,
    id,
    navigate,
  ])
  // The tab is titled while the run loads too (lib/title.ts).
  useDocumentTitle(runPlace(run.data ?? { id }, !!run.data))
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
      r.verb === "rerun"
        ? rerun()
        : r.verb === "continue"
          ? continueHere()
          : r.verb === "edit_prompt"
            ? editPromptAndReplay(r.from)
            : replayFromStep(r.from)
    // No setEdits: a linked verb's draft carries none, and the page
    // remounts per run id (remountDeps), so it starts with none held.
    setReplay((cur) => ({ req: { runID: r.of ?? id, draft }, n: cur.n + 1 }))
    // The link's edits were never in it: a reopened edit / edit_result
    // drawer is the rebuilt draft's verb, and the URL says so.
    if (draft.verb !== r.verb || draft.fromStep !== r.from)
      writeReplay({
        verb: draft.verb,
        from: draft.fromStep,
        ...(r.of ? { of: r.of } : {}),
      })
  }, [capsLoading, replayable, search, id, writeReplay])
  const transcript = useQuery({
    ...transcriptQuery(id),
    // The transcript is the finished words: refresh while live
    // (running or settling), settle when terminal.
    refetchInterval: () => (isLive(run.data) ? 2000 : false),
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
  // stream read twice. It waits for the run's document too, and a
  // deleted run (404) is never followed (storyRunID).
  const stream = useRunEvents(
    capsLoading ? "" : storyRunID(id, run),
    runStatus,
    {
      live: liveCapable,
      settling: run.data?.settling === true,
      onRun: onRunFrame,
    }
  )

  // The run's request record (ADR 0028 §10), every page: each step
  // shows what it called the model with. Only where the server reports
  // the requests capability — the section is not drawn otherwise.
  const requestsCapable = has("requests")
  const requestsQ = useQuery({
    ...requestsQuery(id),
    enabled: requestsCapable && Boolean(run.data),
    refetchInterval: isLive(run.data) ? 2000 : false,
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
    const was =
      seenStatus.current.id === id ? seenStatus.current.status : undefined
    seenStatus.current = { id, status: loadedStatus }
    if (endSeen.current.id !== id) endSeen.current = { id, at: null }
    if (
      was === undefined &&
      loadedStatus &&
      loadedStatus !== "running" &&
      loadedFinished
    ) {
      const fin = Date.parse(loadedFinished)
      const now = Date.now()
      // A server clock ahead of this one counts as now: the window is
      // never longer than INVOKE_AGENT_WAIT_MS from the open.
      if (Number.isFinite(fin) && now - fin <= INVOKE_AGENT_WAIT_MS)
        endSeen.current.at = Math.min(fin, now)
    }
    if (was === "running" && loadedStatus && loadedStatus !== "running") {
      endSeen.current.at = Date.now()
      void queryClient.invalidateQueries({ queryKey: ["requests", id] })
      // The invoke_agent span (the override fingerprint) ships at run
      // end: the spans are read again too — joining a read the status
      // change itself started (the story view enables it now), never a
      // second one.
      void queryClient.invalidateQueries(
        { queryKey: ["spans", id] },
        { cancelRefetch: false }
      )
      // A step doc read while running carries running-time holes (no
      // spans yet): every cached step of the run is read again.
      void queryClient.invalidateQueries({ queryKey: ["step", id] })
    }
  }, [loadedStatus, loadedFinished, id, queryClient])
  // A settling run (lib/honesty.ts's isLive) that stops settling: its
  // trailing records landed or the window passed (a tail_lost gap) —
  // the requests and every cached step are read once more, as at the
  // end of a run the page saw running.
  const loadedSettling = run.data?.settling === true
  const seenSettling = useRef<{ id: string; settling: boolean }>({
    id,
    settling: false,
  })
  useEffect(() => {
    const was = seenSettling.current.id === id && seenSettling.current.settling
    seenSettling.current = { id, settling: loadedSettling }
    if (!was || loadedSettling) return
    void queryClient.invalidateQueries({ queryKey: ["requests", id] })
    void queryClient.invalidateQueries({ queryKey: ["step", id] })
  }, [loadedSettling, id, queryClient])
  // The run's timed spans (the time scale's rows, S4.7), fetched when
  // the waterfall is on and the run has a trace — and, under the
  // requests capability, for the Request pane's "overridden by
  // experiment" chip: the run's invoke_agent span carries the
  // weft.override.* fingerprint (one query, shared with the tree).
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
  // With capability spans_tree (U9.1) the waterfall reads the
  // descendants' spans too: the run tree's child rows read them (each
  // span names its run); the story needs the run's own only. The read
  // waits for api/meta (U8.2-fix): started before the capabilities are
  // known, it would read the plain spans, then the tree again.
  const treeRead = has("spans_tree") && view === "waterfall"
  const spans = useQuery({
    ...spansQuery(id, treeRead),
    enabled:
      !capsLoading &&
      (view === "waterfall" || (requestsCapable && runStatus !== "running")) &&
      Boolean(run.data?.trace_id),
    refetchInterval: (q) => {
      if (runStatus === "running") return view === "waterfall" ? 5000 : false
      const at = endSeen.current.id === id ? endSeen.current.at : null
      if (at === null || Date.now() - at > INVOKE_AGENT_WAIT_MS) return false
      return hasInvokeAgent(q.state.data?.spans, id) ? false : 2000
    },
  })
  // The run's own spans (a tree read carries its descendants' too).
  const ownSpans = useMemo(
    () =>
      spans.data?.spans.filter((s) => !s.run_id || s.run_id === id) ??
      undefined,
    [spans.data, id]
  )
  const override = useMemo(() => overrideOf(ownSpans, id), [ownSpans, id])
  const toolFacts = useMemo(() => toolFactsOf(ownSpans, id), [ownSpans, id])
  const callFacts = useMemo(() => callFactsOf(ownSpans, id), [ownSpans, id])
  // Each step's model-call effects (U23.2): the story's step chips.
  const stepFx = useMemo(() => stepEffectsOf(ownSpans, id), [ownSpans, id])
  // Who let each call run (U15.2-fix, UX-B-14): the span's approval.
  const approvals = useMemo(() => approvalFactsOf(ownSpans, id), [ownSpans, id])
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
              spans: ownSpans,
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
      ownSpans,
      transcript.data,
    ]
  )

  // The time scale is the waterfall's: the story reads the spans only
  // for the override chip, and stays on the order scale (replay).
  const timed: TimedSpan[] = useMemo(
    () => (view === "waterfall" ? (spans.data?.spans ?? []) : []),
    [view, spans.data]
  )
  const haveTime = timed.length > 0
  // Each span's parent (the run's and its descendants'): a log line's
  // span no row draws selects its nearest ancestor's row (jumpSel), and
  // ticks it.
  const spanParents = useMemo(() => {
    const m = new Map<string, string>()
    for (const s of spans.data?.spans ?? [])
      if (s.parent_span_id) m.set(s.span_id, s.parent_span_id)
    return m
  }, [spans.data])
  // A derived run (plan I1.3) has spans and no events: its one scale is
  // time — the order scale and its playhead have nothing to show. Asked
  // for time without spans, the run reads on the order scale.
  const derived = isDerived(run.data)
  // The graph (U34) reads the run tree's rows: the spans' with spans, a
  // span-less run's fold on the order scale.
  const scale: RunScale =
    search.scale === "graph" && (haveTime || !derived)
      ? "graph"
      : derived && haveTime
        ? "time"
        : haveTime
          ? search.scale === "graph"
            ? "time"
            : (search.scale ?? "time")
          : "order"
  // The tree's own scale: the graph reads (and selects in) its rows.
  const treeScale: Scale =
    scale === "graph" ? (haveTime ? "time" : "order") : scale
  // The scales offered: all three with spans, order and graph without;
  // a derived run's spans are its one scale beside the graph.
  const scales: readonly RunScale[] = derived
    ? haveTime
      ? ["time", "graph"]
      : []
    : haveTime
      ? SCALES
      : ["order", "graph"]
  // The scale switch (and its t key) is offered when there is a choice.
  const scaled = scales.length > 1
  // Still waiting for late spans (U34-fix3): inside the 30 s after the
  // end (endSeen) with no invoke_agent span read yet, the spans read
  // polls (above) — an empty answer then is not "recorded none". A
  // timer renders once when the window closes, the poll having stopped.
  const spansAt = endSeen.current.id === id ? endSeen.current.at : null
  const [, setSpansWaitOver] = useState(0)
  const spansAwaited =
    spansAt !== null &&
    Date.now() - spansAt <= INVOKE_AGENT_WAIT_MS &&
    !hasInvokeAgent(spans.data?.spans, id)
  useEffect(() => {
    if (!spansAwaited) return
    const t = setTimeout(
      () => setSpansWaitOver((n) => n + 1),
      spansAt + INVOKE_AGENT_WAIT_MS - Date.now() + 1
    )
    return () => clearTimeout(t)
  }, [spansAwaited, spansAt])
  // Why the agents level is off (U34-fix2): "recorded none" only on a
  // read that succeeded — the spans came back empty on a run that is
  // over and no late span is awaited, or the run has no trace to read;
  // a failed read says so in words (levelError), no badge; nothing
  // while it is being read, polled for late spans, or the run runs.
  const levelError: string | undefined =
    !haveTime && spans.isError
      ? spansUnreadable(spans.error.message)
      : undefined
  const spansNone =
    !haveTime &&
    !spans.isError &&
    ((spans.isSuccess && runStatus !== "running" && !spansAwaited) ||
      (run.isSuccess && !run.data.trace_id))
  // No spans but subagent rows (P9-pre-web-fix3): the agents level is
  // drawn from the run rows (rowsCallGraph), its badge saying what that
  // leaves out; a run of no subagent has no agents level to draw.
  const rowsAgents =
    spansNone && !derived && (run.data?.children.length ?? 0) > 0
  // The agents level reads the spans (the call graph), else the rows:
  // without either it is off with a not_recorded badge, and
  // ?level=agents reads the loop.
  const level: GraphLevel =
    search.level === "agents" && (haveTime || rowsAgents) ? "agents" : "loop"
  const levelHole: HoleMark | undefined = rowsAgents
    ? ROWS_LEVEL
    : spansNone
      ? NO_SPANS_LEVEL
      : undefined

  // The child subtree in place (U9.2, UX-A-01): a child run row stays
  // one row until expanded; its rows (and its children's, lazily — drawn
  // on expand, never before) come from the same ?tree=1 spans. A ?sel=
  // inside a child expands the children above it (expandFor, below).
  // The order scale (and a time row's gists) read the child's own fold:
  // its events and its run document (for its children), fetched on
  // expand — and for a run the ?sel= names, so a pasted link restores
  // the subtree on either scale.
  const [opened, setOpened] = useState<ReadonlySet<string>>(() => new Set())
  const treeHoles = useMemo(() => spans.data?.holes ?? [], [spans.data])
  const knownRuns = useMemo(() => {
    const out = new Set<string>()
    for (const s of spans.data?.spans ?? []) {
      const r = s.run_id ?? str(s.attrs["weft.run.id"])
      if (r) out.add(r)
    }
    for (const c of run.data?.children ?? []) out.add(c.id)
    return [...out].sort()
  }, [spans.data, run.data?.children])
  const sel = search.sel
  const fetchRuns = useMemo(
    () =>
      [
        ...new Set([
          ...opened,
          ...[...knownRuns].filter((r) => r !== id && sel?.includes(r)),
        ]),
      ].sort(),
    [opened, knownRuns, sel, id]
  )
  const onTree = view === "waterfall"
  // An expanded child is followed while it runs (U9.2-fix W3, U10.1-fix
  // #8): its document and its stream are read again every 2 s, its own
  // spans (a cut tree's) every 5 s — never frozen at the expand, and a
  // finished child never re-read. Before its document answers (a child
  // the root is still starting) it is asked again while the root runs.
  // Its stream is read once whole and then from its last position on
  // (childStreamQuery pages it), up to a cap its row badges.
  const rootLive = isLive(run.data)
  const childDocs = useQueries({
    queries: fetchRuns.map((r) => ({
      ...runQuery(r),
      enabled: onTree,
      refetchInterval: (q: { state: { data?: RunRow } }) =>
        isLive(q.state.data) || (rootLive && !q.state.data) ? 2000 : false,
    })),
    combine: dataOf,
  })
  // Which of those reads failed with nothing to show (U34-fix4): the
  // same queries observed, never fetched again from here — a failed
  // read is not in flight, so its rows never read "not read yet".
  const childDocsFailed = useQueries({
    queries: fetchRuns.map((r) => ({ ...runQuery(r), enabled: false })),
    combine: (rs) =>
      fetchRuns.filter((_, i) => rs[i]?.isError && !rs[i]?.data).join("\n"),
  })
  const childStreams = useQueries({
    queries: fetchRuns.map((r, i) => ({
      ...childStreamQuery(r),
      enabled: onTree,
      refetchInterval: (q: { state: { data?: ChildStream } }) =>
        childStreamPolls(childDocs[i], q.state.data, rootLive) ? 2000 : false,
    })),
    combine: dataOf,
  })
  // A tree the server cut (truncated, cause response_cap): an expanded
  // child reads its own ?tree=1, merged in.
  const cutHole = treeHoles.find((h) => h.hole === "truncated")
  const cut = !!cutHole
  const childTrees = useQueries({
    queries: (cut ? fetchRuns : []).map((r) => ({
      ...spansQuery(r, true),
      enabled: onTree,
      refetchInterval: rootLive ? 5000 : (false as const),
    })),
    combine: dataOf,
  })
  const childFolds = useMemo(() => {
    const out = new Map<string, FoldedRun>()
    fetchRuns.forEach((r, i) => {
      const page = childStreams[i]
      if (!page) return
      const f = fold(page.events.map((e) => e.event))
      const kids = childDocs[i]?.children
      out.set(r, kids ? linkView(f, kids) : f)
    })
    return out
  }, [fetchRuns, childStreams, childDocs])
  // A child's stream as read: its gaps and the cap, badged on its row.
  const childMarks = useMemo(() => {
    const out = new Map<string, { gaps: number; capped: boolean }>()
    fetchRuns.forEach((r, i) => {
      const s = childStreams[i]
      if (s && (s.gaps.length || s.capped))
        out.set(r, { gaps: s.gaps.length, capped: s.capped })
    })
    return out
  }, [fetchRuns, childStreams])
  // The children's own status (a running one is never badged a loss).
  const childStatus = useMemo(() => {
    const out = new Map<string, string>()
    for (const c of run.data?.children ?? []) out.set(c.id, c.status)
    fetchRuns.forEach((r, i) => {
      const d = childDocs[i]
      if (d) {
        out.set(r, d.status)
        for (const c of d.children) out.set(c.id, c.status)
      }
    })
    return out
  }, [run.data?.children, fetchRuns, childDocs])
  const treeSpansAll: TimedSpan[] = useMemo(() => {
    const extra = childTrees.flatMap((d) => d?.spans ?? [])
    if (!extra.length) return timed
    const seen = new Set(timed.map((s) => `${s.trace_id}/${s.span_id}`))
    const out = [...timed]
    for (const s of extra)
      if (!seen.has(`${s.trace_id}/${s.span_id}`)) {
        seen.add(`${s.trace_id}/${s.span_id}`)
        out.push(s)
      }
    return out
  }, [timed, childTrees])
  // The runs the tree's no_spans hole names: their child rows say so,
  // in the server's words (its reason, never the table's tracer-less
  // guess — U9.2-fix W2).
  const noSpans = useMemo(() => {
    const out = new Map<string, TreeHole>()
    for (const h of treeHoles)
      if (h.hole === "not_recorded")
        for (const r of h.runs ?? []) if (!out.has(r)) out.set(r, h)
    return out
  }, [treeHoles])

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

  // The ring evicted the stream's head (plan H2: a run past
  // EVENT_RING_CAPACITY events): a replay prefix needs every event from
  // position 0, so there is no playhead — the page reads live, and says
  // the window is partial.
  const evicted = stream.base > 0
  const ph = evicted ? null : playhead

  // Everything below renders the fold AT the playhead on the position
  // scale: one fold, one shape, whether live or scrubbed (B2). The
  // time scale has no playhead — durations do not replay.
  const replaying =
    scale === "order" && ph !== null && ph < stream.events.length
  const foldedNow =
    transcript.data && runStatus !== "running"
      ? applyTranscript(stream.folded, transcript.data.batches, {
          replace: true,
        })
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
    const prefix = fold(stream.events, ph, stream.attrs)
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
  }, [
    replaying,
    stream.events,
    stream.attrs,
    foldedNow,
    ph,
    batches,
    runStatus,
  ])
  // While scrubbing the run reads as running: calls past the playhead
  // are "running", not "never completed".
  const viewStatus = replaying ? "running" : runStatus

  // The calls carry their child's id (linkView, as the story's do):
  // the call detail's subagent block and the tree's child row join on
  // it — never on the bare call id, which may repeat across steps (A10).
  const children = useMemo(
    () => (run.data ? storyChildren(run.data) : undefined),
    [run.data]
  )
  const linkedFold = useMemo(
    () => (children ? linkView(atPlayhead, children) : atPlayhead),
    [atPlayhead, children]
  )
  // The detail pane's rows: the fold's spans at the playhead (the
  // story's bodies, the replay positions).
  const posSpans = useMemo(
    () => spansFromFold(linkedFold, stream.total, viewStatus),
    [linkedFold, stream.total, viewStatus]
  )
  // The run tree (U8.2): lib/waterfall.ts's rows on the page's scale,
  // projected into the Waterfall's rows.
  // A child row no span names reads its agent (U10.2, the panel's rule).
  const childAgents = useMemo(() => {
    const out = new Map<string, string>()
    for (const c of [
      ...(run.data?.children ?? []),
      ...childDocs.flatMap((d) => d?.children ?? []),
    ])
      if (c.agent) out.set(c.id, c.agent)
    return out
  }, [run.data?.children, childDocs])
  const treeInput = useMemo(
    () => ({
      runId: id,
      status: viewStatus,
      spans: treeSpansAll,
      fold: linkedFold,
      total: stream.total,
      children: childFolds,
      agents: childAgents,
    }),
    [
      id,
      viewStatus,
      treeSpansAll,
      linkedFold,
      stream.total,
      childFolds,
      childAgents,
    ]
  )
  // Every child expanded: where a ?sel= row sits, and so which children
  // it opens. A second whole tree, so built only when the selection, the
  // scale or the runs the tree knows change (U9.2-fix W7) — never per
  // event batch of a running run: React's "information from previous
  // renders" pattern, a state updated during render when its key moves.
  const impliedKey = sel
    ? [
        sel,
        search.step ?? "",
        treeScale,
        knownRuns.join(","),
        [...childFolds.keys()].join(","),
        // A cut tree's child reads add spans (and grandchildren).
        treeSpansAll.length,
      ].join("\n")
    : ""
  const [impliedAt, setImpliedAt] = useState<{
    key: string
    runs: string[]
  }>({ key: "", runs: [] })
  let implied = impliedAt.runs
  if (impliedAt.key !== impliedKey) {
    implied = sel
      ? expandFor(
          waterfallRows({ ...treeInput, expanded: ALL_RUNS }, treeScale).rows,
          sel,
          id,
          search.step,
          spanParents
        )
      : []
    setImpliedAt({ key: impliedKey, runs: implied })
  }
  const expanded = useMemo(
    () => new Set([...opened, ...implied]),
    [opened, implied]
  )
  const tree = useMemo(
    () => waterfallRows({ ...treeInput, expanded }, treeScale),
    [treeInput, expanded, treeScale]
  )
  // A step whose request saw a compacted view is marked on its row
  // (U15.2, UX-S5b-05): compaction is not the story's alone.
  // A turn a newer message ended (UX-B-09): its rows neutral, the
  // end's word on them — red stays for real failures.
  const ended = useBusyEnd(run.data)
  const treeRows = useMemo(
    () =>
      busyEndSpans(
        compactionBadges(treeSpans(tree), id, compactionsOf(run.data)),
        id,
        ended
      ),
    [tree, id, run.data, ended]
  )
  // WARN / ERROR ticks (U11.2): read only when the run document counts
  // such lines (log_warn / log_error), at warn and above.
  const tickRead =
    has("logs") &&
    view === "waterfall" &&
    (run.data?.log_warn ?? 0) + (run.data?.log_error ?? 0) > 0
  const warnLogs = useQuery({
    ...allLogsQuery(id, "warn", runStatus === "running"),
    enabled: tickRead,
  })
  const ticks = useMemo(
    () =>
      logTicks(
        warnLogs.data?.logs ?? [],
        treeRows.map((r) => ({ key: r.key, spanId: r.timed?.span.span_id })),
        spanParents
      ),
    [warnLogs.data, treeRows, spanParents]
  )
  // The dim filters (§6.1, UX-A-04): ?dim= names them; rows no filter
  // keeps dim, never hide.
  const dim = useMemo(() => parseDim(search.dim), [search.dim])
  const dimmed = useMemo(() => dimRows(tree.rows, dim), [tree.rows, dim])
  const toggleDim = useCallback(
    (k: (typeof DIMS)[number]) =>
      void navigate({
        search: (prev) => {
          const d = parseDim(prev.dim)
          d[k] = !d[k]
          return { ...prev, dim: dimParam(d) }
        },
        replace: true,
      }),
    [navigate]
  )
  const flow = useMemo(
    () => flowFromFold(atPlayhead, viewStatus),
    [atPlayhead, viewStatus]
  )
  // The selection: ?sel= names a row key; absent, start where
  // something went wrong (UX-A-03: on the rows the tree draws, on the
  // scale shown — the first failed row, else the step the run failed
  // at, else step 0).
  const fullSpans = useMemo(
    () => spansFromFold(stream.folded, stream.total, runStatus),
    [stream.folded, stream.total, runStatus]
  )
  // ?step= (a deep link to a step, lib/links.ts) selects that step's
  // row when no (known) row is named, else the default.
  const stepKey =
    search.step !== undefined &&
    tree.rows.some((r) => r.key === `s${search.step}`)
      ? `s${search.step}`
      : undefined
  // While replaying, a row the playhead has not reached yet stays
  // selected (and says so below) when the whole run has it.
  // Esc's ?sel=none (the loop graph's NO_SEL): nothing selected, and
  // the landing rule below does not pick a row again.
  const selKey =
    search.sel === NO_SEL
      ? undefined
      : (resolveRowSel(tree.rows, search.sel, id, search.step, spanParents) ??
        (replaying && search.sel && fullSpans.some((s) => s.key === search.sel)
          ? search.sel
          : undefined) ??
        stepKey ??
        // A derived run's rows land by the same rule (its steps are drawn
        // from its spans): where something went wrong, else step 0.
        defaultRowSel(
          tree.rows,
          replaying || !run.data ? undefined : failedAtStep(run.data)
        ))
  const selectedRow = treeRows.find((s) => s.key === selKey)
  // The story is the read mode of the same selection (§6.3): the step
  // the selected row is under is the card it highlights.
  // Only a selection the URL names: the default one never scrolls the
  // story.
  const selStepKey = stepRowOf(selKey, treeRows)?.key
  const selStep =
    selStepKey && /^s\d+$/.test(selStepKey)
      ? Number(selStepKey.slice(1))
      : undefined
  const namedStep = search.sel ? selStep : undefined
  const readInStory = useCallback(
    () =>
      void navigate({
        search: (prev) => ({
          ...prev,
          view: "story",
          step: selStep ?? prev.step,
        }),
      }),
    [navigate, selStep]
  )
  // A derived run's fold holds no events: its rows read their spans.
  const selected = useMemo(
    () => detailSpan(selKey, treeRows, derived ? [] : posSpans),
    [selKey, treeRows, posSpans, derived]
  )
  const select = useCallback(
    (key: string) =>
      void navigate({
        // The waterfall's selection: a pane would hold the view on the story.
        search: (prev) => ({
          ...prev,
          sel: key,
          view: undefined,
          pane: undefined,
        }),
        replace: true,
      }),
    [navigate]
  )
  // A child row's toggle: expand it, or fold it back into one row — and
  // a selection inside it moves to the child's own row (it would be
  // drawn nowhere).
  const toggleChild = useCallback(
    (child: string) => {
      if (!expanded.has(child)) {
        setOpened((cur) => new Set([...cur, child]))
        return
      }
      setOpened(
        (cur) =>
          new Set(
            [...cur].filter((r) => r !== child && !r.startsWith(`${child}/`))
          )
      )
      if (implied.includes(child)) select(`r:${child}`)
    },
    [expanded, implied, select]
  )
  // The call graph (U9.2, §6.2): the same spans, agents and MCP servers
  // as nodes; the selection is the tree's.
  const sources = useMemo(
    () => catalogSources(requests?.steps),
    [requests?.steps]
  )
  const graph = useMemo(
    () =>
      scale === "graph" && level === "agents" && haveTime
        ? callGraph(
            treeSpansAll,
            id,
            (r, tool) => (r === id ? sources.get(tool) : undefined),
            // Only this run's catalog is read: a child's MCP calls are
            // said not shown, never guessed (U9.2-fix W5).
            (r) => r === id
          )
        : undefined,
    [scale, level, haveTime, treeSpansAll, id, sources]
  )
  // The loop graph (U34): every child expanded (a subagent expands in
  // place: its loop is drawn from the same rows), on the tree's scale.
  const loopOn = scale === "graph" && level === "loop"
  // A second whole tree (every child expanded) and the folds' arguments,
  // so built only when what the loop reads moves — the U9.2-fix W7 rule,
  // as `implied` above: the scale, the status, the spans, each fold's
  // steps, calls and results — never per event batch of a running run
  // (a text delta moves no node).
  const loopKey = loopOn
    ? [
        treeScale,
        viewStatus,
        treeSpansAll.length,
        knownRuns.join(","),
        childAgents.size,
        loopSig(linkedFold),
        ...[...childFolds].map(([r, f]) => `${r}=${loopSig(f)}`),
      ].join("\n")
    : ""
  const [loopAt, setLoopAt] = useState<{
    key: string
    rows: WaterfallRow[]
    args: Map<string, unknown>
  }>({ key: "", rows: [], args: new Map() })
  let { rows: graphRows, args: argsByRun } = loopAt
  if (loopAt.key !== loopKey) {
    graphRows = loopOn
      ? waterfallRows({ ...treeInput, expanded: ALL_RUNS }, treeScale).rows
      : []
    // By run, step and call id: a call id repeats across steps (each
    // step's calls numbered afresh); a resumed call's step is "resume".
    argsByRun = new Map()
    const put = (r: string, f: FoldedRun) => {
      for (const st of f.steps)
        for (const c of st.toolCalls)
          argsByRun.set(
            `${r}\n${c.resumed ? "resume" : st.index}\n${c.callId}`,
            c.args
          )
    }
    if (loopOn) {
      put(id, linkedFold)
      for (const [r, f] of childFolds) put(r, f)
    }
    setLoopAt({ key: loopKey, rows: graphRows, args: argsByRun })
  }
  // The loop drawn: the selected row's run (a subagent expanded by
  // selecting its run), at most DEEP_HANDOFF_DEPTH levels down.
  const selGraphRow = graphRows.find((r) => r.key === selKey)
  const focusChain = useMemo(() => {
    const c = runChain(id, selGraphRow?.runId ?? id)
    return c.slice(0, DEEP_HANDOFF_DEPTH + 1)
  }, [id, selGraphRow?.runId])
  const focus = focusChain.at(-1) ?? id
  const ownLoop = focus === id
  // The scrubber's step: the root's is ?step=; a child loop's is its
  // selected step row (s:<child>:<n>) — ?step= is the root's, read so by
  // the rest of the page.
  const loopStep = ownLoop
    ? search.step
    : selGraphRow?.runId === focus && selGraphRow.kind === "step"
      ? selGraphRow.step
      : undefined
  // A run's own cost (plan A5: the API's, read time): the root's row,
  // its children's, the expanded children's documents and theirs.
  const pricing = has("pricing")
  // The run rows this page read (the same set): the call graph's
  // status and steps per agent node (U34-fix4, UX-S3-14).
  const readRows = useMemo(() => {
    const out = new Map<string, RunRow>()
    for (const r of [
      run.data,
      ...(run.data?.children ?? []),
      ...childDocs,
      ...childDocs.flatMap((d) => d?.children ?? []),
    ])
      if (r && !out.has(r.id)) out.set(r.id, r)
    return out
  }, [run.data, childDocs])
  const costRows: Map<string, CostFields> = useMemo(
    () => (pricing ? readRows : new Map()),
    [pricing, readRows]
  )
  const rowOfRun = useCallback((r: string) => readRows.get(r), [readRows])
  // The agents level with no spans (rowsAgents): the rows' call graph.
  const rowsGraph = useMemo(
    () =>
      scale === "graph" && level === "agents" && !haveTime
        ? rowsCallGraph(readRows, id)
        : undefined,
    [scale, level, haveTime, readRows, id]
  )
  const shownGraph = graph ?? rowsGraph
  // The documents being read (U34-fix3): a missing row is "not read
  // yet" only while the read that would carry it is in flight — the
  // root's document, its own expanded document or its parent's; any
  // other (a panel token's hidden child, an agents-level grandchild
  // under no expanded child) is "not read on this page".
  // A read that failed (U34-fix4) is not in flight: its rows say the
  // failure, never "yet" — it would promise a read that is not coming.
  const docsFailed = useMemo(() => {
    const out = new Set(childDocsFailed ? childDocsFailed.split("\n") : [])
    if (!run.data && run.isError) out.add(id)
    return out
  }, [childDocsFailed, run.data, run.isError, id])
  const docsInFlight = useMemo(() => {
    const out = new Set<string>()
    if (!run.data && !docsFailed.has(id)) out.add(id)
    if (onTree)
      fetchRuns.forEach((r, i) => {
        if (!childDocs[i] && !docsFailed.has(r)) out.add(r)
      })
    return out
  }, [run.data, id, onTree, fetchRuns, childDocs, docsFailed])
  const costOf = useCallback(
    (r: string) => {
      const row = costRows.get(r)
      if (row) return costView(row)
      const parent = /^(.+)\/\d+\/[^/]+$/.exec(r)?.[1]
      const own = docsInFlight.has(r) || docsFailed.has(r)
      return own || parent === undefined
        ? costNotRead(docsInFlight.has(r), docsFailed.has(r))
        : costNotRead(docsInFlight.has(parent), docsFailed.has(parent))
    },
    [costRows, docsInFlight, docsFailed]
  )
  const costOfRuns = useCallback(
    (rs: readonly string[]) => {
      // A row not read is "—" with its reason (U34-fix2, U34-fix3):
      // drawn, never nothing.
      const vs = rs.map(costOf)
      if (!vs.length) return undefined
      return vs.length === 1 ? vs[0] : sumCost(vs)
    },
    [costOf]
  )
  // The tools offered and never called, unknown: why (the A3 table's
  // not_recorded) — nothing while the requests are being read; a failed
  // read says so, never "no record carried a catalog" (U34-fix2).
  // A failed read is words, not a badge (U34-fix3: the A3 table's gap
  // is a dropped record): offeredError, as the attempts pane says one.
  const offeredError: string | undefined =
    ownLoop && requestsCapable && requestsQ.isError
      ? offeredUnreadable(requestsQ.error.message)
      : undefined
  const offeredHole: HoleMark | undefined = !ownLoop
    ? OFFERED_UNREAD
    : !requestsCapable
      ? { hole: "not_recorded", cause: "not_served" }
      : requestsQ.isError || requestsQ.isPending
        ? undefined
        : OFFERED_NONE
  // run_finish.pending by its ids: a new array per event batch, the
  // same calls.
  const pendingKey = atPlayhead.pending.map((p) => p.id).join(",")
  const loopPending = useMemo(() => atPlayhead.pending, [pendingKey])
  const loop = useMemo(() => {
    if (!loopOn) return undefined
    const own = focus === id
    const kid = own
      ? undefined
      : (childDocs.find((d) => d?.id === focus) ??
        run.data?.children.find((c) => c.id === focus))
    return loopGraph(graphRows, focus, {
      stoppedBy: own ? run.data?.stopped_by : kid?.stopped_by,
      status: own ? viewStatus : (kid?.status ?? childStatus.get(focus)),
      pending: own ? loopPending : undefined,
      argsOf: (call, step) =>
        argsByRun.get(`${focus}\n${step ?? "resume"}\n${call}`),
      // Only this run's catalog is read (U9.2-fix W5's rule): a child's
      // offered tools are a hole, never "none".
      catalog: own ? catalogTools(requests?.steps) : null,
      usage: own ? run.data?.usage : kid?.usage,
    })
  }, [
    loopOn,
    focus,
    id,
    childDocs,
    run.data,
    graphRows,
    viewStatus,
    childStatus,
    loopPending,
    argsByRun,
    requests?.steps,
  ])
  const loopCall = loop ? callOf(loop, selKey) : undefined
  const loopCrumbs = useMemo(
    () =>
      focusChain.map((r) => ({
        run: r,
        label:
          graphRows.find(
            (x) => x.runId === r && (x.kind === "run" || x.kind === "child")
          )?.label ?? r,
      })),
    [focusChain, graphRows]
  )
  const setLevel = useCallback(
    (to: GraphLevel) =>
      void navigate({
        search: (prev) => ({
          ...prev,
          level: to === "agents" ? "agents" : undefined,
        }),
        replace: true,
      }),
    [navigate]
  )
  // The scrubber (its buttons, play, J / K) moves the selection with
  // it: the step's own row, so no pressed node stays dimmed on the step
  // before (a child loop's step is that row already).
  const setLoopStep = useCallback(
    (n: number | undefined) =>
      void navigate({
        search: (prev) =>
          focus === id
            ? {
                ...prev,
                step: n,
                sel:
                  n === undefined
                    ? prev.sel
                    : (loop?.steps.find((st) => st.n === n)?.key ?? prev.sel),
                pane: undefined,
              }
            : // A child loop's step is its own step row, ?step= left the
              // root's (lib/waterfall.ts's s:<run>:<n>).
              {
                ...prev,
                sel:
                  n === undefined ? `r:${focus}` : rowKeys(focus, true).step(n),
                view: undefined,
                pane: undefined,
              },
        replace: true,
      }),
    [navigate, focus, id, loop]
  )
  const setScale = useCallback(
    (to: RunScale) =>
      void navigate({
        // Written as itself (UX-A-02): ?scale=order round-trips.
        search: (prev) => ({ ...prev, scale: to }),
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
        // The playhead is the order scale's: a run with spans
        // switches to it.
        search: (prev) => ({
          ...prev,
          t,
          scale: haveTime ? "order" : prev.scale,
        }),
      })
    },
    [navigate, haveTime]
  )

  // The story's scrubber (plan H2) names a step the way a deep link
  // does (?step=, G1): one history entry per scrub (its first move),
  // the rest of a drag written in place — back returns to the step
  // before the drag.
  const stepInput = useRef<HTMLInputElement>(null)
  const onStep = useCallback(
    (n: number, mode: "push" | "replace") =>
      // A step's pane (?pane=, A6.2) is the step's the link landed on:
      // it never rides onto the steps the scrubber crosses.
      void navigate({
        search: (prev) => ({
          ...prev,
          step: n,
          pane: n === prev.step ? prev.pane : undefined,
        }),
        replace: mode === "replace",
      }),
    [navigate]
  )

  // Keys: r toggles raw, s the story, w the waterfall, q a step's
  // Request, o the app; j/k walk the
  // run tree's rows (A4); n jumps to a step (the story's scrubber).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!isPlainShortcut(e) || !run.data) return
      // The view keys both surfaces bind (lib/keys.ts's VIEW_KEYS:
      // s · w · q · r, and l the logs): a view; on it already, the view
      // before it (the panel's tabs alike). Request is a step's pane
      // here — the story at the step named (else step 0), its Request
      // pane open.
      const setView = (v: ViewName) => {
        const to = keyView === v ? prevView.current : v
        void navigate({
          search: (prev) =>
            to === "request"
              ? {
                  ...prev,
                  view: "story",
                  step: prev.step ?? 0,
                  pane: "request",
                }
              : {
                  ...prev,
                  view: to === "waterfall" ? undefined : to,
                  pane:
                    to === "story" && prev.pane !== "request"
                      ? prev.pane
                      : undefined,
                },
        })
      }
      const v = VIEW_KEYS[e.key]
      if (v) setView(v)
      else if (e.key === "l") setView("logs")
      else if (e.key === "o")
        // o (lib/keys.ts's KEY_OPEN): the run in your app — the header's
        // "open in app", when it has a page to open.
        document
          .querySelector<HTMLAnchorElement>("a[data-open-in-app]")
          ?.click()
      else if (e.key === "n") {
        e.preventDefault()
        if (view !== "story") setView("story")
        // The box mounts with the story: focus it once it is there (a
        // few frames at most).
        let tries = 0
        const focusBox = () => {
          const box = stepInput.current
          if (box) {
            box.focus()
            box.select()
          } else if (++tries < 10) requestAnimationFrame(focusBox)
        }
        requestAnimationFrame(focusBox)
      } else if (
        (e.key === "j" || e.key === "k") &&
        view === "waterfall" &&
        loop
      ) {
        // The loop's step scrubber: J / K move between its steps.
        const ns = loop.steps.map((st) => st.n)
        const i = ns.indexOf(loopStep ?? -1)
        const next =
          e.key === "j" ? ns[i + 1] : i < 0 ? ns.at(-1) : ns[Math.max(i - 1, 0)]
        if (next !== undefined) setLoopStep(next)
      } else if ((e.key === "j" || e.key === "k") && view === "waterfall") {
        const i = treeRows.findIndex((s) => s.key === selKey)
        const next = treeRows.at(e.key === "j" ? i + 1 : Math.max(i - 1, 0))
        if (next) select(next.key)
      } else if (e.key === "t" && view === "waterfall" && scaled) {
        // The scale (§6.1's key; s is the story's): time → order → graph.
        setScale(scales[(scales.indexOf(scale) + 1) % scales.length])
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [
    navigate,
    run.data,
    view,
    keyView,
    treeRows,
    selKey,
    select,
    scaled,
    scale,
    scales,
    setScale,
    loop,
    loopStep,
    setLoopStep,
  ])

  // A row's WARN / ERROR tick: a click opens its drawer's Logs tab.
  const logTick = (sp: (typeof treeRows)[number]) => {
    const t = ticks.get(sp.key)
    if (!t) return null
    return (
      <button
        type="button"
        className={`shrink-0 font-mono text-[10px] ${severityClass(t.error ? 17 : 13)}`}
        title={LOGS.tickHint(t.warn, t.error, warnLogs.data?.cut)}
        data-log-tick={sp.key}
        onClick={(e) => {
          e.stopPropagation()
          void navigate({
            search: (prev) => ({
              ...prev,
              sel: sp.key,
              tab: "logs",
              view: undefined,
              pane: undefined,
            }),
            replace: true,
          })
        }}
      >
        {LOGS.tick(t, warnLogs.data?.cut)}
      </button>
    )
  }

  if (run.isPending) {
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  }
  if (run.isError) {
    if (isNotFound(run.error)) return <NotFound kind="run" id={id} />
    return (
      <LoadFailed
        kind="run"
        id={id}
        error={run.error}
        onRetry={() => void run.refetch()}
      />
    )
  }
  const doc = run.data
  // The editor (plan F2): on the run's own steps, where a replay can
  // keep them — only with the drawer's verbs, never on a running run.
  const maxFrom = transcript.data
    ? replayBounds(transcript.data.batches).max
    : null
  // A derived run has no transcript to replay (the playground refuses
  // the source 400): no verbs, no editor, no drawer — its story says
  // why where the verbs would be.
  const replayOn = replayable && !derived
  // The drawer open on this run's own step (UX-C-46; the URL's
  // ?replay=&from=, of= naming another run — a child's — marks none).
  const replayLinked = replayFromSearch(search)
  const replayFromHere =
    replayLinked && !replayLinked.of ? replayLinked.from : undefined
  const editor: Editor | null =
    replayOn && transcript.data && doc.status !== "running"
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
                req: {
                  runID: doc.id,
                  agent: doc.agent || undefined,
                  draft: editTranscript(from, list),
                },
                n: cur.n + 1,
              }))
              writeReplay({ verb: "edit", from })
            }
          },
        }
      : null

  return (
    <ReplayContext.Provider value={replayOn ? openReplay : null}>
      <EditorContext.Provider value={editor}>
        <div className="space-y-4">
          <RunHeader
            doc={doc}
            openAt={appAt(search)}
            folded={atPlayhead}
            eventCount={Math.max(doc.event_count, stream.total)}
            transcript={transcript.data}
            gaps={stream.gaps}
            sequentialTools={requestsQ.data?.requests[0]?.body.sequential_tools}
            overrideParallelism={overrideParallelism(ownSpans, id)}
            overrideRead={hasInvokeAgent(ownSpans, id)}
            toolFacts={spans.data ? toolFacts : undefined}
            spans={ownSpans}
          />
          {evicted ? (
            <p
              className="rounded-md border border-dashed px-3 py-2 font-mono text-[11px] text-faint"
              data-ring-note
            >
              showing the last {stream.events.length.toLocaleString()} events
              (positions {stream.base.toLocaleString()}–
              {(stream.total - 1).toLocaleString()}); the{" "}
              {stream.base.toLocaleString()} before them are on the API
              (api/runs/{doc.id}
              /events?after=) — the story and the waterfall hold the whole run,
              playback needs the whole stream
            </p>
          ) : scale === "order" && !derived ? (
            <ReplayBar
              events={stream.events}
              eventCount={doc.event_count}
              playhead={playhead}
              onTick={setPlayhead}
              onSeek={seek}
              running={runStatus === "running"}
            />
          ) : null}
          {/* While running: positions may still be in flight. Once over,
          the header's gap badge (the run's holes) says it. */}
          {runStatus === "running" && stream.gaps.length > 0 && (
            <div
              className={`rounded-md border border-status-bad/30 px-3 py-2 font-mono text-xs ${STATUS_TEXT.bad}`}
            >
              {stream.gaps.length >= 1000 ? "1,000+" : stream.gaps.length}{" "}
              recorded {stream.gaps.length === 1 ? "event is" : "events are"}{" "}
              missing from the database (position
              {stream.gaps.length === 1 ? "" : "s"}{" "}
              {stream.gaps.slice(0, 8).join(", ")}
              {stream.gaps.length > 8 ? ", …" : ""}) — still in flight, or lost
              on the way
            </div>
          )}
          {stream.error && (
            <div
              className={`rounded-md border border-status-bad/30 px-3 py-2 font-mono text-xs ${STATUS_TEXT.bad}`}
            >
              event stream: {stream.error}
            </div>
          )}
          <Tabs
            value={view}
            onValueChange={(v) =>
              void navigate({
                search: (prev) => ({
                  ...prev,
                  view:
                    v === "raw" || v === "story" || v === "logs"
                      ? v
                      : undefined,
                  // A step pane is the story's (it would hold the view there).
                  pane: v === "story" ? prev.pane : undefined,
                }),
              })
            }
          >
            <div className="flex flex-wrap items-center gap-3">
              <TabsList>
                {/* The views' labels (lib/links.ts's VIEWS): the panel's tabs say the same. */}
                {/* Each a glossary word (U21.2): its tooltip on focus,
                    drawn in <body> — a tablist holds tabs only. */}
                {(["waterfall", "story", "raw"] as const).map((v) => (
                  <TermWrap key={v} name={v} portal>
                    {(t) => (
                      <TabsTrigger value={v} {...t}>
                        {VIEWS[v]}
                      </TabsTrigger>
                    )}
                  </TermWrap>
                ))}
                <TabsTrigger value="logs" data-logs-tab>
                  {LOGS.view}
                </TabsTrigger>
              </TabsList>
              {view === "waterfall" && scaled ? (
                <div
                  className="flex items-center gap-1 font-mono text-[11px]"
                  role="group"
                  aria-label="scale"
                  data-scale={scale}
                >
                  <span className="text-faint">scale:</span>
                  {scales.map((a) => (
                    <button
                      key={a}
                      type="button"
                      aria-pressed={scale === a}
                      className={`rounded-md border px-2 py-1 ${
                        scale === a
                          ? "border-thread/60 bg-secondary"
                          : "text-muted-foreground hover:text-foreground"
                      }`}
                      title={scaleHint(a)}
                      onClick={() => setScale(a)}
                    >
                      {a}
                    </button>
                  ))}
                </div>
              ) : null}
              {view === "waterfall" && scale === "graph" ? (
                <div
                  className="flex items-center gap-1 font-mono text-[11px]"
                  role="group"
                  aria-label="level"
                  data-level={level}
                >
                  {LEVELS.map((l) => (
                    <button
                      key={l}
                      type="button"
                      aria-pressed={level === l}
                      disabled={l === "agents" && !haveTime && !rowsAgents}
                      className={`rounded-md border px-2 py-1 disabled:opacity-50 ${
                        level === l
                          ? "border-thread/60 bg-secondary"
                          : "text-muted-foreground hover:text-foreground"
                      }`}
                      title={LEVEL_HINT[l]}
                      onClick={() => setLevel(l)}
                    >
                      {l}
                    </button>
                  ))}
                  {levelHole ? (
                    <span data-level-hole>
                      <HoleBadge {...levelHole} />
                    </span>
                  ) : levelError ? (
                    <span
                      className={`${STATUS_TEXT.bad}`}
                      data-level-hole
                      data-read-failed
                    >
                      {levelError}
                    </span>
                  ) : null}
                </div>
              ) : null}
              {view === "waterfall" && scale !== "graph" ? (
                <div
                  className="flex items-center gap-1 font-mono text-[11px]"
                  role="group"
                  aria-label={DIM_LABEL}
                  data-dim={search.dim ?? ""}
                >
                  <span className="text-faint">{DIM_LABEL}:</span>
                  {DIMS.map((k) => (
                    <button
                      key={k}
                      type="button"
                      aria-pressed={!!dim[k]}
                      className={`rounded-md border px-2 py-1 ${
                        dim[k]
                          ? "border-thread/60 bg-secondary"
                          : "text-muted-foreground hover:text-foreground"
                      }`}
                      title={DIM_WORDS[k].hint}
                      data-dim-filter={k}
                      onClick={() => toggleDim(k)}
                    >
                      {DIM_WORDS[k].label}
                    </button>
                  ))}
                </div>
              ) : null}
              {view === "waterfall" && treeRows.some((r) => r.critical) ? (
                <span
                  className="flex items-center gap-1 font-mono text-[11px] text-faint"
                  title={CRITICAL_HINT}
                  data-critical-legend
                >
                  <span
                    className="inline-block h-0.5 w-3 bg-thread"
                    aria-hidden
                  />
                  {CRITICAL}
                </span>
              ) : null}
              {view === "waterfall" && doc.trace_id ? (
                <Link
                  {...traceSelectionLink(doc.trace_id, {
                    // Always run-qualified (U9.2-fix W4): the trace page
                    // keys bare only its own root run — a top-level run
                    // need not be it (a sibling under an app's trace) —
                    // and reads its root's qualified keys back.
                    sel:
                      selectedRow && selectedRow.runId === id
                        ? qualifyKey(selectedRow.key, id)
                        : selectedRow?.key,
                  })}
                  className="font-mono text-[11px] text-thread-ink hover:underline"
                  title={TRACE_HINT}
                  data-trace-link
                >
                  {TRACE_LINK}
                </Link>
              ) : null}
              <span className="hidden items-center gap-1 text-[11px] text-faint md:flex">
                <Kbd>w</Kbd>
                <Kbd>s</Kbd>
                <Kbd>r</Kbd>
                <Kbd>l</Kbd> views · <Kbd>j</Kbd>
                <Kbd>k</Kbd> rows ·{" "}
                {scaled ? (
                  <>
                    <Kbd>t</Kbd> scale ·{" "}
                  </>
                ) : null}
                {derived ? null : (
                  <>
                    <Kbd>space</Kbd> play ·{" "}
                  </>
                )}
                <Kbd>[</Kbd>
                <Kbd>]</Kbd> step · <Kbd>n</Kbd> jump to step · <Kbd>?</Kbd> all
                keys
              </span>
              {stream.loading ? (
                <span className="ml-auto flex items-center gap-1.5 font-mono text-[11px] text-faint">
                  <Spinner className="size-3" /> loading events…
                </span>
              ) : null}
            </div>

            <TabsContent
              value="waterfall"
              className="@container/trace mt-3 space-y-3"
            >
              {/* A derived run's tree is its spans: until they are here (or
              if they cannot be read) the derived note stands in — never
              the order scale's words over a fold of zero events. */}
              {derived && !haveTime ? (
                <DerivedNote what="waterfall" />
              ) : (
                <>
                  <div className="flex items-start gap-2">
                    <div className="min-w-0 flex-1">
                      <StepStrip
                        pills={flow}
                        runStatus={viewStatus}
                        errorType={replaying ? undefined : run.data.error_type}
                        selectedKey={selKey}
                        onSelect={select}
                        ended={replaying ? undefined : ended}
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
                  {treeHoles.length > 0 ? (
                    <div data-tree-holes>
                      <HoleBadges holes={treeHoles} />
                    </div>
                  ) : null}
                  <div
                    className={`grid items-start gap-3 ${gridCols}`}
                    data-trace-grid={layout}
                  >
                    {loop ? (
                      <LoopGraphView
                        graph={loop}
                        chain={loopCrumbs}
                        depth={focusChain.length - 1}
                        selectedNode={nodeOfKey(loop, selKey, selGraphRow)}
                        step={loopStep}
                        offeredHole={offeredHole}
                        offeredError={offeredError}
                        costOf={pricing ? costOf : undefined}
                        onSelect={select}
                        onClear={() =>
                          void navigate({
                            // No selection until the next pick (NO_SEL):
                            // never the landing rule's row again.
                            search: (prev) => ({ ...prev, sel: NO_SEL }),
                            replace: true,
                          })
                        }
                        onStep={setLoopStep}
                        onFocusRun={(r) => select(`r:${r}`)}
                        openRun={(r) =>
                          router.history.createHref(
                            router.buildLocation(runLink(r)).publicHref
                          )
                        }
                      />
                    ) : shownGraph ? (
                      <CallGraphView
                        graph={shownGraph}
                        costOf={pricing ? costOfRuns : undefined}
                        rowOf={rowOfRun}
                        selected={nodeOfRun(shownGraph, selectedRow?.runId)?.id}
                        onSelect={(n) => n.runs[0] && select(`r:${n.runs[0]}`)}
                      />
                    ) : (
                      <Waterfall
                        spans={treeRows}
                        domain={tree.domain}
                        unit={scale === "time" ? "ms" : "event"}
                        playhead={scale === "order" ? ph : null}
                        onSeek={scale === "order" ? seek : undefined}
                        onSelect={(sp) => select(sp.key)}
                        selectedId={selectedRow?.id}
                        label="waterfall"
                        head="run"
                        onExpand={(sp) =>
                          sp.childRun && toggleChild(sp.childRun)
                        }
                        dimmed={dimmed}
                        trail={(sp) => {
                          // A row no span covers (U8.2-fix): pending its
                          // spans while the run goes on; once over, the
                          // A3 table's not_recorded — or truncated, when
                          // the server cut the tree (its cap, not a loss).
                          // A call the loop never ran (rule 11): "not
                          // executed", no span by design — never a loss.
                          const untimed =
                            sp.untimed === "pending" ||
                            sp.untimed === "unrun" ? (
                              <span
                                className="shrink-0 text-[10px] text-faint italic"
                                title={UNTIMED_NOTE[sp.untimed][1]}
                                data-untimed-note={sp.untimed}
                              >
                                {UNTIMED_NOTE[sp.untimed][0]}
                              </span>
                            ) : sp.untimed === "missing" ? (
                              cutHole ? (
                                <HoleBadge {...cutHole} />
                              ) : (
                                <HoleBadge {...UNTIMED_HOLE} />
                              )
                            ) : null
                          if (!sp.childRun)
                            return (
                              <>
                                {logTick(sp)}
                                {untimed}
                              </>
                            )
                          const r = sp.childRun
                          const hole = noSpans.get(r)
                          const marks = childMarks.get(r)
                          // A running child has no span yet (they export
                          // when they end): running, never a loss badge.
                          const running =
                            sp.tone === "running" ||
                            childStatus.get(r) === "running"
                          return (
                            <>
                              {logTick(sp)}
                              {untimed}
                              {hole && !running ? (
                                <HoleBadge
                                  hole="not_recorded"
                                  cause={hole.cause}
                                  reason={hole.reason}
                                  fix={hole.fix}
                                />
                              ) : null}
                              {marks?.capped ? (
                                <HoleBadge
                                  hole="truncated"
                                  reason={childCappedReason(CHILD_EVENTS_CAP)}
                                  fix={CHILD_CAPPED_FIX}
                                />
                              ) : null}
                              {marks?.gaps ? (
                                <HoleBadge
                                  hole="gap"
                                  note={gapNote(marks.gaps)}
                                />
                              ) : null}
                              <Link
                                {...runLink(sp.childRun)}
                                className="ml-auto shrink-0 text-thread-ink hover:underline"
                                data-open-run={sp.childRun}
                                onClick={(e) => e.stopPropagation()}
                              >
                                {OPEN_RUN}
                              </Link>
                            </>
                          )
                        }}
                        className="max-h-[60vh] @3xl/trace:max-h-[calc(100vh-8rem)]"
                      />
                    )}
                    <div className={stickyDetail}>
                      {loop && loopCall && loopCall.tool.calls.length > 1 ? (
                        <div
                          className="mb-2 flex items-center gap-1 font-mono text-[11px]"
                          role="group"
                          aria-label={loopCall.tool.name}
                          data-call-stepper
                        >
                          <button
                            type="button"
                            className="min-h-6 min-w-6 rounded-md border px-1.5 py-0.5 disabled:opacity-40"
                            aria-label="previous call"
                            disabled={loopCall.i === 0}
                            onClick={() =>
                              select(loopCall.tool.calls[loopCall.i - 1].key)
                            }
                          >
                            ‹
                          </button>
                          <span data-call-of>
                            {callStepper(
                              loopCall.i,
                              loopCall.tool.calls.length
                            )}
                          </span>
                          <button
                            type="button"
                            className="min-h-6 min-w-6 rounded-md border px-1.5 py-0.5 disabled:opacity-40"
                            aria-label="next call"
                            disabled={
                              loopCall.i === loopCall.tool.calls.length - 1
                            }
                            onClick={() =>
                              select(loopCall.tool.calls[loopCall.i + 1].key)
                            }
                          >
                            ›
                          </button>
                          <span className="text-muted-foreground">
                            · {loopCall.tool.name}
                          </span>
                        </div>
                      ) : null}
                      {/* The story's run cards (U25) — park words, tool
                          limits, typed child outcomes — for the drawer's
                          call rows too: one source (P8-gate-fix). */}
                      <RunCards
                        doc={doc}
                        pending={foldedNow.pending}
                        stepFx={stepFx}
                        callFacts={spans.data ? callFacts : undefined}
                        toolFacts={toolFacts}
                        approvals={approvals}
                        requests={requests}
                      >
                        <SpanDetail
                          span={selected}
                          row={selectedRow}
                          rows={treeRows}
                          folded={derived ? [] : posSpans}
                          events={stream.events}
                          base={stream.base}
                          doc={doc}
                          runStatus={viewStatus}
                          timed={timed}
                          playhead={scale === "order" ? ph : null}
                          // A pre-U10.1 ?d=events|json reads as Raw OTel.
                          tab={search.tab ?? (search.d ? "raw" : undefined)}
                          onTab={(t) =>
                            void navigate({
                              search: (prev) => ({
                                ...prev,
                                tab: t,
                                d: undefined,
                              }),
                              replace: true,
                            })
                          }
                          onJump={jump}
                          onStory={derived ? undefined : readInStory}
                          requests={requests}
                          maxFrom={maxFrom}
                        />
                      </RunCards>
                    </div>
                  </div>
                  {replaying && selKey && !selectedRow ? (
                    <p className="font-mono text-[11px] text-faint">
                      the selected row ({selKey}) is past the playhead — play
                      forward or press End for live
                    </p>
                  ) : null}
                </>
              )}
            </TabsContent>
            <TabsContent value="story" className="mt-3">
              {derived ? (
                <DerivedStory doc={doc} replayable={replayable} />
              ) : (
                <StepList
                  events={stream.events}
                  folded={foldedNow}
                  atPlayhead={atPlayhead}
                  doc={doc}
                  upTo={scale === "order" ? (ph ?? undefined) : undefined}
                  highlight={search.step ?? namedStep}
                  pane={search.step === undefined ? undefined : search.pane}
                  onJump={evicted ? undefined : jump}
                  requests={requests}
                  transcript={transcript.data}
                  transcriptError={
                    transcript.isError ? transcript.error.message : undefined
                  }
                  onStep={onStep}
                  stepInputRef={stepInput}
                  toolFacts={toolFacts}
                  callFacts={spans.data ? callFacts : undefined}
                  stepFx={stepFx}
                  approvals={approvals}
                  replayFrom={replayOn ? replayFromHere : undefined}
                />
              )}
            </TabsContent>
            <TabsContent value="logs" className="mt-3">
              {view === "logs" ? (
                <LogsView
                  runId={id}
                  started={doc.started}
                  running={runStatus === "running"}
                  level={search.lsev ?? ""}
                  onLevel={(l) =>
                    void navigate({
                      search: (prev) => ({ ...prev, lsev: l || undefined }),
                      replace: true,
                    })
                  }
                  onJump={(span) =>
                    void navigate({
                      search: (prev) => ({
                        ...prev,
                        view: undefined,
                        pane: undefined,
                        sel: jumpSel(span),
                        tab: "logs",
                      }),
                    })
                  }
                />
              ) : null}
            </TabsContent>
            <TabsContent value="raw" className="mt-3 space-y-3">
              {derived ? <DerivedNote what="events" /> : null}
              <RawView
                doc={doc}
                events={stream.events}
                base={stream.base}
                eventHoles={stream.folded.eventHoles}
                playhead={ph}
                onJump={evicted ? undefined : jump}
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
          {replayOn ? (
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
                void navigate({
                  search: (prev) =>
                    prev.replay && prev.from !== from
                      ? { ...prev, from }
                      : prev,
                  replace: true,
                })
              }
            />
          ) : null}
        </div>
      </EditorContext.Provider>
    </ReplayContext.Provider>
  )
}
