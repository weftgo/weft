// Approval decisions in one vocabulary (U13.2; UX-B-03, UX-H-17,
// UX-H-20, UX-S3-04): the verbs both surfaces' buttons, titles, toasts
// and live page say — approve · deny · resolve — and the outcome a
// decided parked call reads, from the run document's decided / resumes
// blocks (studio/decisions.go): "✋ denied · agent-7 · 'need a photo of
// the damage' → t2" on the parked turn, the "resumed from t1" card
// before the resume's step 0. Studio's parked card and resumed card and
// the panel's approvals block and turn view draw these words alone
// (parity.test.ts holds both).
import type { DecidedCall, DecisionDoc, SessionMark } from "./api"
import { absoluteTime, clockTime, plural, spanMs } from "./format"
import type { TONE } from "./palette"

/** The three decision verbs, as every button and toast says them. */
export const VERB = {
  approve: "approve",
  deny: "deny",
  resolve: "resolve…",
} as const

export type Verb = keyof typeof VERB

/** Each verb's title: what it does. */
export const VERB_TITLE: Record<Verb, string> = {
  approve: "Approve: the handler runs for real",
  deny: "Deny: the model sees a denied result, the text its reason",
  resolve: "Resolve: the text is the tool's result; the handler never runs",
}

/** The live page's word on a parked run's cancel cell. */
export const PARKED_LIVE = `a parked run is over until its pending calls are decided: ${VERB.approve}, ${VERB.deny} or ${VERB.resolve} on its page`

/** The one field beside the verbs: a resolve's result, a deny's reason. */
export const DECIDE_FIELD = "result to resolve with · reason to deny"

/** A sent decision's word once its command finished. */
export const DONE: Record<string, string> = {
  approve: "approved",
  deny: "denied",
  resolve: "resolved",
}

/** Each outcome's mark, word and tone (lib/palette.ts's TONE): an
 * approval a result, a denial or a lapse error-as-data, a resolve a
 * value from outside. An outcome this build does not know reads
 * verbatim, as a result. */
export const OUTCOME: Record<
  string,
  { icon: string; word: string; tone: keyof typeof TONE }
> = {
  approved: { icon: "✓", word: "approved", tone: "result" },
  denied: { icon: "✋", word: "denied", tone: "error" },
  expired: { icon: "⌛", word: "expired", tone: "error" },
  no_decision: { icon: "✋", word: "denied: no decision", tone: "error" },
  resolved: { icon: "↩", word: "resolved", tone: "tool" },
}

export const outcomeOf = (o: string) =>
  OUTCOME[o] ?? { icon: "·", word: o, tone: "result" as const }

/** A denial's reason or a resolve's result the reader cannot see: the
 * badge (A3) with its reason and fix, by the call's reason_badge or
 * result_badge ("" fix: none applies). */
export const REASON_HOLE: Record<string, { reason: string; fix: string }> = {
  hidden: {
    reason:
      "a reason or result is content: a read-scoped token does not read it",
    fix: "",
  },
  not_recorded: {
    reason: "the reason or result was not captured: content was off",
    fix: "record with content on",
  },
}

/** Who decided, when only thread's content names it (no one actor in
 * metadata: none, several, or a split verdict's two sides) and the
 * reader may not read content: the hidden badge. */
export const WHO_HOLE = {
  reason: "who decided is content: a read-scoped token does not read it",
  fix: "",
}

/** Who decided, on a state-log decision mark nothing names a decider
 * for (hole not_recorded): no stored turn resumed the run yet — its
 * actor names the decider, never the parked run's own — and the record
 * carries no who or key. */
export const WHO_UNRECORDED = {
  reason:
    "who decided is not recorded yet: no stored turn resumed this run, and the decision names no who or key",
  fix: "resume the run with weft.approval.actor set, or record with content on",
}

/** The hole a decision mark's who reads as when nothing names its
 * decider: hidden to a reader that may not read content, else
 * not_recorded (WHO_UNRECORDED) while no resume's actor is stored;
 * undefined when one is named (a resume's several deciders, actor_of
 * "resume", name the resume's, not a hole), when no person decided (a
 * grant, an approver, an expiry, an interrupt: via says what), or when the
 * mark is stripped (its record badge says so). */
