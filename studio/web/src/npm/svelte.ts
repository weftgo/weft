// @weftgo/devtools/svelte — one action, no component and no Svelte
// import: <div use:weftDevtools={{ scope: { publicId } }}>.
import { binder } from "./marker"
import type { HelperOptions } from "./marker"

export type { HelperOptions } from "./marker"
export type { Scope } from "../lib/scope"

/** weftDevtools is a Svelte action (3, 4 and 5): on mount it sets
 * data-weft-scope on the node, mounts the panel if the page has none
 * and calls scope(); an update with another scope rebinds; destroy
 * removes the marker. */
export function weftDevtools(
  node: Element,
  opts: HelperOptions
): { update: (next: HelperOptions) => void; destroy: () => void } {
  let cur = opts
  const b = binder(() => cur)
  b.bind(node)
  return {
    update(next) {
      cur = next
      b.bind(node)
    },
    destroy: b.unbind,
  }
}

/** useWeftDevtools is weftDevtools under the name the other helpers use. */
export const useWeftDevtools = weftDevtools
