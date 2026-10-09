// The panel's data state (WEFT-DEVTOOLS.md §2, rung 1): one
// conversation by public id — the header from sessions?public_id=,
// the turn list from runs?public_id= plus /api/live?public_id= run
// frames, the selected turn's step story from events + transcript
// through the shared fold, its live tail from /api/live?run= with
// deltas, spans for the honesty badges (and, at Dv3, the waterfall).
// V6 (one API, two clients): these are the Studio UI's own endpoints,
// reached through lib/api.ts's mirrors and lib/events.ts's fold.
//
// The panel runs inside someone else's page, over records that were
// stored as ingested: every async path here ends in a catch, every
// timer is the model's own (dispose clears them), every retry is
// bounded, and a response that arrives for a view the user has left
// is dropped.
import type { Holed, Meta, PosEvent, RunDoc, RunRow, SessionRow, Span, ToolCallPart, Transcript } from "../lib/api"
import { HOLES } from "../lib/honesty"
import { byStep } from "../lib/requests"
import type { StepRequests } from "../lib/requests"
import { serializeScope } from "../lib/scope"
import type { Scope } from "../lib/scope"
import { tokenScope } from "./config"
import { applyTranscript, linkView, newFold } from "../lib/events"
import type { FoldFeed, FoldedRun } from "../lib/events"
import type { LiveRecord, LiveRun } from "../lib/live"
import {
  fetchCommand,
  fetchEvents,
  fetchMeta,
  fetchRequests,
  fetchStrippedNote,
  fetchRun,
  fetchRuns,
  fetchRuntimes,
  fetchSessions,
  fetchSpans,
  fetchTranscript,
  openPanelLive,
  PanelApiError,
  postApproval,
  postPlaygroundRun,
  postSteer,
  putBreakpoints,
} from "./client"
import type { CommandStatus, PanelEndpoint, PanelLiveHandle, RuntimeView } from "./client"
import {
  buildRunBody,
  draftProblem,
  experimentLabel,
  foldedWords,
  pickRuntime,
  turnPromptOf,
  turnWordsOf,
} from "./playground"
import type { ExperimentDraft, ExperimentResult, TurnWords } from "./playground"
import { studioIsTooNew } from "./version"

/** The runs list's paging cursor (next_before, next_before_id). */
interface Cursor {
  before: string
  id: string
}
const sameCursor = (a: Cursor | null, b: Cursor) => !!a && a.before === b.before && a.id === b.id

/** The events walk's page cap: 20 pages of 500. A longer run says so
 * (capped) instead of reading as complete. */
export const MAX_EVENT_PAGES = 20
/** The turn list's page: the newest runs of the conversation, and each
 * older page (D4: before=/before_id= paging, no cap). */
export const TURNS_LIMIT = 50
/** How often a stream that closed for good is reopened before the
 * panel stops knocking (5 s, doubling, capped at a minute). */
export const LIVE_RETRIES = 5
/** The server's overflow frame (the client fell behind) reopens on its
 * own bounded backoff: OVERFLOW_MIN_MS after the frame, doubling for
 * each overflow that follows the last within OVERFLOW_CALM_MS, capped
 * at a minute — never at once (each reopen is a new grant and a
 * refetch), never given up (an overflow is not a failure). */
export const OVERFLOW_MIN_MS = 1_000
export const OVERFLOW_CALM_MS = 60_000
/** The lifecycle poll: its cadence, and how many failed reads in a
 * row end it. */
const POLL_MS = 700
const POLL_FAILS = 8
/** The dev list (no public id) is streamed on its newest run's agent
 * (agent=…, plan C3.4: S4.5 has no selector for everything); while no
 * stream is up (no agent known yet, the grant refused, the stream gone)
 * it is read again this often while the dock is open and the page
 * visible — with one up, every DEV_DISCOVERY_MS. */
export const DEV_POLL_MS = 10_000
/** The dev list's length: the newest top-level runs. */
export const DEV_LIMIT = 10
/** While the fallback's agent stream is up, the dev list is still read
 * this often (dock open, page visible): a run of another agent that
 * starts later is found, never silently missing. */
export const DEV_DISCOVERY_MS = 30_000
/** While a tail streams, the dock is redrawn at most this often: a
 * redraw rebuilds the whole dock inside the host page, and a burst of
 * deltas must not cost it a rebuild per animation frame. Everything
 * else (a click's answer, a turn loading) draws on the next frame. */
export const LIVE_DRAW_MS = 100

/** When the scope's run is not listed on the first look (a streaming
 * handler's header lands before the run row does), the list is read
 * once more this much later; only a second miss says "not in this
 * conversation". */
export const PIN_RECHECK_MS = 1_000
/** How many times an experiment's run is read after its command
 * finished, 1 s apart, before the pane settles on what it has. */
const SETTLE_READS = 15

/** One loaded subagent child (Dv3 expands them lazily; the fetch is
 * the same turn-view pipeline over the child's own run id). */
export interface ChildView {
  events: PosEvent[]
  feed: FoldFeed
  folded: FoldedRun
  transcript: Transcript | null
  /** The events walk stopped at the page cap. */
  capped: boolean
  /** The child's own document (its holes; its children, the
   * grandchildren the panel only hands off to Studio); null when it
   * could not be read. */
  doc: RunDoc | null
  /** The child's request record, read by the child's id (plan A10):
   * the same scope rules as the turn's — a read-scoped token never
   * asks and holds the hidden badge. */
  requests: PanelRequests | null
  /** The child's status (the parent's row of it) when it was read: a
   * reload of the parent that finds it moved reads the child again. */
  status: string
}

/** The selected turn's data: the run doc (children by
 * parent_call_id), the paged events folded so far, the transcript
 * overlay (finished text — deltas are live-only), and the spans. */
export interface TurnView {
  id: string
  doc: RunDoc | null
  events: PosEvent[]
  gaps: number[]
  /** The events walk stopped at the page cap: the story is the run's
   * first MAX_EVENT_PAGES pages, and the view says so. */
  capped: boolean
  /** Durable event positions already folded: the pages and the live
   * lane both deliver them (a reconnect backfills from position 0),
   * and a tool call must fold once. */
  seen: Set<number>
  feed: FoldFeed
  folded: FoldedRun
  transcript: Transcript | null
  spans: Span[] | null
  /** The run's request record per step (ADR 0028 §10), or the hole
   * that stands for it (not_recorded; hidden for a read-scoped token,
   * which never asks); null without the requests capability or before
   * it was read. */
  requests: PanelRequests | null
  children: Map<string, ChildView>
  /** Subagent expanders the user opened (survives re-renders). */
  expanded: Set<string>
  /** Children whose load was started: an open expander is redrawn on
   * every state change, and each redraw must not fetch again (a child
   * that cannot be read is asked for once, until the user reopens it). */
  tried: Set<string>
  /** Children whose history could not be read (the walk failed): the
   * Request tab says so and hands off to Studio. */
  unreachable: Set<string>
  /** The run ended and its stored records were reloaded: late deltas
   * are dropped (the transcript carries the final words). */
  done: boolean
  /** A reload of this view is in flight. */
  loading: boolean
  /** The run ended while that reload was in flight: what it read may
   * predate the end, so it reads once more. */
  again: boolean
  /** The highest durable position folded (-1: none yet). */
  pos: number
  /** Live frames that arrived while the stored pages were being read:
   * folded after them, in arrival order. */
  held: LiveRecord[]
  /** A catch-up read of the pages past pos is in flight. */
  reading: boolean
  /** The stream (re)opened while a read was in flight: what was
   * published before the server subscribed may be in neither, so the
   * pages past pos are read once more when the read ends. */
  recheck: boolean
  /** The fold changed since folded was taken: the next frame takes it
   * again (once per frame, not once per delta). */
  stale: boolean
}

/** A run the panel follows live — the open turn, or the result pane's
 * run: its fold, and where its durable events stand. */
export interface Lane {
  events: PosEvent[]
  seen: Set<number>
  feed: FoldFeed
  pos: number
  held: LiveRecord[]
  reading: boolean
  recheck: boolean
  stale: boolean
  /** A full reload (a rebuild from position 0) is in flight. */
  loading: boolean
}

export interface PanelState {
  /** meta answered; null until then. */
  meta: Meta | null
  /** Studio is newer than this panel understands (§5.1). */
  tooNew: boolean
  /** GET /api/meta failed: the panel removes itself silently (§5.3). */
  gone: boolean
  /** The header's session row (sessions?public_id=). */
  session: SessionRow | null
  /** The conversation's top-level turns, newest first. */
  turns: RunRow[]
  /** Older turns exist past the loaded pages (the runs cursor is set). */
  turnsCapped: boolean
  /** An older page was loaded (D4): the list says when all are. */
  paged: boolean
  /** An older page is being read. */
  loadingOlder: boolean
  /** The runs cursor's values ("before|before_id"), "" with none: the
   * sentinel is keyed by it (a page read is a new sentinel). */
  olderAt: string
  /** Experiments (playground runs) by the source turn id they hang
   * off (forked_from's run part). Empty until step 8 turns the
   * playground on; the slot exists (§2). */
  experiments: Map<string, RunRow[]>
  selected: string
  /** The step the user is reading (Dv3): carried into Studio by the ⤢
   * deep link (?step=N&view=story). */
  selectedStep: number | null
  turn: TurnView | null
  /** The experiment drawer's draft (WEFT-PLAYGROUND §3); null when
   * closed. Rendered only when meta reports the playground. */
  drawer: ExperimentDraft | null
  /** The connected runtimes the drawer picks from (§10.4). */
  runtimes: RuntimeView[]
  /** The drawer's running or finished experiment (the result pane). */
  result: ExperimentResult | null
  /** The drawer's runtime's breakpoint set (§8.3): the stored set
   * GET /api/runtimes reports, then what PUT …/breakpoints answered. */
  breakpoints: string[]
  /** The scope subscription (or, with no public id, the fallback's
   * agent stream) is open (the live dot). */
  live: boolean
  /** The fallback (no public id, plan C3.4): the agent whose runs the
   * dev list streams (agent=…), "" while none does. */
  devAgent: string
  /** The fallback's stream was refused (403, or a panel token, which is
   * never granted an agent's stream): the dev list polls, and says so. */
  devRefused: boolean
  /** The scope's run, when it is not among the conversation's listed
   * runs: "run r_… not in this conversation", an honest line (C3.2). */
  pinMissing: string
  /** The scope names a session but no listed run carries a session id:
   * the list is not narrowed, and says so. */
  sessionUnrecorded: boolean
  /** The conversation (listKey: public id and session) whose turn list
   * was read last, "" before the first read: the host events (plan C4)
   * take a conversation's first read as its history, not as
   * transitions. */
  listKey: string
}

