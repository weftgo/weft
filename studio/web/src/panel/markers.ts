// Scope detection, rung 3 — the DOM marker (plan C3.3, the production
// rung of §13.3). The page's chat element carries its conversation as
// data-weft-scope="pub_…;session=…;flow=…;run=…" (lib/scope.ts's form;
// the framework helpers set it, src/npm/marker.ts). The panel reads
// those attributes and nothing else:
//
//   - passive: attributes read, never written; no global touched
//     (window.fetch stays as the page left it);
//   - one MutationObserver on document.documentElement, its scans
//     debounced with a trailing call, and one capturing, passive
//     focusin listener on document — both removed by disconnect;
//   - never the panel's own tree: a <weft-devtools> (which carries the
//     scope scope()/mount() set) and anything inside one is skipped,
//     and its shadow tree is out of a document query's reach;
//   - silent: no console output, a failure is swallowed.
import { parseScope } from "../lib/scope"
import type { Scope } from "../lib/scope"

/** The marker attribute (the helpers' data-weft-scope). */
export const MARKER_ATTR = "data-weft-scope"

/** One marker on the page: its parsed scope and the element. */
export interface Marker {
  scope: Scope
  element: Element
}

export interface MarkerRungOptions {
  /** The document scanned (default document). */
  root?: Document
  /** Called with every marker, document order, on install and after
   * each settled run of DOM changes. */
  onScopes: (markers: Marker[]) => void
  /** Called with the marker element that holds a newly focused node. */
  onFocus?: (element: Element) => void
  /** The scan's debounce (default 100 ms, trailing). */
  debounceMs?: number
}

export interface MarkerRung {
  /** disconnect removes the observer, the focusin listener and a
   * pending scan. Idempotent. */
  disconnect: () => void
}

const PANEL = "weft-devtools"

/** markerOf is the nearest element at or above n carrying the marker,
 * outside any <weft-devtools>; null when there is none. */
export function markerOf(n: unknown): Element | null {
  try {
    const e = n instanceof Element ? n.closest(`[${MARKER_ATTR}]`) : null
    return e && !e.closest(PANEL) ? e : null
  } catch {
    return null
  }
}

/** scanMarkers lists root's markers in document order: an empty or
 * unparsable value (no public id) is ignored, the panel's own elements
 * are skipped. */
export function scanMarkers(root: Document = document): Marker[] {
  const out: Marker[] = []
  for (const element of Array.from(root.querySelectorAll(`[${MARKER_ATTR}]`))) {
    if (element.closest(PANEL)) continue
    const scope = parseScope(element.getAttribute(MARKER_ATTR) ?? "")
    if (scope.publicId) out.push({ scope, element })
  }
  return out
}

/** installMarkerRung scans root's markers now and after DOM changes,
 * and reports focus moving into one. null where the document cannot be
 * observed — nothing is installed then. */
export function installMarkerRung(opts: MarkerRungOptions): MarkerRung | null {
  const root = opts.root ?? document
  let timer: ReturnType<typeof setTimeout> | null = null
  let obs: MutationObserver | null = null
  const scan = () => {
    timer = null
    try {
      opts.onScopes(scanMarkers(root))
    } catch {
      // fail silent: observability never changes behaviour
    }
  }
  const focus = (e: Event) => {
    const m = markerOf(e.target)
    if (!m) return
    try {
      opts.onFocus?.(m)
    } catch {
      // fail silent
    }
  }
  try {
    obs = new MutationObserver((records) => {
      // The panel's own attribute (scope() sets it) is not the page's.
      if (records.every((r) => r.target instanceof Element && r.target.closest(PANEL))) return
      if (timer) clearTimeout(timer)
      timer = setTimeout(scan, opts.debounceMs ?? 100)
    })
    obs.observe(root.documentElement, { subtree: true, childList: true, attributes: true, attributeFilter: [MARKER_ATTR] })
    root.addEventListener("focusin", focus, { capture: true, passive: true })
  } catch {
    obs?.disconnect()
    return null
  }
  scan()
  return {
    disconnect() {
      obs?.disconnect()
      obs = null
      if (timer) clearTimeout(timer)
      timer = null
      root.removeEventListener("focusin", focus, { capture: true })
    },
  }
}
