// Test helper: render UI that links into the app router inside an
// in-memory router (the components under test use <Link>).
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter,
} from "@tanstack/react-router"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { render } from "@testing-library/react"

export async function renderWithRouter(ui: React.ReactNode) {
  const router = createRouter({
    routeTree: createRootRoute({
      component: () => <>{ui}</>,
    }).addChildren([]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  })
  await router.load()
  // A fresh query client per render: the subagent block's lazy
  // fetches must never retry into the next test.
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}
