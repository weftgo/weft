// The experiments pages without the playground (plan F5): the
// experiments are its routes, so they are off with it — meta's
// capabilities_off says why. And under a panel token: the experiments
// routes answer 403 to every panel token (studio/experiments.go —
// the saved matrices are the whole Studio's, not one public id's), so
// they are not offered there and, reached by URL, read nothing.
import { studioToken } from "@/lib/api"
import { tokenScopeOf } from "@/lib/replay"
import { HoleBadge } from "@/components/studio/hole-badge"

/** The page without the playground: the experiments are its routes. */
export function NoExperiments({ why }: { why?: string }) {
  return (
    <div className="mx-auto max-w-lg space-y-2 py-24 text-center">
      <p className="text-sm">This Studio has no playground, so no experiments.</p>
      {why ? (
        <p className="text-xs text-muted-foreground" data-experiments-why>
          {why}
        </p>
      ) : null}
    </div>
  )
}

/** Whether this bearer may read the experiments: under the playground
 * capability, any bearer but a panel token (read or playground). */
export function mayReadExperiments(capabilities: readonly string[], token = studioToken()): boolean {
  return capabilities.includes("playground") && tokenScopeOf(token) === ""
}

/** The reason the hidden badge gives under a panel token. */
export const EXPERIMENTS_HIDDEN_REASON =
  "the saved experiments are the whole Studio's: a panel token may not read them"

/** The page under a panel token: the hidden badge, nothing fetched. */
export function ExperimentsHidden() {
  return (
    <div className="mx-auto max-w-lg space-y-2 py-24 text-center" data-experiments-hidden>
      <p className="text-sm">Experiments</p>
      <HoleBadge hole="hidden" detail note="dev token only" reason={EXPERIMENTS_HIDDEN_REASON} />
    </div>
  )
}
