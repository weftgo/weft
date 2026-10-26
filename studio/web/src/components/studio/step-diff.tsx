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

import { diffQuery, stepQuery } from "@/lib/api"
import type { CostFields } from "@/lib/api"
import { costCell, costCellWords, costView } from "@/lib/cost"
import { isNotFound } from "@/lib/not-found"
import { runLink } from "@/lib/links"
import {
  ARRIVAL,
  CELL_REASONS,
  CELL_WORDS,
  COST_FOLLOWS_USAGE,
  COLUMN_LABELS,
  DIFF_COLUMNS,
  ROW_WORDS,
  SOURCE_ROW,
  cellHole,
  cellText,
  markerWords,
  nWayView,
} from "@/lib/stepdiff"
import type {
  CellState,
  DiffColumn,
  DiffDoc,
  DiffSide,
  NWayView,
  RowState,
  SideView,
} from "@/lib/stepdiff"
import { useCapabilities } from "@/hooks/use-capabilities"
import { HoleBadge, HoleBadges } from "@/components/studio/hole-badge"
import { Badge } from "@/components/ui/badge"
import { Spinner } from "@/components/ui/spinner"
import { STATUS_TEXT } from "@/lib/status-class"

const STATE_TONE: Record<CellState, string> = {
  same: "text-faint",
  changed: "bg-ev-error/10 text-foreground",
  unknown: "text-muted-foreground italic",
  missing: "text-muted-foreground",
  kept: "text-faint",
  differs: "text-faint",
}

const ROW_TONE: Record<RowState, string> = {
  kept: "text-faint",
  missing: "text-muted-foreground",
  same: "text-faint",
  changed: `${STATUS_TEXT.bad}`,
}

/** RowWord is a compared run's row in one word — kept (not re-run) ·
 * same · changed — lib/stepdiff.ts's ROW_WORDS. */
function RowWord({ state, note = "" }: { state: RowState; note?: string }) {
  return (
    <span
      className={`mr-1 font-sans ${ROW_TONE[state]}`}
      data-diff-row-state={state}
      data-diff-row-note={note || undefined}
      title={note || (state === "kept" ? CELL_REASONS.kept : undefined)}
    >
      {ROW_WORDS[state]}
      {note ? ` · ${SOURCE_ROW.suffix}` : ""}
    </span>
  )
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
  const hole = state
    ? cellHole(col, base ?? null, side.side)
    : cellHole(col, side.side)
  // The base column holds the values; a compared run's cell says its
  // state, and the value where it differs from the base. A withheld
  // system prompt is the hidden badge, its state still the hashes'.
  if (!state)
    return (
      <td
        className="max-w-[18rem] border-l px-2 py-1 align-top break-words"
        data-diff-run={run}
        data-diff-cell={col}
      >
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
      {state === "changed" ? (
        text
      ) : state === "differs" ? (
        <>
          {text}
          <span className="sr-only">{` ${CELL_WORDS.differs}`}</span>
        </>
      ) : (
        CELL_WORDS[state]
      )}
      {hole ? (
        <>
          {" "}
          <HoleBadge hole={hole} />
        </>
      ) : null}
    </td>
  )
}

/** A step's cost as the compare reads it (plan A5): the step route's
 * cost block, "loading" while it is read, or the read's error. */
export type StepCostRead =
  { fields: CostFields } | { loading: true } | { error: string }

/** The compare's cost lookup: run id and step ordinal → its read;
 * absent (no capability "pricing") → no cost column at all. */
export type StepCosts = (run: string, step: number) => StepCostRead | undefined

/** The steps a run's cost column reads at most: one step route read
 * each, so a long run's tail says it was not read. */
export const COST_STEPS = 50

function costFieldsOf(r: StepCostRead | undefined): CostFields | null {
  if (!r)
    return {
      cost_known: false,
      cost_badge: "unknown",
      cost_reason: `the cost is read for the first ${COST_STEPS} steps only`,
    }
  if ("fields" in r) return r.fields
  if ("error" in r)
    return {
      cost_known: false,
      cost_badge: "unknown",
      cost_reason: `the step could not be read: ${r.error}`,
    }
  return null
}

/** The cost column's tones: STATE_TONE less the change mark — cost is
 * informational (it follows usage, already compared), so a different
 * figure is drawn neutral, never as a change. */
