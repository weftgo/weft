// Event summaries: one line per wire event for the replay gutter,
// the raw explorer and the current-event readout. Pure functions.
// Nested events unwrap to their inner event with a depth, so a
// subagent's tool call reads as what it is, one level in.
import type { Message, WireEvent } from "./api"

export type EventKind =
  | "step" // run/step boundaries
  | "tool" // tool_start
  | "result" // tool_finish ok
  | "error" // tool_finish is_error
  | "reasoning"
  | "delta" // text/args deltas
  | "nested"

/** Unwrap nested envelopes: the innermost event and how deep it sits. */
export function unwrap(ev: WireEvent): { inner: WireEvent; depth: number } {
  let inner = ev
  let depth = 0
  while (inner.type === "nested") {
    inner = inner.event
    depth++
  }
  return { inner, depth }
}

export function eventKind(ev: WireEvent): EventKind {
  const { inner } = unwrap(ev)
  switch (inner.type) {
    case "tool_start":
      return "tool"
    case "tool_finish":
      return inner.is_error ? "error" : "result"
    case "reasoning_delta":
      return "reasoning"
    case "text_delta":
    case "tool_args_delta":
      return "delta"
    case "nested":
      return "nested"
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
  nested: "text-ev-tool",
}
const kindBg: Record<EventKind, string> = {
  step: "bg-ev-step",
  tool: "bg-ev-tool",
  result: "bg-ev-result",
  error: "bg-ev-error",
  reasoning: "bg-ev-reasoning",
  delta: "bg-faint",
  nested: "bg-ev-tool",
}
const kindBorder: Record<EventKind, string> = {
  step: "border-ev-step/40",
  tool: "border-ev-tool/50",
  result: "border-ev-result/50",
  error: "border-ev-error/50",
  reasoning: "border-ev-reasoning/40",
  delta: "border-faint/40",
  nested: "border-ev-tool/50",
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
  nested: 2,
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

/** The event's type name as the wire spells it (inner type when nested). */
export function eventType(ev: WireEvent): string {
  return unwrap(ev).inner.type
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
  const { inner } = unwrap(ev)
  switch (inner.type) {
    case "run_start":
      return `${inner.agent ?? "agent"} · ${inner.model.provider}/${inner.model.name} · ${inner.id}`
    case "step_start":
      return `step ${inner.index}`
    case "text_delta":
      return `“${clip(inner.text, width)}”`
    case "reasoning_delta":
      return `“${clip(inner.text, width)}”`
    case "tool_args_delta":
      return `${inner.name} ${clip(inner.args, width)}`
    case "tool_start":
      return `${inner.name}(${short(inner.args, width)})`
    case "tool_finish":
      return `${inner.name} → ${inner.is_error ? "error" : "ok"} · ${bytes(
        new TextEncoder().encode(inner.content).length
      )} · ${clip(inner.content, width)}`
    case "step_finish":
      return `step ${inner.index} · ${inner.reason} · ${inner.usage.input_tokens} in / ${inner.usage.output_tokens} out`
    case "steered":
      return `steered · after step ${inner.step} · ${clip(
        steerText(inner.messages),
        width
      )}`
    case "run_finish":
      return `${inner.steps} steps · ${inner.usage.input_tokens} in / ${inner.usage.output_tokens} out${
        inner.pending?.length ? ` · ${inner.pending.length} pending` : ""
      }`
    case "nested":
      return "nested"
  }
}
