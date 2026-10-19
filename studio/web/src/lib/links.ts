// The deep-link scheme (plan G1): the one place either client builds
// a Studio URL. Studio's app spreads a builder's result into a router
// <Link {...runLink(id)} /> or navigate(runLink(id)); the devtools
// panel, which has no router, turns the same value into an absolute
// URL off its endpoint with href(base, link). One scheme, so a link
// the panel hands off lands where Studio's own link to the same thing
// lands. An ESLint rule (eslint.config.js, "one deep-link scheme")
// refuses a Studio page URL built anywhere else.
//
// Rules every builder keeps:
//   - Step is the step ORDINAL: the step's number within its run as
//     the loop counts it — step_start/step_finish's index, the n of
//     api/runs/{id}/steps/{n}. Never an event's position in the stream
//     (api/runs/{id}/events, the replay playhead t) and never a
//     transcript batch's index: a steer batch, a resumed call or a
//     tool result shifts those, never the ordinal. The run page maps
//     the ordinal to its events itself.
//   - Ids are data (a foreign SDK's ids are any string): each travels
//     as one encoded path segment or one search value, so a `/`, `?`
//     or `#` in it cannot rewrite the URL.
//   - A link carries the scope it needs — the run, session, trace or
//     experiment it names — and never a token: the panel's token and
//     Studio's dev token stay where they are, out of every href.
//
// This module is shared by both clients: no React, no router, no DOM
// globals — plain values and strings.

import type { ReplayVerb } from "./replay"
import { ordinalOf } from "./format"
import { serializeScope } from "./scope"
import type { Scope } from "./scope"

/** The run page's views: the waterfall (the run tree, the default),
 * the story (step cards), the raw JSON and the run's app logs (U11.2).
 * "trace" is the /traces page's word only (U8.2). */
export type RunView = "waterfall" | "story" | "raw" | "logs"

/** The run tree's scale (lib/waterfall.ts's Scale): wall time from the
 * run's spans, or stream order (the replay playhead's); or the call
 * graph over the same spans (lib/graph.ts, U9.2). */
export type RunScale = "time" | "order" | "graph"

/** A step card's panes a link can open (A6.2): the Request pane, or
 * its Wire tab (the attempt's provider request and response). */
export type StepPane = "request" | "wire"

/** The run page's search, as runs/$id validates it. */
export interface RunSearch {
  /** The step ordinal (see the header). */
  step?: number
  view?: Exclude<RunView, "waterfall">
  /** The step card's pane open on the step named (A6.2): its Request
   * pane, or that pane's Wire tab. Only beside a step. */
  pane?: StepPane
  raw?: "events" | "doc"
  /** The selected row's key (waterfall view; lib/waterfall.ts's
   * rowKeys): r:<run>, s<step>, s<step>:m, c:<step>:<call id>
   * (c:resume:<call id> for a resumed call), t:<span id>. */
  sel?: string
  /** A pre-U10.1 detail mode (waterfall view): read as tab=raw, never
   * written. */
  d?: "events" | "json"
  /** The drawer's open tab (waterfall view, U10.1; lib/run-tree.ts's
   * DRAWER_TABS): absent, the selected row's default. */
  tab?: "request" | "response" | "tools" | "logs" | "raw"
  /** The logs view's severity filter (U11.2): the lowest level kept;
   * absent, every line. */
  lsev?: "info" | "warn" | "error"
  /** The run tree's scale: time (spans) or order (positions, replay), or graph.
   * A pre-U8.2 ?axis=time|events is read as time|order, never written. */
  scale?: RunScale
  /** The graph scale's level (U34): absent, the loop (lib/loopgraph.ts);
   * agents, the call graph of agents and MCP servers (lib/graph.ts). */
  level?: "agents"
  /** The run tree's dim filters (§6.1, UX-A-04): errors, slow, tools,
   * comma-separated — rows no filter keeps dim, never hide. */
  dim?: string
  /** The replay playhead: a stream position. */
  t?: number
  /** The raw view's search text (G2). */
  q?: string
  /** The raw view's hidden event kinds, comma-separated (G2). */
  hide?: string
  /** The raw view's open event: its stream position (G2). */
  ev?: number
  /** The replay drawer's verb, while it is open (G2). */
  replay?: ReplayVerb
  /** The drawer's from_step (a step ordinal). */
  from?: number
  /** The run the drawer replays when it is not the page's (a child
   * row's verb: the child's id). */
  of?: string
  /** Save as test (plan F6): the page downloads the run's wefttest zip
   * on landing, then drops the key (saveTestLink). */
  save?: "test"
}

