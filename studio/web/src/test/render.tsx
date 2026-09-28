// Test helper: render UI that links into the app router inside an
// in-memory router (the components under test use <Link>).
import {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRouter,
} from "@tanstack/react-router"
import { render } from "@testing-library/react"

export async function renderWithRouter(ui: React.ReactNode) {
  const router = createRouter({
    routeTree: createRootRoute({
      component: () => <>{ui}</>,
    }).addChildren([]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  })
  await router.load()
  return render(<RouterProvider router={router} />)
}
