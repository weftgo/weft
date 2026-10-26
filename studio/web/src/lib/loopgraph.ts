// The loop graph (U34; WEFT-LOOP-GRAPH.md, audit §6.2, UX-S3-14): what
// one agent run did with its tools — the prompt, the model, one node
// per distinct tool (its calls, failures, denials, parks, the slowest),
// the subagents it started, and how the run ended in the U3 vocabulary
// (lib/stopped-by.ts). Studio's run tree draws it as the `graph` scale's
// `loop` level (components/studio/loop-graph.tsx); the devtools panel
// draws it as one line in the Story tab (loopChips). Both build it here,
// from the same run tree rows (lib/waterfall.ts): the ?tree=1 spans on
// the time scale, the transcript fold on the order scale for a run with
// no spans — one model, so the counts, failures and ending are the same
// on both surfaces (parity.test.ts). It aggregates (nodes are tools,
// never calls): a 1,000-step run builds in well under 50 ms.
//
// Nothing here is guessed: a call's outcome is its row's status, the
// arguments a repeated call is matched on are the fold's (absent
// content-off: no repeat is claimed), the tools offered are the request
// catalog's (null when it was not read: the renderer says so with a
// hole badge). Plain TypeScript, no React.
import { RETRY_RESULT } from "./error-kind"
import { plural, tokenPair } from "./format"
import type { FoldedRun } from "./events"
import type { HoleMark } from "./honesty"
import { FAILED_WORD, stoppedByWords } from "./stopped-by"
import { attrsOf, int, str, took } from "./waterfall"
import type { WaterfallRow } from "./waterfall"

/** What became of one call: its row's status in the loop's words. */
export type Outcome =
  "ok" | "error" | "retry" | "denied" | "parked" | "running" | "never" | "unrun"

export interface LoopCall {
  /** Its run tree row's key: the selection (?sel=). */
  key: string
  step?: number
  outcome: Outcome
  /** Its duration in ms (the time scale); absent or null untimed. */
  ms?: number | null
  /** error.type, or the ToolError code of an error result. */
  error?: string
  /** Its arguments as the fold recorded them (JSON), when it did. */
  args?: string
  /** The subagent run the call started. */
  child?: string
  /** Its call id. */
  call?: string
  /** A parked call no row draws (run_finish.pending alone): the row it
   * selects instead — its step's. Its own key names no row. */
  at?: string
  /** Approved and run before step 0's model call (a resume). */
  resumed?: boolean
}

/** A tool node as loopCore counts it (the panel's chip). */
export interface CoreTool {
  name: string
  /** In call order. */
  calls: LoopCall[]
  fails: number
  /** RETRY results (ModelRetry): the repair loop, never a failure. */
  retries: number
  denied: number
  parked: number
}

export interface LoopTool extends CoreTool {
  /** t:<tool name>. */
  id: string
  running: number
  /** The slowest and the median call, in ms (0 when none was timed). */
  slowest: number
  p50: number
  /** The tools record's source when the catalog names one ("mcp:<server>"). */
  source?: string
  /** A Subagent tool: its calls start child runs (a boxed node). */
  agent?: string
}

export interface LoopStep {
  n: number
  /** The step row's key. */
  key: string
  /** The tool ids called in it (a fan-out lists one id per call). */
  tools: string[]
  finish?: string
}

export type EndTone = "ok" | "bad" | "parked" | "running"

export interface LoopEnd {
  /** The run's stopped_by, when recorded. */
  by?: string
  word: string
  hint: string
  tone: EndTone
  /** The last step's model call's row, else its step's (the end node
   * selects it). */
  key?: string
  tokens?: { input: number; output: number }
}

export interface LoopGraph {
  runId: string
  /** The last step drawn. */
  last?: number
  agent: string
  /** The run's own row (r:<run>): the prompt node selects it. */
  runKey: string
  steps: LoopStep[]
  /** Model calls retried (attempts past the first, summed). */
  retries: number
  /** By first use. */
  tools: LoopTool[]
  /** Offered in the request catalog and never called; null when the
   * catalog was not read (a hole, never "none"). */
  offered: string[] | null
  end: LoopEnd
}

