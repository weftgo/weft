// The step-aligned compare (plan E3, E3.2): GET /api/diff?a=&b=
// (studio/diff.go, E3.1) as both clients read it. Plain TypeScript — no
// React, no router, no DOM: Studio's compare table (N-way) and the
// devtools panel's result block (2-way) import this one module, so the
// same response draws the same rows, cells, marks and "changed at step
// N" markers on either surface (lib/stepdiff.test.ts pins it over the
// goldens; src/panel/parity.test.ts checks both surfaces against it).
//
// The rules the server already applied are read, never re-derived:
//   - rows are keyed by the step ORDINAL (decision 8, lib/links.ts);
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
import { HOLES, isHole, mergeHoles } from "./honesty"
import type { HoleMark } from "./honesty"

/** The compared columns, in the order every surface draws them. */
export type DiffColumn = "system" | "tool_calls" | "tool_results" | "text" | "usage"
export const DIFF_COLUMNS: readonly DiffColumn[] = ["system", "tool_calls", "tool_results", "text", "usage"]

/** A column's header. */
export const COLUMN_LABELS: Record<DiffColumn, string> = {
  system: "system",
  tool_calls: "tool calls",
  tool_results: "tool results",
  text: "text",
  usage: "usage",
}

/** The marks a side can carry (studio/diff.go's diffSideOf). */
export type DiffMark = "compacted" | "subagent" | "max_tokens" | "parked" | "running" | "interrupted" | "error"

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
}

/** One step ordinal of the pair. */
export interface DiffRowDoc {
  step: number
  changed: boolean
  a: DiffSide | null
  b: DiffSide | null
  /** The columns that differ, or ["missing"] when a side lacks the step. */
  changes: string[]
  /** The columns a side did not record: not comparable. */
  unknown: string[]
}

/** GET /api/diff?a=&b= (capability "diff"). */
export interface DiffDoc {
  a: DiffRun
  b: DiffRun
  steps: DiffRowDoc[]
  summary: { changed_steps: number[]; first_changed: number | null }
  /** The response's own: truncated (cause response_cap) when a run has
   * more steps than one diff compares. */
  holes: HoleMark[]
}

/** The API path of one 2-way diff (relative to /api/): both clients
 * fetch it with their own transport. */
export function diffPath(a: string, b: string): string {
  return `diff?${new URLSearchParams({ a, b }).toString()}`
}

// ── Cells ─────────────────────────────────────────────────────────

/** A compared cell's state. */
export type CellState = "same" | "changed" | "unknown" | "missing"

/** What a cell says. unknown is "not comparable": a side did not record
 * the column, so the two were not compared — never "same". */
export const CELL_WORDS: Record<CellState, string> = {
  same: "same",
  changed: "changed",
  unknown: "not comparable",
  missing: "missing",
}

/** A cell's tooltip: why it reads as it does. */
export const CELL_REASONS: Record<CellState, string> = {
  same: "both runs recorded this column and it is the same",
  changed: "both runs recorded this column and it differs",
  unknown: "a side did not record this column (its holes say why): the two were not compared",
  missing: "one run has no such step",
}

/** cellOf reads one column of one row as the server compared it. */
export function cellOf(row: DiffRowDoc, col: DiffColumn): CellState {
  if (!row.a || !row.b || row.changes.includes("missing")) return "missing"
  if (row.unknown.includes(col)) return "unknown"
  if (row.changes.includes(col)) return "changed"
  return "same"
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
    title: "this step called a subagent: the child run's steps are its own, marked here, never aligned",
  },
  parked: { label: "parked", title: "this step parked a call awaiting a decision" },
  running: { label: "running", title: "this step was still running when the diff was read" },
  error: { label: "error", title: "this step ended in an error" },
}

/** markChip is one mark's chip. */
export function markChip(mark: string): MarkChip {
  if (isHole(mark)) {
    const n = HOLES[mark]
    return { mark, label: n.label, title: n.fix ? `${n.reason} — fix: ${n.fix}` : n.reason, hole: mark }
  }
  const w = Object.hasOwn(MARK_WORDS, mark) ? MARK_WORDS[mark] : undefined
  return { mark, label: w?.label ?? mark, title: w?.title ?? mark }
}

/** sideHoles is a side's holes as badges: the server's words, in the
 * table's order. */
export function sideHoles(side: DiffSide | null): HoleMark[] {
  return side ? mergeHoles(side.holes) : []
}

/** docHoles is the response's own holes: a truncated diff is the
 * response_cap cause (the step cap, nothing lost). */
export function docHoles(doc: Pick<DiffDoc, "holes">): HoleMark[] {
  return mergeHoles(
    (Array.isArray(doc.holes) ? doc.holes : []).map((h) =>
      h.hole === "truncated" && !h.cause ? { ...h, cause: "response_cap" } : h
    )
  )
}

// ── Cell words ────────────────────────────────────────────────────

/** The longest a cell's value is drawn; the rest is "…". */
export const CELL_CHARS = 160

function clip(s: string, n = CELL_CHARS): string {
  return s.length > n ? `${s.slice(0, n - 1)}…` : s
}

function argsText(args: unknown): string {
  if (args === undefined || args === null) return ""
  return typeof args === "string" ? args : JSON.stringify(args)
}

/** systemHidden: the side's system prompt is withheld from this
 * token (a read-scoped panel token: system null under hidden). Its
 * hash still compares, so the cell keeps the server's state. */
export function systemHidden(side: DiffSide | null): boolean {
  return !!side && side.system === null && side.holes.some((h) => h.hole === "hidden")
}

/** cellHole is the hole a compared cell carries beside its state: the
 * hidden badge in the system column when either side's prompt is
 * withheld — a "same" there is the hashes', the words unseen. */
