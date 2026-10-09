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
//
// Import-safe on a server: the assembled index.js imports the bundle
// between ssr-guard.js and ssr-unguard.js (npm-package.ts rewrites this
// line), which give the panel's element class a placeholder
// HTMLElement to extend where there is none — and every export below
// is a no-op without a DOM (mount returns null, on returns a no-op
// unsubscribe).
import "./panel.js"
import { parseScope, serializeScope } from "../lib/scope.js"
import type { Scope } from "../lib/scope.js"

export { parseScope, serializeScope } from "../lib/scope.js"
export type { Scope } from "../lib/scope.js"

/** Where the dock starts (data-position, D1): the float's corner, or
 * a dock's side. The panel's PanelPlacement, value for value
 * (package.test.ts pins the two unions). */
export type Position = "bottom-right" | "bottom-left" | "right-dock" | "left-dock" | "top-dock" | "bottom-dock"

/** The dock's initial mode (data-mode, D1); the user's stored layout
 * wins once there is one. */
export type Mode = "float" | "dock" | "pill" | "hidden"

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
  /** The theme: "light" or "dark" names it; "auto" (the default) is
   * the user's stored choice, else the page's <html> (data-theme,
   * class="dark"), else prefers-color-scheme, else dark. */
  theme?: "auto" | "light" | "dark"
  /** The initial mode (data-mode). */
  mode?: Mode
  /** A docked, open panel pads <html> on its side (data-push). */
  push?: boolean
  /** The dock's z-index (data-z-index; default --weft-z, else 2147483000). */
  zIndex?: number | string
  /** Where the element is appended (default document.body; with no
   * <body> yet, it is appended to the body on DOMContentLoaded). */
  target?: Element
  /** false: mount nothing and return null (default true). Gate the
   * panel on your build's environment: enabled: import.meta.env.DEV. */
  enabled?: boolean
}

/** The options the element carries as configuration rung 1: mount's,
 * minus where it goes, whether it mounts, and the layout fields mount
 * writes as the element's data-mode / data-push / data-z-index. */
export type PanelOptions = Omit<MountOptions, "target" | "enabled" | "mode" | "push" | "zIndex">

/** The registered element, as far as this module touches it: the
 * public members of panel.js's WeftDevtools, the same version's. */
export interface WeftDevtoolsElement extends HTMLElement {
  /** Rung 1: the options mount passed. */
  options: PanelOptions | null
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
  /** A run of the conversation failed: its error, once per run (the
   * panel's own failures are lines in the panel, not events). */
  error: { message: string; runId: string }
}

const TAG = "weft-devtools"

/** Whether there is a page to put a panel in: false on a server (Node,
 * a worker, an edge runtime), where every export is a no-op. */
const hasDOM = () => typeof document !== "undefined" && typeof HTMLElement !== "undefined"