const COST_TONE: Record<CellState, string> = {
  ...STATE_TONE,
  changed: "text-foreground",
}

/** CostWords is an unknown cost's reason in the accessible text (the
 * hole badge's pattern), beside the visible words and the title. */
function CostWords({ why }: { why: string }) {
  return (
    <span className="sr-only" data-cost-words>
      {` not known: ${why}`}
    </span>
  )
}

/** CostCellTd is one run's cost at one ordinal: the base's figure, or
 * a compared run's state against it (lib/cost.ts's costCell: an
 * unknown side is "not comparable"). */
function CostCellTd({
  run,
  read,
  base,
  missing,
  compared,
  usageUnknown = false,
}: {
  run: string
  read: StepCostRead | undefined
  base?: StepCostRead | undefined
  missing: boolean
  compared: boolean
  /** The server marked this side's usage not comparable (a derived
   * run's, plan I1.3): cost follows usage, so it is not compared
   * either — two priced figures the usage column refused are no
   * "changed". */
  usageUnknown?: boolean
}) {
  const cls = "border-l px-2 py-1 align-top whitespace-nowrap"
  if (missing && !compared)
    return (
      <td
        className={`${cls} text-muted-foreground`}
        data-diff-run={run}
        data-diff-cell="cost"
      >
        —
      </td>
    )
  const side = costFieldsOf(read)
  const baseSide = costFieldsOf(base)
  if (!missing && (side === null || (compared && baseSide === null)))
    return (
      <td
        className={`${cls} text-faint`}
        data-diff-run={run}
        data-diff-cell="cost"
      >
        …
      </td>
    )
  if (!compared) {
    const v = costView(side)
    return (
      <td
        className={`${cls} ${v.known ? "" : "text-faint"}`}
        data-diff-run={run}
        data-diff-cell="cost"
        title={v.title}
      >
        {v.text}
        {v.known ? null : <CostWords why={v.title} />}
      </td>
    )
  }
  const state =
    usageUnknown && !missing ? "unknown" : costCell(baseSide, side, missing)
  const w =
    usageUnknown && !missing
      ? {
          text: CELL_WORDS.unknown,
          title: COST_FOLLOWS_USAGE,
          why: CELL_REASONS.unknown,
        }
      : costCellWords(
          state,
          state === "unknown" && costView(side).known ? baseSide : side
        )
  return (
    <td
      className={`${cls} ${COST_TONE[state]}`}
      data-diff-run={run}
      data-diff-cell="cost"
      data-diff-state={state}
      title={w.title}
    >
      {w.text}
      {state === "unknown" ? <CostWords why={w.why} /> : null}
    </td>
  )
}

/** StepDiffTable renders an N-way view: the base run first, each
 * compared run beside it, one row per step ordinal. With costs, each
 * run gains a cost column (plan A5) — informational: it never marks a
 * step changed (cost follows usage, already compared). */