export function cellHole(col: DiffColumn, ...sides: (DiffSide | null)[]): "hidden" | undefined {
  return col === "system" && sides.some(systemHidden) ? "hidden" : undefined
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
      if (side.system !== null) return side.system ? clip(side.system) : "(empty)"
      if (systemHidden(side)) return HOLES.hidden.label
      return side.system_hash ? `#${side.system_hash.slice(0, 8)}` : "—"
    case "tool_calls":
      return side.tool_calls.length ? clip(side.tool_calls.map((c) => `${c.name}(${argsText(c.args)})`).join(", ")) : "(none)"
    case "tool_results":
      return side.tool_results.length
        ? clip(side.tool_results.map((r) => `${r.is_error ? "error: " : ""}${r.content}`).join(" · "))
        : "(none)"
    case "text":
      if (side.text === null) return "—"
      return side.text ? clip(side.text) : "(none)"
    case "usage":
      return `${side.usage.input_tokens}→${side.usage.output_tokens}`
  }
}

// ── The 2-way view ────────────────────────────────────────────────

/** One side of a drawn row. */
export interface SideView {
  side: DiffSide | null
  marks: MarkChip[]
  holes: HoleMark[]
}

/** One step ordinal as a surface draws it. */
export interface StepRowView {
  step: number
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
}

/** sideView is a side as drawn: its holes, and its marks less any
 * mark its holes already carry (the server lists max_tokens and
 * interrupted in both) — one badge per hole per side. A mark that is a
 * hole (hole set) is drawn as that hole's badge on both surfaces. */
export function sideView(side: DiffSide | null): SideView {
  const holes = sideHoles(side)
  const marks = (side?.marks ?? []).filter((m) => !holes.some((h) => h.hole === m)).map(markChip)
  return { side, marks, holes }
}

function cellsOf(row: DiffRowDoc): Record<DiffColumn, CellState> {
  const out = {} as Record<DiffColumn, CellState>
  for (const c of DIFF_COLUMNS) out[c] = cellOf(row, c)
  return out
}

/** stepDiffView reads one response into the rows a renderer draws. */
export function stepDiffView(doc: DiffDoc): StepDiffView {
  const rows = [...doc.steps]
    .sort((x, y) => x.step - y.step)
    .map((r): StepRowView => {
      const cells = cellsOf(r)
      return {
        step: r.step,
        changed: r.changed,
        cells,
        changedColumns: DIFF_COLUMNS.filter((c) => cells[c] === "changed").map((c) => COLUMN_LABELS[c]),
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
    unknown: rows.reduce((n, r) => n + DIFF_COLUMNS.filter((c) => r.cells[c] === "unknown").length, 0),
  }
}

/** markerWords is a changed step's marker: "changed at step 3", the
 * columns after it ("· tool results"), or which run lacks the step. */
export function markerWords(row: Pick<StepRowView, "step" | "cells" | "changedColumns" | "a" | "b">): string {
  if (DIFF_COLUMNS.every((c) => row.cells[c] === "missing"))
    return `changed at step ${row.step} · ${row.a.side ? "only in the base run" : "missing from the base run"}`
  return `changed at step ${row.step}${row.changedColumns.length ? ` · ${row.changedColumns.join(", ")}` : ""}`
}

/** summaryWords is the compare's one line: how many steps changed and
 * the first — and, when some cells were not comparable, that "no step
 * changed" covers only what both runs recorded. */
export function summaryWords(v: Pick<StepDiffView, "rows" | "markers" | "unknown">): string {
  const n = v.markers.length
  // A step only one run has is a change (E3.1), said apart.
  const only = v.rows.filter((r) => r.changed && DIFF_COLUMNS.every((c) => r.cells[c] === "missing")).length
  const head = n
    ? `${n} of ${v.rows.length} ${v.rows.length === 1 ? "step" : "steps"} changed${only ? ` (${only} only in one run)` : ""} · first at step ${v.markers[0]}`
    : `no step changed of ${v.rows.length}`
  return v.unknown ? `${head} · ${v.unknown} ${v.unknown === 1 ? "cell" : "cells"} not comparable` : head
}

// ── The N-way view ────────────────────────────────────────────────

/** One compared run's cells at one ordinal (N-way). */
export interface OtherCell {
  run_id: string
  changed: boolean
  cells: Record<DiffColumn, CellState>
  changedColumns: string[]
  side: SideView
}

export interface NWayRow {
  step: number
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
  const empty = (error: string, base: DiffRun = { run_id: "", steps: 0, status: "" }): NWayView => ({
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
  if (other) return empty(`the step compare needs one base run: ${base.run_id} and ${other.a.run_id} were both answered as base`, base)
  const views = docs.map(stepDiffView)
  const steps = [...new Set(views.flatMap((v) => v.rows.map((r) => r.step)))].sort((x, y) => x - y)
  const rows = steps.map((step): NWayRow => {
    const at = views.map((v) => v.rows.find((x) => x.step === step))
    const others = views.map((v, i): OtherCell => {
      const r = at[i]
      // A compared run's diff that does not reach the ordinal: neither
      // the base nor it has the step — absent on both, not a change.
      return r
        ? { run_id: v.b.run_id, changed: r.changed, cells: r.cells, changedColumns: r.changedColumns, side: r.b }
        : { run_id: v.b.run_id, changed: false, cells: ALL_MISSING(), changedColumns: [], side: sideView(null) }
    })
    const baseSide = at.find((r) => r?.a.side)?.a ?? sideView(null)
    return { step, changed: others.some((o) => o.changed), base: baseSide, others }
  })
  return {
    base,
    others: docs.map((d) => d.b),
    rows,
    markers: views.map((v) => v.markers),
    holes: mergeHoles(...views.map((v) => v.holes)),
  }
}