// The package's exports are the API: no panel on this page adds
// window.weft.devtools (the script-tag install's global).
try {
  if (!hasDOM()) throw new Error("no DOM")
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
  hasDOM() ? Array.from(document.querySelectorAll<WeftDevtoolsElement>(TAG)) : []

/** later runs fn now, or — while the page is still parsing, before the
 * bundle has mounted its dock — once it has (the bundle's own
 * DOMContentLoaded listener was added first, so it runs first). */
function later(fn: () => void): void {
  if (!hasDOM()) return
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

/** HOST marks the element mount() made (Symbol.for: it survives the
 * module being evaluated again — HMR, a second copy of the package). */
const HOST = Symbol.for("weft.devtools.mount")
type Hosted = WeftDevtoolsElement & { [HOST]?: true }

/** The element mount() made and has not appended yet: no <body> when
 * it was called (a classic bundle in <head>). */
let pending: { node: WeftDevtoolsElement; target?: Element } | null = null

/** hostMounted is the element an earlier mount() made, if it is still
 * on the page (or waiting for <body>). */
const hostMounted = (): WeftDevtoolsElement | null =>
  pending?.node ?? elements().find((n) => (n as Hosted)[HOST] === true) ?? null

/** The page's <body> and root element now: null before the parser has
 * made them (the DOM types say never). */
const bodyNow = () => document.body as HTMLElement | null
const rootNow = () => document.documentElement as HTMLElement | null

/** layout writes the layout options as the element's attributes
 * (rung 2: the panel's options rung does not carry them); an option
 * left out removes what an earlier mount() wrote. */
function layout(node: Element, o: Pick<MountOptions, "mode" | "push" | "zIndex">): void {
  const set = (name: string, v: string | undefined) =>
    v === undefined ? node.removeAttribute(name) : node.setAttribute(name, v)
  set("data-mode", o.mode)
  set("data-push", o.push === undefined ? undefined : String(o.push))
  set("data-z-index", o.zIndex === undefined ? undefined : String(o.zIndex))
}

/** mount appends a <weft-devtools> configured by opts (rung 1) to
 * opts.target or document.body and returns it. It is the host's
 * mount: where Studio does not answer it shows one "Studio not
 * reachable" line instead of removing itself. One panel per page: the
 * dock the bundle mounted by itself (no markup, data-auto) is replaced
 * — its open state carried over — and markup of the page's own is
 * left alone. Unless opts says otherwise, the new panel starts in the
 * last scope() and open state open()/close() asked for.
 *
 * mount is idempotent: a second call (a React StrictMode effect, HMR)
 * returns the element the first one made, with the new options
 * applied (and moved to a new target, given one) — never a second
 * panel. Called before <body> exists (a bundle in <head>) and with no
 * target, the element is returned at once and appended on
 * DOMContentLoaded. It never throws into the host: with
 * enabled: false, or on a server (no DOM), it mounts nothing and
 * returns null. */
export function mount(opts: MountOptions = {}): WeftDevtoolsElement | null {
  if (opts.enabled === false || !hasDOM()) return null
  const { target, enabled: _enabled, mode, push, zIndex, ...options } = opts
  const o: PanelOptions = { ...options }
  // A session-only scope is resolved by the panel (scope() below), not
  // carried as an option.
  const carry = current && o.publicId === undefined && o.scope === undefined ? current : null
  if (carry?.publicId) {
    o.publicId = carry.publicId
    o.scope = { ...carry }
  }
  if (o.open === undefined && wantOpen !== null) o.open = wantOpen
  const scopeOnto = (n: WeftDevtoolsElement) => {
    if (carry?.publicId) n.setAttribute("data-weft-scope", serializeScope(carry))
    if (carry && !carry.publicId && typeof n.scope === "function") n.scope({ ...carry })
  }

  const again = hostMounted()
  if (again) {
    again.options = o
    layout(again, { mode, push, zIndex })
    if (pending?.node === again) {
      if (target) pending.target = target
    } else if (target && again.parentNode !== target) target.appendChild(again)
    rescanOf(again)?.()
    if (again.isConnected) scopeOnto(again)
    return again
  }

  const node = document.createElement(TAG) as Hosted
  node[HOST] = true
  node.options = o
  layout(node, { mode, push, zIndex })
  const place = (at: Element) => {
    let carried: boolean | null = null
    for (const n of elements())
      if (n.autoMounted && n !== node) {
        carried = openOf(n)
        n.remove()
      }
    if (node.options && node.options.open === undefined && carried !== null)
      node.options = { ...node.options, open: carried }
    at.appendChild(node)
    scopeOnto(node)
  }
  const at = target ?? bodyNow()
  if (at) place(at)
  else if (document.readyState === "loading") {
    pending = { node }
    document.addEventListener(
      "DOMContentLoaded",
      () => {
        const p = pending
        if (p?.node !== node) return
        pending = null
        try {
          const where = p.target ?? bodyNow() ?? rootNow()
          if (where) place(where)
        } catch {
          // never into the host
        }
      },
      { once: true }
    )
  } else {
    const root = rootNow()
    if (root) place(root)
  }
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
  if (!hasDOM()) return () => {}
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