export type PanelNotify = (s: PanelState) => void

/** emptyPanelState is the dock's shape before meta answers. */
export function emptyPanelState(): PanelState {
  return {
    meta: null,
    tooNew: false,
    gone: false,
    session: null,
    turns: [],
    turnsCapped: false,
    paged: false,
    loadingOlder: false,
    olderAt: "",
    experiments: new Map(),
    selected: "",
    selectedStep: null,
    turn: null,
    drawer: null,
    runtimes: [],
    result: null,
    breakpoints: [],
    live: false,
    devAgent: "",
    devRefused: false,
    pinMissing: "",
    sessionUnrecorded: false,
    listKey: "",
  }
}

/** A turn's request record as the panel draws it: the run's hole
 * (badge) or its rows by step. */
export interface PanelRequests extends Holed {
  steps: Map<number, StepRequests>
  /** The walk stopped at its page cap: a step past it is not a gap. */
  truncated?: boolean
  /** The record could not be read (not a hole: an error). */
  error?: string
  /** A stripped row's reason and fix (the tools route's words). */
  stripped?: Holed
}

/** newTurnView starts an empty fold for one run. */
function newTurnView(id: string): TurnView {
  const feed = newFold()
  return {
    id,
    doc: null,
    events: [],
    gaps: [],
    capped: false,
    seen: new Set(),
    feed,
    folded: feed.result(),
    transcript: null,
    spans: null,
    requests: null,
    children: new Map(),
    expanded: new Set(),
    tried: new Set(),
    unreachable: new Set(),
    done: false,
    loading: false,
    again: false,
    pos: -1,
    held: [],
    reading: false,
    recheck: false,
    stale: false,
  }
}

/** partitionRuns splits a runs page into top-level turns and the
 * experiments that hang off them (a playground run carries the public
 * id but no session id; its source turn is forked_from's run part). */
export function partitionRuns(runs: RunRow[]): { turns: RunRow[]; experiments: Map<string, RunRow[]> } {
  const turns: RunRow[] = []
  const experiments = new Map<string, RunRow[]>()
  for (const r of runs) {
    const source = r.forked_from ? r.forked_from.split("#")[0] : ""
    if (r.playground || r.experiment_id || source) {
      const key = source || r.id
      const list = experiments.get(key) ?? []
      list.push(r)
      experiments.set(key, list)
      continue
    }
    turns.push(r)
  }
  turns.sort((a, b) => Date.parse(b.started) - Date.parse(a.started))
  return { turns, experiments }
}

/** strippedContent reports whether the turn's content was not
 * captured (§5.4: say so instead of showing empty boxes): its events'
 * attrs — weft.content = stripped from a content-off chain, or none
 * from the core — folded into the run's stripped hole. Spans never
 * carry weft.content; the events do. */
export function strippedContent(view: FoldedRun | null | undefined): boolean {
  return Boolean(view?.holes?.some((h) => h.hole === "stripped"))
}

/** One events walk: the pages followed, and whether the cap ended it. */
interface EventWalk {
  events: PosEvent[]
  gaps: number[]
  capped: boolean
}

/** feedEvent pushes one stored event into a fold. Event bodies are
 * stored as ingested, so a body may be null, a string or an object
 * with no type: skipped, and a fold that throws on a shape it does not
 * know is contained — nothing here may throw into the host page. */
function feedEvent(feed: FoldFeed, ev: unknown, pos: number, attrs?: PosEvent["attrs"]): void {
  if (!ev || typeof ev !== "object" || typeof (ev as { type?: unknown }).type !== "string") return
  try {
    feed.push(ev as PosEvent["event"], pos, attrs)
  } catch {
    // an event the fold cannot read: skipped
  }
}

/** foldInto folds durable events once each, by position. */
function foldInto(feed: FoldFeed, seen: Set<number>, events: (PosEvent | null)[]): void {
  for (const p of events) {
    if (!p || typeof p.pos !== "number" || seen.has(p.pos)) continue
    seen.add(p.pos)
    feedEvent(feed, p.event, p.pos, p.attrs)
  }
}

/** restart rebuilds a lane's fold from stored pages (position 0 on). */
function restart(lane: Lane, events: PosEvent[]): void {
  const feed = newFold()
  const seen = new Set<number>()
  foldInto(feed, seen, events)
  lane.feed = feed
  lane.seen = seen
  lane.events = events
  lane.pos = seen.size ? Math.max(...seen) : -1
  lane.stale = true
}

/** foldLive folds one live frame into a lane. Events fold once each,
 * by position, and in order: a position past the next one means the
 * stream never carried what lies between (published before the server
 * subscribed, or lost on the way) — a "gap" the stored pages fill.
 * force folds past one: the pages were read and the hole is a lost
 * batch, not something to wait on. Deltas are live-only and carry
 * their own counter (the hook's rule, hooks/use-run-events.ts). */
function foldLive(lane: Lane, rec: LiveRecord, force = false): "folded" | "skip" | "gap" {
  const pos = Number(rec.pos)
  if (rec.kind === "event") {
    if (!Number.isFinite(pos) || lane.seen.has(pos)) return "skip"
    if (!force && pos > lane.pos + 1) return "gap"
    lane.seen.add(pos)
    lane.events.push({ pos, time: rec.time, event: rec.event, ...(rec.attrs ? { attrs: rec.attrs } : {}) })
    if (pos > lane.pos) lane.pos = pos
  } else if (rec.kind !== "delta") return "skip"
  feedEvent(lane.feed, rec.event, pos, rec.attrs)
  lane.stale = true
  return "folded"
}

/** viewOf takes a fold's view; pending is read by the approval
 * controls, so it is always a list. */
function viewOf(feed: FoldFeed): FoldedRun {
  const out = feed.result()
  if (!Array.isArray(out.pending)) out.pending = []
  return out
}

/** overlay puts the run's finished words on a fold (applyTranscript
 * maps only what the run produced onto its steps — its input record,
 * the conversation it was fed, is not its reply). */
function overlay(folded: FoldedRun, transcript: Transcript | null, terminal = false): FoldedRun {
  if (!transcript) return folded
  try {
    // A terminal run's stored words are the story: they replace what
    // the tail streamed (deltas are never backfilled — text streamed
    // across a dropped connection keeps its hole otherwise).
    return applyTranscript(folded, transcript.batches, { replace: terminal })
  } catch {
    return folded
  }
}

const quiet = () => {}

/**
 * PanelModel connects the panel to one Studio and one conversation.
 * One meta request on start (fail silent, no retries); one live
 * subscription per scope; one per watched turn.
 */
export class PanelModel {
  state: PanelState = emptyPanelState()

  private notify: PanelNotify
  private ep: PanelEndpoint
  private scopeSub?: PanelLiveHandle
  private runSub?: PanelLiveHandle
  private expSub?: PanelLiveHandle
  /** The fallback's agent stream (no public id). */
  private devSub?: PanelLiveHandle
  /** The fallback's stream closed and its next open waits on the
   * backoff (retry "dev"); still set once LIVE_RETRIES ran out. */
  private devBackoff = false
  private disposed = false
  private loadSeq = 0 // selects the freshest async load after a rescope
  private timers = new Set<ReturnType<typeof setTimeout>>()
  /** Reopen attempts since each stream last opened (LIVE_RETRIES). */
  private retries = { scope: 0, run: 0, exp: 0, dev: 0 }
  /** Each lane's overflow backoff: the step it is at and the last overflow. */
  private overflows = { scope: { n: 0, at: 0 }, run: { n: 0, at: 0 }, exp: { n: 0, at: 0 }, dev: { n: 0, at: 0 } }
  /** Run frames that arrived while a turn-list fetch was in flight:
   * the fetch's snapshot is older than they are, so they are applied
   * over it. */
  private collectors = new Set<RunRow[]>()
  private posting = false
  private deciding = false

  /** The conversation followed; "" is the dev list. */
  publicId: string
  /** What narrows it (C3.2): session filters the turn list (on runs
   * that carry weft.session.id), run pins the selected turn once per
   * scope, flow is carried (the header's chip) and filters nothing. */
  narrowing: Omit<Scope, "publicId"> = {}
  /** The run the pin was last applied for: a pin selects its run once,
   * the user's clicks own the selection afterwards. */
  private pinApplied = ""
  /** The user clicked a turn since the last pin: a new run of the same
   * conversation (the next turn's header) no longer moves the
   * selection. An explicit rescope (force) clears it. */
  private userSelected = false
  /** Looks (a refresh, a live frame) for the pinned run since it was
   * set: the missing line waits for the second. */
  private pinChecks = 0
  private pinTimer: ReturnType<typeof setTimeout> | null = null

  /** The turn to select first when the list has it (the element's
   * remembered run, D1); used once. */
  prefer = ""

  constructor(ep: PanelEndpoint, scope: Scope | string, notify: PanelNotify) {
    this.ep = ep
    this.notify = notify
    const s: Scope = typeof scope === "string" ? { publicId: scope } : scope
    this.publicId = s.publicId
    this.narrowing = narrowingOf(s)
  }

  /** following is the scope the model follows, as one value. */
  get following(): Scope {
    return { publicId: this.publicId, ...this.narrowing }
  }