export interface LoopOptions {
  /** The run's stopped_by (its row's); else its invoke_agent span's. */
  stoppedBy?: string
  /** The run's status (its row's); else read from its run row. */
  status?: string
  /** run_finish.pending: parked calls a row may not carry (the order
   * scale's fold has no tool_start for them). */
  pending?: readonly { id: string; name: string }[]
  /** A call's arguments (the fold's), by call id and step. */
  argsOf?: (call: string, step?: number) => unknown
  /** The tools the run's requests offered, with their sources; null or
   * absent when the catalog was not read. */
  catalog?: readonly { name: string; source?: string }[] | null
  usage?: { input_tokens?: number; output_tokens?: number }
}

const outcomeOf = (r: WaterfallRow): Outcome =>
  r.untimed === "unrun"
    ? "unrun"
    : r.status === "pending"
      ? "parked"
      : r.error === "DENIED"
        ? "denied"
        : r.error === RETRY_RESULT
          ? "retry"
          : r.status === "error"
            ? "error"
            : r.status === "running" || r.status === "never"
              ? r.status
              : "ok"

/** What a call's row counts toward (its tool's marks). */
const countOf = (r: WaterfallRow) =>
  r.untimed === "unrun"
    ? ""
    : r.status === "pending"
      ? "parked"
      : r.error === "DENIED"
        ? "denied"
        : r.error === RETRY_RESULT
          ? "retries"
          : r.status === "error"
            ? "fails"
            : ""

// A content-off destination strips tool_start's args to null
// (core.StripContent): null is unknown, never an argument to match on.
const json = (v: unknown) => {
  if (v === undefined || v === null) return undefined
  try {
    return typeof v === "string" ? v : JSON.stringify(v)
  } catch {
    return undefined
  }
}

/** The loop's core, the panel's whole read of it: the tools with their
 * calls and counts in first-use order, the steps, the end. Studio's
 * loopGraph adds the rest over the same core — so the two surfaces
 * count alike by construction. */
export interface LoopCore {
  /** The last step drawn. */
  last?: number
  tools: CoreTool[]
  end: Omit<LoopEnd, "hint" | "key" | "tokens">
}

/**
 * loopCore reads one run's loop off run tree rows (any rows: the
 * page's, every child expanded or not — only `runId`'s are read).
 * pending: run_finish.pending, the parked calls a row may not carry
 * (the order scale's fold has no tool_start for them).
 */
