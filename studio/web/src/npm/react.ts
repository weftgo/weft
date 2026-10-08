// @weftgo/devtools/react — one hook-shaped helper, no component and no
// React import: it returns a callback ref, which React (18 and 19)
// calls with the element and on detach.
import { bindNode, releaseNode } from "./marker.js"
import type { HelperOptions } from "./marker.js"

export type { HelperOptions } from "./marker.js"
export type { Scope } from "../lib/scope.js"

/** useWeftDevtools returns a ref for the element that shows the
 * conversation: <div ref={useWeftDevtools({ scope: { publicId } })}>.
 * On attach it sets data-weft-scope on that element, mounts the panel
 * when endpoint or token is given and the page has none
 * (mount({endpoint, token})), and calls scope(opts.scope); on detach
 * it removes the marker. A re-render with the same options does
 * nothing (one binding per element, whichever render's ref holds it). */
export function useWeftDevtools(opts: HelperOptions): (node: Element | null) => (() => void) | undefined {
  let last: Element | null = null
  return (node) => {
    if (!node) {
      // React 18: the previous ref is called with null.
      if (last) releaseNode(last)
      last = null
      return undefined
    }
    last = node
    bindNode(node, opts)
    // React 19: the cleanup this ref returns.
    return () => releaseNode(node)
  }
}
