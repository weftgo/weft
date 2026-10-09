// The detail panel beside the waterfall: the selected span, read
// three ways — detail (what happened, using the same step and call
// bodies as the story view), events (only this span's slice of the
// stream) and json (the folded node). Everything renders from the
// fold AT THE PLAYHEAD, so scrubbing replays inside the panel too:
// a call reads "running…" until its finish is revealed.
import { ArrowUpRight } from "lucide-react"
import { Link } from "@tanstack/react-router"

import type { RunDoc, Span as TimedSpan, WireEvent } from "@/lib/api"
import { attemptLine, msText } from "@/lib/attempts"
import type { FoldedRun, FoldedStep, FoldedToolCall } from "@/lib/events"
import { spanMs, usageSummary } from "@/lib/format"
import type { Span } from "@/lib/trace"
import { JsonTree } from "@/components/studio/json-tree"
import { EventsExplorer } from "@/components/studio/raw-view"
import { HoleBadges } from "@/components/studio/hole-badge"
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
  Usage,
  stepOutcome,
} from "@/components/studio/step-list"
import type { ChildRow } from "@/components/studio/subagent-block"
import { Button } from "@/components/ui/button"
import { runLink } from "@/lib/links"

export type DetailMode = "detail" | "events" | "json"

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

function RunDetail({
  run,
  span,
  doc,
  runStatus,
  onJump,
  childLinks,
}: {
  run: FoldedRun
  span: Span
  doc: RunDoc
  runStatus: string
  onJump: (t: number) => void
  childLinks: Map<string, ChildRow>
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
    </div>
  )
}

function StepDetail({
  step,
  runId,
  runStatus,
  onJump,
  childLinks,
  requests,
}: {
  step: FoldedStep
  /** The run the step is the run's own step of; absent for a
   * subagent's step (its attempts are on the child's page). */
  runId?: string
  runStatus: string
  onJump: (t: number) => void
  childLinks: Map<string, ChildRow>
  requests?: RunRequests
}) {
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        {stepOutcome(step, runStatus)}
        {runId ? (
          <StepHeadline
            step={step}
            runId={runId}
            runStatus={runStatus}
            requests={requests}
          />
        ) : null}
        {runId ? (
          <HoleBadges holes={stepAttemptsHole(step, requests)} />
        ) : null}
        <span className="ml-auto font-mono text-[10px] text-faint tabular-nums">
          events {step.from}–{step.to}
        </span>
      </div>
      {requests ? <RequestSection req={requests} step={step.index} /> : null}
      {runId ? (
        <AttemptsSection
          step={step}
          runId={runId}
          runStatus={runStatus}
          requests={requests}
          defaultOpen
        />
      ) : null}
      <StepBody
        step={step}
        runStatus={runStatus}
        childLinks={childLinks}
        onJump={onJump}
      />
      {!step.text && !step.reasoning && step.toolCalls.length === 0 ? (
        <p className="font-mono text-xs text-faint">
          nothing revealed for this step yet
        </p>
      ) : null}
    </div>
  )
}

function CallDetail({
  call,
  runStatus,
  onJump,
  childLinks,
}: {
  call: FoldedToolCall
  runStatus: string
  onJump: (t: number) => void
  childLinks: Map<string, ChildRow>
}) {
  return (
    <div className="space-y-3">
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
      <ToolCallRow
        call={call}
        runStatus={runStatus}
        child={call.childRunId ? childLinks.get(call.childRunId) : undefined}
        onJump={onJump}
      />
    </div>
  )
}

