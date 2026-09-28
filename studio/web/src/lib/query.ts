// One QueryClient for the SPA (plan §4.5). A module singleton: the
// app is a single-page tool with no SSR, so there is exactly one
// client per page load and router loaders can ensureQueryData on it
// directly.
import { QueryClient } from "@tanstack/react-query"

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
})
