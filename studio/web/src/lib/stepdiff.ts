// The step-aligned compare (plan E3, E3.2): GET /api/diff?a=&b=
// (studio/diff.go, E3.1) as both clients read it. Plain TypeScript — no
// React, no router, no DOM: Studio's compare table (N-way) and the
// devtools panel's result block (2-way) import this one module, so the
// same response draws the same rows, cells, marks and "changed at step
// N" markers on either surface (lib/stepdiff.test.ts pins it over the
// goldens; src/panel/parity.test.ts checks both surfaces against it).
//
// The rules the server already applied are read, never re-derived:
//   - rows are keyed by the step ORDINAL (decision 8, lib/links.ts) —
//     the SOURCE's when one side replays the other (A8: the response's
//     aligned names the replay side and its from_step; each row's
//     a_step / b_step is that side's own ordinal, the one a link to its
//     step carries). A source step before the replay's from_step is a
//     kept row: not re-run, nothing compared, never a change;
//   - a column the server lists in a row's differs[] (usage where a
//     side billed no tokens: a scripted replay's 0→0, UX-D-15) is
//     "differs": shown, never counted as a change;
//   - a column the server lists in a row's unknown[] is "not
//     comparable" — never "same", never "changed": a side did not
//     record it, and its holes say why;
//   - a step one run has and the other lacks is "missing" in every
//     column (the server's ["missing"] change);
//   - a compaction view and a subagent call are marks on their side
//     (decision 12), as are max_tokens, parked, running, interrupted
//     and error — drawn beside the side, never a change by themselves;
//   - every hole (a side's, the response's own truncated/response_cap)
//     is a badge from lib/honesty.ts's one table.
// N-way is N−1 two-way responses against the same base run a (the
// E3 split): nWayView lays them out side by side.
import type { Usage } from "./api"
import { CAUSES, HOLES, isHole, mergeHoles } from "./honesty"
import type { HoleMark } from "./honesty"
import { tokenPair } from "./format"

/** The compared columns, in the order every surface draws them. */
export type DiffColumn =
  "system" | "tool_calls" | "tool_results" | "text" | "usage"
export const DIFF_COLUMNS: readonly DiffColumn[] = [
  "system",
  "tool_calls",
  "tool_results",
  "text",
  "usage",
]

/** A column's header. */
export const COLUMN_LABELS: Record<DiffColumn, string> = {
  system: "system",
  tool_calls: "tool calls",
  tool_results: "tool results",
  text: "text",
  usage: "usage",
}

/** The marks a side can carry (studio/diff.go's diffSideOf). */
export type DiffMark =
  | "compacted"
  | "subagent"
  | "max_tokens"
  | "parked"
  | "running"
  | "interrupted"
  | "error"

/** One run of the pair: its id, the steps it recorded, its status. */
export interface DiffRun {
  run_id: string
  steps: number
  status: string
}

export interface DiffToolCall {
  name: string
  args: unknown
}

export interface DiffToolResult {
  call_id: string
  name: string
  content: string
  is_error: boolean
}

/** One run's step, reduced to the compared columns. system is null
 * when the step's request block does not carry it (hidden, not
 * recorded, a gap — the holes say which); text null when no messages
 * record holds the step's assistant message. */
export interface DiffSide {
  status: string
  system_hash: string
  system: string | null
  tool_calls: DiffToolCall[]
  tool_results: DiffToolResult[]
  text: string | null
  usage: Usage
  marks: string[]
  holes: HoleMark[]
  /** system_hidden: the step's request block is withheld from this
   * token (a read-scoped panel token) — the server's explicit fact;
   * absent when false. */
  system_hidden?: boolean
}

/** One aligned step ordinal of the pair (the source's under a lineage
 * alignment). */
