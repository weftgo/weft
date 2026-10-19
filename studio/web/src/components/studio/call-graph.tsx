// The call graph (U9.2, audit §6.2): lib/graph.ts's nodes in columns by
// call depth, edges labelled with their call count and slowest call.
// Inline SVG, no layout library: the graph is small (agents and MCP
// servers, never spans). A node is a button: it selects its first
// run's row, the selection the tree and the detail pane share. A
// self-call (an agent delegating to itself) is a loop over its node.
// An agent node carries its runs' own cost (U34-fix, UX-S3-14: the run
// rows' figures in lib/cost.ts's words, "—" with the reason unknown).
import type { CostView } from "@/lib/cost"
import type { CallGraph, GraphNode } from "@/lib/graph"
import {
  EDGE_HINT,
  GRAPH_ALONE,
  edgeLabel,
  graphUnsourced,
  loopLabel,
  nodeLine,
} from "@/lib/graph"

const W = 230
const H = 46
const GX = 110
const GY = 22
const PAD = 12
/** Room above the first row for a self-call's loop. */
const LOOP = 20

export function CallGraphView({
  graph,
  selected,
  onSelect,
  costOf,
}: {
  graph: CallGraph
  /** The selected node's id. */
  selected?: string
  onSelect: (n: GraphNode) => void
  /** Runs' own cost, summed (capability "pricing"; absent without it). */
  costOf?: (runs: readonly string[]) => CostView | undefined
}) {
  const cols = new Map<number, GraphNode[]>()
  for (const n of graph.nodes) {
    const c = cols.get(n.depth)
    if (c) c.push(n)
    else cols.set(n.depth, [n])
  }
  const loops = graph.edges.some((e) => e.from === e.to)
  const top = PAD + (loops ? LOOP : 0)
  const at = new Map<string, { x: number; y: number }>()
  let rows = 1
  let ncols = 1
  for (const [d, list] of cols) {
    ncols = Math.max(ncols, d + 1)
    rows = Math.max(rows, list.length)
    list.forEach((n, i) =>
      at.set(n.id, { x: PAD + d * (W + GX), y: top + i * (H + GY) })
    )
  }
  const width = PAD * 2 + ncols * W + (ncols - 1) * GX
  const height = top + PAD + rows * H + (rows - 1) * GY
  const tone = (fails: number) => (fails ? "stroke-ev-error" : "stroke-border")
  return (
    <div
      className="overflow-auto rounded-lg border bg-background p-2 font-mono text-[11px]"
      data-call-graph
    >
      <svg
        width={width}
        height={height}
        viewBox={`0 0 ${width} ${height}`}
        role="group"
        aria-label="call graph"
      >
        {graph.edges.map((e) => {
          const a = at.get(e.from)
          const b = at.get(e.to)
          if (!a || !b) return null
          if (e.from === e.to) {
            // A loop over the node's top edge, its label beside it.
            const x1 = a.x + W * 0.55
            const x2 = a.x + W * 0.8
            const y = a.y
            return (
              <g
                key={`${e.from}>${e.to}`}
                data-edge={`${e.from}>${e.to}`}
                data-self-loop
              >
                <title>{EDGE_HINT}</title>
                <path
                  d={`M${x1},${y} C${x1},${y - 16} ${x2},${y - 16} ${x2},${y - 3}`}
                  className={`fill-none ${tone(e.fails)}`}
                  strokeWidth={1.5}
                />
                <path
                  d={`M${x2 - 4},${y - 7} L${x2},${y - 1} L${x2 + 4},${y - 7}`}
                  className={`fill-none ${tone(e.fails)}`}
                  strokeWidth={1.5}
                />
                <text
                  x={x2 + 6}
                  y={y - 8}
                  className="fill-muted-foreground text-[10px]"
                  data-edge-label
                >
                  {loopLabel(e)}
                </text>
              </g>
            )
          }
          const x1 = a.x + W
          const y1 = a.y + H / 2
          const x2 = b.x
          const y2 = b.y + H / 2
          const mx = (x1 + x2) / 2
          return (
            <g key={`${e.from}>${e.to}`} data-edge={`${e.from}>${e.to}`}>
              <title>{EDGE_HINT}</title>
              <path
                d={`M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2 - 4},${y2}`}
                className={`fill-none ${tone(e.fails)}`}
                strokeWidth={1.5}
              />
              <path
                d={`M${x2 - 8},${y2 - 4} L${x2},${y2} L${x2 - 8},${y2 + 4}`}
                className={`fill-none ${tone(e.fails)}`}
                strokeWidth={1.5}
              />
              <text
                x={mx}
                y={(y1 + y2) / 2 - 5}
                textAnchor="middle"
                className="fill-muted-foreground text-[10px]"
                data-edge-label
              >
                {edgeLabel(e)}
              </text>
            </g>
          )
        })}
        {graph.nodes.map((n) => {
          const p = at.get(n.id)!
          const on = n.id === selected
          const cost = n.kind === "agent" ? costOf?.(n.runs) : undefined
          return (
            <g
              key={n.id}
              transform={`translate(${p.x},${p.y})`}
              role="button"
              tabIndex={0}
              aria-pressed={on}
              // The name starts with the visible words, in their order
              // (axe label-content-name-mismatch): the glyph is aria-hidden.
              aria-label={`${n.label} ${nodeLine(n)}${cost ? ` ${cost.text}` : ""}`}
              data-node={n.id}
              className="group cursor-pointer outline-none"
              onClick={() => onSelect(n)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault()
                  onSelect(n)
                }
              }}
            >
              <rect
                width={W}
                height={H}
                rx={6}
                className={`${on ? "fill-accent stroke-thread" : "fill-background"} ${
                  n.fails ? "stroke-ev-error" : on ? "" : "stroke-border"
                }`}
                strokeWidth={on ? 2 : 1}
              />
              {/* The keyboard focus: a ring outside the node (its own
                  stroke says selected or failed). */}
              <rect
                x={-3}
                y={-3}
                width={W + 6}
                height={H + 6}
                rx={8}
                className="fill-none stroke-ring opacity-0 group-focus-visible:opacity-100"
                strokeWidth={2}
                aria-hidden
                data-focus-ring
              />
              <text
                x={10}
                y={18}
                className={`text-[11px] font-medium ${n.fails ? "fill-ev-error" : "fill-foreground"}`}
              >
                <tspan aria-hidden>{n.kind === "agent" ? "▣ " : "⧉ "}</tspan>
                {n.label}{" "}
              </text>
              <text x={10} y={34} className="fill-muted-foreground text-[10px]">
                {nodeLine(n)}{" "}
              </text>
              {cost ? (
                <text
                  x={W - 8}
                  y={18}
                  textAnchor="end"
                  className="fill-muted-foreground text-[10px]"
                  data-cost-known={cost.known ? "true" : "false"}
                  data-graph-cost
                >
                  <title>{cost.title}</title>
                  {cost.text}
                </text>
              ) : null}
            </g>
          )
        })}
      </svg>
      {graph.edges.length === 0 ? (
        <p className="px-1 pt-1 text-muted-foreground">{GRAPH_ALONE}</p>
      ) : null}
      {graph.unsourced.length > 0 ? (
        <p className="px-1 pt-1 text-muted-foreground" data-graph-unsourced>
          {graphUnsourced(graph.unsourced.length)}
        </p>
      ) : null}
    </div>
  )
}
