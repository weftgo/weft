// The run tree (U8.1; audit §6.1, UX-A-04, UX-F-18): ONE row model
// both renderers draw — Studio's run page and the devtools panel's
// Timeline. A run is a tree
//
//   run › step › model call (› attempt) / tool (lane) › child run
//
// on one of two scales: `time` (milliseconds since the run's earliest
// span, from /spans) or `order` (stream positions, from the folded
// events — a run without spans). Every row carries its offset and its
// duration ("took"), a gist from the folded events and transcript (what
// the model decided, what a tool returned), its status, and whether it
// sits on the critical path. Filters dim, never hide (dimRows).
//
// Child runs are rows of their own, collapsed unless expanded. Their
// rows come from the same inputs: a descendant's spans (each carrying
// its weft.run.id, as GET /api/runs/{id}/spans?tree=1 serves them) in
// `spans`, its folded events in `children` — nothing else changes.
//
// Plain TypeScript, no React, no DOM: shared by both clients.
import type { Span as TimedSpan } from "./api"
import type { FoldedRun, FoldedStep, FoldedToolCall } from "./events"
import { plural, spanMs, stepCount, UNNAMED_AGENT } from "./format"
import { clip, NOT_EXECUTED } from "./summarize"
import type { SpanKind, SpanTone } from "./trace"

/** The axis: wall time from spans, or stream order from events. */
export type Scale = "time" | "order"

/** What a row stands for. `span` is any span weft does not name (a
 * foreign SDK's, an app's own) placed under the row of its parent. */
export type RowKind =
  "run" | "step" | "model" | "attempt" | "tool" | "child" | "span"

/** A row's state as a token; the words are the renderer's (lib). */
export type RowStatus = "ok" | "error" | "pending" | "running" | "never"

/** Why a row has no span (WaterfallRow.untimed). */
export type Untimed = "pending" | "missing" | "unrun"

export interface WaterfallRow {
  /** Unique within the model; the selection key, rowKeys' scheme (the
   * trace page's too): r:<run>, s<step>, s<step>:m (its model call),
   * s<step>:a<n> (an attempt), c:<step>:<call>, c:resume:<call>,
   * t:<span id>; a child run's rows run-qualified. */
  key: string
  /** The parent row's key (absent on the root). */
  parent?: string
  depth: number
  kind: RowKind
  /** The label rule: the agent's name, "step N", "model · <model>",
   * "attempt N · <model>", the tool's name, a span's own name. */
  label: string
  /** What happened, in a line: the finish and what the model asked
   * for, the first 60 characters of a reply or a tool result. */
  gist?: string
  runId: string
  step?: number
  callId?: string
  /** Offset from the domain's start (ms, or a stream position). */
  start: number
  /** The duration on the same scale; null while open. */
  took: number | null
  status: RowStatus
  /** error.type, or the ToolError code of an error result. */
  error?: string
  /** A model call's finish reason. */
  finish?: string
  /** A model call's time to first token, in ms. */
  ttft?: number
  /** A model call's attempts (only when the chain reported any). */
  attempts?: number
  /** A tool's lane within its step (1-based; parallel calls overlap). */
  lane?: number
  /** A step's lane count: its widest concurrency. */
  lanes?: number
  /** A child run's row: its id, whether it is drawn collapsed, and
   * whether its rows are loaded (spans or events to expand into). */
  child?: { runId: string; collapsed: boolean; loaded: boolean }
  /** On the longest chain: the child that ends last, level by level. */
  critical: boolean
  /** The span behind the row (time scale). */
  span?: TimedSpan
  /** A row the fold has and no span covers (time scale): `pending`
   * while the run goes on (its spans are not reported yet), `missing`
   * once it ended (dropped on the way, or never exported). It sits
   * after the row before it, with no duration: its time is not known.
   * `unrun`: a max_tokens step's call the loop never ran (rule 11) —
   * no span by design, "not executed", never a loss. */
  untimed?: Untimed
}

export interface WaterfallInput {
  /** The run the tree is of. */
  runId: string
  /** Its status (running: open rows stay open). */
  status?: string
  /** Its spans — and, from ?tree=1, its descendants'. */
  spans?: readonly TimedSpan[] | null
  /** Its folded events, transcript applied: the gists, and the order
   * scale's positions. */
  fold?: FoldedRun
  /** The stream length (closes the order-scale root, as the folded
   * trace does). */
  total?: number
  /** Descendant runs' folds, by run id. */
  children?: ReadonlyMap<string, FoldedRun>
  /** Child run ids drawn expanded; every other child is collapsed. */
  expanded?: ReadonlySet<string>
  /** Child runs' agents, by run id (the run document's children): a
   * child row no span or fold names reads its agent, else its tool's
   * name — one label rule on both scales and both surfaces. */
  agents?: ReadonlyMap<string, string>
  /** The descendant-span rules (lib/waterfall-tree.ts): Studio's run
   * page passes them (treeRows) for its ?tree=1 read; without them a
   * descendant's spans link no tool and a collapsed child claims none. */
  descend?: Descend
}

export interface WaterfallModel {
  scale: Scale
  rows: WaterfallRow[]
  /** [0, the latest end] on the scale. */
  domain: [number, number]
  /** Spans not placed: unreadable times, or an end before the start. */
  unplaced: number
}

// ── Times ──────────────────────────────────────────────────────────

/** An RFC 3339 instant in epoch ms, keeping the sub-millisecond digits
 * Date.parse drops (a 15µs model call must not read 0) — to about a
 * quarter of a microsecond, a double's grain at epoch scale. NaN when it
 * does not parse. */
