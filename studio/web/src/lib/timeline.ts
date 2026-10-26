// The session timeline's words and shape (U15.1; UX-B-01, UX-S3-01,
// UX-S3-05, UX-E-19): one row per exchange — a turn and the resumes
// that decided its parked calls, grouped under it — on a shared time
// axis, the state log's marks (GET /api/sessions/{id}/timeline, the
// `thread` record family and compaction markers) under the turn they
// are filed under, and a skipped turn number said with the receipt that
// consumed it. A mark opens a drawer: who / when / via / key / entry id
// and the links to the parking and resuming steps. Decision words are
// lib/decisions.ts's (one vocabulary with the run page's parked and
// resumed cards); plain TypeScript, no React.
import {
  BUSY_CAUSE,
  ENDED_WORDS,
  endedGlyph,
  endedText,
  OFF_PATH,
} from "./turn-chips"
import type {
  DecidedCall,
  DecisionDoc,
  RunRow,
  SessionMark,
  TimelineDoc,
  TimelineTurn,
} from "./api"
import {
  decider,
  decisionLine,
  markWhoHole,
  outcomeOf,
  whoUnrecorded,
} from "./decisions"
import { compactionLine } from "./compaction"
import { absoluteTime, plural, tokenPair, tokens } from "./format"
import { runLink, sessionLink, sessionSearch, sessionViewLink } from "./links"
import type { RunLink, SessionLink, SessionSearch, SessionView } from "./links"
import type { TONE } from "./palette"
import { turnTag } from "./status"

/** The drawer's closing line: the marks are thread's records, an index
 * of the session file — the file is the record (ADR 0021 §5). */
export const TRAIL_NOTE =
  "the trail is an index of the session log, not evidence — ADR 0021"

/** The session page's views: lib/links.ts's (the approvals view
 * included, so sessionViewLink builds ?view=approvals). */
export type SessionPageView = SessionView

/** The session page's search: lib/links.ts's, the approvals view too. */
export interface SessionPageSearch extends Omit<SessionSearch, "view"> {
  view?: Exclude<SessionPageView, "timeline">
  /** The table's order (UX-S3-16): newest first unless "oldest". */
  order?: "oldest"
}

/** sessionPageSearch is the session page's validateSearch. */
export function sessionPageSearch(
  search: Record<string, unknown>
): SessionPageSearch {
  const out: SessionPageSearch = sessionSearch(search)
  if (search.order === "oldest") out.order = "oldest"
  return out
}

/** parkedCallLink is a parked call's row on its run page: the parked
 * run's last step (where the call was made), or — a resume that parked
 * again before any step (core's resolvePending: steps 0) — its
 * pre-step card. Without the turn (not listed) the call's row by its
 * id alone, whichever step drew it. */
export function parkedCallLink(
  parked: { run_id: string; steps: number } | string,
  call: string
) {
  if (typeof parked === "string") return runLink(parked, { sel: `c:${call}` })
  return parked.steps > 0
    ? runLink(parked.run_id, { step: parked.steps - 1, call })
    : runLink(parked.run_id, { call, resumed: true })
}

/** pageView is the view a session page draws: a known view, else the
 * timeline — at use too, since the router keeps a search key the
 * route's validation dropped (?view=bogus draws the timeline, its tab
 * the current one). */
export const pageView = (v: unknown): SessionPageView =>
  VIEWS.find((x) => x.view === v)?.view ?? "timeline"

/** pageOrder is the table's order: oldest when the search says so,
 * else newest (?order=bogus included). */
export const pageOrder = (o: unknown): "newest" | "oldest" =>
  o === "oldest" ? "oldest" : "newest"

/** sessionPageLink is the session page in a view (sessionViewLink's,
 * the approvals view too). */
export function sessionPageLink(
  id: string,
  opts: { view?: SessionPageView; mark?: string; lanes?: string }
): Omit<SessionLink, "search"> & { search: SessionPageSearch } {
  return sessionViewLink(id, opts)
}

/** The session page's views, in the switcher's order, with titles. */
export const VIEWS: { view: SessionPageView; word: string; title: string }[] = [
  {
    view: "timeline",
    word: "timeline",
    title:
      "turns on a shared time axis, the state log's marks under the turn each is filed under",
  },
  {
    view: "tree",
    word: "tree",
    title: "the session's branches and forks as a tree",
  },
  { view: "table", word: "table", title: "one row per turn" },
  {
    view: "approvals",
    word: "approvals",
    title:
      "every decision and grant of the session in one table: who, via, key, grant, when, the turns that parked and resumed each call",
  },
]

/** The timeline's read failed: the turns still draw, from the session. */
export const timelineUnread = (why: string) =>
  `the session's state log could not be read (${why}): the turns are drawn without their marks`

/** The timeline is off on this Studio (meta's capability). */
export const TIMELINE_OFF =
  "this Studio does not read a session's state log (capability timeline is off): the turns are drawn without their marks"

/** A session-level mark's heading: filed under no run (run_id null —
 * thread held it until the session closed, with no run to file it
 * under). */
export const SESSION_LEVEL =
  "filed under no turn (held until the session closed)"

/** The heading of the marks filed under a run this page does not list
 * (a deleted turn, or one outside the reader's scope). */
export const UNLISTED_RUN =
  "filed under a run not on this page (a deleted or unlisted turn)"

/** unlistedRun is the drawer's "filed under" value for such a mark. */
export const unlistedRun = (run: string) => `${run} · not on this page`

/** markKeys are the marks' keys in ?mark=, stable while a live session
 * grows (a new mark, earlier or later, moves no other key): a mark's
 * thread entry id, else kind:at:run (an interrupt names no entry); a
 * true duplicate — the same key again — gains ":<n>", its occurrence
 * among that key's marks. */
export function markKeys(marks: readonly SessionMark[]): string[] {
  const seen = new Map<string, number>()
  return marks.map((m) => {
    const base = m.entry || `${m.kind}:${m.at}:${m.run_id ?? "session"}`
    const n = seen.get(base) ?? 0
    seen.set(base, n + 1)
    return n ? `${base}:${n}` : base
  })
}

/** clockTime is a time of day to the millisecond, local: "11:57:53.599"
 * — the axis's label (absoluteTime, with its date and zone, is the
 * title). */
