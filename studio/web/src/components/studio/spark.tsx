// Studio's one small chart component (UX-E-25; the audit's §6.7
// "charts follow the dataviz palette and a shared chart component"):
// a sparkline (a series over the window's buckets) and a bar list (one
// metric across groups). Inline SVG and plain elements, no chart
// dependency (recharts is in package.json for shadcn's chart.tsx but
// unused: ≈ 100 KiB gzip for two marks Studio draws in ≈ 1 KiB). The
// colours are the palette's tokens (--chart-1 the series, --status-bad
// a failure series), never a literal; the numbers are text tokens. Each
// mark carries its value as its hover (a <title>) and the figure is
// summed in an accessible label: identity is never colour alone.
import type { ReactNode } from "react"

import { barScale } from "@/lib/stats"

/** The tones a mark is drawn in: the palette's chart and status tokens. */
const TONE = {
  series: "stroke-chart-1 fill-chart-1",
  bad: "stroke-status-bad fill-status-bad",
} as const

/** Sparkline is a series as a 2px line over a faint area, oldest left;
 * each bucket's value is its hover. Empty or all-zero: a flat baseline
 * (the series holds no runs), still labelled. */
export function Sparkline({
  values,
  label,
  tone = "series",
  width = 96,
  height = 20,
  bucketLabel,
}: {
  values: readonly number[]
  label: string
  tone?: keyof typeof TONE
  width?: number
  height?: number
  /** A bucket's words in its hover ("09:00 · 3"). */
  bucketLabel?: (i: number, v: number) => string
}) {
  const n = values.length
  const max = Math.max(0, ...values)
  const x = (i: number) =>
    n <= 1 ? width / 2 : (i * (width - 2)) / (n - 1) + 1
  const y = (v: number) =>
    max > 0 ? height - 2 - (v / max) * (height - 4) : height - 2
  const pts = values.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`)
  const sum = values.reduce((a, b) => a + b, 0)
  return (
    <svg
      role="img"
      aria-label={`${label}: ${sum.toLocaleString()} over ${n} buckets, at most ${max.toLocaleString()} in one`}
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      className={`shrink-0 overflow-visible ${TONE[tone]}`}
      data-sparkline={label}
    >
      {n > 1 ? (
        <>
          <polygon
            points={`${x(0)},${height - 1} ${pts.join(" ")} ${x(n - 1)},${height - 1}`}
            className="stroke-none opacity-15"
          />
          <polyline
            points={pts.join(" ")}
            fill="none"
            strokeWidth={1.5}
            strokeLinejoin="round"
            strokeLinecap="round"
          />
        </>
      ) : null}
      {values.map((v, i) => (
        // A hit target per bucket wider than the line: its value on hover.
        <rect
          key={i}
          x={x(i) - width / Math.max(n, 1) / 2}
          y={0}
          width={width / Math.max(n, 1)}
          height={height}
          className="fill-transparent stroke-none"
        >
          <title>{bucketLabel ? bucketLabel(i, v) : v.toLocaleString()}</title>
        </rect>
      ))}
    </svg>
  )
}

/** One bar of a BarList. */
export interface Bar {
  key: string
  label: string
  value: number
  /** The value's words (the bar's own label and its hover). */
  text: string
  /** Not known (an unpriced cost): no bar, the words say why. */
  unknown?: boolean
  bad?: boolean
  /** A link around the label (the group's runs). */
  href?: ReactNode
}

/** BarList is one metric across groups: a row per group — its name,
 * a bar scaled to the largest, its value as text (direct labels: no
 * legend, no axis). An unknown value draws no bar and its words. */
export function BarList({ bars, label }: { bars: Bar[]; label: string }) {
  const scale = barScale(bars.map((b) => (b.unknown ? 0 : b.value)))
  return (
    <ul className="space-y-1" aria-label={label} data-bar-list={label}>
      {bars.map((b, i) => (
        <li
          key={b.key}
          className="grid grid-cols-[minmax(0,8rem)_1fr_auto] items-center gap-2 text-[11px]"
          title={`${b.label}: ${b.text}`}
          data-bar={b.key}
        >
          <span className="truncate font-mono text-muted-foreground">
            {b.href ?? b.label}
          </span>
          <span className="h-2 rounded-sm bg-secondary" aria-hidden>
            {b.unknown ? null : (
              <span
                className={`block h-2 rounded-sm ${b.bad ? "bg-status-bad" : "bg-chart-1"}`}
                style={{
                  width: `${Math.max(scale[i] * 100, b.value > 0 ? 2 : 0)}%`,
                }}
              />
            )}
          </span>
          <span
            className={`font-mono tabular-nums ${b.unknown ? "text-faint" : "text-foreground"}`}
          >
            {b.text}
          </span>
        </li>
      ))}
    </ul>
  )
}