/** The replay drawer's state a link carries (G2): its verb, from_step
 * and — for a child — the run it replays. The draft's edits are not in
 * the link: a reopened drawer starts from the verb's own draft. */
export interface ReplayLinkState {
  verb: ReplayVerb
  from: number
  of?: string
}

const REPLAY_VERBS: readonly ReplayVerb[] = [
  "from_step",
  "edit_result",
  "edit_prompt",
  "rerun",
  "continue",
  "edit",
]

/** replaySearch is the drawer's state as search keys, every one present
 * (undefined clears it — a closed drawer). */
export function replaySearch(r: ReplayLinkState | null): {
  replay: ReplayVerb | undefined
  from: number | undefined
  of: string | undefined
} {
  if (!r) return { replay: undefined, from: undefined, of: undefined }
  return { replay: r.verb, from: ordinal(r.from) ?? 0, of: r.of || undefined }
}

/** replayFromSearch reads the drawer's state back from a run page's
 * search (null: no drawer, or a verb this Studio does not know). */
export function replayFromSearch(
  search: Record<string, unknown>
): ReplayLinkState | null {
  const verb = REPLAY_VERBS.find((v) => v === search.replay)
  if (!verb) return null
  const from = ordinal(search.from as number | undefined) ?? 0
  const of =
    typeof search.of === "string" && search.of
      ? search.of
      : typeof search.of === "number"
        ? String(search.of)
        : undefined
  return of ? { verb, from, of } : { verb, from }
}

/** The raw view's event kinds (lib/summarize.ts's EventKind), as a
 * link's ?hide= names them. */
export const RAW_KINDS = [
  "step",
  "tool",
  "result",
  "error",
  "reasoning",
  "delta",
] as const
export type RawKind = (typeof RAW_KINDS)[number]

/** The raw view's state a link carries (G2). */
export interface RawLinkState {
  /** The search text. */
  q?: string
  /** The hidden event kinds. */
  hide?: RawKind[]
  /** The open event's stream position. */
  ev?: number
}

export interface RunLinkOptions {
  /** The step ordinal to land on. */
  step?: number
  /** A tool call to select, by its call id. Call ids repeat across
   * steps, so the call is named with its step (`step`), or as the
   * resumed call of the run (`resumed`) — a call with neither links
   * the run alone, never a key that could name two calls. */
  call?: string
  /** The call is the run's resumed call (executed before step 0's
   * model call: an approved call of the previous run). */
  resumed?: boolean
  /** An OTel span to select, by its span id (the waterfall's time
   * scale rows). */
  span?: string
  /** A run tree row to select, by its key (lib/waterfall.ts's rowKeys:
   * the panel's Timeline hands a child run's row off with it). */
  sel?: string
  /** The drawer's tab on the selected row (U10.1). */
  tab?: RunSearch["tab"]
  view?: RunView
  /** The step's pane to open (needs `step`). */
  pane?: StepPane
  scale?: RunScale
  /** The replay playhead (a stream position). */
  t?: number
  /** The raw view's filters and open event (view "raw"). */
  raw?: RawLinkState
  /** The replay drawer open on this verb and step (G2). */
  replay?: ReplayLinkState
}

export interface RunLink {
  to: "/runs/$id"
  params: { id: string }
  search: RunSearch
}

/** The session page's views (U15.1): the timeline (turns × time with
 * the state log's marks, the default), the tree (branches and forks,
 * U15.2) and the table of turns. */
export type SessionView = "timeline" | "tree" | "table"

/** The session page's search, as sessions/$id validates it. */
export interface SessionSearch {
  /** Absent: the timeline. */
  view?: Exclude<SessionView, "timeline">
  /** The mark whose drawer is open: its thread entry id, else
   * kind:at:run (lib/timeline.ts's markKeys — stable while the
   * session grows). */
  mark?: string
  /** The timeline's lane filter (U15.2): the lanes kept bright, comma
   * separated in lib/timeline.ts's LANES order — the others' marks are
   * dimmed, never hidden. Absent: every lane. */
  lanes?: string
}

