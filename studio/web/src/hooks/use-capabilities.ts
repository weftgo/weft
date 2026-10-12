// The capabilities seam (ADR 0018 §8): api/meta reports what the
// backing server carries; the open handler reports none. Every
// deployment-specific screen gates on a named capability through this
// one hook — no hostname checks, no build flags. why(name) is meta's
// capabilities_off reason for an absent one (plan B4): the option and
// CLI flag that turned it off, for the screen's empty state.
import { useQuery, useQueryClient } from "@tanstack/react-query"

import { metaQuery } from "@/lib/api"
import { heldError } from "@/lib/query"

export function useCapabilities() {
  const qc = useQueryClient()
  const meta = useQuery(metaQuery())
  const caps = meta.data?.capabilities ?? []
  const has = (name: string) => caps.includes(name)
  const why = (name: string): string | undefined =>
    has(name) ? undefined : meta.data?.capabilities_off?.[name]
  // error: meta's failed read, held in the cache (lib/query.ts's
  // heldError) while it is read again (pending once more, its error
  // gone) — a page says it could not read the capabilities, never that
  // one is off. "" once meta answered.
  const error = meta.data
    ? ""
    : (meta.error?.message ?? heldError(qc, metaQuery().queryKey))
  // loading is the first read only: a meta that failed is read again
  // by every new observer (its data never came), and each refetch puts
  // the query back to pending — never loading again then, or a page
  // that waits on it (the run page's event walk) starts over per mount.
  // An observer that mounted during such a read reads the held error at
  // once; with none held it waits for that read rather than call a
  // capability off.
  return {
    caps,
    has,
    why,
    loading: meta.isPending && (meta.errorUpdateCount === 0 || !error),
    error,
  }
}
