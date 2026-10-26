// The call graph (U9.2; audit §6.2, UX-S3-14): the run tree's third
// scale. Who called whom, read from the same spans the tree draws
// (GET /api/runs/{id}/spans?tree=1): every agent a node (its runs, how
// long the slowest took, how many failed), every MCP server a node
// (the execute_tool span's weft.tool.origin, else the tools record's
// source: "mcp" or "mcp:<server>" — O8), every edge a
// caller → callee pair with its call count and the slowest call. The
// selection is the tree's: a node selects its first run's row, and a
// selected row lights the node its run belongs to.
//
// A caller-linked run (thread/pool's child session's turn, O11) is
// drawn under the run whose call started it when the spans hold it.
// Plain TypeScript, no React:
// Studio only (the panel links here).
import type { RunRow, Span } from "./api"
import { plural, tokenPair } from "./format"
import { MCP_FAILURE, isMcpOrigin } from "./onion"
import type { HoleMark } from "./honesty"
import { statusOf } from "./status"
import type { Status, StatusKey, StatusRow } from "./status"
import { attrsOf, instantMs, int, opOf, str, took } from "./waterfall"

export type NodeKind = "agent" | "mcp"

export interface GraphNode {
  /** a:<agent> or m:<source>. */
  id: string
  kind: NodeKind
  label: string
  /** The runs of this agent, in start order (an MCP node: none). */
  runs: string[]
  /** Runs (an agent) or calls (an MCP server) into it; the root's 1. */
  calls: number
  fails: number
  /** The slowest run or call, in ms. */
  slowest: number
  /** Columns: the shortest call chain from the root agent. */
  depth: number
  /** Own tokens summed over its runs, when the spans carry them. */
  tokens?: { input: number; output: number }
  /** Each run's own facts, as its invoke_agent span says them (an
   * agent; an MCP node: none) — the status and steps line's fallback
   * where the page holds no row for the run (UX-S3-14). */
  facts: RunFact[]
  /** An MCP node's failures by kind (U23.2, O8): calls whose
   * tools/call exchange failed (mcp_transport) and calls the server
   * answered isError (mcp_tool_error); any other failure counts in
   * fails alone. */
  unreachable?: number
  /** Calls the server answered with a JSON-RPC error (mcp_rpc_error,
   * U23.2-fix): reached, and refusing. */
  refused?: number
  toolErrors?: number
}

/** A run as its invoke_agent span says it ended. */
export interface RunFact {
  run: string
  failed: boolean
  /** weft.run.pending: calls parked for approval. */
  pending: number
  /** weft.run.steps, when the span carries it. */
  steps?: number
}

export interface GraphEdge {
  from: string
  to: string
  count: number
  /** The slowest call along it, in ms. */
  slowest: number
  fails: number
}

export interface CallGraph {
  nodes: GraphNode[]
  /** A self-call (an agent delegating to itself) is an edge whose from
   * is its to: the view draws it as a loop over the node. */
  edges: GraphEdge[]
  /** The runs that called tools whose sources the page did not read
   * (a child run's catalog): their MCP calls are not drawn. */
  unsourced: string[]
}

/** The tools record's source of a run's tool, when the page knows it. */
export type SourceOf = (run: string, tool: string) => string | undefined

const ms = (s: Span) => {
  const d = instantMs(s.end) - instantMs(s.start)
  return Number.isFinite(d) && d >= 0 ? d : 0
}

/** A run's parent: the invoke_agent span's weft.parent.run.id, the
 * tree answer's parent_run_id, else core's id rule (<parent>/<step>/<call>,
 * <parent>/resume/<call> under Approve). */
function parentOf(inv: Span, run: string): string | undefined {
  const at = attrsOf(inv)
  const a =
    str(at["weft.parent.run.id"]) ??
    inv.parent_run_id ??
    // A caller-linked run (O11: a thread/pool child session's turn)
    // hangs under the run whose call started it.
    str(at["weft.caller.run.id"])
  if (a) return a
  const m = /^(.+)\/(?:\d+|resume)\/[^/]+$/.exec(run)
  return m?.[1]
}

/**
 * callGraph builds the graph from a run tree's spans: rootRun's agent
 * at depth 0, each descendant run's agent under its caller's, MCP calls
 * (a tool whose source starts "mcp") as edges to their server's node.
 * A run without its invoke_agent span is not drawn (the tree's holes
 * say why); an agent two callers reach is one node with two edges.
 * `known` says whose catalogs sourceOf read (every run's by default): a
 * run outside it that called tools is listed unsourced.
 */