export function markWhoHole(
  c: DecidedCall,
  d: DecisionDoc,
  stripped = false
): "hidden" | "not_recorded" | undefined {
  if (decider(c, d)) return undefined
  if (c.who_hidden) return "hidden"
  if (stripped || d.actor || NO_PERSON.has(c.via ?? "")) return undefined
  return "not_recorded"
}

/** The ways a call is decided with no person to name (thread's via). */
const NO_PERSON = new Set(["grant", "approver", "expiry", "interrupt"])

/** A block whose calls no record names (hole not_recorded): no
 * weft.resume.calls, no pre-step result, no decided span. */
export const CALLS_HOLE = {
  reason: "no record names the decided calls",
  fix: "record the resume with a current weft core",
}

/** A parked run whose own marks (GET /entries?run=, past the timeline's
 * cap) are more than one read holds (hole truncated): a request expiry
 * past it is not read. */
export const RUN_MARKS_HOLE = {
  reason:
    "this run's state-log marks are more than one read holds (1,000): a request expiry past them is not read",
  fix: "page the rest: GET /api/sessions/{id}/entries?run=<run>&offset=1000",
}

/** The decider a call names: thread's who, else the signing key, else
 * the resume's one actor (weft.approval.actor — several are the run's,
 * not this call's; nor is it a split verdict's denier, or a decider the
 * server hid). "" when nothing names one. */
export function decider(c: DecidedCall, d: DecisionDoc): string {
  return (
    c.who ||
    c.key_id ||
    (d.actor && !d.actor.includes(",") && !c.who_hidden && !c.approved_by
      ? d.actor
      : "")
  )
}

/** One decided call's line: "✋ denied · agent-7 · 'need a photo' → t2"
 * — the outcome, the decider, a denial's reason (quoted; absent when
 * hidden or not captured: the badge says that) and, given, the turn the
 * decision resumed in. A split verdict names both sides: "✋ denied by
 * bob · approved by alice". */
export function decisionLine(
  c: DecidedCall,
  d: DecisionDoc,
  to?: string
): string {
  return decisionWords(c, d) + (to ? ` → ${to}` : "")
}

/** decisionWords is decisionLine without the turn (the devtools
 * panel draws that one as its own button). */
export function decisionWords(c: DecidedCall, d: DecisionDoc): string {
  const o = outcomeOf(c.outcome)
  const who = decider(c, d)
  const parts = c.approved_by
    ? [
        `${o.icon} ${o.word}${who && ` by ${who}`}`,
        `${OUTCOME.approved.word} by ${c.approved_by}`,
      ]
    : [`${o.icon} ${o.word}`, who]
  // An expired row says "expired" once: thread's reason opens with it.
  const why =
    c.outcome === "expired" ? c.reason?.replace(/^expired: /, "") : c.reason
  if (why) parts.push(`'${why}'`)
  return parts.filter(Boolean).join(" · ")
}

/** How and when: "via signed · key k_alice · grant e_… · <time>" — the
 * decision's own time when a record names one, else the resume's start;
 * time the surface's clock (Studio's absoluteTime, local with its zone;
 * the panel's UTC by default). */
export function decisionDetail(
  c: DecidedCall,
  d: DecisionDoc,
  time: (iso: string) => string = (iso) => `${iso.slice(11, 19)} UTC`
): string {
  return [
    c.via && `via ${c.via}`,
    c.key_id && c.key_id !== c.who && `key ${c.key_id}`,
    c.grant_id && `grant ${c.grant_id}`,
    time(c.at || d.at),
  ]
    .filter(Boolean)
    .join(" · ")
}

/** The resume card's head: "resumed from t1 · 1 decided call". */
export function resumedHead(d: DecisionDoc, from: string): string {
  const n = d.calls.length + (d.more ?? 0)
  return `resumed from ${from} · ${plural(n, "decided call")}`
}

/** The block's honesty line, when it has one: a failed read, the
 * server's notes (a read beside it that failed, records past a cap),
 * calls past the cap, or outcomes derived rather than recorded. */