export interface DiffRowDoc {
  step: number
  /** Each side's own ordinal at this row, null when it has no step here
   * (absent from a Studio before A8: the row's step). */
  a_step?: number | null
  b_step?: number | null
  /** A source step before the replay's from_step: kept, not re-run. */
  kept?: boolean
  /** Two replays of one source from different steps: the side that
   * kept the source's step here while the other re-ran it. That side's
   * column is the source's step and the re-run is compared with it
   * (U24.2-fix); a source Studio could not read leaves that side null
   * and every column unknown — not compared. */
  kept_by?: "a" | "b"
  changed: boolean
  a: DiffSide | null
  b: DiffSide | null
  /** The columns that differ, or ["missing"] when a side lacks the step. */
  changes: string[]
  /** The columns a side did not record: not comparable. */
  unknown: string[]
  /** The columns that differ but do not count as a change (usage when
   * a side billed no tokens). */
  differs?: string[]
}

/** The response's lineage alignment (studio/diff.go's diffAlign): the
 * common source run whose ordinals the rows are, the source ordinal
 * each side's step 0 is, and which side replays the other ("both":
 * two replays of a third run). */
export interface DiffAlign {
  replay: "a" | "b" | "both"
  source: string
  a_from: number
  b_from: number
}

/** GET /api/diff?a=&b= (capability "diff"). */
export interface DiffDoc {
  a: DiffRun
  b: DiffRun
  /** null (or absent, before A8) when the two align by their own ordinals. */
  aligned?: DiffAlign | null
  steps: DiffRowDoc[]
  summary: {
    changed_steps: number[]
    first_changed: number | null
    kept?: number
  }
  /** The response's own: truncated (cause response_cap) when a run has
   * more steps than one diff compares. */
  holes: HoleMark[]
}

/** A compare read right after a command named its run (UX-C-10): the
 * command's "finished" can arrive before Studio has ingested the run
 * row, so a 404 there is "arriving", retried ARRIVAL.retries times
 * ARRIVAL.delayMs apart, then an honest "has not reached Studio yet" — never an
 * error that never recovers, never an empty pane. */
export const ARRIVAL = {
  retries: 3,
  delayMs: 1000,
  reading: "recording the run… the step compare follows once Studio has it",
  notYet: (id: string) =>
    `${id} has not reached Studio yet (still absent after ${ARRIVAL.retries} more reads, ${ARRIVAL.delayMs / 1000} s apart)`,
  again: "read the compare again",
} as const

/** rowNote is a sibling row's note ("" for any other row). */
export function rowNote(
  row: Pick<DiffRowDoc, "kept_by" | "a" | "b">,
  state: RowState
): string {
  if (!row.kept_by) return ""
  return SOURCE_ROW.note(
    row.kept_by,
    !row.a || !row.b ? SOURCE_ROW.notCompared : ROW_WORDS[state]
  )
}

/** The API path of one 2-way diff (relative to /api/): both clients
 * fetch it with their own transport. */
export function diffPath(a: string, b: string): string {
  return `diff?${new URLSearchParams({ a, b }).toString()}`
}

// ── Cells ─────────────────────────────────────────────────────────

/** A compared cell's state. kept: the row is a source step the replay
 * did not re-run; differs: the values differ, not counted as a change. */
export type CellState =
  "same" | "changed" | "unknown" | "missing" | "kept" | "differs"

/** What a cell says. unknown is "not comparable": a side did not record
 * the column, so the two were not compared — never "same". */
export const CELL_WORDS: Record<CellState, string> = {
  same: "same",
  changed: "changed",
  unknown: "not comparable",
  missing: "missing",
  kept: "kept",
  differs: "differs · not counted",
}

/** A cell's tooltip: why it reads as it does. */
export const CELL_REASONS: Record<CellState, string> = {
  same: "both runs recorded this column and it is the same",
  changed: "both runs recorded this column and it differs",
  unknown:
    "a side did not record this column (its gaps say why): the two were not compared",
  missing: "one run has no such step",
  kept: "not re-run: the source's, kept",
  differs: "0 tokens on a side: not counted",
}

/** A row's state as one word: kept (not re-run), missing (one run has
 * no such step), same, or changed. */
export type RowState = "kept" | "missing" | "same" | "changed"

export const ROW_WORDS: Record<RowState, string> = {
  kept: "kept",
  missing: "missing",
  same: "same",
  changed: "changed",
}