export function loopCore(
  rows: readonly WaterfallRow[],
  runId: string,
  o: Pick<LoopOptions, "pending" | "stoppedBy" | "status"> = {}
): LoopCore {
  const tools = new Map<string, CoreTool>()
  let last: WaterfallRow | undefined
  const add = (
    name: string,
    c: LoopCall,
    k: ReturnType<typeof countOf> = "parked"
  ) => {
    let t = tools.get(name)
    if (!t)
      tools.set(
        name,
        (t = {
          name,
          calls: [],
          fails: 0,
          retries: 0,
          denied: 0,
          parked: 0,
        })
      )
    t.calls.push(c)
    if (k) t[k]++
  }
  // The calls in call order: by step (a resumed call before step 0),
  // then weft.tool.seq — never by span start, which parallel calls
  // reach in any order — then the rows' own order (the fold's, its
  // tool_start order, on a run without spans).
  const calls: [WaterfallRow, number, number, number][] = []
  rows.forEach((r, i) => {
    if (r.runId !== runId) return
    if (r.kind === "step" && r.step! >= (last?.step ?? -1)) last = r
    else if (r.kind === "tool")
      calls.push([
        r,
        r.key.includes(":resume:") ? -1 : (r.step ?? Infinity),
        int(attrsOf(r.span)["weft.tool.seq"]) ?? Infinity,
        i,
      ])
  })
  calls.sort((a, b) => a[1] - b[1] || a[2] - b[2] || a[3] - b[3])
  // run_finish.pending names the final step's parked calls, matched on
  // that step and the call id (an id repeats across steps): a row the
  // fold drew for one (its tool_start, no finish) is parked; one no
  // row draws gets a key of its own, selecting its step.
  const parked = new Set(o.pending?.map((p) => p.id))
  for (const [r, at] of calls) {
    // (delete first: a row already pending is that call too)
    const mine = at >= 0 && r.step === last?.step && parked.delete(r.callId!)
    const k = countOf(r) || (mine ? "parked" : "")
    add(
      r.label,
      {
        key: r.key,
        step: r.step,
        // The rest of the outcome is loopGraph's (outcomeOf): the panel
        // reads the counts alone.
        outcome: k === "parked" ? "parked" : "ok",
        call: r.callId,
        resumed: at < 0,
      },
      k
    )
  }
  for (const p of o.pending ?? [])
    if (parked.has(p.id))
      add(p.name, {
        key: `p:${runId}:${p.id}`,
        step: last?.step,
        outcome: "parked",
        call: p.id,
        at: last?.key || `r:${runId}`,
      })
  const { stoppedBy: by, status } = o
  return {
    last: last?.step,
    tools: [...tools.values()],
    end: {
      by,
      word:
        status === "running"
          ? RUNNING_WORD
          : (stoppedByWords(by)?.word ??
            (status === "failed" ? FAILED_WORD : ENDED_WORD)),
      tone:
        status === "running"
          ? "running"
          : by === "parked"
            ? "parked"
            : status === "failed"
              ? "bad"
              : "ok",
    },
  }
}

/**
 * loopGraph is Studio's loop: loopCore's, plus each call's arguments
 * and child run, the tools' timings and MCP sources, the model's
 * retries, the tools offered and never called, the tokens.
 */
