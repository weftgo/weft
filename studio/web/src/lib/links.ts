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

/** The run page's views: the trace (the default), the story (step
 * cards) and the raw JSON. */
export type RunView = "trace" | "story" | "raw"

/** The run page's search, as runs/$id validates it. */
export interface RunSearch {
  /** The step ordinal (see the header). */
  step?: number
  view?: Exclude<RunView, "trace">
  raw?: "events" | "doc"
  /** The selected span's key (trace view): s<step>, c:<step>:<call id>
   * (c:resume:<call id> for a resumed call), t:<span id>. */
  sel?: string
  /** The detail panel's mode (trace view). */
  d?: "events" | "json"
  /** The waterfall's axis: positions (replay) or time (spans). */
  axis?: "events" | "time"
  /** The replay playhead: a stream position. */
  t?: number
  /** The raw view's search text (G2). */
  q?: string
  /** The raw view's hidden event kinds, comma-separated (G2). */
  hide?: string
  /** The raw view's open event: its stream position (G2). */
  ev?: number
}

/** The raw view's event kinds (lib/summarize.ts's EventKind), as a
 * link's ?hide= names them. */
export const RAW_KINDS = ["step", "tool", "result", "error", "reasoning", "delta"] as const
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
  /** An OTel span to select, by its span id (the trace view's time
   * axis rows). */
  span?: string
  view?: RunView
  axis?: "events" | "time"
  /** The replay playhead (a stream position). */
  t?: number
  /** The raw view's filters and open event (view "raw"). */
  raw?: RawLinkState
}

export interface RunLink {
  to: "/runs/$id"
  params: { id: string }
  search: RunSearch
}

export interface SessionLink {
  to: "/sessions/$id"
  params: { id: string }
  search: Record<string, never>
}

export interface TraceSearch {
  /** The span to select, by its span id. */
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
  b?: string[]
  /** The step ordinal to land on (the row scrolled to, highlighted). */
  step?: number
}

export interface CompareLink {
  to: "/compare"
  search: CompareSearch
}

export type StudioLink = RunLink | SessionLink | TraceLink | PlaygroundLink | CompareLink

/** A whole, non-negative number, or undefined. */
function ordinal(n: number | undefined): number | undefined {
  return typeof n === "number" && Number.isInteger(n) && n >= 0 ? n : undefined
}

/** runLink is a run page: the run, and optionally the step (its
 * ordinal), the call or span selected, the view, the axis and the
 * replay position. */
export function runLink(id: string, opts: RunLinkOptions = {}): RunLink {
  const step = ordinal(opts.step)
  const search: RunSearch = {}
  // Key order is the URL's order: step first, then the view.
  if (step !== undefined) search.step = step
  if (opts.view && opts.view !== "trace") search.view = opts.view
  if (opts.call && (opts.resumed || step !== undefined))
    search.sel = `c:${opts.resumed ? "resume" : String(step)}:${opts.call}`
  else if (opts.span) search.sel = `t:${opts.span}`
  if (opts.axis === "time") search.axis = "time"
  else if (opts.axis === "events") search.axis = "events"
  const t = ordinal(opts.t)
  if (t !== undefined) search.t = t
  if (opts.raw) {
    const raw = rawSearch(opts.raw)
    if (raw.q !== undefined) search.q = raw.q
    if (raw.hide !== undefined) search.hide = raw.hide
    if (raw.ev !== undefined) search.ev = raw.ev
  }
  return { to: "/runs/$id", params: { id }, search }
}

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
    typeof search.q === "string" ? search.q : typeof search.q === "number" ? String(search.q) : ""
  const hide =
    typeof search.hide === "string"
      ? RAW_KINDS.filter((k) => (search.hide as string).split(",").includes(k))
      : []
  return { q, hide, ev: ordinal(search.ev as number | undefined) }
}

/** sessionLink is a session's page (its turns). */
export function sessionLink(id: string): SessionLink {
  return { to: "/sessions/$id", params: { id }, search: {} }
}

/** traceLink is a trace's page, optionally with a span selected and
 * the chat view on. */
export function traceLink(
  id: string,
  opts: { span?: string; view?: "tree" | "chat" } = {}
): TraceLink {
  const search: TraceSearch = {}
  if (opts.span) search.span = opts.span
  if (opts.view === "chat") search.view = "chat"
  return { to: "/traces/$id", params: { id }, search }
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

/** experimentLink is a saved experiment, shown in the playground's
 * experiment history. */
export function experimentLink(id: string): PlaygroundLink {
  return { to: "/playground", search: { experiment: id } }
}

/** compareLink is the step-aligned compare of a base run with others
 * (rows by step ordinal; N-way is N−1 diffs against a), optionally
 * landing on one step (its ordinal). Empty ids and the base itself are
 * dropped from others, duplicates kept once. */
export function compareLink(a: string, others: string[] = [], step?: number): CompareLink {
  const b = [...new Set(others.filter((x) => x && x !== a))]
  const search: CompareSearch = { a }
  if (b.length) search.b = b
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
 * scope string, and prompt text (a playground hand-off's input= and
 * instructions=, which an old link may still carry in its query).
 * Studio's own pages strip a handed-over token on arrival (adoptTokenFromLocation); this is the second wall, so a
 * "copy link" can never leak one. */
const STRIPPED_KEYS = ["token", "access_token", "sig", "weft_scope", "input", "instructions"]

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
  for (const k of STRIPPED_KEYS) if (u.searchParams.has(k)) u.searchParams.delete(k)
  return u.toString()
}

/** href is a link as an absolute URL under a Studio base (the panel's
 * endpoint: the Studio mount, ending in "/"). */
export function href(base: string, link: StudioLink): string {
  return new URL(path(link), base).toString()
}