export function instantMs(iso: string): number {
  const m = /^(.*T\d\d:\d\d:\d\d)(?:\.(\d+))?(.*)$/.exec(iso)
  if (!m) return Date.parse(iso)
  const base = Date.parse(m[1] + m[3])
  const frac = m[2] ? Number(`0.${m[2]}`) * 1000 : 0
  return base + frac
}

/** A row's extent as text: its duration on the time scale, its
 * position range on the order scale. */
export function extent(row: WaterfallRow, scale: Scale): string {
  if (row.untimed) return "—"
  if (scale === "time") return row.took === null ? "…" : took(row.took)
  return `#${pos(row.start)}–${row.took === null ? "" : pos(row.start + row.took)}`
}

/** A stream position as text: an expanded child's positions are scaled
 * into its tool's range, so they keep one decimal at most. */
const pos = (v: number) => +v.toFixed(1)

/** The earliest parseable start, in epoch ms; NaN when none parses.
 * A loop, not Math.min(...spread): a large trace overflows the
 * argument stack. */
export function earliestStart(timed: readonly TimedSpan[]): number {
  let t0 = NaN
  for (const s of timed) {
    const t = instantMs(s.start)
    if (Number.isFinite(t) && !(t >= t0)) t0 = t
  }
  return t0
}

/** Marks the critical path (the chain that set the run's length):
 * from a row's end backwards, the child that ends last, then the one
 * that ended last before that child started, and so on — serial steps
 * all mark, of overlapping calls only the one that ended last; then
 * the same inside each marked child. */
function markCritical(root: Node) {
  for (const st = [root]; st.length;) {
    const n = st.pop()!
    n.critical = true
    const kids = [...n.kids].sort((a, b) => endOf(b) - endOf(a))
    let bound = Infinity
    for (const k of kids)
      if (endOf(k) <= bound) {
        st.push(k)
        bound = k.start
      }
  }
}

/** An attempt's key from its model call's key. */
export const attemptKey = (model: string, n: number) =>
  `${model.replace(/:m$/, "")}:a${n}`

type Keys = ReturnType<typeof rowKeys>

// ── The generic span tree ──────────────────────────────────────────

export interface TreeNode {
  span: TimedSpan
  /** The parent's span id, when the parent is in the set. */
  parent?: string
  depth: number
}

/** A span's identity: span ids are unique within a trace only. */
export const spanRef = (s: TimedSpan, id = s.span_id) => `${s.trace_id}/${id}`

// ── Attributes ─────────────────────────────────────────────────────

/** A span's attributes; {} for a span that arrived without any (a
 * hand-built or foreign one: the type promises them, the wire may not). */
export function attrsOf(s: TimedSpan | undefined): Record<string, unknown> {
  return (s as { attrs?: Record<string, unknown> } | undefined)?.attrs ?? {}
}

/** The span's GenAI operation; the name's first word for spans that
 * carry none ("invoke_agent planner"). */
export function opOf(s: TimedSpan): string {
  return str(attrsOf(s)["gen_ai.operation.name"]) ?? s.name.split(" ")[0]
}

/** The time axis's domain: [0, the latest end] in milliseconds. */
export function timeDomain(timed: readonly TimedSpan[]): [number, number] {
  const t0 = earliestStart(timed)
  if (!Number.isFinite(t0)) return [0, 1]
  let hi = 1
  for (const s of timed) {
    const end = instantMs(s.end)
    if (Number.isFinite(end) && end - t0 > hi) hi = end - t0
  }
  return [0, hi]
}

function stepGist(names: string[], text: string | undefined): string {
  if (names.length)
    return `${plural(names.length, "tool")}: ${names.join(", ")}`
  return text ? quote(text) : ""
}

export const str = (v: unknown) => (typeof v === "string" ? v : undefined)

/** The first finish reason: a list, or the list as a JSON string (the
 * ClickHouse fallback map), or one reason. */
function finishOf(v: unknown): string | undefined {
  if (typeof v === "string" && v.startsWith("["))
    try {
      v = JSON.parse(v)
    } catch {
      /* the string itself */
    }
  return Array.isArray(v) ? str(v[0]) : str(v)
}

/** A boolean attribute; ClickHouse's fallback map stores "true". */
export const bool = (v: unknown) => v === true || v === "true"

/** A resumed (approved, before step 0's model call) or denied
 * (zero-length, at the resume) call: the run's own, keyed "resume". */
export const resumedCall = (a: Record<string, unknown>) =>
  bool(a["weft.tool.approved"]) || bool(a["weft.tool.denied"])

// ── Keys ───────────────────────────────────────────────────────────

/**
 * rowKeys is THE selection-key scheme: the run tree's rows here and the
 * trace page's rows (lib/trace-sel.ts) take their keys from it, so a
 * /traces ?sel= names the same row on the run page. The root run's keys
 * are bare, every other run's run-qualified:
 *
 *   r:<run>                      the run (its invoke_agent span)
 *   s<step>        s:<run>:<step>           the step
 *   s<step>:m      s:<run>:<step>:m         its model call (the first
 *                                           by start; a second :m2, …)
 *   s<step>:a<n>   s<step>:m2:a<n>          an attempt of that call
 *   c:<step>:<call>  c:<run>:<step>:<call>  a tool execution
 *   c:resume:<call>  c:<run>:resume:<call>  a resumed or denied call
 *   t:<span>       t:<run>:<span>           any other span
 *
 * Old forms (a chat span keyed s<step>, a root run qualified, a denied
 * call keyed by its step) still resolve: lib/trace-sel.ts's resolveSel.
 */
export function rowKeys(run: string, qualify: boolean) {
  const q = qualify ? `${run}:` : ""
  const step = (n: number | string) => (qualify ? `s:${q}${n}` : `s${n}`)
  return {
    step,
    model: (n: number | string, i = 0) => `${step(n)}:m${i ? i + 1 : ""}`,
    call: (at: number | string, id: string) => `c:${q}${at}:${id}`,
    span: (id: string) => `t:${q}${id}`,
  }
}