export function decisionsNote(d: DecisionDoc, resume: string): string {
  if (d.error) return `the decisions could not be read: ${d.error}`
  return [
    ...(d.notes ?? []),
    d.more ? `${plural(d.more, "more call")} not listed` : "",
    d.calls.some((c) => c.derived)
      ? d.source === "spans"
        ? `derived from ${resume}'s spans: a resolved call has none`
        : `derived from ${resume}'s pre-step results: a resolve reads approved`
      : "",
  ]
    .filter(Boolean)
    .join(" · ")
}

/** What an approved or resolved call left: the resumed card's "→ …"
 * line under its row on both surfaces — step 0 no longer draws the
 * call. An approval's result is the fold's pre-step call's; a resolve's
 * the server's (core runs no handler for it: no tool event to fold),
 * its result_badge the badge beside the row when it is not readable.
 * "" when none. */
export function decidedResult(
  c: DecidedCall,
  calls: { callId: string; result?: { content: string } }[] | undefined
): string {
  return c.outcome === "resolved"
    ? (c.result ?? "")
    : c.outcome === "approved"
      ? (calls?.find((x) => x.callId === c.call_id)?.result?.content ?? "")
      : ""
}

/** decidedCall finds one parked call's decision in a block. */
export const decidedCall = (d: DecisionDoc | undefined, id?: string) =>
  d?.calls.find((c) => c.call_id === id)

/** withoutDecided drops from a resume's steps the pre-step calls (the
 * fold's resumed calls, executed before step 0's model call) the
 * resumed card lists — never inside step 0 — and only those: a call the
 * block does not list (past its cap, a failed or unrecorded read) stays
 * where the fold put it, and a call that owns a child run keeps its
 * block (the story's). A run with no resumes block is unchanged. */
export function withoutDecided<
  TStep extends {
    toolCalls: { callId: string; resumed?: boolean; childRunId?: string }[]
  },
  TFold extends { steps: TStep[] },
>(f: TFold, doc: { resumes?: DecisionDoc } | null | undefined): TFold {
  const ids = new Set(doc?.resumes?.calls.map((c) => c.call_id))
  const keep = (c: TStep["toolCalls"][number]) =>
    !c.resumed || !!c.childRunId || !ids.has(c.callId)
  return {
    ...f,
    steps: f.steps.map((s) =>
      s.toolCalls.every(keep)
        ? s
        : { ...s, toolCalls: s.toolCalls.filter(keep) }
    ),
  }
}

/** requestExpiries is each parked call's request expiry (UX-B-41):
 * the approval.request records filed under runID whose expiry field
 * (thread.RequestExpiry, RFC 3339) is set, by call id. Empty when the
 * session's state log was not read or no request has one. */
export function requestExpiries(
  marks: readonly SessionMark[],
  runID: string
): Map<string, string> {
  const out = new Map<string, string>()
  for (const m of marks) {
    if (m.kind !== "approval.request" || m.run_id !== runID) continue
    const call = m.fields.call_id
    const exp = m.fields.expiry
    if (typeof call === "string" && typeof exp === "string" && exp)
      out.set(call, exp)
  }
  return out
}

/** expiryWords is a parked call's expiry beside its "awaiting a
 * decision": "expires 09:57:56 (in 40.0s)" before it lapses, "⏱
 * expired 09:57:56 — denied at the next touch (lazy expiry)" after —
 * thread denies an expired request when the session is next touched
 * (a Decide, a Send, a Resume), never on a timer. title: the absolute
 * stamp and that rule. */
export function expiryWords(
  expiry: string,
  now: number
): { text: string; title: string; lapsed: boolean } {
  const at = Date.parse(expiry)
  const lapsed = !Number.isNaN(at) && at <= now
  const title = `the request expires ${absoluteTime(expiry)} (thread.RequestExpiry): an expired request is denied the next time the session is touched — a decide, a send, a resume — never on a timer`
  return lapsed
    ? {
        text: `⏱ expired ${clockTime(expiry, false)} — denied at the next touch (lazy expiry)`,
        title,
        lapsed,
      }
    : {
        text: `expires ${clockTime(expiry, false)} (in ${spanMs(at - now)})`,
        title,
        lapsed,
      }
}
