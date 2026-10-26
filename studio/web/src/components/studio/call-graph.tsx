// The call graph (U9.2, audit §6.2): lib/graph.ts's nodes in columns by
// call depth, edges labelled with their call count and slowest call.
// Inline SVG, no layout library: the graph is small (agents and MCP
// servers, never spans). A node is a button: it selects its first
// run's row, the selection the tree and the detail pane share. A
// self-call (an agent delegating to itself) is a loop over its node.
// An agent node carries its runs' own cost (U34-fix, UX-S3-14: the run
// rows' figures in lib/cost.ts's words, "—" with the reason unknown),
// and its status and steps (U34-fix4, UX-S3-14: lib/status's word in
// its palette role — a failed or parked subagent reads so, its node
// stroked in the role's mark). The nodes are one roving tab stop (the
// loop graph's rule): ↑ ↓ through the nodes, ← → across the columns,
// Home / End, the stop on the selected node; the drawing fits its pane,
// never scrolling the page sideways beside the drawer.
import { useRef, useState } from "react"
import type { KeyboardEvent as ReactKeyboardEvent } from "react"

import type { CostView } from "@/lib/cost"
import type { CallGraph, GraphNode, GraphRow } from "@/lib/graph"
import {
  EDGE_HINT,
  GRAPH_ALONE,
  fittedSize,
  graphScrollLabel,
  edgeLabel,
  graphUnsourced,
  loopLabel,
  nodeLine,
  nodeStatus,
  nodeStatusLine,
} from "@/lib/graph"
import { ROLE_SVG, TONE_SVG, studioVar } from "@/lib/status-class"
import { GraphHint, GraphTip } from "@/components/studio/graph-tip"
import type { StatusKey } from "@/lib/status"

/** The statuses a node is coloured by (its stroke and name): the ones
 * that need a look. A succeeded or running node keeps the quiet frame;
 * its word still carries its colour. */
const LOUD: ReadonlySet<StatusKey> = new Set([
  "failed",
  "cancelled",
  "interrupted",
  "parked",
])

