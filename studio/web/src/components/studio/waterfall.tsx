// The Waterfall: spans as bars on one axis, one row per span, nested
// rows indented and foldable, a playhead line, and everything past
// the playhead veiled. It knows nothing about runs — it draws Span[]
// over a numeric domain — so the run page draws positions today and
// T2a's live view draws time on the same component. A row is
// selectable (click, or ↑↓ when the tree has focus; ←→ fold); a bar
// click seeks. Pure CSS: no chart library, nothing off-origin.
// Past VIRTUAL_ROWS rows the tree is virtualised (plan H2): rows are a
// fixed ROW_PX tall, so only the rows in view plus an overscan are
// mounted, between two spacers that keep the scroll height whole.
import { useVirtualizer } from "@tanstack/react-virtual"
import { shared, spacers } from "@/lib/virtual"
import { ChevronRight } from "lucide-react"
import { Fragment, useEffect, useMemo, useRef, useState } from "react"

import type { Span, SpanKind, SpanTone } from "@/lib/trace"
import { took } from "@/lib/waterfall"

const toneBar: Record<SpanTone, string> = {
  step: "bg-ev-step/60",
  tool: "bg-ev-tool",
  result: "bg-ev-result",
  error: "bg-ev-error",
  bad: "bg-status-bad",
  running: "bg-status-run",
  never: "bg-ev-error/60 waterfall-hatched",
}
const toneText: Record<SpanTone, string> = {
  step: "text-foreground",
  tool: "text-ev-tool",
  result: "text-ev-result",
  error: "text-ev-error",
  bad: "text-status-bad",
  running: "text-status-run-ink",
  never: "text-ev-error",
}
const badgeTone: Record<SpanTone, string> = {
  step: "border-border text-muted-foreground",
  tool: "border-ev-result/50 text-ev-result",
  result: "border-ev-result/50 text-ev-result",
  error: "border-ev-error/50 text-ev-error",
  bad: "border-status-bad/50 text-status-bad",
  running: "border-status-run/50 text-status-run-ink",
  never: "border-ev-error/50 text-ev-error",
}
const kindIcon: Record<SpanKind, string> = {
  run: "▣",
  step: "↻",
  tool: "⚙",
  subagent: "↳",
  model: "◆",
  attempt: "◇",
  span: "○",
}

export interface WaterfallProps {
  spans: Span[]
  /** The axis: [min, max] inclusive. */
  domain: [number, number]
  /** Reveal up to here (exclusive); null = everything. */
  playhead: number | null
  /** What one unit is called, for the axis and the tooltips. */
  unit?: string
  onSeek?: (t: number) => void
  onSelect?: (span: Span) => void
  selectedId?: string
  /** A row's own URL (the trace page's selection links, plan H7): the
   * label becomes a link to it — a plain click still selects in place,
   * a modified or middle click opens the link as the browser would. */
  hrefOf?: (span: Span) => string | undefined
  /** Rows deeper than this start folded. */
  foldDepth?: number
  /** The tree's accessible name and its first column's heading. */
  label?: string
  head?: string
  /** What a row draws after its label (the run tree's child row: its
   * "open run ↗" link). */
  trail?: (span: Span) => React.ReactNode
  className?: string
}

/** More visible rows than this and the tree is virtualised. */
export const VIRTUAL_ROWS = 200
/** A row's height: the track's h-6 plus its 1px border. */
const ROW_PX = 25
/** The sticky axis row above the first span row. */
const AXIS_PX = 25

function pct(v: number, [lo, hi]: [number, number]): number {
  const w = Math.max(1, hi - lo + 1)
  return ((v - lo) / w) * 100
}