export function clockTime(iso: string): string {
  const t = new Date(Date.parse(iso))
  if (Number.isNaN(t.getTime())) return "—"
  const p = (n: number, w = 2) => String(n).padStart(w, "0")
  return `${p(t.getHours())}:${p(t.getMinutes())}:${p(t.getSeconds())}.${p(t.getMilliseconds(), 3)}`
}

/** turnOfRow is a session document's run row as a timeline turn: the
 * page draws the turns from it when the timeline cannot be read. */
export function turnOfRow(r: RunRow): TimelineTurn {
  return {
    run_id: r.id,
    turn: r.turn,
    agent: r.agent,
    status: r.status,
    started: r.started,
    finished: r.finished,
    last_seen: r.last_seen,
    steps: r.steps,
    pending: r.pending || undefined,
    prompt_preview: r.prompt_preview,
    resumed_from: r.resumed_from,
    resumed_by: r.resumed_by,
    stopped_by: r.stopped_by,
    error_type: r.error_type,
    usage: r.usage,
    marks: 0,
  }
}

/** An exchange: a turn and the resumes that decided its parked calls
 * (a chain: a resume may park again), in turn order. */
export interface Exchange {
  lead: TimelineTurn
  resumes: TimelineTurn[]
}

/** resumedFrom is the parked turn a turn resumes: its stored link
 * (weft.resumed_from, U13.1), else the turn before it when that one
 * parked and this one carries no prompt of its own (lib/title.ts's
 * resumeOf rule — a resume re-sends the parked turn's context). */
function resumedFrom(
  t: TimelineTurn,
  prev: TimelineTurn | undefined,
  byID: Map<string, TimelineTurn>
): TimelineTurn | undefined {
  if (t.resumed_from) return byID.get(t.resumed_from)
  if (!prev?.pending || prev.resumed_by) return undefined
  if (t.prompt_preview && t.prompt_preview !== prev.prompt_preview)
    return undefined
  return prev
}

/** exchanges groups a session's turns (turn order) into exchanges:
 * every resume under the exchange of the turn it resumes (UX-S3-05). */
export function exchanges(turns: readonly TimelineTurn[]): Exchange[] {
  const sorted = [...turns].sort((a, b) => a.turn - b.turn)
  const byID = new Map(sorted.map((t) => [t.run_id, t]))
  const home = new Map<string, Exchange>()
  const out: Exchange[] = []
  sorted.forEach((t, i) => {
    const from = resumedFrom(t, sorted[i - 1], byID)
    const ex = from && home.get(from.run_id)
    if (ex) {
      ex.resumes.push(t)
      home.set(t.run_id, ex)
      return
    }
    const fresh = { lead: t, resumes: [] }
    out.push(fresh)
    home.set(t.run_id, fresh)
  })
  return out
}

/** resumeOfTurn is the turn that resumed a parked one: the stored link
 * either way (resumed_by on the park, resumed_from on the resume), else
 * the exchange's next turn. */
export function resumeOfTurn(
  run: string | null,
  turns: readonly TimelineTurn[]
): TimelineTurn | undefined {
  if (!run) return undefined
  const parked = turns.find((t) => t.run_id === run)
  if (parked?.resumed_by)
    return turns.find((t) => t.run_id === parked.resumed_by)
  const linked = turns.find((t) => t.resumed_from === run)
  if (linked) return linked
  for (const ex of exchanges(turns)) {
    const chain = [ex.lead, ...ex.resumes]
    const i = chain.findIndex((t) => t.run_id === run)
    if (i >= 0) return chain[i + 1]
  }
  return undefined
}

/** The header's counts (UX-S3-05): exchanges beside runs. */
export function exchangesWords(turns: readonly TimelineTurn[]): string {
  const ex = exchanges(turns).length
  return ex === turns.length
    ? plural(turns.length, "turn")
    : `${plural(ex, "exchange")} · ${plural(turns.length, "turn")}`
}

/** The decision a mark records, in lib/decisions.ts's shapes: thread's
 * outcome (approve · deny · resolve · resolve_error; an expiry's deny)
 * as the run page's (approved · denied · resolved · expired). */
export function decisionOf(
  m: SessionMark,
  tool?: string
): { c: DecidedCall; d: DecisionDoc } {
  const f = m.fields
  const s = (k: string) => (typeof f[k] === "string" ? f[k] : "")
  const cs = (k: string) =>
    typeof m.content?.[k] === "string" ? m.content[k] : ""
  const via = s("via")
  const outcome =
    s("outcome") === "approve"
      ? "approved"
      : s("outcome") === "deny"
        ? via === "expiry"
          ? "expired"
          : "denied"
        : s("outcome") === "resolve" || s("outcome") === "resolve_error"
          ? "resolved"
          : s("outcome")
  const c: DecidedCall = {
    call_id: s("call_id"),
    name: tool,
    outcome,
    via: via || undefined,
    key_id: s("key_id") || undefined,
    grant_id: s("grant_id") || undefined,
    who: cs("who") || undefined,
    // The resume's weft.approval.actor (the mark's actor) is metadata,
    // read by every token as the run page's decided block reads it:
    // who is hidden only when nothing else names the decider — several,
    // the resume's (actor_of), name no one call's.
    who_hidden: (m.content_hidden && (!m.actor || !!m.actor_of)) || undefined,
    reason: cs("reason") || undefined,
    result: cs("content") || undefined,
    at: m.fact_at ?? m.at,
  }
  return {
    c,
    d: {
      run: m.run_id ?? "",
      at: m.at,
      actor: m.actor,
      source: "thread",
      calls: [c],
    },
  }
}

/** A mark as one line: its icon, words and tone (lib/palette.ts's
 * TONE). */
export interface MarkLine {
  icon: string
  text: string
  tone: keyof typeof TONE
}

const str = (v: unknown) => (typeof v === "string" ? v : "")
const num = (v: unknown) => (typeof v === "number" ? v : 0)

/** The words a mark reads as on the timeline. turns names the turns a
 * mark points at; tools the tool each call id parked. */