const W = 230
const H = 62
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
  rowOf,
}: {
  graph: CallGraph
  /** The selected node's id. */
  selected?: string
  onSelect: (n: GraphNode) => void
  /** Runs' own cost, summed (capability "pricing"; absent without it). */
  costOf?: (runs: readonly string[]) => CostView | undefined
  /** The run rows the page read (status, pending, steps): a node's
   * status line; a run without one reads its invoke_agent span. */
  rowOf?: (run: string) => GraphRow | undefined
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
  const tone = (fails: number) =>
    fails ? TONE_SVG.error.stroke : "stroke-border"
  // ── One roving tab stop: column by column, top to bottom ──
  const order = [...cols.entries()]
    .sort(([a], [b]) => a - b)
    .flatMap(([, list]) => list.map((n) => n.id))
  const [focus, setFocus] = useState<string>()
  // The focused node's hint, shown under the graph (GraphHint).
  const [tip, setTip] = useState<string>()
  const stop =
    focus && order.includes(focus)
      ? focus
      : selected && order.includes(selected)
        ? selected
        : order[0]
  const refs = useRef(new Map<string, SVGGElement>())
  const move = (to: string | undefined) => {
    if (!to) return
    setFocus(to)
    refs.current.get(to)?.focus()
  }
  // The node of the next column (dir) nearest this one's row.
  const across = (id: string, dir: 1 | -1) => {
    const p = at.get(id)
    if (!p) return undefined
    const x = p.x + dir * (W + GX)
    const dy = (o: string) => Math.abs((at.get(o)?.y ?? 0) - p.y)
    return order
      .filter((o) => at.get(o)?.x === x)
      .sort((a, b) => dy(a) - dy(b))[0]
  }
  const onKey = (n: GraphNode) => (e: ReactKeyboardEvent) => {
    const k = e.key
    if (k === "Enter" || k === " ") {
      e.preventDefault()
      onSelect(n)
      return
    }
    const i = order.indexOf(n.id)
    const to =
      k === "ArrowDown"
        ? order[i + 1]
        : k === "ArrowUp"
          ? order[i - 1]
          : k === "ArrowRight"
            ? across(n.id, 1)
            : k === "ArrowLeft"
              ? across(n.id, -1)
              : k === "Home"
                ? order[0]
                : k === "End"
                  ? order.at(-1)
                  : undefined
    if (to) {
      e.preventDefault()
      move(to)
    }
  }
  return (
    <div
      className="rounded-lg border bg-background p-2 font-mono text-[11px]"
      data-call-graph
    >
      {/* Fitted: at most its own size, scaled down to its pane, never
          under GRAPH_MIN_SCALE — past that this wrapper scrolls. */}
      <div
        className="max-w-full min-w-0 overflow-x-auto"
        tabIndex={0}
        role="region"
        aria-label={graphScrollLabel("call graph")}
        data-graph-scroll
      >
        <svg
          width="100%"
          viewBox={`0 0 ${width} ${height}`}
          style={fittedSize(width)}
          className="block h-auto"
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
            const st = nodeStatus(n, rowOf)
            const line = st ? nodeStatusLine(st) : undefined
            const paint =
              ROLE_SVG[st ? studioVar(st.status.role) : "muted-foreground"]
            const loud = st !== undefined && LOUD.has(st.status.key)
            return (
              <GraphTip key={n.id} hint={cost?.title}>
                <g
                  transform={`translate(${p.x},${p.y})`}
                  ref={(el: SVGGElement | null) => {
                    if (el) refs.current.set(n.id, el)
                    else refs.current.delete(n.id)
                  }}
                  role="button"
                  tabIndex={n.id === stop ? 0 : -1}
                  aria-pressed={on}
                  // The name starts with the visible words, in their order
                  // (axe label-content-name-mismatch): the glyph is aria-hidden.
                  aria-label={`${n.label} ${nodeLine(n)}${line ? ` ${line.word}${line.rest}` : ""}${cost ? ` ${cost.text}` : ""}`}
                  // The cost's reason: no <title> inside the button (axe
                  // counts it as visible text) — GraphTip's wraps it.
                  aria-description={cost?.title}
                  data-node={n.id}
                  data-node-status={st?.status.key}
                  className="group cursor-pointer outline-none"
                  onClick={() => {
                    setFocus(n.id)
                    onSelect(n)
                  }}
                  onFocus={() => {
                    setFocus(n.id)
                    setTip(cost?.title)
                  }}
                  onBlur={() => setTip(undefined)}
                  onKeyDown={onKey(n)}
                >
                  {/* Selected: the thread's 2px frame on the page's fill
                  (a status word in its ink clears 4.5:1 on paper; the
                  accent wash took interrupted's mustard to 4.19). */}
                  <rect
                    width={W}
                    height={H}
                    rx={6}
                    className={`fill-background ${
                      on
                        ? "stroke-thread"
                        : loud
                          ? paint.stroke
                          : n.fails
                            ? TONE_SVG.error.stroke
                            : "stroke-border"
                    }`}
                    strokeWidth={on || loud ? 2 : 1}
                    data-node-frame
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
                    className={`text-[11px] font-medium ${
                      loud
                        ? paint.fill
                        : n.fails
                          ? TONE_SVG.error.fill
                          : "fill-foreground"
                    }`}
                  >
                    <tspan aria-hidden>
                      {n.kind === "agent" ? "▣ " : "⧉ "}
                    </tspan>
                    {n.label}{" "}
                  </text>
                  <text
                    x={10}
                    y={34}
                    className="fill-muted-foreground text-[10px]"
                  >
                    {nodeLine(n)}{" "}
                  </text>
                  {line ? (
                    <text x={10} y={50} className="text-[10px]" data-node-line>
                      <tspan className={paint.fill} data-node-word>
                        {line.word}
                      </tspan>
                      <tspan className="fill-muted-foreground">
                        {line.rest}{" "}
                      </tspan>
                    </text>
                  ) : null}
                  {cost ? (
                    <text
                      x={W - 8}
                      y={18}
                      textAnchor="end"
                      className="fill-muted-foreground text-[10px]"
                      data-cost-known={cost.known ? "true" : "false"}
                      data-cost-title={cost.title}
                      data-graph-cost
                    >
                      {cost.text}
                    </text>
                  ) : null}
                </g>
              </GraphTip>
            )
          })}
        </svg>
      </div>
      <GraphHint hint={tip} />
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
