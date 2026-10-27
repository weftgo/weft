// A trace page (S4.7, plan H7): any trace, weft or not — the generic
// span tree on the time axis, the runs the trace holds, and a GenAI
// chat view when a span carries the conventions' messages (the
// polyglot promise: a Python app's OTel GenAI instrumentation shows up
// exactly like a weft run's chats). Every selection is a URL: ?sel= is
// the row's step-qualified key (lib/trace-sel.ts); an errored model
// call of a playground run offers "replay from this step" in the run
// page's own drawer.
import { useQuery } from "@tanstack/react-query"
import {
  createFileRoute,
  Link,
  useNavigate,
  useRouter,
} from "@tanstack/react-router"
import { useCallback, useMemo, useState } from "react"

import { runQuery, studioToken, traceQuery } from "@/lib/api"
import type { Span as TimedSpan } from "@/lib/api"
import { absoluteTime, relativeTime, spanMs } from "@/lib/format"
import { genaiChat, isModelCall, NO_MESSAGES_REASON } from "@/lib/genai"
import { canReplay, replayFromStep } from "@/lib/replay"
import { isGenAISpan, spansFromTimed, timeDomain } from "@/lib/trace"
import type { Span as TraceRow } from "@/lib/trace"
import {
  keyRows,
  OLD_KEY_NOTE,
  resolveSel,
  rootRun,
  traceRuns,
} from "@/lib/trace-sel"
import { toRunKey } from "@/lib/scope-switch"
import { ScopeSwitch } from "@/components/studio/scope-switch"
import { useCapabilities } from "@/hooks/use-capabilities"
import { DERIVED_HOLE, DERIVED_WORDS, isDerivedRunId } from "@/lib/honesty"
import { ChatMessages } from "@/components/studio/genai-chat"
import { HoleBadge } from "@/components/studio/hole-badge"
import { LoadFailed } from "@/components/studio/empty-state"
import { JsonTree } from "@/components/studio/json-tree"
import { ReplayDrawer } from "@/components/studio/replay-drawer"
import type { ReplayRequest } from "@/components/studio/replay-drawer"
import { Waterfall } from "@/components/studio/waterfall"
import { SplitPane } from "@/components/studio/split-pane"
import { Button } from "@/components/ui/button"
import { NotFound } from "@/components/studio/not-found"
import { isNotFound } from "@/lib/not-found"
import { Spinner } from "@/components/ui/spinner"
import { SPLITS } from "@/lib/pane-sizes"
import { runLink, traceSelectionLink, tracesLink } from "@/lib/links"
import type { TraceSearch } from "@/lib/links"
import { useDocumentTitle } from "@/hooks/use-document-title"
import { STATUS_TEXT } from "@/lib/status-class"

const searchText = (v: unknown): string | undefined =>
  typeof v === "string" && v ? v : typeof v === "number" ? String(v) : undefined

export const Route = createFileRoute("/traces/$id")({
  // ?sel=<row key> (lib/trace-sel.ts) selects that row; a pre-H7
  // ?span=<span id> still does. ?view=chat is the GenAI chat view (G2:
  // the page's state is its URL).
  validateSearch: (search: Record<string, unknown>): TraceSearch => ({
    sel: searchText(search.sel),
    span: searchText(search.span),
    view: search.view === "chat" ? "chat" : undefined,
    run: searchText(search.run),
  }),
  component: TracePage,
})

/** What the trace list shows of a trace, from its spans. */
function traceFacts(spans: TimedSpan[]) {
  const ids = new Set(spans.map((s) => s.span_id))
  const roots = spans
    .filter((s) => !s.parent_span_id || !ids.has(s.parent_span_id))
    .sort((a, b) => Date.parse(a.start) - Date.parse(b.start))
  const root = roots.at(0)
  return {
    root,
    service: root?.service || spans.find((s) => s.service)?.service || "",
    start: root?.start ?? spans.at(0)?.start ?? "",
    durationMs: timeDomain(spans)[1],
    error: spans.some((s) => s.status === "error"),
  }
}

