// The compaction marker (plan A9.2, ADR 0028 §8), shared by the run
// page and the panel: the line a compaction reads as, and "show
// original" — the transcript range a run-scope view replaced, resolved
// against the growth records the page already holds. No fetch of its
// own: the run document names the compactions (counts and hashes
// only), the transcript route serves the growth records, and a view's
// body (the messages that stood in) stays in the record — the export
// carries it.
//
// A run written before A9 has no `compactions` in its document, and a
// run that never compacted has []: both draw nothing. The record is
// optional — a run that did not compact emits none — so its absence
// is no hole and there is no "not recorded" badge for it.

import type { Message, Part, RunCompaction, Transcript } from "./api"
import { tokens } from "./format"

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`

/** A session marker (thread's compaction) rather than a run-scope view. */
export function isSessionMarker(c: RunCompaction): boolean {
  return c.scope === "session"
}

/** The run's compactions as the document gave them; [] for a document
 * without the field (a Studio older than A9.2). */
export function compactionsOf(doc: { compactions?: RunCompaction[] } | null | undefined): RunCompaction[] {
  return Array.isArray(doc?.compactions) ? doc.compactions : []
}

/**
 * compactionLine is the marker's words:
 *   - session: "12 messages compacted into 2 · 8.1k → 1.2k tokens"
 *     (the token part only when thread reported both estimates);
 *   - a run-scope view: "2 messages rewritten into 1 by PrepareStep",
 *     or "1 message inserted by PrepareStep" when nothing was replaced
 *     (ADR 0028 §8's insertion: from_seq == to_seq).
 */
export function compactionLine(c: RunCompaction): string {
  if (isSessionMarker(c)) {
    const toks =
      c.tokens_before && c.tokens_after
        ? ` · ${tokens(c.tokens_before)} → ${tokens(c.tokens_after)} tokens`
        : ""
    return `${plural(c.replaced, "message")} compacted into ${c.entries}${toks}`
  }
  if (c.replaced === 0) return `${plural(c.entries, "message")} inserted by PrepareStep`
  return `${plural(c.replaced, "message")} rewritten into ${c.entries} by PrepareStep`
}

/** The session marker's label: ADR 0028 §8 files it under the run that
 * produced the compacted context, so it reads as after this run. */
export const SESSION_LABEL = "session compaction · after this run"

/** What "show original" says for a session marker: it carries counts,
 * not a range — the context it replaced is not a seq range of this run. */
export function sessionNote(c: RunCompaction): string {
  return `thread compacted the session context this run belongs to: ${c.replaced} of its messages were replaced by ${c.entries}; the next run starts on the compacted context (its input record). The marker carries counts and a hash, never messages.`
}

/** The original messages of a view, or why they cannot be shown. */
export type Original =
  | { messages: Message[]; from: number; to: number }
  | { gap: string }
  | { loading: true }

/**
 * originalOf resolves a run-scope view's replaced range [from_seq,
 * to_seq) against the growth records: seq is a message's position in
 * the growth records concatenated in index order (ADR 0028 §8 — the
 * transcript route serves exactly those, views skipped), and the view
 * applies to the growth records below its own index. Every index below
 * it must be a growth record the page holds or another view the run
 * document names; a missing one, one asTranscript marked unreadable
 * (its body was not a message array, so its count — and every later
 * seq — is unknown), or a range past the messages held, is a gap —
 * never a guess. transcript null is still loading.
 */
export function originalOf(
  c: RunCompaction,
  transcript: Transcript | null | undefined,
  all: RunCompaction[]
): Original {
  if (!transcript) return { loading: true }
  const index = c.index ?? -1
  const from = c.from_seq ?? -1
  const to = c.to_seq ?? -1
  if (index < 0 || from < 0 || to < from)
    return { gap: "the view names no usable range: its replaced messages cannot be placed" }
  const views = new Set(all.filter((v) => !isSessionMarker(v) && v.index != null).map((v) => v.index as number))
  // The batches as the wire may hold them: a null entry is no record.
  const batches = (Array.isArray(transcript.batches) ? transcript.batches : []) as (Transcript["batches"][number] | null)[]
  const growth = batches
    .filter((b): b is Transcript["batches"][number] => b !== null && typeof b.index === "number" && b.index < index)
    .sort((a, b) => a.index - b.index)
  const held = new Set(growth.map((b) => b.index))
  const missing: number[] = []
  for (let i = 0; i < index; i++) if (!held.has(i) && !views.has(i)) missing.push(i)
  if (missing.length)
    return {
      gap: `messages record${missing.length === 1 ? "" : "s"} ${missing.slice(0, 6).join(", ")}${missing.length > 6 ? ", …" : ""} before the view ${missing.length === 1 ? "is" : "are"} missing: the replaced range cannot be placed`,
    }
  const unreadable = growth.filter((b) => b.unreadable).map((b) => b.index)
  if (unreadable.length)
    return {
      gap: `messages record${unreadable.length === 1 ? "" : "s"} ${unreadable.slice(0, 6).join(", ")}${unreadable.length > 6 ? ", …" : ""} before the view ${unreadable.length === 1 ? "does" : "do"} not read as messages: the positions after ${unreadable.length === 1 ? "it" : "them"} are unknown, so the replaced range cannot be placed`,
    }
  const seq: Message[] = []
  for (const b of growth) seq.push(...b.messages)
  if (to > seq.length)
    return { gap: `the view replaces messages ${from}–${to - 1}, but the transcript holds ${seq.length} before it` }
  return { messages: seq.slice(from, to), from, to }
}

/** One message as a line: its role and its words — text, a tool
 * call's name(args), a tool result's content, clipped. */
export function messageLine(m: Message, max = 160): string {
  // Defensive: a caller may hand a message straight from a stored body.
  const msg = m as Message | null
  const raw: unknown = msg?.content
  const parts = (Array.isArray(raw) ? raw : []) as (Part | null)[]
  const words = parts
    .map((p) => {
      switch (p?.type) {
        case "text":
          return p.text
        case "tool_call":
          return `${p.name}(${typeof p.args === "string" ? p.args : JSON.stringify(p.args ?? null)})`
        case "tool_result":
          return `${p.name ? `${p.name} → ` : ""}${p.content}`
        case "file":
          return `[file ${p.media_type}]`
        default:
          return ""
      }
    })
    .filter(Boolean)
    .join(" ")
  const line = `${msg?.role ?? "?"}: ${words}`
  return line.length > max ? `${line.slice(0, max - 1)}…` : line
}

/** What a view's replacement reads as on the page: the count, and
 * where its bodies are (the page never holds them). */
export function replacementNote(c: RunCompaction, requestCount?: number): string {
  const inAll = requestCount != null ? ` — the step's request carried ${plural(requestCount, "message")} in all` : ""
  return `in their place, ${plural(c.entries, "message")}${inAll}; the view's body stays in its record (export the run: compactions[].messages)`
}