export interface SessionLink {
  to: "/sessions/$id"
  params: { id: string }
  search: SessionSearch
}

export interface TraceSearch {
  /** The selected row's key — the run tree's (lib/waterfall.ts's
   * rowKeys; lib/trace-sel.ts): r:<run>, s<step>:m a model call,
   * c:<step>:<call id>, c:resume:<call id>, the trace's root run bare and
   * another run run-qualified (s:<run>:<step>:m, c:<run>:<step>:<call
   * id>), t:<span id> for any other span. A bare c:<call id> (pre-G1)
   * still resolves, to the first match, as do the pre-U8.1-fix forms. */
  sel?: string
  /** A pre-H7 link's selection, by span id: read, never written. */
  span?: string
  /** The view: the span tree (the default, never written) or the
   * GenAI chat (G2). */
  view?: "chat"
}

export interface TraceLink {
  to: "/traces/$id"
  params: { id: string }
  search: TraceSearch
}

/** The playground's hand-off: the run, the step it continues from
 * (source.from_step, see below) and the overrides, every one optional. */
export interface PlaygroundHandoff {
  run?: string
  /** The step the experiment continues from: source.from_step, which
   * IS the step ordinal runLink carries (the stored weft.step.index —
   * the server and the runtime cut at the first assistant message whose
   * stored index is ≥ from_step), so a step link's number hands off
   * unchanged. */
  step?: number
  instructions?: string
  tools?: string
  model?: string
  thinking?: string
  input?: string
  engine?: "live" | "scripted"
  side_effects?: "substitute" | "park" | "allow"
  thread?: "ephemeral" | "fork"
  agent?: string
  runtime?: string
  /** The option lab's set knobs, JSON (lib/experiment-body.ts's
   * labHandoff). */
  lab?: string
  /** The transcript edits, JSON (editsHandoff). */
  edits?: string
}

export interface PlaygroundSearch {
  /** A saved experiment to show (GET /api/experiments/{id}). */
  experiment?: string
  /** The page's own state (G2), written back as it changes so a
   * copied link reopens it: the source run, the step it continues
   * from (the ordinal), the agent and runtime picked, and the engine.
   * The page never writes a prompt into the query (the hand-off's
   * prompt rides the fragment, see hash below). */
  run?: string
  step?: number
  agent?: string
  runtime?: string
  engine?: "live" | "scripted"
  /** The form's draft in this browser's IndexedDB (plan F5,
   * lib/drafts.ts): the page writes it once the form is saved. */
  exp?: string
}

/** The playground's state a link carries (G2): see PlaygroundSearch. */
export interface PlaygroundState {
  run?: string
  step?: number
  agent?: string
  runtime?: string
  engine?: "live" | "scripted"
}

export interface PlaygroundLink {
  to: "/playground"
  search: PlaygroundSearch
  /** The hand-off rides the fragment (no leading "#"): a prompt can be
   * long and is the app's content — the fragment never reaches a
   * server, an access log or a Referer, and no request line bounds
   * it. Studio's /playground reads it once and strips it. */
  hash?: string
}

/** The step-aligned compare (plan E3): the base run a, and the runs
 * compared with it (each one GET /api/diff?a=&b= against a). */
export interface CompareSearch {
  a?: string
  /** The compared runs as one comma list (UX-F-30: b=r_2,r_3, a link a
   * person can read) — an array only when an id itself holds a comma
   * (runsParam); the page reads both (lib/compare-picker.ts's
   * splitRuns), the older JSON array links too. */
  b?: string | string[]
  /** The step ordinal to land on (the row scrolled to, highlighted). */
  step?: number
}

export interface CompareLink {
  to: "/compare"
  search: CompareSearch
}

/** The runs list's search (plan H1): every filter, the sort and the
 * paging cursor, as /runs validates it (routes/runs.index.tsx) — each
 * a URL chip. */
