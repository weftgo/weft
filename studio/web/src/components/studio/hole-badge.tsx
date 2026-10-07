// Honesty badges (ADR 0028 §11; plan A1.4, A3 lifts this into the
// shared honesty table): every hole the record can have is one of ten
// named badges, each with a reason and — where one exists — a fix.
// A pane with a hole shows the badge and says why; it is never just
// empty. A response's own reason and fix win over the table's words.
// A3 moves this table and lib/requests.ts's REQUEST_HOLES into one
// shared non-React honesty module.
import { REQUEST_HOLES } from "@/lib/requests"
import { Badge } from "@/components/ui/badge"

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

interface HoleNote {
  label: string
  reason: string
  fix?: string
  /** Error-toned: something was lost, not merely withheld. */
  loss: boolean
}

export const HOLES: Record<Hole, HoleNote> = {
  truncated: {
    label: "truncated",
    reason: "a destination's size cap cut this record",
    fix: "raise the destination's content MaxBytes",
    loss: true,
  },
  stripped: { ...REQUEST_HOLES.stripped, loss: false },
  redacted: {
    label: "redacted",
    reason: "a destination's Redact rewrote this value before it was stored",
    loss: false,
  },
  max_tokens: {
    label: "max_tokens",
    reason: "the step hit the output token limit",
    fix: "raise MaxTokens",
    loss: true,
  },
  interrupted: {
    label: "interrupted",
    reason: "the run stopped without a finish",
    loss: true,
  },
  gap: {
    label: "gap",
    reason: "a record this one names was dropped on the way",
    fix: "check the exporter's drops",
    loss: true,
  },
  not_recorded: {
    label: "not recorded",
    reason: "this run was recorded before weft kept this record",
    fix: "upgrade weft and re-run",
    loss: false,
  },
  derived: {
    label: "derived",
    reason: "inferred by the reader, not stored as emitted",
    loss: false,
  },
  hidden: { ...REQUEST_HOLES.hidden, loss: false },
  compacted: {
    label: "compacted",
    reason: "compaction replaced these messages with a summary",
    loss: false,
  },
}

export function isHole(s: string | undefined): s is Hole {
  return s !== undefined && Object.prototype.hasOwnProperty.call(HOLES, s)
}

/**
 * HoleBadge renders one hole: the badge, and with `detail` the reason
 * and fix as text beside it. `label` overrides the table's words for a
 * surface that knows more (the request record's not_recorded names the
 * version). An unknown badge string still renders, verbatim.
 */
export function HoleBadge({
  hole,
  reason,
  fix,
  label,
  detail = false,
}: {
  hole: string
  reason?: string
  fix?: string
  label?: string
  detail?: boolean
}) {
  const note = isHole(hole) ? HOLES[hole] : undefined
  const why = reason || note?.reason || hole
  const remedy = fix || note?.fix
  const badge = (
    <Badge
      variant="outline"
      data-hole={hole}
      className={`font-mono text-[10px] font-normal ${
        note && !note.loss
          ? "border-thread/40 text-muted-foreground"
          : "border-ev-error/40 text-ev-error"
      }`}
      title={remedy ? `${why} — fix: ${remedy}` : why}
    >
      {label ?? note?.label ?? hole}
    </Badge>
  )
  if (!detail) return badge
  return (
    <span className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
      {badge}
      <span className="text-[11px] text-muted-foreground">
        {why}
        {remedy ? <span className="text-faint"> · fix: {remedy}</span> : null}
      </span>
    </span>
  )
}
