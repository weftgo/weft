// <weft-devtools> — the panel custom element (§5): shadow DOM, no
// host-framework dependency (V1, the Dv0 decision), the §5.2
// attributes and defaults. The element owns presentation only;
// state.ts owns the data. Rung 1 is a viewer (§8.1); rung 2's
// experiment drawer and approval controls render only when
// meta.capabilities reports the playground (§8.5 item 3).
import type { RunCompaction, RunRow, ToolCallPart, Transcript, Usage } from "../lib/api"
import { isHoleRef } from "../lib/api"
import { holeWords, mergeHoles, rowHoles, statusHoles, USAGE_AT_FINISH, usageKnown } from "../lib/honesty"
import type { HoleMark } from "../lib/honesty"
import { paramsLine, REQUEST_NOT_RECORDED_LABEL, REQUEST_NOT_STORED, shortHash } from "../lib/requests"
import { fetchSessionPublicId, MAX_REQUEST_PAGES, PanelApiError, REQUEST_PAGE } from "./client"
import { diffLines, diffSummary } from "../lib/diff"
import { callState, runHoles, stepHoles, truncation } from "../lib/events"
import {
  compactionLine,
  compactionsOf,
  isSessionMarker,
  messageLine,
  originalOf,
  replacementNote,
  SESSION_LABEL,
  sessionNote,
} from "../lib/compaction"
import { attemptLine, attemptsHole, factsFromRows, timingLine } from "../lib/attempts"
import type { FoldedRun, FoldedStep, FoldedToolCall } from "../lib/events"
import { duration, relativeTime, tokens } from "../lib/format"
import { discoverEndpoint, headerRungOn, markerRungOn, readConfig, tokenScope, urlScope } from "./config"
import type { MountOptions, PanelConfig } from "./config"
import { installHeaderRung } from "./detect"
import type { HeaderRung } from "./detect"
import { installMarkerRung, markerOf } from "./markers"
import type { Marker, MarkerRung } from "./markers"
import { parseScope, serializeScope } from "../lib/scope"
import type { Scope } from "../lib/scope"
import { el, fmtJSON, waterfall } from "./render"
import { PANEL_CSS } from "./styles"
import {
  DEV_POLL_MS,
  emptyPanelState,
  listKey,
  MAX_EVENT_PAGES,
  PanelModel,
  strippedContent,
  TURNS_LIMIT,
} from "./state"
import type { ChildView, PanelRequests, PanelState, TurnView } from "./state"
import { foldedWords, turnPromptOf } from "./playground"
import type { ExperimentDraft, TurnWords } from "./playground"
import { panelStudioVersion } from "./version"
import { href, playgroundLink, runLink, sessionLink, traceLink } from "../lib/links"
import type { PlaygroundHandoff } from "../lib/links"

/** hasCapability reports whether meta lists the named capability (the
 * panel renders a control only when the server reports it, §8.5). */
export function hasCapability(s: PanelState, name: string): boolean {
  return s.meta?.capabilities.includes(name) ?? false
}

/** statusChip maps a row to the §2 turn-list status word: parked is
 * how the panel shows succeeded with pending calls. */
export function statusChip(r: RunRow): string {
  return r.status === "succeeded" && r.pending > 0 ? "parked" : r.status
}

/** studioLink builds the ⤢ deep link (§2: "open in Studio" with the
 * context carried over; Dv3 adds the step, G1 makes it the ordinal):
 * lib/links.ts's runLink, so the panel lands where Studio's own link
 * to the step lands. The run id travels as one encoded path segment;
 * the panel's token is never part of a link. */
export function studioLink(endpoint: string, runId: string, step?: number): string {
  return href(endpoint, runLink(runId, step === undefined ? {} : { step, view: "story" }))
}

/** The diff is plain LCS over lines (lib/diff.ts): its table is
 * lines × lines, built inside the host page. Past this many cells the
 * panel hands the comparison to Studio instead of freezing the tab. */
export const DIFF_CELLS = 250_000

/** One stylesheet for every panel in the document, adopted by each
 * shadow root: a constructed sheet is exempt from a host page's
 * style-src policy, where an inline <style> would be refused and the
 * dock would land unstyled in the page's own flow. The <style>
 * element is the fallback where constructed sheets do not exist. */
let sheet: CSSStyleSheet | null = null
function attachStyles(root: ShadowRoot): void {
  try {
    if ("adoptedStyleSheets" in root && typeof CSSStyleSheet === "function") {
      if (!sheet) {
        const made = new CSSStyleSheet()
        made.replaceSync(PANEL_CSS)
        sheet = made
      }
      root.adoptedStyleSheets = [sheet]
      return
    }
  } catch {
    // no constructed sheets here (or another document's): fall back
  }
  root.append(el("style", undefined, PANEL_CSS))
}

const quiet = () => {}

/** How long panel-config.json (configuration rung 5) may take before
 * the panel falls through to the script's own directory. */
const PROBE_TIMEOUT_MS = 3_000

/** conversationKey is a scope without its run: the conversation it
 * names (public id, session, flow). */
export function conversationKey(s: Scope): string {
  return serializeScope({ publicId: s.publicId, session: s.session, flow: s.flow })
}

/** How many distinct detected scopes the panel remembers (oldest
 * dropped first): the list C3.3's switcher offers. */
export const DETECTED_LIMIT = 20

/** A scope the header rung saw (C3.2): the newest response path that
 * carried it and when, in first-seen order. */
export interface DetectedScope {
  scope: Scope
  /** Its serialised form — the key the list is distinct by. */
  key: string
  /** The URL path of the newest response that carried it. */
  path: string
  /** Date.now() of that response. */
  at: number
}

/** The rungs installed, as the footer says them. */
type RungWord = "headers" | "markers" | "headers+markers"

/** The detection word the footer shows: the rungs installed
 * ("headers", "markers", "headers+markers"), "off" under
 * data-detect="off", "explicit" when an explicit form (rung 1) names
 * the scope and no rung detects, "none" when nothing does — led by
 * "url" ("url", "url+markers", …) while the scope followed is the page
 * URL's (rung 4, C3.4). */
export type DetectWord = RungWord | "off" | "explicit" | "none" | "url" | `url+${RungWord}`

/** Where a conversation the switcher lists came from. */
export type ScopeSource = "explicit" | "url" | "marker" | "header"

/** The fallback's one-line fixes (plan C3.4): every way to name the
 * scope, one line each, from the README's ladder — shown under "how to
 * scope" when no rung names one. */
export const HOW_TO_SCOPE: readonly string[] = [
  'data-scope="pub_…" on the panel\'s <script> tag (or <weft-devtools>)',
  'scope("pub_…") from @weftgo/devtools',
  'data-weft-scope="pub_…" on the chat\'s element',
  "scope.Header(h, …) on the app's handler (Go, package weft/scope)",
]

/** The fallback's header label: no rung names a scope. */
export const NO_SCOPE_LABEL = "no conversation detected on this page"

/** prefersReducedMotion reports the page's prefers-reduced-motion:
 * reduce (false where matchMedia is missing or throws). */
function prefersReducedMotion(): boolean {
  try {
    return typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches
  } catch {
    return false
  }
}

/** A conversation the panel knows of (plan C3.3's switcher). */
export interface KnownScope {
  scope: Scope
  /** conversationKey(scope): the list is distinct by it. */
  key: string
  source: ScopeSource
}

/** The host events (plan C4) and what each one carries. Each is a
 * CustomEvent named `weft:<event>` dispatched from the element
 * (bubbling, composed): the npm entry's on() hears it on document, a
 * host may listen on the element itself. Only runs of the conversation
 * followed are reported, and only transitions the panel sees: a
 * scope's first read of its list is history (no event), except a run
 * reading running there and the run the scope pins. */
export interface DevtoolsEvents {
  /** A run started (status "running") or changed status — "succeeded",
   * "failed", "parked" (succeeded with calls awaiting approval),
   * "interrupted" — once per transition. step: the run's last step
   * ordinal the panel knows (G1's ordinal). */
  run: { runId: string; status: string; publicId?: string; sessionId?: string; step?: number }
  /** One call a run parked on, once per call. ackId is the id the
   * approval names — the call id weft.Approve/Deny/Resolve and POST
   * /api/runs/{runId}/approvals ({call_id}) take — so it equals
   * callId. */
  parked: { runId: string; callId: string; ackId: string; name: string }
  /** A run failed: its error, once per run. */
  error: { message: string; runId?: string }
}

/** A host event's name. */
export type DevtoolsEvent = keyof DevtoolsEvents

/** The events on() accepts. */
export const DEVTOOLS_EVENTS: readonly DevtoolsEvent[] = ["run", "parked", "error"]

/** The host API (plan C4): the element's own methods, as one object —
 * window.weft.devtools under the script tag, the npm entry's exports
 * over the same element. */
export interface DevtoolsAPI {
  /** Expands the dock. */
  open: () => void
  /** Collapses the dock to its button. */
  close: () => void
  /** Flips the dock. */
  toggle: () => void
  /** Whether the dock is expanded. */
  readonly isOpen: boolean
  /** Sets the explicit scope (rung 1); null hands it back to the ladder. */
  scope: (s: Scope | string | null) => void
  /** Selects a turn, and the step (its ordinal) ⤢ carries. */
  select: (runId: string, step?: number) => void
  /** Follows one host event; returns the unsubscribe. */
  on: <TEvent extends DevtoolsEvent>(event: TEvent, cb: (detail: DevtoolsEvents[TEvent]) => void) => () => void
  /** The Studio page of a run (and step), through lib/links.ts; no token. */
  studioLink: (runId: string, step?: number) => string
}

/** Every API object a panel published: window.weft.devtools is
 * replaced or removed only when it is one of these. */
const API_OBJECTS = new WeakSet<object>()
/** The window.weft namespace a panel created (removed with the last
 * API, when nothing else was put in it). */
let createdNS: Record<string, unknown> | null = null

/** isPlainObject: an object literal's shape (Object.prototype or no
 * prototype) — the only window.weft the panel adds a property to. */
function isPlainObject(v: unknown): v is Record<string, unknown> {
  if (!v || typeof v !== "object") return false
  const proto = Object.getPrototypeOf(v) as unknown
  return proto === Object.prototype || proto === null
}

/** How long a parked run whose calls are not readable yet waits
 * before the next look (PARKED_TRIES looks at most). */
export const PARKED_RECHECK_MS = 1_000
const PARKED_TRIES = 3

/** scopeValue reads a host-given Scope object: its string fields only
 * (null when it has no publicId string). */
function scopeValue(v: unknown): Scope | null {
  if (!v || typeof v !== "object") return null
  const o = v as Record<string, unknown>
  if (typeof o.publicId !== "string") return null
  const out: Scope = { publicId: o.publicId }
  for (const k of ["session", "flow", "run"] as const) if (typeof o[k] === "string" && o[k]) out[k] = o[k]
  return out
}

/** stepOrdinal is a host-given step: a non-negative integer, else none. */
const stepOrdinal = (n: unknown): number | undefined =>
  typeof n === "number" && Number.isInteger(n) && n >= 0 ? n : undefined

/** How the renderers remember which <details> the user opened. */
interface OpenState {
  keys: Set<string>
  scope: string
}

export class WeftDevtools extends HTMLElement {
  static observedAttributes = [
    "data-endpoint",
    "data-scope",
    "data-public-id",
    "data-token",
    "data-detect",
    "data-position",
    "data-open",
    "data-auto",
    "data-global",
  ]

  /** Set by the entry module on the dock it mounts itself: only that
   * node is the panel's to remove (§5.3). An element the page wrote —
   * markup a framework may own — goes inert in place instead, because
   * a node removed behind a framework's back breaks its next unmount. */
  autoMounted = false

  /** Configuration rung 1 (plan C2): what mount(opts) passed; above
   * the element's own attributes, field by field. */
  options: MountOptions | null = null

  private cfg: PanelConfig
  private shadow: ShadowRoot
  private model: PanelModel | null = null
  /** What the running model was started with. */
  private conn: { endpoint: string; token: string; scope: string } | null = null
  /** The header rung's fetch wrapper, while it is installed. */
  private rung: HeaderRung | null = null
  /** The last restore found another patcher over the wrapper. */
  private notRestored = false
  /** The newest scope the header rung saw (null: none yet). */
  private detected: Scope | null = null
  /** The newest scope seen per response path. */
  private byPath = new Map<string, Scope>()
  /** The path whose scope set the conversation followed now. */
  private followedPath = ""
  /** Studio answered the running connection (start's ok): the header
   * rung may be installed. */
  private ready = false
  /** Every distinct scope seen, first-seen order (DETECTED_LIMIT). */
  private seen: DetectedScope[] = []
  /** The DOM-marker rung (C3.3), while it is installed. */
  private markerRung: MarkerRung | null = null
  /** The page's markers, document order. */
  private markers: Marker[] = []
  /** The marker element focus last went into. */
  private lastFocused: Element | null = null
  /** The marked conversation followed (null: none yet). It stays when
   * its marker leaves the page: only focus or the switcher moves it. */
  private marked: Scope | null = null
  /** What the user chose in the switcher (null: nothing). */
  private chosen: Scope | null = null
  /** The next rescope is the user's choice: it pins. */
  private forceNext = false
  /** The explicit scope last applied: a new one resets focus and choice. */
  private explicitForm = ""
  /** A marker has carried the explicit conversation since it was set. */
  private explicitMarked = false
  /** The URL scope last applied (rung 4): a new one resets the choice. */
  private urlForm = ""
  /** The fallback's "how to scope" lines are shown. */
  private howTo = false
  /** The endpoint the running connection talks to: cfg.endpoint, or
   * what panel-config.json named (rung 5). The deep links use it. */
  private base = ""
  /** The endpoint that did not answer, for the not-reachable line of
   * a mount the host made (null: no line). */
  private unreachable: string | null = null
  /** A retry from the not-reachable line is in flight: the line stays
   * (saying "checking…") until the probe resolves. */
  private checking = false
  /** Aborts the panel-config.json request of the start in flight. */
  private probe: AbortController | null = null
  private startSeq = 0
  private scheduled = false
  /** Studio did not answer and the node is not ours to remove. */
  private dormant = false
  private shown: boolean
  /** data-open was adopted (on first connect); toggles own it after. */
  private opened = false
  /** The ? shortcuts overlay (Dv3). */
  private keys = false
  private body!: HTMLElement
  private last: PanelState = emptyPanelState()
  /** A press or an IME composition is in progress inside the dock:
   * redrawing now would take the pressed button from under the
   * pointer (no click follows) or end the composition. The redraw
   * waits for the release. */
  private held = false
  private composing = false
  private dirty = false
  private holdTimer: ReturnType<typeof setTimeout> | null = null
  /** What the user typed into fields the model does not hold (the
   * steer message, a resolve result), by field key. */
  private scratch = new Map<string, string>()
  /** The <details> the user opened, by key (survives redraws). */
  private openKeys = new Set<string>()
  private onKey = (e: KeyboardEvent) => this.keydown(e)
  /** hashchange / popstate (rung 4): passive listeners on window, a
   * read of location — never a patch, never a write to the URL. */
  private onURL = () => {
    const next = urlScope()
    if ((next ? serializeScope(next) : "") !== (this.cfg.urlScope ? serializeScope(this.cfg.urlScope) : "")) this.rescan()
  }
  private onRelease = () => this.release()