export function markLine(
  m: SessionMark,
  turns: readonly TimelineTurn[],
  tools: ReadonlyMap<string, string> = new Map()
): MarkLine {
  const f = m.fields
  const tag = (run: string) => turnTag(run, turns.map(asStatusRow))
  const text = str(m.content?.text)
  const quoted = text ? ` '${text}'` : ""
  switch (m.kind) {
    case "approval.request": {
      const q = num(f.quorum)
      return {
        icon: "⏸",
        text: `parked · ${str(f.tool) || str(f.call_id)}${q > 1 ? ` · needs ${q} approvals` : ""}`,
        tone: "never",
      }
    }
    case "approval.decision": {
      const { c, d } = decisionOf(m, tools.get(str(f.call_id)))
      const to = resumeOfTurn(m.run_id, turns)
      const o = outcomeOf(c.outcome)
      return {
        icon: o.icon,
        text: `${decisionLine(c, d, to ? tag(to.run_id) : undefined).slice(o.icon.length + 1)}${f.always ? " · approve always" : ""}`,
        tone: o.tone,
      }
    }
    case "approval.refused":
      return {
        icon: "⚠",
        text: `signed decision refused · ${str(f.reason).replaceAll("_", " ")}`,
        tone: "error",
      }
    case "grant.mint":
      return {
        icon: "⚿",
        text: `grant ${str(f.grant_id)} · ${str(f.tool)}${f.deny ? " · deny" : ""}${str(f.via) === "always" ? " · from approve always" : ""}`,
        tone: "tool",
      }
    case "grant.use":
      return {
        icon: "⚿",
        text: `grant ${str(f.grant_id)} used · ${str(f.outcome)}${f.uses ? ` · use ${num(f.uses)}${f.max_uses ? ` of ${num(f.max_uses)}` : ""}` : ""}`,
        tone: "tool",
      }
    case "grant.revoke":
      return {
        icon: "⚿",
        text: `grant ${str(f.grant_id)} revoked · ${str(f.tool)}`,
        tone: "error",
      }
    case "grant.expire":
      return {
        icon: "⏱",
        text: `grant ${str(f.grant_id)} expired · ${str(f.reason).replaceAll("_", " ")}`,
        tone: "error",
      }
    case "compaction":
      return {
        icon: "⌗",
        text: `compaction · ${compactionLine({
          scope: "session",
          replaced: num(f.replaced),
          entries: num(f.entries),
          tokens_before: num(f.tokens_before),
          tokens_after: num(f.tokens_after),
        } as Parameters<
          typeof compactionLine
        >[0])}${str(f.reason) ? ` · ${str(f.reason)}` : ""}`,
        tone: "step",
      }
    case "uncompact":
      return { icon: "⌗", text: "compaction undone", tone: "step" }
    case "pin":
      return { icon: "⌗", text: `pinned ${str(f.target)}`, tone: "step" }
    case "receipt":
      return receiptLine(m, tag, quoted)
    case "interrupt": {
      // A busy policy's outcome, never a failure (UX-B-09): neutral.
      const by = str(f.by_run)
      const rollback = str(f.policy) === "rollback"
      return {
        icon: rollback ? "↶" : "✗",
        text: `${rollback ? END_WORDS.rolled_back : END_WORDS.interrupted}${by ? ` by ${tag(by)}` : ""}${str(f.target) === "boundary" ? " · at its approval boundary" : ""}${num(f.denied_calls) ? ` · ${plural(num(f.denied_calls), "call")} denied` : ""}`,
        tone: "step",
      }
    }
    case "rewind":
      return {
        icon: "↶",
        text:
          str(f.reason) === "rollback"
            ? `rewound · ${OFF_PATH} (rollback)`
            : `rewound · ${str(f.reason)}`,
        tone: "step",
      }
    case "branch": {
      // A Branch to the entry the leaf already was on moves nothing:
      // no line is left (U15.2-fix).
      const from = str(f.from_entry)
      const stayed = !!from && from === str(f.to_entry)
      return {
        icon: "⑂",
        text: `${stayed ? BRANCH_WORDS.stayed : BRANCH_WORDS.moved}${f.summarized ? " · the left branch summarized" : ""}`,
        tone: "step",
      }
    }
    case "fork":
      return {
        icon: "⑃",
        text: `${FORKED_INTO} ${str(f.new_session)}`,
        tone: "step",
      }
    case "label":
      return { icon: "⚑", text: `label ${str(f.name)}`, tone: "step" }
    case "pool.delegate":
      return {
        icon: "↳",
        text: `delegated session ${str(f.child_session)}${str(f.call_id) ? ` · call ${str(f.call_id)}` : ""}`,
        tone: "tool",
      }
    case "pool.retire":
      return {
        icon: "↳",
        text: `delegated session ${str(f.child_session)} · ${str(f.state)}`,
        tone: str(f.state) === "done" ? "result" : "error",
      }
  }
  return { icon: "·", text: m.kind, tone: "step" }
}

function receiptLine(
  m: SessionMark,
  tag: (run: string) => string,
  quoted: string
): MarkLine {
  const f = m.fields
  const what = str(f.input) || "input"
  const minted = str(f.minted_run)
  switch (str(f.state)) {
    case "queued":
      return {
        icon: "⏭",
        text: `${what} queued${num(f.turn) ? ` · took t${num(f.turn)}` : ""}${quoted}`,
        tone: "step",
      }
    case "accepted":
      return {
        icon: "⏭",
        text: `${what} accepted${minted ? ` → ran as ${tag(minted)}` : ""}${quoted}`,
        tone: "step",
      }
    case "delivered":
      return {
        icon: "⇢",
        text: `${what} delivered into ${m.run_id ? tag(m.run_id) : "the running turn"}${quoted}`,
        tone: "result",
      }
    case "deferred":
      return {
        icon: "⇢",
        text: `${what} deferred to a follow-up turn${quoted}`,
        tone: "error",
      }
    case "dropped":
      return {
        icon: "🗑",
        text: `${what} dropped${quoted}`,
        tone: "error",
      }
  }
  return { icon: "⏭", text: `${what} ${str(f.state)}${quoted}`, tone: "step" }
}

export const asStatusRow = (t: TimelineTurn) => ({
  id: t.run_id,
  status: t.status,
  turn: t.turn,
})

