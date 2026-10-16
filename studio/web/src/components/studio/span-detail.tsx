// The drawer beside the run tree (U10.1, audit §6.3): the selected
// row, answer first. Its head names the row's kind (RUN, STEP, MODEL,
// ATTEMPT, TOOL, CHILD RUN, SPAN) and carries the verbs, always drawn —
// replay from here · edit… · compare… (UX-C-23). Then the answer: for a
// step of the run (or its model call, an attempt, a tool of it) the
// verdict line, what was sent, what the model replied, what the tools
// did and what came next — lib/summarize.ts's words; for anything else
// its facts. Below, the lazy tabs — Request · Response · Tool I/O ·
// Logs · Raw OTel — only the open one drawn (and, for Logs, read);
// ?tab= names it. Everything renders from the fold AT THE PLAYHEAD, so
// scrubbing replays inside the drawer too: a call reads "running…"
// until its finish is revealed.
import { useQuery } from "@tanstack/react-query"
import { ArrowUpRight } from "lucide-react"
import { Link } from "@tanstack/react-router"
import { useId, useState } from "react"

import { derivedWords, isDerived } from "@/lib/honesty"
import { allLogsQuery, LOGS } from "@/lib/logs"
import { studioToken } from "@/lib/api"
import type { RunDoc, Span as TimedSpan, WireEvent } from "@/lib/api"
import { attemptLine, msText } from "@/lib/attempts"
import { toolFailure } from "@/lib/error-kind"
import type { FoldedRun, FoldedStep, FoldedToolCall } from "@/lib/events"
import { spanMs, usageSummary } from "@/lib/format"
import { compareLink, runLink } from "@/lib/links"
import {
  editPromptAndReplay,
  editResultAndReplay,
  replayFromStep,
  replayOffWhy,
  rerun,
} from "@/lib/replay"
import {
  DRAWER,
  DRAWER_TAB_LABEL,
  DRAWER_TABS,
  defaultTab,
  derivedStep,
  KIND_LABEL,
  stepRowOf,
} from "@/lib/run-tree"
import type { DrawerTab } from "@/lib/run-tree"
import type { AnswerStep } from "@/lib/summarize"
import {
  ANSWER_LABELS,
  repliedLine,
  sentLine,
  stepVerdict,
  thenLine,
  toolsLine,
} from "@/lib/summarize"
import type { Span } from "@/lib/trace"
import { JsonTree } from "@/components/studio/json-tree"
import { EventsExplorer } from "@/components/studio/raw-view"
import { HoleBadges } from "@/components/studio/hole-badge"
import { LogLines, LogsHoles } from "@/components/studio/logs-view"
import { useReplay } from "@/components/studio/replay-drawer"
import {
  AttemptsSection,
  StepHeadline,
  stepAttemptsHole,
} from "@/components/studio/step-attempts"
import { RequestSection } from "@/components/studio/step-request"
import type { RunRequests } from "@/components/studio/step-request"
import {
  StepBody,
  ToolCallRow,
  stepOutcome,
} from "@/components/studio/step-list"
import type { ChildRow } from "@/components/studio/subagent-block"
import { Button } from "@/components/ui/button"
import { useCapabilities } from "@/hooks/use-capabilities"

function Facts({ rows }: { rows: [string, React.ReactNode][] }) {
  return (
    <dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 font-mono text-xs">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-faint">{k}</dt>
          <dd className="min-w-0 break-all">{v}</dd>
        </div>
      ))}
    </dl>
  )
}

