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

/** "UTC", "UTC+2", "UTC-5:30" — the offset a local stamp was rendered
 * in. offsetMinutes is Date#getTimezoneOffset's value (UTC − local). */
export function zoneLabel(offsetMinutes: number): string {
  if (!offsetMinutes) return "UTC"
  const abs = Math.abs(offsetMinutes)
  const h = Math.floor(abs / 60)
  const m = abs % 60
  return `UTC${offsetMinutes < 0 ? "+" : "-"}${h}${m ? `:${String(m).padStart(2, "0")}` : ""}`
}

/** "2026-09-28 09:00:12 UTC+2" — the absolute stamp shown on hover:
 * the viewer's wall clock, with its offset named so the stamp can be
 * lined up with a server log. */
export function absoluteTime(iso: string): string {
  const t = new Date(Date.parse(iso))
  if (Number.isNaN(t.getTime())) return "—"
  const p = (n: number) => String(n).padStart(2, "0")
  return `${t.getFullYear()}-${p(t.getMonth() + 1)}-${p(t.getDate())} ${p(t.getHours())}:${p(t.getMinutes())}:${p(t.getSeconds())} ${zoneLabel(t.getTimezoneOffset())}`
}

/** Run-level duration (finished - started), "12ms" / "1.2s" / "4m03s" / "2h05m". */
export function duration(started: string, finished: string | null): string {
  const a = Date.parse(started)
  const b = finished ? Date.parse(finished) : NaN
  if (Number.isNaN(a) || Number.isNaN(b) || b < a) return "—"
  return spanMs(b - a)
}

/** A span of milliseconds in the tightest unit that still reads. */
export function spanMs(ms: number): string {
  if (!Number.isFinite(ms)) return "—"
  if (ms < 1000) return `${Math.round(ms)}ms`
  // Under a minute keeps a decimal — unless rounding would print
  // "60.0s"; from there on whole seconds, so a remainder never reads 60.
  if (ms < 59_950) return `${(ms / 1000).toFixed(1)}s`
  const s = Math.round(ms / 1000)
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m${String(s % 60).padStart(2, "0")}s`
  const h = Math.floor(m / 60)
  return `${h}h${String(m % 60).padStart(2, "0")}m`
}

/** How long a still-running run has been going, against now. */
export function elapsed(started: string, now: number = Date.now()): string {
  const a = Date.parse(started)
  if (Number.isNaN(a) || now < a) return "—"
  return spanMs(now - a)
}

/** "12.4k / 3.1k" — token counts stay compact; exact values on hover. */
export function tokens(n: number): string {
  if (!Number.isFinite(n)) return "—"
  const abs = Math.abs(n)
  if (abs >= 999_950) return `${(n / 1_000_000).toFixed(1)}M`
  if (abs >= 1000) return `${(n / 1000).toFixed(1)}k`
  return String(n)
}

export function usageSummary(u: Usage): string {
  const n = (v: number | undefined) =>
    (Number.isFinite(v) ? (v as number) : 0).toLocaleString()
  return `${n(u.input_tokens)} in / ${n(u.output_tokens)} out`
}