/** A skipped turn number (UX-E-19): thread mints a turn number for an
 * input it accepts while a turn runs; a steer delivered into that turn
 * (or dropped) never names a run. The words say which receipt took it;
 * a run of numbers no receipt explains is one gap, "t2–t8", drawn as a
 * not_recorded hole with its reason (never a row per number). */
export interface TurnGap {
  /** The first skipped number (the gap's sort key) … */
  turn: number
  /** … and the last: turn itself for a number a receipt explains. */
  to: number
  text: string
  /** The receipt mark's key that consumed it, when one did. */
  mark?: string
  /** No receipt explains the numbers: the not_recorded hole's reason. */
  reason?: string
}

/** The words of a run of turn numbers no receipt explains. */
export const gapRange = (from: number, to: number) =>
  from === to ? `t${from}` : `t${from}–t${to}`
export const gapReason = (from: number, to: number) =>
  `no run of ${from === to ? "this turn number" : "these turn numbers"} was recorded and no receipt names ${from === to ? "it" : "them"}: a turn that never reported (its process ended first, or a destination dropped it), or a turn this reader cannot see`

/** turnGaps lists the turn numbers between a session's first and last
 * turn that no run carries: each one a receipt took with that receipt,
 * consecutive unexplained ones as one range. Linear in the session's
 * span and its marks (t1 + t1000 is one gap, not 998 rows). */
export function turnGaps(
  turns: readonly TimelineTurn[],
  marks: readonly SessionMark[]
): TurnGap[] {
  if (turns.length === 0) return []
  const have = new Set(turns.map((t) => t.turn))
  const lo = Math.min(...have)
  const hi = Math.max(...have)
  const keys = markKeys(marks)
  // The first receipt naming each number, read once.
  const took = new Map<number, number>()
  marks.forEach((m, i) => {
    const n = num(m.fields.turn)
    if (m.kind === "receipt" && n && !took.has(n)) took.set(n, i)
  })
  const out: TurnGap[] = []
  let open: number | undefined
  const close = (to: number) => {
    if (open === undefined) return
    out.push({
      turn: open,
      to,
      text: gapRange(open, to),
      reason: gapReason(open, to),
    })
    open = undefined
  }
  for (let n = lo + 1; n < hi; n++) {
    if (have.has(n)) {
      close(n - 1)
      continue
    }
    const i = took.get(n)
    if (i === undefined) {
      open ??= n
      continue
    }
    close(n - 1)
    const r = marks[i]
    const id = str(r.fields.receipt_id)
    const fate = marks.find(
      (m) =>
        m !== r &&
        m.kind === "receipt" &&
        str(m.fields.receipt_id) === id &&
        ["delivered", "dropped", "deferred"].includes(str(m.fields.state))
    )
    const what = str(r.fields.input) || "input"
    const end = !fate
      ? ""
      : str(fate.fields.state) === "delivered"
        ? ` · delivered into ${fate.run_id ? turnTag(fate.run_id, turns.map(asStatusRow)) : "the running turn"}`
        : ` · ${str(fate.fields.state)}`
    out.push({
      turn: n,
      to: n,
      text: `t${n} · consumed by a ${what}${end}`,
      mark: keys[i],
    })
  }
  close(hi - 1)
  return out
}

/** The tool each parked call id names: the approval.request marks'. */
export function toolsByCall(
  marks: readonly SessionMark[]
): Map<string, string> {
  const out = new Map<string, string>()
  for (const m of marks)
    if (m.kind === "approval.request" && str(m.fields.tool))
      out.set(str(m.fields.call_id), str(m.fields.tool))
  return out
}

/** The header's decision counts: "4 approvals (2 approved · 1 denied ·
 * 1 resolved)"; "" when the session decided nothing. */
export function decisionsWords(
  marks: readonly SessionMark[],
  cut = false
): string {
  const by = new Map<string, number>()
  let n = 0
  for (const m of marks) {
    if (m.kind !== "approval.decision") continue
    n++
    const o = outcomeOf(decisionOf(m).c.outcome).word
    by.set(o, (by.get(o) ?? 0) + 1)
  }
  if (!n) return ""
  const parts = [...by].map(([w, k]) => `${k} ${w}`)
  return `${floorOf(cut)}${plural(n, "approval")} (${parts.join(" · ")})`
}

/** A count over a cut mark list (the timeline's truncated hole: the
 * oldest 2,000 kept) is a floor: "≥ ". */
export const floorOf = (cut: boolean) => (cut ? "≥ " : "")

/** cutOf says the timeline document was cut at its mark cap. */
export const cutOf = (
  doc: { holes?: readonly { hole: string }[] } | undefined
) => !!doc?.holes?.some((h) => h.hole === "truncated")

/** The header's grant count; "" when none was minted. */
export function grantsWords(
  marks: readonly SessionMark[],
  cut = false
): string {
  const n = marks.filter((m) => m.kind === "grant.mint").length
  return n ? `${floorOf(cut)}${plural(n, "grant")}` : ""
}

/** The shared time axis: every turn's bar as fractions of the session's
 * span (start → the last finish or last-seen). */
export function axisOf(turns: readonly TimelineTurn[]): {
  start: number
  span: number
} {
  const t0 = Math.min(...turns.map((t) => Date.parse(t.started)))
  const t1 = Math.max(
    ...turns.map((t) => Date.parse(t.finished ?? t.last_seen))
  )
  if (!Number.isFinite(t0) || !Number.isFinite(t1)) return { start: 0, span: 0 }
  return { start: t0, span: Math.max(t1 - t0, 1) }
}

/** A turn's bar on the axis: left and width in percent (a sliver at
 * least, so a 2 ms turn still shows). */
export function barOf(
  t: TimelineTurn,
  axis: { start: number; span: number }
): { left: number; width: number } {
  if (!axis.span) return { left: 0, width: 100 }
  const a = Date.parse(t.started)
  const b = Date.parse(t.finished ?? t.last_seen)
  const left = ((a - axis.start) / axis.span) * 100
  const width = Math.max(((b - a) / axis.span) * 100, 0.6)
  return {
    left: Math.min(Math.max(left, 0), 99.4),
    width: Math.min(width, 100 - Math.min(left, 99.4)),
  }
}

