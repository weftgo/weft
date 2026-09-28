// The capabilities seam (ADR 0018 §8): api/meta reports what the
// backing server carries; the open handler reports none. Every
// deployment-specific screen gates on a named capability through this
// one hook — no hostname checks, no build flags.
import { useQuery } from "@tanstack/react-query"

import { metaQuery } from "@/lib/api"

export function useCapabilities() {
  const meta = useQuery(metaQuery())
  const caps = meta.data?.capabilities ?? []
  const has = (name: string) => caps.includes(name)
  return { caps, has, loading: meta.isPending }
}