export function int(v: unknown): number | undefined {
  if (typeof v === "number" && Number.isInteger(v)) return v
  if (typeof v === "string" && /^\d+$/.test(v)) return Number(v)
  return undefined
}

// ── Gists ──────────────────────────────────────────────────────────

const GIST = 60

const quote = (s: string) => `“${clip(s, GIST)}”`

/** A ToolError renders "CODE: message" — the code is the error. */
const CODED = /^([A-Z][A-Z0-9_]+): /

/** A duration in ms in the tightest unit that reads: 15µs, 9.5ms,
 * 120ms, 7.7s (lib/format.ts's spanMs from 100ms up). The unit is
 * chosen on the rounded value: 999.6µs reads 1.0ms, 99.96ms 100ms. */
export function took(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—"
  const us = Math.round(ms * 1000)
  if (us < 1000) return `${us}µs`
  const r = Math.round(ms * 10) / 10
  return r < 100 ? `${r.toFixed(1)}ms` : spanMs(ms)
}

// ── Rows ───────────────────────────────────────────────────────────

interface Node extends WaterfallRow {
  kids: Node[]
}

type Base = Omit<WaterfallRow, "depth" | "critical">

const node = (r: Base): Node => ({ ...r, depth: 0, critical: false, kids: [] })

// The row constructors both scales share: the label rule and the gists
// live here once.
const runRow = (
  run: string,
  isChild: boolean,
  agent: string | undefined,
  steps: number | undefined,
  start: number,
  dur: number | null,
  status: RowStatus
) =>
  node({
    key: `r:${run}`,
    kind: isChild ? "child" : "run",
    label: agent || UNNAMED_AGENT,
    gist: steps === undefined ? undefined : stepCount(steps),
    runId: run,
    start,
    took: dur,
    status,
  })

const stepRow = (
  k: Keys,
  run: string,
  n: number,
  names: string[],
  text: string | undefined,
  start: number,
  dur: number | null,
  status: RowStatus,
  finish?: string
) =>
  node({
    key: k.step(n),
    kind: "step",
    label: `step ${n}`,
    gist: stepGist(names, text),
    runId: run,
    step: n,
    start,
    took: dur,
    status,
    finish,
  })

const modelRow = (
  key: string,
  run: string,
  n: number,
  model: string | undefined,
  calls: number | undefined,
  text: string | undefined,
  start: number,
  dur: number | null,
  status: RowStatus,
  finish?: string,
  ttft?: number,
  error?: string
) =>
  node({
    key,
    kind: "model",
    label: model ? `model · ${model}` : "model",
    gist: calls ? `→ ${plural(calls, "call")}` : text ? quote(text) : undefined,
    runId: run,
    step: n,
    start,
    took: dur,
    status,
    finish,
    ttft,
    error,
  })

const toolRow = (
  key: string,
  run: string,
  step: number | undefined,
  callId: string | undefined,
  name: string,
  call: FoldedToolCall | undefined,
  bytes: unknown,
  start: number,
  dur: number | null,
  status: RowStatus,
  error?: string
) =>
  node({
    key,
    kind: "tool",
    label: name,
    // Content-off, the result's text is empty: its size (metadata,
    // kept content-off) says what came back.
    gist: call?.result?.content
      ? clip(call.result.content, GIST)
      : int(bytes) !== undefined
        ? `${int(bytes)} B`
        : undefined,
    runId: run,
    step,
    callId,
    start,
    took: dur,
    status,
    error:
      error ??
      (call?.result?.isError
        ? (CODED.exec(call.result.content)?.[1] ?? "error")
        : undefined),
  })

function spanStatus(s: TimedSpan): RowStatus {
  if (s.status === "error") return "error"
  return bool(attrsOf(s)["weft.tool.pending"]) ? "pending" : "ok"
}

const endOf = (n: WaterfallRow) =>
  n.took === null ? Infinity : n.start + n.took

/**
 * spanTree orders spans as a tree: a span, then its whole subtree,
 * siblings by start, whatever order the API returned. A span whose
 * parent is not in the set (the same trace's) is a root; a parent
 * chain that loops (a span naming itself, two naming each other —
 * ingest stores what it is sent) is cut where the walk would revisit,
 * so every span appears exactly once and no consumer can loop on
 * `parent`. Iterative: a deep trace must not overflow the stack.
 */
export function spanTree(timed: readonly TimedSpan[]): TreeNode[] {
  const t0 = earliestStart(timed)
  const startOf = (s: TimedSpan) => {
    const t = instantMs(s.start)
    return Number.isFinite(t) && Number.isFinite(t0) ? t - t0 : 0
  }
  const byID = new Map<string, TimedSpan>()
  for (const s of timed) if (!byID.has(spanRef(s))) byID.set(spanRef(s), s)
  const kids = new Map<string, TimedSpan[]>()
  const roots: TimedSpan[] = []
  for (const s of timed) {
    const p = s.parent_span_id && spanRef(s, s.parent_span_id)
    if (p && s.parent_span_id !== s.span_id && byID.has(p)) {
      const list = kids.get(p)
      if (list) list.push(s)
      else kids.set(p, [s])
    } else roots.push(s)
  }
  const byStart = (a: TimedSpan, b: TimedSpan) => startOf(a) - startOf(b)
  const out: TreeNode[] = []
  const placed = new Set<TimedSpan>()
  const walk = (start: TimedSpan[]) => {
    const stack: TreeNode[] = [...start]
      .sort(byStart)
      .reverse()
      .map((span) => ({ span, depth: 0 }))
    for (let n = stack.pop(); n; n = stack.pop()) {
      if (placed.has(n.span)) continue
      placed.add(n.span)
      out.push(n)
      const children = (kids.get(spanRef(n.span)) ?? []).slice().sort(byStart)
      for (let i = children.length - 1; i >= 0; i--)
        stack.push({
          span: children[i],
          parent: n.span.span_id,
          depth: n.depth + 1,
        })
    }
  }
  walk(roots)
  // Whatever no root reaches sits on a parent cycle: each remaining
  // span opens its own tree (the cycle is cut at the revisit).
  for (const s of timed) if (!placed.has(s)) walk([s])
  return out
}