export function callGraph(
  spans: readonly Span[],
  rootRun: string,
  sourceOf?: SourceOf,
  known: (run: string) => boolean = () => true
): CallGraph {
  const runOf = (s: Span) =>
    s.run_id ?? str(attrsOf(s)["weft.run.id"]) ?? rootRun
  const invokes = spans
    .filter((s) => opOf(s) === "invoke_agent")
    .sort((a, b) => instantMs(a.start) - instantMs(b.start))
  const agentOf = new Map<string, string>()
  const nodes = new Map<string, GraphNode>()
  const edges = new Map<string, GraphEdge>()
  const nodeFor = (id: string, kind: NodeKind, label: string) => {
    let n = nodes.get(id)
    if (!n) {
      n = {
        id,
        kind,
        label,
        runs: [],
        calls: 0,
        fails: 0,
        slowest: 0,
        depth: -1,
        facts: [],
      }
      nodes.set(id, n)
    }
    return n
  }
  const edge = (from: string, to: string, d: number, failed: boolean) => {
    const k = `${from}\n${to}`
    let e = edges.get(k)
    if (!e) edges.set(k, (e = { from, to, count: 0, slowest: 0, fails: 0 }))
    e.count++
    e.slowest = Math.max(e.slowest, d)
    if (failed) e.fails++
  }
  for (const s of invokes) {
    const run = runOf(s)
    if (agentOf.has(run)) continue
    const a = attrsOf(s)
    const name =
      str(a["gen_ai.agent.name"]) ||
      s.name.replace(/^invoke_agent\s*/, "") ||
      (run === rootRun ? "run" : "subagent")
    const id = `a:${name}`
    agentOf.set(run, id)
    const n = nodeFor(id, "agent", name)
    const d = ms(s)
    n.runs.push(run)
    n.calls++
    n.slowest = Math.max(n.slowest, d)
    if (s.status === "error") n.fails++
    const steps = int(a["weft.run.steps"])
    n.facts.push({
      run,
      failed: s.status === "error",
      pending: int(a["weft.run.pending"]) ?? 0,
      ...(steps !== undefined ? { steps } : {}),
    })
    const tin = int(a["gen_ai.usage.input_tokens"])
    const tout = int(a["gen_ai.usage.output_tokens"])
    if (tin !== undefined || tout !== undefined) {
      n.tokens ??= { input: 0, output: 0 }
      n.tokens.input += tin ?? 0
      n.tokens.output += tout ?? 0
    }
  }
  for (const s of invokes) {
    const run = runOf(s)
    if (run === rootRun || agentOf.get(run) === undefined) continue
    const up = parentOf(s, run)
    const from = up ? agentOf.get(up) : undefined
    if (from) edge(from, agentOf.get(run)!, ms(s), s.status === "error")
  }
  const unsourced = new Set<string>()
  for (const s of spans) {
    if (opOf(s) !== "execute_tool") continue
    const run = runOf(s)
    const a = attrsOf(s)
    // The span names its origin since O8 (weft.tool.origin): a child
    // run's MCP calls are drawn without its catalog. Older spans fall
    // back on the tools record's source.
    let src = str(a["weft.tool.origin"])
    if (!src) {
      if (!sourceOf) continue
      if (!known(run)) {
        unsourced.add(run)
        continue
      }
      const tool = str(a["gen_ai.tool.name"])
      src = tool ? sourceOf(run, tool) : undefined
    }
    const from = agentOf.get(run)
    if (!from || !isMcpOrigin(src)) continue
    const id = `m:${src}`
    const n = nodeFor(id, "mcp", src)
    const d = ms(s)
    const failed = s.status === "error"
    n.calls++
    n.slowest = Math.max(n.slowest, d)
    if (failed) {
      n.fails++
      const type = str(a["error.type"])
      if (type === "mcp_transport") n.unreachable = (n.unreachable ?? 0) + 1
      if (type === "mcp_rpc_error") n.refused = (n.refused ?? 0) + 1
      if (type === "mcp_tool_error") n.toolErrors = (n.toolErrors ?? 0) + 1
    }
    edge(from, id, d, failed)
  }
  // Depth: breadth first from the root's agent; anything it does not
  // reach (a caller with no span) one column past the deepest.
  const root = agentOf.get(rootRun)
  const out = [...edges.values()]
  if (root) {
    nodes.get(root)!.depth = 0
    for (const q = [root]; q.length;) {
      const at = q.shift()!
      for (const e of out)
        if (e.from === at && nodes.get(e.to)!.depth < 0) {
          nodes.get(e.to)!.depth = nodes.get(at)!.depth + 1
          q.push(e.to)
        }
    }
  }
  const all = [...nodes.values()]
  const deepest = Math.max(0, ...all.map((n) => n.depth))
  for (const n of all) if (n.depth < 0) n.depth = deepest + 1
  return { nodes: all, edges: out, unsourced: [...unsourced].sort() }
}

