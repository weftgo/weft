// Render the real app — the generated route tree, the app shell, the
// one QueryClient — at a URL, in jsdom. What a page-level test needs
// that a component test cannot give: the route's own search parsing,
// its queries, and the shell around it.
import {
  RouterProvider,
  createMemoryHistory,
  createRouter,
} from "@tanstack/react-router"
import { cleanup, render } from "@testing-library/react"
import { StrictMode } from "react"
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

export function renderApp(url: string, opts: { strict?: boolean } = {}) {
  // One document, one root: a page rendered before (unmounted or not)
  // is cleaned up first, or the next render would reuse its root.
  cleanup()
  // The client is the app's module singleton: start every page clean.
  queryClient.clear()
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [url] }),
  })
  // The root route renders <html>/<body> (the shell, __root.tsx), so the
  // React root is the document, as in the app (TanStack Start hydrates
  // on document). Rendered into a <div> under <body>, React's
  // root listener on <body> loops forever in its event dispatch for any
  // event fired under <body> once a portal (the ⌘K dialog) has mounted
  // there.
  return {
    router,
    ...render(opts.strict ? <StrictMode><RouterProvider router={router} /></StrictMode> : <RouterProvider router={router} />, {
      container: document,
      baseElement: document.body,
    }),
  }
}