  /** start checks meta once, then follows the conversation. */
  async start(): Promise<boolean> {
    let meta: Meta
    try {
      // Whatever answered: a 200 with JSON that is not Studio's meta
      // (an app's own catch-all route) is not a Studio, same as no
      // answer.
      const doc = (await fetchMeta(this.ep)) as Partial<Meta> | null
      if (!doc || typeof doc !== "object" || typeof doc.studio_version !== "string")
        throw new Error("not a Studio")
      meta = { ...(doc as Meta), capabilities: Array.isArray(doc.capabilities) ? doc.capabilities : [] }
    } catch {
      this.state.gone = true
      return false
    }
    if (this.disposed) return false
    this.state.meta = meta
    this.state.tooNew = studioIsTooNew(meta.studio_version)
    this.emit()
    if (this.state.tooNew) return true
    await this.scope()
    return true
  }

  /** rescope follows another scope (the §5.2 setter path, the header
   * rung): another public id starts over on that conversation; the
   * same one with another session, flow or run re-narrows the list
   * already followed — never a restart: a new run there re-pins the
   * selection unless the user has clicked a turn since the last pin
   * (opts.force, an explicit rescope, pins regardless). A string is a
   * public id. */
  async rescope(next: Scope | string, opts: { force?: boolean } = {}) {
    const s: Scope = typeof next === "string" ? { publicId: next } : next
    const narrowing = narrowingOf(s)
    const sameNarrowing = serializeScope({ publicId: "", ...narrowing }) === serializeScope({ publicId: "", ...this.narrowing })
    if (s.publicId === this.publicId && sameNarrowing) return
    const samePublic = s.publicId === this.publicId
    const sameSession = narrowing.session === this.narrowing.session
    if (narrowing.run !== this.narrowing.run) {
      this.pinApplied = ""
      this.pinChecks = 0
      this.cancel(this.pinTimer)
      this.pinTimer = null
    }
    if (opts.force) this.userSelected = false
    this.publicId = s.publicId
    this.narrowing = narrowing
    if (!this.state.meta || this.state.tooNew) return
    if (!samePublic) {
      await this.scope()
      return
    }
    // The same conversation: a run already listed is pinned in place;
    // otherwise (or with another session) the list is read again.
    if (sameSession && (!narrowing.run || this.rowOf(narrowing.run))) {
      const pin = this.takePin()
      if (pin) await this.select(pin)
      else this.emit()
      return
    }
    await this.refresh()
  }

  /** scope starts over on the conversation: the header and the turn
   * list, and the live lane for it (S4.5: exactly one selector).
   * Nothing of the previous conversation stays — its open turn, its
   * drawer and its result belong to another public id. */
  private async scope() {
    const seq = ++this.loadSeq
    this.scopeSub?.close()
    this.runSub?.close()
    this.expSub?.close()
    this.devSub?.close()
    this.scopeSub = this.runSub = this.expSub = this.devSub = undefined
    this.clearTimers()
    this.retries = { scope: 0, run: 0, exp: 0, dev: 0 }
    this.overflows = { scope: { n: 0, at: 0 }, run: { n: 0, at: 0 }, exp: { n: 0, at: 0 }, dev: { n: 0, at: 0 } }
    this.devBackoff = false
    const s = this.state
    s.live = false
    s.devAgent = ""
    s.devRefused = false
    s.session = null
    s.turns = []
    s.turnsCapped = s.paged = s.loadingOlder = false
    this.setCursor(null)
    s.experiments = new Map()
    s.selected = ""
    s.selectedStep = null
    s.turn = null
    s.drawer = null
    s.result = null
    s.pinMissing = ""
    s.sessionUnrecorded = false
    this.pinApplied = ""
    this.pinChecks = 0
    this.pinTimer = null // clearTimers above dropped it
    this.userSelected = false
    this.compareWords.clear()
    this.emit()
    // Subscribe before the list is fetched: a run frame that lands
    // during the fetch is newer than the page and is applied over it
    // (refresh's collector), so no change falls between the two.
    this.subscribe(seq)
    this.armDev()
    await this.refresh()
  }

  /** subscribe opens the conversation's live lane: run frames only —
   * the turn list is all this stream feeds (each open turn has its own
   * tail). Without a public id there is the dev list (latest turns,
   * unscoped — §5.2's default) and no selector that covers it (S4.5
   * wants exactly one): history only, and the dot says so. */
  private subscribe(seq: number) {
    this.scopeSub?.close()
    this.scopeSub = undefined
    if (!this.publicId) return
    const again = async () => {
      if (seq !== this.loadSeq || this.disposed) return
      this.subscribe(seq)
      await this.refresh()
    }
    this.scopeSub = openPanelLive(this.ep, {
      selector: { public_id: this.publicId },
      kinds: ["run"],
      onOpen: (reopened) => {
        this.retries.scope = 0
        if (!this.state.live) {
          this.state.live = true
          this.emit()
        }
        // The browser reconnected by itself: run frames sent while the
        // stream was down are gone — the list is refetched.
        if (reopened) void this.refresh().catch(quiet)
      },
      onRun: (f) => this.onRunFrame(f),
      onOverflow: (why) => {
        this.scopeSub = undefined
        this.state.live = false
        this.emit()
        // Refetch and reconnect (S4.5): the durable lane is the
        // database's job. A stream that closed for good is retried a
        // bounded number of times, further apart each time — a Studio
        // that went away (or a token that expired) is not hammered.
        // A panel token that expired is not asked again: no live, quietly.
        if (why === "expired") return
        if (why === "overflow") this.overflowed("scope", again)
        else this.retry("scope", again)
      },
    })
    this.state.live = true
    this.emit()
  }

  /** retry runs fn after the kind's next backoff step, LIVE_RETRIES
   * times at most between two successful opens. */
  private retry(kind: "scope" | "run" | "exp" | "dev", fn: () => Promise<void>) {
    const n = this.retries[kind]
    if (n >= LIVE_RETRIES) return
    this.retries[kind] = n + 1
    this.after(Math.min(5_000 * 2 ** n, 60_000), () => void fn().catch(quiet))
  }

  /** overflowed reopens a lane the server's overflow frame closed, on
   * the overflow backoff (OVERFLOW_MIN_MS, doubling while overflows
   * follow each other within OVERFLOW_CALM_MS, a minute at most). */
  private overflowed(kind: "scope" | "run" | "exp" | "dev", fn: () => Promise<void>) {
    const o = this.overflows[kind]
    const now = Date.now()
    o.n = o.at && now - o.at < OVERFLOW_CALM_MS ? o.n + 1 : 0
    o.at = now
    this.after(Math.min(OVERFLOW_MIN_MS * 2 ** Math.min(o.n, 6), 60_000), () => void fn().catch(quiet))
  }

  private after(ms: number, fn: () => void): ReturnType<typeof setTimeout> {
    const t = setTimeout(() => {
      this.timers.delete(t)
      if (!this.disposed) fn()
    }, ms)
    this.timers.add(t)
    return t
  }

  private cancel(t: ReturnType<typeof setTimeout> | null) {
    if (!t) return
    clearTimeout(t)
    this.timers.delete(t)
  }

  /** The dock is open (the element says so on every draw). */
  private watching = false
  private devTimer: ReturnType<typeof setTimeout> | null = null
  private staleTimer: ReturnType<typeof setTimeout> | null = null

  /** watch tells the model whether anyone is looking: the dev list is
   * read again only while the dock is open (and the page visible). */
  watch(open: boolean) {
    if (open === this.watching) return
    this.watching = open
    this.armDev()
  }

  /** armDev schedules the dev list's next read (DEV_POLL_MS): no
   * public id, so no stream covers it. A hidden page reads nothing; it
   * looks again a period later. */
  private armDev() {
    this.cancel(this.devTimer)
    this.devTimer = null
    if (!this.watching || this.publicId || this.disposed || !this.state.meta || this.state.tooNew) return
    // The agent stream keeps its agent's rows current; the slower read
    // finds the other agents' runs.
    this.devTimer = this.after(this.devSub ? DEV_DISCOVERY_MS : DEV_POLL_MS, () => {
      this.devTimer = null
      if (typeof document !== "undefined" && document.visibilityState === "hidden") {
        this.armDev()
        return
      }
      void this.refresh()
        .catch(quiet)
        .then(() => this.armDev())
    })
  }

  /** armStale watches the rows that read running: a run whose app died
   * never sends another frame — its row reads interrupted only when it
   * is read again (derivation, S4.3; meta.interrupted_after_ms). One
   * timer, pushed back by every change heard: the list is read again
   * once nothing has been heard for that long while something reads
   * running. */
  private armStale() {
    this.cancel(this.staleTimer)
    this.staleTimer = null
    if (this.disposed) return
    const rows = [...this.state.turns, ...[...this.state.experiments.values()].flat()]
    if (!rows.some((r) => r.status === "running")) return
    const ia = this.state.meta?.interrupted_after_ms
    const ms = (typeof ia === "number" && ia > 0 ? Math.min(ia, 10 * 60_000) : 30_000) + 1_000
    this.staleTimer = this.after(ms, () => {
      this.staleTimer = null
      void this.refresh().catch(quiet)
    })
  }

  /** capDev bounds the dev list: the newest DEV_LIMIT turns, and the
   * newest DEV_LIMIT experiment rows — frames add rows the replacing
   * read may be minutes away from. */
  private capDev() {
    const s = this.state
    s.turns = s.turns.slice(0, DEV_LIMIT)
    const rows = [...s.experiments.values()].flat()
    if (rows.length <= DEV_LIMIT) return
    const keep = new Set(
      rows
        .sort((a, b) => Date.parse(b.started) - Date.parse(a.started))
        .slice(0, DEV_LIMIT)
        .map((r) => r.id)
    )
    const next = new Map<string, RunRow[]>()
    for (const [key, list] of s.experiments) {
      const kept = list.filter((r) => keep.has(r.id))
      if (kept.length) next.set(key, kept)
    }
    s.experiments = next
  }

