// The run page's run tree (U8.2; audit §6.1, UX-A-02, UX-A-04): what
// Studio's /runs/$id draws from lib/waterfall.ts's model. The rows are
// waterfallRows' — run › step › model call (› attempt) / tool lane ›
// child run, on the time or the order scale — projected into the
// Waterfall component's Span shape (treeSpans); a ?sel= is read back
// against them (resolveRowSel, the old forms included); and a selected
// row is read in the detail pane through the fold's own span where the
// fold has one (detailSpan: the step, the call, the run — what the
// story shows), else through its timed span. Plain TypeScript, no
// React: Studio only (the panel draws the model itself, U8.3).
import type { Span as TimedSpan } from "./api"
import type { Span, SpanKind, SpanTone } from "./trace"
import type { RowKind, WaterfallModel, WaterfallRow } from "./waterfall"
import { lanesWord, rowBadge, rowKeys } from "./waterfall"

const KIND: Record<RowKind, SpanKind> = {
  run: "run",
  child: "subagent",
  step: "step",
  model: "model",
  attempt: "attempt",
  tool: "tool",
  span: "span",
}

function tone(r: WaterfallRow): SpanTone {
  switch (r.status) {
    case "error":
      return r.kind === "tool" ? "error" : "bad"
    case "running":
      return "running"
    case "never":
      return "never"
    case "pending":
      return "result"
    default:
      return r.kind === "tool" ? "tool" : "step"
  }
}

/** A timed span as a detail row (the TimedDetail pane's input). */
function timedOf(span: TimedSpan, r: WaterfallRow): Span["timed"] {
  return {
    span,
    fromMs: r.start,
    toMs: r.took === null ? null : r.start + r.took,
  }
}

/**
 * treeSpans projects the model's rows into the Waterfall's rows: the
 * row's key is both its id and its key (unique in the model), the gist
 * its quieter label (and a step's lanes when it ran calls side by
 * side), the badge lib/waterfall.ts's rowBadge, the extent the row's
 * own on the model's scale; the critical path, lanes and a child run's
 * id ride along as marks.
 */
export function treeSpans(model: WaterfallModel): Span[] {
  const time = model.scale === "time"
  return model.rows.map((r) => {
    const lanes = lanesWord(r.lanes)
    const sub = [r.gist, lanes].filter(Boolean).join(" · ")
    return {
      id: r.key,
      key: r.key,
      parent: r.parent,
      depth: r.depth,
      kind: KIND[r.kind],
      label: r.label,
      sub: sub || undefined,
      badge: rowBadge(r),
      tone: tone(r),
      from: r.start,
      to: r.took === null ? null : r.start + r.took,
      ...(time && r.span ? { timed: timedOf(r.span, r) } : {}),
      runId: r.runId,
      critical: r.critical,
      ...(r.lane !== undefined ? { lane: r.lane } : {}),
      ...(r.lanes !== undefined ? { lanes: r.lanes } : {}),
      ...(r.child
        ? { childRun: r.child.runId, collapsed: r.child.collapsed }
        : {}),
    }
  })
}

/**
 * resolveRowSel finds the row a ?sel= names: its key; else an old form
 * of it — the fold's root key "run" (r:<run> now), the root's
 * run-qualified s:<run>:<n> / c:<run>:… (bare now), a t:<span id> (a
 * pre-U8.2 time-axis row: the row that span is behind), a bare
 * c:<call id> (pre-G1: the call in the linked step, else the first).
 * A key no row has selects nothing.
 */
