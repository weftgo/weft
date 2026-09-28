import { defineConfig } from "vite"
import { tanstackStart } from "@tanstack/react-start/plugin/vite"
import viteReact from "@vitejs/plugin-react"
import tailwindcss from "@tailwindcss/vite"

// SPA only (ADR 0018 §2): no SSR at runtime, no server functions or
// routes — Go is the only server. The SPA build still runs the server
// bundle once to prerender the shell, so vite's outDir stays inside
// this project; scripts/clean-dist.ts then copies the static client
// into ../dist, the committed, embedded tree (ADR 0018 §3).
//
// Relative asset URLs: the runtime base comes from the <base href> the
// Go handler writes into the shell per request (ADR 0018 §6), so the
// same bundle mounts at any prefix.
const config = defineConfig({
  resolve: { tsconfigPaths: true },
  base: "./",
  build: {
    sourcemap: false,
  },
  plugins: [
    tailwindcss(),
    tanstackStart({
      spa: {
        enabled: true,
        prerender: { outputPath: "index.html" },
      },
    }),
    viteReact(),
  ],
})

export default config
