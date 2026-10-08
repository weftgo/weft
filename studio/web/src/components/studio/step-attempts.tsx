// The step's attempts and timing (plan A4.2, ADR 0016's A4 note, ADR
// 0028 §7): the header line — "attempt 4 of 4 · fallback to glm-b",
// "1.2 s · first token 180 ms" — from what the page already holds (the
// folded step_finish's timing, the request record's rows, the step
// route when cached), and the attempt list, one row per attempt with
// its model, outcome, retry_after and times, read from GET
// runs/{id}/steps/{n} only when opened and only under the `steps`
// capability. Every hole is a badge: a run older than attempt
// reporting shows not_recorded with its reason and fix, never an empty
// pane.
import { useQuery } from "@tanstack/react-query"
import { ChevronRight } from "lucide-react"
import { useState } from "react"

import { stepQuery } from "@/lib/api"
import type { StepAttempt, StepDoc } from "@/lib/api"
import {
  ATTEMPTS_NOT_STORED,
  attemptLine,
  factsFromRows,
  factsFromStep,
  attemptsHole,
  relMs,
  msText,
  timingLine,
} from "@/lib/attempts"
import type { FoldedStep } from "@/lib/events"
import type { HoleMark } from "@/lib/honesty"
import { HoleBadge } from "@/components/studio/hole-badge"
import type { RunRequests } from "@/components/studio/step-request"
import { useCapabilities } from "@/hooks/use-capabilities"

/**
 * StepHeadline is the step header's attempt and timing words: the step
 * route's facts when the page has it cached, else the request rows'
 * attempt count and the fold's step_finish timing — so a collapsed
 * card fetches nothing.
 */
export function StepHeadline({
  step,
  runId,
  runStatus,
  requests,
}: {
  step: FoldedStep
  runId: string
  runStatus: string
  requests?: RunRequests
}) {
  const doc = useQuery({ ...stepQuery(runId, step.index), enabled: false })
  const d = doc.data
  // Tools run only after a successful model call: a step whose tools
  // started has answered, finished or not (a run that died mid-tools).
  const answered = stepAnswered(step)
  const fromDoc = d ? factsFromStep(d) : null
  const line = attemptLine(
    (fromDoc && !(fromDoc.n === 0 && answered) ? fromDoc : null) ??
      factsFromRows(
        requests?.steps.get(step.index)?.rows,
        answered,
        runStatus === "running"
      )
  )
  const timing = timingLine(
    d?.latency_ms ?? step.finish?.latencyMs,
    d?.ttft_ms ?? step.finish?.ttftMs
  )
  if (!line && !timing) return null
  return (
    <>
      {line ? (
        <span
          data-attempt-line
          className="rounded-sm border border-thread/40 px-1.5 py-px font-mono text-[10px] text-thread"
          title="the attempt that answered, of the attempts the model chain made for this step"
        >
          {line}
        </span>
      ) : null}
      {timing ? (
        <span
          data-timing
          className="font-mono text-[11px] text-muted-foreground tabular-nums"
          title="the step's model call: latency (the whole call, retries and fallbacks inside it) and time to the first text or tool-args delta"
        >
          {timing}
        </span>
      ) : null}
    </>
  )
}

/** stepAnswered says the step's model call answered: it finished, or
 * its tools started (they run only after a successful call). */
export function stepAnswered(step: Pick<FoldedStep, "finish" | "toolCalls">): boolean {
  return !!step.finish || step.toolCalls.length > 0
}

/**
 * stepAttemptsHole is the card's not_recorded hole for a step written
 * before attempt reporting (attemptsHole) — the panel's rule, computed
 * on the card whatever the capabilities: no requests capability reads
 * as zero rows; a record still loading or unreadable says nothing.
 */
export function stepAttemptsHole(
  step: Pick<FoldedStep, "index" | "finish">,
  requests?: RunRequests
): HoleMark[] {
  if (requests && (requests.loading || requests.error)) return []
  const h = attemptsHole(
    step.finish,
    requests?.steps.get(step.index)?.rows.length ?? 0
  )
  return h ? [h] : []
}

function outcomeOf(a: StepAttempt) {
  if (a.outcome === "ok")
    return <span className="text-ev-result">ok</span>
  if (a.outcome === "error")
    return <span className="text-ev-error">{a.error_type || "error"}</span>
  return <span className="text-faint">outcome not reported</span>
}

