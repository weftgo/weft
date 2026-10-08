// DOM construction for the panel: tiny helpers over the standard
// APIs — no framework, no virtual DOM (the Dv0 decision, §11 Q1); a
// keyed patch (D3) instead of a rebuild.
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

/** h is el with the attributes first: h("div", { role: "list" }, kids). */
export const h = (tag: string, attrs?: Record<string, string>, kids?: string | Node[]): HTMLElement =>
  el(tag, undefined, kids, attrs)

// ── The keyed renderer (D3) ───────────────────────────────────────
// Every draw builds the dock afresh, then patch() reconciles it into
// the nodes on screen: a child matches by data-key (a turn row by its
// run id, a step card by its ordinal), else by tag and class; matched
// nodes keep their identity — focus, caret, scroll and the <details>
// the user opened stay with them — and only what differs is written.
// Handlers ride on(), so a kept node answers with the newest closure.

type Handler = (e: Event, n: HTMLElement) => void
type Live = Node & { _on?: Record<string, Handler | undefined> }

/** on attaches a handler the patch carries to the node kept on
 * screen; the handler gets that live node, never a discarded build. */
export function on(n: HTMLElement, type: string, fn: Handler, capture = false): HTMLElement {
  const m = ((n as Live)._on ??= {})
  const k = capture ? `${type}!` : type
  if (!(k in m)) n.addEventListener(type, (e) => (n as Live)._on?.[k]?.(e, n), capture)
  m[k] = fn
  return n
}

const keyOf = (n: Node) => (n.nodeType === 1 ? (n as Element).getAttribute("data-key") : null)
const like = (a: Node, b: Node) => a.nodeName === b.nodeName && keyOf(a) === keyOf(b)
const cls = (n: Node) => (n.nodeType === 1 ? (n as Element).className : "")

/** patch makes parent's children next, reusing what matches. */
export function patch(parent: Node, next: Node[]): void {
  const old = Array.from(parent.childNodes)
  // A key repeated under one parent pairs in order (never "last
  // wins", which would rebuild both on every draw).
  const keyed = new Map<string, Node[]>()
  const free: Node[] = []
  for (const o of old) {
    const k = keyOf(o)
    if (k === null) free.push(o)
    else keyed.set(k, [...(keyed.get(k) ?? []), o])
  }
  const plan = next.map((n): [Node, Node | undefined] => {
    const k = keyOf(n)
    if (k !== null) {
      const o = keyed.get(k)?.shift()
      return [n, o && like(o, n) ? o : undefined]
    }
    let i = free.findIndex((o) => like(o, n) && cls(o) === cls(n))
    if (i < 0) i = free.findIndex((o) => like(o, n))
    return [n, i < 0 ? undefined : free.splice(i, 1)[0]]
  })
  const kept = new Set(plan.map((p) => p[1]))
  for (const o of old) if (!kept.has(o)) parent.removeChild(o)
  // A move blurs: the node holding focus stays put, and the nodes
  // ahead of it (placed later anyway) step past it instead.
  const active = (parent.getRootNode() as Partial<DocumentOrShadowRoot>).activeElement ?? null
  let ref = parent.firstChild
  for (const [n, o] of plan) {
    const t = o ?? n
    if (ref && t !== ref && o?.contains(active)) {
      const after = t.nextSibling
      while (ref && ref !== t) {
        const nx: ChildNode | null = ref.nextSibling
        parent.insertBefore(ref, after)
        ref = nx
      }
    }
    if (t === ref) ref = ref.nextSibling
    else parent.insertBefore(t, ref)
    if (o) morph(o, n)
  }
}

function morph(o: Node, n: Node): void {
  if (o.nodeType !== 1) {
    if (o.nodeValue !== n.nodeValue) o.nodeValue = n.nodeValue
    return
  }
  const a = o as HTMLInputElement
  const b = n as HTMLInputElement
  for (const at of Array.from(a.attributes)) if (!b.hasAttribute(at.name)) a.removeAttribute(at.name)
  for (const at of Array.from(b.attributes)) if (a.getAttribute(at.name) !== at.value) a.setAttribute(at.name, at.value)
  const hn = (b as Live)._on
  const ho = (a as Live)._on
  if (ho) for (const k in ho) if (!hn?.[k]) ho[k] = undefined
  if (hn) for (const k in hn) on(a, k.replace("!", ""), hn[k] as Handler, k.endsWith("!"))
  patch(a, Array.from(b.childNodes))
  // What the user typed lives on the property: written only when the
  // build says otherwise, so a kept field keeps its caret.
  if (/^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName) && a.value !== b.value) a.value = b.value
  if (a.tagName === "INPUT" && a.checked !== b.checked) a.checked = b.checked
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

/** spanWindow is the spans the waterfall can place (readable times, an
 * end not before the start) and their [min start, max end] window. */
export function spanWindow(spans: { name: string; start: string; end: string }[]) {
  const parsed = spans
    .map((s) => ({ name: s.name, a: Date.parse(s.start), b: Date.parse(s.end) }))
    .filter((s) => Number.isFinite(s.a) && Number.isFinite(s.b) && s.b >= s.a)
  const from = parsed.length ? Math.min(...parsed.map((s) => s.a)) : 0
  const to = parsed.length ? Math.max(...parsed.map((s) => s.b)) : 0
  return { parsed, from, to, placed: parsed.length }
}

/** waterfall lays spans over the run's [min start, max end] window.
 * Spanless runs and unparseable times yield nothing. */
export function waterfall(
  spans: { name: string; start: string; end: string }[]
): WaterfallBar[] {
  const { parsed, from, to } = spanWindow(spans)
  if (!parsed.length) return []
  const span = Math.max(1, to - from)
  return parsed.map((s) => ({
    name: s.name,
    left: (s.a - from) / span,
    width: Math.max(0.005, (s.b - s.a) / span),
    ms: s.b - s.a,
  }))
}
