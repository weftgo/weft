// Scope detection, rung 3 — the DOM marker (plan C3.3, the production
// rung of §13.3). The page's chat element carries its conversation as
// data-weft-scope="pub_…;session=…;flow=…;run=…" (lib/scope.ts's form;
// the framework helpers set it, src/npm/marker.ts). The panel reads
// those attributes and nothing else:
//
//   - passive: attributes read, never written; no global touched
//     (window.fetch stays as the page left it);
//   - one MutationObserver on document.documentElement, its scans
//     debounced with a trailing call (at most MAX_WAIT_MS apart while
//     changes keep coming; none while the page is hidden, one when it
//     is shown), one capturing, passive focusin listener and one
//     visibilitychange listener on document — all removed by disconnect;
//   - never the panel's own tree: a <weft-devtools> (which carries the
//     scope scope()/mount() set) and anything inside one is skipped,
//     and its shadow tree is out of a document query's reach;
//   - never untrusted content: a marker on or inside an element carrying
//     data-weft-untrusted (the host's mark on rendered user/model HTML,
//     where a sanitizer such as DOMPurify keeps data-* attributes) is
//     not read, nor does focus inside one name a conversation;
//   - silent: no console output, a failure is swallowed.
import { parseScope } from "../lib/scope"
import type { Scope } from "../lib/scope"

/** The marker attribute (the helpers' data-weft-scope). */
export const MARKER_ATTR = "data-weft-scope"

/** The host's one-attribute escape hatch: markers on or inside an
 * element carrying it are ignored (rendered user or model HTML). */
export const UNTRUSTED_ATTR = "data-weft-untrusted"

/** untrusted: el is, or is inside, an element the host marked untrusted. */
const untrusted = (el: Element) => !!el.closest(`[${UNTRUSTED_ATTR}]`)

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

/** markerOf is the nearest element at or above n carrying the marker;
 * null when there is none, and null for n inside (or being) a
 * <weft-devtools> — focus in the panel's shadow tree reaches the
 * document retargeted to the panel element, and a panel placed inside
 * a marked chat is not that chat. */
export function markerOf(n: unknown): Element | null {
  try {
    if (!(n instanceof Element) || n.closest(PANEL)) return null
    // Inside untrusted HTML: the nearest marker outside it, if any — the
    // chat element around a rendered message still names its chat.
    let m = n.closest(`[${MARKER_ATTR}]`)
    while (m && untrusted(m)) m = m.parentElement?.closest(`[${MARKER_ATTR}]`) ?? null
    return m
  } catch {
    return null
  }
}

/** The longest a scan waits while DOM changes keep arriving (a chat
 * streaming its reply): the trailing debounce alone would never fire. */
export const MAX_WAIT_MS = 500

/** touchesMarkers reports whether a mutation record can change the
 * marker list: an attribute change outside the panel, or an added or
 * removed element that is or holds a marker. */
function touchesMarkers(r: MutationRecord): boolean {
  if (r.type === "attributes") return !(r.target instanceof Element && r.target.closest(PANEL))
  for (const n of [...Array.from(r.addedNodes), ...Array.from(r.removedNodes)])
    if (n instanceof Element && (n.hasAttribute(MARKER_ATTR) || n.querySelector(`[${MARKER_ATTR}]`))) return true
  // (An untrusted mark set or lifted is an attribute record: above.)
  return false
}

/** scanMarkers lists root's markers in document order: an empty or
 * unparsable value (no public id) is ignored, the panel's own elements
 * and anything on or inside a data-weft-untrusted element are skipped. */
export function scanMarkers(root: Document = document): Marker[] {
  const out: Marker[] = []
  for (const element of Array.from(root.querySelectorAll(`[${MARKER_ATTR}]`))) {
    if (element.closest(PANEL) || untrusted(element)) continue
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
  /** When the pending scan's first change came (0: none pending). */
  let since = 0
  /** A scan was due while the page was hidden: run it when shown. */
  let stale = false
  const scan = () => {
    timer = null
    since = 0
    if (root.hidden) {
      stale = true
      return
    }
    stale = false
    try {
      opts.onScopes(scanMarkers(root))
    } catch {
      // fail silent: observability never changes behaviour
    }
  }
  const shown = () => {
    if (stale && !root.hidden) scan()
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
      // The panel's own attribute (scope() sets it) is not the page's,
      // nor is a node that holds no marker.
      if (!records.some(touchesMarkers)) return
      const now = Date.now()
      since ||= now
      if (timer) clearTimeout(timer)
      timer = setTimeout(scan, Math.max(0, Math.min(opts.debounceMs ?? 100, since + MAX_WAIT_MS - now)))
    })
    obs.observe(root.documentElement, { subtree: true, childList: true, attributes: true, attributeFilter: [MARKER_ATTR, UNTRUSTED_ATTR] })
    root.addEventListener("focusin", focus, { capture: true, passive: true })
    root.addEventListener("visibilitychange", shown, { passive: true })
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
      root.removeEventListener("visibilitychange", shown)
    },
  }
}