export function loopGraph(
  rows: readonly WaterfallRow[],
  runId: string,
  o: LoopOptions = {}
): LoopGraph {
  const run = rows.find(
    (r) => r.runId === runId && (r.kind === "run" || r.kind === "child")
  )
  const attrs = attrsOf(run?.span)
  const by = o.stoppedBy || str(attrs["weft.run.stopped_by"])
  const core = loopCore(rows, runId, {
    pending: o.pending,
    stoppedBy: by,
    status:
      o.status ??
      (run?.status === "error"
        ? "failed"
        : run?.status === "running"
          ? "running"
          : undefined),
  })
  const tools = core.tools as LoopTool[]
  for (const t of tools) t.id = `t:${t.name}`
  const byKey = new Map<string, LoopCall>()
  const toolOf = new Map<string, LoopTool>()
  const stepOf = new Map<number, LoopStep>()
  for (const r of rows)
    if (r.runId === runId && r.kind === "step" && r.step !== undefined)
      stepOf.set(r.step, {
        n: r.step,
        key: stepOf.get(r.step)?.key ?? r.key,
        tools: [],
        finish: r.finish,
      })
  const steps = [...stepOf.values()].sort((x, y) => x.n - y.n)
  for (const t of tools)
    for (const c of t.calls) {
      byKey.set(c.key, c)
      toolOf.set(c.key, t)
      // Arguments by call id AND step: a call id repeats across steps
      // (each step's calls are numbered afresh); a resumed call's step
      // is its own, undefined.
      if (c.call)
        c.args = json(o.argsOf?.(c.call, c.resumed ? undefined : c.step))
      if (c.step !== undefined) stepOf.get(c.step)?.tools.push(t.id)
    }
  let retries = 0
  let endKey: string | undefined
  const lastN = steps.at(-1)?.n
  for (const r of rows) {
    const own = r.runId === runId
    if (own && r.kind === "model") {
      retries += Math.max(0, (r.attempts ?? 1) - 1)
      if (lastN !== undefined && r.step === lastN) endKey ??= r.key
    }
    // A call's timing and error, off its row: a duration only off a
    // span (the order scale's took is stream positions, not ms).
    const at = own && r.kind === "tool" ? byKey.get(r.key) : undefined
    if (at) {
      if (at.outcome !== "parked") at.outcome = outcomeOf(r)
      at.ms = r.untimed || !r.span ? null : r.took
      if (r.error) at.error = r.error
    }
    // A child run's row under one of this run's calls.
    const c = r.kind === "child" && r.parent ? byKey.get(r.parent) : undefined
    if (c && r.runId !== runId) {
      c.child = r.runId
      toolOf.get(r.parent!)!.agent = r.label
    }
  }
  const sources = new Map((o.catalog ?? []).map((t) => [t.name, t.source]))
  for (const t of tools) {
    t.running = t.calls.filter((c) => c.outcome === "running").length
    const ms = t.calls
      .map((c) => c.ms)
      .filter((m): m is number => typeof m === "number")
      .sort((a, b) => a - b)
    t.slowest = ms.at(-1) ?? 0
    t.p50 = ms.length ? ms[(ms.length - 1) >> 1] : 0
    const src = sources.get(t.name)
    if (src) t.source = src
  }
  // Top to bottom by first use (MCP tools beside their server's first).
  const group = (t: LoopTool) => (t.source?.startsWith("mcp") ? t.source : t.id)
  const first = new Map<string, number>()
  tools.forEach((t, i) => {
    if (!first.has(group(t))) first.set(group(t), i)
  })
  const sorted = tools
    .map((t, i) => [t, i] as const)
    .sort(
      (a, b) => first.get(group(a[0]))! - first.get(group(b[0]))! || a[1] - b[1]
    )
    .map(([t]) => t)
  const tin = o.usage?.input_tokens ?? int(attrs["gen_ai.usage.input_tokens"])
  const tout =
    o.usage?.output_tokens ?? int(attrs["gen_ai.usage.output_tokens"])
  const called = new Set(core.tools.map((t) => t.name))
  return {
    runId,
    agent: run?.label ?? "",
    runKey: run?.key ?? `r:${runId}`,
    steps,
    last: steps.at(-1)?.n,
    retries,
    tools: sorted,
    offered: o.catalog
      ? [...new Set(o.catalog.map((t) => t.name))].filter((n) => !called.has(n))
      : null,
    end: {
      ...core.end,
      ...((endKey ?? steps.at(-1)?.key)
        ? { key: endKey ?? steps.at(-1)!.key }
        : {}),
      hint:
        stoppedByWords(by)?.hint ??
        (core.end.tone === "bad" ? FAILED_HINT : ENDED_HINT),
      ...(tin !== undefined || tout !== undefined
        ? { tokens: { input: tin ?? 0, output: tout ?? 0 } }
        : {}),
    },
  }
}

// ── Reading the model ──────────────────────────────────────────────

/** The tool a row key is a call of, and its place among that tool's
 * calls (the drawer's ‹ call i of n ›). */
export function callOf(
  g: LoopGraph,
  key: string | undefined
): { tool: LoopTool; i: number } | undefined {
  if (!key) return undefined
  for (const tool of g.tools) {
    const i = tool.calls.findIndex((c) => c.key === key)
    if (i >= 0) return { tool, i }
  }
  return undefined
}

/** The node a selected row lights: a call's tool, a step's or model
 * call's model node, the run's own row the prompt. */
export function nodeOfKey(
  g: LoopGraph,
  key: string | undefined,
  row?: WaterfallRow
): string | undefined {
  if (!key) return undefined
  const c = callOf(g, key)
  if (c) return c.tool.id
  if (key === g.runKey) return PROMPT
  if (key === g.end.key) return END
  if (row && row.runId === g.runId && row.step !== undefined) return MODEL
  return undefined
}

