// useKeepInView (P020.1): a page opened on one item (the Agents page's
// ?agent=) scrolls it into view — and keeps it there while the page
// above it still lays out (charts that load after the list, cards that
// grow), until the reader takes over (a wheel, a touch, a key, a
// pointer press, a scroll that moved the item: a scrollbar drag sends
// no pointer event) or HOLD_MS pass. A scroll made once, before the
// layout settles, ends wherever the item was then. Once the hold ends
// it is over for that item: new data never pulls the page back.
import { useEffect, useRef } from "react"

/** How long the item is held in view while nobody scrolls. */
export const HOLD_MS = 8_000

/** The reader taking over: any of these ends the hold. */
const TAKE_OVER = ["wheel", "touchstart", "keydown", "pointerdown"] as const

/** How far (px) the item may move under a scroll the hold did not make
 * before that scroll counts as the reader's (scroll anchoring keeps it
 * still; a reader's scroll moves it). */
const MOVED_PX = 4

/** useKeepInView scrolls the first element `selector` matches to the
 * top of the view when ready, again on every change of `deps` (data
 * that lays out above it, landing) and on every resize of the document
 * meanwhile. No selector, or not ready: nothing. */
export function useKeepInView(
  selector: string | undefined,
  ready: boolean,
  deps: readonly unknown[] = []
): void {
  // The selector whose hold ended (taken over, or timed out): a later
  // run of the effect (new deps) never starts it again.
  const over = useRef<string>(undefined)
  useEffect(() => {
    if (
      !selector ||
      !ready ||
      over.current === selector ||
      typeof document === "undefined"
    )
      return
    let held = true
    // The item's top after the hold's own scroll (scrollIntoView lays
    // out at once: its scroll event finds the item there).
    let placed = 0
    const top = () =>
      document.querySelector(selector)?.getBoundingClientRect().top ?? 0
    const go = () => {
      const n = held ? document.querySelector(selector) : null
      if (!n) return
      n.scrollIntoView({ block: "start" })
      placed = top()
    }
    const ro =
      typeof ResizeObserver === "function"
        ? new ResizeObserver(() => go())
        : undefined
    const dispose = () => {
      held = false
      ro?.disconnect()
      clearTimeout(timer)
      for (const e of TAKE_OVER) window.removeEventListener(e, end, true)
      window.removeEventListener("scroll", onScroll, true)
    }
    const end = () => {
      over.current = selector
      dispose()
    }
    const onScroll = () => {
      if (Math.abs(top() - placed) > MOVED_PX) end()
    }
    go()
    ro?.observe(document.body)
    for (const e of TAKE_OVER)
      window.addEventListener(e, end, { capture: true, passive: true })
    window.addEventListener("scroll", onScroll, {
      capture: true,
      passive: true,
    })
    const timer = setTimeout(end, HOLD_MS)
    // A dep change or unmount ends this run, not the hold.
    return dispose
    // deps: the caller's data that lays out above the item.
  }, [selector, ready, ...deps])
}