export function SpanDetail({
  span,
  events,
  doc,
  runStatus,
  playhead,
  mode,
  onMode,
  onJump,
  requests,
  timed,
}: {
  /** The selected span, resolved against the fold at the playhead. */
  span: Span | undefined
  events: WireEvent[]
  doc: RunDoc
  runStatus: string
  playhead: number | null
  mode: DetailMode
  onMode: (m: DetailMode) => void
  onJump: (t: number) => void
  /** The run's request record: a step of the run's own (not a
   * subagent's) shows what it called the model with. */
  requests?: RunRequests
  /** The run's timed spans (the time axis): a chat span's detail
   * reads its attempt children from them. */
  timed?: TimedSpan[]
}) {
  // The run document's children by child id; the page stamped each
  // call with its child's id (linkView — call ids may repeat across
  // steps, child ids carry the step).
  const childLinks = new Map<string, ChildRow>(
    doc.children.map((c) => [c.id, c])
  )
  const modes: DetailMode[] = span?.timed ? ["detail"] : ["detail", "events", "json"]
  // A time-axis row has only its detail: a ?d=events carried over from
  // the event axis would read the span's milliseconds as positions.
  if (span?.timed) mode = "detail"
  const rangeEnd =
    span?.to ??
    (playhead !== null ? Math.max(playhead - 1, 0) : events.length - 1)
  return (
    <div className="flex min-h-0 flex-col rounded-lg border bg-background">
      <div className="flex items-center gap-1 border-b px-2 pt-1">
        {modes.map((m) => (
          <button
            key={m}
            type="button"
            role="tab"
            aria-selected={mode === m}
            className={`-mb-px border-b-2 px-2.5 py-1.5 font-mono text-[11px] ${
              mode === m
                ? "border-thread text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground"
            }`}
            onClick={() => onMode(m)}
          >
            {m}
          </button>
        ))}
        {span ? (
          <span className="ml-auto flex items-center gap-2 pr-1 font-mono text-[11px] text-faint">
            <span className="uppercase">{span.kind}</span>
            <span className="text-foreground">{span.label}</span>
            <Button
              variant="ghost"
              size="xs"
              className="h-5 px-1.5 font-mono text-[10px] text-faint hover:text-foreground"
              title={`jump to the start of this span (event #${span.from})`}
              onClick={() => onJump(span.from + 1)}
            >
              ▶ replay here
            </Button>
          </span>
        ) : null}
      </div>
      <div className="min-h-0 flex-1 overflow-auto px-3 py-3">
        {!span ? (
          <p className="font-mono text-xs text-faint">
            select a span in the trace
          </p>
        ) : mode === "events" ? (
          <EventsExplorer
            events={events}
            playhead={playhead}
            onJump={onJump}
            range={[span.from, rangeEnd]}
            compact
          />
        ) : mode === "json" ? (
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
        ) : span.timed ? (
          <TimedDetail
            span={span}
            runId={doc.id}
            runStatus={runStatus}
            timed={timed}
          />
        ) : span.kind === "run" || span.kind === "subagent" ? (
          <RunDetail
            run={span.node as FoldedRun}
            span={span}
            doc={doc}
            runStatus={runStatus}
            onJump={onJump}
            childLinks={childLinks}
          />
        ) : span.kind === "step" ? (
          <StepDetail
            step={span.node as FoldedStep}
            runId={
              span.key === `s${(span.node as FoldedStep).index}`
                ? doc.id
                : undefined
            }
            runStatus={runStatus}
            onJump={onJump}
            childLinks={childLinks}
            requests={
              span.key === `s${(span.node as FoldedStep).index}`
                ? requests
                : undefined
            }
          />
        ) : (
          <CallDetail
            call={span.node as FoldedToolCall}
            runStatus={runStatus}
            onJump={onJump}
            childLinks={childLinks}
          />
        )}
        {span && mode === "detail" && span.kind === "step" ? (
          <div className="mt-3 border-t pt-2">
            {(span.node as FoldedStep).finish ? (
              <span className="font-mono text-xs text-muted-foreground">
                finished: {(span.node as FoldedStep).finish?.reason} ·{" "}
                <Usage usage={(span.node as FoldedStep).finish!.usage} />
              </span>
            ) : null}
          </div>
        ) : null}
      </div>
    </div>
  )
}

/** A time-axis row's facts: the timed span itself — name, service,
 * times, status, and its attributes as a tree (S4.7's time axis). */
function TimedDetail({
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
          ["service", sp.service || "—"],
          ["status", sp.status_message ? `${sp.status} · ${sp.status_message}` : sp.status],
          [
            "time",
            t.toMs == null
              ? `${spanMs(t.fromMs)}– (open)`
              : `${spanMs(t.fromMs)}–${spanMs(t.toMs)} (${spanMs(t.toMs - t.fromMs)})`,
          ],
          ...(chat ?? []),
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
      <div className="codewin">
        <div className="codewin-bar">
          <span className="font-mono text-[11px] text-code-mut">attrs</span>
        </div>
        <div className="overflow-auto px-3 py-3">
          <JsonTree value={sp.attrs} openDepth={2} />
        </div>
      </div>
      {sp.events.length > 0 ? (
        <div className="codewin">
          <div className="codewin-bar">
            <span className="font-mono text-[11px] text-code-mut">
              span events
            </span>
          </div>
          <div className="overflow-auto px-3 py-3">
            <JsonTree value={sp.events} openDepth={1} />
          </div>
        </div>
      ) : null}
    </div>
  )
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
function chatFacts(sp: TimedSpan, timed: TimedSpan[]): [string, React.ReactNode][] {
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
