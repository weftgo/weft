import { createRouter as createTanStackRouter } from "@tanstack/react-router"
import { routeTree } from "./routeTree.gen"

// The router's basepath comes from the shell's <base href>, which the
// Go handler rewrites to the mount per request (ADR 0018 §6) — the
// same bundle serves /studio/, /x/, or the root. The shell prerender
// runs where document does not exist; there the basepath is "".
function runtimeBasepath(): string {
  if (typeof document === "undefined") return ""
  return new URL(document.baseURI).pathname.replace(/\/$/, "")
}

export function getRouter() {
  const router = createTanStackRouter({
    routeTree,
    basepath: runtimeBasepath(),

    scrollRestoration: true,
    defaultPreload: "intent",
    defaultPreloadStaleTime: 0,
  })

  // hydrateStart unconditionally re-applies TSS_ROUTER_BASEPATH —
  // inlined at build time from vite's `base`, which for this app is
  // "./" because the real mount is only known at runtime. That update
  // ("." ) would clobber the basepath resolved above and every route
  // 404s, so basepath updates are dropped entirely: the mount never
  // changes during a page's life.
  const update = router.update.bind(router)
  router.update = (newOptions) => {
    if ("basepath" in newOptions) {
      const { basepath: _ignored, ...rest } = newOptions
      return update(rest)
    }
    return update(newOptions)
  }

  return router
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof getRouter>
  }
}
