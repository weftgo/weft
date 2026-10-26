// <weft-devtools> — the panel custom element (§5): shadow DOM, no
// host-framework dependency (V1, the Dv0 decision), the §5.2
// attributes and defaults. The element owns presentation only;
// state.ts owns the data. Rung 1 is a viewer (§8.1); rung 2's
// experiment drawer and approval controls render only when
// meta.capabilities reports the playground (§8.5 item 3).
//
// The import order below is panel.js's module order (rolldown places a
// module's imports in the order they are written): it is arranged for
// gzip's 32 KiB window (P9-trim2, panel-budget.json), not by kind. Every
// module the panel bundles is side-effect free at load (main.ts alone
// acts), so the order changes no behaviour — only bytes; a new import
// goes anywhere, and the build's table says what it cost.
//
// So is the order of this class's methods and of the top-level function
// declarations here and in the other src/panel modules (P9-trim3): a
// method's place in a class body and a hoisted function's place in its
// module change nothing a caller sees (no class field, static or const
// moved), so they sit where gzip's window finds their like. The section
// comments mark where a group starts, not every member it holds; a new
// method goes beside its kin.
import {
  conversationLink,
  href,
  logsLink,
  playgroundLink,
  runPage,
  saveTestLink,
  sessionLink,
  runtimesLink,
  settingsLink,
  tracePage,
} from "../lib/links"
import { installMarkerRung, markerOf } from "./markers"
import {
  keepEditedTitle,
  keepPromptWords,
  mayExportTestWith,
  mayProposeWith,
  SAVE_TEST_LABEL,
  SAVE_TEST_TITLE,
} from "../lib/saves"
import {
  CALLS_HOLE,
  DECIDE_FIELD,
  decidedCall,
  decidedResult,
  WHO_HOLE,
  decisionDetail,
  decisionWords,
  decisionsNote,
  DONE,
  expiryNext,
  expiryText,
  expiryTitle,
  EXPIRY_UNREAD,
  outcomeOf,
  REASON_HOLE,
  RUN_MARKS_HOLE,
  resumedHead,
  VERB,
  VERB_TITLE,
  withoutDecided,
} from "../lib/decisions"
import {
  appVerbWords,
  childRun,
  DERIVED_HOLE,
  derivedUsageUnknown,
  isDerived,
  mergeHoles,
  nextTurn,
  parkedKind,
  refusalLine,
  refusalWords,
  rowHoles,
  statusHoles,
  USAGE_AT_FINISH,
  usageKnown,
} from "../lib/honesty"
import {
  AWAITING,
  DECIDED_PARK,
  FILTER_KEYS,
  statusOf,
  turnTag,
} from "../lib/status"
import {
  CHILD_EYEBROW,
  childKind,
  linkedHoles,
  OPEN_SESSION,
} from "../lib/delegation"
import { el, fmtJSON, NEW_TAB, on, option, patch } from "./render"
import {
  attemptLine,
  attemptsHole,
  factsFromRows,
  timingLine,
} from "../lib/attempts"
import { stepDiffBlock } from "./compare"
import { callState, runHoles, stepHoles, truncation } from "../lib/events"
import {
  atLeastCost,
  isScripted,
  summarizerView,
  sumRuns,
  unpricedLine,
} from "../lib/cost"
import {
  paramFields,
  paramsLine,
  REQUEST_NO_RECORD_REASON,
  REQUEST_NOT_STORED,
  shortHash,
} from "../lib/requests"
import type {
  DecidedCall,
  DecisionDoc,
  Manifest,
  RunCompaction,
  RunRow,
  ToolCallPart,
  Transcript,
  Usage,
} from "../lib/api"
import type { HoleMark, ParkedKind } from "../lib/honesty"
import { BUSY_CAUSE, TURN_CHIP } from "../lib/turn-chips"
import type { Status } from "../lib/status"
import {
  ANSWER_LABELS,
  NOT_EXECUTED,
  failedAtStep,
  landStep,
  repliedLine,
  sentLine,
  stepVerdict,
  thenLine,
  toolsLine,
} from "../lib/summarize"
import { diffLines, diffSummary } from "../lib/diff"
import {
  checkArgs,
  editable,
  editKey,
  FORK_EDITS,
  impliedFromStep,
  kindOf,
  markLine,
  putEdit,
  schemaOf,
  userMessagesOf,
} from "../lib/edits"
import type { AnswerRun } from "../lib/summarize"
import {
  allowRefusals,
  breakpointsFor,
  canReplay,
  continueHere,
  editPromptAndReplay,
  editResultAndReplay,
  editTranscript,
  prefixLine,
  replayFromStep,
  replayVerdicts,
  rerun,
  tokenScopeOf,
} from "../lib/replay"
import type { CatalogTool, ReplayDraft } from "../lib/replay"
import type { ReplayEdit, UserMessage } from "../lib/edits"
import {
  compactedRefusal,
  editsHandoff,
  emptyLab,
  LAB_LABELS,
  LAB_NEW,
  LAB_NUMBERS,
  LAB_PREDATES,
  labDefault,
  labHandoff,
  labLacksDefaults,
  labOn,
  replayBounds,
} from "../lib/experiment-body"
import type { LabFields } from "../lib/experiment-body"
import {
  engineFor,
  foldedWords,
  labOf,
  targetFor,
  turnPromptOf,
} from "./playground"
import {
  agentOptions,
  exhaustedOf,
  MISS_FROM_ZERO,
  missOf,
  outcomeWord,
  TARGET_WORDS,
} from "../lib/target"
import {
  BREAKPOINTS_MOVED,
  DRAWER_TITLES,
  SIDE_EFFECT_TOOL,
  SIDE_EFFECT_WORDS,
} from "../lib/drawer-spec"
import type { DrawerSection } from "../lib/drawer-spec"
import { RUNS_IN, registering, runsInLine } from "../lib/runs-in"
import {
  ENGINE_WORDS,
  engineHeld,
  modelLabel,
  noEdit,
  runLabel,
} from "../lib/engine"
import { editList, marksBlock, previewBlock } from "./editor"
import {
  ackCatalogHole,
  badge,
  capLine,
  costText,
  turnCost,
  refusedVerb,
  catalogCapped,
  catalogNotRecorded,
  catalogNotStored,
  catalogReadError,
  cutBadge,
  holeBadges,
  holeLine,
  noPublicIdWords,
  requestCapped,
  requestHole,
  turnChips,
} from "./badges"
import type { KeepPrompt } from "../lib/saves"
import type { AgentView } from "./client"
import type { FoldedRun, FoldedStep, FoldedToolCall } from "../lib/events"
import {
  duration,
  ordinalOf,
  plural,
  relativeTime,
  stepCount,
  tokens,
} from "../lib/format"
import {
  discoverEndpoint,
  headerRungOn,
  markerRungOn,
  readConfig,
  setViaPackage,
  tokenScope,
  urlScope,
  viaPackage,
} from "./config"
import type { MountOptions, PanelConfig } from "./config"
import { installHeaderRung } from "./detect"
import type { HeaderRung } from "./detect"
import type { Marker, MarkerRung } from "./markers"
import { parseScope, serializeScope } from "../lib/scope"
import { clip as clipTitle, turnLabel, turnTitle } from "../lib/title"
import { panelStudioVersion } from "./version"
import type { Scope } from "../lib/scope"
import {
  depthWord,
  extent,
  KIND_ICON,
  lanesWord,
  ROW_KIND,
  rowBadge,
  rowTone,
  took,
  UNTIMED_HOLE,
  UNTIMED_PENDING,
  UNTIMED_PENDING_HINT,
  waterfallRows,
} from "../lib/waterfall"
import {
  compactionLine,
  compactionsOf,
  isSessionMarker,
  messageLine,
  originalOf,
  replacedBy,
  SESSION_LABEL,
  sessionNote,
  STARTED_COMPACTED,
} from "../lib/compaction"
import { PANEL_CSS } from "./styles"
import {
  GRAPH_HANDOFF,
  LOOP_LABEL,
  loopChips,
  loopCore,
} from "../lib/loopgraph"
import { nextTheme, resolveTheme, themeSetting, ThemeWatch } from "./theme"
import {
  DEV_DISCOVERY_MS,
  emptyPanelState,
  listKey,
  MAX_EVENT_PAGES,
  DEV_LIMIT,
  PanelModel,
} from "./state"
import type {
  AppVerb,
  ChildView,
  PanelRequests,
  PanelState,
  TurnView,
} from "./state"
import type { ExperimentDraft, TurnWords } from "./playground"
import {
  clampLayout,
  CYCLE,
  geometry,
  initialLayout,
  NARROW_W,
  pillPlace,
  placedIn,
  Push,
  readStore,
  TABS,
  writeStore,
} from "./layout"
import { newTree, stopTree, treeView } from "./tree"
import type { TreeState } from "./tree"
import type { Geometry, Layout } from "./layout"
import type { PlaygroundHandoff } from "../lib/links"
import { isHoleRef } from "../lib/api"
import { renderRequestTab, stepsOf } from "./request"
import { DRAWER_TAB_LABEL } from "../lib/run-tree"
import {
  fetchHandoff,
  fetchSessionPublicId,
  MAX_REQUEST_PAGES,
  PanelApiError,
  panelGet,
  putProposal,
  REQUEST_PAGE,
} from "./client"
import {
  attachmentsOf,
  callFactsOf,
  childOutcome,
  execLine,
  OUTPUT,
  outputOf,
  parkWord,
  repairedWords,
  tokenSplit,
} from "../lib/cards"

/** hasCapability reports whether meta lists the named capability (the
 * panel renders a control only when the server reports it, §8.5). */
export function hasCapability(s: PanelState, name: string): boolean {
  return s.meta?.capabilities.includes(name) ?? false
}

/** eventStatus is a run's status as the host events say it (plan C4,
 * §2): parked is succeeded with pending calls — a later resume does not
 * unsay a transition already fired. The turn list, picker and filter
 * read lib/status.ts's statusOf instead (U1). */
export function eventStatus(r: RunRow): string {
  return r.status === "succeeded" && r.pending > 0 ? "parked" : r.status
}

/** prevTurn is the turn before r in its session among the loaded
 * turns (turnTitle's prev: Studio's session page reads the turn above;
 * the panel's list is newest first and may span sessions): the highest
 * loaded non-fork turn below r's — turn numbers can have gaps (a queued
 * turn's number is taken at Send and dropped by ClearQueue). */
export function prevTurn(
  r: RunRow,
  turns: readonly RunRow[]
): RunRow | undefined {
  if (r.forked_from || !r.session_id || !(r.turn > 1)) return undefined
  let p: RunRow | undefined
  for (const x of turns)
    if (
      x.session_id === r.session_id &&
      !x.forked_from &&
      x.turn < r.turn &&
      x.turn > (p?.turn ?? 0)
    )
      p = x
  return p
}

/** engineWord is an experiment's engine as its chips say it: scripted
 * when the run answered as the scripted engine's model, else live. */
export function engineWord(r: RunRow): "scripted" | "live" {
  return isScripted(r) ? "scripted" : "live"
}

/** studioLink builds the ⤢ deep link (§2: "open in Studio" with the
 * context carried over; Dv3 adds the step, G1 makes it the ordinal):
 * lib/links.ts's runLink, so the panel lands where Studio's own link
 * to the step lands. The run id travels as one encoded path segment;
 * the panel's token is never part of a link. */
