// @weftgo/devtools/vue — one composable-shaped helper, no component
// and no Vue import: it returns a function ref, which Vue calls with
// the element on every render and with null on unmount.
import { binder } from "./marker"
import type { HelperOptions } from "./marker"

export type { HelperOptions } from "./marker"
export type { Scope } from "../lib/scope"

/** useWeftDevtools returns a function ref for the element that shows
 * the conversation: const weft = useWeftDevtools(() => ({ scope:
 * { publicId: id.value } })) and <div :ref="weft">. Pass a getter to
 * follow reactive state (it is read on each render; an unchanged scope
 * does nothing). On bind it sets data-weft-scope on the element,
 * mounts the panel if the page has none and calls scope(); on unmount
 * it removes the marker. */
export function useWeftDevtools(opts: HelperOptions | (() => HelperOptions)): (el: unknown) => void {
  const b = binder(typeof opts === "function" ? opts : () => opts)
  return (el) => b.bind(el instanceof Element ? el : null)
}