function TracePage() {
  const { id } = Route.useParams()
  const search = Route.useSearch()
  const q = useQuery(traceQuery(id))
  const navigate = useNavigate()
  const router = useRouter()
  const { caps, has } = useCapabilities()
  useDocumentTitle({ page: "trace", id })
  // The replay drawer (plan F1's, as the run page mounts it): offered
  // when the server has the playground and the bearer may act.
  const replayable = canReplay(caps, studioToken())
  const [replay, setReplay] = useState<{
    req: ReplayRequest | null
    n: number
  }>({ req: null, n: 0 })
  const openReplay = useCallback(
    (req: ReplayRequest) => setReplay((cur) => ({ req, n: cur.n + 1 })),
    []
  )

  const spans = useMemo(() => q.data?.spans ?? [], [q.data])
  const rows = useMemo(() => keyRows(spansFromTimed(spans)), [spans])
  const view = search.view === "chat" ? "chat" : "tree"
  const wanted = search.sel ?? (search.span ? `t:${search.span}` : undefined)
  const resolved = resolveSel(rows, wanted)
  // The page's state is its URL (G2): the view is a view change — a
  // history entry, so back walks it — and the selection a cursor move,
  // replaced in place.
  // The run the scope switch came from (U30.2): kept on every link.
  const from = search.run
  const setView = (v: "tree" | "chat") =>
    void navigate({
      ...traceSelectionLink(id, {
        sel: resolved.key ?? wanted,
        view: v,
        run: from,
      }),
    })
  const setSel = (row: TraceRow) =>
    void navigate({
      ...traceSelectionLink(id, { sel: row.key, view, run: from }),
      replace: true,
    })
  const hrefOf = (row: TraceRow) =>
    router.history.createHref(
      router.buildLocation(
        traceSelectionLink(id, { sel: row.key, view, run: from })
      ).publicHref
    )

  if (q.isPending) {
    return (
      <div className="flex justify-center py-24">
        <Spinner />
      </div>
    )
  }
  if (q.isError) {
    if (isNotFound(q.error)) return <NotFound kind="trace" id={id} />
    return (
      <LoadFailed
        kind="trace"
        id={id}
        error={q.error}
        onRetry={() => void q.refetch()}
      />
    )
  }
  const genai = spans.filter(isGenAISpan)
  const facts = traceFacts(spans)
  // The runs the trace makes (api.go's traceDoc): weft's and the
  // derived ones (I6, UX-H-03); a Studio before it names weft's alone.
  const runs = q.data.run_ids ?? traceRuns(spans)
  const notRun = runs.length === 0 ? q.data.not_run : undefined
  const selected = resolved.id
    ? rows.find((r) => r.id === resolved.id)?.timed?.span
    : undefined
  // "this run" (U30.2, UX-A-17): the run the switch came from, else
  // the trace's root run; the selection carried in that run's keys.
  const root = rootRun(rows)
  const back = from && runs.includes(from) ? from : root || runs.at(0)
  const backSel =
    resolved.key && back ? toRunKey(resolved.key, root, back) : undefined

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="font-mono text-base font-medium tracking-tight">
          trace {id.slice(0, 16)}
          {id.length > 16 ? "…" : ""}
        </h1>
        <span className="font-mono text-[11px] text-faint">
          {spans.length} {spans.length === 1 ? "span" : "spans"} ·{" "}
          {spanMs(facts.durationMs)}
        </span>
        {back ? (
          <ScopeSwitch
            at="trace"
            run={runLink(back, backSel ? { sel: backSel } : {})}
          />
        ) : null}
        {has("traces") ? (
          <Link
            {...tracesLink()}
            className="font-mono text-[11px] text-thread-ink hover:underline"
          >
            all traces
          </Link>
        ) : null}
        <div className="ml-auto flex gap-1">
          {(["tree", "chat"] as const).map((v) => (
            <button
              key={v}
              type="button"
              className={`rounded-md border px-2 py-1 font-mono text-[11px] ${
                view === v
                  ? "border-thread/60 bg-secondary"
                  : "text-muted-foreground hover:text-foreground"
              }`}
              aria-pressed={view === v}
              disabled={v === "chat" && genai.length === 0}
              title={
                v === "chat"
                  ? genai.length
                    ? "GenAI spans as a conversation"
                    : "no GenAI semconv spans in this trace"
                  : "the span tree on the time axis"
              }
              onClick={() => setView(v)}
            >
              {v}
            </button>
          ))}
        </div>
      </div>

      {/* The trace as the list shows it (routes/traces.index.tsx), and
          the runs it holds. */}
      <div
        className="flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-[11px]"
        data-trace-facts
      >
        <span>
          <span className="text-faint">root </span>
          {facts.root ? (
            facts.root.name
          ) : (
            <span className="text-muted-foreground">(no root span)</span>
          )}
        </span>
        <span>
          <span className="text-faint">service </span>
          {facts.service || <span className="text-faint">—</span>}
        </span>
        {facts.start ? (
          <span title={absoluteTime(facts.start)}>
            <span className="text-faint">started </span>
            {relativeTime(facts.start)}
          </span>
        ) : null}
        {facts.error ? (
          <span className={`${STATUS_TEXT.bad}`} data-trace-error>
            error
          </span>
        ) : null}
        <nav
          aria-label="runs in this trace"
          className="flex flex-wrap items-center gap-x-2"
          data-trace-runs
        >
          <span className="text-faint">runs</span>
          {runs.length === 0 ? (
            notRun ? (
              // Why the spans make no run (UX-H-02), and what to set.
              <span className="text-muted-foreground" data-not-run>
                {DERIVED_WORDS.notRun}: {notRun.reason}
                <span className="text-faint"> · fix: {notRun.fix}</span>
              </span>
            ) : (
              <span
                className="text-faint"
                title="no weft run: a trace of non-weft spans only"
              >
                —
              </span>
            )
          ) : (
            runs.map((r) => (
              <span key={r} className="inline-flex items-center gap-1.5">
                <Link
                  {...runLink(r)}
                  data-trace-run={r}
                  className="text-muted-foreground hover:text-thread-ink hover:underline"
                >
                  {r}
                </Link>
                {isDerivedRunId(r) ? <HoleBadge {...DERIVED_HOLE} /> : null}
              </span>
            ))
          )}
        </nav>
      </div>

      {resolved.ambiguous ? (
        <p
          className="font-mono text-[11px] text-muted-foreground"
          role="status"
          data-sel-note
        >
          {OLD_KEY_NOTE}
        </p>
      ) : null}

      {view === "tree" ? (
        // The tree beside the selected span (plan H3): the divider
        // dragged or arrowed, its place remembered per device; at
        // phone width the detail stacks under the tree.
        <SplitPane
          split={SPLITS.traceDetail}
          stackedClassName="gap-3"
          panes={[
            {
              id: "tree",
              label: "the span tree",
              defaultSize: 60,
              minSize: 30,
              children: (
                <div className="rounded-lg border bg-background">
                  <Waterfall
                    spans={rows}
                    domain={timeDomain(spans)}
                    playhead={null}
                    unit="ms"
                    onSelect={setSel}
                    selectedId={resolved.id}
                    hrefOf={hrefOf}
                  />
                </div>
              ),
            },
            {
              id: "detail",
              label: "the span detail",
              defaultSize: 40,
              minSize: 25,
              children: (
                <SelectedSpan
                  span={selected}
                  onReplay={replayable ? openReplay : null}
                />
              ),
            },
          ]}
        />
      ) : (
        <GenAIConversation spans={genai} />
      )}
      {replayable ? (
        <ReplayDrawer
          request={replay.req}
          requestKey={replay.n}
          onClose={() => setReplay((cur) => ({ ...cur, req: null }))}
        />
      ) : null}
    </div>
  )
}