export function studioLink(
  endpoint: string,
  runId: string,
  step?: number
): string {
  return href(
    endpoint,
    runPage(runId, step === undefined ? {} : { step, view: "story" })
  )
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

const quiet = () => {}

/** How long panel-config.json (configuration rung 5) may take before
 * the panel falls through to the script's own directory. */
const PROBE_TIMEOUT_MS = 3_000

/** conversationKey is a scope without its run: the conversation it
 * names (public id, session, flow). */
export function conversationKey(s: Scope): string {
  return serializeScope({
    publicId: s.publicId,
    session: s.session,
    flow: s.flow,
  })
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

/** capSet sets k in a map bounded at max; a key set again is the
 * newest. */
function capSet<TValue>(
  m: Map<string, TValue>,
  k: string,
  v: TValue,
  max: number
): void {
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
export type DetectWord =
  RungWord | "off" | "explicit" | "none" | "url" | `url+${RungWord}`

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

/** typing: the key's real target (off the composed path) is a field. */
function typing(e: KeyboardEvent): boolean {
  const t = ((typeof e.composedPath === "function"
    ? e.composedPath()[0]
    : null) ?? e.target) as HTMLElement | null
  return (
    !!t && (/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) || t.isContentEditable)
  )
}

/** SHORTCUTS is the keyboard, in one place (the ? overlay and the
 * README's table). Every key but Alt+W fires only with focus in the
 * panel. */
export const SHORTCUTS: readonly (readonly [string, string])[] = [
  [
    "Alt+W",
    "toggle the dock from the page, not while a text field has focus (Ctrl+Shift+W too, where delivered)",
  ],
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
   * "failed", "parked" (succeeded with calls awaiting a decision),
   * "interrupted" — once per transition. step: the run's last step
   * ordinal the panel knows (G1's ordinal). */
  run: {
    runId: string
    status: string
    publicId?: string
    sessionId?: string
    step?: number
  }
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
export const DEVTOOLS_EVENTS: readonly DevtoolsEvent[] = [
  "run",
  "parked",
  "error",
]

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
  on: <TEvent extends DevtoolsEvent>(
    event: TEvent,
    cb: (detail: DevtoolsEvents[TEvent]) => void
  ) => () => void
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

/** capAdd adds k to a set bounded at max (insertion order: the oldest
 * goes). */
function capAdd(set: Set<string>, k: string, max: number): void {
  set.add(k)
  if (set.size > max) set.delete(set.values().next().value as string)
}

/** How long a parked run whose calls are not readable yet waits
 * before the next look (PARKED_TRIES looks at most). */
export const PARKED_RECHECK_MS = 1_000
const PARKED_TRIES = 3

/** How long a host's select() or session lookup waits for the panel's
 * start or rescope before saying it is not connected. */
export const SETTLE_MS = 10_000

/** childRow is a child run's row: the turn's own children, else a
 * loaded child's (a grandchild at any depth). */
function childRow(t: TurnView, id: string) {
  return [t.doc, ...[...t.children.values()].map((c) => c.doc)]
    .flatMap((d) => (d ? [...d.children, ...(d.caller_linked ?? [])] : []))
    .find((c) => c.id === id)
}

/** scopeForm is the serialised explicit scope options carry ("" for none). */
function scopeForm(o: MountOptions): string {
  const sc = o.scope
  if (typeof sc === "string") return sc
  if (sc) return serializeScope(sc)
  return o.publicId ? serializeScope({ publicId: o.publicId }) : ""
}

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
  const out: Scope = {
    publicId: typeof o.publicId === "string" ? o.publicId : "",
  }
  const flow = str("flow")
  const run = str("run", "runId")
  if (session) out.session = session
  if (flow) out.flow = flow
  if (run) out.run = run
  return out
}

/** panelsOnPage is every <weft-devtools> in the document (another
 * copy of this module's included). */
export const panelsOnPage = () =>
  Array.from(document.querySelectorAll("weft-devtools"))

/** turnsAndExperiments is every run row the panel holds: the turns,
 * then each experiment's runs. */
const turnsAndExperiments = (s: PanelState) => [
  ...s.turns,
  ...[...s.experiments.values()].flat(),
]

/** stepOrdinal is a host-given step: a non-negative integer, else none. */
const stepOrdinal = ordinalOf

/** How the renderers remember which <details> the user opened. */
interface OpenState {
  keys: Set<string>
  scope: string
}

/** clickBtn is a weft-btn button with its click handler. */
const clickBtn = (
  t: string,
  f: () => unknown,
  attrs?: Record<string, string>
): HTMLElement => on(el("button", "weft-btn", t, attrs), "click", f)

export class WeftDevtools extends HTMLElement {
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
    this.shadow.addEventListener("keydown", (e) =>
      this.panelKey(e as KeyboardEvent)
    )
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

  private edSync(key: number | undefined, always = false) {
    if (always || key !== this.edKey)
      for (const m of [this.edOpen, this.edText, this.edErr]) m.clear()
    this.edKey = key
  }

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
  private mf: {
    key: string
    doc: Manifest | null | undefined
    at: number
    busy: boolean
    asked: Set<string>
  } | null = null
  /** The turn list's filter (D4), this session only. */
  private tq = { text: "", status: "", err: false }
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
      typeof requestAnimationFrame === "function"
        ? requestAnimationFrame(frame)
        : (setTimeout(frame, 16) as unknown as number)
  }
  /** data-open was adopted (on first connect); toggles own it after. */
  private opened = false
  /** The ? shortcuts overlay (Dv3). */
  private keys = false
  private body!: HTMLElement
  private last: PanelState = emptyPanelState()
  /** The expiry clock's pending redraw (0: none), due at due. */
  private tick = 0
  private due = 0
  /** A press or an IME composition is in progress inside the dock:
   * redrawing now would take the pressed button from under the
   * pointer (no click follows) or end the composition. The redraw
   * waits for the release. */
  private held = false
  private composing = false
  private dirty = false
  private holdTimer: ReturnType<typeof setTimeout> | null = null

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
        const doc = (await fetchSessionPublicId(
          { base: this.base, token: this.cfg.token },
          id
        )) as {
          public_id?: unknown
          badge?: unknown
          reason?: unknown
          fix?: unknown
        } | null
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
        this.say(
          `session ${id} has no public id · ${mark === "not_recorded" ? noPublicIdWords(why.reason, why.fix) : "none recorded"}`
        )
        return
      }
      this.note = ""
      this.applyScope({ ...s, publicId: pub })
    })
  }

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
    if (
      (next ? serializeScope(next) : "") !==
      (this.cfg.urlScope ? serializeScope(this.cfg.urlScope) : "")
    )
      this.rescan()
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
  /** The hand-off's slow-answer timer (handoff). */
  private slow?: ReturnType<typeof setTimeout>
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
  private preScope: {
    scope?: MountOptions["scope"]
    publicId?: string
  } | null = null
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

  /** choose follows a conversation the user picked in the switcher:
   * above every rung until focus moves into a marker or the explicit
   * scope changes, and its run pins (force). */
  private choose(c: KnownScope) {
    this.chosen = c.scope
    this.forceNext = true
    this.rescopeSoon()
  }

  private say(line: string) {
    this.note = line
    this.render(this.last)
  }

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
      this.lay = {
        ...initialLayout(this.cfg.position, this.cfg.open, this.cfg.mode),
        ...st,
      }
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
      if (this.getAttribute("data-theme-resolved") !== this.resolveNow())
        this.render(this.last)
    })
    this.themeNow = this.resolveNow()
    this.syncTheme()
    this.syncGlobal()
    this.schedule()
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

  /** Expanded and not hidden. */
  private get shown(): boolean {
    return this.lay.open && !this.lay.hidden
  }

  /** applyScope makes s the explicit scope (options rung 1, the
   * data-weft-scope attribute); the same scope again changes nothing
   * (a framework re-render is not a rescope). */
  private applyScope(s: Scope) {
    const form = serializeScope(s)
    const had = this.options?.scope
    const hadForm =
      typeof had === "string" ? had : had ? serializeScope(had) : ""
    if (hadForm === form && this.getAttribute("data-weft-scope") === form) {
      this.render(this.last)
      return
    }
    // What mount() named stays mount()'s: scope(null) restores it.
    this.preScope ??= {
      scope: this.options?.scope,
      publicId: this.options?.publicId,
    }
    // publicId too: the rung-1 field readConfig reads after scope.
    this.options = { ...this.options, publicId: s.publicId, scope: { ...s } }
    this.setAttribute("data-weft-scope", form)
    this.rescan()
    this.render(this.last)
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
      if (
        !next ||
        (!next.publicId &&
          !next.session &&
          typeof s === "string" &&
          s.trim() !== "")
      ) {
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
          if (!this.dormant)
            this.say(`select ${id}: the panel is not connected`)
          return
        }
        const m = this.model
        if (!m || !this.ready) return
        const found = await m.adoptRun(id)
        if (this.model !== m || seq !== this.selectSeq) return
        if (found !== "ok") {
          this.say(
            found === "unreachable"
              ? `run ${id}: Studio did not answer`
              : `run ${id} not in this conversation`
          )
          return
        }
        this.note = ""
        const loading = m.state.selected === id ? null : m.select(id, true)
        if (n !== undefined) m.selectStep(n)
        else if (!loading) this.render(this.last)
        await loading
        if (n === undefined || this.model !== m || seq !== this.selectSeq)
          return
        // The step, checked against the run's own steps once they are
        // read: an ordinal the run does not have is said, and the last
        // step is the one carried instead.
        const t = m.state.turn
        const steps =
          t && t.id === id ? t.folded.steps.map((st) => st.index) : []
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

  /** stepNow is the step being read: linkedStep, or — on the Request
   * tab, which always shows one — the step it falls back to (the
   * first), so ⤢ and J/K start from what is on screen. */
  private stepNow(s: PanelState): number | undefined {
    const n = linkedStep(s)
    if (
      n !== undefined ||
      this.lay.tab !== "request" ||
      !s.turn ||
      s.turn.id !== s.selected
    )
      return n
    return stepsOf(s.turn, s.turn.requests)[0]
  }

  /** stepKey marks the next (1) or previous (-1) step of the open turn
   * as the one being read (⤢ and g s carry it). */
  private stepKey(d: number) {
    const s = this.last
    // The Request tab's picker lists the steps the record names too.
    const steps = s.turn
      ? this.lay.tab === "request"
        ? stepsOf(s.turn, s.turn.requests)
        : s.turn.folded.steps.map((st) => st.index)
      : []
    if (!steps.length) return
    const i = steps.indexOf(this.stepNow(s) ?? -1)
    const at =
      i < 0
        ? d > 0
          ? 0
          : steps.length - 1
        : Math.min(Math.max(i + d, 0), steps.length - 1)
    this.model?.selectStep(steps[at])
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
      const panels =
        cur !== undefined &&
        !!cur &&
        typeof cur === "object" &&
        API_OBJECTS.has(cur)
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
    return panelsOnPage().some(
      (n) => n !== this && (n as Partial<WeftDevtools>).apiObjIs?.(api) === true
    )
  }

  /** dropGlobal removes this panel's window.weft.devtools: another
   * connected panel publishes its own instead; with none, a namespace
   * the panel created and left empty goes too. */
  private dropGlobal(): void {
    try {
      const w = window as unknown as { weft?: unknown }
      const ns = w.weft
      if (!isPlainObject(ns) || ns.devtools !== this.apiObj || !this.apiObj)
        return
      delete ns.devtools
      const other = panelsOnPage().find(
        (n) =>
          n !== this &&
          n.isConnected &&
          typeof (n as Partial<WeftDevtools>).syncGlobal === "function"
      ) as WeftDevtools | undefined
      other?.syncGlobal()
      if (
        !("devtools" in ns) &&
        ns === createdNS &&
        Object.keys(ns).length === 0
      ) {
        delete w.weft
        createdNS = null
      }
    } catch {
      // nothing thrown into the host
    }
  }

  /** afterSettle runs fn once the apply already scheduled, and the
   * start or rescope it began, have settled — so a select() right after
   * a scope() acts in the new scope's list. */
  private afterSettle(fn: (settled: boolean) => Promise<void>) {
    void this.settled().then(fn).catch(quiet)
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
        if (this.model !== m || !row || eventStatus(row) !== "parked") return
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
          this.fire("parked", {
            runId,
            callId: c.id,
            ackId: c.id,
            name: typeof c.name === "string" ? c.name : "",
          })
        }
      })
      .catch(quiet)
  }

  /** on calls cb with each event of the kind named this panel
   * dispatches, and returns the unsubscribe. A throwing cb is the
   * host's: swallowed, never the panel's console. */
  on<TEvent extends DevtoolsEvent>(
    event: TEvent,
    cb: (detail: DevtoolsEvents[TEvent]) => void
  ): () => void {
    if (!DEVTOOLS_EVENTS.includes(event) || typeof cb !== "function")
      return () => {}
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

  /** fire dispatches a host event from the element, asynchronously: a
   * host listener runs outside the panel's draw. */
  private fire<TEvent extends DevtoolsEvent>(
    name: TEvent,
    detail: DevtoolsEvents[TEvent]
  ) {
    queueMicrotask(() => {
      try {
        if (this.isConnected)
          this.dispatchEvent(
            new CustomEvent(`weft:${name}`, {
              detail,
              bubbles: true,
              composed: true,
            })
          )
      } catch {
        // never the host's error
      }
    })
  }

  /** followsURL: the scope followed is the page URL's (rung 4). */
  private followsURL(): boolean {
    return (
      !!this.cfg.urlScope &&
      !this.cfg.scopeExplicit &&
      serializeScope(this.scopeNow()) === serializeScope(this.cfg.urlScope)
    )
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
      if (
        scope.publicId &&
        out.length < DETECTED_LIMIT &&
        !out.some((c) => c.key === key)
      )
        out.push({ scope: { ...scope }, key, source })
    }
    if (this.cfg.scopeExplicit) add(this.cfg.scope, "explicit")
    if (this.cfg.urlScope) add(this.cfg.urlScope, "url")
    for (const m of this.markers) add(m.scope, "marker")
    for (const d of this.seen) add(d.scope, "header")
    return out
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
    return [
      serializeScope(this.scopeNow()),
      ...this.conversations().map((c) => c.key + c.source),
    ].join("|")
  }

  /** onMarkers takes a settled scan of the page's markers; a scan that
   * changed nothing (unrelated DOM churn) draws nothing. */
  private onMarkers(list: Marker[]) {
    const prev = this.markers
    const form = (m: Marker) => serializeScope(m.scope)
    if (
      list.length === prev.length &&
      list.every(
        (m, i) => m.element === prev[i].element && form(m) === form(prev[i])
      )
    )
      return
    const before = this.markerSig()
    // A route change: the followed marker and every old one gone, one
    // new one in their place — that is the page's chat now.
    const key = this.marked ? conversationKey(this.marked) : ""
    if (
      key &&
      list.length === 1 &&
      !list.some((m) => conversationKey(m.scope) === key) &&
      !list.some((m) => prev.some((p) => p.element === m.element))
    )
      this.marked = null
    this.markers = list
    this.follow()
    if (this.markerSig() !== before) this.rescopeSoon()
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
        if (this.evByKey.size > DETECTED_LIMIT)
          this.evByKey.delete(this.evByKey.keys().next().value ?? "")
      }
      this.evBaseline = true
    }
    const baseline = this.evBaseline
    this.evBaseline = false
    const pin = m.narrowing.run ?? ""
    for (const r of turnsAndExperiments(s)) {
      const status = eventStatus(r)
      const prev = this.evStatus.get(r.id)
      if (prev === status) continue
      this.evStatus.set(r.id, status)
      if (
        prev === undefined &&
        baseline &&
        status !== "running" &&
        r.id !== pin
      )
        continue
      const folded =
        s.turn?.id === r.id ? s.turn.folded.steps.at(-1)?.index : undefined
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
        this.fire("error", {
          message: r.err || `run ${r.id} failed`,
          runId: r.id,
        })
      }
      if (status === "parked") this.reportParked(m, r.id, 1)
    }
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
    const at = (e: Element | null) =>
      e ? list.find((m) => m.element === e) : undefined
    const key = this.marked ? conversationKey(this.marked) : ""
    const pick =
      at(markerOf(document.activeElement)) ??
      at(this.lastFocused) ??
      list.find((m) => key && conversationKey(m.scope) === key)
    // Once a marker carries the explicit conversation (a helper's
    // scope() and marker), the explicit scope is a marker's until it
    // changes — that marker leaving does not hand the panel back to it.
    const exKey = conversationKey(this.cfg.scope)
    if (
      this.cfg.scopeExplicit &&
      list.some((m) => conversationKey(m.scope) === exKey)
    )
      this.explicitMarked = true
    if (pick) this.marked = pick.scope
    else if (!this.marked) {
      // An explicit scope whose marker is not read yet (a helper sets
      // both; the scan is debounced) waits for it: the explicit one
      // wins meanwhile, and the next scan finds its marker.
      const ex = this.cfg.scopeExplicit ? conversationKey(this.cfg.scope) : ""
      this.marked =
        (ex ? list.find((m) => conversationKey(m.scope) === ex) : list.at(0))
          ?.scope ?? null
    }
  }

  /** dropMarkers removes the marker rung and forgets what it read. */
  private dropMarkers() {
    this.markerRung?.disconnect()
    this.markerRung = null
    this.markers = []
    this.marked = this.lastFocused = null
  }

  // ── Scope detection (plan C3.2, C3.3) ───────────────────────────

  /** detectWord is the resolved detection choice, as the footer says it. */
  detectWord(): DetectWord {
    const rungs: RungWord | "" = this.rung
      ? this.markerRung
        ? "headers+markers"
        : "headers"
      : this.markerRung
        ? "markers"
        : ""
    if (this.followsURL()) return rungs ? `url+${rungs}` : "url"
    if (rungs) return rungs
    if (this.cfg.detect === "off") return "off"
    return this.cfg.scopeExplicit ? "explicit" : "none"
  }

  /** detectedByPath is the newest scope seen per response path. */
  detectedByPath(): Map<string, Scope> {
    return new Map(this.byPath)
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
      const api = isPlainObject(ns)
        ? Object.getOwnPropertyDescriptor(ns, "devtools")?.value
        : undefined
      if (api) for (const p of LIVE) if (p.apiObjIs(api)) owner = p
    } catch {
      // a page's own window.weft: no global panel
    }
    const pageLive = (p: WeftDevtools) => !p.dormant && !p.autoMounted
    if (owner && pageLive(owner)) return owner === this
    for (const p of LIVE) if (pageLive(p)) return p === this
    return (owner ?? LIVE.values().next().value) === this
  }

  /** syncDetect installs or removes the header rung as the
   * configuration says (headerRungOn): the host page's fetch is
   * touched only while it is on, and put back the moment it is not. */
  private syncDetect() {
    // Only once Studio has answered: a mount whose Studio never answers
    // (dormant, or the auto dock still probing) never touches fetch.
    const want =
      this.isConnected && this.ready && !this.dormant && headerRungOn(this.cfg)
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
      this.markerRung = installMarkerRung({
        onScopes: (m) => this.onMarkers(m),
        onFocus: (e) => this.onMarkerFocus(e),
      })
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
    if (
      cur &&
      conversationKey(cur) !== key &&
      this.followedPath &&
      path !== this.followedPath
    )
      return
    if (!cur || conversationKey(cur) !== key) this.followedPath = path
    const before = cur ? serializeScope(cur) : ""
    this.detected = sc
    if (serializeScope(sc) !== before && !this.cfg.scopeExplicit)
      this.schedule()
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
    if (
      marked &&
      cfg.scopeExplicit &&
      conversationKey(marked) === conversationKey(cfg.scope)
    )
      return cfg.scope
    const d = this.rung ? this.detected : null
    if (marked)
      return d && conversationKey(d) === conversationKey(marked) ? d : marked
    if (cfg.scopeExplicit) return cfg.scope
    return d ?? cfg.scope
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

  /** close collapses the dock to its button. */
  close(): void {
    if (this.shown) this.toggle()
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
      on: <TEvent extends DevtoolsEvent>(
        event: TEvent,
        cb: (detail: DevtoolsEvents[TEvent]) => void
      ) => this.on(event, cb),
      studioLink: (runId: string, step?: number) =>
        this.studioLink(runId, step),
    })
    API_OBJECTS.add(api)
    this.apiObj = api
    return api
  }

  /** handoff opens a Studio page through GET /api/panel-handoff (plan
   * G3) under a playground panel token with the handoff capability:
   * the answer's location — a path under the base, resolved against
   * the endpoint, never the host page — carries a minted read token in
   * its fragment, so the hosted Studio opens on that conversation
   * without a token pasted. false (the caller follows the plain link)
   * otherwise; a refusal opens the plain link — a 403 or 404 silently
   * (the server's rule), anything else with its words. The tab opens
   * after the hand-off answers, so a browser may block it as a popup:
   * the plain link (never the minted one) is on the note line past 1 s
   * and, whatever the answer, once it came. */
  private handoff(url: string): boolean {
    if (
      tokenScope(this.cfg.token) !== "playground" ||
      !hasCapability(this.last, "handoff")
    )
      return false
    const u = new URL(url)
    const origin = new URL(this.base).origin
    const seq = this.startSeq
    const done = (to: string, why = "") => {
      if (seq !== this.startSeq || !this.isConnected) return
      clearTimeout(this.slow)
      openTab(to)
      this.say(`open in Studio: ${why ? `${why} · ` : ""}${url}`)
    }
    clearTimeout(this.slow)
    this.slow = setTimeout(() => this.say(`open in Studio: ${url}`), 1000)
    fetchHandoff(
      this.ep,
      u.pathname.slice(new URL(this.base).pathname.length - 1) + u.search
    ).then(
      (r) => {
        let to = url
        try {
          const l = new URL(r.location, this.base)
          // A path under the base: anything off the endpoint's origin
          // (or no URL at all) is not this hand-off's answer.
          if (r.location && l.origin === origin) to = l.toString()
        } catch {
          // malformed: the plain link
        }
        done(to)
      },
      (e) =>
        done(
          url,
          e instanceof PanelApiError && (e.status === 403 || e.status === 404)
            ? ""
            : e.message
        )
    )
    return true
  }

  /** keydown is the window's one key: Alt+W (Ctrl+Shift+W where the
   * browser delivers it) toggles the dock from anywhere — never while
   * the user types in a field, never during an IME composition, never
   * a key the page already handled. */
  private keydown(e: KeyboardEvent): void {
    if (e.defaultPrevented || e.isComposing || typing(e)) return
    if (!this.ownsToggle()) return
    const toggleCombo =
      (e.altKey &&
        !e.ctrlKey &&
        !e.shiftKey &&
        !e.metaKey &&
        e.code === "KeyW") || // Q4's pick
      (e.ctrlKey && e.shiftKey && !e.altKey && !e.metaKey && e.code === "KeyW") // where delivered
    if (!toggleCombo) return
    e.preventDefault()
    this.keys = false
    this.toggle()
    // Opened from the keyboard: focus goes to the dock, so Esc, j, k…
    // work at once (the user asked for the panel).
    if (this.shown)
      this.body
        .querySelector<HTMLElement>(".weft-dock")
        ?.focus({ preventScroll: true })
  }

  /** turnKey selects the next (1) or previous (-1) turn of the list. */
  private turnKey(d: number) {
    const s = this.last
    const ids = s.turns.filter((r) => this.match(r)).map((r) => r.id)
    const i = ids.indexOf(s.selected)
    const id = ids.at(Math.min(Math.max(i + d, 0), ids.length - 1))
    if (id && id !== s.selected) this.go(this.model?.select(id, true))
  }

  /** openInStudio is g s: the selected turn (and step) in Studio, the
   * link ⤢ carries (lib/links.ts), in a new tab. */
  private openInStudio() {
    const url = this.last.selected
      ? this.studioLink(this.last.selected, this.stepNow(this.last))
      : ""
    if (url && !this.handoff(url)) openTab(url)
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
      dock.querySelectorAll<HTMLElement>(
        "button, a[href], select, input, textarea, summary, [tabindex]"
      )
    ).filter((n) => {
      if (n.tabIndex < 0 || (n as HTMLButtonElement).disabled) return false
      for (
        let d = n.parentElement?.closest("details");
        d;
        d = d.parentElement?.closest("details")
      )
        if (!d.open && !(n.tagName === "SUMMARY" && n.parentElement === d))
          return false
      return laidOut
        ? n.getClientRects().length > 0
        : !(narrow && n.closest(".weft-rows"))
    })
    const first = stops[0]
    const last = stops.at(-1)
    const now = this.shadow.activeElement
    const to = e.shiftKey
      ? now === first || now === dock
        ? last
        : null
      : now === last
        ? first
        : null
    if (!to) return
    e.preventDefault()
    to.focus()
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
    // A Timeline step row (P7) is a button: Enter or Space clicks it.
    const tl = at.matches(".weft-tl[role]")
    if (tl && (e.key === "Enter" || e.key === " ")) {
      e.preventDefault()
      at.click()
      return
    }
    if (
      (e.key === "ArrowDown" || e.key === "ArrowUp") &&
      (tl || at.classList.contains("weft-turn"))
    ) {
      // The roving tabindex (D3): arrows move the tab stop down the
      // turn list (or the Timeline's step rows); Enter or a click selects.
      const rows = Array.from(
        this.body.querySelectorAll<HTMLElement>(
          tl ? ".weft-tl[role]" : ".weft-turn"
        )
      )
      const i = rows.indexOf(at) + (e.key === "ArrowDown" ? 1 : -1)
      const n = i < 0 ? undefined : rows.at(i)
      e.preventDefault()
      if (n && tl) {
        at.tabIndex = -1
        n.tabIndex = 0
      }
      n?.focus()
      return
    }
    if (
      e.altKey &&
      e.shiftKey &&
      !e.ctrlKey &&
      !e.metaKey &&
      e.code === "KeyW"
    ) {
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
      const tree =
        tab === "raw" || tab === "request"
          ? this.body.querySelector<HTMLElement>(`#weft-tp-${tab} .weft-tree-q`)
          : null
      const box =
        tree ??
        (tab === "raw"
          ? null
          : this.body.querySelector<HTMLElement>(".weft-turn-q"))
      if (box) box.focus()
      else done = false // no box (an empty list): the key stays the page's
    } else done = false
    if (done) e.preventDefault()
  }

  /** cycle moves the dock to the next layout of CYCLE (Alt+Shift+W,
   * the header's layout button): float, dock right, bottom, left, top. */
  private cycle() {
    const l = this.lay
    const next =
      CYCLE[
        (CYCLE.indexOf(l.mode === "float" ? "float" : l.side) + 1) %
          CYCLE.length
      ]
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

  attributeChangedCallback(
    name: string,
    old: string | null,
    value: string | null
  ) {
    if (old === value) return
    this.cfg = readConfig(this)
    if (!this.isConnected) return
    // A data-position set after connect is the host moving the dock
    // (D1): the float's corner, or a dock's side; the size stays.
    if (name === "data-position") {
      const at = initialLayout(this.cfg.position, true, "")
      const l = this.lay
      Object.assign(l, {
        mode: at.mode,
        side: at.side,
        y: innerHeight - l.h - 16,
      })
      l.x = this.cfg.position === "bottom-left" ? 16 : innerWidth - l.w - 16
    }
    if (name === "data-theme") this.render(this.last)
    this.syncGlobal()
    this.syncDetect()
    this.schedule()
  }

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
        endpoint =
          (await discoverEndpoint(cfg.configURL, probe.signal)) || cfg.endpoint
      } finally {
        clearTimeout(timer)
        if (this.probe === probe) this.probe = null
      }
      if (seq !== this.startSeq || !this.isConnected) return
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
      this.conn = {
        endpoint: cfg.endpoint,
        token: cfg.token,
        scope: serializeScope(scope),
      }
      this.base = endpoint
      this.last = model.state
      try {
        ok = await model.start()
      } catch {
        ok = false
      }
      // Removed meanwhile (before the stream's first frame, say): the
      // disconnect disposed the model; nothing here may touch a host
      // that is gone.
      if (seq !== this.startSeq || this.model !== model || !this.isConnected)
        return
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

  /** apply brings the model in line with the attributes: a new
   * endpoint or token is a new connection; a new public id rescopes
   * the one there is (the §5.2 setter path); position only redraws. */
  private apply() {
    const cfg = this.cfg
    const conn = this.conn
    this.syncDetect()
    if (
      this.model &&
      conn &&
      conn.endpoint === cfg.endpoint &&
      conn.token === cfg.token
    ) {
      const next = this.scopeNow()
      const form = serializeScope(next)
      if (conn.scope !== form) {
        // What was typed for the previous conversation (an unsent steer,
        // a resolve result) is not offered under the next one.
        if (this.model.publicId !== next.publicId) {
          this.scratch.clear()
        }
        conn.scope = form
        // An explicit scope's run (or the user's switcher choice) pins
        // whatever the user clicked; a detected one (the next turn's
        // header, a marker focus follows) respects the click.
        const force =
          this.forceNext ||
          (cfg.scopeExplicit && next === cfg.scope && !this.explicitMarked)
        this.settling = this.model.rescope(next, { force }).catch(quiet)
      }
      this.forceNext = false
      this.render(this.last)
      return
    }
    this.settling = this.start().catch(quiet)
  }

  /** retry is the not-reachable line's control: probe again. */
  private retry() {
    this.cfg = readConfig(this)
    this.settling = this.start(true).catch(quiet)
  }

  /** turnFilter is the box above the turn list (D4): free text, a
   * status, has error — this session only, never stored. */
  private turnFilter(): HTMLElement {
    const f = this.tq
    const box = el("div", "weft-tq", "", { "data-key": "tq" })
    const q = el("input", "weft-input weft-turn-q", "", {
      type: "search",
      "aria-label": "filter turns",
      placeholder: "/ filter turns",
    }) as HTMLInputElement
    q.value = f.text
    on(q, "input", (_, n) => {
      f.text = (n as HTMLInputElement).value
      this.render(this.last)
    })
    const st = el("select", "weft-input weft-tq-status", "", {
      "aria-label": "status",
    }) as HTMLSelectElement
    for (const v of ["", ...FILTER_KEYS]) {
      option(
        st,
        v === "resumed" ? DECIDED_PARK : v || "any status",
        { value: v },
        v === f.status
      )
    }
    st.value = f.status
    on(st, "change", (_, n) => {
      f.status = (n as HTMLSelectElement).value
      this.render(this.last)
    })
    const err = el("input", "weft-tq-err", "", {
      type: "checkbox",
    }) as HTMLInputElement
    err.checked = f.err
    on(err, "change", (_, n) => {
      f.err = (n as HTMLInputElement).checked
      this.render(this.last)
    })
    box.append(q, st, el("label", "weft-tool", [err, "has error"]))
    return box
  }

  private unhold() {
    this.holdTimer = null
    this.held = false
    this.flush()
  }

  toggleRaw() {
    this.setTab(this.lay.tab === "raw" ? this.prevTab : "raw")
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

  /** The tab r goes back to from Raw. */
  private prevTab = "story"

  disconnectedCallback() {
    LIVE.delete(this)
    window.removeEventListener("keydown", this.onKey)
    window.removeEventListener("resize", this.onResize)
    if (this.resizeFrame !== null) {
      if (typeof cancelAnimationFrame === "function")
        cancelAnimationFrame(this.resizeFrame)
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
    clearTimeout(this.slow)
    clearTimeout(this.tick)
    this.tick = 0
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
    if (
      this.model &&
      s === this.model.state &&
      s.selected &&
      s.selected !== this.lay.run
    ) {
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
    // The expiry clock is re-armed by what this frame draws (expiry).
    clearTimeout(this.tick)
    this.tick = 0
    this.edSync(s.drawer?.key)
    // Build first, swap second: a throw while building leaves the dock
    // as it was.
    const next: Node[] = []
    if (this.dormant) {
      // Studio did not answer a mount the host made: one quiet line.
      if (this.unreachable !== null) {
        const line = el("div", "weft-unreachable", "", {
          role: "status",
        })
        line.append(
          this.unreachable
            ? "Studio not reachable at "
            : "Studio not reachable: no http(s) endpoint",
          ...(this.unreachable
            ? [el("span", "weft-unreachable-at", this.unreachable)]
            : []),
          " · "
        )
        if (this.checking) line.append(el("span", "weft-checking", "checking…"))
        else {
          const again = el("button", "weft-retry", "retry", {
            type: "button",
            title: "ask Studio again",
          })
          on(again, "click", () => this.retry())
          line.append(again)
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
    if (had && !this.shadow.activeElement)
      root
        .querySelector<HTMLElement>(".weft-dock, .weft-fab")
        ?.focus({ preventScroll: true })
    // A verb's drawer opens on its field (plan F1): once, then the
    // user's focus is theirs.
    if (this.focusNext) {
      const f = Array.from(
        root.querySelectorAll<HTMLElement>("[data-weft-k]")
      ).find((n) => n.getAttribute("data-weft-k") === this.focusNext)
      this.focusNext = ""
      f?.focus({ preventScroll: true })
    }
    this.watchOlder()
    this.syncTheme()
    const mode = this.dormant
      ? "line"
      : this.lay.hidden
        ? "hidden"
        : this.shown
          ? this.lay.mode
          : "pill"
    if (root.getAttribute("data-mode") !== mode)
      root.setAttribute("data-mode", mode)
    // data-push (D1): only while a dock is drawn docked.
    // data-push (D1): a dock pads its side by its size; the bottom
    // sheet pads the bottom by its 70vh.
    if (this.cfg.push && this.isConnected && root.querySelector(".weft-docked"))
      this.push.apply(this.lay.side, `${this.lay.d}px`)
    else if (
      this.cfg.push &&
      this.isConnected &&
      root.querySelector(".weft-sheet")
    )
      this.push.apply("bottom", "70vh")
    else this.push.restore()
  }

  /** syncTheme sets data-theme-resolved on the element itself (D2):
   * the :host rule that picks the token set reads it. */
  private syncTheme(): void {
    const t = this.themeNow
    if (this.isConnected && this.getAttribute("data-theme-resolved") !== t)
      this.setAttribute("data-theme-resolved", t)
  }

  /** themeButton cycles the user's theme (D2): auto → light → dark;
   * stored per origin, auto clears it. An explicit data-theme wins, so
   * the button then only says so — aria-disabled, not disabled, so it
   * stays focusable and read. */
  private themeButton(): HTMLElement {
    const pick = themeSetting(this.lay.theme)
    const now = this.themeNow
    const fixed = this.cfg.theme !== "auto"
    const said = fixed
      ? `theme: ${now}, set by the page`
      : `theme: ${pick}${pick === "auto" ? ` (${now})` : ""}`
    const b = el("button", "weft-btn weft-theme", "◐", {
      type: "button",
      title: fixed ? said : `${said} — next: ${nextTheme(pick)}`,
      "aria-label": said,
      "data-w": "theme",
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
    const dock = el("div", `weft-dock weft-open ${g.cls}`, "", {
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
    dock.append(head)
    if (this.howTo && !this.model?.publicId) dock.append(this.howToBox())
    const cols = el("div", "weft-cols")
    if (g.width < NARROW_W) {
      const pick = this.turnPick(s)
      if (pick) cols.append(pick)
    }
    cols.append(this.turnList(s), this.main(s))
    dock.append(cols, this.footer(s))
    // The overlay lives inside the dock: it is its containing block.
    // Outside it it would be laid over the host page.
    if (this.keys) dock.append(this.shortcuts())
    dock.append(this.announcer(s))
    if (!g.sheet) {
      const float = l.mode === "float"
      const grip = el(
        "div",
        float ? "weft-grip" : `weft-edge weft-edge-${l.side}`,
        "",
        { title: "resize", "aria-hidden": "true" }
      )
      on(grip, "pointerdown", (e) => {
        const { w, h: ht, d } = l
        this.grab(e as PointerEvent, (dx, dy) => {
          if (float) {
            l.w = w + dx
            l.h = ht + dy
          } else
            l.d =
              d +
              ({ left: dx, right: -dx, top: dy, bottom: -dy } as const)[l.side]
        })
      })
      dock.append(grip)
    }
    return dock
  }

  /** grab follows one drag of a handle (pointer events, captured):
   * move gets the pointer's offset, the layout is clamped and the dock
   * placed on every move; the end is remembered and redrawn. A press
   * on a control in the header is the control's. */
  private grab(e: PointerEvent, move: (dx: number, dy: number) => void) {
    const h = e.currentTarget as HTMLElement
    if (
      e.button > 0 ||
      (e.target as Element).closest("button, a, select, input, textarea")
    )
      return
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
      if (this.cfg.push && dock.classList.contains("weft-docked"))
        this.push.apply(this.lay.side, `${this.lay.d}px`)
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
    const status = t
      ? (this.rowOf(t.id)?.status ?? t.doc?.status ?? "running")
      : ""
    const cur = status === "running" ? t?.folded.steps.at(-1) : undefined
    const key = t && cur ? `${t.id}\u0000${cur.index}` : ""
    if (this.streamKey && this.streamKey !== key) {
      const [id, n] = this.streamKey.split("\u0000")
      const st =
        t?.id === id
          ? t.folded.steps.find((x) => String(x.index) === n)
          : undefined
      if (st)
        this.said = `step ${n} finished · ${(st.text || "").split(/\s+/).filter(Boolean).length} words`
    }
    this.streamKey = key
    return el("div", "weft-sr", this.said, {
      role: "status",
      "data-key": "said",
    })
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
    const streamed = (r: RunRow) =>
      !!this.model?.publicId || r.agent === s.devAgent
    const running = s.live
      ? s.turns.find((r) => r.status === "running" && streamed(r))
      : undefined
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
      ["devtools", el("span", "weft-fab-count", step ? ` ● ${step}` : " ●")],
      { title: `${words} — Alt+W`, "aria-label": words }
    )
    return this.pillEnd(fab, s, running)
  }

  /** field marks an input the redraw must give focus and caret back
   * to. */
  private field<T extends HTMLElement>(n: T, key: string): T {
    n.setAttribute("data-weft-k", key)
    return n
  }

  /** pillEnd finishes the pill (D1): the current turn's cost — its
   * tokens in→out once the run has finished, "—" while unknown — the
   * z-index, the click. */
  private pillEnd(
    fab: HTMLElement,
    s: PanelState,
    running?: RunRow
  ): HTMLElement {
    const r = running ?? turnsAndExperiments(s).find((x) => x.id === s.selected)
    if (r) {
      const known = usageKnown(r.status)
      fab.append(
        el(
          "span",
          "weft-fab-cost",
          ` · ${known ? `${tokens(r.usage.input_tokens)}→${tokens(r.usage.output_tokens)}` : "—"}`,
          {
            title: known
              ? "the current turn's tokens, input→output"
              : "the current turn's usage: known when it finishes",
          }
        )
      )
    }
    // Named with its visible words (label in name, WCAG 2.5.3).
    if (!running) fab.setAttribute("aria-label", `weft ${fab.textContent}`)
    if (this.cfg.zIndex) fab.style.zIndex = this.cfg.zIndex
    on(fab, "click", () => this.toggle())
    return fab
  }

  /** place gives the dock its box (and z-index, and the narrow class)
   * — also mid-drag, without a rebuild. */
  private place(dock: HTMLElement, g: Geometry = geometry(this.lay)) {
    for (const k of ["left", "top", "width", "height"])
      dock.style.setProperty(k, g.style[k] ?? "")
    if (this.cfg.zIndex) dock.style.zIndex = this.cfg.zIndex
    dock.classList.toggle("weft-narrow", g.width < NARROW_W)
  }

  /** say draws the API's honest line. */
  /** The endpoint the panel's own fetches go to. */
  private get ep() {
    return { base: this.base, token: this.cfg.token }
  }

  /** howToBox is the fallback's expander: the one-line fixes. */
  private howToBox(): HTMLElement {
    const box = el("div", "weft-howto")
    for (const line of HOW_TO_SCOPE) box.append(el("code", "", line))
    return box
  }

  /** go runs one of the model's async verbs; a rejection stays here
   * (the host page's unhandledrejection is not the panel's log). */
  private go(p: Promise<unknown> | undefined) {
    p?.catch(quiet)
  }

  /** turnPick is the turn column as a dropdown (a panel under
   * NARROW_W wide): every listed turn, the selected one chosen. */
  private turnPick(s: PanelState): HTMLElement | null {
    // The filter narrows the dropdown as it does the column; the
    // selected turn stays listed (it is what the select shows).
    const rows = turnsAndExperiments(s).filter(
      (r) => r.id === s.selected || this.match(r)
    )
    if (!rows.length) return null
    const sel = el("select", "weft-turn-pick", "", {
      "aria-label": "turn",
      "data-weft-k": "turnpick",
    }) as HTMLSelectElement
    for (const r of rows) {
      // What the user recognises (U6): "● parked t3 How many units… ·
      // 2 steps · 811ms" (a resume in turnTitle's words, as Studio's
      // session page); an experiment in its card's order, "↳ replay of
      // t3 · scripted · ● failed · 2 steps · 811ms". The id is the
      // option's title only.
      const t = turnTitle(prevTurn(r, s.turns), r)
      const src = r.forked_from && this.rowOf(r.forked_from.split("#")[0])
      const tail = `${stepCount(r.steps)} · ${duration(r.started, r.finished)}`
      option(
        sel,
        r.forked_from
          ? `↳ replay of ${src ? turnLabel(src) || shortId(src.id) : "a turn"} · ${engineWord(r)} · ● ${this.st(r).word} · ${tail}`
          : `${["●", this.st(r).word, turnLabel(r), clipTitle(t.text)].filter(Boolean).join(" ")} · ${tail}`,
        { value: r.id, "data-key": r.id, title: `${r.id}\n${t.full}` },
        r.id === s.selected
      )
    }
    on(sel, "change", (_, n) =>
      this.go(this.model?.select((n as HTMLSelectElement).value, true))
    )
    return sel
  }

  private header(s: PanelState): HTMLElement {
    const h = el("div", "weft-head")
    const running = s.turns.some((r) => r.status === "running")
    h.append(
      el(
        "span",
        `weft-dot${s.live ? (running ? " weft-run" : " weft-on") : ""}`,
        "",
        {
          title: s.live ? "live" : "history",
        }
      )
    )
    const agent = s.session?.agent ?? s.turns.at(0)?.agent ?? ""
    const scope = this.model?.publicId || s.session?.public_id || ""
    const title = scope
      ? `${agent ? agent + " · " : ""}${scope}`
      : NO_SCOPE_LABEL
    h.append(
      el("span", "weft-title", title, {
        title: scope ? title : `${title}: the newest runs are shown`,
      })
    )
    if (!scope) {
      // The fallback (C3.4): never an unexplained list — the fixes are
      // one click away.
      const how = el(
        "button",
        `weft-btn weft-howto-btn${this.howTo ? " weft-active" : ""}`,
        "how to scope",
        {
          type: "button",
          "aria-expanded": String(this.howTo),
          title: "the one-line ways to scope the panel to your conversation",
        }
      )
      on(how, "click", () => {
        this.howTo = !this.howTo
        this.render(this.last)
      })
      h.append(" · ", how)
    }
    const sw = this.switcher(s)
    if (sw) h.append(sw)
    // The scope's narrowing, as chips: the session filters the list,
    // the flow is carried and filters nothing yet (C3.2).
    const narrowing = this.model?.narrowing ?? {}
    if (narrowing.session)
      h.append(
        el(
          "span",
          "weft-chip weft-scope-chip",
          `session ${narrowing.session}`,
          { title: "the turn list is narrowed to this session" }
        )
      )
    if (narrowing.flow)
      h.append(
        el("span", "weft-chip weft-scope-chip", `flow ${narrowing.flow}`, {
          title: "the scope's flow — carried, filters nothing yet",
        })
      )
    h.append(el("span", "weft-grow"))
    // A derived turn whose spans reported no usage is left out of the
    // total (its zeros are "not reported"): the total then reads "≥",
    // or "—" when no turn's usage is known, titled with the API's reason.
    const known = s.turns.filter((r) => !derivedUsageUnknown(r))
    const unknown = s.turns.find((r) => derivedUsageUnknown(r))
    const inTok = known.reduce((n, r) => n + r.usage.input_tokens, 0)
    const outTok = known.reduce((n, r) => n + r.usage.output_tokens, 0)
    // No turn at all reads "—" too (UX-F-33): nothing known, not 0→0.
    const tok = !known.length
      ? "—"
      : `${unknown ? "≥" : ""}${tokens(inTok)}→${tokens(outTok)}`
    // "turn" is a session's word (UX-F-13): a list no session scopes
    // counts runs; "1 turn" singular.
    const count = s.turns.length
    const stats = `${count}${s.turnsCapped ? "+" : ""} ${s.session ? "turn" : "run"}${count - 1 || s.turnsCapped ? "s" : ""} · ${tok} tok`
    h.append(
      el("span", "", stats, {
        title: unknown ? `${stats} — ${unknown.cost_reason ?? ""}` : stats,
        "data-weft-stats": "",
      })
    )
    // The turns' summed cost (A5.3), gated on capability pricing: "≥ $x ·
    // k of n turns priced" when a turn's is not known, "—" when none is,
    // the reasons its title. Older turns not loaded yet: the sum is the
    // loaded turns' only, "≥". Then what is unpriced, once (U7: lib/
    // cost.ts's unpricedLine), a link to Studio's pricing table.
    if (s.turns.length && hasCapability(s, "pricing")) {
      h.append(
        el(
          "span",
          "",
          [
            costText(
              atLeastCost(
                s.turns,
                loadedOnly(s, s.turns.length, "turn"),
                "turn"
              )
            ),
          ],
          { "data-weft-cost-total": "" }
        )
      )
      const u = unpricedLine(s.turns, "turn", s.turnsCapped)
      if (u) {
        const a = el("a", "weft-chip weft-tag weft-unp", u.text, {
          href: href(this.base, settingsLink(u.models[0])),
          ...NEW_TAB,
          title: `${u.title} · price in Studio`,
          "data-weft-unpriced": "",
        })
        on(a, "click", (e, n) => {
          if (this.handoff((n as HTMLAnchorElement).href)) e.preventDefault()
        })
        h.append(a)
      }
    }
    if (s.turns.length && s.selected) {
      const a = el("a", "weft-btn", "⤢", {
        href: studioLink(this.base, s.selected, this.stepNow(s)),
        ...NEW_TAB,
        title: "open in Studio (run, and the step you are reading)",
        "aria-label": "open in Studio",
        "data-w": "Studio",
      })
      on(a, "click", (e, n) => {
        if (this.handoff((n as HTMLAnchorElement).href)) e.preventDefault()
      })
      h.append(a)
    }
    // The conversation's page in Studio (U16): the session's timeline,
    // else the public id's conversation page — through the hand-off.
    const sid = narrowing.session || s.session?.id
    if (sid || scope) {
      const a = el("a", "weft-btn", `${sid ? "timeline" : "conversation"} ⤢`, {
        href: href(this.base, sid ? sessionLink(sid) : conversationLink(scope)),
        ...NEW_TAB,
        "data-weft-conv": "",
      })
      on(a, "click", (e, n) => {
        if (this.handoff((n as HTMLAnchorElement).href)) e.preventDefault()
      })
      h.append(a)
    }
    const where = this.lay.mode === "float" ? "float" : `dock ${this.lay.side}`
    const lay = el("button", "weft-btn weft-layout", "⇆", {
      title: `layout: ${where} — next (Alt+Shift+W)`,
      "aria-label": `layout: ${where}`,
      "data-w": "layout",
    })
    on(lay, "click", () => this.cycle())
    h.append(lay, this.themeButton())
    h.append(
      clickBtn("–", () => this.toggle(), {
        title: "collapse (Alt+W)",
        "aria-label": "collapse",
      })
    )
    return h
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
    const n2 = el(
      known ? "button" : "a",
      "weft-btn weft-src",
      `↖ ${name}${known ? "" : " ⤢"}`,
      {
        "aria-label": `${known ? "go to" : "open in Studio"} the source: run ${src}${where}`,
        "data-weft-source": r.forked_from,
        ...(known
          ? { type: "button" }
          : {
              href: href(this.base, runPage(src, { step: n })),
              ...NEW_TAB,
            }),
      }
    )
    if (known) on(n2, "click", () => this.select(src, n))
    return [n2]
  }

  /** canAct: the write verbs are drawn when the server reports the
   * playground AND the page's token may use them — a read-scoped
   * panel token (§6: read-only unless minted with "playground": true)
   * is refused on every one, so they are not offered. lib/replay.ts's
   * canReplay: Studio's replay verbs gate on the same rule. */
  private canAct(s: PanelState): boolean {
    return canReplay(s.meta?.capabilities ?? [], this.cfg.token)
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
    const sel = el("select", "weft-switch", "", {
      "aria-label": "conversation",
      "data-weft-k": "switch",
    }) as HTMLSelectElement
    const opt = (label: string, value: string, sel1: boolean, key: string) =>
      option(sel, label, { value, "data-key": key }, sel1)
    const dot = s.live ? "● " : "○ "
    if (!listed)
      opt(
        `${dot}${cur || "latest (dev)"} · not on the page`,
        "",
        true,
        "\u0000"
      ).disabled = true
    list.forEach((c, i) => {
      const { publicId, session, flow } = c.scope
      const label = [
        publicId,
        session && `session ${session}`,
        flow && `flow ${flow}`,
        c.source,
      ]
        .filter(Boolean)
        .join(" · ")
      opt(
        c.key === cur ? dot + label : label,
        String(i),
        c.key === cur,
        c.key
      ).setAttribute("data-weft-source", c.source)
    })
    on(sel, "change", (_, n) => {
      const v = (n as HTMLSelectElement).value
      const c = v ? list.at(Number(v)) : undefined
      if (c && c.key !== cur) this.choose(c)
    })
    return sel
  }

  /** filtering: the turn filter narrows anything. */
  private filtering(): boolean {
    return this.tq.text.trim() !== "" || !!this.tq.status || this.tq.err
  }

  /** match is the turn filter's test of one row. */
  private match(r: RunRow): boolean {
    const f = this.tq
    // "parked" is open or not yet linked (U1-fix): a decided one reads
    // parked → tN, the "parked → (decided)" pick.
    if (f.status && this.st(r).key !== f.status) return false
    if (f.err && !r.err && r.status !== "failed") return false
    const q = f.text.trim().toLowerCase()
    return (
      !q ||
      `${r.id}\n${r.err}\n${r.prompt_preview ?? ""}`.toLowerCase().includes(q)
    )
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
    const want =
      !!n &&
      typeof IntersectionObserver === "function" &&
      !n.closest(".weft-narrow") &&
      !this.filtering()
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

  private turnList(s: PanelState): HTMLElement {
    const list = el("div", "weft-turns")
    // The host API's honest line (a lookup refused, a run not here).
    if (this.note)
      list.append(
        el("div", "weft-note weft-api-note", this.note, { role: "status" })
      )
    // The scope's narrowing, said where it applies: never an empty or
    // unpinned list without a reason.
    if (s.pinMissing)
      list.append(
        el(
          "div",
          "weft-note weft-pin-missing",
          `run ${s.pinMissing} not in this conversation`
        )
      )
    const session = this.model?.narrowing.session
    if (session && s.sessionUnrecorded)
      list.append(
        el(
          "div",
          "weft-note",
          `session ${session}: these runs carry no session id — not narrowed`
        )
      )
    // The fallback's stream, said: refused (it polls), or one agent of
    // several followed live.
    if (!this.model?.publicId && s.devRefused)
      list.append(
        el(
          "div",
          "weft-note weft-dev-poll",
          "streaming needs the server token · polling"
        )
      )
    else if (!this.model?.publicId && s.devAgent)
      list.append(
        el(
          "div",
          "weft-note weft-dev-poll",
          `live: agent ${s.devAgent} · the other agents' runs every ${DEV_DISCOVERY_MS / 1000} s`
        )
      )
    if (!s.turns.length && !s.experiments.size) {
      const empty = session
        ? `no turns of session ${session} yet`
        : this.model?.publicId
          ? "no turns yet — run your app"
          : "no runs yet — connect your app (otel.Install)"
      list.append(el("div", "weft-splash", empty))
      return list
    }
    // The filter (D4): text over the id, the error and the row's
    // prompt_preview; a status; has error. Applied to the loaded rows.
    list.append(this.turnFilter())
    const turns = s.turns.filter((r) => this.match(r))
    const filtered = this.filtering()
    if (filtered)
      list.append(
        el(
          "div",
          "weft-tq-n",
          `${turns.length} of ${s.turns.length} ${s.turnsCapped ? "loaded" : "turns"}${s.turnsCapped ? " · the filter applies to the loaded turns" : ""}`
        )
      )
    // One list (D3): a listitem per run, keyed by its id; one row
    // tabbable (the roving tabindex — the row last focused, else the
    // selected one, else the first), arrow keys move it.
    const ids = [...turns, ...[...s.experiments.values()].flat()].map(
      (r) => r.id
    )
    const tab = ids.includes(this.rove)
      ? this.rove
      : ids.includes(s.selected)
        ? s.selected
        : ids[0]
    const items = el("div", "weft-rows", "", {
      role: "list",
      "aria-label": "turns, newest first",
    })
    const row = (r: RunRow) => this.turnRow(r, s.selected, tab, s.turns)
    const shown = new Set<string>(s.turns.map((r) => r.id))
    for (const r of turns) {
      items.append(row(r))
      // The experiment slot (§2): runs that forked from this turn nest
      // under it; empty without the playground capability.
      const expts = s.experiments.get(r.id) ?? []
      if (expts.length)
        items.append(
          el("div", "weft-expts", expts.map(row), { "data-key": `x:${r.id}` })
        )
    }
    // Experiments whose source turn is not in the list (older than the
    // page, or a run of another experiment) are still runs of this
    // conversation: listed, not dropped.
    const orphans: RunRow[] = []
    for (const [key, rows] of s.experiments)
      if (!shown.has(key)) orphans.push(...rows.filter((r) => this.match(r)))
    if (orphans.length)
      items.append(
        el("div", "weft-expts", orphans.map(row), { "data-key": "x:" })
      )
    // The marks a page could not read (U16-fix): said, never dropped.
    list.append(items, s.marksCapped ? holeLine(s.marksCapped) : "")
    // Paging (D4): the sentinel loads the next older page when the list
    // scrolls to it (watchOlder), or on a click; no cap.
    if (s.turnsCapped && this.model?.publicId) {
      const more = el(
        "button",
        "weft-btn weft-older",
        s.loadingOlder ? "loading older turns…" : "older turns ↓",
        {
          type: "button",
          title: "load the next older page of turns",
          // Keyed by the cursor: each page read (even one that added no
          // row) is a new sentinel, so the observer is armed again.
          "data-key": `older:${s.olderAt}`,
        }
      )
      on(more, "click", () => this.go(this.model?.loadOlder()))
      list.append(more)
    } else if (s.turnsCapped)
      list.append(
        el(
          "div",
          "weft-note",
          `the newest ${DEV_LIMIT} runs — older ones are in Studio (⤢)`
        )
      )
    else if (s.paged)
      list.append(
        el("div", "weft-tq-n weft-all", `all ${s.turns.length} turns loaded`)
      )
    return list
  }

  /** st words a turn (lib/status.ts): a session-scoped list holds every
   * turn newer than a loaded one, so its session turns' resumes were
   * looked for. */
  private st(r: RunRow): Status {
    const t = this.last.turns
    const c = statusOf(r, t, !!this.last.session)
    // A newer message ended it (thread's busy policy, U16): neutral,
    // "✗ interrupted by t8" / "↶ rolled back by t9 · off path" — the
    // interrupt record's turn and policy (the row's ended_*, U16-fix),
    // else the next turn, which a newer message started.
    if (r.cancel_cause !== BUSY_CAUSE) return c
    const by = r.ended_by || nextTurn(r, t)
    return {
      ...c,
      role: "resumed",
      word: TURN_CHIP.ended(
        r.ended_kind,
        by ? turnTag(by, t) : "a newer message"
      ),
      hint: TURN_CHIP.endedTitle,
    }
  }

  /** roveTo makes one turn row the list's tabbable one (D3). */
  private roveTo(n: HTMLElement, id: string) {
    this.rove = id
    for (const b of Array.from(
      this.body.querySelectorAll<HTMLElement>(".weft-turn")
    ))
      b.tabIndex = b === n ? 0 : -1
  }

  private footer(s: PanelState): HTMLElement {
    const line = "prompts, args and results from your app, via your Studio"
    const parts: HTMLElement[] = [el("span", "", line)]
    // The content line (D5): on or off and why, the recorder's cuts
    // counted — the shared table's words in its title.
    // Said only on evidence (nothing when unknown); the title's words
    // ride in the accessible text too.
    const cap = capLine(s.turn, s.meta, MAX_EVENT_PAGES * 500)
    if (cap)
      parts.push(
        el(
          "span",
          "weft-cap",
          [` · ${cap.text}`, el("span", "weft-sr", ` — ${cap.title}`)],
          {
            title: cap.title,
            "data-weft-cap": cap.hole ?? "",
          }
        )
      )
    // The resolved detection choice, one word (C3.2); and, when the
    // page's fetch could not be put back (another patcher wrapped it
    // after the panel), that too.
    const word = this.detectWord()
    parts.push(
      el(
        "span",
        "weft-detect",
        ` · detect: ${word}${this.rung?.chained ? " (chained)" : ""}${this.notRestored && !this.rung ? " (fetch not restored: patched after the panel)" : ""}`,
        {
          title: word.startsWith("url")
            ? "scope from the page URL's weft_scope"
            : word.includes("headers") || word === "markers"
              ? "reading Weft-Scope on same-origin fetches / data-weft-scope markers"
              : "scope from data-scope / window.__WEFT__",
        }
      )
    )
    if (this.globalNote)
      parts.push(el("span", "weft-global", ` · global: ${this.globalNote}`))
    return el("div", "weft-footer", parts, { "data-key": "foot" })
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
      // The source (lib/drawer-spec.ts's first section): the run, the
      // step it continues from.
      el(
        "span",
        "",
        `Experiment · ${d.agent}${child ? ` · child run ${d.runId}` : ""}${d.step > 0 ? ` · continue from step ${d.step}` : ""}`,
        { "data-drawer-section": "source" }
      ),
      el("span", "weft-grow"),
    ])
    head.append(
      clickBtn("–", () => this.closeDrawer(), {
        title: "close the drawer",
        "aria-label": "close the drawer",
      })
    )
    card.append(head)
    const root = el("div", "weft-step-b")
    // The verb that opened it (plan F1): Studio's replay drawer title.
    if (d.verb)
      root.append(
        el("div", "weft-verb-t", VERB_TITLES[d.verb], {
          "data-weft-drawer-verb": d.verb,
        })
      )
    // lib/drawer-spec.ts's order (U17): source, target, engine, what
    // changes, will be sent, tools during the replay, advanced, Run.
    const sec = (k: DrawerSection, tag = "div") => {
      const x = el(tag, "weft-field", "", { "data-drawer-section": k })
      if (DRAWER_TITLES[k])
        x.append(
          el(tag == "div" ? "span" : "summary", "weft-name", DRAWER_TITLES[k])
        )
      root.append(x)
      return x
    }
    // The target and its check (U18.2, lib/target.ts): the build line,
    // why Run is held, the picker, the one escape.
    const tc = targetFor(d, s.runtimes, s.preview?.doc)
    const tg = sec("target")
    // Runs in (U20.1, lib/runs-in.ts): the runtime by service, pid and
    // id, a select when two connected ones register the agent; else the
    // refusal — what is connected, the fix, the Studio asked.
    // No agent picked yet: the picker asks, no refusal (as Studio).
    if (d.agent) {
      const live = s.runtimes.filter((r) => r.connected !== false)
      // A disconnected registration runs nothing: the refusal, as Studio.
      const ok = agent && rt && rt.connected !== false
      tg.append(
        el(
          "div",
          ok ? "" : "weft-warn",
          [
            `${RUNS_IN.head}: ${ok ? runsInLine(rt) : RUNS_IN.none(d.agent)} · `,
            el("a", "", RUNS_IN.manage, {
              href: href(
                this.base,
                runtimesLink(ok ? { runtime: rt.id } : { agent: d.agent })
              ),
              ...NEW_TAB,
            }),
          ],
          { "data-runs-in": ok ? rt.id : "none" }
        )
      )
      const regs = registering(live, d.agent)
      if (!ok)
        tg.append(
          el(
            "div",
            "weft-note",
            `${RUNS_IN.connected(live)} · ${RUNS_IN.fix(d.agent)} · ${RUNS_IN.asked(this.base)}`
          )
        )
      else if (regs[1]) {
        const rs = el("select", "weft-input", "", {
          "aria-label": "runtime",
        }) as HTMLSelectElement
        for (const r of regs)
          option(rs, runsInLine(r), { value: r.id }, r.id == rt.id)
        on(rs, "change", (_, n) =>
          this.model?.pickTarget((n as HTMLSelectElement).value, d.agent)
        )
        tg.append(rs)
      }
    }
    // One note: the build line, else why Run is held.
    const tn = tc.build ? `! ${tc.build}` : tc.held
    if (tn)
      tg.append(
        el("div", `weft-note${tc.held ? " weft-warn" : ""}`, tn, {
          role: "status",
          [`data-target-${tc.build ? "build" : "held"}`]: tc.kind,
        })
      )
    const g = el("div", "", "", { role: "group", "aria-label": "target" })
    tg.append(g)
    const pick = (t: string, k: string, v: string, f: () => void) =>
      g.append(clickBtn(t, f, { [`data-target-${k}`]: v }))
    if (/^(un|cr)/.test(tc.kind))
      for (const o of agentOptions(s.runtimes, d.recordedTools))
        pick(o.label, "option", o.agent, () =>
          this.model?.pickTarget(o.runtime, o.agent)
        )
    if (tc.escape)
      pick(tc.escape, "escape", tc.kind, () =>
        this.model?.setDraft({ escaped: tc.key })
      )
    // The engine (lib/engine.ts): two radios, one line each; scripted
    // disabled with its reason when it cannot answer; a pick is never
    // switched behind the user — it is held, said inline.
    const ch = engineFor(d, s.preview?.doc)
    const eng = d.engine ?? ch.default
    const engRow = sec("engine")
    engRow.setAttribute("data-engine", eng)
    engRow.setAttribute("role", "radiogroup")
    engRow.setAttribute("aria-label", DRAWER_TITLES.engine)
    const off = !ch.scripted.enabled
    for (const [v, words, why] of [
      [
        "scripted",
        ENGINE_WORDS.scripted,
        off ? ch.scripted.reason : ENGINE_WORDS.scriptedFor,
      ],
      ["live", ENGINE_WORDS.live(ch.live.model, ch.live.estimate)],
    ] as const) {
      const r = el("input", "", "", {
        type: "radio",
        name: "weft-engine",
      }) as HTMLInputElement
      r.checked = eng === v
      r.disabled = v == "scripted" && off
      on(
        r,
        "change",
        (_, n) =>
          !(n as HTMLInputElement).disabled &&
          this.model?.setDraft({ engine: v })
      )
      engRow.append(
        el(
          "label",
          "weft-tool",
          [
            r,
            words,
            ...(why
              ? [el("span", "weft-reason", why, { "data-engine-why": "" })]
              : []),
          ],
          {
            "data-engine-option": v,
          }
        )
      )
    }
    const engHeld = engineHeld(ch, d.engine)
    if (engHeld)
      engRow.append(
        el("div", "weft-note weft-warn", engHeld, {
          role: "alert",
          "data-engine-refused": "",
        })
      )
    let body = sec("changes")
    // The command's edits by kind (plan F2), each droppable.
    const list = editList(d.edits, (e) => {
      const k = `${d.runId}|${editKey(e)}`
      this.edOpen.delete(k)
      this.edText.delete(k)
      this.edErr.delete(k)
      this.model?.setDraft({
        edits: putEdit(this.model.state.drawer?.edits ?? [], e, true),
      })
    })
    const fork = !!list && d.thread === "fork"
    if (list) body.append(list)
    if (fork)
      body.append(
        el("div", "weft-note weft-warn", FORK_EDITS, {
          role: "alert",
          "data-weft-fork-edits": "",
        })
      )
    // An edit inside the view from_step's request carried: the server's
    // refusal (ADR 0029 decision 4) said first, from transcript?step.
    const srcT = s.turn
      ? d.runId === s.turn.id
        ? s.turn
        : s.turn.children.get(d.runId)
      : undefined
    const inView =
      fork || !srcT?.transcript
        ? ""
        : compactedRefusal(
            srcT.transcript.batches,
            d.edits,
            d.step,
            this.model?.compactedAt(d.runId, d.step)
          )
    if (inView)
      body.append(
        el("div", "weft-note weft-warn", inView, {
          role: "alert",
          "data-weft-view-edit": "",
        })
      )
    // The field the verb opens on gets focus once per opening (draw()).
    // Focus moves into the drawer once per opening: the verb's field,
    // else the prompt.
    if (d.key !== undefined && d.key !== this.focusedKey && d.verb !== "edit") {
      this.focusedKey = d.key
      const e = d.at ?? d.edits.at(0)
      this.focusNext =
        d.focus === "edit" && e
          ? `edit:${e.step}:${e.callID}`
          : d.focus === "input"
            ? "input"
            : "prompt"
    }

    // System prompt, with the registered config one reset away.
    const promptRow = el("label", "weft-field", [
      el("span", "", "System prompt"),
    ])
    const ta = this.field(
      el("textarea", "weft-input") as HTMLTextAreaElement,
      "prompt"
    )
    ta.rows = 3
    ta.value = d.instructions
    on(ta, "input", (_, n) =>
      this.model?.setDraft(
        { instructions: (n as HTMLTextAreaElement).value },
        true
      )
    )
    const reset = el("button", "weft-btn", "↺", {
      title: "reset to the registered prompt",
      "aria-label": "reset to the registered prompt",
    })
    on(reset, "click", () => {
      this.model?.setDraft({ instructions: d.registeredInstructions })
    })
    promptRow.append(ta, reset)
    body.append(promptRow)
    // edit the prompt: the step's own system text when its request
    // record holds it whole, else the registered prompt — said.
    if (d.promptFrom === "registered") {
      const note = el("div", "weft-note", "", {
        "data-weft-prompt-from": "registered",
      })
      if (d.promptHole) note.append(badge(d.promptHole.hole, d.promptHole), " ")
      note.append("step prompt unreadable · the registered one pre-filled")
      body.append(note)
    }

    // Tools: turning off is narrowing; ⚠ marks the side-effect class.
    if (agent?.tools.length) {
      const toolsRow = el("div", "weft-field", [el("span", "", "Tools")])
      for (const t of agent.tools) {
        const cb = el("input") as HTMLInputElement
        cb.type = "checkbox"
        cb.checked = d.tools[t.name] ?? true
        on(cb, "change", (_, n) =>
          this.model?.setDraft({
            tools: {
              ...(this.model.state.drawer?.tools ?? d.tools),
              [t.name]: (n as HTMLInputElement).checked,
            },
          })
        )
        const lab = el("label", "weft-tool", [cb, el("span", "", t.name)])
        if (t.side_effects === "never" || !t.side_effects) {
          lab.append(
            el("span", "weft-badge weft-warn-badge", "⚠", {
              title: SIDE_EFFECT_TOOL,
            })
          )
        }
        toolsRow.append(lab)
      }
      body.append(toolsRow)
    }

    // Model, thinking, input — the row of small selects.
    const opts = el("div", "weft-fields")
    const modelSel = el("select", "weft-input", "", {
      "aria-label": "model",
    }) as HTMLSelectElement
    const own = s.turn?.doc?.model.name ?? ""
    option(modelSel, `model: ${own || "—"}`, { value: "" })
    for (const m of agent?.models ?? [])
      if (m !== own) option(modelSel, modelLabel(m, d.own), { value: m })
    // The free text as typed (modelFree); the model sent is its trim.
    const typed =
      d.modelFree ??
      (d.model && !(agent?.models ?? []).includes(d.model) ? d.model : "")
    const listed = !typed.trim()
    modelSel.value = listed ? d.model : ""
    if (d.model) modelSel.classList.add("weft-ovr")
    on(modelSel, "change", (_, n) =>
      this.model?.setDraft({
        model: (n as HTMLSelectElement).value,
        modelFree: "",
      })
    )
    opts.append(modelSel)
    // Any model name, when the app holds a runtime.ModelResolver (plan
    // F3): the app decides; its refusal is the command's reason.
    if (agent?.resolver) {
      const free = el("input", "weft-input" + (listed ? "" : " weft-ovr"), "", {
        "aria-label": "model name the app resolves",
        placeholder: "or any model: the app resolves it",
      }) as HTMLInputElement
      free.value = typed
      on(free, "input", (_, n) => {
        const v = (n as HTMLInputElement).value
        this.model?.setDraft({ model: v.trim(), modelFree: v })
      })
      opts.append(free)
    }
    const thinkSel = el("select", "weft-input", "", {
      "aria-label": "thinking",
    }) as HTMLSelectElement
    option(
      thinkSel,
      `thinking: default${agent?.defaults?.thinking ? ` (${agent.defaults.thinking})` : ""}`,
      { value: "" }
    )
    for (const lvl of ["off", "low", "medium", "high"])
      option(thinkSel, lvl, { value: lvl })
    thinkSel.value = d.thinking
    on(thinkSel, "change", (_, n) =>
      this.model?.setDraft({ thinking: (n as HTMLSelectElement).value })
    )
    opts.append(thinkSel)
    body.append(opts)

    // Input replaces the turn's user message (a whole-turn re-run).
    if (d.step === 0) {
      const inputRow = el("label", "weft-field", [
        el("span", "", "Input (replaces the user message)"),
      ])
      const inTa = this.field(
        el("textarea", "weft-input") as HTMLTextAreaElement,
        "input"
      )
      inTa.rows = 2
      inTa.value = d.input
      if (d.sourceInput && d.thread !== "fork") inTa.placeholder = d.sourceInput
      on(inTa, "input", (_, n) => {
        const v = (n as HTMLTextAreaElement).value
        this.model?.setDraft({ input: v }, true)
        // A fork's Run waits for its message (no redraw while typing).
        const btn = n
          .closest(".weft-drawer")
          ?.querySelector<HTMLButtonElement>(".weft-run-btn")
        if (btn)
          btn.disabled =
            btn.hasAttribute("data-weft-held") ||
            (this.model?.state.drawer?.thread === "fork" && !v.trim())
      })
      inputRow.append(inTa)
      body.append(inputRow)
    }

    // The transcript edits (D2/D3): when continuing from a step, the
    // kept steps' results are patchable and their call-free replies
    // rewritable — the counterfactual the fresh step answers.
    let unplaced = 0
    // The run the drawer replays: the turn, or a child of it (A10).
    const src = s.turn
      ? d.runId === s.turn.id
        ? s.turn
        : s.turn.children.get(d.runId)
      : undefined
    if (d.step > 0 && src) {
      const draft = (): ExperimentDraft => this.model?.state.drawer ?? d
      const editsBox = el("div", "weft-field")
      editsBox.append(
        el("span", "", `Transcript edits (steps 0..${d.step - 1} are kept)`)
      )
      // Steps are numbered the way from_step and the edits count them
      // on the wire: by the step's own ordinal (its stored index).
      const placed = new Set<string>()
      for (const step of src.folded.steps) {
        const n = step.index
        if (n >= d.step) continue
        for (const call of step.toolCalls) {
          if (!call.result) continue
          const lab = el("label", "weft-edit", "", {
            "data-key": `${n}:${call.callId}`,
          })
          lab.append(el("span", "", `step ${n} · ${call.name} →`))
          const inp = this.field(
            el("input", "weft-input") as HTMLInputElement,
            `edit:${n}:${call.callId}`
          )
          inp.placeholder = String(call.result.content).slice(0, 60)
          const editOf = () =>
            draft().edits.find(
              (e) =>
                e.step === n &&
                e.callID === call.callId &&
                kindOf(e) === "tool_result"
            )
          inp.value = editOf()?.toolResult ?? ""
          on(inp, "input", (_, f) => {
            const v = (f as HTMLInputElement).value
            const cur = editOf()
            const next = [...draft().edits]
            const i = cur ? next.indexOf(cur) : -1
            // The recording itself is no edit (U17).
            if (noEdit(v, String(call.result?.content))) {
              if (i >= 0) next.splice(i, 1)
            } else if (i >= 0) {
              next[i] = { ...cur, toolResult: v, step: n, callID: call.callId }
            } else {
              next.push({ step: n, callID: call.callId, toolResult: v })
            }
            this.model?.setDraft({ edits: next }, true)
          })
          lab.append(inp)
          editsBox.append(lab)
          placed.add(`${n}:${call.callId}`)
        }
        if (step.text && !step.toolCalls.length) {
          const lab = el("label", "weft-edit", "", {
            "data-key": `${n}`,
          })
          lab.append(el("span", "", `step ${n} · reply`))
          const reply = this.field(
            el("textarea", "weft-input") as HTMLTextAreaElement,
            `edit:${n}`
          )
          reply.rows = 2
          const editOf = () =>
            draft().edits.find((e) => e.step === n && kindOf(e) === "reply")
          reply.value = editOf()?.content ?? ""
          on(reply, "input", (_, f) => {
            const v = (f as HTMLTextAreaElement).value
            const cur = editOf()
            const next = [...draft().edits]
            const i = cur ? next.indexOf(cur) : -1
            if (noEdit(v, step.text)) {
              if (i >= 0) next.splice(i, 1)
            } else if (i >= 0) {
              next[i] = { ...cur, content: v, step: n }
            } else {
              next.push({ step: n, content: v })
            }
            this.model?.setDraft({ edits: next }, true)
          })
          lab.append(reply)
          editsBox.append(lab)
          placed.add(`${n}:`)
        }
      }
      // A pre-filled edit is never dropped silently: one whose call
      // the steps as read do not hold is shown, badged, and holds Run.
      for (const e of d.edits) {
        // The Story's own kinds (plan F2) name what the editor read.
        if (
          !["tool_result", "reply"].includes(kindOf(e)) ||
          placed.has(`${e.step}:${e.callID ?? ""}`)
        )
          continue
        unplaced++
        editsBox.append(
          el(
            "div",
            "weft-edit",
            [
              badge("gap"),
              ` step ${e.step} · ${e.callID ?? "reply"} → ${e.toolResult ?? e.content ?? ""} · not in the steps read · Run held`,
            ],
            {
              "data-key": `u:${e.step}:${e.callID ?? ""}`,
              "data-weft-unplaced": "",
            }
          )
        )
      }
      if (editsBox.childElementCount > 1) body.append(editsBox)
    }

    // What the replay's first step will send (plan F2): its 400 holds Run.
    if (s.preview) {
      const pb = previewBlock(s.preview, d.step, this.openKeys)
      pb.setAttribute("data-drawer-section", "send")
      root.append(pb)
    }
    // The ack preview (plan F1, ack-before-execute): what each tool's
    // calls would do, and which prefix is kept — redrawn with the draft.
    const refused = this.ack(s, d, src, agent, sec("tools"))
    // Advanced, folded (open while one of its knobs is set).
    body = sec("advanced", "details")
    body.setAttribute("data-weft-open", "adv")
    if (
      this.openKeys.has("adv") ||
      d.thread == "fork" ||
      (d.sideEffects && d.sideEffects != "substitute") ||
      Object.keys(labOf(d).overrides).length
    )
      body.setAttribute("open", "")
    // The side-effect mode (§6 rule 3): substitute (the default — a
    // recorded side-effect call answers from the record, a miss parks),
    // park (every side-effect call waits), allow (the tools the app
    // opted in with AllowSideEffects run for real — only here; a
    // ReplaySafe tool runs in every mode).
    const seRow = el("div", "weft-fields")
    const seSel = el("select", "weft-input", "", {
      "aria-label": "side effects",
    }) as HTMLSelectElement
    // Studio's words (lib/drawer-spec.ts); what each does per tool is
    // section 3's verdicts.
    for (const [v, w] of Object.entries(SIDE_EFFECT_WORDS))
      option(seSel, v == "substitute" ? `side effects: ${w}` : w, {
        value: v == "substitute" ? "" : v,
      })
    seSel.value = d.sideEffects === "substitute" ? "" : d.sideEffects
    on(seSel, "change", (_, n) =>
      this.model?.setDraft({
        sideEffects: (n as HTMLSelectElement)
          .value as ExperimentDraft["sideEffects"],
      })
    )
    seRow.append(seSel)
    // Fork mode (§5.4, review fix 4a — P4 renders in both surfaces):
    // ephemeral (an experiment, never a turn) or fork (a new session
    // with lineage, the input its next turn). Fork needs an input —
    // the runtime refuses the command otherwise.
    const thrSel = el("select", "weft-input", "", {
      "aria-label": "thread",
    }) as HTMLSelectElement
    option(thrSel, "thread: ephemeral", { value: "ephemeral" })
    option(thrSel, "fork (new session)", { value: "fork" })
    thrSel.value = d.thread
    on(thrSel, "change", (_, n) => {
      const thread = (n as HTMLSelectElement).value as ExperimentDraft["thread"]
      // A fork continues the conversation: it starts at step 0; back to
      // ephemeral, the step the edits imply returns.
      this.model?.setDraft({
        thread,
        step:
          thread === "fork" ? 0 : Math.max(d.step, impliedFromStep(d.edits)),
      })
    })
    seRow.append(thrSel)
    body.append(seRow)
    if (agent) body.append(this.lab(d, agent))

    // Runtime-wide breakpoints are the runtime's, not this replay's
    // (U17): a pointer to where they are set.
    if (hasCapability(s, "breakpoints"))
      body.append(
        el("a", "weft-reason", BREAKPOINTS_MOVED, {
          href: href(this.base, runtimesLink()),
          ...NEW_TAB,
          "data-breakpoints-moved": "",
        })
      )
    body = root
    const bad = [...this.edErr.values()][0]
    if (bad)
      body.append(
        el(
          "div",
          "weft-note weft-warn",
          `an edit is refused in the editor: ${bad}`,
          { role: "alert", "data-weft-ed-held": "" }
        )
      )
    const run = el("button", "weft-run-btn", runLabel(ch, d.engine), {
      "data-run-engine": eng,
      "data-drawer-section": "run",
    })
    const held =
      tc.held ||
      engHeld ||
      refused ||
      unplaced ||
      labOf(d).problems.length ||
      bad ||
      s.preview?.refused ||
      s.preview?.pending ||
      fork ||
      inView
    if (held) run.setAttribute("data-weft-held", "")
    if (held || (d.thread === "fork" && !d.input.trim()))
      run.setAttribute("disabled", "")
    on(
      run,
      "click",
      (_, n) =>
        !n.hasAttribute("disabled") && this.go(this.model?.runExperiment())
    )
    body.append(run)
    card.append(root)
    return card
  }

  /** runBtn is a button that opens a run in the panel (the decided and
   * resumed cards' links). */
  private runBtn(
    label: string,
    id: string,
    attrs?: Record<string, string>
  ): HTMLElement {
    return clickBtn(label, () => this.go(this.model?.select(id, true)), attrs)
  }

  /** tabs is the turn view's tablist (D4): ARIA tabs, one tab stop,
   * ←/→ (Home/End) move and select. */
  private tabs(): HTMLElement {
    const bar = el("div", "weft-tabs", "", {
      role: "tablist",
      "aria-label": "turn views",
      "data-key": "tabs",
    })
    for (const id of TABS) {
      const sel = id === this.lay.tab
      const b = el(
        "button",
        `weft-tab${sel ? " weft-active" : ""}`,
        id[0].toUpperCase() + id.slice(1),
        {
          type: "button",
          role: "tab",
          id: `weft-tab-${id}`,
          "aria-selected": String(sel),
          tabindex: sel ? "0" : "-1",
          "data-key": `tab:${id}`,
          // Only the drawn panel exists to be controlled.
          ...(sel ? { "aria-controls": `weft-tp-${id}` } : {}),
        }
      )
      on(b, "click", () => this.setTab(id))
      bar.append(b)
    }
    on(bar, "keydown", (e) => {
      const k = (e as KeyboardEvent).key
      const i = TABS.indexOf(this.lay.tab)
      const n = TABS.length
      const j =
        k === "ArrowRight"
          ? i + 1
          : k === "ArrowLeft"
            ? i - 1
            : k === "Home"
              ? 0
              : k === "End"
                ? n - 1
                : null
      if (j === null) return
      e.preventDefault()
      const id = TABS[(j + n) % n]
      this.setTab(id)
      this.body.querySelector<HTMLElement>(`#weft-tab-${id}`)?.focus()
    })
    return bar
  }

  // ── The transcript editor (plan F2) ──────────────────────────────
  // The Story's own nodes become editable: a click (or Enter) opens a
  // textarea in place; what it holds becomes an edit of the drawer's
  // one command (opened on the first one). Args are JSON, checked by
  // lib/edits.ts's checkArgs against the step's catalog schema — a
  // refusal is said on the field and holds Run. Open editors, the text
  // of a refused one and its refusal live here, by edit key.
  private edOpen = new Set<string>()
  /** A field's typed text, and what it put into the command then (its
   * edit's JSON and its refusal): when the command holds something
   * else — the drawer closed, another verb, a drop, the drawer's own
   * field — the text is stale and the field follows the command. */
  private edText = new Map<string, { text: string; put: string }>()
  private edErr = new Map<string, string>()

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
      base: this.base || this.cfg.endpoint,
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
      compaction: (c) =>
        compactionBox(c, compactionsOf(t.doc), t.transcript, open),
      child: (call) => {
        const id = call.childRunId ?? ""
        const cv = t.children.get(id)
        const name = el("span", "weft-name", call.name, { title: id })
        const box = el(
          "div",
          "weft-call",
          [
            el("div", "weft-call-h", [
              name,
              el("span", "weft-args", "subagent · step 0"),
            ]),
          ],
          {
            "data-key": `sub:${call.callId}`,
            "data-weft-rq-child": id,
          }
        )
        // Read on demand, as the Story's expander reads it (A10): the
        // child's request line once read (its hole without the record),
        // a hand-off when its history could not be read.
        if (cv)
          box.append(
            cv.requests
              ? requestLine(0, cv.requests, cv.status, open)
              : el(
                  "div",
                  "weft-req",
                  requestHole("not_recorded", {
                    cause: "not_served",
                    reason: s.meta?.capabilities_off?.requests,
                  })
                )
          )
        else if (t.unreachable.has(id))
          box.append(
            el("div", "weft-reason", [
              el("span", "", "child's history unreachable · "),
              el("a", "", "open in Studio (⤢)", {
                href: studioLink(this.base, id),
                ...NEW_TAB,
                "data-weft-handoff": id,
              }),
            ])
          )
        else if (t.tried.has(id))
          box.append(el("div", "weft-reason", "reading…"))
        else
          box.append(
            on(
              el("button", "weft-btn", "read its turn", { type: "button" }),
              "click",
              () => this.go(this.model?.expandChild(id))
            )
          )
        return box
      },
    })
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
        t.querySelector("summary")?.setAttribute(
          "aria-expanded",
          String(isOpen)
        )
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
      // A hand-off link opens through the panel token's hand-off (G3).
      const a = e.target.closest("a[data-weft-handoff]")
      if (a && this.handoff((a as HTMLAnchorElement).href)) e.preventDefault()
      const target = e.target.closest("[data-weft-step]")
      if (!target) return
      const n = Number(target.getAttribute("data-weft-step"))
      if (Number.isFinite(n)) this.model?.selectStep(n)
      // A Timeline row opens its step in Story (U8.3); its ⤢ hands off.
      if (target.matches(".weft-tl") && !e.target.closest("a"))
        this.setTab("story")
    })
    if (s.tooNew && s.meta) {
      main.append(
        el("div", "weft-note weft-warn", [
          el(
            "span",
            "weft-warn",
            "Studio is newer than this panel; update panel.js"
          ),
          el(
            "span",
            "",
            `studio_version ${s.meta.studio_version} · panel built for ${panelStudioVersion()}`
          ),
        ])
      )
      return main
    }
    if (!s.turn) {
      // Only when there is a turn to select (UX-F-33): the list's own
      // empty line says the rest.
      if (s.turns.length) main.append(el("div", "weft-splash", "select a turn"))
      return main
    }
    // The turn's tabs (D4): Story (the step story and the playground),
    // Request (E1.2 fills it), Timeline, Raw.
    main.append(this.tabs())
    const tab = this.lay.tab
    const t = s.turn
    const panel = (tp: HTMLElement, id: string) => {
      const at = {
        class: "weft-tp",
        role: "tabpanel",
        id: `weft-tp-${id}`,
        "aria-labelledby": `weft-tab-${id}`,
        "data-key": `tp:${id}`,
      }
      for (const [k, v] of Object.entries(at)) tp.setAttribute(k, v)
      main.append(tp)
      return tp
    }
    // The story's own box is its panel (its notes stay its children),
    // drawn on every tab and hidden on the others: its node — the
    // <details> opened, its scroll — stays across a tab switch.
    const story = this.turnView(s)
    story.append(this.playgroundArea(s))
    if (tab !== "story") story.setAttribute("hidden", "")
    panel(story, "story")
    if (tab !== "story")
      panel(
        el("div", "", [
          // A derived turn's Timeline and Raw say why no event is there.
          ...(tab !== "request" && this.derived(t)
            ? [holeLine(DERIVED_HOLE)]
            : []),
          tab === "raw"
            ? this.rawView(t)
            : tab === "timeline"
              ? renderTimeline(t, s.selectedStep, this.base)
              : this.requestView(s, t),
        ]),
        tab
      )
    return main
  }

  /** The drawer opening the three maps belong to: another (or none)
   * clears them, so a refusal never holds an unrelated drawer's Run. */
  private edKey?: number

  /** closeDrawer closes the experiment drawer and gives focus back to
   * the verb that opened it (the close button, Escape inside it). */
  private closeDrawer() {
    const back = this.opener
    this.opener = null
    this.model?.closeExperiment()
    if (back?.isConnected) back.focus({ preventScroll: true })
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
    if (s.result && s.result.under === here)
      box.append(this.experimentResult(s))
    if (!this.canAct(s)) return box
    if (s.drawer && (s.drawer.under ?? s.drawer.runId) === here)
      box.append(this.drawer(s))
    return box
  }

  /** resumeChip is a turn's status chip; a decided park's hands off to
   * its resume (U1-fix, Studio's link): a click selects that turn when
   * loaded — the parked block's "resumed as" is the keyboard path. */
  private resumeChip(c: Status): HTMLElement {
    const n = el("span", `weft-chip weft-st weft-${c.role}`, c.word, {
      title: c.hint,
    })
    const to = c.resumedBy
    if (to && this.rowOf(to)) {
      n.setAttribute("data-weft-resume", to)
      on(n, "click", (e) => {
        e.stopPropagation()
        this.go(this.model?.select(to, true))
      })
    }
    return n
  }

  private rowOf(id: string): RunRow | undefined {
    return this.model?.rowOf(id)
  }

  /** apiObjIs reports whether api is this panel's API object. */
  apiObjIs(api: unknown): boolean {
    return this.apiObj !== null && api === this.apiObj && this.isConnected
  }

  /** lab is the option lab (plan F3; Studio's OptionLab): every
   * narrowing or neutral knob the command carries, the agent's default
   * greyed as the placeholder, an override in colour with a reset; the
   * rules are lib/experiment-body.ts's (the server's, by name) and a
   * problem holds Run. A registration without defaults greys the knobs
   * it cannot carry, and says why. */
  private lab(d: ExperimentDraft, agent: AgentView): HTMLElement {
    const lab = d.lab ?? emptyLab()
    const def = agent.defaults
    const old = labLacksDefaults(agent)
    const set = (p: Partial<LabFields>) =>
      this.model?.setDraft({
        lab: { ...(this.model.state.drawer?.lab ?? emptyLab()), ...p },
      })
    const { problems } = labOf(d)
    const box = el("div", "weft-field weft-lab", "", { "data-weft-lab": "" })
    if (old)
      box.append(
        el(
          "div",
          "weft-note",
          `this runtime reports no defaults — ${LAB_PREDATES}`,
          { "data-weft-lab-old": "" }
        )
      )
    const said = (k: keyof LabFields | "lab", into: HTMLElement) => {
      for (const p of problems)
        if (p.field === k)
          into.append(
            el("div", "weft-lab-p", p.message, {
              role: "alert",
              "data-weft-lab-problem": k,
            })
          )
    }
    said("lab", box)
    const row = (k: keyof LabFields, input: HTMLElement) => {
      const lb = el(
        "label",
        "weft-lab-k",
        [el("span", "", LAB_LABELS[k]), input],
        { "data-key": `lab:${k}` }
      )
      if (old && LAB_NEW.includes(k)) input.setAttribute("disabled", "")
      if (labOn(lab, k, def)) {
        input.classList.add("weft-ovr")
        input.setAttribute("data-weft-override", "")
        const r = el("button", "weft-btn", "↺", {
          type: "button",
          "aria-label": `reset ${LAB_LABELS[k]} to the agent's default`,
        })
        on(r, "click", () =>
          set(
            k === "tool_choice"
              ? { tool_choice: "", tool_choice_name: "" }
              : { [k]: Array.isArray(lab[k]) ? [] : "" }
          )
        )
        lb.append(r)
      }
      said(k, lb)
      box.append(lb)
    }
    for (const k of [...LAB_NUMBERS, "stop" as const]) {
      const inp = el(k === "stop" ? "textarea" : "input", "weft-input", "", {
        "aria-label": LAB_LABELS[k],
        placeholder:
          labDefault(k, def) ||
          (old ? "" : k === "stop" ? "none" : "adapter default"),
      }) as HTMLInputElement
      inp.value = lab[k]
      on(inp, "input", (_, n) => set({ [k]: (n as HTMLInputElement).value }))
      row(k, inp)
    }
    const tc = el("select", "weft-input", "", {
      "aria-label": LAB_LABELS.tool_choice,
    }) as HTMLSelectElement
    const dtc = def?.tool_choice
    for (const m of ["", "auto", "any", "none", "named"])
      option(
        tc,
        m ||
          `default${dtc?.mode ? ` (${dtc.mode}${dtc.name ? ` ${dtc.name}` : ""})` : ""}`,
        { value: m }
      )
    tc.value = lab.tool_choice
    on(tc, "change", (_, n) =>
      set({
        tool_choice: (n as HTMLSelectElement).value as LabFields["tool_choice"],
      })
    )
    row("tool_choice", tc)
    const names = agent.tools.map((t) => t.name)
    const isOff = (n: string) => d.tools[n] === false
    if (lab.tool_choice === "named") {
      const ns = el("select", "weft-input", "", {
        "aria-label": "tool choice: the tool",
      }) as HTMLSelectElement
      for (const n of ["", ...names]) {
        const o = option(ns, n || "(pick a tool)", { value: n })
        // A tool the run turns off or parks cannot be forced: greyed.
        if (
          n &&
          ((lab.only_tools.length ? !lab.only_tools.includes(n) : isOff(n)) ||
            lab.park_on.includes(n))
        )
          o.disabled = true
      }
      ns.value = lab.tool_choice_name
      on(ns, "change", (_, n) =>
        set({ tool_choice_name: (n as HTMLSelectElement).value })
      )
      row("tool_choice_name", ns)
    }
    for (const k of ["only_tools", "park_on"] as const) {
      const g = el("div", "weft-lab-k", "", {
        role: "group",
        "aria-label": LAB_LABELS[k],
        "data-key": `lab:${k}`,
      })
      g.append(
        el(
          "span",
          "",
          `${LAB_LABELS[k]} · default ${k === "park_on" ? "none" : "every tool"}`
        )
      )
      for (const n of names) {
        const cb = el("input", "", "", {
          "aria-label": `${LAB_LABELS[k]}: ${n}`,
        }) as HTMLInputElement
        cb.type = "checkbox"
        cb.checked = lab[k].includes(n)
        // only_tools narrows tools_enabled: a tool turned off is greyed.
        // A checked box stays clearable after its tool is turned off.
        cb.disabled = old || (k === "only_tools" && isOff(n) && !cb.checked)
        on(cb, "change", (_, b) => {
          const cur = this.model?.state.drawer?.lab?.[k] ?? []
          set({
            [k]: (b as HTMLInputElement).checked
              ? [...cur, n]
              : cur.filter((x) => x !== n),
          })
        })
        g.append(
          el("span", "weft-tool" + (cb.checked ? " weft-ovr" : ""), [
            cb,
            el("span", "", n),
          ])
        )
      }
      if (labOn(lab, k, def)) {
        const r = el("button", "weft-btn", "↺", {
          type: "button",
          "aria-label": `reset ${LAB_LABELS[k]} to the agent's default`,
        })
        on(r, "click", () => set({ [k]: [] }))
        g.append(r)
      }
      said(k, g)
      box.append(g)
    }
    return box
  }

  /** actions is §3's row: ✎ Experiment (the drawer), ↻ Re-run (the
   * drawer as the rerun() draft — the whole turn, its prompt, tools and
   * model kept, its transcript edits dropped — posted only by Run after
   * the ack preview), ⎇ Continue from
   * the step being read. */
  private actions(s: PanelState): HTMLElement {
    const t = s.turn
    if (!t) return el("div")
    const row = el("div", "weft-actions")
    row.append(
      clickBtn(
        "✎ Experiment",
        () => this.go(this.model?.openExperiment(t.id, 0)),
        {
          title:
            "open the experiment drawer, pre-filled from the registered config",
        }
      )
    )
    const again = el("button", "weft-btn", "↻ Re-run", {
      title:
        "re-run the whole turn from step 0: the drawer keeps its prompt, tools and model, drops transcript edits — Run after the ack",
    })
    on(again, "click", () => {
      this.edSync(this.edKey, true)
      this.go(this.model?.rerun(t.id))
    })
    row.append(again)
    // The step being read, by its ordinal (from_step's count).
    const from = readStep(t.folded, s.selectedStep)
    if (from > 0) {
      const cont = el("button", "weft-btn", `⎇ Continue from step ${from}`, {
        title:
          "keep the transcript through the previous step (edits apply) and run this step fresh",
      })
      on(cont, "click", () =>
        this.go(this.model?.openExperiment(t.id, replayFromStep(from)))
      )
      row.append(cont)
    }
    return row
  }

  private edBox(
    s: PanelState,
    t: TurnView,
    target: ReplayEdit,
    recorded: string,
    shown: HTMLElement,
    label: string,
    tool: string
  ): HTMLElement {
    const ek = editKey(target)
    // By run too: wefttest's call ids (c1) repeat across runs.
    const key = `${t.id}|${ek}`
    const k = kindOf(target)
    const d = s.drawer?.runId === t.id ? s.drawer : null
    const cur = d?.edits.find((e) => editKey(e) === ek)
    const err = this.edErr.get(key)
    const held = `${cur ? JSON.stringify(cur) : ""}\u0000${err ?? ""}`
    const mem = this.edText.get(key)
    if (mem && mem.put !== held) {
      this.edText.delete(key)
      if (held === "\u0000") this.edOpen.delete(key)
    }
    if (!this.edOpen.has(key) && !cur && !err) {
      const open = (e: Event) => {
        e.stopPropagation()
        const go = () => {
          const nd = this.model?.state.drawer
          if (nd?.runId !== t.id) return
          this.edSync(nd.key)
          this.edOpen.add(key)
          this.focusNext = `ed:${ek}`
          this.render(this.last)
        }
        // The editor opens once the drawer is there: the edits are its
        // command's, and nothing typed lands before it.
        if (d) go()
        else
          this.go(
            this.model
              ?.openExperiment(
                t.id,
                editTranscript(impliedFromStep([target])),
                { under: t.id }
              )
              .then(go)
          )
      }
      shown.setAttribute("data-weft-editable", k)
      shown.setAttribute("role", "button")
      shown.setAttribute("tabindex", "0")
      // Its name is its visible text (axe's label-content-name-mismatch);
      // "edit …" is the title, its description. Only an empty field
      // (a "" result) takes it as its name (P7-fix3).
      if (!shown.textContent) shown.setAttribute("aria-label", `edit ${label}`)
      shown.title = `edit ${label}`
      on(shown, "click", open)
      on(shown, "keydown", (e) => {
        const kk = (e as KeyboardEvent).key
        if (kk === "Enter" || kk === " ") {
          e.preventDefault()
          open(e)
        }
      })
      return shown
    }
    const box = el("div", "weft-ed", "", {
      "data-weft-ed": k,
      "data-key": `ed:${ek}`,
    })
    const head = el("div", "weft-call-h", [
      el(
        "span",
        "weft-name",
        k === "insert" ? "inserted · user" : `edit · ${label}`
      ),
    ])
    if (cur)
      head.append(
        el(
          "span",
          "weft-badge weft-err",
          k === "insert" ? "inserted" : "edited",
          { "data-weft-edited": "" }
        )
      )
    const rv = el("button", "weft-btn", "revert", {
      type: "button",
      "aria-label": `revert ${label}`,
    })
    on(rv, "click", () => {
      this.edOpen.delete(key)
      this.edText.delete(key)
      this.edErr.delete(key)
      this.edPut(t, target, null)
      this.render(this.last)
    })
    head.append(rv)
    box.append(head)
    const ta = this.field(
      el("textarea", "weft-input", "", {
        "aria-label": `edit ${label}`,
      }) as HTMLTextAreaElement,
      `ed:${ek}`
    )
    ta.rows = k === "tool_args" ? 4 : 2
    ta.value =
      this.edText.get(key)?.text ??
      (cur
        ? k === "tool_args"
          ? JSON.stringify(cur.args, null, 2)
          : (cur.toolResult ?? cur.content ?? "")
        : recorded)
    if (err) ta.setAttribute("aria-invalid", "true")
    on(ta, "input", (_, n) => {
      const v = (n as HTMLTextAreaElement).value
      let next: ReplayEdit | null = null
      this.edErr.delete(key)
      if (v.trim() && v !== recorded) {
        if (k === "tool_args") {
          const r = checkArgs(tool, schemaOf(t.requests?.steps, tool), v)
          if (r.error) this.edErr.set(key, r.error)
          else next = { ...target, args: r.args }
        } else
          next =
            k === "tool_result"
              ? { ...target, toolResult: v }
              : { ...target, content: v }
      }
      this.edText.set(key, {
        text: v,
        put: `${next ? JSON.stringify(next) : ""}\u0000${this.edErr.get(key) ?? ""}`,
      })
      this.edPut(t, target, next)
    })
    box.append(ta)
    if (err)
      box.append(
        el("div", "weft-lab-p", err, {
          role: "alert",
          "data-weft-ed-error": "",
        })
      )
    return box
  }

  /** diffInto appends one before → after diff under its header, or
   * the note that sends a diff too large for the dock to Studio. */
  private diffInto(
    box: HTMLElement,
    title: string,
    before: string,
    after: string
  ) {
    const cells =
      (before.split("\n").length + 1) * (after.split("\n").length + 1)
    if (cells > DIFF_CELLS) {
      box.append(
        el(
          "div",
          "weft-diff-h",
          `${title}  too large for the panel — compare in Studio`
        )
      )
      return
    }
    const rows = diffLines(before, after)
    box.append(el("div", "weft-diff-h", `${title}  ${diffSummary(rows)}`))
    for (const row of rows) {
      if (row.kind === "same") continue
      box.append(
        el(
          "div",
          `weft-diff-row weft-diff-${row.kind}`,
          `${row.kind === "add" ? "+" : "−"} ${row.text}`
        )
      )
    }
  }

  private edCtx(s: PanelState, t: TurnView): EdCtx | undefined {
    const tr = t.transcript
    if (
      !this.canAct(s) ||
      !tr ||
      this.derived(t) ||
      (this.rowOf(t.id)?.status ?? t.doc?.status ?? "running") === "running"
    )
      return undefined
    const max = boundsOf(tr)?.max ?? null
    return {
      users: userMessagesOf(tr.batches),
      max,
      box: (target, recorded, shown, label, tool = "") =>
        // Only what a replay can keep (the from_step it implies within the bound).
        editable(target, max)
          ? this.edBox(s, t, target, recorded, shown, label, tool)
          : shown,
    }
  }

  /** ack draws the drawer's ack preview (plan F1; the run page's
   * ReplayAck): the prefix line and, per tool of the step's catalog
   * (the request record's replay classes; a hole badged and the
   * registered tools judged instead), whether its calls run, are
   * substituted or park, and why — lib/replay.ts's words. Returns
   * whether the server would refuse the command (side effects allow
   * over a tool not opted in): Run is then held. */
  private ack(
    s: PanelState,
    d: ExperimentDraft,
    src: TurnView | ChildView | undefined,
    agent: AgentView | undefined,
    into: HTMLElement
  ): boolean {
    const box = el("div", "weft-ack", "", {
      "data-weft-ack": "",
      "data-key": "ack",
    })
    const from = d.thread === "fork" ? 0 : d.step
    const compacted =
      from > 0 &&
      compactionsOf(src?.doc).some(
        (c) =>
          !isSessionMarker(c) && typeof c.step === "number" && c.step <= from
      )
    box.append(
      el("div", "weft-res", prefixLine(from, compacted), {
        "data-weft-prefix": "",
      })
    )
    // The replayed run not read (a child whose history did not load):
    // its catalog is the registration's, said.
    // A child's own status (ChildView.status, always a string) — read
    // by its name, never `"status" in src`: a quoted name on an internal
    // type is the mangle guard's refusal (scripts/panel-mangle.ts).
    const status =
      (src as { status?: string } | undefined)?.status ??
      this.rowOf(d.runId)?.status ??
      src?.doc?.status ??
      ""
    const cat = src
      ? catalogAt(
          src.requests,
          from,
          hasCapability(s, "requests"),
          boundsOf(src.transcript)?.stepCount ?? null,
          status === "running"
        )
      : {
          tools: null,
          hole: catalogReadError("the run's records could not be read"),
        }
    if (cat.hole) box.append(ackCatalogHole(cat.hole, !!agent))
    const names = Object.keys(d.tools)
    // only_tools narrows inside tools_enabled; park_on parks (plan F3).
    const lo = labOf(d).overrides
    const kept = lo.only_tools ?? names.filter((n) => d.tools[n])
    const verdicts = replayVerdicts({
      catalog:
        cat.tools ??
        (agent?.tools ?? []).map((t): CatalogTool => ({
          name: t.name,
          replay: t.side_effects === "safe" ? "safe" : "never",
          approval: false,
        })),
      agent,
      mode: d.sideEffects,
      toolsEnabled: kept.length < names.length ? kept : undefined,
      breakpoints: [
        ...breakpointsFor({ breakpoints: s.breakpoints }, agent),
        ...(lo.park_on ?? []),
      ],
    })
    if (cat.loading)
      box.append(el("div", "weft-reason", "reading the step's catalog…"))
    else if (cat.none)
      box.append(
        el(
          "div",
          "weft-reason",
          "the step offered no tools · nothing to substitute or park"
        )
      )
    else {
      const list = el("ul", "weft-verdicts", "", {
        "aria-label": "what each tool would do",
      })
      for (const v of verdicts)
        list.append(
          el(
            "li",
            `weft-v-${v.verdict}`,
            [
              el("span", "weft-name", v.name),
              el("span", "weft-v", v.verdict),
              el("span", "weft-reason", v.why),
            ],
            {
              "data-verdict": v.verdict,
              "data-tool": v.name,
              "data-key": v.name,
            }
          )
        )
      // Beside a hole the badge stands alone: an empty stand-in list is
      // not "no tools".
      if (!verdicts.length && !cat.hole)
        list.append(
          el("li", "weft-reason", "no tools · nothing to substitute or park")
        )
      if (list.childElementCount) box.append(list)
    }
    into.append(box)
    // The server's rule is over the registered tools left on, whatever
    // the step offered: allow runs only ReplaySafe tools and the ones
    // the app opted in (AllowSideEffects) — any other refuses it.
    const refused = allowRefusals(
      agent,
      d.sideEffects,
      kept.length < names.length ? kept : undefined
    )
    if (refused.length) {
      const one = refused.length === 1
      into.append(
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

  /** edCtx is the editor for a turn: the playground's verbs, a turn
   * that is over, its transcript read (the server's bounds). */
  /** derived: the turn was built by the reader from stock spans (plan
   * I1.3) — no transcript, so no replay verb, edit or export. */
  private derived(t: TurnView): boolean {
    return isDerived(this.rowOf(t.id) ?? t.doc)
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
    // The runs its calls started (U14): cut, hidden or unread — never
    // merged into a badge of the run's own (mergeHoles keeps one each).
    for (const m of [...holes, ...linkedHoles(t.doc ?? {})])
      box.append(holeLine(m))
    if (t.capped) {
      box.append(
        el(
          "div",
          "weft-note weft-warn",
          `a long run: the first ${MAX_EVENT_PAGES * 500} events are shown — the whole story is in Studio (⤢)`
        )
      )
    }
    return box
  }

  /** turnLinks is the turn's join keys as links (G1): its session and
   * its trace, each the page Studio shows it on. */
  private turnLinks(t: TurnView, s: PanelState): HTMLElement | null {
    const row = this.rowOf(t.id)
    const session = row?.session_id || t.doc?.session_id || ""
    const trace = row?.trace_id || t.doc?.trace_id || ""
    // Plan F6's "save as test": the run's wefttest zip.
    const save = mayExportTestWith(s.meta?.capabilities ?? [], this.cfg.token)
    if (!session && !trace && !save) return null
    const box = el("div", "weft-row2")
    if (save && this.derived(t))
      box.append(refusedVerb(SAVE_TEST_LABEL, "span"))
    else if (save) {
      // A hand-off (G1 style): Studio's run page downloads it with its
      // own bearer — the measured in-panel fetch-and-save cost ~0.8 KiB.
      // .weft-chip carries text-decoration: none (styles.ts).
      box.append(
        el("a", "weft-chip", SAVE_TEST_LABEL, {
          href: href(this.base, saveTestLink(t.id)),
          ...NEW_TAB,
          title: SAVE_TEST_TITLE,
          "data-weft-save-test": t.id,
        })
      )
    }
    const link = (
      label: string,
      url: string,
      title: string,
      attr: string,
      id: string
    ) =>
      el("a", "weft-chip", label, {
        href: url,
        ...NEW_TAB,
        title,
        [attr]: id,
      })
    if (session)
      box.append(
        link(
          `session ${session}`,
          href(this.base, sessionLink(session)),
          "the session in Studio",
          "data-weft-session-link",
          session
        )
      )
    if (trace)
      box.append(
        link(
          `trace ${trace.slice(0, 8)}`,
          href(this.base, tracePage(trace)),
          `the OTel trace ${trace} in Studio`,
          "data-weft-trace-link",
          trace
        )
      )
    return box
  }

  /** edPut puts (next null: drops) one edit into the drawer's command:
   * from_step follows the edits up (the engine is lib/engine.ts's). */
  private edPut(t: TurnView, target: ReplayEdit, next: ReplayEdit | null) {
    const m = this.model
    const d = m?.state.drawer
    if (!m || !d || d.runId !== t.id) {
      this.render(this.last)
      return
    }
    const edits = putEdit(d.edits, next ?? target, !next)
    // A fork starts at step 0: its from_step stays (the drawer holds Run).
    const step =
      d.thread === "fork" ? d.step : Math.max(d.step, impliedFromStep(edits))
    m.setDraft({ edits, step })
  }

  private turnRow(
    r: RunRow,
    selected: string,
    tab: string,
    turns: readonly RunRow[]
  ): HTMLElement {
    const chip = this.st(r)
    const row = el(
      "button",
      `weft-turn${r.id === selected ? " weft-sel" : ""}`,
      "",
      {
        type: "button",
        tabindex: r.id === tab ? "0" : "-1",
        "data-weft-run": r.id,
        ...(r.id === selected ? { "aria-current": "true" } : {}),
      }
    )
    // Titled, not id'd (U6): "t3 How many units…", the id in the
    // title and on the row (data-weft-run); a resume "t4 resume of t3 ·
    // 1 decided call", turnTitle over the loaded list as Studio's
    // session page reads it.
    const t = turnTitle(prevTurn(r, turns), r)
    const when = relativeTime(r.last_seen || r.started)
    const row1 = el("div", "weft-row1", [
      this.resumeChip(chip),
      el("span", "weft-ttl", `${turnLabel(r)} ${clipTitle(t.text)}`.trim(), {
        title: `${r.id}\n${t.full}`,
        "data-weft-title": t.kind,
      }),
      // What the record says this turn is (D5): scripted, a fork, an
      // experiment of a turn the list names.
      ...turnChips(
        r,
        (id) => {
          const src = this.rowOf(id)
          return src?.turn ? `t${src.turn}` : shortId(id)
        },
        chip.key
      ),
      el("span", "weft-when", when),
    ])
    const usage = r.usage
    const row2 = el("div", "weft-row2", [
      el("span", "", r.model.name ? `${r.model.provider}/${r.model.name}` : ""),
      el("span", "", stepCount(r.steps)),
      // A derived run whose spans reported no usage: "—" with the API's
      // reason (Studio's UnknownTokens), never 0→0 as known.
      derivedUsageUnknown(r)
        ? el("span", "", "—", {
            title: r.cost_reason ?? "",
            "data-weft-usage": "unknown",
          })
        : el(
            "span",
            "",
            `${tokens(usage.input_tokens)}→${tokens(usage.output_tokens)}`
          ),
      el("span", "", duration(r.started, r.finished) || "…"),
    ])
    // Its cost beside its tokens (A5.3), only where Studio prices usage.
    // The open turn's summarizer calls, apart (U16, UX-B-13): the run
    // document serves them; never in the turn's own figure.
    if (this.model && hasCapability(this.model.state, "pricing"))
      row2.insertBefore(
        turnCost(
          r,
          summarizerView(
            this.last.turn?.id === r.id ? this.last.turn.doc?.summarizer : null
          )
        ),
        row2.lastChild
      )
    row.append(row1, row2)
    // Its accessible name in words (UX-F-32), not every field run
    // together: "turn 3, parked, <title>, 2 steps, 60 in 30 out, 3h ago".
    row.setAttribute(
      "aria-label",
      `${r.turn ? `turn ${r.turn}` : "run"}, ${chip.word}, ${clipTitle(t.text)}, ${stepCount(r.steps)}${derivedUsageUnknown(r) ? "" : `, ${tokens(usage.input_tokens)} in ${tokens(usage.output_tokens)} out`}, ${when}`
    )
    if (r.err) row.append(el("div", "weft-reason", r.err))
    on(row, "click", () => this.go(this.model?.select(r.id, true)))
    on(row, "focus", (_, n) => this.roveTo(n, r.id))
    return el("div", "", [row, ...this.sourceLink(r)], {
      role: "listitem",
      "data-key": r.id,
    })
  }

  /** experimentResult is §3's Result pane: the name (`Replay 1 of t3`), the
   * stream in place, the inline diff against the source turn, and —
   * when the run parked — the approval controls (approve / deny /
   * resolve, the runtime-started run's own verbs). */
  private experimentResult(s: PanelState): HTMLElement {
    const r = s.result
    if (!r) return el("div")
    const card = el("div", "weft-step weft-xres")
    const usage = r.row?.usage
    const stats = [
      usage
        ? `${tokens(usage.input_tokens)}→${tokens(usage.output_tokens)} tok`
        : "",
      r.row ? duration(r.row.started, r.row.finished) : "",
    ]
      .filter(Boolean)
      .join(" · ")
    const head = el("div", "weft-step-h", [
      el(
        "span",
        "",
        // The run's status word (lib/status.ts), never the ack's state.
        [
          r.label,
          r.engine ?? (r.row && engineWord(r.row)),
          outcomeWord(r.state, r.status),
        ]
          .filter(Boolean)
          .join(" · ")
      ),
      el("span", "", stats),
      el("span", "weft-grow"),
    ])
    // The replayed run's whole bill (the playground's wrapper title: it
    // folds own + delegated), and every replay of this turn the panel's
    // page lists — not Studio's saved experiment — summed by atLeastCost
    // so one unpriced (scripted) replay leaves the priced ones "≥" (A5.3).
    if (hasCapability(s, "pricing")) {
      if (r.row)
        head.insertBefore(
          el("span", "", [costText(sumRuns([r.row]))], {
            title: "the run's whole bill: its own model calls + its subagents'",
            "data-weft-result-cost": "",
          }),
          head.lastChild
        )
      const xs = s.experiments.get(r.sourceRunID) ?? []
      if (xs.length) {
        // Older pages may hold more of them: the header's loaded-only words.
        const more = loadedOnly(s, xs.length, "replay")
        const x = el(
          "span",
          "",
          `${xs.length} replay${xs.length > 1 ? "s" : ""} of this turn${more ? " (loaded only)" : ""}: `,
          { "data-weft-experiment-cost": "" }
        )
        x.append(costText(atLeastCost(xs, more)))
        head.insertBefore(x, head.lastChild)
      }
    }
    // P4's saves (§3's row): keep-as-prompt copies the text and, under
    // the dev token, proposes it (plan F6) — Studio's title; save as
    // test hands off to Studio's run page (the zip downloads there).
    const keep = el("button", "weft-btn", "keep as prompt ⤴", {
      title: keepEditedTitle(
        this.model?.state.drawer?.agent ?? "",
        s.meta?.capabilities ?? [],
        this.cfg.token
      ),
    })
    on(keep, "click", () => {
      const d = this.model?.state.drawer
      const text = d?.instructions ?? ""
      const agent = d?.agent ?? ""
      const caps = s.meta?.capabilities ?? []
      // The clipboard refuses without focus or permission: not the
      // host page's error.
      let copied: Promise<boolean>
      try {
        // undefined outside a secure context
        const clip = navigator.clipboard as Clipboard | undefined
        copied = clip
          ? clip.writeText(text).then(
              () => true,
              () => false
            )
          : Promise.resolve(false)
      } catch {
        copied = Promise.resolve(false)
      }
      // Plan F6: the dev token also proposes it (the Agents page shows
      // it beside the registered prompt); a panel token copies only.
      if (!caps.includes("proposals")) return
      const say = (o: Omit<KeepPrompt, "kind" | "agent" | "copied">) =>
        void copied.then((ok) =>
          this.say(
            keepPromptWords({ kind: "keep-prompt", agent, copied: ok, ...o })
              .title
          )
        )
      if (!agent || !mayProposeWith(caps, this.cfg.token))
        return say({ proposal: "copy-only" })
      putProposal(this.ep, agent, text).then(
        () => say({ proposal: "proposed" }),
        (e: unknown) =>
          say({ proposal: "refused", message: (e as Error).message })
      )
    })
    head.append(keep)
    // UX-C-29: one verb, one zip — the run page's save as test.
    if (r.runID) {
      const save = el("a", "weft-btn", SAVE_TEST_LABEL, {
        href: href(this.base, saveTestLink(r.runID)),
        ...NEW_TAB,
        title: SAVE_TEST_TITLE,
      })
      head.append(save)
    }
    const compare = el("a", "weft-btn", "compare in Studio", {
      title:
        "open the Studio playground with this run, step and the current overrides carried over",
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
      const step = mine
        ? mine.step
        : s.turn
          ? readStep(s.turn.folded, s.selectedStep)
          : -1
      a.setAttribute("href", studioPlaygroundLink(this.base, mine, step))
    }
    link(compare)
    compare.setAttribute("target", "_blank")
    compare.setAttribute("rel", "noopener")
    for (const ev of ["pointerdown", "focus", "click", "contextmenu"])
      on(compare, ev, (_, a) => link(a))
    head.append(compare)
    head.append(
      clickBtn("discard", () => this.model?.discardResult(), {
        title: "clear the result pane",
      })
    )
    card.append(head)
    const body = el("div", "weft-step-b")
    // A scripted miss: the server's sentence and "run again on live" —
    // the drawer back on live with its estimate, never a retry.
    // A scripted miss (the server's sentence) and its ways on, or a
    // wefttest script's exhaustion (UX-C-09).
    const miss = missOf(r)
    const gone = exhaustedOf(r)
    if (miss || gone) {
      const m = el("div", "weft-note weft-warn", miss || gone, {
        "data-weft-miss": "",
      })
      // "run again on live" acts on this result's source: its drawer
      // when open, else that drawer reopened (UX-C-07's step 0 too).
      const dr = s.drawer
      const mine = dr?.runId == r.sourceRunID && dr
      const usd = mine && engineFor(mine, s.preview?.doc).live.usd
      // Live only on this result's own drawer, whatever else opened.
      const live = () =>
        this.model?.state.drawer?.runId == r.sourceRunID &&
        this.model.setDraft({ engine: "live" })
      // From step 0 only while the recorded agent is registered.
      if (miss)
        for (const v of r.zero ? [1, 0] : [1])
          m.append(
            " ",
            clickBtn(v ? TARGET_WORDS.again : MISS_FROM_ZERO, () =>
              mine && v
                ? live()
                : void this.model
                    ?.openExperiment(r.sourceRunID, v && r.fromStep, {
                      under: r.under,
                    })
                    .then(() => v && live())
            ),
            v && usd ? ` ${usd}` : ""
          )
      body.append(m)
    } else if (r.error) body.append(el("div", "weft-note weft-warn", r.error))
    // The link back (plan F1): the new run, the source step it replays.
    if (r.runID) {
      const src = r.sourceRunID
      const line = el(
        "div",
        "weft-row2",
        [el("span", "", `${shortId(r.runID)} · replay of`), " "],
        {
          "data-weft-replay-of": `${src}#${r.fromStep}`,
          "data-key": "replay-of",
        }
      )
      const words = `${shortId(src)} from step ${r.fromStep}`
      if (src === r.under) {
        const b = el("button", "weft-btn", words, {
          type: "button",
          "aria-label": `go to the source: run ${src} step ${r.fromStep}`,
          "data-weft-source": `${src}#${r.fromStep}`,
        })
        on(b, "click", () => this.select(src, r.fromStep))
        line.append(b)
      } else line.append(el("span", "", words))
      const mk = (text: string, url: string, label: string, attr: string) => {
        const a = el("a", "weft-btn", text, {
          href: url,
          ...NEW_TAB,
          "aria-label": label,
          [attr]: "",
        })
        return a
      }
      line.append(
        mk(
          "⤢",
          href(this.base, runPage(src, { step: r.fromStep })),
          `open the source step in Studio (run ${src} step ${r.fromStep})`,
          "data-weft-source-link"
        ),
        mk(
          "open the replayed run ⤢",
          href(this.base, runPage(r.runID)),
          `open the replayed run ${r.runID} in Studio`,
          "data-weft-run-link"
        )
      )
      body.append(line)
    }
    const status = r.row?.status ?? (r.ready ? "succeeded" : "running")
    for (const step of r.folded.steps) {
      if (step.text) body.append(el("div", "", step.text))
      for (const call of step.toolCalls)
        body.append(
          renderCall(
            call,
            step.index,
            status,
            undefined,
            undefined,
            r.runID ? { endpoint: this.base, runId: r.runID } : undefined
          )
        )
    }
    if (!r.folded.steps.length && !r.error && r.state === "queued")
      body.append(
        el("div", "weft-note", "queued — waiting for the runtime to ack…")
      )
    else if (
      !r.folded.steps.length &&
      !r.error &&
      r.state === "accepted" &&
      !r.runID
    )
      // A fork's accepted row names no run: the finished one names the
      // session's new turn, and the pane moves to it then.
      body.append(
        el(
          "div",
          "weft-note",
          "accepted — the fork's turn is running in its new session…"
        )
      )

    // The 2-way compare (P3, PQ3 — the panel stays 2-way): the diff's
    // other side is the source turn by default, or any sibling of it.
    const siblings = [
      { id: "", label: sourceLabel(r.label) },
      ...(s.experiments.get(r.sourceRunID) ?? [])
        .filter((x) => x.id !== r.runID)
        .map((x) => ({ id: x.id, label: shortId(x.id) })),
    ]
    if (siblings.length > 1) {
      const cmp = el("select", "weft-input", "", {
        "aria-label": "compare with",
      }) as HTMLSelectElement
      for (const sb of siblings)
        option(cmp, `compare vs ${sb.label || "source"}`, { value: sb.id })
      cmp.value = r.compareWith
      on(cmp, "change", (_, n) =>
        this.go(this.model?.setCompare((n as HTMLSelectElement).value))
      )
      body.append(cmp)
    }

    // The inline diff (§3: `diff vs t3:`), once there is final text —
    // on text, and on the tool calls beside it. Both sides are whole
    // turns read the same way (turnWordsOf), and only an ended run is
    // diffed: a table per streamed delta is the host page's frame
    // budget, and half an answer is not a difference.
    const mine: TurnWords | null = r.ready
      ? (r.words ?? foldedWords(r.folded))
      : null
    const other: TurnWords | undefined = r.compareWith
      ? this.model?.compareWords.get(r.compareWith)
      : r.source
    const otherLabel = r.compareWith
      ? shortId(r.compareWith)
      : sourceLabel(r.label)
    if (mine && other && mine.text && other.text) {
      const diffBox = el("div", "weft-diff")
      this.diffInto(diffBox, `diff vs ${otherLabel}:`, other.text, mine.text)
      // The tool-call diff beside the text: name(args) lines.
      if (other.calls.join("\n") !== mine.calls.join("\n"))
        this.diffInto(
          diffBox,
          "tool calls:",
          other.calls.join("\n"),
          mine.calls.join("\n")
        )
      body.append(diffBox)
    }
    // The step compare (plan E3): the run against its source, rows by
    // step ordinal — GET /api/diff through lib/stepdiff.ts, the rows
    // Studio's compare draws; capability "diff" only.
    if (r.ready && hasCapability(s, "diff")) {
      if (r.stepDiff) body.append(stepDiffBlock(r.stepDiff, this.base))
      else if (r.stepDiffError)
        body.append(
          el(
            "div",
            "weft-note weft-warn",
            `the step compare could not be read: ${r.stepDiffError}`
          )
        )
      else if (r.runID && r.sourceRunID)
        body.append(
          el("div", "weft-note", "reading the step compare…", {
            "data-weft-step-diff-loading": "",
          })
        )
    }

    // The parked calls' controls: the runtime-started run's approval
    // verbs (§8.2), through POST /api/runs/{id}/approvals. Offered
    // only once the pane holds this run's own stored pending set
    // (ready) — a call still streaming, or left over from an earlier
    // leg of a chain, is not this run's to decide.
    if (r.ready && r.folded.pending.length && r.runID && this.canAct(s))
      body.append(this.decisions(r.folded.pending, r.decided))
    // Rung 4 (§8.4): steer the running experiment — one user message
    // delivered mid-flight into the run the runtime holds (an ephemeral
    // run, or a fork's turn once the runtime's ack names it).
    if (
      hasCapability(s, "steer") &&
      this.canAct(s) &&
      r.state === "accepted" &&
      r.runID
    )
      body.append(
        this.steerBox("steer this run", "steer", (m) => this.model?.steer(m))
      )
    card.append(body)
    return card
  }

  /** expiry is a parked call's request expiry (UX-B-41) in Studio's
   * words and hover (lib/decisions.ts expiryText, expiryTitle); while it
   * has not lapsed the panel redraws when the words next change or it
   * lapses (expiryNext, the soonest of the drawn calls'). Each draw
   * clears the clock first: a frame that draws no open expiry (the
   * lapse, another turn, the decision) leaves none pending. */
  private expiry(t: TurnView | undefined, id: string): HTMLElement[] {
    const at = t?.exp?.at.get(id)
    if (!at) return []
    const now = Date.now()
    const w = expiryText(at, now)
    const d = expiryNext(at, now)
    if (d && !(this.tick && this.due <= now + d)) {
      clearTimeout(this.tick)
      this.due = now + d
      this.tick = setTimeout(() => {
        this.tick = 0
        this.render(this.last)
      }, d) as unknown as number
    }
    return [
      el("span", w.lapsed ? "weft-warn" : "weft-reason", ` · ${w.text}`, {
        title: expiryTitle(at),
        "data-request-expiry": w.lapsed ? "lapsed" : "open",
      }),
    ]
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
    wrap.append(this.notes(t))
    // A derived turn (plan I1.3): no events to fold here — its steps
    // (the chat spans' usage, the tool spans) are Studio's story.
    if (this.derived(t)) {
      const n = this.rowOf(t.id)?.steps ?? t.doc?.steps ?? 0
      const a = el("a", "", `${stepCount(n)} · open in Studio ⤢`, {
        href: studioLink(this.base, t.id, 0),
        ...NEW_TAB,
        "data-weft-derived-handoff": t.id,
      })
      wrap.append(el("div", "weft-note", [a]))
    }
    const ids = this.turnLinks(t, s)
    if (ids) wrap.append(ids)
    const row0 = this.rowOf(t.id)
    const loop = loopLine(
      t,
      row0?.stopped_by ?? t.doc?.stopped_by,
      row0?.status ?? t.doc?.status,
      s.selectedStep,
      this.base,
      (n) => this.model?.selectStep(n)
    )
    if (loop) wrap.append(loop)
    const prompt = turnPromptOf(t.transcript)
    const ed = this.edCtx(s, t)
    // Step 0's prompt is a user edit's (plan F2): editable in place.
    const first = ed?.users.find((u) => u.step === 0 && u.index === 0)
    if (prompt) {
      const note = el("div", "weft-note", prompt, { "data-key": "prompt" })
      wrap.append(
        ed && first
          ? ed.box(
              { kind: "user", step: 0, index: 0 },
              first.text,
              note,
              "the turn's prompt"
            )
          : note
      )
    }
    // The prompt's files (UX-S1-13), the own / delegated tokens
    // (UX-S3-13), the typed output (UX-S1-06): lib/cards.ts's words.
    const files = attachmentsOf(t.transcript?.batches)
    if (files.length)
      wrap.append(
        el("div", "weft-reason", files.join(" · "), { "data-weft-files": "" })
      )
    const split = t.doc && tokenSplit(t.doc)
    if (split)
      wrap.append(el("div", "weft-reason", split, { "data-weft-split": "" }))
    // none only when nothing was submitted; a submission whose args
    // the record does not carry is typed + the stripped badge.
    const out = outputOf(t.folded, t.spans, t.id)
    if (out) {
      const o = el(
        "div",
        "weft-reason",
        out.none
          ? OUTPUT.none
          : out.repairs
            ? `${OUTPUT.typed} · ${repairedWords(out.repairs)}`
            : OUTPUT.typed,
        { "data-weft-output": "" }
      )
      if (out.stripped) o.append(badge("stripped"))
      wrap.append(o)
    }
    // A replayed run whose prefix was edited says which pairs (weft.edits).
    const mark = marksBlock(this.rowOf(t.id) ?? t.doc, t.transcript?.batches)
    if (mark) wrap.append(mark)
    // A resume's decided calls (U13.2): the card before step 0, never
    // inside it — Studio's ResumedCard.
    const res = t.doc?.resumes
    if (res) {
      const card = el("div", "weft-step", "", {
        "data-weft-resumed-card": res.run,
      })
      card.append(
        el("div", "weft-step-h", [
          this.runBtn(resumedHead(res, turnTag(res.run)), res.run),
        ])
      )
      const b = el("div", "weft-step-b")
      const pre = t.folded.steps[0]?.toolCalls
      for (const c of res.calls) {
        const row = el("div", "weft-call", [
          el("span", "weft-name", c.name || c.call_id),
          this.decision(c, res),
        ])
        // What an approved / resolved call left: step 0 no longer draws it.
        const ran = decidedResult(c, pre)
        if (ran)
          row.append(
            el("div", "weft-reason", `→ ${ran}`, { "data-weft-result": "" })
          )
        b.append(row)
      }
      const note = this.decisionsNote(res, turnTag(t.id))
      if (note) b.append(note)
      card.append(b)
      wrap.append(card)
    }
    const row = this.rowOf(t.id)
    // A turn that started on a context thread compacted after the turn
    // before it (U16, UX-B-12): said where it opens.
    if (row && prevTurn(row, s.turns)?.session_compactions)
      wrap.append(
        el("div", "weft-note", STARTED_COMPACTED, {
          "data-weft-compaction": "started",
        })
      )
    wrap.append(
      renderFolded(
        withoutDecided(t.folded, t.doc),
        row?.status ?? t.doc?.status ?? "running",
        t,
        s.selectedStep,
        { keys: this.openKeys, scope: t.id },
        {
          endpoint: this.base,
          runId: t.id,
          // The logs route refuses every panel token: the hand-off is
          // drawn for no token or the dev token only.
          logs: hasCapability(s, "logs") && tokenScope(this.cfg.token) === "",
          run: row ?? t.doc ?? undefined,
          ...(ed ? { edit: ed } : {}),
          ...(this.canAct(s) && !this.derived(t)
            ? {
                replay: {
                  runId: t.id,
                  agent: row?.agent,
                  max: boundsOf(t.transcript)?.max ?? null,
                  // A session's top-level turn only (a child adopted by
                  // select() is no conversation to fork).
                  fork:
                    !!(row?.session_id || t.doc?.session_id) &&
                    !(childRun(row) || childRun(t.doc)),
                  open: (runId, draft, agent, from) => {
                    this.opener = from
                    this.go(
                      this.model?.openExperiment(runId, draft, {
                        agent,
                        under: t.id,
                      })
                    )
                  },
                },
              }
            : {}),
        }
      )
    )
    // thread's session markers (A9.2) at the end of the turn they are
    // filed under — the session was compacted after it (U16); a run from
    // before A9 names none and draws nothing.
    const comps = compactionsOf(t.doc)
    for (const c of comps.filter(isSessionMarker))
      wrap.append(
        compactionBox(c, comps, t.transcript, {
          keys: this.openKeys,
          scope: t.id,
        })
      )
    const at = row ?? t.doc
    const kind = parkedKind(at, s.runtimes, nextTurn(at, s.turns))
    const running = (row?.status ?? t.doc?.status) === "running"
    // The gate reads the runtimes (a Sessions runtime of the run's
    // service): asked here, at most every 10 s, for a parked or running
    // app turn only.
    if (
      (kind === "decide" || kind === "read-only") &&
      (running || t.folded.pending.length) &&
      this.canAct(s)
    )
      this.model?.wantRuntimes()
    if (t.folded.pending.length && at)
      wrap.append(this.approvals(at, t.folded.pending, kind, s))
    // Steer the app's turn running now (plan F4.3): the run id is this
    // turn's; the answer is a command, its refusal said verbatim.
    if (
      kind === "decide" &&
      this.canAct(s) &&
      hasCapability(s, "steer") &&
      running
    )
      wrap.append(
        this.steerBox(
          "steer this turn",
          "app-steer",
          (m) => this.model?.steerApp(t.id, m),
          this.appSaid(s.app[`${t.id}:steer`], t.id)
        )
      )
    if (this.canAct(s))
      wrap.append(
        this.derived(t)
          ? el("div", "weft-actions", [
              refusedVerb("✎ Experiment"),
              refusedVerb("↻ Re-run"),
            ])
          : this.actions(s)
      )
    return wrap
  }

  /** approvals draws the open turn's parked calls (§2). An
   * experiment's are decided from the result pane that ran it (§8.2).
   * The app's own turn (plan F4.3) takes the same verbs, on the same
   * route, while a connected runtime of its service registered
   * runtime.Sessions and it is its session's latest turn; each verb's
   * command is polled and its refusal said verbatim. Otherwise the
   * verbs are drawn refused with the server's words and fix (a 403
   * already answered, else lib/honesty.ts's — appturn.go's and
   * auth.go's), a turn a later one resumed says so, and a plain
   * Generate's park says the app resumes it. */
  private approvals(
    run: RunRow,
    pending: ToolCallPart[],
    kind: ParkedKind,
    s: PanelState
  ): HTMLElement {
    const runID = run.id
    // A refusal a later runtimes read re-gated to decide is history:
    // the verbs come back (the runtime reconnected, the fix applied).
    const sent = (id: string): AppVerb | undefined => {
      const v = s.app[`${runID}:${id}`] as AppVerb | undefined
      return kind === "decide" && v?.state === "refused" && (v.rt ?? 0) < s.rtN
        ? undefined
        : v
    }
    const refused = pending
      .map((c) => sent(c.id))
      .find((v) => v?.state === "refused" && v.fix)
    const act = this.canAct(s)
    // A resume the record links decided the park for good (U13.2): each
    // call's outcome, whatever this surface may do.
    const t = s.turn?.id === runID ? s.turn : undefined
    const dec = t?.doc?.decided
    if (kind === "decide" && act && !refused && !dec) {
      const decided: Record<string, string> = {}
      for (const c of pending)
        if (sent(c.id)?.state === "finished")
          decided[c.id] = sent(c.id)!.decision ?? ""
      return this.decisions(
        pending,
        decided,
        (id, d, text) => this.go(this.model?.decideApp(runID, id, d, text)),
        (id) => this.appSaid(sent(id), runID),
        t
      )
    }
    const next =
      dec?.run ?? (kind === "resumed" ? nextTurn(run, s.turns) : undefined)
    const box = el("div", "weft-step")
    box.append(
      el("div", "weft-step-h", [
        el("span", "", next ? "decided" : `${AWAITING} (read-only)`),
        ...(next ? [] : this.expHole(t)),
      ])
    )
    const body = el("div", "weft-step-b")
    const read = tokenScopeOf(this.cfg.token) === "read"
    const line = refusalLine(
      refusalWords(
        kind,
        run,
        s.runtimes,
        read,
        refused,
        act && !read ? s.rtErr : "",
        s.meta
          ? {
              capabilities: s.meta.capabilities,
              capabilities_off: s.meta.capabilities_off,
            }
          : undefined
      )
    )
    const waiting = kind === "read-only" && act && !s.rtRead && !refused
    // A failed runtimes read: its error, no verdict, no verbs.
    const unread = kind === "read-only" && act && !read && !!s.rtErr && !refused
    for (const call of pending) {
      const row = el("div", "weft-call", "", { "data-key": call.id })
      row.append(
        parkedHead(
          call,
          decidedCall(dec, call.id)
            ? []
            : [
                el("span", "weft-badge weft-info", next ? "decided" : "parked"),
                ...(next ? [] : this.expiry(t, call.id)),
              ]
        )
      )
      const c = decidedCall(dec, call.id)
      if (c && dec) row.append(this.decision(c, dec, dec.run))
      else if (kind === "experiment")
        row.append(
          el(
            "div",
            "weft-res",
            "an experiment's run — its decision controls are in the result pane of the turn that ran it"
          )
        )
      else if (!next && !waiting && !unread)
        row.append(
          el(
            "div",
            "weft-res",
            Object.values(VERB).map((v) =>
              refusedVerb(v, "button", line, "app-owned")
            )
          )
        )
      body.append(row)
    }
    if (dec) {
      const note = this.decisionsNote(dec, turnTag(dec.run))
      if (note) body.append(note)
    } else if (next) {
      const go = this.runBtn(`resumed as ${next}`, next, {
        "data-weft-resumed": next,
      })
      body.append(
        el("div", "weft-note", ["decided — ", go], {
          "data-weft-app-owned": "resumed",
        })
      )
    } else if (waiting)
      body.append(
        el(
          "div",
          "weft-note",
          "checking for a runtime that acts on the app's sessions…"
        )
      )
    else if (kind !== "experiment")
      body.append(
        el("div", "weft-note weft-warn", line, {
          "data-weft-app-owned": unread
            ? "unread"
            : kind === "decide"
              ? "read-only"
              : kind,
        })
      )
    box.append(body)
    return box
  }

  /** decisionsNote is a decisions block's honesty line and its
   * not_recorded hole — Studio's DecisionsNote — or null. */
  private decisionsNote(d: DecisionDoc, resume: string): HTMLElement | null {
    const note = decisionsNote(d, resume)
    if (!note && !d.hole) return null
    const n = el("div", "weft-note weft-warn", note)
    if (d.hole) n.prepend(badge(d.hole, CALLS_HOLE), " ")
    return n
  }

  /** appSaid is one app-turn verb's state under its call: in flight,
   * decided (and the resumed turn, a link that opens it), or the
   * runtime's rejection / the route's refusal word for word. */
  private appSaid(v: AppVerb | undefined, runID: string): HTMLElement | null {
    if (!v) return null
    const w = appVerbWords(v, runID)
    const box = el("div", w.bad ? "weft-note weft-warn" : "weft-note", w.text, {
      "data-weft-app-verb": v.state,
    })
    if (w.resumed)
      box.append(
        " · ",
        this.runBtn(`resumed as ${w.resumed}`, w.resumed, {
          "data-weft-resumed": w.resumed,
        })
      )
    return box
  }

  /** decision is one decided call's outcome (U13.2, lib/decisions.ts):
   * "✋ denied · agent-7 · 'need a photo' → t2" in its tone, a hidden or
   * uncaptured reason its badge, the turn a button that opens it, how
   * and when under it — Studio's DecisionRow. */
  private decision(c: DecidedCall, d: DecisionDoc, to?: string): HTMLElement {
    const box = el("div", "weft-dec", decisionWords(c, d), {
      "data-tone": outcomeOf(c.outcome).tone,
      "data-weft-decision": c.outcome,
    })
    // A denial's reason or a resolve's result unreadable: one badge.
    const hole = c.reason_badge || c.result_badge
    if (hole) box.append(" ", badge(hole, REASON_HOLE[hole]))
    if (c.who_hidden) box.append(" ", badge("hidden", WHO_HOLE))
    if (to)
      box.append(
        " → ",
        this.runBtn(turnTag(to), to, { "data-weft-resumed": to })
      )
    box.append(el("div", "weft-reason", decisionDetail(c, d)))
    return box
  }

  /** steerBox is rung 4's box (§8.4): one message, delivered by send
   * — the experiment's run, or the app turn running now (plan F4.3,
   * its command's line under it). */
  private steerBox(
    title: string,
    key: string,
    send: (m: string) => Promise<unknown> | undefined,
    said?: HTMLElement | null
  ): HTMLElement {
    const box = el("div", "weft-step", "", { [`data-weft-${key}`]: "" })
    box.append(el("div", "weft-step-h", [el("span", "", title)]))
    const b = el("div", "weft-step-b")
    const input = this.field(el("input", "weft-input") as HTMLInputElement, key)
    input.placeholder = "a message delivered mid-flight"
    input.value = this.scratch.get(key) ?? ""
    on(input, "input", (_, n) =>
      this.scratch.set(key, (n as HTMLInputElement).value)
    )
    const btn = el("button", "weft-btn", "steer", {
      title: "POST /api/runs/{id}/steer (ADR 0019)",
    })
    on(btn, "click", (_, n) => {
      const field = n.parentElement?.querySelector<HTMLInputElement>("input")
      if (!field?.value) return
      this.go(send(field.value))
      this.scratch.delete(key)
      field.value = ""
    })
    b.append(input, btn)
    if (said) b.append(said)
    box.append(b)
    return box
  }

  /** decisions draws the parked calls of the result's run with the
   * approval verbs. resolve pastes the result typed beside it — never
   * an empty one. Each call is decided on its own (POST
   * /api/runs/{id}/approvals carries one), and the runtime holds the
   * decisions until every parked call has one, then resumes the run
   * once: the pane shows which are in and how many it still waits
   * for. A held decision can be changed until the last one lands. */
  private decisions(
    pending: ToolCallPart[],
    decided: Record<string, string>,
    act = (id: string, d: "approve" | "deny" | "resolve", text: string) =>
      this.go(this.model?.decide(id, d, d === "resolve" ? text : undefined)),
    status?: (id: string) => HTMLElement | null,
    t?: TurnView
  ): HTMLElement {
    const approvals = el("div", "weft-step")
    approvals.append(
      el("div", "weft-step-h", [el("span", "", AWAITING), ...this.expHole(t)])
    )
    const abody = el("div", "weft-step-b")
    const left = pending.filter((c) => !decided[c.id]).length
    if (left < pending.length) {
      abody.append(
        el(
          "div",
          "weft-note",
          `waiting for ${plural(left, "more decision")} — the run resumes once every parked call is decided`
        )
      )
    }
    for (const call of pending) {
      const line = el("div", "weft-call", "", { "data-key": call.id })
      line.append(
        parkedHead(
          call,
          decided[call.id]
            ? [
                el(
                  "span",
                  "weft-badge weft-info",
                  DONE[decided[call.id]] ?? decided[call.id]
                ),
              ]
            : this.expiry(t, call.id)
        )
      )
      const said = status?.(call.id)
      if (said) line.append(said)
      const ctl = el("div", "weft-res")
      const key = `resolve:${call.id}`
      const paste = this.field(
        el("input", "weft-input weft-resolve") as HTMLInputElement,
        key
      )
      paste.placeholder = DECIDE_FIELD
      paste.value = this.scratch.get(key) ?? ""
      on(paste, "input", (_, n) =>
        this.scratch.set(key, (n as HTMLInputElement).value)
      )
      const mk = (
        label: string,
        title: string,
        fn: (b: HTMLElement) => void
      ) => {
        const b = el("button", "weft-btn", label, { title })
        on(b, "click", (_, n) => fn(n))
        return b
      }
      const typed = (b: HTMLElement) =>
        b.parentElement?.querySelector<HTMLInputElement>(".weft-resolve")
      ctl.append(
        mk(VERB.approve, VERB_TITLE.approve, () => act(call.id, "approve", "")),
        mk(VERB.deny, VERB_TITLE.deny, (b) =>
          act(call.id, "deny", typed(b)?.value ?? "")
        ),
        paste,
        mk(VERB.resolve, VERB_TITLE.resolve, (b) => {
          const field = typed(b)
          if (!field?.value) {
            field?.focus()
            return
          }
          act(call.id, "resolve", field.value)
        })
      )
      line.append(ctl)
      abody.append(line)
    }
    approvals.append(abody)
    return approvals
  }

  /** shortcuts is §5.2's ? overlay: the keys, and the collision Q4
   * records (Alt+W is the primary because Chrome eats Ctrl+Shift+W
   * as close-window before any page can see it). */
  private shortcuts(): HTMLElement {
    const box = el("div", "weft-keys")
    const dl = el("dl")
    for (const [k, v] of SHORTCUTS) {
      dl.append(el("dt", "", k), el("dd", "", v))
    }
    box.append(dl)
    return box
  }

  /** readManifest is the registered config the Request tab's first
   * step diffs against (E1.2): read when a chip needs it, only where it
   * can be read (a read-scoped token never asks), kept per (endpoint,
   * token); asked again after a failure (MANIFEST_RETRY_MS on), and
   * once per run manifest hash it does not list — a redeploy under
   * weft dev registers a new one. */
  private readManifest(
    redraw: () => void,
    hash?: string
  ): Manifest | null | undefined {
    if (tokenScope(this.cfg.token) === "read") return null
    const key = `${this.base}\u0000${this.cfg.token}`
    if (this.mf?.key !== key)
      this.mf = { key, doc: undefined, at: 0, busy: false, asked: new Set() }
    const m = this.mf
    const unlisted =
      !!m.doc &&
      !!hash &&
      !m.doc.agents.some((a) => a.manifest_hash === hash) &&
      !m.asked.has(hash)
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
      panelGet<Manifest>(
        { base: this.base, token: this.cfg.token },
        "manifest"
      ).then(
        (doc) =>
          done(
            Array.isArray((doc as Partial<Manifest> | null)?.agents)
              ? doc
              : null
          ),
        () => done(null)
      )
    }
    return m.doc
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
          ...(r && !r.error
            ? { requests: { ...r, steps: Object.fromEntries(r.steps) } }
            : {}),
        },
      }
    return treeView(this.rawMemo!.doc, this.tree, t.id.replace(/[\\/]/g, "_"), {
      redraw: () => {
        if (this.isConnected) this.render(this.last)
      },
      root: () => this.shadow,
    })
  }

  /** expHole is the badge for expiries the panel could not read: past
   * the run's marks read's cap (truncated), or a failed read
   * (not_recorded). */
  private expHole(t?: TurnView): HTMLElement[] {
    const h = t?.exp?.hole
    return h
      ? [
          badge(
            h,
            h === "truncated"
              ? { cause: "response_cap", ...RUN_MARKS_HOLE }
              : EXPIRY_UNREAD
          ),
        ]
      : []
  }

  private resolveNow() {
    return resolveTheme(this.cfg.theme, this.lay.theme, this.themeWatch.host)
  }
}

/** The replay drawer's title per verb (Studio's VERB_TITLES). */
const VERB_TITLES: Record<ReplayDraft["verb"], string> = {
  from_step: "Replay from this step",
  edit_result: "Edit this result and replay",
  edit_prompt: "Edit the prompt and replay",
  rerun: "Re-run",
  continue: "Continue here with a new message",
  edit: "Edit the transcript and replay",
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
      settling: row?.settling ?? t.doc?.settling,
    })
  )
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
  if (!req)
    return served
      ? { tools: null, loading: true }
      : {
          tools: null,
          hole: catalogReadError("the server has no request route"),
        }
  if (req.badge)
    return {
      tools: null,
      hole: { hole: req.badge, reason: req.reason, fix: req.fix },
    }
  if (req.error) return { tools: null, hole: catalogReadError(req.error) }
  const rows = req.steps.get(ordinal)?.rows
  const row = rows?.[rows.length - 1]
  if (!row) {
    if (req.truncated && ordinal > lastStep(req))
      return {
        tools: null,
        hole: catalogCapped(MAX_REQUEST_PAGES * REQUEST_PAGE),
      }
    if (count !== null && ordinal >= count)
      return { tools: null, hole: catalogNotRecorded() }
    if (running) return { tools: null, hole: catalogNotStored() }
    return {
      tools: null,
      hole: { hole: "gap", reason: REQUEST_NO_RECORD_REASON },
    }
  }
  const c = row.tools
  if (!c)
    return row.catalog_hash
      ? { tools: null, hole: { hole: "gap" } }
      : { tools: [], none: true }
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
  open: (
    runId: string,
    draft: ReplayDraft,
    agent: string | undefined,
    from: HTMLElement
  ) => void
}

