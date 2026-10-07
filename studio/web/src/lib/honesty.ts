// The honesty table (ADR 0028 §11, plan A3, decision 11): every place a
// record is missing, cut or derived is one of ten named badges, each
// with a one-line reason and — where one exists — the one-line fix. The
// vocabulary is closed. Reasons and fixes are obsdb.HoleNote's words
// (Go is the source: honesty.test.ts checks this table against
// studio/testdata/holes.golden.json key by key); the label and tone are
// how a surface draws them. Plain TypeScript, no React: the run page
// (hole-badge.tsx) and the devtools panel both read this one module.

export type Hole =
  | "truncated"
  | "stripped"
  | "redacted"
  | "max_tokens"
  | "interrupted"
  | "gap"
  | "not_recorded"
  | "derived"
  | "hidden"
  | "compacted"

export interface HoleNote {
  /** The badge's words. */
  label: string
  reason: string
  fix?: string
  /** "loss": something was lost or cut (error-toned); "note": withheld,
   * inferred or by design. */
  tone: "loss" | "note"
}

/** The closed table, in ADR 0028 §11's order. */
export const HOLES: Record<Hole, HoleNote> = {
  truncated: {
    label: "truncated",
    reason:
      "a destination's size cap cut this content before it was stored (weft.content.truncated_bytes)",
    fix: "raise the destination's cap: otel.Content(otel.ContentConfig{MaxBytes: …}), -1 for unlimited",
    tone: "loss",
  },
  stripped: {
    label: "content not captured by this app",
    reason:
      "content not captured by this app: the destination's chain stripped it (weft.content = stripped), or the agent captured none (weft.Content(false), weft.content = none), so prompts, catalogs, messages, tool arguments and results were dropped before they were stored",
    fix: "turn content on: drop otel.NoContent() from the destination, or weft.Content(false) from the agent",
    tone: "note",
  },
  redacted: {
    label: "redacted",
    reason:
      "the destination's Redact hook rewrote this content before it was stored",
    tone: "note",
  },
  max_tokens: {
    label: "max_tokens",
    reason: "the step finished on the output token limit: the response was cut",
    fix: "raise max_tokens: weft.Params(weft.RequestParams{MaxTokens: …})",
    tone: "loss",
  },
  interrupted: {
    label: "interrupted",
    reason:
      "the run stopped reporting: no finish arrived and nothing was heard from it for over 30 s",
    tone: "loss",
  },
  gap: {
    label: "gap",
    reason:
      "a record the run counts is missing: a destination dropped it on the way",
    fix: "check the exporter's drops",
    tone: "loss",
  },
  not_recorded: {
    label: "not recorded",
    reason:
      "the record did not exist in the weft that wrote this run (v0.9.0 or earlier keeps no request, prompt or tools records, ADR 0028): nothing was lost, there was nothing to keep",
    fix: "upgrade weft and re-run",
    tone: "note",
  },
  derived: {
    label: "derived",
    reason:
      "computed by the reader, not stored as emitted: the record that would say it is absent",
    tone: "note",
  },
  hidden: {
    label: "hidden by your token scope",
    reason:
      "your token's scope may not read this: a read-scoped panel token does not read system prompts or tool catalogs",
    fix: "use a playground-scoped token",
    tone: "note",
  },
  compacted: {
    label: "compacted",
    reason:
      "the model saw a compacted view: compaction replaced part of the transcript for this request",
    fix: "open the compaction record",
    tone: "note",
  },
}

/** The table's order: the order every list of holes renders in. */
export const HOLE_ORDER = Object.keys(HOLES) as Hole[]

export function isHole(s: string | undefined): s is Hole {
  return s !== undefined && Object.prototype.hasOwnProperty.call(HOLES, s)
}

/** One hole as a surface holds it: the badge, and the words a response
 * or the fold gave for this instance (the table's otherwise). bytes is
 * what a recorder cap cut, summed (truncated). */
export interface HoleMark {
  hole: string
  reason?: string
  fix?: string
  bytes?: number
}

/** The weft.content.* attributes an event row or a live frame carries
 * (api.go's posEvent): absent when the chain left the content as
 * emitted. */
export interface ContentAttrs {
  "weft.content"?: string
  "weft.content.truncated_bytes"?: number
}

/** kib is a byte count as a badge says it: "92 B", "12.3 KiB". */
export function kib(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / (1024 * 1024)).toFixed(1)} MiB`
}

/** contentHoles reads an event's attrs: a recorder cap's cut is
 * truncated (with the bytes), a content-off chain's mark — or the
 * core's own capture-off mark, "none" — is stripped. Redaction is not
 * marked by weft's pipeline, so redacted never comes from here. */
export function contentHoles(attrs: unknown): HoleMark[] {
  if (typeof attrs !== "object" || attrs === null) return []
  const a = attrs as Record<string, unknown>
  const out: HoleMark[] = []
  const mark = a["weft.content"]
  if (mark === "stripped" || mark === "none") out.push({ hole: "stripped" })
  if (mark === "redacted") out.push({ hole: "redacted" })
  const cut = Number(a["weft.content.truncated_bytes"])
  if (Number.isFinite(cut) && cut > 0)
    out.push({ hole: "truncated", bytes: cut })
  return out
}

/** mergeHoles is the union of hole lists: one mark per badge (the first
 * one's words), truncated's bytes summed, in the table's order — an
 * unknown badge after the table's. */
export function mergeHoles(...lists: (HoleMark[] | undefined)[]): HoleMark[] {
  const by = new Map<string, HoleMark>()
  for (const list of lists)
    for (const x of (list ?? []) as unknown[]) {
      const m = x as HoleMark | null
      if (!m || typeof m.hole !== "string") continue
      const prev = by.get(m.hole)
      if (!prev) {
        by.set(m.hole, { ...m })
        continue
      }
      if (m.bytes && prev.hole === "truncated")
        prev.bytes = (prev.bytes ?? 0) + m.bytes
    }
  const rank = (h: string) => {
    const i = HOLE_ORDER.indexOf(h as Hole)
    return i < 0 ? HOLE_ORDER.length : i
  }
  return [...by.values()].sort((a, b) => rank(a.hole) - rank(b.hole))
}

/** holeWords is what a badge says: its label — truncated with bytes
 * reads "shortened by the recorder: 12.3 KiB cut" — and the reason and
 * fix, the mark's own winning over the table's. An unknown badge reads
 * verbatim. */
export function holeWords(m: HoleMark): {
  label: string
  reason: string
  fix?: string
  tone: "loss" | "note"
} {
  const note = isHole(m.hole) ? HOLES[m.hole] : undefined
  const label =
    m.hole === "truncated" && m.bytes
      ? `shortened by the recorder: ${kib(m.bytes)} cut`
      : (note?.label ?? m.hole)
  return {
    label,
    reason: m.reason || note?.reason || m.hole,
    fix: m.fix || note?.fix,
    tone: note?.tone ?? "loss",
  }
}