  /** subscribeDev opens the fallback's live lane (no public id, plan
   * C3.4): run frames of the agent of the newest listed run — the
   * broadest selector S4.5 has (exactly one of run, session, public_id,
   * agent; none for everything) — through a grant like every stream.
   * Once open it stays on that agent until it ends. A refused grant
   * (403) and a panel token (never granted an agent's stream, so never
   * asked for one) leave the dev list on its poll, said as such; a
   * stream that closed is retried on the bounded backoff, the poll
   * covering the gap. */
  private subscribeDev(seq: number) {
    const s = this.state
    if (this.publicId || this.devSub || s.devRefused || this.devBackoff || this.disposed || !s.meta || s.tooNew) return
    if (tokenScope(this.ep.token) !== "") {
      s.devRefused = true
      return
    }
    const agent = s.turns.find((r) => r.agent)?.agent ?? ""
    if (!agent) return
    const ended = () => {
      this.devSub = undefined
      s.live = false
      s.devAgent = ""
      this.armDev()
      this.emit()
    }
    const again = async () => {
      if (seq !== this.loadSeq || this.disposed) return
      await this.refresh()
    }
    this.devSub = openPanelLive(this.ep, {
      selector: { agent },
      kinds: ["run"],
      onOpen: (reopened) => {
        this.retries.dev = 0
        if (reopened) void this.refresh().catch(quiet)
      },
      onRun: (f) => this.onRunFrame(f),
      onRefused: () => {
        s.devRefused = true
        ended()
      },
      onOverflow: (why) => {
        ended()
        if (why === "expired") return
        // Either backoff: the poll's refreshes do not reopen it meanwhile.
        if (why === "overflow") {
          this.devBackoff = true
          this.overflowed("dev", async () => {
            this.devBackoff = false
            await again()
          })
        } else {
          // Bounded: once the retries ran out the flag stays.
          this.devBackoff = true
          this.retry("dev", async () => {
            this.devBackoff = false
            await again()
          })
        }
      },
    })
    s.live = true
    s.devAgent = agent
    this.armDev()
    this.emit()
  }

  private clearTimers() {
    for (const t of this.timers) clearTimeout(t)
    this.timers.clear()
    this.devTimer = this.staleTimer = this.liveTimer = this.pinTimer = null
  }

  /** refresh reloads the header and the turn list. */
  async refresh() {
    const seq = this.loadSeq
    const during: RunRow[] = []
    this.collectors.add(during)
    try {
      if (this.publicId) {
        // The header's row; without it the list still loads.
        const sessions = await fetchSessions(this.ep, { public_id: this.publicId }).catch(() => null)
        if (seq !== this.loadSeq || this.disposed) return
        if (sessions && Array.isArray(sessions.sessions)) {
          const want = this.narrowing.session
          this.state.session =
            (want ? sessions.sessions.find((x) => x.id === want) : sessions.sessions[0]) ?? null
        }
      }
      const page = await fetchRuns(
        this.ep,
        this.publicId ? { public_id: this.publicId, limit: String(TURNS_LIMIT) } : { limit: String(DEV_LIMIT) }
      )
      if (seq !== this.loadSeq || this.disposed) return
      const listed: (RunRow | null)[] = Array.isArray(page.runs) ? page.runs : []
      const runs = listed.filter((r): r is RunRow => !!r && typeof r.id === "string")
      const { turns, experiments } = partitionRuns(runs)
      const want = this.narrowing.session
      const s = this.state
      s.sessionUnrecorded = !!want && turns.length > 0 && !turns.some((r) => r.session_id)
      const kept = s.turns
      const keptX = s.experiments
      s.turns = turns.filter((r) => this.inSession(r))
      s.experiments = experiments
      if (s.paged && page.next_before != null) {
        // The older pages stay (D4): the rows past this page's cursor
        // are re-merged, and the cursor is still the oldest page's.
        const edge = Date.parse(page.next_before)
        const older = (r: RunRow) => !(Date.parse(r.started) > edge)
        for (const r of kept) if (older(r)) this.upsertRun(r, true)
        for (const r of [...keptX.values()].flat()) if (older(r)) this.upsertRun(r, true)
      } else {
        s.paged = false
        this.setCursor(page.next_before != null ? { before: page.next_before, id: page.next_before_id ?? "" } : null)
      }
      s.turnsCapped = this.cursor != null
      for (const r of during) this.upsertRun(r)
      this.state.listKey = listKey(this.publicId, this.narrowing.session)
    } catch {
      // A refresh that fails leaves what was there; run frames will
      // retry the shape naturally.
      return
    } finally {
      this.collectors.delete(during)
    }
    this.armStale()
    // The fallback's stream: opened once the list names an agent; the
    // poll stops while it covers the list.
    if (!this.publicId) {
      this.subscribeDev(seq)
      this.armDev()
    }
    // The open turn's row may have ended without a frame saying so (a
    // run that read interrupted only now): its tail is done.
    const open = this.state.turn
    if (open && !open.done && !open.loading) {
      const status = this.rowOf(open.id)?.status
      if (status && status !== "running") void this.settleTurn(open).catch(quiet)
    }
    const pin = this.takePin()
    if (pin) {
      await this.select(pin)
    } else if (!this.state.selected) {
      const running = this.state.turns.find((r) => r.status === "running")
      // The turn remembered from the last visit (D1), when it is listed.
      const kept = this.state.turns.find((r) => r.id === this.prefer)
      if (this.state.turns.length) this.prefer = ""
      // A running turn outranks the remembered one.
      const target = running ?? kept ?? this.state.turns.at(0)
      if (target) await this.select(target.id)
      else this.emit()
    } else {
      this.emit()
    }
  }

  /** inSession keeps a row under the scope's session: every row when
   * the scope names none, and a row that carries no session id (an
   * experiment, a run outside a thread) is not judged. */
  private inSession(r: RunRow): boolean {
    const want = this.narrowing.session
    return !want || !r.session_id || r.session_id === want
  }

  /** takePin is the scope's run pin: the id to select now — once per
   * run, when it is among the listed ones and the user has not clicked
   * a turn since the last pin — or "". A run that is not listed is
   * reported (pinMissing) instead of dropped, but only on a second
   * look: the first miss reads the list again PIN_RECHECK_MS later (a
   * live frame counts as a look too), so a header that lands before
   * its run row says nothing false. */
  private takePin(): string {
    const run = this.narrowing.run
    if (!run) {
      this.state.pinMissing = ""
      return ""
    }
    const hit = this.rowOf(run)
    this.pinChecks++
    if (!hit) {
      this.state.pinMissing = this.pinChecks >= 2 ? run : ""
      if (this.pinChecks === 1 && !this.pinTimer)
        this.pinTimer = this.after(PIN_RECHECK_MS, () => {
          this.pinTimer = null
          void this.refresh().catch(quiet)
        })
      return ""
    }
    this.state.pinMissing = ""
    if (this.pinApplied === run || this.userSelected) return ""
    this.pinApplied = run
    return run
  }

  /** onRunFrame upserts a turn row from the live lane and keeps the
   * open turn's final state honest: when a run finishes, its stored
   * records reload (the finished words are the transcript's, not the
   * deltas'). */
  private onRunFrame(f: LiveRun) {
    const run = f.run
    // The dev list's agent stream carries every conversation's runs.
    if (this.publicId && run.public_id && run.public_id !== this.publicId) return
    // A subagent's run inherits the public id, but it is not a turn:
    // the list is top-level runs (runs?public_id= reads the same way)
    // and the child hangs off its parent's call.
    if (run.parent_run_id) return
    // Another session's run is not a turn of the narrowed list.
    if (!this.inSession(run)) return
    for (const c of this.collectors) c.push(run)
    this.upsertRun(run)
    if (!this.publicId) this.capDev()
    this.armStale()
    // The scope's run, heard as it starts: the pin takes it.
    const pin = this.takePin()
    if (pin) {
      void this.select(pin).catch(quiet)
      return
    }
    if (!this.state.selected && this.state.turns.length) {
      const running = this.state.turns.find((r) => r.status === "running")
      void this.select((running ?? this.state.turns[0]).id).catch(quiet)
      return
    }
    const view = this.state.turn
    if (view && view.id === run.id) {
      if (run.status !== "running") void this.settleTurn(view).catch(quiet)
      else if (view.done && !this.runSub && !view.loading) {
        // A row that read ended and runs again (a stale read, a
        // heartbeat after an interruption): its tail is followed.
        view.done = false
        void this.resumeTurn(view).catch(quiet)
      }
    }
    this.emit()
  }

  /** upsertRun merges one run row by id: the live lane forwards every
   * change of a run and never dedupes ("a run row may legitimately
   * change", studio/live.go's liveDedupKey), so a later frame replaces
   * the row instead of appending a duplicate — the Studio UI
   * invalidates its runs query on these frames; the panel merges in
   * place. Drop any existing row (turn or experiment) with the id,
   * then insert per partitionRuns's rule: prepend to turns with the
   * newest-first sort kept, or append to its experiments bucket. */
  private upsertRun(run: RunRow, keep = false) {
    // keep: an older row re-merged under a fresher one — never over it.
    if (keep && this.rowOf(run.id)) return
    const turns = this.state.turns.filter((r) => r.id !== run.id)
    const experiments = new Map<string, RunRow[]>()
    for (const [key, list] of this.state.experiments) {
      const kept = list.filter((r) => r.id !== run.id)
      if (kept.length) experiments.set(key, kept)
    }
    const part = partitionRuns([run, ...turns])
    this.state.turns = part.turns
    for (const [key, list] of part.experiments) {
      experiments.set(key, [...(experiments.get(key) ?? []), ...list])
    }
    this.state.experiments = experiments
  }