function AttemptRow({ a, from }: { a: StepAttempt; from?: string }) {
  const st = relMs(a.started, from)
  const en = relMs(a.finished, from)
  return (
    <li
      data-attempt={a.attempt}
      className="flex flex-wrap items-center gap-x-3 gap-y-0.5 font-mono text-[11px]"
    >
      <span className="w-16 text-faint">
        {a.attempt === 0 && typeof a.request_index === "number"
          ? `request #${a.request_index}`
          : `attempt ${a.attempt}`}
      </span>
      <span data-attempt-model className="text-muted-foreground">
        {a.model}
      </span>
      <span data-attempt-outcome>{outcomeOf(a)}</span>
      {a.retry_after_ms ? (
        <span className="text-faint">retry after {msText(a.retry_after_ms)}</span>
      ) : null}
      {st && en ? (
        <span className="text-faint tabular-nums">
          {st} – {en}
        </span>
      ) : null}
      {a.badge === "derived" ? (
        <HoleBadge
          hole="derived"
          reason={
            a.attempt === 0
              ? "this request record's body did not parse: listed by its request index"
              : "this attempt's request record carried no attempt number (its body did not parse): joined to the first attempt the spans time"
          }
        />
      ) : null}
    </li>
  )
}

/** The opened pane over the step route: the badge, the rows, or the
 * words that say why there are none. */
function AttemptsPane({ doc, running }: { doc: StepDoc; running: boolean }) {
  const rows: StepAttempt[] = Array.isArray(doc.attempts) ? doc.attempts : []
  // A step still running has no attempt spans yet: its badge
  // (not_recorded, "no spans yet") is about the moment, not the step —
  // the doc is refetched until it is over (stepQuery).
  const badge = doc.status === "running" ? undefined : doc.attempts_badge
  return (
    <div className="space-y-1.5 pt-2" data-attempts-pane>
      {badge ? (
        <HoleBadge
          hole={badge.badge ?? "gap"}
          reason={badge.reason}
          fix={badge.fix}
          detail
        />
      ) : null}
      {rows.length ? (
        <ul className="space-y-0.5">
          {rows.map((a, i) => (
            <AttemptRow key={`${a.attempt}-${i}`} a={a} from={doc.started} />
          ))}
        </ul>
      ) : badge ? null : running ? (
        <span className="font-mono text-[11px] text-faint">
          {ATTEMPTS_NOT_STORED}
        </span>
      ) : (
        <HoleBadge
          hole="gap"
          reason="this step ran, but no attempt of it was stored"
          detail
        />
      )}
    </div>
  )
}

/**
 * AttemptsSection is the step's attempt list, collapsed to a line that
 * opens to one row per attempt. It fetches the step route when opened,
 * and is not drawn without the `steps` capability.
 */
export function AttemptsSection({
  step,
  runId,
  runStatus,
  requests,
  defaultOpen = false,
}: {
  step: { index: number }
  runId: string
  runStatus: string
  requests?: RunRequests
  defaultOpen?: boolean
}) {
  const { has } = useCapabilities()
  const capable = has("steps")
  const [open, setOpen] = useState(defaultOpen)
  const q = useQuery({
    ...stepQuery(runId, step.index),
    enabled: capable && open && runId !== "",
  })
  if (!capable) return null
  const running = runStatus === "running"
  // Collapsed, the request rows say how many attempts when there was
  // more than one, as RequestSection does (a step older than attempt
  // reporting is badged on the card: stepAttemptsHole).
  const rows = requests?.steps.get(step.index)?.rows ?? []
  let body: React.ReactNode = null
  if (open) {
    body = q.data ? (
      <AttemptsPane doc={q.data} running={running} />
    ) : q.isError ? (
      <p className="pt-2 font-mono text-[11px] text-status-bad">
        the attempts could not be read: {q.error.message}
      </p>
    ) : (
      <p className="pt-2 font-mono text-[11px] text-faint">
        loading the attempts…
      </p>
    )
  }
  return (
    <div
      className="rounded-md border border-dashed px-3 py-1.5"
      data-attempts={step.index}
    >
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
          aria-expanded={open}
          onClick={() => setOpen((x) => !x)}
        >
          <ChevronRight
            className={`size-3 transition-transform ${open ? "rotate-90" : ""}`}
            data-slot="icon"
          />
          attempts
        </button>
        {!open && rows.length > 1 ? (
          <span className="font-mono text-[11px] text-faint">
            {rows.length} attempts
          </span>
        ) : null}
      </div>
      {body}
    </div>
  )
}
