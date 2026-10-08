// The dock's layout (plan D1): float (dragged by the header, resized
// from the corner) or docked on a side (resized along its edge), the
// pill (collapsed) and hidden (nothing drawn; the API and events keep
// working). Pure geometry plus the one storage key: the element keeps
// a Layout outside its render, so a keyed renderer (D3) can keep it.
import type { PanelPlacement } from "./config"

export type Side = "left" | "right" | "bottom" | "top"

/** The order Alt+Shift+W (and the header's dock button) cycles. */
export const CYCLE = ["float", "right", "bottom", "left", "top"] as const

export interface Layout {
  mode: "float" | "dock"
  side: Side
  /** Expanded (false: the pill). */
  open: boolean
  /** Nothing drawn; Alt+W or open() leaves it. */
  hidden: boolean
  /** The float's box, px: left, top, width, height. */
  x: number
  y: number
  w: number
  h: number
  /** The dock's size across its edge, px. */
  d: number
  /** The last selected turn, by run id. */
  run: string
  /** The user's theme choice (D2, the ◐ button): "light", "dark", or "" for auto. */
  theme: string
  raw: boolean
  /** The open turn's tab (D4): story, request, timeline or raw (raw
   * mirrors it, so a v1 reader that knows only raw still agrees). */
  tab: string
  /** The §5.3 force-on switch, migrated from localStorage.weft_debug. */
  debug: boolean
}

/** The turn view's tabs (D4), in their order. */
export const TABS = ["story", "request", "timeline", "raw"]

/** The one key the panel keeps, per origin. */
export const STORE_KEY = "weft.devtools"
/** The float's smallest box; the dock's smallest size (width docked
 * left/right, height docked top/bottom). */
export const MIN_W = 360
export const MIN_H = 280
/** What the viewport keeps free: no box is larger than it minus this. */
export const GAP = 16
/** Under this panel width the turn column is a dropdown. */
export const NARROW_W = 640
/** Under this viewport width the panel is a bottom sheet. */
export const SHEET_VW = 480

const clamp = (v: number, lo: number, hi: number) => Math.min(Math.max(v, lo), Math.max(lo, hi))
const vw = () => window.innerWidth || 1024
const vh = () => window.innerHeight || 768

/** initialLayout is what the data-* attributes say (data-position,
 * data-open, data-mode), before storage. */
export function initialLayout(position: PanelPlacement, open: boolean, mode: string): Layout {
  const dock = position.endsWith("-dock")
  const w = 520
  const h = 560
  const l: Layout = {
    // An explicit data-mode float|dock wins over what the position implies.
    mode: mode === "float" ? "float" : dock || mode === "dock" ? "dock" : "float",
    side: dock ? (position.slice(0, -5) as Side) : "right",
    open: mode === "pill" ? false : mode === "float" || mode === "dock" || open,
    hidden: mode === "hidden",
    x: position === "bottom-left" ? GAP : vw() - w - GAP,
    y: vh() - h - GAP,
    w,
    h,
    d: 460,
    run: "",
    theme: "",
    raw: false,
    tab: "story",
    debug: false,
  }
  return l
}

/** storage is localStorage, or null where reading it throws. */
function storage(): Storage | null {
  try {
    return window.localStorage
  } catch {
    return null
  }
}

/** readStore reads the stored layout: version 1 only, each field of
 * the right type kept, anything else ignored. Never throws. */
export function readStore(): Partial<Layout> {
  try {
    const doc = JSON.parse(storage()?.getItem(STORE_KEY) ?? "null") as Record<string, unknown> | null
    if (!doc || typeof doc !== "object" || doc.v !== 1) return {}
    const out: Record<string, unknown> = {}
    const want: Record<string, string> = {
      x: "number", y: "number", w: "number", h: "number", d: "number",
      open: "boolean", hidden: "boolean", raw: "boolean", debug: "boolean",
      run: "string", theme: "string", tab: "string",
    }
    for (const [k, t] of Object.entries(want))
      if (typeof doc[k] === t && (t !== "number" || Number.isFinite(doc[k]))) out[k] = doc[k]
    if (doc.mode === "float" || doc.mode === "dock") out.mode = doc.mode
    if (["left", "right", "bottom", "top"].includes(doc.side as string)) out.side = doc.side
    // D4: tab is additive; a v1 document from before it says raw only.
    if (!TABS.includes(out.tab as string)) out.tab = out.raw ? "raw" : "story"
    return out
  } catch {
    return {}
  }
}

/** The fields that place the panel: stored only once the user placed
 * it (toggled, moved, resized, re-docked), so until then the page's
 * data-* attributes keep deciding. */
export const PLACE = ["mode", "side", "open", "hidden", "x", "y", "w", "h", "d"]

/** placedIn: a stored layout carries a placement. */
export const placedIn = (st: Partial<Layout>) => PLACE.some((k) => k in st)

