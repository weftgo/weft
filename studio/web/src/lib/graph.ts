// The call graph (U9.2; audit §6.2, UX-S3-14): the run tree's third
// scale. Who called whom, read from the same spans the tree draws
// (GET /api/runs/{id}/spans?tree=1): every agent a node (its runs, how
// long the slowest took, how many failed), every MCP server a node
// (the tools record's source, "mcp" or "mcp:<server>"), every edge a
// caller → callee pair with its call count and the slowest call. The
// selection is the tree's: a node selects its first run's row, and a
// selected row lights the node its run belongs to.
//
// Pool children (thread/pool) are sessions of their own, not runs of
// this trace: the graph does not draw them. Plain TypeScript, no React:
// Studio only (the panel links here).
import type { Span } from "./api"
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
  edges: GraphEdge[]
}

/** The tools record's source of a run's tool, when the page knows it. */
export type SourceOf = (run: string, tool: string) => string | undefined

const ms = (s: Span) => {
  const d = instantMs(s.end) - instantMs(s.start)
  return Number.isFinite(d) && d >= 0 ? d : 0
}

/** A run's parent: the invoke_agent span's weft.parent.run.id, the
 * tree answer's parent_run_id, else core's id rule (<parent>/<step>/<call>). */
function parentOf(inv: Span, run: string): string | undefined {
  const a = str(attrsOf(inv)["weft.parent.run.id"]) ?? inv.parent_run_id
  if (a) return a
  const m = /^(.+)\/\d+\/[^/]+$/.exec(run)
  return m?.[1]
}

/**
 * callGraph builds the graph from a run tree's spans: rootRun's agent
 * at depth 0, each descendant run's agent under its caller's, MCP calls
 * (a tool whose source starts "mcp") as edges to their server's node.
 * A run without its invoke_agent span is not drawn (the tree's holes
 * say why); an agent two callers reach is one node with two edges.
 */
export function callGraph(
  spans: readonly Span[],
  rootRun: string,
  sourceOf?: SourceOf
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
  if (sourceOf)
    for (const s of spans) {
      if (opOf(s) !== "execute_tool") continue
      const run = runOf(s)
      const from = agentOf.get(run)
      const tool = str(attrsOf(s)["gen_ai.tool.name"])
      const src = tool ? sourceOf(run, tool) : undefined
      if (!from || !src?.startsWith("mcp")) continue
      const id = `m:${src}`
      const n = nodeFor(id, "mcp", src)
      const d = ms(s)
      n.calls++
      n.slowest = Math.max(n.slowest, d)
      if (s.status === "error") n.fails++
      edge(from, id, d, s.status === "error")
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
  return { nodes: all, edges: out }
}

/** The node whose runs hold `run` (the selection's), if any. */
export function nodeOfRun(g: CallGraph, run: string | undefined) {
  return run ? g.nodes.find((n) => n.runs.includes(run)) : undefined
}

// ── Words ──────────────────────────────────────────────────────────

const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"}`

/** A node's second line: "2 runs · slowest 4.8s · 1 failed". */
export function nodeLine(n: GraphNode): string {
  const parts = [
    plural(n.calls, n.kind === "agent" ? "run" : "call"),
    `slowest ${took(n.slowest)}`,
  ]
  if (n.tokens) parts.push(`${n.tokens.input}→${n.tokens.output} tok`)
  if (n.fails) parts.push(`${n.fails} failed`)
  return parts.join(" · ")
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

/** Said when the spans hold one agent and no MCP calls. */
export const GRAPH_ALONE =
  "one agent, no subagents or MCP servers called: nothing to draw between"
