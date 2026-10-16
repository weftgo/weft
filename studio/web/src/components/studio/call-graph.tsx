// The call graph (U9.2, audit §6.2): lib/graph.ts's nodes in columns by
// call depth, edges labelled with their call count and slowest call.
// Inline SVG, no layout library: the graph is small (agents and MCP
// servers, never spans). A node is a button: it selects its first
// run's row, the selection the tree and the detail pane share.
import type { CallGraph, GraphNode } from "@/lib/graph"
import { EDGE_HINT, GRAPH_ALONE, edgeLabel, nodeLine } from "@/lib/graph"

const W = 230
const H = 46
const GX = 110
const GY = 22
const PAD = 12

export function CallGraphView({
  graph,
  selected,
  onSelect,
}: {
  graph: CallGraph
  /** The selected node's id. */
  selected?: string
  onSelect: (n: GraphNode) => void
}) {
  const cols = new Map<number, GraphNode[]>()
  for (const n of graph.nodes) {
    const c = cols.get(n.depth)
    if (c) c.push(n)
    else cols.set(n.depth, [n])
  }
  const at = new Map<string, { x: number; y: number }>()
  let rows = 1
  let ncols = 1
  for (const [d, list] of cols) {
    ncols = Math.max(ncols, d + 1)
    rows = Math.max(rows, list.length)
    list.forEach((n, i) =>
      at.set(n.id, { x: PAD + d * (W + GX), y: PAD + i * (H + GY) })
    )
  }
  const width = PAD * 2 + ncols * W + (ncols - 1) * GX
  const height = PAD * 2 + rows * H + (rows - 1) * GY
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
          const x1 = a.x + W
          const y1 = a.y + H / 2
          const x2 = b.x
          const y2 = b.y + H / 2
          const mx = (x1 + x2) / 2
          return (
            <g key={`${e.from}>${e.to}`} data-edge={`${e.from}>${e.to}`}>
              <path
                d={`M${x1},${y1} C${mx},${y1} ${mx},${y2} ${x2 - 4},${y2}`}
                className={`fill-none ${e.fails ? "stroke-ev-error" : "stroke-border"}`}
                strokeWidth={1.5}
              />
              <path
                d={`M${x2 - 8},${y2 - 4} L${x2},${y2} L${x2 - 8},${y2 + 4}`}
                className={`fill-none ${e.fails ? "stroke-ev-error" : "stroke-border"}`}
                strokeWidth={1.5}
              />
              <text
                x={mx}
                y={(y1 + y2) / 2 - 5}
                textAnchor="middle"
                className="fill-muted-foreground text-[10px]"
                data-edge-label
              >
                <title>{EDGE_HINT}</title>
                {edgeLabel(e)}
              </text>
            </g>
          )
        })}
        {graph.nodes.map((n) => {
          const p = at.get(n.id)!
          const on = n.id === selected
          return (
            <g
              key={n.id}
              transform={`translate(${p.x},${p.y})`}
              role="button"
              tabIndex={0}
              aria-pressed={on}
              aria-label={`${n.label}: ${nodeLine(n)}`}
              data-node={n.id}
              className="cursor-pointer outline-none"
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
              <text
                x={10}
                y={18}
                className={`text-[11px] font-medium ${n.fails ? "fill-ev-error" : "fill-foreground"}`}
              >
                {n.kind === "agent" ? "▣ " : "⧉ "}
                {n.label}
              </text>
              <text x={10} y={34} className="fill-faint text-[10px]">
                {nodeLine(n)}
              </text>
            </g>
          )
        })}
      </svg>
      {graph.edges.length === 0 ? (
        <p className="px-1 pt-1 text-faint">{GRAPH_ALONE}</p>
      ) : null}
    </div>
  )
}
