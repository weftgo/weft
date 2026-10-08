// The devtools panel build (WEFT-DEVTOOLS.md §5.1, S4.7): a separate
// Vite library-mode build, NOT a second entry of vite.config.ts —
// the app config is a TanStack Start SPA build that prerenders
// through a server bundle and cannot emit a self-contained custom
// element. One ESM entry, CSS inlined (the stylesheet is a TS string
// attached to the shadow root, so the artifact is panel.js alone),
// fonts from the system mono stack — no webfonts, the dock works
// offline (V2).
//
// __PANEL_STUDIO_VERSION__ is the Studio version the panel
// understands — the framework module's one version, read from
// version/version.go (scripts/weft-version.ts); panel.js refuses to
// render against a newer Studio (§5.1 Versioning).
// studio/panel_test.go pins the stamp to version.Version.
import { readFileSync } from "node:fs"
import { gzipSync } from "node:zlib"
import { defineConfig } from "vite"
import type { Plugin } from "vite"
import { weftVersion } from "./scripts/weft-version.ts"
import { budgetTable } from "./scripts/panel-budget.ts"
import type { Ledger } from "./scripts/panel-budget.ts"

/** §5.1's budget: panel.js is ≤ 80 KiB gzip. */
const PANEL_GZIP_BUDGET = 80 * 1024

/** panelBudget fails the build — loudly, before anything is copied
 * into the committed dist — when the artifact is not the one file
 * §5.1 names or is over its budget (a shared lib module that starts
 * dragging the app's dependencies in shows up here as size). */
function panelBudget(): Plugin {
  return {
    name: "weft-panel-budget",
    generateBundle(_options, bundle) {
      const files = Object.keys(bundle)
      if (files.length !== 1 || files[0] !== "panel.js")
        this.error(`the panel build must emit panel.js alone, got: ${files.join(", ")}`)
      const out = bundle["panel.js"]
      // The panel has no runtime dependency (V1: vanilla TS, the Dv0
      // decision): it reaches the app's lib/ modules for their pure
      // halves, and lib/api.ts imports @tanstack/react-query at its top.
      // Tree-shaking drops that today; a side-effectful import, or a
      // helper that starts touching it, would pull React into every
      // host page — far under the size budget, so size cannot be the
      // guard. Any package code in the bundle fails the build.
      if (out.type === "chunk") {
        const deps = Object.entries(out.modules)
          .filter(([id, m]) => m.renderedLength > 0 && /[\\/]node_modules[\\/]/.test(id))
          .map(([id]) => id.replace(/^.*[\\/]node_modules[\\/]/, ""))
        if (deps.length)
          this.error(`panel.js must carry no package code (V1), got: ${deps.slice(0, 5).join(", ")}`)
      }
      const code = out.type === "chunk" ? out.code : String(out.source)
      const gz = gzipSync(code).length
      // The per-item table (plan phase 3): the ledger's items against
      // this build — printed, never a failure of its own.
      const ledger = JSON.parse(readFileSync(new URL("./panel-budget.json", import.meta.url), "utf8")) as Ledger
      for (const line of budgetTable({ ...ledger, cap: PANEL_GZIP_BUDGET }, gz).lines) console.log(line)
      if (gz > PANEL_GZIP_BUDGET)
        this.error(`panel.js is ${gz} bytes gzipped, over the ${PANEL_GZIP_BUDGET} budget (WEFT-DEVTOOLS §5.1)`)
    },
  }
}

// The version the Go module reports (version.Version, which
// studio.Version is) — read from the source so the panel can never
// drift from it silently.
const studioVersion = weftVersion()

// The Dv0 decision (§11 Q1, closed): vanilla TS won on gzip
// (7,187 vs Preact's 12,202 on the same surfaces), so the vanilla
// entry is the panel. The Preact candidate lived in the spike commit
// (f80f7e5) and is gone.

export default defineConfig({
  plugins: [panelBudget()],
  define: {
    __PANEL_STUDIO_VERSION__: JSON.stringify(studioVersion),
  },
  // The app's public/ files (favicon, fonts) are the SPA's; a library
  // build emits the bundle and nothing else.
  publicDir: false,
  build: {
    outDir: "dist/panel-tmp",
    emptyOutDir: true,
    sourcemap: false,
    minify: true,
    lib: {
      entry: new URL("./src/panel/main.ts", import.meta.url).pathname,
      formats: ["es"],
      name: "WeftDevtools",
      fileName: () => "panel.js",
    },
    // The panel shares lib/{api,live,events,format}.ts with the app
    // (V6) but nothing else: rollup bundles whatever the entry graph
    // reaches, and the vitest panel suite reads the built artifact
    // and fails it if a React component import leaked in.
    rollupOptions: {
      output: {
        // Library mode emits no chunk for a side-effect entry; the
        // assetFileNames keep any stray emission honest.
        assetFileNames: "panel[extname]",
      },
    },
  },
})
