// The step story (B1) with subagents inline (B7) and truncation
// badged (B9): per step, the model text (whitespace preserved, no
// markdown), reasoning collapsed by default, tool calls as one-line
// rows — name, an args summary, a status pill — that open to the args
// and result windows, then finish reason and usage with the
// cached/reasoning splits. Every step and call can send the replay
// playhead to the moment it happened (the fold carries positions).
import { useQuery } from "@tanstack/react-query"
import { ChevronRight, Play } from "lucide-react"
import { useEffect, useRef, useState } from "react"

import { stepQuery } from "@/lib/api"
import type { RunCompaction, RunDoc, Transcript, WireEvent } from "@/lib/api"
import { compactionsOf, isSessionMarker } from "@/lib/compaction"
import {
  callState,
  fold,
  linkView,
  stepHoles,
  truncation,
} from "@/lib/events"
import type {
  FoldedRun,
  FoldedStep,
  FoldedToolCall,
  PlacedBatch,
} from "@/lib/events"
import type { HoleMark } from "@/lib/honesty"
import { tokens } from "@/lib/format"
import { bytes } from "@/lib/summarize"
import { CodeWin } from "@/components/studio/codewin"
import { CompactionMarker } from "@/components/studio/compaction-marker"
import { HoleBadge, HoleBadges } from "@/components/studio/hole-badge"
import {
  AttemptsSection,
  StepHeadline,
} from "@/components/studio/step-attempts"
import { RequestSection } from "@/components/studio/step-request"
import type { RunRequests } from "@/components/studio/step-request"
import { childOfStep, SubagentBlock } from "@/components/studio/subagent-block"
import type { ChildRow } from "@/components/studio/subagent-block"
import { TruncationBadge } from "@/components/studio/truncation-badge"
import { Button } from "@/components/ui/button"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"

/** A ToolError renders "CODE: message" — the code is a chip (5.2a). */
const CODED = /^([A-Z][A-Z0-9_]+): /

export function Usage({
  usage,
}: {
  usage: {
    input_tokens: number
    output_tokens: number
    cached_input_tokens?: number
    cache_write_tokens?: number
    reasoning_tokens?: number
  }
}) {
  const splits: string[] = []
  if (usage.cached_input_tokens)
    splits.push(`cached ${usage.cached_input_tokens.toLocaleString()}`)
  if (usage.cache_write_tokens)
    splits.push(`cache write ${usage.cache_write_tokens.toLocaleString()}`)
  if (usage.reasoning_tokens)
    splits.push(`reasoning ${usage.reasoning_tokens.toLocaleString()}`)
  return (
    <span
      className="font-mono text-muted-foreground tabular-nums"
      title={`${usage.input_tokens.toLocaleString()} input tokens, ${usage.output_tokens.toLocaleString()} output tokens${
        splits.length ? ` (${splits.join(", ")})` : ""
      }`}
    >
      {tokens(usage.input_tokens)} in / {tokens(usage.output_tokens)} out
      {splits.length ? (
        <span className="text-faint"> · {splits.join(" · ")}</span>
      ) : null}
    </span>
  )
}

/** One-line args: `{"order_id":"42"}`, clipped. */
function argsSummary(call: FoldedToolCall): string {
  const src =
    call.args !== undefined ? JSON.stringify(call.args) : call.streamedArgs
  if (!src) return ""
  return src.length > 80 ? `${src.slice(0, 79)}…` : src
}

function JumpButton({
  pos,
  onJump,
  label,
}: {
  pos: number
  onJump?: (t: number) => void
  label: string
}) {
  if (!onJump) return null
  return (
    <Button
      variant="ghost"
      size="icon-xs"
      className="text-faint opacity-0 transition-opacity group-hover/row:opacity-100 focus-visible:opacity-100"
      aria-label={label}
      title={label}
      onClick={(e) => {
        e.stopPropagation()
        onJump(pos + 1)
      }}
    >
      <Play data-slot="icon" />
    </Button>
  )
}