/** boundsOf is the server's reading of a run's transcript (its step
 * count and highest from_step), null without one. */
export function boundsOf(
  tr: Transcript | null | undefined
): { stepCount: number; max: number } | null {
  return tr ? replayBounds(tr.batches) : null
}

/** verb is one hover/focus affordance (the run page's Verb): a button
 * named by its aria-label, its glyph drawn by CSS, keyed so a live
 * draw keeps it (and its focus); the click never reaches the card. */
function verb(
  label: string,
  glyph: string,
  draft: ReplayDraft,
  r: ReplayCtx
): HTMLElement {
  const b = el("button", "weft-verb", "", {
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

/** childBlock is the lazy subagent expander (§2, Dv3): the call's
 * child run folds on demand and nests under the call. The element
 * delegates the toggle through [data-weft-child]. Any depth expands
 * the same way (UX-D-13); past two levels the indent stops and the
 * summary names the depth. */
function childBlock(
  call: FoldedToolCall,
  t: TurnView,
  open?: OpenState,
  endpoint?: string,
  replay?: ReplayCtx,
  up = 0
): HTMLElement {
  const childId = call.childRunId!
  const depth = up + 1
  const child = t.children.get(childId)
  const row = childRow(t, childId)
  const details = el("details", "weft-collapsible")
  details.setAttribute("data-weft-child", childId)
  if (depth > 2) details.setAttribute("data-depth", String(depth))
  if (t.expanded.has(childId)) details.setAttribute("open", "")
  // The nested row (A10): agent, status, usage — the parent's row of
  // the child, before it is opened; its usage only once the record
  // has it, its holes (interrupted, stripped…) as badges. Its status
  // in statusOf's word (W7-prep; Studio's subagent block): a parked
  // child parked, a cancelled one cancelled — never the stored word.
  const summary = el(
    "summary",
    "",
    row
      ? `${CHILD_EYEBROW[childKind(row)]} ${row.agent || shortId(childId)} · ${statusOf(row).word} · ${
          usageKnown(row.status)
            ? usageLine(row.usage)
            : row.status === "running"
              ? USAGE_AT_FINISH
              : "—"
        }`
      : `subagent ${shortId(childId)}`
  )
  summary.setAttribute("aria-expanded", String(t.expanded.has(childId)))
  if (depth > 2)
    summary.append(el("span", "weft-badge weft-info", depthWord(depth)))
  const holes = row && holeBadges(child?.doc?.holes ?? rowHoles(row))
  if (holes) summary.append(holes)
  details.append(summary)
  if (endpoint) details.append(handOff(endpoint, childId))
  if (!child) {
    details.append(el("div", "", "loading the subagent's turn…"))
  } else {
    // The child's own status (the doc lists it): a child still running
    // shows its open calls as running, not as never completed.
    const status = row?.status ?? "succeeded"
    details.append(
      renderFolded(
        child.folded,
        status,
        undefined,
        undefined,
        open && { keys: open.keys, scope: childId },
        {
          endpoint,
          child,
          runId: childId,
          turn: t,
          depth,
          said: call.result?.content,
          // The child's steps replay the child (A10); never a fork.
          ...(replay
            ? {
                replay: {
                  ...replay,
                  runId: childId,
                  agent: row?.agent ?? "",
                  max: boundsOf(child.transcript)?.max ?? null,
                  fork: false,
                },
              }
            : {}),
        }
      )
    )
    if (child.capped)
      details.append(
        el(
          "div",
          "weft-note weft-warn",
          "a long run: its first events are shown"
        )
      )
  }
  return details
}

const verbs = (kind: string, list: HTMLElement[]) =>
  el("span", "weft-verbs", list, {
    "data-weft-verbs": kind,
    "data-key": `verbs:${kind}`,
  })

/** A tool call's verbs: edit its result and replay (the next step run
 * fresh against the edit), and replay from its step. */
function callVerbs(n: number, call: FoldedToolCall, r: ReplayCtx): HTMLElement {
  const list: HTMLElement[] = []
  // from_step n+1 must be one the server accepts: below the step count,
  // or at it when the transcript's kept prefix ends in answered calls.
  if (call.result && call.callId && r.max !== null && n + 1 <= r.max) {
    list.push(
      verb(
        `edit this result and replay (call ${call.callId})`,
        "✎",
        editResultAndReplay(n, call.callId),
        r
      )
    )
  }
  list.push(
    verb(
      `replay from this step (call ${call.callId}, step ${n})`,
      "↦",
      replayFromStep(n),
      r
    )
  )
  return verbs("call", list)
}

/** A step card's verbs: replay from it, edit the prompt and replay,
 * re-run the turn, continue with a new message. */
function stepVerbs(n: number, r: ReplayCtx): HTMLElement {
  const list = [
    verb(`replay from this step (step ${n})`, "↦", replayFromStep(n), r),
    verb(
      `edit the prompt and replay (step ${n})`,
      "✎",
      editPromptAndReplay(n),
      r
    ),
    verb("re-run the whole turn", "↻", rerun(), r),
  ]
  if (r.fork)
    list.push(verb("continue here with a new message", "⎇", continueHere(), r))
  return verbs("step", list)
}

/** The Request tab asks for a manifest it could not read again after
 * this long (a 404 before a runtime registered, a restart). */
export const MANIFEST_RETRY_MS = 30_000

/** linkedStep is the step ⤢ carries into Studio (G1): the one the
 * user is reading, else — while the turn runs — the running one (its
 * last step). Either is the step's ordinal (its index as the loop
 * counts it, lib/links.ts), never an event's position. */
export function linkedStep(s: PanelState): number | undefined {
  if (s.selectedStep != null) return s.selectedStep
  const t = s.turn
  if (!t || t.id !== s.selected) return undefined
  const row = turnsAndExperiments(s).find((r) => r.id === t.id)
  const running = (row?.status ?? t.doc?.status) === "running"
  return running ? t.folded.steps.at(-1)?.index : undefined
}

/** readStep is the step being read as source.from_step and the
 * playground hand-off count it: its own ordinal (the stored step index
 * the server and the runtime cut at) — never a position — or -1 when
 * none is read or the run has no such step. */
export function readStep(view: FoldedRun, selected: number | null): number {
  return selected != null && view.steps.some((st) => st.index === selected)
    ? selected
    : -1
}

/** A turn's run tree (lib/waterfall.ts): the Timeline's rows, and the
 * loop line's. */
const turnRows = (t: TurnView) =>
  waterfallRows({
    runId: t.id,
    status: t.doc?.status,
    spans: t.spans,
    fold: t.folded,
    total: (t.events.at(-1)?.pos ?? 0) + 1,
    agents: new Map((t.doc?.children ?? []).map((c) => [c.id, c.agent])),
  })

/** The loop in one line (U34, WEFT-LOOP-GRAPH.md §4): lib/loopgraph.ts's
 * model over the Timeline's rows, Studio's counts — each tool with its
 * marks (a click selects its first call's step, as a step card's does),
 * the end in the U3 words, and "graph ⤢" to Studio's loop at the
 * selection. */
function loopLine(
  t: TurnView,
  by: string | undefined,
  status: string | undefined,
  sel: number | null | undefined,
  base: string,
  pick: (step: number) => void
): HTMLElement | null {
  const g = loopCore(turnRows(t).rows, t.id, {
    stoppedBy: by,
    status,
    pending: t.folded.pending,
  })
  if (g.last === undefined) return null
  const box = el("div", "weft-loop", `${LOOP_LABEL} ◆ ⇄`)
  for (const c of loopChips(g)) {
    const b = el("button", "weft-btn", c.text, {
      type: "button",
      "data-tone": c.tone,
    })
    if (c.step !== undefined) on(b, "click", () => pick(c.step!))
    box.append(b)
  }
  box.append(
    el("a", "weft-btn", GRAPH_HANDOFF, {
      href: href(
        base,
        runPage(t.id, { scale: "graph", step: sel ?? undefined })
      ),
      ...NEW_TAB,
      "data-weft-handoff": t.id,
    })
  )
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
  /** The transcript editor (plan F2) on the turn's own steps; absent:
   * nothing is editable (a child, a running turn, no playground). */
  edit?: EdCtx
  /** Capability logs (U11.2): each step hands its app log lines off
   * to Studio's drawer Logs tab (📄 ⤢). */
  logs?: boolean
  /** The turn's run facts (its row, else its doc): the selected step —
   * else the step it failed at — opens with lib/summarize.ts's answer,
   * Studio's drawer at panel width (U10.2). */
  run?: AnswerRun
  /** Inside an expander: the turn the children load into, how deep
   * this child sits, and its call's result — the child's final words
   * equal to it are not drawn twice (UX-D-13). */
  turn?: TurnView
  depth?: number
  said?: string
}

/** The editor as the Story's renderers call it: box turns a shown node
 * into an editable one (or, open or edited, into its editor); users
 * are the user messages a user edit counts (lib/edits.ts). */
export interface EdCtx {
  box: (
    target: ReplayEdit,
    recorded: string,
    shown: HTMLElement,
    label: string,
    tool?: string
  ) => HTMLElement
  users: UserMessage[]
  /** The highest from_step the server accepts (null: unknown). */
  max: number | null
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
    wrap.append(
      el("div", "weft-reason", `${view.model.provider}/${view.model.name}`)
    )
  }
  // The running step is the one that streams: its text is the one
  // live region (D3), and only its card changes while it does.
  const last =
    runStatus === "running" && !ctx?.child ? view.steps.at(-1) : undefined
  // The answer's step (§6.3), Studio's landing rule: the step read,
  // else the one the run failed at (landStep), else the first.
  const ns = view.steps.map((s) => s.index)
  const ans =
    selectedStep ?? landStep(ctx?.run && failedAtStep(ctx.run), ns) ?? ns[0]
  for (const step of view.steps) {
    // The boundary before the step's model call takes an inserted
    // message (plan F2: a steer at rest).
    if (ctx?.edit && !ctx.child)
      wrap.append(
        ctx.edit.box(
          { kind: "insert", step: step.index },
          "",
          el(
            "div",
            "weft-reason weft-ins",
            `· insert a message before step ${step.index}`,
            { "data-key": `ins${step.index}` }
          ),
          markLine({ what: "insert", step: step.index })
        )
      )
    wrap.append(
      renderStep(
        step,
        runStatus,
        t,
        selectedStep,
        open,
        ctx,
        step === last,
        ans
      )
    )
  }
  // The boundary after the last step (its calls all answered).
  const max = ctx?.edit?.max
  if (
    ctx?.edit &&
    !ctx.child &&
    max != null &&
    view.steps.length &&
    max > Math.max(...view.steps.map((x) => x.index))
  )
    wrap.append(
      ctx.edit.box(
        { kind: "insert", step: max },
        "",
        el(
          "div",
          "weft-reason weft-ins",
          "· insert a message after the last step",
          {
            "data-key": `ins${max}`,
          }
        ),
        markLine({ what: "insert", step: max })
      )
    )
  return wrap
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
  box.setAttribute(
    "data-weft-compaction",
    session ? "session" : String(c.step ?? "")
  )
  const head = el("div", "weft-call-h", [
    el("span", "weft-name", session ? SESSION_LABEL : "compaction"),
    el("span", "weft-args", compactionLine(c)),
  ])
  const badges = holeBadges([{ hole: "compacted" }])
  if (badges) head.append(badges)
  box.append(head)
  const d = el("details", "weft-collapsible")
  if (open) {
    const key = `${open.scope}\u0000compaction\u0000${session ? `session\u0000${c.hash}` : `view\u0000${c.index ?? ""}`}`
    d.setAttribute("data-weft-open", key)
    if (open.keys.has(key)) d.setAttribute("open", "")
  }
  d.append(el("summary", "", "show original"))
  if (session) {
    d.append(el("div", "weft-res", sessionNote(c)))
  } else {
    const o = originalOf(c, transcript, all)
    if ("loading" in o)
      d.append(el("div", "weft-res", "loading the transcript…"))
    else if ("gap" in o) {
      const gap = holeBadges([{ hole: "gap", reason: o.gap }])
      if (gap) d.append(gap)
      d.append(el("div", "weft-reason", o.gap))
    } else if (!o.messages.length)
      d.append(
        el("div", "weft-res", `nothing replaced: inserted at message ${o.from}`)
      )
    else
      for (const [i, m] of o.messages.entries())
        d.append(
          el("div", "weft-res", messageLine(m), {
            "data-weft-original": String(o.from + i),
          })
        )
    d.append(el("div", "weft-reason", replacedBy(c)))
  }
  box.append(d)
  return box
}

/** answerBlock is the step read, answer first (U10.2, audit §6.3):
 * Studio's drawer at panel width — lib/summarize.ts's verdict, sent,
 * replied, tools ran and then lines, Studio's words — its Logs and Raw
 * OTel handed off to the run page at the step's row (?sel=&tab=). */
function answerBlock(
  step: FoldedStep,
  t: TurnView,
  run: AnswerRun,
  endpoint?: string
): HTMLElement {
  const n = step.index
  const req = t.requests?.steps
  const mine = req?.get(n)
  const row = mine?.rows.at(-1)
  let p = -1
  for (const k of req?.keys() ?? []) if (k < n && k > p) p = k
  const prev = req?.get(p)?.rows.at(-1)
  const running = run.status === "running"
  // The step_finish latency only, as Studio's drawer (a step row has no
  // span of its own to measure: P8-fix-a dropped the panel's fallback).
  const next = t.folded.steps.some((s) => s.index === n + 1)
  const v = stepVerdict(step, run, undefined, !next)
  const dl = el("dl", "weft-ans")
  const line = (
    k: keyof typeof ANSWER_LABELS,
    text: string,
    bad: boolean,
    at: Record<string, string> = { "data-answer-line": k }
  ) => {
    if (bad) at["data-bad"] = ""
    dl.append(el("dt", "", ANSWER_LABELS[k]), el("dd", "", text, at))
  }
  line("verdict", v.text, v.bad, { "data-verdict": "" })
  line(
    "sent",
    sentLine(
      row && {
        count: row.body.messages_ref.count,
        prevCount: prev?.body.messages_ref.count,
        tools: row.body.tools.names.length,
        promptChanged: mine?.promptChanged,
        catalogChanged: mine?.catalogChanged,
      }
    ),
    false
  )
  line("replied", repliedLine(step, running), false)
  line(
    "tools",
    toolsLine(step, running, t.folded.pending, t.doc?.decided),
    step.toolCalls.some((c) => c.result?.isError)
  )
  line("then", thenLine(step, run, next), v.bad)
  const box = el("div", "", [dl], { "data-key": "ans" })
  // Raw OTel hands off here; Logs is the step head's 📄 ⤢ (U11.2's
  // logsLink, under capability logs) — one link per tab.
  if (endpoint)
    box.append(
      el("div", "weft-reason", [
        el("a", "", `${DRAWER_TAB_LABEL.raw} ⤢`, {
          href: href(endpoint, runPage(t.id, { sel: `s${n}`, tab: "raw" })),
          ...NEW_TAB,
          title: "open in Studio",
          "data-weft-handoff": t.id,
        }),
      ])
    )
  return box
}

/** requestLine is the step's request (ADR 0028 §10, the Studio run
 * page's section in one line): the attempts and the changed marks,
 * the system prompt collapsed, the catalog's names, the params. A hole
 * is its badge with its reason and fix, nothing else — a hidden one
 * never holds a byte of prompt, because a read-scoped token never
 * asked for it. */
function requestLine(
  step: number,
  req: PanelRequests,
  runStatus: string,
  open?: OpenState
): HTMLElement {
  const box = el("div", "weft-req")
  box.setAttribute("data-weft-request", String(step))
  if (req.badge) {
    box.append(...requestHole(req.badge, { reason: req.reason, fix: req.fix }))
    return box
  }
  if (req.error) {
    box.append(
      el(
        "span",
        "weft-badge weft-err",
        `request could not be read: ${req.error}`
      )
    )
    return box
  }
  const mine = req.steps.get(step)
  const row = mine?.rows[mine.rows.length - 1]
  if (!mine || !row) {
    box.append(
      runStatus !== "running" && req.truncated
        ? requestCapped(MAX_REQUEST_PAGES * REQUEST_PAGE)
        : runStatus === "running"
          ? el("span", "weft-res", `request: ${REQUEST_NOT_STORED}`)
          : el(
              "span",
              "",
              requestHole("gap", { reason: REQUEST_NO_RECORD_REASON })
            )
    )
    return box
  }
  // One line by default (UX-D-12): the attempts, the marks and the
  // badges; the prompt, the catalog and the params on a click.
  const head = el("summary", "weft-call-h", [
    el("span", "weft-name", "request"),
    el(
      "span",
      "weft-args",
      mine.rows.map((r) => `attempt ${r.attempt}`).join(" · ")
    ),
  ])
  if (mine.promptChanged)
    head.append(
      el("span", "weft-badge weft-info", "prompt changed at this step")
    )
  if (mine.catalogChanged)
    head.append(
      el("span", "weft-badge weft-info", "catalog changed at this step")
    )
  if (row.content && row.content !== "stripped") head.append(badge(row.content))
  const all = el("details", "weft-collapsible", [head])
  if (open) {
    const key = `${open.scope}\u0000req\u0000${step}`
    all.setAttribute("data-weft-open", key)
    if (open.keys.has(key)) all.setAttribute("open", "")
  }
  box.append(all)
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
    d.append(
      el("summary", "", `system prompt · ${text.length} chars`),
      el("div", "weft-res", text)
    )
    all.append(d)
  } else if (row.system_hash) {
    const line = el(
      "div",
      "weft-res",
      `system prompt ${shortHash(row.system_hash)}`
    )
    if (p) line.append(" · ", badge(p.badge))
    all.append(line)
  }
  const names = row.body.tools.names
  all.append(
    el("div", "weft-res", `tools: ${names.length ? names.join(", ") : "none"}`)
  )
  // Every param the adapter's default: nothing to say (UX-D-12).
  if (paramFields(row).some(([, v]) => v !== "adapter default"))
    all.append(el("div", "weft-res", `params: ${paramsLine(row)}`))
  return box
}

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
  root.append(el("style", "", PANEL_CSS))
}