export function StepDiffTable({
  view,
  focus,
  costs,
}: {
  view: NWayView
  focus?: number
  costs?: StepCosts
}) {
  const width = DIFF_COLUMNS.length + 1 + (costs ? 1 : 0)
  // The step a link landed on (compareLink's step): scrolled to once.
  const target = useRef<HTMLTableRowElement>(null)
  useEffect(() => {
    target.current?.scrollIntoView({ block: "center" })
  }, [focus])
  if (view.error)
    return (
      <p
        className={`text-xs ${STATUS_TEXT.bad}`}
        role="alert"
        data-step-diff-error
      >
        {view.error}
      </p>
    )
  const runs = [view.base.run_id, ...view.others.map((o) => o.run_id)]
  const markers = view.others.flatMap((o, i) =>
    view.rows
      .filter((r) => r.others[i].changed)
      .map((r) => ({
        run: o.run_id,
        step: r.step,
        own: r.others[i].step ?? r.step,
        words: markerWords({
          ...r.others[i],
          step: r.step,
          a: r.base,
          b: r.others[i].side,
        }),
      }))
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
              {view.rows.some((r) =>
                DIFF_COLUMNS.some((c) => r.others[i].cells[c] === "unknown")
              )
                ? " among the columns both runs recorded"
                : ""}
            </li>
          ) : null
        )}
        {markers.map((m) => (
          <li
            key={`${m.run}#${m.step}`}
            data-diff-marker={m.step}
            data-diff-run={m.run}
          >
            <Link
              {...runLink(m.run, { step: m.own })}
              className="text-thread-ink hover:underline"
            >
              {m.words}
            </Link>
            {view.others.length > 1 ? (
              <span className="text-faint"> · {m.run}</span>
            ) : null}
          </li>
        ))}
      </ul>
      {/* relative: an sr-only cell word positions inside this frame, never past the page (H9, UX-A-16). */}
      <div className="relative overflow-x-auto rounded border">
        <table className="w-full font-mono text-[11px]">
          <caption className="sr-only">
            steps of {runs.join(", ")} aligned by{" "}
            {view.aligned ? "the source's step ordinal" : "step ordinal"};{" "}
            {view.base.run_id} is the base
          </caption>
          <thead>
            <tr className="text-left text-faint">
              <th scope="col" rowSpan={2} className="px-2 py-1 font-normal">
                step
              </th>
              {runs.map((r, i) => (
                <th
                  key={r}
                  scope="colgroup"
                  colSpan={width}
                  className="border-l px-2 py-1 font-normal"
                >
                  <Link
                    {...runLink(r)}
                    className="hover:text-foreground hover:underline"
                  >
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
                    <th
                      key={c}
                      scope="col"
                      className="border-l px-2 py-1 font-normal"
                    >
                      {COLUMN_LABELS[c]}
                    </th>
                  ))}
                  {costs ? (
                    <th
                      scope="col"
                      className="border-l px-2 py-1 font-normal"
                      title="the step's own model call, priced at read time: shown, never counted as a change"
                    >
                      cost
                    </th>
                  ) : null}
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
                <th
                  scope="row"
                  className="px-2 py-1 text-left align-top font-normal"
                >
                  {row.baseStep !== null ? (
                    <Link
                      {...runLink(view.base.run_id, { step: row.baseStep })}
                      className="hover:underline"
                    >
                      {row.step}
                    </Link>
                  ) : (
                    row.step
                  )}
                </th>
                <td
                  className="border-l px-2 py-1 align-top"
                  data-diff-run={view.base.run_id}
                  data-diff-marks
                >
                  {row.base.side ? (
                    <SideMarks side={row.base} />
                  ) : (
                    <span className="text-muted-foreground">missing</span>
                  )}
                </td>
                {DIFF_COLUMNS.map((c) => (
                  <Cell
                    key={c}
                    run={view.base.run_id}
                    col={c}
                    side={row.base}
                  />
                ))}
                {costs ? (
                  <CostCellTd
                    run={view.base.run_id}
                    read={
                      row.baseStep !== null
                        ? costs(view.base.run_id, row.baseStep)
                        : undefined
                    }
                    missing={!row.base.side}
                    compared={false}
                  />
                ) : null}
                {row.others.map((o) => (
                  <Fragment key={o.run_id}>
                    <td
                      className="border-l px-2 py-1 align-top"
                      data-diff-run={o.run_id}
                      data-diff-marks
                    >
                      <RowWord state={o.state} note={o.note} />
                      <SideMarks side={o.side} />
                    </td>
                    {DIFF_COLUMNS.map((c) => (
                      <Cell
                        key={c}
                        run={o.run_id}
                        col={c}
                        side={o.side}
                        base={row.base.side}
                        state={o.cells[c]}
                      />
                    ))}
                    {costs && o.state === "kept" ? (
                      <td
                        className="border-l px-2 py-1 align-top text-faint"
                        data-diff-run={o.run_id}
                        data-diff-cell="cost"
                        data-diff-state="kept"
                        title={CELL_REASONS.kept}
                      >
                        {CELL_WORDS.kept}
                      </td>
                    ) : costs ? (
                      <CostCellTd
                        run={o.run_id}
                        read={
                          o.step !== null ? costs(o.run_id, o.step) : undefined
                        }
                        base={
                          row.baseStep !== null
                            ? costs(view.base.run_id, row.baseStep)
                            : undefined
                        }
                        missing={!row.base.side || !o.side.side}
                        compared
                        usageUnknown={o.cells.usage === "unknown"}
                      />
                    ) : null}
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
export function StepCompare({
  base,
  others,
  focus,
  arriving = false,
}: {
  base: string
  others: string[]
  focus?: number
  /** The compared runs were just named by a command (the replay
   * drawer's outcome): a 404 is a run not ingested yet, retried
   * (ARRIVAL), then said as "not recorded yet" with a read-again. */
  arriving?: boolean
}) {
  const { has } = useCapabilities()
  const ids = [...new Set(others.filter((x) => x && x !== base))]
  const on = has("diff") && !!base && ids.length > 0
  const qs = useQueries({
    queries: ids.map((b) => ({
      ...diffQuery(base, b, { arriving }),
      enabled: on,
    })),
  })
  // The cost column (plan A5): each side's step docs (the step route
  // prices a step's own model call), the first COST_STEPS of each run.
  const costOn = on && has("pricing") && has("steps")
  const answeredDocs = qs.flatMap((q) => (q.data ? [q.data] : []))
  const keys: { run: string; step: number }[] = []
  if (costOn && answeredDocs.length) {
    const seen = new Set<string>()
    for (const d of answeredDocs)
      for (const r of (d.steps as DiffDoc["steps"] | undefined) ?? [])
        for (const [run, side, own] of [
          [d.a.run_id, r.a, r.a_step ?? r.step],
          [d.b.run_id, r.b, r.b_step ?? r.step],
        ] as const) {
          const k = `${run}#${own}`
          if (side && own < COST_STEPS && !seen.has(k)) {
            seen.add(k)
            keys.push({ run, step: own })
          }
        }
  }
  const stepQs = useQueries({
    queries: keys.map((k) => ({ ...stepQuery(k.run, k.step), retry: false })),
  })
  const reads = new Map<string, StepCostRead>()
  keys.forEach((k, i) => {
    const q = stepQs[i]
    reads.set(
      `${k.run}#${k.step}`,
      q.data
        ? { fields: q.data }
        : q.isError
          ? { error: q.error.message }
          : { loading: true }
    )
  })
  const costs: StepCosts | undefined = costOn
    ? (run, step) => reads.get(`${run}#${step}`)
    : undefined
  if (!on) return null
  // A failed background re-read keeps what was drawn: a side with data
  // is drawn whatever its last read said.
  const sides = ids.map((id, i) => ({ id, q: qs[i] }))
  if (sides.some(({ q }) => !q.data && !q.isError))
    return (
      <div
        className="flex items-center gap-2 text-xs text-faint"
        data-step-diff-reading={
          arriving && sides.some(({ q }) => q.failureCount > 0)
            ? "arriving"
            : ""
        }
      >
        <Spinner />{" "}
        {arriving && sides.some(({ q }) => q.failureCount > 0)
          ? ARRIVAL.reading
          : "reading the step compare…"}
      </div>
    )
  const answered = sides.filter(({ q }) => q.data)
  const failed = sides.filter(({ q }) => !q.data && q.isError)
  const errors = failed.length ? (
    <ul className="space-y-0.5 text-xs" aria-label="runs not compared">
      {failed.map(({ id, q }) => (
        <li
          key={id}
          className="flex flex-wrap items-center gap-1.5"
          role="alert"
          data-step-diff-error
          data-step-diff-side-error={id}
        >
          <Badge
            variant="outline"
            className={`border-status-bad/40 font-mono text-[10px] font-normal ${STATUS_TEXT.bad}`}
          >
            not compared
          </Badge>
          <span className="font-mono">{id}</span>
          {arriving && isNotFound(q.error) ? (
            <>
              <span className="text-muted-foreground" data-step-diff-not-yet>
                {ARRIVAL.notYet(id)}
              </span>
              <button
                type="button"
                className="hover:underline"
                onClick={() => void q.refetch()}
              >
                {ARRIVAL.again}
              </button>
            </>
          ) : (
            <span className={`${STATUS_TEXT.bad}`}>
              the step compare could not be read: {q.error?.message}
            </span>
          )}
        </li>
      ))}
    </ul>
  ) : null
  if (!answered.length) return errors
  return (
    <div className="space-y-2">
      {errors}
      <StepDiffTable
        view={nWayView(answered.map(({ q }) => q.data as DiffDoc))}
        focus={focus}
        costs={costs}
      />
    </div>
  )
}
