// Approval decisions in one vocabulary (U13.2; UX-B-03, UX-H-17,
// UX-H-20, UX-S3-04): the verbs both surfaces' buttons, titles, toasts
// and live page say — approve · deny · resolve — and the outcome a
// decided parked call reads, from the run document's decided / resumes
// blocks (studio/decisions.go): "✋ denied · agent-7 · 'need a photo of
// the damage' → t2" on the parked turn, the "resumed from t1" card
// before the resume's step 0. Studio's parked card and resumed card and
// the panel's approvals block and turn view draw these words alone
// (parity.test.ts holds both).
import type { DecidedCall, DecisionDoc } from "./api"
import { plural } from "./format"
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

/** A denial's reason the reader cannot see: the badge (A3) with its
 * reason and fix, by the block's reason_badge ("" fix: none applies). */
export const REASON_HOLE: Record<string, { reason: string; fix: string }> = {
  hidden: {
    reason: "a deny reason is content: a read-scoped token does not read it",
    fix: "",
  },
  not_recorded: {
    reason: "the deny reason was not captured: content was off",
    fix: "record with content on",
  },
}

/** Who decided, when only thread's content names it (no actor
 * metadata) and the reader may not read content: the hidden badge. */
export const WHO_HOLE = {
  reason: "who decided is content: a read-scoped token does not read it",
  fix: "",
}

/** A block whose calls no record names (hole not_recorded): no
 * weft.resume.calls, no pre-step result, no decided span. */
export const CALLS_HOLE = {
  reason: "no record names the decided calls",
  fix: "record the resume with a current weft core",
}

/** The decider a call names: thread's who, else the signing key, else
 * the resume's one actor (weft.approval.actor — several are the run's,
 * not this call's). "" when nothing names one. */
export function decider(c: DecidedCall, d: DecisionDoc): string {
  return c.who || c.key_id || (d.actor && !d.actor.includes(",") ? d.actor : "")
}

/** One decided call's line: "✋ denied · agent-7 · 'need a photo' → t2"
 * — the outcome, the decider, a denial's reason (quoted; absent when
 * hidden or not captured: the badge says that) and, given, the turn the
 * decision resumed in. */
export function decisionLine(
  c: DecidedCall,
  d: DecisionDoc,
  to?: string
): string {
  const o = outcomeOf(c.outcome)
  const parts = [`${o.icon} ${o.word}`, decider(c, d)]
  // An expired row says "expired" once: thread's reason opens with it.
  const why =
    c.outcome === "expired" ? c.reason?.replace(/^expired: /, "") : c.reason
  if (why) parts.push(`'${why}'`)
  return parts.filter(Boolean).join(" · ") + (to ? ` → ${to}` : "")
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

/** What an approved or resolved call left (the fold's pre-step call's
 * result): the resumed card's "→ …" line under its row on both
 * surfaces — step 0 no longer draws the call. "" when none. */
export function decidedResult(
  c: DecidedCall,
  calls: { callId: string; result?: { content: string } }[] | undefined
): string {
  return c.outcome === "approved" || c.outcome === "resolved"
    ? (calls?.find((x) => x.callId === c.call_id)?.result?.content ?? "")
    : ""
}

/** decidedCall finds one parked call's decision in a block. */
export const decidedCall = (d: DecisionDoc | undefined, id: string) =>
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
