// Event summaries: one line per wire event for the replay gutter,
// the raw explorer and the current-event readout. Pure functions.
import type { Message, WireEvent } from "./api"
import { msText } from "./attempts"
import { endCard, stopDetail, stoppedByWords, stopStep } from "./stopped-by"

export type EventKind =
  | "step" // run/step boundaries
  | "tool" // tool_start
  | "result" // tool_finish ok
  | "error" // tool_finish is_error
  | "reasoning"
  | "delta" // text/args deltas

/** The stream is stored as ingested: a body can be anything JSON
 * holds. typeOf is the event's discriminator, or "" when there is none
 * (a null, a bare string, an object without a type). */
function typeOf(ev: WireEvent): string {
  const t = (ev as { type?: unknown } | null)?.type
  return typeof t === "string" ? t : ""
}

export function eventKind(ev: WireEvent): EventKind {
  switch (typeOf(ev)) {
    case "":
      return "delta" // not an event at all: the quietest row

    case "tool_start":
      return "tool"
    case "tool_finish":
      return (ev as { is_error?: unknown }).is_error ? "error" : "result"
    case "reasoning_delta":
      return "reasoning"
    case "text_delta":
    case "tool_args_delta":
      return "delta"
    default:
      return "step"
  }
}

/** Deltas stream; everything else is a boundary the eye can land on. */
export function isBoundary(ev: WireEvent): boolean {
  const k = eventKind(ev)
  return k !== "delta" && k !== "reasoning"
}

const kindText: Record<EventKind, string> = {
  step: "text-ev-step",
  tool: "text-ev-tool",
  result: "text-ev-result",
  error: "text-ev-error",
  reasoning: "text-ev-reasoning",
  delta: "text-faint",
}
const kindBg: Record<EventKind, string> = {
  step: "bg-ev-step",
  tool: "bg-ev-tool",
  result: "bg-ev-result",
  error: "bg-ev-error",
  reasoning: "bg-ev-reasoning",
  delta: "bg-faint",
}
const kindBorder: Record<EventKind, string> = {
  step: "border-ev-step/40",
  tool: "border-ev-tool/50",
  result: "border-ev-result/50",
  error: "border-ev-error/50",
  reasoning: "border-ev-reasoning/40",
  delta: "border-faint/40",
}

export function kindClass(
  kind: EventKind,
  slot: "text" | "bg" | "border" = "text"
): string {
  return slot === "text"
    ? kindText[kind]
    : slot === "bg"
      ? kindBg[kind]
      : kindBorder[kind]
}

/** The bar's stacking order: what a bucket of mixed events shows. */
const kindPriority: Record<EventKind, number> = {
  error: 6,
  tool: 5,
  result: 4,
  step: 3,
  reasoning: 1,
  delta: 0,
}

/** The kind that wins when several events share one gutter bucket. */
export function dominantKind(kinds: EventKind[]): EventKind {
  let best: EventKind = "delta"
  for (const k of kinds) if (kindPriority[k] > kindPriority[best]) best = k
  return best
}

function clip(s: unknown, n: number): string {
  if (typeof s !== "string") return ""
  const one = s.replace(/\s+/g, " ").trim()
  return one.length > n ? `${one.slice(0, n - 1)}…` : one
}

