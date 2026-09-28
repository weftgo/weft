import { HeadContent, Scripts, createRootRoute } from "@tanstack/react-router"

import appCss from "../styles.css?url"

export const Route = createRootRoute({
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
    <main className="container mx-auto p-4 pt-16">
      <h1>404</h1>
      <p>The requested page could not be found.</p>
    </main>
  ),
  shellComponent: RootDocument,
})

// The theme bootstrap runs before paint so the first frame is already
// right (no flash). It is inline, so the Go handler's CSP carries its
// sha256 hash (plan §5.5). The localStorage key is the site's.
const themeBootstrap = `(function(){var t;try{t=localStorage.getItem('theme')}catch(e){}
var d=t==='dark'||(t!=='light'&&window.matchMedia&&matchMedia('(prefers-color-scheme:dark)').matches);
var h=document.documentElement;h.classList.toggle('dark',d);h.style.colorScheme=d?'dark':'light'})()`

function RootDocument({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <head>
        {/* Rewritten by the Go handler to the mount path per request
            (ADR 0018 §6); first in head so every relative URL after it
            resolves under the mount. clean-dist.ts keeps it first. */}
        <base href="/" />
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
