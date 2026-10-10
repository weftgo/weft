// The virtualised lists' shared rules (plan H2): the story's cards,
// the waterfall's rows and the raw events table.
//
//  - withPinned: the range a list mounts is the rows in view plus an
//    overscan, and always the row the URL names (a ?step= card, a ?ev=
//    row, the selected span) — mounted before the virtualiser has
//    scrolled to it, so a deep link's target is in the DOM from the
//    first render.
//  - observeRect / measure: a scroller or a row that reports no size
//    is not laid out (a hidden tab, jsdom) — read as a viewport-tall
//    window and as the row's estimate, never as 0 (a 0-tall window
//    mounts nothing; 0-tall rows would mount every row at once).
import {
  defaultRangeExtractor,
  measureElement,
  observeElementRect,
} from "@tanstack/react-virtual"
import type { Range, Virtualizer } from "@tanstack/react-virtual"

export function withPinned(pinned: number) {
  return (range: Range): number[] => {
    const out = defaultRangeExtractor(range)
    if (pinned < 0 || pinned >= range.count || out.includes(pinned)) return out
    out.push(pinned)
    return out.sort((a, b) => a - b)
  }
}

/** The window a scroller with no layout is read as. */
function fallbackRect(): { width: number; height: number } {
  const h =
    typeof window !== "undefined" && window.innerHeight > 0
      ? window.innerHeight
      : 768
  const w =
    typeof window !== "undefined" && window.innerWidth > 0
      ? window.innerWidth
      : 1024
  return { width: w, height: h }
}

export function observeRect<T extends Element>(
  instance: Virtualizer<T, Element>,
  cb: (rect: { width: number; height: number }) => void
) {
  return observeElementRect(instance, (rect) =>
    cb(rect.width === 0 && rect.height === 0 ? fallbackRect() : rect)
  )
}

export function measure<T extends Element>(
  element: Element,
  entry: ResizeObserverEntry | undefined,
  instance: Virtualizer<T, Element>
): number {
  const size = measureElement(element, entry, instance)
  if (size > 0) return size
  return instance.options.estimateSize(instance.indexFromElement(element))
}

/** The options every list shares: the pinned row and the no-layout
 * reads. */
export function shared(pinned: number) {
  return {
    rangeExtractor: withPinned(pinned),
    observeElementRect: observeRect<HTMLDivElement>,
    measureElement: measure<HTMLDivElement>,
  }
}