  /** walkEvents follows next_after until it reads null: done only
   * means the run is terminal — a terminal run's page can still be
   * full (events-ok-paged.golden.json pins done:true with next_after
   * set), and breaking on done lost every later event of a long
   * finished turn. The page cap bounds the walk and is reported; a
   * cursor that does not advance ends it. */
  private async walkEvents(id: string): Promise<EventWalk> {
    const events: PosEvent[] = []
    const gaps: number[] = []
    let after = 0
    for (let page = 0; page < MAX_EVENT_PAGES; page++) {
      const p = await fetchEvents(this.ep, id, after)
      if (Array.isArray(p.events)) for (const e of p.events) events.push(e)
      if (Array.isArray(p.gaps)) for (const g of p.gaps) gaps.push(g)
      if (typeof p.next_after !== "number" || p.next_after <= after)
        return { events, gaps, capped: false }
      after = p.next_after
    }
    return { events, gaps, capped: true }
  }

  /** select loads one turn: the doc (subagent children), every events
   * page, the transcript and the spans, then follows its live tail
   * while it runs. byUser: a click in the turn list (the scope's run
   * pin then leaves the selection alone). */
  async select(id: string, byUser = false) {
    if (byUser) this.userSelected = true
    this.runSub?.close()
    this.runSub = undefined
    this.retries.run = 0
    // The step being read is the previous turn's.
    if (this.state.selected !== id) this.state.selectedStep = null
    this.state.selected = id
    const view = newTurnView(id)
    this.state.turn = view
    this.emit()
    await this.resumeTurn(view)
  }

  /** resumeTurn (re)loads the view's stored records and, while the run
   * is running, follows its tail. The tail is asked for BEFORE the
   * pages are read (the hook's order, hooks/use-run-events.ts); its
   * stream opens once its live grant answers (plan C5). Frames that
   * land during the walk are held and folded after it by position, and
   * the tail's open reads the pages past what was folded, so nothing
   * published between the last page and the subscription is lost until
   * the run ends. */
  private async resumeTurn(view: TurnView) {
    const followed = !view.done && this.rowOf(view.id)?.status === "running"
    if (followed) this.follow(view)
    await this.loadTurn(view)
    if (this.state.turn !== view || this.disposed || view.done) return
    const status = this.rowOf(view.id)?.status ?? view.doc?.status
    if (status === "running") {
      // A run the list did not know as running (no row yet): its tail
      // opens now, and its open reads the pages past what was folded.
      if (!followed) this.follow(view)
      return
    }
    // It ended. Followed, its end may have landed after the pages were
    // read: settling reads them once more. Not followed, they were read
    // after it ended.
    if (followed) await this.settleTurn(view)
    else view.done = true
  }

  /** loadTurn reads the run's stored records into the view, in place:
   * the fold is rebuilt from the durable events (a live tail that
   * began late, or dropped, is made whole), the subagent expanders
   * and whatever the user opened stay. A response for a view the user
   * has left is dropped. */
  private async loadTurn(view: TurnView): Promise<void> {
    const seq = this.loadSeq
    const ep = this.ep
    const id = view.id
    view.loading = true
    const [doc, walk, transcript, spans, requests] = await Promise.all([
      fetchRun(ep, id).catch(() => null),
      this.walkEvents(id).catch(() => null),
      fetchTranscript(ep, id).catch(() => null),
      fetchSpans(ep, id)
        .then((d) => (Array.isArray(d.spans) ? d.spans : null))
        .catch(() => null),
      this.readRequests(id),
    ])
    view.loading = false
    if (seq !== this.loadSeq || this.disposed || this.state.turn !== view) return
    if (view.again) {
      view.again = false
      return this.loadTurn(view)
    }
    if (doc) {
      view.doc = doc
      // The doc is the row as of now: a list loaded a moment ago may
      // still read running for a run that has since ended. The row moves
      // only ever forward: a doc read before a run frame can land after
      // it, and must not take a finished row back to running.
      if (this.rowOf(id)?.status === "running" && doc.status !== "running") {
        const { children: _children, ...row } = doc
        this.upsertRun(row)
      }
    }
    // An expanded child read while it ran is a snapshot: one whose
    // status has moved since (it finished, or went stale) is read again
    // — its words, its request line, its holes (A10, parity with the
    // Studio block). The old copy stays drawn until the new one lands.
    if (doc && Array.isArray(doc.children)) {
      for (const [cid, cv] of view.children) {
        const now = doc.children.find((c) => c.id === cid)?.status
        if (!now || now === cv.status) continue
        view.tried.delete(cid)
        if (view.expanded.has(cid)) void this.expandChild(cid, true).catch(quiet)
        else view.children.delete(cid)
      }
    }
    if (walk) {
      restart(view, walk.events)
      view.gaps = walk.gaps
      view.capped = walk.capped
    }
    if (transcript) view.transcript = transcript
    if (spans) view.spans = spans
    if (requests) view.requests = requests
    // The frames the tail delivered while the pages were read: folded
    // after them, once each. A settled view's are late — the reload is
    // the story.
    if (view.done || view.capped) view.held.length = 0
    else this.drain(view, id, () => this.state.turn === view && !view.done)
    if (view.recheck && !view.done) {
      view.recheck = false
      void this.catchUp(view, id, () => this.state.turn === view && !view.done).catch(quiet)
    }
    this.dress(view)
    this.emit()
  }

  /** readRequests reads the turn's request record when the server has
   * the route (the requests capability). A read-scoped token never
   * asks: it may not read system prompts (the route would refuse it),
   * so the hole is known without a request. */
  private async readRequests(id: string): Promise<PanelRequests | null> {
    if (!this.state.meta?.capabilities.includes("requests")) return null
    if (tokenScope(this.ep.token) === "read") return { badge: "hidden", reason: HOLES.hidden.reason, fix: HOLES.hidden.fix, steps: new Map() }
    try {
      const doc = await fetchRequests(this.ep, id)
      // A stripped row's words are the tools route's (the run page's).
      const stripped = doc.requests.some((r) => r.content === "stripped") ? await fetchStrippedNote(this.ep, id) : undefined
      return {
        badge: doc.badge,
        reason: doc.reason,
        fix: doc.fix,
        truncated: doc.truncated,
        steps: byStep(doc.requests),
        stripped,
      }
    } catch (err) {
      // The panel's error path: words in the view, nothing thrown into
      // the page.
      return { steps: new Map(), error: err instanceof Error ? err.message : String(err) }
    }
  }

  /** drain folds a lane's held frames in arrival order; one past a
   * position nobody carried sends the lane back to the pages (it and
   * the frames behind it wait for that read). */
  private drain(lane: Lane, id: string, current: () => boolean) {
    const held = lane.held.splice(0)
    for (let i = 0; i < held.length; i++) {
      if (foldLive(lane, held[i]) !== "gap") continue
      lane.held.push(...held.slice(i))
      void this.catchUp(lane, id, current).catch(quiet)
      return
    }
  }

  /** liveInto takes one live frame for a lane: held while its pages are
   * being read, folded in order otherwise, and a frame past a position
   * the stream never carried sends the lane back to the pages. */
  private liveInto(lane: Lane, id: string, rec: LiveRecord, current: () => boolean) {
    if (lane.loading || lane.reading) {
      lane.held.push(rec)
      return
    }
    const r = foldLive(lane, rec)
    if (r === "gap") {
      lane.held.push(rec)
      void this.catchUp(lane, id, current).catch(quiet)
    } else if (r === "folded") this.emit(true)
  }

  /** catchUp reads the stored pages past the lane's last position into
   * its fold (in place: the deltas streamed so far stay), then folds
   * what was held meanwhile — past any hole that is still there: the
   * pages are read, so it is a lost batch (the page's gaps). One read
   * at a time; asked again meanwhile, it reads once more. Bounded by
   * the walk's page cap. */
  private async catchUp(lane: Lane, id: string, current: () => boolean): Promise<void> {
    if (lane.loading || lane.reading) {
      lane.recheck = true
      return
    }
    lane.reading = true
    // Read through a function: another call sets these flags while this
    // one awaits (control-flow narrowing would read the stale literal).
    const asked = (): boolean => lane.recheck
    const reloading = (): boolean => lane.loading
    try {
      do {
        lane.recheck = false
        let after = lane.pos + 1
        for (let n = 0; n < MAX_EVENT_PAGES; n++) {
          const p = await fetchEvents(this.ep, id, after)
          if (!current()) return
          if (Array.isArray(p.events)) {
            for (const e of p.events as (PosEvent | null)[]) {
              if (!e || typeof e.pos !== "number" || lane.seen.has(e.pos)) continue
              lane.seen.add(e.pos)
              lane.events.push(e)
              feedEvent(lane.feed, e.event, e.pos, e.attrs)
              if (e.pos > lane.pos) lane.pos = e.pos
              lane.stale = true
            }
          }
          if (typeof p.next_after !== "number" || p.next_after <= after) break
          after = p.next_after
        }
      } while (asked())
    } catch {
      // unreadable now: what was held folds anyway, below
    } finally {
      lane.reading = false
    }
    if (!current() || reloading()) return // a reload drains them itself
    for (const rec of lane.held.splice(0)) foldLive(lane, rec, true)
    this.emit(true)
  }

  /** dress takes the view's fold and puts the transcript's words and
   * the subagent links on it. */
  private dress(view: TurnView) {
    view.stale = false
    const status = this.rowOf(view.id)?.status ?? view.doc?.status
    view.folded = overlay(viewOf(view.feed), view.transcript, view.done || (!!status && status !== "running"))
    if (view.doc && Array.isArray(view.doc.children)) {
      try {
        linkView(view.folded, view.doc.children)
      } catch {
        // children that do not read as rows: no expanders
      }
    }
  }