function RunFacts({
  run,
  span,
  doc,
  runStatus,
}: {
  run: FoldedRun
  span: Span
  doc: RunDoc
  runStatus: string
}) {
  const isChild = span.kind === "subagent"
  const link = isChild
    ? doc.children.find((c) => c.id === run.runId)
    : undefined
  const calls = run.steps.flatMap((s) => s.toolCalls)
  const errors = calls.filter((c) => c.result?.isError).length
  return (
    <div className="space-y-3">
      <Facts
        rows={[
          ["run", run.runId || doc.id],
          ["agent", run.agent || "—"],
          [
            "model",
            run.model ? `${run.model.provider}/${run.model.name}` : "—",
          ],
          [
            "status",
            run.finished
              ? isChild
                ? "finished"
                : runStatus
              : runStatus === "running"
                ? "running…"
                : "did not finish",
          ],
          ["steps", String(run.steps.length)],
          [
            "tool calls",
            `${calls.length}${errors ? ` · ${errors} error${errors > 1 ? "s" : ""}` : ""}`,
          ],
          ["usage", run.usage ? usageSummary(run.usage) : "—"],
          [
            "events",
            span.to === null
              ? `${span.from}– (open)`
              : `${span.from}–${span.to}`,
          ],
        ]}
      />
      {link ? (
        <Link
          {...runLink(link.id)}
          className="inline-flex items-center gap-1 font-mono text-[11px] text-thread-ink hover:underline"
        >
          open the child's own run page
          <ArrowUpRight className="size-3" />
        </Link>
      ) : null}
      {run.pending.length > 0 ? (
        <div className="rounded-md border border-ev-error/30 px-3 py-2 text-xs">
          <span className="eyebrow">pending approval</span>
          <ul className="mt-1 space-y-0.5 font-mono">
            {run.pending.map((p) => (
              <li key={p.id}>
                {p.name}({p.args == null ? "" : JSON.stringify(p.args)})
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  )
}

function RunSteps({
  run,
  runStatus,
  onJump,
  childLinks,
}: {
  run: FoldedRun
  runStatus: string
  onJump: (t: number) => void
  childLinks: Map<string, ChildRow>
}) {
  return (
    <div className="space-y-2">
      <span className="eyebrow">steps</span>
      {run.steps.map((step) => (
        <div key={step.index} className="rounded-md border px-3 py-2">
          <div className="mb-1.5 flex flex-wrap items-center gap-2">
            <span className="eyebrow">step {step.index}</span>
            {stepOutcome(step, runStatus)}
          </div>
          <StepBody
            step={step}
            runStatus={runStatus}
            childLinks={childLinks}
            onJump={onJump}
            compact
          />
        </div>
      ))}
      {run.steps.length === 0 ? (
        <p className="font-mono text-xs text-faint">no steps yet</p>
      ) : null}
    </div>
  )
}

function CallFacts({
  call,
  runStatus,
}: {
  call: FoldedToolCall
  runStatus: string
}) {
  return (
    <Facts
      rows={[
        ["tool", call.name],
        ["call id", call.callId],
        ["started at", `event ${call.startPos}`],
        [
          "finished at",
          call.finishPos === undefined
            ? runStatus === "running"
              ? "not yet"
              : "never"
            : `event ${call.finishPos}`,
        ],
      ]}
    />
  )
}

/** The answer (§6.3): verdict · sent · replied · tools ran · then, for
 * one of the run's own steps. */
function Answer({
  step,
  doc,
  runStatus,
  ms,
  requests,
  hasNext,
  onTab,
  derived = false,
}: {
  step: AnswerStep
  doc: RunDoc
  runStatus: string
  ms?: number
  requests?: RunRequests
  hasNext: boolean
  onTab: (t: DrawerTab) => void
  /** A derived run's step (lib/run-tree.ts's derivedStep): read from
   * its spans; what no span carries says so. */
  derived?: boolean
}) {
  // While scrubbing the run reads as running: the verdict and the end
  // are the playhead's, not the record's.
  const run = runStatus === doc.status ? doc : { status: runStatus }
  const verdict = stepVerdict(step, run, ms)
  const mine = requests?.steps.get(step.index)
  const row = mine?.rows.at(-1)
  const prev = requests?.prev.get(step.index)
  const sent = derived
    ? DRAWER.derivedNoEvents
    : requests?.loading
      ? DRAWER.loading
      : sentLine(
          row
            ? {
                count: row.body.messages_ref.count,
                prevCount: prev?.body.messages_ref.count,
                tools: row.body.tools.names.length,
                promptChanged: mine?.promptChanged,
                catalogChanged: mine?.catalogChanged,
              }
            : undefined
        )
  const running = runStatus === "running"
  const line = (
    k: keyof typeof ANSWER_LABELS,
    text: string,
    to?: DrawerTab,
    bad = false
  ) => (
    <div key={k} className="contents">
      <dt className="text-faint">{ANSWER_LABELS[k]}</dt>
      <dd
        className={`min-w-0 break-words ${bad ? "text-status-bad" : ""}`}
        data-answer-line={k}
      >
        {text}
        {to ? (
          <button
            type="button"
            className="ml-2 text-thread-ink hover:underline"
            onClick={() => onTab(to)}
          >
            → {DRAWER_TAB_LABEL[to]}
          </button>
        ) : null}
      </dd>
    </div>
  )
  return (
    <dl
      className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1 font-mono text-xs"
      data-answer
    >
      <div className="contents">
        <dt className="text-faint">{ANSWER_LABELS.verdict}</dt>
        <dd
          className={`min-w-0 break-words ${verdict.bad ? "text-status-bad" : "text-foreground"}`}
          data-verdict
          data-bad={verdict.bad || undefined}
        >
          {verdict.text}
        </dd>
      </div>
      {line("sent", sent, derived ? undefined : "request")}
      {line(
        "replied",
        derived && !step.toolCalls.length
          ? DRAWER.derivedNoEvents
          : repliedLine(step, running),
        derived ? undefined : "response"
      )}
      {line(
        "tools",
        toolsLine(step, running),
        step.toolCalls.length && !derived ? "tools" : undefined,
        step.toolCalls.some((c) => c.result?.isError)
      )}
      {line("then", thenLine(step, run, hasNext), undefined, verdict.bad)}
    </dl>
  )
}

/** The drawer's verbs (UX-C-23): drawn, with their words, wherever the
 * selection has a step of the run to replay from — the experiment
 * drawer's entry points (lib/replay.ts), and the compare page landing
 * on this step. Hidden, not disabled, where a verb cannot act — one
 * faint line says why in their place (lib/replay.ts's replayOffWhy:
 * no playground, a read token, a running run; a derived run the
 * stock_spans words). */
function Verbs({
  doc,
  step,
  call,
  childRun,
  maxFrom,
  onStory,
}: {
  doc: RunDoc
  step?: number
  call?: FoldedToolCall
  childRun?: string
  maxFrom: number | null
  onStory?: () => void
}) {
  const open = useReplay()
  const { caps, has, why } = useCapabilities()
  const live = doc.status === "running"
  const child = childRun
    ? doc.children.find((c) => c.id === childRun)
    : undefined
  const btn = (
    verb: string,
    label: string,
    title: string,
    onClick: (e: React.MouseEvent<HTMLButtonElement>) => void
  ) => (
    <Button
      key={verb}
      variant="outline"
      size="xs"
      className="h-6 px-2 font-mono text-[11px]"
      title={title}
      data-drawer-verb={verb}
      onClick={onClick}
    >
      {label}
    </Button>
  )
  const agent = doc.agent || undefined
  const verbs: React.ReactNode[] = []
  if (open && !live && child)
    verbs.push(
      btn("rerun", DRAWER.rerunChild, `replay the child run ${child.id}`, (e) =>
        open({
          runID: child.id,
          agent: child.agent || undefined,
          draft: rerun(),
          opener: e.currentTarget,
        })
      )
    )
  else if (open && !live && step !== undefined) {
    verbs.push(
      btn("from_step", DRAWER.replay, DRAWER.replayHint(step), (e) =>
        open({
          runID: doc.id,
          agent,
          draft: replayFromStep(step),
          opener: e.currentTarget,
        })
      )
    )
    const content = call?.result?.content
    if (
      call &&
      content !== undefined &&
      maxFrom !== null &&
      step + 1 <= maxFrom
    )
      verbs.push(
        btn(
          "edit_result",
          DRAWER.editResult,
          DRAWER.editResultHint(call.callId),
          (e) =>
            open({
              runID: doc.id,
              agent,
              draft: editResultAndReplay(step, call.callId, content),
              opener: e.currentTarget,
            })
        )
      )
    else
      verbs.push(
        btn("edit_prompt", DRAWER.edit, DRAWER.editHint(step), (e) =>
          open({
            runID: doc.id,
            agent,
            draft: editPromptAndReplay(step),
            opener: e.currentTarget,
          })
        )
      )
  }
  const compare = has("diff") ? (
    <Link
      {...compareLink(doc.id, [], step)}
      className="font-mono text-[11px] text-thread-ink hover:underline"
      title={DRAWER.compareHint}
      data-drawer-verb="compare"
    >
      {DRAWER.compare}
    </Link>
  ) : null
  // Where a replay verb would stand and none is offered: why, faintly.
  const off =
    !verbs.length && (child || step !== undefined)
      ? isDerived(doc)
        ? derivedWords()
        : replayOffWhy(caps, studioToken(), live, why("playground"))
      : undefined
  if (!verbs.length && !off && !compare && !onStory) return null
  return (
    <div
      className="flex flex-wrap items-center gap-2 border-b px-3 py-1.5"
      data-drawer-verbs
    >
      {verbs}
      {off ? (
        <span
          className="font-mono text-[11px] text-faint"
          data-drawer-verbs-off
        >
          {off}
        </span>
      ) : null}
      {compare}
      {onStory && step !== undefined ? (
        <button
          type="button"
          className="ml-auto font-mono text-[11px] text-muted-foreground hover:text-foreground hover:underline"
          title={DRAWER.storyHint}
          data-drawer-story
          onClick={onStory}
        >
          {DRAWER.story}
        </button>
      ) : null}
    </div>
  )
}

/** The Request tab's section, open (the tab is the request). */
function OpenRequest({ req, step }: { req: RunRequests; step: number }) {
  const [open, setOpen] = useState(true)
  return (
    <RequestSection req={req} step={step} open={open} onOpenChange={setOpen} />
  )
}

/** The span ids under a row: its own timed span and its subtree's,
 * and — on a scale without spans — the step's spans by their
 * weft.step.index (the run's, and their descendants). null: the whole
 * run (its root row). */
function spanIdsFor(
  row: Span | undefined,
  rows: readonly Span[],
  timed: readonly TimedSpan[],
  runId: string,
  step?: number
): Set<string> | null {
  if (!row || (row.kind === "run" && row.parent === undefined)) return null
  const ids = new Set<string>()
  const under = new Set([row.key])
  for (const r of rows) if (r.parent && under.has(r.parent)) under.add(r.key)
  for (const r of rows)
    if (under.has(r.key) && r.timed) ids.add(r.timed.span.span_id)
  if (!ids.size && step !== undefined)
    for (const s of timed)
      if (
        (s.run_id ?? s.attrs["weft.run.id"]) === runId &&
        Number(s.attrs["weft.step.index"]) === step
      )
        ids.add(s.span_id)
  // Their descendants (an app's own spans under a tool, the logs'
  // usual home).
  for (let grew = true; grew;) {
    grew = false
    for (const s of timed)
      if (
        s.parent_span_id &&
        ids.has(s.parent_span_id) &&
        !ids.has(s.span_id)
      ) {
        ids.add(s.span_id)
        grew = true
      }
  }
  return ids
}

/** The Logs tab: the run's app log lines (GET runs/{id}/logs, every
 * page up to lib/logs.ts's cap — a cut list badged truncated) under the
 * row's spans — read when the tab opens, the route's hole badges (a
 * run recorded without otel.Slog(), a read token's hidden) as they
 * come. The read is the run-level Logs view's too (one query). */
function LogsTab({
  runId,
  spans,
  running,
}: {
  runId: string
  spans: Set<string> | null
  /** The run is running: its lines are read again, and a row with no
   * line yet says its spans are pending, never "no line". */
  running: boolean
}) {
  const { has } = useCapabilities()
  const on = has("logs")
  const q = useQuery({
    ...allLogsQuery(runId, undefined, running),
    enabled: on,
  })
  if (!on)
    return <p className="font-mono text-xs text-faint">{DRAWER.logsOff}</p>
  if (q.isPending)
    return <p className="font-mono text-xs text-faint">{DRAWER.loading}</p>
  if (q.isError)
    return (
      <p className="font-mono text-xs text-status-bad">{q.error.message}</p>
    )
  const page = q.data
  const lines = page.logs.filter(
    (l) => spans === null || (l.span_id !== undefined && spans.has(l.span_id))
  )
  return (
    <div className="space-y-2" data-drawer-logs>
      <LogsHoles page={page} />
      {lines.length ? (
        <LogLines lines={lines} />
      ) : page.badge ? null : running || page.partial ? (
        <p className="font-mono text-xs text-faint" data-logs-pending>
          {LOGS.pending}
        </p>
      ) : (
        <p className="font-mono text-xs text-faint">{DRAWER.noLogs}</p>
      )}
    </div>
  )
}

export function SpanDetail({
  span,
  row,
  rows = [],
  folded = [],
  events,
  base = 0,
  doc,
  runStatus,
  playhead,
  tab,
  onTab,
  onJump,
  onStory,
  requests,
  timed,
  maxFrom = null,
}: {
  /** The selected row as the drawer reads it (lib/run-tree.ts's
   * detailSpan), resolved against the fold at the playhead. */
  span: Span | undefined
  /** The run tree's row itself (its kind, its scale's extent). */
  row?: Span
  /** Every row the tree draws (the step a row is under, its spans). */
  rows?: readonly Span[]
  /** The fold's rows at the playhead (a step's own node). */
  folded?: readonly Span[]
  events: WireEvent[]
  /** The stream index of events[0] (the ring's evicted count). */
  base?: number
  doc: RunDoc
  runStatus: string
  playhead: number | null
  /** The open tab (?tab=); absent, the row's default. */
  tab?: DrawerTab
  onTab: (t: DrawerTab) => void
  onJump: (t: number) => void
  /** Read the selection in the story (its read mode). */
  onStory?: () => void
  /** The run's request record: a step of the run's own (not a
   * subagent's) shows what it called the model with. */
  requests?: RunRequests
  /** The run's timed spans (the time axis): a chat span's detail
   * reads its attempt children from them. */
  timed?: TimedSpan[]
  /** The highest from_step a replay may take (replayBounds), null
   * while unknown. */
  maxFrom?: number | null
}) {
  // The run document's children by child id; the page stamped each
  // call with its child's id (linkView — call ids may repeat across
  // steps, child ids carry the step).
  const childLinks = new Map<string, ChildRow>(
    doc.children.map((c) => [c.id, c])
  )
  // The tabs' ids (aria-controls / aria-labelledby): one drawer a page,
  // unique all the same.
  const tabsId = useId()
  if (!span)
    return (
      <div
        className="flex min-h-0 flex-col rounded-lg border bg-background px-3 py-3"
        data-drawer
      >
        <p className="font-mono text-xs text-faint">{DRAWER.select}</p>
      </div>
    )
  const kind = row?.kind ?? span.kind
  const active = tab ?? defaultTab(kind)
  // The step the selection is under: the run's own (s<n>), with its
  // folded node — the answer's subject.
  const stepRow =
    stepRowOf(row?.key ?? span.key, rows) ??
    (span.kind === "step" ? span : undefined)
  const ownStep =
    stepRow && /^s\d+$/.test(stepRow.key) ? stepRow.key : undefined
  const stepSpan = ownStep
    ? span.key === ownStep && span.node
      ? span
      : folded.find((s) => s.key === ownStep)
    : undefined
  const step =
    stepSpan?.kind === "step" ? (stepSpan.node as FoldedStep) : undefined
  const call =
    span.kind === "tool" && span.node
      ? (span.node as FoldedToolCall)
      : undefined
  const stepMs =
    stepRow?.timed && stepRow.timed.toMs !== null
      ? stepRow.timed.toMs - stepRow.timed.fromMs
      : undefined
  // A derived run's fold is empty: its step's answer reads its spans.
  const derived = isDerived(doc)
  const answer: AnswerStep | undefined =
    step ?? (derived && ownStep ? derivedStep(ownStep, rows) : undefined)
  const hasNext =
    answer !== undefined &&
    (derived ? rows : folded).some((s) => s.key === `s${answer.index + 1}`)
  // The timed span behind the row, else (a step: no span of its own)
  // its model call's.
  const own =
    row?.timed ??
    span.timed ??
    rows.find((r) => r.parent === (row?.key ?? span.key) && r.timed)?.timed
  const tabs = DRAWER_TABS
  return (
    <div
      className="flex min-h-0 flex-col rounded-lg border bg-background"
      data-drawer
      data-drawer-kind={kind}
    >
      <div className="flex items-center gap-2 border-b px-3 py-1.5 font-mono text-[11px]">
        <span className="text-faint uppercase" data-kind-label>
          {KIND_LABEL[kind]}
        </span>
        <span className="min-w-0 truncate text-foreground">
          {row?.label ?? span.label}
        </span>
        {/* A derived run (plan I1.3) has no events: no playhead to put
            at a span's start; a timed row's extent is milliseconds, not
            a position. */}
        {isDerived(doc) || span.timed || span.node === undefined ? null : (
          <Button
            variant="ghost"
            size="xs"
            className="ml-auto h-5 px-1.5 font-mono text-[10px] text-faint hover:text-foreground"
            title={`move the playhead to the start of this row (event #${span.from})`}
            onClick={() => onJump(span.from + 1)}
          >
            {DRAWER.jump}
          </Button>
        )}
      </div>
      <Verbs
        doc={doc}
        step={answer?.index}
        call={call}
        childRun={row?.childRun}
        maxFrom={maxFrom}
        onStory={onStory}
      />
      <div className="min-h-0 flex-1 overflow-auto">
        <div className="space-y-3 px-3 py-3" data-drawer-answer>
          {answer ? (
            <Answer
              step={answer}
              derived={!step}
              doc={doc}
              runStatus={runStatus}
              ms={stepMs}
              requests={requests}
              hasNext={hasNext}
              onTab={onTab}
            />
          ) : null}
          {kind === "step" && span.kind === "step" ? (
            <div className="flex flex-wrap items-center gap-2">
              {stepOutcome(span.node as FoldedStep, runStatus)}
              {ownStep ? (
                <StepHeadline
                  step={span.node as FoldedStep}
                  runId={doc.id}
                  runStatus={runStatus}
                  requests={requests}
                />
              ) : null}
              {ownStep ? (
                <HoleBadges
                  holes={stepAttemptsHole(span.node as FoldedStep, requests)}
                />
              ) : null}
              <span className="ml-auto font-mono text-[10px] text-faint tabular-nums">
                events {span.from}–{span.to}
              </span>
            </div>
          ) : null}
          {kind === "step" && span.kind === "step" && ownStep ? (
            <AttemptsSection
              step={span.node as FoldedStep}
              runId={doc.id}
              runStatus={runStatus}
              requests={requests}
              defaultOpen
            />
          ) : null}
          {span.timed && span.kind !== "step" ? (
            <TimedFacts
              span={span}
              runId={doc.id}
              runStatus={runStatus}
              timed={timed}
            />
          ) : (span.kind === "run" || span.kind === "subagent") && span.node ? (
            <RunFacts
              run={span.node as FoldedRun}
              span={span}
              doc={doc}
              runStatus={runStatus}
            />
          ) : call ? (
            <>
              <CallFacts call={call} runStatus={runStatus} />
              {row?.timed ? <Facts rows={toolFacts(row.timed.span)} /> : null}
            </>
          ) : null}
        </div>
        <div
          className="flex items-center gap-1 border-y px-2 pt-1"
          role="tablist"
          aria-label="drawer"
          // The tabs pattern: ←/→ move (wrapping), Home/End jump; the
          // open tab is the one tab stop (roving tabindex).
          onKeyDown={(e) => {
            const i = tabs.indexOf(active)
            const n = tabs.length
            const to =
              e.key === "ArrowRight"
                ? (i + 1) % n
                : e.key === "ArrowLeft"
                  ? (i - 1 + n) % n
                  : e.key === "Home"
                    ? 0
                    : e.key === "End"
                      ? n - 1
                      : -1
            if (to < 0) return
            e.preventDefault()
            onTab(tabs[to])
            document.getElementById(`${tabsId}-tab-${tabs[to]}`)?.focus()
          }}
        >
          {tabs.map((t) => (
            <button
              key={t}
              type="button"
              role="tab"
              id={`${tabsId}-tab-${t}`}
              aria-selected={active === t}
              aria-controls={`${tabsId}-panel`}
              tabIndex={active === t ? 0 : -1}
              data-drawer-tab={t}
              className={`-mb-px border-b-2 px-2.5 py-1.5 font-mono text-[11px] ${
                active === t
                  ? "border-thread text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground"
              }`}
              onClick={() => onTab(t)}
            >
              {DRAWER_TAB_LABEL[t]}
            </button>
          ))}
        </div>
        <div
          className="space-y-3 px-3 py-3"
          role="tabpanel"
          id={`${tabsId}-panel`}
          aria-labelledby={`${tabsId}-tab-${active}`}
          tabIndex={0}
          data-drawer-panel={active}
        >
          {active === "request" ? (
            step && requests ? (
              <OpenRequest req={requests} step={step.index} />
            ) : (
              <p className="font-mono text-xs text-faint">{DRAWER.noRequest}</p>
            )
          ) : active === "response" ? (
            (span.kind === "run" || span.kind === "subagent") && span.node ? (
              <RunSteps
                run={span.node as FoldedRun}
                runStatus={runStatus}
                onJump={onJump}
                childLinks={childLinks}
              />
            ) : step ? (
              <>
                <StepBody
                  step={step}
                  runStatus={runStatus}
                  childLinks={childLinks}
                  onJump={onJump}
                />
                {!step.text &&
                !step.reasoning &&
                step.toolCalls.length === 0 ? (
                  <p className="font-mono text-xs text-faint">
                    nothing revealed for this step yet
                  </p>
                ) : null}
              </>
            ) : span.kind === "step" && span.node ? (
              <StepBody
                step={span.node as FoldedStep}
                runStatus={runStatus}
                childLinks={childLinks}
                onJump={onJump}
              />
            ) : (
              <p className="font-mono text-xs text-faint">
                {DRAWER.noResponse}
              </p>
            )
          ) : active === "tools" ? (
            <ToolsTab
              calls={
                call
                  ? [call]
                  : step
                    ? step.toolCalls
                    : span.kind === "step" && span.node
                      ? (span.node as FoldedStep).toolCalls
                      : []
              }
              runStatus={runStatus}
              childLinks={childLinks}
              onJump={onJump}
            />
          ) : active === "logs" ? (
            <LogsTab
              running={runStatus === "running"}
              runId={row?.runId ?? span.runId}
              spans={spanIdsFor(
                row ?? span,
                rows,
                timed ?? [],
                row?.runId ?? doc.id,
                step?.index
              )}
            />
          ) : (
            <RawTab
              own={own?.span}
              span={span}
              events={events}
              base={base}
              doc={doc}
              playhead={playhead}
              onJump={onJump}
            />
          )}
        </div>
      </div>
    </div>
  )
}

function ToolsTab({
  calls,
  runStatus,
  childLinks,
  onJump,
}: {
  calls: FoldedToolCall[]
  runStatus: string
  childLinks: Map<string, ChildRow>
  onJump: (t: number) => void
}) {
  if (!calls.length)
    return <p className="font-mono text-xs text-faint">{DRAWER.noTools}</p>
  return (
    <div className="space-y-2">
      {calls.map((c) => (
        <ToolCallRow
          key={`${c.startPos}:${c.callId}`}
          call={c}
          runStatus={runStatus}
          child={c.childRunId ? childLinks.get(c.childRunId) : undefined}
          onJump={onJump}
        />
      ))}
    </div>
  )
}

/** The Raw OTel tab: the row's span — its attributes and span events
 * (the old drawer's attribute dump, UX-A-05) — and, for a row the fold
 * holds, its slice of the weft event stream and the folded node. */
function RawTab({
  own,
  span,
  events,
  base,
  doc,
  playhead,
  onJump,
}: {
  own?: TimedSpan
  span: Span
  events: WireEvent[]
  base: number
  doc: RunDoc
  playhead: number | null
  onJump: (t: number) => void
}) {
  const rangeEnd =
    span.to ??
    (playhead !== null ? Math.max(playhead - 1, 0) : base + events.length - 1)
  return (
    <div className="space-y-3" data-drawer-raw>
      {own ? (
        <>
          <Facts
            rows={[
              ["span", own.name],
              ["span id", own.span_id],
              ["service", own.service || "—"],
              [
                "status",
                own.status_message
                  ? `${own.status} · ${own.status_message}`
                  : own.status,
              ],
            ]}
          />
          <div className="codewin">
            <div className="codewin-bar">
              <span className="font-mono text-[11px] text-code-mut">attrs</span>
            </div>
            <div className="overflow-auto px-3 py-3">
              <JsonTree value={own.attrs} openDepth={2} />
            </div>
          </div>
          {own.events.length > 0 ? (
            <div className="codewin">
              <div className="codewin-bar">
                <span className="font-mono text-[11px] text-code-mut">
                  span events
                </span>
              </div>
              <div className="overflow-auto px-3 py-3">
                <JsonTree value={own.events} openDepth={1} />
              </div>
            </div>
          ) : null}
        </>
      ) : (
        <p className="font-mono text-xs text-faint">{DRAWER.noSpan}</p>
      )}
      {span.node && !span.timed ? (
        <>
          <EventsExplorer
            events={events}
            base={base}
            playhead={playhead}
            onJump={onJump}
            range={[span.from, rangeEnd]}
            compact
            derived={isDerived(doc)}
          />
          <div className="codewin">
            <div className="codewin-bar">
              <span className="font-mono text-[11px] text-code-mut">
                folded {span.kind} · at the playhead
              </span>
            </div>
            <div className="overflow-auto px-3 py-3">
              <JsonTree value={span.node} openDepth={3} />
            </div>
          </div>
        </>
      ) : null}
    </div>
  )
}

/** A time-scale row's facts: its span's name, times and status, and
 * — a model call — who answered, the attempts and the first token
 * (S4.7, A4); its attributes are the Raw OTel tab's. */
function TimedFacts({
  span,
  runId,
  runStatus,
  timed,
}: {
  span: Span
  runId: string
  runStatus: string
  timed?: TimedSpan[]
}) {
  const t = span.timed!
  const sp = t.span
  const chat = isChatSpan(sp) ? chatFacts(sp, timed ?? []) : null
  const step = chat ? num(sp.attrs["weft.step.index"]) : undefined
  const chatRun = str(sp.attrs["weft.run.id"]) || runId
  return (
    <div className="space-y-3">
      <Facts
        rows={[
          ["span", sp.name],
          ["span id", sp.span_id],
          [
            "status",
            sp.status_message
              ? `${sp.status} · ${sp.status_message}`
              : sp.status,
          ],
          [
            "time",
            t.toMs == null
              ? `${spanMs(t.fromMs)}– (open)`
              : `${spanMs(t.fromMs)}–${spanMs(t.toMs)} (${spanMs(t.toMs - t.fromMs)})`,
          ],
          ...(chat ?? []),
          ...toolFacts(sp),
        ]}
      />
      {chat && step !== undefined ? (
        <AttemptsSection
          step={{ index: step }}
          runId={chatRun}
          runStatus={runStatus}
          defaultOpen
        />
      ) : null}
    </div>
  )
}

/** An execute_tool span's failure kind and cut (U4), in
 * lib/error-kind.ts's words: error.type with weft.panic.layer /
 * weft.tool.input_error, and weft.tool.truncated_bytes. */
function toolFacts(sp: TimedSpan): [string, React.ReactNode][] {
  if (sp.attrs["gen_ai.operation.name"] !== "execute_tool") return []
  const rows: [string, React.ReactNode][] = []
  const f = toolFailure(str(sp.attrs["error.type"]), {
    panic_layer: str(sp.attrs["weft.panic.layer"]),
    input_error: str(sp.attrs["weft.tool.input_error"]),
    retry: num(sp.attrs["weft.tool.retry"]),
    retry_budget: num(sp.attrs["weft.tool.retry_budget"]),
  })
  if (f)
    rows.push([
      "failure",
      <span key="f" title={f.hint} data-tool-failure>
        {f.word}
      </span>,
    ])
  const cut = num(sp.attrs["weft.tool.truncated_bytes"])
  if (cut) rows.push(["truncated", `${cut} bytes cut by the result cap`])
  return rows
}

function str(v: unknown): string {
  return typeof v === "string" ? v : ""
}

function num(v: unknown): number | undefined {
  const n = typeof v === "string" ? Number(v) : v
  return typeof n === "number" && Number.isInteger(n) && n >= 0 ? n : undefined
}

/** A model call's span: gen_ai.operation.name chat, or named so. */
function isChatSpan(sp: TimedSpan): boolean {
  const op = sp.attrs["gen_ai.operation.name"]
  return op === "chat" || (op === undefined && sp.name.split(" ")[0] === "chat")
}

/** chatFacts is a chat span's A4 facts (ADR 0016's A4 note): the model
 * asked for, the one that answered, "attempt n of m" from its attempt
 * children (the answering one is the last with status ok), and the
 * call's TTFT. */
function chatFacts(
  sp: TimedSpan,
  timed: TimedSpan[]
): [string, React.ReactNode][] {
  const requested = str(sp.attrs["gen_ai.request.model"])
  const kids = timed
    .filter((k) => k.parent_span_id === sp.span_id && k.name === "attempt")
    .sort(
      (a, b) =>
        (num(a.attrs["weft.attempt.index"]) ?? 0) -
        (num(b.attrs["weft.attempt.index"]) ?? 0)
    )
  const ok = kids.filter((k) => k.status === "ok").at(-1)
  const answered =
    str(sp.attrs["gen_ai.response.model"]) ||
    (ok ? str(ok.attrs["gen_ai.request.model"]) : "")
  const rows: [string, React.ReactNode][] = [
    ["requested", requested || "—"],
    ["answered", answered || "not reported"],
  ]
  // No attempt ok on an ended call: every attempt failed (n 0, "none
  // answered"); a call still open says nothing yet.
  const line = attemptLine(
    ok
      ? {
          n: num(ok.attrs["weft.attempt.index"]) ?? kids.indexOf(ok) + 1,
          total: kids.length,
          requested,
          answered,
        }
      : kids.length && sp.status !== "unset"
        ? { n: 0, total: kids.length }
        : null
  )
  if (line) rows.push(["attempts", <span data-attempt-line>{line}</span>])
  else if (kids.length > 1) rows.push(["attempts", String(kids.length)])
  const ttft = num(sp.attrs["weft.ttft_ms"])
  if (ttft) rows.push(["first token", msText(ttft)])
  return rows
}