  // ── The host API's state (plan C4) ──
  /** The API's honest line (a session lookup refused, a run not in the
   * conversation), drawn atop the turn list; "" for none. */
  private note = ""
  /** The newest start or rescope apply began: the API's select and
   * session lookups run once it has settled. */
  private settling: Promise<unknown> = Promise.resolve()
  /** Bumped by each scope() / select(): an older lookup yields. */
  private scopeSeq = 0
  private selectSeq = 0
  /** The model and conversation the event bookkeeping is about. */
  private evModel: PanelModel | null = null
  private evKey = ""
  /** The next pass is the conversation's first read: its history. */
  private evBaseline = true
  /** The status each run was last reported (or recorded) with. */
  private evStatus = new Map<string, string>()
  /** Parked calls reported (run + call id), runs whose failure was
   * reported, runs whose pending calls are being read. */
  private evParked = new Set<string>()
  private evErrored = new Set<string>()
  private evParking = new Set<string>()
  /** The API object (window.weft.devtools), made once. */
  private apiObj: DevtoolsAPI | null = null
  /** Why window.weft.devtools was not added ("" when it was, or off). */
  private globalNote = ""
  /** Set by the npm entry on the registered class: the package's
   * exports are the API, so no panel adds the global. */
  static noGlobal = false

  constructor() {
    super()
    this.cfg = readConfig(this)
    this.shown = this.cfg.open
    this.shadow = this.attachShadow({ mode: "open" })
    this.body = el("div", "weft-root")
    attachStyles(this.shadow)
    this.shadow.append(this.body)
    this.shadow.addEventListener("pointerdown", () => this.hold())
    this.shadow.addEventListener("compositionstart", () => {
      this.composing = true
    })
    this.shadow.addEventListener("compositionend", () => {
      this.composing = false
      this.flush()
    })
    // The failsafe for a composition whose end is never reported: the
    // field lost focus — whatever the IME did with it, nothing is being
    // composed in the dock any more, and a dock that waits for it would
    // stay frozen.
    this.shadow.addEventListener("focusout", () => {
      if (!this.composing) return
      this.composing = false
      this.flush()
    })
    this.render(this.last)
  }

  connectedCallback() {
    // Attributes set before the upgrade land here, not in the
    // constructor: re-read before connecting, and adopt data-open
    // once (the user's toggles own it afterwards).
    this.cfg = readConfig(this)
    if (!this.opened) {
      this.opened = true
      this.shown = this.cfg.open
    }
    // §5.2's keyboard: Alt+W (and Ctrl+Shift+W where the browser
    // delivers it — Q4) toggles the dock, ? lists the keys, r flips
    // the raw JSON, Esc closes. Keys never fire while the user types
    // in an input — the host page's or the panel's own.
    window.addEventListener("keydown", this.onKey)
    window.addEventListener("pointerup", this.onRelease, true)
    window.addEventListener("pointercancel", this.onRelease, true)
    window.addEventListener("hashchange", this.onURL, { passive: true })
    window.addEventListener("popstate", this.onURL, { passive: true })
    this.syncGlobal()
    this.schedule()
  }

  disconnectedCallback() {
    window.removeEventListener("keydown", this.onKey)
    window.removeEventListener("pointerup", this.onRelease, true)
    window.removeEventListener("pointercancel", this.onRelease, true)
    window.removeEventListener("hashchange", this.onURL)
    window.removeEventListener("popstate", this.onURL)
    if (this.holdTimer) clearTimeout(this.holdTimer)
    this.holdTimer = null
    this.held = this.composing = false
    this.startSeq++ // a start still in flight is no longer this element's
    this.probe?.abort()
    this.probe = null
    this.model?.dispose()
    this.model = null
    this.conn = null
    this.ready = false
    this.dropRung()
    this.dropMarkers()
    this.dropGlobal()
  }

  // ── The host API (plan C4) ──────────────────────────────────────

  /** isOpen reports whether the dock is expanded. */
  get isOpen(): boolean {
    return this.shown
  }

  /** open expands the dock. */
  open(): void {
    if (!this.shown) this.toggle()
  }

  /** close collapses the dock to its button. */
  close(): void {
    if (this.shown) this.toggle()
  }

  /** scope sets the explicit scope (detection rung 1: it wins over the
   * URL, markers and headers, and its run pins): a Scope, or its string
   * form ("pub_…;session=…;run=…", parseScope). null clears what scope()
   * set, so the ladder decides again (a data-scope in the page's markup
   * stays the page's). A scope naming a session and no public id is
   * resolved through GET /api/sessions/{id}/public_id — setup A or the
   * dev token only; under a panel token, or where the lookup finds no
   * public id, the panel says so in one line and keeps its scope. Never
   * throws. */
  scope(s: Scope | string | null): void {
    try {
      const seq = ++this.scopeSeq
      if (s === null) {
        this.note = ""
        const { scope: _s, publicId: _p, ...rest } = this.options ?? {}
        this.options = rest
        this.removeAttribute("data-weft-scope")
        this.rescan()
        this.render(this.last)
        return
      }
      const next = typeof s === "string" ? parseScope(s) : scopeValue(s)
      if (!next) return
      if (!next.publicId && next.session) {
        this.lookupSession(next, seq)
        return
      }
      this.note = ""
      this.applyScope(next)
    } catch {
      // the host's call must go through
    }
  }

  /** applyScope makes s the explicit scope (options rung 1, the
   * data-weft-scope attribute); the same scope again changes nothing
   * (a framework re-render is not a rescope). */
  private applyScope(s: Scope) {
    const form = serializeScope(s)
    const had = this.options?.scope
    const hadForm = typeof had === "string" ? had : had ? serializeScope(had) : ""
    if (hadForm === form && this.getAttribute("data-weft-scope") === form) {
      this.render(this.last)
      return
    }
    // publicId too: the rung-1 field readConfig reads after scope.
    this.options = { ...this.options, publicId: s.publicId, scope: { ...s } }
    this.setAttribute("data-weft-scope", form)
    this.rescan()
    this.render(this.last)
  }

  /** lookupSession resolves a session-only scope to its public id
   * (plan C4.1's route) and applies it; refusals are said, not thrown. */
  private lookupSession(s: Scope, seq: number) {
    const id = s.session ?? ""
    if (tokenScope(this.cfg.token) !== "") {
      this.say(`session ${id}: the session lookup needs the dev token`)
      return
    }
    this.afterSettle(async () => {
      if (seq !== this.scopeSeq || !this.ready || !this.base) return
      let pub = ""
      let badge = ""
      try {
        const doc = (await fetchSessionPublicId({ base: this.base, token: this.cfg.token }, id)) as
          | { public_id?: unknown; badge?: unknown }
          | null
        pub = typeof doc?.public_id === "string" ? doc.public_id : ""
        badge = typeof doc?.badge === "string" ? doc.badge : ""
      } catch (err) {
        if (seq !== this.scopeSeq) return
        const status = err instanceof PanelApiError ? err.status : 0
        this.say(
          status === 403
            ? `session ${id}: the session lookup needs the dev token`
            : `session ${id} has no public id · ${status === 404 ? "unknown session" : "Studio did not answer the lookup"}`
        )
        return
      }
      if (seq !== this.scopeSeq) return
      if (!pub) {
        this.say(`session ${id} has no public id · ${badge === "not_recorded" ? "not recorded (created without thread.PublicID)" : "none recorded"}`)
        return
      }
      this.note = ""
      this.applyScope({ ...s, publicId: pub })
    })
  }

  /** select selects a turn — and, given, the step (its ordinal, G1) ⤢
   * then carries — once the scope set before it has settled. A run the
   * list does not show is read by id and joins the list when it is the
   * conversation's; else the panel says "run r_… not in this
   * conversation". Never throws. */
  select(runId: string, step?: number): void {
    try {
      const id = typeof runId === "string" ? runId : ""
      if (!id) return
      const n = stepOrdinal(step)
      const seq = ++this.selectSeq
      this.afterSettle(async () => {
        const m = this.model
        if (!m || !this.ready || seq !== this.selectSeq) return
        const ok = await m.adoptRun(id)
        if (this.model !== m || seq !== this.selectSeq) return
        if (!ok) {
          this.say(`run ${id} not in this conversation`)
          return
        }
        this.note = ""
        const loading = m.state.selected === id ? null : m.select(id, true)
        if (n !== undefined) m.selectStep(n)
        else if (!loading) this.render(this.last)
        await loading
      })
    } catch {
      // the host's call must go through
    }
  }

  /** on calls cb with each event of the kind named this panel
   * dispatches, and returns the unsubscribe. A throwing cb is the
   * host's: swallowed, never the panel's console. */
  on<TEvent extends DevtoolsEvent>(event: TEvent, cb: (detail: DevtoolsEvents[TEvent]) => void): () => void {
    if (!DEVTOOLS_EVENTS.includes(event) || typeof cb !== "function") return () => {}
    const listener = (e: Event) => {
      try {
        cb((e as CustomEvent<DevtoolsEvents[TEvent]>).detail)
      } catch {
        // the host's listener: not the panel's to report
      }
    }
    this.addEventListener(`weft:${event}`, listener)
    return () => this.removeEventListener(`weft:${event}`, listener)
  }

  /** studioLink is the Studio page of a run — at a step, given its
   * ordinal — through lib/links.ts (G1): the link ⤢ would carry,
   * never with a token. "" before the panel knows its endpoint. */
  studioLink(runId: string, step?: number): string {
    const base = this.base || this.cfg.endpoint
    if (!base || typeof runId !== "string" || !runId) return ""
    try {
      return studioLink(base, runId, stepOrdinal(step))
    } catch {
      return ""
    }
  }

  /** api is the host API over this element, one frozen object. */
  get api(): DevtoolsAPI {
    if (this.apiObj) return this.apiObj
    const self = this
    const api: DevtoolsAPI = Object.freeze({
      open: () => this.open(),
      close: () => this.close(),
      toggle: () => this.toggle(),
      get isOpen() {
        return self.isOpen
      },
      scope: (s: Scope | string | null) => this.scope(s),
      select: (runId: string, step?: number) => this.select(runId, step),
      on: <TEvent extends DevtoolsEvent>(event: TEvent, cb: (detail: DevtoolsEvents[TEvent]) => void) => this.on(event, cb),
      studioLink: (runId: string, step?: number) => this.studioLink(runId, step),
    })
    API_OBJECTS.add(api)
    this.apiObj = api
    return api
  }

  /** syncGlobal publishes window.weft.devtools while this panel is
   * connected (the script-tag install's one global, plan C4): window.weft
   * is created only when absent, never replaced; a window.weft that is
   * not a plain object, or a devtools of the page's own, is left alone
   * and the footer says so. data-global="off" (weft:global), and the
   * npm entry, keep it off. */
  syncGlobal(): void {
    try {
      const want = this.isConnected && this.cfg.global && !WeftDevtools.noGlobal
      if (!want) {
        this.globalNote = ""
        this.dropGlobal()
        return
      }
      const w = window as unknown as { weft?: unknown }
      let ns = w.weft
      if (ns === undefined) {
        createdNS = {}
        ns = createdNS
        w.weft = ns
      } else if (!isPlainObject(ns)) {
        this.globalNote = "window.weft is the page's: no window.weft.devtools"
        return
      }
      const ours = ns as Record<string, unknown>
      const cur = ours.devtools
      const panels = cur !== undefined && !!cur && typeof cur === "object" && API_OBJECTS.has(cur)
      if (cur !== undefined && !panels) {
        this.globalNote = "window.weft.devtools is the page's: not replaced"
        return
      }
      this.globalNote = ""
      // Another connected panel published first: it keeps the name.
      if (panels && cur !== this.apiObj && this.publisherConnected(cur)) return
      ours.devtools = this.api
    } catch {
      // a sealed window: no global, and nothing thrown
    }
  }

  /** publisherConnected: the panel whose API api is is still connected. */
  private publisherConnected(api: object): boolean {
    return Array.from(document.querySelectorAll("weft-devtools")).some(
      (n) => n !== this && (n as Partial<WeftDevtools>).apiObjIs?.(api) === true
    )
  }

  /** apiObjIs reports whether api is this panel's API object. */
  apiObjIs(api: unknown): boolean {
    return this.apiObj !== null && api === this.apiObj && this.isConnected
  }

  /** dropGlobal removes this panel's window.weft.devtools: another
   * connected panel publishes its own instead; with none, a namespace
   * the panel created and left empty goes too. */
  private dropGlobal(): void {
    try {
      const w = window as unknown as { weft?: unknown }
      const ns = w.weft
      if (!isPlainObject(ns) || ns.devtools !== this.apiObj || !this.apiObj) return
      delete ns.devtools
      const other = Array.from(document.querySelectorAll("weft-devtools")).find(
        (n) => n !== this && n.isConnected && typeof (n as Partial<WeftDevtools>).syncGlobal === "function"
      ) as WeftDevtools | undefined
      other?.syncGlobal()
      if (!("devtools" in ns) && ns === createdNS && Object.keys(ns).length === 0) {
        delete w.weft
        createdNS = null
      }
    } catch {
      // nothing thrown into the host
    }
  }

  /** say draws the API's honest line. */
  private say(line: string) {
    this.note = line
    this.render(this.last)
  }

  /** afterSettle runs fn once the apply already scheduled, and the
   * start or rescope it began, have settled — so a select() right after
   * a scope() acts in the new scope's list. */
  private afterSettle(fn: () => Promise<void>) {
    void this.settled()
      .then(fn)
      .catch(quiet)
  }

  private async settled(): Promise<void> {
    for (let i = 0; i < 20; i++) {
      // A scheduled apply runs in the microtask queued before this one.
      if (this.scheduled) await Promise.resolve()
      const p = this.settling
      await p.catch(quiet)
      if (p === this.settling && !this.scheduled) return
    }
  }

  /** watchRuns turns what the panel sees into the host events: the
   * runs of the conversation followed, each status change once. A
   * conversation's first read of its list is history — only a run
   * reading running there, and the run the scope pins, are reported. */
  private watchRuns(s: PanelState) {
    const m = this.model
    if (!m || s !== m.state || !m.publicId || this.dormant) return
    // Until the followed conversation's list is read, what the state
    // holds is the previous one's (or a frame ahead of the read).
    const key = listKey(m.publicId, m.narrowing.session)
    if (s.listKey !== key) return
    if (m !== this.evModel || key !== this.evKey) {
      this.evModel = m
      this.evKey = key
      this.evBaseline = true
      this.evStatus = new Map()
    }
    const baseline = this.evBaseline
    this.evBaseline = false
    const pin = m.narrowing.run ?? ""
    for (const r of [...s.turns, ...[...s.experiments.values()].flat()]) {
      const status = statusChip(r)
      const prev = this.evStatus.get(r.id)
      if (prev === status) continue
      this.evStatus.set(r.id, status)
      if (prev === undefined && baseline && status !== "running" && r.id !== pin) continue
      const folded = s.turn?.id === r.id ? s.turn.folded.steps.at(-1)?.index : undefined
      const step = folded ?? (r.steps > 0 ? r.steps - 1 : undefined)
      this.fire("run", {
        runId: r.id,
        status,
        publicId: r.public_id || m.publicId,
        ...(r.session_id ? { sessionId: r.session_id } : {}),
        ...(step !== undefined ? { step } : {}),
      })
      if (status === "failed" && !this.evErrored.has(r.id)) {
        this.evErrored.add(r.id)
        this.fire("error", { message: r.err || `run ${r.id} failed`, runId: r.id })
      }
      if (status === "parked") this.reportParked(m, r.id, 1)
    }
  }

