// The edited request's preview (plan F2, ADR 0029 §8): POST
// /api/playground/preview (studio/preview.go, capability "preview")
// answers the first request a replay would send beside the one the
// source step recorded, and their diff — computed by the server, read
// here, never re-derived. Plain TypeScript: Studio's replay drawer and
// the panel's draw the same rows from previewView (parity.test.ts).
import type { Message, Part } from "./api"
import type { HoleMark } from "./honesty"

export interface PreviewRequest {
  system: string | null
  system_source: "override" | "agent" | "recorded"
  system_badge?: string
  messages: Message[] | null
  messages_badge?: string
  tools: { name: string; description?: string; schema?: unknown }[] | null
  tools_source: string
  tools_badge?: string
  model: string
  params: Record<string, unknown>
  params_source: string
  thinking: string
  tool_choice: { mode: string; name?: string }
  badge?: string
}

export type PreviewOp = "same" | "changed" | "added" | "removed"

export interface PreviewDoc {
  source: { run_id: string; from_step: number }
  agent: string
  engine: string
  runtime: string | null
  will_send: PreviewRequest
  was_sent: PreviewRequest
  diff: {
    system: "same" | "changed" | "unknown" | "hidden"
    messages: { op: PreviewOp; was: number | null; will: number | null }[] | null
    tools: { added: string[]; removed: string[] } | null
    model: string
    params: string
    thinking: string
    tool_choice: string
  }
  compacted_at: unknown
  warnings: { kind: string; message: string }[]
  unchecked: string[]
}

export const PREVIEW_PATH = "playground/preview"

/** How long a preview may stay unanswered before the drawer stops
 * waiting for it, and the line it says then: Run is released (the
 * preview failed, it did not refuse — the server still validates). */
export const PREVIEW_TIMEOUT_MS = 10_000
export const PREVIEW_SILENT = "the preview did not answer; Run is yours — the server still validates"

/** One aligned message: its op, role, and the words on each side. */
export interface PreviewRow {
  op: PreviewOp
  role: string
  was: string
  will: string
}

/** messageLine is one message in a line: the text, the calls as
 * name(args), a result as → content. */
export function messageLine(m: Message | undefined): string {
  if (!m) return ""
  const s = (m.content as (Part | null)[])
    .map((p) =>
      p?.type === "text"
        ? p.text
        : p?.type === "tool_call"
          ? `${p.name}(${JSON.stringify(p.args)})`
          : p?.type === "tool_result"
            ? `${p.call_id} → ${String(p.content)}`
            : (p?.type ?? "")
    )
    .join(" ")
  return s.length > 160 ? `${s.slice(0, 159)}…` : s
}

export interface PreviewView {
  /** null when the messages are hidden (a compaction view, read token). */
  rows: PreviewRow[] | null
  system: string
  /** The knobs the replay changes (model, params, thinking, tool choice). */
  changed: string[]
  added: string[]
  removed: string[]
  /** Every hole either side names, once: badges from the one table. */
  holes: HoleMark[]
  warnings: { kind: string; message: string }[]
  /** The line naming what only a runtime checks; "" when nothing. */
  unchecked: string
}

export function previewView(doc: PreviewDoc): PreviewView {
  const d = doc.diff
  const was = doc.was_sent.messages ?? []
  const will = doc.will_send.messages ?? []
  const holes: HoleMark[] = []
  for (const r of [doc.will_send, doc.was_sent])
    for (const h of [r.system_badge, r.messages_badge, r.tools_badge, r.badge])
      if (h && !holes.some((x) => x.hole === h)) holes.push({ hole: h })
  return {
    rows: d.messages
      ? d.messages.map((r) => {
          const m = (r.will !== null ? will[r.will] : undefined) ?? (r.was !== null ? was[r.was] : undefined)
          return {
            op: r.op,
            role: m?.role ?? "",
            was: r.was !== null && r.op !== "same" ? messageLine(was[r.was]) : "",
            will: r.will !== null ? messageLine(will[r.will]) : "",
          }
        })
      : null,
    system: d.system,
    changed: (["model", "params", "thinking", "tool_choice"] as const).filter((k) => d[k] === "changed").map((k) => k.replace("_", " ")),
    added: d.tools?.added ?? [],
    removed: d.tools?.removed ?? [],
    holes,
    warnings: doc.warnings,
    unchecked: doc.unchecked.length ? `unchecked until a runtime registers ${doc.agent}: ${doc.unchecked.join(", ")}` : "",
  }
}
