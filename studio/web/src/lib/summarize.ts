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
        .filter((p): p is Extract<(typeof m.content)[number], { type: "text" }> =>
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
function inOut(u: { input_tokens?: number; output_tokens?: number } | undefined): string {
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
