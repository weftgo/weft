// The step-aligned compare (plan E3, E3.2): N runs against one base,
// rows by step ordinal, a column per compared field. N-way is N−1 calls
// of GET /api/diff?a=&b= with the same a; every row, cell, mark and
// "changed at step N" marker is lib/stepdiff.ts's reading of those
// responses — the panel's 2-way block reads the same module (parity).
// Gated on capability "diff": without it nothing renders here, and the
// compare page says which option left it off.
import { useQueries } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { Fragment, useEffect, useRef } from "react"

import { diffQuery } from "@/lib/api"
import { runLink } from "@/lib/links"
import {
  CELL_REASONS,
  CELL_WORDS,
  COLUMN_LABELS,
  DIFF_COLUMNS,
  cellHole,
  cellText,
  markerWords,
  nWayView,
} from "@/lib/stepdiff"
import type { CellState, DiffColumn, DiffDoc, DiffSide, NWayView, SideView } from "@/lib/stepdiff"
import { useCapabilities } from "@/hooks/use-capabilities"
import { HoleBadge, HoleBadges } from "@/components/studio/hole-badge"
import { Badge } from "@/components/ui/badge"
import { Spinner } from "@/components/ui/spinner"

const STATE_TONE: Record<CellState, string> = {
  same: "text-faint",
  changed: "bg-ev-error/10 text-foreground",
  unknown: "text-muted-foreground italic",
  missing: "text-muted-foreground",
}

/** SideMarks draws a side's marks and its holes: a mark that is a
 * hole of the table (compacted, max_tokens, interrupted — one its
 * holes do not already carry) is that hole's badge, as the panel's
 * badge() draws it; the others are chips. */
function SideMarks({ side }: { side: SideView }) {
  if (!side.marks.length && !side.holes.length) return null
  return (
    <span className="flex flex-wrap items-center gap-1">
      {side.marks.map((m) =>
        m.hole ? (
          <HoleBadge key={m.mark} hole={m.hole} />
        ) : (
        <Badge
          key={m.mark}
          variant="outline"
          className="font-mono text-[10px] font-normal text-muted-foreground"
          title={m.title}
          data-diff-mark={m.mark}
        >
          {m.label}
          <span className="sr-only">{` — ${m.title}`}</span>
        </Badge>
        )
      )}
      <HoleBadges holes={side.holes} />
    </span>
  )
}

function Cell({
  run,
  col,
  side,
  base,
  state,
}: {
  run: string
  col: DiffColumn
  side: SideView
  /** The base side (a compared run's cell): a hole either side carries
   * in this column rides beside the state. */
  base?: DiffSide | null
  state?: CellState
}) {
  const text = cellText(side.side, col)
  const hole = state ? cellHole(col, base ?? null, side.side) : cellHole(col, side.side)
  // The base column holds the values; a compared run's cell says its
  // state, and the value where it differs from the base. A withheld
  // system prompt is the hidden badge, its state still the hashes'.
  if (!state)
    return (
      <td className="max-w-[18rem] border-l px-2 py-1 align-top break-words" data-diff-run={run} data-diff-cell={col}>
        {hole ? <HoleBadge hole={hole} /> : text}
      </td>
    )
  return (
    <td
      className={`max-w-[18rem] border-l px-2 py-1 align-top break-words ${STATE_TONE[state]}`}
      data-diff-run={run}
      data-diff-cell={col}
      data-diff-state={state}
      title={CELL_REASONS[state]}
    >
      {state === "changed" ? text : CELL_WORDS[state]}
      {hole ? (
        <>
          {" "}
          <HoleBadge hole={hole} />
        </>
      ) : null}
    </td>
  )
}

/** StepDiffTable renders an N-way view: the base run first, each
 * compared run beside it, one row per step ordinal. */