  /** reportParked fires "parked" once per call the run parked on, with
   * the id its approval takes; calls not readable yet are looked for
   * again (PARKED_TRIES looks), while the run is still in scope. */
  private reportParked(m: PanelModel, runId: string, tries: number) {
    if (this.evParking.has(runId)) return
    this.evParking.add(runId)
    void m
      .pendingCalls(runId)
      .then((calls) => {
        this.evParking.delete(runId)
        const row = m.rowOf(runId)
        if (this.model !== m || !row || statusChip(row) !== "parked") return
        if (!calls.length) {
          if (tries < PARKED_TRIES)
            setTimeout(() => {
              if (this.model === m) this.reportParked(m, runId, tries + 1)
            }, PARKED_RECHECK_MS)
          return
        }
        for (const c of calls) {
          const k = `${runId}\u0000${c.id}`
          if (this.evParked.has(k)) continue
          this.evParked.add(k)
          this.fire("parked", { runId, callId: c.id, ackId: c.id, name: typeof c.name === "string" ? c.name : "" })
        }
      })
      .catch(quiet)
  }

  /** fire dispatches a host event from the element, asynchronously: a
   * host listener runs outside the panel's draw. */
  private fire<TEvent extends DevtoolsEvent>(name: TEvent, detail: DevtoolsEvents[TEvent]) {
    queueMicrotask(() => {
      try {
        if (this.isConnected) this.dispatchEvent(new CustomEvent(`weft:${name}`, { detail, bubbles: true, composed: true }))
      } catch {
        // never the host's error
      }
    })
  }

  // ── Scope detection (plan C3.2, C3.3) ───────────────────────────

  /** detectWord is the resolved detection choice, as the footer says it. */
  detectWord(): DetectWord {
    const rungs: RungWord | "" = this.rung ? (this.markerRung ? "headers+markers" : "headers") : this.markerRung ? "markers" : ""
    if (this.followsURL()) return rungs ? `url+${rungs}` : "url"
    if (rungs) return rungs
    if (this.cfg.detect === "off") return "off"
    return this.cfg.scopeExplicit ? "explicit" : "none"
  }

  /** followsURL: the scope followed is the page URL's (rung 4). */
  private followsURL(): boolean {
    return !!this.cfg.urlScope && this.scopeNow() === this.cfg.urlScope
  }

  /** detectedScopes is every conversation the header rung has seen —
   * distinct by public id + session + flow, first-seen order — each
   * with the newest scope (its newest run) and the newest path that
   * carried it: the list C3.3's switcher offers. */
  detectedScopes(): DetectedScope[] {
    return this.seen.map((d) => ({ ...d, scope: { ...d.scope } }))
  }

  /** markerScopes is the page's data-weft-scope markers the rung read,
   * document order (empty while the rung is off). */
  markerScopes(): Marker[] {
    return this.markers.map((m) => ({ ...m, scope: { ...m.scope } }))
  }

  /** conversations is what the switcher lists: the explicit scope,
   * then the markers in document order, then the header-detected
   * scopes in first-seen order — each conversation once, under the
   * first source that names it, at most DETECTED_LIMIT. */
  conversations(): KnownScope[] {
    const out: KnownScope[] = []
    const add = (scope: Scope, source: ScopeSource) => {
      const key = conversationKey(scope)
      if (scope.publicId && out.length < DETECTED_LIMIT && !out.some((c) => c.key === key))
        out.push({ scope: { ...scope }, key, source })
    }
    if (this.cfg.scopeExplicit) add(this.cfg.scope, "explicit")
    if (this.cfg.urlScope) add(this.cfg.urlScope, "url")
    for (const m of this.markers) add(m.scope, "marker")
    for (const d of this.seen) add(d.scope, "header")
    return out
  }

  /** choose follows a conversation the user picked in the switcher:
   * above every rung until focus moves into a marker or the explicit
   * scope changes, and its run pins (force). */
  private choose(c: KnownScope) {
    this.chosen = c.scope
    this.forceNext = true
    this.rescopeSoon()
  }

  /** rescopeSoon applies a new scope once Studio answered; before that
   * the start in flight reads scopeNow itself (and checks again once
   * it is ready) — a scan never restarts a start. */
  private rescopeSoon() {
    if (this.ready) this.schedule()
  }

  /** markerSig is what a marker change can change on screen: the
   * scope followed and the switcher's entries. */
  private markerSig(): string {
    return [serializeScope(this.scopeNow()), ...this.conversations().map((c) => c.key + c.source)].join("|")
  }

  /** onMarkers takes a settled scan of the page's markers; a scan that
   * changed nothing (unrelated DOM churn) draws nothing. */
  private onMarkers(list: Marker[]) {
    const prev = this.markers
    const form = (m: Marker) => serializeScope(m.scope)
    if (list.length === prev.length && list.every((m, i) => m.element === prev[i].element && form(m) === form(prev[i]))) return
    const before = this.markerSig()
    // A route change: the followed marker and every old one gone, one
    // new one in their place — that is the page's chat now.
    const key = this.marked ? conversationKey(this.marked) : ""
    if (
      key && list.length === 1 && !list.some((m) => conversationKey(m.scope) === key) &&
      !list.some((m) => prev.some((p) => p.element === m.element))
    )
      this.marked = null
    this.markers = list
    this.follow()
    if (this.markerSig() !== before) this.rescopeSoon()
  }

  /** onMarkerFocus: focus went into a marker's element — the user is
   * in that chat now (above an earlier switcher choice). Focus again in
   * the chat already followed changes nothing. */
  private onMarkerFocus(e: Element) {
    if (e === this.lastFocused && !this.chosen) return
    const before = this.markerSig()
    this.lastFocused = e
    this.chosen = null
    this.follow()
    if (this.markerSig() !== before) this.rescopeSoon()
  }

  /** follow picks the marked conversation: the marker holding the
   * focused element, else the one focus was last in, else the one
   * followed now (its newest form), else — nothing followed yet — the
   * explicit scope's marker (a helper's scope()) or, with no explicit
   * scope, the first in document order. A followed marker that left the page stays followed. */
  private follow() {
    const list = this.markers
    const at = (e: Element | null) => (e ? list.find((m) => m.element === e) : undefined)
    const key = this.marked ? conversationKey(this.marked) : ""
    const pick =
      at(markerOf(document.activeElement)) ?? at(this.lastFocused) ?? list.find((m) => key && conversationKey(m.scope) === key)
    // Once a marker carries the explicit conversation (a helper's
    // scope() and marker), the explicit scope is a marker's until it
    // changes — that marker leaving does not hand the panel back to it.
    const exKey = conversationKey(this.cfg.scope)
    if (this.cfg.scopeExplicit && list.some((m) => conversationKey(m.scope) === exKey)) this.explicitMarked = true
    if (pick) this.marked = pick.scope
    else if (!this.marked) {
      // An explicit scope whose marker is not read yet (a helper sets
      // both; the scan is debounced) waits for it: the explicit one
      // wins meanwhile, and the next scan finds its marker.
      const ex = this.cfg.scopeExplicit ? conversationKey(this.cfg.scope) : ""
      this.marked = (ex ? list.find((m) => conversationKey(m.scope) === ex) : list.at(0))?.scope ?? null
    }
  }

  /** dropMarkers removes the marker rung and forgets what it read. */
  private dropMarkers() {
    this.markerRung?.disconnect()
    this.markerRung = null
    this.markers = []
    this.marked = this.lastFocused = null
  }

  /** detectedByPath is the newest scope seen per response path. */
  detectedByPath(): Map<string, Scope> {
    return new Map(this.byPath)
  }

  /** syncDetect installs or removes the header rung as the
   * configuration says (headerRungOn): the host page's fetch is
   * touched only while it is on, and put back the moment it is not. */
  private syncDetect() {
    // Only once Studio has answered: a mount whose Studio never answers
    // (dormant, or the auto dock still probing) never touches fetch.
    const want = this.isConnected && this.ready && !this.dormant && headerRungOn(this.cfg)
    if (want && !this.rung) {
      this.notRestored = false
      this.rung = installHeaderRung({
        onScope: (sc, path) => this.onDetected(sc, path),
        // The panel's own Studio requests are never a scope source.
        ignore: (u) => !!this.base && u.startsWith(this.base),
      })
    } else if (!want && this.rung) this.dropRung()
    // The marker rung reads attributes only: on from the connect (its
    // first scan names the scope the first start asks for).
    const marks = this.isConnected && !this.dormant && markerRungOn(this.cfg)
    if (marks && !this.markerRung)
      this.markerRung = installMarkerRung({ onScopes: (m) => this.onMarkers(m), onFocus: (e) => this.onMarkerFocus(e) })
    else if (!marks && this.markerRung) this.dropMarkers()
    // A new explicit scope is the page's new word: focus history and
    // the user's choice were about the previous one.
    const ex = this.cfg.scopeExplicit ? serializeScope(this.cfg.scope) : ""
    if (ex !== this.explicitForm) {
      this.explicitForm = ex
      this.chosen = this.marked = this.lastFocused = null
      this.explicitMarked = false
      // The page's (or a helper's) new word pins its run, as with the
      // marker rung off.
      if (ex) this.forceNext = true
      this.follow()
    }
    // A new URL scope (rung 4: a dev link, a hashchange) is a hand-off:
    // it drops the user's switcher choice and pins its run.
    const url = this.cfg.urlScope ? serializeScope(this.cfg.urlScope) : ""
    if (url !== this.urlForm) {
      this.urlForm = url
      if (url && !this.cfg.scopeExplicit) {
        this.chosen = null
        this.forceNext = true
      }
    }
  }

  /** dropRung restores the page's fetch and forgets what was seen. */
  private dropRung() {
    if (!this.rung) return
    this.notRestored = !this.rung.restore()
    this.rung = null
    this.detected = null
    this.byPath.clear()
    this.seen = []
    this.followedPath = ""
  }

  /** onDetected takes one response's scope: newest per path, the
   * distinct list, and — unless an explicit form names the scope —
   * what the panel follows. A scope of the conversation followed (the
   * same public id, session and flow) is a narrowing from any path:
   * its run re-pins (state.ts decides whether the selection moves). A
   * scope of another conversation is followed only from the path that
   * set the one followed now (an app's chat endpoint moving to the next
   * conversation); another path's is recorded for the switcher (C3.3)
   * — two widgets polling for different conversations do not thrash. */
  private onDetected(sc: Scope, path: string) {
    const key = conversationKey(sc)
    this.byPath.set(path, sc)
    const at = Date.now()
    const known = this.seen.find((d) => d.key === key)
    if (known) {
      known.scope = sc
      known.path = path
      known.at = at
    } else {
      this.seen.push({ scope: sc, key, path, at })
      if (this.seen.length > DETECTED_LIMIT) this.seen.shift()
    }
    const cur = this.detected
    if (cur && conversationKey(cur) !== key && this.followedPath && path !== this.followedPath) return
    if (!cur || conversationKey(cur) !== key) this.followedPath = path
    const before = cur ? serializeScope(cur) : ""
    this.detected = sc
    if (serializeScope(sc) !== before && !this.cfg.scopeExplicit) this.schedule()
  }

  /** scopeNow is the scope the panel follows: the user's switcher
   * choice; the explicit forms', unless a marker on the page carries
   * the same conversation (the helpers' scope() and marker: then the
   * marked one, so focus moves between helpers' chats); with no
   * explicit form, the page URL's (rung 4, as written); the marked
   * conversation (the header's newest scope of it, when the header
   * rung saw it: its run narrows); the explicit one; the newest
   * detected one while the header rung is on; else none (the dev list). */
  private scopeNow(): Scope {
    if (this.chosen) return this.chosen
    const cfg = this.cfg
    const marked = this.markerRung ? this.marked : null
    if (cfg.scopeExplicit && !(marked && this.explicitMarked)) return cfg.scope
    // Rung 4: below every explicit form, above the detected ones.
    if (cfg.urlScope && !cfg.scopeExplicit) return cfg.urlScope
    // The marked conversation is the explicit one: the explicit form
    // carries its run (a helper's scope() names it before the marker
    // scan reads it).
    if (marked && cfg.scopeExplicit && conversationKey(marked) === conversationKey(cfg.scope)) return cfg.scope
    const d = this.rung ? this.detected : null
    if (marked) return d && conversationKey(d) === conversationKey(marked) ? d : marked
    if (cfg.scopeExplicit) return cfg.scope
    return d ?? cfg.scope
  }

  /** keydown is the whole keyboard surface. The window sees a key
   * pressed inside a shadow tree as coming from its host, so the real
   * target is read off the composed path — typing r or ? into the
   * drawer's own fields is typing, not a shortcut. Bare keys only
   * (Ctrl+R stays the browser's reload), never during an IME
   * composition, never a key the page already handled, and only when
   * focus is in the panel or nowhere: a key aimed at one of the host
   * page's own widgets is the page's. preventDefault only on a key
   * that was handled. */
  private keydown(e: KeyboardEvent): void {
    if (e.defaultPrevented || e.isComposing) return
    const path = typeof e.composedPath === "function" ? e.composedPath() : []
    const t = (path[0] ?? e.target) as HTMLElement | null
    if (
      t &&
      (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" ||
        t.isContentEditable)
    )
      return
    const toggleCombo =
      (e.altKey && !e.ctrlKey && !e.shiftKey && !e.metaKey && e.code === "KeyW") || // Q4's pick
      (e.ctrlKey && e.shiftKey && !e.altKey && !e.metaKey && e.code === "KeyW") // where delivered
    if (toggleCombo) {
      e.preventDefault()
      this.keys = false
      this.toggle()
      return
    }
    if (e.ctrlKey || e.metaKey || e.altKey) return
    if (!this.shown) return
    // Nothing focused: the key came from the window, the document or
    // the page's body, not from an element of the page's own. Esc
    // included — the page's own dialog closes on it, not the dock.
    const idle = !t || t.nodeType !== 1 || t.tagName === "BODY" || t.tagName === "HTML"
    if (!idle && !path.includes(this)) return
    if (e.key === "Escape") {
      if (this.keys) {
        this.keys = false
        this.render(this.last)
      } else this.toggle()
      return
    }
    if (e.key === "?") {
      e.preventDefault()
      this.keys = !this.keys
      this.render(this.last)
      return
    }
    if ((e.key === "r" || e.key === "R") && this.model) {
      e.preventDefault()
      this.toggleRaw()
    }
  }

  /** quiet reports that the model has no draw scheduled (a draw held
   * back by a press or a composition waits for the user, not time). */
  quiet(): boolean {
    return !(this.model?.drawPending() ?? false)
  }

  /** rescan re-reads the configuration from outside the element's own
   * attributes — window.__WEFT__ changed (main.ts's setter) — and
   * applies it like an attribute change: an explicit data-public-id
   * still wins (readConfig). */
  rescan() {
    this.cfg = readConfig(this)
    if (this.isConnected) {
      this.syncGlobal()
      this.syncDetect()
      this.schedule()
    }
  }

  attributeChangedCallback(_name: string, old: string | null, value: string | null) {
    if (old === value) return
    this.cfg = readConfig(this)
    if (!this.isConnected) return
    this.syncGlobal()
    this.syncDetect()
    this.schedule()
  }

  /** schedule coalesces connect and attribute changes into one apply:
   * an upgraded element hears every attribute, then the connect, in
   * one turn — that is one start, one meta request. */
  private schedule() {
    if (this.scheduled) return
    this.scheduled = true
    queueMicrotask(() => {
      this.scheduled = false
      if (this.isConnected) this.apply()
    })
  }

