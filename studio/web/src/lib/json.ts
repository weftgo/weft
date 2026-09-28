// JSON helpers for the code windows and the raw explorer: detect
// JSON text so a tool result that is JSON pretty-prints, and a tiny
// tokenizer that colours it — no dependency, a few hundred bytes.

/** Parse when the text is a JSON object or array; else null. */
export function tryJSON(text: string): unknown | null {
  const t = text.trim()
  if (!(t.startsWith("{") || t.startsWith("["))) return null
  try {
    return JSON.parse(t) as unknown
  } catch {
    return null
  }
}

export type Token = {
  kind: "key" | "string" | "number" | "literal" | "punct" | "ws"
  text: string
}

const RE =
  /("(?:\\.|[^"\\])*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|(true|false|null)|([{}[\],:])|(\s+)/g

/** Tokenize pretty-printed JSON text for syntax colouring. */
export function tokenize(text: string): Token[] {
  const out: Token[] = []
  let last = 0
  for (const m of text.matchAll(RE)) {
    const i = m.index
    if (i > last) out.push({ kind: "punct", text: text.slice(last, i) })
    // Unmatched groups are undefined at runtime whatever the lib types say.
    const g = m as unknown as (string | undefined)[]
    if (g[1] !== undefined) {
      if (g[2] !== undefined) {
        out.push({ kind: "key", text: g[1] })
        out.push({ kind: "punct", text: g[2] })
      } else out.push({ kind: "string", text: g[1] })
    } else if (g[3] !== undefined) out.push({ kind: "number", text: g[3] })
    else if (g[4] !== undefined) out.push({ kind: "literal", text: g[4] })
    else if (g[5] !== undefined) out.push({ kind: "punct", text: g[5] })
    else if (g[6] !== undefined) out.push({ kind: "ws", text: g[6] })
    last = i + m[0].length
  }
  if (last < text.length) out.push({ kind: "punct", text: text.slice(last) })
  return out
}

export function pretty(value: unknown): string {
  return JSON.stringify(value, null, 2)
}

/** Save text as a file, offline: a Blob URL on an anchor click. */
export function download(
  name: string,
  text: string,
  type = "application/json"
) {
  const url = URL.createObjectURL(new Blob([text], { type }))
  const a = document.createElement("a")
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/** Copy to the clipboard; false when the browser refuses. */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}
