// Render the real app — the generated route tree, the app shell, the
// one QueryClient — at a URL, in jsdom. What a page-level test needs
// that a component test cannot give: the route's own search parsing,
// its queries, and the shell around it.
import {
  RouterProvider,
  createMemoryHistory,
  createRouter,
} from "@tanstack/react-router"
import { render } from "@testing-library/react"
import { vi } from "vitest"

import { queryClient } from "@/lib/query"
import { routeTree } from "@/routeTree.gen"

/** jsdom lacks these; the shell and the tables call them. */
export function stubBrowser() {
  Element.prototype.scrollIntoView = vi.fn()
  window.matchMedia = vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }))
}

export function renderApp(url: string) {
  // The client is the app's module singleton: start every page clean.
  queryClient.clear()
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [url] }),
  })
  return { router, ...render(<RouterProvider router={router} />) }
}
