// The framework helpers' one body (plan C1, §13.3): set the
// data-weft-scope marker on the host's element, make sure a panel is
// mounted, point it at the scope; undo the marker on detach. The panel
// is imported lazily, when an element is bound — never at module
// evaluation — so a helper imported by a server-rendered component
// (Next, SvelteKit, Nuxt) touches no DOM on the server.
import { serializeScope } from "../lib/scope"
import type { Scope } from "../lib/scope"
import type { WeftDevtoolsElement } from "./index"

/** What every helper takes. endpoint and token configure the panel the
 * helper mounts when the page has none of its own (the bundle's
 * self-mounted dock is replaced when either is given). */
export interface HelperOptions {
  /** The scope to follow and mark; a string is a public id. */
  scope: Scope | string
  endpoint?: string
  token?: string
}

const asScope = (s: Scope | string): Scope => (typeof s === "string" ? { publicId: s } : s)

/** attach marks node, mounts and scopes the panel, and returns the undo
 * (which removes the marker if it is still the one set here). */
function attach(node: Element, o: HelperOptions): () => void {
  const s = asScope(o.scope)
  const value = serializeScope(s)
  node.setAttribute("data-weft-scope", value)
  let live = true
  import("./index.js")
    .then((api) => {
      if (!live) return
      const all = Array.from(document.querySelectorAll<WeftDevtoolsElement>("weft-devtools"))
      const hosts = all.some((n) => !n.autoMounted)
      if (!hosts && (all.length === 0 || o.endpoint || o.token)) api.mount({ endpoint: o.endpoint, token: o.token })
      api.scope(s)
    })
    .catch(() => {
      // the panel fails silent (§5.3)
    })
  return () => {
    live = false
    if (node.getAttribute("data-weft-scope") === value) node.removeAttribute("data-weft-scope")
  }
}

/** binder binds one element at a time: binding the same element with
 * the same options again is a no-op, another element or other options
 * undo the previous binding first, null unbinds. */
export function binder(get: () => HelperOptions) {
  let node: Element | null = null
  let key = ""
  let undo: (() => void) | undefined
  const unbind = () => {
    undo?.()
    undo = undefined
    node = null
    key = ""
  }
  const bind = (n: Element | null) => {
    if (!n) return unbind()
    const o = get()
    const k = [serializeScope(asScope(o.scope)), o.endpoint ?? "", o.token ?? ""].join("\u0000")
    if (n === node && k === key) return
    unbind()
    node = n
    key = k
    undo = attach(n, o)
  }
  return { bind, unbind }
}
