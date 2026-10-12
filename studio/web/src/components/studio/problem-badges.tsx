// A failed run's kind chip and a run's problem badges (U4), drawn from
// lib/error-kind.ts's one table — the runs table's badges line, the run
// header's kind chip and stats strip. Badges only on problems: a clean
// run draws nothing.
import {
  kindChip,
  problemBadges,
  type ProblemRow,
} from "@/lib/error-kind"

/** The kind chip's class per tone: the cancelled tone is its own (the
 * run's context ended), a plain class until U1's status vocabulary. */
export function kindToneClass(tone: "bad" | "cancelled"): string {
  return tone === "cancelled"
    ? "border-status-int/40 text-status-int"
    : "border-status-bad/40 text-status-bad"
}

/** KindChip is the failed run's kind, "[max_steps]"; nothing without
 * one. */
export function KindChip({ errorType }: { errorType?: string }) {
  const c = kindChip(errorType)
  if (!c) return null
  return (
    <span
      className={`rounded-sm border px-1.5 py-0.5 font-mono text-[11px] ${kindToneClass(c.tone)}`}
      title={c.title}
      data-error-type={errorType}
      data-tone={c.tone}
    >
      {c.text}
    </span>
  )
}

/** ProblemBadges is the runs table's line under the title: the kind
 * chip and one glyph per problem, each with its words as the
 * accessible label. */
export function ProblemBadges({
  run,
}: {
  run: ProblemRow & { error_type?: string }
}) {
  const bs = problemBadges(run)
  if (!run.error_type && bs.length === 0) return null
  return (
    <div
      className="flex flex-wrap items-center gap-1 font-mono text-[11px] text-muted-foreground"
      data-problems
    >
      <KindChip errorType={run.error_type} />
      {bs.map((b) => (
        <span
          key={b.key}
          className="rounded-sm border px-1 tabular-nums"
          title={b.label}
          aria-label={b.label}
          data-problem={b.key}
        >
          {b.glyph}
        </span>
      ))}
    </div>
  )
}

/** ProblemStrip is the run header's loop summary beside the step count
 * (UX-A-40): each problem in words — "↻2 2 model attempts retried" —
 * nothing on a clean run. */
export function ProblemStrip({ row }: { row: ProblemRow }) {
  const bs = problemBadges(row)
  if (bs.length === 0) return null
  return (
    <>
      {bs.map((b) => (
        <span key={b.key} data-strip={b.key} title={b.label}>
          {b.glyph} {b.label}
        </span>
      ))}
    </>
  )
}
