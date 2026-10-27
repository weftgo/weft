// useKeepInView (P020.1): a page opened on one item (the Agents page's
// ?agent=) scrolls it into view — and keeps it there while the page
// above it still lays out (charts that load after the list, cards that
// grow), until the reader takes over (a wheel, a touch, a key, a
// pointer press) or HOLD_MS pass. A scroll made once, before the
// layout settles, ends wherever the item was then.
import { useEffect } from "react"

/** How long the item is held in view while nobody scrolls. */
export const HOLD_MS = 8_000

/** The reader taking over: any of these ends the hold. */
const TAKE_OVER = ["wheel", "touchstart", "keydown", "pointerdown"] as const

/** useKeepInView scrolls the first element `selector` matches to the
 * top of the view when ready, again on every change of `deps` (data
 * that lays out above it, landing) and on every resize of the document
 * meanwhile. No selector, or not ready: nothing. */
export function useKeepInView(
  selector: string | undefined,
  ready: boolean,
  deps: readonly unknown[] = []
): void {
  useEffect(() => {
    if (!selector || !ready || typeof document === "undefined") return
    let held = true
    const go = () => {
      if (held)
        document.querySelector(selector)?.scrollIntoView({ block: "start" })
    }
    go()
    const ro =
      typeof ResizeObserver === "function"
        ? new ResizeObserver(() => go())
        : undefined
    ro?.observe(document.body)
    const stop = () => {
      held = false
      ro?.disconnect()
      clearTimeout(timer)
      for (const e of TAKE_OVER) window.removeEventListener(e, stop, true)
    }
    for (const e of TAKE_OVER)
      window.addEventListener(e, stop, { capture: true, passive: true })
    const timer = setTimeout(stop, HOLD_MS)
    return stop
    // deps: the caller's data that lays out above the item.
  }, [selector, ready, ...deps])
}