export function StepDiffTable({ view, focus }: { view: NWayView; focus?: number }) {
  // The step a link landed on (compareLink's step): scrolled to once.
  const target = useRef<HTMLTableRowElement>(null)
  useEffect(() => {
    target.current?.scrollIntoView({ block: "center" })
  }, [focus])
  if (view.error)
    return (
      <p className="text-xs text-status-bad" role="alert" data-step-diff-error>
        {view.error}
      </p>
    )
  const runs = [view.base.run_id, ...view.others.map((o) => o.run_id)]
  const markers = view.others.flatMap((o, i) =>
    view.rows
      .filter((r) => r.others[i].changed)
      .map((r) => ({ run: o.run_id, step: r.step, words: markerWords({ step: r.step, ...r.others[i], a: r.base, b: r.others[i].side }) }))
  )
  return (
    <div className="space-y-2" data-step-diff>
      {view.holes.length ? (
        <div className="space-y-1" data-diff-holes>
          {view.holes.map((h) => (
            <HoleBadge key={h.hole} {...h} detail />
          ))}
        </div>
      ) : null}
      <ul className="space-y-0.5 text-xs" aria-label="changed steps">
        {view.others.map((o, i) =>
          view.markers[i].length === 0 ? (
            <li key={o.run_id} className="text-faint" data-diff-none={o.run_id}>
              {o.run_id}: no step changed
              {view.rows.some((r) => DIFF_COLUMNS.some((c) => r.others[i].cells[c] === "unknown"))
                ? " among the columns both runs recorded"
                : ""}
            </li>
          ) : null
        )}
        {markers.map((m) => (
          <li key={`${m.run}#${m.step}`} data-diff-marker={m.step} data-diff-run={m.run}>
            <Link {...runLink(m.run, { step: m.step })} className="text-thread-ink hover:underline">
              {m.words}
            </Link>
            {view.others.length > 1 ? <span className="text-faint"> · {m.run}</span> : null}
          </li>
        ))}
      </ul>
      <div className="overflow-x-auto rounded border">
        <table className="w-full font-mono text-[11px]">
          <caption className="sr-only">
            steps of {runs.join(", ")} aligned by step ordinal; {view.base.run_id} is the base
          </caption>
          <thead>
            <tr className="text-left text-faint">
              <th scope="col" rowSpan={2} className="px-2 py-1 font-normal">
                step
              </th>
              {runs.map((r, i) => (
                <th key={r} scope="colgroup" colSpan={DIFF_COLUMNS.length + 1} className="border-l px-2 py-1 font-normal">
                  <Link {...runLink(r)} className="hover:text-foreground hover:underline">
                    {r}
                  </Link>
                  {i === 0 ? " · base" : ""}
                </th>
              ))}
            </tr>
            <tr className="text-left text-faint">
              {runs.map((r) => (
                <Fragment key={r}>
                  <th scope="col" className="border-l px-2 py-1 font-normal">
                    marks
                  </th>
                  {DIFF_COLUMNS.map((c) => (
                    <th key={c} scope="col" className="border-l px-2 py-1 font-normal">
                      {COLUMN_LABELS[c]}
                    </th>
                  ))}
                </Fragment>
              ))}
            </tr>
          </thead>
          <tbody>
            {view.rows.map((row) => (
              <tr
                key={row.step}
                ref={row.step === focus ? target : undefined}
                className={`border-t ${row.changed ? "bg-secondary/40" : ""} ${row.step === focus ? "outline-2 outline-thread" : ""}`}
                data-diff-step={row.step}
                data-diff-target={row.step === focus ? "" : undefined}
                data-changed={row.changed ? "" : undefined}
              >
                <th scope="row" className="px-2 py-1 text-left align-top font-normal">
                  <Link {...runLink(view.base.run_id, { step: row.step })} className="hover:underline">
                    {row.step}
                  </Link>
                </th>
                <td className="border-l px-2 py-1 align-top" data-diff-run={view.base.run_id} data-diff-marks>
                  {row.base.side ? <SideMarks side={row.base} /> : <span className="text-muted-foreground">missing</span>}
                </td>
                {DIFF_COLUMNS.map((c) => (
                  <Cell key={c} run={view.base.run_id} col={c} side={row.base} />
                ))}
                {row.others.map((o) => (
                  <Fragment key={o.run_id}>
                    <td className="border-l px-2 py-1 align-top" data-diff-run={o.run_id} data-diff-marks>
                      <SideMarks side={o.side} />
                    </td>
                    {DIFF_COLUMNS.map((c) => (
                      <Cell key={c} run={o.run_id} col={c} side={o.side} base={row.base.side} state={o.cells[c]} />
                    ))}
                  </Fragment>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

/**
 * StepCompare fetches the N−1 diffs of `others` against `base` and
 * draws them. Renders nothing without capability "diff" (the
 * capability seam: a hidden control, never a broken one) or without a
 * run to compare. A side whose diff failed (a 404 on a mistyped id) is
 * badged with its error, never blanking the sides that answered; only
 * when none answered is the error the whole compare.
 */
export function StepCompare({ base, others, focus }: { base: string; others: string[]; focus?: number }) {
  const { has } = useCapabilities()
  const ids = [...new Set(others.filter((x) => x && x !== base))]
  const on = has("diff") && !!base && ids.length > 0
  const qs = useQueries({ queries: ids.map((b) => ({ ...diffQuery(base, b), enabled: on })) })
  if (!on) return null
  // A failed background re-read keeps what was drawn: a side with data
  // is drawn whatever its last read said.
  const sides = ids.map((id, i) => ({ id, q: qs[i] }))
  if (sides.some(({ q }) => !q.data && !q.isError))
    return (
      <div className="flex items-center gap-2 text-xs text-faint">
        <Spinner /> reading the step compare…
      </div>
    )
  const answered = sides.filter(({ q }) => q.data)
  const failed = sides.filter(({ q }) => !q.data && q.isError)
  const errors = failed.length ? (
    <ul className="space-y-0.5 text-xs" aria-label="runs not compared">
      {failed.map(({ id, q }) => (
        <li key={id} className="flex flex-wrap items-center gap-1.5" role="alert" data-step-diff-error data-step-diff-side-error={id}>
          <Badge variant="outline" className="border-status-bad/40 font-mono text-[10px] font-normal text-status-bad">
            not compared
          </Badge>
          <span className="font-mono">{id}</span>
          <span className="text-status-bad">the step compare could not be read: {q.error?.message}</span>
        </li>
      ))}
    </ul>
  ) : null
  if (!answered.length) return errors
  return (
    <div className="space-y-2">
      {errors}
      <StepDiffTable view={nWayView(answered.map(({ q }) => q.data as DiffDoc))} focus={focus} />
    </div>
  )
}
