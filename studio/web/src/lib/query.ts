// One QueryClient for the SPA (plan §4.5). A module singleton: the
// app is a single-page tool with no SSR, so there is exactly one
// client per page load and router loaders can ensureQueryData on it
// directly.
import { QueryCache, QueryClient } from "@tanstack/react-query"

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

export const queryClient: QueryClient = new QueryClient({
  queryCache: new QueryCache({
    // A 401 on any query means the wall went up (or the token went
    // stale) after api/meta was cached: re-ask meta, whose refusal is
    // what brings the token prompt back (TokenWall).
    onError: (err, query) => {
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