/**
 * rowsCallGraph is the agents level of a run that recorded no spans
 * (P9-pre-web-fix3): the same graph from the run rows the page read —
 * rootRun's and each row under it by parent_run_id — each run as its
 * row says it (agent, start to finish, failed, steps, pending). A row's
 * usage counts its subagents', so no node carries tokens; no MCP server
 * is drawn (that needs the execute_tool spans): ROWS_LEVEL says both.
 * undefined when no row under rootRun was read.
 */
export function rowsCallGraph(
  rows: ReadonlyMap<string, RunRow>,
  rootRun: string
): CallGraph | undefined {
  const root = rows.get(rootRun)
  if (!root) return undefined
  const under = (r: RunRow): boolean => {
    for (let p = r.parent_run_id, n = 0; p && n < 64; n++) {
      if (p === rootRun) return true
      p = rows.get(p)?.parent_run_id ?? ""
    }
    return false
  }
  const tree = [...rows.values()].filter((r) => r.id === rootRun || under(r))
  if (tree.length < 2) return undefined
  const spans: Span[] = tree.map((r) => ({
    trace_id: r.trace_id,
    span_id: r.id,
    parent_span_id: "",
    name: `invoke_agent ${r.agent}`,
    kind: "internal",
    start: r.started,
    end: r.finished ?? r.last_seen,
    status: r.status === "failed" ? "error" : "ok",
    status_message: "",
    service: "",
    attrs: {
      "gen_ai.operation.name": "invoke_agent",
      "gen_ai.agent.name": r.agent,
      "weft.run.steps": r.steps,
      "weft.run.pending": r.pending,
    },
    events: [],
    run_id: r.id,
    parent_run_id: r.parent_run_id || undefined,
  }))
  return callGraph(spans, rootRun)
}

/** The agents level drawn from the run rows (rowsCallGraph): the badge
 * that says what it leaves out and what would record the rest. */
export const ROWS_LEVEL: HoleMark = {
  hole: "not_recorded",
  cause: "no_spans",
  reason:
    "this run recorded no spans: its agents are drawn from the run rows this page read — no MCP server, no tokens, a subagent's own subagents once its node is selected",
  fix: "install a tracer (otel.Install records spans)",
}

/** The node whose runs hold `run` (the selection's), if any. */
export function nodeOfRun(g: CallGraph, run: string | undefined) {
  return run ? g.nodes.find((n) => n.runs.includes(run)) : undefined
}

// ── Words ──────────────────────────────────────────────────────────

/** A node's second line: "2 runs · slowest 4.8s · 1 failed". */
export function nodeLine(n: GraphNode): string {
  const parts = [
    plural(n.calls, n.kind === "agent" ? "run" : "call"),
    `slowest ${took(n.slowest)}`,
  ]
  if (n.tokens) parts.push(tokenPair(n.tokens.input, n.tokens.output))
  if (n.fails) parts.push(`${n.fails} failed`)
  return parts.join(" · ")
}

/** An MCP server node's third line (U23.2, O8): its failures by kind
 * in MCP_FAILURE's short words (the node names the server), "1
 * unreachable · 1 refused · 1 tool error" — a transport failure never
 * reads as a refusal or a tool error. Null when none failed so. */
export function mcpFailLine(n: GraphNode): string | null {
  const parts: string[] = []
  if (n.unreachable)
    parts.push(`${n.unreachable} ${MCP_FAILURE.mcp_transport.short}`)
  if (n.refused) parts.push(`${n.refused} ${MCP_FAILURE.mcp_rpc_error.short}`)
  if (n.toolErrors)
    parts.push(`${n.toolErrors} ${MCP_FAILURE.mcp_tool_error.short}`)
  return parts.length ? parts.join(" · ") : null
}

/** An MCP node's hover: its failures' two kinds told apart. */
export const MCP_NODE_HINT = `an MCP server (the tools' Origin, weft.tool.origin): its calls, the slowest, and failures by kind — ${Object.values(
  MCP_FAILURE
)
  .map((f) => `${f.word}: ${f.hint}`)
  .join("; ")}`

/** The least a fitted graph is scaled to: below it its text (10 px in
 * the viewBox) drops under 9 px, so the pane scrolls sideways instead —
 * the graph's own wrapper, never the page (P9-pre-web-fix2 3). */
