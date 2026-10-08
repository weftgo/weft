// @weftgo/devtools — the npm delivery of the devtools panel (plan C1).
// The package's panel.js is the same file /studio/panel.js serves,
// byte for byte; importing this module imports it (the side effect:
// <weft-devtools> defined, the dock mounted as the script tag would),
// and adds the programmatic API below. Everything here works through
// the registered element — none of the panel's code is bundled twice,
// so this file stays a few hundred bytes.
//
// The API itself is the element's (plan C4): each export below calls
// the same method on every panel on the page — the one implementation
// window.weft.devtools exposes under the script tag. Importing this
// module adds no global: its exports are the API.
import "./panel.js"
import { parseScope, serializeScope } from "../lib/scope.js"
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
  /** Expands the dock. */
  open: () => void
  /** Collapses the dock to its button. */
  close: () => void
  /** Whether the dock is expanded. */
  readonly isOpen: boolean
  /** The explicit scope (rung 1); null hands it back to the ladder. */
  scope: (s: Scope | string | null) => void
  /** Selects a turn (and the step, its ordinal, ⤢ carries). */
  select: (runId: string, step?: number) => void
  /** Follows one of this panel's events; returns the unsubscribe. */
  on: <TEvent extends keyof DevtoolsEvents>(event: TEvent, cb: (detail: DevtoolsEvents[TEvent]) => void) => () => void
  /** The Studio page of a run (and step); no token, "" before the
   * panel knows its endpoint. */
  studioLink: (runId: string, step?: number) => string
  /** Publishes or removes window.weft.devtools as the configuration says. */
  syncGlobal: () => void
}

/** The events on() can follow, and what each carries (the panel's
 * DevtoolsEvents, element.ts — the same shapes). Only runs of the
 * conversation the panel follows are reported, and only the
 * transitions it sees: a scope's first read of its list is history,
 * except a run reading running there and the run the scope pins. */
export interface DevtoolsEvents {
  /** A run started ("running") or changed status ("succeeded",
   * "failed", "parked", "interrupted"), once per transition; step is
   * the run's last step ordinal the panel knows. */
  run: { runId: string; status: string; publicId?: string; sessionId?: string; step?: number }
  /** One call a run parked on, once per call. ackId is the id its
   * approval names — the call id weft.Approve/Deny/Resolve and POST
   * /api/runs/{runId}/approvals ({call_id}) take: it equals callId. */
  parked: { runId: string; callId: string; ackId: string; name: string }
  /** A run failed: its error, once per run. */
  error: { message: string; runId?: string }
}

const TAG = "weft-devtools"

// The package's exports are the API: no panel on this page adds
// window.weft.devtools (the script-tag install's global).
try {
  const ctor: (CustomElementConstructor & { noGlobal?: boolean }) | undefined = customElements.get(TAG)
  if (ctor && "noGlobal" in ctor) {
    ctor.noGlobal = true
    for (const n of Array.from(document.querySelectorAll<WeftDevtoolsElement>(TAG)))
      if (typeof n.syncGlobal === "function") n.syncGlobal()
  }
} catch {
  // a page without custom elements: no panel, no global
}

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

/** isOpen reads the element's open state (its public getter). */
const openOf = (n: WeftDevtoolsElement) => (n as Partial<WeftDevtoolsElement>).isOpen === true

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
      carried = openOf(n)
      n.remove()
    }
  const node = document.createElement(TAG) as WeftDevtoolsElement
  const o = { ...options }
  // A session-only scope is resolved by the panel (scope() below), not
  // carried as an option.
  const carry = current && o.publicId === undefined && o.scope === undefined ? current : null
  if (carry?.publicId) {
    o.publicId = carry.publicId
    o.scope = { ...carry }
  }
  const startOpen = wantOpen ?? carried
  if (o.open === undefined && startOpen !== null) o.open = startOpen
  node.options = o
  if (carry?.publicId) node.setAttribute("data-weft-scope", serializeScope(carry))
  ;(target ?? document.body).appendChild(node)
  if (carry && !carry.publicId && typeof node.scope === "function") node.scope({ ...carry })
  return node
}

