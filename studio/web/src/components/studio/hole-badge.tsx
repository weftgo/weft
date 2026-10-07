// Honesty badges (ADR 0028 §11, plan A3): every hole the record can
// have is one of ten named badges from the one shared table
// (lib/honesty.ts — the panel reads the same module), each with a
// reason and, where one exists, a fix. A pane with a hole shows the
// badge and says why; it is never just empty. A response's own reason
// and fix win over the table's words.
import { holeWords } from "@/lib/honesty"
import type { HoleMark } from "@/lib/honesty"
import { Badge } from "@/components/ui/badge"

export { HOLES, isHole } from "@/lib/honesty"
export type { Hole } from "@/lib/honesty"

/**
 * HoleBadge renders one hole: the badge, and with `detail` the reason
 * and fix as text beside it. `label` overrides the table's words for a
 * surface that knows more (the request record's not_recorded names the
 * version); `bytes` is a recorder cap's cut (truncated reads
 * "shortened by the recorder: 12.3 KiB cut"). An unknown badge still
 * renders, verbatim.
 */
export function HoleBadge({
  hole,
  reason,
  fix,
  label,
  bytes,
  detail = false,
}: {
  hole: string
  reason?: string
  fix?: string
  label?: string
  bytes?: number
  detail?: boolean
}) {
  const w = holeWords({ hole, reason, fix, bytes })
  const badge = (
    <Badge
      variant="outline"
      data-hole={hole}
      className={`font-mono text-[10px] font-normal ${
        w.tone === "note"
          ? "border-thread/40 text-muted-foreground"
          : "border-ev-error/40 text-ev-error"
      }`}
      title={w.fix ? `${w.reason} — fix: ${w.fix}` : w.reason}
    >
      {label ?? w.label}
    </Badge>
  )
  if (!detail) return badge
  return (
    <span className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
      {badge}
      <span className="text-[11px] text-muted-foreground">
        {w.reason}
        {w.fix ? <span className="text-faint"> · fix: {w.fix}</span> : null}
      </span>
    </span>
  )
}

/** HoleBadges renders a list of holes (a step's, a run's), one badge
 * each, in the table's order as given. */
export function HoleBadges({
  holes,
  detail = false,
}: {
  holes: HoleMark[]
  detail?: boolean
}) {
  if (holes.length === 0) return null
  return (
    <span className="flex flex-wrap items-center gap-1.5" data-holes>
      {holes.map((h) => (
        <HoleBadge key={h.hole} {...h} detail={detail} />
      ))}
    </span>
  )
}
