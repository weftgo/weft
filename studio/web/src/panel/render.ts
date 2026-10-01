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

/** fmtJSON pretty-prints for the raw toggle; a cycle or a BigInt
 * falls back to String so the toggle never throws into the page. */
export function fmtJSON(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2) ?? String(v)
  } catch {
    return String(v)
  }
}