/** The rows in tree order. A key met twice (a call id a step repeats,
 * spans ingest stored twice) yields to the span's own t: key, else a
 * counter: keys stay unique, as a selection needs. */
function flatten(root: Node, rootRun: string): WaterfallRow[] {
  const out: WaterfallRow[] = []
  const seen = new Set<string>()
  const stack: [Node, number, string | undefined][] = [[root, 0, undefined]]
  for (let it = stack.pop(); it; it = stack.pop()) {
    const [n, depth, parent] = it
    const { kids, ...row } = n
    let key = row.key
    if (seen.has(key) && row.span)
      key = rowKeys(row.runId, row.runId !== rootRun).span(row.span.span_id)
    for (let i = 2; seen.has(key); i++) key = `${row.key}#${i}`
    seen.add(key)
    out.push({ ...row, key, depth, ...(parent ? { parent } : {}) })
    const sorted = [...kids].sort((a, b) => a.start - b.start)
    for (let i = sorted.length - 1; i >= 0; i--)
      stack.push([sorted[i], depth + 1, key])
  }
  return out
}

// ── The time scale ─────────────────────────────────────────────────

export interface Placed {
  span: TimedSpan
  start: number
  took: number
}

/** A tree node as the time scale builds it (a row with its kids). */
export type TreeRowNode = Node

/** The descendant half of the time scale (U9.2; lib/waterfall-tree.ts's
 * descend): over the placed spans, the run's invoke_agent spans (by
 * run), the root run and the span → run rule, it says which tool row a
 * child run hangs under — by span (under: the tool span's ref → the
 * child's run) and, that span absent, by (run, call) and start time
 * (fallback) — and claims a collapsed child's spans (and its
 * descendants') into its one row. A run's own spans (the panel's read:
 * no ?tree=1) give it nothing to do, so the panel bundles none of it;
 * the rules are the run tree's, in one place either way. */
export type Descend = (
  placed: readonly Placed[],
  invoke: ReadonlyMap<string, Placed>,
  root: string,
  runOf: (p: Placed) => string,
  byRun: ReadonlyMap<string, readonly Placed[]>
) => {
  under: ReadonlyMap<string, string>
  fallback: (
    run: string,
    call: string | undefined,
    t: Placed
  ) => string | undefined
  claim: (
    run: string,
    claimed: Set<TimedSpan>,
    bySpan: Map<string, TreeRowNode | null>
  ) => void
}

/** Assigns lanes greedily: a call takes the first lane free at its
 * start. Sets the step's lane count (its widest concurrency). */
function assignLanes(step: Node) {
  const tools = step.kids.filter((t) => t.kind === "tool")
  if (!tools.length) return
  const ends: number[] = []
  for (const t of tools.sort((a, b) => a.start - b.start)) {
    let i = ends.findIndex((e) => e <= t.start)
    if (i < 0) i = ends.push(0) - 1
    ends[i] = endOf(t)
    t.lane = i + 1
  }
  step.lanes = ends.length
}

/**
 * waterfallRows builds the run tree on a scale: `time` when asked, or
 * by default when any span can be placed; else `order`. Rows come out
 * in tree order (a row, then its subtree, siblings by start), with
 * depth and parent keys; a collapsed child contributes its one row.
 */
export function waterfallRows(
  input: WaterfallInput,
  scale?: Scale
): WaterfallModel {
  return (scale ??
    ((input.spans ?? []).some((s) => place(s, 0)) ? "time" : "order")) ===
    "time"
    ? timeRows(input)
    : orderRows(input)
}