export interface RunsSearch {
  /** Free-text search terms (/api/search's q). */
  q?: string
  agent?: string
  /** The stored four, or "parked" (U1-fix): read-time, applied to the
   * loaded rows (lib/run-filters.ts). */
  status?:
    "running" | "succeeded" | "failed" | "interrupted" | "parked" | "cancelled"
  session?: string
  public_id?: string
  /** Experiments on/off: absent = all runs; "off" excludes playground
   * runs, "on" only those (S4.7). */
  experiments?: "on" | "off"
  /** Tag pairs "k=v": one string, or several (each its own chip). */
  tag?: string | string[]
  /** A tool the run called (exact; search only). */
  tool?: string
  /** The run's model name (exact). */
  model?: string
  /** A substring of the run's error. */
  error?: string
  /** The start's range: a day (YYYY-MM-DD, UTC, until inclusive) or an
   * RFC 3339 time. */
  since?: string
  until?: string
  /** Seconds at least (the loaded rows only: the API has no duration
   * filter). */
  min_duration?: number
  /** Input + output tokens at least. */
  min_tokens?: number
  /** USD at least, own + delegated (capability pricing). */
  min_cost?: number
  /** One run's subagent children (the API's parent=<run id>). */
  parent?: string
  /** Subagent children listed too (the API's all=1). */
  subagents?: "all"
  before?: string
  /** The order: absent = newest first (started). */
  sort?: "duration" | "tokens" | "cost"
  /** One experiment's runs (weft.experiment.id, plan F5). */
  experiment_id?: string
}

export interface RunsLink {
  to: "/runs"
  search: RunsSearch
}

/** The trace list's search (plan H1): its filters as URL chips. */
export interface TracesSearch {
  since?: string
  until?: string
  service?: string
}

export interface TracesLink {
  to: "/traces"
  search: TracesSearch
}

/** The Settings page (plan H4); price prefills a pricing row for an
 * unpriced provider/model (U7). */
export interface SettingsLink {
  to: "/settings"
  search: { price?: string }
}

export type StudioLink =
  | RunLink
  | SessionLink
  | TraceLink
  | PlaygroundLink
  | CompareLink
  | RunsLink
  | TracesLink
  | SettingsLink
  | ExperimentLink
  | ExperimentsLink

/** A whole, non-negative number, or undefined. */
function ordinal(n: number | undefined): number | undefined {
  return ordinalOf(n)
}

/** runLink is a run page: the run, and optionally the step (its
 * ordinal), the call or span selected, the view, the scale and the
 * replay position — runPage's keys, then the raw view's and the replay
 * drawer's. */
export function runLink(id: string, opts: RunLinkOptions = {}): RunLink {
  const link = runPage(id, opts)
  const search = link.search
  if (opts.raw) {
    const raw = rawSearch(opts.raw)
    if (raw.q !== undefined) search.q = raw.q
    if (raw.hide !== undefined) search.hide = raw.hide
    if (raw.ev !== undefined) search.ev = raw.ev
  }
  if (opts.replay) {
    const r = replaySearch(opts.replay)
    search.replay = r.replay
    search.from = r.from
    if (r.of) search.of = r.of
  }
  return link
}

/** runPage is runLink without the raw view's state and the replay
 * drawer's (the devtools panel writes neither, so its bundle carries
 * neither): the same keys, in the same order, for every option it
 * takes. */
export function runPage(
  id: string,
  opts: Omit<RunLinkOptions, "raw" | "replay"> = {}
): RunLink {
  const step = ordinal(opts.step)
  const search: RunSearch = {}
  // Key order is the URL's order: step first, then the view.
  if (step !== undefined) search.step = step
  if (opts.view && opts.view !== "waterfall") search.view = opts.view
  if (step !== undefined && (opts.pane === "request" || opts.pane === "wire"))
    search.pane = opts.pane
  if (opts.call && (opts.resumed || step !== undefined))
    search.sel = `c:${opts.resumed ? "resume" : String(step)}:${opts.call}`
  else if (opts.span) search.sel = `t:${opts.span}`
  else if (opts.sel) search.sel = opts.sel
  if (opts.tab) search.tab = opts.tab
  if (opts.scale === "time" || opts.scale === "order" || opts.scale === "graph")
    search.scale = opts.scale
  const t = ordinal(opts.t)
  if (t !== undefined) search.t = t
  return { to: "/runs/$id", params: { id }, search }
}

