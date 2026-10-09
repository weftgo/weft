// <weft-devtools> — the panel custom element (§5): shadow DOM, no
// host-framework dependency (V1, the Dv0 decision), the §5.2
// attributes and defaults. The element owns presentation only;
// state.ts owns the data. Rung 1 is a viewer (§8.1); rung 2's
// experiment drawer and approval controls render only when
// meta.capabilities reports the playground (§8.5 item 3).
import type { Manifest, RunCompaction, RunRow, ToolCallPart, Transcript, Usage } from "../lib/api"
import { isHoleRef } from "../lib/api"
import { mergeHoles, rowHoles, statusHoles, USAGE_AT_FINISH, usageKnown } from "../lib/honesty"
import type { HoleMark } from "../lib/honesty"
import { paramsLine, REQUEST_NO_RECORD_REASON, REQUEST_NOT_STORED, shortHash } from "../lib/requests"
import {
  allowRefusals,
  breakpointsFor,
  canReplay,
  continueHere,
  editPromptAndReplay,
  editResultAndReplay,
  prefixLine,
  replayFromStep,
  replayVerdicts,
  rerun,
} from "../lib/replay"
import type { CatalogTool, ReplayDraft } from "../lib/replay"
import { replayBounds } from "../lib/experiment-body"
import { ackCatalogHole, badge, capLine, catalogCapped, catalogNotRecorded, catalogNotStored, catalogReadError, cutBadge, holeBadges, holeLine, noPublicIdWords, requestCapped, requestHole, turnChips } from "./badges"
import { fetchSessionPublicId, MAX_REQUEST_PAGES, PanelApiError, panelGet, REQUEST_PAGE } from "./client"
import type { AgentView } from "./client"
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
import { discoverEndpoint, headerRungOn, markerRungOn, readConfig, setViaPackage, tokenScope, urlScope, viaPackage } from "./config"
import type { MountOptions, PanelConfig } from "./config"
import { installHeaderRung } from "./detect"
import type { HeaderRung } from "./detect"
import { installMarkerRung, markerOf } from "./markers"
import type { Marker, MarkerRung } from "./markers"
import { parseScope, serializeScope } from "../lib/scope"
import type { Scope } from "../lib/scope"
import { el, fmtJSON, on, patch, spanWindow, waterfall } from "./render"
import { PANEL_CSS } from "./styles"
import { nextTheme, resolveTheme, themeSetting, ThemeWatch } from "./theme"
import {
  DEV_DISCOVERY_MS,
  emptyPanelState,
  listKey,
  MAX_EVENT_PAGES,
  DEV_LIMIT,
  PanelModel,
} from "./state"
import type { ChildView, PanelRequests, PanelState, TurnView } from "./state"
import { foldedWords, turnPromptOf } from "./playground"
import type { ExperimentDraft, TurnWords } from "./playground"
import { panelStudioVersion } from "./version"
import { clampLayout, CYCLE, geometry, initialLayout, NARROW_W, pillPlace, placedIn, Push, readStore, TABS, writeStore } from "./layout"
import { newTree, stopTree, treeView } from "./tree"
import type { TreeState } from "./tree"
import { renderRequestTab, stepsOf } from "./request"
import type { Geometry, Layout } from "./layout"
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

/** How many response paths the header rung remembers (byPath), oldest
 * dropped first: an app that calls a new path per request does not
 * grow the panel. */
export const PATHS_LIMIT = 64

/** How many runs (and parked calls) the per-run bookkeeping remembers —
 * the turn filter's prompts, the failures and parked calls reported —
 * oldest dropped first. Cleared on every start. */
export const RUN_MEMO_LIMIT = 500

/** capAdd adds k to a set bounded at max (insertion order: the oldest
 * goes). */
function capAdd(set: Set<string>, k: string, max: number): void {
  set.add(k)
  if (set.size > max) set.delete(set.values().next().value as string)
}

/** capSet sets k in a map bounded at max; a key set again is the
 * newest. */
function capSet<TValue>(m: Map<string, TValue>, k: string, v: TValue, max: number): void {
  m.delete(k)
  m.set(k, v)
  if (m.size > max) m.delete(m.keys().next().value as string)
}

/** The connected panels, in connect order: one owns Alt+W (a live,
 * page-mounted panel first — ownsToggle), so two panels on one page do
 * not toggle out of phase. */
const LIVE = new Set<WeftDevtools>()

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

/** typing: the key's real target (off the composed path) is a field. */
function typing(e: KeyboardEvent): boolean {
  const t = ((typeof e.composedPath === "function" ? e.composedPath()[0] : null) ?? e.target) as HTMLElement | null
  return !!t && (/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) || t.isContentEditable)
}

/** SHORTCUTS is the keyboard, in one place (the ? overlay and the
 * README's table). Every key but Alt+W fires only with focus in the
 * panel. */
export const SHORTCUTS: readonly (readonly [string, string])[] = [
  ["Alt+W", "toggle the dock from the page, not while a text field has focus (Ctrl+Shift+W too, where delivered)"],
  ["Alt+Shift+W", "next layout: float, dock right, bottom, left, top"],
  ["Esc", "close (this list first); the page's own Esc handlers still run"],
  ["j / k", "next / previous turn"],
  ["J / K", "next / previous step"],
  ["g s", "open the turn (and step) in Studio"],
  ["r", "the Raw tab (again: back to the tab before it)"],
  ["/", "filter: the raw tree's on the Raw tab, else the turn list's"],
  ["← / →", "on the tabs: the previous / next tab"],
  ["?", "this list"],
]

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
  /** A run of the conversation failed: its error, once per run (the
   * panel's own failures — Studio not answering — are lines, not
   * events). */
  error: { message: string; runId: string }
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

/** scopeValue reads a host-given Scope object: its string fields only,
 * sessionId / runId accepted for session / run (a run event's detail
 * fed back), a missing publicId read as "" when a session names the
 * conversation. null when it names neither. */
function scopeValue(v: unknown): Scope | null {
  if (!v || typeof v !== "object") return null
  const o = v as Record<string, unknown>
  const str = (...keys: string[]) => {
    for (const k of keys) if (typeof o[k] === "string" && o[k]) return o[k]
    return ""
  }
  const session = str("session", "sessionId")
  if (typeof o.publicId !== "string" && !session) return null
  const out: Scope = { publicId: typeof o.publicId === "string" ? o.publicId : "" }
  const flow = str("flow")
  const run = str("run", "runId")
  if (session) out.session = session
  if (flow) out.flow = flow
  if (run) out.run = run
  return out
}

/** How long a host's select() or session lookup waits for the panel's
 * start or rescope before saying it is not connected. */
export const SETTLE_MS = 10_000