/** One row of the drawer: its label, its value, the value's title. */
export interface DetailRow {
  k: string
  v: string
  title?: string
  /** A hole a reader cannot see past (lib/decisions.ts's WHO_HOLE). */
  hidden?: boolean
  /** A value no record holds (lib/decisions.ts's whoUnrecorded: not
   * yet, or a stored resume that names no actor), its badge's words. */
  unrecorded?: { reason: string; fix: string }
}

/** A link the drawer offers. */
export interface DetailLink {
  label: string
  link: RunLink | SessionLink
}

/** markDetail is a mark's drawer: who / when / via / key / grant /
 * entry id and the other ids it names, and the links to the parking
 * and resuming steps, the turn it is filed under and the sessions it
 * names (UX-B-01's focus layer). */
export function markDetail(
  m: SessionMark,
  turns: readonly TimelineTurn[],
  tools: ReadonlyMap<string, string> = new Map()
): { rows: DetailRow[]; links: DetailLink[] } {
  const f = m.fields
  const rows: DetailRow[] = []
  const links: DetailLink[] = []
  const tag = (run: string) => turnTag(run, turns.map(asStatusRow))
  const row = (k: string, v: string, title?: string) => {
    if (v) rows.push({ k, v, title })
  }
  if (m.kind === "approval.decision") {
    const { c, d } = decisionOf(m, tools.get(str(f.call_id)))
    const who = decider(c, d)
    const hole = markWhoHole(c, d, m.content_mark === "stripped")
    if (who) row("who", who)
    else if (hole === "hidden") rows.push({ k: "who", v: "", hidden: true })
    else if (hole) {
      const to = resumeOfTurn(m.run_id, turns)
      rows.push({
        k: "who",
        v: "",
        unrecorded: whoUnrecorded(to && tag(to.run_id)),
      })
    }
    row("outcome", outcomeOf(c.outcome).word)
    if (c.reason) row("reason", c.reason)
    if (c.result) row("result", c.result)
  } else if (str(m.content?.who)) {
    row("who", str(m.content?.who))
  } else if (m.content_hidden) {
    rows.push({ k: "who", v: "", hidden: true })
  }
  const when = m.fact_at ?? m.at
  row("when", absoluteTime(when), when)
  // The record time only when it reads otherwise than the fact's.
  if (m.fact_at && absoluteTime(m.fact_at) !== absoluteTime(m.at))
    row("recorded", absoluteTime(m.at), m.at)
  row("via", str(f.via))
  row("key", str(f.key_id))
  row("grant", str(f.grant_id))
  row("tool", str(f.tool) || tools.get(str(f.call_id)) || "")
  row("call", str(f.call_id))
  row("entry", m.entry ?? "")
  row("request entry", str(f.request_entry))
  row("receipt", str(f.receipt_id) !== m.entry ? str(f.receipt_id) : "")
  if (m.kind === "compaction" || m.kind === "branch") {
    const u = f.summarizer_usage as
      { input_tokens?: number; output_tokens?: number } | undefined
    if (u) row("summarizer", tokenPair(u.input_tokens, u.output_tokens))
  }
  row(
    "filed under",
    !m.run_id
      ? SESSION_LEVEL
      : turns.some((t) => t.run_id === m.run_id)
        ? tag(m.run_id)
        : unlistedRun(m.run_id)
  )
  row("record", `${m.event} · content ${m.content_mark}`)

  // The links: the parking step (the parked run's last step, where the
  // call was made), the resuming turn, the turn it is filed under.
  const parked = m.run_id ? turns.find((t) => t.run_id === m.run_id) : undefined
  const call = str(f.call_id)
  if (m.lane === "approvals" && parked && call) {
    // A resume that parks again before any step (core's resolvePending:
    // steps 0) parked on its pre-step card, not on a step.
    links.push({
      label: `parked in ${tag(parked.run_id)} · ${parked.steps > 0 ? `step ${parked.steps - 1}` : PARKED_BEFORE_STEP}`,
      link: parkedCallLink(parked, call),
    })
    const to = resumeOfTurn(parked.run_id, turns)
    if (to)
      links.push({
        label: `resumed in ${tag(to.run_id)}`,
        link: runLink(to.run_id, { call, resumed: true }),
      })
  } else if (m.run_id) {
    links.push({
      label: `filed under ${tag(m.run_id)}`,
      link: runLink(m.run_id),
    })
  }
  for (const k of ["by_run", "minted_run"]) {
    const r = str(f[k])
    if (r && turns.some((t) => t.run_id === r))
      links.push({
        label: `${k === "by_run" ? "by" : "ran as"} ${tag(r)}`,
        link: runLink(r),
      })
  }
  for (const k of ["child_session", "new_session"]) {
    const s = str(f[k])
    if (s) links.push({ label: `open session ${s}`, link: sessionLink(s) })
  }
  return { rows, links }
}

/** A park before any step (a resume that re-parked in resolvePending). */
export const PARKED_BEFORE_STEP = "parked before step 0"

/** The turn stepper's words (UX-B-21): a turn's run page walks its
 * session turn by turn — { and } (the run page's [ ] walk steps). */
export const TURN_STEPPER = {
  /** The position among the session's recorded turns: i is one more
   * than the turns recorded below it, so a session whose turn numbers
   * skip (a turn number no run recorded) puts t6 fourth — said as
   * such, never "turn 4" on t6. */
  of: (i: number, n: number, turn?: number) =>
    turn && turn !== i
      ? `t${turn} · ${i} of ${n} recorded turns`
      : `turn ${i} of ${n}`,
  prev: "the previous turn of this session ({)",
  next: "the next turn of this session (})",
  crumb: "sessions",
}

/** The marks under each turn (run id → indices into doc.marks), the
 * session-level ones (loose: run_id null) and those filed under a run
 * the page does not list (unlisted). */
export function marksByTurn(doc: Pick<TimelineDoc, "marks" | "turns">): {
  byRun: Map<string, number[]>
  loose: number[]
  unlisted: number[]
} {
  const runs = new Set(doc.turns.map((t) => t.run_id))
  const byRun = new Map<string, number[]>()
  const loose: number[] = []
  const unlisted: number[] = []
  doc.marks.forEach((m, i) => {
    if (!m.run_id) loose.push(i)
    else if (!runs.has(m.run_id)) unlisted.push(i)
    else {
      const l = byRun.get(m.run_id) ?? []
      l.push(i)
      byRun.set(m.run_id, l)
    }
  })
  return { byRun, loose, unlisted }
}