/** The words of a sibling row (kept_by): one side kept the source's
 * step, the other was compared with it. */
export const SOURCE_ROW = {
  suffix: "vs source",
  note: (keptBy: "a" | "b", word: string) =>
    `${keptBy} kept the source's step; ${keptBy === "a" ? "b" : "a"} vs source: ${word}`,
  notCompared: "not compared: the source could not be read",
} as const

/** The cost cell's reason when the server marked a side's usage not
 * comparable (a derived run's, a pre-A1 run's, a gap's): cost follows
 * usage, so two priced figures are not compared either. */
export const COST_FOLLOWS_USAGE = `cost follows usage: ${CELL_REASONS.unknown}`

function argsText(args: unknown): string {
  if (args === undefined || args === null) return ""
  return typeof args === "string" ? args : JSON.stringify(args)
}

/** list reads a list the response may have left out (a thin row, a
 * side with no request record) as none: never a throw. */
const list = <T>(v: T[] | undefined): T[] => v ?? []

function clip(s: string, n = CELL_CHARS): string {
  return s.length > n ? `${s.slice(0, n - 1)}…` : s
}

// ── Marks ─────────────────────────────────────────────────────────

/** A mark as drawn: a chip with its words and why. The three marks
 * that are holes of the table (compacted, max_tokens, interrupted)
 * take its words; hole is set so a surface can draw the table's badge. */
export interface MarkChip {
  mark: string
  label: string
  title: string
  hole?: string
}

const MARK_WORDS: Record<string, { label: string; title: string }> = {
  subagent: {
    label: "subagent",
    title:
      "this step called a subagent: the child run's steps are its own, marked here, never aligned",
  },
  parked: {
    label: "parked",
    title: "this step parked a call awaiting a decision",
  },
  running: {
    label: "running",
    title: "this step was still running when the diff was read",
  },
  error: { label: "error", title: "this step ended in an error" },
}

/** cellHole is the hole a compared cell carries beside its state: the
 * hidden badge in the system column when either side's prompt is
 * withheld — a "same" there is the hashes', the words unseen. */
export function cellHole(
  col: DiffColumn,
  ...sides: (DiffSide | null)[]
): "hidden" | undefined {
  return col === "system" && sides.some(systemHidden) ? "hidden" : undefined
}

/** sideHoles is a side's holes as badges: the server's words, in the
 * table's order. */
export function sideHoles(side: DiffSide | null): HoleMark[] {
  return side ? mergeHoles(list(side.holes)) : []
}

/** cellText is what a side holds in a column, in one short line both
 * surfaces print the same: the system prompt (or its hash's head when
 * only the hash is known), name(args) per call, each result (an error
 * one prefixed "error:"), the text, the usage as in→out. A value the
 * side did not record reads as "—"; an empty one as "(none)". */
export function cellText(side: DiffSide | null, col: DiffColumn): string {
  if (!side) return "—"
  switch (col) {
    case "system":
      if (side.system != null)
        return side.system ? clip(side.system) : "(empty)"
      if (systemHidden(side)) return HOLES.hidden.label
      return side.system_hash ? `#${side.system_hash.slice(0, 8)}` : "—"
    case "tool_calls":
      return list(side.tool_calls).length
        ? clip(
            side.tool_calls
              .map((c) => `${c.name}(${argsText(c.args)})`)
              .join(", ")
          )
        : "(none)"
    case "tool_results":
      return list(side.tool_results).length
        ? clip(
            side.tool_results
              .map((r) => `${r.is_error ? "error: " : ""}${r.content}`)
              .join(" · ")
          )
        : "(none)"
    case "text":
      if (side.text === null) return "—"
      return side.text ? clip(side.text) : "(none)"
    case "usage":
      return (side.usage as Usage | undefined)
        ? tokenPair(side.usage.input_tokens, side.usage.output_tokens)
        : "—"
  }
}