/** A model call's run and step (the core stamps both on its chat span). */
function runStepOf(span: TimedSpan): { run: string; step: number } | null {
  const run = span.attrs["weft.run.id"]
  const raw = span.attrs["weft.step.index"]
  const step =
    typeof raw === "number"
      ? raw
      : typeof raw === "string" && /^\d+$/.test(raw)
        ? Number(raw)
        : NaN
  if (typeof run !== "string" || !run || !Number.isInteger(step) || step < 0)
    return null
  return { run, step }
}

/** "replay from this step" on an errored model call of a playground
 * run: the run page's own verb, in its own drawer. The app's own run
 * gets a disabled one that says where replay runs. */
function ReplayFromHere({
  run,
  step,
  onReplay,
}: {
  run: string
  step: number
  onReplay: (r: ReplayRequest) => void
}) {
  const doc = useQuery(runQuery(run))
  if (!doc.data) return null
  if (!doc.data.playground)
    return (
      <Button
        variant="outline"
        size="sm"
        disabled
        data-replay-from-here="off"
        title="the app's own run: replay runs through the playground"
      >
        replay from this step
      </Button>
    )
  return (
    <Button
      variant="outline"
      size="sm"
      data-replay-from-here={`${run}#${step}`}
      onClick={(e) =>
        onReplay({
          runID: run,
          draft: replayFromStep(step),
          opener: e.currentTarget,
        })
      }
    >
      replay from this step
    </Button>
  )
}

