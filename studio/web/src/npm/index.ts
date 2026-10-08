// @weftgo/devtools — the npm delivery of the devtools panel (plan C1).
// The package's panel.js is the same file /studio/panel.js serves,
// byte for byte; importing this module imports it (the side effect:
// <weft-devtools> defined, the dock mounted as the script tag would),
// and adds the programmatic API below. Everything here works through
// the registered element — none of the panel's code is bundled twice,
// so this file stays a few hundred bytes.
//
// What is complete in C1 and what C4 completes is in npm/README.md's
// API table.
import "./panel.js"
import { serializeScope } from "../lib/scope"
import type { Scope } from "../lib/scope"

export { parseScope, serializeScope } from "../lib/scope"
export type { Scope } from "../lib/scope"

/** Where the dock sits. */
export type Position = "bottom-right" | "bottom-left" | "right-dock"

/** mount's options: configuration rung 1, field by field above the
 * element's attributes, meta tags and the script tag. */
export interface MountOptions {
  /** Studio's base URL (setup A: "/studio/"). */
  endpoint?: string
  /** The conversation to follow; "" is the dev list. */
  publicId?: string
  /** API token (setups B and C): a per-page panel token in pages you ship. */
  token?: string
  position?: Position
  /** Start expanded. */
  open?: boolean
  auto?: boolean
  /** Where the element is appended (default document.body). */
  target?: Element
}

/** The registered element, as far as this module touches it: the
 * public members of panel.js's WeftDevtools, the same version's. */
export interface WeftDevtoolsElement extends HTMLElement {
  /** Rung 1: the options mount passed. */
  options: Omit<MountOptions, "target"> | null
  /** True only on the dock the bundle mounted by itself. */
  autoMounted: boolean
  /** Re-reads the configuration ladder and applies it. */
  rescan: () => void
  /** Flips the dock open or closed. */
  toggle: () => void
}

/** The events on() can follow, and what each carries. */
export interface DevtoolsEvents {
  /** A run of the followed conversation started or finished. */
  run: { runId: string; status: string }
  /** A run parked on calls awaiting approval. */
  parked: { runId: string; pending: number }
  /** The panel could not reach or read Studio. */
  error: { message: string }
}

const TAG = "weft-devtools"

const elements = (): WeftDevtoolsElement[] =>
  Array.from(document.querySelectorAll<WeftDevtoolsElement>(TAG))

/** later runs fn now, or — while the page is still parsing, before the
 * bundle has mounted its dock — once it has (the bundle's own
 * DOMContentLoaded listener was added first, so it runs first). */
function later(fn: () => void): void {
  if (document.readyState === "loading")
    document.addEventListener("DOMContentLoaded", fn, { once: true })
  else fn()
}

/** The scope scope() last set: a mount made after it starts there. */
let current: Scope | null = null

/** mount appends a <weft-devtools> configured by opts (rung 1) to
 * opts.target or document.body and returns it. It is the host's
 * mount: where Studio does not answer it shows one "Studio not
 * reachable" line instead of removing itself. One panel per page: the
 * dock the bundle mounted by itself (no markup, data-auto) is replaced;
 * markup of the page's own is left alone. */
export function mount(opts: MountOptions = {}): WeftDevtoolsElement {
  const { target, ...options } = opts
  for (const n of elements()) if (n.autoMounted) n.remove()
  const node = document.createElement(TAG) as WeftDevtoolsElement
  node.options = current && options.publicId === undefined ? { ...options, publicId: current.publicId } : options
  if (current) node.setAttribute("data-weft-scope", serializeScope(current))
  ;(target ?? document.body).appendChild(node)
  return node
}

/** scope points every <weft-devtools> on the page at s: the panel
 * follows s.publicId (rung 1, so it wins over data-public-id and
 * window.__WEFT__), and each element carries s serialised in its
 * data-weft-scope attribute. In C1 the panel follows the public id
 * only; session, flow and run are carried in the marker for C3.1. A
 * string is a public id. */
export function scope(s: Scope | string): void {
  const next: Scope = typeof s === "string" ? { publicId: s } : { ...s }
  current = next
  later(() => {
    for (const n of elements()) {
      n.options = { ...n.options, publicId: next.publicId }
      n.setAttribute("data-weft-scope", serializeScope(next))
      n.rescan()
    }
  })
}

/** isOpen reads the element's open state (the same version's field). */
const isOpen = (n: WeftDevtoolsElement) => (n as unknown as { open?: unknown }).open === true

function setOpen(want: boolean): void {
  later(() => {
    for (const n of elements()) {
      // Not connected yet: the first connect adopts rung 1's open.
      if (!n.isConnected) n.options = { ...n.options, open: want }
      else if (isOpen(n) !== want) n.toggle()
    }
  })
}

/** open expands every panel on the page. */
export function open(): void {
  setOpen(true)
}

/** close collapses every panel on the page to its button. */
export function close(): void {
  setOpen(false)
}

/** toggle flips every panel on the page. */
export function toggle(): void {
  later(() => {
    for (const n of elements()) n.toggle()
  })
}

/** on calls cb with each event of the kind named, from any panel on
 * the page, and returns the unsubscribe. The panel dispatches them as
 * `weft:<event>` CustomEvents (bubbling, composed) whose detail is
 * DevtoolsEvents[event]. In C1 the panel dispatches none yet: C4
 * wires all three; until then on() registers and unsubscribes, and cb
 * is never called. */
export function on<TEvent extends keyof DevtoolsEvents>(
  event: TEvent,
  cb: (detail: DevtoolsEvents[TEvent]) => void
): () => void {
  const listener = (e: Event) => cb((e as CustomEvent<DevtoolsEvents[TEvent]>).detail)
  document.addEventListener(`weft:${event}`, listener)
  return () => document.removeEventListener(`weft:${event}`, listener)
}