  /** follow opens the live tail: deltas stream the text as it is
   * generated, tool calls show running… until they finish (§2). */
  private follow(view: TurnView) {
    const id = view.id
    this.runSub?.close()
    this.runSub = openPanelLive(this.ep, {
      selector: { run: id },
      kinds: ["event", "delta", "run"],
      onOpen: () => {
        this.retries.run = 0
        // Subscribed now: what was published before the server
        // subscribed and after the pages were read is in neither — the
        // pages past the last position are read (after a load in
        // flight, which then reads them).
        if (this.state.turn === view && !view.done && !view.capped)
          void this.catchUp(view, id, () => this.state.turn === view && !view.done).catch(quiet)
      },
      onRecord: (rec: LiveRecord) => {
        if (this.state.turn !== view || view.id !== rec.run_id || view.done) return
        // A story cut at the page cap is not extended: it says so, and
        // Studio has the rest.
        if (view.capped) return
        // The pages and the tail overlap (a reconnect backfills the
        // stored events from position 0): one fold per position.
        this.liveInto(view, id, rec, () => this.state.turn === view && !view.done)
      },
      onRun: (f) => {
        if (f.run.id !== id || this.state.turn !== view) return
        if (this.rowOf(id)) this.upsertRun(f.run)
        if (f.run.status !== "running") void this.settleTurn(view).catch(quiet)
        this.emit()
      },
      onOverflow: (why) => {
        this.runSub = undefined
        if (this.state.turn !== view || view.done) return
        // The tail is gone, the run is not: reload what is stored and
        // follow again — on the overflow backoff after the server's own
        // overflow frame, on the bounded backoff when the stream closed
        // for good.
        // A panel token that expired is not asked again: no live, quietly.
        if (why === "expired") return
        if (why === "overflow")
          this.overflowed("run", async () => {
            if (this.state.turn === view && !view.done) await this.resumeTurn(view)
          })
        else this.retry("run", () => this.resumeTurn(view))
      },
    })
  }

  /** settleTurn is the open turn's end: the tail closes, late deltas
   * are dropped, and the stored records reload — every event the tail
   * may have missed between the page fetch and the subscription, the
   * final words (the transcript's, not the deltas'), the spans and
   * the row. A live tail and a reload agree. */
  private async settleTurn(view: TurnView) {
    if (this.state.turn !== view) return
    view.done = true
    this.runSub?.close()
    this.runSub = undefined
    if (view.loading) {
      view.again = true
      return
    }
    await this.loadTurn(view)
  }

  /** expandChild loads a subagent child's own turn view (lazy, §2:
   * a delegating tool call expands into the child run). */
  async expandChild(childId: string, refresh = false) {
    const view = this.state.turn
    if (!view || (!refresh && view.children.has(childId)) || view.tried.has(childId)) return
    view.tried.add(childId)
    const seq = this.loadSeq
    let walk: EventWalk
    try {
      walk = await this.walkEvents(childId)
    } catch {
      // The child's history is unreachable: leave the expander, say so.
      if (this.state.turn === view) {
        view.unreachable.add(childId)
        this.emit()
      }
      return
    }
    const feed = newFold()
    foldInto(feed, new Set(), walk.events)
    const [transcript, doc, requests] = await Promise.all([
      fetchTranscript(this.ep, childId).catch(() => null),
      fetchRun(this.ep, childId).catch(() => null),
      this.readRequests(childId),
    ])
    if (seq !== this.loadSeq || this.disposed || this.state.turn !== view) return
    const status = view.doc?.children.find((c) => c.id === childId)?.status ?? "running"
    const folded = overlay(viewOf(feed), transcript, status !== "running")
    // The grandchildren are linked (one level inline: the panel badges
    // them and hands off to Studio).
    if (doc && Array.isArray(doc.children)) {
      try {
        linkView(folded, doc.children)
      } catch {
        // children that do not read as rows: no badges
      }
    }
    view.children.set(childId, {
      events: walk.events,
      feed,
      folded,
      transcript,
      capped: walk.capped,
      doc,
      requests,
      status,
    })
    this.emit()
  }

  /** adoptRun makes a run the list does not show selectable (the host
   * API's select, plan C4): its document is read by id (the token
   * scopes it as every read), and a top-level run of the conversation
   * followed — any run's, on the dev list — joins the list: "ok".
   * "foreign" when Studio says it is another conversation's (or a
   * subagent's, or refuses or misses it), "unreachable" when Studio did
   * not answer. A row without a session id is not judged by a session
   * narrowing (a playground run's included). */
  async adoptRun(id: string): Promise<"ok" | "foreign" | "unreachable"> {
    if (this.rowOf(id)) return "ok"
    let raw: unknown
    try {
      raw = await fetchRun(this.ep, id)
    } catch (err) {
      // A refusal or a miss is an answer (not this conversation's); a
      // transport error or a 5xx is not.
      const status = err instanceof PanelApiError ? err.status : 0
      return status >= 400 && status < 500 ? "foreign" : "unreachable"
    }
    if (this.disposed || !raw || typeof raw !== "object") return "unreachable"
    const doc = raw as RunDoc
    if (doc.id !== id || doc.parent_run_id) return "foreign"
    if (this.publicId && doc.public_id !== this.publicId) return "foreign"
    if (!this.inSession(doc)) return "foreign"
    // The row, not the document: children and holes stay the view's.
    const { children: _c, holes: _h, compactions: _x, ...row } = doc
    this.upsertRun(row)
    this.emit()
    return "ok"
  }

  /** pendingCalls reads the calls a run parked on — its run_finish's
   * pending list (the calls Approve/Deny and POST
   * /api/runs/{id}/approvals name by call id): the open turn's fold
   * when it holds the finish, else the run's stored events. [] when
   * they cannot be read. */
  async pendingCalls(id: string): Promise<ToolCallPart[]> {
    const open = this.state.turn
    if (open && open.id === id && open.folded.finished && open.folded.pending.length) return [...open.folded.pending]
    try {
      const walk = await this.walkEvents(id)
      for (let i = walk.events.length - 1; i >= 0; i--) {
        const ev = walk.events[i]?.event as { type?: string; pending?: unknown } | undefined
        if (ev?.type !== "run_finish") continue
        return Array.isArray(ev.pending)
          ? (ev.pending as (ToolCallPart | null)[]).filter(
              (c): c is ToolCallPart => !!c && typeof c.id === "string" && c.id !== ""
            )
          : []
      }
    } catch {
      // unreadable: nothing to report
    }
    return []
  }

  /** rowOf finds a run's row: a turn, or an experiment nested under
   * one (both are selectable). */
  rowOf(id: string): RunRow | undefined {
    const turn = this.state.turns.find((r) => r.id === id)
    if (turn) return turn
    for (const list of this.state.experiments.values()) {
      const x = list.find((r) => r.id === id)
      if (x) return x
    }
    return undefined
  }

  // ── The playground (WEFT-PLAYGROUND §3, WEFT-DEVTOOLS §8.2) ──────

  /** openExperiment opens the drawer pre-filled from the run's
   * registered config — instructions, tools and models come from the
   * runtime's manifest, never guessed from the trace. step 0 re-runs
   * the whole turn; the Continue-from button passes the read step.
   * The runtimes are read each time: a restarted app registers under
   * a new runtime id, and a remembered list would post to the dead
   * one. */
  async openExperiment(runId: string, step = 0) {
    const row = this.rowOf(runId)
    if (!row) return
    // Every await below re-asks whether the conversation is still the
    // one the verb was for: an SPA that switched users meanwhile must
    // not get the previous one's drawer, result or stream.
    const seq = this.loadSeq
    let runtimes: RuntimeView[]
    try {
      const doc = await fetchRuntimes(this.ep)
      runtimes = Array.isArray(doc.runtimes) ? doc.runtimes : []
    } catch (err) {
      if (!this.disposed && seq === this.loadSeq) this.setExperimentError(messageOf(err), runId)
      return
    }
    if (this.disposed || seq !== this.loadSeq) return
    this.state.runtimes = runtimes
    const rt = pickRuntime(runtimes, row.agent)
    const agent = rt?.agents.find((a) => a.name === row.agent)
    if (!rt || !agent) {
      // The verb cannot run; say why instead of a button that does
      // nothing.
      this.setExperimentError(
        `no connected runtime registers the agent "${row.agent}" — experiments run in your app (weft/runtime)`,
        runId
      )
      return
    }
    const tools: Record<string, boolean> = {}
    for (const t of agent.tools) tools[t.name] = true
    // The rule that is parking this runtime's runs, whoever set it.
    this.state.breakpoints = Array.isArray(rt.breakpoints) ? [...rt.breakpoints] : []
    const turn = this.state.turn
    this.state.drawer = {
      runId,
      agent: row.agent,
      edits: [],
      step,
      instructions: agent.instructions ?? "",
      registeredInstructions: agent.instructions ?? "",
      tools,
      model: "",
      thinking: "",
      input: step === 0 && turn && turn.id === runId ? turnPromptOf(turn.transcript) : "",
      engine: "live",
      sideEffects: "",
      thread: "ephemeral",
      runtimeId: rt.id,
    }
    this.emit()
  }

  /** setDraft patches the drawer's editable fields. still leaves the
   * dock as it is — a text field being typed in already shows its own
   * value, and redrawing it would take the caret. */
  setDraft(patch: Partial<ExperimentDraft>, still = false) {
    if (!this.state.drawer) return
    this.state.drawer = { ...this.state.drawer, ...patch }
    if (!still) this.emit()
  }

  /** closeExperiment drops the drawer (the result stays until the next
   * run replaces or discards it). */
  closeExperiment() {
    this.state.drawer = null
    this.emit()
  }

  /** rerun is §3's ↻: the whole turn again with the drawer's current
   * edits — or, with no drawer open on this turn, as registered. */
  async rerun(runId: string) {
    const d = this.state.drawer
    if (!d || d.runId !== runId) {
      await this.openExperiment(runId, 0)
      if (this.state.drawer?.runId !== runId) return
    } else if (d.step !== 0) {
      const turn = this.state.turn
      this.state.drawer = {
        ...d,
        step: 0,
        input: d.input || (turn && turn.id === runId ? turnPromptOf(turn.transcript) : ""),
      }
    }
    await this.runExperiment()
  }