function short(v: unknown, n: number): string {
  if (v === undefined) return ""
  try {
    return clip(JSON.stringify(v), n)
  } catch {
    return ""
  }
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

/** The event's type name as the wire spells it. */
export function eventType(ev: WireEvent): string {
  return typeOf(ev) || "(not an event)"
}

/** A steered delivery's words: its messages' text parts, one line
 * each, whitespace folded — the summary of a user turn (ADR 0019). */
function steerText(messages: Message[] | null | undefined): string {
  return (Array.isArray(messages) ? messages : [])
    .map((m) =>
      // A message (or its content) can be null on a malformed body.
      (Array.isArray((m as Message | null)?.content) ? m.content : [])
        .filter(
          (p): p is Extract<(typeof m.content)[number], { type: "text" }> =>
            (p as { type?: unknown } | null)?.type === "text"
        )
        .map((p) => p.text)
        .join("")
    )
    .join(" ⏎ ")
    .replace(/\s+/g, " ")
    .trim()
}

/** "12 in / 3 out", zeros when the usage is missing. */
function inOut(
  u: { input_tokens?: number; output_tokens?: number } | undefined
): string {
  return `${u?.input_tokens ?? 0} in / ${u?.output_tokens ?? 0} out`
}

/** A one-line, human summary: what happened, in the machine's voice.
 * Total over anything the stream can hold: a known type with fields
 * missing reads short, an unknown type (a newer core's event) shows
 * its JSON, a body that is no event at all shows what it is. */
export function eventSummary(ev: WireEvent, width = 96): string {
  if (!typeOf(ev)) return short(ev, width)
  switch (ev.type) {
    case "run_start": {
      const m = ev.model as typeof ev.model | undefined
      return `${ev.agent ?? "agent"} · ${m?.provider ?? "?"}/${m?.name ?? "?"} · ${ev.id}`
    }
    case "step_start":
      return `step ${ev.index}`
    case "text_delta":
      return `“${clip(ev.text, width)}”`
    case "reasoning_delta":
      return `“${clip(ev.text, width)}”`
    case "tool_args_delta":
      return `${ev.name} ${clip(ev.args, width)}`
    case "tool_start":
      return `${ev.name}(${short(ev.args, width)})`
    case "tool_finish": {
      const content = typeof ev.content === "string" ? ev.content : ""
      return `${ev.name} → ${ev.is_error ? "error" : "ok"} · ${bytes(
        new TextEncoder().encode(content).length
      )} · ${clip(content, width)}`
    }
    case "step_finish":
      return `step ${ev.index} · ${ev.reason} · ${inOut(ev.usage)}`
    case "steered":
      return `steered · after step ${ev.step} · ${clip(
        steerText(ev.messages),
        width
      )}`
    case "run_finish":
      return `${ev.steps} steps · ${inOut(ev.usage)}${
        Array.isArray(ev.pending) && ev.pending.length
          ? ` · ${ev.pending.length} pending`
          : ""
      }`
    default:
      return short(ev, width)
  }
}

// ── The step drawer's answer (U10.1, audit §6.3) ───────────────────
// What one step did, answer first: a verdict line, what was sent,
// what the model replied, what the tools did, and what came next.
// Plain data in, words out — the run page's drawer and (U10.2) the
// panel's Story step read these, so both say the same thing.

/** The run facts a step's verdict reads (a run row or document). */
export interface AnswerRun {
  status?: string
  err?: string
  stopped_by?: string
  cancel_cause?: string
  steps?: number
  error_type?: string
}

/** The step facts the answer reads (a folded step). */
export interface AnswerStep {
  index: number
  text?: string
  toolCalls: {
    name: string
    args?: unknown
    result?: { isError: boolean; content: string }
    finishPos?: number
  }[]
  finish?: {
    reason: string
    usage?: { input_tokens?: number; output_tokens?: number }
    latencyMs?: number
    ttftMs?: number
  }
}

/** failedAtStep is the step a failed run failed at: the one core's
 * RunError names ("run failed at step N"), else its last step;
 * undefined for a run that did not fail or ran no step. Core appends a
 * step's record only after its model call and its tools, so a model
 * error or a cancel during step N reads "at step N" with steps N: step
 * N is the open one (its errored chat span, the fold's open step). A
 * max_steps run's error names the step it would have run next ("at step
 * 3" after steps 0–2): its stop is its last step (stopStep). A cancel
 * before step N began names a step no surface draws: landStep falls
 * back to the last one drawn. */
export function failedAtStep(r: AnswerRun): number | undefined {
  if (r.status !== "failed") return undefined
  const at = /run failed at step (\d+)/.exec(r.err ?? "")?.[1]
  return at === undefined || r.stopped_by === "max_steps"
    ? stopStep(r.steps)
    : Number(at)
}

/** landStep is where a selection lands for the step a run failed at
 * among the steps a surface draws: that step, else the last drawn
 * before it (a cancel before step N began: N − 1); undefined with no
 * failure or none drawn before it (the caller's step 0). */
export function landStep(
  failedAt: number | undefined,
  drawn: readonly number[]
): number | undefined {
  let best: number | undefined
  if (failedAt !== undefined)
    for (const n of drawn)
      if (n <= failedAt && (best === undefined || n > best)) best = n
  return best
}

/** The stops a loop guard makes at the continuation point (AGENTS.md
 * rule 13): the step finished and the guard refused the next model
 * call. A cancel or a model error is no refusal. */
const GUARD_STOPS: ReadonlySet<string> = new Set([
  "usage_limit",
  "loop_detected",
  "model_retries_exceeded",
  "max_steps",
])

/** The answer's line labels, in their order (§6.3). */
export const ANSWER_LABELS = {
  verdict: "verdict",
  sent: "sent",
  replied: "replied",
  tools: "tools ran",
  then: "then",
} as const

const count = (n: number, one: string) =>
  `${n.toLocaleString("en-US")} ${n === 1 ? one : `${one}s`}`

/** tokensWord is "30 in · 10 out" (zeros when a side is missing). */
export function tokensWord(u?: {
  input_tokens?: number
  output_tokens?: number
}): string {
  const n = (v?: number) => (v ?? 0).toLocaleString("en-US")
  return `${n(u?.input_tokens)} in · ${n(u?.output_tokens)} out`
}

/** A step's verdict: its words and whether it reads as a failure. */
export interface Verdict {
  text: string
  bad: boolean
}

/**
 * stepVerdict is the step's one line: where the run failed at this
 * step, what stopped it (the stop's words and the error's detail) and
 * what that means — the step finished and the loop's guard refused the
 * next model call (a guard's stop: GUARD_STOPS), or the step never
 * finished; then the finish reason,
 * its tokens, its latency and its time to first token. ms is the
 * step's measured span, used when no step_finish latency is known;
 * last: no later step is drawn, so a failure named past it (a cancel
 * before the next step began, landStep) reads on it.
 */
export function stepVerdict(
  step: AnswerStep,
  run: AnswerRun,
  ms?: number,
  last = false
): Verdict {
  const parts: string[] = []
  let bad = false
  const f = step.finish
  const at = failedAtStep(run) ?? -1
  if (at === step.index || (last && at > step.index)) {
    bad = true
    const w = stoppedByWords(run.stopped_by, run.cancel_cause)
    const d = stopDetail(run.err, run.stopped_by)
    const why = (run.err ?? "").replace(/^weft: run failed at step \d+: /, "")
    const head = w
      ? `✗ ${w.word}${d ? ` (${d})` : ""}`
      : `✗ failed${why ? `: ${clip(why, 120)}` : ""}`
    parts.push(
      f && GUARD_STOPS.has(run.stopped_by ?? "")
        ? `${head} — the loop refused step ${step.index + 1}`
        : head
    )
  } else if (!f) {
    bad = run.status !== "running"
    parts.push(bad ? "no step_finish" : "running…")
  }
  if (f) parts.push(f.reason || "finished", tokensWord(f.usage))
  const took = f?.latencyMs && f.latencyMs > 0 ? f.latencyMs : ms
  if (took !== undefined && Number.isFinite(took) && took >= 0)
    parts.push(msText(took))
  if (f?.ttftMs && f.ttftMs > 0) parts.push(`first token ${msText(f.ttftMs)}`)
  return { text: parts.join(" · "), bad }
}

/** What the request record says one step sent. */
export interface SentFacts {
  /** Messages the request carried. */
  count: number
  /** The previous recorded step's count: the new ones are the rest. */
  prevCount?: number
  /** Tools offered. */
  tools: number
  /** The step's marks (lib/requests.ts's byStep). */
  promptChanged?: boolean
  catalogChanged?: boolean
}

export const SENT_NOT_RECORDED = "— no request record for this step"
export const SENT_UNCHANGED = "unchanged since the step before"

/** sentLine is "5 messages (1 new) · 1 tool offered · unchanged since
 * the step before" — or the marks the step carries, which the Request
 * tab explains (its chips: changed by PrepareStep, …). */
export function sentLine(s: SentFacts | undefined): string {
  if (!s) return SENT_NOT_RECORDED
  const fresh =
    s.prevCount !== undefined && s.count >= s.prevCount
      ? ` (${s.count - s.prevCount} new)`
      : ""
  const marks = [
    s.promptChanged ? "prompt changed at this step" : "",
    s.catalogChanged ? "catalog changed at this step" : "",
  ].filter(Boolean)
  return [
    `${count(s.count, "message")}${fresh}`,
    `${count(s.tools, "tool")} offered`,
    marks.length
      ? marks.join(" · ")
      : s.prevCount !== undefined
        ? SENT_UNCHANGED
        : "",
  ]
    .filter(Boolean)
    .join(" · ")
}

/** repliedLine is the model's reply in a line: its words, the calls it
 * asked for, or why there is none. */
export function repliedLine(step: AnswerStep, running = false): string {
  const calls = step.toolCalls.map(
    (c) => `${c.name}(${c.args === undefined ? "…" : short(c.args, 40)})`
  )
  const text = step.text ? `“${clip(step.text, 120)}”` : ""
  const asked = calls.length ? `called ${calls.join(", ")}` : ""
  if (text || asked) return [text, asked].filter(Boolean).join(" · ")
  if (!step.finish) return running ? "— (not yet)" : "— (no reply recorded)"
  return "— (no text, no calls)"
}

/** toolsLine is what the step's calls did: "2 ran · 1 error", "1
 * never finished", or "none — the model called no tool". */
export function toolsLine(step: AnswerStep, running = false): string {
  const calls = step.toolCalls
  if (!calls.length) return "none — the model called no tool"
  const done = calls.filter((c) => c.finishPos !== undefined)
  const errors = done.filter((c) => c.result?.isError).length
  const open = calls.length - done.length
  return [
    `${done.length} ran`,
    errors ? count(errors, "error") : "",
    open ? `${open} ${running ? "running" : "never finished"}` : "",
  ]
    .filter(Boolean)
    .join(" · ")
}

/** thenLine is what came after the step: the next step, or how the
 * run ended there (lib/stopped-by.ts's end card, at this step). */
export function thenLine(
  step: AnswerStep,
  run: AnswerRun,
  hasNext: boolean
): string {
  if (hasNext) return `→ step ${step.index + 1}`
  if (run.status === "running") return "running…"
  // The last step drawn is where the run ended: its end card names it.
  return endCard(run, step.index) ?? `■ ${run.status || "ended"}`
}