  /** apply brings the model in line with the attributes: a new
   * endpoint or token is a new connection; a new public id rescopes
   * the one there is (the §5.2 setter path); position only redraws. */
  private apply() {
    const cfg = this.cfg
    const conn = this.conn
    this.syncDetect()
    if (this.model && conn && conn.endpoint === cfg.endpoint && conn.token === cfg.token) {
      const next = this.scopeNow()
      const form = serializeScope(next)
      if (conn.scope !== form) {
        // What was typed for the previous conversation (an unsent steer,
        // a resolve result) is not offered under the next one.
        if (this.model.publicId !== next.publicId) this.scratch.clear()
        conn.scope = form
        // An explicit scope's run (or the user's switcher choice) pins
        // whatever the user clicked; a detected one (the next turn's
        // header, a marker focus follows) respects the click.
        const force = this.forceNext || (cfg.scopeExplicit && next === cfg.scope && !this.explicitMarked)
        this.settling = this.model.rescope(next, { force }).catch(quiet)
      }
      this.forceNext = false
      this.render(this.last)
      return
    }
    this.settling = this.start().catch(quiet)
  }

  /** start (re)connects: one meta request (after panel-config.json
   * when no rung named the endpoint, C2's rung 5). Where Studio does
   * not answer, the dock the entry mounted itself removes itself
   * silently — no console, no retries (§5.3); a mount the host made
   * (its markup, mount(opts), data-auto=false) shows one quiet line
   * instead: "Studio not reachable at … · retry". Only the newest
   * start decides: one that was superseded (the attributes changed
   * while a request was in flight) must not take the panel down. */
  private async start(retrying = false) {
    const seq = ++this.startSeq
    const cfg = this.cfg
    this.ready = false
    this.dropRung()
    this.model?.dispose()
    this.model = null
    this.conn = null
    this.probe?.abort()
    this.probe = null
    this.scratch.clear()
    // A retry keeps the not-reachable line up while it probes: no fab
    // or empty dock flashing in between.
    const keepLine = retrying && this.dormant && this.unreachable !== null
    if (keepLine) {
      this.checking = true
      this.render(emptyPanelState())
    } else {
      this.dormant = false
      this.unreachable = null
      this.checking = false
      // The previous connection's dock is not this one's.
      this.render(emptyPanelState())
    }
    let ok = false
    let endpoint = cfg.endpoint
    if (cfg.configURL) {
      // Rung 5: the endpoint the Studio beside the script names, or
      // the script's directory (rung 6) when it names none. Bounded:
      // a proxy that holds the request does not hold the panel, and a
      // newer start or a disconnect aborts it.
      const probe = new AbortController()
      this.probe = probe
      const timer = setTimeout(() => probe.abort(), PROBE_TIMEOUT_MS)
      try {
        endpoint = (await discoverEndpoint(cfg.configURL, probe.signal)) || cfg.endpoint
      } finally {
        clearTimeout(timer)
        if (this.probe === probe) this.probe = null
      }
      if (seq !== this.startSeq) return
    }
    if (endpoint) {
      const scope = this.scopeNow()
      this.forceNext = false
      const model = new PanelModel(
        { base: endpoint, token: cfg.token },
        scope,
        (s) => this.render(s)
      )
      this.model = model
      this.conn = { endpoint: cfg.endpoint, token: cfg.token, scope: serializeScope(scope) }
      this.base = endpoint
      this.last = model.state
      try {
        ok = await model.start()
      } catch {
        ok = false
      }
      if (seq !== this.startSeq || this.model !== model) return
    }
    this.checking = false
    if (ok) {
      this.ready = true
      if (this.dormant) {
        // A retry that found Studio: the panel replaces the line.
        this.dormant = false
        this.unreachable = null
      }
      // Studio answered: the header rung may go in now (and the footer
      // says so); a marker that changed while Studio was asked is
      // followed now.
      this.syncDetect()
      if (serializeScope(this.scopeNow()) !== this.conn?.scope) this.schedule()
      this.render(this.last)
      return
    }
    // No Studio answered: the page's fetch is not the panel's to touch.
    this.dropRung()
    this.dropMarkers()
    this.model?.dispose()
    this.model = null
    this.conn = null
    // The panel's own dock: this page does not want a panel (V4).
    if (this.autoMounted) this.remove()
    else {
      // The host wrote the mount: it wants to see why nothing is there.
      this.dormant = true
      this.unreachable = endpoint
      this.render(this.last)
    }
  }

  /** retry is the not-reachable line's control: probe again. */
  private retry() {
    this.cfg = readConfig(this)
    this.settling = this.start(true).catch(quiet)
  }

  toggle() {
    this.shown = !this.shown
    this.render(this.last)
  }

  toggleRaw() {
    this.model?.toggleRaw()
  }

  // ── Rendering ───────────────────────────────────────────────────

  private hold() {
    this.held = true
    // A release the window never reports (the pointer left the
    // document) must not freeze the dock.
    if (this.holdTimer) clearTimeout(this.holdTimer)
    this.holdTimer = setTimeout(() => this.unhold(), 1_500)
  }

  /** release ends a press — after the click it produces has been
   * delivered to the button that was pressed. */
  private release() {
    if (!this.held) return
    if (this.holdTimer) clearTimeout(this.holdTimer)
    this.holdTimer = setTimeout(() => this.unhold(), 0)
  }

  private unhold() {
    this.holdTimer = null
    this.held = false
    this.flush()
  }

  private flush() {
    if (this.dirty && !this.held && !this.composing) this.render(this.last)
  }

  /** render never throws into the host page: the model calls it from
   * an animation frame, over records that were stored as ingested. A
   * state that cannot be drawn leaves the previous frame standing. */
  private render(s: PanelState) {
    this.last = s
    try {
      this.watchRuns(s)
    } catch {
      // observability never changes behaviour
    }
    // Whether anyone is looking: the dev list is polled only then.
    this.model?.watch(this.shown && !this.dormant)
    if (this.held || this.composing) {
      this.dirty = true
      return
    }
    this.dirty = false
    try {
      this.draw(s)
    } catch {
      // fail silent (§5.3)
    }
  }

  private draw(s: PanelState) {
    const root = this.body
    // Build first, swap second: a throw while building leaves the dock
    // as it was.
    const next: Node[] = []
    if (this.dormant) {
      // Studio did not answer a mount the host made: one quiet line.
      if (this.unreachable !== null) {
        const line = el("div", "weft-unreachable", undefined, { role: "status" })
        line.append(
          this.unreachable ? "Studio not reachable at " : "Studio not reachable: no http(s) endpoint",
          ...(this.unreachable ? [el("span", "weft-unreachable-at", this.unreachable)] : []),
          " · "
        )
        if (this.checking) line.append(el("span", "weft-checking", "checking…"))
        else {
          const again = el("button", "weft-retry", "retry", { type: "button", title: "ask Studio again" })
          again.addEventListener("click", () => this.retry())
          line.appendChild(again)
        }
        next.push(line)
      }
    } else if (!this.shown) {
      next.push(this.pill(s))
    } else if (!s.gone) {
      const dock = el("div", `weft-dock weft-${this.cfg.position} weft-open`)
      dock.appendChild(this.header(s))
      if (this.howTo && !this.model?.publicId) dock.appendChild(this.howToBox())
      const cols = el("div", "weft-cols")
      cols.appendChild(this.turnList(s))
      cols.appendChild(this.main(s))
      dock.appendChild(cols)
      dock.appendChild(this.footer(s))
      // The overlays live inside the dock: it is their containing
      // block. Outside it they would be laid over the host page.
      if (s.raw && s.turn) dock.appendChild(this.rawView(s.turn))
      if (this.keys) dock.appendChild(this.shortcuts())
      next.push(dock)
    }
    // The whole dock re-renders per state change; the panes' scroll
    // positions, the field the user is typing in and its caret are the
    // user's, not the data's — keep them.
    const scrollOf = (sel: string): number =>
      (root.querySelector(sel))?.scrollTop ?? 0
    const scrolls = [scrollOf(".weft-turns"), scrollOf(".weft-main")]
    const active = this.shadow.activeElement as (HTMLElement & Partial<HTMLInputElement>) | null
    const focusKey = active?.getAttribute("data-weft-k") ?? null
    let caret: [number, number] | null = null
    try {
      if (focusKey && typeof active?.selectionStart === "number")
        caret = [active.selectionStart, active.selectionEnd ?? active.selectionStart]
    } catch {
      // not a text field
    }
    while (root.firstChild) root.removeChild(root.firstChild)
    for (const n of next) root.appendChild(n)
    root.querySelectorAll(".weft-turns, .weft-main").forEach((n, i) => {
      if (scrolls[i]) (n as HTMLElement).scrollTop = scrolls[i]
    })
    if (focusKey) {
      for (const n of Array.from(root.querySelectorAll("[data-weft-k]"))) {
        if (n.getAttribute("data-weft-k") !== focusKey) continue
        const field = n as HTMLInputElement
        try {
          field.focus({ preventScroll: true })
          if (caret) field.setSelectionRange(caret[0], caret[1])
        } catch {
          // a field that takes no selection
        }
        break
      }
    }
  }

  /** pill is the collapsed dock (the fab). The activity signal (plan
   * C3.4): while the stream the panel holds anyway (its scope's, or the
   * fallback's agent stream) has a run reading running, it pulses (not
   * under prefers-reduced-motion) and shows the run's live step count —
   * its open tail's steps when it is the turn followed, else its row's
   * — "● 3"; nothing running, it is the plain pill. No stream of its own. */
  private pill(s: PanelState): HTMLElement {
    const running = s.live ? s.turns.find((r) => r.status === "running") : undefined
    if (!running) {
      const fab = el("button", `weft-fab weft-fab-${this.cfg.position}`, "devtools", {
        title: "weft devtools — Alt+W",
      })
      fab.addEventListener("click", () => this.toggle())
      return fab
    }
    const folded = s.turn?.id === running.id ? s.turn.folded.steps.length : 0
    const step = Math.max(folded, running.steps)
    const words = `weft devtools · running${step ? `, step ${step}` : ""}`
    const fab = el(
      "button",
      `weft-fab weft-fab-${this.cfg.position} weft-fab-running${prefersReducedMotion() ? "" : " weft-fab-pulse"}`,
      [document.createTextNode("devtools"), el("span", "weft-fab-count", step ? ` ● ${step}` : " ●")],
      { title: `${words} — Alt+W`, "aria-label": words }
    )
    fab.addEventListener("click", () => this.toggle())
    return fab
  }

  /** howToBox is the fallback's expander: the one-line fixes. */
  private howToBox(): HTMLElement {
    const box = el("div", "weft-howto")
    for (const line of HOW_TO_SCOPE) box.appendChild(el("code", undefined, line))
    return box
  }

  /** go runs one of the model's async verbs; a rejection stays here
   * (the host page's unhandledrejection is not the panel's log). */
  private go(p: Promise<unknown> | undefined) {
    p?.catch(quiet)
  }

  /** shortcuts is §5.2's ? overlay: the keys, and the collision Q4
   * records (Alt+W is the primary because Chrome eats Ctrl+Shift+W
   * as close-window before any page can see it). */
  private shortcuts(): HTMLElement {
    const box = el("div", "weft-keys")
    const dl = el("dl")
    for (const [k, v] of [
      ["Alt+W", "toggle the dock (Ctrl+Shift+W too, where the browser delivers it)"],
      ["r", "raw JSON of the open turn"],
      ["Esc", "close"],
      ["?", "this list"],
    ] as const) {
      dl.appendChild(el("dt", undefined, k))
      dl.appendChild(el("dd", undefined, v))
    }
    box.appendChild(dl)
    return box
  }

  private header(s: PanelState): HTMLElement {
    const h = el("div", "weft-head")
    const running = s.turns.some((r) => r.status === "running")
    h.appendChild(
      el("span", `weft-dot${s.live ? (running ? " weft-run" : " weft-on") : ""}`, undefined, {
        title: s.live ? "live" : "history",
      })
    )
    const agent = s.session?.agent ?? s.turns.at(0)?.agent ?? ""
    const scope = this.model?.publicId || s.session?.public_id || ""
    const title = scope ? `${agent ? agent + " · " : ""}${scope}` : NO_SCOPE_LABEL
    h.appendChild(el("span", "weft-title", title, { title: scope ? title : `${title}: the newest runs are shown` }))
    if (!scope) {
      // The fallback (C3.4): never an unexplained list — the fixes are
      // one click away.
      const how = el("button", `weft-btn weft-howto-btn${this.howTo ? " weft-active" : ""}`, "how to scope", {
        type: "button",
        "aria-expanded": String(this.howTo),
        title: "the one-line ways to scope the panel to your conversation",
      })
      how.addEventListener("click", () => {
        this.howTo = !this.howTo
        this.render(this.last)
      })
      h.append(" · ", how)
    }
    const sw = this.switcher(s)
    if (sw) h.appendChild(sw)
    // The scope's narrowing, as chips: the session filters the list,
    // the flow is carried and filters nothing yet (C3.2).
    const narrowing = this.model?.narrowing ?? {}
    if (narrowing.session)
      h.appendChild(el("span", "weft-chip weft-scope-chip", `session ${narrowing.session}`, { title: "the turn list is narrowed to this session" }))
    if (narrowing.flow)
      h.appendChild(el("span", "weft-chip weft-scope-chip", `flow ${narrowing.flow}`, { title: "the scope's flow — carried, filters nothing yet" }))
    h.appendChild(el("span", "weft-grow"))
    const inTok = s.turns.reduce((n, r) => n + r.usage.input_tokens, 0)
    const outTok = s.turns.reduce((n, r) => n + r.usage.output_tokens, 0)
    const stats = `${s.turns.length}${s.turnsCapped ? "+" : ""} turns · ${tokens(inTok)}→${tokens(outTok)} tok`
    h.appendChild(el("span", undefined, stats, { title: stats }))
    if (s.turns.length && s.selected) {
      const a = el("a", "weft-btn", "⤢", {
        href: studioLink(this.base, s.selected, linkedStep(s)),
        target: "_blank",
        rel: "noopener",
        title: "open in Studio (run, and the step you are reading)",
      })
      a.style.textDecoration = "none"
      h.appendChild(a)
    }
    const raw = el("button", `weft-btn${s.raw ? " weft-active" : ""}`, "raw", {
      title: "the JSON, one keypress away (r)",
    })
    raw.addEventListener("click", () => this.toggleRaw())
    h.appendChild(raw)
    const close = el("button", "weft-btn", "–", { title: "collapse (Alt+W)" })
    close.addEventListener("click", () => this.toggle())
    h.appendChild(close)
    return h
  }

  /** switcher is the header's conversation switcher (C3.3): shown only
   * when more than one conversation is known, each listed by its public
   * id (session, flow when set) and its source, the live dot (●) on the
   * one followed. A followed conversation no longer known (its marker
   * left the page) is said, not listed as a choice. */
  private switcher(s: PanelState): HTMLElement | null {
    const list = this.conversations()
    const cur = conversationKey(this.scopeNow())
    const listed = list.some((c) => c.key === cur)
    if (list.length < (listed ? 2 : 1)) return null
    const sel = el("select", "weft-switch", undefined, { "aria-label": "conversation", "data-weft-k": "switch" }) as HTMLSelectElement
    const opt = (label: string, value: string, on: boolean) => {
      const o = el("option", undefined, label, { value }) as HTMLOptionElement
      o.selected = on
      sel.appendChild(o)
      return o
    }
    const dot = s.live ? "● " : "○ "
    if (!listed) opt(`${dot}${cur || "latest (dev)"} · not on the page`, "", true).disabled = true
    list.forEach((c, i) => {
      const { publicId, session, flow } = c.scope
      const label = [publicId, session && `session ${session}`, flow && `flow ${flow}`, c.source].filter(Boolean).join(" · ")
      opt(c.key === cur ? dot + label : label, String(i), c.key === cur).setAttribute("data-weft-source", c.source)
    })
    sel.addEventListener("change", () => {
      const c = sel.value ? list.at(Number(sel.value)) : undefined
      if (c && c.key !== cur) this.choose(c)
    })
    return sel
  }