/** prefersReducedMotion reports the page's prefers-reduced-motion:
 * reduce (false where matchMedia is missing or throws). */
function prefersReducedMotion(): boolean {
  try {
    return (
      typeof window.matchMedia === "function" &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches
    )
  } catch {
    return false
  }
}

/** handOff is "open in Studio" for a child run: its run page
 * (studioLink, lib/links.ts's runLink). */
function handOff(endpoint: string, runId: string): HTMLElement {
  return el("a", "weft-btn", "open in Studio ⤢", {
    href: studioLink(endpoint, runId),
    ...NEW_TAB,
    title: `open the child run ${runId} in Studio`,
    "data-weft-handoff": runId,
  })
}

function renderCall(
  call: FoldedToolCall,
  stepIndex: number,
  runStatus: string,
  t?: TurnView,
  open?: OpenState,
  ctx?: FoldCtx
): HTMLElement {
  const box = el("div", "weft-call", "", { "data-key": call.callId })
  const state = callState(call, runStatus)
  // The call's name is its place on the run page (G1): the run tree
  // with this call selected, named with its step — a call id repeats
  // across steps.
  const runId = ctx?.runId
  const name =
    ctx?.endpoint && runId
      ? el("a", "weft-name", call.name, {
          href: href(
            ctx.endpoint,
            runPage(runId, {
              step: stepIndex,
              call: call.callId,
              resumed: call.resumed,
            })
          ),
          ...NEW_TAB,
          title: `open this call in Studio (step ${stepIndex})`,
          "data-weft-call-link": call.callId,
        })
      : el("span", "weft-name", call.name)
  const args = el("span", "weft-args", argsText(call))
  const head = el("div", "weft-call-h", [name])
  box.append(head)
  // The call's args and result are editable in place (plan F2): the
  // args as JSON, checked against the step's catalog schema.
  const ed = ctx?.child ? undefined : ctx?.edit
  const argsEd =
    ed && call.args !== undefined
      ? ed.box(
          { kind: "tool_args", step: stepIndex, callID: call.callId },
          JSON.stringify(call.args, null, 2),
          args,
          `the args of ${call.name} (${call.callId})`,
          call.name
        )
      : args
  head.append(argsEd === args ? args : el("span", "weft-args", argsText(call)))
  if (argsEd !== args) box.append(argsEd)
  const replay = ctx?.replay
  if (call.childRunId && (t || ctx?.child)) {
    // The badge is the child's page too, in its kind's word (U14: a
    // subagent, a delegated session, a run the call started) — a
    // delegated session's own session first (UX-B-06), else the run.
    const kid = t && childRow(t, call.childRunId)
    const kind = kid ? childKind(kid) : "subagent"
    const word = CHILD_EYEBROW[kind]
    const sess = kind === "delegated" && kid ? kid.session_id : ""
    head.append(
      ctx?.endpoint
        ? el("a", "weft-badge weft-info", word, {
            href: sess
              ? href(ctx.endpoint, sessionLink(sess))
              : studioLink(ctx.endpoint, call.childRunId),
            ...NEW_TAB,
            title: sess ? `${OPEN_SESSION} ${sess}` : call.childRunId,
            "data-weft-subagent-link": call.childRunId,
          })
        : el("span", "weft-badge weft-info", word, {
            title: call.childRunId,
          })
    )
    // Any depth inline (UX-D-13): a grandchild expands as its parent did.
    const turn = t ?? ctx?.turn
    if (turn) {
      // A10: replay from here on the child replays the CHILD as its own
      // run (its id, its agent), the parent untouched.
      if (replay) {
        const agent = childRow(turn, call.childRunId)?.agent ?? ""
        head.append(
          verbs("child", [
            verb(
              `replay the child run ${call.childRunId} (agent ${agent || "unnamed"})`,
              "↻",
              rerun(),
              {
                ...replay,
                runId: call.childRunId,
                agent,
                fork: false,
              }
            ),
          ])
        )
      }
      box.append(
        childBlock(call, turn, open, ctx?.endpoint, replay, ctx?.depth)
      )
    }
  }
  if (replay) head.append(callVerbs(stepIndex, call, replay))
  // The call's execution facts (UX-S2-06): lib/cards.ts, the run page's.
  const f =
    t && !ctx?.child
      ? callFactsOf(t.spans, t.id).get(`${stepIndex}:${call.callId}`)
      : undefined
  const ex = execLine(f)
  if (ex) head.append(el("span", "weft-badge weft-info", ex))
  if (call.result) {
    const content = String(call.result.content)
    const cut = truncation(content)
    if (cut) head.append(cutBadge(cut))
    if (call.result.isError) {
      // A Subagent call's typed outcome (UX-S3-15).
      head.append(
        el(
          "span",
          "weft-badge weft-err",
          childOutcome(
            f?.errorType ?? /^([A-Z_]+):/.exec(content)?.[1],
            f,
            !!call.childRunId
          ) ?? "error"
        )
      )
    }
    const holes = holeBadges(call.holes ?? [])
    if (holes) head.append(holes)
    const res = el("div", "weft-res", call.result.content, {
      "data-key": "res",
    })
    box.append(
      ed
        ? ed.box(
            { kind: "tool_result", step: stepIndex, callID: call.callId },
            String(call.result.content),
            res,
            `the result of ${call.name} (${call.callId})`
          )
        : res
    )
  } else if (state === "running") {
    box.append(el("div", "weft-res", "running…"))
  } else {
    // A parked call awaits a decision — a gate or a breakpoint
    // (UX-S3-12, UX-H-18) — it did not fail.
    box.append(
      el(
        "div",
        "weft-res weft-warn",
        t?.folded.pending.some((c) => c.id === call.callId)
          ? parkWord(f, call.name, t.doc, call.callId)
          : "never completed"
      )
    )
  }
  return box
}

