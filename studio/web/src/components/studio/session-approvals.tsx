// The session's approvals view (U15.2-fix; UX-B-14, UX-S3-11):
// ?view=approvals on the session page — every decision thread recorded
// (the call, its tool, the outcome, who / via / key, the grant, when,
// the turn that parked it and the one that resumed it; refused signed
// decisions beside their call) and every grant (its tool and rule, its
// scope, its uses with their calls, revoked / expired / live). Each row
// opens its mark on the timeline. lib/approvals-view.ts's words.
import { Link } from "@tanstack/react-router"
import { Fragment } from "react"

import type { SessionMark, TimelineTurn } from "@/lib/api"
import {
  APPROVALS,
  decisionRows,
  grantRows,
  grantScope,
  grantState,
  rowOutcome,
  usesWord,
} from "@/lib/approvals-view"
import type { DecisionRow, GrantRow } from "@/lib/approvals-view"
import { WHO_HOLE } from "@/lib/decisions"
import { absoluteTime } from "@/lib/format"
import { runLink, sessionViewLink } from "@/lib/links"
import { turnTag } from "@/lib/status"
import { THREAD_LINK, TONE_TEXT } from "@/lib/status-class"
import { clockTime } from "@/lib/timeline"
import { HoleBadge } from "@/components/studio/hole-badge"

export function SessionApprovals({
  sessionID,
  turns,
  marks,
}: {
  sessionID: string
  turns: readonly TimelineTurn[]
  marks: readonly SessionMark[]
}) {
  const decisions = decisionRows(marks, turns)
  const grants = grantRows(marks)
  const rows = turns.map((t) => ({
    id: t.run_id,
    status: t.status,
    turn: t.turn,
  }))
  const tag = (run: string) => turnTag(run, rows)
  if (!decisions.length && !grants.length)
    return (
      <p
        className="py-6 text-center font-mono text-xs text-muted-foreground"
        data-approvals-empty
      >
        {APPROVALS.empty}
      </p>
    )
  return (
    <div className="space-y-4" data-session-approvals>
      {decisions.length ? (
        <section className="space-y-1.5" aria-labelledby="approvals-decisions">
          <h2 id="approvals-decisions" className="eyebrow">
            {APPROVALS.decisions} · {decisions.length}
          </h2>
          <div className="overflow-x-auto rounded-lg border bg-background">
            <table className="w-full min-w-[40rem] text-left font-mono text-[11px]">
              <thead className="text-muted-foreground">
                <tr className="border-b">
                  {Object.values(APPROVALS.heads).map((h) => (
                    <th key={h} scope="col" className="px-2 py-1 font-normal">
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody className="divide-y">
                {decisions.map((r) => (
                  <DecisionTr
                    key={r.key}
                    r={r}
                    sessionID={sessionID}
                    tag={tag}
                  />
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}
      {grants.length ? (
        <section className="space-y-1.5" aria-labelledby="approvals-grants">
          <h2 id="approvals-grants" className="eyebrow">
            {APPROVALS.grants} · {grants.length}
          </h2>
          <div className="overflow-x-auto rounded-lg border bg-background">
            <table className="w-full min-w-[40rem] text-left font-mono text-[11px]">
              <thead className="text-muted-foreground">
                <tr className="border-b">
                  {Object.values(APPROVALS.grantHeads).map((h) => (
                    <th key={h} scope="col" className="px-2 py-1 font-normal">
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody className="divide-y">
                {grants.map((g) => (
                  <GrantTr key={g.id} g={g} sessionID={sessionID} tag={tag} />
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}
      <p className="font-mono text-[11px] text-muted-foreground">
        {APPROVALS.note}
      </p>
    </div>
  )
}

function DecisionTr({
  r,
  sessionID,
  tag,
}: {
  r: DecisionRow
  sessionID: string
  tag: (run: string) => string
}) {
  const o = rowOutcome(r)
  return (
    <tr data-decision={r.call} data-outcome={r.outcome || "pending"}>
      <td className="px-2 py-1">
        <Link
          {...sessionViewLink(sessionID, { mark: r.key })}
          className={THREAD_LINK}
          title="open the mark on the timeline"
        >
          {r.call}
        </Link>
      </td>
      <td className="px-2 py-1 text-ev-tool">{r.tool || "—"}</td>
      <td className="px-2 py-1">
        <span className={TONE_TEXT[o.tone]}>
          <span aria-hidden>{o.icon} </span>
          {o.word}
        </span>
        {r.c?.reason ? (
          <span className="text-muted-foreground"> · ‘{r.c.reason}’</span>
        ) : null}
        {r.refused.length ? (
          <div
            className="text-muted-foreground"
            title={APPROVALS.refusedTitle}
            data-refused={r.refused.length}
          >
            {APPROVALS.refused(r.refused.length)}:{" "}
            {r.refused.map((x, i) => (
              <Fragment key={x.markKey}>
                {i ? ", " : ""}
                {x.reason}
                {x.key ? ` (${x.key})` : ""}
              </Fragment>
            ))}
          </div>
        ) : null}
      </td>
      <td className="px-2 py-1">
        {r.whoHidden ? (
          <HoleBadge hole="hidden" reason={WHO_HOLE.reason} />
        ) : (
          r.who || "—"
        )}
      </td>
      <td className="px-2 py-1 break-all">{r.grant || "—"}</td>
      <td className="px-2 py-1 tabular-nums" title={absoluteTime(r.at)}>
        {clockTime(r.at)}
      </td>
      <td className="px-2 py-1 whitespace-nowrap">
        {r.parked ? (
          <Link
            {...runLink(r.parked, { call: r.call })}
            className={THREAD_LINK}
          >
            {tag(r.parked)}
          </Link>
        ) : (
          "—"
        )}
        {" → "}
        {r.resumed ? (
          <Link
            {...runLink(r.resumed, { call: r.call, resumed: true })}
            className={THREAD_LINK}
          >
            {tag(r.resumed)}
          </Link>
        ) : (
          "—"
        )}
      </td>
    </tr>
  )
}

function GrantTr({
  g,
  sessionID,
  tag,
}: {
  g: GrantRow
  sessionID: string
  tag: (run: string) => string
}) {
  return (
    <tr data-grant={g.id}>
      <td className="px-2 py-1 break-all">
        <Link
          {...sessionViewLink(sessionID, { mark: g.key })}
          className={THREAD_LINK}
          title="open the mark on the timeline"
        >
          {g.id}
        </Link>
        {g.reason ? (
          <div className="text-muted-foreground">‘{g.reason}’</div>
        ) : null}
      </td>
      <td className="px-2 py-1 text-ev-tool">{g.tool || "—"}</td>
      <td className="px-2 py-1">
        {[g.rule, grantScope(g)].filter(Boolean).join(" · ") || "—"}
      </td>
      <td className="px-2 py-1">
        <span className="tabular-nums">{usesWord(g)}</span>
        {g.uses.length ? (
          <span className="text-muted-foreground">
            {" · "}
            {g.uses.map((u, i) => (
              <Fragment key={u.key}>
                {i ? ", " : ""}
                {u.run ? (
                  <Link
                    {...runLink(u.run, { call: u.call })}
                    className={THREAD_LINK}
                  >
                    {u.call} ({tag(u.run)})
                  </Link>
                ) : (
                  u.call
                )}
                {u.outcome && u.outcome !== "approved" ? ` ${u.outcome}` : ""}
              </Fragment>
            ))}
          </span>
        ) : null}
      </td>
      <td className="px-2 py-1">{grantState(g)}</td>
    </tr>
  )
}
