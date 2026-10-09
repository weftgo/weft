// Pane sizes (plan H3): each resizable split remembers where its
// divider was, per device — localStorage under studio.panes.<split>,
// never the URL (a pane size is a preference, not view state: a
// copied link opens at the reader's own layout). The value is the
// panes' percentages, in order; anything else — a missing key, a
// locked-down browser, a hand-edited or stale value — reads as "no
// saved layout" and the split opens at its default.

/** The splits Studio draws, by storage id (studio/README.md lists them). */
export const SPLITS = {
  /** The trace page: the span tree beside the selected span. */
  traceDetail: "trace-detail",
  /** The run page's story: a step's story beside its Request pane. */
  storyRequest: "story-request",
  /** The playground: the config column beside the runs column. */
  playground: "playground",
} as const

export type SplitID = (typeof SPLITS)[keyof typeof SPLITS]

const PREFIX = "studio.panes."

/** The event a layout reset raises: every mounted split goes back to
 * its default. */
export const PANES_RESET_EVENT = "studio:panes-reset"

export function paneKey(id: string): string {
  return PREFIX + id
}

/** readPaneSizes is the saved layout of split `id` with `count` panes,
 * or null: only `count` finite non-negative numbers that sum to 100
 * (±1, the library's rounding) are a layout. */
export function readPaneSizes(id: string, count: number): number[] | null {
  let raw: string | null
  try {
    raw = localStorage.getItem(paneKey(id))
  } catch {
    return null
  }
  if (!raw) return null
  let v: unknown
  try {
    v = JSON.parse(raw)
  } catch {
    return null
  }
  if (!Array.isArray(v) || v.length !== count) return null
  if (!v.every((n): n is number => typeof n === "number" && Number.isFinite(n) && n >= 0)) return null
  const sum = v.reduce((a, b) => a + b, 0)
  if (Math.abs(sum - 100) > 1) return null
  return v
}

/** The event a saved layout raises: the other mounted instances of
 * the same split (one per step card) follow it. */
export const PANES_CHANGED_EVENT = "studio:panes"

export type PanesChanged = { id: string; sizes: number[]; from: string }

/** writePaneSizes saves split `id`'s layout, as instance `from` left
 * it; unwritable storage keeps it for this page only (the mounted
 * splits still follow it). */
export function writePaneSizes(id: string, sizes: number[], from = "") {
  const rounded = sizes.map((n) => Math.round(n * 100) / 100)
  try {
    localStorage.setItem(paneKey(id), JSON.stringify(rounded))
  } catch {
    // unwritable storage: this page only
  }
  window.dispatchEvent(
    new CustomEvent<PanesChanged>(PANES_CHANGED_EVENT, { detail: { id, sizes: rounded, from } })
  )
}

/** resetPaneSizes forgets every split's saved layout and puts the
 * mounted ones back at their defaults (the ⌘K palette's "Reset pane
 * layout"). */
export function resetPaneSizes() {
  try {
    const keys: string[] = []
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i)
      if (k?.startsWith(PREFIX)) keys.push(k)
    }
    for (const k of keys) localStorage.removeItem(k)
  } catch {
    // unreadable storage: nothing was saved
  }
  window.dispatchEvent(new Event(PANES_RESET_EVENT))
}
