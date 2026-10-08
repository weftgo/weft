// The Raw tab's JSON tree (plan D4): hand-written, no library. One
// flat list of rows keyed by path (the keyed patch keeps them), drawn
// lazily — a collapsed node's children are never built, a container
// shows KIDS_CAP children and says how many more, a string shows
// STR_CAP characters and says how many bytes more — so a 10 MB
// transcript costs only the rows that are open. A "/" filter matches
// keys and values (case-insensitive), opens the path to each match and
// marks it; copy-node, copy-all and download hand the JSON out.
import { el, on, stringify } from "./render"

/** A string longer than this shows its head and a "+N bytes" expander. */
export const STR_CAP = 2048
/** A container shows this many children, then "+N more". */
export const KIDS_CAP = 200
/** The rows opened by default, breadth-first: a small document opens
 * whole, a large one only its top. */
export const AUTO_ROWS = 150
/** The matches the filter opens the path to (all are counted). */
const OPEN_HITS = 200

/** What the tree remembers between draws (the element owns it). */
export interface TreeState {
  /** Explicit toggles by path, over the default opening. */
  open: Map<string, boolean>
  /** Children shown past KIDS_CAP, by path. */
  more: Map<string, number>
  /** Strings shown whole, by path. */
  full: Set<string>
  q: string
  /** The last copy's outcome ("copied", or why not). */
  said: string
  /** The text to select by hand when the clipboard refused it. */
  box: string
  memo?: { root: unknown; q: string; hits: Set<string>; via: Set<string>; n: number }
}

export const newTree = (): TreeState => ({ open: new Map(), more: new Map(), full: new Set(), q: "", said: "", box: "" })

type Box = Record<string, unknown> | unknown[]
const isBox = (v: unknown): v is Box => v !== null && typeof v === "object"
const size = (v: Box) => (Array.isArray(v) ? v.length : Object.keys(v).length)
/** each visits a container's children, keys as strings. */
function each(v: Box, fn: (k: string, c: unknown, i: number) => void, to = Infinity) {
  if (Array.isArray(v)) for (let i = 0; i < Math.min(v.length, to); i++) fn(String(i), v[i], i)
  else {
    const ks = Object.keys(v)
    for (let i = 0; i < Math.min(ks.length, to); i++) fn(ks[i], v[ks[i]], i)
  }
}
/** A child's path: JSON-pointer escaped. */
const at = (p: string, k: string) => `${p}/${k.replace(/~/g, "~0").replace(/\//g, "~1")}`

/** json is a node's JSON text; never throws (a cycle reads as text). */
export function json(v: unknown, indent = 2): string {
  try {
    return stringify(v, indent) ?? String(v)
  } catch {
    return String(v)
  }
}

/** autoOpen is the default opening: breadth-first while the rows it
 * would show stay within AUTO_ROWS. */
export function autoOpen(root: unknown): Set<string> {
  const out = new Set<string>()
  if (!isBox(root)) return out
  let rows = 0
  const q: [string, Box][] = [["", root]]
  // The queue grows as it is walked (breadth-first).
  for (const [p, v] of q) {
    const n = Math.min(size(v), KIDS_CAP)
    if (p && rows + n > AUTO_ROWS) continue
    out.add(p)
    rows += n
    each(v, (k, c) => isBox(c) && q.push([at(p, k), c]), KIDS_CAP)
  }
  return out
}

/** search walks the whole document once per (document, query): the
 * paths whose key or value holds q, and the containers on the way to
 * the first OPEN_HITS of them. */
