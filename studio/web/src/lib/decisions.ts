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
  if (c.reason) parts.push(`'${c.reason}'`)
  return parts.filter(Boolean).join(" · ") + (to ? ` → ${to}` : "")
}

/** How and when: "via signed · key k_alice · grant e_… · 09:00:00 UTC"
 * — the decision's own time when a record names one, else the resume's
 * start (UTC: the same words on both surfaces and in any zone). */
export function decisionDetail(c: DecidedCall, d: DecisionDoc): string {
  return [
    c.via && `via ${c.via}`,
    c.key_id && c.key_id !== c.who && `key ${c.key_id}`,
    c.grant_id && `grant ${c.grant_id}`,
    `${(c.at || d.at).slice(11, 19)} UTC`,
  ]
    .filter(Boolean)
    .join(" · ")
}

/** The resume card's head: "resumed from t1 · 1 decided call". */
export function resumedHead(d: DecisionDoc, from: string): string {
  const n = d.calls.length + (d.more ?? 0)
  return `resumed from ${from} · ${plural(n, "decided call")}`
}

/** The block's honesty line, when it has one: a failed read, calls past
 * the cap, or calls derived from the pre-step results. */
export function decisionsNote(d: DecisionDoc, resume: string): string {
  if (d.error) return `the decisions could not be read: ${d.error}`
  return [
    d.more ? `${plural(d.more, "more call")} not listed` : "",
    d.source === "transcript"
      ? `derived from ${resume}'s pre-step results: a resolve reads approved`
      : "",
  ]
    .filter(Boolean)
    .join(" · ")
}

/** decidedCall finds one parked call's decision in a block. */
export const decidedCall = (d: DecisionDoc | undefined, id: string) =>
  d?.calls.find((c) => c.call_id === id)

/** withoutDecided drops a resume's pre-step calls (the fold's resumed
 * calls: the decided ones, executed before step 0's model call) from
 * its steps once the resumed card draws them — never inside step 0 —
 * keeping a call that owns a child run (its block is the story's). A
 * run with no resumes block is unchanged. */
export function withoutDecided<
  TStep extends { toolCalls: { resumed?: boolean; childRunId?: string }[] },
  TFold extends { steps: TStep[] },
>(f: TFold, doc: { resumes?: DecisionDoc } | null | undefined): TFold {
  if (!doc?.resumes) return f
  const keep = (c: TStep["toolCalls"][number]) => !c.resumed || !!c.childRunId
  return {
    ...f,
    steps: f.steps.map((s) =>
      s.toolCalls.every(keep)
        ? s
        : { ...s, toolCalls: s.toolCalls.filter(keep) }
    ),
  }
}