  private turnList(s: PanelState): HTMLElement {
    const list = el("div", "weft-turns")
    // The host API's honest line (a lookup refused, a run not here).
    if (this.note) list.appendChild(el("div", "weft-note weft-api-note", this.note, { role: "status" }))
    // The scope's narrowing, said where it applies: never an empty or
    // unpinned list without a reason.
    if (s.pinMissing)
      list.appendChild(el("div", "weft-note weft-pin-missing", `run ${s.pinMissing} not in this conversation`))
    const session = this.model?.narrowing.session
    if (session && s.sessionUnrecorded)
      list.appendChild(el("div", "weft-note", `session ${session}: these runs carry no session id — not narrowed`))
    // The fallback's stream, said: refused (it polls), or one agent of
    // several followed live.
    if (!this.model?.publicId && s.devRefused)
      list.appendChild(el("div", "weft-note weft-dev-poll", "streaming needs the server token · polling"))
    else if (s.devAgent && s.turns.some((r) => r.agent !== s.devAgent))
      list.appendChild(el("div", "weft-note weft-dev-poll", `live: agent ${s.devAgent} · the other agents' runs every ${DEV_POLL_MS / 1000} s`))
    if (!s.turns.length && !s.experiments.size) {
      const empty = session
        ? `no turns of session ${session} yet`
        : this.model?.publicId
          ? "no turns yet — run your app"
          : "no runs yet (dev)"
      list.appendChild(el("div", "weft-splash", empty))
      return list
    }
    const shown = new Set<string>()
    for (const r of s.turns) {
      shown.add(r.id)
      list.appendChild(this.turnRow(r, s.selected))
      // The experiment slot (§2): runs that forked from this turn nest
      // under it; empty without the playground capability.
      const expts = s.experiments.get(r.id) ?? []
      if (expts.length) {
        const box = el("div", "weft-expts")
        for (const x of expts) box.appendChild(this.turnRow(x, s.selected))
        list.appendChild(box)
      }
    }
    // Experiments whose source turn is not in the list (older than the
    // page, or a run of another experiment) are still runs of this
    // conversation: listed, not dropped.
    const orphans: RunRow[] = []
    for (const [key, rows] of s.experiments) if (!shown.has(key)) orphans.push(...rows)
    if (orphans.length) {
      const box = el("div", "weft-expts")
      for (const x of orphans) box.appendChild(this.turnRow(x, s.selected))
      list.appendChild(box)
    }
    if (s.turnsCapped) {
      list.appendChild(
        el("div", "weft-note", `the newest ${TURNS_LIMIT} runs — older ones are in Studio (⤢)`)
      )
    }
    return list
  }

  private turnRow(r: RunRow, selected: string): HTMLElement {
    const chip = statusChip(r)
    const row = el("button", `weft-turn${r.id === selected ? " weft-sel" : ""}`)
    const row1 = el("div", "weft-row1", [
      el("span", `weft-chip weft-${chip}`, chip),
      el("span", "weft-id", r.id, { title: r.id }),
      el("span", "weft-when", relativeTime(r.last_seen || r.started)),
    ])
    const usage = r.usage
    const row2 = el("div", "weft-row2", [
      el("span", undefined, r.model.name ? `${r.model.provider}/${r.model.name}` : ""),
      el("span", undefined, `${r.steps} steps`),
      el("span", undefined, `${tokens(usage.input_tokens)}→${tokens(usage.output_tokens)}`),
      el("span", undefined, duration(r.started, r.finished) || "…"),
    ])
    row.append(row1, row2)
    if (r.err) row.appendChild(el("div", "weft-reason", r.err))
    row.addEventListener("click", () => this.go(this.model?.select(r.id, true)))
    return row
  }

  private main(s: PanelState): HTMLElement {
    const main = el("div", "weft-main")
    // One delegated listener for every <details> the turn view draws.
    // toggle does not bubble, so it is heard on the way down
    // (capture). The open state lives outside the DOM — the model's
    // for the lazy subagent loader (Dv3: [data-weft-child]), openKeys
    // for the rest — so a re-render never collapses what the user
    // opened.
    main.addEventListener(
      "toggle",
      (e) => {
        if (!(e.target instanceof HTMLElement)) return
        const t = e.target as HTMLDetailsElement
        const isOpen = t.open
        const key = t.getAttribute("data-weft-open")
        if (key && this.openKeys.has(key) !== isOpen) {
          if (isOpen) this.openKeys.add(key)
          else this.openKeys.delete(key)
          this.render(this.last)
        }
        const child = t.getAttribute("data-weft-child")
        if (!child) return
        const turn = this.model?.state.turn
        if (!turn) return
        if (isOpen) {
          turn.expanded.add(child)
          this.go(this.model?.expandChild(child))
        } else {
          turn.expanded.delete(child)
          turn.tried.delete(child) // reopening asks again
        }
      },
      true
    )
    // A step card click marks the step the user is reading — what ⤢
    // carries into Studio (Dv3).
    main.addEventListener("click", (e) => {
      if (!(e.target instanceof Element)) return
      const target = e.target.closest("[data-weft-step]")
      if (!target) return
      const n = Number(target.getAttribute("data-weft-step"))
      if (Number.isFinite(n)) this.model?.selectStep(n)
    })
    if (s.tooNew && s.meta) {
      main.appendChild(
        el("div", "weft-note weft-warn", [
          el("span", "weft-warn", "Studio is newer than this panel; update panel.js"),
          el(
            "span",
            undefined,
            `studio_version ${s.meta.studio_version} · panel built for ${panelStudioVersion()}`
          ),
        ])
      )
      return main
    }
    if (!s.turn) {
      main.appendChild(el("div", "weft-splash", "select a turn"))
      return main
    }
    main.appendChild(this.turnView(s))
    main.appendChild(this.playgroundArea(s))
    return main
  }

  /** playgroundArea draws rung 2 (§8.2) under the turn view: the
   * experiment drawer, and — once a command was posted — the result
   * streaming in place with its inline diff and approval controls.
   * Rendered only when the server reports the playground capability,
   * and only under the turn they belong to: a drawer opened on one
   * turn is that turn's (its kept steps, its diff base), whichever
   * turn is being read. */
  private playgroundArea(s: PanelState): HTMLElement {
    const box = el("div")
    const here = s.turn?.id ?? ""
    if (s.result && s.result.sourceRunID === here) box.appendChild(this.experimentResult(s))
    if (!this.canAct(s)) return box
    if (s.drawer && s.drawer.runId === here) box.appendChild(this.drawer(s))
    return box
  }

  /** canAct: the write verbs are drawn when the server reports the
   * playground AND the page's token may use them — a read-scoped
   * panel token (§6: read-only unless minted with "playground": true)
   * is refused on every one, so they are not offered. */
  private canAct(s: PanelState): boolean {
    return hasCapability(s, "playground") && tokenScope(this.cfg.token) !== "read"
  }

  /** field marks an input the redraw must give focus and caret back
   * to. */
  private field<T extends HTMLElement>(n: T, key: string): T {
    n.setAttribute("data-weft-k", key)
    return n
  }

  /** drawer is §3's edit form, pre-filled from the run's registered
   * config: prompt, tools off, model, thinking, input — and the ⚠ on
   * every side-effect tool (ReplayPolicy never or unannotated). Text
   * fields patch the draft without a redraw (the field already shows
   * what was typed). */
  private drawer(s: PanelState): HTMLElement {
    const d = s.drawer
    if (!d) return el("div")
    const rt = s.runtimes.find((r) => r.id === d.runtimeId)
    const agent = rt?.agents.find((a) => a.name === d.agent)
    const card = el("div", "weft-step weft-drawer")
    const head = el("div", "weft-step-h", [
      el("span", undefined, `Experiment · ${d.agent}${d.step > 0 ? ` · continue from step ${d.step}` : ""}`),
      el("span", "weft-grow"),
    ])
    const close = el("button", "weft-btn", "–", { title: "close the drawer" })
    close.addEventListener("click", () => this.model?.closeExperiment())
    head.appendChild(close)
    card.appendChild(head)
    const body = el("div", "weft-step-b")

    // System prompt, with the registered config one reset away.
    const promptRow = el("label", "weft-field", [el("span", undefined, "System prompt")])
    const ta = this.field(el("textarea", "weft-input") as HTMLTextAreaElement, "prompt")
    ta.rows = 3
    ta.value = d.instructions
    ta.addEventListener("input", () => this.model?.setDraft({ instructions: ta.value }, true))
    const reset = el("button", "weft-btn", "↺", { title: "reset to the registered prompt" })
    reset.addEventListener("click", () => {
      this.model?.setDraft({ instructions: d.registeredInstructions })
    })
    promptRow.append(ta, reset)
    body.appendChild(promptRow)

    // Tools: turning off is narrowing; ⚠ marks the side-effect class.
    if (agent?.tools.length) {
      const toolsRow = el("div", "weft-field", [el("span", undefined, "Tools")])
      for (const t of agent.tools) {
        const cb = el("input") as HTMLInputElement
        cb.type = "checkbox"
        cb.checked = d.tools[t.name] ?? true
        cb.addEventListener("change", () =>
          this.model?.setDraft({
            tools: { ...(this.model.state.drawer?.tools ?? d.tools), [t.name]: cb.checked },
          })
        )
        const lab = el("label", "weft-tool", [cb, el("span", undefined, t.name)])
        if (t.side_effects === "never" || !t.side_effects) {
          lab.appendChild(
            el("span", "weft-badge weft-warn-badge", "⚠", {
              title:
                "side-effect tool (ReplayPolicy never): its calls substitute or park — never re-fire silently; only side effects: allow runs it for real, and only if the app opted it in",
            })
          )
        }
        toolsRow.appendChild(lab)
      }
      body.appendChild(toolsRow)
    }

    // Model, thinking, input — the row of small selects.
    const opts = el("div", "weft-fields")
    const modelSel = el("select", "weft-input") as HTMLSelectElement
    const own = s.turn?.doc?.model.name ?? ""
    const ownOpt = el("option", undefined, `model: ${own || "—"}`) as unknown as HTMLOptionElement
    ownOpt.value = ""
    modelSel.appendChild(ownOpt)
    for (const m of agent?.models ?? []) {
      if (m === own) continue
      const o = el("option", undefined, m) as unknown as HTMLOptionElement
      o.value = m
      modelSel.appendChild(o)
    }
    modelSel.value = d.model
    modelSel.addEventListener("change", () => this.model?.setDraft({ model: modelSel.value }))
    opts.appendChild(modelSel)
    const thinkSel = el("select", "weft-input") as HTMLSelectElement
    const defOpt = el("option", undefined, "thinking: default") as unknown as HTMLOptionElement
    defOpt.value = ""
    thinkSel.appendChild(defOpt)
    for (const lvl of ["off", "low", "medium", "high"]) {
      const o = el("option", undefined, lvl) as unknown as HTMLOptionElement
      o.value = lvl
      thinkSel.appendChild(o)
    }
    thinkSel.value = d.thinking
    thinkSel.addEventListener("change", () => this.model?.setDraft({ thinking: thinkSel.value }))
    opts.appendChild(thinkSel)
    body.appendChild(opts)

    // Input replaces the turn's user message (a whole-turn re-run).
    if (d.step === 0) {
      const inputRow = el("label", "weft-field", [el("span", undefined, "Input (replaces the user message)")])
      const inTa = this.field(el("textarea", "weft-input") as HTMLTextAreaElement, "input")
      inTa.rows = 2
      inTa.value = d.input
      inTa.addEventListener("input", () => this.model?.setDraft({ input: inTa.value }, true))
      inputRow.appendChild(inTa)
      body.appendChild(inputRow)
    }

    // The side-effect mode (§6 rule 3): substitute (the default — a
    // recorded side-effect call answers from the record, a miss parks),
    // park (every side-effect call waits), allow (the tools the app
    // opted in with AllowSideEffects run for real — only here; a
    // ReplaySafe tool runs in every mode).
    const seRow = el("div", "weft-fields")
    const seSel = el("select", "weft-input") as HTMLSelectElement
    seSel.title =
      "How side-effect tools behave in the re-run. ReplaySafe tools always run; the others substitute, park, or — under allow, if the app opted them in — run for real."
    const seLabel = el("option", undefined, "side effects: substitute", {
      title: "a side-effect call the source recorded is answered from the record; any other call parks for you",
    }) as unknown as HTMLOptionElement
    seLabel.value = ""
    seSel.appendChild(seLabel)
    const parkOpt = el("option", undefined, "park", {
      title: "every side-effect call parks for you; nothing is answered from the record",
    }) as unknown as HTMLOptionElement
    parkOpt.value = "park"
    seSel.appendChild(parkOpt)
    const allowOpt = el("option", undefined, "allow — runs the tools this app opted in (AllowSideEffects) for real", {
      title: "refused unless every tool left on is opted in or ReplaySafe",
    }) as unknown as HTMLOptionElement
    allowOpt.value = "allow"
    seSel.appendChild(allowOpt)
    seSel.value = d.sideEffects === "substitute" ? "" : d.sideEffects
    seSel.addEventListener("change", () => this.model?.setDraft({ sideEffects: seSel.value as ExperimentDraft["sideEffects"] }))
    seRow.appendChild(seSel)
    // The engine (§5.5): live, or scripted — the source run's recorded
    // turns at zero tokens, refused with an instructions/model
    // override (the prompt trap).
    const engSel = el("select", "weft-input") as HTMLSelectElement
    const engLive = el("option", undefined, "engine: live") as unknown as HTMLOptionElement
    engLive.value = "live"
    engSel.appendChild(engLive)
    const engScripted = el("option", undefined, "scripted (zero tokens)") as unknown as HTMLOptionElement
    engScripted.value = "scripted"
    engSel.appendChild(engScripted)
    engSel.value = d.engine
    engSel.addEventListener("change", () => this.model?.setDraft({ engine: engSel.value as ExperimentDraft["engine"] }))
    seRow.appendChild(engSel)
    // Fork mode (§5.4, review fix 4a — P4 renders in both surfaces):
    // ephemeral (an experiment, never a turn) or fork (a new session
    // with lineage, the input its next turn). Fork needs an input —
    // the runtime refuses the command otherwise.
    const thrSel = el("select", "weft-input") as HTMLSelectElement
    const thrEphemeral = el("option", undefined, "thread: ephemeral") as unknown as HTMLOptionElement
    thrEphemeral.value = "ephemeral"
    thrSel.appendChild(thrEphemeral)
    const thrFork = el("option", undefined, "fork (new session)") as unknown as HTMLOptionElement
    thrFork.value = "fork"
    thrSel.appendChild(thrFork)
    thrSel.value = d.thread
    thrSel.title = "fork continues the conversation in a new session (needs an input)"
    thrSel.addEventListener("change", () => this.model?.setDraft({ thread: thrSel.value as ExperimentDraft["thread"] }))
    seRow.appendChild(thrSel)
    body.appendChild(seRow)

    // Rung 3 in the panel (§8.3, review fix 4c — "all in the panel"
    // per WEFT-DEVTOOLS §10's rung-3 gate): the tools every run the
    // runtime starts parks on. Rendered only when meta reports the
    // breakpoints capability. The rule is the runtime's: the app's own
    // turns are not touched by it (PQ7), and the label says so.
    // Runtime-wide, so no panel token may set it (the server token, or
    // setup A's open API).
    if (hasCapability(s, "breakpoints") && tokenScope(this.cfg.token) === "" && agent?.tools.length) {
      const brkRow = el("div", "weft-field")
      brkRow.appendChild(
        el("span", undefined, "Break on (parks every run)", {
          title: "applies to runs this runtime starts — the app's own turns are viewer-only (PQ7)",
        })
      )
      for (const t of agent.tools) {
        const cb = el("input") as HTMLInputElement
        cb.type = "checkbox"
        cb.checked = s.breakpoints.includes(t.name)
        cb.addEventListener("change", () => {
          const set = this.model?.state.breakpoints ?? s.breakpoints
          const next = set.filter((n) => n !== t.name)
          if (cb.checked) next.push(t.name)
          next.sort()
          this.go(this.model?.setBreakpoints(next))
        })
        brkRow.appendChild(el("label", "weft-tool", [cb, el("span", undefined, t.name)]))
      }
      body.appendChild(brkRow)
    }

    // The transcript edits (D2/D3): when continuing from a step, the
    // kept steps' results are patchable and their call-free replies
    // rewritable — the counterfactual the fresh step answers.
    const t = s.turn
    if (d.step > 0 && t) {
      const draft = (): ExperimentDraft => this.model?.state.drawer ?? d
      const editsBox = el("div", "weft-field")
      editsBox.appendChild(el("span", undefined, `Transcript edits (steps 0..${d.step - 1} are kept)`))
      // Steps are numbered the way from_step and the edits count them
      // on the wire: over the run's own steps, in order (stepPosition).
      for (const [n, step] of t.folded.steps.entries()) {
        if (n >= d.step) break
        for (const call of step.toolCalls) {
          if (!call.result) continue
          const lab = el("label", "weft-edit")
          lab.appendChild(el("span", undefined, `step ${n} · ${call.name} →`))
          const inp = this.field(
            el("input", "weft-input") as HTMLInputElement,
            `edit:${n}:${call.callId}`
          )
          inp.placeholder = String(call.result.content).slice(0, 60)
          const editOf = () => draft().edits.find((e) => e.step === n && e.callID === call.callId)
          inp.value = editOf()?.toolResult ?? ""
          inp.addEventListener("input", () => {
            const cur = editOf()
            const next = [...draft().edits]
            const i = cur ? next.indexOf(cur) : -1
            if (inp.value === "") {
              if (i >= 0) next.splice(i, 1)
            } else if (i >= 0) {
              next[i] = { ...cur, toolResult: inp.value, step: n, callID: call.callId }
            } else {
              next.push({ step: n, callID: call.callId, toolResult: inp.value })
            }
            this.model?.setDraft({ edits: next }, true)
          })
          lab.appendChild(inp)
          editsBox.appendChild(lab)
        }
        if (step.text && !step.toolCalls.length) {
          const lab = el("label", "weft-edit")
          lab.appendChild(el("span", undefined, `step ${n} · reply`))
          const reply = this.field(
            el("textarea", "weft-input") as HTMLTextAreaElement,
            `edit:${n}`
          )
          reply.rows = 2
          const editOf = () => draft().edits.find((e) => e.step === n && !e.callID)
          reply.value = editOf()?.content ?? ""
          reply.addEventListener("input", () => {
            const cur = editOf()
            const next = [...draft().edits]
            const i = cur ? next.indexOf(cur) : -1
            if (reply.value === "") {
              if (i >= 0) next.splice(i, 1)
            } else if (i >= 0) {
              next[i] = { ...cur, content: reply.value, step: n }
            } else {
              next.push({ step: n, content: reply.value })
            }
            this.model?.setDraft({ edits: next }, true)
          })
          lab.appendChild(reply)
          editsBox.appendChild(lab)
        }
      }
      if (editsBox.childElementCount > 1) body.appendChild(editsBox)
    }

    const run = el("button", "weft-run-btn", "Run experiment ▶", {
      title: "POST /api/playground/runs — the runtime in your app executes it",
    })
    run.addEventListener("click", () => this.go(this.model?.runExperiment()))
    body.appendChild(run)
    card.appendChild(body)
    return card
  }