  /** runExperiment posts §5.1's command and follows it: the lifecycle
   * row until the run id arrives, then the live lane for the run. One
   * post at a time — a second click while the first is in flight would
   * start (and bill) a second run. */
  async runExperiment() {
    const draft = this.state.drawer
    if (!draft || this.posting) return
    const problem = draftProblem(draft)
    if (problem) {
      this.setExperimentError(problem, draft.runId)
      return
    }
    this.posting = true
    const seq = this.loadSeq
    let out: { command_id: string; state: string }
    try {
      out = await postPlaygroundRun(this.ep, buildRunBody(draft, this.publicId))
    } catch (err) {
      if (!this.disposed && seq === this.loadSeq) this.setExperimentError(messageOf(err), draft.runId)
      return
    } finally {
      this.posting = false
    }
    // Posted for a conversation the page has left: the run is the
    // runtime's now, but not this view's to follow.
    if (this.disposed || seq !== this.loadSeq) return
    const forked = this.state.experiments.get(draft.runId)?.length ?? 0
    const turn = this.state.turn
    const source =
      turn && turn.id === draft.runId
        ? (turnWordsOf(turn.transcript) ?? foldedWords(turn.folded))
        : { text: "", calls: [] }
    this.expSub?.close()
    this.expSub = undefined
    const res = newResult(draft.runId, experimentLabel(draft.runId, forked), source)
    res.commandID = out.command_id
    res.thread = draft.thread
    res.state = "queued"
    this.state.result = res
    this.emit()
    this.trackCommand(res)
  }

  /** decide answers one parked call of the experiment's run with the
   * approval verbs (continue / skip / resolve, ADR 0007) and follows
   * the resumed run in the same result pane. The run's pending set is
   * read again from its stored events first: a call that is not in it
   * is not sent. With several calls parked the runtime holds each
   * decision until all are in, then resumes once (trackCommand records
   * the held ones). */
  async decide(callID: string, decision: "approve" | "deny" | "resolve", content?: string) {
    const res = this.state.result
    if (!res || !res.runID || !res.ready || this.deciding) return
    this.deciding = true
    try {
      let walk: EventWalk
      try {
        walk = await this.walkEvents(res.runID)
      } catch (err) {
        res.error = `the parked run could not be read again — nothing was sent (${messageOf(err)})`
        this.emit()
        return
      }
      if (this.left(res)) return
      const feed = newFold()
      foldInto(feed, new Set(), walk.events)
      const pending = viewOf(feed).pending
      if (!pending.some((c) => c.id === callID)) {
        res.folded.pending = pending
        res.error = `call ${callID} is not pending on ${res.runID} — nothing was sent`
        this.emit()
        return
      }
      let out: { command_id: string; state: string }
      try {
        out = await postApproval(this.ep, res.runID, { call_id: callID, decision, content })
      } catch (err) {
        // Refused (a read-only token is a 403, a call that is not
        // pending a 400 naming the ones that are, a runtime that left
        // a 503): say what Studio said, and show the pending set as it
        // stands now.
        res.error = messageOf(err)
        this.emit()
        const again = await this.walkEvents(res.runID).catch(() => null)
        if (again && !this.left(res)) {
          const now = newFold()
          foldInto(now, new Set(), again.events)
          res.folded.pending = viewOf(now).pending
          this.emit()
        }
        return
      }
      if (this.left(res)) return
      // The decision is a command of its own. The pane keeps the
      // parked run until the runtime names the resumed one (a rejected
      // decision leaves it as it was); the label and the diff base
      // stay — it is the same experiment continuing.
      const next: ExperimentResult = {
        ...res,
        commandID: out.command_id,
        state: "queued",
        error: null,
        ready: false,
        words: null,
        deciding: { callID, decision },
        // Its own lane bookkeeping: nothing held or in flight is the
        // new command's.
        held: [],
        reading: false,
        recheck: false,
        loading: false,
      }
      this.state.result = next
      this.emit()
      this.trackCommand(next)
    } finally {
      this.deciding = false
    }
  }

  /** setCompare points the 2-way diff at another run of the same
   * source (PQ3: the panel stays 2-way): "" is the source turn. */
  async setCompare(runID: string) {
    const res = this.state.result
    if (!res) return
    res.compareWith = runID
    this.emit()
    if (!runID || this.compareWords.has(runID)) return
    // The other side's words: its transcript, read the way every side
    // of the diff is (turnWordsOf); its fold when content is stripped.
    const transcript = await fetchTranscript(this.ep, runID).catch(() => null)
    let words = turnWordsOf(transcript)
    if (!words) {
      const feed = newFold()
      try {
        foldInto(feed, new Set(), (await this.walkEvents(runID)).events)
      } catch {
        // unreachable: an empty side
      }
      words = foldedWords(viewOf(feed))
    }
    if (this.left(res)) return
    this.compareWords.set(runID, words)
    this.emit()
  }

  /** compareWords holds the loaded words of each compare target (P3's
   * 2-way diff needs both sides). */
  compareWords = new Map<string, TurnWords>()

  /** setBreakpoints is the rung-3 verb (§8.3): the tools every run
   * this runtime starts parks on from then on. Rendered only when
   * meta reports the breakpoints capability. */
  async setBreakpoints(tools: string[]) {
    const drawer = this.state.drawer
    if (!drawer) return
    const seq = this.loadSeq
    let stored: { tools?: string[] } | null
    try {
      stored = await putBreakpoints(this.ep, drawer.runtimeId, tools)
    } catch (err) {
      // Nothing was stored (a disconnected runtime is a 503): the
      // redraw puts the checkbox back on the set that stands.
      if (!this.disposed && seq === this.loadSeq) this.setExperimentError(messageOf(err), drawer.runId)
      return
    }
    if (this.disposed || seq !== this.loadSeq) return
    this.state.breakpoints = Array.isArray(stored?.tools) ? stored.tools : tools
    this.emit()
  }

  /** steer delivers one user message into the result's run mid-flight
   * (§8.4) — weft.Steering on the ephemeral run the runtime holds. */
  async steer(message: string) {
    const res = this.state.result
    if (!res || !res.runID || !message) return
    try {
      await postSteer(this.ep, res.runID, message)
    } catch (err) {
      res.error = messageOf(err)
    }
    if (!this.left(res)) this.emit()
  }

  /** discardResult clears the result pane (the run itself stays in the
   * turn list, nested under its source turn). */
  discardResult() {
    this.expSub?.close()
    this.expSub = undefined
    this.state.result = null
    this.emit()
  }

  /** setExperimentError shows a refused verb where its result would
   * have been: on the open result, or on an empty one under the turn
   * the verb was for. */
  private setExperimentError(message: string, sourceRunID?: string) {
    const res = this.state.result
    if (res && (!sourceRunID || res.sourceRunID === sourceRunID)) {
      res.error = message
    } else {
      this.expSub?.close()
      this.expSub = undefined
      const blank = newResult(sourceRunID ?? this.state.selected, "—", { text: "", calls: [] })
      blank.error = message
      this.state.result = blank
    }
    this.emit()
  }

  /** trackCommand polls the lifecycle row (§10.5) until the run id
   * arrives — then the live lane takes over — and until the terminal
   * state, when the run's stored records load for the final words and
   * the diff. The id the row names can change: the accepted ack names
   * the first run, the finished ack the last of a substitute chain
   * (each leg resumes under a fresh id) — the pane follows it. A row
   * that cannot be read (the command is unknown to a restarted Studio,
   * the token expired, Studio is gone) ends the poll instead of asking
   * forever. */
  private trackCommand(res: ExperimentResult) {
    let fails = 0
    const tick = async () => {
      if (this.left(res)) return // replaced or discarded
      let st: CommandStatus
      try {
        st = await fetchCommand(this.ep, res.commandID)
      } catch (err) {
        if (this.left(res)) return
        const refused = err instanceof PanelApiError && [401, 403, 404, 410].includes(err.status)
        if (refused || ++fails >= POLL_FAILS) {
          res.state = "lost"
          res.error = refused
            ? messageOf(err)
            : "Studio stopped answering — the command's state is unknown"
          this.emit()
          return
        }
        this.after(POLL_MS, () => void tick().catch(quiet))
        return
      }
      if (this.left(res)) return
      fails = 0
      res.state = st.state
      res.error = st.error ?? null
      if (st.run_id && st.run_id !== res.runID) this.followExperiment(res, st.run_id)
      this.emit()
      if (st.state === "finished" || st.state === "rejected" || st.state === "lost") {
        // A decision that finished under the parked run's own id is
        // one the runtime holds: the run resumes once, when every
        // pending call has a decision — this one is recorded, the park
        // stands, and the remaining calls are still to decide.
        if (st.state === "finished" && res.deciding && !st.error) {
          res.decided = { ...res.decided, [res.deciding.callID]: res.deciding.decision }
        }
        res.deciding = null
        if (res.runID) await this.settleExperiment(res, 0)
        return
      }
      this.after(POLL_MS, () => void tick().catch(quiet))
    }
    void tick().catch(quiet)
  }

  /** followExperiment points the pane at one run and streams it in
   * place (§3: "the result streams in place from the live lane"). */
  private followExperiment(res: ExperimentResult, runID: string) {
    const feed = newFold()
    res.runID = runID
    res.row = null
    res.events = []
    res.seen = new Set()
    res.feed = feed
    res.folded = viewOf(feed)
    // A fresh lane: a read still in flight for the previous run of the
    // chain is no longer this lane's (its own checks drop it).
    res.pos = -1
    res.held = []
    res.reading = false
    res.recheck = false
    res.loading = false
    res.stale = false
    res.ready = false
    res.words = null
    res.deciding = null // the run it was held for has been resumed
    res.decided = {}
    this.retries.exp = 0
    this.openExperimentStream(res)
  }

