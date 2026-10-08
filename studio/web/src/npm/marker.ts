// The framework helpers' one body (plan C1, §13.3): set the
// data-weft-scope marker on the host's element, make sure a panel is
// mounted, point it at the scope; undo the marker on detach. The panel
// is imported lazily, when an element is bound — never at module
// evaluation — so a helper imported by a server-rendered component
// (Next, SvelteKit, Nuxt) touches no DOM on the server.
import { serializeScope } from "../lib/scope.js"
import type { Scope } from "../lib/scope.js"
import type { WeftDevtoolsElement } from "./index.js"

/** What every helper takes. endpoint and token configure the panel the
 * helper mounts when the page has none of its own (the bundle's
 * self-mounted dock is replaced); with neither, the helper mounts
 * nothing and scopes the panel the page has. */
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
      // A mount only where the host named where Studio is: without an
      // endpoint or token the page configures the panel itself (meta
      // tags, its markup) — a helper mount there would be a host mount
      // of nothing in particular, its "not reachable" line on a page
      // that asked for none. scope() alone reaches whatever panel the
      // page has, now or mounted later.
      const all = Array.from(document.querySelectorAll<WeftDevtoolsElement>("weft-devtools"))
      const hosts = all.some((n) => !n.autoMounted)
      if (!hosts && (o.endpoint || o.token)) api.mount({ endpoint: o.endpoint, token: o.token })
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

type Binder = ReturnType<typeof binder>

/** One binder per element, whatever closure binds it: a framework that
 * hands a new ref callback every render (React) rebinds the same node
 * with the same options — a no-op — instead of unmarking and
 * rescoping. */
interface Bound {
  b: Binder
  opts: HelperOptions
  releasing: boolean
}
const bound = new WeakMap<Element, Bound>()

/** bindNode binds node through its one binder, with opts. */
export function bindNode(node: Element, opts: HelperOptions): void {
  let e = bound.get(node)
  if (!e) {
    const entry: Bound = { opts, releasing: false, b: binder(() => entry.opts) }
    bound.set(node, entry)
    e = entry
  }
  e.opts = opts
  e.releasing = false
  e.b.bind(node)
}

/** releaseNode unbinds node at the end of the current task, unless it
 * is bound again first: React detaches the previous render's ref and
 * attaches the next one in the same commit. */
export function releaseNode(node: Element): void {
  const e = bound.get(node)
  if (!e) return
  e.releasing = true
  queueMicrotask(() => {
    if (!e.releasing || bound.get(node) !== e) return
    e.b.unbind()
    bound.delete(node)
  })
}