export function resolveRowSel(
  rows: readonly WaterfallRow[],
  sel: string | undefined,
  rootId: string,
  step?: number
): string | undefined {
  if (!sel) return undefined
  const has = (k: string) => (rows.some((r) => r.key === k) ? k : undefined)
  const direct = has(sel)
  if (direct) return direct
  if (sel === "run") return has(`r:${rootId}`)
  const q = `:${rootId}:`
  if (/^[sc]:/.test(sel) && sel.startsWith(`${sel[0]}${q}`)) {
    const bare = has(
      sel[0] === "s"
        ? `s${sel.slice(q.length + 1)}`
        : `c:${sel.slice(q.length + 1)}`
    )
    if (bare) return bare
  }
  if (sel.startsWith("t:")) {
    const id = sel.slice(2)
    return rows.find((r) => r.span?.span_id === id)?.key
  }
  const call = /^c:([^:]+)$/.exec(sel)?.[1]
  if (!call) {
    // <run>:<span id> (U9.2): the row of that run its span is behind.
    const i = sel.lastIndexOf(":")
    const run = sel.slice(0, i)
    const id = sel.slice(i + 1)
    return i > 0
      ? rows.find((r) => r.runId === run && r.span?.span_id === id)?.key
      : undefined
  }
  const calls = rows.filter(
    (r) => r.kind === "tool" && r.runId === rootId && r.callId === call
  )
  return (calls.find((r) => r.step === step) ?? calls.at(0))?.key
}

/**
 * detailSpan is what the detail pane reads for a row: the fold's span
 * of the same key where there is one (the run's own steps and calls —
 * the story's bodies, the replay positions); a child run row reads its
 * tool call's (the subagent block, "open run"); a model call reads its
 * timed span (the chat facts and the attempts), or its step's on the
 * order scale; anything else its timed span — a step the fold does not
 * hold (a derived run's, a child's) the first span under it.
 */
export function detailSpan(
  key: string | undefined,
  rows: readonly Span[],
  folded: readonly Span[]
): Span | undefined {
  const row = key ? rows.find((r) => r.key === key) : undefined
  if (!row) return undefined
  const fold = (k: string | undefined) =>
    k ? folded.find((s) => s.key === k) : undefined
  switch (row.kind) {
    case "run":
      return (row.parent === undefined ? folded.at(0) : undefined) ?? timed(row)
    case "subagent": {
      const up = rows.find((r) => r.key === row.parent)
      return (up ? detailSpan(up.key, rows, folded) : undefined) ?? timed(row)
    }
    case "model":
      return row.timed ? row : (fold(row.parent) ?? undefined)
    case "step":
    case "tool":
      return (
        fold(row.key) ??
        timed(row) ??
        timed(rows.find((r) => r.parent === row.key && r.timed))
      )
    default:
      return timed(row)
  }
}

const timed = (r: Span | undefined) => (r?.timed ? r : undefined)

/** Every run drawn expanded: the model only asks has(). */
export const ALL_RUNS: ReadonlySet<string> = {
  has: () => true,
} as unknown as ReadonlySet<string>

/**
 * expandFor is the child runs to draw expanded so the row `sel` names
 * is drawn (U9.2: ?sel= restores the subtree it is in): the child rows
 * above it in `full`, the model with every child expanded. A child
 * row itself stays as it is drawn.
 */
export function expandFor(
  full: readonly WaterfallRow[],
  sel: string | undefined,
  rootId: string,
  step?: number
): string[] {
  const key = resolveRowSel(full, sel, rootId, step)
  const by = new Map(full.map((r) => [r.key, r]))
  const out: string[] = []
  const up = (r: WaterfallRow | undefined) =>
    r?.parent ? by.get(r.parent) : undefined
  for (let p = up(key ? by.get(key) : undefined); p; p = up(p))
    if (p.kind === "child") out.push(p.runId)
  return out
}

/**
 * qualifyKey is a run's own (bare) row key in its run-qualified form:
 * the key the /traces page gives that row when the run is not the
 * trace's root (lib/trace-sel.ts — one scheme, rowKeys).
 */
export function qualifyKey(key: string, run: string): string {
  const k = rowKeys(run, true)
  const s = /^s(\d+)(.*)$/.exec(key)
  if (s) return `${k.step(s[1])}${s[2]}`
  if (key.startsWith("c:")) return `c:${run}:${key.slice(2)}`
  if (key.startsWith("t:")) return k.span(key.slice(2))
  return key
}

/** The run page's link to the whole trace (UX-A-17). */
export const TRACE_LINK = "whole trace ↗"
export const TRACE_HINT =
  "the OTel trace this run is in: every run and span of it, the selection kept"