/** systemHidden: the side's system prompt is withheld from this
 * token (a read-scoped panel token: system null, the side's
 * system_hidden). Its hash still compares, so the cell keeps the
 * server's state. A hidden hole alone — child runs a panel token's
 * scope left out (StepHole.count) — hides no prompt (P8-pre-web #7). */
export function systemHidden(side: DiffSide | null): boolean {
  return !!side && side.system == null && side.system_hidden === true
}

// ── Cell words ────────────────────────────────────────────────────

/** The longest a cell's value is drawn; the rest is "…". */
export const CELL_CHARS = 160

/** docHoles is the response's own holes: a truncated diff is the
 * response_cap cause (the step cap, nothing lost). */
export function docHoles(doc: Pick<DiffDoc, "holes">): HoleMark[] {
  return mergeHoles(
    (Array.isArray(doc.holes) ? doc.holes : []).map((h) =>
      h.hole === "truncated" && !h.cause ? { ...h, cause: "response_cap" } : h
    )
  )
}

function cellsOf(row: DiffRowDoc): Record<DiffColumn, CellState> {
  const out = {} as Record<DiffColumn, CellState>
  for (const c of DIFF_COLUMNS) out[c] = cellOf(row, c)
  return out
}

/** markChip is one mark's chip. */
export function markChip(mark: string): MarkChip {
  if (isHole(mark)) {
    const n = HOLES[mark]
    return {
      mark,
      label: n.label,
      title: n.fix ? `${n.reason} — fix: ${n.fix}` : n.reason,
      hole: mark,
    }
  }
  const w = Object.hasOwn(MARK_WORDS, mark) ? MARK_WORDS[mark] : undefined
  return { mark, label: w?.label ?? mark, title: w?.title ?? mark }
}

// ── The 2-way view ────────────────────────────────────────────────

/** One side of a drawn row. */
export interface SideView {
  side: DiffSide | null
  marks: MarkChip[]
  holes: HoleMark[]
}

/** One aligned step ordinal as a surface draws it. */
export interface StepRowView {
  step: number
  /** Each side's own ordinal (null: no step of it here) — the one a
   * link to that run's step carries. */
  aStep: number | null
  bStep: number | null
  state: RowState
  /** A sibling row's note (rowNote), "" otherwise. */
  note: string
  changed: boolean
  cells: Record<DiffColumn, CellState>
  /** The changed columns, by their headers ("tool results"). */
  changedColumns: string[]
  a: SideView
  b: SideView
}

/** The 2-way compare: rows by ordinal, one marker per changed step. */
export interface StepDiffView {
  a: DiffRun
  b: DiffRun
  rows: StepRowView[]
  /** The changed step ordinals, ascending. */
  markers: number[]
  holes: HoleMark[]
  /** How many cells are not comparable. */
  unknown: number
  /** The kept rows (source steps the replay did not re-run). */
  kept: number
  /** The rows with a column that differs, not counted (usage). */
  differs: number
}

/** sideView is a side as drawn: its holes, and its marks less any
 * mark its holes already carry (the server lists max_tokens and
 * interrupted in both) — one badge per hole per side. A mark that is a
 * hole (hole set) is drawn as that hole's badge on both surfaces. */
export function sideView(side: DiffSide | null): SideView {
  const holes = sideHoles(side)
  const marks = (side?.marks ?? [])
    .filter((m) => !holes.some((h) => h.hole === m))
    .map(markChip)
  return { side, marks, holes }
}