/** The session page's footer: the experiments forked from its turns —
 * a link to the table, where they nest under their source turn. */
export const forksWords = (n: number) =>
  `${n} ${n === 1 ? "experiment" : "experiments"} started from these turns`
export const FORKS_TITLE =
  "the table view lists each experiment under the turn it started from"

// ── U15.2: lanes, ends, ids, forks, the context lane ────────────────

/** The timeline's lanes (timeline.go's laneOf), in the filter's order,
 * with their chips' words and titles. */
export const LANES: { lane: string; word: string; title: string }[] = [
  {
    lane: "approvals",
    word: "approvals",
    title: "parked calls, their decisions and refused signatures",
  },
  {
    lane: "grants",
    word: "grants",
    title: "grants minted, used, revoked or lapsed",
  },
  {
    lane: "compaction",
    word: "compaction",
    title: "session compactions, uncompacts and pins",
  },
  {
    lane: "steer_queue",
    word: "steer / queue",
    title:
      "busy-policy receipts (queued, delivered, dropped), interrupts and rollbacks",
  },
  {
    lane: "branch_fork",
    word: "branch / fork",
    title: "branches, forks and labels",
  },
  { lane: "pool", word: "pool", title: "delegated sessions (thread/pool)" },
  {
    lane: "other",
    word: "other",
    title: "marks of a kind this Studio does not know",
  },
]

/** The lane filter's note: a filter dims, it never hides. */
export const LANES_NOTE =
  "lanes: a filter dims the other lanes' marks — it never hides one"

/** The filter's "every lane" chip. */
export const ALL_LANES = "all lanes"

/** parseLanes reads ?lanes= back: the known lanes it names, undefined
 * for none (every lane bright). */
export function parseLanes(v: string | undefined): Set<string> | undefined {
  if (!v) return undefined
  const known = new Set(LANES.map((l) => l.lane))
  const out = new Set(v.split(",").filter((l) => known.has(l)))
  return out.size ? out : undefined
}

/** lanesParam is a lane set as ?lanes= writes it: LANES' order, ""
 * for none (every lane). */
export function lanesParam(set: ReadonlySet<string>): string {
  return LANES.map((l) => l.lane)
    .filter((l) => set.has(l))
    .join(",")
}

/** toggleLane is the filter after a chip's click. */
export function toggleLane(
  set: ReadonlySet<string> | undefined,
  lane: string
): string {
  const next = new Set(set ?? [])
  if (next.has(lane)) next.delete(lane)
  else next.add(lane)
  return lanesParam(next)
}

/** How a busy policy ended a turn (UX-B-09): words, never a failure's. */
export const END_WORDS = {
  interrupted: ENDED_WORDS.interrupt,
  rolled_back: ENDED_WORDS.rollback,
  /** The run page's words before (or without) the timeline's mark. */
  byNewer: endedText("interrupt", "a newer message"),
  /** The run header's error block's label on such a run: the error is
   * the cancel the policy caused, not a failure. */
  label: "ended",
  /** The disclosure over such a turn's raw error on the timeline. */
  errorWord: "the cancel it caused",
  /** A call the cancel cut off (the story's "never completed"). */
  call: "cut off",
  callTitle:
    "tool_start was recorded but no tool_finish: a newer message ended the turn (thread's busy policy) while the call ran",
}

/** The branch mark's words (UX-B-08): a Branch that moved the leaf,
 * and one to the entry the leaf already was on (from_entry ==
 * to_entry: nothing left the line). */
export const BRANCH_WORDS = {
  moved: "branched · the leaf moved off this line",
  stayed: "branched to the entry the leaf was on · no line was left",
}

/** A turn off the session's current path (lib/turn-chips.ts's). */
export { OFF_PATH }

/** The fork mark's words. */
export const FORKED_INTO = "forked into"

/** A turn a busy policy ended: its chip's words and the hover. */
export interface TurnEnd {
  kind: "interrupted" | "rolled_back"
  /** The turn whose send ended it. */
  by?: string
  text: string
  title: string
  /** The interrupt mark's key (?mark=); "" when no mark was read. */
  mark: string
}

/** thread's cancel cause for a turn its Interrupt or Rollback busy
 * policy ended (lib/turn-chips.ts's, shared with the panel). */
export { BUSY_CAUSE }

/** endedByPolicy says a run was ended by a newer message on purpose:
 * its context cancelled by thread's busy policy, never a failure. */
export const endedByPolicy = (r?: { cancel_cause?: string } | null) =>
  r?.cancel_cause === BUSY_CAUSE

/** The end of a run the timeline's marks were not read for: an
 * interrupt by an unnamed newer message (a rollback cannot be told
 * apart without its mark). */
export const UNNAMED_END: TurnEnd = {
  kind: "interrupted",
  text: END_WORDS.byNewer,
  title:
    "a newer message (thread's Interrupt or Rollback busy policy) cancelled this turn: its context was cancelled on purpose, not a failure — the session's timeline names which",
  mark: "",
}

/** An end's glyph (lib/turn-chips.ts's): ↶ a rollback, ✗ an interrupt. */
export const endGlyph = (end: Pick<TurnEnd, "kind">) =>
  endedGlyph(end.kind === "rolled_back" ? "rollback" : "interrupt")

/** The story's end line for such a run: "■ interrupted by t7 at step
 * 0" — never "the stream ended without run_finish". */
export const endLine = (end: TurnEnd, step?: number) =>
  `■ ${end.text}${step === undefined ? "" : ` at step ${step}`}`

/** busyEndSpans redraws a busy-ended run's own rows (the waterfall's
 * run, step and call rows of runId): a failure's or a never-finished
 * tone becomes the neutral step tone and a failed badge the end's word
 * — red stays for real failures (UX-B-09). */
export function busyEndSpans<
  T extends { runId: string; tone: string; badge?: string; kind: string },
