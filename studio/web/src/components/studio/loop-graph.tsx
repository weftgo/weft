// The loop graph (U34; WEFT-LOOP-GRAPH.md §3): lib/loopgraph.ts's model
// drawn — prompt → model ⇄ one node per tool → end — with the insight
// strip above, the breadcrumb of the subagents expanded in place, the
// step scrubber below. Inline SVG, a deterministic layout, no layout
// library (the U9.2 precedent): prompt and model on the left, the tools
// in one column on the right (top to bottom by first use, MCP tools in
// their server's frame, at most 12 before "+N tools"), the end under
// the model, the tools offered and never called dashed and faint below.
//
// Every node is a button on one roving tab stop (→ follows the calls, ←
// back to the caller, ↑ ↓ along the tool column, Enter selects, Esc
// clears); the selection is the run tree's ?sel=, so the same drawer
// opens and switching scales keeps it. An edge selects the first call
// along it (§3.4). A subagent node carries its child run's own cost
// (lib/cost.ts's words: "—" with the reason when unknown). The words
// are lib/loopgraph.ts's, the colours the palette's roles
// (lib/palette.ts: error-as-data mustard, a park in the parked colour),
// a hole the A3 table's badge (lib/honesty.ts).
import { useEffect, useMemo, useRef, useState } from "react"
import type { KeyboardEvent as ReactKeyboardEvent } from "react"

import {
  CALLS_HINT,
  DEEP_HANDOFF_DEPTH,
  END,
  endTokens,
  EXPAND,
  INSIGHTS_LABEL,
  litBy,
  loopInsights,
  MODEL,
  MODEL_LABEL,
  modelLine,
  MORE_TOOLS,
  NEVER_CALLED,
  NEVER_HINT,
  PAUSE,
  PLAY,
  PLAY_OFF_HINT,
  PROMPT,
  PROMPT_LABEL,
  RESULTS_HINT,
  STEPS_LABEL,
  toolHint,
  toolLine,
  toolMarks,
} from "@/lib/loopgraph"
import type { EndTone, LoopCall, LoopGraph, LoopTool } from "@/lib/loopgraph"
import { sumCost } from "@/lib/cost"
import type { CostView } from "@/lib/cost"
import type { HoleMark } from "@/lib/honesty"
import { useMediaQuery } from "@/hooks/use-media-query"
import { OPEN_RUN } from "@/lib/waterfall"
import { HoleBadge } from "@/components/studio/hole-badge"

const PAD = 12
const PW = 110 // the prompt node
const MW = 150 // the model node
const TW = 230 // a tool node
const H = 46
const GY = 14
const GX = 90
const MAX_TOOLS = 12

/** The palette's roles as the literal classes Tailwind needs. */
const STROKE: Record<EndTone | "error" | "idle", string> = {
  ok: "stroke-status-ok",
  bad: "stroke-status-bad",
  parked: "stroke-status-parked",
  running: "stroke-status-run",
  error: "stroke-ev-error",
  idle: "stroke-border",
}
const TEXT: Record<EndTone | "error" | "idle", string> = {
  ok: "fill-status-ok",
  bad: "fill-status-bad",
  parked: "fill-status-parked",
  running: "fill-status-run-ink",
  error: "fill-ev-error",
  idle: "fill-foreground",
}

const toolTone = (t: LoopTool): keyof typeof STROKE =>
  t.parked || t.denied
    ? "parked"
    : t.running
      ? "running"
      : t.fails
        ? "error"
        : "idle"

export interface Crumb {
  run: string
  label: string
}

/** The row a call selects: its own, or (a parked call no row draws)
 * its step's. */
const keyOf = (c: LoopCall | undefined) => c && (c.at ?? c.key)

/** A subagent node's cost: its child runs' own figures, summed. */
function agentCost(
  t: LoopTool,
  costOf: ((run: string) => CostView | undefined) | undefined
): CostView | undefined {
  if (!t.agent || !costOf) return undefined
  const kids = [...new Set(t.calls.flatMap((c) => (c.child ? [c.child] : [])))]
  if (!kids.length) return undefined
  // A row not read yet is sumCost's "—" with its reason, never nothing.
  const views = kids.map(costOf)
  return views.length === 1 ? (views[0] ?? sumCost(views)) : sumCost(views)
}