export function search(root: unknown, q: string): { hits: Set<string>; via: Set<string>; n: number } {
  const hits = new Set<string>()
  const via = new Set<string>()
  let n = 0
  const want = q.toLowerCase()
  const stack: [string, string | null, unknown][] = [["", null, root]]
  while (stack.length) {
    const [p, k, v] = stack.pop()!
    const leaf = isBox(v) ? "" : String(v)
    if ((k !== null && k.toLowerCase().includes(want)) || leaf.toLowerCase().includes(want)) {
      n++
      if (hits.size < OPEN_HITS) {
        hits.add(p)
        for (let i = p.lastIndexOf("/"); i > 0; i = p.lastIndexOf("/", i - 1)) via.add(p.slice(0, i))
        via.add("")
      }
    }
    if (isBox(v)) each(v, (ck, c) => stack.push([at(p, ck), ck, c]))
  }
  return { hits, via, n }
}

/** utf8 is a string's length in UTF-8 bytes. */
const utf8 = (s: string) => new TextEncoder().encode(s).length

/** preview is a collapsed container's one line. */
const preview = (v: Box) => (Array.isArray(v) ? `[…] ${v.length} items` : `{…} ${size(v)} keys`)

export interface TreeCtx {
  /** Redraw the panel (a toggle, the filter, a copy's outcome). */
  redraw: () => void
  /** The shadow root the tree is drawn into (the copy fallback selects in it). */
  root: () => ParentNode | null
}

/** copy puts text on the clipboard, guarded: no clipboard (or a
 * refusal) shows it in a box, selected, to copy by hand. Never throws. */
export function copy(st: TreeState, text: string, cx: TreeCtx) {
  const done = (ok: boolean) => {
    st.said = ok ? "copied" : "the clipboard refused: select and copy below"
    st.box = ok ? "" : text
    cx.redraw()
    if (!ok)
      try {
        cx.root()?.querySelector<HTMLTextAreaElement>(".weft-copybox")?.select()
      } catch {
        // nothing to select
      }
  }
  try {
    // Absent outside a secure context, whatever the DOM types say.
    const c = (navigator as { clipboard?: Clipboard }).clipboard
    const p = c ? c.writeText(text) : null
    if (p) p.then(() => done(true), () => done(false))
    else done(false)
  } catch {
    done(false)
  }
}

/** treeView draws the Raw tab: the toolbar (filter, count, copy all,
 * download) and the rows. name is the download's file name. */
