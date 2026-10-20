// A session's approvals in one place (U15.2-fix; UX-B-14, UX-S3-11):
// every decision thread's state log recorded — the call, its tool, the
// outcome, who / via / key / grant, when, the turn that parked it and
// the turn that resumed it — the signed decisions it refused, and every
// grant: minted (its tool, scope, max uses, expiry, a deny grant, one
// minted by "approve always"), each use with its call, revoked or
// expired. Read from GET /api/sessions/{id}/timeline's marks (thread's
// approval.* and grant.* records, record contract §4) in
// lib/decisions.ts's words; and, on a run page, the execute_tool span's
// weft.tool.approved_by / weft.grant.id as a call's chip. An index of
// the session log, not evidence (ADR 0021 §5). Studio only: the panel
// hands off to this view.
import type { DecidedCall, SessionMark, TimelineTurn } from "./api"
import {
  decisionDetail,
  decider,
  markWhoHole,
  outcomeOf,
  whoUnrecorded,
} from "./decisions"
import type { OUTCOME } from "./decisions"
import { absoluteTime, plural } from "./format"
import { AWAITING, turnTag } from "./status"
import {
  asStatusRow,
  decisionOf,
  markKeys,
  resumeOfTurn,
  toolsByCall,
} from "./timeline"

const str = (v: unknown) => (typeof v === "string" ? v : "")
const num = (v: unknown) => (typeof v === "number" ? v : 0)

/** The view's words. */
export const APPROVALS = {
  decisions: "decisions",
  grants: "grants",
  empty:
    "no approval or grant recorded on this session: no call of it parked, and no grant was minted",
  pending: AWAITING,
  refused: (n: number) => `${plural(n, "signed decision")} refused`,
  refusedTitle:
    "decisions thread refused before they counted — a bad or replayed signature, a retired key, changed arguments",
  heads: {
    call: "call",
    tool: "tool",
    outcome: "outcome",
    who: "who / via",
    grant: "grant",
    when: "when",
    turns: "parked → resumed",
  },
  grantHeads: {
    grant: "grant",
    tool: "tool",
    rule: "rule",
    uses: "uses",
    state: "state",
  },
  note: "the trail is thread's state log, an index of the session file, not evidence — ADR 0021",
}

/** One decision row (or a parked call no decision answered yet). */
export interface DecisionRow {
  /** The mark's key (?mark= on the timeline). */
  key: string
  call: string
  tool: string
  /** lib/decisions.ts's outcome (approved · denied · resolved ·
   * expired · …), "" for a call still awaiting one. */
  outcome: string
  c?: DecidedCall
  /** "who · via signed · key … · grant …" in decisionDetail's words
   * (the time apart). */
  who: string
  /** Who is content the identity may not read. */
  whoHidden: boolean
  /** Nothing names the decider (lib/decisions.ts's whoUnrecorded: no
   * stored turn resumed the run yet, or its resume names no actor; no
   * who or key recorded), the badge's words. */
  whoUnrecorded?: { reason: string; fix: string }
  grant: string
  at: string
  /** The turn that parked the call (the run the decision is filed
   * under), and the one that resumed it. */
  parked?: string
  resumed?: string
  /** approve always: a grant minted from it. */
  always: boolean
  /** The signed decisions refused for this call. */
  refused: { reason: string; key: string; markKey: string }[]
}

/** decisionRows lists the session's decisions in record order, each
 * call's refused signatures beside it, and the parked calls no decision
 * answered (outcome "": awaiting one). */
