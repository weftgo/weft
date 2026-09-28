// Small formatting helpers, pure and tested: relative times, run
// durations, token counts. Mono is the machine's voice — the callers
// render these in Geist Mono (plan §5.4).
import type { Usage } from "./api"

/** "just now" / "4m ago" / "2h ago" / "3d ago" / "Sep 12". */
export function relativeTime(iso: string, now: number = Date.now()): string {
  const then = Date.parse(iso)
  if (Number.isNaN(then)) return "—"
  const s = Math.max(0, Math.round((now - then) / 1000))
  if (s < 45) return "just now"
  const m = Math.round(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.round(m / 60)
  if (h < 24) return `${h}h ago`
  const d = Math.round(h / 24)
  if (d < 14) return `${d}d ago`
  return new Date(then).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  })
}

/** "2026-09-28 09:00:12" — the absolute stamp shown on hover. */
export function absoluteTime(iso: string): string {
  const t = new Date(Date.parse(iso))
  if (Number.isNaN(t.getTime())) return "—"
  const p = (n: number) => String(n).padStart(2, "0")
  return `${t.getFullYear()}-${p(t.getMonth() + 1)}-${p(t.getDate())} ${p(t.getHours())}:${p(t.getMinutes())}:${p(t.getSeconds())}`
}

/** Run-level duration (finished - started), "1.2s" / "40.5s" / "4m03s". */
export function duration(started: string, finished: string | null): string {
  const a = Date.parse(started)
  const b = finished ? Date.parse(finished) : NaN
  if (Number.isNaN(a) || Number.isNaN(b) || b < a) return "—"
  const s = (b - a) / 1000
  if (s < 60) return `${s.toFixed(1)}s`
  const m = Math.floor(s / 60)
  return `${m}m${String(Math.round(s % 60)).padStart(2, "0")}s`
}

/** "12.4k / 3.1k" — token counts stay compact; exact values on hover. */
export function tokens(n: number): string {
  if (Math.abs(n) >= 1000) return `${(n / 1000).toFixed(1)}k`
  return String(n)
}

export function usageSummary(u: Usage): string {
  return `${u.input_tokens.toLocaleString()} in / ${u.output_tokens.toLocaleString()} out`
}
