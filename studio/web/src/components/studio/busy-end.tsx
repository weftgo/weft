// A turn thread's busy policy ended (U15.2-fix, UX-B-09): a newer
// message interrupted it, or rolled it back. Its run failed with a
// cancel on purpose — weft.cancel.cause thread.interrupted — and every
// surface of its run page draws that neutral, naming the turn that
// ended it: red stays for real failures. useBusyEnd reads the session's
// timeline (one cached query, the session page's) for the interrupt
// mark; BusyEndCtx carries the answer to the story's cards.
import { useQuery } from "@tanstack/react-query"
import { createContext, useContext } from "react"

import { metaQuery, timelineQuery } from "@/lib/api"
import type { RunDoc } from "@/lib/api"
import { endedByPolicy, turnEnds, UNNAMED_END } from "@/lib/timeline"
import type { TurnEnd } from "@/lib/timeline"

/** useBusyEnd is how a busy policy ended the run, or undefined: the
 * interrupt mark's words when the session's timeline names it, else
 * the unnamed interrupt (the timeline off, unread or still loading). */
export function useBusyEnd(
  doc: Pick<RunDoc, "id" | "cancel_cause" | "session_id"> | undefined
): TurnEnd | undefined {
  const meta = useQuery(metaQuery())
  const on =
    endedByPolicy(doc) &&
    !!doc?.session_id &&
    !!meta.data?.capabilities.includes("timeline")
  const tl = useQuery({ ...timelineQuery(doc?.session_id ?? ""), enabled: on })
  if (!doc || !endedByPolicy(doc)) return undefined
  return (
    (tl.data
      ? turnEnds(tl.data.marks, tl.data.turns).get(doc.id)
      : undefined) ?? UNNAMED_END
  )
}

/** The run's busy end under its story (RunCards provides it). */
export const BusyEndCtx = createContext<TurnEnd | undefined>(undefined)

export const useBusyEndCtx = () => useContext(BusyEndCtx)