export function decisionRows(
  marks: readonly SessionMark[],
  turns: readonly TimelineTurn[]
): DecisionRow[] {
  const keys = markKeys(marks)
  const tools = toolsByCall(marks)
  // A call a grant decided parked no request: its tool is the grant's.
  const grantTool = new Map<string, string>()
  for (const m of marks)
    if (m.kind.startsWith("grant.") && str(m.fields.tool))
      grantTool.set(str(m.fields.grant_id), str(m.fields.tool))
  const out: DecisionRow[] = []
  const refused = new Map<string, DecisionRow["refused"]>()
  marks.forEach((m, i) => {
    if (m.kind !== "approval.refused") return
    const call = str(m.fields.call_id)
    refused.set(call, [
      ...(refused.get(call) ?? []),
      {
        reason: str(m.fields.reason).replaceAll("_", " "),
        key: str(m.fields.key_id),
        markKey: keys[i],
      },
    ])
  })
  const decided = new Set<string>()
  marks.forEach((m, i) => {
    if (m.kind !== "approval.decision") return
    const call = str(m.fields.call_id)
    const run = m.run_id ?? ""
    decided.add(`${run}|${call}`)
    const { c, d } = decisionOf(m, tools.get(call))
    const who = decider(c, d)
    const resumer = resumeOfTurn(run || null, turns)?.run_id
    const detail = decisionDetail(c, d, () => "")
      .split(" · ")
      .filter((p) => p && !p.startsWith("grant "))
      .join(" · ")
    out.push({
      key: keys[i],
      call,
      tool: tools.get(call) ?? grantTool.get(str(m.fields.grant_id)) ?? "",
      outcome: c.outcome,
      c,
      who: [who, detail].filter(Boolean).join(" · "),
      whoHidden: !who && !!c.who_hidden,
      whoUnrecorded:
        markWhoHole(c, d, m.content_mark === "stripped") === "not_recorded"
          ? whoUnrecorded(resumer && turnTag(resumer, turns.map(asStatusRow)))
          : undefined,
      grant: [str(m.fields.grant_id), str(m.fields.grant_scope)]
        .filter(Boolean)
        .join(" · "),
      at: c.at || m.at,
      parked: run || undefined,
      resumed: resumer,
      always: !!m.fields.always,
      refused: refused.get(call) ?? [],
    })
  })
  // A request no decision answered: still awaiting one.
  marks.forEach((m, i) => {
    if (m.kind !== "approval.request") return
    const call = str(m.fields.call_id)
    const run = m.run_id ?? ""
    if (decided.has(`${run}|${call}`)) return
    out.push({
      key: keys[i],
      call,
      tool: str(m.fields.tool),
      outcome: "",
      who: num(m.fields.quorum) > 1 ? `needs ${num(m.fields.quorum)}` : "",
      whoHidden: false,
      grant: "",
      at: m.fact_at ?? m.at,
      parked: run || undefined,
      always: false,
      refused: refused.get(call) ?? [],
    })
  })
  return out
}

/** A decision's outcome words and tone; a call still awaiting one
 * reads "awaiting a decision" in the parked tone. */
export function rowOutcome(r: DecisionRow): {
  icon: string
  word: string
  tone: (typeof OUTCOME)[string]["tone"]
} {
  if (!r.outcome) return { icon: "⏸", word: APPROVALS.pending, tone: "never" }
  const o = outcomeOf(r.outcome)
  return { ...o, word: r.always ? `${o.word} · always` : o.word }
}

/** One grant: what minted it, its uses (each with its call), its end. */
export interface GrantRow {
  id: string
  /** The mint mark's key, else the first mark's. */
  key: string
  tool: string
  /** "deny" for a deny grant; the args globs' count; "approve always". */
  rule: string
  scope: string
  maxUses: number
  expiry: string
  /** The grant's own reason (content). */
  reason: string
  uses: { call: string; outcome: string; run?: string; key: string }[]
  /** revoked · expired (max uses / expiry) · "" while live. */
  state: string
  /** No mint on this session's log: a shared store's grant. */
  shared: boolean
}

/** grantRows lists the session's grants in mint (else first-use)
 * order: each with its uses and its end. */
