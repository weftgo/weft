// One QueryClient for the SPA (plan §4.5). A module singleton: the
// app is a single-page tool with no SSR, so there is exactly one
// client per page load and router loaders can ensureQueryData on it
// directly.
import { QueryCache, QueryClient } from "@tanstack/react-query"
import type { QueryKey } from "@tanstack/react-query"

import { ApiError } from "./api"

/** The retry rule: one more try for what may be a blip (the network,
 * a 5xx), none for an answer the server gave on purpose — a 401, 403
 * or 404 will not change by asking again, and the retry's backoff is
 * a second of spinner in front of the message the reader needs. */
export function noRetryOnRefusal(failures: number, err: unknown): boolean {
  if (err instanceof ApiError && err.status >= 400 && err.status < 500)
    return false
  return failures < 1
}

/** Each query's last failure, held until it answers: a query that never
 * answered goes back to pending, its error gone, for every read again
 * (a new observer, a refetch, the retry's second of backoff). */
const held = new WeakMap<object, string>()

/** heldError is the last failure of the query under key since it last
 * answered ("" when it has answered, or never failed): what a surface
 * says while the failed read is read again — never "checking" between
 * two failures, never before the first answer either. */
export function heldError(client: QueryClient, queryKey: QueryKey): string {
  const q = client.getQueryCache().find({ queryKey, exact: true })
  return q ? (held.get(q) ?? "") : ""
}

export const queryClient: QueryClient = new QueryClient({
  queryCache: new QueryCache({
    onSuccess: (_data, query) => {
      held.delete(query)
    },
    // A 401 on any query means the wall went up (or the token went
    // stale) after api/meta was cached: re-ask meta, whose refusal is
    // what brings the token prompt back (TokenWall).
    onError: (err, query) => {
      held.set(query, err.message)
      if (
        err instanceof ApiError &&
        err.status === 401 &&
        query.queryKey[0] !== "meta"
      )
        void queryClient.invalidateQueries({ queryKey: ["meta"] })
    },
  }),
  defaultOptions: {
    queries: {
      retry: noRetryOnRefusal,
      refetchOnWindowFocus: false,
    },
  },
})