export function LoopGraphView({
  graph,
  chain,
  selectedNode,
  step,
  onSelect,
  onClear,
  onStep,
  onFocusRun,
  depth,
  openRun,
  offeredHole,
  offeredError,
  costOf,
}: {
  graph: LoopGraph
  /** The runs from the root down to this one (the breadcrumb). */
  chain: Crumb[]
  /** The node the selection lights. */
  selectedNode?: string
  /** The scrubber's step (?step=), when one is named. */
  step?: number
  /** A node's selection: the row key it selects. */
  onSelect: (key: string) => void
  onClear: () => void
  onStep: (n: number | undefined) => void
  /** Expand a subagent in place (or step back up the breadcrumb). */
  onFocusRun: (run: string) => void
  /** This loop's depth under the page's run (0: its own). */
  depth: number
  /** A subagent too deep to expand: its run page. */
  openRun: (run: string) => string
  /** offered null: why the tools offered and never called are unknown
   * (a not_recorded badge); absent while the catalog is being read. */
  offeredHole?: HoleMark
  /** The requests read failed: its words, drawn as the attempts pane
   * draws a failed read (no badge: the A3 table's causes are a
   * record's, not a read's). */
  offeredError?: string
  /** A run's own cost (capability "pricing"; absent without it). */
  costOf?: (run: string) => CostView | undefined
}) {
  const reduced = useMediaQuery("(prefers-reduced-motion: reduce)")
  const [more, setMore] = useState(false)
  const shown = more ? graph.tools : graph.tools.slice(0, MAX_TOOLS)
  const hidden = graph.tools.length - shown.length
  const offered = graph.offered ?? []
  const insights = useMemo(() => loopInsights(graph), [graph])
  const lit = useMemo(
    () => (step === undefined ? undefined : litBy(graph, step)),
    [graph, step]
  )

  // ── Layout ──
  const colX = [PAD, PAD + PW + 40, PAD + PW + 40 + MW + GX]
  const rows = shown.length + (hidden ? 1 : 0) + offered.length
  const toolsH = Math.max(1, rows) * (H + GY) - GY
  const modelY = PAD + Math.max(0, Math.min(toolsH / 2 - H, 60))
  const endY = modelY + H + 48
  const height = Math.max(PAD + toolsH, endY + H) + PAD
  const width = colX[2] + TW + PAD
  const toolY = (i: number) => PAD + i * (H + GY)
  const mx = colX[1] + MW
  const my = modelY + H / 2

  // ── Play (never automatic; off under reduced motion) ──
  const [playing, setPlaying] = useState(false)
  const steps = useMemo(() => graph.steps.map((s) => s.n), [graph])
  const stepRef = useRef(step)
  stepRef.current = step
  useEffect(() => {
    if (!playing || reduced) return
    const id = window.setInterval(() => {
      const i = steps.indexOf(stepRef.current ?? -1)
      const next = steps.at(i + 1)
      if (next === undefined) setPlaying(false)
      else onStep(next)
    }, 800)
    return () => window.clearInterval(id)
  }, [playing, reduced, steps, onStep])

  // ── Keyboard: one roving tab stop over every node ──
  const order = [
    PROMPT,
    MODEL,
    ...shown.map((t) => t.id),
    ...(hidden ? ["more"] : []),
    ...offered.map((n) => `o:${n}`),
    END,
  ]
  const [focus, setFocus] = useState<string>(selectedNode ?? PROMPT)
  const stop = order.includes(focus) ? focus : (selectedNode ?? PROMPT)
  const refs = useRef(new Map<string, SVGGElement>())
  // A never-called tool has no row to select: an insight naming one
  // focuses and marks its node — until any other selection (a node, an
  // edge, an insight, Esc, or the page's ?sel= moving) clears it: one
  // node pressed, never two.
  const [picked, setPicked] = useState<string>()
  const [pickedAt, setPickedAt] = useState(selectedNode)
  if (pickedAt !== selectedNode) {
    setPickedAt(selectedNode)
    if (picked) setPicked(undefined)
  }
  const select = (key: string) => {
    setPicked(undefined)
    onSelect(key)
  }
  const clear = () => {
    setPicked(undefined)
    onClear()
  }
  // The node pressed: the picked never-called one, else the selection's.
  const pressed = picked ?? selectedNode
  const move = (to: string | undefined) => {
    if (!to) return
    setFocus(to)
    refs.current.get(to)?.focus()
  }
  const childOf = (t: LoopTool) => t.calls.find((c) => c.child)?.child
  const deep = depth + 1 > DEEP_HANDOFF_DEPTH
  const expandable = (id: string) => {
    const t = shown.find((x) => x.id === id)
    return t && !deep ? childOf(t) : undefined
  }
  const column = order.filter(
    (n) => n.startsWith("t:") || n === "more" || n.startsWith("o:")
  )
  const onKey = (id: string, act: () => void) => (e: ReactKeyboardEvent) => {
    const i = column.indexOf(id)
    const k = e.key
    let to: string | undefined
    if (k === "Enter" || k === " ") {
      e.preventDefault()
      act()
      return
    }
    if (k === "Escape") {
      e.preventDefault()
      clear()
      return
    }
    if (k === "ArrowRight") {
      // A subagent's → follows its calls into its own loop.
      const child = expandable(id)
      if (child) {
        e.preventDefault()
        onFocusRun(child)
        return
      }
      to = id === PROMPT ? MODEL : id === MODEL ? (column[0] ?? END) : undefined
    } else if (k === "ArrowLeft")
      to = id === MODEL ? PROMPT : id === PROMPT ? undefined : MODEL
    else if (k === "ArrowDown")
      to = i >= 0 ? (column.at(i + 1) ?? END) : id === MODEL ? END : undefined
    else if (k === "ArrowUp")
      to = i > 0 ? column[i - 1] : id === END ? MODEL : undefined
    else if (k === "Home") to = order[0]
    else if (k === "End") to = order.at(-1)
    if (to) {
      e.preventDefault()
      move(to)
    }
  }
  const nodeProps = (id: string, label: string, act: () => void) => ({
    ref: (n: SVGGElement | null) => {
      if (n) refs.current.set(id, n)
      else refs.current.delete(id)
    },
    role: "button",
    tabIndex: id === stop ? 0 : -1,
    "aria-pressed": id === pressed,
    // The name starts with the visible words (axe label-content-name-mismatch).
    "aria-label": label,
    "data-loop-node": id,
    className: "group cursor-pointer outline-none",
    onClick: () => {
      setFocus(id)
      act()
    },
    onFocus: () => setFocus(id),
    onKeyDown: onKey(id, act),
  })
  const ring = (w: number) => (
    <rect
      x={-3}
      y={-3}
      width={w + 6}
      height={H + 6}
      rx={8}
      className="fill-none stroke-ring opacity-0 group-focus-visible:opacity-100"
      strokeWidth={2}
      aria-hidden
      data-focus-ring
    />
  )
  const dimmed = (id: string) =>
    lit !== undefined && !lit.has(id) && id !== MODEL ? 0.3 : 1

  // MCP frames: consecutive shown tools of one mcp source.
  const frames: { source: string; from: number; to: number }[] = []
  shown.forEach((t, i) => {
    if (!t.source?.startsWith("mcp")) return
    const f = frames.at(-1)
    if (f && f.source === t.source && f.to === i - 1) f.to = i
    else frames.push({ source: t.source, from: i, to: i })
  })

  const firstStep = graph.steps[0]?.key
  const lastKey = graph.end.key
  return (
    <div
      className="space-y-2 rounded-lg border bg-background p-2 font-mono text-[11px]"
      data-loop-graph={graph.runId}
    >
      {chain.length > 1 ? (
        <nav aria-label="subagents" data-loop-crumbs>
          <ol className="flex flex-wrap items-center gap-1">
            {chain.map((c, i) => (
              <li key={c.run} className="flex items-center gap-1">
                {i ? <span aria-hidden>›</span> : null}
                {i === chain.length - 1 ? (
                  <span aria-current="page" className="text-foreground">
                    {c.label}
                  </span>
                ) : (
                  <button
                    type="button"
                    className="text-thread-ink hover:underline"
                    onClick={() => onFocusRun(c.run)}
                    data-loop-crumb={c.run}
                  >
                    {c.label}
                  </button>
                )}
              </li>
            ))}
          </ol>
        </nav>
      ) : null}
      {insights.length ? (
        <div
          className="flex flex-wrap items-center gap-1.5"
          role="group"
          aria-label={INSIGHTS_LABEL}
          data-loop-insights
        >
          <span className="text-muted-foreground">{INSIGHTS_LABEL} ▸</span>
          {insights.map((ins, i) => (
            <button
              key={`${ins.kind}${i}`}
              type="button"
              className="rounded-md border px-1.5 py-0.5 text-ev-error hover:bg-secondary"
              data-insight={ins.kind}
              onClick={() => {
                if (ins.node.startsWith("o:")) {
                  setPicked(ins.node)
                  move(ins.node)
                  return
                }
                const t = graph.tools.find((x) => x.id === ins.node)
                const key =
                  ins.node === END
                    ? lastKey
                    : ins.node === MODEL
                      ? firstStep
                      : keyOf(t?.calls[0])
                if (key) select(key)
              }}
            >
              {ins.text}
            </button>
          ))}
        </div>
      ) : null}
      <div className="overflow-auto">
        <svg
          width={width}
          height={height}
          viewBox={`0 0 ${width} ${height}`}
          role="group"
          aria-label="loop graph"
        >
          {frames.map((f) => (
            <g key={`${f.source}${f.from}`} data-mcp-frame={f.source}>
              <rect
                x={colX[2] - 6}
                y={toolY(f.from) - 6}
                width={TW + 12}
                height={toolY(f.to) - toolY(f.from) + H + 12}
                rx={8}
                className="fill-none stroke-border"
                strokeDasharray="3 3"
              />
              <text
                x={colX[2] + TW + 2}
                y={toolY(f.from) - 8}
                textAnchor="end"
                className="fill-muted-foreground text-[9px]"
              >
                {f.source}
              </text>
            </g>
          ))}
          {/* prompt → model */}
          <path
            d={`M${colX[0] + PW},${my} L${colX[1] - 4},${my}`}
            className="fill-none stroke-border"
            strokeWidth={1.5}
          />
          <path
            d={`M${colX[1] - 9},${my - 4} L${colX[1] - 1},${my} L${colX[1] - 9},${my + 4}`}
            className="fill-none stroke-border"
            strokeWidth={1.5}
          />
          {/* model → end */}
          <path
            d={`M${colX[1] + MW / 2},${modelY + H} L${colX[1] + MW / 2},${endY - 2}`}
            className={`cursor-pointer fill-none ${STROKE[graph.end.tone === "ok" ? "idle" : graph.end.tone]}`}
            strokeWidth={1.5}
            data-loop-edge="model>end"
            onClick={() => (lastKey ? select(lastKey) : undefined)}
          />
          {shown.map((t, i) => {
            const y = toolY(i) + H / 2
            const x2 = colX[2]
            const c = (mx + x2) / 2
            const w = 1 + Math.min(2, Math.log2(t.calls.length || 1))
            const on = t.id === pressed || (lit?.has(t.id) ?? false)
            const back =
              t.parked || t.denied ? "parked" : t.fails ? "error" : "idle"
            const first = keyOf(t.calls[0])
            return (
              <g
                key={`e${t.id}`}
                className="group/edge cursor-pointer"
                opacity={dimmed(t.id)}
                data-loop-edge={`model>${t.id}`}
                data-lit={lit?.has(t.id) ? "" : undefined}
                // The first call along it (the node's own button is the
                // keyboard's way to the same selection).
                onClick={() => (first ? select(first) : undefined)}
              >
                <title>{`${CALLS_HINT} ×${t.calls.length} · ${RESULTS_HINT}`}</title>
                <path
                  d={`M${mx},${my - 6} C${c},${my - 6} ${c},${y - 6} ${x2 - 4},${y - 6}`}
                  className={`fill-none ${on ? "stroke-thread" : "stroke-border"}`}
                  strokeWidth={on ? w + 0.5 : w}
                />
                <path
                  d={`M${x2 - 9},${y - 10} L${x2 - 1},${y - 6} L${x2 - 9},${y - 2}`}
                  className={`fill-none ${on ? "stroke-thread" : "stroke-border"}`}
                  strokeWidth={1.5}
                />
                <path
                  d={`M${x2},${y + 6} C${c},${y + 6} ${c},${my + 6} ${mx + 4},${my + 6}`}
                  className={`fill-none ${STROKE[back]}`}
                  strokeWidth={1.2}
                  strokeDasharray={back === "parked" ? "4 3" : undefined}
                  data-loop-result={back}
                />
                <text
                  x={c}
                  y={(my + y) / 2 - 8}
                  textAnchor="middle"
                  className={`fill-muted-foreground text-[10px] ${on ? "" : "opacity-0 group-hover/edge:opacity-100"}`}
                  aria-hidden
                  data-edge-label
                >
                  ×{t.calls.length}
                </text>
              </g>
            )
          })}
          {/* The prompt */}
          <g
            transform={`translate(${colX[0]},${modelY})`}
            {...nodeProps(PROMPT, PROMPT_LABEL, () => select(graph.runKey))}
          >
            <rect
              width={PW}
              height={H}
              rx={6}
              className={
                pressed === PROMPT
                  ? "fill-accent stroke-thread"
                  : "fill-background stroke-border"
              }
              strokeWidth={pressed === PROMPT ? 2 : 1}
            />
            {ring(PW)}
            <text x={10} y={27} className="fill-foreground text-[11px]">
              {PROMPT_LABEL}
            </text>
          </g>
          {/* The model */}
          <g
            transform={`translate(${colX[1]},${modelY})`}
            {...nodeProps(MODEL, `${MODEL_LABEL} ${modelLine(graph)}`, () =>
              firstStep ? select(firstStep) : undefined
            )}
          >
            <rect
              width={MW}
              height={H}
              rx={6}
              className={
                pressed === MODEL
                  ? "fill-accent stroke-thread"
                  : "fill-background stroke-border"
              }
              strokeWidth={pressed === MODEL ? 2 : 1}
            />
            {ring(MW)}
            <text
              x={10}
              y={18}
              className="fill-foreground text-[11px] font-medium"
            >
              <tspan aria-hidden>◆ </tspan>
              {MODEL_LABEL}{" "}
            </text>
            <text x={10} y={34} className="fill-muted-foreground text-[10px]">
              {modelLine(graph)}
            </text>
          </g>
          {/* The tools */}
          {shown.map((t, i) => {
            const on = t.id === pressed
            const tone = toolTone(t)
            const cost = agentCost(t, costOf)
            const label = `${t.agent ?? t.name} ${toolMarks(t)} ${toolLine(t)}${cost ? ` ${cost.text}` : ""}`
            return (
              <g
                key={t.id}
                transform={`translate(${colX[2]},${toolY(i)})`}
                opacity={dimmed(t.id)}
                {...nodeProps(t.id, label, () => {
                  const k = keyOf(t.calls[0])
                  if (k) select(k)
                })}
                data-calls={t.calls.length}
                data-marks={toolMarks(t)}
                data-fails={t.fails}
                data-parked={t.parked || undefined}
                data-denied={t.denied || undefined}
                data-agent={t.agent}
              >
                <title>{toolHint(t)}</title>
                <rect
                  width={TW}
                  height={H}
                  rx={t.agent ? 2 : 6}
                  className={`${on ? "fill-accent" : "fill-background"} ${
                    on ? "stroke-thread" : STROKE[tone]
                  } ${t.running ? "motion-safe:animate-pulse" : ""}`}
                  strokeWidth={on ? 2 : t.agent ? 1.5 : 1}
                  strokeDasharray={t.parked || t.denied ? "4 3" : undefined}
                />
                {ring(TW)}
                <text
                  x={10}
                  y={18}
                  className={`text-[11px] font-medium ${TEXT[tone]}`}
                >
                  <tspan aria-hidden>{t.agent ? "▣ " : "⚙ "}</tspan>
                  {t.agent ?? t.name}{" "}
                  <tspan className="fill-muted-foreground">
                    {toolMarks(t)}
                  </tspan>{" "}
                </text>
                {/* Each line ends in a space: the accessible name is the
                    visible words in their order (label above), and a
                    screen reader's text never runs two lines together. */}
                <text
                  x={10}
                  y={34}
                  className="fill-muted-foreground text-[10px]"
                >
                  {toolLine(t)}{" "}
                </text>
                {cost ? (
                  <text
                    x={TW - 8}
                    y={18}
                    textAnchor="end"
                    className="fill-muted-foreground text-[10px]"
                    data-cost-known={cost.known ? "true" : "false"}
                    data-loop-cost
                  >
                    <title>{cost.title}</title>
                    {cost.text}
                  </text>
                ) : null}
              </g>
            )
          })}
          {/* A subagent's own loop: expand in place (→ on the node), or
              past the depth, its run page. Outside the node's button (no
              nested control). */}
          {shown.map((t, i) => {
            const child = childOf(t)
            if (!child) return null
            const x = colX[2] + TW - 8
            const y = toolY(i) + 34
            return deep ? (
              <a key={`x${t.id}`} href={openRun(child)} data-loop-open={child}>
                <text
                  x={x}
                  y={y}
                  textAnchor="end"
                  className="fill-thread-ink text-[10px]"
                >
                  {OPEN_RUN}
                </text>
              </a>
            ) : (
              <text
                key={`x${t.id}`}
                x={x}
                y={y}
                textAnchor="end"
                className="cursor-pointer fill-thread-ink text-[10px]"
                aria-hidden
                data-loop-expand={child}
                onClick={() => onFocusRun(child)}
              >
                ⊕ {EXPAND}
              </text>
            )
          })}
          {hidden ? (
            <g
              transform={`translate(${colX[2]},${toolY(shown.length)})`}
              {...nodeProps("more", MORE_TOOLS(hidden), () => setMore(true))}
              data-loop-more
            >
              <rect
                width={TW}
                height={H}
                rx={6}
                className="fill-background stroke-border"
                strokeDasharray="3 3"
              />
              {ring(TW)}
              <text x={10} y={27} className="fill-muted-foreground text-[11px]">
                {MORE_TOOLS(hidden)}
              </text>
            </g>
          ) : null}
          {offered.map((n, i) => (
            <g
              key={`o:${n}`}
              transform={`translate(${colX[2]},${toolY(shown.length + (hidden ? 1 : 0) + i)})`}
              {...nodeProps(`o:${n}`, `${n} ${NEVER_CALLED}`, () =>
                setPicked(`o:${n}`)
              )}
              aria-disabled
              data-never-called={n}
              opacity={picked === `o:${n}` ? 1 : 0.6}
            >
              <title>{NEVER_HINT}</title>
              <rect
                width={TW}
                height={H}
                rx={6}
                className={
                  picked === `o:${n}`
                    ? "fill-accent stroke-thread"
                    : "fill-background stroke-border"
                }
                strokeDasharray="4 3"
              />
              {ring(TW)}
              <text x={10} y={27} className="fill-muted-foreground text-[11px]">
                <tspan aria-hidden>⚙ </tspan>
                {n} {NEVER_CALLED}
              </text>
            </g>
          ))}
          {/* The end */}
          <g
            transform={`translate(${colX[1] - 25},${endY})`}
            {...nodeProps(
              END,
              `${graph.end.word} ${endTokens(graph.end)}`.trim(),
              () => (lastKey ? select(lastKey) : undefined)
            )}
            data-loop-end={graph.end.by ?? ""}
            data-word={graph.end.word}
          >
            <title>{graph.end.hint}</title>
            <rect
              width={MW + 50}
              height={H}
              rx={6}
              className={`${pressed === END ? "fill-accent" : "fill-background"} ${STROKE[graph.end.tone]} ${graph.end.tone === "running" ? "motion-safe:animate-pulse" : ""}`}
              strokeWidth={pressed === END ? 2 : 1.5}
            />
            {ring(MW + 50)}
            <text
              x={10}
              y={18}
              className={`text-[11px] ${TEXT[graph.end.tone]}`}
            >
              <tspan aria-hidden>■ </tspan>
              {graph.end.word}{" "}
            </text>
            <text x={10} y={34} className="fill-muted-foreground text-[10px]">
              {endTokens(graph.end)}
            </text>
          </g>
        </svg>
      </div>
      {graph.offered === null && offeredHole ? (
        <div className="flex items-center gap-1.5" data-loop-offered-hole>
          <span className="text-muted-foreground">{NEVER_CALLED}:</span>
          <HoleBadge {...offeredHole} />
        </div>
      ) : graph.offered === null && offeredError ? (
        <p
          className="font-mono text-[11px] text-status-bad"
          data-loop-offered-hole
          data-read-failed
        >
          {offeredError}
        </p>
      ) : null}
      {graph.steps.length ? (
        <div
          className="flex flex-wrap items-center gap-1"
          role="group"
          aria-label={STEPS_LABEL}
          data-loop-steps
        >
          <span className="text-muted-foreground">{STEPS_LABEL}</span>
          {graph.steps.map((s) => (
            <button
              key={s.n}
              type="button"
              aria-pressed={s.n === step}
              className={`min-w-6 rounded-md border px-1.5 py-0.5 ${
                s.n === step
                  ? "border-thread/60 bg-secondary"
                  : "text-muted-foreground hover:text-foreground"
              }`}
              onClick={() => onStep(s.n === step ? undefined : s.n)}
              data-loop-step={s.n}
            >
              {s.n}
            </button>
          ))}
          <button
            type="button"
            className="ml-1 rounded-md border px-1.5 py-0.5 text-muted-foreground hover:text-foreground disabled:opacity-50"
            disabled={reduced}
            title={reduced ? PLAY_OFF_HINT : undefined}
            aria-pressed={playing}
            onClick={() => {
              if (!playing && (step === undefined || step === steps.at(-1)))
                onStep(steps[0])
              setPlaying(!playing)
            }}
            data-loop-play
          >
            {playing ? PAUSE : PLAY}
          </button>
        </div>
      ) : null}
    </div>
  )
}
