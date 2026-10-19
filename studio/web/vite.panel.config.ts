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
import { defineConfig } from "vite"
import type { Plugin } from "vite"
import { weftVersion } from "./scripts/weft-version.ts"
import {
  budgetTable,
  gzipSize,
  normalizeStamp,
  readLedger,
} from "./scripts/panel-budget.ts"
import { analyzeMangle } from "./scripts/panel-mangle.ts"

/** §5.1's budget: panel.js is ≤ the ledger's cap in gzip bytes —
 * panel-budget.json's "cap", 96 KiB (98,304 B) since phase 7 (audit §10
 * decision 1, A5.4; 80 KiB before). The ledger is the one source: the
 * printed table, this build's failure and scripts/panel-asset.ts read it,
 * all through readLedger, which throws on a cap that is not a positive
 * integer (the check fails closed). */
const ledger = readLedger()
const PANEL_GZIP_BUDGET = ledger.cap

/** The member names panel.js mangles (P9-trim): computed from the
 * source on every build, never a committed list that could go stale. */
const mangle = analyzeMangle()

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
        this.error(
          `the panel build must emit panel.js alone, got: ${files.join(", ")}`
        )
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
          .filter(
            ([id, m]) =>
              m.renderedLength > 0 && /[\\/]node_modules[\\/]/.test(id)
          )
          .map(([id]) => id.replace(/^.*[\\/]node_modules[\\/]/, ""))
        if (deps.length)
          this.error(
            `panel.js must carry no package code (V1), got: ${deps.slice(0, 5).join(", ")}`
          )
      }
      const code = out.type === "chunk" ? out.code : String(out.source)
      // Measured with the version stamp normalized (normalizeStamp): a
      // version bump changes no ledger byte, and budget.test.ts measures
      // the committed panel.js the same way. The cap reads the same size.
      // gzipSize is a pure-JS deflate, not node:zlib: the host Node's
      // zlib differs across versions, so the ledger would not reproduce.
      const gz = gzipSize(normalizeStamp(code, studioVersion))
      // The per-item table (plan phase 3): the ledger's items against
      // this build — printed, never a failure of its own.
      const table = budgetTable(ledger, gz)
      for (const line of table.lines) console.log(line)
      // The only failure: budgetTable's over is gz > the cap (budget.test.ts pins it).
      if (table.over)
        this.error(
          `panel.js is ${gz} bytes gzipped, over the ${PANEL_GZIP_BUDGET} budget (WEFT-DEVTOOLS §5.1)`
        )
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

/** minifyCss squeezes a stylesheet's whitespace: line breaks and runs
 * of space to one, none around { } ; , > nor after a declaration's
 * colon, no ; before a }. The panel's stylesheet holds no quoted string those touch
 * (its content values are "\25C8", "\25B8 " and attr()), and a
 * selector's descendant space before a colon is kept. */
export function minifyCss(css: string): string {
  return css
    .replace(/\s+/g, " ")
    .replace(/ ?([{};,>]) ?/g, "$1")
    .replace(/: /g, ":")
    .replace(/;}/g, "}")
}

/** panelCss applies minifyCss to src/panel/styles.ts's PANEL_CSS
 * template at build time (P8-fix-a), its static text only — the
 * ${…} interpolations (tokens, the generated chip and tone rules)
 * pass through. The source keeps its readable form (and the tests that
 * read PANEL_CSS see it). */
function panelCss(): Plugin {
  return {
    name: "weft-panel-css",
    transform(code, id) {
      if (!id.endsWith("/src/panel/styles.ts")) return null
      const open = "export const PANEL_CSS = `"
      const a = code.indexOf(open)
      const b = code.indexOf("\n`", a)
      if (a < 0 || b < 0) this.error("styles.ts: PANEL_CSS template not found")
      const body = code
        .slice(a + open.length, b + 1)
        .split(/(\$\{[^}]*\})/)
        .map((s, i) => (i % 2 ? s : minifyCss(s)))
        .join("")
      return {
        code: code.slice(0, a + open.length) + body + code.slice(b + 1),
        map: null,
      }
    },
  }
}

export default defineConfig({
  plugins: [panelCss(), panelBudget()],
  // The app's "@/" alias: lib/experiment-body.ts (the replay bounds,
  // plan F1) imports its siblings through it.
  resolve: { alias: { "@": new URL("./src", import.meta.url).pathname } },
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
    // "oxc" (Vite 8's default); output.minify below makes it full.
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
        // The minify lever (plan A5.3.0, Decisions log 2026-10-11):
        // panel.js ships fully minified — whitespace, identifiers and
        // syntax. `build.minify: true` alone is not that: for a `lib`
        // build in the `es` format Vite 8 hands rolldown
        // `{ compress: true, mangle: true, codegen: false }`, which keeps
        // indentation and newlines (so a library's consumer can read it).
        // Vite spreads rollupOptions.output (the rolldown alias in Vite 8)
        // after its own defaults, so this `true` (oxc's full minifier,
        // codegen included) wins. No sourcemap is shipped
        // (`sourcemap: false` above): the bundle is unreadable in a host
        // page's devtools — the accepted cost; the source is
        // studio/web/src/panel.
        // P9-trim's property lever: oxc's full minifier (what `true`
        // is: compress, mangle, codegen) plus mangleProps over the names
        // scripts/panel-mangle.ts proves are the bundle's own — the
        // element's and the internal classes' members, PanelState's —
        // never a name a response, the DOM, storage, a host's options or
        // the host API carries; the libs' short names are never emitted.
        minify: {
          compress: true,
          mangle: true,
          codegen: true,
          mangleProps: {
            include: new RegExp(`^(?:${mangle.names.join("|")})$`),
            reserved: mangle.reserved,
          },
        },
        // The `/* @__PURE__ */` annotations Vite keeps for an es lib are
        // dropped (P8-fix-a, −116 B gzip): panel.js is a side-effect
        // bundle (it defines the element), never tree-shaken by a host's
        // bundler, so they serve no consumer. vite.npm.config.ts's thin
        // modules keep their comments (unminified, comments untouched);
        // the npm package's panel.js is these same bytes.
        comments: { legal: true, annotation: false, jsdoc: false },
      },
    },
  },
})