  /** experimentResult is §3's Result pane: the label (`t3·x1`), the
   * stream in place, the inline diff against the source turn, and —
   * when the run parked — the approval controls (continue / skip /
   * resolve, the runtime-started run's own verbs). */
  private experimentResult(s: PanelState): HTMLElement {
    const r = s.result
    if (!r) return el("div")
    const card = el("div", "weft-step weft-xres")
    const usage = r.row?.usage
    const stats = [
      r.state,
      usage ? `${tokens(usage.input_tokens)}→${tokens(usage.output_tokens)} tok` : "",
      r.row ? duration(r.row.started, r.row.finished) : "",
    ]
      .filter(Boolean)
      .join(" · ")
    const head = el("div", "weft-step-h", [
      el("span", undefined, `Result · ${r.label}`),
      el("span", undefined, stats),
      el("span", "weft-grow"),
    ])
    // P4's saves (§3's row): keep-as-prompt is the copy-the-text
    // fallback (PQ2: the weft/prompt version lands with that module);
    // save-as-fixture hands off to Studio (the files download there).
    const keep = el("button", "weft-btn", "keep as prompt ⤴", {
      title: "copy the edited prompt (weft/prompt versions are post-v1, PQ2)",
    })
    keep.addEventListener("click", () => {
      const text = this.model?.state.drawer?.instructions ?? ""
      // The clipboard refuses without focus or permission: not the
      // host page's error.
      try {
        // undefined outside a secure context
        const clip = navigator.clipboard as Clipboard | undefined
        clip?.writeText(text).catch(quiet)
      } catch {
        // no clipboard here
      }
    })
    head.appendChild(keep)
    const fixture = el("a", "weft-btn", "save as fixture", {
      href: href(this.base, playgroundLink(r.runID ? { run: r.runID } : {})),
      target: "_blank",
      rel: "noopener",
      title: "hand off to Studio: the run's records as wefttest replay fixtures (D4)",
    })
    fixture.style.textDecoration = "none"
    head.appendChild(fixture)
    const compare = el("a", "weft-btn", "compare in Studio", {
      title: "open the Studio playground with this run, step and the current overrides carried over",
    })
    // Built when it is used: the drawer's text fields patch the draft
    // without a redraw, and the link must carry what they say now.
    const link = () => {
      const draft = this.model?.state.drawer ?? null
      const mine = draft && draft.runId === r.sourceRunID ? draft : null
      // The step Studio continues from: the drawer's own, else the one
      // being read — as from_step counts it.
      const step =
        mine && mine.step > 0 ? mine.step : s.turn ? stepPosition(s.turn.folded, s.selectedStep) : -1
      compare.setAttribute("href", studioPlaygroundLink(this.base, mine, step))
    }
    link()
    compare.setAttribute("target", "_blank")
    compare.setAttribute("rel", "noopener")
    for (const ev of ["pointerdown", "focus", "click", "contextmenu"]) compare.addEventListener(ev, link)
    compare.style.textDecoration = "none"
    head.appendChild(compare)
    const discard = el("button", "weft-btn", "discard", { title: "clear the result pane" })
    discard.addEventListener("click", () => this.model?.discardResult())
    head.appendChild(discard)
    card.appendChild(head)
    const body = el("div", "weft-step-b")
    if (r.error) body.appendChild(el("div", "weft-note weft-warn", r.error))
    const status = r.row?.status ?? (r.ready ? "succeeded" : "running")
    for (const step of r.folded.steps) {
      if (step.text) body.appendChild(el("div", undefined, step.text))
      for (const call of step.toolCalls)
        body.appendChild(
          renderCall(call, step.index, status, undefined, undefined, r.runID ? { endpoint: this.base, runId: r.runID } : undefined)
        )
    }
    if (!r.folded.steps.length && !r.error && r.state === "queued")
      body.appendChild(el("div", "weft-note", "queued — waiting for the runtime to ack…"))
    else if (!r.folded.steps.length && !r.error && r.state === "accepted" && !r.runID)
      // A fork's accepted row names no run: the finished one names the
      // session's new turn, and the pane moves to it then.
      body.appendChild(el("div", "weft-note", "accepted — the fork's turn is running in its new session…"))

    // The 2-way compare (P3, PQ3 — the panel stays 2-way): the diff's
    // other side is the source turn by default, or any sibling of it.
    const siblings = [
      { id: "", label: sourceLabel(r.label) },
      ...(s.experiments.get(r.sourceRunID) ?? [])
        .filter((x) => x.id !== r.runID)
        .map((x) => ({ id: x.id, label: shortId(x.id) })),
    ]
    if (siblings.length > 1) {
      const cmp = el("select", "weft-input") as HTMLSelectElement
      for (const sb of siblings) {
        const o = el("option", undefined, `compare vs ${sb.label || "source"}`) as unknown as HTMLOptionElement
        o.value = sb.id
        cmp.appendChild(o)
      }
      cmp.value = r.compareWith
      cmp.addEventListener("change", () => this.go(this.model?.setCompare(cmp.value)))
      body.appendChild(cmp)
    }

    // The inline diff (§3: `diff vs t3:`), once there is final text —
    // on text, and on the tool calls beside it. Both sides are whole
    // turns read the same way (turnWordsOf), and only an ended run is
    // diffed: a table per streamed delta is the host page's frame
    // budget, and half an answer is not a difference.
    const mine: TurnWords | null = r.ready ? (r.words ?? foldedWords(r.folded)) : null
    const other: TurnWords | undefined = r.compareWith
      ? this.model?.compareWords.get(r.compareWith)
      : r.source
    const otherLabel = r.compareWith ? shortId(r.compareWith) : sourceLabel(r.label)
    if (mine && other && mine.text && other.text) {
      const diffBox = el("div", "weft-diff")
      this.diffInto(diffBox, `diff vs ${otherLabel}:`, other.text, mine.text)
      // The tool-call diff beside the text: name(args) lines.
      if (other.calls.join("\n") !== mine.calls.join("\n"))
        this.diffInto(diffBox, "tool calls:", other.calls.join("\n"), mine.calls.join("\n"))
      body.appendChild(diffBox)
    }

    // The parked calls' controls: the runtime-started run's approval
    // verbs (§8.2), through POST /api/runs/{id}/approvals. Offered
    // only once the pane holds this run's own stored pending set
    // (ready) — a call still streaming, or left over from an earlier
    // leg of a chain, is not this run's to decide.
    if (r.ready && r.folded.pending.length && r.runID && this.canAct(s))
      body.appendChild(this.decisions(r.folded.pending, r.decided))
    // Rung 4 (§8.4): steer the running experiment — one user message
    // delivered mid-flight into the run the runtime holds (an ephemeral
    // run, or a fork's turn once the runtime's ack names it).
    if (hasCapability(s, "steer") && this.canAct(s) && r.state === "accepted" && r.runID) {
      const box = el("div", "weft-step")
      box.appendChild(el("div", "weft-step-h", [el("span", undefined, "steer this run")]))
      const b = el("div", "weft-step-b")
      const input = this.field(el("input", "weft-input") as HTMLInputElement, "steer")
      input.placeholder = "a message delivered mid-flight"
      input.value = this.scratch.get("steer") ?? ""
      input.addEventListener("input", () => this.scratch.set("steer", input.value))
      const send = el("button", "weft-btn", "steer", { title: "POST /api/runs/{id}/steer (ADR 0019)" })
      send.addEventListener("click", () => {
        if (!input.value) return
        this.go(this.model?.steer(input.value))
        this.scratch.delete("steer")
        input.value = ""
      })
      b.append(input, send)
      box.appendChild(b)
      body.appendChild(box)
    }
    card.appendChild(body)
    return card
  }

  /** diffInto appends one before → after diff under its header, or
   * the note that sends a diff too large for the dock to Studio. */
  private diffInto(box: HTMLElement, title: string, before: string, after: string) {
    const cells = (before.split("\n").length + 1) * (after.split("\n").length + 1)
    if (cells > DIFF_CELLS) {
      box.appendChild(el("div", "weft-diff-h", `${title}  too large for the panel — compare in Studio`))
      return
    }
    const rows = diffLines(before, after)
    box.appendChild(el("div", "weft-diff-h", `${title}  ${diffSummary(rows)}`))
    for (const row of rows) {
      if (row.kind === "same") continue
      box.appendChild(
        el("div", `weft-diff-row weft-diff-${row.kind}`, `${row.kind === "add" ? "+" : "−"} ${row.text}`)
      )
    }
  }

  /** decisions draws the parked calls of the result's run with the
   * approval verbs. resolve pastes the result typed beside it — never
   * an empty one. Each call is decided on its own (POST
   * /api/runs/{id}/approvals carries one), and the runtime holds the
   * decisions until every parked call has one, then resumes the run
   * once: the pane shows which are in and how many it still waits
   * for. A held decision can be changed until the last one lands. */
  private decisions(pending: ToolCallPart[], decided: Record<string, string>): HTMLElement {
    const approvals = el("div", "weft-step")
    approvals.appendChild(el("div", "weft-step-h", [el("span", undefined, "awaiting decision")]))
    const abody = el("div", "weft-step-b")
    const left = pending.filter((c) => !decided[c.id]).length
    if (left < pending.length) {
      abody.appendChild(
        el(
          "div",
          "weft-note",
          `waiting for ${left} more decision${left === 1 ? "" : "s"} — the run resumes once every parked call is decided`
        )
      )
    }
    const verbs: Record<string, string> = { approve: "continue", deny: "skip", resolve: "resolve" }
    for (const call of pending) {
      const line = el("div", "weft-call")
      const head = el("div", "weft-call-h", [
        el("span", "weft-name", call.name),
        el("span", "weft-args", call.args === undefined ? "(…)" : fmtJSON(call.args)),
      ])
      if (decided[call.id])
        head.appendChild(el("span", "weft-badge weft-info", `decided: ${verbs[decided[call.id]] ?? decided[call.id]}`))
      line.appendChild(head)
      const ctl = el("div", "weft-res")
      const key = `resolve:${call.id}`
      const paste = this.field(el("input", "weft-input weft-resolve") as HTMLInputElement, key)
      paste.placeholder = "the result to resolve with"
      paste.value = this.scratch.get(key) ?? ""
      paste.addEventListener("input", () => this.scratch.set(key, paste.value))
      const mk = (label: string, title: string, act: () => void) => {
        const b = el("button", "weft-btn", label, { title })
        b.addEventListener("click", act)
        return b
      }
      ctl.append(
        mk("continue", "Approve: the handler runs for real", () =>
          this.go(this.model?.decide(call.id, "approve"))
        ),
        mk("skip", "Deny: the model sees a denied result", () =>
          this.go(this.model?.decide(call.id, "deny"))
        ),
        paste,
        mk("resolve…", "Resolve: the model sees the result typed here; the handler never runs", () => {
          if (!paste.value) {
            paste.focus()
            return
          }
          this.go(this.model?.decide(call.id, "resolve", paste.value))
        })
      )
      line.appendChild(ctl)
      abody.appendChild(line)
    }
    approvals.appendChild(abody)
    return approvals
  }