/** The tool ids a step lit (the scrubber). */
export const litBy = (g: LoopGraph, step: number | undefined) =>
  new Set(g.steps.find((s) => s.n === step)?.tools ?? [])

/** The node ids the renderer draws besides the tools. */
export const PROMPT = "prompt"
export const MODEL = "model"
export const END = "end"

/** ?sel= after Esc: no selection, and the landing rule (the first
 * failed row, else step 0) does not pick one again — until the next
 * pick writes a row key over it. No row key is this word. */
export const NO_SEL = "none"

/** A run id's ancestors (core's id rule, <parent>/<step>/<call>), the
 * root first: the breadcrumb's runs, down to `run`, never above `root`. */
export function runChain(root: string, run: string): string[] {
  const out = [run]
  for (let r = run; r !== root && r.startsWith(`${root}/`);) {
    const m = /^(.+)\/(?:\d+|resume)\/[^/]+$/.exec(r)
    if (!m) break
    out.unshift((r = m[1]))
  }
  return out[0] === root ? out : [root, ...out]
}

// ── Insights (Studio's strip; WEFT-LOOP-GRAPH.md §3.3) ─────────────

export type InsightKind = "end" | "limit" | "repeat" | "fails" | "never"

export interface Insight {
  kind: InsightKind
  text: string
  /** The node it selects. */
  node: string
}

const BUDGET_ENDS = new Set([
  "max_steps",
  "loop_detected",
  "usage_limit",
  "model_retries_exceeded",
])

/** At most three findings, the run's end first; none found, none —
 * the strip is not drawn. */
export function loopInsights(g: LoopGraph): Insight[] {
  const out: Insight[] = []
  if (g.end.by && BUDGET_ENDS.has(g.end.by))
    out.push({ kind: "end", text: `ended by ${g.end.word}`, node: END })
  for (const s of g.steps)
    if (s.finish === "max_tokens" || s.finish === "length") {
      out.push({
        kind: "limit",
        text: `hit the output limit at step ${s.n}`,
        node: MODEL,
      })
      break
    }
  // A tool called with the same arguments in a row (≥ 2): arguments the
  // fold recorded only — content-off, no repeat is claimed.
  const seq = g.tools
    .flatMap((t) => t.calls.map((c) => [t, c] as const))
    .sort((a, b) => (a[1].step ?? 0) - (b[1].step ?? 0))
  let best: [LoopTool, number] | undefined
  for (let i = 0, run = 1; i < seq.length; i++) {
    const [t, c] = seq[i]
    const next = seq.at(i + 1)
    if (
      next &&
      next[0] === t &&
      c.args !== undefined &&
      next[1].args === c.args
    )
      run++
    else {
      if (run >= 2 && (!best || run > best[1])) best = [t, run]
      run = 1
    }
  }
  if (best)
    out.push({
      kind: "repeat",
      text: `${best[0].name} ×${best[1]} same args`,
      node: best[0].id,
    })
  for (const t of g.tools)
    if (t.calls.length && t.fails === t.calls.length)
      out.push({
        kind: "fails",
        text: `${t.name} failed ${t.fails} of ${t.calls.length}`,
        node: t.id,
      })
  if (g.offered?.length)
    out.push({
      kind: "never",
      text: `${g.offered.slice(0, 3).join(", ")}${g.offered.length > 3 ? ` +${g.offered.length - 3}` : ""} never called`,
      node: `o:${g.offered[0]}`,
    })
  return out.slice(0, 3)
}

// ── Words (both surfaces) ──────────────────────────────────────────

export const RUNNING_WORD = "running"
/** A RETRY result's word: the model was asked to fix its call. */
export const RETRY_WORD = "retried"
const RETRY_HINT = "RETRY: the tool asked the model to fix its call"
export const ENDED_WORD = "ended"
const ENDED_HINT = "the run recorded no stopped_by"
const FAILED_HINT = "the run failed"

/** A tool's marks, the node's and the panel chip's: ×N, ✗ failures,
 * ✋ denials, ⏸ parks. */
