// The @weftgo/devtools build (plan C1): the npm package's ESM modules,
// the second output of the one panel build. vite.panel.config.ts
// emits panel.js — the side-effect bundle /studio/panel.js serves —
// and this config emits the thin modules beside it: index.js (mount,
// scope, open, close, toggle, on — through the registered element)
// and the react / vue / svelte helpers. Their `import "./panel.js"`
// stays external: the package ships the panel bundle itself, the same
// bytes, never a second copy of the panel's code.
// scripts/npm-package.ts assembles studio/web/npm from both outputs
// and the declarations (tsconfig.npm.json).
import { defineConfig } from "vite"
import type { Plugin } from "vite"

const src = new URL("./src/npm/", import.meta.url).pathname

/** The modules the package's thin entries may carry: their own and the
 * shared Scope serialiser. Anything else — the panel's sources, any
 * package — fails the build: the package has zero dependencies and one
 * copy of the panel. */
const ALLOWED = [/[\\/]src[\\/]npm[\\/][^\\/]+\.ts$/, /[\\/]src[\\/]lib[\\/]scope\.ts$/]

function thinEntries(): Plugin {
  return {
    name: "weft-npm-thin",
    resolveId(source, importer) {
      if (source === "./panel.js" && importer?.startsWith(src)) return { id: "./panel.js", external: true }
      return null
    },
    generateBundle(_options, bundle) {
      for (const [name, out] of Object.entries(bundle)) {
        if (out.type !== "chunk") this.error(`the npm build emits modules only, got ${name}`)
        const extra = Object.keys(out.modules).filter(
          (id) => out.modules[id].renderedLength > 0 && !ALLOWED.some((re) => re.test(id))
        )
        if (extra.length) this.error(`${name} must carry only src/npm and lib/scope.ts, got: ${extra.join(", ")}`)
      }
    },
  }
}

export default defineConfig({
  plugins: [thinEntries()],
  publicDir: false,
  build: {
    outDir: "dist/npm-tmp",
    emptyOutDir: true,
    sourcemap: false,
    // Readable on purpose: a few hundred lines a reader of node_modules
    // can audit. The panel bundle is the one the budget is about.
    minify: false,
    lib: {
      entry: {
        index: `${src}index.ts`,
        react: `${src}react.ts`,
        vue: `${src}vue.ts`,
        svelte: `${src}svelte.ts`,
      },
      formats: ["es"],
      fileName: (_format, name) => `${name}.js`,
    },
    rollupOptions: {
      output: {
        chunkFileNames: "[name].js",
      },
    },
  },
})
