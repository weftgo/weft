// The Waterfall: spans as bars on one axis, one row per span, nested
// rows indented and foldable, a playhead line, and everything past
// the playhead dimmed. It knows nothing about runs — it draws Span[]
// over a numeric domain — so the run page draws positions today and
// T2a's live view draws time on the same component. Click a bar to
// seek there; click a label to select (the run page scrolls to that
// step or call). Pure CSS: no chart library, nothing off-origin.
import { ChevronRight } from "lucide-react"
import { useMemo, useState } from "react"

import type { Span, SpanTone } from "@/lib/trace"

const toneBar: Record<SpanTone, string> = {
  step: "bg-ev-step/70",
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
  foldDepth = 4,
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
    const n = Math.min(8, Math.max(1, total))
    const out: number[] = []
    for (let i = 0; i <= n; i++)
      out.push(lo + Math.round((i * (total - 1)) / n))
    return [...new Set(out)]
  }, [lo, total])

  const seekAt = (e: React.MouseEvent<HTMLElement>) => {
    if (!onSeek) return
    const rect = e.currentTarget.getBoundingClientRect()
    const x = (e.clientX - rect.left) / rect.width
    onSeek(lo + Math.round(x * total))
  }

  return (
    <div
      className="waterfall grid grid-cols-[minmax(12rem,16rem)_minmax(0,1fr)] rounded-lg border bg-background font-mono text-[11px]"
      role="table"
      aria-label="trace"
    >
      {/* axis */}
      <div className="flex items-center gap-2 border-b px-3 py-1 text-[10px] tracking-wide text-faint uppercase">
        span
        <span className="tracking-normal normal-case">
          · {unit} position, not time
        </span>
      </div>
      <div
        className="relative h-6 cursor-pointer border-b"
        onClick={seekAt}
        title={`${unit} position — click to seek`}
      >
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

      {visible.map((sp) => {
        const kids = children.get(sp.id) ?? 0
        const isFolded = folded.has(sp.id)
        const left = pct(sp.from, domain)
        const end = sp.to ?? hi
        const width = Math.max(0.6, pct(end + 1, domain) - left)
        const selected = selectedId === sp.id
        const revealed = sp.from < at
        return (
          <div key={sp.id} className="contents" role="row">
            <div
              className={`flex min-w-0 items-center gap-1 border-b border-border/60 py-0.5 pr-2 ${
                selected ? "bg-accent" : "hover:bg-secondary/60"
              } ${revealed ? "" : "opacity-40"}`}
              style={{ paddingLeft: `${0.5 + sp.depth * 0.9}rem` }}
              role="rowheader"
            >
              {kids > 0 ? (
                <button
                  type="button"
                  className="flex size-4 shrink-0 items-center justify-center text-faint hover:text-foreground"
                  aria-label={isFolded ? "expand" : "collapse"}
                  aria-expanded={!isFolded}
                  onClick={() =>
                    setFolded((f) => {
                      const n = new Set(f)
                      if (n.has(sp.id)) n.delete(sp.id)
                      else n.add(sp.id)
                      return n
                    })
                  }
                >
                  <ChevronRight
                    className={`size-3 transition-transform ${isFolded ? "" : "rotate-90"}`}
                  />
                </button>
              ) : (
                <span className="size-4 shrink-0" />
              )}
              <button
                type="button"
                className={`shrink-0 truncate text-left ${toneText[sp.tone]} ${
                  onSelect ? "hover:underline" : ""
                }`}
                style={{ maxWidth: "9rem" }}
                onClick={() => onSelect?.(sp)}
                title={`${sp.label}${sp.sub ? ` — ${sp.sub}` : ""}`}
              >
                {sp.label}
              </button>
              {sp.sub ? (
                <span className="min-w-0 truncate text-faint">{sp.sub}</span>
              ) : null}
            </div>
            <div
              className={`relative h-6 cursor-pointer border-b border-border/60 ${
                selected ? "bg-accent" : ""
              }`}
              onClick={seekAt}
              role="cell"
            >
              <div
                className={`absolute top-1.5 h-3 rounded-[2px] ${toneBar[sp.tone]} ${
                  sp.to === null ? "waterfall-open" : ""
                }`}
                style={{ left: `${left}%`, width: `${width}%` }}
                title={
                  sp.to === null
                    ? `${sp.label}: from ${unit} ${sp.from}, still open`
                    : sp.from === sp.to
                      ? `${sp.label}: ${unit} ${sp.from}`
                      : `${sp.label}: ${unit}s ${sp.from}–${sp.to}`
                }
              />
              {/* the not-yet veil */}
              {playhead !== null && at <= hi ? (
                <div
                  className="pointer-events-none absolute inset-y-0 right-0 bg-background/70"
                  style={{ left: `${pct(at, domain)}%` }}
                />
              ) : null}
            </div>
          </div>
        )
      })}

      {/* the playhead line spans every row: drawn last, over the bars */}
      {playhead !== null && at <= hi ? (
        <div className="contents" aria-hidden>
          <div />
          <div className="relative h-0">
            <div
              className="pointer-events-none absolute bottom-0 w-0.5 bg-thread"
              style={{
                left: `${pct(at, domain)}%`,
                height: `${(visible.length + 1) * 1.5}rem`,
                marginBottom: 0,
              }}
            />
          </div>
        </div>
      ) : null}
      {spans.length === 0 ? (
        <div className="col-span-2 py-6 text-center text-faint">
          no spans yet
        </div>
      ) : null}
    </div>
  )
}
