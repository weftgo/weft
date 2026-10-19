// Page titles (plan G2): one shape for every Studio page, so a tab
// strip, the browser's history menu and a bookmark say which run,
// trace or page each one is — `director · How many units… · succeeded
// · weft studio`, `run s_…-t3 · succeeded · weft studio` before the
// run is read. Plain strings, no DOM: hooks/use-document-title.ts
// writes them.
//
// Titles, not ids (U6): runTitle and sessionTitle are what a run row, a
// session's turn, a session row, the run page and the panel's turn list
// lead with — the words a person recognises (the prompt, the session's
// title), the id a copyable line under them. Studio and the panel read
// the same helper (parity.test.ts pins both renderers on one row).
import { plural } from "./format"
import { isLive } from "./honesty"

/** The suffix every title ends with (the root route's own title). */
export const STUDIO = "weft studio"

/** What a page is, for its title. */
export type TitlePlace =
  | {
      page: "run"
      id: string
      status?: string
      /** The run's title (runTitle's text) and agent, once read. */
      title?: string
      agent?: string
    }
  | { page: "trace"; id: string }
  | { page: "session"; id: string; title?: string; turns?: number }
  | { page: "conversation"; id: string; turns?: number }
  | { page: "compare"; a?: string; b?: readonly string[] }
  | { page: "playground"; experiment?: string }
  | { page: "experiment"; id: string }
  | {
      page:
        | "runs"
        | "sessions"
        | "traces"
        | "live"
        | "agents"
        | "settings"
        | "experiments"
    }

/**
 * shortId elides a long id for a title, keeping what tells ids apart:
 * a session turn's id keeps its prefix and its turn (`s_…-t3`), any
 * other long id its head and tail. An id of 16 characters or fewer is
 * kept whole.
 */
export function shortId(id: string): string {
  if (id.length <= 16) return id
  const turn = /^([A-Za-z]+_).+(-t\d+)$/.exec(id)
  if (turn) return `${turn[1]}…${turn[2]}`
  return `${id.slice(0, 8)}…${id.slice(-4)}`
}

/** pageTitle is a page's document title. */
export function pageTitle(place: TitlePlace): string {
  const parts: string[] = []
  switch (place.page) {
    case "run":
      if (place.title) {
        if (place.agent && place.title !== place.agent) parts.push(place.agent)
        parts.push(clip(place.title))
      } else parts.push(`run ${shortId(place.id)}`)
      if (place.status) parts.push(place.status)
      break
    case "trace":
      parts.push(`trace ${shortId(place.id)}`)
      break
    case "conversation":
      parts.push(`conversation ${shortId(place.id)}`)
      if (place.turns !== undefined) parts.push(plural(place.turns, "turn"))
      break
    case "session":
      parts.push(
        place.title ? clip(place.title) : `session ${shortId(place.id)}`
      )
      if (place.turns !== undefined) parts.push(plural(place.turns, "turn"))
      break
    case "compare": {
      const ids = [place.a, ...(place.b ?? [])].filter((x): x is string => !!x)
      parts.push(
        ids.length ? `compare ${ids.map(shortId).join(" ↔ ")}` : "compare"
      )
      break
    }
    case "experiment":
      parts.push(`experiment ${shortId(place.id)}`)
      break
    case "playground":
      parts.push(
        place.experiment
          ? `playground ${shortId(place.experiment)}`
          : "playground"
      )
      break
    default:
      parts.push(place.page)
  }
  parts.push(STUDIO)
  return parts.join(" · ")
}

/** TITLE_CUT is how many characters a title keeps where it is cut in
 * words, not by the layout: a tab title, the panel's dropdown, a turn
 * row's prompt. */
export const TITLE_CUT = 40

/** clip is s's first non-empty line, at most n characters, "…" marking
 * a cut — a cut is never silent. */
export function clip(s: string, n = TITLE_CUT): string {
  const line =
    s
      .split("\n")
      .map((l) => l.trim())
      .find(Boolean) ?? ""
  const cs = [...line]
  return cs.length > n
    ? `${cs
        .slice(0, n - 1)
        .join("")
        .trimEnd()}…`
    : line
}

/** NO_CONTENT stands in for a prompt the record did not keep: a
 * content-off destination stores no messages (the run page's stripped
 * badge says why). */
export const NO_CONTENT = "(content not captured)"

/** A title and what it is: the prompt (a run's prompt_preview, a
 * session's first turn's), the session's own title, the agent's name,
 * NO_CONTENT, or the id when nothing else is known. full is the whole
 * text a title attribute shows (the 200-rune preview). */
export interface Title {
  text: string
  full: string
  kind: "prompt" | "title" | "agent" | "absent" | "resume" | "id"
}

/** The run-row fields a title reads (api.ts's RunRow). */
export interface TitledRun {
  id: string
  agent?: string
  prompt_preview?: string
  message_count?: number
  event_count?: number
  derived?: boolean
  status?: string
  settling?: boolean
  /** What the core emitted (api.ts's RunRow): fewer stored is a loss. */
  events_emitted?: number
  /** A run document's (or a child row's) holes; a list row has none. */
  holes?: readonly { hole: string }[]
}

