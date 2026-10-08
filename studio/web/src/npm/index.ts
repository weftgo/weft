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
import { serializeScope } from "../lib/scope.js"
import type { Scope } from "../lib/scope.js"

export { parseScope, serializeScope } from "../lib/scope.js"
export type { Scope } from "../lib/scope.js"

/** Where the dock sits. */
export type Position = "bottom-right" | "bottom-left" | "right-dock"

/** mount's options: configuration rung 1, field by field above the
 * element's attributes, meta tags and the script tag. */
export interface MountOptions {
  /** Studio's base URL (setup A: "/studio/"). */
  endpoint?: string
  /** The full scope to follow (data-scope's form, or a Scope); above
   * publicId. */
  scope?: Scope | string
  /** The conversation to follow; "" is the dev list. */
  publicId?: string
  /** Scope detection: "headers" turns the header rung (Weft-Scope on
   * same-origin fetch responses) on anywhere, "markers" the DOM-marker
   * rung (data-weft-scope), "headers,markers" both; "off" turns
   * detection off. Unset: headers only on loopback with no or a dev
   * token; markers everywhere but a read-scoped panel token's page off
   * loopback. */
  detect?: "headers" | "markers" | "headers,markers" | "off"
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
/** What open()/close() last asked for: a mount made after it starts
 * that way (null: the ladder decides). */
let wantOpen: boolean | null = null

/** isOpen reads the element's open state (the same version's field). */
const isOpen = (n: WeftDevtoolsElement) => (n as unknown as { open?: unknown }).open === true

/** The element's methods, when it is the panel's: a foreign element
 * defined under the tag first, or a panel whose boot failed, gets no
 * call — the host's code never sees the panel's TypeError. */
const rescanOf = (n: WeftDevtoolsElement) => (typeof n.rescan === "function" ? () => n.rescan() : null)
const toggleOf = (n: WeftDevtoolsElement) => (typeof n.toggle === "function" ? () => n.toggle() : null)

/** mount appends a <weft-devtools> configured by opts (rung 1) to
 * opts.target or document.body and returns it. It is the host's
 * mount: where Studio does not answer it shows one "Studio not
 * reachable" line instead of removing itself. One panel per page: the
 * dock the bundle mounted by itself (no markup, data-auto) is replaced
 * — its open state carried over — and markup of the page's own is
 * left alone. Unless opts says otherwise, the new panel starts in the
 * last scope() and open state open()/close() asked for. */
export function mount(opts: MountOptions = {}): WeftDevtoolsElement {
  const { target, ...options } = opts
  let carried: boolean | null = null
  for (const n of elements())
    if (n.autoMounted) {
      carried = isOpen(n)
      n.remove()
    }
  const node = document.createElement(TAG) as WeftDevtoolsElement
  const o = { ...options }
  if (current && o.publicId === undefined && o.scope === undefined) {
    o.publicId = current.publicId
    o.scope = { ...current }
  }
  const startOpen = wantOpen ?? carried
  if (o.open === undefined && startOpen !== null) o.open = startOpen
  node.options = o
  if (current) node.setAttribute("data-weft-scope", serializeScope(current))
  ;(target ?? document.body).appendChild(node)
  return node
}

/** scope points every <weft-devtools> on the page at s: the panel
 * follows the whole scope (rung 1, so it wins over data-scope,
 * data-public-id, window.__WEFT__ and any detected scope) — the public
 * id selects the conversation, session narrows its turn list, run pins
 * the selected turn, flow is carried — and each element carries s
 * serialised in its data-weft-scope attribute. A string is a public
 * id. The same scope again, already on every element, does nothing (a
 * framework re-render is not a rescope). */
export function scope(s: Scope | string): void {
  const next: Scope = typeof s === "string" ? { publicId: s } : { ...s }
  const form = serializeScope(next)
  const same = current !== null && serializeScope(current) === form
  current = next
  later(() => {
    for (const n of elements()) {
      const had = n.options?.scope
      const hadForm = typeof had === "string" ? had : had ? serializeScope(had) : ""
      if (same && n.getAttribute("data-weft-scope") === form && hadForm === form) continue
      // publicId too: a panel bundle older than the scope option reads it.
      n.options = { ...n.options, publicId: next.publicId, scope: { ...next } }
      n.setAttribute("data-weft-scope", form)
      rescanOf(n)?.()
    }
  })
}

function setOpen(want: boolean): void {
  wantOpen = want
  later(() => {
    for (const n of elements()) if (isOpen(n) !== want) toggleOf(n)?.()
  })
}

/** open expands every panel on the page; a panel mounted later starts
 * expanded. */
export function open(): void {
  setOpen(true)
}

/** close collapses every panel on the page to its button; a panel
 * mounted later starts collapsed. */
export function close(): void {
  setOpen(false)
}

/** toggle flips every panel on the page (the panels there now only). */
export function toggle(): void {
  later(() => {
    for (const n of elements()) toggleOf(n)?.()
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