/** The status pill after a call's name: what happened to it. */
function CallPill({
  call,
  state,
}: {
  call: FoldedToolCall
  state: "running" | "done" | "never"
}) {
  if (state === "running")
    return (
      <span className="font-mono text-[11px] text-status-run">running…</span>
    )
  if (state === "never")
    return (
      <span
        className="rounded-sm border border-ev-error/40 px-1.5 py-px font-mono text-[10px] tracking-wide text-ev-error uppercase"
        title="tool_start was recorded but no tool_finish — the run ended first"
      >
        never completed
      </span>
    )
  const r = call.result
  if (!r) return null
  const coded = r.isError ? CODED.exec(r.content) : null
  const cut = truncation(r.content)
  return (
    <span className="flex items-center gap-1.5">
      {r.isError ? (
        <span
          className="rounded-sm border border-ev-error/40 px-1.5 py-px font-mono text-[10px] tracking-wide text-ev-error uppercase"
          title="error as data: the model saw this result and could recover"
        >
          {coded ? coded[1] : "tool error"}
        </span>
      ) : (
        <span className="font-mono text-[11px] text-ev-result">ok</span>
      )}
      <span className="font-mono text-[11px] text-faint tabular-nums">
        {bytes(new TextEncoder().encode(r.content).length)}
      </span>
      {cut ? <TruncationBadge content={r.content} /> : null}
      {call.holes?.length ? <HoleBadges holes={call.holes} /> : null}
    </span>
  )
}

