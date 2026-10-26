// The manifest_check badges (U20.1, UX-S4-01): /api/meta's check drawn
// where the question is asked — on an Agents card, whether the latest
// run of the agent ran this manifest ("stale · the latest run ran
// d2151022, not this manifest"), and in the run header, whether the run
// ran the registered manifest. Words and roles: lib/runtimes.ts.
import { useQuery, useQueryClient } from "@tanstack/react-query"

import type { RuntimeView } from "@/lib/api"
import { metaQuery, runtimesQuery } from "@/lib/api"
import {
  MANIFEST_WORDS,
  manifestMark,
  registeredWeft,
  runManifestMark,
  runManifestTitle,
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
  weftVersion,
}: {
  agent?: string
  hash?: string
  /** The run's weft version: a stale mark's title names a weft
   * version change against the registered build's (U23.1-fix). */
  weftVersion?: string
}) {
  const meta = useQuery(metaQuery())
  const mark = runManifestMark(agent, hash, meta.data?.manifest_check)
  if (!mark) return null
  if (mark === "stale")
    return <StaleRunMark agent={agent} weftVersion={weftVersion} />
  return <RunMark mark={mark} title={runManifestTitle(mark)} />
}

/** StaleRunMark names a weft version change when the registered
 * builds (/api/runtimes) are already in the cache — read, never
 * subscribed: the shell's hook is the page's one reader of the key. */
function StaleRunMark({
  agent,
  weftVersion,
}: {
  agent?: string
  weftVersion?: string
}) {
  const cached = useQueryClient().getQueryData<{ runtimes: RuntimeView[] }>(
    runtimesQuery().queryKey
  )
  const title = runManifestTitle(
    "stale",
    weftVersion,
    registeredWeft(agent, cached?.runtimes)
  )
  return <RunMark mark="stale" title={title} />
}

function RunMark({
  mark,
  title,
}: {
  mark: "current" | "stale"
  title: string
}) {
  const w = MANIFEST_WORDS[mark]
  return (
    <span
      className={statusClass(w.role).text}
      title={title}
      data-run-manifest-mark={mark}
    >
      {" "}
      · {w.word}
    </span>
  )
}
