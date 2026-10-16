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
import type { FoldedRun, FoldedToolCall } from "./events"
import { spanMs, stepCount } from "./format"

/** The axis: wall time from spans, or stream order from events. */
export type Scale = "time" | "order"

/** What a row stands for. `span` is any span weft does not name (a
 * foreign SDK's, an app's own) placed under the row of its parent. */
export type RowKind =
  "run" | "step" | "model" | "attempt" | "tool" | "child" | "span"

/** A row's state as a token; the words are the renderer's (lib). */
export type RowStatus = "ok" | "error" | "pending" | "running" | "never"

export interface WaterfallRow {
  /** Unique within the model; the selection key (lib/trace-sel.ts's
   * scheme): r:<run>, s<step>, s<step>:m (its model call), s<step>:a<n>
   * (an attempt), c:<step>:<call>, c:resume:<call>; a child run's rows
   * run-qualified (s:<run>:<step>, c:<run>:<step>:<call>); t:<span id>
   * for any other span. */
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

/** A duration in ms in the tightest unit that reads: 15µs, 9.5ms,
 * 120ms, 7.7s (lib/format.ts's spanMs from a second up). */
export function took(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—"
  if (ms < 1) return `${Math.round(ms * 1000)}µs`
  if (ms < 100) return `${ms.toFixed(1)}ms`
  return spanMs(ms)
}

/** A row's extent as text: its duration on the time scale, its
 * position range on the order scale. */
export function extent(row: WaterfallRow, scale: Scale): string {
  if (scale === "time") return row.took === null ? "…" : took(row.took)
  return row.took === null
    ? `#${row.start}–`
    : `#${row.start}–${row.start + row.took}`
}

// ── The generic span tree ──────────────────────────────────────────

export interface TreeNode {
  span: TimedSpan
  /** The parent's span id, when the parent is in the set. */
  parent?: string
  depth: number
}

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

/**
 * spanTree orders spans as a tree: a span, then its whole subtree,
 * siblings by start, whatever order the API returned. A span whose
 * parent is not in the set is a root; a parent chain that loops (a
 * span naming itself, two naming each other — ingest stores what it
 * is sent) is cut where the walk would revisit, so every span appears
 * exactly once and no consumer can loop on `parent`. Iterative: a
 * deep trace must not overflow the stack.
 */
export function spanTree(timed: readonly TimedSpan[]): TreeNode[] {
  const t0 = earliestStart(timed)
  const startOf = (s: TimedSpan) => {
    const t = instantMs(s.start)
    return Number.isFinite(t) && Number.isFinite(t0) ? t - t0 : 0
  }
  const byID = new Map<string, TimedSpan>()
  for (const s of timed) if (!byID.has(s.span_id)) byID.set(s.span_id, s)
  const kids = new Map<string, TimedSpan[]>()
  const roots: TimedSpan[] = []
  for (const s of timed) {
    const p = s.parent_span_id
    if (p && p !== s.span_id && byID.has(p)) {
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
      const children = (kids.get(n.span.span_id) ?? []).slice().sort(byStart)
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

// ── Gists ──────────────────────────────────────────────────────────

const GIST = 60

function clip(s: string): string {
  const one = s.replace(/\s+/g, " ").trim()
  return one.length > GIST ? `${one.slice(0, GIST - 1)}…` : one
}

const quote = (s: string) => `“${clip(s)}”`

const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"}`

/** A ToolError renders "CODE: message" — the code is the error. */
const CODED = /^([A-Z][A-Z0-9_]+): /

function stepGist(names: string[], text: string | undefined): string {
  if (names.length)
    return `${plural(names.length, "tool")}: ${names.join(", ")}`
  return text ? quote(text) : ""
}

function modelGist(calls: number | undefined, text: string | undefined) {
  if (calls) return `→ ${plural(calls, "call")}`
  return text ? quote(text) : undefined
}

function toolGist(call: FoldedToolCall | undefined, bytes?: unknown) {
  if (call?.result) return clip(call.result.content)
  return typeof bytes === "number" ? `${bytes} B` : undefined
}

// ── Building ───────────────────────────────────────────────────────

interface Node extends WaterfallRow {
  kids: Node[]
}

/** A span's attributes; {} for a span that arrived without any (a
 * hand-built or foreign one: the type promises them, the wire may not). */
function attrsOf(s: TimedSpan | undefined): Record<string, unknown> {
  return (s as { attrs?: Record<string, unknown> } | undefined)?.attrs ?? {}
}

const str = (v: unknown) => (typeof v === "string" ? v : undefined)

function int(v: unknown): number | undefined {
  if (typeof v === "number" && Number.isInteger(v)) return v
  if (typeof v === "string" && /^\d+$/.test(v)) return Number(v)
  return undefined
}

/** The span's GenAI operation; the name's first word for spans that
 * carry none ("invoke_agent planner"). */
function opOf(s: TimedSpan): string {
  return str(attrsOf(s)["gen_ai.operation.name"]) ?? s.name.split(" ")[0]
}

/** Assigns lanes greedily: a call takes the first lane free at its
 * start. Returns the lane count. */
function assignLanes(tools: Node[]): number {
  const ends: number[] = []
  for (const t of [...tools].sort((a, b) => a.start - b.start)) {
    const end = t.took === null ? Infinity : t.start + t.took
    let i = ends.findIndex((e) => e <= t.start)
    if (i < 0) i = ends.push(end) - 1
    else ends[i] = end
    t.lane = i + 1
  }
  return ends.length
}

const endOf = (n: WaterfallRow) =>
  n.took === null ? Infinity : n.start + n.took

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

/** The rows in tree order. A key met twice (a call id a step repeats,
 * spans ingest stored twice) yields to the span's own t:<span id>, else
 * a counter: keys stay unique, as a selection needs. */
function flatten(root: Node): WaterfallRow[] {
  const out: WaterfallRow[] = []
  const seen = new Set<string>()
  const stack: [Node, number, string | undefined][] = [[root, 0, undefined]]
  for (let it = stack.pop(); it; it = stack.pop()) {
    const [n, depth, parent] = it
    const { kids, ...row } = n
    let key = row.key
    if (seen.has(key) && row.span) key = `t:${row.span.span_id}`
    for (let i = 2; seen.has(key); i++) key = `${row.key}#${i}`
    seen.add(key)
    out.push({ ...row, key, depth, ...(parent ? { parent } : {}) })
    const sorted = [...kids].sort((a, b) => a.start - b.start)
    for (let i = sorted.length - 1; i >= 0; i--)
      stack.push([sorted[i], depth + 1, key])
  }
  return out
}

function node(r: Omit<WaterfallRow, "depth" | "critical">): Node {
  return { ...r, depth: 0, critical: false, kids: [] }
}

/** A run's key prefix: the root's rows bare, a child's run-qualified. */
function keys(runId: string, child: boolean) {
  return {
    step: (n: number) => (child ? `s:${runId}:${n}` : `s${n}`),
    call: (at: string, id: string) =>
      child ? `c:${runId}:${at}:${id}` : `c:${at}:${id}`,
  }
}

// ── The time scale ─────────────────────────────────────────────────

interface Placed {
  span: TimedSpan
  start: number
  took: number
}

function spanStatus(s: TimedSpan): RowStatus {
  if (s.status === "error") return "error"
  return attrsOf(s)["weft.tool.pending"] === true ? "pending" : "ok"
}

function timeRows(input: WaterfallInput): WaterfallModel {
  const spans = input.spans ?? []
  const t0 = earliestStart(spans)
  const placed: Placed[] = []
  for (const span of spans) {
    const a = instantMs(span.start)
    const b = instantMs(span.end)
    if (Number.isFinite(a) && Number.isFinite(b) && b >= a)
      placed.push({ span, start: a - t0, took: b - a })
  }
  const unplaced = spans.length - placed.length
  let hi = 0
  for (const p of placed) hi = Math.max(hi, p.start + p.took)
  const domain: [number, number] = [0, hi > 0 ? hi : 1]
  if (!placed.length) return { scale: "time", rows: [], domain, unplaced }

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
  // Children by (parent run, parent call): the child's invoke_agent
  // span names both.
  const childOf = new Map<string, string>()
  for (const [run, p] of invoke) {
    const pr = str(attrsOf(p.span)["weft.parent.run.id"])
    const pc = str(attrsOf(p.span)["weft.parent.call.id"])
    if (pr && pc) childOf.set(`${pr}\n${pc}`, run)
  }

  const bySpan = new Map<string, Node | null>()
  const claimed = new Set<TimedSpan>()
  const nodeOf = (p: Placed, r: Omit<WaterfallRow, "depth" | "critical">) => {
    const n = node({ ...r, span: p.span })
    bySpan.set(p.span.span_id, n)
    claimed.add(p.span)
    return n
  }

  // A run's subtree: its steps, their model calls and tools, the
  // children under the tools (built when expanded; their spans claimed
  // either way, so a collapsed child's spans stay inside it).
  const built = new Set<string>()
  const claimRun = (run: string) => {
    for (const p of byRun.get(run) ?? []) {
      claimed.add(p.span)
      if (!bySpan.has(p.span.span_id)) bySpan.set(p.span.span_id, null)
    }
  }
  const buildRun = (
    run: string,
    isChild: boolean,
    place?: { start: number; took: number | null; status: RowStatus }
  ): Node => {
    built.add(run)
    const own = byRun.get(run) ?? []
    const fold = isChild ? input.children?.get(run) : input.fold
    const k = keys(run, isChild)
    const inv = invoke.get(run)
    let lo = Infinity
    let end = -Infinity
    for (const p of own) {
      lo = Math.min(lo, p.start)
      end = Math.max(end, p.start + p.took)
    }
    const agent =
      str(attrsOf(inv?.span)["gen_ai.agent.name"]) ??
      inv?.span.name.replace(/^invoke_agent\s*/, "") ??
      fold?.agent
    const steps =
      int(attrsOf(inv?.span)["weft.run.steps"]) ?? fold?.steps.length
    const expanded = !isChild || !!input.expanded?.has(run)
    const r: Omit<WaterfallRow, "depth" | "critical"> = {
      key: `r:${run}`,
      kind: isChild ? "child" : "run",
      label: agent || (isChild ? "subagent" : "run"),
      gist: steps !== undefined ? stepCount(steps) : undefined,
      runId: run,
      // Without its invoke_agent span (live, or not exported): the
      // extent of what it has, else its tool's (a child) or the whole
      // domain (the root).
      start: inv?.start ?? (Number.isFinite(lo) ? lo : (place?.start ?? 0)),
      took: inv
        ? inv.took
        : !isChild && input.status === "running"
          ? null
          : Number.isFinite(lo)
            ? end - lo
            : place
              ? place.took
              : hi,
      status: inv
        ? spanStatus(inv.span)
        : isChild
          ? (place?.status ?? "never")
          : input.status === "running"
            ? "running"
            : input.status === "failed"
              ? "error"
              : "ok",
      ...(isChild
        ? {
            child: { runId: run, collapsed: !expanded, loaded: own.length > 0 },
          }
        : {}),
    }
    const root = inv ? nodeOf(inv, r) : node(r)
    if (!expanded) {
      claimRun(run)
      return root
    }
    // Steps: the chat spans and the execute_tool spans of an index.
    const stepsAt = new Map<number, { chats: Placed[]; tools: Placed[] }>()
    const at = (n: number) => {
      let s = stepsAt.get(n)
      if (!s) stepsAt.set(n, (s = { chats: [], tools: [] }))
      return s
    }
    for (const p of own) {
      const op = opOf(p.span)
      const a = attrsOf(p.span)
      const n = int(a["weft.step.index"])
      if (op === "chat" && n !== undefined) at(n).chats.push(p)
      else if (op === "execute_tool" && n !== undefined) {
        // A resumed call (approved before step 0's model call) and a
        // denied one (zero-length, at the resume) are the run's own.
        if (a["weft.tool.approved"] === true || a["weft.tool.denied"] === true)
          root.kids.push(toolNode(p, "resume"))
        else at(n).tools.push(p)
      }
    }
    function toolNode(p: Placed, where: string): Node {
      const a = attrsOf(p.span)
      const callId = str(a["gen_ai.tool.call.id"]) ?? ""
      const step = int(a["weft.step.index"])
      const fstep = fold?.steps.find((s) => s.index === step)
      const call = callId
        ? where === "resume"
          ? fold?.steps
              .flatMap((s) => s.toolCalls)
              .find((c) => c.resumed && c.callId === callId)
          : fstep?.toolCalls.find((c) => c.callId === callId)
        : undefined
      const err =
        str(a["error.type"]) ??
        (call?.result?.isError
          ? CODED.exec(call.result.content)?.[1]
          : undefined)
      const t = nodeOf(p, {
        key: callId ? k.call(where, callId) : `t:${p.span.span_id}`,
        kind: "tool",
        label:
          str(a["gen_ai.tool.name"]) ??
          p.span.name.replace(/^execute_tool\s*/, ""),
        gist: toolGist(call, a["weft.tool.result_bytes"]),
        runId: run,
        ...(step !== undefined ? { step } : {}),
        ...(callId ? { callId } : {}),
        start: p.start,
        took: p.took,
        status: spanStatus(p.span),
        ...(err ? { error: err } : {}),
      })
      const kid = childOf.get(`${run}\n${callId}`) ?? call?.childRunId
      // A run is built once: parent links that loop end here.
      if (kid && !built.has(kid)) t.kids.push(buildRun(kid, true, t))
      return t
    }
    for (const [n, s] of [...stepsAt].sort((a, b) => a[0] - b[0])) {
      const fstep = fold?.steps.find((x) => x.index === n)
      const all = [...s.chats, ...s.tools]
      let a = Infinity
      let b = -Infinity
      for (const p of all) {
        a = Math.min(a, p.start)
        b = Math.max(b, p.start + p.took)
      }
      const first = s.chats.at(0)
      const fa = attrsOf(first?.span)
      const finish =
        (Array.isArray(fa["gen_ai.response.finish_reasons"])
          ? str((fa["gen_ai.response.finish_reasons"] as unknown[])[0])
          : str(fa["gen_ai.response.finish_reasons"])) ?? fstep?.finish?.reason
      const step = node({
        key: k.step(n),
        kind: "step",
        label: `step ${n}`,
        gist: stepGist(
          s.tools.map(
            (p) => str(attrsOf(p.span)["gen_ai.tool.name"]) ?? p.span.name
          ),
          fstep?.text
        ),
        runId: run,
        step: n,
        start: a,
        took: b - a,
        status: first ? spanStatus(first.span) : "ok",
        ...(finish ? { finish } : {}),
      })
      root.kids.push(step)
      s.chats.forEach((p, i) => {
        const pa = attrsOf(p.span)
        const model =
          str(pa["gen_ai.response.model"]) ??
          str(pa["gen_ai.request.model"]) ??
          p.span.name.replace(/^chat\s*/, "")
        const calls = int(pa["weft.model.tool_calls"])
        const ttft = int(pa["weft.ttft_ms"])
        const m = nodeOf(p, {
          key: i === 0 ? `${k.step(n)}:m` : `t:${p.span.span_id}`,
          kind: "model",
          label: model ? `model · ${model}` : "model",
          gist: modelGist(calls, fstep?.text),
          runId: run,
          step: n,
          start: p.start,
          took: p.took,
          status: spanStatus(p.span),
          ...(str(pa["error.type"]) ? { error: str(pa["error.type"]) } : {}),
          ...(i === 0 && finish ? { finish } : {}),
          ...(ttft !== undefined ? { ttft } : {}),
        })
        // Its attempts: the attempt spans under the chat span.
        const tries = own.filter(
          (x) =>
            opOf(x.span) === "attempt" &&
            x.span.parent_span_id === p.span.span_id
        )
        if (tries.length) m.attempts = tries.length
        for (const x of tries) {
          const xa = attrsOf(x.span)
          const idx = int(xa["weft.attempt.index"])
          const xm =
            str(xa["gen_ai.response.model"]) ?? str(xa["gen_ai.request.model"])
          m.kids.push(
            nodeOf(x, {
              key:
                idx !== undefined && i === 0
                  ? `${k.step(n)}:a${idx}`
                  : `t:${x.span.span_id}`,
              kind: "attempt",
              label: `attempt${idx !== undefined ? ` ${idx}` : ""}${xm ? ` · ${xm}` : ""}`,
              runId: run,
              step: n,
              start: x.start,
              took: x.took,
              status: spanStatus(x.span),
              ...(str(xa["error.type"])
                ? { error: str(xa["error.type"]) }
                : {}),
            })
          )
        }
        step.kids.push(m)
      })
      const tools = s.tools.map((p) => toolNode(p, String(n)))
      step.kids.push(...tools)
      if (tools.length) step.lanes = assignLanes(tools)
    }
    return root
  }

  const root = buildRun(input.runId, false)
  // Every span weft does not name hangs under its parent span's row
  // (in tree order, so a parent's node exists first); a span inside a
  // collapsed child stays hidden; anything else under the root.
  const bySpanPlaced = new Map(placed.map((p) => [p.span, p]))
  for (const t of spanTree(placed.map((p) => p.span))) {
    if (claimed.has(t.span)) continue
    const p = bySpanPlaced.get(t.span)!
    const parent = t.parent !== undefined ? bySpan.get(t.parent) : undefined
    if (parent === null) {
      bySpan.set(t.span.span_id, null)
      continue
    }
    const n = nodeOf(p, {
      key: `t:${t.span.span_id}`,
      kind: "span",
      label: t.span.name,
      runId: runOf(p),
      start: p.start,
      took: p.took,
      status: spanStatus(t.span),
    })
    ;(parent ?? root).kids.push(n)
  }
  markCritical(root)
  return { scale: "time", rows: flatten(root), domain, unplaced }
}

// ── The order scale ────────────────────────────────────────────────

function orderRows(input: WaterfallInput): WaterfallModel {
  const fold = input.fold
  const status = input.status ?? ""
  const running = status === "running"
  const total = Math.max(0, (input.total ?? 0) - 1)
  if (!fold) return { scale: "order", rows: [], domain: [0, 1], unplaced: 0 }

  // A child's positions are its own stream's: drawn inside its tool's
  // range, scaled linearly (map).
  const buildRun = (
    f: FoldedRun,
    isChild: boolean,
    map: (p: number) => number
  ): Node => {
    const run = f.runId
    const k = keys(run, isChild)
    const first = f.startPos ?? f.steps.at(0)?.from ?? 0
    let last = f.finishPos ?? first
    for (const s of f.steps) last = Math.max(last, s.to)
    const a = isChild ? map(first) : 0
    const b = isChild ? map(last) : f.finished ? Math.max(last, total) : last
    const root = node({
      key: `r:${run}`,
      kind: isChild ? "child" : "run",
      label: f.agent || (isChild ? "subagent" : "run"),
      gist: stepCount(f.steps.length),
      runId: run,
      start: a,
      took: f.finished || !running ? b - a : null,
      status: f.finished
        ? "ok"
        : running
          ? "running"
          : status === "failed"
            ? "error"
            : "never",
    })
    const callNode = (c: FoldedToolCall, at: string, step?: number): Node => {
      const open = c.finishPos === undefined
      const t = node({
        key: k.call(at, c.callId),
        kind: "tool",
        label: c.name,
        gist: toolGist(c),
        runId: run,
        ...(step !== undefined ? { step } : {}),
        callId: c.callId,
        start: map(c.startPos),
        took: open ? (running ? null : 0) : map(c.finishPos!) - map(c.startPos),
        status: open
          ? running
            ? "running"
            : "never"
          : c.result?.isError
            ? "error"
            : "ok",
        ...(c.result?.isError
          ? { error: CODED.exec(c.result.content)?.[1] ?? "error" }
          : {}),
      })
      if (c.childRunId) {
        const expanded = !!input.expanded?.has(c.childRunId)
        const cf = input.children?.get(c.childRunId)
        const lo = t.start
        const hi = t.took === null ? map(total) : t.start + t.took
        let child: Node
        if (expanded && cf) {
          const c0 = cf.startPos ?? 0
          let c1 = cf.finishPos ?? c0
          for (const s of cf.steps) c1 = Math.max(c1, s.to)
          const w = Math.max(1, c1 - c0)
          child = buildRun(cf, true, (p) => lo + ((p - c0) / w) * (hi - lo))
        } else {
          child = node({
            key: `r:${c.childRunId}`,
            kind: "child",
            label: cf?.agent || c.name,
            ...(cf ? { gist: stepCount(cf.steps.length) } : {}),
            runId: c.childRunId,
            start: lo,
            took: t.took,
            status: t.status,
          })
        }
        child.child = {
          runId: c.childRunId,
          collapsed: !expanded,
          loaded: !!cf,
        }
        t.kids.push(child)
      }
      return t
    }
    for (const s of f.steps) {
      const failedHere = !s.finish && status === "failed"
      const open = !s.finish && running
      const own = s.toolCalls.filter((c) => !c.resumed)
      for (const c of s.toolCalls)
        if (c.resumed) root.kids.push(callNode(c, "resume"))
      const st = node({
        key: k.step(s.index),
        kind: "step",
        label: `step ${s.index}`,
        gist: stepGist(
          own.map((c) => c.name),
          s.text
        ),
        runId: run,
        step: s.index,
        start: map(s.from),
        took: open ? null : map(s.to) - map(s.from),
        status: failedHere
          ? "error"
          : s.finish
            ? "ok"
            : open
              ? "running"
              : "never",
        ...(s.finish ? { finish: s.finish.reason } : {}),
      })
      root.kids.push(st)
      // The model call: from the step's start to its first tool call
      // (the stream says no more on positions).
      const firstTool = own.at(0)
      const mEnd = firstTool ? firstTool.startPos : s.finish ? s.to : undefined
      st.kids.push(
        node({
          key: `${k.step(s.index)}:m`,
          kind: "model",
          label: f.model?.name ? `model · ${f.model.name}` : "model",
          gist: modelGist(own.length, s.text),
          runId: run,
          step: s.index,
          start: map(s.from),
          took: mEnd === undefined ? null : map(mEnd) - map(s.from),
          status: st.status,
          ...(s.finish ? { finish: s.finish.reason } : {}),
          ...(s.finish?.ttftMs !== undefined ? { ttft: s.finish.ttftMs } : {}),
        })
      )
      const tools = own.map((c) => callNode(c, String(s.index), s.index))
      st.kids.push(...tools)
      if (tools.length) st.lanes = assignLanes(tools)
    }
    return root
  }

  const root = buildRun(fold, false, (p) => p)
  markCritical(root)
  const rows = flatten(root)
  let hi = total
  for (const r of rows) hi = Math.max(hi, r.start + (r.took ?? 0))
  return { scale: "order", rows, domain: [0, Math.max(1, hi)], unplaced: 0 }
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
  const s =
    scale ??
    ((input.spans ?? []).some((sp) => {
      const a = instantMs(sp.start)
      const b = instantMs(sp.end)
      return Number.isFinite(a) && Number.isFinite(b) && b >= a
    })
      ? "time"
      : "order")
  return s === "time" ? timeRows(input) : orderRows(input)
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