/** logsLink is a step's hand-off to its app log lines (U11.2): the
 * step's row selected in the run tree, its drawer's Logs tab open —
 * the devtools panel's 📄 ⤢. */
export const logsLink = (id: string, step: number) =>
  runPage(id, { sel: `s${step}`, tab: "logs" })

/** rawSearch is the raw view's state as search keys, every one
 * present (undefined clears it): the raw view merges them into the
 * current search. Hidden kinds are written in RAW_KINDS' order. */
export function rawSearch(raw: RawLinkState): {
  q: string | undefined
  hide: string | undefined
  ev: number | undefined
} {
  const hidden = new Set(raw.hide ?? [])
  const hide = RAW_KINDS.filter((k) => hidden.has(k)).join(",")
  return {
    q: raw.q || undefined,
    hide: hide || undefined,
    ev: ordinal(raw.ev),
  }
}

/** rawFromSearch reads the raw view's state back from a run page's
 * search, as the router parsed it (values arrive JSON-parsed: a search
 * text of digits is a number — still the text the link carried).
 * Unknown kinds and a position that is not an ordinal are dropped. */
export function rawFromSearch(search: Record<string, unknown>): RawLinkState {
  const q =
    typeof search.q === "string"
      ? search.q
      : typeof search.q === "number"
        ? String(search.q)
        : ""
  const hide =
    typeof search.hide === "string"
      ? RAW_KINDS.filter((k) => (search.hide as string).split(",").includes(k))
      : []
  return { q, hide, ev: ordinal(search.ev as number | undefined) }
}

/** sessionLink is a session's page (its timeline). */
export function sessionLink(id: string): SessionLink {
  return { to: "/sessions/$id", params: { id }, search: {} }
}

/** sessionViewLink is a session's page in a view, optionally with one
 * mark's drawer open (U15.1). Apart from sessionLink so the devtools
 * panel, which links the page alone, carries none of it. */
export function sessionViewLink(
  id: string,
  opts: { view?: SessionView; mark?: string; lanes?: string }
): SessionLink {
  const search: SessionSearch = {}
  if (opts.view === "tree" || opts.view === "table") search.view = opts.view
  if (opts.mark) search.mark = opts.mark
  if (opts.lanes) search.lanes = opts.lanes
  return { to: "/sessions/$id", params: { id }, search }
}

/** sessionSearch reads the session page's search back (sessions/$id's
 * validateSearch): an unknown view is the timeline, a mark any
 * non-empty string (a number-looking entry id parses as a number). */
export function sessionSearch(search: Record<string, unknown>): SessionSearch {
  const out: SessionSearch = {}
  if (search.view === "tree" || search.view === "table") out.view = search.view
  const m = search.mark
  if (typeof m === "string" && m) out.mark = m
  else if (typeof m === "number") out.mark = String(m)
  if (typeof search.lanes === "string" && search.lanes) out.lanes = search.lanes
  return out
}

/** traceLink is a trace's page, optionally with a span selected and
 * the chat view on. */
export function traceLink(
  id: string,
  opts: { span?: string; view?: "tree" | "chat" } = {}
): TraceLink {
  const link = tracePage(id)
  if (opts.span) link.search.span = opts.span
  if (opts.view === "chat") link.search.view = "chat"
  return link
}

/** tracePage is a trace's page alone: traceLink with no option (the
 * devtools panel's, whose bundle so carries no other). */
export function tracePage(id: string): TraceLink {
  return { to: "/traces/$id", params: { id }, search: {} }
}

/** traceSelectionLink is a trace's page with a row selected by its
 * key (TraceSearch.sel) — the trace page's own links (plan H7). */
export function traceSelectionLink(
  id: string,
  opts: { sel?: string; view?: "tree" | "chat" } = {}
): TraceLink {
  const link = traceLink(id, { view: opts.view })
  return opts.sel
    ? { ...link, search: { sel: opts.sel, ...link.search } }
    : link
}

/** runsLink is the runs list under these filters (empty values are
 * dropped: a link never carries a cleared chip). */