>(rows: T[], runId: string, end: TurnEnd | undefined): T[] {
  if (!end) return rows
  const word = END_WORDS[end.kind]
  return rows.map((r) =>
    r.runId === runId &&
    (r.tone === "bad" || r.tone === "error" || r.tone === "never")
      ? {
          ...r,
          tone: "step",
          badge:
            r.kind === "tool"
              ? END_WORDS.call
              : r.badge && /^(error|failed|cancelled)\b/.test(r.badge)
                ? word
                : r.badge,
        }
      : r
  )
}

/** The hovers: what an interrupt and a rollback did. */
export const END_TITLES = {
  interrupted:
    "a newer message (thread's Interrupt busy policy) cancelled this turn: its context was cancelled on purpose, not a failure — its entries stay on the path",
  rolled_back:
    "a newer message (thread's Rollback busy policy) cancelled this turn and took its entries off the path: the model's later turns do not see it",
}

/** turnEnds reads the interrupt marks (thread's weft.thread.interrupt,
 * filed under the felled run): the turns a busy policy ended, by run. */
export function turnEnds(
  marks: readonly SessionMark[],
  turns: readonly TimelineTurn[]
): Map<string, TurnEnd> {
  const keys = markKeys(marks)
  const out = new Map<string, TurnEnd>()
  marks.forEach((m, i) => {
    if (m.kind !== "interrupt" || !m.run_id || out.has(m.run_id)) return
    const kind =
      str(m.fields.policy) === "rollback" ? "rolled_back" : "interrupted"
    const by = str(m.fields.by_run) || undefined
    const tag = by ? turnTag(by, turns.map(asStatusRow)) : ""
    out.set(m.run_id, {
      kind,
      by,
      // lib/turn-chips.ts's words: the panel's chip is this text
      // behind its glyph.
      text: endedText(str(m.fields.policy), tag || "a newer message"),
      title: END_TITLES[kind],
      mark: keys[i],
    })
  })
  return out
}

/** The header's turn-number line (UX-B-10): "turn ids t1–t9 · 7 ran ·
 * 2 receipts never ran" — "" when the numbers have no gap. */
export function turnIdsWords(
  turns: readonly TimelineTurn[],
  marks: readonly SessionMark[]
): string {
  if (turns.length < 2) return ""
  const gaps = turnGaps(turns, marks)
  if (!gaps.length) return ""
  const nums = turns.map((t) => t.turn)
  const receipts = gaps.filter((g) => g.mark).length
  const unrecorded = gaps
    .filter((g) => g.reason)
    .reduce((n, g) => n + g.to - g.turn + 1, 0)
  const parts = [
    `turn ids ${gapRange(Math.min(...nums), Math.max(...nums))}`,
    `${new Set(nums).size} ran`,
  ]
  if (receipts)
    parts.push(
      `${receipts} ${receipts === 1 ? "receipt" : "receipts"} never ran`
    )
  if (unrecorded) parts.push(`${unrecorded} not recorded`)
  return parts.join(" · ")
}

/** The turn-number line's hover. */
export const TURN_IDS_TITLE =
  "thread mints a turn number for every input it accepts while a turn runs; a steer delivered into that turn, or dropped, never runs as its own turn — the timeline names the receipt that took each number"

/** forkWords is a fork's state as the origin reads it (UX-B-07). */
export function forkWords(f: {
  turns: number
  status?: string
  hidden?: boolean
  unknown?: boolean
}): string {
  if (f.hidden) return "its state is not your token's to read"
  if (f.unknown) return "not looked up"
  if (!f.turns) return "no turn recorded yet"
  return plural(f.turns, "turn")
}

/** The forked session's page cannot open before it runs a turn. */
export const FORK_NO_PAGE =
  "the fork ran no turn yet: this Studio has no page for it until it does (thread records the fork on the origin only)"

/** forkOf reads a fork's lineage, weft.session.forked_from
 * (`<origin session>#<entry id>`, record contract §2). */
export function forkOf(
  meta: Readonly<Record<string, string>> | undefined
): { session: string; entry: string } | undefined {
  const v = meta?.["weft.session.forked_from"]
  if (!v) return undefined
  const i = v.lastIndexOf("#")
  return i > 0
    ? { session: v.slice(0, i), entry: v.slice(i + 1) }
    : { session: v, entry: "" }
}

/** The lineage line of a forked session or turn (UX-B-07): "⑃ fork of
 * <origin> at entry e9 ("Plan a 4-day trip…")" — the origin a link
 * between FORK_OF and forkAt's words. */
export const FORK_OF = "⑃ fork of"
export const forkAt = (entry: string, prompt?: string) =>
  [
    entry ? `at entry ${entry}` : "",
    prompt
      ? `(“${prompt.length > 60 ? `${prompt.slice(0, 59)}…` : prompt}”)`
      : "",
  ]
    .filter(Boolean)
    .join(" ")

/** forkPoint is the origin's turn a fork was taken from, as the
 * origin's timeline names it: the fork mark naming this session (and
 * the entry, when known) is filed under the run whose line was forked —
 * its prompt the fork point's words. undefined when no mark says. */
export function forkPoint(
  origin: Pick<TimelineDoc, "marks" | "turns"> | undefined,
  self: string,
  entry: string
): TimelineTurn | undefined {
  const m = origin?.marks.find(
    (x) =>
      x.kind === "fork" &&
      str(x.fields.new_session) === self &&
      (!entry || !x.entry || x.entry === entry)
  )
  return m?.run_id
    ? origin?.turns.find((t) => t.run_id === m.run_id)
    : undefined
}

/** The origin's header line: the sessions it forked. */
export const forkedIntoWords = (n: number) =>
  `⑃ ${FORKED_INTO} ${n === 1 ? "1 session" : `${n} sessions`}`

/** The session page's live line (UX-B-22). */
export const LIVE_WORDS = {
  on: "● live",
  running: "● live · a turn is running",
  down: "○ live stream lost · reconnecting",
  title:
    "the page follows this session's runs (/api/live?session=): a new turn, a step, a park or an end refetches the turns and the marks",
}

