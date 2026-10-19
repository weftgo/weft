// A forked session's lineage line (U15.2-fix, UX-B-07): "⑃ fork of
// <origin> at entry e9 ("Plan a 4-day trip…")" — the origin a link, the
// fork point's prompt read from the origin's timeline (its fork mark is
// filed under the run whose line was forked) when this identity may
// read it; without it the line names the entry alone.
import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"

import { metaQuery, timelineQuery } from "@/lib/api"
import { sessionLink } from "@/lib/links"
import { THREAD_LINK } from "@/lib/status-class"
import { FORK_OF, forkAt, forkPoint } from "@/lib/timeline"

export function ForkOfLine({
  origin,
  entry,
  self,
  className,
}: {
  /** The origin session (weft.session.forked_from's session). */
  origin: string
  /** The origin's entry the fork was taken at. */
  entry: string
  /** This (the forked) session's id: the fork mark names it. */
  self?: string
  className?: string
}) {
  const meta = useQuery(metaQuery())
  const tl = useQuery({
    ...timelineQuery(origin),
    enabled: !!self && !!meta.data?.capabilities.includes("timeline"),
    retry: false,
  })
  const at = self ? forkPoint(tl.data, self, entry) : undefined
  return (
    <span className={className} data-fork-of={origin}>
      <span>{FORK_OF}</span>{" "}
      <Link {...sessionLink(origin)} className={`font-mono ${THREAD_LINK}`}>
        {origin}
      </Link>{" "}
      <span data-fork-point={at?.run_id}>
        {forkAt(entry, at?.prompt_preview)}
      </span>
    </span>
  )
}