export const toolMarks = (t: CoreTool) =>
  `×${t.calls.length}` +
  MARKS.map(([k, m]) => (t[k] ? ` ${m}${t[k] > 1 ? t[k] : ""}` : "")).join("")
const MARKS = [
  ["fails", "✗"],
  ["retries", "↻"],
  ["denied", "✋"],
  ["parked", "⏸"],
] as const

/** A tool node's second line: "2 failed · slowest 4.8ms". */
export function toolLine(t: LoopTool): string {
  const parts: string[] = []
  if (t.fails) parts.push(`${t.fails} failed`)
  if (t.retries) parts.push(`${t.retries} ${RETRY_WORD}`)
  if (t.denied) parts.push(`${t.denied} denied`)
  if (t.parked) parts.push(`${t.parked} parked`)
  if (t.running) parts.push(RUNNING_WORD)
  if (t.slowest) parts.push(`slowest ${took(t.slowest)}`)
  return parts.join(" · ") || plural(t.calls.length, "call")
}

/** A tool node's hover: the numbers plus p50 / slowest. */
export const toolHint = (t: LoopTool) =>
  `${t.name}: ${plural(t.calls.length, "call")}` +
  (t.fails ? `, ${t.fails} failed` : "") +
  (t.retries ? `, ${t.retries} ${RETRY_WORD} (${RETRY_HINT})` : "") +
  (t.denied ? `, ${t.denied} denied` : "") +
  (t.parked ? `, ${t.parked} parked` : "") +
  (t.slowest ? ` · p50 ${took(t.p50)} · slowest ${took(t.slowest)}` : "") +
  (t.source ? ` · ${t.source}` : "")

/** The model node's line: "4 steps · ↻ 1 retry". */
export const modelLine = (g: LoopGraph) =>
  plural(g.steps.length, "step") +
  (g.retries ? ` · ↻ ${plural(g.retries, "retry", "retries")}` : "")

/** The end node's tokens: "1,246 in · 62 out" (lib/format.ts). */
export const endTokens = (e: LoopEnd) =>
  e.tokens ? tokenPair(e.tokens.input, e.tokens.output, true) : ""

/** The panel's loop line, as chips: the model, each tool with its
 * marks (it selects its first call's step), the end. */
export interface LoopChip {
  text: string
  /** The step a click selects. */
  step?: number
  /** "" for a tool that only succeeded. */
  tone: "" | "error" | "parked" | EndTone
}
export function loopChips(
  g: Pick<LoopCore, "tools" | "last" | "end">
): LoopChip[] {
  return [
    ...g.tools.map((t): LoopChip => ({
      text: `${t.name} ${toolMarks(t)}`,
      step: t.calls[0]?.step,
      tone: t.parked || t.denied ? "parked" : t.fails ? "error" : "",
    })),
    { text: `→ ${g.end.word}`, step: g.last, tone: g.end.tone },
  ]
}

export const LOOP_LABEL = "loop"
export const GRAPH_HANDOFF = "graph ⤢"
export const GRAPH_HANDOFF_HINT = "open this run's loop graph in Studio"

/** The level switch (the graph's header): the loop, or today's call
 * graph of agents and MCP servers. */
export const LEVELS = ["loop", "agents"] as const
export type GraphLevel = (typeof LEVELS)[number]
export const LEVEL_HINT: Record<GraphLevel, string> = {
  loop: "loop: the model, its tools and how the run ended",
  agents:
    "agents: who called whom — agents and MCP servers, each edge's calls and its slowest call",
}
export const levelOf = (v: unknown): GraphLevel | undefined =>
  v === "loop" || v === "agents" ? v : undefined

export const LOOP_HINT =
  "graph: the loop — the model, one node per tool (calls, failures, parks), how the run ended; agents: who called whom"
