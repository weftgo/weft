// The runs list's error-class facet (UX-C-37; the audit's §4.8): the
// failures /api/search matched, grouped by their normalised error text
// (obsdb.ErrorClass: numbers N, ids <id>), most runs first — a pick is
// the error_class filter (a URL chip) — and, once one class is picked,
// "Experiment on these N →": the playground's multi-source matrix over
// those runs, one matrix per agent (lib/matrix.ts).
import { Link } from "@tanstack/react-router"

import type { RunRow, SearchFacets } from "@/lib/api"
import { matrixLink } from "@/lib/links"
import {
  MATRIX_SOURCES_MAX,
  MATRIX_WORDS,
  byAgent,
  sourcesParam,
} from "@/lib/matrix"
import { UNNAMED_AGENT } from "@/lib/format"

/** The facet's words. */
export const FACET_WORDS = {
  head: "error class",
  capped:
    "more distinct error texts than the server reads: the rarest are left out",
  pick: (c: string) => `list the runs that failed as “${c}”`,
  unpick: "every error class",
} as const

export function ErrorClassFacet({
  facets,
  picked,
  onPick,
}: {
  facets: SearchFacets
  picked?: string
  onPick: (cls: string | undefined) => void
}) {
  if (!facets.error_class.length) return null
  return (
    <fieldset
      className="space-y-0.5 rounded-lg border bg-background px-3 py-2 text-xs"
      data-error-class-facet
    >
      <legend className="eyebrow px-1">{FACET_WORDS.head}</legend>
      {facets.error_class.map((f) => (
        <label
          key={f.class}
          className="flex cursor-pointer items-baseline gap-2 font-mono text-[11px]"
          title={FACET_WORDS.pick(f.class)}
          data-error-class={f.class}
        >
          <input
            type="radio"
            name="error-class"
            checked={picked === f.class}
            onChange={() => onPick(f.class)}
          />
          <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">
            {f.class}
          </span>
          <span className="text-muted-foreground tabular-nums">{f.count}</span>
        </label>
      ))}
      {picked ? (
        <button
          type="button"
          className="font-mono text-[11px] text-thread-ink hover:underline"
          onClick={() => onPick(undefined)}
        >
          {FACET_WORDS.unpick}
        </button>
      ) : null}
      {facets.error_class_capped ? (
        <p className="text-[11px] text-faint">{FACET_WORDS.capped}</p>
      ) : null}
    </fieldset>
  )
}

/** ExperimentOnThese is the class's hand-off: the loaded failed runs,
 * one "Experiment on these N →" per agent (a matrix runs one agent),
 * at most MATRIX_SOURCES_MAX rows each, said when the list holds more
 * than were loaded or taken. */
export function ExperimentOnThese({
  runs,
  total,
}: {
  runs: RunRow[]
  total: number
}) {
  const failed = runs.filter((r) => r.status === "failed")
  if (!failed.length) return null
  const groups = byAgent(failed)
  return (
    <div
      className="flex flex-wrap items-center gap-2 text-xs"
      data-experiment-on
    >
      {groups.map((g) => {
        const ids = g.rows.map((r) => r.id).slice(0, MATRIX_SOURCES_MAX)
        return (
          <Link
            key={g.agent}
            {...matrixLink(sourcesParam(ids), g.agent)}
            className="rounded-md border bg-secondary px-2 py-1 hover:bg-accent"
            data-experiment-on-agent={g.agent}
          >
            {groups.length > 1 ? `${g.agent || UNNAMED_AGENT} · ` : ""}
            {MATRIX_WORDS.experimentOn(ids.length)}
          </Link>
        )
      })}
      {groups.length > 1 ? (
        <span className="text-faint">{MATRIX_WORDS.agents}</span>
      ) : null}
      {total > failed.length || failed.length > MATRIX_SOURCES_MAX ? (
        <span className="text-faint">
          {MATRIX_WORDS.capped(
            total,
            Math.min(failed.length, MATRIX_SOURCES_MAX)
          )}
        </span>
      ) : null}
    </div>
  )
}