export const GRAPH_MIN_SCALE = 0.9

/** A fitted graph's svg size: its pane's width, at most its own
 * (viewBox) width, at least GRAPH_MIN_SCALE of it. */
export function fittedSize(width: number) {
  return { maxWidth: width, minWidth: Math.ceil(width * GRAPH_MIN_SCALE) }
}

/** The fitted graph's scroll wrapper's name (it takes the focus, so a
 * graph wider than its pane scrolls from the keyboard:
 * scrollable-region-focusable). */
export const graphScrollLabel = (graph: string) =>
  `${graph} — scrolls sideways when wider than its pane`

/** A run's row where the page read one (the run document, its
 * children, an expanded child's): its status fields and steps. */
export type GraphRow = StatusRow & { steps?: number }

/** The worst status first: what an agent node's word says when its
 * runs ended differently. */
const WORST: StatusKey[] = [
  "failed",
  "cancelled",
  "interrupted",
  "parked",
  "running",
  "resumed",
  "succeeded",
]
const rank = (k: StatusKey) => {
  const i = WORST.indexOf(k)
  return i < 0 ? WORST.length : i
}

/** An agent node's status and steps (UX-S3-14): each run's status in
 * lib/status's words — its row's when the page read one, else its
 * invoke_agent span's (failed, parked on weft.run.pending, else
 * succeeded) — the worst of them with how many runs share it, and the
 * steps summed (undefined when a run's count is not known). */
export interface NodeStatus {
  status: Status
  /** Runs at that status, of all the node's runs. */
  count: number
  runs: number
  steps?: number
}

export function nodeStatus(
  n: GraphNode,
  rowOf?: (run: string) => GraphRow | undefined
): NodeStatus | undefined {
  if (n.kind !== "agent" || n.facts.length === 0) return undefined
  let worst: Status | undefined
  let count = 0
  let steps: number | undefined = 0
  for (const f of n.facts) {
    const row = rowOf?.(f.run)
    const st = statusOf(
      row ?? {
        id: f.run,
        status: f.failed ? "failed" : "succeeded",
        pending: f.pending,
      }
    )
    if (worst === undefined || rank(st.key) < rank(worst.key)) {
      worst = st
      count = 1
    } else if (st.key === worst.key) count++
    const k = row?.steps ?? f.steps
    steps = steps === undefined || k === undefined ? undefined : steps + k
  }
  return { status: worst as Status, count, runs: n.facts.length, steps }
}

/** A node's status line: "failed · 3 steps", "parked 1 of 2 · 5 steps". */
export function nodeStatusLine(s: NodeStatus): { word: string; rest: string } {
  const word =
    s.runs > 1 && s.count < s.runs
      ? `${s.status.word} ${s.count} of ${s.runs}`
      : s.status.word
  return {
    word,
    rest: s.steps !== undefined ? ` · ${plural(s.steps, "step")}` : "",
  }
}

/** An edge's label: its call count and the slowest call ("1× · 9.3ms"). */
export const edgeLabel = (e: GraphEdge) =>
  `${e.count}× · ${took(e.slowest)}${e.fails ? ` · ${e.fails} failed` : ""}`

/** The edge label's hint. */
export const EDGE_HINT = "calls along this edge · the slowest call"

/** The scale switch's hint for the graph. */
export const GRAPH_HINT =
  "graph: who called whom — agents and MCP servers, each edge's calls and its slowest call"

/** The tools record's sources by tool name, over a run's request
 * catalogs (lib/edits.ts's schemaOf walk): the graph's MCP nodes. */
export function catalogSources(
  steps: Map<number, { rows: { tools?: unknown }[] }> | undefined
): Map<string, string> {
  const out = new Map<string, string>()
  for (const st of steps?.values() ?? [])
    for (const row of st.rows) {
      const cat = row.tools as
        { tools?: { name: string; source?: string }[] } | undefined
      for (const t of cat?.tools ?? []) if (t.source) out.set(t.name, t.source)
    }
  return out
}

/** Said when child runs called tools whose catalogs the page did not
 * read: their MCP servers are not nodes here. */
export const graphUnsourced = (n: number) =>
  `MCP calls of ${n === 1 ? "1 child run" : `${n} child runs`} not shown: the graph reads this run's own tool catalog — open a child run for its MCP servers`

/** A self-call's loop label: "↻ 2× · 18.0ms". */
export const loopLabel = (e: GraphEdge) => `↻ ${edgeLabel(e)}`

/** Said when the spans hold one agent and no MCP calls. */
export const GRAPH_ALONE =
  "one agent, no subagents or MCP servers called: nothing to draw between"
