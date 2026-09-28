// The Waterfall: spans as bars on one axis, one row per span, nested
// rows indented and foldable, a playhead line, and everything past
// the playhead veiled. It knows nothing about runs — it draws Span[]
// over a numeric domain — so the run page draws positions today and
// T2a's live view draws time on the same component. A row is
// selectable (click, or ↑↓ when the tree has focus; ←→ fold); a bar
// click seeks. Pure CSS: no chart library, nothing off-origin.
import { ChevronRight } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"

import type { Span, SpanKind, SpanTone } from "@/lib/trace"

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
  running: "text-status-run",
  never: "text-ev-error",
}
const badgeTone: Record<SpanTone, string> = {
  step: "border-border text-muted-foreground",
  tool: "border-ev-result/50 text-ev-result",
  result: "border-ev-result/50 text-ev-result",
  error: "border-ev-error/50 text-ev-error",
  bad: "border-status-bad/50 text-status-bad",
  running: "border-status-run/50 text-status-run",
  never: "border-ev-error/50 text-ev-error",
}
const kindIcon: Record<SpanKind, string> = {
  run: "▣",
  step: "↻",
  tool: "⚙",
  subagent: "↳",
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
  /** Rows deeper than this start folded. */
  foldDepth?: number
  className?: string
}

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
  foldDepth = 5,
  className = "",
}: WaterfallProps) {
  const [folded, setFolded] = useState<Set<string>>(() => {
    const s = new Set<string>()
    for (const sp of spans) if (sp.depth >= foldDepth) s.add(sp.parent ?? "")
    return s
  })
  const children = useMemo(() => {
    const m = new Map<string, number>()
    for (const sp of spans)
      if (sp.parent) m.set(sp.parent, (m.get(sp.parent) ?? 0) + 1)
    return m
  }, [spans])
  // A row is hidden when any ancestor is folded.
  const byId = useMemo(() => new Map(spans.map((s) => [s.id, s])), [spans])
  const visible = spans.filter((sp) => {
    let p = sp.parent
    while (p) {
      if (folded.has(p)) return false
      p = byId.get(p)?.parent
    }
    return true
  })
  const [lo, hi] = domain
  const total = hi - lo + 1
  const at = playhead ?? hi + 1
  const ticks = useMemo(() => {
    const n = Math.min(6, Math.max(1, total - 1))
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
  // Keep the selected row in view.
  const selRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    selRef.current?.scrollIntoView({ block: "nearest" })
  }, [selectedId])

  const cell = (selected: boolean) =>
    selected ? "bg-accent" : "group-hover/row:bg-secondary/60"

  return (
    <div
      className={`waterfall grid grid-cols-[minmax(13rem,19rem)_minmax(0,1fr)_3.5rem] overflow-auto rounded-lg border bg-background font-mono text-[11px] outline-none focus-visible:ring-2 focus-visible:ring-ring/50 ${className}`}
      role="tree"
      aria-label="trace"
      tabIndex={0}
      onKeyDown={onKey}
    >
      {/* axis */}
      <div className="sticky top-0 z-10 flex items-center gap-2 border-b bg-secondary/80 px-2 py-1 text-[10px] tracking-wide text-faint uppercase backdrop-blur">
        span
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
              {t}
            </span>
          ))}
        </div>
      </div>
      <div className="sticky top-0 z-10 border-b bg-secondary/80 px-2 py-1 text-right text-[10px] tracking-wide text-faint uppercase backdrop-blur">
        {unit}s
      </div>

      {visible.map((sp) => {
        const kids = children.get(sp.id) ?? 0
        const isFolded = folded.has(sp.id)
        const left = pct(sp.from, domain)
        const end = sp.to ?? hi
        const width = Math.max(0.6, pct(end + 1, domain) - left)
        const selected = selectedId === sp.id
        const revealed = sp.from < at
        return (
          <div
            key={sp.id}
            className="group/row contents"
            role="treeitem"
            aria-selected={selected}
            aria-expanded={kids > 0 ? !isFolded : undefined}
            data-span={sp.key}
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
              <span
                className={`shrink-0 truncate ${toneText[sp.tone]} ${
                  sp.kind === "run" || sp.kind === "step" ? "font-medium" : ""
                }`}
                style={{ maxWidth: "8rem" }}
              >
                {sp.label}
              </span>
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
            >
              {sp.to === null
                ? `${sp.from}–`
                : sp.from === sp.to
                  ? `${sp.from}`
                  : `${sp.from}–${sp.to}`}
            </div>
          </div>
        )
      })}
      {spans.length === 0 ? (
        <div className="col-span-3 py-6 text-center text-faint">
          no spans yet
        </div>
      ) : null}
    </div>
  )
}