export function grantRows(marks: readonly SessionMark[]): GrantRow[] {
  const keys = markKeys(marks)
  const by = new Map<string, GrantRow>()
  const order: string[] = []
  const of = (id: string, key: string): GrantRow => {
    let g = by.get(id)
    if (!g) {
      g = {
        id,
        key,
        tool: "",
        rule: "",
        scope: "",
        maxUses: 0,
        expiry: "",
        reason: "",
        uses: [],
        state: "",
        shared: true,
      }
      by.set(id, g)
      order.push(id)
    }
    return g
  }
  marks.forEach((m, i) => {
    const f = m.fields
    const id = str(f.grant_id)
    if (!id || !m.kind.startsWith("grant.")) return
    const g = of(id, keys[i])
    switch (m.kind) {
      case "grant.mint":
        g.key = keys[i]
        g.shared = false
        g.tool = str(f.tool)
        g.scope = str(f.scope)
        g.maxUses = num(f.max_uses)
        g.expiry = str(f.expiry)
        g.reason = str(m.content?.reason)
        g.rule = [
          f.deny ? "deny" : "allow",
          num(f.args) ? plural(num(f.args), "args glob") : "",
          str(f.via) === "always" ? "from approve always" : "",
        ]
          .filter(Boolean)
          .join(" · ")
        break
      case "grant.use":
        g.tool ||= str(f.tool)
        g.scope ||= str(f.scope)
        g.maxUses ||= num(f.max_uses)
        g.uses.push({
          call: str(f.call_id),
          outcome: str(f.outcome),
          run: m.run_id ?? undefined,
          key: keys[i],
        })
        break
      case "grant.revoke":
        g.tool ||= str(f.tool)
        g.state = "revoked"
        break
      case "grant.expire":
        g.tool ||= str(f.tool)
        g.state = `expired · ${str(f.reason).replaceAll("_", " ")}`
        break
    }
  })
  return order.map((id) => by.get(id)!)
}

/** A grant's uses as one cell: "2 of 2" (max uses), else the count. */
export const usesWord = (g: GrantRow) =>
  g.maxUses ? `${g.uses.length} of ${g.maxUses}` : String(g.uses.length)

/** A grant's state cell: its end, else live (with its expiry). */
export const grantState = (g: GrantRow, now = Date.now()) =>
  g.state ||
  (!g.expiry
    ? "live"
    : Date.parse(g.expiry) <= now
      ? // thread records a lapse when a use finds it (grant.expire);
        // none did: the expiry passed unused.
        `expiry passed ${absoluteTime(g.expiry)} · no use after`
      : `live · expires ${absoluteTime(g.expiry)}`)

/** A grant's scope cell; a shared store's grant names no mint here. */
export const grantScope = (g: GrantRow) =>
  g.shared ? `${g.scope || "shared"} · minted outside this session` : g.scope

// ── A call's approval on the run page ───────────────────────────────

/** The execute_tool span's approval facts (U12.1-fix): who let the call
 * run — weft.tool.approved_by user|grant|approver|quorum|policy — and
 * the grant when one did. */
export interface ApprovalFact {
  by: string
  grant?: string
}

/** approvalFactsOf indexes the run's own approved execute_tool spans by
 * call id. */
export function approvalFactsOf(
  spans: readonly { attrs?: Record<string, unknown> }[] | null | undefined,
  runId: string
): Map<string, ApprovalFact> {
  const out = new Map<string, ApprovalFact>()
  for (const sp of spans ?? []) {
    const a = sp.attrs ?? {}
    const call = str(a["gen_ai.tool.call.id"])
    const by = str(a["weft.tool.approved_by"])
    if (
      a["gen_ai.operation.name"] !== "execute_tool" ||
      a["weft.run.id"] !== runId ||
      !call ||
      !by
    )
      continue
    out.set(call, { by, grant: str(a["weft.grant.id"]) || undefined })
  }
  return out
}

/** The call's chip: "✓ approved · by grant e_…" / "✓ approved · by
 * approver". */
export const APPROVED_BY = {
  chip: (f: ApprovalFact) =>
    `✓ approved · by ${f.by}${f.grant ? ` ${f.grant}` : ""}`,
  title:
    "the execute_tool span's weft.tool.approved_by (and weft.grant.id): what let this call run — a user's decision, a grant, the approver, a quorum or a policy",
}