  private openExperimentStream(res: ExperimentResult) {
    const runID = res.runID
    const current = () => !this.left(res) && res.runID === runID && !res.ready
    this.expSub?.close()
    this.expSub = openPanelLive(this.ep, {
      selector: { run: runID },
      kinds: ["event", "delta", "run"],
      onOpen: () => {
        this.retries.exp = 0
        // The run began before its stream opened (the run id arrives
        // with the lifecycle poll): what it stored before the server
        // subscribed is read from the pages, by position.
        if (!this.left(res) && res.runID === runID && !res.ready) void this.catchUp(res, runID, current).catch(quiet)
      },
      onRecord: (rec) => {
        if (this.state.result !== res || res.runID !== rec.run_id || res.ready) return
        this.liveInto(res, runID, rec, current)
      },
      onRun: (f) => {
        if (this.state.result !== res || f.run.id !== res.runID) return
        res.row = f.run
        this.emit()
        if (f.run.status !== "running") void this.settleExperiment(res, 0).catch(quiet)
      },
      onOverflow: (why) => {
        this.expSub = undefined
        if (this.state.result !== res || res.runID !== runID || res.ready) return
        // The stream is gone, the run is not: what is stored reloads
        // and the tail reopens (on the overflow backoff after the
        // server's overflow frame, on the bounded backoff when it
        // closed for good).
        const again = async () => {
          if (this.state.result !== res || res.runID !== runID || res.ready) return
          await this.loadExperiment(res)
          if (this.left(res) || res.runID !== runID || isReady(res)) return
          if (res.state === "queued" || res.state === "accepted") this.openExperimentStream(res)
        }
        // A panel token that expired is not asked again: no live, quietly.
        if (why === "expired") return
        if (why === "overflow") this.overflowed("exp", again)
        else this.retry("exp", again)
      },
    })
  }

  /** loadExperiment rebuilds the pane's fold from the run's stored
   * events — a run can end before the stream to it opens (the
   * scripted engine answers in milliseconds), and the last run of a
   * chain is first named by the finished ack. It reports whether the
   * load still belongs to the pane, and the transcript it read. */
  private async loadExperiment(
    res: ExperimentResult
  ): Promise<{ transcript: Transcript | null } | null> {
    const runID = res.runID
    res.loading = true
    let walk: EventWalk | null, transcript: Transcript | null, row: RunRow | null
    try {
      ;[walk, transcript, row] = await Promise.all([
        this.walkEvents(runID).catch(() => null),
        fetchTranscript(this.ep, runID).catch(() => null),
        fetchRun(this.ep, runID).catch(() => null),
      ])
    } finally {
      res.loading = false
    }
    if (this.disposed || this.state.result !== res || res.runID !== runID) return null
    if (walk && walk.events.length) restart(res, walk.events)
    // Frames the stream delivered meanwhile fold after the pages.
    const current = () => !this.left(res) && res.runID === runID && !res.ready
    this.drain(res, runID, current)
    if (res.recheck) {
      res.recheck = false
      void this.catchUp(res, runID, current).catch(quiet)
    }
    if (row) res.row = row
    res.folded = overlay(viewOf(res.feed), transcript, !!res.row && res.row.status !== "running")
    res.stale = false
    this.emit()
    return { transcript }
  }

  /** settleExperiment loads the ended run's stored records: the diff
   * is taken against the transcript, never the deltas, and the
   * approval controls act on this run's own pending set. The finished
   * ack travels the runtime link while the run's last records travel
   * the OTel pipeline — when they have not landed yet, the load is
   * tried again a few times before the pane settles on what it has. */
  private async settleExperiment(res: ExperimentResult, attempt: number) {
    if (res.ready || this.state.result !== res || this.settling.has(res)) return
    this.settling.add(res)
    let loaded: { transcript: Transcript | null } | null
    try {
      loaded = await this.loadExperiment(res)
    } finally {
      this.settling.delete(res)
    }
    if (!loaded || isReady(res)) return
    // Ack before export: the runtime's finished ack travels the link,
    // the run's last records the OTel pipeline — its row (spans) and
    // its run_finish (logs, the event naming the pending calls) can
    // land after it. Settled is the row out of running and, for a run
    // that succeeded, its run_finish read; until then the stream stays
    // open and the run is read again (SETTLE_READS, 1 s apart, the
    // Studio UI's bound) — the final words, the diff and the approval
    // controls are taken from the settled run only.
    const row = res.row
    const settled = !!row && row.status !== "running" && (row.status !== "succeeded" || res.folded.finished)
    if (!settled && attempt < SETTLE_READS - 1) {
      this.after(1_000, () => void this.settleExperiment(res, attempt + 1).catch(quiet))
      return
    }
    res.ready = true
    res.words = turnWordsOf(loaded.transcript) ?? foldedWords(res.folded)
    this.expSub?.close()
    this.expSub = undefined
    this.emit()
  }

  private settling = new WeakSet<ExperimentResult>()

  /** left: the pane no longer shows this result (replaced, discarded,
   * or the panel is gone) — asked again after every await. */
  private left(res: ExperimentResult): boolean {
    return this.disposed || this.state.result !== res
  }

  /** The runs cursor of the oldest page loaded (next_before /
   * next_before_id), null when every run is listed. */
  private cursor: Cursor | null = null

  /** setCursor moves the runs cursor and the key the sentinel reads. */
  private setCursor(c: Cursor | null) {
    this.cursor = c
    this.state.olderAt = c ? `${c.before}|${c.id}` : ""
  }

  /** loadOlder reads the conversation's next older page (D4): runs?
   * with before= and before_id= (studio/api.go's cursor), merged under
   * the list; one read at a time; a failed read leaves the cursor for
   * the next try. The dev list (no public id) is not paged. */
  async loadOlder() {
    const s = this.state
    const c = this.cursor
    if (!this.publicId || !c || s.loadingOlder) return
    const seq = this.loadSeq
    s.loadingOlder = true
    this.emit()
    try {
      const page = await fetchRuns(this.ep, {
        public_id: this.publicId,
        limit: String(TURNS_LIMIT),
        before: c.before,
        ...(c.id ? { before_id: c.id } : {}),
      })
      // A refresh that landed meanwhile may have set an equal cursor (a
      // new object): the page is still the one asked for.
      if (seq !== this.loadSeq || this.disposed || !sameCursor(this.cursor, c)) return
      const runs: (RunRow | null)[] = Array.isArray(page.runs) ? page.runs : []
      for (const r of runs) if (r && typeof r.id === "string" && !r.parent_run_id && this.inSession(r)) this.upsertRun(r, true)
      this.setCursor(page.next_before != null ? { before: page.next_before, id: page.next_before_id ?? "" } : null)
      s.paged = true
      s.turnsCapped = this.cursor != null
    } catch {
      // the cursor stays: scrolling there again (or the button) retries
    } finally {
      if (seq === this.loadSeq) s.loadingOlder = false
      this.emit()
    }
  }

  /** selectStep marks the step the user is reading: the ⤢ deep link
   * carries it into Studio (Dv3, §2 "with the context carried over"). */
  selectStep(index: number) {
    if (this.state.selectedStep === index) return
    this.state.selectedStep = index
    this.emit()
  }

  /** drawPending: a draw is scheduled (the next frame, or a live draw
   * waiting out LIVE_DRAW_MS). */
  drawPending(): boolean {
    return this.raf !== 0 || this.liveTimer !== null
  }

  private raf = 0
  private lastDraw = 0
  private liveTimer: ReturnType<typeof setTimeout> | null = null
  /** emit draws the state once per frame. The live lanes' views are
   * taken here, not per frame of the stream: a burst of deltas folds
   * at the stream's pace and is dressed (transcript, subagent links)
   * once per draw. A live emit (a streamed frame) draws at most every
   * LIVE_DRAW_MS; any other emit draws on the next frame and takes a
   * waiting live draw with it. */
  private emit(live = false) {
    if (this.disposed) return
    if (live) {
      if (this.raf || this.liveTimer) return
      const wait = this.lastDraw + LIVE_DRAW_MS - Date.now()
      if (wait > 0) {
        this.liveTimer = this.after(wait, () => {
          this.liveTimer = null
          this.emit()
        })
        return
      }
    } else if (this.liveTimer) {
      this.cancel(this.liveTimer)
      this.liveTimer = null
    }
    if (this.raf) return
    this.raf = requestAnimationFrame(() => {
      this.raf = 0
      this.lastDraw = Date.now()
      const t = this.state.turn
      if (t?.stale) this.dress(t)
      const r = this.state.result
      if (r?.stale) {
        r.stale = false
        r.folded = viewOf(r.feed)
      }
      this.notify(this.state)
    })
  }

  dispose() {
    this.disposed = true
    if (this.raf) cancelAnimationFrame(this.raf)
    this.raf = 0
    this.clearTimers()
    this.scopeSub?.close()
    this.runSub?.close()
    this.expSub?.close()
    this.devSub?.close()
    this.scopeSub = this.runSub = this.expSub = this.devSub = undefined
  }
}

/** newResult is an empty result pane under one source turn. */
function newResult(sourceRunID: string, label: string, source: TurnWords): ExperimentResult {
  const feed = newFold()
  return {
    commandID: "",
    state: "rejected",
    runID: "",
    error: null,
    label,
    sourceRunID,
    source,
    compareWith: "",
    row: null,
    events: [],
    seen: new Set(),
    feed,
    folded: viewOf(feed),
    ready: false,
    words: null,
    deciding: null,
    decided: {},
    thread: "ephemeral",
    pos: -1,
    held: [],
    reading: false,
    recheck: false,
    stale: false,
    loading: false,
  }
}

/** isReady reads the flag as it is now (an await ago it was false). */
function isReady(res: ExperimentResult): boolean {
  return res.ready
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/** listKey names the conversation a turn list is of: its public id
 * and session (the scope's serialised form of the two). */
export function listKey(publicId: string, session?: string): string {
  return serializeScope({ publicId, session })
}

/** narrowingOf is a scope's narrowing fields, the empty ones dropped. */
function narrowingOf(s: Scope): Omit<Scope, "publicId"> {
  const out: Omit<Scope, "publicId"> = {}
  if (s.session) out.session = s.session
  if (s.flow) out.flow = s.flow
  if (s.run) out.run = s.run
  return out
}
