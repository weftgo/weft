// @weftgo/devtools/react — one hook-shaped helper, no component and no
// React import: it returns a callback ref, which React (18 and 19)
// calls with the element and on detach.
import { binder } from "./marker"
import type { HelperOptions } from "./marker"

export type { HelperOptions } from "./marker"
export type { Scope } from "../lib/scope"

/** useWeftDevtools returns a ref for the element that shows the
 * conversation: <div ref={useWeftDevtools({ scope: { publicId } })}>.
 * On attach it sets data-weft-scope on that element, mounts the panel
 * if the page has none (mount({endpoint, token})) and calls
 * scope(opts.scope); on detach it removes the marker. */
export function useWeftDevtools(opts: HelperOptions): (node: Element | null) => (() => void) | undefined {
  const b = binder(() => opts)
  return (node) => {
    b.bind(node)
    return node ? b.unbind : undefined
  }
}
