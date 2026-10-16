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
import type { AnswerStep } from "./summarize"
import type { Span, SpanKind } from "./trace"
import type { WaterfallModel, WaterfallRow } from "./waterfall"
import { lanesWord, ROW_KIND, rowBadge, rowKeys, rowTone } from "./waterfall"

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
      kind: ROW_KIND[r.kind],
      label: r.label,
      sub: sub || undefined,
      badge: rowBadge(r),
      tone: rowTone(r),
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
      ...(r.untimed ? { untimed: r.untimed } : {}),
    }
  })
}

/**
 * resolveRowSel finds the row a ?sel= names: its key; else an old form
 * of it — the fold's root key "run" (r:<run> now), the root's
 * run-qualified s:<run>:<n> / c:<run>:… (bare now), a t:<span id> (a
 * pre-U8.2 time-axis row: the row that span is behind), a bare
 * c:<call id> (pre-G1: the call in the linked step, else the first),
 * a denied call keyed by its step (c:<step>:<call>: c:resume:<call>
 * now, lib/trace-sel.ts's mapping). A key inside a child run no row
 * draws (collapsed, or its rows not read) selects that child's row —
 * the deepest drawn child whose run id the key names. A key no row
 * has, nor any child of it, selects nothing.
 */
export function resolveRowSel(
  rows: readonly WaterfallRow[],
  sel: string | undefined,
  rootId: string,
  step?: number,
  parents?: ReadonlyMap<string, string>
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
  // A denied call's old key, by its step: c:resume:<call> now (the
  // root's qualified form read bare).
  const denied = /^(c:(?:.+:)?)\d+:([^:]+)$/.exec(sel)
  if (denied) {
    const k = `${denied[1]}resume:${denied[2]}`
    const hit =
      has(k) ??
      (k.startsWith(`c${q}`) ? has(`c:${k.slice(q.length + 1)}`) : undefined)
    if (hit) return hit
  }
  // t:<span id>: the row that span is behind, whichever run's — and
  // (a log line's "jump to span", U11.2: an app span under a tool) for
  // a span no row draws, the row of its nearest drawn ancestor
  // (parents: span id → parent span id).
  if (sel.startsWith("t:")) {
    for (
      let id: string | undefined = sel.slice(2), hops = 0;
      id && hops < 64;
      id = parents?.get(id), hops++
    ) {
      const at = id
      const hit = rows.find((r) => r.span?.span_id === at)?.key
      if (hit) return hit
    }
  }
  const call = /^c:([^:]+)$/.exec(sel)?.[1]
  if (call) {
    const calls = rows.filter(
      (r) => r.kind === "tool" && r.runId === rootId && r.callId === call
    )
    return (calls.find((r) => r.step === step) ?? calls.at(0))?.key
  }
  // <run>:<span id> (U9.2): the row of that run its span is behind.
  const i = sel.lastIndexOf(":")
  if (i > 0) {
    const run = sel.slice(0, i)
    const id = sel.slice(i + 1)
    const hit = rows.find((r) => r.runId === run && r.span?.span_id === id)
    if (hit) return hit.key
  }
  // Inside a child no row draws: that child's own row, the deepest (a
  // grandchild's keys name its id, <child>/…, as the tree's own
  // collapsing reads it).
  if (!/^[sctr]:/.test(sel)) return undefined
  const rest = sel.slice(2)
  let best: WaterfallRow | undefined
  for (const r of rows)
    if (
      r.kind === "child" &&
      (rest === r.runId ||
        rest.startsWith(`${r.runId}:`) ||
        rest.startsWith(`${r.runId}/`)) &&
      (!best || r.runId.length > best.runId.length)
    )
      best = r
  return best?.key
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

/**
 * defaultRowSel is where the run page lands when no ?sel= or ?step=
 * names a row (UX-A-03; arena's ltDefaultSel): where something went
 * wrong — the first row in tree order that failed (a step, a model
 * call, a tool, a child run; never the root, whose failure is its
 * header's, nor an attempt a later one answered for), else a tool
 * that never finished — then the step a failed run failed at
 * (failedAt: lib/summarize.ts's failedAtStep), then step 0, then the
 * first row. Read on the rows the page draws, on the scale shown.
 */
export function defaultRowSel(
  rows: readonly WaterfallRow[],
  failedAt?: number
): string | undefined {
  const root = rows.at(0)?.runId
  const pick = (f: (r: WaterfallRow) => boolean) =>
    rows.find((r) => r.parent !== undefined && r.kind !== "attempt" && f(r))
      ?.key
  const has = (k: string) => rows.some((r) => r.key === k)
  return (
    pick((r) => r.status === "error") ??
    pick((r) => r.kind === "tool" && r.status === "never") ??
    (failedAt !== undefined &&
    rows.some((r) => r.key === `s${failedAt}` && r.runId === root)
      ? `s${failedAt}`
      : undefined) ??
    (has("s0") ? "s0" : undefined) ??
    rows.at(0)?.key
  )
}

/** The drawer's kind label for a row (UX-A-05: a model call is MODEL,
 * never TOOL). */
export const KIND_LABEL: Record<SpanKind, string> = {
  run: "RUN",
  subagent: "CHILD RUN",
  step: "STEP",
  model: "MODEL",
  attempt: "ATTEMPT",
  tool: "TOOL",
  span: "SPAN",
}

/** The drawer's lazy tabs (§6.3), in their order: ?tab= names one. */
export const DRAWER_TABS = [
  "request",
  "response",
  "tools",
  "logs",
  "raw",
] as const
export type DrawerTab = (typeof DRAWER_TABS)[number]
export const DRAWER_TAB_LABEL: Record<DrawerTab, string> = {
  request: "Request",
  response: "Response",
  tools: "Tool I/O",
  logs: "Logs",
  raw: "Raw OTel",
}

/** A ?tab= value the drawer knows, else undefined. */
export function drawerTabOf(v: unknown): DrawerTab | undefined {
  return DRAWER_TABS.find((t) => t === v)
}

/** The tab a row opens on when ?tab= names none: a model call and its
 * attempts their request, a tool its I/O, a step and a run what they
 * replied (the step's words and calls, a run's steps), a span its
 * attributes. */
export function defaultTab(kind: SpanKind | undefined): DrawerTab {
  switch (kind) {
    case "tool":
      return "tools"
    case "step":
    case "run":
    case "subagent":
      return "response"
    case "span":
      return "raw"
    default:
      return "request"
  }
}

/** The step a row belongs to, by its key and its parents' (a step, its
 * model call, an attempt, a tool): the step row's key, or undefined. */
export function stepRowOf(
  key: string | undefined,
  rows: readonly Span[]
): Span | undefined {
  const by = new Map(rows.map((r) => [r.key, r]))
  for (let r = key ? by.get(key) : undefined; r; r = by.get(r.parent ?? ""))
    if (r.kind === "step") return r
    else if (r.kind === "run" || r.kind === "subagent") return undefined
  return undefined
}

/** A number attribute: a number, or the ClickHouse fallback map's
 * string. */
function numAttr(v: unknown): number | undefined {
  const n = typeof v === "string" && v.trim() !== "" ? Number(v) : v
  return typeof n === "number" && Number.isFinite(n) ? n : undefined
}

/** The first finish reason: a list, its JSON string, or one reason. */
function firstFinish(v: unknown): string | undefined {
  if (typeof v === "string" && v.startsWith("["))
    try {
      v = JSON.parse(v)
    } catch {
      /* the string itself */
    }
  const f = Array.isArray(v) ? (v as unknown[])[0] : v
  return typeof f === "string" ? f : undefined
}

/**
 * derivedStep is a derived run's step (plan I1.3: stock GenAI spans,
 * no events) as the answer reads it, from the step's spans alone:
 * the finish reason and the tokens its chat span's gen_ai attributes
 * carry, the model call's duration, and its execute_tool spans as the
 * calls the model asked for (an error status their failure). No text:
 * a span carries no reply — the drawer says so (DRAWER.derivedNoEvents).
 * undefined when the step row is not drawn.
 */
export function derivedStep(
  stepKey: string,
  rows: readonly Span[]
): AnswerStep | undefined {
  const row = rows.find((r) => r.key === stepKey && r.kind === "step")
  const index = /^s(\d+)$/.exec(stepKey)?.[1]
  if (!row || index === undefined) return undefined
  const under = rows.filter((r) => r.parent === stepKey)
  const model = under.find((r) => r.kind === "model" && r.timed)
  const a = model?.timed?.span.attrs ?? {}
  const reason = firstFinish(a["gen_ai.response.finish_reasons"])
  const inTok = numAttr(a["gen_ai.usage.input_tokens"])
  const outTok = numAttr(a["gen_ai.usage.output_tokens"])
  const t = model?.timed
  const latencyMs = t && t.toMs !== null ? t.toMs - t.fromMs : undefined
  return {
    index: Number(index),
    toolCalls: under
      .filter((r) => r.kind === "tool")
      .map((r) => {
        const s = r.timed?.span
        const ta = s?.attrs ?? {}
        const name = ta["gen_ai.tool.name"]
        const done = !!r.timed && r.timed.toMs !== null
        return {
          name: typeof name === "string" ? name : r.label,
          ...(done
            ? {
                finishPos: 0,
                result: { isError: s?.status === "error", content: "" },
              }
            : {}),
        }
      }),
    ...(model && t?.toMs !== null
      ? {
          finish: {
            reason: reason ?? "",
            ...(inTok !== undefined || outTok !== undefined
              ? { usage: { input_tokens: inTok, output_tokens: outTok } }
              : {}),
            ...(latencyMs !== undefined && latencyMs > 0 ? { latencyMs } : {}),
          },
        }
      : {}),
  }
}

/** The drawer's words (§6.3): its verbs, its tabs' empty states. */
export const DRAWER = {
  derivedNoEvents: "— a derived run has no event record",
  replay: "replay from here ▸",
  replayHint: (n: number) =>
    `replay from step ${n}: the steps before it as recorded, step ${n} on run fresh (the experiment drawer)`,
  edit: "edit…",
  editHint: (n: number) => `edit step ${n}'s prompt and replay`,
  editResult: "edit result…",
  editResultHint: (call: string) => `edit ${call}'s result and replay`,
  compare: "compare…",
  compareHint: "this run beside another, step by step, landing on this step",
  rerunChild: "replay the child ▸",
  story: "read in the story",
  storyHint: "the same selection as a step card (s)",
  noRequest: "no request record for this row: the Request tab is a step's",
  noTools: "no tool calls under this row",
  noResponse: "nothing replied under this row",
  noLogs: "no app log line under this row's spans",
  logsOff:
    "this Studio has no logs capability: the app's log lines are not served",
  noSpan:
    "no OTel span behind this row (the order scale, or a row no span covers): its folded events below",
  select: "select a row in the run tree",
  loading: "loading…",
  jump: "▶ jump here",
} as const

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
  step?: number,
  parents?: ReadonlyMap<string, string>
): string[] {
  const key = resolveRowSel(full, sel, rootId, step, parents)
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

/** A child row's truncated badge when its stream passed the page's
 * read cap (U9.2-fix W3): nothing lost, not all of it read here. */
export const childCappedReason = (cap: number) =>
  `this page reads a child run's first ${cap.toLocaleString("en-US")} events for its rows: the later ones are not drawn here, nothing was lost`
export const CHILD_CAPPED_FIX = "open the child run (open run ↗)"

/** A child row's gap badge: how many of its recorded events are
 * missing. */
export const gapNote = (n: number) =>
  `${n.toLocaleString("en-US")} ${n === 1 ? "event" : "events"} missing`

/** The run page's link to the whole trace (UX-A-17). */
export const TRACE_LINK = "whole trace ↗"
export const TRACE_HINT =
  "the OTel trace this run is in: every run and span of it, the selection kept"
