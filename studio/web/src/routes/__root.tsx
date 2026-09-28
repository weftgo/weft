import {
  HeadContent,
  Link,
  Scripts,
  createRootRoute,
} from "@tanstack/react-router"
import { QueryClientProvider } from "@tanstack/react-query"

import { AppShell } from "@/components/studio/app-shell"
import { queryClient } from "@/lib/query"
import appCss from "../styles.css?url"

export const Route = createRootRoute({
  component: RootComponent,
  head: () => ({
    meta: [
      {
        charSet: "utf-8",
      },
      {
        name: "viewport",
        content: "width=device-width, initial-scale=1",
      },
      {
        title: "weft studio",
      },
    ],
    links: [
      {
        rel: "stylesheet",
        href: appCss,
      },
    ],
  }),
  notFoundComponent: () => (
    <div className="mx-auto max-w-md space-y-3 py-24 text-center">
      <p className="font-mono text-xs text-faint">404</p>
      <p className="text-sm">There is no page here.</p>
      <p className="text-xs text-muted-foreground">
        Studio has two places: the runs list and the agents page. A run link
        looks like <span className="font-mono">runs/&lt;id&gt;</span>.
      </p>
      <Link
        to="/runs"
        className="inline-block font-mono text-xs text-thread-ink hover:underline"
      >
        go to runs
      </Link>
    </div>
  ),
  shellComponent: RootDocument,
})

// The theme bootstrap runs before paint so the first frame is already
// right (no flash). It is inline, so the Go handler's CSP carries its
// sha256 hash (plan §5.5). The localStorage key is the site's.
const themeBootstrap = `(function(){var t;try{t=localStorage.getItem('theme')}catch(e){}
var d=t==='dark'||(t!=='light'&&window.matchMedia&&matchMedia('(prefers-color-scheme:dark)').matches);
var h=document.documentElement;h.classList.toggle('dark',d);h.style.colorScheme=d?'dark':'light'})()`

function RootComponent() {
  return (
    <QueryClientProvider client={queryClient}>
      <AppShell />
    </QueryClientProvider>
  )
}

// The base must be React-owned AND carry the runtime mount, or React
// (which manages <base> as a head singleton) deletes or rewrites it
// after hydration — breaking every later relative fetch on deep
// links. clean-dist.ts injects a parse-time <base href="/">; the Go
// handler rewrites it per request; hydration renders the same href it
// finds, so the element is adopted, not fought over.
function mountBase(): string {
  if (typeof document === "undefined") return "/" // the shell prerender
  return new URL(document.baseURI).pathname
}

function RootDocument({ children }: { children: React.ReactNode }) {
  return (
    // suppressHydrationWarning: the theme bootstrap mutates <html>'s
    // class/style before hydration (plan §5.5); the mismatch is
    // intentional and the attributes are not React-owned after that.
    <html lang="en" suppressHydrationWarning>
      <head>
        <base href={mountBase()} />
        <script dangerouslySetInnerHTML={{ __html: themeBootstrap }} />
        <link rel="icon" type="image/svg+xml" href="favicon.svg" />
        <link rel="stylesheet" href="fonts.css" />
        <HeadContent />
      </head>
      <body>
        {children}
        <Scripts />
      </body>
    </html>
  )
}