  /** turnView draws the step story (§2): the prompt, per step the
   * model text (reasoning collapsed), tool calls name(args) → result,
   * finish reason and usage with cached/reasoning splits, pending
   * approvals read-only, and the honesty notes. With the playground
   * capability on, the experiment actions follow (§3's row). */
  private turnView(s: PanelState): HTMLElement {
    const t = s.turn
    if (!t) return el("div")
    const wrap = el("div")
    wrap.appendChild(this.notes(t))
    const ids = this.turnLinks(t)
    if (ids) wrap.appendChild(ids)
    const wf = waterfall(t.spans ?? [])
    if (wf.length) wrap.appendChild(renderWaterfall(wf))
    const prompt = turnPromptOf(t.transcript)
    if (prompt) wrap.appendChild(el("div", "weft-note", prompt))
    // thread's session markers (A9.2) at the top of the turn they are
    // filed under; a run from before A9 names none and draws nothing.
    const comps = compactionsOf(t.doc)
    for (const c of comps.filter(isSessionMarker))
      wrap.appendChild(compactionBox(c, comps, t.transcript, { keys: this.openKeys, scope: t.id }))
    const row = this.rowOf(t.id)
    wrap.appendChild(
      renderFolded(
        t.folded,
        row?.status ?? t.doc?.status ?? "running",
        t,
        s.selectedStep,
        { keys: this.openKeys, scope: t.id },
        { endpoint: this.base, runId: t.id }
      )
    )
    if (t.folded.pending.length) wrap.appendChild(this.approvals(t.folded.pending, !!row?.playground))
    if (this.canAct(s)) wrap.appendChild(this.actions(s))
    return wrap
  }

  /** turnLinks is the turn's join keys as links (G1): its session and
   * its trace, each the page Studio shows it on. */
  private turnLinks(t: TurnView): HTMLElement | null {
    const row = this.rowOf(t.id)
    const session = row?.session_id || t.doc?.session_id || ""
    const trace = row?.trace_id || t.doc?.trace_id || ""
    if (!session && !trace) return null
    const box = el("div", "weft-row2")
    const link = (label: string, url: string, title: string, attr: string, id: string) => {
      const a = el("a", "weft-chip", label, { href: url, target: "_blank", rel: "noopener", title, [attr]: id })
      a.style.textDecoration = "none"
      return a
    }
    if (session)
      box.appendChild(
        link(`session ${session}`, href(this.base, sessionLink(session)), "the session in Studio", "data-weft-session-link", session)
      )
    if (trace)
      box.appendChild(
        link(`trace ${trace.slice(0, 8)}`, href(this.base, traceLink(trace)), `the OTel trace ${trace} in Studio`, "data-weft-trace-link", trace)
      )
    return box
  }

  /** actions is §3's row: ✎ Experiment (the drawer), ↻ Re-run (the
   * whole turn with the drawer's current edits), ⎇ Continue from the
   * step being read. */
  private actions(s: PanelState): HTMLElement {
    const t = s.turn
    if (!t) return el("div")
    const row = el("div", "weft-actions")
    const experiment = el("button", "weft-btn", "✎ Experiment", {
      title: "open the experiment drawer, pre-filled from the registered config",
    })
    experiment.addEventListener("click", () => this.go(this.model?.openExperiment(t.id, 0)))
    row.appendChild(experiment)
    const rerun = el("button", "weft-btn", "↻ Re-run", {
      title: "re-run the whole turn with the drawer's current edits",
    })
    rerun.addEventListener("click", () => this.go(this.model?.rerun(t.id)))
    row.appendChild(rerun)
    const from = stepPosition(t.folded, s.selectedStep)
    if (from > 0) {
      const cont = el("button", "weft-btn", `⎇ Continue from step ${from}`, {
        title: "keep the transcript through the previous step (edits apply) and run this step fresh",
      })
      cont.addEventListener("click", () => this.go(this.model?.openExperiment(t.id, from)))
      row.appendChild(cont)
    }
    return row
  }

  /** notes renders the honesty rules (§2): truncation badges live on
   * the calls themselves (renderCall); these are the turn's holes from
   * the shared table (lib/honesty.ts, as the run page's header) — the
   * run document's (not_recorded, interrupted, derived, stripped,
   * gap), the recorder's cuts, the gaps the walk saw, a max_tokens
   * finish, content-off spans — each a badge with its reason and fix,
   * and a story longer than the panel reads. */
  private notes(t: TurnView): HTMLElement {
    const box = el("div")
    box.setAttribute("data-weft-turn-holes", "")
    const row = this.rowOf(t.id)
    const holes = turnHoles(t, row)
    for (const m of holes) {
      const w = holeWords(m)
      const note = el("div", `weft-note${w.tone === "loss" ? " weft-warn" : ""}`, `${w.label} — ${w.reason}${w.fix ? ` · fix: ${w.fix}` : ""}`)
      note.setAttribute("data-weft-hole", m.hole)
      box.appendChild(note)
    }
    if (t.capped) {
      box.appendChild(
        el(
          "div",
          "weft-note weft-warn",
          `a long run: the first ${MAX_EVENT_PAGES * 500} events are shown — the whole story is in Studio (⤢)`
        )
      )
    }
    return box
  }

  /** approvals shows the open turn's parked calls read-only (§2): the
   * decision verbs act on runs a runtime started (§8.2), from the
   * result pane that ran them — the app's own turns are viewer-only
   * (D7, PQ7), and the note says so. */
  private approvals(pending: { id: string; name: string; args?: unknown }[], experiment: boolean): HTMLElement {
    const box = el("div", "weft-step")
    box.appendChild(el("div", "weft-step-h", [el("span", undefined, "awaiting decision (read-only)")]))
    const body = el("div", "weft-step-b")
    for (const call of pending) {
      const line = el("div", "weft-call")
      line.appendChild(
        el("div", "weft-call-h", [
          el("span", "weft-name", call.name),
          el("span", "weft-args", call.args === undefined ? "(…)" : fmtJSON(call.args)),
          el("span", "weft-badge weft-info", "parked"),
        ])
      )
      line.appendChild(
        el(
          "div",
          "weft-res",
          experiment
            ? "an experiment's run — its decision controls are in the result pane of the turn that ran it"
            : "the app's own turns are viewer-only (PQ7) — decide from your app"
        )
      )
      body.appendChild(line)
    }
    box.appendChild(body)
    return box
  }

  private footer(s: PanelState): HTMLElement {
    const line = "prompts, args and results from your app, via your Studio"
    const parts: HTMLElement[] = [el("span", undefined, line)]
    if (strippedContent(s.turn?.folded)) parts.push(el("span", undefined, " · content is stripped for this destination"))
    // The resolved detection choice, one word (C3.2); and, when the
    // page's fetch could not be put back (another patcher wrapped it
    // after the panel), that too.
    const word = this.detectWord()
    parts.push(
      el("span", "weft-detect", ` · detect: ${word}${this.rung?.chained ? " (chained)" : ""}${this.notRestored && !this.rung ? " (fetch not restored: patched after the panel)" : ""}`, {
        title: word.startsWith("url")
          ? "scope from the page URL's weft_scope"
          : word.includes("headers") || word === "markers"
            ? "reading Weft-Scope on same-origin fetches / data-weft-scope markers"
            : "scope from data-scope / window.__WEFT__",
      })
    )
    if (this.globalNote) parts.push(el("span", "weft-global", ` · global: ${this.globalNote}`))
    return el("div", "weft-footer", parts)
  }

  private rawView(t: TurnView): HTMLElement {
    return el("pre", "weft-raw", fmtJSON({ doc: t.doc, events: t.events, transcript: t.transcript }))
  }

  private rowOf(id: string): RunRow | undefined {
    return this.model?.rowOf(id)
  }

}

/** turnHoles is a turn's holes, from the shared table: the run
 * document's and the fold's (runHoles, the run page's header), the
 * row's interrupted status and max_tokens stop, the walk's gaps. */
export function turnHoles(t: TurnView, row?: RunRow): HoleMark[] {
  return mergeHoles(
    runHoles(t.doc, t.folded),
    statusHoles({
      status: row?.status ?? t.doc?.status,
      stop_reason: row?.stop_reason ?? t.doc?.stop_reason,
      gaps: t.gaps,
    })
  )
}

/** linkedStep is the step ⤢ carries into Studio (G1): the one the
 * user is reading, else — while the turn runs — the running one (its
 * last step). Either is the step's ordinal (its index as the loop
 * counts it, lib/links.ts), never an event's position. */
export function linkedStep(s: PanelState): number | undefined {
  if (s.selectedStep != null) return s.selectedStep
  const t = s.turn
  if (!t || t.id !== s.selected) return undefined
  const row = [...s.turns, ...[...s.experiments.values()].flat()].find((r) => r.id === t.id)
  const running = (row?.status ?? t.doc?.status) === "running"
  return running ? t.folded.steps.at(-1)?.index : undefined
}

/** stepPosition is the step being read as source.from_step counts it
 * (the playground hand-off's step — not the step ordinal links carry):
 * its place among the run's own steps (0-based) — the runtime and
 * Studio cut the transcript at the Nth assistant message the run
 * produced, whatever index the step's events carry. -1 when none. */
export function stepPosition(view: FoldedRun, selected: number | null): number {
  return selected == null ? -1 : view.steps.findIndex((st) => st.index === selected)
}

/** renderWaterfall draws §2's timing mini-waterfall: one bar per
 * span over the run's own window, wall milliseconds beside. */
export function renderWaterfall(bars: { name: string; left: number; width: number; ms: number }[]): HTMLElement {
  const box = el("div", "weft-wf")
  for (const b of bars) {
    const row = el("div", "weft-wf-row")
    row.appendChild(el("span", "weft-wf-name", b.name, { title: b.name }))
    const track = el("span", "weft-wf-track")
    const bar = el("span", "weft-wf-bar")
    bar.style.left = `${(b.left * 100).toFixed(2)}%`
    bar.style.width = `${(b.width * 100).toFixed(2)}%`
    track.appendChild(bar)
    row.appendChild(track)
    row.appendChild(el("span", "weft-wf-ms", `${b.ms}ms`))
    box.appendChild(row)
  }
  return box
}

/** Where a fold is drawn: the Studio endpoint (the hand-off links),
 * and — inside a subagent expander — the child's own view, whose
 * request record and holes its steps show, and below which nothing
 * nests inline (one level, plan A10). */
export interface FoldCtx {
  endpoint?: string
  child?: ChildView
  /** The run the fold is of: its calls link to their place on its run
   * page (G1). */
  runId?: string
}

/** renderFolded draws the turn view (§2). */
export function renderFolded(
  view: FoldedRun,
  runStatus: string,
  t?: TurnView,
  selectedStep?: number | null,
  open?: OpenState,
  ctx?: FoldCtx
): HTMLElement {
  const wrap = el("div")
  if (view.model?.name) {
    wrap.appendChild(el("div", "weft-reason", `${view.model.provider}/${view.model.name}`))
  }
  for (const step of view.steps)
    wrap.appendChild(renderStep(step, runStatus, t, selectedStep, open, ctx))
  return wrap
}

function renderStep(
  step: FoldedStep,
  runStatus: string,
  t?: TurnView,
  selectedStep?: number | null,
  open?: OpenState,
  ctx?: FoldCtx
): HTMLElement {
  const card = el("div", "weft-step")
  card.setAttribute("data-weft-step", String(step.index))
  if (selectedStep === step.index) card.style.outline = "1px solid var(--w-accent)"
  const head = el("div", "weft-step-h", [
    el("span", undefined, `step ${step.index}`),
    el("span", "weft-grow"),
  ])
  // A child's step reads the child's record (by the child's id).
  const req = ctx?.child ? ctx.child.requests : t?.requests
  // The attempt and timing line (plan A4): the request rows already
  // read for the Request line, the folded step_finish's timing — no
  // fetch of its own; the same words as the run page (lib/attempts).
  const rows = req?.steps.get(step.index)?.rows ?? []
  // A record cut at the page cap holds every row of the steps before
  // its last row's step; that step's rows and later ones may be short
  // — no total and no pre-A4 reading from them.
  const complete = !req?.error && (!req?.truncated || step.index < lastStep(req))
  if (step.finish) {
    head.appendChild(el("span", undefined, step.finish.reason))
    head.appendChild(el("span", undefined, usageLine(step.finish.usage)))
  }
  // A step whose tools started has answered (tools run only after a
  // successful model call), finished or not.
  const line = complete ? attemptLine(factsFromRows(rows, !!step.finish || step.toolCalls.length > 0, runStatus === "running")) : null
  if (line) head.appendChild(el("span", "weft-badge weft-info", line, { "data-weft-attempts": "" }))
  if (step.finish) {
    const timing = timingLine(step.finish.latencyMs, step.finish.ttftMs, "ttft")
    if (timing) head.appendChild(el("span", undefined, timing, { "data-weft-timing": "" }))
  }
  const own = stepHoles(step, ctx?.child ? ctx.child.doc?.holes : t?.doc?.holes)
  const old = complete ? attemptsHole(step.finish, rows.length) : null
  const holes = holeBadges(old ? mergeHoles(own, [old]) : own)
  if (holes) head.appendChild(holes)
  card.appendChild(head)
  const body = el("div", "weft-step-b")
  // The step's run-scope compaction views (A9.2), on its step line.
  const doc = ctx?.child ? ctx.child.doc : t?.doc
  const comps = compactionsOf(doc)
  for (const c of comps)
    if (!isSessionMarker(c) && c.step === step.index)
      body.appendChild(compactionBox(c, comps, ctx?.child ? ctx.child.transcript : t?.transcript, open))
  if (req) body.appendChild(requestLine(step.index, req, runStatus, open))
  if (step.reasoning) {
    const d = el("details", "weft-collapsible")
    if (open) {
      // Collapsed by default (§2); what the user opened stays open
      // across redraws.
      const key = `${open.scope}\u0000reasoning\u0000${step.index}`
      d.setAttribute("data-weft-open", key)
      if (open.keys.has(key)) d.setAttribute("open", "")
    }
    d.appendChild(el("summary", undefined, "reasoning"))
    d.appendChild(el("div", undefined, step.reasoning))
    body.appendChild(d)
  }
  if (step.text) body.appendChild(el("div", undefined, step.text))
  if (step.steer) body.appendChild(el("div", "weft-note", `steered: ${step.steer.text}`))
  for (const call of step.toolCalls) body.appendChild(renderCall(call, step.index, runStatus, t, open, ctx))
  card.appendChild(body)
  return card
}

/** compactionBox is one compaction marker (plan A9.2, the run page's
 * words from lib/compaction): the line, the `compacted` badge, and
 * "show original" collapsed — for a view, the replaced transcript
 * messages read from the growth records the turn already holds (a gap
 * badge when they cannot be placed); for a session marker, where the
 * replaced context lives. Never a fetch. The open state is keyed by
 * a view's index (two views can share a hash: the same insertion at
 * the same place every step) and by a session marker's hash. */