/** The selected span's facts, its chat (a model call's messages) and
 * its attributes. */
function SelectedSpan({
  span,
  onReplay,
}: {
  span: TimedSpan | undefined
  onReplay: ((r: ReplayRequest) => void) | null
}) {
  if (!span) {
    return (
      <p className="font-mono text-[11px] text-faint">
        select a span for its attributes
      </p>
    )
  }
  const chat = genaiChat(span.attrs)
  const modelCall = isModelCall(span.attrs)
  const at =
    span.status === "error" && span.attrs["gen_ai.operation.name"] === "chat"
      ? runStepOf(span)
      : null
  return (
    <div className="grid min-w-0 gap-3">
      {at && onReplay ? (
        <div className="flex flex-wrap items-center gap-2" data-span-retry>
          <span className={`font-mono text-[11px] ${STATUS_TEXT.bad}`}>
            {span.status_message || "the model call failed"}
          </span>
          <ReplayFromHere run={at.run} step={at.step} onReplay={onReplay} />
        </div>
      ) : null}
      {chat.family || modelCall ? (
        <section
          className="codewin"
          aria-label="chat"
          data-span-chat={chat.family ?? "not_recorded"}
        >
          <div className="codewin-bar">
            <span className="font-mono text-[11px] text-code-mut">
              chat
              {chat.family === "legacy"
                ? " · legacy gen_ai.prompt / completion keys"
                : ""}
            </span>
          </div>
          <div className="overflow-auto px-3 py-3">
            {chat.family ? (
              <ChatMessages messages={chat.messages} problems={chat.problems} />
            ) : (
              <HoleBadge
                hole="not_recorded"
                reason={NO_MESSAGES_REASON}
                detail
              />
            )}
          </div>
        </section>
      ) : null}
      <div className="codewin">
        <div className="codewin-bar">
          <span className="font-mono text-[11px] text-code-mut">
            {span.name} · {span.service || "—"}
          </span>
        </div>
        <div className="overflow-auto px-3 py-3">
          <JsonTree value={span.attrs} openDepth={2} />
        </div>
      </div>
      {span.events.length > 0 ? (
        <div className="codewin">
          <div className="codewin-bar">
            <span className="font-mono text-[11px] text-code-mut">
              span events
            </span>
          </div>
          <div className="overflow-auto px-3 py-3">
            <JsonTree value={span.events} openDepth={1} />
          </div>
        </div>
      ) : null}
    </div>
  )
}

/** The GenAI chat view: one block per GenAI span, its messages drawn
 * from either key family, or the honesty line when it carries none. */
function GenAIConversation({ spans }: { spans: TimedSpan[] }) {
  const rows = spans
    .slice()
    .sort((a, b) => Date.parse(a.start) - Date.parse(b.start))
  return (
    <div className="space-y-4">
      {rows.map((sp) => {
        const chat = genaiChat(sp.attrs)
        if (!chat.family && !isModelCall(sp.attrs)) return null
        const runID =
          typeof sp.attrs["weft.run.id"] === "string"
            ? sp.attrs["weft.run.id"]
            : null
        return (
          <section
            key={sp.span_id}
            className="space-y-1"
            aria-label={sp.name}
            data-chat-span={sp.span_id}
          >
            <div className="flex flex-wrap items-center gap-2 font-mono text-[11px] text-faint">
              <span className="text-foreground">{sp.name}</span>
              <span>{String(sp.attrs["gen_ai.operation.name"] ?? "")}</span>
              <span>{spanMs(Date.parse(sp.end) - Date.parse(sp.start))}</span>
              {runID ? (
                <Link
                  {...runLink(runID)}
                  className="[overflow-wrap:anywhere] text-thread-ink hover:underline"
                >
                  {runID}
                </Link>
              ) : null}
            </div>
            {chat.family ? (
              <ChatMessages
                messages={chat.messages}
                problems={chat.problems}
                fold
              />
            ) : (
              <HoleBadge hole="not_recorded" reason={NO_MESSAGES_REASON} />
            )}
          </section>
        )
      })}
      {rows.length === 0 ? (
        <p className="py-8 text-center font-mono text-xs text-faint">
          no GenAI spans in this trace
        </p>
      ) : null}
    </div>
  )
}
