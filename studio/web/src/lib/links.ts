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
  return { to: "/runs/$id", params: { id }, search }
}

/** sessionLink is a session's page (its turns). */
export function sessionLink(id: string): SessionLink {
  return { to: "/sessions/$id", params: { id }, search: {} }
}

/** traceLink is a trace's page, optionally with a span selected. */
export function traceLink(id: string, opts: { span?: string } = {}): TraceLink {
  return {
    to: "/traces/$id",
    params: { id },
    search: opts.span ? { span: opts.span } : {},
  }
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

/** experimentLink is a saved experiment, shown in the playground's
 * experiment history. */
export function experimentLink(id: string): PlaygroundLink {
  return { to: "/playground", search: { experiment: id } }
}

/** compareLink is the step-aligned compare of a base run with others
 * (rows by step ordinal; N-way is N−1 diffs against a). Empty ids and
 * the base itself are dropped from others, duplicates kept once. */
export function compareLink(a: string, others: string[] = []): CompareLink {
  const b = [...new Set(others.filter((x) => x && x !== a))]
  return { to: "/compare", search: b.length ? { a, b } : { a } }
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

/** href is a link as an absolute URL under a Studio base (the panel's
 * endpoint: the Studio mount, ending in "/"). */
export function href(base: string, link: StudioLink): string {
  return new URL(path(link), base).toString()
}
