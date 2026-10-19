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
import type {
  DecidedCall,
  DecisionDoc,
  RunRow,
  SessionMark,
  TimelineDoc,
  TimelineTurn,
} from "./api"
import { decider, decisionLine, outcomeOf } from "./decisions"
import { compactionLine } from "./compaction"
import { absoluteTime, plural, tokens } from "./format"
import { runLink, sessionLink } from "./links"
import type { RunLink, SessionLink, SessionView } from "./links"
import type { TONE } from "./palette"
import { turnTag } from "./status"

/** The drawer's closing line: the marks are thread's records, an index
 * of the session file — the file is the record (ADR 0021 §5). */
export const TRAIL_NOTE =
  "the trail is an index of the session log, not evidence — ADR 0021"

/** The session page's views, in the switcher's order, with titles. */
export const VIEWS: { view: SessionView; word: string; title: string }[] = [
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
]

/** The tree view until it is drawn (U15.2's lanes and tree). */
export const TREE_PENDING =
  "the branch and fork tree is not drawn on this page yet — the timeline lists every turn and every branch, fork and label mark in order"

/** The timeline's read failed: the turns still draw, from the session. */
export const timelineUnread = (why: string) =>
  `the session's state log could not be read (${why}): the turns are drawn without their marks`

/** The timeline is off on this Studio (meta's capability). */
export const TIMELINE_OFF =
  "this Studio does not read a session's state log (capability timeline is off): the turns are drawn without their marks"

/** A session-level mark's heading: filed under no run (held at Close). */
export const SESSION_LEVEL =
  "filed under no turn (held until the session closed)"

/** markKey is a mark's key in ?mark=: its thread entry id when that is
 * unique among the marks, else m<index> (an interrupt names no entry). */
export function markKeys(marks: readonly SessionMark[]): string[] {
  const seen = new Map<string, number>()
  for (const m of marks)
    if (m.entry) seen.set(m.entry, (seen.get(m.entry) ?? 0) + 1)
  return marks.map((m, i) =>
    m.entry && seen.get(m.entry) === 1 ? m.entry : `m${i}`
  )
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
    who_hidden: m.content_hidden || undefined,
    reason: cs("reason") || undefined,
    result: cs("content") || undefined,
    at: m.fact_at ?? m.at,
  }
  return {
    c,
    d: { run: m.run_id ?? "", at: m.at, source: "thread", calls: [c] },
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
      const by = str(f.by_run)
      const word = str(f.policy) === "rollback" ? "rolled back" : "interrupted"
      return {
        icon: "✗",
        text: `${word}${by ? ` by ${tag(by)}` : ""}${num(f.denied_calls) ? ` · ${plural(num(f.denied_calls), "call")} denied` : ""}`,
        tone: "bad",
      }
    }
    case "rewind":
      return {
        icon: "↶",
        text: `rewound · ${str(f.reason)}`,
        tone: "error",
      }
    case "branch":
      return {
        icon: "⑂",
        text: `branched${f.summarized ? " · the left branch summarized" : ""}`,
        tone: "step",
      }
    case "fork":
      return {
        icon: "⑃",
        text: `forked into ${str(f.new_session)}`,
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

const asStatusRow = (t: TimelineTurn) => ({
  id: t.run_id,
  status: t.status,
  turn: t.turn,
})

/** A skipped turn number (UX-E-19): thread mints a turn number for an
 * input it accepts while a turn runs; a steer delivered into that turn
 * (or dropped) never names a run. The words say which receipt took it. */
export interface TurnGap {
  turn: number
  text: string
  /** The receipt mark's key that consumed it, when one did. */
  mark?: string
}

/** turnGaps lists the turn numbers between a session's first and last
 * turn that no run carries, each with the receipt that took it. */
export function turnGaps(
  turns: readonly TimelineTurn[],
  marks: readonly SessionMark[]
): TurnGap[] {
  if (turns.length === 0) return []
  const have = new Set(turns.map((t) => t.turn))
  const lo = Math.min(...have)
  const hi = Math.max(...have)
  const keys = markKeys(marks)
  const out: TurnGap[] = []
  for (let n = lo + 1; n < hi; n++) {
    if (have.has(n)) continue
    const i = marks.findIndex(
      (m) => m.kind === "receipt" && num(m.fields.turn) === n
    )
    if (i < 0) {
      out.push({
        turn: n,
        text: `t${n} · no run of this turn number was recorded`,
      })
      continue
    }
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
      text: `t${n} · consumed by a ${what}${end}`,
      mark: keys[i],
    })
  }
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
export function decisionsWords(marks: readonly SessionMark[]): string {
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
  return `${plural(n, "approval")} (${parts.join(" · ")})`
}

/** The header's grant count; "" when none was minted. */
export function grantsWords(marks: readonly SessionMark[]): string {
  const n = marks.filter((m) => m.kind === "grant.mint").length
  return n ? plural(n, "grant") : ""
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
    if (who) row("who", who)
    else if (c.who_hidden) rows.push({ k: "who", v: "", hidden: true })
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
    if (u)
      row(
        "summarizer",
        `${tokens(u.input_tokens ?? 0)} in / ${tokens(u.output_tokens ?? 0)} out`
      )
  }
  row("filed under", m.run_id ? tag(m.run_id) : SESSION_LEVEL)
  row("record", `${m.event} · content ${m.content_mark}`)

  // The links: the parking step (the parked run's last step, where the
  // call was made), the resuming turn, the turn it is filed under.
  const parked = m.run_id ? turns.find((t) => t.run_id === m.run_id) : undefined
  const call = str(f.call_id)
  if (m.lane === "approvals" && parked && call) {
    const step = Math.max(parked.steps - 1, 0)
    links.push({
      label: `parked in ${tag(parked.run_id)} · step ${step}`,
      link: runLink(parked.run_id, { step, call }),
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

/** The turn stepper's words (UX-B-21): a turn's run page walks its
 * session turn by turn — { and } (the run page's [ ] walk steps). */
export const TURN_STEPPER = {
  of: (i: number, n: number) => `turn ${i} of ${n}`,
  prev: "the previous turn of this session ({)",
  next: "the next turn of this session (})",
  crumb: "sessions",
}

/** turnSiblings places a session turn among its session's own turns
 * (turn order): its neighbours, its 1-based position and the count;
 * null when the run is not one of them. */
export function turnSiblings<T extends { id: string; turn: number }>(
  run: { id: string },
  turns: readonly T[]
): { prev?: T; next?: T; index: number; total: number } | null {
  const own = [...turns].sort((a, b) => a.turn - b.turn)
  const i = own.findIndex((t) => t.id === run.id)
  if (i < 0) return null
  return { prev: own[i - 1], next: own[i + 1], index: i + 1, total: own.length }
}

/** The marks under each turn (run id → indices into doc.marks), and the
 * session-level ones (run_id null, or a run not among the turns). */
export function marksByTurn(doc: Pick<TimelineDoc, "marks" | "turns">): {
  byRun: Map<string, number[]>
  loose: number[]
} {
  const runs = new Set(doc.turns.map((t) => t.run_id))
  const byRun = new Map<string, number[]>()
  const loose: number[] = []
  doc.marks.forEach((m, i) => {
    if (m.run_id && runs.has(m.run_id)) {
      const l = byRun.get(m.run_id) ?? []
      l.push(i)
      byRun.set(m.run_id, l)
    } else loose.push(i)
  })
  return { byRun, loose }
}