/** stepDiffView reads one response into the rows a renderer draws. */
export function stepDiffView(doc: DiffDoc): StepDiffView {
  const rows = [...list(doc.steps)]
    .sort((x, y) => x.step - y.step)
    .map((r): StepRowView => {
      const cells = cellsOf(r)
      const state = rowState(cells, r.changed)
      return {
        step: r.step,
        // Each side's own ordinal: the server's, else (a Studio before
        // A8, or a side with no step: null) the row's when it is there.
        aStep: r.a_step ?? (r.a ? r.step : null),
        bStep: r.b_step ?? (r.b ? r.step : null),
        state,
        note: rowNote(r, state),
        changed: r.changed,
        cells,
        changedColumns: DIFF_COLUMNS.filter((c) => cells[c] === "changed").map(
          (c) => COLUMN_LABELS[c]
        ),
        a: sideView(r.a),
        b: sideView(r.b),
      }
    })
  return {
    a: doc.a,
    b: doc.b,
    rows,
    markers: rows.filter((r) => r.changed).map((r) => r.step),
    holes: docHoles(doc),
    unknown: rows.reduce(
      (n, r) => n + DIFF_COLUMNS.filter((c) => r.cells[c] === "unknown").length,
      0
    ),
    kept: rows.filter((r) => r.state === "kept").length,
    differs: rows.filter((r) =>
      DIFF_COLUMNS.some((c) => r.cells[c] === "differs")
    ).length,
  }
}

/** rowState reads a row from its cells: kept or missing when every
 * cell is, else changed (the server's flag) or same. */
export function rowState(
  cells: Record<DiffColumn, CellState>,
  changed: boolean
): RowState {
  for (const s of ["kept", "missing"] as const)
    if (DIFF_COLUMNS.every((c) => cells[c] === s)) return s
  return changed ? "changed" : "same"
}

/** cellOf reads one column of one row as the server compared it. */
export function cellOf(row: DiffRowDoc, col: DiffColumn): CellState {
  if (row.kept) return "kept"
  // A sibling row whose source could not be read: not compared.
  if (row.kept_by && (!row.a || !row.b)) return "unknown"
  const changes = list(row.changes)
  if (!row.a || !row.b || changes.includes("missing")) return "missing"
  if (list(row.unknown).includes(col)) return "unknown"
  if (changes.includes(col)) return "changed"
  if (list(row.differs).includes(col)) return "differs"
  return "same"
}

/** markerWords is a changed step's marker: "changed at step 3", the
 * columns after it ("· tool results"), or which run lacks the step. */
export function markerWords(
  row: Pick<StepRowView, "step" | "cells" | "changedColumns" | "a" | "b">
): string {
  if (DIFF_COLUMNS.every((c) => row.cells[c] === "missing"))
    return `changed at step ${row.step} · ${row.a.side ? "only in the base run" : "missing from the base run"}`
  return `changed at step ${row.step}${row.changedColumns.length ? ` · ${row.changedColumns.join(", ")}` : ""}`
}

/** summaryWords is the compare's one line: how many steps changed and
 * the first — and, when some cells were not comparable, that "no step
 * changed" covers only what both runs recorded. */
export function summaryWords(
  v: Pick<StepDiffView, "rows" | "markers" | "unknown"> &
    Partial<Pick<StepDiffView, "kept" | "differs">>
): string {
  const n = v.markers.length
  const kept = v.kept ?? 0
  // The steps compared: a kept row is the source's alone.
  const compared = v.rows.length - kept
  // A step only one run has is a change (E3.1), said apart.
  const only = v.rows.filter(
    (r) => r.changed && DIFF_COLUMNS.every((c) => r.cells[c] === "missing")
  ).length
  let head = n
    ? `${n} of ${compared} ${compared === 1 ? "step" : "steps"} changed${only ? ` (${only} only in one run)` : ""} · first at step ${v.markers[0]}`
    : `no step changed of ${compared}`
  if (kept) head += ` · ${kept} kept (not re-run)`
  if (v.differs) head += " · usage differs (0 tokens)"
  return v.unknown
    ? `${head} · ${v.unknown} ${v.unknown === 1 ? "cell" : "cells"} not comparable`
    : head
}

// ── The N-way view ────────────────────────────────────────────────

/** One compared run's cells at one ordinal (N-way). */
export interface OtherCell {
  run_id: string
  /** The compared run's own ordinal at this row (null: none). */
  step: number | null
  state: RowState
  /** A sibling row's note (rowNote), "" otherwise. */
  note: string
  changed: boolean
  cells: Record<DiffColumn, CellState>
  changedColumns: string[]
  side: SideView
}

