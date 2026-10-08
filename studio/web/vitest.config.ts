import path from "node:path"
import { fileURLToPath } from "node:url"
import { defineConfig } from "vitest/config"
import type { Plugin } from "vitest/config"

const root = path.dirname(fileURLToPath(import.meta.url))

/** devtoolsPackage resolves "@weftgo/devtools" and its subpaths for the
 * package suite (src/npm): by default the package's sources, whose
 * `import "./panel.js"` is the committed studio/dist/panel/panel.js —
 * the bytes /studio/panel.js serves; with WEFT_DEVTOOLS_PKG=1 (make
 * devtools-npm) the assembled npm/ package itself, every file as it
 * would be packed. */
function devtoolsPackage(): Plugin {
  const built = process.env.WEFT_DEVTOOLS_PKG === "1"
  const srcNpm = path.resolve(root, "src/npm") + path.sep
  return {
    name: "weft-devtools-package",
    enforce: "pre",
    resolveId(source, importer) {
      const m = /^@weftgo\/devtools(?:\/(react|vue|svelte|scope))?$/.exec(source)
      if (m) {
        const name = m.at(1) ?? "index"
        if (built) return path.resolve(root, `npm/${name}.js`)
        return path.resolve(root, name === "scope" ? "src/lib/scope.ts" : `src/npm/${name}.ts`)
      }
      if (source === "./panel.js" && importer?.startsWith(srcNpm))
        return path.resolve(root, "../dist/panel/panel.js")
      return null
    },
  }
}

export default defineConfig({
  plugins: [devtoolsPackage()],
  // jsdom for the component tests; the folding tests are pure and run
  // happily under it too.
  test: {
    environment: "jsdom",
    // enables @testing-library/react's auto-cleanup between tests
    globals: true,
  },
  resolve: {
    alias: { "@": path.resolve(root, "src") },
  },
})