/** scope points every <weft-devtools> on the page at s: the panel
 * follows the whole scope (rung 1, so it wins over data-scope,
 * data-public-id, window.__WEFT__ and any detected scope) — the public
 * id selects the conversation, session narrows its turn list, run pins
 * the selected turn, flow is carried — and each element carries s
 * serialised in its data-weft-scope attribute. A string is the
 * serialised form (parseScope: a bare public id, or
 * "pub_…;session=…;run=…"). A scope with a session and no public id is
 * resolved through Studio (GET /api/sessions/{id}/public_id: setup A or
 * the dev token only — under a panel token the panel says so and keeps
 * its scope). null clears what scope() set: the ladder (URL, markers,
 * headers, the fallback) decides again. The same scope again, already
 * on every element, does nothing (a framework re-render is not a
 * rescope). */
export function scope(s: Scope | string | null): void {
  if (s === null) {
    current = null
    later(() => {
      for (const n of elements()) {
        if (typeof n.scope === "function") n.scope(null)
        else n.removeAttribute("data-weft-scope")
      }
    })
    return
  }
  const next: Scope = typeof s === "string" ? parseScope(s) : { ...s }
  current = next
  const form = serializeScope(next)
  later(() => {
    for (const n of elements()) {
      if (typeof n.scope === "function") {
        n.scope({ ...next })
        continue
      }
      // Not the panel's element (a foreign one under the tag): the
      // marker and the option, as far as they go.
      n.options = { ...n.options, publicId: next.publicId, scope: { ...next } }
      n.setAttribute("data-weft-scope", form)
      rescanOf(n)?.()
    }
  })
}

/** select selects the turn runId (and, given, the step — its ordinal,
 * the n of runs/{id}/steps/{n} — which the panel's ⤢ link then
 * carries) in every panel on the page, once a scope() set just before
 * it has settled. A run the panel's list does not show is read by id
 * and joins the list when it belongs to the conversation followed;
 * otherwise the panel says "run r_… not in this conversation". */
export function select(runId: string, step?: number): void {
  later(() => {
    for (const n of elements()) if (typeof n.select === "function") n.select(runId, step)
  })
}

/** isOpen reports whether a panel on the page is expanded (false with
 * no panel). */
export function isOpen(): boolean {
  return elements().some(openOf)
}

/** studioLink is the Studio page of runId — at step, given its
 * ordinal — as the first panel on the page builds it (lib/links.ts, the
 * link its ⤢ carries): never with a token; "" with no panel, or before
 * it knows its endpoint. */
export function studioLink(runId: string, step?: number): string {
  for (const n of elements()) if (typeof n.studioLink === "function") return n.studioLink(runId, step)
  return ""
}

function setOpen(want: boolean): void {
  wantOpen = want
  later(() => {
    for (const n of elements()) {
      if (openOf(n) === want) continue
      if (typeof n.open === "function" && typeof n.close === "function") {
        if (want) n.open()
        else n.close()
      } else toggleOf(n)?.()
    }
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
 * the page (one mounted later included), and returns the unsubscribe.
 * The panel dispatches them as `weft:<event>` CustomEvents (bubbling,
 * composed) from its element, asynchronously; detail is
 * DevtoolsEvents[event]. A cb that throws is swallowed: the panel's
 * work goes on and nothing reaches the console. */
export function on<TEvent extends keyof DevtoolsEvents>(
  event: TEvent,
  cb: (detail: DevtoolsEvents[TEvent]) => void
): () => void {
  const listener = (e: Event) => {
    try {
      cb((e as CustomEvent<DevtoolsEvents[TEvent]>).detail)
    } catch {
      // the host's listener: not the panel's to report
    }
  }
  document.addEventListener(`weft:${event}`, listener)
  return () => document.removeEventListener(`weft:${event}`, listener)
}