export function Waterfall({
  spans,
  domain,
  playhead,
  unit = "event",
  onSeek,
  onSelect,
  selectedId,
  hrefOf,
  foldDepth = 5,
  label = "trace",
  head = "span",
  trail,
  className = "",
}: WaterfallProps) {
  const [folded, setFolded] = useState<Set<string>>(() => new Set())
  // Deep subtrees start folded. The events usually arrive after mount,
  // so the fold is applied once, the first time there is a tree.
  const autoFolded = useRef(false)
  useEffect(() => {
    if (autoFolded.current || spans.length <= 1) return
    autoFolded.current = true
    const deep = new Set<string>()
    for (const sp of spans) if (sp.depth >= foldDepth) deep.add(sp.parent ?? "")
    if (deep.size > 0) setFolded((prev) => new Set([...prev, ...deep]))
  }, [spans, foldDepth])
  const children = useMemo(() => {
    const m = new Map<string, number>()
    for (const sp of spans)
      if (sp.parent) m.set(sp.parent, (m.get(sp.parent) ?? 0) + 1)
    return m
  }, [spans])
  // A row is hidden when any ancestor is folded.
  const byId = useMemo(() => new Map(spans.map((s) => [s.id, s])), [spans])
  // The walk is bounded by the span count: a parent chain that loops
  // (rows built from spans that name each other) must not hang the tab.
  const visible = spans.filter((sp) => {
    let p = sp.parent
    for (let hops = 0; p && hops <= spans.length; hops++) {
      if (folded.has(p)) return false
      p = byId.get(p)?.parent
    }
    return true
  })
  const [lo, hi] = domain
  const total = hi - lo + 1
  // The time axis reads humanised (UX-A-23: 7.7s, not 7737), each row
  // its duration with its offset on hover; positions stay ranges.
  const time = unit === "ms"
  const at = playhead ?? hi + 1
  const ticks = useMemo(() => {
    const n = Math.min(4, Math.max(1, total - 1))
    const out: number[] = []
    for (let i = 0; i <= n; i++)
      out.push(lo + Math.round((i * (total - 1)) / n))
    return [...new Set(out)]
  }, [lo, total])

  const seekAt = (e: React.MouseEvent<HTMLElement>) => {
    if (!onSeek) return
    e.stopPropagation()
    const rect = e.currentTarget.getBoundingClientRect()
    const x = (e.clientX - rect.left) / rect.width
    onSeek(lo + Math.round(x * total))
  }
  const toggle = (id: string) =>
    setFolded((f) => {
      const n = new Set(f)
      if (n.has(id)) n.delete(id)
      else n.add(id)
      return n
    })

  // Keyboard on the tree: ↑↓ select, ← fold or go to parent, → unfold.
  const onKey = (e: React.KeyboardEvent) => {
    if (!onSelect) return
    const i = visible.findIndex((s) => s.id === selectedId)
    if (e.key === "ArrowDown") {
      e.preventDefault()
      const next = visible.at(Math.min(i + 1, visible.length - 1))
      if (next) onSelect(next)
    } else if (e.key === "ArrowUp") {
      e.preventDefault()
      const prev = visible.at(Math.max(i - 1, 0))
      if (prev) onSelect(prev)
    } else if (e.key === "ArrowLeft" && i >= 0) {
      e.preventDefault()
      const sp = visible[i]
      if ((children.get(sp.id) ?? 0) > 0 && !folded.has(sp.id)) toggle(sp.id)
      else if (sp.parent) onSelect(byId.get(sp.parent) ?? sp)
    } else if (e.key === "ArrowRight" && i >= 0) {
      e.preventDefault()
      const sp = visible[i]
      if (folded.has(sp.id)) toggle(sp.id)
    }
  }
  // Each row's place among its siblings (aria-posinset/-setsize):
  // a virtualised tree mounts a window of it, and the rows say where
  // they sit in the whole.
  const siblings = useMemo(() => {
    const count = new Map<string, number>()
    const place = new Map<string, number>()
    for (const sp of spans) {
      const p = sp.parent ?? ""
      const n = (count.get(p) ?? 0) + 1
      count.set(p, n)
      place.set(sp.id, n)
    }
    return { count, at: place }
  }, [spans])

  const virtual = visible.length > VIRTUAL_ROWS
  const scroller = useRef<HTMLDivElement>(null)
  const selIndex = virtual ? visible.findIndex((s) => s.id === selectedId) : -1
  const virt = useVirtualizer({
    count: virtual ? visible.length : 0,
    getScrollElement: () => scroller.current,
    estimateSize: () => ROW_PX,
    paddingStart: AXIS_PX,
    scrollPaddingStart: AXIS_PX,
    overscan: 16,
    getItemKey: (i) => visible[i]?.id ?? i,
    ...shared(selIndex),
  })
  const items = virt.getVirtualItems()
  const shown = virtual ? items.map((it) => visible[it.index]) : visible
  // A spacer before every row that does not follow on from the one
  // before it (the pinned selected row far from the window), one after
  // the last: each row lands at its own start.
  const pads = virtual
    ? spacers(items, AXIS_PX, virt.getTotalSize())
    : { before: [], after: 0 }

  // Keep the selected row in view: a mounted row scrolls itself; in a
  // virtualised tree the virtualiser scrolls to its index.
  const selRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (virtual) {
      if (selIndex >= 0) virt.scrollToIndex(selIndex, { align: "auto" })
      return
    }
    selRef.current?.scrollIntoView({ block: "nearest" })
    // On a selection change only: a live append must not pull the view.
  }, [selectedId])

  const cell = (selected: boolean) =>
    selected ? "bg-accent" : "group-hover/row:bg-secondary/60"

  return (
    <div
      ref={scroller}
      className={`waterfall grid grid-cols-[minmax(12rem,17rem)_minmax(0,1fr)_3.5rem] overflow-auto rounded-lg border bg-background font-mono text-[11px] outline-none focus-visible:ring-2 focus-visible:ring-ring/50 ${className}`}
      role="tree"
      aria-label={label}
      data-virtual={virtual ? "" : undefined}
      tabIndex={0}
      onKeyDown={onKey}
    >
      {/* axis */}
      <div className="sticky top-0 z-10 flex items-center gap-2 border-b bg-secondary/80 px-2 py-1 text-[10px] tracking-wide text-faint uppercase backdrop-blur">
        {head}
        <span className="ml-auto flex gap-1 tracking-normal normal-case">
          <button
            type="button"
            className="hover:text-foreground"
            onClick={() => setFolded(new Set())}
          >
            expand
          </button>
          ·
          <button
            type="button"
            className="hover:text-foreground"
            onClick={() =>
              setFolded(
                new Set(
                  spans
                    .filter((s) => s.kind !== "run" && children.has(s.id))
                    .map((s) => s.id)
                )
              )
            }
          >
            collapse
          </button>
        </span>
      </div>
      <div
        className="sticky top-0 z-10 h-6 cursor-pointer border-b bg-secondary/80 backdrop-blur"
        onClick={seekAt}
        title={`${unit} position — click to seek`}
      >
        <div className="relative h-full">
          {ticks.map((t, i) => (
            <span
              key={t}
              className={`absolute top-1 text-[10px] text-faint tabular-nums ${
                i === 0
                  ? "translate-x-1"
                  : i === ticks.length - 1
                    ? "-translate-x-full pr-1"
                    : "-translate-x-1/2"
              }`}
              style={{
                left: `${i === ticks.length - 1 ? 100 : pct(t, domain)}%`,
              }}
            >
              {time && t ? took(t) : t}
            </span>
          ))}
        </div>
      </div>
      <div className="sticky top-0 z-10 border-b bg-secondary/80 px-2 py-1 text-right text-[10px] tracking-wide text-faint uppercase backdrop-blur">
        {unit === "event" ? "events" : unit}
      </div>

      {shown.map((sp, n) => {
        const kids = children.get(sp.id) ?? 0
        const isFolded = folded.has(sp.id)
        const left = pct(sp.from, domain)
        const end = sp.to ?? hi
        const width = Math.max(0.6, pct(end + 1, domain) - left)
        const selected = selectedId === sp.id
        const revealed = sp.from < at
        const gap = pads.before[n] ?? 0
        return (
          <Fragment key={sp.id}>
            {gap > 0 ? (
              <div
                className="col-span-3"
                style={{ height: gap }}
                aria-hidden
                data-spacer
              />
            ) : null}
            <div
              className="group/row contents"
              role="treeitem"
              aria-selected={selected}
              aria-expanded={kids > 0 ? !isFolded : undefined}
              aria-level={sp.depth + 1}
              aria-setsize={siblings.count.get(sp.parent ?? "")}
              aria-posinset={siblings.at.get(sp.id)}
              data-span={sp.key}
              data-critical={sp.critical ? "" : undefined}
              data-lane={sp.lane}
              data-lanes={sp.lanes}
              data-child-run={sp.childRun}
            >
              <div
                ref={selected ? selRef : undefined}
                className={`flex min-w-0 cursor-pointer items-center gap-1 border-b border-border/60 py-0.5 pr-2 ${cell(selected)} ${
                  selected ? "shadow-[inset_2px_0_0_var(--thread)]" : ""
                } ${revealed ? "" : "opacity-40"}`}
                style={{ paddingLeft: `${0.4 + sp.depth * 0.8}rem` }}
                onClick={() => onSelect?.(sp)}
                title={`${sp.label}${sp.sub ? ` — ${sp.sub}` : ""}`}
              >
                {kids > 0 ? (
                  <button
                    type="button"
                    className="flex size-4 shrink-0 items-center justify-center text-faint hover:text-foreground"
                    aria-label={isFolded ? "expand" : "collapse"}
                    onClick={(e) => {
                      e.stopPropagation()
                      toggle(sp.id)
                    }}
                  >
                    <ChevronRight
                      className={`size-3 transition-transform ${isFolded ? "" : "rotate-90"}`}
                    />
                  </button>
                ) : (
                  <span className="size-4 shrink-0" />
                )}
                <span
                  className={`w-3 shrink-0 text-center text-[10px] ${toneText[sp.tone]}`}
                  aria-hidden
                >
                  {kindIcon[sp.kind]}
                </span>
                {(() => {
                  const cls = `shrink-0 truncate ${toneText[sp.tone]} ${
                    sp.kind === "run" || sp.kind === "step" ? "font-medium" : ""
                  }`
                  const href = hrefOf?.(sp)
                  if (!href)
                    return (
                      <span className={cls} style={{ maxWidth: "8rem" }}>
                        {sp.label}
                      </span>
                    )
                  // Out of the tab order: the tree is one stop, its rows
                  // walked with the arrows (the selection is the URL).
                  return (
                    <a
                      href={href}
                      tabIndex={-1}
                      data-span-link
                      className={`${cls} hover:underline`}
                      style={{ maxWidth: "8rem" }}
                      onClick={(e) => {
                        if (
                          e.metaKey ||
                          e.ctrlKey ||
                          e.shiftKey ||
                          e.altKey ||
                          e.button !== 0
                        ) {
                          e.stopPropagation()
                          return
                        }
                        e.preventDefault()
                      }}
                    >
                      {sp.label}
                    </a>
                  )
                })()}
                {sp.badge ? (
                  <span
                    className={`max-w-28 shrink-0 truncate rounded-full border px-1.5 text-[9.5px] leading-4 ${badgeTone[sp.tone]}`}
                    title={sp.badge}
                  >
                    {sp.badge}
                  </span>
                ) : null}
                {sp.sub ? (
                  <span className="min-w-0 truncate text-faint">{sp.sub}</span>
                ) : null}
                {trail?.(sp)}
              </div>
              <div
                className={`waterfall-track relative h-6 cursor-pointer border-b border-border/60 ${cell(selected)}`}
                onClick={seekAt}
              >
                <div
                  className={`absolute rounded-[2px] ${toneBar[sp.tone]} ${
                    sp.to === null ? "waterfall-open" : ""
                  } ${
                    sp.kind === "run" || sp.kind === "subagent"
                      ? "top-2.5 h-1"
                      : "top-1.5 h-3"
                  } ${
                    // The critical path (U8.2): a thin accent under the bar.
                    sp.critical ? "shadow-[0_2px_0_0_var(--thread)]" : ""
                  }`}
                  style={{ left: `${left}%`, width: `${width}%` }}
                />
                {playhead !== null && at <= hi ? (
                  <>
                    <div
                      className="pointer-events-none absolute inset-y-0 right-0 bg-background/70"
                      style={{ left: `${pct(at, domain)}%` }}
                    />
                    <div
                      className="pointer-events-none absolute inset-y-0 w-0.5 bg-thread"
                      style={{ left: `${pct(at, domain)}%` }}
                    />
                  </>
                ) : null}
              </div>
              <div
                className={`cursor-pointer border-b border-border/60 px-2 py-1 text-right text-[10px] text-faint tabular-nums ${cell(selected)}`}
                onClick={() => onSelect?.(sp)}
                title={time ? `+${took(sp.from)}` : undefined}
              >
                {time
                  ? sp.to === null
                    ? "…"
                    : took(sp.to - sp.from)
                  : sp.to === null
                    ? `${sp.from}–`
                    : sp.from === sp.to
                      ? `${sp.from}`
                      : `${sp.from}–${sp.to}`}
              </div>
            </div>
          </Fragment>
        )
      })}
      {pads.after > 0 ? (
        <div
          className="col-span-3"
          style={{ height: pads.after }}
          aria-hidden
          data-spacer
        />
      ) : null}
      {spans.length === 0 ? (
        <div className="col-span-3 py-6 text-center text-faint">
          no spans yet
        </div>
      ) : null}
    </div>
  )
}