export const NEVER_CALLED = "never called"
export const NEVER_HINT = "offered in the request catalog, never called"
export const MORE_TOOLS = (n: number) => `+${n} tools`
/** The tools offered and never called, unknown (offered null): a
 * not_recorded badge of the A3 table (lib/honesty.ts) in this
 * instance's words — a subagent's catalog is not read on its parent's
 * page; a run of its own whose requests carried no catalog. */
export const OFFERED_UNREAD: HoleMark = {
  hole: "not_recorded",
  reason:
    "this subagent's request catalog is not read on this page: the tools it offered and never called are not drawn",
  fix: "open the run (open run ↗): its own page reads its catalog",
}
export const OFFERED_NONE: HoleMark = {
  hole: "not_recorded",
  reason:
    "no request record of this run carried a tool catalog: the tools it offered and never called are not drawn",
}
/** The request record's read failed: the catalog is not known — never
 * "none" (U34-fix2). Said as the attempts pane says a failed read
 * (U34-fix3): plain words in text-status-bad, no badge — the A3 table's
 * gap means a dropped record, not a failed HTTP read. */
export const offeredUnreadable = (error: string): string =>
  `the request record could not be read: ${error} — the tools offered and never called are not known, not none; reload the page to read it again`
export const INSIGHTS_LABEL = "insights"
export const STEPS_LABEL = "steps"
export const PLAY = "▶ play"
export const PAUSE = "❚❚ pause"
export const PLAY_OFF_HINT = "play is off: the system asks for reduced motion"
export const EXPAND = "expand"
export const callStepper = (i: number, n: number) => `call ${i + 1} of ${n}`
export const PROMPT_LABEL = "prompt"
export const MODEL_LABEL = "model"
export const RESULTS_HINT = "results back to the model"
export const CALLS_HINT = "calls"
export const DEEP_HANDOFF_DEPTH = 3

/** The tools offered across a run's request catalogs (lib/edits.ts's
 * schemaOf walk); null when no request carried a catalog. */
export function catalogTools(
  steps: Map<number, { rows: { tools?: unknown }[] }> | undefined
): { name: string; source?: string }[] | null {
  let any = false
  const out = new Map<string, string | undefined>()
  for (const st of steps?.values() ?? [])
    for (const row of st.rows) {
      const cat = row.tools as
        { tools?: { name: string; source?: string }[] } | undefined
      if (!cat?.tools) continue
      any = true
      for (const t of cat.tools)
        if (!out.has(t.name) || t.source) out.set(t.name, t.source)
    }
  return any
    ? [...out].map(([name, source]) => (source ? { name, source } : { name }))
    : null
}

/** The agents level without spans (the call graph reads them): off,
 * with the A3 table's not_recorded badge in these words. */
export const NO_SPANS_LEVEL: HoleMark = {
  hole: "not_recorded",
  cause: "no_spans",
  reason:
    "the agents level is the call graph of the run's spans: this run recorded none",
}

/** The run's spans could not be read: the agents level is off, and
 * whether the run recorded any is not known (U34-fix2) — said as the
 * read's failure, never NO_SPANS_LEVEL's "recorded none"; plain words,
 * no badge (U34-fix3, as offeredUnreadable). */
export const spansUnreadable = (error: string): string =>
  `the run's spans could not be read: ${error} — the agents level is off, not known to be none; reload the page to read them again`

/** loopSig is what the loop reads off a fold, as a key: its steps'
 * finishes, calls, results and children, its parks and its end — the
 * run page rebuilds the loop's rows when it moves, never per event
 * batch (a text delta leaves it unchanged). */
export function loopSig(
  f: Pick<FoldedRun, "steps" | "pending" | "finished">
): string {
  let s = `${+f.finished}|${f.pending.length}`
  for (const st of f.steps) {
    s += `;${st.index}${st.finish?.reason ?? ""}`
    for (const c of st.toolCalls)
      s += `,${c.callId}${c.result ? (c.result.isError ? "!" : "+") : c.state[0]}${c.childRunId ?? ""}`
  }
  return s
}
