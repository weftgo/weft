// The "resumed from tN" card (U13.2; UX-B-03): a resume run's decided
// calls — the parked turn's, each with its outcome, decider and reason
// (lib/decisions.ts) — drawn before step 0, never inside it: the
// approved calls ran before the model's first call, and step 0 no
// longer draws the ones this card lists (withoutDecided) — so an
// approved or resolved row carries what the call left ("→ …",
// decidedResult). The panel's turn view draws the same card
// (parity.test.ts).
import { Link } from "@tanstack/react-router"

import { DecisionRow, DecisionsNote } from "@/components/studio/parked-calls"
import type { RunDoc } from "@/lib/api"
import { decidedResult, resumedHead } from "@/lib/decisions"
import type { FoldedToolCall } from "@/lib/events"
import { runLink } from "@/lib/links"
import { turnTag } from "@/lib/status"

export function ResumedCard({
  doc,
  calls,
}: {
  doc: RunDoc
  /** The fold's pre-step calls (resumed): an approved call's result. */
  calls?: FoldedToolCall[]
}) {
  const d = doc.resumes
  if (!d) return null
  const from = turnTag(d.run)
  return (
    <div
      className="rounded-lg border border-ev-error/30 px-4 py-2 text-xs"
      data-resumed-card={d.run}
    >
      <Link
        {...runLink(d.run)}
        className="eyebrow underline decoration-thread-ink underline-offset-2 hover:decoration-2"
        data-resumed-from={d.run}
      >
        {resumedHead(d, from)}
      </Link>
      <ul className="mt-1 space-y-1">
        {d.calls.map((c) => {
          const ran = decidedResult(c, calls)
          return (
            <li
              key={c.call_id}
              className="font-mono"
              data-resumed-call={c.call_id}
            >
              {c.name || c.call_id}
              <DecisionRow c={c} d={d} />
              {ran ? (
                <div className="truncate text-faint" data-resumed-result="">
                  → {ran}
                </div>
              ) : null}
            </li>
          )
        })}
      </ul>
      <DecisionsNote d={d} resume={turnTag(doc.id)} />
    </div>
  )
}