/** contentOff: the row's run was recorded without content — events
 * stored and not one messages record (a content-off destination stores
 * none). A derived run has neither and is not content-off. Not while
 * the run is live (isLive: running or settling — its messages records
 * may still arrive), and not where the row names a lost-records hole
 * (gap, events_lost among its causes; not_recorded): records were
 * dropped, not content (W7-prep). A list row carries no holes, so
 * there the status and settling fields gate it, and its counts: fewer
 * events stored than the core emitted (events_emitted) is a loss, not
 * content-off (D7). */
export function contentOff(r: TitledRun): boolean {
  return (
    !r.derived &&
    !r.message_count &&
    (r.event_count ?? 0) > 0 &&
    !isLive(r) &&
    !(r.events_emitted && (r.event_count ?? 0) < r.events_emitted) &&
    !r.holes?.some((h) => h.hole === "gap" || h.hole === "not_recorded")
  )
}

/** runTitle titles a run: its prompt_preview's first line, else its
 * agent (with NO_CONTENT beside it when content was off), else
 * NO_CONTENT, else its id. */
export function runTitle(r: TitledRun): Title {
  const p = clip(r.prompt_preview ?? "", Infinity)
  if (p) return { text: p, full: r.prompt_preview ?? p, kind: "prompt" }
  const off = contentOff(r)
  if (r.agent) {
    const text = off ? `${r.agent} · ${NO_CONTENT}` : r.agent
    return { text, full: text, kind: "agent" }
  }
  if (off) return { text: NO_CONTENT, full: NO_CONTENT, kind: "absent" }
  return { text: r.id, full: r.id, kind: "id" }
}

/** sessionTitle titles a session: its title (thread's
 * weft.session.title, metadata that survives content-off), else its
 * first turn's prompt, else NO_CONTENT when that turn was recorded
 * without content (the row's content_off, or first — the turn, when
 * the caller has it), else its id. */
export function sessionTitle(
  s: {
    id: string
    title?: string
    first_prompt?: string
    content_off?: boolean
  },
  first?: TitledRun
): Title {
  const t = clip(s.title ?? "", Infinity)
  if (t) return { text: t, full: s.title ?? t, kind: "title" }
  const p = clip(s.first_prompt ?? "", Infinity)
  if (p) return { text: p, full: s.first_prompt ?? p, kind: "prompt" }
  if (s.content_off || (first && contentOff(first)))
    return { text: NO_CONTENT, full: NO_CONTENT, kind: "absent" }
  return { text: s.id, full: s.id, kind: "id" }
}

/** turnLabel is a session turn's short name, "t3"; "" off a session. */
export function turnLabel(r: { turn?: number }): string {
  return r.turn ? `t${r.turn}` : ""
}

/** resumeOf says a turn resumes the one before it: that turn parked
 * (pending > 0), and this one carries no prompt of its own (none, or
 * the parked turn's again — thread's resume re-sends its context). The
 * rule until the record names a resume (weft.resumed_from, U13.1):
 * the previous turn's pending count, which is also how many calls the
 * resume decided. */
export function resumeOf(
  prev:
    { turn?: number; pending?: number; prompt_preview?: string } | undefined,
  r: { prompt_preview?: string }
): { of: string; decided: number } | null {
  if (!prev || !prev.pending) return null
  if (r.prompt_preview && r.prompt_preview !== prev.prompt_preview) return null
  return { of: turnLabel(prev), decided: prev.pending }
}

/** turnTitle titles a session's turn: resumeOf's line on a resume
 * ("resume of t1 · 1 decided call"), else runTitle. */
export function turnTitle(
  prev: Parameters<typeof resumeOf>[0],
  r: TitledRun
): Title {
  const x = resumeOf(prev, r)
  if (!x) return runTitle(r)
  const text = `resume of ${x.of || "the parked turn"} · ${plural(x.decided, "decided call")}`
  return {
    text,
    full: `${text}\n${r.prompt_preview ?? ""}`.trim(),
    kind: "resume",
  }
}

/** traceTitle titles a trace list row (UX-A-39): its top run's agent
 * and prompt ("orders · Where is order 42?"), else that agent alone,
 * else the root span's name (a trace of non-weft spans), else "(no
 * root span)". full keeps the whole prompt for the title attribute. */
export function traceTitle(t: {
  root: { name: string } | null
  root_run?: { agent?: string; prompt_preview?: string }
}): { text: string; full: string } {
  const run = t.root_run
  const prompt = clip(run?.prompt_preview ?? "", 80)
  if (run?.agent || prompt) {
    const text = [run?.agent, prompt].filter(Boolean).join(" · ")
    return {
      text,
      full: [run?.agent, run?.prompt_preview].filter(Boolean).join(" · "),
    }
  }
  const name = t.root?.name ?? "(no root span)"
  return { text: name, full: name }
}

/** traceRunsWords is a trace row's runs cell (UX-A-39): how many runs
 * the trace holds and how many of them failed — "3 runs · 1 failed" —
 * never the id list; "—" for none. */
export function traceRunsWords(t: {
  run_ids: readonly string[]
  failed_runs?: number
}): string {
  const n = t.run_ids.length
  if (!n) return "—"
  const runs = plural(n, "run")
  return t.failed_runs ? `${runs} · ${t.failed_runs} failed` : runs
}