function compactionBox(
  c: RunCompaction,
  all: RunCompaction[],
  transcript: Transcript | null | undefined,
  open?: OpenState
): HTMLElement {
  const session = isSessionMarker(c)
  const box = el("div", "weft-note")
  box.setAttribute("data-weft-compaction", session ? "session" : String(c.step ?? ""))
  const head = el("div", "weft-call-h", [
    el("span", "weft-name", session ? SESSION_LABEL : "compaction"),
    el("span", "weft-args", compactionLine(c)),
  ])
  const badges = holeBadges([{ hole: "compacted" }])
  if (badges) head.appendChild(badges)
  box.appendChild(head)
  const d = el("details", "weft-collapsible")
  if (open) {
    const key = `${open.scope}\u0000compaction\u0000${session ? `session\u0000${c.hash}` : `view\u0000${c.index ?? ""}`}`
    d.setAttribute("data-weft-open", key)
    if (open.keys.has(key)) d.setAttribute("open", "")
  }
  d.appendChild(el("summary", undefined, "show original"))
  if (session) {
    d.appendChild(el("div", "weft-res", sessionNote(c)))
  } else {
    const o = originalOf(c, transcript, all)
    if ("loading" in o) d.appendChild(el("div", "weft-res", "loading the transcript…"))
    else if ("gap" in o) {
      const gap = holeBadges([{ hole: "gap", reason: o.gap }])
      if (gap) d.appendChild(gap)
      d.appendChild(el("div", "weft-reason", o.gap))
    }
    else if (!o.messages.length) d.appendChild(el("div", "weft-res", `nothing replaced: inserted at message ${o.from}`))
    else
      for (const [i, m] of o.messages.entries())
        d.appendChild(el("div", "weft-res", messageLine(m), { "data-weft-original": String(o.from + i) }))
    d.appendChild(el("div", "weft-reason", replacementNote(c)))
  }
  box.appendChild(d)
  return box
}

/** lastStep is the highest step the request record holds a row of. */
function lastStep(req: PanelRequests): number {
  let n = -1
  for (const k of req.steps.keys()) if (k > n) n = k
  return n
}

/** A request hole's badge as the panel words it: the shared honesty
 * table's label (lib/honesty.ts — the run page reads the same table),
 * prefixed where the label does not name the request. */
function holeLabel(badge: string): string {
  const label = badge === "not_recorded" ? REQUEST_NOT_RECORDED_LABEL : holeWords({ hole: badge }).label
  return label.startsWith("request") ? label : `request: ${label}`
}

/** holeNote draws one hole: the badge, then its reason and fix as
 * words (the response's when it gave them, the shared table's else). */
function holeNote(box: HTMLElement, badge: string, reason?: string, fix?: string): void {
  const w = holeWords({ hole: badge, reason, fix })
  box.appendChild(el("span", "weft-badge weft-info", holeLabel(badge)))
  box.appendChild(el("div", "weft-reason", [w.reason, w.fix && `fix: ${w.fix}`].filter(Boolean).join(" — ")))
}

/** holeBadges draws a list of holes (a step's, a turn's) from the
 * shared table: one badge each, its reason and fix as the title. */
export function holeBadges(holes: HoleMark[]): HTMLElement | null {
  if (!holes.length) return null
  const box = el("span", "weft-holes")
  for (const m of holes) {
    const w = holeWords(m)
    const b = el("span", `weft-badge ${w.tone === "loss" ? "weft-warn-badge" : "weft-info"}`, w.label, {
      title: w.fix ? `${w.reason} — fix: ${w.fix}` : w.reason,
    })
    b.setAttribute("data-weft-hole", m.hole)
    box.appendChild(b)
  }
  return box
}

/** requestLine is the step's request (ADR 0028 §10, the Studio run
 * page's section in one line): the attempts and the changed marks,
 * the system prompt collapsed, the catalog's names, the params. A hole
 * is its badge with its reason and fix, nothing else — a hidden one
 * never holds a byte of prompt, because a read-scoped token never
 * asked for it. */
function requestLine(step: number, req: PanelRequests, runStatus: string, open?: OpenState): HTMLElement {
  const box = el("div", "weft-req")
  box.setAttribute("data-weft-request", String(step))
  if (req.badge) {
    holeNote(box, req.badge, req.reason, req.fix)
    return box
  }
  if (req.error) {
    box.appendChild(el("span", "weft-badge weft-err", `request could not be read: ${req.error}`))
    return box
  }
  const mine = req.steps.get(step)
  const row = mine?.rows[mine.rows.length - 1]
  if (!mine || !row) {
    box.appendChild(
      el(
        "span",
        "weft-badge",
        runStatus === "running"
          ? `request: ${REQUEST_NOT_STORED}`
          : req.truncated
            ? `request: truncated — first ${(MAX_REQUEST_PAGES * REQUEST_PAGE).toLocaleString("en-US").replace(",", " ")} requests`
            : "request: no record for this step"
      )
    )
    return box
  }
  const head = el("div", "weft-call-h", [
    el("span", "weft-name", "request"),
    el("span", "weft-args", mine.rows.map((r) => `attempt ${r.attempt}`).join(" · ")),
  ])
  if (mine.promptChanged) head.appendChild(el("span", "weft-badge weft-info", "prompt changed at this step"))
  if (mine.catalogChanged) head.appendChild(el("span", "weft-badge weft-info", "catalog changed at this step"))
  if (row.content && row.content !== "stripped") head.appendChild(el("span", "weft-badge", row.content))
  box.appendChild(head)
  if (row.content === "stripped") holeNote(box, "stripped")
  const p = row.prompt
  if (p && !isHoleRef(p)) {
    const d = el("details", "weft-collapsible")
    if (open) {
      const key = `${open.scope}\u0000request\u0000${step}`
      d.setAttribute("data-weft-open", key)
      if (open.keys.has(key)) d.setAttribute("open", "")
    }
    const text = p.text
    d.appendChild(el("summary", undefined, `system prompt · ${text.length} chars`))
    d.appendChild(el("div", "weft-res", text))
    box.appendChild(d)
  } else if (row.system_hash) {
    box.appendChild(
      el("div", "weft-res", `system prompt ${shortHash(row.system_hash)}${p ? ` · ${p.badge === "stripped" ? "stripped" : holeLabel(p.badge)}` : ""}`)
    )
  }
  const names = row.body.tools.names
  box.appendChild(el("div", "weft-res", `tools: ${names.length ? names.join(", ") : "none"}`))
  box.appendChild(el("div", "weft-res", `params: ${paramsLine(row)}`))
  return box
}

function renderCall(
  call: FoldedToolCall,
  stepIndex: number,
  runStatus: string,
  t?: TurnView,
  open?: OpenState,
  ctx?: FoldCtx
): HTMLElement {
  const box = el("div", "weft-call")
  const state = callState(call, runStatus)
  // The call's name is its place on the run page (G1): the trace view
  // with this call selected, named with its step — a call id repeats
  // across steps.
  const runId = ctx?.runId
  const name =
    ctx?.endpoint && runId
      ? el("a", "weft-name", call.name, {
          href: href(
            ctx.endpoint,
            runLink(runId, { step: stepIndex, call: call.callId, resumed: call.resumed })
          ),
          target: "_blank",
          rel: "noopener",
          title: `open this call in Studio (step ${stepIndex})`,
          "data-weft-call-link": call.callId,
        })
      : el("span", "weft-name", call.name)
  const head = el("div", "weft-call-h", [name, el("span", "weft-args", argsText(call))])
  box.appendChild(head)
  if (call.childRunId && (t || ctx?.child)) {
    // The badge is the child's run page too.
    head.appendChild(
      ctx?.endpoint
        ? el("a", "weft-badge weft-info", "subagent", {
            href: studioLink(ctx.endpoint, call.childRunId),
            target: "_blank",
            rel: "noopener",
            title: call.childRunId,
            "data-weft-subagent-link": call.childRunId,
          })
        : el("span", "weft-badge weft-info", "subagent", { title: call.childRunId })
    )
    // One level inline: a grandchild is its badge and the hand-off.
    if (ctx?.child) {
      if (ctx.endpoint) head.appendChild(handOff(ctx.endpoint, call.childRunId))
    } else if (t) box.appendChild(childBlock(call.childRunId, t, open, ctx?.endpoint))
  }
  if (call.result) {
    const ms = spanMs(t, call)
    if (ms) head.appendChild(el("span", "weft-badge weft-info", ms))
    const trunc = truncation(String(call.result.content))
    if (trunc) {
      head.appendChild(
        el(
          "span",
          "weft-badge",
          trunc.kind === "bytes"
            ? `truncated ${trunc.bytes} bytes`
            : "not executed (max_tokens)"
        )
      )
    }
    if (call.result.isError) head.appendChild(el("span", "weft-badge weft-err", "error"))
    const holes = holeBadges(call.holes ?? [])
    if (holes) head.appendChild(holes)
    box.appendChild(el("div", "weft-res", call.result.content))
  } else if (state === "running") {
    box.appendChild(el("div", "weft-res", "running…"))
  } else {
    box.appendChild(el("div", "weft-res weft-warn", "never completed"))
  }
  return box
}

/** spanMs reads the tool call's wall time off its execute_tool span
 * when the spans are loaded (the waterfall's numbers, in line). */
function spanMs(t: TurnView | undefined, call: FoldedToolCall): string {
  if (!t?.spans) return ""
  // The call's own span by its call id; a span without one (a foreign
  // SDK's) matches by tool name.
  const tools = t.spans.filter((sp) => sp.name === "execute_tool")
  const span =
    tools.find((sp) => sp.attrs["gen_ai.tool.call.id"] === call.callId) ??
    tools.find(
      (sp) =>
        sp.attrs["gen_ai.tool.call.id"] === undefined &&
        sp.attrs["gen_ai.tool.name"] === call.name
    )
  if (!span) return ""
  const ms = Date.parse(span.end) - Date.parse(span.start)
  if (!Number.isFinite(ms) || ms < 0) return ""
  return `${Math.round(ms)}ms`
}

/** childBlock is the lazy subagent expander (§2, Dv3): the call's
 * child run folds on demand and nests under the call. The element
 * delegates the toggle through [data-weft-child]. */
function childBlock(childId: string, t: TurnView, open?: OpenState, endpoint?: string): HTMLElement {
  const child = t.children.get(childId)
  const row = t.doc?.children.find((c) => c.id === childId)
  const details = el("details", "weft-collapsible")
  details.setAttribute("data-weft-child", childId)
  if (t.expanded.has(childId)) details.setAttribute("open", "")
  // The nested row (A10): agent, status, usage — the parent's row of
  // the child, before it is opened; its usage only once the record
  // has it, its holes (interrupted, stripped…) as badges.
  const summary = el(
    "summary",
    undefined,
    row
      ? `subagent ${row.agent || shortId(childId)} · ${row.status} · ${
          usageKnown(row.status) ? usageLine(row.usage) : row.status === "running" ? USAGE_AT_FINISH : "—"
        }`
      : `subagent ${shortId(childId)}`
  )
  const holes = row && holeBadges(child?.doc?.holes ?? rowHoles(row))
  if (holes) summary.appendChild(holes)
  details.appendChild(summary)
  if (endpoint) details.appendChild(handOff(endpoint, childId))
  if (!child) {
    details.appendChild(el("div", undefined, "loading the subagent's turn…"))
  } else {
    // The child's own status (the doc lists it): a child still running
    // shows its open calls as running, not as never completed.
    const status = row?.status ?? "succeeded"
    details.appendChild(
      renderFolded(child.folded, status, undefined, undefined, open && { keys: open.keys, scope: childId }, {
        endpoint,
        child,
        runId: childId,
      })
    )
    if (child.capped)
      details.appendChild(el("div", "weft-note weft-warn", "a long run: its first events are shown"))
  }
  return details
}

/** handOff is "open in Studio" for a child run: its run page
 * (studioLink, lib/links.ts's runLink). */
function handOff(endpoint: string, runId: string): HTMLElement {
  return el("a", "weft-btn", "open in Studio ⤢", {
    href: studioLink(endpoint, runId),
    target: "_blank",
    rel: "noopener",
    title: `open the child run ${runId} in Studio`,
    "data-weft-handoff": runId,
  })
}

function shortId(id: string): string {
  const parts = id.split("/")
  return parts[parts.length - 1] || id
}

/** studioPlaygroundLink builds the §2 hand-off: "compare in Studio"
 * with the context carried over — the run, the step being read, and
 * the drawer's current overrides — so nothing is retyped (the parity
 * rule's documented hand-off for the Studio-only surfaces). Every
 * parameter rides the URL fragment (/playground#run=…): a prompt can be
 * long and is the app's content — the fragment is never sent to a
 * server, never in its logs or a Referer, and no request line bounds
 * it. Studio's /playground reads the fragment (then strips it). */
export function studioPlaygroundLink(
  endpoint: string,
  draft: ExperimentDraft | null,
  step: number | null
): string {
  const p: PlaygroundHandoff = {}
  if (draft) {
    if (draft.runId) p.run = draft.runId
    if (step != null && step > 0) p.step = step
    // Only what changed (§10.1): Studio pre-fills the registered
    // prompt itself, and a prompt is long for a URL.
    if (draft.instructions && draft.instructions !== draft.registeredInstructions)
      p.instructions = draft.instructions
    const on = Object.entries(draft.tools)
      .filter(([, enabled]) => enabled)
      .map(([name]) => name)
    if (on.length && on.length < Object.keys(draft.tools).length)
      p.tools = on.join(",")
    if (draft.model) p.model = draft.model
    if (draft.thinking) p.thinking = draft.thinking
    if (draft.input && draft.step === 0) p.input = draft.input
    // The run's shape rides along too (the non-default values): a
    // scripted, parked or forked experiment must not open in Studio
    // as a live substitute ephemeral one.
    if (draft.engine === "scripted") p.engine = draft.engine
    if (draft.sideEffects && draft.sideEffects !== "substitute")
      p.side_effects = draft.sideEffects
    if (draft.thread === "fork") p.thread = draft.thread
    // …and who runs it: the agent and the runtime the drawer opened on.
    if (draft.agent) p.agent = draft.agent
    if (draft.runtimeId) p.runtime = draft.runtimeId
  }
  return href(endpoint, playgroundLink(p))
}

/** sourceLabel takes the turn part back out of a `t3·x1` label (the
 * diff header reads "diff vs t3"). */
function sourceLabel(label: string): string {
  return label.split("·")[0] || label
}

function argsText(call: FoldedToolCall): string {
  if (call.args !== undefined) {
    try {
      return `(${JSON.stringify(call.args)})`
    } catch {
      return "(?)"
    }
  }
  return call.streamedArgs ? `(${call.streamedArgs}…)` : "(…)"
}

/** usageLine renders usage with the cached/reasoning splits §2 names. */
function usageLine(u: Usage): string {
  const parts = [`${tokens(u.input_tokens)}→${tokens(u.output_tokens)} tok`]
  if (u.cached_input_tokens) parts.push(`${tokens(u.cached_input_tokens)} cached`)
  if (u.reasoning_tokens) parts.push(`${tokens(u.reasoning_tokens)} reasoning`)
  if (u.cache_write_tokens) parts.push(`${tokens(u.cache_write_tokens)} cache-write`)
  return parts.join(" · ")
}

/** mount is the programmatic mount (plan C2's rung 1; the npm entry,
 * C1, exports it — the script-tag bundle stays a side-effect module):
 * a <weft-devtools> configured by opts, field by field above every
 * other source, appended to target (default document.body). It is the
 * host's mount: where Studio does not answer it shows the
 * not-reachable line, never removes itself. */
export function mount(opts: MountOptions & { target?: Element } = {}): WeftDevtools {
  if (!customElements.get("weft-devtools")) customElements.define("weft-devtools", WeftDevtools)
  const { target, ...options } = opts
  const node = document.createElement("weft-devtools") as WeftDevtools
  node.options = options
  ;(target ?? document.body).appendChild(node)
  return node
}