function timeRows(input: WaterfallInput): WaterfallModel {
  const spans = input.spans ?? []
  const t0 = earliestStart(spans)
  const placed: Placed[] = []
  for (const s of spans) {
    const p = place(s, t0)
    if (p) placed.push(p)
  }
  const unplaced = spans.length - placed.length
  let hi = 0
  for (const p of placed) hi = Math.max(hi, p.start + p.took)
  const domain: [number, number] = [0, hi > 0 ? hi : 1]
  if (!placed.length) return { scale: "time", rows: [], domain, unplaced }
  const live = input.status === "running"

  // A derived run's stock spans carry no weft.run.id (the reader adds
  // the step and seq, docs/record-contract.md §9): with none in the
  // set, every span is the run's own.
  const ids = placed.some((p) => str(attrsOf(p.span)["weft.run.id"]))
  const runOf = (p: Placed) =>
    ids ? (str(attrsOf(p.span)["weft.run.id"]) ?? "") : input.runId
  const byRun = new Map<string, Placed[]>()
  for (const p of placed) {
    const r = runOf(p)
    const list = byRun.get(r)
    if (list) list.push(p)
    else byRun.set(r, [p])
  }
  const invoke = new Map<string, Placed>()
  for (const p of placed)
    if (opOf(p.span) === "invoke_agent" && !invoke.has(runOf(p)))
      invoke.set(runOf(p), p)
  // Descendant runs' spans (?tree=1): which tool each child hangs
  // under, and a collapsed child's spans claimed — input.descend
  // (lib/waterfall-tree.ts). A run's own spans need neither.
  const ds = input.descend?.(placed, invoke, input.runId, runOf, byRun)
  // A chat span's attempts, by the chat span: grouped once (a run of
  // thousands of steps must not scan every span per model call).
  const attemptsOf = new Map<string, Placed[]>()
  for (const p of placed)
    if (opOf(p.span) === "attempt") {
      const k = spanRef(p.span, p.span.parent_span_id)
      const list = attemptsOf.get(k)
      if (list) list.push(p)
      else attemptsOf.set(k, [p])
    }

  const bySpan = new Map<string, Node | null>()
  const claimed = new Set<TimedSpan>()
  const nodeOf = (p: Placed, n: Node) => {
    n.span = p.span
    bySpan.set(spanRef(p.span), n)
    claimed.add(p.span)
    return n
  }

  const built = new Set<string>()
  const claimRun = (run: string) => ds?.claim(run, claimed, bySpan)
  const buildRun = (
    run: string,
    isChild: boolean,
    at?: {
      start: number
      took: number | null
      status: RowStatus
      label?: string
    }
  ): Node => {
    built.add(run)
    const own = byRun.get(run) ?? []
    const fold = isChild ? input.children?.get(run) : input.fold
    const k = rowKeys(run, isChild)
    const inv = invoke.get(run)
    let lo = Infinity
    let end = -Infinity
    for (const p of own) {
      lo = Math.min(lo, p.start)
      end = Math.max(end, p.start + p.took)
    }
    // Without its invoke_agent span (live, or not exported): while the
    // run goes on, open; else the extent of what it has, else its
    // tool's (a child) or the whole domain (the root).
    const going = !inv && live && !(isChild && fold?.finished)
    const ia = attrsOf(inv?.span)
    const root = runRow(
      run,
      isChild,
      str(ia["gen_ai.agent.name"]) ??
        inv?.span.name.replace(/^invoke_agent\s*/, "") ??
        (fold?.agent || input.agents?.get(run) || at?.label),
      int(ia["weft.run.steps"]) ?? fold?.steps.length,
      inv?.start ?? (lo < Infinity ? lo : (at?.start ?? 0)),
      inv
        ? inv.took
        : going
          ? null
          : lo < Infinity
            ? end - lo
            : at
              ? at.took
              : hi,
      inv
        ? spanStatus(inv.span)
        : going
          ? "running"
          : isChild
            ? (at?.status ?? "never")
            : input.status === "failed"
              ? "error"
              : "ok"
    )
    if (inv) nodeOf(inv, root)
    const expanded = !isChild || !!input.expanded?.has(run)
    if (isChild)
      root.child = { runId: run, collapsed: !expanded, loaded: own.length > 0 }
    if (!expanded) {
      claimRun(run)
      return root
    }
    const kid = (t: Node, id: string | undefined) => {
      // A run is built once: parent links that loop end here.
      if (id && !built.has(id)) t.kids.push(buildRun(id, true, t))
    }
    // The fold's steps by index and its resumed calls by call id, once.
    const foldSteps = new Map<number, FoldedStep>()
    const resumed = new Map<string, FoldedToolCall>()
    for (const fs of fold?.steps ?? []) {
      if (!foldSteps.has(fs.index)) foldSteps.set(fs.index, fs)
      for (const c of fs.toolCalls)
        if (c.resumed && !resumed.has(c.callId)) resumed.set(c.callId, c)
    }
    // Steps: the chat spans and the execute_tool spans of an index.
    const stepsAt = new Map<number, { chats: Placed[]; tools: Placed[] }>()
    const seen = new Set<string>()
    for (const p of own) {
      const op = opOf(p.span)
      const a = attrsOf(p.span)
      const n = int(a["weft.step.index"])
      if (n === undefined || (op !== "chat" && op !== "execute_tool")) continue
      if (op === "execute_tool" && resumedCall(a))
        root.kids.push(toolNode(p, "resume"))
      else {
        let s = stepsAt.get(n)
        if (!s) stepsAt.set(n, (s = { chats: [], tools: [] }))
        ;(op === "chat" ? s.chats : s.tools).push(p)
      }
    }
    function toolNode(p: Placed, where: string | number): Node {
      const a = attrsOf(p.span)
      const callId = str(a["gen_ai.tool.call.id"])
      const step = int(a["weft.step.index"])
      seen.add(`${where}\n${callId}`)
      const call = callId
        ? where === "resume"
          ? resumed.get(callId)
          : step === undefined
            ? undefined
            : foldSteps
                .get(step)
                ?.toolCalls.find((c) => c.callId === callId && !c.resumed)
        : undefined
      const t = nodeOf(
        p,
        toolRow(
          callId ? k.call(where, callId) : k.span(p.span.span_id),
          run,
          step,
          callId,
          str(a["gen_ai.tool.name"]) ??
            p.span.name.replace(/^execute_tool\s*/, ""),
          call,
          a["weft.tool.result_bytes"],
          p.start,
          p.took,
          spanStatus(p.span),
          str(a["error.type"])
        )
      )
      kid(
        t,
        ds?.under.get(spanRef(p.span)) ??
          call?.childRunId ??
          ds?.fallback(run, callId, p)
      )
      return t
    }
    const steps = new Map<number, [Node, number]>()
    const byStart = (a: Placed, b: Placed) => a.start - b.start
    for (const [n, s] of [...stepsAt].sort((a, b) => a[0] - b[0])) {
      const fstep = foldSteps.get(n)
      let a = Infinity
      let b = -Infinity
      for (const p of [...s.chats, ...s.tools]) {
        a = Math.min(a, p.start)
        b = Math.max(b, p.start + p.took)
      }
      // Two model calls in a step (re-run after an error): by start;
      // the step reads the last one's status and finish.
      s.chats.sort(byStart)
      const last = s.chats.at(-1)
      const finish =
        finishOf(attrsOf(last?.span)["gen_ai.response.finish_reasons"]) ??
        fstep?.finish?.reason
      const open = live && !!fstep && !fstep.finish
      const step = stepRow(
        k,
        run,
        n,
        s.tools.map(
          (p) => str(attrsOf(p.span)["gen_ai.tool.name"]) ?? p.span.name
        ),
        fstep?.text,
        a,
        open ? null : b - a,
        open ? "running" : last ? spanStatus(last.span) : "ok",
        finish
      )
      root.kids.push(step)
      steps.set(n, [step, b])
      s.chats.forEach((p, i) => {
        const pa = attrsOf(p.span)
        const m = nodeOf(
          p,
          modelRow(
            k.model(n, i),
            run,
            n,
            str(pa["gen_ai.response.model"]) ??
              str(pa["gen_ai.request.model"]) ??
              p.span.name.replace(/^chat\s*/, ""),
            int(pa["weft.model.tool_calls"]),
            fstep?.text,
            p.start,
            p.took,
            spanStatus(p.span),
            finishOf(pa["gen_ai.response.finish_reasons"]) ??
              (p === last ? finish : undefined),
            int(pa["weft.ttft_ms"]),
            str(pa["error.type"])
          )
        )
        // Its attempts: the attempt spans under the chat span.
        const tries = attemptsOf.get(spanRef(p.span)) ?? []
        if (tries.length) m.attempts = tries.length
        for (const x of tries) {
          const xa = attrsOf(x.span)
          const idx = int(xa["weft.attempt.index"])
          const xm =
            str(xa["gen_ai.response.model"]) ?? str(xa["gen_ai.request.model"])
          m.kids.push(
            nodeOf(
              x,
              node({
                key:
                  idx !== undefined
                    ? attemptKey(m.key, idx)
                    : k.span(x.span.span_id),
                kind: "attempt",
                label: `attempt${idx !== undefined ? ` ${idx}` : ""}${xm ? ` · ${xm}` : ""}`,
                runId: run,
                step: n,
                start: x.start,
                took: x.took,
                status: spanStatus(x.span),
                error: str(xa["error.type"]),
              })
            )
          )
        }
        step.kids.push(m)
      })
      step.kids.push(...s.tools.map((p) => toolNode(p, n)))
    }
    // The fold's steps and calls no span covers (U8.2-fix). While the
    // run goes on, an open one runs from the run's latest end (a step)
    // or its step's (a call), and a finished one waits for its spans
    // (`pending`: they export when they end, the page reads them every
    // few seconds); once it ended, they are `missing` (dropped on the
    // way, never exported) — drawn, never left out: truncation is
    // visible. An untimed row sits after the row before it, no duration.
    if (fold) {
      const edge = end > -Infinity ? end : root.start
      const inFlight = live && !fold.finished
      const mark = live ? "pending" : "missing"
      const timed = [...steps].sort((a, b) => a[0] - b[0])
      let prev = root.start
      let j = 0
      for (const fs of [...fold.steps].sort((a, b) => a.index - b.index)) {
        for (; j < timed.length && timed[j][0] < fs.index; j++)
          prev = Math.max(prev, timed[j][1][1])
        let st = steps.get(fs.index)
        if (!st) {
          const open = inFlight && !fs.finish
          const from = open ? edge : prev
          const status: RowStatus = open
            ? "running"
            : fs.finish
              ? "ok"
              : "never"
          const s = stepRow(
            k,
            run,
            fs.index,
            fs.toolCalls.filter((c) => !c.resumed).map((c) => c.name),
            fs.text,
            from,
            open ? null : 0,
            status,
            fs.finish?.reason
          )
          if (!open) s.untimed = mark
          if (!open || !fs.toolCalls.length) {
            const m = modelRow(
              k.model(fs.index),
              run,
              fs.index,
              fold.model?.name,
              open ? 0 : fs.toolCalls.filter((c) => !c.resumed).length,
              fs.text,
              from,
              open ? null : 0,
              status,
              fs.finish?.reason
            )
            if (!open) m.untimed = mark
            s.kids.push(m)
          }
          root.kids.push(s)
          steps.set(fs.index, (st = [s, from]))
        }
        for (const c of fs.toolCalls) {
          const where = c.resumed ? "resume" : fs.index
          if (seen.has(`${where}\n${c.callId}`)) continue
          const open = inFlight && c.finishPos === undefined
          const t = toolRow(
            k.call(where, c.callId),
            run,
            fs.index,
            c.callId,
            c.name,
            c,
            undefined,
            c.resumed ? (open ? edge : root.start) : st[1],
            open ? null : 0,
            open
              ? "running"
              : c.finishPos === undefined
                ? "never"
                : c.result?.isError
                  ? "error"
                  : "ok"
          )
          // A max_tokens step's call the loop never ran (rule 11) has
          // no span by design: "not executed", never a loss.
          if (!open) t.untimed = c.unrun ? "unrun" : mark
          ;(c.resumed ? root : st[0]).kids.push(t)
          kid(t, c.childRunId)
        }
      }
    }
    for (const [st] of steps.values()) assignLanes(st)
    return root
  }

  const root = buildRun(input.runId, false)
  // Every span weft does not name hangs under its parent span's row
  // (in tree order, so a parent's node exists first); a span inside a
  // collapsed child stays hidden; anything else under the root. A run
  // nothing placed yet (a nested Generate's, a live delegation whose
  // tool has not ended) is a child row where its first span sits.
  const byPlaced = new Map(placed.map((p) => [p.span, p]))
  for (const t of spanTree(placed.map((p) => p.span))) {
    if (claimed.has(t.span)) continue
    const p = byPlaced.get(t.span)!
    const r = runOf(p)
    const parent =
      t.parent !== undefined ? bySpan.get(spanRef(t.span, t.parent)) : undefined
    if (parent === null) {
      bySpan.set(spanRef(t.span), null)
      if (r && !built.has(r)) claimRun(r)
      continue
    }
    if (r && !built.has(r)) {
      ;(parent ?? root).kids.push(
        buildRun(r, true, {
          start: p.start,
          took: null,
          status: live ? "running" : "never",
        })
      )
      if (claimed.has(t.span)) continue
    }
    ;(parent ?? root).kids.push(
      nodeOf(
        p,
        node({
          key: rowKeys(r, !!r && r !== input.runId).span(t.span.span_id),
          kind: "span",
          label: t.span.name,
          runId: r,
          start: p.start,
          took: p.took,
          status: spanStatus(t.span),
        })
      )
    )
  }
  markCritical(root)
  return { scale: "time", rows: flatten(root, input.runId), domain, unplaced }
}

