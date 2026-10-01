// Event summaries: one line per wire event for the replay gutter,
// the raw explorer and the current-event readout. Pure functions.
import type { Message, WireEvent } from "./api"

export type EventKind =
  | "step" // run/step boundaries
  | "tool" // tool_start
  | "result" // tool_finish ok
  | "error" // tool_finish is_error
  | "reasoning"
  | "delta" // text/args deltas

export function eventKind(ev: WireEvent): EventKind {
  switch (ev.type) {
    case "tool_start":
      return "tool"
    case "tool_finish":
      return ev.is_error ? "error" : "result"
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

function clip(s: string, n: number): string {
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
  return ev.type
}

/** A steered delivery's words: its messages' text parts, one line
 * each, whitespace folded — the summary of a user turn (ADR 0019). */
function steerText(messages: Message[] | null | undefined): string {
  return (messages ?? [])
    .map((m) =>
      m.content
        .filter((p): p is Extract<(typeof m.content)[number], { type: "text" }> => p.type === "text")
        .map((p) => p.text)
        .join("")
    )
    .join(" ⏎ ")
    .replace(/\s+/g, " ")
    .trim()
}

/** A one-line, human summary: what happened, in the machine's voice. */
export function eventSummary(ev: WireEvent, width = 96): string {
  switch (ev.type) {
    case "run_start":
      return `${ev.agent ?? "agent"} · ${ev.model.provider}/${ev.model.name} · ${ev.id}`
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
    case "tool_finish":
      return `${ev.name} → ${ev.is_error ? "error" : "ok"} · ${bytes(
        new TextEncoder().encode(ev.content).length
      )} · ${clip(ev.content, width)}`
    case "step_finish":
      return `step ${ev.index} · ${ev.reason} · ${ev.usage.input_tokens} in / ${ev.usage.output_tokens} out`
    case "steered":
      return `steered · after step ${ev.step} · ${clip(
        steerText(ev.messages),
        width
      )}`
    case "run_finish":
      return `${ev.steps} steps · ${ev.usage.input_tokens} in / ${ev.usage.output_tokens} out${
        ev.pending?.length ? ` · ${ev.pending.length} pending` : ""
      }`
  }
}
