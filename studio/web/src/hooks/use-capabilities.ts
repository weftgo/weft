// The capabilities seam (ADR 0018 §8): api/meta reports what the
// backing server carries; the open handler reports none. Every
// deployment-specific screen gates on a named capability through this
// one hook — no hostname checks, no build flags. why(name) is meta's
// capabilities_off reason for an absent one (plan B4): the option and
// CLI flag that turned it off, for the screen's empty state.
import { useQuery } from "@tanstack/react-query"

import { metaQuery } from "@/lib/api"

export function useCapabilities() {
  const meta = useQuery(metaQuery())
  const caps = meta.data?.capabilities ?? []
  const has = (name: string) => caps.includes(name)
  const why = (name: string): string | undefined =>
    has(name) ? undefined : meta.data?.capabilities_off?.[name]
  return { caps, has, why, loading: meta.isPending }
}