/** A span on the axis: offset from t0 and duration, or undefined when
 * its times do not read (unparseable, an end before the start). */
function place(span: TimedSpan, t0: number): Placed | undefined {
  const a = instantMs(span.start)
  const b = instantMs(span.end)
  return Number.isFinite(a) && Number.isFinite(b) && b >= a
    ? { span, start: a - t0, took: b - a }
    : undefined
}

// ── The order scale ────────────────────────────────────────────────

function orderRows(input: WaterfallInput): WaterfallModel {
  const fold = input.fold
  const status = input.status ?? ""
  const running = status === "running"
  const total = Math.max(0, (input.total ?? 0) - 1)
  if (!fold) return { scale: "order", rows: [], domain: [0, 1], unplaced: 0 }
  const span = (f: FoldedRun) => {
    const a = f.startPos ?? f.steps.at(0)?.from ?? 0
    let b = f.finishPos ?? a
    for (const s of f.steps) b = Math.max(b, s.to)
    return [a, b]
  }

  // A child's positions are its own stream's: drawn inside its tool's
  // range, scaled linearly (map).
  const buildRun = (
    f: FoldedRun,
    isChild: boolean,
    map: (p: number) => number
  ): Node => {
    const run = f.runId
    const k = rowKeys(run, isChild)
    const [first, last] = span(f)
    const a = isChild ? map(first) : 0
    const b = isChild ? map(last) : f.finished ? Math.max(last, total) : last
    const root = runRow(
      run,
      isChild,
      f.agent,
      f.steps.length,
      a,
      f.finished || !running ? b - a : null,
      f.finished
        ? "ok"
        : running
          ? "running"
          : status === "failed"
            ? "error"
            : "never"
    )
    const callNode = (
      c: FoldedToolCall,
      at: number | string,
      step?: number
    ) => {
      const open = c.finishPos === undefined
      const t = toolRow(
        k.call(at, c.callId),
        run,
        step,
        c.callId,
        c.name,
        c,
        undefined,
        map(c.startPos),
        open ? (running ? null : 0) : map(c.finishPos!) - map(c.startPos),
        open
          ? running
            ? "running"
            : "never"
          : c.result?.isError
            ? "error"
            : "ok"
      )
      const id = c.childRunId
      if (id) {
        const expanded = !!input.expanded?.has(id)
        const cf = input.children?.get(id)
        const lo = t.start
        const hi = t.took === null ? map(total) : t.start + t.took
        let child: Node
        if (expanded && cf) {
          const [c0, c1] = span(cf)
          const w = Math.max(1, c1 - c0)
          child = buildRun(cf, true, (p) => lo + ((p - c0) / w) * (hi - lo))
        } else
          child = runRow(
            id,
            true,
            cf?.agent || input.agents?.get(id) || c.name,
            cf?.steps.length,
            lo,
            t.took,
            t.status
          )
        child.child = { runId: id, collapsed: !expanded, loaded: !!cf }
        t.kids.push(child)
      }
      return t
    }
    for (const s of f.steps) {
      const open = !s.finish && running
      const own = s.toolCalls.filter((c) => !c.resumed)
      for (const c of s.toolCalls)
        if (c.resumed) root.kids.push(callNode(c, "resume"))
      const st = stepRow(
        k,
        run,
        s.index,
        own.map((c) => c.name),
        s.text,
        map(s.from),
        open ? null : map(s.to) - map(s.from),
        s.finish
          ? "ok"
          : open
            ? "running"
            : status === "failed"
              ? "error"
              : "never",
        s.finish?.reason
      )
      root.kids.push(st)
      // The model call: from the step's start to its first tool call
      // (the stream says no more on positions).
      const mEnd = own.length ? own[0].startPos : s.finish ? s.to : undefined
      st.kids.push(
        modelRow(
          k.model(s.index),
          run,
          s.index,
          f.model?.name,
          own.length,
          s.text,
          map(s.from),
          mEnd === undefined ? null : map(mEnd) - map(s.from),
          st.status,
          s.finish?.reason,
          s.finish?.ttftMs
        ),
        ...own.map((c) => callNode(c, s.index, s.index))
      )
      assignLanes(st)
    }
    return root
  }

  const root = buildRun(fold, false, (p) => p)
  markCritical(root)
  const rows = flatten(root, fold.runId)
  let hi = total
  for (const r of rows) hi = Math.max(hi, r.start + (r.took ?? 0))
  return { scale: "order", rows, domain: [0, Math.max(1, hi)], unplaced: 0 }
}