export function runsLink(search: RunsSearch = {}): RunsLink {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(search))
    if (v !== undefined && v !== "" && !(Array.isArray(v) && v.length === 0))
      out[k] = v
  return { to: "/runs", search: out }
}

/** tracesLink is the trace list under these filters. */
export function tracesLink(search: TracesSearch = {}): TracesLink {
  const out: TracesSearch = {}
  if (search.since) out.since = search.since
  if (search.until) out.until = search.until
  if (search.service) out.service = search.service
  return { to: "/traces", search: out }
}

/** settingsLink is the Settings page (plan H4); with a provider/model,
 * its pricing table with a row prefilled for it (U7's "price this
 * model…"). */
export function settingsLink(price?: string): SettingsLink {
  return { to: "/settings", search: price ? { price } : {} }
}

/** playgroundLink is the playground with the hand-off carried over
 * (in the fragment). An empty hand-off is the bare playground. */
export function playgroundLink(
  handoff: PlaygroundHandoff = {}
): PlaygroundLink {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(handoff) as [
    keyof PlaygroundHandoff,
    unknown,
  ][]) {
    if (v === undefined || v === "") continue
    if (k === "step" && ordinal(v as number) === undefined) continue
    p.set(k, String(v))
  }
  const hash = p.toString()
  return hash
    ? { to: "/playground", search: {}, hash }
    : { to: "/playground", search: {} }
}

/** playgroundStateLink is the playground with its own state in the
 * query (G2): what the page writes back as the reader changes it, so
 * the address bar is always a link to what is on screen. The hand-off
 * (playgroundLink) stays the fragment's: this link carries no prompt. */
export function playgroundStateLink(
  state: PlaygroundState,
  experiment?: string
): PlaygroundLink {
  return { to: "/playground", search: playgroundSearch(state, experiment) }
}

/** playgroundSearch is playgroundStateLink's search alone (for the
 * page's own navigate, which merges it into the current search). */
export function playgroundSearch(
  state: PlaygroundState,
  experiment?: string
): PlaygroundSearch {
  const search: PlaygroundSearch = {}
  if (experiment) search.experiment = experiment
  if (state.run) search.run = state.run
  // Step 0 is the default (the whole turn): never written.
  const step = ordinal(state.step)
  if (step) search.step = step
  if (state.agent) search.agent = state.agent
  if (state.runtime) search.runtime = state.runtime
  if (state.engine === "scripted") search.engine = "scripted"
  return search
}

/** The experiments pages (plan F5): the saved experiments, and one
 * experiment's variants × inputs grid. */
export interface ExperimentsLink {
  to: "/experiments"
  search: Record<string, never>
}

export interface ExperimentLink {
  to: "/experiments/$id"
  params: { id: string }
  search: Record<string, never>
}

/** experimentLink is a saved experiment's page (plan F5: its grid,
 * re-run, clone and compare). */
export function experimentLink(id: string): ExperimentLink {
  return { to: "/experiments/$id", params: { id }, search: {} }
}

/** experimentsLink is the saved experiments' list. */
export function experimentsLink(): ExperimentsLink {
  return { to: "/experiments", search: {} }
}

/** sessionsLink is the sessions list. */
export function sessionsLink(): { to: "/sessions"; search: object } {
  return { to: "/sessions", search: {} }
}

/** playgroundDraftLink is the playground with a draft of this browser
 * open (?exp=, lib/drafts.ts). */
export function playgroundDraftLink(id: string): PlaygroundLink {
  return { to: "/playground", search: { exp: id } }
}

/** playgroundShareLink is the playground with a whole form in the
 * fragment (#state=, lib/form-state.ts's encodeForm): it restores the
 * form on any browser against the same Studio, and starts nothing. */
export function playgroundShareLink(state: string): PlaygroundLink {
  return { to: "/playground", search: {}, hash: `state=${state}` }
}

/** runsParam is ?b= for these ids: one comma list, or the array when
 * an id holds a comma (a comma list would split it). */
export function runsParam(b: string[]): string | string[] {
  return b.some((x) => x.includes(",")) ? b : b.join()
}