/** openTab opens url in a new tab; a blocked popup throws nothing into
 * the host. */
function openTab(url: string) {
  try {
    window.open(url, "_blank", "noopener")
  } catch {
    // blocked
  }
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
    if (
      draft.instructions &&
      draft.instructions !== draft.registeredInstructions
    )
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
    // …and the option lab and the edits: the same command, not a cousin.
    p.lab = labHandoff(draft.lab)
    p.edits = editsHandoff(draft.edits)
  }
  return href(endpoint, playgroundLink(p))
}

/** lastStep is the highest step the request record holds a row of. */
function lastStep(req: PanelRequests): number {
  let n = -1
  for (const k of req.steps.keys()) if (k > n) n = k
  return n
}

function renderStep(
  step: FoldedStep,
  runStatus: string,
  t?: TurnView,
  selectedStep?: number | null,
  open?: OpenState,
  ctx?: FoldCtx,
  streaming = false,
  ans?: number
): HTMLElement {
  const card = el("div", "weft-step", "", {
    "data-key": `s${step.index}`,
  })
  card.setAttribute("data-weft-step", String(step.index))
  if (selectedStep === step.index)
    card.style.outline = "1px solid var(--weft-accent)"
  const head = el("div", "weft-step-h", [
    el("span", "", `step ${step.index}`),
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
  const complete =
    !req?.error && (!req?.truncated || step.index < lastStep(req))
  if (step.finish) {
    head.append(
      el("span", "", step.finish.reason),
      el("span", "", usageLine(step.finish.usage))
    )
  }
  // A step whose tools started has answered (tools run only after a
  // successful model call), finished or not.
  const line = complete
    ? attemptLine(
        factsFromRows(
          rows,
          !!step.finish || step.toolCalls.length > 0,
          runStatus === "running"
        )
      )
    : null
  if (line)
    head.append(
      el("span", "weft-badge weft-info", line, { "data-weft-attempts": "" })
    )
  if (step.finish) {
    const timing = timingLine(step.finish.latencyMs, step.finish.ttftMs, "ttft")
    if (timing) head.append(el("span", "", timing, { "data-weft-timing": "" }))
  }
  const own = stepHoles(step, ctx?.child ? ctx.child.doc?.holes : t?.doc?.holes)
  const old = complete ? attemptsHole(step.finish, rows.length) : null
  const holes = holeBadges(old ? mergeHoles(own, [old]) : own)
  if (holes) head.append(holes)
  if (ctx?.replay) head.append(stepVerbs(step.index, ctx.replay))
  if (ctx?.logs && ctx.endpoint && ctx.runId && !ctx.child)
    head.append(
      el("a", "", "📄 ⤢", {
        href: href(ctx.endpoint, logsLink(ctx.runId, step.index)),
        ...NEW_TAB,
        title: "app logs in Studio",
        "data-weft-logs": String(step.index),
      })
    )
  card.append(head)
  const body = el("div", "weft-step-b")
  // The answer first (§6.3), on renderFolded's landing step.
  const run = ctx?.run
  if (t && run && step.index === ans)
    body.append(answerBlock(step, t, run, ctx.endpoint))
  // The step's run-scope compaction views (A9.2), on its step line.
  const doc = ctx?.child ? ctx.child.doc : t?.doc
  const comps = compactionsOf(doc)
  for (const c of comps)
    if (!isSessionMarker(c) && c.step === step.index)
      body.append(
        compactionBox(
          c,
          comps,
          ctx?.child ? ctx.child.transcript : t?.transcript,
          open
        )
      )
  if (req) body.append(requestLine(step.index, req, runStatus, open))
  if (step.reasoning) {
    const d = el("details", "weft-collapsible")
    if (open) {
      // Collapsed by default (§2); what the user opened stays open
      // across redraws.
      const key = `${open.scope}\u0000reasoning\u0000${step.index}`
      d.setAttribute("data-weft-open", key)
      if (open.keys.has(key)) d.setAttribute("open", "")
    }
    d.append(el("summary", "", "reasoning"), el("div", "", step.reasoning))
    body.append(d)
  }
  // Busy while it grows: a reader waits rather than re-reading the
  // text per draw; the step's end is said once by the dock's status.
  if (streaming)
    body.append(
      el("div", "weft-stream", step.text, {
        "aria-live": "polite",
        "aria-busy": "true",
      })
    )
  // A child's final words that are its call's result: drawn there once.
  else if (step.text && step.text.trim() !== ctx?.said?.trim()) {
    const said = el("div", "", step.text, { "data-key": "text" })
    // A call-free reply only: a rewrite may not drop a step's calls.
    body.append(
      ctx?.edit && !step.toolCalls.length
        ? ctx.edit.box(
            { kind: "reply", step: step.index },
            step.text,
            said,
            `the reply of step ${step.index}`
          )
        : said
    )
  }
  if (step.steer) {
    const words = step.steer.text
    // The step's own user message (its stored step), never another
    // step's same words.
    const u = ctx?.edit?.users.find(
      (x) => !(x.step === 0 && x.index === 0) && x.step === step.index
    )
    const note = el("div", "weft-note", `steered: ${words}`, {
      "data-key": "steer",
    })
    // The steer is kept in the prefix; the step that answers it runs
    // fresh (none after it: nothing to replay).
    const next = step.index + 1
    const max = ctx?.replay?.max
    // The verbs beside the note, never inside it: an editable note is
    // a button itself (the call head's rule).
    const row = el("div", "", [
      u && ctx?.edit
        ? ctx.edit.box(
            { kind: "user", step: u.step, index: u.index },
            words,
            note,
            `the steer after step ${step.index}`
          )
        : note,
    ])
    if (ctx?.replay && max != null && next <= max)
      row.append(
        verbs("steer", [
          verb(
            `replay from this steer (step ${next} runs fresh)`,
            "↦",
            replayFromStep(next),
            ctx.replay
          ),
        ])
      )
    body.append(row)
  }
  for (const call of step.toolCalls)
    body.append(renderCall(call, step.index, runStatus, t, open, ctx))
  card.append(body)
  return card
}

/** parkedHead is a parked call's line: its name, its arguments (the
 * JSON, "(…)" when the record has none), then what follows them. */
function parkedHead(call: ToolCallPart, after: HTMLElement[]): HTMLElement {
  return el("div", "weft-call-h", [
    el("span", "weft-name", call.name),
    el(
      "span",
      "weft-args",
      call.args === undefined ? "(…)" : fmtJSON(call.args)
    ),
    ...after,
  ])
}

/** renderTimeline is the Timeline tab (D4, U8.3): lib/waterfall.ts's
 * run tree — Studio's run page's rows, one level deep: the run, its
 * steps, their model calls and tools (and the spans under them), each
 * child run one row with ⤢ handing off to Studio's run page at that row
 * (?sel=), never its subtree. A row is drawn as Studio draws it: indented
 * by depth, its kind's glyph (ROW_KIND, KIND_ICON), its tone's colour
 * (rowTone, lib/palette.ts's TONE), the label rule, the critical path,
 * its extent (took; the offset as its title), an untimed row's words.
 * Over a time axis (ms from the run's first span) when the run has
 * spans; without, the event sequence (seq: the order scale). A row of
 * a step carries data-weft-step: a click selects that step in Story. */
export function renderTimeline(
  t: TurnView,
  sel?: number | null,
  endpoint?: string
): HTMLElement {
  const wf = turnRows(t)
  const time = wf.scale === "time"
  const end = wf.domain[1]
  const box = el("div", "weft-timeline", "", {
    "data-axis": time ? "time" : "seq",
  })
  const lost = wf.unplaced
  if (lost > 0)
    box.append(
      el(
        "div",
        "weft-note weft-warn",
        `${plural(lost, "span")} not placed: unreadable times, or an end before the start`
      )
    )
  if (wf.rows.length < 2) {
    box.append(
      el("div", "weft-note", "nothing to place yet: no spans and no steps")
    )
    return box
  }
  const ticks = el("div", "weft-axis")
  for (const f of [0, 0.25, 0.5, 0.75, 1]) {
    const v = Math.round(end * f)
    const tick = el("span", "weft-tick", time ? `${v} ms` : `seq ${v}`)
    tick.style.left = `${f * 100}%`
    ticks.append(tick)
  }
  box.append(
    el("div", "weft-wf-row", [
      el("span", "weft-wf-name", time ? "time (ms)" : "seq"),
      ticks,
      el("span", "weft-wf-ms"),
    ])
  )
  const wrap = el("div", "weft-wf")
  const steps = wf.rows.filter((r) => r.step !== undefined)
  const stop = (
    steps.find((r) => r.kind === "step" && r.step === sel) ?? steps.at(0)
  )?.key
  for (const r of wf.rows) {
    const kind = ROW_KIND[r.kind]
    const b = r.status === "ok" ? undefined : rowBadge(r)
    const sub = [r.gist, lanesWord(r.lanes)].filter(Boolean).join(" · ")
    const name = el(
      "span",
      "weft-wf-name",
      [
        el("i", "", KIND_ICON[kind], { "aria-hidden": "true" }),
        el("span", "weft-tl-l", r.label),
        ...(b ? [el("b", "", b)] : []),
      ],
      { title: sub ? `${r.label} — ${sub}` : r.label }
    )
    name.style.paddingLeft = `${r.depth * 2}ch`
    if (r.child && endpoint)
      name.append(
        el("a", "", "⤢", {
          href: href(endpoint, runPage(t.id, { sel: r.key })),
          ...NEW_TAB,
          title: "open in Studio",
          "data-weft-handoff": r.child.runId,
        })
      )
    const track = el("span", "weft-wf-track")
    if (r.untimed === "missing")
      track.append(badge(UNTIMED_HOLE.hole, UNTIMED_HOLE))
    else if (r.untimed)
      // A call never run (rule 11) reads its words alone, as the
      // panel's park words do; pending keeps its hint.
      track.append(
        r.untimed == "unrun"
          ? el("span", "weft-tl-u", NOT_EXECUTED)
          : el("span", "weft-tl-u", UNTIMED_PENDING, {
              title: UNTIMED_PENDING_HINT,
            })
      )
    else {
      const bar = el("span", "weft-wf-bar")
      const w = r.took ?? end - r.start
      bar.style.left = `${((r.start / end) * 100).toFixed(2)}%`
      bar.style.width = `${(Math.max(0.005, w / end) * 100).toFixed(2)}%`
      track.append(bar)
    }
    const at: Record<string, string> = {
      "data-key": `tl:${r.key}`,
      "data-kind": kind,
      "data-tone": rowTone(r),
    }
    // A row of a step is a button (P7): Enter or Space selects it, the
    // arrows move one tab stop (the selected step's row, else the first).
    if (r.step !== undefined) {
      at["data-weft-step"] = String(r.step)
      at.role = "button"
      at.tabindex = r.key === stop ? "0" : "-1"
    }
    if (r.critical) at["data-critical"] = ""
    wrap.append(
      el(
        "div",
        `weft-wf-row weft-tl${r.kind === "step" && r.step === sel ? " weft-sel" : ""}`,
        [
          name,
          track,
          el(
            "span",
            "weft-wf-ms",
            extent(r, wf.scale),
            time ? { title: `+${took(r.start)}` } : undefined
          ),
        ],
        at
      )
    )
  }
  box.append(wrap)
  return box
}

/** sourceLabel takes the turn back out of a `Replay 1 of t3` label (the
 * diff header reads "diff vs t3"). */
function sourceLabel(label: string): string {
  return label.split(" of ")[1] || label
}

function argsText(call: FoldedToolCall): string {
  if (typeof call.args === "string") return `(${call.args}`
  if (call.args !== undefined) {
    try {
      return `(${JSON.stringify(call.args)})`
    } catch {
      return "(?)"
    }
  }
  return call.streamedArgs ? `(${call.streamedArgs}…)` : "(…)"
}

/** loadedOnly is a cost sum's note while older turns load on demand
 * (A5.3): the sum covers the loaded page only, so it reads "≥". */
function loadedOnly(
  s: PanelState,
  n: number,
  what: string
): string | undefined {
  return s.turnsCapped
    ? `the ${n} loaded ${what}${n > 1 ? "s" : ""} only — load older turns for more`
    : undefined
}

/** usageLine renders usage with the cached/reasoning splits §2 names. */
function usageLine(u: Usage): string {
  const parts = [`${tokens(u.input_tokens)}→${tokens(u.output_tokens)} tok`]
  if (u.cached_input_tokens)
    parts.push(`${tokens(u.cached_input_tokens)} cached`)
  if (u.reasoning_tokens) parts.push(`${tokens(u.reasoning_tokens)} reasoning`)
  if (u.cache_write_tokens)
    parts.push(`${tokens(u.cache_write_tokens)} cache-write`)
  return parts.join(" · ")
}

/** mount is the programmatic mount (plan C2's rung 1; the npm entry,
 * C1, exports it — the script-tag bundle stays a side-effect module):
 * a <weft-devtools> configured by opts, field by field above every
 * other source, appended to target (default document.body). It is the
 * host's mount: where Studio does not answer it shows the
 * not-reachable line, never removes itself. */
export function mount(
  opts: MountOptions & { target?: Element } = {}
): WeftDevtools {
  if (!customElements.get("weft-devtools"))
    customElements.define("weft-devtools", WeftDevtools)
  const { target, ...options } = opts
  const node = document.createElement("weft-devtools") as WeftDevtools
  node.options = options
  ;(target ?? document.body).append(node)
  return node
}