export interface NWayRow {
  step: number
  /** The base run's own ordinal at this row (null: none). */
  baseStep: number | null
  /** Any compared run changed here. */
  changed: boolean
  base: SideView
  others: OtherCell[]
}

export interface NWayView {
  base: DiffRun
  others: DiffRun[]
  rows: NWayRow[]
  /** Per compared run (same order as others), its changed ordinals. */
  markers: number[][]
  holes: HoleMark[]
  /** Whether any compared run aligned on the source's ordinals (A8). */
  aligned?: boolean
  /** Why the responses cannot be laid side by side (none, or not one
   * base): one line a renderer shows instead of the table. */
  error?: string
}

const ALL_MISSING = (): Record<DiffColumn, CellState> => {
  const out = {} as Record<DiffColumn, CellState>
  for (const c of DIFF_COLUMNS) out[c] = "missing"
  return out
}

/** nWayView lays N−1 responses against one base run side by side:
 * the base in the first column, each compared run beside it, rows by
 * ordinal. Every response must name the same a: else (or with none)
 * the view is empty and its error says why — it never throws. */
export function nWayView(docs: DiffDoc[]): NWayView {
  const empty = (
    error: string,
    base: DiffRun = { run_id: "", steps: 0, status: "" }
  ): NWayView => ({
    base,
    others: [],
    rows: [],
    markers: [],
    holes: [],
    error,
  })
  if (!docs.length) return empty("no step compare to show: no run was compared")
  const base = docs[0].a
  const other = docs.find((d) => d.a.run_id !== base.run_id)
  if (other)
    return empty(
      `the step compare needs one base run: ${base.run_id} and ${other.a.run_id} were both answered as base`,
      base
    )
  const views = docs.map(stepDiffView)
  const steps = [
    ...new Set(views.flatMap((v) => v.rows.map((r) => r.step))),
  ].sort((x, y) => x - y)
  const rows = steps.map((step): NWayRow => {
    const at = views.map((v) => v.rows.find((x) => x.step === step))
    const others = views.map((v, i): OtherCell => {
      const r = at[i]
      // A compared run's diff that does not reach the ordinal: neither
      // the base nor it has the step — absent on both, not a change.
      return r
        ? {
            run_id: v.b.run_id,
            step: r.bStep,
            state: r.state,
            note: r.note,
            changed: r.changed,
            cells: r.cells,
            changedColumns: r.changedColumns,
            side: r.b,
          }
        : {
            run_id: v.b.run_id,
            step: null,
            state: rowState(ALL_MISSING(), false),
            note: "",
            changed: false,
            cells: ALL_MISSING(),
            changedColumns: [],
            side: sideView(null),
          }
    })
    const baseRow = at.find((r) => r?.a.side)
    const baseSide = baseRow?.a ?? sideView(null)
    return {
      step,
      baseStep: baseRow?.aStep ?? null,
      changed: others.some((o) => o.changed),
      base: baseSide,
      others,
    }
  })
  return {
    base,
    others: docs.map((d) => d.b),
    rows,
    markers: views.map((v) => v.markers),
    holes: mergeHoles(...views.map((v) => v.holes)),
    aligned: docs.some((d) => !!d.aligned),
  }
}

/** UNTRACED is the panel's one line for a replay its runtime ran
 * without a tracer (UX-D-40): said once in the step compare's head,
 * never a not_recorded badge on each of the replay's steps. */
export const UNTRACED = "replay ran without tracing"

/** noSpans: a side's hole is not_recorded for want of spans (obsdb's
 * words: "recorded without a tracer"). */
export const noSpans = (h: HoleMark) =>
  h.reason === CAUSES.not_recorded?.no_spans.reason

/** untracedSides: every step a run has among sides carries noSpans. */
export const untracedSides = (sides: SideView[]) =>
  sides.some((s) => s.side) &&
  sides.every((s) => !s.side || s.holes.some(noSpans))

/** untraced: the compared run (b) ran without a tracer — both
 * surfaces' rule (Studio's N-way per compared run). */
export const untraced = (v: StepDiffView) =>
  untracedSides(v.rows.map((r) => r.b))