/** compareLink is the step-aligned compare of a base run with others
 * (rows by step ordinal; N-way is N−1 diffs against a), optionally
 * landing on one step (its ordinal). Empty ids and the base itself are
 * dropped from others, duplicates kept once; they go as one comma list
 * (UX-F-30), never a JSON array. */
export function compareLink(
  a: string,
  others: string[] = [],
  step?: number
): CompareLink {
  const b = [...new Set(others.filter((x) => x && x !== a))]
  const search: CompareSearch = { a }
  if (b.length) search.b = runsParam(b)
  const n = ordinal(step)
  if (n !== undefined) search.step = n
  return { to: "/compare", search }
}

/** A search value as the router writes it (TanStack's default
 * stringifySearch): a string that would parse as JSON is quoted, so
 * it reads back as the same string; numbers are written bare. */
function searchValue(v: unknown): string {
  if (typeof v === "string") {
    try {
      JSON.parse(v)
      return JSON.stringify(v)
    } catch {
      return v
    }
  }
  return typeof v === "object" ? JSON.stringify(v) : String(v)
}

/** path renders a link's route path with its params encoded, relative
 * (no leading "/") so it resolves under the Studio mount. */
export function path(link: StudioLink): string {
  let p = link.to.replace(/^\//, "")
  if ("params" in link) p = p.replace("$id", encodeURIComponent(link.params.id))
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(link.search))
    if (v !== undefined) q.set(k, searchValue(v))
  const qs = q.toString()
  const hash = "hash" in link && link.hash ? `#${link.hash}` : ""
  return `${p}${qs ? `?${qs}` : ""}${hash}`
}

/** Search keys a copied link never carries: credentials, a panel
 * scope string, and prompt text (a playground hand-off's input=,
 * instructions=, edits= — tool results and user words — and lab=,
 * which an old or hand-built link may still carry in its query; the
 * hand-off rides the fragment).
 * Studio's own pages strip a handed-over token on arrival (adoptTokenFromLocation); this is the second wall, so a
 * "copy link" can never leak one. */
const STRIPPED_KEYS = [
  "token",
  "access_token",
  "sig",
  "weft_scope",
  "input",
  "instructions",
  "edits",
  "lab",
]

/**
 * canonical is the page's link as a reader copies it (G2's copy link):
 * the absolute URL with its search — every bit of page state — and
 * without a fragment (a hand-off or a token, never page state) or a
 * credential key. A copied link is the bare page URL: never a token,
 * never a panel scope string, never prompt text.
 */
export function canonical(url: string | URL): string {
  const u = new URL(url)
  u.hash = ""
  // Only a present key is deleted: a delete re-serializes the query.
  for (const k of STRIPPED_KEYS)
    if (u.searchParams.has(k)) u.searchParams.delete(k)
  return u.toString()
}

/** href is a link as an absolute URL under a Studio base (the panel's
 * endpoint: the Studio mount, ending in "/"). */
export function href(base: string, link: StudioLink): string {
  return new URL(path(link), base).toString()
}

// ── Save as test (plan F6) ──────────────────────────────────────────

/** saveTestLink is the run page that, on landing, downloads the run's
 * wefttest zip with Studio's own bearer (never a token in the URL) and
 * drops ?save= — the devtools panel's "save as test" hands off here. */
export function saveTestLink(id: string): RunLink {
  const l = runPage(id)
  return { ...l, search: { ...l.search, save: "test" } }
}

// ── Open in app (plan G3) ───────────────────────────────────────────

/** appLink is "open in app": the host page a run's request came from
 * (its weft.page_url, set by scope.Header) with #weft_scope= — the
 * serialised Scope (lib/scope.ts), encoded ONCE, as the panel's URL
 * rung (plan C3.4) reads it — replacing any fragment the page had.
 * The panel's rung accepts a scope only when it names a public id, so
 * a scope without one carries no fragment: the page opens as it is.
 * null for a page that is not an http(s) URL. Never a token. */
export function appLink(page: string, scope: Scope): string | null {
  let u: URL
  try {
    u = new URL(page)
  } catch {
    return null
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") return null
  u.hash = ""
  const base = u.toString()
  return scope.publicId
    ? `${base}#weft_scope=${encodeURIComponent(serializeScope(scope))}`
    : base
}
