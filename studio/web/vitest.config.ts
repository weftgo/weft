import path from "node:path"
import { fileURLToPath } from "node:url"
import { defineConfig } from "vitest/config"

const root = path.dirname(fileURLToPath(import.meta.url))

export default defineConfig({
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