/** scopeForm is the serialised explicit scope options carry ("" for none). */
function scopeForm(o: MountOptions): string {
  const sc = o.scope
  if (typeof sc === "string") return sc
  if (sc) return serializeScope(sc)
  return o.publicId ? serializeScope({ publicId: o.publicId }) : ""
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
    "data-push",
    "data-z-index",
    "data-theme",
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
  /** The layout (D1): mode, side, box, open, hidden, and what is
   * remembered with them — outside the render, so redraws keep it. */
  private lay: Layout
  /** data-push's padding on <html>, while it is applied. */
  private push = new Push()
  /** D2: follows <html>'s theme and prefers-color-scheme while connected. */
  private themeWatch = new ThemeWatch()
  /** The theme the current render resolved (D2). */
  private themeNow: "light" | "dark" = "dark"
  private resolveNow() {
    return resolveTheme(this.cfg.theme, this.lay.theme, this.themeWatch.host)
  }
  /** The user placed the panel (or a placement was stored): the
   * placement is remembered from then on. */
  private placed = false
  /** A drag or a resize is in progress: draws wait for its end. */
  private dragging = false
  /** The streaming step last drawn (run id + ordinal) and the line
   * said when it ended (D3). */
  private streamKey = ""
  private said = ""
  /** The turn row that holds the list's tab stop (D3), "" for none. */
  private rove = ""
  /** When g was pressed (the g s chord), 0 for none. */
  private gAt = 0
  /** The Raw tab's tree (D4): toggles, the filter, the copy's outcome;
   * of the turn treeFor (another turn starts closed, the filter kept). */
  private tree = newTree()
  private treeFor = ""
  private rawMemo: { key: unknown[]; doc: unknown } | null = null
  /** The Request tab's (E1.2): its trees (a tool's schema, the earlier
   * messages) and the attempt picked per step, of the turn rqFor; the
   * manifest (undefined: not asked yet; null: not readable here). */
  private rqTrees = new Map<string, TreeState>()
  private rqPick = new Map<number, number>()
  private rqFor = ""
  /** The manifest the Request tab reads, per (endpoint, token): doc
   * undefined until answered, null when it could not be read (asked
   * again after MANIFEST_RETRY_MS); hashes it was re-asked for. */
  private mf: { key: string; doc: Manifest | null | undefined; at: number; busy: boolean; asked: Set<string> } | null = null
  /** The turn list's filter (D4), this session only. */
  private tq = { text: "", status: "", err: false }
  /** The prompts of the turns opened so far, by run id: what the
   * filter's text matches beyond the id and the error. */
  private prompts = new Map<string, string>()
  /** The older-turns sentinel watched, and its observer (D4). */
  private io: IntersectionObserver | null = null
  private ioAt: Element | null = null
  /** The resize frame pending: a burst of resize events is one clamp
   * and one redraw per animation frame. */
  private resizeFrame: number | null = null
  private onResize = () => {
    if (this.resizeFrame !== null) return
    const frame = () => {
      this.resizeFrame = null
      if (!this.isConnected) return
      clampLayout(this.lay)
      this.render(this.last)
    }
    this.resizeFrame =
      typeof requestAnimationFrame === "function" ? requestAnimationFrame(frame) : (setTimeout(frame, 16) as unknown as number)
  }
  /** Expanded and not hidden. */
  private get shown(): boolean {
    return this.lay.open && !this.lay.hidden
  }
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
  private onRelease = () => {
    this.endDrag?.()
    this.release()
  }
  /** The drag in progress: its end (saved, redrawn) and its drop
   * (listeners off, nothing saved — a disconnect). */
  private endDrag: (() => void) | null = null
  private dropDrag: (() => void) | null = null

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
  /** Each conversation's statuses (by listKey): returning to one is not
   * a new sighting of its runs. */
  private evByKey = new Map<string, Map<string, string>>()
  /** What mount() / the markup's options named before the first
   * scope(): scope(null) puts it back. null: scope() has not set one. */
  private preScope: { scope?: MountOptions["scope"]; publicId?: string } | null = null
  /** Parked calls reported (run + call id), runs whose failure was
   * reported, runs whose pending calls are being read. */
  private evParked = new Set<string>()
  private evErrored = new Set<string>()
  private evParking = new Set<string>()
  /** The API object (window.weft.devtools), made once. */
  private apiObj: DevtoolsAPI | null = null
  /** The drawer opening whose verb's field took focus, and the field
   * (data-weft-k) the next draw focuses (plan F1). */
  private focusedKey = 0
  private focusNext = ""
  /** The verb that opened the drawer: focus goes back to it on close. */
  private opener: HTMLElement | null = null
  /** Why window.weft.devtools was not added ("" when it was, or off). */
  private globalNote = ""
  /** Set by the npm entry on the registered class: the package's
   * exports are the API, so no panel adds the global. */
  static noGlobal = false
  /** Set by the npm entry on the registered class (with noGlobal): the
   * bundle is part of the app's chunk, so rung 4 never takes the app's
   * script for the panel's tag (config.ts viaPackage). The package's
   * ssr-guard.js marks it before the first read; this keeps it. */
  static get npmEntry(): boolean {
    return viaPackage()
  }
  static set npmEntry(v: boolean) {
    setViaPackage(v === true)
  }

  constructor() {
    super()
    this.cfg = readConfig(this)
    this.lay = initialLayout(this.cfg.position, this.cfg.open, this.cfg.mode)
    this.shadow = this.attachShadow({ mode: "open" })
    this.body = el("div", "weft-root")
    attachStyles(this.shadow)
    this.shadow.append(this.body)
    this.shadow.addEventListener("pointerdown", () => this.hold())
    // Every shortcut but Alt+W is heard here, inside the shadow root:
    // only a key pressed with focus in the panel reaches it, so the
    // panel never captures a key of the host page's (D1).
    this.shadow.addEventListener("keydown", (e) => this.panelKey(e as KeyboardEvent))
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
      // data-position / data-open / data-mode are the initial values;
      // what this origin stored last (D1) wins over them.
      this.opened = true
      const st = readStore()
      this.placed = placedIn(st)
      this.lay = { ...initialLayout(this.cfg.position, this.cfg.open, this.cfg.mode), ...st }
    }
    clampLayout(this.lay)
    // §5.2's keyboard: Alt+W (and Ctrl+Shift+W where the browser
    // delivers it — Q4) toggles the dock from anywhere on the page;
    // every other key is the shadow root's (panelKey). Keys never fire
    // while the user types in an input — the host page's or the
    // panel's own.
    LIVE.add(this)
    window.addEventListener("keydown", this.onKey)
    window.addEventListener("resize", this.onResize, { passive: true })
    window.addEventListener("pointerup", this.onRelease, true)
    window.addEventListener("pointercancel", this.onRelease, true)
    window.addEventListener("hashchange", this.onURL, { passive: true })
    window.addEventListener("popstate", this.onURL, { passive: true })
    this.themeWatch.start(() => {
      if (this.getAttribute("data-theme-resolved") !== this.resolveNow()) this.render(this.last)
    })
    this.themeNow = this.resolveNow()
    this.syncTheme()
    this.syncGlobal()
    this.schedule()
  }

  disconnectedCallback() {
    LIVE.delete(this)
    window.removeEventListener("keydown", this.onKey)
    window.removeEventListener("resize", this.onResize)
    if (this.resizeFrame !== null) {
      if (typeof cancelAnimationFrame === "function") cancelAnimationFrame(this.resizeFrame)
      clearTimeout(this.resizeFrame)
      this.resizeFrame = null
    }
    // A tree's filter debounce or "copied" timer, and a pending
    // composition check, draw nothing once the element is gone.
    stopTree(this.tree)
    for (const st of this.rqTrees.values()) stopTree(st)
    this.dropDrag?.()
    this.dropDrag = null
    this.dragging = false
    this.push.restore()
    this.themeWatch.stop()
    this.io?.disconnect()
    this.io = this.ioAt = null
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
        const pre = this.preScope
        this.preScope = null
        const back: MountOptions = { ...rest }
        if (pre?.scope !== undefined) back.scope = pre.scope
        if (pre?.publicId !== undefined) back.publicId = pre.publicId
        this.options = back
        const form = scopeForm(back)
        if (form) this.setAttribute("data-weft-scope", form)
        else this.removeAttribute("data-weft-scope")
        this.rescan()
        this.render(this.last)
        return
      }
      const next = typeof s === "string" ? parseScope(s) : scopeValue(s)
      if (!next || (!next.publicId && !next.session && typeof s === "string" && s.trim() !== "")) {
        this.say("scope: no public id or session")
        return
      }
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
    // What mount() named stays mount()'s: scope(null) restores it.
    this.preScope ??= { scope: this.options?.scope, publicId: this.options?.publicId }
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
    this.afterSettle(async (ok) => {
      if (seq !== this.scopeSeq) return
      if (!ok) {
        if (!this.dormant) this.say(`session ${id}: the panel is not connected`)
        return
      }
      if (!this.ready || !this.base) return
      let pub = ""
      let mark = ""
      let why: { reason?: string; fix?: string } = {}
      try {
        const doc = (await fetchSessionPublicId({ base: this.base, token: this.cfg.token }, id)) as
          | { public_id?: unknown; badge?: unknown; reason?: unknown; fix?: unknown }
          | null
        pub = typeof doc?.public_id === "string" ? doc.public_id : ""
        mark = typeof doc?.badge === "string" ? doc.badge : ""
        // The response's words first (api.go sends them).
        why = {
          reason: typeof doc?.reason === "string" ? doc.reason : undefined,
          fix: typeof doc?.fix === "string" ? doc.fix : undefined,
        }
      } catch (err) {
        if (seq !== this.scopeSeq) return
        const status = err instanceof PanelApiError ? err.status : 0
        this.say(
          status === 403 || status === 401
            ? `session ${id}: the session lookup needs the dev token`
            : status === 404
              ? `session ${id} has no public id · unknown session`
              : `session ${id}: Studio did not answer the lookup`
        )
        return
      }
      if (seq !== this.scopeSeq) return
      if (!pub) {
        this.say(`session ${id} has no public id · ${mark === "not_recorded" ? noPublicIdWords(why.reason, why.fix) : "none recorded"}`)
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
      this.afterSettle(async (settled) => {
        if (seq !== this.selectSeq) return
        if (!settled) {
          if (!this.dormant) this.say(`select ${id}: the panel is not connected`)
          return
        }
        const m = this.model
        if (!m || !this.ready) return
        const found = await m.adoptRun(id)
        if (this.model !== m || seq !== this.selectSeq) return
        if (found !== "ok") {
          this.say(found === "unreachable" ? `run ${id}: Studio did not answer` : `run ${id} not in this conversation`)
          return
        }
        this.note = ""
        const loading = m.state.selected === id ? null : m.select(id, true)
        if (n !== undefined) m.selectStep(n)
        else if (!loading) this.render(this.last)
        await loading
        if (n === undefined || this.model !== m || seq !== this.selectSeq) return
        // The step, checked against the run's own steps once they are
        // read: an ordinal the run does not have is said, and the last
        // step is the one carried instead.
        const t = m.state.turn
        const steps = t && t.id === id ? t.folded.steps.map((st) => st.index) : []
        if (steps.length && !steps.includes(n)) {
          const last = steps[steps.length - 1]
          m.selectStep(last)
          this.say(`step ${n} not in run ${id} · showing step ${last}`)
        }
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
      // A frozen, sealed or read-only namespace cannot take the name:
      // said, not attempted (a strict-mode write would throw).
      const slot = Object.getOwnPropertyDescriptor(ours, "devtools")
      if (slot ? !slot.writable && !slot.set : !Object.isExtensible(ours)) {
        this.globalNote = "window.weft is the page's: no window.weft.devtools"
        return
      }
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
  private afterSettle(fn: (settled: boolean) => Promise<void>) {
    void this.settled()
      .then(fn)
      .catch(quiet)
  }

  /** settled waits for the scheduled apply and the start or rescope it
   * began, SETTLE_MS at most: false when they did not settle by then (a
   * start that hangs never holds a host's call for ever). */
  private async settled(): Promise<boolean> {
    const deadline = Date.now() + SETTLE_MS
    for (let i = 0; i < 20; i++) {
      // A scheduled apply runs in the microtask queued before this one.
      if (this.scheduled) await Promise.resolve()
      const p = this.settling
      const left = deadline - Date.now()
      if (left <= 0) return false
      let timer: ReturnType<typeof setTimeout> | undefined
      const late = await Promise.race([
        p.catch(quiet).then(() => false),
        new Promise<boolean>((r) => (timer = setTimeout(() => r(true), left))),
      ])
      clearTimeout(timer)
      if (late) return false
      if (p === this.settling && !this.scheduled) return true
    }
    return true
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
      if (m !== this.evModel) this.evByKey.clear()
      this.evModel = m
      this.evKey = key
      // A conversation seen before keeps its statuses: only runs never
      // seen are its history now.
      const known = this.evByKey.get(key)
      this.evStatus = known ?? new Map()
      if (!known) {
        this.evByKey.set(key, this.evStatus)
        if (this.evByKey.size > DETECTED_LIMIT) this.evByKey.delete(this.evByKey.keys().next().value ?? "")
      }
      this.evBaseline = true
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
        capAdd(this.evErrored, r.id, RUN_MEMO_LIMIT)
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
          capAdd(this.evParked, k, RUN_MEMO_LIMIT)
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
    return !!this.cfg.urlScope && !this.cfg.scopeExplicit && serializeScope(this.scopeNow()) === serializeScope(this.cfg.urlScope)
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
    capSet(this.byPath, path, sc, PATHS_LIMIT)
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

  /** keydown is the window's one key: Alt+W (Ctrl+Shift+W where the
   * browser delivers it) toggles the dock from anywhere — never while
   * the user types in a field, never during an IME composition, never
   * a key the page already handled. */
  private keydown(e: KeyboardEvent): void {
    if (e.defaultPrevented || e.isComposing || typing(e)) return
    if (!this.ownsToggle()) return
    const toggleCombo =
      (e.altKey && !e.ctrlKey && !e.shiftKey && !e.metaKey && e.code === "KeyW") || // Q4's pick
      (e.ctrlKey && e.shiftKey && !e.altKey && !e.metaKey && e.code === "KeyW") // where delivered
    if (!toggleCombo) return
    e.preventDefault()
    this.keys = false
    this.toggle()
    // Opened from the keyboard: focus goes to the dock, so Esc, j, k…
    // work at once (the user asked for the panel).
    if (this.shown) this.body.querySelector<HTMLElement>(".weft-dock")?.focus({ preventScroll: true })
  }

  /** ownsToggle: of the connected panels, exactly one answers Alt+W —
   * one toggle per press. A live, page-mounted panel (not dormant, not
   * the dock the bundle mounted by itself) is preferred, so markup the
   * page adds after a dormant mount or the auto dock can be toggled:
   * the one window.weft.devtools is when it is such a panel, else the
   * first connected such panel; with none, the global's panel, else
   * the first connected. */
  private ownsToggle(): boolean {
    let owner: WeftDevtools | undefined
    try {
      const ns = (window as unknown as { weft?: unknown }).weft
      const api = isPlainObject(ns) ? Object.getOwnPropertyDescriptor(ns, "devtools")?.value : undefined
      if (api) for (const p of LIVE) if (p.apiObjIs(api)) owner = p
    } catch {
      // a page's own window.weft: no global panel
    }
    const pageLive = (p: WeftDevtools) => !p.dormant && !p.autoMounted
    if (owner && pageLive(owner)) return owner === this
    for (const p of LIVE) if (pageLive(p)) return p === this
    return (owner ?? LIVE.values().next().value) === this
  }

  /** panelKey is every other shortcut (SHORTCUTS), heard on the shadow
   * root: focus is in the panel, or the key never arrives. Typing in
   * the panel's own fields is typing; Ctrl/Cmd keys stay the
   * browser's; preventDefault only on a key that was handled. */
  private panelKey(e: KeyboardEvent): void {
    if (e.defaultPrevented || e.isComposing) return
    if (e.key === "Tab") return this.trap(e)
    if (typing(e)) return
    const at = e.target as HTMLElement
    if ((e.key === "ArrowDown" || e.key === "ArrowUp") && at.classList.contains("weft-turn")) {
      // The roving tabindex (D3): arrows move the tab stop down the
      // turn list; Enter or a click selects.
      const rows = Array.from(this.body.querySelectorAll<HTMLElement>(".weft-turn"))
      const i = rows.indexOf(at) + (e.key === "ArrowDown" ? 1 : -1)
      const n = i < 0 ? undefined : rows.at(i)
      e.preventDefault()
      n?.focus()
      return
    }
    if (e.altKey && e.shiftKey && !e.ctrlKey && !e.metaKey && e.code === "KeyW") {
      e.preventDefault()
      this.cycle()
      return
    }
    if (e.ctrlKey || e.metaKey || e.altKey || !this.shown) return
    const k = e.key
    const chord = this.gAt > 0 && Date.now() - this.gAt < 1_500
    this.gAt = 0
    let done = true
    if (chord && k === "s") this.openInStudio()
    else if (k === "g") this.gAt = Date.now()
    else if (k === "Escape" && this.last.drawer && at.closest(".weft-drawer")) {
      // Escape on a drawer control closes the drawer (focus back to its
      // verb), not the panel.
      this.closeDrawer()
    } else if (k === "Escape") {
      if (this.keys) {
        this.keys = false
        this.render(this.last)
      } else this.toggle()
    } else if (k === "?") {
      this.keys = !this.keys
      this.render(this.last)
    } else if ((k === "r" || k === "R") && this.model) this.toggleRaw()
    else if (k === "j" || k === "k") this.turnKey(k === "j" ? 1 : -1)
    else if (k === "J" || k === "K") this.stepKey(k === "J" ? 1 : -1)
    else if (k === "/") {
      // D4: the open Raw tab's filter, else the turn list's; on the
      // Request tab (E1.2) an open tree's filter, else the turn list's.
      const tab = this.last.turn ? this.lay.tab : ""
      const tree = tab === "raw" || tab === "request" ? this.body.querySelector<HTMLElement>(`#weft-tp-${tab} .weft-tree-q`) : null
      const box = tree ?? (tab === "raw" ? null : this.body.querySelector<HTMLElement>(".weft-turn-q"))
      if (box) box.focus()
      else done = false // no box (an empty list): the key stays the page's
    } else done = false
    if (done) e.preventDefault()
  }

  /** trap keeps Tab inside a float that is open and focused (D3):
   * past the last control it wraps to the first, and back. Docked or
   * collapsed, Tab is the page's; Esc (collapse) lets go. */
  private trap(e: KeyboardEvent) {
    const dock = this.body.querySelector<HTMLElement>(".weft-dock.weft-float")
    if (!dock || !this.shown) return
    // Only what Tab can reach: not disabled, not inside a closed
    // <details> (its summary excepted), not CSS-hidden — measured where
    // there is layout, else the panel's own hiding rule (the narrow
    // float's list gives way to its dropdown).
    const laidOut = dock.getClientRects().length > 0
    const narrow = dock.classList.contains("weft-narrow")
    const stops = Array.from(
      dock.querySelectorAll<HTMLElement>("button, a[href], select, input, textarea, summary, [tabindex]")
    ).filter((n) => {
      if (n.tabIndex < 0 || (n as HTMLButtonElement).disabled) return false
      for (let d = n.parentElement?.closest("details"); d; d = d.parentElement?.closest("details"))
        if (!d.open && !(n.tagName === "SUMMARY" && n.parentElement === d)) return false
      return laidOut ? n.getClientRects().length > 0 : !(narrow && n.closest(".weft-rows"))
    })
    const first = stops[0]
    const last = stops.at(-1)
    const now = this.shadow.activeElement
    const to = e.shiftKey ? (now === first || now === dock ? last : null) : now === last ? first : null
    if (!to) return
    e.preventDefault()
    to.focus()
  }

  /** turnKey selects the next (1) or previous (-1) turn of the list. */
  private turnKey(d: number) {
    const s = this.last
    const ids = s.turns.filter((r) => this.match(r)).map((r) => r.id)
    const i = ids.indexOf(s.selected)
    const id = ids.at(Math.min(Math.max(i + d, 0), ids.length - 1))
    if (id && id !== s.selected) this.go(this.model?.select(id, true))
  }

  /** stepKey marks the next (1) or previous (-1) step of the open turn
   * as the one being read (⤢ and g s carry it). */
  private stepKey(d: number) {
    const s = this.last
    // The Request tab's picker lists the steps the record names too.
    const steps = s.turn ? (this.lay.tab === "request" ? stepsOf(s.turn, s.turn.requests) : s.turn.folded.steps.map((st) => st.index)) : []
    if (!steps.length) return
    const i = steps.indexOf(this.stepNow(s) ?? -1)
    const at = i < 0 ? (d > 0 ? 0 : steps.length - 1) : Math.min(Math.max(i + d, 0), steps.length - 1)
    this.model?.selectStep(steps[at])
  }

  /** stepNow is the step being read: linkedStep, or — on the Request
   * tab, which always shows one — the step it falls back to (the
   * first), so ⤢ and J/K start from what is on screen. */
  private stepNow(s: PanelState): number | undefined {
    const n = linkedStep(s)
    if (n !== undefined || this.lay.tab !== "request" || !s.turn || s.turn.id !== s.selected) return n
    return stepsOf(s.turn, s.turn.requests)[0]
  }

  /** openInStudio is g s: the selected turn (and step) in Studio, the
   * link ⤢ carries (lib/links.ts), in a new tab. */
  private openInStudio() {
    const url = this.last.selected ? this.studioLink(this.last.selected, this.stepNow(this.last)) : ""
    try {
      if (url) window.open(url, "_blank", "noopener")
    } catch {
      // a blocked popup: nothing thrown into the host
    }
  }

  /** cycle moves the dock to the next layout of CYCLE (Alt+Shift+W,
   * the header's layout button): float, dock right, bottom, left, top. */
  private cycle() {
    const l = this.lay
    const next = CYCLE[(CYCLE.indexOf(l.mode === "float" ? "float" : l.side) + 1) % CYCLE.length]
    if (next === "float") l.mode = "float"
    else {
      l.mode = "dock"
      l.side = next
    }
    clampLayout(l)
    this.save(true)
    this.render(this.last)
  }

  /** save remembers the layout (localStorage["weft.devtools"]); place
   * marks a user's placement (a toggle, a move, a resize, a re-dock). */
  private save(place = false) {
    this.placed ||= place
    writeStore(this.lay, this.placed)
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

  attributeChangedCallback(name: string, old: string | null, value: string | null) {
    if (old === value) return
    this.cfg = readConfig(this)
    if (!this.isConnected) return
    // A data-position set after connect is the host moving the dock
    // (D1): the float's corner, or a dock's side; the size stays.
    if (name === "data-position") {
      const at = initialLayout(this.cfg.position, true, "")
      const l = this.lay
      Object.assign(l, { mode: at.mode, side: at.side, y: innerHeight - l.h - 16 })
      l.x = this.cfg.position === "bottom-left" ? 16 : innerWidth - l.w - 16
    }
    if (name === "data-theme") this.render(this.last)
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
        if (this.model.publicId !== next.publicId) {
          this.scratch.clear()
          // The turn filter's prompts were the previous conversation's.
          this.prompts.clear()
        }
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
    // The previous connection's bookkeeping: a rescope by a new start
    // remembers nothing of the old one (dropRung clears byPath with its
    // rung; this clears it without one too).
    this.byPath.clear()
    this.prompts.clear()
    this.evParked.clear()
    this.evErrored.clear()
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
      model.prefer = this.lay.run
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

  /** toggle flips the dock; from hidden it opens (Alt+W, open()). */
  toggle() {
    if (this.lay.hidden) {
      this.lay.hidden = false
      this.lay.open = true
    } else this.lay.open = !this.lay.open
    this.save(true)
    this.render(this.last)
  }

  toggleRaw() {
    this.setTab(this.lay.tab === "raw" ? this.prevTab : "raw")
  }
  /** The tab r goes back to from Raw. */
  private prevTab = "story"

  /** setTab opens one of the turn view's tabs (D4), remembered as the
   * stored layout's tab (raw mirrors it). */
  private setTab(id: string) {
    if (!TABS.includes(id)) return
    if (this.lay.tab !== "raw") this.prevTab = this.lay.tab
    this.lay.tab = id
    this.lay.raw = id === "raw"
    this.save()
    this.render(this.last)
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
    // Once connected, a disconnected element draws nothing: a timer, a
    // debounce or a promise that answers after the disconnect must not
    // re-create an observer on a detached sentinel. The next connect
    // draws this.last. (Before the first connect the constructor's
    // empty frame is drawn.)
    if (!this.isConnected && this.opened) return
    // D2: the theme, resolved once per render (the header's ◐ and
    // data-theme-resolved read it); not before the connect, which
    // resolves it itself.
    if (this.isConnected) this.themeNow = this.resolveNow()
    try {
      this.watchRuns(s)
    } catch {
      // observability never changes behaviour
    }
    // Whether anyone is looking: the dev list is polled only then.
    this.model?.watch(this.shown && !this.dormant)
    // The selected turn is remembered (D1); the tab by setTab (D4).
    if (this.model && s === this.model.state && s.selected && s.selected !== this.lay.run) {
      this.lay.run = s.selected
      this.save()
    }
    if (this.held || this.composing || this.dragging) {
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
          on(again, "click", () => this.retry())
          line.appendChild(again)
        }
        next.push(line)
      }
    } else if (this.lay.hidden) {
      // hidden (D1): nothing drawn; the element, its API and its
      // events stay, and Alt+W or open() bring the dock back.
    } else if (!this.shown) {
      next.push(this.pill(s))
    } else if (!s.gone) {
      next.push(this.dock(s))
    }
    // The keyed patch (D3): the nodes on screen are kept where the new
    // build matches them, so the panes' scroll, the focused field and
    // its caret stay the user's without a restore. Only a focused node
    // the build no longer has (the dock folded to the pill) hands focus
    // to the dock or the pill, so the shortcuts keep working.
    const had = !!this.shadow.activeElement
    patch(root, next)
    if (had && !this.shadow.activeElement) root.querySelector<HTMLElement>(".weft-dock, .weft-fab")?.focus({ preventScroll: true })
    // A verb's drawer opens on its field (plan F1): once, then the
    // user's focus is theirs.
    if (this.focusNext) {
      const f = Array.from(root.querySelectorAll<HTMLElement>("[data-weft-k]")).find((n) => n.getAttribute("data-weft-k") === this.focusNext)
      this.focusNext = ""
      f?.focus({ preventScroll: true })
    }
    this.watchOlder()
    this.syncTheme()
    const mode = this.dormant ? "line" : this.lay.hidden ? "hidden" : this.shown ? this.lay.mode : "pill"
    if (root.getAttribute("data-mode") !== mode) root.setAttribute("data-mode", mode)
    // data-push (D1): only while a dock is drawn docked.
    // data-push (D1): a dock pads its side by its size; the bottom
    // sheet pads the bottom by its 70vh.
    if (this.cfg.push && this.isConnected && root.querySelector(".weft-docked")) this.push.apply(this.lay.side, `${this.lay.d}px`)
    else if (this.cfg.push && this.isConnected && root.querySelector(".weft-sheet")) this.push.apply("bottom", "70vh")
    else this.push.restore()
  }

  /** syncTheme sets data-theme-resolved on the element itself (D2):
   * the :host rule that picks the token set reads it. */
  private syncTheme(): void {
    const t = this.themeNow
    if (this.isConnected && this.getAttribute("data-theme-resolved") !== t) this.setAttribute("data-theme-resolved", t)
  }

  /** themeButton cycles the user's theme (D2): auto → light → dark;
   * stored per origin, auto clears it. An explicit data-theme wins, so
   * the button then only says so — aria-disabled, not disabled, so it
   * stays focusable and read. */
  private themeButton(): HTMLElement {
    const pick = themeSetting(this.lay.theme)
    const now = this.themeNow
    const fixed = this.cfg.theme !== "auto"
    const said = fixed ? `theme: ${now}, set by the page` : `theme: ${pick}${pick === "auto" ? ` (${now})` : ""}`
    const b = el("button", "weft-btn weft-theme", "◐", {
      type: "button",
      title: fixed ? said : `${said} — next: ${nextTheme(pick)}`,
      "aria-label": said,
      ...(fixed ? { "aria-disabled": "true" } : {}),
    })
    on(b, "click", () => {
      if (fixed) return
      const next = nextTheme(pick)
      this.lay.theme = next === "auto" ? "" : next
      writeStore(this.lay, this.placed)
      this.render(this.last)
    })
    return b
  }

  /** dock is the expanded panel, placed by the layout (D1): a float
   * dragged by its header and resized from its corner, a dock resized
   * along its edge, a bottom sheet on a narrow viewport. */
  private dock(s: PanelState): HTMLElement {
    clampLayout(this.lay)
    const g = geometry(this.lay)
    const dock = el("div", `weft-dock weft-open ${g.cls}`, undefined, {
      tabindex: "-1",
      role: "complementary",
      "aria-label": "weft devtools",
      "data-key": "dock",
    })
    this.place(dock, g)
    const head = this.header(s)
    const l = this.lay
    if (l.mode === "float" && !g.sheet) {
      head.classList.add("weft-drag")
      on(head, "pointerdown", (e) => {
        const { x, y } = l
        this.grab(e as PointerEvent, (dx, dy) => {
          l.x = x + dx
          l.y = y + dy
        })
      })
    }
    dock.appendChild(head)
    if (this.howTo && !this.model?.publicId) dock.appendChild(this.howToBox())
    const cols = el("div", "weft-cols")
    if (g.width < NARROW_W) {
      const pick = this.turnPick(s)
      if (pick) cols.appendChild(pick)
    }
    cols.appendChild(this.turnList(s))
    cols.appendChild(this.main(s))
    dock.appendChild(cols)
    dock.appendChild(this.footer(s))
    // The overlay lives inside the dock: it is its containing block.
    // Outside it it would be laid over the host page.
    if (this.keys) dock.appendChild(this.shortcuts())
    dock.appendChild(this.announcer(s))
    if (!g.sheet) {
      const float = l.mode === "float"
      const grip = el("div", float ? "weft-grip" : `weft-edge weft-edge-${l.side}`, undefined, { title: "resize", "aria-hidden": "true" })
      on(grip, "pointerdown", (e) => {
        const { w, h: ht, d } = l
        this.grab(e as PointerEvent, (dx, dy) => {
          if (float) {
            l.w = w + dx
            l.h = ht + dy
          } else l.d = d + ({ left: dx, right: -dx, top: dy, bottom: -dy } as const)[l.side]
        })
      })
      dock.appendChild(grip)
    }
    return dock
  }

  /** place gives the dock its box (and z-index, and the narrow class)
   * — also mid-drag, without a rebuild. */
  private place(dock: HTMLElement, g: Geometry = geometry(this.lay)) {
    for (const k of ["left", "top", "width", "height"]) dock.style.setProperty(k, g.style[k] ?? "")
    if (this.cfg.zIndex) dock.style.zIndex = this.cfg.zIndex
    dock.classList.toggle("weft-narrow", g.width < NARROW_W)
  }

  /** grab follows one drag of a handle (pointer events, captured):
   * move gets the pointer's offset, the layout is clamped and the dock
   * placed on every move; the end is remembered and redrawn. A press
   * on a control in the header is the control's. */
  private grab(e: PointerEvent, move: (dx: number, dy: number) => void) {
    const h = e.currentTarget as HTMLElement
    if (e.button > 0 || (e.target as Element).closest("button, a, select, input, textarea")) return
    e.preventDefault()
    const dock = h.closest(".weft-dock") as HTMLElement
    const x0 = e.clientX
    const y0 = e.clientY
    this.dragging = true
    try {
      h.setPointerCapture(e.pointerId)
    } catch {
      // no capture here (an old engine, a test DOM): moves on the handle still work
    }
    const mv = (ev: Event) => {
      const p = ev as PointerEvent
      move(p.clientX - x0, p.clientY - y0)
      clampLayout(this.lay)
      this.place(dock)
      if (this.cfg.push && dock.classList.contains("weft-docked")) this.push.apply(this.lay.side, `${this.lay.d}px`)
    }
    const ends = ["pointerup", "pointercancel", "lostpointercapture"]
    const off = () => {
      h.removeEventListener("pointermove", mv)
      for (const t of ends) h.removeEventListener(t, up)
      this.endDrag = this.dropDrag = null
      this.dragging = false
    }
    // Every end ends it: the release on the handle, a cancel, a lost
    // capture, a release anywhere (onRelease), a disconnect.
    const up = () => {
      off()
      this.save(true)
      this.render(this.last)
    }
    this.endDrag = up
    this.dropDrag = off
    h.addEventListener("pointermove", mv)
    for (const t of ends) h.addEventListener(t, up)
  }

  /** announcer is the step end, said once (D3): while the open turn's
   * running step streams its text is aria-busy; when that step ends
   * this role="status" line (always in the dock, visually hidden) is
   * written once — "step N finished · n words". */
  private announcer(s: PanelState): HTMLElement {
    const t = s.turn
    const status = t ? (this.rowOf(t.id)?.status ?? t.doc?.status ?? "running") : ""
    const cur = status === "running" ? t?.folded.steps.at(-1) : undefined
    const key = t && cur ? `${t.id}\u0000${cur.index}` : ""
    if (this.streamKey && this.streamKey !== key) {
      const [id, n] = this.streamKey.split("\u0000")
      const st = t?.id === id ? t.folded.steps.find((x) => String(x.index) === n) : undefined
      if (st) this.said = `step ${n} finished · ${(st.text || "").split(/\s+/).filter(Boolean).length} words`
    }
    this.streamKey = key
    return el("div", "weft-sr", this.said, { role: "status", "data-key": "said" })
  }

  /** turnPick is the turn column as a dropdown (a panel under
   * NARROW_W wide): every listed turn, the selected one chosen. */
  private turnPick(s: PanelState): HTMLElement | null {
    // The filter narrows the dropdown as it does the column; the
    // selected turn stays listed (it is what the select shows).
    const rows = [...s.turns, ...[...s.experiments.values()].flat()].filter((r) => r.id === s.selected || this.match(r))
    if (!rows.length) return null
    const sel = el("select", "weft-turn-pick", undefined, { "aria-label": "turn", "data-weft-k": "turnpick" }) as HTMLSelectElement
    for (const r of rows) {
      const o = el("option", undefined, `${statusChip(r)} · ${r.id} · ${r.steps} steps`, { value: r.id, "data-key": r.id }) as HTMLOptionElement
      o.selected = r.id === s.selected
      sel.appendChild(o)
    }
    on(sel, "change", (_, n) => this.go(this.model?.select((n as HTMLSelectElement).value, true)))
    return sel
  }

  /** pill is the collapsed dock (the fab). The activity signal (plan
   * C3.4): while the stream the panel holds anyway (its scope's, or the
   * fallback's agent stream) has a run reading running, it pulses (not
   * under prefers-reduced-motion) and shows the run's live step count —
   * its open tail's steps when it is the turn followed, else its row's
   * — "● 3"; nothing running, it is the plain pill. No stream of its own. */
  private pill(s: PanelState): HTMLElement {
    // The fallback streams one agent: another agent's row came by the
    // poll, and its status may be a poll period old.
    const streamed = (r: RunRow) => !!this.model?.publicId || r.agent === s.devAgent
    const running = s.live ? s.turns.find((r) => r.status === "running" && streamed(r)) : undefined
    const cls = `weft-fab weft-fab-${pillPlace(this.lay)}`
    if (!running) {
      const fab = el("button", cls, "devtools", {
        title: "weft devtools — Alt+W",
      })
      return this.pillEnd(fab, s)
    }
    const folded = s.turn?.id === running.id ? s.turn.folded.steps.length : 0
    const step = Math.max(folded, running.steps)
    const words = `weft devtools · running${step ? `, step ${step}` : ""}`
    const fab = el(
      "button",
      `${cls} weft-fab-running${prefersReducedMotion() ? "" : " weft-fab-pulse"}`,
      [document.createTextNode("devtools"), el("span", "weft-fab-count", step ? ` ● ${step}` : " ●")],
      { title: `${words} — Alt+W`, "aria-label": words }
    )
    return this.pillEnd(fab, s, running)
  }

  /** pillEnd finishes the pill (D1): the current turn's cost — its
   * tokens in→out once the run has finished, "—" while unknown — the
   * z-index, the click. */
  private pillEnd(fab: HTMLElement, s: PanelState, running?: RunRow): HTMLElement {
    const r = running ?? [...s.turns, ...[...s.experiments.values()].flat()].find((x) => x.id === s.selected)
    if (r) {
      const known = usageKnown(r.status)
      fab.appendChild(
        el("span", "weft-fab-cost", ` · ${known ? `${tokens(r.usage.input_tokens)}→${tokens(r.usage.output_tokens)}` : "—"}`, {
          title: known ? "the current turn's tokens, input→output" : "the current turn's usage: known when it finishes",
        })
      )
    }
    // Named with its visible words (label in name, WCAG 2.5.3).
    if (!running) fab.setAttribute("aria-label", `weft ${fab.textContent}`)
    if (this.cfg.zIndex) fab.style.zIndex = this.cfg.zIndex
    on(fab, "click", () => this.toggle())
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
    for (const [k, v] of SHORTCUTS) {
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
      on(how, "click", () => {
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
        href: studioLink(this.base, s.selected, this.stepNow(s)),
        target: "_blank",
        rel: "noopener",
        title: "open in Studio (run, and the step you are reading)",
        "aria-label": "open in Studio",
      })
      a.style.textDecoration = "none"
      h.appendChild(a)
    }
    const isRaw = this.lay.tab === "raw"
    const raw = el("button", `weft-btn${isRaw ? " weft-active" : ""}`, "raw", {
      title: "the JSON, one keypress away (r)",
      "aria-pressed": String(isRaw),
    })
    on(raw, "click", () => this.toggleRaw())
    h.appendChild(raw)
    const where = this.lay.mode === "float" ? "float" : `dock ${this.lay.side}`
    const lay = el("button", "weft-btn weft-layout", "⇆", { title: `layout: ${where} — next (Alt+Shift+W)`, "aria-label": `layout: ${where}` })
    on(lay, "click", () => this.cycle())
    h.appendChild(lay)
    h.appendChild(this.themeButton())
    const close = el("button", "weft-btn", "–", { title: "collapse (Alt+W)", "aria-label": "collapse" })
    on(close, "click", () => this.toggle())
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
    const opt = (label: string, value: string, sel1: boolean, key: string) => {
      const o = el("option", undefined, label, { value, "data-key": key }) as HTMLOptionElement
      o.selected = sel1
      sel.appendChild(o)
      return o
    }
    const dot = s.live ? "● " : "○ "
    if (!listed) opt(`${dot}${cur || "latest (dev)"} · not on the page`, "", true, "\u0000").disabled = true
    list.forEach((c, i) => {
      const { publicId, session, flow } = c.scope
      const label = [publicId, session && `session ${session}`, flow && `flow ${flow}`, c.source].filter(Boolean).join(" · ")
      opt(c.key === cur ? dot + label : label, String(i), c.key === cur, c.key).setAttribute("data-weft-source", c.source)
    })
    on(sel, "change", (_, n) => {
      const v = (n as HTMLSelectElement).value
      const c = v ? list.at(Number(v)) : undefined
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
    else if (!this.model?.publicId && s.devAgent)
      list.appendChild(el("div", "weft-note weft-dev-poll", `live: agent ${s.devAgent} · the other agents' runs every ${DEV_DISCOVERY_MS / 1000} s`))
    if (!s.turns.length && !s.experiments.size) {
      const empty = session
        ? `no turns of session ${session} yet`
        : this.model?.publicId
          ? "no turns yet — run your app"
          : "no runs yet (dev)"
      list.appendChild(el("div", "weft-splash", empty))
      return list
    }
    // The filter (D4): text over the id, the error and the prompts the
    // panel has read; a status; has error. Applied to the loaded rows.
    list.appendChild(this.turnFilter())
    const turns = s.turns.filter((r) => this.match(r))
    const filtered = this.filtering()
    if (filtered)
      list.appendChild(
        el("div", "weft-tq-n", `${turns.length} of ${s.turns.length} ${s.turnsCapped ? "loaded" : "turns"}${s.turnsCapped ? " · the filter applies to the loaded turns" : ""}`)
      )
    // One list (D3): a listitem per run, keyed by its id; one row
    // tabbable (the roving tabindex — the row last focused, else the
    // selected one, else the first), arrow keys move it.
    const ids = [...turns, ...[...s.experiments.values()].flat()].map((r) => r.id)
    const tab = ids.includes(this.rove) ? this.rove : ids.includes(s.selected) ? s.selected : ids[0]
    const items = el("div", "weft-rows", undefined, { role: "list", "aria-label": "turns" })
    const row = (r: RunRow) => this.turnRow(r, s.selected, tab)
    const shown = new Set<string>(s.turns.map((r) => r.id))
    for (const r of turns) {
      items.appendChild(row(r))
      // The experiment slot (§2): runs that forked from this turn nest
      // under it; empty without the playground capability.
      const expts = s.experiments.get(r.id) ?? []
      if (expts.length) items.appendChild(el("div", "weft-expts", expts.map(row), { "data-key": `x:${r.id}` }))
    }
    // Experiments whose source turn is not in the list (older than the
    // page, or a run of another experiment) are still runs of this
    // conversation: listed, not dropped.
    const orphans: RunRow[] = []
    for (const [key, rows] of s.experiments) if (!shown.has(key)) orphans.push(...rows.filter((r) => this.match(r)))
    if (orphans.length) items.appendChild(el("div", "weft-expts", orphans.map(row), { "data-key": "x:" }))
    list.appendChild(items)
    // Paging (D4): the sentinel loads the next older page when the list
    // scrolls to it (watchOlder), or on a click; no cap.
    if (s.turnsCapped && this.model?.publicId) {
      const more = el("button", "weft-btn weft-older", s.loadingOlder ? "loading older turns…" : "older turns ↓", {
        type: "button",
        title: "load the next older page of turns",
        // Keyed by the cursor: each page read (even one that added no
        // row) is a new sentinel, so the observer is armed again.
        "data-key": `older:${s.olderAt}`,
      })
      on(more, "click", () => this.go(this.model?.loadOlder()))
      list.appendChild(more)
    } else if (s.turnsCapped) list.appendChild(el("div", "weft-note", `the newest ${DEV_LIMIT} runs — older ones are in Studio (⤢)`))
    else if (s.paged) list.appendChild(el("div", "weft-tq-n weft-all", `all ${s.turns.length} turns loaded`))
    return list
  }

  /** turnFilter is the box above the turn list (D4): free text, a
   * status, has error — this session only, never stored. */
  private turnFilter(): HTMLElement {
    const f = this.tq
    const box = el("div", "weft-tq", undefined, { "data-key": "tq" })
    const q = el("input", "weft-input weft-turn-q", undefined, {
      type: "search",
      "aria-label": "filter turns",
      placeholder: "/ filter turns",
    }) as HTMLInputElement
    q.value = f.text
    on(q, "input", (_, n) => {
      f.text = (n as HTMLInputElement).value
      this.render(this.last)
    })
    const st = el("select", "weft-input weft-tq-status", undefined, { "aria-label": "status" }) as HTMLSelectElement
    for (const v of ["", "running", "succeeded", "failed", "parked"]) {
      const o = el("option", undefined, v || "any status", { value: v }) as HTMLOptionElement
      o.selected = v === f.status
      st.appendChild(o)
    }
    st.value = f.status
    on(st, "change", (_, n) => {
      f.status = (n as HTMLSelectElement).value
      this.render(this.last)
    })
    const err = el("input", "weft-tq-err", undefined, { type: "checkbox" }) as HTMLInputElement
    err.checked = f.err
    on(err, "change", (_, n) => {
      f.err = (n as HTMLInputElement).checked
      this.render(this.last)
    })
    box.append(q, st, el("label", "weft-tool", [err, document.createTextNode("has error")]))
    return box
  }

  /** filtering: the turn filter narrows anything. */
  private filtering(): boolean {
    return this.tq.text.trim() !== "" || !!this.tq.status || this.tq.err
  }

  /** match is the turn filter's test of one row. */
  private match(r: RunRow): boolean {
    const f = this.tq
    if (f.status && statusChip(r) !== f.status) return false
    if (f.err && !r.err && r.status !== "failed") return false
    const q = f.text.trim().toLowerCase()
    return !q || `${r.id}\n${r.err}\n${this.prompts.get(r.id) ?? ""}`.toLowerCase().includes(q)
  }

  /** watchOlder observes the older-turns sentinel (D4): one passive
   * IntersectionObserver over the turn column, moved to each new
   * sentinel (one per page, so a list still short of the column keeps
   * loading); disconnected with the element. No observer: the
   * sentinel is a button. */
  private watchOlder() {
    if (!this.isConnected) {
      this.io?.disconnect()
      this.io = this.ioAt = null
      return
    }
    const n = this.body.querySelector(".weft-older")
    // Narrow, the rows are a dropdown; filtered, a scroll would read
    // page after page for rows the filter may hide: the sentinel is a
    // button only.
    const want = !!n && typeof IntersectionObserver === "function" && !n.closest(".weft-narrow") && !this.filtering()
    if (n === this.ioAt && !!this.io === want) return
    this.io?.disconnect()
    this.io = null
    this.ioAt = n
    if (!n || !want) return
    try {
      this.io = new IntersectionObserver(
        (es) => {
          if (es.some((e) => e.isIntersecting)) this.go(this.model?.loadOlder())
        },
        { root: n.closest(".weft-turns") }
      )
      this.io.observe(n)
    } catch {
      this.io = null
    }
  }

  private turnRow(r: RunRow, selected: string, tab: string): HTMLElement {
    const chip = statusChip(r)
    const row = el("button", `weft-turn${r.id === selected ? " weft-sel" : ""}`, undefined, {
      type: "button",
      tabindex: r.id === tab ? "0" : "-1",
      ...(r.id === selected ? { "aria-current": "true" } : {}),
    })
    const row1 = el("div", "weft-row1", [
      el("span", `weft-chip weft-${chip}`, chip),
      el("span", "weft-id", r.id, { title: r.id }),
      // What the record says this turn is (D5): scripted, a fork, an
      // experiment of a turn the list names.
      ...turnChips(r, (id) => {
        const src = this.rowOf(id)
        return src?.turn ? `t${src.turn}` : shortId(id)
      }),
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
    on(row, "click", () => this.go(this.model?.select(r.id, true)))
    on(row, "focus", (_, n) => this.roveTo(n, r.id))
    return el("div", undefined, [row, ...this.sourceLink(r)], { role: "listitem", "data-key": r.id })
  }

  /** sourceLink is an experiment's way back (plan F1): its source turn
   * and step (forked_from = "<src>#<from_step>", the step ordinal) —
   * selected in the panel when the conversation holds it (select()'s
   * rule), and on Studio's run page (⤢) when the list does not. Beside
   * the row, not in it: a button cannot hold another. */
  private sourceLink(r: RunRow): HTMLElement[] {
    if (!r.forked_from) return []
    const [src, at = ""] = r.forked_from.split("#")
    const n = /^\d+$/.test(at) ? Number(at) : undefined
    const where = n === undefined ? "" : ` step ${n}`
    const known = this.rowOf(src)
    const name = `${known?.turn ? `t${known.turn}` : shortId(src)}${where}`
    const n2 = el(known ? "button" : "a", "weft-btn weft-src", `↖ ${name}${known ? "" : " ⤢"}`, {
      "aria-label": `${known ? "go to" : "open in Studio"} the source: run ${src}${where}`,
      "data-weft-source": r.forked_from,
      ...(known ? { type: "button" } : { href: href(this.base, runLink(src, { step: n })), target: "_blank", rel: "noopener" }),
    })
    if (known) on(n2, "click", () => this.select(src, n))
    else n2.style.textDecoration = "none"
    return [n2]
  }

  /** roveTo makes one turn row the list's tabbable one (D3). */
  private roveTo(n: HTMLElement, id: string) {
    this.rove = id
    for (const b of Array.from(this.body.querySelectorAll<HTMLElement>(".weft-turn"))) b.tabIndex = b === n ? 0 : -1
  }

  private main(s: PanelState): HTMLElement {
    const main = el("div", "weft-main")
    // One delegated listener for every <details> the turn view draws.
    // toggle does not bubble, so it is heard on the way down
    // (capture). The open state lives outside the DOM — the model's
    // for the lazy subagent loader (Dv3: [data-weft-child]), openKeys
    // for the rest — so a re-render never collapses what the user
    // opened.
    on(
      main,
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
        t.querySelector("summary")?.setAttribute("aria-expanded", String(isOpen))
        const turn = this.model?.state.turn
        if (!turn) return
        if (isOpen) {
          turn.expanded.add(child)
          this.go(this.model?.expandChild(child))
        } else {
          turn.expanded.delete(child)
          turn.tried.delete(child) // reopening asks again
          turn.unreachable.delete(child)
        }
      },
      true
    )
    // A step card click marks the step the user is reading — what ⤢
    // carries into Studio (Dv3).
    on(main, "click", (e) => {
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
    // The turn's tabs (D4): Story (the step story and the playground),
    // Request (E1.2 fills it), Timeline, Raw.
    main.appendChild(this.tabs())
    const tab = this.lay.tab
    const t = s.turn
    const panel = (tp: HTMLElement, id: string) => {
      const at = { class: "weft-tp", role: "tabpanel", id: `weft-tp-${id}`, "aria-labelledby": `weft-tab-${id}`, "data-key": `tp:${id}` }
      for (const [k, v] of Object.entries(at)) tp.setAttribute(k, v)
      main.appendChild(tp)
      return tp
    }
    // The story's own box is its panel (its notes stay its children),
    // drawn on every tab and hidden on the others: its node — the
    // <details> opened, its scroll — stays across a tab switch.
    const story = this.turnView(s)
    story.appendChild(this.playgroundArea(s))
    if (tab !== "story") story.setAttribute("hidden", "")
    panel(story, "story")
    if (tab !== "story")
      panel(
        el("div", undefined, [
          tab === "raw" ? this.rawView(t) : tab === "timeline" ? renderTimeline(t) : this.requestView(s, t),
        ]),
        tab
      )
    return main
  }

  /** tabs is the turn view's tablist (D4): ARIA tabs, one tab stop,
   * ←/→ (Home/End) move and select. */
  private tabs(): HTMLElement {
    const bar = el("div", "weft-tabs", undefined, { role: "tablist", "aria-label": "turn views", "data-key": "tabs" })
    for (const id of TABS) {
      const sel = id === this.lay.tab
      const b = el("button", `weft-tab${sel ? " weft-active" : ""}`, id[0].toUpperCase() + id.slice(1), {
        type: "button",
        role: "tab",
        id: `weft-tab-${id}`,
        "aria-selected": String(sel),
        tabindex: sel ? "0" : "-1",
        "data-key": `tab:${id}`,
        // Only the drawn panel exists to be controlled.
        ...(sel ? { "aria-controls": `weft-tp-${id}` } : {}),
      })
      on(b, "click", () => this.setTab(id))
      bar.appendChild(b)
    }
    on(bar, "keydown", (e) => {
      const k = (e as KeyboardEvent).key
      const i = TABS.indexOf(this.lay.tab)
      const n = TABS.length
      const j = k === "ArrowRight" ? i + 1 : k === "ArrowLeft" ? i - 1 : k === "Home" ? 0 : k === "End" ? n - 1 : null
      if (j === null) return
      e.preventDefault()
      const id = TABS[(j + n) % n]
      this.setTab(id)
      this.body.querySelector<HTMLElement>(`#weft-tab-${id}`)?.focus()
    })
    return bar
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
    if (s.result && s.result.under === here) box.appendChild(this.experimentResult(s))
    if (!this.canAct(s)) return box
    if (s.drawer && (s.drawer.under ?? s.drawer.runId) === here) box.appendChild(this.drawer(s))
    return box
  }

  /** canAct: the write verbs are drawn when the server reports the
   * playground AND the page's token may use them — a read-scoped
   * panel token (§6: read-only unless minted with "playground": true)
   * is refused on every one, so they are not offered. lib/replay.ts's
   * canReplay: Studio's replay verbs gate on the same rule. */
  private canAct(s: PanelState): boolean {
    return canReplay(s.meta?.capabilities ?? [], this.cfg.token)
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
    const child = d.runId !== (d.under ?? d.runId)
    const head = el("div", "weft-step-h", [
      el("span", undefined, `Experiment · ${d.agent}${child ? ` · child run ${d.runId}` : ""}${d.step > 0 ? ` · continue from step ${d.step}` : ""}`),
      el("span", "weft-grow"),
    ])
    const close = el("button", "weft-btn", "–", { title: "close the drawer", "aria-label": "close the drawer" })
    on(close, "click", () => this.closeDrawer())
    head.appendChild(close)
    card.appendChild(head)
    const body = el("div", "weft-step-b")
    // The verb that opened it (plan F1): Studio's replay drawer title.
    if (d.verb) body.appendChild(el("div", "weft-verb-t", VERB_TITLES[d.verb], { "data-weft-drawer-verb": d.verb }))
    // The field the verb opens on gets focus once per opening (draw()).
    // Focus moves into the drawer once per opening: the verb's field,
    // else the prompt.
    if (d.key !== undefined && d.key !== this.focusedKey) {
      this.focusedKey = d.key
      const e = d.edits.at(0)
      this.focusNext = d.focus === "edit" && e ? `edit:${e.step}:${e.callID}` : d.focus === "input" ? "input" : "prompt"
    }

    // System prompt, with the registered config one reset away.
    const promptRow = el("label", "weft-field", [el("span", undefined, "System prompt")])
    const ta = this.field(el("textarea", "weft-input") as HTMLTextAreaElement, "prompt")
    ta.rows = 3
    ta.value = d.instructions
    on(ta, "input", (_, n) => this.model?.setDraft({ instructions: (n as HTMLTextAreaElement).value }, true))
    const reset = el("button", "weft-btn", "↺", { title: "reset to the registered prompt", "aria-label": "reset to the registered prompt" })
    on(reset, "click", () => {
      this.model?.setDraft({ instructions: d.registeredInstructions })
    })
    promptRow.append(ta, reset)
    body.appendChild(promptRow)
    // edit the prompt: the step's own system text when its request
    // record holds it whole, else the registered prompt — said.
    if (d.promptFrom === "registered") {
      const note = el("div", "weft-note", undefined, { "data-weft-prompt-from": "registered" })
      if (d.promptHole) note.append(badge(d.promptHole.hole, d.promptHole), " ")
      note.append("step prompt unreadable · the registered one pre-filled")
      body.appendChild(note)
    }

    // Tools: turning off is narrowing; ⚠ marks the side-effect class.
    if (agent?.tools.length) {
      const toolsRow = el("div", "weft-field", [el("span", undefined, "Tools")])
      for (const t of agent.tools) {
        const cb = el("input") as HTMLInputElement
        cb.type = "checkbox"
        cb.checked = d.tools[t.name] ?? true
        on(cb, "change", (_, n) =>
          this.model?.setDraft({
            tools: { ...(this.model.state.drawer?.tools ?? d.tools), [t.name]: (n as HTMLInputElement).checked },
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
    const modelSel = el("select", "weft-input", undefined, { "aria-label": "model" }) as HTMLSelectElement
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
    on(modelSel, "change", (_, n) => this.model?.setDraft({ model: (n as HTMLSelectElement).value }))
    opts.appendChild(modelSel)
    const thinkSel = el("select", "weft-input", undefined, { "aria-label": "thinking" }) as HTMLSelectElement
    const defOpt = el("option", undefined, "thinking: default") as unknown as HTMLOptionElement
    defOpt.value = ""
    thinkSel.appendChild(defOpt)
    for (const lvl of ["off", "low", "medium", "high"]) {
      const o = el("option", undefined, lvl) as unknown as HTMLOptionElement
      o.value = lvl
      thinkSel.appendChild(o)
    }
    thinkSel.value = d.thinking
    on(thinkSel, "change", (_, n) => this.model?.setDraft({ thinking: (n as HTMLSelectElement).value }))
    opts.appendChild(thinkSel)
    body.appendChild(opts)

    // Input replaces the turn's user message (a whole-turn re-run).
    if (d.step === 0) {
      const inputRow = el("label", "weft-field", [el("span", undefined, "Input (replaces the user message)")])
      const inTa = this.field(el("textarea", "weft-input") as HTMLTextAreaElement, "input")
      inTa.rows = 2
      inTa.value = d.input
      if (d.sourceInput && d.thread !== "fork") inTa.placeholder = d.sourceInput
      on(inTa, "input", (_, n) => {
        const v = (n as HTMLTextAreaElement).value
        this.model?.setDraft({ input: v }, true)
        // A fork's Run waits for its message (no redraw while typing).
        const btn = n.closest(".weft-drawer")?.querySelector<HTMLButtonElement>(".weft-run-btn")
        if (btn) btn.disabled = btn.hasAttribute("data-weft-held") || (this.model?.state.drawer?.thread === "fork" && !v.trim())
      })
      inputRow.appendChild(inTa)
      body.appendChild(inputRow)
    }

    // The side-effect mode (§6 rule 3): substitute (the default — a
    // recorded side-effect call answers from the record, a miss parks),
    // park (every side-effect call waits), allow (the tools the app
    // opted in with AllowSideEffects run for real — only here; a
    // ReplaySafe tool runs in every mode).
    const seRow = el("div", "weft-fields")
    const seSel = el("select", "weft-input", undefined, { "aria-label": "side effects" }) as HTMLSelectElement
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
    on(seSel, "change", (_, n) => this.model?.setDraft({ sideEffects: (n as HTMLSelectElement).value as ExperimentDraft["sideEffects"] }))
    seRow.appendChild(seSel)
    // The engine (§5.5): live, or scripted — the source run's recorded
    // turns at zero tokens, refused with an instructions/model
    // override (the prompt trap).
    const engSel = el("select", "weft-input", undefined, { "aria-label": "engine" }) as HTMLSelectElement
    const engLive = el("option", undefined, "engine: live") as unknown as HTMLOptionElement
    engLive.value = "live"
    engSel.appendChild(engLive)
    const engScripted = el("option", undefined, "scripted (zero tokens)") as unknown as HTMLOptionElement
    engScripted.value = "scripted"
    engSel.appendChild(engScripted)
    engSel.value = d.engine
    on(engSel, "change", (_, n) => this.model?.setDraft({ engine: (n as HTMLSelectElement).value as ExperimentDraft["engine"] }))
    seRow.appendChild(engSel)
    // Fork mode (§5.4, review fix 4a — P4 renders in both surfaces):
    // ephemeral (an experiment, never a turn) or fork (a new session
    // with lineage, the input its next turn). Fork needs an input —
    // the runtime refuses the command otherwise.
    const thrSel = el("select", "weft-input", undefined, { "aria-label": "thread" }) as HTMLSelectElement
    const thrEphemeral = el("option", undefined, "thread: ephemeral") as unknown as HTMLOptionElement
    thrEphemeral.value = "ephemeral"
    thrSel.appendChild(thrEphemeral)
    const thrFork = el("option", undefined, "fork (new session)") as unknown as HTMLOptionElement
    thrFork.value = "fork"
    thrSel.appendChild(thrFork)
    thrSel.value = d.thread
    thrSel.title = "fork continues the conversation in a new session (needs an input)"
    on(thrSel, "change", (_, n) => {
      const thread = (n as HTMLSelectElement).value as ExperimentDraft["thread"]
      // A fork continues the conversation: it starts at step 0.
      this.model?.setDraft(thread === "fork" ? { thread, step: 0 } : { thread })
    })
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
        on(cb, "change", (_, box) => {
          const set = this.model?.state.breakpoints ?? s.breakpoints
          const next = set.filter((n) => n !== t.name)
          if ((box as HTMLInputElement).checked) next.push(t.name)
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
    let unplaced = 0
    // The run the drawer replays: the turn, or a child of it (A10).
    const src = s.turn ? (d.runId === s.turn.id ? s.turn : s.turn.children.get(d.runId)) : undefined
    if (d.step > 0 && src) {
      const draft = (): ExperimentDraft => this.model?.state.drawer ?? d
      const editsBox = el("div", "weft-field")
      editsBox.appendChild(el("span", undefined, `Transcript edits (steps 0..${d.step - 1} are kept)`))
      // Steps are numbered the way from_step and the edits count them
      // on the wire: by the step's own ordinal (its stored index).
      const placed = new Set<string>()
      for (const step of src.folded.steps) {
        const n = step.index
        if (n >= d.step) continue
        for (const call of step.toolCalls) {
          if (!call.result) continue
          const lab = el("label", "weft-edit", undefined, { "data-key": `${n}:${call.callId}` })
          lab.appendChild(el("span", undefined, `step ${n} · ${call.name} →`))
          const inp = this.field(
            el("input", "weft-input") as HTMLInputElement,
            `edit:${n}:${call.callId}`
          )
          inp.placeholder = String(call.result.content).slice(0, 60)
          const editOf = () => draft().edits.find((e) => e.step === n && e.callID === call.callId)
          inp.value = editOf()?.toolResult ?? ""
          on(inp, "input", (_, f) => {
            const v = (f as HTMLInputElement).value
            const cur = editOf()
            const next = [...draft().edits]
            const i = cur ? next.indexOf(cur) : -1
            if (v === "") {
              if (i >= 0) next.splice(i, 1)
            } else if (i >= 0) {
              next[i] = { ...cur, toolResult: v, step: n, callID: call.callId }
            } else {
              next.push({ step: n, callID: call.callId, toolResult: v })
            }
            this.model?.setDraft({ edits: next }, true)
          })
          lab.appendChild(inp)
          editsBox.appendChild(lab)
          placed.add(`${n}:${call.callId}`)
        }
        if (step.text && !step.toolCalls.length) {
          const lab = el("label", "weft-edit", undefined, { "data-key": `${n}` })
          lab.appendChild(el("span", undefined, `step ${n} · reply`))
          const reply = this.field(
            el("textarea", "weft-input") as HTMLTextAreaElement,
            `edit:${n}`
          )
          reply.rows = 2
          const editOf = () => draft().edits.find((e) => e.step === n && !e.callID)
          reply.value = editOf()?.content ?? ""
          on(reply, "input", (_, f) => {
            const v = (f as HTMLTextAreaElement).value
            const cur = editOf()
            const next = [...draft().edits]
            const i = cur ? next.indexOf(cur) : -1
            if (v === "") {
              if (i >= 0) next.splice(i, 1)
            } else if (i >= 0) {
              next[i] = { ...cur, content: v, step: n }
            } else {
              next.push({ step: n, content: v })
            }
            this.model?.setDraft({ edits: next }, true)
          })
          lab.appendChild(reply)
          editsBox.appendChild(lab)
          placed.add(`${n}:`)
        }
      }
      // A pre-filled edit is never dropped silently: one whose call
      // the steps as read do not hold is shown, badged, and holds Run.
      for (const e of d.edits) {
        if (placed.has(`${e.step}:${e.callID ?? ""}`)) continue
        unplaced++
        editsBox.appendChild(
          el("div", "weft-edit", [badge("gap"), document.createTextNode(` step ${e.step} · ${e.callID ?? "reply"} → ${e.toolResult ?? e.content ?? ""} · not in the steps read · Run held`)], {
            "data-key": `u:${e.step}:${e.callID ?? ""}`,
            "data-weft-unplaced": "",
          })
        )
      }
      if (editsBox.childElementCount > 1) body.appendChild(editsBox)
    }

    // The ack preview (plan F1, ack-before-execute): what each tool's
    // calls would do, and which prefix is kept — redrawn with the draft.
    const refused = this.ack(s, d, src, agent, body)
    const run = el("button", "weft-run-btn", "Run experiment ▶", {
      title: "POST /api/playground/runs — the runtime in your app executes it",
    })
    if (refused || unplaced) run.setAttribute("data-weft-held", "")
    if (refused || unplaced || (d.thread === "fork" && !d.input.trim())) run.setAttribute("disabled", "")
    on(run, "click", () => this.go(this.model?.runExperiment()))
    body.appendChild(run)
    card.appendChild(body)
    return card
  }

  /** closeDrawer closes the experiment drawer and gives focus back to
   * the verb that opened it (the close button, Escape inside it). */
  private closeDrawer() {
    const back = this.opener
    this.opener = null
    this.model?.closeExperiment()
    if (back?.isConnected) back.focus({ preventScroll: true })
  }

  /** ack draws the drawer's ack preview (plan F1; the run page's
   * ReplayAck): the prefix line and, per tool of the step's catalog
   * (the request record's replay classes; a hole badged and the
   * registered tools judged instead), whether its calls run, are
   * substituted or park, and why — lib/replay.ts's words. Returns
   * whether the server would refuse the command (side effects allow
   * over a tool not opted in): Run is then held. */
  private ack(s: PanelState, d: ExperimentDraft, src: TurnView | ChildView | undefined, agent: AgentView | undefined, into: HTMLElement): boolean {
    const box = el("div", "weft-ack", undefined, { "data-weft-ack": "", "data-key": "ack" })
    box.appendChild(el("div", "weft-name", "before you run"))
    const from = d.thread === "fork" ? 0 : d.step
    const compacted = from > 0 && compactionsOf(src?.doc).some((c) => !isSessionMarker(c) && typeof c.step === "number" && c.step <= from)
    box.appendChild(el("div", "weft-res", prefixLine(from, compacted), { "data-weft-prefix": "" }))
    // The replayed run not read (a child whose history did not load):
    // its catalog is the registration's, said.
    const status = src && "status" in src ? src.status : (this.rowOf(d.runId)?.status ?? src?.doc?.status ?? "")
    const cat = src
      ? catalogAt(src.requests, from, hasCapability(s, "requests"), boundsOf(src.transcript)?.stepCount ?? null, status === "running")
      : { tools: null, hole: catalogReadError("the run's records could not be read") }
    if (cat.hole) box.appendChild(ackCatalogHole(cat.hole, !!agent))
    const names = Object.keys(d.tools)
    const kept = names.filter((n) => d.tools[n])
    const verdicts = replayVerdicts({
      catalog: cat.tools ?? (agent?.tools ?? []).map((t): CatalogTool => ({ name: t.name, replay: t.side_effects === "safe" ? "safe" : "never", approval: false })),
      agent,
      mode: d.sideEffects,
      toolsEnabled: kept.length < names.length ? kept : undefined,
      breakpoints: breakpointsFor({ breakpoints: s.breakpoints }, agent),
    })
    if (cat.loading) box.appendChild(el("div", "weft-reason", "reading the step's catalog…"))
    else if (cat.none) box.appendChild(el("div", "weft-reason", "the step offered no tools · nothing to substitute or park"))
    else {
      const list = el("ul", "weft-verdicts", undefined, { "aria-label": "what each tool would do" })
      for (const v of verdicts)
        list.appendChild(
          el("li", `weft-v-${v.verdict}`, [el("span", "weft-name", v.name), el("span", "weft-v", v.verdict), el("span", "weft-reason", v.why)], {
            "data-verdict": v.verdict,
            "data-tool": v.name,
            "data-key": v.name,
          })
        )
      // Beside a hole the badge stands alone: an empty stand-in list is
      // not "no tools".
      if (!verdicts.length && !cat.hole) list.appendChild(el("li", "weft-reason", "no tools · nothing to substitute or park"))
      if (list.childElementCount) box.appendChild(list)
    }
    into.appendChild(box)
    // The server's rule is over the registered tools left on, whatever
    // the step offered: allow runs only ReplaySafe tools and the ones
    // the app opted in (AllowSideEffects) — any other refuses it.
    const refused = allowRefusals(agent, d.sideEffects, kept.length < names.length ? kept : undefined)
    if (refused.length) {
      const one = refused.length === 1
      into.appendChild(
        el(
          "div",
          "weft-note weft-warn",
          `side effects allow is refused while ${refused.join(", ")} ${one ? "is" : "are"} on (allow runs only ReplaySafe tools and those opted in with AllowSideEffects): turn ${one ? "it" : "them"} off, or pick substitute or park`,
          { role: "alert", "data-weft-refused": "" }
        )
      )
    }
    // Ack before execute: no Run while the verdicts are still unread.
    return refused.length > 0 || !!cat.loading
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
    on(keep, "click", () => {
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
    const link = (a: HTMLElement) => {
      const draft = this.model?.state.drawer ?? null
      const mine = draft && draft.runId === r.sourceRunID ? draft : null
      // The step Studio continues from: the drawer's own, else the one
      // being read — as from_step counts it.
      // The drawer's own step (a re-run stays one, a child's is the
      // child's); the step being read only without a drawer.
      const step = mine ? mine.step : s.turn ? readStep(s.turn.folded, s.selectedStep) : -1
      a.setAttribute("href", studioPlaygroundLink(this.base, mine, step))
    }
    link(compare)
    compare.setAttribute("target", "_blank")
    compare.setAttribute("rel", "noopener")
    for (const ev of ["pointerdown", "focus", "click", "contextmenu"]) on(compare, ev, (_, a) => link(a))
    compare.style.textDecoration = "none"
    head.appendChild(compare)
    const discard = el("button", "weft-btn", "discard", { title: "clear the result pane" })
    on(discard, "click", () => this.model?.discardResult())
    head.appendChild(discard)
    card.appendChild(head)
    const body = el("div", "weft-step-b")
    if (r.error) body.appendChild(el("div", "weft-note weft-warn", r.error))
    // The link back (plan F1): the new run, the source step it replays.
    if (r.runID) {
      const src = r.sourceRunID
      const line = el("div", "weft-row2", [el("span", undefined, `${shortId(r.runID)} · replay of`)], {
        "data-weft-replay-of": `${src}#${r.fromStep}`,
        "data-key": "replay-of",
      })
      const words = `${shortId(src)} from step ${r.fromStep}`
      if (src === r.under) {
        const b = el("button", "weft-btn", words, {
          type: "button",
          "aria-label": `go to the source: run ${src} step ${r.fromStep}`,
          "data-weft-source": `${src}#${r.fromStep}`,
        })
        on(b, "click", () => this.select(src, r.fromStep))
        line.appendChild(b)
      } else line.appendChild(el("span", undefined, words))
      const mk = (text: string, url: string, label: string, attr: string) => {
        const a = el("a", "weft-btn", text, { href: url, target: "_blank", rel: "noopener", "aria-label": label, [attr]: "" })
        a.style.textDecoration = "none"
        return a
      }
      line.appendChild(mk("⤢", href(this.base, runLink(src, { step: r.fromStep })), `open the source step in Studio (run ${src} step ${r.fromStep})`, "data-weft-source-link"))
      line.appendChild(mk("open the replayed run ⤢", href(this.base, runLink(r.runID)), `open the replayed run ${r.runID} in Studio`, "data-weft-run-link"))
      body.appendChild(line)
    }
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
      const cmp = el("select", "weft-input", undefined, { "aria-label": "compare with" }) as HTMLSelectElement
      for (const sb of siblings) {
        const o = el("option", undefined, `compare vs ${sb.label || "source"}`) as unknown as HTMLOptionElement
        o.value = sb.id
        cmp.appendChild(o)
      }
      cmp.value = r.compareWith
      on(cmp, "change", (_, n) => this.go(this.model?.setCompare((n as HTMLSelectElement).value)))
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
      on(input, "input", (_, n) => this.scratch.set("steer", (n as HTMLInputElement).value))
      const send = el("button", "weft-btn", "steer", { title: "POST /api/runs/{id}/steer (ADR 0019)" })
      on(send, "click", (_, btn) => {
        const field = btn.parentElement?.querySelector<HTMLInputElement>("input")
        if (!field?.value) return
        this.go(this.model?.steer(field.value))
        this.scratch.delete("steer")
        field.value = ""
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
      const line = el("div", "weft-call", undefined, { "data-key": call.id })
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
      on(paste, "input", (_, n) => this.scratch.set(key, (n as HTMLInputElement).value))
      const mk = (label: string, title: string, act: (b: HTMLElement) => void) => {
        const b = el("button", "weft-btn", label, { title })
        on(b, "click", (_, n) => act(n))
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
        mk("resolve…", "Resolve: the model sees the result typed here; the handler never runs", (b) => {
          const field = b.parentElement?.querySelector<HTMLInputElement>(".weft-resolve")
          if (!field?.value) {
            field?.focus()
            return
          }
          this.go(this.model?.decide(call.id, "resolve", field.value))
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
    const prompt = turnPromptOf(t.transcript)
    if (prompt) capSet(this.prompts, t.id, prompt, RUN_MEMO_LIMIT)
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
        {
          endpoint: this.base,
          runId: t.id,
          ...(this.canAct(s)
            ? {
                replay: {
                  runId: t.id,
                  agent: row?.agent,
                  max: boundsOf(t.transcript)?.max ?? null,
                  // A session's top-level turn only (a child adopted by
                  // select() is no conversation to fork).
                  fork: !!(row?.session_id || t.doc?.session_id) && !(row?.parent_run_id || t.doc?.parent_run_id),
                  open: (runId, draft, agent, from) => {
                    this.opener = from
                    this.go(this.model?.openExperiment(runId, draft, { agent, under: t.id }))
                  },
                },
              }
            : {}),
        }
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
   * drawer as the rerun() draft — the whole turn, its current edits
   * kept — posted only by Run after the ack preview), ⎇ Continue from
   * the step being read. */
  private actions(s: PanelState): HTMLElement {
    const t = s.turn
    if (!t) return el("div")
    const row = el("div", "weft-actions")
    const experiment = el("button", "weft-btn", "✎ Experiment", {
      title: "open the experiment drawer, pre-filled from the registered config",
    })
    on(experiment, "click", () => this.go(this.model?.openExperiment(t.id, 0)))
    row.appendChild(experiment)
    const again = el("button", "weft-btn", "↻ Re-run", {
      title: "re-run the whole turn: the drawer from step 0, with its current edits — Run after the ack",
    })
    on(again, "click", () => this.go(this.model?.rerun(t.id)))
    row.appendChild(again)
    // The step being read, by its ordinal (from_step's count).
    const from = readStep(t.folded, s.selectedStep)
    if (from > 0) {
      const cont = el("button", "weft-btn", `⎇ Continue from step ${from}`, {
        title: "keep the transcript through the previous step (edits apply) and run this step fresh",
      })
      on(cont, "click", () => this.go(this.model?.openExperiment(t.id, replayFromStep(from))))
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
    for (const m of holes) box.appendChild(holeLine(m))
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
      const line = el("div", "weft-call", undefined, { "data-key": call.id })
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
    // The content line (D5): on or off and why, the recorder's cuts
    // counted — the shared table's words in its title.
    // Said only on evidence (nothing when unknown); the title's words
    // ride in the accessible text too.
    const cap = capLine(s.turn, s.meta, MAX_EVENT_PAGES * 500)
    if (cap)
      parts.push(
        el("span", "weft-cap", [document.createTextNode(` · ${cap.text}`), el("span", "weft-sr", ` — ${cap.title}`)], {
          title: cap.title,
          "data-weft-cap": cap.hole ?? "",
        })
      )
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
    return el("div", "weft-footer", parts, { "data-key": "foot" })
  }

  /** rawView is the Raw tab (D4): the turn's records as one JSON tree
   * — {doc, events, transcript, spans}, and requests when the panel
   * holds them — built once per change of what it holds. */
  private rawView(t: TurnView): HTMLElement {
    if (this.treeFor !== t.id) {
      this.tree = { ...newTree(), q: this.tree.q, applied: this.tree.applied }
      this.treeFor = t.id
    }
    const r = t.requests
    // The events array grows in place: its length says it changed.
    const key = [t.id, t.doc, t.events.length, t.transcript, t.spans, r]
    const m = this.rawMemo
    if (!m || m.key.some((v, i) => v !== key[i]))
      this.rawMemo = {
        key,
        doc: {
          doc: t.doc,
          events: t.events,
          transcript: t.transcript,
          spans: t.spans,
          ...(r && !r.error ? { requests: { ...r, steps: Object.fromEntries(r.steps) } } : {}),
        },
      }
    return treeView(this.rawMemo!.doc, this.tree, t.id.replace(/[\\/]/g, "_"), {
      redraw: () => {
        if (this.isConnected) this.render(this.last)
      },
      root: () => this.shadow,
    })
  }

  /** requestView is the Request tab (E1.2, request.ts): the step
   * being read (J/K, a click, select) — its prompt, diff, chips,
   * messages, catalog, params and attempts. */
  private requestView(s: PanelState, t: TurnView): HTMLElement {
    if (this.rqFor !== t.id) {
      this.rqTrees.clear()
      this.rqPick.clear()
      this.rqFor = t.id
    }
    const redraw = () => {
      if (this.isConnected) this.render(this.last)
    }
    const status = this.rowOf(t.id)?.status ?? t.doc?.status ?? "running"
    const open = { keys: this.openKeys, scope: t.id }
    return renderRequestTab({
      t,
      step: this.stepNow(s),
      running: status === "running",
      manifest: () => this.readManifest(redraw, t.doc?.manifest_hash),
      notServed: s.meta?.capabilities_off?.requests,
      keys: this.openKeys,
      tree: (k) => {
        let st = this.rqTrees.get(k)
        if (!st) this.rqTrees.set(k, (st = newTree()))
        return st
      },
      cx: { redraw, root: () => this.shadow },
      select: (n) => this.model?.selectStep(n),
      pick: this.rqPick,
      redraw,
      compaction: (c) => compactionBox(c, compactionsOf(t.doc), t.transcript, open),
      child: (call) => {
        const id = call.childRunId ?? ""
        const cv = t.children.get(id)
        const name = el("span", "weft-name", call.name, { title: id })
        const box = el("div", "weft-call", [el("div", "weft-call-h", [name, el("span", "weft-args", "subagent · step 0")])], {
          "data-key": `sub:${call.callId}`,
          "data-weft-rq-child": id,
        })
        // Read on demand, as the Story's expander reads it (A10): the
        // child's request line once read (its hole without the record),
        // a hand-off when its history could not be read.
        if (cv) box.appendChild(cv.requests ? requestLine(0, cv.requests, cv.status, open) : el("div", "weft-req", requestHole("not_recorded", { cause: "not_served", reason: s.meta?.capabilities_off?.requests })))
        else if (t.unreachable.has(id))
          box.appendChild(
            el("div", "weft-reason", [
              el("span", undefined, "child's history unreachable · "),
              el("a", undefined, "open in Studio (⤢)", { href: studioLink(this.base, id), target: "_blank", rel: "noopener", "data-weft-handoff": id }),
            ])
          )
        else if (t.tried.has(id)) box.appendChild(el("div", "weft-reason", "reading…"))
        else box.appendChild(on(el("button", "weft-btn", "read its turn", { type: "button" }), "click", () => this.go(this.model?.expandChild(id))))
        return box
      },
    })
  }

  /** readManifest is the registered config the Request tab's first
   * step diffs against (E1.2): read when a chip needs it, only where it
   * can be read (a read-scoped token never asks), kept per (endpoint,
   * token); asked again after a failure (MANIFEST_RETRY_MS on), and
   * once per run manifest hash it does not list — a redeploy under
   * weft dev registers a new one. */
  private readManifest(redraw: () => void, hash?: string): Manifest | null | undefined {
    if (tokenScope(this.cfg.token) === "read") return null
    const key = `${this.base}\u0000${this.cfg.token}`
    if (this.mf?.key !== key) this.mf = { key, doc: undefined, at: 0, busy: false, asked: new Set() }
    const m = this.mf
    const unlisted = !!m.doc && !!hash && !m.doc.agents.some((a) => a.manifest_hash === hash) && !m.asked.has(hash)
    const again = m.doc === null && Date.now() - m.at >= MANIFEST_RETRY_MS
    if (!m.busy && (m.at === 0 || again || unlisted)) {
      if (unlisted) m.asked.add(hash)
      m.busy = true
      m.at = Date.now()
      const done = (doc: Manifest | null) => {
        if (this.mf !== m) return
        m.busy = false
        m.at = Date.now()
        // A failed re-ask keeps what was read.
        m.doc = doc || m.doc || null
        redraw()
      }
      panelGet<Manifest>({ base: this.base, token: this.cfg.token }, "manifest").then(
        (doc) => done(Array.isArray((doc as Partial<Manifest> | null)?.agents) ? doc : null),
        () => done(null)
      )
    }
    return m.doc
  }

  private rowOf(id: string): RunRow | undefined {
    return this.model?.rowOf(id)
  }

}

/** The replay drawer's title per verb (Studio's VERB_TITLES). */
const VERB_TITLES: Record<ReplayDraft["verb"], string> = {
  from_step: "Replay from this step",
  edit_result: "Edit this result and replay",
  edit_prompt: "Edit the prompt and replay",
  rerun: "Re-run",
  continue: "Continue here with a new message",
}

/** The step's catalog as the ack preview reads it (the run page's
 * catalogOfStep over the panel's request record): its tools, or the
 * hole that says why not. */
interface AckCatalog {
  tools: CatalogTool[] | null
  hole?: HoleMark
  none?: boolean
  loading?: boolean
}

export function catalogAt(
  req: PanelRequests | null | undefined,
  ordinal: number,
  served: boolean,
  /** The transcript's step count (null: unknown) and whether the run
   * is still running: a missing row is said by why it is missing. */
  count: number | null = null,
  running = false
): AckCatalog {
  if (!req) return served ? { tools: null, loading: true } : { tools: null, hole: catalogReadError("the server has no request route") }
  if (req.badge) return { tools: null, hole: { hole: req.badge, reason: req.reason, fix: req.fix } }
  if (req.error) return { tools: null, hole: catalogReadError(req.error) }
  const rows = req.steps.get(ordinal)?.rows
  const row = rows?.[rows.length - 1]
  if (!row) {
    if (req.truncated && ordinal > lastStep(req)) return { tools: null, hole: catalogCapped(MAX_REQUEST_PAGES * REQUEST_PAGE) }
    if (count !== null && ordinal >= count) return { tools: null, hole: catalogNotRecorded() }
    if (running) return { tools: null, hole: catalogNotStored() }
    return { tools: null, hole: { hole: "gap", reason: REQUEST_NO_RECORD_REASON } }
  }
  const c = row.tools
  if (!c) return row.catalog_hash ? { tools: null, hole: { hole: "gap" } } : { tools: [], none: true }
  if (isHoleRef(c)) return { tools: null, hole: { hole: c.badge } }
  return { tools: c.tools }
}

/** Where a story's verbs replay from (plan F1): the run (a child's own
 * id on a child's steps — A10), its agent, the highest from_step the
 * server accepts for its transcript (lib/experiment-body.ts's
 * replayBounds — studio/edits.go's rule; null without a transcript:
 * no edit or steer verb), whether "continue here" (a fork) is offered
 * — a session's top-level run only — and the opener. from_step and an
 * edit's step are the step's ordinal (its stored index). */
export interface ReplayCtx {
  runId: string
  agent?: string
  max: number | null
  fork: boolean
  open: (runId: string, draft: ReplayDraft, agent: string | undefined, from: HTMLElement) => void
}

/** boundsOf is the server's reading of a run's transcript (its step
 * count and highest from_step), null without one. */
export function boundsOf(tr: Transcript | null | undefined): { stepCount: number; max: number } | null {
  return tr ? replayBounds(tr.batches) : null
}

/** verb is one hover/focus affordance (the run page's Verb): a button
 * named by its aria-label, its glyph drawn by CSS, keyed so a live
 * draw keeps it (and its focus); the click never reaches the card. */
function verb(label: string, glyph: string, draft: ReplayDraft, r: ReplayCtx): HTMLElement {
  const b = el("button", "weft-verb", undefined, {
    type: "button",
    "aria-label": label,
    title: label,
    "data-g": glyph,
    "data-weft-verb": draft.verb,
    "data-key": `v:${draft.verb}`,
  })
  on(b, "click", (e, n) => {
    e.stopPropagation()
    r.open(r.runId, draft, r.agent, n)
  })
  on(b, "keydown", (e) => {
    const k = (e as KeyboardEvent).key
    if (k === "Enter" || k === " ") e.stopPropagation()
  })
  return b
}

const verbs = (kind: string, list: HTMLElement[]) => el("span", "weft-verbs", list, { "data-weft-verbs": kind, "data-key": `verbs:${kind}` })

/** A step card's verbs: replay from it, edit the prompt and replay,
 * re-run the turn, continue with a new message. */
function stepVerbs(n: number, r: ReplayCtx): HTMLElement {
  const list = [
    verb(`replay from this step (step ${n})`, "↦", replayFromStep(n), r),
    verb(`edit the prompt and replay (step ${n})`, "✎", editPromptAndReplay(n), r),
    verb("re-run the whole turn", "↻", rerun(), r),
  ]
  if (r.fork) list.push(verb("continue here with a new message", "⎇", continueHere(), r))
  return verbs("step", list)
}

/** A tool call's verbs: edit its result and replay (the next step run
 * fresh against the edit), and replay from its step. */
function callVerbs(n: number, call: FoldedToolCall, r: ReplayCtx): HTMLElement {
  const list: HTMLElement[] = []
  // from_step n+1 must be one the server accepts: below the step count,
  // or at it when the transcript's kept prefix ends in answered calls.
  if (call.result && call.callId && r.max !== null && n + 1 <= r.max) {
    // An empty recorded result seeds no edit (lib/replay.ts).
    list.push(verb(`edit this result and replay (call ${call.callId})`, "✎", editResultAndReplay(n, call.callId, String(call.result.content)), r))
  }
  list.push(verb(`replay from this step (call ${call.callId}, step ${n})`, "↦", replayFromStep(n), r))
  return verbs("call", list)
}

/** The Request tab asks for a manifest it could not read again after
 * this long (a 404 before a runtime registered, a restart). */
export const MANIFEST_RETRY_MS = 30_000

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

/** readStep is the step being read as source.from_step and the
 * playground hand-off count it: its own ordinal (the stored step index
 * the server and the runtime cut at) — never a position — or -1 when
 * none is read or the run has no such step. */
export function readStep(view: FoldedRun, selected: number | null): number {
  return selected != null && view.steps.some((st) => st.index === selected) ? selected : -1
}

/** renderTimeline is the Timeline tab (D4): the waterfall at the
 * column's full width over a time axis (ms from the run's first span)
 * when the run has spans; without, its steps and tool calls placed by
 * the event sequence (seq: the event's position in the run's stream). */
export function renderTimeline(t: TurnView): HTMLElement {
  const spans = t.spans ?? []
  const win = spanWindow(spans)
  const bars: { name: string; left: number; width: number; ms: number; label?: string }[] = waterfall(spans)
  let axis = "time"
  let end = 0
  if (bars.length) {
    // The axis is the bars' own window (waterfall's).
    end = win.to - win.from
  } else {
    axis = "seq"
    const open = new Map<string, [string, number]>()
    end = Math.max(1, t.events.length ? t.events[t.events.length - 1].pos : 0)
    const put = (name: string, a: number, b: number) =>
      bars.push({ name, left: a / end, width: Math.max(0.005, (b - a) / end), ms: b - a, label: `#${a}–${b}` })
    for (const e of t.events) {
      const ev = e.event as { type?: string; index?: number; call_id?: string; name?: string }
      const id = ev.type?.startsWith("step_") ? `s${ev.index}` : `c${ev.call_id}`
      if (ev.type === "step_start" || ev.type === "tool_start") open.set(id, [ev.type === "step_start" ? `step ${ev.index}` : String(ev.name), e.pos])
      else if ((ev.type === "step_finish" || ev.type === "tool_finish") && open.has(id)) {
        const [name, a] = open.get(id)!
        open.delete(id)
        put(name, a, e.pos)
      }
    }
    // Still running: open to the newest event.
    for (const [name, a] of open.values()) put(name, a, end)
  }
  const box = el("div", "weft-timeline", undefined, { "data-axis": axis })
  const lost = spans.length - win.placed
  if (lost > 0)
    box.appendChild(el("div", "weft-note weft-warn", `${lost} span${lost === 1 ? "" : "s"} not placed: unreadable times, or an end before the start`))
  if (!bars.length) {
    box.appendChild(el("div", "weft-note", "nothing to place yet: no spans and no steps"))
    return box
  }
  const ticks = el("div", "weft-axis")
  for (const f of [0, 0.25, 0.5, 0.75, 1]) {
    const v = Math.round(end * f)
    const tick = el("span", "weft-tick", axis === "time" ? `${v} ms` : `seq ${v}`)
    tick.style.left = `${f * 100}%`
    ticks.appendChild(tick)
  }
  box.appendChild(el("div", "weft-wf-row", [el("span", "weft-wf-name", axis === "time" ? "time (ms)" : "seq"), ticks, el("span", "weft-wf-ms")]))
  box.appendChild(renderWaterfall(bars))
  return box
}

/** renderWaterfall draws §2's timing mini-waterfall: one bar per
 * span over the run's own window, wall milliseconds beside (or the
 * bar's own label: the seq range on a spanless run). */
export function renderWaterfall(bars: { name: string; left: number; width: number; ms: number; label?: string }[]): HTMLElement {
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
    row.appendChild(el("span", "weft-wf-ms", b.label ?? `${b.ms}ms`))
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
  /** The replay verbs' context (plan F1); absent: none drawn (no
   * playground, a read-scoped token, a result pane). */
  replay?: ReplayCtx
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
  // The running step is the one that streams: its text is the one
  // live region (D3), and only its card changes while it does.
  const last = runStatus === "running" && !ctx?.child ? view.steps.at(-1) : undefined
  for (const step of view.steps)
    wrap.appendChild(renderStep(step, runStatus, t, selectedStep, open, ctx, step === last))
  return wrap
}

function renderStep(
  step: FoldedStep,
  runStatus: string,
  t?: TurnView,
  selectedStep?: number | null,
  open?: OpenState,
  ctx?: FoldCtx,
  streaming = false
): HTMLElement {
  const card = el("div", "weft-step", undefined, { "data-key": `s${step.index}` })
  card.setAttribute("data-weft-step", String(step.index))
  if (selectedStep === step.index) card.style.outline = "1px solid var(--weft-accent)"
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
  if (ctx?.replay) head.appendChild(stepVerbs(step.index, ctx.replay))
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
  // Busy while it grows: a reader waits rather than re-reading the
  // text per draw; the step's end is said once by the dock's status.
  if (streaming) body.appendChild(el("div", "weft-stream", step.text, { "aria-live": "polite", "aria-busy": "true" }))
  else if (step.text) body.appendChild(el("div", undefined, step.text))
  if (step.steer) {
    const note = el("div", "weft-note", `steered: ${step.steer.text}`, { "data-key": "steer" })
    // The steer is kept in the prefix; the step that answers it runs
    // fresh (none after it: nothing to replay).
    const next = step.index + 1
    const max = ctx?.replay?.max
    if (ctx?.replay && max != null && next <= max)
      note.appendChild(verbs("steer", [verb(`replay from this steer (step ${next} runs fresh)`, "↦", replayFromStep(next), ctx.replay)]))
    body.appendChild(note)
  }
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
    box.append(...requestHole(req.badge, { reason: req.reason, fix: req.fix }))
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
      runStatus !== "running" && req.truncated
        ? requestCapped(MAX_REQUEST_PAGES * REQUEST_PAGE)
        : runStatus === "running"
          ? el("span", "weft-res", `request: ${REQUEST_NOT_STORED}`)
          : el("span", undefined, requestHole("gap", { reason: REQUEST_NO_RECORD_REASON }))
    )
    return box
  }
  const head = el("div", "weft-call-h", [
    el("span", "weft-name", "request"),
    el("span", "weft-args", mine.rows.map((r) => `attempt ${r.attempt}`).join(" · ")),
  ])
  if (mine.promptChanged) head.appendChild(el("span", "weft-badge weft-info", "prompt changed at this step"))
  if (mine.catalogChanged) head.appendChild(el("span", "weft-badge weft-info", "catalog changed at this step"))
  if (row.content && row.content !== "stripped") head.appendChild(badge(row.content))
  box.appendChild(head)
  if (row.content === "stripped") box.append(...requestHole("stripped"))
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
    const line = el("div", "weft-res", `system prompt ${shortHash(row.system_hash)}`)
    if (p) line.append(" · ", badge(p.badge))
    box.appendChild(line)
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
  const box = el("div", "weft-call", undefined, { "data-key": call.callId })
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
  const replay = ctx?.replay
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
    } else if (t) {
      // A10: replay from here on the child replays the CHILD as its own
      // run (its id, its agent), the parent untouched.
      if (replay) {
        const agent = t.doc?.children.find((c) => c.id === call.childRunId)?.agent ?? ""
        head.appendChild(
          verbs("child", [
            verb(`replay the child run ${call.childRunId} (agent ${agent || "unnamed"})`, "↻", rerun(), {
              ...replay,
              runId: call.childRunId,
              agent,
              fork: false,
            }),
          ])
        )
      }
      box.appendChild(childBlock(call.childRunId, t, open, ctx?.endpoint, replay))
    }
  }
  if (replay) head.appendChild(callVerbs(stepIndex, call, replay))
  if (call.result) {
    const ms = spanMs(t, call)
    if (ms) head.appendChild(el("span", "weft-badge weft-info", ms))
    const cut = truncation(String(call.result.content))
    if (cut) head.appendChild(cutBadge(cut))
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
function childBlock(childId: string, t: TurnView, open?: OpenState, endpoint?: string, replay?: ReplayCtx): HTMLElement {
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
  summary.setAttribute("aria-expanded", String(t.expanded.has(childId)))
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
        // The child's steps replay the child (A10); never a fork.
        ...(replay
          ? { replay: { ...replay, runId: childId, agent: row?.agent ?? "", max: boundsOf(child.transcript)?.max ?? null, fork: false } }
          : {}),
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
    const kept = Object.entries(draft.tools)
      .filter(([, enabled]) => enabled)
      .map(([name]) => name)
    if (kept.length && kept.length < Object.keys(draft.tools).length)
      p.tools = kept.join(",")
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
