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
  // The dev loop (plan §6): terminal 1 serves the API on :7331, this
  // dev server serves the app under /studio/ (run with
  // `bun run dev -- --base /studio/`) and proxies the API across, so
  // the app sees exactly the production path shape.
  server: {
    proxy: {
      "/studio/api": { target: "http://127.0.0.1:7331", changeOrigin: false },
    },
  },
  plugins: [
    tailwindcss(),
    tanstackStart({
      spa: {
        enabled: true,
        // The shell is prerendered at /runs — a real page. "/" would
        // hit the index route's redirect during prerender and yield an
        // empty shell the client then fails to hydrate.
        maskPath: "/runs",
        prerender: { outputPath: "index.html" },
      },
    }),
    viteReact(),
  ],
})

export default config