/** writeStore writes the layout under STORE_KEY (its placement only
 * when placed); a private window (or a full quota) just forgets. */
export function writeStore(l: Layout, placed: boolean): void {
  try {
    const out: Record<string, unknown> = { v: 1 }
    for (const [k, v] of Object.entries(l)) if (placed || !PLACE.includes(k)) out[k] = v
    storage()?.setItem(STORE_KEY, JSON.stringify(out))
  } catch {
    // forgotten: the panel still works
  }
}

/** migrateDebug moves localStorage.weft_debug=1 into the key (debug)
 * and drops the old one; reports whether the switch is on. */
export function migrateDebug(): boolean {
  let old = false
  try {
    const s = storage()
    old = s?.getItem("weft_debug") === "1"
    if (old) {
      s?.setItem(STORE_KEY, JSON.stringify({ v: 1, ...readStore(), debug: true }))
      s?.removeItem("weft_debug")
    }
  } catch {
    // not migrated (a full quota, an old private mode): the old key
    // stays, and still forces the panel on
  }
  return old || readStore().debug === true
}

/** horizontal: docked on the left or right (sized by width). */
export const horizontal = (side: Side) => side === "left" || side === "right"

/** clampLayout fits the box to the viewport: the float between its
 * minimum and the viewport minus GAP, inside the viewport; the dock
 * the same along its axis. */
export function clampLayout(l: Layout): void {
  const W = vw()
  const H = vh()
  l.w = clamp(l.w, Math.min(MIN_W, W - GAP), W - GAP)
  l.h = clamp(l.h, Math.min(MIN_H, H - GAP), H - GAP)
  l.x = clamp(l.x, 0, W - l.w)
  l.y = clamp(l.y, 0, H - l.h)
  const along = horizontal(l.side) ? W : H
  const min = horizontal(l.side) ? MIN_W : MIN_H
  l.d = clamp(l.d, Math.min(min, along - GAP), along - GAP)
}

/** Geometry is what the dock element is given: its classes, its
 * inline box, and its width (the turn column's dropdown reads it). */
export interface Geometry {
  cls: string
  style: Record<string, string>
  width: number
  sheet: boolean
}

/** geometry places the dock: under SHEET_VW of viewport a bottom
 * sheet (full width, 70vh at most; CSS), else the float's box or the
 * dock's size. */
export function geometry(l: Layout): Geometry {
  const W = vw()
  if (W < SHEET_VW) return { cls: "weft-sheet", style: {}, width: W, sheet: true }
  if (l.mode === "float")
    return {
      cls: "weft-float",
      style: { left: `${l.x}px`, top: `${l.y}px`, width: `${l.w}px`, height: `${l.h}px` },
      width: l.w,
      sheet: false,
    }
  const hz = horizontal(l.side)
  return {
    cls: `weft-docked weft-side-${l.side}`,
    style: hz ? { width: `${l.d}px` } : { height: `${l.d}px` },
    width: hz ? l.d : W,
    sheet: false,
  }
}

/** pillPlace is where the pill sits: the float's corner (the half of
 * the viewport its box is in), a dock's side. */
export function pillPlace(l: Layout): string {
  if (l.mode === "float") return l.x + l.w / 2 < vw() / 2 ? "bottom-left" : "bottom-right"
  return l.side === "left" ? "bottom-left" : l.side === "top" ? "top-right" : "bottom-right"
}

/** Push is data-push's one write to the host document: padding on
 * the docked side of <html>, through --weft-devtools-inset, restored
 * exactly (value and priority, or removed) when it ends. */
const INSET = "--weft-devtools-inset"
const INSET_VAR = `var(${INSET})`

export class Push {
  private prev: { prop: string; was: [string, string][] } | null = null

  /** apply pads side by value (a CSS length: the dock's px, the
   * sheet's 70vh). It writes only what is not already its own: a value
   * the host set on that side since is taken as the host's (restore
   * gives it back) and replaced again while docked. */
  apply(side: Side | null, value: string): void {
    const st = document.documentElement.style
    const prop = side ? `padding-${side}` : ""
    if (this.prev && this.prev.prop !== prop) this.restore()
    if (!side) return
    const snap = (p: string): [string, string] => [st.getPropertyValue(p), st.getPropertyPriority(p)]
    const ours = st.getPropertyValue(prop) === INSET_VAR
    if (!this.prev) this.prev = { prop, was: [snap(INSET), snap(prop)] }
    else if (!ours) this.prev.was[1] = snap(prop)
    if (st.getPropertyValue(INSET) !== value) st.setProperty(INSET, value)
    if (!ours) st.setProperty(prop, INSET_VAR)
  }

  restore(): void {
    const p = this.prev
    if (!p) return
    this.prev = null
    const st = document.documentElement.style
    ;[INSET, p.prop].forEach((name, i) => {
      const [v, prio] = p.was[i]
      if (v) st.setProperty(name, v, prio)
      else st.removeProperty(name)
    })
  }
}