/** The context lane's words (UX-B-42). */
export const CONTEXT_LANE = {
  eyebrow: "context per turn",
  title:
    "each turn's input tokens (every step's prompt, summed) with the session compactions filed under it: tokens before → after, by reason; the summarizer's calls are priced apart, never in the turn's usage",
  startedOn: "started on a compacted context",
  reasons:
    "reasons: trim (old tool results cleared), threshold (the context window's limit), manual (Compact), overflow (a provider's context error re-ran once), from_hook (BeforeCompact replaced it)",
}

/** One session compaction as the context lane reads it. */
export interface ContextMark {
  key: string
  reason: string
  before: number
  after: number
  replaced: number
  entries: number
  /** The marker's hash (the run document's compaction names it). */
  hash: string
}

/** One session compaction at its boundary (UX-B-12): the run it is
 * filed under and the turn whose requests first ran on it. */
export interface CompactionStart {
  mark: ContextMark
  /** The run the marker is filed under. */
  from: string
  /** The turn that started on the compacted context: the first turn to
   * start at or after the marker (by start time — after a Branch back
   * the marker is filed under an older turn on the line, never "the
   * next turn number"); the filed-under run itself for a held marker.
   * undefined while no turn started on it. */
  on?: string
  /** Filed under the run that started on it: thread held it (no run of
   * the session produced the compacted context) and emitted it as that
   * run reported — the marker landed inside its own run's span, or
   * before that run started. */
  held: boolean
}

/** isoKey is an RFC 3339 time as [ms since the epoch, ns past the ms]:
 * Date.parse keeps milliseconds, Go's RFC3339Nano writes nanoseconds —
 * a marker 0.9 ms after its run's finish must still read after it. */
function isoKey(iso: string): [number, number] {
  const m = /^(.*T\d\d:\d\d:\d\d)(?:\.(\d+))?(Z|[+-]\d\d:\d\d)$/.exec(iso)
  if (!m) return [Date.parse(iso), 0]
  const frac = (m.at(2) ?? "").padEnd(9, "0").slice(0, 9)
  return [
    Date.parse(`${m[1]}.${frac.slice(0, 3)}${m[3]}`),
    Number(frac.slice(3)),
  ]
}

/** isoCompare orders two RFC 3339 times to the nanosecond. */
export function isoCompare(a: string, b: string): number {
  const x = isoKey(a)
  const y = isoKey(b)
  return x[0] - y[0] || x[1] - y[1]
}

const contextMark = (m: SessionMark, key: string): ContextMark => ({
  key,
  reason: str(m.fields.reason),
  before: num(m.fields.tokens_before),
  after: num(m.fields.tokens_after),
  replaced: num(m.fields.replaced),
  entries: num(m.fields.entries),
  hash: str(m.fields.hash),
})

/** compactionStarts places every session compaction at its boundary:
 * filed under the run that produced the context (ADR 0028 §8), first
 * seen by the turn that started after it — or, held, by the run it is
 * filed under. */
export function compactionStarts(
  turns: readonly TimelineTurn[],
  marks: readonly SessionMark[]
): CompactionStart[] {
  const keys = markKeys(marks)
  const byStart = [...turns].sort((a, b) => isoCompare(a.started, b.started))
  const out: CompactionStart[] = []
  marks.forEach((m, i) => {
    if (m.kind !== "compaction" || !m.run_id) return
    const own = turns.find((t) => t.run_id === m.run_id)
    // Held: the marker is not after the run it is filed under — it
    // landed inside its span (emitted as the run reported), or before
    // the run started at all. Either way that run started on it.
    const held = !!own && (!own.finished || isoCompare(m.at, own.finished) <= 0)
    const on = held
      ? own.run_id
      : byStart.find(
          (t) => t.run_id !== m.run_id && isoCompare(t.started, m.at) >= 0
        )?.run_id
    out.push({ mark: contextMark(m, keys[i]), from: m.run_id, on, held })
  })
  return out
}

/** One turn of the context lane. */
export interface ContextRow {
  run_id: string
  turn: number
  input: number
  /** The compactions filed under it that came after it (a held one is
   * in started instead: it came before its requests). */
  marks: ContextMark[]
  /** The compactions this turn started on: every one since the turn
   * before it (t5: t4's trim and the manual one). */
  started: CompactionStart[]
}

/** contextRows is the context lane (UX-B-42): every turn's input
 * tokens with the session compactions filed under it, and the ones it
 * started on (compactionStarts). */
export function contextRows(
  turns: readonly TimelineTurn[],
  marks: readonly SessionMark[]
): ContextRow[] {
  const starts = compactionStarts(turns, marks)
  const sorted = [...turns].sort((a, b) => a.turn - b.turn)
  return sorted.map((t) => ({
    run_id: t.run_id,
    turn: t.turn,
    input: t.usage.input_tokens,
    marks: starts
      .filter((s) => s.from === t.run_id && !s.held)
      .map((s) => s.mark),
    started: starts.filter((s) => s.on === t.run_id),
  }))
}

/** A context mark as one line: "⌗ trim 11.1k → 6.7k". */
export function contextMarkLine(c: ContextMark): string {
  const toks =
    c.before && c.after ? ` ${tokens(c.before)} → ${tokens(c.after)}` : ""
  return `⌗ ${c.reason || "compaction"}${toks}`
}

/** startedFrom is where the compactions a turn started on came from:
 * "compacted after t4", or — held — "compacted before this turn" (no
 * run of the session produced the context). */
export const startedFrom = (
  s: CompactionStart,
  tag: (run: string) => string
) => (s.held ? "compacted before this turn" : `compacted after ${tag(s.from)}`)

/** startedWords: "started on a compacted context · compacted after t4:
 * ⌗ trim 9.4k → 6.1k · ⌗ manual 6.1k → 2.7k" — every compaction since
 * the turn before, grouped by the run each is filed under. */
export function startedWords(
  starts: readonly CompactionStart[],
  tag: (run: string) => string
): string {
  const groups = new Map<string, CompactionStart[]>()
  for (const s of starts) {
    const k = startedFrom(s, tag)
    groups.set(k, [...(groups.get(k) ?? []), s])
  }
  const parts = [...groups].map(
    ([from, l]) =>
      `${from}: ${l.map((s) => contextMarkLine(s.mark)).join(" · ")}`
  )
  return [CONTEXT_LANE.startedOn, ...parts].join(" · ")
}