// ── Words ──────────────────────────────────────────────────────────
// The renderers' words for a row (U8.2): the badge, the critical path,
// the lanes, the child's link — one place, both surfaces.

/** A row's badge: the error's kind; an open row's state (running,
 * pending, never); a finished step or model call's finish reason; "ok"
 * on a tool or child run; nothing on a finished root (the header says
 * how the run ended). */
export function rowBadge(r: WaterfallRow): string | undefined {
  if (r.status === "error") return r.error ?? "error"
  if (r.status !== "ok") return r.status
  if (r.kind === "step" || r.kind === "model") return r.finish
  return r.kind === "tool" || r.kind === "child" ? "ok" : undefined
}

/** The kind each renderer draws a row as (the Waterfall's SpanKind: a
 * child run is a subagent row), its glyph, and its tone (U8.3): Studio's
 * run tree and the panel's Timeline read the same three tables, so a
 * row is the same kind and colour on both (the panel's colours are
 * lib/palette.ts's TONE). */
export const ROW_KIND: Record<RowKind, SpanKind> = {
  run: "run",
  child: "subagent",
  step: "step",
  model: "model",
  attempt: "attempt",
  tool: "tool",
  span: "span",
}
export const KIND_ICON: Record<SpanKind, string> = {
  run: "▣",
  step: "↻",
  tool: "⚙",
  subagent: "↳",
  model: "◆",
  attempt: "◇",
  span: "○",
}
export function rowTone(r: WaterfallRow): SpanTone {
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

/** The critical path's legend and hint. */
export const CRITICAL = "critical path"
export const CRITICAL_HINT =
  "the chain of rows that set the run's length: of overlapping calls, the one that ended last"

/** A step's lanes when it ran calls side by side ("2 lanes"); one lane
 * says nothing. */
export const lanesWord = (n: number | undefined) =>
  n && n > 1 ? `${n} lanes` : ""

/** A child run nested deeper than its indent shows (UX-D-13: the
 * panel's Story stops indenting past two levels and names the depth). */
export const depthWord = (n: number) => `depth ${n}`

/** An untimed row's mark while the run goes on (not a loss: its spans
 * export when they end). */
export const UNTIMED_PENDING = "spans not reported yet"
export const UNTIMED_PENDING_HINT =
  "its spans export when they end; the page reads them every few seconds"

/** An untimed row's words that are no loss, by its mark: a pending
 * row's, and a call the loop never ran (rule 11: its step hit the
 * output token limit; no span by design). Both renderers. */
export const UNTIMED_NOTE: Record<"pending" | "unrun", [string, string]> = {
  pending: [UNTIMED_PENDING, UNTIMED_PENDING_HINT],
  unrun: [
    NOT_EXECUTED,
    "its step hit the output token limit: the loop ran none of its calls (rule 11), so no span covers it by design",
  ],
}

/** An untimed row's badge once the run ended: the A3 table's
 * not_recorded, in this instance's words (lib/honesty.ts's HoleMark). */
export const UNTIMED_HOLE = {
  hole: "not_recorded",
  reason:
    "no span covers this row: its spans were dropped (weft.records.dropped) or never exported",
  fix: 'on "weft/otel: destination queue full", raise otel.QueueSize(n)',
} as const

/** A child run row's link to its own page. */
export const OPEN_RUN = "open run ↗"

/** The scale switch's words and hints. */
export const SCALE_HINT: Record<Scale, string> = {
  time: "time: real durations from the run's spans",
  order: "order: stream positions, and the replay playhead",
}

// ── Filters ────────────────────────────────────────────────────────

/** The dim filters (§6.1): rows that do not match dim, never hide. */
export interface Dim {
  /** Keep the rows in error (and the rows above them). */
  errors?: boolean
  /** Keep the rows that took at least a fifth of the root's time. */
  slow?: boolean
  /** Keep tool and child-run rows. */
  tools?: boolean
}

/** The filters in the chip row's order, with their words and hints. */
export const DIMS = ["errors", "slow", "tools"] as const
export type DimKey = (typeof DIMS)[number]
export const DIM_WORDS: Record<DimKey, { label: string; hint: string }> = {
  errors: {
    label: "errors",
    hint: "dim every row but the failing ones and the rows above them",
  },
  slow: {
    label: "slow",
    hint: "dim every row that took less than a fifth of the run",
  },
  tools: {
    label: "tools",
    hint: "dim every row but tool calls and child runs",
  },
}
/** The chip row's label. */
export const DIM_LABEL = "dim all but"

/** A ?dim= value read back: the filters it names, unknown names dropped. */
export function parseDim(v: unknown): Dim {
  const out: Dim = {}
  if (typeof v !== "string") return out
  for (const k of v.split(","))
    if ((DIMS as readonly string[]).includes(k)) out[k as DimKey] = true
  return out
}

/** The ?dim= value of a Dim, in DIMS's order; undefined with none on. */
export function dimParam(d: Dim): string | undefined {
  const on = DIMS.filter((k) => d[k])
  return on.length ? on.join(",") : undefined
}

/** dimRows returns the keys to dim: with no filter on, none; else
 * every row no active filter keeps — a kept row's ancestors stay lit,
 * so the path to it reads. */
export function dimRows(rows: readonly WaterfallRow[], dim: Dim): Set<string> {
  const out = new Set<string>()
  if (!dim.errors && !dim.slow && !dim.tools) return out
  const root = rows.at(0)
  const slow = root?.took ? root.took / 5 : Infinity
  const byKey = new Map(rows.map((r) => [r.key, r]))
  const lit = new Set<string>()
  for (const r of rows) {
    const keep =
      (dim.errors && r.status === "error") ||
      (dim.slow && r !== root && (r.took ?? Infinity) >= slow) ||
      (dim.tools && (r.kind === "tool" || r.kind === "child"))
    if (!keep) continue
    for (let p: WaterfallRow | undefined = r; p && !lit.has(p.key);)
      (lit.add(p.key), (p = p.parent ? byKey.get(p.parent) : undefined))
  }
  for (const r of rows) if (!lit.has(r.key)) out.add(r.key)
  return out
}
