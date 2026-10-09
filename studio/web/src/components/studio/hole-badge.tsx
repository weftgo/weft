// Honesty badges (ADR 0028 §11, plan A3): every hole the record can
// have is one of ten named badges from the one shared table
// (lib/honesty.ts — the panel reads the same module), each with a
// reason and, where one exists, a fix. A pane with a hole shows the
// badge and says why; it is never just empty. A response's own reason
// and fix win over the table's words.
import { useId } from "react"

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
 * "shortened by the recorder: 12.3 KiB cut"); `note` is a visible
 * suffix after the label ("truncated · 12.1 KiB cut"), the label's word
 * kept first. An unknown badge still renders, verbatim.
 *
 * Without `detail` the reason and fix are the badge's description: a
 * hidden element its aria-describedby names (said once — a described
 * element's title is not read again); `title` is the pointer's.
 */
export function HoleBadge({
  hole,
  reason,
  fix,
  label,
  bytes,
  cause,
  note,
  detail = false,
}: {
  hole: string
  reason?: string
  fix?: string
  label?: string
  bytes?: number
  /** A known cause (lib/honesty's CAUSES): its words first. */
  cause?: string
  /** Visible words after the label: what this instance lost. */
  note?: string
  detail?: boolean
}) {
  const w = holeWords({ hole, reason, fix, bytes, cause })
  const descId = useId()
  const words = `${w.reason}${w.fix ? ` — fix: ${w.fix}` : ""}`
  const badge = (
    <Badge
      variant="outline"
      data-hole={hole}
      aria-describedby={detail ? undefined : descId}
      className={`font-mono text-[10px] font-normal ${
        w.tone === "note"
          ? "border-thread/40 text-muted-foreground"
          : "border-ev-error/40 text-ev-error"
      }`}
      title={w.fix ? `${w.reason} — fix: ${w.fix}` : w.reason}
    >
      {label ?? w.label}
      {note ? <span data-hole-note>{` · ${note}`}</span> : null}
    </Badge>
  )
  // The title is a pointer's alone: the words are the badge's
  // accessible description too (beside it, visibly, with detail).
  if (!detail)
    return (
      <>
        {badge}
        <span id={descId} hidden data-hole-words>
          {words}
        </span>
      </>
    )
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
