// The devtools panel build (WEFT-DEVTOOLS.md §5.1, S4.7): a separate
// Vite library-mode build, NOT a second entry of vite.config.ts —
// the app config is a TanStack Start SPA build that prerenders
// through a server bundle and cannot emit a self-contained custom
// element. One ESM entry, CSS inlined (the stylesheet is a TS string
// attached to the shadow root, so the artifact is panel.js alone),
// fonts from the system mono stack — no webfonts, the dock works
// offline (V2).
//
// __PANEL_STUDIO_VERSION__ is the studio module version the panel
// understands; panel.js refuses to render against a newer Studio
// (§5.1 Versioning). studio/panel_test.go pins that this string
// matches studio.Version.
import { readFileSync } from "node:fs"
import { defineConfig } from "vite"

// The version the Go module reports (studio/studio.go's Version) —
// read from the source so the panel can never drift from it silently.
const studioVersion = (() => {
  const go = readFileSync(new URL("../studio.go", import.meta.url), "utf8")
  const m = /^const Version = "([^"]+)"/m.exec(go)
  if (!m) throw new Error("vite.panel.config: studio.Version not found in studio.go")
  return m[1]
})()

// The Dv0 spike (§10) builds two candidates — vanilla TS (src/panel/
// main.ts) and Preact (src/panel/preact.tsx) — and the smaller gzip
// wins (§11 Q1). PANEL_CANDIDATE=preact builds the Preact one to
// dist/panel-spike (never shipped); the default build is the vanilla
// panel, whose output scripts/clean-dist.ts copies to ../dist/panel.
const candidate = process.env.PANEL_CANDIDATE ?? "vanilla"
const isPreact = candidate === "preact"

export default defineConfig({
  define: {
    __PANEL_STUDIO_VERSION__: JSON.stringify(studioVersion),
  },
  // The app's public/ files (favicon, fonts) are the SPA's; a library
  // build emits the bundle and nothing else.
  publicDir: false,
  // preact.tsx carries its own /** @jsxImportSource preact */ pragma;
  // the vanilla entry has no JSX at all.
  build: {
    outDir: isPreact ? "dist/panel-spike" : "dist/panel-tmp",
    emptyOutDir: true,
    sourcemap: false,
    minify: true,
    lib: {
      entry: isPreact
        ? new URL("./src/panel/preact.tsx", import.meta.url).pathname
        : new URL("./src/panel/main.ts", import.meta.url).pathname,
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