export function treeView(root: unknown, st: TreeState, name: string, cx: TreeCtx): HTMLElement {
  const q = st.q.trim()
  let found: { hits: Set<string>; via: Set<string>; n: number } | null = null
  if (q) {
    const m = st.memo
    found = m && m.root === root && m.q === q ? m : (st.memo = { root, q, ...search(root, q) })
  }
  const auto = autoOpen(root)
  const isOpen = (p: string) => st.open.get(p) ?? (found?.via.has(p) || auto.has(p))
  const rows: HTMLElement[] = []
  const draw = (k: string, v: unknown, p: string, depth: number) => {
    const box = isBox(v)
    const open = box && isOpen(p)
    const r = el("div", `weft-tn${found?.hits.has(p) ? " weft-hit" : ""}`, undefined, { "data-key": `n${p}` })
    r.style.paddingLeft = `${depth * 12}px`
    if (box) {
      const t = el("button", "weft-tt", open ? "▾" : "▸", {
        type: "button",
        "aria-expanded": String(open),
        "aria-label": `${open ? "collapse" : "expand"} ${k}`,
      })
      on(t, "click", () => {
        st.open.set(p, !open)
        cx.redraw()
      })
      r.appendChild(t)
    } else r.appendChild(el("span", "weft-tt", "", { "aria-hidden": "true" }))
    r.appendChild(el("span", "weft-tk", `${k}: `))
    if (box) r.appendChild(el("span", "weft-tv", preview(v)))
    else if (typeof v === "string" && v.length > STR_CAP && !st.full.has(p)) {
      r.appendChild(el("span", "weft-tv", `${JSON.stringify(v.slice(0, STR_CAP)).slice(0, -1)}…`))
      const more = el("button", "weft-btn weft-tmore", `… +${utf8(v.slice(STR_CAP))} bytes`, { type: "button", title: "show the whole string" })
      on(more, "click", () => {
        st.full.add(p)
        cx.redraw()
      })
      r.appendChild(more)
    } else r.appendChild(el("span", "weft-tv", json(v, 0)))
    const c = el("button", "weft-tc", "⧉", { type: "button", title: "copy this node's JSON", "aria-label": `copy ${k}` })
    on(c, "click", () => copy(st, json(v), cx))
    r.appendChild(c)
    rows.push(r)
    if (open) kids(v, p, depth + 1)
  }
  const kids = (v: Box, p: string, depth: number) => {
    const cap = KIDS_CAP + (st.more.get(p) ?? 0)
    let hidden = 0
    each(v, (k, c, i) => {
      const cp = at(p, k)
      // A match past the cap is drawn anyway: the filter found it.
      if (i < cap || found?.via.has(cp) || found?.hits.has(cp)) draw(k, c, cp, depth)
      else hidden++
    })
    if (hidden) {
      const more = el("button", "weft-btn weft-tmore", `… +${hidden} more`, { type: "button", "data-key": `m${p}` })
      more.style.marginLeft = `${depth * 12 + 16}px`
      on(more, "click", () => {
        st.more.set(p, (st.more.get(p) ?? 0) + KIDS_CAP)
        cx.redraw()
      })
      rows.push(more)
    }
  }
  if (isBox(root)) kids(root, "", 0)
  else draw("value", root, "", 0)

  const bar = el("div", "weft-tbar", undefined, { "data-key": "tbar" })
  const input = el("input", "weft-input weft-tree-q", undefined, {
    type: "search",
    "aria-label": "filter keys and values",
    placeholder: "/ filter keys and values",
  }) as HTMLInputElement
  input.value = st.q
  on(input, "input", (_, n) => {
    st.q = (n as HTMLInputElement).value
    // A new query lays the tree out afresh: the paths to its matches
    // open, whatever was toggled before.
    st.open.clear()
    cx.redraw()
  })
  bar.appendChild(input)
  bar.appendChild(el("span", "weft-tree-n", found ? `${found.n} match${found.n === 1 ? "" : "es"}` : ""))
  const all = el("button", "weft-btn", "copy all", { type: "button", title: "copy the whole document's JSON" })
  on(all, "click", () => copy(st, json(root), cx))
  bar.appendChild(all)
  const dl = el("a", "weft-btn weft-dl", "download", { href: "#", download: `${name}.json`, title: `save as ${name}.json` })
  on(dl, "click", (_, n) => {
    const text = json(root)
    try {
      const url = URL.createObjectURL(new Blob([text], { type: "application/json" }))
      setTimeout(() => URL.revokeObjectURL(url), 60_000)
      n.setAttribute("href", url)
    } catch {
      n.setAttribute("href", `data:application/json;charset=utf-8,${encodeURIComponent(text)}`)
    }
  })
  bar.appendChild(dl)
  if (st.said) bar.appendChild(el("span", "weft-tree-said", st.said))
  const out = el("div", "weft-raw", [bar], { "data-key": "raw" })
  if (st.box) {
    const ta = el("textarea", "weft-input weft-copybox", undefined, { readonly: "", "aria-label": "the JSON to copy", rows: "4" }) as HTMLTextAreaElement
    ta.value = st.box
    out.appendChild(ta)
  }
  const list = el("div", "weft-tree", rows, { "data-key": "tree" })
  // ArrowRight opens a node, ArrowLeft closes it (Enter and Space are
  // the button's own).
  on(list, "keydown", (e) => {
    const k = (e as KeyboardEvent).key
    const t = e.target as HTMLElement
    if ((k !== "ArrowRight" && k !== "ArrowLeft") || !t.classList.contains("weft-tt")) return
    if ((t.getAttribute("aria-expanded") === "true") !== (k === "ArrowLeft")) return
    e.preventDefault()
    t.click()
  })
  out.appendChild(list)
  return out
}
