// DOM construction for the panel: tiny helpers over the standard
// APIs — no framework, no virtual DOM (the Dv0 decision, §11 Q1).
// Shadow DOM keeps the host page's CSS out (§5.4); every element
// carries a weft- class so the panel's own stylesheet owns it.

/** el builds one element: tag, class, text or children, attributes. */
export function el(
  tag: string,
  cls?: string,
  textOrChildren?: string | Node[],
  attrs?: Record<string, string>
): HTMLElement {
  const n = document.createElement(tag)
  if (cls) n.className = cls
  if (typeof textOrChildren === "string") n.textContent = textOrChildren
  else if (Array.isArray(textOrChildren))
    for (const c of textOrChildren) n.appendChild(c)
  if (attrs)
    for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v)
  return n
}

/** clear removes a node's children in one go. */
export function clear(n: HTMLElement): HTMLElement {
  while (n.firstChild) n.removeChild(n.firstChild)
  return n
}

/** fmtArgs renders a tool call's arguments: parsed JSON when the call
 * executed with structured args, the streamed fragment while it is
 * still being written, "…" when nothing is known yet. */
export function fmtArgs(args: unknown, streamed: string): string {
  if (args !== undefined && args !== null) {
    try {
      return JSON.stringify(args)
    } catch {
      return String(args)
    }
  }
  return streamed || "…"
}

/** stringify is JSON.stringify with its honest type: undefined for a
 * value JSON cannot carry (undefined, a function, a symbol). */
export function stringify(v: unknown, indent?: number): string | undefined {
  return JSON.stringify(v, null, indent)
}

/** fmtJSON pretty-prints for the raw toggle; a cycle or a BigInt
 * falls back to String so the toggle never throws into the page. */
export function fmtJSON(v: unknown): string {
  try {
    return stringify(v, 2) ?? String(v)
  } catch {
    return String(v)
  }
}

// ── The timing waterfall (§2 Timing, Dv3) ─────────────────────────
// Spans are available now (unlike v1), so per-step and per-tool
// durations draw as a mini waterfall over the run's own window: one
// bar per span, placed by (start − window start) / window width.

export interface WaterfallBar {
  name: string
  /** Left offset and width, both 0..1 over the run's window. */
  left: number
  width: number
  /** Wall time in ms. */
  ms: number
}

/** waterfall lays spans over the run's [min start, max end] window.
 * Spanless runs and unparseable times yield nothing. */
export function waterfall(
  spans: { name: string; start: string; end: string }[]
): WaterfallBar[] {
  const parsed = spans
    .map((s) => ({ name: s.name, a: Date.parse(s.start), b: Date.parse(s.end) }))
    .filter((s) => Number.isFinite(s.a) && Number.isFinite(s.b) && s.b >= s.a)
  if (!parsed.length) return []
  const from = Math.min(...parsed.map((s) => s.a))
  const to = Math.max(...parsed.map((s) => s.b))
  const span = Math.max(1, to - from)
  return parsed.map((s) => ({
    name: s.name,
    left: (s.a - from) / span,
    width: Math.max(0.005, (s.b - s.a) / span),
    ms: s.b - s.a,
  }))
}