export function ToolCallRow({
  call,
  runStatus,
  child,
  onJump,
  compact,
}: {
  call: FoldedToolCall
  runStatus: string
  child?: ChildRow
  onJump?: (t: number) => void
  compact?: boolean
}) {
  const state = callState(call, runStatus)
  const [open, setOpen] = useState(true)
  const summary = argsSummary(call)
  return (
    <div
      className={`group/row border-l-2 pl-3 ${
        call.result?.isError
          ? "border-ev-error/60"
          : state === "never"
            ? "border-ev-error/40"
            : "border-ev-tool/50"
      }`}
      data-call={call.callId}
    >
      <div
        className="-ml-3 flex cursor-pointer flex-wrap items-center gap-x-2 gap-y-1 rounded-r-md py-0.5 pl-3 hover:bg-secondary/60"
        onClick={() => setOpen((o) => !o)}
        role="button"
        aria-expanded={open}
        tabIndex={0}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault()
            setOpen((o) => !o)
          }
        }}
      >
        <ChevronRight
          className={`size-3 shrink-0 text-faint transition-transform ${open ? "rotate-90" : ""}`}
          data-slot="icon"
        />
        <span className="font-mono text-[13px] text-ev-tool">{call.name}</span>
        {summary ? (
          <span
            className="max-w-[40ch] truncate font-mono text-[11px] text-muted-foreground"
            title={
              call.args !== undefined ? JSON.stringify(call.args) : undefined
            }
          >
            {summary}
          </span>
        ) : null}
        <CallPill call={call} state={state} />
        <span className="ml-auto flex items-center gap-1">
          <span
            className="font-mono text-[10px] text-faint"
            title="the call id (call_id on the wire)"
          >
            {call.callId}
          </span>
          <JumpButton
            pos={call.startPos}
            onJump={onJump}
            label={`replay to this call (event #${call.startPos})`}
          />
        </span>
      </div>
      {open ? (
        <div className={`space-y-1.5 pt-1 ${compact ? "" : "max-w-3xl"}`}>
          {call.args !== undefined ? (
            <CodeWin
              title="args"
              text={JSON.stringify(call.args, null, 2)}
              json
            />
          ) : call.streamedArgs ? (
            <div className="font-mono text-xs text-muted-foreground">
              writing args…{" "}
              <span className="text-faint">{call.streamedArgs}</span>
            </div>
          ) : null}
          {child ? (
            <SubagentBlock child={child} onJump={onJump} />
          ) : null}
          {call.result ? (
            <CodeWin
              title={call.result.isError ? "result · error as data" : "result"}
              text={call.result.content}
              tone={call.result.isError ? "error" : "result"}
            />
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

/**
 * StepBody is the inside of a step card, shared with the subagent
 * block so a child's steps get the same treatment as the parent's.
 */
export function StepBody({
  step,
  runStatus,
  childLinks,
  stepChildren,
  onJump,
  compact,
}: {
  step: FoldedStep
  runStatus: string
  /** The run's subagent children by child run id, looked up by the
   * call's childRunId (linkView stamped it: call ids may repeat across
   * steps, child ids carry the step) — the block fetches its events on
   * expand. */
  childLinks: Map<string, ChildRow>
  /** The step route's children[] (A7) by call id — one step's, so its
   * call ids are unique — when cached: a child the run document does
   * not list yet (a live run's) still gets its row. */
  stepChildren?: Map<string, ChildRow>
  onJump?: (t: number) => void
  compact?: boolean
}) {
  return (
    <>
      {step.reasoning ? (
        <Collapsible defaultOpen={false}>
          <CollapsibleTrigger className="group/reason flex items-center gap-1 text-xs text-ev-reasoning hover:text-foreground">
            <ChevronRight
              className="size-3 transition-transform group-aria-expanded/reason:rotate-90 group-data-[panel-open]/reason:rotate-90"
              data-slot="icon"
            />
            reasoning · {step.reasoning.length.toLocaleString()} chars
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div className="mt-1 border-l-2 border-ev-reasoning/40 pl-3 text-xs whitespace-pre-wrap text-ev-reasoning">
              {step.reasoning}
            </div>
          </CollapsibleContent>
        </Collapsible>
      ) : null}

      {step.toolCalls.length > 0 && (
        <div className="space-y-2">
          {step.toolCalls.map((call) => (
            <ToolCallRow
              key={call.callId}
              call={call}
              runStatus={runStatus}
              child={
                (call.childRunId
                  ? childLinks.get(call.childRunId)
                  : undefined) ?? stepChildren?.get(call.callId)
              }
              onJump={onJump}
              compact={compact}
            />
          ))}
        </div>
      )}

      {step.text ? (
        <div
          className={`${compact ? "text-xs" : "text-sm"} leading-relaxed whitespace-pre-wrap`}
        >
          {step.text}
        </div>
      ) : null}
    </>
  )
}

/** The user turn steering delivered after a step (the Steered event,
 * ADR 0019): the words the user typed mid-run, between the step they
 * interrupted-followed and the one that answers them. */
function SteerBlock({
  steer,
  onJump,
}: {
  steer: { text: string; pos: number }
  onJump?: (t: number) => void
}) {
  return (
    <div
      className="group/row -mt-1 flex gap-2 rounded-lg border border-thread/40 bg-thread/5 px-4 py-2"
      data-steer
    >
      <div className="min-w-0 flex-1">
        <span className="eyebrow text-thread/80">steered · user</span>
        <p className="mt-0.5 text-sm leading-relaxed whitespace-pre-wrap">
          {steer.text}
        </p>
      </div>
      <span className="flex items-start">
        <JumpButton
          pos={steer.pos}
          onJump={onJump}
          label={`replay to this steer (event #${steer.pos})`}
        />
      </span>
    </div>
  )
}

/** The step's end state as a phrase: honest when it never finished. */
export function stepOutcome(step: FoldedStep, runStatus: string) {
  if (step.finish) {
    return (
      <span className="flex items-center gap-2 text-xs">
        <span
          className="font-mono text-muted-foreground"
          title="the step's stop reason"
        >
          {step.finish.reason}
        </span>
        <Usage usage={step.finish.usage} />
      </span>
    )
  }
  if (runStatus === "running")
    return <span className="font-mono text-xs text-status-run">in flight…</span>
  return (
    <span
      className={`font-mono text-xs ${runStatus === "failed" ? "text-status-bad" : "text-ev-error"}`}
      title="no step_finish was recorded: the run ended during this step"
    >
      {runStatus === "failed"
        ? "failed during this step"
        : runStatus === "interrupted"
          ? "interrupted during this step"
          : "no step_finish recorded"}
    </span>
  )
}

function StepCard({
  step,
  runId,
  runHoles,
  runStatus,
  childLinks,
  highlighted,
  onJump,
  requests,
  compactions,
  transcript,
}: {
  step: FoldedStep
  runId: string
  /** The run document's holes: those that hold for every step badge
   * each card (stepHoles). */
  runHoles?: HoleMark[]
  /** The run's compactions (plan A9.2): this step's run-scope views
   * draw their marker inside the card. */
  compactions: RunCompaction[]
  transcript?: Transcript | null
  runStatus: string
  childLinks: Map<string, ChildRow>
  highlighted?: boolean
  onJump?: (t: number) => void
  requests?: RunRequests
}) {
  const ref = useRef<HTMLDivElement>(null)
  // The step route's assembled holes when the page has it (A7): read
  // from the cache only — the card never fetches it.
  const stepDoc = useQuery({ ...stepQuery(runId, step.index), enabled: false })
  const holes = stepHoles(step, runHoles, stepDoc.data)
  // The children the step route names (A10), joined by call id: the
  // run document's rows win where both have one (they carry more).
  const stepChildren = new Map(
    (stepDoc.data?.children ?? []).map((c) => [c.call_id, childOfStep(c)])
  )
  // A ?step= link (A3) lands on the card it names.
  useEffect(() => {
    if (highlighted) ref.current?.scrollIntoView({ block: "center" })
  }, [highlighted])
  const failedHere = !step.finish && runStatus === "failed"
  return (
    <div
      ref={ref}
      data-step={step.index}
      className={`group/row space-y-2 rounded-lg border bg-background px-4 py-3 ${
        highlighted ? "ring-2 ring-thread/60" : ""
      } ${failedHere ? "border-status-bad/40" : ""}`}
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="eyebrow">step {step.index}</span>
        {stepOutcome(step, runStatus)}
        <StepHeadline step={step} runId={runId} requests={requests} />
        <HoleBadges holes={holes} />
        <span className="ml-auto flex items-center gap-1">
          <span
            className="font-mono text-[10px] text-faint tabular-nums"
            title="this step's events, by stream position"
          >
            events {step.from}–{step.to}
          </span>
          <JumpButton
            pos={step.from}
            onJump={onJump}
            label={`replay from this step (event #${step.from})`}
          />
        </span>
      </div>
      {compactions
        .filter((c) => !isSessionMarker(c) && c.step === step.index)
        .map((c) => (
          <CompactionMarker
            key={c.hash || c.index}
            c={c}
            all={compactions}
            runId={runId}
            transcript={transcript}
          />
        ))}
      {requests ? <RequestSection req={requests} step={step.index} /> : null}
      <AttemptsSection
        step={step}
        runId={runId}
        runStatus={runStatus}
        requests={requests}
      />
      <StepBody
        step={step}
        runStatus={runStatus}
        childLinks={childLinks}
        stepChildren={stepChildren}
        onJump={onJump}
      />
    </div>
  )
}

/** The words of transcript batches whose step the record holds no
 * events for (applyTranscript's view.unplaced): kept, under the last
 * step, with the hole that says why — a gap: the step's events were
 * lost — never dropped. */
function Unplaced({ batches }: { batches: PlacedBatch[] }) {
  const steps = [...new Set(batches.map((b) => b.step))].sort((a, b) => a - b)
  const words = batches
    .flatMap((b) => b.messages)
    .filter((m) => m.role === "assistant")
    .map((m) =>
      m.content
        .map((p) => (p.type === "text" ? p.text : ""))
        .join("")
    )
    .filter(Boolean)
  return (
    <div
      className="space-y-1.5 rounded-lg border border-dashed px-4 py-3"
      data-unplaced
    >
      <HoleBadge
        hole="gap"
        reason={`the transcript has words for step ${steps.join(", ")}, but no event of ${steps.length === 1 ? "that step" : "those steps"} is in the record: a destination dropped them`}
        detail
      />
      {words.map((w, i) => (
        <div key={i} className="text-sm leading-relaxed whitespace-pre-wrap">
          {w}
        </div>
      ))}
    </div>
  )
}

export function StepList({
  events,
  folded,
  atPlayhead,
  doc,
  upTo,
  highlight,
  onJump,
  requests,
  transcript,
}: {
  events: WireEvent[]
  folded: FoldedRun
  /** The page's fold at the playhead (the transcript overlaid, the
   * recorder's badges kept): used while replaying instead of folding
   * the bare prefix here. */
  atPlayhead?: FoldedRun
  doc: RunDoc
  /** Replay playhead: render only events up to this position — the
   * same fold over a prefix, never a second shape (B2). */
  upTo?: number
  /** The ?step= selection (A3): the named step is ringed and centred. */
  highlight?: number
  /** Send the replay playhead to a position (a step or call's start). */
  onJump?: (t: number) => void
  /** The run's request record (ADR 0028 §10): each step shows what it
   * called the model with. Absent — the server lacks the requests
   * capability — the section is not drawn. */
  requests?: RunRequests
  /** The transcript route's growth records, when loaded: "show
   * original" reads a view's replaced range from them (plan A9.2). */
  transcript?: Transcript | null
}) {
  // The compactions the run document names (A9.2). A run from before
  // A9 has none in its document and draws no marker: the record is
  // optional, so its absence is not a hole ("not recorded" would be a
  // false claim about a run that may simply never have compacted).
  const compactions = compactionsOf(doc)
  const sessionMarkers = compactions.filter(isSessionMarker)
  const replaying = upTo != null && upTo < events.length
  const view = linkView(
    upTo == null ? folded : (atPlayhead ?? fold(events, upTo)),
    doc.children
  )
  // A child row per child id; linkView stamped each call with its
  // child's id (S4.3). The block expands lazily.
  const childLinks = new Map<string, ChildRow>(
    doc.children.map((c) => [c.id, c])
  )
  // While scrubbing, the run reads as running: calls past the
  // playhead are "running", not "never completed".
  const runStatus = replaying ? "running" : doc.status
  return (
    <div className="space-y-3">
      {sessionMarkers.map((c, i) => (
        <CompactionMarker
          key={c.hash || `session-${i}`}
          c={c}
          all={compactions}
          runId={doc.id}
          transcript={transcript}
        />
      ))}
      {view.steps.map((step) => (
        <div key={step.index} className="space-y-3">
          <StepCard
            step={step}
            runId={doc.id}
            runHoles={doc.holes}
            runStatus={runStatus}
            childLinks={childLinks}
            highlighted={step.index === highlight}
            onJump={onJump}
            requests={requests}
            compactions={compactions}
            transcript={transcript}
          />
          {step.steer ? <SteerBlock steer={step.steer} onJump={onJump} /> : null}
        </div>
      ))}
      {view.unplaced?.length ? <Unplaced batches={view.unplaced} /> : null}
      {view.steps.length === 0 ? (
        <p className="py-6 text-center font-mono text-xs text-faint">
          {replaying || upTo === 0
            ? "nothing revealed yet — play or step forward"
            : events.length === 0
              ? doc.status === "running"
                ? "waiting for the first event…"
                : "no events were recorded for this run"
              : "no steps yet"}
        </p>
      ) : null}
      {view.finished ? null : (
        <p className="text-center font-mono text-xs text-faint">
          {replaying
            ? `replay · ${upTo} of ${events.length} events shown`
            : doc.status === "running"
              ? "stream in progress…"
              : events.length > 0
                ? "the stream ended without run_finish"
                : null}
        </p>
      )}
      {view.pending.length > 0 && (
        <div className="rounded-lg border border-ev-error/30 px-4 py-2 text-xs">
          <span className="eyebrow">pending approval</span>
          <ul className="mt-1 space-y-0.5">
            {view.pending.map((p) => (
              <li key={p.id} className="font-mono">
                {p.name}({p.args == null ? "" : JSON.stringify(p.args)}) — awaiting a decision
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
