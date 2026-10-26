// The manifest_check badges (U20.1, UX-S4-01): /api/meta's check drawn
// where the question is asked — on an Agents card, whether the latest
// run of the agent ran this manifest ("stale · the latest run ran
// d2151022, not this manifest"), and in the run header, whether the run
// ran the registered manifest. Words and roles: lib/runtimes.ts.
import { useQuery } from "@tanstack/react-query"

import { metaQuery } from "@/lib/api"
import {
  MANIFEST_WORDS,
  RUN_MANIFEST_TITLE,
  manifestMark,
  runManifestMark,
} from "@/lib/runtimes"
import { statusClass } from "@/lib/status-class"

const CHIP =
  "cursor-help rounded-full border border-current px-1.5 py-px font-mono text-[10px]"

/** ManifestCheckBadge is an Agents card's badge: this registered
 * manifest against the agent's latest run (meta.manifest_check). Absent
 * while meta is unread or reports no check. */
export function ManifestCheckBadge({
  agent,
  hash,
}: {
  agent: string
  hash?: string
}) {
  const meta = useQuery(metaQuery())
  const check = meta.data?.manifest_check
  if (!check) return null
  const { mark, latest } = manifestMark(agent, hash, check)
  const w = MANIFEST_WORDS[mark]
  return (
    <span
      className={`${CHIP} ${statusClass(w.role).text}`}
      title={w.title(latest)}
      data-manifest-mark={mark}
    >
      {w.word}
      <span className="sr-only">{` — ${w.title(latest)}`}</span>
    </span>
  )
}

/** RunManifestMark is the run header's word after "manifest <hash>":
 * current or stale against the agent's registered manifest
 * (lib/runtimes.ts's runManifestMark, from meta.manifest_check). Nothing
 * when the check cannot tell. */
export function RunManifestMark({
  agent,
  hash,
}: {
  agent?: string
  hash?: string
}) {
  const meta = useQuery(metaQuery())
  const mark = runManifestMark(agent, hash, meta.data?.manifest_check)
  if (!mark) return null
  const w = MANIFEST_WORDS[mark]
  return (
    <span
      className={statusClass(w.role).text}
      title={RUN_MANIFEST_TITLE[mark]}
      data-run-manifest-mark={mark}
    >
      {" "}
      · {w.word}
    </span>
  )
}
